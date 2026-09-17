// Package mcp implements the Model Context Protocol (MCP) server over stdio JSON-RPC
// exposing the 8 CodeFlow tools for agent collaboration.
package mcp

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"codeflow/internal/contractharness"
	"codeflow/internal/curator"
	"codeflow/internal/detect"
	"codeflow/internal/flowview"
	"codeflow/internal/fusion"
	"codeflow/internal/harvest"
	"codeflow/internal/protocol"
	"codeflow/internal/slicing"
	"codeflow/internal/storage"
)

var errMethodNotFound = errors.New("method not found")

// GenerateAuthToken generates a random per-run 32-byte hex token.
func GenerateAuthToken() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Config holds runtime configuration for the MCP server.
type Config struct {
	RepoRoot     string
	AuthToken    string
	DartAdapter  string
	AdapterSpec  string
	Language     string
	RequireToken bool

	// Compatibility hooks
	RuntimeObservationProvider any
	RuntimeObservationStore    any
	ObservationProvider        any
	ObservationStore           any
	RuntimeExecutor            any
	OneShotExecutor            any
}

// AdapterRegistry manages pooled adapter connections per target repository and language.
type AdapterRegistry struct {
	mu    sync.RWMutex
	pools map[string]*protocol.Pool // key: "absRepoRoot:language"
}

func newAdapterRegistry() *AdapterRegistry {
	return &AdapterRegistry{
		pools: make(map[string]*protocol.Pool),
	}
}

func (r *AdapterRegistry) get(key string) (*protocol.Pool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.pools[key]
	return p, ok
}

func (r *AdapterRegistry) set(key string, p *protocol.Pool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pools[key] = p
}

func (r *AdapterRegistry) closeAll() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, p := range r.pools {
		if p != nil {
			p.Close()
		}
	}
	r.pools = make(map[string]*protocol.Pool)
}

// Server handles MCP JSON-RPC requests over stdio.
type Server struct {
	cfg         Config
	registry    *AdapterRegistry
	storageMap  sync.Map // key: absRepoRoot -> *storage.Storage
	eventLogs   sync.Map // key: absRepoRoot -> *fusion.EventLog
	liveServers sync.Map // key: absRepoRoot -> *flowview.Server
	fv          *flowview.Server
	fvMu        sync.Mutex
	closeOnce   sync.Once
	closeErr    error
}

// NewServer creates a configured MCP Server ready for immediate stdio handshake.
func NewServer(cfg Config) (*Server, error) {
	if cfg.RequireToken && strings.TrimSpace(cfg.AuthToken) == "" {
		return nil, fmt.Errorf("auth token configuration is invalid")
	}
	return &Server{
		cfg:      cfg,
		registry: newAdapterRegistry(),
	}, nil
}

// Close releases server resources.
func (s *Server) Close() error {
	if s == nil {
		return nil
	}
	s.closeOnce.Do(func() {
		var closeErrors []error
		if s.registry != nil {
			s.registry.closeAll()
		}
		s.closeLiveServers()
		s.fvMu.Lock()
		if s.fv != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			if err := s.fv.Shutdown(ctx); err != nil {
				closeErrors = append(closeErrors, err)
			}
			cancel()
			s.fv = nil
		}
		s.fvMu.Unlock()
		if len(closeErrors) > 0 {
			s.closeErr = errors.Join(closeErrors...)
		}
	})
	return s.closeErr
}

func (s *Server) resolveTarget(targetArg any) string {
	target := s.cfg.RepoRoot
	if sub, ok := targetArg.(string); ok && strings.TrimSpace(sub) != "" && sub != "." {
		if filepath.IsAbs(sub) {
			target = sub
		} else if target != "" {
			target = filepath.Join(target, sub)
		} else {
			target = sub
		}
	}
	if target == "" {
		target = "."
	}
	abs, err := filepath.Abs(target)
	if err != nil {
		return target
	}
	return abs
}

func (s *Server) getStorage(repoRoot string) (*storage.Storage, error) {
	absRoot := repoRoot
	if !filepath.IsAbs(absRoot) {
		absRoot = s.resolveTarget(repoRoot)
	}
	if val, ok := s.storageMap.Load(absRoot); ok {
		return val.(*storage.Storage), nil
	}
	st := storage.New(absRoot)
	if err := st.InitLayout(); err != nil {
		return nil, fmt.Errorf("init storage layout in %s: %w", absRoot, err)
	}
	s.storageMap.Store(absRoot, st)
	return st, nil
}

func (s *Server) getEventLog(repoRoot string) *fusion.EventLog {
	absRoot := repoRoot
	if !filepath.IsAbs(absRoot) {
		absRoot = s.resolveTarget(repoRoot)
	}
	if val, ok := s.eventLogs.Load(absRoot); ok {
		return val.(*fusion.EventLog)
	}
	el := fusion.NewEventLog(absRoot)
	s.eventLogs.Store(absRoot, el)
	return el
}

func (s *Server) getPoolAndRunners(ctx context.Context, repoRoot string, explicitLang string) (*protocol.Pool, *harvest.Runner, *slicing.Runner, error) {
	return s.getPoolAndRunnersForSnapshot(ctx, repoRoot, explicitLang, nil)
}

func (s *Server) getPoolAndRunnersForSnapshot(ctx context.Context, repoRoot string, explicitLang string, snapshot *protocol.Snapshot) (*protocol.Pool, *harvest.Runner, *slicing.Runner, error) {
	absRoot := repoRoot
	if !filepath.IsAbs(absRoot) {
		absRoot = s.resolveTarget(repoRoot)
	}

	lang := explicitLang
	if lang == "" {
		var det detect.Detection
		if snapshot != nil {
			det = detect.DetectSnapshot(snapshotFiles(snapshot))
		} else {
			det = detect.Detect(absRoot)
		}
		if det.Confident && det.Language != "" && det.Language != "unknown" {
			lang = det.Language
		} else if s.cfg.Language != "" {
			lang = s.cfg.Language
		} else {
			return nil, nil, nil, fmt.Errorf("unsupported project at %s: could not confidently detect project language. Supported languages: Dart, TypeScript/JavaScript. Remediation: ensure package.json or pubspec.yaml exists, or specify language explicitly", absRoot)
		}
	}

	spec := s.cfg.AdapterSpec
	if spec == "" && lang == "dart" {
		spec = s.cfg.DartAdapter
	}

	adapterCfg, err := harvest.ResolveAdapterForRepo(absRoot, "", lang, spec)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("CodeFlow Adapter Error: %s adapter could not be resolved for target %s.\nRemediation:\n- %v", lang, absRoot, err)
	}

	key := absRoot + ":" + lang
	pool, ok := s.registry.get(key)
	if !ok {
		pool = protocol.NewPool(adapterCfg, 2)
		s.registry.set(key, pool)
	}

	slicer := slicing.NewRunner(pool)
	harvester := harvest.NewRunnerWithPool(pool)
	return pool, harvester, slicer, nil
}

func snapshotFiles(snapshot *protocol.Snapshot) map[string]string {
	if snapshot == nil {
		return nil
	}
	if len(snapshot.Files) > 0 {
		return snapshot.Files
	}
	return snapshot.ContentOverlay
}

// JSON-RPC Request/Response structures
type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type rpcResponse struct {
	JSONRPC string    `json:"jsonrpc"`
	ID      any       `json:"id"`
	Result  any       `json:"result,omitempty"`
	Error   *rpcError `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

var errMCPUnauthenticated = errors.New("mcp authentication failed")

type mcpAuthenticationError struct {
	configured bool
}

func (e *mcpAuthenticationError) Error() string {
	if e == nil || !e.configured {
		return "unauthorized: auth token is not configured"
	}
	return "unauthorized: missing or invalid auth token"
}

func (*mcpAuthenticationError) Unwrap() error { return errMCPUnauthenticated }

// Serve reads JSON-RPC requests from in and writes responses to out until EOF.
func (s *Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)
	enc := json.NewEncoder(out)

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var req rpcRequest
		if err := json.Unmarshal(line, &req); err != nil {
			_ = enc.Encode(rpcResponse{
				JSONRPC: "2.0",
				ID:      nil,
				Error:   &rpcError{Code: -32700, Message: "Parse error"},
			})
			continue
		}

		if req.ID == nil || strings.HasPrefix(req.Method, "notifications/") {
			continue
		}

		resp := s.dispatchRequest(ctx, req)
		if err := enc.Encode(resp); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func (s *Server) dispatchRequest(ctx context.Context, req rpcRequest) rpcResponse {
	switch req.Method {
	case "initialize":
		return rpcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]any{
				"protocolVersion": "2024-11-05",
				"serverInfo": map[string]string{
					"name":    "codeflow-mcp",
					"version": "2.0",
				},
				"capabilities": map[string]any{
					"tools": map[string]any{},
				},
			},
		}

	case "ping":
		return rpcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  map[string]any{},
		}

	case "tools/list":
		return rpcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]any{
				"tools": s.listTools(),
			},
		}

	case "tools/call":
		var callParams struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &callParams); err != nil {
			return rpcResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error:   &rpcError{Code: -32602, Message: "Invalid tool call params"},
			}
		}

		var arguments map[string]any
		if len(callParams.Arguments) > 0 && string(callParams.Arguments) != "null" {
			if err := json.Unmarshal(callParams.Arguments, &arguments); err != nil {
				return rpcResponse{
					JSONRPC: "2.0",
					ID:      req.ID,
					Error:   &rpcError{Code: -32602, Message: "Invalid tool call params"},
				}
			}
		}
		res, err := s.executeTool(ctx, callParams.Name, arguments)
		if errors.Is(err, errMethodNotFound) {
			return rpcResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error:   &rpcError{Code: -32601, Message: fmt.Sprintf("Method %s not found", callParams.Name)},
			}
		}
		return mcpToolCallResponse(req.ID, res, err)

	default:
		return rpcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &rpcError{Code: -32601, Message: fmt.Sprintf("Method %s not found", req.Method)},
		}
	}
}

func mcpToolCallResponse(id any, res any, err error) rpcResponse {
	if err != nil {
		msg := err.Error()
		if strings.HasPrefix(strings.TrimSpace(msg), "{") && strings.Contains(msg, "\"code\"") {
			return rpcResponse{
				JSONRPC: "2.0",
				ID:      id,
				Result: map[string]any{
					"content": []map[string]string{
						{"type": "text", "text": msg},
					},
					"isError": true,
				},
			}
		}
		return rpcResponse{
			JSONRPC: "2.0",
			ID:      id,
			Result: map[string]any{
				"content": []map[string]string{
					{"type": "text", "text": fmt.Sprintf("Error: %v", err)},
				},
				"isError": true,
			},
		}
	}

	resJSON, marshalErr := json.Marshal(res)
	if marshalErr != nil {
		return rpcResponse{
			JSONRPC: "2.0",
			ID:      id,
			Result: map[string]any{
				"content": []map[string]string{
					{"type": "text", "text": fmt.Sprintf("Error: %v", marshalErr)},
				},
				"isError": true,
			},
		}
	}
	return rpcResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result: map[string]any{
			"content": []map[string]string{
				{"type": "text", "text": string(resJSON)},
			},
		},
	}
}

func (s *Server) listTools() []map[string]any {
	targetProp := map[string]any{
		"type":        "string",
		"description": "Target repository path or subdirectory (defaults to working directory)",
	}
	return []map[string]any{
		{
			"name":        "publish_core_flow",
			"description": "Publish a verified architecture-layer core flow from an agent-authored intermediate artifact. Verifies every anchor against the current worktree; on mismatch returns a correctable error without persisting.",
			"inputSchema": map[string]any{
				"type":     "object",
				"required": []string{"artifact"},
				"properties": map[string]any{
					"artifact": map[string]any{"$ref": "https://codeflow.local/schemas/core-artifact.schema.json"},
					"target":   targetProp,
					"token":    map[string]any{"type": "string", "description": "Auth token when RequireToken=true (FlowView server). Omitted in local dev."},
				},
			},
		},
		{
			"name":        "harvest_flows",
			"description": "Find candidate entry points for a natural-language flow request. To show a new FlowView, select an unambiguous matching candidate and call analyze_flow with its entrySymbolPath; this call alone does not create a flowId.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"target": targetProp,
					"query":  map[string]any{"type": "string", "description": "Optional case-insensitive substring filter across entrySymbolPath, intentSignals, markerKind, triggerClass"},
				},
			},
		},
		{
			"name":        "get_flow_payload",
			"description": "Retrieve FlowSpec JSON by flowId or entrySymbolPath",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"flowId":          map[string]any{"type": "string"},
					"entrySymbolPath": map[string]any{"type": "string"},
					"target":          targetProp,
				},
			},
		},
		{
			"name":        "analyze_flow",
			"description": "Slice and publish one exact entry point. Returns a persisted FlowSpec containing flowId; when the user requested a visual result, pass that exact flowId to open_review.",
			"inputSchema": map[string]any{
				"type":     "object",
				"required": []string{"entrySymbolPath"},
				"properties": map[string]any{
					"entrySymbolPath": map[string]any{"type": "string"},
					"target":          targetProp,
				},
			},
		},
		{
			"name":        "submit_flow_draft",
			"description": "Submit structured E2 session journey draft with verified anchors",
			"inputSchema": map[string]any{
				"type":     "object",
				"required": []string{"artifact"},
				"properties": map[string]any{
					"artifact": map[string]any{"type": "object"},
					"target":   targetProp,
					"token":    map[string]any{"type": "string"},
				},
			},
		},
		{
			"name":        "approve_step",
			"description": "Approve a step name and rules (E3 in-place approval)",
			"inputSchema": map[string]any{
				"type":     "object",
				"required": []string{"flowId", "symbolPath", "name"},
				"properties": map[string]any{
					"flowId":     map[string]any{"type": "string"},
					"symbolPath": map[string]any{"type": "string"},
					"name":       map[string]any{"type": "string"},
					"rules":      map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
					"target":     targetProp,
					"token":      map[string]any{"type": "string"},
				},
			},
		},
		{
			"name":        "report_unknowns",
			"description": "List unresolved gaps and unknowns in the workspace",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"flowId": map[string]any{"type": "string"},
					"target": targetProp,
				},
			},
		},
		{
			"name":        "open_review",
			"description": "Return the URL for a saved viewId or a persisted flowId. Open the returned URL immediately in the browser to show the result. A saved view is restored without analysis.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"viewId": map[string]any{"type": "string", "description": "Saved FlowView result ID. Open the returned URL immediately to show that exact result without analysis."},
					"flowId": map[string]any{"type": "string"},
					"target": targetProp,
				},
			},
		},
	}
}

func (s *Server) checkAuth(token any) error {
	if !s.cfg.RequireToken {
		return nil
	}
	if strings.TrimSpace(s.cfg.AuthToken) == "" {
		return &mcpAuthenticationError{}
	}
	tStr, _ := token.(string)
	if tStr != s.cfg.AuthToken {
		return &mcpAuthenticationError{configured: true}
	}
	return nil
}

func (s *Server) executeTool(ctx context.Context, name string, args map[string]any) (any, error) {
	switch name {
	case "publish_core_flow":
		return s.processPublishCoreFlow(ctx, args)
	case "harvest_flows":
		target := s.resolveTarget(args["target"])
		snapshot, releaseSnapshot, err := s.captureAnalysisSnapshot(ctx, target)
		if err != nil {
			return nil, err
		}
		defer releaseSnapshot()
		_, harvester, _, err := s.getPoolAndRunnersForSnapshot(ctx, target, "", &snapshot)
		if err != nil {
			return nil, err
		}
		candidates, err := harvester.RunWithSnapshot(ctx, target, snapshot)
		if err != nil {
			return nil, err
		}
		if qRaw, ok := args["query"].(string); ok && strings.TrimSpace(qRaw) != "" {
			q := strings.TrimSpace(qRaw)
			terms := strings.Fields(strings.ToLower(q))
			var filtered []harvest.Candidate
			for _, c := range candidates {
				fields := []string{
					strings.ToLower(c.EntrySymbolPath),
					strings.ToLower(c.IntentSignals.ClassName),
					strings.ToLower(c.IntentSignals.DerivedName),
					strings.ToLower(c.MarkerKind),
					strings.ToLower(c.TriggerClass),
				}
				if c.IntentSignals.DocLine != nil {
					fields = append(fields, strings.ToLower(*c.IntentSignals.DocLine))
				}
				matchedAll := true
				for _, term := range terms {
					found := false
					for _, f := range fields {
						if strings.Contains(f, term) {
							found = true
							break
						}
					}
					if !found {
						matchedAll = false
						break
					}
				}
				if matchedAll {
					filtered = append(filtered, c)
				}
			}
			candidates = filtered
		}
		return map[string]any{
			"count":      len(candidates),
			"candidates": candidates,
		}, nil

	case "get_flow_payload":
		flowID, _ := args["flowId"].(string)
		if flowID == "" {
			if entry, ok := args["entrySymbolPath"].(string); ok && entry != "" {
				flowID = fusion.ComputeFlowID(entry)
			}
		}
		if flowID == "" {
			return nil, fmt.Errorf("flowId or entrySymbolPath required")
		}

		target := s.resolveTarget(args["target"])
		st, err := s.getStorage(target)
		if err != nil {
			return nil, err
		}

		raw, err := st.ReadActiveFlowSpec(flowID)
		if err != nil {
			return nil, fmt.Errorf("flow not found: %w", err)
		}
		var spec fusion.FlowSpec
		if err := json.Unmarshal(raw, &spec); err != nil {
			return nil, err
		}
		if format, _ := args["format"].(string); format == "compact" || args["compact"] == true {
			compact := curator.BuildCompactPayload(&spec, nil, 0, 0)
			return compact, nil
		}
		return spec, nil

	case "analyze_flow":
		entry, _ := args["entrySymbolPath"].(string)
		if entry == "" {
			return nil, fmt.Errorf("entrySymbolPath required")
		}

		target := s.resolveTarget(args["target"])
		snapshot, releaseSnapshot, err := s.captureAnalysisSnapshot(ctx, target)
		if err != nil {
			return nil, err
		}
		defer releaseSnapshot()
		_, _, slicer, err := s.getPoolAndRunnersForSnapshot(ctx, target, "", &snapshot)
		if err != nil {
			return nil, err
		}
		st, err := s.getStorage(target)
		if err != nil {
			return nil, err
		}
		eventLog := s.getEventLog(target)

		h := sha256.Sum256([]byte(entry))
		candidateID := "cand-" + hex.EncodeToString(h[:8])

		sliced, err := slicer.SliceWithSnapshot(ctx, target, candidateID, entry, nil, snapshot)
		if err != nil {
			return nil, fmt.Errorf("slice error: %w", err)
		}

		approved, session, err := eventLog.MaterializeView()
		if err != nil {
			return nil, err
		}

		entryFile := entry
		if idx := strings.Index(entry, "#"); idx >= 0 {
			entryFile = entry[:idx]
		}
		basisSha := snapshotFingerprint(snapshot.Files, []string{entryFile})

		spec, err := fusion.Fuse(sliced, fusion.FuseOptions{
			SnapshotFiles:  snapshotContentBytes(snapshot),
			ApprovedLedger: approved,
			SessionDrafts:  session,
			BasisSha:       basisSha,
		})
		if err != nil {
			return nil, fmt.Errorf("fuse error: %w", err)
		}

		existingPtr, _ := st.ReadPointer()
		var existingIdx *storage.GenerationIndex
		if existingPtr != nil {
			existingIdx, _ = st.ReadLatestIndex()
		}

		sess, err := st.BeginGeneration(basisSha)
		if err != nil {
			return nil, err
		}
		defer sess.Discard()

		if existingIdx != nil && existingPtr != nil {
			for _, sum := range existingIdx.Flows {
				if sum.FlowID == spec.FlowID {
					continue
				}
				raw, err := st.ReadFlowSpec(existingPtr.GenerationID, sum.FlowID)
				if err != nil {
					continue
				}
				_ = sess.AddFlowSpec(sum.FlowID, raw, sum)
			}
		}

		specBytes, _ := json.Marshal(spec)
		if err := sess.AddFlowSpec(spec.FlowID, specBytes, storage.FlowSummary{
			FlowID:          spec.FlowID,
			Title:           spec.Title,
			EntrySymbolPath: entry,
			StepCount:       len(spec.Steps),
		}); err != nil {
			return nil, err
		}
		if err := sess.Commit(); err != nil {
			return nil, err
		}

		return spec, nil

	case "submit_flow_draft":
		if err := s.checkAuth(args["token"]); err != nil {
			return nil, err
		}
		artObj, ok := args["artifact"].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("artifact object required")
		}
		artBytes, err := json.Marshal(artObj)
		if err != nil {
			return nil, err
		}

		if err := contractharness.Validate(contractharness.BaseURL+"session-artifact.schema.json", artBytes); err != nil {
			return nil, fmt.Errorf("session-artifact schema validation failed: %w", err)
		}

		var artifact struct {
			ArtifactID       string `json:"artifactId"`
			SubmittedByAgent struct {
				Name      string `json:"name"`
				SessionID string `json:"sessionId"`
			} `json:"submittedByAgent"`
			JourneyDraft struct {
				FlowIDRef               string `json:"flowIdRef"`
				ProposedEntrySymbolPath string `json:"proposedEntrySymbolPath"`
				Steps                   []struct {
					Ordinal   int            `json:"ordinal"`
					Name      string         `json:"name"`
					Rationale string         `json:"rationale"`
					Anchor    slicing.Anchor `json:"anchor"`
				} `json:"steps"`
			} `json:"journeyDraft"`
		}
		if err := json.Unmarshal(artBytes, &artifact); err != nil {
			return nil, err
		}

		flowID := artifact.JourneyDraft.FlowIDRef
		if flowID == "" && artifact.JourneyDraft.ProposedEntrySymbolPath != "" {
			flowID = fusion.ComputeFlowID(artifact.JourneyDraft.ProposedEntrySymbolPath)
		}

		target := s.resolveTarget(args["target"])
		eventLog := s.getEventLog(target)

		for _, st := range artifact.JourneyDraft.Steps {
			_ = eventLog.Append(fusion.Event{
				Type:       fusion.EventSessionDraftSubmitted,
				FlowID:     flowID,
				SymbolPath: st.Anchor.EnclosingSymbolPath,
				Name:       st.Name,
				Rules:      []string{st.Rationale},
				Author:     artifact.SubmittedByAgent.Name,
			})
		}

		return map[string]any{
			"status":     "accepted",
			"artifactId": artifact.ArtifactID,
			"flowId":     flowID,
			"stepCount":  len(artifact.JourneyDraft.Steps),
		}, nil

	case "approve_step":
		if err := s.checkAuth(args["token"]); err != nil {
			return nil, err
		}
		flowID, _ := args["flowId"].(string)
		symbolPath, _ := args["symbolPath"].(string)
		nameVal, _ := args["name"].(string)

		var rules []string
		if rList, ok := args["rules"].([]any); ok {
			for _, r := range rList {
				if rStr, ok := r.(string); ok {
					rules = append(rules, rStr)
				}
			}
		}

		target := s.resolveTarget(args["target"])
		eventLog := s.getEventLog(target)

		err := eventLog.Append(fusion.Event{
			Type:       fusion.EventStepApproved,
			FlowID:     flowID,
			SymbolPath: symbolPath,
			Name:       nameVal,
			Rules:      rules,
			Author:     "human-approval",
		})
		if err != nil {
			return nil, err
		}

		return map[string]any{
			"status":     "approved",
			"flowId":     flowID,
			"symbolPath": symbolPath,
		}, nil

	case "report_unknowns":
		target := s.resolveTarget(args["target"])
		st, err := s.getStorage(target)
		if err != nil {
			return nil, err
		}

		flowID, _ := args["flowId"].(string)
		if flowID != "" {
			raw, err := st.ReadActiveFlowSpec(flowID)
			if err != nil {
				return nil, err
			}
			var spec fusion.FlowSpec
			_ = json.Unmarshal(raw, &spec)
			return spec.Unknowns, nil
		}
		idx, err := st.ReadLatestIndex()
		if err != nil {
			return nil, err
		}
		if idx == nil {
			return []fusion.Unknown{}, nil
		}
		var all []fusion.Unknown
		for _, f := range idx.Flows {
			raw, err := st.ReadFlowSpec(idx.GenerationID, f.FlowID)
			if err != nil {
				continue
			}
			var spec fusion.FlowSpec
			if json.Unmarshal(raw, &spec) == nil {
				all = append(all, spec.Unknowns...)
			}
		}
		if all == nil {
			all = []fusion.Unknown{}
		}
		return all, nil

	case "open_review":
		target := s.resolveTarget(args["target"])
		flowID, _ := args["flowId"].(string)
		fv, err := s.getLiveCoordinator(target)
		if err != nil {
			return nil, err
		}
		viewURL := fv.URL() + "&flow=" + url.QueryEscape(flowID)
		viewID, _ := args["viewId"].(string)
		if viewID != "" {
			if _, err := fv.RestoreTaskView(ctx, viewID); err != nil {
				return nil, err
			}
			viewURL = fv.TaskViewURL(viewID)
		} else if flowID != "" {
			restored, err := fv.RestoreFlowView(ctx, flowID)
			if err != nil {
				return nil, err
			}
			saved, err := fv.SaveTaskView(ctx, restored)
			if err != nil {
				return nil, err
			}
			if id, ok := saved["viewId"].(string); ok {
				viewID = id
				viewURL = fv.TaskViewURL(id)
			}
		}
		return map[string]any{
			"status":  "ready",
			"flowId":  flowID,
			"url":     viewURL,
			"viewUrl": viewURL,
			"viewId":  viewID,
			"token":   fv.AuthToken(),
		}, nil

	default:
		return nil, fmt.Errorf("%w: %s", errMethodNotFound, name)
	}
}
