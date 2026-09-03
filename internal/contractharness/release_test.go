package contractharness

import (
	"encoding/json"
	"testing"
)

func TestValidateReleaseBenchmarkReport_GateInvariants(t *testing.T) {
	// 1. Valid passing report
	valid := map[string]any{
		"schemaId":      BaseURL + "release-benchmark-report.schema.json",
		"schemaVersion": 1,
		"reportId":      "rep-1",
		"targetVersion": "v0.9.0-rc1",
		"evaluatedAt":   "2026-09-03T10:00:00Z",
		"metrics": map[string]any{
			"latencyP95Ms":       250,
			"precision":          0.92,
			"recall":             0.90,
			"contractViolations": 0,
			"regressionFailures": 0,
		},
		"gates": map[string]any{
			"latencyGatePassed":     true,
			"precisionGatePassed":   true,
			"zeroViolationsPassed":  true,
			"zeroRegressionsPassed": true,
		},
		"releaseReady": true,
		"summary":      "Ready",
	}
	data, _ := json.Marshal(valid)
	if err := ValidateReleaseBenchmarkReport(data); err != nil {
		t.Fatalf("expected valid report to pass: %v", err)
	}

	// 2. Invariant violation: releaseReady=true but regressionFailures > 0
	badMetrics := map[string]any{
		"schemaId":      BaseURL + "release-benchmark-report.schema.json",
		"schemaVersion": 1,
		"reportId":      "rep-bad",
		"targetVersion": "v0.9.0-rc1",
		"evaluatedAt":   "2026-09-03T10:00:00Z",
		"metrics": map[string]any{
			"latencyP95Ms":       250,
			"precision":          0.92,
			"recall":             0.90,
			"contractViolations": 0,
			"regressionFailures": 1, // violation!
		},
		"gates": map[string]any{
			"latencyGatePassed":     true,
			"precisionGatePassed":   true,
			"zeroViolationsPassed":  true,
			"zeroRegressionsPassed": true,
		},
		"releaseReady": true,
		"summary":      "Invalid ready state",
	}
	dataBad, _ := json.Marshal(badMetrics)
	if err := ValidateReleaseBenchmarkReport(dataBad); err == nil {
		t.Fatal("expected report with regressionFailures > 0 and releaseReady=true to fail invariant check")
	}
}

func TestValidateSLMCapabilityState_Valid(t *testing.T) {
	valid := map[string]any{
		"schemaId":      BaseURL + "slm-capability-state.schema.json",
		"schemaVersion": 1,
		"modelId":       "test-slm",
		"modelVersion":  "1.0",
		"evaluatedAt":   "2026-09-03T10:00:00Z",
		"capabilities": map[string]any{
			"entryResolution":         "full",
			"sliceFusion":             "full",
			"stateDeltaInference":     "partial",
			"businessRuleExtraction":  "full",
			"indirectImpactTraversal": "partial",
			"failureBacktrack":        "full",
		},
		"fallbackTier": "local_slm",
	}
	data, _ := json.Marshal(valid)
	if err := ValidateSLMCapabilityState(data); err != nil {
		t.Fatalf("expected valid SLM capability state to pass: %v", err)
	}
}
