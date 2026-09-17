package workspace

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestVersionedChangeIngressPreservesBatchAndDeduplicatesProducerWatcherCapture(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.test/live\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	engine, err := NewSnapshotEngine(root, 0)
	if err != nil {
		t.Fatal(err)
	}
	content := []byte("package live\nconst Enabled = true\n")
	accepted, err := engine.ApplyVersionedChanges(context.Background(), VersionedChangeRequest{
		BatchID: "agent-write-42",
		Source:  SourceAgentTransaction,
		Changes: []VersionedChange{{Kind: ChangeUpsert, Path: "live.go", Content: content, DocumentVersion: 1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if accepted.Duplicate || accepted.Batch == nil || accepted.Batch.BatchID != "agent-write-42" || accepted.Batch.Source != SourceAgentTransaction || len(accepted.Revisions) != 1 {
		t.Fatalf("producer batch identity was not preserved: %+v", accepted)
	}

	retry, err := engine.ApplyVersionedChanges(context.Background(), VersionedChangeRequest{
		BatchID: "watcher-observation-9",
		Source:  SourceWatcherFallback,
		Changes: []VersionedChange{{Kind: ChangeUpsert, Path: "live.go", Content: content}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !retry.Duplicate || retry.Snapshot.SnapshotID != accepted.Snapshot.SnapshotID || retry.Revisions[0].RevisionID != accepted.Revisions[0].RevisionID {
		t.Fatalf("same canonical path/content created a second revision: accepted=%+v retry=%+v", accepted, retry)
	}
	if head := engine.LiveHead(); head == nil || head.Sequence != accepted.Snapshot.Sequence {
		t.Fatalf("duplicate advanced live head: %+v", head)
	}
}

func TestVersionedChangeIngressAppliesCreateRenameAndDeleteAsNamedOperations(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.test/ops\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	engine, err := NewSnapshotEngine(root, 0)
	if err != nil {
		t.Fatal(err)
	}
	created, err := engine.ApplyVersionedChanges(context.Background(), VersionedChangeRequest{BatchID: "ide-create", Source: SourceIDEVersioned, Changes: []VersionedChange{{Kind: ChangeCreate, Path: "old.go", Content: []byte("package ops\n"), DocumentVersion: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	renamed, err := engine.ApplyVersionedChanges(context.Background(), VersionedChangeRequest{BatchID: "ide-rename", Source: SourceIDEVersioned, Changes: []VersionedChange{{Kind: ChangeRename, OldPath: "old.go", Path: "new.go", Content: []byte("package ops\n"), DocumentVersion: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, oldExists := renamed.Snapshot.Entries["old.go"]; oldExists {
		t.Fatal("rename retained the old path")
	}
	if _, newExists := renamed.Snapshot.Entries["new.go"]; !newExists {
		t.Fatal("rename did not publish the new path")
	}
	deleted, err := engine.ApplyVersionedChanges(context.Background(), VersionedChangeRequest{BatchID: "ide-delete", Source: SourceIDEVersioned, Changes: []VersionedChange{{Kind: ChangeDelete, Path: "new.go"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := deleted.Snapshot.Entries["new.go"]; exists || deleted.Snapshot.Sequence != created.Snapshot.Sequence+2 {
		t.Fatalf("delete did not produce the expected lineage: %+v", deleted.Snapshot)
	}
}
