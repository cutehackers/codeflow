package semantic

import "strings"

// groupAlternativeDecisionPaths keeps a verified if/else-if chain and its
// mutually exclusive bodies under one decision gateway. Every original edge
// and source step remains available in the timeline.
func groupAlternativeDecisionPaths(sequence *FlowSequence, mapIR *SemanticMapIR, connections flowGroupingConnections) {
	if sequence == nil || mapIR == nil || len(mapIR.Steps) < 5 {
		return
	}
	steps := mapIR.Steps
	positions := make(map[string]int, len(steps))
	stepIDs := make([]string, len(steps))
	for index, step := range steps {
		positions[step.StepID] = index
		stepIDs[index] = step.StepID
	}
	requestSpecific := requestSpecificStepRefs(mapIR)
	for start := 0; start < len(steps); start++ {
		chain, lastAlternative, ok := alternativeGuardChain(start, steps, positions, connections)
		if !ok {
			continue
		}
		for join := lastAlternative; join < len(steps); join++ {
			if steps[join].InvocationID != steps[start].InvocationID || !alternativeJoinHasMultipleSources(start, join, steps, positions, connections) {
				continue
			}
			if !verifiedAlternativeRegion(start, join, chain, steps, positions, connections, requestSpecific) {
				continue
			}
			frameByStep := make(map[string]int, len(steps))
			for index, frame := range sequence.Frames {
				for _, ref := range frame.StepRefs {
					frameByStep[ref] = index
				}
			}
			segment := handlerExecutionSegment{start: start, end: join, kind: "decision"}
			indexes := segmentFrameIndexes(segment, sequence.Frames, positions, stepIDs, frameByStep, nil)
			if len(indexes) < 2 || !consecutiveFrameIndexes(indexes) {
				continue
			}
			mergeAlternativeFrames(sequence, indexes, steps[start], len(chain))
			start = join - 1
			break
		}
	}
}

func alternativeGuardChain(start int, steps []SemanticStep, positions map[string]int, connections flowGroupingConnections) ([]int, int, bool) {
	if steps[start].Kind != "guard" && steps[start].Kind != "branch" && steps[start].Kind != "decision" {
		return nil, 0, false
	}
	invocation := steps[start].InvocationID
	if invocation == "" {
		return nil, 0, false
	}
	chain := []int{start}
	current := start
	for {
		_, falseTarget, ok := alternativeBranchTargets(steps[current], connections)
		if !ok {
			return nil, 0, false
		}
		falsePosition, exists := positions[falseTarget]
		if !exists || falsePosition <= current || steps[falsePosition].InvocationID != invocation {
			return nil, 0, false
		}
		if steps[falsePosition].Kind != "guard" && steps[falsePosition].Kind != "branch" && steps[falsePosition].Kind != "decision" {
			return chain, falsePosition, true
		}
		chain = append(chain, falsePosition)
		current = falsePosition
	}
}

func alternativeBranchTargets(step SemanticStep, connections flowGroupingConnections) (string, string, bool) {
	truthy, falsy := "", ""
	for _, edge := range connections.outgoing[step.StepID] {
		if edge.Kind != "control_flow" || edge.ResolutionStatus != "resolved" || len(edge.Conditions) != 1 || edge.Conditions[0].StepID != step.StepID {
			continue
		}
		switch edge.Conditions[0].Outcome {
		case "truthy":
			if truthy != "" {
				return "", "", false
			}
			truthy = edge.ToStepID
		case "falsy":
			if falsy != "" {
				return "", "", false
			}
			falsy = edge.ToStepID
		}
	}
	return truthy, falsy, truthy != "" && falsy != ""
}

func alternativeJoinHasMultipleSources(start, join int, steps []SemanticStep, positions map[string]int, connections flowGroupingConnections) bool {
	sources := make(map[string]bool)
	for _, edge := range connections.incoming[steps[join].StepID] {
		position, exists := positions[edge.FromStepID]
		if exists && position >= start && position < join && isAlternativeExecutionEdge(edge) {
			sources[edge.FromStepID] = true
		}
	}
	return len(sources) >= 2
}

func isAlternativeExecutionEdge(edge SemanticEdge) bool {
	// A wait, callback relation, or cross-invocation transition cannot prove
	// that both alternatives reach the same continuation in this invocation.
	return edge.ResolutionStatus == "resolved" && (edge.Kind == "control_flow" || edge.Kind == "sequence")
}

func verifiedAlternativeRegion(start, join int, chain []int, steps []SemanticStep, positions map[string]int, connections flowGroupingConnections, requestSpecific map[string]bool) bool {
	segment := handlerExecutionSegment{start: start, end: join, kind: "decision"}
	if !segmentHasOnlyCoreFlowExecutions(segment, steps, connections) {
		return false
	}
	chainPositions := make(map[int]bool, len(chain))
	for _, position := range chain {
		chainPositions[position] = true
	}
	for index := start; index < join; index++ {
		step := steps[index]
		// This gateway describes the caller's alternatives. A reached callee
		// has its own decisions and results, even when it returns to the same
		// caller continuation. The shared join does not prove one purpose.
		if step.InvocationID != steps[start].InvocationID {
			return false
		}
		if index != start && requestSpecific[step.StepID] {
			return false
		}
		if step.InvocationID == steps[start].InvocationID && (step.Kind == "guard" || step.Kind == "branch" || step.Kind == "decision") && !chainPositions[index] {
			return false
		}
		for _, edge := range connections.incoming[step.StepID] {
			if !isAlternativeExecutionEdge(edge) {
				continue
			}
			from, exists := positions[edge.FromStepID]
			if exists && ((from < start && index != start) || from >= join) {
				return false
			}
		}
		for _, edge := range connections.outgoing[step.StepID] {
			if !isAlternativeExecutionEdge(edge) {
				continue
			}
			to, exists := positions[edge.ToStepID]
			if !exists || to < start || to > join {
				return false
			}
		}
	}
	for _, guardPosition := range chain {
		truthy, falsy, ok := alternativeBranchTargets(steps[guardPosition], connections)
		if !ok || !alternativePathReachesJoin(truthy, start, join, steps, positions, connections) || !alternativePathReachesJoin(falsy, start, join, steps, positions, connections) {
			return false
		}
	}
	reached := map[string]bool{steps[start].StepID: true}
	for changed := true; changed; {
		changed = false
		for index := start; index < join; index++ {
			if !reached[steps[index].StepID] {
				continue
			}
			for _, edge := range connections.outgoing[steps[index].StepID] {
				to, exists := positions[edge.ToStepID]
				if exists && to >= start && to < join && isAlternativeExecutionEdge(edge) && !reached[edge.ToStepID] {
					reached[edge.ToStepID] = true
					changed = true
				}
			}
		}
	}
	return len(reached) == join-start
}

func alternativePathReachesJoin(startID string, start, join int, steps []SemanticStep, positions map[string]int, connections flowGroupingConnections) bool {
	seen := map[string]bool{startID: true}
	queue := []string{startID}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if current == steps[join].StepID {
			return true
		}
		for _, edge := range connections.outgoing[current] {
			to, exists := positions[edge.ToStepID]
			if !exists || to < start || to > join || !isAlternativeExecutionEdge(edge) || seen[edge.ToStepID] {
				continue
			}
			seen[edge.ToStepID] = true
			queue = append(queue, edge.ToStepID)
		}
	}
	return false
}

func consecutiveFrameIndexes(indexes []int) bool {
	for index := 1; index < len(indexes); index++ {
		if indexes[index] != indexes[index-1]+1 {
			return false
		}
	}
	return true
}

func mergeAlternativeFrames(sequence *FlowSequence, indexes []int, primary SemanticStep, conditionCount int) {
	first := sequence.Frames[indexes[0]]
	merged := first
	merged.Role = "decision"
	merged.Title = "조건별 처리 경로"
	if conditionCount == 1 && primary.Branch != nil && strings.TrimSpace(*primary.Branch) != "" {
		merged.Title = strings.TrimSpace(*primary.Branch) + "에 따른 처리"
	}
	merged.Text = "조건에 따라 다른 처리를 거쳐 공통 후속 처리로 진행"
	merged.Condition = nil
	merged.Outcomes = nil
	merged.StepRefs = nil
	merged.Status = "verified"
	removed := make(map[int]bool)
	groupedFrames := make(map[string]bool)
	for _, index := range indexes {
		frame := sequence.Frames[index]
		merged.StepRefs = append(merged.StepRefs, frame.StepRefs...)
		if frame.Status == "unknown" {
			merged.Status = "unknown"
		} else if frame.Status != "verified" && merged.Status == "verified" {
			merged.Status = "partial"
		}
		if index != indexes[0] {
			removed[index] = true
			groupedFrames[frame.FrameID] = true
		}
	}
	merged.PrimaryStepRef = primary.StepID
	merged.TechnicalAnchor = primary.TechnicalName
	merged.CollapsedDetail = &CollapsedDetail{Count: len(merged.StepRefs) - 1, Reason: merged.Text}
	merged.FrameMatchKey = NormalizeFrameMatchKey(merged.Role, primary.Anchor.EnclosingSymbolPath, primary.TechnicalName, merged.Title)
	sequence.Frames[indexes[0]] = merged
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
