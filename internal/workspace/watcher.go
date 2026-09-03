package workspace

import (
	"context"
	"time"
)

// ApplyWatcherCapture ingests bytes verified by stat-before/read/stat-after into the workspace,
// assigning the next monotonic version for the path atomically under write lock.
func (e *SnapshotEngine) ApplyWatcherCapture(ctx context.Context, path string, content []byte, modTime time.Time) (*DocumentRevision, *WorkspaceSnapshot, error) {
	return e.ApplyVersionedEdit(ctx, EditRequest{
		Path:            path,
		Content:         content,
		DocumentVersion: 0, // Atomically auto-increment monotonically under write lock
		Source:          SourceWatcherFallback,
	})
}

// MarkReconciliation records targets needing reconciliation (such as deletes, renames, or event losses)
// and updates the workspace activity state to "reconciling".
func (e *SnapshotEngine) MarkReconciliation(targets []ReconciliationTarget) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.activity = "reconciling"
	paths := make([]string, len(targets))
	for i, t := range targets {
		paths[i] = t.Path
	}
	e.activeScope = paths
}
