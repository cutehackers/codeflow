package slicing_test

import (
	"testing"

	"codeflow/internal/collector/slicing"
)

func TestValidateExecutionReferences(t *testing.T) {
	for _, name := range []string{"valid", "legacy", "duplicate ordinal", "later target", "other invocation", "missing target", "mixed symbol", "missing invocation", "recursive caller"} {
		t.Run(name, func(t *testing.T) {
			firstCaller, secondCaller, target := 1, 4, 2
			steps := []slicing.SliceStep{
				{Ordinal: 1, InvocationID: "root", SymbolPath: "start"},
				{Ordinal: 2, InvocationID: "first", CallerStepOrdinal: &firstCaller, SymbolPath: "calculate", Anchor: slicing.Anchor{RepoRelativePath: "calc.ts"}},
				{Ordinal: 3, InvocationID: "first", CallerStepOrdinal: &firstCaller, SymbolPath: "calculate", Anchor: slicing.Anchor{RepoRelativePath: "calc.ts"}},
				{Ordinal: 4, InvocationID: "root", SymbolPath: "start"},
				{Ordinal: 5, InvocationID: "second", CallerStepOrdinal: &secondCaller, SymbolPath: "calculate", Anchor: slicing.Anchor{RepoRelativePath: "calc.ts"}},
			}
			edges := []slicing.SliceEdge{{StepOrdinal: &firstCaller, TargetStepOrdinal: &target, ToSymbolPath: "calc.ts#calculate", ResolutionStatus: "resolved"}}
			switch name {
			case "legacy":
				for i := range steps {
					steps[i].InvocationID = ""
					steps[i].CallerStepOrdinal = nil
				}
				edges[0].TargetStepOrdinal = nil
			case "duplicate ordinal":
				steps[2].Ordinal = 2
			case "later target":
				target = 3
			case "other invocation":
				target = 5
			case "missing target":
				target = 99
			case "mixed symbol":
				steps[2].SymbolPath = "other"
			case "missing invocation":
				steps[1].InvocationID = ""
			case "recursive caller":
				firstCaller = 3
			}
			err := slicing.ValidateExecutionReferences(steps, edges)
			wantValid := name == "valid" || name == "legacy"
			if (err == nil) != wantValid {
				t.Fatalf("ValidateExecutionReferences() = %v, want valid %v", err, wantValid)
			}
		})
	}
}

func TestValidateControlFlowReferences(t *testing.T) {
	for _, name := range []string{"valid", "different invocation", "completion", "missing target", "wrong symbol"} {
		t.Run(name, func(t *testing.T) {
			from, to := 1, 2
			steps := []slicing.SliceStep{{Ordinal: 1, InvocationID: "root", SymbolPath: "run", Kind: "call", Anchor: slicing.Anchor{RepoRelativePath: "flow.ts"}}, {Ordinal: 2, InvocationID: "root", SymbolPath: "run", Kind: "call", Anchor: slicing.Anchor{RepoRelativePath: "flow.ts"}}}
			edge := slicing.SliceEdge{Kind: "control_flow", StepOrdinal: &from, TargetStepOrdinal: &to, ToSymbolPath: "flow.ts#run", ResolutionStatus: "resolved"}
			switch name {
			case "different invocation":
				steps[1].InvocationID = "other"
			case "completion":
				steps[0].Kind = "return"
			case "missing target":
				edge.TargetStepOrdinal = nil
			case "wrong symbol":
				edge.ToSymbolPath = "flow.ts#other"
			}
			err := slicing.ValidateExecutionReferences(steps, []slicing.SliceEdge{edge})
			if (err == nil) != (name == "valid") {
				t.Fatalf("validation = %v, want valid %v", err, name == "valid")
			}
		})
	}
}

func TestValidateBranchConditions(t *testing.T) {
	for _, name := range []string{"valid", "nullish", "non_nullish", "missing decision", "wrong decision", "not decision", "invalid outcome", "multiple conditions", "call condition"} {
		t.Run(name, func(t *testing.T) {
			from, to := 1, 2
			steps := []slicing.SliceStep{{Ordinal: 1, InvocationID: "root", SymbolPath: "run", Kind: "branch", Anchor: slicing.Anchor{RepoRelativePath: "flow.ts"}}, {Ordinal: 2, InvocationID: "root", SymbolPath: "run", Kind: "call", Anchor: slicing.Anchor{RepoRelativePath: "flow.ts"}}}
			edge := slicing.SliceEdge{Kind: "control_flow", StepOrdinal: &from, TargetStepOrdinal: &to, ToSymbolPath: "flow.ts#run", ResolutionStatus: "resolved", Conditions: []slicing.BranchCondition{{StepOrdinal: 1, Outcome: "truthy"}}}
			switch name {
			case "nullish", "non_nullish":
				edge.Conditions[0].Outcome = name
			case "missing decision":
				edge.Conditions[0].StepOrdinal = 999
			case "wrong decision":
				edge.Conditions[0].StepOrdinal = 2
			case "not decision":
				steps[0].Kind = "call"
			case "invalid outcome":
				edge.Conditions[0].Outcome = "success"
			case "multiple conditions":
				edge.Conditions = append(edge.Conditions, edge.Conditions[0])
			case "call condition":
				edge.Kind = "resolved_cross_file"
			}
			err := slicing.ValidateExecutionReferences(steps, []slicing.SliceEdge{edge})
			if (err == nil) != (name == "valid" || name == "nullish" || name == "non_nullish") {
				t.Fatalf("validation = %v", err)
			}
		})
	}
}

func TestNormalReturnRequiresCallAndContinuation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func([]slicing.SliceStep, []slicing.SliceEdge)
		valid  bool
	}{
		{"valid", nil, true},
		{"wrong caller", func(s []slicing.SliceStep, e []slicing.SliceEdge) { n := 3; s[1].CallerStepOrdinal = &n }, false},
		{"throw is not return", func(s []slicing.SliceStep, e []slicing.SliceEdge) { s[1].Kind = "throw" }, false},
		{"foreign continuation", func(s []slicing.SliceStep, e []slicing.SliceEdge) { s[2].InvocationID = "foreign" }, false},
		{"call not resolved", func(s []slicing.SliceStep, e []slicing.SliceEdge) { e[0].ResolutionStatus = "unresolved_type" }, false},
		{"missing continuation", func(s []slicing.SliceStep, e []slicing.SliceEdge) {
			e[1].Kind = "unknown_edge"
			e[1].TargetStepOrdinal = nil
		}, false},
		{"wrong symbol", func(s []slicing.SliceStep, e []slicing.SliceEdge) { e[2].ToSymbolPath = "flow.ts#other" }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			caller, returned, next := 1, 2, 3
			root := slicing.Anchor{RepoRelativePath: "flow.ts", EnclosingSymbolPath: "start"}
			child := slicing.Anchor{RepoRelativePath: "flow.ts", EnclosingSymbolPath: "price"}
			steps := []slicing.SliceStep{{Ordinal: 1, Kind: "call", InvocationID: "root", SymbolPath: "start", Anchor: root}, {Ordinal: 2, Kind: "return", InvocationID: "price-call", CallerStepOrdinal: &caller, SymbolPath: "price", Anchor: child}, {Ordinal: 3, Kind: "mutation", InvocationID: "root", SymbolPath: "start", Anchor: root}}
			edges := []slicing.SliceEdge{{Kind: "resolved_cross_file", StepOrdinal: &caller, TargetStepOrdinal: &returned, ToSymbolPath: "flow.ts#price", ResolutionStatus: "resolved"}, {Kind: "control_flow", StepOrdinal: &caller, TargetStepOrdinal: &next, ToSymbolPath: "flow.ts#start", ResolutionStatus: "resolved"}, {Kind: "return", StepOrdinal: &returned, TargetStepOrdinal: &next, ToSymbolPath: "flow.ts#start", ResolutionStatus: "resolved"}}
			if tc.change != nil {
				tc.change(steps, edges)
			}
			err := slicing.ValidateExecutionReferences(steps, edges)
			if (err == nil) != tc.valid {
				t.Fatalf("validation=%v want valid=%v", err, tc.valid)
			}
		})
	}
}

func TestAwaitResumptionRequiresMatchingAwaitSource(t *testing.T) {
	for _, tc := range []struct {
		sourceKind, edgeKind string
		valid                bool
	}{
		{"await", "await_resume", true}, {"call", "await_resume", false}, {"await", "control_flow", false},
	} {
		t.Run(tc.sourceKind+"-"+tc.edgeKind, func(t *testing.T) {
			from, to := 1, 2
			anchor := slicing.Anchor{RepoRelativePath: "flow.ts"}
			steps := []slicing.SliceStep{{Ordinal: 1, Kind: tc.sourceKind, InvocationID: "root", SymbolPath: "start", Anchor: anchor}, {Ordinal: 2, Kind: "mutation", InvocationID: "root", SymbolPath: "start", Anchor: anchor}}
			edges := []slicing.SliceEdge{{Kind: tc.edgeKind, StepOrdinal: &from, TargetStepOrdinal: &to, ToSymbolPath: "flow.ts#start", ResolutionStatus: "resolved"}}
			if err := slicing.ValidateExecutionReferences(steps, edges); (err == nil) != tc.valid {
				t.Fatalf("validation=%v want valid=%v", err, tc.valid)
			}
		})
	}
}

func TestParallelWaitRequiresCallAndAwait(t *testing.T) {
	for _, tc := range []struct {
		name                   string
		sourceKind, targetKind string
		from, to               int
		valid                  bool
	}{
		{name: "valid", sourceKind: "call", targetKind: "await", from: 1, to: 2, valid: true},
		{name: "ordinary source", sourceKind: "mutation", targetKind: "await", from: 1, to: 2},
		{name: "ordinary target", sourceKind: "call", targetKind: "mutation", from: 1, to: 2},
		{name: "backward target", sourceKind: "call", targetKind: "await", from: 2, to: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			anchor := slicing.Anchor{RepoRelativePath: "flow.ts"}
			steps := []slicing.SliceStep{{Ordinal: 1, Kind: tc.sourceKind, InvocationID: "root", SymbolPath: "run", Anchor: anchor}, {Ordinal: 2, Kind: tc.targetKind, InvocationID: "root", SymbolPath: "run", Anchor: anchor}}
			edge := slicing.SliceEdge{Kind: "parallel_wait", StepOrdinal: &tc.from, TargetStepOrdinal: &tc.to, ToSymbolPath: "flow.ts#run", ResolutionStatus: "resolved"}
			if err := slicing.ValidateExecutionReferences(steps, []slicing.SliceEdge{edge}); (err == nil) != tc.valid {
				t.Fatalf("validation=%v want valid=%v", err, tc.valid)
			}
		})
	}
}

func TestAwaitLoopBackRequiresAwaitAndEarlierTarget(t *testing.T) {
	for _, tc := range []struct {
		name       string
		sourceKind string
		from, to   int
		valid      bool
	}{
		{name: "valid", sourceKind: "await", from: 2, to: 1, valid: true},
		{name: "ordinary source", sourceKind: "call", from: 2, to: 1},
		{name: "forward target", sourceKind: "await", from: 1, to: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			anchor := slicing.Anchor{RepoRelativePath: "flow.ts"}
			steps := []slicing.SliceStep{{Ordinal: 1, Kind: "branch", InvocationID: "root", SymbolPath: "retry", Anchor: anchor}, {Ordinal: 2, Kind: "call", InvocationID: "root", SymbolPath: "retry", Anchor: anchor}}
			steps[tc.from-1].Kind = tc.sourceKind
			edge := slicing.SliceEdge{Kind: "await_loop_back", StepOrdinal: &tc.from, TargetStepOrdinal: &tc.to, ToSymbolPath: "flow.ts#retry", ResolutionStatus: "resolved"}
			if err := slicing.ValidateExecutionReferences(steps, []slicing.SliceEdge{edge}); (err == nil) != tc.valid {
				t.Fatalf("validation=%v want valid=%v", err, tc.valid)
			}
		})
	}
}

func TestLoopReentryRequiresTrueBranchAndEarlierBody(t *testing.T) {
	for _, tc := range []struct {
		name       string
		sourceKind string
		from, to   int
		outcome    string
		valid      bool
	}{
		{name: "valid", sourceKind: "branch", from: 2, to: 1, outcome: "truthy", valid: true},
		{name: "ordinary source", sourceKind: "mutation", from: 2, to: 1, outcome: "truthy"},
		{name: "forward target", sourceKind: "branch", from: 1, to: 2, outcome: "truthy"},
		{name: "false condition", sourceKind: "branch", from: 2, to: 1, outcome: "falsy"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			anchor := slicing.Anchor{RepoRelativePath: "flow.ts"}
			steps := []slicing.SliceStep{{Ordinal: 1, Kind: "call", InvocationID: "root", SymbolPath: "retry", Anchor: anchor}, {Ordinal: 2, Kind: tc.sourceKind, InvocationID: "root", SymbolPath: "retry", Anchor: anchor}}
			edge := slicing.SliceEdge{Kind: "loop_reentry", StepOrdinal: &tc.from, TargetStepOrdinal: &tc.to, ToSymbolPath: "flow.ts#retry", ResolutionStatus: "resolved", Conditions: []slicing.BranchCondition{{StepOrdinal: tc.from, Outcome: tc.outcome}}}
			if err := slicing.ValidateExecutionReferences(steps, []slicing.SliceEdge{edge}); (err == nil) != tc.valid {
				t.Fatalf("validation=%v want valid=%v", err, tc.valid)
			}
		})
	}
}

func TestFinallyRelationRequiresCompletionSource(t *testing.T) {
	for _, tc := range []struct {
		sourceKind string
		valid      bool
	}{
		{sourceKind: "return", valid: true},
		{sourceKind: "throw", valid: true},
		{sourceKind: "call", valid: false},
	} {
		t.Run(tc.sourceKind, func(t *testing.T) {
			from, to := 1, 2
			anchor := slicing.Anchor{RepoRelativePath: "flow.ts"}
			steps := []slicing.SliceStep{{Ordinal: from, Kind: tc.sourceKind, InvocationID: "root", SymbolPath: "finish", Anchor: anchor}, {Ordinal: to, Kind: "call", InvocationID: "root", SymbolPath: "finish", Anchor: anchor}}
			edge := slicing.SliceEdge{Kind: "finally", StepOrdinal: &from, TargetStepOrdinal: &to, ToSymbolPath: "flow.ts#finish", ResolutionStatus: "resolved"}
			if err := slicing.ValidateExecutionReferences(steps, []slicing.SliceEdge{edge}); (err == nil) != tc.valid {
				t.Fatalf("validation=%v want valid=%v", err, tc.valid)
			}
		})
	}
}

func TestLoopBackRequiresSameInvocationAndEarlierTarget(t *testing.T) {
	for _, name := range []string{"valid", "forward", "foreign", "return"} {
		t.Run(name, func(t *testing.T) {
			from, to := 2, 1
			anchor := slicing.Anchor{RepoRelativePath: "flow.ts"}
			steps := []slicing.SliceStep{{Ordinal: 1, Kind: "branch", InvocationID: "root", SymbolPath: "run", Anchor: anchor}, {Ordinal: 2, Kind: "mutation", InvocationID: "root", SymbolPath: "run", Anchor: anchor}}
			switch name {
			case "forward":
				from, to = 1, 2
			case "foreign":
				steps[1].InvocationID = "other"
			case "return":
				steps[1].Kind = "return"
			}
			edge := slicing.SliceEdge{Kind: "loop_back", StepOrdinal: &from, TargetStepOrdinal: &to, ToSymbolPath: "flow.ts#run", ResolutionStatus: "resolved"}
			if err := slicing.ValidateExecutionReferences(steps, []slicing.SliceEdge{edge}); (err == nil) != (name == "valid") {
				t.Fatalf("validation=%v", err)
			}
		})
	}
}

func TestLoopControlTransfersRejectOrdinaryContinuation(t *testing.T) {
	for _, tc := range []struct {
		source, relation string
		from, to         int
		valid            bool
	}{
		{"break", "loop_exit", 1, 2, true}, {"break", "control_flow", 1, 2, false}, {"mutation", "loop_exit", 1, 2, false},
		{"continue", "loop_back", 2, 1, true}, {"continue", "control_flow", 1, 2, false}, {"break", "loop_back", 2, 1, false},
		{"break", "loop_exit_back", 2, 1, true}, {"mutation", "loop_exit_back", 2, 1, false}, {"break", "loop_exit_back", 2, 2, false},
	} {
		t.Run(tc.source+tc.relation, func(t *testing.T) {
			anchor := slicing.Anchor{RepoRelativePath: "flow.ts"}
			steps := []slicing.SliceStep{{Ordinal: 1, Kind: "branch", InvocationID: "root", SymbolPath: "run", Anchor: anchor}, {Ordinal: 2, Kind: "mutation", InvocationID: "root", SymbolPath: "run", Anchor: anchor}}
			steps[tc.from-1].Kind = tc.source
			edge := slicing.SliceEdge{Kind: tc.relation, StepOrdinal: &tc.from, TargetStepOrdinal: &tc.to, ToSymbolPath: "flow.ts#run", ResolutionStatus: "resolved"}
			if err := slicing.ValidateExecutionReferences(steps, []slicing.SliceEdge{edge}); (err == nil) != tc.valid {
				t.Fatalf("validation=%v", err)
			}
		})
	}
}

func TestSwitchExitRequiresForwardBreak(t *testing.T) {
	for _, tc := range []struct {
		name       string
		sourceKind string
		from, to   int
		valid      bool
	}{
		{name: "valid", sourceKind: "break", from: 1, to: 2, valid: true},
		{name: "ordinary step", sourceKind: "mutation", from: 1, to: 2},
		{name: "backward target", sourceKind: "break", from: 2, to: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			anchor := slicing.Anchor{RepoRelativePath: "flow.ts"}
			steps := []slicing.SliceStep{{Ordinal: 1, Kind: "branch", InvocationID: "root", SymbolPath: "run", Anchor: anchor}, {Ordinal: 2, Kind: "call", InvocationID: "root", SymbolPath: "run", Anchor: anchor}}
			steps[tc.from-1].Kind = tc.sourceKind
			edge := slicing.SliceEdge{Kind: "switch_exit", StepOrdinal: &tc.from, TargetStepOrdinal: &tc.to, ToSymbolPath: "flow.ts#run", ResolutionStatus: "resolved"}
			if err := slicing.ValidateExecutionReferences(steps, []slicing.SliceEdge{edge}); (err == nil) != tc.valid {
				t.Fatalf("validation=%v want valid=%v", err, tc.valid)
			}
		})
	}
}

func TestFailureRecoveryRequiresAwaitOrThrowSource(t *testing.T) {
	for _, tc := range []struct {
		sourceKind string
		valid      bool
	}{
		{sourceKind: "await", valid: true},
		{sourceKind: "throw", valid: true},
		{sourceKind: "call", valid: false},
	} {
		t.Run(tc.sourceKind, func(t *testing.T) {
			from, to := 1, 2
			anchor := slicing.Anchor{RepoRelativePath: "flow.ts"}
			steps := []slicing.SliceStep{{Ordinal: from, Kind: tc.sourceKind, InvocationID: "root", SymbolPath: "recover", Anchor: anchor}, {Ordinal: to, Kind: "call", InvocationID: "root", SymbolPath: "recover", Anchor: anchor}}
			edge := slicing.SliceEdge{Kind: "failure", StepOrdinal: &from, TargetStepOrdinal: &to, ToSymbolPath: "flow.ts#recover", ResolutionStatus: "resolved"}
			if err := slicing.ValidateExecutionReferences(steps, []slicing.SliceEdge{edge}); (err == nil) != tc.valid {
				t.Fatalf("validation=%v want valid=%v", err, tc.valid)
			}
		})
	}
}

func TestCalledThrowFailureRequiresMatchingCallAndRecovery(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func([]slicing.SliceStep, []slicing.SliceEdge)
		valid  bool
	}{
		{name: "valid", valid: true},
		{name: "ordinary source", change: func(steps []slicing.SliceStep, _ []slicing.SliceEdge) { steps[1].Kind = "call" }},
		{name: "foreign recovery", change: func(steps []slicing.SliceStep, _ []slicing.SliceEdge) { steps[2].InvocationID = "foreign" }},
		{name: "missing resolved call", change: func(_ []slicing.SliceStep, edges []slicing.SliceEdge) { edges[0].ResolutionStatus = "unresolved_type" }},
		{name: "wrong recovery symbol", change: func(_ []slicing.SliceStep, edges []slicing.SliceEdge) { edges[1].ToSymbolPath = "flow.ts#other" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			caller, thrown, recovery := 1, 2, 3
			root := slicing.Anchor{RepoRelativePath: "flow.ts", EnclosingSymbolPath: "checkout"}
			child := slicing.Anchor{RepoRelativePath: "flow.ts", EnclosingSymbolPath: "charge"}
			steps := []slicing.SliceStep{
				{Ordinal: caller, Kind: "call", InvocationID: "root", SymbolPath: "checkout", Anchor: root},
				{Ordinal: thrown, Kind: "throw", InvocationID: "charge-call", CallerStepOrdinal: &caller, SymbolPath: "charge", Anchor: child},
				{Ordinal: recovery, Kind: "call", InvocationID: "root", SymbolPath: "checkout", Anchor: root},
			}
			edges := []slicing.SliceEdge{
				{Kind: "resolved_cross_file", StepOrdinal: &caller, TargetStepOrdinal: &thrown, ToSymbolPath: "flow.ts#charge", ResolutionStatus: "resolved"},
				{Kind: "failure", StepOrdinal: &thrown, TargetStepOrdinal: &recovery, ToSymbolPath: "flow.ts#checkout", ResolutionStatus: "resolved"},
			}
			if tc.change != nil {
				tc.change(steps, edges)
			}
			if err := slicing.ValidateExecutionReferences(steps, edges); (err == nil) != tc.valid {
				t.Fatalf("validation=%v want valid=%v", err, tc.valid)
			}
		})
	}
}
