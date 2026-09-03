package contractharness

import (
	"encoding/json"
	"fmt"
)

// ValidateDomainOverview validates raw JSON against domain-overview.schema.json
// and enforces domain overview invariants (VS-09, SID-C2, Raw §8.9, §10).
func ValidateDomainOverview(data []byte) error {
	schemaID := BaseURL + "domain-overview.schema.json"
	if err := Validate(schemaID, data); err != nil {
		return fmt.Errorf("domain-overview schema violation: %w", err)
	}

	var ov struct {
		RepositoryID string `json:"repositoryId"`
		Summary      struct {
			CoverageRatio float64 `json:"coverageRatio"`
		} `json:"summary"`
	}
	if err := json.Unmarshal(data, &ov); err != nil {
		return fmt.Errorf("parse domain-overview JSON: %w", err)
	}

	if ov.Summary.CoverageRatio < 0 || ov.Summary.CoverageRatio > 1.0 {
		return fmt.Errorf("domain-overview %q coverageRatio must be between 0.0 and 1.0, got %f", ov.RepositoryID, ov.Summary.CoverageRatio)
	}

	return nil
}

// ValidateRepresentativeFlowCatalog validates raw JSON against representative-flow-catalog.schema.json
// and enforces catalog invariants (VS-09, SID-C2, Raw §8.9, §10).
func ValidateRepresentativeFlowCatalog(data []byte) error {
	schemaID := BaseURL + "representative-flow-catalog.schema.json"
	if err := Validate(schemaID, data); err != nil {
		return fmt.Errorf("representative-flow-catalog schema violation: %w", err)
	}

	var cat struct {
		CatalogID string `json:"catalogId"`
		Flows     []struct {
			FlowID          string  `json:"flowId"`
			ComplexityScore float64 `json:"complexityScore"`
			GroundedMapID   string  `json:"groundedMapId"`
		} `json:"flows"`
	}
	if err := json.Unmarshal(data, &cat); err != nil {
		return fmt.Errorf("parse representative-flow-catalog JSON: %w", err)
	}

	for _, f := range cat.Flows {
		if f.ComplexityScore < 0 {
			return fmt.Errorf("flow %q in catalog %q has negative complexity score %f", f.FlowID, cat.CatalogID, f.ComplexityScore)
		}
		if f.GroundedMapID == "" {
			return fmt.Errorf("flow %q in catalog %q missing groundedMapId", f.FlowID, cat.CatalogID)
		}
	}

	return nil
}
