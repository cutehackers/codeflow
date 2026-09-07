package semantic

import (
	"errors"
	"fmt"
	"testing"

	"codeflow/internal/slicing"
)

func prepareComparableDeltaMaps(maps ...*SemanticMapIR) {
	for _, m := range maps {
		if m.ComputedBasisID == "" {
			m.ComputedBasisID = "basis-" + m.MapID
		}
		if m.GenerationID == "" {
			m.GenerationID = "generation-" + m.MapID
		}
		m.SchemaID = "codeflow.semantic-map-ir"
		m.Basis.RepositoryID = "repo-test"
		m.Basis.ComputedWorkspaceSnapshotID = "snapshot-" + m.MapID
		m.Basis.SnapshotTreeID = "tree-test"
		m.Basis.DependencyFingerprint = "deps-test"
		m.Basis.WorkspaceEpoch = 100
		m.ValidatedAgainstSnapshotID = m.Basis.ComputedWorkspaceSnapshotID
		if m.Task.TaskID == "" {
			m.Task.TaskID = "task-delta-test"
		}
		if m.Task.Mode == "" {
			m.Task.Mode = "feature"
		}
		for i := range m.Steps {
			if m.Steps[i].StructuralIdentity == "" {
				m.Steps[i].StructuralIdentity = fmt.Sprintf("%s\x00%s\x00%s\x00%s", m.Steps[i].Anchor.RepoRelativePath, m.Steps[i].Anchor.EnclosingSymbolPath, m.Steps[i].TechnicalName, m.Steps[i].Kind)
			}
		}
	}
}

func TestValidateComparableBasesRequiresSharedLogicalScope(t *testing.T) {
	base := &SemanticMapIR{MapID: "base", GenerationID: "g1", ComputedBasisID: "b1", SchemaVersion: 1, Task: MapTaskContext{TaskID: "task-a", Mode: "feature"}}
	curr := &SemanticMapIR{MapID: "current", GenerationID: "g2", ComputedBasisID: "b2", SchemaVersion: 1, Task: MapTaskContext{TaskID: "task-b", Mode: "feature"}}
	prepareComparableDeltaMaps(base, curr)
	if err := ValidateComparableBases(base, curr); !errors.Is(err, ErrIncomparableBasis) {
		t.Fatalf("expected unrelated task scopes to be incomparable, got %v", err)
	}
	base.Task.TaskID = ""
	curr.Task.TaskID = ""
	if err := ValidateComparableBases(base, curr); !errors.Is(err, ErrMissingPrecondition) {
		t.Fatalf("expected missing task scope to be rejected, got %v", err)
	}
}

func TestComputeSemanticDelta_BasisCompatibility(t *testing.T) {
	// VS05-A2: Missing precondition when baseline or current is nil
	_, err := ComputeSemanticDelta("comp-1", nil, &SemanticMapIR{MapID: "m1"})
	if !errors.Is(err, ErrMissingPrecondition) {
		t.Errorf("expected ErrMissingPrecondition for nil baseline, got %v", err)
	}

	_, err = ComputeSemanticDelta("comp-1", &SemanticMapIR{MapID: "m1"}, nil)
	if !errors.Is(err, ErrMissingPrecondition) {
		t.Errorf("expected ErrMissingPrecondition for nil current, got %v", err)
	}

	// VS05-A1: Incomparable basis when workspace epochs differ
	base := &SemanticMapIR{
		MapID:         "m1",
		SchemaVersion: 1,
		Basis:         MapBasisContext{WorkspaceEpoch: 100},
	}
	curr := &SemanticMapIR{
		MapID:         "m2",
		SchemaVersion: 1,
		Basis:         MapBasisContext{WorkspaceEpoch: 200},
	}
	prepareComparableDeltaMaps(base, curr)
	curr.Basis.WorkspaceEpoch = 200
	_, err = ComputeSemanticDelta("comp-1", base, curr)
	if !errors.Is(err, ErrIncomparableBasis) {
		t.Errorf("expected ErrIncomparableBasis for epoch mismatch, got %v", err)
	}

	// Incomparable basis when schema versions differ
	curr.Basis.WorkspaceEpoch = 100
	curr.SchemaVersion = 2
	prepareComparableDeltaMaps(base, curr)
	curr.SchemaVersion = 2
	_, err = ComputeSemanticDelta("comp-1", base, curr)
	if !errors.Is(err, ErrIncomparableBasis) {
		t.Errorf("expected ErrIncomparableBasis for schema version mismatch, got %v", err)
	}
}

func TestComputeSemanticDelta_AddedChangedRemovedAndEvidence(t *testing.T) {
	base := &SemanticMapIR{
		MapID:           "m1",
		GenerationID:    "gen-1",
		ComputedBasisID: "basis-1",
		SchemaVersion:   1,
		Steps: []SemanticStep{
			{
				StepID:        "step-initial-attempt",
				Name:          "결제 시도",
				TechnicalName: "PaymentService.attempt",
				Rules:         []string{"AC-1"},
				EvidenceRefs:  []string{"ev-base-attempt"},
				Anchor:        slicing.Anchor{RepoRelativePath: "payment.go", ByteRange: [2]int{100, 200}},
			},
			{
				StepID:        "step-old-cleanup",
				Name:          "구버전 정리",
				TechnicalName: "PaymentService.cleanupOld",
				Rules:         []string{"AC-old"},
				EvidenceRefs:  []string{"ev-old"},
				Anchor:        slicing.Anchor{RepoRelativePath: "payment.go", ByteRange: [2]int{500, 600}},
			},
		},
	}

	newBranch := "retry > 0"
	curr := &SemanticMapIR{
		MapID:                      "m2",
		GenerationID:               "gen-2",
		ComputedBasisID:            "basis-2",
		ValidatedAgainstSnapshotID: "snap-head",
		SchemaVersion:              1,
		Steps: []SemanticStep{
			{
				// Changed rule / branch
				StepID:        "step-initial-attempt",
				Name:          "결제 시도",
				TechnicalName: "PaymentService.attempt",
				Rules:         []string{"AC-1", "AC-1-retry-limit-3"},
				Branch:        &newBranch,
				EvidenceRefs:  []string{"ev-base-attempt"},
				Anchor:        slicing.Anchor{RepoRelativePath: "payment.go", ByteRange: [2]int{100, 200}},
			},
			{
				// Added behavior
				StepID:        "step-new-compensation",
				Name:          "보상 트랜잭션",
				TechnicalName: "PaymentService.compensate",
				Rules:         []string{"AC-3"},
				EvidenceRefs:  []string{"ev-compensate"},
				Anchor:        slicing.Anchor{RepoRelativePath: "payment.go", ByteRange: [2]int{800, 900}},
			},
			// step-old-cleanup is removed!
		},
	}

	prepareComparableDeltaMaps(base, curr)
	delta, err := ComputeSemanticDelta("comp-1", base, curr)
	if err != nil {
		t.Fatalf("ComputeSemanticDelta failed: %v", err)
	}

	if delta.Status != "comparable" {
		t.Errorf("delta.Status = %s, want comparable", delta.Status)
	}

	kinds := make(map[string]int)
	for _, ch := range delta.Changes {
		kinds[ch.Kind]++
	}

	if kinds["added_behavior"] != 1 {
		t.Errorf("added_behavior count = %d, want 1", kinds["added_behavior"])
	}
	if kinds["changed_rule"] != 1 {
		t.Errorf("changed_rule count = %d, want 1", kinds["changed_rule"])
	}
	if kinds["removed_behavior"] != 1 {
		t.Errorf("removed_behavior count = %d, want 1", kinds["removed_behavior"])
	}

	if delta.StructuralSummary.AddedStepsCount != 1 ||
		delta.StructuralSummary.ChangedStepsCount != 1 ||
		delta.StructuralSummary.RemovedStepsCount != 1 {
		t.Errorf("unexpected structural summary: %+v", delta.StructuralSummary)
	}
}

func TestComputeSemanticDelta_EvidenceUpdatedAndStructuralMove(t *testing.T) {
	base := &SemanticMapIR{
		MapID:           "m1",
		GenerationID:    "gen-1",
		ComputedBasisID: "basis-1",
		SchemaVersion:   1,
		Steps: []SemanticStep{
			{
				StepID:        "step-1",
				Name:          "주문 검증",
				TechnicalName: "OrderService.validate",
				Anchor:        slicing.Anchor{RepoRelativePath: "order.go", ByteRange: [2]int{200, 300}},
				EvidenceRefs:  []string{"ev-old"},
			},
			{
				StepID:        "step-2",
				Name:          "재고 확인",
				TechnicalName: "InventoryService.check",
				Anchor:        slicing.Anchor{RepoRelativePath: "inventory.go", ByteRange: [2]int{150, 250}},
				EvidenceRefs:  []string{"ev-inv"},
			},
		},
	}

	curr := &SemanticMapIR{
		MapID:           "m2",
		GenerationID:    "gen-2",
		ComputedBasisID: "basis-2",
		SchemaVersion:   1,
		Steps: []SemanticStep{
			{
				// Same rules/behavior, updated evidence
				StepID:        "step-1",
				Name:          "주문 검증",
				TechnicalName: "OrderService.validate",
				Anchor:        slicing.Anchor{RepoRelativePath: "order.go", ByteRange: [2]int{200, 300}},
				EvidenceRefs:  []string{"ev-new-test"},
			},
			{
				// Same behavior & evidence, moved bytes (structural move)
				StepID:        "step-2",
				Name:          "재고 확인",
				TechnicalName: "InventoryService.check",
				Anchor:        slicing.Anchor{RepoRelativePath: "inventory.go", ByteRange: [2]int{250, 350}},
				EvidenceRefs:  []string{"ev-inv"},
			},
		},
	}

	prepareComparableDeltaMaps(base, curr)
	delta, err := ComputeSemanticDelta("comp-2", base, curr)
	if err != nil {
		t.Fatalf("ComputeSemanticDelta failed: %v", err)
	}

	if len(delta.Changes) != 2 {
		t.Fatalf("expected exactly 2 semantic changes (evidence_updated + structural_only), got %d", len(delta.Changes))
	}
	kinds := map[string]bool{}
	for _, change := range delta.Changes {
		kinds[change.Kind] = true
	}
	if !kinds["evidence_updated"] || !kinds["structural_only"] {
		t.Errorf("change kinds = %v, want evidence_updated and structural_only", kinds)
	}
	if delta.StructuralSummary.CollapsedStructuralCount != 1 {
		t.Errorf("collapsed structural count = %d, want 1", delta.StructuralSummary.CollapsedStructuralCount)
	}
}

func TestComputeSemanticDelta_RenameMoveStableIdentity(t *testing.T) {
	// SID-C6: Step ID changes but EnclosingSymbolPath matches
	base := &SemanticMapIR{
		MapID:           "m1",
		GenerationID:    "gen-1",
		ComputedBasisID: "basis-1",
		SchemaVersion:   1,
		Steps: []SemanticStep{
			{
				StepID:        "step-legacy-id-1",
				Name:          "결제 처리",
				TechnicalName: "PaymentGateway.process",
				Anchor:        slicing.Anchor{RepoRelativePath: "gateway.go", EnclosingSymbolPath: "PaymentGateway.process"},
				Rules:         []string{"AC-1"},
			},
		},
	}

	curr := &SemanticMapIR{
		MapID:           "m2",
		GenerationID:    "gen-2",
		ComputedBasisID: "basis-2",
		SchemaVersion:   1,
		Steps: []SemanticStep{
			{
				StepID:        "step-new-uuid-2",
				Name:          "결제 처리",
				TechnicalName: "PaymentGateway.process",
				Anchor:        slicing.Anchor{RepoRelativePath: "gateway.go", EnclosingSymbolPath: "PaymentGateway.process"},
				Rules:         []string{"AC-1"},
			},
		},
	}

	prepareComparableDeltaMaps(base, curr)
	delta, err := ComputeSemanticDelta("comp-3", base, curr)
	if err != nil {
		t.Fatalf("ComputeSemanticDelta failed: %v", err)
	}

	// Should match as same step, not added + removed
	if len(delta.Changes) != 0 {
		t.Errorf("expected 0 semantic changes for renamed/re-identified step, got %d", len(delta.Changes))
	}
}
