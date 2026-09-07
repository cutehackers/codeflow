package flowview

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"codeflow/internal/semantic"
)

func TestFlowViewApprovalMutationUsesSharedServiceAndPublishesPendingReplay(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") == "flowview" {
		t.Skip("helper process")
	}
	srv, root, enrichment := newFlowViewApprovalHistoryFixture(t)
	defer func() { _ = srv.Shutdown(context.Background()) }()

	transactionStore := semantic.NewApprovalTransactionStore(root, srv.engine)
	before, err := transactionStore.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("snapshot before shared-service mutation: %v", err)
	}
	if len(before.Events) != 1 || len(before.Aggregates) != 1 || len(before.IdempotencyResults) != 1 || len(before.Outbox) != 1 {
		t.Fatalf("durable rows before shared-service mutation = events %d aggregates %d idempotency %d outbox %d, want one each", len(before.Events), len(before.Aggregates), len(before.IdempotencyResults), len(before.Outbox))
	}
	pendingOutbox := before.Outbox[0]
	if pendingOutbox.DeliveryState != "pending" || pendingOutbox.PublishedAt != "" || pendingOutbox.FailureReason != "" {
		t.Fatalf("fixture outbox before shared-service mutation = %+v, want pending", pendingOutbox)
	}
	headBefore := srv.hub.headSeq

	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/semantic/approve?token="+url.QueryEscape(srv.AuthToken()), strings.NewReader(flowViewApprovalBody(enrichment, "history-endpoint-commit", "approve", false, "")))
	request.Host = "127.0.0.1"
	response := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("shared-service mutation status = %d, body = %s", response.Code, response.Body.String())
	}
	var submitted semantic.ApprovalExecutionResult
	if err := json.Unmarshal(response.Body.Bytes(), &submitted); err != nil {
		t.Fatalf("decode shared-service mutation result: %v", err)
	}
	if !submitted.Receipt.Replayed {
		t.Fatalf("shared-service mutation replay = false, want true for fixture commit")
	}
	if submitted.Receipt.Outbox.DeliveryState != "published" || submitted.Receipt.Outbox.PublishedAt == "" || submitted.Receipt.Outbox.FailureReason != "" {
		t.Fatalf("shared-service mutation outbox = %+v, want published", submitted.Receipt.Outbox)
	}
	wantPublishedOutbox := pendingOutbox
	wantPublishedOutbox.DeliveryState = "published"
	wantPublishedOutbox.PublishedAt = submitted.Receipt.Outbox.PublishedAt
	if !reflect.DeepEqual(submitted.Receipt.Event, before.Events[0]) || !reflect.DeepEqual(submitted.Receipt.Aggregate, before.Aggregates[0]) || !reflect.DeepEqual(submitted.Receipt.IdempotencyResult, before.IdempotencyResults[0]) || !reflect.DeepEqual(submitted.Receipt.Outbox, wantPublishedOutbox) {
		t.Fatalf("shared-service mutation changed durable receipt values: result=%+v before event=%+v aggregate=%+v idempotency=%+v outbox=%+v", submitted, before.Events[0], before.Aggregates[0], before.IdempotencyResults[0], pendingOutbox)
	}

	after, err := transactionStore.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("snapshot after shared-service mutation: %v", err)
	}
	if len(after.Events) != 1 || len(after.Aggregates) != 1 || len(after.IdempotencyResults) != 1 || len(after.Outbox) != 1 || after.Outbox[0].DeliveryState != "published" {
		t.Fatalf("durable rows after shared-service mutation = events %d aggregates %d idempotency %d outbox %d/%+v, want one published outbox", len(after.Events), len(after.Aggregates), len(after.IdempotencyResults), len(after.Outbox), after.Outbox)
	}
	ledger, err := os.ReadFile(srv.hub.ledgerPath)
	if err != nil {
		t.Fatalf("read event ledger after shared-service mutation: %v", err)
	}
	approvalRecords := approvalStartupApprovalRecords(t, approvalStartupLedgerRecords(t, ledger))
	if len(approvalRecords) != 1 {
		t.Fatalf("approval.updated records after shared-service mutation = %d, want exactly 1", len(approvalRecords))
	}
	approvalRecord := approvalRecords[0]
	if approvalRecord.EventID == "" || approvalRecord.Sequence != headBefore+1 || approvalRecord.EventType != "approval.updated" {
		t.Fatalf("shared-service approval publication = sequence %d eventID %q type %q, want non-empty ID, sequence %d, approval.updated", approvalRecord.Sequence, approvalRecord.EventID, approvalRecord.EventType, headBefore+1)
	}
	data, _, err := validateApprovalUpdatedData(approvalRecord.Data)
	if err != nil {
		t.Fatalf("validate shared-service approval publication: %v", err)
	}
	if !reflect.DeepEqual(data.ApprovalEvent, pendingOutbox.CommittedEvent) || data.CommittedAt != pendingOutbox.CommittedAt || data.ComputedBasisID != submitted.ComputedBasisID || data.ValidatedAgainstSnapshotID != submitted.ValidatedSnapshotID || data.GenerationID != submitted.GenerationID {
		t.Fatalf("shared-service approval publication binding = %+v, want committed=%+v basis=%q snapshot=%q generation=%q", data, pendingOutbox.CommittedEvent, submitted.ComputedBasisID, submitted.ValidatedSnapshotID, submitted.GenerationID)
	}
	if srv.hub.headSeq != headBefore+1 {
		t.Fatalf("hub head after shared-service mutation = %d, want %d", srv.hub.headSeq, headBefore+1)
	}
	pending, err := srv.approvalService.PendingApprovalOutboxDeliveries(context.Background())
	if err != nil {
		t.Fatalf("load pending deliveries after shared-service mutation: %v", err)
	}
	if pending == nil || len(pending) != 0 {
		t.Fatalf("pending deliveries after shared-service mutation = %+v, want non-nil empty", pending)
	}
}

func TestFlowViewApprovalHandlersFailClosedWithoutSharedService(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") == "flowview" {
		t.Skip("helper process")
	}
	srv, root, enrichment := newFlowViewApprovalHistoryFixture(t)
	defer func() { _ = srv.Shutdown(context.Background()) }()

	transactionStore := semantic.NewApprovalTransactionStore(root, srv.engine)
	before, err := transactionStore.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("snapshot before shared-service absence: %v", err)
	}
	transactionPath := filepath.Join(root, ".codeflow", "approval-transactions", "approval-transactions.sqlite3")
	transactionBytesBefore, err := os.ReadFile(transactionPath)
	if err != nil {
		t.Fatalf("read transaction bytes before shared-service absence: %v", err)
	}
	ledgerBytesBefore, err := os.ReadFile(srv.hub.ledgerPath)
	if err != nil {
		t.Fatalf("read event ledger before shared-service absence: %v", err)
	}
	headBefore := srv.hub.headSeq
	srv.approvalService = nil

	t.Run("mutation", func(t *testing.T) {
		request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/semantic/approve?token="+url.QueryEscape(srv.AuthToken()), strings.NewReader(flowViewApprovalBody(enrichment, "history-endpoint-commit", "approve", false, "")))
		request.Host = "127.0.0.1"
		response := httptest.NewRecorder()
		srv.httpServer.Handler.ServeHTTP(response, request)
		requireApprovalSharedServiceUnavailable(t, response)
	})

	t.Run("history", func(t *testing.T) {
		request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/semantic/approval-history?proposalId="+url.QueryEscape(enrichment.Proposal.ProposalID)+"&evidencePackId="+url.QueryEscape(enrichment.Pack.EvidencePackID)+"&token="+url.QueryEscape(srv.AuthToken()), nil)
		request.Host = "127.0.0.1"
		response := httptest.NewRecorder()
		srv.httpServer.Handler.ServeHTTP(response, request)
		requireApprovalSharedServiceUnavailable(t, response)
	})

	after, err := transactionStore.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("snapshot after shared-service absence: %v", err)
	}
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("shared-service absence changed transaction snapshot: before=%+v after=%+v", before, after)
	}
	transactionBytesAfter, err := os.ReadFile(transactionPath)
	if err != nil {
		t.Fatalf("read transaction bytes after shared-service absence: %v", err)
	}
	ledgerBytesAfter, err := os.ReadFile(srv.hub.ledgerPath)
	if err != nil {
		t.Fatalf("read event ledger after shared-service absence: %v", err)
	}
	if !bytes.Equal(transactionBytesBefore, transactionBytesAfter) || !bytes.Equal(ledgerBytesBefore, ledgerBytesAfter) || srv.hub.headSeq != headBefore {
		t.Fatalf("shared-service absence mutated durable state: transactionEqual=%v ledgerEqual=%v beforeHead=%d afterHead=%d", bytes.Equal(transactionBytesBefore, transactionBytesAfter), bytes.Equal(ledgerBytesBefore, ledgerBytesAfter), headBefore, srv.hub.headSeq)
	}
}

func requireApprovalSharedServiceUnavailable(t *testing.T, response *httptest.ResponseRecorder) {
	t.Helper()
	if response.Code >= http.StatusOK && response.Code < http.StatusMultipleChoices {
		t.Errorf("response status = %d, body = %s, want bounded non-2xx approval_unavailable", response.Code, response.Body.String())
		return
	}
	if response.Header().Get("Content-Type") != "application/json" {
		t.Errorf("response content type = %q, want application/json", response.Header().Get("Content-Type"))
	}
	var body map[string]string
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Errorf("decode bounded unavailable response: %v, body=%s", err, response.Body.String())
		return
	}
	if body["code"] != "approval_unavailable" {
		t.Errorf("bounded unavailable response = %#v, want approval_unavailable", body)
	}
}
