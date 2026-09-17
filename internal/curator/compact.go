package curator

import (
	"fmt"
	"strings"

	"codeflow/internal/fusion"
)

// CompactFrame represents a minimal gateway scene for AI agent consumption (~500 tokens).
type CompactFrame struct {
	ID     string `json:"id"`
	Gate   string `json:"gate"` // entry | decision | process | effect | result | boundary
	Symbol string `json:"symbol"`
	Steps  int    `json:"steps,omitempty"`
	File   string `json:"file"`
}

// CompactRadarSummary summarizes direct caller and callee counts.
type CompactRadarSummary struct {
	DirectCallers int `json:"directCallers"`
	DirectCallees int `json:"directCallees"`
}

// CompactFlowPayload represents the ~500-token high-density payload.
type CompactFlowPayload struct {
	FlowID       string              `json:"flowId"`
	Summary      string              `json:"summary"`
	Frames       []CompactFrame      `json:"frames"`
	RadarSummary CompactRadarSummary `json:"radarSummary"`
}

// BuildCompactPayload converts a FlowSpec and curated FlowFrames into a high-density AI agent payload.
func BuildCompactPayload(spec *fusion.FlowSpec, frames []FlowFrame, directCallers, directCallees int) CompactFlowPayload {
	if spec == nil {
		return CompactFlowPayload{}
	}

	if len(frames) == 0 {
		c := NewCurator()
		frames = c.CurateFlowSpec(*spec)
	}

	compactFrames := make([]CompactFrame, 0, len(frames))
	symbols := make([]string, 0, len(frames))

	for i, f := range frames {
		sym := f.Title
		if f.TechnicalAnchor != "" {
			sym = f.TechnicalAnchor
		}
		symbols = append(symbols, sym)

		filePos := ""
		if f.SourceAnchor != nil && f.SourceAnchor.RepoRelativePath != "" {
			filePos = f.SourceAnchor.RepoRelativePath
			if f.SourceAnchor.ByteRange[0] > 0 {
				filePos = fmt.Sprintf("%s:%d", f.SourceAnchor.RepoRelativePath, (f.SourceAnchor.ByteRange[0]/40)+1)
			}
		}

		stepsCount := len(f.StepRefs)
		frameID := fmt.Sprintf("f%d", i+1)
		if f.FrameID != "" {
			frameID = strings.TrimPrefix(f.FrameID, "frame-")
			if !strings.HasPrefix(frameID, "f") {
				frameID = "f" + frameID
			}
		}

		compactFrames = append(compactFrames, CompactFrame{
			ID:     frameID,
			Gate:   f.Role,
			Symbol: sym,
			Steps:  stepsCount,
			File:   filePos,
		})
	}

	summary := strings.Join(symbols, " -> ")

	return CompactFlowPayload{
		FlowID:  spec.FlowID,
		Summary: summary,
		Frames:  compactFrames,
		RadarSummary: CompactRadarSummary{
			DirectCallers: directCallers,
			DirectCallees: directCallees,
		},
	}
}
