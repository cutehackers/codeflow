package semantic

import (
	"encoding/json"
	"fmt"
	"testing"

	"codeflow/internal/contractharness"
	"codeflow/internal/slicing"
)

func makeStep(ordinal int, name string, kind string) slicing.SliceStep {
	h := fmt.Sprintf("%064x", ordinal)
	return slicing.SliceStep{
		Ordinal:     ordinal,
		Description: name,
		Kind:        kind,
		SymbolPath:  fmt.Sprintf("Service.step%d", ordinal),
		Anchor: slicing.Anchor{
			RepoRelativePath:        "lib/service.dart",
			ByteRange:               [2]int{ordinal * 10, ordinal * 10 + 20},
			FileHash:                h,
			SpanHash:                h,
			EnclosingSymbolPath:     fmt.Sprintf("Service.step%d", ordinal),
			CanonicalAstFingerprint: h,
		},
	}
}

// TestVS02A3_A4_A6_DeterministicCompiler tests criteria VS02-A3, VS02-A4, VS02-A6:
// 1. Model-free deterministic compilation
// 2. Whole flow preserved in SemanticMapIR
// 3. Soft budget (7-15) and D32 preservation in FlowViewProjection
// 4. Unknown/unresolved preservation
func TestVS02A3_A4_A6_DeterministicCompiler(t *testing.T) {
	intent, err := NormalizeTaskIntent("결제 요청 흐름", IntentOptions{Mode: "feature"})
	if err != nil {
		t.Fatal(err)
	}

	target := &ResolvedTarget{
		EntrySymbolPath: "PaymentController.submit",
		FlowID:          "flow-payment-001",
		Title:           "결제 요청 흐름",
	}

	// Case 1: Short flow (< 7 steps)
	shortSteps := []slicing.SliceStep{
		makeStep(1, "Receive payment click", "user_action"),
		makeStep(2, "Validate card details", "guard"),
		makeStep(3, "Persist transaction result", "mutation"),
	}
	sliceShort := &slicing.SlicedPayload{
		EntrySymbolPath: target.EntrySymbolPath,
		Steps:           shortSteps,
		Edges: []slicing.SliceEdge{
			{
				Kind:             "resolved_cross_file",
				ToSymbolPath:     "PaymentService.validate",
				ResolutionStatus: "resolved",
			},
			{
				Kind:             "unknown_edge",
				ToSymbolPath:     "ExternalGateway.charge",
				ResolutionStatus: "unresolved",
			},
		},
	}

	mapIR, proj, err := CompileDeterministicFeatureMap(target, intent, sliceShort, CompileOptions{
		ComputedBasisID: "basis-snap-100",
		WorkspaceEpoch:  1,
	})
	if err != nil {
		t.Fatalf("CompileDeterministicFeatureMap failed: %v", err)
	}

	// Verify SemanticMapIR preserves all steps
	if len(mapIR.Steps) != 3 {
		t.Errorf("expected 3 steps in SemanticMapIR, got %d", len(mapIR.Steps))
	}
	// Verify unresolved edge generated an unknown item (VS02-A6)
	if len(mapIR.Unknowns) == 0 {
		t.Errorf("expected at least 1 unknown for unresolved edge")
	}
	// Verify quality stage is deterministic baseline Q1 or Q2 without model
	if mapIR.Quality.Stage != "Q1" && mapIR.Quality.Stage != "Q2" {
		t.Errorf("expected quality stage Q1 or Q2, got %q", mapIR.Quality.Stage)
	}
	if mapIR.EnrichmentStatus != "not_requested" {
		t.Errorf("expected enrichmentStatus 'not_requested', got %q", mapIR.EnrichmentStatus)
	}

	// Verify FlowViewProjection soft budget: short flow (< 7) includes all steps, NO fake padding
	if len(proj.VisibleStepRefs) != 3 {
		t.Errorf("expected 3 visible steps for short flow, got %d", len(proj.VisibleStepRefs))
	}
	if len(proj.FoldedSubflows) != 0 {
		t.Errorf("expected 0 folded subflows for short flow, got %d", len(proj.FoldedSubflows))
	}

	// Schema validation for both
	mapData, err := json.Marshal(mapIR)
	if err != nil {
		t.Fatal(err)
	}
	if err := contractharness.ValidateSemanticMapIR(mapData); err != nil {
		t.Fatalf("SemanticMapIR validation failed: %v", err)
	}

	projData, err := json.Marshal(proj)
	if err != nil {
		t.Fatal(err)
	}
	if err := contractharness.ValidateFlowViewProjection(projData); err != nil {
		t.Fatalf("FlowViewProjection validation failed: %v", err)
	}

	// Case 2: Long flow (> 15 steps)
	var longSteps []slicing.SliceStep
	longSteps = append(longSteps, makeStep(1, "Entry submit", "user_action")) // entry (critical)
	for i := 2; i <= 19; i++ {
		longSteps = append(longSteps, makeStep(i, fmt.Sprintf("Intermediate task %d", i), "call")) // non-critical
	}
	longSteps = append(longSteps, makeStep(20, "Final terminal result", "mutation")) // result (critical)

	sliceLong := &slicing.SlicedPayload{
		EntrySymbolPath: target.EntrySymbolPath,
		Steps:           longSteps,
	}

	mapLong, projLong, err := CompileDeterministicFeatureMap(target, intent, sliceLong, CompileOptions{
		ComputedBasisID: "basis-snap-100",
		WorkspaceEpoch:  1,
	})
	if err != nil {
		t.Fatalf("CompileDeterministicFeatureMap long failed: %v", err)
	}

	// SemanticMapIR preserves ALL 20 steps
	if len(mapLong.Steps) != 20 {
		t.Errorf("expected 20 steps in SemanticMapIR, got %d", len(mapLong.Steps))
	}

	// FlowViewProjection folds non-critical subflows into soft budget
	if len(projLong.VisibleStepRefs) > 15 {
		t.Errorf("expected projection visible steps <= 15 after folding non-critical subflow, got %d", len(projLong.VisibleStepRefs))
	}
	if len(projLong.FoldedSubflows) == 0 {
		t.Errorf("expected folded subflows for flow with 20 steps")
	}

	// Critical D32 rule: ALL preservedStepRefs must be in visibleStepRefs
	visMap := make(map[string]bool)
	for _, v := range projLong.VisibleStepRefs {
		visMap[v] = true
	}
	for _, p := range projLong.PreservedStepRefs {
		if !visMap[p] {
			t.Errorf("critical preserved step %q was omitted from visibleStepRefs!", p)
		}
	}
}
