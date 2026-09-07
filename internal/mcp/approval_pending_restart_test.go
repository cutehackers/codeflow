package mcp

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"codeflow/internal/semantic"
)

// VS09-A7/A12: an actual committed outbox left pending at process shutdown
// is delivered once when the next authorized MCP mutation starts recovery.
func TestMCPApprovalPendingCommitPublishesOnceOnRestartedMutation(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") == "mcp" {
		t.Skip("helper process")
	}
	fixture, enrichment := newMCPApprovalEnrichmentFixture(t)
	defer fixture.server.Close()
	coord, err := fixture.server.getLiveCoordinator(fixture.root)
	if err != nil {
		t.Fatal(err)
	}
	stream, closeStream := approvalMCPStream(t, coord.URL(), "force-snapshot")
	head := nextApprovalMCPEnvelope(t, stream)
	closeStream()
	access, err := fixture.server.authorizeSemanticApproval(context.Background(), fixture.root)
	if err != nil {
		t.Fatal(err)
	}
	service, err := semantic.NewApprovalExecutionService(fixture.root, coord.SnapshotEngine(), semantic.NewDurableProposalStore(fixture.root))
	if err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(mcpApprovalRequestArgs(fixture.root, enrichment, "pending-restart"))
	envelope, err := semantic.ParseApprovalCommandDraftEnvelopeJSON(args)
	if err != nil {
		t.Fatal(err)
	}
	pending, err := service.Execute(context.Background(), access, envelope.Draft)
	if err != nil {
		t.Fatal(err)
	}
	if pending.Receipt.Outbox.DeliveryState != "pending" {
		t.Fatalf("outbox=%+v", pending.Receipt.Outbox)
	}
	if err := fixture.server.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := NewServer(Config{RepoRoot: fixture.root})
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	for attempt := 0; attempt < 2; attempt++ {
		body := requireMCPApprovalHistorySuccess(t, serveMCPApprovalHistoryRequest(t, restarted, mcpApprovalHistoryToolCall("submit_semantic_approval", args)))
		var result semantic.ApprovalExecutionResult
		if err := json.Unmarshal([]byte(body), &result); err != nil {
			t.Fatal(err)
		}
		if !result.Receipt.Replayed || result.Receipt.Outbox.DeliveryState != "published" || result.Receipt.Outbox.PublishedAt == "" || !reflect.DeepEqual(result.Receipt.Event, pending.Receipt.Event) {
			t.Fatalf("restarted mutation result=%+v", result)
		}
	}
	coord, err = restarted.getLiveCoordinator(fixture.root)
	if err != nil {
		t.Fatal(err)
	}
	stream, closeStream = approvalMCPStream(t, coord.URL(), head.EventID)
	defer closeStream()
	event := nextApprovalMCPEnvelope(t, stream)
	if event.EventType != "approval.updated" || event.Sequence != head.Sequence+1 {
		t.Fatalf("recovered SSE event=%+v", event)
	}
	data, _ := json.Marshal(event.Data)
	var binding struct {
		ApprovalEvent semantic.ApprovalOutboxCommittedEventV1 `json:"approvalEvent"`
	}
	if err := json.Unmarshal(data, &binding); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(binding.ApprovalEvent, pending.Receipt.Outbox.CommittedEvent) {
		t.Fatalf("recovered event binding=%s", data)
	}
	syncStream, closeSync := approvalMCPStream(t, coord.URL(), "force-snapshot")
	defer closeSync()
	if got := nextApprovalMCPEnvelope(t, syncStream); got.Sequence != event.Sequence {
		t.Fatalf("pending recovery duplicated event: %+v", got)
	}
}
