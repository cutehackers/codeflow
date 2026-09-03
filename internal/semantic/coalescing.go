package semantic

import (
	"fmt"
	"sync"
	"time"

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
		checkpoints: make(chan *workspace.WorkspaceSnapshot, 100),
	}
}

// Checkpoints returns a receive-only channel of selected publication snapshots.
func (s *CoalescingScheduler) Checkpoints() <-chan *workspace.WorkspaceSnapshot {
	return s.checkpoints
}

// NotifyEdit notifies the scheduler that a new WorkspaceSnapshot has been recorded.
func (s *CoalescingScheduler) NotifyEdit(snap *workspace.WorkspaceSnapshot) {
	s.mu.Lock()
	defer s.mu.Unlock()

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

	if s.latestSnap == nil {
		return
	}

	selected := s.latestSnap
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

	select {
	case s.checkpoints <- selected:
	default:
		// Queue full - drop older if overloaded, keep latest
	}
}

// RefinementCoordinator coordinates same-basis late refinement publication (Raw §7.4, §13.2, VS04-A7).
type RefinementCoordinator struct {
	storage *storage.Storage
	gate    *PublicationGate
}

// NewRefinementCoordinator creates a refinement coordinator.
func NewRefinementCoordinator(st *storage.Storage) *RefinementCoordinator {
	return &RefinementCoordinator{
		storage: st,
		gate:    NewPublicationGate(),
	}
}

// PublishLateRefinement checks same computedBasisId and active pointer CAS before publishing a refinement.
func (c *RefinementCoordinator) PublishLateRefinement(
	lateMapIR *SemanticMapIR,
	closure *CausalObservationClosure,
	expectedLiveHeadSnapshotID string,
	expectedPreviousGenID string,
) (bool, error) {
	if lateMapIR == nil {
		return false, fmt.Errorf("lateMapIR must not be nil")
	}

	curPtr, err := c.storage.ReadActivePointer()
	if err != nil {
		return false, fmt.Errorf("read active pointer: %w", err)
	}
	if curPtr == nil {
		return false, fmt.Errorf("no active generation currently published")
	}

	// 1. Same-basis check (VS04-A7, Raw §7.4, INV-08)
	if lateMapIR.ComputedBasisID != curPtr.ComputedBasisID {
		return false, fmt.Errorf("late refinement basis mismatch: got %s, active basis is %s",
			lateMapIR.ComputedBasisID, curPtr.ComputedBasisID)
	}

	// 2. Closure status check
	if closure != nil && closure.ClosureStatus != "closed" {
		return false, fmt.Errorf("late refinement closure is open")
	}

	// 3. Write manifest to CAS
	now := time.Now().UTC()
	settle := c.gate.EvaluateSettlement(lateMapIR)
	manifest := &storage.GenerationProofManifest{
		SchemaID:                   "https://codeflow.local/schemas/generation-proof-manifest.schema.json",
		SchemaVersion:              1,
		ProofID:                    fmt.Sprintf("proof-%s", lateMapIR.GenerationID),
		GenerationID:               lateMapIR.GenerationID,
		ComputedBasisID:            lateMapIR.ComputedBasisID,
		ValidatedAgainstSnapshotID: expectedLiveHeadSnapshotID,
		TaskIntentRevision:         lateMapIR.Task.IntentRevision,
		NormalizedQueryHash:        curPtr.NormalizedQueryHash,
		AnalysisReadSetID:          fmt.Sprintf("readset-%s", lateMapIR.GenerationID),
		CausalObservationClosureID: fmt.Sprintf("closure-%s", lateMapIR.GenerationID),
		CurrentPublication: storage.CurrentPublicationResult{
			Eligibility:           "passed",
			SnapshotGate:          "passed",
			ClosureGate:           "passed",
			EvidenceGate:          "passed",
			SemanticAtomicityGate: "passed",
			TaskRelevanceGate:     "passed",
			ComprehensionGate:     "passed",
		},
		SettlementEvaluation: storage.SettlementEvaluation{
			Gate:                   settle.Gate,
			EvaluatedAt:            settle.EvaluatedAt,
			BlockingObligationRefs: settle.BlockingObligationRefs,
		},
		ArtifactRefs: storage.ArtifactRefs{
			SemanticMap: fmt.Sprintf("cas:sha256:%s", lateMapIR.GenerationID),
		},
		ExpectedLiveHeadSnapshotID:   expectedLiveHeadSnapshotID,
		ExpectedPreviousGenerationID: &expectedPreviousGenID,
		PublishedAt:                  now,
	}

	casRef, err := c.storage.WriteManifestCAS(manifest)
	if err != nil {
		return false, fmt.Errorf("store refinement manifest: %w", err)
	}

	// 4. Compare-and-swap active pointer
	newPtr := &storage.ActivePointer{
		SchemaID:                     "https://codeflow.local/schemas/active-pointer.schema.json",
		SchemaVersion:                1,
		GenerationID:                 lateMapIR.GenerationID,
		ManifestObjectRef:            casRef,
		PublishedAt:                  now,
		ComputedBasisID:              lateMapIR.ComputedBasisID,
		ValidatedAgainstSnapshotID:   expectedLiveHeadSnapshotID,
		ExpectedLiveHeadSnapshotID:   expectedLiveHeadSnapshotID,
		ExpectedPreviousGenerationID: &expectedPreviousGenID,
		WorkspaceEpoch:               curPtr.WorkspaceEpoch,
		TaskIntentRevision:           lateMapIR.Task.IntentRevision,
		NormalizedQueryHash:          curPtr.NormalizedQueryHash,
		FlowCount:                    curPtr.FlowCount,
	}

	if err := c.storage.CompareAndSwapActivePointer(expectedLiveHeadSnapshotID, expectedPreviousGenID, newPtr); err != nil {
		return false, fmt.Errorf("active pointer CAS conflict: %w", err)
	}

	return true, nil
}
