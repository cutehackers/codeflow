package installation

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestInstallCopiesPairedBundleAndActivatesCodexPlugin(t *testing.T) {
	source := t.TempDir()
	write(t, filepath.Join(source, "bin", "codeflow"), "core", 0755)
	write(t, filepath.Join(source, "libexec", "codeflow-dart-adapter"), "adapter", 0755)
	write(t, filepath.Join(source, "libexec", "compatibility.json"), "{}", 0644)
	write(t, filepath.Join(source, ".agents", "plugins", "marketplace.json"), `{"name":"codeflow-local"}`, 0644)
	write(t, filepath.Join(source, "plugins", "codeflow", ".codex-plugin", "plugin.json"), `{"name":"codeflow"}`, 0644)
	home := t.TempDir()
	var calls [][]string
	result, err := Install(context.Background(), Options{
		SourceRoot: source,
		HomeDir:    home,
		LookPath:   func(string) (string, error) { return "/fake/codex", nil },
		Run: func(_ context.Context, command string, args ...string) ([]byte, error) {
			calls = append(calls, append([]string{command}, args...))
			return nil, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Executable != filepath.Join(home, ".codeflow", "bin", "codeflow") {
		t.Fatalf("result=%#v", result)
	}
	for _, relative := range []string{
		"bin/codeflow",
		"libexec/codeflow-dart-adapter",
		"libexec/compatibility.json",
		".agents/plugins/marketplace.json",
		"plugins/codeflow/.codex-plugin/plugin.json",
	} {
		if _, err := os.Stat(filepath.Join(result.Root, relative)); err != nil {
			t.Fatalf("missing %s: %v", relative, err)
		}
	}
	want := [][]string{
		{"/fake/codex", "plugin", "marketplace", "add", result.Root},
		{"/fake/codex", "plugin", "add", "codeflow@codeflow-local"},
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls=%#v want=%#v", calls, want)
	}
}

func write(t *testing.T, path, contents string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), mode); err != nil {
		t.Fatal(err)
	}
}
