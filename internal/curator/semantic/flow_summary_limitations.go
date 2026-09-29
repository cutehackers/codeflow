package semantic

import "strings"

// A selected entry proves the analysis scope, but does not by itself prove
// which of its child decisions and effects are essential to the request.
func markRequestCurationLimitations(sequence *FlowSequence, mapIR *SemanticMapIR) {
	if sequence == nil || mapIR == nil || strings.TrimSpace(mapIR.Summary.Requested) == "" || len(sequence.Frames) == 0 || len(requestSpecificStepRefs(mapIR)) != 0 {
		return
	}
	refs := make([]string, 0, len(sequence.Frames))
	for _, frame := range sequence.Frames {
		refs = append(refs, frame.FrameID)
	}
	sequence.SummaryLimitations = append(sequence.SummaryLimitations, FlowSummaryLimitation{
		Code: "request_relevance_unproven", Message: "요청에 필수적인 처리를 가릴 근거가 부족합니다. 확인된 실행 단계를 보여 주지만 핵심 관문 선정은 완료되지 않았습니다.", FrameRefs: refs,
	})
}

// markUngroupedProcessRuns reports a flat run when direct execution is proven
// but the request does not establish one shared processing purpose or a reason
// to retain each state change as its own gateway.
func markUngroupedProcessRuns(sequence *FlowSequence, mapIR *SemanticMapIR, connections flowGroupingConnections) {
	if sequence == nil || mapIR == nil || len(sequence.Frames) < 2 {
		return
	}
	stepByID := make(map[string]SemanticStep, len(mapIR.Steps))
	for _, step := range mapIR.Steps {
		stepByID[step.StepID] = step
	}
	requestSpecific := requestSpecificStepRefs(mapIR)

	limited := make(map[string]bool)
	for _, limitation := range sequence.SummaryLimitations {
		if limitation.Code != "grouping_evidence_missing" {
			continue
		}
		for _, ref := range limitation.FrameRefs {
			limited[ref] = true
		}
	}
	for index := 1; index < len(sequence.Frames); index++ {
		previous, current := sequence.Frames[index-1], sequence.Frames[index]
		if previous.Role != "process" || current.Role != "process" || len(previous.StepRefs) != 1 || len(current.StepRefs) != 1 {
			continue
		}
		from, fromExists := stepByID[previous.StepRefs[0]]
		to, toExists := stepByID[current.StepRefs[0]]
		if !fromExists || !toExists || from.Kind != "mutation" || to.Kind != "mutation" || requestSpecific[from.StepID] || requestSpecific[to.StepID] || from.InvocationID == "" || from.InvocationID != to.InvocationID || from.Anchor.RepoRelativePath != to.Anchor.RepoRelativePath || from.Anchor.EnclosingSymbolPath != to.Anchor.EnclosingSymbolPath {
			continue
		}
		outgoing, incoming := connections.outgoing[from.StepID], connections.incoming[to.StepID]
		if len(outgoing) != 1 || len(incoming) != 1 {
			continue
		}
		edge := outgoing[0]
		if edge.ToStepID != to.StepID || edge.Kind != "control_flow" || edge.ResolutionStatus != "resolved" || len(edge.Conditions) != 0 {
			continue
		}
		limited[previous.FrameID] = true
		limited[current.FrameID] = true
	}
	if len(limited) == 0 {
		return
	}
	refs := make([]string, 0, len(limited))
	for _, frame := range sequence.Frames {
		if limited[frame.FrameID] {
			refs = append(refs, frame.FrameID)
		}
	}
	for index := range sequence.SummaryLimitations {
		if sequence.SummaryLimitations[index].Code == "grouping_evidence_missing" {
			sequence.SummaryLimitations[index].FrameRefs = refs
			return
		}
	}
	sequence.SummaryLimitations = append(sequence.SummaryLimitations, FlowSummaryLimitation{
		Code:      "grouping_evidence_missing",
		Message:   "일부 처리를 관문으로 묶지 못했습니다. 실행 연결 또는 같은 처리 목적의 근거가 부족해 단계를 구분해 표시합니다.",
		FrameRefs: refs,
	})
}

func requestSpecificStepRefs(mapIR *SemanticMapIR) map[string]bool {
	requestSpecific := make(map[string]bool)
	if mapIR == nil {
		return requestSpecific
	}
	for _, step := range mapIR.Steps {
		for _, rule := range step.Rules {
			if rule == "request_target" || rule == "essential" || strings.HasPrefix(rule, "essential:") {
				requestSpecific[step.StepID] = true
			}
		}
	}
	for ref := range confirmedSpecificAlignmentByStep(mapIR) {
		requestSpecific[ref] = true
	}
	return requestSpecific
}

// A confirmed criterion can cover a complete flow without proving that every
// covered statement is independently necessary. Only a criterion with one
// covered step identifies that step specifically.
func confirmedSpecificAlignmentByStep(mapIR *SemanticMapIR) map[string]RequirementAlignment {
	result := make(map[string]RequirementAlignment)
	if mapIR == nil {
		return result
	}
	for _, alignment := range mapIR.RequirementAlignment {
		if alignment.Status != "confirmed" || len(alignment.CoveredStepRefs) != 1 || alignment.CoveredStepRefs[0] == "" {
			continue
		}
		ref := alignment.CoveredStepRefs[0]
		previous, exists := result[ref]
		if !exists || alignment.CriterionID+"\x00"+alignment.Description < previous.CriterionID+"\x00"+previous.Description {
			result[ref] = alignment
		}
	}
	return result
}
