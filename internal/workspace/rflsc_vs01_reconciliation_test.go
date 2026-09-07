package workspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

func newReconciliationFixture(t *testing.T, files map[string]string) (*SnapshotEngine, *WorkspaceSnapshot) {
	t.Helper()
	root := t.TempDir()
	for path, content := range files {
		fullPath := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	engine, err := NewSnapshotEngine(root, 1)
	if err != nil {
		t.Fatal(err)
	}
	bootstrap := files["bootstrap.go"]
	if bootstrap == "" {
		bootstrap = "package bootstrap\n"
		if err := os.WriteFile(filepath.Join(root, "bootstrap.go"), []byte(bootstrap), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	_, snapshot, err := engine.ApplyVersionedEdit(context.Background(), EditRequest{
		Path: "bootstrap.go", Content: []byte(bootstrap), DocumentVersion: 1, Source: SourceIDEVersioned,
	})
	if err != nil {
		t.Fatal(err)
	}
	return engine, snapshot
}

func reconciliationPaths(entries map[string]SnapshotEntry) []string {
	paths := make([]string, 0, len(entries))
	for path := range entries {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}

func TestRFLSCR2VS01_ReconcileDeleteOmitsPathAndReportsDelta(t *testing.T) {
	engine, prior := newReconciliationFixture(t, map[string]string{
		"bootstrap.go": "package bootstrap\n",
		"keep.go":      "package keep\n",
		"delete.go":    "package delete\n",
	})
	root := engine.repoRoot
	if err := os.Remove(filepath.Join(root, "delete.go")); err != nil {
		t.Fatal(err)
	}

	next, err := engine.Reconcile(context.Background(), []ReconciliationTarget{{
		Path: "delete.go", Kind: "delete", Reason: "watcher delete",
	}})
	if err != nil {
		t.Fatalf("delete reconciliation failed: %v", err)
	}
	if next.Sequence != prior.Sequence+1 {
		t.Fatalf("reconciliation must publish one snapshot: prior=%d next=%d", prior.Sequence, next.Sequence)
	}
	if _, ok := next.Entries["delete.go"]; ok {
		t.Fatalf("deleted repository path was retained: %v", reconciliationPaths(next.Entries))
	}
	if _, ok := next.Entries["keep.go"]; !ok {
		t.Fatalf("unrelated path was lost: %v", reconciliationPaths(next.Entries))
	}
	delta, err := engine.ComputeDelta(prior.SnapshotID, next.SnapshotID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(delta.DeletedPaths, []string{"delete.go"}) {
		t.Fatalf("expected deleted path in delta, got %+v", delta)
	}
}

func TestRFLSCR2VS01_ReconcileRenameRemovesOldAndCapturesNew(t *testing.T) {
	engine, prior := newReconciliationFixture(t, map[string]string{
		"bootstrap.go": "package bootstrap\n",
		"old.go":       "package old\nconst V = 1\n",
	})
	root := engine.repoRoot
	if err := os.Remove(filepath.Join(root, "old.go")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "new.go"), []byte("package new\nconst V = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	next, err := engine.Reconcile(context.Background(), []ReconciliationTarget{{
		Path: "new.go", OldPath: "old.go", Kind: "rename", Reason: "watcher rename",
	}})
	if err != nil {
		t.Fatalf("rename reconciliation failed: %v", err)
	}
	if _, ok := next.Entries["old.go"]; ok {
		t.Fatalf("renamed old path was retained: %v", reconciliationPaths(next.Entries))
	}
	if _, ok := next.Entries["new.go"]; !ok {
		t.Fatalf("renamed new path was not captured: %v", reconciliationPaths(next.Entries))
	}
	delta, err := engine.ComputeDelta(prior.SnapshotID, next.SnapshotID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(delta.DeletedPaths, []string{"old.go"}) || !reflect.DeepEqual(delta.AddedPaths, []string{"new.go"}) {
		t.Fatalf("rename delta mismatch: %+v", delta)
	}
}

func TestRFLSCR2VS01_ReconcileStaleDeleteRetainsRecreatedFile(t *testing.T) {
	engine, prior := newReconciliationFixture(t, map[string]string{
		"bootstrap.go": "package bootstrap\n",
		"recreated.go": "package recreated\nconst V = 1\n",
	})
	if err := os.WriteFile(filepath.Join(engine.repoRoot, "recreated.go"), []byte("package recreated\nconst V = 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	next, err := engine.Reconcile(context.Background(), []ReconciliationTarget{{
		Path: "recreated.go", Kind: "delete", Reason: "stale watcher delete",
	}})
	if err != nil {
		t.Fatalf("stale delete reconciliation failed: %v", err)
	}
	entry, ok := next.Entries["recreated.go"]
	if !ok {
		t.Fatalf("recreated repository path was incorrectly removed: %v", reconciliationPaths(next.Entries))
	}
	if entry.ContentID != hashBytes([]byte("package recreated\nconst V = 2\n")) {
		t.Fatalf("recreated path did not capture current bytes: %+v", entry)
	}
	delta, err := engine.ComputeDelta(prior.SnapshotID, next.SnapshotID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(delta.ModifiedPaths, []string{"recreated.go"}) || len(delta.DeletedPaths) != 0 {
		t.Fatalf("stale delete delta mismatch: %+v", delta)
	}
}

func TestRFLSCR2VS01_ReconcileRenameWithRecreatedOldRetainsBoth(t *testing.T) {
	engine, _ := newReconciliationFixture(t, map[string]string{
		"bootstrap.go": "package bootstrap\n",
		"old.go":       "package old\nconst V = 1\n",
	})
	if err := os.WriteFile(filepath.Join(engine.repoRoot, "old.go"), []byte("package old\nconst V = 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(engine.repoRoot, "new.go"), []byte("package new\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	next, err := engine.Reconcile(context.Background(), []ReconciliationTarget{{
		Path: "new.go", OldPath: "old.go", Kind: "rename", Reason: "stale watcher rename",
	}})
	if err != nil {
		t.Fatalf("stale rename reconciliation failed: %v", err)
	}
	if _, ok := next.Entries["old.go"]; !ok {
		t.Fatalf("recreated old path was incorrectly removed: %v", reconciliationPaths(next.Entries))
	}
	if _, ok := next.Entries["new.go"]; !ok {
		t.Fatalf("new rename path was not captured: %v", reconciliationPaths(next.Entries))
	}
}

func TestRFLSCR2VS01_ReconcileEventLossRecapturesAddChangeDelete(t *testing.T) {
	engine, prior := newReconciliationFixture(t, map[string]string{
		"bootstrap.go": "package bootstrap\n",
		"change.go":    "package change\nconst V = 1\n",
		"delete.go":    "package delete\n",
		"stable.go":    "package stable\n",
	})
	root := engine.repoRoot
	if err := os.WriteFile(filepath.Join(root, "change.go"), []byte("package change\nconst V = 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "delete.go")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "add.go"), []byte("package add\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	next, err := engine.Reconcile(context.Background(), []ReconciliationTarget{{
		Path: "change.go", Kind: "event_loss", Reason: "watcher event gap",
	}})
	if err != nil {
		t.Fatalf("event-loss reconciliation failed: %v", err)
	}
	delta, err := engine.ComputeDelta(prior.SnapshotID, next.SnapshotID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(delta.AddedPaths, []string{"add.go"}) ||
		!reflect.DeepEqual(delta.ModifiedPaths, []string{"change.go"}) ||
		!reflect.DeepEqual(delta.DeletedPaths, []string{"delete.go"}) {
		t.Fatalf("event-loss delta mismatch: %+v", delta)
	}
	lease, err := engine.SnapshotVFS(next.SnapshotID)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	got, err := lease.ReadFile("change.go")
	if err != nil || string(got) != "package change\nconst V = 2\n" {
		t.Fatalf("recaptured changed bytes mismatch: %q %v", got, err)
	}
}

func TestRFLSCR2VS01_ReconcilePreservesOverlayUntilExplicitRemoval(t *testing.T) {
	engine, _ := newReconciliationFixture(t, map[string]string{
		"bootstrap.go":  "package bootstrap\n",
		"repository.go": "package repository\n",
	})
	if _, _, err := engine.ApplyVersionedEdit(context.Background(), EditRequest{
		Path: "unsaved.go", Content: []byte("package unsaved\n"), DocumentVersion: 1, Source: SourceIDEVersioned,
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(engine.repoRoot, "repository.go")); err != nil {
		t.Fatal(err)
	}
	withOverlay, err := engine.Reconcile(context.Background(), []ReconciliationTarget{{
		Path: "repository.go", Kind: "event_loss", Reason: "event gap",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := withOverlay.Entries["repository.go"]; ok {
		t.Fatalf("deleted repository path was retained with overlay: %v", reconciliationPaths(withOverlay.Entries))
	}
	if _, ok := withOverlay.Entries["unsaved.go"]; !ok {
		t.Fatalf("legitimate unsaved overlay was lost: %v", reconciliationPaths(withOverlay.Entries))
	}
	removed, err := engine.Reconcile(context.Background(), []ReconciliationTarget{{
		Path: "unsaved.go", Kind: "delete", Reason: "explicit overlay delete",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := removed.Entries["unsaved.go"]; ok {
		t.Fatalf("explicit overlay deletion did not remove path: %v", reconciliationPaths(removed.Entries))
	}
}

func TestRFLSCR2VS01_ReconcileConflictLeavesPriorHeadAndStableRetryPublishes(t *testing.T) {
	engine, prior := newReconciliationFixture(t, map[string]string{
		"bootstrap.go": "package bootstrap\n",
		"a.go":         "package a\nconst V = 0\n",
		"z.go":         "package z\n",
	})
	root := engine.repoRoot
	var hookErr error
	var calls int
	engine.SetCaptureHook(func(path string) {
		if path != "a.go" || hookErr != nil {
			return
		}
		calls++
		hookErr = os.WriteFile(filepath.Join(root, "a.go"), []byte(fmt.Sprintf("package a\nconst V = %d\n", calls)), 0o644)
	})
	_, err := engine.Reconcile(context.Background(), []ReconciliationTarget{{
		Path: "a.go", Kind: "event_loss", Reason: "unstable recrawl",
	}})
	if hookErr != nil {
		t.Fatal(hookErr)
	}
	if !errors.Is(err, ErrCaptureConflict) {
		t.Fatalf("expected typed capture conflict after bounded exhaustion, got %v", err)
	}
	if calls < 3 {
		t.Fatalf("capture conflict seam did not exhaust retries, calls=%d", calls)
	}
	activity := engine.CurrentActivity()
	if activity.Activity != "reconciling" || activity.CurrentSnapshotID != prior.SnapshotID {
		t.Fatalf("conflict exposed incorrect activity/head: %+v prior=%s", activity, prior.SnapshotID)
	}
	if got := engine.LiveHead(); got == nil || got.SnapshotID != prior.SnapshotID || got.Sequence != prior.Sequence {
		t.Fatalf("conflict changed in-memory head: %+v", got)
	}
	restarted, err := NewSnapshotEngine(root, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := restarted.LiveHead(); got == nil || got.SnapshotID != prior.SnapshotID || got.Sequence != prior.Sequence {
		t.Fatalf("conflict changed durable head: %+v", got)
	}

	engine.SetCaptureHook(nil)
	next, err := engine.Reconcile(context.Background(), []ReconciliationTarget{{
		Path: "a.go", Kind: "event_loss", Reason: "stable retry",
	}})
	if err != nil {
		t.Fatalf("stable reconciliation retry failed: %v", err)
	}
	if next.Sequence != prior.Sequence+1 {
		t.Fatalf("stable retry did not publish sequence+1: prior=%d next=%d", prior.Sequence, next.Sequence)
	}
}
