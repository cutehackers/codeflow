package workspace

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// WorkspaceDelta represents the structural difference between two snapshots (Raw §10.4).
type WorkspaceDelta struct {
	FromSnapshotID string   `json:"fromSnapshotId"`
	ToSnapshotID   string   `json:"toSnapshotId"`
	AddedPaths     []string `json:"addedPaths"`
	ModifiedPaths  []string `json:"modifiedPaths"`
	DeletedPaths   []string `json:"deletedPaths"`
	ChangedPaths   []string `json:"changedPaths"`
	// These dimensions are deliberately separate from ChangedPaths. A source
	// path edit can leave a dependency index, package membership, resolver, or
	// toolchain boundary changed even when no one path names that observation.
	// Publication gates must intersect each measured dimension explicitly.
	RenamedPaths          []string `json:"renamedPaths,omitempty"`
	MembershipChanged     bool     `json:"membershipChanged,omitempty"`
	ResolutionChanged     bool     `json:"resolutionChanged,omitempty"`
	IndexChanged          bool     `json:"indexChanged,omitempty"`
	CapabilityChanged     bool     `json:"capabilityChanged,omitempty"`
	ConfigurationChanged  bool     `json:"configurationChanged,omitempty"`
	PublicContractChanged bool     `json:"publicContractChanged,omitempty"`
}

// ComputeDelta computes the WorkspaceDelta between two snapshots in the SnapshotEngine.
func (e *SnapshotEngine) ComputeDelta(fromSnapshotID, toSnapshotID string) (*WorkspaceDelta, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	fromSnap, okFrom := e.snapshots[fromSnapshotID]
	if !okFrom {
		return nil, fmt.Errorf("snapshot %s not found", fromSnapshotID)
	}
	toSnap, okTo := e.snapshots[toSnapshotID]
	if !okTo {
		return nil, fmt.Errorf("snapshot %s not found", toSnapshotID)
	}

	delta := &WorkspaceDelta{
		FromSnapshotID: fromSnapshotID,
		ToSnapshotID:   toSnapshotID,
		AddedPaths:     []string{},
		ModifiedPaths:  []string{},
		DeletedPaths:   []string{},
		ChangedPaths:   []string{},
	}

	if fromSnapshotID == toSnapshotID {
		return delta, nil
	}

	changedMap := make(map[string]bool)

	// Check entries in toSnap
	for path, toEntry := range toSnap.Entries {
		fromEntry, exists := fromSnap.Entries[path]
		if !exists {
			delta.AddedPaths = append(delta.AddedPaths, path)
			changedMap[path] = true
		} else if fromEntry.ContentID != toEntry.ContentID {
			delta.ModifiedPaths = append(delta.ModifiedPaths, path)
			changedMap[path] = true
		}
	}

	// Check deleted entries
	for path := range fromSnap.Entries {
		if _, exists := toSnap.Entries[path]; !exists {
			delta.DeletedPaths = append(delta.DeletedPaths, path)
			changedMap[path] = true
		}
	}

	for p := range changedMap {
		delta.ChangedPaths = append(delta.ChangedPaths, p)
	}

	// A rename is measured from immutable content identities, never inferred
	// from a filename heuristic. Keep both sides in RenamedPaths so closure
	// intersection can reject either an old negative lookup or a new target.
	addedByContent := make(map[string][]string)
	for _, path := range delta.AddedPaths {
		addedByContent[toSnap.Entries[path].ContentID] = append(addedByContent[toSnap.Entries[path].ContentID], path)
	}
	for _, oldPath := range delta.DeletedPaths {
		contentID := fromSnap.Entries[oldPath].ContentID
		candidates := addedByContent[contentID]
		if len(candidates) != 1 {
			continue
		}
		newPath := candidates[0]
		delta.RenamedPaths = append(delta.RenamedPaths, oldPath, newPath)
	}
	sort.Strings(delta.RenamedPaths)
	delta.MembershipChanged = len(delta.AddedPaths) > 0 || len(delta.DeletedPaths) > 0 || len(delta.RenamedPaths) > 0
	delta.ConfigurationChanged = fromSnap.ConfigurationFingerprint != toSnap.ConfigurationFingerprint
	// A root-tree change invalidates the graph index. This is an explicit
	// invalidation signal, not a claim that a particular relation changed.
	for _, path := range append(append([]string{}, delta.AddedPaths...), append(delta.ModifiedPaths, delta.DeletedPaths...)...) {
		if isSourcePath(path) {
			delta.IndexChanged = true
			break
		}
	}
	if len(delta.RenamedPaths) > 0 {
		for _, path := range delta.RenamedPaths {
			if isSourcePath(path) {
				delta.IndexChanged = true
				break
			}
		}
	}
	for _, path := range append(append(append([]string{}, delta.AddedPaths...), delta.ModifiedPaths...), delta.DeletedPaths...) {
		if isResolverInputPath(path) {
			delta.ResolutionChanged = true
			delta.ConfigurationChanged = true
		}
		if isSourcePath(path) {
			// Source additions/deletions/renames and source edits may alter an
			// exported declaration. Keep the public-contract dimension separate
			// so closure validation cannot silently ignore that possibility.
			delta.PublicContractChanged = true
		}
	}
	if len(delta.RenamedPaths) > 0 {
		for _, path := range delta.RenamedPaths {
			if isSourcePath(path) {
				delta.PublicContractChanged = true
			}
		}
	}

	sort.Strings(delta.AddedPaths)
	sort.Strings(delta.ModifiedPaths)
	sort.Strings(delta.DeletedPaths)
	sort.Strings(delta.ChangedPaths)

	return delta, nil
}

func isSourcePath(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".c", ".cc", ".cpp", ".cs", ".dart", ".go", ".h", ".hpp", ".java", ".js", ".jsx", ".kt", ".m", ".mm", ".py", ".rb", ".rs", ".swift", ".ts", ".tsx", ".vue":
		return true
	default:
		return false
	}
}

func isResolverInputPath(path string) bool {
	base := strings.ToLower(filepath.Base(path))
	if strings.HasPrefix(base, "package-lock.") || strings.HasPrefix(base, "yarn.lock") || strings.HasPrefix(base, "pnpm-lock.") || strings.HasPrefix(base, "pubspec.") || strings.HasPrefix(base, "podfile") || strings.HasPrefix(base, "package.json") || strings.HasPrefix(base, "cargo.") || strings.HasPrefix(base, "requirements") {
		return true
	}
	if base == "go.mod" || base == "go.sum" || base == "go.work" || base == "tsconfig.json" || strings.HasPrefix(base, "tsconfig.") || base == "composer.json" || base == "composer.lock" || base == "gemfile" || base == "gemfile.lock" || base == "build.gradle" || base == "build.gradle.kts" || base == "settings.gradle" || base == "settings.gradle.kts" || base == "package.swift" {
		return true
	}
	return false
}
