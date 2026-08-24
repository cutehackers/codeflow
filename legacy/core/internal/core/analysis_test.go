package core

import (
	"bytes"
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

	"codeflow/core/internal/flowir"
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
	if view.Header.Get("Content-Security-Policy") == "" || view.Header.Get("X-Frame-Options") != "DENY" || view.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("FlowView local security headers are incomplete: %#v", view.Header)
	}
	if !strings.Contains(string(html), "route:/signup") || !strings.Contains(string(html), "lib/signup.dart") {
		t.Fatalf("FlowView lacks real route evidence: %s", html)
	}
	publication, err := http.Get(c.URL + "/_codeflow/publication")
	if err != nil || publication.StatusCode != http.StatusOK {
		t.Fatalf("public FlowView publication probe unavailable: response=%v err=%v", publication, err)
	}
	var published struct {
		PublishedAt string `json:"published_at"`
		Status      string `json:"status"`
	}
	if err := json.NewDecoder(publication.Body).Decode(&published); err != nil {
		t.Fatal(err)
	}
	publication.Body.Close()
	if published.PublishedAt == "" || published.Status != "ready" {
		t.Fatalf("invalid publication probe: %#v", published)
	}
	foreign, _ := http.NewRequest(http.MethodGet, c.URL+"/", nil)
	foreign.Host = "attacker.invalid"
	foreignResponse, err := http.DefaultClient.Do(foreign)
	if err != nil {
		t.Fatal(err)
	}
	_ = foreignResponse.Body.Close()
	if foreignResponse.StatusCode != http.StatusMisdirectedRequest {
		t.Fatalf("foreign Host reached FlowView: %d", foreignResponse.StatusCode)
	}
	if err := c.Close(context.Background()); err != nil {
		t.Fatal(err)
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
	if !strings.Contains(string(html), `aria-label="조건 분기"`) || !strings.Contains(string(html), `data-unknown-reason="dynamic_dispatch"`) || !strings.Contains(string(html), `data-change="current"`) || !strings.Contains(string(html), `aria-label="아직 연결되지 않은 코드 동작"`) || !strings.Contains(string(html), `data-source-state="clean"`) {
		t.Fatalf("FlowView does not distinguish the vertical branch and unknown: %s", html)
	}
}

func TestMultiFlowWorkspacePublishesOneBasisAndNavigatesVerticalFlowView(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "lib"), 0755); err != nil {
		t.Fatal(err)
	}
	source := []byte("final routes = [\n  GoRoute(path: '/join', builder: (context, state) => const JoinPage()),\n  GoRoute(path: '/home', builder: (context, state) => const HomePage()),\n  GoRoute(path: '/auth', builder: (context, state) => const AuthPage()),\n];\nclass JoinPage {\n  const JoinPage();\n  void build() { ElevatedButton(onPressed: _continue); }\n  void _continue() { context.go('/home'); }\n}\nclass HomePage {\n  const HomePage();\n  void build() { ElevatedButton(onPressed: _signOut); }\n  void _signOut() { context.go('/auth'); }\n}\nclass AuthPage {\n  const AuthPage();\n  void build() { ElevatedButton(onPressed: _join); }\n  void _join() { context.go('/join'); }\n}\n")
	if err := os.WriteFile(filepath.Join(repo, "lib", "routes.dart"), source, 0644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("%s %v", out, err)
	}
	_ = exec.Command("git", "-C", repo, "add", ".").Run()
	_ = exec.Command("git", "-C", repo, "-c", "user.email=x@y.z", "-c", "user.name=x", "commit", "-qm", "fixture").Run()
	_, file, _, _ := runtime.Caller(0)
	adapter := "dart " + filepath.Join(filepath.Dir(file), "../../../adapters/dart/bin/codeflow-dart-adapter.dart")
	c, problem, err := StartAnalysis(context.Background(), repo, AnalysisOptions{Selectors: []string{"route:/join", "route:/home", "route:/auth"}, AdapterCommand: adapter})
	if err != nil || problem != nil {
		t.Fatalf("start workspace err=%v problem=%#v", err, problem)
	}
	defer c.Close(context.Background())
	if after, err := os.ReadFile(filepath.Join(repo, "lib", "routes.dart")); err != nil || !bytes.Equal(after, source) {
		t.Fatalf("analysis changed product source: err=%v after=%q", err, after)
	}

	unauthorized, err := http.Get(c.URL + "/api/v1/workspace")
	if err != nil {
		t.Fatal(err)
	}
	if unauthorized.StatusCode != http.StatusUnauthorized {
		t.Fatalf("workspace token gate=%d", unauthorized.StatusCode)
	}
	_ = unauthorized.Body.Close()

	request, _ := http.NewRequest(http.MethodGet, c.URL+"/api/v1/workspace", nil)
	request.Header.Set("X-CodeFlow-Token", c.Token)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var body struct {
		Basis flowir.Basis      `json:"basis"`
		Data  workspaceDocument `json:"data"`
	}
	if err = json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || len(body.Data.Flows) != 3 || len(body.Data.Edges) != 3 {
		t.Fatalf("workspace response status=%d data=%#v", response.StatusCode, body.Data)
	}
	if body.Data.Flows[0].Basis.WorktreeFingerprint != body.Data.Flows[1].Basis.WorktreeFingerprint || body.Basis.WorktreeFingerprint != body.Data.Basis.WorktreeFingerprint {
		t.Fatal("API mixed workspace bases")
	}
	v1Bytes, _ := json.Marshal(body)
	v2Request, _ := http.NewRequest(http.MethodGet, c.URL+"/api/v2/workspace", nil)
	v2Request.Header.Set("X-CodeFlow-Token", c.Token)
	v2Response, err := http.DefaultClient.Do(v2Request)
	if err != nil {
		t.Fatal(err)
	}
	v2Bytes, _ := io.ReadAll(v2Response.Body)
	_ = v2Response.Body.Close()
	var v2 map[string]any
	if err := json.Unmarshal(v2Bytes, &v2); err != nil {
		t.Fatal(err)
	}
	v2Basis, _ := v2["basis"].(map[string]any)
	v2Data, _ := v2["data"].(map[string]any)
	v2Flows, _ := v2Data["flows"].([]any)
	firstV2, _ := v2Flows[0].(map[string]any)
	if v2Response.StatusCode != http.StatusOK || v2Data["schema_version"] != "2" || v2Basis["manifest"] != nil || int(v2Basis["manifest_count"].(float64)) != len(body.Basis.Manifest) || firstV2["basis"] != nil {
		t.Fatalf("compact workspace contract violated: status=%d basis=%#v data=%#v", v2Response.StatusCode, v2Basis, v2Data)
	}
	if len(v2Bytes) >= len(v1Bytes) {
		t.Fatalf("compact workspace did not reduce repeated basis payload: v2=%d v1=%d", len(v2Bytes), len(v1Bytes))
	}
	view, err := http.Get(c.URL + "/?flow=route:%2Fauth")
	if err != nil {
		t.Fatal(err)
	}
	html, _ := io.ReadAll(view.Body)
	_ = view.Body.Close()
	page := string(html)
	for _, expected := range []string{`data-region="workspace-map"`, `data-workspace-flow="route:/join"`, `data-workspace-flow="route:/home"`, `data-workspace-flow="route:/auth"`, `data-from-flow="route:/join"`, `data-region="timeline"`, `VS Code에서 열기`, `route:/auth 코드 흐름`} {
		if !strings.Contains(page, expected) {
			t.Fatalf("multi-flow view missing %q", expected)
		}
	}
	// Removing one requested route must not publish the other flow alone. The
	// complete previous workspace remains visible and is marked analyzing.
	broken := []byte("final routes = [GoRoute(path: '/join', builder: (context, state) => const JoinPage()), GoRoute(path: '/home', builder: (context, state) => const HomePage())];\nclass JoinPage { const JoinPage(); void build() { ElevatedButton(onPressed: _continue); } void _continue() { context.go('/home'); } }\nclass HomePage { const HomePage(); void build() { ElevatedButton(onPressed: _continue); } void _continue() { context.go('/join'); } }\n")
	if err := os.WriteFile(filepath.Join(repo, "lib", "routes.dart"), broken, 0644); err != nil {
		t.Fatal(err)
	}
	if err := c.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	retained, _, status, err := c.store.GetBatch(context.Background())
	if err != nil || status != "analyzing" || len(retained) != 3 || retained[2].Current.ID != "route:/auth" {
		t.Fatalf("failed flow replaced the atomic workspace: status=%s flows=%#v err=%v", status, retained, err)
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
	realAdapter := filepath.Join(filepath.Dir(file), "../../../adapters/dart/bin/codeflow-dart-adapter.dart")
	starts := filepath.Join(t.TempDir(), "adapter-starts")
	adapter := filepath.Join(t.TempDir(), "adapter-wrapper")
	script := fmt.Sprintf("#!/bin/sh\nprintf x >> %q\nexec dart %q \"$@\"\n", starts, realAdapter)
	if err := os.WriteFile(adapter, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
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
	var current flowir.Document
	var status string
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		current, _, status, err = c.store.Get(context.Background(), "route:/signup")
		if err == nil && status == "ready" && current.Basis.WorktreeFingerprint != before.Basis.WorktreeFingerprint {
			break
		}
	}
	if err != nil || status != "ready" || current.Basis.WorktreeFingerprint == before.Basis.WorktreeFingerprint {
		t.Fatalf("watched modify did not publish a new consistent snapshot: err=%v status=%s", err, status)
	}
	started, err := os.ReadFile(starts)
	if err != nil || string(started) != "x" {
		t.Fatalf("reconcile restarted Dart Analyzer instead of reusing one session: starts=%q err=%v", started, err)
	}
	if err := os.Remove(filepath.Join(repo, "lib", "signup.dart")); err != nil {
		t.Fatal(err)
	}
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
