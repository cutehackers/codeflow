package e2e_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"codeflow/internal/agentgateway/mcp"
	"codeflow/internal/collector/fusion"
	"codeflow/internal/collector/slicing"
	"codeflow/internal/collector/storage"
	"codeflow/internal/curator"
	"codeflow/internal/curator/semantic"
	"codeflow/internal/presenter/flowview"
)

func TestAwaitResumptionSurvivesStorageHTTPAndMCP(t *testing.T) {
	root := t.TempDir()
	source := `async function save() { return 1; }
async function notify() { return 2; }
export async function checkout() { const result = await Promise.all([save(), notify()]); return result.length; }`
	if err := os.WriteFile(filepath.Join(root, "await.ts"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	pool, ctx, cancel := tsAdapterPool(t)
	defer cancel()
	defer pool.Close()
	var payload slicing.SlicedPayload
	if err := pool.Call(ctx, "slice", map[string]any{"repoRoot": root, "candidateId": "cand-awaitflow", "entrySymbolPath": "await.ts#checkout", "opts": map[string]any{"includeExecutionSemantics": true}}, &payload); err != nil {
		t.Fatal(err)
	}
	spec, err := fusion.Fuse(&payload, fusion.FuseOptions{RepoRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	model := semantic.ProjectFlowSpec(spec, "await-generation")
	sequence := semantic.BuildFlowSequence(model)
	if err := semantic.ValidateFlowSequenceReferences(model, sequence); err != nil {
		t.Fatal(err)
	}
	var waiting string
	for _, step := range model.Steps {
		if step.Kind == "await" {
			if waiting != "" {
				t.Fatal("duplicate await")
			}
			waiting = step.StepID
		}
	}
	if waiting == "" {
		t.Fatal("await missing")
	}
	resumed, unknown := false, false
	parallelMembers := map[string]bool{}
	memberIDs := map[string]string{}
	for _, step := range model.Steps {
		if step.Name == "save()" || step.Name == "notify()" {
			memberIDs[step.Name] = step.StepID
		}
	}
	unknownTarget := ""
	for _, edge := range model.Edges {
		if edge.Kind == "parallel_wait" && edge.ToStepID == waiting {
			parallelMembers[edge.FromStepID] = true
		}
		if edge.FromStepID == waiting && edge.Kind == "await_resume" {
			resumed = true
		}
		if edge.FromStepID == waiting && edge.ResolutionStatus != "resolved" {
			unknownTarget = edge.ToSymbolPath
		}
		if edge.FromStepID == waiting && edge.Kind == "control_flow" {
			t.Fatal("await flattened into normal progress")
		}
	}
	for _, item := range model.Unknowns {
		if unknownTarget != "" && item.Subject == unknownTarget && item.Reason == "대기 실패·거절 이후의 예외 경로를 확인하지 못했습니다." {
			unknown = true
		}
	}
	if !resumed || !unknown || !parallelMembers[memberIDs["save()"]] || !parallelMembers[memberIDs["notify()"]] {
		t.Fatalf("await continuation=%v unknown=%v parallel=%+v members=%+v", resumed, unknown, parallelMembers, memberIDs)
	}
	for _, edge := range model.Edges {
		if edge.Kind == "control_flow" && edge.FromStepID == memberIDs["save()"] && edge.ToStepID == memberIDs["notify()"] {
			t.Fatal("parallel members were serialized")
		}
	}
	for _, frame := range sequence.Frames {
		for _, id := range frame.StepRefs {
			if id == waiting && frame.Status == "verified" {
				t.Fatal("await failure analysis promoted")
			}
		}
	}
	viewServer, err := flowview.NewServer(flowview.Config{RepoRoot: root, Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer viewServer.Shutdown(ctx)
	view, err := viewServer.SaveTaskView(ctx, map[string]any{"semanticMap": model, "flowSequence": sequence})
	if err != nil {
		t.Fatal(err)
	}
	id := view["viewId"].(string)
	store := storage.New(root)
	before, err := store.ReadView(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/view?viewId="+id+"&token="+viewServer.AuthToken(), nil)
	viewServer.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("HTTP %d: %s", recorder.Code, recorder.Body.String())
	}
	var restored struct {
		Sequence semantic.FlowSequence  `json:"flowSequence"`
		Map      semantic.SemanticMapIR `json:"semanticMap"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &restored); err != nil {
		t.Fatal(err)
	}
	assertLargeHierarchyJSON(t, "HTTP hierarchy", sequence, restored.Sequence)
	assertLargeHierarchyJSON(t, "HTTP steps", model.Steps, restored.Map.Steps)
	assertLargeHierarchyJSON(t, "HTTP edges", model.Edges, restored.Map.Edges)
	assertLargeHierarchyJSON(t, "HTTP unknowns", model.Unknowns, restored.Map.Unknowns)
	server, err := mcp.NewServer(mcp.Config{RepoRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	for _, format := range []string{"full", "compact"} {
		t.Run(format, func(t *testing.T) {
			input, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "get_flow_payload", "arguments": map[string]any{"viewId": id, "format": format}}})
			if err != nil {
				t.Fatal(err)
			}
			var output bytes.Buffer
			if err := server.Serve(ctx, bytes.NewReader(append(input, '\n')), &output); err != nil {
				t.Fatal(err)
			}
			var response struct {
				Result struct {
					IsError bool                    `json:"isError"`
					Content []struct{ Text string } `json:"content"`
				} `json:"result"`
			}
			if err := json.Unmarshal(output.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if response.Result.IsError || len(response.Result.Content) != 1 {
				t.Fatalf("MCP did not return exact await view: %s", output.String())
			}
			if format == "full" {
				var actual struct {
					Sequence semantic.FlowSequence  `json:"flowSequence"`
					Map      semantic.SemanticMapIR `json:"semanticMap"`
				}
				if err := json.Unmarshal([]byte(response.Result.Content[0].Text), &actual); err != nil {
					t.Fatal(err)
				}
				assertLargeHierarchyJSON(t, "MCP full hierarchy", sequence, actual.Sequence)
				assertLargeHierarchyJSON(t, "MCP full edges", model.Edges, actual.Map.Edges)
				assertLargeHierarchyJSON(t, "MCP full unknowns", model.Unknowns, actual.Map.Unknowns)
			} else {
				var actual curator.CompactFlowPayload
				if err := json.Unmarshal([]byte(response.Result.Content[0].Text), &actual); err != nil {
					t.Fatal(err)
				}
				if len(actual.Steps) != len(model.Steps) || len(actual.Frames) != len(sequence.Frames) {
					t.Fatal("MCP compact truncated source references or frames")
				}
				for i, step := range actual.Steps {
					want := model.Steps[i]
					if step.Ordinal != want.Ordinal || step.StepID != want.StepID || step.Kind != want.Kind || step.InvocationID != want.InvocationID {
						t.Fatalf("MCP compact changed step %d", i)
					}
					assertLargeHierarchyJSON(t, "MCP compact source location", want.Anchor, step.Anchor)
					assertLargeHierarchyJSON(t, "MCP compact caller", want.CallerStepOrdinal, step.CallerStepOrdinal)
				}
				assertLargeHierarchyJSON(t, "MCP compact edges", model.Edges, actual.Edges)
				assertLargeHierarchyJSON(t, "MCP compact unknowns", model.Unknowns, actual.Unknowns)
				assertLargeHierarchyJSON(t, "MCP compact summary limitations", sequence.SummaryLimitations, actual.SummaryLimitations)
				for i, frame := range actual.Frames {
					want := sequence.Frames[i]
					if frame.ID != want.FrameID || frame.Ordinal != want.Ordinal || frame.Gate != want.Role || frame.PrimaryStepRef != want.PrimaryStepRef {
						t.Fatalf("MCP compact changed frame %d", i)
					}
					assertLargeHierarchyJSON(t, "MCP compact membership", want.StepRefs, frame.StepRefs)
				}
			}
		})
	}
	after, err := store.ReadView(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("reading rewrote the persisted analysis")
	}
}

func TestSimultaneousLimitationsSurviveTransport(t *testing.T) {
	root := t.TempDir()
	// Source contains:
	// 1. await with catch recovery (failure edge)
	// 2. external unresolvable call (unknown boundary)
	source := `export async function process() {
  try {
    await runExternal();
  } catch (e) {
    handleFailure();
  }
  lookupRemote();
  finish();
}
async function runExternal() { return 1; }
function handleFailure() {}
function finish() {}`
	if err := os.WriteFile(filepath.Join(root, "limitations.ts"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	pool, ctx, cancel := tsAdapterPool(t)
	defer cancel()
	defer pool.Close()
	var payload slicing.SlicedPayload
	if err := pool.Call(ctx, "slice", map[string]any{
		"repoRoot":        root,
		"candidateId":     "cand-simultlimits",
		"entrySymbolPath": "limitations.ts#process",
		"opts":            map[string]any{"includeExecutionSemantics": true},
	}, &payload); err != nil {
		t.Fatal(err)
	}
	spec, err := fusion.Fuse(&payload, fusion.FuseOptions{RepoRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	model := semantic.ProjectFlowSpec(spec, "simult-limits-generation")

	sequence := semantic.BuildFlowSequence(model)
	if err := semantic.ValidateFlowSequenceReferences(model, sequence); err != nil {
		t.Fatal(err)
	}

	// Verify all 3 limitation categories coexist:
	// Category 1: Summary limitation (grouping_evidence_missing)
	hasSummaryLimitation := len(sequence.SummaryLimitations) > 0
	// Category 2: Boundary / unknown target
	hasUnknownTarget := len(model.Unknowns) > 0
	// Category 3: Failure edge
	hasFailureEdge := false
	for _, edge := range model.Edges {
		if edge.Kind == "failure" {
			hasFailureEdge = true
			break
		}
	}

	if !hasSummaryLimitation || !hasUnknownTarget || !hasFailureEdge {
		t.Fatalf("expected all 3 limitations to coexist: summaryLimitation=%v unknownTarget=%v failureEdge=%v",
			hasSummaryLimitation, hasUnknownTarget, hasFailureEdge)
	}

	viewServer, err := flowview.NewServer(flowview.Config{RepoRoot: root, Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer viewServer.Shutdown(ctx)
	view, err := viewServer.SaveTaskView(ctx, map[string]any{"semanticMap": model, "flowSequence": sequence})
	if err != nil {
		t.Fatal(err)
	}
	id := view["viewId"].(string)

	// Verify HTTP returns all 3 limitations
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/view?viewId="+id+"&token="+viewServer.AuthToken(), nil)
	viewServer.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("HTTP %d: %s", recorder.Code, recorder.Body.String())
	}
	var restored struct {
		Sequence semantic.FlowSequence  `json:"flowSequence"`
		Map      semantic.SemanticMapIR `json:"semanticMap"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &restored); err != nil {
		t.Fatal(err)
	}
	if len(restored.Sequence.SummaryLimitations) != len(sequence.SummaryLimitations) {
		t.Fatal("HTTP lost summary limitations")
	}
	if len(restored.Map.Unknowns) != len(model.Unknowns) {
		t.Fatal("HTTP lost unknowns")
	}

	// Verify MCP returns all 3 limitations in both full and compact formats
	server, err := mcp.NewServer(mcp.Config{RepoRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()

	for _, format := range []string{"full", "compact"} {
		input, _ := json.Marshal(map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"method":  "tools/call",
			"params": map[string]any{
				"name":      "get_flow_payload",
				"arguments": map[string]any{"viewId": id, "format": format},
			},
		})
		var output bytes.Buffer
		if err := server.Serve(ctx, bytes.NewReader(append(input, '\n')), &output); err != nil {
			t.Fatal(err)
		}
		var response struct {
			Result struct {
				IsError bool                    `json:"isError"`
				Content []struct{ Text string } `json:"content"`
			} `json:"result"`
		}
		if err := json.Unmarshal(output.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if response.Result.IsError || len(response.Result.Content) != 1 {
			t.Fatalf("MCP error for format %s: %s", format, output.String())
		}
		if format == "compact" {
			var actual curator.CompactFlowPayload
			if err := json.Unmarshal([]byte(response.Result.Content[0].Text), &actual); err != nil {
				t.Fatal(err)
			}
			if len(actual.SummaryLimitations) == 0 {
				t.Fatal("MCP compact lost summary limitations")
			}
			if len(actual.Unknowns) == 0 {
				t.Fatal("MCP compact lost unknowns")
			}
			foundFail := false
			for _, edge := range actual.Edges {
				if edge.Kind == "failure" {
					foundFail = true
					break
				}
			}
			if !foundFail {
				t.Fatal("MCP compact lost failure edge")
			}
		}
	}
}
