// Package mcp provides the two supported stdio MCP negotiations. It reuses a
// repository Core when available and can start one for the first MCP request.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"codeflow/core/internal/compiler"
	flowcore "codeflow/core/internal/core"
	"codeflow/core/internal/runtime"
)

const (
	Modern = "2026-07-28"
	Legacy = "2025-11-25"
)

var tools = []map[string]any{
	{"name": "workspace", "description": "Read the current same-basis screen-flow workspace before inspecting individual flows", "inputSchema": noArguments()},
	{"name": "current", "description": "Read one current CodeFlow document", "inputSchema": flowArguments()},
	{"name": "diff", "description": "Read the current baseline delta", "inputSchema": noArguments()},
	{"name": "step", "description": "Read one causal step from one flow", "inputSchema": stepArguments()},
	{"name": "unknowns", "description": "Read the unresolved boundaries for one flow", "inputSchema": flowArguments()},
	{"name": "refresh", "description": "Reconcile the complete current flow set against one new Basis", "inputSchema": noArguments()},
	{"name": "open", "description": "Get the FlowView URL focused on one flow from the current workspace", "inputSchema": flowArguments()},
}

func noArguments() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false}
}

func flowArguments() map[string]any {
	return map[string]any{
		"type":                 "object",
		"properties":           map[string]any{"flow_id": map[string]any{"type": "string", "pattern": `^route:/`}},
		"required":             []string{"flow_id"},
		"additionalProperties": false,
	}
}

func stepArguments() map[string]any {
	schema := flowArguments()
	schema["properties"].(map[string]any)["step_id"] = map[string]any{"type": "string", "minLength": 1}
	schema["required"] = []string{"flow_id", "step_id"}
	return schema
}

type Server struct {
	Repo  string
	HTTP  *http.Client
	Start func(context.Context, []string) (*flowcore.Core, *compiler.Problem, error)

	startMu sync.Mutex
	started *flowcore.Core
}

func (s *Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	defer s.closeStarted()
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 1024), 2<<20)
	first := true
	era := ""
	for scanner.Scan() {
		var req request
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			write(out, errorResponse(nil, -32700, "PARSE_ERROR", "request must be JSON"))
			continue
		}
		if first {
			first = false
			if req.Method != "initialize" {
				write(out, errorResponse(req.ID, -32001, "INITIALIZE_REQUIRED", "first request must be initialize"))
				continue
			}
			era = protocol(req.Params)
			if era == "" {
				write(out, errorResponse(req.ID, -32002, "UNSUPPORTED_PROTOCOL", "supported protocol versions: "+Legacy+", "+Modern))
				continue
			}
			write(out, response{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{"protocolVersion": era, "serverInfo": map[string]string{"name": "codeflow", "version": "1"}, "capabilities": map[string]any{"tools": map[string]any{}}}})
			continue
		}
		if era == "" {
			write(out, errorResponse(req.ID, -32001, "INITIALIZE_REQUIRED", "initialize with a supported protocol first"))
			continue
		}
		switch req.Method {
		case "tools/list":
			write(out, response{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{"tools": tools}})
		case "tools/call":
			write(out, s.call(ctx, req))
		default:
			write(out, errorResponse(req.ID, -32601, "METHOD_NOT_FOUND", "unsupported MCP method"))
		}
	}
	return scanner.Err()
}

type request struct {
	JSONRPC, Method string
	ID              any            `json:"id"`
	Params          map[string]any `json:"params"`
}
type response struct {
	JSONRPC string    `json:"jsonrpc"`
	ID      any       `json:"id"`
	Result  any       `json:"result,omitempty"`
	Error   *rpcError `json:"error,omitempty"`
}
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func protocol(p map[string]any) string {
	v, _ := p["protocolVersion"].(string)
	if v == Modern || v == Legacy {
		return v
	}
	return ""
}
func errorResponse(id any, code int, message, data string) response {
	return response{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: message, Data: map[string]string{"code": data}}}
}
func write(out io.Writer, v response) { b, _ := json.Marshal(v); _, _ = out.Write(append(b, '\n')) }
func (s *Server) call(ctx context.Context, req request) response {
	name, _ := req.Params["name"].(string)
	args, _ := req.Params["arguments"].(map[string]any)
	flowID, _ := args["flow_id"].(string)
	if name == "current" || name == "unknowns" || name == "step" || name == "open" {
		if !strings.HasPrefix(flowID, "route:/") {
			return errorResponse(req.ID, -32602, "FLOW_ID_REQUIRED", "pass the exact route:/... flow_id from workspace")
		}
	}
	stepID, _ := args["step_id"].(string)
	if name == "step" && strings.TrimSpace(stepID) == "" {
		return errorResponse(req.ID, -32602, "STEP_ID_REQUIRED", "pass a step_id returned by current")
	}
	endpoint, method := "", http.MethodGet
	switch name {
	case "workspace":
		endpoint = "/api/v2/workspace"
	case "current":
		endpoint = "/api/v1/flows/ignored?id=" + escape(flowID)
	case "diff":
		endpoint = "/api/v1/compare"
	case "unknowns":
		endpoint = "/api/v1/flows/ignored?id=" + escape(flowID)
	case "step":
		endpoint = "/api/v1/flows/ignored?id=" + escape(flowID)
	case "refresh":
		endpoint = "/api/v1/refresh"
		method = http.MethodPost
	case "open":
		endpoint = "/api/v1/flows/ignored?id=" + escape(flowID)
	default:
		return errorResponse(req.ID, -32602, "UNKNOWN_TOOL", "use workspace, current, diff, step, unknowns, refresh, or open")
	}
	selectors := []string{""}
	if flowID != "" {
		selectors = []string{flowID}
	}
	if startup := s.ensureCore(ctx, selectors); startup != nil {
		return errorResponse(req.ID, -32010, startup.Code, startup.Message)
	}
	state, err := runtime.ReadState(s.Repo)
	if err != nil {
		return errorResponse(req.ID, -32010, "CORE_UNAVAILABLE", err.Error())
	}
	h := s.HTTP
	if h == nil {
		h = http.DefaultClient
	}
	r, _ := http.NewRequestWithContext(ctx, method, fmt.Sprintf("http://127.0.0.1:%d%s", state.Port, endpoint), nil)
	r.Header.Set("X-CodeFlow-Token", state.AuthToken)
	res, err := h.Do(r)
	if err != nil {
		return errorResponse(req.ID, -32010, "CORE_UNAVAILABLE", err.Error())
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		return errorResponse(req.ID, -32010, "CORE_UNAVAILABLE", err.Error())
	}
	var envelope map[string]any
	if json.Unmarshal(raw, &envelope) != nil {
		return errorResponse(req.ID, -32011, "CORE_RESPONSE", "Core returned malformed response")
	}
	// Core's typed unavailable/unknown envelope is still useful MCP data. Only
	// a transport failure above becomes a JSON-RPC failure.
	if name == "unknowns" {
		envelope["data"] = envelope["unknowns"]
	}
	if name == "step" {
		envelope["data"] = findStep(envelope, stepID)
	}
	if name == "open" {
		envelope["data"] = map[string]any{"view_url": envelope["view_url"]}
	}
	compactMCPEnvelope(envelope)
	return response{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{"content": []map[string]string{{"type": "text", "text": mcpSummary(name, envelope)}}, "structuredContent": envelope}}
}

type startupFailure struct {
	Code, Message string
}

// ensureCore makes MCP usable immediately after installation. A requested
// flow supplies an exact selector; selector-less tools preserve compiler's
// unique-route requirement instead of guessing when several screens exist.
func (s *Server) ensureCore(ctx context.Context, selectors []string) *startupFailure {
	if _, err := runtime.ReadState(s.Repo); err == nil {
		return nil
	}
	s.startMu.Lock()
	defer s.startMu.Unlock()
	if _, err := runtime.ReadState(s.Repo); err == nil {
		return nil
	}
	if s.Start == nil {
		return &startupFailure{Code: "CORE_UNAVAILABLE", Message: "CodeFlow Core is not running and this MCP server cannot start it"}
	}
	startup, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	instance, problem, err := s.Start(startup, selectors)
	if err != nil {
		if err == runtime.ErrRunning {
			if _, stateErr := runtime.ReadState(s.Repo); stateErr == nil {
				return nil
			}
		}
		return &startupFailure{Code: "CORE_START_FAILED", Message: err.Error()}
	}
	if problem != nil {
		return &startupFailure{Code: problem.Code, Message: problem.Message}
	}
	s.started = instance
	return nil
}

func (s *Server) closeStarted() {
	s.startMu.Lock()
	instance := s.started
	s.started = nil
	s.startMu.Unlock()
	if instance == nil {
		return
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = instance.Close(shutdown)
}

// compactMCPEnvelope preserves snapshot identity and every requested causal
// fact while removing the full repository manifest duplicated at the envelope,
// document, and workspace-flow levels. Agents can use fingerprints to detect a
// basis change; source text remains available through step lenses and FlowView.
func compactMCPEnvelope(envelope map[string]any) {
	if basis, ok := envelope["basis"].(map[string]any); ok {
		if manifest, ok := basis["manifest"].([]any); ok {
			basis["manifest_count"] = len(manifest)
			delete(basis, "manifest")
		}
	}
	data, ok := envelope["data"].(map[string]any)
	if !ok {
		return
	}
	delete(data, "basis")
	if flows, ok := data["flows"].([]any); ok {
		for _, item := range flows {
			if flow, ok := item.(map[string]any); ok {
				delete(flow, "basis")
			}
		}
	}
}

func mcpSummary(name string, envelope map[string]any) string {
	status, _ := envelope["status"].(string)
	view, _ := envelope["view_url"].(string)
	fingerprint := ""
	if basis, ok := envelope["basis"].(map[string]any); ok {
		fingerprint, _ = basis["worktree_fingerprint"].(string)
	}
	flowID, steps := "", 0
	if data, ok := envelope["data"].(map[string]any); ok {
		if current, ok := data["current"].(map[string]any); ok {
			flowID, _ = current["id"].(string)
			if values, ok := current["steps"].([]any); ok {
				steps = len(values)
			}
		}
		if ids, ok := data["flow_ids"].([]any); ok {
			steps = len(ids)
		}
	}
	unknowns := 0
	if values, ok := envelope["unknowns"].([]any); ok {
		unknowns = len(values)
	}
	return fmt.Sprintf("CodeFlow %s: status=%s flow=%s items=%d unknowns=%d basis=%s view=%s", name, status, flowID, steps, unknowns, fingerprint, view)
}
func escape(s string) string { return strings.ReplaceAll(s, "/", "%2F") }
func findStep(e map[string]any, id string) any {
	d, _ := e["data"].(map[string]any)
	current, _ := d["current"].(map[string]any)
	steps, _ := current["steps"].([]any)
	for _, s := range steps {
		if x, ok := s.(map[string]any); ok && (id == "" || x["id"] == id) {
			return x
		}
	}
	return map[string]any{"status": "unknown", "message": "step not found"}
}
