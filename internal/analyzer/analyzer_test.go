package analyzer_test

import (
	"testing"

	"codeflow/internal/analyzer"
)

func TestProjectDetector(t *testing.T) {
	d := analyzer.NewProjectDetector()
	files := map[string]string{
		"pubspec.yaml": "name: my_app\n",
	}
	det := d.DetectSnapshot(files)
	if !det.Confident || det.Language != "dart" {
		t.Fatalf("expected confident dart detection, got: %+v", det)
	}
}

func TestCodeGraphClient(t *testing.T) {
	c := analyzer.NewCodeGraphClient("/test/repo")
	if c.RepoRoot() != "/test/repo" {
		t.Fatalf("expected /test/repo, got: %s", c.RepoRoot())
	}
}
