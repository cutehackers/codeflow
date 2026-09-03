package semantic

import (
	"testing"
)

func TestComputeRequirementAlignment_Matrix(t *testing.T) {
	criteria := []AcceptanceCriterion{
		{ID: "AC-1", Text: "결제 실패 시 재시도 로직 수행"},
		{ID: "AC-2", Text: "재시도 한도 3회 초과 시 에러 반환"},
		{ID: "AC-3", Text: "알 수 없는 외부 결제 장애 보상 처리"},
	}

	currMap := &SemanticMapIR{
		ComputedBasisID: "wsnap-test-1",
		Coverage: &CoverageBoundary{
			IncludedSourceRoots: []string{"services/payment"},
		},
		Evidence: []SemanticEvidence{
			{
				EvidenceID:       "ev-retry-test",
				ValidationStatus: "verified",
			},
			{
				EvidenceID:       "ev-stale-test",
				ValidationStatus: "stale", // VS05-A7: stale evidence
			},
		},
		Steps: []SemanticStep{
			{
				StepID:        "step-1",
				Name:          "결제 재시도",
				TechnicalName: "Payment.retry",
				Rules:         []string{"AC-1"},
				EvidenceRefs:  []string{"ev-retry-test"},
			},
			{
				StepID:        "step-2",
				Name:          "한도 확인",
				TechnicalName: "Payment.checkLimit",
				Rules:         []string{"AC-2"},
				EvidenceRefs:  []string{"ev-stale-test"}, // Stale!
			},
			// AC-3 is not covered by any step!
		},
	}

	opts := AlignmentOptions{
		AgentDeclarations: []string{"Agent says: I have implemented AC-3 completely and all tests pass!"},
		ModelProposals:    []string{"Model: AC-3 looks complete with 99% confidence"},
	}

	alignments := ComputeRequirementAlignment(criteria, currMap, opts)
	if len(alignments) != 3 {
		t.Fatalf("expected 3 alignments, got %d", len(alignments))
	}

	byID := make(map[string]RequirementAlignment)
	for _, a := range alignments {
		byID[a.CriterionID] = a
	}

	// 1. AC-1: Confirmed with verified evidence (VS05-A5)
	ac1 := byID["AC-1"]
	if ac1.Status != "confirmed" {
		t.Errorf("AC-1 status = %s, want confirmed", ac1.Status)
	}
	if len(ac1.CoveredStepRefs) == 0 || len(ac1.EvidenceRefs) == 0 {
		t.Errorf("AC-1 missing covered steps or evidence: %+v", ac1)
	}

	// 2. AC-2: Stale evidence -> partial (VS05-A7)
	ac2 := byID["AC-2"]
	if ac2.Status != "partial" {
		t.Errorf("AC-2 status = %s, want partial for stale evidence", ac2.Status)
	}
	if len(ac2.MissingEvidence) == 0 {
		t.Errorf("AC-2 expected missingEvidence note about stale anchor, got none")
	}

	// 3. AC-3: Not observed, agent/model declaration alone MUST NOT promote to confirmed! (Raw D14, D15, VS05-A6)
	ac3 := byID["AC-3"]
	if ac3.Status == "confirmed" {
		t.Errorf("AC-3 status was incorrectly promoted to confirmed by agent text alone!")
	}
	if ac3.Status != "not_observed" && ac3.Status != "unknown" {
		t.Errorf("AC-3 status = %s, want not_observed or unknown", ac3.Status)
	}
}

func TestRequirementAlignment_IntentStatusSeparation(t *testing.T) {
	// VS05-A9: Task intent intentStatus is separate from RequirementAlignment status
	intent := &TaskIntent{
		IntentStatus: "user_confirmed", // User confirmed the request interpretation
		AcceptanceCriteria: []AcceptanceCriterion{
			{ID: "AC-1", Text: "결제 취소 처리"},
		},
	}

	// Empty currentMap: no implementation exists yet
	currMap := &SemanticMapIR{
		ComputedBasisID: "wsnap-empty",
		Steps:           []SemanticStep{},
	}

	alignments := ComputeRequirementAlignment(intent.AcceptanceCriteria, currMap, AlignmentOptions{})
	if len(alignments) != 1 {
		t.Fatalf("expected 1 alignment, got %d", len(alignments))
	}

	// Even though user_confirmed is true, requirement status MUST NOT be confirmed
	if alignments[0].Status == "confirmed" {
		t.Errorf("requirement status confused with intentStatus: got confirmed, want unknown/not_observed")
	}
	if alignments[0].Status != "unknown" && alignments[0].Status != "not_observed" {
		t.Errorf("unexpected status: %s", alignments[0].Status)
	}
}
