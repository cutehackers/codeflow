package slicing

import (
	"os"
	"path/filepath"
	"testing"

	"codeflow/internal/analyzer/protocol"
)

func TestSnapshotParamsRemainBoundToCapturedBytesAfterDiskMutation(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "src", "feature.ts")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	original := []byte("export function Run() { return 'snapshot'; }\n")
	if err := os.WriteFile(path, original, 0o644); err != nil {
		t.Fatal(err)
	}
	snapshot, err := protocol.CaptureSnapshot(root, 8)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("export function Run() { return 'disk'; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	params := snapshot.Params()
	files := params["files"].(map[string]string)
	if files["src/feature.ts"] != string(original) {
		t.Fatalf("request files leaked live-disk mutation: %q", files["src/feature.ts"])
	}
	snapshotFiles := params["snapshot"].(map[string]any)["files"].(map[string]string)
	if snapshotFiles["src/feature.ts"] != string(original) {
		t.Fatalf("nested request files leaked live-disk mutation: %q", snapshotFiles["src/feature.ts"])
	}
}
