package semantic

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// FlowCurationReview records the facts used by the canonical curator. It is an
// internal review result, not a publication payload or a source verification.
type FlowCurationReview struct {
	Sequence   *FlowSequence
	Requested  string
	ScopeBasis string
	Candidates []GatewayCandidateDecision
}

// GatewayCandidateDecision distinguishes retaining an execution fact from
// proving that it is independently essential to a particular request.
type GatewayCandidateDecision struct {
	StepRef            string
	FrameID            string
	Rule               string
	Consequence        string
	RequestRelation    string
	RequestSpecific    bool
	GroupingReason     string
	SupportingStepRefs []string
	SupportingEdges    []SemanticEdge
	EvidenceRefs       []string
	SummaryLimited     bool
}

// ReviewFlowSequence uses the same builder as every production consumer.
// No caller may use this review to change source facts or verification status.
func ReviewFlowSequence(mapIR *SemanticMapIR) *FlowCurationReview {
	if mapIR == nil {
		return nil
	}
	review := &FlowCurationReview{Requested: mapIR.Summary.Requested, ScopeBasis: "existing_requested_flow"}
	if review.Requested == "" {
		review.ScopeBasis = "observed_trace_only"
	}
	review.Sequence = buildFlowSequence(mapIR, review)
	return review
}

func gatewayCandidateDecision(step SemanticStep, role string, intermediate bool) GatewayCandidateDecision {
	rule := role
	if intermediate {
		rule = "unclassified_process"
	} else if role == "process" && step.InvocationID != "" && (step.Kind == "return" || step.Kind == "throw") {
		rule = "local_completion"
	}
	consequences := map[string]string{
		"entry":                "요청 흐름이 시작되는 위치를 보존한다.",
		"boundary":             "확인하지 못한 구현 연결을 드러낸다.",
		"effect":               "상태 밖으로 전달되는 효과 또는 실행 인계를 보존한다.",
		"decision":             "진행 여부 또는 조건별 경로를 구분하는 판단을 보존한다.",
		"process":              "분석에서 확인한 상태 변경을 보존한다.",
		"local_completion":     "현재 호출의 완료를 보존하며 전체 요청 흐름의 종료로 추정하지 않는다.",
		"result":               "분석에서 확인한 결과를 보존한다.",
		"unclassified_process": "처리 목적을 입증할 근거가 부족한 실행 단계를 삭제하지 않는다.",
	}
	return GatewayCandidateDecision{StepRef: step.StepID, Rule: rule, Consequence: consequences[rule], EvidenceRefs: append([]string(nil), step.EvidenceRefs...)}
}

func completeCurationReview(review *FlowCurationReview, sequence *FlowSequence, connections flowGroupingConnections, mapIR *SemanticMapIR) {
	frames := make(map[string]FlowSequenceFrame)
	for _, frame := range sequence.Frames {
		for _, id := range frame.StepRefs {
			frames[id] = frame
		}
	}
	limited := make(map[string]bool)
	for _, limitation := range sequence.SummaryLimitations {
		for _, id := range limitation.FrameRefs {
			limited[id] = true
		}
	}

	stepByID := make(map[string]SemanticStep)
	if mapIR != nil {
		for _, s := range mapIR.Steps {
			stepByID[s.StepID] = s
		}
	}
	alignmentByStep := make(map[string]RequirementAlignment)
	if mapIR != nil {
		for _, req := range mapIR.RequirementAlignment {
			if req.Status == "confirmed" || req.Status == "partial" {
				for _, stepRef := range req.CoveredStepRefs {
					alignmentByStep[stepRef] = req
				}
			}
		}
	}
	obligationByStep := make(map[string]CriticalObligation)
	if mapIR != nil {
		for _, obl := range mapIR.Quality.CriticalObligations {
			if obl.Required && obl.TargetRef != "" {
				obligationByStep[obl.TargetRef] = obl
			}
		}
	}

	for i := range review.Candidates {
		candidate := &review.Candidates[i]
		frame := frames[candidate.StepRef]
		candidate.FrameID = frame.FrameID
		candidate.SupportingStepRefs = append([]string(nil), frame.StepRefs...)
		candidate.SummaryLimited = limited[frame.FrameID]

		step := stepByID[candidate.StepRef]
		if req, ok := alignmentByStep[candidate.StepRef]; ok {
			candidate.RequestSpecific = true
			candidate.RequestRelation = fmt.Sprintf("요청 기준(%s: %s)을 만족하는 핵심 판단·처리다. 생략 시 요청 검증이 성립하지 않는다.", req.CriterionID, req.Description)
		} else if obl, ok := obligationByStep[candidate.StepRef]; ok {
			candidate.RequestSpecific = true
			candidate.RequestRelation = fmt.Sprintf("요청의 필수 책무(%s: %s)에 해당하는 핵심 지점이다. 생략 시 실행 결과 파악이 불가능하다.", obl.ObligationID, obl.Kind)
		} else {
			hasTargetRule := false
			for _, r := range step.Rules {
				if r == "request_target" || r == "essential" || strings.HasPrefix(r, "essential:") {
					hasTargetRule = true
					break
				}
			}
			if hasTargetRule {
				candidate.RequestSpecific = true
				candidate.RequestRelation = "사용자 요청의 핵심 목적 대상과 일치하는 필수 판단이다. 생략 시 원인 이해가 불가능하다."
			} else {
				candidate.RequestSpecific = false
				candidate.RequestRelation = "기존 요청 범위에 포함된 실행 사실이다. 특정 요청 문구에 필수적이라는 별도 근거는 확인하지 못했다."
				if review.ScopeBasis == "observed_trace_only" {
					candidate.RequestRelation = "요청 문맥 없이 제공된 실행 사실이다. 요청별 중요도를 판정하지 않는다."
				}
			}
		}

		candidate.GroupingReason = "같은 처리 목적을 입증할 근거 없이 별도 단계를 병합하지 않는다."
		if len(frame.StepRefs) > 1 && frame.CollapsedDetail != nil {
			candidate.GroupingReason = frame.CollapsedDetail.Reason
		} else if candidate.Rule == "decision" {
			hasDifferentAdjacent := false
			cat := classifyDecisionPurpose(step)
			if cat == decisionPurposeMixed {
				hasDifferentAdjacent = true
			}
			for _, edge := range connections.outgoing[candidate.StepRef] {
				if edge.Kind == "control_flow" {
					if nextStep, ok := stepByID[edge.ToStepID]; ok {
						nextCat := classifyDecisionPurpose(nextStep)
						if (nextStep.Kind == "guard" || nextStep.Kind == "branch" || nextStep.Kind == "decision") && nextCat != cat {
							hasDifferentAdjacent = true
							break
						}
					}
				}
			}
			if !hasDifferentAdjacent {
				for _, edge := range connections.incoming[candidate.StepRef] {
					if edge.Kind == "control_flow" {
						if prevStep, ok := stepByID[edge.FromStepID]; ok {
							prevCat := classifyDecisionPurpose(prevStep)
							if (prevStep.Kind == "guard" || prevStep.Kind == "branch" || prevStep.Kind == "decision") && prevCat != cat {
								hasDifferentAdjacent = true
								break
							}
						}
					}
				}
			}
			if hasDifferentAdjacent {
				candidate.GroupingReason = "서로 다른 핵심 판단은 인접해 있어도 독립 관문으로 분리한다."
			}
		} else if candidate.Rule == "entry" || candidate.Rule == "boundary" || candidate.Rule == "effect" || candidate.Rule == "result" {
			candidate.GroupingReason = candidate.Consequence
		}
		// Stable review order must not depend on edge traversal order. Copy
		// conditions because callers can inspect and mutate this review object.
		edges := make(map[string]SemanticEdge)
		for _, group := range [][]SemanticEdge{connections.incoming[candidate.StepRef], connections.outgoing[candidate.StepRef]} {
			for _, edge := range group {
				key, _ := json.Marshal(edge) // This struct contains only JSON-supported values.
				edge.Conditions = append([]SemanticBranchCondition(nil), edge.Conditions...)
				edges[string(key)] = edge
			}
		}
		keys := make([]string, 0, len(edges))
		for key := range edges {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			candidate.SupportingEdges = append(candidate.SupportingEdges, edges[key])
		}
	}
}
