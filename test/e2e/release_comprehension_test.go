package e2e_test

import (
	"testing"

	"codeflow/internal/semantic"
	"codeflow/test/fixtures"
)

// TestReleaseComprehensionCorpus proves the evaluator's comprehension evidence
// contract. Its inputs are synthetic and are not production benchmark evidence.
func TestReleaseComprehensionCorpus(t *testing.T) {
	input := fixtures.VS10ReleaseEvaluationInput()
	result, err := semantic.EvaluateReleaseCapabilityWithThresholdDecisions(input, fixtures.VS10ReleaseThresholdDecisions())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, metric := range result.BenchmarkReport.Metrics {
		if metric.Metric != "comprehension" {
			continue
		}
		found = true
		if metric.Category != "quality" || metric.Aggregation != "mean" || len(metric.TraceIDs) == 0 || len(metric.EvidenceRefs) == 0 {
			t.Fatalf("comprehension measure is not separately evidence-bound: %+v", metric)
		}
	}
	if !found {
		t.Fatal("separate comprehension measure is absent")
	}

	input = fixtures.VS10ReleaseEvaluationInput()
	series := input.Reports[0].MetricSeries[:0]
	for _, metric := range input.Reports[0].MetricSeries {
		if metric.Metric != "comprehension" {
			series = append(series, metric)
		}
	}
	input.Reports[0].MetricSeries = series
	// The report's prior artifactRef now mismatches too. Either condition must
	// fail closed without emitting partial metrics.
	result, err = semantic.EvaluateReleaseCapabilityWithThresholdDecisions(input, fixtures.VS10ReleaseThresholdDecisions())
	if err != nil {
		t.Fatal(err)
	}
	if result.BenchmarkReport.Status != "incomplete" || len(result.BenchmarkReport.Metrics) != 0 {
		t.Fatalf("missing comprehension evidence did not fail closed: %+v", result.BenchmarkReport)
	}
	t.Log("synthetic evaluator-contract fixture only; productionBenchmark=false")
}
