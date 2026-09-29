package semantic

import (
	"testing"

	"codeflow/internal/collector/fusion"
	"codeflow/internal/collector/slicing"
)

func TestBuildCodeReadingPathEndsAtVerifiedUnresolvedCallBoundary(t *testing.T) {
	entryAnchor := slicing.Anchor{RepoRelativePath: "view.ts", EnclosingSymbolPath: "updateURL", ByteRange: [2]int{1, 8}, FileHash: "file-hash", SpanHash: "entry-hash"}
	callAnchor := entryAnchor
	callAnchor.ByteRange = [2]int{9, 20}
	callAnchor.SpanHash = "call-hash"
	model := &SemanticMapIR{
		GenerationID: "generation", ComputedBasisID: "basis", ValidatedAgainstSnapshotID: "snapshot",
		Steps: []SemanticStep{
			{StepID: "entry", Ordinal: 1, Kind: "call", Name: "update URL", Anchor: entryAnchor, EvidenceRefs: []string{"entry-proof"}},
			{StepID: "call", Ordinal: 2, Kind: "call", Name: "history.pushState", Anchor: callAnchor, EvidenceRefs: []string{"call-proof"}, Rules: []string{"boundary:history.pushState"}},
		},
		Edges: []SemanticEdge{{FromStepID: "entry", ToStepID: "call", Kind: "control_flow", ResolutionStatus: "resolved"}},
		Evidence: []SemanticEvidence{
			{EvidenceID: "entry-proof", SnapshotID: "snapshot", Anchor: entryAnchor, ValidationStatus: "verified", SourceValidationStatus: "verified"},
			{EvidenceID: "call-proof", SnapshotID: "snapshot", Anchor: callAnchor, ValidationStatus: "verified", SourceValidationStatus: "verified"},
		},
		Unknowns: []fusion.Unknown{{Subject: "history.pushState", Reason: "unresolved_dynamic_call"}},
	}
	selections := []CodeReadingSelection{
		{StepID: "entry", Role: "entry", RequestPart: "URL", InclusionReason: "This starts the requested URL update"},
		{StepID: "call", Role: "boundary", RequestPart: "URL", InclusionReason: "The browser history call has no verified implementation"},
	}
	relations := []CodeReadingRelationSelection{{FromStepID: "entry", ToStepID: "call", EdgeIndexes: []int{0}}}
	if _, err := BuildCodeReadingPath(model, BuildFlowSequence(model), "update URL", nil, selections, relations); err != nil {
		t.Fatalf("verified call boundary was rejected: %v", err)
	}
	model.Unknowns = nil
	if _, err := BuildCodeReadingPath(model, BuildFlowSequence(model), "update URL", nil, selections, relations); err == nil {
		t.Fatal("boundary marker without a matching unknown was accepted")
	}
}

func TestBuildCodeReadingPathRequiresExactSourceAndConfirmedRelations(t *testing.T) {
	callOrdinal := 2
	rootAnchor := slicing.Anchor{RepoRelativePath: "controller.go", EnclosingSymbolPath: "Controller.Apply", ByteRange: [2]int{1, 8}, FileHash: "controller-file", SpanHash: "root-span"}
	callAnchor := rootAnchor
	callAnchor.ByteRange = [2]int{9, 20}
	callAnchor.SpanHash = "call-span"
	entryAnchor := slicing.Anchor{RepoRelativePath: "service.go", EnclosingSymbolPath: "Service.Move", ByteRange: [2]int{5, 24}, FileHash: "service-file", SpanHash: "entry-span"}
	model := &SemanticMapIR{
		MapID: "map-move", GenerationID: "generation", ComputedBasisID: "basis", ValidatedAgainstSnapshotID: "snapshot",
		Steps: []SemanticStep{
			{StepID: "root", Ordinal: 1, Kind: "guard", Name: "input accepted", InvocationID: "root-call", Anchor: rootAnchor, EvidenceRefs: []string{"root-proof"}},
			{StepID: "call", Ordinal: 2, Kind: "call", Name: "apply move", InvocationID: "root-call", Anchor: callAnchor, EvidenceRefs: []string{"call-proof"}},
			{StepID: "entry", Ordinal: 3, Kind: "mutation", Name: "board updated", InvocationID: "move-call", CallerStepOrdinal: &callOrdinal, Anchor: entryAnchor, EvidenceRefs: []string{"entry-proof"}},
		},
		Edges: []SemanticEdge{
			{FromStepID: "root", ToStepID: "call", Kind: "control_flow", ResolutionStatus: "resolved"},
			{FromStepID: "call", ToStepID: "entry", ToSymbolPath: "service.go#Service.Move", Kind: "resolved_cross_file", ResolutionStatus: "resolved"},
		},
		Evidence: []SemanticEvidence{
			{EvidenceID: "root-proof", SnapshotID: "snapshot", Anchor: rootAnchor, ValidationStatus: "verified", SourceValidationStatus: "verified"},
			{EvidenceID: "call-proof", SnapshotID: "snapshot", Anchor: callAnchor, ValidationStatus: "verified", SourceValidationStatus: "verified"},
			{EvidenceID: "entry-proof", SnapshotID: "snapshot", Anchor: entryAnchor, ValidationStatus: "verified", SourceValidationStatus: "verified"},
		},
	}
	sequence := BuildFlowSequence(model)
	focus, err := ResolveRequestedFlowFocus(model, "service.go#Service.Move", "This direct call applies the requested move")
	if err != nil {
		t.Fatal(err)
	}
	selections := []CodeReadingSelection{
		{StepID: "root", Role: "entry", RequestPart: "move", InclusionReason: "The event enters this handler"},
		{StepID: "call", Role: "handoff", RequestPart: "move", InclusionReason: "The handler calls the move implementation"},
		{StepID: "entry", Role: "outcome", RequestPart: "move", InclusionReason: "The implementation updates the board"},
	}
	relations := []CodeReadingRelationSelection{{FromStepID: "root", ToStepID: "call", EdgeIndexes: []int{0}}, {FromStepID: "call", ToStepID: "entry", EdgeIndexes: []int{1}}}
	path, err := BuildCodeReadingPath(model, sequence, "apply move to board", focus, selections, relations)
	if err != nil {
		t.Fatal(err)
	}
	if len(path.Nodes) != 3 || path.Nodes[2].Symbol != "service.go#Service.Move" || path.Nodes[2].FrameID == "" ||
		path.Nodes[2].PreviousNodeIDs[0] != "call" || path.Nodes[1].NextNodeIDs[0] != "entry" ||
		path.Relations[1].Kind != "direct_call" || path.Nodes[2].CodeViewAvailable {
		t.Fatalf("path changed the source or invented CodeView proof: %+v", path)
	}
	incomplete := append([]CodeReadingSelection(nil), selections...)
	incomplete[2].Role = "state_change"
	if _, err := BuildCodeReadingPath(model, sequence, "apply move to board", focus, incomplete, relations); err == nil {
		t.Fatal("path without an outcome or boundary was accepted as complete")
	}
	withoutFocus := append([]CodeReadingSelection(nil), selections[:2]...)
	if _, err := BuildCodeReadingPath(model, sequence, "apply move to board", focus, withoutFocus, relations); err == nil {
		t.Fatal("path omitted the verified implementation")
	}
	unconnected := append([]CodeReadingRelationSelection(nil), relations...)
	unconnected[1].EdgeIndexes = []int{0}
	if _, err := BuildCodeReadingPath(model, sequence, "apply move to board", focus, selections, unconnected); err == nil {
		t.Fatal("path accepted an edge that does not connect the selected code")
	}
	model.Edges[1].ResolutionStatus = "unresolved"
	if _, err := BuildCodeReadingPath(model, sequence, "apply move to board", focus, selections, relations); err == nil {
		t.Fatal("path promoted an unresolved call into the requested implementation")
	}
}

func TestBuildCodeReadingPathPreservesBranchAndJoin(t *testing.T) {
	ids := []string{"root", "left", "right", "join"}
	model := &SemanticMapIR{MapID: "branch-map", GenerationID: "generation", ComputedBasisID: "basis", ValidatedAgainstSnapshotID: "snapshot"}
	for index, id := range ids {
		anchor := slicing.Anchor{RepoRelativePath: "flow.go", EnclosingSymbolPath: "Service.Run", ByteRange: [2]int{index * 10, index*10 + 8}, FileHash: "file-hash", SpanHash: id + "-hash"}
		kind := "mutation"
		if index == 0 {
			kind = "guard"
		}
		model.Steps = append(model.Steps, SemanticStep{StepID: id, Ordinal: index + 1, Kind: kind, Name: id, InvocationID: "run", Anchor: anchor, EvidenceRefs: []string{id + "-proof"}})
		model.Evidence = append(model.Evidence, SemanticEvidence{EvidenceID: id + "-proof", SnapshotID: "snapshot", Anchor: anchor, ValidationStatus: "verified", SourceValidationStatus: "verified"})
	}
	model.Edges = []SemanticEdge{
		{FromStepID: "root", ToStepID: "left", Kind: "control_flow", ResolutionStatus: "resolved"},
		{FromStepID: "root", ToStepID: "right", Kind: "control_flow", ResolutionStatus: "resolved"},
		{FromStepID: "left", ToStepID: "join", Kind: "control_flow", ResolutionStatus: "resolved"},
		{FromStepID: "right", ToStepID: "join", Kind: "control_flow", ResolutionStatus: "resolved"},
	}
	selections := []CodeReadingSelection{
		{StepID: "root", Role: "entry", RequestPart: "run", InclusionReason: "Starts the run"},
		{StepID: "left", Role: "transform", RequestPart: "run", InclusionReason: "Shows the left branch"},
		{StepID: "right", Role: "transform", RequestPart: "run", InclusionReason: "Shows the right branch"},
		{StepID: "join", Role: "outcome", RequestPart: "run", InclusionReason: "Shows the joined result"},
	}
	relations := []CodeReadingRelationSelection{
		{FromStepID: "root", ToStepID: "left", EdgeIndexes: []int{0}},
		{FromStepID: "root", ToStepID: "right", EdgeIndexes: []int{1}},
		{FromStepID: "left", ToStepID: "join", EdgeIndexes: []int{2}},
		{FromStepID: "right", ToStepID: "join", EdgeIndexes: []int{3}},
	}
	path, err := BuildCodeReadingPath(model, BuildFlowSequence(model), "run the service", nil, selections, relations)
	if err != nil {
		t.Fatal(err)
	}
	if len(path.Nodes[0].NextNodeIDs) != 2 || len(path.Nodes[3].PreviousNodeIDs) != 2 || len(path.Relations) != 4 {
		t.Fatalf("branch or join was lost: %+v", path)
	}
	broken := append([]CodeReadingRelationSelection(nil), relations[:3]...)
	if _, err := BuildCodeReadingPath(model, BuildFlowSequence(model), "run the service", nil, selections, broken); err == nil {
		t.Fatal("accepted an unfinished branch")
	}
}

func TestCodeReadingRelationCannotSkipVerifiedSynchronousCall(t *testing.T) {
	callerOrdinal := 1
	model := &SemanticMapIR{
		Steps: []SemanticStep{
			{StepID: "call", Ordinal: callerOrdinal, Kind: "call", InvocationID: "caller"},
			{StepID: "entry", Ordinal: 2, Kind: "mutation", ValueRole: "local_binding", InvocationID: "callee", CallerStepOrdinal: &callerOrdinal},
			{StepID: "returned", Ordinal: 3, Kind: "return", InvocationID: "callee", CallerStepOrdinal: &callerOrdinal},
			{StepID: "next", Ordinal: 4, Kind: "mutation", InvocationID: "caller"},
		},
		Edges: []SemanticEdge{
			{FromStepID: "call", ToStepID: "entry", Kind: "resolved_cross_file", ResolutionStatus: "resolved"},
			{FromStepID: "entry", ToStepID: "returned", Kind: "control_flow", ResolutionStatus: "resolved"},
			{FromStepID: "call", ToStepID: "next", Kind: "control_flow", ResolutionStatus: "resolved"},
			{FromStepID: "returned", ToStepID: "next", Kind: "return", ResolutionStatus: "resolved"},
		},
	}
	if _, err := verifyCodeReadingRelation(model, "call", "next", []int{2}, nil, -1); err == nil {
		t.Fatal("reading path skipped the verified synchronous callee")
	}
	if _, err := verifyCodeReadingRelation(model, "call", "returned", []int{0, 1}, nil, -1); err != nil {
		t.Fatalf("reading path rejected the verified callee entry: %v", err)
	}
	if _, err := verifyCodeReadingRelation(model, "returned", "next", []int{3}, nil, -1); err != nil {
		t.Fatalf("reading path rejected the verified return: %v", err)
	}
}

func TestCodeReadingRelationCannotHideDecisionOrOutcome(t *testing.T) {
	for _, kind := range []string{"guard", "branch", "decision", "await", "throw", "return", "break", "continue", "external_effect", "mutation"} {
		t.Run(kind, func(t *testing.T) {
			model := &SemanticMapIR{
				Steps: []SemanticStep{
					{StepID: "entry", Ordinal: 1, Kind: "call"},
					{StepID: "middle", Ordinal: 2, Kind: kind},
					{StepID: "result", Ordinal: 3, Kind: "mutation"},
				},
				Edges: []SemanticEdge{
					{FromStepID: "entry", ToStepID: "middle", Kind: "control_flow", ResolutionStatus: "resolved"},
					{FromStepID: "middle", ToStepID: "result", Kind: "control_flow", ResolutionStatus: "resolved"},
				},
			}
			if _, err := verifyCodeReadingRelation(model, "entry", "result", []int{0, 1}, nil, -1); err == nil {
				t.Fatal("reading path hid a decision, transfer, effect, or outcome")
			}
			if _, err := verifyCodeReadingRelation(model, "entry", "middle", []int{0}, nil, -1); err != nil {
				t.Fatalf("reading path rejected the visible point: %v", err)
			}
		})
	}
}

func TestCodeReadingRelationKeepsVerifiedIntermediateCodeAsDetail(t *testing.T) {
	anchor := slicing.Anchor{RepoRelativePath: "flow.go", EnclosingSymbolPath: "run", ByteRange: [2]int{10, 20}, FileHash: "file-hash", SpanHash: "span-hash"}
	model := &SemanticMapIR{
		ValidatedAgainstSnapshotID: "snapshot",
		Steps: []SemanticStep{
			{StepID: "entry", Kind: "call"},
			{StepID: "decision", Kind: "guard", Anchor: anchor, EvidenceRefs: []string{"decision-proof"}},
			{StepID: "outcome", Kind: "return"},
		},
		Edges: []SemanticEdge{
			{FromStepID: "entry", ToStepID: "decision", Kind: "control_flow", ResolutionStatus: "resolved"},
			{FromStepID: "decision", ToStepID: "outcome", Kind: "control_flow", ResolutionStatus: "resolved"},
		},
		Evidence: []SemanticEvidence{{EvidenceID: "decision-proof", SnapshotID: "snapshot", Anchor: anchor, ValidationStatus: "verified", SourceValidationStatus: "verified"}},
	}
	selected := map[string]int{"entry": 0, "outcome": 1}
	if _, err := verifyCodeReadingRelation(model, "entry", "outcome", []int{0, 1}, selected, 0); err != nil {
		t.Fatalf("verified intermediate decision should remain available as relation detail: %v", err)
	}
	model.Evidence = nil
	if _, err := verifyCodeReadingRelation(model, "entry", "outcome", []int{0, 1}, selected, 0); err == nil {
		t.Fatal("intermediate decision without verified source was hidden")
	}
}

func TestCodeReadingControlTransferCanBeSelected(t *testing.T) {
	for _, kind := range []string{"break", "continue"} {
		if !validCodeReadingRole(nil, SemanticStep{Kind: kind}, "handoff", false) {
			t.Fatalf("%s has no valid code reading role", kind)
		}
	}
}

func TestBuildCodeReadingPathPreservesSelectedLoopRevisit(t *testing.T) {
	model := &SemanticMapIR{MapID: "map-loop", GenerationID: "generation", ComputedBasisID: "basis", ValidatedAgainstSnapshotID: "snapshot"}
	for index, item := range []struct{ id, kind string }{{"entry", "call"}, {"condition", "branch"}, {"body", "mutation"}, {"result", "return"}} {
		anchor := slicing.Anchor{RepoRelativePath: "loop.go", EnclosingSymbolPath: "Run", ByteRange: [2]int{index * 10, index*10 + 8}, FileHash: "file-hash", SpanHash: item.id + "-hash"}
		model.Steps = append(model.Steps, SemanticStep{StepID: item.id, Ordinal: index + 1, Kind: item.kind, Name: item.id, InvocationID: "run", Anchor: anchor, EvidenceRefs: []string{item.id + "-proof"}})
		model.Evidence = append(model.Evidence, SemanticEvidence{EvidenceID: item.id + "-proof", SnapshotID: "snapshot", Anchor: anchor, ValidationStatus: "verified", SourceValidationStatus: "verified"})
	}
	model.Edges = []SemanticEdge{
		{FromStepID: "entry", ToStepID: "condition", ToSymbolPath: "loop.go#Run", Kind: "control_flow", ResolutionStatus: "resolved"},
		{FromStepID: "condition", ToStepID: "body", ToSymbolPath: "loop.go#Run", Kind: "control_flow", ResolutionStatus: "resolved"},
		{FromStepID: "body", ToStepID: "condition", ToSymbolPath: "loop.go#Run", Kind: "loop_back", ResolutionStatus: "resolved"},
		{FromStepID: "condition", ToStepID: "result", ToSymbolPath: "loop.go#Run", Kind: "control_flow", ResolutionStatus: "resolved"},
	}
	selections := []CodeReadingSelection{
		{StepID: "entry", Role: "entry", RequestPart: "loop", InclusionReason: "Starts the requested loop"},
		{StepID: "condition", Role: "decision", RequestPart: "loop", InclusionReason: "Controls whether the loop repeats"},
		{StepID: "body", Role: "state_change", RequestPart: "loop", InclusionReason: "Changes state during the loop"},
		{StepID: "result", Role: "outcome", RequestPart: "loop", InclusionReason: "Returns after the loop"},
	}
	relations := []CodeReadingRelationSelection{
		{FromStepID: "entry", ToStepID: "condition", EdgeIndexes: []int{0}},
		{FromStepID: "condition", ToStepID: "body", EdgeIndexes: []int{1}},
		{FromStepID: "body", ToStepID: "result", EdgeIndexes: []int{2, 3}},
	}
	path, err := BuildCodeReadingPath(model, BuildFlowSequence(model), "process loop", nil, selections, relations)
	if err != nil {
		t.Fatal(err)
	}
	if path.Relations[2].Kind != "conditional" || len(path.Relations[2].EdgeIndexes) != 2 {
		t.Fatalf("loop return did not preserve its exact edges: %+v", path.Relations[2])
	}
}
