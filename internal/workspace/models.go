package workspace

import "time"

// Source types for DocumentRevision and ChangeBatch
const (
	SourceAgentTransaction  = "agent_transaction"
	SourceIDEVersioned      = "ide_versioned"
	SourceWatcherFallback   = "watcher_fallback"
	SourceRepositoryCapture = "repository_capture"
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
	WorkspaceEpoch  int64     `json:"workspaceEpoch"`
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

// SnapshotDocument is the immutable document inventory exposed by a snapshot
// lease. It contains identity metadata only. Callers must use the lease's
// ReadFile method for bytes so the lease remains the sole source of content.
type SnapshotDocument struct {
	Path            string `json:"path"`
	RevisionID      string `json:"documentRevisionId"`
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
	WorkspaceEpoch           int64                    `json:"workspaceEpoch"`
	Sequence                 int                      `json:"sequence"`
	LiveHead                 bool                     `json:"liveHead"`
	ComputedBasisID          string                   `json:"computedBasisId"`
	RootTreeID               string                   `json:"rootTreeId"`
	CreatedAt                time.Time                `json:"createdAt"`
	ConfigurationFingerprint string                   `json:"configurationFingerprint"`
	DependencyFingerprint    string                   `json:"dependencyFingerprint,omitempty"`
	RepositoryID             string                   `json:"repositoryId,omitempty"`
	WorktreeID               string                   `json:"worktreeId,omitempty"`
	Entries                  map[string]SnapshotEntry `json:"entries"`
	ChangedEntries           []SnapshotChange         `json:"changedEntries"`
	RepositoryPathWriteAudit SourceWriteAudit         `json:"repositoryPathWriteAudit"`
}

// SnapshotChange identifies the revisions introduced by one snapshot.
type SnapshotChange struct {
	Path               string `json:"path"`
	DocumentRevisionID string `json:"documentRevisionId"`
}

// ChangeBatch represents an explicit multi-file edit transaction or coalesced edit batch (Raw §7.5).
type ChangeBatch struct {
	SchemaID       string     `json:"schemaId"`
	SchemaVersion  int        `json:"schemaVersion"`
	BatchID        string     `json:"batchId"`
	TransactionID  *string    `json:"transactionId,omitempty"`
	Source         string     `json:"source"`
	WorkspaceEpoch int64      `json:"workspaceEpoch"`
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
	SchemaID              string    `json:"schemaId,omitempty"`
	SchemaVersion         int       `json:"schemaVersion,omitempty"`
	Activity              string    `json:"activity"` // "idle", "editing", "analyzing", "reconciling"
	AnalysisLagMs         int64     `json:"analysisLagMs"`
	PendingRevisions      int       `json:"pendingRevisions"`
	CurrentSnapshotID     string    `json:"currentSnapshotId"`
	WorkspaceEpoch        int64     `json:"workspaceEpoch"`
	Timestamp             time.Time `json:"timestamp"`
	Scope                 []string  `json:"scope,omitempty"`
	TraceID               string    `json:"traceId,omitempty"`
	CapturedAt            time.Time `json:"capturedAt,omitempty"`
	AcknowledgedAt        time.Time `json:"acknowledgedAt,omitempty"`
	AnalysisStartedAt     time.Time `json:"analysisStartedAt,omitempty"`
	CurrentOrGapAt        time.Time `json:"currentOrGapAt,omitempty"`
	ActivityLatencyMs     int64     `json:"activityLatencyMs,omitempty"`
	CurrentOrGapLatencyMs int64     `json:"currentOrGapLatencyMs,omitempty"`
	MetricsMeasured       bool      `json:"metricsMeasured"`
}

// ReconciliationTarget records an event loss, rename, or deletion requiring downstream reconciliation (VS03-A4).
type ReconciliationTarget struct {
	Path      string    `json:"path"`
	OldPath   string    `json:"oldPath,omitempty"`
	Kind      string    `json:"kind"` // "delete", "rename", "event_loss"
	Reason    string    `json:"reason"`
	Timestamp time.Time `json:"timestamp"`
}

// SourceWriteAudit records source-path writes attributed to CodeFlow. Source
// capture itself only reads repository paths. The audit is persisted with each
// snapshot so callers can verify that source integrity was preserved.
type SourceWriteAudit struct {
	CodeFlowWriteCount         int      `json:"codeflowWriteCount"`
	RepositoryPathWrites       []string `json:"repositoryPathWrites,omitempty"`
	SourceIntegrityViolation   bool     `json:"sourceIntegrityViolation"`
	CapturedSnapshotTreeDigest string   `json:"capturedSnapshotTreeDigest"`
}

// WorkspaceIdentity describes the repository/worktree/configuration lineage
// used to decide whether a new epoch is required.
type WorkspaceIdentity struct {
	RepositoryID             string `json:"repositoryId"`
	WorktreeID               string `json:"worktreeId"`
	Branch                   string `json:"branch,omitempty"`
	ConfigurationFingerprint string `json:"configurationFingerprint"`
}

// EpochChange is published after an epoch transition has been durably
// recorded and the prior live head has been detached from current state.
type EpochChange struct {
	PreviousEpoch         int64     `json:"previousEpoch"`
	NewEpoch              int64     `json:"newEpoch"`
	Reason                string    `json:"reason"`
	PreviousSnapshotID    string    `json:"previousSnapshotId,omitempty"`
	OccurredAt            time.Time `json:"occurredAt"`
	HistoricalSnapshotIDs []string  `json:"historicalSnapshotIds,omitempty"`
}

// WatcherObservation carries the stat/read/stat evidence needed to publish a
// watcher capture without mixing bytes from different file states.
type WatcherObservation struct {
	Path          string    `json:"path"`
	Content       []byte    `json:"content"`
	ContentID     string    `json:"contentId"`
	BeforeSize    int64     `json:"beforeSize"`
	AfterSize     int64     `json:"afterSize"`
	BeforeModTime time.Time `json:"beforeModTime"`
	AfterModTime  time.Time `json:"afterModTime"`
}
