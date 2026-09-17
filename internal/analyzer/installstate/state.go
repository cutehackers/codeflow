// Package installstate records the small set of user-scoped files created by
// the one-shot installer. It is deliberately separate from project state:
// removing CodeFlow must never touch a repository's .codeflow directory.
package installstate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const fileName = "install-state.json"

// State is the ownership record used by `codeflow uninstall`.
type State struct {
	Version     int    `json:"version"`
	Binary      string `json:"binary"`
	SourceRoot  string `json:"sourceRoot"`
	OwnedSource bool   `json:"ownedSource"`
	AdapterSpec string `json:"adapterSpec"`
	SkillPath   string `json:"skillPath"`
	SkillSHA256 string `json:"skillSHA256"`
	MCPName     string `json:"mcpName"`
}

func Path() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, ".codeflow", fileName), nil
}

func Load() (State, error) {
	path, err := Path()
	if err != nil {
		return State{}, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return State{}, err
	}
	var state State
	if err := json.Unmarshal(b, &state); err != nil {
		return State{}, fmt.Errorf("read installation state: %w", err)
	}
	if state.Version != 1 || state.Binary == "" || state.MCPName == "" {
		return State{}, fmt.Errorf("read installation state: unsupported or incomplete record")
	}
	return state, nil
}

func Save(state State) error {
	path, err := Path()
	if err != nil {
		return err
	}
	if state.Version != 1 || state.Binary == "" || state.MCPName == "" {
		return fmt.Errorf("save installation state: incomplete record")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o600)
}
