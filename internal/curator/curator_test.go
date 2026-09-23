package curator_test

import (
	"fmt"
	"testing"

	"codeflow/internal/collector/fusion"
	"codeflow/internal/collector/slicing"
	"codeflow/internal/curator"
)

func strPtr(s string) *string {
	return &s
}

// Independent steps remain independent when no execution or purpose evidence exists.
func TestCurator_LargeTracePreservesIndependentGateways(t *testing.T) {
	c := curator.NewCurator()

	stepCount := 160
	steps := make([]fusion.FlowStep, stepCount)
	for i := 0; i < stepCount; i++ {
		kind := "process"
		if i == 0 {
			kind = "entry"
		} else if i == stepCount-1 {
			kind = "result"
		} else if i%10 == 0 {
			kind = "guard"
		} else if i%15 == 0 {
			kind = "effect"
		} else if i%7 == 0 {
			kind = "mutation"
		}

		steps[i] = fusion.FlowStep{
			StepID: strPtr(fmt.Sprintf("step-%03d", i+1)),
			Name:   fmt.Sprintf("Step %d", i+1),
			Kind:   kind,
			Anchor: slicing.Anchor{
				RepoRelativePath:    fmt.Sprintf("pkg/file%d.go", (i/5)+1),
				EnclosingSymbolPath: fmt.Sprintf("pkg/file%d.go#Func%d", (i/5)+1, i%5),
			},
		}
	}

	trace := curator.RawExecutionTrace{
		FlowID: "flow-large-150",
		Steps:  steps,
	}

	frames := c.CurateFlowSequence(trace)

	if len(frames) != stepCount {
		t.Fatalf("expected independent gateways for %d unrelated steps, got %d", stepCount, len(frames))
	}

	// Verify all 160 steps are accounted for in stepRefs across the frames
	totalRefs := 0
	for _, f := range frames {
		totalRefs += len(f.StepRefs)
	}
	if totalRefs != stepCount {
		t.Fatalf("expected all %d steps preserved in stepRefs, got %d", stepCount, totalRefs)
	}

	// First frame must be entry, last frame must be result
	if frames[0].Role != "entry" {
		t.Errorf("expected first frame to be entry, got %s", frames[0].Role)
	}
	if frames[len(frames)-1].Role != "result" {
		t.Errorf("expected last frame to be result, got %s", frames[len(frames)-1].Role)
	}
}

// 2. Under 3 Steps Floor Test (No dummy frames, honest 1~2 frames)
func TestCurator_SmallTraceFloorBound(t *testing.T) {
	c := curator.NewCurator()

	// 1-step getter
	trace1 := curator.RawExecutionTrace{
		FlowID: "flow-small-1",
		Steps: []fusion.FlowStep{
			{
				StepID: strPtr("step-1"),
				Name:   "Get User Name",
				Kind:   "entry",
				Anchor: slicing.Anchor{EnclosingSymbolPath: "user.go#GetName"},
			},
		},
	}
	frames1 := c.CurateFlowSequence(trace1)
	if len(frames1) != 1 {
		t.Fatalf("expected exactly 1 frame for 1 step, got %d", len(frames1))
	}
	if frames1[0].Role != "entry" || frames1[0].Title != "Get User Name" {
		t.Errorf("unexpected frame content: %+v", frames1[0])
	}

	// 2-step passthrough
	trace2 := curator.RawExecutionTrace{
		FlowID: "flow-small-2",
		Steps: []fusion.FlowStep{
			{
				StepID: strPtr("step-1"),
				Name:   "Handle Request",
				Kind:   "entry",
				Anchor: slicing.Anchor{EnclosingSymbolPath: "handler.go#Handle"},
			},
			{
				StepID: strPtr("step-2"),
				Name:   "Return OK",
				Kind:   "result",
				Anchor: slicing.Anchor{EnclosingSymbolPath: "handler.go#ReturnOK"},
			},
		},
	}
	frames2 := c.CurateFlowSequence(trace2)
	if len(frames2) != 2 {
		t.Fatalf("expected exactly 2 frames for 2 steps, got %d", len(frames2))
	}
	if frames2[0].Role != "entry" || frames2[1].Role != "result" {
		t.Errorf("expected entry and result frames, got %s and %s", frames2[0].Role, frames2[1].Role)
	}
}

// 3. 0-Branch Linear Trace Test (Sequential processing, no dummy decision frame)
func TestCurator_ZeroBranchLinearTrace(t *testing.T) {
	c := curator.NewCurator()

	trace := curator.RawExecutionTrace{
		FlowID: "flow-linear",
		Steps: []fusion.FlowStep{
			{
				StepID: strPtr("step-1"),
				Name:   "Trigger Job",
				Kind:   "entry",
				Anchor: slicing.Anchor{EnclosingSymbolPath: "job.go#Trigger"},
			},
			{
				StepID: strPtr("step-2"),
				Name:   "Update Job Status",
				Kind:   "mutation",
				Anchor: slicing.Anchor{EnclosingSymbolPath: "job.go#UpdateStatus"},
			},
			{
				StepID: strPtr("step-3"),
				Name:   "Send Webhook",
				Kind:   "external_effect",
				Anchor: slicing.Anchor{EnclosingSymbolPath: "webhook.go#Send"},
			},
			{
				StepID: strPtr("step-4"),
				Name:   "Complete Job",
				Kind:   "result",
				Anchor: slicing.Anchor{EnclosingSymbolPath: "job.go#Complete"},
			},
		},
	}

	frames := c.CurateFlowSequence(trace)
	for _, f := range frames {
		if f.Role == "decision" {
			t.Errorf("unexpected decision frame in 0-branch linear flow: %+v", f)
		}
	}
}

// Repeated symbol names do not prove a recursive execution path.
func TestCurator_RepeatedSymbolDoesNotProveRecursion(t *testing.T) {
	c := curator.NewCurator()

	trace := curator.RawExecutionTrace{
		FlowID: "flow-recursion",
		Steps: []fusion.FlowStep{
			{
				StepID: strPtr("step-entry"),
				Name:   "Start Order",
				Kind:   "entry",
				Anchor: slicing.Anchor{EnclosingSymbolPath: "order.go#Start"},
			},
			{
				StepID: strPtr("step-a"),
				Name:   "ProcessOrder",
				Kind:   "process",
				Anchor: slicing.Anchor{EnclosingSymbolPath: "order.go#ProcessOrder"},
			},
			{
				StepID: strPtr("step-b"),
				Name:   "VerifyInventory",
				Kind:   "process",
				Anchor: slicing.Anchor{EnclosingSymbolPath: "inventory.go#Verify"},
			},
			{
				StepID: strPtr("step-c"),
				Name:   "RetryPayment",
				Kind:   "process",
				Anchor: slicing.Anchor{EnclosingSymbolPath: "pay.go#RetryPayment"},
			},
			{
				StepID: strPtr("step-a2"),
				Name:   "ProcessOrder",
				Kind:   "process",
				Anchor: slicing.Anchor{EnclosingSymbolPath: "order.go#ProcessOrder"},
			},
			{
				StepID: strPtr("step-final"),
				Name:   "Finalize",
				Kind:   "result",
				Anchor: slicing.Anchor{EnclosingSymbolPath: "order.go#Finalize"},
			},
		},
	}

	frames := c.CurateFlowSequence(trace)

	if len(frames) != len(trace.Steps) {
		t.Fatalf("repeated symbols collapsed without execution evidence: %+v", frames)
	}
	for _, frame := range frames {
		if frame.IsRecursion {
			t.Fatalf("symbol repetition invented recursion: %+v", frame)
		}
	}

}

// 5. Empty Trace Floor Test
func TestCurator_EmptyTrace(t *testing.T) {
	c := curator.NewCurator()
	frames := c.CurateFlowSequence(curator.RawExecutionTrace{})
	if len(frames) != 0 {
		t.Errorf("expected no virtual frame for an empty trace, got %+v", frames)
	}
}

// 5b. Boundary and Unknown Step Status Test (Never false verified)
func TestCurator_BoundaryAndUnknownStatus(t *testing.T) {
	c := curator.NewCurator()

	trace := curator.RawExecutionTrace{
		FlowID: "flow-boundary-test",
		Steps: []fusion.FlowStep{
			{
				StepID: strPtr("step-entry"),
				Name:   "Start",
				Kind:   "entry",
				Anchor: slicing.Anchor{RepoRelativePath: "main.go", EnclosingSymbolPath: "main.go#Start"},
			},
			{
				StepID: strPtr("step-boundary"),
				Name:   "Unresolved External Call",
				Kind:   "boundary",
				Rules:  []string{"boundary:external/service"},
				Anchor: slicing.Anchor{RepoRelativePath: "main.go", EnclosingSymbolPath: "main.go#Start"},
			},
			{
				StepID:     strPtr("step-orphaned"),
				Name:       "Deleted Method Call",
				Kind:       "process",
				Freshness:  "orphaned",
				Confidence: 0.0,
				Anchor:     slicing.Anchor{RepoRelativePath: "deleted.go", EnclosingSymbolPath: "deleted.go#Do"},
			},
			{
				StepID: strPtr("step-result"),
				Name:   "Done",
				Kind:   "result",
				Anchor: slicing.Anchor{RepoRelativePath: "main.go", EnclosingSymbolPath: "main.go#Done"},
			},
		},
	}

	frames := c.CurateFlowSequence(trace)
	for _, f := range frames {
		if f.Role == "boundary" && f.Status == "verified" {
			t.Errorf("boundary frame must not have verified status: %+v", f)
		}
		if f.Title == "Deleted Method Call" && f.Status == "verified" {
			t.Errorf("orphaned step frame must not have verified status: %+v", f)
		}
	}
}

// Adjacent guards remain separate without shared-purpose and execution evidence.
func TestCurator_ConsecutiveGuardsInSameFunctionNotRecursion(t *testing.T) {
	c := curator.NewCurator()

	branch1 := "req == nil"
	branch2 := "req.User == nil"
	trace := curator.RawExecutionTrace{
		FlowID: "flow-guards",
		Steps: []fusion.FlowStep{
			{
				StepID: strPtr("step-entry"),
				Name:   "dispatchRequest",
				Kind:   "entry",
				Anchor: slicing.Anchor{EnclosingSymbolPath: "handler.go#dispatchRequest"},
			},
			{
				StepID: strPtr("step-guard1"),
				Name:   "CheckNilRequest",
				Kind:   "guard",
				Branch: &branch1,
				Anchor: slicing.Anchor{EnclosingSymbolPath: "handler.go#dispatchRequest"},
			},
			{
				StepID: strPtr("step-guard2"),
				Name:   "CheckNilUser",
				Kind:   "guard",
				Branch: &branch2,
				Anchor: slicing.Anchor{EnclosingSymbolPath: "handler.go#dispatchRequest"},
			},
			{
				StepID: strPtr("step-result"),
				Name:   "ReturnSuccess",
				Kind:   "result",
				Anchor: slicing.Anchor{EnclosingSymbolPath: "handler.go#dispatchRequest"},
			},
		},
	}

	frames := c.CurateFlowSequence(trace)

	for _, f := range frames {
		if f.IsRecursion {
			t.Errorf("unexpected IsRecursion=true for linear consecutive guards in same function: %+v", f)
		}
	}

	decisions := 0
	for _, frame := range frames {
		if frame.Role == "decision" {
			decisions++
			if len(frame.StepRefs) != 1 {
				t.Fatalf("guards combined without purpose evidence: %+v", frame)
			}
		}
	}
	if decisions != 2 {
		t.Fatalf("expected two independent decisions, got %d", decisions)
	}

}

func TestMacroClumper_ClumpGroupsSamePurposeAndSeparatesDifferentPurposes(t *testing.T) {
	mc := curator.NewMacroClumper()
	branchAuth := "!hasPermission"
	branchLimit := "amount > limit"
	branchVal1 := "len(email) == 0"
	branchVal2 := "len(pw) == 0"

	steps := []fusion.FlowStep{
		{StepID: strPtr("s1"), Name: "entry", Kind: "entry", Anchor: slicing.Anchor{EnclosingSymbolPath: "pkg#Fn"}},
		{StepID: strPtr("s2"), Name: "CheckPermission", Kind: "guard", Branch: &branchAuth, Anchor: slicing.Anchor{EnclosingSymbolPath: "pkg#Fn"}},
		{StepID: strPtr("s3"), Name: "CheckLimit", Kind: "guard", Branch: &branchLimit, Anchor: slicing.Anchor{EnclosingSymbolPath: "pkg#Fn"}},
		{StepID: strPtr("s4"), Name: "ValidateEmail", Kind: "guard", Branch: &branchVal1, Anchor: slicing.Anchor{EnclosingSymbolPath: "pkg#Fn"}},
		{StepID: strPtr("s5"), Name: "ValidatePassword", Kind: "guard", Branch: &branchVal2, Anchor: slicing.Anchor{EnclosingSymbolPath: "pkg#Fn"}},
		{StepID: strPtr("s6"), Name: "done", Kind: "result", Anchor: slicing.Anchor{EnclosingSymbolPath: "pkg#Fn"}},
	}

	candidates := mc.Clump(steps, nil)
	if len(candidates) != 5 {
		t.Fatalf("expected 5 candidate frames, got %d: %+v", len(candidates), candidates)
	}
	if candidates[1].Role != "decision" || len(candidates[1].StepRefs) != 1 {
		t.Errorf("expected independent auth guard, got %+v", candidates[1])
	}
	if candidates[2].Role != "decision" || len(candidates[2].StepRefs) != 1 {
		t.Errorf("expected independent limit guard, got %+v", candidates[2])
	}
	if candidates[3].Role != "decision" || len(candidates[3].StepRefs) != 2 || candidates[3].Title != "입력값 검증" {
		t.Errorf("expected grouped validation guards, got %+v", candidates[3])
	}
}

// Measure curation without a machine-dependent pass/fail duration.
func BenchmarkCurator(b *testing.B) {
	c := curator.NewCurator()

	stepCount := 1000
	steps := make([]fusion.FlowStep, stepCount)
	for i := 0; i < stepCount; i++ {
		kind := "process"
		if i == 0 {
			kind = "entry"
		} else if i == stepCount-1 {
			kind = "result"
		} else if i%10 == 0 {
			kind = "guard"
		}
		steps[i] = fusion.FlowStep{
			StepID: strPtr(fmt.Sprintf("step-%04d", i+1)),
			Name:   fmt.Sprintf("Step %d", i+1),
			Kind:   kind,
			Anchor: slicing.Anchor{
				RepoRelativePath:    fmt.Sprintf("pkg/file%d.go", i/10),
				EnclosingSymbolPath: fmt.Sprintf("pkg/file%d.go#Func%d", i/10, i%10),
			},
		}
	}

	trace := curator.RawExecutionTrace{
		FlowID: "bench-flow-1000",
		Steps:  steps,
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		frames := c.CurateFlowSequence(trace)
		if len(frames) != stepCount {
			b.Fatalf("independent steps lost: %d", len(frames))
		}
	}
}
