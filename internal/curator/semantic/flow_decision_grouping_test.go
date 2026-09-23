package semantic_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"codeflow/internal/collector/slicing"
	"codeflow/internal/curator/semantic"
)

func TestDecisionGrouping_GroupsConsecutiveSamePurposeGuards(t *testing.T) {
	anchor := slicing.Anchor{RepoRelativePath: "validator.ts", EnclosingSymbolPath: "validateForm"}
	branch1 := "email.length == 0"
	branch2 := "password.length < 8"

	model := &semantic.SemanticMapIR{
		MapID: "map-form-validation",
		Steps: []semantic.SemanticStep{
			{StepID: "entry", Kind: "user_action", Ordinal: 1, Anchor: anchor},
			{StepID: "val-email", Kind: "guard", Name: "이메일 검증", Branch: &branch1, Ordinal: 2, InvocationID: "validate", Anchor: anchor},
			{StepID: "val-password", Kind: "guard", Name: "비밀번호 검증", Branch: &branch2, Ordinal: 3, InvocationID: "validate", Anchor: anchor},
			{StepID: "result", Kind: "return", Name: "완료", Ordinal: 4, InvocationID: "validate", Anchor: anchor},
		},
		Edges: []semantic.SemanticEdge{
			{FromStepID: "entry", ToStepID: "val-email", Kind: "control_flow", ResolutionStatus: "resolved"},
			{FromStepID: "val-email", ToStepID: "val-password", Kind: "control_flow", ResolutionStatus: "resolved", Conditions: []semantic.SemanticBranchCondition{{StepID: "val-email", Outcome: "falsy"}}},
			{FromStepID: "val-password", ToStepID: "result", Kind: "control_flow", ResolutionStatus: "resolved", Conditions: []semantic.SemanticBranchCondition{{StepID: "val-password", Outcome: "falsy"}}},
		},
	}

	seq := semantic.BuildFlowSequence(model)
	// Expected frames: entry (1), grouped validation decision (2), result (3) => 3 frames
	if len(seq.Frames) != 3 {
		t.Fatalf("expected 3 frames after decision grouping, got %d: %+v", len(seq.Frames), seq.Frames)
	}

	decisionFrame := seq.Frames[1]
	if decisionFrame.Role != "decision" {
		t.Fatalf("expected decision role, got %s", decisionFrame.Role)
	}
	if len(decisionFrame.StepRefs) != 2 || decisionFrame.StepRefs[0] != "val-email" || decisionFrame.StepRefs[1] != "val-password" {
		t.Fatalf("expected grouped stepRefs [val-email, val-password], got %+v", decisionFrame.StepRefs)
	}
	if decisionFrame.PrimaryStepRef != "val-email" {
		t.Fatalf("expected primaryStepRef val-email, got %s", decisionFrame.PrimaryStepRef)
	}
	if decisionFrame.CollapsedDetail == nil || decisionFrame.CollapsedDetail.Count != 1 {
		t.Fatalf("expected CollapsedDetail with count 1, got %+v", decisionFrame.CollapsedDetail)
	}

	// Review also preserves grouping reason
	review := semantic.ReviewFlowSequence(model)
	if review.Candidates[1].GroupingReason != decisionFrame.CollapsedDetail.Reason {
		t.Fatalf("expected review GroupingReason to match CollapsedDetail, got %s", review.Candidates[1].GroupingReason)
	}
}

func TestDecisionGrouping_SeparatesDifferentPurposeDecisions(t *testing.T) {
	anchor := slicing.Anchor{RepoRelativePath: "order.ts", EnclosingSymbolPath: "checkout"}
	branchAuth := "!hasPermission"
	branchLimit := "amount > limit"

	model := &semantic.SemanticMapIR{
		MapID: "map-checkout",
		Steps: []semantic.SemanticStep{
			{StepID: "entry", Kind: "user_action", Ordinal: 1, Anchor: anchor},
			{StepID: "auth-guard", Kind: "guard", Name: "권한 확인", Branch: &branchAuth, Ordinal: 2, InvocationID: "checkout", Anchor: anchor},
			{StepID: "limit-guard", Kind: "guard", Name: "결제 한도 판단", Branch: &branchLimit, Ordinal: 3, InvocationID: "checkout", Anchor: anchor},
			{StepID: "result", Kind: "return", Name: "완료", Ordinal: 4, InvocationID: "checkout", Anchor: anchor},
		},
		Edges: []semantic.SemanticEdge{
			{FromStepID: "entry", ToStepID: "auth-guard", Kind: "control_flow", ResolutionStatus: "resolved"},
			{FromStepID: "auth-guard", ToStepID: "limit-guard", Kind: "control_flow", ResolutionStatus: "resolved", Conditions: []semantic.SemanticBranchCondition{{StepID: "auth-guard", Outcome: "falsy"}}},
			{FromStepID: "limit-guard", ToStepID: "result", Kind: "control_flow", ResolutionStatus: "resolved", Conditions: []semantic.SemanticBranchCondition{{StepID: "limit-guard", Outcome: "falsy"}}},
		},
	}

	seq := semantic.BuildFlowSequence(model)
	// Both auth-guard and limit-guard must remain separate: 4 frames total
	if len(seq.Frames) != 4 {
		t.Fatalf("expected 4 frames keeping different decisions separate, got %d: %+v", len(seq.Frames), seq.Frames)
	}

	review := semantic.ReviewFlowSequence(model)
	authCandidate := review.Candidates[1]
	if authCandidate.GroupingReason != "서로 다른 핵심 판단은 인접해 있어도 독립 관문으로 분리한다." {
		t.Fatalf("expected separation reason for auth guard, got: %s", authCandidate.GroupingReason)
	}
}

func TestDecisionGrouping_InterveningMutationPreventsGrouping(t *testing.T) {
	anchor := slicing.Anchor{RepoRelativePath: "auth.ts", EnclosingSymbolPath: "authenticate"}
	branch1 := "!token"
	branch2 := "!role"

	model := &semantic.SemanticMapIR{
		MapID: "map-auth-mutation",
		Steps: []semantic.SemanticStep{
			{StepID: "entry", Kind: "user_action", Ordinal: 1, Anchor: anchor},
			{StepID: "auth-token", Kind: "guard", Name: "토큰 검증", Branch: &branch1, Ordinal: 2, InvocationID: "auth", Anchor: anchor},
			{StepID: "record-attempt", Kind: "mutation", Name: "시도 횟수 기록", Ordinal: 3, InvocationID: "auth", Anchor: anchor},
			{StepID: "auth-role", Kind: "guard", Name: "역할 검증", Branch: &branch2, Ordinal: 4, InvocationID: "auth", Anchor: anchor},
			{StepID: "result", Kind: "return", Name: "완료", Ordinal: 5, InvocationID: "auth", Anchor: anchor},
		},
		Edges: []semantic.SemanticEdge{
			{FromStepID: "entry", ToStepID: "auth-token", Kind: "control_flow", ResolutionStatus: "resolved"},
			{FromStepID: "auth-token", ToStepID: "record-attempt", Kind: "control_flow", ResolutionStatus: "resolved"},
			{FromStepID: "record-attempt", ToStepID: "auth-role", Kind: "control_flow", ResolutionStatus: "resolved"},
			{FromStepID: "auth-role", ToStepID: "result", Kind: "control_flow", ResolutionStatus: "resolved"},
		},
	}

	seq := semantic.BuildFlowSequence(model)
	// Mutation between the two auth guards prevents grouping them into one decision gateway
	for _, f := range seq.Frames {
		if f.Role == "decision" && len(f.StepRefs) > 1 {
			t.Fatalf("guards grouped across intervening state mutation: %+v", f)
		}
	}
}

func TestDecisionGrouping_RequestRelevanceDistinguishesCoreDecision(t *testing.T) {
	anchor := slicing.Anchor{RepoRelativePath: "checkout.ts", EnclosingSymbolPath: "processCheckout"}
	branchAuth := "!authenticated"
	branchLimit := "amount > dailyLimit"

	createModel := func(requested string, target string) *semantic.SemanticMapIR {
		m := &semantic.SemanticMapIR{
			MapID: "map-target-checkout",
			Summary: semantic.MapSummary{
				Requested: requested,
			},
			Steps: []semantic.SemanticStep{
				{StepID: "entry", Kind: "user_action", Ordinal: 1, Anchor: anchor},
				{StepID: "auth-guard", Kind: "guard", Name: "권한 확인", Branch: &branchAuth, Ordinal: 2, InvocationID: "chk", Anchor: anchor},
				{StepID: "limit-guard", Kind: "guard", Name: "결제 한도 판단", Branch: &branchLimit, Ordinal: 3, InvocationID: "chk", Anchor: anchor},
				{StepID: "result", Kind: "return", Name: "완료", Ordinal: 4, InvocationID: "chk", Anchor: anchor},
			},
			Edges: []semantic.SemanticEdge{
				{FromStepID: "entry", ToStepID: "auth-guard", Kind: "control_flow", ResolutionStatus: "resolved"},
				{FromStepID: "auth-guard", ToStepID: "limit-guard", Kind: "control_flow", ResolutionStatus: "resolved", Conditions: []semantic.SemanticBranchCondition{{StepID: "auth-guard", Outcome: "falsy"}}},
				{FromStepID: "limit-guard", ToStepID: "result", Kind: "control_flow", ResolutionStatus: "resolved", Conditions: []semantic.SemanticBranchCondition{{StepID: "limit-guard", Outcome: "falsy"}}},
			},
		}
		if target == "auth" {
			m.RequirementAlignment = []semantic.RequirementAlignment{
				{
					CriterionID:     "AC-AUTH-DENIAL",
					Description:     "인증 및 권한 거부 원인 확인",
					Status:          "confirmed",
					CoveredStepRefs: []string{"auth-guard"},
				},
			}
		} else if target == "limit" {
			m.RequirementAlignment = []semantic.RequirementAlignment{
				{
					CriterionID:     "AC-PAYMENT-LIMIT",
					Description:     "일일 결제 한도 초과 검사",
					Status:          "confirmed",
					CoveredStepRefs: []string{"limit-guard"},
				},
			}
		}
		return m
	}

	// Request 1: targeting auth denial
	model1 := createModel("접근이 거부되는 이유", "auth")
	review1 := semantic.ReviewFlowSequence(model1)
	if !review1.Candidates[1].RequestSpecific {
		t.Fatal("expected auth-guard to be marked RequestSpecific for auth request")
	}
	if review1.Candidates[2].RequestSpecific {
		t.Fatal("limit-guard should not be RequestSpecific when auth is the evidence target")
	}

	// Request 2: targeting limit
	model2 := createModel("결제 한도가 적용되는 이유", "limit")
	review2 := semantic.ReviewFlowSequence(model2)
	if review2.Candidates[1].RequestSpecific {
		t.Fatal("auth-guard should not be RequestSpecific when limit is the evidence target")
	}
	if !review2.Candidates[2].RequestSpecific {
		t.Fatal("expected limit-guard to be marked RequestSpecific for limit request")
	}

	// Request 3: no evidence provided (general trace)
	model3 := createModel("결제 실행 과정", "none")
	review3 := semantic.ReviewFlowSequence(model3)
	for _, c := range review3.Candidates {
		if c.RequestSpecific {
			t.Fatal("no candidate should be RequestSpecific when no evidence is provided")
		}
	}
}

func TestDecisionGrouping_DeterminismAcrossReviewAndEdgeOrder(t *testing.T) {
	anchor := slicing.Anchor{RepoRelativePath: "validator.ts", EnclosingSymbolPath: "validateForm"}
	branch1 := "x < 0"
	branch2 := "y < 0"

	model := &semantic.SemanticMapIR{
		MapID: "map-determinism",
		Steps: []semantic.SemanticStep{
			{StepID: "entry", Kind: "user_action", Ordinal: 1, Anchor: anchor},
			{StepID: "val-x", Kind: "guard", Name: "검증 X", Branch: &branch1, Ordinal: 2, InvocationID: "v", Anchor: anchor},
			{StepID: "val-y", Kind: "guard", Name: "검증 Y", Branch: &branch2, Ordinal: 3, InvocationID: "v", Anchor: anchor},
			{StepID: "result", Kind: "return", Name: "완료", Ordinal: 4, InvocationID: "v", Anchor: anchor},
		},
		Edges: []semantic.SemanticEdge{
			{FromStepID: "entry", ToStepID: "val-x", Kind: "control_flow", ResolutionStatus: "resolved"},
			{FromStepID: "val-x", ToStepID: "val-y", Kind: "control_flow", ResolutionStatus: "resolved", Conditions: []semantic.SemanticBranchCondition{{StepID: "val-x", Outcome: "falsy"}}},
			{FromStepID: "val-y", ToStepID: "result", Kind: "control_flow", ResolutionStatus: "resolved", Conditions: []semantic.SemanticBranchCondition{{StepID: "val-y", Outcome: "falsy"}}},
		},
	}

	baseReview := semantic.ReviewFlowSequence(model)
	baseJSON, _ := json.Marshal(baseReview)

	// Reverse edges order
	model.Edges[0], model.Edges[2] = model.Edges[2], model.Edges[0]
	revReview := semantic.ReviewFlowSequence(model)
	revJSON, _ := json.Marshal(revReview)

	if !reflect.DeepEqual(baseJSON, revJSON) {
		t.Fatalf("edge reordering changed curation review:\n%s\nvs\n%s", string(baseJSON), string(revJSON))
	}
}

func TestDecisionGrouping_CompoundMixedGuardsRemainIndependent(t *testing.T) {
	anchor := slicing.Anchor{RepoRelativePath: "guard.ts", EnclosingSymbolPath: "checkPermissions"}
	branchAuth := "!isAuth()"
	branchCompound := "!isAuth() || hasQuota()"
	branchLimit := "!hasQuota()"

	model := &semantic.SemanticMapIR{
		MapID: "map-compound-guard",
		Steps: []semantic.SemanticStep{
			{StepID: "entry", Kind: "user_action", Ordinal: 1, Anchor: anchor},
			{StepID: "guard-auth", Kind: "guard", Name: "checkAuth", Branch: &branchAuth, Ordinal: 2, InvocationID: "chk", Anchor: anchor},
			{StepID: "guard-compound", Kind: "guard", Name: "checkAuthAndQuota", Branch: &branchCompound, Ordinal: 3, InvocationID: "chk", Anchor: anchor},
			{StepID: "guard-limit", Kind: "guard", Name: "checkQuota", Branch: &branchLimit, Ordinal: 4, InvocationID: "chk", Anchor: anchor},
			{StepID: "result", Kind: "return", Name: "done", Ordinal: 5, InvocationID: "chk", Anchor: anchor},
		},
		Edges: []semantic.SemanticEdge{
			{FromStepID: "entry", ToStepID: "guard-auth", Kind: "control_flow", ResolutionStatus: "resolved"},
			{FromStepID: "guard-auth", ToStepID: "guard-compound", Kind: "control_flow", ResolutionStatus: "resolved", Conditions: []semantic.SemanticBranchCondition{{StepID: "guard-auth", Outcome: "falsy"}}},
			{FromStepID: "guard-compound", ToStepID: "guard-limit", Kind: "control_flow", ResolutionStatus: "resolved", Conditions: []semantic.SemanticBranchCondition{{StepID: "guard-compound", Outcome: "falsy"}}},
			{FromStepID: "guard-limit", ToStepID: "result", Kind: "control_flow", ResolutionStatus: "resolved", Conditions: []semantic.SemanticBranchCondition{{StepID: "guard-limit", Outcome: "falsy"}}},
		},
	}

	seq := semantic.BuildFlowSequence(model)
	// Compound guard matching both auth and limit must not be swallowed into auth or limit:
	// All 3 decisions must remain independent frames: entry(1) + 3 decisions + result(1) = 5 frames
	if len(seq.Frames) != 5 {
		t.Fatalf("expected 5 frames keeping compound decisions independent, got %d: %+v", len(seq.Frames), seq.Frames)
	}

	review := semantic.ReviewFlowSequence(model)
	compoundCandidate := review.Candidates[2]
	if compoundCandidate.GroupingReason != "서로 다른 핵심 판단은 인접해 있어도 독립 관문으로 분리한다." {
		t.Fatalf("expected separation reason for compound guard, got: %s", compoundCandidate.GroupingReason)
	}
}

func TestDecisionGrouping_DifferentSymbolScopePreventsGrouping(t *testing.T) {
	anchor1 := slicing.Anchor{RepoRelativePath: "auth.ts", EnclosingSymbolPath: "validateUser"}
	anchor2 := slicing.Anchor{RepoRelativePath: "order.ts", EnclosingSymbolPath: "validateOrder"}
	branch1 := "!user.valid"
	branch2 := "!order.valid"

	model := &semantic.SemanticMapIR{
		MapID: "map-cross-symbol",
		Steps: []semantic.SemanticStep{
			{StepID: "entry", Kind: "user_action", Ordinal: 1, Anchor: anchor1},
			{StepID: "val-user", Kind: "guard", Name: "유저 검증", Branch: &branch1, Ordinal: 2, InvocationID: "inv-1", Anchor: anchor1, Layer: "domain"},
			{StepID: "val-order", Kind: "guard", Name: "주문 검증", Branch: &branch2, Ordinal: 3, InvocationID: "inv-2", Anchor: anchor2, Layer: "domain"},
			{StepID: "result", Kind: "return", Name: "완료", Ordinal: 4, InvocationID: "inv-2", Anchor: anchor2, Layer: "domain"},
		},
		Edges: []semantic.SemanticEdge{
			{FromStepID: "entry", ToStepID: "val-user", Kind: "control_flow", ResolutionStatus: "resolved"},
			{FromStepID: "val-user", ToStepID: "val-order", Kind: "control_flow", ResolutionStatus: "resolved"},
			{FromStepID: "val-order", ToStepID: "result", Kind: "control_flow", ResolutionStatus: "resolved"},
		},
	}

	seq := semantic.BuildFlowSequence(model)
	// Guards from different files/symbols/invocations must NOT be grouped together
	if len(seq.Frames) != 4 {
		t.Fatalf("expected 4 frames keeping cross-symbol decisions separate, got %d: %+v", len(seq.Frames), seq.Frames)
	}
}

func TestDecisionGrouping_EnglishAuthKeywordsClassified(t *testing.T) {
	anchor := slicing.Anchor{RepoRelativePath: "auth.ts", EnclosingSymbolPath: "authenticateRequest"}
	branch1 := "!checkAuth(req)"
	branch2 := "!isAuthorized(req)"

	model := &semantic.SemanticMapIR{
		MapID: "map-english-auth",
		Steps: []semantic.SemanticStep{
			{StepID: "entry", Kind: "user_action", Ordinal: 1, Anchor: anchor},
			{StepID: "auth-1", Kind: "guard", TechnicalName: "checkAuth", Name: "checkAuth", Branch: &branch1, Ordinal: 2, InvocationID: "inv", Anchor: anchor},
			{StepID: "auth-2", Kind: "guard", TechnicalName: "isAuthorized", Name: "isAuthorized", Branch: &branch2, Ordinal: 3, InvocationID: "inv", Anchor: anchor},
			{StepID: "result", Kind: "return", Name: "done", Ordinal: 4, InvocationID: "inv", Anchor: anchor},
		},
		Edges: []semantic.SemanticEdge{
			{FromStepID: "entry", ToStepID: "auth-1", Kind: "control_flow", ResolutionStatus: "resolved"},
			{FromStepID: "auth-1", ToStepID: "auth-2", Kind: "control_flow", ResolutionStatus: "resolved", Conditions: []semantic.SemanticBranchCondition{{StepID: "auth-1", Outcome: "falsy"}}},
			{FromStepID: "auth-2", ToStepID: "result", Kind: "control_flow", ResolutionStatus: "resolved", Conditions: []semantic.SemanticBranchCondition{{StepID: "auth-2", Outcome: "falsy"}}},
		},
	}

	seq := semantic.BuildFlowSequence(model)
	// Both english auth checks must be classified as auth and grouped
	if len(seq.Frames) != 3 {
		t.Fatalf("expected 3 frames with grouped english auth guards, got %d", len(seq.Frames))
	}
	if seq.Frames[1].Role != "decision" || len(seq.Frames[1].StepRefs) != 2 {
		t.Fatalf("expected grouped decision frame with 2 stepRefs, got %+v", seq.Frames[1])
	}
}
