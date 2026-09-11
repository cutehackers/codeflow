package contractharness

import "fmt"

// VS04ContractRegistryEntry records the producer, consumer, and fixture
// evidence for one public R2 semantic compiler contract.
type VS04ContractRegistryEntry struct {
	ID                      string
	SchemaID                string
	Producer                string
	Consumer                string
	ValidFixture            string
	InvalidAuthorityFixture string
	InvalidIdentityFixture  string
}

// VS04EvidenceRegistryID is the stable evidence registry identity from the
// approved VS-04 contract.
const VS04EvidenceRegistryID = "rflsc-r2-vs-04"

// VS04ContractRegistry is the single registry for the seven VS-04 public
// contracts. Fixture paths are relative to schemas/fixtures.
var VS04ContractRegistry = []VS04ContractRegistryEntry{
	{
		ID:                      "rflsc.task-intent.v2",
		SchemaID:                BaseURL + "rflsc.task-intent.v2.schema.json",
		Producer:                "semantic.NormalizeTaskIntent",
		Consumer:                "semantic compiler and task/review query endpoints",
		ValidFixture:            "rflsc.task-intent.v2/valid/basic.json",
		InvalidAuthorityFixture: "rflsc.task-intent.v2/invalid/forged-authority.json",
		InvalidIdentityFixture:  "rflsc.task-intent.v2/invalid/empty-raw.json",
	},
	{
		ID:                      "rflsc.feature-query.v2",
		SchemaID:                BaseURL + "rflsc.feature-query.v2.schema.json",
		Producer:                "task view feature query decoder",
		Consumer:                "semantic.ResolveFeatureQueryTarget",
		ValidFixture:            "rflsc.feature-query.v2/valid/request.json",
		InvalidAuthorityFixture: "rflsc.feature-query.v2/invalid/wrong-mode.json",
		InvalidIdentityFixture:  "rflsc.feature-query.v2/invalid/no-start.json",
	},
	{
		ID:                      "rflsc.review-query.v2",
		SchemaID:                BaseURL + "rflsc.review-query.v2.schema.json",
		Producer:                "task review query decoder",
		Consumer:                "semantic.ComputeSemanticDelta and requirement alignment",
		ValidFixture:            "rflsc.review-query.v2/valid/generations.json",
		InvalidAuthorityFixture: "rflsc.review-query.v2/invalid/wrong-version.json",
		InvalidIdentityFixture:  "rflsc.review-query.v2/invalid/missing-current.json",
	},
	{
		ID:                      "rflsc.semantic-map-ir.v2",
		SchemaID:                BaseURL + "rflsc.semantic-map-ir.v2.schema.json",
		Producer:                "semantic.CompileDeterministicFeatureMap",
		Consumer:                "FlowView, MCP and task/review result projections",
		ValidFixture:            "rflsc.semantic-map-ir.v2/valid/empty-candidate.json",
		InvalidAuthorityFixture: "rflsc.semantic-map-ir.v2/invalid/confirmed-authority.json",
		InvalidIdentityFixture:  "rflsc.semantic-map-ir.v2/invalid/edge-without-endpoint.json",
	},
	{
		ID:                      "rflsc.semantic-delta-ir.v2",
		SchemaID:                BaseURL + "rflsc.semantic-delta-ir.v2.schema.json",
		Producer:                "semantic.ComputeSemanticDelta",
		Consumer:                "task/review change result and FlowView change pulse",
		ValidFixture:            "rflsc.semantic-delta-ir.v2/valid/no-changes.json",
		InvalidAuthorityFixture: "rflsc.semantic-delta-ir.v2/invalid/invalid-change-kind.json",
		InvalidIdentityFixture:  "rflsc.semantic-delta-ir.v2/invalid/missing-summary.json",
	},
	{
		ID:                      "rflsc.requirement-alignment.v2",
		SchemaID:                BaseURL + "rflsc.requirement-alignment.v2.schema.json",
		Producer:                "semantic.ComputeRequirementAlignment",
		Consumer:                "task/review requirement status and FlowView alignment rail",
		ValidFixture:            "rflsc.requirement-alignment.v2/valid/partial.json",
		InvalidAuthorityFixture: "rflsc.requirement-alignment.v2/invalid/agent-authority.json",
		InvalidIdentityFixture:  "rflsc.requirement-alignment.v2/invalid/confirmed.json",
	},
	{
		ID:                      "rflsc.flowview-projection.v2",
		SchemaID:                BaseURL + "rflsc.flowview-projection.v2.schema.json",
		Producer:                "semantic.BuildFlowViewProjection",
		Consumer:                "FlowView critical-boundary renderer",
		ValidFixture:            "rflsc.flowview-projection.v2/valid/empty.json",
		InvalidAuthorityFixture: "rflsc.flowview-projection.v2/invalid/wrong-schema.json",
		InvalidIdentityFixture:  "rflsc.flowview-projection.v2/invalid/empty-boundary-ref.json",
	},
}

// ValidateVS04RegistryEntry validates a valid fixture and both required
// invalid authority/identity fixtures through the production contract
// validator for that boundary. It does not accept a merely well-formed JSON
// payload when a semantic validator is available.
func ValidateVS04RegistryEntry(entry VS04ContractRegistryEntry, valid, invalidAuthority, invalidIdentity []byte) error {
	if entry.ID == "" || entry.SchemaID == "" || entry.Producer == "" || entry.Consumer == "" || entry.ValidFixture == "" || entry.InvalidAuthorityFixture == "" || entry.InvalidIdentityFixture == "" {
		return fmt.Errorf("VS-04 contract registry entry is incomplete: %+v", entry)
	}
	expectedSchemaID := BaseURL + entry.ID + ".schema.json"
	if entry.SchemaID != expectedSchemaID {
		return fmt.Errorf("%s schema identity mismatch: got %q want %q", entry.ID, entry.SchemaID, expectedSchemaID)
	}
	validator := compilerContractValidatorFor(entry.ID)
	if validator == nil {
		return fmt.Errorf("%s has no production contract validator", entry.ID)
	}
	if err := validator(valid); err != nil {
		return fmt.Errorf("%s valid fixture: %w", entry.ID, err)
	}
	if err := validator(invalidAuthority); err == nil {
		return fmt.Errorf("%s authority fixture unexpectedly passed validation", entry.ID)
	}
	if err := validator(invalidIdentity); err == nil {
		return fmt.Errorf("%s identity fixture unexpectedly passed validation", entry.ID)
	}
	return nil
}

type compilerContractValidator func([]byte) error

func compilerContractValidatorFor(id string) compilerContractValidator {
	switch id {
	case "rflsc.task-intent.v2":
		return ValidateTaskIntent
	case "rflsc.feature-query.v2", "rflsc.review-query.v2":
		return ValidateTaskViewQuery
	case "rflsc.semantic-map-ir.v2":
		return ValidateSemanticMapIR
	case "rflsc.semantic-delta-ir.v2":
		return ValidateSemanticDeltaIR
	case "rflsc.requirement-alignment.v2":
		return ValidateRequirementAlignment
	case "rflsc.flowview-projection.v2":
		return ValidateFlowViewProjection
	default:
		return nil
	}
}
