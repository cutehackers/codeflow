package contractharness

import (
	"encoding/json"
	"testing"
)

func TestValidateFailurePathTrace_Valid(t *testing.T) {
	valid := map[string]any{
		"schemaId":        BaseURL + "failure-path-trace.schema.json",
		"schemaVersion":   1,
		"traceId":         "trace-valid-1",
		"mode":            "debug",
		"computedBasisId": "basis-1",
		"generationId":    "gen-1",
		"failureTarget": map[string]any{
			"error": "Http500InternalServerError",
		},
		"nodes": []map[string]any{
			{
				"nodeId":       "n-1",
				"symbolPath":   "Api.handle",
				"role":         "throw",
				"status":       "runtime_observed",
				"evidenceRefs": []string{"ev-1"},
			},
		},
		"relationships": []any{},
		"unknownCount":  0,
		"hasConflicts":  false,
		"summary": map[string]any{
			"description":        "api error",
			"lastConfirmedState": "State.Ready",
		},
	}

	data, _ := json.Marshal(valid)
	if err := ValidateFailurePathTrace(data); err != nil {
		t.Fatalf("expected valid failure trace to pass: %v", err)
	}
}

func TestValidateFailurePathTrace_ConflictInvariant(t *testing.T) {
	invalidConflict := map[string]any{
		"schemaId":        BaseURL + "failure-path-trace.schema.json",
		"schemaVersion":   1,
		"traceId":         "trace-conflict-1",
		"mode":            "debug",
		"computedBasisId": "basis-1",
		"generationId":    "gen-1",
		"failureTarget": map[string]any{
			"error": "DatabaseTimeout",
		},
		"nodes": []map[string]any{
			{
				"nodeId":       "n-1",
				"symbolPath":   "Db.query",
				"role":         "throw",
				"status":       "conflicting",
				"evidenceRefs": []string{"ev-1"},
			},
		},
		"relationships": []any{},
		"unknownCount":  0,
		"hasConflicts":  false, // Invariant violation: must be true when conflicting node exists
		"summary": map[string]any{
			"description":        "conflict test",
			"lastConfirmedState": "State.Ready",
		},
	}

	data, _ := json.Marshal(invalidConflict)
	if err := ValidateFailurePathTrace(data); err == nil {
		t.Fatal("expected conflict mismatch to be rejected")
	}
}

func TestValidateRuntimeObservation_TrustedLocalApproval(t *testing.T) {
	// Missing approval when isolation is trusted_local
	invalid := map[string]any{
		"schemaId":       BaseURL + "runtime-observation.schema.json",
		"schemaVersion":  1,
		"observationId":  "obs-1",
		"scenario":       "local_exec",
		"environment":    "darwin",
		"isolationLevel": "trusted_local",
		"observedAt":     "2026-09-03T17:00:00Z",
		"traceCoverage": map[string]any{
			"spansCovered": 1,
			"totalSpans":   1,
			"ratio":        1.0,
		},
	}
	data, _ := json.Marshal(invalid)
	if err := ValidateRuntimeObservation(data); err == nil {
		t.Fatal("expected trusted_local without approval to fail")
	}
}
