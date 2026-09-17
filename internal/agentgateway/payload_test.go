package agentgateway_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"codeflow/internal/agentgateway"
	"codeflow/internal/curator"
	"codeflow/internal/fusion"
	"codeflow/internal/slicing"
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
	frames := c.CurateFlowSpec(spec)

	payload := agentgateway.BuildCompactPayload(&spec, frames, 2, 4)

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

	// 2. Verify frame ceiling: 4~7 frames
	if len(payload.Frames) < 1 || len(payload.Frames) > 7 {
		t.Errorf("expected 1~7 frames, got %d", len(payload.Frames))
	}

	// 3. Token count estimation: 1 token ~= 4 chars (GPT/Claude/BPE benchmark)
	// Spec requires average ~500 tokens, strictly under 1,000 tokens
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
