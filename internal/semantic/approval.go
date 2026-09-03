package semantic

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"codeflow/internal/secret"
)

// ModelProposal mirrors schemas/model-proposal.schema.json (VS-08).
type ModelProposal struct {
	SchemaID         string   `json:"schemaId"`
	SchemaVersion    int      `json:"schemaVersion"`
	ProposalID       string   `json:"proposalId"`
	ComputedBasisID  string   `json:"computedBasisId"`
	GenerationID     string   `json:"generationId"`
	TargetSymbolPath string   `json:"targetSymbolPath"`
	ProposedTitle    string   `json:"proposedTitle"`
	ProposedCategory string   `json:"proposedCategory"` // entry | business_rule | orchestration | mutation | infrastructure | exit
	EpistemicStatus  string   `json:"epistemicStatus"`  // candidate | proposed | approved | rejected | stale
	Confidence       float64  `json:"confidence,omitempty"`
	Rationale        string   `json:"rationale,omitempty"`
	EvidenceRefs     []string `json:"evidenceRefs"`
}

// SemanticApproval mirrors schemas/semantic-approval.schema.json (VS-08).
type SemanticApproval struct {
	SchemaID        string          `json:"schemaId"`
	SchemaVersion   int             `json:"schemaVersion"`
	ApprovalID      string          `json:"approvalId"`
	ProposalID      string          `json:"proposalId"`
	ComputedBasisID string          `json:"computedBasisId"`
	GenerationID    string          `json:"generationId"`
	Decision        string          `json:"decision"` // approved | rejected | modified
	ModifiedValues  *ModifiedValues `json:"modifiedValues,omitempty"`
	Approver        string          `json:"approver"`
	ApprovedAt      string          `json:"approvedAt"`
	Freshness       string          `json:"freshness"` // current | stale
	EvidencePackID  string          `json:"evidencePackId,omitempty"`
}

type ModifiedValues struct {
	Title    string `json:"title,omitempty"`
	Category string `json:"category,omitempty"`
}

// EvidencePack mirrors schemas/evidence-pack.schema.json (VS-08).
type EvidencePack struct {
	SchemaID         string         `json:"schemaId"`
	SchemaVersion    int            `json:"schemaVersion"`
	EvidencePackID   string         `json:"evidencePackId"`
	TargetSymbolPath string         `json:"targetSymbolPath"`
	ComputedBasisID  string         `json:"computedBasisId"`
	GenerationID     string         `json:"generationId"`
	Items            []EvidenceItem `json:"items"`
	RedactionStatus  string         `json:"redactionStatus"` // unredacted | redacted | clean
}

type EvidenceItem struct {
	EvidenceID string `json:"evidenceId"`
	Kind       string `json:"kind"` // ast_anchor | call_edge | runtime_span | test_assertion | doc_reference
	Source     string `json:"source"`
	Content    string `json:"content"`
	Verified   bool   `json:"verified"`
}

type ApprovalRequest struct {
	ProposalID     string          `json:"proposalId"`
	Decision       string          `json:"decision"` // approved | rejected | modified
	ModifiedValues *ModifiedValues `json:"modifiedValues,omitempty"`
	Approver       string          `json:"approver"`
}

// SubmitSemanticApproval records human verification of a model proposal with evidence grounding (VS08-A1..A7).
func SubmitSemanticApproval(req ApprovalRequest, proposal *ModelProposal, pack *EvidencePack) (*SemanticApproval, error) {
	if strings.TrimSpace(req.ProposalID) == "" || strings.TrimSpace(req.Approver) == "" {
		return nil, errors.New("missing_precondition: proposalId and approver are required")
	}

	if req.Decision == "" {
		req.Decision = "approved"
	}
	if req.Decision != "approved" && req.Decision != "rejected" && req.Decision != "modified" {
		return nil, fmt.Errorf("invalid decision %q: must be approved, rejected, or modified", req.Decision)
	}

	if req.Decision == "approved" && pack == nil {
		return nil, errors.New("missing_precondition: approved decision requires an attached evidence pack")
	}

	basisID := "basis-active"
	genID := "gen-active"
	if proposal != nil {
		if proposal.ComputedBasisID != "" {
			basisID = proposal.ComputedBasisID
		}
		if proposal.GenerationID != "" {
			genID = proposal.GenerationID
		}
	}

	packID := ""
	if pack != nil {
		packID = pack.EvidencePackID
	}

	approvalID := fmt.Sprintf("appr-%s-%d", req.ProposalID, time.Now().UnixNano())

	return &SemanticApproval{
		SchemaID:        "https://codeflow.local/schemas/semantic-approval.schema.json",
		SchemaVersion:   1,
		ApprovalID:      approvalID,
		ProposalID:      req.ProposalID,
		ComputedBasisID: basisID,
		GenerationID:    genID,
		Decision:        req.Decision,
		ModifiedValues:  req.ModifiedValues,
		Approver:        req.Approver,
		ApprovedAt:      time.Now().UTC().Format(time.RFC3339),
		Freshness:       "current",
		EvidencePackID:  packID,
	}, nil
}

// CheckApprovalFreshness checks if code modifications have made the approval stale (VS08-A4).
func CheckApprovalFreshness(approval *SemanticApproval, currentBasisID, currentGenID string) *SemanticApproval {
	if approval == nil {
		return nil
	}
	cp := *approval
	if (currentBasisID != "" && currentBasisID != approval.ComputedBasisID) || (currentGenID != "" && currentGenID != approval.GenerationID) {
		cp.Freshness = "stale"
	}
	return &cp
}

// BuildEvidencePack builds and sanitizes evidence with secret redaction (VS08-A5).
func BuildEvidencePack(symbolPath, basisID, genID string, items []EvidenceItem) (*EvidencePack, error) {
	sanitizedItems := make([]EvidenceItem, 0, len(items))
	redactedCount := 0

	for _, item := range items {
		res := secret.Redact(item.Content)
		redactedCount += res.Count
		sanitizedItems = append(sanitizedItems, EvidenceItem{
			EvidenceID: item.EvidenceID,
			Kind:       item.Kind,
			Source:     item.Source,
			Content:    res.Text,
			Verified:   item.Verified,
		})
	}

	redactionStatus := "clean"
	if redactedCount > 0 {
		redactionStatus = "redacted"
	}

	packID := fmt.Sprintf("pack-%s-%d", strings.ReplaceAll(symbolPath, ".", "-"), time.Now().UnixNano())

	return &EvidencePack{
		SchemaID:         "https://codeflow.local/schemas/evidence-pack.schema.json",
		SchemaVersion:    1,
		EvidencePackID:   packID,
		TargetSymbolPath: symbolPath,
		ComputedBasisID:  basisID,
		GenerationID:     genID,
		Items:            sanitizedItems,
		RedactionStatus:  redactionStatus,
	}, nil
}
