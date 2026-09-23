package semantic

import (
	"codeflow/internal/collector/fusion"
	"codeflow/internal/collector/slicing"
)

// ProjectFlowSpec adapts a persisted trace without inventing evidence or call
// targets. A repeated target symbol is ambiguous unless the trace identifies
// the invocation. Raw step confidence is not a verified source reference.
func ProjectFlowSpec(spec *fusion.FlowSpec, generationID string) *SemanticMapIR {
	if spec == nil {
		return nil
	}
	m := &SemanticMapIR{
		MapID: "map-" + spec.FlowID, GenerationID: generationID, ComputedBasisID: spec.BasisSha,
		Summary: MapSummary{Requested: spec.Title, Current: spec.Description},
		Steps:   make([]SemanticStep, 0, len(spec.Steps)), Edges: make([]SemanticEdge, 0, len(spec.Edges)),
		Unknowns: append([]fusion.Unknown(nil), spec.Unknowns...), Freshness: "historical", Authority: "historical",
	}
	if spec.Truncated {
		m.Unknowns = append(m.Unknowns, fusion.Unknown{Subject: "flow-traversal", Reason: "분석 순회가 중단되어 이후 처리의 포함 여부를 확인할 수 없습니다."})
	}
	ordinals := make(map[int][]int, len(spec.Steps))
	symbols := make(map[string][]int, len(spec.Steps))
	sliceSteps := make([]slicing.SliceStep, 0, len(spec.Steps))
	for i, step := range spec.Steps {
		sliceSteps = append(sliceSteps, slicing.SliceStep{Ordinal: step.Ordinal, Kind: step.Kind, AssignmentSourceOrdinal: step.AssignmentSourceOrdinal, InvocationID: step.InvocationID, CallerStepOrdinal: step.CallerStepOrdinal, SymbolPath: step.Anchor.EnclosingSymbolPath, Anchor: step.Anchor})
		id := fusion.ComputeStepID(spec.FlowID, step.Ordinal, step.Anchor.EnclosingSymbolPath)
		if step.StepID != nil {
			id = *step.StepID
		}
		m.Steps = append(m.Steps, SemanticStep{AssignmentSourceOrdinal: step.AssignmentSourceOrdinal, InvocationID: step.InvocationID, CallerStepOrdinal: step.CallerStepOrdinal, StepID: id, Ordinal: step.Ordinal, Name: step.Name, TechnicalName: step.Anchor.EnclosingSymbolPath, Anchor: step.Anchor, CodeLens: step.CodeLens, Branch: step.Branch, SideEffect: step.SideEffect, StateDelta: step.StateDelta, Kind: step.Kind, Layer: step.Layer, Rules: append([]string(nil), step.Rules...)})
		ordinals[step.Ordinal] = append(ordinals[step.Ordinal], i)
		symbol := step.Anchor.EnclosingSymbolPath
		if symbol != "" {
			symbols[symbol] = append(symbols[symbol], i)
			qualified := step.Anchor.RepoRelativePath + "#" + symbol
			if qualified != symbol {
				symbols[qualified] = append(symbols[qualified], i)
			}
		}
	}
	for _, edge := range spec.Edges {
		var sources []int
		if edge.StepOrdinal != nil {
			sources = ordinals[*edge.StepOrdinal]
		}
		targets := symbols[edge.ToSymbolPath]
		if edge.TargetStepOrdinal == nil && len(targets) == 1 && m.Steps[targets[0]].InvocationID != "" {
			targets = nil
		}
		if edge.TargetStepOrdinal != nil || len(edge.Conditions) > 0 {
			targets = nil
			reference := slicing.SliceEdge{Conditions: edge.Conditions, Kind: edge.Kind, StepOrdinal: edge.StepOrdinal, TargetStepOrdinal: edge.TargetStepOrdinal, ToSymbolPath: edge.ToSymbolPath, ResolutionStatus: edge.ResolutionStatus}
			validationEdges := []slicing.SliceEdge{reference}
			if edge.Kind == "return" || edge.Kind == "failure" || edge.Kind == "finally" {
				validationEdges = nil
				for _, relation := range spec.Edges {
					validationEdges = append(validationEdges, slicing.SliceEdge{Conditions: relation.Conditions, Kind: relation.Kind, StepOrdinal: relation.StepOrdinal, TargetStepOrdinal: relation.TargetStepOrdinal, ToSymbolPath: relation.ToSymbolPath, ResolutionStatus: relation.ResolutionStatus})
				}
			}
			valid := slicing.ValidateExecutionReferences(sliceSteps, validationEdges) == nil
			if edge.Kind == "failure" {
				valid = validFailureRecoveryEdge(edge, spec.Steps, ordinals, spec.Edges)
			}
			if valid {
				if edge.TargetStepOrdinal != nil {
					targets = ordinals[*edge.TargetStepOrdinal]
				}
			}
		}
		if len(sources) == 1 && len(targets) == 1 && edge.ResolutionStatus == "resolved" {
			conditions := make([]SemanticBranchCondition, 0, len(edge.Conditions))
			for _, condition := range edge.Conditions {
				conditions = append(conditions, SemanticBranchCondition{StepID: m.Steps[ordinals[condition.StepOrdinal][0]].StepID, Outcome: condition.Outcome})
			}
			m.Edges = append(m.Edges, SemanticEdge{Conditions: conditions, FromStepID: m.Steps[sources[0]].StepID, ToStepID: m.Steps[targets[0]].StepID, ToSymbolPath: edge.ToSymbolPath, Kind: edge.Kind, ResolutionStatus: edge.ResolutionStatus})
		} else {
			m.Unknowns = append(m.Unknowns, fusion.Unknown{Subject: edge.ToSymbolPath, Reason: projectionUnresolvedReason(edge)})
			if len(sources) == 1 {
				source := &m.Steps[sources[0]]
				source.Rules = append(source.Rules, "boundary:"+edge.ToSymbolPath)
				m.Edges = append(m.Edges, SemanticEdge{FromStepID: m.Steps[sources[0]].StepID, ToSymbolPath: edge.ToSymbolPath, Kind: edge.Kind, ResolutionStatus: "unresolved"})
			}
		}
	}
	return m
}

// validFailureRecoveryEdge keeps an explicit await rejection or throw inside
// its invocation. It does not infer that a call can throw: only an actual
// throw statement or await step may reach the verified recovery target.
func validFailureRecoveryEdge(edge fusion.FlowEdge, steps []fusion.FlowStep, ordinals map[int][]int, edges []fusion.FlowEdge) bool {
	if edge.StepOrdinal == nil || edge.TargetStepOrdinal == nil || edge.ResolutionStatus != "resolved" || len(ordinals[*edge.StepOrdinal]) != 1 || len(ordinals[*edge.TargetStepOrdinal]) != 1 {
		return false
	}
	from, to := steps[ordinals[*edge.StepOrdinal][0]], steps[ordinals[*edge.TargetStepOrdinal][0]]
	if (from.Kind == "await" || from.Kind == "throw") && from.InvocationID != "" && from.InvocationID == to.InvocationID && from.Anchor.RepoRelativePath == to.Anchor.RepoRelativePath && from.Anchor.EnclosingSymbolPath == to.Anchor.EnclosingSymbolPath && edge.ToSymbolPath == to.Anchor.RepoRelativePath+"#"+to.Anchor.EnclosingSymbolPath {
		return true
	}
	if from.Kind != "throw" || from.InvocationID == "" || from.CallerStepOrdinal == nil || to.InvocationID == "" || edge.ToSymbolPath != to.Anchor.RepoRelativePath+"#"+to.Anchor.EnclosingSymbolPath {
		return false
	}
	callerIndex, callerFound := ordinals[*from.CallerStepOrdinal]
	if !callerFound || len(callerIndex) != 1 {
		return false
	}
	caller := steps[callerIndex[0]]
	if caller.Kind != "call" || caller.InvocationID == from.InvocationID || to.InvocationID != caller.InvocationID || to.Ordinal <= caller.Ordinal {
		return false
	}
	entryOrdinal := 0
	for _, step := range steps {
		if step.InvocationID == from.InvocationID && step.CallerStepOrdinal != nil && *step.CallerStepOrdinal == caller.Ordinal && (entryOrdinal == 0 || step.Ordinal < entryOrdinal) {
			entryOrdinal = step.Ordinal
		}
	}
	if entryOrdinal == 0 {
		return false
	}
	for _, relation := range edges {
		if relation.Kind == "resolved_cross_file" && relation.ResolutionStatus == "resolved" && relation.StepOrdinal != nil && relation.TargetStepOrdinal != nil && *relation.StepOrdinal == caller.Ordinal && *relation.TargetStepOrdinal == entryOrdinal {
			return true
		}
	}
	return false
}

func projectionUnresolvedReason(edge fusion.FlowEdge) string {
	if edge.UnresolvedReason != "" {
		return edge.UnresolvedReason
	}
	return "과거 분석의 직접 연결 대상을 확인할 수 없습니다."
}
