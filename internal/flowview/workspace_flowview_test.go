package flowview

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
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
}
