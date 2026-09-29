package semantic

import (
	"encoding/json"
	"fmt"
	"testing"

	"codeflow/internal/collector/fusion"
	"codeflow/internal/collector/slicing"
)

func TestCompilerPreservesRepeatedInvocationTargets(t *testing.T) {
	for _, wrongTarget := range []bool{false, true} {
		t.Run(fmt.Sprint(wrongTarget), func(t *testing.T) {
			payload := &slicing.SlicedPayload{CandidateID: "cand-invocations", EntrySymbolPath: "lib/service.dart#Service.start"}
			for i := 0; i < 3; i++ {
				callerOrdinal, entryOrdinal := i*4+1, i*4+2
				caller := makeStep(callerOrdinal, "calculate", "call")
				caller.InvocationID, caller.SymbolPath, caller.Anchor.EnclosingSymbolPath = "root", "Service.start", "Service.start"
				entry := makeStep(2, "verify", "call")
				entry.Ordinal, entry.InvocationID, entry.CallerStepOrdinal = entryOrdinal, fmt.Sprintf("invoke-%d", i), &callerOrdinal
				entry.SymbolPath, entry.Anchor.EnclosingSymbolPath = "Service.calculate", "Service.calculate"
				result := makeStep(3, "return value", "return")
				result.Ordinal, result.InvocationID, result.CallerStepOrdinal = entryOrdinal+2, entry.InvocationID, &callerOrdinal
				result.SymbolPath, result.Anchor.EnclosingSymbolPath = entry.SymbolPath, entry.SymbolPath
				second := entry
				second.Ordinal = entryOrdinal + 1
				second.Anchor.ByteRange = [2]int{100, 120}
				payload.Steps = append(payload.Steps, caller, entry, second, result)
				payload.Edges = append(payload.Edges, slicing.SliceEdge{StepOrdinal: &callerOrdinal, TargetStepOrdinal: &entryOrdinal, ToSymbolPath: "lib/service.dart#Service.calculate", Kind: "resolved_cross_file", ResolutionStatus: "resolved"})
			}
			if wrongTarget {
				target := 6
				payload.Edges[0].TargetStepOrdinal = &target
			}
			opts := strictCompileOptions(t, payload, "basis-invocations", 1)
			target := &ResolvedTarget{FlowID: "flow-invocations", CandidateID: payload.CandidateID, EntrySymbolPath: payload.EntrySymbolPath}
			model, _, err := CompileDeterministicFeatureMap(target, nil, payload, opts)
			if wrongTarget {
				if err == nil {
					t.Fatal("compiler accepted a target belonging to another caller")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(model.Edges) != 3 {
				t.Fatalf("edges = %d, want 3", len(model.Edges))
			}
			ids := map[string]bool{}
			for _, step := range model.Steps {
				if ids[step.StepID] {
					t.Fatal("repeated invocation has duplicate step identity")
				}
				ids[step.StepID] = true
			}
			for i, edge := range model.Edges {
				if edge.FromStepID != model.Steps[i*4].StepID || edge.ToStepID != model.Steps[i*4+1].StepID {
					t.Fatalf("wrong invocation edge: %+v", edge)
				}
			}
			sequence := BuildFlowSequence(model)
			if len(sequence.SummaryLimitations) == 0 {
				t.Fatal("control connections claimed successful semantic grouping")
			}
			for _, frame := range sequence.Frames {
				if frame.Role == "result" {
					t.Fatal("nested return presented as entire core flow result")
				}
			}
			spec := &fusion.FlowSpec{FlowID: target.FlowID}
			for i, step := range payload.Steps {
				id := model.Steps[i].StepID
				spec.Steps = append(spec.Steps, fusion.FlowStep{Ordinal: step.Ordinal, StepID: &id, InvocationID: step.InvocationID, CallerStepOrdinal: step.CallerStepOrdinal, Kind: step.Kind, Anchor: step.Anchor})
			}
			for _, edge := range payload.Edges {
				spec.Edges = append(spec.Edges, fusion.FlowEdge{StepOrdinal: edge.StepOrdinal, TargetStepOrdinal: edge.TargetStepOrdinal, ToSymbolPath: edge.ToSymbolPath, Kind: edge.Kind, ResolutionStatus: edge.ResolutionStatus})
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
			for i, edge := range projected.Edges {
				if edge.ToStepID != model.Edges[i].ToStepID {
					t.Fatal("serialized invocation target changed")
				}
			}
			invalid := 6
			restored.Edges[0].TargetStepOrdinal = &invalid
			if edge := ProjectFlowSpec(&restored, "generation").Edges[0]; edge.ResolutionStatus == "resolved" || edge.ToStepID != "" {
				t.Fatal("invalid explicit restore target fell back to symbol lookup")
			}
		})
	}
}

func TestProjectFlowSpecPreservesCallbackArgumentWithoutClaimingExecution(t *testing.T) {
	from, to := 1, 2
	steps := []fusion.FlowStep{
		{Ordinal: 1, InvocationID: "outer", Kind: "mutation", Anchor: slicing.Anchor{RepoRelativePath: "lib/rules.dart", EnclosingSymbolPath: "Rules.run", ByteRange: [2]int{0, 80}}},
		{Ordinal: 2, InvocationID: "callback", Kind: "guard", Anchor: slicing.Anchor{RepoRelativePath: "lib/rules.dart", EnclosingSymbolPath: "Rules.run", ByteRange: [2]int{30, 40}}},
	}
	spec := fusion.FlowSpec{FlowID: "flow-callback", Steps: steps, Edges: []fusion.FlowEdge{{Kind: "callback_argument", StepOrdinal: &from, TargetStepOrdinal: &to, ToSymbolPath: "lib/rules.dart#Rules.run", ResolutionStatus: "resolved"}}}
	encoded, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	var restored fusion.FlowSpec
	if err := json.Unmarshal(encoded, &restored); err != nil {
		t.Fatal(err)
	}
	model := ProjectFlowSpec(&restored, "generation")
	if len(model.Edges) != 1 || model.Edges[0].Kind != "callback_argument" || model.Edges[0].ResolutionStatus != "resolved" || model.Edges[0].ToStepID != model.Steps[1].StepID {
		t.Fatalf("callback source relation changed after restore: %+v", model.Edges)
	}
	sequence := BuildFlowSequence(model)
	if sequence.Frames[1].Status == "verified" {
		t.Fatal("a callback argument falsely proved callback execution")
	}
}

func TestCompilerPreservesUnresolvedCallReasonWhenSymbolRepeats(t *testing.T) {
	callerOrdinal := 1
	caller := makeStep(1, "invoke target", "call")
	caller.InvocationID, caller.SymbolPath, caller.Anchor.EnclosingSymbolPath = "root", "Service.run", "Service.run"
	first, second := makeStep(2, "first target", "return"), makeStep(3, "second target", "return")
	for _, step := range []*slicing.SliceStep{&first, &second} {
		step.SymbolPath, step.Anchor.EnclosingSymbolPath = "Service.target", "Service.target"
		step.CallerStepOrdinal = &callerOrdinal
	}
	first.InvocationID, second.InvocationID = "first", "second"
	payload := &slicing.SlicedPayload{
		CandidateID: "cand-unresolved-repeat", EntrySymbolPath: "lib/service.dart#Service.run",
		Steps: []slicing.SliceStep{caller, first, second},
		Edges: []slicing.SliceEdge{{Kind: "unknown_edge", StepOrdinal: &callerOrdinal, ToSymbolPath: "lib/service.dart#Service.target", ResolutionStatus: "truncated", UnresolvedReason: "재귀 호출의 다음 실행 문장을 펼치지 못했습니다."}},
	}
	target := &ResolvedTarget{FlowID: "flow-unresolved-repeat", CandidateID: payload.CandidateID, EntrySymbolPath: payload.EntrySymbolPath}
	model, _, err := CompileDeterministicFeatureMap(target, nil, payload, strictCompileOptions(t, payload, "basis-unresolved-repeat", 1))
	if err != nil {
		t.Fatal(err)
	}
	if len(model.Edges) != 0 || len(model.Unknowns) == 0 {
		t.Fatalf("unresolved call was treated as a direct relation: edges=%+v unknowns=%+v", model.Edges, model.Unknowns)
	}
	found := false
	for _, unknown := range model.Unknowns {
		if unknown.Subject == "lib/service.dart#Service.target" && unknown.Reason == payload.Edges[0].UnresolvedReason {
			found = true
		}
	}
	if !found {
		t.Fatalf("adapter boundary reason was replaced by symbol ambiguity: %+v", model.Unknowns)
	}
}

func TestInvocationWithoutTargetDoesNotUseSymbolFallback(t *testing.T) {
	callerOrdinal, wrongCaller := 1, 3
	first, child, last := makeStep(1, "first", "call"), makeStep(2, "child", "return"), makeStep(3, "last", "call")
	first.InvocationID, last.InvocationID = "root", "root"
	first.SymbolPath, last.SymbolPath = "Service.start", "Service.start"
	first.Anchor.EnclosingSymbolPath, last.Anchor.EnclosingSymbolPath = first.SymbolPath, last.SymbolPath
	child.InvocationID, child.CallerStepOrdinal = "child", &callerOrdinal
	payload := &slicing.SlicedPayload{CandidateID: "cand-absent", EntrySymbolPath: "lib/service.dart#Service.start", Steps: []slicing.SliceStep{first, child, last}, Edges: []slicing.SliceEdge{{StepOrdinal: &wrongCaller, ToSymbolPath: "lib/service.dart#Service.step2", Kind: "resolved_cross_file", ResolutionStatus: "resolved"}}}
	opts := strictCompileOptions(t, payload, "basis-absent", 1)
	model, _, err := CompileDeterministicFeatureMap(&ResolvedTarget{FlowID: "flow-absent", EntrySymbolPath: payload.EntrySymbolPath}, nil, payload, opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(model.Edges) != 0 {
		t.Fatal("compiler ignored explicit invocation ownership")
	}
	spec := &fusion.FlowSpec{FlowID: "flow-absent", Edges: []fusion.FlowEdge{{StepOrdinal: &wrongCaller, ToSymbolPath: "lib/service.dart#Service.step2", Kind: "resolved_cross_file", ResolutionStatus: "resolved"}}}
	for _, step := range payload.Steps {
		spec.Steps = append(spec.Steps, fusion.FlowStep{Ordinal: step.Ordinal, InvocationID: step.InvocationID, CallerStepOrdinal: step.CallerStepOrdinal, Anchor: step.Anchor})
	}
	for _, edge := range ProjectFlowSpec(spec, "generation").Edges {
		if edge.ResolutionStatus == "resolved" {
			t.Fatal("restoration ignored explicit invocation ownership")
		}
	}
}
