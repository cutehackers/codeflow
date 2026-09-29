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
		{StepID: "second", Ordinal: 2, Name: "Required fields", Kind: "mutation", ValueRole: "local_binding", InvocationID: "root"},
		{StepID: "call", Ordinal: 3, Name: "Calculate fee", Kind: "call", InvocationID: "root", EvidenceRefs: []string{"call-source"}},
		{StepID: "entry", Ordinal: 4, Name: "Fee result", Kind: "mutation", InvocationID: "fee", EvidenceRefs: []string{"entry-source"}},
	}}
	callOrdinal := 3
	m.Steps[3].CallerStepOrdinal = &callOrdinal
	for _, outcome := range []string{"truthy", "falsy"} {
		m.Edges = append(m.Edges, semantic.SemanticEdge{FromStepID: "first", ToStepID: "second", Kind: "control_flow", ResolutionStatus: "resolved", Conditions: []semantic.SemanticBranchCondition{{StepID: "first", Outcome: outcome}}})
	}
	m.Edges = append(m.Edges,
		semantic.SemanticEdge{FromStepID: "second", ToStepID: "call", Kind: "control_flow", ResolutionStatus: "resolved"},
		semantic.SemanticEdge{FromStepID: "call", ToStepID: "entry", ToSymbolPath: "fee.ts#calculateFee", Kind: "resolved_cross_file", ResolutionStatus: "resolved"},
	)
	m.Unknowns = []fusion.Unknown{{Subject: "external", Reason: "target not analyzed"}}
	for i := range m.Steps {
		m.Steps[i].Anchor = slicing.Anchor{RepoRelativePath: "flow.ts", EnclosingSymbolPath: "run", ByteRange: [2]int{i * 10, i*10 + 5}}
	}
	m.Steps[3].Anchor.RepoRelativePath = "fee.ts"
	m.Steps[3].Anchor.EnclosingSymbolPath = "calculateFee"
	m.Evidence = []semantic.SemanticEvidence{
		{EvidenceID: "call-source", SnapshotID: "snapshot", Anchor: m.Steps[2].Anchor, ValidationStatus: "verified", SourceValidationStatus: "verified"},
		{EvidenceID: "entry-source", SnapshotID: "snapshot", Anchor: m.Steps[3].Anchor, ValidationStatus: "verified", SourceValidationStatus: "verified"},
	}
	seq := semantic.BuildFlowSequence(m)
	seq.SummaryLimitations = []semantic.FlowSummaryLimitation{{Code: "grouping_evidence_missing", Message: "그룹 의미 미확인", FrameRefs: []string{"input-validation"}}}
	seq.Frames = []semantic.FlowSequenceFrame{{FrameID: "input-validation", Ordinal: 1, Role: "decision", Title: "Validate input", Status: "partial", StepRefs: []string{"first", "second", "call", "entry"}, PrimaryStepRef: "second", FrameMatchKey: "decision|input"}}
	focus, err := semantic.ResolveRequestedFlowFocus(m, "fee.ts#calculateFee", "Fee calculation explains this request")
	if err != nil {
		t.Fatal(err)
	}
	selection := &fusion.FlowResolution{
		SchemaVersion: 1, Status: "resolved", RawRequest: "입력 검증", CandidateID: "cand-test",
		EntrySymbolPath: "flow.ts#run", FlowID: "flow-test",
		Evidence: []fusion.FlowEvidence{{CandidateID: "cand-test", EntrySymbolPath: "flow.ts#run", Description: "선택한 함수", SnapshotID: "snapshot", ComputedBasisID: "basis", SourceAnchor: &m.Steps[0].Anchor}},
	}
	view, err := fv.SaveTaskView(ctx, map[string]any{"semanticMap": m, "flowSequence": seq, "flowResolution": selection, "request": semantic.FeatureQueryParams{FocusSymbol: focus.SymbolPath, FocusReason: focus.Reason}, "requestedFocus": focus})
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
		Sequence   semantic.FlowSequence        `json:"flowSequence"`
		Map        semantic.SemanticMapIR       `json:"semanticMap"`
		Resolution *fusion.FlowResolution       `json:"flowResolution"`
		Focus      *semantic.RequestedFlowFocus `json:"requestedFocus"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &httpView); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(httpView.Map.Edges, m.Edges) {
		t.Fatal("stored HTTP conditions differ")
	}
	if httpView.Map.Steps[1].ValueRole != "local_binding" {
		t.Fatal("stored HTTP view lost local binding syntax fact")
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
					Sequence semantic.FlowSequence        `json:"flowSequence"`
					Map      semantic.SemanticMapIR       `json:"semanticMap"`
					Focus    *semantic.RequestedFlowFocus `json:"requestedFocus"`
				}
				if err := json.Unmarshal(result, &actual); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(actual.Map.Edges, m.Edges) {
					t.Fatal("stored MCP conditions differ")
				}
				if actual.Map.Steps[1].ValueRole != "local_binding" {
					t.Fatal("full MCP view lost local binding syntax fact")
				}
				if !reflect.DeepEqual(actual.Sequence, httpView.Sequence) {
					t.Fatal("HTTP and MCP hierarchy differ")
				}
				if !reflect.DeepEqual(actual.Focus, httpView.Focus) {
					t.Fatal("full MCP focus differs from HTTP")
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
					if step.StepID != m.Steps[i].StepID || step.Kind != m.Steps[i].Kind || step.ValueRole != m.Steps[i].ValueRole || step.InvocationID != m.Steps[i].InvocationID || !reflect.DeepEqual(step.Anchor, m.Steps[i].Anchor) {
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
				if !reflect.DeepEqual(actual.FlowResolution, httpView.Resolution) {
					t.Fatal("compact lost the request and selected source evidence")
				}
				if !reflect.DeepEqual(actual.RequestedFocus, httpView.Focus) {
					t.Fatal("compact lost the verified requested focus")
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

func TestFlowPayloadLegacyViewMatchesHTTPReadSemantics(t *testing.T) {
	root := t.TempDir()
	ctx := context.Background()
	fv, err := flowview.NewServer(flowview.Config{RepoRoot: root, Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer fv.Shutdown(ctx)
	model := &semantic.SemanticMapIR{
		MapID: "map-legacy", GenerationID: "generation", ComputedBasisID: "basis",
		ValidatedAgainstSnapshotID: "snapshot",
		Steps:                      []semantic.SemanticStep{{StepID: "first", Ordinal: 1, Name: "Legacy step", Kind: "call"}},
	}
	sequence := semantic.BuildFlowSequence(model)
	encoded, err := json.Marshal(sequence)
	if err != nil {
		t.Fatal(err)
	}
	var legacySequence map[string]any
	if err := json.Unmarshal(encoded, &legacySequence); err != nil {
		t.Fatal(err)
	}
	legacySequence["architecture"] = "legacy"
	legacySequence["frames"].([]any)[0].(map[string]any)["architecture"] = "legacy"
	legacy := map[string]any{
		"architecture": "legacy", "flowSequence": legacySequence,
		"semanticMap":     map[string]any{"architecture": "legacy", "steps": model.Steps},
		"codeReadingPath": map[string]any{"schemaId": semantic.CodeReadingPathSchemaID, "schemaVersion": 2, "nodes": []any{}, "relations": []any{}},
	}
	raw, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	store := storage.New(root)
	id, original, err := store.SaveView(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	server, err := mcp.NewServer(mcp.Config{RepoRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	mcpRaw, isError := invokeFlowPayload(t, server, map[string]any{"viewId": id, "format": "full"})
	if isError {
		t.Fatalf("MCP rejected legacy View: %s", mcpRaw)
	}
	var mcpView map[string]any
	if err := json.Unmarshal(mcpRaw, &mcpView); err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/view?viewId="+id+"&token="+fv.AuthToken(), nil)
	fv.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("HTTP %d: %s", recorder.Code, recorder.Body.String())
	}
	var httpView map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &httpView); err != nil {
		t.Fatal(err)
	}
	for _, view := range []map[string]any{httpView, mcpView} {
		if _, exists := view["architecture"]; exists {
			t.Fatal("deprecated architecture exposed at root")
		}
		if _, exists := view["semanticMap"].(map[string]any)["architecture"]; exists {
			t.Fatal("deprecated architecture exposed in semantic map")
		}
		seq := view["flowSequence"].(map[string]any)
		if _, exists := seq["architecture"]; exists {
			t.Fatal("deprecated architecture exposed in sequence")
		}
		if _, exists := seq["frames"].([]any)[0].(map[string]any)["architecture"]; exists {
			t.Fatal("deprecated architecture exposed in frame")
		}
		if view["flowResolutionMissing"] != true || view["resolutionLimitation"] == "" {
			t.Fatal("legacy resolution limitation missing")
		}
		if view["sourceContextMissing"] != true || view["needsReanalysis"] != true {
			t.Fatal("legacy source limitation missing")
		}
		if view["codeReadingPath"] != nil || view["codeReadingPathLimitation"] == "" {
			t.Fatal("unverified historical reading path was presented as current")
		}
	}
	compactRaw, isError := invokeFlowPayload(t, server, map[string]any{"viewId": id, "format": "compact"})
	if isError {
		t.Fatalf("MCP rejected legacy compact View: %s", compactRaw)
	}
	var compact curator.CompactFlowPayload
	if err := json.Unmarshal(compactRaw, &compact); err != nil {
		t.Fatal(err)
	}
	if !compact.FlowResolutionMissing || compact.ResolutionLimitation == "" || compact.FlowResolution != nil || compact.CodeReadingPath != nil || compact.CodeReadingPathLimitation == "" || !compact.SourceContextMissing || !compact.NeedsReanalysis || len(compact.Steps) != 0 {
		t.Fatalf("compact View lost the historical request limitation: %+v", compact)
	}
	stored, err := store.ReadView(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(original, stored) {
		t.Fatal("reading legacy View rewrote stored bytes")
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
