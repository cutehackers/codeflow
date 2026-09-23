package semantic_test

import (
	"codeflow/internal/collector/slicing"
	"codeflow/internal/curator/semantic"
	"encoding/json"
	"reflect"
	"testing"
)

func TestReturnExpressionGateway(t *testing.T) {
	for _, tc := range []struct {
		name    string
		change  func(*semantic.SemanticMapIR)
		grouped bool
	}{
		{"calculation and return", nil, true},
		{"entry calculation", func(m *semantic.SemanticMapIR) { m.Steps = m.Steps[1:] }, true},
		{"visible unresolved call", func(m *semantic.SemanticMapIR) { m.Steps[1].Rules = []string{"boundary:unresolved"} }, true},
		{"separate statement", func(m *semantic.SemanticMapIR) { m.Steps[1].Anchor.ByteRange = [2]int{5, 10} }, false},
		{"other invocation", func(m *semantic.SemanticMapIR) { m.Steps[1].InvocationID = "other" }, false},
		{"missing continuation", func(m *semantic.SemanticMapIR) { m.Edges = nil }, false},
		{"state change", func(m *semantic.SemanticMapIR) { m.Steps[1].Kind = "mutation" }, false},
		{"conditional evaluation", func(m *semantic.SemanticMapIR) { condition := "ready"; m.Steps[1].Branch = &condition }, false},
		{"external effect", func(m *semantic.SemanticMapIR) { effect := "write"; m.Steps[1].SideEffect = &effect }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			anchor := slicing.Anchor{RepoRelativePath: "cart.ts", EnclosingSymbolPath: "total", FileHash: "hash", ByteRange: [2]int{20, 30}}
			returned := anchor
			returned.ByteRange = [2]int{13, 31}
			m := &semantic.SemanticMapIR{MapID: "map-cart", GenerationID: "generation", ComputedBasisID: "basis", Steps: []semantic.SemanticStep{
				{StepID: "entry", Kind: "user_action", Anchor: anchor},
				{StepID: "calculate", Ordinal: 2, Kind: "call", InvocationID: "total-call", Anchor: anchor},
				{StepID: "return", Ordinal: 3, Kind: "return", InvocationID: "total-call", Anchor: returned},
			}, Edges: []semantic.SemanticEdge{{FromStepID: "calculate", ToStepID: "return", Kind: "control_flow", ResolutionStatus: "resolved"}}}
			if tc.change != nil {
				tc.change(m)
			}
			got := semantic.BuildFlowSequence(m)
			want := len(m.Steps)
			if tc.grouped {
				want = len(m.Steps) - 1
			}
			if len(got.Frames) != want {
				t.Fatalf("frames=%+v, want %d", got.Frames, want)
			}
			if err := semantic.ValidateFlowSequenceReferences(m, got); err != nil {
				t.Fatal(err)
			}
			if tc.grouped {
				frame := got.Frames[len(got.Frames)-1]
				if len(frame.StepRefs) != 2 || frame.Title != "total 반환" {
					t.Fatalf("return purpose missing: %+v", frame)
				}
				if tc.name == "visible unresolved call" && (frame.Role != "boundary" || frame.Status != "unknown" || frame.PrimaryStepRef != "calculate") {
					t.Fatalf("boundary hidden: %+v", frame)
				}
				if tc.name == "entry calculation" && (frame.Role != "entry" || frame.PrimaryStepRef != "calculate") {
					t.Fatalf("entry representative lost: %+v", frame)
				}
				if tc.name == "calculation and return" && (frame.Role != "process" || frame.PrimaryStepRef != "return") {
					t.Fatalf("local return misrepresented: %+v", frame)
				}
			}
		})
	}
}

func TestReturnExpressionPreservesIndependentBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name string
		root bool
	}{{"two unresolved calls", false}, {"entry boundary", true}} {
		t.Run(tc.name, func(t *testing.T) {
			a := slicing.Anchor{RepoRelativePath: "cart.ts", EnclosingSymbolPath: "total", ByteRange: [2]int{10, 15}}
			b := a
			b.ByteRange = [2]int{8, 20}
			r := a
			r.ByteRange = [2]int{0, 21}
			m := &semantic.SemanticMapIR{MapID: "map-cart", Steps: []semantic.SemanticStep{
				{StepID: "entry", Kind: "user_action"},
				{StepID: "inner", Kind: "call", InvocationID: "invocation", Anchor: a, Rules: []string{"boundary:unknown"}},
				{StepID: "outer", Kind: "call", InvocationID: "invocation", Anchor: b, Rules: []string{"boundary:unknown"}},
				{StepID: "return", Kind: "return", InvocationID: "invocation", Anchor: r},
			}, Edges: []semantic.SemanticEdge{
				{FromStepID: "inner", ToStepID: "outer", Kind: "control_flow", ResolutionStatus: "resolved"},
				{FromStepID: "outer", ToStepID: "return", Kind: "control_flow", ResolutionStatus: "resolved"},
			}}
			if tc.root {
				m.Steps = m.Steps[1:]
				m.Steps[1].Rules = nil
			}
			got := semantic.BuildFlowSequence(m)
			if len(got.Frames) != len(m.Steps) {
				t.Fatalf("independent boundary or entry hidden: %+v", got.Frames)
			}
			if err := semantic.ValidateFlowSequenceReferences(m, got); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestReturnExpressionKeepsInvocationMembership(t *testing.T) {
	anchor := slicing.Anchor{RepoRelativePath: "cart.ts", EnclosingSymbolPath: "total", ByteRange: [2]int{10, 20}}
	returned := anchor
	returned.ByteRange = [2]int{3, 21}
	nested := slicing.Anchor{RepoRelativePath: "price.ts", EnclosingSymbolPath: "price", ByteRange: [2]int{0, 8}}
	m := &semantic.SemanticMapIR{MapID: "map-cart", Steps: []semantic.SemanticStep{
		{StepID: "entry", Kind: "user_action"},
		{StepID: "call-a", Kind: "call", InvocationID: "a", Anchor: anchor},
		{StepID: "nested-a", Kind: "return", InvocationID: "nested-a", Anchor: nested},
		{StepID: "return-a", Kind: "return", InvocationID: "a", Anchor: returned},
		{StepID: "call-b", Kind: "call", InvocationID: "b", Anchor: anchor},
		{StepID: "nested-b", Kind: "return", InvocationID: "nested-b", Anchor: nested},
		{StepID: "return-b", Kind: "return", InvocationID: "b", Anchor: returned},
	}, Edges: []semantic.SemanticEdge{
		{FromStepID: "call-a", ToStepID: "nested-a", Kind: "call", ResolutionStatus: "resolved"},
		{FromStepID: "call-a", ToStepID: "return-a", Kind: "control_flow", ResolutionStatus: "resolved"},
		{FromStepID: "call-b", ToStepID: "nested-b", Kind: "call", ResolutionStatus: "resolved"},
		{FromStepID: "call-b", ToStepID: "return-b", Kind: "control_flow", ResolutionStatus: "resolved"},
	}}
	before, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	got := semantic.BuildFlowSequence(m)
	if len(got.Frames) != 5 || !reflect.DeepEqual(got.Frames[1].StepRefs, []string{"call-a", "return-a"}) || !reflect.DeepEqual(got.Frames[3].StepRefs, []string{"call-b", "return-b"}) {
		t.Fatalf("invocations mixed: %+v", got.Frames)
	}
	if err := semantic.ValidateFlowSequenceReferences(m, got); err != nil {
		t.Fatal(err)
	}
	after, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("grouping changed source facts")
	}
	if !reflect.DeepEqual(got, semantic.BuildFlowSequence(m)) {
		t.Fatal("grouping is not deterministic")
	}
}
