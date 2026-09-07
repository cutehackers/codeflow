package harvest

import (
	"os"
	"path/filepath"
	"testing"

	"codeflow/internal/protocol"
)

func TestSourceScoringIndexRemainsSnapshotBoundAfterDiskMutation(t *testing.T) {
	root := t.TempDir()
	lib := filepath.Join(root, "lib")
	if err := os.MkdirAll(lib, 0o755); err != nil {
		t.Fatal(err)
	}
	original := []byte("package feature\n\n// Feature\nfunc Run() {\n\tvar repo FeatureRepository\n\t_ = repo\n}\n")
	path := filepath.Join(lib, "feature.go")
	if err := os.WriteFile(path, original, 0o644); err != nil {
		t.Fatal(err)
	}
	snapshot, err := protocol.CaptureSnapshot(root, 4)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("package feature\nfunc Run() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	idx, err := loadSourceIndexFromSnapshot(snapshot.Files, "lib")
	if err != nil {
		t.Fatal(err)
	}
	if got := idx.byRel["lib/feature.go"]; got != string(original) {
		t.Fatalf("scoring index leaked live-disk mutation: %q", got)
	}
	candidates := []Candidate{{EntrySymbolPath: "lib/feature.go#Run", MarkerKind: "usecase_call", IntentSignals: IntentSignals{ClassName: "Feature"}}}
	ScoreAll(candidates, idx)
	if !candidates[0].BoundaryReachable || candidates[0].FanIn == 0 {
		t.Fatalf("snapshot scoring did not use captured source: %+v", candidates[0])
	}
}
