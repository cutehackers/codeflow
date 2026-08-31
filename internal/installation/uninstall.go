// Package installation owns removal of the user-scoped CodeFlow installation.
package installation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"codeflow/internal/installstate"
)

// UninstallResult explains exactly what was removed or deliberately retained.
type UninstallResult struct {
	Removed []string
	Kept    []string
}

// Uninstall only removes assets named in the installer ownership record. A
// changed skill or another MCP server using the same name is kept intact.
func Uninstall(ctx context.Context) (UninstallResult, error) {
	state, err := installstate.Load()
	if err != nil {
		if os.IsNotExist(err) {
			return UninstallResult{}, fmt.Errorf("CodeFlow installation was not found")
		}
		return UninstallResult{}, err
	}
	statePath, err := installstate.Path()
	if err != nil {
		return UninstallResult{}, err
	}
	result := UninstallResult{}

	// 1. Codex MCP registration
	mcpRemoved, err := removeOwnedMCP(ctx, state)
	if err != nil {
		return result, err
	}
	if mcpRemoved {
		result.Removed = append(result.Removed, "Codex MCP registration "+state.MCPName)
	}

	// 2. Primary skill removal
	if state.SkillPath != "" {
		if removed, kept := removeSkillIfMatches(state.SkillPath, state.SkillSHA256); removed {
			result.Removed = append(result.Removed, "CodeFlow skill")
		} else if kept {
			result.Kept = append(result.Kept, "CodeFlow skill (changed after installation)")
		}
	}

	// 3. Multi-agent JSON MCP & Skills cleanup (Claude Desktop, Cursor, Antigravity)
	home, _ := os.UserHomeDir()
	if home != "" {
		// Claude Desktop
		var claudeConfig string
		if runtime.GOOS == "darwin" {
			claudeConfig = filepath.Join(home, "Library", "Application Support", "Claude", "claude_desktop_config.json")
		} else {
			claudeConfig = filepath.Join(home, ".config", "Claude", "claude_desktop_config.json")
		}
		if removed, _ := removeOwnedJSONMCP(claudeConfig, state.MCPName, state.Binary); removed {
			result.Removed = append(result.Removed, "Claude Desktop MCP registration "+state.MCPName)
		}

		// Cursor
		cursorConfig := filepath.Join(home, ".cursor", "mcp.json")
		if removed, _ := removeOwnedJSONMCP(cursorConfig, state.MCPName, state.Binary); removed {
			result.Removed = append(result.Removed, "Cursor MCP registration "+state.MCPName)
		}
		cursorSkill := filepath.Join(home, ".cursor", "skills", "codeflow")
		if removed, _ := removeSkillIfMatches(cursorSkill, state.SkillSHA256); removed {
			result.Removed = append(result.Removed, "Cursor skill")
		}

		// Antigravity
		antigravityConfig := filepath.Join(home, ".gemini", "config", "mcp_config.json")
		if removed, _ := removeOwnedJSONMCP(antigravityConfig, state.MCPName, state.Binary); removed {
			result.Removed = append(result.Removed, "Antigravity MCP registration "+state.MCPName)
		}
		antigravitySkill := filepath.Join(home, ".gemini", "antigravity-cli", "skills", "codeflow")
		if removed, _ := removeSkillIfMatches(antigravitySkill, state.SkillSHA256); removed {
			result.Removed = append(result.Removed, "Antigravity skill")
		}
	}

	if state.OwnedSource && isManagedSource(state.SourceRoot, statePath) {
		if err := os.RemoveAll(state.SourceRoot); err != nil {
			return result, fmt.Errorf("remove managed source: %w", err)
		}
		result.Removed = append(result.Removed, "managed source")
	} else if state.SourceRoot != "" {
		result.Kept = append(result.Kept, "source checkout (not installer-owned)")
	}

	if state.AdapterSpec != "" && !strings.HasPrefix(state.AdapterSpec, "dartrun:") && filepath.IsAbs(state.AdapterSpec) {
		if err := os.Remove(state.AdapterSpec); err != nil && !os.IsNotExist(err) {
			return result, fmt.Errorf("remove adapter binary: %w", err)
		}
		result.Removed = append(result.Removed, "adapter binary")
	}

	if home != "" {
		extraBinaries := []string{
			filepath.Join(home, ".local", "bin", "codeflow_dart_adapter"),
			filepath.Join(home, ".local", "bin", "codeflow_ts_adapter"),
			filepath.Join(home, ".local", "bin", "codeflow_typescript_adapter"),
		}
		for _, eb := range extraBinaries {
			if err := os.Remove(eb); err == nil {
				result.Removed = append(result.Removed, filepath.Base(eb))
			}
		}
		tsLibDir := filepath.Join(home, ".local", "share", "codeflow", "adapters", "typescript")
		if err := os.RemoveAll(tsLibDir); err == nil {
			result.Removed = append(result.Removed, "typescript adapter library")
			_ = os.Remove(filepath.Join(home, ".local", "share", "codeflow", "adapters"))
			_ = os.Remove(filepath.Join(home, ".local", "share", "codeflow"))
		}
	}

	if err := os.Remove(state.Binary); err != nil && !os.IsNotExist(err) {
		return result, fmt.Errorf("remove binary: %w", err)
	}
	result.Removed = append(result.Removed, "codeflow binary")
	if err := os.Remove(statePath); err != nil && !os.IsNotExist(err) {
		return result, fmt.Errorf("remove installation record: %w", err)
	}
	if home != "" {
		_ = os.Remove(filepath.Join(home, ".codeflow"))
	}
	return result, nil
}

func removeOwnedMCP(ctx context.Context, state installstate.State) (bool, error) {
	codex, err := exec.LookPath("codex")
	if err != nil {
		return false, fmt.Errorf("remove MCP registration: Codex CLI was not found")
	}
	get := exec.CommandContext(ctx, codex, "mcp", "get", state.MCPName, "--json")
	out, err := get.CombinedOutput()
	if err != nil {
		message := strings.ToLower(string(out))
		if strings.Contains(message, "not found") || strings.Contains(message, "no mcp server") {
			return false, nil
		}
		return false, fmt.Errorf("inspect MCP registration %q: %s", state.MCPName, strings.TrimSpace(string(out)))
	}
	if !strings.Contains(string(out), state.Binary) {
		return false, fmt.Errorf("MCP registration %q is no longer owned by this CodeFlow install; refusing to remove it", state.MCPName)
	}
	remove := exec.CommandContext(ctx, codex, "mcp", "remove", state.MCPName)
	if out, err := remove.CombinedOutput(); err != nil {
		return false, fmt.Errorf("remove MCP registration %q: %s", state.MCPName, strings.TrimSpace(string(out)))
	}
	return true, nil
}

func skillMatches(skillPath, want string) (bool, error) {
	if want == "" {
		return false, nil
	}
	b, err := os.ReadFile(filepath.Join(skillPath, "SKILL.md"))
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]) == want, nil
}

func removeSkillIfMatches(skillPath, wantHash string) (bool, bool) {
	if skillPath == "" {
		return false, false
	}
	if _, err := os.Stat(skillPath); os.IsNotExist(err) {
		return false, false
	}
	matches, err := skillMatches(skillPath, wantHash)
	if err != nil || !matches {
		return false, true
	}
	if err := os.RemoveAll(skillPath); err == nil {
		return true, false
	}
	return false, true
}

func removeOwnedJSONMCP(jsonPath string, mcpName string, binaryPath string) (bool, error) {
	b, err := os.ReadFile(jsonPath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	var data map[string]any
	if err := json.Unmarshal(b, &data); err != nil {
		return false, nil
	}
	serversRaw, ok := data["mcpServers"]
	if !ok {
		return false, nil
	}
	servers, ok := serversRaw.(map[string]any)
	if !ok {
		return false, nil
	}
	entryRaw, ok := servers[mcpName]
	if !ok {
		return false, nil
	}
	entry, ok := entryRaw.(map[string]any)
	if ok {
		cmdStr, _ := entry["command"].(string)
		if binaryPath != "" && cmdStr != "" && !strings.Contains(cmdStr, filepath.Base(binaryPath)) && cmdStr != binaryPath {
			return false, nil
		}
	}
	delete(servers, mcpName)
	data["mcpServers"] = servers
	outBytes, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return false, err
	}
	if err := os.WriteFile(jsonPath, append(outBytes, '\n'), 0o644); err != nil {
		return false, err
	}
	return true, nil
}

func isManagedSource(sourceRoot, statePath string) bool {
	if sourceRoot == "" {
		return false
	}
	managedRoot := filepath.Join(filepath.Dir(statePath), "src")
	rel, err := filepath.Rel(managedRoot, sourceRoot)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
