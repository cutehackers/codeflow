package mcp

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"codeflow/internal/evidence"
	"codeflow/internal/semantic"
)

func TestMCPApprovalPublicAuthAndMissingPairCreateNoRecords(t *testing.T) {
	for _, tc := range []struct{ name, code, criterion string }{
		{"unauthenticated", "approval_unauthenticated", "VS09-A1"},
		{"unauthorized", "approval_unauthorized", "VS09-A1"},
		{"missing-proposal", "approval_unavailable", "VS09-A2"},
		{"missing-pack", "approval_unavailable", "VS09-A2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture, enrichment := newMCPApprovalEnrichmentFixture(t)
			defer fixture.server.Close()
			coord, err := fixture.server.getLiveCoordinator(fixture.root)
			if err != nil {
				t.Fatal(err)
			}
			stream, cancel := approvalMCPStream(t, coord.URL(), "auth-head")
			head := nextApprovalMCPEnvelope(t, stream)
			cancel()
			args := mcpApprovalRequestArgs(fixture.root, enrichment, tc.name)
			switch tc.name {
			case "unauthenticated":
				fixture.server.approvalGate = semantic.NewApprovalAccessGate(nil, semantic.NewApprovalWorkspaceAuthorizer(fixture.root))
			case "unauthorized":
				args["target"] = t.TempDir()
			case "missing-proposal":
				args["proposalId"] = "missing-proposal"
			case "missing-pack":
				args["evidencePackId"] = "missing-pack"
			}
			before, err := snapshotOptionalApprovalHistoryTree(filepath.Join(fixture.root, ".codeflow", "approval-transactions"))
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(args)
			response, err := serveMCPApprovalRequest(context.Background(), fixture.server, raw)
			if err != nil {
				t.Fatal(err)
			}
			if response.Error != nil || !response.Result.IsError || len(response.Result.Content) != 1 {
				t.Fatalf("request accepted: %+v", response)
			}
			var problem map[string]any
			if err := json.Unmarshal([]byte(response.Result.Content[0].Text), &problem); err != nil {
				t.Fatal(err)
			}
			if problem["code"] != tc.code || len(problem) != 2 {
				t.Fatalf("error=%+v", problem)
			}
			after, err := snapshotOptionalApprovalHistoryTree(filepath.Join(fixture.root, ".codeflow", "approval-transactions"))
			if err != nil {
				t.Fatal(err)
			}
			if !approvalHistoryOptionalTreeEqual(before, after) {
				t.Fatal("auth/missing pair mutated managed transactions")
			}
			stream, cancel = approvalMCPStream(t, coord.URL(), "auth-head")
			finalHead := nextApprovalMCPEnvelope(t, stream)
			cancel()
			if head.EventID != finalHead.EventID || head.Sequence != finalHead.Sequence {
				t.Fatal("auth/missing pair broadcast event")
			}
			evidence.ObserveApproval(t, "codeflow/internal/mcp", []string{tc.criterion}, map[string]any{"before.transactions": map[string]any{"present": before.present, "entries": before.entries}, "after.transactions": map[string]any{"present": after.present, "entries": after.entries}, "before.sse": head, "after.sse": finalHead, "request": raw, "response": response})
		})
	}
}
