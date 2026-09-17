// Package harvest implements CORE-side Stage 1 orchestration (design-v2
// §4.2 Stage 1, §10, 결정 #6): it drives a language adapter over the
// persistent protocol pool (결정 #8), then scores candidates
// deterministically (마커 구체성 × 진입점 팬인 × 경계 도달성), applies
// root-equivalence dedup with tie-breakers (R11), and finally overlays the
// codeflow.flows.yaml manifest overrides (결정 #14) — which always win.
package harvest

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"codeflow/internal/analyzer/installstate"
	"codeflow/internal/analyzer/protocol"
	"codeflow/internal/collector/contractharness"
)

// Adapter env var prefix and defaults
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

// ResolveAdapter turns a language identifier and optional spec into a protocol.Config.
// It preserves the historical cwd-based checkout fallback for callers that do
// not know the target repository root.
func ResolveAdapter(lang string, spec string) (protocol.Config, error) {
	cwd, _ := os.Getwd()
	return ResolveAdapterForRepo("", cwd, lang, spec)
}

// ResolveAdapterForRepo resolves the adapter the same way as ResolveAdapter
// but anchors the bundled-checkout fallback at the target repository and the
// running binary instead of only the process cwd. `codeflow live <target>`
// must not depend on where the user invoked the binary from.
func ResolveAdapterForRepo(repoRoot, cwd, lang string, spec string) (protocol.Config, error) {
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

	// 2. Check install state (only for dart if recorded)
	if spec == "" && lang == "dart" {
		if state, err := installstate.Load(); err == nil {
			spec = strings.TrimSpace(state.AdapterSpec)
		}
	}

	// 3. Check default user local bin ($HOME/.local/bin/codeflow_<lang>_adapter)
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

	// 4. Check workspace adapter directories if running from checkout.
	// Anchor at the target repo first (self-hosted runs), then the process
	// cwd (historical behavior), then the running binary's install layout.
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
		return protocol.Config{}, fmt.Errorf(
			"no %s adapter configured: set CODEFLOW_ADAPTER_%s_BIN to an adapter binary path, or run install.sh",
			lang, strings.ToUpper(lang))
	}

	// Handle dartrun: scheme
	if dir, ok := strings.CutPrefix(spec, dartrunScheme); ok {
		if !filepath.IsAbs(dir) {
			return protocol.Config{}, fmt.Errorf("%s needs an absolute package-directory path: %q", dartrunScheme, spec)
		}
		entry := filepath.Join(dir, filepath.FromSlash(dartEntrypoint))
		if info, err := os.Stat(entry); err != nil || info.IsDir() {
			return protocol.Config{}, fmt.Errorf("adapter entrypoint %s not found", entry)
		}
		dartBin, err := findExecutable("dart")
		if err != nil {
			return protocol.Config{}, fmt.Errorf("the dart SDK executable must be on PATH: %v", err)
		}
		return protocol.Config{BinPath: dartBin, Args: []string{"run", entry}}, nil
	}

	// Handle noderun: scheme
	if dir, ok := strings.CutPrefix(spec, noderunScheme); ok {
		if !filepath.IsAbs(dir) {
			return protocol.Config{}, fmt.Errorf("%s needs an absolute package-directory path: %q", noderunScheme, spec)
		}
		entry := filepath.Join(dir, filepath.FromSlash(tsEntrypointJS))
		if info, err := os.Stat(entry); err != nil || info.IsDir() {
			return protocol.Config{}, fmt.Errorf("adapter entrypoint %s not found", entry)
		}
		nodeBin, err := findExecutable("node")
		if err != nil {
			return protocol.Config{}, fmt.Errorf("node must be on PATH: %v", err)
		}
		return protocol.Config{BinPath: nodeBin, Args: []string{entry}}, nil
	}

	// Handle tsrun: scheme (via tsx or bun or ts-node)
	if dir, ok := strings.CutPrefix(spec, tsrunScheme); ok {
		if !filepath.IsAbs(dir) {
			return protocol.Config{}, fmt.Errorf("%s needs an absolute package-directory path: %q", tsrunScheme, spec)
		}
		entry := filepath.Join(dir, filepath.FromSlash(tsEntrypointTS))
		if info, err := os.Stat(entry); err != nil || info.IsDir() {
			return protocol.Config{}, fmt.Errorf("adapter entrypoint %s not found", entry)
		}
		if tsxBin, err := findExecutable("tsx"); err == nil {
			return protocol.Config{BinPath: tsxBin, Args: []string{entry}}, nil
		}
		if bunBin, err := findExecutable("bun"); err == nil {
			return protocol.Config{BinPath: bunBin, Args: []string{"run", entry}}, nil
		}
		nodeBin, err := findExecutable("node")
		if err != nil {
			return protocol.Config{}, fmt.Errorf("node or tsx must be on PATH: %v", err)
		}
		return protocol.Config{BinPath: nodeBin, Args: []string{entry}}, nil
	}

	// Handle gorun: scheme for the repository-local native Go adapter.
	// The adapter subprocess runs with a disposable cwd (protocol.Spawn
	// isolation), so `go run <dir>` would resolve go.mod from the temp dir
	// and fail with "go.mod file not found". `-C <dir>` anchors the module
	// lookup at the adapter directory while source still arrives only via
	// the protocol snapshot overlay.
	if dir, ok := strings.CutPrefix(spec, gorunScheme); ok {
		if !filepath.IsAbs(dir) {
			return protocol.Config{}, fmt.Errorf("%s needs an absolute adapter-directory path: %q", gorunScheme, spec)
		}
		if info, err := os.Stat(filepath.Join(dir, "main.go")); err != nil || info.IsDir() {
			return protocol.Config{}, fmt.Errorf("Go adapter entrypoint %s not found", filepath.Join(dir, "main.go"))
		}
		goBin, err := findExecutable("go")
		if err != nil {
			return protocol.Config{}, fmt.Errorf("go must be on PATH: %v", err)
		}
		return protocol.Config{BinPath: goBin, Args: []string{"run", "-C", dir, dir}}, nil
	}

	if !filepath.IsAbs(spec) {
		return protocol.Config{}, fmt.Errorf("adapter path must be absolute; got %q", spec)
	}
	if info, err := os.Stat(spec); err != nil || info.IsDir() {
		return protocol.Config{}, fmt.Errorf("adapter binary %q is not an executable file", spec)
	}
	return protocol.Config{BinPath: spec}, nil
}

// findBundledAdapterSpec looks for the bundled <checkout>/adapters tree at
// root or up to 8 levels above it, so package-deep working directories (e.g.
// `go test ./internal/flowview`) still resolve the checkout adapters.
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
// Homebrew, NVM, Flutter, and local binary locations when running in
// stripped environments (e.g. macOS GUI apps launched from Finder/Dock).
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

// ResolveDartAdapter turns a $CODEFLOW_ADAPTER_DART_BIN value into a protocol.Config.
func ResolveDartAdapter(spec string) (protocol.Config, error) {
	return ResolveAdapter("dart", spec)
}

// Runner drives Stage 1 harvest against one language adapter through a
// persistent process pool. It is safe to reuse across Run calls; Close
// drains the pool.
type Runner struct {
	pool      *protocol.Pool
	ownedPool bool
}

// NewRunner builds a Runner whose adapter subprocesses are spawned with
// adapterCfg and of which at most maxIdle stay warm.
func NewRunner(adapterCfg protocol.Config, maxIdle int) *Runner {
	return &Runner{pool: protocol.NewPool(adapterCfg, maxIdle), ownedPool: true}
}

// NewRunnerWithPool builds a Runner that shares an existing pool (caller retains ownership).
func NewRunnerWithPool(pool *protocol.Pool) *Runner {
	return &Runner{pool: pool, ownedPool: false}
}

// Close drains every pooled adapter process if this Runner owns the pool.
func (r *Runner) Close() {
	if r.pool != nil && r.ownedPool {
		r.pool.Close()
	}
}

type detectResult struct {
	Language  string `json:"language"`
	Confident bool   `json:"confident"`
}

// Run performs the full Stage 1 pass over repoRoot:
//
//  1. ping handshake with the adapter (version negotiation happens on
//     every fresh pooled connection),
//  2. detect — hard error when the repo is not confidently Dart,
//  3. harvest_candidates with {repoRoot, libSubdir:"lib"} and no profiles
//     (adapter built-in Riverpod/Bloc/go_router defaults),
//  4. deterministic scoring, root-equivalence dedup, tie-breaking,
//  5. codeflow.flows.yaml manifest overlay (pin/exclude/rename).
//
// It returns the full candidate payload in final priority order:
// representatives lead, deduped members stay flagged via dedupedInto so
// nothing harvested is silently discarded, pinned members are forced back
// in even if dedup dropped them. Every candidate is contract-validated
// (schemas/candidate.schema.json) before return; a violation is a hard
// error naming the offending candidate.
func (r *Runner) Run(ctx context.Context, repoRoot string) ([]Candidate, error) {
	root, err := filepath.Abs(repoRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve repoRoot: %w", err)
	}
	snapshot, err := protocol.CaptureSnapshot(root, 0)
	if err != nil {
		return nil, fmt.Errorf("capture analysis snapshot: %w", err)
	}
	return r.RunWithSnapshot(ctx, root, snapshot)
}

// RunWithSnapshot performs harvest against an explicit immutable analysis
// basis. Its content overlay is sent to the adapter when present.
func (r *Runner) RunWithSnapshot(ctx context.Context, repoRoot string, snapshot protocol.Snapshot) ([]Candidate, error) {
	root, err := filepath.Abs(repoRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve repoRoot: %w", err)
	}

	var vi protocol.VersionInfo
	if err := r.pool.Call(ctx, protocol.OpPing, map[string]any{}, &vi); err != nil {
		return nil, fmt.Errorf("adapter ping: %w", err)
	}

	var det detectResult
	detectParams := snapshot.Params()
	detectParams["requiredObservations"] = []string{"negative_lookup", "membership", "dependency_frontier"}
	if err := r.pool.Call(ctx, protocol.OpDetect, detectParams, &det); err != nil {
		return nil, fmt.Errorf("adapter detect: %w", err)
	}
	if !det.Confident {
		return nil, fmt.Errorf(
			"%s is not recognized by the adapter (detect: language=%q confident=%t)",
			root, det.Language, det.Confident)
	}

	var wire struct {
		Candidates []Candidate `json:"candidates"`
	}
	params := snapshot.Params()
	params["libSubdir"] = defaultLibSubdir
	params["requiredObservations"] = []string{"negative_lookup", "membership", "dependency_frontier"}
	if err := r.pool.Call(ctx, protocol.OpHarvestCandidates, params, &wire); err != nil {
		return nil, fmt.Errorf("adapter harvest_candidates: %w", err)
	}
	for _, c := range wire.Candidates {
		if err := validateCandidate(c); err != nil {
			return nil, fmt.Errorf("adapter returned an invalid candidate (entry %q): %w", c.EntrySymbolPath, err)
		}
	}

	idx, err := loadSourceIndexFromSnapshot(snapshotFiles(snapshot), defaultLibSubdir)
	if err != nil {
		return nil, fmt.Errorf("index sources for scoring: %w", err)
	}

	ScoreAll(wire.Candidates, idx)
	DedupAndTieBreak(wire.Candidates)

	man, err := LoadManifestFromSnapshot(snapshotFiles(snapshot))
	if err != nil {
		return nil, err
	}
	man.Apply(wire.Candidates)
	out := Finalize(wire.Candidates)

	for _, c := range out {
		if err := validateCandidate(c); err != nil {
			return nil, fmt.Errorf("scored candidate %q violates the contract: %w", c.EntrySymbolPath, err)
		}
	}
	return out, nil
}

func snapshotFiles(snapshot protocol.Snapshot) map[string]string {
	if len(snapshot.Files) > 0 {
		return snapshot.Files
	}
	return snapshot.ContentOverlay
}

// validateCandidate checks one candidate against the candidate contract.
func validateCandidate(c Candidate) error {
	b, err := json.Marshal(c)
	if err != nil {
		return fmt.Errorf("marshal candidate: %w", err)
	}
	return contractharness.Validate(contractharness.BaseURL+"candidate.schema.json", b)
}
