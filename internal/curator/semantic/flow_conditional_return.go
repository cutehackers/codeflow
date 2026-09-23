package semantic

// conditionalEvaluationMembers proves a closed decision region inside one return
// expression or guard header. Calls and effects remain independent even when source-contained.
func conditionalEvaluationMembers(returned SemanticStep, invocation []SemanticStep, connections flowGroupingConnections) []SemanticStep {
	span := returned.Anchor.ByteRange
	if span[0] < 0 || span[1] <= span[0] {
		return nil
	}
	var members []SemanticStep
	positions := make(map[string]int)
	for _, step := range invocation {
		source := step.Anchor
		if step.InvocationID != returned.InvocationID || source.RepoRelativePath != returned.Anchor.RepoRelativePath || source.EnclosingSymbolPath != returned.Anchor.EnclosingSymbolPath || source.FileHash != returned.Anchor.FileHash {
			return nil
		}
		r := source.ByteRange
		if r[1] <= span[0] || r[0] >= span[1] {
			continue
		}
		if r[0] < span[0] || r[1] > span[1] || r[1] <= r[0] || (step.Kind != "branch" && step.StepID != returned.StepID) || step.StateDelta != nil || step.SideEffect != nil {
			return nil
		}
		positions[step.StepID] = len(members)
		members = append(members, step)
	}
	if len(members) < 2 || members[len(members)-1].StepID != returned.StepID {
		return nil
	}
	for i, member := range members {
		var outgoing []SemanticEdge
		for _, edge := range connections.outgoing[member.StepID] {
			if edge.Kind != "return" {
				outgoing = append(outgoing, edge)
			}
		}
		if member.StepID == returned.StepID {
			if returned.Kind == "guard" {
				outcomes := make(map[string]bool)
				for _, edge := range outgoing {
					_, internal := positions[edge.ToStepID]
					if internal || edge.Kind != "control_flow" || edge.ResolutionStatus != "resolved" || len(edge.Conditions) != 1 || edge.Conditions[0].StepID != member.StepID {
						return nil
					}
					outcomes[edge.Conditions[0].Outcome] = true
				}
				if len(outgoing) != 2 || !outcomes["truthy"] || !outcomes["falsy"] {
					return nil
				}
			} else if len(outgoing) != 0 {
				return nil
			}
		} else {
			if len(outgoing) != 2 {
				return nil
			}
			outcomes := make(map[string]bool)
			for _, edge := range outgoing {
				target, exists := positions[edge.ToStepID]
				if !exists || target <= i || edge.Kind != "control_flow" || edge.ResolutionStatus != "resolved" || len(edge.Conditions) != 1 || edge.Conditions[0].StepID != member.StepID {
					return nil
				}
				outcomes[edge.Conditions[0].Outcome] = true
			}
			if len(outcomes) != 2 || !(outcomes["truthy"] && outcomes["falsy"] || outcomes["nullish"] && outcomes["non_nullish"]) {
				return nil
			}
		}
		if i == 0 {
			continue
		}
		incoming := connections.incoming[member.StepID]
		if len(incoming) == 0 {
			return nil
		}
		for _, edge := range incoming {
			source, exists := positions[edge.FromStepID]
			if !exists || source >= i || edge.Kind != "control_flow" || edge.ResolutionStatus != "resolved" {
				return nil
			}
		}
	}
	return members
}
