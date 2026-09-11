package flowview

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"codeflow/internal/watch"
	"codeflow/internal/workspace"
)

func (s *Server) runWorkspaceWatcher(ctx context.Context) {
	err := watch.WatchChanges(ctx, s.repoRoot, s.watchInterval, func(change watch.ChangeSet) {
		if err := s.acceptWatcherChange(ctx, change); err != nil && ctx.Err() == nil {
			s.recordPipelineError(fmt.Errorf("watcher fallback: %w", err))
		}
	})
	if err != nil && ctx.Err() == nil {
		s.recordPipelineError(fmt.Errorf("watcher fallback stopped: %w", err))
	}
}

func (s *Server) acceptWatcherChange(ctx context.Context, signal watch.ChangeSet) error {
	if signal.Reconcile {
		targets := make([]workspace.ReconciliationTarget, 0, len(signal.Changed))
		for _, path := range signal.Changed {
			targets = append(targets, workspace.ReconciliationTarget{Path: path, Kind: "event_loss", Reason: signal.Reason, Timestamp: time.Now().UTC()})
		}
		snapshot, err := s.engine.Reconcile(ctx, targets)
		if err != nil {
			return err
		}
		return s.notifyAcceptedSnapshot(snapshot)
	}

	head := s.engine.LiveHead()
	relChanged := s.engine.FilterUncapturedPaths(signal.Changed)
	if len(relChanged) == 0 {
		return nil
	}
	changes := make([]workspace.VersionedChange, 0, len(relChanged))
	deleted := make([]string, 0)
	for _, relativePath := range relChanged {
		capture := watch.CaptureFileWithStatCheck(filepath.Join(s.repoRoot, filepath.FromSlash(relativePath)), 3)
		if capture.Error != nil || capture.Conflict {
			snapshot, err := s.engine.Reconcile(ctx, []workspace.ReconciliationTarget{{Path: relativePath, Kind: "event_loss", Reason: "unstable watcher capture", Timestamp: time.Now().UTC()}})
			if err != nil {
				return err
			}
			return s.notifyAcceptedSnapshot(snapshot)
		}
		if capture.Deleted {
			deleted = append(deleted, relativePath)
			continue
		}
		changes = append(changes, workspace.VersionedChange{Kind: workspace.ChangeUpsert, Path: relativePath, Content: capture.Content, ContentID: capture.ContentID})
	}

	// A delete and create with the same immutable bytes is a measured rename.
	// Pair only unique matches; ambiguous pairs remain delete/create and are
	// resolved by the workspace delta after the batch is accepted.
	usedDeleted := make(map[string]bool)
	if head != nil {
		for index := range changes {
			match := ""
			for _, oldPath := range deleted {
				if usedDeleted[oldPath] {
					continue
				}
				if entry, ok := head.Entries[oldPath]; ok && entry.ContentID == changes[index].ContentID {
					if match != "" {
						match = ""
						break
					}
					match = oldPath
				}
			}
			if match != "" {
				changes[index].Kind = workspace.ChangeRename
				changes[index].OldPath = match
				usedDeleted[match] = true
			}
		}
	}
	for _, path := range deleted {
		if !usedDeleted[path] {
			changes = append(changes, workspace.VersionedChange{Kind: workspace.ChangeDelete, Path: path})
		}
	}
	if len(changes) == 0 {
		return nil
	}
	batchID := fmt.Sprintf("watcher-%d", time.Now().UTC().UnixNano())
	_, err := s.SubmitVersionedChanges(ctx, workspace.VersionedChangeRequest{BatchID: batchID, Source: workspace.SourceWatcherFallback, Changes: changes})
	return err
}
