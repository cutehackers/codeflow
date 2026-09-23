package agentgateway_test

import (
	"codeflow/internal/curator/semantic"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"codeflow/internal/agentgateway"
	"codeflow/internal/collector/fusion"
	"codeflow/internal/collector/slicing"
	"codeflow/internal/curator"
)

func TestCompactPayload_TokenCountBudget(t *testing.T) {
	// Create a realistic 6-step business flow
	steps := []fusion.FlowStep{
		{
			StepID: strPtr("s1"),
			Name:   "OrderCheckoutHandler",
			Kind:   "entry",
			Anchor: slicing.Anchor{
				RepoRelativePath:    "pkg/checkout/handler.go",
				ByteRange:           [2]int{100, 200},
				EnclosingSymbolPath: "pkg/checkout/handler.go#OrderCheckoutHandler",
			},
		},
		{
			StepID: strPtr("s2"),
			Name:   "ValidateStock",
			Kind:   "guard",
			Anchor: slicing.Anchor{
				RepoRelativePath:    "pkg/stock/validator.go",
				ByteRange:           [2]int{300, 450},
				EnclosingSymbolPath: "pkg/stock/validator.go#ValidateStock",
			},
		},
		{
			StepID: strPtr("s3"),
			Name:   "CheckUserQuota",
			Kind:   "guard",
			Anchor: slicing.Anchor{
				RepoRelativePath:    "pkg/stock/validator.go",
				ByteRange:           [2]int{500, 600},
				EnclosingSymbolPath: "pkg/stock/validator.go#ValidateStock",
			},
		},
		{
			StepID: strPtr("s4"),
			Name:   "TossPayment.Approve",
			Kind:   "external_effect",
			Anchor: slicing.Anchor{
				RepoRelativePath:    "pkg/payment/pg.go",
				ByteRange:           [2]int{1200, 1400},
				EnclosingSymbolPath: "pkg/payment/pg.go#Approve",
			},
		},
		{
			StepID: strPtr("s5"),
			Name:   "SaveOrderRecord",
			Kind:   "mutation",
			Anchor: slicing.Anchor{
				RepoRelativePath:    "pkg/order/repo.go",
				ByteRange:           [2]int{800, 950},
				EnclosingSymbolPath: "pkg/order/repo.go#Save",
			},
		},
		{
			StepID: strPtr("s6"),
			Name:   "CompleteOrder",
			Kind:   "result",
			Anchor: slicing.Anchor{
				RepoRelativePath:    "pkg/checkout/handler.go",
				ByteRange:           [2]int{250, 350},
				EnclosingSymbolPath: "pkg/checkout/handler.go#CompleteOrder",
			},
		},
	}

	spec := fusion.FlowSpec{
		FlowID: "flow-order-checkout",
		Steps:  steps,
	}

	c := curator.NewCurator()
	sequence := c.CurateFlowSpec(spec)
	frames := sequence.Frames

	payload := agentgateway.BuildCompactPayload(sequence, 2, 4)
	if len(sequence.SummaryLimitations) == 0 || !reflect.DeepEqual(payload.SummaryLimitations, sequence.SummaryLimitations) {
		t.Fatal("summary limitations lost")
	}

	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal compact payload: %v", err)
	}

	// 1. Verify required fields
	if payload.FlowID != "flow-order-checkout" {
		t.Errorf("unexpected flowId: %s", payload.FlowID)
	}
	if !strings.Contains(payload.Summary, "OrderCheckoutHandler") {
		t.Errorf("summary missing entry symbol: %s", payload.Summary)
	}
	if payload.RadarSummary.DirectCallers != 2 || payload.RadarSummary.DirectCallees != 4 {
		t.Errorf("unexpected radar summary: %+v", payload.RadarSummary)
	}

	// Compact is a projection, not another opportunity to regroup or invent locations.
	if len(payload.Frames) != len(frames) {
		t.Fatalf("frame count changed: %d vs %d", len(payload.Frames), len(frames))
	}
	for i, frame := range payload.Frames {
		if frame.ID != frames[i].FrameID || frame.PrimaryStepRef != frames[i].PrimaryStepRef || frame.Steps != len(frames[i].StepRefs) {
			t.Errorf("frame identity changed: %+v", frame)
		}
		if strings.Contains(frame.File, ":") {
			t.Errorf("byte offsets were presented as line numbers: %s", frame.File)
		}
	}

	// 3. Token count estimation: 1 token ~= 4 chars (GPT/Claude/BPE benchmark)
	// This small fixture should remain compact. Large flows must preserve their steps.
	tokenEstimate := len(data) / 4
	if tokenEstimate > 1000 {
		t.Fatalf("payload exceeded 1,000 tokens ceiling: %d tokens (%d bytes)", tokenEstimate, len(data))
	}
	t.Logf("Compact payload size: %d bytes (~%d tokens)", len(data), tokenEstimate)

	// Pretty-printed verification
	pretty, _ := json.MarshalIndent(payload, "", "  ")
	fmt.Printf("Generated compact payload (~%d tokens):\n%s\n", tokenEstimate, string(pretty))
}

func strPtr(s string) *string {
	return &s
}

func TestCompactPayloadPreservesEvidenceStatus(t *testing.T) {
	for _, status := range []string{"verified", "partial", "unknown"} {
		t.Run(status, func(t *testing.T) {
			frames := []curator.FlowSequenceFrame{{FrameID: "f", Status: status, StepRefs: []string{"s"}, PrimaryStepRef: "s"}}
			payload := agentgateway.BuildCompactPayload(&semantic.FlowSequence{FlowID: "flow", Frames: frames}, -1, -1)
			if payload.Frames[0].Status != status {
				t.Fatal("evidence status lost")
			}
			if payload.RadarSummary != nil {
				t.Fatal("unmeasured relation count is not absent")
			}
		})
	}
}
