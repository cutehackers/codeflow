package comparison

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"codeflow/core/internal/flowir"
)

func TestBuildCompilesImmutableBaselineAndLeavesProductWorktreeUntouched(t *testing.T) {
	repo := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(repo, "lib"), 0755))
	baseSource := "final routes=[GoRoute(path: '/signup',builder:(c,s)=>const SignupPage())];\nclass SignupPage { void build(){ ElevatedButton(onPressed: _submit); } void _submit(){ _navigate(); } void _navigate(){ context.go('/welcome'); } }\n"
	must(t, os.WriteFile(filepath.Join(repo, "lib/signup.dart"), []byte(baseSource), 0644))
	git(t, repo, "init", "-q")
	git(t, repo, "add", ".")
	git(t, repo, "-c", "user.email=x@y.z", "-c", "user.name=x", "commit", "-qm", "base")
	base := strings.TrimSpace(string(gitOut(t, repo, "rev-parse", "HEAD")))
	currentSource := "final routes=[GoRoute(path: '/signup',builder:(c,s)=>const SignupPage())];\nclass SignupPage { void build(){ ElevatedButton(onPressed: _submit); } void _submit(){ _navigate(); } void _navigate(){ if (approved) { context.go('/welcome'); } else { dynamic fallback; fallback.go('/retry'); } } }\n"
	must(t, os.WriteFile(filepath.Join(repo, "lib/signup.dart"), []byte(currentSource), 0644))
	git(t, repo, "add", ".")
	git(t, repo, "-c", "user.email=x@y.z", "-c", "user.name=x", "commit", "-qm", "current")
	current := strings.TrimSpace(string(gitOut(t, repo, "rev-parse", "HEAD")))
	before := string(gitOut(t, repo, "status", "--porcelain"))
	server := graph(t, base, current, flowir.SHA256Bytes([]byte(baseSource)), flowir.SHA256Bytes([]byte(currentSource)))
	defer server.Close()
	_, file, _, _ := runtime.Caller(0)
	adapter := "dart " + filepath.Join(filepath.Dir(file), "../../../adapters/dart/bin/codeflow-dart-adapter.dart")
	result, problem, err := Build(context.Background(), Options{Repo: repo, Revision: base, Selector: "signup", CodeGraphURL: server.URL, AdapterCommand: adapter})
	if err != nil || problem != nil {
		t.Fatalf("err=%v problem=%#v", err, problem)
	}
	if result.Baseline.Basis.HeadRevision != base || result.Current.Basis.HeadRevision == base || len(result.Delta.AddedSteps) == 0 || len(result.Delta.NewUnknowns) != 1 {
		t.Fatalf("unexpected comparison %#v", result)
	}
	if after := string(gitOut(t, repo, "status", "--porcelain")); after != before+"?? .codeflow/\n" {
		t.Fatalf("comparison changed product worktree: before=%q after=%q", before, after)
	}
}
func graph(t *testing.T, base, current string, baseHash, currentHash string) *httptest.Server {
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
			json.NewDecoder(r.Body).Decode(&req)
			root := req.Arguments["repository"].(string)
			b, _ := os.ReadFile(filepath.Join(root, "lib/signup.dart"))
			hash := flowir.SHA256Bytes(b)
			rev := ""
			if hash == baseHash {
				rev = base
			} else if hash == currentHash {
				rev = current
			}
			fmt.Fprintf(w, `{"result":{"relationships":[{"kind":"call","from":{"path":"lib/signup.dart","symbol":"SignupPage._submit","byte_start":0,"byte_end":%d,"file_hash":"%s","revision":"%s"},"to":{"path":"lib/signup.dart","symbol":"SignupPage._navigate","byte_start":0,"byte_end":%d,"file_hash":"%s","revision":"%s"}}]}}`, len(b), hash, rev, len(b), hash, rev)
		default:
			w.WriteHeader(404)
		}
	}))
}
func git(t *testing.T, repo string, args ...string) {
	t.Helper()
	if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
		t.Fatalf("git %v: %s", args, out)
	}
}
func gitOut(t *testing.T, repo string, args ...string) []byte {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).Output()
	if err != nil {
		t.Fatal(err)
	}
	return out
}
func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
