package workspace

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestVS03A5_SnapshotVFSConformance(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "codeflow-vfs-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	// Create base file on disk
	libDir := filepath.Join(tempDir, "lib")
	if err := os.MkdirAll(libDir, 0o755); err != nil {
		t.Fatal(err)
	}
	widgetPath := filepath.Join(libDir, "widget.dart")
	if err := os.WriteFile(widgetPath, []byte("version on disk v1"), 0o644); err != nil {
		t.Fatal(err)
	}

	otherPath := filepath.Join(libDir, "other.dart")
	if err := os.WriteFile(otherPath, []byte("other unmodified file"), 0o644); err != nil {
		t.Fatal(err)
	}

	engine, err := NewSnapshotEngine(tempDir, "epoch-vfs-01")
	if err != nil {
		t.Fatalf("NewSnapshotEngine failed: %v", err)
	}

	ctx := context.Background()

	// 1. Create a snapshot with an in-memory/virtual overlay of widget.dart (v2)
	overlayContent := []byte("version in snapshot overlay v2")
	_, snap, err := engine.ApplyVersionedEdit(ctx, EditRequest{
		Path:            "lib/widget.dart",
		Content:         overlayContent,
		DocumentVersion: 2,
		Source:          "ide_versioned",
	})
	if err != nil {
		t.Fatalf("ApplyVersionedEdit failed: %v", err)
	}

	// 2. Get SnapshotVFS
	vfs, err := engine.SnapshotVFS(snap.SnapshotID)
	if err != nil {
		t.Fatalf("SnapshotVFS failed: %v", err)
	}

	if vfs.ComputedBasisID() != snap.ComputedBasisID {
		t.Errorf("expected computedBasisId %s, got %s", snap.ComputedBasisID, vfs.ComputedBasisID())
	}

	// 3. Read overlay file -> must yield v2
	readBytes, err := vfs.ReadFile("lib/widget.dart")
	if err != nil {
		t.Fatalf("vfs.ReadFile failed: %v", err)
	}
	if string(readBytes) != string(overlayContent) {
		t.Errorf("expected %q, got %q", string(overlayContent), string(readBytes))
	}

	// 4. Now modify OS disk file to v3 (simulating external modification or mutation)
	if err := os.WriteFile(widgetPath, []byte("corrupted v3 on disk"), 0o644); err != nil {
		t.Fatal(err)
	}

	// 5. Read again via SnapshotVFS -> MUST STILL yield v2 (no OS filesystem re-read leakage)
	readBytesAgain, err := vfs.ReadFile("lib/widget.dart")
	if err != nil {
		t.Fatalf("vfs.ReadFile after OS edit failed: %v", err)
	}
	if string(readBytesAgain) != string(overlayContent) {
		t.Errorf("leak detected! vfs.ReadFile returned OS modified content %q instead of snapshot content %q", string(readBytesAgain), string(overlayContent))
	}

	// 6. Non-overlaid file reads base disk and leases it
	otherBytes, err := vfs.ReadFile("lib/other.dart")
	if err != nil {
		t.Fatalf("vfs.ReadFile for non-overlaid file failed: %v", err)
	}
	if string(otherBytes) != "other unmodified file" {
		t.Errorf("expected %q, got %q", "other unmodified file", string(otherBytes))
	}

	// 7. Modify non-overlaid base file on OS disk (simulating external disk change during analysis)
	if err := os.WriteFile(otherPath, []byte("corrupted base file on disk"), 0o644); err != nil {
		t.Fatal(err)
	}

	// 8. Read again via SnapshotVFS -> MUST STILL yield "other unmodified file" (zero OS leak for base files)
	otherBytesAgain, err := vfs.ReadFile("lib/other.dart")
	if err != nil {
		t.Fatalf("vfs.ReadFile after OS base edit failed: %v", err)
	}
	if string(otherBytesAgain) != "other unmodified file" {
		t.Errorf("base leak detected! vfs.ReadFile returned OS modified content %q instead of snapshot leased content", string(otherBytesAgain))
	}
}
