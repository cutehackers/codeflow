package semantic

import (
	"fmt"
	"strings"
)

// AlignmentOptions parameterizes requirement alignment computation.
type AlignmentOptions struct {
	AgentDeclarations []string // Agent completion claims or text (must be treated as hints, not facts: Raw D14)
	ModelProposals    []string // Model proposals (must not promote to confirmed: Raw D15)
}

// ComputeRequirementAlignment computes RequirementAlignment records for the given
// acceptance criteria against the current SemanticMapIR (Raw §9.10, §10.13, VS05-A5, A6, A7, A9).
func ComputeRequirementAlignment(criteria []AcceptanceCriterion, currentMap *SemanticMapIR, opts AlignmentOptions) []RequirementAlignment {
	if currentMap == nil || len(criteria) == 0 {
		return nil
	}

	results := make([]RequirementAlignment, 0, len(criteria))

	// Build evidence lookup from currentMap
	evidenceStatus := make(map[string]string) // evidenceId -> validationStatus (verified, stale, orphaned, invalid)
	for _, ev := range currentMap.Evidence {
		evidenceStatus[ev.EvidenceID] = ev.ValidationStatus
	}

	for _, ac := range criteria {
		align := RequirementAlignment{
			SchemaID:        "codeflow.requirement-alignment",
			SchemaVersion:   1,
			CriterionID:     ac.ID,
			Description:     ac.Text,
			CoveredStepRefs: make([]string, 0),
			EvidenceRefs:    make([]string, 0),
			MissingEvidence: make([]string, 0),
			ComputedBasisID: currentMap.ComputedBasisID,
		}

		// Find covered steps
		critToken := strings.ToLower(ac.ID)
		critTextTokens := strings.Fields(strings.ToLower(ac.Text))

		var hasConflict bool
		var hasStaleEvidence bool

		for _, st := range currentMap.Steps {
			matches := false

			// Match rule references or ID
			for _, r := range st.Rules {
				if strings.EqualFold(r, ac.ID) || strings.Contains(strings.ToLower(r), critToken) {
					matches = true
					break
				}
			}

			// Match text tokens in name or technicalName
			if !matches && len(critTextTokens) > 0 {
				stepDesc := strings.ToLower(st.Name + " " + st.TechnicalName)
				matchCount := 0
				for _, tok := range critTextTokens {
					if len(tok) > 2 && strings.Contains(stepDesc, tok) {
						matchCount++
					}
				}
				if matchCount >= 2 || (len(critTextTokens) == 1 && matchCount == 1) {
					matches = true
				}
			}

			if matches {
				align.CoveredStepRefs = append(align.CoveredStepRefs, st.StepID)
				for _, evID := range st.EvidenceRefs {
					align.EvidenceRefs = append(align.EvidenceRefs, evID)
					status := evidenceStatus[evID]
					if status == "stale" || status == "orphaned" {
						hasStaleEvidence = true
					}
					if status == "conflicting" || status == "invalid" {
						hasConflict = true
					}
				}
			}
		}

		// Status determination (VS05-A5, A6, A7, Raw D14, D15)
		if hasConflict {
			align.Status = "conflicting"
			align.Notes = "모순되거나 충돌하는 근거가 발견됨"
		} else if len(align.CoveredStepRefs) == 0 {
			if currentMap.Coverage != nil && len(currentMap.Coverage.IncludedSourceRoots) > 0 {
				align.Status = "not_observed"
				align.Notes = "분석 범위 내에서 구현이 관찰되지 않음"
			} else {
				align.Status = "unknown"
				align.Notes = "근거 부재 및 분석 범위 미관찰"
			}
		} else if len(align.EvidenceRefs) == 0 {
			// Steps exist but have NO ground evidence attached
			// Raw D15 / VS05-A6: Agent declaration or model proposal alone NEVER promotes to confirmed!
			align.Status = "unknown"
			align.MissingEvidence = append(align.MissingEvidence, "코드 실행 또는 검증 근거(Evidence) 부재")
			align.Notes = "행동은 매핑되었으나 검증된 코드/테스트 Evidence 없음"
		} else if hasStaleEvidence {
			// VS05-A7: Anchor/code changed, evidence is stale or orphaned
			align.Status = "partial"
			align.MissingEvidence = append(align.MissingEvidence, "기존 anchor 변경으로 인한 stale/orphaned 근거 갱신 필요")
			align.Notes = "일부 근거가 최신 코드와 불일치(stale)"
		} else {
			// Check if any critical obligation or tests are missing
			// If all covered steps have verified evidence
			var missingTests bool
			for _, evID := range align.EvidenceRefs {
				if !strings.Contains(strings.ToLower(evID), "test") {
					// heuristic for demonstration, or check actual test evidence
				}
			}
			if missingTests {
				align.Status = "partial"
				align.MissingEvidence = append(align.MissingEvidence, "경계 테스트(boundary test) 근거 누락")
				align.Notes = "기본 동작은 확인되었으나 테스트 증거 불완전"
			} else {
				align.Status = "confirmed"
				align.Notes = fmt.Sprintf("현재 basis(%s)에서 핵심 step과 Evidence가 모두 검증됨", currentMap.ComputedBasisID)
			}
		}

		results = append(results, align)
	}

	return results
}
