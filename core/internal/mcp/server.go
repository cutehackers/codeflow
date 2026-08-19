// Package mcp provides the two supported stdio MCP negotiations. It is a thin
// client of the repository Core; it never opens SQLite or starts analysis.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"codeflow/core/internal/runtime"
)

const (
	Modern = "2026-07-28"
	Legacy = "2025-11-25"
)

var tools = []map[string]any{{"name": "current", "description": "Current CodeFlow document"}, {"name": "diff", "description": "Current baseline delta"}, {"name": "step", "description": "One flow step"}, {"name": "unknowns", "description": "Current unknowns"}, {"name": "refresh", "description": "Reconcile current worktree"}, {"name": "open", "description": "Open FlowView"}}

type Server struct {
	Repo string
	HTTP *http.Client
}

func (s Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
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
func (s Server) call(ctx context.Context, req request) response {
	name, _ := req.Params["name"].(string)
	args, _ := req.Params["arguments"].(map[string]any)
	endpoint, method := "", http.MethodGet
	switch name {
	case "current":
		endpoint = "/api/v1/flows/ignored?id=" + escape(stringArg(args, "flow_id", "route:/signup"))
	case "diff":
		endpoint = "/api/v1/compare"
	case "unknowns":
		endpoint = "/api/v1/flows/ignored?id=" + escape(stringArg(args, "flow_id", "route:/signup"))
	case "step":
		endpoint = "/api/v1/flows/ignored?id=" + escape(stringArg(args, "flow_id", "route:/signup"))
	case "refresh":
		endpoint = "/api/v1/refresh"
		method = http.MethodPost
	case "open":
		endpoint = "/api/v1/flows/ignored?id=" + escape(stringArg(args, "flow_id", "route:/signup"))
	default:
		return errorResponse(req.ID, -32602, "UNKNOWN_TOOL", "use current, diff, step, unknowns, refresh, or open")
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
		envelope["data"] = findStep(envelope, stringArg(args, "step_id", ""))
	}
	if name == "open" {
		envelope["data"] = map[string]any{"view_url": envelope["view_url"]}
	}
	b, _ := json.Marshal(envelope)
	return response{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{"content": []map[string]string{{"type": "text", "text": string(b)}}, "structuredContent": envelope}}
}
func stringArg(a map[string]any, key, def string) string {
	if v, ok := a[key].(string); ok && v != "" {
		return v
	}
	return def
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
