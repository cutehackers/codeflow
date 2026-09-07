package workspace

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// WithDurableLiveHead observes a persisted head under the same canonical-root
// lock used by SnapshotEngine publication. It never initializes, repairs, or
// migrates workspace state, and supplies no mutable engine to the reader.
func WithDurableLiveHead(repoRoot, expectedID string, read func() error) error {
	if expectedID == "" || read == nil {
		return ErrLiveHeadConflict
	}
	absRoot, err := filepath.Abs(repoRoot)
	if err != nil {
		return err
	}
	root, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		return err
	}
	lock := snapshotEngineRootCommitMutex(root)
	lock.Lock()
	defer lock.Unlock()
	for _, rel := range []string{".codeflow/workspace/live-head.json", ".codeflow/workspace/state.json"} {
		if _, err := validateRepositoryPath(root, rel, false); err != nil {
			return err
		}
	}
	e := &SnapshotEngine{codeflowDir: filepath.Join(root, DirName)}
	head, present, err := e.readDurableLiveHead()
	if err != nil {
		return err
	}
	if !present || head.SnapshotID != expectedID {
		return ErrLiveHeadConflict
	}
	data, err := os.ReadFile(filepath.Join(e.codeflowDir, "workspace", "state.json"))
	if err != nil {
		return err
	}
	var state persistedWorkspaceState
	if err := json.Unmarshal(data, &state); err != nil {
		return err
	}
	if state.SchemaID != "https://codeflow.local/schemas/rflsc.workspace-state.v2.schema.json" || state.SchemaVersion != 2 || state.LiveHeadSnapshotID != head.SnapshotID || state.WorkspaceEpoch != head.WorkspaceEpoch || state.Sequence != head.Sequence || state.BaseFingerprint != computeBaseFingerprint(root) {
		return ErrLiveHeadConflict
	}
	rel := filepath.ToSlash(filepath.Join(DirName, "workspace", "snapshots", sanitizePath(head.SnapshotID)+".json"))
	if _, err := validateRepositoryPath(root, rel, false); err != nil {
		return err
	}
	data, err = os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return err
	}
	var snapshot WorkspaceSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return err
	}
	if snapshot.SchemaID != workspaceSnapshotSchemaID || snapshot.SchemaVersion != 2 || snapshot.SnapshotID != head.SnapshotID || snapshot.WorkspaceEpoch != head.WorkspaceEpoch || snapshot.Sequence != head.Sequence || snapshot.RootTreeID != head.RootTreeID || snapshot.RootTreeID != computeRootTreeID(snapshot.Entries) {
		return fmt.Errorf("durable workspace head does not match its snapshot")
	}
	return read()
}
