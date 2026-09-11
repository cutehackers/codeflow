package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"codeflow/internal/evidence"
	"codeflow/internal/semantic"
)

func TestMCPApprovalAllLifecycleCommandsPreserveCanonicalProofAndGuards(t *testing.T) {
	for _, terminal := range []string{"reject", "revoke", "supersede"} {
		t.Run(terminal, func(t *testing.T) {
			fixture, enrichment := newMCPApprovalEnrichmentFixture(t)
			defer fixture.server.Close()
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
			stream, cancel := approvalMCPStream(t, coord.URL(), "lifecycle-head")
			defer cancel()
			head := nextApprovalMCPEnvelope(t, stream)
			cancel()
			expectedPrior := head
			var events []semantic.ApprovalEventV2
			var envelopes []semantic.EventEnvelope
			state := "none"
			predecessor := ""
			history := func() []byte {
				t.Helper()
				raw, _ := json.Marshal(map[string]any{"proposalId": enrichment.Proposal.ProposalID, "evidencePackId": enrichment.Pack.EvidencePackID})
				response := serveMCPApprovalHistoryRequest(t, fixture.server, mcpApprovalHistoryToolCall("get_semantic_approval_history", raw))
				data, err := json.Marshal(response)
				if err != nil {
					t.Fatal(err)
				}
				return data
			}
			for index, decision := range []string{"approve", "edit_then_approve", terminal} {
				args := mcpApprovalRequestArgs(fixture.root, enrichment, fmt.Sprintf("lifecycle-%s-%d", terminal, index))
				args["decision"] = decision
				args["expectedApprovalVersion"] = int64(index)
				args["expectedState"] = state
				if index > 0 {
					args["predecessorApprovalId"] = predecessor
				}
				if decision == "edit_then_approve" {
					args["editedText"] = "Explicit human edit"
				}
				beforeHistory := history()
				for _, mutation := range []struct {
					name   string
					change func(map[string]any)
				}{
					{"missing-version", func(a map[string]any) { delete(a, "expectedApprovalVersion") }},
					{"missing-state", func(a map[string]any) { delete(a, "expectedState") }},
					{"wrong-version", func(a map[string]any) { a["expectedApprovalVersion"] = int64(index + 7) }},
					{"wrong-state", func(a map[string]any) { a["expectedState"] = "revoked" }},
					{"wrong-predecessor", func(a map[string]any) { a["predecessorApprovalId"] = "wrong-predecessor" }},
				} {
					bad := make(map[string]any, len(args))
					for key, value := range args {
						bad[key] = value
					}
					mutation.change(bad)
					raw, _ := json.Marshal(bad)
					response, err := serveMCPApprovalRequest(context.Background(), fixture.server, raw)
					if err != nil {
						t.Fatal(err)
					}
					if response.Error != nil || !response.Result.IsError || len(response.Result.Content) != 1 {
						t.Fatalf("%s/%s accepted: %+v", decision, mutation.name, response)
					}
					if !reflect.DeepEqual(beforeHistory, history()) {
						t.Fatalf("%s/%s changed public history", decision, mutation.name)
					}
					evidence.ObserveApproval(t, "codeflow/internal/mcp", []string{"VS09-A10"}, map[string]any{"before.history": beforeHistory, "after.history": history(), "request": raw, "response": response})
				}
				if decision == "edit_then_approve" || decision == "revoke" || decision == "supersede" {
					missing := make(map[string]any, len(args))
					for key, value := range args {
						missing[key] = value
					}
					delete(missing, "predecessorApprovalId")
					raw, _ := json.Marshal(missing)
					response, err := serveMCPApprovalRequest(context.Background(), fixture.server, raw)
					if err != nil {
						t.Fatal(err)
					}
					if !response.Result.IsError || !reflect.DeepEqual(beforeHistory, history()) {
						t.Fatalf("%s missing predecessor accepted or mutated", decision)
					}
				}
				// The shared stream helper is deliberately bounded to five
				// seconds. Subscribe after the slower guard/history matrix, and
				// require the exact previous public cursor before submitting.
				stream, cancel = approvalMCPStream(t, coord.URL(), "lifecycle-command-head")
				defer cancel()
				prior := nextApprovalMCPEnvelope(t, stream)
				if prior.EventType != "snapshot_sync" || prior.EventID != expectedPrior.EventID || prior.Sequence != expectedPrior.Sequence {
					t.Fatalf("invalid/replayed command changed prior public cursor: got=%+v want=%+v", prior, expectedPrior)
				}
				raw, _ := json.Marshal(args)
				response, err := serveMCPApprovalRequest(context.Background(), fixture.server, raw)
				if err != nil {
					t.Fatal(err)
				}
				if response.Error != nil || response.Result.IsError || len(response.Result.Content) != 1 {
					t.Fatalf("%s failed: %+v", decision, response)
				}
				var result semantic.ApprovalExecutionResult
				if err := json.Unmarshal([]byte(response.Result.Content[0].Text), &result); err != nil {
					t.Fatal(err)
				}
				event := result.Receipt.Event
				wantState := "active"
				if decision == terminal {
					wantState = map[string]string{"reject": "rejected", "revoke": "revoked", "supersede": "superseded"}[terminal]
				}
				if event.Decision != decision || event.AggregateVersion != int64(index+1) || event.ActorID == "" || event.SessionID == "" || event.PredecessorApprovalID != predecessor || result.Receipt.Aggregate.State != wantState || result.Receipt.Replayed {
					t.Fatalf("incorrect attributed lifecycle: %+v", result)
				}
				if index > 0 && (event.ActorID != events[0].ActorID || event.SessionID != events[0].SessionID || event.EventID == events[index-1].EventID) {
					t.Fatal("event attribution or append identity changed")
				}
				if decision == "edit_then_approve" && event.ApprovedText != "Explicit human edit" {
					t.Fatal("edit text not retained")
				}
				broadcast := nextApprovalMCPEnvelope(t, stream)
				cancel()
				if broadcast.EventType != "approval.updated" || broadcast.Sequence != head.Sequence+index+1 {
					t.Fatalf("lifecycle broadcast=%+v", broadcast)
				}
				var delivery struct {
					ApprovalEvent semantic.ApprovalOutboxCommittedEventV1 `json:"approvalEvent"`
				}
				encoded, _ := json.Marshal(broadcast.Data)
				if err := json.Unmarshal(encoded, &delivery); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(delivery.ApprovalEvent, result.Receipt.Outbox.CommittedEvent) {
					t.Fatal("lifecycle SSE did not bind exact committed command/outbox")
				}
				expectedPrior = broadcast
				events = append(events, event)
				envelopes = append(envelopes, broadcast)
				state = wantState
				predecessor = result.Receipt.Aggregate.ActiveApprovalID
				retry, err := serveMCPApprovalRequest(context.Background(), fixture.server, raw)
				if err != nil {
					t.Fatal(err)
				}
				var replay semantic.ApprovalExecutionResult
				if len(retry.Result.Content) != 1 || json.Unmarshal([]byte(retry.Result.Content[0].Text), &replay) != nil || !replay.Receipt.Replayed || !reflect.DeepEqual(replay.Receipt.Event, event) || !reflect.DeepEqual(replay.Receipt.Outbox, result.Receipt.Outbox) {
					t.Fatalf("replay changed committed result: %+v", retry)
				}
				proofAfter, err := st.ReadValidatedActiveProofBundle()
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(proofBefore, proofAfter) {
					t.Fatalf("%s changed active manifest/map/analyzer/delta/CAS identity including Fact/Evidence/alignment/freshness/settlement", decision)
				}
				evidence.ObserveApproval(t, "codeflow/internal/mcp", []string{"VS09-A6", "VS09-A8", "VS09-A10"}, map[string]any{"before.history": beforeHistory, "after.history": history(), "before.proof": proofBefore, "after.proof": proofAfter, "response": response, "replay": retry, "sse": broadcast})
			}
			raw, _ := json.Marshal(map[string]any{"proposalId": enrichment.Proposal.ProposalID, "evidencePackId": enrichment.Pack.EvidencePackID})
			text := requireMCPApprovalHistorySuccess(t, serveMCPApprovalHistoryRequest(t, fixture.server, mcpApprovalHistoryToolCall("get_semantic_approval_history", raw)))
			var final semantic.ApprovalHistoryResult
			if err := json.Unmarshal([]byte(text), &final); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(final.Events, events) || final.Aggregate.Version != 3 || final.Aggregate.State != state || final.Freshness != "current" {
				t.Fatalf("canonical lifecycle history=%+v", final)
			}
			check, closeCheck := approvalMCPStream(t, coord.URL(), "lifecycle-final-head")
			actualHead := nextApprovalMCPEnvelope(t, check)
			closeCheck()
			if actualHead.EventID != envelopes[2].EventID || actualHead.Sequence != envelopes[2].Sequence {
				t.Fatal("rejected/replayed command broadcast an extra event")
			}
		})
	}
}
