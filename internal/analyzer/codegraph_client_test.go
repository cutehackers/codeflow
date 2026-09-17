package analyzer_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"codeflow/internal/analyzer"
	_ "modernc.org/sqlite"
)

func setupMockCodeGraph(t *testing.T, commitSHA string) (string, func()) {
	t.Helper()
	dir := t.TempDir()

	// 1. Setup .git directory with symbolic HEAD
	gitDir := filepath.Join(dir, ".git")
	if err := os.MkdirAll(filepath.Join(gitDir, "refs", "heads"), 0755); err != nil {
		t.Fatal(err)
	}
	headContent := "ref: refs/heads/main\n"
	if err := os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte(headContent), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "refs", "heads", "main"), []byte(commitSHA+"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// 2. Setup .codegraph directory with sqlite db and head file
	codegraphDir := filepath.Join(dir, ".codegraph")
	if err := os.MkdirAll(codegraphDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(codegraphDir, "head"), []byte(commitSHA+"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	dbPath := filepath.Join(codegraphDir, "codegraph.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}

	createSchema := `
	CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT);
	CREATE TABLE symbols (name TEXT, symbol_path TEXT, file TEXT, line INTEGER, kind TEXT);
	CREATE TABLE callees (caller_symbol TEXT, callee_symbol TEXT, kind TEXT);
	CREATE TABLE callers (caller_symbol TEXT, callee_symbol TEXT);

	INSERT INTO meta (key, value) VALUES ('head_commit', '` + commitSHA + `');

	INSERT INTO symbols VALUES ('OrderCheckoutHandler', 'checkout.go#OrderCheckoutHandler', 'checkout.go', 45, 'route');
	INSERT INTO symbols VALUES ('ValidateStock', 'stock.go#ValidateStock', 'stock.go', 12, 'entry');

	INSERT INTO callees VALUES ('OrderService.Pay', 'TossPayment.Approve', 'implementation');
	INSERT INTO callees VALUES ('OrderService.Pay', 'KakaoPayment.Approve', 'implementation');

	INSERT INTO callers VALUES ('WebRouter.Route', 'OrderCheckoutHandler');
	INSERT INTO callers VALUES ('ApiGateway.Handle', 'OrderCheckoutHandler');
	INSERT INTO callees VALUES ('OrderCheckoutHandler', 'OrderService.Pay', 'call');
	`
	if _, err := db.Exec(createSchema); err != nil {
		db.Close()
		t.Fatal(err)
	}
	db.Close()

	return dir, func() {}
}

func TestCodeGraphClient_FindEntrypoints(t *testing.T) {
	commit := "1111222233334444555566667777888899990000"
	repoRoot, cleanup := setupMockCodeGraph(t, commit)
	defer cleanup()

	client := analyzer.NewCodeGraphClient(repoRoot)
	ctx := context.Background()

	t0 := time.Now()
	entries, err := client.FindEntrypoints(ctx, repoRoot)
	elapsed := time.Since(t0)

	if err != nil {
		t.Fatalf("FindEntrypoints failed: %v", err)
	}
	if elapsed > 10*time.Millisecond {
		t.Errorf("FindEntrypoints took too long: %v (budget: 5~10ms)", elapsed)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entrypoints, got %d", len(entries))
	}
	if entries[0].Name != "ValidateStock" || entries[0].Kind != "entry" {
		t.Errorf("unexpected first entry: %+v", entries[0])
	}
	if entries[1].Name != "OrderCheckoutHandler" || entries[1].Kind != "route" {
		t.Errorf("unexpected second entry: %+v", entries[1])
	}
}

func TestCodeGraphClient_ResolveDynamicDispatch(t *testing.T) {
	commit := "aaaaabbbbbcccccdddddeeeeefffff0000011111"
	repoRoot, cleanup := setupMockCodeGraph(t, commit)
	defer cleanup()

	client := analyzer.NewCodeGraphClient(repoRoot)
	ctx := context.Background()

	callees, err := client.ResolveDynamicDispatch(ctx, "OrderService.Pay")
	if err != nil {
		t.Fatalf("ResolveDynamicDispatch failed: %v", err)
	}
	if len(callees) != 2 {
		t.Fatalf("expected 2 resolved implementations, got %d", len(callees))
	}
	if callees[0] != "TossPayment.Approve" || callees[1] != "KakaoPayment.Approve" {
		t.Errorf("unexpected callees: %v", callees)
	}
}

func TestCodeGraphClient_GetDirectNeighbors(t *testing.T) {
	commit := "1234567890123456789012345678901234567890"
	repoRoot, cleanup := setupMockCodeGraph(t, commit)
	defer cleanup()

	client := analyzer.NewCodeGraphClient(repoRoot)
	ctx := context.Background()

	t0 := time.Now()
	radar, err := client.GetDirectNeighbors(ctx, "OrderCheckoutHandler")
	elapsed := time.Since(t0)

	if err != nil {
		t.Fatalf("GetDirectNeighbors failed: %v", err)
	}
	if elapsed > 10*time.Millisecond {
		t.Errorf("GetDirectNeighbors took too long: %v (budget: 10ms)", elapsed)
	}
	if len(radar.DirectCallers) != 2 {
		t.Errorf("expected 2 direct callers, got %d", len(radar.DirectCallers))
	}
	if len(radar.DirectCallees) != 1 {
		t.Errorf("expected 1 direct callee, got %d", len(radar.DirectCallees))
	}
}

func TestCodeGraphClient_IsHeadSynced_SuccessAndMismatch(t *testing.T) {
	commit := "feedfacefeedfacefeedfacefeedfacefeedface"
	repoRoot, cleanup := setupMockCodeGraph(t, commit)
	defer cleanup()

	client := analyzer.NewCodeGraphClient(repoRoot)
	ctx := context.Background()

	synced, err := client.IsHeadSynced(ctx, repoRoot)
	if err != nil {
		t.Fatalf("IsHeadSynced failed: %v", err)
	}
	if !synced {
		t.Errorf("expected synced to be true")
	}

	// Switch branch by updating git HEAD to a new commit
	newCommit := "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
	gitRef := filepath.Join(repoRoot, ".git", "refs", "heads", "main")
	if err := os.WriteFile(gitRef, []byte(newCommit+"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	syncedAfterBranchSwitch, err := client.IsHeadSynced(ctx, repoRoot)
	if err != nil {
		t.Fatalf("IsHeadSynced failed after branch switch: %v", err)
	}
	if syncedAfterBranchSwitch {
		t.Errorf("expected synced to be false after branch switch")
	}
}

func TestCodeGraphClient_PackedRefsSupport(t *testing.T) {
	dir := t.TempDir()
	gitDir := filepath.Join(dir, ".git")
	if err := os.MkdirAll(gitDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Symbolic HEAD points to feature branch in packed-refs
	headContent := "ref: refs/heads/feature/checkout\n"
	if err := os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte(headContent), 0644); err != nil {
		t.Fatal(err)
	}

	targetSHA := "3333444455556666777788889999000011112222"
	packedRefs := "# pack-refs with: peeled-tags\n" +
		"1111111111111111111111111111111111111111 refs/heads/main\n" +
		targetSHA + " refs/heads/feature/checkout\n"
	if err := os.WriteFile(filepath.Join(gitDir, "packed-refs"), []byte(packedRefs), 0644); err != nil {
		t.Fatal(err)
	}

	sha, err := analyzer.ResolveGitHead(gitDir)
	if err != nil {
		t.Fatalf("ResolveGitHead failed: %v", err)
	}
	if sha != targetSHA {
		t.Fatalf("expected SHA %s, got %s", targetSHA, sha)
	}
}

func TestCodeGraphClient_NotIndexedFallback(t *testing.T) {
	dir := t.TempDir()
	client := analyzer.NewCodeGraphClient(dir)
	ctx := context.Background()

	_, err := client.FindEntrypoints(ctx, dir)
	if err != analyzer.ErrNotIndexed {
		t.Errorf("expected ErrNotIndexed, got: %v", err)
	}

	synced, err := client.IsHeadSynced(ctx, dir)
	if err != analyzer.ErrNotIndexed || synced {
		t.Errorf("expected ErrNotIndexed, got: synced=%v, err=%v", synced, err)
	}
}

func TestCodeGraphClient_WorktreeGitFileSupport(t *testing.T) {
	tempDir := t.TempDir()
	actualGitDir := filepath.Join(tempDir, "actual-git-repo", "worktrees", "wt1")
	if err := os.MkdirAll(actualGitDir, 0755); err != nil {
		t.Fatal(err)
	}

	worktreeDir := filepath.Join(tempDir, "my-worktree")
	if err := os.MkdirAll(worktreeDir, 0755); err != nil {
		t.Fatal(err)
	}

	// In a git worktree, .git is a file containing "gitdir: <path>"
	gitFile := filepath.Join(worktreeDir, ".git")
	if err := os.WriteFile(gitFile, []byte("gitdir: "+actualGitDir+"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	targetSHA := "7777888899990000111122223333444455556666"
	if err := os.WriteFile(filepath.Join(actualGitDir, "HEAD"), []byte(targetSHA+"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	sha, err := analyzer.ResolveGitHead(gitFile)
	if err != nil {
		t.Fatalf("ResolveGitHead failed on git worktree pointer: %v", err)
	}
	if sha != targetSHA {
		t.Errorf("expected %s, got %s", targetSHA, sha)
	}
}
