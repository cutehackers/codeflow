package semantic

import (
	"testing"

	"codeflow/internal/collector/slicing"
)

func TestFlowSequenceMarksUnprovenCallbackExecutionPartial(t *testing.T) {
	anchor := slicing.Anchor{RepoRelativePath: "lib/service.dart", EnclosingSymbolPath: "Service.run"}
	model := &SemanticMapIR{
		MapID: "map-callback", Authority: "candidate",
		Steps: []SemanticStep{
			{StepID: "call", Ordinal: 1, Kind: "call", InvocationID: "root", Anchor: anchor, EvidenceRefs: []string{"source"}},
			{StepID: "callback-return", Ordinal: 2, Kind: "return", InvocationID: "callback", Anchor: anchor, EvidenceRefs: []string{"source"}},
			{StepID: "after", Ordinal: 3, Kind: "mutation", InvocationID: "root", Anchor: anchor, EvidenceRefs: []string{"source"}},
		},
		Edges:    []SemanticEdge{{FromStepID: "call", ToStepID: "after", Kind: "control_flow", ResolutionStatus: "resolved"}},
		Evidence: []SemanticEvidence{{EvidenceID: "source", ValidationStatus: "verified"}},
	}
	sequence := BuildFlowSequence(model)
	callbackFrame := frameContainingStep(sequence, "callback-return")
	if callbackFrame == nil || callbackFrame.Status != "partial" {
		t.Fatalf("unproven callback execution was presented as verified: %+v", callbackFrame)
	}
	if len(sequence.SummaryLimitations) == 0 || sequence.SummaryLimitations[len(sequence.SummaryLimitations)-1].Code != "invocation_unproven" {
		t.Fatalf("missing callback execution limitation: %+v", sequence.SummaryLimitations)
	}
	for _, limitation := range sequence.SummaryLimitations {
		if limitation.Code != "grouping_evidence_missing" {
			continue
		}
		for _, ref := range limitation.FrameRefs {
			if ref == callbackFrame.FrameID {
				t.Fatalf("unproven callback was also reported as a grouping failure: %+v", sequence.SummaryLimitations)
			}
		}
	}
	historical := *model
	historical.Authority = "historical"
	if restored := frameContainingStep(BuildFlowSequence(&historical), "callback-return"); restored == nil || restored.Status != "verified" {
		t.Fatalf("historical projection changed: %+v", restored)
	}
	resolved := *model
	resolved.Steps = append([]SemanticStep(nil), model.Steps...)
	caller := 1
	resolved.Steps[1].CallerStepOrdinal = &caller
	resolved.Edges = append(append([]SemanticEdge(nil), model.Edges...), SemanticEdge{FromStepID: "call", ToStepID: "callback-return", Kind: "resolved_cross_file", ResolutionStatus: "resolved"})
	if called := frameContainingStep(BuildFlowSequence(&resolved), "callback-return"); called == nil || called.Status != "verified" {
		t.Fatalf("proven direct call lost verified status: %+v", called)
	}
}

func TestFlowSequenceKeepsProvenProcessGroupingLimitationSeparateFromCallback(t *testing.T) {
	anchor := slicing.Anchor{RepoRelativePath: "lib/service.dart", EnclosingSymbolPath: "Service.run"}
	model := &SemanticMapIR{
		MapID: "map-process-callback", Authority: "candidate",
		Steps: []SemanticStep{
			{StepID: "call", Ordinal: 1, Kind: "call", InvocationID: "root", Anchor: anchor},
			{StepID: "first-write", Ordinal: 2, Kind: "mutation", InvocationID: "root", Anchor: anchor},
			{StepID: "second-write", Ordinal: 3, Kind: "mutation", InvocationID: "root", Anchor: anchor},
			{StepID: "callback-return", Ordinal: 4, Kind: "return", InvocationID: "callback", Anchor: anchor},
		},
		Edges: []SemanticEdge{
			{FromStepID: "call", ToStepID: "first-write", Kind: "control_flow", ResolutionStatus: "resolved"},
			{FromStepID: "first-write", ToStepID: "second-write", Kind: "control_flow", ResolutionStatus: "resolved"},
			{FromStepID: "call", ToStepID: "callback-return", Kind: "callback_argument", ResolutionStatus: "resolved"},
		},
	}
	sequence := BuildFlowSequence(model)
	first := frameContainingStep(sequence, "first-write")
	second := frameContainingStep(sequence, "second-write")
	callback := frameContainingStep(sequence, "callback-return")
	if first == nil || second == nil || callback == nil {
		t.Fatalf("missing source frames: %+v", sequence.Frames)
	}
	var grouping, invocation map[string]bool
	for _, limitation := range sequence.SummaryLimitations {
		refs := make(map[string]bool, len(limitation.FrameRefs))
		for _, ref := range limitation.FrameRefs {
			refs[ref] = true
		}
		switch limitation.Code {
		case "grouping_evidence_missing":
			grouping = refs
		case "invocation_unproven":
			invocation = refs
		}
	}
	if !grouping[first.FrameID] || !grouping[second.FrameID] || grouping[callback.FrameID] || !invocation[callback.FrameID] {
		t.Fatalf("process grouping and callback invocation limits were confused: %+v", sequence.SummaryLimitations)
	}
}

func frameContainingStep(sequence *FlowSequence, stepID string) *FlowSequenceFrame {
	for index := range sequence.Frames {
		for _, ref := range sequence.Frames[index].StepRefs {
			if ref == stepID {
				return &sequence.Frames[index]
			}
		}
	}
	return nil
}
