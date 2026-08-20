package baseline

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMaterializeUsesImmutableGitObjectsWithoutWorktreeMutation(t *testing.T) {
	repo := t.TempDir()
	must(t, os.WriteFile(filepath.Join(repo, "app.dart"), []byte("one\n"), 0644))
	run(t, "git", "init", "-q", repo)
	run(t, "git", "-C", repo, "add", ".")
	run(t, "git", "-C", repo, "-c", "user.email=x@y.z", "-c", "user.name=x", "commit", "-qm", "one")
	base := strings.TrimSpace(string(output(t, "git", "-C", repo, "rev-parse", "HEAD")))
	must(t, os.WriteFile(filepath.Join(repo, "app.dart"), []byte("dirty current\n"), 0644))
	before := string(output(t, "git", "-C", repo, "diff", "--", "app.dart"))
	m, err := Materialize(repo, base)
	if err != nil {
		t.Fatal(err)
	}
	if m.Revision != base || m.Basis.HeadRevision != base || m.Basis.Dirty {
		t.Fatalf("bad mirror %#v", m)
	}
	contents, err := os.ReadFile(filepath.Join(m.Root, "app.dart"))
	if err != nil || string(contents) != "one\n" {
		t.Fatalf("mirror=%q err=%v", contents, err)
	}
	if after := string(output(t, "git", "-C", repo, "diff", "--", "app.dart")); after != before {
		t.Fatalf("product worktree changed: before=%q after=%q", before, after)
	}
	again, err := Materialize(repo, base)
	if err != nil || again.Basis.WorktreeFingerprint != m.Basis.WorktreeFingerprint {
		t.Fatalf("nondeterministic mirror %#v %v", again, err)
	}
}

func TestMaterializeKeepsOnlyAnalysisInputsAndBoundsOldMirrors(t *testing.T) {
	repo := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(repo, "lib"), 0755))
	must(t, os.MkdirAll(filepath.Join(repo, "docs"), 0755))
	must(t, os.WriteFile(filepath.Join(repo, "lib/app.dart"), []byte("void main() {}\n"), 0644))
	must(t, os.WriteFile(filepath.Join(repo, "pubspec.yaml"), []byte("name: app\n"), 0644))
	must(t, os.WriteFile(filepath.Join(repo, "analysis_options.yaml"), []byte("analyzer:\n"), 0644))
	must(t, os.WriteFile(filepath.Join(repo, "codeflow.external-contracts.json"), []byte("{}\n"), 0644))
	must(t, os.WriteFile(filepath.Join(repo, "docs/large.bin"), []byte(strings.Repeat("x", 1024*1024)), 0644))
	run(t, "git", "init", "-q", repo)
	run(t, "git", "-C", repo, "add", ".")
	run(t, "git", "-C", repo, "-c", "user.email=x@y.z", "-c", "user.name=x", "commit", "-qm", "one")
	base := strings.TrimSpace(string(output(t, "git", "-C", repo, "rev-parse", "HEAD")))
	m, err := Materialize(repo, base)
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"lib/app.dart", "pubspec.yaml", "analysis_options.yaml", "codeflow.external-contracts.json"} {
		if _, err := os.Stat(filepath.Join(m.Root, required)); err != nil {
			t.Fatalf("required analysis input %s: %v", required, err)
		}
	}
	if _, err := os.Stat(filepath.Join(m.Root, "docs/large.bin")); !os.IsNotExist(err) {
		t.Fatalf("unrelated artifact copied into analysis cache: %v", err)
	}
	report, err := InspectCache(repo)
	if err != nil || len(report.Baselines) != 1 || report.TotalBytes >= 1024*1024 {
		t.Fatalf("unexpected report %#v err=%v", report, err)
	}

	root := filepath.Join(repo, ".codeflow", "cache", "baselines")
	for i := 0; i < 4; i++ {
		name := fmt.Sprintf("%040x", i+100)
		dir := filepath.Join(root, name)
		must(t, os.MkdirAll(dir, 0755))
		old := time.Now().Add(-2*time.Hour - time.Duration(i)*time.Minute)
		must(t, os.Chtimes(dir, old, old))
	}
	if err := prune(repo, base, retainedMirrors); err != nil {
		t.Fatal(err)
	}
	report, err = InspectCache(repo)
	if err != nil || len(report.Baselines) != retainedMirrors {
		t.Fatalf("retention failed: %#v err=%v", report, err)
	}
}

func TestCleanCacheRemovesOnlyReconstructableBaselines(t *testing.T) {
	repo := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(repo, ".codeflow/cache/baselines/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"), 0755))
	must(t, os.WriteFile(filepath.Join(repo, ".codeflow/state.db"), []byte("state"), 0644))
	if err := CleanCache(repo); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(repo, ".codeflow/cache/baselines")); !os.IsNotExist(err) {
		t.Fatalf("baseline cache remains: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(repo, ".codeflow/state.db")); err != nil || string(got) != "state" {
		t.Fatalf("state was touched: %q %v", got, err)
	}
}

func TestRecentBaselineGracePeriodStillHasAHardLimit(t *testing.T) {
	repo := t.TempDir()
	root := filepath.Join(repo, ".codeflow", "cache", "baselines")
	for i := 0; i < hardRetainedMirrors+4; i++ {
		name := fmt.Sprintf("%040x", i+1)
		dir := filepath.Join(root, name)
		must(t, os.MkdirAll(dir, 0755))
		recent := time.Now().Add(-time.Duration(i) * time.Minute)
		must(t, os.Chtimes(dir, recent, recent))
	}
	if err := prune(repo, "", retainedMirrors); err != nil {
		t.Fatal(err)
	}
	report, err := InspectCache(repo)
	if err != nil || len(report.Baselines) != hardRetainedMirrors || report.HardLimit != hardRetainedMirrors {
		t.Fatalf("recent grace period left cache unbounded: %#v err=%v", report, err)
	}
}
func TestResolveRejectsUnavailableRevision(t *testing.T) {
	repo := t.TempDir()
	run(t, "git", "init", "-q", repo)
	if _, err := Resolve(repo, "does-not-exist"); err == nil || !strings.Contains(err.Error(), "BASELINE_NOT_FOUND") {
		t.Fatalf("%v", err)
	}
}
func run(t *testing.T, name string, args ...string) {
	t.Helper()
	if out, err := exec.Command(name, args...).CombinedOutput(); err != nil {
		t.Fatalf("%s %v: %s", name, args, out)
	}
}
func output(t *testing.T, name string, args ...string) []byte {
	t.Helper()
	out, err := exec.Command(name, args...).Output()
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
