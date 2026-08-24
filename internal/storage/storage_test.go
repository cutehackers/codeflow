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
