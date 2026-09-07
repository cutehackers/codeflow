package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"codeflow/internal/semantic"
	"codeflow/internal/workspace"
)

// VS09-A7/A9: even a committed, undelivered approval must be readable without
// starting recovery, creating workspace artifacts, or publishing events.
func TestMCPApprovalColdHistoryLeavesPendingCommitAndWorkspaceUntouched(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") == "mcp" {
		t.Skip("helper process")
	}
	fixture, enrichment := newMCPApprovalEnrichmentFixture(t)
	engine, err := fixture.server.getSnapshotEngine(fixture.root)
	if err != nil {
		t.Fatal(err)
	}
	access, err := fixture.server.authorizeSemanticApproval(context.Background(), fixture.root)
	if err != nil {
		t.Fatal(err)
	}
	service, err := semantic.NewApprovalExecutionService(fixture.root, engine, semantic.NewDurableProposalStore(fixture.root))
	if err != nil {
		t.Fatal(err)
	}
	draft, err := approvalCommandDraftFromArgs(mcpApprovalRequestArgs(fixture.root, enrichment, "cold-pending"))
	if err != nil {
		t.Fatal(err)
	}
	committed, err := service.Execute(context.Background(), access, draft)
	if err != nil {
		t.Fatal(err)
	}
	if committed.Receipt.Outbox.DeliveryState != "pending" {
		t.Fatalf("commit state = %q", committed.Receipt.Outbox.DeliveryState)
	}
	if err := fixture.server.Close(); err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(Config{RepoRoot: fixture.root})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	args, _ := json.Marshal(map[string]any{"target": fixture.root, "proposalId": enrichment.Proposal.ProposalID, "evidencePackId": enrichment.Pack.EvidencePackID})
	query := func(wantFreshness string) {
		t.Helper()
		before, err := snapshotApprovalHistoryTree(filepath.Join(fixture.root, ".codeflow"))
		if err != nil {
			t.Fatal(err)
		}
		body := requireMCPApprovalHistorySuccess(t, serveMCPApprovalHistoryRequest(t, server, mcpApprovalHistoryToolCall("get_semantic_approval_history", args)))
		var history semantic.ApprovalHistoryResult
		if err := json.Unmarshal([]byte(body), &history); err != nil {
			t.Fatal(err)
		}
		if history.Freshness != wantFreshness || len(history.Events) != 1 || !reflect.DeepEqual(history.Events[0], committed.Receipt.Event) || !reflect.DeepEqual(history.Aggregate, committed.Receipt.Aggregate) {
			t.Fatalf("history = %+v", history)
		}
		after, err := snapshotApprovalHistoryTree(filepath.Join(fixture.root, ".codeflow"))
		if err != nil {
			t.Fatal(err)
		}
		if !approvalHistoryTreeEqual(before, after) {
			t.Fatal("cold history changed workspace, transaction, or event artifacts")
		}
	}
	query("current")
	statePath := filepath.Join(fixture.root, ".codeflow", "workspace", "state.json")
	stateBytes, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"schemaId", "schemaVersion", "liveHeadSnapshotId"} {
		var state map[string]any
		if err := json.Unmarshal(stateBytes, &state); err != nil {
			t.Fatal(err)
		}
		if field == "schemaVersion" {
			state[field] = 999
		} else {
			state[field] = "unrecognized-state"
		}
		invalid, err := json.Marshal(state)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(statePath, invalid, 0o600); err != nil {
			t.Fatal(err)
		}
		query("historical")
	}
	if err := os.WriteFile(statePath, stateBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	peer, err := workspace.NewSnapshotEngine(fixture.root, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := peer.ApplyVersionedEdit(context.Background(), workspace.EditRequest{Path: "main.go", Content: []byte("package main\nfunc Changed() {}\n"), DocumentVersion: 2, Source: workspace.SourceIDEVersioned}); err != nil {
		t.Fatal(err)
	}
	query("historical")
	headPath := filepath.Join(fixture.root, ".codeflow", "workspace", "live-head.json")
	if err := os.WriteFile(headPath, []byte(`{"corrupt":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	query("historical")
	if err := os.Remove(headPath); err != nil {
		t.Fatal(err)
	}
	query("historical")
}

func TestMCPApprovalColdHistoryAbsentCreatesNoArtifacts(t *testing.T) {
	root := t.TempDir()
	server, err := NewServer(Config{RepoRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	before, err := snapshotApprovalHistoryTree(root)
	if err != nil {
		t.Fatal(err)
	}
	raw := serveMCPApprovalHistoryRequest(t, server, mcpApprovalHistoryToolCall("get_semantic_approval_history", []byte(`{"proposalId":"missing-proposal","evidencePackId":"missing-pack"}`)))
	requireMCPApprovalHistoryError(t, raw, "approval_unavailable")
	after, err := snapshotApprovalHistoryTree(root)
	if err != nil {
		t.Fatal(err)
	}
	if !approvalHistoryTreeEqual(before, after) {
		t.Fatal("absent cold history created artifacts")
	}
}
