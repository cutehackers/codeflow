package semantic

import (
	"fmt"
	"strings"

	"codeflow/internal/collector/slicing"
)

// CollapsedDetail records folded continuous non-critical steps within a frame.
type CollapsedDetail struct {
	Count  int    `json:"count"`
	Reason string `json:"reason"`
}

// FlowSequenceFrame represents an end-to-end business gateway scene.
type FlowSequenceFrame struct {
	FrameID         string           `json:"frameID"`
	Ordinal         int              `json:"ordinal"`
	Role            string           `json:"role"` // entry | decision | process | effect | result | boundary
	Title           string           `json:"title"`
	Text            string           `json:"text,omitempty"`
	TechnicalAnchor string           `json:"technicalAnchor,omitempty"`
	StepRefs        []string         `json:"stepRefs"`
	PrimaryStepRef  string           `json:"primaryStepRef"`
	SourceAnchor    *slicing.Anchor  `json:"sourceAnchor,omitempty"`
	Condition       *string          `json:"condition,omitempty"`
	Outcomes        []string         `json:"outcomes,omitempty"`
	CollapsedDetail *CollapsedDetail `json:"collapsedDetail,omitempty"`
	Status          string           `json:"status"` // verified | partial | unknown
	FrameMatchKey   string           `json:"frameMatchKey"`
	IsRecursion     bool             `json:"isRecursion,omitempty"`
}

// FlowSequence holds the canonical projection of business gateway scenes derived from SemanticMapIR.
type FlowSequence struct {
	SummaryLimitations []FlowSummaryLimitation `json:"summaryLimitations,omitempty"`
	SchemaID           string                  `json:"schemaId"`
	SchemaVersion      int                     `json:"schemaVersion"`
	FlowID             string                  `json:"flowID"`
	GenerationID       string                  `json:"generationId"`
	ComputedBasisID    string                  `json:"computedBasisId"`
	SnapshotID         string                  `json:"snapshotID"`
	Frames             []FlowSequenceFrame     `json:"frames"`
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

// BuildFlowSequence derives a versioned FlowSequence projection from a SemanticMapIR.
// It partitions steps at entry, decision, process (state/transaction), external effect,
// result, and boundary, collapsing intermediate continuous steps into the previous frame.
func BuildFlowSequence(mapIR *SemanticMapIR) *FlowSequence {
	return buildFlowSequence(mapIR, nil)
}

func buildFlowSequence(mapIR *SemanticMapIR, review *FlowCurationReview) *FlowSequence {
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

	flowID := strings.TrimPrefix(mapIR.MapID, "map-")
	if flowID == "" {
		flowID = mapIR.GenerationID
	}
	if flowID == "" {
		flowID = mapIR.ComputedBasisID
	}
	sequence := &FlowSequence{
		SchemaID:        FlowSequenceSchemaID,
		SchemaVersion:   FlowSequenceSchemaVersion,
		FlowID:          flowID,
		GenerationID:    mapIR.GenerationID,
		ComputedBasisID: mapIR.ComputedBasisID,
		SnapshotID:      snapshotID,
		Frames:          make([]FlowSequenceFrame, 0),
	}

	steps := mapIR.Steps
	totalSteps := len(steps)
	if totalSteps == 0 {
		return sequence
	}
	assignmentSources := make(map[int]bool)
	for _, step := range steps {
		if step.AssignmentSourceOrdinal != nil {
			assignmentSources[*step.AssignmentSourceOrdinal] = true
		}
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

	// Index evidence by evidenceId
	evidenceMap := make(map[string]SemanticEvidence, len(mapIR.Evidence))
	for _, ev := range mapIR.Evidence {
		evidenceMap[ev.EvidenceID] = ev
	}

	connections := groupingConnections(mapIR.Edges)
	limitedFrames := make([]string, 0)
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
		} else if step.Kind == "guard" || step.Kind == "branch" || step.Kind == "decision" || step.Kind == "failure" || (step.Kind == "" && step.Branch != nil) {
			// A known statement kind separates a decision from a call or return
			// that merely executes under an inherited condition.
			// Untyped historical steps retain their condition-based classification.
			role = "decision"
		} else if step.Kind == "mutation" || step.StateDelta != nil {
			// 3. Process / transaction boundary / state mutation
			role = "process"
		} else if step.InvocationID != "" && (step.Kind == "return" || step.Kind == "throw") {
			// Local completion does not prove termination of the core flow.
			role = "process"
		} else if step.Kind == "return" || step.Kind == "result" {
			role = "result"
		} else {
			// Continuous intermediate step without state boundary or side effect
			isIntermediate = true
		}

		if review != nil {
			review.Candidates = append(review.Candidates, gatewayCandidateDecision(step, role, isIntermediate))
		}

		if isIntermediate && i > 0 && len(sequence.Frames) > 0 &&
			!isStepBoundary(steps[i-1], boundaryTargets, unknownSubjects) &&
			!assignmentSources[step.Ordinal] &&
			connections.canFold(steps[i-1], step, sequence.Frames[len(sequence.Frames)-1].Role) {
			// Collapse into current frame
			lastIdx := len(sequence.Frames) - 1
			sequence.Frames[lastIdx].StepRefs = append(sequence.Frames[lastIdx].StepRefs, step.StepID)
			if sequence.Frames[lastIdx].CollapsedDetail == nil {
				sequence.Frames[lastIdx].CollapsedDetail = &CollapsedDetail{
					Count:  0,
					Reason: "같은 호출 문맥의 확인된 직접 처리 연결",
				}
			}
			sequence.Frames[lastIdx].CollapsedDetail.Count++
			collapsedStatus := flowStepStatus(step, unknownSubjects, evidenceMap)
			if collapsedStatus == "unknown" && sequence.Frames[lastIdx].Status == "verified" {
				sequence.Frames[lastIdx].Status = "partial"
			} else if collapsedStatus == "partial" && sequence.Frames[lastIdx].Status == "verified" {
				sequence.Frames[lastIdx].Status = "partial"
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

		text := ""
		if step.Branch != nil && *step.Branch != "" {
			text = "조건 · " + *step.Branch
		} else if step.SideEffect != nil && *step.SideEffect != "" {
			text = "외부 효과 · " + *step.SideEffect
		} else if step.StateDelta != nil {
			text = fmt.Sprintf("상태 변경: %s -> %s", step.StateDelta.Before, step.StateDelta.After)
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
		status := flowStepStatus(step, unknownSubjects, evidenceMap)
		if role == "boundary" || isStepBoundary(step, boundaryTargets, unknownSubjects) {
			status = "unknown"
		}

		frameOrdinal := len(sequence.Frames) + 1
		frame := FlowSequenceFrame{
			FrameID:         fmt.Sprintf("frame-%02d", frameOrdinal),
			Ordinal:         frameOrdinal,
			Role:            role,
			Title:           title,
			Text:            text,
			TechnicalAnchor: step.TechnicalName,
			StepRefs:        []string{step.StepID},
			PrimaryStepRef:  step.StepID,

			Outcomes:      outcomes,
			Status:        status,
			FrameMatchKey: matchKey,
		}

		if step.Branch != nil {
			condition := *step.Branch
			frame.Condition = &condition
		}
		if step.Anchor.RepoRelativePath != "" {
			anchorCopy := step.Anchor
			if step.Anchor.SymbolRange != nil {
				symbolRange := *step.Anchor.SymbolRange
				anchorCopy.SymbolRange = &symbolRange
			}
			frame.SourceAnchor = &anchorCopy
		}

		hasIncoming := false
		for _, edge := range connections.incoming[step.StepID] {
			if edge.ResolutionStatus == "resolved" {
				hasIncoming = true
				break
			}
		}
		if i > 0 && (!hasIncoming || (step.InvocationID != "" && isIntermediate)) {
			limitedFrames = append(limitedFrames, frame.FrameID)
		}
		sequence.Frames = append(sequence.Frames, frame)
	}

	if len(limitedFrames) > 0 {
		sequence.SummaryLimitations = []FlowSummaryLimitation{{Code: "grouping_evidence_missing", Message: "일부 처리를 관문으로 묶지 못했습니다. 실행 연결 또는 같은 처리 목적의 근거가 부족해 단계를 구분해 표시합니다.", FrameRefs: limitedFrames}}
	}
	groupExpressionEvaluations(sequence, steps, connections, boundaryTargets, unknownSubjects)
	groupDecisionEvaluations(sequence, steps, connections, boundaryTargets, unknownSubjects)
	groupAssignmentResults(sequence, steps, connections, boundaryTargets, unknownSubjects)
	groupCoreFlowExecutionSegments(sequence, steps, connections)
	if review != nil {
		completeCurationReview(review, sequence, connections, mapIR)
	}
	return sequence
}

func isStepBoundary(step SemanticStep, boundaries map[string]bool, unknowns map[string]bool) bool {
	if boundaries[step.StepID] || boundaries[step.TechnicalName] {
		return true
	}
	for _, r := range step.Rules {
		if strings.HasPrefix(r, "boundary:") {
			return true
		}
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

// FindMatchingFrame searches a FlowSequence for frames matching the specified FrameMatchKey.
// Returns the matched frame only when exactly one frame matches; otherwise returns nil.
func (sequence *FlowSequence) FindMatchingFrame(matchKey string) *FlowSequenceFrame {
	if sequence == nil || matchKey == "" {
		return nil
	}
	var matched *FlowSequenceFrame
	matchCount := 0
	for i := range sequence.Frames {
		if sequence.Frames[i].FrameMatchKey == matchKey {
			matched = &sequence.Frames[i]
			matchCount++
		}
	}
	if matchCount == 1 {
		return matched
	}
	return nil
}

func flowStepStatus(s SemanticStep, unknownSubjects map[string]bool, evidenceMap map[string]SemanticEvidence) string {
	if unknownSubjects[s.TechnicalName] || unknownSubjects[s.Name] {
		return "unknown"
	}
	if len(s.EvidenceRefs) == 0 {
		return "partial"
	}
	verifiedCount := 0
	unknownCount := 0
	for _, ref := range s.EvidenceRefs {
		ev, ok := evidenceMap[ref]
		if !ok {
			return "partial"
		}
		switch ev.ValidationStatus {
		case "verified":
			verifiedCount++
		case "unknown", "invalid":
			unknownCount++
		default:
			// partial or unstated
		}
	}
	if unknownCount == len(s.EvidenceRefs) {
		return "unknown"
	}
	if verifiedCount == len(s.EvidenceRefs) && unknownCount == 0 {
		return "verified"
	}
	return "partial"
}
