package semantic

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"codeflow/internal/contractharness"
	"codeflow/internal/rflscvs02"
	"codeflow/internal/storage"
	"codeflow/internal/workspace"
)

// CoalescingConfig defines the publication coalescing timing parameters (Raw §7.2).
type CoalescingConfig struct {
	QuietWindow  time.Duration
	MaxWait      time.Duration
	MaxQueueSize int
}

// DefaultCoalescingConfig provides standard 2-second publication coalescing defaults.
func DefaultCoalescingConfig() CoalescingConfig {
	return CoalescingConfig{
		QuietWindow:  2 * time.Second,
		MaxWait:      2 * time.Second,
		MaxQueueSize: 50,
	}
}

// CoalescingScheduler manages 2s quiet window and max-wait publication snapshot selection (Raw §7.2, VS04-A1, VS04-A12).
type CoalescingScheduler struct {
	mu           sync.Mutex
	cfg          CoalescingConfig
	latestSnap   *workspace.WorkspaceSnapshot
	quietTimer   *time.Timer
	maxTimer     *time.Timer
	cycleActive  bool
	checkpoints  chan *workspace.WorkspaceSnapshot
	pendingCount int
	closed       bool
}

// NewCoalescingScheduler initializes a coalescing scheduler.
func NewCoalescingScheduler(cfg CoalescingConfig) *CoalescingScheduler {
	if cfg.QuietWindow <= 0 {
		cfg.QuietWindow = 2 * time.Second
	}
	if cfg.MaxWait <= 0 {
		cfg.MaxWait = 2 * time.Second
	}
	if cfg.MaxQueueSize <= 0 {
		cfg.MaxQueueSize = 50
	}
	return &CoalescingScheduler{
		cfg:         cfg,
		checkpoints: make(chan *workspace.WorkspaceSnapshot, cfg.MaxQueueSize),
	}
}

// Checkpoints returns a receive-only channel of selected publication snapshots.
func (s *CoalescingScheduler) Checkpoints() <-chan *workspace.WorkspaceSnapshot {
	return s.checkpoints
}

// HasPending reports whether an uncoalesced edit awaits a checkpoint.
func (s *CoalescingScheduler) HasPending() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.latestSnap != nil
}

// NotifyEdit notifies the scheduler that a new WorkspaceSnapshot has been recorded.
func (s *CoalescingScheduler) NotifyEdit(snap *workspace.WorkspaceSnapshot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || snap == nil {
		return
	}

	s.latestSnap = snap
	s.pendingCount++

	// 1. Reset quiet timer
	if s.quietTimer != nil {
		s.quietTimer.Stop()
	}
	s.quietTimer = time.AfterFunc(s.cfg.QuietWindow, func() {
		s.triggerCheckpoint("quiet_window")
	})

	// 2. Start max wait timer on the first uncoalesced edit in cycle
	if !s.cycleActive {
		s.cycleActive = true
		s.maxTimer = time.AfterFunc(s.cfg.MaxWait, func() {
			s.triggerCheckpoint("max_wait")
		})
	}
}

func (s *CoalescingScheduler) triggerCheckpoint(reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}

	if s.latestSnap == nil {
		return
	}

	selected := s.latestSnap
	// Consume the selected snapshot before stopping the timers. Both timer
	// callbacks may already be runnable when one acquires the mutex. Keeping the
	// value here would let the second callback enqueue the same checkpoint again.
	s.latestSnap = nil
	s.cycleActive = false
	s.pendingCount = 0

	if s.quietTimer != nil {
		s.quietTimer.Stop()
		s.quietTimer = nil
	}
	if s.maxTimer != nil {
		s.maxTimer.Stop()
		s.maxTimer = nil
	}

	// A full queue is an overload signal, not permission to lose the newest
	// edit. Coalescing makes the selected snapshot the only value that needs to
	// survive, so replace stale queued values before enqueueing it. The channel
	// is never closed on overload and the always-on consumer can continue.
	for {
		select {
		case <-s.checkpoints:
			continue
		default:
		}
		break
	}
	select {
	case s.checkpoints <- selected:
	default:
		// A concurrent consumer cannot make the channel full again while this
		// mutex is held. If a caller configured a zero-capacity channel, retain
		// the snapshot as the next latest value instead of dropping it.
		s.latestSnap = selected
	}
}

// Close stops timer callbacks and closes the checkpoint stream. It is safe to
// call more than once and is required by long-lived servers to avoid timer and
// goroutine leaks.
func (s *CoalescingScheduler) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	if s.quietTimer != nil {
		s.quietTimer.Stop()
		s.quietTimer = nil
	}
	if s.maxTimer != nil {
		s.maxTimer.Stop()
		s.maxTimer = nil
	}
	close(s.checkpoints)
}

// RefinementCoordinator coordinates same-basis late refinement publication (Raw §7.4, §13.2, VS04-A7).
type RefinementCoordinator struct {
	storage *storage.Storage
	gate    *PublicationGate
}

// LateRefinementInput is the complete, snapshot-bound input required to
// promote an enrichment of an existing current proof. A late refinement is a
// second publication proof, not a pointer-only metadata update.
type LateRefinementInput struct {
	Map                *SemanticMapIR
	Projection         *FlowViewProjection
	Closure            *CausalObservationClosure
	Delta              *workspace.WorkspaceDelta
	CapturedSnapshot   *workspace.WorkspaceSnapshot
	LiveHeadSnapshot   *workspace.WorkspaceSnapshot
	Intent             *TaskIntent
	AnalysisRequest    *rflscvs02.AnalyzerRequest
	AnalysisResult     *rflscvs02.Result
	PreviousMapBytes   []byte
	ArtifactBytes      map[string][]byte
	ArtifactDigests    map[string]string
	Event              []byte
	RepositoryID       string
	WorktreeID         string
	QueryHash          string
	ExpectedPreviousID string
	// Metrics are measured at the same checkpoint as the analyzer result. A
	// late refinement cannot manufacture lag, pending, or trace evidence.
	Metrics PublicationMetrics
	// LiveHeadCommit must hold the authoritative workspace head lock while
	// the publisher executes the storage transaction.
	LiveHeadCommit func(expectedID string, commit func() error) error
	// Publish is injectable only for product integration tests and for the
	// EventHub wrapper. The default is Storage.PublishGeneration.
	Publish func(storage.PublicationTransaction) (storage.PublicationCommit, error)
}

// NewRefinementCoordinator creates a refinement coordinator.
func NewRefinementCoordinator(st *storage.Storage) *RefinementCoordinator {
	return &RefinementCoordinator{
		storage: st,
		gate:    NewPublicationGate(),
	}
}

// PublishLateRefinement is the retired metadata-only API. It cannot create a
// current proof because it has no immutable snapshot, analyzer result, or
// causal closure to validate.
func (c *RefinementCoordinator) PublishLateRefinement(
	lateMapIR *SemanticMapIR,
	closure *CausalObservationClosure,
	expectedLiveHeadSnapshotID string,
	expectedPreviousGenID string,
) (bool, error) {
	return false, fmt.Errorf("legacy late refinement API cannot publish current authority; use PublishLateRefinementV2")
}

// PublishLateRefinementV2 revalidates a late refinement through the same
// current-publication gate as an ordinary checkpoint and commits it through a
// single storage transaction. No durable write occurs before every identity,
// analyzer, closure, evidence, and Q3 digest check succeeds.
func (c *RefinementCoordinator) PublishLateRefinementV2(input LateRefinementInput) (storage.PublicationCommit, error) {
	if c == nil || c.storage == nil || c.gate == nil {
		return storage.PublicationCommit{}, fmt.Errorf("refinement coordinator is unavailable")
	}
	if err := validateLateRefinementInput(input); err != nil {
		return storage.PublicationCommit{}, err
	}
	activeManifest, activePointer, err := c.storage.ReadValidatedActiveProofManifest()
	if err != nil {
		return storage.PublicationCommit{}, fmt.Errorf("read validated active proof: %w", err)
	}
	if activeManifest == nil || activePointer == nil {
		return storage.PublicationCommit{}, fmt.Errorf("no validated active proof currently published")
	}
	if input.ExpectedPreviousID == "" || input.ExpectedPreviousID != activePointer.GenerationID || input.ExpectedPreviousID == input.Map.GenerationID {
		return storage.PublicationCommit{}, fmt.Errorf("late refinement predecessor identity is stale or missing")
	}
	if activeManifest.ComputedBasisID != input.Map.ComputedBasisID || activeManifest.WorkspaceEpoch != input.CapturedSnapshot.WorkspaceEpoch || activeManifest.TaskIntentRevision != input.Intent.Revision || activeManifest.NormalizedQueryHash != input.QueryHash || activePointer.RepositoryID != input.RepositoryID || activePointer.WorktreeID != input.WorktreeID || activePointer.TaskID != input.Intent.TaskID {
		return storage.PublicationCommit{}, fmt.Errorf("late refinement does not match active proof scope")
	}
	if activeManifest.ValidatedAgainstSnapshotID != activePointer.ValidatedAgainstSnapshotID || activePointer.ExpectedLiveHeadSnapshotID != input.LiveHeadSnapshot.SnapshotID {
		return storage.PublicationCommit{}, fmt.Errorf("late refinement live-head identity is stale")
	}
	if activeManifest.ArtifactRefs.SemanticMap == "" || storage.ArtifactCASRef(input.PreviousMapBytes) != activeManifest.ArtifactRefs.SemanticMap {
		return storage.PublicationCommit{}, fmt.Errorf("late refinement prior semantic-map artifact is not the active CAS object")
	}
	if err := contractharness.ValidateSemanticMapIR(input.PreviousMapBytes); err != nil {
		return storage.PublicationCommit{}, fmt.Errorf("validate prior semantic map: %w", err)
	}
	var previousMap SemanticMapIR
	if err := json.Unmarshal(input.PreviousMapBytes, &previousMap); err != nil {
		return storage.PublicationCommit{}, fmt.Errorf("decode prior semantic map: %w", err)
	}
	previousDigests, err := q3Digests(&previousMap)
	if err != nil {
		return storage.PublicationCommit{}, err
	}
	currentDigests, err := q3Digests(input.Map)
	if err != nil {
		return storage.PublicationCommit{}, err
	}
	if previousDigests != currentDigests {
		return storage.PublicationCommit{}, fmt.Errorf("late refinement changes Q3 fact/alignment/obligation/settlement digest")
	}

	// Build every artifact from the validated input before entering the
	// publication gate. The caller's artifact map and digest map are never
	// trusted as proof of these bytes.
	mapBytes, err := json.Marshal(input.Map)
	if err != nil {
		return storage.PublicationCommit{}, fmt.Errorf("marshal late refinement map: %w", err)
	}
	if err := contractharness.ValidateSemanticMapIR(mapBytes); err != nil {
		return storage.PublicationCommit{}, fmt.Errorf("validate late refinement map artifact: %w", err)
	}
	mapRef := storage.ArtifactCASRef(mapBytes)
	projectionBytes, err := json.Marshal(input.Projection)
	if err != nil {
		return storage.PublicationCommit{}, fmt.Errorf("marshal late refinement projection: %w", err)
	}
	if err := contractharness.ValidateFlowViewProjection(projectionBytes); err != nil {
		return storage.PublicationCommit{}, fmt.Errorf("validate late refinement projection artifact: %w", err)
	}
	projectionRef := storage.ArtifactCASRef(projectionBytes)
	readSetBytes, err := json.Marshal(input.AnalysisResult.ReadSet)
	if err != nil {
		return storage.PublicationCommit{}, fmt.Errorf("marshal late refinement analysis read-set: %w", err)
	}
	if err := contractharness.Validate(rflscvs02.ReadSetSchemaID, readSetBytes); err != nil {
		return storage.PublicationCommit{}, fmt.Errorf("validate late refinement analysis read-set artifact: %w", err)
	}
	readSetRef := storage.ArtifactCASRef(readSetBytes)
	closureBytes, err := json.Marshal(input.AnalysisResult.Closure)
	if err != nil {
		return storage.PublicationCommit{}, fmt.Errorf("marshal late refinement observation closure: %w", err)
	}
	if err := contractharness.Validate(rflscvs02.ClosureSchemaID, closureBytes); err != nil {
		return storage.PublicationCommit{}, fmt.Errorf("validate late refinement observation closure artifact: %w", err)
	}
	closureRef := storage.ArtifactCASRef(closureBytes)
	resultBytes, err := json.Marshal(input.AnalysisResult)
	if err != nil {
		return storage.PublicationCommit{}, fmt.Errorf("marshal late refinement analyzer result: %w", err)
	}
	if err := contractharness.Validate(rflscvs02.AnalyzerResultSchemaID, resultBytes); err != nil {
		return storage.PublicationCommit{}, fmt.Errorf("validate late refinement analyzer result artifact: %w", err)
	}
	resultRef := storage.ArtifactCASRef(resultBytes)
	capabilityDigest, err := CanonicalCapabilityProfileDigest(input.AnalysisResult.Capability)
	if err != nil {
		return storage.PublicationCommit{}, fmt.Errorf("digest late refinement capability profile: %w", err)
	}
	if activeManifest.CapabilityProfileDigest == "" || activeManifest.CapabilityProfileDigest != capabilityDigest {
		return storage.PublicationCommit{}, fmt.Errorf("late refinement capability profile is not bound to the active proof")
	}

	artifactBytes := map[string][]byte{
		mapRef:        append([]byte(nil), mapBytes...),
		projectionRef: append([]byte(nil), projectionBytes...),
		readSetRef:    append([]byte(nil), readSetBytes...),
		closureRef:    append([]byte(nil), closureBytes...),
		resultRef:     append([]byte(nil), resultBytes...),
	}
	artifactDigests := map[string]string{
		"semanticMap":        strings.TrimPrefix(mapRef, "cas:sha256:"),
		"projection":         strings.TrimPrefix(projectionRef, "cas:sha256:"),
		"analysisReadSet":    strings.TrimPrefix(readSetRef, "cas:sha256:"),
		"observationClosure": strings.TrimPrefix(closureRef, "cas:sha256:"),
		"analyzerResult":     strings.TrimPrefix(resultRef, "cas:sha256:"),
	}

	settle := c.gate.EvaluateSettlement(input.Map)
	if input.Map.Settlement != settle.Gate {
		return storage.PublicationCommit{}, fmt.Errorf("late refinement settlement digest/state is not evaluated")
	}
	if activeManifest.SettlementEvaluation.Gate != settle.Gate || !sameStringSlice(activeManifest.SettlementEvaluation.BlockingObligationRefs, settle.BlockingObligationRefs) {
		return storage.PublicationCommit{}, fmt.Errorf("late refinement settlement evaluation changed from the active proof")
	}
	gateResult, gap := c.gate.EvaluateCurrent(PublicationInput{
		Map:                          input.Map,
		Closure:                      input.Closure,
		Delta:                        input.Delta,
		CapturedSnapshot:             input.CapturedSnapshot,
		LiveHeadSnapshot:             input.LiveHeadSnapshot,
		Intent:                       input.Intent,
		RepositoryID:                 input.RepositoryID,
		WorktreeID:                   input.WorktreeID,
		DependencyFingerprint:        input.CapturedSnapshot.DependencyFingerprint,
		QueryHash:                    input.QueryHash,
		ExpectedPreviousGenerationID: input.ExpectedPreviousID,
		GenerationID:                 input.Map.GenerationID,
		ArtifactDigests:              artifactDigests,
		AnalysisRequest:              input.AnalysisRequest,
		AnalysisResult:               input.AnalysisResult,
		CapabilityProfileDigest:      capabilityDigest,
		Metrics:                      input.Metrics,
	})
	if gateResult.Eligibility != "passed" || gap != nil {
		causes := ""
		if gap != nil {
			causes = strings.Join(gap.IntersectedCauses, ", ")
		}
		if causes == "" {
			causes = "one or more current-publication gates failed"
		}
		return storage.PublicationCommit{}, fmt.Errorf("current publication gate rejected late refinement: %s", causes)
	}

	now := time.Now().UTC()
	previousID := input.ExpectedPreviousID
	manifest := &storage.GenerationProofManifest{
		SchemaID:                       GenerationProofSchemaID,
		SchemaVersion:                  SemanticSchemaVersion,
		ProofID:                        fmt.Sprintf("proof-%s", input.Map.GenerationID),
		GenerationID:                   input.Map.GenerationID,
		ComputedBasisID:                input.Map.ComputedBasisID,
		ComputedSnapshotID:             input.CapturedSnapshot.SnapshotID,
		ValidatedAgainstSnapshotID:     input.LiveHeadSnapshot.SnapshotID,
		TaskIntentRevision:             input.Intent.Revision,
		NormalizedQueryHash:            input.QueryHash,
		AnalysisReadSetID:              input.Closure.AnalysisReadSetID,
		CausalObservationClosureID:     input.Closure.ClosureID,
		CausalObservationClosureDigest: input.Closure.ClosureDigest,
		CapabilityProfileDigest:        capabilityDigest,
		WorkspaceEpoch:                 input.CapturedSnapshot.WorkspaceEpoch,
		CurrentPublication:             storage.CurrentPublicationResult{Eligibility: gateResult.Eligibility, SnapshotGate: gateResult.SnapshotGate, ClosureGate: gateResult.ClosureGate, EvidenceGate: gateResult.EvidenceGate, SemanticAtomicityGate: gateResult.SemanticAtomicityGate, TaskRelevanceGate: gateResult.TaskRelevanceGate, ComprehensionGate: gateResult.ComprehensionGate},
		// Preserve the previously evaluated Q3 settlement object byte-for-byte.
		// EvaluateSettlement above is only a predicate check. Its timestamp is a
		// new observation and must never replace the active Q3 evaluation.
		SettlementEvaluation:         storage.SettlementEvaluation{Gate: activeManifest.SettlementEvaluation.Gate, EvaluatedAt: activeManifest.SettlementEvaluation.EvaluatedAt, BlockingObligationRefs: append([]string{}, activeManifest.SettlementEvaluation.BlockingObligationRefs...)},
		ArtifactRefs:                 storage.ArtifactRefs{SemanticMap: mapRef, Projection: projectionRef, AnalysisReadSet: readSetRef, ObservationClosure: closureRef, AnalyzerResult: resultRef},
		ExpectedLiveHeadSnapshotID:   input.LiveHeadSnapshot.SnapshotID,
		ExpectedPreviousGenerationID: &previousID,
		PublishedAt:                  now,
	}
	pointer := &storage.ActivePointer{
		SchemaID:                     ActivePointerSchemaID,
		SchemaVersion:                SemanticSchemaVersion,
		GenerationID:                 input.Map.GenerationID,
		ComputedBasisID:              input.Map.ComputedBasisID,
		ValidatedAgainstSnapshotID:   input.LiveHeadSnapshot.SnapshotID,
		ExpectedLiveHeadSnapshotID:   input.LiveHeadSnapshot.SnapshotID,
		ExpectedPreviousGenerationID: &previousID,
		WorkspaceEpoch:               input.CapturedSnapshot.WorkspaceEpoch,
		TaskIntentRevision:           input.Intent.Revision,
		NormalizedQueryHash:          input.QueryHash,
		FlowCount:                    1,
		RepositoryID:                 input.RepositoryID,
		WorktreeID:                   input.WorktreeID,
		TaskID:                       input.Intent.TaskID,
		PublishedAt:                  now,
	}
	publish := input.Publish
	if publish == nil {
		publish = c.storage.PublishGeneration
	}
	commit, err := publish(storage.PublicationTransaction{Manifest: manifest, Pointer: pointer, Event: append([]byte(nil), input.Event...), Artifacts: artifactBytes, ExpectedLiveHeadSnapshotID: input.LiveHeadSnapshot.SnapshotID, ActualLiveHeadSnapshotID: input.LiveHeadSnapshot.SnapshotID, LiveHeadCommit: input.LiveHeadCommit, ExpectedPreviousGenerationID: previousID})
	if err != nil {
		return storage.PublicationCommit{}, fmt.Errorf("publish late refinement: %w", err)
	}
	return commit, nil
}

type refinementQ3Digests struct {
	Fact       string
	Alignment  string
	Obligation string
	Settlement string
}

func q3Digests(m *SemanticMapIR) (refinementQ3Digests, error) {
	if m == nil {
		return refinementQ3Digests{}, fmt.Errorf("late refinement semantic map is required")
	}
	digest := func(v any) (string, error) {
		b, err := json.Marshal(v)
		if err != nil {
			return "", err
		}
		sum := sha256.Sum256(b)
		return hex.EncodeToString(sum[:]), nil
	}
	fact, err := digest(struct {
		Steps    []SemanticStep     `json:"steps"`
		Edges    []SemanticEdge     `json:"edges"`
		Evidence []SemanticEvidence `json:"evidence"`
	}{m.Steps, m.Edges, m.Evidence})
	if err != nil {
		return refinementQ3Digests{}, fmt.Errorf("digest Q3 facts: %w", err)
	}
	alignment, err := digest(m.RequirementAlignment)
	if err != nil {
		return refinementQ3Digests{}, fmt.Errorf("digest Q3 alignment: %w", err)
	}
	obligation, err := digest(struct {
		Obligations []CriticalObligation `json:"obligations"`
		Unresolved  int                  `json:"unresolved"`
		Conflicting int                  `json:"conflicting"`
	}{m.Quality.CriticalObligations, m.Quality.UnresolvedCriticalCount, m.Quality.ConflictingCriticalCount})
	if err != nil {
		return refinementQ3Digests{}, fmt.Errorf("digest Q3 obligations: %w", err)
	}
	settlement, err := digest(m.Settlement)
	if err != nil {
		return refinementQ3Digests{}, fmt.Errorf("digest Q3 settlement: %w", err)
	}
	return refinementQ3Digests{Fact: fact, Alignment: alignment, Obligation: obligation, Settlement: settlement}, nil
}

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sameStringSlice(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func validateLateRefinementInput(input LateRefinementInput) error {
	if input.Map == nil || input.Projection == nil || input.Closure == nil || input.Delta == nil || input.CapturedSnapshot == nil || input.LiveHeadSnapshot == nil || input.Intent == nil || input.AnalysisRequest == nil || input.AnalysisResult == nil {
		return fmt.Errorf("missing_precondition: complete late refinement input is required")
	}
	if len(input.PreviousMapBytes) == 0 || len(input.Event) == 0 || input.LiveHeadCommit == nil {
		return fmt.Errorf("missing_precondition: prior artifact, event, and live-head authority are required")
	}
	if input.RepositoryID == "" || input.WorktreeID == "" || input.QueryHash == "" {
		return fmt.Errorf("missing_precondition: repository, worktree, and query identities are required")
	}
	if input.Metrics.MeasuredAt.IsZero() || input.Metrics.TraceID == "" || input.Metrics.Activity == "" || input.Metrics.LagMs < 0 || input.Metrics.PendingRevisions < 0 {
		return fmt.Errorf("missing_precondition: measured publication metrics with trace, time, lag, and pending revisions are required")
	}
	if input.Map.Authority == "current" || input.Map.Freshness == "current" {
		return fmt.Errorf("late refinement input cannot claim current authority before publication")
	}
	mapBytes, err := json.Marshal(input.Map)
	if err != nil {
		return fmt.Errorf("marshal late refinement candidate: %w", err)
	}
	if err := contractharness.ValidateSemanticMapIR(mapBytes); err != nil {
		return fmt.Errorf("late refinement candidate schema: %w", err)
	}
	if err := ValidateFlowViewProjectionAgainstMap(input.Projection, input.Map); err != nil {
		return fmt.Errorf("late refinement projection: %w", err)
	}
	requestBytes, err := json.Marshal(input.AnalysisRequest.Params())
	if err != nil {
		return fmt.Errorf("marshal late refinement analyzer request: %w", err)
	}
	if err := contractharness.Validate(rflscvs02.AnalyzerRequestSchemaID, requestBytes); err != nil {
		return fmt.Errorf("late refinement analyzer request schema: %w", err)
	}
	resultBytes, err := json.Marshal(input.AnalysisResult)
	if err != nil {
		return fmt.Errorf("marshal late refinement analyzer result: %w", err)
	}
	if err := contractharness.Validate(rflscvs02.AnalyzerResultSchemaID, resultBytes); err != nil {
		return fmt.Errorf("late refinement analyzer result schema: %w", err)
	}
	if err := rflscvs02.ValidateResult(*input.AnalysisRequest, *input.AnalysisResult); err != nil {
		return fmt.Errorf("late refinement analyzer result: %w", err)
	}
	if input.AnalysisResult.RequestID != input.AnalysisRequest.RequestID || input.AnalysisResult.SnapshotID != input.CapturedSnapshot.SnapshotID || input.AnalysisResult.ComputedBasisID != input.CapturedSnapshot.ComputedBasisID || input.AnalysisResult.WorkspaceEpoch != input.CapturedSnapshot.WorkspaceEpoch {
		return fmt.Errorf("late refinement analyzer snapshot identity mismatch")
	}
	if err := analyzerSnapshotMatchesWorkspace(input.AnalysisRequest.Snapshot, input.CapturedSnapshot); err != nil {
		return fmt.Errorf("late refinement analyzer snapshot is not the captured workspace snapshot: %w", err)
	}
	if input.Closure.CanonicalResult == nil || input.Closure.CanonicalResult.RequestID != input.AnalysisResult.RequestID || input.Closure.ClosureID != input.AnalysisResult.Closure.ClosureID || input.Closure.AnalysisReadSetID != input.AnalysisResult.ReadSet.ReadSetID || input.Closure.ClosureDigest != input.AnalysisResult.Closure.ClosureDigest {
		return fmt.Errorf("late refinement closure is not losslessly bound to canonical analyzer result")
	}
	if input.Map.ComputedBasisID != input.CapturedSnapshot.ComputedBasisID || input.Map.Basis.ComputedWorkspaceSnapshotID != input.CapturedSnapshot.SnapshotID || input.Map.Basis.WorkspaceEpoch != input.CapturedSnapshot.WorkspaceEpoch || input.Map.Basis.RepositoryID != input.RepositoryID || input.Map.Basis.WorktreeID != input.WorktreeID || input.Map.Task.TaskID != input.Intent.TaskID || input.Map.Task.IntentRevision != input.Intent.Revision || input.Map.Task.Mode != input.Intent.Mode {
		return fmt.Errorf("late refinement map identity is not bound to captured snapshot and task")
	}
	if input.Closure.ComputedBasisID != input.Map.ComputedBasisID || input.Closure.TaskIntentRevision != input.Intent.Revision || input.Closure.NormalizedQueryHash != input.QueryHash || input.Closure.ClosureStatus != "closed" {
		return fmt.Errorf("late refinement closure identity or status is invalid")
	}
	if err := validateSemanticClosureAgainstCanonical(input.Closure, input.AnalysisRequest, input.AnalysisResult); err != nil {
		return fmt.Errorf("late refinement closure observations are not losslessly bound: %w", err)
	}
	if input.Delta.FromSnapshotID != input.CapturedSnapshot.SnapshotID || input.Delta.ToSnapshotID != input.LiveHeadSnapshot.SnapshotID {
		return fmt.Errorf("late refinement delta is not bound to captured/live snapshots")
	}
	var event struct {
		SchemaID                   string  `json:"schemaId"`
		SchemaVersion              int     `json:"schemaVersion"`
		EventType                  string  `json:"eventType"`
		ComputedBasisID            *string `json:"computedBasisId"`
		ValidatedAgainstSnapshotID *string `json:"validatedAgainstSnapshotId"`
		GenerationID               *string `json:"generationId"`
	}
	if err := json.Unmarshal(input.Event, &event); err != nil || event.SchemaID != EventEnvelopeSchemaID || event.SchemaVersion != SemanticSchemaVersion || event.EventType != "generation.published" || event.ComputedBasisID == nil || *event.ComputedBasisID != input.Map.ComputedBasisID || event.ValidatedAgainstSnapshotID == nil || *event.ValidatedAgainstSnapshotID != input.LiveHeadSnapshot.SnapshotID || event.GenerationID == nil || *event.GenerationID != input.Map.GenerationID {
		return fmt.Errorf("late refinement event is not bound to canonical identities")
	}
	return contractharness.ValidateEventEnvelopeV2(input.Event)
}

func validateSemanticClosureAgainstCanonical(closure *CausalObservationClosure, request *rflscvs02.AnalyzerRequest, result *rflscvs02.Result) error {
	if closure == nil || request == nil || result == nil {
		return fmt.Errorf("closure, request, and result are required")
	}
	if !sameStringSlice(closure.RequiredObservations, result.Closure.RequiredObservations) || !sameStringSlice(closure.MeasuredObservations, result.Closure.MeasuredObservations) {
		return fmt.Errorf("required or measured observation names differ")
	}
	if closure.PositiveDependencies.ConfigurationFingerprint != request.Snapshot.ConfigurationFingerprint {
		return fmt.Errorf("configuration fingerprint differs")
	}
	if len(closure.PositiveDependencies.DocumentRevisionRefs) != len(result.ReadSet.Documents) {
		return fmt.Errorf("positive document revision set differs")
	}
	for i, doc := range result.ReadSet.Documents {
		want := doc.Path
		if doc.DocumentRevisionID != "" {
			want += "@" + doc.DocumentRevisionID
		}
		if closure.PositiveDependencies.DocumentRevisionRefs[i] != want {
			return fmt.Errorf("positive document revision %q differs", want)
		}
	}
	if len(closure.NegativeObservations) != len(result.Closure.NegativeObservations) {
		return fmt.Errorf("negative observation set differs")
	}
	for i, observation := range result.Closure.NegativeObservations {
		got := closure.NegativeObservations[i]
		wantScope := observation.Path
		if rflscvs02.IsZeroMissMarker(observation) {
			wantScope = ""
		}
		if got.Kind != observation.Kind || got.Selector != observation.Path || got.ScopeRef != wantScope || got.ObservedAgainstIndexRevision != observation.ValueHash {
			return fmt.Errorf("negative observation %q differs", observation.Path)
		}
	}
	if len(closure.MembershipObservations) != len(result.Closure.MembershipObservations) {
		return fmt.Errorf("membership observation set differs")
	}
	for i, observation := range result.Closure.MembershipObservations {
		got := closure.MembershipObservations[i]
		if got.Kind != observation.Kind || got.ContainerRef != observation.Path || got.MembershipDigest != observation.ValueHash {
			return fmt.Errorf("membership observation %q differs", observation.Path)
		}
	}
	if len(closure.DependencyFrontiers) != len(result.Closure.DependencyFrontiers) {
		return fmt.Errorf("dependency frontier set differs")
	}
	for i, observation := range result.Closure.DependencyFrontiers {
		got := closure.DependencyFrontiers[i]
		if got.Direction != observation.Kind || got.RootRef != observation.Path || got.BoundaryRef != observation.Path || got.GraphRevision != observation.ValueHash {
			return fmt.Errorf("dependency frontier %q differs", observation.Path)
		}
	}
	if closure.CapabilityProfile == nil || closure.CapabilityProfile.Adapter != result.Capability.Adapter || !sameStringSlice(closure.CapabilityProfile.Features, result.Capability.Features) {
		return fmt.Errorf("capability profile differs")
	}
	if closure.CoverageBoundary == nil || !sameStringSlice(closure.CoverageBoundary.IncludedSourceRoots, result.Coverage.IncludedSourceRoots) || !sameStringSlice(closure.CoverageBoundary.ExcludedReasons, result.Coverage.ExcludedReasons) {
		return fmt.Errorf("coverage boundary differs")
	}
	return nil
}

func analyzerSnapshotMatchesWorkspace(snapshot rflscvs02.SnapshotInput, workspaceSnapshot *workspace.WorkspaceSnapshot) error {
	if workspaceSnapshot == nil {
		return fmt.Errorf("workspace snapshot is required")
	}
	if snapshot.SnapshotID != workspaceSnapshot.SnapshotID || snapshot.ComputedBasisID != workspaceSnapshot.ComputedBasisID || snapshot.RootTreeID != workspaceSnapshot.RootTreeID || snapshot.DependencyFingerprint != workspaceSnapshot.DependencyFingerprint || snapshot.WorkspaceEpoch != workspaceSnapshot.WorkspaceEpoch {
		return fmt.Errorf("snapshot identity differs")
	}
	if len(snapshot.Documents) != len(workspaceSnapshot.Entries) {
		return fmt.Errorf("document inventory is incomplete")
	}
	seen := make(map[string]bool, len(snapshot.Documents))
	for _, doc := range snapshot.Documents {
		if seen[doc.Path] {
			return fmt.Errorf("duplicate document %q", doc.Path)
		}
		seen[doc.Path] = true
		entry, ok := workspaceSnapshot.Entries[doc.Path]
		if !ok || doc.RevisionID != entry.RevisionID || doc.ContentID != entry.ContentID || doc.DocumentVersion != entry.DocumentVersion || doc.ByteLength != entry.ByteLength || len(doc.Bytes) != entry.ByteLength {
			return fmt.Errorf("document %q identity differs", doc.Path)
		}
		sum := sha256.Sum256(doc.Bytes)
		if hex.EncodeToString(sum[:]) != entry.ContentID {
			return fmt.Errorf("document %q bytes differ", doc.Path)
		}
	}
	return nil
}
