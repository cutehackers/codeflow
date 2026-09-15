package flowview

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestFlowViewTaskViewEndpoint(t *testing.T) {
	root := copyFixtureWithoutCodeflow(t, "nextjs-app-fixture")
	if _, err := os.Stat(filepath.Join(root, ".codeflow")); !os.IsNotExist(err) {
		t.Fatalf("fixture copy is not pristine: .codeflow stat error=%v", err)
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
	if _, ok := resDoc["candidateAnswer"]; !ok {
		t.Error("missing candidateAnswer in response")
	}
	if _, ok := resDoc["semanticMap"]; !ok {
		t.Error("missing semanticMap in response")
	}
	if _, ok := resDoc["projection"]; !ok {
		t.Error("missing projection in response")
	}
	baseline, _ := resDoc["baseline"].(map[string]any)
	if baseline == nil || baseline["status"] != "none" {
		t.Errorf("expected explicit baseline none marker, got %v", resDoc["baseline"])
	}
}

func TestFlowViewTaskViewRequestIdentity(t *testing.T) {
	root := copyFixtureWithoutCodeflow(t, "nextjs-app-fixture")
	moduleRoot, _ := filepath.Abs("../..")
	t.Setenv("CODEFLOW_ADAPTER_TYPESCRIPT_BIN", "noderun:"+filepath.Join(moduleRoot, "adapters", "typescript"))

	srv, err := NewServer(Config{
		RepoRoot: root,
		Port:     0,
	})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}
	entry := "app/page.tsx%23HomePage.handleQuickCheckout"
	firstURL := "http://127.0.0.1/api/task/view?token=" + srv.AuthToken() + "&entrySymbol=" + entry + "&requestId=req-identity-1"

	get := func(url string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, url, nil)
		rec := httptest.NewRecorder()
		srv.httpServer.Handler.ServeHTTP(rec, req)
		return rec
	}

	first := get(firstURL)
	if first.Code != http.StatusOK {
		t.Fatalf("expected 200 for first request, got %d: %s", first.Code, first.Body.String())
	}
	var firstDoc map[string]any
	if err := json.Unmarshal(first.Body.Bytes(), &firstDoc); err != nil {
		t.Fatalf("failed to decode first response: %v", err)
	}
	if firstDoc["requestId"] != "req-identity-1" {
		t.Errorf("expected requestId echo, got %v", firstDoc["requestId"])
	}

	second := get(firstURL)
	if second.Code != http.StatusOK {
		t.Fatalf("expected 200 for repeated request, got %d: %s", second.Code, second.Body.String())
	}
	if first.Body.String() != second.Body.String() {
		t.Error("expected identical payload reuse for same request ID and input")
	}

	conflict := get("http://127.0.0.1/api/task/view?token=" + srv.AuthToken() + "&query=checkout&requestId=req-identity-1")
	if conflict.Code != http.StatusConflict {
		t.Fatalf("expected 409 for reused request ID with different input, got %d: %s", conflict.Code, conflict.Body.String())
	}
	var conflictDoc map[string]any
	_ = json.Unmarshal(conflict.Body.Bytes(), &conflictDoc)
	if conflictDoc["code"] != "conflict" {
		t.Errorf("expected code conflict, got %v", conflictDoc["code"])
	}

	anonymous := get("http://127.0.0.1/api/task/view?token=" + srv.AuthToken() + "&entrySymbol=" + entry)
	if anonymous.Code != http.StatusOK {
		t.Fatalf("expected 200 without request ID, got %d: %s", anonymous.Code, anonymous.Body.String())
	}
	var anonymousDoc map[string]any
	_ = json.Unmarshal(anonymous.Body.Bytes(), &anonymousDoc)
	rid, _ := anonymousDoc["requestId"].(string)
	if rid == "" {
		t.Error("expected server-assigned requestId when the caller omits it")
	}
}
