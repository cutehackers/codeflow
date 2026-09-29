package flowview

import (
	"context"
	"encoding/json"
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
	const source = "class Service {\n  int state = 0;\n  void run() {\n    // client.deletedCall();\n    final marker = 'semi;colon';\n    state = marker.length;\n  }\n}\n"
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
	if len(m.Steps) != 2 {
		t.Fatalf("comment entered semantic ledger: %+v", m.Steps)
	}
	foundDeclaration := false
	for _, step := range m.Steps {
		span := source[step.Anchor.ByteRange[0]:step.Anchor.ByteRange[1]]
		if span == "final marker = 'semi;colon';" {
			foundDeclaration = true
		}
		if span == "// client.deletedCall();" {
			t.Fatal("comment entered semantic ledger")
		}
		selected := response.FlowContexts[step.StepID]
		if selected.Precision != PrecisionExact {
			t.Logf("evidence=%+v unknowns=%+v step=%+v", m.Evidence, m.Unknowns, step)
		}
		requireExact(t, &selected)
		for _, scope := range []string{"flow_context", "callable", "file"} {
			r = httptest.NewRecorder()
			srv.serveFlowContext(r, httptest.NewRequest("GET", "/api/flow/context?stepId="+step.StepID+"&generationId="+m.GenerationID+"&expand="+scope, nil))
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
	if !foundDeclaration {
		t.Fatal("AST declaration did not reach FlowView source context")
	}
}

func TestTaskViewPreservesDartCallbackSourceWithoutFalseContinuation(t *testing.T) {
	if _, err := exec.LookPath("dart"); err != nil {
		t.Skip("Dart SDK is required")
	}
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "lib"), 0755); err != nil {
		t.Fatal(err)
	}
	const source = "class Service {\n  void run(List<int> items) {\n    items.forEach((item) {\n      if (item == 0) return;\n    });\n    final finished = true;\n  }\n}\n"
	if err := os.WriteFile(filepath.Join(root, "pubspec.yaml"), []byte("name: callback_scope_test\nenvironment:\n  sdk: ^3.0.0\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "lib", "service.dart"), []byte(source), 0600); err != nil {
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
	srv.serveTaskView(recorder, httptest.NewRequest(http.MethodGet, "/api/task/view?mode=feature&entrySymbol=lib/service.dart%23Service.run", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("Dart task request failed: %d %s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		SemanticMap  semantic.SemanticMapIR           `json:"semanticMap"`
		FlowSequence semantic.FlowSequence            `json:"flowSequence"`
		FlowContexts map[string]FlowContextProjection `json:"flowContexts"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if err := semantic.ValidateFlowSequenceReferences(&response.SemanticMap, &response.FlowSequence); err != nil {
		t.Fatal(err)
	}
	foundFinished, foundCallbackReturn := false, false
	var callbackReturnID, finishedID, registrationID, callbackEntryID, callbackInvocationID, outerInvocationID string
	for _, step := range response.SemanticMap.Steps {
		span := source[step.Anchor.ByteRange[0]:step.Anchor.ByteRange[1]]
		if strings.HasPrefix(span, "items.forEach(") {
			registrationID = step.StepID
		}
		if strings.Contains(span, "return;") && !strings.HasPrefix(span, "items.forEach(") {
			foundCallbackReturn = true
			callbackReturnID = step.StepID
			callbackInvocationID = step.InvocationID
		}
		if strings.Contains(span, "if (item == 0)") && !strings.HasPrefix(span, "items.forEach(") {
			callbackEntryID = step.StepID
		}
		if span == "final finished = true;" {
			foundFinished = true
			finishedID = step.StepID
			outerInvocationID = step.InvocationID
		}
		context, ok := response.FlowContexts[step.StepID]
		if !ok {
			t.Fatalf("missing code context for %s", step.StepID)
		}
		requireExact(t, &context)
	}
	if !foundFinished || !foundCallbackReturn || registrationID == "" || callbackEntryID == "" || callbackInvocationID == outerInvocationID {
		t.Fatal("callback and outer continuation must retain exact source in separate invocations")
	}
	foundPartialCallback := false
	for _, frame := range response.FlowSequence.Frames {
		for _, ref := range frame.StepRefs {
			if ref == callbackReturnID && frame.Status == "partial" {
				foundPartialCallback = true
			}
		}
	}
	if !foundPartialCallback {
		t.Fatal("callback source was presented as proven execution")
	}
	foundCallbackBoundary := false
	for _, unknown := range response.SemanticMap.Unknowns {
		if strings.Contains(unknown.Subject, "unresolved_dynamic.callback_") && strings.Contains(unknown.Reason, "콜백") {
			foundCallbackBoundary = true
		}
	}
	if !foundCallbackBoundary {
		t.Fatal("callback registration did not preserve its unresolved execution boundary")
	}
	foundCallbackArgument := false
	for _, edge := range response.SemanticMap.Edges {
		if edge.FromStepID == registrationID && edge.ToStepID == callbackEntryID && edge.Kind == "callback_argument" && edge.ResolutionStatus == "resolved" {
			foundCallbackArgument = true
		}
		if edge.FromStepID == callbackReturnID && edge.ToStepID == finishedID && edge.ResolutionStatus == "resolved" {
			t.Fatal("callback return falsely continued the outer method")
		}
	}
	if !foundCallbackArgument {
		t.Fatal("callback argument did not link registration source to callback code")
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
	if response["reasonCode"] != "entry_symbol_not_found" || response["target"] != "lib/service.dart#Service.missing" || response["nextAction"] == "" {
		t.Fatalf("expected structured entry failure detail, got %#v", response)
	}
	views, err := srv.storage.ListViews(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 0 {
		t.Fatalf("analysis failure stored task views: %#v", views)
	}
}
