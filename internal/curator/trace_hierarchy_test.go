package curator_test

import (
	"reflect"
	"testing"

	"codeflow/internal/collector/fusion"
	"codeflow/internal/collector/slicing"
	"codeflow/internal/curator"
)

func TestTraceUsesCanonicalGatewayHierarchy(t *testing.T) {
	c := curator.NewCurator()
	for _, tc := range []struct {
		name  string
		steps []fusion.FlowStep
	}{
		{"repeated symbols are not recursion", []fusion.FlowStep{
			{Ordinal: 1, Name: "first", StepID: strPtr("first"), Anchor: slicing.Anchor{RepoRelativePath: "flow.ts", EnclosingSymbolPath: "run"}},
			{Ordinal: 2, Name: "other", StepID: strPtr("other"), Anchor: slicing.Anchor{RepoRelativePath: "flow.ts", EnclosingSymbolPath: "other"}},
			{Ordinal: 3, Name: "repeated", StepID: strPtr("repeated"), Anchor: slicing.Anchor{RepoRelativePath: "flow.ts", EnclosingSymbolPath: "run"}},
		}},
		{"adjacent guards need purpose evidence", []fusion.FlowStep{
			{Ordinal: 1, Name: "start", StepID: strPtr("start")},
			{Ordinal: 2, Kind: "guard", Name: "first guard", StepID: strPtr("first"), Anchor: slicing.Anchor{EnclosingSymbolPath: "run"}},
			{Ordinal: 3, Kind: "guard", Name: "second guard", StepID: strPtr("second"), Anchor: slicing.Anchor{EnclosingSymbolPath: "run"}},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			trace := curator.RawExecutionTrace{FlowID: "flow-trace", Steps: tc.steps}
			actual := c.CurateFlowSequence(trace)
			canonical := c.CurateFlowSpec(fusion.FlowSpec{FlowID: trace.FlowID, Steps: trace.Steps})
			if !reflect.DeepEqual(actual, canonical.Frames) {
				t.Fatalf("trace differs from canonical hierarchy: %+v != %+v", actual, canonical.Frames)
			}
		})
	}
}

func TestTraceHierarchyRetainsLimitationsAndRejectsDuplicateChildren(t *testing.T) {
	c := curator.NewCurator()
	trace := curator.RawExecutionTrace{FlowID: "flow-trace", BasisSha: "basis", Steps: []fusion.FlowStep{{StepID: strPtr("first"), Name: "first"}, {StepID: strPtr("second"), Name: "second"}}}
	sequence, err := c.CurateTrace(trace)
	if err != nil {
		t.Fatal(err)
	}
	if sequence.Sequence.ComputedBasisID != "basis" || len(sequence.Sequence.SummaryLimitations) == 0 {
		t.Fatalf("trace metadata lost: %+v", sequence)
	}
	trace.Steps[1].StepID = trace.Steps[0].StepID
	if _, err := c.CurateTrace(trace); err == nil {
		t.Fatal("duplicate trace step accepted")
	}
}

func TestTraceCurationPreservesUnresolvedAndTruncatedAnalysis(t *testing.T) {
	source := 2
	trace := curator.RawExecutionTrace{FlowID: "flow-limited", Truncated: true, Steps: []fusion.FlowStep{
		{Ordinal: 1, StepID: strPtr("entry"), Name: "entry"},
		{Ordinal: 2, StepID: strPtr("call"), Name: "lookup", Kind: "call"},
	}, Edges: []fusion.FlowEdge{{StepOrdinal: &source, Kind: "unknown_edge", ToSymbolPath: "external#lookup", ResolutionStatus: "unresolved", UnresolvedReason: "target unavailable"}}}
	result, err := curator.NewCurator().CurateTrace(trace)
	if err != nil {
		t.Fatal(err)
	}
	if result.Sequence.Frames[1].Role != "boundary" || result.Sequence.Frames[1].Status != "unknown" {
		t.Fatalf("unresolved call hidden: %+v", result.Sequence.Frames[1])
	}
	reasons := map[string]string{}
	for _, unknown := range result.Map.Unknowns {
		reasons[unknown.Subject] = unknown.Reason
	}
	if reasons["external#lookup"] != "target unavailable" || reasons["flow-traversal"] == "" {
		t.Fatalf("limitations lost: %+v", result.Map.Unknowns)
	}
	if len(result.Map.Edges) != 1 || result.Map.Edges[0].ResolutionStatus == "resolved" {
		t.Fatal("unresolved relation lost or promoted")
	}
	if len(trace.Steps[1].Rules) != 0 {
		t.Fatal("projection changed input boundary rules")
	}
}
