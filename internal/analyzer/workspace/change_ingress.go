package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"time"
)

// ApplyVersionedChanges is the shared, batch-preserving workspace ingress.
func (e *SnapshotEngine) ApplyVersionedChanges(ctx context.Context, request VersionedChangeRequest) (*VersionedChangeResult, error) {
	if err := contextErr(ctx); err != nil {
		return nil, err
	}
	request.BatchID = strings.TrimSpace(request.BatchID)
	if request.BatchID == "" || len(request.BatchID) > 128 {
		return nil, fmt.Errorf("batchId must contain 1 to 128 characters")
	}
	if !isAcceptedEditSource(request.Source) {
		return nil, fmt.Errorf("unsupported change source %q", request.Source)
	}
	if len(request.Changes) == 0 {
		return nil, fmt.Errorf("versioned change batch must not be empty")
	}

	unlockRoot := e.lockRootCommit()
	defer unlockRoot()
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.ensureCachedLiveHeadCurrentLocked(); err != nil {
		return nil, err
	}
	if existing, ok := e.batches[request.BatchID]; ok {
		if existing.Source != request.Source {
			return nil, fmt.Errorf("batchId %q already belongs to source %q", request.BatchID, existing.Source)
		}
		return e.duplicateChangeResultLocked(existing, request.Changes), nil
	}

	normalized := make([]VersionedChange, 0, len(request.Changes))
	for index, change := range request.Changes {
		change.Kind = ChangeKind(strings.TrimSpace(string(change.Kind)))
		if change.Kind != ChangeCreate && change.Kind != ChangeUpsert && change.Kind != ChangeDelete && change.Kind != ChangeRename {
			return nil, fmt.Errorf("change %d has unsupported kind %q", index, change.Kind)
		}
		path, err := validateRepositoryPath(e.canonicalRoot, change.Path, true)
		if err != nil {
			return nil, fmt.Errorf("change %d path: %w", index, err)
		}
		change.Path = path
		if change.Kind == ChangeRename {
			oldPath, oldErr := validateRepositoryPath(e.canonicalRoot, change.OldPath, true)
			if oldErr != nil {
				return nil, fmt.Errorf("change %d oldPath: %w", index, oldErr)
			}
			if oldPath == path {
				return nil, fmt.Errorf("change %d rename paths must differ", index)
			}
			change.OldPath = oldPath
		}
		if change.Kind == ChangeCreate || change.Kind == ChangeUpsert || change.Kind == ChangeRename {
			if len(change.Content) == 0 && change.Kind == ChangeRename {
				if prior, ok := e.liveHeadEntries()[change.OldPath]; ok {
					if content, cached := e.casCache[prior.ContentID]; cached {
						change.Content = append([]byte(nil), content...)
					} else {
						content, readErr := os.ReadFile(e.ContentPath(prior.ContentID))
						if readErr != nil {
							return nil, fmt.Errorf("read rename source %q: %w", change.OldPath, readErr)
						}
						change.Content = content
					}
				}
			}
			contentHash := sha256.Sum256(change.Content)
			contentID := hex.EncodeToString(contentHash[:])
			if change.ContentID != "" && change.ContentID != contentID {
				return nil, fmt.Errorf("change %d contentId does not match content", index)
			}
			change.ContentID = contentID
		}
		normalized = append(normalized, change)
	}

	if e.changesAlreadyAppliedLocked(normalized) {
		return e.duplicateChangeResultLocked(nil, normalized), nil
	}

	edits := make([]EditRequest, 0, len(normalized))
	removals := make(map[string]bool)
	affectedPriorRevisions := make([]string, 0, len(normalized))
	for index := range normalized {
		change := &normalized[index]
		switch change.Kind {
		case ChangeDelete:
			if prior, ok := e.liveHeadEntries()[change.Path]; ok {
				affectedPriorRevisions = append(affectedPriorRevisions, prior.RevisionID)
			}
			removals[change.Path] = true
		case ChangeRename:
			if prior, ok := e.liveHeadEntries()[change.OldPath]; ok {
				affectedPriorRevisions = append(affectedPriorRevisions, prior.RevisionID)
			}
			removals[change.OldPath] = true
			fallthrough
		case ChangeCreate, ChangeUpsert:
			version := change.DocumentVersion
			if version == 0 && request.Source == SourceWatcherFallback {
				version = e.fileVersions[change.Path] + 1
			}
			if version < 1 {
				return nil, &InvalidDocumentVersion{Path: change.Path, Version: version}
			}
			if current := e.fileVersions[change.Path]; version <= current {
				return nil, &DocumentVersionConflict{Path: change.Path, IncomingVersion: version, CurrentVersion: current}
			}
			change.DocumentVersion = version
			edits = append(edits, EditRequest{Path: change.Path, Content: append([]byte(nil), change.Content...), DocumentVersion: version, Source: request.Source})
		}
	}

	build, err := e.buildSnapshotWithRemovalsLocked(ctx, edits, removals)
	if err != nil {
		return nil, err
	}
	revisionIDs := append([]string(nil), affectedPriorRevisions...)
	acceptedRevisions := make([]*DocumentRevision, 0, len(edits))
	for _, edit := range edits {
		for _, revision := range build.revisions {
			if revision.Path == edit.Path && revision.DocumentVersion == edit.DocumentVersion && revision.Source == request.Source {
				revisionIDs = append(revisionIDs, revision.RevisionID)
				acceptedRevisions = append(acceptedRevisions, cloneRevision(revision))
				break
			}
		}
	}
	if len(revisionIDs) == 0 {
		return nil, fmt.Errorf("change batch %q did not resolve an affected revision", request.BatchID)
	}
	now := time.Now().UTC()
	batch := &ChangeBatch{
		SchemaID:       changeBatchSchemaID,
		SchemaVersion:  2,
		BatchID:        request.BatchID,
		Source:         request.Source,
		WorkspaceEpoch: e.currentEpoch,
		Revisions:      revisionIDs,
		Status:         "committed",
		CreatedAt:      now,
		CommittedAt:    &now,
	}
	snapshot, err := e.publishSnapshotLocked(build, now, batch)
	if err != nil {
		return nil, err
	}
	return &VersionedChangeResult{Batch: cloneBatch(batch), Revisions: acceptedRevisions, Snapshot: snapshot}, nil
}

func (e *SnapshotEngine) changesAlreadyAppliedLocked(changes []VersionedChange) bool {
	entries := e.liveHeadEntries()
	for _, change := range changes {
		switch change.Kind {
		case ChangeCreate, ChangeUpsert:
			entry, ok := entries[change.Path]
			if !ok || entry.ContentID != change.ContentID {
				return false
			}
		case ChangeDelete:
			if _, ok := entries[change.Path]; ok {
				return false
			}
		case ChangeRename:
			if _, oldExists := entries[change.OldPath]; oldExists {
				return false
			}
			entry, newExists := entries[change.Path]
			if !newExists || entry.ContentID != change.ContentID {
				return false
			}
		}
	}
	return true
}

func (e *SnapshotEngine) duplicateChangeResultLocked(batch *ChangeBatch, changes []VersionedChange) *VersionedChangeResult {
	result := &VersionedChangeResult{Batch: cloneBatch(batch), Snapshot: cloneSnapshotForQuery(e.liveHead, e.liveHeadID), Duplicate: true}
	if result.Snapshot == nil {
		return result
	}
	seen := make(map[string]bool)
	for _, change := range changes {
		path := change.Path
		entry, ok := result.Snapshot.Entries[path]
		if !ok {
			continue
		}
		if seen[entry.RevisionID] {
			continue
		}
		if revision, ok := e.revisions[entry.RevisionID]; ok {
			result.Revisions = append(result.Revisions, cloneRevision(revision))
			seen[entry.RevisionID] = true
		}
	}
	return result
}
