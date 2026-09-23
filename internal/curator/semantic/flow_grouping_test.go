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
}
