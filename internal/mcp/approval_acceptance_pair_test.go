package mcp

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"codeflow/internal/rflscvs09evidence"
	"codeflow/internal/semantic"
)

// This decorator corrupts only the approved ProposalStore response boundary.
// The producer and unchanged control use the real persisted model-host pair.
type approvalCorruptiblePairStore struct {
	semantic.ProposalStore
	corrupt func(*semantic.StoredProposal)
}

func (s *approvalCorruptiblePairStore) Load(ctx context.Context, workspace, proposal, pack string) (*semantic.StoredProposal, error) {
	value, err := s.ProposalStore.Load(ctx, workspace, proposal, pack)
	if err == nil && s.corrupt != nil {
		s.corrupt(value)
	}
	return value, err
}

func TestMCPApprovalRejectsEachCorruptStoredPairWithoutMutation(t *testing.T) {
	fixture := newMCPEnrichmentFactoryFixture(t)
	defer fixture.server.Close()
	store := &approvalCorruptiblePairStore{ProposalStore: fixture.server.proposalStore}
	fixture.server.proposalStore = store
	args, _ := json.Marshal(map[string]any{"target": fixture.root, "generationId": fixture.generation, "targetStepId": fixture.stepID, "promptRevision": "acceptance-pair"})
	body := requireMCPApprovalHistorySuccess(t, serveMCPApprovalHistoryRequest(t, fixture.server, mcpApprovalHistoryToolCall("request_semantic_enrichment", args)))
	var enrichment semantic.EnrichmentResult
	if err := json.Unmarshal([]byte(body), &enrichment); err != nil {
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
	proof, err := st.ReadValidatedActiveProofBundle()
	if err != nil {
		t.Fatal(err)
	}
	transactions := semantic.NewApprovalTransactionStore(fixture.root, coord.SnapshotEngine())
	before, err := transactions.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	stream, closeStream := approvalMCPStream(t, coord.URL(), "pair-corruption-head")
	head := nextApprovalMCPEnvelope(t, stream)
	closeStream()
	for _, tc := range []struct {
		name    string
		corrupt func(*semantic.StoredProposal)
	}{
		{"evidence-verification", func(p *semantic.StoredProposal) { p.Pack.Items[0].Verified = false }},
		{"evidence-ref", func(p *semantic.StoredProposal) { p.Proposal.EvidenceRefs[0] = "missing-evidence" }},
		{"redaction", func(p *semantic.StoredProposal) { p.Pack.RedactionStatus = "unredacted" }},
		{"target", func(p *semantic.StoredProposal) { p.Proposal.TargetStepID = "different-target" }},
		{"basis", func(p *semantic.StoredProposal) { p.Pack.ComputedBasisID = "different-basis" }},
		{"generation", func(p *semantic.StoredProposal) { p.Proposal.GenerationID = "different-generation" }},
		{"snapshot", func(p *semantic.StoredProposal) { p.Pack.Items[0].SnapshotID = "different-snapshot" }},
		{"content-digest", func(p *semantic.StoredProposal) { p.Pack.Items[0].ContentDigest = "invalid-digest" }},
		{"workspace", func(p *semantic.StoredProposal) { p.WorkspaceID = "different-workspace" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store.corrupt = tc.corrupt
			defer func() { store.corrupt = nil }()
			raw, _ := json.Marshal(mcpApprovalRequestArgs(fixture.root, enrichment, tc.name))
			response, err := serveMCPApprovalRequest(context.Background(), fixture.server, raw)
			if err != nil {
				t.Fatal(err)
			}
			if response.Error != nil || !response.Result.IsError || len(response.Result.Content) != 1 {
				t.Fatalf("corrupt pair accepted: %+v", response)
			}
			var problem map[string]any
			if err := json.Unmarshal([]byte(response.Result.Content[0].Text), &problem); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(problem, map[string]any{"code": "approval_invalid", "message": "approval request is invalid"}) {
				t.Fatalf("unbounded corruption error: %s", response.Result.Content[0].Text)
			}
			after, err := transactions.Snapshot(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, after) {
				t.Fatal("corrupt pair mutated approval records")
			}
			rflscvs09evidence.Observe(t, "codeflow/internal/mcp", []string{"VS09-A3"}, map[string]any{"before.transactions": before, "after.transactions": after, "request": raw, "response": response, "before.proof": proof})
		})
	}
	// Intent revision belongs to the command/current Q3 identity, not the pair DTO.
	for _, field := range []string{"intentRevision", "generationId"} {
		t.Run("stale-"+field, func(t *testing.T) {
			arguments := mcpApprovalRequestArgs(fixture.root, enrichment, "stale-"+field)
			if field == "intentRevision" {
				arguments[field] = int64(2)
			} else {
				arguments[field] = "different-generation"
			}
			raw, _ := json.Marshal(arguments)
			response, err := serveMCPApprovalRequest(context.Background(), fixture.server, raw)
			if err != nil {
				t.Fatal(err)
			}
			var problem map[string]any
			if len(response.Result.Content) != 1 {
				t.Fatalf("response=%+v", response)
			}
			if err := json.Unmarshal([]byte(response.Result.Content[0].Text), &problem); err != nil {
				t.Fatal(err)
			}
			if !response.Result.IsError || !reflect.DeepEqual(problem, map[string]any{"code": "approval_conflict", "message": "approval request conflicts with current state"}) {
				t.Fatalf("stale identity=%+v", response)
			}
			rflscvs09evidence.Observe(t, "codeflow/internal/mcp", []string{"VS09-A4"}, map[string]any{"before.transactions": before, "request": raw, "response": response})
		})
	}
	after, err := transactions.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	proofAfter, err := st.ReadValidatedActiveProofBundle()
	if err != nil {
		t.Fatal(err)
	}
	stream, closeStream = approvalMCPStream(t, coord.URL(), "pair-corruption-head")
	finalHead := nextApprovalMCPEnvelope(t, stream)
	closeStream()
	if !reflect.DeepEqual(before, after) || !reflect.DeepEqual(proof, proofAfter) || finalHead.EventID != head.EventID || finalHead.Sequence != head.Sequence {
		t.Fatal("rejected pair/intent changed durable state, Q3 identity, or stream head")
	}
	// Removing only the store corruption must admit the same genuine pair.
	controlStream, closeControl := approvalMCPStream(t, coord.URL(), "pair-control-head")
	defer closeControl()
	_ = nextApprovalMCPEnvelope(t, controlStream)
	raw, _ := json.Marshal(mcpApprovalRequestArgs(fixture.root, enrichment, "valid-pair-control"))
	response, err := serveMCPApprovalRequest(context.Background(), fixture.server, raw)
	if err != nil {
		t.Fatal(err)
	}
	if response.Error != nil || response.Result.IsError {
		t.Fatalf("valid stored pair control failed: %+v", response)
	}
	controlEvent := nextApprovalMCPEnvelope(t, controlStream)
	if controlEvent.EventType != "approval.updated" || controlEvent.Sequence != head.Sequence+1 {
		t.Fatalf("valid control SSE=%+v", controlEvent)
	}
	rflscvs09evidence.Observe(t, "codeflow/internal/mcp", []string{"VS09-A3", "VS09-A4"}, map[string]any{"before.transactions": before, "after.transactions": after, "before.proof": proof, "after.proof": proofAfter, "before.sse": head, "after.sse": finalHead, "control.response": response, "control.sse": controlEvent})
}
