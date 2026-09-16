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
	"sync/atomic"
	"time"

	"codeflow/internal/contractharness"
	"codeflow/internal/detect"
	"codeflow/internal/fusion"
	"codeflow/internal/harvest"
	"codeflow/internal/protocol"
	"codeflow/internal/runtime"
	"codeflow/internal/secret"
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
	taskViewRecords     map[string]taskViewRecord
	taskViewOrder       []string
	hub                 *EventHub
	gate                *semantic.PublicationGate
	scheduler           *semantic.CoalescingScheduler
	mapCache            map[string]*semantic.SemanticMapIR
	flowContextMetadata map[string]flowContextGeneration
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
	runtimeExecutionSpec       runtime.RuntimeExecutionSpec
	runtimeConsent             *runtime.RuntimeConsent
	releaseThresholdDecisions  semantic.ThresholdDecisionResolver
	live                       liveState
	liveCtx                    context.Context
	liveCancel                 context.CancelFunc
	analysisMu                 sync.Mutex
	analysisCancel             context.CancelFunc
	compileCandidate           candidateCompiler
	watchInterval              time.Duration
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

	projectMu                    sync.RWMutex
	lastVerifiedBasisID          string
	livePrototype                bool
	svelteUI                     bool
	adapterRegistry              *protocol.AdapterRegistry
	adapterRegistryMu            sync.Mutex
	baselineCompileQueued        bool
	lastPublishedEntrySymbolPath string
	projectMode                  string
	projectStatus                string
	projectNotice                string
	projectGap                   *semantic.VerifiedGap
	projectEnv                   ProjectEnv
}

// ProjectEnv records the startup environment Live View actually detected for
// the target repository. It is diagnostic state for polling recovery and
// startup triage; the primary view renders only mode/status/notice.
type ProjectEnv struct {
	Language          string `json:"language"`
	Confident         bool   `json:"confident"`
	ProjectName       string `json:"projectName,omitempty"`
	WorkspaceMonorepo bool   `json:"workspaceMonorepo,omitempty"`
	AdapterResolved   bool   `json:"adapterResolved"`
	AdapterDetail     string `json:"adapterDetail,omitempty"`
}

// DetectProjectEnv identifies the target project's language, workspace
// layout, and adapter resolvability from the live worktree without spawning
// any adapter process.
func DetectProjectEnv(repoRoot string) ProjectEnv {
	env := ProjectEnv{}
	det := detect.Detect(repoRoot)
	env.Language = det.Language
	env.Confident = det.Confident
	env.ProjectName = det.ProjectName
	if det.Language == "dart" {
		if data, err := os.ReadFile(filepath.Join(repoRoot, "pubspec.yaml")); err == nil {
			for _, line := range strings.Split(string(data), "\n") {
				trimmed := strings.TrimSpace(line)
				if strings.HasPrefix(trimmed, "workspace:") {
					env.WorkspaceMonorepo = true
					break
				}
			}
		}
	}
	cwd, _ := filepath.Abs(".")
	if _, err := harvest.ResolveAdapterForRepo(repoRoot, cwd, det.Language, ""); err == nil {
		env.AdapterResolved = true
	} else {
		env.AdapterDetail = err.Error()
	}
	return env
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

func (s *Server) initialProjectGap(reason string) *semantic.VerifiedGap {
	if reason == "" {
		reason = "새 변경을 확인하지 못했습니다. 분석 기준을 확인하세요."
	}
	act := s.engine.CurrentActivity()
	if act.TraceID == "" {
		if head := s.engine.LiveHead(); head != nil && head.SnapshotID != "" {
			act.TraceID = "init-" + head.SnapshotID
		} else if act.CurrentSnapshotID != "" {
			act.TraceID = "init-" + act.CurrentSnapshotID
		} else {
			act.TraceID = "init-" + strconv.FormatInt(time.Now().UTC().UnixNano(), 10)
		}
	}
	if act.Timestamp.IsZero() {
		act.Timestamp = time.Now().UTC()
	}
	if act.Activity == "" {
		act.Activity = "reconciling"
	}
	if act.AnalysisLagMs < 0 {
		act.AnalysisLagMs = 0
	}
	if act.PendingRevisions < 0 {
		act.PendingRevisions = 0
	}
	latestID := act.CurrentSnapshotID
	epoch := act.WorkspaceEpoch
	if head := s.engine.LiveHead(); head != nil {
		latestID = head.SnapshotID
		epoch = head.WorkspaceEpoch
	}
	return &semantic.VerifiedGap{
		SchemaID:          "https://codeflow.local/schemas/rflsc.verified-gap.v2.schema.json",
		SchemaVersion:     2,
		Freshness:         "last_verified",
		Activity:          "reconciling",
		LatestSnapshotID:  latestID,
		WorkspaceEpoch:    epoch,
		AffectedScope:     []string{},
		AnalysisLagMs:     act.AnalysisLagMs,
		PendingRevisions:  act.PendingRevisions,
		IntersectedCauses: []string{reason},
		Timestamp:         act.Timestamp,
		TraceID:           act.TraceID,
	}
}

func (s *Server) setProjectGapLocked(reason string) {
	s.projectStatus = "gap"
	s.projectNotice = reason
	s.projectGap = s.initialProjectGap(reason)
}

func (s *Server) initLiveProjectState(mode string) {
	s.projectMu.Lock()
	s.projectMode = mode
	s.projectMu.Unlock()
	if mode != "project_change" {
		return
	}

	if s.engine.LiveHead() == nil {
		if snap, err := s.engine.Reconcile(context.Background(), nil); err == nil && snap != nil {
			s.engine.EndAnalysis(snap.SnapshotID, true)
		}
	}

	bundle, bundleErr := s.storage.ReadValidatedActiveProofBundle()
	if bundleErr != nil {
		s.projectMu.Lock()
		s.setProjectGapLocked("새 변경을 확인하지 못했습니다. 분석 기준을 확인하세요.")
		s.projectMu.Unlock()
		return
	}

	if bundle != nil && bundle.Manifest != nil {
		basis := bundle.Manifest.ValidatedAgainstSnapshotID
		if basis == "" {
			basis = bundle.Manifest.ComputedSnapshotID
		}
		snap, snapErr := s.engine.GetSnapshot(basis)
		if snapErr != nil || snap.WorkspaceEpoch != bundle.Manifest.WorkspaceEpoch {
			s.projectMu.Lock()
			s.setProjectGapLocked("새 변경을 확인하지 못했습니다. 분석 기준을 확인하세요.")
			s.projectMu.Unlock()
			return
		}
		s.projectMu.Lock()
		s.lastVerifiedBasisID = basis
		s.projectStatus = "watching"
		s.projectNotice = "현재 프로젝트의 변경을 감시하고 있습니다"
		s.projectGap = nil
		s.projectMu.Unlock()
		_ = WriteAnalysisBasis(s.repoRoot, AnalysisBasisRecord{
			BasisSnapshotID: basis,
			WorkspaceEpoch:  snap.WorkspaceEpoch,
			RootTreeID:      snap.RootTreeID,
		})
	} else {
		// Check if an established analysis basis was recorded
		basisRec, recErr := ReadAnalysisBasis(s.repoRoot)
		if recErr != nil {
			s.projectMu.Lock()
			s.setProjectGapLocked("새 변경을 확인하지 못했습니다. 분석 기준을 확인하세요.")
			s.projectMu.Unlock()
			return
		}
		if basisRec != nil && basisRec.BasisSnapshotID != "" {
			snap, snapErr := s.engine.GetSnapshot(basisRec.BasisSnapshotID)
			if snapErr != nil || snap.WorkspaceEpoch != basisRec.WorkspaceEpoch {
				s.projectMu.Lock()
				s.setProjectGapLocked("새 변경을 확인하지 못했습니다. 분석 기준을 확인하세요.")
				s.projectMu.Unlock()
				return
			}
			s.projectMu.Lock()
			s.lastVerifiedBasisID = basisRec.BasisSnapshotID
			s.projectStatus = "watching"
			s.projectNotice = "현재 프로젝트의 변경을 감시하고 있습니다"
			s.projectGap = nil
			s.projectMu.Unlock()
		} else {
			// First start without prior basis: enqueue exactly one baseline
			// compile. The head is not marked verified and no basis file is
			// written until the first publication commits.
			if head := s.engine.LiveHead(); head != nil && s.scheduler != nil {
				s.projectMu.Lock()
				if !s.baselineCompileQueued {
					s.baselineCompileQueued = true
					s.projectStatus = "pending"
					s.projectNotice = "변경을 확인 중입니다"
					s.projectMu.Unlock()
					s.scheduler.NotifyEdit(head)
				} else {
					s.projectMu.Unlock()
				}
			} else {
				s.projectMu.Lock()
				s.projectStatus = "watching"
				s.projectNotice = "현재 프로젝트의 변경을 감시하고 있습니다"
				s.projectGap = nil
				s.projectMu.Unlock()
			}
		}
	}

	// Check if offline disk changes occurred or if unanalyzed edits accumulated
	needsPending := false
	if recSnap, err := s.engine.ReconcileIfChanged(context.Background(), nil); err == nil && recSnap != nil {
		if s.engine.LiveHeadID() != s.LastVerifiedBasisID() {
			needsPending = true
		}
	} else if s.engine.LiveHeadID() != s.LastVerifiedBasisID() {
		needsPending = true
	}
	if needsPending {
		s.projectMu.Lock()
		// Do not override an existing gap set above.
		if s.projectStatus == "watching" {
			s.projectStatus = "pending"
			s.projectNotice = "변경을 확인 중입니다"
		}
		s.projectMu.Unlock()
		if head := s.engine.LiveHead(); head != nil && s.scheduler != nil {
			s.scheduler.NotifyEdit(head)
		}
	}
}

// LastVerifiedBasisID returns the snapshot ID of the last verified comparison basis.
func (s *Server) LastVerifiedBasisID() string {
	s.projectMu.RLock()
	defer s.projectMu.RUnlock()
	return s.lastVerifiedBasisID
}

// ProjectEnvSnapshot returns the startup environment detected for the target.
func (s *Server) ProjectEnvSnapshot() ProjectEnv {
	s.projectMu.RLock()
	defer s.projectMu.RUnlock()
	return s.projectEnv
}

// handleLiveProject serves the project-change watch state. The primary view
// renders only mode, status, title, and notice. basisId, lastVerifiedBasisId,
// liveHeadId, and gap internals are diagnostic for polling recovery and are
// never rendered as primary telemetry per INV-LIVE-09.
func (s *Server) handleLiveProject(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.projectMu.RLock()
	defer s.projectMu.RUnlock()

	var clientGap any
	if s.projectGap != nil {
		affectedScope := s.projectGap.AffectedScope
		if affectedScope == nil {
			affectedScope = []string{}
		}
		clientGap = map[string]any{
			"status":        "gap",
			"affectedScope": affectedScope,
			"notice":        s.projectNotice,
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"mode":                s.projectMode,
		"status":              s.projectStatus,
		"title":               "프로젝트 변경 감시 상태",
		"notice":              s.projectNotice,
		"basisId":             s.lastVerifiedBasisID,
		"lastVerifiedBasisId": s.lastVerifiedBasisID,
		"liveHeadId":          s.engine.LiveHeadID(),
		"gap":                 clientGap,
		"env":                 s.projectEnv,
	})
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
	RuntimeExecutionSpec runtime.RuntimeExecutionSpec
	RuntimeConsent       *runtime.RuntimeConsent
	// ModelHostFactory creates one Core-supervised host per enrichment request.
	// The returned host is owned and closed by that request.
	ModelHostFactory protocol.ModelHostFactory
	// WorkspaceWatchInterval controls the coordinator-owned fallback watcher.
	// Values at or below zero use the production default.
	WorkspaceWatchInterval time.Duration
	// Mode configures the FlowView mode ("project_change" or "feature").
	Mode string
	// LivePrototype keeps serving the archived live prototype on /live in
	// project_change mode (MCP coordinator contract). The CLI live surface
	// leaves this false so /live serves the 7-lane FlowView.
	LivePrototype bool
	// SvelteUI enables serving the modular Svelte 5 FlowView application.
	SvelteUI bool
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
		watchInterval:              cfg.WorkspaceWatchInterval,
		livePrototype:              cfg.LivePrototype,
		svelteUI:                   cfg.SvelteUI,
	}
	if err := recoverPendingApprovalOutboxDeliveries(context.Background(), s.approvalService, s.hub); err != nil {
		return nil, fmt.Errorf("recover approval outbox: %w", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/api/flows", s.handleListFlows)
	mux.HandleFunc("/api/views", s.handleSavedViews)
	mux.HandleFunc("/api/view/legacy", s.handleLegacyView)
	mux.HandleFunc("/api/view", s.handleSavedView)
	mux.HandleFunc("/api/view/compare", s.handleSavedComparison)
	mux.HandleFunc("/api/flow", s.handleGetFlow)
	mux.HandleFunc("/api/flow/context", s.handleFlowContext)
	mux.HandleFunc("/api/source", s.handleGetSource)
	mux.HandleFunc("/api/approve", s.handleApprove)
	mux.HandleFunc("/api/map", s.handleGetMap)
	mux.HandleFunc("/api/map/override", s.handlePostLaneOverride)
	mux.HandleFunc("/api/task/view", s.handleTaskView)
	mux.HandleFunc("/api/task/review", s.handleTaskReview)
	mux.HandleFunc("/api/task/requirement", s.handleTaskRequirement)
	mux.HandleFunc("/api/task/analyses", s.handleTaskAnalyses)
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
	mux.HandleFunc("/api/live/generation", s.handleLiveGeneration)
	mux.HandleFunc("/api/live/project", s.handleLiveProject)

	s.projectMu.Lock()
	s.projectEnv = DetectProjectEnv(cfg.RepoRoot)
	s.projectMu.Unlock()
	s.initLiveProjectState(cfg.Mode)

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

// SubmitVersionedChanges is the one coordinator ingress used by IDE, agent,
// and watcher producers. Duplicate captures return the existing identity and
// do not schedule or notify a second change.
func (s *Server) SubmitVersionedChanges(ctx context.Context, request workspace.VersionedChangeRequest) (*workspace.VersionedChangeResult, error) {
	if s == nil || s.engine == nil || s.scheduler == nil || s.hub == nil {
		return nil, fmt.Errorf("live coordinator is unavailable")
	}
	result, err := s.engine.ApplyVersionedChanges(ctx, request)
	if err != nil {
		if errors.Is(err, workspace.ErrLiveHeadConflict) {
			s.projectMu.Lock()
			prevStatus := s.projectStatus
			prevNotice := s.projectNotice
			s.projectStatus = "reconciling"
			s.projectNotice = "변경을 다시 확인하고 있습니다"
			s.projectMu.Unlock()
			s.engine.SetActivity("reconciling")
			act := s.engine.CurrentActivity()
			if act.CurrentSnapshotID != "" {
				basisID := act.CurrentSnapshotID
				snapID := act.CurrentSnapshotID
				if head := s.engine.LiveHead(); head != nil {
					if head.ComputedBasisID != "" {
						basisID = head.ComputedBasisID
					}
					if head.SnapshotID != "" {
						snapID = head.SnapshotID
					}
				}
				_, _ = s.hub.PublishChecked("activity.updated", act, &basisID, &snapID, nil)
			}

			var reloadErr error
			const maxConflictRetries = 5
			for attempt := 0; attempt < maxConflictRetries; attempt++ {
				jitter := time.Duration(time.Now().UnixNano()%6) * time.Millisecond
				backoff := (time.Duration(5+attempt*4) * time.Millisecond) + jitter
				select {
				case <-ctx.Done():
					s.projectMu.Lock()
					s.projectStatus = prevStatus
					s.projectNotice = prevNotice
					s.projectMu.Unlock()
					return nil, ctx.Err()
				case <-time.After(backoff):
				}

				reloadErr = s.engine.ReloadFromDurable()
				if reloadErr != nil {
					s.recordPipelineError(reloadErr)
					break
				}
				result, err = s.engine.ApplyVersionedChanges(ctx, request)
				if err == nil || !errors.Is(err, workspace.ErrLiveHeadConflict) {
					break
				}
			}
			if err == nil {
				s.projectMu.Lock()
				s.projectStatus = "pending"
				s.projectNotice = "변경을 확인 중입니다"
				s.projectMu.Unlock()
			}
			if err != nil {
				// A retry failure that is no longer a head conflict (for
				// example a document-version or validation input error) is a
				// caller error, not a system gap. Restore the pre-recovery
				// state and return the input error unchanged.
				if reloadErr == nil && !errors.Is(err, workspace.ErrLiveHeadConflict) {
					s.projectMu.Lock()
					s.projectStatus = prevStatus
					s.projectNotice = prevNotice
					s.projectMu.Unlock()
					return nil, err
				}
				var paths []string
				for _, c := range request.Changes {
					paths = append(paths, c.Path)
				}
				reason := "새 변경을 확인하지 못했습니다"
				if gapErr := s.publishConflictGap(paths, reason); gapErr != nil {
					s.recordPipelineError(gapErr)
				}
				return nil, err
			}
		} else {
			return nil, err
		}
	}
	if result.Duplicate {
		return result, nil
	}
	if err := s.notifyAcceptedSnapshot(result.Snapshot); err != nil {
		return result, err
	}
	return result, nil
}

// publishConflictGap records a measured recoverable gap after a live-head
// conflict retry fails. It uses the canonical gap path so SSE subscribers
// observe the same gap state as pollers.
func (s *Server) publishConflictGap(paths []string, reason string) error {
	if paths == nil {
		paths = []string{}
	}
	if reason == "" {
		reason = "새 변경을 확인하지 못했습니다"
	}
	snap := s.engine.LiveHead()
	act := s.engine.CurrentActivity()
	if act.TraceID == "" {
		if snap != nil && snap.SnapshotID != "" {
			act.TraceID = "conflict-" + snap.SnapshotID
		} else if act.CurrentSnapshotID != "" {
			act.TraceID = "conflict-" + act.CurrentSnapshotID
		} else {
			act.TraceID = "conflict-" + strconv.FormatInt(time.Now().UTC().UnixNano(), 10)
		}
	}
	if act.Timestamp.IsZero() {
		act.Timestamp = time.Now().UTC()
	}
	if act.Activity == "" {
		act.Activity = "idle"
	}
	if act.AnalysisLagMs < 0 {
		act.AnalysisLagMs = 0
	}
	if act.PendingRevisions < 0 {
		act.PendingRevisions = 0
	}
	if snap == nil {
		s.projectMu.Lock()
		s.projectStatus = "gap"
		s.projectNotice = reason
		s.projectGap = &semantic.VerifiedGap{
			SchemaID:          "https://codeflow.local/schemas/rflsc.verified-gap.v2.schema.json",
			SchemaVersion:     2,
			Freshness:         "last_verified",
			Activity:          act.Activity,
			LatestSnapshotID:  act.CurrentSnapshotID,
			WorkspaceEpoch:    act.WorkspaceEpoch,
			AffectedScope:     paths,
			AnalysisLagMs:     act.AnalysisLagMs,
			PendingRevisions:  act.PendingRevisions,
			IntersectedCauses: []string{reason},
			Timestamp:         act.Timestamp,
			TraceID:           act.TraceID,
		}
		s.projectMu.Unlock()
		return fmt.Errorf("conflict gap has no live head")
	}
	gap := &semantic.VerifiedGap{
		SchemaID:          "https://codeflow.local/schemas/rflsc.verified-gap.v2.schema.json",
		SchemaVersion:     2,
		Freshness:         "last_verified",
		Activity:          act.Activity,
		LatestSnapshotID:  snap.SnapshotID,
		WorkspaceEpoch:    snap.WorkspaceEpoch,
		AffectedScope:     paths,
		AnalysisLagMs:     act.AnalysisLagMs,
		PendingRevisions:  act.PendingRevisions,
		IntersectedCauses: []string{reason},
		Timestamp:         act.Timestamp,
		TraceID:           act.TraceID,
	}
	return s.publishGapValue(gap, snap)
}

func (s *Server) notifyAcceptedSnapshot(snap *workspace.WorkspaceSnapshot) error {
	if snap == nil {
		return fmt.Errorf("accepted workspace snapshot is missing")
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
		return fmt.Errorf("persist activity event: %w", err)
	}
	// Acknowledgement is recorded only after the edit activity has a durable
	// event. Publish the post-ack state as a second bounded event so observers
	// can measure the transition without treating an in-memory timestamp as
	// evidence.
	s.engine.AcknowledgeEdit(snap.SnapshotID)
	if _, err := s.hub.PublishChecked("activity.updated", s.engine.CurrentActivity(), &snap.ComputedBasisID, &snap.SnapshotID, nil); err != nil {
		return fmt.Errorf("persist activity acknowledgement: %w", err)
	}
	return nil
}

// Start runs the HTTP server in the background.
func (s *Server) Start() {
	s.startOnce.Do(func() {
		s.startLiveConsumer()
		go func() { _ = s.httpServer.Serve(s.listener) }()
		_, _, _ = ClaimLiveCoordinator(s.repoRoot, LiveCoordinatorRecord{
			URL:       s.URL(),
			Token:     s.authToken,
			PID:       os.Getpid(),
			StartedAt: time.Now().UTC(),
			RepoRoot:  s.repoRoot,
		})
	})
}

// Shutdown gracefully shuts down the HTTP server. Callers without a
// deadline get a 5s ceiling so in-flight artifact writes can finish; the
// timeout is a ceiling, not a delay, and Shutdown returns as soon as done.
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
				_ = s.httpServer.Close()
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
		s.closeAdapterRegistry()
		if waitErr := s.waitModelHostRequests(ctx); waitErr != nil {
			waitErrors = append(waitErrors, waitErr)
		}
		firstErr := joinLifecycleErrors(append([]error{initErr}, waitErrors...)...)
		_ = RemoveLiveCoordinatorIfOwned(s.repoRoot, s.URL(), os.Getpid())
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
	if r.URL.Path != "/" && r.URL.Path != "/live" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	s.projectMu.RLock()
	prototype := s.livePrototype
	svelte := s.svelteUI
	s.projectMu.RUnlock()
	if r.URL.Path == "/live" && prototype {
		_, _ = w.Write([]byte(LiveViewHTML))
		return
	}
	if svelte || r.URL.Query().Get("ui") == "svelte" {
		_, _ = w.Write([]byte(SvelteFlowViewHTML))
		return
	}
	_, _ = w.Write([]byte(FlowViewHTML))
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

	content = secret.Redact(content).Text
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(content))
}

func (s *Server) handleFlowContext(w http.ResponseWriter, r *http.Request) {
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
	var mapIR *semantic.SemanticMapIR
	mapIR = s.mapCache[genID]
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

	// Selection and expansion read the generation's original source, never
	// reconcile or silently substitute the live workspace head.
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

// writeTaskViewError reports a task/view failure as a structured error screen
// payload. A result-less failure never masquerades as a candidate result.
func writeTaskViewError(w http.ResponseWriter, err error, status int) {
	code := "internal_error"
	message := ""
	if err != nil {
		message = err.Error()
		for _, candidate := range []string{"missing_precondition", "invalid_precondition", "ambiguous_target", "incomparable_basis", "unavailable", "unknown", "conflict"} {
			if strings.HasPrefix(message, candidate+":") || message == candidate {
				code = candidate
				break
			}
		}
	}
	if status == 0 {
		status = http.StatusInternalServerError
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"code": code, "message": message})
}

// taskViewRecord binds one explicit view request to the snapshot basis it
// analyzed. The same request ID with the same input and basis reuses the
// stored payload; the same ID with different input is a conflict.
type taskViewRecord struct {
	inputHash    string
	basisID      string
	generationID string
	payload      []byte
}

// taskViewInputHash identifies the user-supplied portion of a view request.
// Snapshot basis is tracked separately so an edit between retries produces a
// new analysis instead of a stale reuse.
func taskViewInputHash(mode, query, flowID, entrySymbol, domain string) string {
	raw := strings.Join([]string{mode, query, flowID, entrySymbol, domain}, "\x00")
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// lookupTaskViewRecord returns the stored payload when the request repeats the
// same input against the same snapshot basis. A differing input is a
// conflict; a new basis means the caller must analyze again.
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
	requestID := strings.TrimSpace(r.URL.Query().Get("requestId"))
	var taskViewBody map[string]any

	if r.Method == http.MethodPost {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err == nil {
			taskViewBody = body
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
		writeTaskViewError(w, err, http.StatusInternalServerError)
		return
	}
	defer releaseSnapshot()

	if strings.TrimSpace(requestID) == "" {
		requestID = newTaskViewRequestID()
	}
	inputHash := taskViewInputHash(mode, reqQuery, flowID, entrySymbol, domain)
	if cached, err := s.lookupTaskViewRecord(requestID, inputHash, snapshot.ComputedBasisID); err != nil {
		writeTaskViewError(w, err, http.StatusConflict)
		return
	} else if cached != nil {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(cached)
		return
	}

	det := detect.DetectSnapshot(snapshot.Files)
	lang := det.Language
	if lang == "" || lang == "unknown" {
		lang = "typescript"
	}
	cwd, _ := filepath.Abs(".")
	adapterCfg, err := harvest.ResolveAdapterForRepo(s.repoRoot, cwd, lang, "")
	if err != nil {
		writeTaskViewError(w, fmt.Errorf("unavailable: resolve adapter: %w", err), http.StatusInternalServerError)
		return
	}
	pool := protocol.NewPool(adapterCfg, 2)
	defer pool.Close()

	harvester := harvest.NewRunnerWithPool(pool)
	candidates, err := harvester.RunWithSnapshot(ctx, s.repoRoot, snapshot)
	if err != nil {
		writeTaskViewError(w, fmt.Errorf("unavailable: harvest candidates: %w", err), http.StatusInternalServerError)
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
		writeTaskViewError(w, err, http.StatusBadRequest)
		return
	}

	slicer := slicing.NewRunner(pool)
	slicePayload, err := slicer.SliceWithSnapshot(ctx, s.repoRoot, resolved.CandidateID, resolved.EntrySymbolPath, nil, snapshot)
	if err != nil {
		writeTaskViewError(w, fmt.Errorf("unavailable: slice error: %w", err), http.StatusInternalServerError)
		return
	}

	reqText := reqQuery
	if reqText == "" {
		reqText = resolved.Title
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

	s.mu.Lock()
	if s.mapCache == nil {
		s.mapCache = make(map[string]*semantic.SemanticMapIR)
	}
	s.mapCache[mapIR.GenerationID] = mapIR
	s.mapCache[mapIR.ComputedBasisID] = mapIR
	s.mu.Unlock()
	s.rememberLiveRequest(query, reqText)

	evidenceRecords, _ := semantic.ExtractAndRedactEvidenceFromProtocolSnapshot(resolved, slicePayload, snapshot)

	adapterHasFlowContext := false
	if conn, connErr := pool.Get(ctx); connErr == nil {
		adapterHasFlowContext = conn.Version().Capabilities.FlowContext
		pool.Put(conn)
	}
	metadata := s.rememberFlowContexts(mapIR, slicePayload, adapterHasFlowContext)
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
		"publicationGate":     map[string]any{"eligibility": "not_evaluated", "reason": "VS03 current proof required"},
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
	result["request"] = &semantic.FeatureQueryParams{Request: reqText, EntrySymbol: resolved.EntrySymbolPath, FlowID: resolved.CandidateID, Domain: domain}
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
	s.storeTaskViewRecord(requestID, inputHash, snapshot.ComputedBasisID, mapIR.GenerationID, payload)
	_, _ = w.Write(payload)
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
		BatchID         string `json:"batchId"`
		Kind            string `json:"kind"`
		OldPath         string `json:"oldPath"`
		Changes         []struct {
			Kind            string `json:"kind"`
			Path            string `json:"path"`
			OldPath         string `json:"oldPath"`
			Content         string `json:"content"`
			ContentID       string `json:"contentId"`
			DocumentVersion int    `json:"documentVersion"`
		} `json:"changes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.Source == "" {
		req.Source = workspace.SourceAgentTransaction
	}
	changes := make([]workspace.VersionedChange, 0, len(req.Changes))
	for _, change := range req.Changes {
		changes = append(changes, workspace.VersionedChange{Kind: workspace.ChangeKind(change.Kind), Path: change.Path, OldPath: change.OldPath, Content: []byte(change.Content), ContentID: change.ContentID, DocumentVersion: change.DocumentVersion})
	}
	if len(changes) == 0 {
		kind := workspace.ChangeKind(req.Kind)
		if kind == "" {
			kind = workspace.ChangeUpsert
		}
		changes = append(changes, workspace.VersionedChange{Kind: kind, Path: req.Path, OldPath: req.OldPath, Content: []byte(req.Content), DocumentVersion: req.DocumentVersion})
	}
	if req.BatchID == "" {
		digest := sha256.Sum256([]byte(req.Source + "\x00" + req.Path + "\x00" + strconv.Itoa(req.DocumentVersion) + "\x00" + req.Content))
		req.BatchID = "http-" + hex.EncodeToString(digest[:16])
	}
	result, err := s.SubmitVersionedChanges(r.Context(), workspace.VersionedChangeRequest{BatchID: req.BatchID, Source: req.Source, Changes: changes})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var revision *workspace.DocumentRevision
	if len(result.Revisions) > 0 {
		revision = result.Revisions[0]
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"revision":  revision,
		"revisions": result.Revisions,
		"batch":     result.Batch,
		"snapshot":  result.Snapshot,
		"duplicate": result.Duplicate,
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
	flusher.Flush()

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
		item := map[string]any{
			"time":            time.Now().UTC().Format("15:04:05"),
			"summary":         ch.Summary,
			"kind":            ch.Kind,
			"targetStepId":    ch.TargetStepID,
			"epistemicStatus": ch.EpistemicStatus,
		}
		symbol, side, sideMap := pulseNavigationTarget(ch, baseMap, currMap)
		item["symbol"] = symbol
		item["side"] = side
		item["navigable"] = symbol != "" && sideMap != nil
		if sideMap != nil {
			item["sideGenerationId"] = sideMap.GenerationID
			item["sideBasisId"] = sideMap.ComputedBasisID
		}
		changePulse = append(changePulse, item)
	}

	resp := map[string]any{
		"semanticDelta":        delta,
		"requirementAlignment": alignments,
		"changePulse":          changePulse,
		"structuralSummary":    delta.StructuralSummary,
		"baseline":             analysisDescriptor(baseMap, "preserved"),
		"current":              analysisDescriptor(currMap, "explicit"),
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
