package semantic

import (
	"codeflow/internal/collector/slicing"
	"fmt"
	"reflect"
	"strings"
)

// ValidateFlowSequenceReferences checks the hierarchy against its source analysis.
// Schema validation remains the caller's responsibility. This does not infer
// execution edges from child order or change historical stored projections.
func ValidateFlowSequenceReferences(mapIR *SemanticMapIR, sequence *FlowSequence) error {
	if mapIR == nil || sequence == nil {
		return fmt.Errorf("analysis and FlowSequence are required")
	}
	snapshotID := mapIR.ValidatedAgainstSnapshotID
	if snapshotID == "" {
		snapshotID = mapIR.Basis.ComputedWorkspaceSnapshotID
	}
	if snapshotID == "" {
		snapshotID = mapIR.ComputedBasisID
	}
	flowID := strings.TrimPrefix(mapIR.MapID, "map-")
	if flowID == "" {
		flowID = mapIR.GenerationID
	}
	if flowID == "" {
		flowID = mapIR.ComputedBasisID
	}
	if sequence.FlowID != flowID || sequence.GenerationID != mapIR.GenerationID || sequence.ComputedBasisID != mapIR.ComputedBasisID || sequence.SnapshotID != snapshotID {
		return fmt.Errorf("FlowSequence identity does not match analysis")
	}
	if err := validateInvocationRelations(mapIR); err != nil {
		return err
	}
	assignmentConnections := groupingConnections(mapIR.Edges)
	byOrdinal := make(map[int]SemanticStep)
	counts := make(map[int]int)
	for _, step := range mapIR.Steps {
		byOrdinal[step.Ordinal] = step
		counts[step.Ordinal]++
	}
	for _, step := range mapIR.Steps {
		if step.AssignmentSourceOrdinal == nil {
			continue
		}
		source, exists := byOrdinal[*step.AssignmentSourceOrdinal]
		if !exists || counts[source.Ordinal] != 1 || counts[step.Ordinal] != 1 {
			return fmt.Errorf("invalid assignment source ordinal")
		}
		if err := slicing.ValidateAssignmentSource(assignmentSliceStep(source), assignmentSliceStep(step)); err != nil {
			return err
		}
		connected := false
		for _, edge := range assignmentConnections.outgoing[source.StepID] {
			if edge.FromStepID == source.StepID && edge.ToStepID == step.StepID && edge.Kind == "control_flow" && edge.ResolutionStatus == "resolved" && len(edge.Conditions) == 0 {
				connected = true
			}
		}
		if !connected {
			return fmt.Errorf("assignment source has no direct continuation")
		}
	}
	stepPositions := make(map[string]int, len(mapIR.Steps))
	for i, step := range mapIR.Steps {
		if step.StepID == "" {
			return fmt.Errorf("analysis step %d has no ID", i+1)
		}
		if _, exists := stepPositions[step.StepID]; exists {
			return fmt.Errorf("duplicate analysis step %q", step.StepID)
		}
		stepPositions[step.StepID] = i
	}
	for _, edge := range mapIR.Edges {
		if len(edge.Conditions) == 0 {
			continue
		}
		if len(edge.Conditions) != 1 || (edge.Kind != "control_flow" && edge.Kind != "loop_back" && edge.Kind != "loop_reentry") || edge.ResolutionStatus != "resolved" {
			return fmt.Errorf("invalid branch condition relation")
		}
		condition := edge.Conditions[0]
		from, sourceExists := stepPositions[edge.FromStepID]
		to, targetExists := stepPositions[edge.ToStepID]
		if !sourceExists || !targetExists || condition.StepID != edge.FromStepID || (condition.Outcome != "truthy" && condition.Outcome != "falsy" && condition.Outcome != "nullish" && condition.Outcome != "non_nullish") {
			return fmt.Errorf("invalid branch condition reference")
		}
		source, target := mapIR.Steps[from], mapIR.Steps[to]
		if (source.Kind != "branch" && source.Kind != "guard") || source.InvocationID == "" || source.InvocationID != target.InvocationID || source.Anchor.RepoRelativePath != target.Anchor.RepoRelativePath || source.Anchor.EnclosingSymbolPath != target.Anchor.EnclosingSymbolPath {
			return fmt.Errorf("invalid branch condition endpoints")
		}
	}
	unknownSubjects := make(map[string]bool, len(mapIR.Unknowns))
	for _, unknown := range mapIR.Unknowns {
		if unknown.Subject != "" {
			unknownSubjects[unknown.Subject] = true
		}
	}
	evidence := make(map[string]SemanticEvidence, len(mapIR.Evidence))
	for _, item := range mapIR.Evidence {
		evidence[item.EvidenceID] = item
	}
	boundaryTargets := make(map[string]bool, len(mapIR.BoundaryTargets))
	for _, target := range mapIR.BoundaryTargets {
		boundaryTargets[target] = true
	}
	parents := make(map[string]string, len(mapIR.Steps))
	frameIDs := make(map[string]bool, len(sequence.Frames))
	previousFramePosition := -1
	for i, frame := range sequence.Frames {
		if frame.FrameID == "" || frameIDs[frame.FrameID] {
			return fmt.Errorf("empty or duplicate frame ID %q", frame.FrameID)
		}
		frameIDs[frame.FrameID] = true
		if frame.Ordinal != i+1 {
			return fmt.Errorf("frame %q ordinal does not match reading order", frame.FrameID)
		}
		if len(frame.StepRefs) == 0 {
			return fmt.Errorf("frame %q has no children", frame.FrameID)
		}
		primaryFound := false
		previousPosition := -1
		for _, ref := range frame.StepRefs {
			position, exists := stepPositions[ref]
			if !exists {
				return fmt.Errorf("frame %q references missing step %q", frame.FrameID, ref)
			}
			if parent, exists := parents[ref]; exists {
				return fmt.Errorf("step %q belongs to both %q and %q", ref, parent, frame.FrameID)
			}
			if position <= previousPosition {
				return fmt.Errorf("frame %q children do not preserve analysis reading order", frame.FrameID)
			}
			if previousPosition == -1 {
				if position <= previousFramePosition {
					return fmt.Errorf("frame %q does not preserve analysis reading order", frame.FrameID)
				}
				previousFramePosition = position
			}
			previousPosition = position
			step := mapIR.Steps[position]
			if frame.Status == "verified" && (flowStepStatus(step, unknownSubjects, evidence) != "verified" || isStepBoundary(step, boundaryTargets, unknownSubjects)) {
				return fmt.Errorf("frame %q promotes unverified step %q", frame.FrameID, ref)
			}
			if ref == frame.PrimaryStepRef && frame.SourceAnchor != nil && !reflect.DeepEqual(*frame.SourceAnchor, step.Anchor) {
				return fmt.Errorf("frame %q source differs from its representative", frame.FrameID)
			}
			parents[ref] = frame.FrameID
			primaryFound = primaryFound || ref == frame.PrimaryStepRef
		}
		if !primaryFound {
			return fmt.Errorf("frame %q representative is not a child", frame.FrameID)
		}
	}
	for _, limitation := range sequence.SummaryLimitations {
		seen := make(map[string]bool, len(limitation.FrameRefs))
		for _, ref := range limitation.FrameRefs {
			if !frameIDs[ref] || seen[ref] {
				return fmt.Errorf("summary limitation references missing or duplicate frame %q", ref)
			}
			seen[ref] = true
		}
	}

	for _, step := range mapIR.Steps {
		if _, exists := parents[step.StepID]; !exists {
			return fmt.Errorf("analysis step %q has no parent", step.StepID)
		}
	}
	return nil
}

func assignmentSliceStep(step SemanticStep) slicing.SliceStep {
	return slicing.SliceStep{Ordinal: step.Ordinal, Kind: step.Kind, InvocationID: step.InvocationID, Anchor: step.Anchor}
}

// validateInvocationRelations preserves legacy relation-only maps while checking
// the full call/continuation proof for invocation-aware return facts.
func validateInvocationRelations(model *SemanticMapIR) error {
	byID := make(map[string]SemanticStep)
	invocationAware := false
	for _, step := range model.Steps {
		byID[step.StepID] = step
		invocationAware = invocationAware || step.InvocationID != "" || step.CallerStepOrdinal != nil
	}
	required := false
	for _, step := range model.Steps {
		required = required || step.Kind == "await" || step.Kind == "break" || step.Kind == "continue"
	}
	for _, edge := range model.Edges {
		if (edge.Kind == "return" && invocationAware) || edge.Kind == "failure" || edge.Kind == "parallel_wait" || edge.Kind == "await_resume" || edge.Kind == "await_loop_back" || edge.Kind == "loop_back" || edge.Kind == "loop_reentry" || edge.Kind == "loop_exit" || edge.Kind == "loop_exit_back" {
			required = true
		}
	}
	if !required {
		return nil
	}
	var steps []slicing.SliceStep
	for _, step := range model.Steps {
		steps = append(steps, slicing.SliceStep{Ordinal: step.Ordinal, Kind: step.Kind, InvocationID: step.InvocationID, CallerStepOrdinal: step.CallerStepOrdinal, SymbolPath: step.Anchor.EnclosingSymbolPath, Anchor: step.Anchor})
	}
	var edges []slicing.SliceEdge
	for _, edge := range model.Edges {
		source, sourceOK := byID[edge.FromStepID]
		target, targetOK := byID[edge.ToStepID]
		relation := slicing.SliceEdge{Kind: edge.Kind, ToSymbolPath: edge.ToSymbolPath, ResolutionStatus: edge.ResolutionStatus}
		if sourceOK {
			ordinal := source.Ordinal
			relation.StepOrdinal = &ordinal
		}
		if targetOK {
			ordinal := target.Ordinal
			relation.TargetStepOrdinal = &ordinal
		}
		for _, condition := range edge.Conditions {
			relation.Conditions = append(relation.Conditions, slicing.BranchCondition{StepOrdinal: byID[condition.StepID].Ordinal, Outcome: condition.Outcome})
		}
		edges = append(edges, relation)
	}
	return slicing.ValidateExecutionReferences(steps, edges)
}
