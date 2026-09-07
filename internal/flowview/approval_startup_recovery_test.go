package flowview

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"codeflow/internal/semantic"
)

func TestNewServerRecoversPendingApprovalOutboxExactlyOnceBeforeServing(t *testing.T) {
	srv, root, _ := newFlowViewApprovalHistoryFixture(t)
	transactionPath := filepath.Join(root, ".codeflow", "approval-transactions", "approval-transactions.sqlite3")
	ledgerPath := srv.hub.ledgerPath
	transactionStore := semantic.NewApprovalTransactionStore(root, srv.engine)
	before, err := transactionStore.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("snapshot before startup recovery: %v", err)
	}
	if len(before.Outbox) != 1 || before.Outbox[0].DeliveryState != "pending" || before.Outbox[0].PublishedAt != "" {
		t.Fatalf("fixture outbox = %+v, want exactly one pending record", before.Outbox)
	}
	pendingOutbox := before.Outbox[0]
	if srv.approvalService == nil {
		t.Fatal("fixture server approval service is nil")
	}
	approvalService := srv.approvalService
	deliveries, err := approvalService.PendingApprovalOutboxDeliveries(context.Background())
	if err != nil {
		t.Fatalf("load pending delivery: %v", err)
	}
	if len(deliveries) != 1 {
		t.Fatalf("pending deliveries = %d, want 1", len(deliveries))
	}
	delivery := deliveries[0]
	transactionBefore, err := os.ReadFile(transactionPath)
	if err != nil {
		t.Fatalf("read transaction database before recovery: %v", err)
	}
	ledgerBefore, err := os.ReadFile(ledgerPath)
	if err != nil {
		t.Fatalf("read event ledger before recovery: %v", err)
	}
	ledgerRecordsBefore := approvalStartupLedgerRecords(t, ledgerBefore)
	approvalRecordsBefore := approvalStartupApprovalRecords(t, ledgerRecordsBefore)
	if len(approvalRecordsBefore) != 0 {
		t.Fatalf("fixture already contains approval.updated records: %d", len(approvalRecordsBefore))
	}
	headBefore := srv.hub.headSeq
	if err := srv.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown fixture server: %v", err)
	}

	restarted, err := NewServer(Config{RepoRoot: root, Port: 0, AuthToken: "startup-recovery-token"})
	if err != nil {
		t.Fatalf("NewServer startup recovery: %v", err)
	}
	defer func() { _ = restarted.Shutdown(context.Background()) }()
	after, err := semantic.NewApprovalTransactionStore(root, restarted.engine).Snapshot(context.Background())
	if err != nil {
		t.Fatalf("snapshot after startup recovery: %v", err)
	}
	if len(after.Outbox) != 1 || after.Outbox[0].DeliveryState != "published" || after.Outbox[0].PublishedAt == "" || after.Outbox[0].FailureReason != "" {
		t.Fatalf("recovered outbox = %+v, want one published record", after.Outbox)
	}
	committedAt, err := time.Parse(time.RFC3339Nano, pendingOutbox.CommittedAt)
	if err != nil {
		t.Fatalf("parse committedAt: %v", err)
	}
	publishedAt, err := time.Parse(time.RFC3339Nano, after.Outbox[0].PublishedAt)
	if err != nil || publishedAt.Location() != time.UTC || publishedAt.Format(time.RFC3339Nano) != after.Outbox[0].PublishedAt || publishedAt.Before(committedAt) {
		t.Fatalf("publishedAt = %q, want canonical UTC at or after %q", after.Outbox[0].PublishedAt, pendingOutbox.CommittedAt)
	}
	wantOutbox := pendingOutbox
	wantOutbox.DeliveryState = "published"
	wantOutbox.PublishedAt = after.Outbox[0].PublishedAt
	if after.Outbox[0] != wantOutbox || after.Outbox[0].CommittedEvent != pendingOutbox.CommittedEvent {
		t.Fatalf("startup recovery changed immutable outbox fields: before=%+v after=%+v", pendingOutbox, after.Outbox[0])
	}
	transactionAfter, err := os.ReadFile(transactionPath)
	if err != nil {
		t.Fatalf("read transaction database after recovery: %v", err)
	}
	if len(transactionBefore) == 0 || len(transactionAfter) == 0 {
		t.Fatalf("transaction database bytes before/after recovery = %d/%d, want non-empty", len(transactionBefore), len(transactionAfter))
	}
	ledgerAfter, err := os.ReadFile(ledgerPath)
	if err != nil {
		t.Fatalf("read event ledger after recovery: %v", err)
	}
	ledgerRecordsAfter := approvalStartupLedgerRecords(t, ledgerAfter)
	approvalRecordsAfter := approvalStartupApprovalRecords(t, ledgerRecordsAfter)
	if len(approvalRecordsAfter) != 1 {
		t.Fatalf("approval.updated records after startup recovery = %d, want exactly 1", len(approvalRecordsAfter))
	}
	approvalRecord := approvalRecordsAfter[0]
	if approvalRecord.Sequence != headBefore+1 || approvalRecord.EventType != "approval.updated" {
		t.Fatalf("approval publication sequence/type = %d/%q, want %d/approval.updated", approvalRecord.Sequence, approvalRecord.EventType, headBefore+1)
	}
	data, _, err := validateApprovalUpdatedData(approvalRecord.Data)
	if err != nil {
		t.Fatalf("validate startup approval publication: %v", err)
	}
	if data.ApprovalEvent != pendingOutbox.CommittedEvent || data.CommittedAt != pendingOutbox.CommittedAt || data.ComputedBasisID != delivery.ComputedBasisID || data.ValidatedAgainstSnapshotID != delivery.ValidatedSnapshotID || data.GenerationID != delivery.GenerationID {
		t.Fatalf("startup approval publication binding = %+v, want outbox=%+v delivery=%+v", data, pendingOutbox, delivery)
	}
	if restarted.hub.headSeq != headBefore+1 {
		t.Fatalf("restarted hub head = %d, want %d", restarted.hub.headSeq, headBefore+1)
	}
	if restarted.approvalService == nil {
		t.Fatal("restarted server approval service is nil")
	}
	remaining := restarted.approvalService
	pendingAfter, err := remaining.PendingApprovalOutboxDeliveries(context.Background())
	if err != nil {
		t.Fatalf("load pending after startup recovery: %v", err)
	}
	if len(pendingAfter) != 0 {
		t.Fatalf("pending deliveries after startup recovery = %d, want 0", len(pendingAfter))
	}
	ch, replay, needsSnapshotSync, cancel := restarted.hub.Subscribe(approvalRecord.EventID)
	defer cancel()
	if len(replay) != 0 || needsSnapshotSync {
		t.Fatalf("subscription before Start replay=%+v needsSnapshotSync=%v, want no replay", replay, needsSnapshotSync)
	}
	select {
	case got := <-ch:
		t.Fatalf("constructor recovery left a subscriber event: %+v", got)
	default:
	}

	if err := restarted.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown recovered server: %v", err)
	}

	transactionBeforeSecond, err := os.ReadFile(transactionPath)
	if err != nil {
		t.Fatalf("read transaction database before second startup: %v", err)
	}
	ledgerBeforeSecond, err := os.ReadFile(ledgerPath)
	if err != nil {
		t.Fatalf("read event ledger before second startup: %v", err)
	}

	second, err := NewServer(Config{RepoRoot: root, Port: 0, AuthToken: "startup-recovery-token-2"})
	if err != nil {
		t.Fatalf("second NewServer: %v", err)
	}
	defer func() { _ = second.Shutdown(context.Background()) }()
	secondSnapshot, err := semantic.NewApprovalTransactionStore(root, second.engine).Snapshot(context.Background())
	if err != nil {
		t.Fatalf("snapshot after second startup: %v", err)
	}
	if len(secondSnapshot.Outbox) != 1 || secondSnapshot.Outbox[0] != after.Outbox[0] {
		t.Fatalf("second startup changed outbox: first=%+v second=%+v", after.Outbox[0], secondSnapshot.Outbox)
	}
	transactionAfterSecond, err := os.ReadFile(transactionPath)
	if err != nil {
		t.Fatalf("read transaction database after second startup: %v", err)
	}
	ledgerAfterSecond, err := os.ReadFile(ledgerPath)
	if err != nil {
		t.Fatalf("read event ledger after second startup: %v", err)
	}
	if !bytes.Equal(transactionBeforeSecond, transactionAfterSecond) || !bytes.Equal(ledgerBeforeSecond, ledgerAfterSecond) || second.hub.headSeq != restarted.hub.headSeq {
		t.Fatalf("second startup mutated durable state: transactionEqual=%v ledgerEqual=%v firstHead=%d secondHead=%d", bytes.Equal(transactionBeforeSecond, transactionAfterSecond), bytes.Equal(ledgerBeforeSecond, ledgerAfterSecond), restarted.hub.headSeq, second.hub.headSeq)
	}
	secondApprovalRecords := approvalStartupApprovalRecords(t, approvalStartupLedgerRecords(t, ledgerAfterSecond))
	if len(secondApprovalRecords) != 1 || secondApprovalRecords[0].EventID != approvalRecord.EventID {
		t.Fatalf("second startup approval records = %+v, want one original publication", secondApprovalRecords)
	}
}

func approvalStartupLedgerRecords(t *testing.T, ledger []byte) []semantic.EventEnvelope {
	t.Helper()
	records := make([]semantic.EventEnvelope, 0)
	for _, line := range bytes.Split(ledger, []byte{'\n'}) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var envelope semantic.EventEnvelope
		if err := json.Unmarshal(line, &envelope); err != nil {
			t.Fatalf("decode event ledger record: %v", err)
		}
		records = append(records, envelope)
	}
	return records
}

func approvalStartupApprovalRecords(t *testing.T, records []semantic.EventEnvelope) []semantic.EventEnvelope {
	t.Helper()
	approvalRecords := make([]semantic.EventEnvelope, 0)
	for _, record := range records {
		if record.EventType == "approval.updated" {
			approvalRecords = append(approvalRecords, record)
		}
	}
	return approvalRecords
}
