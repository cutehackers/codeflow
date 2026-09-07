package semantic

import (
	"testing"

	"codeflow/internal/slicing"
)

func TestBuildFlowViewProjectionPreservesCriticalKindsAndUnknownBoundaries(t *testing.T) {
	steps := make([]SemanticStep, 0, 20)
	kinds := map[int]string{1: "user_action", 2: "decision", 3: "security", 4: "external_effect", 20: "mutation"}
	for i := 1; i <= 20; i++ {
		kind := kinds[i]
		if kind == "" {
			kind = "call"
		}
		steps = append(steps, SemanticStep{
			StepID: "step-" + string(rune('a'+i)), Ordinal: i, Name: kind,
			TechnicalName: "Service.step", Kind: kind,
			Anchor: slicing.Anchor{RepoRelativePath: "src/service.go", EnclosingSymbolPath: "Service.step"},
		})
	}
	mapIR := &SemanticMapIR{
		MapID: "map-projection", GenerationID: "generation-projection", ComputedBasisID: "basis-projection",
		Task: MapTaskContext{Mode: "feature"}, Steps: steps, BoundaryTargets: []string{"boundary-unknown"},
	}

	projection := BuildFlowViewProjection(mapIR)
	if len(projection.FoldedSubflows) == 0 {
		t.Fatal("expected non-critical steps to be folded")
	}
	if len(projection.UnknownBoundaryRefs) != 1 || projection.UnknownBoundaryRefs[0] != "boundary-unknown" {
		t.Fatalf("unknown boundary was not represented: %+v", projection.UnknownBoundaryRefs)
	}
	visible := make(map[string]bool, len(projection.VisibleStepRefs))
	for _, ref := range projection.VisibleStepRefs {
		visible[ref] = true
	}
	for _, step := range steps {
		if step.Kind == "user_action" || step.Kind == "decision" || step.Kind == "security" || step.Kind == "external_effect" || step.Kind == "mutation" {
			if !visible[step.StepID] {
				t.Errorf("critical %s step %q was folded", step.Kind, step.StepID)
			}
		}
	}
	for _, fold := range projection.FoldedSubflows {
		if fold.HiddenCount < 1 || fold.DrilldownTarget == "" {
			t.Fatalf("fold lacks count/drilldown: %+v", fold)
		}
	}
	if err := ValidateFlowViewProjectionAgainstMap(projection, mapIR); err != nil {
		t.Fatalf("projection/map identity validation failed: %v", err)
	}
}

func TestValidateFlowViewProjectionAgainstMapRejectsUnknownRefs(t *testing.T) {
	mapIR := &SemanticMapIR{GenerationID: "generation-1", ComputedBasisID: "basis-1", Steps: []SemanticStep{{StepID: "step-1"}}}
	projection := &FlowViewProjection{GenerationID: "generation-1", ComputedBasisID: "basis-1", VisibleStepRefs: []string{"step-1"}, UnknownBoundaryRefs: []string{"boundary-not-in-map"}}
	if err := ValidateFlowViewProjectionAgainstMap(projection, mapIR); err == nil {
		t.Fatal("projection accepted an unknown boundary outside the canonical map")
	}
}
