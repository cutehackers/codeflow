package semantic

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"testing"

	"codeflow/internal/contractharness"
	"codeflow/internal/rflscvs02"
	"codeflow/internal/slicing"
)

func strictCompileOptions(t *testing.T, payload *slicing.SlicedPayload, basis string, epoch int64) CompileOptions {
	t.Helper()
	data := bytes.Repeat([]byte("x\n"), 600)
	fileSum := sha256.Sum256(data)
	fileHash := hex.EncodeToString(fileSum[:])
	for i := range payload.Steps {
		start, end := payload.Steps[i].Anchor.ByteRange[0], payload.Steps[i].Anchor.ByteRange[1]
		if start < 0 || end < start || end > len(data) {
			start, end = 0, len(data)
			payload.Steps[i].Anchor.ByteRange = [2]int{start, end}
		}
		spanSum := sha256.Sum256(data[start:end])
		payload.Steps[i].Anchor.FileHash = fileHash
		payload.Steps[i].Anchor.SpanHash = hex.EncodeToString(spanSum[:])
	}
	files := map[string]string{"lib/service.dart": string(data)}
	input, err := rflscvs02.SnapshotInputFromContent("snapshot-"+basis, basis, "tree-"+basis, "", "deps-"+basis, epoch, files)
	if err != nil {
		t.Fatal(err)
	}
	result := attachValidatedVS02Result(t, payload, input, []string{"lib"})
	return CompileOptions{ComputedBasisID: basis, WorkspaceEpoch: epoch, ValidatedAgainstSnapshotID: input.SnapshotID, SnapshotID: input.SnapshotID, SnapshotTreeID: input.RootTreeID, RepositoryID: "repo-test", DependencyFingerprint: input.DependencyFingerprint, AdapterVersion: result.AdapterVersion, AnalyzerRevision: result.AnalyzerRevision, AnalysisReadSetID: result.ReadSet.ReadSetID, CausalObservationClosureID: result.Closure.ClosureID, SnapshotFiles: files, SnapshotInput: &input}
}

func attachValidatedVS02Result(t *testing.T, payload *slicing.SlicedPayload, input rflscvs02.SnapshotInput, roots []string) rflscvs02.Result {
	t.Helper()
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	documents := make([]rflscvs02.ReadDocument, 0, len(input.Documents))
	for _, document := range input.Documents {
		documents = append(documents, rflscvs02.ReadDocument{
			Path: document.Path, DocumentRevisionID: document.RevisionID, ContentID: document.ContentID,
			ContentHash: document.ContentID, DocumentVersion: document.DocumentVersion, ByteLength: document.ByteLength,
		})
	}
	negative := rflscvs02.Observation{Kind: "negative_lookup", Path: "missing.config", Detail: "not found", Measured: true}
	membership := rflscvs02.Observation{Kind: "membership", Path: ".", ValueHash: "membership-test", Measured: true}
	frontier := rflscvs02.Observation{Kind: "dependency_frontier", Path: "dependencies.lock", ValueHash: "frontier-test", Measured: true}
	readSet := rflscvs02.AnalysisReadSet{
		SchemaID: rflscvs02.ReadSetSchemaID, SchemaVersion: rflscvs02.SchemaVersion,
		ReadSetID: "readset-test-001", ComputedBasisID: input.ComputedBasisID, WorkspaceEpoch: input.WorkspaceEpoch,
		Documents: documents, NegativeObservations: []rflscvs02.Observation{negative}, MembershipObservations: []rflscvs02.Observation{membership}, DependencyFrontiers: []rflscvs02.Observation{frontier},
	}
	closure := rflscvs02.ObservationClosure{
		SchemaID: rflscvs02.ClosureSchemaID, SchemaVersion: rflscvs02.SchemaVersion,
		ClosureID: "closure-test-001", AnalysisReadSetID: readSet.ReadSetID, ComputedBasisID: input.ComputedBasisID, WorkspaceEpoch: input.WorkspaceEpoch,
		Status: "closed", NegativeObservations: []rflscvs02.Observation{negative}, MembershipObservations: []rflscvs02.Observation{membership}, DependencyFrontiers: []rflscvs02.Observation{frontier},
		RequiredObservations: []string{"negative_lookup", "membership", "dependency_frontier"}, MeasuredObservations: []string{"negative_lookup", "membership", "dependency_frontier"},
	}
	result := rflscvs02.Result{
		SchemaID: rflscvs02.AnalyzerResultSchemaID, SchemaVersion: rflscvs02.SchemaVersion, RequestID: "request-test-001", Operation: "slice",
		AdapterVersion: "test-adapter/1", AnalyzerRevision: "test-analyzer/1", WorkspaceEpoch: input.WorkspaceEpoch,
		ComputedBasisID: input.ComputedBasisID, SnapshotID: input.SnapshotID, SnapshotTreeDigest: input.RootTreeID, DependencyFingerprint: input.DependencyFingerprint,
		ReadSet: readSet, Closure: closure, Capability: rflscvs02.CapabilityProfile{Adapter: "test", AdapterVersion: "test-adapter/1", AnalyzerRevision: "test-analyzer/1", Features: []string{"snapshot_bytes"}},
		Coverage: rflscvs02.Coverage{IncludedSourceRoots: append([]string(nil), roots...), Measured: true}, Diagnostics: []rflscvs02.Diagnostic{}, Payload: payloadBytes,
	}
	if err := payload.BindValidatedResult(result); err != nil {
		t.Fatal(err)
	}
	return result
}

func makeStep(ordinal int, name string, kind string) slicing.SliceStep {
	h := fmt.Sprintf("%064x", ordinal)
	return slicing.SliceStep{
		Ordinal:     ordinal,
		Description: name,
		Kind:        kind,
		SymbolPath:  fmt.Sprintf("Service.step%d", ordinal),
		Anchor: slicing.Anchor{
			RepoRelativePath:        "lib/service.dart",
			ByteRange:               [2]int{ordinal * 10, ordinal*10 + 20},
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
				StepOrdinal:      func() *int { v := 1; return &v }(),
			},
			{
				Kind:             "unknown_edge",
				ToSymbolPath:     "ExternalGateway.charge",
				ResolutionStatus: "unresolved",
				StepOrdinal:      func() *int { v := 2; return &v }(),
			},
		},
	}

	mapIR, proj, err := CompileDeterministicFeatureMap(target, intent, sliceShort, strictCompileOptions(t, sliceShort, "basis-snap-100", 1))
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

	mapLong, projLong, err := CompileDeterministicFeatureMap(target, intent, sliceLong, strictCompileOptions(t, sliceLong, "basis-snap-100", 1))
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

func TestFeatureModeSettlementGate_ObligationsAndEvaluation(t *testing.T) {
	target := &ResolvedTarget{
		EntrySymbolPath: "AuthController.login",
		FlowID:          "flow-login-001",
		Title:           "사용자 로그인 흐름",
	}

	steps := []slicing.SliceStep{
		makeStep(1, "Receive credentials", "user_action"),
		makeStep(2, "Check password hash", "decision"),
		makeStep(3, "Emit auth session token", "result"),
	}

	// Case 1: Fully resolved flow without unknowns -> Settlement: passed
	cleanSlice := &slicing.SlicedPayload{
		EntrySymbolPath: target.EntrySymbolPath,
		Steps:           steps,
		Edges: []slicing.SliceEdge{
			{
				Kind:             "resolved_cross_file",
				ToSymbolPath:     "Service.step2",
				ResolutionStatus: "resolved",
				StepOrdinal:      func() *int { v := 1; return &v }(),
			},
		},
	}

	mapClean, _, err := CompileDeterministicFeatureMap(target, nil, cleanSlice, strictCompileOptions(t, cleanSlice, "basis-clean-001", 1))
	if err != nil {
		t.Fatalf("Compile clean failed: %v", err)
	}

	if mapClean.Settlement != "pending" {
		t.Errorf("expected settlement 'pending' for candidate flow without VS-03 proof, got %q", mapClean.Settlement)
	}
	if mapClean.Quality.UnresolvedCriticalCount != 0 {
		t.Errorf("expected 0 unresolved critical count, got %d", mapClean.Quality.UnresolvedCriticalCount)
	}

	// Verify all 5 kinds are present in critical obligations
	kinds := make(map[string]bool)
	for _, ob := range mapClean.Quality.CriticalObligations {
		kinds[ob.Kind] = true
	}
	for _, expectedKind := range []string{"entry", "causal_chain", "critical_branch", "result", "no_critical_unknown"} {
		if !kinds[expectedKind] {
			t.Errorf("missing expected critical obligation kind: %s", expectedKind)
		}
	}

	// Case 2: Flow with unknown edge -> Settlement: pending
	unknownSlice := &slicing.SlicedPayload{
		EntrySymbolPath: target.EntrySymbolPath,
		Steps:           steps,
		Edges: []slicing.SliceEdge{
			{
				Kind:             "unknown_edge",
				ToSymbolPath:     "ExternalAuth.oauth",
				ResolutionStatus: "unresolved",
				StepOrdinal:      func() *int { v := 2; return &v }(),
			},
		},
	}

	mapUnknown, _, err := CompileDeterministicFeatureMap(target, nil, unknownSlice, strictCompileOptions(t, unknownSlice, "basis-unknown-001", 1))
	if err != nil {
		t.Fatalf("Compile unknown failed: %v", err)
	}

	if mapUnknown.Settlement != "pending" {
		t.Errorf("expected settlement 'pending' when unknown edge exists, got %q", mapUnknown.Settlement)
	}
	if mapUnknown.Quality.UnresolvedCriticalCount <= 0 {
		t.Errorf("expected unresolvedCriticalCount > 0 when unknown exists, got %d", mapUnknown.Quality.UnresolvedCriticalCount)
	}
}
