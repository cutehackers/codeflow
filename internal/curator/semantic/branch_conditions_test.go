package semantic

import (
	"encoding/json"
	"reflect"
	"testing"

	"codeflow/internal/collector/fusion"
	"codeflow/internal/collector/slicing"
)

func TestBranchConditionsSurviveCompilationAndRestoration(t *testing.T) {
	for _, outcomes := range [][]string{{"truthy", "falsy"}, {"nullish", "non_nullish"}} {
		t.Run(outcomes[0], func(t *testing.T) {
			from, to := 1, 2
			decision := makeStep(1, "if (ready)", "branch")
			next := makeStep(2, "continueWork", "call")
			for _, step := range []*slicing.SliceStep{&decision, &next} {
				step.InvocationID = "root"
				step.SymbolPath = "Service.start"
				step.Anchor.EnclosingSymbolPath = "Service.start"
			}
			payload := &slicing.SlicedPayload{CandidateID: "cand-branch", EntrySymbolPath: "lib/service.dart#Service.start", Steps: []slicing.SliceStep{decision, next}}
			for _, outcome := range outcomes {
				payload.Edges = append(payload.Edges, slicing.SliceEdge{Kind: "control_flow", StepOrdinal: &from, TargetStepOrdinal: &to, ToSymbolPath: "lib/service.dart#Service.start", ResolutionStatus: "resolved", Conditions: []slicing.BranchCondition{{StepOrdinal: from, Outcome: outcome}}})
			}
			opts := strictCompileOptions(t, payload, "basis-branch", 1)
			target := &ResolvedTarget{FlowID: "flow-branch", CandidateID: payload.CandidateID, EntrySymbolPath: payload.EntrySymbolPath}
			model, _, err := CompileDeterministicFeatureMap(target, nil, payload, opts)
			if err != nil {
				t.Fatal(err)
			}
			if len(model.Edges) != 2 {
				t.Fatalf("conditional alternatives collapsed: %+v", model.Edges)
			}
			spec := &fusion.FlowSpec{FlowID: target.FlowID}
			for i, step := range payload.Steps {
				id := model.Steps[i].StepID
				spec.Steps = append(spec.Steps, fusion.FlowStep{Ordinal: step.Ordinal, StepID: &id, InvocationID: step.InvocationID, Kind: step.Kind, Anchor: step.Anchor})
			}
			for _, edge := range payload.Edges {
				spec.Edges = append(spec.Edges, fusion.FlowEdge{Kind: edge.Kind, StepOrdinal: edge.StepOrdinal, TargetStepOrdinal: edge.TargetStepOrdinal, ToSymbolPath: edge.ToSymbolPath, ResolutionStatus: edge.ResolutionStatus, Conditions: edge.Conditions})
			}
			data, err := json.Marshal(spec)
			if err != nil {
				t.Fatal(err)
			}
			var restored fusion.FlowSpec
			if err := json.Unmarshal(data, &restored); err != nil {
				t.Fatal(err)
			}
			projected := ProjectFlowSpec(&restored, "generation")
			if !reflect.DeepEqual(model.Edges, projected.Edges) {
				t.Fatalf("conditions changed on restore: %+v != %+v", model.Edges, projected.Edges)
			}
			for i, outcome := range outcomes {
				if !reflect.DeepEqual(model.Edges[i].Conditions, []SemanticBranchCondition{{StepID: model.Steps[0].StepID, Outcome: outcome}}) {
					t.Fatalf("wrong condition: %+v", model.Edges[i])
				}
			}
			if err := ValidateFlowSequenceReferences(model, BuildFlowSequence(model)); err != nil {
				t.Fatal(err)
			}
			restored.Edges[0].Conditions[0].StepOrdinal = 999
			invalid := ProjectFlowSpec(&restored, "generation")
			if invalid.Edges[0].ResolutionStatus == "resolved" {
				t.Fatal("invalid condition restored as unconditional progress")
			}
			model.Edges[0].Conditions[0].StepID = "missing"
			if err := ValidateFlowSequenceReferences(model, BuildFlowSequence(model)); err == nil {
				t.Fatal("invalid condition accepted for storage")
			}
		})
	}
}
