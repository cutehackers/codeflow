package rflscvs09evidence

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// FaultOverlay adds the private fault bridge only to a disposable test build.
// No normal product build contains the injected function or test source.
func FaultOverlay(t *testing.T, root string) string {
	t.Helper()
	base := filepath.Join(root, "internal", "flowview", "testdata", "vs09")
	values := map[string]string{
		filepath.Join(root, "internal", "semantic", "zz_vs09_fault_bridge.go"):       filepath.Join(base, "fault_bridge.go.txt"),
		filepath.Join(root, "internal", "flowview", "zz_vs09_fault_overlay_test.go"): filepath.Join(base, "fault_overlay_test.go.txt"),
	}
	data, err := json.Marshal(map[string]any{"Replace": values})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "overlay.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
