package semantic

import (
	"fmt"
	"sort"
	"strings"
)

type decisionPurposeCategory string

const (
	decisionPurposeUnknown      decisionPurposeCategory = ""
	decisionPurposeAuth         decisionPurposeCategory = "auth"
	decisionPurposeValidation   decisionPurposeCategory = "validation"
	decisionPurposeLimit        decisionPurposeCategory = "limit"
	decisionPurposePrecondition decisionPurposeCategory = "precondition"
	decisionPurposeMixed        decisionPurposeCategory = "mixed"
)

func purposeLabel(category decisionPurposeCategory) string {
	switch category {
	case decisionPurposeAuth:
		return "권한·인증 확인"
	case decisionPurposeValidation:
		return "입력값 검증"
	case decisionPurposeLimit:
		return "한도·한계 확인"
	case decisionPurposePrecondition:
		return "상태·준비 확인"
	default:
		return "조건 판단"
	}
}

func matchAuth(text string) bool {
	if strings.Contains(text, "permission") || strings.Contains(text, "authorize") ||
		strings.Contains(text, "role") || strings.Contains(text, "credential") ||
		strings.Contains(text, "access") || strings.Contains(text, "forbidden") ||
		strings.Contains(text, "unauthorized") || strings.Contains(text, "allow") ||
		strings.Contains(text, "token") || strings.Contains(text, "authenticate") ||
		strings.Contains(text, "authenticated") || strings.Contains(text, "authentication") ||
		strings.Contains(text, "권한") || strings.Contains(text, "인증") ||
		strings.Contains(text, "인가") || strings.Contains(text, "접근") ||
		strings.Contains(text, "토큰") {
		return true
	}
	for _, part := range strings.FieldsFunc(text, func(r rune) bool {
		return !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_')
	}) {
		p := strings.ToLower(part)
		if strings.Contains(p, "auth") && !strings.Contains(p, "author") && !strings.Contains(p, "authority") {
			return true
		}
	}
	return false
}

func matchLimit(text string) bool {
	return strings.Contains(text, "limit") || strings.Contains(text, "quota") ||
		strings.Contains(text, "rate") || strings.Contains(text, "threshold") ||
		strings.Contains(text, "balance") || strings.Contains(text, "amount") ||
		strings.Contains(text, "budget") || strings.Contains(text, "한도") ||
		strings.Contains(text, "잔액") || strings.Contains(text, "초과")
}

func matchValidation(text string) bool {
	return strings.Contains(text, "validate") || strings.Contains(text, "valid") ||
		strings.Contains(text, "schema") || strings.Contains(text, "format") ||
		strings.Contains(text, "required") || strings.Contains(text, "verify") ||
		strings.Contains(text, "검증") || strings.Contains(text, "유효") ||
		strings.Contains(text, "형식") || strings.Contains(text, "누락")
}

func matchPrecondition(text string) bool {
	return strings.Contains(text, "ready") || strings.Contains(text, "init") ||
		strings.Contains(text, "state") || strings.Contains(text, "status") ||
		strings.Contains(text, "enabled") || strings.Contains(text, "active") ||
		strings.Contains(text, "상태") || strings.Contains(text, "준비") ||
		strings.Contains(text, "활성")
}

func classifyDecisionPurpose(step SemanticStep) decisionPurposeCategory {
	matches := make(map[decisionPurposeCategory]bool)

	// 1. Explicit rules take highest priority
	for _, rule := range step.Rules {
		r := strings.ToLower(rule)
		if matchAuth(r) {
			matches[decisionPurposeAuth] = true
		}
		if matchLimit(r) {
			matches[decisionPurposeLimit] = true
		}
		if matchValidation(r) {
			matches[decisionPurposeValidation] = true
		}
		if matchPrecondition(r) {
			matches[decisionPurposePrecondition] = true
		}
	}

	// 2. Text heuristics from TechnicalName, Name, StepID, and Branch
	if len(matches) == 0 {
		text := strings.ToLower(step.TechnicalName + " " + step.Name + " " + step.StepID)
		if step.Branch != nil {
			text += " " + strings.ToLower(*step.Branch)
		}

		if matchAuth(text) {
			matches[decisionPurposeAuth] = true
		}
		if matchLimit(text) {
			matches[decisionPurposeLimit] = true
		}
		if matchValidation(text) {
			matches[decisionPurposeValidation] = true
		}
		if matchPrecondition(text) {
			matches[decisionPurposePrecondition] = true
		}
	}

	if len(matches) > 1 {
		return decisionPurposeMixed
	}
	for cat := range matches {
		return cat
	}
	return decisionPurposeUnknown
}

// groupDecisionEvaluations groups consecutive validation, authorization, or
// state precondition decision steps that share the same verified purpose and have
// direct control_flow without intervening mutations, effects, or boundaries.
//
// Decisions with different purposes (e.g. permission check vs limit check) are
// strictly separated into independent gateways.
func groupDecisionEvaluations(sequence *FlowSequence, steps []SemanticStep, connections flowGroupingConnections, boundaries, unknowns map[string]bool) {
	if sequence == nil || len(sequence.Frames) < 2 {
		return
	}

	stepMap := make(map[string]SemanticStep, len(steps))
	stepIndexMap := make(map[string]int, len(steps))
	for i, s := range steps {
		stepMap[s.StepID] = s
		stepIndexMap[s.StepID] = i
	}

	frames := make(map[string]int, len(steps))
	for i, frame := range sequence.Frames {
		if len(frame.StepRefs) == 1 {
			frames[frame.StepRefs[0]] = i
		}
	}

	removed := make(map[int]bool)
	grouped := make(map[string]bool)

	// Identify chains of decisions with the same purpose category
	for i := 0; i < len(steps); i++ {
		firstStep := steps[i]
		firstFrameIdx, ok := frames[firstStep.StepID]
		if !ok || removed[firstFrameIdx] || grouped[sequence.Frames[firstFrameIdx].FrameID] {
			continue
		}
		if sequence.Frames[firstFrameIdx].Role != "decision" {
			continue
		}
		if isStepBoundary(firstStep, boundaries, unknowns) || firstStep.StateDelta != nil || firstStep.SideEffect != nil {
			continue
		}

		category := classifyDecisionPurpose(firstStep)
		if category == decisionPurposeUnknown || category == decisionPurposeMixed {
			continue
		}

		chain := []SemanticStep{firstStep}
		curr := firstStep

		for {
			// Find direct successor decision in the same invocation
			var nextStep *SemanticStep
			candidateEdges := append([]SemanticEdge(nil), connections.outgoing[curr.StepID]...)
			sort.SliceStable(candidateEdges, func(a, b int) bool {
				return candidateEdges[a].ToStepID < candidateEdges[b].ToStepID
			})

			// Check for fork to multiple alternative decisions
			decisionTargetCount := 0
			for _, edge := range candidateEdges {
				if edge.Kind != "control_flow" || edge.ResolutionStatus != "resolved" {
					continue
				}
				if target, exists := stepMap[edge.ToStepID]; exists {
					if targetFrameIdx, ok := frames[target.StepID]; ok && sequence.Frames[targetFrameIdx].Role == "decision" {
						decisionTargetCount++
					}
				}
			}
			if decisionTargetCount > 1 {
				// Multiple decision branches (fork) cannot be treated as a single linear sequence
				break
			}

			for _, edge := range candidateEdges {
				if edge.Kind != "control_flow" || edge.ResolutionStatus != "resolved" {
					continue
				}
				cand, exists := stepMap[edge.ToStepID]
				if !exists {
					continue
				}
				candFrameIdx, candOK := frames[cand.StepID]
				if !candOK || removed[candFrameIdx] || grouped[sequence.Frames[candFrameIdx].FrameID] {
					continue
				}
				if sequence.Frames[candFrameIdx].Role != "decision" {
					continue
				}
				if isStepBoundary(cand, boundaries, unknowns) || cand.StateDelta != nil || cand.SideEffect != nil {
					continue
				}
				// Must share identical invocation context, and file/symbol scope
				if cand.InvocationID != firstStep.InvocationID ||
					cand.Anchor.RepoRelativePath != firstStep.Anchor.RepoRelativePath ||
					cand.Anchor.EnclosingSymbolPath != firstStep.Anchor.EnclosingSymbolPath {
					continue
				}

				candCategory := classifyDecisionPurpose(cand)
				// Strict separation: if category differs, stop chain and keep separate
				if candCategory != category {
					continue
				}

				// Cand must not be a join point with other incoming control flow edges
				hasOtherIncoming := false
				for _, inEdge := range connections.incoming[cand.StepID] {
					if inEdge.Kind == "control_flow" && inEdge.ResolutionStatus == "resolved" && inEdge.FromStepID != curr.StepID {
						hasOtherIncoming = true
						break
					}
				}
				if hasOtherIncoming {
					continue
				}

				// Verify there are no intervening mutations or external effects between curr and cand in execution order
				currIdx := stepIndexMap[curr.StepID]
				candIdx := stepIndexMap[cand.StepID]
				if candIdx <= currIdx {
					continue
				}
				intervening := false
				for mid := currIdx + 1; mid < candIdx; mid++ {
					m := steps[mid]
					if m.StateDelta != nil || m.Kind == "mutation" || m.SideEffect != nil || m.Kind == "effect" || isStepBoundary(m, boundaries, unknowns) {
						intervening = true
						break
					}
				}
				if intervening {
					continue
				}

				nextStep = &cand
				break
			}

			if nextStep == nil {
				break
			}
			chain = append(chain, *nextStep)
			curr = *nextStep
		}

		if len(chain) < 2 {
			continue
		}

		// Consolidate chain into firstFrameIdx
		status := "verified"
		for _, member := range chain {
			idx := frames[member.StepID]
			f := sequence.Frames[idx]
			if f.Status == "unknown" {
				status = "unknown"
			} else if f.Status != "verified" && status == "verified" {
				status = "partial"
			}
		}

		frame := sequence.Frames[firstFrameIdx]
		frame.Role = "decision"
		categoryLabel := purposeLabel(category)
		symbol := firstStep.Anchor.EnclosingSymbolPath
		if symbol != "" {
			frame.Title = symbol + " " + categoryLabel
		} else {
			frame.Title = categoryLabel
		}
		frame.Text = fmt.Sprintf("동일한 목적(%s)을 가진 연속 검증 단계 묶음", categoryLabel)
		frame.Status = status
		frame.StepRefs = make([]string, len(chain))
		for cIdx, member := range chain {
			frame.StepRefs[cIdx] = member.StepID
			mFrameIdx := frames[member.StepID]
			grouped[sequence.Frames[mFrameIdx].FrameID] = true
			if mFrameIdx != firstFrameIdx {
				removed[mFrameIdx] = true
			}
		}
		frame.PrimaryStepRef = chain[0].StepID
		frame.CollapsedDetail = &CollapsedDetail{
			Count:  len(chain) - 1,
			Reason: frame.Text,
		}
		frame.FrameMatchKey = NormalizeFrameMatchKey(frame.Role, symbol, frame.TechnicalAnchor, frame.Title)
		sequence.Frames[firstFrameIdx] = frame
	}

	result := make([]FlowSequenceFrame, 0, len(sequence.Frames)-len(removed))
	for i, frame := range sequence.Frames {
		if !removed[i] {
			frame.Ordinal = len(result) + 1
			result = append(result, frame)
		}
	}
	sequence.Frames = result

	limitations := make([]FlowSummaryLimitation, 0, len(sequence.SummaryLimitations))
	for _, limitation := range sequence.SummaryLimitations {
		if limitation.Code == "grouping_evidence_missing" {
			refs := make([]string, 0, len(limitation.FrameRefs))
			for _, ref := range limitation.FrameRefs {
				if !grouped[ref] {
					refs = append(refs, ref)
				}
			}
			limitation.FrameRefs = refs
		}
		if len(limitation.FrameRefs) > 0 {
			limitations = append(limitations, limitation)
		}
	}
	sequence.SummaryLimitations = limitations
}
