package flowview

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// TestLPCA_VS06_A01_StaticFlowViewFeatureQueryPreserved verifies that submitting a feature query
// via HTTP /api/task/view retains existing static query behavior.
func TestLPCA_VS06_A01_StaticFlowViewFeatureQueryPreserved(t *testing.T) {
	root := copyFixtureWithoutCodeflow(t, "nextjs-app-fixture")
	moduleRoot, _ := filepath.Abs("../..")
	t.Setenv("CODEFLOW_ADAPTER_TYPESCRIPT_BIN", "noderun:"+filepath.Join(moduleRoot, "adapters", "typescript"))

	srv, err := NewServer(Config{RepoRoot: root, Port: 0, Mode: "feature"})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer func() { _ = srv.Shutdown(context.Background()) }()

	// 1. Missing query returns typed 400 missing_precondition error
	reqMissing := httptest.NewRequest("GET", "/api/task/view?token="+srv.AuthToken(), nil)
	recMissing := httptest.NewRecorder()
	srv.handleTaskView(recMissing, reqMissing)
	if recMissing.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing precondition, got %d", recMissing.Code)
	}
	var errDoc map[string]any
	_ = json.Unmarshal(recMissing.Body.Bytes(), &errDoc)
	if errDoc["code"] != "missing_precondition" {
		t.Fatalf("expected code missing_precondition, got %v", errDoc["code"])
	}

	// 2. Feature query with entrySymbol returns 200 OK with semanticMap
	reqEntry := httptest.NewRequest("GET", "/api/task/view?token="+srv.AuthToken()+"&mode=feature&entrySymbol=app/page.tsx%23HomePage.handleQuickCheckout", nil)
	recEntry := httptest.NewRecorder()
	srv.handleTaskView(recEntry, reqEntry)
	if recEntry.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for resolved entry query, got %d: %s", recEntry.Code, recEntry.Body.String())
	}
	var respDoc map[string]any
	if err := json.Unmarshal(recEntry.Body.Bytes(), &respDoc); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if respDoc["semanticMap"] == nil {
		t.Fatal("expected semanticMap in feature query response")
	}
}

// TestLPCA_VS06_A03_LiveURLIgnoresRequestParams verifies that opening /live?request=...&entrySymbol=...
// serves LiveViewHTML without using request or entrySymbol as query start condition.
func TestLPCA_VS06_A03_LiveURLIgnoresRequestParams(t *testing.T) {
	root := t.TempDir()
	createSampleProject(t, root)

	srv, err := NewServer(Config{RepoRoot: root, Port: 0, Mode: "project_change"})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer func() { _ = srv.Shutdown(context.Background()) }()

	// GET /live with legacy query parameters
	req := httptest.NewRequest("GET", "/live?token="+srv.AuthToken()+"&request=legacy_request&entrySymbol=legacy_sym", nil)
	rec := httptest.NewRecorder()
	srv.handleIndex(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for /live, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "CODEFLOW · FLOWVIEW") {
		t.Fatal("expected the 7-lane FlowView to be served on /live in project_change mode")
	}

	// Server state must remain watching without an active task request from the URL
	if srv.latestLiveRequest() != nil {
		t.Fatal("server must not record URL query parameters as live task request")
	}

	// /api/live/project reports watching in project_change mode
	projReq := httptest.NewRequest("GET", "/api/live/project?token="+srv.AuthToken(), nil)
	projRec := httptest.NewRecorder()
	srv.handleLiveProject(projRec, projReq)
	if projRec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from /api/live/project, got %d", projRec.Code)
	}
	var proj map[string]any
	_ = json.Unmarshal(projRec.Body.Bytes(), &proj)
	if proj["mode"] != "project_change" {
		t.Fatalf("expected mode project_change, got %v", proj["mode"])
	}
	if proj["status"] != "pending" {
		t.Fatalf("expected status pending while the baseline compiles, got %v", proj["status"])
	}
}

// TestLPCA_VS06_A04_StaticFlowViewNoProjectChangeSubscription verifies that Static FlowView (/)
// serves FlowViewHTML, which is decoupled from live SSE streams.
func TestLPCA_VS06_A04_StaticFlowViewNoProjectChangeSubscription(t *testing.T) {
	root := t.TempDir()
	createSampleProject(t, root)

	srv, err := NewServer(Config{RepoRoot: root, Port: 0, Mode: "feature"})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer func() { _ = srv.Shutdown(context.Background()) }()

	// GET /
	req := httptest.NewRequest("GET", "/?token="+srv.AuthToken(), nil)
	rec := httptest.NewRecorder()
	srv.handleIndex(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for /, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "CODEFLOW · FLOWVIEW") {
		t.Fatal("expected FlowViewHTML to be served on /")
	}
	if strings.Contains(body, `data-view="live-semantic-map"`) {
		t.Fatal("FlowViewHTML must not have live-semantic-map view attribute")
	}
}

// TestLPCA_VS06_A05_ViewServeAliasesServeStaticFlowView verifies routing between static FlowView
// and Live View on the same coordinator.
func TestLPCA_VS06_A05_ViewServeAliasesServeStaticFlowView(t *testing.T) {
	root := t.TempDir()
	createSampleProject(t, root)

	srv, err := NewServer(Config{RepoRoot: root, Port: 0, Mode: "project_change"})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer func() { _ = srv.Shutdown(context.Background()) }()

	// 1. / serves FlowViewHTML
	reqRoot := httptest.NewRequest("GET", "/?token="+srv.AuthToken(), nil)
	recRoot := httptest.NewRecorder()
	srv.handleIndex(recRoot, reqRoot)
	if !strings.Contains(recRoot.Body.String(), "CODEFLOW · FLOWVIEW") {
		t.Fatal("/ must serve FlowViewHTML")
	}

	// 2. /live serves the 7-lane FlowView in project_change mode
	reqLive := httptest.NewRequest("GET", "/live?token="+srv.AuthToken(), nil)
	recLive := httptest.NewRecorder()
	srv.handleIndex(recLive, reqLive)
	if !strings.Contains(recLive.Body.String(), "CODEFLOW · FLOWVIEW") {
		t.Fatal("/live must serve the 7-lane FlowView in project_change mode")
	}

	// 3. /?live=1 also serves FlowViewHTML
	reqLiveAlias := httptest.NewRequest("GET", "/?token="+srv.AuthToken()+"&live=1", nil)
	recLiveAlias := httptest.NewRecorder()
	srv.handleIndex(recLiveAlias, reqLiveAlias)
	if !strings.Contains(recLiveAlias.Body.String(), "CODEFLOW · FLOWVIEW") {
		t.Fatal("/?live=1 must serve FlowViewHTML")
	}
}
