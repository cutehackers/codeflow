// Package comparison compiles both current and immutable baseline evidence
// through the same compiler before deriving a behavior delta.
package comparison

import (
	"context"

	"codeflow/core/internal/baseline"
	"codeflow/core/internal/compiler"
	"codeflow/core/internal/delta"
	"codeflow/core/internal/flowir"
)

type Options struct{ Repo, Revision, Selector, CodeGraphURL, AdapterCommand string }
type Result struct {
	Current  flowir.Document `json:"current"`
	Baseline flowir.Document `json:"baseline"`
	Delta    delta.Delta     `json:"delta"`
}

func Build(ctx context.Context, opt Options) (Result, *compiler.Problem, error) {
	current, problem, err := compiler.Compile(ctx, compiler.Options{Repo: opt.Repo, Selector: opt.Selector, CodeGraphURL: opt.CodeGraphURL, AdapterCommand: opt.AdapterCommand})
	if err != nil || problem != nil {
		return Result{}, problem, err
	}
	m, err := baseline.Materialize(opt.Repo, opt.Revision)
	if err != nil {
		return Result{}, &compiler.Problem{Code: "BASELINE_UNAVAILABLE", Message: err.Error()}, nil
	}
	base, problem, err := compiler.Compile(ctx, compiler.Options{Repo: m.Root, Selector: opt.Selector, CodeGraphURL: opt.CodeGraphURL, AdapterCommand: opt.AdapterCommand, Basis: &m.Basis})
	if err != nil || problem != nil {
		return Result{}, problem, err
	}
	return Result{Current: current, Baseline: base, Delta: delta.Compare(base, current)}, nil, nil
}
