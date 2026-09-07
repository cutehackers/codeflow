package semantic

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

func TestApprovalTransactionOutboxAcceptsPublishedState(t *testing.T) {
	store := newFileApprovalTransactionStoreForTest(t.TempDir())
	receipt, err := store.Commit(context.Background(), approvalTransactionTestMutation(t, "outbox-published-key", "outbox-published-command", "outbox-published-event", "outbox-published-approval", "outbox-published-aggregate"))
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	published := receipt.Outbox
	published.DeliveryState = "published"
	published.PublishedAt = receipt.Outbox.CommittedAt
	if err := validateApprovalTransactionOutbox(published); err != nil {
		t.Fatalf("published outbox validation: %v", err)
	}
	failed := published
	failed.DeliveryState = "failed"
	failed.PublishedAt = ""
	failed.FailureReason = "delivery failed"
	if err := validateApprovalTransactionOutbox(failed); err == nil {
		t.Fatal("failed outbox state unexpectedly validated")
	}
}

func TestApprovalTransactionPublishOutboxPersistsAndIsIdempotent(t *testing.T) {
	root := t.TempDir()
	store := newFileApprovalTransactionStoreForTest(root)
	mutation := approvalTransactionTestMutation(t, "outbox-transition-key", "outbox-transition-command", "outbox-transition-event", "outbox-transition-approval", "outbox-transition-aggregate")
	receipt, err := store.Commit(context.Background(), mutation)
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	before := receipt.Outbox
	publishedAt := "9999-12-31T23:59:59.999999999Z"
	got, err := store.MarkApprovalOutboxPublished(context.Background(), before.OutboxID, before.EventID, before.AggregateID, publishedAt)
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if got.DeliveryState != "published" || got.PublishedAt != publishedAt || got.FailureReason != "" {
		t.Fatalf("published outbox = %#v, want published state and canonical timestamp", got)
	}
	if got.OutboxID != before.OutboxID || got.EventID != before.EventID || got.AggregateID != before.AggregateID || got.AggregateVersion != before.AggregateVersion || got.WorkspaceID != before.WorkspaceID || got.PayloadDigest != before.PayloadDigest || got.CommittedAt != before.CommittedAt || !reflect.DeepEqual(got.CommittedEvent, before.CommittedEvent) {
		t.Fatalf("published outbox changed immutable fields: before=%#v after=%#v", before, got)
	}
	snapshot, err := store.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("snapshot after publish: %v", err)
	}
	if len(snapshot.Outbox) != 1 || !reflect.DeepEqual(snapshot.Outbox[0], got) {
		t.Fatalf("snapshot outbox = %#v, want %#v", snapshot.Outbox, got)
	}
	replayed, found, err := approvalExecutionReplay(snapshot, mutation.Command())
	if err != nil || !found || replayed.Receipt.Outbox.DeliveryState != "published" || replayed.Receipt.Outbox.PublishedAt != publishedAt {
		t.Fatalf("replayed published outbox = %+v found=%t err=%v, want published durable receipt", replayed, found, err)
	}
	restarted := newFileApprovalTransactionStoreForTest(root)
	restartedSnapshot, err := restarted.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("restart snapshot: %v", err)
	}
	if !reflect.DeepEqual(restartedSnapshot, snapshot) {
		t.Fatalf("restart snapshot = %#v, want %#v", restartedSnapshot, snapshot)
	}
	replay, err := restarted.MarkApprovalOutboxPublished(context.Background(), before.OutboxID, before.EventID, before.AggregateID, publishedAt)
	if err != nil || !reflect.DeepEqual(replay, got) {
		t.Fatalf("idempotent publish = %#v, err=%v, want %#v", replay, err, got)
	}
	unchanged, err := restarted.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("snapshot after idempotent publish: %v", err)
	}
	if !reflect.DeepEqual(unchanged, snapshot) {
		t.Fatalf("idempotent publish changed state: before=%#v after=%#v", snapshot, unchanged)
	}
	wrongTime, err := restarted.MarkApprovalOutboxPublished(context.Background(), before.OutboxID, before.EventID, before.AggregateID, "2026-09-07T12:34:57Z")
	if err != nil || !reflect.DeepEqual(wrongTime, got) {
		t.Fatalf("idempotent timestamp variation = %#v, err=%v, want existing published outbox", wrongTime, err)
	}

	db, err := sql.Open("sqlite", store.databasePath())
	if err != nil {
		t.Fatalf("open durable database: %v", err)
	}
	var deliveryState, storedPublishedAt, failureReason string
	var outboxJSON []byte
	if err := db.QueryRow("SELECT delivery_state,published_at,failure_reason,outbox_json FROM approval_outbox WHERE outbox_id = ?", got.OutboxID).Scan(&deliveryState, &storedPublishedAt, &failureReason, &outboxJSON); err != nil {
		_ = db.Close()
		t.Fatalf("read durable outbox: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close durable database: %v", err)
	}
	var persisted ApprovalOutboxV1
	if err := json.Unmarshal(outboxJSON, &persisted); err != nil {
		t.Fatalf("decode durable outbox JSON: %v", err)
	}
	if deliveryState != "published" || storedPublishedAt != publishedAt || failureReason != "" || !reflect.DeepEqual(persisted, got) {
		t.Fatalf("durable outbox columns/json = state=%q publishedAt=%q failure=%q json=%#v, want %#v", deliveryState, storedPublishedAt, failureReason, persisted, got)
	}
}

func TestApprovalTransactionMarkOutboxPublishedRejectsUnknownIdentityAndTimestamp(t *testing.T) {
	root := t.TempDir()
	store := newFileApprovalTransactionStoreForTest(root)
	receipt, err := store.Commit(context.Background(), approvalTransactionTestMutation(t, "outbox-input-key", "outbox-input-command", "outbox-input-event", "outbox-input-approval", "outbox-input-aggregate"))
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	cases := []struct {
		name        string
		outboxID    string
		eventID     string
		aggregateID string
		publishedAt string
	}{
		{name: "unknown-outbox", outboxID: "outbox-unknown", eventID: receipt.Outbox.EventID, aggregateID: receipt.Outbox.AggregateID, publishedAt: "9999-12-31T23:59:59Z"},
		{name: "wrong-event", outboxID: receipt.Outbox.OutboxID, eventID: "outbox-input-event-other", aggregateID: receipt.Outbox.AggregateID, publishedAt: "9999-12-31T23:59:59Z"},
		{name: "wrong-aggregate", outboxID: receipt.Outbox.OutboxID, eventID: receipt.Outbox.EventID, aggregateID: "outbox-input-aggregate-other", publishedAt: "9999-12-31T23:59:59Z"},
		{name: "noncanonical-offset", outboxID: receipt.Outbox.OutboxID, eventID: receipt.Outbox.EventID, aggregateID: receipt.Outbox.AggregateID, publishedAt: "9999-12-31T23:59:59+00:00"},
		{name: "noncanonical-fraction", outboxID: receipt.Outbox.OutboxID, eventID: receipt.Outbox.EventID, aggregateID: receipt.Outbox.AggregateID, publishedAt: "9999-12-31T23:59:59.000Z"},
		{name: "malformed-time", outboxID: receipt.Outbox.OutboxID, eventID: receipt.Outbox.EventID, aggregateID: receipt.Outbox.AggregateID, publishedAt: "not-a-timestamp"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := store.MarkApprovalOutboxPublished(context.Background(), testCase.outboxID, testCase.eventID, testCase.aggregateID, testCase.publishedAt)
			if err == nil || !errors.Is(err, ErrApprovalTransactionInvalid) || !reflect.DeepEqual(got, ApprovalOutboxV1{}) {
				t.Fatalf("invalid publication = %#v, err=%v, want bounded invalid and zero", got, err)
			}
			snapshot, err := store.Snapshot(context.Background())
			if err != nil {
				t.Fatalf("snapshot after invalid publication: %v", err)
			}
			if len(snapshot.Outbox) != 1 || !reflect.DeepEqual(snapshot.Outbox[0], receipt.Outbox) {
				t.Fatalf("invalid publication changed outbox: %#v, want pending %#v", snapshot.Outbox, receipt.Outbox)
			}
		})
	}
}

func TestApprovalTransactionPublishOutboxRejectsTamperedStateWithoutMutation(t *testing.T) {
	root := t.TempDir()
	store := newFileApprovalTransactionStoreForTest(root)
	receipt, err := store.Commit(context.Background(), approvalTransactionTestMutation(t, "outbox-tamper-key", "outbox-tamper-command", "outbox-tamper-event", "outbox-tamper-approval", "outbox-tamper-aggregate"))
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	db, err := sql.Open("sqlite", store.databasePath())
	if err != nil {
		t.Fatalf("open durable database: %v", err)
	}
	if _, err := db.Exec("UPDATE approval_outbox SET delivery_state = 'failed' WHERE outbox_id = ?", receipt.Outbox.OutboxID); err != nil {
		_ = db.Close()
		t.Fatalf("tamper outbox: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close tampered database: %v", err)
	}
	got, err := store.MarkApprovalOutboxPublished(context.Background(), receipt.Outbox.OutboxID, receipt.Outbox.EventID, receipt.Outbox.AggregateID, "9999-12-31T23:59:59Z")
	if err == nil || !errors.Is(err, ErrApprovalTransactionInvalid) || !reflect.DeepEqual(got, ApprovalOutboxV1{}) {
		t.Fatalf("tampered publish = %#v, err=%v, want bounded invalid and zero", got, err)
	}
	db, err = sql.Open("sqlite", store.databasePath())
	if err != nil {
		t.Fatalf("reopen tampered database: %v", err)
	}
	var deliveryState string
	if err := db.QueryRow("SELECT delivery_state FROM approval_outbox WHERE outbox_id = ?", receipt.Outbox.OutboxID).Scan(&deliveryState); err != nil {
		_ = db.Close()
		t.Fatalf("read tampered outbox: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close reopened tampered database: %v", err)
	}
	if deliveryState != "failed" {
		t.Fatalf("tampered state changed to %q, want failed", deliveryState)
	}
}

func TestApprovalTransactionPublishOutboxFaultRollsBack(t *testing.T) {
	root := t.TempDir()
	initial := newFileApprovalTransactionStoreForTest(root)
	receipt, err := initial.Commit(context.Background(), approvalTransactionTestMutation(t, "outbox-fault-key", "outbox-fault-command", "outbox-fault-event", "outbox-fault-approval", "outbox-fault-aggregate"))
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	sentinel := errors.New("outbox publish fault")
	faultStore := newApprovalTransactionStoreWithDependencies(root, approvalTransactionDependencies{fault: func(step string) error {
		if step == "outbox-publish" {
			return sentinel
		}
		return nil
	}})
	got, err := faultStore.MarkApprovalOutboxPublished(context.Background(), receipt.Outbox.OutboxID, receipt.Outbox.EventID, receipt.Outbox.AggregateID, "9999-12-31T23:59:59Z")
	if err == nil || !errors.Is(err, sentinel) || !errors.Is(err, ErrApprovalTransactionPersistence) || !reflect.DeepEqual(got, ApprovalOutboxV1{}) {
		t.Fatalf("fault publish = %#v, err=%v, want bounded persistence and zero", got, err)
	}
	snapshot, err := newFileApprovalTransactionStoreForTest(root).Snapshot(context.Background())
	if err != nil {
		t.Fatalf("snapshot after fault: %v", err)
	}
	if len(snapshot.Outbox) != 1 || !reflect.DeepEqual(snapshot.Outbox[0], receipt.Outbox) {
		t.Fatalf("fault changed outbox: %#v, want pending %#v", snapshot.Outbox, receipt.Outbox)
	}
}

func TestApprovalTransactionMarkOutboxPublishedCommitFailureRollsBack(t *testing.T) {
	root := t.TempDir()
	initial := newFileApprovalTransactionStoreForTest(root)
	receipt, err := initial.Commit(context.Background(), approvalTransactionTestMutation(t, "outbox-commit-fault-key", "outbox-commit-fault-command", "outbox-commit-fault-event", "outbox-commit-fault-approval", "outbox-commit-fault-aggregate"))
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	sentinel := errors.New("outbox commit fault")
	faultStore := newApprovalTransactionStoreWithDependencies(root, approvalTransactionDependencies{commitExecutor: func(context.Context, *sql.Conn) error {
		return sentinel
	}})
	got, err := faultStore.MarkApprovalOutboxPublished(context.Background(), receipt.Outbox.OutboxID, receipt.Outbox.EventID, receipt.Outbox.AggregateID, "9999-12-31T23:59:59Z")
	if err == nil || !errors.Is(err, sentinel) || !errors.Is(err, ErrApprovalTransactionPersistence) || !reflect.DeepEqual(got, ApprovalOutboxV1{}) {
		t.Fatalf("commit-fault publication = %#v, err=%v, want bounded persistence and zero", got, err)
	}
	snapshot, err := newFileApprovalTransactionStoreForTest(root).Snapshot(context.Background())
	if err != nil {
		t.Fatalf("snapshot after commit fault: %v", err)
	}
	if len(snapshot.Outbox) != 1 || !reflect.DeepEqual(snapshot.Outbox[0], receipt.Outbox) {
		t.Fatalf("commit fault changed outbox: %#v, want pending %#v", snapshot.Outbox, receipt.Outbox)
	}
}

func TestApprovalTransactionPublishOutboxReconcilesCommitObserverError(t *testing.T) {
	root := t.TempDir()
	initial := newFileApprovalTransactionStoreForTest(root)
	receipt, err := initial.Commit(context.Background(), approvalTransactionTestMutation(t, "outbox-observer-key", "outbox-observer-command", "outbox-observer-event", "outbox-observer-approval", "outbox-observer-aggregate"))
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	observerErr := errors.New("publication observer failed after commit")
	observerStore := newApprovalTransactionStoreWithDependencies(root, approvalTransactionDependencies{commitExecutor: func(ctx context.Context, conn *sql.Conn) error {
		if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
			return err
		}
		return observerErr
	}})
	got, err := observerStore.MarkApprovalOutboxPublished(context.Background(), receipt.Outbox.OutboxID, receipt.Outbox.EventID, receipt.Outbox.AggregateID, "9999-12-31T23:59:59Z")
	if err != nil || got.DeliveryState != "published" {
		t.Fatalf("observer-error publication = %#v, err=%v, want reconciled success", got, err)
	}
	// A successful reconciliation deliberately hides the observer diagnostic at
	// the public result boundary, while the durable record remains idempotently
	// retryable.
	restarted := newFileApprovalTransactionStoreForTest(root)
	retry, err := restarted.MarkApprovalOutboxPublished(context.Background(), receipt.Outbox.OutboxID, receipt.Outbox.EventID, receipt.Outbox.AggregateID, "2026-09-07T00:00:00Z")
	if err != nil || !reflect.DeepEqual(retry, got) {
		t.Fatalf("restart retry after observer error = %#v, err=%v, want reconciled published record", retry, err)
	}
}

func TestApprovalTransactionPublishOutboxRejectsPublishedAtBeforeCommit(t *testing.T) {
	root := t.TempDir()
	store := newFileApprovalTransactionStoreForTest(root)
	receipt, err := store.Commit(context.Background(), approvalTransactionTestMutation(t, "outbox-time-key", "outbox-time-command", "outbox-time-event", "outbox-time-approval", "outbox-time-aggregate"))
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	got, err := store.MarkApprovalOutboxPublished(context.Background(), receipt.Outbox.OutboxID, receipt.Outbox.EventID, receipt.Outbox.AggregateID, "0001-01-01T00:00:00Z")
	if err == nil || !errors.Is(err, ErrApprovalTransactionInvalid) || !reflect.DeepEqual(got, ApprovalOutboxV1{}) {
		t.Fatalf("pre-commit timestamp publication = %#v, err=%v, want bounded invalid and zero", got, err)
	}
	snapshot, err := store.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("snapshot after pre-commit rejection: %v", err)
	}
	if len(snapshot.Outbox) != 1 || !reflect.DeepEqual(snapshot.Outbox[0], receipt.Outbox) {
		t.Fatalf("pre-commit timestamp rejection changed outbox: %#v", snapshot.Outbox)
	}
}
