// Package curator projects and curates macro-storyboards from detailed flow execution traces.
package curator

import (
	"codeflow/internal/semantic"
)

// FlowFrame is the canonical story frame representation.
type FlowFrame = semantic.FlowFrame

// StoryboardFrame is an alias retained for backward compatibility.
type StoryboardFrame = semantic.StoryboardFrame

// Storyboard holds the canonical projection of business gateway scenes.
type Storyboard = semantic.Storyboard

// CollapsedDetail records folded continuous non-critical steps within a frame.
type CollapsedDetail = semantic.CollapsedDetail

// Curator handles storyboard projection and progressive timeline curation.
type Curator struct{}

// NewCurator creates a Curator instance.
func NewCurator() *Curator {
	return &Curator{}
}

// BuildStoryboard projects a SemanticMapIR into a canonical Storyboard.
func (c *Curator) BuildStoryboard(mapIR *semantic.SemanticMapIR) *Storyboard {
	return semantic.BuildStoryboard(mapIR)
}
