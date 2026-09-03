package flowview

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestFlowViewTaskViewEndpoint(t *testing.T) {
	root, err := filepath.Abs("../../test/fixtures/nextjs-app-fixture")
	if err != nil {
		t.Fatal(err)
	}

	moduleRoot, _ := filepath.Abs("../..")
	t.Setenv("CODEFLOW_ADAPTER_TYPESCRIPT_BIN", "noderun:"+filepath.Join(moduleRoot, "adapters", "typescript"))

	srv, err := NewServer(Config{
		RepoRoot: root,
		Port:     0,
	})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	// 1. Missing precondition
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/task/view?token="+srv.AuthToken(), nil)
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing precondition, got %d", rec.Code)
	}
	var errDoc map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &errDoc)
	if errDoc["code"] != "missing_precondition" {
		t.Errorf("expected code missing_precondition, got %v", errDoc["code"])
	}

	// 2. Ambiguous target
	reqAmb := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/task/view?token="+srv.AuthToken()+"&query=checkout", nil)
	recAmb := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(recAmb, reqAmb)

	if recAmb.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for ambiguous target, got %d", recAmb.Code)
	}
	var ambDoc map[string]any
	_ = json.Unmarshal(recAmb.Body.Bytes(), &ambDoc)
	if ambDoc["code"] != "ambiguous_target" {
		t.Errorf("expected code ambiguous_target, got %v", ambDoc["code"])
	}
	candidates, _ := ambDoc["candidateTargets"].([]any)
	if len(candidates) < 2 {
		t.Errorf("expected at least 2 candidates in ambiguous response, got %v", candidates)
	}

	// 3. Unambiguous query
	reqValid := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/task/view?token="+srv.AuthToken()+"&entrySymbol=app/page.tsx%23HomePage.handleQuickCheckout", nil)
	recValid := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(recValid, reqValid)

	if recValid.Code != http.StatusOK {
		t.Fatalf("expected 200 for valid query, got %d: %s", recValid.Code, recValid.Body.String())
	}
	var resDoc map[string]any
	if err := json.Unmarshal(recValid.Body.Bytes(), &resDoc); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if _, ok := resDoc["currentAnswer"]; !ok {
		t.Error("missing currentAnswer in response")
	}
	if _, ok := resDoc["semanticMap"]; !ok {
		t.Error("missing semanticMap in response")
	}
	if _, ok := resDoc["projection"]; !ok {
		t.Error("missing projection in response")
	}
}
