package contractharness

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"reflect"
	"sort"
	"strings"

	"codeflow/schemas"
)

// VS09EvidenceRegistryID is the stable evidence registry identity from the
// approved VS-09 contract.
const VS09EvidenceRegistryID = "rflsc-r2-vs-09"

// VS09ContractRegistryEntry records one approval boundary and its executable
// valid and adversarial fixture evidence.
type VS09ContractRegistryEntry struct {
	ID                       string
	SchemaID                 string
	Producer                 string
	Consumer                 string
	ValidFixture             string
	InvalidAuthorityFixture  string
	InvalidReferenceFixture  string
	InvalidVersionFixture    string
	InvalidTransitionFixture string
}

// VS09ContractRegistry is ordered to match the five required VS-09 contracts
// followed by the canonical approval history read boundary.
var VS09ContractRegistry = []VS09ContractRegistryEntry{
	{
		ID: "rflsc.approval-command.v2", SchemaID: BaseURL + "rflsc.approval-command.v2.schema.json",
		Producer: "MCP/FlowView approval command decoder", Consumer: "semantic approval command validator",
		ValidFixture: "rflsc.approval-command.v2/valid/command.json", InvalidAuthorityFixture: "rflsc.approval-command.v2/invalid/authority.json",
		InvalidReferenceFixture: "rflsc.approval-command.v2/invalid/reference.json", InvalidVersionFixture: "rflsc.approval-command.v2/invalid/version.json", InvalidTransitionFixture: "rflsc.approval-command.v2/invalid/transition.json",
	},
	{
		ID: "rflsc.approval-event.v2", SchemaID: BaseURL + "rflsc.approval-event.v2.schema.json",
		Producer: "semantic approval event append boundary", Consumer: "approval history projector",
		ValidFixture: "rflsc.approval-event.v2/valid/event.json", InvalidAuthorityFixture: "rflsc.approval-event.v2/invalid/authority.json",
		InvalidReferenceFixture: "rflsc.approval-event.v2/invalid/reference.json", InvalidVersionFixture: "rflsc.approval-event.v2/invalid/version.json", InvalidTransitionFixture: "rflsc.approval-event.v2/invalid/transition.json",
	},
	{
		ID: "rflsc.approval-aggregate.v2", SchemaID: BaseURL + "rflsc.approval-aggregate.v2.schema.json",
		Producer: "approval event projector", Consumer: "MCP/FlowView approval history",
		ValidFixture: "rflsc.approval-aggregate.v2/valid/aggregate.json", InvalidAuthorityFixture: "rflsc.approval-aggregate.v2/invalid/authority.json",
		InvalidReferenceFixture: "rflsc.approval-aggregate.v2/invalid/reference.json", InvalidVersionFixture: "rflsc.approval-aggregate.v2/invalid/version.json", InvalidTransitionFixture: "rflsc.approval-aggregate.v2/invalid/transition.json",
	},
	{
		ID: "rflsc.approval-idempotency-result.v1", SchemaID: BaseURL + "rflsc.approval-idempotency-result.v1.schema.json",
		Producer: "approval transaction coordinator", Consumer: "approval retry handler",
		ValidFixture: "rflsc.approval-idempotency-result.v1/valid/result.json", InvalidAuthorityFixture: "rflsc.approval-idempotency-result.v1/invalid/authority.json",
		InvalidReferenceFixture: "rflsc.approval-idempotency-result.v1/invalid/reference.json", InvalidVersionFixture: "rflsc.approval-idempotency-result.v1/invalid/version.json", InvalidTransitionFixture: "rflsc.approval-idempotency-result.v1/invalid/transition.json",
	},
	{
		ID: "rflsc.approval-outbox.v1", SchemaID: BaseURL + "rflsc.approval-outbox.v1.schema.json",
		Producer: "approval transaction coordinator", Consumer: "post-commit event broadcaster",
		ValidFixture: "rflsc.approval-outbox.v1/valid/outbox.json", InvalidAuthorityFixture: "rflsc.approval-outbox.v1/invalid/authority.json",
		InvalidReferenceFixture: "rflsc.approval-outbox.v1/invalid/reference.json", InvalidVersionFixture: "rflsc.approval-outbox.v1/invalid/version.json", InvalidTransitionFixture: "rflsc.approval-outbox.v1/invalid/transition.json",
	},
	{
		ID: "rflsc.approval-history.v1", SchemaID: BaseURL + "rflsc.approval-history.v1.schema.json",
		Producer: "semantic approval history query service", Consumer: "MCP/FlowView approval history adapters",
		ValidFixture: "rflsc.approval-history.v1/valid/history.json", InvalidAuthorityFixture: "rflsc.approval-history.v1/invalid/authority.json",
		InvalidReferenceFixture: "rflsc.approval-history.v1/invalid/reference.json", InvalidVersionFixture: "rflsc.approval-history.v1/invalid/version.json", InvalidTransitionFixture: "rflsc.approval-history.v1/invalid/transition.json",
	},
}

type vs09ContractDefinition struct {
	producer string
	consumer string
}

var expectedVS09ContractDefinitions = map[string]vs09ContractDefinition{
	"rflsc.approval-command.v2":            {producer: "MCP/FlowView approval command decoder", consumer: "semantic approval command validator"},
	"rflsc.approval-event.v2":              {producer: "semantic approval event append boundary", consumer: "approval history projector"},
	"rflsc.approval-aggregate.v2":          {producer: "approval event projector", consumer: "MCP/FlowView approval history"},
	"rflsc.approval-idempotency-result.v1": {producer: "approval transaction coordinator", consumer: "approval retry handler"},
	"rflsc.approval-outbox.v1":             {producer: "approval transaction coordinator", consumer: "post-commit event broadcaster"},
	"rflsc.approval-history.v1":            {producer: "semantic approval history query service", consumer: "MCP/FlowView approval history adapters"},
}

type vs09InvalidFixtureExpectation struct {
	category string
	field    string
}

var expectedVS09InvalidFixtureExpectations = map[string]map[string]vs09InvalidFixtureExpectation{
	"rflsc.approval-command.v2": {
		"authority": {category: "authority", field: "/actorId"}, "reference": {category: "reference", field: "/proposalId"},
		"version": {category: "version", field: "/expectedApprovalVersion"}, "transition": {category: "transition", field: "/expectedState"},
	},
	"rflsc.approval-event.v2": {
		"authority": {category: "authority", field: "/actorId"}, "reference": {category: "reference", field: "/proposalId"},
		"version": {category: "version", field: "/aggregateVersion"}, "transition": {category: "transition", field: "/lifecycleRelation"},
	},
	"rflsc.approval-aggregate.v2": {
		"authority": {category: "authority", field: "/workspaceId"}, "reference": {category: "reference", field: "/proposalId"},
		"version": {category: "version", field: "/history/1/version"}, "transition": {category: "transition", field: "/state"},
	},
	"rflsc.approval-idempotency-result.v1": {
		"authority": {category: "authority", field: "/actorId"}, "reference": {category: "reference", field: "/originalCommand/proposalId"},
		"version": {category: "version", field: "/aggregateVersion"}, "transition": {category: "transition", field: "/state"},
	},
	"rflsc.approval-outbox.v1": {
		"authority": {category: "authority", field: "/workspaceId"}, "reference": {category: "reference", field: "/committedEvent/eventId"},
		"version": {category: "version", field: "/committedEvent/aggregateVersion"}, "transition": {category: "transition", field: "/deliveryState"},
	},
	"rflsc.approval-history.v1": {
		"authority": {category: "authority", field: "/target/workspaceId"}, "reference": {category: "reference", field: "/target/proposalId"},
		"version": {category: "version", field: "/events/0/aggregateVersion"}, "transition": {category: "transition", field: "/events/0/lifecycleRelation"},
	},
}

type vs09Fixture struct {
	class string
	path  string
}

func vs09RegistryFixtures(entry VS09ContractRegistryEntry) []vs09Fixture {
	return []vs09Fixture{
		{class: "valid", path: entry.ValidFixture},
		{class: "authority", path: entry.InvalidAuthorityFixture},
		{class: "reference", path: entry.InvalidReferenceFixture},
		{class: "version", path: entry.InvalidVersionFixture},
		{class: "transition", path: entry.InvalidTransitionFixture},
	}
}

func validateVS09ContractRegistryMetadata(entry VS09ContractRegistryEntry) error {
	if entry.ID == "" || entry.SchemaID == "" || entry.Producer == "" || entry.Consumer == "" || entry.ValidFixture == "" || entry.InvalidAuthorityFixture == "" || entry.InvalidReferenceFixture == "" || entry.InvalidVersionFixture == "" || entry.InvalidTransitionFixture == "" {
		return fmt.Errorf("VS-09 contract registry entry is incomplete: %+v", entry)
	}
	definition, ok := expectedVS09ContractDefinitions[entry.ID]
	if !ok {
		return fmt.Errorf("VS-09 contract registry entry has unexpected ID %q", entry.ID)
	}
	if entry.SchemaID != BaseURL+entry.ID+".schema.json" {
		return fmt.Errorf("%s schema identity mismatch: got %q", entry.ID, entry.SchemaID)
	}
	if entry.Producer != definition.producer || entry.Consumer != definition.consumer {
		return fmt.Errorf("%s producer/consumer boundary mismatch", entry.ID)
	}
	seen := make(map[string]string, 5)
	for _, fixture := range vs09RegistryFixtures(entry) {
		clean := path.Clean(fixture.path)
		if clean != fixture.path || strings.HasPrefix(fixture.path, "/") || strings.HasPrefix(clean, "../") || strings.Contains(clean, "/../") || !strings.HasPrefix(fixture.path, entry.ID+"/") {
			return fmt.Errorf("%s %s fixture path is unsafe or outside its schema directory: %q", entry.ID, fixture.class, fixture.path)
		}
		if previous, duplicate := seen[fixture.path]; duplicate {
			return fmt.Errorf("%s fixture path %q is reused by %s and %s", entry.ID, fixture.path, previous, fixture.class)
		}
		seen[fixture.path] = fixture.class
	}
	return nil
}

// ValidateVS09ContractRegistryEntry validates one valid fixture and four
// semantic invalid fixtures. Each invalid fixture must produce exactly the
// named semantic category and field, not merely any error.
func ValidateVS09ContractRegistryEntry(entry VS09ContractRegistryEntry, valid, invalidAuthority, invalidReference, invalidVersion, invalidTransition []byte) error {
	if err := validateVS09ContractRegistryMetadata(entry); err != nil {
		return err
	}
	validator := ValidatorForVS09Contract(entry.ID)
	if validator == nil {
		return fmt.Errorf("%s has no VS-09 semantic validator", entry.ID)
	}
	if err := validator(valid); err != nil {
		return fmt.Errorf("%s valid fixture %q: %w", entry.ID, entry.ValidFixture, err)
	}
	for _, fixture := range []struct {
		class string
		data  []byte
	}{
		{class: "authority", data: invalidAuthority}, {class: "reference", data: invalidReference}, {class: "version", data: invalidVersion}, {class: "transition", data: invalidTransition},
	} {
		expectation := expectedVS09InvalidFixtureExpectations[entry.ID][fixture.class]
		err := validator(fixture.data)
		if err == nil {
			return fmt.Errorf("%s invalid %s fixture %q unexpectedly passed validation", entry.ID, fixture.class, vs09FixturePath(entry, fixture.class))
		}
		if err := validateVS09SingleLeafDifference(valid, fixture.data, expectation.field); err != nil {
			return fmt.Errorf("%s invalid %s fixture is not a single-leaf mutation: %w", entry.ID, fixture.class, err)
		}
		var semanticErr *VS09ValidationError
		if !errors.As(err, &semanticErr) {
			return fmt.Errorf("%s invalid %s fixture did not produce a semantic validation error: %w", entry.ID, fixture.class, err)
		}
		if semanticErr.Category != expectation.category || semanticErr.Field != expectation.field {
			return fmt.Errorf("%s invalid %s fixture targeted %s %s, got %s %s", entry.ID, fixture.class, expectation.category, expectation.field, semanticErr.Category, semanticErr.Field)
		}
	}
	return nil
}

func validateVS09SingleLeafDifference(valid, invalid []byte, expectedField string) error {
	var validDocument, invalidDocument any
	if err := json.Unmarshal(valid, &validDocument); err != nil {
		return fmt.Errorf("decode valid fixture: %w", err)
	}
	if err := json.Unmarshal(invalid, &invalidDocument); err != nil {
		return fmt.Errorf("decode invalid fixture: %w", err)
	}
	var differences []string
	collectVS09JSONDifferences(validDocument, invalidDocument, "", &differences)
	if len(differences) != 1 || differences[0] != expectedField {
		return fmt.Errorf("got changed leaves %v, want exactly [%s]", differences, expectedField)
	}
	return nil
}

func collectVS09JSONDifferences(left, right any, field string, differences *[]string) {
	if len(*differences) > 1 {
		return
	}
	leftObject, leftIsObject := left.(map[string]any)
	rightObject, rightIsObject := right.(map[string]any)
	if leftIsObject || rightIsObject {
		if !leftIsObject || !rightIsObject {
			*differences = append(*differences, field)
			return
		}
		keys := make(map[string]struct{}, len(leftObject)+len(rightObject))
		for key := range leftObject {
			keys[key] = struct{}{}
		}
		for key := range rightObject {
			keys[key] = struct{}{}
		}
		orderedKeys := make([]string, 0, len(keys))
		for key := range keys {
			orderedKeys = append(orderedKeys, key)
		}
		sort.Strings(orderedKeys)
		for _, key := range orderedKeys {
			leftValue, leftPresent := leftObject[key]
			rightValue, rightPresent := rightObject[key]
			childField := field + "/" + strings.ReplaceAll(strings.ReplaceAll(key, "~", "~0"), "/", "~1")
			if !leftPresent || !rightPresent {
				*differences = append(*differences, childField)
				continue
			}
			collectVS09JSONDifferences(leftValue, rightValue, childField, differences)
		}
		return
	}
	leftArray, leftIsArray := left.([]any)
	rightArray, rightIsArray := right.([]any)
	if leftIsArray || rightIsArray {
		if !leftIsArray || !rightIsArray || len(leftArray) != len(rightArray) {
			*differences = append(*differences, field)
			return
		}
		for index := range leftArray {
			collectVS09JSONDifferences(leftArray[index], rightArray[index], fmt.Sprintf("%s/%d", field, index), differences)
		}
		return
	}
	if !reflect.DeepEqual(left, right) {
		*differences = append(*differences, field)
	}
}

// ValidateVS09ContractRegistry validates every registry entry and all 30
// embedded fixture payloads.
func ValidateVS09ContractRegistry() error {
	if len(VS09ContractRegistry) != len(expectedVS09ContractDefinitions) {
		return fmt.Errorf("VS-09 contract registry count=%d want=%d", len(VS09ContractRegistry), len(expectedVS09ContractDefinitions))
	}
	seenIDs := make(map[string]struct{}, len(VS09ContractRegistry))
	seenSchemas := make(map[string]struct{}, len(VS09ContractRegistry))
	seenFixtures := make(map[string]string, len(VS09ContractRegistry)*5)
	for _, entry := range VS09ContractRegistry {
		if _, duplicate := seenIDs[entry.ID]; duplicate {
			return fmt.Errorf("VS-09 contract registry duplicate ID %q", entry.ID)
		}
		seenIDs[entry.ID] = struct{}{}
		if _, duplicate := seenSchemas[entry.SchemaID]; duplicate {
			return fmt.Errorf("VS-09 contract registry duplicate schema ID %q", entry.SchemaID)
		}
		seenSchemas[entry.SchemaID] = struct{}{}
		if err := validateVS09ContractRegistryMetadata(entry); err != nil {
			return err
		}
		fixtureData := make([][]byte, 0, 5)
		for _, fixture := range vs09RegistryFixtures(entry) {
			if previous, duplicate := seenFixtures[fixture.path]; duplicate {
				return fmt.Errorf("VS-09 fixture path %q is reused by %s and %s", fixture.path, previous, entry.ID+"/"+fixture.class)
			}
			seenFixtures[fixture.path] = entry.ID + "/" + fixture.class
			data, err := fs.ReadFile(schemas.FixturesFS, path.Join("fixtures", fixture.path))
			if err != nil {
				return fmt.Errorf("%s %s fixture %q: %w", entry.ID, fixture.class, fixture.path, err)
			}
			fixtureData = append(fixtureData, data)
		}
		if err := ValidateVS09ContractRegistryEntry(entry, fixtureData[0], fixtureData[1], fixtureData[2], fixtureData[3], fixtureData[4]); err != nil {
			return err
		}
	}
	if len(seenIDs) != len(expectedVS09ContractDefinitions) || len(seenFixtures) != len(expectedVS09ContractDefinitions)*5 {
		return fmt.Errorf("VS-09 contract registry identity or fixture set is incomplete")
	}
	return nil
}

func vs09FixturePath(entry VS09ContractRegistryEntry, class string) string {
	for _, fixture := range vs09RegistryFixtures(entry) {
		if fixture.class == class {
			return fixture.path
		}
	}
	return ""
}
