package flowmeter

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"codeflow/internal/contractharness"
	"codeflow/internal/releaseartifact"
	"codeflow/internal/semantic"
)

type thresholdPrompt struct {
	metric   string
	operator string
	unit     string
}

var customThresholdPrompts = []thresholdPrompt{
	{metric: "precision", operator: "gte", unit: "ratio"},
	{metric: "recall", operator: "gte", unit: "ratio"},
	{metric: "semantic_delta", operator: "gte", unit: "ratio"},
	{metric: "alignment_validity", operator: "gte", unit: "ratio"},
	{metric: "unknown_coverage", operator: "gte", unit: "ratio"},
	{metric: "comprehension", operator: "gte", unit: "ratio"},
	{metric: "peak_memory_bytes", operator: "lte", unit: "bytes"},
}

func runApprove(args []string, stdin io.Reader, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("approve", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dir := flags.String("dir", "", "measurement run directory")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *dir == "" || flags.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "사용법: flowmeter approve --dir <실행 디렉터리>")
		return 2
	}
	plan, err := loadPlan(filepath.Join(*dir, "flowmeter-plan.json"))
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "측정 계획 읽기 실패: %v\n", err)
		return 1
	}
	profile, corpus, reports, err := loadApprovalEvidence(*dir)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "승인할 측정 근거 읽기 실패: %v\n", err)
		return 1
	}
	if profile.TargetVersion != plan.TargetVersion {
		_, _ = fmt.Fprintln(stderr, "측정 계획과 profile의 targetVersion이 다릅니다.")
		return 1
	}
	printObservedMetrics(stdout, reports)

	scanner := bufio.NewScanner(stdin)
	approvalID := time.Now().UTC().Format("20060102T150405.000000000Z")
	thresholds := make([]semantic.ApprovedThreshold, 0, len(profile.Capabilities)*9)
	decisions := make([]semantic.ApprovedThresholdDecision, 0, len(profile.Capabilities)*7)
	for _, capability := range profile.Capabilities {
		thresholds = append(thresholds,
			semantic.ApprovedThreshold{Metric: "activity_latency_ms", CapabilityID: capability.CapabilityID, Operator: "lte", Value: 300, Unit: "ms", DecisionRef: "parent:Raw-16"},
			semantic.ApprovedThreshold{Metric: "current_or_gap_latency_ms", CapabilityID: capability.CapabilityID, Operator: "lte", Value: 3000, Unit: "ms", DecisionRef: "decision:D9"},
		)
		for _, prompt := range customThresholdPrompts {
			_, _ = fmt.Fprintf(stdout, "%s/%s 기준값 (%s, %s): ", capability.CapabilityID, prompt.metric, prompt.operator, prompt.unit)
			if !scanner.Scan() {
				_, _ = fmt.Fprintln(stderr, "승인을 취소했습니다. 모든 기준값을 입력해야 합니다.")
				return 1
			}
			value, err := strconv.ParseFloat(strings.TrimSpace(scanner.Text()), 64)
			if err != nil || !validThresholdValue(prompt, value) {
				_, _ = fmt.Fprintf(stderr, "잘못된 기준값: %q\n", scanner.Text())
				return 1
			}
			decisionRef := fmt.Sprintf("decision:flowmeter:%s:%s:%s:%s:%s:%s", sanitizeDecisionPart(plan.TargetVersion), sanitizeDecisionPart(profile.ProfileID), sanitizeDecisionPart(corpus.CorpusVersion), sanitizeDecisionPart(capability.CapabilityID), prompt.metric, approvalID)
			thresholds = append(thresholds, semantic.ApprovedThreshold{
				Metric: prompt.metric, CapabilityID: capability.CapabilityID, Operator: prompt.operator, Value: value, Unit: prompt.unit, DecisionRef: decisionRef,
			})
			decisions = append(decisions, semantic.ApprovedThresholdDecision{
				DecisionRef: decisionRef, Metric: prompt.metric, CapabilityID: capability.CapabilityID,
				ProfileID: profile.ProfileID, ProfileRef: profile.ArtifactRef,
				CorpusID: corpus.CorpusID, CorpusVersion: corpus.CorpusVersion, CorpusRef: corpus.ArtifactRef,
				Operator: prompt.operator, Value: value, Unit: prompt.unit,
			})
		}
	}
	_, _ = fmt.Fprintf(stdout, "확정하려면 APPROVE %s 를 입력하세요: ", plan.TargetVersion)
	if !scanner.Scan() || strings.TrimSpace(scanner.Text()) != "APPROVE "+plan.TargetVersion {
		_, _ = fmt.Fprintln(stderr, "승인을 취소했습니다. 파일을 만들지 않았습니다.")
		return 1
	}
	set := semantic.ApprovedThresholdSet{
		ProfileID: profile.ProfileID, ProfileRef: profile.ArtifactRef,
		CorpusID: corpus.CorpusID, CorpusVersion: corpus.CorpusVersion, CorpusRef: corpus.ArtifactRef,
		Thresholds: thresholds,
	}
	if set.ArtifactRef, err = releaseartifact.Ref(set); err != nil {
		_, _ = fmt.Fprintf(stderr, "threshold sealing 실패: %v\n", err)
		return 1
	}
	decisionSet := semantic.ApprovedThresholdDecisionSet{Decisions: decisions}
	if decisionSet.ArtifactRef, err = releaseartifact.Ref(decisionSet); err != nil {
		_, _ = fmt.Fprintf(stderr, "decision sealing 실패: %v\n", err)
		return 1
	}
	if err := writeJSONExclusive(filepath.Join(*dir, "approved-thresholds.json"), set); err != nil {
		_, _ = fmt.Fprintf(stderr, "승인 파일 저장 실패: %v\n", err)
		return 1
	}
	if err := writeJSONExclusive(filepath.Join(*dir, "approved-threshold-decisions.json"), decisionSet); err != nil {
		_ = os.Remove(filepath.Join(*dir, "approved-thresholds.json"))
		_, _ = fmt.Fprintf(stderr, "승인 파일 저장 실패: %v\n", err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "승인 근거를 저장했습니다. 다음: flowmeter finalize --dir %s\n", *dir)
	return 0
}

func loadApprovalEvidence(dir string) (semantic.ReleaseProfile, semantic.ScenarioManifest, []semantic.ExecutionReport, error) {
	var profile semantic.ReleaseProfile
	if err := readStrictJSON(filepath.Join(dir, "release-profile.json"), &profile); err != nil {
		return profile, semantic.ScenarioManifest{}, nil, err
	}
	if err := validateVS10Value(profile.SchemaID, profile); err != nil {
		return profile, semantic.ScenarioManifest{}, nil, err
	}
	var corpus semantic.ScenarioManifest
	if err := readStrictJSON(filepath.Join(dir, "scenario-manifest.json"), &corpus); err != nil {
		return profile, corpus, nil, err
	}
	if err := validateVS10Value(corpus.SchemaID, corpus); err != nil {
		return profile, corpus, nil, err
	}
	var reports []semantic.ExecutionReport
	if err := readStrictJSON(filepath.Join(dir, "execution-reports.json"), &reports); err != nil {
		return profile, corpus, nil, err
	}
	for _, report := range reports {
		if err := validateVS10Value(report.SchemaID, report); err != nil {
			return profile, corpus, nil, err
		}
	}
	return profile, corpus, reports, nil
}

func validateVS10Value(schemaID string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return contractharness.ValidateVS10Contract(schemaID, data)
}

func readStrictJSON(path string, target any) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%s에는 JSON 값 하나만 있어야 합니다", path)
	}
	return nil
}

func writeJSONExclusive(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func validThresholdValue(prompt thresholdPrompt, value float64) bool {
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
		return false
	}
	return prompt.unit != "ratio" || value <= 1
}

func sanitizeDecisionPart(value string) string {
	return strings.NewReplacer(":", "_", "/", "_", " ", "_").Replace(value)
}

func printObservedMetrics(stdout io.Writer, reports []semantic.ExecutionReport) {
	values := map[string][]float64{}
	for _, report := range reports {
		for _, series := range report.MetricSeries {
			key := series.CapabilityID + "/" + series.Metric
			for _, sample := range series.Samples {
				values[key] = append(values[key], sample.Value)
			}
		}
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	_, _ = fmt.Fprintln(stdout, "실제 수집된 측정값:")
	for _, key := range keys {
		samples := values[key]
		aggregation := "평균"
		value := mean(samples)
		if strings.HasSuffix(key, "/activity_latency_ms") || strings.HasSuffix(key, "/current_or_gap_latency_ms") {
			aggregation = "P95"
			value = p95(samples)
		}
		_, _ = fmt.Fprintf(stdout, "  %s: %s %g, 표본 %d개\n", key, aggregation, value, len(samples))
	}
	_, _ = fmt.Fprintf(stdout, "기준 승인 시각: %s\n", time.Now().UTC().Format(time.RFC3339))
}

func mean(values []float64) float64 {
	total := 0.0
	for _, value := range values {
		total += value
	}
	return total / float64(len(values))
}

func p95(values []float64) float64 {
	ordered := append([]float64(nil), values...)
	sort.Float64s(ordered)
	index := int(math.Ceil(0.95*float64(len(ordered)))) - 1
	if index < 0 {
		index = 0
	}
	return ordered[index]
}
