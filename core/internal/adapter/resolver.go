// Package adapter resolves the owned Dart adapter from a CodeFlow installation.
package adapter

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// Resolution records the command and its discovery source. Command is safe to
// pass to the compiler's adapter process boundary.
type Resolution struct {
	Command string
	Path    string
	Source  string
}

// Resolve uses one precedence order for every public CodeFlow command. A
// packaged adapter belongs beside bin/codeflow, so a normal installation never
// needs PATH or CODEFLOW_DART_ADAPTER configuration.
func Resolve(explicit string) Resolution {
	if value := strings.TrimSpace(explicit); value != "" {
		return fromValue(value, "--adapter")
	}
	if value := strings.TrimSpace(os.Getenv("CODEFLOW_DART_ADAPTER")); value != "" {
		return fromValue(value, "CODEFLOW_DART_ADAPTER")
	}
	if executable, err := os.Executable(); err == nil {
		dir := filepath.Dir(executable)
		for _, candidate := range []string{
			filepath.Join(dir, "..", "libexec", "codeflow-dart-adapter"),
			filepath.Join(dir, "adapters", "dart", "bin", "codeflow-dart-adapter.dart"),
			filepath.Join(dir, "..", "adapters", "dart", "bin", "codeflow-dart-adapter.dart"),
		} {
			if exists(candidate) {
				return fromPath(candidate, "bundled installation")
			}
		}
	}
	// Keep source checkout commands and go run practical without making a
	// target repository supply an adapter path.
	if _, sourceFile, _, ok := runtime.Caller(0); ok {
		candidate := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "../../..", "adapters", "dart", "bin", "codeflow-dart-adapter.dart"))
		if exists(candidate) {
			return fromPath(candidate, "source checkout")
		}
	}
	if cwd, err := os.Getwd(); err == nil {
		candidate := filepath.Join(cwd, "adapters", "dart", "bin", "codeflow-dart-adapter.dart")
		if exists(candidate) {
			return fromPath(candidate, "working directory")
		}
	}
	if path, err := exec.LookPath("codeflow-dart-adapter"); err == nil {
		return fromPath(path, "PATH")
	}
	return Resolution{}
}

func fromValue(value, source string) Resolution {
	if strings.HasSuffix(value, ".dart") && exists(value) {
		return fromPath(value, source)
	}
	return Resolution{Command: value, Path: value, Source: source}
}

func fromPath(path, source string) Resolution {
	if strings.HasSuffix(path, ".dart") {
		return Resolution{Command: "dart " + strconv.Quote(path), Path: path, Source: source}
	}
	return Resolution{Command: path, Path: path, Source: source}
}

func exists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
