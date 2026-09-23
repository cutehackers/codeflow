package semantic

import (
	"reflect"
	"testing"

	"codeflow/internal/collector/slicing"
)

func TestBuildFlowSequenceGroupsCoreFlowHandlerSegments(t *testing.T) {
	anchor := func(symbol string) slicing.Anchor {
		return slicing.Anchor{RepoRelativePath: "app/checkout.ts", EnclosingSymbolPath: symbol}
	}
	condition := "response.success"
	mapIR := &SemanticMapIR{
		GenerationID:    "generation-handler",
		ComputedBasisID: "basis-handler",
		BoundaryTargets: nil,
		Steps: []SemanticStep{
			{StepID: "start", Ordinal: 1, Name: "setStatus('processing')", TechnicalName: "Checkout.onSubmit", Kind: "call", InvocationID: "handler", Anchor: anchor("Checkout.onSubmit"), Rules: []string{"boundary:status"}},
			{StepID: "calculate", Ordinal: 2, Name: "calculateTotal()", TechnicalName: "Checkout.onSubmit", Kind: "call", InvocationID: "handler", Anchor: anchor("Checkout.onSubmit")},
			{StepID: "reduce", Ordinal: 3, Name: "cart.reduce()", TechnicalName: "Cart.calculateTotal", Kind: "call", InvocationID: "calculate", Anchor: anchor("Cart.calculateTotal"), Rules: []string{"boundary:reduce"}},
			{StepID: "calculated", Ordinal: 4, Name: "return cart.reduce()", TechnicalName: "Cart.calculateTotal", Kind: "return", InvocationID: "calculate", Anchor: anchor("Cart.calculateTotal")},
			{StepID: "payload", Ordinal: 5, Name: "orderPayload = { total: calculateTotal() }", TechnicalName: "Checkout.onSubmit", Kind: "mutation", InvocationID: "handler", Anchor: anchor("Checkout.onSubmit")},
			{StepID: "checkout", Ordinal: 6, Name: "api.orders.checkout(orderPayload)", TechnicalName: "Checkout.onSubmit", Kind: "call", InvocationID: "handler", Anchor: anchor("Checkout.onSubmit")},
			{StepID: "submitted", Ordinal: 7, Name: "return { success: true }", TechnicalName: "api.orders.checkout", Kind: "return", InvocationID: "checkout", Anchor: anchor("api.orders.checkout")},
			{StepID: "wait", Ordinal: 8, Name: "await api.orders.checkout(orderPayload)", TechnicalName: "Checkout.onSubmit", Kind: "await", InvocationID: "handler", Anchor: anchor("Checkout.onSubmit"), Rules: []string{"boundary:await"}},
			{StepID: "response", Ordinal: 9, Name: "response = await api.orders.checkout(orderPayload)", TechnicalName: "Checkout.onSubmit", Kind: "mutation", InvocationID: "handler", Anchor: anchor("Checkout.onSubmit")},
			{StepID: "decision", Ordinal: 10, Name: "if (response.success)", TechnicalName: "Checkout.onSubmit", Kind: "branch", InvocationID: "handler", Anchor: anchor("Checkout.onSubmit")},
			{StepID: "complete", Ordinal: 11, Name: "setStatus('completed')", TechnicalName: "Checkout.onSubmit", Kind: "call", InvocationID: "handler", Branch: &condition, Anchor: anchor("Checkout.onSubmit"), Rules: []string{"boundary:status"}},
		},
		Edges: []SemanticEdge{
			{FromStepID: "start", ToStepID: "calculate", Kind: "control_flow", ResolutionStatus: "resolved"},
			{FromStepID: "calculate", ToStepID: "reduce", Kind: "resolved_cross_file", ResolutionStatus: "resolved"},
			{FromStepID: "reduce", ToStepID: "calculated", Kind: "control_flow", ResolutionStatus: "resolved"},
			{FromStepID: "calculate", ToStepID: "payload", Kind: "control_flow", ResolutionStatus: "resolved"},
			{FromStepID: "payload", ToStepID: "checkout", Kind: "control_flow", ResolutionStatus: "resolved"},
			{FromStepID: "checkout", ToStepID: "submitted", Kind: "resolved_cross_file", ResolutionStatus: "resolved"},
			{FromStepID: "checkout", ToStepID: "wait", Kind: "control_flow", ResolutionStatus: "resolved"},
			{FromStepID: "wait", ToStepID: "response", Kind: "await_resume", ResolutionStatus: "resolved"},
			{FromStepID: "response", ToStepID: "decision", Kind: "control_flow", ResolutionStatus: "resolved"},
			{FromStepID: "decision", ToStepID: "complete", Kind: "control_flow", ResolutionStatus: "resolved", Conditions: []SemanticBranchCondition{{StepID: "decision", Outcome: "truthy"}}},
		},
	}

	sequence := BuildFlowSequence(mapIR)
	if len(sequence.Frames) != 3 {
		t.Fatalf("frames=%d, want preparation, awaited operation, decision: %+v", len(sequence.Frames), sequence.Frames)
	}
	wantTitles := []string{"orderPayload 준비", "api.orders.checkout(orderPayload)", "if (response.success)"}
	wantChildren := [][]string{{"start", "calculate", "reduce", "calculated", "payload"}, {"checkout", "submitted", "wait", "response"}, {"decision", "complete"}}
	for index, frame := range sequence.Frames {
		if frame.Title != wantTitles[index] || !reflect.DeepEqual(frame.StepRefs, wantChildren[index]) {
			t.Fatalf("frame %d = %+v, want title %q and children %v", index+1, frame, wantTitles[index], wantChildren[index])
		}
	}
}
