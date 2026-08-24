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
	"codeflow/internal/protocol"
)

// DartAdapterEnvVar selects the Dart adapter spawn command. Resolution
// precedence (ticket 06):
//
//  1. $CODEFLOW_ADAPTER_DART_BIN — either an absolute path to an adapter
//     executable invoked directly, or the special shell-ish form
//     "dartrun:<path-to-package-dir>" meaning `dart run
//     bin/codeflow_dart_adapter.dart` with <path> as the adapter package
//     directory (the entrypoint is resolved inside that package, so no
//     process cwd change is needed).
//  2. Workspace adapter pins will slot in here in a later ticket; today a
//     missing variable is a hard error telling the user how to point CORE
//     at an adapter.
const DartAdapterEnvVar = "CODEFLOW_ADAPTER_DART_BIN"

// dartrunScheme and dartEntrypoint define the dartrun: form.
const (
	dartrunScheme    = "dartrun:"
	dartEntrypoint   = "bin/codeflow_dart_adapter.dart"
	defaultLibSubdir = "lib"
)

// ResolveDartAdapter turns a $CODEFLOW_ADAPTER_DART_BIN value into a
// protocol.Config. spec may be empty (→ actionable error), an absolute
// executable path, or "dartrun:<absolute package dir>".
func ResolveDartAdapter(spec string) (protocol.Config, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return protocol.Config{}, fmt.Errorf(
			"no Dart adapter configured: set %s to an absolute adapter binary path, or to %s<path-to-adapters/dart>",
			DartAdapterEnvVar, dartrunScheme)
	}
	if dir, ok := strings.CutPrefix(spec, dartrunScheme); ok {
		if !filepath.IsAbs(dir) {
			return protocol.Config{}, fmt.Errorf(
				"%s %q: %s needs an absolute package-directory path", DartAdapterEnvVar, spec, dartrunScheme)
		}
		entry := filepath.Join(dir, filepath.FromSlash(dartEntrypoint))
		info, err := os.Stat(entry)
		if err != nil || info.IsDir() {
			return protocol.Config{}, fmt.Errorf(
				"%s %q: adapter entrypoint %s not found; point %s at the adapter package directory (e.g. adapters/dart)",
				DartAdapterEnvVar, spec, entry, DartAdapterEnvVar)
		}
		dartBin, err := exec.LookPath("dart")
		if err != nil {
			return protocol.Config{}, fmt.Errorf(
				"%s %q: the dart SDK executable must be on PATH for %s adapters: %v",
				DartAdapterEnvVar, spec, dartrunScheme, err)
		}
		return protocol.Config{BinPath: dartBin, Args: []string{"run", entry}}, nil
	}
	if !filepath.IsAbs(spec) {
		return protocol.Config{}, fmt.Errorf(
			"%s must be an absolute executable path or %s<package-dir>; got %q", DartAdapterEnvVar, dartrunScheme, spec)
	}
	if info, err := os.Stat(spec); err != nil || info.IsDir() {
		return protocol.Config{}, fmt.Errorf("%s %q is not an executable file", DartAdapterEnvVar, spec)
	}
	return protocol.Config{BinPath: spec}, nil
}

// Runner drives Stage 1 harvest against one language adapter through a
// persistent process pool. It is safe to reuse across Run calls; Close
// drains the pool.
type Runner struct {
	pool       *protocol.Pool
	ownedPool  bool
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
	if det.Language != "dart" || !det.Confident {
		return nil, fmt.Errorf(
			"%s does not look like a Dart project (detect: language=%q confident=%t); a pubspec.yaml with an sdk: section and Dart sources under lib/ is required",
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
