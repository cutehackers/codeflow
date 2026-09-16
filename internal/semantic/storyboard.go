package semantic

import (
	"fmt"
	"strings"

	"codeflow/internal/slicing"
)

// CollapsedDetail records folded continuous non-critical steps within a frame.
type CollapsedDetail struct {
	Count  int    `json:"count"`
	Reason string `json:"reason"`
}

// StoryboardFrame represents an end-to-end business gateway scene.
type StoryboardFrame struct {
	FrameID         string           `json:"frameId"`
	Ordinal         int              `json:"ordinal"`
	Role            string           `json:"role"` // entry | decision | process | effect | result | boundary
	Title           string           `json:"title"`
	Narrative       string           `json:"narrative,omitempty"`
	TechnicalAnchor string           `json:"technicalAnchor,omitempty"`
	StepRefs        []string         `json:"stepRefs"`
	PrimaryStepRef  string           `json:"primaryStepRef"`
	SourceAnchor    *slicing.Anchor  `json:"sourceAnchor,omitempty"`
	Condition       *string          `json:"condition,omitempty"`
	Outcomes        []string         `json:"outcomes,omitempty"`
	CollapsedDetail *CollapsedDetail `json:"collapsedDetail,omitempty"`
	Architecture    string           `json:"architecture,omitempty"`
	Status          string           `json:"status"` // verified | partial | unknown
	FrameMatchKey   string           `json:"frameMatchKey"`
}

// Storyboard holds the canonical projection of business gateway scenes derived from SemanticMapIR.
type Storyboard struct {
	SchemaID        string            `json:"schemaId"`
	SchemaVersion   int               `json:"schemaVersion"`
	GenerationID    string            `json:"generationId"`
	ComputedBasisID string            `json:"computedBasisId"`
	SnapshotID      string            `json:"snapshotId"`
	Frames          []StoryboardFrame `json:"frames"`
}

// NormalizeFrameMatchKey constructs a stable frame-matching key using canonical
// callable/symbol identity and role, intentionally omitting snapshot IDs and line/byte offsets.
func NormalizeFrameMatchKey(role, symbolPath, technicalName, title string) string {
	roleNorm := strings.ToLower(strings.TrimSpace(role))
	sym := strings.TrimSpace(symbolPath)
	if sym == "" {
		sym = strings.TrimSpace(technicalName)
	}
	if sym == "" {
		sym = strings.TrimSpace(title)
	}
	return fmt.Sprintf("%s|%s", roleNorm, sym)
}

// BuildStoryboard derives a versioned Storyboard projection from a SemanticMapIR.
// It partitions steps at entry, decision, process (state/transaction), external effect,
// result, and boundary, collapsing intermediate continuous steps into the previous frame.
func BuildStoryboard(mapIR *SemanticMapIR) *Storyboard {
	if mapIR == nil {
		return nil
	}

	snapshotID := mapIR.ValidatedAgainstSnapshotID
	if snapshotID == "" {
		snapshotID = mapIR.Basis.ComputedWorkspaceSnapshotID
	}
	if snapshotID == "" {
		snapshotID = mapIR.ComputedBasisID
	}

	sb := &Storyboard{
		SchemaID:        StoryboardSchemaID,
		SchemaVersion:   StoryboardSchemaVersion,
		GenerationID:    mapIR.GenerationID,
		ComputedBasisID: mapIR.ComputedBasisID,
		SnapshotID:      snapshotID,
		Frames:          make([]StoryboardFrame, 0),
	}

	steps := mapIR.Steps
	totalSteps := len(steps)
	if totalSteps == 0 {
		return sb
	}

	// Index external/async edges by fromStepId
	hasExternalOrAsync := make(map[string]bool)
	hasAsync := make(map[string]bool)
	for _, e := range mapIR.Edges {
		if e.ResolutionStatus == "resolved" && (e.Kind == "async" || e.Kind == "external") {
			hasExternalOrAsync[e.FromStepID] = true
			if e.Kind == "async" {
				hasAsync[e.FromStepID] = true
			}
		}
	}

	// Index unknowns by subject
	unknownSubjects := make(map[string]bool)
	for _, u := range mapIR.Unknowns {
		if u.Subject != "" {
			unknownSubjects[u.Subject] = true
		}
	}

	// Boundary targets
	boundaryTargets := make(map[string]bool)
	for _, bt := range mapIR.BoundaryTargets {
		boundaryTargets[bt] = true
	}

	for i, step := range steps {
		role := ""
		isIntermediate := false

		// 1. Entry
		if i == 0 || step.Kind == "user_action" {
			role = "entry"
		} else if isStepBoundary(step, boundaryTargets, unknownSubjects) {
			// 6. Boundary
			role = "boundary"
		} else if step.Kind == "external_effect" || step.Kind == "external" || step.SideEffect != nil || hasExternalOrAsync[step.StepID] {
			// 4. External effect / async handoff
			role = "effect"
		} else if step.Kind == "guard" || step.Kind == "branch" || step.Kind == "decision" || step.Kind == "failure" || step.Branch != nil {
			// 2. Decision / branch / failure
			role = "decision"
		} else if step.Kind == "mutation" || step.StateDelta != nil {
			// 3. Process / transaction boundary / state mutation
			role = "process"
		} else if step.Kind == "return" || i == totalSteps-1 {
			role = "result"
		} else {
			// Continuous intermediate step without state boundary or side effect
			isIntermediate = true
		}

		if isIntermediate && len(sb.Frames) > 0 {
			// Collapse into current frame
			lastIdx := len(sb.Frames) - 1
			sb.Frames[lastIdx].StepRefs = append(sb.Frames[lastIdx].StepRefs, step.StepID)
			if sb.Frames[lastIdx].CollapsedDetail == nil {
				sb.Frames[lastIdx].CollapsedDetail = &CollapsedDetail{
					Count:  0,
					Reason: "연속 내부 처리 단계 접힘",
				}
			}
			sb.Frames[lastIdx].CollapsedDetail.Count++
			if sb.Frames[lastIdx].Condition == nil && step.Branch != nil && *step.Branch != "" {
				sb.Frames[lastIdx].Condition = step.Branch
			}
			continue
		}

		// Fallback role for intermediate if it is the first step (should never happen due to i==0 check)
		if role == "" {
			role = "process"
		}

		title := strings.TrimSpace(step.Name)
		if title == "" {
			title = strings.TrimSpace(step.TechnicalName)
		}
		if title == "" {
			title = "확인되지 않은 처리 목적"
		}

		narrative := ""
		if step.Branch != nil && *step.Branch != "" {
			narrative = "조건 · " + *step.Branch
		} else if step.SideEffect != nil && *step.SideEffect != "" {
			narrative = "외부 효과 · " + *step.SideEffect
		} else if step.StateDelta != nil {
			narrative = fmt.Sprintf("상태 변경: %s -> %s", step.StateDelta.Before, step.StateDelta.After)
		}

		matchKey := NormalizeFrameMatchKey(role, step.Anchor.EnclosingSymbolPath, step.TechnicalName, title)

		// Outcomes
		outcomes := []string{}
		if role == "decision" {
			outcomes = []string{}
		} else if role == "boundary" {
			outcomes = []string{"boundary"}
		} else if role == "effect" {
			if hasAsync[step.StepID] {
				outcomes = []string{"async"}
			}
		}

		// Status determination: lack of evidence -> partial / unknown
		status := "verified"
		if unknownSubjects[step.TechnicalName] || unknownSubjects[step.Name] || len(step.EvidenceRefs) == 0 {
			status = "partial"
		}
		if role == "boundary" {
			status = "unknown"
		}

		frameOrdinal := len(sb.Frames) + 1
		frame := StoryboardFrame{
			FrameID:         fmt.Sprintf("frame-%02d", frameOrdinal),
			Ordinal:         frameOrdinal,
			Role:            role,
			Title:           title,
			Narrative:       narrative,
			TechnicalAnchor: step.TechnicalName,
			StepRefs:        []string{step.StepID},
			PrimaryStepRef:  step.StepID,
			Condition:       step.Branch,
			Outcomes:        outcomes,
			Architecture:    step.Layer,
			Status:          status,
			FrameMatchKey:   matchKey,
		}

		if step.Anchor.RepoRelativePath != "" {
			anchorCopy := step.Anchor
			frame.SourceAnchor = &anchorCopy
		}

		sb.Frames = append(sb.Frames, frame)
	}

	return sb
}

func isStepBoundary(step SemanticStep, boundaries map[string]bool, unknowns map[string]bool) bool {
	if boundaries[step.StepID] || boundaries[step.TechnicalName] {
		return true
	}
	if unknowns[step.TechnicalName] || unknowns[step.Name] {
		for _, r := range step.Rules {
			if strings.Contains(strings.ToLower(r), "boundary") {
				return true
			}
		}
	}
	return false
}

// FindMatchingFrame searches a Storyboard for frames matching the specified FrameMatchKey.
// Returns the matched frame only when exactly one frame matches; otherwise returns nil.
func (sb *Storyboard) FindMatchingFrame(matchKey string) *StoryboardFrame {
	if sb == nil || matchKey == "" {
		return nil
	}
	var matched *StoryboardFrame
	matchCount := 0
	for i := range sb.Frames {
		if sb.Frames[i].FrameMatchKey == matchKey {
			matched = &sb.Frames[i]
			matchCount++
		}
	}
	if matchCount == 1 {
		return matched
	}
	return nil
}
