// Package installation owns the deliberate, user-scoped CodeFlow setup step.
package installation

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	MarketplaceName = "codeflow-local"
	PluginName      = "codeflow"
)

type Options struct {
	SourceRoot string
	HomeDir    string
	LookPath   func(string) (string, error)
	Run        func(context.Context, string, ...string) ([]byte, error)
}

type Result struct {
	Root       string
	Executable string
}

// Install copies the self-contained bundle to one predictable user-owned
// location, then registers its included Codex marketplace and plugin. No
// product repository is inspected or modified.
func Install(ctx context.Context, options Options) (Result, error) {
	if options.LookPath == nil {
		options.LookPath = exec.LookPath
	}
	if options.Run == nil {
		options.Run = run
	}
	root, err := filepath.Abs(options.SourceRoot)
	if err != nil {
		return Result{}, err
	}
	if options.HomeDir == "" {
		options.HomeDir, err = os.UserHomeDir()
		if err != nil {
			return Result{}, fmt.Errorf("find home directory: %w", err)
		}
	}
	destination := filepath.Join(options.HomeDir, ".codeflow")
	components := []struct{ from, to string }{
		{filepath.Join(root, "bin", "codeflow"), filepath.Join(destination, "bin", "codeflow")},
		{filepath.Join(root, "libexec", "codeflow-dart-adapter"), filepath.Join(destination, "libexec", "codeflow-dart-adapter")},
		{filepath.Join(root, "libexec", "compatibility.json"), filepath.Join(destination, "libexec", "compatibility.json")},
		{filepath.Join(root, ".agents", "plugins", "marketplace.json"), filepath.Join(destination, ".agents", "plugins", "marketplace.json")},
	}
	for _, component := range components {
		if err := copyFile(component.from, component.to); err != nil {
			return Result{}, fmt.Errorf("package is incomplete (%s): %w", filepath.Base(component.from), err)
		}
	}
	pluginSource := filepath.Join(root, "plugins", PluginName)
	pluginDestination := filepath.Join(destination, "plugins", PluginName)
	if err := copyTree(pluginSource, pluginDestination); err != nil {
		return Result{}, fmt.Errorf("package plugin is incomplete: %w", err)
	}
	codex, err := options.LookPath("codex")
	if err != nil {
		return Result{}, fmt.Errorf("Codex CLI was not found; install Codex, then run `codeflow install` again")
	}
	if output, err := options.Run(ctx, codex, "plugin", "marketplace", "add", destination); err != nil && !alreadyConfigured(output) {
		return Result{}, fmt.Errorf("register CodeFlow marketplace: %s", commandError(err, output))
	}
	if output, err := options.Run(ctx, codex, "plugin", "add", PluginName+"@"+MarketplaceName); err != nil && !alreadyInstalled(output) {
		return Result{}, fmt.Errorf("activate CodeFlow plugin: %s", commandError(err, output))
	}
	return Result{Root: destination, Executable: filepath.Join(destination, "bin", "codeflow")}, nil
}

func copyTree(source, destination string) error {
	info, err := os.Stat(source)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", source)
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
		return err
	}
	staging, err := os.MkdirTemp(filepath.Dir(destination), ".codeflow-plugin-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(staging)
	stagedPlugin := filepath.Join(staging, PluginName)
	err = filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(stagedPlugin, relative)
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("symbolic links are not supported: %s", relative)
		}
		if entry.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		return copyFile(path, target)
	})
	if err != nil {
		return err
	}
	if err := os.RemoveAll(destination); err != nil {
		return err
	}
	return os.Rename(stagedPlugin, destination)
}

func copyFile(source, destination string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	info, err := input.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("not a regular file")
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(destination), ".codeflow-")
	if err != nil {
		return err
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if _, err := io.Copy(temporary, input); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Chmod(info.Mode().Perm()); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryName, destination)
}

func alreadyConfigured(output []byte) bool {
	message := strings.ToLower(string(output))
	return strings.Contains(message, "already configured") || strings.Contains(message, "already exists")
}

func alreadyInstalled(output []byte) bool {
	message := strings.ToLower(string(output))
	return strings.Contains(message, "already installed")
}

func commandError(err error, output []byte) string {
	message := strings.TrimSpace(string(output))
	if message == "" {
		return err.Error()
	}
	return message
}

func run(ctx context.Context, command string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, command, args...).CombinedOutput()
}
