package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// SnapshotEngine manages immutable DocumentRevisions, WorkspaceSnapshots,
// content CAS, and the atomic liveHead state (design-v2 §7, VS-03).
type SnapshotEngine struct {
	mu           sync.RWMutex
	repoRoot     string
	casDir       string
	currentEpoch string
	sequence     int
	liveHead     *WorkspaceSnapshot

	// In-memory indexing and caches
	revisions       map[string]*DocumentRevision
	snapshots       map[string]*WorkspaceSnapshot
	fileVersions    map[string]int // path -> highest documentVersion seen
	casCache        map[string][]byte
	batches         map[string]*ChangeBatch
	baseCache       map[string][]byte
	baseFingerprint string

	// Activity tracking (VS03-A6)
	activity     string
	lastEditTime time.Time
	pendingCount int
	activeScope  []string
}

// NewSnapshotEngine initializes a SnapshotEngine for repoRoot with an initial workspace epoch.
func NewSnapshotEngine(repoRoot, epoch string) (*SnapshotEngine, error) {
	if repoRoot == "" {
		return nil, fmt.Errorf("repoRoot must not be empty")
	}
	if epoch == "" {
		epoch = fmt.Sprintf("epoch-%s", time.Now().UTC().Format("20060102-150405"))
	}

	casDir := filepath.Join(repoRoot, DirName, "cas")
	if err := os.MkdirAll(casDir, 0o755); err != nil {
		return nil, fmt.Errorf("init cas dir: %w", err)
	}

	baseFP := computeBaseFingerprint(repoRoot)

	return &SnapshotEngine{
		repoRoot:        repoRoot,
		casDir:          casDir,
		currentEpoch:    epoch,
		sequence:        0,
		revisions:       make(map[string]*DocumentRevision),
		snapshots:       make(map[string]*WorkspaceSnapshot),
		fileVersions:    make(map[string]int),
		casCache:        make(map[string][]byte),
		batches:         make(map[string]*ChangeBatch),
		baseCache:       make(map[string][]byte),
		baseFingerprint: baseFP,
		activity:        "idle",
	}, nil
}

// ApplyVersionedEdit processes a single incoming EditRequest outside of an explicit transaction,
// creating an immutable DocumentRevision and updating liveHead with a new WorkspaceSnapshot.
func (e *SnapshotEngine) ApplyVersionedEdit(ctx context.Context, edit EditRequest) (*DocumentRevision, *WorkspaceSnapshot, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if edit.Path == "" {
		return nil, nil, fmt.Errorf("edit path must not be empty")
	}
	if edit.Source == "" {
		edit.Source = SourceIDEVersioned
	}
	currentVer := e.fileVersions[edit.Path]
	if edit.DocumentVersion <= 0 {
		edit.DocumentVersion = currentVer + 1
	} else if edit.DocumentVersion <= currentVer {
		return nil, nil, fmt.Errorf("non-monotonic documentVersion %d <= current %d for path %s", edit.DocumentVersion, currentVer, edit.Path)
	}

	// 1. Store content in CAS
	contentHash := sha256.Sum256(edit.Content)
	contentID := hex.EncodeToString(contentHash[:])
	if err := e.storeCAS(contentID, edit.Content); err != nil {
		return nil, nil, fmt.Errorf("store content CAS: %w", err)
	}

	now := time.Now().UTC()
	revID := fmt.Sprintf("rev-%s-%d-%s", sanitizePath(edit.Path), edit.DocumentVersion, contentID[:8])

	rev := &DocumentRevision{
		SchemaID:        "https://codeflow.local/schemas/document-revision.schema.json",
		SchemaVersion:   1,
		RevisionID:      revID,
		Path:            edit.Path,
		DocumentVersion: edit.DocumentVersion,
		ContentID:       contentID,
		Source:          edit.Source,
		WorkspaceEpoch:  e.currentEpoch,
		CreatedAt:       now,
		Content:         string(edit.Content),
		ByteLength:      len(edit.Content),
	}

	e.revisions[rev.RevisionID] = rev
	e.fileVersions[edit.Path] = edit.DocumentVersion

	// 2. Derive new snapshot
	e.sequence++
	snapID := fmt.Sprintf("snap-%s-%04d", e.currentEpoch, e.sequence)

	newEntries := make(map[string]SnapshotEntry)
	var parentID *string
	if e.liveHead != nil {
		pID := e.liveHead.SnapshotID
		parentID = &pID
		// Copy previous snapshot entries
		for k, v := range e.liveHead.Entries {
			newEntries[k] = v
		}
	}

	// Apply updated entry
	newEntries[edit.Path] = SnapshotEntry{
		RevisionID:      rev.RevisionID,
		ContentID:       rev.ContentID,
		DocumentVersion: rev.DocumentVersion,
		ByteLength:      rev.ByteLength,
	}

	computedBasis := computeBasisFingerprint(e.baseFingerprint, newEntries)

	// Update previous liveHead marker
	if e.liveHead != nil {
		e.liveHead.LiveHead = false
	}

	snap := &WorkspaceSnapshot{
		SchemaID:                 "https://codeflow.local/schemas/workspace-snapshot.schema.json",
		SchemaVersion:            1,
		SnapshotID:               snapID,
		ParentSnapshotID:         parentID,
		WorkspaceEpoch:           e.currentEpoch,
		Sequence:                 e.sequence,
		LiveHead:                 true,
		ComputedBasisID:          computedBasis,
		CreatedAt:                now,
		ConfigurationFingerprint: "config-default",
		Entries:                  newEntries,
	}

	e.snapshots[snap.SnapshotID] = snap
	e.liveHead = snap

	// Update activity state
	e.activity = "editing"
	e.lastEditTime = now
	e.activeScope = []string{edit.Path}

	return rev, snap, nil
}

// LiveHead returns the current live workspace snapshot or nil if none.
func (e *SnapshotEngine) LiveHead() *WorkspaceSnapshot {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.liveHead
}

// GetSnapshot retrieves a snapshot by ID.
func (e *SnapshotEngine) GetSnapshot(snapshotID string) (*WorkspaceSnapshot, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	snap, ok := e.snapshots[snapshotID]
	if !ok {
		return nil, fmt.Errorf("snapshot %s not found", snapshotID)
	}
	return snap, nil
}

// GetRevision retrieves a revision by ID.
func (e *SnapshotEngine) GetRevision(revisionID string) (*DocumentRevision, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	rev, ok := e.revisions[revisionID]
	if !ok {
		return nil, fmt.Errorf("revision %s not found", revisionID)
	}
	return rev, nil
}

// CurrentActivity reports the current activity status.
func (e *SnapshotEngine) CurrentActivity() ActivityStatus {
	e.mu.RLock()
	defer e.mu.RUnlock()

	now := time.Now().UTC()
	var lagMs int64
	if !e.lastEditTime.IsZero() {
		lagMs = now.Sub(e.lastEditTime).Milliseconds()
	}

	snapID := ""
	if e.liveHead != nil {
		snapID = e.liveHead.SnapshotID
	}

	return ActivityStatus{
		Activity:          e.activity,
		AnalysisLagMs:     lagMs,
		PendingRevisions:  e.pendingCount,
		CurrentSnapshotID: snapID,
		WorkspaceEpoch:    e.currentEpoch,
		Timestamp:         now,
		Scope:             e.activeScope,
	}
}

// SetActivity updates the current activity status (e.g. "analyzing", "reconciling", "idle").
func (e *SnapshotEngine) SetActivity(activity string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.activity = activity
}

// SetEpoch transitions the workspace to a new epoch (VS03-A7).
func (e *SnapshotEngine) SetEpoch(newEpoch string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if newEpoch == "" || newEpoch == e.currentEpoch {
		return
	}
	e.currentEpoch = newEpoch
	e.sequence = 0
	e.liveHead = nil
	e.fileVersions = make(map[string]int)
	e.baseCache = make(map[string][]byte)
	e.baseFingerprint = computeBaseFingerprint(e.repoRoot)
	e.activity = "reconciling"
}

// storeCAS writes bytes to the CAS storage and caches in-memory.
func (e *SnapshotEngine) storeCAS(contentID string, data []byte) error {
	e.casCache[contentID] = data
	filePath := filepath.Join(e.casDir, contentID)
	if _, err := os.Stat(filePath); err == nil {
		return nil // already exists
	}
	return os.WriteFile(filePath, data, 0o644)
}

// ReadCAS retrieves content bytes by contentID.
func (e *SnapshotEngine) ReadCAS(contentID string) ([]byte, error) {
	e.mu.RLock()
	if data, ok := e.casCache[contentID]; ok {
		e.mu.RUnlock()
		return data, nil
	}
	e.mu.RUnlock()

	filePath := filepath.Join(e.casDir, contentID)
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("read CAS %s: %w", contentID, err)
	}

	e.mu.Lock()
	e.casCache[contentID] = data
	e.mu.Unlock()

	return data, nil
}

// PruneOrphanCAS scans the CAS storage and deletes blobs that are not referenced by
// any living WorkspaceSnapshot or DocumentRevision in the engine (Raw §7.1 background GC).
// Returns the count of deleted blobs. Active snapshot leases and revisions are preserved.
func (e *SnapshotEngine) PruneOrphanCAS() (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	referenced := make(map[string]bool)
	for _, rev := range e.revisions {
		if rev.ContentID != "" {
			referenced[rev.ContentID] = true
		}
	}
	for _, snap := range e.snapshots {
		for _, entry := range snap.Entries {
			if entry.ContentID != "" {
				referenced[entry.ContentID] = true
			}
		}
	}

	entries, err := os.ReadDir(e.casDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("read CAS dir: %w", err)
	}

	prunedCount := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		contentID := entry.Name()
		if !referenced[contentID] {
			filePath := filepath.Join(e.casDir, contentID)
			if err := os.Remove(filePath); err == nil {
				delete(e.casCache, contentID)
				prunedCount++
			}
		}
	}

	return prunedCount, nil
}

func computeBaseFingerprint(repoRoot string) string {
	headFile := filepath.Join(repoRoot, ".git", "HEAD")
	if data, err := os.ReadFile(headFile); err == nil {
		h := sha256.Sum256(data)
		return hex.EncodeToString(h[:])
	}
	h := sha256.Sum256([]byte(repoRoot))
	return hex.EncodeToString(h[:])
}

func computeBasisFingerprint(baseFingerprint string, entries map[string]SnapshotEntry) string {
	keys := make([]string, 0, len(entries))
	for k := range entries {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	h := sha256.New()
	if baseFingerprint != "" {
		h.Write([]byte("base:" + baseFingerprint + "\n"))
	}
	for _, k := range keys {
		entry := entries[k]
		h.Write([]byte(fmt.Sprintf("%s:%s:%d\n", k, entry.ContentID, entry.DocumentVersion)))
	}
	return hex.EncodeToString(h.Sum(nil))
}

func sanitizePath(p string) string {
	var out []rune
	for _, r := range p {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			out = append(out, r)
		} else {
			out = append(out, '_')
		}
	}
	return string(out)
}
