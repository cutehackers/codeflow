package contractharness

import (
	"encoding/json"
	"fmt"
)

// ValidateSemanticDeltaIR validates raw JSON against semantic-delta-ir.schema.json
// and enforces semantic delta invariants (SID-C2, Raw §10.12, VS-05).
func ValidateSemanticDeltaIR(data []byte) error {
	schemaID := BaseURL + "semantic-delta-ir.schema.json"
	if err := Validate(schemaID, data); err != nil {
		return fmt.Errorf("semantic-delta-ir schema violation: %w", err)
	}

	var delta struct {
		ComparisonID            string `json:"comparisonId"`
		BaselineComputedBasisID string `json:"baselineComputedBasisId"`
		CurrentComputedBasisID  string `json:"currentComputedBasisId"`
		FromGeneration          string `json:"fromGeneration"`
		ToGeneration            string `json:"toGeneration"`
		Changes                 []struct {
			DeltaID          string   `json:"deltaId"`
			Kind             string   `json:"kind"`
			TargetStepID     string   `json:"targetStepId"`
			Summary          string   `json:"summary"`
			EvidenceRefs     []string `json:"evidenceRefs"`
			EpistemicStatus  string   `json:"epistemicStatus"`
			ValidationStatus string   `json:"validationStatus"`
		} `json:"changes"`
	}

	if err := json.Unmarshal(data, &delta); err != nil {
		return fmt.Errorf("parse semantic-delta-ir JSON: %w", err)
	}

	for _, ch := range delta.Changes {
		// Invariant: evidence_updated requires non-empty evidenceRefs
		if ch.Kind == "evidence_updated" && len(ch.EvidenceRefs) == 0 {
			return fmt.Errorf("change %q has kind 'evidence_updated' but empty evidenceRefs", ch.DeltaID)
		}
		// Invariant: verified validationStatus requires observed or inferred epistemicStatus
		if ch.ValidationStatus == "verified" && (ch.EpistemicStatus == "unknown" || ch.EpistemicStatus == "unobserved") {
			return fmt.Errorf("change %q has validationStatus 'verified' but epistemicStatus %q", ch.DeltaID, ch.EpistemicStatus)
		}
	}

	return nil
}

// ValidateRequirementAlignment validates raw JSON against requirement-alignment.schema.json
// and enforces evidence grounding invariants (SID-C2, Raw D15, §10.13, VS05-A5, A6).
func ValidateRequirementAlignment(data []byte) error {
	schemaID := BaseURL + "requirement-alignment.schema.json"
	if err := Validate(schemaID, data); err != nil {
		return fmt.Errorf("requirement-alignment schema violation: %w", err)
	}

	var alignment struct {
		CriterionID     string   `json:"criterionId"`
		Status          string   `json:"status"`
		CoveredStepRefs []string `json:"coveredStepRefs"`
		EvidenceRefs    []string `json:"evidenceRefs"`
		MissingEvidence []string `json:"missingEvidence"`
	}

	if err := json.Unmarshal(data, &alignment); err != nil {
		return fmt.Errorf("parse requirement-alignment JSON: %w", err)
	}

	// Invariant (Raw D15, VS05-A6): 'confirmed' requires non-empty evidenceRefs and coveredStepRefs, and no missing evidence
	if alignment.Status == "confirmed" {
		if len(alignment.EvidenceRefs) == 0 {
			return fmt.Errorf("criterion %q has status 'confirmed' but 0 evidenceRefs (Evidence required)", alignment.CriterionID)
		}
		if len(alignment.CoveredStepRefs) == 0 {
			return fmt.Errorf("criterion %q has status 'confirmed' but 0 coveredStepRefs (Critical step required)", alignment.CriterionID)
		}
		if len(alignment.MissingEvidence) > 0 {
			return fmt.Errorf("criterion %q has status 'confirmed' but non-empty missingEvidence (%d items)", alignment.CriterionID, len(alignment.MissingEvidence))
		}
	}

	return nil
}
