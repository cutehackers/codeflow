package semantic

import (
	"fmt"
	"slices"
	"strings"

	"codeflow/internal/collector/secret"
	"codeflow/internal/collector/slicing"
)

const CodeReadingPathSchemaID = "https://codeflow.local/schemas/code-reading-path.schema.json"

// CodeReadingSelection is the selector's proposal. The curator derives every
// source location and relationship from the verified analysis, not this input.
type CodeReadingSelection struct {
	StepID          string `json:"stepId"`
	Role            string `json:"role"`
	RequestPart     string `json:"requestPart"`
	InclusionReason string `json:"inclusionReason"`
}

type CodeReadingRelationSelection struct {
	FromStepID  string `json:"fromStepId"`
	ToStepID    string `json:"toStepId"`
	EdgeIndexes []int  `json:"edgeIndexes"`
}

type CodeReadingRelation struct {
	FromNodeID  string `json:"fromNodeId"`
	ToNodeID    string `json:"toNodeId"`
	Kind        string `json:"kind"`
	Status      string `json:"status"`
	EdgeIndexes []int  `json:"edgeIndexes"`
}

type CodeReadingNode struct {
	NodeID            string         `json:"nodeId"`
	StepID            string         `json:"stepId"`
	FrameID           string         `json:"frameId"`
	Symbol            string         `json:"symbol"`
	SourceAnchor      slicing.Anchor `json:"sourceAnchor"`
	Role              string         `json:"role"`
	RequestPart       string         `json:"requestPart"`
	InclusionReason   string         `json:"inclusionReason"`
	PreviousNodeIDs   []string       `json:"previousNodeIds"`
	NextNodeIDs       []string       `json:"nextNodeIds"`
	Status            string         `json:"status"`
	EvidenceRefs      []string       `json:"evidenceRefs"`
	CodeViewAvailable bool           `json:"codeViewAvailable"`
}

// CodeReadingPath is the curated default route through exact source. The full
// SemanticMapIR and FlowSequence remain available for detail exploration.
type CodeReadingPath struct {
	SchemaID        string                `json:"schemaId"`
	SchemaVersion   int                   `json:"schemaVersion"`
	Request         string                `json:"request"`
	SnapshotID      string                `json:"snapshotId"`
	ComputedBasisID string                `json:"computedBasisId"`
	Nodes           []CodeReadingNode     `json:"nodes"`
	Relations       []CodeReadingRelation `json:"relations"`
}

// BuildCodeReadingPath verifies an agent's request-specific selection against
// the current graph, hierarchy, source evidence, and confirmed direct edges.
// The presenter must additionally verify CodeView before saving the path.
func BuildCodeReadingPath(model *SemanticMapIR, sequence *FlowSequence, request string, focus *RequestedFlowFocus, selections []CodeReadingSelection, relationSelections []CodeReadingRelationSelection) (*CodeReadingPath, error) {
	if model == nil || sequence == nil || len(model.Steps) == 0 || len(selections) == 0 {
		return nil, fmt.Errorf("invalid_precondition: code reading path requires analysis, hierarchy, and selected code")
	}
	request = strings.TrimSpace(secret.Redact(request).Text)
	if request == "" || model.ValidatedAgainstSnapshotID == "" || model.ComputedBasisID == "" ||
		sequence.SnapshotID != model.ValidatedAgainstSnapshotID || sequence.ComputedBasisID != model.ComputedBasisID {
		return nil, fmt.Errorf("incomparable_basis: code reading path requires one request and validated snapshot")
	}
	if err := ValidateFlowSequenceReferences(model, sequence); err != nil {
		return nil, fmt.Errorf("invalid_precondition: hierarchy: %w", err)
	}
	stepByID := make(map[string]SemanticStep, len(model.Steps))
	for _, step := range model.Steps {
		stepByID[step.StepID] = step
	}
	frameByStep := make(map[string]string, len(model.Steps))
	for _, frame := range sequence.Frames {
		for _, ref := range frame.StepRefs {
			frameByStep[ref] = frame.FrameID
		}
	}
	path := &CodeReadingPath{
		SchemaID: CodeReadingPathSchemaID, SchemaVersion: 2, Request: request, SnapshotID: model.ValidatedAgainstSnapshotID,
		ComputedBasisID: model.ComputedBasisID, Nodes: make([]CodeReadingNode, 0, len(selections)),
		Relations: make([]CodeReadingRelation, 0, len(relationSelections)),
	}
	seen := make(map[string]bool, len(selections))
	for index, selection := range selections {
		step, exists := stepByID[selection.StepID]
		if !exists || seen[selection.StepID] {
			return nil, fmt.Errorf("unresolved_target: code reading path has missing or duplicate step %q", selection.StepID)
		}
		seen[selection.StepID] = true
		if index == 0 && (selection.StepID != model.Steps[0].StepID || selection.Role != "entry") {
			return nil, fmt.Errorf("invalid_precondition: code reading path must start at the selected entry")
		}
		if !validCodeReadingRole(model, step, selection.Role, index == 0) {
			return nil, fmt.Errorf("invalid_precondition: role %q does not match selected code %q", selection.Role, selection.StepID)
		}
		requestPart := strings.TrimSpace(selection.RequestPart)
		reason := strings.TrimSpace(secret.Redact(selection.InclusionReason).Text)
		if requestPart == "" || !strings.Contains(request, requestPart) || reason == "" {
			return nil, fmt.Errorf("invalid_precondition: selected code requires an exact request phrase and inclusion reason")
		}
		anchor := step.Anchor
		if anchor.RepoRelativePath == "" || anchor.EnclosingSymbolPath == "" || anchor.ByteRange[0] < 0 || anchor.ByteRange[1] <= anchor.ByteRange[0] || anchor.FileHash == "" || anchor.SpanHash == "" {
			return nil, fmt.Errorf("invalid_evidence: selected code has no exact statement source range")
		}
		refs := verifiedFocusEvidenceRefs(model, step, model.ValidatedAgainstSnapshotID)
		if len(refs) == 0 {
			return nil, fmt.Errorf("invalid_evidence: selected code %q has no verified source", selection.StepID)
		}
		node := CodeReadingNode{
			NodeID: selection.StepID, StepID: selection.StepID, FrameID: frameByStep[selection.StepID],
			Symbol: anchor.RepoRelativePath + "#" + anchor.EnclosingSymbolPath, SourceAnchor: anchor,
			Role: selection.Role, RequestPart: requestPart, InclusionReason: reason,
			Status: "verified", EvidenceRefs: refs,
		}
		node.PreviousNodeIDs = []string{}
		node.NextNodeIDs = []string{}
		path.Nodes = append(path.Nodes, node)
	}
	position := make(map[string]int, len(path.Nodes))
	for index, node := range path.Nodes {
		position[node.StepID] = index
	}
	seenRelations := make(map[string]bool, len(relationSelections))
	for _, selection := range relationSelections {
		from, fromExists := position[selection.FromStepID]
		to, toExists := position[selection.ToStepID]
		if !fromExists || !toExists || from >= to {
			return nil, fmt.Errorf("invalid_precondition: code reading relation must connect selected code in reading order")
		}
		key := selection.FromStepID + "\x00" + selection.ToStepID
		if seenRelations[key] {
			return nil, fmt.Errorf("invalid_precondition: duplicate code reading relation")
		}
		seenRelations[key] = true
		relation, err := verifyCodeReadingRelation(model, selection.FromStepID, selection.ToStepID, selection.EdgeIndexes, position, from)
		if err != nil {
			return nil, err
		}
		for index := 0; index < len(selection.EdgeIndexes)-1; index++ {
			intermediate := model.Edges[selection.EdgeIndexes[index]].ToStepID
			if selectedPosition, selected := position[intermediate]; selected &&
				!(selectedPosition < from && codeReadingLoopRevisit(model.Edges[selection.EdgeIndexes[index]].Kind)) {
				return nil, fmt.Errorf("invalid_precondition: code reading relation skips selected code")
			}
		}
		relation.FromNodeID = selection.FromStepID
		relation.ToNodeID = selection.ToStepID
		path.Relations = append(path.Relations, *relation)
		path.Nodes[from].NextNodeIDs = append(path.Nodes[from].NextNodeIDs, selection.ToStepID)
		path.Nodes[to].PreviousNodeIDs = append(path.Nodes[to].PreviousNodeIDs, selection.FromStepID)
	}
	for index, node := range path.Nodes {
		if index > 0 && len(node.PreviousNodeIDs) == 0 {
			return nil, fmt.Errorf("invalid_precondition: selected code is disconnected from the entry")
		}
		if len(node.NextNodeIDs) == 0 && node.Role != "outcome" && node.Role != "boundary" {
			return nil, fmt.Errorf("invalid_precondition: every reading branch must end at an outcome or analysis boundary")
		}
	}
	if focus != nil {
		callIndex, entryIndex := slices.IndexFunc(path.Nodes, func(node CodeReadingNode) bool { return node.StepID == focus.CallStepID }),
			slices.IndexFunc(path.Nodes, func(node CodeReadingNode) bool { return node.StepID == focus.EntryStepID })
		if callIndex < 0 || entryIndex <= callIndex || focus.SnapshotID != path.SnapshotID ||
			!slices.Contains(path.Nodes[callIndex].NextNodeIDs, focus.EntryStepID) {
			return nil, fmt.Errorf("invalid_precondition: code reading path omits the verified requested implementation")
		}
		direct := slices.ContainsFunc(path.Relations, func(relation CodeReadingRelation) bool {
			return relation.FromNodeID == focus.CallStepID && relation.ToNodeID == focus.EntryStepID &&
				len(relation.EdgeIndexes) == 1 && model.Edges[relation.EdgeIndexes[0]].Kind == "resolved_cross_file"
		})
		if !direct {
			return nil, fmt.Errorf("invalid_evidence: requested implementation needs its verified direct call")
		}
	}
	return path, nil
}

func validCodeReadingRole(model *SemanticMapIR, step SemanticStep, role string, first bool) bool {
	if first {
		return role == "entry"
	}
	switch role {
	case "decision":
		return step.Kind == "guard" || step.Kind == "branch" || step.Kind == "decision"
	case "transform":
		return step.Kind == "mutation"
	case "state_change":
		return step.Kind == "mutation" && step.ValueRole != "local_binding"
	case "effect":
		if step.Kind == "external_effect" || step.SideEffect != nil {
			return true
		}
		for _, edge := range model.Edges {
			if edge.FromStepID == step.StepID && edge.ResolutionStatus == "resolved" && (edge.Kind == "external" || edge.Kind == "async") {
				return true
			}
		}
		return false
	case "handoff":
		return step.Kind == "call" || step.Kind == "await" || step.Kind == "break" || step.Kind == "continue"
	case "outcome":
		return step.Kind == "return" || step.Kind == "throw" || step.Kind == "mutation" || step.Kind == "external_effect"
	case "boundary":
		for _, edge := range model.Edges {
			if edge.FromStepID == step.StepID && (edge.ResolutionStatus != "resolved" || edge.Kind == "callback_argument") {
				return true
			}
		}
		for _, unknown := range model.Unknowns {
			if unknown.Subject == step.StepID {
				return true
			}
			for _, rule := range step.Rules {
				if target, marked := strings.CutPrefix(rule, "boundary:"); marked && target != "" && unknown.Subject == target {
					return true
				}
			}
		}
		return false
	default:
		return false
	}
}

func verifyCodeReadingRelation(model *SemanticMapIR, fromStepID, toStepID string, indexes []int, selected map[string]int, fromPosition int) (*CodeReadingRelation, error) {
	if len(indexes) == 0 {
		return nil, fmt.Errorf("unresolved_target: code reading path has no verified relation between selected code")
	}
	steps := make(map[string]SemanticStep, len(model.Steps))
	for _, step := range model.Steps {
		steps[step.StepID] = step
	}
	current := fromStepID
	seen := make(map[int]bool, len(indexes))
	kind := "control_flow"
	for position, index := range indexes {
		if index < 0 || index >= len(model.Edges) || seen[index] {
			return nil, fmt.Errorf("invalid_evidence: relation edge index is missing or repeated")
		}
		seen[index] = true
		edge := model.Edges[index]
		if edge.FromStepID != current || edge.ToStepID == "" || edge.ResolutionStatus != "resolved" ||
			!codeReadingExecutionEdge(edge.Kind) || directReturnBypass(model, edge) {
			return nil, fmt.Errorf("invalid_evidence: selected code relation is not a confirmed execution edge")
		}
		current = edge.ToStepID
		if position < len(indexes)-1 {
			step, exists := steps[current]
			if !exists {
				return nil, fmt.Errorf("invalid_evidence: code reading relation crosses a missing step")
			}
			selectedPosition, revisited := selected[current]
			if requiredCodeReadingPoint(step) && !(revisited && selectedPosition < fromPosition && codeReadingLoopRevisit(edge.Kind)) &&
				(len(verifiedFocusEvidenceRefs(model, step, model.ValidatedAgainstSnapshotID)) == 0 ||
					step.Anchor.RepoRelativePath == "" || step.Anchor.EnclosingSymbolPath == "" || step.Anchor.ByteRange[1] <= step.Anchor.ByteRange[0]) {
				return nil, fmt.Errorf("invalid_evidence: code reading relation cannot show intermediate code point %q", step.StepID)
			}
		}
		if edge.Kind == "async" || edge.Kind == "await_resume" || edge.Kind == "await_loop_back" {
			kind = "async_handoff"
		} else if (len(edge.Conditions) > 0 || codeReadingLoopRevisit(edge.Kind)) && kind != "async_handoff" {
			kind = "conditional"
		} else if edge.Kind == "resolved_cross_file" && kind == "control_flow" {
			kind = "direct_call"
		}
	}
	if current != toStepID {
		return nil, fmt.Errorf("unresolved_target: relation does not reach the selected code")
	}
	return &CodeReadingRelation{Kind: kind, Status: "verified", EdgeIndexes: append([]int(nil), indexes...)}, nil
}

func codeReadingLoopRevisit(kind string) bool {
	switch kind {
	case "loop_back", "loop_reentry", "loop_exit_back", "await_loop_back":
		return true
	default:
		return false
	}
}

func requiredCodeReadingPoint(step SemanticStep) bool {
	if step.SideEffect != nil {
		return true
	}
	if step.Kind == "mutation" && step.ValueRole != "local_binding" {
		return true
	}
	switch step.Kind {
	case "guard", "branch", "decision", "call", "await", "throw", "return", "break", "continue", "external_effect":
		return true
	default:
		return false
	}
}

// The adapter retains a caller-local continuation to prove where a synchronous
// call resumes. Once a verified callee return reaches it, that local edge is
// structural evidence, not a route that can skip the callee's execution.
func directReturnBypass(model *SemanticMapIR, candidate SemanticEdge) bool {
	if candidate.Kind != "control_flow" || candidate.ResolutionStatus != "resolved" {
		return false
	}
	var caller *SemanticStep
	for index := range model.Steps {
		if model.Steps[index].StepID == candidate.FromStepID && model.Steps[index].Kind == "call" {
			caller = &model.Steps[index]
			break
		}
	}
	if caller == nil {
		return false
	}
	for _, returned := range model.Steps {
		if returned.Kind != "return" || returned.CallerStepOrdinal == nil || *returned.CallerStepOrdinal != caller.Ordinal ||
			returned.InvocationID == "" || returned.InvocationID == caller.InvocationID {
			continue
		}
		for _, relation := range model.Edges {
			if relation.Kind == "return" && relation.ResolutionStatus == "resolved" &&
				relation.FromStepID == returned.StepID && relation.ToStepID == candidate.ToStepID {
				return true
			}
		}
	}
	return false
}

func codeReadingExecutionEdge(kind string) bool {
	switch kind {
	case "control_flow", "resolved_cross_file", "return", "failure", "finally", "async", "await_resume", "await_loop_back",
		"loop_back", "loop_reentry", "loop_exit", "loop_exit_back", "switch_exit", "external", "parallel_wait", "call", "calls":
		return true
	default:
		return false
	}
}
