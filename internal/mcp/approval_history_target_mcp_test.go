package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"codeflow/internal/semantic"
)

func TestMCPApprovalHistoryTargetAuthorizationMatrix(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") == "mcp" {
		t.Skip("helper process")
	}
	fixture, enrichment := newMCPApprovalHistoryDurableFixture(t)
	defer fixture.server.Close()

	transactionRoot := filepath.Join(fixture.root, ".codeflow", "approval-transactions")
	baselineTree, err := snapshotApprovalHistoryTree(transactionRoot)
	if err != nil {
		t.Fatalf("snapshot approval transaction tree before baseline query: %v", err)
	}
	baselineArgs, err := json.Marshal(map[string]string{
		"proposalId":     enrichment.Proposal.ProposalID,
		"evidencePackId": enrichment.Pack.EvidencePackID,
	})
	if err != nil {
		t.Fatalf("marshal baseline history arguments: %v", err)
	}
	baselineRaw := serveMCPApprovalHistoryRequest(t, fixture.server, mcpApprovalHistoryToolCall("get_semantic_approval_history", baselineArgs))
	baselineText := requireMCPApprovalHistorySuccess(t, baselineRaw)
	var baseline semantic.ApprovalHistoryResult
	if err := json.Unmarshal([]byte(baselineText), &baseline); err != nil {
		t.Fatalf("decode baseline approval history: %v", err)
	}
	baselineAfterTree, err := snapshotApprovalHistoryTree(transactionRoot)
	if err != nil {
		t.Fatalf("snapshot approval transaction tree after baseline query: %v", err)
	}
	if !approvalHistoryTreeEqual(baselineTree, baselineAfterTree) {
		t.Fatal("baseline approval history query changed managed transaction tree")
	}

	nested := filepath.Join(fixture.root, "history-target-nested")
	if err := os.Mkdir(nested, 0o700); err != nil {
		t.Fatalf("create nested target: %v", err)
	}
	outside := t.TempDir()
	separator := string(os.PathSeparator)
	relativeTraversal := "history-target-nested" + separator + ".."
	absoluteTraversal := fixture.root + separator + "history-target-nested" + separator + ".."

	type targetCase struct {
		name       string
		target     *string
		allowed    bool
		skipReason string
	}
	cases := []targetCase{
		{name: "target omitted", allowed: true},
		{name: "target empty", target: stringPointer(""), allowed: true},
		{name: "target dot", target: stringPointer("."), allowed: true},
		{name: "exact configured root", target: stringPointer(fixture.root), allowed: true},
		{name: "target space-only", target: stringPointer(" "), allowed: false},
		{name: "target tab-only", target: stringPointer("\t"), allowed: false},
		{name: "nested directory", target: stringPointer(nested), allowed: false},
		{name: "outside absolute directory", target: stringPointer(outside), allowed: false},
		{name: "relative parent traversal", target: stringPointer(relativeTraversal), allowed: false},
		{name: "absolute parent traversal", target: stringPointer(absoluteTraversal), allowed: false},
	}

	sameRootAliasParent := t.TempDir()
	sameRootAlias := filepath.Join(sameRootAliasParent, "history-configured-root-alias")
	if err := os.Symlink(fixture.root, sameRootAlias); err != nil {
		cases = append(cases, targetCase{name: "same-root symlink", skipReason: err.Error()})
	} else {
		cases = append(cases, targetCase{name: "same-root symlink", target: stringPointer(sameRootAlias), allowed: true})
	}
	outsideAliasParent := t.TempDir()
	outsideAlias := filepath.Join(outsideAliasParent, "history-outside-root-alias")
	if err := os.Symlink(outside, outsideAlias); err != nil {
		cases = append(cases, targetCase{name: "outside symlink", skipReason: err.Error()})
	} else {
		cases = append(cases, targetCase{name: "outside symlink", target: stringPointer(outsideAlias), allowed: false})
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.skipReason != "" {
				t.Skipf("symlink unavailable: %s", tc.skipReason)
			}
			before, err := snapshotApprovalHistoryTree(transactionRoot)
			if err != nil {
				t.Fatalf("snapshot approval transaction tree before %s: %v", tc.name, err)
			}
			var hookCalls atomic.Int64
			fixture.server.approvalHistoryBeforeQueryHook = func(context.Context) error {
				hookCalls.Add(1)
				return nil
			}
			args := map[string]any{
				"proposalId":     enrichment.Proposal.ProposalID,
				"evidencePackId": enrichment.Pack.EvidencePackID,
			}
			if tc.target != nil {
				args["target"] = *tc.target
			}
			rawArgs, err := json.Marshal(args)
			if err != nil {
				t.Fatalf("marshal %s arguments: %v", tc.name, err)
			}
			raw := serveMCPApprovalHistoryRequest(t, fixture.server, mcpApprovalHistoryToolCall("get_semantic_approval_history", rawArgs))
			after, err := snapshotApprovalHistoryTree(transactionRoot)
			if err != nil {
				t.Fatalf("snapshot approval transaction tree after %s: %v", tc.name, err)
			}
			if !approvalHistoryTreeEqual(before, after) {
				t.Fatalf("%s changed managed approval transaction tree", tc.name)
			}

			if tc.allowed {
				if got := hookCalls.Load(); got != 1 {
					t.Fatalf("%s history boundary hook calls = %d, want 1", tc.name, got)
				}
				text := requireMCPApprovalHistorySuccess(t, raw)
				var got semantic.ApprovalHistoryResult
				if err := json.Unmarshal([]byte(text), &got); err != nil {
					t.Fatalf("decode %s approval history: %v", tc.name, err)
				}
				if text != baselineText || !reflect.DeepEqual(got, baseline) {
					t.Fatalf("%s history differs from baseline: text=%s baseline=%s got=%+v baseline=%+v", tc.name, text, baselineText, got, baseline)
				}
				return
			}

			if got := hookCalls.Load(); got != 0 {
				t.Fatalf("%s history boundary hook calls = %d, want 0", tc.name, got)
			}
			canaries := []string{enrichment.Proposal.ProposalID, enrichment.Pack.EvidencePackID}
			if tc.target != nil {
				targetCanary := *tc.target
				if strings.TrimSpace(targetCanary) == "" {
					// A raw whitespace-only value necessarily occurs in formatted
					// JSON; assert its escaped representation is absent instead.
					targetCanary = strconv.Quote(targetCanary)
				}
				canaries = append(canaries, targetCanary)
			}
			requireMCPApprovalHistoryError(t, raw, "approval_unauthorized", canaries...)
		})
	}
}
