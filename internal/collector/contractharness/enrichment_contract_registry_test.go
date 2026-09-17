package contractharness_test

import (
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	harness "codeflow/internal/collector/contractharness"
	"codeflow/schemas"
)

func TestVS08ContractRegistry(t *testing.T) {
	if err := harness.EnsureAllCompiled(); err != nil {
		t.Fatalf("compile VS-08 contract registry: %v", err)
	}
	expected := []struct {
		id       string
		producer string
		consumer string
	}{
		{id: "rflsc.evidence-pack.v2", producer: "semantic.BuildEvidencePackV2", consumer: "model host and browser egress"},
		{id: "rflsc.model-host-request.v2", producer: "semantic.RunSemanticEnrichment", consumer: "protocol.ModelHost"},
		{id: "rflsc.model-host-response.v2", producer: "protocol.ModelHost", consumer: "semantic proposal validator"},
		{id: "rflsc.semantic-proposal.v2", producer: "semantic proposal validator", consumer: "FlowView and MCP inferred projection"},
		{id: "rflsc.enrichment-state.v2", producer: "semantic.RunSemanticEnrichment", consumer: "FlowView and MCP status"},
		{id: "rflsc.model-activation-disclosure.v1", producer: "semantic.NewModelActivationDisclosure", consumer: "activation choice UI"},
	}
	if len(harness.VS08ContractRegistry) != len(expected) {
		t.Fatalf("VS-08 contract registry count=%d want=%d", len(harness.VS08ContractRegistry), len(expected))
	}
	seenIDs := map[string]bool{}
	seenSchemas := map[string]bool{}
	seenFixtures := map[string]bool{}
	for index, entry := range harness.VS08ContractRegistry {
		want := expected[index]
		if entry.ID != want.id || entry.Producer != want.producer || entry.Consumer != want.consumer {
			t.Fatalf("unexpected VS-08 registry boundary at %d: %+v", index, entry)
		}
		if seenIDs[entry.ID] || seenSchemas[entry.SchemaID] {
			t.Fatalf("duplicate VS-08 registry identity at %d: %+v", index, entry)
		}
		seenIDs[entry.ID], seenSchemas[entry.SchemaID] = true, true
		for _, fixture := range []string{entry.ValidFixture, entry.InvalidAuthorityFixture, entry.InvalidReferenceFixture, entry.InvalidBoundFixture} {
			if seenFixtures[fixture] {
				t.Fatalf("duplicate VS-08 fixture path %q", fixture)
			}
			seenFixtures[fixture] = true
		}
		valid := readVS08Fixture(t, entry.ValidFixture)
		authority := readVS08Fixture(t, entry.InvalidAuthorityFixture)
		reference := readVS08Fixture(t, entry.InvalidReferenceFixture)
		bound := readVS08Fixture(t, entry.InvalidBoundFixture)
		if err := harness.ValidateVS08ContractRegistryEntry(entry, valid, authority, reference, bound); err != nil {
			t.Fatalf("VS-08 registry entry %s failed: %v", entry.ID, err)
		}
	}
	if err := harness.ValidateVS08ContractRegistry(); err != nil {
		t.Fatalf("VS-08 embedded registry validation failed: %v", err)
	}
}

func TestVS08ContractRegistryValidatesFixtureBytesAndClasses(t *testing.T) {
	for _, entry := range harness.VS08ContractRegistry {
		entry := entry
		valid := readVS08Fixture(t, entry.ValidFixture)
		authority := readVS08Fixture(t, entry.InvalidAuthorityFixture)
		reference := readVS08Fixture(t, entry.InvalidReferenceFixture)
		bound := readVS08Fixture(t, entry.InvalidBoundFixture)

		t.Run(entry.ID+"/invalid-valid-fixture", func(t *testing.T) {
			invalidValid := []byte(`{"not":"a valid fixture"}`)
			if err := harness.ValidateVS08ContractRegistryEntry(entry, invalidValid, authority, reference, bound); err == nil || !strings.Contains(err.Error(), "valid fixture") {
				t.Fatalf("registry accepted invalid valid-fixture bytes: %v", err)
			}
		})

		for _, test := range []struct {
			name string
			args func() ([]byte, []byte, []byte)
		}{
			{name: "authority", args: func() ([]byte, []byte, []byte) { return valid, reference, bound }},
			{name: "reference", args: func() ([]byte, []byte, []byte) { return authority, valid, bound }},
			{name: "bound", args: func() ([]byte, []byte, []byte) { return authority, reference, valid }},
		} {
			test := test
			t.Run(entry.ID+"/"+test.name+"-repaired", func(t *testing.T) {
				invalidAuthority, invalidReference, invalidBound := test.args()
				err := harness.ValidateVS08ContractRegistryEntry(entry, valid, invalidAuthority, invalidReference, invalidBound)
				if err == nil || !strings.Contains(err.Error(), "invalid "+test.name) {
					t.Fatalf("registry accepted repaired %s fixture bytes: %v", test.name, err)
				}
			})
		}
	}
}

func readVS08Fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := fs.ReadFile(schemas.FixturesFS, filepath.ToSlash(filepath.Join("fixtures", name)))
	if err != nil {
		t.Fatalf("read VS-08 fixture %s: %v", name, err)
	}
	return data
}
