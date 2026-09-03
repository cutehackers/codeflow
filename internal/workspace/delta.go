package workspace

import (
	"fmt"
	"sort"
)

// WorkspaceDelta represents the structural difference between two snapshots (Raw §10.4).
type WorkspaceDelta struct {
	FromSnapshotID string   `json:"fromSnapshotId"`
	ToSnapshotID   string   `json:"toSnapshotId"`
	AddedPaths     []string `json:"addedPaths"`
	ModifiedPaths  []string `json:"modifiedPaths"`
	DeletedPaths   []string `json:"deletedPaths"`
	ChangedPaths   []string `json:"changedPaths"`
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

	sort.Strings(delta.AddedPaths)
	sort.Strings(delta.ModifiedPaths)
	sort.Strings(delta.DeletedPaths)
	sort.Strings(delta.ChangedPaths)

	return delta, nil
}
