package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
)

func TestCodeflowStatusSubcommand(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "codeflow-status-cli-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	runStatus([]string{tempDir, "--json"})

	w.Close()
	os.Stdout = oldStdout

	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)

	var doc map[string]any
	if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatalf("failed to parse status JSON: %v, raw: %s", err, buf.String())
	}

	if doc["activity"] != "idle" {
		t.Errorf("expected initial activity idle, got %v", doc["activity"])
	}
	if doc["workspaceEpoch"] == "" {
		t.Error("expected non-empty workspaceEpoch")
	}

	// Also test text output
	rText, wText, _ := os.Pipe()
	os.Stdout = wText

	runStatus([]string{tempDir})

	wText.Close()
	os.Stdout = oldStdout

	var bufText bytes.Buffer
	_, _ = io.Copy(&bufText, rText)
	textOut := bufText.String()

	if !strings.Contains(textOut, "CodeFlow Workspace Status") {
		t.Errorf("expected header in status output, got %s", textOut)
	}
	if !strings.Contains(textOut, "Workspace Epoch:") {
		t.Errorf("expected Workspace Epoch: in status output, got %s", textOut)
	}
}
