package flowview

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestDiscoverLiveCoordinatorKeepsRecordOnAuthFailure verifies that a 401/403
// from the ping endpoint is treated as proof the endpoint is owned, not as a
// stale record. Deleting another owner's registration would break discovery
// for another process sharing the repository.
func TestDiscoverLiveCoordinatorKeepsRecordOnAuthFailure(t *testing.T) {
	root := t.TempDir()
	srv, err := NewServer(Config{RepoRoot: root, Port: 0, Mode: "project_change"})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer func() { _ = srv.Shutdown(context.Background()) }()
	srv.Start()

	bad := LiveCoordinatorRecord{
		URL:       srv.URL(),
		Token:     "mismatched-token",
		PID:       os.Getpid(),
		StartedAt: time.Now().UTC(),
		RepoRoot:  root,
	}
	if err := WriteLiveCoordinator(root, bad); err != nil {
		t.Fatalf("WriteLiveCoordinator: %v", err)
	}
	got, err := DiscoverLiveCoordinator(root)
	if err != nil {
		t.Fatalf("DiscoverLiveCoordinator: %v", err)
	}
	if got == nil {
		t.Fatal("expected coordinator with auth failure to stay active")
	}
	if _, err := ReadLiveCoordinator(root); err != nil {
		t.Fatalf("record must survive an auth failure: %v", err)
	}
}
