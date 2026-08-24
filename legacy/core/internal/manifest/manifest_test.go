package manifest

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"codeflow/core/internal/flowir"
)

func TestCaptureClassifiesGitStatesAndEvidence(t *testing.T) {
	repo := initRepository(t)
	write(t, repo, "clean.txt", "clean\n")
	write(t, repo, "modified.txt", "before\n")
	write(t, repo, "deleted.txt", "gone\n")
	write(t, repo, "old.txt", "rename\n")
	write(t, repo, "lib/model.g.dart", "generated\n")
	write(t, repo, ".env", "must-not-appear")
	write(t, repo, "build/output", "must-not-appear")
	git(t, repo, "add", ".")
	git(t, repo, "-c", "user.email=test@example.test", "-c", "user.name=Test", "commit", "-qm", "initial")
	write(t, repo, "modified.txt", "after\x00raw\n")
	if err := os.Remove(filepath.Join(repo, "deleted.txt")); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "mv", "old.txt", "renamed.txt")
	write(t, repo, "added.txt", "staged")
	git(t, repo, "add", "added.txt")
	write(t, repo, "untracked.txt", "loose")
	if err := os.Symlink("clean.txt", filepath.Join(repo, "link")); err != nil {
		t.Fatal(err)
	}

	basis, err := Capture(repo)
	if err != nil {
		t.Fatal(err)
	}
	if basis.HeadRevision == "unavailable" || !basis.Dirty {
		t.Fatalf("git basis=%#v", basis)
	}
	entries := byPath(basis.Manifest)
	assertState(t, entries, "clean.txt", "clean")
	assertState(t, entries, "modified.txt", "modified")
	assertState(t, entries, "deleted.txt", "deleted")
	assertState(t, entries, "renamed.txt", "renamed")
	assertState(t, entries, "added.txt", "added")
	assertState(t, entries, "untracked.txt", "untracked")
	link := entries["link"]
	if link.Type != "symlink" || link.FileHash != flowir.SHA256Bytes([]byte("clean.txt")) {
		t.Fatalf("symlink was followed or incorrectly hashed: %#v", link)
	}
	if _, found := entries[".env"]; found {
		t.Fatal("secret file must be excluded")
	}
	if _, found := entries["build/output"]; found {
		t.Fatal("build output must be excluded")
	}
	if !entries["lib/model.g.dart"].Generated {
		t.Fatal("generated classification lost")
	}
	if entries["modified.txt"].FileHash != flowir.SHA256Bytes([]byte("after\x00raw\n")) {
		t.Fatal("file hash was not raw bytes")
	}
}

func TestCaptureExcludesLocalToolStateWithoutDroppingProductSource(t *testing.T) {
	repo := initRepository(t)
	write(t, repo, "lib/feature.dart", "void feature() {}\n")
	for _, path := range []string{
		".venv/lib/python/site.py",
		"packages/app/.dart_tool/package_config.json",
		"packages/app/build/output.bin",
		".codegraph/index.json",
		".flow-trace/events.jsonl",
		".codex/session.json",
		".claude/settings.json",
		".tasks/local.md",
		".brv/cache.json",
		".vscode/settings.json",
		".DS_Store",
	} {
		write(t, repo, path, "local-only")
	}
	git(t, repo, "add", "-f", ".")
	git(t, repo, "-c", "user.email=test@example.test", "-c", "user.name=Test", "commit", "-qm", "initial")
	if err := os.Remove(filepath.Join(repo, ".venv/lib/python/site.py")); err != nil {
		t.Fatal(err)
	}

	basis, err := Capture(repo)
	if err != nil {
		t.Fatal(err)
	}
	entries := byPath(basis.Manifest)
	if _, ok := entries["lib/feature.dart"]; !ok {
		t.Fatal("product source was excluded")
	}
	for _, path := range []string{
		".venv/lib/python/site.py",
		"packages/app/.dart_tool/package_config.json",
		"packages/app/build/output.bin",
		".codegraph/index.json",
		".flow-trace/events.jsonl",
		".codex/session.json",
		".claude/settings.json",
		".tasks/local.md",
		".brv/cache.json",
		".vscode/settings.json",
		".DS_Store",
	} {
		if _, ok := entries[path]; ok {
			t.Fatalf("local tool path %q was captured", path)
		}
	}
	if basis.Dirty {
		t.Fatal("changes confined to excluded local tool state must not dirty the product basis")
	}
}

func TestCaptureRetriesOnceAndRejectsContinualMutation(t *testing.T) {
	repo := initRepository(t)
	write(t, repo, "file.txt", "one")
	git(t, repo, "add", ".")
	git(t, repo, "-c", "user.email=test@example.test", "-c", "user.name=Test", "commit", "-qm", "initial")
	changed := false
	basis, err := CaptureWithOptions(repo, Options{BeforeVerify: func() {
		if !changed {
			changed = true
			write(t, repo, "file.txt", "two")
		}
	}})
	if err != nil {
		t.Fatal(err)
	}
	if basis.Manifest[0].FileHash != flowir.SHA256Bytes([]byte("two")) {
		t.Fatalf("retry did not publish a complete second observation: %#v", basis.Manifest)
	}
	mutations := 0
	_, err = CaptureWithOptions(repo, Options{BeforeVerify: func() {
		mutations++
		write(t, repo, "file.txt", string(rune('a'+mutations)))
	}})
	if !errors.Is(err, ErrChanging) {
		t.Fatalf("err=%v, want changing", err)
	}
}

func TestExcludedKeepsRemovedToolDirectoriesOutsideWatcherBoundary(t *testing.T) {
	for _, path := range []string{".codeflow", "build", "packages/app/.dart_tool"} {
		if !Excluded(path, false) {
			t.Fatalf("removed tool directory %q was treated as product input", path)
		}
	}
	if Excluded("lib", false) || Excluded("lib/feature.dart", false) {
		t.Fatal("product source was excluded from watcher boundary")
	}
}

func byPath(entries []flowir.ManifestEntry) map[string]flowir.ManifestEntry {
	result := map[string]flowir.ManifestEntry{}
	for _, entry := range entries {
		result[entry.Path] = entry
	}
	return result
}
func assertState(t *testing.T, entries map[string]flowir.ManifestEntry, path, state string) {
	t.Helper()
	if entry, ok := entries[path]; !ok || entry.GitState != state {
		t.Fatalf("%s = %#v, want %s", path, entry, state)
	}
}
func initRepository(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	git(t, repo, "init", "-q")
	return repo
}
func git(t *testing.T, repo string, args ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", repo}, args...)...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
}
func write(t *testing.T, root, name, value string) {
	t.Helper()
	full := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(value), 0o644); err != nil {
		t.Fatal(err)
	}
}
