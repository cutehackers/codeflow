// Package installation owns removal of the user-scoped CodeFlow installation.
package installation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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

	mcpRemoved, err := removeOwnedMCP(ctx, state)
	if err != nil {
		return result, err
	}
	if mcpRemoved {
		result.Removed = append(result.Removed, "Codex MCP registration "+state.MCPName)
	}

	if state.SkillPath != "" {
		matches, err := skillMatches(state.SkillPath, state.SkillSHA256)
		if err != nil {
			return result, err
		}
		if matches {
			if err := os.RemoveAll(state.SkillPath); err != nil {
				return result, fmt.Errorf("remove managed skill: %w", err)
			}
			result.Removed = append(result.Removed, "CodeFlow skill")
		} else {
			result.Kept = append(result.Kept, "CodeFlow skill (changed after installation)")
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

	if err := os.Remove(state.Binary); err != nil && !os.IsNotExist(err) {
		return result, fmt.Errorf("remove binary: %w", err)
	}
	result.Removed = append(result.Removed, "codeflow binary")
	if err := os.Remove(statePath); err != nil && !os.IsNotExist(err) {
		return result, fmt.Errorf("remove installation record: %w", err)
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

func isManagedSource(sourceRoot, statePath string) bool {
	if sourceRoot == "" {
		return false
	}
	managedRoot := filepath.Join(filepath.Dir(statePath), "src")
	rel, err := filepath.Rel(managedRoot, sourceRoot)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
