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

	engine, err := NewSnapshotEngine(tempDir, 1)
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
	if delta.ResolutionChanged {
		t.Fatal("source edits must not overclaim a measured resolver change")
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

func TestSnapshotEngineComputeDeltaSeparatesMeasuredDimensions(t *testing.T) {
	tempDir := t.TempDir()
	for path, content := range map[string]string{
		"main.go": "package main\nfunc main() {}\n",
		"go.mod":  "module example.test\n\ngo 1.22\n",
	} {
		if err := os.WriteFile(filepath.Join(tempDir, path), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	engine, err := NewSnapshotEngine(tempDir, 1)
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := engine.Reconcile(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	_, sourceEdit, err := engine.ApplyVersionedEdit(context.Background(), EditRequest{Path: "main.go", Content: []byte("package main\nfunc main() { println(1) }\n"), DocumentVersion: 2, Source: SourceAgentTransaction})
	if err != nil {
		t.Fatal(err)
	}
	sourceDelta, err := engine.ComputeDelta(baseline.SnapshotID, sourceEdit.SnapshotID)
	if err != nil {
		t.Fatal(err)
	}
	if !sourceDelta.IndexChanged || !sourceDelta.PublicContractChanged || sourceDelta.ResolutionChanged {
		t.Fatalf("source edit dimensions over/under-reported: %+v", sourceDelta)
	}
	_, resolverEdit, err := engine.ApplyVersionedEdit(context.Background(), EditRequest{Path: "go.mod", Content: []byte("module example.test\n\ngo 1.22\n\nrequire example.test/dep v1.0.0\n"), DocumentVersion: 2, Source: SourceAgentTransaction})
	if err != nil {
		t.Fatal(err)
	}
	resolverDelta, err := engine.ComputeDelta(sourceEdit.SnapshotID, resolverEdit.SnapshotID)
	if err != nil {
		t.Fatal(err)
	}
	if !resolverDelta.ResolutionChanged || !resolverDelta.ConfigurationChanged || !resolverDelta.IndexChanged {
		t.Fatalf("resolver input dimensions were not measured: %+v", resolverDelta)
	}
}
