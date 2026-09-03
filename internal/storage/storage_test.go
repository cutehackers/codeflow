package storage_test

import (
	"os"
	"path/filepath"
	"testing"

	"codeflow/internal/storage"
)

func TestStorageAtomicPublishAndRecovery(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "codeflow-storage-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	st := storage.New(tmpDir)
	if err := st.InitLayout(); err != nil {
		t.Fatalf("InitLayout failed: %v", err)
	}

	// 1. Initial state: pointer is nil
	ptr, err := st.ReadPointer()
	if err != nil {
		t.Fatalf("ReadPointer error: %v", err)
	}
	if ptr != nil {
		t.Fatalf("expected nil pointer, got %+v", ptr)
	}

	// 2. Publish Generation 1
	sess1, err := st.BeginGeneration("fingerprint-v1")
	if err != nil {
		t.Fatalf("BeginGeneration failed: %v", err)
	}
	flow1 := []byte(`{"flowId":"flow-1234567890abcdef","title":"Flow 1","basisSha":"abcd","generatedAt":"2026-08-24T00:00:00Z","steps":[],"unknowns":[]}`)
	if err := sess1.AddFlowSpec("flow-1234567890abcdef", flow1, storage.FlowSummary{
		FlowID:          "flow-1234567890abcdef",
		Title:           "Flow 1",
		EntrySymbolPath: "lib/main.dart#main",
		StepCount:       1,
	}); err != nil {
		t.Fatalf("AddFlowSpec failed: %v", err)
	}
	if err := sess1.Commit(); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	ptr1, err := st.ReadPointer()
	if err != nil || ptr1 == nil {
		t.Fatalf("expected valid pointer, got %v, err: %v", ptr1, err)
	}
	if ptr1.FlowCount != 1 || ptr1.BasisFingerprint != "fingerprint-v1" {
		t.Errorf("unexpected pointer: %+v", ptr1)
	}

	// 3. Crash injection during Generation 2: Begin and discard without commit
	sess2, err := st.BeginGeneration("fingerprint-v2")
	if err != nil {
		t.Fatalf("BeginGeneration 2 failed: %v", err)
	}
	_ = sess2.AddFlowSpec("flow-9999999999abcdef", flow1, storage.FlowSummary{
		FlowID: "flow-9999999999abcdef",
	})
	sess2.Discard() // Simulates crash / rollback before pointer rename

	// Pointer 1 MUST remain unchanged and consistent
	ptrRecovered, err := st.ReadPointer()
	if err != nil || ptrRecovered == nil {
		t.Fatalf("expected valid pointer after crash, got %v", ptrRecovered)
	}
	if ptrRecovered.GenerationID != ptr1.GenerationID {
		t.Errorf("pointer changed after discarded generation: got %s, want %s", ptrRecovered.GenerationID, ptr1.GenerationID)
	}

	// Index 1 must still load perfectly
	idx, err := st.ReadLatestIndex()
	if err != nil || idx == nil {
		t.Fatalf("failed to read latest index: %v", err)
	}
	if len(idx.Flows) != 1 || idx.Flows[0].FlowID != "flow-1234567890abcdef" {
		t.Errorf("unexpected index flows: %+v", idx.Flows)
	}
}

func TestSliceFactCache(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "codeflow-cache-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	st := storage.New(tmpDir)
	key := storage.SliceCacheKey("hash123", "cand-123", "v1")

	if _, ok := st.ReadSliceCache(key); ok {
		t.Errorf("expected cache miss")
	}

	data := []byte(`{"candidateId":"cand-123"}`)
	if err := st.WriteSliceCache(key, data); err != nil {
		t.Fatalf("WriteSliceCache failed: %v", err)
	}

	cached, ok := st.ReadSliceCache(key)
	if !ok || string(cached) != string(data) {
		t.Errorf("cache read mismatch: got %q, want %q", string(cached), string(data))
	}
}

func TestWorktreeFingerprint(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "codeflow-fp-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	f1 := filepath.Join(tmpDir, "a.dart")
	f2 := filepath.Join(tmpDir, "b.dart")
	_ = os.WriteFile(f1, []byte("class A {}"), 0o644)
	_ = os.WriteFile(f2, []byte("class B {}"), 0o644)

	fp1, err := storage.ComputeWorktreeFingerprint(tmpDir, []string{"a.dart", "b.dart"})
	if err != nil {
		t.Fatalf("ComputeWorktreeFingerprint failed: %v", err)
	}
	// Permuted order should yield identical fingerprint
	fp2, err := storage.ComputeWorktreeFingerprint(tmpDir, []string{"b.dart", "a.dart"})
	if err != nil {
		t.Fatalf("ComputeWorktreeFingerprint failed: %v", err)
	}
	if fp1 != fp2 {
		t.Errorf("fingerprint order dependency: %s != %s", fp1, fp2)
	}
}

func TestActivePointerCASAndProofManifestCAS(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "codeflow-pointer-cas-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	st := storage.New(tmpDir)
	if err := st.InitLayout(); err != nil {
		t.Fatalf("InitLayout failed: %v", err)
	}

	// 1. Initial state: active pointer is nil
	actPtr, err := st.ReadActivePointer()
	if err != nil {
		t.Fatalf("ReadActivePointer error: %v", err)
	}
	if actPtr != nil {
		t.Fatalf("expected nil active pointer, got %+v", actPtr)
	}

	// 2. Write a GenerationProofManifest to CAS
	manifest1 := &storage.GenerationProofManifest{
		SchemaID:                   "https://codeflow.local/schemas/generation-proof-manifest.schema.json",
		SchemaVersion:              1,
		ProofID:                    "proof-1",
		GenerationID:               "gen-1",
		ComputedBasisID:            "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		ValidatedAgainstSnapshotID: "snap-1",
		TaskIntentRevision:         1,
		NormalizedQueryHash:        "a1b2c3d4e5f67890a1b2c3d4e5f67890a1b2c3d4e5f67890a1b2c3d4e5f67890",
		AnalysisReadSetID:          "readset-1",
		CausalObservationClosureID: "closure-1",
		CurrentPublication: storage.CurrentPublicationResult{
			Eligibility:           "passed",
			SnapshotGate:          "passed",
			ClosureGate:           "passed",
			EvidenceGate:          "passed",
			SemanticAtomicityGate: "passed",
			TaskRelevanceGate:     "passed",
			ComprehensionGate:     "passed",
		},
		SettlementEvaluation: storage.SettlementEvaluation{
			Gate: "pending",
		},
		ArtifactRefs: storage.ArtifactRefs{
			SemanticMap: "cas:sha256:map1",
		},
		ExpectedLiveHeadSnapshotID: "snap-1",
	}

	casRef1, err := st.WriteManifestCAS(manifest1)
	if err != nil {
		t.Fatalf("WriteManifestCAS failed: %v", err)
	}
	if casRef1 == "" {
		t.Fatalf("expected non-empty casRef")
	}

	readManifest, err := st.ReadManifestCAS(casRef1)
	if err != nil {
		t.Fatalf("ReadManifestCAS failed: %v", err)
	}
	if readManifest.ProofID != "proof-1" || readManifest.GenerationID != "gen-1" {
		t.Errorf("manifest mismatch: %+v", readManifest)
	}

	// 3. Initial CAS: expectedPreviousGen = "", expectedLiveHead = "snap-1" -> Succeeded
	ptr1 := &storage.ActivePointer{
		SchemaID:                   "https://codeflow.local/schemas/active-pointer.schema.json",
		SchemaVersion:              1,
		GenerationID:               "gen-1",
		ManifestObjectRef:          casRef1,
		ComputedBasisID:            manifest1.ComputedBasisID,
		ValidatedAgainstSnapshotID: "snap-1",
		ExpectedLiveHeadSnapshotID: "snap-1",
		WorkspaceEpoch:             "epoch-1",
		TaskIntentRevision:         1,
		NormalizedQueryHash:        manifest1.NormalizedQueryHash,
		FlowCount:                  1,
	}

	if err := st.CompareAndSwapActivePointer("snap-1", "", ptr1); err != nil {
		t.Fatalf("initial CAS failed: %v", err)
	}

	currentPtr, err := st.ReadActivePointer()
	if err != nil || currentPtr == nil {
		t.Fatalf("failed to read active pointer: %v", err)
	}
	if currentPtr.GenerationID != "gen-1" {
		t.Errorf("expected gen-1, got %s", currentPtr.GenerationID)
	}

	// 4. Stale writer CAS attempt 1: wrong expectedPreviousGeneration ("gen-old") -> ErrCASConflict
	ptr2 := &storage.ActivePointer{
		GenerationID: "gen-2",
	}
	err = st.CompareAndSwapActivePointer("snap-2", "gen-old", ptr2)
	if err != storage.ErrCASConflict {
		t.Errorf("expected ErrCASConflict on mismatched previous generation, got %v", err)
	}

	// 5. Stale writer CAS attempt 2: wrong expectedLiveHead ("snap-old") -> ErrCASConflict
	err = st.CompareAndSwapActivePointer("snap-old", "gen-1", ptr2)
	if err != storage.ErrCASConflict {
		t.Errorf("expected ErrCASConflict on mismatched live head, got %v", err)
	}

	// 6. Valid CAS attempt: correct previousGen "gen-1" and correct liveHead "snap-1" (current pointer snapshot) -> Succeeded
	ptr2 = &storage.ActivePointer{
		SchemaID:                   "https://codeflow.local/schemas/active-pointer.schema.json",
		SchemaVersion:              1,
		GenerationID:               "gen-2",
		ManifestObjectRef:          casRef1,
		ComputedBasisID:            manifest1.ComputedBasisID,
		ValidatedAgainstSnapshotID: "snap-2",
		ExpectedLiveHeadSnapshotID: "snap-2",
		WorkspaceEpoch:             "epoch-1",
		TaskIntentRevision:         1,
		NormalizedQueryHash:        manifest1.NormalizedQueryHash,
		FlowCount:                  2,
	}
	if err := st.CompareAndSwapActivePointer("snap-1", "gen-1", ptr2); err != nil {
		t.Fatalf("valid CAS 2 failed: %v", err)
	}

	// 7. Verify ReadActiveProofManifest
	activeManifest, err := st.ReadActiveProofManifest()
	if err != nil || activeManifest == nil {
		t.Fatalf("failed to read active proof manifest: %v", err)
	}
	if activeManifest.ProofID != "proof-1" {
		t.Errorf("unexpected active manifest: %+v", activeManifest)
	}
}

