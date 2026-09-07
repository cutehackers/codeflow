package semantic

import (
	"encoding/json"
	"strings"
	"testing"

	"codeflow/internal/fusion"
	"codeflow/internal/slicing"
)

func TestFailureV2_MissingPreconditionsDoNotCreateTrace(t *testing.T) {
	base := failureQuery("debug", &FailureDebugQuery{})
	if err := ValidateFailureQueryV2(base); err == nil || !strings.Contains(err.Error(), "missing_precondition") {
		t.Fatalf("missing debug start did not fail closed: %v", err)
	}
	base.Mode = "incident"
	base.Debug = nil
	base.Incident = &FailureIncidentQuery{}
	if err := ValidateFailureQueryV2(base); err == nil || !strings.Contains(err.Error(), "missing_precondition") {
		t.Fatalf("missing incident start did not fail closed: %v", err)
	}
	if trace, err := InvestigateFailureV2(base, failureMap(), nil, nil, FailureInvestigationOptions{}); err == nil || trace != nil {
		t.Fatalf("missing incident precondition returned a trace: trace=%+v err=%v", trace, err)
	}
}

func TestFailureV2_DebugReversePathUsesCanonicalEvidence(t *testing.T) {
	graph := failureMap()
	query := failureQuery("debug", &FailureDebugQuery{FailureEvidenceID: "ev-failure"})
	trace, err := InvestigateFailureV2(query, graph, nil, nil, FailureInvestigationOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if trace.SchemaID != FailurePathTraceSchemaID || trace.SchemaVersion != 2 || len(trace.Nodes) != 3 {
		t.Fatalf("unexpected canonical failure trace: %+v", trace)
	}
	for _, node := range trace.Nodes {
		if node.NodeID == "node-origin" || node.SymbolPath == "ErrorOrigin" || len(node.EvidenceRefs) == 0 {
			t.Fatalf("synthetic or evidence-free node escaped: %+v", node)
		}
	}
	for _, relation := range trace.Relationships {
		if relation.FromNodeID == relation.ToNodeID || len(relation.EvidenceRefs) == 0 {
			t.Fatalf("invalid reverse relation: %+v", relation)
		}
	}
	if trace.RuntimeObservationRef != "" || len(trace.Timeline) != 0 {
		t.Fatalf("debug investigation fabricated runtime state: %+v", trace)
	}
}

func TestFailureV2_UnresolvedFrontierPreservesCoverage(t *testing.T) {
	graph := failureMap()
	graph.Unknowns = []fusion.Unknown{{Subject: "Plugin.dispatch", Reason: "dynamic relation is not resolved"}}
	query := failureQuery("debug", &FailureDebugQuery{Error: "Service.Fail"})
	trace, err := InvestigateFailureV2(query, graph, nil, nil, FailureInvestigationOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(trace.UnknownFrontier) == 0 || trace.Coverage == nil || trace.Coverage.Complete {
		t.Fatalf("unresolved relation was not exposed as a bounded frontier: %+v", trace)
	}
	for _, frontier := range trace.UnknownFrontier {
		if frontier.LastConfirmedNodeID == "" || frontier.LastConfirmedNodeID == "node-origin" || frontier.Reason == "" {
			t.Fatalf("frontier is not tied to a confirmed node: %+v", frontier)
		}
	}
}

func TestFailureV2_IncidentUsesOnlySuppliedScopedEvents(t *testing.T) {
	graph := failureMap()
	query := failureQuery("incident", &FailureIncidentQuery{TraceID: "trace-1", Scenario: "checkout", Environment: "prod", DependencyFingerprint: "deps-1", TimeWindow: failureWindow()})
	observation := failureObservation(false)
	trace, err := InvestigateFailureV2(query, graph, nil, observation, FailureInvestigationOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if trace.RuntimeObservationRef != observation.ObservationID || len(trace.Timeline) != 1 || trace.Timeline[0].EventID != "event-1" {
		t.Fatalf("incident timeline was not bounded to supplied event: %+v", trace.Timeline)
	}
	for _, node := range trace.Nodes {
		if node.Status == "runtime_observed" {
			t.Fatalf("runtime_observed status was synthesized: %+v", node)
		}
	}
	if len(trace.RecoveryStates) != 4 {
		t.Fatalf("expected explicit recovery uncertainty: %+v", trace.RecoveryStates)
	}
	for _, state := range trace.RecoveryStates {
		if state.Status != "possible_unknown" || len(state.EvidenceRefs) != 0 {
			t.Fatalf("unobserved recovery was promoted: %+v", state)
		}
	}
}

func TestFailureV2_IncidentUnknownCountCountsEachFrontierOnce(t *testing.T) {
	observation := failureObservation(false)
	observation.Events[0].Target = "External.Unknown"
	observation.Events[0].SymbolPath = "External.Unknown"
	query := failureQuery("incident", &FailureIncidentQuery{TraceID: "trace-1", Scenario: "checkout", Environment: "prod", DependencyFingerprint: "deps-1", TimeWindow: failureWindow()})
	trace, err := InvestigateFailureV2(query, failureMap(), nil, observation, FailureInvestigationOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(trace.UnknownFrontier) != 1 {
		t.Fatalf("expected one unmatched runtime frontier, got %+v", trace.UnknownFrontier)
	}
	want := len(trace.UnknownFrontier)
	for _, state := range trace.RecoveryStates {
		if state.Status == "possible_unknown" {
			want++
		}
	}
	if trace.UnknownCount != want {
		t.Fatalf("unknown count double-counted frontiers: got %d want %d", trace.UnknownCount, want)
	}
}

func TestFailureV2_UnobservedRecoveryEventRemainsPossibleUnknown(t *testing.T) {
	observation := failureObservation(false)
	observation.Events[0].Kind = "retry"
	observation.Events[0].Status = "not_observed"
	query := failureQuery("incident", &FailureIncidentQuery{TraceID: "trace-1", Scenario: "checkout", Environment: "prod", DependencyFingerprint: "deps-1", TimeWindow: failureWindow()})
	trace, err := InvestigateFailureV2(query, failureMap(), nil, observation, FailureInvestigationOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range trace.RecoveryStates {
		if state.Kind != "retry" {
			continue
		}
		if state.Status != "possible_unknown" || len(state.EvidenceRefs) != 0 {
			t.Fatalf("negative retry event was promoted: %+v", state)
		}
		return
	}
	t.Fatal("retry recovery state was not returned")
}

func TestFailureV2_MissingRecoveryEventsRemainUnknown(t *testing.T) {
	observation := failureObservation(false)
	observation.Events = []RuntimeObservationEvent{{EventID: "event-1", Timestamp: "2026-09-05T10:00:01Z", Kind: "failure_emission", Target: "Service.Fail", Status: "observed", EvidenceRef: "run-1"}}
	if err := ValidateRuntimeObservationV2(*observation); err != nil {
		t.Fatal(err)
	}
	trace, err := InvestigateFailureV2(failureQuery("incident", &FailureIncidentQuery{TraceID: "trace-1", Scenario: "checkout", Environment: "prod", DependencyFingerprint: "deps-1", TimeWindow: failureWindow()}), failureMap(), nil, observation, FailureInvestigationOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(trace.Timeline) != 1 {
		t.Fatalf("unexpected fabricated recovery timeline: %+v", trace.Timeline)
	}
	for _, event := range trace.Timeline {
		if event.Kind == "timeout" || event.Kind == "retry" || event.Kind == "circuit_break" || event.Kind == "compensation" {
			t.Fatalf("unobserved recovery event was fabricated: %+v", event)
		}
	}
}

func TestFailureV2_StaticRuntimeConflictRetainsBothEvidenceSets(t *testing.T) {
	observation := failureObservation(true)
	query := failureQuery("incident", &FailureIncidentQuery{TraceID: "trace-1", Scenario: "checkout", Environment: "prod", DependencyFingerprint: "deps-1", TimeWindow: failureWindow()})
	trace, err := InvestigateFailureV2(query, failureMap(), nil, observation, FailureInvestigationOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, node := range trace.Nodes {
		if node.SymbolPath == "Service.Fail" {
			found = true
			if node.Status != "conflicting" || !containsString(node.EvidenceRefs, "ev-failure") || !containsString(node.EvidenceRefs, "run-1") {
				t.Fatalf("conflict did not retain static and runtime Evidence: %+v", node)
			}
		}
	}
	if !found || !trace.HasConflicts || len(trace.RuntimeEvidence) != 1 {
		t.Fatalf("conflict output is incomplete: %+v", trace)
	}
}

func TestFailureV2_RejectsInvalidAnchorsAndRedactsOutput(t *testing.T) {
	graph := failureMap()
	graph.Steps[2].Anchor.RepoRelativePath = "/tmp/outside.go"
	if _, err := InvestigateFailureV2(failureQuery("debug", &FailureDebugQuery{Error: "Service.Fail"}), graph, nil, nil, FailureInvestigationOptions{}); err == nil || !strings.Contains(err.Error(), "invalid_anchor") {
		t.Fatalf("unsafe source anchor was accepted: %v", err)
	}
	for _, unsafePath := range []string{" src/fail.go", "src/fail.go\x00generated"} {
		graph = failureMap()
		graph.Steps[2].Anchor.RepoRelativePath = unsafePath
		if _, err := InvestigateFailureV2(failureQuery("debug", &FailureDebugQuery{Error: "Service.Fail"}), graph, nil, nil, FailureInvestigationOptions{}); err == nil || !strings.Contains(err.Error(), "invalid_anchor") {
			t.Fatalf("unsafe source anchor %q was accepted: %v", unsafePath, err)
		}
	}
	observation := failureObservation(false)
	observation.Events[0].Anchor = &slicing.Anchor{RepoRelativePath: "../secret.go", EnclosingSymbolPath: "Service.Fail", FileHash: "file", SpanHash: "span", ByteRange: [2]int{0, 1}}
	if err := ValidateRuntimeObservationV2(*observation); err == nil || !strings.Contains(err.Error(), "invalid_anchor") {
		t.Fatalf("unsafe runtime anchor was accepted: %v", err)
	}

	trace := &FailurePathTrace{SchemaID: FailurePathTraceSchemaID, SchemaVersion: 2, TraceID: "trace", Mode: "debug", ComputedBasisID: "basis", GenerationID: "gen", ValidatedAgainstSnapshotID: "snapshot", Freshness: "historical", FailureTarget: FailureTarget{Error: "api:token=secret"}, Nodes: []FailureNode{{NodeID: "node-x", SymbolPath: "Service.Fail", Role: "throw", Status: "static_candidate", EvidenceRefs: []string{"ev"}}}, Summary: FailureSummary{Description: "api:token=secret", LastConfirmedState: "state"}, StaticEvidence: []SemanticEvidence{{EvidenceID: "ev", ComputedBasisID: "basis", SnapshotID: "snapshot", ValidationStatus: "verified", RedactionStatus: "clean", Anchor: failureAnchor("src/fail.go", "Service.Fail")}}, UnknownFrontier: []FailureFrontier{}, RecoveryStates: []FailureRecoveryState{}, Coverage: &FailureCoverage{Complete: true}}
	clean, err := redactFailureTrace(trace)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(mustJSON(clean)), "secret") {
		t.Fatalf("secret survived failure egress: %s", mustJSON(clean))
	}
}

func TestFailureV2_RejectsRuntimeEvidenceBasisDrift(t *testing.T) {
	observation := failureObservation(false)
	observation.Evidence[0].GenerationID = "generation-other"
	if err := ValidateRuntimeObservationV2(*observation); err == nil || !strings.Contains(err.Error(), "incomparable_basis") {
		t.Fatalf("runtime Evidence from another generation was accepted: %v", err)
	}

	observation = failureObservation(false)
	observation.Evidence[0].SnapshotID = "snapshot-other"
	if err := ValidateRuntimeObservationV2(*observation); err == nil || !strings.Contains(err.Error(), "incomparable_basis") {
		t.Fatalf("runtime Evidence from another snapshot was accepted: %v", err)
	}
}

func TestFailureV2_RejectsDuplicateAndUnbackedRuntimeEvents(t *testing.T) {
	observation := failureObservation(false)
	observation.Events = append(observation.Events, observation.Events[0])
	if err := ValidateRuntimeObservationV2(*observation); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate runtime event was accepted: %v", err)
	}

	observation = failureObservation(false)
	observation.Evidence = nil
	if err := ValidateRuntimeObservationV2(*observation); err == nil || !strings.Contains(err.Error(), "supplied Evidence") {
		t.Fatalf("event without supplied Evidence was accepted: %v", err)
	}
}

func TestFailureV2_TrustedLocalRequiresActorAndTimestamp(t *testing.T) {
	observation := failureObservation(false)
	observation.IsolationLevel = "trusted_local"
	observation.TrustedLocalApproval = &TrustedLocalApproval{Approved: true}
	if err := ValidateRuntimeObservationV2(*observation); err == nil || !strings.Contains(err.Error(), "approving actor") {
		t.Fatalf("trusted_local approval without actor was accepted: %v", err)
	}

	observation.TrustedLocalApproval = &TrustedLocalApproval{Approved: true, ApprovedBy: "operator-1", Timestamp: "not-a-timestamp"}
	if err := ValidateRuntimeObservationV2(*observation); err == nil || !strings.Contains(err.Error(), "timestamp") {
		t.Fatalf("trusted_local approval with invalid timestamp was accepted: %v", err)
	}
}

func failureQuery(mode string, discriminator any) FailureQueryV2 {
	query := FailureQueryV2{SchemaID: FailureQuerySchemaID, SchemaVersion: 2, Mode: mode, ComputedBasisID: "basis-1", GenerationID: "generation-1", ValidatedAgainstSnapshotID: "snapshot-1", Freshness: "historical"}
	if mode == "debug" {
		query.Debug = discriminator.(*FailureDebugQuery)
	} else {
		query.Incident = discriminator.(*FailureIncidentQuery)
	}
	return query
}

func failureMap() *SemanticMapIR {
	steps := []SemanticStep{
		{StepID: "step-root", Name: "Service.Start", TechnicalName: "Service.Start", Kind: "entry", Anchor: failureAnchor("src/start.go", "Service.Start"), EvidenceRefs: []string{"ev-root"}},
		{StepID: "step-handler", Name: "Service.Handle", TechnicalName: "Service.Handle", Kind: "transform", Anchor: failureAnchor("src/handle.go", "Service.Handle"), EvidenceRefs: []string{"ev-handler"}},
		{StepID: "step-failure", Name: "Service.Fail", TechnicalName: "Service.Fail", Kind: "failure", Anchor: failureAnchor("src/fail.go", "Service.Fail"), EvidenceRefs: []string{"ev-failure"}},
	}
	evidence := []SemanticEvidence{
		{EvidenceID: "ev-root", Kind: "source", SourceAuthority: "code", ComputedBasisID: "basis-1", SnapshotID: "snapshot-1", DocumentRevisionID: "rev-root", ValidationStatus: "verified", RedactionStatus: "clean", Anchor: failureAnchor("src/start.go", "Service.Start")},
		{EvidenceID: "ev-handler", Kind: "source", SourceAuthority: "code", ComputedBasisID: "basis-1", SnapshotID: "snapshot-1", DocumentRevisionID: "rev-handler", ValidationStatus: "verified", RedactionStatus: "clean", Anchor: failureAnchor("src/handle.go", "Service.Handle")},
		{EvidenceID: "ev-failure", Kind: "source", SourceAuthority: "code", ComputedBasisID: "basis-1", SnapshotID: "snapshot-1", DocumentRevisionID: "rev-failure", ValidationStatus: "verified", RedactionStatus: "clean", Anchor: failureAnchor("src/fail.go", "Service.Fail")},
	}
	return &SemanticMapIR{SchemaID: SemanticMapSchemaID, SchemaVersion: SemanticSchemaVersion, MapID: "map-1", GenerationID: "generation-1", ComputedBasisID: "basis-1", ValidatedAgainstSnapshotID: "snapshot-1", Freshness: "historical", Authority: "candidate", Steps: steps, Edges: []SemanticEdge{{FromStepID: "step-root", ToStepID: "step-handler", ToSymbolPath: "Service.Handle", Kind: "calls", ResolutionStatus: "verified"}, {FromStepID: "step-handler", ToStepID: "step-failure", ToSymbolPath: "Service.Fail", Kind: "throws", ResolutionStatus: "verified"}}, Evidence: evidence, Coverage: &CoverageBoundary{IncludedSourceRoots: []string{"src"}}}
}

func failureAnchor(file, symbol string) slicing.Anchor {
	return slicing.Anchor{RepoRelativePath: file, ByteRange: [2]int{1, 8}, FileHash: "file-" + strings.ReplaceAll(file, "/", "-"), SpanHash: "span-" + strings.ReplaceAll(symbol, ".", "-"), EnclosingSymbolPath: symbol}
}

func failureWindow() FailureTimeWindow {
	return FailureTimeWindow{From: "2026-09-05T10:00:00Z", To: "2026-09-05T10:00:10Z"}
}

func failureObservation(conflict bool) *RuntimeObservationV2 {
	status := "observed"
	if conflict {
		status = "not_observed"
	}
	return &RuntimeObservation{SchemaID: RuntimeObservationSchemaID, SchemaVersion: 2, ObservationID: "obs-1", TraceID: "trace-1", Scenario: "checkout", Environment: "prod", DependencyFingerprint: "deps-1", ComputedBasisID: "basis-1", GenerationID: "generation-1", ValidatedAgainstSnapshotID: "snapshot-1", Freshness: "historical", ObservedAt: "2026-09-05T10:00:02Z", IsolationLevel: "sandboxed", TimeWindow: failureWindow(), TraceCoverage: TraceCoverage{SpansCovered: 1, TotalSpans: 1, Ratio: 1}, Events: []RuntimeObservationEvent{{EventID: "event-1", Timestamp: "2026-09-05T10:00:01Z", Kind: "failure_emission", Target: "Service.Fail", Status: status, EvidenceRef: "run-1"}}, Evidence: []RuntimeEvidence{{EvidenceID: "run-1", ObservationID: "obs-1", TraceID: "trace-1", ComputedBasisID: "basis-1", GenerationID: "generation-1", SnapshotID: "snapshot-1", ValidatedAgainstSnapshotID: "snapshot-1", Scenario: "checkout", Environment: "prod", DependencyFingerprint: "deps-1", Timestamp: "2026-09-05T10:00:01Z", ValidationStatus: "verified", RedactionStatus: "clean"}}}
}

func mustJSON(value any) []byte {
	data, _ := json.Marshal(value)
	return data
}
