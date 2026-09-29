package semantic

import (
	"reflect"
	"testing"

	"codeflow/internal/collector/slicing"
)

func TestFlowSequenceGroupsVerifiedLeafCallWithItsSourceSteps(t *testing.T) {
	callerOrdinal := 2
	callerAnchor := slicing.Anchor{RepoRelativePath: "app/controller.dart", EnclosingSymbolPath: "Controller.onTap"}
	calleeAnchor := slicing.Anchor{RepoRelativePath: "app/controller.dart", EnclosingSymbolPath: "Controller.showBanner"}
	mapIR := &SemanticMapIR{
		MapID:           "map-banner",
		BoundaryTargets: []string{"notify"},
		Steps: []SemanticStep{
			{StepID: "entry", Ordinal: 1, Kind: "user_action", Name: "tap", InvocationID: "tap", Anchor: callerAnchor},
			{StepID: "call", Ordinal: 2, Kind: "call", Name: "showBanner()", InvocationID: "tap", Anchor: callerAnchor},
			{StepID: "state", Ordinal: 3, Kind: "mutation", Name: "banner = text", InvocationID: "banner", CallerStepOrdinal: &callerOrdinal, Anchor: calleeAnchor},
			{StepID: "guard", Ordinal: 4, Kind: "guard", Name: "if active", InvocationID: "banner", CallerStepOrdinal: &callerOrdinal, Anchor: calleeAnchor},
			{StepID: "notify", Ordinal: 5, Kind: "call", Name: "notifyListeners()", InvocationID: "banner", CallerStepOrdinal: &callerOrdinal, Anchor: calleeAnchor},
			{StepID: "result", Ordinal: 6, Kind: "result", Name: "done", InvocationID: "tap", Anchor: callerAnchor},
		},
		Edges: []SemanticEdge{
			{FromStepID: "entry", ToStepID: "call", Kind: "control_flow", ResolutionStatus: "resolved"},
			{FromStepID: "call", ToStepID: "state", Kind: "resolved_cross_file", ResolutionStatus: "resolved"},
			{FromStepID: "call", ToStepID: "result", Kind: "control_flow", ResolutionStatus: "resolved"},
			{FromStepID: "state", ToStepID: "guard", Kind: "control_flow", ResolutionStatus: "resolved"},
			{FromStepID: "guard", ToStepID: "notify", Kind: "control_flow", ResolutionStatus: "resolved", Conditions: []SemanticBranchCondition{{StepID: "guard", Outcome: "truthy"}}},
		},
	}
	sequence := BuildFlowSequence(mapIR)
	if len(sequence.Frames) != 3 || !reflect.DeepEqual(sequence.Frames[1].StepRefs, []string{"call", "state", "guard", "notify"}) || sequence.Frames[1].PrimaryStepRef != "call" || sequence.Frames[1].Status != "unknown" {
		t.Fatalf("leaf call was not one navigable, honest gateway: %+v", sequence.Frames)
	}
	if err := ValidateFlowSequenceReferences(mapIR, sequence); err != nil {
		t.Fatal(err)
	}

	mapIR.Steps[3].Rules = []string{"essential"}
	sequence = BuildFlowSequence(mapIR)
	if len(sequence.Frames) != 6 {
		t.Fatalf("independently essential decision was hidden in a call gateway: %+v", sequence.Frames)
	}
}

func TestFlowSequenceGroupsPreviouslyCombinedLeafDetails(t *testing.T) {
	callerOrdinal := 2
	anchor := slicing.Anchor{RepoRelativePath: "app/controller.dart", EnclosingSymbolPath: "Controller.onTap"}
	childAnchor := slicing.Anchor{RepoRelativePath: "app/rules.dart", EnclosingSymbolPath: "Rules.calculate"}
	mapIR := &SemanticMapIR{
		Steps: []SemanticStep{
			{StepID: "entry", Ordinal: 1, Kind: "user_action", InvocationID: "tap", Anchor: anchor},
			{StepID: "call", Ordinal: 2, Kind: "call", InvocationID: "tap", Anchor: anchor},
			{StepID: "first", Ordinal: 3, Kind: "mutation", InvocationID: "calculate", CallerStepOrdinal: &callerOrdinal, Anchor: childAnchor},
			{StepID: "second", Ordinal: 4, Kind: "mutation", InvocationID: "calculate", CallerStepOrdinal: &callerOrdinal, Anchor: childAnchor},
			{StepID: "result", Ordinal: 5, Kind: "result", InvocationID: "tap", Anchor: anchor},
		},
		Edges: []SemanticEdge{
			{FromStepID: "call", ToStepID: "first", Kind: "resolved_cross_file", ResolutionStatus: "resolved"},
			{FromStepID: "first", ToStepID: "second", Kind: "control_flow", ResolutionStatus: "resolved"},
		},
	}
	sequence := &FlowSequence{Frames: []FlowSequenceFrame{
		{FrameID: "frame-01", Role: "entry", StepRefs: []string{"entry"}, PrimaryStepRef: "entry", Status: "verified"},
		{FrameID: "frame-02", Role: "process", StepRefs: []string{"call"}, PrimaryStepRef: "call", Status: "verified"},
		{FrameID: "frame-03", Role: "process", StepRefs: []string{"first", "second"}, PrimaryStepRef: "first", Status: "verified"},
		{FrameID: "frame-04", Role: "result", StepRefs: []string{"result"}, PrimaryStepRef: "result", Status: "verified"},
	}}
	groupVerifiedLeafCalls(sequence, mapIR, groupingConnections(mapIR.Edges))
	if len(sequence.Frames) != 3 || !reflect.DeepEqual(sequence.Frames[1].StepRefs, []string{"call", "first", "second"}) || sequence.Frames[1].CollapsedDetail == nil || sequence.Frames[1].CollapsedDetail.Count != 2 {
		t.Fatalf("previously grouped leaf details did not remain under their direct call: %+v", sequence.Frames)
	}
}
