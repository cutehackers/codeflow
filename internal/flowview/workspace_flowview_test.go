package flowview

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codeflow/internal/semantic"
)

func TestFlowViewWorkspaceEndpoints(t *testing.T) {
	root, err := filepath.Abs("../../test/fixtures/nextjs-app-fixture")
	if err != nil {
		t.Fatal(err)
	}

	srv, err := NewServer(Config{
		RepoRoot: root,
		Port:     0,
	})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	// 1. GET /api/workspace/activity
	reqAct := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/workspace/activity?token="+srv.AuthToken(), nil)
	recAct := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(recAct, reqAct)

	if recAct.Code != http.StatusOK {
		t.Fatalf("expected 200 for activity, got %d: %s", recAct.Code, recAct.Body.String())
	}
	var actDoc map[string]any
	if err := json.Unmarshal(recAct.Body.Bytes(), &actDoc); err != nil {
		t.Fatalf("unmarshal activity response: %v", err)
	}
	if actDoc["activity"] != "idle" {
		t.Errorf("expected initial activity idle, got %v", actDoc["activity"])
	}

	// 2. POST /api/workspace/edit
	editBody, _ := json.Marshal(map[string]any{
		"path":            "app/page.tsx",
		"content":         "export default function Test() { return <p>Test</p>; }",
		"documentVersion": 1,
		"source":          "agent_transaction",
	})
	reqEdit := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/workspace/edit?token="+srv.AuthToken(), bytes.NewReader(editBody))
	recEdit := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(recEdit, reqEdit)

	if recEdit.Code != http.StatusOK {
		t.Fatalf("expected 200 for edit, got %d: %s", recEdit.Code, recEdit.Body.String())
	}
	var editDoc map[string]any
	if err := json.Unmarshal(recEdit.Body.Bytes(), &editDoc); err != nil {
		t.Fatalf("unmarshal edit response: %v", err)
	}
	if _, ok := editDoc["revision"]; !ok {
		t.Error("missing revision in edit response")
	}
	if _, ok := editDoc["snapshot"]; !ok {
		t.Error("missing snapshot in edit response")
	}

	// 3. GET /api/workspace/activity after edit -> activity should be editing
	reqAct2 := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/workspace/activity?token="+srv.AuthToken(), nil)
	recAct2 := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(recAct2, reqAct2)

	var actDoc2 map[string]any
	_ = json.Unmarshal(recAct2.Body.Bytes(), &actDoc2)
	if actDoc2["activity"] != "editing" {
		t.Errorf("expected activity editing after edit, got %v", actDoc2["activity"])
	}

	// 4. GET /api/workspace/proof
	reqProof := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/workspace/proof?token="+srv.AuthToken(), nil)
	recProof := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(recProof, reqProof)
	if recProof.Code != http.StatusOK {
		t.Fatalf("expected 200 for proof, got %d: %s", recProof.Code, recProof.Body.String())
	}

	// 5. GET /api/workspace/stream with lastEventId triggering snapshot_sync
	reqStream := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/workspace/stream?token="+srv.AuthToken()+"&lastEventId=ev-unknown", nil)
	recStream := httptest.NewRecorder()
	go func() {
		srv.httpServer.Handler.ServeHTTP(recStream, reqStream)
	}()

	// Give stream a moment to output snapshot_sync header and data
	time.Sleep(50 * time.Millisecond)
	if !strings.Contains(recStream.Body.String(), "snapshot_sync") {
		t.Logf("stream body: %s", recStream.Body.String())
	}
}

func TestFlowViewReviewEndpoint(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "codeflow-test-review-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	srv, err := NewServer(Config{
		RepoRoot: tmpDir,
		Port:     0,
	})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	// 1. Missing precondition (VS05-A2)
	reqMissing := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/task/review?token="+srv.AuthToken(), nil)
	recMissing := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(recMissing, reqMissing)
	if recMissing.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing params, got %d", recMissing.Code)
	}

	// 2. Seed maps
	baseMap := &semantic.SemanticMapIR{
		MapID:           "map-base",
		GenerationID:    "gen-base",
		ComputedBasisID: "basis-base",
		SchemaVersion:   1,
		Basis:           semantic.MapBasisContext{WorkspaceEpoch: 1},
		Steps: []semantic.SemanticStep{
			{
				StepID:        "step-login",
				Name:          "로그인",
				TechnicalName: "Auth.login",
				Rules:         []string{"AC-1"},
				EvidenceRefs:  []string{"ev-1"},
			},
		},
	}

	currMap := &semantic.SemanticMapIR{
		MapID:           "map-curr",
		GenerationID:    "gen-curr",
		ComputedBasisID: "basis-curr",
		SchemaVersion:   1,
		Basis:           semantic.MapBasisContext{WorkspaceEpoch: 1},
		Evidence: []semantic.SemanticEvidence{
			{EvidenceID: "ev-1", ValidationStatus: "verified"},
			{EvidenceID: "ev-2", ValidationStatus: "verified"},
		},
		Steps: []semantic.SemanticStep{
			{
				StepID:        "step-login",
				Name:          "로그인",
				TechnicalName: "Auth.login",
				Rules:         []string{"AC-1"},
				EvidenceRefs:  []string{"ev-1"},
			},
			{
				StepID:        "step-mfa",
				Name:          "2차 인증",
				TechnicalName: "Auth.mfa",
				Rules:         []string{"AC-2"},
				EvidenceRefs:  []string{"ev-2"},
			},
		},
	}

	incompatMap := &semantic.SemanticMapIR{
		MapID:           "map-incompat",
		GenerationID:    "gen-incompat",
		ComputedBasisID: "basis-incompat",
		SchemaVersion:   1,
		Basis:           semantic.MapBasisContext{WorkspaceEpoch: 999}, // Mismatch!
	}

	srv.mu.Lock()
	srv.mapCache["base"] = baseMap
	srv.mapCache["curr"] = currMap
	srv.mapCache["incompat"] = incompatMap
	srv.mu.Unlock()

	// 3. Incomparable basis (VS05-A1, A2)
	reqIncompat := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/task/review?baseline=base&current=incompat&token="+srv.AuthToken(), nil)
	recIncompat := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(recIncompat, reqIncompat)
	if recIncompat.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for epoch mismatch, got %d: %s", recIncompat.Code, recIncompat.Body.String())
	}

	// 4. Successful review query (VS05-A3, A5, A8)
	reqOK := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/task/review?baseline=base&current=curr&token="+srv.AuthToken(), nil)
	recOK := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(recOK, reqOK)
	if recOK.Code != http.StatusOK {
		t.Fatalf("expected 200 for review query, got %d: %s", recOK.Code, recOK.Body.String())
	}

	var resp struct {
		SemanticDelta        semantic.SemanticDeltaIR        `json:"semanticDelta"`
		RequirementAlignment []semantic.RequirementAlignment `json:"requirementAlignment"`
		ChangePulse          []map[string]any                `json:"changePulse"`
	}
	if err := json.Unmarshal(recOK.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal review response: %v", err)
	}

	if len(resp.SemanticDelta.Changes) != 1 {
		t.Errorf("expected 1 change (added step), got %d", len(resp.SemanticDelta.Changes))
	}
	if len(resp.RequirementAlignment) < 1 {
		t.Errorf("expected requirement alignments, got none")
	}
	if len(resp.ChangePulse) != 1 {
		t.Errorf("expected 1 change pulse item, got %d", len(resp.ChangePulse))
	}
}

func TestFlowViewImpactEndpoint(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "codeflow-test-impact-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	srv, err := NewServer(Config{
		RepoRoot: tmpDir,
		Port:     0,
	})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	// 1. Missing precondition (VS06-A2)
	reqMissing := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/task/impact?token="+srv.AuthToken(), nil)
	recMissing := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(recMissing, reqMissing)
	if recMissing.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing params, got %d", recMissing.Code)
	}

	// 2. Seed active map in cache
	activeMap := &semantic.SemanticMapIR{
		MapID:           "map-checkout",
		GenerationID:    "gen-100",
		ComputedBasisID: "basis-100",
		SchemaVersion:   1,
		Basis:           semantic.MapBasisContext{WorkspaceEpoch: 1},
		Coverage: &semantic.CoverageBoundary{
			IncludedSourceRoots: []string{"src"},
		},
		Steps: []semantic.SemanticStep{
			{
				StepID:        "step-checkout",
				Name:          "체크아웃",
				TechnicalName: "OrderService.checkout",
				Rules:         []string{"test:testCheckout"},
			},
		},
	}

	srv.mu.Lock()
	srv.mapCache["checkout"] = activeMap
	srv.mu.Unlock()

	// 3. Valid impact query (VS06-A1, A3, A7)
	reqOK := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/task/impact?symbolId=OrderService.checkout&token="+srv.AuthToken(), nil)
	recOK := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(recOK, reqOK)
	if recOK.Code != http.StatusOK {
		t.Fatalf("expected 200 for impact query, got %d: %s", recOK.Code, recOK.Body.String())
	}

	var graph semantic.ChangeImpactGraph
	if err := json.Unmarshal(recOK.Body.Bytes(), &graph); err != nil {
		t.Fatalf("unmarshal impact response: %v", err)
	}
	if graph.Target.SymbolID != "OrderService.checkout" {
		t.Errorf("expected symbolId OrderService.checkout, got %s", graph.Target.SymbolID)
	}
	if graph.Freshness != "current" {
		t.Errorf("expected current freshness, got %s", graph.Freshness)
	}
	if !graph.IndirectImpact.Bounded {
		t.Error("expected bounded indirect impact")
	}
}

func TestFlowViewFailureEndpoints(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "codeflow-test-failure-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	srv, err := NewServer(Config{
		RepoRoot: tmpDir,
		Port:     0,
	})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	// 1. Missing precondition on /api/task/debug
	reqMissingDebug := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/task/debug?token="+srv.AuthToken(), nil)
	recMissingDebug := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(recMissingDebug, reqMissingDebug)
	if recMissingDebug.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing debug params, got %d", recMissingDebug.Code)
	}

	// 2. Valid /api/task/debug
	reqOKDebug := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/task/debug?error=DbConnectionTimeout&token="+srv.AuthToken(), nil)
	recOKDebug := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(recOKDebug, reqOKDebug)
	if recOKDebug.Code != http.StatusOK {
		t.Fatalf("expected 200 for debug query, got %d: %s", recOKDebug.Code, recOKDebug.Body.String())
	}
	var debugTrace semantic.FailurePathTrace
	if err := json.Unmarshal(recOKDebug.Body.Bytes(), &debugTrace); err != nil {
		t.Fatalf("unmarshal debug trace: %v", err)
	}
	if debugTrace.Mode != "debug" {
		t.Errorf("expected debug mode, got %s", debugTrace.Mode)
	}

	// 3. Valid /api/task/incident
	reqOKInc := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/task/incident?traceId=trace-inc-10&token="+srv.AuthToken(), nil)
	recOKInc := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(recOKInc, reqOKInc)
	if recOKInc.Code != http.StatusOK {
		t.Fatalf("expected 200 for incident query, got %d: %s", recOKInc.Code, recOKInc.Body.String())
	}
	var incTrace semantic.FailurePathTrace
	if err := json.Unmarshal(recOKInc.Body.Bytes(), &incTrace); err != nil {
		t.Fatalf("unmarshal incident trace: %v", err)
	}
	if incTrace.Mode != "incident" {
		t.Errorf("expected incident mode, got %s", incTrace.Mode)
	}
	if len(incTrace.Timeline) == 0 {
		t.Error("expected incident timeline")
	}
}

func TestFlowViewApprovalEndpoints(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "codeflow-test-appr-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	srv, err := NewServer(Config{
		RepoRoot: tmpDir,
		Port:     0,
	})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	// 1. GET /api/semantic/evidence-pack
	reqEv := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/semantic/evidence-pack?symbolPath=PaymentService.process&token="+srv.AuthToken(), nil)
	recEv := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(recEv, reqEv)
	if recEv.Code != http.StatusOK {
		t.Fatalf("expected 200 for evidence-pack, got %d: %s", recEv.Code, recEv.Body.String())
	}
	var pack semantic.EvidencePack
	if err := json.Unmarshal(recEv.Body.Bytes(), &pack); err != nil {
		t.Fatalf("unmarshal evidence pack: %v", err)
	}
	if len(pack.Items) == 0 {
		t.Error("expected items in evidence pack")
	}

	// 2. POST /api/semantic/approve
	apprBody := `{"proposalId":"prop-checkout","decision":"approved","approver":"lead@company.corp"}`
	reqAppr := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/semantic/approve?token="+srv.AuthToken(), strings.NewReader(apprBody))
	recAppr := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(recAppr, reqAppr)
	if recAppr.Code != http.StatusOK {
		t.Fatalf("expected 200 for approve, got %d: %s", recAppr.Code, recAppr.Body.String())
	}
	var appr semantic.SemanticApproval
	if err := json.Unmarshal(recAppr.Body.Bytes(), &appr); err != nil {
		t.Fatalf("unmarshal approval response: %v", err)
	}
	if appr.Decision != "approved" {
		t.Errorf("expected decision approved, got %s", appr.Decision)
	}
	if appr.Approver != "lead@company.corp" {
		t.Errorf("expected approver lead@company.corp, got %s", appr.Approver)
	}
}

func TestFlowViewOnboardingEndpoint(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "codeflow-test-onb-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	srv, err := NewServer(Config{
		RepoRoot: tmpDir,
		Port:     0,
	})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	// GET /api/task/onboarding (VS09-A1, A2)
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/task/onboarding?repositoryId=shop-core&token="+srv.AuthToken(), nil)
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for onboarding, got %d: %s", rec.Code, rec.Body.String())
	}
	var ov semantic.DomainOverview
	if err := json.Unmarshal(rec.Body.Bytes(), &ov); err != nil {
		t.Fatalf("unmarshal domain overview: %v", err)
	}
	if ov.RepositoryID != "shop-core" {
		t.Errorf("expected repositoryId shop-core, got %s", ov.RepositoryID)
	}
	if len(ov.Domains) == 0 {
		t.Error("expected domains in overview")
	}
}

func TestFlowViewReleaseCapabilityEndpoint(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "codeflow-test-rel-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	srv, err := NewServer(Config{
		RepoRoot: tmpDir,
		Port:     0,
	})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	// GET /api/release/capability (VS10-A1, A2, A3)
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/release/capability?targetVersion=v0.9.0-rc1&token="+srv.AuthToken(), nil)
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for release capability, got %d: %s", rec.Code, rec.Body.String())
	}

	var res struct {
		BenchmarkReport semantic.ReleaseBenchmarkReport `json:"benchmarkReport"`
		SLMCapability   semantic.SLMCapabilityState     `json:"slmCapability"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("unmarshal release capability response: %v", err)
	}

	if !res.BenchmarkReport.ReleaseReady {
		t.Error("expected releaseReady true")
	}
	if res.SLMCapability.FallbackTier != "local_slm" {
		t.Errorf("expected fallbackTier local_slm, got %s", res.SLMCapability.FallbackTier)
	}
}






