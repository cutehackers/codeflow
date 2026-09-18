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
	"path/filepath"

	"codeflow/internal/analyzer/protocol"
	"codeflow/internal/collector/contractharness"
)

const defaultLibSubdir = "lib"

// Adapter env var prefix and defaults delegated to protocol.
const (
	DartAdapterEnvVar       = protocol.DartAdapterEnvVar
	TypeScriptAdapterEnvVar = protocol.TypeScriptAdapterEnvVar
	TSAdapterEnvVar         = protocol.TSAdapterEnvVar
	GoAdapterEnvVar         = protocol.GoAdapterEnvVar
	KotlinAdapterEnvVar     = protocol.KotlinAdapterEnvVar
	SwiftAdapterEnvVar      = protocol.SwiftAdapterEnvVar
	PythonAdapterEnvVar     = protocol.PythonAdapterEnvVar
)

// ResolveAdapter turns a language identifier and optional spec into a protocol.Config.
func ResolveAdapter(lang string, spec string) (protocol.Config, error) {
	return protocol.ResolveAdapter(lang, spec)
}

// ResolveAdapterForRepo resolves the adapter for a given repository root.
func ResolveAdapterForRepo(repoRoot, cwd, lang string, spec string) (protocol.Config, error) {
	return protocol.ResolveAdapterForRepo(repoRoot, cwd, lang, spec)
}

// ResolveDartAdapter turns a $CODEFLOW_ADAPTER_DART_BIN value into a protocol.Config.
func ResolveDartAdapter(spec string) (protocol.Config, error) {
	return protocol.ResolveDartAdapter(spec)
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
