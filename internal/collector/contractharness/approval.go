package contractharness

import (
	"encoding/json"
	"fmt"
)

// ValidateModelProposal validates raw JSON against model-proposal.schema.json
// and enforces model proposal invariants (VS-08, SID-C2, Raw §9.1..§9.3).
func ValidateModelProposal(data []byte) error {
	schemaID := BaseURL + "model-proposal.schema.json"
	if err := Validate(schemaID, data); err != nil {
		return fmt.Errorf("model-proposal schema violation: %w", err)
	}

	var prop struct {
		ProposalID      string   `json:"proposalId"`
		EpistemicStatus string   `json:"epistemicStatus"`
		EvidenceRefs    []string `json:"evidenceRefs"`
	}
	if err := json.Unmarshal(data, &prop); err != nil {
		return fmt.Errorf("parse model-proposal JSON: %w", err)
	}

	// Invariant (VS08-A1): Model proposal is never auto-promoted to approved without approval record
	if len(prop.EvidenceRefs) == 0 {
		return fmt.Errorf("proposal %q has 0 evidenceRefs", prop.ProposalID)
	}

	return nil
}

// ValidateSemanticApproval validates raw JSON against semantic-approval.schema.json
// and enforces approval grounding invariants (VS-08, SID-C2, Raw §9.4..§9.6).
func ValidateSemanticApproval(data []byte) error {
	schemaID := BaseURL + "semantic-approval.schema.json"
	if err := Validate(schemaID, data); err != nil {
		return fmt.Errorf("semantic-approval schema violation: %w", err)
	}

	var appr struct {
		ApprovalID     string `json:"approvalId"`
		Decision       string `json:"decision"`
		Approver       string `json:"approver"`
		EvidencePackID string `json:"evidencePackId"`
	}
	if err := json.Unmarshal(data, &appr); err != nil {
		return fmt.Errorf("parse semantic-approval JSON: %w", err)
	}

	// Invariant (VS08-A6, VS08-A7): approval decision requires non-empty approver and evidencePackId
	if appr.Decision == "approved" {
		if appr.Approver == "" {
			return fmt.Errorf("approval %q decision is approved but approver identity is missing", appr.ApprovalID)
		}
		if appr.EvidencePackID == "" {
			return fmt.Errorf("approval %q decision is approved but evidencePackId is missing", appr.ApprovalID)
		}
	}

	return nil
}

// ValidateEvidencePack validates raw JSON against evidence-pack.schema.json
// and enforces evidence grounding invariants (VS-08, SID-C2, Raw §9.7).
func ValidateEvidencePack(data []byte) error {
	schemaID := BaseURL + "evidence-pack.schema.json"
	if err := Validate(schemaID, data); err != nil {
		return fmt.Errorf("evidence-pack schema violation: %w", err)
	}

	var pack struct {
		EvidencePackID string `json:"evidencePackId"`
		Items          []struct {
			EvidenceID string `json:"evidenceId"`
			Kind       string `json:"kind"`
			Verified   bool   `json:"verified"`
		} `json:"items"`
		RedactionStatus string `json:"redactionStatus"`
	}
	if err := json.Unmarshal(data, &pack); err != nil {
		return fmt.Errorf("parse evidence-pack JSON: %w", err)
	}

	if len(pack.Items) == 0 {
		return fmt.Errorf("evidence-pack %q must contain at least 1 evidence item", pack.EvidencePackID)
	}

	return nil
}
