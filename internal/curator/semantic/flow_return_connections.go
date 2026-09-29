package semantic

import "codeflow/internal/collector/slicing"

func verifiedDirectReturnMode(step slicing.SliceStep, snapshotID string) string {
	context := step.FlowContext
	if step.Kind != "return" || context == nil || !context.Statement.DirectReturnCompletion ||
		context.Callable.CompletionMode != "synchronous" || snapshotID == "" || context.SnapshotID != snapshotID ||
		context.CanonicalPath != step.Anchor.RepoRelativePath || context.SourceHash != step.Anchor.FileHash ||
		context.Statement.ByteRange != step.Anchor.ByteRange {
		return ""
	}
	return context.Callable.CompletionMode
}

// deriveDirectReturnConnections adds a caller resumption only when the adapter
// proved a synchronous callable, a direct call target, and a caller-local
// continuation. Deferred and unproven calls retain their original relations.
func deriveDirectReturnConnections(steps []SemanticStep, edges []SemanticEdge, completionMode map[string]string) []SemanticEdge {
	byID := make(map[string]SemanticStep, len(steps))
	byOrdinal := make(map[int]SemanticStep, len(steps))
	for _, step := range steps {
		byID[step.StepID] = step
		byOrdinal[step.Ordinal] = step
	}
	callEntries := make(map[string][]SemanticEdge)
	continuations := make(map[string][]SemanticEdge)
	flow := make(map[string][]string)
	finalized := make(map[string]bool)
	knownReturns := make(map[string]bool)
	for _, edge := range edges {
		if edge.ResolutionStatus != "resolved" {
			continue
		}
		switch edge.Kind {
		case "resolved_cross_file":
			callEntries[edge.FromStepID] = append(callEntries[edge.FromStepID], edge)
		case "control_flow":
			if len(edge.Conditions) == 0 {
				continuations[edge.FromStepID] = append(continuations[edge.FromStepID], edge)
			}
		}
		if isDirectExecutionConnection(edge.Kind) {
			flow[edge.FromStepID] = append(flow[edge.FromStepID], edge.ToStepID)
		}
		if edge.Kind == "finally" {
			finalized[edge.FromStepID] = true
		}
		if edge.Kind == "return" {
			knownReturns[edge.FromStepID+"\x00"+edge.ToStepID] = true
		}
	}
	result := append([]SemanticEdge{}, edges...)
	for _, returned := range steps {
		if returned.Kind != "return" || returned.CallerStepOrdinal == nil || returned.InvocationID == "" || completionMode[returned.StepID] != "synchronous" || finalized[returned.StepID] {
			continue
		}
		caller, exists := byOrdinal[*returned.CallerStepOrdinal]
		if !exists || caller.Kind != "call" || caller.InvocationID == "" || caller.InvocationID == returned.InvocationID {
			continue
		}
		entries, next := callEntries[caller.StepID], continuations[caller.StepID]
		if len(entries) != 1 || len(next) != 1 || len(next[0].Conditions) != 0 {
			continue
		}
		entry, entryExists := byID[entries[0].ToStepID]
		continuation, continuationExists := byID[next[0].ToStepID]
		if !entryExists || !continuationExists || entry.InvocationID != returned.InvocationID || entry.CallerStepOrdinal == nil || *entry.CallerStepOrdinal != caller.Ordinal || continuation.InvocationID != caller.InvocationID || continuation.Ordinal <= caller.Ordinal || next[0].ToSymbolPath == "" {
			continue
		}
		if !reachableDirectReturn(entry.StepID, returned.StepID, returned.InvocationID, byID, flow) {
			continue
		}
		if knownReturns[returned.StepID+"\x00"+continuation.StepID] {
			continue
		}
		result = append(result, SemanticEdge{FromStepID: returned.StepID, ToStepID: continuation.StepID, ToSymbolPath: next[0].ToSymbolPath, Kind: "return", ResolutionStatus: "resolved"})
		knownReturns[returned.StepID+"\x00"+continuation.StepID] = true
	}
	return result
}

func isDirectExecutionConnection(kind string) bool {
	switch kind {
	case "control_flow", "loop_back", "loop_reentry", "loop_exit", "loop_exit_back", "switch_exit":
		return true
	default:
		return false
	}
}

func reachableDirectReturn(entryID, returnID, invocationID string, steps map[string]SemanticStep, flow map[string][]string) bool {
	queue := []string{entryID}
	seen := map[string]bool{entryID: true}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if current == returnID {
			return true
		}
		for _, next := range flow[current] {
			step, exists := steps[next]
			if !exists || step.InvocationID != invocationID || seen[next] {
				continue
			}
			seen[next] = true
			queue = append(queue, next)
		}
	}
	return false
}
