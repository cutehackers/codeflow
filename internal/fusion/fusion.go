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
type CodeLens struct {
	Path      string `json:"path"`
	StartLine int    `json:"startLine"`
	EndLine   int    `json:"endLine"`
}

// StateDelta represents before/after states for mutation steps.
type StateDelta struct {
	Before string `json:"before"`
	After  string `json:"after"`
}

// FlowStep represents a fused business flow step ready for FlowView rendering.
type FlowStep struct {
	Ordinal    int         `json:"ordinal"`
	Name       string      `json:"name"`
	Provenance string      `json:"provenance"` // approved | session | derived | unknown
	Freshness  string      `json:"freshness"`  // fresh | stale | orphaned (schemas/identity $defs/freshness)
	Confidence float64     `json:"confidence"` // 0.0 - 1.0
	BasisSha   string      `json:"basisSha"`
	Anchor     slicing.Anchor `json:"anchor"`
	StepID     *string     `json:"stepId,omitempty"`
	Rules      []string    `json:"rules,omitempty"`
	StateDelta *StateDelta `json:"stateDelta,omitempty"`
	SideEffect *string     `json:"sideEffect,omitempty"`
	Branch     *string     `json:"branch,omitempty"`
	CodeLens   *CodeLens   `json:"codeLens,omitempty"`
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
	BasisSha    string     `json:"basisSha"`
	GeneratedAt string     `json:"generatedAt"`
	Steps       []FlowStep `json:"steps"`
	Unknowns    []Unknown  `json:"unknowns"`
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
	CustomTitle     string
	SessionDrafts   map[string]SessionDraftStep // keyed by enclosingSymbolPath or stepId
	ApprovedLedger  map[string]ApprovedStep    // keyed by enclosingSymbolPath or stepId
	RepoRoot        string
	BasisSha        string
}

// SessionDraftStep represents an agent proposed step in E2.
type SessionDraftStep struct {
	Name      string
	Rationale string
	Rules     []string
}

// ApprovedStep represents a human approved step in E3.
type ApprovedStep struct {
	Name      string
	Rules     []string
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
			CodeLens:   lens,
		}

		steps = append(steps, step)
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
		BasisSha:    basisSha,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Steps:       steps,
		Unknowns:    unknowns,
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

// deriveCodeLens calculates 1-indexed start and end line numbers for presentation.
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

	startByte := anchor.ByteRange[0]
	endByte := anchor.ByteRange[1]
	if startByte > len(data) {
		startByte = len(data)
	}
	if endByte > len(data) {
		endByte = len(data)
	}

	startLine := 1
	for i := 0; i < startByte; i++ {
		if data[i] == '\n' {
			startLine++
		}
	}

	endLine := startLine
	for i := startByte; i < endByte; i++ {
		if data[i] == '\n' {
			endLine++
		}
	}

	return &CodeLens{
		Path:      anchor.RepoRelativePath,
		StartLine: startLine,
		EndLine:   endLine,
	}
}
