package semantic_test

import (
	"reflect"
	"testing"

	"codeflow/internal/collector/slicing"
	"codeflow/internal/curator/semantic"
)

func TestFlowSequenceFoldsContinuousStepsInOneInvocation(t *testing.T) {
	anchor := slicing.Anchor{RepoRelativePath: "app/checkout.ts", EnclosingSymbolPath: "checkout", FileHash: "source", ByteRange: [2]int{0, 80}}
	mapIR := &semantic.SemanticMapIR{
		MapID: "map-checkout",
		Steps: []semantic.SemanticStep{
			{StepID: "entry", Ordinal: 1, Name: "startCheckout()", Kind: "user_action", InvocationID: "checkout", Anchor: anchor},
			{StepID: "load", Ordinal: 2, Name: "loadCart()", Kind: "call", InvocationID: "checkout", Anchor: anchor},
			{StepID: "format", Ordinal: 3, Name: "formatCart()", Kind: "call", InvocationID: "checkout", Anchor: anchor},
			{StepID: "persist", Ordinal: 4, Name: "saveOrder()", Kind: "mutation", InvocationID: "checkout", Anchor: anchor},
		},
		Edges: []semantic.SemanticEdge{
			{FromStepID: "entry", ToStepID: "load", Kind: "control_flow", ResolutionStatus: "resolved"},
			{FromStepID: "load", ToStepID: "format", Kind: "control_flow", ResolutionStatus: "resolved"},
			{FromStepID: "format", ToStepID: "persist", Kind: "control_flow", ResolutionStatus: "resolved"},
		},
	}

	sequence := semantic.BuildFlowSequence(mapIR)
	if len(sequence.Frames) != 2 {
		t.Fatalf("frames=%+v, want entry timeline and state-change gateway", sequence.Frames)
	}
	if frame := sequence.Frames[0]; frame.Role != "entry" || !reflect.DeepEqual(frame.StepRefs, []string{"entry", "load", "format"}) || frame.CollapsedDetail == nil || frame.CollapsedDetail.Count != 2 {
		t.Fatalf("continuous invocation steps were not preserved as one timeline: %+v", frame)
	}
	if err := semantic.ValidateFlowSequenceReferences(mapIR, sequence); err != nil {
		t.Fatal(err)
	}
}

func TestCallbackArgumentDoesNotSplitProvenOuterSequence(t *testing.T) {
	anchor := slicing.Anchor{RepoRelativePath: "lib/rules.dart", EnclosingSymbolPath: "Rules.run", FileHash: "source", ByteRange: [2]int{0, 100}}
	model := &semantic.SemanticMapIR{
		MapID: "map-callback-grouping",
		Steps: []semantic.SemanticStep{
			{StepID: "entry", Ordinal: 1, Name: "run", Kind: "user_action", InvocationID: "outer", Anchor: anchor},
			{StepID: "registration", Ordinal: 2, Name: "register", Kind: "call", InvocationID: "outer", Anchor: anchor},
			{StepID: "continuation", Ordinal: 3, Name: "continue", Kind: "call", InvocationID: "outer", Anchor: anchor},
			{StepID: "callback", Ordinal: 4, Name: "callback body", Kind: "return", InvocationID: "callback", Anchor: anchor},
		},
		Edges: []semantic.SemanticEdge{
			{FromStepID: "entry", ToStepID: "registration", Kind: "control_flow", ResolutionStatus: "resolved"},
			{FromStepID: "registration", ToStepID: "continuation", Kind: "control_flow", ResolutionStatus: "resolved"},
			{FromStepID: "registration", ToStepID: "callback", Kind: "callback_argument", ResolutionStatus: "resolved"},
		},
	}
	sequence := semantic.BuildFlowSequence(model)
	if len(sequence.Frames) != 2 || !reflect.DeepEqual(sequence.Frames[0].StepRefs, []string{"entry", "registration", "continuation"}) || !reflect.DeepEqual(sequence.Frames[1].StepRefs, []string{"callback"}) {
		t.Fatalf("callback delivery split the proven outer sequence: %+v", sequence.Frames)
	}
	if len(model.Edges) != 3 || model.Edges[2].Kind != "callback_argument" {
		t.Fatal("callback source relation disappeared from the semantic map")
	}
}

func TestFlowSequenceDoesNotFoldSeparateInvocations(t *testing.T) {
	anchor := slicing.Anchor{RepoRelativePath: "app/checkout.ts", EnclosingSymbolPath: "checkout", FileHash: "source", ByteRange: [2]int{0, 80}}
	mapIR := &semantic.SemanticMapIR{
		MapID: "map-checkout",
		Steps: []semantic.SemanticStep{
			{StepID: "entry", Ordinal: 1, Name: "startCheckout()", Kind: "user_action", InvocationID: "checkout", Anchor: anchor},
			{StepID: "child", Ordinal: 2, Name: "loadCart()", Kind: "call", InvocationID: "load-cart", Anchor: anchor},
		},
		Edges: []semantic.SemanticEdge{{FromStepID: "entry", ToStepID: "child", Kind: "control_flow", ResolutionStatus: "resolved"}},
	}

	sequence := semantic.BuildFlowSequence(mapIR)
	if len(sequence.Frames) != 2 || len(sequence.Frames[0].StepRefs) != 1 || len(sequence.Frames[1].StepRefs) != 1 {
		t.Fatalf("separate invocations were merged: %+v", sequence.Frames)
	}
}

func TestFlowSequenceDoesNotFoldResolvedCalleeEntry(t *testing.T) {
	anchor := slicing.Anchor{RepoRelativePath: "app/checkout.ts", EnclosingSymbolPath: "checkout", FileHash: "source", ByteRange: [2]int{0, 80}}
	calleeAnchor := slicing.Anchor{RepoRelativePath: "services/orders.ts", EnclosingSymbolPath: "submitOrder", FileHash: "source", ByteRange: [2]int{0, 40}}
	mapIR := &semantic.SemanticMapIR{
		MapID: "map-checkout",
		Steps: []semantic.SemanticStep{
			{StepID: "entry", Ordinal: 1, Name: "buildPayload()", Kind: "user_action", InvocationID: "checkout", Anchor: anchor},
			{StepID: "submit", Ordinal: 2, Name: "submitOrder(payload)", Kind: "call", InvocationID: "checkout", Anchor: anchor},
			{StepID: "result", Ordinal: 3, Name: "return receipt;", Kind: "return", InvocationID: "submit-order", Anchor: calleeAnchor},
		},
		Edges: []semantic.SemanticEdge{
			{FromStepID: "entry", ToStepID: "submit", Kind: "control_flow", ResolutionStatus: "resolved"},
			{FromStepID: "submit", ToStepID: "result", Kind: "resolved_cross_file", ResolutionStatus: "resolved"},
		},
	}

	sequence := semantic.BuildFlowSequence(mapIR)
	if len(sequence.Frames) != 3 || !reflect.DeepEqual(sequence.Frames[0].StepRefs, []string{"entry"}) || !reflect.DeepEqual(sequence.Frames[1].StepRefs, []string{"submit"}) {
		t.Fatalf("resolved callee entry was hidden in the previous gateway: %+v", sequence.Frames)
	}
	if len(sequence.SummaryLimitations) != 0 {
		t.Fatalf("resolved callee transition was reported as missing grouping evidence: %+v", sequence.SummaryLimitations)
	}
}

func TestFlowSequenceReportsUngroupedStateChanges(t *testing.T) {
	anchor := slicing.Anchor{RepoRelativePath: "app/controller.dart", EnclosingSymbolPath: "Controller.applyMove", FileHash: "source", ByteRange: [2]int{0, 80}}
	mapIR := &semantic.SemanticMapIR{
		MapID: "map-move",
		Steps: []semantic.SemanticStep{
			{StepID: "entry", Ordinal: 1, Name: "applyMove", Kind: "user_action", InvocationID: "move", Anchor: anchor},
			{StepID: "selection", Ordinal: 2, Name: "selection = null", Kind: "mutation", InvocationID: "move", Anchor: anchor},
			{StepID: "state", Ordinal: 3, Name: "state = next", Kind: "mutation", InvocationID: "move", Anchor: anchor},
		},
		Edges: []semantic.SemanticEdge{
			{FromStepID: "entry", ToStepID: "selection", Kind: "control_flow", ResolutionStatus: "resolved"},
			{FromStepID: "selection", ToStepID: "state", Kind: "control_flow", ResolutionStatus: "resolved"},
		},
	}
	sequence := semantic.BuildFlowSequence(mapIR)
	if len(sequence.Frames) != 3 || len(sequence.SummaryLimitations) != 1 || !reflect.DeepEqual(sequence.SummaryLimitations[0].FrameRefs, []string{"frame-02", "frame-03"}) {
		t.Fatalf("flat state changes lack an honest summary limitation: frames=%+v limitations=%+v", sequence.Frames, sequence.SummaryLimitations)
	}
	mapIR.Steps[1].Rules = []string{"essential"}
	mapIR.Steps[2].Rules = []string{"essential"}
	sequence = semantic.BuildFlowSequence(mapIR)
	if len(sequence.SummaryLimitations) != 0 {
		t.Fatalf("independently essential state changes were labeled as ungrouped: %+v", sequence.SummaryLimitations)
	}
}

func TestFlowSequenceGroupsProvenLocalPreparationWithoutHidingStateChange(t *testing.T) {
	anchor := slicing.Anchor{RepoRelativePath: "lib/rules.dart", EnclosingSymbolPath: "Rules.applyMove", FileHash: "source", ByteRange: [2]int{0, 80}}
	model := &semantic.SemanticMapIR{
		MapID: "map-local-preparation",
		Steps: []semantic.SemanticStep{
			{StepID: "call", Ordinal: 1, Name: "applyMove", Kind: "call", InvocationID: "caller", Anchor: anchor},
			{StepID: "player", Ordinal: 2, Name: "final player = state.currentTurn;", Kind: "mutation", ValueRole: "local_binding", InvocationID: "callee", Anchor: anchor},
			{StepID: "opponent", Ordinal: 3, Name: "final opponent = player.opponent;", Kind: "mutation", ValueRole: "local_binding", InvocationID: "callee", Anchor: anchor},
			{StepID: "state", Ordinal: 4, Name: "state = next;", Kind: "mutation", InvocationID: "callee", Anchor: anchor},
		},
		Edges: []semantic.SemanticEdge{
			{FromStepID: "call", ToStepID: "player", Kind: "resolved_cross_file", ResolutionStatus: "resolved"},
			{FromStepID: "player", ToStepID: "opponent", Kind: "control_flow", ResolutionStatus: "resolved"},
			{FromStepID: "opponent", ToStepID: "state", Kind: "control_flow", ResolutionStatus: "resolved"},
		},
	}
	sequence := semantic.BuildFlowSequence(model)
	if len(sequence.Frames) != 3 || !reflect.DeepEqual(sequence.Frames[1].StepRefs, []string{"player", "opponent"}) || !reflect.DeepEqual(sequence.Frames[2].StepRefs, []string{"state"}) {
		t.Fatalf("local preparation and state write were not separated: %+v", sequence.Frames)
	}
	if len(sequence.SummaryLimitations) != 0 {
		t.Fatalf("proven local preparation reported a missing grouping: %+v", sequence.SummaryLimitations)
	}
	if err := semantic.ValidateFlowSequenceReferences(model, sequence); err != nil {
		t.Fatal(err)
	}
	model.Steps[0].Kind = "guard"
	model.Steps[0].InvocationID = "callee"
	model.Edges[0] = semantic.SemanticEdge{
		FromStepID: "call", ToStepID: "player", Kind: "control_flow", ResolutionStatus: "resolved",
		Conditions: []semantic.SemanticBranchCondition{{StepID: "call", Outcome: "falsy"}},
	}
	sequence = semantic.BuildFlowSequence(model)
	if len(sequence.SummaryLimitations) != 0 {
		t.Fatalf("a proven decision-to-process transition was called a grouping failure: %+v", sequence.SummaryLimitations)
	}
	model.Steps[0].Kind = "call"
	model.Steps[0].InvocationID = "caller"
	model.Edges[0] = semantic.SemanticEdge{FromStepID: "call", ToStepID: "player", Kind: "resolved_cross_file", ResolutionStatus: "resolved"}
	model.Edges = model.Edges[:2]
	sequence = semantic.BuildFlowSequence(model)
	if len(sequence.Frames) != 3 || !reflect.DeepEqual(sequence.Frames[1].StepRefs, []string{"player", "opponent"}) {
		t.Fatalf("missing state continuation changed proven local preparation: %+v", sequence.Frames)
	}
	if len(sequence.SummaryLimitations) == 0 {
		t.Fatal("missing state continuation lost its grouping limitation")
	}
}

func TestFlowSequenceDoesNotCallProvenLoopTransferAnUngroupedProcess(t *testing.T) {
	anchor := slicing.Anchor{RepoRelativePath: "lib/rules.dart", EnclosingSymbolPath: "Rules.moves", FileHash: "source", ByteRange: [2]int{0, 80}}
	model := &semantic.SemanticMapIR{
		MapID: "map-loop",
		Steps: []semantic.SemanticStep{
			{StepID: "condition", Ordinal: 1, Name: "next move", Kind: "branch", InvocationID: "moves", Anchor: anchor},
			{StepID: "skip", Ordinal: 2, Name: "continue", Kind: "continue", InvocationID: "moves", Anchor: anchor},
		},
		Edges: []semantic.SemanticEdge{
			{FromStepID: "condition", ToStepID: "skip", Kind: "control_flow", ResolutionStatus: "resolved"},
			{FromStepID: "skip", ToStepID: "condition", Kind: "loop_back", ResolutionStatus: "resolved"},
		},
	}
	sequence := semantic.BuildFlowSequence(model)
	if len(sequence.Frames) != 2 || len(sequence.SummaryLimitations) != 0 {
		t.Fatalf("proven continue was called a grouping failure: frames=%+v limitations=%+v", sequence.Frames, sequence.SummaryLimitations)
	}
	model.Edges = model.Edges[:1]
	sequence = semantic.BuildFlowSequence(model)
	if len(sequence.SummaryLimitations) != 1 || !reflect.DeepEqual(sequence.SummaryLimitations[0].FrameRefs, []string{"frame-02"}) {
		t.Fatalf("unproven continue lost its limitation: %+v", sequence.SummaryLimitations)
	}
}
