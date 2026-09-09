package flowview

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCoordinatorWatcherFallbackCapturesDirectFilesystemChange(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte("package main\nconst Value = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv, err := NewServer(Config{RepoRoot: root, Port: 0, WorkspaceWatchInterval: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	srv.Start()
	defer func() { _ = srv.Shutdown(context.Background()) }()
	time.Sleep(60 * time.Millisecond)
	updated := []byte("package main\nconst Value = 2\n")
	if err := os.WriteFile(path, updated, 0o644); err != nil {
		t.Fatal(err)
	}
	wantHash := sha256.Sum256(updated)
	want := hex.EncodeToString(wantHash[:])
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		head := srv.engine.LiveHead()
		if head != nil && head.Entries["main.go"].ContentID == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("watcher did not capture the direct source change: head=%+v err=%v", srv.engine.LiveHead(), srv.LastPipelineError())
}
