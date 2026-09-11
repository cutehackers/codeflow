package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"codeflow/internal/contractharness"
	"codeflow/internal/evidence"
	"codeflow/internal/fusion"
	"codeflow/internal/runtime"
	"codeflow/internal/semantic"
	"codeflow/internal/slicing"
	"codeflow/internal/storage"
	"codeflow/internal/workspace"
)

type mcpFailureFixture struct {
	debugQuery    semantic.FailureQueryV2
	incidentQuery semantic.FailureQueryV2
	mapIR         *semantic.SemanticMapIR
	proof         *storage.GenerationProofManifest
	observation   *semantic.RuntimeObservationV2
}

func newMCPFailureFixture(snapshotID string) mcpFailureFixture {
	basis := strings.Repeat("a", 64)
	generation := "generation-vs06"
	mapIR := mcpFailureMap(basis, generation, snapshotID)
	proof := mcpHistoricalProof(basis, generation, snapshotID)
	windowFrom := time.Now().UTC().Add(-2 * time.Minute).Truncate(time.Second)
	windowTo := windowFrom.Add(5 * time.Minute)
	eventAt := windowFrom.Add(time.Minute)
	window := semantic.FailureTimeWindow{From: windowFrom.Format(time.RFC3339Nano), To: windowTo.Format(time.RFC3339Nano)}
	observationID := "observation-vs06"
	traceID := "trace-vs06"
	scenario := "checkout-payment-declined"
	environment := "staging"
	dependencyFingerprint := "deps-vs06"
	debugQuery := semantic.FailureQueryV2{
		SchemaID:                   semantic.FailureQuerySchemaID,
		SchemaVersion:              semantic.FailureContractSchemaVersion,
		Mode:                       "debug",
		ComputedBasisID:            basis,
		GenerationID:               generation,
		ValidatedAgainstSnapshotID: snapshotID,
		Freshness:                  "historical",
		Debug:                      &semantic.FailureDebugQuery{FailureEvidenceID: "ev-failure"},
	}
	incidentQuery := semantic.FailureQueryV2{
		SchemaID:                   semantic.FailureQuerySchemaID,
		SchemaVersion:              semantic.FailureContractSchemaVersion,
		Mode:                       "incident",
		ComputedBasisID:            basis,
		GenerationID:               generation,
		ValidatedAgainstSnapshotID: snapshotID,
		Freshness:                  "historical",
		Incident: &semantic.FailureIncidentQuery{
			TraceID:               traceID,
			IncidentEvidenceID:    "incident-vs06",
			RuntimeObservationID:  observationID,
			Scenario:              scenario,
			Environment:           environment,
			DependencyFingerprint: dependencyFingerprint,
			TimeWindow:            window,
		},
	}
	observation := &semantic.RuntimeObservation{
		SchemaID:                   semantic.RuntimeObservationSchemaID,
		SchemaVersion:              semantic.FailureContractSchemaVersion,
		ObservationID:              observationID,
		TraceID:                    traceID,
		IncidentEvidenceID:         "incident-vs06",
		Scenario:                   scenario,
		Environment:                environment,
		DependencyFingerprint:      dependencyFingerprint,
		ComputedBasisID:            basis,
		GenerationID:               generation,
		ValidatedAgainstSnapshotID: snapshotID,
		Freshness:                  "historical",
		ObservedAt:                 eventAt.Add(time.Second).Format(time.RFC3339Nano),
		IsolationLevel:             "no_egress_sandbox",
		TimeWindow:                 window,
		TraceCoverage:              semantic.TraceCoverage{SpansCovered: 1, TotalSpans: 1, Ratio: 1},
		Events: []semantic.RuntimeObservationEvent{{
			EventID: "event-failure", Timestamp: eventAt.Format(time.RFC3339Nano), Kind: "failure_emission",
			Target: "Service.Fail", SymbolPath: "Service.Fail", Status: "observed", EvidenceRef: "ev-runtime",
		}},
		Evidence: []semantic.RuntimeEvidence{{
			EvidenceID: "ev-runtime", ObservationID: observationID, TraceID: traceID,
			GenerationID: generation, ComputedBasisID: basis, SnapshotID: snapshotID,
			ValidatedAgainstSnapshotID: snapshotID, Scenario: scenario, Environment: environment,
			DependencyFingerprint: dependencyFingerprint, Timestamp: eventAt.Format(time.RFC3339Nano),
			ValidationStatus: "verified", RedactionStatus: "clean",
		}},
	}
	return mcpFailureFixture{debugQuery: debugQuery, incidentQuery: incidentQuery, mapIR: mapIR, proof: proof, observation: observation}
}

func mcpFailureMap(basis, generation, snapshotID string) *semantic.SemanticMapIR {
	steps := []semantic.SemanticStep{
		{StepID: "step-root", StructuralIdentity: "flow-vs06|src/start.go|Service.Start|entry", Ordinal: 1, Name: "Service.Start", TechnicalName: "Service.Start", Kind: "entry", Anchor: mcpFailureAnchor("src/start.go", "Service.Start"), EvidenceRefs: []string{"ev-root"}},
		{StepID: "step-handler", StructuralIdentity: "flow-vs06|src/handle.go|Service.Handle|transform", Ordinal: 2, Name: "Service.Handle", TechnicalName: "Service.Handle", Kind: "transform", Anchor: mcpFailureAnchor("src/handle.go", "Service.Handle"), EvidenceRefs: []string{"ev-handler"}},
		{StepID: "step-failure", StructuralIdentity: "flow-vs06|src/fail.go|Service.Fail|failure", Ordinal: 3, Name: "Service.Fail", TechnicalName: "Service.Fail", Kind: "failure", Anchor: mcpFailureAnchor("src/fail.go", "Service.Fail"), EvidenceRefs: []string{"ev-failure"}},
	}
	evidence := []semantic.SemanticEvidence{
		{EvidenceID: "ev-root", Kind: "source", SourceAuthority: "code", ComputedBasisID: basis, DocumentRevisionID: "rev-root", Anchor: mcpFailureAnchor("src/start.go", "Service.Start"), ValidationStatus: "verified", RedactionStatus: "clean", SnapshotID: snapshotID, ByteRange: [2]int{1, 8}, LineRange: [2]int{1, 2}},
		{EvidenceID: "ev-handler", Kind: "source", SourceAuthority: "code", ComputedBasisID: basis, DocumentRevisionID: "rev-handler", Anchor: mcpFailureAnchor("src/handle.go", "Service.Handle"), ValidationStatus: "verified", RedactionStatus: "clean", SnapshotID: snapshotID, ByteRange: [2]int{1, 8}, LineRange: [2]int{1, 2}},
		{EvidenceID: "ev-failure", Kind: "source", SourceAuthority: "code", ComputedBasisID: basis, DocumentRevisionID: "rev-failure", Anchor: mcpFailureAnchor("src/fail.go", "Service.Fail"), ValidationStatus: "verified", RedactionStatus: "clean", SnapshotID: snapshotID, ByteRange: [2]int{1, 8}, LineRange: [2]int{1, 2}},
	}
	return &semantic.SemanticMapIR{
		SchemaID: semantic.SemanticMapSchemaID, SchemaVersion: semantic.SemanticSchemaVersion,
		MapID: "map-" + generation, GenerationID: generation, ComputedBasisID: basis,
		ValidatedAgainstSnapshotID: snapshotID, PublicationKind: "initial", Freshness: "historical",
		Settlement: "pending", EnrichmentStatus: "not_requested", Authority: "candidate",
		Quality: semantic.MapQuality{Stage: "Q1", UnresolvedCriticalCount: 0, ConflictingCriticalCount: 0},
		Task:    semantic.MapTaskContext{TaskID: "task-vs06", IntentRevision: 1, Mode: "feature"},
		Basis: semantic.MapBasisContext{
			RepositoryID: "repo-vs06", WorktreeID: "worktree-vs06", WorkspaceEpoch: 1,
			ComputedWorkspaceSnapshotID: snapshotID, ComputedBasisID: basis, SnapshotTreeID: "tree-vs06",
			DependencyFingerprint: "deps-vs06", AnalysisReadSetID: "readset-vs06", CausalObservationClosureID: "closure-vs06",
		},
		Summary: semantic.MapSummary{Requested: "trace checkout failure", Current: "candidate failure path"},
		Steps:   steps,
		Edges: []semantic.SemanticEdge{
			{FromStepID: "step-root", ToStepID: "step-handler", ToSymbolPath: "Service.Handle", Kind: "calls", ResolutionStatus: "verified"},
			{FromStepID: "step-handler", ToStepID: "step-failure", ToSymbolPath: "Service.Fail", Kind: "throws", ResolutionStatus: "verified"},
		},
		Unknowns: []fusion.Unknown{}, Coverage: &semantic.CoverageBoundary{IncludedSourceRoots: []string{"src"}, ExcludedReasons: []string{}},
		Evidence: evidence,
	}
}

func mcpFailureAnchor(path, symbol string) slicing.Anchor {
	return slicing.Anchor{
		RepoRelativePath: path, ByteRange: [2]int{1, 8}, FileHash: "file-" + strings.ReplaceAll(path, "/", "-"),
		SpanHash: "span-" + strings.ReplaceAll(symbol, ".", "-"), EnclosingSymbolPath: symbol,
		CanonicalAstFingerprint: "ast-" + strings.ReplaceAll(symbol, ".", "-"),
	}
}

func mcpHistoricalProof(basis, generation, snapshotID string) *storage.GenerationProofManifest {
	return &storage.GenerationProofManifest{
		SchemaID: semantic.GenerationProofSchemaID, SchemaVersion: semantic.SemanticSchemaVersion,
		ProofID: "proof-" + generation, GenerationID: generation, ComputedBasisID: basis,
		ComputedSnapshotID: snapshotID, ValidatedAgainstSnapshotID: snapshotID, TaskIntentRevision: 1,
		NormalizedQueryHash: strings.Repeat("b", 64), AnalysisReadSetID: "readset-vs06", CausalObservationClosureID: "closure-vs06",
		CausalObservationClosureDigest: strings.Repeat("c", 64), CapabilityProfileDigest: strings.Repeat("d", 64), WorkspaceEpoch: 1,
		CurrentPublication:   storage.CurrentPublicationResult{Eligibility: "passed", SnapshotGate: "passed", ClosureGate: "passed", EvidenceGate: "passed", SemanticAtomicityGate: "passed", TaskRelevanceGate: "passed", ComprehensionGate: "passed"},
		SettlementEvaluation: storage.SettlementEvaluation{Gate: "pending", BlockingObligationRefs: []string{}},
		ArtifactRefs: storage.ArtifactRefs{
			SemanticMap: "cas:sha256:" + strings.Repeat("e", 64), Projection: "cas:sha256:" + strings.Repeat("f", 64),
			AnalysisReadSet: "cas:sha256:" + strings.Repeat("1", 64), ObservationClosure: "cas:sha256:" + strings.Repeat("2", 64),
			AnalyzerResult: "cas:sha256:" + strings.Repeat("3", 64),
		},
		ExpectedLiveHeadSnapshotID: snapshotID, ExpectedPreviousGenerationID: nil, PublishedAt: time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC),
	}
}

func TestMCPFailureV2HistoricalRequiresExplicitMapAndProof(t *testing.T) {
	root := t.TempDir()
	srv, err := NewServer(Config{RepoRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	fixture := newMCPFailureFixture("snapshot-vs06")
	ctx := context.Background()

	for name, args := range map[string]map[string]any{
		"missing map and proof": {"target": root, "query": fixture.debugQuery},
		"missing proof":         {"target": root, "query": fixture.debugQuery, "semanticMap": fixture.mapIR},
		"missing map":           {"target": root, "query": fixture.debugQuery, "proof": fixture.proof},
	} {
		t.Run(name, func(t *testing.T) {
			res, callErr := srv.executeTool(ctx, "investigate_failure", args)
			if callErr != nil {
				t.Fatalf("unexpected execution error: %v", callErr)
			}
			doc, ok := res.(map[string]any)
			if !ok || doc["code"] != "missing_precondition" || doc["corroborated"] != false {
				t.Fatalf("expected typed historical precondition, got %#v", res)
			}
		})
	}

	res, err := srv.executeTool(ctx, "investigate_failure", map[string]any{
		"target": root, "query": fixture.debugQuery, "semanticMap": fixture.mapIR, "proof": fixture.proof,
	})
	if err != nil {
		t.Fatalf("explicit historical investigation failed: %v", err)
	}
	trace, ok := res.(*semantic.FailurePathTraceV2)
	if !ok || trace.SchemaID != semantic.FailurePathTraceSchemaID || len(trace.Nodes) != 3 || len(trace.Relationships) != 2 {
		t.Fatalf("expected canonical v2 reverse path, got %#v", res)
	}
}

func TestMCPFailureV2CurrentRejectsStaleLiveHead(t *testing.T) {
	root := t.TempDir()
	srv, err := NewServer(Config{RepoRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	query, err := installMCPStaleCurrentProof(srv, root)
	if err != nil {
		t.Fatalf("install current proof fixture: %v", err)
	}
	res, err := srv.executeTool(context.Background(), "investigate_failure", map[string]any{"target": root, "query": query})
	if err != nil {
		t.Fatalf("unexpected stale-head execution error: %v", err)
	}
	doc, ok := res.(map[string]any)
	if !ok || doc["code"] != "stale_live_head" || doc["status"] != "unknown" || doc["corroborated"] != false {
		t.Fatalf("current stale proof was not exposed as typed unknown: %#v", res)
	}
}

func TestMCPFailureV2IncidentRequiresServerObservationIDAndProvider(t *testing.T) {
	root := t.TempDir()
	fixture := newMCPFailureFixture("snapshot-vs06")
	calledID := ""
	srv, err := NewServer(Config{
		RepoRoot: root,
		RuntimeObservationProvider: RuntimeObservationProviderFunc(func(_ context.Context, request RuntimeObservationRequest) (*semantic.RuntimeObservationV2, error) {
			calledID = request.ObservationID
			return fixture.observation, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	ctx := context.Background()

	missingID := fixture.incidentQuery
	missingID.Incident = cloneMCPIncident(missingID.Incident)
	missingID.Incident.RuntimeObservationID = ""
	res, err := srv.executeTool(ctx, "investigate_failure", map[string]any{
		"target": root, "query": missingID, "semanticMap": fixture.mapIR, "proof": fixture.proof,
	})
	if err != nil {
		t.Fatalf("missing observation ID returned execution error: %v", err)
	}
	doc, ok := res.(map[string]any)
	if !ok || doc["code"] != "missing_precondition" || doc["corroborated"] != false {
		t.Fatalf("expected missing observation ID precondition, got %#v", res)
	}

	res, err = srv.executeTool(ctx, "investigate_failure", map[string]any{
		"target": root, "query": fixture.incidentQuery, "semanticMap": fixture.mapIR, "proof": fixture.proof,
	})
	if err != nil {
		t.Fatalf("provider-backed incident failed: %v", err)
	}
	trace, ok := res.(*semantic.FailurePathTraceV2)
	if !ok || trace.RuntimeObservationRef != fixture.observation.ObservationID || len(trace.Timeline) != 1 || trace.Timeline[0].EventID != "event-failure" {
		t.Fatalf("provider-backed incident was not bounded to supplied observation: %#v", res)
	}
	if calledID != fixture.observation.ObservationID {
		t.Fatalf("provider received observation ID %q, want %q", calledID, fixture.observation.ObservationID)
	}
}

func TestMCPFailureV2ObservationIDMaySupplyIncidentIdentity(t *testing.T) {
	root := t.TempDir()
	fixture := newMCPFailureFixture("snapshot-vs06")
	srv, err := NewServer(Config{
		RepoRoot: root,
		RuntimeObservationProvider: RuntimeObservationProviderFunc(func(_ context.Context, request RuntimeObservationRequest) (*semantic.RuntimeObservationV2, error) {
			if request.ObservationID != fixture.observation.ObservationID {
				t.Fatalf("provider received observation ID %q, want %q", request.ObservationID, fixture.observation.ObservationID)
			}
			return fixture.observation, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	query := fixture.incidentQuery
	query.Incident = cloneMCPIncident(query.Incident)
	query.Incident.TraceID = ""
	query.Incident.IncidentEvidenceID = ""
	res, err := srv.executeTool(context.Background(), "investigate_failure", map[string]any{
		"target": root, "query": query, "semanticMap": fixture.mapIR, "proof": fixture.proof,
	})
	if err != nil {
		t.Fatalf("observation-ID incident returned execution error: %v", err)
	}
	trace, ok := res.(*semantic.FailurePathTraceV2)
	if !ok {
		t.Fatalf("expected provider-bound failure trace, got %#v", res)
	}
	if trace.RuntimeObservationRef != fixture.observation.ObservationID || trace.FailureTarget.IncidentTraceID != fixture.observation.TraceID || len(trace.Timeline) != 1 {
		t.Fatalf("provider observation identity was not bound before semantic investigation: %+v", trace)
	}
}

func TestMCPFailureV2BlockedObservationDisclosesIsolationState(t *testing.T) {
	root := t.TempDir()
	fixture := newMCPFailureFixture("snapshot-vs06")
	blocked := *fixture.observation
	blocked.IsolationLevel = "blocked"
	srv, err := NewServer(Config{
		RepoRoot: root,
		RuntimeObservationProvider: RuntimeObservationProviderFunc(func(context.Context, RuntimeObservationRequest) (*semantic.RuntimeObservationV2, error) {
			return &blocked, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	res, err := srv.executeTool(context.Background(), "investigate_failure", map[string]any{
		"target": root, "query": fixture.incidentQuery, "semanticMap": fixture.mapIR, "proof": fixture.proof,
	})
	if err != nil {
		t.Fatalf("blocked observation returned execution error: %v", err)
	}
	doc, ok := res.(map[string]any)
	if !ok || doc["code"] != "blocked" || doc["runtimeObservationId"] != blocked.ObservationID || doc["isolationLevel"] != "blocked" || doc["corroborated"] != false {
		t.Fatalf("blocked observation omitted trusted isolation disclosure: %#v", res)
	}
}

func TestMCPFailureV2ConflictNeverClaimsCorroborated(t *testing.T) {
	root := t.TempDir()
	fixture := newMCPFailureFixture("snapshot-vs06")
	observation := *fixture.observation
	observation.Events = append([]semantic.RuntimeObservationEvent(nil), fixture.observation.Events...)
	observation.Events[0].Outcome = "conflict"
	srv, err := NewServer(Config{
		RepoRoot: root,
		RuntimeObservationProvider: RuntimeObservationProviderFunc(func(_ context.Context, _ RuntimeObservationRequest) (*semantic.RuntimeObservationV2, error) {
			return &observation, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	res, err := srv.executeTool(context.Background(), "investigate_failure", map[string]any{
		"target": root, "query": fixture.incidentQuery, "semanticMap": fixture.mapIR, "proof": fixture.proof,
	})
	if err != nil {
		t.Fatalf("conflicting incident returned execution error: %v", err)
	}
	trace, ok := res.(*semantic.FailurePathTraceV2)
	if !ok {
		t.Fatalf("expected failure trace, got %#v", res)
	}
	if !trace.HasConflicts {
		t.Fatalf("conflicting runtime event did not mark trace conflict: %+v", trace)
	}
	// The public MCP wrapper emits corroborated only for trusted_local. Verify
	// the shared predicate directly for this conflict-bearing trace as well.
	if failureTraceCorroborated(trace) {
		t.Fatal("conflicting trace was classified as corroborated")
	}
}

func TestMCPFailureV2TrustedLocalBlocksUnapprovedAndIneligiblePromotion(t *testing.T) {
	root := t.TempDir()
	engine, err := workspace.NewSnapshotEngine(root, 0)
	if err != nil {
		t.Fatal(err)
	}
	head, err := engine.Reconcile(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	fixture := newMCPFailureFixture(head.SnapshotID)
	fixture.incidentQuery.Incident.Scenario = "trusted-local"
	fixture.incidentQuery.Incident.Environment = "test"
	fixture.incidentQuery.Incident.DependencyFingerprint = "deps-trusted-local"
	fixture.observation = mcpTrustedLocalObservation(fixture.incidentQuery, true)

	called := false
	executor := RuntimeOneShotExecutorFunc(func(_ context.Context, request runtime.ExecutionRequest) (runtime.ExecutionResult, error) {
		called = true
		return runtime.ExecutionResult{Isolation: mcpIsolationResult(request, runtime.RuntimePromotionBlocked)}, nil
	})
	srv, err := NewServer(Config{
		RepoRoot: root,
		RuntimeObservationProvider: RuntimeObservationProviderFunc(func(_ context.Context, request RuntimeObservationRequest) (*semantic.RuntimeObservationV2, error) {
			return fixture.observation, nil
		}),
		RuntimeExecutor: executor,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	ctx := context.Background()
	consent := mcpRuntimeConsent(head.SnapshotID, head.RootTreeID, "actor-vs06")
	args := map[string]any{
		"target": root, "query": fixture.incidentQuery, "semanticMap": fixture.mapIR, "proof": fixture.proof,
		"runtimeConsent": consent,
	}

	// An unapproved trusted_local observation is blocked before the executor is
	// reached, so a fabricated caller result cannot become evidence.
	unapproved := *fixture.observation
	unapproved.TrustedLocalApproval = &semantic.TrustedLocalApproval{Approved: false, ApprovedBy: "actor-vs06", Timestamp: time.Now().UTC().Format(time.RFC3339Nano)}
	fixture.observation = &unapproved
	res, err := srv.executeTool(ctx, "investigate_failure", args)
	if err != nil {
		t.Fatalf("unapproved trusted_local returned execution error: %v", err)
	}
	doc, ok := res.(map[string]any)
	if !ok || doc["code"] != "blocked" || doc["status"] != "blocked" || doc["corroborated"] != false {
		t.Fatalf("unapproved trusted_local was not blocked: %#v", res)
	}
	if called {
		t.Fatal("executor ran for an unapproved trusted_local observation")
	}
	if doc["command"] == nil || doc["accessScope"] == nil || doc["isolationScope"] == nil {
		t.Fatalf("unapproved trusted_local result omitted approved command/scope disclosure: %#v", doc)
	}

	// Approval permits the one-shot call, but the executor-owned isolation
	// result still blocks promotion when EvidencePromotion is not eligible.
	approved := *fixture.observation
	approved.TrustedLocalApproval = &semantic.TrustedLocalApproval{Approved: true, ApprovedBy: "actor-vs06", Timestamp: time.Now().UTC().Format(time.RFC3339Nano)}
	fixture.observation = &approved
	res, err = srv.executeTool(ctx, "investigate_failure", args)
	if err != nil {
		t.Fatalf("ineligible trusted_local returned execution error: %v", err)
	}
	doc, ok = res.(map[string]any)
	if !ok || doc["code"] != "blocked" || doc["status"] != "blocked" || doc["corroborated"] != false {
		t.Fatalf("ineligible promotion was not blocked: %#v", res)
	}
	if !called {
		t.Fatal("executor was not called after approved trusted_local observation")
	}
	if doc["runtimeIsolation"] == nil || doc["command"] == nil || doc["accessScope"] == nil || doc["isolationScope"] == nil {
		t.Fatalf("blocked result omitted disclosed runtime scope: %#v", doc)
	}
}

func TestMCPFailureV2RejectsCallerObservationAndRedactsEgress(t *testing.T) {
	root := t.TempDir()
	fixture := newMCPFailureFixture("snapshot-vs06")
	fixture.mapIR.Evidence[2].DocumentRevisionID = "revision:token=sensitive-value-123"
	srv, err := NewServer(Config{RepoRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	args := map[string]any{
		"target": root, "query": fixture.debugQuery, "semanticMap": fixture.mapIR, "proof": fixture.proof,
		"runtimeObservation": fixture.observation,
	}
	res, err := srv.executeTool(context.Background(), "investigate_failure", args)
	if err != nil {
		t.Fatalf("caller observation rejection returned execution error: %v", err)
	}
	doc, ok := res.(map[string]any)
	if !ok || doc["code"] != "invalid_precondition" || doc["corroborated"] != false {
		t.Fatalf("caller-fabricated observation was not rejected: %#v", res)
	}

	delete(args, "runtimeObservation")
	res, err = srv.executeTool(context.Background(), "investigate_failure", args)
	if err != nil {
		t.Fatalf("redaction investigation returned execution error: %v", err)
	}
	trace, ok := res.(*semantic.FailurePathTraceV2)
	if !ok {
		t.Fatalf("expected trace after removing caller observation, got %#v", res)
	}
	raw, err := marshalPublicMCPResult(trace)
	if err != nil {
		t.Fatalf("public trace egress failed: %v", err)
	}
	if strings.Contains(string(raw), "sensitive-value-123") {
		t.Fatalf("sensitive value escaped MCP egress redaction: %s", raw)
	}
	if err := contractharness.ValidateFailurePathTraceV2(raw); err != nil {
		t.Fatalf("redacted public trace failed contract validation: %v", err)
	}
}

func TestMCPFailureV2StatusRedactsAndBoundsWrappedDiagnostics(t *testing.T) {
	secretValue := strings.Repeat("x", 700)
	status := failureStatus("unknown", "provider failed", fmt.Errorf("provider detail token=%s", secretValue))
	detail, ok := status["detail"].(string)
	if !ok {
		t.Fatalf("failure status detail type = %T, want string", status["detail"])
	}
	if len(detail) > 512 {
		t.Fatalf("failure status detail length=%d, want <=512", len(detail))
	}
	if strings.Contains(detail, secretValue) || strings.Contains(detail, "token=") {
		t.Fatalf("failure status leaked wrapped secret diagnostic: %q", detail)
	}
}

func cloneMCPIncident(incident *semantic.FailureIncidentQuery) *semantic.FailureIncidentQuery {
	if incident == nil {
		return nil
	}
	copy := *incident
	return &copy
}

func mcpTrustedLocalObservation(query semantic.FailureQueryV2, approved bool) *semantic.RuntimeObservationV2 {
	incident := query.Incident
	return &semantic.RuntimeObservation{
		SchemaID: semantic.RuntimeObservationSchemaID, SchemaVersion: semantic.FailureContractSchemaVersion,
		ObservationID: incident.RuntimeObservationID, TraceID: incident.TraceID, IncidentEvidenceID: incident.IncidentEvidenceID,
		Scenario: incident.Scenario, Environment: incident.Environment, DependencyFingerprint: incident.DependencyFingerprint,
		ComputedBasisID: query.ComputedBasisID, GenerationID: query.GenerationID, ValidatedAgainstSnapshotID: query.ValidatedAgainstSnapshotID,
		Freshness: query.Freshness, ObservedAt: incident.TimeWindow.From, IsolationLevel: "trusted_local", TrustedLocalApproval: &semantic.TrustedLocalApproval{
			Approved: approved, ApprovedBy: "actor-vs06", Timestamp: incident.TimeWindow.From,
		}, TimeWindow: incident.TimeWindow, TraceCoverage: semantic.TraceCoverage{SpansCovered: 1, TotalSpans: 1, Ratio: 1},
		Events:   []semantic.RuntimeObservationEvent{{EventID: "event-failure", Timestamp: incident.TimeWindow.From, Kind: "failure_emission", Target: "Service.Fail", SymbolPath: "Service.Fail", Status: "observed", EvidenceRef: "ev-runtime"}},
		Evidence: []semantic.RuntimeEvidence{{EvidenceID: "ev-runtime", ObservationID: incident.RuntimeObservationID, TraceID: incident.TraceID, GenerationID: query.GenerationID, ComputedBasisID: query.ComputedBasisID, SnapshotID: query.ValidatedAgainstSnapshotID, ValidatedAgainstSnapshotID: query.ValidatedAgainstSnapshotID, Scenario: incident.Scenario, Environment: incident.Environment, DependencyFingerprint: incident.DependencyFingerprint, Timestamp: incident.TimeWindow.From, ValidationStatus: "verified", RedactionStatus: "clean"}},
	}
}

func mcpRuntimeConsent(snapshotID, treeDigest, actor string) runtime.RuntimeConsentV1 {
	issued := time.Now().UTC().Add(-time.Minute).Truncate(time.Second)
	command := "fixture-runtime"
	args := []string{"--read-only"}
	return runtime.RuntimeConsent{
		SchemaID: runtime.RuntimeConsentSchemaID, SchemaVersion: runtime.RuntimeSchemaVersion,
		ConsentID: "consent-vs06", ActorID: actor, ApprovedBy: actor, Approved: true,
		IssuedAt: issued.Format(time.RFC3339Nano), ExpiresAt: issued.Add(time.Hour).Format(time.RFC3339Nano),
		Command: command, Args: args, CommandDigest: runtime.CommandDigest(command, args),
		AccessScope:    runtime.RuntimeAccessScope{Source: "immutable_snapshot", Network: "none", Credentials: "none"},
		IsolationScope: runtime.RuntimeIsolationScope{Level: "trusted_local", SourceMount: "immutable_snapshot", SourcePermission: "read_only", WorkingDirectory: "isolated", WritableLayer: "disposable", RepositoryPathExposed: false},
		SnapshotID:     snapshotID, SnapshotTreeDigest: treeDigest, Nonce: "nonce-vs06-123456",
	}
}

func mcpIsolationResult(request runtime.ExecutionRequest, promotion string) runtime.RuntimeIsolationResult {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	audit := workspace.SourceWriteAudit{CapturedSnapshotTreeDigest: request.Snapshot.RootTreeID}
	return runtime.RuntimeIsolationResult{
		SchemaID: runtime.RuntimeIsolationResultSchemaID, SchemaVersion: runtime.RuntimeSchemaVersion,
		ResultAuthority: "runtime_executor", ProcessObserved: true, ExecutionID: "execution-vs06", ConsentID: request.Consent.ConsentID,
		ActorID: request.Consent.ActorID, Nonce: request.Nonce, Status: runtime.RuntimeTerminalSuccess, ResultCode: "ok",
		Command: request.Consent.Command, Args: request.Consent.Args, CommandDigest: runtime.CommandDigest(request.Consent.Command, request.Consent.Args),
		SnapshotID: request.Snapshot.SnapshotID, SnapshotTreeDigest: request.Snapshot.RootTreeID, RecomputedTreeDigest: request.Snapshot.RootTreeID, InputTreeVerified: true,
		AccessScope: request.Consent.AccessScope, IsolationScope: request.Consent.IsolationScope,
		MountPermissionEvidence: evidence.MountPermissionEvidence{SourceDelivery: "protocol_snapshot_bytes", SourceMount: "not_mounted", WorkingDirectoryMode: "process_private_disposable", WorkingDirectoryPermission: "0700", ReadOnlySource: true, Disposable: true, RepositoryPathExposed: false, DependencyEnvironmentPreserved: true, CleanupVerified: true},
		Cleanup:                 runtime.RuntimeCleanupEvidence{LayerCreated: true, LayerDisposed: true, Verified: true}, SourceWriteAuditStatus: runtime.RuntimeAuditClean, RepositoryPathWriteAudit: audit,
		SourceIntegrityStatus: runtime.RuntimeAuditClean, EvidencePromotion: promotion, PromotionBlockedReason: func() string {
			if promotion == runtime.RuntimePromotionBlocked {
				return "fixture intentionally blocks promotion"
			}
			return ""
		}(),
		ConcurrentWorktree: runtime.RuntimeWorktreeComparison{Classification: "unchanged"}, StartedAt: now, FinishedAt: now,
	}
}

func installMCPStaleCurrentProof(srv *Server, root string) (semantic.FailureQueryV2, error) {
	basis := strings.Repeat("a", 64)
	generation := "generation-current-vs06"
	computedSnapshot := "computed-snapshot-vs06"
	expectedLive := "expected-live-vs06"
	mapIR := &semantic.SemanticMapIR{
		SchemaID: semantic.SemanticMapSchemaID, SchemaVersion: semantic.SemanticSchemaVersion,
		MapID: "map-" + generation, GenerationID: generation, ComputedBasisID: basis, ValidatedAgainstSnapshotID: computedSnapshot,
		PublicationKind: "initial", Freshness: "historical", Settlement: "pending", EnrichmentStatus: "not_requested", Authority: "candidate",
		Quality: semantic.MapQuality{Stage: "Q1", UnresolvedCriticalCount: 0, ConflictingCriticalCount: 0}, Task: semantic.MapTaskContext{TaskID: "task-vs06", IntentRevision: 1, Mode: "feature"},
		Basis:   semantic.MapBasisContext{RepositoryID: "repo-vs06", WorktreeID: "worktree-vs06", WorkspaceEpoch: 1, ComputedWorkspaceSnapshotID: computedSnapshot, ComputedBasisID: basis, SnapshotTreeID: "tree-vs06", DependencyFingerprint: "deps-vs06", AnalysisReadSetID: "readset-vs06", CausalObservationClosureID: "closure-vs06"},
		Summary: semantic.MapSummary{Requested: "current proof", Current: "candidate"}, Steps: []semantic.SemanticStep{}, Edges: []semantic.SemanticEdge{}, Unknowns: []fusion.Unknown{}, Coverage: &semantic.CoverageBoundary{IncludedSourceRoots: []string{"."}, ExcludedReasons: []string{}},
	}
	mapData, err := json.Marshal(mapIR)
	if err != nil {
		return semantic.FailureQueryV2{}, err
	}
	if err := contractharness.ValidateSemanticMapIR(mapData); err != nil {
		return semantic.FailureQueryV2{}, fmt.Errorf("map fixture: %w", err)
	}
	readSet, closure, result, capabilityDigest := mcpCurrentAnalysisArtifacts(basis, computedSnapshot)
	projection := map[string]any{
		"schemaId": semantic.FlowViewProjectionSchemaID, "schemaVersion": 2, "projectionId": "projection-" + generation,
		"generationId": generation, "computedBasisId": basis, "mode": "feature",
		"displayBudget":   map[string]any{"targetMin": 1, "targetMax": 1, "enforcement": "soft"},
		"visibleStepRefs": []string{}, "preservedStepRefs": []string{}, "unknownBoundaryRefs": []string{}, "foldedSubflows": []any{},
	}
	readSetData, _ := json.Marshal(readSet)
	closureData, _ := json.Marshal(closure)
	resultData, _ := json.Marshal(result)
	projectionData, _ := json.Marshal(projection)
	mapRef := storage.ArtifactCASRef(mapData)
	projectionRef := storage.ArtifactCASRef(projectionData)
	readSetRef := storage.ArtifactCASRef(readSetData)
	closureRef := storage.ArtifactCASRef(closureData)
	resultRef := storage.ArtifactCASRef(resultData)
	manifest := &storage.GenerationProofManifest{
		SchemaID: semantic.GenerationProofSchemaID, SchemaVersion: semantic.SemanticSchemaVersion, ProofID: "proof-" + generation,
		GenerationID: generation, ComputedBasisID: basis, ComputedSnapshotID: computedSnapshot, ValidatedAgainstSnapshotID: expectedLive,
		TaskIntentRevision: 1, NormalizedQueryHash: strings.Repeat("b", 64), AnalysisReadSetID: "readset-vs06", CausalObservationClosureID: "closure-vs06", CausalObservationClosureDigest: strings.Repeat("c", 64), CapabilityProfileDigest: capabilityDigest, WorkspaceEpoch: 1,
		CurrentPublication: storage.CurrentPublicationResult{Eligibility: "passed", SnapshotGate: "passed", ClosureGate: "passed", EvidenceGate: "passed", SemanticAtomicityGate: "passed", TaskRelevanceGate: "passed", ComprehensionGate: "passed"}, SettlementEvaluation: storage.SettlementEvaluation{Gate: "pending", BlockingObligationRefs: []string{}},
		ArtifactRefs: storage.ArtifactRefs{SemanticMap: mapRef, Projection: projectionRef, AnalysisReadSet: readSetRef, ObservationClosure: closureRef, AnalyzerResult: resultRef}, ExpectedLiveHeadSnapshotID: expectedLive, ExpectedPreviousGenerationID: nil, PublishedAt: time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC),
	}
	ptr := &storage.ActivePointer{SchemaID: semantic.ActivePointerSchemaID, SchemaVersion: semantic.SemanticSchemaVersion, GenerationID: generation, ComputedBasisID: basis, ValidatedAgainstSnapshotID: expectedLive, ExpectedLiveHeadSnapshotID: expectedLive, ExpectedPreviousGenerationID: nil, WorkspaceEpoch: 1, TaskIntentRevision: 1, NormalizedQueryHash: strings.Repeat("b", 64), FlowCount: 1, RepositoryID: "repo-vs06", WorktreeID: "worktree-vs06", TaskID: "task-vs06", PublishedAt: manifest.PublishedAt}
	eventBasis, eventSnapshot, eventGeneration := basis, expectedLive, generation
	eventData, _ := json.Marshal(map[string]any{"schemaId": semantic.EventEnvelopeSchemaID, "schemaVersion": semantic.SemanticSchemaVersion, "streamId": "flowview-live-stream", "sequence": 1, "eventId": "event-vs06", "eventType": "generation.published", "occurredAt": manifest.PublishedAt, "computedBasisId": eventBasis, "validatedAgainstSnapshotId": eventSnapshot, "generationId": eventGeneration})
	tx := storage.PublicationTransaction{Manifest: manifest, Pointer: ptr, Event: eventData, Artifacts: map[string][]byte{mapRef: mapData, projectionRef: projectionData, readSetRef: readSetData, closureRef: closureData, resultRef: resultData}, ExpectedLiveHeadSnapshotID: expectedLive, ActualLiveHeadSnapshotID: expectedLive, ExpectedPreviousGenerationID: "", LiveHeadCommit: func(_ string, commit func() error) error { return commit() }}
	st, err := srv.getStorage(root)
	if err != nil {
		return semantic.FailureQueryV2{}, err
	}
	if _, err := st.PublishGeneration(tx); err != nil {
		return semantic.FailureQueryV2{}, err
	}
	return semantic.FailureQueryV2{SchemaID: semantic.FailureQuerySchemaID, SchemaVersion: semantic.FailureContractSchemaVersion, Mode: "debug", ComputedBasisID: basis, GenerationID: generation, ValidatedAgainstSnapshotID: computedSnapshot, Freshness: "current", Debug: &semantic.FailureDebugQuery{FailureEvidenceID: "missing-evidence"}}, nil
}

func mcpCurrentAnalysisArtifacts(basis, snapshot string) (evidence.AnalysisReadSet, evidence.ObservationClosure, evidence.Result, string) {
	negative := []evidence.Observation{{Kind: "negative_lookup", Path: "test/missing.go", Measured: true}}
	membership := []evidence.Observation{{Kind: "membership", Path: "test", Measured: true}}
	frontier := []evidence.Observation{{Kind: "dependency_frontier", Path: "test/go.mod", Measured: true}}
	readSet := evidence.AnalysisReadSet{SchemaID: evidence.ReadSetSchemaID, SchemaVersion: evidence.SchemaVersion, ReadSetID: "readset-vs06", ComputedBasisID: basis, WorkspaceEpoch: 1, Documents: []evidence.ReadDocument{}, NegativeObservations: negative, MembershipObservations: membership, DependencyFrontiers: frontier}
	closure := evidence.ObservationClosure{SchemaID: evidence.ClosureSchemaID, SchemaVersion: evidence.SchemaVersion, ClosureID: "closure-vs06", AnalysisReadSetID: readSet.ReadSetID, ComputedBasisID: basis, WorkspaceEpoch: 1, Status: "closed", NegativeObservations: negative, MembershipObservations: membership, DependencyFrontiers: frontier, RequiredObservations: []string{"negative_lookup", "membership", "dependency_frontier"}, MeasuredObservations: []string{"negative_lookup", "membership", "dependency_frontier"}, ClosureDigest: strings.Repeat("c", 64)}
	capability := evidence.CapabilityProfile{Adapter: "fixture-adapter", AdapterVersion: "adapter-vs06", AnalyzerRevision: "analyzer-vs06", Features: []string{"snapshot_bytes"}}
	capabilityData, _ := json.Marshal(capability)
	capabilitySum := sha256.Sum256(capabilityData)
	capabilityDigest := hex.EncodeToString(capabilitySum[:])
	result := evidence.Result{SchemaID: evidence.AnalyzerResultSchemaID, SchemaVersion: evidence.SchemaVersion, RequestID: "request-vs06", Operation: "detect", AdapterVersion: capability.AdapterVersion, AnalyzerRevision: capability.AnalyzerRevision, WorkspaceEpoch: 1, ComputedBasisID: basis, SnapshotID: snapshot, SnapshotTreeDigest: "tree-vs06", DependencyFingerprint: "deps-vs06", ReadSet: readSet, Closure: closure, Capability: capability, Coverage: evidence.Coverage{IncludedSourceRoots: []string{"."}, Measured: true}, Diagnostics: []evidence.Diagnostic{}, Payload: json.RawMessage(`{"language":"go","confident":true}`)}
	return readSet, closure, result, capabilityDigest
}
