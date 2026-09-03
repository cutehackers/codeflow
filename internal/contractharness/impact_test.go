package contractharness

import (
	"encoding/json"
	"testing"
)

func TestValidateChangeImpactGraph_Valid(t *testing.T) {
	valid := map[string]any{
		"schemaId":      BaseURL + "change-impact-graph.schema.json",
		"schemaVersion": 1,
		"impactGraphId": "impact-1",
		"target": map[string]any{
			"symbolId": "AuthService.login",
		},
		"computedBasisId": "basis-1",
		"generationId":    "gen-1",
		"freshness":       "current",
		"directImpact": map[string]any{
			"callers": []map[string]any{
				{
					"symbolPath":   "AuthController.handleLogin",
					"name":         "handleLogin",
					"relationKind": "calls",
				},
			},
			"stateMutations":  []any{},
			"externalEffects": []any{},
			"relatedFlows":    []any{},
			"tests":           []any{},
		},
		"indirectImpact": map[string]any{
			"callers":         []any{},
			"stateMutations":  []any{},
			"externalEffects": []any{},
			"relatedFlows":    []any{},
			"tests":           []any{},
			"maxDepth":        2,
			"totalNodeCount":  1,
			"bounded":         true,
		},
		"unresolvedBoundaries": []map[string]any{
			{
				"boundaryType": "unresolved_dynamic_caller",
				"target":       "DynamicInvoker.dispatch",
				"description":  "reflection boundary",
			},
		},
		"additionalExplorationAvailable": true,
		"unknownCount":                   1,
	}

	data, err := json.Marshal(valid)
	if err != nil {
		t.Fatal(err)
	}

	if err := ValidateChangeImpactGraph(data); err != nil {
		t.Fatalf("expected valid change impact graph to pass: %v", err)
	}
}

func TestValidateChangeImpactGraph_InvariantViolations(t *testing.T) {
	// Exceeds max depth
	invalidDepth := map[string]any{
		"schemaId":      BaseURL + "change-impact-graph.schema.json",
		"schemaVersion": 1,
		"impactGraphId": "impact-invalid-depth",
		"target": map[string]any{
			"symbolId": "AuthService.login",
		},
		"computedBasisId": "basis-1",
		"generationId":    "gen-1",
		"freshness":       "current",
		"directImpact": map[string]any{
			"callers":         []any{},
			"stateMutations":  []any{},
			"externalEffects": []any{},
			"relatedFlows":    []any{},
			"tests":           []any{},
		},
		"indirectImpact": map[string]any{
			"callers":         []any{},
			"stateMutations":  []any{},
			"externalEffects": []any{},
			"relatedFlows":    []any{},
			"tests":           []any{},
			"maxDepth":        6, // Violates maxDepth <= 5
			"totalNodeCount":  1,
			"bounded":         true,
		},
		"unresolvedBoundaries":           []any{},
		"additionalExplorationAvailable": false,
		"unknownCount":                   0,
	}

	data, _ := json.Marshal(invalidDepth)
	if err := ValidateChangeImpactGraph(data); err == nil {
		t.Fatal("expected depth > 5 to be rejected")
	}
}
