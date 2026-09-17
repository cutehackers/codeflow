package agentgateway

import (
	"codeflow/internal/curator"
	"codeflow/internal/fusion"
)

// CompactFrame represents a minimal gateway scene for AI agent consumption.
type CompactFrame = curator.CompactFrame

// CompactRadarSummary summarizes direct caller and callee counts.
type CompactRadarSummary = curator.CompactRadarSummary

// CompactFlowPayload represents the ~500-token high-density payload.
type CompactFlowPayload = curator.CompactFlowPayload

// BuildCompactPayload converts a FlowSpec into a high-density AI agent payload.
func BuildCompactPayload(spec *fusion.FlowSpec, frames []curator.FlowSequenceFrame, directCallers, directCallees int) CompactFlowPayload {
	return curator.BuildCompactPayload(spec, frames, directCallers, directCallees)
}
