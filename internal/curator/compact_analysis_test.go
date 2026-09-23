package curator_test

import (
	"testing"

	"codeflow/internal/collector/fusion"
	"codeflow/internal/collector/slicing"
	"codeflow/internal/curator"
	"codeflow/internal/curator/semantic"
)

func TestCompactAnalysisPreservesGraphWithoutAliasing(t *testing.T) {
	span := [2]int{0, 100}
	anchor := slicing.Anchor{RepoRelativePath: "flow.ts", EnclosingSymbolPath: "run", ByteRange: [2]int{10, 20}, SymbolRange: &span}
	model := &semantic.SemanticMapIR{MapID: "map-compact", Steps: []semantic.SemanticStep{
		{StepID: "decision", Ordinal: 1, Name: "if (ready)", Kind: "branch", InvocationID: "run", Anchor: anchor},
		{StepID: "next", Ordinal: 2, Name: "next()", Kind: "call", InvocationID: "run", Anchor: anchor},
	}, Edges: []semantic.SemanticEdge{{FromStepID: "decision", ToStepID: "next", Kind: "control_flow", ResolutionStatus: "resolved", Conditions: []semantic.SemanticBranchCondition{{StepID: "decision", Outcome: "truthy"}}}, {FromStepID: "next", ToSymbolPath: "external#save", Kind: "unknown_edge", ResolutionStatus: "unresolved"}}, Unknowns: []fusion.Unknown{{Subject: "external#save", Reason: "target missing"}}}
	sequence := semantic.BuildFlowSequence(model)
	payload, err := curator.BuildCompactAnalysisPayload(model, sequence)
	if err != nil {
		t.Fatal(err)
	}
	if len(payload.Steps) != 2 || len(payload.Edges) != 2 || len(payload.Unknowns) != 1 || payload.Edges[1].ResolutionStatus != "unresolved" {
		t.Fatalf("analysis facts lost: %+v", payload)
	}
	payload.Edges[0].Conditions[0].Outcome = "falsy"
	payload.Steps[0].Anchor.SymbolRange[0] = 99
	payload.Unknowns[0].Reason = "changed"
	payload.Frames[0].StepRefs[0] = "changed"
	if model.Edges[0].Conditions[0].Outcome != "truthy" || model.Steps[0].Anchor.SymbolRange[0] != 0 || model.Unknowns[0].Reason != "target missing" || sequence.Frames[0].StepRefs[0] != "decision" {
		t.Fatal("compact mutation changed source analysis")
	}
	sequence.Frames[0].PrimaryStepRef = "missing"
	if _, err := curator.BuildCompactAnalysisPayload(model, sequence); err == nil {
		t.Fatal("invalid hierarchy accepted")
	}
}
