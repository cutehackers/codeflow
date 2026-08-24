package detect

import (
	"os"
	"path/filepath"
	"testing"
)

func writePubspec(t *testing.T, repoRoot, content string) string {
	t.Helper()
	path := filepath.Join(repoRoot, pubspecFileName)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

const fullPubspec = `name: super_app
description: A Flutter application.
publish_to: "none"
version: 1.0.0

environment:
  sdk: ">=3.0.0 <4.0.0"

dependencies:
  flutter:
    sdk: flutter
  name_provider: ^1.0.0
`

func TestDetectDartConfidentWithParsedName(t *testing.T) {
	repoRoot := t.TempDir()
	writePubspec(t, repoRoot, fullPubspec)

	det := DetectDart(repoRoot)
	if !det.Confident {
		t.Fatal("Confident = false, want true")
	}
	if det.Language != "dart" {
		t.Errorf("Language = %q, want dart", det.Language)
	}
	if det.ProjectName != "super_app" {
		t.Errorf("ProjectName = %q, want super_app", det.ProjectName)
	}
}

func TestDetectDartParsesQuotedName(t *testing.T) {
	repoRoot := t.TempDir()
	writePubspec(t, repoRoot, "name: \"my_app\"\n")

	if det := DetectDart(repoRoot); det.ProjectName != "my_app" {
		t.Errorf("ProjectName = %q, want my_app", det.ProjectName)
	}
}

func TestDetectDartIgnoresNestedAndSuffixLines(t *testing.T) {
	repoRoot := t.TempDir()
	writePubspec(t, repoRoot, "dependencies:\n  name_provider:\n    name: nested\n")

	if det := DetectDart(repoRoot); det.ProjectName != "" {
		t.Errorf("ProjectName = %q, want empty (only top-level name counts)", det.ProjectName)
	}
}

func TestDetectDartWithoutPubspecUnconfident(t *testing.T) {
	repoRoot := t.TempDir()
	det := DetectDart(repoRoot)
	if det.Confident {
		t.Error("Confident = true without pubspec.yaml")
	}
	if det.Language != "dart" || det.ProjectName != "" {
		t.Errorf("Detection = %+v, want unconfident empty dart detection", det)
	}
}

func TestDetectDartPubspecWithoutNameLineStillConfident(t *testing.T) {
	repoRoot := t.TempDir()
	writePubspec(t, repoRoot, "environment:\n  sdk: '>=3.0.0'\n")

	det := DetectDart(repoRoot)
	if !det.Confident {
		t.Error("pubspec presence alone should make detection confident")
	}
	if det.ProjectName != "" {
		t.Errorf("ProjectName = %q, want empty", det.ProjectName)
	}
}

func TestDetectFallsBackToUnknown(t *testing.T) {
	repoRoot := t.TempDir()
	det := Detect(repoRoot)
	if det.Confident {
		t.Error("empty repo should be unconfident")
	}
	if det.Language != "dart" {
		t.Errorf("Language = %q, want dart probe result (unconfident)", det.Language)
	}
}

func TestParsePubspecNameHandlesCommentAndWhitespace(t *testing.T) {
	tests := map[string]string{
		"name: foo_bar   # trailing comment\n": "foo_bar",
		"name:    spaced\n":                    "spaced",
		"name: 'quoted'\n":                     "quoted",
		"\n\nname: later\n":                    "later",
		"namename: x\n":                        "",
		"description: name-like\n":             "",
	}
	for content, want := range tests {
		if got := ParsePubspecName([]byte(content)); got != want {
			t.Errorf("ParsePubspecName(%q) = %q, want %q", content, got, want)
		}
	}
}
