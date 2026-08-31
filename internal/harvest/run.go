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

	"codeflow/internal/contractharness"
	"codeflow/internal/installstate"
	"codeflow/internal/protocol"
)

// Adapter env var prefix and defaults
const (
	DartAdapterEnvVar       = "CODEFLOW_ADAPTER_DART_BIN"
	TypeScriptAdapterEnvVar = "CODEFLOW_ADAPTER_TYPESCRIPT_BIN"
	TSAdapterEnvVar         = "CODEFLOW_ADAPTER_TS_BIN"
	KotlinAdapterEnvVar     = "CODEFLOW_ADAPTER_KOTLIN_BIN"
	SwiftAdapterEnvVar      = "CODEFLOW_ADAPTER_SWIFT_BIN"
	PythonAdapterEnvVar     = "CODEFLOW_ADAPTER_PYTHON_BIN"
)

const (
	dartrunScheme   = "dartrun:"
	dartEntrypoint  = "bin/codeflow_dart_adapter.dart"
	noderunScheme   = "noderun:"
	tsrunScheme     = "tsrun:"
	tsEntrypointJS  = "bin/codeflow_ts_adapter.js"
	tsEntrypointTS  = "bin/codeflow_ts_adapter.ts"
	defaultLibSubdir = "lib"
)

// ResolveAdapter turns a language identifier and optional spec into a protocol.Config.
func ResolveAdapter(lang string, spec string) (protocol.Config, error) {
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
		case "kotlin", "java":
			spec = strings.TrimSpace(os.Getenv(KotlinAdapterEnvVar))
		case "swift":
			spec = strings.TrimSpace(os.Getenv(SwiftAdapterEnvVar))
		case "python":
			spec = strings.TrimSpace(os.Getenv(PythonAdapterEnvVar))
		}
	}

	// 2. Check install state
	if spec == "" {
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
			if lang == "typescript" || lang == "javascript" {
				candidates = append(candidates, filepath.Join(home, ".local", "bin", "codeflow_ts_adapter"))
			}
			for _, cand := range candidates {
				if info, err := os.Stat(cand); err == nil && !info.IsDir() {
					spec = cand
					break
				}
			}
		}
	}

	// 4. Check workspace adapter directories if running from checkout
	if spec == "" {
		cwd, err := os.Getwd()
		if err == nil {
			if lang == "dart" {
				dartDir := filepath.Join(cwd, "adapters", "dart")
				if info, err := os.Stat(filepath.Join(dartDir, dartEntrypoint)); err == nil && !info.IsDir() {
					spec = dartrunScheme + dartDir
				}
			} else if lang == "typescript" || lang == "javascript" {
				tsDir := filepath.Join(cwd, "adapters", "typescript")
				if info, err := os.Stat(filepath.Join(tsDir, tsEntrypointJS)); err == nil && !info.IsDir() {
					spec = noderunScheme + tsDir
				} else if info, err := os.Stat(filepath.Join(tsDir, tsEntrypointTS)); err == nil && !info.IsDir() {
					spec = tsrunScheme + tsDir
				}
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
		dartBin, err := exec.LookPath("dart")
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
		nodeBin, err := exec.LookPath("node")
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
		if tsxBin, err := exec.LookPath("tsx"); err == nil {
			return protocol.Config{BinPath: tsxBin, Args: []string{entry}}, nil
		}
		if bunBin, err := exec.LookPath("bun"); err == nil {
			return protocol.Config{BinPath: bunBin, Args: []string{"run", entry}}, nil
		}
		nodeBin, err := exec.LookPath("node")
		if err != nil {
			return protocol.Config{}, fmt.Errorf("node or tsx must be on PATH: %v", err)
		}
		return protocol.Config{BinPath: nodeBin, Args: []string{entry}}, nil
	}

	if !filepath.IsAbs(spec) {
		return protocol.Config{}, fmt.Errorf("adapter path must be absolute; got %q", spec)
	}
	if info, err := os.Stat(spec); err != nil || info.IsDir() {
		return protocol.Config{}, fmt.Errorf("adapter binary %q is not an executable file", spec)
	}
	return protocol.Config{BinPath: spec}, nil
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

	var vi protocol.VersionInfo
	if err := r.pool.Call(ctx, protocol.OpPing, map[string]any{}, &vi); err != nil {
		return nil, fmt.Errorf("adapter ping: %w", err)
	}

	var det detectResult
	if err := r.pool.Call(ctx, protocol.OpDetect, map[string]any{"repoRoot": root}, &det); err != nil {
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
	params := map[string]any{"repoRoot": root, "libSubdir": defaultLibSubdir}
	if err := r.pool.Call(ctx, protocol.OpHarvestCandidates, params, &wire); err != nil {
		return nil, fmt.Errorf("adapter harvest_candidates: %w", err)
	}
	for _, c := range wire.Candidates {
		if err := validateCandidate(c); err != nil {
			return nil, fmt.Errorf("adapter returned an invalid candidate (entry %q): %w", c.EntrySymbolPath, err)
		}
	}

	idx, err := loadSourceIndex(root, defaultLibSubdir)
	if err != nil {
		return nil, fmt.Errorf("index sources for scoring: %w", err)
	}

	ScoreAll(wire.Candidates, idx)
	DedupAndTieBreak(wire.Candidates)

	man, err := LoadManifest(root)
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

// validateCandidate checks one candidate against the candidate contract.
func validateCandidate(c Candidate) error {
	b, err := json.Marshal(c)
	if err != nil {
		return fmt.Errorf("marshal candidate: %w", err)
	}
	return contractharness.Validate(contractharness.BaseURL+"candidate.schema.json", b)
}
