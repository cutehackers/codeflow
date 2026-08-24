// Package entrypoint applies CodeFlow's deliberately conservative selector
// rules. A selector can be exact, configured, or uniquely discovered; it is
// never a ranking instruction.
package entrypoint

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"codeflow/core/internal/config"
	"codeflow/core/internal/dartadapter"
	"codeflow/core/internal/flowir"
	"codeflow/core/internal/manifest"
)

type State string

const (
	Ready       State = "ready"
	Unknown     State = "unknown"
	Unavailable State = "unavailable"
)

type Result struct {
	State      State        `json:"state"`
	Selector   string       `json:"selector"`
	EntryPoint *EntryPoint  `json:"entry_point,omitempty"`
	Candidates []EntryPoint `json:"candidates"`
	Unknown    *Problem     `json:"unknown,omitempty"`
}
type Problem struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
type EntryPoint struct {
	FlowID string        `json:"flow_id"`
	Alias  string        `json:"alias"`
	Anchor flowir.Anchor `json:"anchor"`
}

func Resolve(ctx context.Context, repo, selector, command string) Result {
	selector = strings.TrimSpace(selector)
	entries, err := dartadapter.Discover(ctx, command, repo)
	if err != nil {
		f := dartadapter.AsFailure(err)
		state := Unavailable
		if f.Code == "ADAPTER_TIMEOUT" {
			state = Unknown
		}
		return Result{State: state, Selector: selector, Candidates: []EntryPoint{}, Unknown: &Problem{f.Code, f.Message}}
	}
	basis, err := manifest.Capture(repo)
	if err != nil {
		return Result{State: Unavailable, Selector: selector, Candidates: []EntryPoint{}, Unknown: &Problem{"WORKTREE_UNAVAILABLE", err.Error()}}
	}
	return ResolveDiscovered(repo, selector, entries, basis)
}

// ResolveDiscovered applies the exact same selector and evidence gate to entry
// points returned by an already initialized adapter. Multi-flow compilation
// uses it so every selector shares one manifest capture and one analyzer
// process instead of silently observing the worktree several times.
func ResolveDiscovered(repo, selector string, entries []dartadapter.EntryPoint, basis flowir.Basis) Result {
	selector = strings.TrimSpace(selector)
	loaded, err := config.Load(repo)
	if err != nil {
		return Result{State: Unavailable, Selector: selector, Candidates: []EntryPoint{}, Unknown: &Problem{"CONFIG_INVALID", err.Error()}}
	}
	exact := selector
	if feature, ok := loaded.Config.Features[selector]; ok {
		exact = feature.EntryPoint
	}
	candidates, err := convert(entries, basis)
	if err != nil {
		return Result{State: Unknown, Selector: selector, Candidates: []EntryPoint{}, Unknown: &Problem{"ADAPTER_STALE_OR_MALFORMED", err.Error()}}
	}
	if selector == "" {
		if len(candidates) == 1 {
			return Result{State: Ready, Selector: candidates[0].FlowID, EntryPoint: &candidates[0], Candidates: []EntryPoint{}}
		}
		return Result{State: Unknown, Selector: selector, Candidates: candidates, Unknown: &Problem{"SELECTOR_REQUIRED", "multiple routes are available; choose one exact route:/... identifier from candidates"}}
	}
	if strings.HasPrefix(exact, "route:/") || strings.HasPrefix(exact, "system:") {
		matches := filterFlow(candidates, exact)
		if len(matches) == 1 {
			return Result{State: Ready, Selector: selector, EntryPoint: &matches[0], Candidates: []EntryPoint{}}
		}
		if len(matches) > 1 {
			return ambiguous(selector, matches)
		}
		return Result{State: Unknown, Selector: selector, Candidates: candidates, Unknown: &Problem{"ENTRY_POINT_NOT_FOUND", fmt.Sprintf("%s is not present in the current supported route or system declarations", exact)}}
	}
	matches := filterAlias(candidates, selector)
	if len(matches) == 1 {
		return Result{State: Ready, Selector: selector, EntryPoint: &matches[0], Candidates: []EntryPoint{}}
	}
	if len(matches) > 1 {
		return ambiguous(selector, matches)
	}
	return Result{State: Unknown, Selector: selector, Candidates: candidates, Unknown: &Problem{"ENTRY_POINT_NOT_FOUND", "no supported route or system entry point matches this selector"}}
}
func ambiguous(selector string, candidates []EntryPoint) Result {
	return Result{State: Unknown, Selector: selector, Candidates: candidates, Unknown: &Problem{"AMBIGUOUS_ENTRY_POINT", "multiple entry points match; choose an exact route:/... identifier"}}
}
func convert(values []dartadapter.EntryPoint, basis flowir.Basis) ([]EntryPoint, error) {
	manifestByPath := map[string]flowir.ManifestEntry{}
	for _, entry := range basis.Manifest {
		manifestByPath[entry.Path] = entry
	}
	out := make([]EntryPoint, 0, len(values))
	for _, p := range values {
		entry, ok := manifestByPath[p.Anchor.Path]
		if !ok || entry.Type != "file" || (p.Anchor.FileHash != "" && entry.FileHash != p.Anchor.FileHash) {
			return nil, fmt.Errorf("adapter anchor %s does not match current manifest", p.Anchor.Path)
		}
		if p.Anchor.ByteStart < 0 || p.Anchor.ByteEnd <= p.Anchor.ByteStart || p.Anchor.LineEnd < p.Anchor.LineStart {
			return nil, fmt.Errorf("adapter returned invalid range for %s", p.Anchor.Path)
		}
		bytes, readErr := os.ReadFile(filepath.Join(basis.Repository, filepath.FromSlash(p.Anchor.Path)))
		if readErr != nil || p.Anchor.ByteEnd > len(bytes) || flowir.SHA256Bytes(bytes) != entry.FileHash {
			return nil, fmt.Errorf("adapter range cannot be verified for %s", p.Anchor.Path)
		}
		out = append(out, EntryPoint{FlowID: p.FlowID, Alias: p.Alias, Anchor: flowir.Anchor{Kind: "code", Path: p.Anchor.Path, Symbol: "route", LineRange: []int{p.Anchor.LineStart, p.Anchor.LineEnd}, ByteRange: []int{p.Anchor.ByteStart, p.Anchor.ByteEnd}, FileHash: entry.FileHash, Fingerprint: p.Anchor.Fingerprint, Revision: "current"}})
	}
	for i := range out {
		out[i].Anchor.Revision = basis.HeadRevision
		out[i].Anchor.SpanHash = flowir.SHA256Bytes(mustRange(basis.Repository, out[i].Anchor))
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].FlowID < out[j].FlowID || out[i].FlowID == out[j].FlowID && out[i].Anchor.Path < out[j].Anchor.Path
	})
	return out, nil
}
func mustRange(repo string, anchor flowir.Anchor) []byte {
	bytes, _ := os.ReadFile(filepath.Join(repo, filepath.FromSlash(anchor.Path)))
	return bytes[anchor.ByteRange[0]:anchor.ByteRange[1]]
}
func filterFlow(all []EntryPoint, flow string) []EntryPoint {
	var out []EntryPoint
	for _, v := range all {
		if v.FlowID == flow {
			out = append(out, v)
		}
	}
	return out
}
func filterAlias(all []EntryPoint, alias string) []EntryPoint {
	var out []EntryPoint
	for _, v := range all {
		if v.Alias == alias {
			out = append(out, v)
		}
	}
	return out
}
