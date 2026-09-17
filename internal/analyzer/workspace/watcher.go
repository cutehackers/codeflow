package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// ApplyWatcherCapture ingests bytes verified by stat-before/read/stat-after into the workspace,
// assigning the next monotonic version for the path atomically under write lock.
func (e *SnapshotEngine) ApplyWatcherCapture(ctx context.Context, path string, content []byte, modTime time.Time) (*DocumentRevision, *WorkspaceSnapshot, error) {
	if modTime.IsZero() {
		return nil, nil, fmt.Errorf("watcher capture requires stat evidence")
	}
	return e.ApplyVersionedEdit(ctx, EditRequest{
		Path:            path,
		Content:         content,
		DocumentVersion: 0, // Atomically auto-increment monotonically under write lock
		Source:          SourceWatcherFallback,
	})
}

// ApplyWatcherObservation accepts a stat-read-stat observation only when the
// metadata and content hash are stable. An unstable observation is surfaced as
// reconciling and never becomes a published revision.
func (e *SnapshotEngine) ApplyWatcherObservation(ctx context.Context, observation WatcherObservation) (*DocumentRevision, *WorkspaceSnapshot, error) {
	if observation.Path == "" {
		return nil, nil, fmt.Errorf("watcher observation path must not be empty")
	}
	if observation.BeforeSize != observation.AfterSize || !observation.BeforeModTime.Equal(observation.AfterModTime) {
		e.MarkReconciliation([]ReconciliationTarget{{Path: observation.Path, Kind: "event_loss", Reason: "unstable stat-read-stat observation", Timestamp: time.Now().UTC()}})
		return nil, nil, fmt.Errorf("reconciling: watcher observation changed during read")
	}
	hash := sha256.Sum256(observation.Content)
	contentID := hex.EncodeToString(hash[:])
	if observation.ContentID == "" {
		observation.ContentID = contentID
	}
	if observation.ContentID != contentID {
		e.MarkReconciliation([]ReconciliationTarget{{Path: observation.Path, Kind: "event_loss", Reason: "content hash changed during capture", Timestamp: time.Now().UTC()}})
		return nil, nil, fmt.Errorf("reconciling: watcher content hash mismatch")
	}
	return e.ApplyWatcherCapture(ctx, observation.Path, observation.Content, observation.AfterModTime)
}

// Reconcile performs one bounded whole-tree recapture for validated watcher
// targets. The prior live head remains active until the complete recapture and
// its immutable snapshot are durably published. Delete and rename targets are
// recrawl hints: existing safe files are captured from disk, while missing
// prior entries and legitimate unsaved IDE or agent overlays are removed only
// when a target explicitly names them.
func (e *SnapshotEngine) Reconcile(ctx context.Context, targets []ReconciliationTarget) (*WorkspaceSnapshot, error) {
	if err := contextErr(ctx); err != nil {
		return nil, err
	}
	normalized, removals, err := e.validateReconciliationTargets(targets)
	if err != nil {
		return nil, err
	}

	unlockRoot := e.lockRootCommit()
	defer unlockRoot()
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.ensureCachedLiveHeadCurrentLocked(); err != nil {
		return nil, err
	}
	priorActivity := e.activity
	priorScope := append([]string(nil), e.activeScope...)
	e.activity = "reconciling"
	e.activeScope = reconciliationScope(normalized)
	build, err := e.buildSnapshotWithRemovalsLocked(ctx, nil, removals)
	if err != nil {
		// captureCompleteTreeWithRemovalsLocked records the bounded conflict as
		// reconciling and leaves the prior head/indexes untouched.
		return nil, err
	}
	snap, err := e.publishSnapshotLocked(build, time.Now().UTC(), nil)
	if err != nil {
		// Publication failures are atomic too. Restore the activity view that
		// belonged to the still-current head.
		e.activity = priorActivity
		e.activeScope = priorScope
		return nil, err
	}
	return cloneSnapshotForQuery(snap, e.liveHeadID), nil
}

// ReconcileWorkspace is an explicit alias for callers that prefer the
// workspace-qualified seam name.
func (e *SnapshotEngine) ReconcileWorkspace(ctx context.Context, targets []ReconciliationTarget) (*WorkspaceSnapshot, error) {
	return e.Reconcile(ctx, targets)
}

// ReconcileIfChanged performs the same bounded whole-tree capture as
// Reconcile, but keeps the existing immutable head when an unscoped capture
// observes the identical tree. This is the request-boundary operation for
// current-proof consumers: direct worktree edits are incorporated, while a
// read does not manufacture a new snapshot identity and invalidate a valid
// proof merely by checking it.
func (e *SnapshotEngine) ReconcileIfChanged(ctx context.Context, targets []ReconciliationTarget) (*WorkspaceSnapshot, error) {
	if err := contextErr(ctx); err != nil {
		return nil, err
	}
	normalized, removals, err := e.validateReconciliationTargets(targets)
	if err != nil {
		return nil, err
	}

	unlockRoot := e.lockRootCommit()
	defer unlockRoot()
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.ensureCachedLiveHeadCurrentLocked(); err != nil {
		return nil, err
	}
	priorActivity := e.activity
	priorScope := append([]string(nil), e.activeScope...)
	e.activity = "reconciling"
	e.activeScope = reconciliationScope(normalized)
	build, err := e.buildSnapshotWithRemovalsLocked(ctx, nil, removals)
	if err != nil {
		return nil, err
	}
	if e.liveHead != nil && len(normalized) == 0 && len(removals) == 0 && computeRootTreeID(build.entries) == e.liveHead.RootTreeID {
		// The capture was only a measurement. Preserve the prior activity state
		// and head because no new source bytes or reconciliation target exists.
		e.activity = priorActivity
		e.activeScope = priorScope
		return cloneSnapshotForQuery(e.liveHead, e.liveHeadID), nil
	}
	snap, err := e.publishSnapshotLocked(build, time.Now().UTC(), nil)
	if err != nil {
		e.activity = priorActivity
		e.activeScope = priorScope
		return nil, err
	}
	return cloneSnapshotForQuery(snap, e.liveHeadID), nil
}

func (e *SnapshotEngine) validateReconciliationTargets(targets []ReconciliationTarget) ([]ReconciliationTarget, map[string]bool, error) {
	normalized := make([]ReconciliationTarget, 0, len(targets))
	removals := make(map[string]bool)
	for index, target := range targets {
		target.Kind = strings.TrimSpace(target.Kind)
		if target.Kind != "delete" && target.Kind != "rename" && target.Kind != "event_loss" {
			return nil, nil, &ReconciliationTargetError{Index: index, Reason: fmt.Sprintf("unsupported kind %q", target.Kind)}
		}
		if target.Kind == "rename" {
			oldPath, err := e.validateReconciliationPath(target.OldPath)
			if err != nil {
				return nil, nil, &ReconciliationTargetError{Index: index, Reason: "oldPath: " + err.Error()}
			}
			newPath, err := e.validateReconciliationPath(target.Path)
			if err != nil {
				return nil, nil, &ReconciliationTargetError{Index: index, Reason: "path: " + err.Error()}
			}
			if oldPath == newPath {
				return nil, nil, &ReconciliationTargetError{Index: index, Reason: "rename oldPath and path must differ"}
			}
			target.OldPath = oldPath
			target.Path = newPath
			removals[oldPath] = true
		} else if target.Path != "" {
			path, err := e.validateReconciliationPath(target.Path)
			if err != nil {
				return nil, nil, &ReconciliationTargetError{Index: index, Reason: "path: " + err.Error()}
			}
			target.Path = path
			if target.Kind == "delete" {
				removals[path] = true
			}
		} else if target.Kind == "delete" {
			return nil, nil, &ReconciliationTargetError{Index: index, Reason: "delete requires path"}
		}
		normalized = append(normalized, target)
	}
	return normalized, removals, nil
}

func (e *SnapshotEngine) validateReconciliationPath(path string) (string, error) {
	if path == "" {
		return "", &PathPolicyError{Path: path, Reason: "path must not be empty"}
	}
	return validateRepositoryPath(e.canonicalRoot, path, true)
}

func reconciliationScope(targets []ReconciliationTarget) []string {
	seen := make(map[string]bool)
	scope := make([]string, 0, len(targets))
	for _, target := range targets {
		for _, path := range []string{target.Path, target.OldPath} {
			if path != "" && !seen[path] {
				seen[path] = true
				scope = append(scope, path)
			}
		}
	}
	return scope
}

// MarkReconciliation records targets needing reconciliation (such as deletes, renames, or event losses)
// and updates the workspace activity state to "reconciling".
func (e *SnapshotEngine) MarkReconciliation(targets []ReconciliationTarget) {
	unlockRoot := e.lockRootCommit()
	defer unlockRoot()
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.ensureCachedLiveHeadCurrentLocked() != nil {
		return
	}

	e.activity = "reconciling"
	paths := make([]string, len(targets))
	for i, t := range targets {
		paths[i] = t.Path
	}
	e.activeScope = paths
	_ = e.persistState()
}
