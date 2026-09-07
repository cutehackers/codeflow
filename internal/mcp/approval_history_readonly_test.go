package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type approvalHistoryTreeEntry struct {
	Path string
	Mode os.FileMode
	Size int64
	Data []byte
}

func TestMCPApprovalHistoryPreservesManagedDatabaseTree(t *testing.T) {
	fixture, enrichment := newMCPApprovalHistoryDurableFixture(t)
	defer fixture.server.Close()

	transactionRoot := filepath.Join(fixture.root, ".codeflow", "approval-transactions")
	before, err := snapshotApprovalHistoryTree(transactionRoot)
	if err != nil {
		t.Fatalf("snapshot approval transaction tree before query: %v", err)
	}
	args, err := json.Marshal(map[string]string{
		"proposalId":     enrichment.Proposal.ProposalID,
		"evidencePackId": enrichment.Pack.EvidencePackID,
		"target":         fixture.root,
	})
	if err != nil {
		t.Fatalf("marshal history arguments: %v", err)
	}
	raw := serveMCPApprovalHistoryRequest(t, fixture.server, mcpApprovalHistoryToolCall("get_semantic_approval_history", args))
	if text := requireMCPApprovalHistorySuccess(t, raw); text == "" {
		t.Fatal("successful history query returned empty content")
	}
	after, err := snapshotApprovalHistoryTree(transactionRoot)
	if err != nil {
		t.Fatalf("snapshot approval transaction tree after query: %v", err)
	}
	if !approvalHistoryTreeEqual(before, after) {
		t.Fatalf("read-only history query changed managed database/WAL tree")
	}
}

func TestMCPApprovalHistoryRejectsUnsafeTransactionRootWithoutRepair(t *testing.T) {
	fixture, enrichment := newMCPApprovalHistoryDurableFixture(t)
	defer fixture.server.Close()

	transactionRoot := filepath.Join(fixture.root, ".codeflow", "approval-transactions")
	if err := os.Chmod(transactionRoot, 0o755); err != nil {
		t.Fatalf("make approval transaction root unsafe: %v", err)
	}
	before, err := snapshotApprovalHistoryTree(transactionRoot)
	if err != nil {
		t.Fatalf("snapshot approval transaction tree before query: %v", err)
	}
	args, err := json.Marshal(map[string]string{
		"proposalId":     enrichment.Proposal.ProposalID,
		"evidencePackId": enrichment.Pack.EvidencePackID,
		"target":         fixture.root,
	})
	if err != nil {
		t.Fatalf("marshal history arguments: %v", err)
	}
	raw := serveMCPApprovalHistoryRequest(t, fixture.server, mcpApprovalHistoryToolCall("get_semantic_approval_history", args))
	response := decodeMCPApprovalHistoryResponse(t, raw)
	if response.Error != nil || !response.Result.IsError || len(response.Result.Content) != 1 || response.Result.Content[0].Type != "text" {
		t.Errorf("history query did not return bounded approval_invalid content")
	} else {
		var payload map[string]string
		if err := json.Unmarshal([]byte(response.Result.Content[0].Text), &payload); err != nil || payload["code"] != "approval_invalid" {
			t.Errorf("history query error code was not bounded approval_invalid")
		}
	}
	after, err := snapshotApprovalHistoryTree(transactionRoot)
	if err != nil {
		t.Fatalf("snapshot approval transaction tree after query: %v", err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Errorf("read-only history query changed managed approval transaction tree")
	}
	info, err := os.Stat(transactionRoot)
	if err != nil {
		t.Fatalf("stat approval transaction root after query: %v", err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("approval transaction root mode after query = %o, want unsafe mode preserved", info.Mode().Perm())
	}
}

func TestMCPApprovalHistoryMissingTransactionRootRemainsAbsent(t *testing.T) {
	root := t.TempDir()
	server, err := NewServer(Config{RepoRoot: root})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer server.Close()
	transactionRoot := filepath.Join(root, ".codeflow", "approval-transactions")
	before, err := snapshotOptionalApprovalHistoryTree(transactionRoot)
	if err != nil {
		t.Fatalf("snapshot missing approval transaction tree before query: %v", err)
	}
	if before.present {
		t.Fatalf("approval transaction tree unexpectedly exists before missing query: %#v", before.entries)
	}
	args, err := json.Marshal(map[string]string{
		"proposalId":     "missing-proposal",
		"evidencePackId": "missing-pack",
		"target":         root,
	})
	if err != nil {
		t.Fatalf("marshal missing query: %v", err)
	}
	raw := serveMCPApprovalHistoryRequest(t, server, mcpApprovalHistoryToolCall("get_semantic_approval_history", args))
	requireMCPApprovalHistoryError(t, raw, "approval_unavailable")
	after, err := snapshotOptionalApprovalHistoryTree(transactionRoot)
	if err != nil {
		t.Fatalf("snapshot missing approval transaction tree after query: %v", err)
	}
	if !approvalHistoryOptionalTreeEqual(before, after) {
		t.Fatalf("missing history query changed transaction tree presence: before=%#v after=%#v", before, after)
	}
}

func TestMCPApprovalHistoryServeCancellationAfterHandlerEntryPreservesTree(t *testing.T) {
	fixture, enrichment := newMCPApprovalHistoryDurableFixture(t)
	defer fixture.server.Close()
	transactionRoot := filepath.Join(fixture.root, ".codeflow", "approval-transactions")
	before, err := snapshotApprovalHistoryTree(transactionRoot)
	if err != nil {
		t.Fatalf("snapshot approval transaction tree before cancellation: %v", err)
	}
	args, err := json.Marshal(map[string]string{
		"proposalId":     enrichment.Proposal.ProposalID,
		"evidencePackId": enrichment.Pack.EvidencePackID,
		"target":         fixture.root,
	})
	if err != nil {
		t.Fatalf("marshal cancellation arguments: %v", err)
	}
	hookCalls := 0
	entryLive := false
	entryExpired := false
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fixture.server.approvalHistoryBeforeQueryHook = func(ctx context.Context) error {
		hookCalls++
		if ctx.Err() != nil {
			entryExpired = true
			return ctx.Err()
		}
		entryLive = true
		cancel()
		return ctx.Err()
	}
	var output bytes.Buffer
	if err := fixture.server.Serve(ctx, strings.NewReader(mcpApprovalHistoryToolCall("get_semantic_approval_history", args)+"\n"), &output); err != nil {
		t.Fatalf("canceled approval-history Serve: %v", err)
	}
	if hookCalls != 1 || !entryLive || entryExpired {
		t.Fatalf("cancellation hook state = calls:%d live:%t expired:%t, want one live entry", hookCalls, entryLive, entryExpired)
	}
	requireMCPApprovalHistoryError(t, output.Bytes(), "approval_unavailable")
	after, err := snapshotApprovalHistoryTree(transactionRoot)
	if err != nil {
		t.Fatalf("snapshot approval transaction tree after cancellation: %v", err)
	}
	if !approvalHistoryTreeEqual(before, after) {
		t.Fatal("canceled approval-history query changed managed transaction tree")
	}
}

func TestMCPApprovalHistoryServeDeadlineAfterHandlerEntryPreservesTree(t *testing.T) {
	fixture, enrichment := newMCPApprovalHistoryDurableFixture(t)
	defer fixture.server.Close()
	transactionRoot := filepath.Join(fixture.root, ".codeflow", "approval-transactions")
	before, err := snapshotApprovalHistoryTree(transactionRoot)
	if err != nil {
		t.Fatalf("snapshot approval transaction tree before deadline: %v", err)
	}
	args, err := json.Marshal(map[string]string{
		"proposalId":     enrichment.Proposal.ProposalID,
		"evidencePackId": enrichment.Pack.EvidencePackID,
		"target":         fixture.root,
	})
	if err != nil {
		t.Fatalf("marshal deadline arguments: %v", err)
	}
	hookCalls := 0
	entryLive := false
	entryExpired := false
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	fixture.server.approvalHistoryBeforeQueryHook = func(ctx context.Context) error {
		hookCalls++
		if ctx.Err() != nil {
			entryExpired = true
			return ctx.Err()
		}
		entryLive = true
		<-ctx.Done()
		return ctx.Err()
	}
	var output bytes.Buffer
	if err := fixture.server.Serve(ctx, strings.NewReader(mcpApprovalHistoryToolCall("get_semantic_approval_history", args)+"\n"), &output); err != nil {
		t.Fatalf("deadline approval-history Serve: %v", err)
	}
	if hookCalls != 1 || !entryLive || entryExpired {
		t.Fatalf("deadline hook state = calls:%d live:%t expired:%t, want one live entry", hookCalls, entryLive, entryExpired)
	}
	requireMCPApprovalHistoryError(t, output.Bytes(), "approval_unavailable")
	after, err := snapshotApprovalHistoryTree(transactionRoot)
	if err != nil {
		t.Fatalf("snapshot approval transaction tree after deadline: %v", err)
	}
	if !approvalHistoryTreeEqual(before, after) {
		t.Fatal("deadline approval-history query changed managed transaction tree")
	}
}

func snapshotApprovalHistoryTree(root string) ([]approvalHistoryTreeEntry, error) {
	entries := make([]approvalHistoryTreeEntry, 0)
	err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		entry := approvalHistoryTreeEntry{Path: relative, Mode: info.Mode(), Size: info.Size()}
		if info.Mode().IsRegular() {
			entry.Data, err = os.ReadFile(path)
			if err != nil {
				return err
			}
		}
		entries = append(entries, entry)
		return nil
	})
	return entries, err
}

type optionalApprovalHistoryTree struct {
	present bool
	entries []approvalHistoryTreeEntry
}

func snapshotOptionalApprovalHistoryTree(root string) (optionalApprovalHistoryTree, error) {
	info, err := os.Lstat(root)
	if errors.Is(err, os.ErrNotExist) {
		return optionalApprovalHistoryTree{}, nil
	}
	if err != nil {
		return optionalApprovalHistoryTree{}, err
	}
	if !info.IsDir() {
		return optionalApprovalHistoryTree{present: true}, nil
	}
	entries, err := snapshotApprovalHistoryTree(root)
	return optionalApprovalHistoryTree{present: true, entries: entries}, err
}

func approvalHistoryOptionalTreeEqual(before, after optionalApprovalHistoryTree) bool {
	return before.present == after.present && (!before.present || approvalHistoryTreeEqual(before.entries, after.entries))
}

func approvalHistoryTreeEqual(before, after []approvalHistoryTreeEntry) bool {
	// SQLite's -shm file is transient WAL-index/lock storage. Its contents may
	// change during a read, but its path, mode, size, and presence remain part
	// of the invariant. Database and WAL bytes are authoritative and strict.
	if len(before) != len(after) {
		return false
	}
	for index := range before {
		left, right := before[index], after[index]
		if left.Path != right.Path || left.Mode != right.Mode || left.Size != right.Size {
			return false
		}
		if strings.HasSuffix(left.Path, "-shm") {
			continue
		}
		if !bytes.Equal(left.Data, right.Data) {
			return false
		}
	}
	return true
}
