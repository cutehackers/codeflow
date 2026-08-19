package entrypoint

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func adapterCommand(t *testing.T) string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "../../.."))
	return "dart " + filepath.Join(root, "adapters/dart/bin/codeflow-dart-adapter.dart")
}
func fixtureRepo(t *testing.T, routes string) string {
	t.Helper()
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "lib"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "lib", "routes.dart"), []byte(routes), 0644); err != nil {
		t.Fatal(err)
	}
	return repo
}
func TestResolveUsesExactConfiguredAndUniqueSelectorsWithoutGuessing(t *testing.T) {
	repo := fixtureRepo(t, "final r = GoRoute(path: '/signup', builder: (c,s) => const SizedBox());\n")
	config := "schema_version: '1'\nrepository: {id: fixture}\nfeatures:\n  signup: {entry_point: 'route:/signup'}\n"
	if err := os.WriteFile(filepath.Join(repo, "codeflow.yaml"), []byte(config), 0644); err != nil {
		t.Fatal(err)
	}
	for _, selector := range []string{"route:/signup", "signup"} {
		result := Resolve(context.Background(), repo, selector, adapterCommand(t))
		if result.State != Ready || result.EntryPoint == nil || result.EntryPoint.FlowID != "route:/signup" || result.EntryPoint.Anchor.FileHash == "" || result.EntryPoint.Anchor.SpanHash == "" {
			t.Fatalf("%s: %#v", selector, result)
		}
	}
	automatic := Resolve(context.Background(), repo, "", adapterCommand(t))
	if automatic.State != Ready || automatic.EntryPoint == nil || automatic.EntryPoint.FlowID != "route:/signup" {
		t.Fatalf("unique route should require no selector: %#v", automatic)
	}
	missing := Resolve(context.Background(), repo, "missing", adapterCommand(t))
	if missing.State != Unknown || missing.Unknown.Code != "ENTRY_POINT_NOT_FOUND" || len(missing.Candidates) != 1 {
		t.Fatalf("missing %#v", missing)
	}
}
func TestResolveDoesNotChooseAmbiguousDiscoveredAlias(t *testing.T) {
	repo := fixtureRepo(t, "final a=GoRoute(path: '/account', builder: x); final b=GoRoute(path: '/settings/account', builder: x);")
	result := Resolve(context.Background(), repo, "account", adapterCommand(t))
	if result.State != Unknown || result.Unknown.Code != "AMBIGUOUS_ENTRY_POINT" || len(result.Candidates) != 2 {
		t.Fatalf("%#v", result)
	}
	withoutSelector := Resolve(context.Background(), repo, "", adapterCommand(t))
	if withoutSelector.State != Unknown || withoutSelector.Unknown.Code != "SELECTOR_REQUIRED" || len(withoutSelector.Candidates) != 2 {
		t.Fatalf("ambiguous repository must present exact candidates: %#v", withoutSelector)
	}
}

func TestResolveAcceptsLiteralRouteWithoutWhitespaceAfterComma(t *testing.T) {
	repo := fixtureRepo(t, "final routes=[GoRoute(path: '/signup',builder:(c,s)=>const SignupPage())]; class SignupPage { void build(){ ElevatedButton(onPressed: _submit); } void _submit(){ _navigate(); } void _navigate(){ if (approved) { context.go('/welcome'); } else { dynamic fallback; fallback.go('/retry'); } } }\n")
	if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %s %v", out, err)
	}
	if out, err := exec.Command("git", "-C", repo, "add", ".").CombinedOutput(); err != nil {
		t.Fatalf("git add: %s %v", out, err)
	}
	if out, err := exec.Command("git", "-C", repo, "-c", "user.email=x@y.z", "-c", "user.name=x", "commit", "-qm", "route").CombinedOutput(); err != nil {
		t.Fatalf("git commit: %s %v", out, err)
	}
	result := Resolve(context.Background(), repo, "signup", adapterCommand(t))
	if result.State != Ready || result.EntryPoint == nil || result.EntryPoint.FlowID != "route:/signup" {
		t.Fatalf("%#v", result)
	}
}
func TestResolveReportsUnavailableAdapter(t *testing.T) {
	repo := fixtureRepo(t, "final a=GoRoute(path: '/signup', builder: x);")
	result := Resolve(context.Background(), repo, "signup", "/missing/adapter")
	if result.State != Unavailable || result.Unknown.Code != "ADAPTER_UNAVAILABLE" {
		t.Fatalf("%#v", result)
	}
}
