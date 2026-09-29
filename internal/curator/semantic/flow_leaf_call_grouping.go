package semantic

// groupVerifiedLeafCalls places a directly reached leaf implementation under
// its call gateway. The callee's conditions, boundaries, and source steps stay
// in the timeline. An independently required child or a second invocation
// keeps the implementation separate.
func groupVerifiedLeafCalls(sequence *FlowSequence, mapIR *SemanticMapIR, connections flowGroupingConnections) {
	if sequence == nil || mapIR == nil || len(mapIR.Steps) < 3 {
		return
	}
	steps := mapIR.Steps
	requestSpecific := requestSpecificStepRefs(mapIR)
	frameByStep := make(map[string]int, len(steps))
	for index, frame := range sequence.Frames {
		if len(frame.StepRefs) == 1 {
			frameByStep[frame.StepRefs[0]] = index
		}
	}
	removed := make(map[int]bool)
	groupedFrames := make(map[string]bool)
	for index := 0; index+2 < len(steps); index++ {
		caller, firstChild := steps[index], steps[index+1]
		if caller.Kind != "call" || caller.InvocationID == "" || firstChild.InvocationID == "" || firstChild.InvocationID == caller.InvocationID || firstChild.CallerStepOrdinal == nil || *firstChild.CallerStepOrdinal != caller.Ordinal {
			continue
		}
		callerFrameIndex, hasCallerFrame := frameByStep[caller.StepID]
		if !hasCallerFrame || removed[callerFrameIndex] || sequence.Frames[callerFrameIndex].Role == "boundary" || sequence.Frames[callerFrameIndex].Role == "effect" {
			continue
		}
		childEnd := index + 1
		for childEnd < len(steps) && steps[childEnd].InvocationID == firstChild.InvocationID {
			childEnd++
		}
		if childEnd-index < 3 || !verifiedLeafCallSteps(caller, steps[index+1:childEnd], steps, connections, requestSpecific) {
			continue
		}
		childFrames := make([]int, 0, childEnd-index-1)
		validFrames := true
		childIndex := index + 1
		for frameIndex := callerFrameIndex + 1; childIndex < childEnd; frameIndex++ {
			if frameIndex >= len(sequence.Frames) || removed[frameIndex] || len(sequence.Frames[frameIndex].StepRefs) == 0 {
				validFrames = false
				break
			}
			for _, ref := range sequence.Frames[frameIndex].StepRefs {
				if childIndex >= childEnd || ref != steps[childIndex].StepID {
					validFrames = false
					break
				}
				childIndex++
			}
			if !validFrames {
				break
			}
			childFrames = append(childFrames, frameIndex)
		}
		if !validFrames {
			continue
		}
		merged := sequence.Frames[callerFrameIndex]
		merged.Text = "확인된 직접 호출의 내부 처리"
		for _, frameIndex := range childFrames {
			frame := sequence.Frames[frameIndex]
			merged.StepRefs = append(merged.StepRefs, frame.StepRefs...)
			if frame.Status == "unknown" {
				merged.Status = "unknown"
			} else if frame.Status != "verified" && merged.Status == "verified" {
				merged.Status = "partial"
			}
			removed[frameIndex] = true
			groupedFrames[frame.FrameID] = true
		}
		merged.CollapsedDetail = &CollapsedDetail{Count: len(merged.StepRefs) - 1, Reason: merged.Text}
		sequence.Frames[callerFrameIndex] = merged
	}
	if len(removed) == 0 {
		return
	}
	frames := make([]FlowSequenceFrame, 0, len(sequence.Frames)-len(removed))
	for index, frame := range sequence.Frames {
		if removed[index] {
			continue
		}
		frame.Ordinal = len(frames) + 1
		frames = append(frames, frame)
	}
	sequence.Frames = frames
	remapCoreFlowGroupingLimitations(sequence, nil, groupedFrames)
}

func verifiedLeafCallSteps(caller SemanticStep, children, allSteps []SemanticStep, connections flowGroupingConnections, requestSpecific map[string]bool) bool {
	first := children[0]
	called := false
	for _, edge := range connections.outgoing[caller.StepID] {
		if edge.Kind == "resolved_cross_file" && edge.ResolutionStatus == "resolved" && edge.ToStepID == first.StepID {
			called = true
			break
		}
	}
	if !called {
		return false
	}
	allowed := make(map[string]bool, len(children))
	decisions := 0
	for _, child := range children {
		if child.InvocationID != first.InvocationID || child.CallerStepOrdinal == nil || *child.CallerStepOrdinal != caller.Ordinal || requestSpecific[child.StepID] || child.Kind == "failure" || child.Kind == "throw" || child.Kind == "external_effect" || child.Kind == "external" || child.SideEffect != nil {
			return false
		}
		if child.Kind == "guard" || child.Kind == "branch" || child.Kind == "decision" {
			decisions++
			if decisions > 1 {
				return false
			}
		}
		allowed[child.StepID] = true
	}
	for _, step := range allSteps {
		if step.InvocationID == first.InvocationID && !allowed[step.StepID] {
			return false
		}
	}
	for _, child := range children {
		for _, edge := range connections.outgoing[child.StepID] {
			if edge.ResolutionStatus == "resolved" && !allowed[edge.ToStepID] {
				return false
			}
		}
	}
	reached := map[string]bool{first.StepID: true}
	for changed := true; changed; {
		changed = false
		for _, child := range children {
			if !reached[child.StepID] {
				continue
			}
			for _, edge := range connections.outgoing[child.StepID] {
				if edge.ResolutionStatus == "resolved" && allowed[edge.ToStepID] && !reached[edge.ToStepID] {
					reached[edge.ToStepID] = true
					changed = true
				}
			}
		}
	}
	return len(reached) == len(children)
}
