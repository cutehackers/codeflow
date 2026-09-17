package workspace_test

import (
	"os"
	"path/filepath"
	"testing"

	"codeflow/internal/analyzer/workspace"
	verifworkspace "codeflow/internal/collector/verification/workspace"
)

func TestRFLSCR2VS01_A08(t *testing.T) {
	verifworkspace.RunA08(t)
}

func TestRFLSCR2VS01_A08_LegacyObjectAdvancesEpoch(t *testing.T) {
	root := t.TempDir()
	workspaceDir := filepath.Join(root, workspace.DirName, "workspace")
	if err := os.MkdirAll(filepath.Join(workspaceDir, "snapshots"), 0o755); err != nil {
		t.Fatal(err)
	}
	state := []byte(`{"schemaId":"rflsc.workspace-state.v2","schemaVersion":2,"workspaceEpoch":7,"sequence":1,"liveHeadSnapshotId":"legacy-snapshot"}`)
	if err := os.WriteFile(filepath.Join(workspaceDir, "state.json"), state, 0o644); err != nil {
		t.Fatal(err)
	}
	legacySnapshot := []byte(`{"schemaId":"legacy.workspace-snapshot","schemaVersion":1,"snapshotId":"legacy-snapshot","workspaceEpoch":"epoch-legacy","sequence":1,"entries":{}}`)
	if err := os.WriteFile(filepath.Join(workspaceDir, "snapshots", "legacy-snapshot.json"), legacySnapshot, 0o644); err != nil {
		t.Fatal(err)
	}

	engine, err := workspace.NewSnapshotEngine(root, 0)
	if err != nil {
		t.Fatal(err)
	}
	if activity := engine.CurrentActivity(); activity.WorkspaceEpoch != 8 || activity.CurrentSnapshotID != "" {
		t.Fatalf("legacy object was reused as current state: %+v", activity)
	}
	history, err := os.ReadDir(filepath.Join(root, workspace.DirName, "workspace", "historical", "incompatible"))
	if err != nil {
		t.Fatal(err)
	}
	if len(history) == 0 {
		t.Fatal("legacy object was not preserved in incompatible history")
	}
	if _, err := os.Stat(filepath.Join(workspaceDir, "snapshots", "legacy-snapshot.json")); !os.IsNotExist(err) {
		t.Fatalf("legacy object remained in the active directory after quarantine: %v", err)
	}
	historyCount := len(history)
	restarted, err := workspace.NewSnapshotEngine(root, 0)
	if err != nil {
		t.Fatal(err)
	}
	if activity := restarted.CurrentActivity(); activity.WorkspaceEpoch != 8 || activity.CurrentSnapshotID != "" {
		t.Fatalf("legacy migration repeated or reused the old head on restart: %+v", activity)
	}
	history, err = os.ReadDir(filepath.Join(root, workspace.DirName, "workspace", "historical", "incompatible"))
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != historyCount {
		t.Fatalf("legacy migration repeated on restart: first=%d second=%d", historyCount, len(history))
	}
}
