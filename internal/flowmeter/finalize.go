package flowmeter

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"codeflow/internal/semantic"
)

func runFinalize(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("finalize", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dir := flags.String("dir", "", "measurement run directory")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *dir == "" || flags.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "사용법: flowmeter finalize --dir <실행 디렉터리>")
		return 2
	}
	plan, err := loadPlan(filepath.Join(*dir, "flowmeter-plan.json"))
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "측정 계획 읽기 실패: %v\n", err)
		return 1
	}
	profile, corpus, reports, err := loadApprovalEvidence(*dir)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "실행 근거 읽기 실패: %v\n", err)
		return 1
	}
	var thresholds semantic.ApprovedThresholdSet
	if err := readStrictJSON(filepath.Join(*dir, "approved-thresholds.json"), &thresholds); err != nil {
		_, _ = fmt.Fprintf(stderr, "승인 기준 읽기 실패: %v\n", err)
		return 1
	}
	var children []semantic.ChildEvidence
	if err := readStrictJSON(filepath.Join(*dir, "child-evidence.json"), &children); err != nil {
		_, _ = fmt.Fprintf(stderr, "child evidence 읽기 실패: %v\n", err)
		return 1
	}
	resolver, err := semantic.LoadReleaseThresholdDecisionRegistry(filepath.Join(*dir, "approved-threshold-decisions.json"))
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "승인 decision 읽기 실패: %v\n", err)
		return 1
	}
	if profile.TargetVersion != plan.TargetVersion {
		_, _ = fmt.Fprintln(stderr, "측정 계획과 profile의 targetVersion이 다릅니다.")
		return 1
	}
	input := semantic.ReleaseEvaluationInput{
		TargetVersion: plan.TargetVersion,
		EvaluationID:  "flowmeter-" + sanitizeDecisionPart(plan.TargetVersion) + "-" + time.Now().UTC().Format("20060102T150405Z"),
		EvaluatedAt:   time.Now().UTC().Format(time.RFC3339),
		Profile:       &profile,
		Corpus:        &corpus,
		Reports:       reports,
		Thresholds:    &thresholds,
		ChildEvidence: children,
	}
	result, err := semantic.EvaluateReleaseCapabilityWithThresholdDecisions(input, resolver)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "release 평가 실패: %v\n", err)
		return 1
	}
	inputPath := filepath.Join(*dir, "release-evaluation-input.json")
	outputPath := filepath.Join(*dir, "release-evaluation-output.json")
	for _, path := range []string{inputPath, outputPath} {
		if _, err := os.Stat(path); err == nil {
			_, _ = fmt.Fprintf(stderr, "기존 결과를 덮어쓰지 않았습니다: %s\n", path)
			return 1
		} else if !os.IsNotExist(err) {
			_, _ = fmt.Fprintf(stderr, "결과 경로 검사 실패: %v\n", err)
			return 1
		}
	}
	if err := writeJSONExclusive(inputPath, input); err != nil {
		_, _ = fmt.Fprintf(stderr, "평가 입력 저장 실패: %v\n", err)
		return 1
	}
	if err := writeJSONExclusive(outputPath, result); err != nil {
		_ = os.Remove(inputPath)
		_, _ = fmt.Fprintf(stderr, "평가 결과 저장 실패: %v\n", err)
		return 1
	}
	if result.BenchmarkReport.ReleaseReady {
		_, _ = fmt.Fprintf(stdout, "release ready: PASS\n결과: %s\n다음 실행 후 비교: flowmeter compare latest --root %s\n", outputPath, filepath.Dir(*dir))
		return 0
	}
	_, _ = fmt.Fprintf(stderr, "release ready: FAIL (%s)\n결과: %s\n", result.BenchmarkReport.Status, outputPath)
	for _, reason := range result.BenchmarkReport.Reasons {
		_, _ = fmt.Fprintf(stderr, "- %s\n", reason)
	}
	return 1
}
