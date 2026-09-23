package semantic_test

import (
	"codeflow/internal/collector/slicing"
	"codeflow/internal/curator/semantic"
	"testing"
)

func TestAssignmentExpressionGateway(t *testing.T) {
	for _, tc := range []struct {
		name    string
		change  func(*semantic.SemanticMapIR)
		grouped bool
	}{
		{"calculation feeds assignment", nil, true},
		{"missing value fact", func(m *semantic.SemanticMapIR) { m.Steps[2].AssignmentSourceOrdinal = nil }, false},
		{"separate statement", func(m *semantic.SemanticMapIR) { m.Steps[1].Anchor.ByteRange = [2]int{1, 5} }, false},
		{"independent mutation", func(m *semantic.SemanticMapIR) { m.Steps[1].Kind = "mutation" }, false},
		{"unresolved calculation", func(m *semantic.SemanticMapIR) { m.Steps[1].Rules = []string{"boundary:unresolved"} }, false},
		{"external effect", func(m *semantic.SemanticMapIR) { effect := "write"; m.Steps[1].SideEffect = &effect }, false},
		{"conditional path", func(m *semantic.SemanticMapIR) {
			m.Edges[0].Conditions = []semantic.SemanticBranchCondition{{StepID: "calculate", Outcome: "truthy"}}
		}, false},
		{"other invocation", func(m *semantic.SemanticMapIR) { m.Steps[1].InvocationID = "other" }, false},
		{"missing source identity", func(m *semantic.SemanticMapIR) { m.Steps[1].Anchor.FileHash = ""; m.Steps[2].Anchor.FileHash = "" }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := slicing.Anchor{RepoRelativePath: "order.ts", EnclosingSymbolPath: "update", FileHash: "source-hash", ByteRange: [2]int{20, 35}}
			assignment := a
			assignment.ByteRange = [2]int{10, 35}
			m := &semantic.SemanticMapIR{MapID: "map-order", Steps: []semantic.SemanticStep{
				{StepID: "entry", Kind: "user_action"},
				{StepID: "calculate", Kind: "call", InvocationID: "update", Anchor: a},
				{StepID: "assign", Kind: "mutation", Name: "total = calculate(items)", InvocationID: "update", Anchor: assignment},
				{StepID: "return", Kind: "return", InvocationID: "update", Anchor: slicing.Anchor{RepoRelativePath: "order.ts", EnclosingSymbolPath: "update", FileHash: "source-hash", ByteRange: [2]int{37, 55}}},
			}, Edges: []semantic.SemanticEdge{
				{FromStepID: "calculate", ToStepID: "assign", Kind: "control_flow", ResolutionStatus: "resolved"},
				{FromStepID: "assign", ToStepID: "return", Kind: "control_flow", ResolutionStatus: "resolved"},
			}}
			sourceOrdinal := 2
			m.Steps[2].AssignmentSourceOrdinal = &sourceOrdinal
			for i := range m.Steps {
				m.Steps[i].Ordinal = i + 1
			}
			if tc.change != nil {
				tc.change(m)
			}
			seq := semantic.BuildFlowSequence(m)
			want := 4
			if tc.grouped {
				want = 3
			}
			if len(seq.Frames) != want {
				t.Fatalf("frames=%+v, want %d", seq.Frames, want)
			}
			if tc.grouped {
				if err := semantic.ValidateFlowSequenceReferences(m, seq); err != nil {
					t.Fatal(err)
				}
				f := seq.Frames[1]
				if f.PrimaryStepRef != "assign" || f.Role != "process" || len(f.StepRefs) != 2 || f.Title != "total = calculate(items)" {
					t.Fatalf("assignment purpose hidden: %+v", f)
				}
			}
		})
	}
}

func TestAssignmentGatewayIncludesOnlyProvenSingleCalleeReturn(t *testing.T) {
	for _, tc := range []struct {
		name       string
		change     func(*semantic.SemanticMapIR)
		grouped    bool
		wantFrames int
	}{
		{name: "single return", grouped: true, wantFrames: 3},
		{name: "callee has its own processing", change: func(model *semantic.SemanticMapIR) {
			callerOrdinal := 2
			model.Steps[2].Ordinal = 4
			model.Steps[3].Ordinal = 5
			model.Steps[4].Ordinal = 6
			calleeStep := model.Steps[2]
			calleeStep.StepID = "callee-mutation"
			calleeStep.Kind = "mutation"
			calleeStep.Ordinal = 3
			calleeStep.CallerStepOrdinal = &callerOrdinal
			calleeStep.Anchor.ByteRange = [2]int{0, 10}
			model.Steps = append(model.Steps[:2], append([]semantic.SemanticStep{calleeStep}, model.Steps[2:]...)...)
			model.Edges[0].ToStepID = "callee-mutation"
			model.Edges = append(model.Edges, semantic.SemanticEdge{FromStepID: "callee-mutation", ToStepID: "callee-return", Kind: "control_flow", ResolutionStatus: "resolved", ToSymbolPath: "calculation.ts#calculate"})
		}, wantFrames: 6},
	} {
		t.Run(tc.name, func(t *testing.T) {
			callerOrdinal := 2
			sourceOrdinal := 2
			callerAnchor := slicing.Anchor{RepoRelativePath: "order.ts", EnclosingSymbolPath: "update", FileHash: "source-hash", ByteRange: [2]int{20, 35}}
			assignmentAnchor := callerAnchor
			assignmentAnchor.ByteRange = [2]int{10, 35}
			calleeAnchor := slicing.Anchor{RepoRelativePath: "calculation.ts", EnclosingSymbolPath: "calculate", FileHash: "callee-hash", ByteRange: [2]int{0, 22}}
			model := &semantic.SemanticMapIR{MapID: "map-order", Steps: []semantic.SemanticStep{
				{StepID: "entry", Ordinal: 1, Kind: "user_action", InvocationID: "update", Anchor: callerAnchor},
				{StepID: "calculate", Ordinal: 2, Kind: "call", InvocationID: "update", Anchor: callerAnchor},
				{StepID: "callee-return", Ordinal: 3, Kind: "return", InvocationID: "calculate-invocation", CallerStepOrdinal: &callerOrdinal, Anchor: calleeAnchor},
				{StepID: "assign", Ordinal: 4, Kind: "mutation", InvocationID: "update", AssignmentSourceOrdinal: &sourceOrdinal, Name: "total = calculate(items)", Anchor: assignmentAnchor},
				{StepID: "complete", Ordinal: 5, Kind: "return", InvocationID: "update", Anchor: slicing.Anchor{RepoRelativePath: "order.ts", EnclosingSymbolPath: "update", FileHash: "source-hash", ByteRange: [2]int{37, 55}}},
			}, Edges: []semantic.SemanticEdge{
				{FromStepID: "calculate", ToStepID: "callee-return", Kind: "resolved_cross_file", ResolutionStatus: "resolved", ToSymbolPath: "calculation.ts#calculate"},
				{FromStepID: "calculate", ToStepID: "assign", Kind: "control_flow", ResolutionStatus: "resolved", ToSymbolPath: "order.ts#update"},
				{FromStepID: "callee-return", ToStepID: "assign", Kind: "return", ResolutionStatus: "resolved", ToSymbolPath: "order.ts#update"},
				{FromStepID: "assign", ToStepID: "complete", Kind: "control_flow", ResolutionStatus: "resolved", ToSymbolPath: "order.ts#update"},
			}}
			if tc.change != nil {
				tc.change(model)
			}
			sequence := semantic.BuildFlowSequence(model)
			if len(sequence.Frames) != tc.wantFrames {
				t.Fatalf("frames=%d, want %d: %+v", len(sequence.Frames), tc.wantFrames, sequence.Frames)
			}
			if tc.grouped {
				frame := sequence.Frames[1]
				if len(frame.StepRefs) != 3 || frame.PrimaryStepRef != "assign" {
					t.Fatalf("assignment frame=%+v", frame)
				}
				if frame.StepRefs[0] != "calculate" || frame.StepRefs[1] != "callee-return" || frame.StepRefs[2] != "assign" || frame.CollapsedDetail == nil || frame.CollapsedDetail.Reason != "호출의 단일 반환값을 받아 대입하는 처리" {
					t.Fatalf("single return value transfer was not preserved: %+v", frame)
				}
			} else {
				for _, frame := range sequence.Frames {
					if len(frame.StepRefs) > 1 && frame.StepRefs[0] == "calculate" {
						t.Fatalf("unproven callee return was hidden in %+v", frame)
					}
				}
			}
			if err := semantic.ValidateFlowSequenceReferences(model, sequence); err != nil {
				t.Fatal(err)
			}
		})
	}
}
