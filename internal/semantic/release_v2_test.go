package semantic

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codeflow/internal/evidence"
)

func TestRFLSCR2VS10_A01(t *testing.T) {
	recordReleaseCriterionEvidence(t, "VS10-A1", nil)
	tests := []struct {
		name   string
		mutate func(*ReleaseEvaluationInput)
	}{
		{name: "profile", mutate: func(in *ReleaseEvaluationInput) { in.Profile = nil }},
		{name: "corpus", mutate: func(in *ReleaseEvaluationInput) { in.Corpus = nil }},
		{name: "executed report", mutate: func(in *ReleaseEvaluationInput) { in.Reports = nil }},
		{name: "approved thresholds", mutate: func(in *ReleaseEvaluationInput) { in.Thresholds = nil }},
		{name: "child evidence", mutate: func(in *ReleaseEvaluationInput) { in.ChildEvidence = nil }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := completeReleaseEvaluationInput()
			test.mutate(&input)
			sealReleaseInput(&input)
			result, err := EvaluateReleaseCapability(input)
			if err != nil {
				t.Fatalf("missing evidence must produce an inspectable incomplete result: %v", err)
			}
			if result == nil || result.BenchmarkReport == nil || result.CapabilityMatrix == nil {
				t.Fatal("missing evidence did not produce both public reports")
			}
			if result.BenchmarkReport.Status != "incomplete" || result.BenchmarkReport.ReleaseReady || result.CapabilityMatrix.ReleaseReady {
				t.Fatalf("missing %s did not fail closed: %+v", test.name, result)
			}
			if len(result.BenchmarkReport.Metrics) != 0 || len(result.BenchmarkReport.Gates) != 0 {
				t.Fatalf("missing %s synthesized metrics or gates: %+v", test.name, result.BenchmarkReport)
			}
		})
	}
}

func TestRFLSCR2VS10_A02(t *testing.T) {
	recordReleaseCriterionEvidence(t, "VS10-A2", func(input *ReleaseEvaluationInput) { input.Profile.OS = "criterion-tamper" })
	tests := []struct {
		name   string
		mutate func(*ReleaseEvaluationInput)
	}{
		{
			name: "metric profile",
			mutate: func(in *ReleaseEvaluationInput) {
				in.Reports[0].MetricSeries[0].Samples[0].ProfileID = "profile-other"
			},
		},
		{
			name: "metric trace",
			mutate: func(in *ReleaseEvaluationInput) {
				in.Reports[0].MetricSeries[0].Samples[0].TraceID = ""
			},
		},
		{
			name: "metric scenario",
			mutate: func(in *ReleaseEvaluationInput) {
				in.Reports[0].MetricSeries[0].Samples[0].ScenarioID = "scenario-undeclared"
			},
		},
		{
			name: "metric toolchain",
			mutate: func(in *ReleaseEvaluationInput) {
				in.Reports[0].MetricSeries[0].Samples[0].ToolchainID = "toolchain-undeclared"
			},
		},
		{
			name: "mutable evidence reference",
			mutate: func(in *ReleaseEvaluationInput) {
				in.Reports[0].MetricSeries[0].Samples[0].EvidenceRef = "report/latest.json"
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := completeReleaseEvaluationInput()
			test.mutate(&input)
			sealReleaseInput(&input)
			result, err := EvaluateReleaseCapability(input)
			if err != nil {
				t.Fatalf("invalid evidence identity must produce an inspectable incomplete result: %v", err)
			}
			if result.BenchmarkReport.Status != "incomplete" || result.BenchmarkReport.ReleaseReady {
				t.Fatalf("invalid %s did not fail closed: %+v", test.name, result.BenchmarkReport)
			}
			if len(result.BenchmarkReport.Metrics) != 0 || len(result.BenchmarkReport.Gates) != 0 {
				t.Fatalf("invalid %s leaked unbound metrics or gates", test.name)
			}
		})
	}
}

func TestRFLSCR2VS10_RejectsExecutionCapabilitiesOutsideDeclaredScenarioScope(t *testing.T) {
	input := completeReleaseEvaluationInput()
	input.Reports[0].ScenarioResults[0].CapabilityIDs = append(input.Reports[0].ScenarioResults[0].CapabilityIDs, "undeclared-capability")
	sealReleaseInput(&input)

	result, err := EvaluateReleaseCapability(input)
	if err != nil {
		t.Fatal(err)
	}
	if result.BenchmarkReport.Status != "incomplete" || !releaseStringContainsText(result.BenchmarkReport.Reasons, "capability scope") {
		t.Fatalf("out-of-scope execution capability was accepted: %+v", result.BenchmarkReport)
	}
}

func TestRFLSCR2VS10_RejectsScopedIdentityBypasses(t *testing.T) {
	tests := []struct {
		name       string
		wantReason string
		mutate     func(*ReleaseEvaluationInput)
	}{
		{
			name: "corpus capability outside profile",
			mutate: func(input *ReleaseEvaluationInput) {
				input.Corpus.Scenarios[0].CapabilityIDs = append(input.Corpus.Scenarios[0].CapabilityIDs, "undeclared-capability")
			},
			wantReason: "scenario names a capability outside the declared profile scope",
		},
		{
			name: "result and sample toolchain absent from report declaration",
			mutate: func(input *ReleaseEvaluationInput) {
				input.Profile.Toolchains = append(input.Profile.Toolchains, ReleaseToolchain{ToolchainID: "go-other", Name: "go", Version: "other", ArtifactRef: releaseTestRef("go-other")})
				input.Reports[0].ScenarioResults[0].ToolchainID = "go-other"
				for index := range input.Reports[0].MetricSeries {
					input.Reports[0].MetricSeries[index].Samples[0].ToolchainID = "go-other"
				}
			},
			wantReason: "outside the report toolchain scope",
		},
		{
			name: "passing execution carries failure data",
			mutate: func(input *ReleaseEvaluationInput) {
				input.Reports[0].ScenarioResults[0].FailureReason = "contradiction"
				input.Reports[0].ScenarioResults[0].Recovery = "contradiction"
			},
			wantReason: "passing scenario result contains failure data",
		},
		{
			name: "trace identity conflicts across reports",
			mutate: func(input *ReleaseEvaluationInput) {
				second := cloneReleaseReport(input.Reports[0])
				second.ReportID = "report-vs10-second"
				second.ScenarioResults[0].TraceRef = releaseTestRef("conflicting-trace-ref")
				for index := range second.MetricSeries {
					second.MetricSeries[index].Samples[0].TraceRef = second.ScenarioResults[0].TraceRef
				}
				input.Reports = append(input.Reports, second)
			},
			wantReason: "reuses a trace identity across different executions",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := completeReleaseEvaluationInput()
			test.mutate(&input)
			sealReleaseInput(&input)
			result, err := EvaluateReleaseCapability(input)
			if err != nil {
				t.Fatal(err)
			}
			if result.BenchmarkReport.Status != "incomplete" || !releaseStringContainsText(result.BenchmarkReport.Reasons, test.wantReason) {
				t.Fatalf("scoped identity bypass was accepted or misreported: %+v", result.BenchmarkReport)
			}
		})
	}
}

func TestRFLSCR2VS10_RejectsTopLevelEvidenceTampering(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ReleaseEvaluationInput)
	}{
		{name: "profile", mutate: func(in *ReleaseEvaluationInput) { in.Profile.OS = "linux" }},
		{name: "corpus", mutate: func(in *ReleaseEvaluationInput) { in.Corpus.CorpusVersion = "tampered" }},
		{name: "execution report", mutate: func(in *ReleaseEvaluationInput) { in.Reports[0].Command = "unobserved command" }},
		{name: "thresholds", mutate: func(in *ReleaseEvaluationInput) { in.Thresholds.Thresholds[0].DecisionRef = "decision:unapproved" }},
		{name: "child evidence", mutate: func(in *ReleaseEvaluationInput) { in.ChildEvidence[0].ContractRef = "contract:other" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := completeReleaseEvaluationInput()
			test.mutate(&input)
			result, err := EvaluateReleaseCapability(input)
			if err != nil {
				t.Fatalf("tampering must produce an inspectable incomplete result: %v", err)
			}
			if result.BenchmarkReport.Status != "incomplete" || result.BenchmarkReport.ReleaseReady || len(result.BenchmarkReport.Metrics) != 0 {
				t.Fatalf("tampered %s was evaluated: %+v", test.name, result.BenchmarkReport)
			}
			if !releaseStringContainsText(result.BenchmarkReport.Reasons, "content/ref mismatch") {
				t.Fatalf("tampered %s did not report the immutable binding failure: %v", test.name, result.BenchmarkReport.Reasons)
			}
		})
	}
}

func TestRFLSCR2VS10_RejectsEvidenceReplayAcrossReleaseIdentity(t *testing.T) {
	t.Run("target version", func(t *testing.T) {
		input := completeReleaseEvaluationInput()
		input.TargetVersion = "v2.0.0"
		result, err := evaluateReleaseTest(input)
		if err != nil {
			t.Fatal(err)
		}
		if result.BenchmarkReport.Status != "incomplete" {
			t.Fatalf("old evidence was replayed for another target version: %+v", result.BenchmarkReport)
		}
	})

	t.Run("profile artifact", func(t *testing.T) {
		input := completeReleaseEvaluationInput()
		input.Profile.OS = "linux"
		input.Profile.ArtifactRef = mustReleaseArtifactRef(input.Profile)
		result, err := evaluateReleaseTest(input)
		if err != nil {
			t.Fatal(err)
		}
		if result.BenchmarkReport.Status != "incomplete" || !releaseStringContainsText(result.BenchmarkReport.Reasons, "scenario corpus identity") {
			t.Fatalf("old corpus was replayed for a resealed profile: %+v", result.BenchmarkReport)
		}
	})

	t.Run("corpus artifact", func(t *testing.T) {
		input := completeReleaseEvaluationInput()
		input.Corpus.Scenarios[0].FixtureRef = releaseTestRef("new-corpus-fixture")
		input.Corpus.ArtifactRef = mustReleaseArtifactRef(input.Corpus)
		result, err := evaluateReleaseTest(input)
		if err != nil {
			t.Fatal(err)
		}
		if result.BenchmarkReport.Status != "incomplete" || !releaseStringContainsText(result.BenchmarkReport.Reasons, "not bound to the declared profile and corpus") {
			t.Fatalf("old report was replayed for a resealed corpus: %+v", result.BenchmarkReport)
		}
	})

	t.Run("child target version", func(t *testing.T) {
		input := completeReleaseEvaluationInput()
		input.ChildEvidence[0].TargetVersion = "v0.9.0"
		input.ChildEvidence[0].ArtifactRef = mustReleaseArtifactRef(input.ChildEvidence[0])
		result, err := evaluateReleaseTest(input)
		if err != nil {
			t.Fatal(err)
		}
		if result.BenchmarkReport.Status != "incomplete" || !releaseStringContainsText(result.BenchmarkReport.Reasons, "child evidence identity") {
			t.Fatalf("old child evidence was replayed for another release: %+v", result.BenchmarkReport)
		}
	})

	t.Run("child source", func(t *testing.T) {
		input := completeReleaseEvaluationInput()
		input.ChildEvidence[0].SourceRef = releaseTestRef("old-source-tree")
		input.ChildEvidence[0].ArtifactRef = mustReleaseArtifactRef(input.ChildEvidence[0])
		result, err := evaluateReleaseTest(input)
		if err != nil {
			t.Fatal(err)
		}
		if result.BenchmarkReport.Status != "incomplete" || !releaseStringContainsText(result.BenchmarkReport.Reasons, "child evidence identity") {
			t.Fatalf("child evidence from another source was replayed: %+v", result.BenchmarkReport)
		}
	})
}

func TestRFLSCR2VS10_A03(t *testing.T) {
	recordReleaseCriterionEvidence(t, "VS10-A3", nil)
	t.Run("separate same-trace distributions", func(t *testing.T) {
		result, err := evaluateReleaseTest(completeReleaseEvaluationInput())
		if err != nil {
			t.Fatalf("evaluate complete evidence: %v", err)
		}
		activity := releaseMetricByName(t, result.BenchmarkReport.Metrics, "activity_latency_ms")
		currentOrGap := releaseMetricByName(t, result.BenchmarkReport.Metrics, "current_or_gap_latency_ms")
		if activity.Aggregation != "p95" || activity.Value != 110 {
			t.Fatalf("activity distribution = %+v, want independent p95 110ms", activity)
		}
		if currentOrGap.Aggregation != "p95" || currentOrGap.Value != 1200 {
			t.Fatalf("current-or-gap distribution = %+v, want independent p95 1200ms", currentOrGap)
		}
		if !sameReleaseStrings(activity.TraceIDs, currentOrGap.TraceIDs) {
			t.Fatalf("latency distributions are not based on the same end-to-end traces: %v vs %v", activity.TraceIDs, currentOrGap.TraceIDs)
		}
	})

	t.Run("different trace populations fail closed", func(t *testing.T) {
		input := completeReleaseEvaluationInput()
		current := &input.Reports[0].MetricSeries[1].Samples[1]
		current.ScenarioID = "scenario-branch_switch"
		current.TraceID = "trace-branch_switch"
		current.TraceRef = releaseTestRef("trace-branch_switch")
		sealReleaseInput(&input)
		result, err := evaluateReleaseTest(input)
		if err != nil {
			t.Fatalf("different trace populations must produce an inspectable incomplete result: %v", err)
		}
		if result.BenchmarkReport.Status != "incomplete" || len(result.BenchmarkReport.Metrics) != 0 {
			t.Fatalf("different trace populations were evaluated: %+v", result.BenchmarkReport)
		}
	})
}

func TestRFLSCR2VS10_A04(t *testing.T) {
	recordReleaseCriterionEvidence(t, "VS10-A4", func(input *ReleaseEvaluationInput) {
		input.Reports[0].InvariantChecks[0].Result = "fail"
		input.Reports[0].InvariantChecks[0].FailureReason = "synthetic criterion failure"
		input.Reports[0].InvariantChecks[0].RecoveryCondition = "rerun synthetic criterion"
		sealReleaseInput(input)
	})
	kinds := []string{"proof_less_current", "false_settlement", "cross_generation_mix", "fabricated_evidence", "fabricated_runtime", "unsafe_approval", "secret_leak", "path_leak", "race", "unrecovered_event_gap"}
	for _, kind := range kinds {
		t.Run(kind, func(t *testing.T) {
			input := completeReleaseEvaluationInput()
			for index := range input.Reports[0].InvariantChecks {
				if input.Reports[0].InvariantChecks[index].Kind == kind {
					input.Reports[0].InvariantChecks[index].Result = "fail"
					input.Reports[0].InvariantChecks[index].FailureReason = "observed " + kind
					input.Reports[0].InvariantChecks[index].RecoveryCondition = "fix and rerun " + kind
				}
			}
			sealReleaseInput(&input)
			result, err := evaluateReleaseTest(input)
			if err != nil {
				t.Fatalf("evaluate hard failure: %v", err)
			}
			capability := result.CapabilityMatrix.Capabilities[0]
			if capability.State != "blocked" || result.BenchmarkReport.ReleaseReady || result.CapabilityMatrix.ReleaseReady {
				t.Fatalf("hard failure %s did not block the affected capability: %+v", kind, capability)
			}
			if len(capability.FailureReasons) != 1 || len(capability.RecoveryConditions) != 1 {
				t.Fatalf("hard failure %s lost reason or recovery: %+v", kind, capability)
			}
		})
	}

	t.Run("missing executed invariant check", func(t *testing.T) {
		input := completeReleaseEvaluationInput()
		input.Reports[0].InvariantChecks = input.Reports[0].InvariantChecks[1:]
		sealReleaseInput(&input)
		result, err := evaluateReleaseTest(input)
		if err != nil {
			t.Fatal(err)
		}
		if result.BenchmarkReport.Status != "incomplete" || result.BenchmarkReport.ReleaseReady || len(result.BenchmarkReport.Metrics) != 0 {
			t.Fatalf("missing adversarial execution check was accepted: %+v", result.BenchmarkReport)
		}
	})
}

func TestRFLSCR2VS10_A05(t *testing.T) {
	recordReleaseCriterionEvidence(t, "VS10-A5", nil)
	for _, kind := range requiredReleaseScenarioKinds() {
		t.Run(kind, func(t *testing.T) {
			input := completeReleaseEvaluationInput()
			scenarioID := "scenario-" + kind
			input.Corpus.Scenarios = removeReleaseScenario(input.Corpus.Scenarios, scenarioID)
			input.Reports[0].ScenarioResults = removeReleaseScenarioResult(input.Reports[0].ScenarioResults, scenarioID)
			for index := range input.Reports[0].MetricSeries {
				input.Reports[0].MetricSeries[index].Samples = removeReleaseMetricScenario(input.Reports[0].MetricSeries[index].Samples, scenarioID)
			}
			sealReleaseInput(&input)
			result, err := EvaluateReleaseCapability(input)
			if err != nil {
				t.Fatalf("missing required scenario must produce an inspectable result: %v", err)
			}
			if result.BenchmarkReport.Status != "incomplete" || result.BenchmarkReport.ReleaseReady {
				t.Fatalf("missing required scenario %s did not fail closed: %+v", kind, result.BenchmarkReport)
			}
			if !releaseStringContainsText(result.BenchmarkReport.Reasons, kind) {
				t.Fatalf("missing scenario reason does not identify %s: %v", kind, result.BenchmarkReport.Reasons)
			}
		})
	}

	t.Run("capability cannot borrow another capability scenario execution", func(t *testing.T) {
		input := completeReleaseEvaluationInput()
		input.Profile.Capabilities = append(input.Profile.Capabilities, CapabilityDeclaration{
			CapabilityID: "workspace-snapshot", RequiredScenarios: []string{"rapid_edit"}, ChildEvidenceIDs: []string{"evidence-vs01"},
		})
		sealReleaseInput(&input)
		result, err := evaluateReleaseTest(input)
		if err != nil {
			t.Fatal(err)
		}
		if result.BenchmarkReport.Status != "incomplete" || !releaseStringContainsText(result.BenchmarkReport.Reasons, "workspace-snapshot lacks required scenario evidence: rapid_edit") {
			t.Fatalf("capability borrowed an unrelated scenario/result: %+v", result.BenchmarkReport)
		}
	})
}

func TestRFLSCR2VS10_A06(t *testing.T) {
	recordReleaseCriterionEvidence(t, "VS10-A6", nil)
	required := map[string]string{
		"activity_latency_ms":       "latency",
		"current_or_gap_latency_ms": "latency",
		"precision":                 "quality",
		"recall":                    "quality",
		"semantic_delta":            "quality",
		"alignment_validity":        "quality",
		"unknown_coverage":          "quality",
		"comprehension":             "quality",
		"peak_memory_bytes":         "resource",
	}
	result, err := evaluateReleaseTest(completeReleaseEvaluationInput())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.BenchmarkReport.Metrics) != len(required) {
		t.Fatalf("metrics were combined or omitted: %+v", result.BenchmarkReport.Metrics)
	}
	for metric, category := range required {
		observed := releaseMetricByName(t, result.BenchmarkReport.Metrics, metric)
		if observed.Category != category {
			t.Fatalf("metric %s category=%s want %s", metric, observed.Category, category)
		}
	}

	for metric := range required {
		t.Run("missing "+metric, func(t *testing.T) {
			input := completeReleaseEvaluationInput()
			input.Reports[0].MetricSeries = removeReleaseMetricSeries(input.Reports[0].MetricSeries, metric)
			sealReleaseInput(&input)
			result, err := EvaluateReleaseCapability(input)
			if err != nil {
				t.Fatalf("missing separate metric must produce an inspectable result: %v", err)
			}
			if result.BenchmarkReport.Status != "incomplete" || len(result.BenchmarkReport.Metrics) != 0 {
				t.Fatalf("missing separate metric %s was accepted: %+v", metric, result.BenchmarkReport)
			}
		})
	}

	t.Run("capability cannot borrow another capability metrics", func(t *testing.T) {
		input := completeReleaseEvaluationInput()
		input.Profile.Capabilities = append(input.Profile.Capabilities, CapabilityDeclaration{
			CapabilityID: "workspace-snapshot", RequiredScenarios: []string{"rapid_edit"}, ChildEvidenceIDs: []string{"evidence-vs01"},
		})
		input.Corpus.Scenarios[0].CapabilityIDs = append(input.Corpus.Scenarios[0].CapabilityIDs, "workspace-snapshot")
		input.Reports[0].ScenarioResults[0].CapabilityIDs = append(input.Reports[0].ScenarioResults[0].CapabilityIDs, "workspace-snapshot")
		for _, check := range releaseTestInvariantChecks() {
			check.CapabilityID = "workspace-snapshot"
			input.Reports[0].InvariantChecks = append(input.Reports[0].InvariantChecks, check)
		}
		for _, threshold := range releaseTestThresholds() {
			threshold.CapabilityID = "workspace-snapshot"
			if strings.HasPrefix(threshold.DecisionRef, "decision:synthetic-vs10-") {
				threshold.DecisionRef += "-workspace-snapshot"
			}
			input.Thresholds.Thresholds = append(input.Thresholds.Thresholds, threshold)
		}
		sealReleaseInput(&input)
		result, err := evaluateReleaseTest(input)
		if err != nil {
			t.Fatal(err)
		}
		if result.BenchmarkReport.Status != "incomplete" || !releaseStringContainsText(result.BenchmarkReport.Reasons, "workspace-snapshot") {
			t.Fatalf("capability borrowed another capability's metric samples: %+v", result.BenchmarkReport)
		}
	})

	t.Run("capability metrics and gates remain independently scoped", func(t *testing.T) {
		input := completeReleaseEvaluationInput()
		input.Profile.Capabilities = append(input.Profile.Capabilities, CapabilityDeclaration{
			CapabilityID: "workspace-snapshot", RequiredScenarios: []string{"rapid_edit"}, ChildEvidenceIDs: []string{"evidence-vs01"},
		})
		input.Corpus.Scenarios[0].CapabilityIDs = append(input.Corpus.Scenarios[0].CapabilityIDs, "workspace-snapshot")
		input.Reports[0].ScenarioResults[0].CapabilityIDs = append(input.Reports[0].ScenarioResults[0].CapabilityIDs, "workspace-snapshot")
		for _, check := range releaseTestInvariantChecks() {
			check.CapabilityID = "workspace-snapshot"
			input.Reports[0].InvariantChecks = append(input.Reports[0].InvariantChecks, check)
		}
		for _, series := range append([]MetricSeries(nil), input.Reports[0].MetricSeries...) {
			series.CapabilityID = "workspace-snapshot"
			series.Samples = append([]MetricObservation(nil), series.Samples[:1]...)
			if series.Metric == "precision" {
				series.Samples[0].Value = 0.5
			}
			input.Reports[0].MetricSeries = append(input.Reports[0].MetricSeries, series)
		}
		for _, threshold := range releaseTestThresholds() {
			threshold.CapabilityID = "workspace-snapshot"
			if strings.HasPrefix(threshold.DecisionRef, "decision:synthetic-vs10-") {
				threshold.DecisionRef += "-workspace-snapshot"
			}
			input.Thresholds.Thresholds = append(input.Thresholds.Thresholds, threshold)
		}
		sealReleaseInput(&input)
		result, err := evaluateReleaseTest(input)
		if err != nil {
			t.Fatal(err)
		}
		full := releaseCapabilityByID(t, result.CapabilityMatrix.Capabilities, "full-product-release")
		scoped := releaseCapabilityByID(t, result.CapabilityMatrix.Capabilities, "workspace-snapshot")
		if full.State != "ga" || scoped.State != "partial" || result.BenchmarkReport.Status != "failed" {
			t.Fatalf("capability-specific metric failure crossed scope: full=%+v scoped=%+v report=%+v", full, scoped, result.BenchmarkReport)
		}
	})

	for _, test := range []struct {
		name   string
		mutate func(*ReleaseEvaluationInput)
	}{
		{name: "negative latency", mutate: func(input *ReleaseEvaluationInput) { input.Reports[0].MetricSeries[0].Samples[0].Value = -1 }},
		{name: "quality above one", mutate: func(input *ReleaseEvaluationInput) { input.Reports[0].MetricSeries[2].Samples[0].Value = 1.01 }},
		{name: "negative resource", mutate: func(input *ReleaseEvaluationInput) { input.Reports[0].MetricSeries[8].Samples[0].Value = -1 }},
		{name: "duplicate trace sample", mutate: func(input *ReleaseEvaluationInput) {
			input.Reports[0].MetricSeries[0].Samples = append(input.Reports[0].MetricSeries[0].Samples, input.Reports[0].MetricSeries[0].Samples[0])
		}},
	} {
		t.Run(test.name+" is incomplete", func(t *testing.T) {
			input := completeReleaseEvaluationInput()
			test.mutate(&input)
			sealReleaseInput(&input)
			result, err := EvaluateReleaseCapability(input)
			if err != nil {
				t.Fatal(err)
			}
			if result.BenchmarkReport.Status != "incomplete" || result.BenchmarkReport.ReleaseReady {
				t.Fatalf("impossible or duplicate metric sample was accepted: %+v", result.BenchmarkReport)
			}
		})
	}

	t.Run("metric metadata cannot change across reports", func(t *testing.T) {
		input := completeReleaseEvaluationInput()
		second := cloneReleaseReport(input.Reports[0])
		second.ReportID = "report-vs10-second"
		second.MetricSeries[0].Unit = "s"
		input.Reports = append(input.Reports, second)
		sealReleaseInput(&input)
		result, err := evaluateReleaseTest(input)
		if err != nil {
			t.Fatal(err)
		}
		if result.BenchmarkReport.Status != "incomplete" || !releaseStringContainsText(result.BenchmarkReport.Reasons, "changes category or unit across reports") {
			t.Fatalf("incompatible metric series were merged: %+v", result.BenchmarkReport)
		}
	})
}

func TestRFLSCR2VS10_A07(t *testing.T) {
	recordReleaseCriterionEvidence(t, "VS10-A7", nil)
	t.Run("unmeasured model capability is unsupported", func(t *testing.T) {
		input := completeReleaseEvaluationInput()
		input.Profile.Capabilities = append(input.Profile.Capabilities, CapabilityDeclaration{
			CapabilityID: "model-semantic-enrichment", RequiredScenarios: []string{"model_crash"}, ChildEvidenceIDs: []string{"evidence-vs01"},
		})
		sealReleaseInput(&input)
		result, err := EvaluateReleaseCapability(input)
		if err != nil {
			t.Fatal(err)
		}
		capability := releaseCapabilityByID(t, result.CapabilityMatrix.Capabilities, "model-semantic-enrichment")
		if capability.State != "unsupported" {
			t.Fatalf("unmeasured model capability state=%s want unsupported", capability.State)
		}
	})

	t.Run("measured failure is partial", func(t *testing.T) {
		input := completeReleaseEvaluationInput()
		input.Reports[0].ScenarioResults[0].Passed = false
		input.Reports[0].ScenarioResults[0].FailureReason = "rapid edit did not recover"
		input.Reports[0].ScenarioResults[0].Recovery = "fix and rerun rapid edit"
		sealReleaseInput(&input)
		result, err := evaluateReleaseTest(input)
		if err != nil {
			t.Fatal(err)
		}
		capability := releaseCapabilityByID(t, result.CapabilityMatrix.Capabilities, "full-product-release")
		if capability.State != "partial" || len(capability.FailureReasons) == 0 || len(capability.RecoveryConditions) == 0 {
			t.Fatalf("failed measured capability did not remain partial with recovery evidence: %+v", capability)
		}
	})
}

func TestRFLSCR2VS10_A08(t *testing.T) {
	recordReleaseCriterionEvidence(t, "VS10-A8", nil)
	tests := []struct {
		name       string
		wantStatus string
		wantState  string
		mutate     func(*ReleaseEvaluationInput)
	}{
		{name: "document label without executed acceptance", wantStatus: "incomplete", wantState: "unsupported", mutate: func(in *ReleaseEvaluationInput) { in.ChildEvidence[0].Acceptance = nil }},
		{name: "missing verification plan execution", wantStatus: "incomplete", wantState: "unsupported", mutate: func(in *ReleaseEvaluationInput) { in.ChildEvidence[0].Verification = nil }},
		{name: "zero-match acceptance", wantStatus: "incomplete", wantState: "unsupported", mutate: func(in *ReleaseEvaluationInput) {
			in.ChildEvidence[0].Acceptance[0].RunCount = 0
			in.ChildEvidence[0].Acceptance[0].PassCount = 0
		}},
		{name: "failed acceptance", wantStatus: "failed", wantState: "partial", mutate: func(in *ReleaseEvaluationInput) {
			in.ChildEvidence[0].Acceptance[0].Result = "fail"
			in.ChildEvidence[0].Acceptance[0].PassCount = 0
		}},
		{name: "unregistered evidence identity", wantStatus: "incomplete", wantState: "unsupported", mutate: func(in *ReleaseEvaluationInput) { in.ChildEvidence[0].EvidenceRegistryID = "" }},
		{name: "caller narrows away required child slice", wantStatus: "incomplete", wantState: "unsupported", mutate: func(in *ReleaseEvaluationInput) {
			in.ChildEvidence = in.ChildEvidence[:8]
			in.Profile.Capabilities[0].ChildEvidenceIDs = in.Profile.Capabilities[0].ChildEvidenceIDs[:8]
		}},
		{name: "missing required child acceptance", wantStatus: "incomplete", wantState: "unsupported", mutate: func(in *ReleaseEvaluationInput) {
			in.ChildEvidence[0].Acceptance = in.ChildEvidence[0].Acceptance[:9]
		}},
		{name: "duplicate child acceptance", wantStatus: "incomplete", wantState: "unsupported", mutate: func(in *ReleaseEvaluationInput) {
			in.ChildEvidence[0].Acceptance[1].AcceptanceID = in.ChildEvidence[0].Acceptance[0].AcceptanceID
		}},
		{name: "missing required verification check", wantStatus: "incomplete", wantState: "unsupported", mutate: func(in *ReleaseEvaluationInput) {
			in.ChildEvidence[0].Verification = in.ChildEvidence[0].Verification[:len(in.ChildEvidence[0].Verification)-1]
		}},
		{name: "duplicate verification check", wantStatus: "incomplete", wantState: "unsupported", mutate: func(in *ReleaseEvaluationInput) {
			in.ChildEvidence[0].Verification[1].CheckID = in.ChildEvidence[0].Verification[0].CheckID
		}},
		{name: "unexpected verification check", wantStatus: "incomplete", wantState: "unsupported", mutate: func(in *ReleaseEvaluationInput) {
			in.ChildEvidence[0].Verification = append(in.ChildEvidence[0].Verification, VerificationExecution{CheckID: "invented_check", ExactCommand: "not in the active contract", Result: "pass", EvidenceRef: releaseTestRef("invented-check")})
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := completeReleaseEvaluationInput()
			test.mutate(&input)
			sealReleaseInput(&input)
			result, err := evaluateReleaseTest(input)
			if err != nil {
				t.Fatalf("invalid child evidence must produce an inspectable result: %v", err)
			}
			if result.BenchmarkReport.ReleaseReady || result.CapabilityMatrix.ReleaseReady {
				t.Fatalf("document-only or failed child evidence became release-ready: %+v", result)
			}
			capability := releaseCapabilityByID(t, result.CapabilityMatrix.Capabilities, "full-product-release")
			if result.BenchmarkReport.Status != test.wantStatus || capability.State != test.wantState {
				t.Fatalf("child evidence status/state = %s/%s, want %s/%s: %+v", result.BenchmarkReport.Status, capability.State, test.wantStatus, test.wantState, capability)
			}
		})
	}

	t.Run("unreferenced child failure does not block scoped capability", func(t *testing.T) {
		input := completeReleaseEvaluationInput()
		input.Profile.Capabilities[0].CapabilityID = "workspace-snapshot"
		input.Profile.Capabilities[0].ChildEvidenceIDs = []string{"evidence-vs01"}
		for index := range input.Corpus.Scenarios {
			input.Corpus.Scenarios[index].CapabilityIDs = []string{"workspace-snapshot"}
		}
		for index := range input.Reports[0].ScenarioResults {
			input.Reports[0].ScenarioResults[index].CapabilityIDs = []string{"workspace-snapshot"}
		}
		for index := range input.Reports[0].MetricSeries {
			input.Reports[0].MetricSeries[index].CapabilityID = "workspace-snapshot"
		}
		for index := range input.Reports[0].InvariantChecks {
			input.Reports[0].InvariantChecks[index].CapabilityID = "workspace-snapshot"
		}
		for index := range input.Thresholds.Thresholds {
			input.Thresholds.Thresholds[index].CapabilityID = "workspace-snapshot"
		}
		last := len(input.ChildEvidence) - 1
		input.ChildEvidence[last].Acceptance[0].Result = "fail"
		input.ChildEvidence[last].Acceptance[0].PassCount = 0
		sealReleaseInput(&input)
		result, err := evaluateReleaseTest(input)
		if err != nil {
			t.Fatal(err)
		}
		capability := releaseCapabilityByID(t, result.CapabilityMatrix.Capabilities, "workspace-snapshot")
		if capability.State != "ga" || !result.BenchmarkReport.ReleaseReady || !result.CapabilityMatrix.ReleaseReady {
			t.Fatalf("unreferenced VS-09 failure blocked a VS-01-scoped capability: %+v / %+v", result.BenchmarkReport, capability)
		}
	})
}

func TestRFLSCR2VS10_A09(t *testing.T) {
	recordReleaseCriterionEvidence(t, "VS10-A9", nil)
	t.Run("default evaluator rejects unregistered quality decisions", func(t *testing.T) {
		result, err := EvaluateReleaseCapability(completeReleaseEvaluationInput())
		if err != nil {
			t.Fatal(err)
		}
		if result.BenchmarkReport.Status != "incomplete" || result.BenchmarkReport.ReleaseReady {
			t.Fatalf("caller-provided decision labels self-approved thresholds: %+v", result.BenchmarkReport)
		}
	})

	for _, test := range []struct {
		name   string
		mutate func(*ApprovedThresholdDecision)
	}{
		{name: "capability", mutate: func(record *ApprovedThresholdDecision) { record.CapabilityID = "other-capability" }},
		{name: "profile", mutate: func(record *ApprovedThresholdDecision) { record.ProfileID = "other-profile" }},
		{name: "corpus", mutate: func(record *ApprovedThresholdDecision) { record.CorpusID = "other-corpus" }},
		{name: "corpus version", mutate: func(record *ApprovedThresholdDecision) { record.CorpusVersion = "0.9.0" }},
	} {
		t.Run("approved decision cannot cross "+test.name, func(t *testing.T) {
			input := completeReleaseEvaluationInput()
			records := releaseTestThresholdDecisionRecords(input)
			test.mutate(&records[0])
			resolver, err := NewReleaseThresholdDecisionRegistry(records)
			if err != nil {
				t.Fatal(err)
			}
			result, err := EvaluateReleaseCapabilityWithThresholdDecisions(input, resolver)
			if err != nil {
				t.Fatal(err)
			}
			if result.BenchmarkReport.Status != "incomplete" || result.BenchmarkReport.ReleaseReady {
				t.Fatalf("approved decision crossed %s scope: %+v", test.name, result.BenchmarkReport)
			}
		})
	}

	for _, test := range []struct {
		name   string
		mutate func(*ReleaseEvaluationInput)
	}{
		{name: "resealed profile artifact", mutate: func(input *ReleaseEvaluationInput) { input.Profile.OS = "linux" }},
		{name: "resealed corpus artifact", mutate: func(input *ReleaseEvaluationInput) {
			input.Corpus.Scenarios[0].FixtureRef = releaseTestRef("new-corpus-for-threshold")
		}},
	} {
		t.Run("approved decision cannot cross "+test.name, func(t *testing.T) {
			input := completeReleaseEvaluationInput()
			resolver := releaseTestThresholdDecisionResolver(input)
			test.mutate(&input)
			sealReleaseInput(&input)
			result, err := EvaluateReleaseCapabilityWithThresholdDecisions(input, resolver)
			if err != nil {
				t.Fatal(err)
			}
			if result.BenchmarkReport.Status != "incomplete" || result.BenchmarkReport.ReleaseReady {
				t.Fatalf("approved decision crossed %s: %+v", test.name, result.BenchmarkReport)
			}
		})
	}

	for _, test := range []struct {
		name   string
		mutate func(*ReleaseEvaluationInput)
	}{
		{name: "Raw 16 cannot relax activity SLO", mutate: func(input *ReleaseEvaluationInput) { input.Thresholds.Thresholds[0].Value = 301 }},
		{name: "D9 cannot relax current-or-gap SLO", mutate: func(input *ReleaseEvaluationInput) { input.Thresholds.Thresholds[1].Value = 3001 }},
		{name: "D9 cannot approve another metric", mutate: func(input *ReleaseEvaluationInput) { input.Thresholds.Thresholds[2].DecisionRef = "decision:D9" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := completeReleaseEvaluationInput()
			test.mutate(&input)
			sealReleaseInput(&input)
			result, err := evaluateReleaseTest(input)
			if err != nil {
				t.Fatal(err)
			}
			if result.BenchmarkReport.Status != "incomplete" || result.BenchmarkReport.ReleaseReady {
				t.Fatalf("reserved decision mismatch was accepted: %+v", result.BenchmarkReport)
			}
		})
	}

	t.Run("all scoped evidence and approved thresholds pass", func(t *testing.T) {
		result, err := evaluateReleaseTest(completeReleaseEvaluationInput())
		if err != nil {
			t.Fatal(err)
		}
		if result.BenchmarkReport.Status != "passed" || !result.BenchmarkReport.ReleaseReady || !result.CapabilityMatrix.ReleaseReady {
			t.Fatalf("complete passing evidence was not release-ready: %+v", result)
		}
		capability := releaseCapabilityByID(t, result.CapabilityMatrix.Capabilities, "full-product-release")
		if capability.State != "ga" || capability.ProfileID != "profile-darwin-arm64" || capability.CorpusID != "corpus-vs10" || capability.CorpusVersion != "1.0.0" {
			t.Fatalf("capability is not scoped GA: %+v", capability)
		}
		for _, check := range completeReleaseEvaluationInput().Reports[0].InvariantChecks {
			if !releaseStringContains(capability.EvidenceRefs, check.EvidenceRef) {
				t.Fatalf("GA capability omitted passing hard-invariant evidence %s: %+v", check.Kind, capability.EvidenceRefs)
			}
		}
		if len(result.BenchmarkReport.Gates) != len(releaseTestThresholds()) {
			t.Fatalf("gate count=%d want %d", len(result.BenchmarkReport.Gates), len(releaseTestThresholds()))
		}
		for _, gate := range result.BenchmarkReport.Gates {
			if !gate.Passed || gate.DecisionRef != releaseTestDecisionRef(gate.Metric) || !strings.HasPrefix(gate.ThresholdRef, "sha256:") || gate.ProfileID == "" || len(gate.ScenarioIDs) == 0 || len(gate.TraceIDs) == 0 || len(gate.ToolchainIDs) == 0 || len(gate.EvidenceRefs) == 0 {
				t.Fatalf("gate lacks a passing immutable evidence binding: %+v", gate)
			}
		}
	})

	t.Run("missing per-metric threshold is incomplete", func(t *testing.T) {
		input := completeReleaseEvaluationInput()
		input.Thresholds.Thresholds = input.Thresholds.Thresholds[1:]
		sealReleaseInput(&input)
		result, err := evaluateReleaseTest(input)
		if err != nil {
			t.Fatal(err)
		}
		if result.BenchmarkReport.Status != "incomplete" || result.BenchmarkReport.ReleaseReady || len(result.BenchmarkReport.Gates) != 0 {
			t.Fatalf("missing metric threshold did not fail closed: %+v", result.BenchmarkReport)
		}
	})

	t.Run("unlabeled threshold approval is incomplete", func(t *testing.T) {
		input := completeReleaseEvaluationInput()
		input.Thresholds.Thresholds[0].DecisionRef = "approved-by-document-label"
		sealReleaseInput(&input)
		result, err := evaluateReleaseTest(input)
		if err != nil {
			t.Fatal(err)
		}
		if result.BenchmarkReport.Status != "incomplete" || result.BenchmarkReport.ReleaseReady || len(result.BenchmarkReport.Gates) != 0 {
			t.Fatalf("unlabeled threshold approval was accepted: %+v", result.BenchmarkReport)
		}
	})

	t.Run("D36 evidence policy cannot approve a numeric threshold", func(t *testing.T) {
		input := completeReleaseEvaluationInput()
		input.Thresholds.Thresholds[0].DecisionRef = "decision:D36"
		sealReleaseInput(&input)
		result, err := evaluateReleaseTest(input)
		if err != nil {
			t.Fatal(err)
		}
		if result.BenchmarkReport.Status != "incomplete" || result.BenchmarkReport.ReleaseReady || len(result.BenchmarkReport.Gates) != 0 {
			t.Fatalf("D36 was incorrectly accepted as numeric threshold approval: %+v", result.BenchmarkReport)
		}
	})

	t.Run("failed approved gate remains scoped and not ready", func(t *testing.T) {
		input := completeReleaseEvaluationInput()
		for index := range input.Thresholds.Thresholds {
			if input.Thresholds.Thresholds[index].Metric == "precision" {
				input.Thresholds.Thresholds[index].Value = 0.99
			}
		}
		sealReleaseInput(&input)
		result, err := evaluateReleaseTest(input)
		if err != nil {
			t.Fatal(err)
		}
		capability := releaseCapabilityByID(t, result.CapabilityMatrix.Capabilities, "full-product-release")
		if result.BenchmarkReport.Status != "failed" || result.BenchmarkReport.ReleaseReady || capability.State != "partial" {
			t.Fatalf("failed approved gate did not produce scoped partial state: %+v / %+v", result.BenchmarkReport, capability)
		}
	})

	t.Run("nonsensical threshold is incomplete", func(t *testing.T) {
		input := completeReleaseEvaluationInput()
		input.Thresholds.Thresholds[0].Value = -1
		sealReleaseInput(&input)
		result, err := evaluateReleaseTest(input)
		if err != nil {
			t.Fatal(err)
		}
		if result.BenchmarkReport.Status != "incomplete" || result.BenchmarkReport.ReleaseReady {
			t.Fatalf("negative latency threshold was accepted: %+v", result.BenchmarkReport)
		}
	})

	t.Run("threshold direction cannot invert metric semantics", func(t *testing.T) {
		input := completeReleaseEvaluationInput()
		input.Thresholds.Thresholds[2].Operator = "lte"
		input.Thresholds.Thresholds[2].Value = 1
		sealReleaseInput(&input)
		result, err := EvaluateReleaseCapability(input)
		if err != nil {
			t.Fatal(err)
		}
		if result.BenchmarkReport.Status != "incomplete" || result.BenchmarkReport.ReleaseReady {
			t.Fatalf("inverted quality threshold was accepted: %+v", result.BenchmarkReport)
		}
	})
}

func TestLoadReleaseThresholdDecisionRegistry(t *testing.T) {
	input := completeReleaseEvaluationInput()
	set := ApprovedThresholdDecisionSet{Decisions: releaseTestThresholdDecisionRecords(input)}
	set.ArtifactRef = mustReleaseArtifactRef(set)
	data, err := json.Marshal(set)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "approved-release-decisions.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	resolver, err := LoadReleaseThresholdDecisionRegistry(path)
	if err != nil {
		t.Fatalf("load sealed trusted decision set: %v", err)
	}
	result, err := EvaluateReleaseCapabilityWithThresholdDecisions(input, resolver)
	if err != nil || !result.BenchmarkReport.ReleaseReady {
		t.Fatalf("loaded trusted decisions were not usable: result=%+v err=%v", result, err)
	}

	set.Decisions[0].Value = 0
	tampered, err := json.Marshal(set)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, tampered, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadReleaseThresholdDecisionRegistry(path); err == nil || !strings.Contains(err.Error(), "content/ref mismatch") {
		t.Fatalf("tampered trusted decision set was accepted: %v", err)
	}
}

func releaseCapabilityByID(t *testing.T, capabilities []CapabilityState, id string) CapabilityState {
	t.Helper()
	for _, capability := range capabilities {
		if capability.CapabilityID == id {
			return capability
		}
	}
	t.Fatalf("missing capability %s", id)
	return CapabilityState{}
}

func removeReleaseMetricSeries(values []MetricSeries, metric string) []MetricSeries {
	result := make([]MetricSeries, 0, len(values))
	for _, value := range values {
		if value.Metric != metric {
			result = append(result, value)
		}
	}
	return result
}

func removeReleaseScenario(values []ReleaseScenario, scenarioID string) []ReleaseScenario {
	result := make([]ReleaseScenario, 0, len(values))
	for _, value := range values {
		if value.ScenarioID != scenarioID {
			result = append(result, value)
		}
	}
	return result
}

func removeReleaseScenarioResult(values []ScenarioExecution, scenarioID string) []ScenarioExecution {
	result := make([]ScenarioExecution, 0, len(values))
	for _, value := range values {
		if value.ScenarioID != scenarioID {
			result = append(result, value)
		}
	}
	return result
}

func removeReleaseMetricScenario(values []MetricObservation, scenarioID string) []MetricObservation {
	result := make([]MetricObservation, 0, len(values))
	for _, value := range values {
		if value.ScenarioID != scenarioID {
			result = append(result, value)
		}
	}
	return result
}

func releaseStringContainsText(values []string, wanted string) bool {
	for _, value := range values {
		if strings.Contains(value, wanted) {
			return true
		}
	}
	return false
}

func releaseMetricByName(t *testing.T, metrics []EvaluatedMetric, name string) EvaluatedMetric {
	t.Helper()
	for _, metric := range metrics {
		if metric.Metric == name {
			return metric
		}
	}
	t.Fatalf("missing metric %s", name)
	return EvaluatedMetric{}
}

func sameReleaseStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	seen := map[string]int{}
	for _, value := range left {
		seen[value]++
	}
	for _, value := range right {
		seen[value]--
	}
	for _, count := range seen {
		if count != 0 {
			return false
		}
	}
	return true
}

func cloneReleaseReport(report ExecutionReport) ExecutionReport {
	data, err := json.Marshal(report)
	if err != nil {
		panic(err)
	}
	var clone ExecutionReport
	if err := json.Unmarshal(data, &clone); err != nil {
		panic(err)
	}
	return clone
}

func completeReleaseEvaluationInput() ReleaseEvaluationInput {
	input := ReleaseEvaluationInput{
		TargetVersion: "v1.0.0",
		EvaluationID:  "evaluation-vs10",
		EvaluatedAt:   "2026-09-07T00:00:00Z",
		Profile: &ReleaseProfile{
			SchemaID: ReleaseProfileV2SchemaID, SchemaVersion: 2,
			TargetVersion: "v1.0.0", ProfileID: "profile-darwin-arm64", ArtifactRef: releaseTestRef("profile"), OS: "darwin", Architecture: "arm64",
			Hardware:    HardwareProfile{CPU: "test-cpu", LogicalCPUs: 8, MemoryBytes: 16 << 30},
			Repository:  RepositoryProfile{Shape: "polyglot", FileCount: 1000, Bytes: 1 << 20, Languages: []string{"go", "typescript", "dart"}, FixtureRef: releaseTestRef("repository")},
			Toolchains:  []ReleaseToolchain{{ToolchainID: "go-1.25", Name: "go", Version: "1.25", ArtifactRef: releaseTestRef("toolchain")}},
			ActiveScope: "repository", LoadProfile: "single-user", Browser: "chromium",
			Capabilities: []CapabilityDeclaration{{CapabilityID: "full-product-release", RequiredScenarios: requiredReleaseScenarioKinds(), ChildEvidenceIDs: releaseTestChildEvidenceIDs()}},
		},
		Corpus: &ScenarioManifest{
			SchemaID: ScenarioManifestV2SchemaID, SchemaVersion: 2, CorpusID: "corpus-vs10", CorpusVersion: "1.0.0", ArtifactRef: releaseTestRef("corpus"), ProfileID: "profile-darwin-arm64", ProfileRef: releaseTestRef("profile"),
			Scenarios: releaseTestScenarios(),
		},
		Reports: []ExecutionReport{{
			SchemaID: ExecutionReportV2SchemaID, SchemaVersion: 2, ReportID: "report-vs10", ArtifactRef: releaseTestRef("report"), ProfileID: "profile-darwin-arm64", ProfileRef: releaseTestRef("profile"), CorpusID: "corpus-vs10", CorpusVersion: "1.0.0", CorpusRef: releaseTestRef("corpus"), Command: "go test ./...", ExecutedAt: "2026-09-07T00:00:00Z", ToolchainIDs: []string{"go-1.25"},
			ScenarioResults: releaseTestScenarioResults(),
			MetricSeries: []MetricSeries{
				releaseTestMetric("activity_latency_ms", "latency", "ms", []float64{100, 110}),
				releaseTestMetric("current_or_gap_latency_ms", "latency", "ms", []float64{1000, 1200}),
				releaseTestMetric("precision", "quality", "ratio", []float64{0.91, 0.92}),
				releaseTestMetric("recall", "quality", "ratio", []float64{0.90, 0.91}),
				releaseTestMetric("semantic_delta", "quality", "ratio", []float64{1, 1}),
				releaseTestMetric("alignment_validity", "quality", "ratio", []float64{1, 1}),
				releaseTestMetric("unknown_coverage", "quality", "ratio", []float64{1, 1}),
				releaseTestMetric("comprehension", "quality", "ratio", []float64{0.90, 0.95}),
				releaseTestMetric("peak_memory_bytes", "resource", "bytes", []float64{1000, 1200}),
			},
			InvariantChecks: releaseTestInvariantChecks(),
		}},
		Thresholds:    &ApprovedThresholdSet{ProfileID: "profile-darwin-arm64", ProfileRef: releaseTestRef("profile"), CorpusID: "corpus-vs10", CorpusVersion: "1.0.0", CorpusRef: releaseTestRef("corpus"), ArtifactRef: releaseTestRef("thresholds"), Thresholds: releaseTestThresholds()},
		ChildEvidence: releaseTestChildEvidence(),
	}
	sealReleaseInput(&input)
	return input
}

func releaseTestChildEvidenceIDs() []string {
	result := make([]string, 0, 9)
	for slice := 1; slice <= 9; slice++ {
		result = append(result, fmt.Sprintf("evidence-vs%02d", slice))
	}
	return result
}

func releaseTestChildEvidence() []ChildEvidence {
	acceptanceCounts := []int{10, 11, 15, 14, 7, 9, 7, 10, 13}
	result := make([]ChildEvidence, 0, len(acceptanceCounts))
	for index, count := range acceptanceCounts {
		slice := index + 1
		sliceID := fmt.Sprintf("VS-%02d", slice)
		compactSliceID := fmt.Sprintf("VS%02d", slice)
		acceptance := make([]AcceptanceExecution, 0, count)
		for criterion := 1; criterion <= count; criterion++ {
			acceptance = append(acceptance, AcceptanceExecution{
				AcceptanceID: fmt.Sprintf("%s-A%d", compactSliceID, criterion), TestID: fmt.Sprintf("TestRFLSCR2%s_A%02d", compactSliceID, criterion),
				Package: "codeflow/internal/contractharness", RunCount: 1, PassCount: 1, Result: "pass", EvidenceRef: releaseTestRef(fmt.Sprintf("acceptance-%02d-%02d", slice, criterion)),
			})
		}
		verification := make([]VerificationExecution, 0, len(releaseTestVerificationCheckIDs[sliceID]))
		for _, checkID := range releaseTestVerificationCheckIDs[sliceID] {
			verification = append(verification, VerificationExecution{CheckID: checkID, ExactCommand: "synthetic evaluator fixture for " + sliceID + " " + checkID, Result: "pass", EvidenceRef: releaseTestRef(fmt.Sprintf("verification-%02d-%s", slice, checkID))})
		}
		result = append(result, ChildEvidence{
			EvidenceID: fmt.Sprintf("evidence-vs%02d", slice), SliceID: sliceID, ContractRef: "REQUESTED-FLOW-LIVE-SEMANTIC-COMPILER-R2-" + sliceID,
			EvidenceRegistryID: fmt.Sprintf("rflsc-r2-vs-%02d", slice), ImplementationRef: "codeflow/internal/contractharness", ExecutionID: fmt.Sprintf("execution-vs%02d", slice), BinaryDigest: releaseTestRef(fmt.Sprintf("binary-%02d", slice)),
			Acceptance: acceptance, Verification: verification,
		})
	}
	return result
}

var releaseTestVerificationCheckIDs = map[string][]string{
	"VS-01": {"registry", "acceptance", "protocol", "static", "build", "regression", "security", "migration", "concurrency"},
	"VS-02": {"registry", "acceptance", "dart", "typescript", "go", "static", "build", "security", "concurrency_reliability", "regression"},
	"VS-03": {"registry", "acceptance", "contract", "static", "build", "concurrency", "reliability", "browser", "a11y", "regression"},
	"VS-04": {"registry", "acceptance", "contract", "static", "build", "regression", "browser", "a11y", "security"},
	"VS-05": {"registry", "acceptance", "full_slice", "static", "build", "security", "regression"},
	"VS-06": {"registry", "acceptance", "full_slice", "static", "build", "security", "concurrency_reliability", "regression"},
	"VS-07": {"registry", "acceptance", "full_slice", "static", "build", "security", "browser", "a11y", "regression"},
	"VS-08": {"registry", "acceptance", "full_slice", "static", "build", "security", "concurrency_reliability", "browser", "a11y", "regression"},
	"VS-09": {"registry", "acceptance", "full_slice", "static", "build", "security", "migration_persistence", "concurrency_reliability", "browser", "a11y", "regression"},
}

func sealReleaseInput(input *ReleaseEvaluationInput) {
	if input.Profile != nil {
		input.Profile.ArtifactRef = mustReleaseArtifactRef(input.Profile)
	}
	if input.Corpus != nil {
		if input.Profile != nil {
			input.Corpus.ProfileRef = input.Profile.ArtifactRef
		}
		input.Corpus.ArtifactRef = mustReleaseArtifactRef(input.Corpus)
	}
	for index := range input.Reports {
		if input.Profile != nil {
			input.Reports[index].ProfileRef = input.Profile.ArtifactRef
		}
		if input.Corpus != nil {
			input.Reports[index].CorpusRef = input.Corpus.ArtifactRef
		}
		input.Reports[index].ArtifactRef = mustReleaseArtifactRef(input.Reports[index])
	}
	if input.Thresholds != nil {
		if input.Profile != nil {
			input.Thresholds.ProfileRef = input.Profile.ArtifactRef
		}
		if input.Corpus != nil {
			input.Thresholds.CorpusID = input.Corpus.CorpusID
			input.Thresholds.CorpusVersion = input.Corpus.CorpusVersion
			input.Thresholds.CorpusRef = input.Corpus.ArtifactRef
		}
		input.Thresholds.ArtifactRef = mustReleaseArtifactRef(input.Thresholds)
	}
	for index := range input.ChildEvidence {
		input.ChildEvidence[index].TargetVersion = input.TargetVersion
		if input.Profile != nil {
			input.ChildEvidence[index].SourceRef = input.Profile.Repository.FixtureRef
		}
		input.ChildEvidence[index].ArtifactRef = mustReleaseArtifactRef(input.ChildEvidence[index])
	}
}

func mustReleaseArtifactRef(value any) string {
	ref, err := evidence.Ref(value)
	if err != nil {
		panic(err)
	}
	return ref
}

func releaseTestRef(seed string) string {
	digest := sha256.Sum256([]byte(seed))
	return "sha256:" + hex.EncodeToString(digest[:])
}

func requiredReleaseScenarioKinds() []string {
	return []string{"rapid_edit", "multi_file", "rename_delete", "syntax_error", "branch_switch", "watcher_gap", "open_closure", "late_result", "cas_conflict", "adapter_crash", "model_crash", "reconnect"}
}

func releaseTestScenarios() []ReleaseScenario {
	kinds := requiredReleaseScenarioKinds()
	result := make([]ReleaseScenario, 0, len(kinds))
	for _, kind := range kinds {
		result = append(result, ReleaseScenario{ScenarioID: "scenario-" + kind, Kind: kind, CapabilityIDs: []string{"full-product-release"}, FixtureRef: releaseTestRef("fixture-" + kind)})
	}
	return result
}

func releaseTestScenarioResults() []ScenarioExecution {
	result := make([]ScenarioExecution, 0, len(requiredReleaseScenarioKinds()))
	for _, kind := range requiredReleaseScenarioKinds() {
		result = append(result, ScenarioExecution{ScenarioID: "scenario-" + kind, TraceID: "trace-" + kind, TraceRef: releaseTestRef("trace-" + kind), ToolchainID: "go-1.25", CapabilityIDs: []string{"full-product-release"}, Passed: true})
	}
	return result
}

func releaseTestInvariantChecks() []HardInvariantCheck {
	result := make([]HardInvariantCheck, 0, len(hardReleaseFailureKinds))
	for _, kind := range []string{"proof_less_current", "false_settlement", "cross_generation_mix", "fabricated_evidence", "fabricated_runtime", "unsafe_approval", "secret_leak", "path_leak", "race", "unrecovered_event_gap"} {
		result = append(result, HardInvariantCheck{
			Kind: kind, CapabilityID: "full-product-release", ScenarioID: "scenario-rapid_edit", TraceID: "trace-rapid_edit",
			EvidenceRef: releaseTestRef("invariant-" + kind), Result: "pass",
		})
	}
	return result
}

func releaseTestMetric(metric, category, unit string, values []float64) MetricSeries {
	observations := make([]MetricObservation, 0, len(values))
	for index, value := range values {
		kind := requiredReleaseScenarioKinds()[index]
		observations = append(observations, MetricObservation{ScenarioID: "scenario-" + kind, TraceID: "trace-" + kind, TraceRef: releaseTestRef("trace-" + kind), ToolchainID: "go-1.25", ProfileID: "profile-darwin-arm64", EvidenceRef: releaseTestRef(metric + kind), Value: value})
	}
	return MetricSeries{Metric: metric, CapabilityID: "full-product-release", Category: category, Unit: unit, Samples: observations}
}

func releaseTestThresholds() []ApprovedThreshold {
	return []ApprovedThreshold{
		{Metric: "activity_latency_ms", CapabilityID: "full-product-release", Operator: "lte", Value: 300, Unit: "ms", DecisionRef: releaseTestDecisionRef("activity_latency_ms")},
		{Metric: "current_or_gap_latency_ms", CapabilityID: "full-product-release", Operator: "lte", Value: 3000, Unit: "ms", DecisionRef: releaseTestDecisionRef("current_or_gap_latency_ms")},
		{Metric: "precision", CapabilityID: "full-product-release", Operator: "gte", Value: 0.9, Unit: "ratio", DecisionRef: releaseTestDecisionRef("precision")},
		{Metric: "recall", CapabilityID: "full-product-release", Operator: "gte", Value: 0.9, Unit: "ratio", DecisionRef: releaseTestDecisionRef("recall")},
		{Metric: "semantic_delta", CapabilityID: "full-product-release", Operator: "gte", Value: 1, Unit: "ratio", DecisionRef: releaseTestDecisionRef("semantic_delta")},
		{Metric: "alignment_validity", CapabilityID: "full-product-release", Operator: "gte", Value: 1, Unit: "ratio", DecisionRef: releaseTestDecisionRef("alignment_validity")},
		{Metric: "unknown_coverage", CapabilityID: "full-product-release", Operator: "gte", Value: 1, Unit: "ratio", DecisionRef: releaseTestDecisionRef("unknown_coverage")},
		{Metric: "comprehension", CapabilityID: "full-product-release", Operator: "gte", Value: 0.9, Unit: "ratio", DecisionRef: releaseTestDecisionRef("comprehension")},
		{Metric: "peak_memory_bytes", CapabilityID: "full-product-release", Operator: "lte", Value: 2000, Unit: "bytes", DecisionRef: releaseTestDecisionRef("peak_memory_bytes")},
	}
}

func releaseTestDecisionRef(metric string) string {
	switch metric {
	case "activity_latency_ms":
		return "parent:Raw-16"
	case "current_or_gap_latency_ms":
		return "decision:D9"
	default:
		return "decision:synthetic-vs10-" + metric
	}
}

func releaseTestThresholdDecisionRecords(input ReleaseEvaluationInput) []ApprovedThresholdDecision {
	records := []ApprovedThresholdDecision{}
	if input.Thresholds == nil {
		return records
	}
	for _, threshold := range input.Thresholds.Thresholds {
		if !strings.HasPrefix(threshold.DecisionRef, "decision:synthetic-vs10-") {
			continue
		}
		records = append(records, ApprovedThresholdDecision{
			DecisionRef:   threshold.DecisionRef,
			Metric:        threshold.Metric,
			CapabilityID:  threshold.CapabilityID,
			ProfileID:     input.Thresholds.ProfileID,
			ProfileRef:    input.Thresholds.ProfileRef,
			CorpusID:      input.Thresholds.CorpusID,
			CorpusVersion: input.Thresholds.CorpusVersion,
			CorpusRef:     input.Thresholds.CorpusRef,
			Operator:      threshold.Operator,
			Value:         threshold.Value,
			Unit:          threshold.Unit,
		})
	}
	return records
}

func releaseTestThresholdDecisionResolver(input ReleaseEvaluationInput) ThresholdDecisionResolver {
	records := releaseTestThresholdDecisionRecords(input)
	resolver, err := NewReleaseThresholdDecisionRegistry(records)
	if err != nil {
		panic(err)
	}
	return resolver
}

func evaluateReleaseTest(input ReleaseEvaluationInput) (*ReleaseEvaluation, error) {
	return EvaluateReleaseCapabilityWithThresholdDecisions(input, releaseTestThresholdDecisionResolver(input))
}

const releaseCriterionEvidencePrefix = "VS10_CRITERION_EVIDENCE "

type releaseCriterionEvidence struct {
	Criterion           string   `json:"criterion"`
	InputRef            string   `json:"inputRef"`
	ProfileRef          string   `json:"profileRef,omitempty"`
	CorpusRef           string   `json:"corpusRef,omitempty"`
	ExecutionReportRefs []string `json:"executionReportRefs"`
	ThresholdRef        string   `json:"thresholdRef,omitempty"`
	ChildEvidenceRefs   []string `json:"childEvidenceRefs"`
	OutputRef           string   `json:"outputRef"`
	InputArtifact       string   `json:"inputArtifact,omitempty"`
	OutputArtifact      string   `json:"outputArtifact,omitempty"`
}

func recordReleaseCriterionEvidence(t *testing.T, criterion string, mutate func(*ReleaseEvaluationInput)) {
	t.Helper()
	input := completeReleaseEvaluationInput()
	input.EvaluationID = "synthetic-criterion-" + strings.ToLower(criterion)
	if mutate != nil {
		mutate(&input)
	}
	result, err := evaluateReleaseTest(input)
	if err != nil {
		t.Fatalf("execute criterion evidence: %v", err)
	}
	record := releaseCriterionEvidence{
		Criterion: criterion,
		InputRef:  mustReleaseArtifactRef(input),
		OutputRef: mustReleaseArtifactRef(result),
	}
	if evidenceDir := os.Getenv("CODEFLOW_VS10_EVIDENCE_DIR"); evidenceDir != "" {
		base := strings.ToLower(criterion)
		record.InputArtifact = base + "-input.json"
		record.OutputArtifact = base + "-output.json"
		inputBytes, marshalErr := json.Marshal(input)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		outputBytes, marshalErr := json.Marshal(result)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		if err := os.WriteFile(filepath.Join(evidenceDir, record.InputArtifact), inputBytes, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(evidenceDir, record.OutputArtifact), outputBytes, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if input.Profile != nil {
		record.ProfileRef = input.Profile.ArtifactRef
	}
	if input.Corpus != nil {
		record.CorpusRef = input.Corpus.ArtifactRef
	}
	for _, report := range input.Reports {
		record.ExecutionReportRefs = append(record.ExecutionReportRefs, report.ArtifactRef)
	}
	if input.Thresholds != nil {
		record.ThresholdRef = input.Thresholds.ArtifactRef
	}
	for _, evidence := range input.ChildEvidence {
		record.ChildEvidenceRefs = append(record.ChildEvidenceRefs, evidence.ArtifactRef)
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	t.Log(releaseCriterionEvidencePrefix + string(encoded))
}
