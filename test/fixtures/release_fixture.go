// Package fixtures contains deterministic data used only to test evaluators
// and public transport boundaries. It is not production benchmark evidence.
package fixtures

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"codeflow/internal/evidence"
	"codeflow/internal/semantic"
)

// VS10ReleaseEvaluationInput returns synthetic, internally consistent evidence.
// It proves evaluator behavior only and must never be used for a release claim.
func VS10ReleaseEvaluationInput() semantic.ReleaseEvaluationInput {
	kinds := []string{"rapid_edit", "multi_file", "rename_delete", "syntax_error", "branch_switch", "watcher_gap", "open_closure", "late_result", "cas_conflict", "adapter_crash", "model_crash", "reconnect"}
	scenarios := make([]semantic.ReleaseScenario, 0, len(kinds))
	results := make([]semantic.ScenarioExecution, 0, len(kinds))
	for _, kind := range kinds {
		scenarioID := "scenario-" + kind
		traceID := "trace-" + kind
		scenarios = append(scenarios, semantic.ReleaseScenario{ScenarioID: scenarioID, Kind: kind, CapabilityIDs: []string{"full-product-release"}, FixtureRef: fixtureRef("fixture-" + kind)})
		results = append(results, semantic.ScenarioExecution{ScenarioID: scenarioID, TraceID: traceID, TraceRef: fixtureRef(traceID), ToolchainID: "go-1.25", CapabilityIDs: []string{"full-product-release"}, Passed: true})
	}
	metric := func(name, category, unit string, values []float64) semantic.MetricSeries {
		samples := make([]semantic.MetricObservation, 0, len(values))
		for index, value := range values {
			kind := kinds[index]
			samples = append(samples, semantic.MetricObservation{
				ScenarioID: "scenario-" + kind, TraceID: "trace-" + kind, TraceRef: fixtureRef("trace-" + kind),
				ToolchainID: "go-1.25", ProfileID: "profile-darwin-arm64", EvidenceRef: fixtureRef(name + kind), Value: value,
			})
		}
		return semantic.MetricSeries{Metric: name, CapabilityID: "full-product-release", Category: category, Unit: unit, Samples: samples}
	}
	input := semantic.ReleaseEvaluationInput{
		TargetVersion: "v1.0.0", EvaluationID: "synthetic-evaluator-execution", EvaluatedAt: "2026-09-07T00:00:00Z",
		Profile: &semantic.ReleaseProfile{
			SchemaID: semantic.ReleaseProfileV2SchemaID, SchemaVersion: 2, TargetVersion: "v1.0.0", ProfileID: "profile-darwin-arm64", OS: "darwin", Architecture: "arm64",
			Hardware:    semantic.HardwareProfile{CPU: "synthetic-cpu", LogicalCPUs: 8, MemoryBytes: 16 << 30},
			Repository:  semantic.RepositoryProfile{Shape: "synthetic-polyglot", FileCount: 1000, Bytes: 1 << 20, Languages: []string{"go", "typescript", "dart"}, FixtureRef: fixtureRef("repository")},
			Toolchains:  []semantic.ReleaseToolchain{{ToolchainID: "go-1.25", Name: "go", Version: "1.25", ArtifactRef: fixtureRef("toolchain")}},
			ActiveScope: "repository", LoadProfile: "single-user", Browser: "chromium",
			Capabilities: []semantic.CapabilityDeclaration{{CapabilityID: "full-product-release", RequiredScenarios: kinds, ChildEvidenceIDs: childEvidenceIDs()}},
		},
		Corpus: &semantic.ScenarioManifest{
			SchemaID: semantic.ScenarioManifestV2SchemaID, SchemaVersion: 2, CorpusID: "corpus-vs10", CorpusVersion: "1.0.0", ProfileID: "profile-darwin-arm64", Scenarios: scenarios,
		},
		Reports: []semantic.ExecutionReport{{
			SchemaID: semantic.ExecutionReportV2SchemaID, SchemaVersion: 2, ReportID: "report-vs10-synthetic", ProfileID: "profile-darwin-arm64", CorpusID: "corpus-vs10", CorpusVersion: "1.0.0",
			Command: "synthetic evaluator fixture; not a production benchmark", ExecutedAt: "2026-09-07T00:00:00Z", ToolchainIDs: []string{"go-1.25"}, ScenarioResults: results,
			MetricSeries: []semantic.MetricSeries{
				metric("activity_latency_ms", "latency", "ms", []float64{100, 110}),
				metric("current_or_gap_latency_ms", "latency", "ms", []float64{1000, 1200}),
				metric("precision", "quality", "ratio", []float64{0.91, 0.92}),
				metric("recall", "quality", "ratio", []float64{0.90, 0.91}),
				metric("semantic_delta", "quality", "ratio", []float64{1, 1}),
				metric("alignment_validity", "quality", "ratio", []float64{1, 1}),
				metric("unknown_coverage", "quality", "ratio", []float64{1, 1}),
				metric("comprehension", "quality", "ratio", []float64{0.90, 0.95}),
				metric("peak_memory_bytes", "resource", "bytes", []float64{1000, 1200}),
			},
			InvariantChecks: invariantChecks(),
		}},
		Thresholds: &semantic.ApprovedThresholdSet{
			ProfileID: "profile-darwin-arm64", CorpusID: "corpus-vs10", CorpusVersion: "1.0.0",
			Thresholds: []semantic.ApprovedThreshold{
				{Metric: "activity_latency_ms", CapabilityID: "full-product-release", Operator: "lte", Value: 300, Unit: "ms", DecisionRef: "parent:Raw-16"},
				{Metric: "current_or_gap_latency_ms", CapabilityID: "full-product-release", Operator: "lte", Value: 3000, Unit: "ms", DecisionRef: "decision:D9"},
				{Metric: "precision", CapabilityID: "full-product-release", Operator: "gte", Value: 0.9, Unit: "ratio", DecisionRef: "decision:synthetic-vs10-precision"},
				{Metric: "recall", CapabilityID: "full-product-release", Operator: "gte", Value: 0.9, Unit: "ratio", DecisionRef: "decision:synthetic-vs10-recall"},
				{Metric: "semantic_delta", CapabilityID: "full-product-release", Operator: "gte", Value: 1, Unit: "ratio", DecisionRef: "decision:synthetic-vs10-semantic_delta"},
				{Metric: "alignment_validity", CapabilityID: "full-product-release", Operator: "gte", Value: 1, Unit: "ratio", DecisionRef: "decision:synthetic-vs10-alignment_validity"},
				{Metric: "unknown_coverage", CapabilityID: "full-product-release", Operator: "gte", Value: 1, Unit: "ratio", DecisionRef: "decision:synthetic-vs10-unknown_coverage"},
				{Metric: "comprehension", CapabilityID: "full-product-release", Operator: "gte", Value: 0.9, Unit: "ratio", DecisionRef: "decision:synthetic-vs10-comprehension"},
				{Metric: "peak_memory_bytes", CapabilityID: "full-product-release", Operator: "lte", Value: 2000, Unit: "bytes", DecisionRef: "decision:synthetic-vs10-peak_memory_bytes"},
			},
		},
		ChildEvidence: childEvidence(),
	}
	input.Profile.ArtifactRef = artifactRef(input.Profile)
	input.Corpus.ProfileRef = input.Profile.ArtifactRef
	input.Corpus.ArtifactRef = artifactRef(input.Corpus)
	for index := range input.Reports {
		input.Reports[index].ProfileRef = input.Profile.ArtifactRef
		input.Reports[index].CorpusRef = input.Corpus.ArtifactRef
		input.Reports[index].ArtifactRef = artifactRef(input.Reports[index])
	}
	input.Thresholds.ProfileRef = input.Profile.ArtifactRef
	input.Thresholds.CorpusRef = input.Corpus.ArtifactRef
	input.Thresholds.ArtifactRef = artifactRef(input.Thresholds)
	for index := range input.ChildEvidence {
		input.ChildEvidence[index].TargetVersion = input.TargetVersion
		input.ChildEvidence[index].SourceRef = input.Profile.Repository.FixtureRef
		input.ChildEvidence[index].ArtifactRef = artifactRef(input.ChildEvidence[index])
	}
	return input
}

// VS10ReleaseThresholdDecisions returns a trusted registry for the synthetic
// quality and resource thresholds in VS10ReleaseEvaluationInput. Production
// code must load maintainer-approved records instead.
func VS10ReleaseThresholdDecisions() semantic.ThresholdDecisionResolver {
	input := VS10ReleaseEvaluationInput()
	records := make([]semantic.ApprovedThresholdDecision, 0, len(input.Thresholds.Thresholds)-2)
	for _, threshold := range input.Thresholds.Thresholds {
		if threshold.DecisionRef == "parent:Raw-16" || threshold.DecisionRef == "decision:D9" {
			continue
		}
		records = append(records, semantic.ApprovedThresholdDecision{
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
	resolver, err := semantic.NewReleaseThresholdDecisionRegistry(records)
	if err != nil {
		panic(err)
	}
	return resolver
}

var verificationCheckIDs = map[string][]string{
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

func childEvidenceIDs() []string {
	result := make([]string, 0, 9)
	for slice := 1; slice <= 9; slice++ {
		result = append(result, fmt.Sprintf("evidence-vs%02d", slice))
	}
	return result
}

func invariantChecks() []semantic.HardInvariantCheck {
	kinds := []string{"proof_less_current", "false_settlement", "cross_generation_mix", "fabricated_evidence", "fabricated_runtime", "unsafe_approval", "secret_leak", "path_leak", "race", "unrecovered_event_gap"}
	result := make([]semantic.HardInvariantCheck, 0, len(kinds))
	for _, kind := range kinds {
		result = append(result, semantic.HardInvariantCheck{
			Kind: kind, CapabilityID: "full-product-release", ScenarioID: "scenario-rapid_edit", TraceID: "trace-rapid_edit", EvidenceRef: fixtureRef("invariant-" + kind), Result: "pass",
		})
	}
	return result
}

func childEvidence() []semantic.ChildEvidence {
	acceptanceCounts := []int{10, 11, 15, 14, 7, 9, 7, 10, 13}
	result := make([]semantic.ChildEvidence, 0, len(acceptanceCounts))
	for index, count := range acceptanceCounts {
		slice := index + 1
		sliceID := fmt.Sprintf("VS-%02d", slice)
		compactSliceID := fmt.Sprintf("VS%02d", slice)
		acceptance := make([]semantic.AcceptanceExecution, 0, count)
		for criterion := 1; criterion <= count; criterion++ {
			acceptance = append(acceptance, semantic.AcceptanceExecution{
				AcceptanceID: fmt.Sprintf("%s-A%d", compactSliceID, criterion), TestID: fmt.Sprintf("TestRFLSCR2%s_A%02d", compactSliceID, criterion), Package: "codeflow/internal/contractharness",
				RunCount: 1, PassCount: 1, Result: "pass", EvidenceRef: fixtureRef(fmt.Sprintf("acceptance-%02d-%02d", slice, criterion)),
			})
		}
		verification := make([]semantic.VerificationExecution, 0, len(verificationCheckIDs[sliceID]))
		for _, checkID := range verificationCheckIDs[sliceID] {
			verification = append(verification, semantic.VerificationExecution{CheckID: checkID, ExactCommand: "synthetic evaluator fixture for " + sliceID + " " + checkID, Result: "pass", EvidenceRef: fixtureRef(fmt.Sprintf("verification-%02d-%s", slice, checkID))})
		}
		result = append(result, semantic.ChildEvidence{
			EvidenceID: fmt.Sprintf("evidence-vs%02d", slice), SliceID: sliceID, ContractRef: "REQUESTED-FLOW-LIVE-SEMANTIC-COMPILER-R2-" + sliceID, EvidenceRegistryID: fmt.Sprintf("rflsc-r2-vs-%02d", slice),
			ImplementationRef: "codeflow/internal/contractharness", ExecutionID: fmt.Sprintf("synthetic-execution-vs%02d", slice), BinaryDigest: fixtureRef(fmt.Sprintf("binary-%02d", slice)), Acceptance: acceptance, Verification: verification,
		})
	}
	return result
}

func artifactRef(value any) string {
	ref, err := evidence.ArtifactRef(value)
	if err != nil {
		panic(err)
	}
	return ref
}

func fixtureRef(seed string) string {
	digest := sha256.Sum256([]byte(seed))
	return "sha256:" + hex.EncodeToString(digest[:])
}
