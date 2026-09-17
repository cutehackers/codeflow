package curator

import (
	"codeflow/internal/collector/fusion"
	"codeflow/internal/curator/semantic"
)

// RawExecutionTrace represents the input sequence of sliced steps and edges for curation.
type RawExecutionTrace struct {
	FlowID   string            `json:"flowId"`
	Steps    []fusion.FlowStep `json:"steps"`
	Edges    []fusion.FlowEdge `json:"edges,omitempty"`
	Unknowns []fusion.Unknown  `json:"unknowns,omitempty"`
}

// CurateFlowSequence executes the 2-stage curation pipeline:
// 1. MacroClumper (block clumping and cycle/recursion folding)
// 2. SignificanceRanker (4~7 frame ceiling, floor preservation)
func (c *Curator) CurateFlowSequence(trace RawExecutionTrace) []FlowSequenceFrame {
	clumper := NewMacroClumper()
	candidates := clumper.Clump(trace.Steps, trace.Edges)

	ranker := NewSignificanceRanker()
	return ranker.Rank(candidates)
}

// CurateFlowSpec adapts a fusion.FlowSpec and runs the 2-stage curation pipeline.
func (c *Curator) CurateFlowSpec(spec fusion.FlowSpec) []FlowSequenceFrame {
	trace := RawExecutionTrace{
		FlowID:   spec.FlowID,
		Steps:    spec.Steps,
		Edges:    spec.Edges,
		Unknowns: spec.Unknowns,
	}
	return c.CurateFlowSequence(trace)
}

// CurateMap bridges semantic.SemanticMapIR into a curated versioned FlowSequence.
func (c *Curator) CurateMap(mapIR *semantic.SemanticMapIR) *semantic.FlowSequence {
	if mapIR == nil {
		return nil
	}

	// Convert semantic steps to fusion steps for 2-stage curation
	var steps []fusion.FlowStep
	for _, s := range mapIR.Steps {
		stepID := s.StepID
		steps = append(steps, fusion.FlowStep{
			StepID:     &stepID,
			Name:       s.Name,
			Kind:       s.Kind,
			Layer:      s.Layer,
			Branch:     s.Branch,
			SideEffect: s.SideEffect,
			StateDelta: s.StateDelta,
			Anchor:     s.Anchor,
			Rules:      s.Rules,
		})
	}

	var edges []fusion.FlowEdge
	for _, e := range mapIR.Edges {
		edges = append(edges, fusion.FlowEdge{
			Kind:             e.Kind,
			ToSymbolPath:     e.ToSymbolPath,
			ResolutionStatus: e.ResolutionStatus,
		})
	}

	trace := RawExecutionTrace{
		FlowID: mapIR.MapID,
		Steps:  steps,
		Edges:  edges,
	}

	frames := c.CurateFlowSequence(trace)

	snapshotID := mapIR.ValidatedAgainstSnapshotID
	if snapshotID == "" {
		snapshotID = mapIR.Basis.ComputedWorkspaceSnapshotID
	}
	if snapshotID == "" {
		snapshotID = mapIR.ComputedBasisID
	}

	return &semantic.FlowSequence{
		SchemaID:        semantic.FlowSequenceSchemaID,
		SchemaVersion:   semantic.FlowSequenceSchemaVersion,
		GenerationID:    mapIR.GenerationID,
		ComputedBasisID: mapIR.ComputedBasisID,
		SnapshotID:      snapshotID,
		Frames:          frames,
	}
}
