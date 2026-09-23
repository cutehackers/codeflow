package semantic

import "codeflow/internal/collector/slicing"

// groupAssignmentResults uses explicit AST value consumption, not proximity or
// function names. Callee invocations remain independently navigable.
func groupAssignmentResults(sequence *FlowSequence, steps []SemanticStep, connections flowGroupingConnections, boundaries, unknowns map[string]bool) {
	byOrdinal := make(map[int]SemanticStep, len(steps))
	counts := make(map[int]int, len(steps))
	frames := make(map[string]int, len(steps))
	invocationMembers := make(map[string][]SemanticStep)
	returnsByCaller := make(map[int][]SemanticStep)
	for _, step := range steps {
		byOrdinal[step.Ordinal] = step
		counts[step.Ordinal]++
		if step.InvocationID != "" {
			invocationMembers[step.InvocationID] = append(invocationMembers[step.InvocationID], step)
		}
		if step.Kind == "return" && step.CallerStepOrdinal != nil {
			returnsByCaller[*step.CallerStepOrdinal] = append(returnsByCaller[*step.CallerStepOrdinal], step)
		}
	}
	for i, frame := range sequence.Frames {
		if len(frame.StepRefs) == 1 {
			frames[frame.StepRefs[0]] = i
		}
	}
	removed := make(map[int]bool)
	grouped := make(map[string]bool)
	for _, target := range steps {
		if target.AssignmentSourceOrdinal == nil {
			continue
		}
		source, exists := byOrdinal[*target.AssignmentSourceOrdinal]
		if !exists || counts[source.Ordinal] != 1 || counts[target.Ordinal] != 1 || source.InvocationID != target.InvocationID || source.Anchor.RepoRelativePath != target.Anchor.RepoRelativePath || source.Anchor.EnclosingSymbolPath != target.Anchor.EnclosingSymbolPath || slicing.ValidateAssignmentSource(assignmentSliceStep(source), assignmentSliceStep(target)) != nil {
			continue
		}
		from, fromOK := frames[source.StepID]
		to, toOK := frames[target.StepID]
		if !fromOK || !toOK || removed[from] || removed[to] || from >= to || sequence.Frames[from].Role == "entry" {
			continue
		}
		if isStepBoundary(source, boundaries, unknowns) || isStepBoundary(target, boundaries, unknowns) || source.Branch != nil || target.Branch != nil || source.SideEffect != nil || target.SideEffect != nil || source.StateDelta != nil {
			continue
		}
		valid := true
		local := 0
		for _, edge := range connections.outgoing[source.StepID] {
			if edge.Kind == "external" || edge.Kind == "async" || edge.ResolutionStatus != "resolved" {
				valid = false
			}
			if edge.Kind == "control_flow" {
				local++
				if edge.ToStepID != target.StepID || len(edge.Conditions) != 0 {
					valid = false
				}
			}
		}
		incoming := 0
		for _, edge := range connections.incoming[target.StepID] {
			if edge.Kind != "return" {
				incoming++
			}
		}
		if local != 1 || incoming != 1 {
			continue
		}
		for _, edge := range connections.outgoing[target.StepID] {
			if edge.Kind == "external" || edge.Kind == "async" || edge.ResolutionStatus != "resolved" {
				valid = false
			}
		}
		if !valid || sequence.Frames[from].Role == "effect" || sequence.Frames[to].Role != "process" {
			continue
		}
		memberRefs := []string{source.StepID, target.StepID}
		memberFrames := []int{from, to}
		groupingReason := "호출 결과를 받아 대입하는 처리"
		if len(returnsByCaller[source.Ordinal]) > 0 {
			returned, returnedFrame, ok := singleReturnedAssignmentValue(source, target, invocationMembers, returnsByCaller, frames, sequence, connections, boundaries, unknowns, removed)
			if !ok {
				// A normal return occurs between the caller and its continuation.
				// Keeping only the endpoints together would make frame reading order
				// diverge from the execution timeline and hide the callee boundary.
				continue
			}
			memberRefs = []string{source.StepID, returned.StepID, target.StepID}
			memberFrames = []int{from, returnedFrame, to}
			groupingReason = "호출의 단일 반환값을 받아 대입하는 처리"
		}
		frame := sequence.Frames[to]
		frame.FrameID = sequence.Frames[from].FrameID
		frame.StepRefs = memberRefs
		frame.Text = groupingReason
		frame.CollapsedDetail = &CollapsedDetail{Count: len(memberRefs) - 1, Reason: groupingReason}
		if sequence.Frames[from].Status == "unknown" {
			frame.Status = "unknown"
		} else if sequence.Frames[from].Status != "verified" && frame.Status == "verified" {
			frame.Status = "partial"
		}
		for _, memberFrame := range memberFrames {
			grouped[sequence.Frames[memberFrame].FrameID] = true
			if memberFrame != from {
				removed[memberFrame] = true
			}
		}
		sequence.Frames[from] = frame
	}
	result := make([]FlowSequenceFrame, 0, len(sequence.Frames)-len(removed))
	for i, frame := range sequence.Frames {
		if !removed[i] {
			frame.Ordinal = len(result) + 1
			result = append(result, frame)
		}
	}
	sequence.Frames = result
	var limitations []FlowSummaryLimitation
	for _, limitation := range sequence.SummaryLimitations {
		if limitation.Code == "grouping_evidence_missing" {
			refs := make([]string, 0, len(limitation.FrameRefs))
			for _, ref := range limitation.FrameRefs {
				if !grouped[ref] {
					refs = append(refs, ref)
				}
			}
			limitation.FrameRefs = refs
		}
		if len(limitation.FrameRefs) > 0 {
			limitations = append(limitations, limitation)
		}
	}
	sequence.SummaryLimitations = limitations
}

// singleReturnedAssignmentValue proves that a direct call, its entire invoked
// body, and its result assignment describe one value transfer. A callee with
// any additional step could contain a decision, effect, or other independent
// processing, so it remains separately navigable.
func singleReturnedAssignmentValue(source, target SemanticStep, invocationMembers map[string][]SemanticStep, returnsByCaller map[int][]SemanticStep, frames map[string]int, sequence *FlowSequence, connections flowGroupingConnections, boundaries, unknowns map[string]bool, removed map[int]bool) (SemanticStep, int, bool) {
	returns := returnsByCaller[source.Ordinal]
	if len(returns) != 1 {
		return SemanticStep{}, 0, false
	}
	returned := returns[0]
	members := invocationMembers[returned.InvocationID]
	if returned.InvocationID == "" || len(members) != 1 || members[0].StepID != returned.StepID || returned.Ordinal <= source.Ordinal || returned.Ordinal >= target.Ordinal || isStepBoundary(returned, boundaries, unknowns) {
		return SemanticStep{}, 0, false
	}
	returnedFrame, exists := frames[returned.StepID]
	if !exists || removed[returnedFrame] || sequence.Frames[returnedFrame].Role == "boundary" || sequence.Frames[returnedFrame].Role == "effect" {
		return SemanticStep{}, 0, false
	}
	called, resumed := false, false
	for _, edge := range connections.outgoing[source.StepID] {
		if edge.ResolutionStatus != "resolved" || len(edge.Conditions) != 0 {
			continue
		}
		if edge.Kind == "resolved_cross_file" && edge.ToStepID == returned.StepID {
			called = true
		}
	}
	for _, edge := range connections.outgoing[returned.StepID] {
		if edge.Kind == "return" && edge.ResolutionStatus == "resolved" && edge.ToStepID == target.StepID {
			resumed = true
		}
	}
	return returned, returnedFrame, called && resumed
}
