// Package rflscvs04 contains the shared executable criterion runners for the
// VS-04 semantic compiler contract. The runners deliberately call the public
// semantic/query/compiler seams so the contract registry cannot pass by
// validating only caller-constructed JSON values.
package rflscvs04

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"codeflow/internal/fusion"
	"codeflow/internal/harvest"
	"codeflow/internal/rflscvs02"
	"codeflow/internal/semantic"
	"codeflow/internal/slicing"
)

// Evidence identifies the implementation behavior and its immutable semantic
// artifacts. The contract registry adds the exact test binary and execution ID
// after a runner returns successfully.
type Evidence struct {
	Criterion             string
	ImplementationTestID  string
	ImplementationPackage string
	SnapshotTreeDigest    string
	ObjectRefs            []string
}

var implementationTestIDs = map[string]string{
	"VS04-A1":  "codeflow/internal/rflscvs04.TestRFLSCR2VS04_A01",
	"VS04-A2":  "codeflow/internal/rflscvs04.TestRFLSCR2VS04_A02",
	"VS04-A3":  "codeflow/internal/rflscvs04.TestRFLSCR2VS04_A03",
	"VS04-A4":  "codeflow/internal/rflscvs04.TestRFLSCR2VS04_A04",
	"VS04-A5":  "codeflow/internal/rflscvs04.TestRFLSCR2VS04_A05",
	"VS04-A6":  "codeflow/internal/rflscvs04.TestRFLSCR2VS04_A06",
	"VS04-A7":  "codeflow/internal/rflscvs04.TestRFLSCR2VS04_A07",
	"VS04-A8":  "codeflow/internal/rflscvs04.TestRFLSCR2VS04_A08",
	"VS04-A9":  "codeflow/internal/rflscvs04.TestRFLSCR2VS04_A09",
	"VS04-A10": "codeflow/internal/rflscvs04.TestRFLSCR2VS04_A10",
	"VS04-A11": "codeflow/internal/rflscvs04.TestRFLSCR2VS04_A11",
	"VS04-A12": "codeflow/internal/rflscvs04.TestRFLSCR2VS04_A12",
	"VS04-A13": "codeflow/internal/rflscvs04.TestRFLSCR2VS04_A13",
	"VS04-A14": "codeflow/internal/rflscvs04.TestRFLSCR2VS04_A14",
}

// Fixture is an immutable, validated semantic input used by all runners and
// by the contract registry. It is built from protocol bytes and then passed
// through the production compiler before any criterion assertion is made.
type Fixture struct {
	Input         rflscvs02.SnapshotInput
	SnapshotFiles map[string]string
	Payload       *slicing.SlicedPayload
	Result        rflscvs02.Result
	Target        *semantic.ResolvedTarget
	Intent        *semantic.TaskIntent
	Options       semantic.CompileOptions
	Map           *semantic.SemanticMapIR
	Projection    *semantic.FlowViewProjection
}

const fixtureSource = "package demo\n\nfunc Submit() {\n\tif !Validate() {\n\t\tFail()\n\t}\n\tCommit()\n}\n\nfunc Validate() bool { return true }\n\nfunc Fail() {}\n\nfunc Commit() {}\n\nfunc Result() {}\n"

// NewFixture builds one complete VS-02 validated slice and compiles it through
// the VS-04 production compiler. No repository path is involved.
func NewFixture() (*Fixture, error) {
	files := map[string]string{"service.go": fixtureSource}
	input, err := rflscvs02.SnapshotInputFromContent("snapshot-vs04", "basis-vs04", "tree-vs04", "config-vs04", "deps-vs04", 11, files)
	if err != nil {
		return nil, err
	}
	target := &semantic.ResolvedTarget{
		EntrySymbolPath: "service.go#demo.Submit",
		CandidateID:     "cand-vs04demo",
		FlowID:          fusion.ComputeFlowID("service.go#demo.Submit"),
		Title:           "submit flow",
	}
	steps, err := fixtureSteps(input)
	if err != nil {
		return nil, err
	}
	firstOrdinal := 1
	payload := &slicing.SlicedPayload{
		CandidateID:     target.CandidateID,
		Language:        "go",
		EntrySymbolPath: target.EntrySymbolPath,
		Steps:           steps,
		Edges: []slicing.SliceEdge{
			{Kind: "resolved_cross_file", ToSymbolPath: "service.go#demo.Validate", ResolutionStatus: "resolved", Depth: 1, StepOrdinal: &firstOrdinal},
			{Kind: "resolved_cross_file", ToSymbolPath: "service.go#demo.Fail", ResolutionStatus: "resolved", Depth: 1, StepOrdinal: &firstOrdinal},
			{Kind: "resolved_cross_file", ToSymbolPath: "service.go#demo.Commit", ResolutionStatus: "resolved", Depth: 1, StepOrdinal: &firstOrdinal},
			{Kind: "resolved_cross_file", ToSymbolPath: "service.go#demo.Result", ResolutionStatus: "resolved", Depth: 1, StepOrdinal: &firstOrdinal},
			{Kind: "boundary_call", ToSymbolPath: "service.go#demo.External", ResolutionStatus: "unknown", Depth: 1, StepOrdinal: &firstOrdinal},
		},
		Truncated:            false,
		VisitedCycleDetected: false,
		RedactedCount:        0,
	}
	result, err := fixtureResult(input, payload)
	if err != nil {
		return nil, err
	}
	if err := payload.BindValidatedResult(result); err != nil {
		return nil, err
	}
	intent, err := semantic.NormalizeTaskIntent("사용자 결제 제출 흐름을 보여줘", semantic.IntentOptions{Mode: "feature", TaskID: "task-vs04", Revision: 1})
	if err != nil {
		return nil, err
	}
	options := semantic.CompileOptions{
		ComputedBasisID:            input.ComputedBasisID,
		WorkspaceEpoch:             input.WorkspaceEpoch,
		GenerationID:               "generation-vs04-01",
		ValidatedAgainstSnapshotID: input.SnapshotID,
		SnapshotID:                 input.SnapshotID,
		SnapshotTreeID:             input.RootTreeID,
		RepositoryID:               "repository-vs04",
		DependencyFingerprint:      input.DependencyFingerprint,
		ConfigurationFingerprint:   input.ConfigurationFingerprint,
		AdapterVersion:             result.AdapterVersion,
		AnalyzerRevision:           result.AnalyzerRevision,
		AnalysisReadSetID:          result.ReadSet.ReadSetID,
		CausalObservationClosureID: result.Closure.ClosureID,
		SnapshotFiles:              cloneFiles(files),
		SnapshotInput:              &input,
	}
	mapIR, projection, err := semantic.CompileDeterministicFeatureMap(target, intent, payload, options)
	if err != nil {
		return nil, err
	}
	return &Fixture{Input: input, SnapshotFiles: files, Payload: payload, Result: result, Target: target, Intent: intent, Options: options, Map: mapIR, Projection: projection}, nil
}

func fixtureSteps(input rflscvs02.SnapshotInput) ([]slicing.SliceStep, error) {
	content, ok := input.Document("service.go")
	if !ok {
		return nil, errors.New("fixture source document is missing")
	}
	spans := []struct {
		needle string
		symbol string
		kind   string
		desc   string
		guard  string
		before string
		after  string
		effect string
	}{
		{"func Submit() {\n\tif !Validate() {\n\t\tFail()\n\t}\n\tCommit()\n}", "demo.Submit", "user_action", "submit", "", "", "", ""},
		{"func Validate() bool { return true }", "demo.Validate", "decision", "validate", "validated", "", "", ""},
		{"func Fail() {}", "demo.Fail", "failure", "fail", "", "", "", ""},
		{"func Commit() {}", "demo.Commit", "effect", "commit", "", "pending", "committed", "payment ledger"},
		{"func Result() {}", "demo.Result", "result", "result", "", "", "", ""},
	}
	fileSum := sha256.Sum256(content.Bytes)
	fileHash := hex.EncodeToString(fileSum[:])
	steps := make([]slicing.SliceStep, 0, len(spans))
	for i, span := range spans {
		start := bytes.Index(content.Bytes, []byte(span.needle))
		if start < 0 {
			return nil, fmt.Errorf("fixture span %q is missing", span.needle)
		}
		end := start + len(span.needle)
		spanSum := sha256.Sum256(content.Bytes[start:end])
		astSum := sha256.Sum256([]byte("ast:" + span.symbol + ":" + span.kind))
		step := slicing.SliceStep{
			Ordinal: i + 1, Kind: span.kind, Description: span.desc, SymbolPath: span.symbol,
			Anchor: slicing.Anchor{
				RepoRelativePath: "service.go", ByteRange: [2]int{start, end}, FileHash: fileHash,
				SpanHash: hex.EncodeToString(spanSum[:]), EnclosingSymbolPath: span.symbol,
				CanonicalAstFingerprint: hex.EncodeToString(astSum[:]),
			},
		}
		if span.guard != "" {
			step.GuardCondition = stringPtr(span.guard)
		}
		if span.before != "" || span.after != "" {
			step.StateBefore = stringPtr(span.before)
			step.StateAfter = stringPtr(span.after)
		}
		if span.effect != "" {
			step.EffectTarget = stringPtr(span.effect)
		}
		steps = append(steps, step)
	}
	return steps, nil
}

func fixtureResult(input rflscvs02.SnapshotInput, payload *slicing.SlicedPayload) (rflscvs02.Result, error) {
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return rflscvs02.Result{}, err
	}
	documents := make([]rflscvs02.ReadDocument, 0, len(input.Documents))
	for _, doc := range input.Documents {
		documents = append(documents, rflscvs02.ReadDocument{
			Path: doc.Path, DocumentRevisionID: doc.RevisionID, ContentID: doc.ContentID,
			ContentHash: doc.ContentID, DocumentVersion: doc.DocumentVersion, ByteLength: doc.ByteLength,
		})
	}
	readSet := rflscvs02.AnalysisReadSet{
		SchemaID: rflscvs02.ReadSetSchemaID, SchemaVersion: rflscvs02.SchemaVersion,
		ReadSetID: "readset-vs04-01", ComputedBasisID: input.ComputedBasisID,
		WorkspaceEpoch: input.WorkspaceEpoch, Documents: documents,
		NegativeObservations: []rflscvs02.Observation{}, MembershipObservations: []rflscvs02.Observation{}, DependencyFrontiers: []rflscvs02.Observation{},
	}
	closure := rflscvs02.ObservationClosure{
		SchemaID: rflscvs02.ClosureSchemaID, SchemaVersion: rflscvs02.SchemaVersion,
		ClosureID: "closure-vs04-01", AnalysisReadSetID: readSet.ReadSetID,
		ComputedBasisID: input.ComputedBasisID, WorkspaceEpoch: input.WorkspaceEpoch, Status: "closed",
		NegativeObservations: []rflscvs02.Observation{}, MembershipObservations: []rflscvs02.Observation{}, DependencyFrontiers: []rflscvs02.Observation{},
		RequiredObservations: []string{}, MeasuredObservations: []string{}, IncompleteReasons: []string{},
	}
	result := rflscvs02.Result{
		SchemaID: rflscvs02.AnalyzerResultSchemaID, SchemaVersion: rflscvs02.SchemaVersion,
		RequestID: "request-vs04-01", Operation: "slice", AdapterVersion: "vs04-adapter/1", AnalyzerRevision: "vs04-analyzer/1",
		WorkspaceEpoch: input.WorkspaceEpoch, ComputedBasisID: input.ComputedBasisID, SnapshotID: input.SnapshotID,
		SnapshotTreeDigest: input.RootTreeID, DependencyFingerprint: input.DependencyFingerprint,
		ReadSet: readSet, Closure: closure,
		Capability: rflscvs02.CapabilityProfile{Adapter: "go", AdapterVersion: "vs04-adapter/1", AnalyzerRevision: "vs04-analyzer/1", Features: []string{"snapshot_bytes"}},
		Coverage:   rflscvs02.Coverage{IncludedSourceRoots: []string{"."}, ExcludedReasons: []string{}, Measured: true},
		Facts:      []rflscvs02.Fact{}, Diagnostics: []rflscvs02.Diagnostic{}, Payload: payloadBytes,
	}
	request, err := rflscvs02.NewAnalyzerRequest(result.RequestID, result.Operation, input, nil, nil)
	if err != nil {
		return rflscvs02.Result{}, err
	}
	if err := rflscvs02.ValidateResult(request, result); err != nil {
		return rflscvs02.Result{}, err
	}
	return result, nil
}

func cloneFiles(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for path, content := range in {
		out[path] = content
	}
	return out
}

func evidenceFor(criterion string, fixture *Fixture, refs ...string) Evidence {
	objectRefs := []string{"snapshot:" + fixture.Input.SnapshotID, "tree:" + fixture.Input.RootTreeID, "basis:" + fixture.Input.ComputedBasisID}
	objectRefs = append(objectRefs, refs...)
	return Evidence{Criterion: criterion, ImplementationTestID: implementationTestIDs[criterion], ImplementationPackage: "codeflow/internal/rflscvs04", SnapshotTreeDigest: fixture.Input.RootTreeID, ObjectRefs: objectRefs}
}

func requireFixture(t *testing.T, criterion string) *Fixture {
	t.Helper()
	fixture, err := NewFixture()
	if err != nil {
		t.Fatalf("%s production fixture failed: %v", criterion, err)
	}
	return fixture
}

// RunA01 exercises typed query precondition and ambiguity errors.
func RunA01(t *testing.T) Evidence {
	t.Helper()
	fixture := requireFixture(t, "VS04-A1")
	if _, err := semantic.ResolveFeatureQueryTarget(&semantic.TaskViewQuery{Mode: "feature"}, nil); err == nil || !strings.Contains(err.Error(), semantic.ErrCodeMissingPrecondition) {
		t.Fatalf("missing feature precondition was not typed: %v", err)
	}
	candidates := []harvest.Candidate{
		{CandidateID: "cand-signup01", EntrySymbolPath: "service.go#Signup.start", Score: 1, IntentSignals: harvest.IntentSignals{DerivedName: "signup"}},
		{CandidateID: "cand-signup02", EntrySymbolPath: "other.go#Signup.start", Score: 1, IntentSignals: harvest.IntentSignals{DerivedName: "signup"}},
	}
	_, err := semantic.ResolveFeatureQueryTarget(&semantic.TaskViewQuery{Mode: "feature", Feature: &semantic.FeatureQueryParams{Request: "signup"}}, candidates)
	var queryErr *semantic.QueryError
	if !errors.As(err, &queryErr) || queryErr.Code != semantic.ErrCodeAmbiguousTarget || len(queryErr.CandidateTargets) != 2 {
		t.Fatalf("ambiguous query did not return typed candidates: %v", err)
	}
	if _, err := semantic.ComputeSemanticDelta("review-missing-baseline", nil, fixture.Map); !errors.Is(err, semantic.ErrMissingPrecondition) {
		t.Fatalf("missing review baseline was not rejected with typed precondition: %v", err)
	}
	if _, err := semantic.ComputeSemanticDelta("review-missing-current", fixture.Map, nil); !errors.Is(err, semantic.ErrMissingPrecondition) {
		t.Fatalf("missing review current was not rejected with typed precondition: %v", err)
	}
	incomparable := cloneMap(fixture.Map)
	incomparable.GenerationID = "generation-vs04-incomparable"
	incomparable.ComputedBasisID = "basis-vs04-incomparable"
	incomparable.ValidatedAgainstSnapshotID = "snapshot-vs04-incomparable"
	incomparable.Basis.ComputedBasisID = incomparable.ComputedBasisID
	incomparable.Basis.ComputedWorkspaceSnapshotID = incomparable.ValidatedAgainstSnapshotID
	incomparable.Basis.WorkspaceEpoch++
	if _, err := semantic.ComputeSemanticDelta("review-incomparable", fixture.Map, incomparable); !errors.Is(err, semantic.ErrIncomparableBasis) {
		t.Fatalf("incomparable review basis was not typed: %v", err)
	}
	return evidenceFor("VS04-A1", fixture, "query:missing_precondition", "query:ambiguous_target", "review:missing_baseline", "review:missing_current", "review:incomparable_basis")
}

// RunA02 exercises one complete canonical generation containing structure,
// evidence, coverage, unknowns, and candidate authority.
func RunA02(t *testing.T) Evidence {
	fixture := requireFixture(t, "VS04-A2")
	if len(fixture.Map.Steps) != len(fixture.Payload.Steps) || len(fixture.Map.Evidence) != len(fixture.Map.Steps) || fixture.Map.Coverage == nil || fixture.Map.Authority != "candidate" || len(fixture.Map.Unknowns) == 0 {
		t.Fatalf("canonical generation is incomplete: %+v", fixture.Map)
	}
	if fixture.Map.Freshness == "current" || fixture.Map.Settlement == "passed" {
		t.Fatalf("candidate compiler fabricated authority: freshness=%s settlement=%s", fixture.Map.Freshness, fixture.Map.Settlement)
	}
	if len(fixture.Map.Coverage.IncludedSourceRoots) == 0 || fixture.Map.Quality.Stage == "" || len(fixture.Map.Quality.CriticalObligations) == 0 {
		t.Fatalf("canonical quality or coverage evidence is incomplete: %+v", fixture.Map)
	}
	hasEntry, hasDecision, hasStateEffect, hasFailure, hasResult := false, false, false, false, false
	for _, step := range fixture.Map.Steps {
		if step.CodeLens == nil || len(step.EvidenceRefs) == 0 {
			t.Fatalf("canonical step lacks source evidence: %+v", step)
		}
		switch step.Kind {
		case "user_action", "entry":
			hasEntry = true
		case "decision", "guard", "branch":
			hasDecision = true
		case "mutation", "effect", "external_effect":
			hasStateEffect = hasStateEffect || step.StateDelta != nil || step.SideEffect != nil
		case "failure":
			hasFailure = true
		case "result", "terminal":
			hasResult = true
		}
	}
	if !hasEntry || !hasDecision || !hasStateEffect || !hasFailure || !hasResult {
		t.Fatalf("canonical generation omitted semantic roles: entry=%t decision=%t stateEffect=%t failure=%t result=%t", hasEntry, hasDecision, hasStateEffect, hasFailure, hasResult)
	}
	return evidenceFor("VS04-A2", fixture, "map:"+fixture.Map.MapID, "projection:"+fixture.Projection.ProjectionID)
}

// RunA03 verifies every emitted edge endpoint resolves to a canonical step.
func RunA03(t *testing.T) Evidence {
	fixture := requireFixture(t, "VS04-A3")
	steps := make(map[string]bool, len(fixture.Map.Steps))
	for _, step := range fixture.Map.Steps {
		steps[step.StepID] = true
	}
	if len(fixture.Map.Edges) != 4 {
		t.Fatalf("expected four resolved relation edges, got %d", len(fixture.Map.Edges))
	}
	for _, edge := range fixture.Map.Edges {
		if edge.FromStepID == "" || edge.ToStepID == "" || !steps[edge.FromStepID] || !steps[edge.ToStepID] || edge.ToSymbolPath == "" || edge.ResolutionStatus != "resolved" {
			t.Fatalf("edge identity is not canonical: %+v", edge)
		}
	}
	return evidenceFor("VS04-A3", fixture, "edges:resolved-canonical")
}

// RunA04 proves that a line move preserves structural identity while its
// presentation range and content hash change.
func RunA04(t *testing.T) Evidence {
	fixture := requireFixture(t, "VS04-A4")
	baseMap, _, err := semantic.CompileDeterministicFeatureMap(fixture.Target, fixture.Intent, fixture.Payload, fixture.Options)
	if err != nil {
		t.Fatalf("baseline production compiler failed: %v", err)
	}

	shiftedFiles := map[string]string{"service.go": "// source moved without a semantic edit\n\n" + fixtureSource}
	shiftedInput, err := rflscvs02.SnapshotInputFromContent("snapshot-vs04-shifted", "basis-vs04-shifted", "tree-vs04-shifted", "config-vs04-shifted", "deps-vs04-shifted", fixture.Input.WorkspaceEpoch, shiftedFiles)
	if err != nil {
		t.Fatal(err)
	}
	shiftedSteps, err := fixtureSteps(shiftedInput)
	if err != nil {
		t.Fatal(err)
	}
	shiftedPayload := *fixture.Payload
	shiftedPayload.Steps = shiftedSteps
	shiftedPayload.Edges = append([]slicing.SliceEdge(nil), fixture.Payload.Edges...)
	shiftedResult, err := fixtureResult(shiftedInput, &shiftedPayload)
	if err != nil {
		t.Fatal(err)
	}
	if err := shiftedPayload.BindValidatedResult(shiftedResult); err != nil {
		t.Fatal(err)
	}
	shiftedOptions := fixture.Options
	shiftedOptions.ComputedBasisID = shiftedInput.ComputedBasisID
	shiftedOptions.GenerationID = "generation-vs04-shifted"
	shiftedOptions.ValidatedAgainstSnapshotID = shiftedInput.SnapshotID
	shiftedOptions.SnapshotID = shiftedInput.SnapshotID
	shiftedOptions.SnapshotTreeID = shiftedInput.RootTreeID
	shiftedOptions.DependencyFingerprint = shiftedInput.DependencyFingerprint
	shiftedOptions.ConfigurationFingerprint = shiftedInput.ConfigurationFingerprint
	shiftedOptions.AnalysisReadSetID = shiftedResult.ReadSet.ReadSetID
	shiftedOptions.CausalObservationClosureID = shiftedResult.Closure.ClosureID
	shiftedOptions.SnapshotFiles = cloneFiles(shiftedFiles)
	shiftedOptions.SnapshotInput = &shiftedInput
	shiftedMap, _, err := semantic.CompileDeterministicFeatureMap(fixture.Target, fixture.Intent, &shiftedPayload, shiftedOptions)
	if err != nil {
		t.Fatalf("shifted production compiler failed: %v", err)
	}
	if len(baseMap.Steps) == 0 || len(shiftedMap.Steps) == 0 || baseMap.Steps[0].StepID != shiftedMap.Steps[0].StepID {
		t.Fatalf("source move changed structural step selection: baseline=%+v shifted=%+v", baseMap.Steps, shiftedMap.Steps)
	}
	baseStep, shiftedStep := baseMap.Steps[0], shiftedMap.Steps[0]
	if baseStep.CodeLens == nil || shiftedStep.CodeLens == nil || baseStep.CodeLens.StartLine == shiftedStep.CodeLens.StartLine || baseStep.Anchor.ByteRange == shiftedStep.Anchor.ByteRange || baseStep.Anchor.FileHash == shiftedStep.Anchor.FileHash {
		t.Fatalf("source move did not update presentation anchor while preserving identity: baseline=%+v shifted=%+v", baseStep, shiftedStep)
	}
	return evidenceFor("VS04-A4", fixture, "map:"+shiftedMap.MapID, "identity:"+shiftedStep.StepID, "source-anchor:moved")
}

// RunA05 executes comparable-generation delta behavior and checks that a
// changed behavior is materialized instead of silently disappearing.
func RunA05(t *testing.T) Evidence {
	fixture := requireFixture(t, "VS04-A5")
	baseline := cloneMap(fixture.Map)
	current := cloneMap(fixture.Map)
	current.GenerationID = "generation-vs04-02"
	current.ComputedBasisID = "basis-vs04-02"
	current.ValidatedAgainstSnapshotID = "snapshot-vs04-02"
	current.Basis.ComputedBasisID = current.ComputedBasisID
	current.Basis.ComputedWorkspaceSnapshotID = current.ValidatedAgainstSnapshotID
	current.Basis.SnapshotTreeID = "tree-vs04-02"
	current.Basis.DependencyFingerprint = "deps-vs04-02"
	current.Steps[0].Branch = stringPtr("authenticated")
	current.Steps[1].EvidenceRefs = []string{"evidence-vs04-current"}
	current.Steps[2].Anchor.ByteRange = [2]int{current.Steps[2].Anchor.ByteRange[0] + 1, current.Steps[2].Anchor.ByteRange[1] + 1}
	removedStepID := current.Steps[3].StepID
	current.Steps = append(current.Steps[:3], current.Steps[4:]...)
	added := cloneMap(fixture.Map).Steps[3]
	added.StepID = "step-vs04-added"
	added.StructuralIdentity = "service.go\x00demo.Audit\x00demo.Audit\x00effect"
	added.TechnicalName = "demo.Audit"
	added.Name = "audit"
	current.Steps = append(current.Steps, added)
	delta, err := semantic.ComputeSemanticDelta("comparison-vs04", baseline, current)
	if err != nil {
		t.Fatal(err)
	}
	if delta.Status != "comparable" || delta.StructuralSummary.AddedStepsCount != 1 || delta.StructuralSummary.ChangedStepsCount != 1 || delta.StructuralSummary.RemovedStepsCount != 1 || delta.StructuralSummary.CollapsedStructuralCount != 1 {
		t.Fatalf("delta summary did not distinguish all structural categories: %+v", delta)
	}
	kinds := make(map[string]bool, len(delta.Changes))
	for _, change := range delta.Changes {
		kinds[change.Kind] = true
	}
	for _, kind := range []string{"added_behavior", "changed_rule", "removed_behavior", "evidence_updated", "structural_only"} {
		if !kinds[kind] {
			t.Fatalf("delta omitted %s: %+v", kind, delta)
		}
	}
	if !kinds["removed_behavior"] || removedStepID == "" {
		t.Fatalf("removed behavior evidence is incomplete: %+v", delta)
	}
	return evidenceFor("VS04-A5", fixture, "delta:"+delta.ComparisonID)
}

// RunA06 verifies one-to-many structural matching returns ambiguous_move.
func RunA06(t *testing.T) Evidence {
	fixture := requireFixture(t, "VS04-A6")
	baseline := cloneMap(fixture.Map)
	duplicate := baseline.Steps[0]
	duplicate.StepID = "step-duplicate01"
	baseline.Steps = append(baseline.Steps, duplicate)
	current := cloneMap(fixture.Map)
	current.GenerationID = "generation-vs04-ambiguous"
	current.ComputedBasisID = "basis-vs04-ambiguous"
	current.ValidatedAgainstSnapshotID = "snapshot-vs04-ambiguous"
	current.Basis.ComputedBasisID = current.ComputedBasisID
	current.Basis.ComputedWorkspaceSnapshotID = current.ValidatedAgainstSnapshotID
	current.Basis.SnapshotTreeID = "tree-vs04-ambiguous"
	current.Basis.DependencyFingerprint = "deps-vs04-ambiguous"
	currentStep := current.Steps[0]
	currentStep.StepID = "step-newambig01"
	current.Steps[0] = currentStep
	delta, err := semantic.ComputeSemanticDelta("comparison-vs04-ambiguous", baseline, current)
	if err != nil {
		t.Fatal(err)
	}
	if len(delta.Changes) == 0 {
		t.Fatalf("ambiguous structural move was guessed: %+v", delta)
	}
	ambiguous := 0
	for _, change := range delta.Changes {
		if change.Kind == "ambiguous_move" && change.MoveStatus == "ambiguous" {
			ambiguous++
		}
	}
	if ambiguous != 1 {
		t.Fatalf("ambiguous structural move was not preserved exactly once: %+v", delta)
	}
	return evidenceFor("VS04-A6", fixture, "delta:ambiguous_move")
}

// RunA07 proves basis-verified evidence still awaits VS-03 current proof.
func RunA07(t *testing.T) Evidence {
	fixture := requireFixture(t, "VS04-A7")
	criteria := []semantic.AcceptanceCriterion{{ID: "AC-submit", Text: "submit"}}
	alignments := semantic.ComputeRequirementAlignment(criteria, fixture.Map, semantic.AlignmentOptions{})
	if len(alignments) != 1 || alignments[0].Status != "partial" || alignments[0].Reason != "awaiting_current_proof" || alignments[0].Authority != "candidate" {
		t.Fatalf("verified candidate was promoted without current proof: %+v", alignments)
	}
	return evidenceFor("VS04-A7", fixture, "alignment:"+alignments[0].CriterionID)
}

// RunA08 proves agent/model text and unsupported authorities cannot promote an
// alignment.
func RunA08(t *testing.T) Evidence {
	fixture := requireFixture(t, "VS04-A8")
	criteria := []semantic.AcceptanceCriterion{{ID: "AC-submit", Text: "submit", RequiredEvidenceKinds: []string{"runtime"}}}
	alignments := semantic.ComputeRequirementAlignment(criteria, fixture.Map, semantic.AlignmentOptions{AgentDeclarations: []string{"done"}, ModelProposals: []string{"verified"}})
	if len(alignments) != 1 || alignments[0].Status == "confirmed" || len(alignments[0].MissingEvidence) == 0 {
		t.Fatalf("agent/model text satisfied evidence requirement: %+v", alignments)
	}
	mapWithUntrusted := cloneMap(fixture.Map)
	mapWithUntrusted.Evidence[0].SourceAuthority = "agent"
	alignments = semantic.ComputeRequirementAlignment([]semantic.AcceptanceCriterion{{ID: "AC-submit", Text: "submit"}}, mapWithUntrusted, semantic.AlignmentOptions{})
	if len(alignments) != 1 || alignments[0].Status != "conflicting" {
		t.Fatalf("untrusted evidence authority was not rejected: %+v", alignments)
	}
	return evidenceFor("VS04-A8", fixture, "authority:allowlist", "agent-text:non-evidence")
}

// RunA09 exercises critical preservation and fold/drilldown metadata.
func RunA09(t *testing.T) Evidence {
	fixture := requireFixture(t, "VS04-A9")
	large := cloneMap(fixture.Map)
	large.Steps = make([]semantic.SemanticStep, 0, 20)
	for i := 0; i < 20; i++ {
		step := fixture.Map.Steps[i%len(fixture.Map.Steps)]
		step.StepID = fmt.Sprintf("step-vs04-%02d", i+1)
		step.Ordinal = i + 1
		step.Name = fmt.Sprintf("step %02d", i+1)
		step.TechnicalName = fmt.Sprintf("demo.Step%02d", i+1)
		step.StructuralIdentity = fmt.Sprintf("service.go\x00demo.Step%02d\x00call", i+1)
		step.EvidenceRefs = append([]string(nil), fixture.Map.Steps[0].EvidenceRefs...)
		step.Kind = "call"
		if i == 0 {
			step.Kind = "user_action"
		}
		if i == 5 {
			step.Kind = "decision"
		}
		if i == 9 {
			step.Kind = "security"
		}
		if i == 13 {
			step.Kind = "external_effect"
		}
		if i == 19 {
			step.Kind = "failure"
		}
		large.Steps = append(large.Steps, step)
	}
	large.BoundaryTargets = []string{"boundary-vs04-unknown"}
	projection := semantic.BuildFlowViewProjection(large)
	if len(projection.FoldedSubflows) == 0 || len(projection.VisibleStepRefs) > 15 || len(projection.UnknownBoundaryRefs) != 1 {
		t.Fatalf("large projection did not expose fold/unknown metadata: %+v", projection)
	}
	visible := make(map[string]bool, len(projection.VisibleStepRefs))
	for _, ref := range projection.VisibleStepRefs {
		visible[ref] = true
	}
	for _, step := range large.Steps {
		if step.Kind == "user_action" || step.Kind == "decision" || step.Kind == "security" || step.Kind == "external_effect" || step.Kind == "failure" {
			if !visible[step.StepID] {
				t.Fatalf("critical step folded away: %s", step.StepID)
			}
		}
	}
	if err := semantic.ValidateFlowViewProjectionAgainstMap(projection, large); err != nil {
		t.Fatal(err)
	}
	return evidenceFor("VS04-A9", fixture, "projection:"+projection.ProjectionID, "boundary:boundary-vs04-unknown")
}

// RunA10 calls the model-free compiler, delta and alignment seams twice and
// compares the complete deterministic flow result.
func RunA10(t *testing.T) Evidence {
	fixture := requireFixture(t, "VS04-A10")
	first, firstProjection, err := semantic.CompileDeterministicFeatureMap(fixture.Target, fixture.Intent, fixture.Payload, fixture.Options)
	if err != nil {
		t.Fatal(err)
	}
	second, secondProjection, err := semantic.CompileDeterministicFeatureMap(fixture.Target, fixture.Intent, fixture.Payload, fixture.Options)
	if err != nil {
		t.Fatal(err)
	}
	criteria := []semantic.AcceptanceCriterion{{ID: "AC-submit", Text: "submit"}}
	firstAlignment := semantic.ComputeRequirementAlignment(criteria, first, semantic.AlignmentOptions{})
	secondAlignment := semantic.ComputeRequirementAlignment(criteria, second, semantic.AlignmentOptions{})
	if len(firstAlignment) != 1 || len(secondAlignment) != 1 || firstAlignment[0].Status != "partial" || secondAlignment[0].Status != "partial" {
		t.Fatalf("deterministic alignment seam did not produce candidate status: first=%+v second=%+v", firstAlignment, secondAlignment)
	}
	currentFirst := cloneMap(first)
	currentFirst.GenerationID = "generation-vs04-deterministic-current"
	currentFirst.ComputedBasisID = "basis-vs04-deterministic-current"
	currentFirst.ValidatedAgainstSnapshotID = "snapshot-vs04-deterministic-current"
	currentFirst.Basis.ComputedBasisID = currentFirst.ComputedBasisID
	currentFirst.Basis.ComputedWorkspaceSnapshotID = currentFirst.ValidatedAgainstSnapshotID
	currentFirst.Basis.SnapshotTreeID = "tree-vs04-deterministic-current"
	currentFirst.Basis.DependencyFingerprint = "deps-vs04-deterministic-current"
	currentSecond := cloneMap(second)
	currentSecond.GenerationID = currentFirst.GenerationID
	currentSecond.ComputedBasisID = currentFirst.ComputedBasisID
	currentSecond.ValidatedAgainstSnapshotID = currentFirst.ValidatedAgainstSnapshotID
	currentSecond.Basis.ComputedBasisID = currentSecond.ComputedBasisID
	currentSecond.Basis.ComputedWorkspaceSnapshotID = currentSecond.ValidatedAgainstSnapshotID
	currentSecond.Basis.SnapshotTreeID = currentFirst.Basis.SnapshotTreeID
	currentSecond.Basis.DependencyFingerprint = currentFirst.Basis.DependencyFingerprint
	firstDelta, err := semantic.ComputeSemanticDelta("comparison-vs04-deterministic", first, currentFirst)
	if err != nil {
		t.Fatal(err)
	}
	secondDelta, err := semantic.ComputeSemanticDelta("comparison-vs04-deterministic", second, currentSecond)
	if err != nil {
		t.Fatal(err)
	}
	if firstDelta.Status != "comparable" || secondDelta.Status != "comparable" {
		t.Fatalf("deterministic delta seam did not produce comparable result: first=%+v second=%+v", firstDelta, secondDelta)
	}
	left, _ := json.Marshal(struct {
		Map        *semantic.SemanticMapIR
		Projection *semantic.FlowViewProjection
		Delta      *semantic.SemanticDeltaIR
		Alignment  []semantic.RequirementAlignment
	}{first, firstProjection, firstDelta, firstAlignment})
	right, _ := json.Marshal(struct {
		Map        *semantic.SemanticMapIR
		Projection *semantic.FlowViewProjection
		Delta      *semantic.SemanticDeltaIR
		Alignment  []semantic.RequirementAlignment
	}{second, secondProjection, secondDelta, secondAlignment})
	if !bytes.Equal(left, right) || !reflect.DeepEqual(fixture.Projection, firstProjection) {
		t.Fatal("model-free flow, delta or alignment output is not deterministic")
	}
	return evidenceFor("VS04-A10", fixture, "determinism:byte-identical", "delta:comparable", "alignment:partial")
}

// RunA11 exercises immutable raw intent and explicit revision transitions.
func RunA11(t *testing.T) Evidence {
	fixture := requireFixture(t, "VS04-A11")
	raw := "  submit인지 preview인지 흐름을 보여줘\n"
	intent, err := semantic.NormalizeTaskIntent(raw, semantic.IntentOptions{Mode: "feature", TaskID: "task-vs04-intent"})
	if err != nil {
		t.Fatal(err)
	}
	if intent.Request.RawRequest != raw || intent.IntentStatus != "needs_confirmation" {
		t.Fatalf("raw or ambiguity lifecycle was normalized incorrectly: %+v", intent)
	}
	before, _ := json.Marshal(intent)
	confirmed, err := semantic.ConfirmTaskIntent(intent, "service.go#demo.Submit", "payments")
	if err != nil || confirmed.Revision != intent.Revision+1 || confirmed.IntentStatus != "user_confirmed" {
		t.Fatalf("explicit confirmation transition failed: %v %+v", err, confirmed)
	}
	after, _ := json.Marshal(intent)
	if !bytes.Equal(before, after) {
		t.Fatal("confirmation mutated the prior intent revision")
	}
	revised, err := semantic.ReviseTaskIntent(intent, " submit only ", semantic.IntentOptions{Mode: "feature"})
	if err != nil || revised.Request.RawRequest != " submit only " || revised.Revision != intent.Revision+1 {
		t.Fatalf("raw revision transition failed: %v %+v", err, revised)
	}
	return evidenceFor("VS04-A11", fixture, "intent:revision-1", "intent:revision-2")
}

// RunA12 proves an unresolved intent returns typed ambiguity and cannot reach
// compiler output.
func RunA12(t *testing.T) Evidence {
	fixture := requireFixture(t, "VS04-A12")
	ambiguous, err := semantic.NormalizeTaskIntent("submit인지 preview인지", semantic.IntentOptions{Mode: "feature", TaskID: "task-vs04-ambiguous"})
	if err != nil || ambiguous.IntentStatus != "needs_confirmation" {
		t.Fatalf("ambiguous intent was not retained: %v %+v", err, ambiguous)
	}
	_, _, err = semantic.CompileDeterministicFeatureMap(fixture.Target, ambiguous, fixture.Payload, fixture.Options)
	var queryErr *semantic.QueryError
	if !errors.As(err, &queryErr) || queryErr.Code != semantic.ErrCodeAmbiguousTarget {
		t.Fatalf("compiler did not return typed ambiguous target: %v", err)
	}
	return evidenceFor("VS04-A12", fixture, "intent:needs_confirmation", "compiler:no-publication")
}

// RunA13 verifies the semantic output keeps intent and implementation
// authority separate and never presents the candidate as current.
func RunA13(t *testing.T) Evidence {
	fixture := requireFixture(t, "VS04-A13")
	data, err := json.Marshal(fixture.Map)
	if err != nil {
		t.Fatal(err)
	}
	serialized := string(data)
	if !strings.Contains(serialized, `"authority":"candidate"`) || !strings.Contains(serialized, `"freshness":"historical"`) || !strings.Contains(serialized, `"settlement":"pending"`) || strings.Contains(serialized, `"authority":"current"`) {
		t.Fatalf("semantic authority fields are not candidate-scoped: %s", serialized)
	}
	if fixture.Intent.Request.RawRequest == "" || fixture.Intent.NormalizedIntent.ExpectedOutcome == "" || len(fixture.Map.Evidence) == 0 || fixture.Map.Evidence[0].SourceAuthority != "code" {
		t.Fatal("raw, normalized, or Evidence-backed implementation fields are incomplete")
	}
	return evidenceFor("VS04-A13", fixture, "authority:candidate", "evidence:code")
}

// RunA14 checks that model/agent completion text cannot become Evidence,
// implementation fact, alignment confirmation, or current authority.
func RunA14(t *testing.T) Evidence {
	fixture := requireFixture(t, "VS04-A14")
	criteria := []semantic.AcceptanceCriterion{{ID: "AC-submit", Text: "submit"}}
	alignments := semantic.ComputeRequirementAlignment(criteria, fixture.Map, semantic.AlignmentOptions{AgentDeclarations: []string{"implementation complete"}, ModelProposals: []string{"confirmed"}})
	if len(alignments) != 1 || alignments[0].Status == "confirmed" || alignments[0].Authority != "candidate" {
		t.Fatalf("agent/model text changed alignment authority: %+v", alignments)
	}
	for _, evidence := range fixture.Map.Evidence {
		if evidence.SourceAuthority == "agent" || evidence.SourceAuthority == "model" || evidence.ValidationStatus != "verified" {
			t.Fatalf("implementation evidence was contaminated by agent/model text: %+v", evidence)
		}
	}
	return evidenceFor("VS04-A14", fixture, "authority:code-only", "text:non-evidence")
}

func cloneMap(in *semantic.SemanticMapIR) *semantic.SemanticMapIR {
	data, _ := json.Marshal(in)
	var out semantic.SemanticMapIR
	_ = json.Unmarshal(data, &out)
	return &out
}

func stringPtr(value string) *string { return &value }
