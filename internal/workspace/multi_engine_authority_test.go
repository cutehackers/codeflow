package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSnapshotEngineRejectsStaleStateMutationsBeforeReopen(t *testing.T) {
	root, current, stale, currentHead := staleSnapshotEnginePair(t)
	statePath := filepath.Join(root, DirName, "workspace", "state.json")
	beforeState, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("read current workspace state: %v", err)
	}

	stale.SetActivity("stale-activity")
	stale.AcknowledgeEdit("")
	stale.BeginAnalysis("", "stale-trace")
	stale.EndAnalysis("", false)
	stale.MarkReconciliation([]ReconciliationTarget{{Path: "checkout.go", Kind: "event_loss", Reason: "stale"}})
	if err := stale.WriteSource("checkout.go", []byte("must not write")); !errors.Is(err, ErrLiveHeadConflict) {
		t.Fatalf("stale WriteSource error = %v, want live-head conflict", err)
	}
	if _, err := stale.SetWorkspaceIdentity(WorkspaceIdentity{
		RepositoryID: "stale-repository", WorktreeID: "stale-worktree", ConfigurationFingerprint: "stale-config",
	}); !errors.Is(err, ErrLiveHeadConflict) {
		t.Fatalf("stale SetWorkspaceIdentity error = %v, want live-head conflict", err)
	}
	if err := stale.BindWorkspaceIdentity(WorkspaceIdentity{
		RepositoryID: "stale-repository", WorktreeID: "stale-worktree", ConfigurationFingerprint: "stale-config",
	}); !errors.Is(err, ErrLiveHeadConflict) {
		t.Fatalf("stale BindWorkspaceIdentity error = %v, want live-head conflict", err)
	}
	if err := stale.SetEpoch(stale.currentEpoch + 1); !errors.Is(err, ErrLiveHeadConflict) {
		t.Fatalf("stale SetEpoch error = %v, want live-head conflict", err)
	}
	if _, err := stale.AdvanceEpoch("stale"); !errors.Is(err, ErrLiveHeadConflict) {
		t.Fatalf("stale AdvanceEpoch error = %v, want live-head conflict", err)
	}

	afterState, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("read workspace state after stale mutations: %v", err)
	}
	if string(afterState) != string(beforeState) {
		t.Fatalf("stale state mutation changed durable state:\nbefore=%s\nafter=%s", beforeState, afterState)
	}

	reopened, err := NewSnapshotEngine(root, 0)
	if err != nil {
		t.Fatalf("reopen after stale state mutations: %v", err)
	}
	if reopened.LiveHeadID() != currentHead || reopened.LiveHeadID() != current.LiveHeadID() {
		t.Fatalf("reopened live head = %q, want current %q", reopened.LiveHeadID(), currentHead)
	}
	called := false
	if err := reopened.WithLiveHead(currentHead, func() error {
		called = true
		return nil
	}); err != nil {
		t.Fatalf("current live-head authority after stale mutations: %v", err)
	}
	if !called {
		t.Fatal("current live-head callback was not invoked")
	}
}

func TestSnapshotEngineRejectsStalePublisherWithoutArtifacts(t *testing.T) {
	root, current, stale, currentHead := staleSnapshotEnginePair(t)
	statePath := filepath.Join(root, DirName, "workspace", "state.json")
	headPath := filepath.Join(root, DirName, "workspace", "live-head.json")
	beforeState, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("read state before stale publisher: %v", err)
	}
	beforeHead, err := os.ReadFile(headPath)
	if err != nil {
		t.Fatalf("read live head before stale publisher: %v", err)
	}

	revision, snapshot, err := stale.ApplyVersionedEdit(context.Background(), EditRequest{
		Path: "checkout.go", Content: []byte("func Submit() { stale }"), DocumentVersion: 2, Source: SourceIDEVersioned,
	})
	if !errors.Is(err, ErrLiveHeadConflict) {
		t.Fatalf("stale publisher error = %v, want live-head conflict", err)
	}
	if revision != nil || snapshot != nil {
		t.Fatalf("stale publisher returned artifacts: revision=%+v snapshot=%+v", revision, snapshot)
	}

	afterState, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("read state after stale publisher: %v", err)
	}
	afterHead, err := os.ReadFile(headPath)
	if err != nil {
		t.Fatalf("read live head after stale publisher: %v", err)
	}
	if string(afterState) != string(beforeState) || string(afterHead) != string(beforeHead) {
		t.Fatalf("stale publisher changed durable authority:\nstate before=%s\nstate after=%s\nhead before=%s\nhead after=%s", beforeState, afterState, beforeHead, afterHead)
	}
	if current.LiveHeadID() != currentHead {
		t.Fatalf("current engine live head = %q, want %q", current.LiveHeadID(), currentHead)
	}
}

func TestNewSnapshotEngineRejectsInconsistentDurableAuthorityWithoutRewrite(t *testing.T) {
	testCases := []struct {
		name   string
		mutate func(t *testing.T, statePath, headPath string)
	}{
		{
			name: "state head identity",
			mutate: func(t *testing.T, statePath, _ string) {
				data, err := os.ReadFile(statePath)
				if err != nil {
					t.Fatalf("read state: %v", err)
				}
				var state persistedWorkspaceState
				if err := json.Unmarshal(data, &state); err != nil {
					t.Fatalf("decode state: %v", err)
				}
				state.LiveHeadSnapshotID = "snap-stale-authority"
				updated, err := json.Marshal(state)
				if err != nil {
					t.Fatalf("encode state: %v", err)
				}
				if err := os.WriteFile(statePath, updated, 0o600); err != nil {
					t.Fatalf("write state: %v", err)
				}
			},
		},
		{
			name: "live head sequence",
			mutate: func(t *testing.T, _, headPath string) {
				data, err := os.ReadFile(headPath)
				if err != nil {
					t.Fatalf("read live head: %v", err)
				}
				var head liveHeadRecord
				if err := json.Unmarshal(data, &head); err != nil {
					t.Fatalf("decode live head: %v", err)
				}
				head.Sequence++
				updated, err := json.Marshal(head)
				if err != nil {
					t.Fatalf("encode live head: %v", err)
				}
				if err := os.WriteFile(headPath, updated, 0o600); err != nil {
					t.Fatalf("write live head: %v", err)
				}
			},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			root, _, _, _ := staleSnapshotEnginePair(t)
			statePath := filepath.Join(root, DirName, "workspace", "state.json")
			headPath := filepath.Join(root, DirName, "workspace", "live-head.json")
			testCase.mutate(t, statePath, headPath)
			beforeState, err := os.ReadFile(statePath)
			if err != nil {
				t.Fatalf("read mutated state: %v", err)
			}
			beforeHead, err := os.ReadFile(headPath)
			if err != nil {
				t.Fatalf("read mutated live head: %v", err)
			}

			if reopened, err := NewSnapshotEngine(root, 0); err == nil || reopened != nil {
				t.Fatalf("inconsistent durable authority reopened: engine=%p err=%v", reopened, err)
			}
			afterState, err := os.ReadFile(statePath)
			if err != nil {
				t.Fatalf("read state after rejected reopen: %v", err)
			}
			afterHead, err := os.ReadFile(headPath)
			if err != nil {
				t.Fatalf("read live head after rejected reopen: %v", err)
			}
			if string(afterState) != string(beforeState) || string(afterHead) != string(beforeHead) {
				t.Fatalf("rejected reopen rewrote durable authority:\nstate before=%s\nstate after=%s\nhead before=%s\nhead after=%s", beforeState, afterState, beforeHead, afterHead)
			}
		})
	}
}

func staleSnapshotEnginePair(t *testing.T) (string, *SnapshotEngine, *SnapshotEngine, string) {
	t.Helper()
	root := t.TempDir()
	current, _, err := func() (*SnapshotEngine, *WorkspaceSnapshot, error) {
		engine, err := NewSnapshotEngine(root, 1)
		if err != nil {
			return nil, nil, err
		}
		_, snapshot, err := engine.ApplyVersionedEdit(context.Background(), EditRequest{
			Path: "checkout.go", Content: []byte("func Submit() { initial }"), DocumentVersion: 1, Source: SourceIDEVersioned,
		})
		return engine, snapshot, err
	}()
	if err != nil {
		t.Fatalf("create initial workspace head: %v", err)
	}
	stale, err := NewSnapshotEngine(root, 0)
	if err != nil {
		t.Fatalf("open stale workspace engine: %v", err)
	}
	_, currentHead, err := current.ApplyVersionedEdit(context.Background(), EditRequest{
		Path: "checkout.go", Content: []byte("func Submit() { current }"), DocumentVersion: 2, Source: SourceIDEVersioned,
	})
	if err != nil {
		t.Fatalf("publish current workspace head: %v", err)
	}
	return root, current, stale, currentHead.SnapshotID
}
