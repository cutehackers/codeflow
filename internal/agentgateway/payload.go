package agentgateway

import (
	"codeflow/internal/curator"
	"codeflow/internal/curator/semantic"
)

// CompactFrame represents a minimal gateway scene for AI agent consumption.
type CompactFrame = curator.CompactFrame

// CompactRadarSummary summarizes direct caller and callee counts.
type CompactRadarSummary = curator.CompactRadarSummary

// CompactFlowPayload represents the ~500-token high-density payload.
type CompactFlowPayload = curator.CompactFlowPayload

// BuildCompactPayload converts a FlowSequence into a high-density AI agent payload.
func BuildCompactPayload(sequence *semantic.FlowSequence, directCallers, directCallees int) CompactFlowPayload {
	return curator.BuildCompactPayload(sequence, directCallers, directCallees)
}
