package flowview

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func copyFixtureWithoutCodeflow(t *testing.T, fixtureName string) string {
	t.Helper()
	src := filepath.Join("..", "..", "..", "test", "fixtures", fixtureName)
	dst, err := os.MkdirTemp("", "codeflow-test-ws-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dst) })

	err = filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if rel == ".codeflow" || strings.HasPrefix(rel, ".codeflow"+string(filepath.Separator)) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		targetPath := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(targetPath, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(targetPath, data, 0o644)
	})
	if err != nil {
		t.Fatalf("copy fixture failed: %v", err)
	}
	return dst
}

func TestRemovedEndpointsReturn404(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "codeflow-test-404-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	srv, err := NewServer(Config{
		RepoRoot: tmpDir,
		Port:     0,
	})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}
	defer func() { _ = srv.Shutdown(context.Background()) }()

	removedRoutes := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/workspace/stream"},
		{http.MethodGet, "/api/workspace/activity"},
		{http.MethodPost, "/api/workspace/edit"},
		{http.MethodGet, "/api/workspace/proof"},
		{http.MethodGet, "/api/live/generation"},
		{http.MethodGet, "/api/live/project"},
		{http.MethodGet, "/api/task/review"},
		{http.MethodGet, "/api/task/onboarding"},
		{http.MethodPost, "/api/release/capability"},
		{http.MethodGet, "/api/map"},
		{http.MethodPost, "/api/map/override"},
		{http.MethodGet, "/api/task/requirement"},
		{http.MethodGet, "/api/task/analyses"},
		{http.MethodGet, "/api/task/debug"},
		{http.MethodGet, "/api/task/incident"},
		{http.MethodPost, "/api/semantic/approve"},
		{http.MethodGet, "/api/semantic/approval-history"},
		{http.MethodGet, "/api/semantic/evidence-pack"},
	}

	for _, tc := range removedRoutes {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, "http://127.0.0.1"+tc.path+"?token="+srv.AuthToken(), nil)
			rec := httptest.NewRecorder()
			srv.httpServer.Handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusNotFound {
				t.Errorf("%s %s = %d, want 404 Not Found", tc.method, tc.path, rec.Code)
			}
		})
	}
}

func TestEssential12EndpointsRegistered(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "codeflow-test-12routes-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	srv, err := NewServer(Config{
		RepoRoot: tmpDir,
		Port:     0,
	})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}
	defer func() { _ = srv.Shutdown(context.Background()) }()

	routes := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/"},
		{http.MethodGet, "/api/flows"},
		{http.MethodGet, "/api/views"},
		{http.MethodGet, "/api/view"},
		{http.MethodGet, "/api/view/compare"},
		{http.MethodGet, "/api/flow"},
		{http.MethodGet, "/api/flow/context"},
		{http.MethodGet, "/api/source"},
		{http.MethodPost, "/api/approve"},
		{http.MethodGet, "/api/task/impact"},
		{http.MethodPost, "/api/semantic/labels"},
	}

	for _, tc := range routes {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, "http://127.0.0.1"+tc.path+"?token="+srv.AuthToken(), nil)
			rec := httptest.NewRecorder()
			srv.httpServer.Handler.ServeHTTP(rec, req)

			// An unregistered mux route returns literal "404 page not found\n"
			if strings.TrimSpace(rec.Body.String()) == "404 page not found" {
				t.Errorf("%s %s was not routed by mux (unregistered)", tc.method, tc.path)
			}
		})
	}
}
