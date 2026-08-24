// Package workspace models and persists the per-repo CodeFlow manifest
// stored at <repoRoot>/.codeflow/workspace.json (design-v2 §6.1).
package workspace

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// SchemaVersion is the workspace manifest schema this binary writes.
const SchemaVersion = "2.0"

// DirName is the per-repo storage directory name under the repo root.
const DirName = ".codeflow"

// FileName is the workspace manifest file name inside DirName.
const FileName = "workspace.json"

// Workspace is the repo-level CodeFlow manifest: schema version, adapter
// pins, creation time, and the publish basis fingerprint (empty until the
// publish stage exists).
type Workspace struct {
	RepoRoot string `json:"-"`

	SchemaVersion    string            `json:"schemaVersion"`
	CreatedAt        time.Time         `json:"createdAt"`
	AdapterPins      map[string]string `json:"adapterPins"`
	BasisFingerprint string            `json:"basisFingerprint"`
}

// Dir returns <repoRoot>/.codeflow.
func Dir(repoRoot string) string {
	return filepath.Join(repoRoot, DirName)
}

// FilePath returns the manifest path <repoRoot>/.codeflow/workspace.json.
func FilePath(repoRoot string) string {
	return filepath.Join(Dir(repoRoot), FileName)
}

// Exists reports whether a workspace manifest is present at repoRoot.
func Exists(repoRoot string) bool {
	info, err := os.Stat(FilePath(repoRoot))
	return err == nil && info.Mode().IsRegular()
}

// New builds a default workspace for repoRoot with the current schema
// version, no pins yet, and CreatedAt set to now (UTC).
func New(repoRoot string) *Workspace {
	return &Workspace{
		RepoRoot:      repoRoot,
		SchemaVersion: SchemaVersion,
		CreatedAt:     time.Now().UTC().Truncate(time.Second),
		AdapterPins:   map[string]string{},
	}
}

// Load reads and validates the workspace manifest at repoRoot.
func Load(repoRoot string) (*Workspace, error) {
	path := FilePath(repoRoot)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("load workspace: %w", err)
	}
	var ws Workspace
	if err := json.Unmarshal(data, &ws); err != nil {
		return nil, fmt.Errorf("load workspace %s: %w", path, err)
	}
	ws.RepoRoot = repoRoot
	if err := ws.Validate(); err != nil {
		return nil, fmt.Errorf("load workspace %s: %w", path, err)
	}
	return &ws, nil
}

// Save atomically writes the manifest to <repoRoot>/.codeflow/workspace.json,
// creating the directory if needed. The write goes through a temp file plus
// rename so readers never observe a partial manifest.
func (w *Workspace) Save() error {
	if w.RepoRoot == "" {
		return fmt.Errorf("save workspace: RepoRoot is empty")
	}
	if err := w.Validate(); err != nil {
		return fmt.Errorf("save workspace: %w", err)
	}
	dir := Dir(w.RepoRoot)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("save workspace: create %s: %w", dir, err)
	}
	data, err := json.MarshalIndent(w, "", "  ")
	if err != nil {
		return fmt.Errorf("save workspace: %w", err)
	}
	data = append(data, '\n')

	tmp, err := os.CreateTemp(dir, "."+FileName+".tmp-")
	if err != nil {
		return fmt.Errorf("save workspace: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("save workspace: write %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("save workspace: close %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, FilePath(w.RepoRoot)); err != nil {
		return fmt.Errorf("save workspace: rename into place: %w", err)
	}
	return nil
}

// SetPin records an adapter pin version.
func (w *Workspace) SetPin(adapter, version string) {
	if w.AdapterPins == nil {
		w.AdapterPins = map[string]string{}
	}
	w.AdapterPins[adapter] = version
}

// Validate checks basic invariants: known schema version, initialized pin
// table with non-empty entries, non-zero CreatedAt.
func (w *Workspace) Validate() error {
	if w.SchemaVersion != SchemaVersion {
		return fmt.Errorf("workspace schema %q unsupported (want %q)", w.SchemaVersion, SchemaVersion)
	}
	if w.AdapterPins == nil {
		return fmt.Errorf("workspace adapterPins must not be null")
	}
	for adapter, version := range w.AdapterPins {
		if adapter == "" {
			return fmt.Errorf("workspace has an empty adapter name in adapterPins")
		}
		if version == "" {
			return fmt.Errorf("adapter %s has an empty pinned version", adapter)
		}
	}
	if w.CreatedAt.IsZero() {
		return fmt.Errorf("workspace createdAt must be set")
	}
	return nil
}
