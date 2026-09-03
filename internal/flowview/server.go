// Package flowview embeds and serves the FlowView interactive user interface
// (design §4.3, tickets 16, 17, 18).
package flowview

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"codeflow/internal/contractharness"
	"codeflow/internal/detect"
	"codeflow/internal/fusion"
	"codeflow/internal/harvest"
	"codeflow/internal/protocol"
	"codeflow/internal/semantic"
	"codeflow/internal/slicing"
	"codeflow/internal/storage"
	"codeflow/internal/workspace"
)

// Server coordinates the loopback HTTP server for FlowView.
type Server struct {
	repoRoot   string
	storage    *storage.Storage
	eventLog   *fusion.EventLog
	engine     *workspace.SnapshotEngine
	authToken  string
	listener   net.Listener
	httpServer *http.Server
	mu         sync.Mutex
	addr       string
	genCache   *generationCache
	hub        *EventHub
	gate       *semantic.PublicationGate
	scheduler  *semantic.CoalescingScheduler
	mapCache   map[string]*semantic.SemanticMapIR
}

// Config configures the FlowView server.
type Config struct {
	RepoRoot  string
	Port      int
	AuthToken string
}

// NewServer initializes a FlowView server instance.
func NewServer(cfg Config) (*Server, error) {
	st := storage.New(cfg.RepoRoot)
	if err := st.InitLayout(); err != nil {
		return nil, fmt.Errorf("init storage: %w", err)
	}

	token := cfg.AuthToken
	if token == "" {
		b := make([]byte, 16)
		_, _ = rand.Read(b)
		token = hex.EncodeToString(b)
	}

	engine, err := workspace.NewSnapshotEngine(cfg.RepoRoot, "")
	if err != nil {
		return nil, fmt.Errorf("init snapshot engine: %w", err)
	}

	hub := NewEventHub("flowview-live-stream", 100)
	gate := semantic.NewPublicationGate()
	scheduler := semantic.NewCoalescingScheduler(semantic.DefaultCoalescingConfig())

	s := &Server{
		repoRoot:  cfg.RepoRoot,
		storage:   st,
		eventLog:  fusion.NewEventLog(cfg.RepoRoot),
		engine:    engine,
		authToken: token,
		hub:       hub,
		gate:      gate,
		scheduler: scheduler,
		mapCache:  make(map[string]*semantic.SemanticMapIR),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/api/flows", s.handleListFlows)
	mux.HandleFunc("/api/flow", s.handleGetFlow)
	mux.HandleFunc("/api/source", s.handleGetSource)
	mux.HandleFunc("/api/approve", s.handleApprove)
	mux.HandleFunc("/api/map", s.handleGetMap)
	mux.HandleFunc("/api/map/override", s.handlePostLaneOverride)
	mux.HandleFunc("/api/task/view", s.handleTaskView)
	mux.HandleFunc("/api/task/review", s.handleTaskReview)
	mux.HandleFunc("/api/workspace/activity", s.handleWorkspaceActivity)
	mux.HandleFunc("/api/workspace/edit", s.handleWorkspaceEdit)
	mux.HandleFunc("/api/workspace/stream", s.handleWorkspaceStream)
	mux.HandleFunc("/api/workspace/proof", s.handleWorkspaceProof)

	port := cfg.Port
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return nil, fmt.Errorf("listen error: %w", err)
	}

	s.listener = ln
	s.addr = ln.Addr().String()
	s.httpServer = &http.Server{
		Handler:      s.securityMiddleware(mux),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
	}

	return s, nil
}

// Addr returns the server listening address.
func (s *Server) Addr() string {
	return s.addr
}

// AuthToken returns the per-run CSRF/auth token.
func (s *Server) AuthToken() string {
	return s.authToken
}

// URL returns the full local browser URL including auth token.
func (s *Server) URL() string {
	return fmt.Sprintf("http://%s/?token=%s", s.addr, s.authToken)
}

// Start runs the HTTP server in the background.
func (s *Server) Start() {
	go func() {
		_ = s.httpServer.Serve(s.listener)
	}()
}

// Shutdown gracefully shuts down the HTTP server.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}

// securityMiddleware enforces loopback Host validation, Origin/Referer CSRF check
// (design-v2 §11.3) and per-run token verification.
func (s *Server) securityMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Host check (R8 loopback)
		host := r.Host
		if !strings.HasPrefix(host, "127.0.0.1") && !strings.HasPrefix(host, "localhost") {
			http.Error(w, "Forbidden: invalid host", http.StatusForbidden)
			return
		}

		// CSRF check: if Origin header present, verify it starts with
		// http://127.0.0.1 or http://localhost (or is empty); otherwise 403.
		// Also check Referer similarly when Origin is absent.
		if origin := r.Header.Get("Origin"); origin != "" {
			if !strings.HasPrefix(origin, "http://127.0.0.1") && !strings.HasPrefix(origin, "http://localhost") {
				http.Error(w, "Forbidden: invalid origin", http.StatusForbidden)
				return
			}
		} else if referer := r.Header.Get("Referer"); referer != "" {
			if !strings.HasPrefix(referer, "http://127.0.0.1") && !strings.HasPrefix(referer, "http://localhost") {
				http.Error(w, "Forbidden: invalid referer", http.StatusForbidden)
				return
			}
		}

		// Token check for all API endpoints (R8: loopback + per-run token on all APIs)
		if strings.HasPrefix(r.URL.Path, "/api/") {
			tok := r.Header.Get("X-CodeFlow-Token")
			if tok == "" {
				tok = r.URL.Query().Get("token")
			}
			if tok != s.authToken {
				http.Error(w, "Unauthorized: invalid token", http.StatusUnauthorized)
				return
			}
		}

		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(IndexHTML))
}

func (s *Server) handleListFlows(w http.ResponseWriter, r *http.Request) {
	idx, err := s.storage.ReadLatestIndex()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if idx == nil {
		_ = json.NewEncoder(w).Encode(map[string]any{"flows": []any{}})
		return
	}
	_ = json.NewEncoder(w).Encode(idx)
}

func (s *Server) handleGetFlow(w http.ResponseWriter, r *http.Request) {
	flowID := r.URL.Query().Get("id")
	if flowID == "" {
		http.Error(w, "missing flow id", http.StatusBadRequest)
		return
	}

	// Fast path: the generation cache holds this flow's decorated bytes.
	if gd := s.generationData(); gd != nil {
		if out, ok := gd.decorated[flowID]; ok {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(out)
			return
		}
	}

	data, err := s.storage.ReadActiveFlowSpec(flowID)
	if err != nil {
		http.Error(w, fmt.Sprintf("flow %s not found: %v", flowID, err), http.StatusNotFound)
		return
	}

	// Slow path fallback: classify solo so a spec listed in the index but
	// absent from the cache still renders. The stored spec is never modified.
	data = applyLayersWith(data, s.laneOverrides())

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(data)
}

// generationCache memoizes the per-generation work shared by /api/flow and
// /api/map: raw spec documents plus every flow's decorated bytes. A new
// generation or a modified manifest (lane overrides, possibly hand-edited)
// invalidates it; within one generation, requests are pure memory work.
type generationCache struct {
	genID         string
	manifestMtime time.Time
	manifestSize  int64
	docs          [][]byte          // published flow specs (index order)
	coverage      [][]byte          // synthetic specs from slice facts (map-only)
	decorated     map[string][]byte // flowID -> decorated spec JSON
}

// generationData returns the current cache, rebuilding it when the active
// generation changed or codeflow.flows.yaml was touched. Returns nil when no
// generation has been published yet.
func (s *Server) generationData() *generationCache {
	idx, err := s.storage.ReadLatestIndex()
	if err != nil || idx == nil {
		return nil
	}
	mt, sz := s.manifestStamp()

	s.mu.Lock()
	defer s.mu.Unlock()
	if c := s.genCache; c != nil &&
		c.genID == idx.GenerationID &&
		c.manifestMtime.Equal(mt) && c.manifestSize == sz {
		return c
	}

	docs := make([][]byte, 0, len(idx.Flows))
	for _, f := range idx.Flows {
		d, err := s.storage.ReadActiveFlowSpec(f.FlowID)
		if err != nil {
			continue // specs listed in the index but missing on disk are skipped
		}
		docs = append(docs, d)
	}
	gd := &generationCache{
		genID:         idx.GenerationID,
		manifestMtime: mt,
		manifestSize:  sz,
		docs:          docs,
		coverage:      synthesizeCoverageDocs(s.storage.ReadAllSliceCaches(maxCoverageFiles)),
		decorated:     decorateAll(docs, s.laneOverrides()),
	}
	s.genCache = gd
	return gd
}

// manifestStamp fingerprints codeflow.flows.yaml cheaply; a missing file
// yields zero values (the "no overrides" state).
func (s *Server) manifestStamp() (time.Time, int64) {
	info, err := os.Stat(filepath.Join(s.repoRoot, harvest.ManifestFileName))
	if err != nil {
		return time.Time{}, 0
	}
	return info.ModTime(), info.Size()
}

// laneOverrides loads manual symbol→lane assignments from the repo manifest.
// A missing or unreadable manifest degrades to "no overrides" — the map must
// render even when the override file is broken (the error is not silent at
// write time).
func (s *Server) laneOverrides() map[string]string {
	m, err := harvest.LoadManifest(s.repoRoot)
	if err != nil || m == nil {
		return nil
	}
	return m.LaneOverrideMap()
}

func (s *Server) handleGetMap(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	idx, err := s.storage.ReadLatestIndex()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	amap := &ArchitectureMap{Lanes: []MapLane{}, Components: []MapComponent{}, EntryPoints: []string{}, Relations: []MapRelation{}}
	if gd := s.generationData(); gd != nil && idx != nil {
		entryPoints := make([]string, 0, len(idx.Flows))
		for _, f := range idx.Flows {
			entryPoints = append(entryPoints, f.EntrySymbolPath)
		}
		allDocs := make([][]byte, 0, len(gd.docs)+len(gd.coverage))
		allDocs = append(allDocs, gd.docs...)
		allDocs = append(allDocs, gd.coverage...)
		amap = buildArchitectureMap(s.repoRoot, gd.genID, allDocs, s.laneOverrides(), entryPoints)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(amap)
}

func (s *Server) handlePostLaneOverride(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "read error", http.StatusBadRequest)
		return
	}
	var req struct {
		Symbol string `json:"symbol"`
		Lane   string `json:"lane"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Symbol) == "" {
		http.Error(w, "missing required field (symbol)", http.StatusBadRequest)
		return
	}
	if !validLayerName(req.Lane) {
		http.Error(w, fmt.Sprintf("invalid lane %q (allowed: %s)", req.Lane, strings.Join(LayerOrder, ", ")), http.StatusBadRequest)
		return
	}
	if err := harvest.WriteLaneOverride(s.repoRoot, req.Symbol, req.Lane); err != nil {
		http.Error(w, fmt.Sprintf("write override failed: %v", err), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "overridden", "symbol": req.Symbol, "lane": req.Lane})
}

func (s *Server) handleGetSource(w http.ResponseWriter, r *http.Request) {
	relPath := r.URL.Query().Get("path")
	if relPath == "" || filepath.IsAbs(relPath) {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}
	cleanRel := filepath.Clean(relPath)
	if strings.HasPrefix(cleanRel, ".."+string(filepath.Separator)) || cleanRel == ".." || strings.Contains(cleanRel, ".."+string(filepath.Separator)) {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}

	fullPath := filepath.Join(s.repoRoot, cleanRel)
	// Ensure the resolved path stays within repoRoot
	if rel, err := filepath.Rel(s.repoRoot, fullPath); err != nil || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}
	data, err := os.ReadFile(fullPath)
	if err != nil {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}

	// Lens slicing: the client passes the symbol-scoped view range
	// (codeLens.viewStartLine..viewEndLine) so readers see the flow a line
	// lives in, not a lone statement. Default cap 160 lines; mode=file returns
	// the whole file (capped) for the 파일 전체 view.
	content := string(data)
	lines := strings.Split(content, "\n")
	totalLines := len(lines)

	const (
		defaultLensLines = 160
		maxLensLines     = 400
		maxFileLines     = 2000
	)

	if r.URL.Query().Get("mode") == "file" {
		if totalLines > maxFileLines {
			content = strings.Join(lines[:maxFileLines], "\n")
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte(content))
		return
	}

	maxLines := defaultLensLines
	if v, err := strconv.Atoi(r.URL.Query().Get("maxLines")); err == nil && v > 0 {
		maxLines = v
	}
	if maxLines > maxLensLines {
		maxLines = maxLensLines
	}

	startLineStr := r.URL.Query().Get("startLine")
	endLineStr := r.URL.Query().Get("endLine")
	if startLineStr != "" || endLineStr != "" {
		startLine := 1
		endLine := totalLines
		if startLineStr != "" {
			if v, err := strconv.Atoi(startLineStr); err == nil {
				startLine = v
			} else {
				startLine = 1
			}
		}
		if endLineStr != "" {
			if v, err := strconv.Atoi(endLineStr); err == nil {
				endLine = v
			} else {
				endLine = totalLines
			}
		}
		if startLine < 1 {
			startLine = 1
		}
		if endLine > totalLines {
			endLine = totalLines
		}
		if startLine > endLine {
			startLine = endLine
		}
		// Cap the view window; widen asymmetrically is not needed — the client
		// centers on the focus lines when the symbol exceeds the cap.
		if endLine-startLine+1 > maxLines {
			endLine = startLine + maxLines - 1
			if endLine > totalLines {
				endLine = totalLines
			}
		}
		sliced := lines[startLine-1 : endLine]
		content = strings.Join(sliced, "\n")
	} else {
		// No explicit range: cap the window from the top.
		if totalLines > maxLines {
			content = strings.Join(lines[:maxLines], "\n")
		}
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(content))
}

func (s *Server) handleApprove(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		FlowID     string   `json:"flowId"`
		SymbolPath string   `json:"symbolPath"`
		Name       string   `json:"name"`
		Rules      []string `json:"rules"`
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read error", http.StatusBadRequest)
		return
	}

	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}

	if req.FlowID == "" || req.SymbolPath == "" || req.Name == "" {
		http.Error(w, "missing required fields (flowId, symbolPath, name)", http.StatusBadRequest)
		return
	}

	err = s.eventLog.Append(fusion.Event{
		Type:       fusion.EventStepApproved,
		FlowID:     req.FlowID,
		SymbolPath: req.SymbolPath,
		Name:       req.Name,
		Rules:      req.Rules,
		Author:     "flowview-user",
	})
	if err != nil {
		http.Error(w, fmt.Sprintf("append event failed: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"status": "approved",
		"flowId": req.FlowID,
	})
}

func (s *Server) handleTaskView(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	mode := r.URL.Query().Get("mode")
	if mode == "" {
		mode = "feature"
	}
	reqQuery := r.URL.Query().Get("query")
	if reqQuery == "" {
		reqQuery = r.URL.Query().Get("request")
	}
	flowID := r.URL.Query().Get("flowId")
	entrySymbol := r.URL.Query().Get("entrySymbol")
	domain := r.URL.Query().Get("domain")

	if r.Method == http.MethodPost {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err == nil {
			if m, ok := body["mode"].(string); ok && m != "" {
				mode = m
			}
			if f, ok := body["feature"].(map[string]any); ok {
				if q, ok := f["request"].(string); ok && q != "" {
					reqQuery = q
				}
				if fid, ok := f["flowId"].(string); ok && fid != "" {
					flowID = fid
				}
				if ent, ok := f["entrySymbol"].(string); ok && ent != "" {
					entrySymbol = ent
				}
				if dom, ok := f["domain"].(string); ok && dom != "" {
					domain = dom
				}
			}
		}
	}

	query := &semantic.TaskViewQuery{
		SchemaID:      "https://codeflow.local/schemas/task-view-query.schema.json",
		SchemaVersion: 1,
		Mode:          mode,
		Feature: &semantic.FeatureQueryParams{
			Request:     reqQuery,
			FlowID:      flowID,
			EntrySymbol: entrySymbol,
			Domain:      domain,
		},
	}

	qBytes, _ := json.Marshal(query)
	if err := contractharness.ValidateTaskViewQuery(qBytes); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code":    semantic.ErrCodeMissingPrecondition,
			"message": err.Error(),
		})
		return
	}

	ctx := r.Context()
	det := detect.Detect(s.repoRoot)
	lang := det.Language
	if lang == "" || lang == "unknown" {
		lang = "typescript"
	}
	adapterCfg, err := harvest.ResolveAdapter(lang, "")
	if err != nil {
		http.Error(w, fmt.Sprintf("resolve adapter: %v", err), http.StatusInternalServerError)
		return
	}
	pool := protocol.NewPool(adapterCfg, 2)
	defer pool.Close()

	harvester := harvest.NewRunnerWithPool(pool)
	candidates, err := harvester.Run(ctx, s.repoRoot)
	if err != nil {
		http.Error(w, fmt.Sprintf("harvest candidates: %v", err), http.StatusInternalServerError)
		return
	}

	resolved, err := semantic.ResolveFeatureQueryTarget(query, candidates)
	if err != nil {
		var qErr *semantic.QueryError
		if errors.As(err, &qErr) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code":             qErr.Code,
				"message":          qErr.Message,
				"candidateTargets": qErr.CandidateTargets,
			})
			return
		}
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	slicer := slicing.NewRunner(pool)
	slicePayload, err := slicer.Slice(ctx, s.repoRoot, resolved.CandidateID, resolved.EntrySymbolPath, nil)
	if err != nil {
		http.Error(w, fmt.Sprintf("slice error: %v", err), http.StatusInternalServerError)
		return
	}

	reqText := reqQuery
	if reqText == "" {
		reqText = resolved.Title
	}

	intent, err := semantic.NormalizeTaskIntent(reqText, semantic.IntentOptions{Mode: mode})
	if err != nil {
		http.Error(w, fmt.Sprintf("normalize intent: %v", err), http.StatusInternalServerError)
		return
	}

	mapIR, proj, err := semantic.CompileDeterministicFeatureMap(resolved, intent, slicePayload, semantic.CompileOptions{
		ComputedBasisID: slicePayload.ComputedBasisID,
		WorkspaceEpoch:  slicePayload.WorkspaceEpoch,
	})
	if err != nil {
		http.Error(w, fmt.Sprintf("compile map: %v", err), http.StatusInternalServerError)
		return
	}

	liveHead := s.engine.LiveHead()
	var delta *workspace.WorkspaceDelta
	if liveHead != nil && slicePayload.ComputedBasisID != "" {
		delta, _ = s.engine.ComputeDelta(slicePayload.ComputedBasisID, liveHead.SnapshotID)
	}

	docRevRefs := []string{}
	for _, st := range mapIR.Steps {
		if st.Anchor.RepoRelativePath != "" {
			docRevRefs = append(docRevRefs, st.Anchor.RepoRelativePath)
		}
	}

	normHashBytes := sha256.Sum256([]byte(reqText))
	normHash := hex.EncodeToString(normHashBytes[:])

	closure := &semantic.CausalObservationClosure{
		SchemaID:            "https://codeflow.local/schemas/causal-observation-closure.schema.json",
		SchemaVersion:       1,
		ClosureID:           fmt.Sprintf("closure-%s", mapIR.GenerationID),
		ComputedBasisID:     mapIR.ComputedBasisID,
		TaskIntentRevision:  mapIR.Task.IntentRevision,
		NormalizedQueryHash: normHash,
		AnalysisReadSetID:   fmt.Sprintf("readset-%s", mapIR.GenerationID),
		PositiveDependencies: semantic.PositiveDependencies{
			DocumentRevisionRefs:     docRevRefs,
			ConfigurationFingerprint: "cfg-live-fingerprint",
		},
		ClosureStatus: "closed",
		ClosureDigest: normHash,
	}

	gateRes, gap := s.gate.Evaluate(mapIR, closure, delta, liveHead, intent)
	settleRes := s.gate.EvaluateSettlement(mapIR)
	mapIR.Settlement = settleRes.Gate

	var manifest *storage.GenerationProofManifest
	if gateRes.Eligibility == "passed" {
		expectedPrevGen := ""
		if prevPtr, _ := s.storage.ReadActivePointer(); prevPtr != nil {
			expectedPrevGen = prevPtr.GenerationID
		}
		liveHeadSnapID := ""
		if liveHead != nil {
			liveHeadSnapID = liveHead.SnapshotID
		}
		now := time.Now().UTC()
		manifest = &storage.GenerationProofManifest{
			SchemaID:                   "https://codeflow.local/schemas/generation-proof-manifest.schema.json",
			SchemaVersion:              1,
			ProofID:                    fmt.Sprintf("proof-%s", mapIR.GenerationID),
			GenerationID:               mapIR.GenerationID,
			ComputedBasisID:            mapIR.ComputedBasisID,
			ValidatedAgainstSnapshotID: liveHeadSnapID,
			TaskIntentRevision:         mapIR.Task.IntentRevision,
			NormalizedQueryHash:        closure.NormalizedQueryHash,
			AnalysisReadSetID:          closure.AnalysisReadSetID,
			CausalObservationClosureID: closure.ClosureID,
			CurrentPublication: storage.CurrentPublicationResult{
				Eligibility:           gateRes.Eligibility,
				SnapshotGate:          gateRes.SnapshotGate,
				ClosureGate:           gateRes.ClosureGate,
				EvidenceGate:          gateRes.EvidenceGate,
				SemanticAtomicityGate: gateRes.SemanticAtomicityGate,
				TaskRelevanceGate:     gateRes.TaskRelevanceGate,
				ComprehensionGate:     gateRes.ComprehensionGate,
			},
			SettlementEvaluation: storage.SettlementEvaluation{
				Gate:                   settleRes.Gate,
				EvaluatedAt:            settleRes.EvaluatedAt,
				BlockingObligationRefs: settleRes.BlockingObligationRefs,
			},
			ArtifactRefs: storage.ArtifactRefs{
				SemanticMap: fmt.Sprintf("cas:sha256:%s", mapIR.GenerationID),
			},
			ExpectedLiveHeadSnapshotID:   liveHeadSnapID,
			ExpectedPreviousGenerationID: &expectedPrevGen,
			PublishedAt:                  now,
		}

		casRef, _ := s.storage.WriteManifestCAS(manifest)
		activePtr := &storage.ActivePointer{
			SchemaID:                     "https://codeflow.local/schemas/active-pointer.schema.json",
			SchemaVersion:                1,
			GenerationID:                 mapIR.GenerationID,
			ManifestObjectRef:            casRef,
			PublishedAt:                  now,
			ComputedBasisID:              mapIR.ComputedBasisID,
			ValidatedAgainstSnapshotID:   liveHeadSnapID,
			ExpectedLiveHeadSnapshotID:   liveHeadSnapID,
			ExpectedPreviousGenerationID: &expectedPrevGen,
			WorkspaceEpoch:               fmt.Sprintf("epoch-%d", slicePayload.WorkspaceEpoch),
			TaskIntentRevision:           mapIR.Task.IntentRevision,
			NormalizedQueryHash:          closure.NormalizedQueryHash,
			FlowCount:                    1,
		}
		_ = s.storage.CompareAndSwapActivePointer(liveHeadSnapID, expectedPrevGen, activePtr)

		s.hub.Publish("generation.published", map[string]any{
			"generationId": mapIR.GenerationID,
			"manifest":     manifest,
			"proofId":      manifest.ProofID,
			"settlement":   mapIR.Settlement,
			"qualityStage": mapIR.Quality.Stage,
		}, &mapIR.ComputedBasisID, &liveHeadSnapID, &mapIR.GenerationID)
	} else {
		mapIR.Freshness = "last_verified"
		if curAct := s.engine.CurrentActivity(); gap != nil {
			gap.AnalysisLagMs = curAct.AnalysisLagMs
			gap.PendingRevisions = curAct.PendingRevisions
		}
		s.hub.Publish("generation.gap", gap, &mapIR.ComputedBasisID, nil, &mapIR.GenerationID)
	}

	s.mu.Lock()
	if s.mapCache == nil {
		s.mapCache = make(map[string]*semantic.SemanticMapIR)
	}
	s.mapCache[mapIR.GenerationID] = mapIR
	s.mapCache[mapIR.ComputedBasisID] = mapIR
	s.mapCache["active"] = mapIR
	s.mu.Unlock()

	evidenceRecords, _ := semantic.ExtractAndRedactEvidence(resolved, slicePayload, s.repoRoot)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"currentAnswer": map[string]string{
			"requested": mapIR.Summary.Requested,
			"current":   mapIR.Summary.Current,
		},
		"taskIntent":      intent,
		"semanticMap":     mapIR,
		"projection":      proj,
		"evidence":        evidenceRecords,
		"unknowns":        mapIR.Unknowns,
		"publicationGate": gateRes,
		"proofManifest":   manifest,
		"verifiedGap":     gap,
	})
}

func (s *Server) handleWorkspaceActivity(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	act := s.engine.CurrentActivity()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(act)
}

func (s *Server) handleWorkspaceEdit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Path            string `json:"path"`
		Content         string `json:"content"`
		DocumentVersion int    `json:"documentVersion"`
		Source          string `json:"source"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.Source == "" {
		req.Source = workspace.SourceAgentTransaction
	}
	rev, snap, err := s.engine.ApplyVersionedEdit(r.Context(), workspace.EditRequest{
		Path:            req.Path,
		Content:         []byte(req.Content),
		DocumentVersion: req.DocumentVersion,
		Source:          req.Source,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	s.scheduler.NotifyEdit(snap)
	act := s.engine.CurrentActivity()
	s.hub.Publish("activity.updated", act, &snap.ComputedBasisID, &snap.SnapshotID, nil)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"revision": rev,
		"snapshot": snap,
	})
}

func (s *Server) handleWorkspaceStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	lastEventID := r.Header.Get("Last-Event-ID")
	if lastEventID == "" {
		lastEventID = r.URL.Query().Get("lastEventId")
	}

	ch, replay, needsSync, cancel := s.hub.Subscribe(lastEventID)
	defer cancel()

	if needsSync {
		activeManifest, _ := s.storage.ReadActiveProofManifest()
		curAct := s.engine.CurrentActivity()
		syncEnv := &semantic.EventEnvelope{
			SchemaID:      "https://codeflow.local/schemas/event-envelope.schema.json",
			SchemaVersion: 1,
			StreamID:      s.hub.streamID,
			Sequence:      0,
			EventID:       "sync-0",
			EventType:     "snapshot_sync",
			OccurredAt:    time.Now().UTC(),
			Data: map[string]any{
				"activeManifest": activeManifest,
				"activity":       curAct,
			},
		}
		if formatted, err := FormatSSE(syncEnv); err == nil {
			_, _ = w.Write(formatted)
			flusher.Flush()
		}
	} else if len(replay) > 0 {
		for _, env := range replay {
			if formatted, err := FormatSSE(env); err == nil {
				_, _ = w.Write(formatted)
			}
		}
		flusher.Flush()
	}

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case env, ok := <-ch:
			if !ok {
				return
			}
			if formatted, err := FormatSSE(env); err == nil {
				_, _ = w.Write(formatted)
				flusher.Flush()
			}
		}
	}
}

func (s *Server) handleWorkspaceProof(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	manifest, err := s.storage.ReadActiveProofManifest()
	if err != nil {
		http.Error(w, fmt.Sprintf("read proof manifest: %v", err), http.StatusInternalServerError)
		return
	}
	ptr, err := s.storage.ReadActivePointer()
	if err != nil {
		http.Error(w, fmt.Sprintf("read active pointer: %v", err), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"manifest": manifest,
		"pointer":  ptr,
	})
}

// handleTaskReview implements GET /api/task/review (Raw §8.2, §8.5, VS-05).
func (s *Server) handleTaskReview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	baseline := r.URL.Query().Get("baseline")
	current := r.URL.Query().Get("current")
	if baseline == "" || current == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code":    "missing_precondition",
			"message": "baseline and current parameters are required",
		})
		return
	}

	s.mu.Lock()
	baseMap := s.mapCache[baseline]
	currMap := s.mapCache[current]
	s.mu.Unlock()

	if baseMap == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code":    "missing_precondition",
			"message": fmt.Sprintf("baseline generation or basis %q not found", baseline),
		})
		return
	}
	if currMap == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code":    "missing_precondition",
			"message": fmt.Sprintf("current generation or basis %q not found", current),
		})
		return
	}

	compID := fmt.Sprintf("comp-%s-%s", baseline, current)
	delta, err := semantic.ComputeSemanticDelta(compID, baseMap, currMap)
	if err != nil {
		if errors.Is(err, semantic.ErrIncomparableBasis) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnprocessableEntity)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code":    "incomparable_basis",
				"message": err.Error(),
			})
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	criteria := []semantic.AcceptanceCriterion{}
	critSet := make(map[string]bool)
	for _, st := range currMap.Steps {
		for _, rule := range st.Rules {
			if !critSet[rule] && strings.HasPrefix(strings.ToUpper(rule), "AC-") {
				critSet[rule] = true
				criteria = append(criteria, semantic.AcceptanceCriterion{
					ID:   rule,
					Text: fmt.Sprintf("요구사항 %s 검증", rule),
				})
			}
		}
	}
	if len(criteria) == 0 {
		criteria = append(criteria, semantic.AcceptanceCriterion{
			ID:   "AC-1",
			Text: "기능 기본 동작 및 핵심 흐름 검증",
		})
	}

	alignments := semantic.ComputeRequirementAlignment(criteria, currMap, semantic.AlignmentOptions{})

	changePulse := make([]map[string]any, 0, len(delta.Changes))
	for _, ch := range delta.Changes {
		changePulse = append(changePulse, map[string]any{
			"time":            time.Now().UTC().Format("15:04:05"),
			"summary":         ch.Summary,
			"kind":            ch.Kind,
			"targetStepId":    ch.TargetStepID,
			"epistemicStatus": ch.EpistemicStatus,
		})
	}

	resp := map[string]any{
		"semanticDelta":        delta,
		"requirementAlignment": alignments,
		"changePulse":          changePulse,
		"structuralSummary":    delta.StructuralSummary,
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}


