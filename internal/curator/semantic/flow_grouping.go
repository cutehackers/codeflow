package semantic

// FlowSummaryLimitation describes a limitation of gateway grouping, independently
// of source verification and unresolved execution targets.
type FlowSummaryLimitation struct {
	Code      string   `json:"code"`
	Message   string   `json:"message"`
	FrameRefs []string `json:"frameRefs"`
}

type flowGroupingConnections struct {
	incoming map[string][]SemanticEdge
	outgoing map[string][]SemanticEdge
}

func groupingConnections(edges []SemanticEdge) flowGroupingConnections {
	result := flowGroupingConnections{incoming: make(map[string][]SemanticEdge), outgoing: make(map[string][]SemanticEdge)}
	for _, edge := range edges {
		result.outgoing[edge.FromStepID] = append(result.outgoing[edge.FromStepID], edge)
		result.incoming[edge.ToStepID] = append(result.incoming[edge.ToStepID], edge)
	}
	return result
}

func (c flowGroupingConnections) canFold(previous, current SemanticStep, parentRole string) bool {
	// Invocation identity prevents grouping steps from separate calls. A shared
	// invocation, however, is the proof that two otherwise foldable internal
	// steps belong to the same execution context.
	// Legacy condition text cannot prove that two statements execute under the
	// same decision. Keep their conditions local to their own gateways.
	if previous.Branch != nil || current.Branch != nil {
		return false
	}
	if previous.InvocationID != current.InvocationID || previous.Kind == "return" || previous.Kind == "throw" || current.Kind == "return" || current.Kind == "throw" {
		return false
	}
	if parentRole == "boundary" || parentRole == "effect" || parentRole == "result" {
		return false
	}
	if previous.Anchor.EnclosingSymbolPath == "" || previous.Anchor.EnclosingSymbolPath != current.Anchor.EnclosingSymbolPath || previous.Anchor.RepoRelativePath != current.Anchor.RepoRelativePath {
		return false
	}
	// A step that enters a separately resolved implementation is a core-flow
	// transition. Keep the call visible as its own gateway even when its caller
	// has a direct continuation edge to it.
	for _, edge := range c.outgoing[current.StepID] {
		if edge.ResolutionStatus == "resolved" && (edge.Kind == "resolved_cross_file" || edge.Kind == "external" || edge.Kind == "async") {
			return false
		}
	}
	outgoing, incoming := c.outgoing[previous.StepID], c.incoming[current.StepID]
	if len(outgoing) != 1 || len(incoming) != 1 {
		return false
	}
	edge := outgoing[0]
	if edge.ToStepID != current.StepID || edge.ResolutionStatus != "resolved" || len(edge.Conditions) != 0 {
		return false
	}
	switch edge.Kind {
	case "sequence", "control_flow":
		return true
	default:
		return false
	}
}
