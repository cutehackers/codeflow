package workspace

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"codeflow/internal/contractharness"
)

func TestVS03A8_SchemaIdentityAndImmutability(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "codeflow-immutability-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	// Create physical source file on disk
	libDir := filepath.Join(tempDir, "lib")
	if err := os.MkdirAll(libDir, 0o755); err != nil {
		t.Fatal(err)
	}
	filePath := filepath.Join(libDir, "app.dart")
	originalSource := []byte("void main() { print('original'); }")
	if err := os.WriteFile(filePath, originalSource, 0o644); err != nil {
		t.Fatal(err)
	}

	engine, err := NewSnapshotEngine(tempDir, "epoch-immutability")
	if err != nil {
		t.Fatalf("NewSnapshotEngine failed: %v", err)
	}

	ctx := context.Background()

	// 1. Transaction producing DocumentRevision, WorkspaceSnapshot, and ChangeBatch
	tx, err := engine.BeginTransaction("agent_transaction")
	if err != nil {
		t.Fatalf("BeginTransaction failed: %v", err)
	}

	newContent := []byte("void main() { print('new virtual revision'); }")
	err = tx.StageEdit(EditRequest{
		Path:            "lib/app.dart",
		Content:         newContent,
		DocumentVersion: 1,
	})
	if err != nil {
		t.Fatalf("StageEdit failed: %v", err)
	}

	batch, snap, err := engine.CommitTransaction(ctx, tx)
	if err != nil {
		t.Fatalf("CommitTransaction failed: %v", err)
	}

	rev, err := engine.GetRevision(batch.Revisions[0])
	if err != nil {
		t.Fatalf("GetRevision failed: %v", err)
	}

	// 2. Validate DocumentRevision against schema
	revBytes, err := json.Marshal(rev)
	if err != nil {
		t.Fatalf("marshal rev failed: %v", err)
	}
	if err := contractharness.ValidateDocumentRevision(revBytes); err != nil {
		t.Errorf("DocumentRevision schema validation failed: %v", err)
	}

	// 3. Validate WorkspaceSnapshot against schema
	snapBytes, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("marshal snap failed: %v", err)
	}
	if err := contractharness.ValidateWorkspaceSnapshot(snapBytes); err != nil {
		t.Errorf("WorkspaceSnapshot schema validation failed: %v", err)
	}

	// 4. Validate ChangeBatch against schema
	batchBytes, err := json.Marshal(batch)
	if err != nil {
		t.Fatalf("marshal batch failed: %v", err)
	}
	if err := contractharness.ValidateChangeBatch(batchBytes); err != nil {
		t.Errorf("ChangeBatch schema validation failed: %v", err)
	}

	// 5. Verify source file on disk is STRICTLY UNMODIFIED (read-only)
	diskContent, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("read disk file failed: %v", err)
	}
	if string(diskContent) != string(originalSource) {
		t.Errorf("source file was modified on disk! expected %q, got %q", string(originalSource), string(diskContent))
	}
}
