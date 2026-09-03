package semantic

import (
	"testing"

	"codeflow/internal/fusion"
	"codeflow/internal/slicing"
)

func TestComputeChangeImpact_MissingPrecondition(t *testing.T) {
	_, err := ComputeChangeImpact(ImpactTarget{}, nil, ImpactOptions{})
	if err == nil {
		t.Fatal("expected error when both symbolId and changeBatchId are empty")
	}
	if err.Error() != "missing_precondition: either symbolId or changeBatchId must be provided" {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestComputeChangeImpact_DirectAndIndirectTraversal(t *testing.T) {
	sideEff := "api:Stripe.charge"
	mapIR := &SemanticMapIR{
		MapID:           "map-1",
		GenerationID:    "gen-1",
		ComputedBasisID: "basis-1",
		SchemaVersion:   1,
		Coverage: &CoverageBoundary{
			IncludedSourceRoots: []string{"src"},
		},
		Steps: []SemanticStep{
			{
				StepID:        "step-order",
				Name:          "주문 처리",
				TechnicalName: "OrderController.checkout",
				Anchor: slicing.Anchor{
					EnclosingSymbolPath: "OrderController.checkout",
					RepoRelativePath:    "src/controllers/order.ts",
				},
			},
			{
				StepID:        "step-payment",
				Name:          "결제 승인",
				TechnicalName: "PaymentService.process",
				Anchor: slicing.Anchor{
					EnclosingSymbolPath: "PaymentService.process",
					RepoRelativePath:    "src/services/payment.ts",
				},
				StateDelta: &fusion.StateDelta{
					Before: "State.Cart",
					After:  "State.Paid",
				},
				SideEffect: &sideEff,
				Rules:      []string{"test:testPaymentSuccess"},
			},
		},
		Edges: []SemanticEdge{
			{
				FromStepID:       "step-order",
				ToStepID:         "step-payment",
				ToSymbolPath:     "PaymentService.process",
				Kind:             "calls",
				ResolutionStatus: "verified",
			},
		},
		Evidence: []SemanticEvidence{
			{
				EvidenceID: "ev-order-ctrl",
				Kind:       "ast_call",
				Anchor: slicing.Anchor{
					EnclosingSymbolPath: "OrderController.checkout",
					RepoRelativePath:    "src/controllers/order.ts",
				},
				ValidationStatus: "verified",
			},
		},
	}

	target := ImpactTarget{
		SymbolID: "PaymentService.process",
	}

	res, err := ComputeChangeImpact(target, mapIR, ImpactOptions{
		MaxDepth: 3,
	})
	if err != nil {
		t.Fatalf("ComputeChangeImpact failed: %v", err)
	}

	// VS06-A1, VS06-A3: direct caller reverse slice
	if len(res.DirectImpact.Callers) != 1 {
		t.Fatalf("expected 1 direct caller, got %d", len(res.DirectImpact.Callers))
	}
	if res.DirectImpact.Callers[0].SymbolPath != "OrderController.checkout" {
		t.Errorf("expected caller OrderController.checkout, got %s", res.DirectImpact.Callers[0].SymbolPath)
	}

	// VS06-A3: state mutation forward slice
	if len(res.DirectImpact.StateMutations) != 1 {
		t.Fatalf("expected 1 state mutation, got %d", len(res.DirectImpact.StateMutations))
	}
	if res.DirectImpact.StateMutations[0].TargetState != "State.Cart → State.Paid" {
		t.Errorf("expected state transition State.Cart → State.Paid, got %s", res.DirectImpact.StateMutations[0].TargetState)
	}

	// VS06-A3: external effect forward slice
	if len(res.DirectImpact.ExternalEffects) != 1 {
		t.Fatalf("expected 1 external effect, got %d", len(res.DirectImpact.ExternalEffects))
	}
	if res.DirectImpact.ExternalEffects[0].Target != "Stripe.charge" {
		t.Errorf("expected external effect Stripe.charge, got %s", res.DirectImpact.ExternalEffects[0].Target)
	}

	// VS06-A3: test forward slice
	if len(res.DirectImpact.Tests) != 1 {
		t.Fatalf("expected 1 test, got %d", len(res.DirectImpact.Tests))
	}
	if res.DirectImpact.Tests[0].TestSymbolPath != "test:testPaymentSuccess" {
		t.Errorf("expected test:testPaymentSuccess, got %s", res.DirectImpact.Tests[0].TestSymbolPath)
	}

	// VS06-A4: bounded indirect impact
	if !res.IndirectImpact.Bounded {
		t.Error("expected indirect impact to be bounded")
	}
	if res.IndirectImpact.MaxDepth > 5 {
		t.Errorf("expected maxDepth <= 5, got %d", res.IndirectImpact.MaxDepth)
	}

	// VS06-A7: basis, generation, freshness
	if res.ComputedBasisID != "basis-1" {
		t.Errorf("expected basis-1, got %s", res.ComputedBasisID)
	}
	if res.Freshness != "current" {
		t.Errorf("expected current freshness, got %s", res.Freshness)
	}
}

func TestComputeChangeImpact_UnresolvedDynamicCaller(t *testing.T) {
	mapIR := &SemanticMapIR{
		MapID:           "map-2",
		GenerationID:    "gen-2",
		ComputedBasisID: "basis-2",
		SchemaVersion:   1,
		Steps: []SemanticStep{
			{
				StepID:        "step-dynamic",
				Name:          "동적 이벤트",
				TechnicalName: "EventBus.publish",
				Anchor: slicing.Anchor{
					EnclosingSymbolPath: "EventBus.publish",
					RepoRelativePath:    "src/eventbus.ts",
				},
			},
		},
		Unknowns: []fusion.Unknown{
			{
				Subject: "EventBus.publish",
				Reason:  "reflection dynamic dispatch cannot resolve subscriber statically",
			},
		},
	}

	target := ImpactTarget{
		SymbolID: "EventBus.publish",
	}

	res, err := ComputeChangeImpact(target, mapIR, ImpactOptions{})
	if err != nil {
		t.Fatalf("ComputeChangeImpact failed: %v", err)
	}

	// VS06-A5: unconfirmed dynamic relation marked as unresolved boundary
	if len(res.UnresolvedBoundaries) == 0 {
		t.Fatal("expected at least 1 unresolved boundary for dynamic dispatch")
	}
	if res.UnresolvedBoundaries[0].BoundaryType != "unresolved_dynamic_caller" {
		t.Errorf("expected unresolved_dynamic_caller, got %s", res.UnresolvedBoundaries[0].BoundaryType)
	}
	if res.UnknownCount < 1 {
		t.Errorf("expected unknownCount >= 1, got %d", res.UnknownCount)
	}
}
