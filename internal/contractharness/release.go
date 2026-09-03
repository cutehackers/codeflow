package contractharness

import (
	"encoding/json"
	"fmt"
)

// ValidateReleaseBenchmarkReport validates raw JSON against release-benchmark-report.schema.json
// and enforces release readiness gate invariants (VS-10, SID-C2, Raw §16–§18, §10).
func ValidateReleaseBenchmarkReport(data []byte) error {
	schemaID := BaseURL + "release-benchmark-report.schema.json"
	if err := Validate(schemaID, data); err != nil {
		return fmt.Errorf("release-benchmark-report schema violation: %w", err)
	}

	var rep struct {
		ReportID string `json:"reportId"`
		Metrics  struct {
			ContractViolations int `json:"contractViolations"`
			RegressionFailures int `json:"regressionFailures"`
		} `json:"metrics"`
		Gates struct {
			LatencyGatePassed     bool `json:"latencyGatePassed"`
			PrecisionGatePassed   bool `json:"precisionGatePassed"`
			ZeroViolationsPassed  bool `json:"zeroViolationsPassed"`
			ZeroRegressionsPassed bool `json:"zeroRegressionsPassed"`
		} `json:"gates"`
		ReleaseReady bool `json:"releaseReady"`
	}
	if err := json.Unmarshal(data, &rep); err != nil {
		return fmt.Errorf("parse release-benchmark-report JSON: %w", err)
	}

	// Epistemic release gate invariant: releaseReady can only be true if all gates pass
	if rep.ReleaseReady {
		if !rep.Gates.LatencyGatePassed || !rep.Gates.PrecisionGatePassed ||
			!rep.Gates.ZeroViolationsPassed || !rep.Gates.ZeroRegressionsPassed ||
			rep.Metrics.ContractViolations > 0 || rep.Metrics.RegressionFailures > 0 {
			return fmt.Errorf("release-benchmark-report %q invalid: releaseReady is true but gates failed or regressions present", rep.ReportID)
		}
	}

	return nil
}

// ValidateSLMCapabilityState validates raw JSON against slm-capability-state.schema.json
// and enforces SLM capability invariants (VS-10, SID-C2, Raw §16–§18, §10).
func ValidateSLMCapabilityState(data []byte) error {
	schemaID := BaseURL + "slm-capability-state.schema.json"
	if err := Validate(schemaID, data); err != nil {
		return fmt.Errorf("slm-capability-state schema violation: %w", err)
	}

	var state struct {
		ModelID      string `json:"modelId"`
		FallbackTier string `json:"fallbackTier"`
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("parse slm-capability-state JSON: %w", err)
	}

	switch state.FallbackTier {
	case "local_slm", "cloud_llm", "ast_deterministic":
		// valid
	default:
		return fmt.Errorf("invalid fallbackTier %q for model %q", state.FallbackTier, state.ModelID)
	}

	return nil
}
