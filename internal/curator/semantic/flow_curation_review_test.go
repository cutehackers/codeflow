package semantic_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"codeflow/internal/collector/slicing"
	"codeflow/internal/curator/semantic"
)

func TestCurationReviewPreservesIndependentDecisionsAcrossRequests(t *testing.T) {
	anchor := slicing.Anchor{RepoRelativePath: "middleware.ts", EnclosingSymbolPath: "canPurchase", FileHash: "source"}
	model := &semantic.SemanticMapIR{MapID: "map-checkout", Steps: []semantic.SemanticStep{
		{StepID: "entry", Kind: "user_action", Ordinal: 1},
		{StepID: "permission", Kind: "guard", Ordinal: 2, InvocationID: "predicate", Anchor: anchor, EvidenceRefs: []string{"permission-source"}},
		{StepID: "payment-limit", Kind: "guard", Ordinal: 3, InvocationID: "predicate", Anchor: anchor},
		{StepID: "result", Kind: "return", Ordinal: 4, InvocationID: "predicate", Anchor: anchor},
	}, Edges: []semantic.SemanticEdge{
		{FromStepID: "permission", ToStepID: "payment-limit", Kind: "control_flow", ResolutionStatus: "resolved", Conditions: []semantic.SemanticBranchCondition{{StepID: "permission", Outcome: "falsy"}}},
		{FromStepID: "payment-limit", ToStepID: "result", Kind: "control_flow", ResolutionStatus: "resolved", Conditions: []semantic.SemanticBranchCondition{{StepID: "payment-limit", Outcome: "falsy"}}},
	}}
	var baseline *semantic.FlowSequence
	for _, request := range []string{"접근이 거부되는 이유", "결제 한도가 적용되는 이유", ""} {
		model.Summary.Requested = request
		before, _ := json.Marshal(model)
		review := semantic.ReviewFlowSequence(model)
		if len(review.Candidates) != len(model.Steps) {
			t.Fatalf("candidate decisions missing: %+v", review)
		}
		if review.Requested != request || review.ScopeBasis == "" {
			t.Fatal("request context or its absence was lost")
		}
		if !reflect.DeepEqual(review.Sequence, semantic.BuildFlowSequence(model)) || !reflect.DeepEqual(review, semantic.ReviewFlowSequence(model)) {
			t.Fatal("review changed canonical result or is nondeterministic")
		}
		if baseline != nil && !reflect.DeepEqual(baseline, review.Sequence) {
			t.Fatal("request wording alone changed source-based grouping")
		}
		baseline = review.Sequence
		for i, candidate := range review.Candidates {
			if candidate.StepRef != model.Steps[i].StepID || candidate.FrameID == "" || candidate.Rule == "" || candidate.Consequence == "" || candidate.RequestRelation == "" {
				t.Fatalf("incomplete decision: %+v", candidate)
			}
			if candidate.RequestSpecific {
				t.Fatal("request-specific necessity invented without evidence")
			}
		}
		permission := review.Candidates[1]
		if permission.Rule != "decision" || len(permission.SupportingEdges) != 1 || !reflect.DeepEqual(permission.EvidenceRefs, []string{"permission-source"}) || permission.GroupingReason == "" {
			t.Fatalf("decision evidence lost: %+v", permission)
		}
		review.Candidates[1].SupportingEdges[0].Conditions[0].Outcome = "truthy"
		review.Candidates[1].EvidenceRefs[0] = "changed"
		review.Candidates[1].SupportingStepRefs[0] = "changed"
		after, _ := json.Marshal(model)
		if !reflect.DeepEqual(before, after) {
			t.Fatal("review mutations changed source facts")
		}
	}
}

func TestCurationReviewExplainsMergedReturnAndStableConnectionOrder(t *testing.T) {
	anchor := slicing.Anchor{RepoRelativePath: "total.ts", EnclosingSymbolPath: "total", ByteRange: [2]int{10, 20}}
	returned := anchor
	returned.ByteRange = [2]int{3, 21}
	model := &semantic.SemanticMapIR{MapID: "map-total", Steps: []semantic.SemanticStep{
		{StepID: "entry", Kind: "user_action"},
		{StepID: "call", Kind: "call", InvocationID: "total", Anchor: anchor},
		{StepID: "return", Kind: "return", InvocationID: "total", Anchor: returned},
	}, Edges: []semantic.SemanticEdge{
		{FromStepID: "entry", ToStepID: "call", Kind: "call", ResolutionStatus: "resolved"},
		{FromStepID: "call", ToStepID: "return", Kind: "control_flow", ResolutionStatus: "resolved"},
	}}
	review := semantic.ReviewFlowSequence(model)
	if len(review.Sequence.Frames) != 2 || review.Candidates[1].FrameID != review.Candidates[2].FrameID {
		t.Fatal("review lost merged membership")
	}
	if review.Candidates[1].GroupingReason != review.Sequence.Frames[1].CollapsedDetail.Reason || !reflect.DeepEqual(review.Candidates[1].SupportingStepRefs, []string{"call", "return"}) {
		t.Fatalf("merge reason or supporting steps lost: %+v", review.Candidates[1])
	}
	model.Edges[0], model.Edges[1] = model.Edges[1], model.Edges[0]
	if !reflect.DeepEqual(review, semantic.ReviewFlowSequence(model)) {
		t.Fatal("connection enumeration changed curation review")
	}
	if semantic.ReviewFlowSequence(nil) != nil {
		t.Fatal("nil input invented a review")
	}
	if len(semantic.ReviewFlowSequence(&semantic.SemanticMapIR{}).Candidates) != 0 {
		t.Fatal("empty input invented candidates")
	}
}

func TestCurationReviewDoesNotExposeSourcePointers(t *testing.T) {
	condition := "request.allowed"
	symbolRange := [2]int{0, 80}
	model := &semantic.SemanticMapIR{MapID: "map-guard", Steps: []semantic.SemanticStep{
		{StepID: "guard", Kind: "guard", Branch: &condition, Anchor: slicing.Anchor{RepoRelativePath: "guard.ts", EnclosingSymbolPath: "guard", SymbolRange: &symbolRange}},
	}}
	for _, sequence := range []*semantic.FlowSequence{semantic.ReviewFlowSequence(model).Sequence, semantic.BuildFlowSequence(model)} {
		*sequence.Frames[0].Condition = "changed"
		sequence.Frames[0].SourceAnchor.SymbolRange[0] = 99
		if condition != "request.allowed" || symbolRange != [2]int{0, 80} {
			t.Fatal("curation result exposed mutable source pointers")
		}
	}
}
