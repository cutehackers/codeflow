package semantic_test

import (
	"testing"

	"codeflow/internal/semantic"
	"codeflow/internal/slicing"
	"codeflow/internal/workspace"
)

func createTestMapIR(stage string, obligations []semantic.CriticalObligation, unresolved, conflicting int) *semantic.SemanticMapIR {
	return &semantic.SemanticMapIR{
		SchemaID:        "https://codeflow.local/schemas/semantic-map-ir.schema.json",
		SchemaVersion:   1,
		MapID:           "map-1",
		GenerationID:    "gen-1",
		ComputedBasisID: "basis-1",
		PublicationKind: "initial",
		Freshness:       "current",
		Settlement:      "pending",
		Quality: semantic.MapQuality{
			Stage:                    stage,
			CriticalObligations:      obligations,
			UnresolvedCriticalCount:  unresolved,
			ConflictingCriticalCount: conflicting,
		},
		Task: semantic.MapTaskContext{
			TaskID:         "task-1",
			IntentRevision: 1,
			Mode:           "feature",
		},
		Summary: semantic.MapSummary{
			Requested: "user requested feature",
			Current:   "current flow summary",
		},
		Steps: []semantic.SemanticStep{
			{
				StepID:        "step-1",
				Ordinal:       1,
				Name:          "Entry Step",
				TechnicalName: "lib/entry.dart#entry",
				Layer:         "presentation",
				Kind:          "action",
				Anchor: slicing.Anchor{
					RepoRelativePath: "lib/entry.dart",
					ByteRange:        [2]int{10, 50},
				},
				EvidenceRefs: []string{"ev-1"},
			},
		},
	}
}

func TestCurrentPublicationGate_AllPass(t *testing.T) {
	mapIR := createTestMapIR("Q2", nil, 0, 0)
	snap := &workspace.WorkspaceSnapshot{
		SnapshotID:      "snap-1",
		ComputedBasisID: "basis-1",
	}
	closure := &semantic.CausalObservationClosure{
		ClosureID:          "closure-1",
		ComputedBasisID:    "basis-1",
		ClosureStatus:      "closed",
		PositiveDependencies: semantic.PositiveDependencies{
			DocumentRevisionRefs: []string{"lib/entry.dart"},
		},
	}
	delta := &workspace.WorkspaceDelta{
		FromSnapshotID: "basis-1",
		ToSnapshotID:   "snap-1",
		ChangedPaths:   []string{}, // no changes
	}

	gate := semantic.NewPublicationGate()
	res, gap := gate.Evaluate(mapIR, closure, delta, snap, nil)
	if res.Eligibility != "passed" {
		t.Fatalf("expected eligibility passed, got %s", res.Eligibility)
	}
	if res.ClosureGate != "passed" || res.SnapshotGate != "passed" || res.EvidenceGate != "passed" {
		t.Errorf("expected all subgates passed, got %+v", res)
	}
	if gap != nil {
		t.Errorf("expected nil verified gap on pass, got %+v", gap)
	}
}

func TestCurrentPublicationGate_ClosureOpen(t *testing.T) {
	mapIR := createTestMapIR("Q2", nil, 0, 0)
	snap := &workspace.WorkspaceSnapshot{
		SnapshotID:      "snap-1",
		ComputedBasisID: "basis-1",
	}
	closure := &semantic.CausalObservationClosure{
		ClosureID:         "closure-1",
		ComputedBasisID:   "basis-1",
		ClosureStatus:     "open",
		IncompleteReasons: []string{"missing dynamic lookup"},
	}
	delta := &workspace.WorkspaceDelta{
		FromSnapshotID: "basis-1",
		ToSnapshotID:   "snap-1",
	}

	gate := semantic.NewPublicationGate()
	res, gap := gate.Evaluate(mapIR, closure, delta, snap, nil)
	if res.Eligibility != "rejected" {
		t.Fatalf("expected eligibility rejected for open closure, got %s", res.Eligibility)
	}
	if res.ClosureGate != "failed" {
		t.Errorf("expected closure gate failed, got %s", res.ClosureGate)
	}
	if gap == nil || gap.Freshness != "last_verified" {
		t.Errorf("expected last_verified gap on open closure, got %+v", gap)
	}
}

func TestCurrentPublicationGate_DeltaIntersectsClosure(t *testing.T) {
	mapIR := createTestMapIR("Q2", nil, 0, 0)
	snap := &workspace.WorkspaceSnapshot{
		SnapshotID:      "snap-1",
		ComputedBasisID: "basis-1",
	}

	// 1. Intersects positive dependencies
	closure1 := &semantic.CausalObservationClosure{
		ClosureStatus: "closed",
		PositiveDependencies: semantic.PositiveDependencies{
			DocumentRevisionRefs: []string{"lib/entry.dart"},
		},
	}
	delta1 := &workspace.WorkspaceDelta{
		ChangedPaths: []string{"lib/entry.dart"},
	}

	gate := semantic.NewPublicationGate()
	res1, gap1 := gate.Evaluate(mapIR, closure1, delta1, snap, nil)
	if res1.Eligibility != "rejected" || res1.ClosureGate != "failed" {
		t.Errorf("expected rejection when delta touches positive dependency")
	}
	if gap1 == nil || len(gap1.AffectedScope) == 0 || gap1.AffectedScope[0] != "lib/entry.dart" {
		t.Errorf("expected affected scope in gap: %+v", gap1)
	}

	// 2. Intersects negative observations (e.g. caller-of:symbol in package src/payment)
	closure2 := &semantic.CausalObservationClosure{
		ClosureStatus: "closed",
		NegativeObservations: []semantic.NegativeObservation{
			{
				Kind:     "relation_absent",
				Selector: "caller-of:symbol-retry-policy",
				ScopeRef: "src/payment",
			},
		},
	}
	delta2 := &workspace.WorkspaceDelta{
		AddedPaths:   []string{"src/payment/new_caller.dart"},
		ChangedPaths: []string{"src/payment/new_caller.dart"},
	}

	res2, gap2 := gate.Evaluate(mapIR, closure2, delta2, snap, nil)
	if res2.Eligibility != "rejected" || res2.ClosureGate != "failed" {
		t.Errorf("expected rejection when delta satisfies negative observation")
	}
	if gap2 == nil {
		t.Errorf("expected gap report on negative observation intersection")
	}

	// 3. Intersects membership observations
	closure3 := &semantic.CausalObservationClosure{
		ClosureStatus: "closed",
		MembershipObservations: []semantic.MembershipObservation{
			{
				Kind:         "package_sources",
				ContainerRef: "src/payment",
			},
		},
	}
	delta3 := &workspace.WorkspaceDelta{
		AddedPaths:   []string{"src/payment/added.dart"},
		ChangedPaths: []string{"src/payment/added.dart"},
	}
	res3, _ := gate.Evaluate(mapIR, closure3, delta3, snap, nil)
	if res3.Eligibility != "rejected" || res3.ClosureGate != "failed" {
		t.Errorf("expected rejection when delta changes membership observation")
	}

	// 4. Intersects dependency frontiers
	closure4 := &semantic.CausalObservationClosure{
		ClosureStatus: "closed",
		DependencyFrontiers: []semantic.DependencyFrontier{
			{
				Direction:   "callers",
				RootRef:     "symbol-retry",
				BoundaryRef: "src/payment",
			},
		},
	}
	delta4 := &workspace.WorkspaceDelta{
		ChangedPaths: []string{"src/payment/retry.dart"},
	}
	res4, _ := gate.Evaluate(mapIR, closure4, delta4, snap, nil)
	if res4.Eligibility != "rejected" || res4.ClosureGate != "failed" {
		t.Errorf("expected rejection when delta crosses dependency frontier")
	}
}

func TestSettlementGate_Evaluation(t *testing.T) {
	gate := semantic.NewPublicationGate()

	// 1. Q1 or Q2 cannot pass settlement regardless of verified obligations (VS04-A5)
	obligationsQ2 := []semantic.CriticalObligation{
		{ObligationID: "ob-1", Kind: "entry", Required: true, Status: "verified"},
		{ObligationID: "ob-2", Kind: "result", Required: true, Status: "verified"},
	}
	mapIRQ2 := createTestMapIR("Q2", obligationsQ2, 0, 0)
	settleQ2 := gate.EvaluateSettlement(mapIRQ2)
	if settleQ2.Gate != "pending" {
		t.Errorf("expected Q2 settlement to be pending, got %s", settleQ2.Gate)
	}

	// 2. Q3 with all required verified and 0 unresolved/conflicting passes settlement (VS04-A6)
	mapIRQ3Pass := createTestMapIR("Q3", obligationsQ2, 0, 0)
	settleQ3Pass := gate.EvaluateSettlement(mapIRQ3Pass)
	if settleQ3Pass.Gate != "passed" {
		t.Errorf("expected Q3 with all verified obligations to pass, got %s", settleQ3Pass.Gate)
	}
	if len(settleQ3Pass.BlockingObligationRefs) != 0 {
		t.Errorf("expected empty blocking obligations on passed, got %v", settleQ3Pass.BlockingObligationRefs)
	}

	// 3. Q3 with missing/unresolved required obligation fails settlement (VS04-A6)
	obligationsQ3Fail := []semantic.CriticalObligation{
		{ObligationID: "ob-1", Kind: "entry", Required: true, Status: "verified"},
		{ObligationID: "ob-2", Kind: "result", Required: true, Status: "unknown"},
	}
	mapIRQ3Fail := createTestMapIR("Q3", obligationsQ3Fail, 1, 0)
	settleQ3Fail := gate.EvaluateSettlement(mapIRQ3Fail)
	if settleQ3Fail.Gate != "failed" {
		t.Errorf("expected Q3 with unknown required obligation to fail, got %s", settleQ3Fail.Gate)
	}
	if len(settleQ3Fail.BlockingObligationRefs) != 1 || settleQ3Fail.BlockingObligationRefs[0] != "ob-2" {
		t.Errorf("expected blocking obligation ob-2, got %v", settleQ3Fail.BlockingObligationRefs)
	}
}
