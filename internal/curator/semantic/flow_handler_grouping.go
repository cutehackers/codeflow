package semantic

import (
	"sort"
	"strings"
)

// groupCoreFlowExecutionSegments makes the 1:N hierarchy visible when one
// core handler contains a preparation segment, an awaited operation, and
// a later decision. It groups only steps whose enclosing invocation is known;
// a nested invocation must be reached through a resolved cross-file edge from
// that handler segment. Boundaries remain children and make the parent unknown
// rather than disappearing or becoming verified.
func groupCoreFlowExecutionSegments(sequence *FlowSequence, steps []SemanticStep, connections flowGroupingConnections) {
	if sequence == nil || len(sequence.Frames) < 2 || len(steps) < 2 || steps[0].InvocationID == "" {
		return
	}

	segments := coreFlowExecutionSegments(steps, connections)
	if len(segments) == 0 {
		return
	}

	stepPositions := make(map[string]int, len(steps))
	stepIDsByPosition := make([]string, len(steps))
	for position, step := range steps {
		stepPositions[step.StepID] = position
		stepIDsByPosition[position] = step.StepID
	}
	frameByStep := make(map[string]int, len(steps))
	for index, frame := range sequence.Frames {
		for _, stepRef := range frame.StepRefs {
			frameByStep[stepRef] = index
		}
	}

	removed := make(map[int]bool)
	remappedFrames := make(map[string]string)
	groupedFrames := make(map[string]bool)
	for _, segment := range segments {
		frameIndexes := segmentFrameIndexes(segment, sequence.Frames, stepPositions, stepIDsByPosition, frameByStep, removed)
		if len(frameIndexes) < 2 || !segmentHasOnlyCoreFlowExecutions(segment, steps, connections) {
			continue
		}

		firstIndex := frameIndexes[0]
		primaryStep := executionSegmentPrimary(segment, steps)
		primaryIndex, exists := frameByStep[primaryStep.StepID]
		if !exists || removed[primaryIndex] {
			continue
		}
		firstFrame := sequence.Frames[firstIndex]
		merged := sequence.Frames[primaryIndex]
		merged.FrameID = firstFrame.FrameID
		merged.Role = firstFrame.Role
		merged.StepRefs = nil
		merged.Status = "verified"
		merged.Text = "같은 호출 문맥에서 확인한 연속 처리"
		merged.CollapsedDetail = nil
		merged.Condition = nil
		merged.Outcomes = append([]string(nil), firstFrame.Outcomes...)

		for _, frameIndex := range frameIndexes {
			frame := sequence.Frames[frameIndex]
			merged.StepRefs = append(merged.StepRefs, frame.StepRefs...)
			if frame.Status == "unknown" {
				merged.Status = "unknown"
			} else if frame.Status != "verified" && merged.Status == "verified" {
				merged.Status = "partial"
			}
			groupedFrames[frame.FrameID] = true
			remappedFrames[frame.FrameID] = firstFrame.FrameID
			if frameIndex != firstIndex {
				removed[frameIndex] = true
			}
		}

		merged.PrimaryStepRef = primaryStep.StepID
		merged.TechnicalAnchor = primaryStep.TechnicalName
		if primaryStep.Anchor.RepoRelativePath != "" {
			anchor := primaryStep.Anchor
			if primaryStep.Anchor.SymbolRange != nil {
				rangeCopy := *primaryStep.Anchor.SymbolRange
				anchor.SymbolRange = &rangeCopy
			}
			merged.SourceAnchor = &anchor
		}
		if primaryStep.Branch != nil {
			condition := *primaryStep.Branch
			merged.Condition = &condition
		}
		merged.Title = coreFlowSegmentTitle(segment, primaryStep)
		merged.CollapsedDetail = &CollapsedDetail{Count: len(merged.StepRefs) - 1, Reason: merged.Text}
		merged.FrameMatchKey = NormalizeFrameMatchKey(merged.Role, primaryStep.Anchor.EnclosingSymbolPath, primaryStep.TechnicalName, merged.Title)
		sequence.Frames[firstIndex] = merged
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
	remapCoreFlowGroupingLimitations(sequence, remappedFrames, groupedFrames)
}

type handlerExecutionSegment struct {
	start int
	end   int
	kind  string
}

func coreFlowExecutionSegments(steps []SemanticStep, connections flowGroupingConnections) []handlerExecutionSegment {
	rootInvocation := steps[0].InvocationID
	start := 0
	startKind := ""
	segments := make([]handlerExecutionSegment, 0)
	appendSegment := func(end int, kind string) {
		if end-start >= 2 {
			segments = append(segments, handlerExecutionSegment{start: start, end: end, kind: kind})
		}
	}
	for index, step := range steps {
		if step.InvocationID != rootInvocation {
			continue
		}
		if index > start && isAwaitedCoreFlowCall(index, steps) {
			appendSegment(index, "preparation")
			start = index
			startKind = "awaited_operation"
		}
		if step.Kind != "branch" {
			continue
		}
		if index > start {
			if startKind == "awaited_operation" {
				appendSegment(index, "operation")
			}
		}
		end := branchExecutionSegmentEnd(index, steps, connections)
		if end-index >= 2 {
			segments = append(segments, handlerExecutionSegment{start: index, end: end, kind: "decision"})
		}
		start = end
		startKind = ""
	}
	if startKind == "awaited_operation" {
		appendSegment(len(steps), "operation")
	}
	return segments
}

func isAwaitedCoreFlowCall(index int, steps []SemanticStep) bool {
	called := steps[index]
	if called.Kind != "call" || strings.TrimSpace(called.Name) == "" {
		return false
	}
	for _, candidate := range steps[index+1:] {
		if candidate.InvocationID != called.InvocationID {
			continue
		}
		if candidate.Kind == "branch" || candidate.Kind == "guard" || candidate.Kind == "decision" {
			return false
		}
		if candidate.Kind == "await" && strings.Contains(strings.TrimSpace(candidate.Name), strings.TrimSpace(called.Name)) {
			return true
		}
	}
	return false
}

func branchExecutionSegmentEnd(index int, steps []SemanticStep, connections flowGroupingConnections) int {
	branch := steps[index]
	end := index + 1
	for end < len(steps) {
		candidate := steps[end]
		if candidate.InvocationID != branch.InvocationID || candidate.Kind != "call" || candidate.Branch == nil || !hasConditionedControlFlow(connections, branch.StepID, candidate.StepID) {
			break
		}
		end++
	}
	return end
}

func hasConditionedControlFlow(connections flowGroupingConnections, from, to string) bool {
	for _, edge := range connections.outgoing[from] {
		if edge.ToStepID == to && edge.Kind == "control_flow" && edge.ResolutionStatus == "resolved" && len(edge.Conditions) == 1 {
			return true
		}
	}
	return false
}

func segmentFrameIndexes(segment handlerExecutionSegment, frames []FlowSequenceFrame, positions map[string]int, stepIDsByPosition []string, frameByStep map[string]int, removed map[int]bool) []int {
	indexes := make(map[int]bool)
	for position := segment.start; position < segment.end; position++ {
		stepID := stepIDsByPosition[position]
		frameIndex, exists := frameByStep[stepID]
		if !exists || removed[frameIndex] {
			return nil
		}
		indexes[frameIndex] = true
	}
	ordered := make([]int, 0, len(indexes))
	for index := range indexes {
		for _, stepID := range frames[index].StepRefs {
			position := positions[stepID]
			if position < segment.start || position >= segment.end {
				return nil
			}
		}
		ordered = append(ordered, index)
	}
	sort.Ints(ordered)
	return ordered
}

func segmentHasOnlyCoreFlowExecutions(segment handlerExecutionSegment, steps []SemanticStep, connections flowGroupingConnections) bool {
	rootInvocation := steps[segment.start].InvocationID
	allowedInvocations := map[string]bool{rootInvocation: true}
	for changed := true; changed; {
		changed = false
		for index := segment.start; index < segment.end; index++ {
			step := steps[index]
			if !allowedInvocations[step.InvocationID] {
				continue
			}
			for _, edge := range connections.outgoing[step.StepID] {
				if edge.Kind != "resolved_cross_file" || edge.ResolutionStatus != "resolved" {
					continue
				}
				for targetIndex := segment.start; targetIndex < segment.end; targetIndex++ {
					target := steps[targetIndex]
					if target.StepID == edge.ToStepID && target.InvocationID != "" && !allowedInvocations[target.InvocationID] {
						allowedInvocations[target.InvocationID] = true
						changed = true
					}
				}
			}
		}
	}
	for index := segment.start; index < segment.end; index++ {
		step := steps[index]
		if !allowedInvocations[step.InvocationID] || step.Kind == "failure" || step.Kind == "external_effect" || step.Kind == "external" || step.SideEffect != nil {
			return false
		}
		if segment.kind != "decision" && (step.Kind == "branch" || step.Kind == "guard" || step.Kind == "decision" || step.Branch != nil) {
			return false
		}
	}
	return true
}

func executionSegmentPrimary(segment handlerExecutionSegment, steps []SemanticStep) SemanticStep {
	if segment.kind == "decision" || segment.kind == "operation" && steps[segment.start].Kind == "call" {
		return steps[segment.start]
	}
	rootInvocation := steps[segment.start].InvocationID
	for index := segment.end - 1; index >= segment.start; index-- {
		step := steps[index]
		if step.InvocationID == rootInvocation && step.Kind == "mutation" && strings.Contains(step.Name, "=") {
			return step
		}
	}
	return steps[segment.start]
}

func coreFlowSegmentTitle(segment handlerExecutionSegment, primary SemanticStep) string {
	if segment.kind == "preparation" {
		if assignment := strings.TrimSpace(strings.SplitN(primary.Name, "=", 2)[0]); assignment != "" && strings.Contains(primary.Name, "=") {
			assignment = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(strings.TrimPrefix(assignment, "const "), "let "), "var "))
			return assignment + " 준비"
		}
	}
	if title := strings.TrimSpace(primary.Name); title != "" {
		return title
	}
	return "확인된 처리"
}

func remapCoreFlowGroupingLimitations(sequence *FlowSequence, remapped map[string]string, grouped map[string]bool) {
	limitations := make([]FlowSummaryLimitation, 0, len(sequence.SummaryLimitations))
	for _, limitation := range sequence.SummaryLimitations {
		refs := make([]string, 0, len(limitation.FrameRefs))
		seen := make(map[string]bool)
		for _, ref := range limitation.FrameRefs {
			if limitation.Code == "grouping_evidence_missing" && grouped[ref] {
				continue
			}
			if replacement, exists := remapped[ref]; exists {
				ref = replacement
			}
			if !seen[ref] {
				seen[ref] = true
				refs = append(refs, ref)
			}
		}
		if len(refs) > 0 {
			limitation.FrameRefs = refs
			limitations = append(limitations, limitation)
		}
	}
	if len(limitations) == 0 {
		sequence.SummaryLimitations = nil
		return
	}
	sequence.SummaryLimitations = limitations
}
