// Package mcp provides the two supported stdio MCP negotiations. It reuses a
// repository Core when available and can start one for the first MCP request.
package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"codeflow/core/internal/compiler"
	flowcore "codeflow/core/internal/core"
	"codeflow/core/internal/entrypoint"
	"codeflow/core/internal/runtime"
	"codeflow/core/internal/subgraph"
)

const (
	Modern = "2026-07-28"
	Legacy = "2025-11-25"
)

var tools = []map[string]any{
	{"name": "entry_points", "description": "Discover exact supported route and system entry points before preparing a business journey workspace", "inputSchema": noArguments()},
	{"name": "domain_subgraph", "description": "Extract end-to-end multi-step domain flows and business journeys (e.g. push token, payments, auth session, carts) using bidirectional graph traversal", "inputSchema": domainSubgraphArguments()},
	{"name": "prepare_workspace", "description": "Compile one to three exact route or system entry points into the same current-basis workspace for review or BusinessJourney registration", "inputSchema": workspaceArguments()},
	{"name": "workspace", "description": "Read the current same-basis workspace, including route and system-entry flows, before inspecting individual flows", "inputSchema": noArguments()},
	{"name": "business_journeys", "description": "List reviewer-approved BusinessJourney definitions in the prepared workspace", "inputSchema": noArguments()},
	{"name": "upsert_business_journey", "description": "Create or update one explicitly user-approved BusinessJourney using exact current flow and scenario IDs; Core rejects unsupported paths", "inputSchema": businessJourneyArguments()},
	{"name": "current", "description": "Read one current CodeFlow document", "inputSchema": flowArguments()},
	{"name": "diff", "description": "Read the current baseline delta", "inputSchema": noArguments()},
	{"name": "step", "description": "Read one causal step from one flow", "inputSchema": stepArguments()},
	{"name": "unknowns", "description": "Read the unresolved boundaries for one flow", "inputSchema": flowArguments()},
	{"name": "refresh", "description": "Reconcile the complete current flow set against one new Basis", "inputSchema": noArguments()},
	{"name": "open", "description": "Get the FlowView URL focused on one flow from the current workspace", "inputSchema": flowArguments()},
	{"name": "open_business_journey", "description": "Get the FlowView URL focused on one registered BusinessJourney", "inputSchema": businessJourneyIDArguments()},
}

func noArguments() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false}
}

func flowArguments() map[string]any {
	return map[string]any{
		"type":                 "object",
		"properties":           map[string]any{"flow_id": map[string]any{"type": "string", "pattern": `^(route:/|system:)`}},
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

func workspaceArguments() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"flow_ids": map[string]any{
				"type": "array", "minItems": 1, "maxItems": 3, "uniqueItems": true,
				"items": map[string]any{"type": "string", "pattern": `^(route:/|system:)`},
			},
		},
		"required": []string{"flow_ids"}, "additionalProperties": false,
	}
}

func businessJourneyIDArguments() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id": map[string]any{"type": "string", "pattern": `^[a-z][a-z0-9-]{0,63}$`},
		},
		"required": []string{"id"}, "additionalProperties": false,
	}
}

func businessJourneyArguments() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":      map[string]any{"type": "string", "pattern": `^[a-z][a-z0-9-]{0,63}$`},
			"title":   map[string]any{"type": "string", "minLength": 1, "maxLength": 140},
			"outcome": map[string]any{"type": "string", "maxLength": 200},
			"segments": map[string]any{
				"type": "array", "minItems": 1, "maxItems": 20,
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"flow_id":     map[string]any{"type": "string", "pattern": `^(route:/|system:)`},
						"scenario_id": map[string]any{"type": "string", "minLength": 1},
					},
					"required": []string{"flow_id", "scenario_id"}, "additionalProperties": false,
				},
			},
		},
		"required": []string{"id", "title", "segments"}, "additionalProperties": false,
	}
}

func domainSubgraphArguments() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{
				"type":        "string",
				"minLength":   1,
				"description": "Domain topic, concept, or user inquiry (e.g. '푸시 토큰 등록', 'payment checkout', 'auth session refresh')",
			},
			"depth": map[string]any{
				"type":        "integer",
				"minimum":     1,
				"maximum":     4,
				"default":     2,
				"description": "Traversal depth for caller/callee and causal hops",
			},
		},
		"required":             []string{"query"},
		"additionalProperties": false,
	}
}

type Server struct {
	Repo     string
	HTTP     *http.Client
	Start    func(context.Context, []string) (*flowcore.Core, *compiler.Problem, error)
	Discover func(context.Context) entrypoint.Result

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
	if args == nil {
		args = map[string]any{}
	}
	if name == "entry_points" {
		return s.entryPoints(ctx, req.ID)
	}
	if name == "domain_subgraph" {
		return s.domainSubgraph(ctx, req.ID, args)
	}
	if name == "prepare_workspace" {
		return s.prepareWorkspace(ctx, req.ID, args)
	}
	if name == "business_journeys" || name == "upsert_business_journey" || name == "open_business_journey" {
		return s.businessJourney(ctx, req.ID, name, args)
	}
	flowID, _ := args["flow_id"].(string)
	if name == "current" || name == "unknowns" || name == "step" || name == "open" {
		if !strings.HasPrefix(flowID, "route:/") && !strings.HasPrefix(flowID, "system:") {
			return errorResponse(req.ID, -32602, "FLOW_ID_REQUIRED", "pass the exact route:/... or system:... flow_id from workspace")
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
	envelope, failure := s.coreRequest(ctx, method, endpoint, nil)
	if failure != nil {
		return errorResponse(req.ID, -32010, failure.Code, failure.Message)
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

func (s *Server) entryPoints(ctx context.Context, id any) response {
	if s.Discover == nil {
		return errorResponse(id, -32010, "ENTRY_POINTS_UNAVAILABLE", "this MCP server cannot discover entry points")
	}
	result := s.Discover(ctx)
	status := string(result.State)
	unknowns := []any{}
	if result.Unknown != nil {
		unknowns = append(unknowns, result.Unknown)
	}
	data := map[string]any{"entry_points": result.Candidates}
	if result.EntryPoint != nil {
		data["entry_points"] = []entrypoint.EntryPoint{*result.EntryPoint}
	}
	envelope := map[string]any{"status": status, "data": data, "unknowns": unknowns}
	return toolResponse(id, "entry_points", envelope)
}

func (s *Server) domainSubgraph(ctx context.Context, id any, args map[string]any) response {
	query, _ := args["query"].(string)
	if strings.TrimSpace(query) == "" {
		return errorResponse(id, -32602, "QUERY_REQUIRED", "query argument is required")
	}
	depth := 2
	if d, ok := args["depth"].(float64); ok && d > 0 {
		depth = int(d)
	}
	res, err := subgraph.Extract(ctx, s.Repo, query, depth, nil, nil)
	if err != nil {
		return errorResponse(id, -32010, "DOMAIN_SUBGRAPH_FAILED", err.Error())
	}
	data := map[string]any{
		"topic":       res.Topic,
		"backend":     res.Backend,
		"nodes_count": len(res.Nodes),
		"edges_count": len(res.Edges),
		"nodes":       res.Nodes,
		"edges":       res.Edges,
		"journey":     res.Journey,
	}
	envelope := map[string]any{
		"status":   "observed",
		"data":     data,
		"unknowns": []any{},
	}
	return toolResponse(id, "domain_subgraph", envelope)
}

func (s *Server) prepareWorkspace(ctx context.Context, id any, args map[string]any) response {
	flowIDs, err := exactFlowIDs(args["flow_ids"])
	if err != nil {
		return errorResponse(id, -32602, "FLOW_IDS_REQUIRED", err.Error())
	}
	if _, err := runtime.ReadState(s.Repo); err != nil {
		if startup := s.ensureCore(ctx, flowIDs); startup != nil {
			return errorResponse(id, -32010, startup.Code, startup.Message)
		}
	}
	envelope, failure := s.coreRequest(ctx, http.MethodGet, "/api/v2/workspace", nil)
	if failure != nil {
		return errorResponse(id, -32010, failure.Code, failure.Message)
	}
	if !sameFlowScope(envelope, flowIDs) {
		return errorResponse(id, -32012, "WORKSPACE_SCOPE_MISMATCH", "the running Core has a different workspace scope; do not replace another active analysis")
	}
	compactMCPEnvelope(envelope)
	return toolResponse(id, "prepare_workspace", envelope)
}

func (s *Server) businessJourney(ctx context.Context, id any, name string, args map[string]any) response {
	if _, err := runtime.ReadState(s.Repo); err != nil {
		return errorResponse(id, -32010, "WORKSPACE_REQUIRED", "call prepare_workspace with exact flow_ids before working with BusinessJourneys")
	}
	endpoint, method := "/api/v1/business-journeys", http.MethodGet
	var body io.Reader
	if name == "upsert_business_journey" {
		payload, err := json.Marshal(args)
		if err != nil {
			return errorResponse(id, -32602, "BUSINESS_JOURNEY_INVALID", "BusinessJourney arguments could not be encoded")
		}
		endpoint, method, body = "/api/v1/business-journeys", http.MethodPut, bytes.NewReader(payload)
	}
	envelope, failure := s.coreRequest(ctx, method, endpoint, body)
	if failure != nil {
		return errorResponse(id, -32010, failure.Code, failure.Message)
	}
	if name == "open_business_journey" {
		journeyID, _ := args["id"].(string)
		if !journeyExists(envelope, journeyID) {
			return errorResponse(id, -32602, "BUSINESS_JOURNEY_NOT_FOUND", "pass an id returned by business_journeys")
		}
		envelope["data"] = map[string]any{"view_url": journeyViewURL(envelope, journeyID)}
	}
	compactMCPEnvelope(envelope)
	return toolResponse(id, name, envelope)
}

func (s *Server) coreRequest(ctx context.Context, method, endpoint string, body io.Reader) (map[string]any, *startupFailure) {
	state, err := runtime.ReadState(s.Repo)
	if err != nil {
		return nil, &startupFailure{Code: "CORE_UNAVAILABLE", Message: err.Error()}
	}
	h := s.HTTP
	if h == nil {
		h = http.DefaultClient
	}
	r, err := http.NewRequestWithContext(ctx, method, fmt.Sprintf("http://127.0.0.1:%d%s", state.Port, endpoint), body)
	if err != nil {
		return nil, &startupFailure{Code: "CORE_UNAVAILABLE", Message: err.Error()}
	}
	r.Header.Set("X-CodeFlow-Token", state.AuthToken)
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	res, err := h.Do(r)
	if err != nil {
		return nil, &startupFailure{Code: "CORE_UNAVAILABLE", Message: err.Error()}
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, &startupFailure{Code: "CORE_UNAVAILABLE", Message: err.Error()}
	}
	var envelope map[string]any
	if json.Unmarshal(raw, &envelope) != nil {
		return nil, &startupFailure{Code: "CORE_RESPONSE", Message: "Core returned malformed response"}
	}
	return envelope, nil
}

func exactFlowIDs(value any) ([]string, error) {
	items, ok := value.([]any)
	if !ok || len(items) == 0 || len(items) > 3 {
		return nil, fmt.Errorf("pass one to three exact route:/... or system:... flow_ids")
	}
	seen := map[string]bool{}
	flowIDs := make([]string, 0, len(items))
	for _, item := range items {
		flowID, ok := item.(string)
		if !ok || (!strings.HasPrefix(flowID, "route:/") && !strings.HasPrefix(flowID, "system:")) || seen[flowID] {
			return nil, fmt.Errorf("pass one to three unique exact route:/... or system:... flow_ids")
		}
		seen[flowID] = true
		flowIDs = append(flowIDs, flowID)
	}
	return flowIDs, nil
}

func sameFlowScope(envelope map[string]any, expected []string) bool {
	data, _ := envelope["data"].(map[string]any)
	actual, _ := data["flow_ids"].([]any)
	if len(actual) != len(expected) {
		return false
	}
	for i, flowID := range expected {
		if actual[i] != flowID {
			return false
		}
	}
	return true
}

func journeyExists(envelope map[string]any, id string) bool {
	data, _ := envelope["data"].([]any)
	for _, item := range data {
		journey, _ := item.(map[string]any)
		if journey["id"] == id {
			return true
		}
	}
	return false
}

func journeyViewURL(envelope map[string]any, id string) string {
	viewURL, _ := envelope["view_url"].(string)
	return viewURL + "?journey=" + url.QueryEscape(id)
}

func toolResponse(id any, name string, envelope map[string]any) response {
	return response{JSONRPC: "2.0", ID: id, Result: map[string]any{"content": []map[string]string{{"type": "text", "text": mcpSummary(name, envelope)}}, "structuredContent": envelope}}
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
		if name == "domain_subgraph" {
			topic, _ := data["topic"].(string)
			nodes, _ := data["nodes_count"].(int)
			edges, _ := data["edges_count"].(int)
			return fmt.Sprintf("CodeFlow domain_subgraph: topic=%q nodes=%d edges=%d status=%s", topic, nodes, edges, status)
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
