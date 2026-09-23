package e2e_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codeflow/internal/agentgateway/mcp"
	"codeflow/internal/collector/fusion"
	"codeflow/internal/collector/slicing"
	"codeflow/internal/collector/storage"
	"codeflow/internal/curator"
	"codeflow/internal/curator/semantic"
	"codeflow/internal/presenter/flowview"
)

func TestLargeSourceHierarchySurvivesStorageHTTPAndMCP(t *testing.T) {
	root := t.TempDir()
	checks := []string{"authenticated", "authorized", "addressValid", "stockAvailable", "paymentAllowed", "limitAvailable", "currencySupported", "termsAccepted"}
	var source strings.Builder
	source.WriteString("export function processOrder(request: any) {\n")
	for _, check := range checks {
		fmt.Fprintf(&source, "  if (!request.%s) return '%s';\n", check, check)
	}
	source.WriteString("  let total = request.amount;\n")
	for i := 1; i <= 80; i++ {
		fmt.Fprintf(&source, "  total = applyAdjustment%02d(total);\n", i)
	}
	source.WriteString("  return total;\n}\n")
	for i := 1; i <= 80; i++ {
		fmt.Fprintf(&source, "function applyAdjustment%02d(value: number) { return value + %d; }\n", i, i)
	}
	if err := os.WriteFile(filepath.Join(root, "order.ts"), []byte(source.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	pool, ctx, cancel := tsAdapterPool(t)
	defer cancel()
	defer pool.Close()
	var payload slicing.SlicedPayload
	if err := pool.Call(ctx, "slice", map[string]any{
		"repoRoot": root, "candidateId": "cand-largeorder", "entrySymbolPath": "order.ts#processOrder",
		"opts": map[string]any{"includeExecutionSemantics": true},
	}, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Truncated || len(payload.Steps) != 257 {
		t.Fatalf("source execution steps lost: count=%d, truncated=%v", len(payload.Steps), payload.Truncated)
	}
	assertLargeOrderConnections(t, payload, checks)
	if err := slicing.ValidateExecutionReferences(payload.Steps, payload.Edges); err != nil {
		t.Fatal(err)
	}
	spec, err := fusion.Fuse(&payload, fusion.FuseOptions{RepoRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	model := semantic.ProjectFlowSpec(spec, "large-order-generation")
	sequence := semantic.BuildFlowSequence(model)
	if err := semantic.ValidateFlowSequenceReferences(model, sequence); err != nil {
		t.Fatal(err)
	}
	parents := make(map[string]string)
	for _, frame := range sequence.Frames {
		for _, id := range frame.StepRefs {
			if parents[id] != "" {
				t.Fatalf("duplicate parent for %s", id)
			}
			parents[id] = frame.FrameID
		}
	}
	if len(parents) != 257 {
		t.Fatalf("hierarchy contains %d steps, want 257", len(parents))
	}
	assignmentCount := 0
	ordinalParents := make(map[int]string)
	stepsByOrdinal := make(map[int]semantic.SemanticStep)
	returnsByCaller := make(map[int][]semantic.SemanticStep)
	framesByID := make(map[string]semantic.FlowSequenceFrame)
	for _, frame := range sequence.Frames {
		framesByID[frame.FrameID] = frame
	}
	for _, step := range model.Steps {
		ordinalParents[step.Ordinal] = parents[step.StepID]
		stepsByOrdinal[step.Ordinal] = step
		if step.Kind == "return" && step.CallerStepOrdinal != nil {
			returnsByCaller[*step.CallerStepOrdinal] = append(returnsByCaller[*step.CallerStepOrdinal], step)
		}
	}
	for _, step := range model.Steps {
		if step.Kind != "mutation" {
			continue
		}
		if step.AssignmentSourceOrdinal == nil {
			t.Fatal("adapter assignment value source lost")
		}
		if ordinalParents[*step.AssignmentSourceOrdinal] != parents[step.StepID] {
			t.Fatal("direct call result and assignment separated")
		}
		frame := framesByID[parents[step.StepID]]
		returns := returnsByCaller[*step.AssignmentSourceOrdinal]
		if len(returns) != 1 || len(frame.StepRefs) != 3 || frame.PrimaryStepRef != step.StepID || frame.StepRefs[0] != stepsByOrdinal[*step.AssignmentSourceOrdinal].StepID || frame.StepRefs[1] != returns[0].StepID || frame.StepRefs[2] != step.StepID {
			t.Fatalf("call, single callee return, and assignment were not one value transfer: %+v", frame)
		}
		assignmentCount++
	}
	if assignmentCount != 80 || len(sequence.Frames) != 97 {
		t.Fatalf("value grouping count=%d frames=%d", assignmentCount, len(sequence.Frames))
	}
	guardParents := make(map[string]bool)
	kinds := make(map[string]int)
	for _, step := range model.Steps {
		kinds[step.Kind]++
		if step.Kind == "guard" {
			guardParents[parents[step.StepID]] = true
		}
	}
	if len(guardParents) != 8 || kinds["call"] != 80 || kinds["mutation"] != 80 || kinds["return"] != 89 {
		t.Fatalf("source decisions/calculations lost: guards=%d, kinds=%v", len(guardParents), kinds)
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
				t.Fatalf("MCP did not return exact large view: %s", output.String())
			}
			if format == "full" {
				var actual struct {
					Sequence semantic.FlowSequence `json:"flowSequence"`
				}
				if err := json.Unmarshal([]byte(response.Result.Content[0].Text), &actual); err != nil {
					t.Fatal(err)
				}
				assertLargeHierarchyJSON(t, "MCP full hierarchy", sequence, actual.Sequence)
			} else {
				var actual curator.CompactFlowPayload
				if err := json.Unmarshal([]byte(response.Result.Content[0].Text), &actual); err != nil {
					t.Fatal(err)
				}
				if len(actual.Steps) != 257 || len(actual.Frames) != len(sequence.Frames) {
					t.Fatal("MCP compact truncated source references or frames")
				}
				for i, step := range actual.Steps {
					want := model.Steps[i]
					if step.Ordinal != want.Ordinal || step.StepID != want.StepID || step.Kind != want.Kind || step.InvocationID != want.InvocationID {
						t.Fatalf("MCP compact changed step %d", i)
					}
					assertLargeHierarchyJSON(t, "MCP compact source location", want.Anchor, step.Anchor)
					assertLargeHierarchyJSON(t, "MCP compact assignment source", want.AssignmentSourceOrdinal, step.AssignmentSourceOrdinal)
				}
				assertLargeHierarchyJSON(t, "MCP compact edges", model.Edges, actual.Edges)
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
	actualSource, err := os.ReadFile(filepath.Join(root, "order.ts"))
	if err != nil {
		t.Fatal(err)
	}
	if string(actualSource) != source.String() {
		t.Fatal("analysis or navigation changed source code")
	}
}

// Compare the public JSON values, where omitted empty optional fields decode
// as nil slices without changing the contract.
func assertLargeHierarchyJSON(t *testing.T, label string, want, got any) {
	t.Helper()
	expected, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(expected, actual) {
		t.Fatalf("%s changed after transport", label)
	}
}

// Derive the expected graph from the fixture's source structure, independently
// of the curator: reject or continue, call callee, then assign and continue.
func assertLargeOrderConnections(t *testing.T, payload slicing.SlicedPayload, checks []string) {
	t.Helper()
	ordinals := make(map[string]int)
	for _, step := range payload.Steps {
		key := step.SymbolPath + "|" + step.Description
		if ordinals[key] != 0 {
			t.Fatalf("duplicate source step %q", key)
		}
		ordinals[key] = step.Ordinal
	}
	ordinal := func(symbol, description string) int {
		t.Helper()
		value := ordinals[symbol+"|"+description]
		if value == 0 {
			t.Fatalf("missing source step %s: %s", symbol, description)
		}
		return value
	}
	key := func(kind string, from, to int, outcome string) string {
		return fmt.Sprintf("%s:%d:%d:%s", kind, from, to, outcome)
	}
	expected := make(map[string]bool)
	for i, check := range checks {
		from := ordinal("processOrder", "if (!request."+check+") return")
		rejected := ordinal("processOrder", "return '"+check+"';")
		next := "applyAdjustment01(total)"
		if i+1 < len(checks) {
			next = "if (!request." + checks[i+1] + ") return"
		}
		expected[key("control_flow", from, rejected, "truthy")] = true
		expected[key("control_flow", from, ordinal("processOrder", next), "falsy")] = true
	}
	for i := 1; i <= 80; i++ {
		symbol := fmt.Sprintf("applyAdjustment%02d", i)
		call := ordinal("processOrder", symbol+"(total)")
		assigned := ordinal("processOrder", "total = "+symbol+"(total)")
		returned := ordinal(symbol, fmt.Sprintf("return value + %d;", i))
		next := "return total;"
		if i < 80 {
			next = fmt.Sprintf("applyAdjustment%02d(total)", i+1)
		}
		expected[key("resolved_cross_file", call, returned, "")] = true
		expected[key("return", returned, assigned, "")] = true
		expected[key("control_flow", call, assigned, "")] = true
		expected[key("control_flow", assigned, ordinal("processOrder", next), "")] = true
	}
	if len(payload.Edges) != len(expected) {
		t.Fatalf("connections=%d, want %d", len(payload.Edges), len(expected))
	}
	for _, edge := range payload.Edges {
		if edge.StepOrdinal == nil || edge.TargetStepOrdinal == nil || edge.ResolutionStatus != "resolved" {
			t.Fatalf("unexpected unresolved relationship: %+v", edge)
		}
		outcome := ""
		if len(edge.Conditions) != 0 {
			if len(edge.Conditions) != 1 || edge.Conditions[0].StepOrdinal != *edge.StepOrdinal {
				t.Fatalf("invalid decision source: %+v", edge)
			}
			outcome = edge.Conditions[0].Outcome
		}
		id := key(edge.Kind, *edge.StepOrdinal, *edge.TargetStepOrdinal, outcome)
		if !expected[id] {
			t.Fatalf("unexpected or duplicate connection: %s", id)
		}
		delete(expected, id)
	}
	if len(expected) != 0 {
		t.Fatalf("missing source connections: %v", expected)
	}
}
