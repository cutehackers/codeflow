package protocol

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"codeflow/internal/collector/contractharness"
	"codeflow/internal/collector/evidence"
	"codeflow/internal/collector/secret"
)

// Transport defaults.
const (
	DefaultMaxMessageSizeBytes = int64(1 << 20)
	// DefaultAdapterMessageSizeBytes is deliberately separate from the generic
	// protocol and model-host bounds. Analyzer requests currently carry complete
	// workspace snapshots, while model-host evidence packs remain bounded by
	// their own schemas.
	DefaultAdapterMessageSizeBytes = int64(128 << 20)
	DefaultCallTimeout             = 30 * time.Second
	DefaultMaxInFlight             = 64
	defaultIdleProbeTimeout        = 2 * time.Second
	stderrTailBytes                = 8 << 10
	maxFrameHeaderBytes            = 8 << 10
)

var errFrameTooLarge = errors.New("content-length frame exceeds configured bound")

// Config configures one adapter subprocess connection.
type Config struct {
	BinPath string
	Args    []string
	Env     []string // nil means inherit the parent environment

	// DisposableRoot optionally selects the parent directory used for a
	// process-private working directory. The adapter never runs with the
	// repository as its cwd. An empty value uses the host temporary directory.
	DisposableRoot string

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
		c.MaxMessageSizeBytes = DefaultAdapterMessageSizeBytes
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
	workDir, err := os.MkdirTemp(cfg.DisposableRoot, "codeflow-adapter-")
	if err != nil {
		return nil, CrashedError(fmt.Sprintf("create disposable adapter cwd: %v", err))
	}
	cleanup := func() { _ = os.RemoveAll(workDir) }
	cmd := exec.Command(cfg.BinPath, cfg.Args...)
	cmd.Dir = workDir
	cmd.Env = environmentWithDisposableTemp(cfg.Env, workDir)
	dependencyEnvironmentPreserved := sameNonDisposableEnvironment(cfg.Env, cmd.Env)
	cmd.WaitDelay = 2 * time.Second
	configureProcessGroup(cmd)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		cleanup()
		return nil, CrashedError(fmt.Sprintf("stdin pipe: %v", err))
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cleanup()
		return nil, CrashedError(fmt.Sprintf("stdout pipe: %v", err))
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		cleanup()
		return nil, CrashedError(fmt.Sprintf("stderr pipe: %v", err))
	}
	if err := cmd.Start(); err != nil {
		cleanup()
		return nil, CrashedError(fmt.Sprintf("spawn %s: %v", cfg.BinPath, err))
	}

	c := newConn(cfg, cmd, stdin, workDir, dependencyEnvironmentPreserved)
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

func environmentWithDisposableTemp(env []string, workDir string) []string {
	base := env
	if base == nil {
		base = os.Environ()
	} else {
		base = append([]string(nil), base...)
	}
	for _, key := range []string{"TMPDIR", "TMP", "TEMP", "PWD", "OLDPWD", "CODEFLOW_ADAPTER_WORKDIR"} {
		prefix := key + "="
		kept := base[:0]
		for _, entry := range base {
			if !strings.HasPrefix(entry, prefix) {
				kept = append(kept, entry)
			}
		}
		base = append(kept, key+"="+workDir)
	}
	return base
}

func sameNonDisposableEnvironment(before, after []string) bool {
	if before == nil {
		before = os.Environ()
	}
	values := func(entries []string) map[string]string {
		out := make(map[string]string, len(entries))
		for _, entry := range entries {
			key, value, ok := strings.Cut(entry, "=")
			if !ok || isDisposableEnvironmentKey(key) {
				continue
			}
			out[key] = value
		}
		return out
	}
	left, right := values(before), values(after)
	if len(left) != len(right) {
		return false
	}
	for key, value := range left {
		if right[key] != value {
			return false
		}
	}
	return true
}

func isDisposableEnvironmentKey(key string) bool {
	switch key {
	case "TMPDIR", "TMP", "TEMP", "PWD", "OLDPWD", "CODEFLOW_ADAPTER_WORKDIR":
		return true
	default:
		return false
	}
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

	waitDone                       chan struct{}
	workDir                        string
	workDirPermission              string
	cleanupOnce                    sync.Once
	isolationMu                    sync.RWMutex
	cleanupVerified                bool
	dependencyEnvironmentPreserved bool
	sourceDeliveryVerified         bool
	terminalModes                  []string
	stderrMu                       sync.Mutex
	stderr                         []byte
	stderrRedactor                 diagnosticStreamRedactor
}

func newConn(cfg Config, cmd *exec.Cmd, stdin io.WriteCloser, workDir string, dependencyEnvironmentPreserved bool) *Conn {
	permission := ""
	if info, err := os.Stat(workDir); err == nil {
		permission = fmt.Sprintf("%04o", info.Mode().Perm())
	}
	return &Conn{
		cfg:                            cfg,
		cmd:                            cmd,
		stdin:                          stdin,
		workDir:                        workDir,
		workDirPermission:              permission,
		dependencyEnvironmentPreserved: dependencyEnvironmentPreserved,
		pending:                        make(map[string]chan *reply),
		slots:                          make(chan struct{}, cfg.MaxInFlight),
		waitDone:                       make(chan struct{}),
	}
}

// MountPermissionEvidence records the executable boundary used by an
// adapter/helper and the cleanup result for its disposable process area.
// SourceDelivery is protocol_snapshot_bytes when source never enters the
// subprocess through a repository path. SourceMount stays not_mounted for
// that transport mode and is intentionally explicit for registry consumers.
type MountPermissionEvidence struct {
	SourceDelivery                 string   `json:"sourceDelivery"`
	SourceMount                    string   `json:"sourceMount"`
	WorkingDirectoryMode           string   `json:"workingDirectoryMode"`
	WorkingDirectoryPermission     string   `json:"workingDirectoryPermission"`
	ReadOnlySource                 bool     `json:"readOnlySource"`
	Disposable                     bool     `json:"disposable"`
	RepositoryPathExposed          bool     `json:"repositoryPathExposed"`
	DependencyEnvironmentPreserved bool     `json:"dependencyEnvironmentPreserved"`
	CleanupVerified                bool     `json:"cleanupVerified"`
	TerminalModes                  []string `json:"terminalModes,omitempty"`
}

// MountPermissionEvidence returns the process isolation proof accumulated by
// this connection. The repository path is never used as the adapter cwd, and
// the source side is supplied through the analyzer protocol envelope.
func (c *Conn) MountPermissionEvidence() MountPermissionEvidence {
	if c == nil {
		return MountPermissionEvidence{}
	}
	c.isolationMu.RLock()
	defer c.isolationMu.RUnlock()
	sourceDelivery := ""
	sourceMount := "unproven"
	readOnlySource := false
	if c.sourceDeliveryVerified {
		sourceDelivery = "protocol_snapshot_bytes"
		sourceMount = "not_mounted"
		readOnlySource = true
	}
	return MountPermissionEvidence{
		SourceDelivery:                 sourceDelivery,
		SourceMount:                    sourceMount,
		WorkingDirectoryMode:           "process_private_disposable",
		WorkingDirectoryPermission:     c.workDirPermission,
		ReadOnlySource:                 readOnlySource,
		Disposable:                     c.workDir != "",
		RepositoryPathExposed:          false,
		DependencyEnvironmentPreserved: c.dependencyEnvironmentPreserved,
		CleanupVerified:                c.cleanupVerified,
		TerminalModes:                  append([]string(nil), c.terminalModes...),
	}
}

func (c *Conn) recordTerminalMode(mode string) {
	if c == nil || mode == "" {
		return
	}
	c.isolationMu.Lock()
	defer c.isolationMu.Unlock()
	for _, existing := range c.terminalModes {
		if existing == mode {
			return
		}
	}
	c.terminalModes = append(c.terminalModes, mode)
}

func (c *Conn) start(stdout io.Reader, stderr io.Reader) {
	go func() {
		_ = c.cmd.Wait()
		c.cleanupWorkDir()
		close(c.waitDone)
	}()
	go func() {
		tail := &tailBuffer{c: c}
		_, _ = io.Copy(tail, stderr)
		c.finalizeStderr()
	}()
	go c.readLoop(stdout)
}

func (c *Conn) cleanupWorkDir() {
	c.cleanupOnce.Do(func() {
		if c.workDir == "" {
			return
		}
		err := os.RemoveAll(c.workDir)
		c.isolationMu.Lock()
		c.cleanupVerified = err == nil
		c.isolationMu.Unlock()
	})
}

type tailBuffer struct{ c *Conn }

var diagnosticAssignmentPattern = regexp.MustCompile("(?is)(?:\\\"(?:(?:api[_-]?key|secret|token|password|credential|authorization|private[_-]?key|access[_-]?token|client[_-]?secret)[A-Za-z0-9_-]*|[A-Za-z][A-Za-z0-9_-]*(?:api[_-]?key|secret|token|password|credential|authorization|private[_-]?key|access[_-]?token|client[_-]?secret)[A-Za-z0-9_-]*)\\\"|(?:api[_-]?key|secret|token|password|credential|authorization|private[_-]?key|access[_-]?token|client[_-]?secret))\\s*[:=]\\s*[\\\"']?")

// Recognizes a credential key before its separator arrives. This prevents a
// long key from being truncated into the bounded lookbehind and losing the
// marker needed to discard its value on a later chunk.
var diagnosticCredentialKeyPattern = regexp.MustCompile("(?is)\\\"(?:(?:api[_-]?key|secret|token|password|credential|authorization|private[_-]?key|access[_-]?token|client[_-]?secret)[A-Za-z0-9_-]*|[A-Za-z][A-Za-z0-9_-]*(?:api[_-]?key|secret|token|password|credential|authorization|private[_-]?key|access[_-]?token|client[_-]?secret)[A-Za-z0-9_-]*)")

const (
	diagnosticStreamNone byte = iota
	diagnosticStreamKey
	diagnosticStreamQuoted
	diagnosticStreamUnquoted
)

const diagnosticStreamLookbehind = 128

// diagnosticStreamRedactor consumes stderr without retaining secret bytes.
// It keeps only a short possible-key suffix between chunks and enters a
// discard mode as soon as a credential assignment is recognized.
type diagnosticStreamRedactor struct {
	pending []byte
	mode    byte
	quote   byte
	escaped bool
}

func (r *diagnosticStreamRedactor) write(p []byte) []byte {
	data := make([]byte, 0, len(r.pending)+len(p))
	data = append(data, r.pending...)
	data = append(data, p...)
	r.pending = nil
	out := make([]byte, 0, len(data))
	for len(data) > 0 {
		if r.mode == diagnosticStreamKey {
			consumed := 0
			for consumed < len(data) {
				value := data[consumed]
				consumed++
				if value != ':' && value != '=' {
					continue
				}
				data = data[consumed:]
				for len(data) > 0 && (data[0] == ' ' || data[0] == '\t' || data[0] == '\r' || data[0] == '\n' || data[0] == '\f') {
					data = data[1:]
				}
				if len(data) > 0 && (data[0] == '\'' || data[0] == '"') {
					r.mode = diagnosticStreamQuoted
					r.quote = data[0]
					data = data[1:]
				} else {
					r.mode = diagnosticStreamUnquoted
				}
				break
			}
			if r.mode == diagnosticStreamKey {
				// The key is still incomplete. Drop it and wait for the
				// separator without retaining an unbounded diagnostic.
				break
			}
			continue
		}
		if r.mode != diagnosticStreamNone {
			consumed := 0
			for index, value := range data {
				consumed = index + 1
				if r.mode == diagnosticStreamQuoted {
					if r.escaped {
						r.escaped = false
						continue
					}
					if value == '\\' {
						r.escaped = true
						continue
					}
					if value == r.quote {
						r.mode = diagnosticStreamNone
						out = append(out, value)
						break
					}
					continue
				}
				if value == ',' || value == ';' || value == '}' || value == ']' || value == '\n' || value == '\r' {
					r.mode = diagnosticStreamNone
					out = append(out, value)
					break
				}
			}
			if r.mode != diagnosticStreamNone {
				// The value is still secret. The marker was emitted when the
				// assignment was recognized, so discard this entire chunk.
				break
			}
			data = data[consumed:]
			continue
		}

		match := diagnosticAssignmentPattern.FindIndex(data)
		if match != nil {
			out = append(out, data[:match[0]]...)
			out = append(out, []byte("***REDACTED***")...)
			assignment := data[match[0]:match[1]]
			if len(assignment) > 0 && (assignment[len(assignment)-1] == '\'' || assignment[len(assignment)-1] == '"') {
				r.mode = diagnosticStreamQuoted
				r.quote = assignment[len(assignment)-1]
			} else {
				r.mode = diagnosticStreamUnquoted
			}
			data = data[match[1]:]
			continue
		}
		if match := diagnosticCredentialKeyPattern.FindIndex(data); match != nil {
			out = append(out, data[:match[0]]...)
			out = append(out, []byte("***REDACTED***")...)
			r.mode = diagnosticStreamKey
			data = data[match[1]:]
			continue
		}

		if len(data) > diagnosticStreamLookbehind {
			cut := len(data) - diagnosticStreamLookbehind
			out = append(out, data[:cut]...)
			r.pending = append(r.pending, data[cut:]...)
		} else {
			r.pending = append(r.pending, data...)
		}
		break
	}
	return out
}

// flush is used only after the stderr reader reaches EOF. StderrTail must not
// call it because a diagnostic read can happen between two writes and would
// otherwise discard the key lookbehind needed to redact a split assignment.
func (r *diagnosticStreamRedactor) flush() []byte {
	if r.mode != diagnosticStreamNone {
		r.pending = nil
		return nil
	}
	clean := []byte(secret.Redact(string(r.pending)).Text)
	r.pending = nil
	return clean
}

func (t tailBuffer) Write(p []byte) (int, error) {
	t.c.stderrMu.Lock()
	defer t.c.stderrMu.Unlock()
	clean := t.c.stderrRedactor.write(p)
	combined := make([]byte, 0, len(t.c.stderr)+len(clean))
	combined = append(combined, t.c.stderr...)
	combined = append(combined, clean...)
	if len(combined) > stderrTailBytes {
		combined = combined[len(combined)-stderrTailBytes:]
	}
	t.c.stderr = append(t.c.stderr[:0], combined...)
	return len(p), nil
}

// StderrTail returns the bounded, latest adapter stderr content.
func (c *Conn) StderrTail() string {
	c.stderrMu.Lock()
	defer c.stderrMu.Unlock()
	// Do not finalize pending lookbehind here. A caller can inspect stderr
	// between writes, and clearing the suffix would make a credential key split
	// across chunks impossible to recognize. Pending bytes are intentionally
	// omitted until the stream supplies enough context. They are not retained
	// in the visible tail as raw diagnostics.
	return clip(c.stderr, stderrTailBytes)
}

func (c *Conn) finalizeStderr() {
	c.stderrMu.Lock()
	defer c.stderrMu.Unlock()
	pending := c.stderrRedactor.flush()
	if len(pending) == 0 {
		return
	}
	combined := make([]byte, 0, len(c.stderr)+len(pending))
	combined = append(combined, c.stderr...)
	combined = append(combined, pending...)
	if len(combined) > stderrTailBytes {
		combined = combined[len(combined)-stderrTailBytes:]
	}
	c.stderr = append(c.stderr[:0], combined...)
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
			c.markBroken(CrashedError(fmt.Sprintf("malformed JSON-RPC from adapter: %s", clip(body, 256))))
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
	clean, _, err := secret.RedactJSON(b)
	if err != nil {
		clean = []byte(secret.Redact(string(b)).Text)
	}
	if len(clean) > n {
		return string(clean[:n]) + "..."
	}
	return string(clean)
}

// writeFrame writes one UTF-8 byte-counted Content-Length frame.
func writeFrame(w io.Writer, body []byte) error {
	return writeFrameBounded(w, body, DefaultMaxMessageSizeBytes)
}

// writeFrameBounded applies the negotiated body limit before writing either
// header or body. Callers receive a typed bounded error and no partial frame
// is emitted when the body is too large.
func writeFrameBounded(w io.Writer, body []byte, max int64) error {
	if max <= 0 {
		max = DefaultMaxMessageSizeBytes
	}
	if int64(len(body)) > max {
		return BadRequestError(fmt.Sprintf(
			"outbound frame of %d bytes exceeds maxMessageSizeBytes %d",
			len(body), max))
	}
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
	return writeJSONRPCNotificationBounded(ctx, w, method, params, DefaultMaxMessageSizeBytes)
}

func writeJSONRPCNotificationBounded(ctx context.Context, w io.Writer, method string, params any, max int64) error {
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
	return writeFrameBounded(w, body, max)
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
	if reason == nil {
		reason = CrashedError("adapter connection became broken")
	}
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
	c.recordTerminalMode("crash")
	rep := &reply{ok: false, err: reason}
	for _, ch := range doomed {
		select {
		case ch <- rep:
		default:
		}
	}
	// A malformed frame, an oversized response, or a stdout failure can leave
	// the child blocked on stdin even though the reader has stopped. Tear down
	// the process here so a broken connection is never left running until a
	// pool happens to reclaim it.
	_ = c.closeWithError(reason)
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
	if isAnalysisOperation(op) {
		c.recordSnapshotDelivery(fr)
	}

	wait := c.cfg.DefaultTimeout
	if d, ok := ctx.Deadline(); ok {
		wait = time.Until(d)
	}
	if wait <= 0 {
		c.recordTerminalMode("timeout")
		c.removePending(fr.env.ID)
		c.sendCancelHint(fr.env.ID)
		err := TimeoutError(fmt.Sprintf("op %s deadline already expired", op))
		_ = c.closeWithError(err)
		return err
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()

	select {
	case rep := <-ch:
		err := finishCall(rep, result, op, params, fr.env.ID, c.cfg.MaxMessageSizeBytes)
		if err == nil && isAnalysisOperation(op) {
			c.recordTerminalMode("success")
		}
		return err
	case <-timer.C:
		c.recordTerminalMode("timeout")
		c.removePending(fr.env.ID)
		c.sendCancelHint(fr.env.ID)
		err := TimeoutError(fmt.Sprintf("op %s exceeded %v", op, wait))
		_ = c.closeWithError(err)
		return err
	case <-ctx.Done():
		c.removePending(fr.env.ID)
		c.sendCancelHint(fr.env.ID)
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			// A context deadline is a timeout even when its Done channel wins
			// the race with the local timer. Keep explicit caller cancellation
			// distinct so lifecycle evidence cannot misclassify a deadline.
			c.recordTerminalMode("timeout")
			err := TimeoutError(fmt.Sprintf("op %s exceeded %v", op, wait))
			_ = c.closeWithError(err)
			return err
		}
		c.recordTerminalMode("cancel")
		err := CancelledError(fmt.Sprintf("op %s: %v", op, ctx.Err()))
		_ = c.closeWithError(err)
		return err
	}
}

func (c *Conn) recordSnapshotDelivery(fr *frame) {
	if c == nil || fr == nil || fr.env == nil {
		return
	}
	var params map[string]any
	if err := json.Unmarshal(fr.env.Params, &params); err != nil {
		return
	}
	if params["schemaId"] != evidence.AnalyzerRequestSchemaID ||
		int64Value(params["schemaVersion"]) != evidence.SchemaVersion ||
		params["requestId"] != fr.env.ID || params["operation"] != fr.env.Op {
		return
	}
	if _, exposed := params["repoRoot"]; exposed {
		return
	}
	snapshot, ok := params["snapshot"].(map[string]any)
	if !ok {
		return
	}
	if _, ok := snapshot["files"].(map[string]any); !ok {
		return
	}
	c.isolationMu.Lock()
	c.sourceDeliveryVerified = true
	c.isolationMu.Unlock()
}

func finishCall(rep *reply, result any, op string, params any, requestID string, maxMessageBytes int64) error {
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
		request, err := analyzerRequestForResponse(requestID, op, params, maxMessageBytes)
		if err != nil {
			return BadRequestError(fmt.Sprintf("analyzer request v2 rejected: %v", err))
		}
		if err := contractharness.Validate(evidence.AnalyzerResultSchemaID, sanitized); err != nil {
			return BadRequestError(fmt.Sprintf("analyzer result v2 schema rejected: %v", err))
		}
		var envelope evidence.Result
		if err := json.Unmarshal(sanitized, &envelope); err != nil {
			return BadRequestError(fmt.Sprintf("decode analyzer result v2: %v", err))
		}
		if err := evidence.ValidateResult(request, envelope); err != nil {
			return BadRequestError(fmt.Sprintf("analyzer result v2 semantic validation failed: %v", err))
		}
		if err := validateAnalysisPayload(op, envelope.Payload); err != nil {
			return BadRequestError(fmt.Sprintf("analyzer result operation payload rejected: %v", err))
		}
		if result == nil {
			return nil
		}
		if envelopeResult, ok := result.(*evidence.Result); ok {
			*envelopeResult = envelope
			return nil
		}
		if err := json.Unmarshal(envelope.Payload, result); err != nil {
			return AdapterInternalError(fmt.Sprintf("operation payload does not fit caller type: %v", err))
		}
		return nil
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
	maxMessageBytes := c.cfg.MaxMessageSizeBytes
	if maxMessageBytes <= 0 {
		maxMessageBytes = DefaultAdapterMessageSizeBytes
	}
	id := c.ids.next()
	var raw json.RawMessage
	if isAnalysisOperation(op) {
		request, err := analyzerRequestForCall(id, op, params, maxMessageBytes)
		if err != nil {
			return nil, err
		}
		b, err := json.Marshal(request.Params())
		if err != nil {
			return nil, BadRequestError(fmt.Sprintf("marshal analyzer request v2: %v", err))
		}
		raw = b
	} else {
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
	}
	env := &RequestEnvelope{
		JSONRPC: JSONRPCVersion,
		ID:      id,
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
	if int64(len(body)) > maxMessageBytes {
		return nil, BadRequestError(fmt.Sprintf(
			"envelope of %d bytes exceeds maxMessageSizeBytes %d; rejected without sending",
			len(body), maxMessageBytes))
	}
	return &frame{env: env, body: body}, nil
}

func (c *Conn) sendCancelHint(id string) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.muBrokenLocked() {
		return
	}
	_ = writeJSONRPCNotificationBounded(context.Background(), c.stdin, "$/cancelRequest", map[string]any{"id": id}, c.cfg.MaxMessageSizeBytes)
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
	return writeFrameBounded(c.stdin, fr.body, c.cfg.MaxMessageSizeBytes)
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
	err := c.closeWithError(CrashedError("connection closed"))
	// A broken reader may have marked the connection closed and be finishing
	// its kill/Wait sequence concurrently with this caller. Always wait for
	// that terminal sequence before publishing cleanup evidence.
	if c != nil && c.waitDone != nil {
		select {
		case <-c.waitDone:
		case <-time.After(3 * time.Second):
		}
	}
	if c != nil {
		c.cleanupWorkDir()
	}
	return err
}

// closeWithError tears down the subprocess and resolves every still-pending
// call with the same terminal error. A timeout or cancellation therefore
// cannot leave sibling calls blocked on a pipe that is about to be killed.
func (c *Conn) closeWithError(reason *Error) error {
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
	rep := &reply{ok: false, err: reason}
	for _, ch := range doomed {
		select {
		case ch <- rep:
		default:
		}
	}
	if c.stdin != nil {
		_ = c.stdin.Close()
	}
	_ = terminateProcessGroup(c.cmd)
	if c.waitDone != nil {
		select {
		case <-c.waitDone:
		case <-time.After(3 * time.Second):
		}
	}
	// A stubborn child must not retain the disposable tree after the bounded
	// wait. The process is already killed above, and RemoveAll is recoverable on
	// Unix even if a pipe teardown is still completing.
	c.cleanupWorkDir()
	return nil
}

type idGen struct{ n atomic.Uint64 }

func (g *idGen) next() string { return fmt.Sprintf("cf-%06d", g.n.Add(1)) }
