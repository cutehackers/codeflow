package curator

import (
	"codeflow/internal/collector/fusion"
	"codeflow/internal/curator/semantic"
)

// RawExecutionTrace represents the input sequence of sliced steps and edges for curation.
type RawExecutionTrace struct {
	Truncated bool              `json:"truncated,omitempty"`
	BasisSha  string            `json:"basisSha,omitempty"`
	FlowID    string            `json:"flowId"`
	Steps     []fusion.FlowStep `json:"steps"`
	Edges     []fusion.FlowEdge `json:"edges,omitempty"`
	Unknowns  []fusion.Unknown  `json:"unknowns,omitempty"`
}

// CurateFlowSequence preserves the legacy frame-list API while using the
// canonical hierarchy. Call CurateTrace when summary limitations are needed.
func (c *Curator) CurateFlowSequence(trace RawExecutionTrace) []FlowSequenceFrame {
	return c.CurateFlowSpec(fusion.FlowSpec{FlowID: trace.FlowID, BasisSha: trace.BasisSha, Steps: trace.Steps, Edges: trace.Edges, Unknowns: trace.Unknowns, Truncated: trace.Truncated}).Frames
}

// TraceCuration retains both the gateway hierarchy and its source graph.
type TraceCuration struct {
	Sequence *semantic.FlowSequence
	Map      *semantic.SemanticMapIR
}

// CurateTrace validates the canonical hierarchy and preserves analysis limitations.
func (c *Curator) CurateTrace(trace RawExecutionTrace) (*TraceCuration, error) {
	model := semantic.ProjectFlowSpec(&fusion.FlowSpec{FlowID: trace.FlowID, BasisSha: trace.BasisSha, Steps: trace.Steps, Edges: trace.Edges, Unknowns: trace.Unknowns, Truncated: trace.Truncated}, "")
	sequence := c.CurateMap(model)
	if err := semantic.ValidateFlowSequenceReferences(model, sequence); err != nil {
		return nil, err
	}
	return &TraceCuration{Sequence: sequence, Map: model}, nil
}

// CurateFlowSpec uses the same semantic projection as FlowView restoration.
func (c *Curator) CurateFlowSpec(spec fusion.FlowSpec) *semantic.FlowSequence {
	return c.CurateMap(semantic.ProjectFlowSpec(&spec, ""))
}

// CurateMap retains the original map's identity, evidence and relations while
// deriving the canonical gateway hierarchy.
func (c *Curator) CurateMap(mapIR *semantic.SemanticMapIR) *semantic.FlowSequence {
	return semantic.BuildFlowSequence(mapIR)
}
