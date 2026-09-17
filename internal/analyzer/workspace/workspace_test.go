package workspace

import (
	"errors"
	"io/fs"
	"os"
	"testing"
	"time"
)

func writeRawManifest(repoRoot, content string) error {
	if err := os.MkdirAll(Dir(repoRoot), 0o755); err != nil {
		return err
	}
	return os.WriteFile(FilePath(repoRoot), []byte(content), 0o644)
}

func TestNewHasSaneDefaults(t *testing.T) {
	ws := New(t.TempDir())
	if ws.SchemaVersion != SchemaVersion {
		t.Errorf("SchemaVersion = %q, want %q", ws.SchemaVersion, SchemaVersion)
	}
	if ws.AdapterPins == nil {
		t.Error("AdapterPins must be initialized, not nil")
	}
	if ws.CreatedAt.IsZero() {
		t.Error("CreatedAt must be set")
	}
	if ws.BasisFingerprint != "" {
		t.Errorf("BasisFingerprint = %q, want empty placeholder", ws.BasisFingerprint)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	repoRoot := t.TempDir()
	ws := New(repoRoot)
	ws.SetPin("dart", "0.1.0")
	if err := ws.Save(); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	loaded, err := Load(repoRoot)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded.RepoRoot != repoRoot {
		t.Errorf("RepoRoot = %q, want %q", loaded.RepoRoot, repoRoot)
	}
	if loaded.SchemaVersion != SchemaVersion {
		t.Errorf("SchemaVersion = %q, want %q", loaded.SchemaVersion, SchemaVersion)
	}
	if !loaded.CreatedAt.Equal(ws.CreatedAt) {
		t.Errorf("CreatedAt = %v, want %v", loaded.CreatedAt, ws.CreatedAt)
	}
	if loaded.AdapterPins["dart"] != "0.1.0" {
		t.Errorf("pin dart = %q, want %q", loaded.AdapterPins["dart"], "0.1.0")
	}
	if loaded.BasisFingerprint != "" {
		t.Errorf("BasisFingerprint = %q, want empty", loaded.BasisFingerprint)
	}
}

func TestSaveCreatesDirectoryAndIsIdempotentOnDisk(t *testing.T) {
	repoRoot := t.TempDir()
	if Exists(repoRoot) {
		t.Fatal("Exists should be false before first save")
	}
	ws := New(repoRoot)
	if err := ws.Save(); err != nil {
		t.Fatalf("first Save() error = %v", err)
	}
	if !Exists(repoRoot) {
		t.Fatal("Exists should be true after save")
	}
	before, err := Load(repoRoot)
	if err != nil {
		t.Fatalf("Load after first save: %v", err)
	}
	time.Sleep(1100 * time.Millisecond)
	if err := ws.Save(); err != nil {
		t.Fatalf("second Save() error = %v", err)
	}
	after, err := Load(repoRoot)
	if err != nil {
		t.Fatalf("Load after second save: %v", err)
	}
	if !after.CreatedAt.Equal(before.CreatedAt) {
		t.Errorf("CreatedAt changed between saves of the same Workspace: %v -> %v", before.CreatedAt, after.CreatedAt)
	}
}

func TestLoadMissingFileErrorWrapsErrNotExist(t *testing.T) {
	repoRoot := t.TempDir()
	if _, err := Load(repoRoot); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Load(missing) error = %v, want fs.ErrNotExist wrapped", err)
	}
}

func TestLoadCorruptManifestFailsValidation(t *testing.T) {
	repoRoot := t.TempDir()
	if err := writeRawManifest(repoRoot, `{"schemaVersion":"9.9"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(repoRoot); err == nil {
		t.Error("Load(corrupt schema) should error")
	}
}

func TestValidateRejectsInvalidWorkspaces(t *testing.T) {
	tests := map[string]*Workspace{
		"bad schema": {SchemaVersion: "1.0", CreatedAt: time.Now(), AdapterPins: map[string]string{}},
		"nil pins":   {SchemaVersion: SchemaVersion, CreatedAt: time.Now()},
		"zero createdAt": {
			SchemaVersion: SchemaVersion,
			AdapterPins:   map[string]string{},
		},
		"empty pin value": {
			SchemaVersion: SchemaVersion,
			CreatedAt:     time.Now(),
			AdapterPins:   map[string]string{"dart": ""},
		},
	}
	for name, ws := range tests {
		if err := ws.Validate(); err == nil {
			t.Errorf("%s: Validate should reject", name)
		}
	}
}

func TestSetPinInitializesNilMap(t *testing.T) {
	var ws Workspace
	ws.SetPin("dart", "0.1.0")
	if ws.AdapterPins["dart"] != "0.1.0" {
		t.Errorf("SetPin on nil map failed: %v", ws.AdapterPins)
	}
}

func TestFilePathLayout(t *testing.T) {
	repoRoot := t.TempDir()
	want := repoRoot + "/" + DirName + "/" + FileName
	if got := FilePath(repoRoot); got != want {
		t.Errorf("FilePath = %q, want %q", got, want)
	}
}
