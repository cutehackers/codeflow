package doctor

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDiagnoseReadyFixtureDoesNotModifyGitWorktree(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test fixture uses a shell script")
	}
	repo := newGitRepo(t)
	write(t, filepath.Join(repo, "codeflow.yaml"), `schema_version: "1"
repository: {id: doctor-fixture}
features:
  signup: {entry_point: "route:/signup"}
`)
	adapter := filepath.Join(t.TempDir(), "adapter")
	write(t, adapter, "#!/bin/sh\nprintf '{\"protocol_version\":\"1\",\"status\":\"ready\"}\\n'\n")
	if err := os.Chmod(adapter, 0o755); err != nil {
		t.Fatal(err)
	}
	server := compatibleServer(t)
	before := gitOutput(t, repo, "status", "--porcelain=v1")
	report := Diagnose(context.Background(), Options{Repo: repo, CodeGraphURL: server.URL, AdapterPath: adapter})
	after := gitOutput(t, repo, "status", "--porcelain=v1")
	if before != after {
		t.Fatalf("doctor changed git worktree: before %q after %q", before, after)
	}
	if !report.Ready {
		t.Fatalf("expected ready report: %#v", report)
	}
	if got := check(report, "configuration"); got.State != Ready {
		t.Fatalf("configuration = %#v", got)
	}
}

func TestDiagnoseReadyWithoutConfigCreatesNoProductFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test fixture uses a shell script")
	}
	repo := newGitRepo(t)
	adapter := filepath.Join(t.TempDir(), "adapter")
	write(t, adapter, "#!/bin/sh\nprintf '{\"protocol_version\":\"1\",\"status\":\"ready\"}\\n'\n")
	if err := os.Chmod(adapter, 0o755); err != nil {
		t.Fatal(err)
	}
	server := compatibleServer(t)
	before := gitOutput(t, repo, "status", "--porcelain=v1")
	report := Diagnose(context.Background(), Options{Repo: repo, CodeGraphURL: server.URL, AdapterPath: adapter})
	after := gitOutput(t, repo, "status", "--porcelain=v1")
	if !report.Ready {
		t.Fatalf("expected ready report without configuration: %#v", report)
	}
	if got := check(report, "configuration"); got.State != Unconfigured {
		t.Fatalf("configuration = %#v", got)
	}
	if before != after {
		t.Fatalf("doctor changed git worktree: before %q after %q", before, after)
	}
	for _, relativePath := range []string{
		"codeflow.yaml",
		".codeflow",
		".codeflow/runtime.json",
		".codeflow/cache",
		".codeflow/codeflow.lock",
	} {
		if _, err := os.Lstat(filepath.Join(repo, relativePath)); !os.IsNotExist(err) {
			t.Fatalf("doctor created or left %s: %v", relativePath, err)
		}
	}
}

func TestDiagnoseReportsIndependentPartialAndIncompatibleChecks(t *testing.T) {
	repo := newGitRepo(t)
	server := compatibleServer(t)
	report := Diagnose(context.Background(), Options{Repo: repo, CodeGraphURL: server.URL})
	if report.Ready {
		t.Fatal("an absent adapter must leave the repository not ready")
	}
	if got := check(report, "configuration"); got.State != Unconfigured {
		t.Fatalf("optional config = %#v", got)
	}
	if got := check(report, "codegraph"); got.State != Ready {
		t.Fatalf("codegraph = %#v", got)
	}
	if got := check(report, "dart_adapter"); got.State != Unavailable {
		t.Fatalf("adapter = %#v", got)
	}

	write(t, filepath.Join(repo, "codeflow.yaml"), "schema_version: \"99\"\nrepository: {id: demo}\n")
	report = Diagnose(context.Background(), Options{Repo: repo, CodeGraphURL: server.URL})
	if got := check(report, "configuration"); got.State != Incompatible || !strings.Contains(got.Message, "unsupported") {
		t.Fatalf("config = %#v", got)
	}
}

func TestCodeGraphRejectsMissingDiscoveryTools(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/health" {
			_, _ = writer.Write([]byte(`{}`))
			return
		}
		if request.URL.Path == "/api/v1/tools" {
			_, _ = writer.Write([]byte(`{"tools":[{"name":"find_code"}]}`))
			return
		}
		if request.URL.Path != "/api/v1/status" {
			http.NotFound(writer, request)
			return
		}
		_, _ = writer.Write([]byte(`{"database":"connected","backend":"kuzu"}`))
	}))
	defer server.Close()
	check := checkCodeGraph(context.Background(), Options{CodeGraphURL: server.URL, HTTPClient: server.Client()})
	if check.State != Incompatible || check.Details["required_schema"] == "" {
		t.Fatalf("check = %#v", check)
	}
}

func compatibleServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/health" {
			_, _ = writer.Write([]byte(`{}`))
			return
		}
		if request.URL.Path == "/api/v1/tools" {
			_, _ = writer.Write([]byte(`{"tools":[{"name":"analyze_code_relationships","inputSchema":{"properties":{"query_type":{"type":"string"},"target":{"type":"string"}}}}]}`))
			return
		}
		if request.URL.Path != "/api/v1/status" {
			http.NotFound(writer, request)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"database":"connected","backend":"kuzu"}`))
	}))
}

func newGitRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	command := exec.Command("git", "init", "-q", repo)
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	return repo
}

func gitOutput(t *testing.T, repo string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", repo}, args...)...)
	out, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func check(report Report, name string) Check {
	for _, candidate := range report.Checks {
		if candidate.Name == name {
			return candidate
		}
	}
	return Check{Name: name, State: Invalid, Message: "missing check"}
}
