package semantic

import (
	"codeflow/internal/collector/slicing"
	"encoding/json"
	"testing"
)

func TestCompilerSeparatesSourceVerificationFromOpenAnalysis(t *testing.T) {
	payload := &slicing.SlicedPayload{CandidateID: "candidate-source", EntrySymbolPath: "lib/service.dart#Service.step1", Steps: []slicing.SliceStep{makeStep(1, "source", "call")}}
	opts := strictCompileOptions(t, payload, "source-status", 1)
	result := *payload.ValidatedResult
	result.Closure.Status = "open"
	result.Closure.IncompleteReasons = []string{"dynamic target is unresolved"}
	if err := payload.BindValidatedResult(result); err != nil {
		t.Fatal(err)
	}
	model, _, err := CompileDeterministicFeatureMap(&ResolvedTarget{FlowID: "flow-source", EntrySymbolPath: payload.EntrySymbolPath}, nil, payload, opts)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(model)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	proof := decoded["evidence"].([]any)[0].(map[string]any)
	if proof["sourceValidationStatus"] != "verified" || proof["validationStatus"] != "unknown" {
		t.Fatalf("source and analysis statuses conflated: %+v", proof)
	}
	if model.Authority != "candidate" || len(model.Unknowns) == 0 || BuildFlowSequence(model).Frames[0].Status == "verified" {
		t.Fatal("source verification promoted analysis authority")
	}
}
