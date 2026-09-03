package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// VFS provides a read-only virtual filesystem interface anchored to an immutable snapshot (VS03-A5).
type VFS interface {
	SnapshotID() string
	ComputedBasisID() string
	ReadFile(relPath string) ([]byte, error)
	HasOverlay(relPath string) bool
}

type snapshotVFS struct {
	snapshot  *WorkspaceSnapshot
	engine    *SnapshotEngine
	mu        sync.RWMutex
	baseLease map[string][]byte
}

// SnapshotVFS creates a VFS anchored to the given snapshotID with an immutable lease (VS03-A5).
func (e *SnapshotEngine) SnapshotVFS(snapshotID string) (VFS, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	snap, ok := e.snapshots[snapshotID]
	if !ok {
		return nil, fmt.Errorf("snapshot %s not found", snapshotID)
	}

	return &snapshotVFS{
		snapshot:  snap,
		engine:    e,
		baseLease: make(map[string][]byte),
	}, nil
}

func (v *snapshotVFS) SnapshotID() string {
	return v.snapshot.SnapshotID
}

func (v *snapshotVFS) ComputedBasisID() string {
	return v.snapshot.ComputedBasisID
}

func (v *snapshotVFS) HasOverlay(relPath string) bool {
	norm := filepath.ToSlash(filepath.Clean(relPath))
	_, ok := v.snapshot.Entries[norm]
	return ok
}

func (v *snapshotVFS) ReadFile(relPath string) ([]byte, error) {
	norm := filepath.ToSlash(filepath.Clean(relPath))

	// 1. If present in snapshot overlay entries, read from immutable CAS
	if entry, ok := v.snapshot.Entries[norm]; ok {
		return v.engine.ReadCAS(entry.ContentID)
	}

	// 2. Check local lease cache for this snapshot instance
	v.mu.RLock()
	if data, ok := v.baseLease[norm]; ok {
		v.mu.RUnlock()
		return data, nil
	}
	v.mu.RUnlock()

	// 3. Check engine baseCache or read from disk under engine lock to prevent concurrent disk leaks
	v.engine.mu.Lock()
	if data, ok := v.engine.baseCache[norm]; ok {
		v.engine.mu.Unlock()
		v.mu.Lock()
		v.baseLease[norm] = data
		v.mu.Unlock()
		return data, nil
	}

	fullPath := filepath.Join(v.engine.repoRoot, norm)
	data, err := os.ReadFile(fullPath)
	if err != nil {
		v.engine.mu.Unlock()
		return nil, fmt.Errorf("vfs read %s: %w", norm, err)
	}

	v.engine.baseCache[norm] = data
	v.engine.mu.Unlock()

	v.mu.Lock()
	v.baseLease[norm] = data
	v.mu.Unlock()

	return data, nil
}
