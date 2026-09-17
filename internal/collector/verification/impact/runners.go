// Package impact contains the shared executable acceptance runners for the
// impact projection contract. Each runner invokes the semantic impact
// production seam and returns immutable graph references for the contract
// harness. The registry and the named tests use these same functions.
package impact

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"codeflow/internal/collector/contractharness"
	"codeflow/internal/collector/fusion"
	"codeflow/internal/collector/slicing"
	"codeflow/internal/curator/semantic"
)

const (
	implementationPackage = "codeflow/internal/collector/verification/impact"
	basisID               = "basis-impact"
	snapshotID            = "snapshot-impact"
	treeID                = "tree-impact"
)

// Evidence is the shared runner result consumed by the VS-05 evidence
// registry. The execution package, binary and execution ID are added only by
// the registry after the runner has returned successfully.
type Evidence struct {
	Criterion             string
	ImplementationTestID  string
	ImplementationPackage string
	SnapshotTreeDigest    string
	ObjectRefs            []string
}

var implementationTestIDs = map[string]string{
	"VS05-A1": "codeflow/internal/collector/verification/impact.TestRFLSCR2VS05_A01",
	"VS05-A2": "codeflow/internal/collector/verification/impact.TestRFLSCR2VS05_A02",
	"VS05-A3": "codeflow/internal/collector/verification/impact.TestRFLSCR2VS05_A03",
	"VS05-A4": "codeflow/internal/collector/verification/impact.TestRFLSCR2VS05_A04",
	"VS05-A5": "codeflow/internal/collector/verification/impact.TestRFLSCR2VS05_A05",
	"VS05-A6": "codeflow/internal/collector/verification/impact.TestRFLSCR2VS05_A06",
	"VS05-A7": "codeflow/internal/collector/verification/impact.TestRFLSCR2VS05_A07",
}

type fixture struct {
	Map *semantic.SemanticMapIR
}

// NewFixture is intentionally exported so registry-level tests can prove
// that output evidence is tied to one immutable graph basis without reaching
// into runner internals.
func NewFixture() *semantic.SemanticMapIR {
	state := &fusion.StateDelta{Before: "cart", After: "paid"}
	effect := "api:Stripe.charge"
	steps := []semantic.SemanticStep{
		impactStep("route", "WebRoute.postOrder", "src/route.go", "ev-route"),
		impactStep("caller", "OrderController.checkout", "src/controller.go", "ev-caller"),
		impactStep("target", "PaymentService.process", "src/payment.go", "ev-target"),
		impactStep("mutation", "PaymentState.markPaid", "src/state.go", "ev-mutation"),
		impactStep("effect", "PaymentGateway.charge", "src/gateway.go", "ev-effect"),
		impactStep("test", "PaymentFlowTest.success", "test/payment_test.go", "ev-test"),
	}
	steps[3].StateDelta = state
	steps[4].SideEffect = &effect
	steps[5].Rules = []string{"test:PaymentFlowTest.success"}
	evidence := make([]semantic.SemanticEvidence, 0, len(steps))
	for i, step := range steps {
		evidence = append(evidence, semantic.SemanticEvidence{
			EvidenceID: step.EvidenceRefs[0], Kind: "source", SourceAuthority: "code",
			ComputedBasisID: basisID, SnapshotID: snapshotID, ValidationStatus: "verified",
			RedactionStatus: "clean", DocumentRevisionID: "revision-" + step.StepID,
			Anchor:    slicing.Anchor{RepoRelativePath: step.Anchor.RepoRelativePath, EnclosingSymbolPath: step.TechnicalName, ByteRange: [2]int{i * 10, i*10 + 5}},
			ByteRange: [2]int{i * 10, i*10 + 5}, LineRange: [2]int{i + 1, i + 1},
		})
	}
	return &semantic.SemanticMapIR{
		SchemaID: SemanticMapSchemaID(), SchemaVersion: SemanticMapSchemaVersion(),
		MapID: "map-impact", GenerationID: "generation-impact", ComputedBasisID: basisID,
		ValidatedAgainstSnapshotID: snapshotID, PublicationKind: "checkpoint", Freshness: "historical",
		Settlement: "pending", EnrichmentStatus: "available", Authority: "candidate",
		Task:    semantic.MapTaskContext{TaskID: "task-impact", IntentRevision: 1, Mode: "feature"},
		Basis:   semantic.MapBasisContext{ComputedBasisID: basisID, ComputedWorkspaceSnapshotID: snapshotID, SnapshotTreeID: treeID, RepositoryID: "repository-impact", WorktreeID: "worktree-impact", WorkspaceEpoch: 1},
		Summary: semantic.MapSummary{Requested: "trace payment impact", Current: "candidate"},
		Steps:   steps,
		Edges: []semantic.SemanticEdge{
			{FromStepID: "route", ToStepID: "caller", ToSymbolPath: "OrderController.checkout", Kind: "calls", ResolutionStatus: "verified"},
			{FromStepID: "caller", ToStepID: "target", ToSymbolPath: "PaymentService.process", Kind: "calls", ResolutionStatus: "verified"},
			{FromStepID: "target", ToStepID: "mutation", ToSymbolPath: "PaymentState.markPaid", Kind: "calls", ResolutionStatus: "verified"},
			{FromStepID: "mutation", ToStepID: "effect", ToSymbolPath: "PaymentGateway.charge", Kind: "calls", ResolutionStatus: "verified"},
			{FromStepID: "effect", ToStepID: "test", ToSymbolPath: "PaymentFlowTest.success", Kind: "calls", ResolutionStatus: "verified"},
		},
		Evidence: evidence, Unknowns: []fusion.Unknown{},
		Coverage: &semantic.CoverageBoundary{IncludedSourceRoots: []string{"src", "test"}, ExcludedReasons: []string{}},
	}
}

// SemanticMapSchemaID and SemanticMapSchemaVersion are tiny named accessors
// used only to keep the fixture readable while retaining the canonical values
// owned by the semantic package.
func SemanticMapSchemaID() string   { return semantic.SemanticMapSchemaID }
func SemanticMapSchemaVersion() int { return semantic.SemanticSchemaVersion }

func impactStep(id, symbol, file, evidence string) semantic.SemanticStep {
	return semantic.SemanticStep{
		StepID: id, StructuralIdentity: file + "\x00" + symbol, Name: symbol, TechnicalName: symbol,
		Anchor: slicing.Anchor{RepoRelativePath: file, EnclosingSymbolPath: symbol, ByteRange: [2]int{0, 1}}, EvidenceRefs: []string{evidence},
	}
}

func impactOptions(mapIR *semantic.SemanticMapIR) semantic.ImpactOptions {
	relations := []string{"calls", "state_mutation", "external_effect", "related_test", "related_flow"}
	return semantic.ImpactOptions{
		MaxDepth: 3, MaxNodes: 50, ComputedBasisID: mapIR.ComputedBasisID, GenerationID: mapIR.GenerationID,
		ValidatedAgainstSnapshotID: mapIR.ValidatedAgainstSnapshotID, Freshness: "historical", RelationKinds: relations,
		SupportedRelationKinds: relations, CoverageComplete: true,
	}
}

func evidenceFor(criterion string, graph *semantic.ChangeImpactGraph, refs ...string) Evidence {
	objectRefs := []string{"snapshot:" + graph.ValidatedAgainstSnapshotID, "tree:" + treeID, "basis:" + graph.ComputedBasisID, "impactGraph:" + graph.ImpactGraphID}
	for _, evidence := range graph.Evidence {
		if evidence.EvidenceID != "" {
			objectRefs = append(objectRefs, "evidence:"+evidence.EvidenceID)
		}
	}
	objectRefs = append(objectRefs, refs...)
	return Evidence{Criterion: criterion, ImplementationTestID: implementationTestIDs[criterion], ImplementationPackage: implementationPackage, SnapshotTreeDigest: treeID, ObjectRefs: objectRefs}
}

func runImpact(t *testing.T, target semantic.ImpactTarget, mapIR *semantic.SemanticMapIR, opts semantic.ImpactOptions) *semantic.ChangeImpactGraph {
	t.Helper()
	graph, err := semantic.ComputeChangeImpact(target, mapIR, opts)
	if err != nil {
		t.Fatalf("production impact seam failed: %v", err)
	}
	if graph.SchemaID != semantic.ChangeImpactGraphSchemaID || graph.SchemaVersion != 2 {
		t.Fatalf("impact seam returned non-canonical graph identity: %+v", graph)
	}
	data, err := json.Marshal(graph)
	if err != nil {
		t.Fatalf("marshal production impact graph: %v", err)
	}
	if err := contractharness.ValidateChangeImpactGraphV2(data); err != nil {
		t.Fatalf("production impact graph failed its contract validator: %v", err)
	}
	return graph
}

func requireFixture(t *testing.T, criterion string) *semantic.SemanticMapIR {
	t.Helper()
	fixture := NewFixture()
	if fixture == nil || fixture.Coverage == nil || len(fixture.Steps) == 0 {
		t.Fatalf("%s fixture is incomplete", criterion)
	}
	if err := ValidateFixtureIdentity(fixture); err != nil {
		t.Fatalf("%s fixture identity is incomplete: %v", criterion, err)
	}
	return fixture
}

// RunA01 proves missing target and unverified current authority fail without
// selecting a default symbol or current basis.
func RunA01(t *testing.T) Evidence {
	mapIR := requireFixture(t, "VS05-A1")
	if _, err := semantic.ComputeChangeImpact(semantic.ImpactTarget{}, mapIR, impactOptions(mapIR)); err == nil || !strings.Contains(err.Error(), "missing_precondition") {
		t.Fatalf("missing target did not fail closed: %v", err)
	}
	options := impactOptions(mapIR)
	options.Freshness = "current"
	if _, err := semantic.ComputeChangeImpact(semantic.ImpactTarget{SymbolID: "PaymentService.process"}, mapIR, options); err == nil || !strings.Contains(err.Error(), "invalid_authority") {
		t.Fatalf("unverified current authority did not fail closed: %v", err)
	}
	graph := runImpact(t, semantic.ImpactTarget{SymbolID: "PaymentService.process"}, mapIR, impactOptions(mapIR))
	return evidenceFor("VS05-A1", graph, "precondition:missing_target", "authority:historical_candidate")
}

// RunA02 proves the source of an incoming edge is the caller and the changed
// callee is never reported as its own caller.
func RunA02(t *testing.T) Evidence {
	mapIR := requireFixture(t, "VS05-A2")
	graph := runImpact(t, semantic.ImpactTarget{SymbolID: "PaymentService.process"}, mapIR, impactOptions(mapIR))
	if len(graph.DirectImpact.Callers) != 1 || graph.DirectImpact.Callers[0].SymbolPath != "OrderController.checkout" || !samePath(graph.DirectImpact.Callers[0].Path, "PaymentService.process", "OrderController.checkout") {
		t.Fatalf("reverse caller direction is incorrect: %+v", graph.DirectImpact.Callers)
	}
	if graph.DirectImpact.Callers[0].SymbolPath == "PaymentService.process" {
		t.Fatal("callee was reported as its own caller")
	}
	return evidenceFor("VS05-A2", graph, "edge:caller-to-callee")
}

// RunA03 proves state, external effect, and related test claims carry
// same-basis verified Evidence through the actual projection.
func RunA03(t *testing.T) Evidence {
	mapIR := requireFixture(t, "VS05-A3")
	graph := runImpact(t, semantic.ImpactTarget{SymbolID: "PaymentService.process"}, mapIR, impactOptions(mapIR))
	if len(graph.DirectImpact.StateMutations) != 1 || len(graph.IndirectImpact.ExternalEffects) != 1 || len(graph.IndirectImpact.Tests) != 1 {
		t.Fatalf("forward impact relations are incomplete: %+v", graph)
	}
	if len(graph.Evidence) < 5 {
		t.Fatalf("same-basis evidence was not retained: %d", len(graph.Evidence))
	}
	for _, evidence := range graph.Evidence {
		if evidence.ComputedBasisID != mapIR.ComputedBasisID || evidence.SnapshotID != mapIR.ValidatedAgainstSnapshotID || evidence.ValidationStatus != "verified" {
			t.Fatalf("evidence identity drifted: %+v", evidence)
		}
	}
	return evidenceFor("VS05-A3", graph, "evidence:same-basis", "relation:state-effect-test")
}

// RunA04 proves bounded indirect paths include each traversed step and depth.
func RunA04(t *testing.T) Evidence {
	mapIR := requireFixture(t, "VS05-A4")
	graph := runImpact(t, semantic.ImpactTarget{SymbolID: "PaymentService.process"}, mapIR, impactOptions(mapIR))
	if len(graph.IndirectImpact.Callers) != 1 || graph.IndirectImpact.Callers[0].Depth != 2 || !samePath(graph.IndirectImpact.Callers[0].Path, "PaymentService.process", "OrderController.checkout", "WebRoute.postOrder") {
		t.Fatalf("indirect caller path is incomplete: %+v", graph.IndirectImpact.Callers)
	}
	if len(graph.IndirectImpact.ExternalEffects) != 1 || graph.IndirectImpact.ExternalEffects[0].Depth != 2 || !samePath(graph.IndirectImpact.ExternalEffects[0].Path, "PaymentService.process", "PaymentState.markPaid", "PaymentGateway.charge") {
		t.Fatalf("indirect effect path is incomplete: %+v", graph.IndirectImpact.ExternalEffects)
	}
	return evidenceFor("VS05-A4", graph, "path:bounded-reverse", "path:bounded-forward")
}

func samePath(got []string, want ...string) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range got {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}

// RunA05 proves dynamic and budget limits become explicit expandable
// frontiers rather than empty or falsely complete impact.
func RunA05(t *testing.T) Evidence {
	mapIR := requireFixture(t, "VS05-A5")
	mapIR.Edges[1].ResolutionStatus = "dynamic"
	graph := runImpact(t, semantic.ImpactTarget{SymbolID: "OrderController.checkout"}, mapIR, impactOptions(mapIR))
	if !graph.AdditionalExplorationAvailable || !hasFrontier(graph, "unresolved_dynamic_caller") {
		t.Fatalf("dynamic relation did not produce expandable frontier: %+v", graph.Frontiers)
	}
	mapIR = requireFixture(t, "VS05-A5-budget")
	options := impactOptions(mapIR)
	options.MaxDepth = 1
	graph = runImpact(t, semantic.ImpactTarget{SymbolID: "PaymentService.process"}, mapIR, options)
	if !graph.AdditionalExplorationAvailable || !hasFrontier(graph, "budget_exhausted") {
		t.Fatalf("depth boundary did not produce expandable frontier: %+v", graph.Frontiers)
	}
	return evidenceFor("VS05-A5", graph, "frontier:dynamic", "frontier:budget")
}

// RunA06 proves an empty indirect set means complete only within a declared
// coverage boundary and does not claim repository-wide absence.
func RunA06(t *testing.T) Evidence {
	mapIR := requireFixture(t, "VS05-A6")
	mapIR.Steps = mapIR.Steps[2:3]
	mapIR.Edges = []semantic.SemanticEdge{}
	mapIR.Evidence = mapIR.Evidence[2:3]
	graph := runImpact(t, semantic.ImpactTarget{SymbolID: "PaymentService.process"}, mapIR, impactOptions(mapIR))
	if len(graph.IndirectImpact.Callers) != 0 || !graph.IndirectImpact.CompletedWithinCoverage || graph.AdditionalExplorationAvailable || graph.CoverageBoundary == nil || len(graph.CoverageBoundary.IncludedSourceRoots) == 0 {
		t.Fatalf("empty bounded impact did not preserve coverage meaning: %+v", graph)
	}
	options := impactOptions(mapIR)
	options.CoverageComplete = false
	graph = runImpact(t, semantic.ImpactTarget{SymbolID: "PaymentService.process"}, mapIR, options)
	if graph.IndirectImpact.CompletedWithinCoverage || !hasFrontier(graph, "coverage_boundary") {
		t.Fatalf("incomplete coverage was presented as complete: %+v", graph)
	}
	return evidenceFor("VS05-A6", graph, "coverage:bounded-empty", "coverage:incomplete")
}

// RunA07 proves invalid Evidence removes the affected claim and every output
// is recursively redacted before it leaves the semantic seam.
func RunA07(t *testing.T) Evidence {
	mapIR := requireFixture(t, "VS05-A7")
	mapIR.Evidence[1].ValidationStatus = "stale"
	// The invalid-evidence branch must itself remain a canonical graph. The
	// production seam must preserve the unknown frontier, omit the rejected
	// Evidence ref, and never promote the stale ref into a claim.
	graph := runImpact(t, semantic.ImpactTarget{SymbolID: "PaymentService.process"}, mapIR, impactOptions(mapIR))
	if len(graph.DirectImpact.Callers) != 0 || !hasFrontier(graph, "invalid_evidence") {
		t.Fatalf("invalid Evidence still supported a caller claim: %+v", graph)
	}
	mapIR = requireFixture(t, "VS05-A7-secret")
	secretEffect := "api:token=super-secret-value"
	mapIR.Steps[4].SideEffect = &secretEffect
	graph = runImpact(t, semantic.ImpactTarget{SymbolID: "PaymentService.process"}, mapIR, impactOptions(mapIR))
	data, err := json.Marshal(graph)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if strings.Contains(text, "super-secret-value") || !strings.Contains(strings.ToLower(text), "redacted") {
		t.Fatalf("impact graph leaked secret or lost redaction marker: %s", text)
	}
	return evidenceFor("VS05-A7", graph, "evidence:invalid-is-unknown", "redaction:recursive-before-egress")
}

func hasFrontier(graph *semantic.ChangeImpactGraph, kind string) bool {
	for _, frontier := range graph.Frontiers {
		if frontier.BoundaryType == kind {
			return true
		}
	}
	return false
}

// Ensure the runner's public fixture cannot accidentally lose its immutable
// basis identity when the semantic model evolves.
func ValidateFixtureIdentity(mapIR *semantic.SemanticMapIR) error {
	if mapIR == nil || mapIR.SchemaID != semantic.SemanticMapSchemaID || mapIR.SchemaVersion != semantic.SemanticSchemaVersion || mapIR.ComputedBasisID == "" || mapIR.GenerationID == "" || mapIR.ValidatedAgainstSnapshotID == "" || mapIR.Basis.SnapshotTreeID == "" {
		return errors.New("VS05 fixture has incomplete canonical identity")
	}
	return nil
}
