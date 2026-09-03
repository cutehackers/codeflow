package semantic

import (
	"testing"
)

func TestEvaluateReleaseBenchmark_MissingPreconditions(t *testing.T) {
	_, err := EvaluateReleaseBenchmark("", BenchmarkOptions{})
	if err == nil {
		t.Fatal("expected error when version is empty")
	}
	if err.Error() != "missing_precondition: targetVersion is required" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestEvaluateReleaseBenchmark_PassingGates(t *testing.T) {
	opts := BenchmarkOptions{
		LatencySamplesMs: []float64{100, 150, 200, 250, 300},
		Precision:        0.92,
		Recall:           0.89,
	}

	report, err := EvaluateReleaseBenchmark("v0.9.0-rc1", opts)
	if err != nil {
		t.Fatalf("EvaluateReleaseBenchmark failed: %v", err)
	}

	// VS10-A1, A3
	if !report.ReleaseReady {
		t.Errorf("expected releaseReady true, got false")
	}
	if !report.Gates.LatencyGatePassed || !report.Gates.PrecisionGatePassed ||
		!report.Gates.ZeroViolationsPassed || !report.Gates.ZeroRegressionsPassed {
		t.Errorf("expected all gates passed: %+v", report.Gates)
	}
	if report.Metrics.RegressionFailures != 0 {
		t.Errorf("expected 0 regressions, got %d", report.Metrics.RegressionFailures)
	}
}

func TestEvaluateReleaseBenchmark_FailingRegressionGate(t *testing.T) {
	opts := BenchmarkOptions{
		LatencySamplesMs:   []float64{200, 250},
		Precision:          0.90,
		Recall:             0.88,
		RegressionFailures: 2, // 2 regressions detected
	}

	report, err := EvaluateReleaseBenchmark("v0.9.0-rc2", opts)
	if err != nil {
		t.Fatalf("EvaluateReleaseBenchmark failed: %v", err)
	}

	// VS10-A3: Gate must fail and releaseReady must be false
	if report.ReleaseReady {
		t.Errorf("expected releaseReady false due to regressions")
	}
	if report.Gates.ZeroRegressionsPassed {
		t.Errorf("expected zeroRegressionsPassed false")
	}
}

func TestGetSLMCapabilityState_FallbackTier(t *testing.T) {
	// Full capability model
	stateFull := GetSLMCapabilityState("qwen2.5-coder-32b", "2024-11")
	if stateFull.FallbackTier != "local_slm" {
		t.Errorf("expected fallbackTier local_slm, got %s", stateFull.FallbackTier)
	}

	// Small / limited model falling back
	stateLight := GetSLMCapabilityState("nano-slm-0.5b", "1.0")
	if stateLight.FallbackTier != "cloud_llm" && stateLight.FallbackTier != "ast_deterministic" {
		t.Errorf("expected fallback tier, got %s", stateLight.FallbackTier)
	}
}
