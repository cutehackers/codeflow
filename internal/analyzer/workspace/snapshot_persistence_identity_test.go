package workspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type injectedPersistence struct {
	delegate Persistence
	match    func(string) bool
}

func (p *injectedPersistence) WriteAtomic(path string, data []byte) error {
	if p.match != nil && p.match(path) {
		return fmt.Errorf("injected persistence failure for %s", path)
	}
	return p.delegate.WriteAtomic(path, data)
}

func (p *injectedPersistence) Remove(path string) error {
	return p.delegate.Remove(path)
}

func TestRFLSCR2VS01_PersistenceAtomicityFaults(t *testing.T) {
	categories := []string{"revision", "snapshot", "batch", "state", "live-head"}
	for _, category := range categories {
		t.Run(category, func(t *testing.T) {
			root := t.TempDir()
			engine, err := NewSnapshotEngine(root, 1)
			if err != nil {
				t.Fatal(err)
			}
			_, prior, err := engine.ApplyVersionedEdit(context.Background(), EditRequest{
				Path: "prior.go", Content: []byte("package prior\n"), DocumentVersion: 1, Source: SourceIDEVersioned,
			})
			if err != nil {
				t.Fatal(err)
			}
			before := engine.CurrentActivity()
			fault := &injectedPersistence{delegate: osPersistence{}, match: func(path string) bool {
				path = filepath.ToSlash(path)
				switch category {
				case "revision":
					return strings.Contains(path, "/revisions/")
				case "snapshot":
					return strings.Contains(path, "/snapshots/")
				case "batch":
					return strings.Contains(path, "/batches/")
				case "state":
					return filepath.Base(path) == "state.json"
				case "live-head":
					return filepath.Base(path) == "live-head.json"
				default:
					return false
				}
			}}
			if err := engine.SetPersistence(fault); err != nil {
				t.Fatal(err)
			}

			var retryTx *Transaction
			if category == "batch" {
				tx, err := engine.BeginTransaction(SourceAgentTransaction)
				if err != nil {
					t.Fatal(err)
				}
				retryTx = tx
				for _, edit := range []EditRequest{
					{Path: "a.go", Content: []byte("package a\n"), DocumentVersion: 1},
					{Path: "b.go", Content: []byte("package b\n"), DocumentVersion: 1},
				} {
					if err := tx.StageEdit(edit); err != nil {
						t.Fatal(err)
					}
				}
				if _, _, err := engine.CommitTransaction(context.Background(), tx); err == nil {
					t.Fatal("injected batch failure unexpectedly committed")
				}
				if _, err := engine.GetBatch("batch-" + tx.ID); err == nil {
					t.Fatal("failed transaction exposed a committed batch")
				}
			} else {
				if _, _, err := engine.ApplyVersionedEdit(context.Background(), EditRequest{
					Path: "prior.go", Content: []byte("package prior\nfunc Next() {}\n"), DocumentVersion: 2, Source: SourceIDEVersioned,
				}); err == nil {
					t.Fatal("injected publication failure unexpectedly committed")
				}
			}
			assertActivityUnchanged(t, before, engine.CurrentActivity())
			if got := engine.LiveHead(); got == nil || got.SnapshotID != prior.SnapshotID || got.Sequence != prior.Sequence {
				t.Fatalf("failed publication changed observable head: %+v", got)
			}

			// A fresh engine must see only the prior durable state. This also
			// proves staged revisions and batches were not accepted on restart.
			restarted, err := NewSnapshotEngine(root, 0)
			if err != nil {
				t.Fatal(err)
			}
			if got := restarted.LiveHead(); got == nil || got.SnapshotID != prior.SnapshotID || got.Sequence != prior.Sequence {
				t.Fatalf("restart exposed staged state after failure: %+v", got)
			}
			if category == "batch" {
				fault.match = nil
				if _, snap, err := engine.CommitTransaction(context.Background(), retryTx); err != nil || snap.Sequence != prior.Sequence+1 {
					t.Fatalf("failed transaction was not retryable: snap=%+v err=%v", snap, err)
				}
			} else {
				_, snap, err := restarted.ApplyVersionedEdit(context.Background(), EditRequest{
					Path: "prior.go", Content: []byte("package prior\nfunc Next() {}\n"), DocumentVersion: 2, Source: SourceIDEVersioned,
				})
				if err != nil || snap.Sequence != prior.Sequence+1 {
					t.Fatalf("failed publication was not retryable: snap=%+v err=%v", snap, err)
				}
			}
		})
	}
}

func TestRFLSCR2VS01_CASFailureDoesNotPopulateCache(t *testing.T) {
	root := t.TempDir()
	engine, err := NewSnapshotEngine(root, 1)
	if err != nil {
		t.Fatal(err)
	}
	content := []byte("package cas\n")
	contentID := hashBytes(content)
	fault := &injectedPersistence{delegate: osPersistence{}, match: func(path string) bool {
		return filepath.Base(path) == contentID
	}}
	if err := engine.SetPersistence(fault); err != nil {
		t.Fatal(err)
	}
	if _, _, err := engine.ApplyVersionedEdit(context.Background(), EditRequest{Path: "cas.go", Content: content, DocumentVersion: 1, Source: SourceIDEVersioned}); err == nil {
		t.Fatal("injected CAS failure unexpectedly committed")
	}
	if _, err := engine.ReadCAS(contentID); err == nil {
		t.Fatal("failed CAS write was visible through the cache")
	}
	fault.match = nil
	if _, _, err := engine.ApplyVersionedEdit(context.Background(), EditRequest{Path: "cas.go", Content: content, DocumentVersion: 1, Source: SourceIDEVersioned}); err != nil {
		t.Fatalf("CAS retry failed: %v", err)
	}
}

func TestRFLSCR2VS01_WholeTreeCaptureRetriesAfterEarlierFileChanges(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\nconst V=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "z.go"), []byte("package z\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	engine, err := NewSnapshotEngine(root, 1)
	if err != nil {
		t.Fatal(err)
	}
	mutated := false
	engine.SetCaptureHook(func(path string) {
		if path == "a.go" && !mutated {
			mutated = true
			if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package a\nconst V=2\n"), 0o644); err != nil {
				t.Fatalf("mutate captured file: %v", err)
			}
		}
	})
	_, snap, err := engine.ApplyVersionedEdit(context.Background(), EditRequest{
		Path: "edit.go", Content: []byte("package edit\n"), DocumentVersion: 1, Source: SourceIDEVersioned,
	})
	if err != nil {
		t.Fatalf("whole-tree capture did not retry: %v", err)
	}
	if !mutated {
		t.Fatal("capture hook did not exercise the concurrent mutation")
	}
	if got := snap.Entries["a.go"].ContentID; got != hashBytes([]byte("package a\nconst V=2\n")) {
		t.Fatalf("snapshot published mixed-time bytes for a.go: %s", got)
	}
	lease, err := engine.SnapshotVFS(snap.SnapshotID)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	got, err := lease.ReadFile("a.go")
	if err != nil || string(got) != "package a\nconst V=2\n" {
		t.Fatalf("snapshot lease returned mixed-time bytes: %q %v", got, err)
	}
}

func TestRFLSCR2VS01_IdentityTransitionsAreAtomicAndDurable(t *testing.T) {
	root := t.TempDir()
	engine, err := NewSnapshotEngine(root, 1)
	if err != nil {
		t.Fatal(err)
	}
	_, prior, err := engine.ApplyVersionedEdit(context.Background(), EditRequest{Path: "identity.go", Content: []byte("package identity\n"), DocumentVersion: 1, Source: SourceIDEVersioned})
	if err != nil {
		t.Fatal(err)
	}
	changes, cancel := engine.SubscribeEpochChanges()
	defer cancel()

	identities := []WorkspaceIdentity{
		{RepositoryID: "repo", WorktreeID: "worktree-1", Branch: "main", ConfigurationFingerprint: "cfg-1"},
		{RepositoryID: "repo", WorktreeID: "worktree-2", Branch: "main", ConfigurationFingerprint: "cfg-1"},
		{RepositoryID: "repo", WorktreeID: "worktree-2", Branch: "main", ConfigurationFingerprint: "cfg-2"},
	}
	for index, identity := range identities {
		change, err := engine.SetWorkspaceIdentity(identity)
		if err != nil {
			t.Fatalf("identity %d: %v", index, err)
		}
		if change.NewEpoch != int64(index+2) || engine.WorkspaceIdentity() != identity || engine.LiveHead() != nil {
			t.Fatalf("identity transition %d was not committed atomically: change=%+v identity=%+v head=%+v", index, change, engine.WorkspaceIdentity(), engine.LiveHead())
		}
		select {
		case event := <-changes:
			if event.NewEpoch != change.NewEpoch || event.Reason != "workspace_identity_change" {
				t.Fatalf("unexpected identity event: %+v", event)
			}
		default:
			t.Fatalf("identity transition %d did not publish an event", index)
		}
	}
	restarted, err := NewSnapshotEngine(root, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := restarted.CurrentActivity(); got.WorkspaceEpoch != 4 || got.CurrentSnapshotID != "" {
		t.Fatalf("identity transition was not durable: %+v", got)
	}
	if got := restarted.WorkspaceIdentity(); got != identities[2] {
		t.Fatalf("identity was not durable: %+v", got)
	}
	if historical, err := restarted.GetSnapshot(prior.SnapshotID); err != nil || historical.LiveHead {
		t.Fatalf("prior snapshot was not historical: %+v %v", historical, err)
	}
}

func TestRFLSCR2VS01_IdentityFailureDoesNotPublishEpochOrEvent(t *testing.T) {
	root := t.TempDir()
	engine, err := NewSnapshotEngine(root, 1)
	if err != nil {
		t.Fatal(err)
	}
	_, prior, err := engine.ApplyVersionedEdit(context.Background(), EditRequest{Path: "identity.go", Content: []byte("package identity\n"), DocumentVersion: 1, Source: SourceIDEVersioned})
	if err != nil {
		t.Fatal(err)
	}
	changes, cancel := engine.SubscribeEpochChanges()
	defer cancel()
	fault := &injectedPersistence{delegate: osPersistence{}, match: func(path string) bool { return filepath.Base(path) == "state.json" }}
	if err := engine.SetPersistence(fault); err != nil {
		t.Fatal(err)
	}
	newIdentity := WorkspaceIdentity{RepositoryID: "repo", WorktreeID: "worktree", Branch: "feature", ConfigurationFingerprint: "cfg"}
	if _, err := engine.SetWorkspaceIdentity(newIdentity); err == nil {
		t.Fatal("identity transition unexpectedly succeeded during state failure")
	}
	if got := engine.CurrentActivity(); got.WorkspaceEpoch != 1 || got.CurrentSnapshotID != prior.SnapshotID || engine.WorkspaceIdentity() != (WorkspaceIdentity{}) {
		t.Fatalf("failed identity transition changed observable state: %+v identity=%+v", got, engine.WorkspaceIdentity())
	}
	select {
	case event := <-changes:
		t.Fatalf("failed identity transition published event: %+v", event)
	default:
	}
	restarted, err := NewSnapshotEngine(root, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := restarted.CurrentActivity(); got.WorkspaceEpoch != 1 || got.CurrentSnapshotID != prior.SnapshotID {
		t.Fatalf("restart accepted failed identity transition: %+v", got)
	}
	change, err := restarted.SetWorkspaceIdentity(newIdentity)
	if err != nil || change.NewEpoch != 2 || restarted.WorkspaceIdentity() != newIdentity {
		t.Fatalf("identity retry failed: change=%+v err=%v identity=%+v", change, err, restarted.WorkspaceIdentity())
	}
}

func TestRFLSCR2VS01_RestartDetectsBranchFingerprintChange(t *testing.T) {
	root := t.TempDir()
	gitDir := filepath.Join(root, ".git")
	if err := os.MkdirAll(filepath.Join(gitDir, "refs", "heads"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	refPath := filepath.Join(gitDir, "refs", "heads", "main")
	if err := os.WriteFile(refPath, []byte("commit-one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	engine, err := NewSnapshotEngine(root, 1)
	if err != nil {
		t.Fatal(err)
	}
	_, prior, err := engine.ApplyVersionedEdit(context.Background(), EditRequest{Path: "branch.go", Content: []byte("package branch\n"), DocumentVersion: 1, Source: SourceIDEVersioned})
	if err != nil {
		t.Fatal(err)
	}
	unchanged, err := NewSnapshotEngine(root, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := unchanged.CurrentActivity(); got.WorkspaceEpoch != 1 || got.CurrentSnapshotID != prior.SnapshotID {
		t.Fatalf("unchanged branch unexpectedly advanced: %+v", got)
	}
	if err := os.WriteFile(refPath, []byte("commit-two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	changed, err := NewSnapshotEngine(root, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := changed.CurrentActivity(); got.WorkspaceEpoch != 2 || got.CurrentSnapshotID != "" {
		t.Fatalf("branch fingerprint change did not advance epoch: %+v", got)
	}
	stable, err := NewSnapshotEngine(root, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := stable.CurrentActivity(); got.WorkspaceEpoch != 2 || got.CurrentSnapshotID != "" {
		t.Fatalf("branch fingerprint transition repeated on restart: %+v", got)
	}
}

func TestRFLSCR2VS01_WorktreeFileFingerprintChangesWithRef(t *testing.T) {
	root := t.TempDir()
	gitDir := filepath.Join(t.TempDir(), "worktree-git")
	if err := os.MkdirAll(filepath.Join(gitDir, "refs", "heads"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	refPath := filepath.Join(gitDir, "refs", "heads", "main")
	if err := os.WriteFile(refPath, []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: "+gitDir+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	first := computeBaseFingerprint(root)
	if err := os.WriteFile(refPath, []byte("two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	second := computeBaseFingerprint(root)
	if first == second {
		t.Fatalf("worktree .git file fingerprint ignored referenced ref transition: %s", first)
	}
}

func TestRFLSCR2VS01_VersionedIngressRequiresPositiveVersion(t *testing.T) {
	engine, err := NewSnapshotEngine(t.TempDir(), 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, version := range []int{0, -1} {
		_, _, err := engine.ApplyVersionedEdit(context.Background(), EditRequest{Path: "invalid.go", Content: []byte("package invalid\n"), DocumentVersion: version, Source: SourceIDEVersioned})
		var invalid *InvalidDocumentVersion
		if !errors.As(err, &invalid) || invalid.Version != version {
			t.Fatalf("version %d did not return typed invalid ingress error: %T %v", version, err, err)
		}
	}
	if _, _, err := engine.ApplyVersionedEdit(context.Background(), EditRequest{Path: "watcher.go", Content: []byte("package watcher\n"), DocumentVersion: 0, Source: SourceWatcherFallback}); err != nil {
		t.Fatalf("watcher fallback version synthesis was rejected: %v", err)
	}
	if _, _, err := engine.ApplyVersionedEdit(context.Background(), EditRequest{Path: "negative-watcher.go", Content: []byte("package watcher\n"), DocumentVersion: -1, Source: SourceWatcherFallback}); !errors.Is(err, ErrInvalidDocumentVersion) {
		t.Fatalf("negative watcher version was silently synthesized: %v", err)
	}
}

func TestRFLSCR2VS01_RevisionPathIdentityAvoidsSanitizeCollision(t *testing.T) {
	engine, err := NewSnapshotEngine(t.TempDir(), 1)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := engine.BeginTransaction(SourceAgentTransaction)
	if err != nil {
		t.Fatal(err)
	}
	for _, edit := range []EditRequest{
		{Path: "a/b.go", Content: []byte("package nested\n"), DocumentVersion: 1},
		{Path: "a_b.go", Content: []byte("package flat\n"), DocumentVersion: 1},
	} {
		if err := tx.StageEdit(edit); err != nil {
			t.Fatal(err)
		}
	}
	batch, snap, err := engine.CommitTransaction(context.Background(), tx)
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.Revisions) != 2 || batch.Revisions[0] == batch.Revisions[1] {
		t.Fatalf("colliding paths received non-distinct revision IDs: %+v", batch.Revisions)
	}
	for _, path := range []string{"a/b.go", "a_b.go"} {
		entry, ok := snap.Entries[path]
		if !ok {
			t.Fatalf("snapshot omitted %s: %+v", path, snap.Entries)
		}
		rev, err := engine.GetRevision(entry.RevisionID)
		if err != nil {
			t.Fatal(err)
		}
		if rev.Path != path {
			t.Fatalf("revision path identity was corrupted: want=%s got=%s", path, rev.Path)
		}
	}
}

func assertActivityUnchanged(t *testing.T, want, got ActivityStatus) {
	t.Helper()
	if want.Activity != got.Activity || want.PendingRevisions != got.PendingRevisions || want.CurrentSnapshotID != got.CurrentSnapshotID || want.WorkspaceEpoch != got.WorkspaceEpoch || !reflect.DeepEqual(want.Scope, got.Scope) {
		t.Fatalf("activity changed across failed publication: before=%+v after=%+v", want, got)
	}
}
