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

	engine, err := NewSnapshotEngine(tempDir, "epoch-watch-01")
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

	engine, err := NewSnapshotEngine(tempDir, "epoch-watch-race")
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
