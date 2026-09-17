package installstate

import (
	"os"
	"testing"
)

func TestSaveAndLoad(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	want := State{
		Version:     1,
		Binary:      "/tmp/codeflow",
		SourceRoot:  "/tmp/source",
		OwnedSource: true,
		AdapterSpec: "dartrun:/tmp/source/adapters/dart",
		SkillPath:   "/tmp/skill",
		SkillSHA256: "abc",
		MCPName:     "codeflow",
	}
	if err := Save(want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got != want {
		t.Fatalf("Load = %+v, want %+v", got, want)
	}
	path, err := Path()
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("state permissions = %o, want 600", info.Mode().Perm())
	}
}

func TestSaveRejectsIncompleteState(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := Save(State{Version: 1, Binary: "/tmp/codeflow"}); err == nil {
		t.Fatal("Save succeeded with missing MCP name")
	}
}
