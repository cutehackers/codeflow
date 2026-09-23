package e2e_test

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFixtureCopyExcludesRuntimeAnalysisState(t *testing.T) {
	source, target := t.TempDir(), filepath.Join(t.TempDir(), "copy")
	stateDir := filepath.Join(source, ".codeflow", "workspace")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "state.json"), []byte(`{"incomplete":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "entry.ts"), []byte("export function entry() {}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := copyDir(source, target); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(target, ".codeflow")); !os.IsNotExist(err) {
		t.Fatalf("runtime state copied: stat error %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, "entry.ts")); err != nil {
		t.Fatal(err)
	}
}
