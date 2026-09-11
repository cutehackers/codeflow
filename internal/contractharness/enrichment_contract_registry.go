package contractharness

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strings"

	"codeflow/schemas"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

const VS08EvidenceRegistryID = "rflsc-r2-vs-08"

var VS08EvidenceCriteria = []string{
	"VS08-A1", "VS08-A2", "VS08-A3", "VS08-A4", "VS08-A5",
	"VS08-A6", "VS08-A7", "VS08-A8", "VS08-A9", "VS08-A10",
}

// VS08ContractRegistry records the six public enrichment schemas and the
// adversarial fixture classes required by the approved contract.
type VS08ContractRegistryEntry struct {
	ID                      string
	SchemaID                string
	Producer                string
	Consumer                string
	ValidFixture            string
	InvalidAuthorityFixture string
	InvalidReferenceFixture string
	InvalidBoundFixture     string
}

var VS08ContractRegistry = []VS08ContractRegistryEntry{
	{ID: "rflsc.evidence-pack.v2", SchemaID: BaseURL + "rflsc.evidence-pack.v2.schema.json", Producer: "semantic.BuildEvidencePackV2", Consumer: "model host and browser egress", ValidFixture: "rflsc.evidence-pack.v2/valid/pack.json", InvalidAuthorityFixture: "rflsc.evidence-pack.v2/invalid/unverified-item.json", InvalidReferenceFixture: "rflsc.evidence-pack.v2/invalid/unsafe-path.json", InvalidBoundFixture: "rflsc.evidence-pack.v2/invalid/oversized-pack.json"},
	{ID: "rflsc.model-host-request.v2", SchemaID: BaseURL + "rflsc.model-host-request.v2.schema.json", Producer: "semantic.RunSemanticEnrichment", Consumer: "protocol.ModelHost", ValidFixture: "rflsc.model-host-request.v2/valid/request.json", InvalidAuthorityFixture: "rflsc.model-host-request.v2/invalid/missing-pack.json", InvalidReferenceFixture: "rflsc.model-host-request.v2/invalid/missing-target.json", InvalidBoundFixture: "rflsc.model-host-request.v2/invalid/oversized-response-bound.json"},
	{ID: "rflsc.model-host-response.v2", SchemaID: BaseURL + "rflsc.model-host-response.v2.schema.json", Producer: "protocol.ModelHost", Consumer: "semantic proposal validator", ValidFixture: "rflsc.model-host-response.v2/valid/response.json", InvalidAuthorityFixture: "rflsc.model-host-response.v2/invalid/forged-authority.json", InvalidReferenceFixture: "rflsc.model-host-response.v2/invalid/missing-proposal.json", InvalidBoundFixture: "rflsc.model-host-response.v2/invalid/oversized-response.json"},
	{ID: "rflsc.semantic-proposal.v2", SchemaID: BaseURL + "rflsc.semantic-proposal.v2.schema.json", Producer: "semantic proposal validator", Consumer: "FlowView and MCP inferred projection", ValidFixture: "rflsc.semantic-proposal.v2/valid/proposal.json", InvalidAuthorityFixture: "rflsc.semantic-proposal.v2/invalid/verified-authority.json", InvalidReferenceFixture: "rflsc.semantic-proposal.v2/invalid/empty-evidence.json", InvalidBoundFixture: "rflsc.semantic-proposal.v2/invalid/bad-digest.json"},
	{ID: "rflsc.enrichment-state.v2", SchemaID: BaseURL + "rflsc.enrichment-state.v2.schema.json", Producer: "semantic.RunSemanticEnrichment", Consumer: "FlowView and MCP status", ValidFixture: "rflsc.enrichment-state.v2/valid/available.json", InvalidAuthorityFixture: "rflsc.enrichment-state.v2/invalid/unknown-status.json", InvalidReferenceFixture: "rflsc.enrichment-state.v2/invalid/repository-write-capability.json", InvalidBoundFixture: "rflsc.enrichment-state.v2/invalid/unbounded-attempts.json"},
	{ID: "rflsc.model-activation-disclosure.v1", SchemaID: BaseURL + "rflsc.model-activation-disclosure.v1.schema.json", Producer: "semantic.NewModelActivationDisclosure", Consumer: "activation choice UI", ValidFixture: "rflsc.model-activation-disclosure.v1/valid/disclosure.json", InvalidAuthorityFixture: "rflsc.model-activation-disclosure.v1/invalid/implicit-approval.json", InvalidReferenceFixture: "rflsc.model-activation-disclosure.v1/invalid/missing-checksum.json", InvalidBoundFixture: "rflsc.model-activation-disclosure.v1/invalid/unknown-choice.json"},
}

type enrichmentContractDef struct {
	producer string
	consumer string
}

var expectedEnrichmentContractDefs = map[string]enrichmentContractDef{
	"rflsc.evidence-pack.v2":               {producer: "semantic.BuildEvidencePackV2", consumer: "model host and browser egress"},
	"rflsc.model-host-request.v2":          {producer: "semantic.RunSemanticEnrichment", consumer: "protocol.ModelHost"},
	"rflsc.model-host-response.v2":         {producer: "protocol.ModelHost", consumer: "semantic proposal validator"},
	"rflsc.semantic-proposal.v2":           {producer: "semantic proposal validator", consumer: "FlowView and MCP inferred projection"},
	"rflsc.enrichment-state.v2":            {producer: "semantic.RunSemanticEnrichment", consumer: "FlowView and MCP status"},
	"rflsc.model-activation-disclosure.v1": {producer: "semantic.NewModelActivationDisclosure", consumer: "activation choice UI"},
}

type enrichmentInvalidFixture struct {
	instanceLocation string
	keyword          string
}

// expectedEnrichmentInvalidFixtures binds each named invalid fixture to
// the one schema violation it is intended to exercise. The validator below
// checks the structured jsonschema.ValidationError tree rather than trusting
// fixture filenames or class labels.
var expectedEnrichmentInvalidFixtures = map[string]map[string]enrichmentInvalidFixture{
	"rflsc.evidence-pack.v2": {
		"invalid authority": {instanceLocation: "/items/0/verified", keyword: "const"},
		"invalid reference": {instanceLocation: "/items/0/source", keyword: "pattern"},
		"invalid bound":     {instanceLocation: "/targetStepIds", keyword: "maxItems"},
	},
	"rflsc.model-host-request.v2": {
		"invalid authority": {instanceLocation: "", keyword: "required"},
		"invalid reference": {instanceLocation: "/targetSymbolPath", keyword: "minLength"},
		"invalid bound":     {instanceLocation: "/maxResponseBytes", keyword: "maximum"},
	},
	"rflsc.model-host-response.v2": {
		"invalid authority": {instanceLocation: "/proposal/authority", keyword: "enum"},
		"invalid reference": {instanceLocation: "", keyword: "required"},
		"invalid bound":     {instanceLocation: "/proposal/confidence", keyword: "maximum"},
	},
	"rflsc.semantic-proposal.v2": {
		"invalid authority": {instanceLocation: "/authority", keyword: "enum"},
		"invalid reference": {instanceLocation: "/evidenceRefs", keyword: "minItems"},
		"invalid bound":     {instanceLocation: "/factDigest", keyword: "pattern"},
	},
	"rflsc.enrichment-state.v2": {
		"invalid authority": {instanceLocation: "/status", keyword: "enum"},
		"invalid reference": {instanceLocation: "/isolation/repositoryWriteCapability", keyword: "const"},
		"invalid bound":     {instanceLocation: "/isolation/repositoryWriteAttempts", keyword: "maxItems"},
	},
	"rflsc.model-activation-disclosure.v1": {
		"invalid authority": {instanceLocation: "/choiceRequired", keyword: "const"},
		"invalid reference": {instanceLocation: "", keyword: "required"},
		"invalid bound":     {instanceLocation: "/choice", keyword: "enum"},
	},
}

type enrichmentFixture struct {
	class string
	path  string
}

func enrichmentRegistryFixtures(entry VS08ContractRegistryEntry) []enrichmentFixture {
	return []enrichmentFixture{
		{class: "valid", path: entry.ValidFixture},
		{class: "invalid authority", path: entry.InvalidAuthorityFixture},
		{class: "invalid reference", path: entry.InvalidReferenceFixture},
		{class: "invalid bound", path: entry.InvalidBoundFixture},
	}
}

func validateVS08ContractRegistryMetadata(entry VS08ContractRegistryEntry) error {
	if entry.ID == "" || entry.SchemaID == "" || entry.Producer == "" || entry.Consumer == "" || entry.ValidFixture == "" || entry.InvalidAuthorityFixture == "" || entry.InvalidReferenceFixture == "" || entry.InvalidBoundFixture == "" {
		return fmt.Errorf("VS-08 contract registry entry is incomplete: %+v", entry)
	}
	definition, ok := expectedEnrichmentContractDefs[entry.ID]
	if !ok {
		return fmt.Errorf("VS-08 contract registry entry has unexpected ID %q", entry.ID)
	}
	expectedSchemaID := BaseURL + entry.ID + ".schema.json"
	if entry.SchemaID != expectedSchemaID {
		return fmt.Errorf("%s schema identity mismatch: got %q want %q", entry.ID, entry.SchemaID, expectedSchemaID)
	}
	if entry.Producer != definition.producer {
		return fmt.Errorf("%s producer boundary mismatch: got %q want %q", entry.ID, entry.Producer, definition.producer)
	}
	if entry.Consumer != definition.consumer {
		return fmt.Errorf("%s consumer boundary mismatch: got %q want %q", entry.ID, entry.Consumer, definition.consumer)
	}
	seenFixtures := make(map[string]string, len(enrichmentRegistryFixtures(entry)))
	for _, fixture := range enrichmentRegistryFixtures(entry) {
		cleanPath := path.Clean(fixture.path)
		if cleanPath != fixture.path || strings.HasPrefix(fixture.path, "/") || strings.HasPrefix(cleanPath, "../") || strings.Contains(cleanPath, "/../") {
			return fmt.Errorf("%s %s fixture path is not a safe relative path: %q", entry.ID, fixture.class, fixture.path)
		}
		if !strings.HasPrefix(fixture.path, entry.ID+"/") {
			return fmt.Errorf("%s %s fixture path is outside its schema directory: %q", entry.ID, fixture.class, fixture.path)
		}
		if previous, duplicate := seenFixtures[fixture.path]; duplicate {
			return fmt.Errorf("%s fixture path %q is reused by %s and %s", entry.ID, fixture.path, previous, fixture.class)
		}
		seenFixtures[fixture.path] = fixture.class
	}
	return nil
}

// ValidateVS08ContractRegistryEntry validates the registry metadata and the
// four fixture payloads assigned to one VS-08 schema. The valid fixture must
// pass its schema, while each named invalid class must fail independently.
func ValidateVS08ContractRegistryEntry(entry VS08ContractRegistryEntry, valid, invalidAuthority, invalidReference, invalidBound []byte) error {
	if err := validateVS08ContractRegistryMetadata(entry); err != nil {
		return err
	}
	expectations, ok := expectedEnrichmentInvalidFixtures[entry.ID]
	if !ok {
		return fmt.Errorf("%s has no invalid fixture expectations", entry.ID)
	}
	fixtures := []struct {
		class string
		data  []byte
	}{
		{class: "valid", data: valid},
		{class: "invalid authority", data: invalidAuthority},
		{class: "invalid reference", data: invalidReference},
		{class: "invalid bound", data: invalidBound},
	}
	if err := Validate(entry.SchemaID, valid); err != nil {
		return fmt.Errorf("%s valid fixture %q failed schema validation: %w", entry.ID, entry.ValidFixture, err)
	}
	for _, fixture := range fixtures[1:] {
		expectation, ok := expectations[fixture.class]
		if !ok {
			return fmt.Errorf("%s has no expectation for %s", entry.ID, fixture.class)
		}
		if err := validateEnrichmentInvalidFixture(entry.SchemaID, fixture.data, expectation); err != nil {
			return fmt.Errorf("%s %s fixture %q: %w", entry.ID, fixture.class, enrichmentFixturePath(entry, fixture.class), err)
		}
	}
	return nil
}

type enrichmentValidationEvidence struct {
	instanceLocation string
	keyword          string
}

func validateEnrichmentInvalidFixture(schemaID string, data []byte, expectation enrichmentInvalidFixture) error {
	validationErr := Validate(schemaID, data)
	if validationErr == nil {
		return errors.New("fixture unexpectedly passed schema validation")
	}
	var structuredErr *jsonschema.ValidationError
	if !errors.As(validationErr, &structuredErr) {
		return fmt.Errorf("invalid fixture did not produce a structured schema validation error: %w", validationErr)
	}
	evidence := make([]enrichmentValidationEvidence, 0, 1)
	collectVS08ValidationEvidence(structuredErr, &evidence)
	if len(evidence) != 1 {
		return fmt.Errorf("got %d validation leaves, want one (%s %s): %v", len(evidence), expectation.instanceLocation, expectation.keyword, evidence)
	}
	actual := evidence[0]
	if actual != (enrichmentValidationEvidence(expectation)) {
		return fmt.Errorf("got %s %s, want %s %s", actual.instanceLocation, actual.keyword, expectation.instanceLocation, expectation.keyword)
	}
	return nil
}

func collectVS08ValidationEvidence(err *jsonschema.ValidationError, evidence *[]enrichmentValidationEvidence) {
	if len(err.Causes) == 0 {
		keywordPath := err.ErrorKind.KeywordPath()
		keyword := ""
		if len(keywordPath) > 0 {
			keyword = keywordPath[len(keywordPath)-1]
		}
		*evidence = append(*evidence, enrichmentValidationEvidence{
			instanceLocation: jsonPointerPath(err.InstanceLocation),
			keyword:          keyword,
		})
		return
	}
	for _, cause := range err.Causes {
		collectVS08ValidationEvidence(cause, evidence)
	}
}

func jsonPointerPath(parts []string) string {
	if len(parts) == 0 {
		return ""
	}
	var builder strings.Builder
	for _, part := range parts {
		builder.WriteByte('/')
		builder.WriteString(strings.ReplaceAll(strings.ReplaceAll(part, "~", "~0"), "/", "~1"))
	}
	return builder.String()
}

func enrichmentFixturePath(entry VS08ContractRegistryEntry, class string) string {
	for _, fixture := range enrichmentRegistryFixtures(entry) {
		if fixture.class == class {
			return fixture.path
		}
	}
	return ""
}

// ValidateVS08ContractRegistry validates the complete six-entry registry
// against the embedded schema and fixture bytes. It is the executable
// contract check used by VS-08 registry tests and release audits.
func ValidateVS08ContractRegistry() error {
	if len(VS08ContractRegistry) != len(expectedEnrichmentContractDefs) {
		return fmt.Errorf("VS-08 contract registry count=%d want=%d", len(VS08ContractRegistry), len(expectedEnrichmentContractDefs))
	}
	seenIDs := make(map[string]struct{}, len(VS08ContractRegistry))
	seenSchemas := make(map[string]struct{}, len(VS08ContractRegistry))
	seenFixtures := make(map[string]string, len(VS08ContractRegistry)*4)
	for _, entry := range VS08ContractRegistry {
		if _, duplicate := seenIDs[entry.ID]; duplicate {
			return fmt.Errorf("VS-08 contract registry duplicate ID %q", entry.ID)
		}
		seenIDs[entry.ID] = struct{}{}
		if _, duplicate := seenSchemas[entry.SchemaID]; duplicate {
			return fmt.Errorf("VS-08 contract registry duplicate schema ID %q", entry.SchemaID)
		}
		seenSchemas[entry.SchemaID] = struct{}{}
		if err := validateVS08ContractRegistryMetadata(entry); err != nil {
			return err
		}
		fixtureData := make([][]byte, 0, 4)
		for _, fixture := range enrichmentRegistryFixtures(entry) {
			if previous, duplicate := seenFixtures[fixture.path]; duplicate {
				return fmt.Errorf("VS-08 fixture path %q is reused by %s and %s", fixture.path, previous, entry.ID+"/"+fixture.class)
			}
			seenFixtures[fixture.path] = entry.ID + "/" + fixture.class
			data, err := readVS08EmbeddedFixture(fixture.path)
			if err != nil {
				return fmt.Errorf("%s %s fixture %q: %w", entry.ID, fixture.class, fixture.path, err)
			}
			fixtureData = append(fixtureData, data)
		}
		if err := ValidateVS08ContractRegistryEntry(entry, fixtureData[0], fixtureData[1], fixtureData[2], fixtureData[3]); err != nil {
			return err
		}
	}
	if len(seenIDs) != len(expectedEnrichmentContractDefs) {
		return fmt.Errorf("VS-08 contract registry ID set has %d entries want %d", len(seenIDs), len(expectedEnrichmentContractDefs))
	}
	return nil
}

func readVS08EmbeddedFixture(name string) ([]byte, error) {
	return fs.ReadFile(schemas.FixturesFS, path.Join("fixtures", name))
}
