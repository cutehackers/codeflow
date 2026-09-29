package semantic

import (
	"reflect"
	"testing"

	"codeflow/internal/collector/slicing"
)

func TestDeriveDirectReturnConnectionsRequiresSynchronousSourceProof(t *testing.T) {
	callerOrdinal := 1
	callerAnchor := slicing.Anchor{RepoRelativePath: "app/controller.dart", EnclosingSymbolPath: "Controller.run"}
	calleeAnchor := slicing.Anchor{RepoRelativePath: "app/rules.dart", EnclosingSymbolPath: "Rules.apply"}
	steps := []SemanticStep{
		{StepID: "call", Ordinal: 1, Kind: "call", InvocationID: "root", Anchor: callerAnchor},
		{StepID: "entry", Ordinal: 2, Kind: "guard", InvocationID: "child", CallerStepOrdinal: &callerOrdinal, Anchor: calleeAnchor},
		{StepID: "return", Ordinal: 3, Kind: "return", InvocationID: "child", CallerStepOrdinal: &callerOrdinal, Anchor: calleeAnchor},
		{StepID: "next", Ordinal: 4, Kind: "mutation", InvocationID: "root", Anchor: callerAnchor},
	}
	edges := []SemanticEdge{
		{FromStepID: "call", ToStepID: "entry", ToSymbolPath: "app/rules.dart#Rules.apply", Kind: "resolved_cross_file", ResolutionStatus: "resolved"},
		{FromStepID: "entry", ToStepID: "return", ToSymbolPath: "app/rules.dart#Rules.apply", Kind: "control_flow", ResolutionStatus: "resolved"},
		{FromStepID: "call", ToStepID: "next", ToSymbolPath: "app/controller.dart#Controller.run", Kind: "control_flow", ResolutionStatus: "resolved"},
	}
	want := SemanticEdge{FromStepID: "return", ToStepID: "next", ToSymbolPath: "app/controller.dart#Controller.run", Kind: "return", ResolutionStatus: "resolved"}
	result := deriveDirectReturnConnections(steps, edges, map[string]string{"return": "synchronous"})
	if len(result) != len(edges)+1 || !reflect.DeepEqual(result[len(edges)], want) {
		t.Fatalf("verified synchronous return did not resume caller: %+v", result)
	}
	if repeated := deriveDirectReturnConnections(steps, result, map[string]string{"return": "synchronous"}); len(repeated) != len(result) {
		t.Fatalf("return connection was duplicated: %+v", repeated)
	}

	for _, tc := range []struct {
		name  string
		steps []SemanticStep
		edges []SemanticEdge
		mode  string
	}{
		{name: "missing mode", steps: steps, edges: edges},
		{name: "deferred callable", steps: steps, edges: edges, mode: "deferred"},
		{name: "unproven call", steps: steps, edges: edges[1:], mode: "synchronous"},
		{name: "no continuation", steps: steps, edges: edges[:2], mode: "synchronous"},
		{name: "unreachable return", steps: steps, edges: []SemanticEdge{edges[0], edges[2]}, mode: "synchronous"},
		{name: "finalizer may override", steps: steps, edges: append(append([]SemanticEdge(nil), edges...), SemanticEdge{FromStepID: "return", ToStepID: "next", Kind: "finally", ResolutionStatus: "resolved"}), mode: "synchronous"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := deriveDirectReturnConnections(tc.steps, tc.edges, map[string]string{"return": tc.mode}); len(got) != len(tc.edges) {
				t.Fatalf("unproven return relation was added: %+v", got)
			}
		})
	}
}

func TestVerifiedDirectReturnModeRejectsStaleOrIncompleteSourceProof(t *testing.T) {
	step := slicing.SliceStep{
		Kind:   "return",
		Anchor: slicing.Anchor{RepoRelativePath: "lib/rules.dart", FileHash: "source-hash", ByteRange: [2]int{10, 20}},
		FlowContext: &slicing.FlowContextMetadata{
			SnapshotID: "snapshot-1", CanonicalPath: "lib/rules.dart", SourceHash: "source-hash",
			Statement: slicing.StatementNodeMetadata{ByteRange: [2]int{10, 20}, DirectReturnCompletion: true},
			Callable:  slicing.CallableMetadata{CompletionMode: "synchronous"},
		},
	}
	if mode := verifiedDirectReturnMode(step, "snapshot-1"); mode != "synchronous" {
		t.Fatalf("valid direct return proof rejected: %q", mode)
	}
	for _, tc := range []struct {
		name string
		edit func(*slicing.SliceStep)
	}{
		{name: "deferred", edit: func(s *slicing.SliceStep) { s.FlowContext.Callable.CompletionMode = "deferred" }},
		{name: "finalizer", edit: func(s *slicing.SliceStep) { s.FlowContext.Statement.DirectReturnCompletion = false }},
		{name: "stale snapshot", edit: func(s *slicing.SliceStep) { s.FlowContext.SnapshotID = "snapshot-0" }},
		{name: "wrong path", edit: func(s *slicing.SliceStep) { s.FlowContext.CanonicalPath = "lib/other.dart" }},
		{name: "wrong hash", edit: func(s *slicing.SliceStep) { s.FlowContext.SourceHash = "different" }},
		{name: "wrong span", edit: func(s *slicing.SliceStep) { s.FlowContext.Statement.ByteRange = [2]int{11, 20} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := step
			context := *step.FlowContext
			candidate.FlowContext = &context
			tc.edit(&candidate)
			if mode := verifiedDirectReturnMode(candidate, "snapshot-1"); mode != "" {
				t.Fatalf("unverified direct return was promoted: %q", mode)
			}
		})
	}
}
