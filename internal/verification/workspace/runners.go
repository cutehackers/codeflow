// Package workspace contains the shared verification test runners for VS-01
// (Workspace & Snapshot VFS) criteria.
package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	ws "codeflow/internal/workspace"
)

// Evidence is the observable result produced by one shared criterion runner.
// Execution identity is added by the caller because the same runner can be
// invoked by a named package test or by the evidence registry subtest.
type Evidence struct {
	Criterion                string
	ImplementationTestID     string
	SnapshotTreeDigest       string
	RepositoryPathWriteAudit ws.SourceWriteAudit
	ObjectRefs               []string
}

var implementationTestIDs = map[string]string{
	"VS01-A1":  "codeflow/internal/workspace.TestRFLSCR2VS01_A01",
	"VS01-A2":  "codeflow/internal/workspace.TestRFLSCR2VS01_A02",
	"VS01-A3":  "codeflow/internal/workspace.TestRFLSCR2VS01_A03",
	"VS01-A4":  "codeflow/internal/workspace.TestRFLSCR2VS01_A04",
	"VS01-A5":  "codeflow/internal/workspace.TestRFLSCR2VS01_A05",
	"VS01-A6":  "codeflow/internal/workspace.TestRFLSCR2VS01_A06",
	"VS01-A7":  "codeflow/internal/workspace.TestRFLSCR2VS01_A07",
	"VS01-A8":  "codeflow/internal/workspace.TestRFLSCR2VS01_A08",
	"VS01-A9":  "codeflow/internal/workspace.TestRFLSCR2VS01_A09",
	"VS01-A10": "codeflow/internal/workspace.TestRFLSCR2VS01_A10",
}

func newEngine(t *testing.T) (*ws.SnapshotEngine, string) {
	t.Helper()
	root := t.TempDir()
	engine, err := ws.NewSnapshotEngine(root, 1)
	if err != nil {
		t.Fatalf("NewSnapshotEngine: %v", err)
	}
	return engine, root
}

func applyEdit(t *testing.T, engine *ws.SnapshotEngine, path, content string, version int) (*ws.DocumentRevision, *ws.WorkspaceSnapshot) {
	t.Helper()
	rev, snap, err := engine.ApplyVersionedEdit(context.Background(), ws.EditRequest{
		Path: path, Content: []byte(content), DocumentVersion: version, Source: ws.SourceIDEVersioned,
	})
	if err != nil {
		t.Fatalf("ApplyVersionedEdit: %v", err)
	}
	return rev, snap
}

func evidence(t *testing.T, criterion string, engine *ws.SnapshotEngine, refs ...string) Evidence {
	t.Helper()
	head := engine.LiveHead()
	digest := "none"
	if head != nil {
		digest = head.RootTreeID
	}
	if digest == "none" {
		for _, ref := range refs {
			if snap, err := engine.GetSnapshot(ref); err == nil && snap.RootTreeID != "" {
				digest = snap.RootTreeID
				break
			}
		}
	}
	audit := engine.SourceWriteAudit()
	if audit.CapturedSnapshotTreeDigest == "" {
		for _, ref := range refs {
			if snap, err := engine.GetSnapshot(ref); err == nil && snap.RepositoryPathWriteAudit.CapturedSnapshotTreeDigest != "" {
				audit = snap.RepositoryPathWriteAudit
				break
			}
		}
	}
	return Evidence{
		Criterion:                criterion,
		ImplementationTestID:     implementationTestIDs[criterion],
		SnapshotTreeDigest:       digest,
		RepositoryPathWriteAudit: audit,
		ObjectRefs:               append([]string(nil), refs...),
	}
}

// RunA01 executes the VS01-A1 public edit and complete snapshot behavior.
func RunA01(t *testing.T) Evidence {
	t.Helper()
	engine, root := newEngine(t)
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "unchanged.go"), []byte("package src\n"), 0o444); err != nil {
		t.Fatal(err)
	}
	rev, snap := applyEdit(t, engine, "src/edited.go", "package src\nfunc Edited() {}\n", 1)
	if rev == nil || snap == nil {
		t.Fatal("accepted edit must return a revision and snapshot")
	}
	if _, ok := snap.Entries["src/edited.go"]; !ok {
		t.Fatal("snapshot must contain the accepted revision")
	}
	if _, ok := snap.Entries["src/unchanged.go"]; !ok {
		t.Fatal("snapshot must contain the complete repository tree")
	}
	if snap.RootTreeID == "" {
		t.Fatal("snapshot must expose a root tree identity")
	}
	changedEdited := false
	for _, changed := range snap.ChangedEntries {
		if changed.Path == "src/edited.go" && changed.DocumentRevisionID == rev.RevisionID {
			changedEdited = true
		}
	}
	if !changedEdited {
		t.Fatalf("accepted revision missing from changed entries: %+v", snap.ChangedEntries)
	}
	restarted, err := ws.NewSnapshotEngine(root, 0)
	if err != nil {
		t.Fatalf("restart SnapshotEngine: %v", err)
	}
	if loaded := restarted.LiveHead(); loaded == nil || loaded.SnapshotID != snap.SnapshotID {
		t.Fatalf("snapshot must survive restart, got %+v", loaded)
	}
	return evidence(t, "VS01-A1", engine, rev.RevisionID, snap.SnapshotID)
}

// RunA02 executes the immutable snapshot-VFS read behavior.
func RunA02(t *testing.T) Evidence {
	t.Helper()
	engine, root := newEngine(t)
	basePath := filepath.Join(root, "src", "base.go")
	if err := os.MkdirAll(filepath.Dir(basePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(basePath, []byte("package base\nconst Version = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, snap := applyEdit(t, engine, "src/edit.go", "package base\n", 1)
	if err := os.WriteFile(basePath, []byte("package base\nconst Version = 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	lease, err := engine.SnapshotVFS(snap.SnapshotID)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	got, err := lease.ReadFile("src/base.go")
	if err != nil || string(got) != "package base\nconst Version = 1\n" {
		t.Fatalf("snapshot leaked disk mutation: %q %v", got, err)
	}
	return evidence(t, "VS01-A2", engine, snap.SnapshotID, lease.RootTreeID())
}

// RunA03 executes defensive ownership checks for returned objects and bytes.
func RunA03(t *testing.T) Evidence {
	t.Helper()
	engine, _ := newEngine(t)
	original := []byte("package immutable\nfunc Keep() {}\n")
	rev, snap := applyEdit(t, engine, "immutable.go", string(original), 1)
	original[0] = 'X'
	snap.Entries["immutable.go"] = ws.SnapshotEntry{}
	snap.ChangedEntries[0].Path = "mutated"
	lease, err := engine.SnapshotVFS(snap.SnapshotID)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	bytes, err := lease.ReadFile("immutable.go")
	if err != nil || string(bytes) != "package immutable\nfunc Keep() {}\n" {
		t.Fatalf("defensive ownership evidence failed: %q %v", bytes, err)
	}
	storedRev, err := engine.GetRevision(rev.RevisionID)
	if err != nil || storedRev.Content != "package immutable\nfunc Keep() {}\n" {
		t.Fatalf("revision ownership evidence failed: %+v %v", storedRev, err)
	}
	storedSnap, err := engine.GetSnapshot(snap.SnapshotID)
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := storedSnap.Entries["immutable.go"]
	if !ok || entry.RevisionID != rev.RevisionID {
		t.Fatalf("stored snapshot changed after caller mutation: %+v", storedSnap.Entries)
	}
	return evidence(t, "VS01-A3", engine, rev.RevisionID, snap.SnapshotID)
}

// RunA04 executes the all-or-previous multi-file transaction behavior.
func RunA04(t *testing.T) Evidence {
	t.Helper()
	engine, _ := newEngine(t)
	_, previous := applyEdit(t, engine, "root.go", "package root\n", 1)
	tx, err := engine.BeginTransaction(ws.SourceAgentTransaction)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.StageEdit(ws.EditRequest{Path: "a.go", Content: []byte("package a\n"), DocumentVersion: 1}); err != nil {
		t.Fatal(err)
	}
	if err := tx.StageEdit(ws.EditRequest{Path: "b.go", Content: []byte("package b\n"), DocumentVersion: 1}); err != nil {
		t.Fatal(err)
	}
	batch, snap, err := engine.CommitTransaction(context.Background(), tx)
	if err != nil || snap.Sequence != previous.Sequence+1 || len(batch.Revisions) != 2 {
		t.Fatalf("transaction evidence failed: batch=%+v snap=%+v err=%v", batch, snap, err)
	}
	for _, path := range []string{"root.go", "a.go", "b.go"} {
		if _, ok := snap.Entries[path]; !ok {
			t.Fatalf("committed snapshot missing %s: %+v", path, snap.Entries)
		}
	}
	return evidence(t, "VS01-A4", engine, batch.BatchID, snap.SnapshotID)
}

// RunA05 executes direct and symlink repository path policy checks.
func RunA05(t *testing.T) Evidence {
	t.Helper()
	root := t.TempDir()
	outsideDir := t.TempDir()
	outsidePath := filepath.Join(outsideDir, "secret.go")
	if err := os.WriteFile(outsidePath, []byte("package secret\nconst Token = \"do-not-disclose\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	linkPath := filepath.Join(root, "linked.go")
	if err := os.Symlink(outsidePath, linkPath); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	engine, err := ws.NewSnapshotEngine(root, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"../secret.go", outsidePath, "linked.go"} {
		_, _, err := engine.ApplyVersionedEdit(context.Background(), ws.EditRequest{
			Path: path, Content: []byte("not allowed"), DocumentVersion: 1,
		})
		if err == nil {
			t.Fatalf("expected path policy rejection for %q", path)
		}
		var policyErr *ws.PathPolicyError
		if !errors.As(err, &policyErr) {
			t.Fatalf("expected typed path policy error for %q, got %T: %v", path, err, err)
		}
	}
	if got := engine.LiveHead(); got != nil {
		t.Fatalf("rejected paths must not publish a snapshot: %+v", got)
	}
	if err := os.Remove(linkPath); err != nil {
		t.Fatal(err)
	}
	_, snap := applyEdit(t, engine, "valid.go", "package valid\n", 1)
	return evidence(t, "VS01-A5", engine, snap.SnapshotID)
}

// RunA06 executes watcher stat/read/stat validation and fallback capture.
func RunA06(t *testing.T) Evidence {
	t.Helper()
	engine, _ := newEngine(t)
	content := []byte("package observed\n")
	hash := sha256.Sum256(content)
	contentID := hex.EncodeToString(hash[:])
	now := time.Now().UTC()
	unstable := ws.WatcherObservation{
		Path: "observed.go", Content: content, ContentID: contentID,
		BeforeSize: int64(len(content)), AfterSize: int64(len(content) + 1), BeforeModTime: now, AfterModTime: now,
	}
	if _, _, err := engine.ApplyWatcherObservation(context.Background(), unstable); err == nil {
		t.Fatal("unstable watcher observation must not publish")
	}
	if activity := engine.CurrentActivity(); activity.Activity != "reconciling" {
		t.Fatalf("unstable capture must expose reconciling, got %+v", activity)
	}
	if head := engine.LiveHead(); head != nil {
		t.Fatalf("unstable capture published a mixed snapshot: %+v", head)
	}
	unstable.AfterSize = unstable.BeforeSize
	rev, snap, err := engine.ApplyWatcherObservation(context.Background(), unstable)
	if err != nil {
		t.Fatal(err)
	}
	if rev.Source != ws.SourceWatcherFallback || snap.Entries["observed.go"].ContentID != contentID {
		t.Fatalf("stable capture mismatch: rev=%+v snap=%+v", rev, snap)
	}
	return evidence(t, "VS01-A6", engine, snap.SnapshotID)
}

// RunA07 executes an epoch transition and restart lineage check.
func RunA07(t *testing.T) Evidence {
	t.Helper()
	root := t.TempDir()
	engine, err := ws.NewSnapshotEngine(root, 3)
	if err != nil {
		t.Fatal(err)
	}
	changes, cancel := engine.SubscribeEpochChanges()
	defer cancel()
	_, oldSnap := applyEdit(t, engine, "branch.go", "package branch\n", 1)
	if err := engine.SetEpoch(4); err != nil {
		t.Fatal(err)
	}
	select {
	case change := <-changes:
		if change.PreviousEpoch != 3 || change.NewEpoch != 4 || change.PreviousSnapshotID != oldSnap.SnapshotID {
			t.Fatalf("unexpected epoch change: %+v", change)
		}
	default:
		t.Fatal("epoch change event was not published")
	}
	if head := engine.LiveHead(); head != nil {
		t.Fatalf("epoch transition retained old live head: %+v", head)
	}
	historical, err := engine.GetSnapshot(oldSnap.SnapshotID)
	if err != nil {
		t.Fatal(err)
	}
	if historical.LiveHead {
		t.Fatal("prior snapshot must be historical after epoch transition")
	}
	if activity := engine.CurrentActivity(); activity.WorkspaceEpoch != 4 || activity.Activity != "reconciling" {
		t.Fatalf("unexpected post-transition activity: %+v", activity)
	}
	restarted, err := ws.NewSnapshotEngine(root, 0)
	if err != nil {
		t.Fatal(err)
	}
	if activity := restarted.CurrentActivity(); activity.WorkspaceEpoch != 4 {
		t.Fatalf("epoch was not durable: %+v", activity)
	}
	_, fresh := applyEdit(t, restarted, "branch.go", "package branch\nfunc Main() {}\n", 1)
	if fresh.WorkspaceEpoch != 4 || fresh.Sequence != 1 || fresh.ParentSnapshotID != nil {
		t.Fatalf("new epoch did not start a fresh chain: %+v", fresh)
	}
	return evidence(t, "VS01-A7", engine, oldSnap.SnapshotID)
}

// RunA08 executes incompatible string epoch preservation and rebuilding.
func RunA08(t *testing.T) Evidence {
	t.Helper()
	root := t.TempDir()
	stateDir := filepath.Join(root, ws.DirName, "workspace")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := []byte(`{"schemaId":"legacy.workspace-state","schemaVersion":1,"workspaceEpoch":"epoch-feature","sequence":7,"liveHeadSnapshotId":"legacy-snap"}`)
	if err := os.WriteFile(filepath.Join(stateDir, "state.json"), legacy, 0o644); err != nil {
		t.Fatal(err)
	}
	engine, err := ws.NewSnapshotEngine(root, 0)
	if err != nil {
		t.Fatal(err)
	}
	if head := engine.LiveHead(); head != nil {
		t.Fatalf("legacy active state was reused as current: %+v", head)
	}
	if epoch := engine.CurrentActivity().WorkspaceEpoch; epoch != 1 {
		t.Fatalf("expected rebuilt integer epoch 1, got %d", epoch)
	}
	historyEntries, err := os.ReadDir(filepath.Join(root, ws.DirName, "workspace", "historical", "incompatible"))
	if err != nil {
		t.Fatal(err)
	}
	if len(historyEntries) == 0 {
		t.Fatal("legacy state was not preserved as historical")
	}
	foundLegacy := false
	for _, entry := range historyEntries {
		data, readErr := os.ReadFile(filepath.Join(root, ws.DirName, "workspace", "historical", "incompatible", entry.Name()))
		if readErr == nil && string(data) == string(legacy) {
			foundLegacy = true
		}
	}
	if !foundLegacy {
		t.Fatal("historical artifact bytes were not preserved")
	}
	_, snap := applyEdit(t, engine, "rebuilt.go", "package rebuilt\n", 1)
	if snap.WorkspaceEpoch != 1 {
		t.Fatalf("rebuilt live state has wrong epoch: %+v", snap)
	}
	return evidence(t, "VS01-A8", engine, snap.SnapshotID)
}

// RunA09 executes retention-safe CAS collection for a leased snapshot.
func RunA09(t *testing.T) Evidence {
	t.Helper()
	engine, _ := newEngine(t)
	_, snap := applyEdit(t, engine, "retained.go", "package retained\n", 1)
	lease, err := engine.SnapshotVFS(snap.SnapshotID)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	entry := snap.Entries["retained.go"]
	orphanID := "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	if err := os.WriteFile(engine.ContentPath(orphanID), []byte("orphan"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.PruneOrphanCAS(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(engine.ContentPath(entry.ContentID)); err != nil {
		t.Fatalf("retained revision CAS was collected: %v", err)
	}
	if _, err := os.Stat(engine.ContentPath(orphanID)); !os.IsNotExist(err) {
		t.Fatalf("unreferenced CAS was not collected, err=%v", err)
	}
	if _, err := lease.ReadFile("retained.go"); err != nil {
		t.Fatalf("lease lost retained revision after GC: %v", err)
	}
	return evidence(t, "VS01-A9", engine, snap.SnapshotID)
}

// RunA10 executes read-only source capture and blocked source-write audit.
func RunA10(t *testing.T) Evidence {
	t.Helper()
	root := t.TempDir()
	sourcePath := filepath.Join(root, "source.go")
	original := []byte("package source\nconst Value = 1\n")
	if err := os.WriteFile(sourcePath, original, 0o444); err != nil {
		t.Fatal(err)
	}
	engine, err := ws.NewSnapshotEngine(root, 1)
	if err != nil {
		t.Fatal(err)
	}
	_, snap := applyEdit(t, engine, "edit.go", "package source\n", 1)
	if got := engine.SourceWriteAudit(); got.CodeFlowWriteCount != 0 || got.SourceIntegrityViolation {
		t.Fatalf("read-only capture recorded an unexpected source write: %+v", got)
	}
	if snap.RepositoryPathWriteAudit.CapturedSnapshotTreeDigest != snap.RootTreeID {
		t.Fatalf("snapshot tree digest was not preserved in audit: %+v", snap.RepositoryPathWriteAudit)
	}
	disk, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(disk) != string(original) {
		t.Fatalf("capture modified repository source: %q", string(disk))
	}
	if err := engine.WriteSource("source.go", []byte("must not overwrite")); !errors.Is(err, ws.ErrSourceIntegrityViolation) {
		t.Fatalf("expected source integrity violation, got %v", err)
	}
	disk, err = os.ReadFile(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(disk) != string(original) {
		t.Fatalf("blocked source write changed repository source: %q", string(disk))
	}
	if audit := engine.SourceWriteAudit(); audit.CodeFlowWriteCount != 1 || !audit.SourceIntegrityViolation {
		t.Fatalf("source write audit missing blocked attempt: %+v", audit)
	}
	return evidence(t, "VS01-A10", engine, snap.SnapshotID)
}
