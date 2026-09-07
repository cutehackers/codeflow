package flowview

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"codeflow/internal/semantic"
)

func TestSubmitSemanticApprovalPublishesPendingReplayExactlyOnce(t *testing.T) {
	srv, root, enrichment := newFlowViewApprovalHistoryFixture(t)
	defer func() { _ = srv.Shutdown(context.Background()) }()

	transactionPath := filepath.Join(root, ".codeflow", "approval-transactions", "approval-transactions.sqlite3")
	transactionStore := semantic.NewApprovalTransactionStore(root, srv.engine)
	before, err := transactionStore.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("snapshot before replay publication: %v", err)
	}
	if len(before.Events) != 1 || len(before.Aggregates) != 1 || len(before.IdempotencyResults) != 1 || len(before.Outbox) != 1 {
		t.Fatalf("durable rows before replay publication = events %d aggregates %d idempotency %d outbox %d, want one each", len(before.Events), len(before.Aggregates), len(before.IdempotencyResults), len(before.Outbox))
	}
	pendingOutbox := before.Outbox[0]
	if pendingOutbox.DeliveryState != "pending" || pendingOutbox.PublishedAt != "" || pendingOutbox.FailureReason != "" {
		t.Fatalf("fixture outbox before replay publication = %+v, want pending", pendingOutbox)
	}
	transactionBefore, err := os.ReadFile(transactionPath)
	if err != nil {
		t.Fatalf("read transaction bytes before replay publication: %v", err)
	}
	ledgerBefore, err := os.ReadFile(srv.hub.ledgerPath)
	if err != nil {
		t.Fatalf("read event ledger before replay publication: %v", err)
	}
	if approvalRecords := approvalStartupApprovalRecords(t, approvalStartupLedgerRecords(t, ledgerBefore)); len(approvalRecords) != 0 {
		t.Fatalf("approval.updated records before replay publication = %d, want 0", len(approvalRecords))
	}
	headBefore := srv.hub.headSeq

	access, err := srv.authorizeSemanticApproval(context.Background(), root)
	if err != nil {
		t.Fatalf("authorize configured approval workspace: %v", err)
	}
	draft, err := semantic.ParseApprovalCommandDraftJSON([]byte(flowViewApprovalBody(enrichment, "history-endpoint-commit", "approve", false, "")))
	if err != nil {
		t.Fatalf("parse exact fixture approval draft: %v", err)
	}

	first, err := srv.SubmitSemanticApproval(context.Background(), access, draft)
	if err != nil {
		t.Fatalf("submit pending semantic approval replay: %v", err)
	}
	if !first.Receipt.Replayed {
		t.Fatalf("first submission replay = false, want true for existing durable commit")
	}
	if first.Receipt.Outbox.DeliveryState != "published" || first.Receipt.Outbox.PublishedAt == "" || first.Receipt.Outbox.FailureReason != "" {
		t.Fatalf("first submission outbox = %+v, want published", first.Receipt.Outbox)
	}
	committedAt, err := time.Parse(time.RFC3339Nano, pendingOutbox.CommittedAt)
	if err != nil {
		t.Fatalf("parse committedAt: %v", err)
	}
	publishedAt, err := time.Parse(time.RFC3339Nano, first.Receipt.Outbox.PublishedAt)
	if err != nil || publishedAt.Location() != time.UTC || publishedAt.Format(time.RFC3339Nano) != first.Receipt.Outbox.PublishedAt || publishedAt.Before(committedAt) {
		t.Fatalf("publishedAt = %q, want canonical UTC at or after %q", first.Receipt.Outbox.PublishedAt, pendingOutbox.CommittedAt)
	}
	wantPublishedOutbox := pendingOutbox
	wantPublishedOutbox.DeliveryState = "published"
	wantPublishedOutbox.PublishedAt = first.Receipt.Outbox.PublishedAt
	if !reflect.DeepEqual(first.Receipt.Event, before.Events[0]) || !reflect.DeepEqual(first.Receipt.Aggregate, before.Aggregates[0]) || !reflect.DeepEqual(first.Receipt.IdempotencyResult, before.IdempotencyResults[0]) || !reflect.DeepEqual(first.Receipt.Outbox, wantPublishedOutbox) {
		t.Fatalf("replayed receipt changed durable immutable values: receipt=%+v before event=%+v aggregate=%+v idempotency=%+v outbox=%+v", first.Receipt, before.Events[0], before.Aggregates[0], before.IdempotencyResults[0], pendingOutbox)
	}

	afterFirst, err := transactionStore.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("snapshot after replay publication: %v", err)
	}
	if len(afterFirst.Events) != 1 || len(afterFirst.Aggregates) != 1 || len(afterFirst.IdempotencyResults) != 1 || len(afterFirst.Outbox) != 1 || afterFirst.Outbox[0].DeliveryState != "published" {
		t.Fatalf("durable rows after replay publication = events %d aggregates %d idempotency %d outbox %d/%+v, want one published outbox", len(afterFirst.Events), len(afterFirst.Aggregates), len(afterFirst.IdempotencyResults), len(afterFirst.Outbox), afterFirst.Outbox)
	}
	if srv.approvalService == nil {
		t.Fatal("server approval service is nil after replay publication")
	}
	service := srv.approvalService
	pendingAfterFirst, err := service.PendingApprovalOutboxDeliveries(context.Background())
	if err != nil {
		t.Fatalf("load pending deliveries after replay publication: %v", err)
	}
	if pendingAfterFirst == nil || len(pendingAfterFirst) != 0 {
		t.Fatalf("pending deliveries after replay publication = %+v, want non-nil empty", pendingAfterFirst)
	}
	ledgerAfterFirst, err := os.ReadFile(srv.hub.ledgerPath)
	if err != nil {
		t.Fatalf("read event ledger after replay publication: %v", err)
	}
	approvalRecordsAfterFirst := approvalStartupApprovalRecords(t, approvalStartupLedgerRecords(t, ledgerAfterFirst))
	if len(approvalRecordsAfterFirst) != 1 {
		t.Fatalf("approval.updated records after replay publication = %d, want exactly 1", len(approvalRecordsAfterFirst))
	}
	approvalRecord := approvalRecordsAfterFirst[0]
	if approvalRecord.Sequence != headBefore+1 || approvalRecord.EventID == "" || approvalRecord.EventType != "approval.updated" {
		t.Fatalf("approval publication = sequence %d eventID %q type %q, want non-empty envelope ID, sequence %d, approval.updated", approvalRecord.Sequence, approvalRecord.EventID, approvalRecord.EventType, headBefore+1)
	}
	data, _, err := validateApprovalUpdatedData(approvalRecord.Data)
	if err != nil {
		t.Fatalf("validate approval publication after replay: %v", err)
	}
	if data.ApprovalEvent != pendingOutbox.CommittedEvent || data.CommittedAt != pendingOutbox.CommittedAt || data.ComputedBasisID != first.ComputedBasisID || data.ValidatedAgainstSnapshotID != first.ValidatedSnapshotID || data.GenerationID != first.GenerationID {
		t.Fatalf("approval publication binding = %+v, want committed=%+v basis=%q snapshot=%q generation=%q", data, pendingOutbox.CommittedEvent, first.ComputedBasisID, first.ValidatedSnapshotID, first.GenerationID)
	}
	if srv.hub.headSeq != headBefore+1 {
		t.Fatalf("hub head after replay publication = %d, want %d", srv.hub.headSeq, headBefore+1)
	}
	transactionAfterFirst, err := os.ReadFile(transactionPath)
	if err != nil {
		t.Fatalf("read transaction bytes after replay publication: %v", err)
	}
	if len(transactionBefore) == 0 || len(transactionAfterFirst) == 0 {
		t.Fatalf("transaction bytes before/after replay publication = %d/%d, want non-empty", len(transactionBefore), len(transactionAfterFirst))
	}

	subscriber, replay, needsSnapshotSync, cancel := srv.hub.Subscribe(approvalRecord.EventID)
	defer cancel()
	if len(replay) != 0 || needsSnapshotSync {
		t.Fatalf("second exact submission subscription replay=%+v needsSnapshotSync=%v, want no replay", replay, needsSnapshotSync)
	}
	firstSnapshot := afterFirst
	firstHead := srv.hub.headSeq
	firstTransactionBytes := append([]byte(nil), transactionAfterFirst...)
	firstLedgerBytes := append([]byte(nil), ledgerAfterFirst...)

	second, err := srv.SubmitSemanticApproval(context.Background(), access, draft)
	if err != nil {
		t.Fatalf("submit exact published approval replay: %v", err)
	}
	if !reflect.DeepEqual(second, first) {
		t.Fatalf("exact second submission changed replayed receipt: first=%+v second=%+v", first, second)
	}
	select {
	case event := <-subscriber:
		t.Fatalf("exact replay emitted a duplicate subscriber event: %+v", event)
	default:
	}
	secondSnapshot, err := transactionStore.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("snapshot after exact replay: %v", err)
	}
	if !reflect.DeepEqual(secondSnapshot, firstSnapshot) {
		t.Fatalf("exact replay changed transaction snapshot: first=%+v second=%+v", firstSnapshot, secondSnapshot)
	}
	transactionAfterSecond, err := os.ReadFile(transactionPath)
	if err != nil {
		t.Fatalf("read transaction bytes after exact replay: %v", err)
	}
	ledgerAfterSecond, err := os.ReadFile(srv.hub.ledgerPath)
	if err != nil {
		t.Fatalf("read event ledger after exact replay: %v", err)
	}
	if !bytes.Equal(firstTransactionBytes, transactionAfterSecond) || !bytes.Equal(firstLedgerBytes, ledgerAfterSecond) || srv.hub.headSeq != firstHead {
		t.Fatalf("exact replay mutated durable state: transactionEqual=%v ledgerEqual=%v firstHead=%d secondHead=%d", bytes.Equal(firstTransactionBytes, transactionAfterSecond), bytes.Equal(firstLedgerBytes, ledgerAfterSecond), firstHead, srv.hub.headSeq)
	}
	secondApprovalRecords := approvalStartupApprovalRecords(t, approvalStartupLedgerRecords(t, ledgerAfterSecond))
	if len(secondApprovalRecords) != 1 || secondApprovalRecords[0].EventID != approvalRecord.EventID {
		t.Fatalf("approval.updated records after exact replay = %+v, want one original publication", secondApprovalRecords)
	}
}
