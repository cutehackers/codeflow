package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"codeflow/internal/rflscvs09evidence"
	"codeflow/internal/semantic"
)

// ProposalStore is the approved replaceable boundary. Execute reads its
// transaction snapshot before this lookup. Holding all real loads here
// establishes concurrent pre-winner snapshots without changing authority.
type approvalSnapshotBarrierStore struct {
	semantic.ProposalStore
	armed   atomic.Bool
	entered chan struct{}
	release chan struct{}
}

func (s *approvalSnapshotBarrierStore) Load(ctx context.Context, workspace, proposal, pack string) (*semantic.StoredProposal, error) {
	value, err := s.ProposalStore.Load(ctx, workspace, proposal, pack)
	if err == nil && s.armed.Load() {
		select {
		case s.entered <- struct{}{}:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		select {
		case <-s.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return value, err
}

type publicApprovalConflictReply struct {
	body   []byte
	failed bool
	status int
	err    error
	args   map[string]any
}

func TestApprovalPublicConflictReportsWinnerVersionAndPublishesOnce(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") == "mcp" {
		t.Skip("helper process")
	}
	for _, transport := range []string{"REST", "MCP"} {
		for _, mode := range []string{"observed-winner", "commit-race"} {
			t.Run(transport+"/"+mode, func(t *testing.T) {
				fixture := newMCPEnrichmentFactoryFixture(t)
				defer fixture.server.Close()
				barrier := &approvalSnapshotBarrierStore{ProposalStore: fixture.server.proposalStore, entered: make(chan struct{}, 4), release: make(chan struct{})}
				// The configured store is bound once before the coordinator/service
				// exists, and is shared by real enrichment and approval.
				fixture.server.proposalStore = barrier
				enrichArgs, _ := json.Marshal(map[string]any{"target": fixture.root, "generationId": fixture.generation, "targetStepId": fixture.stepID, "promptRevision": "public-conflict"})
				var enrichment semantic.EnrichmentResult
				text := requireMCPApprovalHistorySuccess(t, serveMCPApprovalHistoryRequest(t, fixture.server, mcpApprovalHistoryToolCall("request_semantic_enrichment", enrichArgs)))
				if err := json.Unmarshal([]byte(text), &enrichment); err != nil {
					t.Fatal(err)
				}
				coord, err := fixture.server.getLiveCoordinator(fixture.root)
				if err != nil {
					t.Fatal(err)
				}
				st, err := fixture.server.getStorage(fixture.root)
				if err != nil {
					t.Fatal(err)
				}
				proofBefore, err := st.ReadValidatedActiveProofBundle()
				if err != nil {
					t.Fatal(err)
				}
				storedBefore, err := barrier.ProposalStore.Load(context.Background(), fixture.server.approvalWorkspaceID, enrichment.Proposal.ProposalID, enrichment.Pack.EvidencePackID)
				if err != nil {
					t.Fatal(err)
				}
				stream, cancelStream := approvalMCPStream(t, coord.URL(), "public-conflict-head")
				defer cancelStream()
				head := nextApprovalMCPEnvelope(t, stream)
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				endpoint := func(path string) string { u, _ := url.Parse(coord.URL()); u.Path = path; return u.String() }
				submit := func(args map[string]any) publicApprovalConflictReply {
					out := publicApprovalConflictReply{args: args}
					if transport == "REST" {
						draft := make(map[string]any, len(args))
						for key, value := range args {
							if key != "target" {
								draft[key] = value
							}
						}
						raw, _ := json.Marshal(draft)
						req, _ := http.NewRequestWithContext(ctx, http.MethodPost, endpoint("/api/semantic/approve"), bytes.NewReader(raw))
						req.Header.Set("Content-Type", "application/json")
						response, err := http.DefaultClient.Do(req)
						if err != nil {
							out.err = err
							return out
						}
						defer response.Body.Close()
						out.body, out.err = io.ReadAll(response.Body)
						out.status = response.StatusCode
						out.failed = response.StatusCode != http.StatusOK
					} else {
						raw, _ := json.Marshal(args)
						response, err := serveMCPApprovalRequest(ctx, fixture.server, raw)
						if err != nil {
							out.err = err
							return out
						}
						if response.Error != nil || len(response.Result.Content) != 1 || response.Result.Content[0].Type != "text" {
							out.err = fmt.Errorf("invalid RPC response: %+v", response)
							return out
						}
						out.body = []byte(response.Result.Content[0].Text)
						out.failed = response.Result.IsError
					}
					return out
				}
				var replies []publicApprovalConflictReply
				transactionPath := filepath.Join(fixture.root, ".codeflow", "approval-transactions", "approval-transactions.sqlite3")
				ledgerPath := filepath.Join(st.BaseDir(), "semantics", "events", "event-ledger.jsonl")
				readArtifact := func(path string) []byte {
					t.Helper()
					data, err := os.ReadFile(path)
					if err != nil {
						t.Fatal(err)
					}
					return data
				}
				if mode == "observed-winner" {
					for i := 0; i < 4; i++ {
						var transactionBefore, ledgerBefore []byte
						if i > 0 {
							transactionBefore, ledgerBefore = readArtifact(transactionPath), readArtifact(ledgerPath)
						}
						replies = append(replies, submit(mcpApprovalRequestArgs(fixture.root, enrichment, fmt.Sprintf("observed-%d", i))))
						if i > 0 && (!bytes.Equal(transactionBefore, readArtifact(transactionPath)) || !bytes.Equal(ledgerBefore, readArtifact(ledgerPath))) {
							t.Fatal("observed-winner loser mutated durable transaction or event ledger")
						}
					}
				} else {
					barrier.armed.Store(true)
					completed := make(chan publicApprovalConflictReply, 4)
					for i := 0; i < 4; i++ {
						args := mcpApprovalRequestArgs(fixture.root, enrichment, fmt.Sprintf("racing-%d", i))
						go func() { completed <- submit(args) }()
					}
					for i := 0; i < 4; i++ {
						select {
						case <-barrier.entered:
						case <-ctx.Done():
							t.Fatal("concurrent callers did not reach the real proposal boundary")
						}
					}
					close(barrier.release)
					for i := 0; i < 4; i++ {
						replies = append(replies, <-completed)
					}
					barrier.armed.Store(false)
				}
				var winner publicApprovalConflictReply
				var committed semantic.ApprovalExecutionResult
				wins := 0
				for _, reply := range replies {
					if reply.err != nil {
						t.Fatal(reply.err)
					}
					if !reply.failed {
						wins++
						winner = reply
						if err := json.Unmarshal(reply.body, &committed); err != nil {
							t.Fatal(err)
						}
						continue
					}
					if transport == "REST" && reply.status != http.StatusConflict {
						t.Fatalf("loser status=%d body=%s", reply.status, reply.body)
					}
					var payload map[string]any
					if err := json.Unmarshal(reply.body, &payload); err != nil {
						t.Fatal(err)
					}
					message := "approval request conflicts with current state"
					if transport == "REST" {
						message = "approval command could not be executed"
					}
					want := map[string]any{"code": "approval_conflict", "message": message, "currentVersion": float64(1)}
					if !reflect.DeepEqual(payload, want) {
						t.Fatalf("loser must expose only safe committed version: got %s", reply.body)
					}
				}
				if wins != 1 || committed.Receipt.Aggregate.Version != 1 || committed.Receipt.Outbox.DeliveryState != "published" {
					t.Fatalf("winners=%d result=%+v", wins, committed)
				}
				// Supplemental storage evidence ensures losing calls left no
				// extra idempotency/outbox records hidden behind public history.
				rows, err := semantic.NewApprovalTransactionStore(fixture.root, coord.SnapshotEngine()).Snapshot(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if len(rows.Events) != 1 || len(rows.Aggregates) != 1 || len(rows.IdempotencyResults) != 1 || len(rows.Outbox) != 1 {
					t.Fatalf("losers left extra durable records: %+v", rows)
				}
				event := nextApprovalMCPEnvelope(t, stream)
				if event.EventType != "approval.updated" || event.Sequence != head.Sequence+1 {
					t.Fatalf("approval broadcast=%+v head=%+v", event, head)
				}
				encoded, _ := json.Marshal(event.Data)
				var delivery struct {
					ApprovalEvent semantic.ApprovalOutboxCommittedEventV1 `json:"approvalEvent"`
				}
				if err := json.Unmarshal(encoded, &delivery); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(delivery.ApprovalEvent, committed.Receipt.Outbox.CommittedEvent) {
					t.Fatal("public SSE does not describe the winner")
				}
				historyArgs, _ := json.Marshal(map[string]any{"proposalId": enrichment.Proposal.ProposalID, "evidencePackId": enrichment.Pack.EvidencePackID})
				readHistory := func() string {
					t.Helper()
					if transport == "MCP" {
						return requireMCPApprovalHistorySuccess(t, serveMCPApprovalHistoryRequest(t, fixture.server, mcpApprovalHistoryToolCall("get_semantic_approval_history", historyArgs)))
					}
					u, _ := url.Parse(endpoint("/api/semantic/approval-history"))
					query := u.Query()
					query.Set("proposalId", enrichment.Proposal.ProposalID)
					query.Set("evidencePackId", enrichment.Pack.EvidencePackID)
					u.RawQuery = query.Encode()
					req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
					response, err := http.DefaultClient.Do(req)
					if err != nil {
						t.Fatal(err)
					}
					defer response.Body.Close()
					body, err := io.ReadAll(response.Body)
					if err != nil {
						t.Fatal(err)
					}
					if response.StatusCode != http.StatusOK {
						t.Fatalf("history status=%d body=%s", response.StatusCode, body)
					}
					return string(body)
				}
				historyText := readHistory()
				var history semantic.ApprovalHistoryResult
				if err := json.Unmarshal([]byte(historyText), &history); err != nil {
					t.Fatal(err)
				}
				if history.Aggregate.Version != 1 || len(history.Events) != 1 || !reflect.DeepEqual(history.Events[0], committed.Receipt.Event) || history.Freshness != "current" {
					t.Fatalf("history=%+v", history)
				}
				transactionBefore, err := os.ReadFile(transactionPath)
				if err != nil {
					t.Fatal(err)
				}
				// Changed payload with the winning key has no authenticated
				// aggregate-conflict metadata and must retain the bare error.
				changed := make(map[string]any, len(winner.args))
				for key, value := range winner.args {
					changed[key] = value
				}
				changed["decision"] = "reject"
				bare := submit(changed)
				if bare.err != nil {
					t.Fatal(bare.err)
				}
				var barePayload map[string]any
				if err := json.Unmarshal(bare.body, &barePayload); err != nil {
					t.Fatal(err)
				}
				if !bare.failed || barePayload["code"] != "approval_conflict" || len(barePayload) != 2 {
					t.Fatalf("same-key payload mismatch exposed aggregate state: %s", bare.body)
				}
				stale := mcpApprovalRequestArgs(fixture.root, enrichment, "wrong-proof-basis")
				stale["computedBasisId"] = "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
				staleReply := submit(stale)
				if staleReply.err != nil {
					t.Fatal(staleReply.err)
				}
				var stalePayload map[string]any
				if err := json.Unmarshal(staleReply.body, &stalePayload); err != nil {
					t.Fatal(err)
				}
				if !staleReply.failed || stalePayload["code"] != "approval_conflict" || len(stalePayload) != 2 {
					t.Fatalf("proof mismatch exposed aggregate state: %s", staleReply.body)
				}
				transactionAfter, err := os.ReadFile(transactionPath)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(transactionBefore, transactionAfter) {
					t.Fatal("loser changed transaction bytes")
				}
				finalHistory := readHistory()
				if finalHistory != historyText {
					t.Fatal("loser changed public history")
				}
				finalStream, closeFinal := approvalMCPStream(t, coord.URL(), "check-public-conflict-head")
				defer closeFinal()
				finalHead := nextApprovalMCPEnvelope(t, finalStream)
				if finalHead.Sequence != event.Sequence || finalHead.EventID != event.EventID {
					t.Fatalf("loser broadcast extra event: %+v", finalHead)
				}
				proofAfter, err := st.ReadValidatedActiveProofBundle()
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(proofBefore, proofAfter) {
					t.Fatal("approval conflict changed proof, Fact, Evidence, alignment, freshness or settlement")
				}
				observedReplies := make([]json.RawMessage, 0, len(replies))
				for _, reply := range replies {
					observedReplies = append(observedReplies, json.RawMessage(reply.body))
				}
				rflscvs09evidence.Observe(t, "codeflow/internal/mcp", []string{"VS09-A11"}, map[string]any{"before.transactions": transactionBefore, "after.transactions": transactionAfter, "before.proof": proofBefore, "after.proof": proofAfter, "before.sse": head, "after.sse": event, "final.sse": finalHead, "history": history, "responses": observedReplies})
				storedAfter, err := barrier.ProposalStore.Load(context.Background(), fixture.server.approvalWorkspaceID, enrichment.Proposal.ProposalID, enrichment.Pack.EvidencePackID)
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(storedBefore, storedAfter) {
					t.Fatal("approval changed stored proposal or evidence pack")
				}
			})
		}
	}
}
