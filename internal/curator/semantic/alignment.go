package semantic

import (
	"sort"
	"strings"
)

// AlignmentOptions parameterizes requirement alignment computation. Agent and
// model text is deliberately retained as context only. It never contributes
// implementation evidence.
type AlignmentOptions struct {
	AgentDeclarations []string
	ModelProposals    []string
}

// ComputeRequirementAlignment computes authority-aware requirement alignment.
// Matching a rule to a step is only candidate coverage. Promotion additionally
// requires an existing, basis-bound, verified Evidence item from an allowed
// source authority, and an explicit current proof.
func ComputeRequirementAlignment(criteria []AcceptanceCriterion, currentMap *SemanticMapIR, opts AlignmentOptions) []RequirementAlignment {
	_ = opts // Agent/model text is context only in VS-04.
	if currentMap == nil || len(criteria) == 0 {
		return nil
	}

	evidenceByID := make(map[string]SemanticEvidence, len(currentMap.Evidence))
	for _, ev := range currentMap.Evidence {
		if ev.EvidenceID != "" {
			evidenceByID[ev.EvidenceID] = ev
		}
	}

	results := make([]RequirementAlignment, 0, len(criteria))
	for _, criterion := range criteria {
		alignment := RequirementAlignment{
			SchemaID:        RequirementAlignmentSchemaID,
			SchemaVersion:   SemanticSchemaVersion,
			CriterionID:     criterion.ID,
			Description:     criterion.Text,
			CoveredStepRefs: []string{},
			EvidenceRefs:    []string{},
			MissingEvidence: []string{},
			ComputedBasisID: currentMap.ComputedBasisID,
			Authority:       "candidate",
		}

		for _, step := range currentMap.Steps {
			if !criterionMatchesStep(criterion, step) {
				continue
			}
			if step.StepID != "" {
				alignment.CoveredStepRefs = append(alignment.CoveredStepRefs, step.StepID)
			}
			for _, evidenceID := range step.EvidenceRefs {
				if evidenceID == "" || contains(alignment.EvidenceRefs, evidenceID) {
					continue
				}
				alignment.EvidenceRefs = append(alignment.EvidenceRefs, evidenceID)
			}
		}
		sort.Strings(alignment.CoveredStepRefs)
		sort.Strings(alignment.EvidenceRefs)

		if len(alignment.CoveredStepRefs) == 0 {
			if currentMap.Coverage != nil && len(currentMap.Coverage.IncludedSourceRoots) > 0 {
				alignment.Status = "not_observed"
				alignment.Notes = "분석 범위 안에서 해당 기준의 구조적 구현이 관찰되지 않음"
			} else {
				alignment.Status = "unknown"
				alignment.Notes = "분석 범위와 구현 근거가 없음"
			}
			alignment.MissingEvidence = append(alignment.MissingEvidence, "implementation_step")
			alignment.MissingRuntime = append(alignment.MissingRuntime, "current_runtime_proof")
			results = append(results, alignment)
			continue
		}

		if len(alignment.EvidenceRefs) == 0 {
			alignment.Status = "unknown"
			alignment.MissingEvidence = append(alignment.MissingEvidence, "verified_evidence")
			alignment.MissingTests = append(alignment.MissingTests, "test_evidence")
			alignment.MissingContracts = append(alignment.MissingContracts, "contract_evidence")
			alignment.MissingRuntime = append(alignment.MissingRuntime, "current_runtime_proof")
			alignment.Notes = "구조적 후보는 있으나 Evidence 참조가 없음"
			results = append(results, alignment)
			continue
		}

		invalid, stale, missingKinds := validateAlignmentEvidence(criterion, alignment.EvidenceRefs, evidenceByID, currentMap.ComputedBasisID)
		if invalid {
			alignment.Status = "conflicting"
			alignment.Notes = "근거의 authority 또는 basis identity가 충돌함"
		} else if stale {
			alignment.Status = "partial"
			alignment.MissingEvidence = append(alignment.MissingEvidence, "fresh_basis_evidence")
			alignment.Notes = "일부 Evidence가 stale, orphaned 또는 현재 basis와 불일치함"
		} else if len(missingKinds) > 0 {
			alignment.Status = "partial"
			alignment.MissingEvidence = append(alignment.MissingEvidence, missingKinds...)
			alignment.Notes = "필수 Evidence 종류가 아직 모두 관찰되지 않음"
		} else {
			alignment.Status = "partial"
			alignment.Reason = "awaiting_current_proof"
			alignment.MissingRuntime = append(alignment.MissingRuntime, "current_runtime_proof")
			alignment.Notes = "snapshot basis Evidence는 검증되었으나 VS-03 current proof 대기 중"
		}
		results = append(results, alignment)
	}
	return results
}

func criterionMatchesStep(criterion AcceptanceCriterion, step SemanticStep) bool {
	for _, rule := range step.Rules {
		if strings.EqualFold(strings.TrimSpace(rule), strings.TrimSpace(criterion.ID)) {
			return true
		}
	}
	// Text matching is a candidate hint only. Evidence validation below still
	// has to succeed before any non-unknown status can be returned.
	criterionTokens := significantTokens(criterion.Text)
	if len(criterionTokens) == 0 {
		return false
	}
	description := strings.ToLower(step.Name + " " + step.TechnicalName)
	matches := 0
	for _, token := range criterionTokens {
		if strings.Contains(description, token) {
			matches++
		}
	}
	return matches >= 2 || (len(criterionTokens) == 1 && matches == 1)
}

func significantTokens(text string) []string {
	var result []string
	for _, token := range strings.Fields(strings.ToLower(text)) {
		if len([]rune(token)) > 2 && !strings.HasPrefix(token, "ac-") {
			result = append(result, token)
		}
	}
	return result
}

func validateAlignmentEvidence(criterion AcceptanceCriterion, refs []string, evidenceByID map[string]SemanticEvidence, basis string) (invalid, stale bool, missingKinds []string) {
	seenKinds := make(map[string]bool)
	for _, ref := range refs {
		ev, ok := evidenceByID[ref]
		if !ok {
			stale = true
			continue
		}
		if !allowedEvidenceAuthority(ev.SourceAuthority) {
			invalid = true
		}
		if ev.ComputedBasisID != basis {
			stale = true
		}
		switch ev.ValidationStatus {
		case "verified":
			seenKinds[ev.Kind] = true
		case "conflicting", "invalid":
			invalid = true
		default:
			stale = true
		}
	}
	for _, required := range criterion.RequiredEvidenceKinds {
		if !seenKinds[required] {
			missingKinds = append(missingKinds, "evidence_kind:"+required)
		}
	}
	sort.Strings(missingKinds)
	return invalid, stale, missingKinds
}

func allowedEvidenceAuthority(authority string) bool {
	switch authority {
	case "code", "test", "contract", "runtime":
		return true
	default:
		return false
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
