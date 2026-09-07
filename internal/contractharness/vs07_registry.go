package contractharness

import "fmt"

// VS07EvidenceRegistryID is the stable executable evidence registry identity
// from the approved VS-07 contract.
const VS07EvidenceRegistryID = "rflsc-r2-vs-07"

// VS07EvidenceCriteria is ordered by the public acceptance criteria.  The
// executable registry test uses this list to reject missing, duplicate,
// not-run and zero-match records.  Domain contract validators remain owned by
// the semantic/public seam implementation and are deliberately not duplicated
// here.
var VS07EvidenceCriteria = []string{
	"VS07-A1", "VS07-A2", "VS07-A3", "VS07-A4", "VS07-A5", "VS07-A6", "VS07-A7",
}

// VS07ContractRegistryEntry records the producer, consumer, and adversarial
// fixture for each onboarding contract.  The four fixture classes are kept
// explicit so a schema can never be registered with only a happy-path sample.
type VS07ContractRegistryEntry struct {
	ID                       string
	SchemaID                 string
	Producer                 string
	Consumer                 string
	ValidFixture             string
	InvalidRepositoryFixture string
	InvalidEvidenceFixture   string
	InvalidOrderingFixture   string
}

// VS07ContractRegistry is the executable registry for all four v2 onboarding
// boundaries. Fixture paths are relative to schemas/fixtures.
var VS07ContractRegistry = []VS07ContractRegistryEntry{
	{
		ID: "rflsc.onboarding-query.v2", SchemaID: BaseURL + "rflsc.onboarding-query.v2.schema.json",
		Producer: "semantic onboarding query decoder", Consumer: "semantic.ExploreDomainsV2 and progressive onboarding endpoints",
		ValidFixture:             "rflsc.onboarding-query.v2/valid/basic.json",
		InvalidRepositoryFixture: "rflsc.onboarding-query.v2/invalid/missing-repository.json",
		InvalidEvidenceFixture:   "rflsc.onboarding-query.v2/invalid/evidence-mismatch.json",
		InvalidOrderingFixture:   "rflsc.onboarding-query.v2/invalid/noncanonical-order.json",
	},
	{
		ID: "rflsc.domain-overview.v2", SchemaID: BaseURL + "rflsc.domain-overview.v2.schema.json",
		Producer: "semantic.ExploreDomainsV2", Consumer: "onboarding level-1 domain overview and FlowView onboarding",
		ValidFixture:             "rflsc.domain-overview.v2/valid/overview.json",
		InvalidRepositoryFixture: "rflsc.domain-overview.v2/invalid/repository-mismatch.json",
		InvalidEvidenceFixture:   "rflsc.domain-overview.v2/invalid/evidence-mismatch.json",
		InvalidOrderingFixture:   "rflsc.domain-overview.v2/invalid/noncanonical-order.json",
	},
	{
		ID: "rflsc.domain-candidate.v2", SchemaID: BaseURL + "rflsc.domain-candidate.v2.schema.json",
		Producer: "semantic.projectOnboardingDomains", Consumer: "domain overview domain candidate renderer",
		ValidFixture:             "rflsc.domain-candidate.v2/valid/candidate.json",
		InvalidRepositoryFixture: "rflsc.domain-candidate.v2/invalid/repository-mismatch.json",
		InvalidEvidenceFixture:   "rflsc.domain-candidate.v2/invalid/missing-evidence.json",
		InvalidOrderingFixture:   "rflsc.domain-candidate.v2/invalid/noncanonical-order.json",
	},
	{
		ID: "rflsc.representative-flow-catalog.v2", SchemaID: BaseURL + "rflsc.representative-flow-catalog.v2.schema.json",
		Producer: "semantic.GetRepresentativeFlowCatalogV2", Consumer: "onboarding level-2 catalog and same-map drilldown",
		ValidFixture:             "rflsc.representative-flow-catalog.v2/valid/catalog.json",
		InvalidRepositoryFixture: "rflsc.representative-flow-catalog.v2/invalid/repository-mismatch.json",
		InvalidEvidenceFixture:   "rflsc.representative-flow-catalog.v2/invalid/missing-entry-evidence.json",
		InvalidOrderingFixture:   "rflsc.representative-flow-catalog.v2/invalid/noncanonical-order.json",
	},
}

type vs07ContractValidator func([]byte) error

// ValidateVS07ContractRegistryEntry runs every fixture through the production
// semantic validator.  It is intentionally stricter than raw schema checks so
// cross-field identity, Evidence, and canonical ordering cannot drift.
func ValidateVS07ContractRegistryEntry(entry VS07ContractRegistryEntry, valid, invalidRepository, invalidEvidence, invalidOrdering []byte) error {
	if entry.ID == "" || entry.SchemaID == "" || entry.Producer == "" || entry.Consumer == "" || entry.ValidFixture == "" || entry.InvalidRepositoryFixture == "" || entry.InvalidEvidenceFixture == "" || entry.InvalidOrderingFixture == "" {
		return fmt.Errorf("VS-07 contract registry entry is incomplete: %+v", entry)
	}
	if entry.SchemaID != BaseURL+entry.ID+".schema.json" {
		return fmt.Errorf("%s schema identity mismatch: got %q", entry.ID, entry.SchemaID)
	}
	validator := validatorForVS07Contract(entry.ID)
	if validator == nil {
		return fmt.Errorf("%s has no production contract validator", entry.ID)
	}
	if err := validator(valid); err != nil {
		return fmt.Errorf("%s valid fixture: %w", entry.ID, err)
	}
	for name, data := range map[string][]byte{"repository": invalidRepository, "Evidence": invalidEvidence, "ordering": invalidOrdering} {
		if err := validator(data); err == nil {
			return fmt.Errorf("%s invalid %s fixture unexpectedly passed validation", entry.ID, name)
		}
	}
	return nil
}

func validatorForVS07Contract(id string) vs07ContractValidator {
	switch id {
	case "rflsc.onboarding-query.v2":
		return ValidateOnboardingQueryV2
	case "rflsc.domain-overview.v2":
		return ValidateDomainOverviewV2
	case "rflsc.domain-candidate.v2":
		return ValidateDomainCandidateV2
	case "rflsc.representative-flow-catalog.v2":
		return ValidateRepresentativeFlowCatalogV2
	default:
		return nil
	}
}
