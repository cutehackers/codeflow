package workspace

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	documentRevisionSchemaID  = "https://codeflow.local/schemas/rflsc.document-revision.v2.schema.json"
	workspaceSnapshotSchemaID = "https://codeflow.local/schemas/rflsc.workspace-snapshot.v2.schema.json"
	changeBatchSchemaID       = "https://codeflow.local/schemas/change-batch.schema.json"
	liveHeadSchemaID          = "https://codeflow.local/schemas/rflsc.workspace-live-head.v2.schema.json"
	epochTransitionSchemaID   = "https://codeflow.local/schemas/rflsc.workspace-epoch-transition.v2.schema.json"
)

// ErrLiveHeadConflict identifies a stale expected live-head identity at the
// workspace commit boundary. Callers can classify this without depending on
// an error string or exposing the compared snapshot IDs.
var ErrLiveHeadConflict = errors.New("workspace live-head conflict")

// Persistence is the replaceable durable write boundary for workspace
// artifacts. Implementations must make WriteAtomic all-or-nothing for the
// target path. Remove is used to quarantine incompatible active artifacts and
// to roll back staged artifacts after a failed publication.
type Persistence interface {
	WriteAtomic(path string, data []byte) error
	Remove(path string) error
}

type osPersistence struct{}

func (osPersistence) WriteAtomic(path string, data []byte) error {
	return atomicWriteFile(path, data)
}

func (osPersistence) Remove(path string) error { return os.Remove(path) }

// SnapshotEngine manages immutable DocumentRevisions, complete WorkspaceSnapshots,
// content CAS, and the durable atomic live-head state.
type SnapshotEngine struct {
	mu sync.RWMutex
	// rootCommitMu coordinates all in-process SnapshotEngine instances that
	// address the same canonical repository root. It is acquired before mu at
	// live-head publication boundaries, so a stale engine cannot commit while
	// another engine publishes a newer head.
	rootCommitMu  *sync.Mutex
	repoRoot      string
	canonicalRoot string
	codeflowDir   string
	casDir        string
	revisionsDir  string
	snapshotsDir  string
	batchesDir    string
	historyDir    string
	eventsDir     string

	currentEpoch          int64
	sequence              int
	liveHeadID            string
	liveHead              *WorkspaceSnapshot
	identity              WorkspaceIdentity
	loadedBaseFingerprint string
	persistence           Persistence
	captureHook           func(path string)
	liveHeadHookMu        sync.RWMutex
	liveHeadAttemptHook   func()

	// In-memory indexes and caches. Every byte in these maps is owned by the
	// engine and is copied on ingress and egress.
	revisions    map[string]*DocumentRevision
	snapshots    map[string]*WorkspaceSnapshot
	fileVersions map[string]int
	casCache     map[string][]byte
	batches      map[string]*ChangeBatch

	// Snapshot leases retain referenced content until Close is called.
	leases map[string]int

	activity         string
	lastEditTime     time.Time
	pendingCount     int
	activeScope      []string
	sourceAudit      SourceWriteAudit
	traceID          string
	ackTime          time.Time
	analysisStart    time.Time
	currentOrGapTime time.Time

	epochSubscribers map[chan EpochChange]struct{}
}

// snapshotEngineRootCommitLocks is a fixed-size process-wide lock table. A
// shard is deliberately used instead of an engine registry: engines do not
// own entries, and roots remain coordinated after any individual engine is
// discarded. Different roots may share a shard and only incur extra
// serialization.
var snapshotEngineRootCommitLocks [64]sync.Mutex

func snapshotEngineRootCommitMutex(canonicalRoot string) *sync.Mutex {
	digest := sha256.Sum256([]byte(canonicalRoot))
	return &snapshotEngineRootCommitLocks[int(digest[0])%len(snapshotEngineRootCommitLocks)]
}

func (e *SnapshotEngine) lockRootCommit() func() {
	if e == nil {
		return func() {}
	}
	mu := e.rootCommitMu
	if mu == nil {
		mu = snapshotEngineRootCommitMutex(e.canonicalRoot)
	}
	mu.Lock()
	return mu.Unlock
}

type persistedWorkspaceState struct {
	SchemaID                 string            `json:"schemaId"`
	SchemaVersion            int               `json:"schemaVersion"`
	WorkspaceEpoch           int64             `json:"workspaceEpoch"`
	Sequence                 int               `json:"sequence"`
	LiveHeadSnapshotID       string            `json:"liveHeadSnapshotId,omitempty"`
	BaseFingerprint          string            `json:"baseFingerprint,omitempty"`
	Identity                 WorkspaceIdentity `json:"identity"`
	RepositoryPathWriteAudit SourceWriteAudit  `json:"repositoryPathWriteAudit"`
	TraceID                  string            `json:"traceId,omitempty"`
	LastEditTime             time.Time         `json:"lastEditTime,omitempty"`
	AcknowledgedAt           time.Time         `json:"acknowledgedAt,omitempty"`
	AnalysisStartedAt        time.Time         `json:"analysisStartedAt,omitempty"`
	CurrentOrGapAt           time.Time         `json:"currentOrGapAt,omitempty"`
	PendingRevisions         int               `json:"pendingRevisions,omitempty"`
}

type rawWorkspaceState struct {
	WorkspaceEpoch json.RawMessage `json:"workspaceEpoch"`
}

type liveHeadRecord struct {
	SchemaID       string `json:"schemaId"`
	SchemaVersion  int    `json:"schemaVersion"`
	SnapshotID     string `json:"snapshotId"`
	WorkspaceEpoch int64  `json:"workspaceEpoch"`
	RootTreeID     string `json:"rootTreeId,omitempty"`
	Sequence       int    `json:"sequence"`
}

type snapshotBuild struct {
	revision      *DocumentRevision
	revisions     []*DocumentRevision
	entries       map[string]SnapshotEntry
	changed       []SnapshotChange
	versionUpdate map[string]int
}

// NewSnapshotEngine initializes or reopens the durable workspace state. An
// epoch of zero is valid and means “use the persisted epoch when present”.
func NewSnapshotEngine(repoRoot string, epoch int64) (*SnapshotEngine, error) {
	if repoRoot == "" {
		return nil, fmt.Errorf("repoRoot must not be empty")
	}
	if epoch < 0 {
		return nil, fmt.Errorf("workspace epoch must be non-negative, got %d", epoch)
	}
	absRoot, err := filepath.Abs(repoRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve repoRoot: %w", err)
	}
	info, err := os.Stat(absRoot)
	if err != nil {
		return nil, fmt.Errorf("stat repoRoot: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("repoRoot must be a directory")
	}
	canonicalRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve canonical repoRoot: %w", err)
	}
	rootCommitMu := snapshotEngineRootCommitMutex(canonicalRoot)
	rootCommitMu.Lock()
	defer rootCommitMu.Unlock()

	codeflowDir := filepath.Join(absRoot, DirName)
	e := &SnapshotEngine{
		rootCommitMu:     rootCommitMu,
		repoRoot:         absRoot,
		canonicalRoot:    canonicalRoot,
		codeflowDir:      codeflowDir,
		casDir:           filepath.Join(codeflowDir, "cas"),
		revisionsDir:     filepath.Join(codeflowDir, "workspace", "revisions"),
		snapshotsDir:     filepath.Join(codeflowDir, "workspace", "snapshots"),
		batchesDir:       filepath.Join(codeflowDir, "workspace", "batches"),
		historyDir:       filepath.Join(codeflowDir, "workspace", "historical", "incompatible"),
		eventsDir:        filepath.Join(codeflowDir, "workspace", "events"),
		currentEpoch:     epoch,
		revisions:        make(map[string]*DocumentRevision),
		snapshots:        make(map[string]*WorkspaceSnapshot),
		fileVersions:     make(map[string]int),
		casCache:         make(map[string][]byte),
		batches:          make(map[string]*ChangeBatch),
		leases:           make(map[string]int),
		activity:         "idle",
		epochSubscribers: make(map[chan EpochChange]struct{}),
		persistence:      osPersistence{},
	}

	for _, dir := range []string{e.casDir, e.revisionsDir, e.snapshotsDir, e.batchesDir, e.historyDir, e.eventsDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("init workspace state %s: %w", dir, err)
		}
	}

	stateEpoch, statePresent, legacyState, err := e.loadState()
	if err != nil {
		return nil, err
	}
	durableHead, durableHeadPresent, err := e.readDurableLiveHead()
	if err != nil {
		return nil, err
	}
	if legacyState {
		// The incompatible artifact has already been preserved by loadState.
		e.currentEpoch = maxInt64(epoch, stateEpoch+1)
		if durableHeadPresent {
			return nil, fmt.Errorf("legacy workspace state has an incompatible live head")
		}
	} else if statePresent {
		if epoch == 0 || stateEpoch > epoch {
			e.currentEpoch = stateEpoch
		}
		if durableHeadPresent && (durableHead.WorkspaceEpoch != stateEpoch || durableHead.Sequence != e.sequence || durableHead.SnapshotID != e.liveHeadID) {
			return nil, fmt.Errorf("workspace state and live head are inconsistent")
		}
	} else if durableHeadPresent {
		return nil, fmt.Errorf("workspace live head exists without workspace state")
	}
	legacyObjects, err := e.loadObjects()
	if err != nil {
		return nil, err
	}
	if !legacyState && statePresent && !durableHeadPresent && !legacyObjects {
		return nil, fmt.Errorf("workspace state has no live head")
	}
	if !legacyState && durableHeadPresent && durableHead.SnapshotID != "" {
		snap, ok := e.snapshots[durableHead.SnapshotID]
		if !ok || snap.SnapshotID != e.liveHeadID || snap.WorkspaceEpoch != durableHead.WorkspaceEpoch || snap.Sequence != durableHead.Sequence || snap.RootTreeID != durableHead.RootTreeID {
			return nil, fmt.Errorf("workspace live head does not match its snapshot")
		}
	}
	fingerprintChanged := statePresent && !legacyState && e.loadedBaseFingerprint != "" && e.loadedBaseFingerprint != computeBaseFingerprint(e.canonicalRoot)
	if !legacyState && (legacyObjects || fingerprintChanged) {
		// A numeric state pointer cannot make a legacy object current, and a
		// changed repository base cannot reuse the prior lineage. Both cases
		// advance once and detach the old head before the new state is exposed.
		reason := "repository_base_changed"
		if legacyObjects {
			reason = "incompatible_legacy_object"
		}
		if _, err := e.transitionEpochLocked(e.currentEpoch+1, reason, e.identity, false); err != nil {
			return nil, err
		}
	}
	if e.currentEpoch < 0 {
		return nil, fmt.Errorf("persisted workspace epoch must be non-negative")
	}
	if e.liveHeadID != "" {
		if snap, ok := e.snapshots[e.liveHeadID]; ok && snap.WorkspaceEpoch == e.currentEpoch {
			e.liveHead = snap
		} else {
			e.liveHeadID = ""
			e.liveHead = nil
		}
	}
	if e.sequence == 0 {
		for _, snap := range e.snapshots {
			if snap.WorkspaceEpoch == e.currentEpoch && snap.Sequence > e.sequence {
				e.sequence = snap.Sequence
			}
		}
	}
	if e.sourceAudit.CapturedSnapshotTreeDigest == "" && e.liveHead != nil {
		e.sourceAudit.CapturedSnapshotTreeDigest = e.liveHead.RootTreeID
	}
	if err := e.persistState(); err != nil {
		return nil, err
	}
	if err := e.persistLiveHead(); err != nil {
		return nil, err
	}
	return e, nil
}

func (e *SnapshotEngine) loadState() (epoch int64, present, legacy bool, err error) {
	statePath := filepath.Join(e.codeflowDir, "workspace", "state.json")
	data, readErr := os.ReadFile(statePath)
	if errors.Is(readErr, os.ErrNotExist) {
		return e.currentEpoch, false, false, nil
	}
	if readErr != nil {
		return 0, false, false, fmt.Errorf("read workspace state: %w", readErr)
	}
	var raw rawWorkspaceState
	if err := json.Unmarshal(data, &raw); err != nil {
		return 0, false, false, fmt.Errorf("unmarshal workspace state: %w", err)
	}
	if len(raw.WorkspaceEpoch) == 0 || raw.WorkspaceEpoch[0] == '"' {
		if _, archiveErr := e.archiveIncompatible("state.json", data); archiveErr != nil {
			return 0, false, false, archiveErr
		}
		return 0, true, true, nil
	}
	var state persistedWorkspaceState
	if err := json.Unmarshal(data, &state); err != nil {
		return 0, false, false, fmt.Errorf("unmarshal workspace state: %w", err)
	}
	if state.WorkspaceEpoch < 0 {
		return 0, false, false, fmt.Errorf("workspace state has negative workspaceEpoch")
	}
	e.currentEpoch = state.WorkspaceEpoch
	e.sequence = state.Sequence
	e.liveHeadID = state.LiveHeadSnapshotID
	e.identity = state.Identity
	e.loadedBaseFingerprint = state.BaseFingerprint
	e.sourceAudit = cloneSourceWriteAudit(state.RepositoryPathWriteAudit)
	e.traceID = state.TraceID
	e.lastEditTime = state.LastEditTime
	e.ackTime = state.AcknowledgedAt
	e.analysisStart = state.AnalysisStartedAt
	e.currentOrGapTime = state.CurrentOrGapAt
	e.pendingCount = state.PendingRevisions
	return state.WorkspaceEpoch, true, false, nil
}

func (e *SnapshotEngine) loadObjects() (bool, error) {
	legacyObjects := false
	loadDir := func(dir string, snapshot bool) error {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}
			filePath := filepath.Join(dir, entry.Name())
			data, err := os.ReadFile(filePath)
			if err != nil {
				return fmt.Errorf("read workspace object %s: %w", filePath, err)
			}
			var raw struct {
				WorkspaceEpoch json.RawMessage `json:"workspaceEpoch"`
			}
			if err := json.Unmarshal(data, &raw); err != nil {
				return fmt.Errorf("unmarshal workspace object %s: %w", filePath, err)
			}
			if len(raw.WorkspaceEpoch) > 0 && raw.WorkspaceEpoch[0] == '"' {
				if _, err := e.archiveIncompatible("object-"+entry.Name(), data); err != nil {
					return err
				}
				// The archived bytes are the recovery copy. Remove the
				// incompatible file from the active object directory so the next
				// restart cannot migrate it a second time.
				if err := e.persistence.Remove(filePath); err != nil && !errors.Is(err, os.ErrNotExist) {
					return fmt.Errorf("quarantine incompatible object %s: %w", filePath, err)
				}
				legacyObjects = true
				continue
			}
			if snapshot {
				var snap WorkspaceSnapshot
				if err := json.Unmarshal(data, &snap); err != nil {
					return fmt.Errorf("unmarshal snapshot %s: %w", filePath, err)
				}
				if snap.WorkspaceEpoch < 0 {
					return fmt.Errorf("snapshot %s has negative workspaceEpoch", filePath)
				}
				if snap.RootTreeID == "" {
					snap.RootTreeID = computeRootTreeID(snap.Entries)
				}
				if snap.ChangedEntries == nil {
					snap.ChangedEntries = []SnapshotChange{}
				}
				if snap.RepositoryPathWriteAudit.CapturedSnapshotTreeDigest == "" {
					snap.RepositoryPathWriteAudit.CapturedSnapshotTreeDigest = snap.RootTreeID
				}
				e.snapshots[snap.SnapshotID] = cloneSnapshot(&snap)
			} else {
				var rev DocumentRevision
				if err := json.Unmarshal(data, &rev); err != nil {
					return fmt.Errorf("unmarshal revision %s: %w", filePath, err)
				}
				if rev.WorkspaceEpoch < 0 {
					return fmt.Errorf("revision %s has negative workspaceEpoch", filePath)
				}
				e.revisions[rev.RevisionID] = cloneRevision(&rev)
				if rev.Source != SourceRepositoryCapture && rev.DocumentVersion > e.fileVersions[rev.Path] && rev.WorkspaceEpoch == e.currentEpoch {
					e.fileVersions[rev.Path] = rev.DocumentVersion
				}
			}
		}
		return nil
	}
	if err := loadDir(e.revisionsDir, false); err != nil {
		return false, err
	}
	if err := loadDir(e.snapshotsDir, true); err != nil {
		return false, err
	}
	if err := e.loadBatches(&legacyObjects); err != nil {
		return false, err
	}
	return legacyObjects, nil
}

func (e *SnapshotEngine) loadBatches(legacyObjects *bool) error {
	entries, err := os.ReadDir(e.batchesDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		filePath := filepath.Join(e.batchesDir, entry.Name())
		data, err := os.ReadFile(filePath)
		if err != nil {
			return fmt.Errorf("read workspace batch %s: %w", filePath, err)
		}
		var raw struct {
			WorkspaceEpoch json.RawMessage `json:"workspaceEpoch"`
		}
		if err := json.Unmarshal(data, &raw); err != nil {
			return fmt.Errorf("unmarshal workspace batch %s: %w", filePath, err)
		}
		if len(raw.WorkspaceEpoch) > 0 && raw.WorkspaceEpoch[0] == '"' {
			if _, err := e.archiveIncompatible("batch-"+entry.Name(), data); err != nil {
				return err
			}
			if err := e.persistence.Remove(filePath); err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("quarantine incompatible batch %s: %w", filePath, err)
			}
			*legacyObjects = true
			continue
		}
		var batch ChangeBatch
		if err := json.Unmarshal(data, &batch); err != nil {
			return fmt.Errorf("unmarshal batch %s: %w", filePath, err)
		}
		if batch.WorkspaceEpoch < 0 {
			return fmt.Errorf("batch %s has negative workspaceEpoch", filePath)
		}
		e.batches[batch.BatchID] = cloneBatch(&batch)
	}
	return nil
}

func (e *SnapshotEngine) archiveIncompatible(name string, data []byte) (string, error) {
	name = sanitizePath(name)
	if name == "" {
		name = "artifact"
	}
	ref := filepath.Join(e.historyDir, fmt.Sprintf("%s-%d.json", name, time.Now().UTC().UnixNano()))
	if err := e.persistence.WriteAtomic(ref, data); err != nil {
		return "", fmt.Errorf("preserve incompatible artifact: %w", err)
	}
	return ref, nil
}

// ApplyVersionedEdit accepts one edit and atomically publishes one complete
// workspace snapshot containing every path captured at commit time.
func (e *SnapshotEngine) ApplyVersionedEdit(ctx context.Context, edit EditRequest) (*DocumentRevision, *WorkspaceSnapshot, error) {
	if err := contextErr(ctx); err != nil {
		return nil, nil, err
	}
	content := append([]byte(nil), edit.Content...)
	unlockRoot := e.lockRootCommit()
	defer unlockRoot()
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.ensureCachedLiveHeadCurrentLocked(); err != nil {
		return nil, nil, err
	}
	norm, err := validateRepositoryPath(e.canonicalRoot, edit.Path, true)
	if err != nil {
		return nil, nil, err
	}
	edit.Path = norm
	edit.Content = content
	if edit.Source == "" {
		edit.Source = SourceIDEVersioned
	}
	if !isAcceptedEditSource(edit.Source) {
		return nil, nil, fmt.Errorf("unsupported edit source %q", edit.Source)
	}
	if edit.DocumentVersion == 0 && edit.Source == SourceWatcherFallback {
		edit.DocumentVersion = e.fileVersions[edit.Path] + 1
	}
	if edit.DocumentVersion < 1 {
		return nil, nil, &InvalidDocumentVersion{Path: edit.Path, Version: edit.DocumentVersion}
	}
	if current := e.fileVersions[edit.Path]; edit.DocumentVersion <= current {
		return nil, nil, &DocumentVersionConflict{Path: edit.Path, IncomingVersion: edit.DocumentVersion, CurrentVersion: current}
	}
	build, err := e.buildSnapshotLocked(ctx, []EditRequest{edit})
	if err != nil {
		return nil, nil, err
	}
	snap, err := e.publishSnapshotLocked(build, time.Now().UTC(), nil)
	if err != nil {
		return nil, nil, err
	}
	return cloneRevision(build.revision), cloneSnapshotForQuery(snap, e.liveHeadID), nil
}

func (e *SnapshotEngine) buildSnapshotLocked(ctx context.Context, edits []EditRequest) (*snapshotBuild, error) {
	return e.buildSnapshotWithRemovalsLocked(ctx, edits, nil)
}

func (e *SnapshotEngine) buildSnapshotWithRemovalsLocked(ctx context.Context, edits []EditRequest, removals map[string]bool) (*snapshotBuild, error) {
	if err := contextErr(ctx); err != nil {
		return nil, err
	}
	overlay := make(map[string]EditRequest, len(edits))
	for _, edit := range edits {
		norm, err := validateRepositoryPath(e.canonicalRoot, edit.Path, true)
		if err != nil {
			return nil, err
		}
		edit.Path = norm
		overlay[norm] = EditRequest{Path: norm, Content: append([]byte(nil), edit.Content...), DocumentVersion: edit.DocumentVersion, Source: edit.Source}
	}

	entries, revisions, changes, versions, err := e.captureCompleteTreeWithRemovalsLocked(ctx, overlay, removals)
	if err != nil {
		return nil, err
	}
	for _, edit := range edits {
		if err := contextErr(ctx); err != nil {
			return nil, err
		}
		content := append([]byte(nil), edit.Content...)
		contentID := hashBytes(content)
		if err := e.storeCASLocked(contentID, content); err != nil {
			return nil, fmt.Errorf("store content CAS: %w", err)
		}
		rev := &DocumentRevision{
			SchemaID:        documentRevisionSchemaID,
			SchemaVersion:   2,
			RevisionID:      fmt.Sprintf("rev-%d-path-%s-%d-%s", e.currentEpoch, pathIdentity(edit.Path), edit.DocumentVersion, contentID[:8]),
			Path:            edit.Path,
			DocumentVersion: edit.DocumentVersion,
			ContentID:       contentID,
			Source:          edit.Source,
			WorkspaceEpoch:  e.currentEpoch,
			CreatedAt:       time.Now().UTC(),
			Content:         string(content),
			ByteLength:      len(content),
		}
		entries[edit.Path] = SnapshotEntry{RevisionID: rev.RevisionID, ContentID: rev.ContentID, DocumentVersion: rev.DocumentVersion, ByteLength: rev.ByteLength}
		revisions = append(revisions, rev)
		changes = append(changes, SnapshotChange{Path: edit.Path, DocumentRevisionID: rev.RevisionID})
		versions[edit.Path] = edit.DocumentVersion
	}
	var latestRevision *DocumentRevision
	if len(revisions) > 0 {
		latestRevision = revisions[len(revisions)-1]
	}
	return &snapshotBuild{revision: latestRevision, revisions: revisions, entries: entries, changed: dedupeChanges(changes), versionUpdate: versions}, nil
}

func (e *SnapshotEngine) captureCompleteTreeLocked(ctx context.Context, overlays map[string]EditRequest) (map[string]SnapshotEntry, []*DocumentRevision, []SnapshotChange, map[string]int, error) {
	return e.captureCompleteTreeWithRemovalsLocked(ctx, overlays, nil)
}

func (e *SnapshotEngine) captureCompleteTreeWithRemovalsLocked(ctx context.Context, overlays map[string]EditRequest, removals map[string]bool) (map[string]SnapshotEntry, []*DocumentRevision, []SnapshotChange, map[string]int, error) {
	const maxAttempts = 3
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		entries, revisions, changes, versions, err := e.captureCompleteTreeOnceLocked(ctx, overlays, removals)
		if err == nil {
			return entries, revisions, changes, versions, nil
		}
		if !errors.Is(err, ErrCaptureConflict) {
			return nil, nil, nil, nil, err
		}
		lastErr = err
		if attempt+1 < maxAttempts {
			time.Sleep(10 * time.Millisecond)
		}
	}
	// A failed bounded reconciliation leaves the previous head untouched. The
	// next ingress can retry from the durable prior state.
	e.activity = "reconciling"
	_ = e.persistState()
	return nil, nil, nil, nil, lastErr
}

func (e *SnapshotEngine) captureCompleteTreeOnceLocked(ctx context.Context, overlays map[string]EditRequest, removals map[string]bool) (map[string]SnapshotEntry, []*DocumentRevision, []SnapshotChange, map[string]int, error) {
	entries := make(map[string]SnapshotEntry)
	var revisions []*DocumentRevision
	var changes []SnapshotChange
	versions := make(map[string]int)
	capturedContent := make(map[string]string)
	previousEntries := e.liveHeadEntries()
	err := filepath.WalkDir(e.canonicalRoot, func(fullPath string, dirEntry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, relErr := filepath.Rel(e.canonicalRoot, fullPath)
		if relErr != nil {
			return relErr
		}
		if rel == "." {
			return nil
		}
		rel = filepath.ToSlash(rel)
		first := strings.Split(rel, "/")[0]
		if first == DirName || first == ".git" {
			if dirEntry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if dirEntry.IsDir() {
			return nil
		}
		if _, overridden := overlays[rel]; overridden {
			return nil
		}
		if dirEntry.Type()&os.ModeSymlink != 0 {
			if _, err := validateRepositoryPath(e.canonicalRoot, rel, false); err != nil {
				return err
			}
		}
		if !dirEntry.Type().IsRegular() && dirEntry.Type()&os.ModeSymlink == 0 {
			return nil
		}
		if err := contextErr(ctx); err != nil {
			return err
		}
		data, err := readStableReadOnlyFile(filepath.Join(e.canonicalRoot, filepath.FromSlash(rel)), 3)
		if err != nil {
			if errors.Is(err, ErrCaptureConflict) || os.IsNotExist(err) {
				return fmt.Errorf("%w: capture %s: %v", ErrCaptureConflict, rel, err)
			}
			return fmt.Errorf("capture %s: %w", rel, err)
		}
		contentID := hashBytes(data)
		capturedContent[rel] = contentID
		if e.captureHook != nil {
			e.captureHook(rel)
		}
		if previous, ok := previousEntries[rel]; ok && previous.ContentID == contentID {
			entries[rel] = previous
			versions[rel] = e.fileVersions[rel]
			return nil
		}
		if err := e.storeCASLocked(contentID, data); err != nil {
			return err
		}
		version := e.fileVersions[rel]
		if previous, ok := previousEntries[rel]; ok && previous.DocumentVersion >= version {
			version = previous.DocumentVersion + 1
		}
		if version < 1 {
			version = 1
		}
		rev := &DocumentRevision{
			SchemaID:        documentRevisionSchemaID,
			SchemaVersion:   2,
			RevisionID:      fmt.Sprintf("rev-%d-path-%s-base-%s", e.currentEpoch, pathIdentity(rel), contentID[:8]),
			Path:            rel,
			DocumentVersion: version,
			ContentID:       contentID,
			Source:          SourceRepositoryCapture,
			WorkspaceEpoch:  e.currentEpoch,
			CreatedAt:       time.Now().UTC(),
			Content:         string(data),
			ByteLength:      len(data),
		}
		entries[rel] = SnapshotEntry{RevisionID: rev.RevisionID, ContentID: contentID, DocumentVersion: version, ByteLength: len(data)}
		revisions = append(revisions, rev)
		if previous, ok := previousEntries[rel]; !ok || previous.ContentID != contentID {
			changes = append(changes, SnapshotChange{Path: rel, DocumentRevisionID: rev.RevisionID})
		}
		versions[rel] = version
		return nil
	})
	if err != nil {
		return nil, nil, nil, nil, err
	}
	if err := e.validateCapturedTreeLocked(ctx, capturedContent, overlays, removals); err != nil {
		return nil, nil, nil, nil, err
	}
	// A versioned edit may represent an unsaved virtual file that is not yet
	// present on disk. Preserve such prior snapshot entries unless this capture
	// explicitly replaces the path, so a later transaction cannot publish a
	// partial state merely because the OS tree does not contain the overlay.
	for path, previous := range previousEntries {
		if _, present := entries[path]; present {
			continue
		}
		if removals[path] {
			continue
		}
		if _, replaced := overlays[path]; replaced {
			continue
		}
		if e.isLegitimateUnsavedOverlayLocked(previous) {
			entries[path] = previous
		}
	}
	return entries, revisions, changes, versions, nil
}

func (e *SnapshotEngine) validateCapturedTreeLocked(ctx context.Context, expected map[string]string, overlays map[string]EditRequest, removals map[string]bool) error {
	seen := make(map[string]bool, len(expected))
	err := filepath.WalkDir(e.canonicalRoot, func(fullPath string, dirEntry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(e.canonicalRoot, fullPath)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		rel = filepath.ToSlash(rel)
		first := strings.Split(rel, "/")[0]
		if first == DirName || first == ".git" {
			if dirEntry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if dirEntry.IsDir() {
			return nil
		}
		if _, overridden := overlays[rel]; overridden {
			return nil
		}
		if dirEntry.Type()&os.ModeSymlink != 0 {
			if _, err := validateRepositoryPath(e.canonicalRoot, rel, false); err != nil {
				return err
			}
		}
		if !dirEntry.Type().IsRegular() && dirEntry.Type()&os.ModeSymlink == 0 {
			return nil
		}
		if err := contextErr(ctx); err != nil {
			return err
		}
		data, err := readStableReadOnlyFile(filepath.Join(e.canonicalRoot, filepath.FromSlash(rel)), 3)
		if err != nil {
			if os.IsNotExist(err) || errors.Is(err, ErrCaptureConflict) {
				return fmt.Errorf("%w: validate %s: %v", ErrCaptureConflict, rel, err)
			}
			return err
		}
		contentID := hashBytes(data)
		want, ok := expected[rel]
		if !ok || want != contentID {
			return fmt.Errorf("%w: path %s changed during whole-tree capture", ErrCaptureConflict, rel)
		}
		seen[rel] = true
		return nil
	})
	if err != nil {
		return err
	}
	for rel := range expected {
		if !seen[rel] {
			return fmt.Errorf("%w: path %s disappeared during whole-tree capture", ErrCaptureConflict, rel)
		}
	}
	return nil
}

func (e *SnapshotEngine) isLegitimateUnsavedOverlayLocked(entry SnapshotEntry) bool {
	rev, ok := e.revisions[entry.RevisionID]
	if !ok {
		return false
	}
	return rev.Source == SourceIDEVersioned || rev.Source == SourceAgentTransaction
}

func (e *SnapshotEngine) liveHeadEntries() map[string]SnapshotEntry {
	if e.liveHead == nil {
		return nil
	}
	return e.liveHead.Entries
}

func (e *SnapshotEngine) publishSnapshotLocked(build *snapshotBuild, now time.Time, batch *ChangeBatch) (*WorkspaceSnapshot, error) {
	nextSequence := e.sequence + 1
	entries := cloneEntries(build.entries)
	rootTreeID := computeRootTreeID(entries)
	snapID := fmt.Sprintf("snap-%d-%04d-%s", e.currentEpoch, nextSequence, rootTreeID[:8])
	var parentID *string
	if e.liveHeadID != "" {
		parent := e.liveHeadID
		parentID = &parent
	}
	snap := &WorkspaceSnapshot{
		SchemaID:                 workspaceSnapshotSchemaID,
		SchemaVersion:            2,
		SnapshotID:               snapID,
		ParentSnapshotID:         parentID,
		WorkspaceEpoch:           e.currentEpoch,
		Sequence:                 nextSequence,
		LiveHead:                 true,
		ComputedBasisID:          rootTreeID,
		RootTreeID:               rootTreeID,
		CreatedAt:                now,
		ConfigurationFingerprint: e.configurationFingerprintLocked(),
		DependencyFingerprint:    dependencyFingerprintForSnapshot(rootTreeID, e.configurationFingerprintLocked()),
		RepositoryID:             e.identity.RepositoryID,
		WorktreeID:               e.identity.WorktreeID,
		Entries:                  entries,
		ChangedEntries:           dedupeChanges(build.changed),
		RepositoryPathWriteAudit: SourceWriteAudit{
			CodeFlowWriteCount:         e.sourceAudit.CodeFlowWriteCount,
			RepositoryPathWrites:       append([]string(nil), e.sourceAudit.RepositoryPathWrites...),
			SourceIntegrityViolation:   e.sourceAudit.SourceIntegrityViolation,
			CapturedSnapshotTreeDigest: rootTreeID,
		},
	}
	candidateAudit := cloneSourceWriteAudit(e.sourceAudit)
	candidateAudit.CapturedSnapshotTreeDigest = rootTreeID
	artifacts := make([]persistedArtifact, 0, len(build.revisions)+4)
	rollback := func(err error) (*WorkspaceSnapshot, error) {
		rollbackPersistedArtifacts(artifacts)
		return nil, err
	}
	for _, rev := range build.revisions {
		if err := e.stageJSONLocked(&artifacts, filepath.Join(e.revisionsDir, sanitizePath(rev.RevisionID)+".json"), rev); err != nil {
			return rollback(err)
		}
	}
	if err := e.stageJSONLocked(&artifacts, filepath.Join(e.snapshotsDir, sanitizePath(snap.SnapshotID)+".json"), snap); err != nil {
		return rollback(err)
	}
	if batch != nil {
		if err := e.stageJSONLocked(&artifacts, filepath.Join(e.batchesDir, sanitizePath(batch.BatchID)+".json"), batch); err != nil {
			return rollback(err)
		}
	}
	state := e.persistedStateForLocked(e.currentEpoch, nextSequence, snap.SnapshotID, e.identity, candidateAudit)
	if err := e.stageJSONLocked(&artifacts, filepath.Join(e.codeflowDir, "workspace", "state.json"), &state); err != nil {
		return rollback(err)
	}
	headRecord := liveHeadRecord{SchemaID: liveHeadSchemaID, SchemaVersion: 2, SnapshotID: snap.SnapshotID, WorkspaceEpoch: e.currentEpoch, Sequence: nextSequence, RootTreeID: rootTreeID}
	if err := e.stageJSONLocked(&artifacts, filepath.Join(e.codeflowDir, "workspace", "live-head.json"), &headRecord); err != nil {
		return rollback(err)
	}

	// Only after every artifact, state record, and live-head pointer is durable
	// do the in-memory indexes become observable.
	e.sequence = nextSequence
	for p, v := range build.versionUpdate {
		if v > e.fileVersions[p] {
			e.fileVersions[p] = v
		}
	}
	for _, rev := range build.revisions {
		e.revisions[rev.RevisionID] = cloneRevision(rev)
	}
	e.snapshots[snap.SnapshotID] = cloneSnapshot(snap)
	e.liveHeadID = snap.SnapshotID
	e.liveHead = cloneSnapshot(snap)
	e.activity = "editing"
	e.lastEditTime = now
	e.traceID = "edit-" + snap.SnapshotID
	e.ackTime = time.Time{}
	e.analysisStart = time.Time{}
	e.currentOrGapTime = time.Time{}
	e.pendingCount++
	e.activeScope = changedPaths(snap.ChangedEntries)
	e.sourceAudit.CapturedSnapshotTreeDigest = rootTreeID
	if batch != nil {
		e.batches[batch.BatchID] = cloneBatch(batch)
	}
	if err := e.persistState(); err != nil {
		return rollback(fmt.Errorf("persist activity ledger: %w", err))
	}
	return cloneSnapshot(snap), nil
}

func dependencyFingerprintForSnapshot(rootTreeID, configuration string) string {
	h := sha256.Sum256([]byte(rootTreeID + "\x00" + configuration))
	return hex.EncodeToString(h[:])
}

// LiveHead returns a defensive copy of the current snapshot. The stored
// snapshot is never mutated to toggle its live marker.
func (e *SnapshotEngine) LiveHead() *WorkspaceSnapshot {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.liveHead == nil {
		return nil
	}
	return cloneSnapshotForQuery(e.liveHead, e.liveHeadID)
}

// LiveHeadID returns the current immutable head identity without exposing the
// mutable snapshot object. Publication transactions use it at their commit
// boundary to reject a stale first publication.
func (e *SnapshotEngine) LiveHeadID() string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.liveHeadID
}

// CanonicalRoot returns the repository root bound to this engine. It is a
// diagnostic identity accessor and does not grant mutation authority.
func (e *SnapshotEngine) CanonicalRoot() string {
	if e == nil {
		return ""
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.canonicalRoot
}

// WithLiveHead executes commit while the canonical-root authority lock and
// this engine's head lock are held. Callers must pass the exact head identity
// they validated. Versioned edits through any coordinated engine block until
// commit returns, so storage cannot publish a proof for an older head after a
// successful check.
func (e *SnapshotEngine) WithLiveHead(expectedID string, commit func() error) error {
	if e == nil || commit == nil {
		return fmt.Errorf("live-head commit boundary requires an engine and callback")
	}
	e.liveHeadHookMu.RLock()
	hook := e.liveHeadAttemptHook
	e.liveHeadHookMu.RUnlock()
	if hook != nil {
		hook()
	}
	unlockRoot := e.lockRootCommit()
	defer unlockRoot()
	e.mu.Lock()
	defer e.mu.Unlock()
	if expectedID == "" {
		return ErrLiveHeadConflict
	}
	if err := e.ensureCachedLiveHeadCurrentLocked(); err != nil {
		return err
	}
	if e.liveHeadID != expectedID {
		return ErrLiveHeadConflict
	}
	return commit()
}

// readDurableLiveHead reads the independently published live-head pointer
// while the canonical-root authority lock is held. WithLiveHead compares the
// complete pointer identity with the cached engine state before invoking its
// commit callback, so a second engine cannot authorize an older cache.
func (e *SnapshotEngine) readDurableLiveHead() (liveHeadRecord, bool, error) {
	var record liveHeadRecord
	data, err := os.ReadFile(filepath.Join(e.codeflowDir, "workspace", "live-head.json"))
	if errors.Is(err, os.ErrNotExist) {
		return record, false, nil
	}
	if err != nil {
		return record, false, fmt.Errorf("read durable workspace live head: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&record); err != nil {
		return record, false, fmt.Errorf("decode durable workspace live head: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return record, false, fmt.Errorf("durable workspace live head has trailing data")
		}
		return record, false, fmt.Errorf("decode durable workspace live head trailing data: %w", err)
	}
	if record.SchemaID != liveHeadSchemaID || record.SchemaVersion != 2 || record.WorkspaceEpoch < 0 || record.Sequence < 0 {
		return record, false, fmt.Errorf("durable workspace live head has invalid schema identity")
	}
	if record.SnapshotID == "" {
		if record.Sequence != 0 || record.RootTreeID != "" {
			return record, false, fmt.Errorf("durable workspace live head has invalid empty head")
		}
	} else if record.Sequence == 0 || record.RootTreeID == "" {
		return record, false, fmt.Errorf("durable workspace live head has incomplete head identity")
	}
	return record, true, nil
}

// cachedLiveHeadMatchesDurableLocked reports whether the engine's complete
// cached head still agrees with the independently durable pointer. Callers
// hold the canonical-root authority lock and e.mu while invoking it.
func (e *SnapshotEngine) cachedLiveHeadMatchesDurableLocked(record liveHeadRecord, present bool) bool {
	if !present || record.WorkspaceEpoch != e.currentEpoch || record.Sequence != e.sequence || record.SnapshotID != e.liveHeadID {
		return false
	}
	if record.SnapshotID == "" {
		return e.liveHead == nil && record.Sequence == 0 && record.RootTreeID == ""
	}
	return e.liveHead != nil && e.liveHead.SnapshotID == record.SnapshotID &&
		e.liveHead.WorkspaceEpoch == record.WorkspaceEpoch &&
		e.liveHead.Sequence == record.Sequence && e.liveHead.RootTreeID == record.RootTreeID
}

// ensureCachedLiveHeadCurrentLocked rejects a stale engine before it changes
// any durable workspace state. Callers hold the canonical-root authority lock
// and e.mu, which keeps this check adjacent to the state mutation.
func (e *SnapshotEngine) ensureCachedLiveHeadCurrentLocked() error {
	record, present, err := e.readDurableLiveHead()
	if err != nil {
		return err
	}
	if !e.cachedLiveHeadMatchesDurableLocked(record, present) {
		return ErrLiveHeadConflict
	}
	return nil
}

// GetSnapshot retrieves a defensive copy of a snapshot by ID.
func (e *SnapshotEngine) GetSnapshot(snapshotID string) (*WorkspaceSnapshot, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	snap, ok := e.snapshots[snapshotID]
	if !ok {
		return nil, fmt.Errorf("snapshot %s not found", snapshotID)
	}
	return cloneSnapshotForQuery(snap, e.liveHeadID), nil
}

// GetRevision retrieves a defensive copy of a revision by ID.
func (e *SnapshotEngine) GetRevision(revisionID string) (*DocumentRevision, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	rev, ok := e.revisions[revisionID]
	if !ok {
		return nil, fmt.Errorf("revision %s not found", revisionID)
	}
	return cloneRevision(rev), nil
}

// GetBatch retrieves a defensive copy of a durably committed change batch.
func (e *SnapshotEngine) GetBatch(batchID string) (*ChangeBatch, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	batch, ok := e.batches[batchID]
	if !ok {
		return nil, fmt.Errorf("batch %s not found", batchID)
	}
	return cloneBatch(batch), nil
}

// SetPersistence replaces the durable write boundary. It is intended for
// fault-injection and alternate persistence implementations and should be set
// before starting a publication.
func (e *SnapshotEngine) SetPersistence(p Persistence) error {
	if p == nil {
		return fmt.Errorf("persistence must not be nil")
	}
	e.mu.Lock()
	e.persistence = p
	e.mu.Unlock()
	return nil
}

// SetCaptureHook installs a callback invoked after each source file has been
// read during a whole-tree capture. It is a replacement boundary for tests and
// controlled filesystem integrations. Passing nil clears the callback.
func (e *SnapshotEngine) SetCaptureHook(hook func(path string)) {
	e.mu.Lock()
	e.captureHook = hook
	e.mu.Unlock()
}

// SetLiveHeadAttemptHook installs a diagnostic hook invoked immediately before
// WithLiveHead waits for the canonical-root authority lock. It exists for
// deterministic lifecycle tests and controlled integrations. Passing nil
// clears the hook.
func (e *SnapshotEngine) SetLiveHeadAttemptHook(hook func()) {
	if e == nil {
		return
	}
	e.liveHeadHookMu.Lock()
	e.liveHeadAttemptHook = hook
	e.liveHeadHookMu.Unlock()
}

// CurrentActivity reports a defensive activity snapshot tied to the current
// live head and integer workspace epoch.
func (e *SnapshotEngine) CurrentActivity() ActivityStatus {
	e.mu.RLock()
	defer e.mu.RUnlock()
	now := time.Now().UTC()
	var lagMs int64
	if !e.lastEditTime.IsZero() {
		lagMs = now.Sub(e.lastEditTime).Milliseconds()
	}
	return ActivityStatus{
		SchemaID:              "https://codeflow.local/schemas/rflsc.activity-state.v2.schema.json",
		SchemaVersion:         2,
		Activity:              e.activity,
		AnalysisLagMs:         lagMs,
		PendingRevisions:      e.pendingCount,
		CurrentSnapshotID:     e.liveHeadID,
		WorkspaceEpoch:        e.currentEpoch,
		Timestamp:             now,
		Scope:                 append([]string(nil), e.activeScope...),
		TraceID:               e.traceID,
		CapturedAt:            e.lastEditTime,
		AcknowledgedAt:        e.ackTime,
		AnalysisStartedAt:     e.analysisStart,
		CurrentOrGapAt:        e.currentOrGapTime,
		ActivityLatencyMs:     latencyMs(e.lastEditTime, e.ackTime),
		CurrentOrGapLatencyMs: latencyMs(e.lastEditTime, e.currentOrGapTime),
		MetricsMeasured:       !e.lastEditTime.IsZero() && !e.ackTime.IsZero(),
	}
}

// SetActivity updates the current activity status.
func (e *SnapshotEngine) SetActivity(activity string) {
	unlockRoot := e.lockRootCommit()
	defer unlockRoot()
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.ensureCachedLiveHeadCurrentLocked() != nil {
		return
	}
	e.activity = activity
	_ = e.persistState()
}

// AcknowledgeEdit records the UI acknowledgement for the latest captured
// snapshot. It is part of the same durable activity ledger as the edit.
func (e *SnapshotEngine) AcknowledgeEdit(snapshotID string) {
	unlockRoot := e.lockRootCommit()
	defer unlockRoot()
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.ensureCachedLiveHeadCurrentLocked() != nil {
		return
	}
	if snapshotID != "" && snapshotID != e.liveHeadID {
		return
	}
	if e.traceID == "" {
		e.traceID = "edit-" + e.liveHeadID
	}
	e.ackTime = time.Now().UTC()
	_ = e.persistState()
}

// BeginAnalysis records the checkpoint consumer's start without clearing the
// pending ledger. The pending count is decremented only by EndAnalysis.
func (e *SnapshotEngine) BeginAnalysis(snapshotID, traceID string) {
	unlockRoot := e.lockRootCommit()
	defer unlockRoot()
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.ensureCachedLiveHeadCurrentLocked() != nil {
		return
	}
	if snapshotID != "" && snapshotID != e.liveHeadID {
		return
	}
	e.activity = "analyzing"
	e.analysisStart = time.Now().UTC()
	if traceID != "" {
		e.traceID = traceID
	}
	_ = e.persistState()
}

// EndAnalysis closes the activity measurement. A successful current/gap
// artifact settles every revision covered by the analyzed snapshot. Revisions
// captured after that checkpoint remain pending and keep the workspace in the
// editing state. A failed publication does not advance the terminal activity
// ledger and therefore cannot silently acknowledge work that has no durable
// current/gap result.
func (e *SnapshotEngine) EndAnalysis(snapshotID string, publishedOrGap bool) {
	unlockRoot := e.lockRootCommit()
	defer unlockRoot()
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.ensureCachedLiveHeadCurrentLocked() != nil {
		return
	}
	if !publishedOrGap {
		if snapshotID == "" || snapshotID == e.liveHeadID {
			if e.pendingCount > 0 {
				e.activity = "editing"
			}
			_ = e.persistState()
		}
		return
	}
	selected, selectedOK := e.snapshots[snapshotID]
	if !selectedOK {
		return
	}
	current, currentOK := e.snapshots[e.liveHeadID]
	if !currentOK {
		return
	}
	// An analysis from an older epoch can finish after a branch/config
	// transition. It did not cover the new epoch and must not settle its
	// pending revisions or create a terminal timestamp.
	if current.WorkspaceEpoch != selected.WorkspaceEpoch {
		return
	}
	if current.Sequence <= selected.Sequence {
		e.pendingCount = 0
	} else {
		// Sequence is assigned once per complete snapshot. Counting the
		// descendants of the analyzed checkpoint settles coalesced edits while
		// retaining edits captured after the checkpoint began.
		remaining := 0
		for _, candidate := range e.snapshots {
			if candidate.WorkspaceEpoch == current.WorkspaceEpoch && candidate.Sequence > selected.Sequence && candidate.Sequence <= current.Sequence {
				remaining++
			}
		}
		e.pendingCount = remaining
	}
	e.currentOrGapTime = time.Now().UTC()
	if e.pendingCount == 0 {
		e.activity = "idle"
	} else {
		e.activity = "editing"
	}
	_ = e.persistState()
}

func latencyMs(start, end time.Time) int64 {
	if start.IsZero() || end.IsZero() || end.Before(start) {
		return -1
	}
	return end.Sub(start).Milliseconds()
}

// SetEpoch transitions to a new non-negative epoch. The previous live head is
// retained as historical data and is detached from the new current state.
func (e *SnapshotEngine) SetEpoch(newEpoch int64) error {
	return e.transitionEpoch(newEpoch, "explicit_epoch_change")
}

// AdvanceEpoch creates the next durable epoch for a branch, worktree, or
// incompatible configuration transition.
func (e *SnapshotEngine) AdvanceEpoch(reason string) (EpochChange, error) {
	e.mu.RLock()
	next := e.currentEpoch + 1
	e.mu.RUnlock()
	return e.transitionEpochRecord(next, reason)
}

func (e *SnapshotEngine) transitionEpoch(newEpoch int64, reason string) error {
	_, err := e.transitionEpochRecord(newEpoch, reason)
	return err
}

func (e *SnapshotEngine) transitionEpochRecord(newEpoch int64, reason string) (EpochChange, error) {
	unlockRoot := e.lockRootCommit()
	defer unlockRoot()
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.ensureCachedLiveHeadCurrentLocked(); err != nil {
		return EpochChange{}, err
	}
	return e.transitionEpochLocked(newEpoch, reason, e.identity, true)
}

func (e *SnapshotEngine) transitionEpochLocked(newEpoch int64, reason string, identity WorkspaceIdentity, notify bool) (EpochChange, error) {
	if newEpoch < 0 {
		return EpochChange{}, fmt.Errorf("workspace epoch must be non-negative, got %d", newEpoch)
	}
	if newEpoch == e.currentEpoch {
		return EpochChange{}, nil
	}
	if newEpoch < e.currentEpoch {
		return EpochChange{}, fmt.Errorf("workspace epoch must increase: %d <= %d", newEpoch, e.currentEpoch)
	}
	previous := e.currentEpoch
	previousSnapshot := e.liveHeadID
	historical := make([]string, 0)
	if previousSnapshot != "" {
		historical = append(historical, previousSnapshot)
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "epoch_change"
	}
	change := EpochChange{PreviousEpoch: previous, NewEpoch: newEpoch, Reason: reason, PreviousSnapshotID: previousSnapshot, OccurredAt: time.Now().UTC(), HistoricalSnapshotIDs: historical}
	eventPath := filepath.Join(e.eventsDir, fmt.Sprintf("epoch-%d-%d.json", newEpoch, change.OccurredAt.UnixNano()))
	candidateAudit := cloneSourceWriteAudit(e.sourceAudit)
	candidateAudit.CapturedSnapshotTreeDigest = ""
	state := e.persistedStateForLocked(newEpoch, 0, "", identity, candidateAudit)
	headRecord := liveHeadRecord{SchemaID: liveHeadSchemaID, SchemaVersion: 2, SnapshotID: "", WorkspaceEpoch: newEpoch, Sequence: 0}
	artifacts := make([]persistedArtifact, 0, 3)
	rollback := func(err error) (EpochChange, error) {
		rollbackPersistedArtifacts(artifacts)
		return EpochChange{}, err
	}
	if err := e.stageJSONLocked(&artifacts, eventPath, map[string]any{
		"schemaId": epochTransitionSchemaID, "schemaVersion": 2, "previousEpoch": previous, "newEpoch": newEpoch,
		"reason": reason, "historicalSnapshotIds": historical, "occurredAt": change.OccurredAt,
	}); err != nil {
		return rollback(err)
	}
	if err := e.stageJSONLocked(&artifacts, filepath.Join(e.codeflowDir, "workspace", "state.json"), &state); err != nil {
		return rollback(err)
	}
	if err := e.stageJSONLocked(&artifacts, filepath.Join(e.codeflowDir, "workspace", "live-head.json"), &headRecord); err != nil {
		return rollback(err)
	}

	// The identity, epoch, head, and event become visible together only after
	// every durable write has succeeded.
	e.currentEpoch = newEpoch
	e.sequence = 0
	e.liveHeadID = ""
	e.liveHead = nil
	e.identity = identity
	e.fileVersions = make(map[string]int)
	e.activity = "reconciling"
	e.lastEditTime = time.Time{}
	e.pendingCount = 0
	e.activeScope = nil
	e.sourceAudit = candidateAudit
	if notify {
		for subscriber := range e.epochSubscribers {
			select {
			case subscriber <- change:
			default:
			}
		}
	}
	return change, nil
}

// SetWorkspaceIdentity advances the epoch when repository/worktree or
// configuration identity changes.
func (e *SnapshotEngine) SetWorkspaceIdentity(identity WorkspaceIdentity) (EpochChange, error) {
	unlockRoot := e.lockRootCommit()
	defer unlockRoot()
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.ensureCachedLiveHeadCurrentLocked(); err != nil {
		return EpochChange{}, err
	}
	if e.identity == identity {
		return EpochChange{}, nil
	}
	next := e.currentEpoch + 1
	return e.transitionEpochLocked(next, "workspace_identity_change", identity, true)
}

// BindWorkspaceIdentity records the initial repository/worktree/configuration
// lineage without creating a workspace transition. A newly opened engine has
// no prior snapshot to invalidate, so identity binding must preserve the
// initial idle state and epoch. Once any lineage is visible callers must use
// SetWorkspaceIdentity, which creates the required epoch transition.
func (e *SnapshotEngine) BindWorkspaceIdentity(identity WorkspaceIdentity) error {
	if identity.RepositoryID == "" || identity.WorktreeID == "" || identity.ConfigurationFingerprint == "" {
		return fmt.Errorf("workspace identity is incomplete")
	}
	unlockRoot := e.lockRootCommit()
	defer unlockRoot()
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.ensureCachedLiveHeadCurrentLocked(); err != nil {
		return err
	}
	if e.identity == identity {
		return nil
	}
	if e.identity != (WorkspaceIdentity{}) || e.liveHeadID != "" || e.sequence != 0 || len(e.snapshots) != 0 {
		return fmt.Errorf("initial workspace identity can only be bound before lineage exists")
	}
	e.identity = identity
	return e.persistState()
}

func (e *SnapshotEngine) WorkspaceIdentity() WorkspaceIdentity {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.identity
}

// SubscribeEpochChanges subscribes to durable epoch transition events. The
// returned cancel function is idempotent.
func (e *SnapshotEngine) SubscribeEpochChanges() (<-chan EpochChange, func()) {
	ch := make(chan EpochChange, 8)
	e.mu.Lock()
	e.epochSubscribers[ch] = struct{}{}
	e.mu.Unlock()
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			e.mu.Lock()
			delete(e.epochSubscribers, ch)
			close(ch)
			e.mu.Unlock()
		})
	}
}

// WriteSource blocks CodeFlow-attributed writes to repository paths and
// records the attempt in system-owned workspace state.
func (e *SnapshotEngine) WriteSource(relPath string, _ []byte) error {
	norm, err := validateRepositoryPath(e.canonicalRoot, relPath, true)
	unlockRoot := e.lockRootCommit()
	defer unlockRoot()
	e.mu.Lock()
	defer e.mu.Unlock()
	if currentErr := e.ensureCachedLiveHeadCurrentLocked(); currentErr != nil {
		return currentErr
	}
	e.sourceAudit.CodeFlowWriteCount++
	e.sourceAudit.SourceIntegrityViolation = true
	if err == nil {
		e.sourceAudit.RepositoryPathWrites = append(e.sourceAudit.RepositoryPathWrites, norm)
	}
	_ = e.persistState()
	if err != nil {
		return err
	}
	return fmt.Errorf("%w: CodeFlow source writes are blocked for %s", ErrSourceIntegrityViolation, norm)
}

// SourceWriteAudit returns the current defensive audit snapshot.
func (e *SnapshotEngine) SourceWriteAudit() SourceWriteAudit {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return cloneSourceWriteAudit(e.sourceAudit)
}

// ReadHistoricalArtifact reads a preserved incompatible artifact from the
// system-owned historical directory.
func (e *SnapshotEngine) ReadHistoricalArtifact(ref string) ([]byte, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	clean := filepath.Clean(ref)
	rel, err := filepath.Rel(e.historyDir, clean)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return nil, &PathPolicyError{Path: ref, Reason: "historical artifact path escapes system state"}
	}
	data, err := os.ReadFile(clean)
	if err != nil {
		return nil, err
	}
	return append([]byte(nil), data...), nil
}

// ContentPath returns the system-owned CAS path for a content identity.
func (e *SnapshotEngine) ContentPath(contentID string) string {
	if len(contentID) != 64 {
		return ""
	}
	if _, err := hex.DecodeString(contentID); err != nil {
		return ""
	}
	return filepath.Join(e.casDir, contentID)
}

// PruneOrphanCAS deletes only unreferenced CAS blobs. Revisions, snapshots,
// and active leases all count as references.
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
			referenced[entry.ContentID] = true
		}
	}
	for snapshotID := range e.leases {
		if snap, ok := e.snapshots[snapshotID]; ok {
			for _, entry := range snap.Entries {
				referenced[entry.ContentID] = true
			}
		}
	}
	entries, err := os.ReadDir(e.casDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}
	pruned := 0
	for _, entry := range entries {
		// Workspace content blobs are extensionless SHA-256 identities. Storage
		// generation manifests share the CAS directory as JSON artifacts and
		// are retained by the generation pointer, not this workspace index.
		if entry.IsDir() || strings.HasSuffix(entry.Name(), ".json") || referenced[entry.Name()] {
			continue
		}
		if err := os.Remove(filepath.Join(e.casDir, entry.Name())); err == nil {
			delete(e.casCache, entry.Name())
			pruned++
		}
	}
	return pruned, nil
}

type persistedArtifact struct {
	path     string
	previous []byte
	existed  bool
}

func (e *SnapshotEngine) stageJSONLocked(artifacts *[]persistedArtifact, path string, value any) error {
	data, err := marshalJSON(value)
	if err != nil {
		return err
	}
	previous, readErr := os.ReadFile(path)
	artifact := persistedArtifact{path: path, previous: append([]byte(nil), previous...)}
	if readErr == nil {
		artifact.existed = true
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return readErr
	}
	*artifacts = append(*artifacts, artifact)
	if err := e.persistence.WriteAtomic(path, data); err != nil {
		return err
	}
	return nil
}

func rollbackPersistedArtifacts(artifacts []persistedArtifact) {
	for index := len(artifacts) - 1; index >= 0; index-- {
		artifact := artifacts[index]
		if artifact.existed {
			_ = atomicWriteFile(artifact.path, artifact.previous)
		} else {
			_ = os.Remove(artifact.path)
		}
	}
}

func (e *SnapshotEngine) persistRevisionLocked(rev *DocumentRevision) error {
	return e.writeJSONAtomic(filepath.Join(e.revisionsDir, sanitizePath(rev.RevisionID)+".json"), rev)
}

func (e *SnapshotEngine) persistSnapshotLocked(snap *WorkspaceSnapshot) error {
	return e.writeJSONAtomic(filepath.Join(e.snapshotsDir, sanitizePath(snap.SnapshotID)+".json"), snap)
}

func (e *SnapshotEngine) persistBatchLocked(batch *ChangeBatch) error {
	return e.writeJSONAtomic(filepath.Join(e.batchesDir, sanitizePath(batch.BatchID)+".json"), batch)
}

func (e *SnapshotEngine) persistedStateForLocked(epoch int64, sequence int, liveHeadID string, identity WorkspaceIdentity, audit SourceWriteAudit) persistedWorkspaceState {
	return persistedWorkspaceState{
		SchemaID:                 "https://codeflow.local/schemas/rflsc.workspace-state.v2.schema.json",
		SchemaVersion:            2,
		WorkspaceEpoch:           epoch,
		Sequence:                 sequence,
		LiveHeadSnapshotID:       liveHeadID,
		BaseFingerprint:          computeBaseFingerprint(e.canonicalRoot),
		Identity:                 identity,
		RepositoryPathWriteAudit: cloneSourceWriteAudit(audit),
		TraceID:                  e.traceID,
		LastEditTime:             e.lastEditTime,
		AcknowledgedAt:           e.ackTime,
		AnalysisStartedAt:        e.analysisStart,
		CurrentOrGapAt:           e.currentOrGapTime,
		PendingRevisions:         e.pendingCount,
	}
}

func (e *SnapshotEngine) persistState() error {
	state := e.persistedStateForLocked(e.currentEpoch, e.sequence, e.liveHeadID, e.identity, e.sourceAudit)
	return e.writeJSONAtomic(filepath.Join(e.codeflowDir, "workspace", "state.json"), &state)
}

func (e *SnapshotEngine) persistLiveHead() error {
	record := liveHeadRecord{SchemaID: liveHeadSchemaID, SchemaVersion: 2, SnapshotID: e.liveHeadID, WorkspaceEpoch: e.currentEpoch, Sequence: e.sequence}
	if e.liveHead != nil {
		record.RootTreeID = e.liveHead.RootTreeID
	}
	return e.writeJSONAtomic(filepath.Join(e.codeflowDir, "workspace", "live-head.json"), &record)
}

func (e *SnapshotEngine) writeJSONAtomic(path string, value any) error {
	data, err := marshalJSON(value)
	if err != nil {
		return err
	}
	return e.persistence.WriteAtomic(path, data)
}

func (e *SnapshotEngine) storeCASLocked(contentID string, data []byte) error {
	owned := append([]byte(nil), data...)
	path := e.ContentPath(contentID)
	if _, err := os.Stat(path); err == nil {
		e.casCache[contentID] = owned
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := e.persistence.WriteAtomic(path, owned); err != nil {
		// Do not make a failed disk write visible through the in-memory CAS.
		delete(e.casCache, contentID)
		return err
	}
	e.casCache[contentID] = owned
	return nil
}

func (e *SnapshotEngine) configurationFingerprintLocked() string {
	if e.identity.ConfigurationFingerprint != "" {
		return e.identity.ConfigurationFingerprint
	}
	return computeBaseFingerprint(e.canonicalRoot)
}

func contextErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

func isAcceptedEditSource(source string) bool {
	return source == SourceAgentTransaction || source == SourceIDEVersioned || source == SourceWatcherFallback
}

func readStableReadOnlyFile(path string, maxRetries int) ([]byte, error) {
	if maxRetries <= 0 {
		maxRetries = 1
	}
	for attempt := 0; attempt < maxRetries; attempt++ {
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		before, statErr := f.Stat()
		if statErr != nil {
			_ = f.Close()
			return nil, statErr
		}
		data, readErr := io.ReadAll(f)
		closeErr := f.Close()
		if readErr != nil {
			return nil, readErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		after, statErr := os.Stat(path)
		if statErr != nil {
			return nil, statErr
		}
		if before.Size() == after.Size() && before.ModTime().Equal(after.ModTime()) {
			return append([]byte(nil), data...), nil
		}
		if attempt+1 < maxRetries {
			time.Sleep(10 * time.Millisecond)
		}
	}
	return nil, fmt.Errorf("%w: file %s changed concurrently during read", ErrCaptureConflict, path)
}

func computeBaseFingerprint(repoRoot string) string {
	h := sha256.New()
	gitPath := filepath.Join(repoRoot, ".git")
	info, err := os.Stat(gitPath)
	if err == nil {
		if info.IsDir() {
			writeGitFingerprint(h, gitPath, "directory")
		} else if data, readErr := os.ReadFile(gitPath); readErr == nil {
			// Linked worktrees use a .git file containing a gitdir pointer.
			_, _ = h.Write([]byte("gitfile\x00"))
			_, _ = h.Write(data)
			gitDir := parseGitDirPointer(repoRoot, data)
			if gitDir != "" {
				writeGitFingerprint(h, gitDir, "linked")
			}
		}
	}
	if h.Size() == 0 {
		_, _ = h.Write([]byte(repoRoot))
	}
	return hex.EncodeToString(h.Sum(nil))
}

func parseGitDirPointer(repoRoot string, data []byte) string {
	line := strings.TrimSpace(string(data))
	if !strings.HasPrefix(line, "gitdir:") {
		return ""
	}
	value := strings.TrimSpace(strings.TrimPrefix(line, "gitdir:"))
	if value == "" {
		return ""
	}
	if !filepath.IsAbs(value) {
		value = filepath.Join(repoRoot, value)
	}
	return filepath.Clean(value)
}

func writeGitFingerprint(h io.Writer, gitDir, kind string) {
	_, _ = io.WriteString(h, kind+"\x00"+gitDir+"\x00")
	headPath := filepath.Join(gitDir, "HEAD")
	head, err := os.ReadFile(headPath)
	if err != nil {
		return
	}
	_, _ = io.WriteString(h, "HEAD\x00")
	_, _ = h.Write(head)
	headLine := strings.TrimSpace(string(head))
	if !strings.HasPrefix(headLine, "ref: ") {
		return
	}
	ref := strings.TrimSpace(strings.TrimPrefix(headLine, "ref: "))
	if ref == "" || strings.Contains(ref, "..") || filepath.IsAbs(ref) {
		return
	}
	_, _ = io.WriteString(h, "REF\x00"+ref+"\x00")
	refData, err := os.ReadFile(filepath.Join(gitDir, filepath.FromSlash(ref)))
	if err == nil {
		_, _ = h.Write(refData)
		return
	}
	// Packed refs are common in bare or compact repositories where the loose
	// ref file is absent.
	packed, err := os.ReadFile(filepath.Join(gitDir, "packed-refs"))
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(packed), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == ref {
			_, _ = io.WriteString(h, fields[0])
			return
		}
	}
}

func computeRootTreeID(entries map[string]SnapshotEntry) string {
	keys := make([]string, 0, len(entries))
	for key := range entries {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	h := sha256.New()
	for _, key := range keys {
		entry := entries[key]
		_, _ = fmt.Fprintf(h, "%s:%s:%s:%d:%d\n", key, entry.RevisionID, entry.ContentID, entry.DocumentVersion, entry.ByteLength)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func hashBytes(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// pathIdentity is a canonical, collision-resistant identity for a normalized
// repository path. It is kept separate from the display path because replacing
// separators with underscores makes distinct paths such as a/b.go and a_b.go
// collide.
func pathIdentity(relPath string) string {
	h := sha256.Sum256([]byte(relPath))
	return hex.EncodeToString(h[:])
}

func atomicWriteJSON(path string, value any) error {
	data, err := marshalJSON(value)
	if err != nil {
		return err
	}
	return atomicWriteFile(path, data)
}

func marshalJSON(value any) ([]byte, error) {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func atomicWriteFile(targetPath string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(targetPath), ".tmp-workspace-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, targetPath)
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
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

func dedupeChanges(changes []SnapshotChange) []SnapshotChange {
	seen := make(map[string]SnapshotChange, len(changes))
	for _, change := range changes {
		if change.Path == "" {
			continue
		}
		seen[change.Path] = change
	}
	paths := make([]string, 0, len(seen))
	for path := range seen {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	out := make([]SnapshotChange, 0, len(paths))
	for _, path := range paths {
		out = append(out, seen[path])
	}
	return out
}

func changedPaths(changes []SnapshotChange) []string {
	paths := make([]string, 0, len(changes))
	for _, change := range changes {
		paths = append(paths, change.Path)
	}
	return paths
}

func cloneEntries(in map[string]SnapshotEntry) map[string]SnapshotEntry {
	out := make(map[string]SnapshotEntry, len(in))
	for path, entry := range in {
		out[path] = entry
	}
	return out
}

func cloneRevision(in *DocumentRevision) *DocumentRevision {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}

func cloneBatch(in *ChangeBatch) *ChangeBatch {
	if in == nil {
		return nil
	}
	out := *in
	out.Revisions = append([]string(nil), in.Revisions...)
	if in.TransactionID != nil {
		txID := *in.TransactionID
		out.TransactionID = &txID
	}
	if in.CommittedAt != nil {
		committedAt := *in.CommittedAt
		out.CommittedAt = &committedAt
	}
	return &out
}

func cloneSnapshot(in *WorkspaceSnapshot) *WorkspaceSnapshot {
	if in == nil {
		return nil
	}
	out := *in
	if in.ParentSnapshotID != nil {
		parent := *in.ParentSnapshotID
		out.ParentSnapshotID = &parent
	}
	out.Entries = cloneEntries(in.Entries)
	out.ChangedEntries = append([]SnapshotChange(nil), in.ChangedEntries...)
	out.RepositoryPathWriteAudit = cloneSourceWriteAudit(in.RepositoryPathWriteAudit)
	return &out
}

func cloneSnapshotForQuery(in *WorkspaceSnapshot, liveHeadID string) *WorkspaceSnapshot {
	out := cloneSnapshot(in)
	if out != nil {
		out.LiveHead = out.SnapshotID == liveHeadID
	}
	return out
}

func cloneSourceWriteAudit(in SourceWriteAudit) SourceWriteAudit {
	in.RepositoryPathWrites = append([]string(nil), in.RepositoryPathWrites...)
	return in
}
