package semantic

import (
	"reflect"
	"testing"

	"codeflow/internal/collector/slicing"
)

func TestFlowSequenceGroupsProvenAlternativePathsBeforeTheirJoin(t *testing.T) {
	anchor := slicing.Anchor{RepoRelativePath: "app/controller.dart", EnclosingSymbolPath: "Controller.applyMove"}
	firstCondition, secondCondition := "exploded", "infected"
	mapIR := &SemanticMapIR{
		MapID: "map-move-alternatives",
		Steps: []SemanticStep{
			{StepID: "entry", Ordinal: 1, Kind: "user_action", InvocationID: "move", Anchor: anchor},
			{StepID: "first", Ordinal: 2, Kind: "guard", InvocationID: "move", Branch: &firstCondition, Anchor: anchor},
			{StepID: "explosion", Ordinal: 3, Kind: "mutation", InvocationID: "move", Anchor: anchor},
			{StepID: "second", Ordinal: 4, Kind: "guard", InvocationID: "move", Branch: &secondCondition, Anchor: anchor},
			{StepID: "infection", Ordinal: 5, Kind: "mutation", InvocationID: "move", Anchor: anchor},
			{StepID: "ordinary", Ordinal: 6, Kind: "mutation", InvocationID: "move", Anchor: anchor},
			{StepID: "join", Ordinal: 7, Kind: "result", InvocationID: "move", Anchor: anchor},
		},
		Edges: []SemanticEdge{
			{FromStepID: "entry", ToStepID: "first", Kind: "control_flow", ResolutionStatus: "resolved"},
			{FromStepID: "first", ToStepID: "explosion", Kind: "control_flow", ResolutionStatus: "resolved", Conditions: []SemanticBranchCondition{{StepID: "first", Outcome: "truthy"}}},
			{FromStepID: "first", ToStepID: "second", Kind: "control_flow", ResolutionStatus: "resolved", Conditions: []SemanticBranchCondition{{StepID: "first", Outcome: "falsy"}}},
			{FromStepID: "explosion", ToStepID: "join", Kind: "control_flow", ResolutionStatus: "resolved"},
			{FromStepID: "second", ToStepID: "infection", Kind: "control_flow", ResolutionStatus: "resolved", Conditions: []SemanticBranchCondition{{StepID: "second", Outcome: "truthy"}}},
			{FromStepID: "second", ToStepID: "ordinary", Kind: "control_flow", ResolutionStatus: "resolved", Conditions: []SemanticBranchCondition{{StepID: "second", Outcome: "falsy"}}},
			{FromStepID: "infection", ToStepID: "join", Kind: "control_flow", ResolutionStatus: "resolved"},
			{FromStepID: "ordinary", ToStepID: "join", Kind: "control_flow", ResolutionStatus: "resolved"},
		},
	}
	sequence := BuildFlowSequence(mapIR)
	if len(sequence.Frames) != 3 || sequence.Frames[1].Role != "decision" || sequence.Frames[1].Title != "조건별 처리 경로" || sequence.Frames[1].PrimaryStepRef != "first" || !reflect.DeepEqual(sequence.Frames[1].StepRefs, []string{"first", "explosion", "second", "infection", "ordinary"}) {
		t.Fatalf("proven alternatives did not form one navigable decision: %+v", sequence.Frames)
	}
	if err := ValidateFlowSequenceReferences(mapIR, sequence); err != nil {
		t.Fatal(err)
	}

	mapIR.Steps[3].Rules = []string{"essential"}
	sequence = BuildFlowSequence(mapIR)
	if len(sequence.Frames) <= 3 {
		t.Fatalf("independently essential decision was hidden: %+v", sequence.Frames)
	}
	mapIR.Steps[3].Rules = nil
	effect := "external payment"
	mapIR.Steps[4].SideEffect = &effect
	sequence = BuildFlowSequence(mapIR)
	if len(sequence.Frames) <= 3 {
		t.Fatalf("independent external effect was hidden: %+v", sequence.Frames)
	}
}

func TestFlowSequenceGroupsSingleConditionalOnlyWhenBothPathsJoin(t *testing.T) {
	anchor := slicing.Anchor{RepoRelativePath: "app/controller.dart", EnclosingSymbolPath: "Controller.selectCell"}
	condition := "selected"
	mapIR := &SemanticMapIR{
		MapID: "map-selection",
		Steps: []SemanticStep{
			{StepID: "entry", Ordinal: 1, Kind: "user_action", InvocationID: "selection", Anchor: anchor},
			{StepID: "guard", Ordinal: 2, Kind: "guard", Branch: &condition, InvocationID: "selection", Anchor: anchor},
			{StepID: "deselect", Ordinal: 3, Kind: "mutation", InvocationID: "selection", Anchor: anchor},
			{StepID: "select", Ordinal: 4, Kind: "mutation", InvocationID: "selection", Anchor: anchor},
			{StepID: "join", Ordinal: 5, Kind: "result", InvocationID: "selection", Anchor: anchor},
		},
		Edges: []SemanticEdge{
			{FromStepID: "entry", ToStepID: "guard", Kind: "control_flow", ResolutionStatus: "resolved"},
			{FromStepID: "guard", ToStepID: "deselect", Kind: "control_flow", ResolutionStatus: "resolved", Conditions: []SemanticBranchCondition{{StepID: "guard", Outcome: "truthy"}}},
			{FromStepID: "guard", ToStepID: "select", Kind: "control_flow", ResolutionStatus: "resolved", Conditions: []SemanticBranchCondition{{StepID: "guard", Outcome: "falsy"}}},
			{FromStepID: "deselect", ToStepID: "join", Kind: "control_flow", ResolutionStatus: "resolved"},
			{FromStepID: "select", ToStepID: "join", Kind: "control_flow", ResolutionStatus: "resolved"},
		},
	}
	sequence := BuildFlowSequence(mapIR)
	if len(sequence.Frames) != 3 || sequence.Frames[1].Title != "selected에 따른 처리" || !reflect.DeepEqual(sequence.Frames[1].StepRefs, []string{"guard", "deselect", "select"}) {
		t.Fatalf("single conditional alternatives did not stay together: %+v", sequence.Frames)
	}
	mapIR.Steps[2].Kind = "return"
	mapIR.Edges = append(mapIR.Edges[:3], mapIR.Edges[4])
	sequence = BuildFlowSequence(mapIR)
	if len(sequence.Frames) <= 3 {
		t.Fatalf("early exit with no common join was hidden: %+v", sequence.Frames)
	}
}

func TestFlowSequenceKeepsNestedDecisionOutsideCallerAlternative(t *testing.T) {
	caller := slicing.Anchor{RepoRelativePath: "app/controller.dart", EnclosingSymbolPath: "Controller.selectCell"}
	callee := slicing.Anchor{RepoRelativePath: "app/rules.dart", EnclosingSymbolPath: "Rules.validMoves"}
	selected, valid := "selected", "valid"
	mapIR := &SemanticMapIR{
		MapID: "map-nested-alternative",
		Steps: []SemanticStep{
			{StepID: "entry", Ordinal: 1, Kind: "user_action", InvocationID: "selection", Anchor: caller},
			{StepID: "caller-guard", Ordinal: 2, Kind: "guard", Branch: &selected, InvocationID: "selection", Anchor: caller},
			{StepID: "skip", Ordinal: 3, Kind: "mutation", InvocationID: "selection", Anchor: caller},
			{StepID: "call", Ordinal: 4, Kind: "call", InvocationID: "selection", Anchor: caller},
			{StepID: "callee-guard", Ordinal: 5, Kind: "guard", Branch: &valid, InvocationID: "rules", Anchor: callee},
			{StepID: "valid-return", Ordinal: 6, Kind: "return", InvocationID: "rules", Anchor: callee},
			{StepID: "invalid-return", Ordinal: 7, Kind: "return", InvocationID: "rules", Anchor: callee},
			{StepID: "join", Ordinal: 8, Kind: "mutation", InvocationID: "selection", Anchor: caller},
		},
		Edges: []SemanticEdge{
			{FromStepID: "entry", ToStepID: "caller-guard", Kind: "control_flow", ResolutionStatus: "resolved"},
			{FromStepID: "caller-guard", ToStepID: "skip", Kind: "control_flow", ResolutionStatus: "resolved", Conditions: []SemanticBranchCondition{{StepID: "caller-guard", Outcome: "truthy"}}},
			{FromStepID: "caller-guard", ToStepID: "call", Kind: "control_flow", ResolutionStatus: "resolved", Conditions: []SemanticBranchCondition{{StepID: "caller-guard", Outcome: "falsy"}}},
			{FromStepID: "skip", ToStepID: "join", Kind: "control_flow", ResolutionStatus: "resolved"},
			{FromStepID: "call", ToStepID: "callee-guard", Kind: "resolved_cross_file", ResolutionStatus: "resolved"},
			{FromStepID: "callee-guard", ToStepID: "valid-return", Kind: "control_flow", ResolutionStatus: "resolved", Conditions: []SemanticBranchCondition{{StepID: "callee-guard", Outcome: "truthy"}}},
			{FromStepID: "callee-guard", ToStepID: "invalid-return", Kind: "control_flow", ResolutionStatus: "resolved", Conditions: []SemanticBranchCondition{{StepID: "callee-guard", Outcome: "falsy"}}},
			{FromStepID: "valid-return", ToStepID: "join", Kind: "return", ResolutionStatus: "resolved"},
			{FromStepID: "invalid-return", ToStepID: "join", Kind: "return", ResolutionStatus: "resolved"},
		},
	}
	sequence := BuildFlowSequence(mapIR)
	for _, frame := range sequence.Frames {
		if frame.PrimaryStepRef != "caller-guard" {
			continue
		}
		for _, stepID := range frame.StepRefs {
			if stepID == "callee-guard" {
				t.Fatalf("nested decision disappeared into caller alternative: %+v", frame)
			}
		}
		return
	}
	t.Fatalf("caller decision missing: %+v", sequence.Frames)
}

func TestFlowSequenceDoesNotTreatParallelWaitAsAlternativeJoin(t *testing.T) {
	anchor := slicing.Anchor{RepoRelativePath: "app/controller.dart", EnclosingSymbolPath: "Controller.selectCell"}
	condition := "selected"
	mapIR := &SemanticMapIR{
		MapID: "map-parallel-wait-alternative",
		Steps: []SemanticStep{
			{StepID: "entry", Ordinal: 1, Kind: "user_action", InvocationID: "selection", Anchor: anchor},
			{StepID: "guard", Ordinal: 2, Kind: "guard", Branch: &condition, InvocationID: "selection", Anchor: anchor},
			{StepID: "true-path", Ordinal: 3, Kind: "mutation", InvocationID: "selection", Anchor: anchor},
			{StepID: "false-path", Ordinal: 4, Kind: "call", InvocationID: "selection", Anchor: anchor},
			{StepID: "next", Ordinal: 5, Kind: "await", InvocationID: "selection", Anchor: anchor},
		},
		Edges: []SemanticEdge{
			{FromStepID: "entry", ToStepID: "guard", Kind: "control_flow", ResolutionStatus: "resolved"},
			{FromStepID: "guard", ToStepID: "true-path", Kind: "control_flow", ResolutionStatus: "resolved", Conditions: []SemanticBranchCondition{{StepID: "guard", Outcome: "truthy"}}},
			{FromStepID: "guard", ToStepID: "false-path", Kind: "control_flow", ResolutionStatus: "resolved", Conditions: []SemanticBranchCondition{{StepID: "guard", Outcome: "falsy"}}},
			{FromStepID: "true-path", ToStepID: "next", Kind: "control_flow", ResolutionStatus: "resolved"},
			{FromStepID: "false-path", ToStepID: "next", Kind: "parallel_wait", ResolutionStatus: "resolved"},
		},
	}
	sequence := BuildFlowSequence(mapIR)
	for _, frame := range sequence.Frames {
		if frame.PrimaryStepRef == "guard" && len(frame.StepRefs) > 1 {
			t.Fatalf("an async wait falsely proved both alternatives rejoin: %+v", frame)
		}
	}
}
