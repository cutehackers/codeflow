package curator

import (
	"codeflow/internal/collector/fusion"
	"codeflow/internal/collector/slicing"
	"fmt"
	"strings"

	"codeflow/internal/curator/semantic"
)

// CompactFrame represents a minimal gateway scene for AI agent consumption.
type CompactFrame struct {
	Status         string   `json:"status"`
	Ordinal        int      `json:"ordinal"`
	PrimaryStepRef string   `json:"primaryStepRef"`
	StepRefs       []string `json:"stepRefs"`
	ID             string   `json:"id"`
	Gate           string   `json:"gate"` // entry | decision | process | effect | result | boundary
	Symbol         string   `json:"symbol"`
	Steps          int      `json:"steps,omitempty"`
	File           string   `json:"file"`
}

// CompactRadarSummary summarizes direct caller and callee counts.
type CompactRadarSummary struct {
	DirectCallers int `json:"directCallers"`
	DirectCallees int `json:"directCallees"`
}

// CompactStep retains a source location and identity without source text or proof internals.
type CompactStep struct {
	CallerStepOrdinal       *int           `json:"callerStepOrdinal,omitempty"`
	Ordinal                 int            `json:"ordinal,omitempty"`
	AssignmentSourceOrdinal *int           `json:"assignmentSourceOrdinal,omitempty"`
	StepID                  string         `json:"stepId"`
	Kind                    string         `json:"kind,omitempty"`
	Name                    string         `json:"name"`
	InvocationID            string         `json:"invocationId,omitempty"`
	Anchor                  slicing.Anchor `json:"anchor"`
}

// CompactFlowPayload preserves the hierarchy and optional source relationships.
type CompactFlowPayload struct {
	Steps              []CompactStep                    `json:"steps,omitempty"`
	Edges              []semantic.SemanticEdge          `json:"edges,omitempty"`
	Unknowns           []fusion.Unknown                 `json:"unknowns,omitempty"`
	SummaryLimitations []semantic.FlowSummaryLimitation `json:"summaryLimitations,omitempty"`
	FlowID             string                           `json:"flowId"`
	Summary            string                           `json:"summary"`
	Frames             []CompactFrame                   `json:"frames"`
	RadarSummary       *CompactRadarSummary             `json:"radarSummary,omitempty"`
}

// BuildCompactPayload projects a complete sequence without regrouping it.
// Negative relation counts omit the optional radar summary.
func BuildCompactPayload(sequence *semantic.FlowSequence, directCallers, directCallees int) CompactFlowPayload {
	if sequence == nil {
		return CompactFlowPayload{}
	}
	frames := sequence.Frames
	limitations := make([]semantic.FlowSummaryLimitation, len(sequence.SummaryLimitations))
	for i, limitation := range sequence.SummaryLimitations {
		limitations[i] = limitation
		limitations[i].FrameRefs = append([]string(nil), limitation.FrameRefs...)
	}

	compactFrames := make([]CompactFrame, 0, len(frames))
	symbols := make([]string, 0, len(frames))

	for _, f := range frames {
		sym := f.Title
		if f.TechnicalAnchor != "" {
			sym = f.TechnicalAnchor
		}
		symbols = append(symbols, sym)

		filePos := ""
		if f.SourceAnchor != nil && f.SourceAnchor.RepoRelativePath != "" {
			filePos = f.SourceAnchor.RepoRelativePath
		}

		stepsCount := len(f.StepRefs)

		compactFrames = append(compactFrames, CompactFrame{
			ID:             f.FrameID,
			Status:         f.Status,
			Ordinal:        f.Ordinal,
			PrimaryStepRef: f.PrimaryStepRef,
			StepRefs:       append([]string(nil), f.StepRefs...),
			Gate:           f.Role,
			Symbol:         sym,
			Steps:          stepsCount,
			File:           filePos,
		})
	}

	summary := strings.Join(symbols, " · ")

	var radar *CompactRadarSummary
	if directCallers >= 0 && directCallees >= 0 {
		radar = &CompactRadarSummary{DirectCallers: directCallers, DirectCallees: directCallees}
	}
	return CompactFlowPayload{
		FlowID:             sequence.FlowID,
		SummaryLimitations: limitations,
		Summary:            summary,
		Frames:             compactFrames,
		RadarSummary:       radar,
	}
}

// BuildCompactAnalysisPayload retains the source graph of an existing hierarchy.
func BuildCompactAnalysisPayload(model *semantic.SemanticMapIR, sequence *semantic.FlowSequence) (CompactFlowPayload, error) {
	if err := semantic.ValidateFlowSequenceReferences(model, sequence); err != nil {
		return CompactFlowPayload{}, fmt.Errorf("compact analysis: %w", err)
	}
	payload := BuildCompactPayload(sequence, -1, -1)
	payload.Steps = make([]CompactStep, 0, len(model.Steps))
	for _, step := range model.Steps {
		anchor := step.Anchor
		if anchor.SymbolRange != nil {
			span := *anchor.SymbolRange
			anchor.SymbolRange = &span
		}
		var assignmentSource *int
		if step.AssignmentSourceOrdinal != nil {
			value := *step.AssignmentSourceOrdinal
			assignmentSource = &value
		}
		var caller *int
		if step.CallerStepOrdinal != nil {
			value := *step.CallerStepOrdinal
			caller = &value
		}
		payload.Steps = append(payload.Steps, CompactStep{CallerStepOrdinal: caller, Ordinal: step.Ordinal, AssignmentSourceOrdinal: assignmentSource, StepID: step.StepID, Kind: step.Kind, Name: step.Name, InvocationID: step.InvocationID, Anchor: anchor})
	}
	payload.Edges = make([]semantic.SemanticEdge, len(model.Edges))
	for i, edge := range model.Edges {
		payload.Edges[i] = edge
		payload.Edges[i].Conditions = append([]semantic.SemanticBranchCondition(nil), edge.Conditions...)
	}
	payload.Unknowns = append([]fusion.Unknown(nil), model.Unknowns...)
	return payload, nil
}
