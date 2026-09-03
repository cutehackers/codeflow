package protocol

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"codeflow/internal/contractharness"
	"codeflow/internal/secret"
)

// Transport defaults.
const (
	DefaultMaxMessageSizeBytes = int64(1 << 20)
	DefaultCallTimeout         = 30 * time.Second
	DefaultMaxInFlight         = 64
	defaultIdleProbeTimeout    = 2 * time.Second
	stderrTailBytes            = 8 << 10
	maxFrameHeaderBytes        = 8 << 10
)

var errFrameTooLarge = errors.New("content-length frame exceeds configured bound")

// Config configures one adapter subprocess connection.
type Config struct {
	BinPath string
	Args    []string
	Env     []string // nil means inherit the parent environment

	// MaxMessageSizeBytes caps the UTF-8 body in either direction. The
	// Content-Length header itself is bounded separately.
	MaxMessageSizeBytes int64

	// DefaultTimeout applies to calls whose ctx has no deadline.
	DefaultTimeout time.Duration

	// MaxInFlight bounds concurrent pending calls per connection.
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

// Spawn starts the adapter subprocess and completes initialize/capability
// negotiation before returning a usable connection.
func Spawn(ctx context.Context, cfg Config) (*Conn, error) {
	cfg = cfg.withDefaults()
	cmd := exec.Command(cfg.BinPath, cfg.Args...)
	cmd.Env = cfg.Env
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
	if err := validateCapabilities(vi.Capabilities, cfg); err != nil {
		c.Close()
		return nil, err
	}
	c.mu.Lock()
	c.version = vi
	c.initialized = true
	c.mu.Unlock()
	return c, nil
}

func validateCapabilities(caps Capabilities, cfg Config) error {
	missing := make([]string, 0, 5)
	if !caps.Cancellation {
		missing = append(missing, "cancellation")
	}
	if !caps.Progress {
		missing = append(missing, "progress")
	}
	if !caps.BatchAck {
		missing = append(missing, "batchAck")
	}
	if !caps.SnapshotOverlay {
		missing = append(missing, "snapshotOverlay")
	}
	if !caps.AnalysisMetadata {
		missing = append(missing, "analysisMetadata")
	}
	if len(missing) > 0 {
		return BadRequestError(fmt.Sprintf("adapter missing required capabilities: %s", strings.Join(missing, ", ")))
	}
	if caps.MaxMessageBytes > 0 && caps.MaxMessageBytes < cfg.MaxMessageSizeBytes {
		return BadRequestError(fmt.Sprintf("adapter maxMessageBytes=%d is below CORE bound %d", caps.MaxMessageBytes, cfg.MaxMessageSizeBytes))
	}
	if caps.MaxInFlight > 0 && caps.MaxInFlight < cfg.MaxInFlight {
		return BadRequestError(fmt.Sprintf("adapter maxInFlight=%d is below CORE bound %d", caps.MaxInFlight, cfg.MaxInFlight))
	}
	return nil
}

type reply struct {
	ok     bool
	result json.RawMessage
	err    *Error
}

type frame struct {
	env  *RequestEnvelope
	body []byte
}

// Conn is one adapter subprocess over Content-Length framed JSON-RPC stdio.
// It is safe for concurrent use. A single reader correlates responses by ID,
// while a bounded slot channel prevents unbounded pending work.
type Conn struct {
	cfg Config

	cmd   *exec.Cmd
	stdin io.WriteCloser

	ids idGen

	mu          sync.Mutex
	pending     map[string]chan *reply
	broken      error
	closed      bool
	initialized bool
	version     VersionInfo

	writeMu sync.Mutex
	slots   chan struct{}

	waitDone chan struct{}
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

// StderrTail returns the bounded, latest adapter stderr content.
func (c *Conn) StderrTail() string {
	c.stderrMu.Lock()
	defer c.stderrMu.Unlock()
	return secret.Redact(string(c.stderr)).Text
}

// Version returns the negotiated adapter information.
func (c *Conn) Version() VersionInfo {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.version
}

func (c *Conn) readLoop(r io.Reader) {
	br := bufio.NewReaderSize(r, 64<<10)
	for {
		body, err := readNextFrame(br, c.cfg.MaxMessageSizeBytes)
		if err != nil {
			if errors.Is(err, io.EOF) {
				c.markBroken(CrashedError(fmt.Sprintf(
					"adapter process exited (EOF on stdout); stderr tail: %s", c.StderrTail())))
			} else if errors.Is(err, errFrameTooLarge) {
				c.markBroken(CrashedError(fmt.Sprintf(
					"adapter emitted a frame exceeding maxMessageSizeBytes (%d)", c.cfg.MaxMessageSizeBytes)))
			} else {
				c.markBroken(CrashedError(fmt.Sprintf("stdout frame read error: %v", err)))
			}
			return
		}
		if len(bytes.TrimSpace(body)) == 0 {
			continue
		}

		var probe struct {
			JSONRPC *string          `json:"jsonrpc"`
			ID      *string          `json:"id"`
			Method  *string          `json:"method"`
			Params  json.RawMessage  `json:"params"`
			Result  *json.RawMessage `json:"result"`
			Error   *json.RawMessage `json:"error"`
		}
		if err := json.Unmarshal(body, &probe); err != nil || probe.JSONRPC == nil || *probe.JSONRPC != JSONRPCVersion {
			c.markBroken(CrashedError(fmt.Sprintf("malformed JSON-RPC from adapter: %q", clip(body, 256))))
			return
		}
		if probe.Method != nil {
			// Progress, diagnostics, and batch acknowledgements are notifications.
			if probe.ID != nil {
				c.markBroken(CrashedError("adapter notification unexpectedly carried a response id"))
				return
			}
			if err := validateNotificationShape(*probe.Method, probe.Params); err != nil {
				c.markBroken(CrashedError(fmt.Sprintf("invalid adapter notification: %v", err)))
				return
			}
			continue
		}

		if err := validateRPCResponseShape(probe.ID, probe.Result, probe.Error); err != nil {
			c.markBroken(CrashedError(fmt.Sprintf("protocol violation in response: %v", err)))
			return
		}
		var response rpcResponse
		if err := json.Unmarshal(body, &response); err != nil {
			c.markBroken(CrashedError(fmt.Sprintf("decode JSON-RPC response: %v", err)))
			return
		}
		rep := &reply{ok: response.Error == nil, result: response.Result}
		if response.Error != nil {
			rep.err = errorFromRPC(*response.Error)
		}
		if ch, ok := c.removePending(response.ID); ok {
			select {
			case ch <- rep:
			default:
			}
		}
		// Unknown IDs are dropped. This is the late-result rule after timeout
		// or cancellation and prevents stale output from reaching callers.
	}
}

func clip(b []byte, n int) string {
	if len(b) > n {
		return string(b[:n]) + "..."
	}
	return string(b)
}

// writeFrame writes one UTF-8 byte-counted Content-Length frame.
func writeFrame(w io.Writer, body []byte) error {
	header := []byte("Content-Length: " + strconv.Itoa(len(body)) + "\r\n\r\n")
	if err := writeAll(w, header); err != nil {
		return err
	}
	return writeAll(w, body)
}

func writeAll(w io.Writer, b []byte) error {
	for len(b) > 0 {
		n, err := w.Write(b)
		if n > 0 {
			b = b[n:]
		}
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}

// readFrame reads one frame from a fresh reader. Production connections use
// readNextFrame so buffered bytes are preserved between frames.
func readFrame(r io.Reader, max int64) ([]byte, error) {
	return readNextFrame(bufio.NewReaderSize(r, 64<<10), max)
}

func readNextFrame(br *bufio.Reader, max int64) ([]byte, error) {
	if max <= 0 {
		max = DefaultMaxMessageSizeBytes
	}
	contentLength := int64(-1)
	headerBytes := 0
	for {
		line, err := readHeaderLine(br)
		if err != nil {
			return nil, err
		}
		headerBytes += len(line)
		if headerBytes > maxFrameHeaderBytes {
			return nil, errFrameTooLarge
		}
		line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		if line == "" {
			break
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok || !strings.EqualFold(strings.TrimSpace(key), "Content-Length") {
			// Content-Type and other MIME-style headers are harmless. Unknown
			// headers are rejected to keep the boundary deterministic.
			if ok && strings.EqualFold(strings.TrimSpace(key), "Content-Type") {
				continue
			}
			return nil, fmt.Errorf("invalid frame header %q", clip([]byte(line), 256))
		}
		if contentLength >= 0 {
			return nil, fmt.Errorf("duplicate Content-Length header")
		}
		parsed, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		if err != nil || parsed < 0 {
			return nil, fmt.Errorf("invalid Content-Length %q", strings.TrimSpace(value))
		}
		contentLength = parsed
	}
	if contentLength < 0 {
		return nil, fmt.Errorf("frame missing Content-Length")
	}
	if contentLength > max {
		return nil, errFrameTooLarge
	}
	body := make([]byte, contentLength)
	if _, err := io.ReadFull(br, body); err != nil {
		return nil, err
	}
	return body, nil
}

func readHeaderLine(br *bufio.Reader) (string, error) {
	var acc []byte
	for {
		part, err := br.ReadSlice('\n')
		if len(acc)+len(part) > maxFrameHeaderBytes {
			return "", errFrameTooLarge
		}
		if len(part) > 0 {
			acc = append(acc, part...)
		}
		if err == nil {
			return string(acc), nil
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		return "", err
	}
}

func writeJSONRPCNotification(ctx context.Context, w io.Writer, method string, params any) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	if !isJSONObject(raw) {
		return BadRequestError("notification params must be an object")
	}
	body, err := json.Marshal(struct {
		JSONRPC string          `json:"jsonrpc"`
		Method  string          `json:"method"`
		Params  json.RawMessage `json:"params"`
	}{JSONRPCVersion, method, raw})
	if err != nil {
		return err
	}
	return writeFrame(w, body)
}

// readLimitedLine is kept for the old direct helper test. It is not used by
// the subprocess transport, which is Content-Length framed.
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

func (c *Conn) registerPending(id string) chan *reply {
	ch := make(chan *reply, 1)
	c.mu.Lock()
	c.pending[id] = ch
	c.mu.Unlock()
	return ch
}

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

// Ping performs initialize/capability negotiation. It remains named Ping at
// the Go seam for callers that predate the JSON-RPC initialize method.
func (c *Conn) Ping(ctx context.Context) (VersionInfo, error) {
	var vi VersionInfo
	if err := c.Call(ctx, OpPing, map[string]any{}, &vi); err != nil {
		return VersionInfo{}, err
	}
	return vi, nil
}

func (c *Conn) Call(ctx context.Context, op string, params any, result any) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if !Ops[op] {
		return BadRequestError(fmt.Sprintf("op %q not in protocol enum", op))
	}
	c.mu.Lock()
	initialized := c.initialized
	c.mu.Unlock()
	if !initialized && op != OpPing && op != OpInitialize {
		return BadRequestError("initialize/capability negotiation has not completed")
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
	if wait <= 0 {
		c.removePending(fr.env.ID)
		c.sendCancelHint(fr.env.ID)
		return TimeoutError(fmt.Sprintf("op %s deadline already expired", op))
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()

	select {
	case rep := <-ch:
		return finishCall(rep, result, op, params)
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

func finishCall(rep *reply, result any, op string, params any) error {
	if !rep.ok {
		if rep.err == nil {
			return AdapterInternalError("error response missing typed error")
		}
		return rep.err
	}
	sanitized, _, err := secret.RedactJSON(rep.result)
	if err != nil {
		return AdapterInternalError(fmt.Sprintf("sanitize adapter result: %v", err))
	}
	if isAnalysisOperation(op) {
		basis, epoch := analysisContext(params)
		if err := contractharness.ValidateAdapterAnalysis(sanitized, op, basis, epoch); err != nil {
			return BadRequestError(fmt.Sprintf("adapter analysis metadata rejected: %v", err))
		}
	}
	if result == nil {
		return nil
	}
	if err := json.Unmarshal(sanitized, result); err != nil {
		return AdapterInternalError(fmt.Sprintf("result does not fit caller type: %v", err))
	}
	return nil
}

func isAnalysisOperation(op string) bool {
	switch op {
	case OpDetect, OpHarvestCandidates, OpSlice:
		return true
	default:
		return false
	}
}

func analysisContext(params any) (string, int64) {
	var raw []byte
	switch p := params.(type) {
	case json.RawMessage:
		raw = p
	default:
		raw, _ = json.Marshal(p)
	}
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return "", -1
	}
	basis, _ := m["computedBasisId"].(string)
	epoch := int64(-1)
	if n, ok := m["workspaceEpoch"].(float64); ok {
		epoch = int64(n)
	}
	if snapshot, ok := m["snapshot"].(map[string]any); ok {
		if basis == "" {
			basis, _ = snapshot["computedBasisId"].(string)
		}
		if epoch < 0 {
			if n, ok := snapshot["workspaceEpoch"].(float64); ok {
				epoch = int64(n)
			}
		}
	}
	return basis, epoch
}

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
	env := &RequestEnvelope{
		JSONRPC: JSONRPCVersion,
		ID:      c.ids.next(),
		Method:  rpcMethodForOp(op),
		Params:  raw,
		V:       ProtocolVersion,
		Op:      op,
	}
	if err := ValidateRequest(env); err != nil {
		return nil, err
	}
	body, err := json.Marshal(env)
	if err != nil {
		return nil, BadRequestError(fmt.Sprintf("marshal envelope: %v", err))
	}
	if int64(len(body)) > c.cfg.MaxMessageSizeBytes {
		return nil, BadRequestError(fmt.Sprintf(
			"envelope of %d bytes exceeds maxMessageSizeBytes %d; rejected without sending",
			len(body), c.cfg.MaxMessageSizeBytes))
	}
	return &frame{env: env, body: body}, nil
}

func (c *Conn) sendCancelHint(id string) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.muBrokenLocked() {
		return
	}
	_ = writeJSONRPCNotification(context.Background(), c.stdin, "$/cancelRequest", map[string]any{"id": id})
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
	return writeFrame(c.stdin, fr.body)
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

// Shutdown requests graceful drain and then closes the connection regardless
// of the adapter response.
func (c *Conn) Shutdown(timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	_ = c.Call(ctx, OpShutdown, map[string]any{}, nil)
	return c.Close()
}

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

type idGen struct{ n atomic.Uint64 }

func (g *idGen) next() string { return fmt.Sprintf("cf-%06d", g.n.Add(1)) }
