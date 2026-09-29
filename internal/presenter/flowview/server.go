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
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"codeflow/internal/analyzer/detect"
	"codeflow/internal/analyzer/protocol"
	"codeflow/internal/analyzer/workspace"
	"codeflow/internal/collector/contractharness"
	"codeflow/internal/collector/fusion"
	"codeflow/internal/collector/harvest"
	"codeflow/internal/collector/secret"
	"codeflow/internal/collector/slicing"
	"codeflow/internal/collector/storage"
	"codeflow/internal/curator/semantic"
)

// Server coordinates the loopback HTTP server for FlowView.
type Server struct {
	repoRoot            string
	approvalWorkspaceID string
	storage             *storage.Storage
	eventLog            *fusion.EventLog
	engine              *workspace.SnapshotEngine
	authToken           string
	listener            net.Listener
	httpServer          *http.Server
	mu                  sync.Mutex
	addr                string
	genCache            *generationCache
	taskViewRecords     map[string]taskViewRecord
	taskViewOrder       []string
	gate                *semantic.PublicationGate
	mapCache            map[string]*semantic.SemanticMapIR
	flowContextMetadata map[string]flowContextGeneration

	watchInterval time.Duration

	startOnce          sync.Once
	shutdownMu         sync.Mutex
	shutdownStarted    bool
	shutdownInitDone   chan struct{}
	shutdownResultDone chan struct{}
	shutdownInitErr    error
	shutdownFirstErr   error

	projectMu         sync.RWMutex
	livePrototype     bool
	svelteUI          bool
	adapterRegistry   *protocol.AdapterRegistry
	adapterRegistryMu sync.Mutex
}

// Config configures the FlowView server.
type Config struct {
	RepoRoot  string
	Port      int
	AuthToken string

	WorkspaceWatchInterval time.Duration
	Mode                   string
	LivePrototype          bool
	SvelteUI               bool
}

// NewServer initializes a FlowView server instance.
func NewServer(cfg Config) (*Server, error) {
	st := storage.New(cfg.RepoRoot)
	if err := st.InitLayout(); err != nil {
		return nil, fmt.Errorf("init storage: %w", err)
	}
	if err := st.RecoverPendingPublication(); err != nil {
		return nil, fmt.Errorf("recover live publication: %w", err)
	}

	token := cfg.AuthToken
	if token == "" {
		b := make([]byte, 16)
		_, _ = rand.Read(b)
		token = hex.EncodeToString(b)
	}

	engine, err := workspace.NewSnapshotEngine(cfg.RepoRoot, 0)
	if err != nil {
		return nil, fmt.Errorf("init snapshot engine: %w", err)
	}
	identity := defaultLiveIdentity(cfg.RepoRoot)
	currentIdentity := engine.WorkspaceIdentity()
	if currentIdentity == (workspace.WorkspaceIdentity{}) {
		if err := engine.BindWorkspaceIdentity(identity); err != nil {
			if _, transitionErr := engine.SetWorkspaceIdentity(identity); transitionErr != nil {
				return nil, fmt.Errorf("bind workspace identity: %w", err)
			}
		}
	} else if currentIdentity != identity {
		if _, err := engine.SetWorkspaceIdentity(identity); err != nil {
			return nil, fmt.Errorf("set workspace identity: %w", err)
		}
	}

	gate := semantic.NewPublicationGate()

	s := &Server{
		repoRoot:            cfg.RepoRoot,
		approvalWorkspaceID: identity.WorktreeID,
		storage:             st,
		eventLog:            fusion.NewEventLog(cfg.RepoRoot),
		engine:              engine,
		authToken:           token,
		gate:                gate,
		mapCache:            make(map[string]*semantic.SemanticMapIR),
		watchInterval:       cfg.WorkspaceWatchInterval,
		livePrototype:       cfg.LivePrototype,
		svelteUI:            cfg.SvelteUI,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", s.serveIndex)
	mux.HandleFunc("/api/flows", s.serveListFlows)
	mux.HandleFunc("/api/views", s.serveTaskViewList)
	mux.HandleFunc("/api/view", s.serveTaskViewDetail)
	mux.HandleFunc("/api/view/reading-path", s.serveCodeReadingPath)
	mux.HandleFunc("/api/view/compare", s.serveTaskViewComparison)
	mux.HandleFunc("/api/flow", s.serveGetFlow)
	mux.HandleFunc("/api/flow/context", s.serveFlowContext)
	mux.HandleFunc("/api/candidate/preview", s.serveCandidatePreview)
	mux.HandleFunc("/api/source", s.serveGetSource)
	mux.HandleFunc("/api/approve", s.serveApprove)
	mux.HandleFunc("/api/task/view", s.serveTaskView)
	mux.HandleFunc("/api/task/impact", s.serveTaskImpact)
	mux.HandleFunc("/api/semantic/labels", s.serveSemanticLabels)

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

// SnapshotEngine exposes the single workspace lineage.
func (s *Server) SnapshotEngine() *workspace.SnapshotEngine {
	if s == nil {
		return nil
	}
	return s.engine
}

// CurrentActivity returns the workspace activity view.
func (s *Server) CurrentActivity() workspace.ActivityStatus {
	if s == nil || s.engine == nil {
		return workspace.ActivityStatus{SchemaID: "https://codeflow.local/schemas/rflsc.activity-state.v2.schema.json", SchemaVersion: 2, Activity: "idle", AnalysisLagMs: -1, PendingRevisions: 0, Timestamp: time.Now().UTC()}
	}
	return s.engine.CurrentActivity()
}

// RememberTaskQuery binds the latest feature query parameters.
func (s *Server) RememberTaskQuery(query *semantic.TaskViewQuery, requestText string) error {
	if s == nil || query == nil || query.Feature == nil {
		return fmt.Errorf("feature query is required")
	}
	return nil
}

// SubmitVersionedEdit submits a versioned document edit to the workspace snapshot engine.
func (s *Server) SubmitVersionedEdit(ctx context.Context, edit workspace.EditRequest) (*workspace.DocumentRevision, *workspace.WorkspaceSnapshot, error) {
	if edit.Source == "" {
		edit.Source = workspace.SourceIDEVersioned
	}
	digest := sha256.Sum256(append(append([]byte(edit.Source+"\x00"+edit.Path+"\x00"+strconv.Itoa(edit.DocumentVersion)+"\x00"), edit.Content...), byte(0)))
	result, err := s.SubmitVersionedChanges(ctx, workspace.VersionedChangeRequest{
		BatchID: "single-" + hex.EncodeToString(digest[:16]),
		Source:  edit.Source,
		Changes: []workspace.VersionedChange{{Kind: workspace.ChangeUpsert, Path: edit.Path, Content: edit.Content, DocumentVersion: edit.DocumentVersion}},
	})
	if err != nil {
		return nil, nil, err
	}
	if len(result.Revisions) == 0 {
		return nil, nil, fmt.Errorf("accepted edit has no document revision")
	}
	return result.Revisions[0], result.Snapshot, nil
}

// SubmitVersionedChanges applies versioned changes to the snapshot engine.
func (s *Server) SubmitVersionedChanges(ctx context.Context, request workspace.VersionedChangeRequest) (*workspace.VersionedChangeResult, error) {
	if s == nil || s.engine == nil {
		return nil, fmt.Errorf("snapshot engine is unavailable")
	}
	return s.engine.ApplyVersionedChanges(ctx, request)
}

// Start runs the HTTP server in the background.
func (s *Server) Start() {
	s.startOnce.Do(func() {
		go func() { _ = s.httpServer.Serve(s.listener) }()
	})
}

// Handler returns the underlying http.Handler for testing or mounting.
func (s *Server) Handler() http.Handler {
	if s == nil || s.httpServer == nil {
		return nil
	}
	return s.httpServer.Handler
}

// Shutdown gracefully terminates the server.
func (s *Server) Shutdown(ctx context.Context) error {
	if s == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
	}
	s.shutdownMu.Lock()
	if !s.shutdownStarted {
		s.shutdownStarted = true
		s.shutdownInitDone = make(chan struct{})
		s.shutdownResultDone = make(chan struct{})
		initDone := s.shutdownInitDone
		s.shutdownMu.Unlock()

		var initErrors []error
		if s.httpServer != nil {
			if shutdownErr := s.httpServer.Shutdown(ctx); shutdownErr != nil {
				_ = s.httpServer.Close()
				initErrors = append(initErrors, shutdownErr)
			}
		}
		initErr := errors.Join(initErrors...)
		s.shutdownMu.Lock()
		s.shutdownInitErr = initErr
		close(initDone)
		s.shutdownMu.Unlock()

		s.closeAdapterRegistry()
		s.shutdownMu.Lock()
		s.shutdownFirstErr = initErr
		close(s.shutdownResultDone)
		s.shutdownMu.Unlock()
		return initErr
	}
	resultDone := s.shutdownResultDone
	s.shutdownMu.Unlock()
	select {
	case <-resultDone:
	case <-ctx.Done():
		return ctx.Err()
	}

	s.shutdownMu.Lock()
	firstErr := s.shutdownFirstErr
	s.shutdownMu.Unlock()
	var retryErrors []error
	if s.httpServer != nil {
		if shutdownErr := s.httpServer.Shutdown(ctx); shutdownErr != nil {
			retryErrors = append(retryErrors, shutdownErr)
		}
	}
	return errors.Join(append([]error{firstErr}, retryErrors...)...)
}

func (s *Server) closeAdapterRegistry() {
	s.adapterRegistryMu.Lock()
	defer s.adapterRegistryMu.Unlock()
	if s.adapterRegistry != nil {
		s.adapterRegistry.Close()
		s.adapterRegistry = nil
	}
}

// securityMiddleware enforces loopback Host validation, Origin/Referer CSRF check
// and per-run token verification.
func (s *Server) securityMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isAllowedLoopbackHost(r.Host) {
			http.Error(w, "Forbidden: invalid host", http.StatusForbidden)
			return
		}

		if origin := r.Header.Get("Origin"); origin != "" {
			if !isAllowedLoopbackURL(origin) {
				http.Error(w, "Forbidden: invalid origin", http.StatusForbidden)
				return
			}
		} else if referer := r.Header.Get("Referer"); referer != "" {
			if !isAllowedLoopbackURL(referer) {
				http.Error(w, "Forbidden: invalid referer", http.StatusForbidden)
				return
			}
		}

		if strings.HasPrefix(r.URL.Path, "/api/") {
			tok := requestAuthToken(r)
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

func isAllowedLoopbackHost(host string) bool {
	h := host
	if strings.Contains(h, ":") {
		var err error
		h, _, err = net.SplitHostPort(h)
		if err != nil {
			return false
		}
	}
	return h == "127.0.0.1" || h == "localhost" || h == "::1"
}

func isAllowedLoopbackURL(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	return isAllowedLoopbackHost(u.Host)
}

func requestAuthToken(r *http.Request) string {
	if tok := r.Header.Get("X-CodeFlow-Token"); tok != "" {
		return tok
	}
	if tok := r.Header.Get("X-Auth-Token"); tok != "" {
		return tok
	}
	if tok := r.Header.Get("Authorization"); strings.HasPrefix(tok, "Bearer ") {
		return strings.TrimPrefix(tok, "Bearer ")
	}
	return r.URL.Query().Get("token")
}

func (s *Server) serveIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" && r.URL.Path != "/live" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(SvelteFlowViewHTML))
}

func (s *Server) serveListFlows(w http.ResponseWriter, r *http.Request) {
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

func (s *Server) serveGetFlow(w http.ResponseWriter, r *http.Request) {
	flowID := r.URL.Query().Get("id")
	if flowID == "" {
		http.Error(w, "missing flow id", http.StatusBadRequest)
		return
	}

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

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(data)
}

type generationCache struct {
	genID         string
	manifestMtime time.Time
	manifestSize  int64
	docs          [][]byte
	decorated     map[string][]byte
}

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
	decorated := make(map[string][]byte, len(idx.Flows))
	for _, f := range idx.Flows {
		d, err := s.storage.ReadActiveFlowSpec(f.FlowID)
		if err != nil {
			continue
		}
		docs = append(docs, d)
		decorated[f.FlowID] = d
	}
	gd := &generationCache{
		genID:         idx.GenerationID,
		manifestMtime: mt,
		manifestSize:  sz,
		docs:          docs,
		decorated:     decorated,
	}
	s.genCache = gd
	return gd
}

func (s *Server) manifestStamp() (time.Time, int64) {
	info, err := os.Stat(filepath.Join(s.repoRoot, harvest.ManifestFileName))
	if err != nil {
		return time.Time{}, 0
	}
	return info.ModTime(), info.Size()
}

func (s *Server) laneOverrides() map[string]string {
	m, err := harvest.LoadManifest(s.repoRoot)
	if err != nil || m == nil {
		return nil
	}
	return m.LaneOverrideMap()
}

func (s *Server) serveGetSource(w http.ResponseWriter, r *http.Request) {
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

	snapshot, _, releaseSnapshot, err := s.captureAnalysisSnapshot(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer releaseSnapshot()
	data, ok := snapshotSourceBytes(snapshot)[filepath.ToSlash(cleanRel)]
	if !ok {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}

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
		content = secret.Redact(content).Text
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
		if endLine-startLine+1 > maxLines {
			endLine = startLine + maxLines - 1
			if endLine > totalLines {
				endLine = totalLines
			}
		}
		sliced := lines[startLine-1 : endLine]
		content = strings.Join(sliced, "\n")
	} else {
		if totalLines > maxLines {
			content = strings.Join(lines[:maxLines], "\n")
		}
	}

	content = secret.Redact(content).Text
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(content))
}

func (s *Server) serveFlowContext(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	stepID := r.URL.Query().Get("stepId")
	if stepID == "" {
		http.Error(w, "missing required query parameter: stepId", http.StatusBadRequest)
		return
	}

	genID := r.URL.Query().Get("generationId")
	expandStr := r.URL.Query().Get("expand")
	expansion := FlowExpansionScope(expandStr)
	if expansion == "" {
		expansion = ExpansionFlowContext
	}

	s.mu.Lock()
	mapIR := s.mapCache[genID]
	metadata := s.flowContextMetadata[genID]
	s.mu.Unlock()
	if genID == "" || (expansion != ExpansionFlowContext && expansion != ExpansionCallable && expansion != ExpansionFile) {
		http.Error(w, "generationId and valid expansion scope are required", http.StatusBadRequest)
		return
	}

	if mapIR == nil || mapIR.GenerationID != genID {
		http.Error(w, "no semantic map available", http.StatusNotFound)
		return
	}

	var targetStep *semantic.SemanticStep
	for i := range mapIR.Steps {
		if mapIR.Steps[i].StepID == stepID {
			targetStep = &mapIR.Steps[i]
			break
		}
	}
	if targetStep == nil {
		http.Error(w, fmt.Sprintf("step %q not found", stepID), http.StatusNotFound)
		return
	}

	snapshotID := flowSourceSnapshotID(mapIR)
	files := map[string][]byte{}
	if lease, err := s.engine.SnapshotVFS(snapshotID); err == nil {
		defer lease.Close()
		if data, err := lease.ReadFile(targetStep.Anchor.RepoRelativePath); err == nil {
			files[targetStep.Anchor.RepoRelativePath] = data
		}
	}

	flowCtx := DeriveFlowContext(DeriveFlowContextParams{
		Step:                  *targetStep,
		SemanticMap:           mapIR,
		SnapshotFiles:         files,
		SourceSnapshotID:      snapshotID,
		AdapterHasFlowContext: metadata.Capability && metadata.SnapshotID == snapshotID,
		Metadata:              metadata.Steps[stepID],
		Expansion:             expansion,
	})

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(flowCtx)
}

func (s *Server) serveApprove(w http.ResponseWriter, r *http.Request) {
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

func writeTaskViewError(w http.ResponseWriter, err error, status int) {
	code := "internal_error"
	message := ""
	var candidateTargets []string
	if err != nil {
		message = err.Error()
		var qErr *semantic.QueryError
		if errors.As(err, &qErr) {
			code = qErr.Code
			message = qErr.Message
			candidateTargets = qErr.CandidateTargets
			if qErr.Code == "no_entrypoints_found" {
				status = http.StatusBadRequest
			}
		} else if errors.Is(err, errUnsupportedFlowSequenceSchema) {
			code = errUnsupportedFlowSequenceSchema.Error()
		} else {
			for _, candidate := range []string{"no_entrypoints_found", "missing_precondition", "invalid_precondition", "invalid_evidence", "unresolved_target", "ambiguous_target", "incomparable_basis", "analysis_failed", "unavailable", "unknown", "conflict"} {
				if strings.HasPrefix(message, candidate+":") || message == candidate {
					code = candidate
					if candidate == "no_entrypoints_found" {
						status = http.StatusBadRequest
					}
					break
				}
			}
		}
	}
	if status == 0 {
		status = http.StatusInternalServerError
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	resp := map[string]any{"code": code, "error": code, "message": message}
	if len(candidateTargets) > 0 {
		resp["candidateTargets"] = candidateTargets
	}
	_ = json.NewEncoder(w).Encode(resp)
}

func writeTaskAnalysisFailure(w http.ResponseWriter, rawRequest string, target *semantic.ResolvedTarget, cause error) {
	targetPath := ""
	if target != nil {
		targetPath = target.EntrySymbolPath
	}
	requestText := strings.TrimSpace(rawRequest)
	if requestText == "" {
		requestText = targetPath
	}
	reason := "선택한 코드의 분석 근거를 확인할 수 없습니다."
	reasonCode := "source_proof_unavailable"
	nextAction := "진입점과 해당 코드 범위를 확인한 뒤 명시적으로 다시 분석하세요."
	message := ""
	if cause != nil {
		message = cause.Error()
	}
	switch {
	case strings.Contains(message, "entry_symbol_not_found:"):
		reasonCode = "entry_symbol_not_found"
		reason = "현재 코드에서 선택한 진입 메서드를 찾지 못했습니다."
		nextAction = "현재 코드에서 진입점을 다시 선택한 뒤 분석하세요."
	case strings.Contains(message, "entry_symbol_has_no_executable_steps:"):
		reasonCode = "entry_symbol_has_no_executable_steps"
		reason = "선택한 진입 메서드에서 실행 단계를 찾지 못했습니다."
	case strings.Contains(message, "entry_symbol_unsupported_control_flow:"):
		reasonCode = "entry_symbol_unsupported_control_flow"
		reason = "선택한 진입 메서드에 분석기가 지원하지 않는 제어 흐름이 있습니다."
		nextAction = "지원되지 않은 제어 흐름을 확인하고 다른 진입점을 선택하거나 분석 지원을 보완한 뒤 다시 분석하세요."
	case strings.Contains(message, "selected source is not highlighted"), strings.Contains(message, "exact CodeView source context is unavailable"):
		reasonCode = "codeview_source_unavailable"
		reason = "관문 대표 코드를 CodeView에서 정확히 표시할 수 없습니다."
	case strings.Contains(message, "whole-file fallback range"):
		reasonCode = "whole_file_fallback"
		reason = "선택한 코드 근거가 문장 대신 파일 전체를 가리킵니다."
	case strings.Contains(message, "source range or hash does not match"), strings.Contains(message, "source file"):
		reasonCode = "snapshot_source_mismatch"
		reason = "분석한 코드 위치가 현재 스냅샷의 소스와 일치하지 않습니다."
	case strings.Contains(message, "no entry step"), strings.Contains(message, "no executable steps"):
		reasonCode = "no_executable_steps"
		reason = "요청 흐름의 실행 단계를 확인하지 못했습니다."
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnprocessableEntity)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"code":       "analysis_failed",
		"error":      "analysis_failed",
		"message":    "선택한 요청 흐름을 분석하지 못했습니다: " + reason,
		"target":     targetPath,
		"reasonCode": reasonCode,
		"reason":     reason,
		"nextAction": nextAction,
		"flowResolution": map[string]any{
			"schemaVersion":   1,
			"status":          "analysis_failed",
			"rawRequest":      secret.Redact(requestText).Text,
			"entrySymbolPath": targetPath,
			"failure": map[string]any{
				"target":     targetPath,
				"reasonCode": reasonCode,
				"reason":     reason,
				"nextAction": nextAction,
			},
		},
	})
}

type taskViewRecord struct {
	inputHash    string
	basisID      string
	generationID string
	payload      []byte
}

func taskViewInputHash(mode, query, flowID, entrySymbol, domain, focusSymbol, focusReason string) string {
	raw := strings.Join([]string{mode, query, flowID, entrySymbol, domain, focusSymbol, focusReason}, "\x00")
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func (s *Server) lookupTaskViewRecord(requestID, inputHash, basisID string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.taskViewRecords == nil {
		return nil, nil
	}
	rec, ok := s.taskViewRecords[requestID]
	if !ok {
		return nil, nil
	}
	if rec.inputHash != inputHash {
		return nil, errors.New("conflict: request ID was reused with different input")
	}
	if rec.basisID != basisID || len(rec.payload) == 0 {
		return nil, nil
	}
	out := make([]byte, len(rec.payload))
	copy(out, rec.payload)
	return out, nil
}

func (s *Server) storeTaskViewRecord(requestID, inputHash, basisID, generationID string, payload []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.taskViewRecords == nil {
		s.taskViewRecords = make(map[string]taskViewRecord)
	}
	if _, ok := s.taskViewRecords[requestID]; !ok {
		s.taskViewOrder = append(s.taskViewOrder, requestID)
	}
	dup := make([]byte, len(payload))
	copy(dup, payload)
	s.taskViewRecords[requestID] = taskViewRecord{inputHash: inputHash, basisID: basisID, generationID: generationID, payload: dup}
	for len(s.taskViewOrder) > 32 {
		oldest := s.taskViewOrder[0]
		s.taskViewOrder = s.taskViewOrder[1:]
		if _, ok := s.taskViewRecords[oldest]; ok && oldest != requestID {
			delete(s.taskViewRecords, oldest)
		}
	}
}

func newTaskViewRequestID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("req-%d", time.Now().UTC().UnixNano())
	}
	return "req-" + hex.EncodeToString(b[:])
}

func flowRequestCandidateDescription(candidate harvest.Candidate) string {
	if candidate.IntentSignals.DocLine != nil && strings.TrimSpace(*candidate.IntentSignals.DocLine) != "" {
		return *candidate.IntentSignals.DocLine
	}
	for _, value := range []string{candidate.IntentSignals.DerivedName, candidate.IntentSignals.ClassName, candidate.TriggerClass, candidate.EntrySymbolPath} {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return candidate.EntrySymbolPath
}

func resolveFlowRecord(rawRequest, explicitEntrySymbol string, resolved *semantic.ResolvedTarget, candidates []harvest.Candidate, snapshot protocol.Snapshot) (*fusion.FlowResolution, error) {
	if resolved == nil {
		return nil, errors.New("flow target is missing")
	}
	requestText := strings.TrimSpace(rawRequest)
	if requestText == "" {
		requestText = resolved.EntrySymbolPath
	}
	for _, candidate := range candidates {
		if candidate.CandidateID != resolved.CandidateID || candidate.EntrySymbolPath != resolved.EntrySymbolPath {
			continue
		}
		return &fusion.FlowResolution{
			SchemaVersion:   1,
			Status:          "resolved",
			RawRequest:      secret.Redact(requestText).Text,
			CandidateID:     candidate.CandidateID,
			EntrySymbolPath: candidate.EntrySymbolPath,
			FlowID:          fusion.ComputeFlowID(candidate.EntrySymbolPath),
			Evidence: []fusion.FlowEvidence{{
				CandidateID:     candidate.CandidateID,
				EntrySymbolPath: candidate.EntrySymbolPath,
				Description:     flowRequestCandidateDescription(candidate),
				SnapshotID:      snapshot.SnapshotID,
				ComputedBasisID: snapshot.ComputedBasisID,
			}},
		}, nil
	}
	if strings.TrimSpace(explicitEntrySymbol) != "" && strings.TrimSpace(explicitEntrySymbol) == resolved.EntrySymbolPath {
		return &fusion.FlowResolution{
			SchemaVersion:   1,
			Status:          "resolved",
			RawRequest:      secret.Redact(requestText).Text,
			CandidateID:     resolved.CandidateID,
			EntrySymbolPath: resolved.EntrySymbolPath,
			FlowID:          fusion.ComputeFlowID(resolved.EntrySymbolPath),
			Evidence: []fusion.FlowEvidence{{
				CandidateID:     resolved.CandidateID,
				EntrySymbolPath: resolved.EntrySymbolPath,
				Description:     "분석 요청에 명시된 진입 심볼",
				SnapshotID:      snapshot.SnapshotID,
				ComputedBasisID: snapshot.ComputedBasisID,
			}},
		}, nil
	}
	return nil, errors.New("flow target is not a candidate in the current snapshot")
}

func (s *Server) serveTaskView(w http.ResponseWriter, r *http.Request) {
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
	focusSymbol := r.URL.Query().Get("focusSymbol")
	focusReason := r.URL.Query().Get("focusReason")
	requestID := strings.TrimSpace(r.URL.Query().Get("requestId"))

	if r.Method == http.MethodPost {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err == nil {
			if rid, ok := body["requestId"].(string); ok && strings.TrimSpace(rid) != "" {
				requestID = strings.TrimSpace(rid)
			}
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
				if symbol, ok := f["focusSymbol"].(string); ok && symbol != "" {
					focusSymbol = symbol
				}
				if reason, ok := f["focusReason"].(string); ok && reason != "" {
					focusReason = reason
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
			FocusSymbol: focusSymbol,
			FocusReason: focusReason,
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
	snapshot, _, releaseSnapshot, err := s.captureAnalysisSnapshot(ctx)
	if err != nil {
		writeTaskViewError(w, err, http.StatusInternalServerError)
		return
	}
	defer releaseSnapshot()
	if expected := strings.TrimSpace(r.URL.Query().Get("expectedSnapshotId")); expected != "" && expected != snapshot.SnapshotID {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": "source_changed", "message": "미리 본 코드가 변경됐습니다. 현재 코드를 다시 확인한 뒤 선택해 주세요.",
			"candidateTargets": []string{entrySymbol},
		})
		return
	}

	if strings.TrimSpace(requestID) == "" {
		requestID = newTaskViewRequestID()
	}
	inputHash := taskViewInputHash(mode, reqQuery, flowID, entrySymbol, domain, focusSymbol, focusReason)
	if cached, err := s.lookupTaskViewRecord(requestID, inputHash, snapshot.ComputedBasisID); err != nil {
		writeTaskViewError(w, err, http.StatusConflict)
		return
	} else if cached != nil {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(cached)
		return
	}

	cwd, _ := filepath.Abs(".")
	pools := make(map[string]*protocol.Pool)
	defer func() {
		for _, pool := range pools {
			pool.Close()
		}
	}()
	var candidates []harvest.Candidate
	for _, language := range detect.RequestedFlowCandidateLanguages(entrySymbol, snapshot.Files) {
		adapterCfg, resolveErr := harvest.ResolveAdapterForRepo(s.repoRoot, cwd, language, "")
		if resolveErr != nil {
			writeTaskViewError(w, fmt.Errorf("unavailable: resolve %s adapter: %w", language, resolveErr), http.StatusInternalServerError)
			return
		}
		pool := protocol.NewPool(adapterCfg, 2)
		pools[language] = pool
		languageCandidates, harvestErr := harvest.NewRunnerWithPool(pool).RunWithSnapshot(ctx, s.repoRoot, snapshot)
		if harvestErr != nil {
			writeTaskViewError(w, fmt.Errorf("unavailable: harvest %s candidates: %w", language, harvestErr), http.StatusInternalServerError)
			return
		}
		candidates = append(candidates, languageCandidates...)
	}

	resolved, err := semantic.ResolveFeatureQueryTarget(query, candidates)
	if err != nil {
		var qErr *semantic.QueryError
		if errors.As(err, &qErr) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code":             qErr.Code,
				"error":            qErr.Code,
				"message":          qErr.Message,
				"candidateTargets": qErr.CandidateTargets,
			})
			return
		}
		writeTaskViewError(w, err, http.StatusBadRequest)
		return
	}
	flowResolution, err := resolveFlowRecord(reqQuery, entrySymbol, resolved, candidates, snapshot)
	if err != nil {
		writeTaskViewError(w, err, http.StatusBadRequest)
		return
	}

	pool := pools[detect.RequestedEntryLanguage(resolved.EntrySymbolPath, snapshot.Files)]
	if pool == nil {
		writeTaskViewError(w, errors.New("unavailable: selected source language was not analyzed"), http.StatusInternalServerError)
		return
	}
	slicer := slicing.NewRunner(pool)
	slicePayload, err := slicer.SliceWithSnapshot(ctx, s.repoRoot, resolved.CandidateID, resolved.EntrySymbolPath, nil, snapshot)
	if err != nil {
		if isEntryAnalysisFailure(err) {
			writeTaskAnalysisFailure(w, reqQuery, resolved, err)
			return
		}
		writeTaskViewError(w, fmt.Errorf("unavailable: slice error: %w", err), http.StatusInternalServerError)
		return
	}

	reqText := reqQuery
	if reqText == "" {
		if flowResolution != nil {
			reqText = flowResolution.RawRequest
		} else {
			reqText = resolved.Title
		}
	}

	intent, err := semantic.NormalizeTaskIntent(reqText, semantic.IntentOptions{Mode: mode})
	if err != nil {
		writeTaskViewError(w, err, http.StatusInternalServerError)
		return
	}
	snapshotInput, err := snapshot.AnalyzerInput()
	if err != nil {
		writeTaskViewError(w, fmt.Errorf("unavailable: validated snapshot input: %w", err), http.StatusInternalServerError)
		return
	}

	mapIR, proj, err := semantic.CompileDeterministicFeatureMap(resolved, intent, slicePayload, semantic.CompileOptions{
		ComputedBasisID: snapshot.ComputedBasisID, WorkspaceEpoch: snapshot.WorkspaceEpoch,
		ValidatedAgainstSnapshotID: snapshot.SnapshotID, SnapshotID: snapshot.SnapshotID, SnapshotTreeID: snapshot.RootTreeID,
		RepositoryID: snapshot.RepositoryID, WorktreeID: snapshot.WorktreeID, DependencyFingerprint: snapshot.DependencyFingerprint, ConfigurationFingerprint: snapshot.ConfigurationFingerprint,
		AdapterVersion: slicePayload.AdapterVersion, AnalyzerRevision: slicePayload.AnalyzerVersion,
		AnalysisReadSetID: semantic.MetadataString(slicePayload.AnalysisReadSet, "readSetId"), CausalObservationClosureID: semantic.MetadataString(slicePayload.CausalObservationClosure, "closureId"),
		SnapshotFiles: snapshot.Files, SnapshotInput: &snapshotInput,
	})
	if err != nil {
		writeTaskViewError(w, fmt.Errorf("unavailable: compile map: %w", err), http.StatusInternalServerError)
		return
	}
	requestedFocus, err := semantic.ResolveRequestedFlowFocus(mapIR, focusSymbol, focusReason)
	if err != nil {
		writeTaskViewError(w, err, http.StatusBadRequest)
		return
	}
	mapBytes, err := json.Marshal(mapIR)
	if err != nil {
		writeTaskViewError(w, err, http.StatusInternalServerError)
		return
	}
	if err := contractharness.ValidateSemanticMapIR(mapBytes); err != nil {
		writeTaskViewError(w, err, http.StatusInternalServerError)
		return
	}
	projectionBytes, err := json.Marshal(proj)
	if err != nil {
		writeTaskViewError(w, err, http.StatusInternalServerError)
		return
	}
	if err := contractharness.ValidateFlowViewProjection(projectionBytes); err != nil {
		writeTaskViewError(w, err, http.StatusInternalServerError)
		return
	}
	if flowResolution != nil {
		if len(mapIR.Steps) == 0 {
			writeTaskAnalysisFailure(w, reqQuery, resolved, errors.New("requested flow has no entry step"))
			return
		}
		if err := fusion.AttachFlowResolutionSourceAnchor(flowResolution, mapIR.Steps[0].Anchor, snapshot.Files); err != nil {
			writeTaskAnalysisFailure(w, reqQuery, resolved, err)
			return
		}
	}

	evidenceRecords, _ := semantic.ExtractAndRedactEvidenceFromProtocolSnapshot(resolved, slicePayload, snapshot)

	adapterHasFlowContext := false
	if conn, connErr := pool.Get(ctx); connErr == nil {
		adapterHasFlowContext = conn.Version().Capabilities.FlowContext
		pool.Put(conn)
	}
	metadata := flowContextGenerationFor(mapIR, slicePayload, adapterHasFlowContext)
	flowContexts := make(map[string]*FlowContextProjection, len(mapIR.Steps))
	sourceBytes := snapshotSourceBytes(snapshot)
	for _, step := range mapIR.Steps {
		flowContexts[step.StepID] = DeriveFlowContext(DeriveFlowContextParams{
			Step:                  step,
			SemanticMap:           mapIR,
			SnapshotFiles:         sourceBytes,
			SourceSnapshotID:      snapshot.SnapshotID,
			Metadata:              metadata.Steps[step.StepID],
			AdapterHasFlowContext: adapterHasFlowContext,
			Expansion:             ExpansionFlowContext,
		})
	}
	if requestedFocus != nil {
		for _, stepID := range []string{requestedFocus.CallStepID, requestedFocus.EntryStepID} {
			var focusedStep *semantic.SemanticStep
			for index := range mapIR.Steps {
				if mapIR.Steps[index].StepID == stepID {
					focusedStep = &mapIR.Steps[index]
					break
				}
			}
			if focusedStep == nil {
				writeTaskAnalysisFailure(w, reqQuery, resolved, fmt.Errorf("requested focus step %q is missing", stepID))
				return
			}
			if err := validateRepresentativeStep(*focusedStep, nil, flowContexts, snapshot, mapIR); err != nil {
				writeTaskAnalysisFailure(w, reqQuery, resolved, fmt.Errorf("requested focus step %q: %w", stepID, err))
				return
			}
		}
	}
	flowSequence := semantic.BuildFlowSequence(mapIR)
	if err := validateRepresentativeCode(resolved, mapIR, flowSequence, flowContexts, snapshot); err != nil {
		writeTaskAnalysisFailure(w, reqQuery, resolved, err)
		return
	}
	markDetailSourceBoundaries(mapIR, flowSequence, flowContexts, snapshot)

	w.Header().Set("Content-Type", "application/json")
	payload, err := json.Marshal(map[string]any{
		"requestId":   requestID,
		"workspaceId": s.approvalWorkspaceID,
		"candidateAnswer": map[string]string{
			"requested":  mapIR.Summary.Requested,
			"candidate":  mapIR.Summary.Current,
			"authority":  mapIR.Authority,
			"freshness":  mapIR.Freshness,
			"settlement": mapIR.Settlement,
		},
		"taskIntent":          intent,
		"agentReportedStatus": nil,
		"semanticMap":         mapIR,
		"projection":          proj,
		"evidence":            evidenceRecords,
		"flowContexts":        flowContexts,
		"unknowns":            mapIR.Unknowns,
		"baseline":            map[string]any{"status": "none", "reason": "no comparison baseline selected; current flow only"},
		"publicationGate":     map[string]any{"eligibility": "not_evaluated", "reason": "current proof required"},
		"proofManifest":       nil,
		"verifiedGap":         nil,
	})
	if err != nil {
		writeTaskViewError(w, err, http.StatusInternalServerError)
		return
	}
	var result map[string]any
	if err := json.Unmarshal(payload, &result); err != nil {
		writeTaskViewError(w, err, http.StatusInternalServerError)
		return
	}
	result["sourceFiles"] = BuildSourceFiles(mapIR, snapshot)
	result["flowSequence"] = flowSequence
	redactedFocusReason := ""
	if requestedFocus != nil {
		redactedFocusReason = requestedFocus.Reason
	}
	result["request"] = &semantic.FeatureQueryParams{Request: reqText, EntrySymbol: resolved.EntrySymbolPath, FlowID: resolved.FlowID, Domain: domain, FocusSymbol: focusSymbol, FocusReason: redactedFocusReason}
	if requestedFocus != nil {
		result["requestedFocus"] = requestedFocus
	}
	if flowResolution != nil {
		result["flowResolution"] = flowResolution
	} else {
		result["flowResolutionMissing"] = true
		result["resolutionLimitation"] = "과거 분석으로 요청 확정 기록이 존재하지 않습니다."
	}
	saved, err := s.SaveTaskView(ctx, result)
	if err != nil {
		writeTaskViewError(w, err, http.StatusInternalServerError)
		return
	}
	payload, err = json.Marshal(saved)
	if err != nil {
		writeTaskViewError(w, err, http.StatusInternalServerError)
		return
	}
	s.mu.Lock()
	if s.mapCache == nil {
		s.mapCache = make(map[string]*semantic.SemanticMapIR)
	}
	if s.flowContextMetadata == nil {
		s.flowContextMetadata = make(map[string]flowContextGeneration)
	}
	s.mapCache[mapIR.GenerationID] = mapIR
	s.mapCache[mapIR.ComputedBasisID] = mapIR
	s.flowContextMetadata[mapIR.GenerationID] = metadata
	s.mu.Unlock()
	s.storeTaskViewRecord(requestID, inputHash, snapshot.ComputedBasisID, mapIR.GenerationID, payload)
	_, _ = w.Write(payload)
}

func isEntryAnalysisFailure(err error) bool {
	if err == nil {
		return false
	}
	message := err.Error()
	return strings.Contains(message, "entry_symbol_not_found:") ||
		strings.Contains(message, "entry_symbol_has_no_executable_steps:") ||
		strings.Contains(message, "entry_symbol_unsupported_control_flow:") ||
		strings.Contains(message, "representative_code_invalid:")
}

func validateRepresentativeCode(resolved *semantic.ResolvedTarget, mapIR *semantic.SemanticMapIR, sequence *semantic.FlowSequence, flowContexts map[string]*FlowContextProjection, snapshot protocol.Snapshot) error {
	if mapIR == nil || len(mapIR.Steps) == 0 {
		return fmt.Errorf("representative_code_invalid: no executable steps in feature map")
	}
	if sequence == nil || len(sequence.Frames) == 0 {
		return fmt.Errorf("representative_code_invalid: no FlowSequence gateways")
	}
	stepsByID := make(map[string]semantic.SemanticStep, len(mapIR.Steps))
	for _, step := range mapIR.Steps {
		stepsByID[step.StepID] = step
	}
	checked := make(map[string]bool, len(sequence.Frames)+1)
	entry := mapIR.Steps[0]
	if err := validateRepresentativeStep(entry, resolved, flowContexts, snapshot, mapIR); err != nil {
		return err
	}
	checked[entry.StepID] = true
	for _, frame := range sequence.Frames {
		step, ok := stepsByID[frame.PrimaryStepRef]
		if !ok {
			return fmt.Errorf("representative_code_invalid: gateway %q references missing step %q", frame.FrameID, frame.PrimaryStepRef)
		}
		if checked[step.StepID] {
			continue
		}
		if err := validateRepresentativeStep(step, nil, flowContexts, snapshot, mapIR); err != nil {
			return fmt.Errorf("gateway %q representative %q: %w", frame.FrameID, step.StepID, err)
		}
		checked[step.StepID] = true
	}
	return nil
}

func validateRepresentativeStep(repStep semantic.SemanticStep, resolved *semantic.ResolvedTarget, flowContexts map[string]*FlowContextProjection, snapshot protocol.Snapshot, mapIR *semantic.SemanticMapIR) error {
	if repStep.Anchor.RepoRelativePath == "" {
		return fmt.Errorf("representative_code_invalid: missing source file anchor")
	}
	fileBytes, ok := snapshot.Files[repStep.Anchor.RepoRelativePath]
	if !ok {
		return fmt.Errorf("representative_code_invalid: representative source file %q not in snapshot", repStep.Anchor.RepoRelativePath)
	}

	br := repStep.Anchor.ByteRange
	if br[0] <= 0 && br[1] >= len(fileBytes) && (len(fileBytes) > 50 || strings.Contains(string(fileBytes), "\n")) {
		return fmt.Errorf("representative_code_invalid: whole-file fallback range is not a valid representative statement")
	}
	if br[0] < 0 || br[0] >= br[1] || br[1] > len(fileBytes) ||
		repStep.Anchor.FileHash != sha256Hex([]byte(fileBytes)) ||
		repStep.Anchor.SpanHash != sha256Hex([]byte(fileBytes[br[0]:br[1]])) {
		return fmt.Errorf("representative_code_invalid: source range or hash does not match the snapshot")
	}
	if mapIR.ComputedBasisID != snapshot.ComputedBasisID || flowSourceSnapshotID(mapIR) != snapshot.SnapshotID {
		return fmt.Errorf("representative_code_invalid: analysis basis or snapshot mismatch")
	}

	if resolved != nil && resolved.EntrySymbolPath != "" {
		parts := strings.Split(resolved.EntrySymbolPath, "#")
		expectedFile := parts[0]
		expectedSymbol := ""
		if len(parts) > 1 {
			expectedSymbol = parts[1]
		}
		if repStep.Anchor.RepoRelativePath != expectedFile {
			return fmt.Errorf("representative_code_invalid: source file mismatch (expected %s, got %s)", expectedFile, repStep.Anchor.RepoRelativePath)
		}
		if expectedSymbol != "" && repStep.Anchor.EnclosingSymbolPath != "" {
			if !isSymbolMatch(repStep.Anchor.EnclosingSymbolPath, expectedSymbol) {
				return fmt.Errorf("representative_code_invalid: symbol mismatch (expected %s, got %s)", expectedSymbol, repStep.Anchor.EnclosingSymbolPath)
			}
		}
	}

	fc := flowContexts[repStep.StepID]
	if fc == nil || fc.StepID != repStep.StepID || fc.SnapshotID != snapshot.SnapshotID ||
		fc.CanonicalPath != repStep.Anchor.RepoRelativePath || fc.Precision != PrecisionExact ||
		fc.SourceValidationStatus != "verified" || fc.Statement == nil || fc.Statement.ByteRange != br || fc.SourceLimitation != "" {
		return fmt.Errorf("representative_code_invalid: exact CodeView source context is unavailable")
	}
	for _, line := range fc.DisplayedLines {
		if line.IsHit && line.Selection != nil && line.Selection.Text != "" &&
			line.Selection.Before+line.Selection.Text+line.Selection.After == line.Text {
			return nil
		}
	}
	return fmt.Errorf("representative_code_invalid: selected source is not highlighted in CodeView")
}

func isSymbolMatch(actual, expected string) bool {
	if actual == expected {
		return true
	}
	if strings.HasSuffix(expected, "."+actual) || strings.HasSuffix(actual, "."+expected) {
		return true
	}
	if strings.HasSuffix(expected, "#"+actual) || strings.HasSuffix(actual, "#"+expected) {
		return true
	}
	return false
}

func (s *Server) captureAnalysisSnapshot(ctx context.Context) (protocol.Snapshot, *workspace.WorkspaceSnapshot, func(), error) {
	head, err := s.engine.ReconcileIfChanged(ctx, nil)
	if err != nil {
		return protocol.Snapshot{}, nil, nil, fmt.Errorf("capture workspace snapshot: %w", err)
	}
	lease, err := s.engine.SnapshotVFS(head.SnapshotID)
	if err != nil {
		return protocol.Snapshot{}, nil, nil, fmt.Errorf("retain workspace snapshot: %w", err)
	}
	snapshot, err := protocol.SnapshotFromLease(lease)
	if err != nil {
		_ = lease.Close()
		return protocol.Snapshot{}, nil, nil, fmt.Errorf("convert workspace snapshot: %w", err)
	}
	return snapshot, head, func() { _ = lease.Close() }, nil
}

func snapshotSourceFiles(snapshot protocol.Snapshot) map[string]string {
	files := snapshot.Files
	if len(files) == 0 {
		files = snapshot.ContentOverlay
	}
	out := make(map[string]string, len(files))
	for path, content := range files {
		out[path] = content
	}
	return out
}

func snapshotSourceBytes(snapshot protocol.Snapshot) map[string][]byte {
	files := snapshotSourceFiles(snapshot)
	out := make(map[string][]byte, len(files))
	for path, content := range files {
		out[path] = []byte(content)
	}
	return out
}

func defaultLiveIdentity(root string) workspace.WorkspaceIdentity {
	abs, _ := filepath.Abs(root)
	digest := sha256.Sum256([]byte(abs))
	hexDigest := hex.EncodeToString(digest[:])
	return workspace.WorkspaceIdentity{
		RepositoryID:             "repo-" + hexDigest[:24],
		WorktreeID:               "worktree-" + hexDigest[24:],
		ConfigurationFingerprint: "config-" + hexDigest[:32],
	}
}
