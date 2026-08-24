package installation

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestSkillMatches(t *testing.T) {
	skillPath := filepath.Join(t.TempDir(), "codeflow")
	if err := os.Mkdir(skillPath, 0o755); err != nil {
		t.Fatal(err)
	}
	contents := []byte("skill contents\n")
	if err := os.WriteFile(filepath.Join(skillPath, "SKILL.md"), contents, 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(contents)
	want := hex.EncodeToString(sum[:])
	matched, err := skillMatches(skillPath, want)
	if err != nil || !matched {
		t.Fatalf("skillMatches = %t, %v; want true, nil", matched, err)
	}
	matched, err = skillMatches(skillPath, "changed")
	if err != nil || matched {
		t.Fatalf("skillMatches changed = %t, %v; want false, nil", matched, err)
	}
}

func TestIsManagedSource(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), ".codeflow", "install-state.json")
	if !isManagedSource(filepath.Join(filepath.Dir(statePath), "src"), statePath) {
		t.Fatal("managed source was rejected")
	}
	if isManagedSource(filepath.Join(filepath.Dir(statePath), "other"), statePath) {
		t.Fatal("non-managed source was accepted")
	}
}
