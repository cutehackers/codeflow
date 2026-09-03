package mcp

import (
	"context"
	"os"
	"testing"
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
