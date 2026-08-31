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
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"codeflow/internal/contractharness"
	"codeflow/internal/detect"
	"codeflow/internal/flowview"
	"codeflow/internal/fusion"
	"codeflow/internal/harvest"
	"codeflow/internal/protocol"
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
}

// Server handles MCP JSON-RPC requests over stdio.
type Server struct {
	cfg       Config
	storage   *storage.Storage
	eventLog  *fusion.EventLog
	pool      *protocol.Pool
	slicer    *slicing.Runner
	harvester *harvest.Runner
	fv        *flowview.Server
	fvMu      sync.Mutex
}

// NewServer creates a configured MCP Server.
func NewServer(cfg Config) (*Server, error) {
	st := storage.New(cfg.RepoRoot)
	if err := st.InitLayout(); err != nil {
		return nil, fmt.Errorf("init storage layout: %w", err)
	}

	lang := cfg.Language
	if lang == "" {
		det := detect.Detect(cfg.RepoRoot)
		if det.Confident && det.Language != "" && det.Language != "unknown" {
			lang = det.Language
		} else {
			lang = "dart"
		}
	}
	spec := cfg.AdapterSpec
	if spec == "" {
		spec = cfg.DartAdapter
	}
	adapterCfg, err := harvest.ResolveAdapter(lang, spec)
	if err != nil {
		return nil, fmt.Errorf("resolve %s adapter: %w", lang, err)
	}

	pool := protocol.NewPool(adapterCfg, 2)
	slicer := slicing.NewRunner(pool)
	harvester := harvest.NewRunnerWithPool(pool)

	return &Server{
		cfg:       cfg,
		storage:   st,
		eventLog:  fusion.NewEventLog(cfg.RepoRoot),
		pool:      pool,
		slicer:    slicer,
		harvester: harvester,
	}, nil
}

// Close releases server resources.
func (s *Server) Close() {
	if s.pool != nil {
		s.pool.Close()
	}
	if s.harvester != nil {
		s.harvester.Close()
	}
	s.fvMu.Lock()
	if s.fv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = s.fv.Shutdown(ctx)
		cancel()
		s.fv = nil
	}
	s.fvMu.Unlock()
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
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &callParams); err != nil {
			return rpcResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error:   &rpcError{Code: -32602, Message: "Invalid tool call params"},
			}
		}

		res, err := s.executeTool(ctx, callParams.Name, callParams.Arguments)
		if err != nil {
			msg := err.Error()
			// Structured core-flow errors are JSON with a "code" field — emit as-is per spec §7.
			if strings.HasPrefix(strings.TrimSpace(msg), "{") && strings.Contains(msg, "\"code\"") {
				return rpcResponse{
					JSONRPC: "2.0",
					ID:      req.ID,
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
				ID:      req.ID,
				Result: map[string]any{
					"content": []map[string]string{
						{"type": "text", "text": fmt.Sprintf("Error: %v", err)},
					},
					"isError": true,
				},
			}
		}

		resJSON, _ := json.Marshal(res)
		return rpcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]any{
				"content": []map[string]string{
					{"type": "text", "text": string(resJSON)},
				},
			},
		}

	default:
		return rpcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &rpcError{Code: -32601, Message: fmt.Sprintf("Method %s not found", req.Method)},
		}
	}
}

func (s *Server) listTools() []map[string]any {
	return []map[string]any{
		{
			"name":        "publish_core_flow",
			"description": "Publish a verified architecture-layer core flow from an agent-authored intermediate artifact. Verifies every anchor against the current worktree; on mismatch returns a correctable error without persisting.",
			"inputSchema": map[string]any{
				"type": "object",
				"required": []string{"artifact"},
				"properties": map[string]any{
					"artifact": map[string]any{"$ref": "https://codeflow.local/schemas/core-artifact.schema.json"},
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
					"target": map[string]any{"type": "string", "description": "Target subdir or '.'"},
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
				},
			},
		},
		{
			"name":        "analyze_flow",
			"description": "On-demand slice and publish for an arbitrary entry point",
			"inputSchema": map[string]any{
				"type": "object",
				"required": []string{"entrySymbolPath"},
				"properties": map[string]any{
					"entrySymbolPath": map[string]any{"type": "string"},
				},
			},
		},
		{
			"name":        "submit_flow_draft",
			"description": "Submit structured E2 session journey draft with verified anchors",
			"inputSchema": map[string]any{
				"type": "object",
				"required": []string{"artifact"},
				"properties": map[string]any{
					"artifact": map[string]any{"type": "object"},
					"token":    map[string]any{"type": "string"},
				},
			},
		},
		{
			"name":        "approve_step",
			"description": "Approve a step name and rules (E3 in-place approval)",
			"inputSchema": map[string]any{
				"type": "object",
				"required": []string{"flowId", "symbolPath", "name"},
				"properties": map[string]any{
					"flowId":     map[string]any{"type": "string"},
					"symbolPath": map[string]any{"type": "string"},
					"name":       map[string]any{"type": "string"},
					"rules":      map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
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
				},
			},
		},
	}
}

func (s *Server) checkAuth(token any) error {
	if !s.cfg.RequireToken {
		return nil
	}
	tStr, _ := token.(string)
	if s.cfg.AuthToken != "" && tStr != s.cfg.AuthToken {
		return fmt.Errorf("unauthorized: missing or invalid auth token")
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
		target := s.cfg.RepoRoot
		if sub, ok := args["target"].(string); ok && sub != "" && sub != "." {
			if filepath.IsAbs(sub) {
				target = sub
			} else {
				target = filepath.Join(s.cfg.RepoRoot, sub)
			}
			if !filepath.IsAbs(target) {
				return nil, fmt.Errorf("invalid target path: %q", sub)
			}
			clean := filepath.Clean(target)
			if !isSubpath(s.cfg.RepoRoot, clean) {
				return nil, fmt.Errorf("target escapes repo root")
			}
		}
		candidates, err := s.harvester.Run(ctx, target)
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

		raw, err := s.storage.ReadActiveFlowSpec(flowID)
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
		h := sha256.Sum256([]byte(entry))
		candidateID := "cand-" + hex.EncodeToString(h[:8])

		sliced, err := s.slicer.Slice(ctx, s.cfg.RepoRoot, candidateID, entry, nil)
		if err != nil {
			return nil, fmt.Errorf("slice error: %w", err)
		}

		approved, session, err := s.eventLog.MaterializeView()
		if err != nil {
			return nil, err
		}

		// Compute basisSha via worktree fingerprint over unique file part of entry.
		entryFile := entry
		if idx := strings.Index(entry, "#"); idx >= 0 {
			entryFile = entry[:idx]
		}
		basisSha, err := storage.ComputeWorktreeFingerprint(s.cfg.RepoRoot, []string{entryFile})
		if err != nil {
			return nil, fmt.Errorf("compute basisSha: %w", err)
		}

		spec, err := fusion.Fuse(sliced, fusion.FuseOptions{
			RepoRoot:       s.cfg.RepoRoot,
			ApprovedLedger: approved,
			SessionDrafts:  session,
			BasisSha:       basisSha,
		})
		if err != nil {
			return nil, fmt.Errorf("fuse error: %w", err)
		}

		// Read existing generation for non-destructive merge.
		existingPtr, _ := s.storage.ReadPointer()
		var existingIdx *storage.GenerationIndex
		if existingPtr != nil {
			existingIdx, _ = s.storage.ReadLatestIndex()
		}

		sess, err := s.storage.BeginGeneration(basisSha)
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
				raw, err := s.storage.ReadFlowSpec(existingPtr.GenerationID, sum.FlowID)
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

		// Append to event log
		for _, st := range artifact.JourneyDraft.Steps {
			_ = s.eventLog.Append(fusion.Event{
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

		err := s.eventLog.Append(fusion.Event{
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
		flowID, _ := args["flowId"].(string)
		if flowID != "" {
			raw, err := s.storage.ReadActiveFlowSpec(flowID)
			if err != nil {
				return nil, err
			}
			var spec fusion.FlowSpec
			_ = json.Unmarshal(raw, &spec)
			return spec.Unknowns, nil
		}
		idx, err := s.storage.ReadLatestIndex()
		if err != nil {
			return nil, err
		}
		if idx == nil {
			return []fusion.Unknown{}, nil
		}
		var all []fusion.Unknown
		for _, f := range idx.Flows {
			raw, err := s.storage.ReadFlowSpec(idx.GenerationID, f.FlowID)
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
		flowID, _ := args["flowId"].(string)
		s.fvMu.Lock()
		if s.fv == nil {
			srv, err := flowview.NewServer(flowview.Config{RepoRoot: s.cfg.RepoRoot, Port: 4567})
			if err != nil {
				srv, err = flowview.NewServer(flowview.Config{RepoRoot: s.cfg.RepoRoot, Port: 0})
				if err != nil {
					s.fvMu.Unlock()
					return nil, fmt.Errorf("start flowview: %w", err)
				}
			}
			srv.Start()
			s.fv = srv
		}
		fv := s.fv
		s.fvMu.Unlock()
		url := fv.URL() + "&flow=" + flowID
		return map[string]any{
			"status": "ready",
			"flowId": flowID,
			"url":    url,
			"token":  fv.AuthToken(),
		}, nil

	default:
		return nil, fmt.Errorf("unknown tool %s", name)
	}
}
