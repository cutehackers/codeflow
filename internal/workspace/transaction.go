package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
	"time"
)

// Transaction coordinates staging multiple versioned edits before committing
// them into a single WorkspaceSnapshot sequence (VS03-A3, Raw §7.5).
type Transaction struct {
	ID     string
	Source string
	mu     sync.Mutex
	staged []EditRequest
	engine *SnapshotEngine
	closed bool
}

// BeginTransaction creates an open multi-file edit transaction.
func (e *SnapshotEngine) BeginTransaction(source string) (*Transaction, error) {
	if source == "" {
		source = SourceAgentTransaction
	}
	txID := fmt.Sprintf("tx-%d", time.Now().UnixNano())
	return &Transaction{
		ID:     txID,
		Source: source,
		engine: e,
	}, nil
}

// StageEdit stages an EditRequest within this transaction.
func (t *Transaction) StageEdit(edit EditRequest) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return fmt.Errorf("transaction %s is closed", t.ID)
	}
	if edit.Path == "" {
		return fmt.Errorf("edit path must not be empty")
	}
	if edit.DocumentVersion < 1 {
		return fmt.Errorf("documentVersion must be >= 1, got %d", edit.DocumentVersion)
	}
	if edit.Source == "" {
		edit.Source = t.Source
	}
	t.staged = append(t.staged, edit)
	return nil
}

// CommitTransaction commits all staged edits in the transaction into a single new snapshot sequence.
func (e *SnapshotEngine) CommitTransaction(ctx context.Context, tx *Transaction) (*ChangeBatch, *WorkspaceSnapshot, error) {
	tx.mu.Lock()
	if tx.closed {
		tx.mu.Unlock()
		return nil, nil, fmt.Errorf("transaction %s already closed", tx.ID)
	}
	tx.closed = true
	staged := append([]EditRequest(nil), tx.staged...)
	tx.mu.Unlock()

	if len(staged) == 0 {
		return nil, nil, fmt.Errorf("transaction %s has no staged edits", tx.ID)
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	// Verify all staged edits satisfy monotonic document versioning
	for _, edit := range staged {
		currentVer := e.fileVersions[edit.Path]
		if edit.DocumentVersion <= currentVer {
			return nil, nil, fmt.Errorf("non-monotonic documentVersion %d <= current %d for %s", edit.DocumentVersion, currentVer, edit.Path)
		}
	}

	now := time.Now().UTC()
	var revisionIDs []string

	newEntries := make(map[string]SnapshotEntry)
	if e.liveHead != nil {
		for k, v := range e.liveHead.Entries {
			newEntries[k] = v
		}
	}

	for _, edit := range staged {
		contentHash := sha256.Sum256(edit.Content)
		contentID := hex.EncodeToString(contentHash[:])
		if err := e.storeCAS(contentID, edit.Content); err != nil {
			return nil, nil, fmt.Errorf("store CAS: %w", err)
		}

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
		revisionIDs = append(revisionIDs, rev.RevisionID)

		newEntries[edit.Path] = SnapshotEntry{
			RevisionID:      rev.RevisionID,
			ContentID:       rev.ContentID,
			DocumentVersion: rev.DocumentVersion,
			ByteLength:      rev.ByteLength,
		}
	}

	e.sequence++
	snapID := fmt.Sprintf("snap-%s-%04d", e.currentEpoch, e.sequence)
	var parentID *string
	if e.liveHead != nil {
		pID := e.liveHead.SnapshotID
		parentID = &pID
		e.liveHead.LiveHead = false
	}

	computedBasis := computeBasisFingerprint(e.baseFingerprint, newEntries)

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

	batchID := fmt.Sprintf("batch-%s", tx.ID)
	committedAt := now
	txID := tx.ID
	batch := &ChangeBatch{
		SchemaID:       "https://codeflow.local/schemas/change-batch.schema.json",
		SchemaVersion:  1,
		BatchID:        batchID,
		TransactionID:  &txID,
		Source:         tx.Source,
		WorkspaceEpoch: e.currentEpoch,
		Revisions:      revisionIDs,
		Status:         "committed",
		CreatedAt:      now,
		CommittedAt:    &committedAt,
	}

	e.batches[batch.BatchID] = batch
	e.activity = "editing"
	e.lastEditTime = now
	scope := make([]string, len(staged))
	for i, ed := range staged {
		scope[i] = ed.Path
	}
	e.activeScope = scope

	return batch, snap, nil
}

// AbortTransaction aborts an in-flight transaction without modifying liveHead.
func (e *SnapshotEngine) AbortTransaction(tx *Transaction) error {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	tx.closed = true
	return nil
}
