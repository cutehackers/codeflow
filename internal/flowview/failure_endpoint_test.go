package flowview

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"codeflow/internal/rflscvs06"
	oneshootRuntime "codeflow/internal/runtime"
	"codeflow/internal/semantic"
	"codeflow/internal/slicing"
	"codeflow/internal/workspace"
)

func TestFailureEndpointRequiresExplicitV2Identity(t *testing.T) {
	srv := newFailureEndpointServer(t, nil)

	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/task/debug?error=Service.Fail&token="+srv.AuthToken(), nil)
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("legacy debug request must fail closed, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "validatedAgainstSnapshotId") {
		t.Fatalf("missing v2 identity error is not actionable: %s", rec.Body.String())
	}

	incident := "http://127.0.0.1/api/task/incident?computedBasisId=basis-failure&generationId=generation-failure&validatedAgainstSnapshotId=snapshot-failure&freshness=historical&traceId=trace-1&scenario=checkout&environment=test&dependencyFingerprint=deps-1&timeWindowFrom=2026-09-05T10:00:00Z&timeWindowTo=2026-09-05T10:00:10Z&token=" + srv.AuthToken()
	req = httptest.NewRequest(http.MethodGet, incident, nil)
	rec = httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)
	if rec.Code == http.StatusOK {
		t.Fatal("incident query without runtimeObservationId must not use caller trace authority")
	}
}

func TestFailureEndpointDebugUsesExactHistoricalMapAndReversePath(t *testing.T) {
	mapIR := failureEndpointMap()
	srv := newFailureEndpointServer(t, mapIR)

	url := "http://127.0.0.1/api/task/debug?computedBasisId=" + mapIR.ComputedBasisID + "&generationId=" + mapIR.GenerationID + "&validatedAgainstSnapshotId=" + mapIR.ValidatedAgainstSnapshotID + "&freshness=historical&failureEvidenceId=ev-failure&token=" + srv.AuthToken()
	req := httptest.NewRequest(http.MethodGet, url, nil)
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("historical debug query failed: %d: %s", rec.Code, rec.Body.String())
	}
	var trace semantic.FailurePathTraceV2
	if err := json.Unmarshal(rec.Body.Bytes(), &trace); err != nil {
		t.Fatalf("decode debug trace: %v", err)
	}
	if trace.SchemaID != semantic.FailurePathTraceSchemaID || trace.Mode != "debug" {
		t.Fatalf("unexpected debug trace identity: %+v", trace)
	}
	if len(trace.Nodes) != 3 || trace.Nodes[0].SymbolPath != "Service.Fail" {
		t.Fatalf("expected canonical reverse path from failure node, got %+v", trace.Nodes)
	}
	if len(trace.Relationships) != 2 || trace.Relationships[0].FromNodeID != "node-step-handler" || trace.Relationships[0].ToNodeID != "node-step-failure" || trace.Relationships[1].FromNodeID != "node-step-root" || trace.Relationships[1].ToNodeID != "node-step-handler" {
		t.Fatalf("expected evidence-backed reverse relationships, got %+v", trace.Relationships)
	}
	if len(trace.Timeline) != 0 {
		t.Fatal("debug trace must not fabricate a runtime timeline")
	}
}

func TestFailureEndpointIncidentUsesScopedServerObservation(t *testing.T) {
	mapIR := failureEndpointMap()
	observation := failureEndpointObservation(mapIR, "sandboxed", true)
	var request RuntimeObservationRequest
	srv := newFailureEndpointServer(t, mapIR)
	srv.runtimeObservationProvider = RuntimeObservationProviderFunc(func(_ context.Context, in RuntimeObservationRequest) (*semantic.RuntimeObservationV2, error) {
		request = in
		return observation, nil
	})

	url := "http://127.0.0.1/api/task/incident?computedBasisId=" + mapIR.ComputedBasisID + "&generationId=" + mapIR.GenerationID + "&validatedAgainstSnapshotId=" + mapIR.ValidatedAgainstSnapshotID + "&freshness=historical&traceId=trace-failure&runtimeObservationId=obs-failure&scenario=checkout&environment=prod&dependencyFingerprint=deps-failure&timeWindowFrom=2026-09-05T10:00:00Z&timeWindowTo=2026-09-05T10:00:10Z&token=" + srv.AuthToken()
	req := httptest.NewRequest(http.MethodGet, url, nil)
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("scoped incident query failed: %d: %s", rec.Code, rec.Body.String())
	}
	var response map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode incident response: %v", err)
	}
	if request.ObservationID != "obs-failure" || request.Query.Incident == nil || request.Query.Incident.DependencyFingerprint != "deps-failure" {
		t.Fatalf("provider did not receive server-scoped observation request: %+v", request)
	}
	if _, ok := response["runtimeObservation"]; !ok {
		t.Fatal("incident response omitted the redacted, validated observation descriptor")
	}
	if response["runtimeObservation"].(map[string]any)["input"] != "***REDACTED***" {
		t.Fatalf("runtime observation input was not redacted: %+v", response["runtimeObservation"])
	}
	var trace semantic.FailurePathTraceV2
	if err := json.Unmarshal(rec.Body.Bytes(), &trace); err != nil {
		t.Fatalf("decode trace from incident response: %v", err)
	}
	if len(trace.Timeline) != 1 || trace.RuntimeObservationRef != "obs-failure" || trace.Nodes[0].Status != "corroborated" {
		t.Fatalf("incident runtime correlation did not use supplied observation: %+v", trace)
	}
}

func TestFailureEndpointNoObservationNeverUsesNilOrCallerPayload(t *testing.T) {
	mapIR := failureEndpointMap()
	srv := newFailureEndpointServer(t, mapIR)
	srv.runtimeObservationStore = RuntimeObservationStoreFunc(func(context.Context, string) (*semantic.RuntimeObservationV2, error) {
		return nil, nil
	})
	body := map[string]any{
		"query": map[string]any{
			"schemaId": semantic.FailureQuerySchemaID, "schemaVersion": 2, "mode": "incident",
			"computedBasisId": mapIR.ComputedBasisID, "generationId": mapIR.GenerationID,
			"validatedAgainstSnapshotId": mapIR.ValidatedAgainstSnapshotID, "freshness": "historical",
			"incident": map[string]any{"runtimeObservationId": "obs-missing", "traceId": "trace-failure", "scenario": "checkout", "environment": "prod", "dependencyFingerprint": "deps-failure", "timeWindow": map[string]string{"from": failureEndpointWindowFrom, "to": failureEndpointWindowTo}},
		},
		"runtimeObservation": failureEndpointObservation(mapIR, "sandboxed", true),
	}
	data, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/task/incident?token="+srv.AuthToken(), bytes.NewReader(data))
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("caller-supplied observation must be rejected, got %d: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "apiKey") {
		t.Fatal("caller-supplied runtime observation leaked into an error")
	}
}

func TestFailureEndpointBlockedObservationDisclosesIsolationState(t *testing.T) {
	mapIR := failureEndpointMap()
	observation := failureEndpointObservation(mapIR, "blocked", false)
	srv := newFailureEndpointServer(t, mapIR)
	srv.runtimeObservationProvider = RuntimeObservationProviderFunc(func(context.Context, RuntimeObservationRequest) (*semantic.RuntimeObservationV2, error) {
		return observation, nil
	})
	req := httptest.NewRequest(http.MethodGet, failureEndpointIncidentURL(srv, mapIR, observation.ObservationID), nil)
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("blocked observation must return forbidden, got %d: %s", rec.Code, rec.Body.String())
	}
	var blocked map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &blocked); err != nil {
		t.Fatalf("decode blocked observation response: %v", err)
	}
	if blocked["runtimeObservationId"] != observation.ObservationID || blocked["isolationLevel"] != "blocked" || blocked["corroborated"] != false {
		t.Fatalf("blocked observation omitted trusted isolation disclosure: %#v", blocked)
	}
}

func TestFailureEndpointCurrentRequiresValidatedProofAndLiveHead(t *testing.T) {
	mapIR := failureEndpointMap()
	srv := newFailureEndpointServer(t, mapIR)
	url := "http://127.0.0.1/api/task/debug?computedBasisId=" + mapIR.ComputedBasisID + "&generationId=" + mapIR.GenerationID + "&validatedAgainstSnapshotId=" + mapIR.ValidatedAgainstSnapshotID + "&freshness=current&failureEvidenceId=ev-failure&token=" + srv.AuthToken()
	req := httptest.NewRequest(http.MethodGet, url, nil)
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("current request without proof must fail closed, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "current_proof_unavailable") {
		t.Fatalf("current failure did not identify the proof requirement: %s", rec.Body.String())
	}
}

func TestFailureEndpointTrustedLocalRequiresConsentAndPromotion(t *testing.T) {
	mapIR := failureEndpointMap()
	observation := failureEndpointObservation(mapIR, "trusted_local", false)
	var calls atomic.Int32
	srv := newFailureEndpointServer(t, mapIR)
	srv.runtimeObservationProvider = RuntimeObservationProviderFunc(func(context.Context, RuntimeObservationRequest) (*semantic.RuntimeObservationV2, error) {
		return observation, nil
	})
	srv.runtimeExecutor = RuntimeOneShotExecutorFunc(func(context.Context, oneshootRuntime.ExecutionRequest) (oneshootRuntime.ExecutionResult, error) {
		calls.Add(1)
		return oneshootRuntime.ExecutionResult{}, nil
	})
	url := failureEndpointIncidentURL(srv, mapIR, "obs-failure")
	req := httptest.NewRequest(http.MethodGet, url, nil)
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden || calls.Load() != 0 {
		t.Fatalf("unapproved trusted_local observation must block before execution, got %d calls=%d: %s", rec.Code, calls.Load(), rec.Body.String())
	}

	approved := failureEndpointObservation(mapIR, "trusted_local", true)
	srv.runtimeObservationProvider = RuntimeObservationProviderFunc(func(context.Context, RuntimeObservationRequest) (*semantic.RuntimeObservationV2, error) {
		return approved, nil
	})
	snapshot, err := srv.engine.Reconcile(context.Background(), nil)
	if err != nil {
		t.Fatalf("seed runtime snapshot: %v", err)
	}
	mapIR.ValidatedAgainstSnapshotID = snapshot.SnapshotID
	mapIR.Basis.ComputedWorkspaceSnapshotID = snapshot.SnapshotID
	mapIR.Basis.SnapshotTreeID = snapshot.RootTreeID
	for i := range mapIR.Evidence {
		mapIR.Evidence[i].SnapshotID = snapshot.SnapshotID
	}
	srv.mu.Lock()
	srv.mapCache[mapIR.GenerationID] = mapIR
	srv.mapCache[mapIR.ComputedBasisID] = mapIR
	srv.mu.Unlock()
	approved = failureEndpointObservation(mapIR, "trusted_local", true)
	srv.runtimeObservationProvider = RuntimeObservationProviderFunc(func(context.Context, RuntimeObservationRequest) (*semantic.RuntimeObservationV2, error) {
		return approved, nil
	})
	consent := failureEndpointConsent(snapshot)
	body := map[string]any{
		"query":          failureEndpointIncidentQuery(mapIR, "obs-failure"),
		"runtimeConsent": consent,
		"runtime":        map[string]any{"executionId": "exec-failure", "operation": "detect"},
	}
	data, _ := json.Marshal(body)
	req = httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/task/incident?token="+srv.AuthToken(), bytes.NewReader(data))
	rec = httptest.NewRecorder()
	srv.runtimeExecutor = RuntimeOneShotExecutorFunc(func(context.Context, oneshootRuntime.ExecutionRequest) (oneshootRuntime.ExecutionResult, error) {
		calls.Add(1)
		return oneshootRuntime.ExecutionResult{Isolation: rflscvs06.RuntimeIsolationResult{EvidencePromotion: rflscvs06.RuntimePromotionBlocked}}, nil
	})
	srv.httpServer.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden || calls.Load() != 1 {
		t.Fatalf("audit-blocked isolation must not correlate runtime Evidence, got %d calls=%d: %s", rec.Code, calls.Load(), rec.Body.String())
	}
	var blocked map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &blocked); err != nil {
		t.Fatalf("decode blocked trusted_local response: %v", err)
	}
	for _, field := range []string{"command", "args", "accessScope", "isolationScope"} {
		if blocked[field] == nil {
			t.Fatalf("blocked trusted_local response omitted %s disclosure: %#v", field, blocked)
		}
	}
}

func TestFlowViewFailureUIUsesExplicitIdentityScopeAndRuntimeStates(t *testing.T) {
	for _, required := range []string{
		"failure-symptom-input", "failure-evidence-input", "failure-observation-input",
		"failure-basis-id", "failure-generation-id", "failure-snapshot-id", "failure-freshness",
		"failure-scenario-input", "failure-environment-input", "failure-dependency-input",
		"failure-window-from-input", "failure-window-to-input", "failure-command-state",
		"failure-access-state", "failure-isolation-state", "failure-promotion-state", "failure-integrity-state",
		"failure-unknown-state", "failure-conflict-state", "validatedAgainstSnapshotId", "runtimeObservationId",
	} {
		if !strings.Contains(IndexHTML, required) {
			t.Errorf("embedded failure UI is missing %q", required)
		}
	}
	for _, forbidden := range []string{"trace-inc-default", "CardDeclinedException", "|| 'trace-inc-default'", "|| 'CardDeclinedException'"} {
		if strings.Contains(IndexHTML, forbidden) {
			t.Errorf("embedded failure UI contains a synthetic fallback %q", forbidden)
		}
	}
}

const (
	failureEndpointWindowFrom = "2026-09-05T10:00:00Z"
	failureEndpointWindowTo   = "2026-09-05T10:00:10Z"
)

func newFailureEndpointServer(t *testing.T, mapIR *semantic.SemanticMapIR) *Server {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(root+"/service.go", []byte("package service\nfunc Start() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv, err := NewServer(Config{RepoRoot: root, Port: 0})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	t.Cleanup(func() { _ = srv.Shutdown(context.Background()) })
	if mapIR != nil {
		srv.mu.Lock()
		srv.mapCache[mapIR.GenerationID] = mapIR
		srv.mapCache[mapIR.ComputedBasisID] = mapIR
		srv.mu.Unlock()
	}
	return srv
}

func failureEndpointMap() *semantic.SemanticMapIR {
	anchor := func(path, symbol string) slicing.Anchor {
		return slicing.Anchor{RepoRelativePath: path, ByteRange: [2]int{1, 8}, FileHash: "file-" + strings.ReplaceAll(path, "/", "-"), SpanHash: "span-" + strings.ReplaceAll(symbol, ".", "-"), EnclosingSymbolPath: symbol, CanonicalAstFingerprint: "ast-" + strings.ReplaceAll(symbol, ".", "-")}
	}
	return &semantic.SemanticMapIR{
		SchemaID: semantic.SemanticMapSchemaID, SchemaVersion: semantic.SemanticSchemaVersion,
		MapID: "map-failure", GenerationID: "generation-failure", ComputedBasisID: "basis-failure", ValidatedAgainstSnapshotID: "snapshot-failure", Freshness: "historical", Authority: "candidate",
		Task:     semantic.MapTaskContext{TaskID: "task-failure", IntentRevision: 1, Mode: "feature"},
		Basis:    semantic.MapBasisContext{ComputedWorkspaceSnapshotID: "snapshot-failure", ComputedBasisID: "basis-failure", SnapshotTreeID: "tree-failure", WorkspaceEpoch: 1},
		Coverage: &semantic.CoverageBoundary{IncludedSourceRoots: []string{"src"}, ExcludedReasons: []string{}},
		Steps: []semantic.SemanticStep{
			{StepID: "step-root", Name: "Service.Start", TechnicalName: "Service.Start", Kind: "entry", Anchor: anchor("src/start.go", "Service.Start"), EvidenceRefs: []string{"ev-root"}},
			{StepID: "step-handler", Name: "Service.Handle", TechnicalName: "Service.Handle", Kind: "transform", Anchor: anchor("src/handle.go", "Service.Handle"), EvidenceRefs: []string{"ev-handler"}},
			{StepID: "step-failure", Name: "Service.Fail", TechnicalName: "Service.Fail", Kind: "failure", Anchor: anchor("src/fail.go", "Service.Fail"), EvidenceRefs: []string{"ev-failure"}},
		},
		Edges: []semantic.SemanticEdge{
			{FromStepID: "step-root", ToStepID: "step-handler", ToSymbolPath: "Service.Handle", Kind: "calls", ResolutionStatus: "verified"},
			{FromStepID: "step-handler", ToStepID: "step-failure", ToSymbolPath: "Service.Fail", Kind: "throws", ResolutionStatus: "verified"},
		},
		Evidence: []semantic.SemanticEvidence{
			{EvidenceID: "ev-root", Kind: "source", SourceAuthority: "code", ComputedBasisID: "basis-failure", SnapshotID: "snapshot-failure", ValidationStatus: "verified", RedactionStatus: "clean", Anchor: anchor("src/start.go", "Service.Start"), ByteRange: [2]int{1, 8}, LineRange: [2]int{1, 2}},
			{EvidenceID: "ev-handler", Kind: "source", SourceAuthority: "code", ComputedBasisID: "basis-failure", SnapshotID: "snapshot-failure", ValidationStatus: "verified", RedactionStatus: "clean", Anchor: anchor("src/handle.go", "Service.Handle"), ByteRange: [2]int{1, 8}, LineRange: [2]int{1, 2}},
			{EvidenceID: "ev-failure", Kind: "source", SourceAuthority: "code", ComputedBasisID: "basis-failure", SnapshotID: "snapshot-failure", ValidationStatus: "verified", RedactionStatus: "clean", Anchor: anchor("src/fail.go", "Service.Fail"), ByteRange: [2]int{1, 8}, LineRange: [2]int{1, 2}},
		},
	}
}

func failureEndpointObservation(mapIR *semantic.SemanticMapIR, isolation string, approved bool) *semantic.RuntimeObservationV2 {
	approval := (*semantic.TrustedLocalApproval)(nil)
	if isolation == "trusted_local" {
		approval = &semantic.TrustedLocalApproval{Approved: approved, ApprovedBy: "actor-failure", Timestamp: failureEndpointWindowFrom}
	}
	return &semantic.RuntimeObservation{
		SchemaID: semantic.RuntimeObservationSchemaID, SchemaVersion: semantic.FailureContractSchemaVersion,
		ObservationID: "obs-failure", TraceID: "trace-failure", IncidentEvidenceID: "incident-failure", Scenario: "checkout", Environment: "prod", DependencyFingerprint: "deps-failure",
		ComputedBasisID: mapIR.ComputedBasisID, GenerationID: mapIR.GenerationID, ValidatedAgainstSnapshotID: mapIR.ValidatedAgainstSnapshotID, Freshness: "historical", ObservedAt: "2026-09-05T10:00:02Z", IsolationLevel: isolation, TrustedLocalApproval: approval, Input: "apiKey=secret",
		TimeWindow: semantic.FailureTimeWindow{From: failureEndpointWindowFrom, To: failureEndpointWindowTo}, TraceCoverage: semantic.TraceCoverage{SpansCovered: 1, TotalSpans: 1, Ratio: 1},
		Events:   []semantic.RuntimeObservationEvent{{EventID: "event-failure", Timestamp: "2026-09-05T10:00:03Z", Kind: "failure_emission", Target: "Service.Fail", Status: "observed", EvidenceRef: "run-failure"}},
		Evidence: []semantic.RuntimeEvidence{{EvidenceID: "run-failure", ObservationID: "obs-failure", TraceID: "trace-failure", GenerationID: mapIR.GenerationID, ComputedBasisID: mapIR.ComputedBasisID, SnapshotID: mapIR.ValidatedAgainstSnapshotID, ValidatedAgainstSnapshotID: mapIR.ValidatedAgainstSnapshotID, Scenario: "checkout", Environment: "prod", DependencyFingerprint: "deps-failure", Timestamp: "2026-09-05T10:00:03Z", ValidationStatus: "verified", RedactionStatus: "clean"}},
	}
}

func failureEndpointIncidentURL(srv *Server, mapIR *semantic.SemanticMapIR, observationID string) string {
	return "http://127.0.0.1/api/task/incident?computedBasisId=" + mapIR.ComputedBasisID + "&generationId=" + mapIR.GenerationID + "&validatedAgainstSnapshotId=" + mapIR.ValidatedAgainstSnapshotID + "&freshness=historical&traceId=trace-failure&runtimeObservationId=" + observationID + "&scenario=checkout&environment=prod&dependencyFingerprint=deps-failure&timeWindowFrom=" + failureEndpointWindowFrom + "&timeWindowTo=" + failureEndpointWindowTo + "&token=" + srv.AuthToken()
}

func failureEndpointIncidentQuery(mapIR *semantic.SemanticMapIR, observationID string) map[string]any {
	return map[string]any{
		"schemaId": semantic.FailureQuerySchemaID, "schemaVersion": 2, "mode": "incident", "computedBasisId": mapIR.ComputedBasisID, "generationId": mapIR.GenerationID, "validatedAgainstSnapshotId": mapIR.ValidatedAgainstSnapshotID, "freshness": "historical",
		"incident": map[string]any{"traceId": "trace-failure", "runtimeObservationId": observationID, "scenario": "checkout", "environment": "prod", "dependencyFingerprint": "deps-failure", "timeWindow": map[string]string{"from": failureEndpointWindowFrom, "to": failureEndpointWindowTo}},
	}
}

func failureEndpointConsent(snapshot *workspace.WorkspaceSnapshot) rflscvs06.RuntimeConsent {
	return rflscvs06.RuntimeConsent{
		SchemaID: rflscvs06.RuntimeConsentSchemaID, SchemaVersion: rflscvs06.RuntimeSchemaVersion,
		ConsentID: "consent-failure", ActorID: "actor-failure", ApprovedBy: "actor-failure", Approved: true,
		IssuedAt: "2000-01-01T00:00:00Z", ExpiresAt: "2999-09-05T11:00:00Z", Command: "codeflow", Args: []string{"analyze"},
		CommandDigest:  rflscvs06.CommandDigest("codeflow", []string{"analyze"}),
		AccessScope:    rflscvs06.RuntimeAccessScope{Source: "immutable_snapshot", Network: "disabled", Credentials: "not_available"},
		IsolationScope: rflscvs06.RuntimeIsolationScope{Level: "trusted_local", SourceMount: "not_mounted", SourcePermission: "read_only_protocol", WorkingDirectory: "process_private_disposable", WritableLayer: "discarded_after_terminal", RepositoryPathExposed: false},
		SnapshotID:     snapshot.SnapshotID, SnapshotTreeDigest: snapshot.RootTreeID, Nonce: "nonce-failure-0001",
	}
}
