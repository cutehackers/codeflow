package curator

import (
	"fmt"
	"sort"
)

// SignificanceRanker enforces the 4~7 frame ceiling and absorbs lower-priority frames.
type SignificanceRanker struct{}

// NewSignificanceRanker creates a SignificanceRanker instance.
func NewSignificanceRanker() *SignificanceRanker {
	return &SignificanceRanker{}
}

// Rank takes clumped candidate frames and reduces them to 4~7 high-significance FlowFrames.
// For traces with <3 steps, it preserves the 1~2 frames without creating dummy cards.
func (sr *SignificanceRanker) Rank(candidates []CandidateFrame) []FlowFrame {
	if len(candidates) == 0 {
		return []FlowFrame{
			{
				FrameID:        "frame-01",
				Ordinal:        1,
				Role:           "boundary",
				Title:          "분석 대상 없음",
				PrimaryStepRef: "empty",
				StepRefs:       []string{"empty"},
				Status:         "unknown",
			},
		}
	}

	// Floor bound: if candidates <= 7, keep all of them (do not inject dummy cards for 1-2 steps)
	if len(candidates) <= 7 {
		frames := make([]FlowFrame, len(candidates))
		for i, c := range candidates {
			ordinal := i + 1
			matchKey := fmt.Sprintf("%s|%s", c.Role, c.Title)
			var collapsed *CollapsedDetail
			if c.CollapsedDetail != nil {
				collapsed = &CollapsedDetail{
					Count:  c.CollapsedDetail.Count,
					Reason: c.CollapsedDetail.Reason,
				}
			}
			status := c.Status
			if status == "" {
				if c.Role == "boundary" {
					status = "unknown"
				} else {
					status = "verified"
				}
			}
			frames[i] = FlowFrame{
				FrameID:         fmt.Sprintf("frame-%02d", ordinal),
				Ordinal:         ordinal,
				Role:            c.Role,
				Title:           c.Title,
				PrimaryStepRef:  c.PrimaryStepRef,
				StepRefs:        c.StepRefs,
				Condition:       c.Condition,
				SourceAnchor:    &c.Anchor,
				Status:          status,
				FrameMatchKey:   matchKey,
				IsRecursion:     c.IsRecursion,
				CollapsedDetail: collapsed,
			}
		}
		return frames
	}

	// Ceiling bound: candidates > 7. We select strictly 7 frames.
	// Keep index 0 (entry) and index len(candidates)-1 (result/tail).
	targetCount := 7
	keepIndices := make(map[int]bool)
	keepIndices[0] = true
	keepIndices[len(candidates)-1] = true

	// Rank intermediate indices by significance score
	type scoredIndex struct {
		index int
		score int
	}
	intermediates := make([]scoredIndex, 0, len(candidates)-2)
	for i := 1; i < len(candidates)-1; i++ {
		score := candidates[i].Score
		if candidates[i].IsRecursion {
			score += 1000 // Recursion scenes must never be dropped
		}
		intermediates = append(intermediates, scoredIndex{index: i, score: score})
	}

	// Stable sort descending by score
	sort.SliceStable(intermediates, func(i, j int) bool {
		return intermediates[i].score > intermediates[j].score
	})

	needed := targetCount - len(keepIndices)
	for i := 0; i < needed && i < len(intermediates); i++ {
		keepIndices[intermediates[i].index] = true
	}

	// Build final list of selected frames in original chronological order
	var orderedSelectedIndices []int
	for i := range candidates {
		if keepIndices[i] {
			orderedSelectedIndices = append(orderedSelectedIndices, i)
		}
	}

	// Absorb unselected frames into the nearest preceding selected frame
	frames := make([]FlowFrame, 0, len(orderedSelectedIndices))
	currentSelectedPtr := -1

	for i, c := range candidates {
		if keepIndices[i] {
			currentSelectedPtr++
			ordinal := currentSelectedPtr + 1
			matchKey := fmt.Sprintf("%s|%s", c.Role, c.Title)
			var collapsed *CollapsedDetail
			if c.CollapsedDetail != nil {
				collapsed = &CollapsedDetail{
					Count:  c.CollapsedDetail.Count,
					Reason: c.CollapsedDetail.Reason,
				}
			}
			stepRefsCopy := make([]string, len(c.StepRefs))
			copy(stepRefsCopy, c.StepRefs)

			status := c.Status
			if status == "" {
				if c.Role == "boundary" {
					status = "unknown"
				} else {
					status = "verified"
				}
			}
			frames = append(frames, FlowFrame{
				FrameID:         fmt.Sprintf("frame-%02d", ordinal),
				Ordinal:         ordinal,
				Role:            c.Role,
				Title:           c.Title,
				PrimaryStepRef:  c.PrimaryStepRef,
				StepRefs:        stepRefsCopy,
				Condition:       c.Condition,
				SourceAnchor:    &c.Anchor,
				Status:          status,
				FrameMatchKey:   matchKey,
				IsRecursion:     c.IsRecursion,
				CollapsedDetail: collapsed,
			})
		} else {
			// Absorb into current preceding frame
			if currentSelectedPtr >= 0 && currentSelectedPtr < len(frames) {
				frames[currentSelectedPtr].StepRefs = append(frames[currentSelectedPtr].StepRefs, c.StepRefs...)
				if (c.Status == "unknown" || c.Status == "partial" || c.Role == "boundary") && frames[currentSelectedPtr].Status == "verified" {
					frames[currentSelectedPtr].Status = "partial"
				}
				if frames[currentSelectedPtr].CollapsedDetail == nil {
					frames[currentSelectedPtr].CollapsedDetail = &CollapsedDetail{
						Count:  0,
						Reason: "하위 중요도 단계 접힘",
					}
				}
				frames[currentSelectedPtr].CollapsedDetail.Count += len(c.StepRefs)
			}
		}
	}

	return frames
}
