package flowview

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"codeflow/internal/collector/storage"
	"codeflow/internal/curator/semantic"
)

func TestFlowViewTaskViewEndpoint(t *testing.T) {
	root := copyFixtureWithoutCodeflow(t, "nextjs-app-fixture")
	if _, err := os.Stat(filepath.Join(root, ".codeflow")); !os.IsNotExist(err) {
		t.Fatalf("fixture copy is not pristine: .codeflow stat error=%v", err)
	}

	moduleRoot, _ := filepath.Abs("../../..")
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
	reqValid := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/task/view?token="+srv.AuthToken()+"&entrySymbol=app/page.tsx%23HomePage.onQuickCheckout", nil)
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
	sbDoc, ok := resDoc["flowSequence"].(map[string]any)
	if !ok || sbDoc == nil {
		t.Fatal("missing or invalid flowSequence in response")
	}
	if sbDoc["schemaId"] != semantic.FlowSequenceSchemaID {
		t.Errorf("expected flowSequence schemaId %s, got %v", semantic.FlowSequenceSchemaID, sbDoc["schemaId"])
	}
	frames, ok := sbDoc["frames"].([]any)
	if !ok || len(frames) == 0 {
		t.Errorf("expected at least 1 frame in flowSequence, got %v", sbDoc["frames"])
	}
	baseline, _ := resDoc["baseline"].(map[string]any)
	if baseline == nil || baseline["status"] != "none" {
		t.Errorf("expected explicit baseline none marker, got %v", resDoc["baseline"])
	}
}

func TestFlowViewTaskViewRequestIdentity(t *testing.T) {
	root := copyFixtureWithoutCodeflow(t, "nextjs-app-fixture")
	moduleRoot, _ := filepath.Abs("../../..")
	t.Setenv("CODEFLOW_ADAPTER_TYPESCRIPT_BIN", "noderun:"+filepath.Join(moduleRoot, "adapters", "typescript"))

	srv, err := NewServer(Config{
		RepoRoot: root,
		Port:     0,
	})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}
	entry := "app/page.tsx%23HomePage.onQuickCheckout"
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

func TestSavedTaskViewSurvivesRestartWithoutAnalysis(t *testing.T) {
	root := copyFixtureWithoutCodeflow(t, "nextjs-app-fixture")
	moduleRoot, _ := filepath.Abs("../../..")
	t.Setenv("CODEFLOW_ADAPTER_TYPESCRIPT_BIN", "noderun:"+filepath.Join(moduleRoot, "adapters", "typescript"))
	srv, err := NewServer(Config{RepoRoot: root, Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, httptest.NewRequest("GET", "http://127.0.0.1/api/task/view?token="+srv.AuthToken()+"&entrySymbol=app/page.tsx%23HomePage.onQuickCheckout", nil))
	if rec.Code != 200 {
		t.Fatalf("analysis: %d %s", rec.Code, rec.Body.String())
	}
	var result map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	id, _ := result["viewId"].(string)
	if id == "" {
		t.Fatal("missing saved view")
	}
	if err := srv.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	// The adapter and current source are unavailable. Reading must still succeed.
	t.Setenv("CODEFLOW_ADAPTER_TYPESCRIPT_BIN", "missing-adapter")
	if err := os.Remove(filepath.Join(root, "app", "page.tsx")); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewServer(Config{RepoRoot: root, Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Shutdown(context.Background())
	restored := httptest.NewRecorder()
	reopened.httpServer.Handler.ServeHTTP(restored, httptest.NewRequest("GET", "http://127.0.0.1/api/view?token="+reopened.AuthToken()+"&viewId="+id, nil))
	if restored.Code != 200 {
		t.Fatalf("restore: %d %s", restored.Code, restored.Body.String())
	}
	var actual map[string]any
	if err := json.Unmarshal(restored.Body.Bytes(), &actual); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result, actual) {
		t.Fatal("restored result differs from analysis")
	}
	denied := httptest.NewRecorder()
	reopened.httpServer.Handler.ServeHTTP(denied, httptest.NewRequest("GET", "http://127.0.0.1/api/view?viewId="+id, nil))
	if denied.Code == 200 {
		t.Fatal("unauthenticated read accepted")
	}
}

func TestRestoreTaskViewSourceContextMissingTriggersReanalysisFlag(t *testing.T) {
	store := storage.New(t.TempDir())
	savedPayload := map[string]any{
		"flowId":       "flow-missing-context",
		"title":        "결제 승인",
		"flowSequence": testFlowSequence("flow-missing-context"),
		"flowContexts": map[string]any{},
		"sourceFiles":  map[string]any{},
		"semanticMap": map[string]any{
			"steps": []any{
				map[string]any{"stepId": "step-1", "name": "승인 요청"},
			},
		},
	}
	savedBytes, err := json.Marshal(savedPayload)
	if err != nil {
		t.Fatal(err)
	}
	viewID, _, err := store.SaveView(context.Background(), savedBytes)
	if err != nil {
		t.Fatal(err)
	}

	srv := &Server{storage: store}
	restored, err := srv.RestoreTaskView(context.Background(), viewID)
	if err != nil {
		t.Fatalf("failed to restore view: %v", err)
	}

	if restored["sourceContextMissing"] != true {
		t.Fatalf("expected sourceContextMissing to be true, got: %v", restored["sourceContextMissing"])
	}
	if restored["needsReanalysis"] != true {
		t.Fatalf("expected needsReanalysis to be true, got: %v", restored["needsReanalysis"])
	}
	if notice, ok := restored["sourceNotice"].(string); !ok || notice == "" {
		t.Fatalf("expected non-empty sourceNotice, got: %v", restored["sourceNotice"])
	}
}

func TestRestoreTaskViewRejectsMissingFlowSequence(t *testing.T) {
	store := storage.New(t.TempDir())
	savedBytes, err := json.Marshal(map[string]any{
		"flowId":      "flow-old",
		"semanticMap": map[string]any{"steps": []any{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	viewID, _, err := store.SaveView(context.Background(), savedBytes)
	if err != nil {
		t.Fatal(err)
	}

	srv := &Server{storage: store}
	if _, err := srv.RestoreTaskView(context.Background(), viewID); !errors.Is(err, errUnsupportedFlowSequenceSchema) {
		t.Fatalf("expected unsupported FlowSequence schema error, got: %v", err)
	}
	rec := httptest.NewRecorder()
	srv.serveTaskViewDetail(rec, httptest.NewRequest(http.MethodGet, "/api/view?viewId="+viewID, nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected HTTP 400 for unsupported FlowSequence schema, got %d", rec.Code)
	}
	var errorDoc map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &errorDoc); err != nil {
		t.Fatal(err)
	}
	if errorDoc["code"] != errUnsupportedFlowSequenceSchema.Error() {
		t.Fatalf("expected error code %q, got %v", errUnsupportedFlowSequenceSchema, errorDoc["code"])
	}
}

func TestRestoreTaskViewSourceDeletedOnDiskTriggersReanalysis(t *testing.T) {
	tempRepo := t.TempDir()
	store := storage.New(tempRepo)

	// A saved view pointing to a file "deleted_module.go" that does NOT exist in tempRepo
	savedPayload := map[string]any{
		"viewId":       "view-deleted-disk",
		"flowId":       "flow-deleted-disk",
		"title":        "삭제된 모듈 흐름",
		"flowSequence": testFlowSequence("flow-deleted-disk"),
		"flowContexts": map[string]any{
			"step-1": map[string]any{
				"canonicalPath": "deleted_module.go",
				"filePath":      "deleted_module.go",
			},
		},
		"sourceFiles": map[string]any{
			"deleted_module.go": []any{"line 1", "line 2"},
		},
		"semanticMap": map[string]any{
			"steps": []any{
				map[string]any{"stepId": "step-1", "name": "모듈 호출"},
			},
		},
	}
	savedBytes, err := json.Marshal(savedPayload)
	if err != nil {
		t.Fatal(err)
	}
	viewID, _, err := store.SaveView(context.Background(), savedBytes)
	if err != nil {
		t.Fatal(err)
	}

	srv := &Server{storage: store, repoRoot: tempRepo}
	restored, err := srv.RestoreTaskView(context.Background(), viewID)
	if err != nil {
		t.Fatalf("failed to restore view: %v", err)
	}

	if restored["sourceContextMissing"] != true {
		t.Fatalf("expected sourceContextMissing=true when source files are deleted from disk, got: %v", restored["sourceContextMissing"])
	}
	if restored["needsReanalysis"] != true {
		t.Fatalf("expected needsReanalysis=true when source files are deleted from disk, got: %v", restored["needsReanalysis"])
	}
	notice, _ := restored["sourceNotice"].(string)
	if !strings.Contains(notice, "삭제") && !strings.Contains(notice, "재분석") {
		t.Fatalf("expected informative notice mentioning deletion/reanalysis, got: %q", notice)
	}
}

func testFlowSequence(flowID string) map[string]any {
	return map[string]any{
		"schemaId":        semantic.FlowSequenceSchemaID,
		"schemaVersion":   semantic.FlowSequenceSchemaVersion,
		"flowID":          flowID,
		"generationId":    "generation-test",
		"computedBasisId": "basis-test",
		"snapshotID":      "snapshot-test",
		"frames":          []any{},
	}
}
