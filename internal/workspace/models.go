package workspace

import "time"

// Source types for DocumentRevision and ChangeBatch
const (
	SourceAgentTransaction = "agent_transaction"
	SourceIDEVersioned     = "ide_versioned"
	SourceWatcherFallback  = "watcher_fallback"
)

// DocumentRevision represents an immutable single-document revision (Raw §7.1).
type DocumentRevision struct {
	SchemaID        string    `json:"schemaId"`
	SchemaVersion   int       `json:"schemaVersion"`
	RevisionID      string    `json:"revisionId"`
	Path            string    `json:"path"`
	DocumentVersion int       `json:"documentVersion"`
	ContentID       string    `json:"contentId"`
	Source          string    `json:"source"`
	WorkspaceEpoch  string    `json:"workspaceEpoch"`
	CreatedAt       time.Time `json:"createdAt"`
	Content         string    `json:"content,omitempty"`
	ByteLength      int       `json:"byteLength"`
}

// SnapshotEntry is a file pointer inside a WorkspaceSnapshot.
type SnapshotEntry struct {
	RevisionID      string `json:"revisionId"`
	ContentID       string `json:"contentId"`
	DocumentVersion int    `json:"documentVersion"`
	ByteLength      int    `json:"byteLength"`
}

// WorkspaceSnapshot represents an immutable whole-workspace snapshot state (Raw §7.2).
type WorkspaceSnapshot struct {
	SchemaID                 string                   `json:"schemaId"`
	SchemaVersion            int                      `json:"schemaVersion"`
	SnapshotID               string                   `json:"snapshotId"`
	ParentSnapshotID         *string                  `json:"parentSnapshotId"`
	WorkspaceEpoch           string                   `json:"workspaceEpoch"`
	Sequence                 int                      `json:"sequence"`
	LiveHead                 bool                     `json:"liveHead"`
	ComputedBasisID          string                   `json:"computedBasisId"`
	CreatedAt                time.Time                `json:"createdAt"`
	ConfigurationFingerprint string                   `json:"configurationFingerprint"`
	Entries                  map[string]SnapshotEntry `json:"entries"`
}

// ChangeBatch represents an explicit multi-file edit transaction or coalesced edit batch (Raw §7.5).
type ChangeBatch struct {
	SchemaID       string     `json:"schemaId"`
	SchemaVersion  int        `json:"schemaVersion"`
	BatchID        string     `json:"batchId"`
	TransactionID  *string    `json:"transactionId,omitempty"`
	Source         string     `json:"source"`
	WorkspaceEpoch string     `json:"workspaceEpoch"`
	Revisions      []string   `json:"revisions"`
	Status         string     `json:"status"` // "open", "committed", "aborted"
	CreatedAt      time.Time  `json:"createdAt"`
	CommittedAt    *time.Time `json:"committedAt,omitempty"`
}

// EditRequest defines an incoming versioned edit from agent, IDE, or watcher.
type EditRequest struct {
	Path            string
	Content         []byte
	DocumentVersion int
	Source          string
}

// ActivityStatus represents the activity state of the workspace.
type ActivityStatus struct {
	Activity          string    `json:"activity"` // "idle", "editing", "analyzing", "reconciling"
	AnalysisLagMs     int64     `json:"analysisLagMs"`
	PendingRevisions  int       `json:"pendingRevisions"`
	CurrentSnapshotID string    `json:"currentSnapshotId"`
	WorkspaceEpoch    string    `json:"workspaceEpoch"`
	Timestamp         time.Time `json:"timestamp"`
	Scope             []string  `json:"scope,omitempty"`
}

// ReconciliationTarget records an event loss, rename, or deletion requiring downstream reconciliation (VS03-A4).
type ReconciliationTarget struct {
	Path      string    `json:"path"`
	OldPath   string    `json:"oldPath,omitempty"`
	Kind      string    `json:"kind"` // "delete", "rename", "event_loss"
	Reason    string    `json:"reason"`
	Timestamp time.Time `json:"timestamp"`
}

