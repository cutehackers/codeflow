package installation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSkillMatches(t *testing.T) {
	skillPath := filepath.Join(t.TempDir(), "codeflow")
	if err := os.Mkdir(skillPath, 0o755); err != nil {
		t.Fatal(err)
	}
	contents := []byte("skill contents\n")
	if err := os.WriteFile(filepath.Join(skillPath, "SKILL.md"), contents, 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(contents)
	want := hex.EncodeToString(sum[:])
	matched, err := skillMatches(skillPath, want)
	if err != nil || !matched {
		t.Fatalf("skillMatches = %t, %v; want true, nil", matched, err)
	}
	matched, err = skillMatches(skillPath, "changed")
	if err != nil || matched {
		t.Fatalf("skillMatches changed = %t, %v; want false, nil", matched, err)
	}
}

func TestIsManagedSource(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), ".codeflow", "install-state.json")
	if !isManagedSource(filepath.Join(filepath.Dir(statePath), "src"), statePath) {
		t.Fatal("managed source was rejected")
	}
	if isManagedSource(filepath.Join(filepath.Dir(statePath), "other"), statePath) {
		t.Fatal("non-managed source was accepted")
	}
}

func TestUninstallMultiAgentCleanups(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)

	skillContent := []byte("test skill content\n")
	sum := sha256.Sum256(skillContent)
	skillSHA := hex.EncodeToString(sum[:])

	// 1. Setup multi-agent skill directories
	skillPaths := []string{
		filepath.Join(tempHome, ".claude", "skills", "codeflow"),
		filepath.Join(tempHome, ".gemini", "config", "skills", "codeflow"),
		filepath.Join(tempHome, ".gemini", "antigravity-cli", "skills", "codeflow"),
		filepath.Join(tempHome, ".cursor", "skills", "codeflow"),
	}
	for _, sp := range skillPaths {
		if err := os.MkdirAll(sp, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(sp, "SKILL.md"), skillContent, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// 2. Setup Antigravity MCP schemas
	mcpDir := filepath.Join(tempHome, ".gemini", "antigravity-cli", "mcp", "codeflow")
	if err := os.MkdirAll(mcpDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mcpDir, "harvest_flows.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	// 3. Setup Claude Code ~/.claude.json and Cursor ~/.cursor/mcp.json
	claudeJSONPath := filepath.Join(tempHome, ".claude.json")
	claudeJSONContent := fmt.Sprintf(`{
		"mcpServers": {
			"codeflow": {
				"command": %q,
				"args": ["mcp"]
			}
		}
	}`, filepath.Join(tempHome, ".local", "bin", "codeflow"))
	if err := os.WriteFile(claudeJSONPath, []byte(claudeJSONContent), 0o644); err != nil {
		t.Fatal(err)
	}

	cursorJSONPath := filepath.Join(tempHome, ".cursor", "mcp.json")
	if err := os.MkdirAll(filepath.Dir(cursorJSONPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cursorJSONPath, []byte(claudeJSONContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// 4. Setup workspace shared .agents
	srcRoot := t.TempDir()
	agentsSkill := filepath.Join(srcRoot, ".agents", "skills", "codeflow")
	if err := os.MkdirAll(agentsSkill, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentsSkill, "SKILL.md"), skillContent, 0o644); err != nil {
		t.Fatal(err)
	}

	// 5. Setup install state
	binPath := filepath.Join(tempHome, ".local", "bin", "codeflow")
	if err := os.MkdirAll(filepath.Dir(binPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	primarySkill := filepath.Join(tempHome, ".codex", "skills", "codeflow")
	if err := os.MkdirAll(primarySkill, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(primarySkill, "SKILL.md"), skillContent, 0o644); err != nil {
		t.Fatal(err)
	}

	stateDir := filepath.Join(tempHome, ".codeflow")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	stateJSON := fmt.Sprintf(`{
		"version": 1,
		"binary": %q,
		"sourceRoot": %q,
		"ownedSource": false,
		"skillPath": %q,
		"skillSHA256": %q,
		"mcpName": "codeflow"
	}`, binPath, srcRoot, primarySkill, skillSHA)
	if err := os.WriteFile(filepath.Join(stateDir, "install-state.json"), []byte(stateJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	// 6. Run Uninstall
	res, err := Uninstall(context.Background())
	if err != nil {
		t.Fatalf("Uninstall failed: %v", err)
	}
	if len(res.Removed) == 0 {
		t.Fatalf("expected items removed, got none")
	}

	// 7. Verify removals
	for _, sp := range skillPaths {
		if _, err := os.Stat(sp); !os.IsNotExist(err) {
			t.Errorf("skill %s was not removed", sp)
		}
	}
	if _, err := os.Stat(mcpDir); !os.IsNotExist(err) {
		t.Errorf("antigravity mcp schema dir was not removed")
	}
	if _, err := os.Stat(agentsSkill); !os.IsNotExist(err) {
		t.Errorf("shared workspace skill was not removed")
	}
	if _, err := os.Stat(primarySkill); !os.IsNotExist(err) {
		t.Errorf("primary skill was not removed")
	}

	// Verify ~/.claude.json and ~/.cursor/mcp.json cleaned
	if b, err := os.ReadFile(claudeJSONPath); err == nil {
		if strings.Contains(string(b), `"codeflow"`) {
			t.Errorf("codeflow entry survived in ~/.claude.json")
		}
	}
	if b, err := os.ReadFile(cursorJSONPath); err == nil {
		if strings.Contains(string(b), `"codeflow"`) {
			t.Errorf("codeflow entry survived in ~/.cursor/mcp.json")
		}
	}
}

func TestUninstallWithoutCodexAndWithEmptySkillHash(t *testing.T) {
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)
	t.Setenv("PATH", "/usr/bin:/bin") // Ensure codex is NOT in PATH

	skillContent := []byte("---\nname: codeflow\n---\n# CodeFlow Skill\n")

	// Create Gemini and Claude skills
	geminiSkill := filepath.Join(tempHome, ".gemini", "config", "skills", "codeflow")
	if err := os.MkdirAll(geminiSkill, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(geminiSkill, "SKILL.md"), skillContent, 0o644); err != nil {
		t.Fatal(err)
	}

	binPath := filepath.Join(tempHome, ".local", "bin", "codeflow")
	if err := os.MkdirAll(filepath.Dir(binPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	stateDir := filepath.Join(tempHome, ".codeflow")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Note empty skillSHA256 and no Codex skill
	stateJSON := fmt.Sprintf(`{
		"version": 1,
		"binary": %q,
		"sourceRoot": "",
		"ownedSource": false,
		"skillPath": "",
		"skillSHA256": "",
		"mcpName": "codeflow"
	}`, binPath)
	if err := os.WriteFile(filepath.Join(stateDir, "install-state.json"), []byte(stateJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := Uninstall(context.Background())
	if err != nil {
		t.Fatalf("Uninstall failed when codex is absent: %v", err)
	}
	if len(res.Removed) == 0 {
		t.Fatalf("expected items removed, got none")
	}

	if _, err := os.Stat(geminiSkill); !os.IsNotExist(err) {
		t.Errorf("gemini skill was not removed when skillSHA256 was empty")
	}
	if _, err := os.Stat(binPath); !os.IsNotExist(err) {
		t.Errorf("binary was not removed")
	}
}
