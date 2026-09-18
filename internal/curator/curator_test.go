package curator_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"codeflow/internal/collector/fusion"
	"codeflow/internal/collector/slicing"
	"codeflow/internal/curator"
)

func strPtr(s string) *string {
	return &s
}

// 1. 150+ Steps Large Trace Test (Ceiling Bound: strictly <= 7 frames, all stepRefs preserved)
func TestCurator_LargeTraceCeilingBound(t *testing.T) {
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

	if len(frames) < 4 || len(frames) > 7 {
		t.Fatalf("expected 4~7 frames for 160 steps, got %d", len(frames))
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

// 4. Mutual Indirect Recursion (A -> B -> C -> A) Clumping and Recursion Badge
func TestCurator_IndirectRecursionCycle(t *testing.T) {
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

	// Look for the recursive frame
	var recursionFrame *curator.FlowSequenceFrame
	for i := range frames {
		if frames[i].IsRecursion {
			recursionFrame = &frames[i]
			break
		}
	}

	if recursionFrame == nil {
		t.Fatalf("expected a frame with IsRecursion=true, got none in %+v", frames)
	}

	if recursionFrame.Title != "ProcessOrder" {
		t.Errorf("expected recursion frame to be collapsed under ProcessOrder, got %s", recursionFrame.Title)
	}

	if recursionFrame.CollapsedDetail == nil {
		t.Fatal("expected CollapsedDetail on recursion frame")
	}

	if !strings.Contains(recursionFrame.CollapsedDetail.Reason, "재귀/순환 실행 경로 접힘") {
		t.Errorf("expected recursion reason in CollapsedDetail, got: %s", recursionFrame.CollapsedDetail.Reason)
	}

	// Verify cycle steps [step-a, step-b, step-c, step-a2] are absorbed in stepRefs
	if len(recursionFrame.StepRefs) < 4 {
		t.Errorf("expected at least 4 stepRefs in recursion frame, got %d", len(recursionFrame.StepRefs))
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

// 5.5 Consecutive Guards in Same Function should merge into "사전 유효성 검증" without false recursion
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

	// Check that a decision frame with title "사전 유효성 검증" was created containing both guard steps
	var decisionFrame *curator.FlowSequenceFrame
	for i := range frames {
		if frames[i].Role == "decision" {
			decisionFrame = &frames[i]
			break
		}
	}

	if decisionFrame == nil {
		t.Fatalf("expected a decision frame for consecutive guards, got none: %+v", frames)
	}

	if decisionFrame.Title != "사전 유효성 검증" {
		t.Errorf("expected title '사전 유효성 검증', got %s", decisionFrame.Title)
	}

	if len(decisionFrame.StepRefs) != 2 {
		t.Errorf("expected 2 stepRefs in decision frame, got %d", len(decisionFrame.StepRefs))
	}
}

// 6. Benchmark: 1,000 steps curated in <5ms
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

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		t0 := time.Now()
		frames := c.CurateFlowSequence(trace)
		elapsed := time.Since(t0)
		if elapsed > 5*time.Millisecond {
			b.Fatalf("curation exceeded 5ms budget: %v", elapsed)
		}
		if len(frames) > 7 {
			b.Fatalf("exceeded 7 frames: %d", len(frames))
		}
	}
}
