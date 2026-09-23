package flowview

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"codeflow/internal/collector/slicing"
	"codeflow/internal/collector/storage"
	"codeflow/internal/curator/semantic"
)

func TestTaskViewPreservesCuratedHierarchy(t *testing.T) {
	ctx := context.Background()
	store := storage.New(t.TempDir())
	server := &Server{storage: store}
	source := hierarchyAnalysis()
	sequence := source["flowSequence"].(*semantic.FlowSequence)
	expected, err := json.Marshal(sequence)
	if err != nil {
		t.Fatal(err)
	}
	result, err := server.SaveTaskView(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := json.Marshal(result["flowSequence"])
	if err != nil {
		t.Fatal(err)
	}
	var want, got any
	if err := json.Unmarshal(expected, &want); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(actual, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("saving replaced curated hierarchy: want %s, got %s", expected, actual)
	}
	id := result["viewId"].(string)
	before, err := store.ReadView(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := server.RestoreTaskView(ctx, id); err != nil {
		t.Fatal(err)
	}
	after, err := store.ReadView(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("reading rewrote persisted view")
	}
}

func TestTaskViewRejectsInvalidHierarchyBeforeStorage(t *testing.T) {
	cases := []struct {
		name   string
		change func(*semantic.FlowSequence, *semantic.SemanticMapIR)
	}{
		{"promoted evidence", func(s *semantic.FlowSequence, _ *semantic.SemanticMapIR) { s.Frames[0].Status = "verified" }},
		{"fabricated source", func(s *semantic.FlowSequence, _ *semantic.SemanticMapIR) {
			s.Frames[0].SourceAnchor = &slicing.Anchor{RepoRelativePath: "another.ts"}
		}},
		{"reversed frame order", func(s *semantic.FlowSequence, _ *semantic.SemanticMapIR) {
			first := s.Frames[0]
			first.StepRefs = []string{"step-2"}
			first.PrimaryStepRef = "step-2"
			second := first
			second.FrameID = "second"
			second.Ordinal = 2
			second.StepRefs = []string{"step-1"}
			second.PrimaryStepRef = "step-1"
			s.Frames = []semantic.FlowSequenceFrame{first, second}
		}},
		{"missing child", func(s *semantic.FlowSequence, _ *semantic.SemanticMapIR) { s.Frames[0].StepRefs[1] = "missing" }},
		{"duplicate child", func(s *semantic.FlowSequence, _ *semantic.SemanticMapIR) {
			s.Frames[0].StepRefs = append(s.Frames[0].StepRefs, "step-1")
		}},
		{"missing parent", func(s *semantic.FlowSequence, _ *semantic.SemanticMapIR) {
			s.Frames[0].StepRefs = []string{"step-1"}
			s.Frames[0].PrimaryStepRef = "step-1"
		}},
		{"representative outside parent", func(s *semantic.FlowSequence, _ *semantic.SemanticMapIR) { s.Frames[0].PrimaryStepRef = "missing" }},
		{"duplicate parent", func(s *semantic.FlowSequence, _ *semantic.SemanticMapIR) {
			f := s.Frames[0]
			f.FrameID = "another"
			f.Ordinal = 2
			s.Frames = append(s.Frames, f)
		}},
		{"foreign snapshot", func(s *semantic.FlowSequence, _ *semantic.SemanticMapIR) { s.SnapshotID = "other" }},
		{"foreign basis", func(s *semantic.FlowSequence, _ *semantic.SemanticMapIR) { s.ComputedBasisID = "other" }},
		{"foreign generation", func(s *semantic.FlowSequence, _ *semantic.SemanticMapIR) { s.GenerationID = "other" }},
		{"foreign flow", func(s *semantic.FlowSequence, _ *semantic.SemanticMapIR) { s.FlowID = "other" }},
		{"duplicate source step", func(_ *semantic.FlowSequence, m *semantic.SemanticMapIR) { m.Steps[1].StepID = m.Steps[0].StepID }},
		{"reversed child order", func(s *semantic.FlowSequence, _ *semantic.SemanticMapIR) {
			s.Frames[0].StepRefs = []string{"step-2", "step-1"}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := storage.New(t.TempDir())
			server := &Server{storage: store}
			source := hierarchyAnalysis()
			tc.change(source["flowSequence"].(*semantic.FlowSequence), source["semanticMap"].(*semantic.SemanticMapIR))
			if _, err := server.SaveTaskView(context.Background(), source); err == nil {
				t.Fatal("invalid hierarchy accepted")
			}
			views, err := store.ListViews(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if len(views) != 0 {
				t.Fatal("invalid hierarchy persisted")
			}
		})
	}
}

func hierarchyAnalysis() map[string]any {
	m := &semantic.SemanticMapIR{MapID: "map-checkout", GenerationID: "generation-checkout", ComputedBasisID: "basis-checkout", ValidatedAgainstSnapshotID: "snapshot-checkout", Steps: []semantic.SemanticStep{
		{StepID: "step-1", Ordinal: 1, Name: "Validate input", Kind: "guard"},
		{StepID: "step-2", Ordinal: 2, Name: "Validate required fields", Kind: "guard"},
	}}
	s := semantic.BuildFlowSequence(m)
	s.SummaryLimitations = nil
	s.Frames = []semantic.FlowSequenceFrame{{FrameID: "validation", Ordinal: 1, Role: "decision", Title: "Validate checkout input", StepRefs: []string{"step-1", "step-2"}, PrimaryStepRef: "step-2", Status: "partial", FrameMatchKey: "decision|checkout-input"}}
	return map[string]any{"semanticMap": m, "flowSequence": s}
}

func TestTaskViewGeneratesUnresolvedEntryWithoutPromotingBoundary(t *testing.T) {
	source := hierarchyAnalysis()
	m := source["semanticMap"].(*semantic.SemanticMapIR)
	m.Steps[0].EvidenceRefs = []string{"entry-evidence"}
	m.Evidence = []semantic.SemanticEvidence{{EvidenceID: "entry-evidence", ValidationStatus: "verified"}}
	m.BoundaryTargets = []string{"step-1"}
	delete(source, "flowSequence")
	server := &Server{storage: storage.New(t.TempDir())}
	result, err := server.SaveTaskView(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(result["flowSequence"])
	if err != nil {
		t.Fatal(err)
	}
	var sequence semantic.FlowSequence
	if err := json.Unmarshal(data, &sequence); err != nil {
		t.Fatal(err)
	}
	if sequence.Frames[0].Role != "entry" || sequence.Frames[0].Status != "unknown" {
		t.Fatalf("unresolved entry promoted: %+v", sequence.Frames[0])
	}
	if _, exists := source["flowSequence"]; exists {
		t.Fatal("saving modified caller analysis")
	}
}
