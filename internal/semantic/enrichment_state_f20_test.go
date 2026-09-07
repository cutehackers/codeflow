package semantic

import (
	"encoding/json"
	"io/fs"
	"strings"
	"testing"

	"codeflow/internal/contractharness"
	"codeflow/schemas"
)

func TestEnrichmentStateAvailableSchemaFailClosed(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{name: "missing state pack digest", mutate: func(doc map[string]any) { delete(doc, "packDigest") }},
		{name: "missing proposal identity", mutate: func(doc map[string]any) { delete(doc, "proposalId") }},
		{name: "unsupported capability", mutate: func(doc map[string]any) { doc["capability"].(map[string]any)["status"] = "unsupported" }},
		{name: "missing capability model identity", mutate: func(doc map[string]any) { delete(doc["capability"].(map[string]any), "modelId") }},
		{name: "schema unconstrained", mutate: func(doc map[string]any) { doc["capability"].(map[string]any)["schemaConstrained"] = false }},
		{name: "cancellation unavailable", mutate: func(doc map[string]any) { doc["capability"].(map[string]any)["cancellation"] = false }},
		{name: "capability unmeasured", mutate: func(doc map[string]any) { doc["capability"].(map[string]any)["measured"] = false }},
		{name: "zero request limit", mutate: func(doc map[string]any) { doc["capability"].(map[string]any)["maxRequestBytes"] = 0 }},
		{name: "zero response limit", mutate: func(doc map[string]any) { doc["capability"].(map[string]any)["maxResponseBytes"] = 0 }},
		{name: "raw source delivery", mutate: func(doc map[string]any) { doc["isolation"].(map[string]any)["sourceDelivery"] = "raw_source" }},
		{name: "mounted source", mutate: func(doc map[string]any) { doc["isolation"].(map[string]any)["sourceMount"] = "mounted" }},
		{name: "shared working directory", mutate: func(doc map[string]any) { doc["isolation"].(map[string]any)["workingDirectoryMode"] = "shared" }},
		{name: "non-disposable working directory", mutate: func(doc map[string]any) { doc["isolation"].(map[string]any)["disposable"] = false }},
		{name: "unsupported isolation capability", mutate: func(doc map[string]any) { doc["isolation"].(map[string]any)["capabilityStatus"] = "unsupported" }},
		{name: "invalid terminal status", mutate: func(doc map[string]any) { doc["isolation"].(map[string]any)["terminalStatus"] = "crashed" }},
		{name: "cleanup not verified", mutate: func(doc map[string]any) { doc["isolation"].(map[string]any)["cleanupVerified"] = false }},
		{name: "repository read allowed", mutate: func(doc map[string]any) { doc["isolation"].(map[string]any)["repositoryReadAttempt"] = "allowed" }},
		{name: "repository write allowed", mutate: func(doc map[string]any) { doc["isolation"].(map[string]any)["repositoryWriteAttempt"] = "allowed" }},
		{name: "disposable write allowed", mutate: func(doc map[string]any) { doc["isolation"].(map[string]any)["disposableWriteAttempt"] = "allowed" }},
		{name: "sentinel changed", mutate: func(doc map[string]any) { doc["isolation"].(map[string]any)["sentinelUnchanged"] = false }},
		{name: "network allowed", mutate: func(doc map[string]any) { doc["isolation"].(map[string]any)["networkAttempt"] = "allowed" }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc := readF20AvailableFixture(t)
			tc.mutate(doc)
			data, err := json.Marshal(doc)
			if err != nil {
				t.Fatal(err)
			}
			if err := contractharness.Validate(EnrichmentStateV2SchemaID, data); err == nil {
				t.Fatalf("available state mutation %q was accepted", tc.name)
			}
		})
	}
}

func readF20AvailableFixture(t *testing.T) map[string]any {
	t.Helper()
	raw, err := fs.ReadFile(schemas.FixturesFS, "fixtures/rflsc.enrichment-state.v2/valid/available.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

func TestEnrichmentStateTypedValidatorBindsAvailableCrossFieldIdentity(t *testing.T) {
	raw, err := fs.ReadFile(schemas.FixturesFS, "fixtures/rflsc.enrichment-state.v2/valid/available.json")
	if err != nil {
		t.Fatal(err)
	}
	var valid EnrichmentState
	if err := json.Unmarshal(raw, &valid); err != nil {
		t.Fatal(err)
	}
	if err := ValidateEnrichmentState(valid); err != nil {
		t.Fatalf("known-good available state rejected: %v", err)
	}

	for _, tc := range []struct {
		name   string
		mutate func(*EnrichmentState)
	}{
		{name: "state and isolation pack digest mismatch", mutate: func(state *EnrichmentState) { state.Isolation.PackDigest = strings.Repeat("1", 64) }},
		{name: "received pack digest mismatch", mutate: func(state *EnrichmentState) { state.Isolation.ReceivedPackDigest = strings.Repeat("1", 64) }},
		{name: "isolation backend mismatch", mutate: func(state *EnrichmentState) { state.Isolation.IsolationBackend = "different-backend" }},
		{name: "capability probe outcome mismatch", mutate: func(state *EnrichmentState) { state.Capability.IsolationProbe.RepositoryReadAttempt = "allowed" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mutated := valid
			if valid.Capability.IsolationProbe != nil {
				probe := *valid.Capability.IsolationProbe
				mutated.Capability.IsolationProbe = &probe
			}
			tc.mutate(&mutated)
			if err := ValidateEnrichmentState(mutated); err == nil {
				t.Fatalf("typed validator accepted cross-field mutation %q", tc.name)
			}
		})
	}
}
