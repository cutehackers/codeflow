package flowmeter_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codeflow/internal/flowmeter"
	"codeflow/internal/semantic"
	"codeflow/test/fixtures"
)

func TestHelpExplainsTheCompleteRoutineInPlainLanguage(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := flowmeter.Run(context.Background(), []string{"help"}, strings.NewReader(""), &stdout, &stderr)

	if exitCode != 0 {
		t.Fatalf("exit code = %d, stderr = %q", exitCode, stderr.String())
	}
	for _, expected := range []string{
		"고정된 CodeFlow benchmark corpus",
		"flowmeter prepare",
		"flowmeter run",
		"flowmeter approve",
		"flowmeter finalize",
		"flowmeter compare latest",
	} {
		if !strings.Contains(stdout.String(), expected) {
			t.Fatalf("help does not contain %q:\n%s", expected, stdout.String())
		}
	}
}

func TestReleaseHelpShowsTheOneCommandCollectionFlow(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := flowmeter.Run(context.Background(), []string{"release", "help"}, strings.NewReader(""), &stdout, &stderr)

	if exitCode != 0 || !strings.Contains(stdout.String(), "flowmeter release collect --target-version vX.Y.Z") {
		t.Fatalf("exit code = %d, stdout = %q, stderr = %q", exitCode, stdout.String(), stderr.String())
	}
}

func TestReleaseCollectRejectsAnInvalidTargetVersionBeforeWriting(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := flowmeter.Run(context.Background(), []string{"release", "collect", "--target-version", "0.4"}, strings.NewReader(""), &stdout, &stderr)

	if exitCode != 2 || !strings.Contains(stderr.String(), "vX.Y.Z") {
		t.Fatalf("exit code = %d, stderr = %q", exitCode, stderr.String())
	}
}

func TestRunMeasuresTheBuiltInFixedCorpus(t *testing.T) {
	root := filepath.Join(t.TempDir(), "runs")
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := flowmeter.Run(context.Background(), []string{"run", "--output-root", root}, strings.NewReader(""), &stdout, &stderr)

	if exitCode != 0 && exitCode != 1 {
		t.Fatalf("exit code = %d, stderr = %q", exitCode, stderr.String())
	}
	paths, err := filepath.Glob(filepath.Join(root, "*", "benchmark-result.json"))
	if err != nil || len(paths) != 1 {
		t.Fatalf("results = %#v, err = %v", paths, err)
	}
	var result flowmeter.BenchmarkResult
	readJSON(t, paths[0], &result)
	if result.BenchmarkID != "codeflow-live-semantic-compiler" || result.CorpusVersion != "live-semantic-compiler-v1" || !strings.HasPrefix(result.CorpusRef, "sha256:") {
		t.Fatalf("benchmark identity = %#v", result)
	}
	if len(result.Traces) != 24 || len(result.Metrics) != 4 || !strings.HasPrefix(result.TargetRef, "sha256:") {
		t.Fatalf("trace count = %d, metrics = %d, target ref = %q", len(result.Traces), len(result.Metrics), result.TargetRef)
	}
	if (exitCode == 0 && result.Status != "passed") || (exitCode == 1 && result.Status != "failed") {
		t.Fatalf("exit code = %d, benchmark status = %q", exitCode, result.Status)
	}
}

func TestPrepareCreatesARepeatableFailClosedPlan(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "run")
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := flowmeter.Run(context.Background(), []string{
		"prepare", "--dir", dir, "--target-version", "v1.2.3",
	}, strings.NewReader(""), &stdout, &stderr)

	if exitCode != 0 {
		t.Fatalf("exit code = %d, stderr = %q", exitCode, stderr.String())
	}
	data, err := os.ReadFile(filepath.Join(dir, "flowmeter-plan.json"))
	if err != nil {
		t.Fatal(err)
	}
	var plan struct {
		TargetVersion string `json:"targetVersion"`
		Commands      []struct {
			ID      string `json:"id"`
			Command string `json:"command"`
		} `json:"commands"`
		RequiredArtifacts []string `json:"requiredArtifacts"`
	}
	if err := json.Unmarshal(data, &plan); err != nil {
		t.Fatal(err)
	}
	if plan.TargetVersion != "v1.2.3" {
		t.Fatalf("target version = %q", plan.TargetVersion)
	}
	if len(plan.Commands) != 3 || plan.Commands[0].Command != "" {
		t.Fatalf("commands = %#v", plan.Commands)
	}
	if len(plan.RequiredArtifacts) != 4 {
		t.Fatalf("required artifacts = %#v", plan.RequiredArtifacts)
	}
	if !strings.Contains(stdout.String(), "flowmeter run --dir") {
		t.Fatalf("missing next action: %s", stdout.String())
	}
}

func TestPrepareDoesNotOverwriteAnExistingPlan(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "flowmeter-plan.json")
	if err := os.WriteFile(path, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := flowmeter.Run(context.Background(), []string{
		"prepare", "--dir", dir, "--target-version", "v1.2.3",
	}, strings.NewReader(""), &stdout, &stderr)

	if exitCode == 0 {
		t.Fatal("prepare unexpectedly overwrote an existing plan")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "keep" {
		t.Fatalf("existing plan changed to %q", data)
	}
}

func TestRunRefusesMissingCommandsAndMissingEvidence(t *testing.T) {
	dir := t.TempDir()
	writePlan(t, dir, flowmeter.Plan{
		SchemaVersion:     1,
		TargetVersion:     "v1.2.3",
		Commands:          []flowmeter.PlanCommand{{ID: "scenarios", Description: "collect"}},
		RequiredArtifacts: requiredArtifacts(),
	})
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := flowmeter.Run(context.Background(), []string{"run", "--dir", dir}, strings.NewReader(""), &stdout, &stderr)

	if exitCode == 0 || !strings.Contains(stderr.String(), "명령이 비어 있습니다") {
		t.Fatalf("exit code = %d, stderr = %q", exitCode, stderr.String())
	}
}

func TestRunExecutesConfiguredCollectorsAndRequiresTheirArtifacts(t *testing.T) {
	dir := t.TempDir()
	writePlan(t, dir, flowmeter.Plan{
		SchemaVersion: 1,
		TargetVersion: "v1.2.3",
		Commands: []flowmeter.PlanCommand{{
			ID: "profile", Description: "collect profile", Command: "for f in release-profile.json scenario-manifest.json execution-reports.json child-evidence.json; do printf '{}\\n' > \"$f\"; done",
		}},
		RequiredArtifacts: requiredArtifacts(),
	})
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := flowmeter.Run(context.Background(), []string{"run", "--dir", dir}, strings.NewReader(""), &stdout, &stderr)

	if exitCode != 0 {
		t.Fatalf("exit code = %d, stderr = %q", exitCode, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "release-profile.json")); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "flowmeter approve --dir") {
		t.Fatalf("missing next action: %s", stdout.String())
	}
}

func TestRunRejectsAPlanThatRemovesRequiredEvidence(t *testing.T) {
	dir := t.TempDir()
	writePlan(t, dir, flowmeter.Plan{
		SchemaVersion: 1,
		TargetVersion: "v1.2.3",
		Commands:      []flowmeter.PlanCommand{{ID: "collector", Command: ":"}},
		RequiredArtifacts: []string{
			"../outside.json", "scenario-manifest.json", "execution-reports.json", "child-evidence.json",
		},
	})
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := flowmeter.Run(context.Background(), []string{"run", "--dir", dir}, strings.NewReader(""), &stdout, &stderr)

	if exitCode == 0 || !strings.Contains(stderr.String(), "release-profile.json") {
		t.Fatalf("exit code = %d, stderr = %q", exitCode, stderr.String())
	}
}

func writePlan(t *testing.T, dir string, plan flowmeter.Plan) {
	t.Helper()
	if plan.RequiredArtifacts == nil {
		plan.RequiredArtifacts = requiredArtifacts()
	}
	data, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "flowmeter-plan.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func requiredArtifacts() []string {
	return []string{"release-profile.json", "scenario-manifest.json", "execution-reports.json", "child-evidence.json"}
}

func TestApproveRequiresExplicitPerMetricValuesAndFinalConfirmation(t *testing.T) {
	dir := t.TempDir()
	input := fixtures.VS10ReleaseEvaluationInput()
	writePlan(t, dir, flowmeter.Plan{SchemaVersion: 1, TargetVersion: input.TargetVersion})
	writeJSON(t, filepath.Join(dir, "release-profile.json"), input.Profile)
	writeJSON(t, filepath.Join(dir, "scenario-manifest.json"), input.Corpus)
	writeJSON(t, filepath.Join(dir, "execution-reports.json"), input.Reports)
	stdin := strings.NewReader(strings.Repeat("0.9\n", 6) + "2000\nAPPROVE " + input.TargetVersion + "\n")
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := flowmeter.Run(context.Background(), []string{"approve", "--dir", dir}, stdin, &stdout, &stderr)

	if exitCode != 0 {
		t.Fatalf("exit code = %d, stderr = %q", exitCode, stderr.String())
	}
	var thresholds semantic.ApprovedThresholdSet
	readJSON(t, filepath.Join(dir, "approved-thresholds.json"), &thresholds)
	if len(thresholds.Thresholds) != 9 {
		t.Fatalf("threshold count = %d", len(thresholds.Thresholds))
	}
	if _, err := semantic.LoadReleaseThresholdDecisionRegistry(filepath.Join(dir, "approved-threshold-decisions.json")); err != nil {
		t.Fatalf("decision registry: %v", err)
	}
	if !strings.Contains(stdout.String(), "activity_latency_ms: P95") {
		t.Fatalf("approval did not show evaluator-compatible latency aggregation: %s", stdout.String())
	}
}

func TestApproveCancellationCreatesNoApprovalFiles(t *testing.T) {
	dir := t.TempDir()
	input := fixtures.VS10ReleaseEvaluationInput()
	writePlan(t, dir, flowmeter.Plan{SchemaVersion: 1, TargetVersion: input.TargetVersion})
	writeJSON(t, filepath.Join(dir, "release-profile.json"), input.Profile)
	writeJSON(t, filepath.Join(dir, "scenario-manifest.json"), input.Corpus)
	writeJSON(t, filepath.Join(dir, "execution-reports.json"), input.Reports)
	stdin := strings.NewReader(strings.Repeat("0.9\n", 6) + "2000\nNO\n")
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := flowmeter.Run(context.Background(), []string{"approve", "--dir", dir}, stdin, &stdout, &stderr)

	if exitCode == 0 {
		t.Fatal("cancelled approval unexpectedly succeeded")
	}
	for _, name := range []string{"approved-thresholds.json", "approved-threshold-decisions.json"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatalf("%s was created", name)
		}
	}
}

func TestFinalizeEvaluatesSealedEvidenceAndWritesReusableResults(t *testing.T) {
	dir := t.TempDir()
	input := fixtures.VS10ReleaseEvaluationInput()
	writePlan(t, dir, flowmeter.Plan{SchemaVersion: 1, TargetVersion: input.TargetVersion})
	writeJSON(t, filepath.Join(dir, "release-profile.json"), input.Profile)
	writeJSON(t, filepath.Join(dir, "scenario-manifest.json"), input.Corpus)
	writeJSON(t, filepath.Join(dir, "execution-reports.json"), input.Reports)
	writeJSON(t, filepath.Join(dir, "child-evidence.json"), input.ChildEvidence)
	approveInput := strings.NewReader(strings.Repeat("0.9\n", 6) + "2000\nAPPROVE " + input.TargetVersion + "\n")
	if code := flowmeter.Run(context.Background(), []string{"approve", "--dir", dir}, approveInput, io.Discard, io.Discard); code != 0 {
		t.Fatalf("approve exit code = %d", code)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := flowmeter.Run(context.Background(), []string{"finalize", "--dir", dir}, strings.NewReader(""), &stdout, &stderr)

	if exitCode != 0 {
		t.Fatalf("exit code = %d, stderr = %q", exitCode, stderr.String())
	}
	var evaluation semantic.ReleaseEvaluation
	readJSON(t, filepath.Join(dir, "release-evaluation-output.json"), &evaluation)
	if !evaluation.BenchmarkReport.ReleaseReady || evaluation.BenchmarkReport.Status != "passed" {
		t.Fatalf("evaluation = %#v", evaluation.BenchmarkReport)
	}
	if !strings.Contains(stdout.String(), "release ready: PASS") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestCompareShowsMetricDeltaForTheSameCorpusAndProfile(t *testing.T) {
	root := t.TempDir()
	oldDir := filepath.Join(root, "run-old")
	newDir := filepath.Join(root, "run-new")
	if err := os.MkdirAll(oldDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(newDir, 0o755); err != nil {
		t.Fatal(err)
	}
	oldPath := filepath.Join(oldDir, "benchmark-result.json")
	newPath := filepath.Join(newDir, "benchmark-result.json")
	writeJSON(t, oldPath, comparisonBenchmark("same-ref", 100, "2026-09-07T00:00:00Z"))
	writeJSON(t, newPath, comparisonBenchmark("same-ref", 80, "2026-09-07T01:00:00Z"))
	oldTime := time.Now().Add(-time.Hour)
	if err := os.Chtimes(oldPath, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := flowmeter.Run(context.Background(), []string{"compare", "--root", root}, strings.NewReader(""), &stdout, &stderr)

	if exitCode != 0 {
		t.Fatalf("exit code = %d, stderr = %q", exitCode, stderr.String())
	}
	if !strings.Contains(stdout.String(), "100 → 80 (-20 ms)") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestCompareRejectsDifferentCorpus(t *testing.T) {
	root := t.TempDir()
	for index, corpusRef := range []string{"old-ref", "new-ref"} {
		dir := filepath.Join(root, string(rune('a'+index)))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		writeJSON(t, filepath.Join(dir, "benchmark-result.json"), comparisonBenchmark(corpusRef, 100, fmt.Sprintf("2026-09-07T0%d:00:00Z", index)))
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := flowmeter.Run(context.Background(), []string{"compare", "--root", root}, strings.NewReader(""), &stdout, &stderr)
	if exitCode == 0 || !strings.Contains(stderr.String(), "corpus가 다릅니다") {
		t.Fatalf("exit code = %d, stderr = %q", exitCode, stderr.String())
	}
}

func comparisonBenchmark(corpusRef string, value float64, executedAt string) flowmeter.BenchmarkResult {
	return flowmeter.BenchmarkResult{
		SchemaVersion: 1, BenchmarkID: "benchmark", CorpusVersion: "v1", CorpusRef: corpusRef,
		TargetVersion: "target", TargetRef: "sha256:target", ExecutedAt: executedAt,
		Profile: flowmeter.BenchmarkProfile{OS: "test", Architecture: "test", CPU: "test", LogicalCPUs: 1, GoVersion: "test"},
		Metrics: []flowmeter.BenchmarkMetric{{Name: "activity_latency_ms", Unit: "ms", Aggregation: "p95", Value: value}},
	}
}

func writeJSON(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readJSON(t *testing.T, path string, target any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, target); err != nil {
		t.Fatal(err)
	}
}
