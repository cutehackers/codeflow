package main

import (
	"bufio"
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

	flowcore "codeflow/core/internal/core"
	"codeflow/core/internal/flowir"
	flowruntime "codeflow/core/internal/runtime"
)

func TestRunRejectsUnknownCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if status := run([]string{"not-a-command"}, &stdout, &stderr); status != 2 {
		t.Fatalf("status = %d", status)
	}
	if stderr.Len() == 0 {
		t.Fatal("expected usage")
	}
}

func TestCacheStatusAndCleanAreScopedToReconstructableBaselines(t *testing.T) {
	repo := t.TempDir()
	baselineDir := filepath.Join(repo, ".codeflow/cache/baselines/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	if err := os.MkdirAll(baselineDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(baselineDir, "input.dart"), []byte("void main() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(repo, ".codeflow/state.db")
	if err := os.WriteFile(state, []byte("state"), 0644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if status := run([]string{"cache", "status", "--repo", repo, "--format", "json"}, &stdout, &stderr); status != 0 {
		t.Fatalf("status=%d stderr=%s", status, stderr.String())
	}
	if !bytes.Contains(stdout.Bytes(), []byte(`"total_bytes":15`)) {
		t.Fatalf("status output=%s", stdout.String())
	}
	stdout.Reset()
	stderr.Reset()
	if status := run([]string{"cache", "clean", "--repo", repo}, &stdout, &stderr); status != 0 {
		t.Fatalf("clean=%d stderr=%s", status, stderr.String())
	}
	if _, err := os.Stat(baselineDir); !os.IsNotExist(err) {
		t.Fatalf("baseline remains: %v", err)
	}
	if got, err := os.ReadFile(state); err != nil || string(got) != "state" {
		t.Fatalf("state changed: %q %v", got, err)
	}
}

func TestNativePackageLayoutRunsBundledBinaryAndAdapter(t *testing.T) {
	root := filepath.Clean(filepath.Join(filepath.Dir(mustCallerFile(t)), "../../.."))
	build := exec.Command("make", "package")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("package: %v %s", err, out)
	}
	repo := initRepo(t)
	binary := filepath.Join(root, "dist/codeflow/bin/codeflow")
	adapter := filepath.Join(root, "dist/codeflow/libexec/codeflow-dart-adapter")
	out, err := exec.Command(binary, "doctor", "--repo", repo, "--adapter", adapter, "--format", "json").CombinedOutput()
	if err == nil || !bytes.Contains(out, []byte(`"dart_adapter"`)) {
		t.Fatalf("bundled doctor output=%s err=%v", out, err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "lib"), 0755); err != nil {
		t.Fatal(err)
	}
	source := "final routes = [GoRoute(path: '/signup', builder: (c, s) => const SignupPage())];\nclass SignupPage { const SignupPage(); void build() { ElevatedButton(onPressed: _submit); } void _submit() { context.go('/done'); } }\n"
	if err := os.WriteFile(filepath.Join(repo, "lib", "routes.dart"), []byte(source), 0644); err != nil {
		t.Fatal(err)
	}
	_ = exec.Command("git", "-C", repo, "add", ".").Run()
	_ = exec.Command("git", "-C", repo, "-c", "user.email=x@y.z", "-c", "user.name=x", "commit", "-qm", "fixture").Run()
	out, err = exec.Command(binary, "analyze", "--repo", repo, "--adapter", adapter, "route:/signup").CombinedOutput()
	if err != nil || !bytes.Contains(out, []byte(`"id":"route:/signup"`)) || !bytes.Contains(out, []byte(`"status":"observed"`)) {
		t.Fatalf("bundled AOT adapter did not perform resolved analysis: %s err=%v", out, err)
	}
}

func TestReuseRuntimeRefreshesCompatibleCoreAndRejectsBadCache(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "lib"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "lib/signup.dart"), []byte("void signup() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	c, err := flowcore.StartFixture(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(context.Background())
	reused, url, err := reuseRuntime(repo)
	if err != nil || !reused || url != c.URL+"/" {
		t.Fatalf("reused=%v url=%q err=%v", reused, url, err)
	}
	var stdout, stderr bytes.Buffer
	if status := run([]string{"refresh", "--repo", repo}, &stdout, &stderr); status != 0 || !strings.Contains(stdout.String(), "CodeFlow refreshed: ready · 1 flow(s)") || !strings.Contains(stdout.String(), c.URL) {
		t.Fatalf("refresh did not attach to the live Core: status=%d stdout=%s stderr=%s", status, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if status := run([]string{"refresh", "--format", "json", "--repo", repo}, &stdout, &stderr); status != 0 || !bytes.Contains(stdout.Bytes(), []byte(`"status":"ready"`)) {
		t.Fatalf("JSON refresh contract failed: status=%d stdout=%s stderr=%s", status, stdout.String(), stderr.String())
	}
	if err := os.WriteFile(filepath.Join(repo, ".codeflow/runtime.json"), []byte(`{"runtime_version":"old"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if reused, _, err := reuseRuntime(repo); err != nil || reused {
		t.Fatalf("stale cache must not be reused: %v %v", reused, err)
	}
}

func TestPackagedPluginMCPUsesOneLiveCoreForCurrentDiffUnknownsAndOpen(t *testing.T) {
	root := filepath.Clean(filepath.Join(filepath.Dir(mustCallerFile(t)), "../../.."))
	build := exec.Command("make", "package")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("package: %v %s", err, out)
	}
	repo := initRepo(t)
	if err := os.MkdirAll(filepath.Join(repo, "lib"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "lib", "signup.dart"), []byte("void signup() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	core, err := flowcore.StartFixture(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	defer core.Close(context.Background())
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2026-07-28"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"current","arguments":{"flow_id":"route:/signup"}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"unknowns","arguments":{"flow_id":"route:/signup"}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"diff","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"open","arguments":{"flow_id":"route:/signup"}}}`}, "\n") + "\n"
	cmd := exec.Command(filepath.Join(root, "dist/codeflow/bin/codeflow"), "mcp", "--repo", repo)
	cmd.Stdin = strings.NewReader(input)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mcp: %v %s", err, out)
	}
	for _, required := range []string{`"id":2`, `"id":3`, `"id":4`, `"id":5`, `"status":"observed"`, core.URL} {
		if !strings.Contains(string(out), required) {
			t.Fatalf("missing %s: %s", required, out)
		}
	}
}

func TestPluginSessionHookFailureLeavesCurrentFlowDeterministic(t *testing.T) {
	root := filepath.Clean(filepath.Join(filepath.Dir(mustCallerFile(t)), "../../.."))
	repo := initRepo(t)
	if err := os.MkdirAll(filepath.Join(repo, "lib"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "lib", "signup.dart"), []byte("void signup() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	core, err := flowcore.StartFixture(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	defer core.Close(context.Background())
	before, err := core.Document(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	beforeJSON, err := json.Marshal(before)
	if err != nil {
		t.Fatal(err)
	}
	hook := filepath.Join(root, "plugins", "codeflow", "scripts", "session-refresh.sh")
	cmd := exec.Command(hook, repo)
	cmd.Env = append(os.Environ(), "CODEFLOW_BIN=/usr/bin/false")
	if err := cmd.Run(); err == nil {
		t.Fatal("expected refresh-only hook discovery failure")
	}
	after, err := core.Document(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	afterJSON, err := json.Marshal(after)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(beforeJSON, afterJSON) {
		t.Fatalf("hook failure changed current flow\nbefore=%s\nafter=%s", beforeJSON, afterJSON)
	}
}

func mustCallerFile(t *testing.T) string { t.Helper(); _, file, _, _ := runtime.Caller(0); return file }

func TestResolvedAdapterFindsLocalSourceCheckoutWithoutFlags(t *testing.T) {
	command := resolvedAdapter("")
	if !strings.HasPrefix(command, "dart ") || !strings.HasSuffix(command, "adapters/dart/bin/codeflow-dart-adapter.dart") {
		t.Fatalf("local source adapter was not discovered: %q", command)
	}
}

func TestAnalyzeAcceptsRepeatedFlowSelectorsAndReturnsWorkspace(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "lib"), 0755); err != nil {
		t.Fatal(err)
	}
	source := []byte("final routes = [\n  GoRoute(path: '/signup', builder: (context, state) => const SignupPage()),\n  GoRoute(path: '/settings', builder: (context, state) => const SettingsPage()),\n];\nclass SignupPage { const SignupPage(); void build() { ElevatedButton(onPressed: _submit); } void _submit() { context.go('/settings'); } }\nclass SettingsPage { const SettingsPage(); void build() { ElevatedButton(onPressed: _save); } void _save() { context.go('/signup'); } }\n")
	if err := os.WriteFile(filepath.Join(repo, "lib", "routes.dart"), source, 0644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %s %v", out, err)
	}
	_ = exec.Command("git", "-C", repo, "add", ".").Run()
	_ = exec.Command("git", "-C", repo, "-c", "user.email=x@y.z", "-c", "user.name=x", "commit", "-qm", "fixture").Run()
	_, file, _, _ := runtime.Caller(0)
	adapter := "dart " + filepath.Join(filepath.Dir(file), "../../../adapters/dart/bin/codeflow-dart-adapter.dart")
	var stdout, stderr bytes.Buffer
	exit := run([]string{"analyze", "--repo", repo, "--adapter", adapter, "--flow", "route:/signup", "--flow", "route:/settings"}, &stdout, &stderr)
	if exit != 0 {
		t.Fatalf("analyze exit=%d stderr=%s stdout=%s", exit, stderr.String(), stdout.String())
	}
	var result struct {
		FlowIDs []string `json:"flow_ids"`
		Flows   []struct {
			Current struct {
				ID string `json:"id"`
			} `json:"current"`
		} `json:"flows"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if strings.Join(result.FlowIDs, ",") != "route:/signup,route:/settings" || len(result.Flows) != 2 {
		t.Fatalf("workspace output=%#v", result)
	}
}

func TestMultiFlowCLIRejectsEmptyDuplicateAndOversizedSetsBeforeAnalysis(t *testing.T) {
	for _, test := range []struct {
		name string
		args []string
		code string
	}{
		{name: "empty", args: []string{"analyze", "--flow", " "}, code: "SELECTOR_REQUIRED"},
		{name: "duplicate", args: []string{"analyze", "--flow", "route:/join", "--flow", "route:/join"}, code: "DUPLICATE_SELECTOR"},
		{name: "too many", args: []string{"analyze", "--flow", "route:/a", "--flow", "route:/b", "--flow", "route:/c", "--flow", "route:/d"}, code: "FLOW_SET_TOO_LARGE"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if status := run(test.args, &stdout, &stderr); status != 1 || !strings.Contains(stdout.String(), `"Code":"`+test.code+`"`) {
				t.Fatalf("status/output = %d %s stderr=%s", status, stdout.String(), stderr.String())
			}
		})
	}
}

// TestServeHelperProcess exercises the same public command dispatch from a
// separate foreground process. It is deliberately not called as a normal test.
func TestServeHelperProcess(t *testing.T) {
	if os.Getenv("CODEFLOW_SERVE_HELPER") != "1" {
		return
	}
	os.Exit(run([]string{"serve", "--repo", os.Getenv("CODEFLOW_TARGET_REPO"), "--codegraph-url", "http://127.0.0.1:1", "--adapter", os.Getenv("CODEFLOW_TARGET_ADAPTER"), "route:/join"}, os.Stdout, os.Stderr))
}

func TestServePublicProcessPublishesReviewedJoinFlowAndCleansUp(t *testing.T) {
	repo := filepath.Join(os.Getenv("HOME"), "workspace", "sgp-981-app")
	if _, err := os.Stat(repo); err != nil {
		t.Skip("supplied target unavailable")
	}
	if _, err := flowruntime.ReadState(repo); err == nil {
		t.Skip("supplied target already has a running CodeFlow Core")
	}
	_, file, _, _ := runtime.Caller(0)
	adapter := "dart " + filepath.Join(filepath.Dir(file), "../../../adapters/dart/bin/codeflow-dart-adapter.dart")
	cmd := exec.Command(os.Args[0], "-test.run=TestServeHelperProcess")
	cmd.Env = append(os.Environ(), "CODEFLOW_SERVE_HELPER=1", "CODEFLOW_TARGET_REPO="+repo, "CODEFLOW_TARGET_ADAPTER="+adapter)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		t.Fatalf("serve did not publish review URL: %v stderr=%s", err, stderr.String())
	}
	const prefix = "CodeFlow review URL: "
	if !strings.HasPrefix(line, prefix) || !strings.HasPrefix(strings.TrimSpace(strings.TrimPrefix(line, prefix)), "http://127.0.0.1:") {
		t.Fatalf("unexpected serve output %q", line)
	}
	url := strings.TrimSpace(strings.TrimPrefix(line, prefix))
	flowURL := url + "api/v1/flows/?id=route:%2Fjoin"
	unauthenticated, err := http.Get(flowURL)
	if err != nil {
		t.Fatal(err)
	}
	if unauthenticated.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated API status=%d", unauthenticated.StatusCode)
	}
	_ = unauthenticated.Body.Close()
	state, err := flowruntime.ReadState(repo)
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodGet, flowURL, nil)
	req.Header.Set("X-CodeFlow-Token", state.AuthToken)
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || !bytes.Contains(body, []byte(`"route:/home"`)) || !bytes.Contains(body, []byte(`"route:/auth"`)) || !bytes.Contains(body, []byte(`"graph:owned_dart_structural"`)) {
		t.Fatalf("authenticated flow response status=%d body=%s", response.StatusCode, body)
	}
	var flowResponse struct {
		Unknowns []flowir.UnknownDetail `json:"unknowns"`
	}
	if err := json.Unmarshal(body, &flowResponse); err != nil {
		t.Fatalf("decode authenticated flow response: %v", err)
	}
	for _, unknown := range flowResponse.Unknowns {
		if unknown.Reason == "conditional_route_alternative" {
			t.Fatalf("current target still reports the statically resolved route alternative as unknown: %#v", unknown)
		}
	}
	page, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	html, _ := io.ReadAll(page.Body)
	_ = page.Body.Close()
	for _, required := range []string{`aria-label="조건 분기"`, `data-break-after="true"`, `data-alternative="true"`, `data-jump-step=`, `data-boundary="graph:owned_dart_structural"`, `aria-label="아키텍처 인과 흐름"`, `data-layer="state"`, `aria-label="코드 변경에서 상태와 사용자 결과까지의 영향"`, `data-state-change="true"`, `vscode://file/`, `join_page.dart:114-114`, `join_controller.dart:276-276`} {
		if !bytes.Contains(html, []byte(required)) {
			t.Fatalf("FlowView misses %s: %s", required, html)
		}
	}
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serve shutdown: %v stderr=%s", err, stderr.String())
		}
	case <-time.After(8 * time.Second):
		t.Fatal("serve did not shut down after interrupt")
	}
	if _, err := flowruntime.ReadState(repo); err == nil {
		t.Fatal("serve left a live runtime owner after shutdown")
	}
}

func TestResolveCLIUsesTheSameUniqueOnlySelectorContract(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "lib"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "lib", "routes.dart"), []byte("final r = GoRoute(path: '/signup', builder: x);\n"), 0644); err != nil {
		t.Fatal(err)
	}
	_, file, _, _ := runtime.Caller(0)
	adapter := "dart " + filepath.Join(filepath.Dir(file), "../../../adapters/dart/bin/codeflow-dart-adapter.dart")
	var stdout, stderr bytes.Buffer
	status := run([]string{"resolve", "--repo", repo, "--adapter", adapter, "signup"}, &stdout, &stderr)
	var result struct {
		State      string `json:"state"`
		EntryPoint struct {
			FlowID string `json:"flow_id"`
		} `json:"entry_point"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || status != 0 || result.State != "ready" || result.EntryPoint.FlowID != "route:/signup" {
		t.Fatalf("status=%d result=%s err=%v stderr=%s", status, stdout.String(), err, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	status = run([]string{"resolve", "--repo", repo}, &stdout, &stderr)
	result = struct {
		State      string `json:"state"`
		EntryPoint struct {
			FlowID string `json:"flow_id"`
		} `json:"entry_point"`
	}{}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || status != 0 || result.EntryPoint.FlowID != "route:/signup" {
		t.Fatalf("zero-config unique resolve status=%d result=%s err=%v stderr=%s", status, stdout.String(), err, stderr.String())
	}
}

func TestVerifyCLIEnforcesAnExplicitLocalFlowContract(t *testing.T) {
	repo := t.TempDir()
	source := []byte("final routes=[GoRoute(path: '/signup',builder:(c,s)=>const SignupPage())]; class SignupPage { void build(){ ElevatedButton(onPressed: _submit); } void _submit(){ _navigate(); } void _navigate(){ context.go('/welcome'); } }\n")
	if err := os.MkdirAll(filepath.Join(repo, "lib"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "lib", "signup.dart"), source, 0644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	exec.Command("git", "-C", repo, "add", ".").Run()
	exec.Command("git", "-C", repo, "-c", "user.email=x@y.z", "-c", "user.name=x", "commit", "-qm", "fixture").Run()
	revision := strings.TrimSpace(string(mustOutput(t, "git", "-C", repo, "rev-parse", "HEAD")))
	graph := comparisonGraph(t, revision, revision, flowir.SHA256Bytes(source), flowir.SHA256Bytes(source))
	defer graph.Close()
	contract := filepath.Join(t.TempDir(), "expectations.json")
	if err := os.WriteFile(contract, []byte(`{"version":"1","flows":{"route:/signup":{"required_results":["route:/welcome"],"required_causal_kinds":["produces"],"allowed_debt_reasons":[],"max_open_debt":0}}}`), 0644); err != nil {
		t.Fatal(err)
	}
	_, file, _, _ := runtime.Caller(0)
	adapter := "dart " + filepath.Join(filepath.Dir(file), "../../../adapters/dart/bin/codeflow-dart-adapter.dart")
	var stdout, stderr bytes.Buffer
	status := run([]string{"verify", "--repo", repo, "--codegraph-url", graph.URL, "--adapter", adapter, "--expectations", contract, "signup"}, &stdout, &stderr)
	if status != 0 || stderr.Len() != 0 || !strings.Contains(stdout.String(), `"ready":true`) {
		t.Fatalf("status=%d stdout=%s stderr=%s", status, stdout.String(), stderr.String())
	}
}

func TestDoctorJSONWritesOnlyJSONToStdout(t *testing.T) {
	repo := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	var stdout, stderr bytes.Buffer
	status := run([]string{"doctor", "--repo", repo, "--format", "json"}, &stdout, &stderr)
	if status != 1 {
		t.Fatalf("status = %d", status)
	}
	var report map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("stdout must be JSON only: %q (%v)", stdout.String(), err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("unexpected stderr: %q", stderr.String())
	}
	resolved, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Clean(report["repository"].(string)) != resolved {
		t.Fatalf("repository = %#v", report["repository"])
	}
}

func TestDoctorCommandFixtures(t *testing.T) {
	server := compatibleCodeGraph(t)
	adapter := filepath.Join(t.TempDir(), "adapter")
	if err := os.WriteFile(adapter, []byte("#!/bin/sh\nprintf '{\"protocol_version\":\"1\",\"status\":\"ready\"}\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	t.Run("ready without configuration", func(t *testing.T) {
		repo := initRepo(t)
		report, stderr, status := invokeDoctor(t, repo, server.URL, adapter)
		if status != 0 || stderr != "" || report["ready"] != true {
			t.Fatalf("status=%d stderr=%q report=%#v", status, stderr, report)
		}
		if stateOf(t, report, "configuration") != "unconfigured" {
			t.Fatalf("configuration = %#v", report["checks"])
		}
	})

	t.Run("partial with unavailable adapter", func(t *testing.T) {
		repo := initRepo(t)
		report, _, status := invokeDoctor(t, repo, server.URL, filepath.Join(repo, "missing-adapter"))
		if status != 1 || report["ready"] != false {
			t.Fatalf("status=%d report=%#v", status, report)
		}
		if stateOf(t, report, "dart_adapter") != "unavailable" {
			t.Fatalf("adapter = %#v", report["checks"])
		}
	})

	t.Run("incompatible configuration", func(t *testing.T) {
		repo := initRepo(t)
		if err := os.WriteFile(filepath.Join(repo, "codeflow.yaml"), []byte("schema_version: \"99\"\nrepository: {id: fixture}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		report, _, status := invokeDoctor(t, repo, server.URL, adapter)
		if status != 2 || report["ready"] != false {
			t.Fatalf("status=%d report=%#v", status, report)
		}
		if stateOf(t, report, "configuration") != "incompatible" {
			t.Fatalf("configuration = %#v", report["checks"])
		}
	})
}

func TestCompareCLIPrintsImmutableBaselineDelta(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "lib"), 0755); err != nil {
		t.Fatal(err)
	}
	baseSource := []byte("final routes=[GoRoute(path: '/signup',builder:(c,s)=>const SignupPage())]; class SignupPage { void build(){ ElevatedButton(onPressed: _submit); } void _submit(){ _navigate(); } void _navigate(){ context.go('/welcome'); } }\n")
	if err := os.WriteFile(filepath.Join(repo, "lib/signup.dart"), baseSource, 0644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatal(string(out), err)
	}
	exec.Command("git", "-C", repo, "add", ".").Run()
	exec.Command("git", "-C", repo, "-c", "user.email=x@y.z", "-c", "user.name=x", "commit", "-qm", "base").Run()
	base := strings.TrimSpace(string(mustOutput(t, "git", "-C", repo, "rev-parse", "HEAD")))
	currentSource := []byte("final routes=[GoRoute(path: '/signup',builder:(c,s)=>const SignupPage())]; class SignupPage { void build(){ ElevatedButton(onPressed: _submit); } void _submit(){ _navigate(); } void _navigate(){ if (approved) { context.go('/welcome'); } else { dynamic fallback; fallback.go('/retry'); } } }\n")
	if err := os.WriteFile(filepath.Join(repo, "lib/signup.dart"), currentSource, 0644); err != nil {
		t.Fatal(err)
	}
	exec.Command("git", "-C", repo, "add", ".").Run()
	exec.Command("git", "-C", repo, "-c", "user.email=x@y.z", "-c", "user.name=x", "commit", "-qm", "current").Run()
	current := strings.TrimSpace(string(mustOutput(t, "git", "-C", repo, "rev-parse", "HEAD")))
	server := comparisonGraph(t, base, current, flowir.SHA256Bytes(baseSource), flowir.SHA256Bytes(currentSource))
	defer server.Close()
	_, file, _, _ := runtime.Caller(0)
	adapter := "dart " + filepath.Join(filepath.Dir(file), "../../../adapters/dart/bin/codeflow-dart-adapter.dart")
	var stdout, stderr bytes.Buffer
	status := run([]string{"compare", "--repo", repo, "--baseline", base, "--codegraph-url", server.URL, "--adapter", adapter, "signup"}, &stdout, &stderr)
	if status != 0 || stderr.Len() != 0 || !strings.Contains(stdout.String(), `"baseline_revision"`) {
		t.Fatalf("status=%d stdout=%s stderr=%s", status, stdout.String(), stderr.String())
	}
}

func comparisonGraph(t *testing.T, base, current, baseHash, currentHash string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.Write([]byte(`{"status":"ok"}`))
		case "/api/v1/tools":
			w.Write([]byte(`{"tools":[{"name":"analyze_code_relationships"}]}`))
		case "/api/v1/tools/call":
			var req struct {
				Arguments map[string]any `json:"arguments"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			root := req.Arguments["repository"].(string)
			b, _ := os.ReadFile(filepath.Join(root, "lib/signup.dart"))
			hash := flowir.SHA256Bytes(b)
			rev := current
			if hash == baseHash {
				rev = base
			}
			fmt.Fprintf(w, `{"result":{"relationships":[{"kind":"call","from":{"path":"lib/signup.dart","symbol":"SignupPage._submit","byte_start":0,"byte_end":%d,"file_hash":"%s","revision":"%s"},"to":{"path":"lib/signup.dart","symbol":"SignupPage._navigate","byte_start":0,"byte_end":%d,"file_hash":"%s","revision":"%s"}}]}}`, len(b), hash, rev, len(b), hash, rev)
		default:
			w.WriteHeader(404)
		}
	}))
}
func mustOutput(t *testing.T, name string, args ...string) []byte {
	t.Helper()
	out, err := exec.Command(name, args...).Output()
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func compatibleCodeGraph(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/health":
			_, _ = writer.Write([]byte(`{"status":"ok"}`))
		case "/api/v1/status":
			_, _ = writer.Write([]byte(`{"database":"connected","backend":"kuzu"}`))
		case "/api/v1/tools":
			_, _ = writer.Write([]byte(`{"tools":[{"name":"analyze_code_relationships","inputSchema":{"properties":{"query_type":{"type":"string"},"target":{"type":"string"}}}}]}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func initRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	return repo
}

func invokeDoctor(t *testing.T, repo, codeGraphURL, adapter string) (map[string]any, string, int) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	status := run([]string{"doctor", "--repo", repo, "--codegraph-url", codeGraphURL, "--adapter", adapter, "--format", "json"}, &stdout, &stderr)
	var report map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("stdout must be JSON: %q (%v)", stdout.String(), err)
	}
	return report, stderr.String(), status
}

func stateOf(t *testing.T, report map[string]any, name string) string {
	t.Helper()
	checks, ok := report["checks"].([]any)
	if !ok {
		t.Fatalf("checks = %#v", report["checks"])
	}
	for _, rawCheck := range checks {
		check, ok := rawCheck.(map[string]any)
		if ok && check["name"] == name {
			return check["state"].(string)
		}
	}
	t.Fatalf("missing check %q in %#v", name, checks)
	return ""
}
