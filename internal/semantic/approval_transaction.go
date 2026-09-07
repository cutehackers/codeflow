package semantic

// This file owns the VS-09 approval commit boundary. The transaction is a
// normalized SQLite WAL publication, and its production authority is bound
// to the independent storage package's validated active-proof reader.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"codeflow/internal/contractharness"
	"codeflow/internal/secret"
	"codeflow/internal/storage"
	"codeflow/internal/workspace"
	_ "modernc.org/sqlite"
)

const (
	approvalTransactionStoreVersion                  = 1
	approvalTransactionMaxBytes                      = 16 << 20
	approvalTransactionMaxRecords                    = 10000
	approvalTransactionDatabaseName                  = "approval-transactions.sqlite3"
	approvalTransactionBusyTimeoutMS                 = 5000
	approvalTransactionMaxDatabaseBytes              = 256 << 20
	approvalTransactionSQLiteSchemaVersion           = 1
	approvalTransactionSQLiteWALHeaderBytes          = 32
	approvalTransactionSQLiteWALFrameBytes           = 24
	approvalTransactionSQLiteWALIndexRegionBytes     = 32 << 10
	approvalTransactionSQLiteWALFramesPerIndexRegion = 4096
	approvalTransactionSQLiteWALIndexSafetyRegions   = 2
)

var (
	ErrApprovalTransactionInvalid                   = errors.New("approval transaction invalid")
	ErrApprovalTransactionConflict                  = errors.New("approval transaction conflict")
	ErrApprovalTransactionPersistence               = errors.New("approval transaction persistence failure")
	ErrApprovalTransactionWorkspaceLiveHeadConflict = errors.New("approval transaction workspace live-head conflict")
	errApprovalTransactionStorageBudgetExceeded     = errors.New("approval transaction storage budget exceeded")

	errApprovalTransactionAuthorityDidNotCommit          = errors.New("current authority did not commit")
	errApprovalTransactionAuthorityCommittedMoreThanOnce = errors.New("current authority committed more than once")

	approvalTransactionLocks sync.Map // canonical root -> *sync.Mutex
)

// ApprovalTransactionError is intentionally bounded.  It never includes a
// command value, filesystem path, proposal text, or another caller value.
// Cause remains available to errors.Is for test and adapter boundaries.
type ApprovalTransactionError struct {
	Kind           string
	CurrentState   string
	CurrentVersion int64
	cause          error
}

func (e *ApprovalTransactionError) Error() string {
	if e == nil {
		return ErrApprovalTransactionInvalid.Error()
	}
	switch e.Kind {
	case "conflict":
		if e.CurrentState != "" {
			return fmt.Sprintf("approval transaction conflict at current state %s version %d", e.CurrentState, e.CurrentVersion)
		}
		return ErrApprovalTransactionConflict.Error()
	case "persistence":
		if e.CurrentState != "" {
			return fmt.Sprintf("%s at current state %s version %d", ErrApprovalTransactionPersistence, e.CurrentState, e.CurrentVersion)
		}
		return ErrApprovalTransactionPersistence.Error()
	default:
		return ErrApprovalTransactionInvalid.Error()
	}
}

func (e *ApprovalTransactionError) Unwrap() error {
	if e == nil {
		return ErrApprovalTransactionInvalid
	}
	base := ErrApprovalTransactionInvalid
	if e.Kind == "conflict" {
		base = ErrApprovalTransactionConflict
	} else if e.Kind == "persistence" {
		base = ErrApprovalTransactionPersistence
	}
	if e.cause != nil {
		return errors.Join(base, e.cause)
	}
	return base
}

func approvalTransactionInvalid(cause error) error {
	return &ApprovalTransactionError{Kind: "invalid", cause: cause}
}

func approvalTransactionConflict(state string, version int64, cause error) error {
	return &ApprovalTransactionError{Kind: "conflict", CurrentState: safeApprovalTransactionState(state), CurrentVersion: safeApprovalTransactionVersion(version), cause: cause}
}

func approvalTransactionPersistence(cause error) error {
	return &ApprovalTransactionError{Kind: "persistence", cause: cause}
}

func safeApprovalTransactionState(state string) string {
	switch state {
	case "none", "active", "rejected", "revoked", "superseded":
		return state
	default:
		return ""
	}
}

func safeApprovalTransactionVersion(version int64) int64 {
	if version < 0 || version > approvalLifecycleMaxVersion {
		return 0
	}
	return version
}

// ApprovalIdempotencyOriginalCommandV1 is the bounded command identity kept
// inside an idempotency record.  It intentionally mirrors the registered
// schema's nested object instead of embedding the command's optional fields.
type ApprovalIdempotencyOriginalCommandV1 struct {
	CommandID               string `json:"commandId"`
	IdempotencyKey          string `json:"idempotencyKey"`
	ActorID                 string `json:"actorId"`
	WorkspaceID             string `json:"workspaceId"`
	ProposalID              string `json:"proposalId"`
	Decision                string `json:"decision"`
	ExpectedApprovalVersion int64  `json:"expectedApprovalVersion"`
}

// ApprovalIdempotencyOriginalResultV1 is the immutable result identity
// returned for a replay.
type ApprovalIdempotencyOriginalResultV1 struct {
	Outcome          string `json:"outcome"`
	ApprovalID       string `json:"approvalId"`
	EventID          string `json:"eventId"`
	AggregateID      string `json:"aggregateId"`
	AggregateVersion int64  `json:"aggregateVersion"`
	State            string `json:"state"`
}

// ApprovalIdempotencyResultV1 is the exact registered durable idempotency
// contract.  The first committed result is retained with outcome committed.
// A replay is indicated by ApprovalCommitReceipt.Replayed and does not write
// a second record.
type ApprovalIdempotencyResultV1 struct {
	SchemaID         string                               `json:"schemaId"`
	SchemaVersion    int                                  `json:"schemaVersion"`
	IdempotencyKey   string                               `json:"idempotencyKey"`
	CommandID        string                               `json:"commandId"`
	ActorID          string                               `json:"actorId"`
	SessionID        string                               `json:"sessionId"`
	WorkspaceID      string                               `json:"workspaceId"`
	ProposalID       string                               `json:"proposalId"`
	EvidencePackID   string                               `json:"evidencePackId"`
	ComputedBasisID  string                               `json:"computedBasisId"`
	GenerationID     string                               `json:"generationId"`
	RequestDigest    string                               `json:"requestDigest"`
	Outcome          string                               `json:"outcome"`
	ApprovalID       string                               `json:"approvalId"`
	EventID          string                               `json:"eventId"`
	AggregateID      string                               `json:"aggregateId"`
	AggregateVersion int64                                `json:"aggregateVersion"`
	State            string                               `json:"state"`
	CommittedAt      string                               `json:"committedAt"`
	OriginalCommand  ApprovalIdempotencyOriginalCommandV1 `json:"originalCommand"`
	OriginalResult   ApprovalIdempotencyOriginalResultV1  `json:"originalResult"`
}

// ApprovalOutboxCommittedEventV1 is the event identity in the pending
// outbox.  The complete event payload remains in the immutable event log.
type ApprovalOutboxCommittedEventV1 struct {
	EventID          string `json:"eventId"`
	ApprovalID       string `json:"approvalId"`
	AggregateID      string `json:"aggregateId"`
	AggregateVersion int64  `json:"aggregateVersion"`
	WorkspaceID      string `json:"workspaceId"`
	Decision         string `json:"decision"`
	PayloadDigest    string `json:"payloadDigest"`
}

// ApprovalOutboxV1 is one post-commit delivery record.  The approval commit
// creates it as pending, and the delivery boundary may advance it exactly
// once to published after an external publication succeeds.
type ApprovalOutboxV1 struct {
	SchemaID         string                         `json:"schemaId"`
	SchemaVersion    int                            `json:"schemaVersion"`
	OutboxID         string                         `json:"outboxId"`
	EventID          string                         `json:"eventId"`
	AggregateID      string                         `json:"aggregateId"`
	AggregateVersion int64                          `json:"aggregateVersion"`
	WorkspaceID      string                         `json:"workspaceId"`
	PayloadDigest    string                         `json:"payloadDigest"`
	CommittedAt      string                         `json:"committedAt"`
	DeliveryState    string                         `json:"deliveryState"`
	PublishedAt      string                         `json:"publishedAt,omitempty"`
	FailureReason    string                         `json:"failureReason,omitempty"`
	CommittedEvent   ApprovalOutboxCommittedEventV1 `json:"committedEvent"`
}

// ApprovalCommitReceipt is the one result returned by Commit.  All nested
// values are copied before return, and EventPayload is copied as well.
type ApprovalCommitReceipt struct {
	IdempotencyResult ApprovalIdempotencyResultV1 `json:"idempotencyResult"`
	Event             ApprovalEventV2             `json:"event"`
	Aggregate         ApprovalAggregateV2         `json:"aggregate"`
	Outbox            ApprovalOutboxV1            `json:"outbox"`
	EventPayload      []byte                      `json:"-"`
	Replayed          bool                        `json:"replayed"`
}

// ApprovalTransactionSnapshot is a defensive readback of one complete
// durable transaction state.  Events are immutable append records, while
// Aggregates contains the latest projection for each target.
type ApprovalTransactionSnapshot struct {
	Events             []ApprovalEventV2
	Aggregates         []ApprovalAggregateV2
	IdempotencyResults []ApprovalIdempotencyResultV1
	Outbox             []ApprovalOutboxV1
	// targets carries the validated durable target identity needed by the
	// semantic execution replay projection. It remains private so callers
	// cannot manufacture or replace current-authority metadata.
	targets []approvalTransactionTargetRecord
}

type approvalTransactionTarget struct {
	WorkspaceID         string `json:"workspaceId"`
	ProposalID          string `json:"proposalId"`
	EvidencePackID      string `json:"evidencePackId"`
	ComputedBasisID     string `json:"computedBasisId"`
	GenerationID        string `json:"generationId"`
	IntentRevision      int64  `json:"intentRevision"`
	ValidatedSnapshotID string `json:"validatedSnapshotId"`
	WorkspaceEpoch      int64  `json:"workspaceEpoch"`
	MapID               string `json:"mapId"`
	TaskID              string `json:"taskId"`
}

type approvalTransactionTargetRecord struct {
	Target         approvalTransactionTarget `json:"target"`
	Aggregate      ApprovalAggregateV2       `json:"aggregate"`
	GenesisEventID string                    `json:"-"`
	TargetDigest   string                    `json:"-"`
}

type approvalTransactionState struct {
	StoreVersion       int                               `json:"storeVersion"`
	Events             []ApprovalEventV2                 `json:"events"`
	Aggregates         []ApprovalAggregateV2             `json:"aggregates"`
	IdempotencyResults []ApprovalIdempotencyResultV1     `json:"idempotencyResults"`
	Outbox             []ApprovalOutboxV1                `json:"outbox"`
	Targets            []approvalTransactionTargetRecord `json:"targets"`
}

// approvalTransactionCurrentAuthority is a Core-owned seam.  The default
// implementation is installed by the production constructor.  Tests may
// replace it only through the package-private dependency constructor.  The
// commit callback is executed while the store's shared root lock is held.
type approvalTransactionCurrentAuthority func(context.Context, approvalTransactionTarget, *approvalTransactionTargetRecord, func() error) error

// approvalTransactionCommitExecutor is a package-private boundary around the
// final SQLite COMMIT. The default executes the real SQL operation. Tests may
// decorate it through the package-private dependency constructor to model an
// observer error after SQLite accepted the commit without exposing a public
// way to replace the durable commit authority.
type approvalTransactionCommitExecutor func(context.Context, *sql.Conn) error

type approvalTransactionDependencies struct {
	currentAuthority approvalTransactionCurrentAuthority
	commitExecutor   approvalTransactionCommitExecutor
	storageBudget    int64
	fault            func(string) error
	// beforeDatabasePublish is a package-private lifecycle seam used only by
	// tests to pause a fully initialized temporary database before its
	// no-replace publication. It is deliberately not part of the public store
	// configuration.
	beforeDatabasePublish func(string) error
	// cleanupTemporaryArtifacts is a package-private test seam. The production
	// default always removes the temporary database and its sidecars.
	cleanupTemporaryArtifacts func(string) error
	// closeReadOnlyDatabase and removeReadOnlyTemporaryRoot are package-private
	// read-side cleanup seams. The production defaults call the real database
	// close and filesystem removal operations.
	closeReadOnlyDatabase       func(*sql.DB) error
	closeReadOnlyConnection     func(*sql.Conn) error
	removeReadOnlyTemporaryRoot func(string) error
	readOnlyTemporaryRootBase   string
}

// ApprovalTransactionStore is a single-root, process-safe durable commit
// store.  It has no exported fields, so external callers cannot install a
// fake authority or fabricate a persistence-ready mutation.
type ApprovalTransactionStore struct {
	root                        string
	initErr                     error
	lock                        *sync.Mutex
	currentAuthority            approvalTransactionCurrentAuthority
	commitExecutor              approvalTransactionCommitExecutor
	storageBudget               int64
	fault                       func(string) error
	beforeDatabasePublish       func(string) error
	cleanupTemporaryArtifacts   func(string) error
	closeReadOnlyDatabase       func(*sql.DB) error
	closeReadOnlyConnection     func(*sql.Conn) error
	removeReadOnlyTemporaryRoot func(string) error
	readOnlyTemporaryRootBase   string
}

// NewApprovalTransactionStore creates the canonical store below a repository
// root and binds it to a root-coordinated workspace engine. The engine is
// required so approval currentness cannot be inferred from repository-local
// files alone. SnapshotEngine coordinates peer instances for the canonical
// root at the live-head boundary. The repository root itself may be a
// legitimate alias, but managed descendants are required to be non-symlink
// directories/files.
func NewApprovalTransactionStore(repoRoot string, engine *workspace.SnapshotEngine) *ApprovalTransactionStore {
	if strings.TrimSpace(repoRoot) == "" {
		return invalidApprovalTransactionStore("repository root is invalid")
	}
	if engine == nil {
		return invalidApprovalTransactionStore("authoritative workspace engine is required")
	}
	canonicalRoot, err := canonicalApprovalTransactionRepoRoot(repoRoot)
	if err != nil {
		return invalidApprovalTransactionStore("repository root is unavailable")
	}
	if engine.CanonicalRoot() != canonicalRoot {
		return invalidApprovalTransactionStore("authoritative workspace engine does not match repository root")
	}
	activeStorage := storage.New(canonicalRoot)
	return newApprovalTransactionStore(filepath.Join(canonicalRoot, ".codeflow", "approval-transactions"), approvalTransactionDependencies{
		currentAuthority: activeApprovalTransactionAuthority(activeStorage, engine, approvalWorkspaceIDForCanonicalRoot(canonicalRoot)),
	})
}

func canonicalApprovalTransactionRepoRoot(repoRoot string) (string, error) {
	absRoot, err := filepath.Abs(repoRoot)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(absRoot)
	if err != nil || !info.IsDir() {
		return "", errors.New("repository root is unavailable")
	}
	resolved, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		return "", err
	}
	return filepath.Clean(resolved), nil
}

// newFileApprovalTransactionStoreForTest creates the explicit-directory seam
// used by same-package tests. It injects the legacy default authority
// explicitly so the generic constructor cannot silently self-authorize.
// Production callers must use NewApprovalTransactionStore with an
// authoritative workspace engine.
func newFileApprovalTransactionStoreForTest(directory string) *ApprovalTransactionStore {
	return newApprovalTransactionStoreWithDependencies(directory, approvalTransactionDependencies{
		currentAuthority: defaultApprovalTransactionAuthority,
	})
}

// newApprovalTransactionStoreWithDependencies is package-private by design.
// It is a test-only dependency seam and explicitly fills the test authority
// when callers omit one. The underlying generic constructor remains
// fail-closed for nil currentAuthority.
func newApprovalTransactionStoreWithDependencies(directory string, dependencies approvalTransactionDependencies) *ApprovalTransactionStore {
	if dependencies.currentAuthority == nil {
		dependencies.currentAuthority = defaultApprovalTransactionAuthority
	}
	return newApprovalTransactionStore(directory, dependencies)
}

func newApprovalTransactionStore(directory string, dependencies approvalTransactionDependencies) *ApprovalTransactionStore {
	if strings.TrimSpace(directory) == "" {
		return invalidApprovalTransactionStore("managed approval transaction root is invalid")
	}
	root := filepath.Clean(directory)
	var initErr error
	if info, err := os.Lstat(root); err == nil && info.Mode()&os.ModeSymlink != 0 {
		// The managed directory itself is never followed.  A repository or
		// temporary-directory alias supplied by the operating system is
		// canonicalized only when the managed directory is not that alias.
		initErr = errors.New("managed approval transaction root is a symlink")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		initErr = err
	} else if resolved, resolveErr := filepath.EvalSymlinks(root); resolveErr == nil {
		root = filepath.Clean(resolved)
	} else if errors.Is(resolveErr, os.ErrNotExist) {
		parent := filepath.Dir(root)
		if resolvedParent, parentErr := filepath.EvalSymlinks(parent); parentErr == nil {
			root = filepath.Join(resolvedParent, filepath.Base(root))
		}
	}
	if initErr == nil {
		if err := validateApprovalTransactionRoot(root); err != nil {
			initErr = err
		}
	}
	if dependencies.commitExecutor == nil {
		dependencies.commitExecutor = defaultApprovalTransactionCommit
	}
	if dependencies.closeReadOnlyDatabase == nil {
		dependencies.closeReadOnlyDatabase = defaultCloseApprovalTransactionReadOnlyDatabase
	}
	if dependencies.closeReadOnlyConnection == nil {
		dependencies.closeReadOnlyConnection = defaultCloseApprovalTransactionReadOnlyConnection
	}
	if dependencies.removeReadOnlyTemporaryRoot == nil {
		dependencies.removeReadOnlyTemporaryRoot = defaultRemoveApprovalTransactionReadOnlyTemporaryRoot
	}
	if dependencies.storageBudget <= 0 {
		dependencies.storageBudget = approvalTransactionMaxDatabaseBytes
	}
	if dependencies.storageBudget > approvalTransactionMaxDatabaseBytes {
		dependencies.storageBudget = approvalTransactionMaxDatabaseBytes
	}
	if initErr == nil && dependencies.currentAuthority == nil {
		initErr = errors.New("approval transaction current authority is required")
	}
	canonical := root
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		canonical = filepath.Clean(resolved)
	}
	lockValue, _ := approvalTransactionLocks.LoadOrStore(canonical, &sync.Mutex{})
	return &ApprovalTransactionStore{root: root, initErr: initErr, lock: lockValue.(*sync.Mutex), currentAuthority: dependencies.currentAuthority, commitExecutor: dependencies.commitExecutor, storageBudget: dependencies.storageBudget, fault: dependencies.fault, beforeDatabasePublish: dependencies.beforeDatabasePublish, cleanupTemporaryArtifacts: dependencies.cleanupTemporaryArtifacts, closeReadOnlyDatabase: dependencies.closeReadOnlyDatabase, closeReadOnlyConnection: dependencies.closeReadOnlyConnection, removeReadOnlyTemporaryRoot: dependencies.removeReadOnlyTemporaryRoot, readOnlyTemporaryRootBase: dependencies.readOnlyTemporaryRootBase}
}

func invalidApprovalTransactionStore(reason string) *ApprovalTransactionStore {
	return &ApprovalTransactionStore{initErr: errors.New(reason), lock: &sync.Mutex{}, currentAuthority: defaultApprovalTransactionAuthority, commitExecutor: defaultApprovalTransactionCommit, storageBudget: approvalTransactionMaxDatabaseBytes, closeReadOnlyDatabase: defaultCloseApprovalTransactionReadOnlyDatabase, closeReadOnlyConnection: defaultCloseApprovalTransactionReadOnlyConnection, removeReadOnlyTemporaryRoot: defaultRemoveApprovalTransactionReadOnlyTemporaryRoot}
}

func defaultApprovalTransactionAuthority(ctx context.Context, expected approvalTransactionTarget, current *approvalTransactionTargetRecord, commit func() error) error {
	if err := approvalTransactionContextError(ctx); err != nil {
		return err
	}
	if current != nil && !approvalTransactionTargetEqual(expected, current.Target) {
		return approvalTransactionConflict(current.Aggregate.State, current.Aggregate.Version, nil)
	}
	return commit()
}

func activeApprovalTransactionAuthority(activeStorage *storage.Storage, engine *workspace.SnapshotEngine, workspaceID string) approvalTransactionCurrentAuthority {
	return func(ctx context.Context, expected approvalTransactionTarget, current *approvalTransactionTargetRecord, commit func() error) error {
		if err := approvalTransactionContextError(ctx); err != nil {
			return err
		}
		if activeStorage == nil || engine == nil || !validApprovalOpaqueID(workspaceID, approvalWorkspaceIDMax) {
			return approvalTransactionPersistence(nil)
		}
		var preliminary storage.ValidatedActiveApprovalIdentity
		if err := activeStorage.WithValidatedActiveApprovalIdentity(func(identity storage.ValidatedActiveApprovalIdentity) error {
			preliminary = identity
			return nil
		}); err != nil {
			return err
		}
		if err := approvalTransactionContextError(ctx); err != nil {
			return err
		}
		if preliminary.ExpectedLiveHeadSnapshotID == "" {
			return approvalTransactionPersistence(nil)
		}
		authorityErr := engine.WithLiveHead(preliminary.ExpectedLiveHeadSnapshotID, func() error {
			if err := approvalTransactionContextError(ctx); err != nil {
				return err
			}
			return activeStorage.WithValidatedActiveApprovalIdentity(func(identity storage.ValidatedActiveApprovalIdentity) error {
				if err := approvalTransactionContextError(ctx); err != nil {
					return err
				}
				if preliminary.ExpectedLiveHeadSnapshotID != identity.ExpectedLiveHeadSnapshotID || expected.WorkspaceID != workspaceID || expected.ComputedBasisID != identity.ComputedBasisID || expected.GenerationID != identity.GenerationID || expected.ValidatedSnapshotID != identity.ValidatedSnapshotID || expected.MapID != identity.MapID || expected.TaskID != identity.TaskID || expected.IntentRevision != identity.IntentRevision || expected.WorkspaceEpoch != identity.WorkspaceEpoch {
					if current == nil {
						return approvalTransactionConflict("none", 0, nil)
					}
					return approvalTransactionConflict(current.Aggregate.State, current.Aggregate.Version, nil)
				}
				return commit()
			})
		})
		// WithLiveHead may have waited behind a workspace edit. If the caller
		// was cancelled while that wait was in progress, prefer the lifecycle
		// cancellation over the stale-head result returned by the lock check.
		if err := approvalTransactionContextError(ctx); err != nil {
			return err
		}
		if errors.Is(authorityErr, workspace.ErrLiveHeadConflict) {
			if current == nil {
				return approvalTransactionConflict("none", 0, ErrApprovalTransactionWorkspaceLiveHeadConflict)
			}
			return approvalTransactionConflict(current.Aggregate.State, current.Aggregate.Version, ErrApprovalTransactionWorkspaceLiveHeadConflict)
		}
		return authorityErr
	}
}

// Commit validates and durably publishes one Core-sealed mutation.  The
// idempotency lookup occurs before current-authority comparison so exact
// retries remain replayable after the workspace has advanced.
func (s *ApprovalTransactionStore) Commit(ctx context.Context, mutation *ValidatedApprovalMutation) (ApprovalCommitReceipt, error) {
	var zero ApprovalCommitReceipt
	if s == nil {
		return zero, approvalTransactionInvalid(nil)
	}
	prepared, err := prepareApprovalTransactionMutation(mutation)
	if err != nil {
		return zero, err
	}
	if err := approvalTransactionContextError(ctx); err != nil {
		return zero, err
	}
	if s.initErr != nil || s.lock == nil || s.currentAuthority == nil {
		return zero, approvalTransactionPersistence(nil)
	}
	s.lock.Lock()
	defer s.lock.Unlock()
	if err := approvalTransactionContextError(ctx); err != nil {
		return zero, err
	}
	// Read existing state without creating a managed directory. This preserves
	// the no-publication/no-artifact invariant when Core authority rejects the
	// mutation before invoking the commit callback.
	var db *sql.DB
	closeDB := func() {
		if db != nil {
			_ = db.Close()
			db = nil
		}
	}
	defer closeDB()
	db, err = s.openApprovalTransactionDatabaseLocked(ctx, false)
	var state approvalTransactionState
	state.StoreVersion = approvalTransactionStoreVersion
	if errors.Is(err, os.ErrNotExist) {
		err = nil
	} else if err != nil {
		return zero, approvalTransactionPersistence(err)
	} else {
		state, err = loadApprovalTransactionStateDB(ctx, db)
		if err != nil {
			return zero, err
		}
	}
	commandValue := prepared.command.Command()
	if existing, found := findApprovalTransactionIdempotency(state, commandValue.IdempotencyKey, commandValue.WorkspaceID); found {
		if existing.RequestDigest != prepared.requestDigest {
			return zero, approvalTransactionConflict("", 0, nil)
		}
		return receiptFromApprovalTransactionState(state, existing, true), nil
	}
	current, currentIndex := findApprovalTransactionTarget(state, prepared.target)
	if current != nil && current.Aggregate.AggregateID != prepared.aggregate.AggregateID {
		return zero, approvalTransactionConflict(current.Aggregate.State, current.Aggregate.Version, nil)
	}
	if current != nil && !approvalTransactionTargetEqual(prepared.target, current.Target) {
		return zero, approvalTransactionConflict(current.Aggregate.State, current.Aggregate.Version, nil)
	}
	if current == nil {
		if commandValue.ExpectedState != "none" || commandValue.ExpectedApprovalVersion != 0 {
			return zero, approvalTransactionConflict("none", 0, nil)
		}
	} else if current.Aggregate.State != commandValue.ExpectedState || current.Aggregate.Version != commandValue.ExpectedApprovalVersion {
		return zero, approvalTransactionConflict(current.Aggregate.State, current.Aggregate.Version, nil)
	}
	if existingAggregate, found := findApprovalTransactionAggregate(state, prepared.aggregate.AggregateID); found {
		if current == nil || existingAggregate.AggregateID != current.Aggregate.AggregateID {
			return zero, approvalTransactionConflict(existingAggregate.State, existingAggregate.Version, nil)
		}
	}
	if err := verifyApprovalTransactionReducerOutput(prepared, current); err != nil {
		return zero, err
	}
	_, receipt, err := buildApprovalTransactionState(state, prepared, currentIndex)
	if err != nil {
		return zero, err
	}
	currentForAuthority := cloneApprovalTransactionTargetRecord(current)
	var commitMu sync.Mutex
	commitCalls := 0
	var firstCommitErr error
	var duplicateCommitErr error
	commit := func() error {
		// Keep the call count and the physical publication serialized.  A
		// malicious or buggy authority must never be able to publish twice,
		// including when it invokes the callback concurrently.
		commitMu.Lock()
		defer commitMu.Unlock()
		commitCalls++
		if commitCalls > 1 {
			duplicateCommitErr = errApprovalTransactionAuthorityCommittedMoreThanOnce
			return approvalTransactionPersistence(duplicateCommitErr)
		}
		if db == nil {
			var openErr error
			db, openErr = s.openApprovalTransactionDatabaseLocked(ctx, true)
			if openErr != nil {
				persistErr := approvalTransactionPersistence(openErr)
				firstCommitErr = persistErr
				return persistErr
			}
		}
		persisted, persistErr := s.persistApprovalTransactionMutationLocked(ctx, db, prepared)
		if persistErr == nil {
			receipt = persisted
		}
		firstCommitErr = persistErr
		return firstCommitErr
	}
	authorityErr := s.currentAuthority(ctx, prepared.target, currentForAuthority, commit)
	closeDB()
	commitMu.Lock()
	calls := commitCalls
	firstErr := firstCommitErr
	duplicateErr := duplicateCommitErr
	commitMu.Unlock()
	if calls == 0 {
		var transactionErr *ApprovalTransactionError
		if errors.As(authorityErr, &transactionErr) && transactionErr.Kind == "conflict" {
			return zero, authorityErr
		}
		cause := errApprovalTransactionAuthorityDidNotCommit
		if authorityErr != nil {
			cause = errors.Join(cause, authorityErr)
		}
		return zero, approvalTransactionAttachCurrentOrGenesis(approvalTransactionPersistence(cause), current)
	}
	// A successful physical publication is authoritative.  Do not turn a
	// later authority error, including the bounded duplicate-call error,
	// into a failure after the durable result has been committed.
	if firstErr == nil {
		return receipt, nil
	}
	if errors.Is(firstErr, errApprovalTransactionCommitOutcomeUnknown) {
		reconciled, reconcileErr := s.reconcileApprovalTransactionCommit(ctx, prepared, receipt)
		if reconcileErr == nil {
			return reconciled, nil
		}
		reconcileCause := errors.Join(firstErr, reconcileErr)
		if ctxErr := approvalTransactionContextError(ctx); ctxErr != nil {
			reconcileCause = errors.Join(reconcileCause, ctxErr)
		}
		return zero, approvalTransactionAttachCurrentOrGenesis(approvalTransactionPersistence(reconcileCause), current)
	}
	if calls > 1 {
		cause := errApprovalTransactionAuthorityCommittedMoreThanOnce
		if duplicateErr != nil {
			cause = errors.Join(cause, duplicateErr)
		}
		if firstErr != nil {
			cause = errors.Join(cause, firstErr)
		}
		if authorityErr != nil {
			cause = errors.Join(cause, authorityErr)
		}
		return zero, approvalTransactionAttachCurrentOrGenesis(approvalTransactionPersistence(cause), current)
	}
	if firstErr != nil && authorityErr == nil {
		return zero, approvalTransactionAttachCurrentOrGenesis(normalizeApprovalTransactionError(firstErr), current)
	}
	if authorityErr != nil {
		return zero, approvalTransactionAttachCurrentOrGenesis(normalizeApprovalTransactionError(authorityErr), current)
	}
	return receipt, nil
}

func approvalTransactionAttachCurrent(err error, current *approvalTransactionTargetRecord) error {
	if err == nil || current == nil {
		return err
	}
	var transactionErr *ApprovalTransactionError
	if !errors.As(err, &transactionErr) || transactionErr.Kind != "persistence" || transactionErr.CurrentState != "" {
		return err
	}
	copyOfError := *transactionErr
	copyOfError.CurrentState = safeApprovalTransactionState(current.Aggregate.State)
	copyOfError.CurrentVersion = safeApprovalTransactionVersion(current.Aggregate.Version)
	return &copyOfError
}

func approvalTransactionAttachCurrentOrGenesis(err error, current *approvalTransactionTargetRecord) error {
	if current != nil {
		return approvalTransactionAttachCurrent(err, current)
	}
	if err == nil {
		return nil
	}
	var transactionErr *ApprovalTransactionError
	if !errors.As(err, &transactionErr) || transactionErr.Kind != "persistence" || transactionErr.CurrentState != "" {
		return err
	}
	copyOfError := *transactionErr
	copyOfError.CurrentState = "none"
	copyOfError.CurrentVersion = 0
	return &copyOfError
}

// reconcileApprovalTransactionCommit resolves the only ambiguous point in a
// commit: the database may have accepted COMMIT while the caller observed an
// error from the commit boundary.  The caller has already closed the
// connection used for the attempted transaction before entering here.  Read
// a fresh, consistent snapshot and accept success only when the complete
// durable result is exactly the result that was prepared before COMMIT.
//
// This function deliberately does not use the in-memory receipt as authority.
// Every record is loaded and validated by loadApprovalTransactionStateDB, and
// the reconstructed receipt and target are compared field-for-field.  A
// missing, mismatched, or corrupt record remains an unresolved persistence
// outcome, never a successful commit.
func (s *ApprovalTransactionStore) reconcileApprovalTransactionCommit(ctx context.Context, prepared preparedApprovalTransaction, intended ApprovalCommitReceipt) (ApprovalCommitReceipt, error) {
	var zero ApprovalCommitReceipt
	if s == nil {
		return zero, approvalTransactionPersistence(errApprovalTransactionCommitOutcomeUnknown)
	}

	// A commit executor may have durably committed and then reported a context
	// cancellation, or the caller's deadline may expire while this read is in
	// progress. Reconciliation must still be able to observe that durable
	// result, so it uses a fresh bounded context independent of the request.
	// Ordinary cancellation is still preserved by the caller when reconciliation
	// cannot establish a durable outcome.
	reconcileCtx, cancel := context.WithTimeout(context.Background(), time.Duration(approvalTransactionBusyTimeoutMS)*time.Millisecond)
	defer cancel()
	db, err := s.openApprovalTransactionDatabaseLocked(reconcileCtx, false)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return zero, approvalTransactionPersistence(errApprovalTransactionCommitOutcomeUnknown)
		}
		return zero, approvalTransactionPersistence(err)
	}
	defer db.Close()

	state, err := loadApprovalTransactionStateDB(reconcileCtx, db)
	if err != nil {
		return zero, approvalTransactionPersistence(err)
	}
	command := prepared.command.Command()
	durableResult, found := findApprovalTransactionIdempotency(state, command.IdempotencyKey, command.WorkspaceID)
	if !found || durableResult.RequestDigest != prepared.requestDigest {
		return zero, approvalTransactionPersistence(errApprovalTransactionCommitOutcomeUnknown)
	}

	durableReceipt := receiptFromApprovalTransactionState(state, durableResult, false)
	if durableReceipt.Replayed || !reflect.DeepEqual(durableReceipt, intended) {
		return zero, approvalTransactionPersistence(errApprovalTransactionCommitOutcomeUnknown)
	}
	durableTarget, durableIndex := findApprovalTransactionTarget(state, prepared.target)
	if durableIndex < 0 || durableTarget == nil || !approvalTransactionTargetEqual(durableTarget.Target, prepared.target) || !reflect.DeepEqual(approvalTransactionAggregateAtVersion(durableTarget.Aggregate, durableResult.AggregateVersion), durableReceipt.Aggregate) {
		return zero, approvalTransactionPersistence(errApprovalTransactionCommitOutcomeUnknown)
	}
	return durableReceipt, nil
}

// Snapshot reads and validates the complete committed state without repairing
// or reconfiguring the managed approval transaction artifacts.
func (s *ApprovalTransactionStore) Snapshot(ctx context.Context) (snapshot ApprovalTransactionSnapshot, err error) {
	var zero ApprovalTransactionSnapshot
	if s == nil {
		return zero, approvalTransactionInvalid(nil)
	}
	if err := approvalTransactionContextError(ctx); err != nil {
		return zero, err
	}
	if s.initErr != nil || s.lock == nil {
		return zero, approvalTransactionPersistence(nil)
	}
	s.lock.Lock()
	defer s.lock.Unlock()
	db, temporaryRoot, err := s.openApprovalTransactionDatabaseReadOnlyLocked(ctx)
	if err == os.ErrNotExist {
		return ApprovalTransactionSnapshot{}, nil
	}
	if err != nil {
		return zero, approvalTransactionPersistence(err)
	}
	state, loadErr := s.loadApprovalTransactionStateDBReadOnly(ctx, db)
	closeErr := s.closeApprovalTransactionReadOnlyDatabase(db)
	removeErr := s.removeApprovalTransactionReadOnlyTemporaryRoot(temporaryRoot)
	cleanupErr := errors.Join(closeErr, removeErr)
	if loadErr != nil {
		if cleanupErr == nil {
			return zero, loadErr
		}
		return zero, errors.Join(loadErr, approvalTransactionPersistence(cleanupErr))
	}
	if cleanupErr != nil {
		return zero, approvalTransactionPersistence(cleanupErr)
	}
	return snapshotFromApprovalTransactionState(state), nil
}

// MarkApprovalOutboxPublished durably advances one exact committed outbox record
// from pending to published after the caller has completed external delivery.
// It does not create an outbox, alter immutable event data, or support a
// failed delivery state. Repeating the same publication for an already
// published record is an idempotent read-only success.
func (s *ApprovalTransactionStore) MarkApprovalOutboxPublished(ctx context.Context, outboxID, eventID, aggregateID, publishedAt string) (ApprovalOutboxV1, error) {
	var zero ApprovalOutboxV1
	if s == nil {
		return zero, approvalTransactionInvalid(nil)
	}
	if err := approvalTransactionContextError(ctx); err != nil {
		return zero, err
	}
	if !validApprovalLifecycleID(outboxID) || !validApprovalLifecycleID(eventID) || !validApprovalLifecycleID(aggregateID) || !validApprovalTransactionPublishedAt(publishedAt) {
		return zero, approvalTransactionInvalid(nil)
	}
	if s.initErr != nil || s.lock == nil || s.commitExecutor == nil {
		return zero, approvalTransactionPersistence(nil)
	}
	s.lock.Lock()
	defer s.lock.Unlock()
	if err := approvalTransactionContextError(ctx); err != nil {
		return zero, err
	}
	db, err := s.openApprovalTransactionDatabaseLocked(ctx, false)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return zero, approvalTransactionInvalid(nil)
		}
		return zero, approvalTransactionPersistence(err)
	}
	defer db.Close()
	conn, err := db.Conn(ctx)
	if err != nil {
		return zero, approvalTransactionPersistence(err)
	}
	defer conn.Close()
	if err := configureApprovalTransactionConnection(ctx, conn); err != nil {
		return zero, approvalTransactionPersistence(err)
	}
	if err := s.ensureApprovalTransactionStorageBudget(ctx, conn); err != nil {
		return zero, approvalTransactionPersistence(err)
	}
	if err := s.checkpointApprovalTransactionWAL(ctx, conn); err != nil {
		return zero, approvalTransactionPersistence(err)
	}
	if err := retryApprovalTransactionSQLiteBusy(ctx, func() error {
		_, beginErr := conn.ExecContext(approvalTransactionContext(ctx), "BEGIN IMMEDIATE")
		return beginErr
	}); err != nil {
		return zero, approvalTransactionPersistence(err)
	}
	inTransaction := true
	rollback := func(primary error) error {
		if !inTransaction {
			return primary
		}
		_, rollbackErr := conn.ExecContext(context.Background(), "ROLLBACK")
		inTransaction = false
		if rollbackErr != nil {
			return approvalTransactionPersistence(errors.Join(primary, rollbackErr))
		}
		return primary
	}
	state, err := loadApprovalTransactionStateConn(ctx, conn)
	if err != nil {
		return zero, rollback(err)
	}
	selected := -1
	for index := range state.Outbox {
		if state.Outbox[index].OutboxID == outboxID {
			if selected != -1 {
				return zero, rollback(approvalTransactionInvalid(nil))
			}
			selected = index
		}
	}
	if selected < 0 {
		return zero, rollback(approvalTransactionInvalid(nil))
	}
	current := state.Outbox[selected]
	if current.EventID != eventID || current.AggregateID != aggregateID || current.OutboxID != approvalTransactionOutboxID(current.EventID) {
		return zero, rollback(approvalTransactionInvalid(nil))
	}
	if current.DeliveryState == "published" {
		if current.FailureReason != "" || validateApprovalTransactionOutbox(current) != nil {
			return zero, rollback(approvalTransactionInvalid(nil))
		}
		if err := rollback(nil); err != nil {
			return zero, err
		}
		return cloneApprovalOutbox(current), nil
	}
	if current.DeliveryState != "pending" || current.PublishedAt != "" || current.FailureReason != "" {
		return zero, rollback(approvalTransactionInvalid(nil))
	}
	next := cloneApprovalOutbox(current)
	next.DeliveryState = "published"
	next.PublishedAt = publishedAt
	next.FailureReason = ""
	if err := validateApprovalTransactionOutbox(next); err != nil {
		return zero, rollback(approvalTransactionInvalid(err))
	}
	nextState := cloneApprovalTransactionState(state)
	nextState.Outbox[selected] = cloneApprovalOutbox(next)
	if err := validateApprovalTransactionState(nextState); err != nil {
		return zero, rollback(approvalTransactionInvalid(err))
	}
	if err := replayApprovalTransactionState(nextState); err != nil {
		return zero, rollback(approvalTransactionInvalid(err))
	}
	if err := updateApprovalTransactionOutboxPublishedConn(ctx, conn, current, next); err != nil {
		return zero, rollback(approvalTransactionPersistence(err))
	}
	if err := s.callFault("outbox-publish"); err != nil {
		return zero, rollback(approvalTransactionPersistence(err))
	}
	if err := s.ensureApprovalTransactionCommitCapacity(ctx, conn); err != nil {
		return zero, rollback(approvalTransactionPersistence(err))
	}
	if err := approvalTransactionContextError(ctx); err != nil {
		return zero, rollback(err)
	}
	if err := s.commitExecutor(ctx, conn); err != nil {
		primaryErr := approvalTransactionPersistence(&approvalTransactionCommitError{cause: err})
		rollbackErr := rollback(primaryErr)
		_ = conn.Close()
		_ = db.Close()
		if reconciled, reconcileErr := s.reconcileApprovalOutboxPublication(outboxID, eventID, aggregateID); reconcileErr == nil {
			return reconciled, nil
		} else if rollbackErr != nil {
			return zero, errors.Join(rollbackErr, reconcileErr)
		} else {
			return zero, reconcileErr
		}
	}
	inTransaction = false
	postCommitContext, postCommitCancel := context.WithTimeout(context.Background(), approvalTransactionBusyTimeoutMS*time.Millisecond)
	_ = s.checkpointApprovalTransactionWAL(postCommitContext, conn)
	postCommitCancel()
	return cloneApprovalOutbox(next), nil
}

func (s *ApprovalTransactionStore) reconcileApprovalOutboxPublication(outboxID, eventID, aggregateID string) (ApprovalOutboxV1, error) {
	var zero ApprovalOutboxV1
	if s == nil {
		return zero, approvalTransactionPersistence(errApprovalTransactionCommitOutcomeUnknown)
	}
	ctx, cancel := context.WithTimeout(context.Background(), approvalTransactionBusyTimeoutMS*time.Millisecond)
	defer cancel()
	db, err := s.openApprovalTransactionDatabaseLocked(ctx, false)
	if err != nil {
		return zero, approvalTransactionPersistence(errApprovalTransactionCommitOutcomeUnknown)
	}
	defer db.Close()
	state, err := loadApprovalTransactionStateDB(ctx, db)
	if err != nil {
		return zero, approvalTransactionPersistence(errApprovalTransactionCommitOutcomeUnknown)
	}
	var published *ApprovalOutboxV1
	for index := range state.Outbox {
		candidate := state.Outbox[index]
		if candidate.OutboxID != outboxID {
			continue
		}
		if published != nil || candidate.EventID != eventID || candidate.AggregateID != aggregateID || candidate.DeliveryState != "published" || validateApprovalTransactionOutbox(candidate) != nil {
			return zero, approvalTransactionPersistence(errApprovalTransactionCommitOutcomeUnknown)
		}
		copy := cloneApprovalOutbox(candidate)
		published = &copy
	}
	if published == nil {
		return zero, approvalTransactionPersistence(errApprovalTransactionCommitOutcomeUnknown)
	}
	return *published, nil
}

type preparedApprovalTransaction struct {
	command       *ValidatedApprovalCommandV2
	stored        *StoredProposal
	before        *SemanticMapIR
	after         *SemanticMapIR
	event         ApprovalEventV2
	aggregate     ApprovalAggregateV2
	redactedEvent []byte
	requestDigest string
	payloadDigest string
	target        approvalTransactionTarget
}

func prepareApprovalTransactionMutation(mutation *ValidatedApprovalMutation) (preparedApprovalTransaction, error) {
	var zero preparedApprovalTransaction
	if mutation == nil {
		return zero, approvalTransactionInvalid(nil)
	}
	if err := mutation.Validate(); err != nil {
		return zero, approvalTransactionInvalid(nil)
	}
	redactedEvent, err := MarshalRedactedApprovalEventAudit(mutation)
	if err != nil {
		return zero, approvalTransactionInvalid(nil)
	}
	command := mutation.ValidatedCommand()
	stored := mutation.StoredProposal()
	before := mutation.BeforeMap()
	after := mutation.AfterMap()
	event := mutation.Event()
	aggregate := mutation.Aggregate()
	if command == nil || stored == nil || before == nil || after == nil || event == nil || aggregate == nil {
		return zero, approvalTransactionInvalid(nil)
	}
	// Reject non-persistable command and lifecycle payloads before opening or
	// creating the managed SQLite root.  The insert path repeats this check as
	// defense in depth, but preparation must preserve the no-artifact rule for
	// secret-bearing or otherwise unsafe mutations.
	if _, err := approvalTransactionSafeJSON(command.Command()); err != nil {
		return zero, approvalTransactionInvalid(nil)
	}
	if _, err := approvalTransactionSafeJSON(*event); err != nil {
		return zero, approvalTransactionInvalid(nil)
	}
	if _, err := approvalTransactionSafeJSON(*aggregate); err != nil {
		return zero, approvalTransactionInvalid(nil)
	}
	commandDigest, err := approvalCommandIdempotencyDigest(command.Command())
	if err != nil {
		return zero, approvalTransactionInvalid(nil)
	}
	payloadDigestBytes := sha256.Sum256(redactedEvent)
	if !validApprovalTransactionDigest(commandDigest) || !validApprovalTransactionDigest(payloadDigestBytes) {
		return zero, approvalTransactionInvalid(nil)
	}
	target := approvalTransactionTarget{
		WorkspaceID:         command.Command().WorkspaceID,
		ProposalID:          command.Command().ProposalID,
		EvidencePackID:      command.Command().EvidencePackID,
		ComputedBasisID:     command.Command().ComputedBasisID,
		GenerationID:        command.Command().GenerationID,
		IntentRevision:      command.Command().IntentRevision,
		ValidatedSnapshotID: before.ValidatedAgainstSnapshotID,
		WorkspaceEpoch:      before.Basis.WorkspaceEpoch,
		MapID:               before.MapID,
		TaskID:              before.Task.TaskID,
	}
	if !validApprovalTransactionTarget(target) {
		return zero, approvalTransactionInvalid(nil)
	}
	return preparedApprovalTransaction{
		command: command, stored: stored, before: before, after: after,
		event: *event, aggregate: *aggregate, redactedEvent: append([]byte(nil), redactedEvent...),
		requestDigest: "sha256:" + hex.EncodeToString(commandDigest[:]),
		payloadDigest: "sha256:" + hex.EncodeToString(payloadDigestBytes[:]), target: target,
	}, nil
}

func validApprovalTransactionDigest(digest [sha256.Size]byte) bool { return len(digest) == sha256.Size }

func validApprovalTransactionTarget(target approvalTransactionTarget) bool {
	for _, value := range []string{target.WorkspaceID, target.ProposalID, target.EvidencePackID, target.ComputedBasisID, target.GenerationID, target.ValidatedSnapshotID, target.MapID, target.TaskID} {
		if !validApprovalLifecycleID(value) {
			return false
		}
	}
	return target.IntentRevision > 0 && target.WorkspaceEpoch >= 0
}

func approvalTransactionTargetDigest(target approvalTransactionTarget) string {
	data, err := json.Marshal(target)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(append([]byte("codeflow/approval-transaction-target/v1\x00"), data...))
	return "sha256:" + hex.EncodeToString(digest[:])
}

func validApprovalTransactionTargetDigest(target approvalTransactionTarget, digest string) bool {
	if !validApprovalTransactionDigestString(digest) {
		return false
	}
	return digest == approvalTransactionTargetDigest(target)
}

func approvalTransactionTargetEqual(left, right approvalTransactionTarget) bool {
	return left.WorkspaceID == right.WorkspaceID && left.ProposalID == right.ProposalID && left.EvidencePackID == right.EvidencePackID && left.ComputedBasisID == right.ComputedBasisID && left.GenerationID == right.GenerationID && left.IntentRevision == right.IntentRevision && left.ValidatedSnapshotID == right.ValidatedSnapshotID && left.WorkspaceEpoch == right.WorkspaceEpoch && left.MapID == right.MapID && left.TaskID == right.TaskID
}

func cloneApprovalTransactionTargetRecord(record *approvalTransactionTargetRecord) *approvalTransactionTargetRecord {
	if record == nil {
		return nil
	}
	copy := *record
	copy.Aggregate = cloneApprovalAggregateV2(record.Aggregate)
	return &copy
}

func verifyApprovalTransactionReducerOutput(prepared preparedApprovalTransaction, current *approvalTransactionTargetRecord) error {
	var base ApprovalAggregateV2
	if current == nil {
		genesisID := ""
		if len(prepared.aggregate.History) > 0 {
			genesisID = prepared.aggregate.History[0].EventID
		}
		var err error
		base, err = NewApprovalGenesisAggregateV2(prepared.command, prepared.aggregate.AggregateID, genesisID)
		if err != nil {
			return approvalTransactionInvalid(nil)
		}
	} else {
		base = cloneApprovalAggregateV2(current.Aggregate)
	}
	metadata, err := approvalTransactionMetadata(prepared.event, prepared.stored)
	if err != nil {
		return approvalTransactionInvalid(nil)
	}
	wantEvent, wantAggregate, err := ReduceApprovalCommandV2(base, prepared.command, metadata)
	if err != nil || wantEvent == nil || wantAggregate == nil || !reflect.DeepEqual(*wantEvent, prepared.event) || !reflect.DeepEqual(*wantAggregate, prepared.aggregate) {
		return approvalTransactionInvalid(nil)
	}
	return nil
}

func approvalTransactionMetadata(event ApprovalEventV2, stored *StoredProposal) (ApprovalTransitionMetadata, error) {
	timestamp, err := time.Parse(time.RFC3339Nano, event.Timestamp)
	if err != nil || timestamp.UTC().Format(time.RFC3339Nano) != event.Timestamp {
		return ApprovalTransitionMetadata{}, errors.New("invalid lifecycle timestamp")
	}
	metadata := ApprovalTransitionMetadata{EventID: event.EventID, ApprovalID: event.ApprovalID, AggregateID: event.AggregateID, OccurredAt: timestamp}
	if event.Decision == "approve" {
		if stored == nil || stored.Proposal == nil {
			return ApprovalTransitionMetadata{}, errors.New("missing proposal")
		}
		metadata.StoredProposalText = stored.Proposal.ProposedTitle
	}
	return metadata, nil
}

func buildApprovalTransactionState(state approvalTransactionState, prepared preparedApprovalTransaction, currentIndex int) (approvalTransactionState, ApprovalCommitReceipt, error) {
	next := cloneApprovalTransactionState(state)
	if len(next.Events) >= approvalTransactionMaxRecords || len(next.IdempotencyResults) >= approvalTransactionMaxRecords || len(next.Outbox) >= approvalTransactionMaxRecords {
		return approvalTransactionState{}, ApprovalCommitReceipt{}, approvalTransactionInvalid(nil)
	}
	if currentIndex >= 0 {
		next.Aggregates[currentIndex] = cloneApprovalAggregateV2(prepared.aggregate)
		genesisEventID := next.Targets[currentIndex].GenesisEventID
		if genesisEventID == "" && len(prepared.aggregate.History) > 0 {
			genesisEventID = prepared.aggregate.History[0].EventID
		}
		next.Targets[currentIndex] = approvalTransactionTargetRecord{Target: prepared.target, Aggregate: cloneApprovalAggregateV2(prepared.aggregate), GenesisEventID: genesisEventID, TargetDigest: approvalTransactionTargetDigest(prepared.target)}
	} else {
		next.Aggregates = append(next.Aggregates, cloneApprovalAggregateV2(prepared.aggregate))
		genesisEventID := ""
		if len(prepared.aggregate.History) > 0 {
			genesisEventID = prepared.aggregate.History[0].EventID
		}
		next.Targets = append(next.Targets, approvalTransactionTargetRecord{Target: prepared.target, Aggregate: cloneApprovalAggregateV2(prepared.aggregate), GenesisEventID: genesisEventID, TargetDigest: approvalTransactionTargetDigest(prepared.target)})
	}
	next.Events = append(next.Events, prepared.event)
	idempotency := ApprovalIdempotencyResultV1{
		SchemaID: contractharness.ApprovalIdempotencyResultV1SchemaID, SchemaVersion: 1,
		IdempotencyKey: prepared.command.Command().IdempotencyKey, CommandID: prepared.command.Command().CommandID,
		ActorID: prepared.command.Command().ActorID, SessionID: prepared.command.Command().SessionID, WorkspaceID: prepared.command.command.WorkspaceID,
		ProposalID: prepared.command.Command().ProposalID, EvidencePackID: prepared.command.Command().EvidencePackID, ComputedBasisID: prepared.command.Command().ComputedBasisID, GenerationID: prepared.command.Command().GenerationID,
		RequestDigest: prepared.requestDigest, Outcome: "committed", ApprovalID: prepared.event.ApprovalID, EventID: prepared.event.EventID, AggregateID: prepared.aggregate.AggregateID, AggregateVersion: prepared.aggregate.Version, State: prepared.aggregate.State, CommittedAt: prepared.event.Timestamp,
		OriginalCommand: ApprovalIdempotencyOriginalCommandV1{CommandID: prepared.command.command.CommandID, IdempotencyKey: prepared.command.command.IdempotencyKey, ActorID: prepared.command.command.ActorID, WorkspaceID: prepared.command.command.WorkspaceID, ProposalID: prepared.command.command.ProposalID, Decision: prepared.command.command.Decision, ExpectedApprovalVersion: prepared.command.command.ExpectedApprovalVersion},
		OriginalResult:  ApprovalIdempotencyOriginalResultV1{Outcome: "committed", ApprovalID: prepared.event.ApprovalID, EventID: prepared.event.EventID, AggregateID: prepared.aggregate.AggregateID, AggregateVersion: prepared.aggregate.Version, State: prepared.aggregate.State},
	}
	outboxID := approvalTransactionOutboxID(prepared.event.EventID)
	if !validApprovalLifecycleID(outboxID) {
		return approvalTransactionState{}, ApprovalCommitReceipt{}, approvalTransactionInvalid(nil)
	}
	outbox := ApprovalOutboxV1{SchemaID: contractharness.ApprovalOutboxV1SchemaID, SchemaVersion: 1, OutboxID: outboxID, EventID: prepared.event.EventID, AggregateID: prepared.aggregate.AggregateID, AggregateVersion: prepared.aggregate.Version, WorkspaceID: prepared.event.WorkspaceID, PayloadDigest: prepared.payloadDigest, CommittedAt: prepared.event.Timestamp, DeliveryState: "pending", CommittedEvent: ApprovalOutboxCommittedEventV1{EventID: prepared.event.EventID, ApprovalID: prepared.event.ApprovalID, AggregateID: prepared.aggregate.AggregateID, AggregateVersion: prepared.aggregate.Version, WorkspaceID: prepared.event.WorkspaceID, Decision: prepared.event.Decision, PayloadDigest: prepared.payloadDigest}}
	if err := validateApprovalTransactionRecords(prepared.event, prepared.aggregate, idempotency, outbox); err != nil {
		return approvalTransactionState{}, ApprovalCommitReceipt{}, err
	}
	next.IdempotencyResults = append(next.IdempotencyResults, idempotency)
	next.Outbox = append(next.Outbox, outbox)
	if err := validateApprovalTransactionState(next); err != nil {
		return approvalTransactionState{}, ApprovalCommitReceipt{}, err
	}
	receipt := ApprovalCommitReceipt{IdempotencyResult: cloneApprovalIdempotencyResult(idempotency), Event: prepared.event, Aggregate: cloneApprovalAggregateV2(prepared.aggregate), Outbox: cloneApprovalOutbox(outbox), EventPayload: append([]byte(nil), prepared.redactedEvent...)}
	return next, receipt, nil
}

func validateApprovalTransactionRecords(event ApprovalEventV2, aggregate ApprovalAggregateV2, idempotency ApprovalIdempotencyResultV1, outbox ApprovalOutboxV1) error {
	eventBytes, err := json.Marshal(event)
	if err != nil {
		return approvalTransactionInvalid(nil)
	}
	if err := contractharness.ValidateVS09Contract(contractharness.ApprovalEventV2SchemaID, eventBytes); err != nil {
		return approvalTransactionInvalid(nil)
	}
	if err := validateApprovalLifecycleEvent(event); err != nil {
		return approvalTransactionInvalid(nil)
	}
	aggregateBytes, err := json.Marshal(aggregate)
	if err != nil {
		return approvalTransactionInvalid(nil)
	}
	if err := contractharness.ValidateVS09Contract(contractharness.ApprovalAggregateV2SchemaID, aggregateBytes); err != nil {
		return approvalTransactionInvalid(nil)
	}
	if err := validateApprovalLifecycleAggregate(aggregate); err != nil {
		return approvalTransactionInvalid(nil)
	}
	idempotencyBytes, err := json.Marshal(idempotency)
	if err != nil {
		return approvalTransactionInvalid(nil)
	}
	if err := contractharness.ValidateVS09Contract(contractharness.ApprovalIdempotencyResultV1SchemaID, idempotencyBytes); err != nil {
		return approvalTransactionInvalid(nil)
	}
	outboxBytes, err := json.Marshal(outbox)
	if err != nil {
		return approvalTransactionInvalid(nil)
	}
	if err := contractharness.ValidateVS09Contract(contractharness.ApprovalOutboxV1SchemaID, outboxBytes); err != nil {
		return approvalTransactionInvalid(nil)
	}
	return nil
}

func (s *ApprovalTransactionStore) callFault(step string) error {
	if s != nil && s.fault != nil {
		return s.fault(step)
	}
	return nil
}

var errApprovalTransactionCommitOutcomeUnknown = errors.New("approval transaction commit outcome unknown")

type approvalTransactionCommitError struct {
	cause error
}

func (e *approvalTransactionCommitError) Error() string {
	return errApprovalTransactionCommitOutcomeUnknown.Error()
}

func (e *approvalTransactionCommitError) Unwrap() error {
	if e == nil || e.cause == nil {
		return errApprovalTransactionCommitOutcomeUnknown
	}
	return errors.Join(errApprovalTransactionCommitOutcomeUnknown, e.cause)
}

func defaultApprovalTransactionCommit(ctx context.Context, conn *sql.Conn) error {
	if conn == nil {
		return errors.New("approval transaction connection is unavailable")
	}
	_, err := conn.ExecContext(approvalTransactionContext(ctx), "COMMIT")
	return err
}

// persistApprovalTransactionMutationLocked is the only function that
// publishes approval records.  It starts an IMMEDIATE SQLite transaction,
// re-reads the current event log under the writer lock, then appends the event
// and updates the projection, idempotency record, and outbox together.  The
// SQL COMMIT is the sole publication point.
func (s *ApprovalTransactionStore) persistApprovalTransactionMutationLocked(ctx context.Context, db *sql.DB, prepared preparedApprovalTransaction) (ApprovalCommitReceipt, error) {
	var zero ApprovalCommitReceipt
	if db == nil {
		return zero, approvalTransactionPersistence(nil)
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return zero, approvalTransactionPersistence(err)
	}
	defer conn.Close()
	if err := configureApprovalTransactionConnection(ctx, conn); err != nil {
		return zero, approvalTransactionPersistence(err)
	}
	if err := s.ensureApprovalTransactionStorageBudget(ctx, conn); err != nil {
		return zero, approvalTransactionPersistence(err)
	}
	if err := approvalTransactionContextError(ctx); err != nil {
		return zero, err
	}
	if err := s.checkpointApprovalTransactionWAL(ctx, conn); err != nil {
		return zero, approvalTransactionPersistence(err)
	}
	if err := retryApprovalTransactionSQLiteBusy(ctx, func() error {
		_, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE")
		return err
	}); err != nil {
		return zero, approvalTransactionPersistence(err)
	}
	inTransaction := true
	rollback := func(primary error) error {
		if !inTransaction {
			return primary
		}
		_, rollbackErr := conn.ExecContext(context.Background(), "ROLLBACK")
		inTransaction = false
		if rollbackErr != nil {
			return approvalTransactionPersistence(errors.Join(primary, rollbackErr))
		}
		return primary
	}

	state, err := loadApprovalTransactionStateConn(ctx, conn)
	if err != nil {
		return zero, rollback(err)
	}
	commandValue := prepared.command.Command()
	if existing, found := findApprovalTransactionIdempotency(state, commandValue.IdempotencyKey, commandValue.WorkspaceID); found {
		if existing.RequestDigest != prepared.requestDigest {
			return zero, rollback(approvalTransactionConflict("", 0, nil))
		}
		replayed := receiptFromApprovalTransactionState(state, existing, true)
		return replayed, rollback(nil)
	}
	current, currentIndex := findApprovalTransactionTarget(state, prepared.target)
	if current != nil && current.Aggregate.AggregateID != prepared.aggregate.AggregateID {
		return zero, rollback(approvalTransactionConflict(current.Aggregate.State, current.Aggregate.Version, nil))
	}
	if current != nil && !approvalTransactionTargetEqual(prepared.target, current.Target) {
		return zero, rollback(approvalTransactionConflict(current.Aggregate.State, current.Aggregate.Version, nil))
	}
	if current == nil {
		if commandValue.ExpectedState != "none" || commandValue.ExpectedApprovalVersion != 0 {
			return zero, rollback(approvalTransactionConflict("none", 0, nil))
		}
	} else if current.Aggregate.State != commandValue.ExpectedState || current.Aggregate.Version != commandValue.ExpectedApprovalVersion {
		return zero, rollback(approvalTransactionConflict(current.Aggregate.State, current.Aggregate.Version, nil))
	}
	if existingAggregate, found := findApprovalTransactionAggregate(state, prepared.aggregate.AggregateID); found {
		if current == nil || existingAggregate.AggregateID != current.Aggregate.AggregateID {
			return zero, rollback(approvalTransactionConflict(existingAggregate.State, existingAggregate.Version, nil))
		}
	}
	if err := verifyApprovalTransactionReducerOutput(prepared, current); err != nil {
		return zero, rollback(err)
	}
	next, receipt, err := buildApprovalTransactionState(state, prepared, currentIndex)
	if err != nil {
		return zero, rollback(err)
	}
	if err := s.callFault("transaction-target"); err != nil {
		return zero, rollback(approvalTransactionPersistence(err))
	}
	target := next.Targets[len(next.Targets)-1]
	if currentIndex >= 0 {
		target = next.Targets[currentIndex]
		if err := updateApprovalTransactionTargetConn(ctx, conn, target); err != nil {
			return zero, rollback(approvalTransactionPersistence(err))
		}
	} else if err := insertApprovalTransactionTargetConn(ctx, conn, target); err != nil {
		return zero, rollback(approvalTransactionPersistence(err))
	}
	if err := s.callFault("transaction-event"); err != nil {
		return zero, rollback(approvalTransactionPersistence(err))
	}
	event := next.Events[len(next.Events)-1]
	if err := insertApprovalTransactionEventConn(ctx, conn, event); err != nil {
		return zero, rollback(approvalTransactionPersistence(err))
	}
	if err := s.callFault("transaction-idempotency"); err != nil {
		return zero, rollback(approvalTransactionPersistence(err))
	}
	idempotency := next.IdempotencyResults[len(next.IdempotencyResults)-1]
	commandJSON, err := json.Marshal(prepared.command)
	if err != nil {
		return zero, rollback(approvalTransactionInvalid(nil))
	}
	if err := insertApprovalTransactionIdempotencyConn(ctx, conn, idempotency, commandJSON); err != nil {
		return zero, rollback(approvalTransactionPersistence(err))
	}
	if err := s.callFault("transaction-outbox"); err != nil {
		return zero, rollback(approvalTransactionPersistence(err))
	}
	outbox := next.Outbox[len(next.Outbox)-1]
	if err := insertApprovalTransactionOutboxConn(ctx, conn, outbox); err != nil {
		return zero, rollback(approvalTransactionPersistence(err))
	}
	if err := s.ensureApprovalTransactionCommitCapacity(ctx, conn); err != nil {
		return zero, rollback(approvalTransactionPersistence(err))
	}
	if err := s.callFault("transaction-before-commit"); err != nil {
		return zero, rollback(approvalTransactionPersistence(err))
	}
	if err := approvalTransactionContextError(ctx); err != nil {
		return zero, rollback(err)
	}
	if s == nil || s.commitExecutor == nil {
		return zero, rollback(approvalTransactionPersistence(errApprovalTransactionCommitOutcomeUnknown))
	}
	if err := s.commitExecutor(ctx, conn); err != nil {
		return zero, rollback(approvalTransactionPersistence(&approvalTransactionCommitError{cause: err}))
	}
	inTransaction = false
	// A post-commit hook models interruption after SQLite has made the
	// transaction authoritative.  Its error can never turn success into an
	// ambiguous failure.
	postCommitContext, postCommitCancel := context.WithTimeout(context.Background(), approvalTransactionBusyTimeoutMS*time.Millisecond)
	_ = s.checkpointApprovalTransactionWAL(postCommitContext, conn)
	postCommitCancel()
	_ = s.callFault("after-commit")
	return receipt, nil
}

var approvalTransactionSQLiteSchema = []string{
	`CREATE TABLE IF NOT EXISTS approval_targets (
		aggregate_id TEXT PRIMARY KEY NOT NULL,
		workspace_id TEXT NOT NULL,
		proposal_id TEXT NOT NULL,
		evidence_pack_id TEXT NOT NULL,
		computed_basis_id TEXT NOT NULL,
		generation_id TEXT NOT NULL,
		intent_revision INTEGER NOT NULL,
		validated_snapshot_id TEXT NOT NULL,
		workspace_epoch INTEGER NOT NULL,
		map_id TEXT NOT NULL,
		task_id TEXT NOT NULL,
		target_digest TEXT NOT NULL,
		aggregate_version INTEGER NOT NULL,
		state TEXT NOT NULL,
		genesis_event_id TEXT NOT NULL,
		aggregate_json BLOB NOT NULL,
		UNIQUE(workspace_id, proposal_id, evidence_pack_id, computed_basis_id, generation_id, intent_revision),
		CHECK(length(aggregate_json) <= 16777216)
	) STRICT`,
	`CREATE TABLE IF NOT EXISTS approval_events (
		seq INTEGER PRIMARY KEY AUTOINCREMENT,
		schema_id TEXT NOT NULL,
		schema_version INTEGER NOT NULL,
		event_id TEXT NOT NULL UNIQUE,
		approval_id TEXT NOT NULL UNIQUE,
		aggregate_id TEXT NOT NULL REFERENCES approval_targets(aggregate_id),
		aggregate_version INTEGER NOT NULL,
		actor_id TEXT NOT NULL,
		session_id TEXT NOT NULL,
		workspace_id TEXT NOT NULL,
		proposal_id TEXT NOT NULL,
		evidence_pack_id TEXT NOT NULL,
		computed_basis_id TEXT NOT NULL,
		generation_id TEXT NOT NULL,
		intent_revision INTEGER NOT NULL,
		decision TEXT NOT NULL,
		approved_text TEXT NOT NULL,
		timestamp TEXT NOT NULL,
		lifecycle_relation TEXT NOT NULL,
		predecessor_approval_id TEXT NOT NULL,
		payload_digest TEXT NOT NULL,
		payload_json BLOB NOT NULL,
		CHECK(length(payload_json) <= 16777216),
		UNIQUE(aggregate_id, aggregate_version)
	) STRICT`,
	`CREATE TABLE IF NOT EXISTS approval_idempotency (
		workspace_id TEXT NOT NULL,
		idempotency_key TEXT NOT NULL,
		command_id TEXT NOT NULL,
		actor_id TEXT NOT NULL,
		session_id TEXT NOT NULL,
		proposal_id TEXT NOT NULL,
		evidence_pack_id TEXT NOT NULL,
		computed_basis_id TEXT NOT NULL,
		generation_id TEXT NOT NULL,
		request_digest TEXT NOT NULL,
		outcome TEXT NOT NULL,
		approval_id TEXT NOT NULL,
		event_id TEXT NOT NULL UNIQUE REFERENCES approval_events(event_id),
		aggregate_id TEXT NOT NULL,
		aggregate_version INTEGER NOT NULL,
		state TEXT NOT NULL,
		committed_at TEXT NOT NULL,
		command_json BLOB NOT NULL,
		result_json BLOB NOT NULL,
		PRIMARY KEY(workspace_id, idempotency_key),
		CHECK(length(command_json) <= 16777216),
		CHECK(length(result_json) <= 16777216)
	) STRICT`,
	`CREATE TABLE IF NOT EXISTS approval_outbox (
		outbox_id TEXT PRIMARY KEY NOT NULL,
		event_id TEXT NOT NULL UNIQUE REFERENCES approval_events(event_id),
		aggregate_id TEXT NOT NULL,
		aggregate_version INTEGER NOT NULL,
		workspace_id TEXT NOT NULL,
		payload_digest TEXT NOT NULL,
		committed_at TEXT NOT NULL,
		delivery_state TEXT NOT NULL,
		published_at TEXT NOT NULL,
		failure_reason TEXT NOT NULL,
		committed_event_json BLOB NOT NULL,
		outbox_json BLOB NOT NULL,
		CHECK(length(committed_event_json) <= 16777216),
		CHECK(length(outbox_json) <= 16777216)
	) STRICT`,
	`CREATE TRIGGER IF NOT EXISTS approval_events_no_update BEFORE UPDATE ON approval_events BEGIN SELECT RAISE(ABORT, 'approval events are immutable'); END`,
	`CREATE TRIGGER IF NOT EXISTS approval_events_no_delete BEFORE DELETE ON approval_events BEGIN SELECT RAISE(ABORT, 'approval events are immutable'); END`,
}

var approvalTransactionSQLiteTables = map[string][]string{
	"approval_targets":     {"aggregate_id", "workspace_id", "proposal_id", "evidence_pack_id", "computed_basis_id", "generation_id", "intent_revision", "validated_snapshot_id", "workspace_epoch", "map_id", "task_id", "target_digest", "aggregate_version", "state", "genesis_event_id", "aggregate_json"},
	"approval_events":      {"seq", "schema_id", "schema_version", "event_id", "approval_id", "aggregate_id", "aggregate_version", "actor_id", "session_id", "workspace_id", "proposal_id", "evidence_pack_id", "computed_basis_id", "generation_id", "intent_revision", "decision", "approved_text", "timestamp", "lifecycle_relation", "predecessor_approval_id", "payload_digest", "payload_json"},
	"approval_idempotency": {"workspace_id", "idempotency_key", "command_id", "actor_id", "session_id", "proposal_id", "evidence_pack_id", "computed_basis_id", "generation_id", "request_digest", "outcome", "approval_id", "event_id", "aggregate_id", "aggregate_version", "state", "committed_at", "command_json", "result_json"},
	"approval_outbox":      {"outbox_id", "event_id", "aggregate_id", "aggregate_version", "workspace_id", "payload_digest", "committed_at", "delivery_state", "published_at", "failure_reason", "committed_event_json", "outbox_json"},
}

var approvalTransactionSQLiteTriggers = []string{"approval_events_no_update", "approval_events_no_delete"}

type approvalTransactionSQLiteColumn struct {
	name     string
	typeName string
	notNull  int
	primary  int
}

var approvalTransactionSQLiteColumnDefinitions = map[string][]approvalTransactionSQLiteColumn{
	"approval_targets": {
		{name: "aggregate_id", typeName: "TEXT", notNull: 1, primary: 1},
		{name: "workspace_id", typeName: "TEXT", notNull: 1},
		{name: "proposal_id", typeName: "TEXT", notNull: 1},
		{name: "evidence_pack_id", typeName: "TEXT", notNull: 1},
		{name: "computed_basis_id", typeName: "TEXT", notNull: 1},
		{name: "generation_id", typeName: "TEXT", notNull: 1},
		{name: "intent_revision", typeName: "INTEGER", notNull: 1},
		{name: "validated_snapshot_id", typeName: "TEXT", notNull: 1},
		{name: "workspace_epoch", typeName: "INTEGER", notNull: 1},
		{name: "map_id", typeName: "TEXT", notNull: 1},
		{name: "task_id", typeName: "TEXT", notNull: 1},
		{name: "target_digest", typeName: "TEXT", notNull: 1},
		{name: "aggregate_version", typeName: "INTEGER", notNull: 1},
		{name: "state", typeName: "TEXT", notNull: 1},
		{name: "genesis_event_id", typeName: "TEXT", notNull: 1},
		{name: "aggregate_json", typeName: "BLOB", notNull: 1},
	},
	"approval_events": {
		{name: "seq", typeName: "INTEGER", primary: 1},
		{name: "schema_id", typeName: "TEXT", notNull: 1},
		{name: "schema_version", typeName: "INTEGER", notNull: 1},
		{name: "event_id", typeName: "TEXT", notNull: 1},
		{name: "approval_id", typeName: "TEXT", notNull: 1},
		{name: "aggregate_id", typeName: "TEXT", notNull: 1},
		{name: "aggregate_version", typeName: "INTEGER", notNull: 1},
		{name: "actor_id", typeName: "TEXT", notNull: 1},
		{name: "session_id", typeName: "TEXT", notNull: 1},
		{name: "workspace_id", typeName: "TEXT", notNull: 1},
		{name: "proposal_id", typeName: "TEXT", notNull: 1},
		{name: "evidence_pack_id", typeName: "TEXT", notNull: 1},
		{name: "computed_basis_id", typeName: "TEXT", notNull: 1},
		{name: "generation_id", typeName: "TEXT", notNull: 1},
		{name: "intent_revision", typeName: "INTEGER", notNull: 1},
		{name: "decision", typeName: "TEXT", notNull: 1},
		{name: "approved_text", typeName: "TEXT", notNull: 1},
		{name: "timestamp", typeName: "TEXT", notNull: 1},
		{name: "lifecycle_relation", typeName: "TEXT", notNull: 1},
		{name: "predecessor_approval_id", typeName: "TEXT", notNull: 1},
		{name: "payload_digest", typeName: "TEXT", notNull: 1},
		{name: "payload_json", typeName: "BLOB", notNull: 1},
	},
	"approval_idempotency": {
		{name: "workspace_id", typeName: "TEXT", notNull: 1, primary: 1},
		{name: "idempotency_key", typeName: "TEXT", notNull: 1, primary: 2},
		{name: "command_id", typeName: "TEXT", notNull: 1},
		{name: "actor_id", typeName: "TEXT", notNull: 1},
		{name: "session_id", typeName: "TEXT", notNull: 1},
		{name: "proposal_id", typeName: "TEXT", notNull: 1},
		{name: "evidence_pack_id", typeName: "TEXT", notNull: 1},
		{name: "computed_basis_id", typeName: "TEXT", notNull: 1},
		{name: "generation_id", typeName: "TEXT", notNull: 1},
		{name: "request_digest", typeName: "TEXT", notNull: 1},
		{name: "outcome", typeName: "TEXT", notNull: 1},
		{name: "approval_id", typeName: "TEXT", notNull: 1},
		{name: "event_id", typeName: "TEXT", notNull: 1},
		{name: "aggregate_id", typeName: "TEXT", notNull: 1},
		{name: "aggregate_version", typeName: "INTEGER", notNull: 1},
		{name: "state", typeName: "TEXT", notNull: 1},
		{name: "committed_at", typeName: "TEXT", notNull: 1},
		{name: "command_json", typeName: "BLOB", notNull: 1},
		{name: "result_json", typeName: "BLOB", notNull: 1},
	},
	"approval_outbox": {
		{name: "outbox_id", typeName: "TEXT", notNull: 1, primary: 1},
		{name: "event_id", typeName: "TEXT", notNull: 1},
		{name: "aggregate_id", typeName: "TEXT", notNull: 1},
		{name: "aggregate_version", typeName: "INTEGER", notNull: 1},
		{name: "workspace_id", typeName: "TEXT", notNull: 1},
		{name: "payload_digest", typeName: "TEXT", notNull: 1},
		{name: "committed_at", typeName: "TEXT", notNull: 1},
		{name: "delivery_state", typeName: "TEXT", notNull: 1},
		{name: "published_at", typeName: "TEXT", notNull: 1},
		{name: "failure_reason", typeName: "TEXT", notNull: 1},
		{name: "committed_event_json", typeName: "BLOB", notNull: 1},
		{name: "outbox_json", typeName: "BLOB", notNull: 1},
	},
}

type approvalTransactionSQLiteIndex struct {
	origin  string
	columns []string
}

var approvalTransactionSQLiteIndexDefinitions = map[string][]approvalTransactionSQLiteIndex{
	"approval_targets": {
		{origin: "pk", columns: []string{"aggregate_id"}},
		{origin: "u", columns: []string{"workspace_id", "proposal_id", "evidence_pack_id", "computed_basis_id", "generation_id", "intent_revision"}},
	},
	"approval_events": {
		{origin: "u", columns: []string{"event_id"}},
		{origin: "u", columns: []string{"approval_id"}},
		{origin: "u", columns: []string{"aggregate_id", "aggregate_version"}},
	},
	"approval_idempotency": {
		{origin: "pk", columns: []string{"workspace_id", "idempotency_key"}},
		{origin: "u", columns: []string{"event_id"}},
	},
	"approval_outbox": {
		{origin: "pk", columns: []string{"outbox_id"}},
		{origin: "u", columns: []string{"event_id"}},
	},
}

type approvalTransactionSQLiteForeignKey struct {
	table    string
	from     string
	to       string
	onUpdate string
	onDelete string
	match    string
}

var approvalTransactionSQLiteForeignKeyDefinitions = map[string][]approvalTransactionSQLiteForeignKey{
	"approval_targets": {},
	"approval_events": {
		{table: "approval_targets", from: "aggregate_id", to: "aggregate_id", onUpdate: "NO ACTION", onDelete: "NO ACTION", match: "NONE"},
	},
	"approval_idempotency": {
		{table: "approval_events", from: "event_id", to: "event_id", onUpdate: "NO ACTION", onDelete: "NO ACTION", match: "NONE"},
	},
	"approval_outbox": {
		{table: "approval_events", from: "event_id", to: "event_id", onUpdate: "NO ACTION", onDelete: "NO ACTION", match: "NONE"},
	},
}

// databasePath is intentionally package-private. The public store API exposes
// records through Snapshot, never the SQLite file itself.
func (s *ApprovalTransactionStore) databasePath() string {
	if s == nil {
		return ""
	}
	return filepath.Join(s.root, approvalTransactionDatabaseName)
}

func approvalTransactionContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

func approvalTransactionBusyTimeoutForContext(ctx context.Context) int {
	defaultTimeout := approvalTransactionBusyTimeoutMS
	if ctx == nil {
		return defaultTimeout
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		return defaultTimeout
	}
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return 1
	}
	remainingMS := remaining.Milliseconds()
	if remainingMS < 1 {
		return 1
	}
	if remainingMS >= int64(defaultTimeout) {
		return defaultTimeout
	}
	return int(remainingMS)
}

func (s *ApprovalTransactionStore) openApprovalTransactionDatabaseLocked(ctx context.Context, create bool) (*sql.DB, error) {
	if s == nil || s.initErr != nil {
		return nil, errors.New("approval transaction store is unavailable")
	}
	ctx = approvalTransactionContext(ctx)
	if err := s.ensureRootLocked(create); err != nil {
		return nil, err
	}
	path := s.databasePath()
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		if !create {
			return nil, os.ErrNotExist
		}
		for attempts := 0; attempts < 3; attempts++ {
			temporaryPath, prepareErr := s.prepareApprovalTransactionDatabaseLocked(ctx)
			if prepareErr != nil {
				return nil, prepareErr
			}
			publishErr := s.publishApprovalTransactionDatabaseLocked(temporaryPath, path)
			if publishErr == nil {
				info, err = os.Lstat(path)
				break
			}
			cleanupErr := s.cleanupApprovalTransactionTemporaryArtifacts(temporaryPath)
			if cleanupErr != nil {
				return nil, errors.Join(publishErr, errors.New("approval transaction temporary cleanup failed"), cleanupErr)
			}
			if !errors.Is(publishErr, os.ErrExist) {
				return nil, publishErr
			}
			info, err = os.Lstat(path)
			if err == nil {
				break
			}
			if !errors.Is(err, os.ErrNotExist) {
				return nil, errors.Join(err, cleanupErr)
			}
			if ctxErr := approvalTransactionContextError(ctx); ctxErr != nil {
				return nil, ctxErr
			}
		}
		if errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
	if err != nil {
		return nil, err
	}
	budget := s.storageBudgetBytes()
	if err := validateApprovalTransactionSQLiteArtifactWithBudget(path, info, true, budget); err != nil {
		return nil, err
	}
	// A final database path is always an already-published artifact. Empty or
	// short files are never valid here, even when this call is made from the
	// create path. New databases are initialized privately in a temporary file
	// and validated before no-replace publication.
	if err := validateApprovalTransactionSQLiteHeader(path, info, false); err != nil {
		return nil, err
	}
	if _, err := approvalTransactionSQLiteArtifactBytes(path, budget); err != nil {
		return nil, err
	}
	// modernc.org/sqlite applies SQLITE_DBCONFIG_DEFENSIVE while opening a
	// physical connection when this DSN option is present.  The SQL PRAGMA
	// spelling is not sufficient because SQLite exposes defensive mode as a
	// connection configuration rather than a readable pragma. Build a file
	// URI so path bytes such as '?', '#', '%' and spaces cannot be parsed as
	// DSN syntax or decoded into a different filename.
	dsn, err := approvalTransactionSQLiteDSN(path)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := retryApprovalTransactionSQLiteBusy(ctx, func() error { return db.PingContext(ctx) }); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := configureApprovalTransactionDatabase(ctx, db, budget); err != nil {
		_ = db.Close()
		return nil, err
	}
	if _, err := approvalTransactionSQLiteArtifactBytes(path, budget); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

// openApprovalTransactionDatabaseReadOnlyLocked opens only an existing,
// owner-only approval database in SQLite read-only mode. It deliberately does
// not call ensureRootLocked or any write-capable PRAGMA so a history query
// cannot create, repair, chmod, or reconfigure managed artifacts.
func (s *ApprovalTransactionStore) openApprovalTransactionDatabaseReadOnlyLocked(ctx context.Context) (*sql.DB, string, error) {
	if s == nil || s.initErr != nil {
		return nil, "", errors.New("approval transaction store is unavailable")
	}
	ctx = approvalTransactionContext(ctx)
	if err := s.validateApprovalTransactionRootReadOnlyLocked(); err != nil {
		return nil, "", err
	}
	path := s.databasePath()
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, "", os.ErrNotExist
	}
	if err != nil {
		return nil, "", err
	}
	budget := s.storageBudgetBytes()
	if err := validateApprovalTransactionSQLiteArtifactWithBudget(path, info, true, budget); err != nil {
		return nil, "", err
	}
	if err := validateApprovalTransactionSQLiteHeader(path, info, false); err != nil {
		return nil, "", err
	}
	temporaryRoot, temporaryPath, err := s.prepareApprovalTransactionReadOnlyCopy(ctx, path, budget)
	if err == os.ErrNotExist {
		return nil, "", os.ErrNotExist
	}
	if err != nil {
		return nil, "", err
	}
	dsn, err := approvalTransactionSQLiteReadOnlyDSN(temporaryPath)
	if err != nil {
		return nil, "", errors.Join(err, s.removeApprovalTransactionReadOnlyTemporaryRoot(temporaryRoot))
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, "", errors.Join(err, s.removeApprovalTransactionReadOnlyTemporaryRoot(temporaryRoot))
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := retryApprovalTransactionSQLiteBusy(ctx, func() error { return db.PingContext(ctx) }); err != nil {
		return nil, "", errors.Join(err, s.closeApprovalTransactionReadOnlyDatabase(db), s.removeApprovalTransactionReadOnlyTemporaryRoot(temporaryRoot))
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, "", errors.Join(err, s.closeApprovalTransactionReadOnlyDatabase(db), s.removeApprovalTransactionReadOnlyTemporaryRoot(temporaryRoot))
	}
	configureErr := configureApprovalTransactionReadOnlyConnection(ctx, conn)
	connCloseErr := conn.Close()
	if configureErr != nil || connCloseErr != nil {
		return nil, "", errors.Join(configureErr, connCloseErr, s.closeApprovalTransactionReadOnlyDatabase(db), s.removeApprovalTransactionReadOnlyTemporaryRoot(temporaryRoot))
	}
	return db, temporaryRoot, nil
}

type approvalTransactionReadArtifact struct {
	suffix string
	mode   os.FileMode
	size   int64
	data   []byte
}

// prepareApprovalTransactionReadOnlyCopy copies only a stable committed DB/WAL
// view into a private temporary directory. SQLite's WAL reader must update its
// shared-memory index and lock bytes, so the query connection never opens the
// managed path. A source change during the copy causes a bounded retry instead
// of returning a mixed database/WAL pair.
func (s *ApprovalTransactionStore) prepareApprovalTransactionReadOnlyCopy(ctx context.Context, path string, budget int64) (string, string, error) {
	ctx = approvalTransactionContext(ctx)
	for attempt := 0; attempt < 3; attempt++ {
		if err := approvalTransactionContextError(ctx); err != nil {
			return "", "", err
		}
		before, err := readApprovalTransactionReadArtifacts(path, budget)
		if errors.Is(err, os.ErrNotExist) {
			return "", "", os.ErrNotExist
		}
		if err != nil {
			return "", "", err
		}
		temporaryRoot, err := os.MkdirTemp(s.approvalTransactionReadOnlyTemporaryRootBase(), ".codeflow-approval-read-*")
		if err != nil {
			return "", "", err
		}
		temporaryPath := filepath.Join(temporaryRoot, approvalTransactionDatabaseName)
		copyErr := writeApprovalTransactionReadArtifacts(temporaryPath, before)
		if copyErr != nil {
			return "", "", errors.Join(copyErr, s.removeApprovalTransactionReadOnlyTemporaryRoot(temporaryRoot))
		}
		after, err := readApprovalTransactionReadArtifacts(path, budget)
		if errors.Is(err, os.ErrNotExist) {
			return "", "", joinApprovalTransactionReadOnlyCleanup(os.ErrNotExist, s.removeApprovalTransactionReadOnlyTemporaryRoot(temporaryRoot))
		}
		if err != nil {
			return "", "", joinApprovalTransactionReadOnlyCleanup(err, s.removeApprovalTransactionReadOnlyTemporaryRoot(temporaryRoot))
		}
		if approvalTransactionReadArtifactsEqual(before, after) {
			return temporaryRoot, temporaryPath, nil
		}
		cleanupErr := s.removeApprovalTransactionReadOnlyTemporaryRoot(temporaryRoot)
		if cleanupErr != nil {
			return "", "", errors.Join(errors.New("approval transaction database changed during read"), cleanupErr)
		}
	}
	return "", "", errors.New("approval transaction database changed during read")
}

func joinApprovalTransactionReadOnlyCleanup(primary, cleanup error) error {
	if cleanup == nil {
		return primary
	}
	return errors.Join(primary, cleanup)
}

func readApprovalTransactionReadArtifacts(path string, budget int64) ([]approvalTransactionReadArtifact, error) {
	if _, err := approvalTransactionSQLiteArtifactBytes(path, budget); err != nil {
		return nil, err
	}
	artifacts := make([]approvalTransactionReadArtifact, 0, 2)
	for _, suffix := range []string{"", "-wal"} {
		artifactPath := path + suffix
		info, err := os.Lstat(artifactPath)
		if errors.Is(err, os.ErrNotExist) {
			if suffix == "" {
				return nil, os.ErrNotExist
			}
			continue
		}
		if err != nil {
			return nil, err
		}
		if err := validateApprovalTransactionSQLiteArtifactWithBudget(artifactPath, info, true, budget); err != nil {
			return nil, err
		}
		data, err := os.ReadFile(artifactPath)
		if err != nil {
			return nil, err
		}
		if int64(len(data)) != info.Size() {
			return nil, errors.New("approval transaction artifact changed during read")
		}
		artifacts = append(artifacts, approvalTransactionReadArtifact{suffix: suffix, mode: info.Mode(), size: info.Size(), data: data})
	}
	return artifacts, nil
}

func writeApprovalTransactionReadArtifacts(path string, artifacts []approvalTransactionReadArtifact) error {
	for _, artifact := range artifacts {
		targetPath := path + artifact.suffix
		if err := os.WriteFile(targetPath, artifact.data, 0o600); err != nil {
			return err
		}
		if err := os.Chmod(targetPath, artifact.mode.Perm()); err != nil {
			return err
		}
	}
	return nil
}

func approvalTransactionReadArtifactsEqual(left, right []approvalTransactionReadArtifact) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index].suffix != right[index].suffix || left[index].mode != right[index].mode || left[index].size != right[index].size || !bytes.Equal(left[index].data, right[index].data) {
			return false
		}
	}
	return true
}

func (s *ApprovalTransactionStore) prepareApprovalTransactionDatabaseLocked(ctx context.Context) (string, error) {
	if s == nil || strings.TrimSpace(s.root) == "" {
		return "", errors.New("approval transaction database is unavailable")
	}
	temporaryFile, err := os.CreateTemp(s.root, "."+approvalTransactionDatabaseName+".tmp-*")
	if err != nil {
		return "", err
	}
	temporaryPath := temporaryFile.Name()
	temporaryClosed := false
	cleanup := func(primary error) (string, error) {
		var closeErr error
		if !temporaryClosed {
			closeErr = temporaryFile.Close()
			temporaryClosed = true
		}
		return "", errors.Join(primary, closeErr, removeApprovalTransactionTemporaryArtifacts(temporaryPath))
	}
	if err := temporaryFile.Chmod(0o600); err != nil {
		return cleanup(err)
	}
	if err := temporaryFile.Close(); err != nil {
		temporaryClosed = true
		return cleanup(err)
	}
	temporaryClosed = true
	dsn, err := approvalTransactionSQLiteDSN(temporaryPath)
	if err != nil {
		return cleanup(err)
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return cleanup(err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	closeDatabase := func(primary error) (string, error) {
		return "", errors.Join(primary, db.Close(), removeApprovalTransactionTemporaryArtifacts(temporaryPath))
	}
	if err := retryApprovalTransactionSQLiteBusy(ctx, func() error { return db.PingContext(ctx) }); err != nil {
		return closeDatabase(err)
	}
	if err := configureApprovalTransactionDatabase(ctx, db, s.storageBudgetBytes()); err != nil {
		return closeDatabase(err)
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return closeDatabase(err)
	}
	checkpointErr := checkpointApprovalTransactionWALAtPath(ctx, conn, temporaryPath, s.storageBudgetBytes())
	connCloseErr := conn.Close()
	if checkpointErr != nil || connCloseErr != nil {
		return closeDatabase(errors.Join(checkpointErr, connCloseErr))
	}
	if err := db.Close(); err != nil {
		return "", errors.Join(err, removeApprovalTransactionTemporaryArtifacts(temporaryPath))
	}
	if err := syncApprovalTransactionFile(temporaryPath); err != nil {
		return "", errors.Join(err, removeApprovalTransactionTemporaryArtifacts(temporaryPath))
	}
	if err := removeApprovalTransactionTemporarySidecars(temporaryPath); err != nil {
		return "", errors.Join(err, removeApprovalTransactionTemporaryArtifacts(temporaryPath))
	}
	info, err := os.Lstat(temporaryPath)
	if err != nil {
		return "", errors.Join(err, removeApprovalTransactionTemporaryArtifacts(temporaryPath))
	}
	if err := validateApprovalTransactionSQLiteArtifactWithBudget(temporaryPath, info, true, s.storageBudgetBytes()); err != nil {
		return "", errors.Join(err, removeApprovalTransactionTemporaryArtifacts(temporaryPath))
	}
	if err := validateApprovalTransactionSQLiteHeader(temporaryPath, info, false); err != nil {
		return "", errors.Join(err, removeApprovalTransactionTemporaryArtifacts(temporaryPath))
	}
	if _, err := approvalTransactionSQLiteArtifactBytes(temporaryPath, s.storageBudgetBytes()); err != nil {
		return "", errors.Join(err, removeApprovalTransactionTemporaryArtifacts(temporaryPath))
	}
	if s.beforeDatabasePublish != nil {
		if err := s.beforeDatabasePublish(temporaryPath); err != nil {
			return "", errors.Join(err, removeApprovalTransactionTemporaryArtifacts(temporaryPath))
		}
	}
	return temporaryPath, nil
}

func (s *ApprovalTransactionStore) publishApprovalTransactionDatabaseLocked(temporaryPath, finalPath string) error {
	if strings.TrimSpace(temporaryPath) == "" || strings.TrimSpace(finalPath) == "" {
		return errors.New("approval transaction database publication path is invalid")
	}
	if err := os.Link(temporaryPath, finalPath); err != nil {
		return err
	}
	if err := syncProposalDirectory(s.root); err != nil {
		return err
	}
	if err := os.Remove(temporaryPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return syncProposalDirectory(s.root)
}

func checkpointApprovalTransactionWALAtPath(ctx context.Context, conn *sql.Conn, path string, budget int64) error {
	if conn == nil || strings.TrimSpace(path) == "" || budget <= 0 {
		return errApprovalTransactionStorageBudgetExceeded
	}
	err := retryApprovalTransactionSQLiteBusy(ctx, func() error {
		var busy, frames, checkpointed int
		if err := conn.QueryRowContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &frames, &checkpointed); err != nil {
			return err
		}
		if busy != 0 {
			return errors.New("database is locked")
		}
		return nil
	})
	if err != nil {
		return err
	}
	_, err = approvalTransactionSQLiteArtifactBytes(path, budget)
	return err
}

func syncApprovalTransactionFile(path string) error {
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	syncErr := file.Sync()
	closeErr := file.Close()
	return errors.Join(syncErr, closeErr)
}

func removeApprovalTransactionTemporarySidecars(path string) error {
	var joined error
	for _, sidecar := range []string{path + "-wal", path + "-shm"} {
		if err := os.Remove(sidecar); err != nil && !errors.Is(err, os.ErrNotExist) {
			joined = errors.Join(joined, err)
		}
	}
	return joined
}

func removeApprovalTransactionTemporaryArtifacts(path string) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	return errors.Join(removeApprovalTransactionTemporarySidecars(path), func() error {
		err := os.Remove(path)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}())
}

func (s *ApprovalTransactionStore) cleanupApprovalTransactionTemporaryArtifacts(path string) error {
	if s != nil && s.cleanupTemporaryArtifacts != nil {
		return s.cleanupTemporaryArtifacts(path)
	}
	return removeApprovalTransactionTemporaryArtifacts(path)
}

func defaultCloseApprovalTransactionReadOnlyDatabase(db *sql.DB) error {
	if db == nil {
		return nil
	}
	return db.Close()
}

func defaultCloseApprovalTransactionReadOnlyConnection(conn *sql.Conn) error {
	if conn == nil {
		return nil
	}
	return conn.Close()
}

func defaultRemoveApprovalTransactionReadOnlyTemporaryRoot(path string) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	return os.RemoveAll(path)
}

func (s *ApprovalTransactionStore) closeApprovalTransactionReadOnlyDatabase(db *sql.DB) error {
	if db == nil {
		return nil
	}
	if s != nil && s.closeReadOnlyDatabase != nil {
		return s.closeReadOnlyDatabase(db)
	}
	return defaultCloseApprovalTransactionReadOnlyDatabase(db)
}

func (s *ApprovalTransactionStore) removeApprovalTransactionReadOnlyTemporaryRoot(path string) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	if s != nil && s.removeReadOnlyTemporaryRoot != nil {
		return s.removeReadOnlyTemporaryRoot(path)
	}
	return defaultRemoveApprovalTransactionReadOnlyTemporaryRoot(path)
}

func (s *ApprovalTransactionStore) approvalTransactionReadOnlyTemporaryRootBase() string {
	if s == nil {
		return ""
	}
	return s.readOnlyTemporaryRootBase
}

func approvalTransactionSQLiteDSN(path string) (string, error) {
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	databaseURL := &url.URL{Scheme: "file", Path: absolutePath, RawQuery: "_defensive=1"}
	return databaseURL.String(), nil
}

func approvalTransactionSQLiteReadOnlyDSN(path string) (string, error) {
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	databaseURL := &url.URL{Scheme: "file", Path: absolutePath, RawQuery: "mode=ro&_defensive=1"}
	return databaseURL.String(), nil
}

func validateApprovalTransactionSQLiteArtifact(path string, info os.FileInfo, requireMode bool) error {
	return validateApprovalTransactionSQLiteArtifactWithBudget(path, info, requireMode, approvalTransactionMaxDatabaseBytes)
}

func validateApprovalTransactionSQLiteArtifactWithBudget(_ string, info os.FileInfo, requireMode bool, budget int64) error {
	if info == nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return errors.New("approval transaction database artifact is unsafe")
	}
	if info.Size() < 0 || info.Size() > approvalTransactionMaxDatabaseBytes {
		return errors.New("approval transaction database is oversized")
	}
	if budget > 0 && info.Size() > budget {
		return errApprovalTransactionStorageBudgetExceeded
	}
	if requireMode && info.Mode().Perm() != 0o600 {
		return errors.New("approval transaction database is not owner-only")
	}
	return nil
}

func validateApprovalTransactionSQLiteHeader(path string, info os.FileInfo, allowEmpty bool) error {
	if info == nil {
		return errors.New("approval transaction database header is invalid")
	}
	if info.Size() == 0 && allowEmpty {
		return nil
	}
	const sqliteHeaderBytes = int64(16)
	if info.Size() < sqliteHeaderBytes {
		return errors.New("approval transaction database header is invalid")
	}
	file, err := os.Open(path)
	if err != nil {
		return errors.New("approval transaction database header is unavailable")
	}
	defer file.Close()
	header := make([]byte, sqliteHeaderBytes)
	if _, err := io.ReadFull(file, header); err != nil || !bytes.Equal(header, []byte("SQLite format 3\x00")) {
		return errors.New("approval transaction database header is invalid")
	}
	return nil
}

func (s *ApprovalTransactionStore) storageBudgetBytes() int64 {
	if s == nil || s.storageBudget <= 0 {
		return approvalTransactionMaxDatabaseBytes
	}
	return s.storageBudget
}

func approvalTransactionSQLiteArtifactBytes(path string, budget int64) (int64, error) {
	if strings.TrimSpace(path) == "" || budget <= 0 {
		return 0, errApprovalTransactionStorageBudgetExceeded
	}
	var total int64
	for _, artifactPath := range []string{path, path + "-wal", path + "-shm"} {
		info, err := os.Lstat(artifactPath)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return 0, err
		}
		if err := validateApprovalTransactionSQLiteArtifactWithBudget(artifactPath, info, true, budget); err != nil {
			return 0, err
		}
		size := info.Size()
		if size < 0 || total > budget-size {
			return 0, errApprovalTransactionStorageBudgetExceeded
		}
		total += size
	}
	return total, nil
}

func (s *ApprovalTransactionStore) ensureApprovalTransactionStorageWithinBudget() error {
	if s == nil {
		return errApprovalTransactionStorageBudgetExceeded
	}
	_, err := approvalTransactionSQLiteArtifactBytes(s.databasePath(), s.storageBudgetBytes())
	return err
}

func ensureApprovalTransactionStorageBudget(ctx context.Context, conn *sql.Conn, budget int64) error {
	if conn == nil || budget <= 0 {
		return errApprovalTransactionStorageBudgetExceeded
	}
	var pageSize, maxPageCount int64
	if err := conn.QueryRowContext(ctx, "PRAGMA page_size").Scan(&pageSize); err != nil || pageSize <= 0 {
		return errApprovalTransactionStorageBudgetExceeded
	}
	if err := conn.QueryRowContext(ctx, "PRAGMA max_page_count").Scan(&maxPageCount); err != nil || maxPageCount <= 0 {
		return errApprovalTransactionStorageBudgetExceeded
	}
	maxPages := budget / pageSize
	if maxPages <= 0 {
		return errApprovalTransactionStorageBudgetExceeded
	}
	var pageCount int64
	if err := conn.QueryRowContext(ctx, "PRAGMA page_count").Scan(&pageCount); err != nil || pageCount < 0 || pageCount > maxPages {
		return errApprovalTransactionStorageBudgetExceeded
	}
	if _, err := conn.ExecContext(ctx, fmt.Sprintf("PRAGMA max_page_count=%d", maxPages)); err != nil {
		return errApprovalTransactionStorageBudgetExceeded
	}
	var appliedMaxPageCount int64
	if err := conn.QueryRowContext(ctx, "PRAGMA max_page_count").Scan(&appliedMaxPageCount); err != nil || appliedMaxPageCount <= 0 || appliedMaxPageCount != maxPages || appliedMaxPageCount > int64(^uint64(0)>>1)/pageSize || appliedMaxPageCount*pageSize > budget {
		return errApprovalTransactionStorageBudgetExceeded
	}
	if _, err := conn.ExecContext(ctx, fmt.Sprintf("PRAGMA journal_size_limit=%d", budget)); err != nil {
		return errApprovalTransactionStorageBudgetExceeded
	}
	var journalSizeLimit int64
	if err := conn.QueryRowContext(ctx, "PRAGMA journal_size_limit").Scan(&journalSizeLimit); err != nil || journalSizeLimit != budget {
		return errApprovalTransactionStorageBudgetExceeded
	}
	return nil
}

func (s *ApprovalTransactionStore) ensureApprovalTransactionStorageBudget(ctx context.Context, conn *sql.Conn) error {
	if s == nil {
		return errApprovalTransactionStorageBudgetExceeded
	}
	return ensureApprovalTransactionStorageBudget(ctx, conn, s.storageBudgetBytes())
}

func (s *ApprovalTransactionStore) checkpointApprovalTransactionWAL(ctx context.Context, conn *sql.Conn) error {
	if s == nil || conn == nil {
		return errApprovalTransactionStorageBudgetExceeded
	}
	err := retryApprovalTransactionSQLiteBusy(ctx, func() error {
		var busy, frames, checkpointed int
		if err := conn.QueryRowContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &frames, &checkpointed); err != nil {
			return err
		}
		if busy != 0 {
			return errors.New("database is locked")
		}
		return nil
	})
	if err != nil {
		return err
	}
	return s.ensureApprovalTransactionStorageWithinBudget()
}

func approvalTransactionSafeAdd(left, right int64) (int64, bool) {
	if left < 0 || right < 0 || left > int64(^uint64(0)>>1)-right {
		return 0, false
	}
	return left + right, true
}

func approvalTransactionSafeMul(left, right int64) (int64, bool) {
	if left < 0 || right < 0 {
		return 0, false
	}
	if left == 0 || right == 0 {
		return 0, true
	}
	if left > int64(^uint64(0)>>1)/right {
		return 0, false
	}
	return left * right, true
}

func approvalTransactionWALIndexReserveBytes(budget, frameBytes int64) (int64, bool) {
	if budget <= 0 || frameBytes <= 0 {
		return 0, false
	}
	maxFrames, ok := approvalTransactionCeilDiv(budget, frameBytes)
	if !ok {
		return 0, false
	}
	regions, ok := approvalTransactionCeilDiv(maxFrames, approvalTransactionSQLiteWALFramesPerIndexRegion)
	if !ok {
		return 0, false
	}
	regions, ok = approvalTransactionSafeAdd(regions, approvalTransactionSQLiteWALIndexSafetyRegions)
	if !ok {
		return 0, false
	}
	return approvalTransactionSafeMul(regions, approvalTransactionSQLiteWALIndexRegionBytes)
}

func approvalTransactionCeilDiv(value, divisor int64) (int64, bool) {
	if value < 0 || divisor <= 0 {
		return 0, false
	}
	if value == 0 {
		return 0, true
	}
	quotient := value / divisor
	if value%divisor != 0 {
		if quotient == int64(^uint64(0)>>1) {
			return 0, false
		}
		quotient++
	}
	return quotient, true
}

func (s *ApprovalTransactionStore) ensureApprovalTransactionCommitCapacity(ctx context.Context, conn *sql.Conn) error {
	if s == nil || conn == nil {
		return errApprovalTransactionStorageBudgetExceeded
	}
	budget := s.storageBudgetBytes()
	var pageSize, pageCount, maxPageCount int64
	if err := conn.QueryRowContext(ctx, "PRAGMA page_size").Scan(&pageSize); err != nil {
		return err
	}
	if pageSize <= 0 {
		return errApprovalTransactionStorageBudgetExceeded
	}
	if err := conn.QueryRowContext(ctx, "PRAGMA page_count").Scan(&pageCount); err != nil {
		return err
	}
	if pageCount < 0 {
		return errApprovalTransactionStorageBudgetExceeded
	}
	if err := conn.QueryRowContext(ctx, "PRAGMA max_page_count").Scan(&maxPageCount); err != nil {
		return err
	}
	maxPages := budget / pageSize
	if maxPages <= 0 || pageCount > maxPages || maxPageCount != maxPages || maxPageCount > int64(^uint64(0)>>1)/pageSize {
		return errApprovalTransactionStorageBudgetExceeded
	}
	frameBytes, ok := approvalTransactionSafeAdd(approvalTransactionSQLiteWALFrameBytes, pageSize)
	if !ok {
		return errApprovalTransactionStorageBudgetExceeded
	}
	dirtyFrameBytes, ok := approvalTransactionSafeMul(pageCount, frameBytes)
	if !ok {
		return errApprovalTransactionStorageBudgetExceeded
	}
	potentialBytes, ok := approvalTransactionSafeAdd(approvalTransactionSQLiteWALHeaderBytes, dirtyFrameBytes)
	if !ok {
		return errApprovalTransactionStorageBudgetExceeded
	}
	// SQLite's WAL-index grows in 32 KiB regions, each covering at most 4096
	// frames. Reserve a budget-derived maximum number of regions, plus two
	// safety regions for the header/checkpoint boundary. The existing SHM file
	// is included in actual below, so this is only the possible growth.
	walIndexBytes, ok := approvalTransactionWALIndexReserveBytes(budget, frameBytes)
	if !ok {
		return errApprovalTransactionStorageBudgetExceeded
	}
	potentialBytes, ok = approvalTransactionSafeAdd(potentialBytes, walIndexBytes)
	if !ok {
		return errApprovalTransactionStorageBudgetExceeded
	}
	actual, err := approvalTransactionSQLiteArtifactBytes(s.databasePath(), budget)
	if err != nil {
		return err
	}
	projected, ok := approvalTransactionSafeAdd(actual, potentialBytes)
	if !ok || projected > budget {
		return errApprovalTransactionStorageBudgetExceeded
	}
	return nil
}

func configureApprovalTransactionDatabase(ctx context.Context, db *sql.DB, budget int64) error {
	if db == nil {
		return errors.New("nil approval transaction database")
	}
	ctx = approvalTransactionContext(ctx)
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := configureApprovalTransactionConnection(ctx, conn); err != nil {
		return err
	}
	if err := ensureApprovalTransactionStorageBudget(ctx, conn, budget); err != nil {
		return err
	}
	var version int
	if err := conn.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version != 0 && version != approvalTransactionSQLiteSchemaVersion {
		return errors.New("approval transaction schema version is unsupported")
	}
	if version == 0 {
		var count int
		if err := conn.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'`).Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			// Another process may have committed the schema while this
			// connection still has the pre-migration version cached. Acquire
			// the writer lock and re-read both values before rejecting what
			// could be an ordinary first-open race.
			if err := retryApprovalTransactionSQLiteBusy(ctx, func() error {
				_, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE")
				return err
			}); err != nil {
				return err
			}
			var lockedVersion, lockedTables int
			versionErr := conn.QueryRowContext(ctx, "PRAGMA user_version").Scan(&lockedVersion)
			tablesErr := conn.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'`).Scan(&lockedTables)
			_, rollbackErr := conn.ExecContext(context.Background(), "ROLLBACK")
			if versionErr != nil || tablesErr != nil || rollbackErr != nil {
				return errors.Join(versionErr, tablesErr, rollbackErr)
			}
			if lockedVersion == approvalTransactionSQLiteSchemaVersion {
				return verifyApprovalTransactionSQLiteSchema(ctx, conn)
			}
			return errors.New("approval transaction schema migration is incomplete")
		}
		if err := conn.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type='trigger'`).Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			return errors.New("approval transaction schema migration is incomplete")
		}
		if err := retryApprovalTransactionSQLiteBusy(ctx, func() error { return initializeApprovalTransactionSQLiteSchema(ctx, conn) }); err != nil {
			return err
		}
	}
	return verifyApprovalTransactionSQLiteSchema(ctx, conn)
}

func configureApprovalTransactionConnection(ctx context.Context, conn *sql.Conn) error {
	if conn == nil {
		return errors.New("nil approval transaction connection")
	}
	ctx = approvalTransactionContext(ctx)
	configuredBusyTimeout := approvalTransactionBusyTimeoutForContext(ctx)
	for _, pragma := range []string{
		"PRAGMA foreign_keys=ON",
		"PRAGMA synchronous=FULL",
		fmt.Sprintf("PRAGMA busy_timeout=%d", configuredBusyTimeout),
		"PRAGMA trusted_schema=OFF",
		"PRAGMA defensive=ON",
	} {
		if _, err := conn.ExecContext(ctx, pragma); err != nil {
			return err
		}
	}
	if err := ensureApprovalTransactionWAL(ctx, conn); err != nil {
		return err
	}
	var foreignKeys, synchronous, observedBusyTimeout, trustedSchema int
	if err := conn.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		return fmt.Errorf("foreign keys: %w", err)
	}
	if err := conn.QueryRowContext(ctx, "PRAGMA synchronous").Scan(&synchronous); err != nil {
		return fmt.Errorf("synchronous: %w", err)
	}
	if err := conn.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&observedBusyTimeout); err != nil {
		return fmt.Errorf("busy timeout: %w", err)
	}
	if err := conn.QueryRowContext(ctx, "PRAGMA trusted_schema").Scan(&trustedSchema); err != nil {
		return fmt.Errorf("trusted schema: %w", err)
	}
	if foreignKeys != 1 || synchronous != 2 || observedBusyTimeout != configuredBusyTimeout || trustedSchema != 0 {
		return errors.New("approval transaction database safety pragmas are unavailable")
	}
	return nil
}

func configureApprovalTransactionReadOnlyConnection(ctx context.Context, conn *sql.Conn) error {
	if conn == nil {
		return errors.New("nil approval transaction connection")
	}
	ctx = approvalTransactionContext(ctx)
	configuredBusyTimeout := approvalTransactionBusyTimeoutForContext(ctx)
	for _, pragma := range []string{
		"PRAGMA foreign_keys=ON",
		fmt.Sprintf("PRAGMA busy_timeout=%d", configuredBusyTimeout),
		"PRAGMA trusted_schema=OFF",
		"PRAGMA defensive=ON",
		"PRAGMA query_only=ON",
	} {
		if _, err := conn.ExecContext(ctx, pragma); err != nil {
			return err
		}
	}
	var foreignKeys, observedBusyTimeout, trustedSchema, queryOnly int
	if err := conn.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		return fmt.Errorf("foreign keys: %w", err)
	}
	if err := conn.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&observedBusyTimeout); err != nil {
		return fmt.Errorf("busy timeout: %w", err)
	}
	if err := conn.QueryRowContext(ctx, "PRAGMA trusted_schema").Scan(&trustedSchema); err != nil {
		return fmt.Errorf("trusted schema: %w", err)
	}
	if err := conn.QueryRowContext(ctx, "PRAGMA query_only").Scan(&queryOnly); err != nil {
		return fmt.Errorf("query only: %w", err)
	}
	if foreignKeys != 1 || observedBusyTimeout != configuredBusyTimeout || trustedSchema != 0 || queryOnly != 1 {
		return errors.New("approval transaction read-only safety pragmas are unavailable")
	}
	var version int
	if err := conn.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version != approvalTransactionSQLiteSchemaVersion {
		return errors.New("approval transaction schema version is unsupported")
	}
	if err := verifyApprovalTransactionSQLiteSchema(ctx, conn); err != nil {
		return err
	}
	return nil
}

func ensureApprovalTransactionWAL(ctx context.Context, conn *sql.Conn) error {
	ctx = approvalTransactionContext(ctx)
	err := retryApprovalTransactionSQLiteBusy(ctx, func() error {
		var journalMode string
		if err := conn.QueryRowContext(ctx, "PRAGMA journal_mode=WAL").Scan(&journalMode); err != nil {
			return fmt.Errorf("journal mode: %w", err)
		}
		if !strings.EqualFold(journalMode, "wal") {
			return errors.New("approval transaction database is not in WAL mode")
		}
		return nil
	})
	return err
}

func approvalTransactionSQLiteBusy(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "database is locked") || strings.Contains(message, "database table is locked") || strings.Contains(message, "sqlite_busy")
}

func retryApprovalTransactionSQLiteBusy(ctx context.Context, operation func() error) error {
	ctx = approvalTransactionContext(ctx)
	deadline := time.Now().Add(approvalTransactionBusyTimeoutMS * time.Millisecond)
	for {
		err := operation()
		if err == nil || !approvalTransactionSQLiteBusy(err) {
			return err
		}
		if time.Now().After(deadline) {
			return err
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func initializeApprovalTransactionSQLiteSchema(ctx context.Context, conn *sql.Conn) error {
	ctx = approvalTransactionContext(ctx)
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()
	for _, statement := range approvalTransactionSQLiteSchema {
		if _, err := conn.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	if _, err := conn.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version=%d", approvalTransactionSQLiteSchemaVersion)); err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return err
	}
	committed = true
	return nil
}

func verifyApprovalTransactionSQLiteSchema(ctx context.Context, conn *sql.Conn) error {
	ctx = approvalTransactionContext(ctx)
	type sqliteObject struct {
		kind    string
		name    string
		table   string
		sqlText sql.NullString
	}
	rows, err := conn.QueryContext(ctx, "SELECT type,name,tbl_name,sql FROM sqlite_master ORDER BY type,name")
	if err != nil {
		return err
	}
	defer rows.Close()
	objects := make(map[string]sqliteObject)
	for rows.Next() {
		var object sqliteObject
		if err := rows.Scan(&object.kind, &object.name, &object.table, &object.sqlText); err != nil {
			return err
		}
		if object.name == "sqlite_sequence" {
			if object.kind != "table" {
				return errors.New("approval transaction schema internal object is invalid")
			}
			continue
		}
		if strings.HasPrefix(object.name, "sqlite_autoindex_") {
			if object.kind != "index" {
				return errors.New("approval transaction schema internal index is invalid")
			}
			continue
		}
		objects[object.kind+":"+object.name] = object
	}
	if err := rows.Err(); err != nil {
		return err
	}
	expectedObjects := make(map[string]struct{}, len(approvalTransactionSQLiteColumnDefinitions)+len(approvalTransactionSQLiteTriggers))
	for table := range approvalTransactionSQLiteColumnDefinitions {
		expectedObjects["table:"+table] = struct{}{}
	}
	for _, trigger := range approvalTransactionSQLiteTriggers {
		expectedObjects["trigger:"+trigger] = struct{}{}
	}
	for object := range objects {
		if _, ok := expectedObjects[object]; !ok {
			return errors.New("approval transaction schema contains an unexpected object")
		}
	}
	for object := range expectedObjects {
		if _, ok := objects[object]; !ok {
			return errors.New("approval transaction schema object is missing")
		}
	}

	tableList, err := conn.QueryContext(ctx, "PRAGMA table_list")
	if err != nil {
		return err
	}
	seenTables := make(map[string]struct{})
	for tableList.Next() {
		var schemaName, tableName, tableType string
		var columnCount, withoutRowID, strict int
		if err := tableList.Scan(&schemaName, &tableName, &tableType, &columnCount, &withoutRowID, &strict); err != nil {
			_ = tableList.Close()
			return err
		}
		if schemaName != "main" || tableName == "sqlite_sequence" || tableName == "sqlite_schema" || tableName == "sqlite_temp_schema" {
			continue
		}
		if tableType != "table" || withoutRowID != 0 || strict != 1 {
			_ = tableList.Close()
			return errors.New("approval transaction schema table options are invalid")
		}
		columns, ok := approvalTransactionSQLiteColumnDefinitions[tableName]
		if !ok || columnCount != len(columns) {
			_ = tableList.Close()
			return errors.New("approval transaction schema table list is invalid")
		}
		seenTables[tableName] = struct{}{}
	}
	if err := tableList.Err(); err != nil {
		_ = tableList.Close()
		return err
	}
	if err := tableList.Close(); err != nil {
		return err
	}
	if len(seenTables) != len(approvalTransactionSQLiteColumnDefinitions) {
		return errors.New("approval transaction schema strict table is missing")
	}

	expectedTableSQL := map[string]string{
		"approval_targets":     approvalTransactionSQLiteSchema[0],
		"approval_events":      approvalTransactionSQLiteSchema[1],
		"approval_idempotency": approvalTransactionSQLiteSchema[2],
		"approval_outbox":      approvalTransactionSQLiteSchema[3],
	}
	for table, columns := range approvalTransactionSQLiteColumnDefinitions {
		object := objects["table:"+table]
		if !object.sqlText.Valid || normalizeApprovalTransactionSQLiteSQL(object.sqlText.String) != normalizeApprovalTransactionSQLiteSQL(expectedTableSQL[table]) {
			return errors.New("approval transaction schema table definition is invalid")
		}
		columnRows, err := conn.QueryContext(ctx, `PRAGMA table_info("`+approvalTransactionSQLiteEscapeIdentifier(table)+`")`)
		if err != nil {
			return err
		}
		columnCount := 0
		for columnRows.Next() {
			var cid int
			var name, typeName string
			var notNull, primaryKey int
			var defaultValue any
			if err := columnRows.Scan(&cid, &name, &typeName, &notNull, &defaultValue, &primaryKey); err != nil {
				_ = columnRows.Close()
				return err
			}
			if columnCount >= len(columns) {
				_ = columnRows.Close()
				return errors.New("approval transaction schema contains an unexpected column")
			}
			expected := columns[columnCount]
			if cid != columnCount || name != expected.name || strings.ToUpper(strings.TrimSpace(typeName)) != expected.typeName || notNull != expected.notNull || primaryKey != expected.primary || defaultValue != nil {
				_ = columnRows.Close()
				return errors.New("approval transaction schema column definition is invalid")
			}
			columnCount++
		}
		if err := columnRows.Err(); err != nil {
			_ = columnRows.Close()
			return err
		}
		if err := columnRows.Close(); err != nil {
			return err
		}
		if columnCount != len(columns) {
			return errors.New("approval transaction schema column count is invalid")
		}
		if err := verifyApprovalTransactionSQLiteIndexes(ctx, conn, table); err != nil {
			return err
		}
		if err := verifyApprovalTransactionSQLiteForeignKeys(ctx, conn, table); err != nil {
			return err
		}
	}
	for _, trigger := range approvalTransactionSQLiteTriggers {
		object := objects["trigger:"+trigger]
		triggerIndex := len(approvalTransactionSQLiteSchema) - len(approvalTransactionSQLiteTriggers)
		if trigger == approvalTransactionSQLiteTriggers[1] {
			triggerIndex++
		}
		if !object.sqlText.Valid || normalizeApprovalTransactionSQLiteSQL(object.sqlText.String) != normalizeApprovalTransactionSQLiteSQL(approvalTransactionSQLiteSchema[triggerIndex]) || object.table != "approval_events" {
			return errors.New("approval transaction schema trigger definition is invalid")
		}
	}
	return nil
}

func normalizeApprovalTransactionSQLiteSQL(sqlText string) string {
	normalized := strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(sqlText))), " ")
	for _, prefix := range []string{"create table if not exists ", "create trigger if not exists "} {
		if strings.HasPrefix(normalized, prefix) {
			normalized = strings.TrimPrefix(normalized, prefix)
			if strings.HasPrefix(prefix, "create table") {
				normalized = "create table " + normalized
			} else {
				normalized = "create trigger " + normalized
			}
			break
		}
	}
	return strings.TrimSuffix(normalized, ";")
}

func approvalTransactionSQLiteEscapeIdentifier(identifier string) string {
	return strings.ReplaceAll(identifier, `"`, `""`)
}

func verifyApprovalTransactionSQLiteIndexes(ctx context.Context, conn *sql.Conn, table string) error {
	rows, err := conn.QueryContext(ctx, `PRAGMA index_list("`+approvalTransactionSQLiteEscapeIdentifier(table)+`")`)
	if err != nil {
		return err
	}
	actual := make(map[string]int)
	for rows.Next() {
		var seq, unique, partial int
		var name, origin string
		if err := rows.Scan(&seq, &name, &unique, &origin, &partial); err != nil {
			_ = rows.Close()
			return err
		}
		indexRows, err := conn.QueryContext(ctx, `PRAGMA index_info("`+approvalTransactionSQLiteEscapeIdentifier(name)+`")`)
		if err != nil {
			_ = rows.Close()
			return err
		}
		columns := make([]string, 0)
		for indexRows.Next() {
			var indexSequence, columnID int
			var columnName sql.NullString
			if err := indexRows.Scan(&indexSequence, &columnID, &columnName); err != nil {
				_ = indexRows.Close()
				_ = rows.Close()
				return err
			}
			if !columnName.Valid || indexSequence != len(columns) {
				_ = indexRows.Close()
				_ = rows.Close()
				return errors.New("approval transaction schema index definition is invalid")
			}
			columns = append(columns, columnName.String)
		}
		if err := indexRows.Err(); err != nil {
			_ = indexRows.Close()
			_ = rows.Close()
			return err
		}
		if err := indexRows.Close(); err != nil {
			_ = rows.Close()
			return err
		}
		if unique != 1 {
			return errors.New("approval transaction schema index uniqueness is invalid")
		}
		actual[strings.ToLower(origin)+"|"+strings.Join(columns, ",")]++
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	expected := make(map[string]int)
	for _, index := range approvalTransactionSQLiteIndexDefinitions[table] {
		expected[index.origin+"|"+strings.Join(index.columns, ",")]++
	}
	if !reflect.DeepEqual(actual, expected) {
		return errors.New("approval transaction schema index definition is invalid")
	}
	return nil
}

func verifyApprovalTransactionSQLiteForeignKeys(ctx context.Context, conn *sql.Conn, table string) error {
	rows, err := conn.QueryContext(ctx, `PRAGMA foreign_key_list("`+approvalTransactionSQLiteEscapeIdentifier(table)+`")`)
	if err != nil {
		return err
	}
	actual := make(map[string]int)
	for rows.Next() {
		var id, sequence int
		var foreignKey approvalTransactionSQLiteForeignKey
		if err := rows.Scan(&id, &sequence, &foreignKey.table, &foreignKey.from, &foreignKey.to, &foreignKey.onUpdate, &foreignKey.onDelete, &foreignKey.match); err != nil {
			_ = rows.Close()
			return err
		}
		key := strings.Join([]string{foreignKey.table, foreignKey.from, foreignKey.to, strings.ToUpper(foreignKey.onUpdate), strings.ToUpper(foreignKey.onDelete), strings.ToUpper(foreignKey.match)}, "|")
		actual[key]++
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	expected := make(map[string]int)
	for _, foreignKey := range approvalTransactionSQLiteForeignKeyDefinitions[table] {
		key := strings.Join([]string{foreignKey.table, foreignKey.from, foreignKey.to, strings.ToUpper(foreignKey.onUpdate), strings.ToUpper(foreignKey.onDelete), strings.ToUpper(foreignKey.match)}, "|")
		expected[key]++
	}
	if !reflect.DeepEqual(actual, expected) {
		return errors.New("approval transaction schema foreign key definition is invalid")
	}
	return nil
}

func approvalTransactionSafeJSON(value any) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil || len(raw) == 0 || len(raw) > approvalTransactionMaxBytes || !utf8.Valid(raw) {
		return nil, errors.New("approval transaction payload is invalid")
	}
	redacted, count, err := secret.RedactJSON(raw)
	if err != nil || count != 0 {
		return nil, errors.New("approval transaction payload is not persistable")
	}
	return redacted, nil
}

func insertApprovalTransactionTargetConn(ctx context.Context, conn *sql.Conn, record approvalTransactionTargetRecord) error {
	if conn == nil || !validApprovalTransactionTarget(record.Target) || !validApprovalTransactionTargetDigest(record.Target, record.TargetDigest) || validateApprovalLifecycleAggregate(record.Aggregate) != nil {
		return errors.New("approval transaction target is invalid")
	}
	aggregateJSON, err := approvalTransactionSafeJSON(record.Aggregate)
	if err != nil {
		return fmt.Errorf("target aggregate payload: %w", err)
	}
	if len(record.Aggregate.History) == 0 || !validApprovalLifecycleID(record.Aggregate.History[0].EventID) {
		return errors.New("approval transaction target genesis is invalid")
	}
	genesisEventID := record.GenesisEventID
	if genesisEventID == "" {
		genesisEventID = record.Aggregate.History[0].EventID
	}
	if genesisEventID != record.Aggregate.History[0].EventID {
		return errors.New("approval transaction target genesis does not match")
	}
	_, err = conn.ExecContext(approvalTransactionContext(ctx), `INSERT INTO approval_targets
		(aggregate_id,workspace_id,proposal_id,evidence_pack_id,computed_basis_id,generation_id,intent_revision,validated_snapshot_id,workspace_epoch,map_id,task_id,target_digest,aggregate_version,state,genesis_event_id,aggregate_json)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		record.Aggregate.AggregateID, record.Target.WorkspaceID, record.Target.ProposalID, record.Target.EvidencePackID, record.Target.ComputedBasisID, record.Target.GenerationID,
		record.Target.IntentRevision, record.Target.ValidatedSnapshotID, record.Target.WorkspaceEpoch, record.Target.MapID, record.Target.TaskID, record.TargetDigest,
		record.Aggregate.Version, record.Aggregate.State, genesisEventID, aggregateJSON)
	return err
}

func updateApprovalTransactionTargetConn(ctx context.Context, conn *sql.Conn, record approvalTransactionTargetRecord) error {
	if conn == nil || !validApprovalTransactionTarget(record.Target) || !validApprovalTransactionTargetDigest(record.Target, record.TargetDigest) || validateApprovalLifecycleAggregate(record.Aggregate) != nil {
		return errors.New("approval transaction target is invalid")
	}
	aggregateJSON, err := approvalTransactionSafeJSON(record.Aggregate)
	if err != nil {
		return fmt.Errorf("target aggregate payload: %w", err)
	}
	if len(record.Aggregate.History) == 0 || record.GenesisEventID == "" || record.GenesisEventID != record.Aggregate.History[0].EventID {
		return errors.New("approval transaction target genesis is invalid")
	}
	result, err := conn.ExecContext(approvalTransactionContext(ctx), `UPDATE approval_targets SET
		workspace_id=?,proposal_id=?,evidence_pack_id=?,computed_basis_id=?,generation_id=?,intent_revision=?,validated_snapshot_id=?,workspace_epoch=?,map_id=?,task_id=?,target_digest=?,aggregate_version=?,state=?,genesis_event_id=?,aggregate_json=? WHERE aggregate_id=?`,
		record.Target.WorkspaceID, record.Target.ProposalID, record.Target.EvidencePackID, record.Target.ComputedBasisID, record.Target.GenerationID, record.Target.IntentRevision,
		record.Target.ValidatedSnapshotID, record.Target.WorkspaceEpoch, record.Target.MapID, record.Target.TaskID, record.TargetDigest, record.Aggregate.Version, record.Aggregate.State,
		record.GenesisEventID, aggregateJSON, record.Aggregate.AggregateID)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return errors.New("approval transaction target projection is missing")
	}
	return nil
}

func insertApprovalTransactionEventConn(ctx context.Context, conn *sql.Conn, event ApprovalEventV2) error {
	if conn == nil || validateApprovalLifecycleEvent(event) != nil {
		return errors.New("approval transaction event is invalid")
	}
	payloadJSON, err := approvalTransactionSafeJSON(event)
	if err != nil {
		return fmt.Errorf("event payload: %w", err)
	}
	payloadDigest := sha256.Sum256(payloadJSON)
	digest := "sha256:" + hex.EncodeToString(payloadDigest[:])
	_, err = conn.ExecContext(approvalTransactionContext(ctx), `INSERT INTO approval_events
		(schema_id,schema_version,event_id,approval_id,aggregate_id,aggregate_version,actor_id,session_id,workspace_id,proposal_id,evidence_pack_id,computed_basis_id,generation_id,intent_revision,decision,approved_text,timestamp,lifecycle_relation,predecessor_approval_id,payload_digest,payload_json)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		event.SchemaID, event.SchemaVersion, event.EventID, event.ApprovalID, event.AggregateID, event.AggregateVersion, event.ActorID, event.SessionID, event.WorkspaceID, event.ProposalID,
		event.EvidencePackID, event.ComputedBasisID, event.GenerationID, event.IntentRevision, event.Decision, event.ApprovedText, event.Timestamp, event.LifecycleRelation,
		event.PredecessorApprovalID, digest, payloadJSON)
	return err
}

func insertApprovalTransactionIdempotencyConn(ctx context.Context, conn *sql.Conn, result ApprovalIdempotencyResultV1, commandJSON []byte) error {
	if conn == nil {
		return errors.New("approval transaction idempotency is invalid")
	}
	resultJSON, err := approvalTransactionSafeJSON(result)
	if err != nil || len(commandJSON) == 0 || len(commandJSON) > approvalTransactionMaxBytes || !utf8.Valid(commandJSON) {
		if err != nil {
			return fmt.Errorf("idempotency result payload: %w", err)
		}
		return errors.New("approval transaction idempotency payload is invalid")
	}
	redacted, count, redactErr := secret.RedactJSON(commandJSON)
	if redactErr != nil || count != 0 {
		return errors.New("approval transaction command payload is not persistable")
	}
	commandJSON = redacted
	_, err = conn.ExecContext(approvalTransactionContext(ctx), `INSERT INTO approval_idempotency
		(workspace_id,idempotency_key,command_id,actor_id,session_id,proposal_id,evidence_pack_id,computed_basis_id,generation_id,request_digest,outcome,approval_id,event_id,aggregate_id,aggregate_version,state,committed_at,command_json,result_json)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		result.WorkspaceID, result.IdempotencyKey, result.CommandID, result.ActorID, result.SessionID, result.ProposalID, result.EvidencePackID, result.ComputedBasisID,
		result.GenerationID, result.RequestDigest, result.Outcome, result.ApprovalID, result.EventID, result.AggregateID, result.AggregateVersion,
		result.State, result.CommittedAt, commandJSON, resultJSON)
	return err
}

func insertApprovalTransactionOutboxConn(ctx context.Context, conn *sql.Conn, outbox ApprovalOutboxV1) error {
	if conn == nil {
		return errors.New("approval transaction outbox is invalid")
	}
	outboxJSON, err := approvalTransactionSafeJSON(outbox)
	if err != nil {
		return fmt.Errorf("outbox payload: %w", err)
	}
	committedEventJSON, err := approvalTransactionSafeJSON(outbox.CommittedEvent)
	if err != nil {
		return fmt.Errorf("committed event payload: %w", err)
	}
	_, err = conn.ExecContext(approvalTransactionContext(ctx), `INSERT INTO approval_outbox
		(outbox_id,event_id,aggregate_id,aggregate_version,workspace_id,payload_digest,committed_at,delivery_state,published_at,failure_reason,committed_event_json,outbox_json)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
		outbox.OutboxID, outbox.EventID, outbox.AggregateID, outbox.AggregateVersion, outbox.WorkspaceID, outbox.PayloadDigest, outbox.CommittedAt, outbox.DeliveryState,
		outbox.PublishedAt, outbox.FailureReason, committedEventJSON, outboxJSON)
	return err
}

func updateApprovalTransactionOutboxPublishedConn(ctx context.Context, conn *sql.Conn, previous, next ApprovalOutboxV1) error {
	if conn == nil || previous.DeliveryState != "pending" || next.DeliveryState != "published" {
		return errors.New("approval transaction outbox transition is invalid")
	}
	previousJSON, err := approvalTransactionSafeJSON(previous)
	if err != nil {
		return fmt.Errorf("previous outbox payload: %w", err)
	}
	committedEventJSON, err := approvalTransactionSafeJSON(previous.CommittedEvent)
	if err != nil {
		return fmt.Errorf("committed event payload: %w", err)
	}
	nextJSON, err := approvalTransactionSafeJSON(next)
	if err != nil {
		return fmt.Errorf("published outbox payload: %w", err)
	}
	result, err := conn.ExecContext(approvalTransactionContext(ctx), `UPDATE approval_outbox SET
		delivery_state=?,published_at=?,failure_reason=?,outbox_json=?
		WHERE outbox_id=? AND event_id=? AND aggregate_id=? AND aggregate_version=? AND workspace_id=? AND payload_digest=? AND committed_at=? AND delivery_state=? AND published_at=? AND failure_reason=? AND committed_event_json=? AND outbox_json=?`,
		next.DeliveryState, next.PublishedAt, next.FailureReason, nextJSON,
		previous.OutboxID, previous.EventID, previous.AggregateID, previous.AggregateVersion, previous.WorkspaceID, previous.PayloadDigest, previous.CommittedAt,
		previous.DeliveryState, previous.PublishedAt, previous.FailureReason, committedEventJSON, previousJSON)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return errors.New("approval transaction outbox transition target is missing")
	}
	return nil
}

type approvalTransactionReadHook func(component string) error

func loadApprovalTransactionStateDB(ctx context.Context, db *sql.DB) (approvalTransactionState, error) {
	return loadApprovalTransactionStateDBWithReadHook(ctx, db, nil)
}

func loadApprovalTransactionStateDBReadOnly(ctx context.Context, db *sql.DB) (approvalTransactionState, error) {
	return loadApprovalTransactionStateDBReadOnlyWithConnCloser(ctx, db, nil)
}

func (s *ApprovalTransactionStore) loadApprovalTransactionStateDBReadOnly(ctx context.Context, db *sql.DB) (approvalTransactionState, error) {
	if s == nil {
		return loadApprovalTransactionStateDBReadOnlyWithConnCloser(ctx, db, nil)
	}
	return loadApprovalTransactionStateDBReadOnlyWithConnCloser(ctx, db, s.closeReadOnlyConnection)
}

func loadApprovalTransactionStateDBReadOnlyWithConnCloser(ctx context.Context, db *sql.DB, closeConnection func(*sql.Conn) error) (approvalTransactionState, error) {
	return loadApprovalTransactionStateDBWithReadHookModeAndConnCloser(ctx, db, nil, true, closeConnection)
}

func loadApprovalTransactionStateDBWithReadHook(ctx context.Context, db *sql.DB, readHook approvalTransactionReadHook) (approvalTransactionState, error) {
	return loadApprovalTransactionStateDBWithReadHookMode(ctx, db, readHook, false)
}

func loadApprovalTransactionStateDBWithReadHookMode(ctx context.Context, db *sql.DB, readHook approvalTransactionReadHook, readOnly bool) (approvalTransactionState, error) {
	return loadApprovalTransactionStateDBWithReadHookModeAndConnCloser(ctx, db, readHook, readOnly, nil)
}

func loadApprovalTransactionStateDBWithReadHookModeAndConnCloser(ctx context.Context, db *sql.DB, readHook approvalTransactionReadHook, readOnly bool, closeReadOnlyConnection func(*sql.Conn) error) (state approvalTransactionState, err error) {
	var zero approvalTransactionState
	if db == nil {
		return zero, approvalTransactionPersistence(nil)
	}
	ctx = approvalTransactionContext(ctx)
	conn, err := db.Conn(ctx)
	if err != nil {
		return zero, approvalTransactionPersistence(err)
	}
	if readOnly {
		if closeReadOnlyConnection == nil {
			closeReadOnlyConnection = defaultCloseApprovalTransactionReadOnlyConnection
		}
		defer func() {
			closeErr := closeReadOnlyConnection(conn)
			if closeErr == nil {
				return
			}
			state = zero
			cleanupErr := approvalTransactionPersistence(closeErr)
			if err == nil {
				err = cleanupErr
				return
			}
			err = errors.Join(err, cleanupErr)
		}()
	} else {
		defer conn.Close()
	}
	configure := configureApprovalTransactionConnection
	if readOnly {
		configure = configureApprovalTransactionReadOnlyConnection
	}
	if err := configure(ctx, conn); err != nil {
		return zero, approvalTransactionPersistence(err)
	}
	if _, err := conn.ExecContext(ctx, "BEGIN DEFERRED"); err != nil {
		return zero, approvalTransactionPersistence(err)
	}
	inTransaction := true
	rollback := func(primary error) error {
		if !inTransaction {
			return primary
		}
		_, rollbackErr := conn.ExecContext(context.Background(), "ROLLBACK")
		inTransaction = false
		if rollbackErr != nil {
			return approvalTransactionPersistence(errors.Join(primary, rollbackErr))
		}
		return primary
	}
	state, err = loadApprovalTransactionStateConnWithReadHook(ctx, conn, readHook)
	if err != nil {
		return zero, rollback(err)
	}
	if err := approvalTransactionContextError(ctx); err != nil {
		return zero, rollback(err)
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return zero, rollback(approvalTransactionPersistence(err))
	}
	inTransaction = false
	return state, nil
}

func loadApprovalTransactionStateConn(ctx context.Context, conn *sql.Conn) (approvalTransactionState, error) {
	return loadApprovalTransactionStateConnWithReadHook(ctx, conn, nil)
}

func approvalTransactionReadFailure(ctx context.Context, cause error) error {
	if err := approvalTransactionContextError(ctx); err != nil {
		return err
	}
	return approvalTransactionInvalid(cause)
}

func loadApprovalTransactionStateConnWithReadHook(ctx context.Context, conn *sql.Conn, readHook approvalTransactionReadHook) (approvalTransactionState, error) {
	var state approvalTransactionState
	state.StoreVersion = approvalTransactionStoreVersion
	if conn == nil {
		return state, approvalTransactionPersistence(nil)
	}
	ctx = approvalTransactionContext(ctx)
	if err := rejectOversizedApprovalTransactionRows(ctx, conn); err != nil {
		return state, approvalTransactionReadFailure(ctx, err)
	}
	rows, err := conn.QueryContext(ctx, `SELECT seq,schema_id,schema_version,event_id,approval_id,aggregate_id,aggregate_version,actor_id,session_id,workspace_id,proposal_id,evidence_pack_id,computed_basis_id,generation_id,intent_revision,decision,approved_text,timestamp,lifecycle_relation,predecessor_approval_id,payload_digest,payload_json FROM approval_events ORDER BY seq`)
	if err != nil {
		return state, approvalTransactionReadFailure(ctx, nil)
	}
	for rows.Next() {
		var seq int64
		var event ApprovalEventV2
		var payloadDigest string
		var payloadJSON []byte
		if err := rows.Scan(&seq, &event.SchemaID, &event.SchemaVersion, &event.EventID, &event.ApprovalID, &event.AggregateID, &event.AggregateVersion, &event.ActorID, &event.SessionID, &event.WorkspaceID, &event.ProposalID, &event.EvidencePackID, &event.ComputedBasisID, &event.GenerationID, &event.IntentRevision, &event.Decision, &event.ApprovedText, &event.Timestamp, &event.LifecycleRelation, &event.PredecessorApprovalID, &payloadDigest, &payloadJSON); err != nil {
			_ = rows.Close()
			return state, approvalTransactionReadFailure(ctx, nil)
		}
		if seq != int64(len(state.Events)+1) || len(payloadJSON) == 0 || len(payloadJSON) > approvalTransactionMaxBytes {
			_ = rows.Close()
			return state, approvalTransactionReadFailure(ctx, nil)
		}
		var payloadEvent ApprovalEventV2
		if decodeApprovalTransactionJSON(payloadJSON, &payloadEvent) != nil || !reflect.DeepEqual(event, payloadEvent) || validateApprovalLifecycleEvent(event) != nil {
			_ = rows.Close()
			return state, approvalTransactionReadFailure(ctx, nil)
		}
		wantPayload := approvalTransactionEventPayload(event)
		wantDigestBytes := sha256.Sum256(wantPayload)
		wantDigest := "sha256:" + hex.EncodeToString(wantDigestBytes[:])
		if len(wantPayload) == 0 || !bytes.Equal(wantPayload, payloadJSON) || payloadDigest != wantDigest {
			_ = rows.Close()
			return state, approvalTransactionReadFailure(ctx, nil)
		}
		state.Events = append(state.Events, event)
		if len(state.Events) > approvalTransactionMaxRecords {
			_ = rows.Close()
			return state, approvalTransactionInvalid(nil)
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return state, approvalTransactionReadFailure(ctx, nil)
	}
	if err := rows.Close(); err != nil {
		return state, approvalTransactionReadFailure(ctx, nil)
	}
	if readHook != nil {
		if err := readHook("events"); err != nil {
			return state, approvalTransactionReadFailure(ctx, err)
		}
	}

	targetRows, err := conn.QueryContext(ctx, `SELECT aggregate_id,workspace_id,proposal_id,evidence_pack_id,computed_basis_id,generation_id,intent_revision,validated_snapshot_id,workspace_epoch,map_id,task_id,target_digest,aggregate_version,state,genesis_event_id,aggregate_json FROM approval_targets ORDER BY aggregate_id`)
	if err != nil {
		return state, approvalTransactionReadFailure(ctx, nil)
	}
	for targetRows.Next() {
		var record approvalTransactionTargetRecord
		var aggregateID string
		var aggregateVersion, workspaceEpoch, intentRevision int64
		var stateName, genesisEventID string
		var aggregateJSON []byte
		if err := targetRows.Scan(&aggregateID, &record.Target.WorkspaceID, &record.Target.ProposalID, &record.Target.EvidencePackID, &record.Target.ComputedBasisID, &record.Target.GenerationID, &intentRevision, &record.Target.ValidatedSnapshotID, &workspaceEpoch, &record.Target.MapID, &record.Target.TaskID, &record.TargetDigest, &aggregateVersion, &stateName, &genesisEventID, &aggregateJSON); err != nil {
			_ = targetRows.Close()
			return state, approvalTransactionReadFailure(ctx, nil)
		}
		record.Target.IntentRevision = intentRevision
		record.Target.WorkspaceEpoch = workspaceEpoch
		record.GenesisEventID = genesisEventID
		if decodeApprovalTransactionJSON(aggregateJSON, &record.Aggregate) != nil || validateApprovalLifecycleAggregate(record.Aggregate) != nil || record.Aggregate.AggregateID != aggregateID || record.Aggregate.Version != aggregateVersion || record.Aggregate.State != stateName || len(record.Aggregate.History) == 0 || record.Aggregate.History[0].EventID != genesisEventID || !validApprovalTransactionTarget(record.Target) || !validApprovalTransactionTargetDigest(record.Target, record.TargetDigest) || !approvalTransactionTargetAggregateIdentityMatches(record.Target, record.Aggregate) || len(aggregateJSON) > approvalTransactionMaxBytes {
			_ = targetRows.Close()
			return state, approvalTransactionReadFailure(ctx, nil)
		}
		state.Targets = append(state.Targets, record)
		state.Aggregates = append(state.Aggregates, cloneApprovalAggregateV2(record.Aggregate))
		if len(state.Targets) > approvalTransactionMaxRecords {
			_ = targetRows.Close()
			return state, approvalTransactionReadFailure(ctx, nil)
		}
	}
	if err := targetRows.Err(); err != nil {
		_ = targetRows.Close()
		return state, approvalTransactionReadFailure(ctx, nil)
	}
	if err := targetRows.Close(); err != nil {
		return state, approvalTransactionReadFailure(ctx, nil)
	}
	if readHook != nil {
		if err := readHook("targets"); err != nil {
			return state, approvalTransactionReadFailure(ctx, err)
		}
	}

	var idempotencyRowCount, linkedIdempotencyRowCount int64
	if err := conn.QueryRowContext(ctx, "SELECT count(*) FROM approval_idempotency").Scan(&idempotencyRowCount); err != nil || idempotencyRowCount > approvalTransactionMaxRecords {
		return state, approvalTransactionReadFailure(ctx, nil)
	}
	if err := conn.QueryRowContext(ctx, "SELECT count(*) FROM approval_idempotency i JOIN approval_events e ON e.event_id=i.event_id").Scan(&linkedIdempotencyRowCount); err != nil || idempotencyRowCount != linkedIdempotencyRowCount {
		return state, approvalTransactionReadFailure(ctx, nil)
	}
	idempotencyRows, err := conn.QueryContext(ctx, `SELECT i.workspace_id,i.idempotency_key,i.command_id,i.actor_id,i.session_id,i.proposal_id,i.evidence_pack_id,i.computed_basis_id,i.generation_id,i.request_digest,i.outcome,i.approval_id,i.event_id,i.aggregate_id,i.aggregate_version,i.state,i.committed_at,i.command_json,i.result_json,e.decision FROM approval_idempotency i JOIN approval_events e ON e.event_id=i.event_id ORDER BY e.seq`)
	if err != nil {
		return state, approvalTransactionReadFailure(ctx, nil)
	}
	for idempotencyRows.Next() {
		var rowResult ApprovalIdempotencyResultV1
		var commandJSON, resultJSON []byte
		var eventDecision string
		if err := idempotencyRows.Scan(&rowResult.WorkspaceID, &rowResult.IdempotencyKey, &rowResult.CommandID, &rowResult.ActorID, &rowResult.SessionID, &rowResult.ProposalID, &rowResult.EvidencePackID, &rowResult.ComputedBasisID, &rowResult.GenerationID, &rowResult.RequestDigest, &rowResult.Outcome, &rowResult.ApprovalID, &rowResult.EventID, &rowResult.AggregateID, &rowResult.AggregateVersion, &rowResult.State, &rowResult.CommittedAt, &commandJSON, &resultJSON, &eventDecision); err != nil {
			_ = idempotencyRows.Close()
			return state, approvalTransactionReadFailure(ctx, nil)
		}
		rowResult.SchemaID = contractharness.ApprovalIdempotencyResultV1SchemaID
		rowResult.SchemaVersion = 1
		rowResult.OriginalCommand = ApprovalIdempotencyOriginalCommandV1{
			CommandID: rowResult.CommandID, IdempotencyKey: rowResult.IdempotencyKey, ActorID: rowResult.ActorID,
			WorkspaceID: rowResult.WorkspaceID, ProposalID: rowResult.ProposalID, Decision: eventDecision,
			ExpectedApprovalVersion: rowResult.AggregateVersion - 1,
		}
		rowResult.OriginalResult = ApprovalIdempotencyOriginalResultV1{
			Outcome: rowResult.Outcome, ApprovalID: rowResult.ApprovalID, EventID: rowResult.EventID,
			AggregateID: rowResult.AggregateID, AggregateVersion: rowResult.AggregateVersion, State: rowResult.State,
		}
		if len(commandJSON) == 0 || len(commandJSON) > approvalTransactionMaxBytes || len(resultJSON) == 0 || len(resultJSON) > approvalTransactionMaxBytes {
			_ = idempotencyRows.Close()
			return state, approvalTransactionReadFailure(ctx, nil)
		}
		var result ApprovalIdempotencyResultV1
		if decodeApprovalTransactionJSON(resultJSON, &result) != nil || !reflect.DeepEqual(rowResult, result) || validateApprovalTransactionResult(result) != nil {
			_ = idempotencyRows.Close()
			return state, approvalTransactionReadFailure(ctx, nil)
		}
		var command ApprovalCommandV2
		if decodeApprovalTransactionJSON(commandJSON, &command) != nil || validateApprovalCommandValue(command) != nil {
			_ = idempotencyRows.Close()
			return state, approvalTransactionReadFailure(ctx, nil)
		}
		commandDigest, digestErr := approvalCommandIdempotencyDigest(command)
		if digestErr != nil || result.RequestDigest != "sha256:"+hex.EncodeToString(commandDigest[:]) || result.CommandID != command.CommandID || result.IdempotencyKey != command.IdempotencyKey || result.ActorID != command.ActorID || result.SessionID != command.SessionID || result.WorkspaceID != command.WorkspaceID || result.ProposalID != command.ProposalID || result.EvidencePackID != command.EvidencePackID || result.ComputedBasisID != command.ComputedBasisID || result.GenerationID != command.GenerationID || result.OriginalCommand != approvalTransactionOriginalCommand(command) {
			_ = idempotencyRows.Close()
			return state, approvalTransactionReadFailure(ctx, nil)
		}
		state.IdempotencyResults = append(state.IdempotencyResults, result)
	}
	if err := idempotencyRows.Err(); err != nil {
		_ = idempotencyRows.Close()
		return state, approvalTransactionReadFailure(ctx, nil)
	}
	if err := idempotencyRows.Close(); err != nil {
		return state, approvalTransactionReadFailure(ctx, nil)
	}
	if readHook != nil {
		if err := readHook("idempotency"); err != nil {
			return state, approvalTransactionReadFailure(ctx, err)
		}
	}

	var outboxRowCount, linkedOutboxRowCount int64
	if err := conn.QueryRowContext(ctx, "SELECT count(*) FROM approval_outbox").Scan(&outboxRowCount); err != nil || outboxRowCount > approvalTransactionMaxRecords {
		return state, approvalTransactionReadFailure(ctx, nil)
	}
	if err := conn.QueryRowContext(ctx, "SELECT count(*) FROM approval_outbox o JOIN approval_events e ON e.event_id=o.event_id").Scan(&linkedOutboxRowCount); err != nil || outboxRowCount != linkedOutboxRowCount {
		return state, approvalTransactionReadFailure(ctx, nil)
	}
	outboxRows, err := conn.QueryContext(ctx, `SELECT o.outbox_id,o.event_id,o.aggregate_id,o.aggregate_version,o.workspace_id,o.payload_digest,o.committed_at,o.delivery_state,o.published_at,o.failure_reason,o.committed_event_json,o.outbox_json,e.approval_id,e.decision FROM approval_outbox o JOIN approval_events e ON e.event_id=o.event_id ORDER BY e.seq`)
	if err != nil {
		return state, approvalTransactionReadFailure(ctx, nil)
	}
	for outboxRows.Next() {
		var rowOutbox ApprovalOutboxV1
		var committedEventJSON, outboxJSON []byte
		var eventApprovalID, eventDecision string
		if err := outboxRows.Scan(&rowOutbox.OutboxID, &rowOutbox.EventID, &rowOutbox.AggregateID, &rowOutbox.AggregateVersion, &rowOutbox.WorkspaceID, &rowOutbox.PayloadDigest, &rowOutbox.CommittedAt, &rowOutbox.DeliveryState, &rowOutbox.PublishedAt, &rowOutbox.FailureReason, &committedEventJSON, &outboxJSON, &eventApprovalID, &eventDecision); err != nil {
			_ = outboxRows.Close()
			return state, approvalTransactionReadFailure(ctx, nil)
		}
		rowOutbox.SchemaID = contractharness.ApprovalOutboxV1SchemaID
		rowOutbox.SchemaVersion = 1
		rowOutbox.CommittedEvent = ApprovalOutboxCommittedEventV1{
			EventID: rowOutbox.EventID, ApprovalID: eventApprovalID, AggregateID: rowOutbox.AggregateID,
			AggregateVersion: rowOutbox.AggregateVersion, WorkspaceID: rowOutbox.WorkspaceID, Decision: eventDecision,
			PayloadDigest: rowOutbox.PayloadDigest,
		}
		var outbox ApprovalOutboxV1
		var committedEvent ApprovalOutboxCommittedEventV1
		if len(committedEventJSON) == 0 || len(committedEventJSON) > approvalTransactionMaxBytes || len(outboxJSON) == 0 || len(outboxJSON) > approvalTransactionMaxBytes || decodeApprovalTransactionJSON(outboxJSON, &outbox) != nil || !reflect.DeepEqual(rowOutbox, outbox) || decodeApprovalTransactionJSON(committedEventJSON, &committedEvent) != nil || !reflect.DeepEqual(outbox.CommittedEvent, committedEvent) || validateApprovalTransactionOutbox(outbox) != nil {
			_ = outboxRows.Close()
			return state, approvalTransactionReadFailure(ctx, nil)
		}
		state.Outbox = append(state.Outbox, outbox)
	}
	if err := outboxRows.Err(); err != nil {
		_ = outboxRows.Close()
		return state, approvalTransactionReadFailure(ctx, nil)
	}
	if err := outboxRows.Close(); err != nil {
		return state, approvalTransactionReadFailure(ctx, nil)
	}
	if readHook != nil {
		if err := readHook("outbox"); err != nil {
			return state, approvalTransactionReadFailure(ctx, err)
		}
	}
	if err := validateApprovalTransactionState(state); err != nil {
		return state, approvalTransactionReadFailure(ctx, fmt.Errorf("state validation: %w", err))
	}
	if err := replayApprovalTransactionState(state); err != nil {
		return state, approvalTransactionReadFailure(ctx, fmt.Errorf("event replay: %w", err))
	}
	return state, nil
}

func approvalTransactionOriginalCommand(command ApprovalCommandV2) ApprovalIdempotencyOriginalCommandV1 {
	return ApprovalIdempotencyOriginalCommandV1{
		CommandID: command.CommandID, IdempotencyKey: command.IdempotencyKey, ActorID: command.ActorID,
		WorkspaceID: command.WorkspaceID, ProposalID: command.ProposalID, Decision: command.Decision,
		ExpectedApprovalVersion: command.ExpectedApprovalVersion,
	}
}

func validateApprovalTransactionResult(result ApprovalIdempotencyResultV1) error {
	data, err := json.Marshal(result)
	if err != nil || contractharness.ValidateVS09Contract(contractharness.ApprovalIdempotencyResultV1SchemaID, data) != nil {
		return errors.New("approval transaction idempotency result is invalid")
	}
	for _, value := range []string{result.IdempotencyKey, result.CommandID, result.ActorID, result.SessionID, result.WorkspaceID, result.ProposalID, result.EvidencePackID, result.ComputedBasisID, result.GenerationID, result.ApprovalID, result.EventID, result.AggregateID} {
		if !validApprovalLifecycleID(value) {
			return errors.New("approval transaction idempotency identity is invalid")
		}
	}
	expectedOriginalCommand := ApprovalIdempotencyOriginalCommandV1{
		CommandID: result.CommandID, IdempotencyKey: result.IdempotencyKey, ActorID: result.ActorID,
		WorkspaceID: result.WorkspaceID, ProposalID: result.ProposalID, Decision: result.OriginalCommand.Decision,
		ExpectedApprovalVersion: result.OriginalCommand.ExpectedApprovalVersion,
	}
	if !validApprovalLifecycleID(result.OriginalCommand.CommandID) || !validApprovalLifecycleID(result.OriginalCommand.IdempotencyKey) || !validApprovalLifecycleID(result.OriginalCommand.ActorID) || !validApprovalLifecycleID(result.OriginalCommand.WorkspaceID) || !validApprovalLifecycleID(result.OriginalCommand.ProposalID) || result.OriginalCommand.Decision == "" || result.OriginalCommand.ExpectedApprovalVersion < 0 || result.OriginalCommand.ExpectedApprovalVersion > approvalLifecycleMaxVersion || result.OriginalCommand.ExpectedApprovalVersion+1 != result.AggregateVersion || result.Outcome != "committed" || result.OriginalResult != (ApprovalIdempotencyOriginalResultV1{
		Outcome: "committed", ApprovalID: result.ApprovalID, EventID: result.EventID, AggregateID: result.AggregateID,
		AggregateVersion: result.AggregateVersion, State: result.State,
	}) || result.OriginalCommand != expectedOriginalCommand || !validApprovalTransactionDigestString(result.RequestDigest) || !validApprovalLifecycleTimestamp(result.CommittedAt) {
		return errors.New("approval transaction idempotency result identity is invalid")
	}
	if _, ok := map[string]struct{}{"approve": {}, "edit_then_approve": {}, "reject": {}, "revoke": {}, "supersede": {}}[result.OriginalCommand.Decision]; !ok {
		return errors.New("approval transaction idempotency result transition is invalid")
	}
	return nil
}

func validateApprovalTransactionOutbox(outbox ApprovalOutboxV1) error {
	data, err := json.Marshal(outbox)
	if err != nil || contractharness.ValidateVS09Contract(contractharness.ApprovalOutboxV1SchemaID, data) != nil {
		return errors.New("approval transaction outbox is invalid")
	}
	for _, value := range []string{outbox.OutboxID, outbox.EventID, outbox.AggregateID, outbox.WorkspaceID, outbox.CommittedEvent.EventID, outbox.CommittedEvent.ApprovalID, outbox.CommittedEvent.AggregateID} {
		if !validApprovalLifecycleID(value) {
			return errors.New("approval transaction outbox identity is invalid")
		}
	}
	if (outbox.DeliveryState != "pending" && outbox.DeliveryState != "published") || outbox.FailureReason != "" || !validApprovalTransactionDigestString(outbox.PayloadDigest) || !validApprovalTransactionDigestString(outbox.CommittedEvent.PayloadDigest) || !validApprovalLifecycleTimestamp(outbox.CommittedAt) || outbox.CommittedEvent.AggregateVersion != outbox.AggregateVersion || outbox.CommittedEvent.WorkspaceID != outbox.WorkspaceID || outbox.CommittedEvent.PayloadDigest != outbox.PayloadDigest {
		return errors.New("approval transaction outbox identity is invalid")
	}
	if outbox.DeliveryState == "pending" && outbox.PublishedAt != "" {
		return errors.New("approval transaction outbox transition is invalid")
	}
	if outbox.DeliveryState == "published" && (!validApprovalTransactionPublishedAt(outbox.PublishedAt) || !approvalTransactionPublishedAtNotBeforeCommittedAt(outbox.PublishedAt, outbox.CommittedAt)) {
		return errors.New("approval transaction outbox identity is invalid")
	}
	return nil
}

func validApprovalTransactionPublishedAt(value string) bool {
	if value == "" || len(value) > 128 || !utf8.ValidString(value) {
		return false
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	return err == nil && parsed.UTC().Format(time.RFC3339Nano) == value
}

func approvalTransactionPublishedAtNotBeforeCommittedAt(publishedAt, committedAt string) bool {
	published, publishedErr := time.Parse(time.RFC3339Nano, publishedAt)
	committed, committedErr := time.Parse(time.RFC3339Nano, committedAt)
	return publishedErr == nil && committedErr == nil && !published.Before(committed)
}

func validApprovalTransactionDigestString(value string) bool {
	if len(value) != len("sha256:")+sha256.Size*2 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	for _, char := range value[len("sha256:"):] {
		if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f')) {
			return false
		}
	}
	return true
}

// replayApprovalTransactionState derives every projection from the immutable
// event rows. Stored aggregate JSON is compared by the caller through the
// target record, but is never used as the transition authority here.
func replayApprovalTransactionState(state approvalTransactionState) error {
	if len(state.Aggregates) != len(state.Targets) {
		return approvalTransactionInvalid(nil)
	}
	for _, outbox := range state.Outbox {
		if validateApprovalTransactionOutbox(outbox) != nil {
			return approvalTransactionInvalid(nil)
		}
	}
	eventsByAggregate := make(map[string][]ApprovalEventV2, len(state.Aggregates))
	for _, event := range state.Events {
		eventsByAggregate[event.AggregateID] = append(eventsByAggregate[event.AggregateID], event)
	}
	seenEvents := make(map[string]struct{}, len(state.Events))
	seenGenesis := make(map[string]struct{}, len(state.Aggregates))
	for index, aggregate := range state.Aggregates {
		target := state.Targets[index]
		if target.Aggregate.AggregateID != aggregate.AggregateID || aggregate.Version < 1 || len(aggregate.History) < 2 || target.GenesisEventID != aggregate.History[0].EventID {
			return approvalTransactionInvalid(nil)
		}
		aggregateEvents := eventsByAggregate[aggregate.AggregateID]
		if len(aggregateEvents) != len(aggregate.History)-1 || aggregate.Version != int64(len(aggregateEvents)) {
			return approvalTransactionInvalid(nil)
		}
		if _, duplicate := seenGenesis[target.GenesisEventID]; duplicate {
			return approvalTransactionInvalid(nil)
		}
		seenGenesis[target.GenesisEventID] = struct{}{}
		if aggregate.History[0].Version != 0 || aggregate.History[0].State != "none" || aggregate.History[0].Decision != "none" || aggregate.History[0].ApprovalID != "" {
			return approvalTransactionInvalid(nil)
		}
		currentState := "none"
		currentActive := ""
		for eventIndex, event := range aggregateEvents {
			historyIndex := eventIndex + 1
			entry := aggregate.History[historyIndex]
			if entry.Version != int64(historyIndex) || event.AggregateVersion != int64(historyIndex) || event.EventID != entry.EventID || event.Decision != entry.Decision || event.ApprovalID != entry.ApprovalID || event.LifecycleRelation == "" || event.WorkspaceID != aggregate.WorkspaceID || event.ProposalID != aggregate.ProposalID || event.EvidencePackID != aggregate.EvidencePackID || event.ComputedBasisID != aggregate.ComputedBasisID || event.GenerationID != aggregate.GenerationID || event.IntentRevision != aggregate.IntentRevision {
				return approvalTransactionInvalid(nil)
			}
			if _, duplicate := seenEvents[event.EventID]; duplicate {
				return approvalTransactionInvalid(nil)
			}
			seenEvents[event.EventID] = struct{}{}
			wantState := ""
			switch event.Decision {
			case "approve":
				if currentState != "none" || event.LifecycleRelation != "initial" || event.PredecessorApprovalID != "" || event.ApprovedText == "" {
					return approvalTransactionInvalid(nil)
				}
				wantState, currentActive = "active", event.ApprovalID
			case "edit_then_approve":
				if currentState != "active" || event.LifecycleRelation != "edit" || event.PredecessorApprovalID != currentActive || event.ApprovedText == "" {
					return approvalTransactionInvalid(nil)
				}
				wantState, currentActive = "active", event.ApprovalID
			case "reject":
				if (currentState != "none" && currentState != "active") || event.LifecycleRelation != "reject" || event.ApprovedText != "" || (currentState == "none" && event.PredecessorApprovalID != "") || (currentState == "active" && event.PredecessorApprovalID != "" && event.PredecessorApprovalID != currentActive) {
					return approvalTransactionInvalid(nil)
				}
				wantState, currentActive = "rejected", ""
			case "revoke":
				if currentState != "active" || event.LifecycleRelation != "revoke" || event.PredecessorApprovalID != currentActive || event.ApprovedText != "" {
					return approvalTransactionInvalid(nil)
				}
				wantState, currentActive = "revoked", ""
			case "supersede":
				if currentState != "active" || event.LifecycleRelation != "supersede" || event.PredecessorApprovalID != currentActive || event.ApprovedText != "" {
					return approvalTransactionInvalid(nil)
				}
				wantState, currentActive = "superseded", ""
			default:
				return approvalTransactionInvalid(nil)
			}
			if entry.State != wantState {
				return approvalTransactionInvalid(nil)
			}
			currentState = wantState
		}
		if aggregate.Version != int64(len(aggregate.History)-1) || aggregate.State != currentState || aggregate.LastDecision != aggregate.History[len(aggregate.History)-1].Decision || aggregate.LastEventID != aggregate.History[len(aggregate.History)-1].EventID || aggregate.ActiveApprovalID != currentActive {
			return approvalTransactionInvalid(nil)
		}
	}
	if len(seenEvents) != len(state.Events) {
		return approvalTransactionInvalid(nil)
	}
	return nil
}

func (s *ApprovalTransactionStore) ensureRootLocked(create bool) error {
	if s == nil {
		return errors.New("nil approval transaction store")
	}
	if err := validateApprovalTransactionRoot(s.root); err != nil {
		return err
	}
	info, err := os.Lstat(s.root)
	created := false
	if errors.Is(err, os.ErrNotExist) {
		if !create {
			return os.ErrNotExist
		}
		if err := os.MkdirAll(s.root, 0o700); err != nil {
			return err
		}
		created = true
		info, err = os.Lstat(s.root)
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("managed approval transaction root is not a directory")
	}
	if err := os.Chmod(s.root, 0o700); err != nil {
		return err
	}
	info, err = os.Lstat(s.root)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() || info.Mode().Perm() != 0o700 {
		return errors.New("managed approval transaction root is not owner-only")
	}
	if created {
		if err := syncProposalDirectory(filepath.Dir(s.root)); err != nil {
			return err
		}
		if err := syncProposalDirectory(s.root); err != nil {
			return err
		}
	}
	return nil
}

func (s *ApprovalTransactionStore) validateApprovalTransactionRootReadOnlyLocked() error {
	if s == nil {
		return errors.New("nil approval transaction store")
	}
	if err := validateApprovalTransactionRoot(s.root); err != nil {
		return err
	}
	info, err := os.Lstat(s.root)
	if errors.Is(err, os.ErrNotExist) {
		return os.ErrNotExist
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("managed approval transaction root is not a directory")
	}
	if info.Mode().Perm() != 0o700 {
		return errors.New("managed approval transaction root is not owner-only")
	}
	return nil
}

func validateApprovalTransactionRoot(root string) error {
	if strings.TrimSpace(root) == "" || filepath.Clean(root) != root {
		return errors.New("managed approval transaction root is invalid")
	}
	for current := root; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				return errors.New("managed approval transaction path contains a symlink")
			}
			if !info.IsDir() {
				return errors.New("managed approval transaction path is not a directory")
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return nil
		}
	}
}

func decodeApprovalTransactionJSON(data []byte, value any) error {
	if len(data) == 0 || len(data) > approvalTransactionMaxBytes {
		return errors.New("approval transaction JSON is invalid")
	}
	if !utf8.Valid(data) {
		return errors.New("approval transaction JSON encoding is invalid")
	}
	_, redactionCount, err := secret.RedactJSON(data)
	if err != nil || redactionCount != 0 {
		return errors.New("approval transaction JSON is not persistable")
	}
	if err := rejectDuplicateJSONKeys(data); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("approval transaction JSON has trailing data")
		}
		return err
	}
	return nil
}

func rejectOversizedApprovalTransactionRows(ctx context.Context, conn *sql.Conn) error {
	if conn == nil {
		return errors.New("approval transaction database connection is unavailable")
	}
	// Check every persisted text/blob column with SQLite's length() before any
	// row is scanned into a Go value. This keeps malformed values bounded even
	// when a test or external tool bypasses the table CHECK constraints.
	columns := []struct {
		table  string
		column string
	}{
		{table: "approval_targets", column: "aggregate_id"},
		{table: "approval_targets", column: "workspace_id"},
		{table: "approval_targets", column: "proposal_id"},
		{table: "approval_targets", column: "evidence_pack_id"},
		{table: "approval_targets", column: "computed_basis_id"},
		{table: "approval_targets", column: "generation_id"},
		{table: "approval_targets", column: "validated_snapshot_id"},
		{table: "approval_targets", column: "map_id"},
		{table: "approval_targets", column: "task_id"},
		{table: "approval_targets", column: "target_digest"},
		{table: "approval_targets", column: "state"},
		{table: "approval_targets", column: "genesis_event_id"},
		{table: "approval_targets", column: "aggregate_json"},
		{table: "approval_events", column: "schema_id"},
		{table: "approval_events", column: "event_id"},
		{table: "approval_events", column: "approval_id"},
		{table: "approval_events", column: "aggregate_id"},
		{table: "approval_events", column: "actor_id"},
		{table: "approval_events", column: "session_id"},
		{table: "approval_events", column: "workspace_id"},
		{table: "approval_events", column: "proposal_id"},
		{table: "approval_events", column: "evidence_pack_id"},
		{table: "approval_events", column: "computed_basis_id"},
		{table: "approval_events", column: "generation_id"},
		{table: "approval_events", column: "decision"},
		{table: "approval_events", column: "approved_text"},
		{table: "approval_events", column: "timestamp"},
		{table: "approval_events", column: "lifecycle_relation"},
		{table: "approval_events", column: "predecessor_approval_id"},
		{table: "approval_events", column: "payload_digest"},
		{table: "approval_events", column: "payload_json"},
		{table: "approval_idempotency", column: "workspace_id"},
		{table: "approval_idempotency", column: "idempotency_key"},
		{table: "approval_idempotency", column: "command_id"},
		{table: "approval_idempotency", column: "actor_id"},
		{table: "approval_idempotency", column: "session_id"},
		{table: "approval_idempotency", column: "proposal_id"},
		{table: "approval_idempotency", column: "evidence_pack_id"},
		{table: "approval_idempotency", column: "computed_basis_id"},
		{table: "approval_idempotency", column: "generation_id"},
		{table: "approval_idempotency", column: "request_digest"},
		{table: "approval_idempotency", column: "outcome"},
		{table: "approval_idempotency", column: "approval_id"},
		{table: "approval_idempotency", column: "event_id"},
		{table: "approval_idempotency", column: "aggregate_id"},
		{table: "approval_idempotency", column: "state"},
		{table: "approval_idempotency", column: "committed_at"},
		{table: "approval_idempotency", column: "command_json"},
		{table: "approval_idempotency", column: "result_json"},
		{table: "approval_outbox", column: "outbox_id"},
		{table: "approval_outbox", column: "event_id"},
		{table: "approval_outbox", column: "aggregate_id"},
		{table: "approval_outbox", column: "workspace_id"},
		{table: "approval_outbox", column: "payload_digest"},
		{table: "approval_outbox", column: "committed_at"},
		{table: "approval_outbox", column: "delivery_state"},
		{table: "approval_outbox", column: "published_at"},
		{table: "approval_outbox", column: "failure_reason"},
		{table: "approval_outbox", column: "committed_event_json"},
		{table: "approval_outbox", column: "outbox_json"},
	}
	ctx = approvalTransactionContext(ctx)
	for _, entry := range columns {
		query := "SELECT count(*) FROM " + entry.table + " WHERE " + entry.column + " IS NULL OR length(CAST(" + entry.column + " AS BLOB)) > ?"
		var count int64
		if err := conn.QueryRowContext(ctx, query, approvalTransactionMaxBytes).Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			return errors.New("approval transaction persisted value exceeds bound")
		}
	}
	return nil
}

func validateApprovalTransactionState(state approvalTransactionState) error {
	if state.StoreVersion != approvalTransactionStoreVersion || len(state.Events) > approvalTransactionMaxRecords || len(state.Aggregates) > approvalTransactionMaxRecords || len(state.IdempotencyResults) > approvalTransactionMaxRecords || len(state.Outbox) > approvalTransactionMaxRecords || len(state.Targets) > approvalTransactionMaxRecords {
		return approvalTransactionInvalid(nil)
	}
	if len(state.Aggregates) != len(state.Targets) {
		return approvalTransactionInvalid(nil)
	}
	seenEvents := map[string]struct{}{}
	seenApprovals := map[string]struct{}{}
	eventsByID := make(map[string]ApprovalEventV2, len(state.Events))
	for _, event := range state.Events {
		if err := validateApprovalLifecycleEvent(event); err != nil {
			return approvalTransactionInvalid(nil)
		}
		if _, ok := seenEvents[event.EventID]; ok {
			return approvalTransactionInvalid(nil)
		}
		seenEvents[event.EventID] = struct{}{}
		eventsByID[event.EventID] = event
		if _, ok := seenApprovals[event.ApprovalID]; ok {
			return approvalTransactionInvalid(nil)
		}
		seenApprovals[event.ApprovalID] = struct{}{}
	}
	seenAggregates := map[string]struct{}{}
	aggregatesByID := make(map[string]ApprovalAggregateV2, len(state.Aggregates))
	seenTargetCores := make(map[string]struct{}, len(state.Targets))
	seenHistoryEvents := map[string]struct{}{}
	seenHistoryApprovals := map[string]struct{}{}
	historyEventCounts := make(map[string]int, len(state.Events))
	for index, aggregate := range state.Aggregates {
		if err := validateApprovalLifecycleAggregate(aggregate); err != nil {
			return approvalTransactionInvalid(nil)
		}
		if _, ok := seenAggregates[aggregate.AggregateID]; ok {
			return approvalTransactionInvalid(nil)
		}
		seenAggregates[aggregate.AggregateID] = struct{}{}
		aggregatesByID[aggregate.AggregateID] = aggregate
		target := state.Targets[index]
		if !validApprovalTransactionTarget(target.Target) || !validApprovalTransactionTargetDigest(target.Target, target.TargetDigest) || !approvalTransactionTargetAggregateIdentityMatches(target.Target, aggregate) || !reflect.DeepEqual(target.Aggregate, aggregate) {
			return approvalTransactionInvalid(nil)
		}
		coreKey := approvalTransactionTargetCoreKey(target.Target)
		if _, duplicate := seenTargetCores[coreKey]; duplicate {
			return approvalTransactionInvalid(nil)
		}
		seenTargetCores[coreKey] = struct{}{}
		for historyIndex, entry := range aggregate.History {
			if historyIndex == 0 {
				if _, isEvent := eventsByID[entry.EventID]; isEvent {
					return approvalTransactionInvalid(nil)
				}
				continue
			}
			if _, duplicate := seenHistoryEvents[entry.EventID]; duplicate {
				return approvalTransactionInvalid(nil)
			}
			seenHistoryEvents[entry.EventID] = struct{}{}
			if _, duplicate := seenHistoryApprovals[entry.ApprovalID]; duplicate {
				return approvalTransactionInvalid(nil)
			}
			seenHistoryApprovals[entry.ApprovalID] = struct{}{}
			if _, isEvent := eventsByID[entry.EventID]; !isEvent {
				return approvalTransactionInvalid(nil)
			}
			historyEventCounts[entry.EventID]++
		}
	}
	seenKeys := map[string]struct{}{}
	idempotencyByEvent := make(map[string]int, len(state.IdempotencyResults))
	for _, result := range state.IdempotencyResults {
		if result.Outcome != "committed" || result.OriginalResult.Outcome != "committed" {
			return approvalTransactionInvalid(nil)
		}
		data, err := json.Marshal(result)
		if err != nil || contractharness.ValidateVS09Contract(contractharness.ApprovalIdempotencyResultV1SchemaID, data) != nil {
			return approvalTransactionInvalid(nil)
		}
		key := result.WorkspaceID + "\x00" + result.IdempotencyKey
		if _, ok := seenKeys[key]; ok {
			return approvalTransactionInvalid(nil)
		}
		seenKeys[key] = struct{}{}
		event, ok := eventsByID[result.EventID]
		if !ok {
			return approvalTransactionInvalid(nil)
		}
		aggregate, ok := aggregatesByID[result.AggregateID]
		if !ok {
			return approvalTransactionInvalid(nil)
		}
		idempotencyByEvent[result.EventID]++
		eventState, stateOK := approvalTransactionAggregateStateForEvent(aggregate, event.EventID)
		if result.ActorID != event.ActorID || result.SessionID != event.SessionID || result.WorkspaceID != event.WorkspaceID || result.ProposalID != event.ProposalID || result.EvidencePackID != event.EvidencePackID || result.ComputedBasisID != event.ComputedBasisID || result.GenerationID != event.GenerationID || result.ApprovalID != event.ApprovalID || result.EventID != event.EventID || result.AggregateID != event.AggregateID || result.AggregateVersion != event.AggregateVersion || !stateOK || result.State != eventState || result.AggregateVersion > aggregate.Version || result.CommittedAt != event.Timestamp || result.OriginalCommand.Decision != event.Decision || result.OriginalCommand.ExpectedApprovalVersion+1 != event.AggregateVersion {
			return approvalTransactionInvalid(nil)
		}
	}
	seenOutbox := map[string]struct{}{}
	outboxByEvent := make(map[string]int, len(state.Outbox))
	for _, outbox := range state.Outbox {
		if validateApprovalTransactionOutbox(outbox) != nil || outbox.OutboxID != approvalTransactionOutboxID(outbox.EventID) {
			return approvalTransactionInvalid(nil)
		}
		data, err := json.Marshal(outbox)
		if err != nil || contractharness.ValidateVS09Contract(contractharness.ApprovalOutboxV1SchemaID, data) != nil {
			return approvalTransactionInvalid(nil)
		}
		if _, ok := seenOutbox[outbox.OutboxID]; ok {
			return approvalTransactionInvalid(nil)
		}
		seenOutbox[outbox.OutboxID] = struct{}{}
		event, ok := eventsByID[outbox.EventID]
		if !ok {
			return approvalTransactionInvalid(nil)
		}
		outboxByEvent[outbox.EventID]++
		payload := approvalTransactionEventPayload(event)
		payloadDigest := sha256.Sum256(payload)
		wantPayloadDigest := "sha256:" + hex.EncodeToString(payloadDigest[:])
		if outbox.EventID != event.EventID || outbox.AggregateID != event.AggregateID || outbox.AggregateVersion != event.AggregateVersion || outbox.WorkspaceID != event.WorkspaceID || outbox.CommittedAt != event.Timestamp || outbox.PayloadDigest != wantPayloadDigest || outbox.CommittedEvent.EventID != event.EventID || outbox.CommittedEvent.ApprovalID != event.ApprovalID || outbox.CommittedEvent.AggregateID != event.AggregateID || outbox.CommittedEvent.AggregateVersion != event.AggregateVersion || outbox.CommittedEvent.WorkspaceID != event.WorkspaceID || outbox.CommittedEvent.Decision != event.Decision || outbox.CommittedEvent.PayloadDigest != wantPayloadDigest {
			return approvalTransactionInvalid(nil)
		}
	}
	if len(state.IdempotencyResults) != len(state.Events) || len(state.Outbox) != len(state.Events) {
		return approvalTransactionInvalid(nil)
	}
	for index, event := range state.Events {
		if state.IdempotencyResults[index].EventID != event.EventID || state.Outbox[index].EventID != event.EventID {
			return approvalTransactionInvalid(nil)
		}
	}
	for eventID, event := range eventsByID {
		if historyEventCounts[eventID] != 1 || idempotencyByEvent[eventID] != 1 || outboxByEvent[eventID] != 1 {
			return approvalTransactionInvalid(nil)
		}
		for _, aggregate := range state.Aggregates {
			for index, entry := range aggregate.History {
				if index == 0 || entry.EventID != eventID {
					continue
				}
				if event.AggregateID != aggregate.AggregateID || event.AggregateVersion != entry.Version || event.ApprovalID != entry.ApprovalID || event.Decision != entry.Decision || event.WorkspaceID != aggregate.WorkspaceID || !approvalTransactionEventPredecessorMatches(aggregate.History, index, event) {
					return approvalTransactionInvalid(nil)
				}
			}
		}
	}
	return nil
}

func approvalTransactionEventPredecessorMatches(history []ApprovalHistoryEntry, index int, event ApprovalEventV2) bool {
	if index <= 0 || index >= len(history) {
		return false
	}
	previous := history[index-1]
	switch event.Decision {
	case "approve":
		return previous.State == "none" && event.PredecessorApprovalID == ""
	case "edit_then_approve", "revoke", "supersede":
		return previous.State == "active" && event.PredecessorApprovalID == previous.ApprovalID
	case "reject":
		if previous.State == "none" {
			return event.PredecessorApprovalID == ""
		}
		return previous.State == "active" && (event.PredecessorApprovalID == "" || event.PredecessorApprovalID == previous.ApprovalID)
	default:
		return false
	}
}

func approvalTransactionAggregateStateForEvent(aggregate ApprovalAggregateV2, eventID string) (string, bool) {
	for _, entry := range aggregate.History {
		if entry.EventID == eventID {
			return entry.State, true
		}
	}
	return "", false
}

func approvalTransactionOutboxID(eventID string) string {
	digest := sha256.Sum256(append([]byte("codeflow/approval-outbox/v1\x00"), []byte(eventID)...))
	return "outbox-" + hex.EncodeToString(digest[:16])
}

func approvalTransactionTargetAggregateIdentityMatches(target approvalTransactionTarget, aggregate ApprovalAggregateV2) bool {
	return target.WorkspaceID == aggregate.WorkspaceID && target.ProposalID == aggregate.ProposalID && target.EvidencePackID == aggregate.EvidencePackID && target.ComputedBasisID == aggregate.ComputedBasisID && target.GenerationID == aggregate.GenerationID && target.IntentRevision == aggregate.IntentRevision
}

func findApprovalTransactionTarget(state approvalTransactionState, target approvalTransactionTarget) (*approvalTransactionTargetRecord, int) {
	for index := range state.Targets {
		if approvalTransactionTargetCoreEqual(state.Targets[index].Target, target) {
			copy := state.Targets[index]
			copy.Aggregate = cloneApprovalAggregateV2(copy.Aggregate)
			return &copy, index
		}
	}
	return nil, -1
}

func approvalTransactionTargetCoreEqual(left, right approvalTransactionTarget) bool {
	return left.WorkspaceID == right.WorkspaceID && left.ProposalID == right.ProposalID && left.EvidencePackID == right.EvidencePackID && left.ComputedBasisID == right.ComputedBasisID && left.GenerationID == right.GenerationID && left.IntentRevision == right.IntentRevision
}

func approvalTransactionTargetCoreKey(target approvalTransactionTarget) string {
	return strings.Join([]string{target.WorkspaceID, target.ProposalID, target.EvidencePackID, target.ComputedBasisID, target.GenerationID, fmt.Sprintf("%d", target.IntentRevision)}, "\x00")
}

func findApprovalTransactionAggregate(state approvalTransactionState, aggregateID string) (ApprovalAggregateV2, bool) {
	for _, aggregate := range state.Aggregates {
		if aggregate.AggregateID == aggregateID {
			return cloneApprovalAggregateV2(aggregate), true
		}
	}
	return ApprovalAggregateV2{}, false
}

func findApprovalTransactionIdempotency(state approvalTransactionState, key, workspace string) (ApprovalIdempotencyResultV1, bool) {
	for _, result := range state.IdempotencyResults {
		if result.IdempotencyKey == key && result.WorkspaceID == workspace {
			return cloneApprovalIdempotencyResult(result), true
		}
	}
	return ApprovalIdempotencyResultV1{}, false
}

func receiptFromApprovalTransactionState(state approvalTransactionState, result ApprovalIdempotencyResultV1, replayed bool) ApprovalCommitReceipt {
	var event ApprovalEventV2
	for _, candidate := range state.Events {
		if candidate.EventID == result.EventID {
			event = candidate
			break
		}
	}
	var aggregate ApprovalAggregateV2
	for _, candidate := range state.Aggregates {
		if candidate.AggregateID == result.AggregateID {
			aggregate = approvalTransactionAggregateAtVersion(candidate, result.AggregateVersion)
			break
		}
	}
	var outbox ApprovalOutboxV1
	for _, candidate := range state.Outbox {
		if candidate.EventID == result.EventID {
			outbox = cloneApprovalOutbox(candidate)
			break
		}
	}
	payload := approvalTransactionEventPayload(event)
	return ApprovalCommitReceipt{IdempotencyResult: cloneApprovalIdempotencyResult(result), Event: event, Aggregate: aggregate, Outbox: outbox, EventPayload: append([]byte(nil), payload...), Replayed: replayed}
}

func approvalTransactionAggregateAtVersion(aggregate ApprovalAggregateV2, version int64) ApprovalAggregateV2 {
	clone := cloneApprovalAggregateV2(aggregate)
	if version < 0 || version >= clone.Version || version >= int64(len(clone.History)) {
		return clone
	}
	clone.History = append([]ApprovalHistoryEntry(nil), clone.History[:version+1]...)
	last := clone.History[len(clone.History)-1]
	clone.Version = last.Version
	clone.State = last.State
	clone.LastEventID = last.EventID
	clone.LastDecision = last.Decision
	if clone.State == "active" {
		clone.ActiveApprovalID = last.ApprovalID
	} else {
		clone.ActiveApprovalID = ""
	}
	return clone
}

func approvalTransactionEventPayload(event ApprovalEventV2) []byte {
	raw, err := json.Marshal(event)
	if err != nil {
		return nil
	}
	redacted, count, err := secret.RedactJSON(raw)
	if err != nil || count != 0 {
		return nil
	}
	return redacted
}

func snapshotFromApprovalTransactionState(state approvalTransactionState) ApprovalTransactionSnapshot {
	result := ApprovalTransactionSnapshot{Events: make([]ApprovalEventV2, len(state.Events)), Aggregates: make([]ApprovalAggregateV2, len(state.Aggregates)), IdempotencyResults: make([]ApprovalIdempotencyResultV1, len(state.IdempotencyResults)), Outbox: make([]ApprovalOutboxV1, len(state.Outbox))}
	copy(result.Events, state.Events)
	for index := range state.Aggregates {
		result.Aggregates[index] = cloneApprovalAggregateV2(state.Aggregates[index])
	}
	for index := range state.IdempotencyResults {
		result.IdempotencyResults[index] = cloneApprovalIdempotencyResult(state.IdempotencyResults[index])
	}
	for index := range state.Outbox {
		result.Outbox[index] = cloneApprovalOutbox(state.Outbox[index])
	}
	if len(state.Targets) > 0 {
		result.targets = make([]approvalTransactionTargetRecord, len(state.Targets))
		for index := range state.Targets {
			result.targets[index] = state.Targets[index]
			result.targets[index].Aggregate = cloneApprovalAggregateV2(state.Targets[index].Aggregate)
		}
	}
	return result
}

func cloneApprovalTransactionState(state approvalTransactionState) approvalTransactionState {
	clone := approvalTransactionState{StoreVersion: state.StoreVersion, Events: append([]ApprovalEventV2(nil), state.Events...), Aggregates: make([]ApprovalAggregateV2, len(state.Aggregates)), IdempotencyResults: make([]ApprovalIdempotencyResultV1, len(state.IdempotencyResults)), Outbox: make([]ApprovalOutboxV1, len(state.Outbox)), Targets: make([]approvalTransactionTargetRecord, len(state.Targets))}
	for index := range state.Aggregates {
		clone.Aggregates[index] = cloneApprovalAggregateV2(state.Aggregates[index])
	}
	for index := range state.IdempotencyResults {
		clone.IdempotencyResults[index] = cloneApprovalIdempotencyResult(state.IdempotencyResults[index])
	}
	for index := range state.Outbox {
		clone.Outbox[index] = cloneApprovalOutbox(state.Outbox[index])
	}
	for index := range state.Targets {
		clone.Targets[index] = state.Targets[index]
		clone.Targets[index].Aggregate = cloneApprovalAggregateV2(state.Targets[index].Aggregate)
	}
	return clone
}

func cloneApprovalIdempotencyResult(value ApprovalIdempotencyResultV1) ApprovalIdempotencyResultV1 {
	return value
}

func cloneApprovalOutbox(value ApprovalOutboxV1) ApprovalOutboxV1 { return value }

func approvalTransactionContextError(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

func normalizeApprovalTransactionError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var typed *ApprovalTransactionError
	if errors.As(err, &typed) {
		return err
	}
	return approvalTransactionPersistence(err)
}
