package semantic

import (
	"testing"

	"codeflow/internal/collector/slicing"
)

func TestResolveRequestedFlowFocusRequiresOneReachableVerifiedCall(t *testing.T) {
	callOrdinal := 2
	otherCallOrdinal := 4
	entryAnchor := slicing.Anchor{
		RepoRelativePath: "lib/move.dart", EnclosingSymbolPath: "MoveService.apply",
		ByteRange: [2]int{12, 22}, FileHash: "file-hash", SpanHash: "span-hash",
	}
	rootAnchor := slicing.Anchor{RepoRelativePath: "lib/controller.dart", EnclosingSymbolPath: "Controller.onTap", ByteRange: [2]int{2, 10}, FileHash: "controller-hash", SpanHash: "call-hash"}
	model := &SemanticMapIR{
		ValidatedAgainstSnapshotID: "snapshot-1",
		Steps: []SemanticStep{
			{StepID: "root", Ordinal: 1, Kind: "guard", InvocationID: "root-call", Anchor: rootAnchor},
			{StepID: "call", Ordinal: 2, Kind: "call", InvocationID: "root-call", Anchor: rootAnchor, EvidenceRefs: []string{"evidence-call"}},
			{StepID: "entry", Ordinal: 3, Kind: "mutation", InvocationID: "move-call", CallerStepOrdinal: &callOrdinal, Anchor: entryAnchor, EvidenceRefs: []string{"evidence-1"}},
			{StepID: "unreachable-call", Ordinal: 4, Kind: "call", InvocationID: "root-call", Anchor: rootAnchor, EvidenceRefs: []string{"evidence-call"}},
			{StepID: "unreachable-entry", Ordinal: 5, Kind: "mutation", InvocationID: "other-call", CallerStepOrdinal: &otherCallOrdinal, Anchor: entryAnchor, EvidenceRefs: []string{"evidence-1"}},
		},
		Edges: []SemanticEdge{
			{FromStepID: "root", ToStepID: "call", Kind: "control_flow", ResolutionStatus: "resolved"},
			{FromStepID: "call", ToStepID: "entry", ToSymbolPath: "lib/move.dart#MoveService.apply", Kind: "resolved_cross_file", ResolutionStatus: "resolved"},
			{FromStepID: "unreachable-call", ToStepID: "unreachable-entry", ToSymbolPath: "lib/move.dart#MoveService.apply", Kind: "resolved_cross_file", ResolutionStatus: "resolved"},
		},
		Evidence: []SemanticEvidence{{
			EvidenceID: "evidence-1", SnapshotID: "snapshot-1", Anchor: entryAnchor,
			ValidationStatus: "verified", SourceValidationStatus: "verified",
		}, {
			EvidenceID: "evidence-call", SnapshotID: "snapshot-1", Anchor: rootAnchor,
			ValidationStatus: "verified", SourceValidationStatus: "verified",
		}},
	}
	focus, err := ResolveRequestedFlowFocus(model, "lib/move.dart#MoveService.apply", "The selected move branch calls this implementation")
	if err != nil || focus == nil || focus.CallStepID != "call" || focus.EntryStepID != "entry" || len(focus.EvidenceRefs) != 2 {
		t.Fatalf("verified request focus was not retained: focus=%+v err=%v", focus, err)
	}
	if _, err := ResolveRequestedFlowFocus(model, "lib/move.dart#MoveService.apply", ""); err == nil {
		t.Fatal("a focus without the selector's reason was accepted")
	}
	if _, err := ResolveRequestedFlowFocus(model, "lib/other.dart#MoveService.apply", "other function"); err == nil {
		t.Fatal("a name-only target without a direct call was accepted")
	}

	model.Edges = append(model.Edges, SemanticEdge{FromStepID: "root", ToStepID: "unreachable-call", Kind: "control_flow", ResolutionStatus: "resolved"})
	if _, err := ResolveRequestedFlowFocus(model, "lib/move.dart#MoveService.apply", "move branch"); err == nil {
		t.Fatal("two reachable calls to one symbol were silently merged")
	}
	model.Edges = model.Edges[:len(model.Edges)-1]
	model.Evidence[0].SourceValidationStatus = "unknown"
	if _, err := ResolveRequestedFlowFocus(model, "lib/move.dart#MoveService.apply", "move branch"); err == nil {
		t.Fatal("unverified source was accepted as request focus")
	}
}
