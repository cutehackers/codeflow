package flowview

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func newRequirementFixtureServer(t *testing.T) *Server {
	t.Helper()
	root := copyFixtureWithoutCodeflow(t, "nextjs-app-fixture")
	moduleRoot, _ := filepath.Abs("../..")
	t.Setenv("CODEFLOW_ADAPTER_TYPESCRIPT_BIN", "noderun:"+filepath.Join(moduleRoot, "adapters", "typescript"))
	srv, err := NewServer(Config{RepoRoot: root, Port: 0})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}
	return srv
}

func postRequirement(t *testing.T, srv *Server, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/task/requirement?token="+srv.AuthToken(), bytes.NewReader(raw))
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)
	return rec
}

func TestTaskRequirementEvidenceBinding(t *testing.T) {
	srv := newRequirementFixtureServer(t)
	entry := "app/page.tsx%23HomePage.handleQuickCheckout"

	viewReq := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/task/view?token="+srv.AuthToken()+"&entrySymbol="+entry, nil)
	viewRec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(viewRec, viewReq)
	if viewRec.Code != http.StatusOK {
		t.Fatalf("expected 200 for task view, got %d: %s", viewRec.Code, viewRec.Body.String())
	}
	var viewDoc map[string]any
	if err := json.Unmarshal(viewRec.Body.Bytes(), &viewDoc); err != nil {
		t.Fatalf("failed to decode task view: %v", err)
	}
	semMap, _ := viewDoc["semanticMap"].(map[string]any)
	generation, _ := semMap["generationId"].(string)
	basis, _ := semMap["computedBasisId"].(string)
	if generation == "" || basis == "" {
		t.Fatalf("task view lacks generation identity: %v", semMap)
	}

	criteria := []map[string]any{{"id": "AC-1", "text": "quick checkout flow"}}
	first := postRequirement(t, srv, map[string]any{
		"generationId": generation, "requirementRevision": "rev-1", "criteria": criteria,
	})
	if first.Code != http.StatusOK {
		t.Fatalf("expected 200 for requirement evidence, got %d: %s", first.Code, first.Body.String())
	}
	var firstDoc map[string]any
	_ = json.Unmarshal(first.Body.Bytes(), &firstDoc)
	if firstDoc["requirementRevision"] != "rev-1" {
		t.Errorf("expected revision echo rev-1, got %v", firstDoc["requirementRevision"])
	}
	if firstDoc["computedBasisId"] != basis {
		t.Errorf("expected basis %s, got %v", basis, firstDoc["computedBasisId"])
	}
	alignments, _ := firstDoc["alignments"].([]any)
	if len(alignments) == 0 {
		t.Fatalf("expected at least one alignment, got none")
	}
	for _, item := range alignments {
		alignment, _ := item.(map[string]any)
		if alignment["authority"] != "candidate" {
			t.Errorf("expected candidate authority, got %v", alignment["authority"])
		}
		if status, _ := alignment["status"].(string); status == "verified" || status == "confirmed" {
			t.Errorf("text match must not promote to %q", status)
		}
		if refs, _ := alignment["coveredStepRefs"].([]any); len(refs) == 0 {
			t.Errorf("expected covered steps for matching criterion, got %v", alignment)
		}
	}

	second := postRequirement(t, srv, map[string]any{
		"generationId": generation, "requirementRevision": "rev-2", "criteria": criteria,
	})
	if second.Code != http.StatusOK {
		t.Fatalf("expected 200 for revised requirement, got %d: %s", second.Code, second.Body.String())
	}
	var secondDoc map[string]any
	_ = json.Unmarshal(second.Body.Bytes(), &secondDoc)
	if secondDoc["requirementRevision"] != "rev-2" {
		t.Errorf("revised requirement must bind to rev-2, got %v", secondDoc["requirementRevision"])
	}

	unmatched := postRequirement(t, srv, map[string]any{
		"generationId": generation, "requirementRevision": "rev-3",
		"criteria": []map[string]any{{"id": "AC-9", "text": "quantum teleportation ledger xyzzy"}},
	})
	if unmatched.Code != http.StatusOK {
		t.Fatalf("expected 200 for unmatched requirement, got %d", unmatched.Code)
	}
	var unmatchedDoc map[string]any
	_ = json.Unmarshal(unmatched.Body.Bytes(), &unmatchedDoc)
	for _, item := range unmatchedDoc["alignments"].([]any) {
		alignment, _ := item.(map[string]any)
		if status, _ := alignment["status"].(string); status == "verified" || status == "confirmed" {
			t.Errorf("unmatched requirement must not verify, got %q", status)
		}
	}

	unknown := postRequirement(t, srv, map[string]any{
		"generationId": "gen-missing", "requirementRevision": "rev-1", "criteria": criteria,
	})
	if unknown.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for unknown generation, got %d", unknown.Code)
	}

	empty := postRequirement(t, srv, map[string]any{
		"generationId": generation, "requirementRevision": "rev-1", "criteria": []map[string]any{},
	})
	if empty.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for empty criteria, got %d", empty.Code)
	}
	if body := empty.Body.String(); !strings.Contains(body, "missing_precondition") {
		t.Errorf("expected missing_precondition code, got %s", body)
	}
}
