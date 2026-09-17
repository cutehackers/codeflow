// Package collector handles candidate harvesting, AST slicing, evidence fusion, and secret masking.
package collector

import (
	"context"

	"codeflow/internal/fusion"
	"codeflow/internal/harvest"
	"codeflow/internal/protocol"
	"codeflow/internal/secret"
	"codeflow/internal/slicing"
)

// Collector coordinates the extraction and fusion of execution flows.
type Collector struct {
	repoRoot string
}

// NewCollector constructs a collector for a repository.
func NewCollector(repoRoot string) *Collector {
	return &Collector{repoRoot: repoRoot}
}

// RepoRoot returns the configured target repository root.
func (c *Collector) RepoRoot() string {
	return c.repoRoot
}

// MaskSecret applies single-gate secret redaction to source code or strings.
func (c *Collector) MaskSecret(input string) string {
	return secret.Redact(input).Text
}

// Harvest finds entrypoint candidates in the repository using the adapter pool.
func (c *Collector) Harvest(ctx context.Context, pool *protocol.Pool, repoRoot string) ([]harvest.Candidate, error) {
	runner := harvest.NewRunnerWithPool(pool)
	return runner.Run(ctx, repoRoot)
}

// Slice performs static slicing on a specific entrypoint.
func (c *Collector) Slice(ctx context.Context, pool *protocol.Pool, repoRoot, candidateID, entrySymbolPath string) (*slicing.SlicedPayload, error) {
	runner := slicing.NewRunner(pool)
	return runner.Slice(ctx, repoRoot, candidateID, entrySymbolPath, nil)
}

// Fuse merges static slice results and event logs into a canonical FlowSpec.
func (c *Collector) Fuse(sliced *slicing.SlicedPayload, opts fusion.FuseOptions) (*fusion.FlowSpec, error) {
	return fusion.Fuse(sliced, opts)
}
