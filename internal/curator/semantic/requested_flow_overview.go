package semantic

import (
	"fmt"
	"strings"

	"codeflow/internal/collector/secret"
)

const RequestedFlowOverviewSchemaID = "https://codeflow.local/schemas/requested-flow-overview.schema.json"

// RequestedFlowSegmentSelection proposes purpose and membership, never source text or ranges.
type RequestedFlowSegmentSelection struct {
	SegmentID       string                        `json:"segmentId"`
	Purpose         string                        `json:"purpose"`
	InclusionReason string                        `json:"inclusionReason"`
	StepIDs         []string                      `json:"stepIds"`
	Blocks          []RequestedFlowBlockSelection `json:"blocks"`
}

type RequestedFlowBlockSelection struct {
	StepIDs []string `json:"stepIds"`
}

type RequestedFlowRelationSelection struct {
	FromSegmentID string `json:"fromSegmentId"`
	ToSegmentID   string `json:"toSegmentId"`
	FromStepID    string `json:"fromStepId"`
	ToStepID      string `json:"toStepId"`
	EdgeIndexes   []int  `json:"edgeIndexes"`
}

type RequestedFlowLineSelection struct {
	LineNumber  int `json:"lineNumber"`
	StartColumn int `json:"startColumn"`
	EndColumn   int `json:"endColumn"`
}

type RequestedFlowHighlight struct {
	StepID         string                       `json:"stepId"`
	LineRange      [2]int                       `json:"lineRange"`
	ByteRange      [2]int                       `json:"byteRange"`
	LineSelections []RequestedFlowLineSelection `json:"lineSelections"`
}

// RequestedFlowBlock source fields are derived by the snapshot presenter.
type RequestedFlowBlock struct {
	BlockID          string                   `json:"blockId"`
	RepoRelativePath string                   `json:"repoRelativePath"`
	SymbolPath       string                   `json:"symbolPath"`
	LineRange        [2]int                   `json:"lineRange"`
	StepIDs          []string                 `json:"stepIds"`
	Highlights       []RequestedFlowHighlight `json:"highlights"`
}

type RequestedFlowSegment struct {
	SegmentID       string               `json:"segmentId"`
	Purpose         string               `json:"purpose"`
	InclusionReason string               `json:"inclusionReason"`
	StepIDs         []string             `json:"stepIds"`
	EntryStepID     string               `json:"entryStepId"`
	Blocks          []RequestedFlowBlock `json:"blocks"`
}

type RequestedFlowRelation struct {
	Derived       bool           `json:"derived"`
	FromSegmentID string         `json:"fromSegmentId,omitempty"`
	ToSegmentID   string         `json:"toSegmentId,omitempty"`
	FromStepID    string         `json:"fromStepId"`
	ToStepID      string         `json:"toStepId"`
	Kind          string         `json:"kind"`
	Status        string         `json:"status"`
	EdgeIndexes   []int          `json:"edgeIndexes"`
	Edges         []SemanticEdge `json:"edges"`
}

type RequestedFlowBoundary struct {
	FromStepID string `json:"fromStepId"`
	ToStepID   string `json:"toStepId,omitempty"`
	Target     string `json:"target,omitempty"`
	Reason     string `json:"reason"`
}

// RequestedFlowCategory groups execution segments for navigation. Membership
// covers this overview only and makes no claim about complete analysis coverage.
type RequestedFlowCategory struct {
	CategoryID string   `json:"categoryId"`
	Title      string   `json:"title"`
	SegmentIDs []string `json:"segmentIds"`
}

type RequestedFlowOverview struct {
	SchemaID        string                  `json:"schemaId"`
	SchemaVersion   int                     `json:"schemaVersion"`
	Categories      []RequestedFlowCategory `json:"categories,omitempty"`
	Request         string                  `json:"request"`
	SnapshotID      string                  `json:"snapshotId"`
	ComputedBasisID string                  `json:"computedBasisId"`
	Segments        []RequestedFlowSegment  `json:"segments"`
	Relations       []RequestedFlowRelation `json:"relations"`
	Boundaries      []RequestedFlowBoundary `json:"boundaries"`
}

// BuildRequestedFlowOverview verifies membership and execution facts. Source blocks
// still require presenter verification against the same snapshot before storage.
func BuildRequestedFlowOverview(model *SemanticMapIR, sequence *FlowSequence, request string, selections []RequestedFlowSegmentSelection, relationSelections []RequestedFlowRelationSelection, categorySelections ...[]RequestedFlowCategory) (*RequestedFlowOverview, error) {
	if model == nil || sequence == nil || len(model.Steps) == 0 || len(selections) == 0 {
		return nil, fmt.Errorf("invalid_precondition: overview requires analysis, hierarchy, and segments")
	}
	request = strings.TrimSpace(secret.Redact(request).Text)
	if request == "" || model.ValidatedAgainstSnapshotID == "" || model.ComputedBasisID == "" || sequence.SnapshotID != model.ValidatedAgainstSnapshotID || sequence.ComputedBasisID != model.ComputedBasisID {
		return nil, fmt.Errorf("incomparable_basis: overview requires one request and validated snapshot")
	}
	if err := ValidateFlowSequenceReferences(model, sequence); err != nil {
		return nil, fmt.Errorf("invalid_precondition: hierarchy: %w", err)
	}
	steps := make(map[string]SemanticStep, len(model.Steps))
	for _, step := range model.Steps {
		if _, exists := steps[step.StepID]; exists {
			return nil, fmt.Errorf("invalid_precondition: duplicate analysis step")
		}
		steps[step.StepID] = step
	}
	reachable := requestedFlowOverviewReachable(model, steps)
	overview := &RequestedFlowOverview{SchemaID: RequestedFlowOverviewSchemaID, SchemaVersion: 1, Request: request, SnapshotID: model.ValidatedAgainstSnapshotID, ComputedBasisID: model.ComputedBasisID, Segments: []RequestedFlowSegment{}, Relations: []RequestedFlowRelation{}, Boundaries: []RequestedFlowBoundary{}}
	membership := make(map[string]string)
	segmentIDs := make(map[string]bool)
	scope := make(map[string]bool)
	for position, selection := range selections {
		purpose := strings.TrimSpace(secret.Redact(selection.Purpose).Text)
		reason := strings.TrimSpace(secret.Redact(selection.InclusionReason).Text)
		if strings.TrimSpace(selection.SegmentID) == "" || segmentIDs[selection.SegmentID] || purpose == "" || reason == "" || len(selection.StepIDs) == 0 || len(selection.Blocks) == 0 {
			return nil, fmt.Errorf("invalid_precondition: segment requires unique ID, purpose, reason, steps, and blocks")
		}
		segmentIDs[selection.SegmentID] = true
		if position == 0 && selection.StepIDs[0] != model.Steps[0].StepID {
			return nil, fmt.Errorf("invalid_precondition: overview must start at selected entry")
		}
		segment := RequestedFlowSegment{SegmentID: selection.SegmentID, Purpose: purpose, InclusionReason: reason, StepIDs: append([]string(nil), selection.StepIDs...), EntryStepID: selection.StepIDs[0], Blocks: []RequestedFlowBlock{}}
		for _, id := range selection.StepIDs {
			step, exists := steps[id]
			if !exists || !reachable[id] || membership[id] != "" {
				return nil, fmt.Errorf("invalid_precondition: segment step %q is missing, unreachable, or repeated", id)
			}
			if !requestedFlowOverviewSourceVerified(model, step) {
				return nil, fmt.Errorf("invalid_evidence: segment step %q has no exact verified source", id)
			}
			membership[id] = selection.SegmentID
			scope[id] = true
		}
		blockSteps := make(map[string]bool)
		for blockPosition, proposal := range selection.Blocks {
			if len(proposal.StepIDs) == 0 {
				return nil, fmt.Errorf("invalid_precondition: empty overview block")
			}
			first := steps[proposal.StepIDs[0]].Anchor
			for _, id := range proposal.StepIDs {
				step := steps[id]
				if membership[id] != selection.SegmentID || blockSteps[id] || step.Anchor.RepoRelativePath != first.RepoRelativePath || step.Anchor.EnclosingSymbolPath != first.EnclosingSymbolPath {
					return nil, fmt.Errorf("invalid_precondition: blocks must partition segment steps by file and symbol")
				}
				blockSteps[id] = true
			}
			segment.Blocks = append(segment.Blocks, RequestedFlowBlock{BlockID: fmt.Sprintf("%s-block-%d", selection.SegmentID, blockPosition+1), StepIDs: append([]string(nil), proposal.StepIDs...), Highlights: []RequestedFlowHighlight{}})
		}
		if len(blockSteps) != len(selection.StepIDs) {
			return nil, fmt.Errorf("invalid_precondition: blocks omit segment steps")
		}
		overview.Segments = append(overview.Segments, segment)
	}
	if len(categorySelections) > 1 {
		return nil, fmt.Errorf("invalid_precondition: one category proposal is allowed")
	}
	if len(categorySelections) == 1 && categorySelections[0] != nil {
		categories, err := validateRequestedFlowCategories(categorySelections[0], segmentIDs)
		if err != nil {
			return nil, err
		}
		overview.Categories = categories
		overview.SchemaVersion = 2
	}
	for _, segment := range overview.Segments {
		entryVerified := segment.EntryStepID == model.Steps[0].StepID
		for _, edge := range model.Edges {
			if edge.ToStepID == segment.EntryStepID && membership[edge.FromStepID] != segment.SegmentID && reachable[edge.FromStepID] && requestedFlowOverviewExecutionEdge(model, edge, steps) {
				entryVerified = true
			}
		}
		if !entryVerified {
			return nil, fmt.Errorf("invalid_precondition: segment entry must be a verified execution entry")
		}
	}
	for _, selection := range relationSelections {
		if membership[selection.FromStepID] == "" || membership[selection.FromStepID] != selection.FromSegmentID || membership[selection.ToStepID] == "" || membership[selection.ToStepID] != selection.ToSegmentID {
			return nil, fmt.Errorf("invalid_precondition: relation endpoints do not belong to segments")
		}
		relation, err := verifyCodeReadingRelation(model, selection.FromStepID, selection.ToStepID, selection.EdgeIndexes, nil, 0)
		if err != nil {
			return nil, err
		}
		edges := make([]SemanticEdge, 0, len(selection.EdgeIndexes))
		for _, index := range selection.EdgeIndexes {
			edge := model.Edges[index]
			if !requestedFlowOverviewSourceVerified(model, steps[edge.FromStepID]) || !requestedFlowOverviewSourceVerified(model, steps[edge.ToStepID]) {
				return nil, fmt.Errorf("invalid_evidence: relation has unverified intermediate source")
			}
			scope[edge.FromStepID], scope[edge.ToStepID] = true, true
			edges = append(edges, edge)
		}
		overview.Relations = append(overview.Relations, RequestedFlowRelation{FromSegmentID: selection.FromSegmentID, ToSegmentID: selection.ToSegmentID, FromStepID: selection.FromStepID, ToStepID: selection.ToStepID, Kind: relation.Kind, Status: relation.Status, EdgeIndexes: relation.EdgeIndexes, Edges: edges})
	}
	// Include verified intermediate steps on paths from the entry to membership.
	for changed := true; changed; {
		changed = false
		for _, edge := range model.Edges {
			if scope[edge.ToStepID] && !scope[edge.FromStepID] && reachable[edge.FromStepID] && requestedFlowOverviewExecutionEdge(model, edge, steps) {
				scope[edge.FromStepID] = true
				changed = true
			}
		}
	}
	// Follow omitted successors to their outcomes or analysis boundaries. A short
	// segment proposal must not hide later decisions, failure returns, or loops.
	for id := range reachable {
		scope[id] = true
	}
	// Each outgoing graph fact stays visible even when the proposed route omits a
	// decision outcome, callback, or unresolved handoff. Array order proves nothing.
	for index, edge := range model.Edges {
		if !scope[edge.FromStepID] || directReturnBypass(model, edge) {
			continue
		}
		status := "verified"
		if !requestedFlowOverviewExecutionEdge(model, edge, steps) {
			status = "boundary"
			overview.Boundaries = append(overview.Boundaries, RequestedFlowBoundary{FromStepID: edge.FromStepID, ToStepID: edge.ToStepID, Target: secret.Redact(edge.ToSymbolPath).Text, Reason: "unverified_execution_relation"})
		}
		overview.Relations = append(overview.Relations, RequestedFlowRelation{Derived: true, FromSegmentID: membership[edge.FromStepID], ToSegmentID: membership[edge.ToStepID], FromStepID: edge.FromStepID, ToStepID: edge.ToStepID, Kind: edge.Kind, Status: status, EdgeIndexes: []int{index}, Edges: []SemanticEdge{edge}})
	}
	for _, step := range model.Steps {
		if !scope[step.StepID] {
			continue
		}
		for _, unknown := range model.Unknowns {
			matches := unknown.Subject == step.StepID
			for _, rule := range step.Rules {
				target, marked := strings.CutPrefix(rule, "boundary:")
				matches = matches || (marked && target != "" && target == unknown.Subject)
			}
			if matches {
				overview.Boundaries = append(overview.Boundaries, RequestedFlowBoundary{FromStepID: step.StepID, Target: secret.Redact(unknown.Subject).Text, Reason: secret.Redact(unknown.Reason).Text})
			}
		}
	}
	return overview, nil
}

func requestedFlowOverviewSourceVerified(model *SemanticMapIR, step SemanticStep) bool {
	anchor := step.Anchor
	return anchor.RepoRelativePath != "" && anchor.EnclosingSymbolPath != "" && anchor.ByteRange[0] >= 0 && anchor.ByteRange[1] > anchor.ByteRange[0] && anchor.FileHash != "" && anchor.SpanHash != "" && len(verifiedFocusEvidenceRefs(model, step, model.ValidatedAgainstSnapshotID)) > 0
}

func requestedFlowOverviewExecutionEdge(model *SemanticMapIR, edge SemanticEdge, steps map[string]SemanticStep) bool {
	_, targetExists := steps[edge.ToStepID]
	return targetExists && edge.ResolutionStatus == "resolved" && codeReadingExecutionEdge(edge.Kind) && !directReturnBypass(model, edge) && requestedFlowOverviewSourceVerified(model, steps[edge.FromStepID]) && requestedFlowOverviewSourceVerified(model, steps[edge.ToStepID])
}

func requestedFlowOverviewReachable(model *SemanticMapIR, steps map[string]SemanticStep) map[string]bool {
	reachable := map[string]bool{model.Steps[0].StepID: true}
	queue := []string{model.Steps[0].StepID}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, edge := range model.Edges {
			if edge.FromStepID == current && !reachable[edge.ToStepID] && requestedFlowOverviewExecutionEdge(model, edge, steps) {
				reachable[edge.ToStepID] = true
				queue = append(queue, edge.ToStepID)
			}
		}
	}
	return reachable
}

func validateRequestedFlowCategories(proposals []RequestedFlowCategory, segmentIDs map[string]bool) ([]RequestedFlowCategory, error) {
	if len(proposals) == 0 {
		return nil, fmt.Errorf("invalid_precondition: categories must be nonempty when supplied")
	}
	categories := make([]RequestedFlowCategory, 0, len(proposals))
	categoryIDs := make(map[string]bool, len(proposals))
	titles := make(map[string]bool, len(proposals))
	membership := make(map[string]bool, len(segmentIDs))
	for _, proposal := range proposals {
		id := strings.TrimSpace(secret.Redact(proposal.CategoryID).Text)
		title := strings.TrimSpace(secret.Redact(proposal.Title).Text)
		if id == "" || title == "" || categoryIDs[id] || titles[title] || len(proposal.SegmentIDs) == 0 {
			return nil, fmt.Errorf("invalid_precondition: categories require unique nonempty IDs, titles, and segment membership")
		}
		categoryIDs[id], titles[title] = true, true
		for _, segmentID := range proposal.SegmentIDs {
			if !segmentIDs[segmentID] || membership[segmentID] {
				return nil, fmt.Errorf("invalid_precondition: each overview segment must belong to exactly one category")
			}
			membership[segmentID] = true
		}
		categories = append(categories, RequestedFlowCategory{CategoryID: id, Title: title, SegmentIDs: append([]string(nil), proposal.SegmentIDs...)})
	}
	if len(membership) != len(segmentIDs) {
		return nil, fmt.Errorf("invalid_precondition: categories must cover every overview segment")
	}
	return categories, nil
}
