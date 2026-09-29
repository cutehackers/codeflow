package semantic

import "testing"

func TestFlowSequenceReportsUnprovenRequestRelevance(t *testing.T) {
	model := &SemanticMapIR{
		MapID: "map-request", GenerationID: "generation", ComputedBasisID: "basis",
		Summary: MapSummary{Requested: "How is the move processed?"},
		Steps: []SemanticStep{
			{StepID: "entry", Ordinal: 1, Kind: "call", Name: "onTap", InvocationID: "handler"},
			{StepID: "change", Ordinal: 2, Kind: "mutation", Name: "state = nextState", InvocationID: "handler"},
		},
		Edges: []SemanticEdge{{FromStepID: "entry", ToStepID: "change", Kind: "control_flow", ResolutionStatus: "resolved"}},
	}
	sequence := BuildFlowSequence(model)
	if len(sequence.SummaryLimitations) != 1 || sequence.SummaryLimitations[0].Code != "request_relevance_unproven" || len(sequence.SummaryLimitations[0].FrameRefs) != len(sequence.Frames) {
		t.Fatalf("missing request relevance limitation: %+v", sequence.SummaryLimitations)
	}
	if err := ValidateFlowSequenceReferences(model, sequence); err != nil {
		t.Fatalf("invalid hierarchy after limitation: %v", err)
	}
	model.Steps[1].Rules = []string{"request_target"}
	if got := BuildFlowSequence(model).SummaryLimitations; len(got) != 0 {
		t.Fatalf("explicit request target should supply selection evidence: %+v", got)
	}
}
