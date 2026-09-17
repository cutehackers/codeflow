package contractharness

import (
	"encoding/json"
	"testing"
)

func TestValidateDomainOverview_Valid(t *testing.T) {
	valid := map[string]any{
		"schemaId":        BaseURL + "domain-overview.schema.json",
		"schemaVersion":   1,
		"repositoryId":    "repo-1",
		"computedBasisId": "basis-1",
		"generationId":    "gen-1",
		"domains": []map[string]any{
			{
				"domainId":                "dom-1",
				"name":                    "Auth",
				"description":             "User auth",
				"representativeFlowCount": 2,
				"entryPoints":             []string{"AuthController.login"},
			},
		},
		"unmappedModules": []string{},
		"summary": map[string]any{
			"totalDomains":  1,
			"totalFlows":    2,
			"coverageRatio": 0.95,
		},
	}

	data, _ := json.Marshal(valid)
	if err := ValidateDomainOverview(data); err != nil {
		t.Fatalf("expected valid domain-overview to pass: %v", err)
	}
}

func TestValidateRepresentativeFlowCatalog_Invariants(t *testing.T) {
	valid := map[string]any{
		"schemaId":        BaseURL + "representative-flow-catalog.schema.json",
		"schemaVersion":   1,
		"catalogId":       "cat-1",
		"domainId":        "dom-1",
		"computedBasisId": "basis-1",
		"generationId":    "gen-1",
		"flows": []map[string]any{
			{
				"flowId":          "flow-login",
				"title":           "Login flow",
				"entrySymbol":     "AuthController.login",
				"complexityScore": 1.2,
				"keyMutations":    []string{"Session.Active"},
				"groundedMapId":   "map-login",
			},
		},
	}

	data, _ := json.Marshal(valid)
	if err := ValidateRepresentativeFlowCatalog(data); err != nil {
		t.Fatalf("expected valid catalog to pass: %v", err)
	}
}
