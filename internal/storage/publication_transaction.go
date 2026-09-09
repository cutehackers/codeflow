package storage

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"codeflow/internal/contractharness"
)

// PublicationTransaction is the durable commit unit used by VS03. The
// manifest, active pointer, and sequenced event are prepared together and
// become visible only after the transaction marker is committed.
type PublicationTransaction struct {
	Manifest *GenerationProofManifest
	Pointer  *ActivePointer
	Event    []byte
	// Artifacts contains the immutable bytes referenced by Manifest.ArtifactRefs.
	// Production publication supplies these bytes here so the artifact cannot
	// become visible before the manifest/pointer/event transaction commits.
	// The map key must be the complete cas:sha256:<digest> reference.
	Artifacts                  map[string][]byte
	ExpectedLiveHeadSnapshotID string
	ActualLiveHeadSnapshotID   string
	// LiveHeadCommit is the workspace authority boundary for this transaction.
	// The callback must hold the workspace head lock while commit runs. A
	// caller-provided snapshot ID or a check-then-write boolean callback is not
	// sufficient because a new edit could advance the head between the check
	// and the first durable rename.
	LiveHeadCommit func(expectedID string, commit func() error) error
	// LiveHeadValidator is retained only so older callers fail with a useful
	// migration error. It is never used as a publication authority.
	LiveHeadValidator            func() bool
	ExpectedPreviousGenerationID string
}

// PublicationCommit describes the references made visible by a committed
// publication. Event remains a copy owned by the caller.
type PublicationCommit struct {
	ManifestRef string
	Pointer     *ActivePointer
	Event       []byte
}

var publicationFaults = struct {
	sync.Mutex
	byBase map[string]string
}{byBase: make(map[string]string)}

// SetPublicationFault injects one named failure at a publication boundary.
// It is intentionally small and recoverable so lifecycle tests can exercise
// every commit edge without replacing the production filesystem.
func (s *Storage) SetPublicationFault(step string) {
	publicationFaults.Lock()
	defer publicationFaults.Unlock()
	if strings.TrimSpace(step) == "" {
		delete(publicationFaults.byBase, s.baseDir)
		return
	}
	publicationFaults.byBase[s.baseDir] = step
}

func (s *Storage) publicationFault(step string) error {
	publicationFaults.Lock()
	defer publicationFaults.Unlock()
	if publicationFaults.byBase[s.baseDir] == step {
		return fmt.Errorf("injected publication failure at %s", step)
	}
	return nil
}

type publicationJournal struct {
	ManifestPath  string          `json:"manifestPath"`
	PointerPath   string          `json:"pointerPath"`
	EventPath     string          `json:"eventPath"`
	ArtifactPaths map[string]bool `json:"artifactPaths,omitempty"`
	HadManifest   bool            `json:"hadManifest"`
	OldPointer    []byte          `json:"oldPointer,omitempty"`
	HadPointer    bool            `json:"hadPointer"`
	HadEvent      bool            `json:"hadEvent"`
	// PriorEventLength and PriorEventHighWater are sufficient to roll back an
	// append-only event ledger. The journal must never duplicate the complete
	// ledger because that makes each publication's durable work grow with the
	// history.
	PriorEventLength    int64 `json:"priorEventLength"`
	PriorEventHighWater int   `json:"priorEventHighWater"`
}

type eventLedgerMetadata struct {
	Exists      bool
	Length      int64
	HighWater   int
	LastEventID string
}

func (s *Storage) publicationPaths() (journal, marker string) {
	root := filepath.Join(s.baseDir, "semantics", "publication")
	return filepath.Join(root, "journal.json"), filepath.Join(root, "commit.marker")
}

func (s *Storage) recoverPublication() error {
	journalPath, markerPath := s.publicationPaths()
	b, err := os.ReadFile(journalPath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read publication journal: %w", err)
	}
	var j publicationJournal
	if err := json.Unmarshal(b, &j); err != nil {
		return fmt.Errorf("decode publication journal: %w", err)
	}
	marker, markerErr := os.ReadFile(markerPath)
	if markerErr == nil && string(marker) == "committed\n" {
		_ = removeAndSync(journalPath)
		_ = removeAndSync(markerPath)
		return nil
	}
	// A process can die after one rename and before the commit marker. Restore
	// the prior pointer and remove all staged/partially visible event material.
	if j.HadPointer {
		if err := durableWrite(j.PointerPath, j.OldPointer, 0o600); err != nil {
			return fmt.Errorf("restore prior active pointer: %w", err)
		}
	} else if err := removeAndSync(j.PointerPath); err != nil {
		return fmt.Errorf("remove uncommitted active pointer: %w", err)
	}
	if j.HadEvent {
		if err := truncateAndSync(j.EventPath, j.PriorEventLength); err != nil {
			return fmt.Errorf("restore prior event ledger: %w", err)
		}
	} else if err := removeAndSync(j.EventPath); err != nil {
		return fmt.Errorf("remove uncommitted event ledger: %w", err)
	}
	for path, existed := range j.ArtifactPaths {
		if existed {
			continue
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("remove uncommitted publication artifact: %w", err)
		}
	}
	if !j.HadManifest {
		if err := os.Remove(j.ManifestPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("remove uncommitted publication manifest: %w", err)
		}
	}
	for _, path := range []string{journalPath, markerPath} {
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("remove uncommitted publication record: %w", err)
		}
	}
	if err := syncDirectory(filepath.Dir(journalPath)); err != nil {
		return fmt.Errorf("sync publication recovery: %w", err)
	}
	return nil
}

func durableWrite(path string, data []byte, mode fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".publication-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	// Sync the containing directory after rename so the name update survives
	// a crash. File fsync alone does not make the directory entry durable.
	return syncDirectory(filepath.Dir(path))
}

func syncDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

// truncateAndSync restores an existing event ledger to its journaled offset.
// It never rewrites the retained prefix, so recovery work is independent of
// the ledger's historical size.
func truncateAndSync(path string, length int64) error {
	if length < 0 {
		return fmt.Errorf("event ledger length must be non-negative, got %d", length)
	}
	f, err := os.OpenFile(path, os.O_WRONLY, 0o600)
	if errors.Is(err, fs.ErrNotExist) && length == 0 {
		return nil
	}
	if err != nil {
		return err
	}
	info, statErr := f.Stat()
	if statErr != nil {
		_ = f.Close()
		return statErr
	}
	if info.Size() < length {
		_ = f.Close()
		return fmt.Errorf("event ledger is shorter than prior offset: size=%d prior=%d", info.Size(), length)
	}
	if err := f.Truncate(length); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func removeAndSync(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return syncDirectory(filepath.Dir(path))
}

const maxPublicationEventRecordBytes = 1 << 20

// appendEventAndSync appends exactly one canonical JSON event record and
// fsyncs the existing ledger. Callers can truncate back to their journaled
// offset if any step after the write fails.
func appendEventAndSync(path string, event []byte) error {
	var compact bytes.Buffer
	if err := json.Compact(&compact, event); err != nil {
		return fmt.Errorf("compact event envelope: %w", err)
	}
	if compact.Len() == 0 || compact.Len() > maxPublicationEventRecordBytes {
		return fmt.Errorf("event record size must be between 1 and %d bytes", maxPublicationEventRecordBytes)
	}
	record := make([]byte, compact.Len()+1)
	copy(record, compact.Bytes())
	record[len(record)-1] = '\n'
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	if n, writeErr := f.Write(record); writeErr != nil {
		_ = f.Close()
		return writeErr
	} else if n != len(record) {
		_ = f.Close()
		return io.ErrShortWrite
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(path))
}

func lastByteBefore(f *os.File, end int64) (int64, error) {
	const chunkSize = int64(64 * 1024)
	buf := make([]byte, chunkSize)
	for end > 0 {
		start := end - chunkSize
		if start < 0 {
			start = 0
		}
		n, err := f.ReadAt(buf[:end-start], start)
		if err != nil && err != io.EOF {
			return -1, err
		}
		for i := n - 1; i >= 0; i-- {
			if buf[i] == '\n' {
				return start + int64(i), nil
			}
		}
		end = start
	}
	return -1, nil
}

// readEventLedgerMetadata reads only the last record to capture the rollback
// offset and durable sequence high-water. The append path therefore does not
// read or rewrite the historical ledger for each publication.
func readEventLedgerMetadata(path string) (eventLedgerMetadata, error) {
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return eventLedgerMetadata{}, nil
	}
	if err != nil {
		return eventLedgerMetadata{}, err
	}
	if info.IsDir() {
		return eventLedgerMetadata{}, fmt.Errorf("event ledger path is a directory")
	}
	metadata := eventLedgerMetadata{Exists: true, Length: info.Size()}
	if info.Size() == 0 {
		return metadata, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return eventLedgerMetadata{}, err
	}
	defer f.Close()
	last := []byte{0}
	if _, err := f.ReadAt(last, info.Size()-1); err != nil {
		return eventLedgerMetadata{}, err
	}
	if last[0] != '\n' {
		return eventLedgerMetadata{}, fmt.Errorf("event ledger ends with an unterminated record")
	}
	end := info.Size() - 1
	for {
		previousNewline, err := lastByteBefore(f, end)
		if err != nil {
			return eventLedgerMetadata{}, err
		}
		start := previousNewline + 1
		length := end - start
		if length > maxPublicationEventRecordBytes {
			return eventLedgerMetadata{}, fmt.Errorf("event record exceeds %d bytes", maxPublicationEventRecordBytes)
		}
		if length > 0 {
			record := make([]byte, length)
			if _, err := f.ReadAt(record, start); err != nil {
				return eventLedgerMetadata{}, err
			}
			if len(bytes.TrimSpace(record)) > 0 {
				var event struct {
					SchemaID      string `json:"schemaId"`
					SchemaVersion int    `json:"schemaVersion"`
					Sequence      int    `json:"sequence"`
					EventID       string `json:"eventId"`
					EventType     string `json:"eventType"`
				}
				if err := json.Unmarshal(record, &event); err != nil {
					return eventLedgerMetadata{}, fmt.Errorf("decode event ledger tail: %w", err)
				}
				if event.SchemaID != "https://codeflow.local/schemas/rflsc.event-envelope.v2.schema.json" || event.SchemaVersion != 2 || event.Sequence < 1 || event.EventID == "" || event.EventType == "snapshot_sync" {
					return eventLedgerMetadata{}, fmt.Errorf("event ledger tail is not a canonical persisted event")
				}
				metadata.HighWater = event.Sequence
				metadata.LastEventID = event.EventID
				return metadata, nil
			}
		}
		if previousNewline < 0 {
			return metadata, nil
		}
		end = previousNewline
	}
}

// PublishGeneration commits one generation proof, active pointer, and event.
// The workspace authority callback surrounds the entire durable commit. This
// prevents a head edit from landing after validation but before pointer/event
// visibility.
func (s *Storage) PublishGeneration(tx PublicationTransaction) (PublicationCommit, error) {
	if tx.LiveHeadCommit == nil {
		if tx.LiveHeadValidator != nil {
			return PublicationCommit{}, fmt.Errorf("check-then-write live-head validator is not a publication authority")
		}
		return PublicationCommit{}, fmt.Errorf("authoritative live-head commit boundary is required")
	}
	if tx.ExpectedLiveHeadSnapshotID == "" {
		return PublicationCommit{}, ErrCASConflict
	}
	var committed PublicationCommit
	err := tx.LiveHeadCommit(tx.ExpectedLiveHeadSnapshotID, func() error {
		var err error
		committed, err = s.publishGenerationLocked(tx)
		return err
	})
	if err != nil {
		return PublicationCommit{}, err
	}
	return committed, nil
}

func (s *Storage) publishGenerationLocked(tx PublicationTransaction) (PublicationCommit, error) {
	casLock.Lock()
	defer casLock.Unlock()

	if tx.Manifest == nil || tx.Pointer == nil {
		return PublicationCommit{}, fmt.Errorf("publication manifest and pointer are required")
	}
	const (
		manifestSchema = "https://codeflow.local/schemas/rflsc.generation-proof-manifest.v2.schema.json"
		pointerSchema  = "https://codeflow.local/schemas/rflsc.active-pointer.v2.schema.json"
	)
	if tx.Manifest.SchemaID != manifestSchema || tx.Manifest.SchemaVersion != 2 {
		return PublicationCommit{}, fmt.Errorf("publication manifest schema identity is not canonical")
	}
	if tx.Pointer.SchemaID != pointerSchema || tx.Pointer.SchemaVersion != 2 {
		return PublicationCommit{}, fmt.Errorf("publication pointer schema identity is not canonical")
	}
	if tx.Pointer.RepositoryID == "" || tx.Pointer.WorktreeID == "" || tx.Pointer.TaskID == "" {
		return PublicationCommit{}, fmt.Errorf("publication repository, worktree, and task identity are required")
	}
	if tx.Manifest.GenerationID == "" || tx.Pointer.GenerationID == "" || tx.Manifest.GenerationID != tx.Pointer.GenerationID {
		return PublicationCommit{}, fmt.Errorf("publication generation identity mismatch")
	}
	if tx.Manifest.WorkspaceEpoch < 0 || tx.Pointer.WorkspaceEpoch < 0 || tx.Manifest.WorkspaceEpoch != tx.Pointer.WorkspaceEpoch {
		return PublicationCommit{}, fmt.Errorf("publication workspace epoch mismatch")
	}
	if tx.ExpectedLiveHeadSnapshotID == "" || tx.ActualLiveHeadSnapshotID == "" || tx.ExpectedLiveHeadSnapshotID != tx.ActualLiveHeadSnapshotID {
		return PublicationCommit{}, ErrCASConflict
	}
	if tx.Manifest.ExpectedLiveHeadSnapshotID != tx.ExpectedLiveHeadSnapshotID || tx.Pointer.ExpectedLiveHeadSnapshotID != tx.ExpectedLiveHeadSnapshotID {
		return PublicationCommit{}, ErrCASConflict
	}
	if tx.Manifest.ComputedSnapshotID == "" {
		return PublicationCommit{}, fmt.Errorf("publication computed snapshot identity is required")
	}
	if tx.Manifest.ValidatedAgainstSnapshotID == "" || tx.Pointer.ValidatedAgainstSnapshotID == "" || tx.Manifest.ValidatedAgainstSnapshotID != tx.Pointer.ValidatedAgainstSnapshotID || tx.Pointer.ValidatedAgainstSnapshotID != tx.ExpectedLiveHeadSnapshotID {
		return PublicationCommit{}, fmt.Errorf("publication validated snapshot identity is incomplete or mismatched")
	}
	if tx.Manifest.ComputedBasisID == "" || tx.Pointer.ComputedBasisID == "" || tx.Manifest.ComputedBasisID != tx.Pointer.ComputedBasisID {
		return PublicationCommit{}, fmt.Errorf("publication computed basis identity is incomplete or mismatched")
	}
	if tx.Manifest.TaskIntentRevision < 1 || tx.Pointer.TaskIntentRevision != tx.Manifest.TaskIntentRevision || tx.Manifest.NormalizedQueryHash == "" || tx.Pointer.NormalizedQueryHash != tx.Manifest.NormalizedQueryHash {
		return PublicationCommit{}, fmt.Errorf("publication intent/query identity is incomplete or mismatched")
	}
	if tx.Manifest.AnalysisReadSetID == "" || tx.Manifest.CausalObservationClosureID == "" || tx.Manifest.CausalObservationClosureDigest == "" || tx.Pointer.FlowCount < 1 {
		return PublicationCommit{}, fmt.Errorf("publication closure identity is incomplete")
	}
	publication := tx.Manifest.CurrentPublication
	if publication.Eligibility != "passed" || publication.SnapshotGate != "passed" || publication.ClosureGate != "passed" || publication.EvidenceGate != "passed" || publication.SemanticAtomicityGate != "passed" || publication.TaskRelevanceGate != "passed" || publication.ComprehensionGate != "passed" {
		return PublicationCommit{}, fmt.Errorf("publication manifest has not passed every current gate")
	}
	if tx.ExpectedPreviousGenerationID != "" && tx.Pointer.ExpectedPreviousGenerationID != nil && *tx.Pointer.ExpectedPreviousGenerationID != tx.ExpectedPreviousGenerationID {
		return PublicationCommit{}, ErrCASConflict
	}
	if err := s.recoverPublication(); err != nil {
		return PublicationCommit{}, err
	}
	current, err := s.readActivePointerUnlocked()
	if err != nil {
		return PublicationCommit{}, err
	}
	if current == nil {
		if tx.ExpectedPreviousGenerationID != "" && tx.ExpectedPreviousGenerationID != "*" {
			return PublicationCommit{}, ErrCASConflict
		}
	} else {
		if tx.ExpectedPreviousGenerationID == "" || (tx.ExpectedPreviousGenerationID != "*" && current.GenerationID != tx.ExpectedPreviousGenerationID) {
			return PublicationCommit{}, ErrCASConflict
		}
	}
	if tx.Event == nil {
		return PublicationCommit{}, fmt.Errorf("publication event is required")
	}
	if tx.Manifest.ArtifactRefs.SemanticMap == "" {
		return PublicationCommit{}, fmt.Errorf("publication semantic-map artifact reference is required")
	}
	if len(tx.Artifacts) == 0 {
		return PublicationCommit{}, fmt.Errorf("publication artifact bytes are required")
	}

	if tx.Manifest.PublishedAt.IsZero() {
		tx.Manifest.PublishedAt = time.Now().UTC()
	}
	if tx.Pointer.PublishedAt.IsZero() {
		tx.Pointer.PublishedAt = tx.Manifest.PublishedAt
	}
	manifestData, err := json.MarshalIndent(tx.Manifest, "", "  ")
	if err != nil {
		return PublicationCommit{}, fmt.Errorf("marshal publication manifest: %w", err)
	}
	manifestRef := "cas:sha256:" + sha256Hex(manifestData)
	if tx.Pointer.ManifestObjectRef == "" {
		tx.Pointer.ManifestObjectRef = manifestRef
	}
	if tx.Pointer.ManifestObjectRef != manifestRef {
		return PublicationCommit{}, fmt.Errorf("publication manifest reference mismatch")
	}
	pointerData, err := json.MarshalIndent(tx.Pointer, "", "  ")
	if err != nil {
		return PublicationCommit{}, fmt.Errorf("marshal publication pointer: %w", err)
	}
	var eventMeta struct {
		SchemaID                   string  `json:"schemaId"`
		SchemaVersion              int     `json:"schemaVersion"`
		StreamID                   string  `json:"streamId"`
		Sequence                   int     `json:"sequence"`
		EventID                    string  `json:"eventId"`
		EventType                  string  `json:"eventType"`
		ComputedBasisID            *string `json:"computedBasisId"`
		ValidatedAgainstSnapshotID *string `json:"validatedAgainstSnapshotId"`
		GenerationID               *string `json:"generationId"`
	}
	if err := json.Unmarshal(tx.Event, &eventMeta); err != nil || eventMeta.Sequence < 1 || eventMeta.EventID == "" {
		return PublicationCommit{}, fmt.Errorf("publication event must have a positive sequence and event id")
	}
	if eventMeta.SchemaID != "https://codeflow.local/schemas/rflsc.event-envelope.v2.schema.json" || eventMeta.SchemaVersion != 2 || eventMeta.StreamID == "" || eventMeta.EventType != "generation.published" {
		return PublicationCommit{}, fmt.Errorf("publication event is not a canonical generation envelope")
	}
	if eventMeta.ComputedBasisID == nil || eventMeta.ValidatedAgainstSnapshotID == nil || eventMeta.GenerationID == nil ||
		*eventMeta.ComputedBasisID != tx.Manifest.ComputedBasisID ||
		*eventMeta.ValidatedAgainstSnapshotID != tx.Manifest.ValidatedAgainstSnapshotID ||
		*eventMeta.GenerationID != tx.Manifest.GenerationID ||
		*eventMeta.ComputedBasisID != tx.Pointer.ComputedBasisID ||
		*eventMeta.ValidatedAgainstSnapshotID != tx.Pointer.ValidatedAgainstSnapshotID ||
		*eventMeta.GenerationID != tx.Pointer.GenerationID {
		return PublicationCommit{}, fmt.Errorf("publication event identity does not match manifest")
	}
	if err := contractharness.ValidateGenerationProofManifestV2(manifestData); err != nil {
		return PublicationCommit{}, fmt.Errorf("publication manifest contract: %w", err)
	}
	if err := contractharness.ValidateActivePointerV2(pointerData); err != nil {
		return PublicationCommit{}, fmt.Errorf("publication pointer contract: %w", err)
	}
	if err := contractharness.ValidateEventEnvelopeV2(tx.Event); err != nil {
		return PublicationCommit{}, fmt.Errorf("publication event contract: %w", err)
	}

	journalPath, markerPath := s.publicationPaths()
	manifestPath := filepath.Join(s.baseDir, "cas", strings.TrimPrefix(manifestRef, "cas:sha256:")+".json")
	eventPath := filepath.Join(s.baseDir, "semantics", "events", "event-ledger.jsonl")
	oldPointerPath := filepath.Join(s.baseDir, "active-pointer.json")
	oldPointer, readErr := os.ReadFile(oldPointerPath)
	hadPointer := readErr == nil
	if readErr != nil && !errors.Is(readErr, fs.ErrNotExist) {
		return PublicationCommit{}, readErr
	}
	eventMetadata, eventReadErr := readEventLedgerMetadata(eventPath)
	if eventReadErr != nil {
		return PublicationCommit{}, fmt.Errorf("inspect event ledger: %w", eventReadErr)
	}
	if eventMeta.Sequence <= eventMetadata.HighWater {
		return PublicationCommit{}, fmt.Errorf("event sequence %d is not above durable high-water %d", eventMeta.Sequence, eventMetadata.HighWater)
	}
	if eventMeta.EventID == eventMetadata.LastEventID {
		return PublicationCommit{}, fmt.Errorf("event id %q already exists", eventMeta.EventID)
	}
	manifestExisting, manifestErr := os.ReadFile(manifestPath)
	hadManifest := manifestErr == nil
	if manifestErr != nil && !errors.Is(manifestErr, fs.ErrNotExist) {
		return PublicationCommit{}, manifestErr
	}
	if hadManifest && !bytes.Equal(manifestExisting, manifestData) {
		return PublicationCommit{}, fmt.Errorf("content-addressed manifest ref already contains different bytes")
	}
	artifactPaths := make(map[string]bool, len(tx.Artifacts))
	for ref, data := range tx.Artifacts {
		path, err := artifactPath(s.baseDir, ref)
		if err != nil {
			return PublicationCommit{}, err
		}
		if ArtifactCASRef(data) != ref {
			return PublicationCommit{}, fmt.Errorf("artifact %q digest does not match bytes", ref)
		}
		existing, readErr := os.ReadFile(path)
		exists := readErr == nil
		if readErr != nil && !errors.Is(readErr, fs.ErrNotExist) {
			return PublicationCommit{}, readErr
		}
		if exists && !bytes.Equal(existing, data) {
			return PublicationCommit{}, fmt.Errorf("content-addressed artifact %q already contains different bytes", ref)
		}
		artifactPaths[path] = exists
	}
	for _, ref := range []string{tx.Manifest.ArtifactRefs.SemanticMap, tx.Manifest.ArtifactRefs.SemanticDelta, tx.Manifest.ArtifactRefs.EvidenceIndex, tx.Manifest.ArtifactRefs.Projection, tx.Manifest.ArtifactRefs.LiveView} {
		if ref == "" {
			continue
		}
		if _, ok := tx.Artifacts[ref]; !ok {
			return PublicationCommit{}, fmt.Errorf("publication artifact %q bytes are missing", ref)
		}
	}
	mapData, ok := tx.Artifacts[tx.Manifest.ArtifactRefs.SemanticMap]
	if !ok {
		return PublicationCommit{}, fmt.Errorf("publication semantic-map artifact bytes are missing")
	}
	if err := validateSemanticMapArtifactIdentity(mapData, tx.Manifest); err != nil {
		return PublicationCommit{}, err
	}
	if deltaRef := tx.Manifest.ArtifactRefs.SemanticDelta; deltaRef != "" {
		deltaData, ok := tx.Artifacts[deltaRef]
		if !ok {
			return PublicationCommit{}, fmt.Errorf("publication semantic-delta artifact bytes are missing")
		}
		if err := validateSemanticDeltaArtifactIdentity(deltaData, tx.Manifest); err != nil {
			return PublicationCommit{}, err
		}
	}
	var mapScope struct {
		Basis struct {
			RepositoryID string `json:"repositoryId"`
			WorktreeID   string `json:"worktreeId"`
		} `json:"basis"`
		Task struct {
			TaskID string `json:"taskId"`
		} `json:"task"`
	}
	if err := json.Unmarshal(mapData, &mapScope); err != nil {
		return PublicationCommit{}, fmt.Errorf("decode semantic-map scope: %w", err)
	}
	if mapScope.Basis.RepositoryID == "" || mapScope.Basis.WorktreeID == "" || mapScope.Task.TaskID == "" || mapScope.Basis.RepositoryID != tx.Pointer.RepositoryID || mapScope.Basis.WorktreeID != tx.Pointer.WorktreeID || mapScope.Task.TaskID != tx.Pointer.TaskID {
		return PublicationCommit{}, fmt.Errorf("publication pointer scope does not match semantic-map scope")
	}
	j := publicationJournal{ManifestPath: manifestPath, PointerPath: oldPointerPath, EventPath: eventPath, ArtifactPaths: artifactPaths, HadManifest: hadManifest, OldPointer: oldPointer, HadPointer: hadPointer, HadEvent: eventMetadata.Exists, PriorEventLength: eventMetadata.Length, PriorEventHighWater: eventMetadata.HighWater}
	journalData, err := json.MarshalIndent(j, "", "  ")
	if err != nil {
		return PublicationCommit{}, fmt.Errorf("marshal publication journal: %w", err)
	}
	if err := durableWrite(journalPath, journalData, 0o600); err != nil {
		return PublicationCommit{}, fmt.Errorf("write publication journal: %w", err)
	}
	rollback := func(cause error) (PublicationCommit, error) {
		_ = s.recoverPublication()
		return PublicationCommit{}, cause
	}
	if err := s.publicationFault("manifest"); err != nil {
		return rollback(err)
	}
	if !hadManifest {
		if err := durableWrite(manifestPath, manifestData, 0o600); err != nil {
			return rollback(fmt.Errorf("write publication manifest: %w", err))
		}
	}
	if err := s.publicationFault("artifact"); err != nil {
		return rollback(err)
	}
	for ref, data := range tx.Artifacts {
		path, _ := artifactPath(s.baseDir, ref)
		if artifactPaths[path] {
			continue
		}
		if err := durableWrite(path, data, 0o600); err != nil {
			return rollback(fmt.Errorf("write publication artifact: %w", err))
		}
	}
	if err := s.publicationFault("event"); err != nil {
		return rollback(err)
	}
	if err := appendEventAndSync(eventPath, tx.Event); err != nil {
		return rollback(fmt.Errorf("write publication event: %w", err))
	}
	if err := s.publicationFault("pointer"); err != nil {
		return rollback(err)
	}
	if err := durableWrite(oldPointerPath, pointerData, 0o600); err != nil {
		return rollback(fmt.Errorf("write active pointer: %w", err))
	}
	if err := s.publicationFault("fsync"); err != nil {
		return rollback(err)
	}
	if err := durableWrite(markerPath, []byte("committed\n"), 0o600); err != nil {
		return rollback(fmt.Errorf("commit publication: %w", err))
	}
	_ = os.Remove(journalPath)
	_ = os.Remove(markerPath)
	return PublicationCommit{ManifestRef: manifestRef, Pointer: tx.Pointer, Event: append([]byte(nil), tx.Event...)}, nil
}

func sha256Hex(data []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

func artifactPath(baseDir, ref string) (string, error) {
	const prefix = "cas:sha256:"
	digest, ok := strings.CutPrefix(ref, prefix)
	if !ok || len(digest) != sha256.Size*2 {
		return "", fmt.Errorf("invalid artifact cas ref %q", ref)
	}
	if _, err := hex.DecodeString(digest); err != nil {
		return "", fmt.Errorf("invalid artifact cas ref %q", ref)
	}
	return filepath.Join(baseDir, "cas", digest+".artifact"), nil
}
