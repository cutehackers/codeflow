package flowmeter

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"time"

	"codeflow/internal/semantic"
)

func runCompare(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) > 0 && args[0] == "latest" {
		return runReleaseCompare(args[1:], stdout, stderr)
	}
	flags := flag.NewFlagSet("compare", flag.ContinueOnError)
	flags.SetOutput(stderr)
	root := flags.String("root", ".codeflow/flowmeter/runs", "benchmark result root")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "사용법: flowmeter compare [--root <결과 경로>]")
		return 2
	}
	return compareBenchmarks(*root, stdout, stderr)
}

func compareBenchmarks(root string, stdout io.Writer, stderr io.Writer) int {
	paths, err := filepath.Glob(filepath.Join(root, "*", "benchmark-result.json"))
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "결과 검색 실패: %v\n", err)
		return 1
	}
	type resultFile struct {
		path     string
		result   BenchmarkResult
		executed time.Time
	}
	files := make([]resultFile, 0, len(paths))
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		var result BenchmarkResult
		if err := readStrictJSON(path, &result); err != nil {
			continue
		}
		executed, err := time.Parse(time.RFC3339Nano, result.ExecutedAt)
		if err != nil {
			executed = info.ModTime()
		}
		files = append(files, resultFile{path: path, result: result, executed: executed})
	}
	if len(files) < 2 {
		_, _ = fmt.Fprintln(stderr, "비교하려면 flowmeter run 결과가 2개 이상 필요합니다.")
		return 1
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].executed.Equal(files[j].executed) {
			return files[i].path < files[j].path
		}
		return files[i].executed.Before(files[j].executed)
	})
	previous := files[len(files)-2]
	latest := files[len(files)-1]
	if previous.result.BenchmarkID != latest.result.BenchmarkID || previous.result.CorpusVersion != latest.result.CorpusVersion || previous.result.CorpusRef != latest.result.CorpusRef {
		_, _ = fmt.Fprintln(stderr, "비교 거부: 최근 두 결과의 benchmark corpus가 다릅니다.")
		return 1
	}
	if !reflect.DeepEqual(previous.result.Profile, latest.result.Profile) {
		_, _ = fmt.Fprintln(stderr, "비교 거부: 최근 두 결과의 OS, CPU 또는 Go 실행 환경이 다릅니다.")
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "이전: %s (%s)\n최신: %s (%s)\n", previous.path, previous.result.TargetVersion, latest.path, latest.result.TargetVersion)
	previousMetrics := benchmarkMetricMap(previous.result.Metrics)
	latestMetrics := benchmarkMetricMap(latest.result.Metrics)
	keys := make([]string, 0, len(latestMetrics))
	for key := range latestMetrics {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		current := latestMetrics[key]
		old, ok := previousMetrics[key]
		if !ok || old.Unit != current.Unit || old.Aggregation != current.Aggregation {
			_, _ = fmt.Fprintf(stdout, "%s: 비교 불가\n", key)
			continue
		}
		_, _ = fmt.Fprintf(stdout, "%s: %g → %g (%+g %s)\n", key, old.Value, current.Value, current.Value-old.Value, current.Unit)
	}
	return 0
}

func benchmarkMetricMap(metrics []BenchmarkMetric) map[string]BenchmarkMetric {
	result := make(map[string]BenchmarkMetric, len(metrics))
	for _, metric := range metrics {
		result[metric.Name] = metric
	}
	return result
}

func runReleaseCompare(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("compare latest", flag.ContinueOnError)
	flags.SetOutput(stderr)
	root := flags.String("root", "", "parent directory containing release evidence runs")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *root == "" || flags.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "사용법: flowmeter compare latest --root <실행 디렉터리들의 상위 경로>")
		return 2
	}
	return compareReleaseLatest(*root, stdout, stderr)
}

func compareReleaseLatest(root string, stdout io.Writer, stderr io.Writer) int {
	paths, err := filepath.Glob(filepath.Join(root, "*", "release-evaluation-output.json"))
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "결과 검색 실패: %v\n", err)
		return 1
	}
	type resultFile struct {
		path       string
		evaluation semantic.ReleaseEvaluation
		evaluated  time.Time
	}
	files := make([]resultFile, 0, len(paths))
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		evaluation, err := loadEvaluation(path)
		if err != nil || evaluation.BenchmarkReport == nil {
			continue
		}
		evaluated, err := time.Parse(time.RFC3339, evaluation.BenchmarkReport.EvaluatedAt)
		if err != nil {
			evaluated = info.ModTime()
		}
		files = append(files, resultFile{path: path, evaluation: evaluation, evaluated: evaluated})
	}
	if len(files) < 2 {
		_, _ = fmt.Fprintln(stderr, "비교할 수 있는 release 결과 파일이 2개 이상 필요합니다.")
		return 1
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].evaluated.Equal(files[j].evaluated) {
			return files[i].path < files[j].path
		}
		return files[i].evaluated.Before(files[j].evaluated)
	})
	previous := files[len(files)-2].evaluation
	latest := files[len(files)-1].evaluation
	_, _ = fmt.Fprintf(stdout, "이전: %s\n최신: %s\n", files[len(files)-2].path, files[len(files)-1].path)
	previousMetrics := metricMap(previous.BenchmarkReport.Metrics)
	latestMetrics := metricMap(latest.BenchmarkReport.Metrics)
	keys := make([]string, 0, len(latestMetrics))
	for key := range latestMetrics {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		current := latestMetrics[key]
		old, ok := previousMetrics[key]
		if !ok || old.Unit != current.Unit || old.Aggregation != current.Aggregation {
			_, _ = fmt.Fprintf(stdout, "%s: 비교 불가\n", key)
			continue
		}
		_, _ = fmt.Fprintf(stdout, "%s: %g → %g (%+g %s)\n", key, old.Value, current.Value, current.Value-old.Value, current.Unit)
	}
	_, _ = fmt.Fprintf(stdout, "releaseReady: %t → %t\n", previous.BenchmarkReport.ReleaseReady, latest.BenchmarkReport.ReleaseReady)
	return 0
}

func loadEvaluation(path string) (semantic.ReleaseEvaluation, error) {
	var evaluation semantic.ReleaseEvaluation
	err := readStrictJSON(path, &evaluation)
	return evaluation, err
}

func metricMap(metrics []semantic.EvaluatedMetric) map[string]semantic.EvaluatedMetric {
	result := make(map[string]semantic.EvaluatedMetric, len(metrics))
	for _, metric := range metrics {
		result[metric.CapabilityID+"/"+metric.Metric] = metric
	}
	return result
}
