package workspace

import (
	"context"
	"os"
	"testing"
)

func TestVS03A7_WorkspaceEpochSeparation(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "codeflow-epoch-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	engine, err := NewSnapshotEngine(tempDir, 1)
	if err != nil {
		t.Fatalf("NewSnapshotEngine failed: %v", err)
	}

	ctx := context.Background()

	// 1. Edit in epoch-feature-branch
	_, snap1, err := engine.ApplyVersionedEdit(ctx, EditRequest{
		Path:            "lib/main.dart",
		Content:         []byte("void main() {}"),
		DocumentVersion: 1,
		Source:          "ide_versioned",
	})
	if err != nil {
		t.Fatalf("ApplyVersionedEdit 1 failed: %v", err)
	}

	if snap1.WorkspaceEpoch != 1 {
		t.Errorf("expected epoch 1, got %d", snap1.WorkspaceEpoch)
	}
	if !snap1.LiveHead {
		t.Errorf("expected snap1.LiveHead == true")
	}

	// 2. Branch or configuration switch triggers epoch transition
	if err := engine.SetEpoch(2); err != nil {
		t.Fatalf("SetEpoch failed: %v", err)
	}

	// Verify liveHead is decoupled from prior epoch
	if engine.LiveHead() != nil {
		t.Errorf("expected liveHead to be nil in fresh epoch before first edit")
	}
	act := engine.CurrentActivity()
	if act.WorkspaceEpoch != 2 {
		t.Errorf("expected activity epoch 2, got %d", act.WorkspaceEpoch)
	}
	if act.Activity != "reconciling" {
		t.Errorf("expected activity reconciling on epoch switch, got %s", act.Activity)
	}

	// 3. First edit in new epoch starts at sequence 1
	_, snap2, err := engine.ApplyVersionedEdit(ctx, EditRequest{
		Path:            "lib/main.dart",
		Content:         []byte("void main() { runApp(); }"),
		DocumentVersion: 1, // can start at 1 because epoch is separated
		Source:          "ide_versioned",
	})
	if err != nil {
		t.Fatalf("ApplyVersionedEdit 2 failed: %v", err)
	}

	if snap2.WorkspaceEpoch != 2 {
		t.Errorf("expected snap2 epoch 2, got %d", snap2.WorkspaceEpoch)
	}
	if snap2.Sequence != 1 {
		t.Errorf("expected snap2 sequence 1 in new epoch, got %d", snap2.Sequence)
	}
	if snap2.ParentSnapshotID != nil {
		t.Errorf("expected snap2 to not chain from prior epoch snapshot as current parent, got %v", *snap2.ParentSnapshotID)
	}

	// 4. Prior epoch snapshot remains intact as historical record
	historicalSnap, err := engine.GetSnapshot(snap1.SnapshotID)
	if err != nil {
		t.Fatalf("GetSnapshot historical failed: %v", err)
	}
	if historicalSnap.WorkspaceEpoch != 1 {
		t.Errorf("historical snap epoch changed: %d", historicalSnap.WorkspaceEpoch)
	}
}
