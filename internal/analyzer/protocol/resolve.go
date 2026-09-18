package protocol

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"codeflow/internal/analyzer/installstate"
)

// Adapter environment variable names and defaults.
const (
	DartAdapterEnvVar       = "CODEFLOW_ADAPTER_DART_BIN"
	TypeScriptAdapterEnvVar = "CODEFLOW_ADAPTER_TYPESCRIPT_BIN"
	TSAdapterEnvVar         = "CODEFLOW_ADAPTER_TS_BIN"
	GoAdapterEnvVar         = "CODEFLOW_ADAPTER_GO_BIN"
	KotlinAdapterEnvVar     = "CODEFLOW_ADAPTER_KOTLIN_BIN"
	SwiftAdapterEnvVar      = "CODEFLOW_ADAPTER_SWIFT_BIN"
	PythonAdapterEnvVar     = "CODEFLOW_ADAPTER_PYTHON_BIN"
)

const (
	dartrunScheme    = "dartrun:"
	dartEntrypoint   = "bin/codeflow_dart_adapter.dart"
	noderunScheme    = "noderun:"
	tsrunScheme      = "tsrun:"
	gorunScheme      = "gorun:"
	tsEntrypointJS   = "bin/codeflow_ts_adapter.js"
	tsEntrypointTS   = "bin/codeflow_ts_adapter.ts"
	goAdapterDir     = "adapters/go"
	defaultLibSubdir = "lib"
)

// ResolveAdapter turns a language identifier and optional spec into a Config.
// It preserves the historical cwd-based checkout fallback for callers that do
// not know the target repository root.
func ResolveAdapter(lang string, spec string) (Config, error) {
	cwd, _ := os.Getwd()
	return ResolveAdapterForRepo("", cwd, lang, spec)
}

// ResolveAdapterForRepo resolves the adapter the same way as ResolveAdapter
// but anchors the bundled-checkout fallback at the target repository and the
// running binary instead of only the process cwd.
func ResolveAdapterForRepo(repoRoot, cwd, lang string, spec string) (Config, error) {
	lang = strings.ToLower(strings.TrimSpace(lang))
	if lang == "" {
		lang = "dart"
	}
	spec = strings.TrimSpace(spec)

	// 1. Check environment variables if spec is empty
	if spec == "" {
		switch lang {
		case "dart":
			spec = strings.TrimSpace(os.Getenv(DartAdapterEnvVar))
		case "typescript", "javascript":
			spec = strings.TrimSpace(os.Getenv(TypeScriptAdapterEnvVar))
			if spec == "" {
				spec = strings.TrimSpace(os.Getenv(TSAdapterEnvVar))
			}
		case "go":
			spec = strings.TrimSpace(os.Getenv(GoAdapterEnvVar))
		case "kotlin", "java":
			spec = strings.TrimSpace(os.Getenv(KotlinAdapterEnvVar))
		case "swift":
			spec = strings.TrimSpace(os.Getenv(SwiftAdapterEnvVar))
		case "python":
			spec = strings.TrimSpace(os.Getenv(PythonAdapterEnvVar))
		}
	}

	// 2. Check workspace adapter directories if running from checkout.
	if spec == "" {
		searchRoots := make([]string, 0, 4)
		seenRoots := map[string]bool{}
		addRoot := func(root string) {
			if root == "" || seenRoots[root] {
				return
			}
			seenRoots[root] = true
			searchRoots = append(searchRoots, root)
		}
		if abs, err := filepath.Abs(repoRoot); err == nil && repoRoot != "" {
			addRoot(abs)
		}
		if cwd != "" {
			if abs, err := filepath.Abs(cwd); err == nil {
				addRoot(abs)
			} else {
				addRoot(cwd)
			}
		} else if wd, err := os.Getwd(); err == nil {
			addRoot(wd)
		}
		if exe, err := os.Executable(); err == nil && exe != "" {
			if resolved, err := filepath.EvalSymlinks(exe); err == nil && resolved != "" {
				exe = resolved
			}
			exeDir := filepath.Dir(exe)
			addRoot(exeDir)
			addRoot(filepath.Dir(exeDir))
			addRoot(filepath.Join(exeDir, ".."))
		}
		for _, root := range searchRoots {
			if root == "" {
				continue
			}
			if found, ok := findBundledAdapterSpec(root, lang); ok {
				spec = found
				break
			}
		}
	}

	// 3. Check install state (only for dart if recorded)
	if spec == "" && lang == "dart" {
		if state, err := installstate.Load(); err == nil {
			spec = strings.TrimSpace(state.AdapterSpec)
		}
	}

	// 4. Check default user local bin ($HOME/.local/bin/codeflow_<lang>_adapter)
	if spec == "" {
		home, err := os.UserHomeDir()
		if err == nil {
			candidates := []string{
				filepath.Join(home, ".local", "bin", fmt.Sprintf("codeflow_%s_adapter", lang)),
			}
			if lang == "dart" {
				candidates = append(candidates,
					filepath.Join(home, ".local", "bin", "dart-adapter"),
					filepath.Join(home, ".local", "bin", "codeflow_dart_adapter"),
				)
			}
			if lang == "typescript" || lang == "javascript" {
				candidates = append(candidates,
					filepath.Join(home, ".local", "bin", "codeflow_ts_adapter"),
					filepath.Join(home, ".local", "bin", "codeflow_typescript_adapter"),
				)
			}
			for _, cand := range candidates {
				if info, err := os.Stat(cand); err == nil && !info.IsDir() {
					spec = cand
					break
				}
			}
		}
	}

	// 5. Check installed adapter share directory ($HOME/.local/share/codeflow/adapters/...)
	if spec == "" && (lang == "typescript" || lang == "javascript") {
		home, err := os.UserHomeDir()
		if err == nil {
			installedTS := filepath.Join(home, ".local", "share", "codeflow", "adapters", "typescript")
			if info, err := os.Stat(filepath.Join(installedTS, tsEntrypointJS)); err == nil && !info.IsDir() {
				spec = noderunScheme + installedTS
			}
		}
	}

	if spec == "" {
		return Config{}, fmt.Errorf(
			"no %s adapter configured: set CODEFLOW_ADAPTER_%s_BIN to an adapter binary path, or run install.sh",
			lang, strings.ToUpper(lang))
	}

	// Handle dartrun: scheme
	if dir, ok := strings.CutPrefix(spec, dartrunScheme); ok {
		if !filepath.IsAbs(dir) {
			return Config{}, fmt.Errorf("%s needs an absolute package-directory path: %q", dartrunScheme, spec)
		}
		entry := filepath.Join(dir, filepath.FromSlash(dartEntrypoint))
		if info, err := os.Stat(entry); err != nil || info.IsDir() {
			return Config{}, fmt.Errorf("adapter entrypoint %s not found", entry)
		}
		dartBin, err := findExecutable("dart")
		if err != nil {
			return Config{}, fmt.Errorf("the dart SDK executable must be on PATH: %v", err)
		}
		return Config{BinPath: dartBin, Args: []string{"run", entry}}, nil
	}

	// Handle noderun: scheme
	if dir, ok := strings.CutPrefix(spec, noderunScheme); ok {
		if !filepath.IsAbs(dir) {
			return Config{}, fmt.Errorf("%s needs an absolute package-directory path: %q", noderunScheme, spec)
		}
		entry := filepath.Join(dir, filepath.FromSlash(tsEntrypointJS))
		if info, err := os.Stat(entry); err != nil || info.IsDir() {
			return Config{}, fmt.Errorf("adapter entrypoint %s not found", entry)
		}
		nodeBin, err := findExecutable("node")
		if err != nil {
			return Config{}, fmt.Errorf("node must be on PATH: %v", err)
		}
		return Config{BinPath: nodeBin, Args: []string{entry}}, nil
	}

	// Handle tsrun: scheme (via tsx or bun or ts-node)
	if dir, ok := strings.CutPrefix(spec, tsrunScheme); ok {
		if !filepath.IsAbs(dir) {
			return Config{}, fmt.Errorf("%s needs an absolute package-directory path: %q", tsrunScheme, spec)
		}
		entry := filepath.Join(dir, filepath.FromSlash(tsEntrypointTS))
		if info, err := os.Stat(entry); err != nil || info.IsDir() {
			return Config{}, fmt.Errorf("adapter entrypoint %s not found", entry)
		}
		if tsxBin, err := findExecutable("tsx"); err == nil {
			return Config{BinPath: tsxBin, Args: []string{entry}}, nil
		}
		if bunBin, err := findExecutable("bun"); err == nil {
			return Config{BinPath: bunBin, Args: []string{"run", entry}}, nil
		}
		nodeBin, err := findExecutable("node")
		if err != nil {
			return Config{}, fmt.Errorf("node or tsx must be on PATH: %v", err)
		}
		return Config{BinPath: nodeBin, Args: []string{entry}}, nil
	}

	// Handle gorun: scheme for the repository-local native Go adapter.
	if dir, ok := strings.CutPrefix(spec, gorunScheme); ok {
		if !filepath.IsAbs(dir) {
			return Config{}, fmt.Errorf("%s needs an absolute adapter-directory path: %q", gorunScheme, spec)
		}
		if info, err := os.Stat(filepath.Join(dir, "main.go")); err != nil || info.IsDir() {
			return Config{}, fmt.Errorf("Go adapter entrypoint %s not found", filepath.Join(dir, "main.go"))
		}
		goBin, err := findExecutable("go")
		if err != nil {
			return Config{}, fmt.Errorf("go must be on PATH: %v", err)
		}
		return Config{BinPath: goBin, Args: []string{"run", "-C", dir, dir}}, nil
	}

	if !filepath.IsAbs(spec) {
		return Config{}, fmt.Errorf("adapter path must be absolute; got %q", spec)
	}
	if info, err := os.Stat(spec); err != nil || info.IsDir() {
		return Config{}, fmt.Errorf("adapter binary %q is not an executable file", spec)
	}
	return Config{BinPath: spec}, nil
}

// findBundledAdapterSpec looks for the bundled <checkout>/adapters tree at
// root or up to 8 levels above it.
func findBundledAdapterSpec(root, lang string) (string, bool) {
	dir := root
	for depth := 0; depth <= 8; depth++ {
		switch lang {
		case "dart":
			dartDir := filepath.Join(dir, "adapters", "dart")
			if info, err := os.Stat(filepath.Join(dartDir, dartEntrypoint)); err == nil && !info.IsDir() {
				return dartrunScheme + dartDir, true
			}
		case "typescript", "javascript":
			tsDir := filepath.Join(dir, "adapters", "typescript")
			if info, err := os.Stat(filepath.Join(tsDir, tsEntrypointJS)); err == nil && !info.IsDir() {
				return noderunScheme + tsDir, true
			}
			if info, err := os.Stat(filepath.Join(tsDir, tsEntrypointTS)); err == nil && !info.IsDir() {
				return tsrunScheme + tsDir, true
			}
		case "go":
			goDir := filepath.Join(dir, goAdapterDir)
			if info, err := os.Stat(filepath.Join(goDir, "main.go")); err == nil && !info.IsDir() {
				if _, err := findExecutable("go"); err == nil {
					return gorunScheme + goDir, true
				}
				return "", false
			}
		default:
			return "", false
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
	return "", false
}

// findExecutable looks for an executable on PATH, falling back to standard
// Homebrew, NVM, Flutter, and local binary locations.
func findExecutable(name string) (string, error) {
	if bin, err := exec.LookPath(name); err == nil {
		return bin, nil
	}

	home, _ := os.UserHomeDir()
	var candidates []string

	switch name {
	case "node":
		candidates = append(candidates,
			"/opt/homebrew/bin/node",
			"/usr/local/bin/node",
		)
		if home != "" {
			candidates = append(candidates,
				filepath.Join(home, ".local", "bin", "node"),
				filepath.Join(home, ".local", "share", "fnm", "current", "bin", "node"),
				filepath.Join(home, ".asdf", "shims", "node"),
				filepath.Join(home, ".volta", "bin", "node"),
			)
			nvmDir := filepath.Join(home, ".nvm", "versions", "node")
			if entries, err := os.ReadDir(nvmDir); err == nil {
				for i := len(entries) - 1; i >= 0; i-- {
					if entries[i].IsDir() {
						candidates = append(candidates, filepath.Join(nvmDir, entries[i].Name(), "bin", "node"))
					}
				}
			}
		}

	case "dart":
		candidates = append(candidates,
			"/opt/homebrew/bin/dart",
			"/usr/local/bin/dart",
		)
		if home != "" {
			candidates = append(candidates,
				filepath.Join(home, ".local", "bin", "dart"),
				filepath.Join(home, ".pub-cache", "bin", "dart"),
				filepath.Join(home, "fvm", "default", "bin", "dart"),
				filepath.Join(home, "flutter", "bin", "dart"),
				filepath.Join(home, "Library", "flutter", "bin", "dart"),
				filepath.Join(home, "development", "flutter", "bin", "dart"),
			)
		}

	case "go":
		candidates = append(candidates,
			"/opt/homebrew/bin/go",
			"/usr/local/bin/go",
			"/usr/local/go/bin/go",
		)
		if home != "" {
			candidates = append(candidates,
				filepath.Join(home, ".local", "bin", "go"),
				filepath.Join(home, "go", "bin", "go"),
				filepath.Join(home, ".asdf", "shims", "go"),
			)
		}

	case "tsx":
		candidates = append(candidates,
			"/opt/homebrew/bin/tsx",
			"/usr/local/bin/tsx",
		)
		if home != "" {
			candidates = append(candidates,
				filepath.Join(home, ".local", "bin", "tsx"),
				filepath.Join(home, ".asdf", "shims", "tsx"),
			)
		}

	case "bun":
		candidates = append(candidates,
			"/opt/homebrew/bin/bun",
			"/usr/local/bin/bun",
		)
		if home != "" {
			candidates = append(candidates,
				filepath.Join(home, ".bun", "bin", "bun"),
				filepath.Join(home, ".local", "bin", "bun"),
			)
		}
	}

	for _, cand := range candidates {
		if info, err := os.Stat(cand); err == nil && !info.IsDir() {
			if (info.Mode() & 0o111) != 0 {
				return cand, nil
			}
		}
	}

	return "", fmt.Errorf("%s executable not found in PATH or standard runtime locations", name)
}

// ResolveDartAdapter turns a $CODEFLOW_ADAPTER_DART_BIN value into a Config.
func ResolveDartAdapter(spec string) (Config, error) {
	return ResolveAdapter("dart", spec)
}
