package contractharness_test

import (
	"encoding/json"
	"io/fs"
	"path/filepath"
	"testing"

	harness "codeflow/internal/collector/contractharness"
	"codeflow/schemas"
)

func TestVS07ContractRegistryHasExecutableV2Entries(t *testing.T) {
	if err := harness.EnsureAllCompiled(); err != nil {
		t.Fatalf("compile VS-07 contract registry: %v", err)
	}
	expected := []string{
		"rflsc.onboarding-query.v2",
		"rflsc.domain-overview.v2",
		"rflsc.domain-candidate.v2",
		"rflsc.representative-flow-catalog.v2",
	}
	if len(harness.VS07ContractRegistry) != len(expected) {
		t.Fatalf("VS-07 contract registry count=%d want=%d", len(harness.VS07ContractRegistry), len(expected))
	}
	seen := map[string]bool{}
	for index, entry := range harness.VS07ContractRegistry {
		if entry.ID != expected[index] || seen[entry.ID] {
			t.Fatalf("unexpected VS-07 registry order or duplicate at %d: %+v", index, entry)
		}
		seen[entry.ID] = true
		valid := readVS07ContractFixture(t, entry.ValidFixture)
		repository := readVS07ContractFixture(t, entry.InvalidRepositoryFixture)
		evidence := readVS07ContractFixture(t, entry.InvalidEvidenceFixture)
		ordering := readVS07ContractFixture(t, entry.InvalidOrderingFixture)
		if err := harness.ValidateVS07ContractRegistryEntry(entry, valid, repository, evidence, ordering); err != nil {
			t.Fatalf("VS-07 registry entry %s failed: %v", entry.ID, err)
		}
	}
}

func TestVS07V2CrossFieldOrderingValidatorsRejectReorderedValues(t *testing.T) {
	t.Run("query budget", func(t *testing.T) {
		doc := readVS07ContractJSON(t, "rflsc.onboarding-query.v2/valid/basic.json")
		doc["displayBudget"] = map[string]any{"targetMin": 8, "targetMax": 2, "enforcement": "soft"}
		if err := harness.ValidateOnboardingQueryV2(marshalVS07ContractJSON(t, doc)); err == nil {
			t.Fatal("reversed query display budget was accepted")
		}
	})

	t.Run("candidate evidence refs", func(t *testing.T) {
		doc := readVS07ContractJSON(t, "rflsc.domain-candidate.v2/valid/candidate.json")
		doc["evidenceRefs"] = []any{"ev-z", "ev-a"}
		if err := harness.ValidateDomainCandidateV2(marshalVS07ContractJSON(t, doc)); err == nil {
			t.Fatal("noncanonical candidate Evidence refs were accepted")
		}
	})

	t.Run("overview domains", func(t *testing.T) {
		first := readVS07ContractJSON(t, "rflsc.domain-candidate.v2/valid/candidate.json")
		second := readVS07ContractJSON(t, "rflsc.domain-candidate.v2/valid/candidate.json")
		first["domainId"], first["name"] = "domain-z", "Zeta"
		second["domainId"], second["name"] = "domain-a", "Alpha"
		doc := readVS07ContractJSON(t, "rflsc.domain-overview.v2/valid/overview.json")
		doc["domains"] = []any{first, second}
		doc["summary"] = map[string]any{"totalDomains": 2, "totalFlows": 2, "coverageRatio": 1}
		if err := harness.ValidateDomainOverviewV2(marshalVS07ContractJSON(t, doc)); err == nil {
			t.Fatal("reversed overview domains were accepted")
		}
	})

	t.Run("catalog flows", func(t *testing.T) {
		low := readVS07ContractJSON(t, "rflsc.representative-flow-catalog.v2/valid/catalog.json")
		flows := low["flows"].([]any)
		first := flows[0].(map[string]any)
		second := map[string]any{}
		for key, value := range first {
			second[key] = value
		}
		second["flowId"], second["complexityScore"] = "flow-high", 4.0
		first["flowId"], first["complexityScore"] = "flow-low", 1.0
		low["flows"] = []any{first, second}
		if err := harness.ValidateRepresentativeFlowCatalogV2(marshalVS07ContractJSON(t, low)); err == nil {
			t.Fatal("ascending catalog flow scores were accepted")
		}
	})
}

func readVS07ContractFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := fs.ReadFile(schemas.FixturesFS, filepath.ToSlash(filepath.Join("fixtures", name)))
	if err != nil {
		t.Fatalf("read VS-07 contract fixture %s: %v", name, err)
	}
	return data
}

func readVS07ContractJSON(t *testing.T, name string) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(readVS07ContractFixture(t, name), &doc); err != nil {
		t.Fatalf("decode VS-07 contract fixture %s: %v", name, err)
	}
	return doc
}

func marshalVS07ContractJSON(t *testing.T, doc map[string]any) []byte {
	t.Helper()
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("encode VS-07 contract fixture: %v", err)
	}
	return data
}
