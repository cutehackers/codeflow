package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestVS03A1_A2_DocumentRevisionAndLiveHead(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "codeflow-workspace-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	engine, err := NewSnapshotEngine(tempDir, "epoch-test-01")
	if err != nil {
		t.Fatalf("NewSnapshotEngine failed: %v", err)
	}

	ctx := context.Background()

	// 1. Submit valid versioned edit (documentVersion = 1)
	content1 := []byte("class Counter { int count = 0; }")
	hash1 := sha256.Sum256(content1)
	expectedContentID1 := hex.EncodeToString(hash1[:])

	edit1 := EditRequest{
		Path:            "lib/counter.dart",
		Content:         content1,
		DocumentVersion: 1,
		Source:          "ide_versioned",
	}

	rev1, snap1, err := engine.ApplyVersionedEdit(ctx, edit1)
	if err != nil {
		t.Fatalf("ApplyVersionedEdit 1 failed: %v", err)
	}

	// VS03-A1 assertions
	if rev1.Path != "lib/counter.dart" {
		t.Errorf("expected path lib/counter.dart, got %s", rev1.Path)
	}
	if rev1.DocumentVersion != 1 {
		t.Errorf("expected version 1, got %d", rev1.DocumentVersion)
	}
	if rev1.ContentID != expectedContentID1 {
		t.Errorf("expected contentId %s, got %s", expectedContentID1, rev1.ContentID)
	}
	if rev1.Source != "ide_versioned" {
		t.Errorf("expected source ide_versioned, got %s", rev1.Source)
	}
	if rev1.WorkspaceEpoch != "epoch-test-01" {
		t.Errorf("expected epoch epoch-test-01, got %s", rev1.WorkspaceEpoch)
	}

	// VS03-A2 assertions: Snapshot created, liveHead updated atomically
	if !snap1.LiveHead {
		t.Errorf("expected snap1.LiveHead == true")
	}
	if snap1.Sequence != 1 {
		t.Errorf("expected snap1.Sequence == 1, got %d", snap1.Sequence)
	}
	if snap1.ParentSnapshotID != nil {
		t.Errorf("expected snap1.ParentSnapshotID == nil, got %v", *snap1.ParentSnapshotID)
	}
	entry1, ok := snap1.Entries["lib/counter.dart"]
	if !ok {
		t.Fatalf("snap1 missing entry for lib/counter.dart")
	}
	if entry1.RevisionID != rev1.RevisionID || entry1.ContentID != rev1.ContentID {
		t.Errorf("snap1 entry mismatch: %+v vs %+v", entry1, rev1)
	}

	// 2. Reject non-monotonic version (same version 1 or lower)
	badEdit := EditRequest{
		Path:            "lib/counter.dart",
		Content:         []byte("class Counter { int count = 1; }"),
		DocumentVersion: 1, // not monotonic (> 1)
		Source:          "ide_versioned",
	}
	if _, _, err := engine.ApplyVersionedEdit(ctx, badEdit); err == nil {
		t.Error("expected error for non-monotonic document version 1 <= 1")
	}

	// 3. Monotonic edit (documentVersion = 2)
	content2 := []byte("class Counter { int count = 2; void inc() => count++; }")
	edit2 := EditRequest{
		Path:            "lib/counter.dart",
		Content:         content2,
		DocumentVersion: 2,
		Source:          "ide_versioned",
	}

	rev2, snap2, err := engine.ApplyVersionedEdit(ctx, edit2)
	if err != nil {
		t.Fatalf("ApplyVersionedEdit 2 failed: %v", err)
	}

	if rev2.DocumentVersion != 2 {
		t.Errorf("expected version 2, got %d", rev2.DocumentVersion)
	}
	if snap2.Sequence != 2 {
		t.Errorf("expected sequence 2, got %d", snap2.Sequence)
	}
	if snap2.ParentSnapshotID == nil || *snap2.ParentSnapshotID != snap1.SnapshotID {
		t.Errorf("expected snap2.ParentSnapshotID == %s, got %v", snap1.SnapshotID, snap2.ParentSnapshotID)
	}

	// Verify liveHead in engine
	currentLiveHead := engine.LiveHead()
	if currentLiveHead == nil || currentLiveHead.SnapshotID != snap2.SnapshotID {
		t.Errorf("engine.LiveHead mismatch, expected %s", snap2.SnapshotID)
	}
}

func TestPruneOrphanCAS_PreservesLivingReferencesAndPrunesOrphans(t *testing.T) {
	tmpDir := t.TempDir()
	engine, err := NewSnapshotEngine(tmpDir, "epoch-test")
	if err != nil {
		t.Fatalf("failed to create engine: %v", err)
	}

	ctx := context.Background()
	_, snap1, err := engine.ApplyVersionedEdit(ctx, EditRequest{
		Path:            "live.go",
		Content:         []byte("package live\nfunc Live() {}"),
		DocumentVersion: 1,
		Source:          "ide_versioned",
	})
	if err != nil {
		t.Fatalf("edit failed: %v", err)
	}

	// Manually write an unreferenced orphan blob directly to the CAS directory
	orphanContentID := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	orphanPath := filepath.Join(tmpDir, ".codeflow", "cas", orphanContentID)
	if err := os.WriteFile(orphanPath, []byte("orphan data"), 0o644); err != nil {
		t.Fatalf("failed to write orphan CAS: %v", err)
	}

	// Verify both exist before prune
	liveEntry := snap1.Entries["live.go"]
	if _, err := os.Stat(filepath.Join(tmpDir, ".codeflow", "cas", liveEntry.ContentID)); err != nil {
		t.Fatalf("live CAS blob should exist: %v", err)
	}
	if _, err := os.Stat(orphanPath); err != nil {
		t.Fatalf("orphan CAS blob should exist before prune: %v", err)
	}

	// Prune orphans
	pruned, err := engine.PruneOrphanCAS()
	if err != nil {
		t.Fatalf("PruneOrphanCAS failed: %v", err)
	}
	if pruned != 1 {
		t.Errorf("expected 1 pruned orphan blob, got %d", pruned)
	}

	// Verify orphan is removed
	if _, err := os.Stat(orphanPath); !os.IsNotExist(err) {
		t.Errorf("orphan CAS blob should be deleted, err=%v", err)
	}

	// Verify live entry CAS blob is strictly preserved
	liveData, err := engine.ReadCAS(liveEntry.ContentID)
	if err != nil {
		t.Fatalf("live CAS blob must remain readable: %v", err)
	}
	if string(liveData) != "package live\nfunc Live() {}" {
		t.Errorf("live CAS blob corrupted: %s", string(liveData))
	}
}

