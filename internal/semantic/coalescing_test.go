package semantic_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"codeflow/internal/semantic"
	"codeflow/internal/storage"
	"codeflow/internal/workspace"
)

func TestCoalescingScheduler_QuietAndMaxWait(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "codeflow-coalesce-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	_ = os.WriteFile(filepath.Join(tempDir, "f.txt"), []byte("init"), 0o644)
	engine, err := workspace.NewSnapshotEngine(tempDir, "epoch-1")
	if err != nil {
		t.Fatal(err)
	}

	scheduler := semantic.NewCoalescingScheduler(semantic.CoalescingConfig{
		QuietWindow: 100 * time.Millisecond,
		MaxWait:     300 * time.Millisecond,
	})

	ctx := context.Background()

	// 1. Quiet window test: rapid edits stop, then triggers on quiet window
	_, snap1, _ := engine.ApplyVersionedEdit(ctx, workspace.EditRequest{
		Path:            "f.txt",
		Content:         []byte("edit 1"),
		DocumentVersion: 1,
	})
	scheduler.NotifyEdit(snap1)

	time.Sleep(30 * time.Millisecond)
	_, snap2, _ := engine.ApplyVersionedEdit(ctx, workspace.EditRequest{
		Path:            "f.txt",
		Content:         []byte("edit 2"),
		DocumentVersion: 2,
	})
	scheduler.NotifyEdit(snap2)

	// Wait for quiet window (100ms after edit 2)
	select {
	case checkpoint := <-scheduler.Checkpoints():
		if checkpoint.SnapshotID != snap2.SnapshotID {
			t.Errorf("expected quiet window checkpoint to select latest snap2, got %s", checkpoint.SnapshotID)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("timed out waiting for quiet window checkpoint")
	}

	// 2. Max wait test: continuous edits every 40ms (> quiet window threshold if continuous)
	stopContinuous := make(chan struct{})
	go func() {
		ver := 3
		for {
			select {
			case <-stopContinuous:
				return
			case <-time.After(30 * time.Millisecond):
				_, snap, _ := engine.ApplyVersionedEdit(ctx, workspace.EditRequest{
					Path:            "f.txt",
					Content:         []byte("edit continuous"),
					DocumentVersion: ver,
				})
				ver++
				scheduler.NotifyEdit(snap)
			}
		}
	}()

	select {
	case checkpoint := <-scheduler.Checkpoints():
		if checkpoint == nil {
			t.Fatalf("expected non-nil checkpoint on max wait")
		}
	case <-time.After(600 * time.Millisecond):
		t.Fatalf("timed out waiting for max wait checkpoint")
	}
	close(stopContinuous)
}

func TestLateRefinement_SameBasisVsStale(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "codeflow-late-refine-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	st := storage.New(tempDir)
	_ = st.InitLayout()

	basisA := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	basisB := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

	// Initial active pointer on basisA, gen-1, liveHead snap-1
	ptr1 := &storage.ActivePointer{
		GenerationID:               "gen-1",
		ManifestObjectRef:          "cas:manifest-1",
		ComputedBasisID:            basisA,
		ValidatedAgainstSnapshotID: "snap-1",
		ExpectedLiveHeadSnapshotID: "snap-1",
		WorkspaceEpoch:             "epoch-1",
		TaskIntentRevision:         1,
		NormalizedQueryHash:        "hash-1",
		FlowCount:                  1,
	}
	_ = st.CompareAndSwapActivePointer("snap-1", "", ptr1)

	refinementEngine := semantic.NewRefinementCoordinator(st)

	// 1. Same-basis late refinement with matching basisA and unchanged liveHead snap-1 -> SUCCESS
	lateMapIR := createTestMapIR("Q4", nil, 0, 0)
	lateMapIR.ComputedBasisID = basisA
	lateMapIR.GenerationID = "gen-1-refine"

	lateClosure := &semantic.CausalObservationClosure{
		ClosureStatus:   "closed",
		ComputedBasisID: basisA,
	}

	res1, err := refinementEngine.PublishLateRefinement(lateMapIR, lateClosure, "snap-1", "gen-1")
	if err != nil {
		t.Fatalf("expected same-basis late refinement to succeed: %v", err)
	}
	if !res1 {
		t.Fatalf("expected res1 to be true")
	}

	curPtr, _ := st.ReadActivePointer()
	if curPtr.GenerationID != "gen-1-refine" {
		t.Errorf("expected active pointer updated to gen-1-refine, got %s", curPtr.GenerationID)
	}

	// 2. Different basis late result (basisB != basisA) -> REJECTED
	lateMapIRDifferentBasis := createTestMapIR("Q4", nil, 0, 0)
	lateMapIRDifferentBasis.ComputedBasisID = basisB
	lateMapIRDifferentBasis.GenerationID = "gen-stale"

	res2, err := refinementEngine.PublishLateRefinement(lateMapIRDifferentBasis, lateClosure, "snap-1", "gen-1-refine")
	if err == nil || res2 {
		t.Errorf("expected different basis late result to be rejected, got err: %v, res: %v", err, res2)
	}

	// Active pointer must remain unchanged (gen-1-refine)
	curPtrAfter, _ := st.ReadActivePointer()
	if curPtrAfter.GenerationID != "gen-1-refine" {
		t.Errorf("active pointer was corrupted: got %s, want gen-1-refine", curPtrAfter.GenerationID)
	}
}
