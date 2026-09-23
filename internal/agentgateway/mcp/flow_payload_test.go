package mcp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"codeflow/internal/agentgateway/mcp"
	"codeflow/internal/collector/fusion"
	"codeflow/internal/collector/slicing"
	"codeflow/internal/collector/storage"
	"codeflow/internal/curator"
	"codeflow/internal/curator/semantic"
	"codeflow/internal/presenter/flowview"
)

func TestFlowPayloadMatchesExactStoredHTTPHierarchy(t *testing.T) {
	root := t.TempDir()
	ctx := context.Background()
	fv, err := flowview.NewServer(flowview.Config{RepoRoot: root, Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer fv.Shutdown(ctx)
	m := &semantic.SemanticMapIR{MapID: "map-checkout", GenerationID: "generation", ComputedBasisID: "basis", ValidatedAgainstSnapshotID: "snapshot", Steps: []semantic.SemanticStep{
		{StepID: "first", Ordinal: 1, Name: "Input", Kind: "guard", InvocationID: "root"},
		{StepID: "second", Ordinal: 2, Name: "Required fields", Kind: "guard", InvocationID: "root"},
	}}
	for _, outcome := range []string{"truthy", "falsy"} {
		m.Edges = append(m.Edges, semantic.SemanticEdge{FromStepID: "first", ToStepID: "second", Kind: "control_flow", ResolutionStatus: "resolved", Conditions: []semantic.SemanticBranchCondition{{StepID: "first", Outcome: outcome}}})
	}
	m.Unknowns = []fusion.Unknown{{Subject: "external", Reason: "target not analyzed"}}
	for i := range m.Steps {
		m.Steps[i].Anchor = slicing.Anchor{RepoRelativePath: "flow.ts", EnclosingSymbolPath: "run", ByteRange: [2]int{i * 10, i*10 + 5}}
	}
	seq := semantic.BuildFlowSequence(m)
	seq.SummaryLimitations = []semantic.FlowSummaryLimitation{{Code: "grouping_evidence_missing", Message: "그룹 의미 미확인", FrameRefs: []string{"input-validation"}}}
	seq.Frames = []semantic.FlowSequenceFrame{{FrameID: "input-validation", Ordinal: 1, Role: "decision", Title: "Validate input", Status: "partial", StepRefs: []string{"first", "second"}, PrimaryStepRef: "second", FrameMatchKey: "decision|input"}}
	view, err := fv.SaveTaskView(ctx, map[string]any{"semanticMap": m, "flowSequence": seq})
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
	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/view?viewId="+id+"&token="+fv.AuthToken(), nil)
	fv.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("HTTP %d: %s", recorder.Code, recorder.Body.String())
	}
	var httpView struct {
		Sequence semantic.FlowSequence  `json:"flowSequence"`
		Map      semantic.SemanticMapIR `json:"semanticMap"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &httpView); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(httpView.Map.Edges, m.Edges) {
		t.Fatal("stored HTTP conditions differ")
	}
	server, err := mcp.NewServer(mcp.Config{RepoRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	for _, format := range []string{"full", "compact"} {
		t.Run(format, func(t *testing.T) {
			result, isError := invokeFlowPayload(t, server, map[string]any{"viewId": id, "format": format})
			if isError {
				t.Fatalf("MCP rejected exact view: %s", result)
			}
			if format == "full" {
				var actual struct {
					Sequence semantic.FlowSequence  `json:"flowSequence"`
					Map      semantic.SemanticMapIR `json:"semanticMap"`
				}
				if err := json.Unmarshal(result, &actual); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(actual.Map.Edges, m.Edges) {
					t.Fatal("stored MCP conditions differ")
				}
				if !reflect.DeepEqual(actual.Sequence, httpView.Sequence) {
					t.Fatal("HTTP and MCP hierarchy differ")
				}
			} else {
				var graph struct {
					Edges    []semantic.SemanticEdge `json:"edges"`
					Unknowns []fusion.Unknown        `json:"unknowns"`
					Steps    []semantic.SemanticStep `json:"steps"`
				}
				if err := json.Unmarshal(result, &graph); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(graph.Unknowns, m.Unknowns) {
					t.Fatal("compact lost analysis limitations")
				}
				if !reflect.DeepEqual(graph.Edges, httpView.Map.Edges) {
					t.Fatal("compact lost conditional relationships")
				}
				if len(graph.Steps) != len(m.Steps) {
					t.Fatal("compact lost relationship source steps")
				}
				for i, step := range graph.Steps {
					if step.StepID != m.Steps[i].StepID || step.Kind != m.Steps[i].Kind || step.InvocationID != m.Steps[i].InvocationID || !reflect.DeepEqual(step.Anchor, m.Steps[i].Anchor) {
						t.Fatal("compact changed condition source")
					}
				}
				var actual curator.CompactFlowPayload
				if err := json.Unmarshal(result, &actual); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(actual.SummaryLimitations, httpView.Sequence.SummaryLimitations) {
					t.Fatal("summary limitations differ")
				}
				f := httpView.Sequence.Frames[0]
				if len(actual.Frames) != 1 {
					t.Fatalf("compact recuration: %+v", actual.Frames)
				}
				if actual.RadarSummary != nil {
					t.Fatal("unmeasured relations presented as zero")
				}
				cf := actual.Frames[0]
				if cf.Status != f.Status || cf.ID != f.FrameID || cf.Ordinal != f.Ordinal || cf.Gate != f.Role || cf.PrimaryStepRef != f.PrimaryStepRef || cf.Steps != len(f.StepRefs) || !reflect.DeepEqual(cf.StepRefs, f.StepRefs) {
					t.Fatalf("compact changed hierarchy: %+v", cf)
				}
			}
		})
	}
	_, isError := invokeFlowPayload(t, server, map[string]any{"viewId": id, "flowId": "checkout"})
	if !isError {
		t.Fatal("ambiguous target accepted")
	}
	_, isError = invokeFlowPayload(t, server, map[string]any{"viewId": "../outside"})
	if !isError {
		t.Fatal("invalid storage ID accepted")
	}
	after, err := store.ReadView(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("MCP query rewrote analysis")
	}
}

func invokeFlowPayload(t *testing.T, server *mcp.Server, args map[string]any) (json.RawMessage, bool) {
	t.Helper()
	input, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "get_flow_payload", "arguments": args}})
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := server.Serve(context.Background(), bytes.NewReader(append(input, '\n')), &output); err != nil {
		t.Fatal(err)
	}
	var response struct {
		Result struct {
			IsError bool `json:"isError"`
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if err := json.Unmarshal(output.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Result.Content) != 1 {
		t.Fatalf("unexpected MCP response: %s", output.String())
	}
	return json.RawMessage(response.Result.Content[0].Text), response.Result.IsError
}
