// Package mcp implements the Model Context Protocol (MCP) server over stdio JSON-RPC
// exposing the 7 CodeFlow tools for agent collaboration (ticket 15, design §12).
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
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"codeflow/internal/contractharness"
	"codeflow/internal/detect"
	"codeflow/internal/flowview"
	"codeflow/internal/fusion"
	"codeflow/internal/harvest"
	"codeflow/internal/protocol"
	"codeflow/internal/rflscvs06"
	"codeflow/internal/semantic"
	"codeflow/internal/slicing"
	"codeflow/internal/storage"
)

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
	// ReleaseThresholdDecisions is trusted server-side configuration. Release
	// evaluation requests cannot supply or replace it.
	ReleaseThresholdDecisions semantic.ThresholdDecisionResolver

	// RuntimeObservationProvider and RuntimeObservationStore are trusted
	// server-side seams. Public requests carry only an observation identifier.
	// They are interface-valued at this boundary so existing deployments can
	// inject either the exported typed adapters or an equivalent implementation.
	RuntimeObservationProvider any
	RuntimeObservationStore    any
	ObservationProvider        any // compatibility alias for integrations
	ObservationStore           any // compatibility alias for integrations

	// RuntimeExecutor is an optional one-shot trusted_local executor hook. It
	// is intentionally not constructed from a caller-provided command.
	RuntimeExecutor      any
	OneShotExecutor      any // compatibility alias for integrations
	RuntimeExecutionSpec rflscvs06.RuntimeExecutionSpec
	RuntimeConsent       *rflscvs06.RuntimeConsent
	// ModelHostFactory creates one Core-supervised host per enrichment request.
	// The request owns and closes the returned host.
	ModelHostFactory protocol.ModelHostFactory
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
	cfg          Config
	approvalGate *semantic.ApprovalAccessGate
	// approvalHistoryBeforeQueryHook is a package-private test seam that runs
	// after authorization and immediately before the read-only history query.
	// Production servers leave it nil.
	approvalHistoryBeforeQueryHook func(context.Context) error
	// approvalWorkspaceID is derived from the Core-configured repository root
	// and is never accepted from a request.
	approvalWorkspaceID string
	proposalStore       semantic.ProposalStore
	registry            *AdapterRegistry
	storageMap          sync.Map // key: absRepoRoot -> *storage.Storage
	eventLogs           sync.Map // key: absRepoRoot -> *fusion.EventLog
	engines             sync.Map // key: absRepoRoot -> *workspace.SnapshotEngine
	semanticMaps        sync.Map // key: absRepoRoot + NUL + generation/basis alias
	liveServers         sync.Map // key: absRepoRoot -> *flowview.Server
	fv                  *flowview.Server
	fvMu                sync.Mutex
	modelHostMu         sync.Mutex
	// modelHostSpawnMu serializes the complete request-scoped factory
	// invocation. modelHostClosing is independent so Close can close
	// admission and cancel active requests without waiting for a spawn.
	modelHostSpawnMu   sync.Mutex
	modelHostClosing   atomic.Bool
	modelHostActive    map[uint64]context.CancelFunc
	modelHostNextID    uint64
	modelHostClosed    bool
	modelHostWG        sync.WaitGroup
	modelHostDrainDone chan struct{}
	modelHostSpawning  int
	modelHostSpawnDone chan struct{}
	closeOnce          sync.Once
	closeErr           error
}

func semanticMapCacheKey(targetRoot, id string) string {
	absTarget, err := filepath.Abs(targetRoot)
	if err != nil {
		absTarget = filepath.Clean(targetRoot)
	}
	if canonical, canonicalErr := filepath.EvalSymlinks(absTarget); canonicalErr == nil {
		absTarget = filepath.Clean(canonical)
	}
	return absTarget + "\x00" + id
}

func (s *Server) rememberSemanticMap(targetRoot string, mapIR *semantic.SemanticMapIR) {
	if s == nil || mapIR == nil {
		return
	}
	for _, id := range []string{mapIR.MapID, mapIR.GenerationID, mapIR.ComputedBasisID} {
		if id != "" {
			s.semanticMaps.Store(semanticMapCacheKey(targetRoot, id), mapIR)
		}
	}
	s.semanticMaps.Store(semanticMapCacheKey(targetRoot, "active"), mapIR)
}

func (s *Server) loadSemanticMap(targetRoot, id string) (*semantic.SemanticMapIR, bool) {
	if s == nil || id == "" {
		return nil, false
	}
	value, ok := s.semanticMaps.Load(semanticMapCacheKey(targetRoot, id))
	if !ok {
		return nil, false
	}
	mapIR, ok := value.(*semantic.SemanticMapIR)
	return mapIR, ok && mapIR != nil
}

// loadEnrichmentSemanticMap resolves only the active, schema-validated map
// permitted by the enrichment contract. Memory is an optimization, not an
// authority. On a miss, recovery reads the validated active proof bundle and
// binds every supplied identity to the same map, pointer, and manifest.
func (s *Server) loadEnrichmentSemanticMap(targetRoot, generationID, basisID string) (*semantic.SemanticMapIR, bool) {
	if s == nil {
		return nil, false
	}
	lookupIDs := make([]string, 0, 2)
	if generationID != "" {
		lookupIDs = append(lookupIDs, generationID)
	}
	if basisID != "" {
		lookupIDs = append(lookupIDs, basisID)
	}
	if len(lookupIDs) == 0 {
		lookupIDs = append(lookupIDs, "active")
	}
	for _, id := range lookupIDs {
		value, ok := s.semanticMaps.Load(semanticMapCacheKey(targetRoot, id))
		mapIR, valid := value.(*semantic.SemanticMapIR)
		if !ok || !valid || mapIR == nil || mapIR.Freshness != "current" || !semanticMapMatchesEnrichmentIdentity(mapIR, generationID, basisID) {
			continue
		}
		mapBytes, err := json.Marshal(mapIR)
		if err == nil && contractharness.ValidateSemanticMapIR(mapBytes) == nil {
			return mapIR, true
		}
	}

	st, err := s.getStorage(targetRoot)
	if err != nil {
		return nil, false
	}
	bundle, err := st.ReadValidatedActiveProofBundle()
	if err != nil || bundle == nil || bundle.Pointer == nil || bundle.Manifest == nil || len(bundle.SemanticMap) == 0 {
		return nil, false
	}
	if generationID != "" && (bundle.Pointer.GenerationID != generationID || bundle.Manifest.GenerationID != generationID) {
		return nil, false
	}
	if basisID != "" && (bundle.Pointer.ComputedBasisID != basisID || bundle.Manifest.ComputedBasisID != basisID) {
		return nil, false
	}
	if err := contractharness.ValidateSemanticMapIR(bundle.SemanticMap); err != nil {
		return nil, false
	}
	var mapIR semantic.SemanticMapIR
	if err := json.Unmarshal(bundle.SemanticMap, &mapIR); err != nil || !semanticMapMatchesEnrichmentIdentity(&mapIR, generationID, basisID) {
		return nil, false
	}
	if mapIR.GenerationID != bundle.Pointer.GenerationID || mapIR.ComputedBasisID != bundle.Pointer.ComputedBasisID || mapIR.GenerationID != bundle.Manifest.GenerationID || mapIR.ComputedBasisID != bundle.Manifest.ComputedBasisID {
		return nil, false
	}
	return &mapIR, true
}

func semanticMapMatchesEnrichmentIdentity(mapIR *semantic.SemanticMapIR, generationID, basisID string) bool {
	return mapIR != nil && (generationID == "" || mapIR.GenerationID == generationID) && (basisID == "" || mapIR.ComputedBasisID == basisID)
}

// NewServer creates a configured MCP Server ready for immediate stdio handshake.
// Adapters, storage layouts, and child process pools are initialized on-demand per tool call.
func NewServer(cfg Config) (*Server, error) {
	if cfg.RequireToken && !semantic.ValidApprovalAuthToken(cfg.AuthToken) {
		return nil, fmt.Errorf("auth token configuration is invalid")
	}
	authenticator := semantic.NewLocalProcessApprovalAuthenticator()
	authorizer := semantic.NewApprovalWorkspaceAuthorizer(cfg.RepoRoot)
	return &Server{
		cfg:                 cfg,
		approvalGate:        semantic.NewApprovalAccessGate(authenticator, authorizer),
		approvalWorkspaceID: authorizer.WorkspaceID(),
		proposalStore:       semantic.NewDurableProposalStore(cfg.RepoRoot),
		registry:            newAdapterRegistry(),
		modelHostActive:     make(map[uint64]context.CancelFunc),
		modelHostDrainDone:  closedLifecycleChannel(),
		modelHostSpawnDone:  closedLifecycleChannel(),
	}, nil
}

// Close releases server resources.
func (s *Server) Close() error {
	if s == nil {
		return nil
	}
	s.closeOnce.Do(func() {
		var closeErrors []error
		stopCtx, cancelStop := context.WithTimeout(context.Background(), 5*time.Second)
		if err := s.stopModelHostRequests(stopCtx); err != nil {
			closeErrors = append(closeErrors, err)
		}
		cancelStop()
		if s.registry != nil {
			s.registry.closeAll()
		}
		servers := make([]*flowview.Server, 0)
		s.liveServers.Range(func(key, value any) bool {
			if server, ok := value.(*flowview.Server); ok && server != nil {
				servers = append(servers, server)
			}
			s.liveServers.Delete(key)
			return true
		})

		s.fvMu.Lock()
		if s.fv != nil {
			servers = append(servers, s.fv)
			s.fv = nil
		}
		s.fvMu.Unlock()
		closedServers := make(map[*flowview.Server]struct{}, len(servers))
		for _, server := range servers {
			if server == nil {
				continue
			}
			if _, seen := closedServers[server]; seen {
				continue
			}
			closedServers[server] = struct{}{}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			if err := server.Shutdown(ctx); err != nil {
				closeErrors = append(closeErrors, err)
			}
			cancel()
		}
		waitCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := s.waitModelHostRequests(waitCtx); err != nil {
			closeErrors = append(closeErrors, err)
		}
		cancel()
		s.closeErr = joinLifecycleErrors(closeErrors...)
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

	adapterCfg, err := harvest.ResolveAdapter(lang, spec)
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

var errMCPApprovalUnauthenticated = errors.New("mcp approval authentication failed")

// mcpAuthenticationError preserves the legacy checkAuth text for non-approval
// tools while giving the approval response boundary a typed classification.
// It never stores or exposes the supplied token.
type mcpAuthenticationError struct {
	configured bool
}

func (e *mcpAuthenticationError) Error() string {
	if e == nil || !e.configured {
		return "unauthorized: auth token is not configured"
	}
	return "unauthorized: missing or invalid auth token"
}

func (*mcpAuthenticationError) Unwrap() error { return errMCPApprovalUnauthenticated }

// Serve reads JSON-RPC requests from in and writes responses to out until EOF.
func (s *Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	scanner := bufio.NewScanner(in)
	// Artifact may be up to 512 KiB plus envelope; allow 2 MiB lines.
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

		resp := s.handleRequest(ctx, req)
		if err := enc.Encode(resp); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func (s *Server) handleRequest(ctx context.Context, req rpcRequest) rpcResponse {
	// Standard MCP methods
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

		if callParams.Name == "submit_semantic_approval" {
			res, err := s.handleSubmitSemanticApprovalJSON(ctx, callParams.Arguments)
			return mcpApprovalToolCallResponse(req.ID, res, err)
		}
		if callParams.Name == "get_semantic_approval_history" {
			res, err := s.handleGetSemanticApprovalHistoryJSON(ctx, callParams.Arguments)
			return mcpApprovalToolCallResponse(req.ID, res, err)
		}
		var arguments map[string]any
		if err := json.Unmarshal(callParams.Arguments, &arguments); err != nil {
			return rpcResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error:   &rpcError{Code: -32602, Message: "Invalid tool call params"},
			}
		}
		res, err := s.executeTool(ctx, callParams.Name, arguments)
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
		// Structured core-flow errors are JSON with a "code" field — emit as-is per spec §7.
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

	resJSON, marshalErr := marshalPublicMCPResult(res)
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

func mcpApprovalToolCallResponse(id any, res any, err error) rpcResponse {
	if err != nil {
		return mcpApprovalErrorResponse(id, classifyMCPApprovalError(err), err)
	}
	resJSON, marshalErr := marshalPublicMCPResult(res)
	if marshalErr != nil {
		return mcpApprovalErrorResponse(id, "approval_invalid", nil)
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

func classifyMCPApprovalError(err error) string {
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return "approval_unavailable"
	case errors.Is(err, errMCPApprovalUnauthenticated), errors.Is(err, semantic.ErrApprovalUnauthenticated):
		return "approval_unauthenticated"
	case errors.Is(err, semantic.ErrApprovalUnauthorized):
		return "approval_unauthorized"
	case errors.Is(err, semantic.ErrApprovalExecutionConflict):
		return "approval_conflict"
	case errors.Is(err, semantic.ErrApprovalExecutionUnavailable):
		return "approval_unavailable"
	case errors.Is(err, semantic.ErrApprovalHistoryUnavailable):
		return "approval_unavailable"
	case errors.Is(err, semantic.ErrApprovalHistoryInvalid):
		return "approval_invalid"
	default:
		return "approval_invalid"
	}
}

func mcpApprovalErrorResponse(id any, code string, cause error) rpcResponse {
	messages := map[string]string{
		"approval_invalid":         "approval request is invalid",
		"approval_conflict":        "approval request conflicts with current state",
		"approval_unavailable":     "approval service is unavailable",
		"approval_unauthenticated": "approval authentication is required",
		"approval_unauthorized":    "approval workspace is not authorized",
	}
	message, ok := messages[code]
	if !ok {
		code = "approval_invalid"
		message = messages[code]
	}
	details := map[string]any{"code": code, "message": message}
	if code == "approval_conflict" {
		if version, ok := semantic.ApprovalConflictCurrentVersion(cause); ok {
			details["currentVersion"] = version
		}
	}
	payload, _ := json.Marshal(details)
	return rpcResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result: map[string]any{
			"content": []map[string]string{
				{"type": "text", "text": string(payload)},
			},
			"isError": true,
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
			"description": "Harvest candidate flows with scoring and intent signals for natural language matching",
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
			"description": "On-demand slice and publish for an arbitrary entry point",
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
			"description": "Open FlowView in the browser for visual review",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"flowId": map[string]any{"type": "string"},
					"target": targetProp,
				},
			},
		},
		{
			"name":        "query_task_view",
			"description": "Execute a task-scoped query against the workspace. Feature/review/impact modes use task-view-query; debug/incident modes require an explicit rflsc.failure-query.v2 with exact basis, generation, snapshot, freshness, and server-resolved runtime observation identity.",
			"inputSchema": map[string]any{
				"type":     "object",
				"required": []string{"query"},
				"properties": map[string]any{
					"query":  map[string]any{"$ref": "https://codeflow.local/schemas/task-view-query.schema.json"},
					"target": targetProp,
					"token":  map[string]any{"type": "string", "description": "Auth token when RequireToken=true"},
				},
			},
		},
		{
			"name":        "get_current_answer",
			"description": "Get the VS-03 current-answer publication for a flow query or flowId. If no current proof is published, returns a typed no_current_proof result.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query":  map[string]any{"type": "string", "description": "Natural language question or flow query"},
					"flowId": map[string]any{"type": "string", "description": "Optional explicit flow ID"},
					"target": targetProp,
					"token":  map[string]any{"type": "string", "description": "Auth token when RequireToken=true"},
				},
			},
		},
		{
			"name":        "get_workspace_activity",
			"description": "Get current workspace activity status, pending revisions count, and live snapshot info",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"target": targetProp,
					"token":  map[string]any{"type": "string", "description": "Auth token when RequireToken=true"},
				},
			},
		},
		{
			"name":        "submit_versioned_edit",
			"description": "Submit a versioned document edit to the workspace snapshot engine",
			"inputSchema": map[string]any{
				"type":     "object",
				"required": []string{"path", "content", "documentVersion"},
				"properties": map[string]any{
					"path":            map[string]any{"type": "string", "description": "Relative file path"},
					"content":         map[string]any{"type": "string", "description": "New file content bytes"},
					"documentVersion": map[string]any{"type": "integer", "description": "Monotonic document version >= 1"},
					"source":          map[string]any{"type": "string", "description": "Edit source (agent_transaction, ide_versioned, watcher_fallback)"},
					"target":          targetProp,
					"token":           map[string]any{"type": "string", "description": "Auth token when RequireToken=true"},
				},
			},
		},
		{
			"name":        "get_generation_proof",
			"description": "Retrieves the active pointer and Generation Proof Manifest for the current workspace generation (VS-04, Raw §10.11)",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"target": targetProp,
					"token":  map[string]any{"type": "string", "description": "Auth token when RequireToken=true"},
				},
			},
		},
		{
			"name":        "get_verified_gap",
			"description": "Retrieves the latest-vs-verified gap and affected scope when workspace edits have occurred (VS-04, Raw §7.3, §7.4)",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"target": targetProp,
					"token":  map[string]any{"type": "string", "description": "Auth token when RequireToken=true"},
				},
			},
		},
		{
			"name":        "get_semantic_delta",
			"description": "Compute the semantic delta (added, changed, removed behavior, evidence updates) between baseline and current generations (VS-05, Raw §8.5, §10.12)",
			"inputSchema": map[string]any{
				"type":     "object",
				"required": []string{"baseline", "current"},
				"properties": map[string]any{
					"baseline": map[string]any{"type": "string", "description": "Baseline generation ID or basis"},
					"current":  map[string]any{"type": "string", "description": "Current generation ID or basis"},
					"target":   targetProp,
					"token":    map[string]any{"type": "string", "description": "Auth token when RequireToken=true"},
				},
			},
		},
		{
			"name":        "get_requirement_alignment",
			"description": "Evaluate and retrieve the requirement alignment matrix and evidence grounding for the current workspace (VS-05, Raw §9.10, §10.13)",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"target":         targetProp,
					"intentRevision": map[string]any{"type": "integer", "description": "Optional task intent revision filter"},
					"token":          map[string]any{"type": "string", "description": "Auth token when RequireToken=true"},
				},
			},
		},
		{
			"name":        "get_change_impact",
			"description": "Trace evidence-grounded direct and bounded indirect callers, state mutations, external effects, related tests, and unknown frontiers for an explicit symbol or change batch basis (VS-05)",
			"inputSchema": map[string]any{
				"type": "object",
				"required": []string{
					"computedBasisId",
					"generationId",
					"freshness",
					"maxDepth",
					"maxNodes",
					"relationKinds",
				},
				"oneOf": []map[string]any{
					{
						"required": []string{"symbolId"},
						"not":      map[string]any{"required": []string{"changeBatchId"}},
					},
					{
						"required": []string{"changeBatchId"},
						"not":      map[string]any{"required": []string{"symbolId"}},
					},
				},
				"properties": map[string]any{
					"symbolId":        map[string]any{"type": "string", "description": "Changed canonical symbol identity. Mutually exclusive with changeBatchId"},
					"changeBatchId":   map[string]any{"type": "string", "description": "Committed change batch identity. Mutually exclusive with symbolId"},
					"computedBasisId": map[string]any{"type": "string", "description": "Explicit immutable computed basis identity"},
					"generationId":    map[string]any{"type": "string", "description": "Explicit semantic generation identity"},
					"freshness":       map[string]any{"type": "string", "enum": []string{"current", "historical"}, "description": "Whether the requested basis is the validated active proof or an explicit historical map"},
					"maxDepth":        map[string]any{"type": "integer", "minimum": 1, "maximum": 5, "description": "Maximum bounded traversal depth"},
					"maxNodes":        map[string]any{"type": "integer", "minimum": 1, "maximum": 50, "description": "Maximum bounded traversal nodes"},
					"relationKinds":   map[string]any{"type": "array", "minItems": 1, "uniqueItems": true, "items": map[string]any{"type": "string", "enum": []string{"calls", "overrides", "instantiates", "state_mutation", "external_effect", "related_test", "related_flow"}}, "description": "Explicit impact relation filters"},
					"target":          targetProp,
					"token":           map[string]any{"type": "string", "description": "Auth token when RequireToken=true"},
				},
			},
		},
		{
			"name":        "investigate_failure",
			"description": "Trace a failure path using an explicit rflsc.failure-query.v2 and canonical semantic map/proof. Incident runtime observations are resolved by the configured trusted provider/store. No caller-supplied observation is accepted as authority.",
			"inputSchema": map[string]any{
				"type":     "object",
				"required": []string{"query"},
				"properties": map[string]any{
					"query":          map[string]any{"$ref": semantic.FailureQuerySchemaID},
					"semanticMap":    map[string]any{"$ref": semantic.SemanticMapSchemaID, "description": "Required for historical freshness; current freshness uses the strict active proof."},
					"map":            map[string]any{"$ref": semantic.SemanticMapSchemaID, "description": "Alias for semanticMap in historical queries."},
					"proof":          map[string]any{"type": "object", "description": "Explicit proof manifest plus pointer for historical queries."},
					"pointer":        map[string]any{"type": "object", "description": "Explicit active-pointer identity paired with a historical proof."},
					"runtimeConsent": map[string]any{"$ref": rflscvs06.RuntimeConsentSchemaID, "description": "Exact one-shot consent required for trusted_local observations."},
					"runtime":        map[string]any{"type": "object", "description": "Optional one-shot operation parameters for trusted_local execution."},
					"target":         targetProp,
					"token":          map[string]any{"type": "string", "description": "Auth token when RequireToken=true"},
				},
			},
		},
		{
			"name":        "request_semantic_enrichment",
			"description": "Request optional display-only semantic enrichment from a configured measured model host. The response is grounded in the current verified snapshot evidence pack or an explicit deterministic fallback.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"generationId":     map[string]any{"type": "string", "description": "Optional exact cached deterministic generation identity"},
					"computedBasisId":  map[string]any{"type": "string", "description": "Optional exact cached deterministic basis identity"},
					"targetStepId":     map[string]any{"type": "string", "description": "Optional deterministic map step identity"},
					"targetStepIds":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
					"targetSymbolPath": map[string]any{"type": "string", "description": "Optional canonical step symbol path"},
					"scopePaths":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
					"promptRevision":   map[string]any{"type": "string"},
					"target":           targetProp,
					"token":            map[string]any{"type": "string", "description": "Auth token when RequireToken=true"},
				},
			},
		},
		{
			"name":        "get_evidence_pack",
			"description": "Aggregate and retrieve verified AST, call, and test evidence with secret redaction for a symbol (VS-08, Raw §9.7, §10)",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"symbolPath": map[string]any{"type": "string", "description": "Target symbol path"},
					"target":     targetProp,
					"token":      map[string]any{"type": "string", "description": "Auth token when RequireToken=true"},
				},
			},
		},
		{
			"name":        "submit_semantic_approval",
			"description": "Execute one authenticated, durable v2 semantic approval command grounded to an exact stored proposal and evidence pack",
			"inputSchema": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"required": []string{
					"commandId", "proposalId", "evidencePackId", "computedBasisId", "generationId",
					"intentRevision", "decision", "idempotencyKey", "expectedApprovalVersion", "expectedState",
				},
				"properties": map[string]any{
					"commandId":               map[string]any{"type": "string", "minLength": 1, "maxLength": 256, "description": "Unique v2 approval command identity"},
					"proposalId":              map[string]any{"type": "string", "maxLength": 256, "description": "Exact durable proposal identity"},
					"evidencePackId":          map[string]any{"type": "string", "maxLength": 256, "description": "Exact durable evidence-pack identity"},
					"computedBasisId":         map[string]any{"type": "string", "maxLength": 256, "description": "Exact immutable basis identity"},
					"generationId":            map[string]any{"type": "string", "maxLength": 256, "description": "Exact generation identity"},
					"intentRevision":          map[string]any{"type": "integer", "minimum": 1, "maximum": 1000000, "description": "Exact task intent revision"},
					"decision":                map[string]any{"type": "string", "enum": []string{"approve", "edit_then_approve", "reject", "revoke", "supersede"}, "description": "Explicit v2 lifecycle decision"},
					"editedText":              map[string]any{"type": "string", "maxLength": 4096, "description": "Required only for edit_then_approve"},
					"idempotencyKey":          map[string]any{"type": "string", "minLength": 1, "maxLength": 256, "description": "Stable retry identity"},
					"expectedApprovalVersion": map[string]any{"type": "integer", "minimum": 0, "maximum": 1000000000, "description": "Expected current approval version"},
					"expectedState":           map[string]any{"type": "string", "enum": []string{"none", "active", "rejected", "revoked", "superseded"}, "description": "Expected current approval state"},
					"predecessorApprovalId":   map[string]any{"type": "string", "maxLength": 256, "description": "Required by decisions that supersede an existing approval"},
					"target":                  targetProp,
					"token":                   map[string]any{"type": "string", "description": "Auth token when RequireToken=true"},
				},
			},
		},
		{
			"name":        "get_semantic_approval_history",
			"description": "Read-only durable semantic approval history for one exact proposal and evidence-pack identity",
			"inputSchema": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"required":             []string{"proposalId", "evidencePackId"},
				"properties": map[string]any{
					"proposalId":     map[string]any{"type": "string", "minLength": 1, "maxLength": 256, "description": "Exact durable proposal identity"},
					"evidencePackId": map[string]any{"type": "string", "minLength": 1, "maxLength": 256, "description": "Exact durable evidence-pack identity"},
					"target":         map[string]any{"type": "string", "maxLength": 4096, "description": "Optional exact configured workspace target"},
					"token":          map[string]any{"type": "string", "minLength": 1, "maxLength": 256, "description": "Auth token when RequireToken=true"},
				},
			},
		},
		{
			"name":        "explore_project_domains",
			"description": "Explore evidence-backed repository domains and deterministic representative flows for progressive onboarding (VS-07, Raw §8.9, §10)",
			"inputSchema": map[string]any{
				"type":     "object",
				"required": []string{"repositoryId", "freshness"},
				"properties": map[string]any{
					"repositoryId":               map[string]any{"type": "string", "minLength": 1, "description": "Repository identity. Required."},
					"freshness":                  map[string]any{"type": "string", "enum": []string{"current", "historical"}, "description": "Current requires validated proof/live-head match. Historical requires exact basis, generation, and snapshot."},
					"computedBasisId":            map[string]any{"type": "string", "description": "Exact immutable basis identity. Required for historical."},
					"generationId":               map[string]any{"type": "string", "description": "Exact generation identity. Required for historical."},
					"validatedAgainstSnapshotId": map[string]any{"type": "string", "description": "Exact validated snapshot identity. Required for historical and optional only when current proof resolves it."},
					"domain":                     map[string]any{"type": "string", "description": "Optional evidence-backed domain filter"},
					"level":                      map[string]any{"type": "integer", "minimum": 1, "maximum": 2, "description": "Progressive disclosure level (1: domain map, 2: representative flow catalog)"},
					"maxVisibleCoreSteps":        map[string]any{"type": "integer", "minimum": 1, "description": "Optional display budget for level-2 flow drilldown"},
					"target":                     targetProp,
					"token":                      map[string]any{"type": "string", "description": "Auth token when RequireToken=true"},
				},
			},
		},
		{
			"name":        "validate_release_capability",
			"description": "Evaluate explicitly supplied, immutable release evidence. Missing evidence returns an incomplete result; the tool never generates benchmark metrics or infers capability from model names (VS-10).",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"evaluation": map[string]any{
						"type":        "object",
						"description": "Explicit ReleaseEvaluationInput containing a declared profile, versioned corpus, executed reports, approved thresholds, and child execution evidence. Top-level artifactRef values must match canonical content hashes.",
					},
					"target": targetProp,
					"token":  map[string]any{"type": "string", "description": "Auth token when RequireToken=true"},
				},
				"additionalProperties": false,
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

func isSubpath(root, target string) bool {
	root = filepath.Clean(root)
	target = filepath.Clean(target)
	if root == target {
		return true
	}
	if len(target) <= len(root) {
		return false
	}
	if target[len(root)] != filepath.Separator {
		return false
	}
	return target[:len(root)] == root
}

func (s *Server) executeTool(ctx context.Context, name string, args map[string]any) (any, error) {
	switch name {
	case "publish_core_flow":
		return s.handlePublishCoreFlow(ctx, args)
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
		// Optional server-side query filter (case-insensitive substring across multiple signals).
		if qRaw, ok := args["query"].(string); ok && strings.TrimSpace(qRaw) != "" {
			q := strings.TrimSpace(qRaw)
			terms := strings.Fields(strings.ToLower(q))
			var filtered []harvest.Candidate
			for _, c := range candidates {
				// Build searchable corpus lowercased.
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

		// Compute basisSha via worktree fingerprint over unique file part of entry.
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

		// Read existing generation for non-destructive merge.
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

		// Copy existing flows (except same FlowID which will be replaced).
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

		// Validate against session-artifact schema
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

		// Append to event log
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
		url := fv.URL() + "&flow=" + flowID
		return map[string]any{
			"status": "ready",
			"flowId": flowID,
			"url":    url,
			"token":  fv.AuthToken(),
		}, nil

	case "query_task_view":
		return s.handleQueryTaskView(ctx, args)

	case "get_current_answer":
		return s.handleGetCurrentAnswer(ctx, args)

	case "get_workspace_activity":
		return s.handleGetWorkspaceActivity(ctx, args)

	case "submit_versioned_edit":
		return s.handleSubmitVersionedEdit(ctx, args)

	case "get_generation_proof":
		return s.handleGetGenerationProof(ctx, args)

	case "get_verified_gap":
		return s.handleGetVerifiedGap(ctx, args)

	case "get_semantic_delta":
		return s.handleGetSemanticDelta(ctx, args)

	case "get_requirement_alignment":
		return s.handleGetRequirementAlignment(ctx, args)

	case "get_change_impact":
		return s.handleGetChangeImpact(ctx, args)

	case "investigate_failure":
		return s.handleInvestigateFailure(ctx, args)

	case "request_semantic_enrichment":
		return s.handleSemanticEnrichment(ctx, args)

	case "get_evidence_pack":
		return s.handleGetEvidencePack(ctx, args)

	case "submit_semantic_approval":
		return s.handleSubmitSemanticApproval(ctx, args)

	case "explore_project_domains":
		return s.handleExploreProjectDomains(ctx, args)

	case "validate_release_capability":
		return s.handleValidateReleaseCapability(ctx, args)

	default:
		return nil, fmt.Errorf("unknown tool %s", name)
	}
}
