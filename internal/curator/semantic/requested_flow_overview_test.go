package semantic

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"codeflow/internal/collector/fusion"
	"codeflow/internal/collector/slicing"
)

func requestedFlowOverviewFixture() (*SemanticMapIR, []RequestedFlowSegmentSelection) {
	model := &SemanticMapIR{GenerationID: "generation", ComputedBasisID: "basis", ValidatedAgainstSnapshotID: "snapshot"}
	for i, id := range []string{"entry", "prepare", "decision", "result", "other"} {
		anchor := slicing.Anchor{RepoRelativePath: "controller.go", EnclosingSymbolPath: "apply", ByteRange: [2]int{i * 10, i*10 + 8}, FileHash: "file", SpanHash: id}
		if id == "result" {
			anchor.RepoRelativePath = "service.go"
			anchor.EnclosingSymbolPath = "save"
		}
		kind := "mutation"
		if id == "decision" {
			kind = "guard"
		}
		if id == "other" {
			kind = "return"
		}
		model.Steps = append(model.Steps, SemanticStep{StepID: id, Ordinal: i + 1, Kind: kind, InvocationID: "invoke", Anchor: anchor, EvidenceRefs: []string{id + "-proof"}})
		model.Evidence = append(model.Evidence, SemanticEvidence{EvidenceID: id + "-proof", SnapshotID: "snapshot", Anchor: anchor, ValidationStatus: "verified", SourceValidationStatus: "verified"})
	}
	model.Edges = []SemanticEdge{
		{FromStepID: "entry", ToStepID: "prepare", Kind: "control_flow", ResolutionStatus: "resolved"},
		{FromStepID: "prepare", ToStepID: "decision", Kind: "control_flow", ResolutionStatus: "resolved"},
		{FromStepID: "decision", ToStepID: "result", Kind: "resolved_cross_file", ResolutionStatus: "resolved"},
		{FromStepID: "decision", ToStepID: "prepare", Kind: "loop_back", ResolutionStatus: "resolved"},
		{FromStepID: "decision", ToStepID: "other", Kind: "control_flow", ResolutionStatus: "resolved", Conditions: []SemanticBranchCondition{{StepID: "decision", Outcome: "falsy"}}},
		{FromStepID: "decision", ToSymbolPath: "callback", Kind: "external", ResolutionStatus: "unresolved"},
	}
	for i := range model.Edges {
		if target := model.Edges[i].ToStepID; target != "" {
			for _, step := range model.Steps {
				if step.StepID == target {
					model.Edges[i].ToSymbolPath = step.Anchor.RepoRelativePath + "#" + step.Anchor.EnclosingSymbolPath
				}
			}
		}
	}
	model.Steps[3].InvocationID = "save"
	callerOrdinal := 3
	model.Steps[3].CallerStepOrdinal = &callerOrdinal
	model.Unknowns = []fusion.Unknown{{Subject: "callback", Reason: "unproven_callback_execution"}}
	model.Steps[2].Rules = []string{"boundary:callback"}
	selections := []RequestedFlowSegmentSelection{
		{SegmentID: "prepare-input", Purpose: "Prepare the requested input", InclusionReason: "The entry validates and prepares the input", StepIDs: []string{"entry", "prepare"}, Blocks: []RequestedFlowBlockSelection{{StepIDs: []string{"entry", "prepare"}}}},
		{SegmentID: "save-result", Purpose: "Save the accepted result", InclusionReason: "This implements the requested persistence", StepIDs: []string{"result"}, Blocks: []RequestedFlowBlockSelection{{StepIDs: []string{"result"}}}},
	}
	return model, selections
}

func TestBuildRequestedFlowOverviewPreservesBlocksLoopsAndOmittedOutcomes(t *testing.T) {
	model, selections := requestedFlowOverviewFixture()
	relations := []RequestedFlowRelationSelection{
		{FromSegmentID: "prepare-input", ToSegmentID: "save-result", FromStepID: "prepare", ToStepID: "result", EdgeIndexes: []int{1, 2}},
		{FromSegmentID: "prepare-input", ToSegmentID: "prepare-input", FromStepID: "prepare", ToStepID: "prepare", EdgeIndexes: []int{1, 3}},
	}
	overview, err := BuildRequestedFlowOverview(model, BuildFlowSequence(model), "save accepted input", selections, relations)
	if err != nil {
		t.Fatal(err)
	}
	if len(overview.Segments) != 2 || len(overview.Segments[0].Blocks[0].StepIDs) != 2 || overview.Segments[0].EntryStepID != "entry" || overview.Segments[0].Blocks[0].RepoRelativePath != "" {
		t.Fatalf("wrong segment/source ownership: %+v", overview.Segments)
	}
	if len(overview.Relations[0].Edges) != 2 || overview.Relations[1].Kind != "conditional" {
		t.Fatalf("lost ordered route or loop: %+v", overview.Relations)
	}
	outcome, callback := false, false
	for _, relation := range overview.Relations {
		if relation.FromStepID == "decision" && relation.ToStepID == "other" && len(relation.Edges[0].Conditions) == 1 {
			outcome = true
		}
		if relation.FromStepID == "decision" && relation.Kind == "external" && relation.Status == "boundary" {
			callback = true
		}
	}
	if !outcome || !callback || len(overview.Boundaries) != 2 {
		t.Fatalf("omitted intermediate decisions or boundaries disappeared: %+v", overview)
	}
}

func TestBuildRequestedFlowOverviewRejectsInvalidProposals(t *testing.T) {
	tests := []struct {
		name   string
		change func(*SemanticMapIR, *[]RequestedFlowSegmentSelection, *[]RequestedFlowRelationSelection)
	}{
		{"source proof", func(m *SemanticMapIR, _ *[]RequestedFlowSegmentSelection, _ *[]RequestedFlowRelationSelection) {
			m.Evidence[0].SourceValidationStatus = "unverified"
		}},
		{"unreachable callback", func(m *SemanticMapIR, _ *[]RequestedFlowSegmentSelection, _ *[]RequestedFlowRelationSelection) {
			m.Edges[2].Kind = "callback_argument"
		}},
		{"duplicate membership", func(_ *SemanticMapIR, s *[]RequestedFlowSegmentSelection, _ *[]RequestedFlowRelationSelection) {
			(*s)[1].StepIDs = []string{"prepare"}
			(*s)[1].Blocks = []RequestedFlowBlockSelection{{StepIDs: []string{"prepare"}}}
		}},
		{"missing block member", func(_ *SemanticMapIR, s *[]RequestedFlowSegmentSelection, _ *[]RequestedFlowRelationSelection) {
			(*s)[0].Blocks[0].StepIDs = []string{"entry"}
		}},
		{"cross file block", func(_ *SemanticMapIR, s *[]RequestedFlowSegmentSelection, _ *[]RequestedFlowRelationSelection) {
			(*s)[0].StepIDs = append((*s)[0].StepIDs, "result")
			(*s)[0].Blocks[0].StepIDs = append((*s)[0].Blocks[0].StepIDs, "result")
		}},
		{"empty purpose", func(_ *SemanticMapIR, s *[]RequestedFlowSegmentSelection, _ *[]RequestedFlowRelationSelection) {
			(*s)[0].Purpose = "  "
		}},
		{"wrong entry", func(_ *SemanticMapIR, s *[]RequestedFlowSegmentSelection, _ *[]RequestedFlowRelationSelection) {
			(*s)[0].StepIDs = []string{"prepare", "entry"}
		}},
		{"wrong relation order", func(_ *SemanticMapIR, _ *[]RequestedFlowSegmentSelection, r *[]RequestedFlowRelationSelection) {
			(*r)[0].EdgeIndexes = []int{2, 1}
		}},
		{"wrong segment endpoint", func(_ *SemanticMapIR, _ *[]RequestedFlowSegmentSelection, r *[]RequestedFlowRelationSelection) {
			(*r)[0].ToSegmentID = "prepare-input"
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			model, selections := requestedFlowOverviewFixture()
			relations := []RequestedFlowRelationSelection{{FromSegmentID: "prepare-input", ToSegmentID: "save-result", FromStepID: "prepare", ToStepID: "result", EdgeIndexes: []int{1, 2}}}
			test.change(model, &selections, &relations)
			if _, err := BuildRequestedFlowOverview(model, BuildFlowSequence(model), "save input", selections, relations); err == nil {
				t.Fatal("invalid proposal accepted")
			}
		})
	}
}

func TestBuildRequestedFlowOverviewRequiresMatchingBasis(t *testing.T) {
	model, selections := requestedFlowOverviewFixture()
	sequence := BuildFlowSequence(model)
	sequence.ComputedBasisID = "other-basis"
	if _, err := BuildRequestedFlowOverview(model, sequence, "save input", selections, nil); err == nil {
		t.Fatal("mismatched basis accepted")
	}
}

func TestBuildRequestedFlowOverviewSupportsSeveralFilesInOnePurpose(t *testing.T) {
	model, selections := requestedFlowOverviewFixture()
	selections[0].StepIDs = append(selections[0].StepIDs, "result")
	selections[0].Blocks = append(selections[0].Blocks, RequestedFlowBlockSelection{StepIDs: []string{"result"}})
	overview, err := BuildRequestedFlowOverview(model, BuildFlowSequence(model), "save input", selections[:1], nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(overview.Segments[0].Blocks) != 2 || len(overview.Segments[0].StepIDs) != 3 {
		t.Fatalf("lost multi-file purpose: %+v", overview)
	}
	found := false
	for _, relation := range overview.Relations {
		if relation.FromStepID == "decision" && relation.ToStepID == "other" && relation.Derived {
			found = true
		}
	}
	if !found {
		t.Fatal("omitted decision did not remain visible without an explicit route proposal")
	}
}

func TestRequestedFlowOverviewKeepsOmittedSuccessorDecisionAndBoundary(t *testing.T) {
	model, selections := requestedFlowOverviewFixture()
	overview, err := BuildRequestedFlowOverview(model, BuildFlowSequence(model), "prepare input", selections[:1], nil)
	if err != nil {
		t.Fatal(err)
	}
	decisionVisible, boundaryVisible := false, false
	for _, relation := range overview.Relations {
		if relation.FromStepID == "decision" && relation.ToStepID == "other" {
			decisionVisible = true
		}
	}
	for _, boundary := range overview.Boundaries {
		if boundary.FromStepID == "decision" {
			boundaryVisible = true
		}
	}
	if !decisionVisible || !boundaryVisible {
		t.Fatalf("omitted descendants hidden: %+v", overview)
	}
}

func TestRequestedFlowOverviewCategoriesPreserveExecutionAndLegacyShape(t *testing.T) {
	model, selections := requestedFlowOverviewFixture()
	legacy, err := BuildRequestedFlowOverview(model, BuildFlowSequence(model), "save input", selections, nil)
	if err != nil {
		t.Fatal(err)
	}
	proposals := []RequestedFlowCategory{{CategoryID: " input ", Title: "Prepare input password=category-private-value", SegmentIDs: []string{"prepare-input"}}, {CategoryID: "result", Title: "Save result", SegmentIDs: []string{"save-result"}}}
	overview, err := BuildRequestedFlowOverview(model, BuildFlowSequence(model), "save input", selections, nil, proposals)
	if err != nil {
		t.Fatal(err)
	}
	if overview.SchemaVersion != 2 || overview.Categories[0].CategoryID != "input" || strings.Contains(overview.Categories[0].Title, "category-private-value") {
		t.Fatalf("category version or redaction incorrect: %+v", overview.Categories)
	}
	if !reflect.DeepEqual(overview.Segments, legacy.Segments) || !reflect.DeepEqual(overview.Relations, legacy.Relations) || !reflect.DeepEqual(overview.Boundaries, legacy.Boundaries) || overview.SnapshotID != legacy.SnapshotID {
		t.Fatal("category navigation changed execution facts")
	}
	// Omitted analyzed decision/outcome steps do not prevent category coverage of
	// the selected overview. This does not claim full analysis completeness.
	if len(model.Steps) <= len(selections[0].StepIDs)+len(selections[1].StepIDs) {
		t.Fatal("fixture should contain unselected analysis steps")
	}
	raw, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if legacy.SchemaVersion != 1 || strings.Contains(string(raw), "categories") {
		t.Fatalf("legacy shape changed: %s", raw)
	}
}

func TestRequestedFlowOverviewRejectsMalformedCategories(t *testing.T) {
	tests := []struct {
		name       string
		categories []RequestedFlowCategory
	}{
		{"empty", []RequestedFlowCategory{}},
		{"empty ID", []RequestedFlowCategory{{Title: "Input", SegmentIDs: []string{"prepare-input", "save-result"}}}},
		{"empty title", []RequestedFlowCategory{{CategoryID: "input", Title: " ", SegmentIDs: []string{"prepare-input", "save-result"}}}},
		{"duplicate ID", []RequestedFlowCategory{{CategoryID: "same", Title: "Input", SegmentIDs: []string{"prepare-input"}}, {CategoryID: "same", Title: "Result", SegmentIDs: []string{"save-result"}}}},
		{"duplicate title", []RequestedFlowCategory{{CategoryID: "input", Title: "Same", SegmentIDs: []string{"prepare-input"}}, {CategoryID: "result", Title: "Same", SegmentIDs: []string{"save-result"}}}},
		{"duplicate redacted titles", []RequestedFlowCategory{{CategoryID: "input", Title: "password=private-one", SegmentIDs: []string{"prepare-input"}}, {CategoryID: "result", Title: "password=private-two", SegmentIDs: []string{"save-result"}}}},
		{"unknown segment", []RequestedFlowCategory{{CategoryID: "input", Title: "Input", SegmentIDs: []string{"prepare-input", "missing"}}}},
		{"incomplete coverage", []RequestedFlowCategory{{CategoryID: "input", Title: "Input", SegmentIDs: []string{"prepare-input"}}}},
		{"repeated member", []RequestedFlowCategory{{CategoryID: "input", Title: "Input", SegmentIDs: []string{"prepare-input", "prepare-input", "save-result"}}}},
		{"overlapping categories", []RequestedFlowCategory{{CategoryID: "input", Title: "Input", SegmentIDs: []string{"prepare-input", "save-result"}}, {CategoryID: "result", Title: "Result", SegmentIDs: []string{"save-result"}}}},
		{"empty members", []RequestedFlowCategory{{CategoryID: "input", Title: "Input", SegmentIDs: []string{}}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			model, selections := requestedFlowOverviewFixture()
			if _, err := BuildRequestedFlowOverview(model, BuildFlowSequence(model), "save input", selections, nil, test.categories); err == nil {
				t.Fatal("malformed categories accepted")
			}
		})
	}
}
