package flowview

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"codeflow/internal/collector/contractharness"
	"codeflow/internal/curator/semantic"
)

// Exercise the real Dart adapter, immutable snapshot, canonical validator and
// HTTP source consumer together. DOM fixtures alone cannot cover this seam.
func TestFlowContextDartProductionIntegration(t *testing.T) {
	if _, err := exec.LookPath("dart"); err != nil {
		t.Skip("Dart SDK is required")
	}
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "lib"), 0755)
	const source = "class Service {\n  int state = 0;\n  void run() {\n    // client.deletedCall();\n    state = 1;\n  }\n}\n"
	os.WriteFile(filepath.Join(root, "pubspec.yaml"), []byte("name: context_test\nenvironment:\n  sdk: ^3.0.0\n"), 0600)
	os.WriteFile(filepath.Join(root, "lib/service.dart"), []byte(source), 0600)
	adapter, err := filepath.Abs("../../../adapters/dart")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEFLOW_ADAPTER_DART_BIN", "dartrun:"+adapter)
	srv, err := NewServer(Config{RepoRoot: root, Port: 0, AuthToken: "test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Shutdown(context.Background()) })
	r := httptest.NewRecorder()
	srv.serveTaskView(r, httptest.NewRequest("GET", "/api/task/view?mode=feature&entrySymbol=lib/service.dart%23Service.run", nil))
	if r.Code != 200 {
		t.Fatalf("Dart task request failed: %d %s", r.Code, r.Body.String())
	}
	var response struct {
		SemanticMap  json.RawMessage                  `json:"semanticMap"`
		FlowContexts map[string]FlowContextProjection `json:"flowContexts"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if err := contractharness.ValidateSemanticMapIR(response.SemanticMap); err != nil {
		t.Fatal(err)
	}
	var m semantic.SemanticMapIR
	json.Unmarshal(response.SemanticMap, &m)
	if len(m.Steps) != 1 {
		t.Fatalf("comment entered semantic ledger: %+v", m.Steps)
	}
	selected := response.FlowContexts[m.Steps[0].StepID]
	if selected.Precision != PrecisionExact {
		t.Logf("evidence=%+v unknowns=%+v step=%+v", m.Evidence, m.Unknowns, m.Steps[0])
	}
	requireExact(t, &selected)
	for _, scope := range []string{"flow_context", "callable", "file"} {
		r = httptest.NewRecorder()
		srv.serveFlowContext(r, httptest.NewRequest("GET", "/api/flow/context?stepId="+m.Steps[0].StepID+"&generationId="+m.GenerationID+"&expand="+scope, nil))
		var p FlowContextProjection
		if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &p) != nil {
			t.Fatal(r.Body.String())
		}
		requireExact(t, &p)
		if p.Statement.ByteRange != selected.Statement.ByteRange {
			t.Fatal("expansion changed selected statement")
		}
	}
}

func TestTaskViewRejectsMissingDartEntryWithoutStorage(t *testing.T) {
	if _, err := exec.LookPath("dart"); err != nil {
		t.Skip("Dart SDK is required")
	}
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "lib"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "pubspec.yaml"), []byte("name: missing_entry_test\nenvironment:\n  sdk: ^3.0.0\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "lib", "service.dart"), []byte("class Service {\n  void run() {}\n}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	adapter, err := filepath.Abs("../../../adapters/dart")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEFLOW_ADAPTER_DART_BIN", "dartrun:"+adapter)
	srv, err := NewServer(Config{RepoRoot: root, Port: 0, AuthToken: "test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Shutdown(context.Background()) })

	recorder := httptest.NewRecorder()
	srv.serveTaskView(recorder, httptest.NewRequest(http.MethodGet, "/api/task/view?mode=feature&entrySymbol=lib/service.dart%23Service.missing", nil))
	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d: %s", recorder.Code, recorder.Body.String())
	}
	var response map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response["code"] != "analysis_failed" {
		t.Fatalf("expected analysis_failed, got %#v", response)
	}
	if !strings.Contains(fmt.Sprint(response["message"]), "entry_symbol_not_found") {
		t.Fatalf("expected entry failure detail, got %#v", response)
	}
	views, err := srv.storage.ListViews(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 0 {
		t.Fatalf("analysis failure stored task views: %#v", views)
	}
}
