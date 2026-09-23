package semantic_test

import (
	"reflect"
	"testing"

	"codeflow/internal/collector/fusion"
	"codeflow/internal/collector/slicing"
	"codeflow/internal/curator/semantic"
)

func TestFlowSpecProjectionPreservesEvidenceAndAmbiguousCalls(t *testing.T) {
	ordinal := 1
	sourceID := "invoke"
	lens := &fusion.CodeLens{StartLine: 201, EndLine: 203}
	spec := &fusion.FlowSpec{FlowID: "checkout", BasisSha: "basis", Steps: []fusion.FlowStep{
		{StepID: &sourceID, Ordinal: 1, Name: "Invoke", Anchor: slicing.Anchor{RepoRelativePath: "entry.ts", EnclosingSymbolPath: "invoke"}, CodeLens: lens, Confidence: 1},
		{Ordinal: 2, Name: "Process first call", Anchor: slicing.Anchor{EnclosingSymbolPath: "process"}},
		{Ordinal: 3, Name: "Process second call", Anchor: slicing.Anchor{EnclosingSymbolPath: "process"}},
	}, Edges: []fusion.FlowEdge{{StepOrdinal: &ordinal, ToSymbolPath: "process", Kind: "call", ResolutionStatus: "resolved"}}, Unknowns: []fusion.Unknown{{Subject: "existing", Reason: "original"}}}
	original := append([]fusion.Unknown(nil), spec.Unknowns...)
	m := semantic.ProjectFlowSpec(spec, "generation")
	if m.MapID != "map-checkout" || m.GenerationID != "generation" || m.Steps[0].StepID != sourceID || !reflect.DeepEqual(m.Steps[0].CodeLens, lens) {
		t.Fatalf("projection lost identity or source: %+v", m)
	}
	if len(m.Steps) != 3 || m.Steps[1].StepID == m.Steps[2].StepID {
		t.Fatal("separate invocations merged")
	}
	if len(m.Edges) != 1 || m.Edges[0].FromStepID != sourceID || m.Edges[0].ToStepID != "" || m.Edges[0].ResolutionStatus == "resolved" {
		t.Fatalf("ambiguous invocation resolved: %+v", m.Edges)
	}
	if len(m.Unknowns) != 2 || !reflect.DeepEqual(spec.Unknowns, original) {
		t.Fatal("unknowns lost or caller modified")
	}
	for _, frame := range semantic.BuildFlowSequence(m).Frames {
		if frame.Status == "verified" {
			t.Fatal("confidence promoted to verified evidence")
		}
	}
}
