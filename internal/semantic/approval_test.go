package semantic

import (
	"testing"
)

func TestSubmitSemanticApproval_MissingPreconditions(t *testing.T) {
	_, err := SubmitSemanticApproval(ApprovalRequest{}, nil, nil)
	if err == nil {
		t.Fatal("expected missing_precondition error when request is empty")
	}
	if err.Error() != "missing_precondition: proposalId and approver are required" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestSubmitSemanticApproval_ApprovedWithEvidencePack(t *testing.T) {
	proposal := &ModelProposal{
		ProposalID:       "prop-1",
		TargetSymbolPath: "PaymentService.process",
		ProposedTitle:    "PG 결제 승인 요청",
		ProposedCategory: "business_rule",
		EpistemicStatus:  "proposed",
		ComputedBasisID:  "basis-1",
		GenerationID:     "gen-1",
		EvidenceRefs:     []string{"ev-ast-1"},
	}

	pack := &EvidencePack{
		EvidencePackID:   "pack-1",
		TargetSymbolPath: "PaymentService.process",
		ComputedBasisID:  "basis-1",
		GenerationID:     "gen-1",
		Items: []EvidenceItem{
			{
				EvidenceID: "ev-ast-1",
				Kind:       "ast_anchor",
				Source:     "src/payment.ts:25",
				Content:    "stripe.charge(token)",
				Verified:   true,
			},
		},
		RedactionStatus: "clean",
	}

	req := ApprovalRequest{
		ProposalID: "prop-1",
		Decision:   "approved",
		Approver:   "lead@company.com",
	}

	approval, err := SubmitSemanticApproval(req, proposal, pack)
	if err != nil {
		t.Fatalf("SubmitSemanticApproval failed: %v", err)
	}

	// VS08-A2, A3
	if approval.Decision != "approved" {
		t.Errorf("expected decision approved, got %s", approval.Decision)
	}
	if approval.Approver != "lead@company.com" {
		t.Errorf("expected approver lead@company.com, got %s", approval.Approver)
	}
	if approval.Freshness != "current" {
		t.Errorf("expected freshness current, got %s", approval.Freshness)
	}
	if approval.EvidencePackID != "pack-1" {
		t.Errorf("expected evidencePackId pack-1, got %s", approval.EvidencePackID)
	}
}

func TestSubmitSemanticApproval_StaleDetection(t *testing.T) {
	approval := &SemanticApproval{
		ApprovalID:      "appr-1",
		ProposalID:      "prop-1",
		ComputedBasisID: "basis-old",
		GenerationID:    "gen-old",
		Decision:        "approved",
		Approver:        "lead@company.com",
		Freshness:       "current",
	}

	// VS08-A4: check freshness against new generation/basis
	stale := CheckApprovalFreshness(approval, "basis-new", "gen-new")
	if stale.Freshness != "stale" {
		t.Errorf("expected freshness to be stale, got %s", stale.Freshness)
	}
}

func TestBuildEvidencePack_SecretRedaction(t *testing.T) {
	rawContent := "export const apiKey = 'sk_live_1234567890abcdef';"
	pack, err := BuildEvidencePack("PaymentService.process", "basis-1", "gen-1", []EvidenceItem{
		{
			EvidenceID: "ev-secret",
			Kind:       "ast_anchor",
			Source:     "src/config.ts",
			Content:    rawContent,
			Verified:   true,
		},
	})
	if err != nil {
		t.Fatalf("BuildEvidencePack failed: %v", err)
	}

	// VS08-A5: secret must be redacted
	if pack.RedactionStatus != "redacted" && pack.RedactionStatus != "clean" {
		t.Errorf("expected redactionStatus redacted or clean, got %s", pack.RedactionStatus)
	}
	if pack.Items[0].Content == rawContent {
		t.Errorf("expected secret to be redacted, got original content: %s", pack.Items[0].Content)
	}
}
