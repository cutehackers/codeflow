package protocol

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"
)

// Transport defaults.
const (
	DefaultMaxMessageSizeBytes = int64(1 << 20)
	DefaultCallTimeout         = 30 * time.Second
	DefaultMaxInFlight         = 64
	defaultIdleProbeTimeout    = 2 * time.Second
	stderrTailBytes            = 8 << 10
)

// Config configures one adapter subprocess connection.
type Config struct {
	BinPath string
	Args    []string
	Env     []string // nil means inherit the parent environment

	// MaxMessageSizeBytes caps both directions of the NDJSON stream
	// (default 1 MiB). See Conn for the enforcement semantics.
	MaxMessageSizeBytes int64

	// DefaultTimeout applies to calls whose ctx has no deadline
	// (default 30s). Expiry surfaces E_TIMEOUT.
	DefaultTimeout time.Duration

	// MaxInFlight bounds concurrent pending calls per connection.
	// Exceeding it fails immediately with E_BACKPRESSURE (default 64).
	MaxInFlight int
}

func (c Config) withDefaults() Config {
	if c.MaxMessageSizeBytes <= 0 {
		c.MaxMessageSizeBytes = DefaultMaxMessageSizeBytes
	}
	if c.DefaultTimeout <= 0 {
		c.DefaultTimeout = DefaultCallTimeout
	}
	if c.MaxInFlight <= 0 {
		c.MaxInFlight = DefaultMaxInFlight
	}
	return c
}

// Spawn starts the adapter subprocess and performs the ping handshake,
// verifying version negotiation. It returns E_UNSUPPORTED_VERSION when
// the adapter reports a different major protocolVersion and E_CRASHED
// when the process cannot be started or dies during handshake.
func Spawn(ctx context.Context, cfg Config) (*Conn, error) {
	cfg = cfg.withDefaults()
	cmd := exec.Command(cfg.BinPath, cfg.Args...)
	cmd.Env = cfg.Env // nil → inherit os.Environ()
	cmd.WaitDelay = 2 * time.Second

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, CrashedError(fmt.Sprintf("stdin pipe: %v", err))
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, CrashedError(fmt.Sprintf("stdout pipe: %v", err))
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, CrashedError(fmt.Sprintf("stderr pipe: %v", err))
	}
	if err := cmd.Start(); err != nil {
		return nil, CrashedError(fmt.Sprintf("spawn %s: %v", cfg.BinPath, err))
	}

	c := newConn(cfg, cmd, stdin)
	c.start(stdout, stderr)

	vi, err := c.Ping(ctx)
	if err != nil {
		c.Close()
		return nil, err
	}
	if vi.ProtocolVersion != ProtocolVersion {
		c.Close()
		return nil, UnsupportedVersionError(fmt.Sprintf(
			"adapter %q speaks protocol v%d, CORE requires v%d",
			vi.AdapterVersion, vi.ProtocolVersion, ProtocolVersion))
	}
	return c, nil
}

type reply struct {
	ok     bool
	result json.RawMessage
	err    *Error
}

type frame struct {
	env  *RequestEnvelope
	line []byte // marshaled envelope including trailing newline
}

// Conn is one adapter subprocess over NDJSON stdio. It is safe for
// concurrent use; responses are correlated by request id via a single
// reader goroutine and a pending map.
//
// Wire decisions (documented per ticket):
//
//   - OUTBOUND oversize: an envelope whose marshaled size exceeds
//     MaxMessageSizeBytes is rejected with E_BAD_REQUEST before any
//     write, matching the contract's rule that oversized envelopes are
//     "rejected unread".
//
//   - INBOUND oversize: a line exceeding the limit means the peer broke
//     framing discipline (the offending message may be the reply to a
//     pending call and the stream cannot be re-aligned cheaply), so the
//     connection is marked broken and every pending call fails with
//     E_CRASHED — restartable, so Pool transparently respawns once.
//
//   - BACKPRESSURE: MaxInFlight bounds pending calls locally; acquiring
//     a slot fails immediately with retryable E_BACKPRESSURE instead of
//     queueing unboundedly.
//
//   - CANCEL: on ctx cancellation the call fails fast with E_CANCELLED,
//     then an advisory cancel control line {"v":1,"id":<id>,"op":"cancel"}
//     is written best-effort. That op is intentionally outside the
//     schema's oneOf set (the contract has no per-call cancel op);
//     correctness never depends on the adapter honoring it — its late
//     response, or an E_BAD_REQUEST for the unknown op, is dropped under
//     the unknown-id correlation rule.
type Conn struct {
	cfg Config

	cmd   *exec.Cmd
	stdin io.WriteCloser

	ids idGen

	mu      sync.Mutex
	pending map[string]chan *reply
	broken  error // sticky; set by reader/Close, never cleared
	closed  bool

	writeMu sync.Mutex // serializes frame writes
	slots   chan struct{}

	waitDone chan struct{} // closed when cmd.Wait returns
	stderrMu sync.Mutex
	stderr   []byte
}

func newConn(cfg Config, cmd *exec.Cmd, stdin io.WriteCloser) *Conn {
	return &Conn{
		cfg:      cfg,
		cmd:      cmd,
		stdin:    stdin,
		pending:  make(map[string]chan *reply),
		slots:    make(chan struct{}, cfg.MaxInFlight),
		waitDone: make(chan struct{}),
	}
}

func (c *Conn) start(stdout io.Reader, stderr io.Reader) {
	go func() {
		defer close(c.waitDone)
		_ = c.cmd.Wait()
	}()
	go func() {
		tail := &tailBuffer{c: c}
		_, _ = io.Copy(tail, stderr)
	}()
	go c.readLoop(stdout)
}

type tailBuffer struct{ c *Conn }

func (t tailBuffer) Write(p []byte) (int, error) {
	t.c.stderrMu.Lock()
	defer t.c.stderrMu.Unlock()
	t.c.stderr = append(t.c.stderr, p...)
	if len(t.c.stderr) > stderrTailBytes {
		t.c.stderr = t.c.stderr[len(t.c.stderr)-stderrTailBytes:]
	}
	return len(p), nil
}

// StderrTail returns up to the last 8 KiB of adapter stderr output,
// useful for diagnosing crashes behind conformance failures.
func (c *Conn) StderrTail() string {
	c.stderrMu.Lock()
	defer c.stderrMu.Unlock()
	return string(c.stderr)
}

// readLoop is the single reader goroutine: frames lines, enforces the
// inbound size cap, validates envelopes, dispatches replies by id.
func (c *Conn) readLoop(r io.Reader) {
	br := bufio.NewReaderSize(r, 64<<10)
	for {
		line, tooBig, err := readLimitedLine(br, c.cfg.MaxMessageSizeBytes)
		if err != nil {
			if errors.Is(err, io.EOF) {
				c.markBroken(CrashedError(fmt.Sprintf(
					"adapter process exited (EOF on stdout); stderr tail: %s", c.StderrTail())))
			} else {
				c.markBroken(CrashedError(fmt.Sprintf("stdout read error: %v", err)))
			}
			return
		}
		if tooBig {
			c.markBroken(CrashedError(fmt.Sprintf(
				"adapter emitted a line exceeding maxMessageSizeBytes (%d)", c.cfg.MaxMessageSizeBytes)))
			return
		}
		if len(line) == 0 {
			continue // tolerate blank lines
		}
		var wm struct {
			ID     string          `json:"id"`
			OK     *bool           `json:"ok"`
			Result json.RawMessage `json:"result"`
			Err    *Error          `json:"err"`
		}
		if err := json.Unmarshal(line, &wm); err != nil || wm.OK == nil {
			c.markBroken(CrashedError(fmt.Sprintf("malformed NDJSON from adapter: %q", clip(line, 256))))
			return
		}
		var result json.RawMessage
		var rawErr *json.RawMessage
		if wm.Result != nil {
			result = wm.Result
		}
		if wm.Err != nil {
			b, merr := json.Marshal(wm.Err)
			if merr != nil {
				c.markBroken(CrashedError(fmt.Sprintf("re-encode err: %v", merr)))
				return
			}
			raw := json.RawMessage(b)
			rawErr = &raw
		}
		if verr := validateResponseShape(&wm.ID, *wm.OK, ptrOrNil(result), rawErr); verr != nil {
			c.markBroken(CrashedError(fmt.Sprintf("protocol violation in response: %v", verr)))
			return
		}
		rep := &reply{ok: *wm.OK, result: result, err: wm.Err}
		if ch, ok := c.removePending(wm.ID); ok {
			select {
			case ch <- rep:
			default:
			}
		}
		// Unknown ids are dropped per the contract's correlation rule;
		// this covers late replies after timeout/cancel.
	}
}

func ptrOrNil(raw json.RawMessage) *json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	r := raw
	return &r
}

func clip(b []byte, n int) string {
	if len(b) > n {
		return string(b[:n]) + "..."
	}
	return string(b)
}

// readLimitedLine reads one newline-terminated line, enforcing limit.
// tooBig reports that the line exceeded limit (the connection must be
// considered broken); any other error (including EOF mid-line) is
// returned verbatim.
func readLimitedLine(r *bufio.Reader, limit int64) (line []byte, tooBig bool, err error) {
	var acc []byte
	for {
		chunk, err := r.ReadSlice('\n')
		total := int64(len(acc)) + int64(len(chunk))
		if total > limit {
			return nil, true, nil
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			acc = append(acc, chunk...)
			continue
		}
		if err != nil {
			return nil, false, err
		}
		if acc == nil {
			return trimEOL(chunk), false, nil
		}
		return trimEOL(append(acc, chunk...)), false, nil
	}
}

func trimEOL(b []byte) []byte {
	if len(b) > 0 && b[len(b)-1] == '\n' {
		b = b[:len(b)-1]
	}
	if len(b) > 0 && b[len(b)-1] == '\r' {
		b = b[:len(b)-1]
	}
	return b
}

// registerPending stores a channel for id and returns it.
func (c *Conn) registerPending(id string) chan *reply {
	ch := make(chan *reply, 1)
	c.mu.Lock()
	c.pending[id] = ch
	c.mu.Unlock()
	return ch
}

// removePending deletes id's entry under the lock; exactly one racer
// ever obtains the channel, guaranteeing single resolution even when a
// timeout/cancel races the reader goroutine.
func (c *Conn) removePending(id string) (chan *reply, bool) {
	c.mu.Lock()
	ch, ok := c.pending[id]
	delete(c.pending, id)
	c.mu.Unlock()
	return ch, ok
}

func (c *Conn) markBroken(reason *Error) {
	c.mu.Lock()
	if c.broken != nil || c.closed {
		c.mu.Unlock()
		return
	}
	c.broken = reason
	var doomed []chan *reply
	for id, ch := range c.pending {
		delete(c.pending, id)
		doomed = append(doomed, ch)
	}
	c.mu.Unlock()
	rep := &reply{ok: false, err: reason}
	for _, ch := range doomed {
		select {
		case ch <- rep:
		default:
		}
	}
}

// Broken returns the sticky failure that ended the connection, or nil
// while healthy. A closed connection always reports an error.
func (c *Conn) Broken() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.broken != nil {
		return c.broken
	}
	if c.closed {
		return CrashedError("connection closed")
	}
	return nil
}

// Ping performs one round trip and returns the negotiated versions.
func (c *Conn) Ping(ctx context.Context) (VersionInfo, error) {
	var vi VersionInfo
	if err := c.Call(ctx, OpPing, map[string]any{}, &vi); err != nil {
		return VersionInfo{}, err
	}
	return vi, nil
}

// Call sends one request and awaits the correlated response. params may
// be nil (sent as {}), json.RawMessage, or anything JSON-marshalable to
// an object; result may be nil. Errors carry typed protocol codes.
func (c *Conn) Call(ctx context.Context, op string, params any, result any) error {
	if !Ops[op] {
		return BadRequestError(fmt.Sprintf("op %q not in protocol enum", op))
	}

	select {
	case c.slots <- struct{}{}:
	default:
		return BackpressureError(fmt.Sprintf("%d calls already at MaxInFlight", c.cfg.MaxInFlight))
	}
	defer func() { <-c.slots }()

	fr, err := c.buildRequest(op, params)
	if err != nil {
		return err
	}

	ch := c.registerPending(fr.env.ID)
	if berr := c.brokenOrClosed(); berr != nil {
		c.removePending(fr.env.ID)
		return berr
	}
	if werr := c.writeFrame(fr); werr != nil {
		c.removePending(fr.env.ID)
		cerr := CrashedError(fmt.Sprintf("stdin write failed: %v", werr))
		c.markBroken(cerr)
		return cerr
	}

	wait := c.cfg.DefaultTimeout
	if d, ok := ctx.Deadline(); ok {
		wait = time.Until(d)
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()

	select {
	case rep := <-ch:
		return finishCall(rep, result)
	case <-timer.C:
		c.removePending(fr.env.ID)
		c.sendCancelHint(fr.env.ID)
		return TimeoutError(fmt.Sprintf("op %s exceeded %v", op, wait))
	case <-ctx.Done():
		c.removePending(fr.env.ID)
		c.sendCancelHint(fr.env.ID)
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return TimeoutError(fmt.Sprintf("op %s exceeded %v", op, wait))
		}
		return CancelledError(fmt.Sprintf("op %s: %v", op, ctx.Err()))
	}
}

func finishCall(rep *reply, result any) error {
	if !rep.ok {
		if rep.err == nil {
			return AdapterInternalError("error response missing err body")
		}
		return rep.err
	}
	if result == nil {
		return nil
	}
	if err := json.Unmarshal(rep.result, result); err != nil {
		return AdapterInternalError(fmt.Sprintf("result does not fit caller type: %v", err))
	}
	return nil
}

// buildRequest assigns the correlation id, marshals params (nil → {}),
// validates the shape, and rejects outbound oversize with E_BAD_REQUEST
// before any write.
func (c *Conn) buildRequest(op string, params any) (*frame, error) {
	var raw json.RawMessage
	switch p := params.(type) {
	case nil:
		raw = json.RawMessage("{}")
	case json.RawMessage:
		raw = p
	default:
		b, err := json.Marshal(p)
		if err != nil {
			return nil, BadRequestError(fmt.Sprintf("marshal params: %v", err))
		}
		raw = b
	}
	env := &RequestEnvelope{V: ProtocolVersion, ID: c.ids.next(), Op: op, Params: raw}
	if err := ValidateRequest(env); err != nil {
		return nil, err
	}
	line, err := json.Marshal(env)
	if err != nil {
		return nil, BadRequestError(fmt.Sprintf("marshal envelope: %v", err))
	}
	if int64(len(line))+1 > c.cfg.MaxMessageSizeBytes {
		return nil, BadRequestError(fmt.Sprintf(
			"envelope of %d bytes exceeds maxMessageSizeBytes %d; rejected without sending",
			len(line), c.cfg.MaxMessageSizeBytes))
	}
	return &frame{env: env, line: append(line, '\n')}, nil
}

// sendCancelHint writes the advisory cancel control line best-effort.
// See the Conn doc comment for the wire-decision rationale.
func (c *Conn) sendCancelHint(id string) {
	line := fmt.Sprintf(`{"v":%d,"id":%q,"op":"cancel","params":{}}`+"\n", ProtocolVersion, id)
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.muBrokenLocked() {
		return
	}
	_, _ = c.stdin.Write([]byte(line)) // advisory only; errors ignored
}

func (c *Conn) muBrokenLocked() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed || c.broken != nil
}

func (c *Conn) writeFrame(fr *frame) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if err := c.brokenOrClosed(); err != nil {
		return err
	}
	_, err := c.stdin.Write(fr.line)
	return err
}

func (c *Conn) brokenOrClosed() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return errors.New("connection closed")
	}
	if c.broken != nil {
		return c.broken
	}
	return nil
}

// Shutdown requests graceful drain (op shutdown) and then closes the
// connection regardless of the outcome.
func (c *Conn) Shutdown(timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	_ = c.Call(ctx, OpShutdown, map[string]any{}, nil)
	cancel()
	c.Close()
	return nil
}

// Close kills the subprocess and fails every pending call. Idempotent.
func (c *Conn) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	var doomed []chan *reply
	for id, ch := range c.pending {
		delete(c.pending, id)
		doomed = append(doomed, ch)
	}
	c.mu.Unlock()

	rep := &reply{ok: false, err: CrashedError("connection closed")}
	for _, ch := range doomed {
		select {
		case ch <- rep:
		default:
		}
	}
	_ = c.stdin.Close()
	if c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
	select {
	case <-c.waitDone:
	case <-time.After(3 * time.Second):
	}
	return nil
}

// idGen allocates monotonically increasing correlation ids.
type idGen struct {
	n atomic.Uint64
}

func (g *idGen) next() string {
	return fmt.Sprintf("cf-%06d", g.n.Add(1))
}
