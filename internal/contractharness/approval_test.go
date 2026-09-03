package contractharness

import (
	"encoding/json"
	"testing"
)

func TestValidateSemanticApproval_Invariants(t *testing.T) {
	// Valid approval
	valid := map[string]any{
		"schemaId":        BaseURL + "semantic-approval.schema.json",
		"schemaVersion":   1,
		"approvalId":      "appr-1",
		"proposalId":      "prop-1",
		"computedBasisId": "basis-1",
		"generationId":    "gen-1",
		"decision":        "approved",
		"approver":        "lead@company.com",
		"approvedAt":      "2026-09-03T17:00:00Z",
		"freshness":       "current",
		"evidencePackId":  "pack-1",
	}

	data, _ := json.Marshal(valid)
	if err := ValidateSemanticApproval(data); err != nil {
		t.Fatalf("expected valid approval to pass: %v", err)
	}

	// Missing evidencePackId when decision is approved
	invalid := map[string]any{
		"schemaId":        BaseURL + "semantic-approval.schema.json",
		"schemaVersion":   1,
		"approvalId":      "appr-2",
		"proposalId":      "prop-1",
		"computedBasisId": "basis-1",
		"generationId":    "gen-1",
		"decision":        "approved",
		"approver":        "lead@company.com",
		"approvedAt":      "2026-09-03T17:00:00Z",
		"freshness":       "current",
	}
	invalidData, _ := json.Marshal(invalid)
	if err := ValidateSemanticApproval(invalidData); err == nil {
		t.Fatal("expected approval without evidencePackId to fail")
	}
}
