package slicing

import "fmt"

// ValidateExecutionReferences checks explicitly supplied invocation facts. Older
// payloads may omit them, but a malformed explicit reference never falls back
// to a same-name target.
func ValidateExecutionReferences(steps []SliceStep, edges []SliceEdge) error {
	ordinals := make(map[int]SliceStep, len(steps))
	entries := make(map[string]SliceStep)
	for _, step := range steps {
		if _, exists := ordinals[step.Ordinal]; exists || step.Ordinal < 1 {
			return fmt.Errorf("invalid step ordinal %d", step.Ordinal)
		}
		ordinals[step.Ordinal] = step
		if step.InvocationID == "" {
			if step.CallerStepOrdinal != nil {
				return fmt.Errorf("step %d has caller without invocation", step.Ordinal)
			}
			continue
		}
		entry, exists := entries[step.InvocationID]
		if exists && (executionSymbol(entry) != executionSymbol(step) || entry.Anchor.RepoRelativePath != step.Anchor.RepoRelativePath || !sameOrdinal(entry.CallerStepOrdinal, step.CallerStepOrdinal)) {
			return fmt.Errorf("inconsistent invocation %q", step.InvocationID)
		}
		if !exists || step.Ordinal < entry.Ordinal {
			entries[step.InvocationID] = step
		}
	}
	for _, step := range steps {
		if step.AssignmentSourceOrdinal != nil {
			source, exists := ordinals[*step.AssignmentSourceOrdinal]
			if !exists {
				return fmt.Errorf("assignment source missing for step %d", step.Ordinal)
			}
			if err := ValidateAssignmentSource(source, step); err != nil {
				return err
			}
		}
	}
	for _, entry := range entries {
		if entry.CallerStepOrdinal == nil {
			continue
		}
		caller, exists := ordinals[*entry.CallerStepOrdinal]
		if !exists || caller.InvocationID == "" || caller.InvocationID == entry.InvocationID || caller.Ordinal >= entry.Ordinal {
			return fmt.Errorf("invalid caller for invocation %q", entry.InvocationID)
		}
	}
	for _, edge := range edges {
		if len(edge.Conditions) > 0 {
			if (edge.Kind != "control_flow" && edge.Kind != "loop_back" && edge.Kind != "loop_reentry") || edge.StepOrdinal == nil || len(edge.Conditions) != 1 {
				return fmt.Errorf("invalid branch condition relation")
			}
			condition := edge.Conditions[0]
			decision, exists := ordinals[condition.StepOrdinal]
			if !exists || condition.StepOrdinal != *edge.StepOrdinal || (decision.Kind != "branch" && decision.Kind != "guard") || (condition.Outcome != "truthy" && condition.Outcome != "falsy" && condition.Outcome != "nullish" && condition.Outcome != "non_nullish") {
				return fmt.Errorf("invalid branch condition reference")
			}
		}
		if edge.Kind == "return" {
			if edge.StepOrdinal == nil || edge.TargetStepOrdinal == nil || edge.ResolutionStatus != "resolved" {
				return fmt.Errorf("return requires resolved source and continuation")
			}
			source, sourceOK := ordinals[*edge.StepOrdinal]
			target, targetOK := ordinals[*edge.TargetStepOrdinal]
			if !sourceOK || !targetOK || source.Kind != "return" || source.InvocationID == "" || source.CallerStepOrdinal == nil {
				return fmt.Errorf("return source has no caller")
			}
			caller, callerOK := ordinals[*source.CallerStepOrdinal]
			if !callerOK || caller.Kind != "call" || target.InvocationID != caller.InvocationID || target.InvocationID == source.InvocationID || target.Ordinal <= caller.Ordinal || edge.ToSymbolPath != target.Anchor.RepoRelativePath+"#"+target.SymbolPath {
				return fmt.Errorf("return continuation does not belong to caller")
			}
			called, continued := false, false
			for _, relation := range edges {
				if relation.StepOrdinal == nil || *relation.StepOrdinal != caller.Ordinal || relation.TargetStepOrdinal == nil || relation.ResolutionStatus != "resolved" {
					continue
				}
				if relation.Kind == "resolved_cross_file" && *relation.TargetStepOrdinal == entries[source.InvocationID].Ordinal {
					called = true
				}
				if relation.Kind == "control_flow" && *relation.TargetStepOrdinal == target.Ordinal && len(relation.Conditions) == 0 {
					continued = true
				}
			}
			if !called || !continued {
				return fmt.Errorf("return lacks matching call and continuation")
			}
			continue
		}
		if edge.Kind == "control_flow" || edge.Kind == "parallel_wait" || edge.Kind == "await_resume" || edge.Kind == "await_loop_back" || edge.Kind == "failure" || edge.Kind == "finally" || edge.Kind == "switch_exit" || edge.Kind == "loop_back" || edge.Kind == "loop_reentry" || edge.Kind == "loop_exit" || edge.Kind == "loop_exit_back" {
			if edge.StepOrdinal == nil || edge.TargetStepOrdinal == nil {
				return fmt.Errorf("control flow requires both step references")
			}
			source, sourceExists := ordinals[*edge.StepOrdinal]
			target, targetExists := ordinals[*edge.TargetStepOrdinal]
			if edge.Kind == "failure" && sourceExists && targetExists && source.InvocationID != target.InvocationID {
				if err := validateCalledThrowFailure(edge, source, target, ordinals, entries, edges); err != nil {
					return err
				}
				continue
			}
			if !sourceExists || !targetExists || source.InvocationID == "" || source.InvocationID != target.InvocationID || executionSymbol(source) != executionSymbol(target) || source.Anchor.RepoRelativePath != target.Anchor.RepoRelativePath || edge.ToSymbolPath != target.Anchor.RepoRelativePath+"#"+executionSymbol(target) || edge.ResolutionStatus != "resolved" {
				return fmt.Errorf("invalid control flow endpoints")
			}
			if (edge.Kind == "loop_exit" || edge.Kind == "loop_exit_back" || edge.Kind == "switch_exit") != (source.Kind == "break") || (source.Kind == "continue" && edge.Kind != "loop_back") {
				return fmt.Errorf("invalid loop control transfer")
			}
			if edge.Kind == "loop_exit" && target.Ordinal <= source.Ordinal {
				return fmt.Errorf("loop exit must advance beyond its break")
			}
			if edge.Kind == "switch_exit" && (source.Kind != "break" || target.Ordinal <= source.Ordinal) {
				return fmt.Errorf("switch exit must advance beyond its break")
			}
			if edge.Kind == "loop_exit_back" && target.Ordinal == source.Ordinal {
				return fmt.Errorf("inner loop exit cannot resume at its own break")
			}
			if (edge.Kind == "loop_back" || edge.Kind == "await_loop_back" || edge.Kind == "loop_exit_back") && target.Ordinal > source.Ordinal {
				return fmt.Errorf("loop back must return to an earlier condition evaluation")
			}
			if edge.Kind == "loop_reentry" && (source.Kind != "branch" || target.Ordinal >= source.Ordinal || len(edge.Conditions) != 1 || edge.Conditions[0].Outcome != "truthy") {
				return fmt.Errorf("loop reentry requires a true branch condition and an earlier body target")
			}
			if edge.Kind == "await_resume" && source.Kind != "await" {
				return fmt.Errorf("await resumption requires an await source")
			}
			if edge.Kind == "await_loop_back" && source.Kind != "await" {
				return fmt.Errorf("await loop back requires an await source")
			}
			if edge.Kind == "parallel_wait" && (source.Kind != "call" || target.Kind != "await" || target.Ordinal <= source.Ordinal) {
				return fmt.Errorf("parallel wait requires a call source and later await target")
			}
			if source.Kind == "await" && edge.Kind != "await_resume" && edge.Kind != "await_loop_back" && edge.Kind != "failure" {
				return fmt.Errorf("await source requires an explicit completion relation")
			}
			if edge.Kind == "failure" && source.Kind != "await" && source.Kind != "throw" {
				return fmt.Errorf("failure relation requires an await or throw source")
			}
			if edge.Kind == "finally" && source.Kind != "return" && source.Kind != "throw" {
				return fmt.Errorf("finally relation requires a completion source")
			}
			if (source.Kind == "return" || source.Kind == "throw") && edge.Kind != "finally" && edge.Kind != "failure" {
				return fmt.Errorf("normal flow cannot leave completion step %d", source.Ordinal)
			}
			continue
		}
		if edge.TargetStepOrdinal == nil {
			continue
		}
		target, exists := ordinals[*edge.TargetStepOrdinal]
		if !exists || target.InvocationID == "" || entries[target.InvocationID].Ordinal != target.Ordinal || target.Anchor.RepoRelativePath+"#"+target.SymbolPath != edge.ToSymbolPath {
			return fmt.Errorf("invalid explicit target for %q", edge.ToSymbolPath)
		}
		if edge.StepOrdinal == nil || target.CallerStepOrdinal == nil || *edge.StepOrdinal != *target.CallerStepOrdinal || edge.ResolutionStatus != "resolved" {
			return fmt.Errorf("explicit target caller mismatch for %q", edge.ToSymbolPath)
		}
	}
	return nil
}

func executionSymbol(step SliceStep) string {
	if step.Anchor.EnclosingSymbolPath != "" {
		return step.Anchor.EnclosingSymbolPath
	}
	return step.SymbolPath
}

func validateCalledThrowFailure(edge SliceEdge, source, target SliceStep, ordinals map[int]SliceStep, entries map[string]SliceStep, edges []SliceEdge) error {
	if source.Kind != "throw" || source.InvocationID == "" || source.CallerStepOrdinal == nil || target.InvocationID == "" || edge.ResolutionStatus != "resolved" || edge.ToSymbolPath != target.Anchor.RepoRelativePath+"#"+target.SymbolPath {
		return fmt.Errorf("invalid called throw recovery relation")
	}
	caller, callerOK := ordinals[*source.CallerStepOrdinal]
	entry, entryOK := entries[source.InvocationID]
	if !callerOK || !entryOK || caller.Kind != "call" || caller.InvocationID == source.InvocationID || target.InvocationID != caller.InvocationID || target.Ordinal <= caller.Ordinal {
		return fmt.Errorf("called throw recovery does not belong to caller")
	}
	for _, relation := range edges {
		if relation.Kind == "resolved_cross_file" && relation.ResolutionStatus == "resolved" && relation.StepOrdinal != nil && relation.TargetStepOrdinal != nil && *relation.StepOrdinal == caller.Ordinal && *relation.TargetStepOrdinal == entry.Ordinal {
			return nil
		}
	}
	return fmt.Errorf("called throw recovery lacks matching call")
}

func sameOrdinal(left, right *int) bool {
	return left == nil && right == nil || left != nil && right != nil && *left == *right
}
