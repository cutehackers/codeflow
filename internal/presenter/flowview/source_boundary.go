package flowview

import (
	"fmt"
	"slices"

	"codeflow/internal/analyzer/protocol"
	"codeflow/internal/curator/semantic"
)

// markDetailSourceBoundaries keeps readable steps while marking the verified
// predecessor where a child statement can no longer be read in CodeView.
func markDetailSourceBoundaries(mapIR *semantic.SemanticMapIR, sequence *semantic.FlowSequence, contexts map[string]*FlowContextProjection, snapshot protocol.Snapshot) {
	if mapIR == nil || sequence == nil || len(mapIR.Steps) == 0 {
		return
	}
	steps := make(map[string]semantic.SemanticStep, len(mapIR.Steps))
	for _, step := range mapIR.Steps {
		steps[step.StepID] = step
	}
	frameByStep := make(map[string]*semantic.FlowSequenceFrame, len(mapIR.Steps))
	incoming := make(map[string][]string, len(mapIR.Steps))
	for _, edge := range mapIR.Edges {
		if edge.ResolutionStatus == "resolved" && edge.Kind != "callback_argument" {
			incoming[edge.ToStepID] = append(incoming[edge.ToStepID], edge.FromStepID)
		}
	}
	for i := range sequence.Frames {
		frame := &sequence.Frames[i]
		for _, stepID := range frame.StepRefs {
			frameByStep[stepID] = frame
		}
	}
	for i := range sequence.Frames {
		frame := &sequence.Frames[i]
		for _, stepID := range frame.StepRefs {
			if stepID == frame.PrimaryStepRef || stepID == mapIR.Steps[0].StepID {
				continue
			}
			step, ok := steps[stepID]
			if !ok || validateRepresentativeStep(step, nil, contexts, snapshot, mapIR) == nil {
				continue
			}
			if frame.Status == "verified" {
				frame.Status = "partial"
			}
			boundaryFrames := []string{}
			seen := map[string]bool{stepID: true}
			pending := append([]string(nil), incoming[stepID]...)
			for len(pending) > 0 {
				predecessorID := pending[0]
				pending = pending[1:]
				if seen[predecessorID] {
					continue
				}
				seen[predecessorID] = true
				predecessor, found := steps[predecessorID]
				if !found {
					continue
				}
				if validateRepresentativeStep(predecessor, nil, contexts, snapshot, mapIR) != nil {
					pending = append(pending, incoming[predecessorID]...)
					continue
				}
				previousFrame := frameByStep[predecessorID]
				if previousFrame == nil {
					continue
				}
				if previousFrame.Status == "verified" {
					previousFrame.Status = "partial"
				}
				if !slices.Contains(previousFrame.Outcomes, "boundary") {
					previousFrame.Outcomes = append(previousFrame.Outcomes, "boundary")
				}
				if !slices.Contains(boundaryFrames, previousFrame.FrameID) {
					boundaryFrames = append(boundaryFrames, previousFrame.FrameID)
				}
			}
			if len(boundaryFrames) == 0 {
				if !slices.Contains(frame.Outcomes, "boundary") {
					frame.Outcomes = append(frame.Outcomes, "boundary")
				}
				boundaryFrames = append(boundaryFrames, frame.FrameID)
			}
			if !slices.Contains(boundaryFrames, frame.FrameID) {
				boundaryFrames = append(boundaryFrames, frame.FrameID)
			}
			reason := "선택한 문장의 정확한 코드를 표시할 수 없습니다"
			if context := contexts[stepID]; context != nil && context.SourceLimitation != "" {
				reason = context.SourceLimitation
			}
			sequence.SummaryLimitations = append(sequence.SummaryLimitations, semantic.FlowSummaryLimitation{
				Code:      "source_code_unavailable",
				Message:   fmt.Sprintf("%s의 상세 코드를 읽는 경계: %s", step.Anchor.RepoRelativePath, reason),
				FrameRefs: boundaryFrames,
			})
		}
	}
}
