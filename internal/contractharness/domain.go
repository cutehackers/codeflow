package contractharness

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
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

// ValidateOnboardingQueryV2 validates the explicit identity selector used by
// the evidence-backed onboarding projector.  Unlike the legacy validator this
// contract never supplies defaults for repository, basis, generation, or
// snapshot identity.
func ValidateOnboardingQueryV2(data []byte) error {
	if err := Validate(BaseURL+"rflsc.onboarding-query.v2.schema.json", data); err != nil {
		return fmt.Errorf("rflsc onboarding-query v2 schema violation: %w", err)
	}
	var query struct {
		SchemaID                   string `json:"schemaId"`
		SchemaVersion              int    `json:"schemaVersion"`
		RepositoryID               string `json:"repositoryId"`
		ComputedBasisID            string `json:"computedBasisId"`
		GenerationID               string `json:"generationId"`
		ValidatedAgainstSnapshotID string `json:"validatedAgainstSnapshotId"`
		Freshness                  string `json:"freshness"`
		Level                      int    `json:"level"`
	}
	if err := json.Unmarshal(data, &query); err != nil {
		return fmt.Errorf("parse rflsc onboarding-query v2 JSON: %w", err)
	}
	for name, value := range map[string]string{
		"repositoryId": query.RepositoryID, "computedBasisId": query.ComputedBasisID,
		"generationId": query.GenerationID, "validatedAgainstSnapshotId": query.ValidatedAgainstSnapshotID,
	} {
		if strings.TrimSpace(value) == "" || strings.TrimSpace(value) != value {
			return fmt.Errorf("rflsc onboarding-query v2 %s is not canonical", name)
		}
	}
	if query.SchemaID != BaseURL+"rflsc.onboarding-query.v2.schema.json" || query.SchemaVersion != 2 || (query.Freshness != "current" && query.Freshness != "historical") || (query.Level != 1 && query.Level != 2) {
		return fmt.Errorf("rflsc onboarding-query v2 identity or selector is invalid")
	}
	var budget struct {
		TargetMin int `json:"targetMin"`
		TargetMax int `json:"targetMax"`
	}
	var envelope struct {
		DisplayBudget *struct {
			TargetMin int `json:"targetMin"`
			TargetMax int `json:"targetMax"`
		} `json:"displayBudget"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return fmt.Errorf("parse rflsc onboarding-query v2 display budget: %w", err)
	}
	if envelope.DisplayBudget != nil {
		budget = struct {
			TargetMin int `json:"targetMin"`
			TargetMax int `json:"targetMax"`
		}{envelope.DisplayBudget.TargetMin, envelope.DisplayBudget.TargetMax}
		if budget.TargetMax < budget.TargetMin {
			return fmt.Errorf("rflsc onboarding-query v2 display budget is not ordered")
		}
	}
	return nil
}

// ValidateDomainCandidateV2 validates one projected domain candidate.  A
// candidate can be candidate or confirmed, but a confirmed label must retain
// explicit ownership Evidence rather than relying on a generic source ref.
func ValidateDomainCandidateV2(data []byte) error {
	if err := Validate(BaseURL+"rflsc.domain-candidate.v2.schema.json", data); err != nil {
		return fmt.Errorf("rflsc domain-candidate v2 schema violation: %w", err)
	}
	var candidate struct {
		DomainID              string   `json:"domainId"`
		EntryPoints           []string `json:"entryPoints"`
		EvidenceRefs          []string `json:"evidenceRefs"`
		OwnershipEvidenceRefs []string `json:"ownershipEvidenceRefs"`
		GlossaryEvidenceRefs  []string `json:"glossaryEvidenceRefs"`
		Confidence            float64  `json:"confidence"`
		EpistemicState        string   `json:"epistemicState"`
		RepresentativeCount   int      `json:"representativeFlowCount"`
	}
	if err := json.Unmarshal(data, &candidate); err != nil {
		return fmt.Errorf("parse rflsc domain-candidate v2 JSON: %w", err)
	}
	if !canonicalIdentity(candidate.DomainID) || candidate.RepresentativeCount < 0 || candidate.Confidence < 0 || candidate.Confidence > 1 || len(candidate.EntryPoints) == 0 || len(candidate.EvidenceRefs) == 0 {
		return fmt.Errorf("rflsc domain-candidate v2 identity/evidence is incomplete")
	}
	if !canonicalStringArray(candidate.EntryPoints) || !canonicalStringArray(candidate.EvidenceRefs) || !canonicalStringArray(candidate.OwnershipEvidenceRefs) || !canonicalStringArray(candidate.GlossaryEvidenceRefs) {
		return fmt.Errorf("rflsc domain-candidate v2 Evidence refs are not sorted and unique")
	}
	if candidate.EpistemicState == "confirmed" && len(candidate.OwnershipEvidenceRefs) == 0 && len(candidate.GlossaryEvidenceRefs) == 0 {
		return fmt.Errorf("confirmed rflsc domain-candidate v2 lacks explicit ownership Evidence")
	}
	return nil
}

// ValidateDomainOverviewV2 validates the complete progressive onboarding
// overview and its measured coverage/recovery fields.
func ValidateDomainOverviewV2(data []byte) error {
	if err := Validate(BaseURL+"rflsc.domain-overview.v2.schema.json", data); err != nil {
		return fmt.Errorf("rflsc domain-overview v2 schema violation: %w", err)
	}
	var overview struct {
		SchemaID                   string            `json:"schemaId"`
		SchemaVersion              int               `json:"schemaVersion"`
		RepositoryID               string            `json:"repositoryId"`
		ComputedBasisID            string            `json:"computedBasisId"`
		GenerationID               string            `json:"generationId"`
		ValidatedAgainstSnapshotID string            `json:"validatedAgainstSnapshotId"`
		Domains                    []json.RawMessage `json:"domains"`
		UnmappedModules            []string          `json:"unmappedModules"`
		Summary                    struct {
			TotalDomains  int     `json:"totalDomains"`
			TotalFlows    int     `json:"totalFlows"`
			CoverageRatio float64 `json:"coverageRatio"`
		} `json:"summary"`
	}
	if err := json.Unmarshal(data, &overview); err != nil {
		return fmt.Errorf("parse rflsc domain-overview v2 JSON: %w", err)
	}
	if overview.SchemaID != BaseURL+"rflsc.domain-overview.v2.schema.json" || overview.SchemaVersion != 2 || !canonicalIdentity(overview.RepositoryID) || !canonicalIdentity(overview.ComputedBasisID) || !canonicalIdentity(overview.GenerationID) || !canonicalIdentity(overview.ValidatedAgainstSnapshotID) {
		return fmt.Errorf("rflsc domain-overview v2 identity is incomplete")
	}
	if overview.Summary.TotalDomains != len(overview.Domains) || overview.Summary.TotalDomains < 0 || overview.Summary.TotalFlows < 0 || overview.Summary.CoverageRatio < 0 || overview.Summary.CoverageRatio > 1 {
		return fmt.Errorf("rflsc domain-overview v2 summary is inconsistent")
	}
	if !canonicalStringArray(overview.UnmappedModules) {
		return fmt.Errorf("rflsc domain-overview v2 unmapped modules are not sorted and unique")
	}
	if !canonicalUnknownSubjects(data, "unknowns") {
		return fmt.Errorf("rflsc domain-overview v2 unknowns are not sorted and unique")
	}
	var domainOrder []struct {
		Name   string   `json:"name"`
		ID     string   `json:"domainId"`
		Points []string `json:"entryPoints"`
	}
	if err := json.Unmarshal(data, &struct {
		Domains *[]struct {
			Name   string   `json:"name"`
			ID     string   `json:"domainId"`
			Points []string `json:"entryPoints"`
		} `json:"domains"`
	}{Domains: &domainOrder}); err != nil {
		return fmt.Errorf("parse rflsc domain-overview v2 ordering: %w", err)
	}
	for index := range domainOrder {
		if index > 0 && canonicalDomainOrderLess(domainOrder[index], domainOrder[index-1]) {
			return fmt.Errorf("rflsc domain-overview v2 domains are not in canonical order")
		}
		if !canonicalStringArray(domainOrder[index].Points) {
			return fmt.Errorf("rflsc domain-overview v2 entry points are not sorted and unique")
		}
	}
	totalFlows := 0
	for index, raw := range overview.Domains {
		if err := ValidateDomainCandidateV2(raw); err != nil {
			return fmt.Errorf("rflsc domain-overview v2 domain[%d]: %w", index, err)
		}
		var candidate struct {
			RepresentativeFlowCount int `json:"representativeFlowCount"`
		}
		if err := json.Unmarshal(raw, &candidate); err != nil {
			return fmt.Errorf("rflsc domain-overview v2 domain[%d] parse: %w", index, err)
		}
		totalFlows += candidate.RepresentativeFlowCount
	}
	if totalFlows != overview.Summary.TotalFlows {
		return fmt.Errorf("rflsc domain-overview v2 totalFlows is inconsistent with domain candidates")
	}
	return nil
}

// ValidateRepresentativeFlowCatalogV2 validates the level-2 catalog and
// checks that every flow remains grounded in the catalog's generation/map.
func ValidateRepresentativeFlowCatalogV2(data []byte) error {
	if err := Validate(BaseURL+"rflsc.representative-flow-catalog.v2.schema.json", data); err != nil {
		return fmt.Errorf("rflsc representative-flow-catalog v2 schema violation: %w", err)
	}
	var catalog struct {
		SchemaID      string `json:"schemaId"`
		SchemaVersion int    `json:"schemaVersion"`
		CatalogID     string `json:"catalogId"`
		DomainID      string `json:"domainId"`
		GenerationID  string `json:"generationId"`
		Flows         []struct {
			FlowID          string   `json:"flowId"`
			GroundedMapID   string   `json:"groundedMapId"`
			GenerationID    string   `json:"generationId"`
			ComplexityScore float64  `json:"complexityScore"`
			EvidenceRefs    []string `json:"evidenceRefs"`
			EntryEvidence   []string `json:"entryEvidenceRefs"`
			ResultEvidence  []string `json:"resultEvidenceRefs"`
		} `json:"flows"`
	}
	if err := json.Unmarshal(data, &catalog); err != nil {
		return fmt.Errorf("parse rflsc representative-flow-catalog v2 JSON: %w", err)
	}
	if catalog.SchemaID != BaseURL+"rflsc.representative-flow-catalog.v2.schema.json" || catalog.SchemaVersion != 2 || !canonicalIdentity(catalog.CatalogID) || !canonicalIdentity(catalog.DomainID) || !canonicalIdentity(catalog.GenerationID) {
		return fmt.Errorf("rflsc representative-flow-catalog v2 identity is incomplete")
	}
	seen := map[string]bool{}
	if !canonicalStringArrayFromCatalog(data, "unmappedModules") || !canonicalUnknownSubjects(data, "unknowns") {
		return fmt.Errorf("rflsc representative-flow-catalog v2 unmapped/unknown arrays are not canonical")
	}
	previousScore := 0.0
	previousID := ""
	for index, flow := range catalog.Flows {
		if !canonicalIdentity(flow.FlowID) || seen[flow.FlowID] || !canonicalIdentity(flow.GroundedMapID) || flow.GenerationID != catalog.GenerationID || flow.ComplexityScore < 0 || len(flow.EvidenceRefs) == 0 || len(flow.EntryEvidence) == 0 {
			return fmt.Errorf("rflsc representative-flow-catalog v2 contains an ungrounded or duplicate flow")
		}
		if index > 0 && (flow.ComplexityScore > previousScore || (flow.ComplexityScore == previousScore && flow.FlowID < previousID)) {
			return fmt.Errorf("rflsc representative-flow-catalog v2 flows are not in canonical score/ID order")
		}
		if !canonicalStringArray(flow.EvidenceRefs) || !canonicalStringArray(flow.EntryEvidence) || !canonicalStringArray(flow.ResultEvidence) {
			return fmt.Errorf("rflsc representative-flow-catalog v2 entry Evidence refs are not sorted and unique")
		}
		previousScore, previousID = flow.ComplexityScore, flow.FlowID
		seen[flow.FlowID] = true
	}
	return nil
}

// ValidateOnboardingFlowDrilldownV2 validates the dedicated public shape for
// a level-2 flow drilldown. The drilldown is reference-only, so all identity
// fields and reference arrays must be canonical and internally consistent.
func ValidateOnboardingFlowDrilldownV2(data []byte) error {
	if err := Validate(BaseURL+"rflsc.onboarding-flow-drilldown.v2.schema.json", data); err != nil {
		return fmt.Errorf("rflsc onboarding-flow-drilldown v2 schema violation: %w", err)
	}
	var drilldown struct {
		SchemaID                   string   `json:"schemaId"`
		SchemaVersion              int      `json:"schemaVersion"`
		FlowID                     string   `json:"flowId"`
		DomainID                   string   `json:"domainId"`
		CanonicalMapID             string   `json:"canonicalMapId"`
		ComputedBasisID            string   `json:"computedBasisId"`
		GenerationID               string   `json:"generationId"`
		ValidatedAgainstSnapshotID string   `json:"validatedAgainstSnapshotId"`
		Freshness                  string   `json:"freshness"`
		StepRefs                   []string `json:"stepRefs"`
		EvidenceRefs               []string `json:"evidenceRefs"`
	}
	if err := json.Unmarshal(data, &drilldown); err != nil {
		return fmt.Errorf("parse rflsc onboarding-flow-drilldown v2 JSON: %w", err)
	}
	if drilldown.SchemaID != BaseURL+"rflsc.onboarding-flow-drilldown.v2.schema.json" || drilldown.SchemaVersion != 2 || !canonicalIdentity(drilldown.FlowID) || !canonicalIdentity(drilldown.DomainID) || !canonicalIdentity(drilldown.CanonicalMapID) || !canonicalIdentity(drilldown.ComputedBasisID) || !canonicalIdentity(drilldown.GenerationID) || !canonicalIdentity(drilldown.ValidatedAgainstSnapshotID) || (drilldown.Freshness != "current" && drilldown.Freshness != "historical") {
		return fmt.Errorf("rflsc onboarding-flow-drilldown v2 identity is incomplete")
	}
	if !canonicalStringArray(drilldown.StepRefs) || !canonicalStringArray(drilldown.EvidenceRefs) {
		return fmt.Errorf("rflsc onboarding-flow-drilldown v2 references are not sorted and unique")
	}
	if !canonicalUnknownSubjects(data, "unknowns") {
		return fmt.Errorf("rflsc onboarding-flow-drilldown v2 unknowns are not sorted and unique")
	}
	return nil
}

func canonicalStringArray(values []string) bool {
	for index, value := range values {
		if strings.TrimSpace(value) == "" || strings.TrimSpace(value) != value || (index > 0 && values[index-1] >= value) {
			return false
		}
	}
	return true
}

func canonicalIdentity(value string) bool {
	return strings.TrimSpace(value) != "" && strings.TrimSpace(value) == value
}

func canonicalDomainOrderLess(left, right struct {
	Name   string   `json:"name"`
	ID     string   `json:"domainId"`
	Points []string `json:"entryPoints"`
}) bool {
	leftName, rightName := strings.ToLower(left.Name), strings.ToLower(right.Name)
	if leftName != rightName {
		return leftName < rightName
	}
	return left.ID < right.ID
}

func canonicalStringArrayFromCatalog(data []byte, field string) bool {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		return false
	}
	var values []string
	if err := json.Unmarshal(object[field], &values); err != nil {
		return false
	}
	return canonicalStringArray(values)
}

func canonicalUnknownSubjects(data []byte, field string) bool {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		return false
	}
	var unknowns []struct {
		Subject string `json:"subject"`
	}
	if err := json.Unmarshal(object[field], &unknowns); err != nil {
		return false
	}
	seen := make([]string, 0, len(unknowns))
	for _, unknown := range unknowns {
		seen = append(seen, unknown.Subject)
	}
	if !sort.StringsAreSorted(seen) {
		return false
	}
	return canonicalStringArray(seen)
}
