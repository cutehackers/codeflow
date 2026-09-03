package mcp

import (
	"context"
	"os"
	"testing"

	"codeflow/internal/semantic"
	"codeflow/internal/storage"
)

func TestVS03_MCPWorkspaceTools(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "codeflow-mcp-ws-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	srv, err := NewServer(Config{
		RepoRoot:     tempDir,
		RequireToken: false,
	})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	ctx := context.Background()

	// 1. Initial activity query
	res1, err := srv.executeTool(ctx, "get_workspace_activity", map[string]any{
		"target": tempDir,
	})
	if err != nil {
		t.Fatalf("get_workspace_activity failed: %v", err)
	}
	act1, ok := res1.(map[string]any)
	if !ok {
		t.Fatalf("unexpected type for activity response: %T", res1)
	}
	if act1["activity"] != "idle" {
		t.Errorf("expected initial activity idle, got %v", act1["activity"])
	}

	// 2. Submit versioned edit
	resEdit, err := srv.executeTool(ctx, "submit_versioned_edit", map[string]any{
		"target":          tempDir,
		"path":            "src/app.ts",
		"content":         "export const ready = true;",
		"documentVersion": float64(1),
		"source":          "agent_transaction",
	})
	if err != nil {
		t.Fatalf("submit_versioned_edit failed: %v", err)
	}
	editDoc, ok := resEdit.(map[string]any)
	if !ok {
		t.Fatalf("unexpected type for edit response: %T", resEdit)
	}
	if _, ok := editDoc["revision"]; !ok {
		t.Error("missing revision in submit_versioned_edit response")
	}
	if _, ok := editDoc["snapshot"]; !ok {
		t.Error("missing snapshot in submit_versioned_edit response")
	}

	// 3. Query activity again -> should be "editing"
	res2, err := srv.executeTool(ctx, "get_workspace_activity", map[string]any{
		"target": tempDir,
	})
	if err != nil {
		t.Fatalf("get_workspace_activity 2 failed: %v", err)
	}
	act2 := res2.(map[string]any)
	if act2["activity"] != "editing" {
		t.Errorf("expected activity editing, got %v", act2["activity"])
	}
	if act2["currentSnapshotId"] == "" {
		t.Error("expected non-empty currentSnapshotId after edit")
	}
}

func TestVS04_MCPProofAndGapTools(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "codeflow-mcp-vs04-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	srv, err := NewServer(Config{
		RepoRoot:     tempDir,
		RequireToken: false,
	})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	ctx := context.Background()

	// 1. Initial state
	resProof, err := srv.executeTool(ctx, "get_generation_proof", map[string]any{"target": tempDir})
	if err != nil {
		t.Fatalf("get_generation_proof error: %v", err)
	}
	proofMap := resProof.(map[string]any)
	if proofMap["pointer"] != nil {
		t.Errorf("expected nil initial pointer, got %v", proofMap["pointer"])
	}

	resGap, err := srv.executeTool(ctx, "get_verified_gap", map[string]any{"target": tempDir})
	if err != nil {
		t.Fatalf("get_verified_gap error: %v", err)
	}
	gapMap := resGap.(map[string]any)
	if gapMap["status"] != "no_generation_published" {
		t.Errorf("expected no_generation_published, got %v", gapMap["status"])
	}

	// 2. Publish a generation directly to storage
	st, _ := srv.getStorage(tempDir)
	_ = st.InitLayout()
	manifest := &storage.GenerationProofManifest{
		ProofID:                    "proof-mcp-1",
		GenerationID:               "gen-mcp-1",
		ComputedBasisID:            "basis-1",
		ValidatedAgainstSnapshotID: "snap-1",
		CurrentPublication: storage.CurrentPublicationResult{
			Eligibility: "passed",
		},
		ExpectedLiveHeadSnapshotID: "snap-1",
	}
	casRef, _ := st.WriteManifestCAS(manifest)
	ptr := &storage.ActivePointer{
		GenerationID:               "gen-mcp-1",
		ManifestObjectRef:          casRef,
		ComputedBasisID:            "basis-1",
		ValidatedAgainstSnapshotID: "snap-1",
		ExpectedLiveHeadSnapshotID: "snap-1",
	}
	_ = st.CompareAndSwapActivePointer("snap-1", "", ptr)

	// 3. Query get_generation_proof again -> non-nil
	resProof2, err := srv.executeTool(ctx, "get_generation_proof", map[string]any{"target": tempDir})
	if err != nil {
		t.Fatalf("get_generation_proof 2 error: %v", err)
	}
	proofMap2 := resProof2.(map[string]any)
	if proofMap2["pointer"] == nil || proofMap2["manifest"] == nil {
		t.Fatalf("expected non-nil pointer and manifest, got %+v", proofMap2)
	}

	// 4. Submit edit so snapshot changes -> get_verified_gap should report last_verified
	_, _ = srv.executeTool(ctx, "submit_versioned_edit", map[string]any{
		"target":          tempDir,
		"path":            "src/file.ts",
		"content":         "console.log('hi');",
		"documentVersion": float64(1),
	})

	resGap2, err := srv.executeTool(ctx, "get_verified_gap", map[string]any{"target": tempDir})
	if err != nil {
		t.Fatalf("get_verified_gap 2 error: %v", err)
	}
	gapMap2 := resGap2.(map[string]any)
	if gapMap2["freshness"] != "last_verified" {
		t.Errorf("expected last_verified freshness, got %v", gapMap2["freshness"])
	}
	if gapMap2["lastVerifiedGenId"] != "gen-mcp-1" {
		t.Errorf("expected lastVerifiedGenId gen-mcp-1, got %v", gapMap2["lastVerifiedGenId"])
	}
}

func TestVS05_MCPSemanticDeltaAndAlignmentTools(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "codeflow-mcp-vs05-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	srv, err := NewServer(Config{
		RepoRoot:     tempDir,
		RequireToken: false,
	})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	ctx := context.Background()

	// 1. get_semantic_delta missing arguments
	resDeltaMissing, err := srv.executeTool(ctx, "get_semantic_delta", map[string]any{"target": tempDir})
	if err != nil {
		t.Fatalf("get_semantic_delta error: %v", err)
	}
	deltaMissingMap, ok := resDeltaMissing.(map[string]any)
	if !ok || deltaMissingMap["code"] != "missing_precondition" {
		t.Errorf("expected missing_precondition error for missing arguments, got %+v", resDeltaMissing)
	}

	// 2. get_semantic_delta valid
	resDelta, err := srv.executeTool(ctx, "get_semantic_delta", map[string]any{
		"target":   tempDir,
		"baseline": "gen-1",
		"current":  "gen-2",
	})
	if err != nil {
		t.Fatalf("get_semantic_delta valid call failed: %v", err)
	}
	deltaDoc, ok := resDelta.(*semantic.SemanticDeltaIR)
	if !ok {
		t.Fatalf("expected *semantic.SemanticDeltaIR, got %T: %+v", resDelta, resDelta)
	}
	if deltaDoc.Status != "comparable" {
		t.Errorf("expected delta status comparable, got %s", deltaDoc.Status)
	}

	// 3. get_requirement_alignment
	resAlign, err := srv.executeTool(ctx, "get_requirement_alignment", map[string]any{"target": tempDir})
	if err != nil {
		t.Fatalf("get_requirement_alignment failed: %v", err)
	}
	alignDoc, ok := resAlign.(map[string]any)
	if !ok {
		t.Fatalf("unexpected type for requirement alignment: %T", resAlign)
	}
	if alignDoc["computedBasisId"] == "" {
		t.Error("expected non-empty computedBasisId")
	}
}


