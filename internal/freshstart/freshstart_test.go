package freshstart

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"codeflow/internal/workspace"
)

const schemaVersionProbeValue = workspace.SchemaVersion

func setupRepo(t *testing.T) string {
	t.Helper()
	repoRoot := t.TempDir()
	if err := os.MkdirAll(workspace.Dir(repoRoot), 0o755); err != nil {
		t.Fatal(err)
	}
	return repoRoot
}

func writeTree(t *testing.T, repoRoot string, paths ...string) {
	t.Helper()
	for _, p := range paths {
		full := filepath.Join(repoRoot, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestScanReturnsNilWithoutCodeflowDir(t *testing.T) {
	repoRoot := t.TempDir()
	got, err := ScanV1Remnants(repoRoot)
	if err != nil || got != nil {
		t.Errorf("ScanV1Remnants = %v, %v; want nil, nil", got, err)
	}
}

func TestScanDetectsGroundedV1Layout(t *testing.T) {
	repoRoot := setupRepo(t)
	writeTree(t, repoRoot,
		filepath.Join(".codeflow", "runtime.json"),
		filepath.Join(".codeflow", "codeflow.lock"),
		filepath.Join(".codeflow", "state.db"),
		filepath.Join(".codeflow", "cache", "baselines", "abc", "manifest.json"),
		filepath.Join(".codeflow", "knowledge", "confirmed.json"),
		filepath.Join(".codeflow", "flows", "old.flowir.json"),
	)

	got, err := ScanV1Remnants(repoRoot)
	if err != nil {
		t.Fatalf("ScanV1Remnants error = %v", err)
	}
	wantBases := []string{"cache", "codeflow.lock", "knowledge", "old.flowir.json", "runtime.json", "state.db"}
	var gotBases []string
	for _, path := range got {
		gotBases = append(gotBases, filepath.Base(path))
	}
	slices.Sort(gotBases)
	if !slices.Equal(gotBases, wantBases) {
		t.Errorf("remnant bases = %v, want %v", gotBases, wantBases)
	}
}

func TestScanFindsNestedFlowirNames(t *testing.T) {
	repoRoot := setupRepo(t)
	writeTree(t, repoRoot,
		filepath.Join(".codeflow", "deeply", "nested", "FLOWIR-dump.json"),
	)

	got, err := ScanV1Remnants(repoRoot)
	if err != nil {
		t.Fatalf("ScanV1Remnants error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("remnants = %v, want exactly the flowir file", got)
	}
	if filepath.Base(got[0]) != "FLOWIR-dump.json" {
		t.Errorf("found %q, want FLOWIR-dump.json (case-insensitive match)", got[0])
	}
}

func TestScanLeavesV2WorkspaceDataAlone(t *testing.T) {
	repoRoot := setupRepo(t)
	writeRawWorkspace(t, repoRoot)
	writeTree(t, repoRoot,
		filepath.Join(".codeflow", "facts", "ast", "abc.json"),
		filepath.Join(".codeflow", "ir", "flow-1", "latest.json"),
		filepath.Join(".codeflow", "index.sqlite"),
		filepath.Join(".codeflow", "publish.pointer"),
	)

	got, err := ScanV1Remnants(repoRoot)
	if err != nil {
		t.Fatalf("ScanV1Remnants error = %v", err)
	}
	if len(got) != 0 {
		t.Errorf("remnants = %v, want none in a v2 workspace", got)
	}
}

func TestScanFlagsIrWhenNoV2Workspace(t *testing.T) {
	repoRoot := setupRepo(t)
	writeTree(t, repoRoot,
		filepath.Join(".codeflow", "ir", "legacy-flow", "snapshot.json"),
	)

	got, err := ScanV1Remnants(repoRoot)
	if err != nil {
		t.Fatalf("ScanV1Remnants error = %v", err)
	}
	if len(got) != 1 || filepath.Base(got[0]) != "ir" {
		t.Errorf("remnants = %v, want [ir] without workspace.json", got)
	}
}

func TestScanStillCatchesFlowirNextToV2Workspace(t *testing.T) {
	repoRoot := setupRepo(t)
	writeRawWorkspace(t, repoRoot)
	writeTree(t, repoRoot, filepath.Join(".codeflow", "leftover.flowir.json"))

	got, err := ScanV1Remnants(repoRoot)
	if err != nil {
		t.Fatalf("ScanV1Remnants error = %v", err)
	}
	if len(got) != 1 {
		t.Errorf("remnants = %v, want the flowir file even beside a v2 workspace", got)
	}
}

func TestPurgeRemovesListedPaths(t *testing.T) {
	repoRoot := setupRepo(t)
	v1Paths := []string{
		filepath.Join(".codeflow", "runtime.json"),
		filepath.Join(".codeflow", "cache", "baselines"),
		filepath.Join(".codeflow", "keepme.txt"),
	}
	writeTree(t, repoRoot, v1Paths...)

	list, err := ScanV1Remnants(repoRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := Purge(repoRoot, list); err != nil {
		t.Fatalf("Purge error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(repoRoot, ".codeflow", "runtime.json")); !errors.Is(err, fs.ErrNotExist) {
		t.Error("runtime.json should be gone after purge")
	}
	if _, err := os.Stat(filepath.Join(repoRoot, ".codeflow", "keepme.txt")); err != nil {
		t.Error("keepme.txt must survive purge")
	}
}

func TestPurgeRejectsEscapePaths(t *testing.T) {
	repoRoot := setupRepo(t)
	hostage := filepath.Join(repoRoot, "precious.txt")
	writeTree(t, repoRoot, "precious.txt")
	cfKeep := filepath.Join(repoRoot, ".codeflow", "keep.txt")
	writeTree(t, repoRoot, filepath.Join(".codeflow", "keep.txt"))

	escapes := []string{
		filepath.Join(repoRoot, "..", "escaped-target"),
		filepath.Join(hostage),
		filepath.Join(repoRoot, ".codeflow"),
	}
	for _, escape := range escapes {
		if err := Purge(repoRoot, []string{escape}); err == nil {
			t.Errorf("Purge(%q) should refuse to run", escape)
		}
	}
	if _, err := os.Stat(hostage); err != nil {
		t.Error("file outside .codeflow was deleted by an escaping path")
	}
	if _, err := os.Stat(cfKeep); err != nil {
		t.Error(".codeflow itself or its content was deleted despite refusal")
	}
}

func writeRawWorkspace(t *testing.T, repoRoot string) {
	t.Helper()
	content := `{"schemaVersion": "` + schemaVersionProbeValue + `"}`
	path := filepath.Join(repoRoot, ".codeflow", "workspace.json")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
