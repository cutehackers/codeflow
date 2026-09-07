package workspace

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestVS03A4_WorkspaceWatcherIntegration(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "codeflow-watch-int-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	engine, err := NewSnapshotEngine(tempDir, 1)
	if err != nil {
		t.Fatalf("NewSnapshotEngine failed: %v", err)
	}

	ctx := context.Background()

	// 1. Ingest valid watcher capture
	content := []byte("package auth\n\ntype Service struct{}\n")
	rev, snap, err := engine.ApplyWatcherCapture(ctx, "pkg/auth/service.go", content, time.Now())
	if err != nil {
		t.Fatalf("ApplyWatcherCapture failed: %v", err)
	}

	if rev.Source != SourceWatcherFallback {
		t.Errorf("expected source watcher_fallback, got %s", rev.Source)
	}
	if rev.DocumentVersion != 1 {
		t.Errorf("expected docVersion 1, got %d", rev.DocumentVersion)
	}
	if !snap.LiveHead {
		t.Errorf("expected liveHead == true")
	}

	// 2. Mark reconciliation target on rename or delete
	engine.MarkReconciliation([]ReconciliationTarget{
		{
			Path:   "pkg/auth/service.go",
			Kind:   "delete",
			Reason: "file removed from disk",
		},
	})

	act := engine.CurrentActivity()
	if act.Activity != "reconciling" {
		t.Errorf("expected activity reconciling after markReconciliation, got %s", act.Activity)
	}
}

func TestVS03A4_WatcherConcurrentCaptures(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "codeflow-watch-race-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	engine, err := NewSnapshotEngine(tempDir, 1)
	if err != nil {
		t.Fatalf("NewSnapshotEngine failed: %v", err)
	}

	ctx := context.Background()
	n := 10
	errChan := make(chan error, n)

	for i := 0; i < n; i++ {
		go func(idx int) {
			data := []byte("// revision")
			_, _, err := engine.ApplyWatcherCapture(ctx, "pkg/concurrent.go", data, time.Now())
			errChan <- err
		}(i)
	}

	for i := 0; i < n; i++ {
		if err := <-errChan; err != nil {
			t.Errorf("concurrent capture %d failed: %v", i, err)
		}
	}

	head := engine.LiveHead()
	if head == nil {
		t.Fatal("expected non-nil liveHead")
	}
	if head.Entries["pkg/concurrent.go"].DocumentVersion != n {
		t.Errorf("expected final documentVersion %d, got %d", n, head.Entries["pkg/concurrent.go"].DocumentVersion)
	}
}

func TestVS06CurrentProofReconcileIfChangedPreservesUnchangedHead(t *testing.T) {
	tempDir := t.TempDir()
	path := tempDir + "/main.go"
	if err := os.WriteFile(path, []byte("package main\nconst Version = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	engine, err := NewSnapshotEngine(tempDir, 1)
	if err != nil {
		t.Fatal(err)
	}

	first, err := engine.ReconcileIfChanged(context.Background(), nil)
	if err != nil {
		t.Fatalf("initial reconciliation failed: %v", err)
	}
	second, err := engine.ReconcileIfChanged(context.Background(), nil)
	if err != nil {
		t.Fatalf("unchanged reconciliation failed: %v", err)
	}
	if second.SnapshotID != first.SnapshotID || second.Sequence != first.Sequence {
		t.Fatalf("unchanged current-proof check manufactured a new head: first=%+v second=%+v", first, second)
	}

	if err := os.WriteFile(path, []byte("package main\nconst Version = 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	third, err := engine.ReconcileIfChanged(context.Background(), nil)
	if err != nil {
		t.Fatalf("changed reconciliation failed: %v", err)
	}
	if third.SnapshotID == second.SnapshotID || third.RootTreeID == second.RootTreeID || third.Sequence != second.Sequence+1 {
		t.Fatalf("changed worktree was not assigned a new immutable head: second=%+v third=%+v", second, third)
	}
}
