package flowview

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"codeflow/internal/semantic"
)

func TestTaskViewsAndComparisonDescriptors(t *testing.T) {
	root := copyFixtureWithoutCodeflow(t, "nextjs-app-fixture")
	moduleRoot, _ := filepath.Abs("../..")
	t.Setenv("CODEFLOW_ADAPTER_TYPESCRIPT_BIN", "noderun:"+filepath.Join(moduleRoot, "adapters", "typescript"))
	srv, err := NewServer(Config{RepoRoot: root, Port: 0})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}
	serve := func(url string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, url, nil)
		rec := httptest.NewRecorder()
		srv.httpServer.Handler.ServeHTTP(rec, req)
		return rec
	}

	empty := serve("http://127.0.0.1/api/views?token=" + srv.AuthToken())
	if empty.Code != http.StatusOK {
		t.Fatalf("expected 200 for views, got %d", empty.Code)
	}
	var emptyDoc struct {
		Views []map[string]any `json:"views"`
	}
	_ = json.Unmarshal(empty.Body.Bytes(), &emptyDoc)
	if len(emptyDoc.Views) != 0 {
		t.Fatalf("expected no preserved views before any view, got %d", len(emptyDoc.Views))
	}

	entry := "app/page.tsx%23HomePage.handleQuickCheckout"
	view := serve("http://127.0.0.1/api/task/view?token=" + srv.AuthToken() + "&entrySymbol=" + entry)
	if view.Code != http.StatusOK {
		t.Fatalf("expected 200 for task view, got %d: %s", view.Code, view.Body.String())
	}
	var viewDoc map[string]any
	_ = json.Unmarshal(view.Body.Bytes(), &viewDoc)
	viewID, _ := viewDoc["viewId"].(string)
	if viewID == "" {
		t.Fatalf("expected saved viewId in response")
	}

	listed := serve("http://127.0.0.1/api/views?token=" + srv.AuthToken())
	var listedDoc struct {
		Views []map[string]any `json:"views"`
	}
	_ = json.Unmarshal(listed.Body.Bytes(), &listedDoc)
	if len(listedDoc.Views) != 1 {
		t.Fatalf("expected 1 preserved view, got %d", len(listedDoc.Views))
	}

	compare := serve("http://127.0.0.1/api/view/compare?baselineId=" + viewID + "&viewId=" + viewID + "&token=" + srv.AuthToken())
	if compare.Code != http.StatusOK {
		t.Fatalf("expected 200 for compare, got %d: %s", compare.Code, compare.Body.String())
	}
	var compareDoc map[string]any
	_ = json.Unmarshal(compare.Body.Bytes(), &compareDoc)
	if compareDoc["deltaId"] == "" {
		t.Fatalf("missing deltaId in compare response")
	}

	unknown := serve("http://127.0.0.1/api/view/compare?baselineId=missing-id&viewId=" + viewID + "&token=" + srv.AuthToken())
	if unknown.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for unknown baseline, got %d", unknown.Code)
	}
}

func TestPulseNavigationTargetPerSide(t *testing.T) {
	baseMap := &semantic.SemanticMapIR{
		GenerationID: "gen-base", ComputedBasisID: "basis-base",
		Steps: []semantic.SemanticStep{
			{StepID: "step-gone", TechnicalName: "OrderService.cancel"},
			{StepID: "step-both", TechnicalName: "OrderService.checkout"},
		},
	}
	currMap := &semantic.SemanticMapIR{
		GenerationID: "gen-curr", ComputedBasisID: "basis-curr",
		Steps: []semantic.SemanticStep{
			{StepID: "step-both", TechnicalName: "OrderService.checkout"},
			{StepID: "step-new", TechnicalName: "RefundService.issue"},
		},
	}

	symbol, side, sideMap := pulseNavigationTarget(semantic.DeltaChange{
		Kind: "removed_behavior", FromStepID: "step-gone", TargetStepID: "step-gone",
	}, baseMap, currMap)
	if symbol != "OrderService.cancel" || side != "baseline" || sideMap != baseMap {
		t.Errorf("removed change must navigate the baseline, got %q %q", symbol, side)
	}

	symbol, side, sideMap = pulseNavigationTarget(semantic.DeltaChange{
		Kind: "added_behavior", ToStepID: "step-new", TargetStepID: "step-new",
	}, baseMap, currMap)
	if symbol != "RefundService.issue" || side != "current" || sideMap != currMap {
		t.Errorf("added change must navigate the current generation, got %q %q", symbol, side)
	}

	symbol, side, sideMap = pulseNavigationTarget(semantic.DeltaChange{
		Kind: "changed_rule", FromStepID: "step-both", ToStepID: "step-both", TargetStepID: "step-both",
	}, baseMap, currMap)
	if symbol != "OrderService.checkout" || side != "current" || sideMap != currMap {
		t.Errorf("changed change must navigate the current endpoint, got %q %q", symbol, side)
	}

	symbol, side, sideMap = pulseNavigationTarget(semantic.DeltaChange{
		Kind: "removed_behavior", FromStepID: "step-missing", TargetStepID: "step-missing",
	}, baseMap, currMap)
	if symbol != "" || sideMap != nil {
		t.Errorf("unresolvable change must not navigate, got %q", symbol)
	}
}
