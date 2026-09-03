package semantic

import (
	"fmt"
	"math"
	"strings"

	"codeflow/internal/fusion"
	"codeflow/internal/harvest"
)

const (
	ErrCodeMissingPrecondition    = "missing_precondition"
	ErrCodeAmbiguousTarget        = "ambiguous_target"
	ErrCodeIncomparableBasis      = "incomparable_basis"
	ErrCodeUnsupportedCapability  = "unsupported_capability"
)

// TaskViewQuery mirrors schemas/task-view-query.schema.json.
type TaskViewQuery struct {
	SchemaID      string              `json:"schemaId"`
	SchemaVersion int                 `json:"schemaVersion"`
	Mode          string              `json:"mode"`
	Common        *CommonQueryParams  `json:"common,omitempty"`
	Feature       *FeatureQueryParams `json:"feature,omitempty"`
}

type CommonQueryParams struct {
	TaskID         string         `json:"taskId,omitempty"`
	IntentRevision int            `json:"intentRevision,omitempty"`
	BasisSelector  *BasisSelector `json:"basisSelector,omitempty"`
	Filters        *QueryFilters  `json:"filters,omitempty"`
}

type BasisSelector struct {
	Kind string `json:"kind"` // active | generation | workspaceSnapshot
	ID   string `json:"id,omitempty"`
}

type QueryFilters struct {
	IncludeTests           bool `json:"includeTests,omitempty"`
	IncludeRuntimeEvidence bool `json:"includeRuntimeEvidence,omitempty"`
	MaxVisibleCoreSteps    int  `json:"maxVisibleCoreSteps,omitempty"`
}

type FeatureQueryParams struct {
	Request     string `json:"request,omitempty"`
	FlowID      string `json:"flowId,omitempty"`
	EntrySymbol string `json:"entrySymbol,omitempty"`
	Domain      string `json:"domain,omitempty"`
}

// QueryError represents a typed error returned for query validation failures.
type QueryError struct {
	Code             string   `json:"code"`
	Message          string   `json:"message"`
	CandidateTargets []string `json:"candidateTargets,omitempty"`
}

func (e *QueryError) Error() string {
	if len(e.CandidateTargets) > 0 {
		return fmt.Sprintf("%s: %s (candidates: %s)", e.Code, e.Message, strings.Join(e.CandidateTargets, ", "))
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// ResolvedTarget holds the confirmed entrypoint for deterministic compilation.
type ResolvedTarget struct {
	EntrySymbolPath string
	CandidateID     string
	FlowID          string
	Title           string
}

// ResolveFeatureQueryTarget resolves the target entrypoint from a feature query.
// Enforces VS02-A2 and Raw §8.2: missing conditions return missing_precondition,
// and equally matching targets return ambiguous_target without guessing.
func ResolveFeatureQueryTarget(query *TaskViewQuery, candidates []harvest.Candidate) (*ResolvedTarget, error) {
	if query == nil || query.Mode != "feature" {
		return nil, &QueryError{
			Code:    ErrCodeMissingPrecondition,
			Message: "query must have mode='feature'",
		}
	}

	feat := query.Feature
	if feat == nil {
		return nil, &QueryError{
			Code:    ErrCodeMissingPrecondition,
			Message: "feature mode requires start conditions in the 'feature' property",
		}
	}

	req := strings.TrimSpace(feat.Request)
	flowID := strings.TrimSpace(feat.FlowID)
	entrySymbol := strings.TrimSpace(feat.EntrySymbol)
	domain := strings.TrimSpace(feat.Domain)

	if req == "" && flowID == "" && entrySymbol == "" && domain == "" {
		return nil, &QueryError{
			Code:    ErrCodeMissingPrecondition,
			Message: "feature query requires at least one start condition: request, flowId, entrySymbol, or domain",
		}
	}

	// 1. Explicit entrySymbol takes precedence
	if entrySymbol != "" {
		for _, c := range candidates {
			if c.EntrySymbolPath == entrySymbol {
				fID := fusion.ComputeFlowID(c.EntrySymbolPath)
				return &ResolvedTarget{
					EntrySymbolPath: c.EntrySymbolPath,
					CandidateID:     c.CandidateID,
					FlowID:          fID,
					Title:           deriveTitle(c),
				}, nil
			}
		}
		// If not in candidate list, allow direct entry
		return &ResolvedTarget{
			EntrySymbolPath: entrySymbol,
			FlowID:          fusion.ComputeFlowID(entrySymbol),
			Title:           entrySymbol,
		}, nil
	}

	// 2. Explicit flowId match
	if flowID != "" {
		for _, c := range candidates {
			if fusion.ComputeFlowID(c.EntrySymbolPath) == flowID || c.CandidateID == flowID {
				return &ResolvedTarget{
					EntrySymbolPath: c.EntrySymbolPath,
					CandidateID:     c.CandidateID,
					FlowID:          flowID,
					Title:           deriveTitle(c),
				}, nil
			}
		}
		return nil, &QueryError{
			Code:    ErrCodeMissingPrecondition,
			Message: fmt.Sprintf("no candidate found for flowId %q", flowID),
		}
	}

	// 3. Natural language or domain match across candidates
	searchTerm := req
	if searchTerm == "" {
		searchTerm = domain
	}
	searchLower := strings.ToLower(searchTerm)

	type scoredMatch struct {
		cand  harvest.Candidate
		score float64
	}

	var matches []scoredMatch
	for _, c := range candidates {
		// Excluded candidates are skipped
		if c.ManifestOverride == "excluded" {
			continue
		}
		match := false
		if strings.Contains(strings.ToLower(c.EntrySymbolPath), searchLower) ||
			strings.Contains(strings.ToLower(c.IntentSignals.DerivedName), searchLower) ||
			strings.Contains(strings.ToLower(c.IntentSignals.ClassName), searchLower) {
			match = true
		}
		if match {
			matches = append(matches, scoredMatch{cand: c, score: c.Score})
		}
	}

	if len(matches) == 0 {
		return nil, &QueryError{
			Code:    ErrCodeMissingPrecondition,
			Message: fmt.Sprintf("no matching flow candidates found for query %q", searchTerm),
		}
	}

	if len(matches) == 1 {
		c := matches[0].cand
		return &ResolvedTarget{
			EntrySymbolPath: c.EntrySymbolPath,
			CandidateID:     c.CandidateID,
			FlowID:          fusion.ComputeFlowID(c.EntrySymbolPath),
			Title:           deriveTitle(c),
		}, nil
	}

	// Check if top candidates tie
	topScore := matches[0].score
	var topCandidates []harvest.Candidate
	for _, m := range matches {
		if math.Abs(m.score-topScore) < 0.0001 {
			topCandidates = append(topCandidates, m.cand)
		} else if m.score > topScore {
			topScore = m.score
			topCandidates = []harvest.Candidate{m.cand}
		}
	}

	if len(topCandidates) > 1 {
		var targets []string
		for _, tc := range topCandidates {
			targets = append(targets, tc.EntrySymbolPath)
		}
		return nil, &QueryError{
			Code:             ErrCodeAmbiguousTarget,
			Message:          fmt.Sprintf("multiple flow targets match %q with equal confidence; please narrow the query with entrySymbol or flowId", searchTerm),
			CandidateTargets: targets,
		}
	}

	c := topCandidates[0]
	return &ResolvedTarget{
		EntrySymbolPath: c.EntrySymbolPath,
		CandidateID:     c.CandidateID,
		FlowID:          fusion.ComputeFlowID(c.EntrySymbolPath),
		Title:           deriveTitle(c),
	}, nil
}

func deriveTitle(c harvest.Candidate) string {
	if c.IntentSignals.DerivedName != "" {
		return c.IntentSignals.DerivedName
	}
	if c.IntentSignals.ClassName != "" {
		return c.IntentSignals.ClassName
	}
	return c.EntrySymbolPath
}
