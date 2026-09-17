package workspace

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Transaction coordinates staging multiple versioned edits before committing
// them into a single WorkspaceSnapshot.
type Transaction struct {
	ID         string
	Source     string
	mu         sync.Mutex
	staged     []EditRequest
	engine     *SnapshotEngine
	closed     bool
	committing bool
}

// BeginTransaction creates an open multi-file edit transaction.
func (e *SnapshotEngine) BeginTransaction(source string) (*Transaction, error) {
	if source == "" {
		source = SourceAgentTransaction
	}
	if !isAcceptedEditSource(source) && source != SourceAgentTransaction {
		return nil, fmt.Errorf("unsupported transaction source %q", source)
	}
	return &Transaction{
		ID:     fmt.Sprintf("tx-%d", time.Now().UTC().UnixNano()),
		Source: source,
		engine: e,
	}, nil
}

// StageEdit stages an EditRequest within this transaction. The bytes are
// copied at the boundary so later caller mutations cannot affect the commit.
func (t *Transaction) StageEdit(edit EditRequest) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed || t.committing {
		return fmt.Errorf("transaction %s is closed", t.ID)
	}
	norm, err := NormalizeRepositoryPath(edit.Path)
	if err != nil {
		return err
	}
	if edit.DocumentVersion < 1 {
		return fmt.Errorf("documentVersion must be >= 1, got %d", edit.DocumentVersion)
	}
	if edit.Source == "" {
		edit.Source = t.Source
	}
	if !isAcceptedEditSource(edit.Source) {
		return fmt.Errorf("unsupported edit source %q", edit.Source)
	}
	edit.Path = norm
	edit.Content = append([]byte(nil), edit.Content...)
	t.staged = append(t.staged, edit)
	return nil
}

// CommitTransaction commits all staged edits into one complete snapshot. The
// live head is not changed until every revision, snapshot, batch, and pointer
// has been durably written.
func (e *SnapshotEngine) CommitTransaction(ctx context.Context, tx *Transaction) (*ChangeBatch, *WorkspaceSnapshot, error) {
	if tx == nil || tx.engine != e {
		return nil, nil, fmt.Errorf("transaction does not belong to this snapshot engine")
	}
	if err := contextErr(ctx); err != nil {
		return nil, nil, err
	}
	tx.mu.Lock()
	if tx.closed || tx.committing {
		tx.mu.Unlock()
		return nil, nil, fmt.Errorf("transaction %s already closed", tx.ID)
	}
	tx.committing = true
	staged := append([]EditRequest(nil), tx.staged...)
	tx.mu.Unlock()
	if len(staged) == 0 {
		tx.mu.Lock()
		tx.committing = false
		tx.mu.Unlock()
		return nil, nil, fmt.Errorf("transaction %s has no staged edits", tx.ID)
	}
	fail := func(batch *ChangeBatch, snap *WorkspaceSnapshot, err error) (*ChangeBatch, *WorkspaceSnapshot, error) {
		tx.mu.Lock()
		tx.committing = false
		tx.mu.Unlock()
		return batch, snap, err
	}

	unlockRoot := e.lockRootCommit()
	defer unlockRoot()
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.ensureCachedLiveHeadCurrentLocked(); err != nil {
		return fail(nil, nil, err)
	}
	seen := make(map[string]int, len(staged))
	for _, edit := range staged {
		if current := e.fileVersions[edit.Path]; edit.DocumentVersion <= current {
			return fail(nil, nil, &DocumentVersionConflict{Path: edit.Path, IncomingVersion: edit.DocumentVersion, CurrentVersion: current})
		}
		if previous, ok := seen[edit.Path]; ok && edit.DocumentVersion <= previous {
			return fail(nil, nil, &DocumentVersionConflict{Path: edit.Path, IncomingVersion: edit.DocumentVersion, CurrentVersion: previous})
		}
		seen[edit.Path] = edit.DocumentVersion
	}
	build, err := e.buildSnapshotLocked(ctx, staged)
	if err != nil {
		return fail(nil, nil, err)
	}
	revisionIDs := make([]string, 0, len(staged))
	for _, edit := range staged {
		for _, rev := range build.revisions {
			if rev.Path == edit.Path && rev.DocumentVersion == edit.DocumentVersion && rev.Source == edit.Source {
				revisionIDs = append(revisionIDs, rev.RevisionID)
				break
			}
		}
	}
	if len(revisionIDs) != len(staged) {
		return fail(nil, nil, fmt.Errorf("transaction %s could not resolve all staged revisions", tx.ID))
	}
	now := time.Now().UTC()
	txID := tx.ID
	batch := &ChangeBatch{
		SchemaID:       changeBatchSchemaID,
		SchemaVersion:  2,
		BatchID:        fmt.Sprintf("batch-%s", tx.ID),
		TransactionID:  &txID,
		Source:         tx.Source,
		WorkspaceEpoch: e.currentEpoch,
		Revisions:      revisionIDs,
		Status:         "committed",
		CreatedAt:      now,
		CommittedAt:    &now,
	}
	snap, err := e.publishSnapshotLocked(build, now, batch)
	if err != nil {
		return fail(nil, nil, err)
	}
	tx.mu.Lock()
	tx.committing = false
	tx.closed = true
	tx.mu.Unlock()
	return cloneBatch(batch), snap, nil
}

// AbortTransaction aborts an in-flight transaction without modifying the
// live head.
func (e *SnapshotEngine) AbortTransaction(tx *Transaction) error {
	if tx == nil || tx.engine != e {
		return fmt.Errorf("transaction does not belong to this snapshot engine")
	}
	tx.mu.Lock()
	defer tx.mu.Unlock()
	if tx.closed {
		return nil
	}
	tx.closed = true
	return nil
}
