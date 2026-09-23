package semantic

import (
	"codeflow/internal/collector/slicing"
	"testing"
)

func TestReturnRelationRejectsMissingInvocationProof(t *testing.T) {
	for _, source := range []string{"missing", "legacy"} {
		t.Run(source, func(t *testing.T) {
			model := &SemanticMapIR{Steps: []SemanticStep{
				{StepID: "caller", Ordinal: 1, Kind: "call", InvocationID: "root", Anchor: slicing.Anchor{RepoRelativePath: "flow.ts", EnclosingSymbolPath: "start"}},
				{StepID: "legacy", Ordinal: 2, Kind: "return"},
			}, Edges: []SemanticEdge{{FromStepID: source, ToStepID: "caller", Kind: "return", ResolutionStatus: "resolved"}}}
			if err := validateInvocationRelations(model); err == nil {
				t.Fatal("accepted return without invocation proof")
			}
		})
	}
}

func TestAwaitRelationValidationWithoutReturnEdges(t *testing.T) {
	for _, tc := range []struct {
		source, relation string
		valid            bool
	}{{"await", "await_resume", true}, {"call", "await_resume", false}, {"await", "control_flow", false}} {
		t.Run(tc.source+tc.relation, func(t *testing.T) {
			anchor := slicing.Anchor{RepoRelativePath: "flow.ts", EnclosingSymbolPath: "start"}
			model := &SemanticMapIR{Steps: []SemanticStep{{StepID: "wait", Ordinal: 1, Kind: tc.source, InvocationID: "root", Anchor: anchor}, {StepID: "next", Ordinal: 2, Kind: "mutation", InvocationID: "root", Anchor: anchor}}, Edges: []SemanticEdge{{FromStepID: "wait", ToStepID: "next", ToSymbolPath: "flow.ts#start", Kind: tc.relation, ResolutionStatus: "resolved"}}}
			if err := validateInvocationRelations(model); (err == nil) != tc.valid {
				t.Fatalf("validation=%v want valid=%v", err, tc.valid)
			}
		})
	}
}

func TestParallelWaitRelationRequiresCallAndAwait(t *testing.T) {
	for _, tc := range []struct {
		sourceKind, targetKind string
		valid                  bool
	}{
		{sourceKind: "call", targetKind: "await", valid: true},
		{sourceKind: "mutation", targetKind: "await"},
		{sourceKind: "call", targetKind: "mutation"},
	} {
		t.Run(tc.sourceKind+"-"+tc.targetKind, func(t *testing.T) {
			anchor := slicing.Anchor{RepoRelativePath: "flow.ts", EnclosingSymbolPath: "start"}
			model := &SemanticMapIR{Steps: []SemanticStep{{StepID: "call", Ordinal: 1, Kind: tc.sourceKind, InvocationID: "root", Anchor: anchor}, {StepID: "wait", Ordinal: 2, Kind: tc.targetKind, InvocationID: "root", Anchor: anchor}}, Edges: []SemanticEdge{{FromStepID: "call", ToStepID: "wait", ToSymbolPath: "flow.ts#start", Kind: "parallel_wait", ResolutionStatus: "resolved"}}}
			if err := validateInvocationRelations(model); (err == nil) != tc.valid {
				t.Fatalf("validation=%v want valid=%v", err, tc.valid)
			}
		})
	}
}

func TestAwaitLoopBackRelationRequiresAwait(t *testing.T) {
	for _, sourceKind := range []string{"await", "call"} {
		t.Run(sourceKind, func(t *testing.T) {
			anchor := slicing.Anchor{RepoRelativePath: "flow.ts", EnclosingSymbolPath: "retry"}
			model := &SemanticMapIR{Steps: []SemanticStep{{StepID: "condition", Ordinal: 1, Kind: "branch", InvocationID: "root", Anchor: anchor}, {StepID: "wait", Ordinal: 2, Kind: sourceKind, InvocationID: "root", Anchor: anchor}}, Edges: []SemanticEdge{{FromStepID: "wait", ToStepID: "condition", ToSymbolPath: "flow.ts#retry", Kind: "await_loop_back", ResolutionStatus: "resolved"}}}
			if err := validateInvocationRelations(model); (err == nil) != (sourceKind == "await") {
				t.Fatalf("validation=%v", err)
			}
		})
	}
}
