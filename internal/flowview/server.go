// Package flowview embeds and serves the FlowView interactive user interface
// (design §4.3, tickets 16, 17, 18).
package flowview

import (
	"context"
	"crypto/rand"
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
	"sync/atomic"
	"time"

	"codeflow/internal/contractharness"
	"codeflow/internal/detect"
	"codeflow/internal/fusion"
	"codeflow/internal/harvest"
	"codeflow/internal/protocol"
	"codeflow/internal/rflscvs06"
	"codeflow/internal/semantic"
	"codeflow/internal/slicing"
	"codeflow/internal/storage"
	"codeflow/internal/workspace"
)

// Server coordinates the loopback HTTP server for FlowView.
type Server struct {
	repoRoot     string
	approvalGate *semantic.ApprovalAccessGate
	// approvalWorkspaceID is derived from the Core-configured repository root
	// and is never accepted from an HTTP request.
	approvalWorkspaceID string
	proposalStore       semantic.ProposalStore
	approvalService     *semantic.ApprovalExecutionService
	storage             *storage.Storage
	eventLog            *fusion.EventLog
	engine              *workspace.SnapshotEngine
	authToken           string
	listener            net.Listener
	httpServer          *http.Server
	mu                  sync.Mutex
	addr                string
	genCache            *generationCache
	hub                 *EventHub
	gate                *semantic.PublicationGate
	scheduler           *semantic.CoalescingScheduler
	mapCache            map[string]*semantic.SemanticMapIR
	modelHostFactory    protocol.ModelHostFactory
	modelHostMu         sync.Mutex
	// modelHostSpawnMu serializes the complete request-scoped factory
	// invocation. modelHostClosing is independent so Shutdown can close
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
	// Failure investigation dependencies are injected at the FlowView seam.
	// They are intentionally interface-valued so a request can never supply
	// runtime authority or a repository-backed observation directly.
	runtimeObservationProvider any
	runtimeObservationStore    any
	observationProvider        any
	observationStore           any
	runtimeExecutor            any
	oneShotExecutor            any
	runtimeExecutionSpec       rflscvs06.RuntimeExecutionSpec
	runtimeConsent             *rflscvs06.RuntimeConsent
	releaseThresholdDecisions  semantic.ThresholdDecisionResolver
	live                       liveState
	liveCtx                    context.Context
	liveCancel                 context.CancelFunc
	analysisMu                 sync.Mutex
	analysisCancel             context.CancelFunc
	compileCandidate           candidateCompiler
	pipelineErrMu              sync.Mutex
	lastPipelineErr            error
	liveWG                     sync.WaitGroup
	liveMu                     sync.Mutex
	liveDone                   chan struct{}
	startOnce                  sync.Once
	shutdownMu                 sync.Mutex
	shutdownStarted            bool
	shutdownInitDone           chan struct{}
	shutdownResultDone         chan struct{}
	shutdownInitErr            error
	shutdownFirstErr           error
}

func (s *Server) recordPipelineError(err error) {
	if err == nil {
		return
	}
	s.pipelineErrMu.Lock()
	s.lastPipelineErr = err
	s.pipelineErrMu.Unlock()
}

func (s *Server) lastPipelineError() error {
	s.pipelineErrMu.Lock()
	defer s.pipelineErrMu.Unlock()
	return s.lastPipelineErr
}

// LastPipelineError reports the most recent terminal live-analysis error. It
// is diagnostic state only and never grants current authority.
func (s *Server) LastPipelineError() error {
	return s.lastPipelineError()
}

// captureAnalysisSnapshot establishes one VS-01 lease for the complete
// FlowView request. The lease remains live until adapters, publication, and
// evidence extraction have all consumed the captured bytes.
func (s *Server) captureAnalysisSnapshot(ctx context.Context) (protocol.Snapshot, *workspace.WorkspaceSnapshot, func(), error) {
	// Reconcile at the request boundary so a persisted head from an earlier
	// process cannot silently become the analysis basis for the current tree.
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

// Config configures the FlowView server.
type Config struct {
	RepoRoot  string
	Port      int
	AuthToken string
	// ReleaseThresholdDecisions is trusted server-side configuration. HTTP
	// request bodies cannot supply or replace it.
	ReleaseThresholdDecisions semantic.ThresholdDecisionResolver
	// ProposalStore lets local product surfaces share the same durable
	// proposal/evidence boundary with enrichment and approval execution.
	ProposalStore semantic.ProposalStore

	// Runtime observations are resolved by ID through a trusted server-side
	// provider or store. The HTTP request never carries an observation object.
	RuntimeObservationProvider any
	RuntimeObservationStore    any
	ObservationProvider        any // compatibility alias for integrations
	ObservationStore           any // compatibility alias for integrations

	// RuntimeExecutor is an optional one-shot trusted_local execution seam.
	// RuntimeExecutionSpec describes the command and isolation scope that a
	// RuntimeConsent must authorize. Neither is constructed from the request.
	RuntimeExecutor      any
	OneShotExecutor      any // compatibility alias for integrations
	RuntimeExecutionSpec rflscvs06.RuntimeExecutionSpec
	RuntimeConsent       *rflscvs06.RuntimeConsent
	// ModelHostFactory creates one Core-supervised host per enrichment request.
	// The returned host is owned and closed by that request.
	ModelHostFactory protocol.ModelHostFactory
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
			// A state created by an earlier version may have snapshots but no
			// persisted identity. It has lineage and therefore needs the normal
			// epoch transition before the new identity can become authoritative.
			if _, transitionErr := engine.SetWorkspaceIdentity(identity); transitionErr != nil {
				return nil, fmt.Errorf("bind workspace identity: %w", err)
			}
		}
	} else if currentIdentity != identity {
		if _, err := engine.SetWorkspaceIdentity(identity); err != nil {
			return nil, fmt.Errorf("set workspace identity: %w", err)
		}
	}

	hub, err := NewDurableEventHub("flowview-live-stream", 100, filepath.Join(st.BaseDir(), "semantics", "events", "event-ledger.jsonl"))
	if err != nil {
		return nil, fmt.Errorf("init live event ledger: %w", err)
	}
	gate := semantic.NewPublicationGate()
	scheduler := semantic.NewCoalescingScheduler(semantic.DefaultCoalescingConfig())
	authenticator := semantic.NewLocalProcessApprovalAuthenticator()
	authorizer := semantic.NewApprovalWorkspaceAuthorizer(cfg.RepoRoot)
	proposalStore := cfg.ProposalStore
	if proposalStore == nil {
		proposalStore = semantic.NewDurableProposalStore(cfg.RepoRoot)
	}
	approvalService, err := semantic.NewApprovalExecutionService(cfg.RepoRoot, engine, proposalStore)
	if err != nil {
		return nil, fmt.Errorf("init approval service: %w", err)
	}

	s := &Server{
		repoRoot:                   cfg.RepoRoot,
		approvalGate:               semantic.NewApprovalAccessGate(authenticator, authorizer),
		approvalWorkspaceID:        authorizer.WorkspaceID(),
		proposalStore:              proposalStore,
		approvalService:            approvalService,
		storage:                    st,
		eventLog:                   fusion.NewEventLog(cfg.RepoRoot),
		engine:                     engine,
		authToken:                  token,
		hub:                        hub,
		gate:                       gate,
		scheduler:                  scheduler,
		mapCache:                   make(map[string]*semantic.SemanticMapIR),
		modelHostFactory:           cfg.ModelHostFactory,
		modelHostActive:            make(map[uint64]context.CancelFunc),
		modelHostDrainDone:         closedLifecycleChannel(),
		modelHostSpawnDone:         closedLifecycleChannel(),
		liveDone:                   closedLifecycleChannel(),
		runtimeObservationProvider: cfg.RuntimeObservationProvider,
		runtimeObservationStore:    cfg.RuntimeObservationStore,
		observationProvider:        cfg.ObservationProvider,
		observationStore:           cfg.ObservationStore,
		runtimeExecutor:            cfg.RuntimeExecutor,
		oneShotExecutor:            cfg.OneShotExecutor,
		runtimeExecutionSpec:       cfg.RuntimeExecutionSpec,
		runtimeConsent:             cloneRuntimeConsent(cfg.RuntimeConsent),
		releaseThresholdDecisions:  cfg.ReleaseThresholdDecisions,
	}
	if err := recoverPendingApprovalOutboxDeliveries(context.Background(), s.approvalService, s.hub); err != nil {
		return nil, fmt.Errorf("recover approval outbox: %w", err)
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
	mux.HandleFunc("/api/task/impact", s.handleTaskImpact)
	mux.HandleFunc("/api/task/debug", s.handleTaskDebug)
	mux.HandleFunc("/api/task/incident", s.handleTaskIncident)
	mux.HandleFunc("/api/semantic/approve", s.handleSemanticApprove)
	mux.HandleFunc("/api/semantic/approval-history", s.handleSemanticApprovalHistory)
	mux.HandleFunc("/api/semantic/evidence-pack", s.handleEvidencePack)
	mux.HandleFunc("/api/semantic/enrich", s.handleSemanticEnrichment)
	mux.HandleFunc("/api/task/onboarding", s.handleTaskOnboarding)
	mux.HandleFunc("/api/release/capability", s.handleReleaseCapability)
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
		Handler:     s.securityMiddleware(mux),
		ReadTimeout: 15 * time.Second,
		// The workspace stream is intentionally long-lived. Per-response write
		// deadlines would terminate a healthy idle SSE connection before the
		// client can reconnect with Last-Event-ID.
		WriteTimeout: 0,
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

// SnapshotEngine exposes the single workspace lineage used by the FlowView
// live coordinator. MCP and other local product surfaces use this accessor so
// edits cannot create a second engine for the same repository.
func (s *Server) SnapshotEngine() *workspace.SnapshotEngine {
	if s == nil {
		return nil
	}
	return s.engine
}

// CurrentActivity returns the live coordinator's durable activity view.
func (s *Server) CurrentActivity() workspace.ActivityStatus {
	if s == nil || s.engine == nil {
		return workspace.ActivityStatus{SchemaID: "https://codeflow.local/schemas/rflsc.activity-state.v2.schema.json", SchemaVersion: 2, Activity: "idle", AnalysisLagMs: -1, PendingRevisions: 0, Timestamp: time.Now().UTC()}
	}
	return s.engine.CurrentActivity()
}

// RememberTaskQuery binds the latest normalized feature query to the live
// checkpoint consumer. It stores only a defensive copy and does not publish
// or grant authority by itself.
func (s *Server) RememberTaskQuery(query *semantic.TaskViewQuery, requestText string) error {
	if s == nil || query == nil || query.Feature == nil {
		return fmt.Errorf("feature query is required")
	}
	s.rememberLiveRequest(query, requestText)
	return nil
}

// SubmitVersionedEdit is the shared edit ingress for HTTP and MCP. The edit
// snapshot, scheduler notification, durable activity event, and UX
// acknowledgement all use this server's one live coordinator.
func (s *Server) SubmitVersionedEdit(ctx context.Context, edit workspace.EditRequest) (*workspace.DocumentRevision, *workspace.WorkspaceSnapshot, error) {
	if s == nil || s.engine == nil || s.scheduler == nil || s.hub == nil {
		return nil, nil, fmt.Errorf("live coordinator is unavailable")
	}
	rev, snap, err := s.engine.ApplyVersionedEdit(ctx, edit)
	if err != nil {
		return nil, nil, err
	}
	// A newly accepted edit supersedes any checkpoint still compiling. The
	// cancellation happens before notification so an extremely short timer
	// cannot start the replacement analysis and then be canceled by this edit.
	s.cancelActiveAnalysis()
	// The scheduler retains this snapshot and the consumer processes it after
	// the canceled analysis returns.
	s.scheduler.NotifyEdit(snap)
	act := s.engine.CurrentActivity()
	if _, err := s.hub.PublishChecked("activity.updated", act, &snap.ComputedBasisID, &snap.SnapshotID, nil); err != nil {
		return rev, snap, fmt.Errorf("persist activity event: %w", err)
	}
	// Acknowledgement is recorded only after the edit activity has a durable
	// event. Publish the post-ack state as a second bounded event so observers
	// can measure the transition without treating an in-memory timestamp as
	// evidence.
	s.engine.AcknowledgeEdit(snap.SnapshotID)
	if _, err := s.hub.PublishChecked("activity.updated", s.engine.CurrentActivity(), &snap.ComputedBasisID, &snap.SnapshotID, nil); err != nil {
		return rev, snap, fmt.Errorf("persist activity acknowledgement: %w", err)
	}
	return rev, snap, nil
}

// Start runs the HTTP server in the background.
func (s *Server) Start() {
	s.startOnce.Do(func() {
		s.startLiveConsumer()
		go func() { _ = s.httpServer.Serve(s.listener) }()
	})
}

// Shutdown gracefully shuts down the HTTP server.
func (s *Server) Shutdown(ctx context.Context) error {
	if s == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	s.shutdownMu.Lock()
	if !s.shutdownStarted {
		s.shutdownStarted = true
		s.shutdownInitDone = make(chan struct{})
		s.shutdownResultDone = make(chan struct{})
		initDone := s.shutdownInitDone
		s.shutdownMu.Unlock()

		var initErrors []error
		if stopErr := s.stopModelHostRequests(ctx); stopErr != nil {
			initErrors = append(initErrors, stopErr)
		}
		s.liveMu.Lock()
		liveCancel := s.liveCancel
		s.liveMu.Unlock()
		if liveCancel != nil {
			liveCancel()
		}
		if s.scheduler != nil {
			s.scheduler.Close()
		}
		if s.httpServer != nil {
			if shutdownErr := s.httpServer.Shutdown(ctx); shutdownErr != nil {
				initErrors = append(initErrors, shutdownErr)
			}
		}
		initErr := joinLifecycleErrors(initErrors...)
		s.shutdownMu.Lock()
		s.shutdownInitErr = initErr
		close(initDone)
		s.shutdownMu.Unlock()

		var waitErrors []error
		if waitErr := s.waitLiveConsumer(ctx); waitErr != nil {
			waitErrors = append(waitErrors, waitErr)
		}
		if waitErr := s.waitModelHostRequests(ctx); waitErr != nil {
			waitErrors = append(waitErrors, waitErr)
		}
		firstErr := joinLifecycleErrors(append([]error{initErr}, waitErrors...)...)
		s.shutdownMu.Lock()
		s.shutdownFirstErr = firstErr
		close(s.shutdownResultDone)
		s.shutdownMu.Unlock()
		return firstErr
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
	if waitErr := s.waitLiveConsumer(ctx); waitErr != nil {
		retryErrors = append(retryErrors, waitErr)
	}
	if waitErr := s.waitModelHostRequests(ctx); waitErr != nil {
		retryErrors = append(retryErrors, waitErr)
	}
	return joinLifecycleErrors(append([]error{firstErr}, retryErrors...)...)
}

// securityMiddleware enforces loopback Host validation, Origin/Referer CSRF check
// (design-v2 §11.3) and per-run token verification.
func (s *Server) securityMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Host check (R8 loopback). The hostname must be an exact local
		// authority, with only a numeric optional port accepted.
		if !isAllowedLoopbackHost(r.Host) {
			http.Error(w, "Forbidden: invalid host", http.StatusForbidden)
			return
		}

		// CSRF check: if Origin header is present, it must be an HTTP URL
		// whose authority is an exact loopback host. Also check Referer
		// similarly when Origin is absent.
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

		// Token check for all API endpoints (R8: loopback + per-run token on all APIs)
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

// generationDataForSnapshot reads persisted artifacts but takes all source
// configuration from the request's retained snapshot. It is intentionally
// uncached because the snapshot may differ from the current live head while a
// request is in flight.
func (s *Server) generationDataForSnapshot(snapshot protocol.Snapshot, overrides map[string]string) *generationCache {
	idx, err := s.storage.ReadLatestIndex()
	if err != nil || idx == nil {
		return nil
	}
	docs := make([][]byte, 0, len(idx.Flows))
	for _, f := range idx.Flows {
		d, err := s.storage.ReadActiveFlowSpec(f.FlowID)
		if err != nil {
			continue
		}
		docs = append(docs, d)
	}
	return &generationCache{
		genID:     idx.GenerationID,
		docs:      docs,
		coverage:  synthesizeCoverageDocs(s.storage.ReadAllSliceCaches(maxCoverageFiles)),
		decorated: decorateAll(docs, overrides),
	}
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
	snapshot, _, releaseSnapshot, err := s.captureAnalysisSnapshot(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer releaseSnapshot()

	idx, err := s.storage.ReadLatestIndex()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	amap := &ArchitectureMap{Lanes: []MapLane{}, Components: []MapComponent{}, EntryPoints: []string{}, Relations: []MapRelation{}}
	manifest, manifestErr := harvest.LoadManifestFromSnapshot(snapshotSourceFiles(snapshot))
	if manifestErr != nil {
		http.Error(w, manifestErr.Error(), http.StatusInternalServerError)
		return
	}
	if gd := s.generationDataForSnapshot(snapshot, manifest.LaneOverrideMap()); gd != nil && idx != nil {
		entryPoints := make([]string, 0, len(idx.Flows))
		for _, f := range idx.Flows {
			entryPoints = append(entryPoints, f.EntrySymbolPath)
		}
		allDocs := make([][]byte, 0, len(gd.docs)+len(gd.coverage))
		allDocs = append(allDocs, gd.docs...)
		allDocs = append(allDocs, gd.coverage...)
		amap = buildArchitectureMapFromSnapshot(snapshotSourceBytes(snapshot), gd.genID, allDocs, manifest.LaneOverrideMap(), entryPoints)
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

	// Source is served from the same retained VS-01 snapshot used for analysis.
	// Reading repoRoot here would allow a live edit to disagree with the map and
	// evidence produced for this request.
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
	var taskViewBody map[string]any

	if r.Method == http.MethodPost {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err == nil {
			taskViewBody = body
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
	if mode == "onboarding" {
		req, err := onboardingQueryValues(r.URL.Query())
		if err != nil {
			writeOnboardingHTTPError(w, err)
			return
		}
		if taskViewBody != nil {
			req, err = mergeOnboardingEnvelope(req, taskViewBody)
			if err != nil {
				writeOnboardingHTTPError(w, err)
				return
			}
		}
		result, err := s.ExploreOnboarding(r.Context(), req)
		if err != nil {
			writeOnboardingHTTPError(w, err)
			return
		}
		payload, err := redactOnboardingJSON(result)
		if err != nil {
			writeOnboardingHTTPError(w, fmt.Errorf("internal_error: %w", err))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(payload)
		return
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
	snapshot, _, releaseSnapshot, err := s.captureAnalysisSnapshot(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer releaseSnapshot()

	det := detect.DetectSnapshot(snapshot.Files)
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
	candidates, err := harvester.RunWithSnapshot(ctx, s.repoRoot, snapshot)
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
	slicePayload, err := slicer.SliceWithSnapshot(ctx, s.repoRoot, resolved.CandidateID, resolved.EntrySymbolPath, nil, snapshot)
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
	snapshotInput, err := snapshot.AnalyzerInput()
	if err != nil {
		http.Error(w, fmt.Sprintf("validated snapshot input: %v", err), http.StatusInternalServerError)
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
		http.Error(w, fmt.Sprintf("compile map: %v", err), http.StatusInternalServerError)
		return
	}
	mapBytes, err := json.Marshal(mapIR)
	if err != nil {
		http.Error(w, fmt.Sprintf("marshal map: %v", err), http.StatusInternalServerError)
		return
	}
	if err := contractharness.ValidateSemanticMapIR(mapBytes); err != nil {
		http.Error(w, fmt.Sprintf("semantic map contract: %v", err), http.StatusInternalServerError)
		return
	}
	projectionBytes, err := json.Marshal(proj)
	if err != nil {
		http.Error(w, fmt.Sprintf("marshal projection: %v", err), http.StatusInternalServerError)
		return
	}
	if err := contractharness.ValidateFlowViewProjection(projectionBytes); err != nil {
		http.Error(w, fmt.Sprintf("projection contract: %v", err), http.StatusInternalServerError)
		return
	}

	s.mu.Lock()
	if s.mapCache == nil {
		s.mapCache = make(map[string]*semantic.SemanticMapIR)
	}
	s.mapCache[mapIR.GenerationID] = mapIR
	s.mapCache[mapIR.ComputedBasisID] = mapIR
	s.mu.Unlock()
	s.rememberLiveRequest(query, reqText)

	evidenceRecords, _ := semantic.ExtractAndRedactEvidenceFromProtocolSnapshot(resolved, slicePayload, snapshot)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
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
		"unknowns":            mapIR.Unknowns,
		"publicationGate":     map[string]any{"eligibility": "not_evaluated", "reason": "VS03 current proof required"},
		"proofManifest":       nil,
		"verifiedGap":         nil,
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
	rev, snap, err := s.SubmitVersionedEdit(r.Context(), workspace.EditRequest{
		Path:            req.Path,
		Content:         []byte(req.Content),
		DocumentVersion: req.DocumentVersion,
		Source:          req.Source,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

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
		activeManifest, activePointer, proofErr := s.storage.ReadValidatedActiveProofManifest()
		curAct := s.engine.CurrentActivity()
		if proofErr != nil {
			// An invalid pointer/manifest pair is diagnostic state, never current
			// authority exposed to a reconnecting client.
			activeManifest = nil
			activePointer = nil
		}
		syncData := map[string]any{"activeManifest": activeManifest, "activePointer": activePointer, "activity": curAct}
		if terminal := s.hub.LatestTerminalEvent(); terminal != nil && terminal.EventType == "generation.gap" {
			// The terminal payload came from the durable, schema-validated event
			// ledger. Include it verbatim so restart/full-sync preserves measured
			// scope, causes, lag, pending count, snapshot, and trace identity.
			syncData["verifiedGap"] = terminal.Data
		}
		if proofErr != nil {
			syncData["proofError"] = proofErr.Error()
		}
		syncEnv := s.hub.SnapshotSync(syncData)
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
	heartbeat := time.NewTicker(10 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-heartbeat.C:
			if _, err := io.WriteString(w, ": heartbeat\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case env, ok := <-ch:
			if !ok {
				return
			}
			if formatted, err := FormatSSE(env); err == nil {
				if _, err := w.Write(formatted); err != nil {
					return
				}
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
	manifest, ptr, err := s.storage.ReadValidatedActiveProofManifest()
	if err != nil {
		http.Error(w, fmt.Sprintf("read validated current proof: %v", err), http.StatusInternalServerError)
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

// handleTaskDebug serves the explicit rflsc.failure-query.v2 debug seam.
func (s *Server) handleTaskDebug(w http.ResponseWriter, r *http.Request) {
	s.handleFailureV2(w, r, "debug")
}

// handleTaskIncident serves the explicit rflsc.failure-query.v2 incident seam.
func (s *Server) handleTaskIncident(w http.ResponseWriter, r *http.Request) {
	s.handleFailureV2(w, r, "incident")
}

// handleEvidencePack implements GET /api/semantic/evidence-pack (Raw §9.7, VS-08).
func (s *Server) handleEvidencePack(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	sym := r.URL.Query().Get("symbolPath")
	if sym == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code":    "missing_precondition",
			"message": "symbolPath parameter is required",
		})
		return
	}

	var pack *semantic.EvidencePack
	if mapIR := s.cachedSemanticMap("", ""); mapIR != nil {
		var targetIDs []string
		for _, step := range mapIR.Steps {
			if step.Name == sym || step.TechnicalName == sym || step.StructuralIdentity == sym {
				targetIDs = append(targetIDs, step.StepID)
			}
		}
		if len(targetIDs) > 0 {
			snapshot, _, releaseSnapshot, snapshotErr := s.captureAnalysisSnapshot(r.Context())
			if snapshotErr == nil {
				publication := s.currentSemanticPublication()
				var currentProof *storage.GenerationProofManifest
				var currentProofBytes, semanticMapBytes []byte
				var currentPointer *storage.ActivePointer
				if publication != nil {
					currentProof, currentProofBytes, currentPointer, semanticMapBytes = publication.Manifest, publication.ManifestBytes, publication.Pointer, publication.SemanticMap
				}
				pack, _ = semantic.BuildEvidencePackV2(semantic.EvidencePackRequest{Map: mapIR, Snapshot: snapshot, CurrentProof: currentProof, CurrentProofBytes: currentProofBytes, CurrentPointer: currentPointer, SemanticMapBytes: semanticMapBytes, LiveHeadSnapshotID: snapshot.SnapshotID, TargetStepIDs: targetIDs, TargetSymbolPath: sym})
				releaseSnapshot()
			}
		}
	}
	if pack == nil {
		// Keep the legacy response shape for callers that have not published a
		// deterministic map, but mark the item unverified instead of inventing
		// source representation or a current authority claim.
		pack, _ = semantic.BuildEvidencePack(sym, "active", "active", []semantic.EvidenceItem{{
			EvidenceID: "ev-unavailable-" + sym,
			Kind:       "ast_anchor",
			Source:     sym,
			Content:    "current verified evidence unavailable",
			Verified:   false,
		}})
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(pack)
}

// handleSemanticApprove implements POST /api/semantic/approve (Raw §9.4..§9.6, VS-08).
func (s *Server) handleSemanticApprove(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if r.Body == nil {
		semanticJSONError(w, http.StatusBadRequest, "missing_precondition", "failed to decode approval request body")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, (1<<20)+1))
	if err != nil || len(body) > 1<<20 {
		semanticJSONError(w, http.StatusBadRequest, "missing_precondition", "failed to decode approval request body")
		return
	}
	draft, err := semantic.ParseApprovalCommandDraftJSON(body)
	if err != nil {
		semanticJSONError(w, http.StatusBadRequest, "missing_precondition", "failed to decode approval request body")
		return
	}
	target, err := filepath.Abs(s.repoRoot)
	if err != nil {
		writeSemanticApprovalAccessError(w, &semantic.ApprovalUnauthorizedError{Reason: "approval workspace is unavailable"})
		return
	}
	access, err := s.authorizeSemanticApproval(r.Context(), target)
	if err != nil {
		writeSemanticApprovalAccessError(w, err)
		return
	}
	result, err := s.SubmitSemanticApproval(r.Context(), access, draft)
	if err != nil {
		writeSemanticApprovalExecutionError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}

func (s *Server) authorizeSemanticApproval(ctx context.Context, target string) (semantic.ApprovalAccess, error) {
	if s == nil || s.approvalGate == nil {
		return semantic.ApprovalAccess{}, &semantic.ApprovalUnauthenticatedError{Reason: "approval authenticator is unavailable"}
	}
	access, err := s.approvalGate.AuthenticateAndAuthorize(ctx, s.approvalWorkspaceID, target)
	if err != nil {
		return semantic.ApprovalAccess{}, err
	}
	return access, nil
}

func writeSemanticApprovalExecutionError(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	code := "approval_invalid"
	message := "approval command could not be executed"
	switch {
	case errors.Is(err, semantic.ErrApprovalExecutionConflict):
		status = http.StatusConflict
		code = "approval_conflict"
	case errors.Is(err, semantic.ErrApprovalExecutionUnavailable):
		status = http.StatusNotFound
		code = "approval_unavailable"
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		status = http.StatusRequestTimeout
		code = "approval_unavailable"
	}
	if code == "approval_conflict" {
		if version, ok := semantic.ApprovalConflictCurrentVersion(err); ok {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(map[string]any{"code": code, "message": message, "currentVersion": version})
			return
		}
	}
	semanticJSONError(w, status, code, message)
}

func writeSemanticApprovalAccessError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	code := "approval_unavailable"
	message := "approval authorization is unavailable"
	var unauthenticated *semantic.ApprovalUnauthenticatedError
	var unauthorized *semantic.ApprovalUnauthorizedError
	switch {
	case errors.As(err, &unauthenticated):
		status = http.StatusUnauthorized
		code = "approval_unauthenticated"
	case errors.As(err, &unauthorized):
		status = http.StatusForbidden
		code = "approval_unauthorized"
	}
	semanticJSONError(w, status, code, message)
}

// handleTaskOnboarding implements the evidence-backed onboarding projection
// (Raw §8.9, VS-07). It accepts only an explicit repository and exact basis.
func (s *Server) handleTaskOnboarding(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	req, err := onboardingQueryValues(r.URL.Query())
	if err != nil {
		writeOnboardingHTTPError(w, err)
		return
	}
	if r.Method == http.MethodPost {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeOnboardingHTTPError(w, fmt.Errorf("invalid_precondition: decode onboarding request: %w", err))
			return
		}
		req, err = mergeOnboardingEnvelope(req, body)
		if err != nil {
			writeOnboardingHTTPError(w, err)
			return
		}
	}

	result, err := s.ExploreOnboarding(r.Context(), req)
	if err != nil {
		writeOnboardingHTTPError(w, err)
		return
	}
	payload, err := redactOnboardingJSON(result)
	if err != nil {
		writeOnboardingHTTPError(w, fmt.Errorf("internal_error: %w", err))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(payload)
}

func writeOnboardingHTTPError(w http.ResponseWriter, err error) {
	code := "onboarding_error"
	status := http.StatusBadRequest
	message := "onboarding request failed"
	if err != nil {
		message = err.Error()
		lower := strings.ToLower(message)
		switch {
		case strings.HasPrefix(lower, "missing_precondition:"), strings.HasPrefix(lower, "invalid_precondition:"), strings.HasPrefix(lower, "incomparable_basis:"):
			code = strings.SplitN(message, ":", 2)[0]
		case strings.HasPrefix(lower, "stale_live_head:"):
			code = "stale_live_head"
			status = http.StatusConflict
		case strings.HasPrefix(lower, "current_proof_unavailable:"), strings.HasPrefix(lower, "unavailable:"):
			code = "current_proof_unavailable"
			status = http.StatusPreconditionFailed
		case strings.HasPrefix(lower, "invalid_graph:"):
			code = "invalid_graph"
			status = http.StatusUnprocessableEntity
		case strings.HasPrefix(lower, "invalid_identity:"), strings.HasPrefix(lower, "invalid_evidence:"), strings.HasPrefix(lower, "invalid_authority:"), strings.HasPrefix(lower, "no_match:"):
			code = strings.SplitN(message, ":", 2)[0]
			status = http.StatusUnprocessableEntity
		case strings.HasPrefix(lower, "internal_error:"):
			code = "internal_error"
			status = http.StatusInternalServerError
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"code": code, "message": message})
}

// handleReleaseCapability evaluates only caller-supplied immutable evidence.
func (s *Server) handleReleaseCapability(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 8<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var input semantic.ReleaseEvaluationInput
	if err := decoder.Decode(&input); err != nil && !errors.Is(err, io.EOF) {
		writeReleaseAPIError(w, http.StatusBadRequest, fmt.Errorf("invalid release evaluation input: %w", err))
		return
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		writeReleaseAPIError(w, http.StatusBadRequest, fmt.Errorf("invalid release evaluation input: expected one JSON object"))
		return
	}

	evaluation, err := semantic.EvaluateReleaseCapabilityWithThresholdDecisions(input, s.releaseThresholdDecisions)
	if err != nil {
		writeReleaseAPIError(w, http.StatusInternalServerError, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(evaluation)
}

func writeReleaseAPIError(w http.ResponseWriter, status int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"code": "release_evaluation_error", "message": err.Error()})
}
