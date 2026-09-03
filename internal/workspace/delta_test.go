package workspace

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestSnapshotEngineComputeDelta(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "codeflow-delta-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	_ = os.WriteFile(filepath.Join(tempDir, "base.txt"), []byte("initial base"), 0o644)

	engine, err := NewSnapshotEngine(tempDir, "epoch-1")
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()

	// Snapshot 1: edit base.txt
	_, snap1, err := engine.ApplyVersionedEdit(ctx, EditRequest{
		Path:            "base.txt",
		Content:         []byte("base v1"),
		DocumentVersion: 1,
		Source:          SourceAgentTransaction,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Snapshot 2: edit base.txt (modified) + add new.txt (added)
	_, _, err = engine.ApplyVersionedEdit(ctx, EditRequest{
		Path:            "base.txt",
		Content:         []byte("base v2"),
		DocumentVersion: 2,
		Source:          SourceAgentTransaction,
	})
	if err != nil {
		t.Fatal(err)
	}

	_, snap2, err := engine.ApplyVersionedEdit(ctx, EditRequest{
		Path:            "new.txt",
		Content:         []byte("new content"),
		DocumentVersion: 1,
		Source:          SourceAgentTransaction,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Compute delta from snap1 to snap2
	delta, err := engine.ComputeDelta(snap1.SnapshotID, snap2.SnapshotID)
	if err != nil {
		t.Fatalf("ComputeDelta failed: %v", err)
	}

	if len(delta.ModifiedPaths) != 1 || delta.ModifiedPaths[0] != "base.txt" {
		t.Errorf("expected modified base.txt, got %v", delta.ModifiedPaths)
	}
	if len(delta.AddedPaths) != 1 || delta.AddedPaths[0] != "new.txt" {
		t.Errorf("expected added new.txt, got %v", delta.AddedPaths)
	}
	if len(delta.ChangedPaths) != 2 {
		t.Errorf("expected 2 changed paths, got %v", delta.ChangedPaths)
	}

	// Compute delta from snap1 to snap1 (same)
	sameDelta, err := engine.ComputeDelta(snap1.SnapshotID, snap1.SnapshotID)
	if err != nil {
		t.Fatalf("ComputeDelta same failed: %v", err)
	}
	if len(sameDelta.ChangedPaths) != 0 {
		t.Errorf("expected 0 changed paths for same snapshot, got %v", sameDelta.ChangedPaths)
	}
}
