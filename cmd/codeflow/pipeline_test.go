package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

var (
	pipelineBinaryPreparationOnce  sync.Once
	pipelineBinaryPath             string
	pipelineBinaryPreparationError error
)

func buildBinary(t *testing.T) string {
	t.Helper()
	pipelineBinaryPreparationOnce.Do(func() {
		tempDir, err := os.MkdirTemp("", "codeflow-pipeline-bin-*")
		if err != nil {
			pipelineBinaryPreparationError = err
			return
		}
		binPath := filepath.Join(tempDir, "codeflow-test-bin")
		cmd := exec.Command("go", "build", "-o", binPath, ".")
		cmd.Dir = "."
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
		if out, err := cmd.CombinedOutput(); err != nil {
			pipelineBinaryPreparationError = fmt.Errorf("build codeflow binary failed: %w\nOutput: %s", err, string(out))
			return
		}
		pipelineBinaryPath = binPath
	})
	if pipelineBinaryPreparationError != nil {
		t.Fatalf("build codeflow binary failed: %v", pipelineBinaryPreparationError)
	}
	return pipelineBinaryPath
}

func TestPipeline_StandaloneAnalyze(t *testing.T) {
	bin := buildBinary(t)

	cmd := exec.Command(bin, "analyze", "../..")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("analyze failed: %v\nOutput: %s", err, string(out))
	}

	var res map[string]any
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("analyze output is not valid JSON: %v\nOutput: %s", err, string(out))
	}

	if res["language"] != "go" {
		t.Errorf("expected language go, got: %v", res["language"])
	}
	if res["confident"] != true {
		t.Errorf("expected confident true, got: %v", res["confident"])
	}
}

func TestPipeline_StandaloneCollect(t *testing.T) {
	bin := buildBinary(t)

	cmd := exec.Command(bin, "collect", "OrderService.pay")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("collect failed: %v\nOutput: %s", err, string(out))
	}

	var res map[string]any
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("collect output is not valid JSON: %v\nOutput: %s", err, string(out))
	}

	if res["flowId"] == nil || res["flowId"] == "" {
		t.Errorf("expected non-empty flowId in trace, got: %v", res["flowId"])
	}
	steps, _ := res["steps"].([]any)
	if len(steps) != 1 {
		t.Fatalf("expected 1 step in trace, got %d", len(steps))
	}
}

func TestPipeline_StandaloneCurateFromStdin(t *testing.T) {
	bin := buildBinary(t)

	sampleTrace := `{
		"flowId": "flow-test-123",
		"steps": [
			{
				"stepId": "s1",
				"ordinal": 1,
				"name": "OrderService.pay",
				"kind": "entry",
				"anchor": { "enclosingSymbolPath": "order.go#Pay" }
			},
			{
				"stepId": "s2",
				"ordinal": 2,
				"name": "CompleteOrder",
				"kind": "result",
				"anchor": { "enclosingSymbolPath": "order.go#Complete" }
			}
		]
	}`

	cmd := exec.Command(bin, "curate", "-")
	cmd.Stdin = strings.NewReader(sampleTrace)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("curate failed: %v\nOutput: %s", err, string(out))
	}

	var res map[string]any
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("curate output is not valid JSON: %v\nOutput: %s", err, string(out))
	}

	frames, _ := res["frames"].([]any)
	if len(frames) != 2 {
		t.Fatalf("expected 2 frames, got %d", len(frames))
	}
	if limitations, ok := res["summaryLimitations"].([]any); !ok || len(limitations) == 0 {
		t.Fatal("CLI omitted hierarchy limitations")
	}
	for _, raw := range frames {
		if raw.(map[string]any)["status"] == "verified" {
			t.Fatal("raw trace without evidence promoted to verified")
		}
	}

}

func TestPipeline_UnixPiping_CollectCurateView(t *testing.T) {
	bin := buildBinary(t)

	// Step 1: collect
	collectCmd := exec.Command(bin, "collect", "OrderService.pay")
	var collectOut bytes.Buffer
	collectCmd.Stdout = &collectOut
	if err := collectCmd.Run(); err != nil {
		t.Fatalf("collectCmd failed: %v", err)
	}

	// Step 2: curate
	curateCmd := exec.Command(bin, "curate", "-")
	curateCmd.Stdin = &collectOut
	var curateOut bytes.Buffer
	curateCmd.Stdout = &curateOut
	if err := curateCmd.Run(); err != nil {
		t.Fatalf("curateCmd failed: %v", err)
	}

	// Step 3: view --dry-run
	viewCmd := exec.Command(bin, "view", "--dry-run", "-")
	viewCmd.Stdin = &curateOut
	viewCmd.Env = append(os.Environ(), "CODEFLOW_NONINTERACTIVE=1")
	viewOut, err := viewCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("viewCmd failed: %v\nOutput: %s", err, string(viewOut))
	}

	if !strings.Contains(string(viewOut), "FlowSequence 검증 성공") {
		t.Errorf("expected validation success message, got: %s", string(viewOut))
	}
}

func TestPipeline_Curate_BrokenJSON_Exits1(t *testing.T) {
	bin := buildBinary(t)

	cmd := exec.Command(bin, "curate", "-")
	cmd.Stdin = strings.NewReader("not-a-valid-json-content")
	err := cmd.Run()
	if err == nil {
		t.Fatalf("expected curate to exit with non-zero on broken JSON, but succeeded")
	}
}

func TestPipeline_View_EmptyFrames_DisplaysNotice(t *testing.T) {
	bin := buildBinary(t)

	emptyFlowSequence := `{"flowId": "flow-empty", "frames": []}`
	cmd := exec.Command(bin, "view", "--dry-run", "-")
	cmd.Stdin = strings.NewReader(emptyFlowSequence)
	cmd.Env = append(os.Environ(), "CODEFLOW_NONINTERACTIVE=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("view failed: %v\nOutput: %s", err, string(out))
	}

	if !strings.Contains(string(out), "빈 FlowSequence") {
		t.Errorf("expected empty FlowSequence notice, got: %s", string(out))
	}
}

func TestArchitecture_StrictUnidirectionalDependency(t *testing.T) {
	// Verify that internal packages strictly maintain unidirectional DAG:
	// analyzer -> collector -> curator -> (presenter, agentgateway)
	// No reverse imports allowed.
	pkgs := []string{
		"codeflow/internal/analyzer",
		"codeflow/internal/collector",
		"codeflow/internal/curator",
		"codeflow/internal/presenter",
		"codeflow/internal/agentgateway",
	}

	for _, p := range pkgs {
		cmd := exec.Command("go", "list", "-f", "{{range .Imports}}{{.}}\n{{end}}", p)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("go list %s failed: %v", p, err)
		}
		imports := strings.Split(string(out), "\n")

		switch p {
		case "codeflow/internal/analyzer":
			for _, imp := range imports {
				if strings.Contains(imp, "collector") || strings.Contains(imp, "curator") || strings.Contains(imp, "presenter") || strings.Contains(imp, "agentgateway") {
					t.Fatalf("analyzer has forbidden downstream import: %s", imp)
				}
			}
		case "codeflow/internal/collector":
			for _, imp := range imports {
				if strings.Contains(imp, "curator") || strings.Contains(imp, "presenter") || strings.Contains(imp, "agentgateway") {
					t.Fatalf("collector has forbidden downstream import: %s", imp)
				}
			}
		case "codeflow/internal/curator":
			for _, imp := range imports {
				if strings.Contains(imp, "presenter") || strings.Contains(imp, "agentgateway") {
					t.Fatalf("curator has forbidden downstream import: %s", imp)
				}
			}
		}
	}
}

func TestCuratePreservesAnalysisLimitations(t *testing.T) {
	bin := buildBinary(t)
	input := `{"flowId":"flow-limited","truncated":true,"steps":[{"ordinal":1,"stepId":"entry","name":"entry"},{"ordinal":2,"stepId":"lookup","name":"lookup","kind":"call"}],"edges":[{"stepOrdinal":2,"kind":"unknown_edge","toSymbolPath":"external#lookup","resolutionStatus":"unresolved","unresolvedReason":"target unavailable"}]}`
	command := exec.Command(bin, "curate", "-")
	command.Stdin = strings.NewReader(input)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("curate: %v: %s", err, output)
	}
	var result struct {
		Frames []struct {
			Role   string `json:"role"`
			Status string `json:"status"`
		} `json:"frames"`
		Steps    []json.RawMessage `json:"steps"`
		Edges    []json.RawMessage `json:"edges"`
		Unknowns []struct {
			Subject string `json:"subject"`
			Reason  string `json:"reason"`
		} `json:"unknowns"`
	}
	if err := json.Unmarshal(output, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Frames) != 2 || result.Frames[1].Role != "boundary" || result.Frames[1].Status != "unknown" {
		t.Fatalf("boundary lost: %s", output)
	}
	if len(result.Steps) != 2 || len(result.Edges) != 1 {
		t.Fatalf("source graph lost: %s", output)
	}
	reasons := map[string]string{}
	for _, item := range result.Unknowns {
		reasons[item.Subject] = item.Reason
	}
	if reasons["external#lookup"] != "target unavailable" || reasons["flow-traversal"] == "" {
		t.Fatalf("analysis limitations lost: %s", output)
	}
}

func TestCurateRejectsDuplicateStepReferences(t *testing.T) {
	command := exec.Command(buildBinary(t), "curate", "-")
	command.Stdin = strings.NewReader(`{"flowId":"flow-invalid","steps":[{"stepId":"same"},{"stepId":"same"}]}`)
	output, err := command.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "duplicate analysis step") {
		t.Fatalf("duplicate references accepted: %v: %s", err, output)
	}
}

func TestCurateRedactsTraceBeforeDerivingOutput(t *testing.T) {
	command := exec.Command(buildBinary(t), "curate", "-")
	command.Stdin = strings.NewReader(`{"flowId":"flow-redaction","steps":[{"ordinal":1,"stepId":"start","name":"password='private-name'","stateDelta":{"before":"token='private-before'","after":"token='private-after'"}}],"unknowns":[{"subject":"lookup","reason":"api_key='private-reason'"}]}`)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("curate: %v: %s", err, output)
	}
	for _, value := range []string{"private-name", "private-before", "private-after", "private-reason"} {
		if strings.Contains(string(output), value) {
			t.Fatalf("secret retained in curated output: %s", value)
		}
	}
	if !json.Valid(output) || !strings.Contains(string(output), "REDACTED") {
		t.Fatal("redaction damaged JSON or missed fixture")
	}
}
