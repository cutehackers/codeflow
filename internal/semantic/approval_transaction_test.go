package semantic

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"codeflow/internal/workspace"
	_ "modernc.org/sqlite"
)

func TestNewApprovalTransactionStoreWithoutAuthorityFailsClosed(t *testing.T) {
	root := filepath.Join(t.TempDir(), "missing-authority")
	store := newApprovalTransactionStore(root, approvalTransactionDependencies{})
	receipt, err := store.Commit(context.Background(), approvalTransactionTestMutation(t, "missing-authority-key", "missing-authority-command", "missing-authority-event", "missing-authority-approval", "missing-authority-aggregate"))
	if !errors.Is(err, ErrApprovalTransactionPersistence) {
		t.Fatalf("missing authority commit error = %v, want persistence failure", err)
	}
	if !reflect.DeepEqual(receipt, ApprovalCommitReceipt{}) {
		t.Fatalf("missing authority returned receipt: %#v", receipt)
	}
	if _, statErr := os.Stat(root); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("missing authority published artifacts: stat=%v", statErr)
	}
}

func TestApprovalTransactionFirstCommitPublishesDurableRecords(t *testing.T) {
	mutation := approvalTransactionTestMutation(t, "idempotency-first", "command-first", "event-first", "approval-first", "aggregate-first")
	store := newFileApprovalTransactionStoreForTest(t.TempDir())
	receipt, err := store.Commit(context.Background(), mutation)
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	if receipt.Replayed {
		t.Fatal("first commit unexpectedly reported replay")
	}
	snapshot, err := store.Snapshot(context.Background())
	if err != nil {
		var typed *ApprovalTransactionError
		errors.As(err, &typed)
		t.Fatalf("snapshot: %v cause=%v", err, typed.cause)
	}
	if len(snapshot.Events) != 1 || len(snapshot.Aggregates) != 1 || len(snapshot.IdempotencyResults) != 1 || len(snapshot.Outbox) != 1 {
		t.Fatalf("record counts = events %d aggregates %d idempotency %d outbox %d", len(snapshot.Events), len(snapshot.Aggregates), len(snapshot.IdempotencyResults), len(snapshot.Outbox))
	}
	if err := validateApprovalTransactionRecords(snapshot.Events[0], snapshot.Aggregates[0], snapshot.IdempotencyResults[0], snapshot.Outbox[0]); err != nil {
		t.Fatalf("records failed validation: %v", err)
	}
	if !bytes.Equal(receipt.EventPayload, approvalTransactionEventPayload(snapshot.Events[0])) {
		t.Fatal("receipt payload does not match the durable redacted event")
	}
}

func TestApprovalTransactionTruncatedDatabaseFailsClosedWithoutPublication(t *testing.T) {
	root := filepath.Join(t.TempDir(), "truncated-store")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatalf("mkdir store root: %v", err)
	}
	store := newFileApprovalTransactionStoreForTest(root)
	if err := os.WriteFile(store.databasePath(), nil, 0o600); err != nil {
		t.Fatalf("write truncated database: %v", err)
	}

	snapshot, err := store.Snapshot(context.Background())
	if err == nil || (!errors.Is(err, ErrApprovalTransactionPersistence) && !errors.Is(err, ErrApprovalTransactionInvalid)) {
		t.Fatalf("truncated snapshot = %#v, err=%v, want bounded typed failure", snapshot, err)
	}
	if !reflect.DeepEqual(snapshot, ApprovalTransactionSnapshot{}) {
		t.Fatalf("truncated snapshot = %#v, want zero snapshot", snapshot)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read truncated store root: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != approvalTransactionDatabaseName {
		t.Fatalf("truncated snapshot published artifacts: %#v", entries)
	}
	receipt, commitErr := store.Commit(context.Background(), approvalTransactionTestMutation(t, "truncated-commit-key", "truncated-commit-command", "truncated-commit-event", "truncated-commit-approval", "truncated-commit-aggregate"))
	if commitErr == nil || (!errors.Is(commitErr, ErrApprovalTransactionPersistence) && !errors.Is(commitErr, ErrApprovalTransactionInvalid)) {
		t.Fatalf("truncated commit = %#v, err=%v, want bounded typed failure", receipt, commitErr)
	}
	if !reflect.DeepEqual(receipt, ApprovalCommitReceipt{}) {
		t.Fatalf("truncated commit receipt = %#v, want zero receipt", receipt)
	}
	entries, err = os.ReadDir(root)
	if err != nil {
		t.Fatalf("read truncated store root after commit: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != approvalTransactionDatabaseName {
		t.Fatalf("truncated commit published artifacts: %#v", entries)
	}
}

func TestApprovalTransactionSecretCommandFailsBeforePublication(t *testing.T) {
	root := filepath.Join(t.TempDir(), "secret-command-store")
	mutation := approvalTransactionTestMutation(t, "secret-command-key", "secret-command", "secret-event", "secret-approval", "secret-aggregate")
	mutation.command.command.IdempotencyKey = `password: "transaction-canary"`
	commandDigest, err := approvalCommandDigest(mutation.command.command)
	if err != nil {
		t.Fatalf("recompute command seal: %v", err)
	}
	mutation.command.digest = commandDigest
	mutation.digest, err = mutation.contentDigest()
	if err != nil {
		t.Fatalf("recompute mutation seal: %v", err)
	}
	if err := mutation.Validate(); err != nil {
		t.Fatalf("secret command setup is not otherwise valid: %v", err)
	}

	store := newFileApprovalTransactionStoreForTest(root)
	receipt, err := store.Commit(context.Background(), mutation)
	if err == nil || (!errors.Is(err, ErrApprovalTransactionInvalid) && !errors.Is(err, ErrApprovalTransactionPersistence)) {
		t.Fatalf("secret command commit = receipt=%#v err=%v, want bounded typed rejection", receipt, err)
	}
	if !reflect.DeepEqual(receipt, ApprovalCommitReceipt{}) {
		t.Fatalf("secret command receipt = %#v, want zero receipt", receipt)
	}
	if _, statErr := os.Stat(root); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("secret command published store root: stat=%v", statErr)
	}
}

func TestApprovalTransactionOversizedPersistedPayloadFailsClosed(t *testing.T) {
	root := t.TempDir()
	store := newFileApprovalTransactionStoreForTest(root)
	if _, err := store.Commit(context.Background(), approvalTransactionTestMutation(t, "oversized-payload-key", "oversized-payload-command", "oversized-payload-event", "oversized-payload-approval", "oversized-payload-aggregate")); err != nil {
		t.Fatalf("initial commit: %v", err)
	}
	db, err := sql.Open("sqlite", store.databasePath())
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if _, err := db.Exec("PRAGMA ignore_check_constraints=ON"); err != nil {
		_ = db.Close()
		t.Fatalf("enable bounded tamper fixture: %v", err)
	}
	if _, err := db.Exec("UPDATE approval_targets SET aggregate_json=zeroblob(?)", approvalTransactionMaxBytes+1); err != nil {
		_ = db.Close()
		t.Fatalf("write oversized persisted payload: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close tamper database: %v", err)
	}

	snapshot, err := newFileApprovalTransactionStoreForTest(root).Snapshot(context.Background())
	if err == nil || (!errors.Is(err, ErrApprovalTransactionPersistence) && !errors.Is(err, ErrApprovalTransactionInvalid)) {
		t.Fatalf("oversized snapshot = %#v, err=%v, want bounded typed failure", snapshot, err)
	}
	if !reflect.DeepEqual(snapshot, ApprovalTransactionSnapshot{}) {
		t.Fatalf("oversized snapshot = %#v, want zero snapshot", snapshot)
	}
}

func TestApprovalTransactionOversizedTextUsesPersistedBytesBeforeScan(t *testing.T) {
	cases := []struct {
		name   string
		update string
		args   []any
	}{
		{
			name:   "multibyte-text",
			update: "UPDATE approval_targets SET map_id=replace(printf('%0*d', ?, 0), '0', '한')",
			args:   []any{approvalTransactionMaxBytes/3 + 128},
		},
		{
			name:   "embedded-nul-text",
			update: "UPDATE approval_targets SET map_id=CAST(zeroblob(?) AS TEXT)",
			args:   []any{approvalTransactionMaxBytes + 1},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			root := t.TempDir()
			store := newFileApprovalTransactionStoreForTest(root)
			if _, err := store.Commit(context.Background(), approvalTransactionTestMutation(t, "oversized-text-"+testCase.name, "oversized-text-command-"+testCase.name, "oversized-text-event-"+testCase.name, "oversized-text-approval-"+testCase.name, "oversized-text-aggregate-"+testCase.name)); err != nil {
				t.Fatalf("initial commit: %v", err)
			}
			db, err := sql.Open("sqlite", store.databasePath())
			if err != nil {
				t.Fatalf("open sqlite: %v", err)
			}
			if _, err := db.Exec(testCase.update, testCase.args...); err != nil {
				_ = db.Close()
				t.Fatalf("write oversized text fixture: %v", err)
			}
			if err := db.Close(); err != nil {
				t.Fatalf("close tamper database: %v", err)
			}

			snapshot, err := newFileApprovalTransactionStoreForTest(root).Snapshot(context.Background())
			if err == nil {
				t.Fatalf("oversized text snapshot = %#v, want bounded typed failure", snapshot)
			}
			var typed *ApprovalTransactionError
			if !errors.As(err, &typed) || typed.cause == nil || typed.cause.Error() != "approval transaction persisted value exceeds bound" {
				t.Fatalf("oversized text error = %v cause=%v, want pre-scan bound cause", err, typed.cause)
			}
			if !reflect.DeepEqual(snapshot, ApprovalTransactionSnapshot{}) {
				t.Fatalf("oversized text snapshot = %#v, want zero snapshot", snapshot)
			}
		})
	}
}

func TestApprovalTransactionIncompleteAndTamperedRowsFailClosed(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*sql.DB) error
	}{
		{
			name: "missing-outbox",
			mutate: func(db *sql.DB) error {
				if _, err := db.Exec("PRAGMA foreign_keys=OFF"); err != nil {
					return err
				}
				_, err := db.Exec("DELETE FROM approval_outbox")
				return err
			},
		},
		{
			name: "malformed-result-json",
			mutate: func(db *sql.DB) error {
				_, err := db.Exec("UPDATE approval_idempotency SET result_json=?", []byte("{}"))
				return err
			},
		},
		{
			name: "wrong-event-link",
			mutate: func(db *sql.DB) error {
				if _, err := db.Exec("PRAGMA foreign_keys=OFF"); err != nil {
					return err
				}
				_, err := db.Exec("UPDATE approval_idempotency SET event_id=?", "missing-event-link")
				return err
			},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			root := t.TempDir()
			store := newFileApprovalTransactionStoreForTest(root)
			if _, err := store.Commit(context.Background(), approvalTransactionTestMutation(t, "tamper-row-"+testCase.name, "tamper-row-command-"+testCase.name, "tamper-row-event-"+testCase.name, "tamper-row-approval-"+testCase.name, "tamper-row-aggregate-"+testCase.name)); err != nil {
				t.Fatalf("initial commit: %v", err)
			}
			db, err := sql.Open("sqlite", store.databasePath())
			if err != nil {
				t.Fatalf("open sqlite: %v", err)
			}
			if err := testCase.mutate(db); err != nil {
				_ = db.Close()
				t.Fatalf("tamper durable row: %v", err)
			}
			if err := db.Close(); err != nil {
				t.Fatalf("close tamper database: %v", err)
			}

			snapshot, err := newFileApprovalTransactionStoreForTest(root).Snapshot(context.Background())
			if err == nil || (!errors.Is(err, ErrApprovalTransactionPersistence) && !errors.Is(err, ErrApprovalTransactionInvalid)) {
				t.Fatalf("tampered snapshot = %#v, err=%v, want bounded typed failure", snapshot, err)
			}
			if !reflect.DeepEqual(snapshot, ApprovalTransactionSnapshot{}) {
				t.Fatalf("tampered snapshot = %#v, want zero snapshot", snapshot)
			}
		})
	}
}

func TestApprovalTransactionPersistedSecretIsRejectedWithoutLeak(t *testing.T) {
	root := t.TempDir()
	store := newFileApprovalTransactionStoreForTest(root)
	if _, err := store.Commit(context.Background(), approvalTransactionTestMutation(t, "persisted-secret-key", "persisted-secret-command", "persisted-secret-event", "persisted-secret-approval", "persisted-secret-aggregate")); err != nil {
		t.Fatalf("initial commit: %v", err)
	}
	db, err := sql.Open("sqlite", store.databasePath())
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	rewriteApprovalTransactionPersistedCommand(t, db, "persisted-transaction-control")
	if err := db.Close(); err != nil {
		t.Fatalf("close tamper database: %v", err)
	}

	controlSnapshot, err := newFileApprovalTransactionStoreForTest(root).Snapshot(context.Background())
	if err != nil || len(controlSnapshot.IdempotencyResults) != 1 || controlSnapshot.IdempotencyResults[0].IdempotencyKey != "persisted-transaction-control" {
		t.Fatalf("consistent control snapshot = %#v, err=%v, want successful snapshot", controlSnapshot, err)
	}

	db, err = sql.Open("sqlite", store.databasePath())
	if err != nil {
		t.Fatalf("reopen sqlite: %v", err)
	}
	const canary = "token=persisted-transaction-canary"
	rewriteApprovalTransactionPersistedCommand(t, db, canary)
	if err := db.Close(); err != nil {
		t.Fatalf("close secret tamper database: %v", err)
	}

	snapshot, err := newFileApprovalTransactionStoreForTest(root).Snapshot(context.Background())
	if err == nil || (!errors.Is(err, ErrApprovalTransactionPersistence) && !errors.Is(err, ErrApprovalTransactionInvalid)) {
		t.Fatalf("persisted secret snapshot = %#v, err=%v, want bounded typed failure", snapshot, err)
	}
	if strings.Contains(err.Error(), canary) {
		t.Fatalf("persisted secret leaked in error: %v", err)
	}
	if !reflect.DeepEqual(snapshot, ApprovalTransactionSnapshot{}) {
		t.Fatalf("persisted secret snapshot = %#v, want zero snapshot", snapshot)
	}
}

func rewriteApprovalTransactionPersistedCommand(t *testing.T, db *sql.DB, idempotencyKey string) {
	t.Helper()
	var oldKey string
	var commandJSON, resultJSON []byte
	if err := db.QueryRow("SELECT idempotency_key,command_json,result_json FROM approval_idempotency LIMIT 1").Scan(&oldKey, &commandJSON, &resultJSON); err != nil {
		t.Fatalf("read persisted command/result: %v", err)
	}
	var command ApprovalCommandV2
	if err := decodeApprovalTransactionJSON(commandJSON, &command); err != nil {
		t.Fatalf("decode persisted command: %v", err)
	}
	var result ApprovalIdempotencyResultV1
	if err := decodeApprovalTransactionJSON(resultJSON, &result); err != nil {
		t.Fatalf("decode persisted result: %v", err)
	}
	command.IdempotencyKey = idempotencyKey
	digest, err := approvalCommandIdempotencyDigest(command)
	if err != nil {
		t.Fatalf("recompute persisted command digest: %v", err)
	}
	result.IdempotencyKey = command.IdempotencyKey
	result.CommandID = command.CommandID
	result.ActorID = command.ActorID
	result.SessionID = command.SessionID
	result.WorkspaceID = command.WorkspaceID
	result.ProposalID = command.ProposalID
	result.EvidencePackID = command.EvidencePackID
	result.ComputedBasisID = command.ComputedBasisID
	result.GenerationID = command.GenerationID
	result.RequestDigest = "sha256:" + hex.EncodeToString(digest[:])
	result.OriginalCommand = approvalTransactionOriginalCommand(command)
	if err := validateApprovalCommandValue(command); err != nil {
		t.Fatalf("rewritten command is otherwise invalid: %v", err)
	}
	if err := validateApprovalTransactionResult(result); err != nil {
		t.Fatalf("rewritten result is otherwise invalid: %v", err)
	}
	updatedCommandJSON, err := json.Marshal(command)
	if err != nil {
		t.Fatalf("encode persisted command: %v", err)
	}
	updatedResultJSON, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("encode persisted result: %v", err)
	}
	_, err = db.Exec(`UPDATE approval_idempotency SET
		workspace_id=?,idempotency_key=?,command_id=?,actor_id=?,session_id=?,proposal_id=?,evidence_pack_id=?,computed_basis_id=?,generation_id=?,request_digest=?,outcome=?,approval_id=?,event_id=?,aggregate_id=?,aggregate_version=?,state=?,committed_at=?,command_json=?,result_json=?
		WHERE idempotency_key=?`,
		result.WorkspaceID, result.IdempotencyKey, result.CommandID, result.ActorID, result.SessionID, result.ProposalID, result.EvidencePackID, result.ComputedBasisID, result.GenerationID,
		result.RequestDigest, result.Outcome, result.ApprovalID, result.EventID, result.AggregateID, result.AggregateVersion, result.State, result.CommittedAt, updatedCommandJSON, updatedResultJSON, oldKey)
	if err != nil {
		t.Fatalf("write consistent persisted command/result: %v", err)
	}
}

func TestApprovalTransactionStorageBudgetRejectsBeforeCommitAndReopens(t *testing.T) {
	root := t.TempDir()
	initialStore := newFileApprovalTransactionStoreForTest(root)
	access := approvalCommandTestAccess(t)
	initialMutation := approvalTransactionTestMutationWithAccess(t, access, "storage-budget-initial-key", "storage-budget-initial-command", "event-all-initial", "approval-all-initial", "storage-budget-initial-aggregate")
	if _, err := initialStore.Commit(context.Background(), initialMutation); err != nil {
		t.Fatalf("initial commit: %v", err)
	}
	baseline, err := approvalTransactionSQLiteArtifactBytes(initialStore.databasePath(), approvalTransactionMaxDatabaseBytes)
	if err != nil {
		t.Fatalf("measure baseline storage: %v", err)
	}
	if baseline <= 0 {
		t.Fatalf("baseline storage = %d, want positive", baseline)
	}

	// Leave enough room for the shared-memory sidecar, but less than the
	// conservative WAL frame reserve for another transaction. The existing
	// implementation only checks the current artifact and therefore accepts
	// this write, which is the Red condition for this regression.
	constrainedBudget := baseline + 128<<10
	constrainedStore := newApprovalTransactionStoreWithDependencies(root, approvalTransactionDependencies{storageBudget: constrainedBudget})
	constrainedMutation := approvalTransactionTestMutationForDecisionWithAccess(t, access, "edit_then_approve", "storage-budget-initial-aggregate", "storage-budget-rejected-event", "storage-budget-rejected-approval", "storage-budget-rejected-key")
	constrainedReceipt, err := constrainedStore.Commit(context.Background(), constrainedMutation)
	if !errors.Is(err, ErrApprovalTransactionPersistence) {
		t.Fatalf("constrained commit = %v, want typed persistence capacity failure", err)
	}
	if !reflect.DeepEqual(constrainedReceipt, ApprovalCommitReceipt{}) {
		t.Fatalf("constrained receipt = %#v, want zero receipt", constrainedReceipt)
	}
	var capacityErr *ApprovalTransactionError
	if !errors.As(err, &capacityErr) || !errors.Is(err, errApprovalTransactionStorageBudgetExceeded) {
		t.Fatalf("constrained commit error = %v, want storage budget cause", err)
	}
	if capacityErr.CurrentState != "active" || capacityErr.CurrentVersion != 1 {
		t.Fatalf("constrained prior state = %s/%d, want active/1", capacityErr.CurrentState, capacityErr.CurrentVersion)
	}
	snapshot, err := constrainedStore.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("snapshot after capacity rejection: %v", err)
	}
	if len(snapshot.Events) != 1 || len(snapshot.Aggregates) != 1 || len(snapshot.IdempotencyResults) != 1 || len(snapshot.Outbox) != 1 {
		t.Fatalf("capacity rejection published records: %#v", snapshot)
	}

	adequateBudget := baseline + 2<<20
	ableStore := newApprovalTransactionStoreWithDependencies(root, approvalTransactionDependencies{storageBudget: adequateBudget})
	ableMutation := constrainedMutation
	if _, err := ableStore.Commit(context.Background(), ableMutation); err != nil {
		t.Fatalf("adequate-capacity commit: %v", err)
	}
	acceptedSnapshot, err := ableStore.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("snapshot after adequate commit: %v", err)
	}
	if len(acceptedSnapshot.Events) != 2 || len(acceptedSnapshot.Aggregates) != 1 || len(acceptedSnapshot.IdempotencyResults) != 2 || len(acceptedSnapshot.Outbox) != 2 {
		t.Fatalf("adequate commit counts: events=%d aggregates=%d idempotency=%d outbox=%d, want 2/1/2/2", len(acceptedSnapshot.Events), len(acceptedSnapshot.Aggregates), len(acceptedSnapshot.IdempotencyResults), len(acceptedSnapshot.Outbox))
	}
	pragmaDB, err := ableStore.openApprovalTransactionDatabaseLocked(context.Background(), false)
	if err != nil {
		t.Fatalf("open bounded database for pragma checks: %v", err)
	}
	var pageSize, maxPageCount, journalSizeLimit int64
	if err := pragmaDB.QueryRow("PRAGMA page_size").Scan(&pageSize); err != nil {
		_ = pragmaDB.Close()
		t.Fatalf("read page size: %v", err)
	}
	if err := pragmaDB.QueryRow("PRAGMA max_page_count").Scan(&maxPageCount); err != nil {
		_ = pragmaDB.Close()
		t.Fatalf("read max page count: %v", err)
	}
	if err := pragmaDB.QueryRow("PRAGMA journal_size_limit").Scan(&journalSizeLimit); err != nil {
		_ = pragmaDB.Close()
		t.Fatalf("read journal size limit: %v", err)
	}
	if err := pragmaDB.Close(); err != nil {
		t.Fatalf("close bounded pragma database: %v", err)
	}
	if pageSize <= 0 || maxPageCount != adequateBudget/pageSize || journalSizeLimit != adequateBudget {
		t.Fatalf("bounded pragmas pageSize=%d maxPageCount=%d journalSizeLimit=%d, want max=%d journal=%d", pageSize, maxPageCount, journalSizeLimit, adequateBudget/pageSize, adequateBudget)
	}
	total, err := approvalTransactionSQLiteArtifactBytes(ableStore.databasePath(), adequateBudget)
	if err != nil {
		t.Fatalf("measure accepted storage: %v", err)
	}
	if total > adequateBudget {
		t.Fatalf("accepted storage total=%d exceeds budget=%d", total, adequateBudget)
	}
	reopenedStore := newApprovalTransactionStoreWithDependencies(root, approvalTransactionDependencies{storageBudget: adequateBudget})
	reopenedSnapshot, err := reopenedStore.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("reopen after bounded commit: %v", err)
	}
	if len(reopenedSnapshot.Events) != 2 || len(reopenedSnapshot.IdempotencyResults) != 2 || len(reopenedSnapshot.Outbox) != 2 {
		t.Fatalf("reopened counts: events=%d idempotency=%d outbox=%d, want 2/2/2", len(reopenedSnapshot.Events), len(reopenedSnapshot.IdempotencyResults), len(reopenedSnapshot.Outbox))
	}
}

func TestApprovalTransactionStorageBudgetArithmeticBoundaries(t *testing.T) {
	maxInt64 := int64(^uint64(0) >> 1)
	if sum, ok := approvalTransactionSafeAdd(1, 2); !ok || sum != 3 {
		t.Fatalf("safe add = %d,%t, want 3,true", sum, ok)
	}
	if _, ok := approvalTransactionSafeAdd(maxInt64, 1); ok {
		t.Fatal("safe add overflow was accepted")
	}
	if product, ok := approvalTransactionSafeMul(3, 7); !ok || product != 21 {
		t.Fatalf("safe multiply = %d,%t, want 21,true", product, ok)
	}
	if _, ok := approvalTransactionSafeMul(maxInt64, 2); ok {
		t.Fatal("safe multiply overflow was accepted")
	}
	if quotient, ok := approvalTransactionCeilDiv(0, 4096); !ok || quotient != 0 {
		t.Fatalf("ceil div zero = %d,%t, want 0,true", quotient, ok)
	}
	if quotient, ok := approvalTransactionCeilDiv(4097, 4096); !ok || quotient != 2 {
		t.Fatalf("ceil div boundary = %d,%t, want 2,true", quotient, ok)
	}
	if _, ok := approvalTransactionCeilDiv(1, 0); ok {
		t.Fatal("ceil div zero divisor was accepted")
	}
	frameBytes := int64(4096 + approvalTransactionSQLiteWALFrameBytes)
	belowRegion, ok := approvalTransactionWALIndexReserveBytes(int64(approvalTransactionSQLiteWALFramesPerIndexRegion)*frameBytes, frameBytes)
	if !ok {
		t.Fatal("WAL-index reserve at region boundary failed")
	}
	aboveRegion, ok := approvalTransactionWALIndexReserveBytes(int64(approvalTransactionSQLiteWALFramesPerIndexRegion)*frameBytes+1, frameBytes)
	if !ok || aboveRegion != belowRegion+approvalTransactionSQLiteWALIndexRegionBytes {
		t.Fatalf("WAL-index region boundary below=%d above=%d, want one region growth", belowRegion, aboveRegion)
	}
	if _, ok := approvalTransactionWALIndexReserveBytes(maxInt64, 0); ok {
		t.Fatal("WAL-index reserve accepted zero frame size")
	}
}

func TestApprovalTransactionStorageBudgetAggregatesAndRejectsUnsafeSidecars(t *testing.T) {
	cases := []struct {
		name       string
		prepare    func(*testing.T, string)
		wantCause  error
		wantReject bool
	}{
		{
			name: "aggregate-over-budget",
			prepare: func(t *testing.T, sidecar string) {
				t.Helper()
				if err := os.WriteFile(sidecar, bytes.Repeat([]byte("w"), 32<<10), 0o600); err != nil {
					t.Fatalf("write sidecar: %v", err)
				}
			},
			wantCause:  errApprovalTransactionStorageBudgetExceeded,
			wantReject: true,
		},
		{
			name: "symlink-sidecar",
			prepare: func(t *testing.T, sidecar string) {
				t.Helper()
				target := filepath.Join(filepath.Dir(sidecar), "sidecar-target")
				if err := os.WriteFile(target, []byte("target"), 0o600); err != nil {
					t.Fatalf("write sidecar target: %v", err)
				}
				if err := os.Symlink(target, sidecar); err != nil {
					t.Skipf("symlink unavailable: %v", err)
				}
			},
			wantReject: true,
		},
		{
			name: "permissive-sidecar",
			prepare: func(t *testing.T, sidecar string) {
				t.Helper()
				if err := os.WriteFile(sidecar, []byte("mode"), 0o600); err != nil {
					t.Fatalf("write sidecar: %v", err)
				}
				if err := os.Chmod(sidecar, 0o644); err != nil {
					t.Fatalf("chmod sidecar: %v", err)
				}
				if info, err := os.Stat(sidecar); err != nil || info.Mode().Perm() != 0o644 {
					t.Skipf("filesystem did not retain permissive mode: %v", err)
				}
			},
			wantReject: true,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			root := t.TempDir()
			store := newFileApprovalTransactionStoreForTest(root)
			if _, err := store.Commit(context.Background(), approvalTransactionTestMutation(t, "sidecar-"+testCase.name, "sidecar-command-"+testCase.name, "sidecar-event-"+testCase.name, "sidecar-approval-"+testCase.name, "sidecar-aggregate-"+testCase.name)); err != nil {
				t.Fatalf("initial commit: %v", err)
			}
			baseline, err := approvalTransactionSQLiteArtifactBytes(store.databasePath(), approvalTransactionMaxDatabaseBytes)
			if err != nil {
				t.Fatalf("baseline storage: %v", err)
			}
			sidecar := store.databasePath() + "-wal"
			testCase.prepare(t, sidecar)
			budget := baseline + 1
			if testCase.wantCause != nil {
				if _, err := approvalTransactionSQLiteArtifactBytes(store.databasePath(), budget); !errors.Is(err, testCase.wantCause) {
					t.Fatalf("artifact total error = %v, want %v", err, testCase.wantCause)
				}
			} else if _, err := approvalTransactionSQLiteArtifactBytes(store.databasePath(), budget); err == nil {
				t.Fatal("unsafe sidecar artifact was accepted")
			}
			if testCase.wantReject {
				constrainedStore := newApprovalTransactionStoreWithDependencies(root, approvalTransactionDependencies{storageBudget: budget})
				if _, err := constrainedStore.Snapshot(context.Background()); !errors.Is(err, ErrApprovalTransactionPersistence) {
					t.Fatalf("snapshot with unsafe sidecar = %v, want persistence", err)
				}
			}
		})
	}
}

func TestApprovalTransactionCommitCapacityUsesPostDMLPageCount(t *testing.T) {
	root := t.TempDir()
	initialStore := newFileApprovalTransactionStoreForTest(root)
	if _, err := initialStore.Commit(context.Background(), approvalTransactionTestMutation(t, "page-count-initial-key", "page-count-initial-command", "page-count-initial-event", "page-count-initial-approval", "page-count-initial-aggregate")); err != nil {
		t.Fatalf("initial commit: %v", err)
	}
	baseline, err := approvalTransactionSQLiteArtifactBytes(initialStore.databasePath(), approvalTransactionMaxDatabaseBytes)
	if err != nil {
		t.Fatalf("baseline storage: %v", err)
	}
	budget := baseline + 900<<10
	store := newApprovalTransactionStoreWithDependencies(root, approvalTransactionDependencies{storageBudget: budget})
	db, err := store.openApprovalTransactionDatabaseLocked(context.Background(), false)
	if err != nil {
		t.Fatalf("open bounded database: %v", err)
	}
	defer db.Close()
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatalf("database connection: %v", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(context.Background(), "BEGIN IMMEDIATE"); err != nil {
		t.Fatalf("begin direct DML: %v", err)
	}
	defer conn.ExecContext(context.Background(), "ROLLBACK")
	payload := bytes.Repeat([]byte("page-count-growth"), 48<<10)
	if _, err := conn.ExecContext(context.Background(), "UPDATE approval_targets SET aggregate_json=?", payload); err != nil {
		t.Fatalf("grow aggregate during DML: %v", err)
	}
	var pageSize, pageCount int64
	if err := conn.QueryRowContext(context.Background(), "PRAGMA page_size").Scan(&pageSize); err != nil {
		t.Fatalf("read post-DML page size: %v", err)
	}
	if err := conn.QueryRowContext(context.Background(), "PRAGMA page_count").Scan(&pageCount); err != nil {
		t.Fatalf("read post-DML page count: %v", err)
	}
	if pageSize <= 0 || pageCount < 128 {
		t.Fatalf("post-DML page size/count=%d/%d, want positive/128+", pageSize, pageCount)
	}
	// Before the page-count implementation, a fixed page reserve accepted this
	// state even though the post-DML page count required a larger frame bound.
	// This assertion protects the page-count based capacity proof.
	if err := store.ensureApprovalTransactionCommitCapacity(context.Background(), conn); !errors.Is(err, errApprovalTransactionStorageBudgetExceeded) {
		t.Fatalf("post-DML capacity error=%v, want storage budget rejection", err)
	}
}

func TestApprovalTransactionStorageBudgetTooSmallForSchemaCanRetry(t *testing.T) {
	root := t.TempDir()
	mutation := approvalTransactionTestMutation(t, "schema-budget-key", "schema-budget-command", "schema-budget-event", "schema-budget-approval", "schema-budget-aggregate")
	smallStore := newApprovalTransactionStoreWithDependencies(root, approvalTransactionDependencies{storageBudget: 64 << 10})
	receipt, err := smallStore.Commit(context.Background(), mutation)
	if err == nil || !errors.Is(err, ErrApprovalTransactionPersistence) || !reflect.DeepEqual(receipt, ApprovalCommitReceipt{}) {
		t.Fatalf("too-small schema commit = receipt=%#v err=%v, want zero typed persistence", receipt, err)
	}
	adequateStore := newApprovalTransactionStoreWithDependencies(root, approvalTransactionDependencies{storageBudget: 2 << 20})
	if _, err := adequateStore.Commit(context.Background(), mutation); err != nil {
		t.Fatalf("retry after increasing schema budget: %v", err)
	}
	snapshot, err := adequateStore.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("snapshot after schema-budget retry: %v", err)
	}
	if len(snapshot.Events) != 1 || len(snapshot.Aggregates) != 1 || len(snapshot.IdempotencyResults) != 1 || len(snapshot.Outbox) != 1 {
		t.Fatalf("schema-budget retry counts: events=%d idempotency=%d outbox=%d, want 1/1/1", len(snapshot.Events), len(snapshot.IdempotencyResults), len(snapshot.Outbox))
	}
}

func TestApprovalTransactionCommitRejectsWhenExternalReaderBlocksWALBaseline(t *testing.T) {
	root := t.TempDir()
	access := approvalCommandTestAccess(t)
	store := newFileApprovalTransactionStoreForTest(root)
	initial := approvalTransactionTestMutationWithAccess(t, access, "wal-reader-initial-key", "wal-reader-initial-command", "event-all-initial", "approval-all-initial", "wal-reader-aggregate")
	if _, err := store.Commit(context.Background(), initial); err != nil {
		t.Fatalf("initial commit: %v", err)
	}
	readerDB, err := sql.Open("sqlite", store.databasePath())
	if err != nil {
		t.Fatalf("open external reader: %v", err)
	}
	readerDB.SetMaxOpenConns(1)
	readerConn, err := readerDB.Conn(context.Background())
	if err != nil {
		_ = readerDB.Close()
		t.Fatalf("external reader connection: %v", err)
	}
	defer readerConn.Close()
	defer readerDB.Close()
	if _, err := readerConn.ExecContext(context.Background(), "BEGIN"); err != nil {
		t.Fatalf("begin external read: %v", err)
	}
	defer readerConn.ExecContext(context.Background(), "ROLLBACK")
	var aggregateJSON []byte
	if err := readerConn.QueryRowContext(context.Background(), "SELECT aggregate_json FROM approval_targets LIMIT 1").Scan(&aggregateJSON); err != nil {
		t.Fatalf("hold external read snapshot: %v", err)
	}
	writerDB, err := sql.Open("sqlite", store.databasePath())
	if err != nil {
		t.Fatalf("open external writer: %v", err)
	}
	writerDB.SetMaxOpenConns(1)
	if _, err := writerDB.ExecContext(context.Background(), "PRAGMA busy_timeout=2000"); err != nil {
		_ = writerDB.Close()
		t.Fatalf("configure external writer: %v", err)
	}
	if _, err := writerDB.ExecContext(context.Background(), "BEGIN IMMEDIATE"); err != nil {
		_ = writerDB.Close()
		t.Fatalf("begin external writer: %v", err)
	}
	if _, err := writerDB.ExecContext(context.Background(), "UPDATE approval_targets SET workspace_epoch=workspace_epoch+1"); err != nil {
		_, _ = writerDB.ExecContext(context.Background(), "ROLLBACK")
		_ = writerDB.Close()
		t.Fatalf("emit external WAL frame: %v", err)
	}
	if _, err := writerDB.ExecContext(context.Background(), "UPDATE approval_targets SET workspace_epoch=workspace_epoch-1"); err != nil {
		_, _ = writerDB.ExecContext(context.Background(), "ROLLBACK")
		_ = writerDB.Close()
		t.Fatalf("restore external WAL frame: %v", err)
	}
	if _, err := writerDB.ExecContext(context.Background(), "COMMIT"); err != nil {
		_ = writerDB.Close()
		t.Fatalf("commit external WAL frame: %v", err)
	}
	if info, err := os.Stat(store.databasePath() + "-wal"); err != nil || info.Size() == 0 {
		t.Fatalf("external writer did not leave a WAL frame: size/error=%d/%v", func() int64 {
			if info != nil {
				return info.Size()
			}
			return 0
		}(), err)
	}
	defer writerDB.Close()

	second := approvalTransactionTestMutationForDecisionWithAccess(t, access, "edit_then_approve", "wal-reader-aggregate", "wal-reader-second-event", "wal-reader-second-approval", "wal-reader-second-key")
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	started := time.Now()
	receipt, err := store.Commit(ctx, second)
	if err == nil || !errors.Is(err, ErrApprovalTransactionPersistence) || !reflect.DeepEqual(receipt, ApprovalCommitReceipt{}) {
		t.Fatalf("blocked-baseline commit = receipt=%#v err=%v, want zero typed persistence", receipt, err)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("blocked-baseline rejection took %s, want bounded failure", elapsed)
	}
	snapshot, err := store.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("snapshot after blocked-baseline rejection: %v", err)
	}
	if len(snapshot.Events) != 1 || len(snapshot.Aggregates) != 1 || len(snapshot.IdempotencyResults) != 1 || len(snapshot.Outbox) != 1 {
		t.Fatalf("blocked-baseline published records: %#v", snapshot)
	}
	if _, err := readerConn.ExecContext(context.Background(), "ROLLBACK"); err != nil {
		t.Fatalf("release external read snapshot: %v", err)
	}
	if _, err := store.Commit(context.Background(), second); err != nil {
		t.Fatalf("retry after releasing reader: %v", err)
	}
}

func TestApprovalTransactionSQLiteSchemaUsesWALAndStrictTables(t *testing.T) {
	store := newFileApprovalTransactionStoreForTest(t.TempDir())
	if _, err := store.Commit(context.Background(), approvalTransactionTestMutation(t, "schema-key", "schema-command", "schema-event", "schema-approval", "schema-aggregate")); err != nil {
		t.Fatalf("commit: %v", err)
	}
	db, err := sql.Open("sqlite", store.databasePath())
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()
	var mode string
	if err := db.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil || mode != "wal" {
		t.Fatalf("journal mode = %q, err=%v, want wal", mode, err)
	}
	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != approvalTransactionSQLiteSchemaVersion {
		t.Fatalf("schema version = %d, err=%v", version, err)
	}
	for _, table := range []string{"approval_events", "approval_targets", "approval_idempotency", "approval_outbox"} {
		var count int
		if err := db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&count); err != nil || count != 1 {
			t.Fatalf("table %q count=%d err=%v", table, count, err)
		}
	}
	rows, err := db.Query("PRAGMA table_list")
	if err != nil {
		t.Fatalf("table_list: %v", err)
	}
	strictTables := make(map[string]int)
	for rows.Next() {
		var schemaName, tableName, tableType string
		var columnCount, withoutRowID, strict int
		if err := rows.Scan(&schemaName, &tableName, &tableType, &columnCount, &withoutRowID, &strict); err != nil {
			_ = rows.Close()
			t.Fatalf("table_list scan: %v", err)
		}
		if schemaName == "main" && tableType == "table" {
			strictTables[tableName] = strict
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		t.Fatalf("table_list rows: %v", err)
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("close table_list: %v", err)
	}
	for _, table := range []string{"approval_events", "approval_targets", "approval_idempotency", "approval_outbox"} {
		if strictTables[table] != 1 {
			t.Fatalf("table %q strict=%d, want 1", table, strictTables[table])
		}
	}
	for _, trigger := range approvalTransactionSQLiteTriggers {
		var count int
		if err := db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='trigger' AND name=?", trigger).Scan(&count); err != nil || count != 1 {
			t.Fatalf("trigger %q count=%d err=%v", trigger, count, err)
		}
	}
}

func TestApprovalTransactionSQLiteSchemaDriftFailsClosed(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*sql.DB) error
	}{
		{
			name: "same-name-ineffective-trigger",
			mutate: func(db *sql.DB) error {
				if _, err := db.Exec("DROP TRIGGER approval_events_no_update"); err != nil {
					return err
				}
				_, err := db.Exec("CREATE TRIGGER approval_events_no_update BEFORE UPDATE ON approval_events BEGIN SELECT 1; END")
				return err
			},
		},
		{
			name: "extra-column",
			mutate: func(db *sql.DB) error {
				_, err := db.Exec("ALTER TABLE approval_targets ADD COLUMN unexpected TEXT")
				return err
			},
		},
		{
			name: "extra-index",
			mutate: func(db *sql.DB) error {
				_, err := db.Exec("CREATE INDEX unexpected_approval_event_index ON approval_events(event_id)")
				return err
			},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			root := t.TempDir()
			store := newFileApprovalTransactionStoreForTest(root)
			if _, err := store.Commit(context.Background(), approvalTransactionTestMutation(t, "schema-drift-"+testCase.name, "schema-drift-command-"+testCase.name, "schema-drift-event-"+testCase.name, "schema-drift-approval-"+testCase.name, "schema-drift-aggregate-"+testCase.name)); err != nil {
				t.Fatalf("commit: %v", err)
			}
			db, err := sql.Open("sqlite", store.databasePath())
			if err != nil {
				t.Fatalf("open sqlite: %v", err)
			}
			if err := testCase.mutate(db); err != nil {
				_ = db.Close()
				t.Fatalf("mutate schema: %v", err)
			}
			if err := db.Close(); err != nil {
				t.Fatalf("close sqlite: %v", err)
			}
			if _, err := newFileApprovalTransactionStoreForTest(root).Snapshot(context.Background()); !errors.Is(err, ErrApprovalTransactionPersistence) {
				t.Fatalf("schema drift snapshot = %v, want persistence", err)
			}
		})
	}
}

func TestApprovalTransactionSnapshotReadUsesOneConsistentSQLiteSnapshot(t *testing.T) {
	root := t.TempDir()
	store := newFileApprovalTransactionStoreForTest(root)
	if _, err := store.Commit(context.Background(), approvalTransactionTestMutation(t, "snapshot-consistency-key", "snapshot-consistency-command", "snapshot-consistency-event", "snapshot-consistency-approval", "snapshot-consistency-aggregate")); err != nil {
		t.Fatalf("commit: %v", err)
	}
	readDB, err := sql.Open("sqlite", store.databasePath())
	if err != nil {
		t.Fatalf("open read database: %v", err)
	}
	readDB.SetMaxOpenConns(1)
	defer readDB.Close()
	writerDB, err := sql.Open("sqlite", store.databasePath())
	if err != nil {
		t.Fatalf("open writer database: %v", err)
	}
	writerDB.SetMaxOpenConns(1)
	defer writerDB.Close()
	if _, err := writerDB.Exec("PRAGMA busy_timeout=2000"); err != nil {
		t.Fatalf("configure writer database: %v", err)
	}

	writerFinished := make(chan struct{})
	var writerErr error
	var startWriter sync.Once
	readHook := func(component string) error {
		if component != "events" {
			return nil
		}
		startWriter.Do(func() {
			go func() {
				if _, err := writerDB.Exec("BEGIN IMMEDIATE"); err != nil {
					writerErr = err
					close(writerFinished)
					return
				}
				if _, err := writerDB.Exec("UPDATE approval_targets SET state='rejected'"); err != nil {
					_, _ = writerDB.Exec("ROLLBACK")
					writerErr = err
					close(writerFinished)
					return
				}
				writerErr = func() error {
					_, err := writerDB.Exec("COMMIT")
					return err
				}()
				close(writerFinished)
			}()
		})
		select {
		case <-writerFinished:
			return writerErr
		case <-time.After(2 * time.Second):
			return errors.New("independent writer did not commit during the read")
		}
	}

	state, err := loadApprovalTransactionStateDBWithReadHook(context.Background(), readDB, readHook)
	if err != nil {
		t.Fatalf("inconsistent snapshot read: %v", err)
	}
	if len(state.Events) != 1 || len(state.Targets) != 1 || state.Targets[0].Aggregate.State != "active" {
		t.Fatalf("read mixed state: events=%d targets=%d state=%q", len(state.Events), len(state.Targets), state.Targets[0].Aggregate.State)
	}
	select {
	case <-writerFinished:
		if writerErr != nil {
			t.Fatalf("writer after snapshot: %v", writerErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("independent writer did not complete after snapshot")
	}
}

func TestApprovalTransactionSnapshotReadCancellationPreservesCauseAndRollsBack(t *testing.T) {
	store := newFileApprovalTransactionStoreForTest(t.TempDir())
	if _, err := store.Commit(context.Background(), approvalTransactionTestMutation(t, "snapshot-cancel-key", "snapshot-cancel-command", "snapshot-cancel-event", "snapshot-cancel-approval", "snapshot-cancel-aggregate")); err != nil {
		t.Fatalf("commit: %v", err)
	}
	db, err := sql.Open("sqlite", store.databasePath())
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	db.SetMaxOpenConns(1)
	defer db.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, err = loadApprovalTransactionStateDBWithReadHook(ctx, db, func(component string) error {
		if component == "events" {
			cancel()
		}
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled snapshot error = %v, want context.Canceled", err)
	}
	state, err := loadApprovalTransactionStateDB(context.Background(), db)
	if err != nil {
		t.Fatalf("snapshot after canceled read: %v", err)
	}
	if len(state.Events) != 1 || len(state.Targets) != 1 {
		t.Fatalf("snapshot after canceled read = events %d targets %d, want one each", len(state.Events), len(state.Targets))
	}
}

func TestApprovalTransactionSnapshotReadDeadlinePreservesCauseAndRollsBack(t *testing.T) {
	store := newFileApprovalTransactionStoreForTest(t.TempDir())
	if _, err := store.Commit(context.Background(), approvalTransactionTestMutation(t, "snapshot-deadline-key", "snapshot-deadline-command", "snapshot-deadline-event", "snapshot-deadline-approval", "snapshot-deadline-aggregate")); err != nil {
		t.Fatalf("commit: %v", err)
	}
	db, err := sql.Open("sqlite", store.databasePath())
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	db.SetMaxOpenConns(1)
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	_, err = loadApprovalTransactionStateDBWithReadHook(ctx, db, func(component string) error {
		if component == "events" {
			<-ctx.Done()
		}
		return nil
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline snapshot error = %v, want context.DeadlineExceeded", err)
	}
	state, err := loadApprovalTransactionStateDB(context.Background(), db)
	if err != nil {
		t.Fatalf("snapshot after deadline read: %v", err)
	}
	if len(state.Events) != 1 || len(state.Targets) != 1 {
		t.Fatalf("snapshot after deadline read = events %d targets %d, want one each", len(state.Events), len(state.Targets))
	}
}

func TestApprovalTransactionReplayReturnsOriginalWithoutWriting(t *testing.T) {
	mutation := approvalTransactionTestMutation(t, "idempotency-replay", "command-replay", "event-replay", "approval-replay", "aggregate-replay")
	store := newFileApprovalTransactionStoreForTest(t.TempDir())
	first, err := store.Commit(context.Background(), mutation)
	if err != nil {
		t.Fatalf("first commit: %v", err)
	}
	before, err := store.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("snapshot before replay: %v", err)
	}
	replay, err := store.Commit(context.Background(), mutation)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if !replay.Replayed || replay.IdempotencyResult != first.IdempotencyResult || replay.Event != first.Event || !reflect.DeepEqual(replay.Aggregate, first.Aggregate) || replay.Outbox != first.Outbox || !bytes.Equal(replay.EventPayload, first.EventPayload) {
		t.Fatal("replay did not preserve the original result")
	}
	after, err := store.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("snapshot after replay: %v", err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("idempotent replay changed durable records")
	}
}

func TestApprovalTransactionReplayRejectsReorderedEventSequence(t *testing.T) {
	root := t.TempDir()
	access := approvalCommandTestAccess(t)
	store := newFileApprovalTransactionStoreForTest(root)
	firstMutation := approvalTransactionTestMutationWithAccess(t, access, "replay-order-first-key", "replay-order-first-command", "event-all-initial", "approval-all-initial", "replay-order-aggregate")
	first, err := store.Commit(context.Background(), firstMutation)
	if err != nil {
		t.Fatalf("first commit: %v", err)
	}
	secondMutation := approvalTransactionTestMutationForDecisionWithAccess(t, access, "edit_then_approve", "replay-order-aggregate", "replay-order-second-event", "replay-order-second-approval", "replay-order-second-key")
	second, err := store.Commit(context.Background(), secondMutation)
	if err != nil {
		t.Fatalf("second commit: %v", err)
	}
	if second.Event.AggregateVersion != 2 || len(second.Aggregate.History) != 3 {
		t.Fatalf("second commit did not create a two-event aggregate: event=%+v aggregate=%+v", second.Event, second.Aggregate)
	}

	db, err := sql.Open("sqlite", store.databasePath())
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	for _, trigger := range approvalTransactionSQLiteTriggers {
		if _, err := db.Exec("DROP TRIGGER " + trigger); err != nil {
			_ = db.Close()
			t.Fatalf("drop immutable trigger %q: %v", trigger, err)
		}
	}
	if _, err := db.Exec("UPDATE approval_events SET seq = seq + 10"); err != nil {
		_ = db.Close()
		t.Fatalf("move event rows to temporary sequence: %v", err)
	}
	if _, err := db.Exec("UPDATE approval_events SET seq = 2 WHERE event_id = ?", first.Event.EventID); err != nil {
		_ = db.Close()
		t.Fatalf("move first event sequence: %v", err)
	}
	if _, err := db.Exec("UPDATE approval_events SET seq = 1 WHERE event_id = ?", second.Event.EventID); err != nil {
		_ = db.Close()
		t.Fatalf("move second event sequence: %v", err)
	}
	for _, statement := range approvalTransactionSQLiteSchema[len(approvalTransactionSQLiteSchema)-len(approvalTransactionSQLiteTriggers):] {
		if _, err := db.Exec(statement); err != nil {
			_ = db.Close()
			t.Fatalf("restore immutable trigger: %v", err)
		}
	}
	rows, err := db.Query("SELECT event_id FROM approval_events ORDER BY seq")
	if err != nil {
		_ = db.Close()
		t.Fatalf("read reordered event sequence: %v", err)
	}
	var eventIDs []string
	for rows.Next() {
		var eventID string
		if err := rows.Scan(&eventID); err != nil {
			_ = rows.Close()
			_ = db.Close()
			t.Fatalf("scan reordered event sequence: %v", err)
		}
		eventIDs = append(eventIDs, eventID)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		_ = db.Close()
		t.Fatalf("read reordered event sequence: %v", err)
	}
	if err := rows.Close(); err != nil {
		_ = db.Close()
		t.Fatalf("close reordered event sequence: %v", err)
	}
	if !reflect.DeepEqual(eventIDs, []string{second.Event.EventID, first.Event.EventID}) {
		_ = db.Close()
		t.Fatalf("event sequence = %v, want swapped durable rows", eventIDs)
	}
	idempotencyRows, err := db.Query("SELECT i.event_id FROM approval_idempotency i JOIN approval_events e ON e.event_id = i.event_id ORDER BY e.seq")
	if err != nil {
		_ = db.Close()
		t.Fatalf("read reordered idempotency sequence: %v", err)
	}
	var idempotencyEventIDs []string
	for idempotencyRows.Next() {
		var eventID string
		if err := idempotencyRows.Scan(&eventID); err != nil {
			_ = idempotencyRows.Close()
			_ = db.Close()
			t.Fatalf("scan reordered idempotency sequence: %v", err)
		}
		idempotencyEventIDs = append(idempotencyEventIDs, eventID)
	}
	if err := idempotencyRows.Err(); err != nil {
		_ = idempotencyRows.Close()
		_ = db.Close()
		t.Fatalf("read reordered idempotency sequence: %v", err)
	}
	if err := idempotencyRows.Close(); err != nil {
		_ = db.Close()
		t.Fatalf("close reordered idempotency sequence: %v", err)
	}
	if !reflect.DeepEqual(idempotencyEventIDs, eventIDs) {
		_ = db.Close()
		t.Fatalf("idempotency sequence = %v, want event sequence %v", idempotencyEventIDs, eventIDs)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close sqlite: %v", err)
	}

	for name, candidate := range map[string]*ApprovalTransactionStore{
		"same store":     store,
		"reopened store": newFileApprovalTransactionStoreForTest(root),
	} {
		snapshot, err := candidate.Snapshot(context.Background())
		if err == nil || !errors.Is(err, ErrApprovalTransactionInvalid) && !errors.Is(err, ErrApprovalTransactionPersistence) {
			t.Fatalf("%s reordered snapshot = %#v, err=%v, want typed replay rejection", name, snapshot, err)
		}
		if !reflect.DeepEqual(snapshot, ApprovalTransactionSnapshot{}) {
			t.Fatalf("%s reordered snapshot = %#v, want zero snapshot", name, snapshot)
		}
	}
}

func TestApprovalTransactionReopenReplaysTwoEventLifecycleHistory(t *testing.T) {
	cases := []struct {
		name         string
		decision     string
		wantState    string
		wantActiveID string
	}{
		{name: "edit_then_approve", decision: "edit_then_approve", wantState: "active", wantActiveID: "reopen-edit_then_approve-second-approval"},
		{name: "reject", decision: "reject", wantState: "rejected"},
		{name: "revoke", decision: "revoke", wantState: "revoked"},
		{name: "supersede", decision: "supersede", wantState: "superseded"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			root, store, first, second, before := approvalTransactionTwoEventFixture(t, testCase.decision, "reopen-"+testCase.name)
			reopened, err := newFileApprovalTransactionStoreForTest(root).Snapshot(context.Background())
			if err != nil {
				t.Fatalf("reopen snapshot: %v", err)
			}
			if !reflect.DeepEqual(reopened, before) {
				t.Fatalf("reopened snapshot differs from same-process snapshot:\nreopened=%#v\nbefore=%#v", reopened, before)
			}
			if len(reopened.Events) != 2 || len(reopened.Aggregates) != 1 || len(reopened.IdempotencyResults) != 2 || len(reopened.Outbox) != 2 {
				t.Fatalf("reopened lifecycle counts = events=%d aggregates=%d idempotency=%d outbox=%d, want 2/1/2/2", len(reopened.Events), len(reopened.Aggregates), len(reopened.IdempotencyResults), len(reopened.Outbox))
			}
			aggregate := reopened.Aggregates[0]
			if aggregate.Version != 2 || aggregate.State != testCase.wantState || aggregate.ActiveApprovalID != testCase.wantActiveID {
				t.Fatalf("reopened aggregate = state=%q version=%d active=%q, want state=%q version=2 active=%q", aggregate.State, aggregate.Version, aggregate.ActiveApprovalID, testCase.wantState, testCase.wantActiveID)
			}
			if len(aggregate.History) != 3 || aggregate.History[1].EventID != first.Event.EventID || aggregate.History[1].ApprovalID != first.Event.ApprovalID || aggregate.History[2].EventID != second.Event.EventID || aggregate.History[2].ApprovalID != second.Event.ApprovalID {
				t.Fatalf("reopened aggregate history = %#v, want first/second event order", aggregate.History)
			}
			if reopened.Events[0].EventID != first.Event.EventID || reopened.Events[1].EventID != second.Event.EventID || reopened.IdempotencyResults[0].EventID != first.Event.EventID || reopened.IdempotencyResults[1].EventID != second.Event.EventID || reopened.Outbox[0].EventID != first.Event.EventID || reopened.Outbox[1].EventID != second.Event.EventID {
				t.Fatalf("reopened durable ordering does not match event history: events=%#v idempotency=%#v outbox=%#v", reopened.Events, reopened.IdempotencyResults, reopened.Outbox)
			}
			if !reflect.DeepEqual(reopened, before) {
				t.Fatalf("reopened lifecycle changed after assertions")
			}
			_ = store
		})
	}
}

func TestApprovalTransactionReopenRejectsLifecycleCorruptionMatrix(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*sql.DB, ApprovalCommitReceipt) error
	}{
		{
			name: "missing-event",
			mutate: func(db *sql.DB, second ApprovalCommitReceipt) error {
				if _, err := db.Exec("DELETE FROM approval_outbox WHERE event_id = ?", second.Event.EventID); err != nil {
					return err
				}
				if _, err := db.Exec("DELETE FROM approval_idempotency WHERE event_id = ?", second.Event.EventID); err != nil {
					return err
				}
				if _, err := db.Exec("DELETE FROM approval_events WHERE event_id = ?", second.Event.EventID); err != nil {
					return err
				}
				var count int
				if err := db.QueryRow("SELECT count(*) FROM approval_events WHERE event_id = ?", second.Event.EventID).Scan(&count); err != nil {
					return err
				}
				if count != 0 {
					return errors.New("missing event tamper did not remove the event")
				}
				return nil
			},
		},
		{
			name: "duplicate-history-identity",
			mutate: func(db *sql.DB, second ApprovalCommitReceipt) error {
				var aggregateJSON []byte
				if err := db.QueryRow("SELECT aggregate_json FROM approval_targets WHERE aggregate_id = ?", second.Aggregate.AggregateID).Scan(&aggregateJSON); err != nil {
					return err
				}
				var aggregate ApprovalAggregateV2
				if err := decodeApprovalTransactionJSON(aggregateJSON, &aggregate); err != nil {
					return err
				}
				aggregate.History[2].EventID = aggregate.History[1].EventID
				updated, err := json.Marshal(aggregate)
				if err != nil {
					return err
				}
				if _, err := db.Exec("UPDATE approval_targets SET aggregate_json = ? WHERE aggregate_id = ?", updated, second.Aggregate.AggregateID); err != nil {
					return err
				}
				var tampered []byte
				if err := db.QueryRow("SELECT aggregate_json FROM approval_targets WHERE aggregate_id = ?", second.Aggregate.AggregateID).Scan(&tampered); err != nil {
					return err
				}
				var checked ApprovalAggregateV2
				if err := decodeApprovalTransactionJSON(tampered, &checked); err != nil {
					return err
				}
				if checked.History[2].EventID != checked.History[1].EventID {
					return errors.New("duplicate history tamper did not persist")
				}
				return nil
			},
		},
		{
			name: "aggregate-version-gap",
			mutate: func(db *sql.DB, second ApprovalCommitReceipt) error {
				var aggregateJSON []byte
				if err := db.QueryRow("SELECT aggregate_json FROM approval_targets WHERE aggregate_id = ?", second.Aggregate.AggregateID).Scan(&aggregateJSON); err != nil {
					return err
				}
				var aggregate ApprovalAggregateV2
				if err := decodeApprovalTransactionJSON(aggregateJSON, &aggregate); err != nil {
					return err
				}
				aggregate.History[2].Version = 3
				updated, err := json.Marshal(aggregate)
				if err != nil {
					return err
				}
				if _, err := db.Exec("UPDATE approval_targets SET aggregate_json = ? WHERE aggregate_id = ?", updated, second.Aggregate.AggregateID); err != nil {
					return err
				}
				var tampered []byte
				if err := db.QueryRow("SELECT aggregate_json FROM approval_targets WHERE aggregate_id = ?", second.Aggregate.AggregateID).Scan(&tampered); err != nil {
					return err
				}
				var checked ApprovalAggregateV2
				if err := decodeApprovalTransactionJSON(tampered, &checked); err != nil {
					return err
				}
				if checked.History[2].Version != 3 {
					return errors.New("aggregate version tamper did not persist")
				}
				return nil
			},
		},
		{
			name: "predecessor-corruption",
			mutate: func(db *sql.DB, second ApprovalCommitReceipt) error {
				mutated := second.Event
				mutated.PredecessorApprovalID = "replay-order-invalid-predecessor"
				payload := approvalTransactionEventPayload(mutated)
				if len(payload) == 0 {
					return errors.New("mutated event payload is empty")
				}
				digestBytes := sha256.Sum256(payload)
				digest := "sha256:" + hex.EncodeToString(digestBytes[:])
				mutatedOutbox := second.Outbox
				mutatedOutbox.PayloadDigest = digest
				mutatedOutbox.CommittedEvent.PayloadDigest = digest
				committedEventJSON, err := approvalTransactionSafeJSON(mutatedOutbox.CommittedEvent)
				if err != nil {
					return err
				}
				outboxJSON, err := approvalTransactionSafeJSON(mutatedOutbox)
				if err != nil {
					return err
				}
				if _, err := db.Exec("UPDATE approval_events SET predecessor_approval_id = ?, payload_digest = ?, payload_json = ? WHERE event_id = ?", mutated.PredecessorApprovalID, digest, payload, mutated.EventID); err != nil {
					return err
				}
				if _, err := db.Exec("UPDATE approval_outbox SET payload_digest = ?, committed_event_json = ?, outbox_json = ? WHERE event_id = ?", digest, committedEventJSON, outboxJSON, mutated.EventID); err != nil {
					return err
				}
				var predecessor, storedDigest string
				if err := db.QueryRow("SELECT predecessor_approval_id, payload_digest FROM approval_events WHERE event_id = ?", mutated.EventID).Scan(&predecessor, &storedDigest); err != nil {
					return err
				}
				if predecessor != mutated.PredecessorApprovalID || storedDigest != digest {
					return errors.New("predecessor tamper did not persist")
				}
				return nil
			},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			root, store, _, second, _ := approvalTransactionTwoEventFixture(t, "edit_then_approve", "corrupt-"+testCase.name)
			mutateApprovalTransactionReplayFixture(t, store.databasePath(), second, testCase.mutate)
			for name, candidate := range map[string]*ApprovalTransactionStore{
				"same store":     store,
				"reopened store": newFileApprovalTransactionStoreForTest(root),
			} {
				snapshot, err := candidate.Snapshot(context.Background())
				if err == nil || !errors.Is(err, ErrApprovalTransactionInvalid) && !errors.Is(err, ErrApprovalTransactionPersistence) {
					t.Fatalf("%s corrupted snapshot = %#v, err=%v, want typed failure", name, snapshot, err)
				}
				if !reflect.DeepEqual(snapshot, ApprovalTransactionSnapshot{}) {
					t.Fatalf("%s corrupted snapshot = %#v, want zero snapshot", name, snapshot)
				}
			}
		})
	}
}

func approvalTransactionTwoEventFixture(t *testing.T, decision, prefix string) (string, *ApprovalTransactionStore, ApprovalCommitReceipt, ApprovalCommitReceipt, ApprovalTransactionSnapshot) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "two-event-store")
	access := approvalCommandTestAccess(t)
	store := newFileApprovalTransactionStoreForTest(root)
	aggregateID := prefix + "-aggregate"
	firstMutation := approvalTransactionTestMutationWithAccess(t, access, prefix+"-first-key", prefix+"-first-command", "event-all-initial", "approval-all-initial", aggregateID)
	first, err := store.Commit(context.Background(), firstMutation)
	if err != nil {
		t.Fatalf("first lifecycle commit: %v", err)
	}
	var secondMutation *ValidatedApprovalMutation
	if decision == "reject" {
		secondMutation = approvalTransactionTestActiveRejectMutation(t, access, aggregateID, prefix)
	} else {
		secondMutation = approvalTransactionTestMutationForDecisionWithAccess(t, access, decision, aggregateID, prefix+"-second-event", prefix+"-second-approval", prefix+"-second-key")
	}
	second, err := store.Commit(context.Background(), secondMutation)
	if err != nil {
		t.Fatalf("second %s lifecycle commit: %v", decision, err)
	}
	snapshot, err := store.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("two-event snapshot: %v", err)
	}
	return root, store, first, second, snapshot
}

func approvalTransactionTestActiveRejectMutation(t *testing.T, access ApprovalAccess, aggregateID, prefix string) *ValidatedApprovalMutation {
	t.Helper()
	command, stored, before, after, _, active := approvalMeaningMutationTestInputsForDecisionWithAccess(t, "approve", access)
	active.AggregateID = aggregateID
	active.History[1].EventID = "event-all-initial"
	active.History[1].ApprovalID = "approval-all-initial"
	active.LastEventID = "event-all-initial"
	active.ActiveApprovalID = "approval-all-initial"
	command.command.CommandID = prefix + "-second-command"
	command.command.IdempotencyKey = prefix + "-second-key"
	command.command.Decision = "reject"
	command.command.ExpectedState = "active"
	command.command.ExpectedApprovalVersion = 1
	command.command.EditedText = nil
	command.command.PredecessorApprovalID = nil
	command.digest, _ = approvalCommandDigest(command.command)
	eventID := prefix + "-second-event"
	approvalID := prefix + "-second-approval"
	metadata := lifecycleTestMetadata(aggregateID, eventID, approvalID)
	event, aggregate, err := ReduceApprovalCommandV2(*active, command, metadata)
	if err != nil {
		t.Fatalf("reduce active reject: %v", err)
	}
	return mustApprovalMeaningMutation(t, command, stored, before, after, event, aggregate)
}

func mustApprovalMeaningMutation(t *testing.T, command *ValidatedApprovalCommandV2, stored *StoredProposal, before, after *SemanticMapIR, event *ApprovalEventV2, aggregate *ApprovalAggregateV2) *ValidatedApprovalMutation {
	t.Helper()
	mutation, err := ValidateApprovalMeaningMutation(command, stored, before, after, event, aggregate)
	if err != nil {
		t.Fatalf("build approval meaning mutation: %v", err)
	}
	return mutation
}

func mutateApprovalTransactionReplayFixture(t *testing.T, databasePath string, second ApprovalCommitReceipt, mutate func(*sql.DB, ApprovalCommitReceipt) error) {
	t.Helper()
	db, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatalf("open replay fixture database: %v", err)
	}
	for _, trigger := range approvalTransactionSQLiteTriggers {
		if _, err := db.Exec("DROP TRIGGER " + trigger); err != nil {
			_ = db.Close()
			t.Fatalf("drop replay fixture trigger %q: %v", trigger, err)
		}
	}
	if err := mutate(db, second); err != nil {
		_ = db.Close()
		t.Fatalf("mutate replay fixture: %v", err)
	}
	for _, statement := range approvalTransactionSQLiteSchema[len(approvalTransactionSQLiteSchema)-len(approvalTransactionSQLiteTriggers):] {
		if _, err := db.Exec(statement); err != nil {
			_ = db.Close()
			t.Fatalf("restore replay fixture trigger: %v", err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close replay fixture database: %v", err)
	}
}

func TestApprovalTransactionReplayAfterStoreReopenReturnsOriginalProjection(t *testing.T) {
	root := filepath.Join(t.TempDir(), "reopen-store")
	mutation := approvalTransactionTestMutation(t, "idempotency-reopen", "command-reopen", "event-reopen", "approval-reopen", "aggregate-reopen")
	firstStore := newFileApprovalTransactionStoreForTest(root)
	first, err := firstStore.Commit(context.Background(), mutation)
	if err != nil {
		t.Fatalf("first commit: %v", err)
	}
	second, err := newFileApprovalTransactionStoreForTest(root).Commit(context.Background(), mutation)
	if err != nil {
		t.Fatalf("reopened replay: %v", err)
	}
	if !second.Replayed || second.IdempotencyResult != first.IdempotencyResult || second.Event != first.Event || !reflect.DeepEqual(second.Aggregate, first.Aggregate) || second.Outbox != first.Outbox || !bytes.Equal(second.EventPayload, first.EventPayload) {
		t.Fatal("reopened replay did not preserve original result")
	}
}

func TestApprovalTransactionSameKeyDifferentDigestConflictsWithoutWriting(t *testing.T) {
	access := approvalCommandTestAccess(t)
	firstMutation := approvalTransactionTestMutationWithAccess(t, access, "idempotency-collision", "command-collision-a", "event-collision-a", "approval-collision-a", "aggregate-collision-a")
	secondMutation := approvalTransactionTestMutationWithAccess(t, access, "idempotency-collision", "command-collision-b", "event-collision-b", "approval-collision-b", "aggregate-collision-b")
	store := newFileApprovalTransactionStoreForTest(t.TempDir())
	if _, err := store.Commit(context.Background(), firstMutation); err != nil {
		t.Fatalf("first commit: %v", err)
	}
	before, err := store.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("snapshot before collision: %v", err)
	}
	if _, err := store.Commit(context.Background(), secondMutation); !errors.Is(err, ErrApprovalTransactionConflict) {
		t.Fatalf("collision error = %v, want conflict", err)
	}
	after, err := store.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("snapshot after collision: %v", err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("idempotency collision changed records")
	}
}

func TestApprovalTransactionSameTargetDifferentAggregateIDConflicts(t *testing.T) {
	access := approvalCommandTestAccess(t)
	firstMutation := approvalTransactionTestMutationWithAccess(t, access, "idempotency-target-a", "command-target-a", "event-target-a", "approval-target-a", "aggregate-target-a")
	secondMutation := approvalTransactionTestMutationWithAccess(t, access, "idempotency-target-b", "command-target-b", "event-target-b", "approval-target-b", "aggregate-target-b")
	store := newFileApprovalTransactionStoreForTest(t.TempDir())
	if _, err := store.Commit(context.Background(), firstMutation); err != nil {
		t.Fatalf("first target commit: %v", err)
	}
	if _, err := store.Commit(context.Background(), secondMutation); !errors.Is(err, ErrApprovalTransactionConflict) {
		t.Fatalf("target fork error = %v, want conflict", err)
	}
	snapshot, err := store.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if len(snapshot.Events) != 1 || len(snapshot.IdempotencyResults) != 1 || len(snapshot.Outbox) != 1 {
		t.Fatalf("fork changed record counts: %#v", snapshot)
	}
}

func TestApprovalTransactionConcurrentDifferentKeysOnlyOneExpectedVersionWins(t *testing.T) {
	access := approvalCommandTestAccess(t)
	firstMutation := approvalTransactionTestMutationWithAccess(t, access, "idempotency-concurrent-a", "command-concurrent-a", "event-concurrent-a", "approval-concurrent-a", "aggregate-concurrent-a")
	secondMutation := approvalTransactionTestMutationWithAccess(t, access, "idempotency-concurrent-b", "command-concurrent-b", "event-concurrent-b", "approval-concurrent-b", "aggregate-concurrent-b")
	store := newFileApprovalTransactionStoreForTest(t.TempDir())
	var wait sync.WaitGroup
	errs := make(chan error, 2)
	wait.Add(2)
	go func() { defer wait.Done(); _, err := store.Commit(context.Background(), firstMutation); errs <- err }()
	go func() { defer wait.Done(); _, err := store.Commit(context.Background(), secondMutation); errs <- err }()
	wait.Wait()
	close(errs)
	var success, conflict int
	for err := range errs {
		switch {
		case err == nil:
			success++
		case errors.Is(err, ErrApprovalTransactionConflict):
			conflict++
		default:
			t.Fatalf("concurrent commit error = %v", err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("concurrent outcomes success=%d conflict=%d", success, conflict)
	}
}

func TestApprovalTransactionConcurrentSameKeyReturnsOneDurableResult(t *testing.T) {
	mutation := approvalTransactionTestMutation(t, "idempotency-same-concurrent", "command-same-concurrent", "event-same-concurrent", "approval-same-concurrent", "aggregate-same-concurrent")
	store := newFileApprovalTransactionStoreForTest(t.TempDir())
	var wait sync.WaitGroup
	errs := make(chan error, 2)
	receipts := make(chan ApprovalCommitReceipt, 2)
	wait.Add(2)
	for range 2 {
		go func() {
			defer wait.Done()
			receipt, err := store.Commit(context.Background(), mutation)
			receipts <- receipt
			errs <- err
		}()
	}
	wait.Wait()
	close(errs)
	close(receipts)
	for err := range errs {
		if err != nil {
			t.Fatalf("same-key concurrent commit: %v", err)
		}
	}
	var got []ApprovalCommitReceipt
	for receipt := range receipts {
		got = append(got, receipt)
	}
	if len(got) != 2 || got[0].Event != got[1].Event || !bytes.Equal(got[0].EventPayload, got[1].EventPayload) {
		t.Fatal("same-key receipts diverged")
	}
	snapshot, err := store.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if len(snapshot.Events) != 1 || len(snapshot.IdempotencyResults) != 1 || len(snapshot.Outbox) != 1 {
		t.Fatalf("same-key record counts = %#v", snapshot)
	}
}

func TestNewApprovalTransactionStoreRequiresValidatedActiveProof(t *testing.T) {
	root := t.TempDir()
	engine, err := workspace.NewSnapshotEngine(root, 0)
	if err != nil {
		t.Fatalf("workspace engine: %v", err)
	}
	mutation := approvalTransactionTestMutation(t, "idempotency-without-proof", "command-without-proof", "event-without-proof", "approval-without-proof", "aggregate-without-proof")

	receipt, err := NewApprovalTransactionStore(root, engine).Commit(context.Background(), mutation)
	if err == nil {
		t.Fatalf("production store committed without a validated active proof: receipt=%+v", receipt)
	}
	if !errors.Is(err, ErrApprovalTransactionPersistence) {
		t.Fatalf("missing active proof error = %v, want persistence", err)
	}
	if !reflect.DeepEqual(receipt, ApprovalCommitReceipt{}) {
		t.Fatalf("missing active proof returned non-zero receipt: %+v", receipt)
	}
	if _, statErr := os.Stat(filepath.Join(root, ".codeflow", "approval-transactions")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("missing active proof created approval artifacts: stat error=%v", statErr)
	}
}

func TestApprovalTransactionIndependentProcessesDifferentKeysOnlyOneWins(t *testing.T) {
	root := filepath.Join(t.TempDir(), "cross-process-different-keys")
	outcomes := runApprovalTransactionSubprocesses(t, root, false)
	var successes, conflicts int
	var winner approvalTransactionSubprocessResult
	var conflict approvalTransactionSubprocessResult
	for _, outcome := range outcomes {
		if outcome.resultErr != nil {
			t.Fatalf("cross-process different-key result: %v", outcome.resultErr)
		}
		switch outcome.exitCode {
		case 0:
			successes++
			if outcome.result.Outcome != "committed" || outcome.result.Replayed || outcome.result.EventID == "" || outcome.result.ApprovalID == "" || outcome.result.AggregateID == "" || outcome.result.State != "active" || outcome.result.Version != 1 || outcome.result.CurrentState != nil || outcome.result.CurrentVersion != nil {
				t.Fatalf("cross-process different-key success result = %#v, want one non-replayed active/1 commit", outcome.result)
			}
			winner = outcome.result
		case 2:
			conflicts++
			if outcome.result.Outcome != "conflict" || outcome.result.Replayed || outcome.result.EventID != "" || outcome.result.ApprovalID != "" || outcome.result.AggregateID != "" || outcome.result.State != "active" || outcome.result.Version != 1 || outcome.result.CurrentState == nil || *outcome.result.CurrentState != "active" || outcome.result.CurrentVersion == nil || *outcome.result.CurrentVersion != 1 {
				t.Fatalf("cross-process different-key conflict result = %#v, want active/1 conflict with current state/version", outcome.result)
			}
			conflict = outcome.result
		default:
			t.Fatalf("cross-process different-key exit=%d output=%s", outcome.exitCode, outcome.output)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("cross-process different-key outcomes successes=%d conflicts=%d", successes, conflicts)
	}
	snapshot, err := newFileApprovalTransactionStoreForTest(root).Snapshot(context.Background())
	if err != nil {
		t.Fatalf("cross-process different-key snapshot: %v", err)
	}
	if len(snapshot.Events) != 1 || len(snapshot.Aggregates) != 1 || len(snapshot.IdempotencyResults) != 1 || len(snapshot.Outbox) != 1 {
		t.Fatalf("cross-process different-key records=%#v", snapshot)
	}
	event := snapshot.Events[0]
	aggregate := snapshot.Aggregates[0]
	idempotency := snapshot.IdempotencyResults[0]
	outbox := snapshot.Outbox[0]
	if event.EventID != winner.EventID || event.ApprovalID != winner.ApprovalID || event.AggregateID != winner.AggregateID || aggregate.AggregateID != winner.AggregateID || aggregate.LastEventID != winner.EventID || aggregate.ActiveApprovalID != winner.ApprovalID || len(aggregate.History) < 2 || aggregate.History[len(aggregate.History)-1].EventID != winner.EventID || aggregate.History[len(aggregate.History)-1].ApprovalID != winner.ApprovalID || idempotency.EventID != winner.EventID || idempotency.ApprovalID != winner.ApprovalID || idempotency.AggregateID != winner.AggregateID || idempotency.OriginalResult.EventID != winner.EventID || idempotency.OriginalResult.ApprovalID != winner.ApprovalID || idempotency.OriginalResult.AggregateID != winner.AggregateID || outbox.EventID != winner.EventID || outbox.AggregateID != winner.AggregateID || outbox.CommittedEvent.EventID != winner.EventID || outbox.CommittedEvent.ApprovalID != winner.ApprovalID || outbox.CommittedEvent.AggregateID != winner.AggregateID {
		t.Fatalf("cross-process different-key winner result does not identify durable records: result=%#v snapshot=%#v", winner, snapshot)
	}
	if conflict.EventID != "" || conflict.ApprovalID != "" || conflict.AggregateID != "" {
		t.Fatalf("cross-process loser exposed durable identity: %#v", conflict)
	}
}

func TestApprovalTransactionIndependentProcessesSameKeyReplayOnce(t *testing.T) {
	root := filepath.Join(t.TempDir(), "cross-process-same-key")
	outcomes := runApprovalTransactionSubprocesses(t, root, true)
	var commits []approvalTransactionSubprocessResult
	for _, outcome := range outcomes {
		if outcome.resultErr != nil {
			t.Fatalf("cross-process same-key result: %v", outcome.resultErr)
		}
		if outcome.exitCode != 0 || outcome.result.Outcome != "committed" || outcome.result.EventID == "" || outcome.result.ApprovalID == "" || outcome.result.AggregateID == "" || outcome.result.State != "active" || outcome.result.Version != 1 || outcome.result.CurrentState != nil || outcome.result.CurrentVersion != nil {
			t.Fatalf("cross-process same-key exit=%d output=%s", outcome.exitCode, outcome.output)
		}
		commits = append(commits, outcome.result)
	}
	if len(commits) != 2 || commits[0].Replayed == commits[1].Replayed || commits[0].EventID != commits[1].EventID || commits[0].ApprovalID != commits[1].ApprovalID || commits[0].AggregateID != commits[1].AggregateID {
		t.Fatalf("cross-process same-key results = %#v, want one original and one exact replay", commits)
	}
	snapshot, err := newFileApprovalTransactionStoreForTest(root).Snapshot(context.Background())
	if err != nil {
		t.Fatalf("cross-process same-key snapshot: %v", err)
	}
	if len(snapshot.Events) != 1 || len(snapshot.Aggregates) != 1 || len(snapshot.IdempotencyResults) != 1 || len(snapshot.Outbox) != 1 {
		t.Fatalf("cross-process same-key records=%#v", snapshot)
	}
	if snapshot.Events[0].EventID != commits[0].EventID || snapshot.Events[0].ApprovalID != commits[0].ApprovalID || snapshot.Events[0].AggregateID != commits[0].AggregateID || snapshot.Aggregates[0].State != "active" || snapshot.Aggregates[0].Version != 1 || snapshot.IdempotencyResults[0].EventID != commits[0].EventID || snapshot.Outbox[0].EventID != commits[0].EventID {
		t.Fatalf("cross-process same-key result does not identify durable records: results=%#v snapshot=%#v", commits, snapshot)
	}
}

func TestApprovalTransactionIndependentProcessesPublishInitializedDatabaseAtomically(t *testing.T) {
	root := filepath.Join(t.TempDir(), "cross-process-initialization")
	parent := filepath.Dir(root)
	startPath := filepath.Join(parent, "initialization-start")
	pausedPath := filepath.Join(parent, "initialization-paused")
	releasePath := filepath.Join(parent, "initialization-release")
	resultPaths := []string{
		filepath.Join(parent, "initialization-result-0"),
		filepath.Join(parent, "initialization-result-1"),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	type processOutcome struct {
		index  int
		code   int
		output string
		err    error
	}
	outcomes := make(chan processOutcome, 2)
	commands := make([]*exec.Cmd, 0, 2)
	for index := range 2 {
		key := "atomic-initialization-key"
		command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestApprovalTransactionSubprocessHelper$", "-test.v")
		command.WaitDelay = time.Second
		command.Env = append(os.Environ(),
			"CODEFLOW_APPROVAL_TRANSACTION_HELPER=1",
			"CODEFLOW_APPROVAL_TRANSACTION_ROOT="+root,
			"CODEFLOW_APPROVAL_TRANSACTION_READY="+filepath.Join(parent, "initialization-ready-"+strconv.Itoa(index)),
			"CODEFLOW_APPROVAL_TRANSACTION_START="+startPath,
			"CODEFLOW_APPROVAL_TRANSACTION_RESULT="+resultPaths[index],
			"CODEFLOW_APPROVAL_TRANSACTION_KEY="+key,
			"CODEFLOW_APPROVAL_TRANSACTION_COMMAND=atomic-initialization-command",
			"CODEFLOW_APPROVAL_TRANSACTION_EVENT=atomic-initialization-event",
			"CODEFLOW_APPROVAL_TRANSACTION_APPROVAL=atomic-initialization-approval",
			"CODEFLOW_APPROVAL_TRANSACTION_INDEX="+strconv.Itoa(index),
		)
		if index == 0 {
			command.Env = append(command.Env,
				"CODEFLOW_APPROVAL_TRANSACTION_INIT_PAUSE="+pausedPath,
				"CODEFLOW_APPROVAL_TRANSACTION_INIT_RELEASE="+releasePath,
			)
		} else {
			command.Env = append(command.Env, "CODEFLOW_APPROVAL_TRANSACTION_INIT_WAIT="+pausedPath)
		}
		commands = append(commands, command)
		go func(index int, command *exec.Cmd) {
			output, err := command.CombinedOutput()
			code := 0
			if err != nil {
				if exitError, ok := err.(*exec.ExitError); ok && exitError.ProcessState != nil {
					code = exitError.ProcessState.ExitCode()
				} else {
					code = -1
				}
			}
			outcomes <- processOutcome{index: index, code: code, output: string(output), err: err}
		}(index, command)
	}
	waitForPath := func(path string) error {
		for {
			if _, err := os.Stat(path); err == nil {
				return nil
			} else if !errors.Is(err, os.ErrNotExist) {
				return err
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(5 * time.Millisecond):
			}
		}
	}
	readyPaths := []string{filepath.Join(parent, "initialization-ready-0"), filepath.Join(parent, "initialization-ready-1")}
	for _, readyPath := range readyPaths {
		if err := waitForPath(readyPath); err != nil {
			cancel()
			for range commands {
				<-outcomes
			}
			t.Fatalf("initialization helper readiness: %v", err)
		}
	}
	if err := os.WriteFile(startPath, []byte("start"), 0o600); err != nil {
		cancel()
		for range commands {
			<-outcomes
		}
		t.Fatalf("write initialization start: %v", err)
	}
	if err := waitForPath(pausedPath); err != nil {
		cancel()
		for range commands {
			<-outcomes
		}
		t.Fatalf("initialization did not pause before publication: %v", err)
	}
	if err := waitForPath(resultPaths[1]); err != nil {
		cancel()
		for range commands {
			<-outcomes
		}
		t.Fatalf("uninvolved initializer did not finish: %v", err)
	}
	if err := os.WriteFile(releasePath, []byte("release"), 0o600); err != nil {
		cancel()
		for range commands {
			<-outcomes
		}
		t.Fatalf("write initialization release: %v", err)
	}
	results := make([]processOutcome, 2)
	for range commands {
		select {
		case outcome := <-outcomes:
			results[outcome.index] = outcome
		case <-ctx.Done():
			t.Fatalf("initialization helper completion: %v", ctx.Err())
		}
	}
	subprocessResults := make([]approvalTransactionSubprocessResult, 2)
	for _, outcome := range results {
		if outcome.err != nil {
			t.Fatalf("initialization helper %d: code=%d output=%s err=%v", outcome.index, outcome.code, outcome.output, outcome.err)
		}
		result, err := readApprovalTransactionSubprocessResult(resultPaths[outcome.index])
		if err != nil {
			t.Fatalf("initialization helper %d result: %v", outcome.index, err)
		}
		if result.Outcome != "committed" || result.EventID == "" || result.ApprovalID == "" || result.AggregateID == "" || result.State != "active" || result.Version != 1 {
			t.Fatalf("initialization helper %d result=%#v, want committed active/1", outcome.index, result)
		}
		subprocessResults[outcome.index] = result
	}
	if subprocessResults[0].Replayed == subprocessResults[1].Replayed || subprocessResults[0].EventID != subprocessResults[1].EventID || subprocessResults[0].ApprovalID != subprocessResults[1].ApprovalID || subprocessResults[0].AggregateID != subprocessResults[1].AggregateID || subprocessResults[0].State != subprocessResults[1].State || subprocessResults[0].Version != subprocessResults[1].Version {
		t.Fatalf("initialization helper replay results=%#v, want one original and one exact replay", subprocessResults)
	}
	snapshot, err := newFileApprovalTransactionStoreForTest(root).Snapshot(context.Background())
	if err != nil {
		t.Fatalf("atomic initialization snapshot: %v", err)
	}
	if len(snapshot.Events) != 1 || len(snapshot.Aggregates) != 1 || len(snapshot.IdempotencyResults) != 1 || len(snapshot.Outbox) != 1 {
		t.Fatalf("atomic initialization records=%#v, want one durable record set", snapshot)
	}
	winner := subprocessResults[0]
	event := snapshot.Events[0]
	aggregate := snapshot.Aggregates[0]
	idempotency := snapshot.IdempotencyResults[0]
	outbox := snapshot.Outbox[0]
	if event.EventID != winner.EventID || event.ApprovalID != winner.ApprovalID || event.AggregateID != winner.AggregateID || aggregate.AggregateID != winner.AggregateID || aggregate.LastEventID != winner.EventID || aggregate.ActiveApprovalID != winner.ApprovalID || len(aggregate.History) < 2 || aggregate.History[len(aggregate.History)-1].EventID != winner.EventID || aggregate.History[len(aggregate.History)-1].ApprovalID != winner.ApprovalID || idempotency.EventID != winner.EventID || idempotency.ApprovalID != winner.ApprovalID || idempotency.AggregateID != winner.AggregateID || idempotency.OriginalResult.EventID != winner.EventID || idempotency.OriginalResult.ApprovalID != winner.ApprovalID || idempotency.OriginalResult.AggregateID != winner.AggregateID || outbox.EventID != winner.EventID || outbox.AggregateID != winner.AggregateID || outbox.CommittedEvent.EventID != winner.EventID || outbox.CommittedEvent.ApprovalID != winner.ApprovalID || outbox.CommittedEvent.AggregateID != winner.AggregateID {
		t.Fatalf("atomic initialization winner does not identify durable records: result=%#v snapshot=%#v", winner, snapshot)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read atomic initialization root: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != approvalTransactionDatabaseName {
		t.Fatalf("atomic initialization left temporary artifacts: %#v", entries)
	}
}

func TestApprovalTransactionIndependentProcessesLoserCleanupFailureIsVisible(t *testing.T) {
	root := filepath.Join(t.TempDir(), "cross-process-initialization-cleanup")
	cleanupPath := filepath.Join(filepath.Dir(root), "initialization-cleanup-observed")
	results := runApprovalTransactionInitializationCleanupRace(t, root, cleanupPath)
	if results[0].resultErr != nil || results[0].result.Outcome != "persistence-error" || results[0].result.ErrorKind != "cleanup" || results[0].result.Replayed || results[0].result.EventID != "" || results[0].result.ApprovalID != "" || results[0].result.AggregateID != "" || results[0].result.CurrentState != nil || results[0].result.CurrentVersion != nil || results[0].exitCode == 0 {
		t.Fatalf("losing initializer result=%#v, code=%d err=%v output=%s, want visible cleanup persistence failure", results[0].result, results[0].exitCode, results[0].resultErr, results[0].output)
	}
	if results[1].resultErr != nil || results[1].result.Outcome != "committed" || results[1].result.Replayed || results[1].result.EventID == "" || results[1].result.AggregateID == "" || results[1].result.State != "active" || results[1].result.Version != 1 {
		t.Fatalf("winning initializer result=%#v, code=%d err=%v output=%s, want one committed active/1 result", results[1].result, results[1].exitCode, results[1].resultErr, results[1].output)
	}
	cleanupTargetBytes, err := os.ReadFile(cleanupPath)
	if err != nil {
		t.Fatalf("read cleanup observation: %v", err)
	}
	cleanupTarget := string(cleanupTargetBytes)
	resolvedCleanupDir, cleanupResolveErr := filepath.EvalSymlinks(filepath.Dir(cleanupTarget))
	resolvedRoot, rootResolveErr := filepath.EvalSymlinks(root)
	if cleanupResolveErr != nil || rootResolveErr != nil || resolvedCleanupDir != resolvedRoot || !strings.Contains(filepath.Base(cleanupTarget), ".tmp-") {
		t.Fatalf("cleanup target=%q, want temporary artifact directly under root", cleanupTarget)
	}
	if _, err := os.Lstat(cleanupTarget); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("loser temporary artifact remains after injected cleanup: %v", err)
	}
	snapshot, err := newFileApprovalTransactionStoreForTest(root).Snapshot(context.Background())
	if err != nil {
		t.Fatalf("loser cleanup snapshot: %v", err)
	}
	if len(snapshot.Events) != 1 || len(snapshot.Aggregates) != 1 || len(snapshot.IdempotencyResults) != 1 || len(snapshot.Outbox) != 1 {
		t.Fatalf("loser cleanup records=%#v, want one durable record set", snapshot)
	}
	winner := results[1].result
	event := snapshot.Events[0]
	aggregate := snapshot.Aggregates[0]
	idempotency := snapshot.IdempotencyResults[0]
	outbox := snapshot.Outbox[0]
	if event.EventID != winner.EventID || event.ApprovalID != winner.ApprovalID || event.AggregateID != winner.AggregateID || aggregate.AggregateID != winner.AggregateID || aggregate.LastEventID != winner.EventID || aggregate.ActiveApprovalID != winner.ApprovalID || len(aggregate.History) < 2 || aggregate.History[len(aggregate.History)-1].EventID != winner.EventID || aggregate.History[len(aggregate.History)-1].ApprovalID != winner.ApprovalID || idempotency.EventID != winner.EventID || idempotency.ApprovalID != winner.ApprovalID || idempotency.AggregateID != winner.AggregateID || idempotency.OriginalResult.EventID != winner.EventID || idempotency.OriginalResult.ApprovalID != winner.ApprovalID || idempotency.OriginalResult.AggregateID != winner.AggregateID || outbox.EventID != winner.EventID || outbox.AggregateID != winner.AggregateID || outbox.CommittedEvent.EventID != winner.EventID || outbox.CommittedEvent.ApprovalID != winner.ApprovalID || outbox.CommittedEvent.AggregateID != winner.AggregateID {
		t.Fatalf("loser cleanup winner result does not identify durable records: result=%#v snapshot=%#v", winner, snapshot)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read loser cleanup root: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != approvalTransactionDatabaseName {
		t.Fatalf("loser cleanup left temporary artifacts: %#v", entries)
	}
}

var errApprovalTransactionTestInitializationCleanup = errors.New("injected initialization cleanup failure")

func runApprovalTransactionInitializationCleanupRace(t *testing.T, root, cleanupPath string) []approvalTransactionSubprocessOutcome {
	t.Helper()
	parent := filepath.Dir(root)
	startPath := filepath.Join(parent, "cleanup-initialization-start")
	pausedPath := filepath.Join(parent, "cleanup-initialization-paused")
	releasePath := filepath.Join(parent, "cleanup-initialization-release")
	readyPaths := []string{filepath.Join(parent, "cleanup-initialization-ready-0"), filepath.Join(parent, "cleanup-initialization-ready-1")}
	resultPaths := []string{filepath.Join(parent, "cleanup-initialization-result-0"), filepath.Join(parent, "cleanup-initialization-result-1")}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	commands := make([]*exec.Cmd, 0, 2)
	outputs := make(chan approvalTransactionSubprocessOutcome, 2)
	for index := range 2 {
		command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestApprovalTransactionSubprocessHelper$", "-test.v")
		command.WaitDelay = time.Second
		command.Env = append(os.Environ(),
			"CODEFLOW_APPROVAL_TRANSACTION_HELPER=1",
			"CODEFLOW_APPROVAL_TRANSACTION_ROOT="+root,
			"CODEFLOW_APPROVAL_TRANSACTION_READY="+readyPaths[index],
			"CODEFLOW_APPROVAL_TRANSACTION_START="+startPath,
			"CODEFLOW_APPROVAL_TRANSACTION_RESULT="+resultPaths[index],
			"CODEFLOW_APPROVAL_TRANSACTION_KEY=cleanup-initialization-key",
			"CODEFLOW_APPROVAL_TRANSACTION_COMMAND=cleanup-initialization-command",
			"CODEFLOW_APPROVAL_TRANSACTION_EVENT=cleanup-initialization-event",
			"CODEFLOW_APPROVAL_TRANSACTION_APPROVAL=cleanup-initialization-approval",
			"CODEFLOW_APPROVAL_TRANSACTION_INDEX="+strconv.Itoa(index),
		)
		if index == 0 {
			command.Env = append(command.Env,
				"CODEFLOW_APPROVAL_TRANSACTION_INIT_PAUSE="+pausedPath,
				"CODEFLOW_APPROVAL_TRANSACTION_INIT_RELEASE="+releasePath,
				"CODEFLOW_APPROVAL_TRANSACTION_INIT_CLEANUP="+cleanupPath,
			)
		} else {
			command.Env = append(command.Env, "CODEFLOW_APPROVAL_TRANSACTION_INIT_WAIT="+pausedPath)
		}
		commands = append(commands, command)
		go func(index int, command *exec.Cmd) {
			output, err := command.CombinedOutput()
			code := 0
			if err != nil {
				if exitError, ok := err.(*exec.ExitError); ok && exitError.ProcessState != nil {
					code = exitError.ProcessState.ExitCode()
				} else {
					code = -1
				}
			}
			outputs <- approvalTransactionSubprocessOutcome{exitCode: code, output: string(output), resultPath: resultPaths[index]}
		}(index, command)
	}
	waitForPath := func(path string) error {
		for {
			if _, err := os.Stat(path); err == nil {
				return nil
			} else if !errors.Is(err, os.ErrNotExist) {
				return err
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(5 * time.Millisecond):
			}
		}
	}
	stopAndDrain := func() {
		cancel()
		for range commands {
			<-outputs
		}
	}
	for _, readyPath := range readyPaths {
		if err := waitForPath(readyPath); err != nil {
			stopAndDrain()
			t.Fatalf("cleanup initializer readiness: %v", err)
		}
	}
	if err := os.WriteFile(startPath, []byte("start"), 0o600); err != nil {
		stopAndDrain()
		t.Fatalf("write cleanup initializer start: %v", err)
	}
	if err := waitForPath(pausedPath); err != nil {
		stopAndDrain()
		t.Fatalf("cleanup initializer did not pause: %v", err)
	}
	if err := waitForPath(resultPaths[1]); err != nil {
		stopAndDrain()
		t.Fatalf("cleanup initializer winner did not finish: %v", err)
	}
	if err := os.WriteFile(releasePath, []byte("release"), 0o600); err != nil {
		stopAndDrain()
		t.Fatalf("write cleanup initializer release: %v", err)
	}
	if err := waitForPath(cleanupPath); err != nil {
		stopAndDrain()
		t.Fatalf("loser cleanup was not observed: %v", err)
	}
	results := make([]approvalTransactionSubprocessOutcome, 2)
	for range commands {
		select {
		case outcome := <-outputs:
			index := 0
			if outcome.resultPath == resultPaths[1] {
				index = 1
			}
			results[index] = outcome
		case <-ctx.Done():
			stopAndDrain()
			t.Fatalf("cleanup initializer completion: %v", ctx.Err())
		}
	}
	for index := range results {
		results[index].result, results[index].resultErr = readApprovalTransactionSubprocessResult(results[index].resultPath)
	}
	return results
}

func TestApprovalTransactionSubprocessHarnessBoundsHangingHelper(t *testing.T) {
	for _, mode := range []string{"completion", "ready"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("CODEFLOW_APPROVAL_TRANSACTION_HANG", mode)
			t.Setenv("CODEFLOW_APPROVAL_TRANSACTION_TIMEOUT_MS", "150")
			started := time.Now()
			outcomes := runApprovalTransactionSubprocesses(t, filepath.Join(t.TempDir(), "cross-process-hang"), false)
			if elapsed := time.Since(started); elapsed > 2*time.Second {
				t.Fatalf("hanging helper harness took %s, want bounded cleanup", elapsed)
			}
			for _, outcome := range outcomes {
				if outcome.exitCode == 0 || outcome.resultErr == nil {
					t.Fatalf("hanging helper outcome = %#v, want terminated process and missing result", outcome)
				}
			}
		})
	}
}

type approvalTransactionSubprocessResult struct {
	Outcome        string  `json:"outcome"`
	ErrorKind      string  `json:"errorKind,omitempty"`
	Replayed       bool    `json:"replayed"`
	EventID        string  `json:"eventId"`
	ApprovalID     string  `json:"approvalId"`
	AggregateID    string  `json:"aggregateId"`
	State          string  `json:"state"`
	Version        int64   `json:"version"`
	CurrentState   *string `json:"currentState,omitempty"`
	CurrentVersion *int64  `json:"currentVersion,omitempty"`
}

type approvalTransactionSubprocessOutcome struct {
	exitCode   int
	output     string
	resultPath string
	result     approvalTransactionSubprocessResult
	resultErr  error
}

const approvalTransactionSubprocessResultMaxBytes = 16 << 10

func readApprovalTransactionSubprocessResult(path string) (approvalTransactionSubprocessResult, error) {
	var zero approvalTransactionSubprocessResult
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || info.Size() <= 0 || info.Size() > approvalTransactionSubprocessResultMaxBytes {
		return zero, errors.New("cross-process result is missing or unsafe")
	}
	file, err := os.Open(path)
	if err != nil {
		return zero, errors.New("cross-process result is unreadable")
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, approvalTransactionSubprocessResultMaxBytes+1))
	if err != nil || len(data) == 0 || len(data) > approvalTransactionSubprocessResultMaxBytes {
		return zero, errors.New("cross-process result is oversized")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil {
		return zero, errors.New("cross-process result is malformed")
	}
	delimiter, ok := token.(json.Delim)
	if !ok || delimiter != '{' {
		return zero, errors.New("cross-process result is not an object")
	}
	allowed := map[string]bool{
		"outcome": true, "replayed": true, "eventId": true, "approvalId": true,
		"aggregateId": true, "state": true, "version": true, "currentState": true,
		"currentVersion": true, "errorKind": true,
	}
	required := []string{"outcome", "replayed", "eventId", "approvalId", "aggregateId", "state", "version"}
	fields := make(map[string]json.RawMessage, len(allowed))
	for decoder.More() {
		keyToken, err := decoder.Token()
		key, keyOK := keyToken.(string)
		if err != nil || !keyOK || !allowed[key] || fields[key] != nil {
			return zero, errors.New("cross-process result has an unknown or duplicate field")
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil || string(value) == "null" {
			return zero, errors.New("cross-process result has an invalid field")
		}
		fields[key] = value
	}
	if closing, err := decoder.Token(); err != nil || closing != json.Delim('}') {
		return zero, errors.New("cross-process result is malformed")
	}
	if _, err := decoder.Token(); err != io.EOF {
		return zero, errors.New("cross-process result has trailing data")
	}
	for _, key := range required {
		if fields[key] == nil {
			return zero, errors.New("cross-process result is incomplete")
		}
	}
	if (fields["currentState"] == nil) != (fields["currentVersion"] == nil) {
		return zero, errors.New("cross-process result has incomplete conflict identity")
	}
	var result approvalTransactionSubprocessResult
	strictDecoder := json.NewDecoder(bytes.NewReader(data))
	strictDecoder.DisallowUnknownFields()
	if err := strictDecoder.Decode(&result); err != nil {
		return zero, errors.New("cross-process result has invalid types")
	}
	if _, err := strictDecoder.Token(); err != io.EOF {
		return zero, errors.New("cross-process result has trailing data")
	}
	if result.Outcome != "committed" && result.Outcome != "conflict" && result.Outcome != "persistence-error" {
		return zero, errors.New("cross-process result has invalid outcome")
	}
	if result.State != "active" || result.Version != 1 {
		return zero, errors.New("cross-process result has invalid state")
	}
	if result.Outcome == "committed" {
		if result.EventID == "" || result.ApprovalID == "" || result.AggregateID == "" || result.CurrentState != nil || result.CurrentVersion != nil {
			return zero, errors.New("cross-process commit result is incomplete")
		}
	} else if result.Outcome == "conflict" && (result.Replayed || result.EventID != "" || result.ApprovalID != "" || result.AggregateID != "" || result.CurrentState == nil || result.CurrentVersion == nil || *result.CurrentState != "active" || *result.CurrentVersion != 1) {
		return zero, errors.New("cross-process conflict result is incomplete")
	} else if result.Outcome == "persistence-error" && (result.ErrorKind != "cleanup" || result.Replayed || result.EventID != "" || result.ApprovalID != "" || result.AggregateID != "" || result.CurrentState != nil || result.CurrentVersion != nil) {
		return zero, errors.New("cross-process persistence result is incomplete")
	}
	return result, nil
}

func TestApprovalTransactionSubprocessResultParserRejectsUntrustedOutput(t *testing.T) {
	valid := `{"outcome":"conflict","replayed":false,"eventId":"","approvalId":"","aggregateId":"","state":"active","version":1,"currentState":"active","currentVersion":1}`
	cases := map[string]string{
		"missing-field":     strings.Replace(valid, `,"version":1`, "", 1),
		"duplicate-field":   strings.Replace(valid, `"state":"active"`, `"state":"active","state":"active"`, 1),
		"unknown-field":     strings.Replace(valid, `}`, `,"unexpected":true}`, 1),
		"trailing-json":     valid + `{}`,
		"arbitrary-state":   strings.Replace(valid, `"state":"active"`, `"state":"rejected"`, 1),
		"arbitrary-version": strings.Replace(valid, `"version":1`, `"version":2`, 1),
	}
	for name, contents := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "result.json")
			if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
				t.Fatalf("write result: %v", err)
			}
			if _, err := readApprovalTransactionSubprocessResult(path); err == nil {
				t.Fatalf("untrusted %s output was accepted", name)
			}
		})
	}
}

const approvalTransactionSubprocessDefaultTimeout = 15 * time.Second

func approvalTransactionSubprocessTimeout() time.Duration {
	timeout := approvalTransactionSubprocessDefaultTimeout
	raw := os.Getenv("CODEFLOW_APPROVAL_TRANSACTION_TIMEOUT_MS")
	if raw == "" {
		return timeout
	}
	milliseconds, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || milliseconds <= 0 || milliseconds > int64((30*time.Second)/time.Millisecond) {
		return timeout
	}
	return time.Duration(milliseconds) * time.Millisecond
}

func runApprovalTransactionSubprocesses(t *testing.T, root string, sameKey bool) []approvalTransactionSubprocessOutcome {
	t.Helper()
	startPath := filepath.Join(filepath.Dir(root), "start")
	var commands []*exec.Cmd
	readyPaths := make([]string, 2)
	resultPaths := make([]string, 2)
	ctx, cancel := context.WithTimeout(context.Background(), approvalTransactionSubprocessTimeout())
	defer cancel()
	for index := range 2 {
		readyPath := filepath.Join(filepath.Dir(root), "ready-"+strconv.Itoa(index))
		resultPath := filepath.Join(filepath.Dir(root), "result-"+strconv.Itoa(index))
		readyPaths[index] = readyPath
		resultPaths[index] = resultPath
		key := "cross-process-key-" + strconv.Itoa(index)
		commandID := "cross-process-command-" + strconv.Itoa(index)
		eventID := "cross-process-event-" + strconv.Itoa(index)
		approvalID := "cross-process-approval-" + strconv.Itoa(index)
		if sameKey {
			key = "cross-process-same-key"
			commandID = "cross-process-same-command"
			eventID = "cross-process-same-event"
			approvalID = "cross-process-same-approval"
		}
		command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestApprovalTransactionSubprocessHelper$", "-test.v")
		command.WaitDelay = time.Second
		command.Env = append(os.Environ(),
			"CODEFLOW_APPROVAL_TRANSACTION_HELPER=1",
			"CODEFLOW_APPROVAL_TRANSACTION_ROOT="+root,
			"CODEFLOW_APPROVAL_TRANSACTION_READY="+readyPath,
			"CODEFLOW_APPROVAL_TRANSACTION_START="+startPath,
			"CODEFLOW_APPROVAL_TRANSACTION_RESULT="+resultPath,
			"CODEFLOW_APPROVAL_TRANSACTION_KEY="+key,
			"CODEFLOW_APPROVAL_TRANSACTION_COMMAND="+commandID,
			"CODEFLOW_APPROVAL_TRANSACTION_EVENT="+eventID,
			"CODEFLOW_APPROVAL_TRANSACTION_APPROVAL="+approvalID,
		)
		commands = append(commands, command)
	}
	outputs := make(chan approvalTransactionSubprocessOutcome, len(commands))
	for index, command := range commands {
		resultPath := resultPaths[index]
		go func(command *exec.Cmd, resultPath string) {
			output, err := command.CombinedOutput()
			code := 0
			if err != nil {
				if exitError, ok := err.(*exec.ExitError); ok && exitError.ProcessState != nil {
					code = exitError.ProcessState.ExitCode()
				} else {
					code = -1
				}
			}
			outputs <- approvalTransactionSubprocessOutcome{exitCode: code, output: string(output), resultPath: resultPath}
		}(command, resultPath)
	}

	collect := func(cancelChildren bool) []approvalTransactionSubprocessOutcome {
		if cancelChildren {
			cancel()
		}
		results := make([]approvalTransactionSubprocessOutcome, 0, len(commands))
		for range commands {
			outcome := <-outputs
			outcome.result, outcome.resultErr = readApprovalTransactionSubprocessResult(outcome.resultPath)
			results = append(results, outcome)
		}
		entries, err := os.ReadDir(filepath.Dir(root))
		if err != nil {
			t.Fatalf("read cross-process result directory: %v", err)
		}
		expectedResults := make(map[string]bool, len(resultPaths))
		for _, resultPath := range resultPaths {
			expectedResults[filepath.Base(resultPath)] = true
		}
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), "result-") && !expectedResults[entry.Name()] {
				t.Fatalf("unexpected or duplicate cross-process result file: %s", entry.Name())
			}
		}
		return results
	}

	ready := true
	for _, readyPath := range readyPaths {
		for {
			if _, err := os.Stat(readyPath); err == nil {
				break
			}
			select {
			case <-ctx.Done():
				ready = false
			case <-time.After(5 * time.Millisecond):
			}
			if !ready {
				break
			}
		}
		if !ready {
			break
		}
	}
	if !ready {
		return collect(true)
	}
	if err := os.WriteFile(startPath, []byte("start"), 0o600); err != nil {
		return collect(true)
	}
	return collect(false)
}

func writeApprovalTransactionSubprocessResult(t *testing.T, path string, result approvalTransactionSubprocessResult) {
	t.Helper()
	if strings.TrimSpace(path) == "" {
		t.Fatal("cross-process result path is empty")
	}
	data, err := json.Marshal(result)
	if err != nil || len(data) == 0 || len(data) > approvalTransactionSubprocessResultMaxBytes {
		t.Fatal("cross-process result could not be encoded")
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatal("cross-process result could not be created")
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		t.Fatal("cross-process result could not be written")
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		t.Fatal("cross-process result could not be synced")
	}
	if err := file.Close(); err != nil {
		t.Fatal("cross-process result could not be closed")
	}
}

func TestApprovalTransactionSubprocessHelper(t *testing.T) {
	if os.Getenv("CODEFLOW_APPROVAL_TRANSACTION_HELPER") != "1" {
		return
	}
	readyPath := os.Getenv("CODEFLOW_APPROVAL_TRANSACTION_READY")
	startPath := os.Getenv("CODEFLOW_APPROVAL_TRANSACTION_START")
	resultPath := os.Getenv("CODEFLOW_APPROVAL_TRANSACTION_RESULT")
	root := os.Getenv("CODEFLOW_APPROVAL_TRANSACTION_ROOT")
	if readyPath == "" || startPath == "" || resultPath == "" || root == "" {
		t.Fatal("cross-process helper environment is incomplete")
	}
	if os.Getenv("CODEFLOW_APPROVAL_TRANSACTION_HANG") == "ready" {
		time.Sleep(5 * time.Second)
		return
	}
	if err := os.WriteFile(readyPath, []byte("ready"), 0o600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		if _, err := os.Stat(startPath); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("cross-process helper start timed out")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if os.Getenv("CODEFLOW_APPROVAL_TRANSACTION_HANG") == "completion" {
		time.Sleep(5 * time.Second)
		return
	}
	if waitPath := os.Getenv("CODEFLOW_APPROVAL_TRANSACTION_INIT_WAIT"); waitPath != "" {
		deadline := time.Now().Add(5 * time.Second)
		for {
			if _, err := os.Stat(waitPath); err == nil {
				break
			} else if !errors.Is(err, os.ErrNotExist) {
				t.Fatal(err)
			}
			if time.Now().After(deadline) {
				t.Fatal("cross-process initialization wait timed out")
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	access := approvalAuditAccess(t, "actor-cross-process", "session-cross-process", "workspace-cross-process")
	mutation := approvalTransactionTestMutationWithAccess(t, access,
		os.Getenv("CODEFLOW_APPROVAL_TRANSACTION_KEY"),
		os.Getenv("CODEFLOW_APPROVAL_TRANSACTION_COMMAND"),
		os.Getenv("CODEFLOW_APPROVAL_TRANSACTION_EVENT"),
		os.Getenv("CODEFLOW_APPROVAL_TRANSACTION_APPROVAL"),
		"cross-process-aggregate")
	store := newFileApprovalTransactionStoreForTest(root)
	if pausePath := os.Getenv("CODEFLOW_APPROVAL_TRANSACTION_INIT_PAUSE"); pausePath != "" && os.Getenv("CODEFLOW_APPROVAL_TRANSACTION_INDEX") == "0" {
		releasePath := os.Getenv("CODEFLOW_APPROVAL_TRANSACTION_INIT_RELEASE")
		dependencies := approvalTransactionDependencies{
			beforeDatabasePublish: func(_ string) error {
				if _, err := os.Lstat(filepath.Join(root, approvalTransactionDatabaseName)); !errors.Is(err, os.ErrNotExist) {
					return errors.New("final database path was visible before initialized publication")
				}
				if err := os.WriteFile(pausePath, []byte("paused"), 0o600); err != nil {
					return err
				}
				deadline := time.Now().Add(5 * time.Second)
				for {
					if _, err := os.Stat(releasePath); err == nil {
						return nil
					} else if !errors.Is(err, os.ErrNotExist) {
						return err
					}
					if time.Now().After(deadline) {
						return errors.New("initialization publication release timed out")
					}
					time.Sleep(5 * time.Millisecond)
				}
			},
		}
		if cleanupPath := os.Getenv("CODEFLOW_APPROVAL_TRANSACTION_INIT_CLEANUP"); cleanupPath != "" {
			dependencies.cleanupTemporaryArtifacts = func(temporaryPath string) error {
				if err := os.WriteFile(cleanupPath, []byte(temporaryPath), 0o600); err != nil {
					return err
				}
				cleanupErr := removeApprovalTransactionTemporaryArtifacts(temporaryPath)
				return errors.Join(cleanupErr, errApprovalTransactionTestInitializationCleanup)
			}
		}
		store = newApprovalTransactionStoreWithDependencies(root, dependencies)
	}
	receipt, err := store.Commit(context.Background(), mutation)
	if err == nil {
		writeApprovalTransactionSubprocessResult(t, resultPath, approvalTransactionSubprocessResult{
			Outcome:     "committed",
			Replayed:    receipt.Replayed,
			EventID:     receipt.Event.EventID,
			ApprovalID:  receipt.Event.ApprovalID,
			AggregateID: receipt.Event.AggregateID,
			State:       receipt.Aggregate.State,
			Version:     receipt.Aggregate.Version,
		})
		return
	}
	if errors.Is(err, ErrApprovalTransactionConflict) {
		var typed *ApprovalTransactionError
		if !errors.As(err, &typed) || typed.CurrentState == "" || typed.CurrentVersion < 0 {
			t.Fatal("cross-process conflict did not expose bounded current state")
		}
		currentState, currentVersion := typed.CurrentState, typed.CurrentVersion
		writeApprovalTransactionSubprocessResult(t, resultPath, approvalTransactionSubprocessResult{
			Outcome:        "conflict",
			State:          typed.CurrentState,
			Version:        typed.CurrentVersion,
			CurrentState:   &currentState,
			CurrentVersion: &currentVersion,
		})
		os.Exit(2)
	}
	if os.Getenv("CODEFLOW_APPROVAL_TRANSACTION_INIT_CLEANUP") != "" && errors.Is(err, ErrApprovalTransactionPersistence) && errors.Is(err, errApprovalTransactionTestInitializationCleanup) {
		if !reflect.DeepEqual(receipt, ApprovalCommitReceipt{}) {
			t.Fatalf("cleanup failure returned non-zero receipt: %#v", receipt)
		}
		writeApprovalTransactionSubprocessResult(t, resultPath, approvalTransactionSubprocessResult{
			Outcome:   "persistence-error",
			ErrorKind: "cleanup",
			State:     "active",
			Version:   1,
		})
		os.Exit(3)
	}
	t.Fatalf("cross-process helper commit: %v", err)
}

func TestApprovalTransactionStaleExpectedVersionDoesNotWrite(t *testing.T) {
	access := approvalCommandTestAccess(t)
	first := approvalTransactionTestMutationWithAccess(t, access, "idempotency-stale-initial", "command-stale-initial", "event-stale-initial", "approval-stale-initial", "aggregate-stale")
	stale := approvalTransactionTestMutationWithAccess(t, access, "idempotency-stale-next", "command-stale-next", "event-stale-next", "approval-stale-next", "aggregate-stale")
	store := newFileApprovalTransactionStoreForTest(t.TempDir())
	if _, err := store.Commit(context.Background(), first); err != nil {
		t.Fatalf("initial commit: %v", err)
	}
	before, err := store.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("snapshot before stale: %v", err)
	}
	if _, err := store.Commit(context.Background(), stale); !errors.Is(err, ErrApprovalTransactionConflict) {
		t.Fatalf("stale error = %v, want conflict", err)
	} else {
		var conflict *ApprovalTransactionError
		if !errors.As(err, &conflict) || conflict.CurrentState != "active" || conflict.CurrentVersion != 1 {
			t.Fatalf("stale current state/version = %#v", conflict)
		}
	}
	after, err := store.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("snapshot after stale: %v", err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("stale commit changed records")
	}
}

func TestApprovalTransactionCurrentAuthoritySeamRunsBeforePublication(t *testing.T) {
	mutation := approvalTransactionTestMutation(t, "idempotency-authority", "command-authority", "event-authority", "approval-authority", "aggregate-authority")
	sentinel := errors.New("current authority unavailable")
	called := false
	root := filepath.Join(t.TempDir(), "authority-store")
	store := newApprovalTransactionStoreWithDependencies(root, approvalTransactionDependencies{currentAuthority: func(_ context.Context, _ approvalTransactionTarget, _ *approvalTransactionTargetRecord, _ func() error) error {
		called = true
		return sentinel
	}})
	if _, err := store.Commit(context.Background(), mutation); !errors.Is(err, sentinel) {
		t.Fatalf("authority error = %v, want sentinel", err)
	}
	if !called {
		t.Fatal("authority seam was not called")
	}
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("authority rejection created store root, stat error=%v", err)
	}
}

func TestApprovalTransactionAuthorityMustInvokeCommitOnceZeroCallsFailClosed(t *testing.T) {
	root := filepath.Join(t.TempDir(), "authority-zero-call")
	mutation := approvalTransactionTestMutation(t, "idempotency-authority-zero", "command-authority-zero", "event-authority-zero", "approval-authority-zero", "aggregate-authority-zero")
	store := newApprovalTransactionStoreWithDependencies(root, approvalTransactionDependencies{currentAuthority: func(_ context.Context, _ approvalTransactionTarget, _ *approvalTransactionTargetRecord, _ func() error) error {
		return nil
	}})
	if _, err := store.Commit(context.Background(), mutation); !errors.Is(err, ErrApprovalTransactionPersistence) {
		t.Fatalf("zero-call result = %v, want persistence failure", err)
	}
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("zero-call created root, stat error=%v", err)
	}
}

func TestApprovalTransactionAuthorityDuplicateCommitReturnsCommittedReceipt(t *testing.T) {
	root := filepath.Join(t.TempDir(), "authority-double-call")
	mutation := approvalTransactionTestMutation(t, "idempotency-authority-double", "command-authority-double", "event-authority-double", "approval-authority-double", "aggregate-authority-double")
	var secondCommitErr error
	store := newApprovalTransactionStoreWithDependencies(root, approvalTransactionDependencies{currentAuthority: func(_ context.Context, _ approvalTransactionTarget, _ *approvalTransactionTargetRecord, commit func() error) error {
		if err := commit(); err != nil {
			return err
		}
		secondCommitErr = commit()
		return secondCommitErr
	}})
	receipt, err := store.Commit(context.Background(), mutation)
	if err != nil {
		t.Fatalf("duplicate authority returned failure after commit: %v", err)
	}
	if !errors.Is(secondCommitErr, ErrApprovalTransactionPersistence) || receipt.Replayed {
		t.Fatalf("second error=%v receipt replayed=%t", secondCommitErr, receipt.Replayed)
	}
	snapshot, err := store.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if len(snapshot.Events) != 1 || len(snapshot.IdempotencyResults) != 1 || len(snapshot.Outbox) != 1 {
		t.Fatalf("duplicate authority left unexpected records: %#v", snapshot)
	}
}

func TestApprovalTransactionPreCommitFaultRollsBackAllRecords(t *testing.T) {
	sentinel := errors.New("injected transaction fault")
	faultEnabled := true
	root := filepath.Join(t.TempDir(), "fault-store")
	store := newApprovalTransactionStoreWithDependencies(root, approvalTransactionDependencies{fault: func(step string) error {
		if faultEnabled && step == "transaction-before-commit" {
			return sentinel
		}
		return nil
	}})
	mutation := approvalTransactionTestMutation(t, "idempotency-fault", "command-fault", "event-fault", "approval-fault", "aggregate-fault")
	if _, err := store.Commit(context.Background(), mutation); !errors.Is(err, sentinel) {
		t.Fatalf("fault error = %v, want sentinel", err)
	}
	snapshot, err := store.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("snapshot after rollback: %v", err)
	}
	if len(snapshot.Events) != 0 || len(snapshot.IdempotencyResults) != 0 || len(snapshot.Outbox) != 0 || len(snapshot.Aggregates) != 0 {
		t.Fatalf("pre-commit fault published records: %#v", snapshot)
	}
	faultEnabled = false
	if _, err := store.Commit(context.Background(), mutation); err != nil {
		t.Fatalf("retry after rollback: %v", err)
	}
}

func TestApprovalTransactionPreCommitFaultMatrixRollsBackAndRetries(t *testing.T) {
	stages := []string{
		"transaction-target",
		"transaction-event",
		"transaction-idempotency",
		"transaction-outbox",
		"transaction-before-commit",
	}
	for _, stage := range stages {
		t.Run("genesis/"+stage, func(t *testing.T) {
			const (
				idempotencyKey = "fault-matrix-genesis-key"
				commandID      = "fault-matrix-genesis-command"
				eventID        = "fault-matrix-genesis-event"
				approvalID     = "fault-matrix-genesis-approval"
				aggregateID    = "fault-matrix-genesis-aggregate"
			)
			sentinel := errors.New("fault-matrix injected pre-commit failure")
			faultEnabled := true
			store := newApprovalTransactionStoreWithDependencies(filepath.Join(t.TempDir(), "genesis"), approvalTransactionDependencies{fault: func(step string) error {
				if faultEnabled && step == stage {
					return sentinel
				}
				return nil
			}})
			mutation := approvalTransactionTestMutation(t, idempotencyKey, commandID, eventID, approvalID, aggregateID)
			before, err := store.Snapshot(context.Background())
			if err != nil {
				t.Fatalf("snapshot before fault: %v", err)
			}
			before.Events = append([]ApprovalEventV2{}, before.Events...)
			before.Aggregates = append([]ApprovalAggregateV2{}, before.Aggregates...)
			before.IdempotencyResults = append([]ApprovalIdempotencyResultV1{}, before.IdempotencyResults...)
			before.Outbox = append([]ApprovalOutboxV1{}, before.Outbox...)
			receipt, err := store.Commit(context.Background(), mutation)
			if err == nil || !errors.Is(err, sentinel) || !errors.Is(err, ErrApprovalTransactionPersistence) || !reflect.DeepEqual(receipt, ApprovalCommitReceipt{}) {
				t.Fatalf("%s fault = receipt=%#v err=%v, want zero typed persistence with sentinel", stage, receipt, err)
			}
			var typed *ApprovalTransactionError
			if !errors.As(err, &typed) || typed.CurrentState != "none" || typed.CurrentVersion != 0 {
				t.Fatalf("%s current state/version = %#v, want none/0", stage, typed)
			}
			after, err := store.Snapshot(context.Background())
			if err != nil {
				t.Fatalf("snapshot after %s fault: %v", stage, err)
			}
			if !reflect.DeepEqual(after, before) {
				t.Fatalf("%s fault changed durable snapshot: before=%#v after=%#v", stage, before, after)
			}
			faultEnabled = false
			retry, err := store.Commit(context.Background(), mutation)
			if err != nil || retry.Replayed {
				t.Fatalf("retry after %s fault = receipt=%#v err=%v, want one non-replayed commit", stage, retry, err)
			}
			replay, err := store.Commit(context.Background(), mutation)
			if err != nil || !replay.Replayed {
				t.Fatalf("exact retry after %s fault = receipt=%#v err=%v, want replay", stage, replay, err)
			}
			final, err := store.Snapshot(context.Background())
			if err != nil {
				t.Fatalf("final snapshot after %s fault: %v", stage, err)
			}
			if len(final.Events) != 1 || len(final.Aggregates) != 1 || len(final.IdempotencyResults) != 1 || len(final.Outbox) != 1 {
				t.Fatalf("final %s fault counts = events=%d aggregates=%d idempotency=%d outbox=%d, want 1/1/1/1", stage, len(final.Events), len(final.Aggregates), len(final.IdempotencyResults), len(final.Outbox))
			}
		})

		t.Run("active/"+stage, func(t *testing.T) {
			const aggregateID = "fault-matrix-active-aggregate"
			sentinel := errors.New("fault-matrix injected pre-commit failure")
			faultEnabled := false
			store := newApprovalTransactionStoreWithDependencies(filepath.Join(t.TempDir(), "active"), approvalTransactionDependencies{fault: func(step string) error {
				if faultEnabled && step == stage {
					return sentinel
				}
				return nil
			}})
			access := approvalCommandTestAccess(t)
			initial := approvalTransactionTestMutationWithAccess(t, access, "fault-matrix-active-initial-key", "fault-matrix-active-initial-command", "event-all-initial", "approval-all-initial", aggregateID)
			if _, err := store.Commit(context.Background(), initial); err != nil {
				t.Fatalf("initial active commit: %v", err)
			}
			before, err := store.Snapshot(context.Background())
			if err != nil {
				t.Fatalf("snapshot before active fault: %v", err)
			}
			mutation := approvalTransactionTestMutationForDecisionWithAccess(t, access, "edit_then_approve", aggregateID, "fault-matrix-active-event", "fault-matrix-active-approval", "fault-matrix-active-key")
			faultEnabled = true
			receipt, err := store.Commit(context.Background(), mutation)
			if err == nil || !errors.Is(err, sentinel) || !errors.Is(err, ErrApprovalTransactionPersistence) || !reflect.DeepEqual(receipt, ApprovalCommitReceipt{}) {
				t.Fatalf("%s active fault = receipt=%#v err=%v, want zero typed persistence with sentinel", stage, receipt, err)
			}
			var typed *ApprovalTransactionError
			if !errors.As(err, &typed) || typed.CurrentState != "active" || typed.CurrentVersion != 1 {
				t.Fatalf("%s active current state/version = %#v, want active/1", stage, typed)
			}
			after, err := store.Snapshot(context.Background())
			if err != nil {
				t.Fatalf("snapshot after active %s fault: %v", stage, err)
			}
			if !reflect.DeepEqual(after, before) {
				t.Fatalf("active %s fault changed durable snapshot: before=%#v after=%#v", stage, before, after)
			}
			faultEnabled = false
			retry, err := store.Commit(context.Background(), mutation)
			if err != nil || retry.Replayed {
				t.Fatalf("active retry after %s fault = receipt=%#v err=%v, want one non-replayed commit", stage, retry, err)
			}
			replay, err := store.Commit(context.Background(), mutation)
			if err != nil || !replay.Replayed {
				t.Fatalf("active exact retry after %s fault = receipt=%#v err=%v, want replay", stage, replay, err)
			}
			final, err := store.Snapshot(context.Background())
			if err != nil {
				t.Fatalf("final active snapshot after %s fault: %v", stage, err)
			}
			if len(final.Events) != 2 || len(final.Aggregates) != 1 || len(final.IdempotencyResults) != 2 || len(final.Outbox) != 2 {
				t.Fatalf("final active %s fault counts = events=%d aggregates=%d idempotency=%d outbox=%d, want 2/1/2/2", stage, len(final.Events), len(final.Aggregates), len(final.IdempotencyResults), len(final.Outbox))
			}
		})
	}
}

func TestApprovalTransactionAfterCommitFaultStillReturnsCommittedReceipt(t *testing.T) {
	fault := errors.New("post-commit interruption")
	faultEnabled := true
	root := filepath.Join(t.TempDir(), "after-commit")
	store := newApprovalTransactionStoreWithDependencies(root, approvalTransactionDependencies{fault: func(step string) error {
		if faultEnabled && step == "after-commit" {
			return fault
		}
		return nil
	}})
	mutation := approvalTransactionTestMutation(t, "idempotency-after-commit", "command-after-commit", "event-after-commit", "approval-after-commit", "aggregate-after-commit")
	receipt, err := store.Commit(context.Background(), mutation)
	if err != nil || receipt.Replayed {
		t.Fatalf("post-commit hook changed result: receipt=%#v err=%v", receipt, err)
	}
	faultEnabled = false
	replay, err := newFileApprovalTransactionStoreForTest(root).Commit(context.Background(), mutation)
	if err != nil || !replay.Replayed {
		t.Fatalf("reopen replay: receipt=%#v err=%v", replay, err)
	}
}

func TestApprovalTransactionCommitErrorAfterPhysicalCommitReconciles(t *testing.T) {
	commitObservationErr := errors.New("commit observation failed")
	mutation := approvalTransactionTestMutation(t, "commit-observed-key", "commit-observed-command", "commit-observed-event", "commit-observed-approval", "commit-observed-aggregate")
	want := approvalTransactionExpectedReceipt(t, mutation)
	root := filepath.Join(t.TempDir(), "commit-observed-error")
	commitExecutor := func(ctx context.Context, conn *sql.Conn) error {
		if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
			return err
		}
		return commitObservationErr
	}
	store := newApprovalTransactionStoreWithDependencies(root, approvalTransactionDependencies{commitExecutor: commitExecutor})
	receipt, err := store.Commit(context.Background(), mutation)
	if err != nil {
		t.Fatalf("commit-after-observation error was not reconciled: receipt=%#v err=%v", receipt, err)
	}
	if receipt.Replayed || !reflect.DeepEqual(receipt, want) || !bytes.Equal(receipt.EventPayload, want.EventPayload) {
		t.Fatalf("commit-after-observation receipt differs from intended receipt: got=%#v want=%#v", receipt, want)
	}
	snapshot, err := store.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("snapshot after commit observation error: %v", err)
	}
	if len(snapshot.Events) != 1 || len(snapshot.Aggregates) != 1 || len(snapshot.IdempotencyResults) != 1 || len(snapshot.Outbox) != 1 {
		t.Fatalf("reconciled record counts = events %d aggregates %d idempotency %d outbox %d", len(snapshot.Events), len(snapshot.Aggregates), len(snapshot.IdempotencyResults), len(snapshot.Outbox))
	}
	replay, err := newFileApprovalTransactionStoreForTest(root).Commit(context.Background(), mutation)
	if err != nil || !replay.Replayed {
		t.Fatalf("exact retry after reconciliation = receipt=%#v err=%v", replay, err)
	}
}

func TestApprovalTransactionCommitErrorAfterPhysicalCommitCancellationReconciles(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mutation := approvalTransactionTestMutation(t, "commit-cancel-key", "commit-cancel-command", "commit-cancel-event", "commit-cancel-approval", "commit-cancel-aggregate")
	want := approvalTransactionExpectedReceipt(t, mutation)
	commitErr := func(_ context.Context, conn *sql.Conn) error {
		if _, err := conn.ExecContext(context.Background(), "COMMIT"); err != nil {
			return err
		}
		cancel()
		return ctx.Err()
	}
	store := newApprovalTransactionStoreWithDependencies(filepath.Join(t.TempDir(), "commit-cancel-error"), approvalTransactionDependencies{commitExecutor: commitErr})
	got, err := store.Commit(ctx, mutation)
	if err != nil || got.Replayed || !reflect.DeepEqual(got, want) || !bytes.Equal(got.EventPayload, want.EventPayload) {
		t.Fatalf("commit-after-cancellation receipt = %#v err=%v, want exact non-replay receipt", got, err)
	}
}

func TestApprovalTransactionCommitErrorAfterPhysicalCommitDeadlineReconciles(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	mutation := approvalTransactionTestMutation(t, "commit-deadline-key", "commit-deadline-command", "commit-deadline-event", "commit-deadline-approval", "commit-deadline-aggregate")
	want := approvalTransactionExpectedReceipt(t, mutation)
	commitErr := func(_ context.Context, conn *sql.Conn) error {
		if _, err := conn.ExecContext(context.Background(), "COMMIT"); err != nil {
			return err
		}
		<-ctx.Done()
		return ctx.Err()
	}
	store := newApprovalTransactionStoreWithDependencies(filepath.Join(t.TempDir(), "commit-deadline-error"), approvalTransactionDependencies{commitExecutor: commitErr})
	got, err := store.Commit(ctx, mutation)
	if err != nil || got.Replayed || !reflect.DeepEqual(got, want) || !bytes.Equal(got.EventPayload, want.EventPayload) {
		t.Fatalf("commit-after-deadline receipt = %#v err=%v, want exact non-replay receipt", got, err)
	}
}

func TestApprovalTransactionCommitErrorBeforePhysicalCommitRollsBackAndRetries(t *testing.T) {
	commitErr := errors.New("commit was not executed")
	failCommit := true
	commitExecutor := func(ctx context.Context, conn *sql.Conn) error {
		if failCommit {
			return commitErr
		}
		_, err := conn.ExecContext(ctx, "COMMIT")
		return err
	}
	root := filepath.Join(t.TempDir(), "commit-before-error")
	store := newApprovalTransactionStoreWithDependencies(root, approvalTransactionDependencies{commitExecutor: commitExecutor})
	mutation := approvalTransactionTestMutation(t, "commit-before-key", "commit-before-command", "commit-before-event", "commit-before-approval", "commit-before-aggregate")
	receipt, err := store.Commit(context.Background(), mutation)
	if err == nil || !errors.Is(err, ErrApprovalTransactionPersistence) || !errors.Is(err, commitErr) || !reflect.DeepEqual(receipt, ApprovalCommitReceipt{}) {
		t.Fatalf("pre-commit error = receipt=%#v err=%v, want typed persistence with commit error", receipt, err)
	}
	var typed *ApprovalTransactionError
	if !errors.As(err, &typed) || typed.CurrentState != "none" || typed.CurrentVersion != 0 {
		t.Fatalf("pre-commit current state/version = %#v", typed)
	}
	snapshot, err := store.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("snapshot after pre-commit error: %v", err)
	}
	if len(snapshot.Events) != 0 || len(snapshot.Aggregates) != 0 || len(snapshot.IdempotencyResults) != 0 || len(snapshot.Outbox) != 0 {
		t.Fatalf("pre-commit error published partial records: %#v", snapshot)
	}
	failCommit = false
	retry, err := store.Commit(context.Background(), mutation)
	if err != nil || retry.Replayed {
		t.Fatalf("retry after pre-commit error = receipt=%#v err=%v", retry, err)
	}
	snapshot, err = store.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("snapshot after successful retry: %v", err)
	}
	if len(snapshot.Events) != 1 || len(snapshot.Aggregates) != 1 || len(snapshot.IdempotencyResults) != 1 || len(snapshot.Outbox) != 1 {
		t.Fatalf("successful retry published unexpected records: %#v", snapshot)
	}
}

func TestApprovalTransactionCommitErrorReconciliationRetainsCurrentState(t *testing.T) {
	root := filepath.Join(t.TempDir(), "commit-current-state")
	access := approvalCommandTestAccess(t)
	store := newFileApprovalTransactionStoreForTest(root)
	initial := approvalTransactionTestMutationWithAccess(t, access, "all-initial-key", "all-initial-command", "event-all-initial", "approval-all-initial", "commit-current-aggregate")
	if _, err := store.Commit(context.Background(), initial); err != nil {
		t.Fatalf("initial commit: %v", err)
	}
	second := approvalTransactionTestMutationForDecisionWithAccess(t, access, "edit_then_approve", "commit-current-aggregate", "commit-current-second-event", "commit-current-second-approval", "commit-current-second-key")
	commitErr := errors.New("second commit was not executed")
	failStore := newApprovalTransactionStoreWithDependencies(root, approvalTransactionDependencies{commitExecutor: func(context.Context, *sql.Conn) error {
		return commitErr
	}})
	receipt, err := failStore.Commit(context.Background(), second)
	if err == nil || !errors.Is(err, ErrApprovalTransactionPersistence) || !errors.Is(err, commitErr) || !reflect.DeepEqual(receipt, ApprovalCommitReceipt{}) {
		t.Fatalf("current-state commit error = receipt=%#v err=%v", receipt, err)
	}
	var typed *ApprovalTransactionError
	if !errors.As(err, &typed) || typed.CurrentState != "active" || typed.CurrentVersion != 1 {
		t.Fatalf("current state/version = %#v", typed)
	}
}

func TestApprovalTransactionCommitErrorCorruptResolutionFailsClosed(t *testing.T) {
	commitErr := errors.New("commit result was corrupted")
	root := filepath.Join(t.TempDir(), "commit-corrupt-resolution")
	commitExecutor := func(ctx context.Context, conn *sql.Conn) error {
		if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
			return err
		}
		if _, err := conn.ExecContext(ctx, "UPDATE approval_idempotency SET request_digest='sha256:0000000000000000000000000000000000000000000000000000000000000000'"); err != nil {
			return err
		}
		return commitErr
	}
	store := newApprovalTransactionStoreWithDependencies(root, approvalTransactionDependencies{commitExecutor: commitExecutor})
	mutation := approvalTransactionTestMutation(t, "commit-corrupt-key", "commit-corrupt-command", "commit-corrupt-event", "commit-corrupt-approval", "commit-corrupt-aggregate")
	receipt, err := store.Commit(context.Background(), mutation)
	if err == nil || !errors.Is(err, ErrApprovalTransactionPersistence) || !reflect.DeepEqual(receipt, ApprovalCommitReceipt{}) {
		t.Fatalf("corrupt reconciliation result = receipt=%#v err=%v", receipt, err)
	}
	if errors.Is(err, commitErr) == false {
		t.Fatalf("corrupt reconciliation lost commit cause: %v", err)
	}
}

func TestApprovalTransactionCommitErrorReconciliationRejectsNonMatchingDurableState(t *testing.T) {
	cases := []struct {
		name          string
		mutate        func(context.Context, *sql.Conn) error
		durableValid  bool
		exactRetry    bool
		wantRetryConf bool
	}{
		{
			name: "partial-outbox",
			mutate: func(ctx context.Context, conn *sql.Conn) error {
				_, err := conn.ExecContext(ctx, "DELETE FROM approval_outbox")
				return err
			},
		},
		{
			name:          "same-key-different-recomputable-digest",
			durableValid:  true,
			exactRetry:    true,
			mutate:        mutateApprovalTransactionRequestDigest,
			wantRetryConf: true,
		},
		{
			name: "valid-target-mismatch",
			mutate: func(ctx context.Context, conn *sql.Conn) error {
				_, err := conn.ExecContext(ctx, "UPDATE approval_targets SET validated_snapshot_id='different-snapshot'")
				return err
			},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "commit-reconciliation-matrix")
			mutation := approvalTransactionTestMutation(t, "matrix-key-"+testCase.name, "matrix-command-"+testCase.name, "matrix-event-"+testCase.name, "matrix-approval-"+testCase.name, "matrix-aggregate-"+testCase.name)
			commitErr := errors.New("matrix commit observation failed")
			commitExecutor := func(_ context.Context, conn *sql.Conn) error {
				if _, err := conn.ExecContext(context.Background(), "COMMIT"); err != nil {
					return err
				}
				if err := testCase.mutate(context.Background(), conn); err != nil {
					return err
				}
				return commitErr
			}
			store := newApprovalTransactionStoreWithDependencies(root, approvalTransactionDependencies{commitExecutor: commitExecutor})
			receipt, err := store.Commit(context.Background(), mutation)
			if err == nil || !errors.Is(err, ErrApprovalTransactionPersistence) || !errors.Is(err, commitErr) || !reflect.DeepEqual(receipt, ApprovalCommitReceipt{}) {
				t.Fatalf("nonmatching durable state = receipt=%#v err=%v, want zero typed persistence", receipt, err)
			}
			var typed *ApprovalTransactionError
			if !errors.As(err, &typed) || typed.CurrentState != "none" || typed.CurrentVersion != 0 {
				t.Fatalf("nonmatching durable state current = %#v", typed)
			}
			snapshot, snapshotErr := store.Snapshot(context.Background())
			if testCase.durableValid {
				if snapshotErr != nil || len(snapshot.Events) != 1 || len(snapshot.Aggregates) != 1 || len(snapshot.IdempotencyResults) != 1 || len(snapshot.Outbox) != 1 {
					t.Fatalf("durable-valid state snapshot = %#v err=%v", snapshot, snapshotErr)
				}
			} else if !errors.Is(snapshotErr, ErrApprovalTransactionInvalid) {
				t.Fatalf("partial durable state snapshot = %v, want invalid", snapshotErr)
			}
			if testCase.exactRetry {
				retry, retryErr := store.Commit(context.Background(), mutation)
				if testCase.wantRetryConf {
					if retryErr == nil || !errors.Is(retryErr, ErrApprovalTransactionConflict) || !reflect.DeepEqual(retry, ApprovalCommitReceipt{}) {
						t.Fatalf("same-key digest retry = receipt=%#v err=%v, want zero conflict", retry, retryErr)
					}
				} else if retryErr != nil {
					t.Fatalf("exact retry after nonmatching state: %v", retryErr)
				}
			}
		})
	}
}

func TestApprovalTransactionCommitReconciliationRejectsMismatchedIntendedAggregate(t *testing.T) {
	mutation := approvalTransactionTestMutation(t, "intended-aggregate-key", "intended-aggregate-command", "intended-aggregate-event", "intended-aggregate-approval", "intended-aggregate")
	prepared, err := prepareApprovalTransactionMutation(mutation)
	if err != nil {
		t.Fatalf("prepare mutation: %v", err)
	}
	store := newFileApprovalTransactionStoreForTest(filepath.Join(t.TempDir(), "intended-aggregate"))
	want := approvalTransactionExpectedReceipt(t, mutation)
	if _, err := store.Commit(context.Background(), mutation); err != nil {
		t.Fatalf("commit durable state: %v", err)
	}
	intended := want
	intended.Aggregate.ActiveApprovalID = "different-active-approval"
	got, err := store.reconcileApprovalTransactionCommit(context.Background(), prepared, intended)
	if err == nil || !errors.Is(err, ErrApprovalTransactionPersistence) || !reflect.DeepEqual(got, ApprovalCommitReceipt{}) {
		t.Fatalf("mismatched intended aggregate = receipt=%#v err=%v, want zero typed persistence", got, err)
	}
}

func mutateApprovalTransactionRequestDigest(ctx context.Context, conn *sql.Conn) error {
	var commandJSON, resultJSON []byte
	if err := conn.QueryRowContext(ctx, "SELECT command_json,result_json FROM approval_idempotency LIMIT 1").Scan(&commandJSON, &resultJSON); err != nil {
		return err
	}
	var command ApprovalCommandV2
	if err := decodeApprovalTransactionJSON(commandJSON, &command); err != nil {
		return err
	}
	var result ApprovalIdempotencyResultV1
	if err := decodeApprovalTransactionJSON(resultJSON, &result); err != nil {
		return err
	}
	command.CommandID = command.CommandID + "-alternate"
	result.CommandID = command.CommandID
	result.OriginalCommand.CommandID = command.CommandID
	digest, err := approvalCommandIdempotencyDigest(command)
	if err != nil {
		return err
	}
	result.RequestDigest = "sha256:" + hex.EncodeToString(digest[:])
	updatedCommandJSON, err := json.Marshal(command)
	if err != nil {
		return err
	}
	updatedResultJSON, err := json.Marshal(result)
	if err != nil {
		return err
	}
	_, err = conn.ExecContext(ctx, "UPDATE approval_idempotency SET command_id=?,request_digest=?,command_json=?,result_json=?", command.CommandID, result.RequestDigest, updatedCommandJSON, updatedResultJSON)
	return err
}

func TestApprovalTransactionEventLogIsAppendOnly(t *testing.T) {
	store := newFileApprovalTransactionStoreForTest(t.TempDir())
	if _, err := store.Commit(context.Background(), approvalTransactionTestMutation(t, "append-key", "append-command", "append-event", "append-approval", "append-aggregate")); err != nil {
		t.Fatalf("commit: %v", err)
	}
	db, err := sql.Open("sqlite", store.databasePath())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec("UPDATE approval_events SET decision='reject'"); err == nil {
		t.Fatal("event update unexpectedly succeeded")
	}
	if _, err := db.Exec("DELETE FROM approval_events"); err == nil {
		t.Fatal("event delete unexpectedly succeeded")
	}
}

func TestApprovalTransactionProjectionTamperAndSchemaDriftFailClosed(t *testing.T) {
	root := t.TempDir()
	store := newFileApprovalTransactionStoreForTest(root)
	if _, err := store.Commit(context.Background(), approvalTransactionTestMutation(t, "tamper-key", "tamper-command", "tamper-event", "tamper-approval", "tamper-aggregate")); err != nil {
		t.Fatalf("commit: %v", err)
	}
	db, err := sql.Open("sqlite", store.databasePath())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := db.Exec("UPDATE approval_targets SET state='rejected'"); err != nil {
		_ = db.Close()
		t.Fatalf("tamper projection: %v", err)
	}
	_ = db.Close()
	if _, err := store.Snapshot(context.Background()); !errors.Is(err, ErrApprovalTransactionInvalid) {
		t.Fatalf("projection tamper = %v, want invalid", err)
	}

	root2 := t.TempDir()
	store2 := newFileApprovalTransactionStoreForTest(root2)
	if _, err := store2.Commit(context.Background(), approvalTransactionTestMutation(t, "version-key", "version-command", "version-event", "version-approval", "version-aggregate")); err != nil {
		t.Fatalf("version commit: %v", err)
	}
	db, err = sql.Open("sqlite", store2.databasePath())
	if err != nil {
		t.Fatalf("open version db: %v", err)
	}
	if _, err := db.Exec("PRAGMA user_version=999"); err != nil {
		t.Fatalf("set schema version: %v", err)
	}
	_ = db.Close()
	if _, err := newFileApprovalTransactionStoreForTest(root2).Snapshot(context.Background()); !errors.Is(err, ErrApprovalTransactionPersistence) {
		t.Fatalf("schema drift = %v, want persistence", err)
	}
}

func TestApprovalTransactionTargetProvenanceMutationFailsClosed(t *testing.T) {
	cases := []struct {
		name   string
		column string
		value  any
	}{
		{name: "validated snapshot", column: "validated_snapshot_id", value: "target-valid-other-snapshot"},
		{name: "workspace epoch", column: "workspace_epoch", value: int64(2)},
		{name: "map", column: "map_id", value: "target-valid-other-map"},
		{name: "task", column: "task_id", value: "target-valid-other-task"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			root := t.TempDir()
			store := newFileApprovalTransactionStoreForTest(root)
			if _, err := store.Commit(context.Background(), approvalTransactionTestMutation(t, "target-provenance-"+testCase.name, "target-provenance-command-"+testCase.name, "target-provenance-event-"+testCase.name, "target-provenance-approval-"+testCase.name, "target-provenance-aggregate-"+testCase.name)); err != nil {
				t.Fatalf("initial target provenance commit: %v", err)
			}
			db, err := sql.Open("sqlite", store.databasePath())
			if err != nil {
				t.Fatalf("open target provenance database: %v", err)
			}
			if _, err := db.Exec("UPDATE approval_targets SET "+testCase.column+"=?", testCase.value); err != nil {
				_ = db.Close()
				t.Fatalf("mutate target %s: %v", testCase.column, err)
			}
			if err := db.Close(); err != nil {
				t.Fatalf("close target provenance database: %v", err)
			}
			snapshot, err := newFileApprovalTransactionStoreForTest(root).Snapshot(context.Background())
			if err == nil || (!errors.Is(err, ErrApprovalTransactionInvalid) && !errors.Is(err, ErrApprovalTransactionPersistence)) || !reflect.DeepEqual(snapshot, ApprovalTransactionSnapshot{}) {
				t.Fatalf("mutated target %s snapshot = %#v err=%v, want zero typed failure", testCase.column, snapshot, err)
			}
		})
	}
}

func TestApprovalTransactionSnapshotIsDefensive(t *testing.T) {
	store := newFileApprovalTransactionStoreForTest(t.TempDir())
	if _, err := store.Commit(context.Background(), approvalTransactionTestMutation(t, "defensive-key", "defensive-command", "defensive-event", "defensive-approval", "defensive-aggregate")); err != nil {
		t.Fatalf("commit: %v", err)
	}
	snapshot, err := store.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	snapshot.Aggregates[0].History[0].EventID = "mutated"
	snapshot.Events[0].EventID = "mutated"
	snapshot.IdempotencyResults[0].CommandID = "mutated"
	snapshot.Outbox[0].OutboxID = "mutated"
	fresh, err := store.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("fresh snapshot: %v", err)
	}
	if fresh.Events[0].EventID == "mutated" || fresh.Aggregates[0].History[0].EventID == "mutated" || fresh.IdempotencyResults[0].CommandID == "mutated" || fresh.Outbox[0].OutboxID == "mutated" {
		t.Fatal("snapshot mutation changed durable state")
	}
}

func TestApprovalTransactionRejectsSymlinkedRootAndDatabase(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatalf("mkdir target: %v", err)
	}
	rootLink := filepath.Join(base, "root-link")
	if err := os.Symlink(target, rootLink); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	store := newFileApprovalTransactionStoreForTest(rootLink)
	if _, err := store.Commit(context.Background(), approvalTransactionTestMutation(t, "symlink-root-key", "symlink-root-command", "symlink-root-event", "symlink-root-approval", "symlink-root-aggregate")); !errors.Is(err, ErrApprovalTransactionPersistence) {
		t.Fatalf("symlinked root commit = %v, want persistence", err)
	}

	validRoot := filepath.Join(base, "valid")
	validStore := newFileApprovalTransactionStoreForTest(validRoot)
	mutation := approvalTransactionTestMutation(t, "symlink-db-key", "symlink-db-command", "symlink-db-event", "symlink-db-approval", "symlink-db-aggregate")
	if _, err := validStore.Commit(context.Background(), mutation); err != nil {
		t.Fatalf("valid commit: %v", err)
	}
	dbPath := validStore.databasePath()
	linkTarget := filepath.Join(base, "db-target")
	if err := os.Rename(dbPath, linkTarget); err != nil {
		t.Fatalf("move db: %v", err)
	}
	if err := os.Symlink(linkTarget, dbPath); err != nil {
		t.Skipf("database symlink unavailable: %v", err)
	}
	if _, err := validStore.Snapshot(context.Background()); !errors.Is(err, ErrApprovalTransactionPersistence) {
		t.Fatalf("symlinked database snapshot = %v, want persistence", err)
	}
}

func TestApprovalTransactionSQLiteDSNPreservesSpecialManagedPaths(t *testing.T) {
	cases := []struct {
		name string
		dir  string
	}{
		{name: "question", dir: "managed-question?mark"},
		{name: "fragment", dir: "managed-fragment#mark"},
		{name: "percent", dir: "managed-percent%mark"},
		{name: "spaces", dir: "managed space name"},
		{name: "unicode", dir: "managed-승인-저장소"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			base := t.TempDir()
			root := filepath.Join(base, testCase.dir)
			store := newFileApprovalTransactionStoreForTest(root)
			if _, err := store.Commit(context.Background(), approvalTransactionTestMutation(t, "path-"+testCase.name, "path-command-"+testCase.name, "path-event-"+testCase.name, "path-approval-"+testCase.name, "path-aggregate-"+testCase.name)); err != nil {
				t.Fatalf("commit special managed path: %v", err)
			}
			rootInfo, err := os.Stat(root)
			if err != nil {
				t.Fatalf("stat managed root: %v", err)
			}
			if !rootInfo.IsDir() || rootInfo.Mode().Perm() != 0o700 {
				t.Fatalf("managed root mode=%v isDir=%t, want 0700 directory", rootInfo.Mode().Perm(), rootInfo.IsDir())
			}
			databasePath := store.databasePath()
			databaseInfo, err := os.Stat(databasePath)
			if err != nil {
				t.Fatalf("stat database at exact path: %v", err)
			}
			if !databaseInfo.Mode().IsRegular() || databaseInfo.Mode().Perm() != 0o600 {
				t.Fatalf("database mode=%v regular=%t, want 0600 regular file", databaseInfo.Mode().Perm(), databaseInfo.Mode().IsRegular())
			}
			lstatInfo, err := os.Lstat(databasePath)
			if err != nil || !os.SameFile(databaseInfo, lstatInfo) {
				t.Fatalf("database path is not the exact managed file")
			}
			entries, err := os.ReadDir(base)
			if err != nil {
				t.Fatalf("read managed path parent: %v", err)
			}
			if len(entries) != 1 || entries[0].Name() != testCase.dir {
				t.Fatalf("unexpected sibling/prefix artifacts: %#v", entries)
			}
			reopened, err := newFileApprovalTransactionStoreForTest(root).Snapshot(context.Background())
			if err != nil {
				t.Fatalf("reopen special managed path: %v", err)
			}
			if len(reopened.Events) != 1 || len(reopened.IdempotencyResults) != 1 || len(reopened.Outbox) != 1 {
				t.Fatalf("reopened records=%#v", reopened)
			}
		})
	}
}

func TestApprovalTransactionSupportsAllLifecycleDecisions(t *testing.T) {
	for _, decision := range []string{"approve", "reject"} {
		t.Run(decision, func(t *testing.T) {
			if _, err := newFileApprovalTransactionStoreForTest(t.TempDir()).Commit(context.Background(), approvalTransactionTestMutationForDecision(t, decision)); err != nil {
				t.Fatalf("%s commit: %v", decision, err)
			}
		})
	}
	for _, decision := range []string{"edit_then_approve", "revoke", "supersede"} {
		t.Run(decision, func(t *testing.T) {
			access := approvalCommandTestAccess(t)
			store := newFileApprovalTransactionStoreForTest(t.TempDir())
			initial := approvalTransactionTestMutationWithAccess(t, access, "all-initial-key", "all-initial-command", "event-all-initial", "approval-all-initial", "all-aggregate")
			if _, err := store.Commit(context.Background(), initial); err != nil {
				t.Fatalf("initial commit: %v", err)
			}
			second := approvalTransactionTestMutationForDecisionWithAccess(t, access, decision, "all-aggregate", "all-"+decision+"-event", "all-"+decision+"-approval", "all-"+decision+"-key")
			if _, err := store.Commit(context.Background(), second); err != nil {
				t.Fatalf("%s commit: %v", decision, err)
			}
		})
	}
}

func approvalTransactionExpectedReceipt(t *testing.T, mutation *ValidatedApprovalMutation) ApprovalCommitReceipt {
	t.Helper()
	store := newFileApprovalTransactionStoreForTest(filepath.Join(t.TempDir(), "expected-receipt"))
	receipt, err := store.Commit(context.Background(), mutation)
	if err != nil {
		t.Fatalf("derive intended receipt: %v", err)
	}
	if receipt.Replayed {
		t.Fatal("derived intended receipt unexpectedly reported replay")
	}
	return receipt
}

func approvalTransactionTestMutation(t *testing.T, idempotencyKey, commandID, eventID, approvalID, aggregateID string) *ValidatedApprovalMutation {
	t.Helper()
	return approvalTransactionTestMutationWithAccess(t, approvalCommandTestAccess(t), idempotencyKey, commandID, eventID, approvalID, aggregateID)
}

func approvalTransactionTestMutationForDecision(t *testing.T, decision string) *ValidatedApprovalMutation {
	t.Helper()
	return approvalTransactionTestMutationForDecisionWithAccess(t, approvalCommandTestAccess(t), decision, "aggregate-decision", "event-decision-"+decision, "approval-decision-"+decision, "idempotency-decision-"+decision)
}

func approvalTransactionTestMutationForDecisionWithAccess(t *testing.T, access ApprovalAccess, decision, aggregateID, eventID, approvalID, idempotencyKey string) *ValidatedApprovalMutation {
	t.Helper()
	command, stored, before, after, event, aggregate := approvalMeaningMutationTestInputsForDecisionWithAccess(t, decision, access)
	command.command.IdempotencyKey = idempotencyKey
	command.command.CommandID = "command-decision-" + decision
	if decision == "edit_then_approve" || decision == "revoke" || decision == "supersede" {
		predecessor := "approval-all-initial"
		command.command.PredecessorApprovalID = &predecessor
		event.PredecessorApprovalID = predecessor
	}
	command.digest, _ = approvalCommandDigest(command.command)
	event.EventID = eventID
	event.ApprovalID = approvalID
	event.AggregateID = aggregateID
	aggregate.AggregateID = aggregateID
	aggregate.LastEventID = eventID
	if aggregate.State == "active" {
		aggregate.ActiveApprovalID = approvalID
	} else {
		aggregate.ActiveApprovalID = ""
	}
	if len(aggregate.History) > 2 {
		aggregate.History[1].EventID = "event-all-initial"
		aggregate.History[1].ApprovalID = "approval-all-initial"
	}
	if len(aggregate.History) > 1 {
		aggregate.History[len(aggregate.History)-1].EventID = eventID
		aggregate.History[len(aggregate.History)-1].ApprovalID = approvalID
	}
	mutation, err := ValidateApprovalMeaningMutation(command, stored, before, after, event, aggregate)
	if err != nil {
		t.Fatalf("build %s mutation: %v", decision, err)
	}
	return mutation
}

func approvalTransactionTestMutationWithAccess(t *testing.T, access ApprovalAccess, idempotencyKey, commandID, eventID, approvalID, aggregateID string) *ValidatedApprovalMutation {
	t.Helper()
	command, stored, before, after, event, aggregate := approvalMeaningMutationTestInputsForDecisionWithAccess(t, "approve", access)
	command.command.IdempotencyKey = idempotencyKey
	command.command.CommandID = commandID
	command.digest, _ = approvalCommandDigest(command.command)
	event.EventID = eventID
	event.ApprovalID = approvalID
	event.AggregateID = aggregateID
	aggregate.AggregateID = aggregateID
	aggregate.LastEventID = eventID
	aggregate.ActiveApprovalID = approvalID
	if len(aggregate.History) > 1 {
		aggregate.History[len(aggregate.History)-1].EventID = eventID
		aggregate.History[len(aggregate.History)-1].ApprovalID = approvalID
	}
	if err := validateApprovalLifecycleAggregate(*aggregate); err != nil {
		t.Fatalf("aggregate identity setup: %v", err)
	}
	mutation, err := ValidateApprovalMeaningMutation(command, stored, before, after, event, aggregate)
	if err != nil {
		t.Fatalf("build transaction mutation: %v", err)
	}
	return mutation
}
