package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"codeflow/internal/semantic"
)

// VS09-A7 and inherited path containment: a repository-root alias is valid,
// but managed descendants must never redirect the authenticated history read.
func TestMCPApprovalColdHistoryRejectsManagedSymlinksAndAllowsRootAlias(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") == "mcp" {
		t.Skip("helper process")
	}
	for _, target := range []string{"managed parent", "transaction directory", "repository alias"} {
		t.Run(target, func(t *testing.T) {
			fixture, enrichment := newMCPApprovalHistoryDurableFixture(t)
			if err := fixture.server.Close(); err != nil {
				t.Fatal(err)
			}
			requestRoot := fixture.root
			external := filepath.Join(t.TempDir(), "external-artifacts")
			if target == "repository alias" {
				requestRoot = filepath.Join(t.TempDir(), "repo-alias")
				if err := os.Symlink(fixture.root, requestRoot); err != nil {
					t.Fatal(err)
				}
				external = fixture.root
			} else {
				managed := filepath.Join(fixture.root, ".codeflow")
				if target == "transaction directory" {
					managed = filepath.Join(managed, "approval-transactions")
				}
				if err := os.Rename(managed, external); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(external, managed); err != nil {
					t.Fatal(err)
				}
			}
			server, err := NewServer(Config{RepoRoot: requestRoot})
			if err != nil {
				t.Fatal(err)
			}
			defer server.Close()
			localBefore, err := snapshotApprovalHistoryTree(fixture.root)
			if err != nil {
				t.Fatal(err)
			}
			externalBefore, err := snapshotApprovalHistoryTree(external)
			if err != nil {
				t.Fatal(err)
			}
			args, err := json.Marshal(map[string]any{"target": requestRoot, "proposalId": enrichment.Proposal.ProposalID, "evidencePackId": enrichment.Pack.EvidencePackID})
			if err != nil {
				t.Fatal(err)
			}
			raw := serveMCPApprovalHistoryRequest(t, server, mcpApprovalHistoryToolCall("get_semantic_approval_history", args))
			if target == "repository alias" {
				body := requireMCPApprovalHistorySuccess(t, raw)
				var result semantic.ApprovalHistoryResult
				if err := json.Unmarshal([]byte(body), &result); err != nil {
					t.Fatal(err)
				}
				if len(result.Events) != 1 || result.Aggregate.Version != 1 || result.Freshness != "current" {
					t.Fatalf("root alias history=%+v", result)
				}
			} else {
				requireMCPApprovalHistoryError(t, raw, "approval_invalid", fixture.root, external, "MCP enrichment")
			}
			localAfter, err := snapshotApprovalHistoryTree(fixture.root)
			if err != nil {
				t.Fatal(err)
			}
			externalAfter, err := snapshotApprovalHistoryTree(external)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(localBefore, localAfter) || !reflect.DeepEqual(externalBefore, externalAfter) {
				t.Fatal("history changed local or external artifacts")
			}
		})
	}
}
