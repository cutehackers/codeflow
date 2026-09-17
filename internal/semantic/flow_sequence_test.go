package semantic

import (
	"encoding/json"
	"testing"

	"codeflow/internal/contractharness"
	"codeflow/internal/fusion"
	"codeflow/internal/slicing"
)

func TestBuildFlowSequence_ConstructsValidFlowSequence(t *testing.T) {
	branchText := "items.length > 0"
	mapIR := &SemanticMapIR{
		SchemaID:                   SemanticMapSchemaID,
		SchemaVersion:              SemanticSchemaVersion,
		GenerationID:               "gen-123",
		ComputedBasisID:            "basis-456",
		ValidatedAgainstSnapshotID: "snap-789",
		Steps: []SemanticStep{
			{
				StepID:             "step-1",
				StructuralIdentity: "ident-1",
				Ordinal:            1,
				Name:               "고객 결제 버튼 클릭",
				TechnicalName:      "CheckoutPage.submit",
				Layer:              "presentation",
				Kind:               "user_action",
				Anchor: slicing.Anchor{
					RepoRelativePath:    "web/checkout.ts",
					EnclosingSymbolPath: "CheckoutPage.submit",
				},
				EvidenceRefs: []string{"ev-1"},
			},
			{
				StepID:             "step-2",
				StructuralIdentity: "ident-2",
				Ordinal:            2,
				Name:               "장바구니 정합성 검증",
				TechnicalName:      "CartService.validate",
				Layer:              "gateway",
				Kind:               "guard",
				Branch:             &branchText,
				Anchor: slicing.Anchor{
					RepoRelativePath:    "services/cart.ts",
					EnclosingSymbolPath: "CartService.validate",
				},
				EvidenceRefs: []string{"ev-2"},
			},
			{
				StepID:             "step-3-helper",
				StructuralIdentity: "ident-3-helper",
				Ordinal:            3,
				Name:               "내부 데이터 포맷팅",
				TechnicalName:      "CartService.formatPayload",
				Layer:              "gateway",
				Kind:               "call",
				Anchor: slicing.Anchor{
					RepoRelativePath:    "services/cart.ts",
					EnclosingSymbolPath: "CartService.formatPayload",
				},
				EvidenceRefs: []string{"ev-3"},
			},
			{
				StepID:             "step-4",
				StructuralIdentity: "ident-4",
				Ordinal:            4,
				Name:               "재고 선점 락 부여",
				TechnicalName:      "InventoryService.lockStock",
				Layer:              "domain",
				Kind:               "mutation",
				StateDelta: &fusion.StateDelta{
					Before: "stock: available",
					After:  "stock: locked",
				},
				Anchor: slicing.Anchor{
					RepoRelativePath:    "domain/inventory.ts",
					EnclosingSymbolPath: "InventoryService.lockStock",
				},
				EvidenceRefs: []string{"ev-4"},
			},
			{
				StepID:             "step-5",
				StructuralIdentity: "ident-5",
				Ordinal:            5,
				Name:               "PG 결제 승인 요청",
				TechnicalName:      "PaymentGatewayClient.charge",
				Layer:              "external",
				Kind:               "external_effect",
				Anchor: slicing.Anchor{
					RepoRelativePath:    "infra/payment.ts",
					EnclosingSymbolPath: "PaymentGatewayClient.charge",
				},
				EvidenceRefs: []string{"ev-5"},
			},
			{
				StepID:             "step-6",
				StructuralIdentity: "ident-6",
				Ordinal:            6,
				Name:               "주문 완료 결과 반환",
				TechnicalName:      "OrderController.complete",
				Layer:              "application",
				Kind:               "return",
				Anchor: slicing.Anchor{
					RepoRelativePath:    "app/order.ts",
					EnclosingSymbolPath: "OrderController.complete",
				},
				EvidenceRefs: []string{"ev-6"},
			},
		},
		Edges: []SemanticEdge{
			{FromStepID: "step-1", ToStepID: "step-2", Kind: "call", ResolutionStatus: "resolved"},
			{FromStepID: "step-2", ToStepID: "step-3-helper", Kind: "call", ResolutionStatus: "resolved"},
			{FromStepID: "step-3-helper", ToStepID: "step-4", Kind: "call", ResolutionStatus: "resolved"},
			{FromStepID: "step-4", ToStepID: "step-5", Kind: "call", ResolutionStatus: "resolved"},
			{FromStepID: "step-5", ToStepID: "step-6", Kind: "call", ResolutionStatus: "resolved"},
		},
	}

	sb := BuildFlowSequence(mapIR)
	if sb == nil {
		t.Fatalf("expected non-nil FlowSequence")
	}

	// Verify schema compliance
	sbBytes, err := json.Marshal(sb)
	if err != nil {
		t.Fatalf("failed to marshal FlowSequence: %v", err)
	}
	if err := contractharness.ValidateFlowSequence(sbBytes); err != nil {
		t.Fatalf("contractharness.ValidateFlowSequence failed: %v", err)
	}

	// Verify frame count: step-3-helper was collapsed into frame-02 (CartService.validate)
	// Frames:
	// 1: step-1 (entry)
	// 2: step-2 (decision) + step-3-helper (collapsed)
	// 3: step-4 (process)
	// 4: step-5 (effect)
	// 5: step-6 (result)
	if len(sb.Frames) != 5 {
		t.Fatalf("expected 5 frames, got %d", len(sb.Frames))
	}

	f2 := sb.Frames[1]
	if f2.Role != "decision" {
		t.Errorf("frame 2 expected role decision, got %s", f2.Role)
	}
	if len(f2.StepRefs) != 2 || f2.StepRefs[0] != "step-2" || f2.StepRefs[1] != "step-3-helper" {
		t.Errorf("frame 2 expected collapsed step-3-helper in stepRefs, got %v", f2.StepRefs)
	}
	if f2.CollapsedDetail == nil || f2.CollapsedDetail.Count != 1 {
		t.Errorf("frame 2 expected collapsedDetail count 1, got %+v", f2.CollapsedDetail)
	}

	// Verify FrameMatchKey: canonical symbol and role without line/byte or snapshot
	expectedKey := "decision|CartService.validate"
	if f2.FrameMatchKey != expectedKey {
		t.Errorf("expected frameMatchKey %q, got %q", expectedKey, f2.FrameMatchKey)
	}

	// Verify FindMatchingFrame
	matched := sb.FindMatchingFrame("decision|CartService.validate")
	if matched == nil || matched.FrameID != "frame-02" {
		t.Fatalf("expected to find frame-02 for matching key, got %+v", matched)
	}

	// Ambiguous or non-existent key returns nil
	if sb.FindMatchingFrame("non-existent") != nil {
		t.Errorf("expected nil for non-existent match key")
	}
}

func TestBuildFlowSequence_MissingArchitectureConstructsFramesAndPartialStatus(t *testing.T) {
	mapIR := &SemanticMapIR{
		SchemaID:                   SemanticMapSchemaID,
		SchemaVersion:              SemanticSchemaVersion,
		GenerationID:               "gen-no-arch",
		ComputedBasisID:            "basis-no-arch",
		ValidatedAgainstSnapshotID: "snap-no-arch",
		Steps: []SemanticStep{
			{
				StepID:             "step-a",
				StructuralIdentity: "ident-a",
				Ordinal:            1,
				Name:               "초기 진입 처리",
				TechnicalName:      "EntryHandler.handle",
				Layer:              "", // Empty architecture layer
				Kind:               "user_action",
				EvidenceRefs:       nil, // Missing evidence -> partial
			},
			{
				StepID:             "step-b",
				StructuralIdentity: "ident-b",
				Ordinal:            2,
				Name:               "미지의 외부 서비스 호출",
				TechnicalName:      "UnknownBoundary.call",
				Layer:              "custom_layer",
				Kind:               "external_effect",
				EvidenceRefs:       nil,
			},
		},
		Unknowns: []fusion.Unknown{
			{
				Subject: "UnknownBoundary.call",
				Reason:  "unresolved foreign boundary",
			},
		},
		BoundaryTargets: []string{"step-b"},
	}

	sb := BuildFlowSequence(mapIR)
	if sb == nil {
		t.Fatalf("expected non-nil FlowSequence")
	}

	// Valid frames constructed despite missing architecture layer (FA-02)
	if len(sb.Frames) != 2 {
		t.Fatalf("expected 2 frames, got %d", len(sb.Frames))
	}

	f1 := sb.Frames[0]
	if f1.Role != "entry" {
		t.Errorf("expected role entry, got %s", f1.Role)
	}
	// Status should be partial due to missing evidence
	if f1.Status != "partial" {
		t.Errorf("expected status partial for f1, got %s", f1.Status)
	}

	f2 := sb.Frames[1]
	// Should identify boundary target / unknown
	if f2.Role != "boundary" {
		t.Errorf("expected role boundary, got %s", f2.Role)
	}
	if f2.Status != "unknown" {
		t.Errorf("expected status unknown for f2, got %s", f2.Status)
	}

	// Validate JSON schema
	sbBytes, err := json.Marshal(sb)
	if err != nil {
		t.Fatalf("failed to marshal FlowSequence: %v", err)
	}
	if err := contractharness.ValidateFlowSequence(sbBytes); err != nil {
		t.Fatalf("contractharness.ValidateFlowSequence failed: %v", err)
	}
}

func TestFlowSequencePreservesMeaningAcrossSameFileAndLayer(t *testing.T) {
	condition := "stock > 0"
	for _, layer := range []string{"domain", ""} {
		t.Run("layer="+layer, func(t *testing.T) {
			m := &SemanticMapIR{Steps: []SemanticStep{
				{StepID: "entry", Kind: "user_action"},
				{StepID: "helper", Kind: "call"},
				{StepID: "decision", Kind: "guard", Branch: &condition},
				{StepID: "state", Kind: "mutation"},
				{StepID: "effect", Kind: "external_effect"},
				{StepID: "failure", Kind: "failure"},
				{StepID: "return", Kind: "return"},
			}}
			for i := range m.Steps {
				m.Steps[i].Layer = layer
				m.Steps[i].Anchor = slicing.Anchor{RepoRelativePath: "same.ts", EnclosingSymbolPath: "checkout"}
			}
			sb := BuildFlowSequence(m)
			if len(sb.Frames) != 6 {
				t.Fatalf("lost meaningful scenes: %+v", sb.Frames)
			}
			if len(sb.Frames[0].StepRefs) != 2 {
				t.Fatal("helper not collapsed")
			}
			for i, role := range []string{"entry", "decision", "process", "effect", "decision", "result"} {
				if sb.Frames[i].Role != role {
					t.Errorf("scene %d = %s, want %s", i, sb.Frames[i].Role, role)
				}
			}
		})
	}
}
