package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadValidV1Configuration(t *testing.T) {
	repo := t.TempDir()
	writeConfig(t, repo, `schema_version: "1"
repository:
  id: demo
analysis:
  include: ["lib/**"]
  exclude: ["**/build/**"]
features:
  signup:
    entry_point: "route:/signup"
  profile:
    entry_point: "symbol:package:demo/profile.dart::Profile"
extra: ignored
`)
	result, err := Load(repo)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Present || result.Config.Features["signup"].EntryPoint != "route:/signup" {
		t.Fatalf("unexpected config: %#v", result)
	}
	if len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], "extra") {
		t.Fatalf("warnings = %#v", result.Warnings)
	}
}

func TestLoadAllowsNoConfiguration(t *testing.T) {
	result, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if result.Present {
		t.Fatal("missing config must be supported")
	}
}

func TestLoadRejectsUnsupportedSchemaAndInvalidEntryPoint(t *testing.T) {
	repo := t.TempDir()
	writeConfig(t, repo, "schema_version: \"2\"\nrepository: {id: demo}\n")
	if _, err := Load(repo); err == nil || !strings.Contains(err.Error(), "unsupported schema_version") {
		t.Fatalf("err = %v", err)
	}
	writeConfig(t, repo, "schema_version: \"1\"\nrepository: {id: demo}\nfeatures: {signup: {entry_point: signup}}\n")
	if _, err := Load(repo); err == nil || !strings.Contains(err.Error(), "logical entry point") {
		t.Fatalf("err = %v", err)
	}
}

func writeConfig(t *testing.T, repo, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(repo, "codeflow.yaml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
