// Package dartadapter owns the versioned, line-delimited JSON-RPC boundary to
// CodeFlow's Dart child process.  It deliberately has no Dart semantics: the
// only trust boundary here is a validated protocol response.
package dartadapter

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const ProtocolVersion = "1"

type EntryPoint struct {
	FlowID string `json:"flow_id"`
	Alias  string `json:"alias"`
	Anchor Anchor `json:"anchor"`
}

// SemanticFact is deliberately narrow. It is a source-derived fact for the
// selected graph slice, not an unbounded whole-repository interpretation.
type SemanticFact struct {
	Kind     string `json:"kind"`
	Subject  string `json:"subject"`
	Object   string `json:"object"`
	Proof    string `json:"proof"`
	SymbolID string `json:"symbol_id,omitempty"`
	Anchor   Anchor `json:"anchor"`
}
type Anchor struct {
	Path        string `json:"path"`
	LineStart   int    `json:"line_start"`
	LineEnd     int    `json:"line_end"`
	ByteStart   int    `json:"byte_start"`
	ByteEnd     int    `json:"byte_end"`
	FileHash    string `json:"file_hash"`
	Fingerprint string `json:"semantic_fingerprint"`
}
type Failure struct{ Code, Message string }

func (e *Failure) Error() string { return e.Code + ": " + e.Message }

type Client struct {
	cmd  *exec.Cmd
	in   io.WriteCloser
	out  *bufio.Reader
	mu   sync.Mutex
	next int
	dead bool
	done chan error
}

// Start runs the adapter with --stdio.  Command may contain a program followed
// by arguments; relative script paths are made repository-independent by the
// caller before reaching this boundary.
func Start(ctx context.Context, command string) (*Client, error) {
	parts, splitErr := splitCommand(command)
	if splitErr != nil {
		return nil, &Failure{"ADAPTER_UNAVAILABLE", splitErr.Error()}
	}
	if len(parts) == 0 {
		return nil, &Failure{"ADAPTER_UNAVAILABLE", "no Dart adapter command was configured"}
	}
	select {
	case <-ctx.Done():
		return nil, &Failure{"ADAPTER_TIMEOUT", ctx.Err().Error()}
	default:
	}
	// The child belongs to the Client/Core lifecycle, not to the context of the
	// request that happened to start it. Individual RPC calls still honor their
	// deadlines and abort the child on timeout; Shutdown owns normal teardown.
	cmd := exec.Command(parts[0], parts[1:]...)
	cmd.Args = append(cmd.Args, "--stdio")
	isolateProcessGroup(cmd)
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, &Failure{"ADAPTER_UNAVAILABLE", err.Error()}
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	return &Client{cmd: cmd, in: in, out: bufio.NewReader(out), done: done}, nil
}

func splitCommand(command string) ([]string, error) {
	var parts []string
	var current strings.Builder
	var quote rune
	escaped, started := false, false
	flush := func() {
		if started {
			parts = append(parts, current.String())
			current.Reset()
			started = false
		}
	}
	for _, character := range command {
		if escaped {
			current.WriteRune(character)
			escaped, started = false, true
			continue
		}
		if character == '\\' && quote != '\'' {
			escaped, started = true, true
			continue
		}
		if quote != 0 {
			if character == quote {
				quote = 0
				started = true
			} else {
				current.WriteRune(character)
				started = true
			}
			continue
		}
		if character == '\'' || character == '"' {
			quote, started = character, true
			continue
		}
		if character == ' ' || character == '\t' || character == '\n' || character == '\r' {
			flush()
			continue
		}
		current.WriteRune(character)
		started = true
	}
	if escaped || quote != 0 {
		return nil, fmt.Errorf("adapter command has an unfinished quote or escape")
	}
	flush()
	return parts, nil
}

func (c *Client) Initialize(ctx context.Context) error {
	var result struct {
		ProtocolVersion string   `json:"protocol_version"`
		Capabilities    []string `json:"capabilities"`
	}
	if err := c.call(ctx, "initialize", map[string]any{"protocol_version": ProtocolVersion}, &result); err != nil {
		return err
	}
	if result.ProtocolVersion != ProtocolVersion {
		return &Failure{"ADAPTER_INCOMPATIBLE", "adapter protocol " + result.ProtocolVersion + " is not supported"}
	}
	found := false
	for _, v := range result.Capabilities {
		if v == "discover_entry_points" {
			found = true
		}
	}
	if !found {
		return &Failure{"ADAPTER_INCOMPATIBLE", "adapter does not support discover_entry_points"}
	}
	return nil
}
func (c *Client) Discover(ctx context.Context, repo string) ([]EntryPoint, error) {
	var result struct {
		EntryPoints []EntryPoint `json:"entry_points"`
	}
	if err := c.call(ctx, "discoverEntryPoints", map[string]any{"repository": repo}, &result); err != nil {
		return nil, err
	}
	for _, p := range result.EntryPoints {
		// The adapter supplies structural location only. Core attaches and
		// verifies the authoritative manifest hash for the same observation.
		if (!strings.HasPrefix(p.FlowID, "route:/") && !strings.HasPrefix(p.FlowID, "system:")) || p.Anchor.Path == "" || p.Anchor.LineStart < 1 {
			return nil, &Failure{"ADAPTER_MALFORMED", "adapter returned an invalid route or system entry point"}
		}
	}
	return result.EntryPoints, nil
}
func (c *Client) RefineRouteFlow(ctx context.Context, repo, flowID string, paths []string) ([]SemanticFact, error) {
	return c.RefineRouteFlowWithAnalysisPaths(ctx, repo, flowID, paths, paths)
}

// RefineRouteFlowWithAnalysisPaths keeps the evidence slice for one flow
// separate from the bounded union used to initialize a shared Analyzer
// context. Multi-flow compilation passes the same union for every flow so
// package resolution is paid once without allowing facts from another flow's
// slice to leak into this result.
func (c *Client) RefineRouteFlowWithAnalysisPaths(ctx context.Context, repo, flowID string, paths, analysisPaths []string) ([]SemanticFact, error) {
	var result struct {
		Facts []SemanticFact `json:"facts"`
	}
	if err := c.call(ctx, "refineRouteFlow", map[string]any{"repository": repo, "flow_id": flowID, "paths": paths, "analysis_paths": analysisPaths}, &result); err != nil {
		return nil, err
	}
	for _, fact := range result.Facts {
		if fact.Kind == "" || fact.Subject == "" || fact.Anchor.Path == "" || fact.Anchor.ByteEnd <= fact.Anchor.ByteStart || !validProof(fact.Proof) {
			return nil, &Failure{"ADAPTER_MALFORMED", "adapter returned an invalid semantic fact"}
		}
		if (fact.Kind == "user_action" || fact.Kind == "call") && (fact.Proof != "resolved_ast" || fact.SymbolID == "") {
			return nil, &Failure{"ADAPTER_MALFORMED", "action and call facts require a resolved canonical symbol"}
		}
	}
	return result.Facts, nil
}

func validProof(proof string) bool {
	return proof == "resolved_ast" || proof == "framework_rule_v1" || proof == "contract_v1"
}
func (c *Client) Shutdown(ctx context.Context) error {
	c.mu.Lock()
	if c.dead {
		c.mu.Unlock()
		return nil
	}
	c.mu.Unlock()
	var ignored any
	err := c.call(ctx, "shutdown", map[string]any{}, &ignored)
	_ = c.in.Close()
	select {
	case <-ctx.Done():
		c.abort()
		return ctx.Err()
	case <-c.done:
	}
	return err
}
func (c *Client) call(ctx context.Context, method string, params any, result any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.dead {
		return &Failure{"ADAPTER_UNAVAILABLE", "adapter process has already been terminated"}
	}
	c.next++
	id := c.next
	request := map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}
	b, _ := json.Marshal(request)
	if _, err := c.in.Write(append(b, '\n')); err != nil {
		return &Failure{"ADAPTER_UNAVAILABLE", err.Error()}
	}
	line := make(chan struct {
		b   []byte
		err error
	}, 1)
	go func() {
		b, err := c.out.ReadBytes('\n')
		line <- struct {
			b   []byte
			err error
		}{b, err}
	}()
	select {
	case <-ctx.Done():
		// Best effort cancellation is intentionally sent before reporting timeout.
		cancel, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": "cancel", "params": map[string]any{"request_id": id}})
		_, _ = c.in.Write(append(cancel, '\n'))
		// A late response must never be correlated with a later request. The
		// process is single-use after deadline/cancellation, so its reader can
		// no longer corrupt this protocol stream.
		c.abortLocked()
		return &Failure{"ADAPTER_TIMEOUT", ctx.Err().Error()}
	case got := <-line:
		if got.err != nil {
			if ctx.Err() != nil {
				c.abortLocked()
				return &Failure{"ADAPTER_TIMEOUT", ctx.Err().Error()}
			}
			return &Failure{"ADAPTER_MALFORMED", "adapter closed stdout before a response"}
		}
		var response struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      int             `json:"id"`
			Result  json.RawMessage `json:"result"`
			Error   *struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(got.b, &response); err != nil || response.JSONRPC != "2.0" || response.ID != id {
			return &Failure{"ADAPTER_MALFORMED", "adapter returned an invalid JSON-RPC response"}
		}
		if response.Error != nil {
			return &Failure{response.Error.Code, response.Error.Message}
		}
		if len(response.Result) == 0 || json.Unmarshal(response.Result, result) != nil {
			return &Failure{"ADAPTER_MALFORMED", "adapter result does not match the declared contract"}
		}
		return nil
	}
}

func (c *Client) abort() { c.mu.Lock(); defer c.mu.Unlock(); c.abortLocked() }
func (c *Client) abortLocked() {
	if c.dead {
		return
	}
	c.dead = true
	_ = c.in.Close()
	if c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
	<-c.done
}

func Discover(ctx context.Context, command, repo string) ([]EntryPoint, error) {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
	}
	c, err := Start(ctx, command)
	if err != nil {
		return nil, err
	}
	completed := false
	defer func() {
		if !completed {
			c.abort()
			return
		}
		cleanup, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = c.Shutdown(cleanup)
	}()
	if err = c.Initialize(ctx); err != nil {
		return nil, err
	}
	entries, err := c.Discover(ctx, repo)
	if err == nil {
		completed = true
	}
	return entries, err
}

// RefineRouteFlow performs one bounded semantic pass over the graph-provided
// files. A timeout makes the child unusable, exactly like discovery.
func RefineRouteFlow(ctx context.Context, command, repo, flowID string, paths []string) ([]SemanticFact, error) {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		// Resolved-symbol analysis initializes the target package analyzer once.
		// Large Flutter workspaces need more than the discovery-only budget, but
		// this boundary remains finite and still kills the child on timeout.
		ctx, cancel = context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
	}
	c, err := Start(ctx, command)
	if err != nil {
		return nil, err
	}
	completed := false
	defer func() {
		if !completed {
			c.abort()
			return
		}
		cleanup, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = c.Shutdown(cleanup)
	}()
	if err = c.Initialize(ctx); err != nil {
		return nil, err
	}
	facts, err := c.RefineRouteFlow(ctx, repo, flowID, paths)
	if err == nil {
		completed = true
	}
	return facts, err
}
func AsFailure(err error) *Failure {
	var f *Failure
	if errors.As(err, &f) {
		return f
	}
	if err != nil {
		return &Failure{"ADAPTER_UNAVAILABLE", fmt.Sprint(err)}
	}
	return nil
}
