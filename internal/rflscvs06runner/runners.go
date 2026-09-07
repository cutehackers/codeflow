// Package rflscvs06runner contains the executable acceptance runners for the
// evidence-bounded failure contract.  The package is separate from
// rflscvs06 because the production one-shot executor imports the runtime
// contract package.  Keeping the runners in a leaf package lets the registry
// exercise both production packages without introducing an import cycle.
package rflscvs06runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"codeflow/internal/fusion"
	"codeflow/internal/protocol"
	"codeflow/internal/rflscvs06"
	oneshoot "codeflow/internal/runtime"
	"codeflow/internal/semantic"
	"codeflow/internal/slicing"
	"codeflow/internal/workspace"
)

const (
	implementationPackage = "codeflow/internal/rflscvs06runner"
	basisID               = "basis-vs06"
	generationID          = "generation-vs06-01"
	incidentTraceID       = "trace-vs06-01"
	incidentScenario      = "checkout"
	incidentEnvironment   = "staging"
	dependencyFingerprint = "deps-vs06-01"
)

// Evidence is the result returned by a criterion runner.  The contract
// harness adds execution-package and test-binary identity because those facts
// belong to the process that invoked the runner, not to the implementation
// package.
type Evidence struct {
	Criterion                string                     `json:"criterion"`
	ImplementationTestID     string                     `json:"implementationTestId"`
	ImplementationPackage    string                     `json:"implementationPackage"`
	SnapshotID               string                     `json:"snapshotId"`
	SnapshotTreeDigest       string                     `json:"snapshotTreeDigest"`
	InputTreeVerified        bool                       `json:"inputTreeVerified"`
	CopyOnWriteLayerDisposed bool                       `json:"copyOnWriteLayerDisposed"`
	RepositoryPathWriteAudit workspace.SourceWriteAudit `json:"repositoryPathWriteAudit"`
	RuntimeOutcomes          []RuntimeOutcome           `json:"runtimeOutcomes"`
	ObjectRefs               []string                   `json:"objectRefs"`
}

// RuntimeOutcome records the executor-owned result for one terminal path.
// It is intentionally copied out of the result so a registry record cannot
// retain a mutable executor object.
type RuntimeOutcome struct {
	Name                     string `json:"name"`
	ExecutionID              string `json:"executionId"`
	Status                   string `json:"status"`
	CleanupVerified          bool   `json:"cleanupVerified"`
	InputTreeVerified        bool   `json:"inputTreeVerified"`
	SourceReadOnly           bool   `json:"sourceReadOnly"`
	Disposable               bool   `json:"disposable"`
	RepositoryPathExposed    bool   `json:"repositoryPathExposed"`
	SourceWriteAuditStatus   string `json:"sourceWriteAuditStatus"`
	EvidencePromotion        string `json:"evidencePromotion"`
	ConcurrentClassification string `json:"concurrentClassification"`
}

var implementationTestIDs = map[string]string{
	"VS06-A1": "codeflow/internal/rflscvs06runner.TestRFLSCR2VS06_A01",
	"VS06-A2": "codeflow/internal/rflscvs06runner.TestRFLSCR2VS06_A02",
	"VS06-A3": "codeflow/internal/rflscvs06runner.TestRFLSCR2VS06_A03",
	"VS06-A4": "codeflow/internal/rflscvs06runner.TestRFLSCR2VS06_A04",
	"VS06-A5": "codeflow/internal/rflscvs06runner.TestRFLSCR2VS06_A05",
	"VS06-A6": "codeflow/internal/rflscvs06runner.TestRFLSCR2VS06_A06",
	"VS06-A7": "codeflow/internal/rflscvs06runner.TestRFLSCR2VS06_A07",
	"VS06-A8": "codeflow/internal/rflscvs06runner.TestRFLSCR2VS06_A08",
	"VS06-A9": "codeflow/internal/rflscvs06runner.TestRFLSCR2VS06_A09",
}

// Fixture is an immutable snapshot identity paired with a canonical semantic
// failure graph.  Each runner creates a fresh fixture and returns refs bound
// to this exact basis.
type Fixture struct {
	Snapshot protocol.Snapshot
	Map      *semantic.SemanticMapIR
}

// NewFixture builds the smallest complete canonical graph needed by the v2
// failure seam.  The source bytes are carried by protocol.Snapshot so the
// artifact refs are tied to a deterministic input tree rather than a live
// repository path.
func NewFixture() (*Fixture, error) {
	snapshot, err := protocol.NewSnapshot(7, map[string]string{
		"src/start.go":  "package service\nfunc Start() {}\n",
		"src/handle.go": "package service\nfunc Handle() {}\n",
		"src/fail.go":   "package service\nfunc Fail() {}\n",
	}, basisID)
	if err != nil {
		return nil, err
	}
	steps := []semantic.SemanticStep{
		failureStep("step-root", "Service.Start", "entry", "src/start.go", "ev-root"),
		failureStep("step-handler", "Service.Handle", "transform", "src/handle.go", "ev-handler"),
		failureStep("step-failure", "Service.Fail", "failure", "src/fail.go", "ev-failure"),
	}
	evidence := []semantic.SemanticEvidence{
		failureEvidence("ev-root", snapshot, "src/start.go", "Service.Start", "rev-root"),
		failureEvidence("ev-handler", snapshot, "src/handle.go", "Service.Handle", "rev-handler"),
		failureEvidence("ev-failure", snapshot, "src/fail.go", "Service.Fail", "rev-failure"),
	}
	return &Fixture{
		Snapshot: snapshot,
		Map: &semantic.SemanticMapIR{
			SchemaID:                   semantic.SemanticMapSchemaID,
			SchemaVersion:              semantic.SemanticSchemaVersion,
			MapID:                      "map-vs06-01",
			GenerationID:               generationID,
			ComputedBasisID:            basisID,
			ValidatedAgainstSnapshotID: snapshot.SnapshotID,
			PublicationKind:            "initial",
			Freshness:                  "historical",
			Settlement:                 "pending",
			EnrichmentStatus:           "not_requested",
			Authority:                  "historical",
			Quality:                    semantic.MapQuality{Stage: "Q1", UnresolvedCriticalCount: 1},
			Task:                       semantic.MapTaskContext{TaskID: "task-vs06-01", IntentRevision: 1, Mode: "feature"},
			Basis: semantic.MapBasisContext{
				RepositoryID:                "repo-vs06-01",
				WorkspaceEpoch:              snapshot.WorkspaceEpoch,
				ComputedWorkspaceSnapshotID: snapshot.SnapshotID,
				ComputedBasisID:             basisID,
				SnapshotTreeID:              snapshot.RootTreeID,
				DependencyFingerprint:       dependencyFingerprint,
			},
			Summary:  semantic.MapSummary{Requested: "checkout failure", Current: "historical failure graph"},
			Steps:    steps,
			Edges:    []semantic.SemanticEdge{{FromStepID: "step-root", ToStepID: "step-handler", ToSymbolPath: "Service.Handle", Kind: "calls", ResolutionStatus: "verified"}, {FromStepID: "step-handler", ToStepID: "step-failure", ToSymbolPath: "Service.Fail", Kind: "throws", ResolutionStatus: "verified"}},
			Evidence: evidence,
			Unknowns: []fusion.Unknown{},
			Coverage: &semantic.CoverageBoundary{IncludedSourceRoots: []string{"src"}},
		},
	}, nil
}

func failureStep(id, symbol, kind, file, evidenceID string) semantic.SemanticStep {
	return semantic.SemanticStep{
		StepID: id, StructuralIdentity: file + "#" + symbol, Ordinal: len(id), Name: symbol,
		TechnicalName: symbol, Kind: kind, Anchor: failureAnchor(file, symbol), EvidenceRefs: []string{evidenceID},
	}
}

func failureEvidence(id string, snapshot protocol.Snapshot, file, symbol, revision string) semantic.SemanticEvidence {
	return semantic.SemanticEvidence{
		EvidenceID: id, Kind: "source", SourceAuthority: "code", ComputedBasisID: basisID,
		DocumentRevisionID: revision, Anchor: failureAnchor(file, symbol), ValidationStatus: "verified",
		RedactionStatus: "clean", SnapshotID: snapshot.SnapshotID,
	}
}

func failureAnchor(file, symbol string) slicing.Anchor {
	return slicing.Anchor{
		RepoRelativePath: file, ByteRange: [2]int{1, 8}, FileHash: "file-" + strings.ReplaceAll(file, "/", "-"),
		SpanHash: "span-" + strings.ReplaceAll(symbol, ".", "-"), EnclosingSymbolPath: symbol,
	}
}

func failureWindow() semantic.FailureTimeWindow {
	return semantic.FailureTimeWindow{From: "2026-09-05T10:00:00Z", To: "2026-09-05T10:00:10Z"}
}

func failureQuery(fixture *Fixture, mode string) semantic.FailureQueryV2 {
	query := semantic.FailureQueryV2{
		SchemaID: semantic.FailureQuerySchemaID, SchemaVersion: semantic.FailureContractSchemaVersion,
		Mode: mode, ComputedBasisID: fixture.Map.ComputedBasisID, GenerationID: fixture.Map.GenerationID,
		ValidatedAgainstSnapshotID: fixture.Map.ValidatedAgainstSnapshotID, Freshness: fixture.Map.Freshness,
	}
	if mode == "debug" {
		query.Debug = &semantic.FailureDebugQuery{FailureEvidenceID: "ev-failure"}
	} else {
		query.Incident = &semantic.FailureIncidentQuery{
			TraceID: incidentTraceID, Scenario: incidentScenario, Environment: incidentEnvironment,
			DependencyFingerprint: dependencyFingerprint, TimeWindow: failureWindow(),
		}
	}
	return query
}

func failureObservation(fixture *Fixture, conflict bool) *semantic.RuntimeObservationV2 {
	status := "observed"
	if conflict {
		status = "not_observed"
	}
	return &semantic.RuntimeObservationV2{
		SchemaID: semantic.RuntimeObservationSchemaID, SchemaVersion: semantic.FailureContractSchemaVersion,
		ObservationID: "observation-vs06-01", TraceID: incidentTraceID, Scenario: incidentScenario,
		Environment: incidentEnvironment, DependencyFingerprint: dependencyFingerprint,
		ComputedBasisID: fixture.Map.ComputedBasisID, GenerationID: fixture.Map.GenerationID,
		ValidatedAgainstSnapshotID: fixture.Map.ValidatedAgainstSnapshotID, Freshness: fixture.Map.Freshness,
		ObservedAt: "2026-09-05T10:00:02Z", IsolationLevel: "sandboxed", TimeWindow: failureWindow(),
		TraceCoverage: semantic.TraceCoverage{SpansCovered: 1, TotalSpans: 1, Ratio: 1},
		Events: []semantic.RuntimeObservationEvent{{
			EventID: "event-vs06-01", Timestamp: "2026-09-05T10:00:01Z", Kind: "failure_emission",
			Target: "Service.Fail", Status: status, EvidenceRef: "run-vs06-01",
		}},
		Evidence: []semantic.RuntimeEvidence{{
			EvidenceID: "run-vs06-01", ObservationID: "observation-vs06-01", TraceID: incidentTraceID,
			ComputedBasisID: fixture.Map.ComputedBasisID, GenerationID: fixture.Map.GenerationID,
			SnapshotID: fixture.Map.ValidatedAgainstSnapshotID, ValidatedAgainstSnapshotID: fixture.Map.ValidatedAgainstSnapshotID,
			Scenario: incidentScenario, Environment: incidentEnvironment, DependencyFingerprint: dependencyFingerprint,
			Timestamp: "2026-09-05T10:00:01Z", ValidationStatus: "verified", RedactionStatus: "clean",
		}},
	}
}

func requireFixture(t *testing.T, criterion string) *Fixture {
	t.Helper()
	fixture, err := NewFixture()
	if err != nil {
		t.Fatalf("%s fixture construction failed: %v", criterion, err)
	}
	return fixture
}

func evidenceFor(criterion string, fixture *Fixture, refs ...string) Evidence {
	objectRefs := []string{
		"snapshot:" + fixture.Snapshot.SnapshotID,
		"tree:" + fixture.Snapshot.RootTreeID,
		"basis:" + fixture.Map.ComputedBasisID,
		"graph:" + fixture.Map.MapID,
	}
	objectRefs = append(objectRefs, refs...)
	return Evidence{
		Criterion: criterion, ImplementationTestID: implementationTestIDs[criterion], ImplementationPackage: implementationPackage,
		SnapshotID: fixture.Snapshot.SnapshotID, SnapshotTreeDigest: fixture.Snapshot.RootTreeID,
		InputTreeVerified: true, ObjectRefs: objectRefs,
	}
}

// RunA01 proves that a missing debug/incident start is a typed precondition
// failure and creates neither an origin node nor a timeline.
func RunA01(t *testing.T) Evidence {
	t.Helper()
	fixture := requireFixture(t, "VS06-A1")
	query := failureQuery(fixture, "debug")
	query.Debug = &semantic.FailureDebugQuery{}
	if err := semantic.ValidateFailureQueryV2(query); err == nil || !strings.Contains(err.Error(), "missing_precondition") {
		t.Fatalf("missing debug start was not rejected: %v", err)
	}
	query = failureQuery(fixture, "incident")
	query.Incident.TraceID = ""
	query.Incident.IncidentEvidenceID = ""
	if err := semantic.ValidateFailureQueryV2(query); err == nil || !strings.Contains(err.Error(), "missing_precondition") {
		t.Fatalf("missing incident start was not rejected: %v", err)
	}
	if trace, err := semantic.InvestigateFailureEvidenceBounded(semantic.FailureInvestigationInput{Query: query, Map: fixture.Map}); err == nil || trace != nil {
		t.Fatalf("missing precondition produced a trace: trace=%+v err=%v", trace, err)
	}
	return evidenceFor("VS06-A1", fixture, "precondition:missing_start", "origin:no_synthetic", "timeline:none")
}

// RunA02 exercises the production reverse-path projection from failure
// Evidence through every connected graph hop.
func RunA02(t *testing.T) Evidence {
	t.Helper()
	fixture := requireFixture(t, "VS06-A2")
	trace, err := semantic.InvestigateFailureV2(failureQuery(fixture, "debug"), fixture.Map, nil, nil, semantic.FailureInvestigationOptions{})
	if err != nil {
		t.Fatalf("debug reverse path failed: %v", err)
	}
	if len(trace.Nodes) != 3 || len(trace.Relationships) != 2 || len(trace.StaticEvidence) != 3 {
		t.Fatalf("reverse path is incomplete: nodes=%d relationships=%d evidence=%d", len(trace.Nodes), len(trace.Relationships), len(trace.StaticEvidence))
	}
	for _, node := range trace.Nodes {
		if node.NodeID == "node-origin" || node.SymbolPath == "ErrorOrigin" || len(node.EvidenceRefs) == 0 {
			t.Fatalf("synthetic or evidence-free node escaped: %+v", node)
		}
	}
	for _, relation := range trace.Relationships {
		if relation.FromNodeID == relation.ToNodeID || len(relation.EvidenceRefs) == 0 {
			t.Fatalf("invalid or unsupported relation: %+v", relation)
		}
	}
	if len(trace.Timeline) != 0 || trace.RuntimeObservationRef != "" {
		t.Fatalf("debug path fabricated runtime evidence: %+v", trace)
	}
	return evidenceFor("VS06-A2", fixture, "trace:"+trace.TraceID, "static-evidence:ev-root", "static-evidence:ev-handler", "static-evidence:ev-failure")
}

// RunA03 preserves an unresolved relation as an explicit frontier tied to a
// confirmed graph node and coverage boundary.
func RunA03(t *testing.T) Evidence {
	t.Helper()
	fixture := requireFixture(t, "VS06-A3")
	fixture.Map.Unknowns = []fusion.Unknown{{Subject: "Plugin.dispatch", Reason: "dynamic relation is not resolved"}}
	trace, err := semantic.InvestigateFailureV2(failureQuery(fixture, "debug"), fixture.Map, nil, nil, semantic.FailureInvestigationOptions{})
	if err != nil {
		t.Fatalf("unresolved frontier investigation failed: %v", err)
	}
	if len(trace.UnknownFrontier) == 0 || trace.Coverage == nil || trace.Coverage.Complete {
		t.Fatalf("unresolved frontier was not preserved: %+v", trace)
	}
	for _, frontier := range trace.UnknownFrontier {
		if frontier.LastConfirmedNodeID == "" || frontier.LastConfirmedNodeID == "node-origin" || frontier.Reason == "" {
			t.Fatalf("frontier is not bounded by confirmed evidence: %+v", frontier)
		}
	}
	return evidenceFor("VS06-A3", fixture, "frontier:Plugin.dispatch", "coverage:incomplete")
}

// RunA04 admits only the supplied, scoped runtime event to the incident
// projection.
func RunA04(t *testing.T) Evidence {
	t.Helper()
	fixture := requireFixture(t, "VS06-A4")
	observation := failureObservation(fixture, false)
	if err := semantic.ValidateRuntimeObservationV2(*observation); err != nil {
		t.Fatalf("scoped observation failed validation: %v", err)
	}
	trace, err := semantic.InvestigateFailureV2(failureQuery(fixture, "incident"), fixture.Map, nil, observation, semantic.FailureInvestigationOptions{})
	if err != nil {
		t.Fatalf("incident investigation failed: %v", err)
	}
	if trace.RuntimeObservationRef != observation.ObservationID || len(trace.Timeline) != 1 || trace.Timeline[0].EventID != "event-vs06-01" {
		t.Fatalf("incident timeline did not use supplied scoped event: %+v", trace.Timeline)
	}
	return evidenceFor("VS06-A4", fixture, "observation:"+observation.ObservationID, "event:event-vs06-01")
}

// RunA05 verifies that absent timeout/retry/circuit-break/compensation events
// remain possible_unknown and are never emitted as a synthetic timeline.
func RunA05(t *testing.T) Evidence {
	t.Helper()
	fixture := requireFixture(t, "VS06-A5")
	observation := failureObservation(fixture, false)
	trace, err := semantic.InvestigateFailureV2(failureQuery(fixture, "incident"), fixture.Map, nil, observation, semantic.FailureInvestigationOptions{})
	if err != nil {
		t.Fatalf("incident uncertainty projection failed: %v", err)
	}
	if len(trace.Timeline) != 1 || len(trace.RecoveryStates) != 4 {
		t.Fatalf("unexpected synthesized recovery timeline/state: timeline=%+v recovery=%+v", trace.Timeline, trace.RecoveryStates)
	}
	for _, event := range trace.Timeline {
		if event.Kind == "timeout" || event.Kind == "retry" || event.Kind == "circuit_break" || event.Kind == "compensation" {
			t.Fatalf("unobserved recovery event was fabricated: %+v", event)
		}
	}
	for _, state := range trace.RecoveryStates {
		if state.Status != "possible_unknown" || len(state.EvidenceRefs) != 0 {
			t.Fatalf("unobserved recovery was promoted: %+v", state)
		}
	}
	return evidenceFor("VS06-A5", fixture, "recovery:possible_unknown", "timeout:not_observed", "retry:not_observed", "circuit-break:not_observed", "compensation:not_observed")
}

// RunA06 keeps static and runtime Evidence sets when their status conflicts.
func RunA06(t *testing.T) Evidence {
	t.Helper()
	fixture := requireFixture(t, "VS06-A6")
	observation := failureObservation(fixture, true)
	trace, err := semantic.InvestigateFailureV2(failureQuery(fixture, "incident"), fixture.Map, nil, observation, semantic.FailureInvestigationOptions{})
	if err != nil {
		t.Fatalf("conflict investigation failed: %v", err)
	}
	found := false
	for _, node := range trace.Nodes {
		if node.SymbolPath == "Service.Fail" {
			found = true
			if node.Status != "conflicting" || !contains(node.EvidenceRefs, "ev-failure") || !contains(node.EvidenceRefs, "run-vs06-01") {
				t.Fatalf("static/runtime conflict lost one Evidence set: %+v", node)
			}
		}
	}
	if !found || !trace.HasConflicts || len(trace.RuntimeEvidence) != 1 {
		t.Fatalf("conflict output is incomplete: %+v", trace)
	}
	return evidenceFor("VS06-A6", fixture, "conflict:ev-failure+run-vs06-01")
}

// RunA07 verifies explicit trusted-local approval and that the command and
// access/isolation scopes shown to the actor are the values being authorized.
func RunA07(t *testing.T) Evidence {
	t.Helper()
	fixture := requireFixture(t, "VS06-A7")
	command := rflscvs06.RuntimeCommand{Command: "configured-analyzer", Args: []string{"--snapshot"}}
	spec := runtimeSpec(command)
	nonce := strings.Repeat("a", 16)
	consent := runtimeConsent(fixture.Snapshot, spec, "consent-vs06-a7", nonce)
	if err := consent.Validate(time.Time{}); err != nil {
		t.Fatalf("approved trusted-local consent failed validation: %v", err)
	}
	if err := consent.Matches(spec, fixture.Snapshot.SnapshotID, fixture.Snapshot.RootTreeID, nonce); err != nil {
		t.Fatalf("approved consent did not bind exact command/scope/snapshot: %v", err)
	}
	if consent.Command != command.Command || len(consent.Args) != len(command.Args) || consent.AccessScope.Source == "" || consent.IsolationScope.Level == "" {
		t.Fatalf("consent does not preserve actor-visible execution scope: %+v", consent)
	}
	unapproved := consent
	unapproved.Approved = false
	if err := unapproved.Validate(time.Time{}); err == nil {
		t.Fatal("unapproved trusted-local consent was accepted")
	}
	return evidenceFor("VS06-A7", fixture, "consent:"+consent.ConsentID, "command:"+consent.Command, "access-scope:"+consent.AccessScope.Source, "isolation-scope:"+consent.IsolationScope.Level)
}

// RunA08 rejects unsafe anchors at the source/runtime boundary and verifies
// that the semantic egress redactor removes secret values.
func RunA08(t *testing.T) Evidence {
	t.Helper()
	fixture := requireFixture(t, "VS06-A8")
	unsafe := *fixture.Map
	unsafe.Steps = append([]semantic.SemanticStep(nil), fixture.Map.Steps...)
	unsafe.Steps[2].Anchor.RepoRelativePath = "../outside.go"
	if _, err := semantic.InvestigateFailureV2(failureQuery(fixture, "debug"), &unsafe, nil, nil, semantic.FailureInvestigationOptions{}); err == nil || !strings.Contains(err.Error(), "invalid_anchor") {
		t.Fatalf("unsafe source anchor was accepted: %v", err)
	}
	observation := failureObservation(fixture, false)
	observation.Events[0].Anchor = &slicing.Anchor{RepoRelativePath: "/tmp/outside.go", EnclosingSymbolPath: "Service.Fail", FileHash: "file", SpanHash: "span", ByteRange: [2]int{0, 1}}
	if err := semantic.ValidateRuntimeObservationV2(*observation); err == nil || !strings.Contains(err.Error(), "invalid_anchor") {
		t.Fatalf("unsafe runtime anchor was accepted: %v", err)
	}
	secretQuery := failureQuery(fixture, "debug")
	secretQuery.Debug = &semantic.FailureDebugQuery{Error: "api:token=secret-vs06-value", FailureEvidenceID: "ev-failure"}
	trace, err := semantic.InvestigateFailureV2(secretQuery, fixture.Map, nil, nil, semantic.FailureInvestigationOptions{})
	if err != nil {
		t.Fatalf("redaction path failed: %v", err)
	}
	raw, err := json.Marshal(trace)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "secret-vs06-value") || !strings.Contains(strings.ToLower(string(raw)), "redacted") {
		t.Fatalf("secret escaped failure egress: %s", raw)
	}
	return evidenceFor("VS06-A8", fixture, "anchor:rejected", "anchor:redacted", "egress:redacted")
}

// RunA09 exercises the actual one-shot runtime executor for every terminal
// mode and verifies the isolation, audit, replay, digest, and live-edit
// reconciliation boundaries.  It intentionally uses the repository's mock
// adapter binary through protocol.Spawn, not a context-only simulation.
func RunA09(t *testing.T) Evidence {
	t.Helper()
	fixture := requireFixture(t, "VS06-A9")
	bin := buildMockAdapter(t)
	disposableRoot := filepath.Join(t.TempDir(), "disposable")
	if err := os.MkdirAll(disposableRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	baseSpec := runtimeSpec(rflscvs06.RuntimeCommand{Command: bin})
	baseSnapshot := fixture.Snapshot
	baseSnapshot.SourceWriteAudit.CapturedSnapshotTreeDigest = baseSnapshot.RootTreeID
	outcomes := make([]RuntimeOutcome, 0, 7)

	terminalCases := []struct {
		name string
		env  []string
		want string
		ctx  func() (context.Context, context.CancelFunc)
	}{
		{name: "success", want: rflscvs06.RuntimeTerminalSuccess, ctx: func() (context.Context, context.CancelFunc) { return context.Background(), func() {} }},
		{name: "failure", env: []string{"MOCK_CRASH_AFTER_N_REQUESTS=1"}, want: rflscvs06.RuntimeTerminalFailure, ctx: func() (context.Context, context.CancelFunc) { return context.Background(), func() {} }},
		{name: "timeout", env: []string{"MOCK_HANG_OPS=detect"}, want: rflscvs06.RuntimeTerminalTimeout, ctx: func() (context.Context, context.CancelFunc) {
			return context.WithTimeout(context.Background(), 350*time.Millisecond)
		}},
	}
	for _, tc := range terminalCases {
		t.Run("runtime-"+tc.name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "disposable")
			if err := os.MkdirAll(root, 0o700); err != nil {
				t.Fatal(err)
			}
			spec := baseSpec
			executor := oneshoot.NewExecutor(oneshoot.ExecutorConfig{
				AdapterConfig: protocol.Config{BinPath: bin, DisposableRoot: root, Env: tc.env},
				Spec:          spec, AuditProvider: oneshoot.CleanSourceAuditProvider{}, RequireConsent: true,
			})
			nonce := "nonce-vs06-" + tc.name + "-1234"
			executionID := "exec-vs06-" + tc.name
			ctx, cancel := tc.ctx()
			defer cancel()
			result, runErr := executor.Execute(ctx, oneshoot.ExecutionRequest{
				ExecutionID: executionID, Nonce: nonce, Consent: runtimeConsent(baseSnapshot, spec, "consent-"+executionID, nonce),
				Snapshot: baseSnapshot, Operation: protocol.OpDetect,
			})
			if tc.want == rflscvs06.RuntimeTerminalSuccess && runErr != nil {
				t.Fatalf("success execution failed: %v", runErr)
			}
			if tc.want != rflscvs06.RuntimeTerminalSuccess && runErr == nil {
				t.Fatalf("%s execution unexpectedly succeeded", tc.name)
			}
			assertRuntimeOutcome(t, result, tc.want, root)
			outcomes = append(outcomes, runtimeOutcome(tc.name, result))
		})
	}

	// Cancellation must settle and preserve cleanup after the caller cancels.
	t.Run("runtime-cancel", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "disposable")
		if err := os.MkdirAll(root, 0o700); err != nil {
			t.Fatal(err)
		}
		spec := baseSpec
		executor := oneshoot.NewExecutor(oneshoot.ExecutorConfig{AdapterConfig: protocol.Config{BinPath: bin, DisposableRoot: root, Env: []string{"MOCK_HANG_OPS=detect"}}, Spec: spec, AuditProvider: oneshoot.CleanSourceAuditProvider{}, RequireConsent: true})
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := make(chan struct{})
		var result oneshoot.ExecutionResult
		var runErr error
		go func() {
			result, runErr = executor.Execute(ctx, oneshoot.ExecutionRequest{ExecutionID: "exec-vs06-cancel", Nonce: "nonce-vs06-cancel-1234", Consent: runtimeConsent(baseSnapshot, spec, "consent-vs06-cancel", "nonce-vs06-cancel-1234"), Snapshot: baseSnapshot, Operation: protocol.OpDetect})
			close(done)
		}()
		time.Sleep(100 * time.Millisecond)
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("cancelled one-shot execution did not settle")
		}
		if runErr == nil {
			t.Fatal("cancelled execution unexpectedly succeeded")
		}
		assertRuntimeOutcome(t, result, rflscvs06.RuntimeTerminalCancel, root)
		outcomes = append(outcomes, runtimeOutcome("cancel", result))
	})

	// A clean run is not promotion eligible when source-write attribution is
	// unavailable or reports a runtime-attributed write.
	t.Run("runtime-audit", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "disposable")
		if err := os.MkdirAll(root, 0o700); err != nil {
			t.Fatal(err)
		}
		executor := oneshoot.NewExecutor(oneshoot.ExecutorConfig{AdapterConfig: protocol.Config{BinPath: bin, DisposableRoot: root}, Spec: baseSpec, AuditProvider: oneshoot.AttributedWriteSourceAuditProvider{Path: "src/fail.go"}, RequireConsent: true})
		result, err := executor.Execute(context.Background(), oneshoot.ExecutionRequest{ExecutionID: "exec-vs06-audit", Nonce: "nonce-vs06-audit-1234", Consent: runtimeConsent(baseSnapshot, baseSpec, "consent-vs06-audit", "nonce-vs06-audit-1234"), Snapshot: baseSnapshot, Operation: protocol.OpDetect})
		if err != nil {
			t.Fatalf("audit execution failed before terminal result: %v", err)
		}
		if result.Isolation.SourceWriteAuditStatus != rflscvs06.RuntimeAuditViolation || result.Isolation.EvidencePromotion != rflscvs06.RuntimePromotionBlocked {
			t.Fatalf("runtime-attributed source write was promotable: %+v", result.Isolation)
		}
		assertRuntimeOutcome(t, result, rflscvs06.RuntimeTerminalSuccess, root)
		outcomes = append(outcomes, runtimeOutcome("audit-violation", result))
	})

	// Consent replay is rejected before a second subprocess is spawned.
	t.Run("runtime-replay", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "disposable")
		if err := os.MkdirAll(root, 0o700); err != nil {
			t.Fatal(err)
		}
		executor := oneshoot.NewExecutor(oneshoot.ExecutorConfig{AdapterConfig: protocol.Config{BinPath: bin, DisposableRoot: root}, Spec: baseSpec, AuditProvider: oneshoot.CleanSourceAuditProvider{}, RequireConsent: true})
		request := oneshoot.ExecutionRequest{ExecutionID: "exec-vs06-replay", Nonce: "nonce-vs06-replay-1234", Consent: runtimeConsent(baseSnapshot, baseSpec, "consent-vs06-replay", "nonce-vs06-replay-1234"), Snapshot: baseSnapshot, Operation: protocol.OpDetect}
		first, err := executor.Execute(context.Background(), request)
		if err != nil {
			t.Fatalf("first replay execution failed: %v", err)
		}
		assertRuntimeOutcome(t, first, rflscvs06.RuntimeTerminalSuccess, root)
		second, err := executor.Execute(context.Background(), request)
		if !errors.Is(err, oneshoot.ErrConsentReplay) || second.Isolation.ProcessObserved {
			t.Fatalf("replayed consent spawned or returned wrong error: err=%v isolation=%+v", err, second.Isolation)
		}
		outcomes = append(outcomes, runtimeOutcome("replay", first))
	})

	// The executor rejects an input whose declared tree digest does not match
	// its captured bytes before it starts the adapter.
	badSnapshot := baseSnapshot
	badSnapshot.RootTreeID = strings.Repeat("f", 64)
	badSnapshot.SourceWriteAudit.CapturedSnapshotTreeDigest = badSnapshot.RootTreeID
	badRequest := oneshoot.ExecutionRequest{ExecutionID: "exec-vs06-bad-tree", Nonce: "nonce-vs06-bad-tree", Consent: runtimeConsent(badSnapshot, baseSpec, "consent-vs06-bad-tree", "nonce-vs06-bad-tree"), Snapshot: badSnapshot, Operation: protocol.OpDetect}
	badExecutor := oneshoot.NewExecutor(oneshoot.ExecutorConfig{AdapterConfig: protocol.Config{BinPath: bin}, Spec: baseSpec, RequireConsent: true})
	if _, err := badExecutor.Execute(context.Background(), badRequest); !errors.Is(err, oneshoot.ErrSnapshotIdentityMismatch) {
		t.Fatalf("snapshot digest mismatch was accepted: %v", err)
	}

	// A live edit made outside the disposable execution is separately
	// reconciled and never attributed as a runtime source write.
	concurrent := runConcurrentLiveEdit(t, bin, baseSpec)
	outcomes = append(outcomes, concurrent)

	if len(outcomes) < 7 {
		t.Fatalf("A09 did not record all runtime paths: %d", len(outcomes))
	}
	for _, outcome := range outcomes {
		if !outcome.InputTreeVerified || !outcome.CleanupVerified {
			t.Fatalf("A09 outcome lacks immutable/cleanup proof: %+v", outcome)
		}
	}
	record := evidenceFor("VS06-A9", fixture, "runtime:success", "runtime:failure", "runtime:timeout", "runtime:cancel", "runtime:audit-violation", "runtime:replay", "runtime:concurrent-live-edit", "digest:verified", "cleanup:disposed")
	record.SnapshotID = baseSnapshot.SnapshotID
	record.SnapshotTreeDigest = baseSnapshot.RootTreeID
	record.RepositoryPathWriteAudit = baseSnapshot.SourceWriteAudit
	record.CopyOnWriteLayerDisposed = true
	record.RuntimeOutcomes = append([]RuntimeOutcome(nil), outcomes...)
	record.ObjectRefs = append(record.ObjectRefs, "isolation:exec-vs06-success", "audit:source-write", "reconciliation:concurrent-live-edit")
	return record
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func runtimeSpec(command rflscvs06.RuntimeCommand) rflscvs06.RuntimeExecutionSpec {
	return rflscvs06.RuntimeExecutionSpec{
		Command: command, CommandDigest: rflscvs06.CommandDigest(command.Command, command.Args),
		AccessScope:    rflscvs06.RuntimeAccessScope{Source: "immutable_snapshot", Network: "disabled", Credentials: "not_available"},
		IsolationScope: rflscvs06.RuntimeIsolationScope{Level: "trusted_local", SourceMount: "not_mounted", SourcePermission: "read_only_protocol", WorkingDirectory: "process_private_disposable", WritableLayer: "discarded_after_terminal", RepositoryPathExposed: false},
	}
}

func runtimeConsent(snapshot protocol.Snapshot, spec rflscvs06.RuntimeExecutionSpec, consentID, nonce string) rflscvs06.RuntimeConsent {
	now := time.Now().UTC()
	return rflscvs06.RuntimeConsent{
		SchemaID: rflscvs06.RuntimeConsentSchemaID, SchemaVersion: rflscvs06.RuntimeSchemaVersion,
		ConsentID: consentID, ActorID: "vs06-test-actor", ApprovedBy: "vs06-test-actor", Approved: true,
		IssuedAt: now.Add(-time.Second).Format(time.RFC3339Nano), ExpiresAt: now.Add(2 * time.Minute).Format(time.RFC3339Nano),
		Command: spec.Command.Command, Args: append([]string(nil), spec.Command.Args...), CommandDigest: spec.CommandDigest,
		AccessScope: spec.AccessScope, IsolationScope: spec.IsolationScope, SnapshotID: snapshot.SnapshotID,
		SnapshotTreeDigest: snapshot.RootTreeID, Nonce: nonce,
	}
}

func runtimeOutcome(name string, result oneshoot.ExecutionResult) RuntimeOutcome {
	permission := result.Isolation.MountPermissionEvidence
	return RuntimeOutcome{
		Name: name, ExecutionID: result.Isolation.ExecutionID, Status: result.Isolation.Status,
		CleanupVerified: result.Isolation.Cleanup.Verified, InputTreeVerified: result.Isolation.InputTreeVerified,
		SourceReadOnly: permission.ReadOnlySource, Disposable: permission.Disposable,
		RepositoryPathExposed:  permission.RepositoryPathExposed,
		SourceWriteAuditStatus: result.Isolation.SourceWriteAuditStatus, EvidencePromotion: result.Isolation.EvidencePromotion,
		ConcurrentClassification: result.Isolation.ConcurrentWorktree.Classification,
	}
}

func assertRuntimeOutcome(t *testing.T, result oneshoot.ExecutionResult, want, disposableRoot string) {
	t.Helper()
	if result.Isolation.Status != want {
		t.Fatalf("runtime status=%q want=%q: %+v", result.Isolation.Status, want, result.Isolation)
	}
	if !result.Isolation.ProcessObserved || !result.Isolation.InputTreeVerified || !result.Isolation.Cleanup.LayerCreated || !result.Isolation.Cleanup.LayerDisposed || !result.Isolation.Cleanup.Verified {
		t.Fatalf("runtime isolation/cleanup proof incomplete: %+v", result.Isolation)
	}
	permission := result.Isolation.MountPermissionEvidence
	if permission.SourceDelivery != "protocol_snapshot_bytes" || permission.SourceMount != "not_mounted" || permission.WorkingDirectoryMode != "process_private_disposable" || permission.WorkingDirectoryPermission != "0700" || !permission.ReadOnlySource || !permission.Disposable || permission.RepositoryPathExposed || !permission.CleanupVerified {
		t.Fatalf("runtime read-only disposable boundary was not proven: %+v", permission)
	}
	if entries, err := os.ReadDir(disposableRoot); err != nil {
		t.Fatal(err)
	} else if len(entries) != 0 {
		t.Fatalf("disposable layer was not discarded: %+v", entries)
	}
}

func runConcurrentLiveEdit(t *testing.T, bin string, spec rflscvs06.RuntimeExecutionSpec) RuntimeOutcome {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	engine, err := workspace.NewSnapshotEngine(root, 1)
	if err != nil {
		t.Fatal(err)
	}
	_, head, err := engine.ApplyVersionedEdit(context.Background(), workspace.EditRequest{Path: "main.go", Content: []byte("package main\n"), DocumentVersion: 1, Source: workspace.SourceIDEVersioned})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := engine.SnapshotVFS(head.SnapshotID)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	snapshot, err := protocol.SnapshotFromLease(lease)
	if err != nil {
		t.Fatal(err)
	}
	disposableRoot := filepath.Join(t.TempDir(), "disposable")
	if err := os.MkdirAll(disposableRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	executor := oneshoot.NewExecutor(oneshoot.ExecutorConfig{AdapterConfig: protocol.Config{BinPath: bin, DisposableRoot: disposableRoot, Env: []string{"MOCK_HANG_OPS=detect"}}, Spec: spec, Engine: engine, AuditProvider: oneshoot.CleanSourceAuditProvider{}, RequireConsent: true})
	request := oneshoot.ExecutionRequest{ExecutionID: "exec-vs06-concurrent", Nonce: "nonce-vs06-concurrent", Consent: runtimeConsent(snapshot, spec, "consent-vs06-concurrent", "nonce-vs06-concurrent"), Snapshot: snapshot, Operation: protocol.OpDetect}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := make(chan struct{})
	var result oneshoot.ExecutionResult
	var runErr error
	go func() {
		result, runErr = executor.Execute(ctx, request)
		close(done)
	}()
	time.Sleep(120 * time.Millisecond)
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n// concurrent live edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("concurrent live-edit execution did not settle")
	}
	if runErr == nil {
		t.Fatal("hung concurrent execution unexpectedly succeeded")
	}
	assertRuntimeOutcome(t, result, rflscvs06.RuntimeTerminalTimeout, disposableRoot)
	if result.Isolation.ConcurrentWorktree.Classification != "reconciled_unattributed" {
		t.Fatalf("concurrent live edit was attributed to runtime: %+v", result.Isolation.ConcurrentWorktree)
	}
	if result.Isolation.SourceWriteAuditStatus != rflscvs06.RuntimeAuditClean {
		t.Fatalf("concurrent live edit changed runtime source audit: %+v", result.Isolation)
	}
	return runtimeOutcome("concurrent-live-edit", result)
}

func buildMockAdapter(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot := filepath.Dir(filepath.Dir(filepath.Dir(file)))
	bin := filepath.Join(t.TempDir(), "mockadapter")
	cmd := exec.Command("go", "build", "-o", bin, "./internal/mockadapter")
	cmd.Dir = repoRoot
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build mock adapter: %v\n%s", err, output)
	}
	return bin
}

// ValidateEvidence is used by the registry to ensure all refs are immutable
// and tied to the result snapshot.  It intentionally rejects unknown ref kinds
// so a caller cannot smuggle an unbounded or session-only claim into evidence.
func ValidateEvidence(evidence Evidence) error {
	if evidence.Criterion == "" || evidence.ImplementationTestID == "" || evidence.ImplementationPackage != implementationPackage || evidence.SnapshotID == "" || evidence.SnapshotTreeDigest == "" || !evidence.InputTreeVerified {
		return fmt.Errorf("VS06 evidence identity or immutable input proof is incomplete: %+v", evidence)
	}
	if len(evidence.ObjectRefs) < 4 {
		return fmt.Errorf("VS06 evidence has too few immutable refs: %+v", evidence.ObjectRefs)
	}
	seen := map[string]bool{}
	hasSnapshot, hasTree, hasBasis, hasProjection := false, false, false, false
	for _, ref := range evidence.ObjectRefs {
		parts := strings.SplitN(ref, ":", 2)
		if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" || seen[ref] {
			return fmt.Errorf("invalid or duplicate immutable ref %q", ref)
		}
		seen[ref] = true
		switch parts[0] {
		case "snapshot":
			if parts[1] != evidence.SnapshotID {
				return fmt.Errorf("snapshot ref %q does not match snapshot ID %q", ref, evidence.SnapshotID)
			}
			hasSnapshot = true
		case "tree":
			if parts[1] != evidence.SnapshotTreeDigest {
				return fmt.Errorf("tree ref %q does not match tree digest %q", ref, evidence.SnapshotTreeDigest)
			}
			hasTree = true
		case "basis":
			hasBasis = true
		case "graph", "isolation", "audit", "reconciliation", "trace", "observation", "frontier", "coverage", "precondition", "origin", "timeline", "recovery", "timeout", "retry", "circuit-break", "compensation", "conflict", "consent", "command", "access-scope", "isolation-scope", "anchor", "egress", "runtime", "digest", "cleanup", "static-evidence", "event":
			hasProjection = true
		default:
			return fmt.Errorf("unknown VS06 immutable ref kind %q", parts[0])
		}
	}
	if !hasSnapshot || !hasTree || !hasBasis || !hasProjection {
		return fmt.Errorf("snapshot, tree, basis, and criterion artifact refs are required: %+v", evidence.ObjectRefs)
	}
	if evidence.Criterion == "VS06-A9" {
		if !evidence.CopyOnWriteLayerDisposed || evidence.RepositoryPathWriteAudit.CapturedSnapshotTreeDigest != evidence.SnapshotTreeDigest || evidence.RepositoryPathWriteAudit.SourceIntegrityViolation || evidence.RepositoryPathWriteAudit.CodeFlowWriteCount != 0 || len(evidence.RepositoryPathWriteAudit.RepositoryPathWrites) != 0 {
			return fmt.Errorf("A09 runtime isolation/write audit proof is incomplete: %+v", evidence)
		}
		for _, outcome := range evidence.RuntimeOutcomes {
			if outcome.ExecutionID == "" || outcome.Status == "" || !outcome.InputTreeVerified || !outcome.CleanupVerified {
				return fmt.Errorf("A09 terminal outcome is incomplete: %+v", outcome)
			}
		}
	}
	return nil
}
