package presenter_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"codeflow/internal/presenter"
)

func TestPresenterServer_RoutesAndPrunedEndpoints(t *testing.T) {
	tempDir := t.TempDir()
	srv, err := presenter.NewServer(presenter.Config{
		RepoRoot: tempDir,
		Port:     0,
		SvelteUI: true,
	})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	handler := srv.Handler()

	// 1. Verify official essential routes are registered (return non-404)
	essentialRoutes := []struct {
		method string
		path   string
	}{
		{"GET", "/"},
		{"GET", "/api/flows"},
		{"GET", "/api/views"},
		{"GET", "/api/view"},
		{"GET", "/api/view/compare"},
		{"GET", "/api/flow"},
		{"GET", "/api/flow/context"},
		{"GET", "/api/source"},
		{"POST", "/api/approve"},
		{"GET", "/api/task/impact"},
		{"POST", "/api/semantic/labels"},
	}

	for _, route := range essentialRoutes {
		req := httptest.NewRequest(route.method, "http://127.0.0.1"+route.path+"?token="+srv.AuthToken(), nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if strings.TrimSpace(rec.Body.String()) == "404 page not found" {
			t.Errorf("essential route %s %s was not routed by mux (unregistered)", route.method, route.path)
		}
	}

	// 2. Verify pruned routes return strictly 404
	prunedRoutes := []struct {
		method string
		path   string
	}{
		{"GET", "/api/workspace/stream"},
		{"GET", "/api/live/stream"},
		{"GET", "/api/task/onboarding"},
		{"GET", "/api/task/incident"},
		{"GET", "/api/task/debug"},
		{"GET", "/api/task/analyses"},
		{"GET", "/api/task/requirement"},
		{"POST", "/api/map/override"},
		{"GET", "/api/workspace/activity"},
		{"POST", "/api/workspace/edit"},
		{"GET", "/api/workspace/proof"},
	}

	for _, route := range prunedRoutes {
		req := httptest.NewRequest(route.method, "http://127.0.0.1"+route.path+"?token="+srv.AuthToken(), nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Errorf("pruned route %s %s returned %d, want 404", route.method, route.path, rec.Code)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
}
