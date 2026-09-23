package semantic_test

import (
	"codeflow/internal/collector/fusion"
	"codeflow/internal/collector/slicing"
	"codeflow/internal/curator/semantic"
	"encoding/json"
	"reflect"
	"testing"
)

func TestConditionalReturnGateway(t *testing.T) {
	for _, tc := range []struct {
		name    string
		change  func(*semantic.SemanticMapIR)
		grouped bool
	}{
		{"one conditional return", nil, true},
		{"independent call", func(m *semantic.SemanticMapIR) { m.Steps[2].Kind = "call" }, false},
		{"missing outcome", func(m *semantic.SemanticMapIR) { m.Edges = m.Edges[:3] }, false},
		{"wrong outcome source", func(m *semantic.SemanticMapIR) { m.Edges[0].Conditions[0].StepID = "second" }, false},
		{"state change", func(m *semantic.SemanticMapIR) { m.Steps[1].StateDelta = &fusion.StateDelta{Before: "a", After: "b"} }, false},
		{"separate statement", func(m *semantic.SemanticMapIR) { m.Steps[1].Anchor.ByteRange = [2]int{41, 45} }, false},
		{"unresolved boundary", func(m *semantic.SemanticMapIR) { m.Steps[1].Rules = []string{"boundary:missing"} }, false},
		{"different invocation", func(m *semantic.SemanticMapIR) { m.Steps[2].InvocationID = "other" }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			anchor := slicing.Anchor{RepoRelativePath: "order.ts", EnclosingSymbolPath: "isReady", FileHash: "hash", ByteRange: [2]int{10, 15}}
			second := anchor
			second.ByteRange = [2]int{10, 25}
			returned := anchor
			returned.ByteRange = [2]int{0, 40}
			edge := func(from, to, outcome string) semantic.SemanticEdge {
				return semantic.SemanticEdge{FromStepID: from, ToStepID: to, Kind: "control_flow", ResolutionStatus: "resolved", Conditions: []semantic.SemanticBranchCondition{{StepID: from, Outcome: outcome}}}
			}
			m := &semantic.SemanticMapIR{MapID: "map-return", Steps: []semantic.SemanticStep{
				{StepID: "entry", Ordinal: 1, Kind: "user_action"},
				{StepID: "first", Ordinal: 2, Kind: "branch", InvocationID: "ready", Anchor: anchor},
				{StepID: "second", Ordinal: 3, Kind: "branch", InvocationID: "ready", Anchor: second},
				{StepID: "return", Ordinal: 4, Kind: "return", InvocationID: "ready", Anchor: returned},
			}, Edges: []semantic.SemanticEdge{edge("first", "second", "truthy"), edge("first", "return", "falsy"), edge("second", "return", "truthy"), edge("second", "return", "falsy")}}
			if tc.change != nil {
				tc.change(m)
			}
			before, _ := json.Marshal(m)
			sequence := semantic.BuildFlowSequence(m)
			want := 4
			if tc.grouped {
				want = 2
			}
			if len(sequence.Frames) != want {
				t.Fatalf("frames=%+v, want %d", sequence.Frames, want)
			}
			after, _ := json.Marshal(m)
			if !reflect.DeepEqual(before, after) {
				t.Fatal("grouping changed source graph")
			}
			if tc.grouped {
				frame := sequence.Frames[1]
				if frame.Role != "decision" || frame.PrimaryStepRef != "first" || frame.Title != "isReady 반환값 판단" || !reflect.DeepEqual(frame.StepRefs, []string{"first", "second", "return"}) {
					t.Fatalf("conditional meaning lost: %+v", frame)
				}
				if frame.Status == "verified" {
					t.Fatal("unverified steps promoted")
				}
				if err := semantic.ValidateFlowSequenceReferences(m, sequence); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestCompoundGuardGateway(t *testing.T) {
	for _, tc := range []struct {
		name    string
		change  func(*semantic.SemanticMapIR)
		grouped bool
	}{
		{"one guard expression", nil, true},
		{"call inside condition", func(m *semantic.SemanticMapIR) { m.Steps[1].Kind = "call" }, false},
		{"independent condition", func(m *semantic.SemanticMapIR) { m.Steps[1].Anchor.ByteRange = [2]int{1, 5} }, false},
		{"missing outcome", func(m *semantic.SemanticMapIR) { m.Edges = m.Edges[1:] }, false},
		{"unknown condition", func(m *semantic.SemanticMapIR) { m.Steps[1].Rules = []string{"boundary:missing"} }, false},
		{"external entry", func(m *semantic.SemanticMapIR) {
			m.Edges = append(m.Edges, semantic.SemanticEdge{FromStepID: "entry", ToStepID: "guard", Kind: "control_flow", ResolutionStatus: "resolved"})
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := slicing.Anchor{RepoRelativePath: "order.ts", EnclosingSymbolPath: "checkout", FileHash: "hash", ByteRange: [2]int{14, 18}}
			g := a
			g.ByteRange = [2]int{10, 30}
			r := a
			r.ByteRange = [2]int{31, 40}
			n := a
			n.ByteRange = [2]int{41, 50}
			edge := func(from, to, outcome string) semantic.SemanticEdge {
				return semantic.SemanticEdge{FromStepID: from, ToStepID: to, Kind: "control_flow", ResolutionStatus: "resolved", Conditions: []semantic.SemanticBranchCondition{{StepID: from, Outcome: outcome}}}
			}
			m := &semantic.SemanticMapIR{MapID: "guard", Steps: []semantic.SemanticStep{
				{StepID: "entry", Kind: "user_action"},
				{StepID: "operand", Ordinal: 1, Kind: "branch", InvocationID: "checkout", Anchor: a},
				{StepID: "guard", Ordinal: 2, Kind: "guard", InvocationID: "checkout", Anchor: g, Name: "if (a && b)"},
				{StepID: "failure", Ordinal: 3, Kind: "return", InvocationID: "checkout", Anchor: r},
				{StepID: "next", Ordinal: 4, Kind: "guard", InvocationID: "checkout", Anchor: n},
			}, Edges: []semantic.SemanticEdge{edge("operand", "guard", "truthy"), edge("operand", "guard", "falsy"), edge("guard", "failure", "truthy"), edge("guard", "next", "falsy")}}
			if tc.change != nil {
				tc.change(m)
			}
			before, _ := json.Marshal(m)
			seq := semantic.BuildFlowSequence(m)
			want := 5
			if tc.grouped {
				want = 4
			}
			if len(seq.Frames) != want {
				t.Fatalf("frames=%+v want=%d", seq.Frames, want)
			}
			after, _ := json.Marshal(m)
			if !reflect.DeepEqual(before, after) {
				t.Fatal("source graph changed")
			}
			if tc.grouped {
				f := seq.Frames[1]
				if f.Role != "decision" || f.PrimaryStepRef != "guard" || !reflect.DeepEqual(f.StepRefs, []string{"operand", "guard"}) {
					t.Fatalf("wrong guard: %+v", f)
				}
				if err := semantic.ValidateFlowSequenceReferences(m, seq); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
