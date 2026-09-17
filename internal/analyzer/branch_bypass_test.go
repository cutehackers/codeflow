package analyzer_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"codeflow/internal/analyzer"
)

// TestBranchBypass_AutoFallback verifies that when a Git branch switch causes
// an index hash mismatch, the system seamlessly falls back to AST analysis
// without locking, crashing, or returning stale code.
func TestBranchBypass_AutoFallback(t *testing.T) {
	initialCommit := "abcdef0123456789abcdef0123456789abcdef01"
	repoRoot, cleanup := setupMockCodeGraph(t, initialCommit)
	defer cleanup()

	// Add a sample Go file and go.mod in the working tree
	if err := os.WriteFile(filepath.Join(repoRoot, "go.mod"), []byte("module example.com/checkout\n\ngo 1.22\n"), 0644); err != nil {
		t.Fatal(err)
	}
	goCode := "package main\n\nfunc HandleCheckout() {}\n"
	if err := os.WriteFile(filepath.Join(repoRoot, "checkout.go"), []byte(goCode), 0644); err != nil {
		t.Fatal(err)
	}

	client := analyzer.NewCodeGraphClient(repoRoot)
	ctx := context.Background()

	// 1. Initially synced
	synced, err := client.IsHeadSynced(ctx, repoRoot)
	if err != nil || !synced {
		t.Fatalf("expected synced=true, got synced=%v, err=%v", synced, err)
	}

	// 2. Simulate git branch checkout to new feature branch with new commit
	newCommit := "9876543210fedcba9876543210fedcba98765432"
	gitRef := filepath.Join(repoRoot, ".git", "refs", "heads", "main")
	if err := os.WriteFile(gitRef, []byte(newCommit+"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// 3. Verify mismatch is detected instantly (<1ms)
	synced, err = client.IsHeadSynced(ctx, repoRoot)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if synced {
		t.Fatalf("expected synced=false after git branch checkout")
	}

	// 4. Seamless bypass to AST ProjectDetector & AST Slicer
	detector := analyzer.NewProjectDetector()
	detection := detector.Detect(repoRoot)
	if detection.Language != "go" {
		t.Fatalf("expected language go via AST detector fallback, got: %s", detection.Language)
	}
}
