package flowmeter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"codeflow/internal/flowview"
	"codeflow/internal/semantic"
	"codeflow/internal/workspace"
)

const (
	benchmarkID            = "codeflow-live-semantic-compiler"
	benchmarkCorpusVersion = "live-semantic-compiler-v1"
	benchmarkSampleCount   = 24
	benchmarkFixture       = "package service\nfunc HandleSubmit() {}\n"
	benchmarkEditTemplate  = "package service\nfunc HandleSubmit(){step%d()}\n"
)

type BenchmarkResult struct {
	SchemaVersion int               `json:"schemaVersion"`
	BenchmarkID   string            `json:"benchmarkId"`
	CorpusVersion string            `json:"corpusVersion"`
	CorpusRef     string            `json:"corpusRef"`
	TargetVersion string            `json:"targetVersion"`
	TargetRef     string            `json:"targetRef"`
	ExecutedAt    string            `json:"executedAt"`
	Profile       BenchmarkProfile  `json:"profile"`
	Metrics       []BenchmarkMetric `json:"metrics"`
	Traces        []BenchmarkTrace  `json:"traces"`
	Status        string            `json:"status"`
	Reasons       []string          `json:"reasons"`
}

type BenchmarkProfile struct {
	OS           string `json:"os"`
	Architecture string `json:"architecture"`
	CPU          string `json:"cpu"`
	LogicalCPUs  int    `json:"logicalCpus"`
	GoVersion    string `json:"goVersion"`
}

type BenchmarkMetric struct {
	Name        string  `json:"name"`
	Unit        string  `json:"unit"`
	Aggregation string  `json:"aggregation"`
	Value       float64 `json:"value"`
}

type BenchmarkTrace struct {
	TraceID               string  `json:"traceId"`
	ActivityLatencyMs     float64 `json:"activityLatencyMs"`
	CurrentOrGapLatencyMs float64 `json:"currentOrGapLatencyMs"`
	SnapshotRef           string  `json:"snapshotRef"`
	TreeRef               string  `json:"treeRef"`
	BasisRef              string  `json:"basisRef"`
}

func runBenchmark(ctx context.Context, args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("run", flag.ContinueOnError)
	flags.SetOutput(stderr)
	outputRoot := flags.String("output-root", ".codeflow/flowmeter/runs", "benchmark result root")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "사용법: flowmeter run [--output-root <경로>]")
		return 2
	}
	result, err := measureFixedBenchmark(ctx)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "benchmark 실패: %v\n", err)
		return 1
	}
	runID := time.Now().UTC().Format("20060102T150405.000000000Z") + "-" + safeBenchmarkPathPart(result.TargetVersion)
	runDir := filepath.Join(*outputRoot, runID)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		_, _ = fmt.Fprintf(stderr, "benchmark 결과 디렉터리 생성 실패: %v\n", err)
		return 1
	}
	path := filepath.Join(runDir, "benchmark-result.json")
	if err := writeJSONExclusive(path, result); err != nil {
		_, _ = fmt.Fprintf(stderr, "benchmark 결과 저장 실패: %v\n", err)
		return 1
	}
	for _, metric := range result.Metrics {
		_, _ = fmt.Fprintf(stdout, "%s: %g %s (%s)\n", metric.Name, metric.Value, metric.Unit, metric.Aggregation)
	}
	_, _ = fmt.Fprintf(stdout, "결과: %s\n비교: flowmeter compare\n", path)
	if result.Status != "passed" {
		for _, reason := range result.Reasons {
			_, _ = fmt.Fprintf(stderr, "- %s\n", reason)
		}
		return 1
	}
	return 0
}

func measureFixedBenchmark(ctx context.Context) (BenchmarkResult, error) {
	started := time.Now()
	var peakMemory atomic.Uint64
	stopMemory := make(chan struct{})
	var memoryWG sync.WaitGroup
	memoryWG.Add(1)
	go func() {
		defer memoryWG.Done()
		ticker := time.NewTicker(5 * time.Millisecond)
		defer ticker.Stop()
		for {
			var stats runtime.MemStats
			runtime.ReadMemStats(&stats)
			for current := peakMemory.Load(); stats.Sys > current && !peakMemory.CompareAndSwap(current, stats.Sys); current = peakMemory.Load() {
			}
			select {
			case <-ticker.C:
			case <-stopMemory:
				return
			}
		}
	}()
	traces, err := measureLiveSemanticTraces(ctx, benchmarkSampleCount)
	close(stopMemory)
	memoryWG.Wait()
	if err != nil {
		return BenchmarkResult{}, err
	}
	activity := make([]float64, 0, len(traces))
	currentOrGap := make([]float64, 0, len(traces))
	for _, trace := range traces {
		activity = append(activity, trace.ActivityLatencyMs)
		currentOrGap = append(currentOrGap, trace.CurrentOrGapLatencyMs)
	}
	activityP95 := p95(activity)
	currentOrGapP95 := p95(currentOrGap)
	status := "passed"
	reasons := []string{}
	if activityP95 > 300 {
		status = "failed"
		reasons = append(reasons, fmt.Sprintf("activity latency P95 %gms exceeds 300ms", activityP95))
	}
	if currentOrGapP95 > 3000 {
		status = "failed"
		reasons = append(reasons, fmt.Sprintf("current-or-gap latency P95 %gms exceeds 3000ms", currentOrGapP95))
	}
	return BenchmarkResult{
		SchemaVersion: 1,
		BenchmarkID:   benchmarkID,
		CorpusVersion: benchmarkCorpusVersion,
		CorpusRef:     fixedCorpusRef(),
		TargetVersion: detectCodeVersion(),
		TargetRef:     executableRef(),
		ExecutedAt:    time.Now().UTC().Format(time.RFC3339Nano),
		Profile: BenchmarkProfile{
			OS: runtime.GOOS, Architecture: runtime.GOARCH, CPU: detectCPU(), LogicalCPUs: runtime.NumCPU(), GoVersion: runtime.Version(),
		},
		Metrics: []BenchmarkMetric{
			{Name: "activity_latency_ms", Unit: "ms", Aggregation: "p95", Value: activityP95},
			{Name: "current_or_gap_latency_ms", Unit: "ms", Aggregation: "p95", Value: currentOrGapP95},
			{Name: "benchmark_wall_time_ms", Unit: "ms", Aggregation: "total", Value: float64(time.Since(started).Milliseconds())},
			{Name: "runtime_memory_bytes", Unit: "bytes", Aggregation: "observed_max_sys", Value: float64(peakMemory.Load())},
		},
		Traces: traces, Status: status, Reasons: reasons,
	}, nil
}

func measureLiveSemanticTraces(ctx context.Context, samples int) ([]BenchmarkTrace, error) {
	results := make(chan BenchmarkTrace, samples)
	errorsCh := make(chan error, samples)
	var wg sync.WaitGroup
	for index := 0; index < samples; index++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			if err := ctx.Err(); err != nil {
				errorsCh <- err
				return
			}
			root, err := os.MkdirTemp("", "flowmeter-corpus-v1-")
			if err != nil {
				errorsCh <- err
				return
			}
			defer os.RemoveAll(root)
			if err := os.WriteFile(filepath.Join(root, "service.go"), []byte(benchmarkFixture), 0o644); err != nil {
				errorsCh <- err
				return
			}
			server, err := flowview.NewServer(flowview.Config{RepoRoot: root, Port: 0})
			if err != nil {
				errorsCh <- err
				return
			}
			requestText := "show HandleSubmit flow"
			intent, err := semantic.NormalizeTaskIntent(requestText, semantic.IntentOptions{TaskID: fmt.Sprintf("flowmeter-%02d", index), Revision: 1, Mode: "feature"})
			if err != nil || intent == nil || intent.IntentStatus != "parsed" {
				errorsCh <- fmt.Errorf("sample %d task intent normalization failed: %v", index, err)
				return
			}
			query := &semantic.TaskViewQuery{SchemaID: "https://codeflow.local/schemas/task-view-query.schema.json", SchemaVersion: 1, Mode: "feature", Feature: &semantic.FeatureQueryParams{Request: requestText, EntrySymbol: "service.go#HandleSubmit", Domain: "service"}}
			if err := server.RememberTaskQuery(query, requestText); err != nil {
				errorsCh <- err
				return
			}
			server.Start()
			defer server.Shutdown(context.Background())
			_, snapshot, err := server.SubmitVersionedEdit(ctx, workspace.EditRequest{Path: "service.go", Content: []byte(fmt.Sprintf(benchmarkEditTemplate, index)), DocumentVersion: 1, Source: workspace.SourceIDEVersioned})
			if err != nil {
				errorsCh <- err
				return
			}
			ack := server.CurrentActivity()
			deadline := time.Now().Add(5 * time.Second)
			var terminal workspace.ActivityStatus
			for time.Now().Before(deadline) {
				if err := ctx.Err(); err != nil {
					errorsCh <- err
					return
				}
				terminal = server.CurrentActivity()
				if err := server.LastPipelineError(); err != nil {
					errorsCh <- err
					return
				}
				if !terminal.CurrentOrGapAt.IsZero() {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if ack.TraceID == "" || ack.AcknowledgedAt.IsZero() || ack.ActivityLatencyMs < 0 || terminal.CurrentOrGapAt.IsZero() || terminal.TraceID != ack.TraceID || terminal.CurrentSnapshotID != snapshot.SnapshotID || terminal.CurrentOrGapLatencyMs < 0 {
				errorsCh <- fmt.Errorf("sample %d did not produce same-trace current-or-gap evidence", index)
				return
			}
			results <- BenchmarkTrace{
				TraceID: ack.TraceID, ActivityLatencyMs: float64(ack.ActivityLatencyMs), CurrentOrGapLatencyMs: float64(terminal.CurrentOrGapLatencyMs),
				SnapshotRef: "snapshot:" + snapshot.SnapshotID, TreeRef: "sha256:" + snapshot.RootTreeID, BasisRef: "basis:" + snapshot.ComputedBasisID,
			}
		}(index)
	}
	wg.Wait()
	close(results)
	close(errorsCh)
	for err := range errorsCh {
		return nil, err
	}
	traces := make([]BenchmarkTrace, 0, samples)
	for trace := range results {
		traces = append(traces, trace)
	}
	if len(traces) != samples {
		return nil, fmt.Errorf("expected %d traces, received %d", samples, len(traces))
	}
	sort.Slice(traces, func(i, j int) bool { return traces[i].TraceID < traces[j].TraceID })
	return traces, nil
}

func fixedCorpusRef() string {
	data, _ := json.Marshal(struct {
		Version      string `json:"version"`
		Samples      int    `json:"samples"`
		Fixture      string `json:"fixture"`
		EditTemplate string `json:"editTemplate"`
	}{benchmarkCorpusVersion, benchmarkSampleCount, benchmarkFixture, benchmarkEditTemplate})
	digest := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func detectCodeVersion() string {
	if info, ok := debug.ReadBuildInfo(); ok {
		var revision string
		modified := false
		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs.revision":
				revision = setting.Value
			case "vcs.modified":
				modified = setting.Value == "true"
			}
		}
		if revision != "" {
			if modified {
				revision += "-dirty"
			}
			return revision
		}
	}
	ref := strings.TrimPrefix(executableRef(), "sha256:")
	if len(ref) >= 12 {
		return ref[:12]
	}
	return "local"
}

func executableRef() string {
	path, err := os.Executable()
	if err != nil {
		return "unavailable"
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "unavailable"
	}
	digest := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func detectCPU() string {
	if runtime.GOOS == "darwin" {
		if output, err := exec.Command("sysctl", "-n", "machdep.cpu.brand_string").Output(); err == nil && strings.TrimSpace(string(output)) != "" {
			return strings.TrimSpace(string(output))
		}
	}
	if runtime.GOOS == "linux" {
		if data, err := os.ReadFile("/proc/cpuinfo"); err == nil {
			for _, line := range strings.Split(string(data), "\n") {
				if name, value, ok := strings.Cut(line, ":"); ok && strings.TrimSpace(name) == "model name" {
					return strings.TrimSpace(value)
				}
			}
		}
	}
	return "unknown"
}

func safeBenchmarkPathPart(value string) string {
	return strings.Map(func(character rune) rune {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || character == '.' || character == '-' || character == '_' {
			return character
		}
		return '_'
	}, value)
}
