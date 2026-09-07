package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"codeflow/internal/rflscvs09evidence"
	"codeflow/internal/semantic"
)

// VS09-A5/A12: MCP mutation must return the shared delivery receipt, and the
// same committed event must appear exactly once through the public SSE seam.
func TestMCPApprovalMutationPublishesOnceAcrossRetryAndRestart(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") == "mcp" {
		t.Skip("helper process")
	}
	fixture, enrichment := newMCPApprovalEnrichmentFixture(t)
	defer fixture.server.Close()
	coord, err := fixture.server.getLiveCoordinator(fixture.root)
	if err != nil {
		t.Fatal(err)
	}
	readHead := func(server *Server) semantic.EventEnvelope {
		t.Helper()
		coordinator, err := server.getLiveCoordinator(fixture.root)
		if err != nil {
			t.Fatal(err)
		}
		stream, cancel := approvalMCPStream(t, coordinator.URL(), "force-snapshot")
		defer cancel()
		return nextApprovalMCPEnvelope(t, stream)
	}
	head := readHead(fixture.server)
	if head.EventID != "mcp-factory-event-1" {
		t.Fatalf("snapshot_sync cursor = %q, want the opaque published generation event ID", head.EventID)
	}
	stream, cancel := approvalMCPStream(t, coord.URL(), "force-snapshot")
	defer cancel()
	_ = nextApprovalMCPEnvelope(t, stream)
	args := mcpApprovalRequestArgs(fixture.root, enrichment, "shared-submission")
	rawArgs, _ := json.Marshal(args)
	submit := func(server *Server) semantic.ApprovalExecutionResult {
		t.Helper()
		body := requireMCPApprovalHistorySuccess(t, serveMCPApprovalHistoryRequest(t, server, mcpApprovalHistoryToolCall("submit_semantic_approval", rawArgs)))
		var result semantic.ApprovalExecutionResult
		if err := json.Unmarshal([]byte(body), &result); err != nil {
			t.Fatal(err)
		}
		if result.Receipt.Outbox.DeliveryState != "published" || result.Receipt.Outbox.PublishedAt == "" {
			t.Fatalf("MCP approval outbox = %+v, want published receipt", result.Receipt.Outbox)
		}
		return result
	}
	first := submit(fixture.server)
	event := nextApprovalMCPEnvelope(t, stream)
	if event.EventType != "approval.updated" || event.Sequence != head.Sequence+1 {
		t.Fatalf("published event = %+v, initial sequence=%d", event, head.Sequence)
	}
	data, _ := json.Marshal(event.Data)
	var binding struct {
		ApprovalEvent semantic.ApprovalOutboxCommittedEventV1 `json:"approvalEvent"`
		CommittedAt   string                                  `json:"committedAt"`
	}
	if err := json.Unmarshal(data, &binding); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(binding.ApprovalEvent, first.Receipt.Outbox.CommittedEvent) || binding.CommittedAt != first.Receipt.Outbox.CommittedAt {
		t.Fatalf("SSE binding = %s", data)
	}
	retry := submit(fixture.server)
	if !retry.Receipt.Replayed || !reflect.DeepEqual(retry.Receipt.Event, first.Receipt.Event) || !reflect.DeepEqual(retry.Receipt.Outbox, first.Receipt.Outbox) {
		t.Fatalf("retry differs: %+v", retry)
	}
	// The direct tool-dispatch boundary must use the same service too.
	value, err := fixture.server.executeTool(context.Background(), "submit_semantic_approval", args)
	if err != nil {
		t.Fatal(err)
	}
	if direct, ok := value.(semantic.ApprovalExecutionResult); !ok || direct.Receipt.Outbox.DeliveryState != "published" {
		t.Fatalf("direct submission = %+v", value)
	}
	if got := readHead(fixture.server); got.Sequence != event.Sequence {
		t.Fatalf("retry broadcast another event: %+v", got)
	}
	cancel()
	if err := fixture.server.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := NewServer(Config{RepoRoot: fixture.root})
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	replayed := submit(restarted)
	if !replayed.Receipt.Replayed || !reflect.DeepEqual(replayed.Receipt.Event, first.Receipt.Event) || !reflect.DeepEqual(replayed.Receipt.Outbox, first.Receipt.Outbox) {
		t.Fatalf("restart differs: %+v", replayed)
	}
	if got := readHead(restarted); got.Sequence != event.Sequence {
		t.Fatalf("restart broadcast another event: %+v", got)
	}
	restartedCoordinator, err := restarted.getLiveCoordinator(fixture.root)
	if err != nil {
		t.Fatal(err)
	}
	replayStream, closeReplay := approvalMCPStream(t, restartedCoordinator.URL(), head.EventID)
	defer closeReplay()
	if replay := nextApprovalMCPEnvelope(t, replayStream); !reflect.DeepEqual(replay, event) {
		t.Fatalf("restarted SSE replay differs: %+v", replay)
	}
	historyArgs, _ := json.Marshal(map[string]any{"proposalId": enrichment.Proposal.ProposalID, "evidencePackId": enrichment.Pack.EvidencePackID})
	historyBody := requireMCPApprovalHistorySuccess(t, serveMCPApprovalHistoryRequest(t, restarted, mcpApprovalHistoryToolCall("get_semantic_approval_history", historyArgs)))
	var history semantic.ApprovalHistoryResult
	if err := json.Unmarshal([]byte(historyBody), &history); err != nil {
		t.Fatal(err)
	}
	if len(history.Events) != 1 || history.Aggregate.Version != 1 || !reflect.DeepEqual(history.Events[0], first.Receipt.Event) {
		t.Fatalf("restart history duplicated or changed approval: %+v", history)
	}
	rflscvs09evidence.Observe(t, "codeflow/internal/mcp", []string{"VS09-A5", "VS09-A7"}, map[string]any{"before.sse": head, "after.sse": event, "before.receipt": first, "after.receipt": replayed, "after.history": history, "retry": retry})
}

func approvalMCPStream(t *testing.T, rawURL, lastID string) (*bufio.Scanner, context.CancelFunc) {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/api/workspace/stream"
	query := u.Query()
	query.Set("lastEventId", lastID)
	u.RawQuery = query.Encode()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		cancel()
		t.Fatalf("stream status=%d", response.StatusCode)
	}
	return bufio.NewScanner(response.Body), func() { response.Body.Close(); cancel() }
}

func nextApprovalMCPEnvelope(t *testing.T, scanner *bufio.Scanner) semantic.EventEnvelope {
	t.Helper()
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var event semantic.EventEnvelope
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event); err != nil {
			t.Fatal(err)
		}
		return event
	}
	t.Fatalf("SSE event missing: %v", scanner.Err())
	return semantic.EventEnvelope{}
}
