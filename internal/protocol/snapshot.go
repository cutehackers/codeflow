package protocol

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Snapshot is the immutable analysis input passed to an adapter. A caller
// may provide contentOverlay for files that differ from the worktree. When
// an overlay is present, adapters must read it as authoritative and must not
// fall back to the live file for those paths.
type Snapshot struct {
	ComputedBasisID string            `json:"computedBasisId"`
	WorkspaceEpoch  int64             `json:"workspaceEpoch"`
	ContentOverlay  map[string]string `json:"contentOverlay,omitempty"`
}

// NewSnapshot constructs a snapshot from an optional overlay. The basis is
// deterministic over sorted path/content pairs unless an explicit basis is
// supplied by the workspace owner.
func NewSnapshot(workspaceEpoch int64, overlay map[string]string, basis string) (Snapshot, error) {
	if workspaceEpoch < 0 {
		return Snapshot{}, fmt.Errorf("workspace epoch must be non-negative")
	}
	clean := make(map[string]string, len(overlay))
	for rawPath, content := range overlay {
		rel := filepath.ToSlash(filepath.Clean(rawPath))
		if rel == "." || filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, "../") || strings.Contains(rel, "/../") {
			return Snapshot{}, fmt.Errorf("overlay path must be repository-relative: %q", rawPath)
		}
		clean[rel] = content
	}
	if basis == "" {
		basis = basisForOverlay(clean)
	}
	return Snapshot{ComputedBasisID: basis, WorkspaceEpoch: workspaceEpoch, ContentOverlay: clean}, nil
}

// CaptureSnapshot captures a deterministic basis from the current worktree
// without copying source contents into the request. It is bounded to the
// same document count used by adapter read sets.
func CaptureSnapshot(repoRoot string, workspaceEpoch int64) (Snapshot, error) {
	if workspaceEpoch < 0 {
		return Snapshot{}, fmt.Errorf("workspace epoch must be non-negative")
	}
	root, err := filepath.Abs(repoRoot)
	if err != nil {
		return Snapshot{}, err
	}
	entries := make([]string, 0, 4096)
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", ".codeflow", "node_modules", "build", "dist", ".dart_tool", "vendor":
				return filepath.SkipDir
			}
			return nil
		}
		if len(entries) >= 4096 || !entry.Type().IsRegular() {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr == nil {
			entries = append(entries, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		return Snapshot{}, err
	}
	sort.Strings(entries)
	h := sha256.New()
	for _, rel := range entries {
		data, readErr := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if readErr != nil {
			continue
		}
		fileHash := sha256.Sum256(data)
		fmt.Fprintf(h, "%s:%s\n", rel, hex.EncodeToString(fileHash[:]))
	}
	return Snapshot{ComputedBasisID: hex.EncodeToString(h.Sum(nil)), WorkspaceEpoch: workspaceEpoch}, nil
}

func basisForOverlay(overlay map[string]string) string {
	keys := make([]string, 0, len(overlay))
	for key := range overlay {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	h := sha256.New()
	for _, key := range keys {
		digest := sha256.Sum256([]byte(overlay[key]))
		fmt.Fprintf(h, "%s:%s\n", key, hex.EncodeToString(digest[:]))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Params returns a request-ready copy. The overlay map is copied so callers
// cannot mutate the snapshot while an adapter is reading it.
func (s Snapshot) Params() map[string]any {
	params := map[string]any{
		"computedBasisId": s.ComputedBasisID,
		"workspaceEpoch":  s.WorkspaceEpoch,
		"snapshot": map[string]any{
			"computedBasisId": s.ComputedBasisID,
			"workspaceEpoch":  s.WorkspaceEpoch,
		},
	}
	if len(s.ContentOverlay) > 0 {
		overlay := make(map[string]string, len(s.ContentOverlay))
		for key, value := range s.ContentOverlay {
			overlay[key] = value
		}
		params["contentOverlay"] = overlay
		params["snapshot"].(map[string]any)["contentOverlay"] = overlay
	}
	return params
}
