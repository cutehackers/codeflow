package codeflowplugin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPackagedMCPConfigDelegatesAllPublicTools(t *testing.T) {
	root := must(t)
	b, err := os.ReadFile(filepath.Join(root, ".mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		MCPServers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	s := v.MCPServers["codeflow"]
	if s.Command != "sh" || len(s.Args) != 4 || s.Args[0] != "-c" || !strings.Contains(s.Args[1], "CODEFLOW_BIN") || !strings.Contains(s.Args[1], "$HOME/.codeflow/bin/codeflow") || s.Args[2] != "codeflow-mcp" || s.Args[3] != "${workspaceFolder}" {
		t.Fatalf("%#v", s)
	}
	skill, err := os.ReadFile(filepath.Join(root, "skills/codeflow/SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range []string{"current", "diff", "unknowns", "open", "refresh"} {
		if !strings.Contains(string(skill), tool) {
			t.Fatalf("missing %s", tool)
		}
	}
}
func TestSessionHookFailureHasNoPersistedFlowOutput(t *testing.T) {
	root := must(t)
	hook, err := os.ReadFile(filepath.Join(root, "scripts/session-refresh.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(hook), `"refresh"`) || strings.Contains(string(hook), "current") {
		t.Fatalf("hook=%s", hook)
	}
}

func must(t *testing.T) string {
	t.Helper()
	p, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return p
}
