package semantic

import (
	"encoding/json"
	"strings"
	"testing"

	"codeflow/internal/fusion"
	"codeflow/internal/slicing"
)

func TestComputeChangeImpact_MissingPreconditionAndAuthority(t *testing.T) {
	if _, err := ComputeChangeImpact(ImpactTarget{}, nil, ImpactOptions{}); err == nil || !strings.Contains(err.Error(), "missing_precondition") {
		t.Fatalf("missing target must fail without a default: %v", err)
	}
	fixture := impactTestMap()
	opts := impactTestOptions(fixture)
	opts.Freshness = "current"
	opts.CurrentProofVerified = false
	if _, err := ComputeChangeImpact(ImpactTarget{SymbolID: "PaymentService.process"}, fixture, opts); err == nil || !strings.Contains(err.Error(), "invalid_authority") {
		t.Fatalf("unverified current authority must fail: %v", err)
	}
	opts = impactTestOptions(fixture)
	opts.RelationKinds = nil
	if _, err := ComputeChangeImpact(ImpactTarget{SymbolID: "PaymentService.process"}, fixture, opts); err == nil || !strings.Contains(err.Error(), "relationKinds") {
		t.Fatalf("implicit relation scope must fail: %v", err)
	}
	broken := impactTestMap()
	broken.Edges[1].ToSymbolPath = "Wrong.target"
	if _, err := ComputeChangeImpact(ImpactTarget{SymbolID: "PaymentService.process"}, broken, impactTestOptions(broken)); err == nil || !strings.Contains(err.Error(), "invalid_graph") {
		t.Fatalf("edge target identity mismatch must fail: %v", err)
	}
}

func TestComputeChangeImpact_DirectedAndBoundedTraversal(t *testing.T) {
	fixture := impactTestMap()
	fixture.Edges[1].ResolutionStatus = "resolved"
	fixture.Edges[1].Kind = "resolved_cross_file"
	result, err := ComputeChangeImpact(ImpactTarget{SymbolID: "PaymentService.process"}, fixture, impactTestOptions(fixture))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.DirectImpact.Callers) != 1 || result.DirectImpact.Callers[0].SymbolPath != "OrderController.checkout" || !equalStrings(result.DirectImpact.Callers[0].Path, []string{"PaymentService.process", "OrderController.checkout"}) {
		t.Fatalf("reverse caller direction is wrong: %+v", result.DirectImpact.Callers)
	}
	if result.DirectImpact.Callers[0].RelationKind != "calls" {
		t.Fatalf("adapter edge kind was not normalized to the public relation contract: %+v", result.DirectImpact.Callers[0])
	}
	if result.DirectImpact.Callers[0].SymbolPath == "PaymentService.process" {
		t.Fatal("callee was reported as its own caller")
	}
	if len(result.IndirectImpact.Callers) != 1 || result.IndirectImpact.Callers[0].SymbolPath != "WebRoute.postOrder" || result.IndirectImpact.Callers[0].Depth != 2 || !equalStrings(result.IndirectImpact.Callers[0].Path, []string{"PaymentService.process", "OrderController.checkout", "WebRoute.postOrder"}) {
		t.Fatalf("bounded indirect caller path is wrong: %+v", result.IndirectImpact.Callers)
	}
	if len(result.DirectImpact.StateMutations) != 1 || result.DirectImpact.StateMutations[0].Depth != 1 || !equalStrings(result.DirectImpact.StateMutations[0].Path, []string{"PaymentService.process", "PaymentState.markPaid"}) {
		t.Fatalf("direct state mutation missing: %+v", result.DirectImpact.StateMutations)
	}
	if len(result.IndirectImpact.ExternalEffects) != 1 || result.IndirectImpact.ExternalEffects[0].Target != "Stripe.charge" || result.IndirectImpact.ExternalEffects[0].Depth != 2 || !equalStrings(result.IndirectImpact.ExternalEffects[0].Path, []string{"PaymentService.process", "PaymentState.markPaid", "PaymentGateway.charge"}) {
		t.Fatalf("indirect external effect missing: %+v", result.IndirectImpact.ExternalEffects)
	}
	if len(result.IndirectImpact.Tests) != 1 || result.IndirectImpact.Tests[0].Depth != 3 || !equalStrings(result.IndirectImpact.Tests[0].Path, []string{"PaymentService.process", "PaymentState.markPaid", "PaymentGateway.charge", "PaymentFlowTest.success"}) {
		t.Fatalf("indirect test impact missing: %+v", result.IndirectImpact.Tests)
	}
	if len(result.Evidence) < 5 {
		t.Fatalf("impact relations did not retain same-basis Evidence: %+v", result.Evidence)
	}
	for _, evidence := range result.Evidence {
		if evidence.ComputedBasisID != fixture.ComputedBasisID || evidence.SnapshotID != fixture.ValidatedAgainstSnapshotID || evidence.ValidationStatus != "verified" {
			t.Fatalf("impact evidence drifted from generation: %+v", evidence)
		}
	}
	if !result.IndirectImpact.CompletedWithinCoverage || result.AdditionalExplorationAvailable || len(result.Frontiers) != 0 {
		t.Fatalf("complete bounded traversal reported a false frontier: %+v", result)
	}
}

func TestComputeChangeImpact_ReportsDynamicAndBudgetFrontiers(t *testing.T) {
	fixture := impactTestMap()
	fixture.Edges[0].ResolutionStatus = "dynamic"
	result, err := ComputeChangeImpact(ImpactTarget{SymbolID: "OrderController.checkout"}, fixture, impactTestOptions(fixture))
	if err != nil {
		t.Fatal(err)
	}
	if !result.AdditionalExplorationAvailable || !containsImpactFrontier(result.Frontiers, "unresolved_dynamic_caller") {
		t.Fatalf("dynamic relation was not exposed as expandable unknown: %+v", result.Frontiers)
	}

	fixture = impactTestMap()
	opts := impactTestOptions(fixture)
	opts.MaxDepth = 1
	result, err = ComputeChangeImpact(ImpactTarget{SymbolID: "PaymentService.process"}, fixture, opts)
	if err != nil {
		t.Fatal(err)
	}
	if !result.AdditionalExplorationAvailable || !containsImpactFrontier(result.Frontiers, "budget_exhausted") {
		t.Fatalf("depth boundary did not report expansion availability: %+v", result.Frontiers)
	}

	fixture = impactTestMap()
	fixture.Unknowns = append(fixture.Unknowns, fusion.Unknown{Subject: "Plugin.dispatch", Reason: "dynamic dispatch target is unresolved"})
	result, err = ComputeChangeImpact(ImpactTarget{SymbolID: "PaymentService.process"}, fixture, impactTestOptions(fixture))
	if err != nil {
		t.Fatal(err)
	}
	if !containsImpactFrontier(result.Frontiers, "unresolved_dynamic_caller") {
		t.Fatalf("task-scoped unresolved boundary disappeared from impact output: %+v", result.Frontiers)
	}
}

func TestComputeChangeImpact_EmptyMeansCompleteOnlyInsideDeclaredCoverage(t *testing.T) {
	fixture := impactTestMap()
	fixture.Steps = fixture.Steps[2:3]
	fixture.Edges = []SemanticEdge{}
	fixture.Evidence = fixture.Evidence[2:3]
	result, err := ComputeChangeImpact(ImpactTarget{SymbolID: "PaymentService.process"}, fixture, impactTestOptions(fixture))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.IndirectImpact.Callers) != 0 || !result.IndirectImpact.CompletedWithinCoverage || result.CoverageBoundary == nil || len(result.CoverageBoundary.IncludedSourceRoots) == 0 {
		t.Fatalf("empty bounded result omitted its coverage meaning: %+v", result)
	}

	opts := impactTestOptions(fixture)
	opts.CoverageComplete = false
	result, err = ComputeChangeImpact(ImpactTarget{SymbolID: "PaymentService.process"}, fixture, opts)
	if err != nil {
		t.Fatal(err)
	}
	if result.IndirectImpact.CompletedWithinCoverage || !containsImpactFrontier(result.Frontiers, "coverage_boundary") {
		t.Fatalf("incomplete coverage was presented as complete absence: %+v", result)
	}
}

func TestComputeChangeImpact_InvalidEvidenceBecomesUnknownAndSecretsAreRedacted(t *testing.T) {
	fixture := impactTestMap()
	fixture.Evidence[1].ValidationStatus = "stale"
	result, err := ComputeChangeImpact(ImpactTarget{SymbolID: "PaymentService.process"}, fixture, impactTestOptions(fixture))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.DirectImpact.Callers) != 0 || !containsImpactFrontier(result.Frontiers, "invalid_evidence") {
		t.Fatalf("invalid Evidence supported an impact claim: callers=%+v frontiers=%+v", result.DirectImpact.Callers, result.Frontiers)
	}

	fixture = impactTestMap()
	fixture.Evidence[1].Anchor.RepoRelativePath = "/tmp/outside.go"
	result, err = ComputeChangeImpact(ImpactTarget{SymbolID: "PaymentService.process"}, fixture, impactTestOptions(fixture))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.DirectImpact.Callers) != 0 || !containsImpactFrontier(result.Frontiers, "invalid_evidence") {
		t.Fatalf("unsafe Evidence path supported an impact claim: %+v", result)
	}

	fixture = impactTestMap()
	secretEffect := "api:token=super-secret-value"
	fixture.Steps[3].SideEffect = &secretEffect
	result, err = ComputeChangeImpact(ImpactTarget{SymbolID: "PaymentService.process"}, fixture, impactTestOptions(fixture))
	if err != nil {
		t.Fatal(err)
	}
	raw := strings.ToLower(impactJSON(t, result))
	if strings.Contains(raw, "super-secret-value") || !strings.Contains(raw, "redacted") {
		t.Fatalf("impact egress leaked a secret: %s", raw)
	}
}

func TestComputeChangeImpact_CapabilityFiltersEveryClaimKind(t *testing.T) {
	fixture := impactTestMap()
	opts := impactTestOptions(fixture)
	opts.SupportedRelationKinds = []string{"calls"}
	opts.CoverageComplete = false
	result, err := ComputeChangeImpact(ImpactTarget{SymbolID: "PaymentService.process"}, fixture, opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.DirectImpact.StateMutations) != 0 || len(result.IndirectImpact.ExternalEffects) != 0 || len(result.IndirectImpact.Tests) != 0 || len(result.DirectImpact.RelatedFlows) != 0 {
		t.Fatalf("unsupported intrinsic impact escaped capability gating: %+v", result)
	}
	if !containsImpactFrontier(result.Frontiers, "unsupported_capability") || !result.AdditionalExplorationAvailable {
		t.Fatalf("unsupported impact was not exposed as an expandable frontier: %+v", result.Frontiers)
	}

	supported := SupportedImpactRelationKinds([]string{"snapshot_bytes", "relation:calls", "impact_relation:related_test", "external_effect"}, []string{"calls", "related_test", "external_effect", "state_mutation"})
	if strings.Join(supported, ",") != "calls,external_effect,related_test" {
		t.Fatalf("capability intersection is wrong: %v", supported)
	}
	terminalOnly := SupportedImpactRelationKinds([]string{"relation:calls", "external_effect"}, []string{"external_effect"})
	if strings.Join(terminalOnly, ",") != "calls,external_effect" {
		t.Fatalf("terminal query lost required calls capability: %v", terminalOnly)
	}
	if !ImpactCoverageComplete(true, nil, "closed", []string{"external_effect"}, terminalOnly) {
		t.Fatal("measured calls connectivity and terminal capability should close terminal-only coverage")
	}
	if ImpactCoverageComplete(true, nil, "closed", []string{"external_effect"}, []string{"external_effect"}) {
		t.Fatal("terminal-only coverage closed without required calls connectivity")
	}
	if ImpactCoverageComplete(true, nil, "closed", []string{"calls", "state_mutation"}, supported) {
		t.Fatal("coverage was marked complete without every requested relation capability")
	}
}

func TestComputeChangeImpact_TerminalOnlyQueriesTraverseCallsWithoutEmittingCallers(t *testing.T) {
	tests := []struct {
		name       string
		relation   string
		wantDepth  int
		wantPath   []string
		claimCount func(*ChangeImpactGraph) int
		unexpected func(*ChangeImpactGraph) int
	}{
		{
			name:       "external effect",
			relation:   "external_effect",
			wantDepth:  2,
			wantPath:   []string{"PaymentService.process", "PaymentState.markPaid", "PaymentGateway.charge"},
			claimCount: func(graph *ChangeImpactGraph) int { return len(graph.IndirectImpact.ExternalEffects) },
			unexpected: func(graph *ChangeImpactGraph) int {
				return len(graph.DirectImpact.Callers) + len(graph.IndirectImpact.Callers) + len(graph.DirectImpact.StateMutations) + len(graph.IndirectImpact.Tests)
			},
		},
		{
			name:       "related test",
			relation:   "related_test",
			wantDepth:  3,
			wantPath:   []string{"PaymentService.process", "PaymentState.markPaid", "PaymentGateway.charge", "PaymentFlowTest.success"},
			claimCount: func(graph *ChangeImpactGraph) int { return len(graph.IndirectImpact.Tests) },
			unexpected: func(graph *ChangeImpactGraph) int {
				return len(graph.DirectImpact.Callers) + len(graph.IndirectImpact.Callers) + len(graph.DirectImpact.ExternalEffects) + len(graph.IndirectImpact.StateMutations)
			},
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := impactTestMap()
			options := impactTestOptions(fixture)
			options.RelationKinds = []string{testCase.relation}
			options.SupportedRelationKinds = []string{"calls", testCase.relation}
			result, err := ComputeChangeImpact(ImpactTarget{SymbolID: "PaymentService.process"}, fixture, options)
			if err != nil {
				t.Fatal(err)
			}
			if testCase.claimCount(result) != 1 || testCase.unexpected(result) != 0 {
				t.Fatalf("terminal-only query emitted the wrong categories: %+v", result)
			}
			var path []string
			var depth int
			if testCase.relation == "external_effect" {
				path = result.IndirectImpact.ExternalEffects[0].Path
				depth = result.IndirectImpact.ExternalEffects[0].Depth
			} else {
				path = result.IndirectImpact.Tests[0].Path
				depth = result.IndirectImpact.Tests[0].Depth
			}
			if depth != testCase.wantDepth || !equalStrings(path, testCase.wantPath) {
				t.Fatalf("terminal-only query lost call connectivity: depth=%d path=%v", depth, path)
			}
			if !result.IndirectImpact.CompletedWithinCoverage || len(result.Frontiers) != 0 {
				t.Fatalf("verified terminal-only traversal was not complete: %+v", result)
			}
		})
	}
}

func TestComputeChangeImpact_TerminalOnlyQueryRequiresCallsCapability(t *testing.T) {
	fixture := impactTestMap()
	options := impactTestOptions(fixture)
	options.RelationKinds = []string{"external_effect"}
	options.SupportedRelationKinds = []string{"external_effect"}
	result, err := ComputeChangeImpact(ImpactTarget{SymbolID: "PaymentService.process"}, fixture, options)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.DirectImpact.ExternalEffects) != 0 || len(result.IndirectImpact.ExternalEffects) != 0 {
		t.Fatalf("effect claim crossed an unsupported calls capability: %+v", result)
	}
	if result.IndirectImpact.CompletedWithinCoverage || !result.AdditionalExplorationAvailable || !containsImpactFrontier(result.Frontiers, "unsupported_capability") {
		t.Fatalf("unsupported calls capability was not exposed as an incomplete frontier: %+v", result)
	}
}

func TestComputeChangeImpact_NodeBudgetCountsDistinctGraphNodesAcrossDirections(t *testing.T) {
	fixture := impactTestMap()
	fixture.Edges = append(fixture.Edges, SemanticEdge{FromStepID: "target", ToStepID: "caller", ToSymbolPath: "OrderController.checkout", Kind: "calls", ResolutionStatus: "verified"})
	opts := impactTestOptions(fixture)
	opts.MaxNodes = 1
	result, err := ComputeChangeImpact(ImpactTarget{SymbolID: "PaymentService.process"}, fixture, opts)
	if err != nil {
		t.Fatal(err)
	}
	if result.IndirectImpact.TotalNodeCount != 1 {
		t.Fatalf("the same graph node was counted more than once across reverse/forward traversal: %+v", result.IndirectImpact)
	}
	if !containsImpactFrontier(result.Frontiers, "budget_exhausted") {
		t.Fatalf("distinct-node budget did not expose its frontier: %+v", result.Frontiers)
	}
}

func TestComputeChangeImpact_GraphIdentityBindsNormalizedQuery(t *testing.T) {
	fixture := impactTestMap()
	opts := impactTestOptions(fixture)
	first, err := ComputeChangeImpact(ImpactTarget{SymbolID: "PaymentService.process"}, fixture, opts)
	if err != nil {
		t.Fatal(err)
	}
	reordered := opts
	reordered.RelationKinds = append([]string{}, opts.RelationKinds...)
	for left, right := 0, len(reordered.RelationKinds)-1; left < right; left, right = left+1, right-1 {
		reordered.RelationKinds[left], reordered.RelationKinds[right] = reordered.RelationKinds[right], reordered.RelationKinds[left]
	}
	second, err := ComputeChangeImpact(ImpactTarget{SymbolID: "PaymentService.process"}, fixture, reordered)
	if err != nil {
		t.Fatal(err)
	}
	if first.ImpactGraphID != second.ImpactGraphID {
		t.Fatalf("equivalent relation order changed graph identity: %q != %q", first.ImpactGraphID, second.ImpactGraphID)
	}
	reordered.MaxDepth = 2
	third, err := ComputeChangeImpact(ImpactTarget{SymbolID: "PaymentService.process"}, fixture, reordered)
	if err != nil {
		t.Fatal(err)
	}
	if first.ImpactGraphID == third.ImpactGraphID {
		t.Fatal("different traversal budget reused the same immutable impact graph identity")
	}
}

func impactTestMap() *SemanticMapIR {
	basis := strings.Repeat("b", 64)
	snapshot := "snapshot-impact"
	state := &fusion.StateDelta{Before: "cart", After: "paid"}
	effect := "api:Stripe.charge"
	steps := []SemanticStep{
		impactTestStep("route", "WebRoute.postOrder", "src/route.go", "ev-route"),
		impactTestStep("caller", "OrderController.checkout", "src/controller.go", "ev-caller"),
		impactTestStep("target", "PaymentService.process", "src/payment.go", "ev-target"),
		impactTestStep("mutation", "PaymentState.markPaid", "src/state.go", "ev-mutation"),
		impactTestStep("effect", "PaymentGateway.charge", "src/gateway.go", "ev-effect"),
		impactTestStep("test", "PaymentFlowTest.success", "test/payment_test.go", "ev-test"),
	}
	steps[3].StateDelta = state
	steps[4].SideEffect = &effect
	steps[5].Rules = []string{"test:PaymentFlowTest.success"}
	evidence := make([]SemanticEvidence, 0, len(steps))
	for i, step := range steps {
		evidence = append(evidence, SemanticEvidence{EvidenceID: step.EvidenceRefs[0], Kind: "source", SourceAuthority: "code", ComputedBasisID: basis, SnapshotID: snapshot, ValidationStatus: "verified", Anchor: slicing.Anchor{RepoRelativePath: step.Anchor.RepoRelativePath, EnclosingSymbolPath: step.TechnicalName, ByteRange: [2]int{i * 10, i*10 + 5}}, ByteRange: [2]int{i * 10, i*10 + 5}, LineRange: [2]int{i + 1, i + 1}})
	}
	return &SemanticMapIR{
		SchemaID: SemanticMapSchemaID, SchemaVersion: SemanticSchemaVersion,
		MapID: "map-impact", GenerationID: "generation-impact", ComputedBasisID: basis, ValidatedAgainstSnapshotID: snapshot,
		PublicationKind: "checkpoint", Freshness: "historical", Settlement: "pending", EnrichmentStatus: "available", Authority: "candidate",
		Task: MapTaskContext{TaskID: "task-impact", IntentRevision: 1, Mode: "feature"}, Basis: MapBasisContext{ComputedBasisID: basis, ComputedWorkspaceSnapshotID: snapshot},
		Summary: MapSummary{Requested: "trace payment impact", Current: "candidate"}, Quality: MapQuality{Stage: "Q3"},
		Steps: steps,
		Edges: []SemanticEdge{
			{FromStepID: "route", ToStepID: "caller", ToSymbolPath: "OrderController.checkout", Kind: "calls", ResolutionStatus: "verified"},
			{FromStepID: "caller", ToStepID: "target", ToSymbolPath: "PaymentService.process", Kind: "calls", ResolutionStatus: "verified"},
			{FromStepID: "target", ToStepID: "mutation", ToSymbolPath: "PaymentState.markPaid", Kind: "calls", ResolutionStatus: "verified"},
			{FromStepID: "mutation", ToStepID: "effect", ToSymbolPath: "PaymentGateway.charge", Kind: "calls", ResolutionStatus: "verified"},
			{FromStepID: "effect", ToStepID: "test", ToSymbolPath: "PaymentFlowTest.success", Kind: "calls", ResolutionStatus: "verified"},
		},
		Evidence: evidence, Unknowns: []fusion.Unknown{}, Coverage: &CoverageBoundary{IncludedSourceRoots: []string{"src", "test"}, ExcludedReasons: []string{}},
	}
}

func impactTestStep(id, symbol, path, evidence string) SemanticStep {
	return SemanticStep{StepID: id, Name: symbol, TechnicalName: symbol, Anchor: slicing.Anchor{RepoRelativePath: path, EnclosingSymbolPath: symbol, ByteRange: [2]int{0, 1}}, EvidenceRefs: []string{evidence}}
}

func impactTestOptions(mapIR *SemanticMapIR) ImpactOptions {
	relations := []string{"calls", "state_mutation", "external_effect", "related_test", "related_flow"}
	return ImpactOptions{MaxDepth: 3, MaxNodes: 50, ComputedBasisID: mapIR.ComputedBasisID, GenerationID: mapIR.GenerationID, ValidatedAgainstSnapshotID: mapIR.ValidatedAgainstSnapshotID, Freshness: "historical", RelationKinds: relations, SupportedRelationKinds: relations, CoverageComplete: true}
}

func containsImpactFrontier(frontiers []ImpactFrontier, kind string) bool {
	for _, frontier := range frontiers {
		if frontier.BoundaryType == kind {
			return true
		}
	}
	return false
}

func impactJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func equalStrings(left, right []string) bool {
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
