package workspace

import (
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// VFS provides a read-only virtual filesystem interface anchored to an
// immutable snapshot. It never reads the live repository after construction.
type VFS interface {
	SnapshotID() string
	ComputedBasisID() string
	ReadFile(relPath string) ([]byte, error)
	HasOverlay(relPath string) bool
}

// SnapshotLease extends VFS with explicit retention ownership. A lease keeps
// every referenced CAS object available until Close.
type SnapshotLease interface {
	VFS
	RootTreeID() string
	WorkspaceEpoch() int64
	ConfigurationFingerprint() string
	RepositoryID() string
	WorktreeID() string
	Documents() []SnapshotDocument
	SourceWriteAudit() SourceWriteAudit
	Close() error
}

type snapshotVFS struct {
	snapshot *WorkspaceSnapshot
	engine   *SnapshotEngine
	mu       sync.RWMutex
	closed   bool
}

// SnapshotVFS creates a lease anchored to snapshotID. All metadata and bytes
// are copied out of engine-owned state.
func (e *SnapshotEngine) SnapshotVFS(snapshotID string) (SnapshotLease, error) {
	e.mu.Lock()
	snap, ok := e.snapshots[snapshotID]
	if !ok {
		e.mu.Unlock()
		return nil, fmt.Errorf("snapshot %s not found", snapshotID)
	}
	e.leases[snapshotID]++
	lease := &snapshotVFS{snapshot: cloneSnapshot(snap), engine: e}
	e.mu.Unlock()
	return lease, nil
}

func (v *snapshotVFS) SnapshotID() string {
	return v.snapshot.SnapshotID
}

func (v *snapshotVFS) ComputedBasisID() string {
	return v.snapshot.ComputedBasisID
}

func (v *snapshotVFS) RootTreeID() string {
	return v.snapshot.RootTreeID
}

func (v *snapshotVFS) WorkspaceEpoch() int64 {
	return v.snapshot.WorkspaceEpoch
}

func (v *snapshotVFS) ConfigurationFingerprint() string {
	return v.snapshot.ConfigurationFingerprint
}

func (v *snapshotVFS) RepositoryID() string { return v.snapshot.RepositoryID }

func (v *snapshotVFS) WorktreeID() string { return v.snapshot.WorktreeID }

// Documents returns a sorted defensive copy of the selected snapshot's
// document inventory. It never exposes the engine's entries map.
func (v *snapshotVFS) Documents() []SnapshotDocument {
	v.mu.RLock()
	if v.closed {
		v.mu.RUnlock()
		return nil
	}
	documents := make([]SnapshotDocument, 0, len(v.snapshot.Entries))
	for path, entry := range v.snapshot.Entries {
		documents = append(documents, SnapshotDocument{Path: path, RevisionID: entry.RevisionID, ContentID: entry.ContentID, DocumentVersion: entry.DocumentVersion, ByteLength: entry.ByteLength})
	}
	v.mu.RUnlock()
	sort.Slice(documents, func(i, j int) bool { return documents[i].Path < documents[j].Path })
	return documents
}

func (v *snapshotVFS) SourceWriteAudit() SourceWriteAudit {
	v.mu.RLock()
	defer v.mu.RUnlock()
	audit := v.snapshot.RepositoryPathWriteAudit
	audit.RepositoryPathWrites = append([]string(nil), audit.RepositoryPathWrites...)
	return audit
}

func (v *snapshotVFS) HasOverlay(relPath string) bool {
	norm, err := NormalizeRepositoryPath(relPath)
	if err != nil {
		return false
	}
	_, ok := v.snapshot.Entries[norm]
	return ok
}

func (v *snapshotVFS) ReadFile(relPath string) ([]byte, error) {
	norm, err := NormalizeRepositoryPath(relPath)
	if err != nil {
		return nil, err
	}
	v.mu.RLock()
	closed := v.closed
	entry, ok := v.snapshot.Entries[norm]
	v.mu.RUnlock()
	if closed {
		return nil, fmt.Errorf("snapshot lease %s is closed", v.snapshot.SnapshotID)
	}
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrSnapshotPathNotFound, norm)
	}
	return v.engine.ReadCAS(entry.ContentID)
}

func (v *snapshotVFS) Close() error {
	v.mu.Lock()
	if v.closed {
		v.mu.Unlock()
		return nil
	}
	v.closed = true
	v.mu.Unlock()
	v.engine.mu.Lock()
	if count := v.engine.leases[v.snapshot.SnapshotID]; count <= 1 {
		delete(v.engine.leases, v.snapshot.SnapshotID)
	} else {
		v.engine.leases[v.snapshot.SnapshotID] = count - 1
	}
	v.engine.mu.Unlock()
	return nil
}

// ReadCAS returns a defensive copy of a CAS object.
func (e *SnapshotEngine) ReadCAS(contentID string) ([]byte, error) {
	if len(contentID) != 64 {
		return nil, fmt.Errorf("invalid content ID %q", contentID)
	}
	if _, err := hex.DecodeString(contentID); err != nil {
		return nil, fmt.Errorf("invalid content ID %q: %w", contentID, err)
	}
	e.mu.RLock()
	if data, ok := e.casCache[contentID]; ok {
		out := append([]byte(nil), data...)
		e.mu.RUnlock()
		return out, nil
	}
	e.mu.RUnlock()
	path := filepath.Join(e.casDir, contentID)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read CAS %s: %w", contentID, err)
	}
	e.mu.Lock()
	e.casCache[contentID] = append([]byte(nil), data...)
	e.mu.Unlock()
	return append([]byte(nil), data...), nil
}
