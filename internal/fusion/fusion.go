// Package fusion implements evidence ranking, anchor relinking, and event ledger
// integration (design §3, §12, §16 R2/R3, tickets 12, 13, 14, 18).
//
// Invariants:
//  1. E1 structural steps are immutable: E2/E3 cannot overwrite or delete structural cards.
//  2. Authority hierarchy for names/rules: approved (E3) > session (E2) > derived > unknown.
//  3. Stale detection: if code changes break the canonical AST fingerprint, freshness flips to 'stale'.
//  4. Unknowns are preserved explicitly, never guessed.
package fusion

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"codeflow/internal/contractharness"
	"codeflow/internal/naming"
	"codeflow/internal/secret"
	"codeflow/internal/slicing"
)

// CodeLens specifies presentation line ranges derived at render time.
// Focus = [StartLine, EndLine] (the step's exact evidence lines).
// View = [ViewStartLine, ViewEndLine] (the enclosing symbol body) so FlowView
// can show the flow a line lives in instead of a lone statement.
type CodeLens struct {
	Path          string `json:"path"`
	StartLine     int    `json:"startLine"`
	EndLine       int    `json:"endLine"`
	ViewStartLine int    `json:"viewStartLine,omitempty"`
	ViewEndLine   int    `json:"viewEndLine,omitempty"`
}

// StateDelta represents before/after states for mutation steps.
type StateDelta struct {
	Before string `json:"before"`
	After  string `json:"after"`
}

// FlowStep represents a fused business flow step ready for FlowView rendering.
type FlowStep struct {
	Ordinal    int            `json:"ordinal"`
	Name       string         `json:"name"`
	Provenance string         `json:"provenance"` // approved | session | derived | unknown
	Freshness  string         `json:"freshness"`  // fresh | stale | orphaned (schemas/identity $defs/freshness)
	Confidence float64        `json:"confidence"` // 0.0 - 1.0
	BasisSha   string         `json:"basisSha"`
	Anchor     slicing.Anchor `json:"anchor"`
	StepID     *string        `json:"stepId,omitempty"`
	Rules      []string       `json:"rules,omitempty"`
	StateDelta *StateDelta    `json:"stateDelta,omitempty"`
	SideEffect *string        `json:"sideEffect,omitempty"`
	Branch     *string        `json:"branch,omitempty"`
	Kind       string         `json:"kind,omitempty"` // guard | mutation | call | branch (presentation-only)
	Layer      string         `json:"layer,omitempty"`
	CodeLens   *CodeLens      `json:"codeLens,omitempty"`
}

// FlowEdge is a presentation-only delegation target: which symbol a step hands
// work off to. Carried over from the slice so FlowView can show the causal
// hand-off, not just the timeline order.
type FlowEdge struct {
	Kind             string `json:"kind"` // resolved_cross_file | boundary_call | unknown_edge
	ToSymbolPath     string `json:"toSymbolPath"`
	ResolutionStatus string `json:"resolutionStatus"`
	StepOrdinal      *int   `json:"stepOrdinal,omitempty"`
	ToLayer          string `json:"toLayer,omitempty"`
}

// Unknown represents an unresolvable item preserved explicitly.
type Unknown struct {
	Subject string `json:"subject"`
	Reason  string `json:"reason"`
}

// FlowSpec is the primary document published to FlowView.
type FlowSpec struct {
	FlowID      string     `json:"flowId"`
	Title       string     `json:"title"`
	Description string     `json:"description,omitempty"`
	BasisSha    string     `json:"basisSha"`
	GeneratedAt string     `json:"generatedAt"`
	Steps       []FlowStep `json:"steps"`
	Unknowns    []Unknown  `json:"unknowns"`
	// Edges is OPTIONAL (absent in older specs): delegation targets per step.
	Edges []FlowEdge `json:"edges,omitempty"`
	// Truncated is OPTIONAL: true when the slice traversal stopped early.
	Truncated bool `json:"truncated,omitempty"`
}

// ComputeFlowID derives 'flow-<16hex>' from the canonical entry symbol path.
func ComputeFlowID(canonicalEntrySymbolPath string) string {
	h := sha256.Sum256([]byte(canonicalEntrySymbolPath))
	hexStr := hex.EncodeToString(h[:])
	return "flow-" + hexStr[:16]
}

// ComputeStepID derives 'step-<16hex>' from '<flowId>#<ordinal>:<symbolPath>'.
func ComputeStepID(flowID string, ordinal int, symbolPath string) string {
	raw := fmt.Sprintf("%s#%d:%s", flowID, ordinal, symbolPath)
	h := sha256.Sum256([]byte(raw))
	hexStr := hex.EncodeToString(h[:])
	return "step-" + hexStr[:16]
}

// FuseOptions parameterizes the fusion process with optional overrides.
type FuseOptions struct {
	CustomTitle       string
	CustomDescription string
	SessionDrafts     map[string]SessionDraftStep // keyed by enclosingSymbolPath or stepId
	ApprovedLedger    map[string]ApprovedStep     // keyed by enclosingSymbolPath or stepId
	RepoRoot          string
	BasisSha          string
}

// SessionDraftStep represents an agent proposed step in E2.
type SessionDraftStep struct {
	Name      string
	Rationale string
	Rules     []string
}

// ApprovedStep represents a human approved step in E3.
type ApprovedStep struct {
	Name       string
	Rules      []string
	ApprovedAt time.Time
}

// Fuse merges an E1 SlicedPayload with E2/E3 semantics into a validated FlowSpec.
func Fuse(sliced *slicing.SlicedPayload, opts FuseOptions) (*FlowSpec, error) {
	if sliced == nil {
		return nil, fmt.Errorf("sliced payload is nil")
	}

	flowID := ComputeFlowID(sliced.EntrySymbolPath)
	title := opts.CustomTitle
	if title == "" {
		// Extract method from entry symbol path
		hashIdx := strings.Index(sliced.EntrySymbolPath, "#")
		sym := sliced.EntrySymbolPath
		if hashIdx >= 0 {
			sym = sliced.EntrySymbolPath[hashIdx+1:]
		}
		if dotIdx := strings.LastIndex(sym, "."); dotIdx >= 0 {
			sym = sym[dotIdx+1:]
		}
		title = naming.DeriveTitle(sym)
	}

	basisSha := opts.BasisSha
	if basisSha == "" {
		h := sha256.Sum256([]byte(sliced.EntrySymbolPath + ":" + sliced.CandidateID))
		basisSha = hex.EncodeToString(h[:])
	}

	steps := make([]FlowStep, 0, len(sliced.Steps))
	unknowns := make([]Unknown, 0)

	for _, s := range sliced.Steps {
		stepID := ComputeStepID(flowID, s.Ordinal, s.SymbolPath)

		// Determine name, rules, and provenance
		var (
			stepName   string
			rules      []string
			provenance = "derived"
			freshness  = "fresh"
			confidence = 0.85
		)

		// 1. Derived baseline from AST
		stepName = s.Description
		if stepName == "" {
			stepName = naming.DeriveTitle(s.SymbolPath)
		}

		// 2. Check E2 session drafts
		if draft, ok := opts.SessionDrafts[s.Anchor.EnclosingSymbolPath]; ok {
			provenance = "session"
			stepName = draft.Name
			rules = draft.Rules
			confidence = 0.90
		}

		// 3. Check E3 approved overrides (highest authority for semantic labels)
		if approved, ok := opts.ApprovedLedger[s.Anchor.EnclosingSymbolPath]; ok {
			provenance = "approved"
			stepName = approved.Name
			rules = approved.Rules
			confidence = 1.0
		}

		// 4. Verify freshness / relink against disk if repoRoot is provided
		if opts.RepoRoot != "" {
			freshness = checkFreshness(opts.RepoRoot, s.Anchor)
		}

		// Compute presentation CodeLens from byte range
		var lens *CodeLens
		if opts.RepoRoot != "" {
			lens = deriveCodeLens(opts.RepoRoot, s.Anchor)
		} else {
			lens = &CodeLens{
				Path:      s.Anchor.RepoRelativePath,
				StartLine: 1,
				EndLine:   10,
			}
		}

		var stateDelta *StateDelta
		if s.StateBefore != nil || s.StateAfter != nil {
			stateDelta = &StateDelta{
				Before: derefOr(s.StateBefore, "idle"),
				After:  derefOr(s.StateAfter, "updated"),
			}
		}

		var branch *string
		if s.Kind == "branch" || s.GuardCondition != nil {
			cond := derefOr(s.GuardCondition, "분기 처리")
			branch = &cond
		}

		step := FlowStep{
			Ordinal:    s.Ordinal,
			Name:       stepName,
			Provenance: provenance,
			Freshness:  freshness,
			Confidence: confidence,
			BasisSha:   basisSha,
			Anchor:     s.Anchor,
			StepID:     &stepID,
			Rules:      rules,
			StateDelta: stateDelta,
			SideEffect: s.EffectTarget,
			Branch:     branch,
			Kind:       s.Kind,
			Layer:      s.Layer,
			CodeLens:   lens,
		}

		steps = append(steps, step)
	}

	// Presentation edges: keep every slice edge with its producing step so
	// FlowView can show where each step hands work off to.
	var edges []FlowEdge
	for _, edge := range sliced.Edges {
		edges = append(edges, FlowEdge{
			Kind:             edge.Kind,
			ToSymbolPath:     edge.ToSymbolPath,
			ResolutionStatus: edge.ResolutionStatus,
			StepOrdinal:      edge.StepOrdinal,
			ToLayer:          edge.ToLayer,
		})
	}

	// Unknowns from sliced edges
	for _, edge := range sliced.Edges {
		if edge.ResolutionStatus == "unresolved_dynamic" {
			unknowns = append(unknowns, Unknown{
				Subject: edge.ToSymbolPath,
				Reason:  "unresolved_dynamic_call",
			})
		}
	}

	if sliced.Truncated {
		unknowns = append(unknowns, Unknown{
			Subject: "depth > 5 traversal",
			Reason:  "truncated_traversal",
		})
	}

	spec := &FlowSpec{
		FlowID:      flowID,
		Title:       title,
		Description: opts.CustomDescription,
		BasisSha:    basisSha,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Steps:       steps,
		Unknowns:    unknowns,
		Edges:       edges,
		Truncated:   sliced.Truncated,
	}

	// Secret scan & validate against schema
	specBytes, err := json.Marshal(spec)
	if err != nil {
		return nil, fmt.Errorf("marshal flowspec: %w", err)
	}
	cleanBytes, _, err := secret.RedactJSON(specBytes)
	if err != nil {
		return nil, fmt.Errorf("redact flowspec: %w", err)
	}

	if err := contractharness.Validate(contractharness.BaseURL+"flowspec.schema.json", cleanBytes); err != nil {
		return nil, fmt.Errorf("flowspec validation failed: %w", err)
	}

	var validSpec FlowSpec
	if err := json.Unmarshal(cleanBytes, &validSpec); err != nil {
		return nil, fmt.Errorf("unmarshal validated flowspec: %w", err)
	}

	return &validSpec, nil
}

func derefOr(s *string, def string) string {
	if s == nil {
		return def
	}
	return *s
}

// checkFreshness verifies an anchor against the current worktree on disk.
// Freshness semantics (schemas/identity $defs/freshness): fresh | stale | orphaned.
//
//   - if os.ReadFile fails → stale (orphaned if file missing / IsNotExist)
//   - if hex(fileHash)==anchor.FileHash → fresh
//   - if anchor.ByteRange within file and sha256(span)==anchor.SpanHash → fresh
//   - else locate enclosing symbol: search file for last segment of
//     anchor.EnclosingSymbolPath (split at '.') as declaration
//     (regex `(?m)^\s*(?:@\w+\s+)*[A-Za-z_][\w<>,\s?]*\s+<method>\s*\( `).
//     Extract balanced-brace span from declaration start, hash that span
//     (sha256 of raw bytes) and compare to anchor.SpanHash → if match → fresh
//     (line-shift relink), if found but mismatch → stale.
//   - if symbol not found anywhere in file → orphaned.
//
// TODO(canonicalAstFingerprint): true equality would require adapter-side
// canonicalization (whitespace/format-insensitive AST serialization). Until
// adapters expose that directly for re-computation, SpanHash is used as a
// proxy for the anchored span's identity; a matching enclosing-symbol span
// hash implies freshness under line-shift, while mismatch implies stale.
func checkFreshness(repoRoot string, anchor slicing.Anchor) string {
	fullPath := filepath.Join(repoRoot, anchor.RepoRelativePath)
	data, err := os.ReadFile(fullPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "orphaned"
		}
		return "stale"
	}
	fileHash := sha256.Sum256(data)
	if hex.EncodeToString(fileHash[:]) == anchor.FileHash {
		return "fresh"
	}

	if anchor.ByteRange[0] >= 0 && anchor.ByteRange[1] <= len(data) && anchor.ByteRange[0] <= anchor.ByteRange[1] {
		span := data[anchor.ByteRange[0]:anchor.ByteRange[1]]
		spanHash := sha256.Sum256(span)
		if hex.EncodeToString(spanHash[:]) == anchor.SpanHash {
			return "fresh"
		}
	}

	// Locate enclosing symbol by last segment of EnclosingSymbolPath.
	method := anchor.EnclosingSymbolPath
	if idx := strings.LastIndex(method, "."); idx >= 0 {
		method = method[idx+1:]
	}
	method = strings.TrimSpace(method)
	if method == "" {
		return "stale"
	}

	contentStr := string(data)
	if !strings.Contains(contentStr, method) {
		return "orphaned"
	}

	// Declaration regex: (?m)^\s*(?:@\w+\s+)*[A-Za-z_][\w<>,\s?]*\s+<method>\s*\(
	pattern := fmt.Sprintf(`(?m)^\s*(?:@\w+\s+)*[A-Za-z_][\w<>,\s?]*\s+%s\s*\(`, regexp.QuoteMeta(method))
	re, err := regexp.Compile(pattern)
	if err != nil {
		return "stale"
	}
	loc := re.FindStringIndex(contentStr)
	if loc == nil {
		return "orphaned"
	}
	declStart := loc[0]
	// Find opening brace after declaration start.
	openRel := strings.Index(contentStr[declStart:], "{")
	if openRel == -1 {
		return "stale"
	}
	braceStart := declStart + openRel
	depth := 0
	endIdx := -1
	for i := braceStart; i < len(data); i++ {
		switch data[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				endIdx = i + 1
				break
			}
		}
		if endIdx != -1 {
			break
		}
	}
	if endIdx == -1 {
		return "stale"
	}
	span := data[declStart:endIdx]
	spanHash := sha256.Sum256(span)
	if hex.EncodeToString(spanHash[:]) == anchor.SpanHash {
		return "fresh"
	}
	return "stale"
}

// deriveCodeLens calculates 1-indexed presentation line ranges: focus from the
// anchor's byte range, and view from the enclosing symbol range when present.
func deriveCodeLens(repoRoot string, anchor slicing.Anchor) *CodeLens {
	fullPath := filepath.Join(repoRoot, anchor.RepoRelativePath)
	data, err := os.ReadFile(fullPath)
	if err != nil {
		return &CodeLens{
			Path:      anchor.RepoRelativePath,
			StartLine: 1,
			EndLine:   10,
		}
	}

	startByte := clampOffset(anchor.ByteRange[0], len(data))
	endByte := clampOffset(anchor.ByteRange[1], len(data))

	focusStart := lineAtOffset(data, startByte)
	focusEnd := lineAtOffset(data, endByte)
	if focusEnd < focusStart {
		focusEnd = focusStart
	}

	lens := &CodeLens{
		Path:      anchor.RepoRelativePath,
		StartLine: focusStart,
		EndLine:   focusEnd,
	}

	// Symbol-scoped view: prefer the adapter-provided symbol range, fall back
	// to a margin around the focus lines. Presentation-only, best effort.
	totalLines := 1 + bytes.Count(data, []byte("\n"))
	viewStart, viewEnd := focusStart, focusEnd
	if anchor.SymbolRange != nil {
		symStart := clampOffset((*anchor.SymbolRange)[0], len(data))
		symEnd := clampOffset((*anchor.SymbolRange)[1], len(data))
		if symEnd > symStart {
			symStartLine := lineAtOffset(data, symStart)
			symEndLine := lineAtOffset(data, symEnd)
			viewStart = symStartLine
			viewEnd = symEndLine
			if viewEnd-viewStart+1 > maxViewLines {
				// Cap the window centered on the focus lines, inside the symbol.
				viewStart = focusStart - maxViewLines/2
				if viewStart < symStartLine {
					viewStart = symStartLine
				}
				viewEnd = viewStart + maxViewLines - 1
				if viewEnd > symEndLine {
					viewEnd = symEndLine
					viewStart = viewEnd - maxViewLines + 1
					if viewStart < symStartLine {
						viewStart = symStartLine
					}
				}
			}
		}
	} else {
		viewStart = focusStart - fallbackViewMargin
		viewEnd = focusEnd + fallbackViewMargin
	}
	if viewEnd < viewStart {
		viewStart, viewEnd = focusStart, focusEnd
	}
	if viewStart > focusStart {
		viewStart = focusStart
	}
	if viewEnd < focusEnd {
		viewEnd = focusEnd
	}
	if viewStart < 1 {
		viewStart = 1
	}
	if viewEnd > totalLines {
		viewEnd = totalLines
	}
	if viewEnd > viewStart {
		lens.ViewStartLine = viewStart
		lens.ViewEndLine = viewEnd
	}
	return lens
}

const (
	// maxViewLines caps the symbol-scoped view window rendered by FlowView.
	maxViewLines = 120
	// fallbackViewMargin widens the view around focus when no symbol range exists.
	fallbackViewMargin = 12
)

func clampOffset(offset, size int) int {
	if offset < 0 {
		return 0
	}
	if offset > size {
		return size
	}
	return offset
}

// lineAtOffset returns the 1-indexed line containing the given byte offset.
func lineAtOffset(data []byte, offset int) int {
	line := 1
	for i := 0; i < offset && i < len(data); i++ {
		if data[i] == '\n' {
			line++
		}
	}
	return line
}
