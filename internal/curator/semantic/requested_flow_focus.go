package semantic

import (
	"fmt"
	"slices"
	"strings"

	"codeflow/internal/collector/secret"
	"codeflow/internal/collector/slicing"
)

// RequestedFlowFocus records a caller-selected implementation target after
// its direct call and source evidence have been verified in one analysis.
type RequestedFlowFocus struct {
	SymbolPath   string   `json:"symbolPath"`
	Reason       string   `json:"reason"`
	CallStepID   string   `json:"callStepId"`
	EntryStepID  string   `json:"entryStepId"`
	SnapshotID   string   `json:"snapshotId"`
	EvidenceRefs []string `json:"evidenceRefs"`
}

// ResolveRequestedFlowFocus rejects a target that cannot be tied to one
// reachable, directly verified invocation and exact source evidence.
func ResolveRequestedFlowFocus(model *SemanticMapIR, symbolPath, reason string) (*RequestedFlowFocus, error) {
	symbolPath, reason = strings.TrimSpace(symbolPath), strings.TrimSpace(reason)
	if symbolPath == "" && reason == "" {
		return nil, nil
	}
	if symbolPath == "" || reason == "" || model == nil || len(model.Steps) == 0 {
		return nil, fmt.Errorf("invalid_precondition: requested focus requires a symbol, reason, and analysis")
	}
	snapshotID := model.ValidatedAgainstSnapshotID
	if snapshotID == "" {
		return nil, fmt.Errorf("invalid_precondition: requested focus requires a validated snapshot")
	}
	steps := make(map[string]SemanticStep, len(model.Steps))
	for _, step := range model.Steps {
		steps[step.StepID] = step
	}
	reachable := reachableFocusSteps(model, steps)
	var match *SemanticEdge
	var evidenceRefs []string
	missingEvidence := false
	for index := range model.Edges {
		edge := &model.Edges[index]
		if edge.Kind != "resolved_cross_file" || edge.ResolutionStatus != "resolved" || edge.ToSymbolPath != symbolPath || !reachable[edge.FromStepID] {
			continue
		}
		caller, callerExists := steps[edge.FromStepID]
		entry, entryExists := steps[edge.ToStepID]
		if !callerExists || !entryExists || caller.Kind != "call" || caller.InvocationID == "" || entry.InvocationID == "" || caller.InvocationID == entry.InvocationID ||
			entry.CallerStepOrdinal == nil || *entry.CallerStepOrdinal != caller.Ordinal ||
			entry.Anchor.RepoRelativePath+"#"+entry.Anchor.EnclosingSymbolPath != symbolPath {
			continue
		}
		callEvidence := verifiedFocusEvidenceRefs(model, caller, snapshotID)
		entryEvidence := verifiedFocusEvidenceRefs(model, entry, snapshotID)
		if len(callEvidence) == 0 || len(entryEvidence) == 0 {
			missingEvidence = true
			continue
		}
		if match != nil && (match.FromStepID != edge.FromStepID || match.ToStepID != edge.ToStepID) {
			return nil, fmt.Errorf("ambiguous_target: requested focus has multiple verified call sites")
		}
		match = edge
		evidenceRefs = append([]string(nil), callEvidence...)
		for _, ref := range entryEvidence {
			if !slices.Contains(evidenceRefs, ref) {
				evidenceRefs = append(evidenceRefs, ref)
			}
		}
	}
	if match == nil {
		if missingEvidence {
			return nil, fmt.Errorf("invalid_evidence: requested focus has no verified call and source ranges")
		}
		return nil, fmt.Errorf("unresolved_target: requested focus has no reachable verified direct call")
	}
	return &RequestedFlowFocus{
		SymbolPath: symbolPath, Reason: secret.Redact(reason).Text,
		CallStepID: match.FromStepID, EntryStepID: match.ToStepID,
		SnapshotID: snapshotID, EvidenceRefs: evidenceRefs,
	}, nil
}

func verifiedFocusEvidenceRefs(model *SemanticMapIR, step SemanticStep, snapshotID string) []string {
	refs := make([]string, 0, len(step.EvidenceRefs))
	for _, ref := range step.EvidenceRefs {
		for _, evidence := range model.Evidence {
			if evidence.EvidenceID == ref && evidence.SnapshotID == snapshotID && evidence.ValidationStatus == "verified" &&
				evidence.SourceValidationStatus == "verified" && sameFocusAnchor(evidence.Anchor, step.Anchor) {
				refs = append(refs, ref)
				break
			}
		}
	}
	return refs
}

func sameFocusAnchor(left, right slicing.Anchor) bool {
	return left.RepoRelativePath == right.RepoRelativePath && left.EnclosingSymbolPath == right.EnclosingSymbolPath &&
		left.ByteRange == right.ByteRange && left.FileHash == right.FileHash && left.SpanHash == right.SpanHash
}

func reachableFocusSteps(model *SemanticMapIR, steps map[string]SemanticStep) map[string]bool {
	reachable := make(map[string]bool, len(steps))
	if len(model.Steps) == 0 {
		return reachable
	}
	queue := []string{model.Steps[0].StepID}
	reachable[queue[0]] = true
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, edge := range model.Edges {
			if edge.FromStepID != current || edge.ResolutionStatus != "resolved" || edge.Kind == "callback_argument" || reachable[edge.ToStepID] {
				continue
			}
			if _, exists := steps[edge.ToStepID]; !exists {
				continue
			}
			reachable[edge.ToStepID] = true
			queue = append(queue, edge.ToStepID)
		}
	}
	return reachable
}
