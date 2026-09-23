package semantic

import (
	"codeflow/internal/collector/contractharness"
	"codeflow/internal/collector/slicing"
	"encoding/json"
	"testing"
)

func TestCompilerSharesSourceEvidenceAcrossExecutionSteps(t *testing.T) {
	for _, mode := range []string{"same expression", "separate invocation"} {
		t.Run(mode, func(t *testing.T) {
			first := makeStep(1, "expression", "call")
			first.InvocationID = "first"
			second := first
			second.Ordinal = 2
			if mode == "same expression" {
				second.Kind = "return"
			} else {
				second.InvocationID = "second"
			}
			payload := &slicing.SlicedPayload{CandidateID: "cand-shared-source", EntrySymbolPath: "lib/service.dart#Service.step1", Steps: []slicing.SliceStep{first, second}}
			opts := strictCompileOptions(t, payload, "basis-shared-source", 1)
			target := &ResolvedTarget{FlowID: "flow-shared-source", CandidateID: payload.CandidateID, EntrySymbolPath: payload.EntrySymbolPath}
			model, _, err := CompileDeterministicFeatureMap(target, nil, payload, opts)
			if err != nil {
				t.Fatal(err)
			}
			if len(model.Steps) != 2 || model.Steps[0].StepID == model.Steps[1].StepID {
				t.Fatal("execution identities collapsed")
			}
			if len(model.Evidence) != 1 {
				t.Fatalf("source evidence duplicated: %d", len(model.Evidence))
			}
			if model.Steps[0].EvidenceRefs[0] != model.Steps[1].EvidenceRefs[0] {
				t.Fatal("same source evidence not shared")
			}
			data, err := json.Marshal(model)
			if err != nil {
				t.Fatal(err)
			}
			if err := contractharness.ValidateSemanticMapIR(data); err != nil {
				t.Fatal(err)
			}
		})
	}
}
