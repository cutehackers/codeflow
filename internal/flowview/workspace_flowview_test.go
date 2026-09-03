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

