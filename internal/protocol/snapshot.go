package protocol

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"codeflow/internal/evidence"
	"codeflow/internal/workspace"
)

// Snapshot is the immutable analysis input passed to an adapter. A caller
// may provide contentOverlay for files that differ from the worktree. When
// an overlay is present, adapters must read it as authoritative and must not
// fall back to the live file for those paths.
type Snapshot struct {
	SnapshotID               string                       `json:"snapshotId,omitempty"`
	ComputedBasisID          string                       `json:"computedBasisId"`
	WorkspaceEpoch           int64                        `json:"workspaceEpoch"`
	RootTreeID               string                       `json:"rootTreeId,omitempty"`
	ConfigurationFingerprint string                       `json:"configurationFingerprint,omitempty"`
	DependencyFingerprint    string                       `json:"dependencyFingerprint,omitempty"`
	RepositoryID             string                       `json:"repositoryId,omitempty"`
	WorktreeID               string                       `json:"worktreeId,omitempty"`
	Documents                []workspace.SnapshotDocument `json:"documents,omitempty"`
	Files                    map[string]string            `json:"files,omitempty"`
	ContentOverlay           map[string]string            `json:"contentOverlay,omitempty"`
	SourceWriteAudit         workspace.SourceWriteAudit   `json:"repositoryPathWriteAudit,omitempty"`
}

// AnalyzerInput converts captured protocol bytes into the canonical VS-02
// analyzer envelope. It performs no filesystem access.
func (s Snapshot) AnalyzerInput() (evidence.SnapshotInput, error) {
	files := s.Files
	if len(files) == 0 {
		files = s.ContentOverlay
	}
	input, err := evidence.SnapshotInputFromContent(
		s.SnapshotID, s.ComputedBasisID, s.RootTreeID,
		s.ConfigurationFingerprint, s.DependencyFingerprint,
		s.WorkspaceEpoch, files,
	)
	if err != nil {
		return evidence.SnapshotInput{}, err
	}
	identities := make(map[string]workspace.SnapshotDocument, len(s.Documents))
	for _, document := range s.Documents {
		if _, ok := files[document.Path]; !ok {
			return evidence.SnapshotInput{}, fmt.Errorf("snapshot document %s has no captured bytes", document.Path)
		}
		identities[document.Path] = document
	}
	for i := range input.Documents {
		if identity, ok := identities[input.Documents[i].Path]; ok {
			if identity.ContentID != "" && identity.ContentID != input.Documents[i].ContentID {
				return evidence.SnapshotInput{}, fmt.Errorf("snapshot document %s content identity mismatch", identity.Path)
			}
			if identity.ByteLength != 0 && identity.ByteLength != input.Documents[i].ByteLength {
				return evidence.SnapshotInput{}, fmt.Errorf("snapshot document %s byte length mismatch", identity.Path)
			}
			input.Documents[i].RevisionID = identity.RevisionID
			input.Documents[i].ContentID = identity.ContentID
			input.Documents[i].DocumentVersion = identity.DocumentVersion
			input.Documents[i].ByteLength = identity.ByteLength
		}
	}
	input.SourceWriteAudit = s.SourceWriteAudit
	if input.SourceWriteAudit.CapturedSnapshotTreeDigest == "" {
		input.SourceWriteAudit.CapturedSnapshotTreeDigest = input.RootTreeID
	}
	return input, nil
}

// SnapshotFromLease converts one retained VS-01 lease into the protocol
// snapshot used by Core analysis. All bytes come from the lease's CAS-backed
// VFS and are copied before the adapter is invoked. The returned Snapshot has
// no repository path from which an adapter could perform a live-disk read.
func SnapshotFromLease(lease workspace.SnapshotLease) (Snapshot, error) {
	input, err := evidence.SnapshotInputFromLease(lease)
	if err != nil {
		return Snapshot{}, err
	}
	files := make(map[string]string, len(input.Documents))
	documents := make([]workspace.SnapshotDocument, 0, len(input.Documents))
	for _, doc := range input.Documents {
		files[doc.Path] = string(doc.Bytes)
		documents = append(documents, workspace.SnapshotDocument{
			Path: doc.Path, RevisionID: doc.RevisionID, ContentID: doc.ContentID,
			DocumentVersion: doc.DocumentVersion, ByteLength: doc.ByteLength,
		})
	}
	return Snapshot{
		SnapshotID:               input.SnapshotID,
		ComputedBasisID:          input.ComputedBasisID,
		WorkspaceEpoch:           input.WorkspaceEpoch,
		RootTreeID:               input.RootTreeID,
		ConfigurationFingerprint: input.ConfigurationFingerprint,
		DependencyFingerprint:    input.DependencyFingerprint,
		RepositoryID:             lease.RepositoryID(),
		WorktreeID:               lease.WorktreeID(),
		Documents:                documents,
		Files:                    files,
		ContentOverlay:           cloneFiles(files),
		SourceWriteAudit:         input.SourceWriteAudit,
	}, nil
}

// NewSnapshot constructs a snapshot from an optional overlay. The basis is
// deterministic over sorted path/content pairs unless an explicit basis is
// supplied by the workspace owner.
func NewSnapshot(workspaceEpoch int64, overlay map[string]string, basis string) (Snapshot, error) {
	if workspaceEpoch < 0 {
		return Snapshot{}, fmt.Errorf("workspace epoch must be non-negative")
	}
	clean := make(map[string]string, len(overlay))
	for rawPath, content := range overlay {
		rel := filepath.ToSlash(filepath.Clean(rawPath))
		if rel == "." || filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, "../") || strings.Contains(rel, "/../") {
			return Snapshot{}, fmt.Errorf("overlay path must be repository-relative: %q", rawPath)
		}
		clean[rel] = content
	}
	if basis == "" {
		basis = basisForOverlay(clean)
	}
	files := cloneFiles(clean)
	documents := make([]workspace.SnapshotDocument, 0, len(files))
	keys := make([]string, 0, len(files))
	for key := range files {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		contentID := sha256.Sum256([]byte(files[key]))
		documents = append(documents, workspace.SnapshotDocument{
			Path: key, RevisionID: "rev-" + hex.EncodeToString(contentID[:]),
			ContentID: hex.EncodeToString(contentID[:]), DocumentVersion: 1,
			ByteLength: len([]byte(files[key])),
		})
	}
	rootTreeID := basisForOverlay(files)
	if rootTreeID == "" {
		rootTreeID = basis
	}
	dependencyDigest := sha256.Sum256([]byte(rootTreeID + "\x00"))
	return Snapshot{
		SnapshotID:      "snapshot-" + rootTreeID,
		ComputedBasisID: basis, WorkspaceEpoch: workspaceEpoch,
		RootTreeID:            rootTreeID,
		DependencyFingerprint: hex.EncodeToString(dependencyDigest[:]),
		Documents:             documents, Files: files, ContentOverlay: clean,
		SourceWriteAudit: workspace.SourceWriteAudit{CapturedSnapshotTreeDigest: rootTreeID},
	}, nil
}

// CaptureSnapshot captures a deterministic basis and the complete textual
// document content from the current worktree. Analysis consumers must use the
// captured Files map. They must not re-read repoRoot after this call returns.
func CaptureSnapshot(repoRoot string, workspaceEpoch int64) (Snapshot, error) {
	if workspaceEpoch < 0 {
		return Snapshot{}, fmt.Errorf("workspace epoch must be non-negative")
	}
	engine, err := workspace.NewSnapshotEngine(repoRoot, workspaceEpoch)
	if err != nil {
		return Snapshot{}, err
	}
	head, err := engine.Reconcile(context.Background(), nil)
	if err != nil {
		return Snapshot{}, err
	}
	lease, err := engine.SnapshotVFS(head.SnapshotID)
	if err != nil {
		return Snapshot{}, err
	}
	defer lease.Close()
	return SnapshotFromLease(lease)
}

func basisForOverlay(overlay map[string]string) string {
	keys := make([]string, 0, len(overlay))
	for key := range overlay {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	h := sha256.New()
	for _, key := range keys {
		digest := sha256.Sum256([]byte(overlay[key]))
		fmt.Fprintf(h, "%s:%s\n", key, hex.EncodeToString(digest[:]))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Params returns a request-ready copy. The overlay map is copied so callers
// cannot mutate the snapshot while an adapter is reading it.
func (s Snapshot) Params() map[string]any {
	files := cloneFiles(s.Files)
	if len(files) == 0 {
		files = cloneFiles(s.ContentOverlay)
	}
	params := map[string]any{
		"computedBasisId": s.ComputedBasisID,
		"workspaceEpoch":  s.WorkspaceEpoch,
		"snapshot": map[string]any{
			"schemaId":                 evidence.AnalyzerRequestSchemaID,
			"schemaVersion":            evidence.SchemaVersion,
			"computedBasisId":          s.ComputedBasisID,
			"workspaceEpoch":           s.WorkspaceEpoch,
			"snapshotId":               s.SnapshotID,
			"rootTreeId":               s.RootTreeID,
			"dependencyFingerprint":    s.DependencyFingerprint,
			"repositoryPathWriteAudit": s.SourceWriteAudit,
		},
	}
	if files == nil {
		files = map[string]string{}
	}
	if len(s.Documents) > 0 {
		metadata := make([]map[string]any, 0, len(s.Documents))
		for _, document := range s.Documents {
			metadata = append(metadata, map[string]any{
				"path": document.Path, "documentRevisionId": document.RevisionID,
				"contentId": document.ContentID, "documentVersion": document.DocumentVersion,
				"contentHash": document.ContentID, "byteLength": document.ByteLength,
			})
		}
		params["snapshot"].(map[string]any)["documents"] = metadata
	}
	if _, ok := params["snapshot"].(map[string]any)["documents"]; !ok {
		params["snapshot"].(map[string]any)["documents"] = []map[string]any{}
	}
	if s.SnapshotID != "" {
		params["snapshot"].(map[string]any)["snapshotId"] = s.SnapshotID
	}
	if s.RootTreeID != "" {
		params["snapshot"].(map[string]any)["rootTreeId"] = s.RootTreeID
	}
	if s.ConfigurationFingerprint != "" {
		params["snapshot"].(map[string]any)["configurationFingerprint"] = s.ConfigurationFingerprint
	}
	if s.DependencyFingerprint != "" {
		params["snapshot"].(map[string]any)["dependencyFingerprint"] = s.DependencyFingerprint
	}
	{
		params["files"] = cloneFiles(files)
		if params["files"] == nil {
			params["files"] = map[string]string{}
		}
		params["snapshot"].(map[string]any)["files"] = cloneFiles(files)
		if params["snapshot"].(map[string]any)["files"] == nil {
			params["snapshot"].(map[string]any)["files"] = map[string]string{}
		}
		// contentOverlay remains for protocol-v1 adapters and is always a
		// defensive copy of the immutable snapshot, never a live-disk hint.
		params["contentOverlay"] = cloneFiles(files)
		params["snapshot"].(map[string]any)["contentOverlay"] = cloneFiles(files)
	}
	return params
}

func cloneFiles(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
