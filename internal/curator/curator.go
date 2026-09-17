// Package curator projects and curates FlowSequence data from detailed flow execution traces.
package curator

import (
	"codeflow/internal/curator/semantic"
)

// FlowSequenceFrame is the canonical FlowSequence frame representation.
type FlowSequenceFrame = semantic.FlowSequenceFrame

// FlowSequence holds the canonical projection of business gateway scenes.
type FlowSequence = semantic.FlowSequence

// CollapsedDetail records folded continuous non-critical steps within a frame.
type CollapsedDetail = semantic.CollapsedDetail

// Curator handles FlowSequence projection and progressive timeline curation.
type Curator struct{}

// NewCurator creates a Curator instance.
func NewCurator() *Curator {
	return &Curator{}
}

// BuildFlowSequence projects a SemanticMapIR into a canonical FlowSequence.
func (c *Curator) BuildFlowSequence(mapIR *semantic.SemanticMapIR) *FlowSequence {
	return semantic.BuildFlowSequence(mapIR)
}
