package semantic

// groupExpressionEvaluations groups source-contained evaluation steps without
// replacing execution edges or hiding independently unresolved calls.
func groupExpressionEvaluations(sequence *FlowSequence, steps []SemanticStep, connections flowGroupingConnections, boundaries, unknowns map[string]bool) {
	frames := make(map[string]int, len(steps))
	invocations := make(map[string][]SemanticStep)
	for i, frame := range sequence.Frames {
		if len(frame.StepRefs) == 1 {
			frames[frame.StepRefs[0]] = i
		}
	}
	for _, step := range steps {
		if step.InvocationID != "" {
			invocations[step.InvocationID] = append(invocations[step.InvocationID], step)
		}
	}
	removed := make(map[int]bool)
	grouped := make(map[string]bool)
	for _, returned := range steps {
		if (returned.Kind != "return" && returned.Kind != "guard") || returned.InvocationID == "" || returned.Anchor.EnclosingSymbolPath == "" || returned.Anchor.RepoRelativePath == "" {
			continue
		}
		var members []SemanticStep
		if returned.Kind == "return" {
			members = returnExpressionMembers(returned, invocations[returned.InvocationID], connections)
		}
		conditional := false
		if len(members) < 2 {
			members = conditionalEvaluationMembers(returned, invocations[returned.InvocationID], connections)
			conditional = len(members) > 1
		}
		if len(members) < 2 {
			continue
		}
		primary, boundaryCount, valid := returned.StepID, 0, true
		status := "verified"
		for _, member := range members {
			index, ok := frames[member.StepID]
			if !ok || removed[index] || grouped[sequence.Frames[index].FrameID] {
				valid = false
				break
			}
			frame := sequence.Frames[index]
			if frame.Role == "effect" || (frame.Role == "decision" && !conditional) {
				valid = false
				break
			}
			if isStepBoundary(member, boundaries, unknowns) {
				if conditional {
					valid = false
					break
				}
				boundaryCount++
				primary = member.StepID
			}
			if frame.Status == "unknown" {
				status = "unknown"
			} else if frame.Status != "verified" && status == "verified" {
				status = "partial"
			}
		}
		if !valid || boundaryCount > 1 || (boundaryCount > 0 && sequence.Frames[frames[members[0].StepID]].Role == "entry") {
			continue
		}
		first := frames[members[0].StepID]
		if returned.Kind == "return" && (conditional || sequence.Frames[first].Role == "entry") {
			primary = members[0].StepID
		}
		frame := sequence.Frames[frames[primary]]
		frame.FrameID = sequence.Frames[first].FrameID
		frame.StepRefs = make([]string, 0, len(members))
		frame.Role = "process"
		if conditional {
			frame.Role = "decision"
		}
		if boundaryCount == 1 {
			frame.Role = "boundary"
		} else if sequence.Frames[first].Role == "entry" {
			frame.Role = "entry"
		}
		frame.Status = status
		frame.Title = returned.Anchor.EnclosingSymbolPath + " 반환"
		frame.Text = "하나의 반환식을 계산하고 반환하는 단계"
		if conditional {
			frame.Title = returned.Anchor.EnclosingSymbolPath + " 반환값 판단"
			frame.Text = "한 반환식의 조건 평가와 반환"
			if returned.Kind == "guard" {
				frame.Title = sequence.Frames[frames[returned.StepID]].Title
				frame.Text = "한 검증 조건식의 내부 평가와 판단"
			}
		}
		frame.CollapsedDetail = &CollapsedDetail{Count: len(members) - 1, Reason: frame.Text}
		frame.FrameMatchKey = NormalizeFrameMatchKey(frame.Role, returned.Anchor.EnclosingSymbolPath, frame.TechnicalAnchor, frame.Title)
		for _, member := range members {
			index := frames[member.StepID]
			grouped[sequence.Frames[index].FrameID] = true
			frame.StepRefs = append(frame.StepRefs, member.StepID)
			if index != first {
				removed[index] = true
			}
		}
		sequence.Frames[first] = frame
	}
	result := make([]FlowSequenceFrame, 0, len(sequence.Frames)-len(removed))
	for i, frame := range sequence.Frames {
		if !removed[i] {
			frame.Ordinal = len(result) + 1
			result = append(result, frame)
		}
	}
	sequence.Frames = result
	limitations := make([]FlowSummaryLimitation, 0, len(sequence.SummaryLimitations))
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

func returnExpressionMembers(returned SemanticStep, invocation []SemanticStep, connections flowGroupingConnections) []SemanticStep {
	span := returned.Anchor.ByteRange
	if span[0] < 0 || span[1] <= span[0] {
		return nil
	}
	var members []SemanticStep
	for _, step := range invocation {
		source := step.Anchor
		if step.InvocationID != returned.InvocationID || source.RepoRelativePath != returned.Anchor.RepoRelativePath || source.EnclosingSymbolPath != returned.Anchor.EnclosingSymbolPath || source.FileHash != returned.Anchor.FileHash {
			return nil
		}
		r := source.ByteRange
		if r[1] <= span[0] || r[0] >= span[1] {
			continue
		}
		if r[0] < span[0] || r[1] > span[1] || r[1] <= r[0] || (step.Kind != "call" && step.StepID != returned.StepID) || step.Branch != nil || step.StateDelta != nil || step.SideEffect != nil {
			return nil
		}
		for _, edge := range connections.outgoing[step.StepID] {
			if edge.Kind == "external" || edge.Kind == "async" {
				return nil
			}
		}
		members = append(members, step)
	}
	if len(members) < 2 || members[len(members)-1].StepID != returned.StepID {
		return nil
	}
	for i, member := range members {
		outgoing := 0
		for _, edge := range connections.outgoing[member.StepID] {
			if edge.Kind != "control_flow" {
				continue
			}
			if i == len(members)-1 || edge.ToStepID != members[i+1].StepID || edge.ResolutionStatus != "resolved" {
				return nil
			}
			outgoing++
		}
		if i < len(members)-1 && outgoing != 1 {
			return nil
		}
		if i > 0 {
			incoming := 0
			for _, edge := range connections.incoming[member.StepID] {
				if edge.Kind != "control_flow" {
					continue
				}
				if edge.FromStepID != members[i-1].StepID || edge.ResolutionStatus != "resolved" {
					return nil
				}
				incoming++
			}
			if incoming != 1 {
				return nil
			}
		}
	}
	return members
}
