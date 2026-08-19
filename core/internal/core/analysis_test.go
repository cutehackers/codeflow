package core

import (
	"codeflow/core/internal/flowir"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"codeflow/core/internal/manifest"
)

func TestAnalysisCoreServesRealRouteFlowAndRejectsStaleGraph(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "lib"), 0755); err != nil {
		t.Fatal(err)
	}
	source := []byte("final routes = [GoRoute(path: '/signup', builder: (context, state) => const SignupPage())];\nclass SignupPage {\n void build() { ElevatedButton(onPressed: _submit); }\n void _submit() { _navigate(); }\n void _navigate() { context.go('/welcome'); }\n}\n")
	if err := os.WriteFile(filepath.Join(repo, "lib", "signup.dart"), source, 0644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("%s %v", out, err)
	}
	exec.Command("git", "-C", repo, "add", ".").Run()
	exec.Command("git", "-C", repo, "-c", "user.email=x@y.z", "-c", "user.name=x", "commit", "-qm", "fixture").Run()
	graph := analysisGraph(t, repo, flowir.SHA256Bytes(source))
	defer graph.Close()
	_, file, _, _ := runtime.Caller(0)
	adapter := "dart " + filepath.Join(filepath.Dir(file), "../../../adapters/dart/bin/codeflow-dart-adapter.dart")
	c, problem, err := StartAnalysis(context.Background(), repo, AnalysisOptions{Selector: "signup", CodeGraphURL: graph.URL, AdapterCommand: adapter})
	if err != nil || problem != nil {
		t.Fatalf("err=%v problem=%#v", err, problem)
	}
	defer c.Close(context.Background())
	req, _ := http.NewRequest(http.MethodGet, c.URL+"/api/v1/flows/ignored?id=route:%2Fsignup", nil)
	req.Header.Set("X-CodeFlow-Token", c.Token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body struct {
		Data flowir.Document `json:"data"`
	}
	if err = json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 || body.Data.Current.ID != "route:/signup" || len(body.Data.Architecture.Relations) == 0 || len(body.Data.Current.Steps) != 2 {
		t.Fatalf("status=%d document=%#v", resp.StatusCode, body.Data)
	}
	view, err := http.Get(c.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	html, _ := io.ReadAll(view.Body)
	view.Body.Close()
	if !strings.Contains(string(html), "route:/signup") || !strings.Contains(string(html), "lib/signup.dart") {
		t.Fatalf("FlowView lacks real route evidence: %s", html)
	}
	stale := analysisGraph(t, repo, "sha256:stale")
	defer stale.Close()
	c2, p2, e2 := StartAnalysis(context.Background(), repo, AnalysisOptions{Selector: "signup", CodeGraphURL: stale.URL, AdapterCommand: adapter})
	if c2 != nil {
		c2.Close(context.Background())
	}
	if e2 != nil || p2 == nil || p2.Code != "STALE_GRAPH" {
		t.Fatalf("stale err=%v problem=%#v", e2, p2)
	}
}

func TestAnalysisCorePublishesBranchAndDynamicUnknownToAPIAndFlowView(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "lib"), 0755); err != nil {
		t.Fatal(err)
	}
	source := []byte("final routes = [GoRoute(path: '/signup', builder: (context, state) => const SignupPage())];\nclass SignupPage {\n void build() { ElevatedButton(onPressed: _submit); }\n void _submit() { _navigate(); }\n void _navigate() { if (approved) { context.go('/welcome'); } else { dynamic fallback; fallback.go('/retry'); } }\n}\n")
	if err := os.WriteFile(filepath.Join(repo, "lib", "signup.dart"), source, 0644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("%s %v", out, err)
	}
	exec.Command("git", "-C", repo, "add", ".").Run()
	exec.Command("git", "-C", repo, "-c", "user.email=x@y.z", "-c", "user.name=x", "commit", "-qm", "fixture").Run()
	graph := analysisGraph(t, repo, flowir.SHA256Bytes(source))
	defer graph.Close()
	_, file, _, _ := runtime.Caller(0)
	adapter := "dart " + filepath.Join(filepath.Dir(file), "../../../adapters/dart/bin/codeflow-dart-adapter.dart")
	c, problem, err := StartAnalysis(context.Background(), repo, AnalysisOptions{Selector: "signup", CodeGraphURL: graph.URL, AdapterCommand: adapter})
	if err != nil || problem != nil {
		t.Fatalf("err=%v problem=%#v", err, problem)
	}
	defer c.Close(context.Background())
	req, _ := http.NewRequest(http.MethodGet, c.URL+"/api/v1/flows/ignored?id=route:%2Fsignup", nil)
	req.Header.Set("X-CodeFlow-Token", c.Token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body struct {
		Data     flowir.Document        `json:"data"`
		Unknowns []flowir.UnknownDetail `json:"unknowns"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Data.Current.Status != flowir.Mixed || len(body.Data.Current.Steps) != 4 || len(body.Unknowns) != 1 || body.Unknowns[0].Reason != "dynamic_dispatch" {
		t.Fatalf("API did not retain branch uncertainty: %#v", body)
	}
	view, err := http.Get(c.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	html, _ := io.ReadAll(view.Body)
	view.Body.Close()
	if !strings.Contains(string(html), `aria-label="Branch"`) || !strings.Contains(string(html), `data-unknown-reason="dynamic_dispatch"`) || !strings.Contains(string(html), `data-change="current"`) || !strings.Contains(string(html), `aria-label="Cognitive debt"`) || !strings.Contains(string(html), `data-source-state="clean"`) {
		t.Fatalf("FlowView does not distinguish the vertical branch and unknown: %s", html)
	}
}

func TestAnalysisReconcileModifyDeleteRenameAndRapidRewriteNeverPublishesMixedSnapshot(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "lib"), 0755); err != nil {
		t.Fatal(err)
	}
	source := []byte("final routes = [GoRoute(path: '/signup', builder: (context, state) => const SignupPage())];\nclass SignupPage { void build() { ElevatedButton(onPressed: _submit); } void _submit() { _navigate(); } void _navigate() { context.go('/welcome'); } }\n")
	if err := os.WriteFile(filepath.Join(repo, "lib", "signup.dart"), source, 0644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("%s %v", out, err)
	}
	exec.Command("git", "-C", repo, "add", ".").Run()
	exec.Command("git", "-C", repo, "-c", "user.email=x@y.z", "-c", "user.name=x", "commit", "-qm", "fixture").Run()
	graph := changingAnalysisGraph(t, repo)
	defer graph.Close()
	_, file, _, _ := runtime.Caller(0)
	adapter := "dart " + filepath.Join(filepath.Dir(file), "../../../adapters/dart/bin/codeflow-dart-adapter.dart")
	c, problem, err := StartAnalysis(context.Background(), repo, AnalysisOptions{Selector: "signup", CodeGraphURL: graph.URL, AdapterCommand: adapter})
	if err != nil || problem != nil {
		t.Fatalf("start err=%v problem=%#v", err, problem)
	}
	defer c.Close(context.Background())
	before, _, _, err := c.store.Get(context.Background(), "route:/signup")
	if err != nil {
		t.Fatal(err)
	}
	changed := []byte(strings.Replace(string(source), "/welcome", "/welcome2", 1))
	if err := os.WriteFile(filepath.Join(repo, "lib", "signup.dart"), changed, 0644); err != nil {
		t.Fatal(err)
	}
	if err := c.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	current, _, status, err := c.store.Get(context.Background(), "route:/signup")
	if err != nil || status != "ready" || current.Basis.WorktreeFingerprint == before.Basis.WorktreeFingerprint {
		t.Fatalf("modify did not publish a new consistent snapshot: err=%v status=%s", err, status)
	}
	if err := os.Remove(filepath.Join(repo, "lib", "signup.dart")); err != nil {
		t.Fatal(err)
	}
	c.ScheduleReconcile()
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		_, _, scheduled, _ := c.store.Get(context.Background(), "route:/signup")
		if scheduled == "analyzing" {
			break
		}
	}
	deleted, _, status, err := c.store.Get(context.Background(), "route:/signup")
	if err != nil || status != "analyzing" || deleted.Basis.WorktreeFingerprint != current.Basis.WorktreeFingerprint {
		t.Fatalf("delete must retain last consistent snapshot: %v %s", err, status)
	}
	if err := os.WriteFile(filepath.Join(repo, "lib", "renamed.dart"), changed, 0644); err != nil {
		t.Fatal(err)
	}
	if err := c.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	renamed, _, status, _ := c.store.Get(context.Background(), "route:/signup")
	if status != "analyzing" || renamed.Basis.WorktreeFingerprint != current.Basis.WorktreeFingerprint {
		t.Fatal("rename without a matching current graph anchor published mixed evidence")
	}
	if err := os.Rename(filepath.Join(repo, "lib", "renamed.dart"), filepath.Join(repo, "lib", "signup.dart")); err != nil {
		t.Fatal(err)
	}
	c.capture = func(string) (flowir.Basis, error) { return flowir.Basis{}, manifest.ErrChanging }
	if err := c.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	rapid, _, status, _ := c.store.Get(context.Background(), "route:/signup")
	if status != "analyzing" || rapid.Basis.WorktreeFingerprint != current.Basis.WorktreeFingerprint {
		t.Fatal("rapid rewrite must not publish a partial snapshot")
	}
	c.capture = manifest.Capture
	if err := c.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	final, _, status, err := c.store.Get(context.Background(), "route:/signup")
	if err != nil || status != "ready" || final.Basis.WorktreeFingerprint != current.Basis.WorktreeFingerprint {
		t.Fatalf("public refresh did not recover the missed event: %v %s", err, status)
	}
	request, _ := http.NewRequest(http.MethodPost, c.URL+"/api/v1/refresh", nil)
	request.Header.Set("X-CodeFlow-Token", c.Token)
	response, err := http.DefaultClient.Do(request)
	if err != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("API refresh=%v status=%v", err, response)
	}
	response.Body.Close()
}

func changingAnalysisGraph(t *testing.T, repo string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		case "/api/v1/tools":
			_, _ = w.Write([]byte(`{"tools":[{"name":"analyze_code_relationships"}]}`))
		case "/api/v1/tools/call":
			b, _ := os.ReadFile(filepath.Join(repo, "lib", "signup.dart"))
			head, _ := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
			_, _ = fmt.Fprintf(w, `{"result":{"relationships":[{"kind":"call","from":{"path":"lib/signup.dart","symbol":"SignupPage._submit","byte_start":0,"byte_end":%d,"file_hash":"%s","revision":"%s"},"to":{"path":"lib/signup.dart","symbol":"SignupPage._navigate","byte_start":0,"byte_end":%d,"file_hash":"%s","revision":"%s"}}]}}`, len(b), flowir.SHA256Bytes(b), strings.TrimSpace(string(head)), len(b), flowir.SHA256Bytes(b), strings.TrimSpace(string(head)))
		default:
			w.WriteHeader(404)
		}
	}))
}
func analysisGraph(t *testing.T, repo, hash string) *httptest.Server {
	t.Helper()
	b, _ := os.ReadFile(filepath.Join(repo, "lib", "signup.dart"))
	head, _ := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	rev := strings.TrimSpace(string(head))
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		case "/api/v1/tools":
			_, _ = w.Write([]byte(`{"tools":[{"name":"analyze_code_relationships"}]}`))
		case "/api/v1/tools/call":
			_, _ = fmt.Fprintf(w, `{"result":{"relationships":[{"kind":"call","from":{"path":"lib/signup.dart","symbol":"SignupPage._submit","byte_start":0,"byte_end":%d,"file_hash":"%s","revision":"%s"},"to":{"path":"lib/signup.dart","symbol":"SignupPage._navigate","byte_start":0,"byte_end":%d,"file_hash":"%s","revision":"%s"}}]}}`, len(b), hash, rev, len(b), hash, rev)
		default:
			w.WriteHeader(404)
		}
	}))
}
