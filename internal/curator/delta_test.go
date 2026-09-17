package curator_test

import (
	"testing"

	"codeflow/internal/collector/fusion"
	"codeflow/internal/collector/slicing"
	"codeflow/internal/curator"
)

func TestCurator_ComputeDelta(t *testing.T) {
	baseline := fusion.FlowSpec{
		FlowID: "flow-01",
		Title:  "Checkout Flow",
		Steps: []fusion.FlowStep{
			{
				Ordinal: 1,
				Name:    "Validate Cart",
				Kind:    "guard",
				Anchor:  slicing.Anchor{EnclosingSymbolPath: "cart.go#Validate"},
			},
			{
				Ordinal: 2,
				Name:    "Charge Card",
				Kind:    "mutation",
				Anchor:  slicing.Anchor{EnclosingSymbolPath: "pay.go#Charge"},
			},
		},
		Unknowns: []fusion.Unknown{
			{Subject: "notify.go#Send", Reason: "external dependency"},
		},
	}

	current := fusion.FlowSpec{
		FlowID: "flow-01",
		Title:  "Checkout Flow",
		Steps: []fusion.FlowStep{
			{
				Ordinal: 1,
				Name:    "Validate Cart and Stock",
				Kind:    "guard",
				Anchor:  slicing.Anchor{EnclosingSymbolPath: "cart.go#Validate"},
			},
			{
				Ordinal: 2,
				Name:    "Send Receipt",
				Kind:    "call",
				Anchor:  slicing.Anchor{EnclosingSymbolPath: "receipt.go#Send"},
			},
		},
		Unknowns: []fusion.Unknown{
			{Subject: "audit.go#Log", Reason: "unverified audit sink"},
		},
	}

	delta := curator.ComputeDelta(baseline, current)

	if delta.BaselineFlowID != "flow-01" || delta.CurrentFlowID != "flow-01" {
		t.Fatalf("unexpected flow IDs in delta: baseline=%s current=%s", delta.BaselineFlowID, delta.CurrentFlowID)
	}

	// Step 1 was modified (name changed)
	if len(delta.ModifiedSteps) != 1 || delta.ModifiedSteps[0].CurrentStep.Name != "Validate Cart and Stock" {
		t.Errorf("expected 1 modified step, got: %+v", delta.ModifiedSteps)
	}

	// Step 2 was added ("receipt.go#Send")
	if len(delta.AddedSteps) != 1 || delta.AddedSteps[0].Anchor.EnclosingSymbolPath != "receipt.go#Send" {
		t.Errorf("expected 1 added step, got: %+v", delta.AddedSteps)
	}

	// Step "pay.go#Charge" was removed
	if len(delta.RemovedSteps) != 1 || delta.RemovedSteps[0].Anchor.EnclosingSymbolPath != "pay.go#Charge" {
		t.Errorf("expected 1 removed step, got: %+v", delta.RemovedSteps)
	}

	// Unknown "audit.go#Log" was added
	if len(delta.AddedUnknowns) != 1 || delta.AddedUnknowns[0].Subject != "audit.go#Log" {
		t.Errorf("expected 1 added unknown, got: %+v", delta.AddedUnknowns)
	}

	// Unknown "notify.go#Send" was removed
	if len(delta.RemovedUnknowns) != 1 || delta.RemovedUnknowns[0].Subject != "notify.go#Send" {
		t.Errorf("expected 1 removed unknown, got: %+v", delta.RemovedUnknowns)
	}
}
