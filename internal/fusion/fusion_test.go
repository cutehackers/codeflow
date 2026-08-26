package fusion_test

import (
	"os"
	"testing"
	"time"

	"codeflow/internal/fusion"
	"codeflow/internal/slicing"
)

func TestFusionAuthorityMatrixAndSchemaConformance(t *testing.T) {
	entry := "lib/features/auth/email_signup_notifier.dart#EmailSignupNotifier.submit"
	fileHash := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	spanHash := "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210"
	astHash := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	guardCond := "email.contains('@')"
	stateB := "status: idle"
	stateA := "status: submitting"
	target := "AuthRepository.signup"

	sliced := &slicing.SlicedPayload{
		CandidateID:     "cand-1234567890abcdef",
		Language:        "dart",
		EntrySymbolPath: entry,
		Steps: []slicing.SliceStep{
			{
				Ordinal:        1,
				Kind:           "guard",
				Description:    "이메일 형식과 유효성을 검사한다",
				SymbolPath:     "EmailSignupNotifier.submit",
				GuardCondition: &guardCond,
				Anchor: slicing.Anchor{
					RepoRelativePath:        "lib/features/auth/email_signup_notifier.dart",
					ByteRange:               [2]int{10, 50},
					FileHash:                fileHash,
					SpanHash:                spanHash,
					EnclosingSymbolPath:     "EmailSignupNotifier.submit",
					CanonicalAstFingerprint: astHash,
				},
			},
			{
				Ordinal:     2,
				Kind:        "mutation",
				Description: "진행 상태로 갱신한다",
				SymbolPath:  "EmailSignupNotifier.submit",
				StateBefore: &stateB,
				StateAfter:  &stateA,
				Anchor: slicing.Anchor{
					RepoRelativePath:        "lib/features/auth/email_signup_notifier.dart",
					ByteRange:               [2]int{60, 100},
					FileHash:                fileHash,
					SpanHash:                spanHash,
					EnclosingSymbolPath:     "EmailSignupNotifier.submit",
					CanonicalAstFingerprint: astHash,
				},
			},
			{
				Ordinal:      3,
				Kind:         "call",
				Description:  "외부 서비스/저장소에 작업을 요청한다",
				SymbolPath:   "AuthRepository.signup",
				EffectTarget: &target,
				Anchor: slicing.Anchor{
					RepoRelativePath:        "lib/features/auth/email_signup_notifier.dart",
					ByteRange:               [2]int{110, 150},
					FileHash:                fileHash,
					SpanHash:                spanHash,
					EnclosingSymbolPath:     "AuthRepository.signup",
					CanonicalAstFingerprint: astHash,
				},
			},
		},
		Edges: []slicing.SliceEdge{
			{
				Kind:             "boundary_call",
				ToSymbolPath:     "lib/features/auth/auth_repository.dart#AuthRepository.signup",
				ResolutionStatus: "resolved",
				Depth:            1,
			},
			{
				Kind:             "unknown_edge",
				ToSymbolPath:     "dynamic_func()",
				ResolutionStatus: "unresolved_dynamic",
				Depth:            1,
			},
		},
	}

	// 1. Baseline Fusion: Derived provenance
	spec1, err := fusion.Fuse(sliced, fusion.FuseOptions{})
	if err != nil {
		t.Fatalf("Fuse baseline failed: %v", err)
	}

	if spec1.FlowID != fusion.ComputeFlowID(entry) {
		t.Errorf("got flowId %q, want %q", spec1.FlowID, fusion.ComputeFlowID(entry))
	}
	if len(spec1.Steps) != 3 {
		t.Fatalf("expected 3 steps, got %d", len(spec1.Steps))
	}
	if spec1.Steps[0].Provenance != "derived" {
		t.Errorf("step 0 provenance = %q, want 'derived'", spec1.Steps[0].Provenance)
	}
	if len(spec1.Unknowns) != 1 || spec1.Unknowns[0].Reason != "unresolved_dynamic_call" {
		t.Errorf("unknowns not preserved: %+v", spec1.Unknowns)
	}

	// 2. Fusion with E2 session drafts and E3 approved ledger
	opts := fusion.FuseOptions{
		SessionDrafts: map[string]fusion.SessionDraftStep{
			"EmailSignupNotifier.submit": {
				Name:  "사용자 입력값 사전 검증 및 상태 초기화",
				Rules: []string{"이메일 주소에 '@' 기호가 포함되어 있어야 함"},
			},
		},
		ApprovedLedger: map[string]fusion.ApprovedStep{
			"AuthRepository.signup": {
				Name:       "회원가입 API 호출 승인",
				Rules:      []string{"신규 계정 발급 후 인증 토큰 반환"},
				ApprovedAt: time.Now().UTC(),
			},
		},
	}

	spec2, err := fusion.Fuse(sliced, opts)
	if err != nil {
		t.Fatalf("Fuse with E2/E3 failed: %v", err)
	}

	// Step 1 had Session draft -> provenance = "session"
	if spec2.Steps[0].Provenance != "session" || spec2.Steps[0].Name != "사용자 입력값 사전 검증 및 상태 초기화" {
		t.Errorf("step 0 failed session authority: %+v", spec2.Steps[0])
	}
	if len(spec2.Steps[0].Rules) != 1 {
		t.Errorf("step 0 rules missing: %+v", spec2.Steps[0].Rules)
	}

	// Step 3 had Approved step -> provenance = "approved" (highest authority)
	if spec2.Steps[2].Provenance != "approved" || spec2.Steps[2].Name != "회원가입 API 호출 승인" {
		t.Errorf("step 2 failed approved authority: %+v", spec2.Steps[2])
	}

	// Invariant check: All 3 structural steps still exist, none deleted
	if len(spec2.Steps) != 3 {
		t.Errorf("E1 structural step count invariant violated: got %d, want 3", len(spec2.Steps))
	}
}

func TestEventLogAppendAndMaterialize(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "codeflow-eventlog-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	el := fusion.NewEventLog(tmpDir)

	// Append Session Draft
	err = el.Append(fusion.Event{
		Type:       fusion.EventSessionDraftSubmitted,
		FlowID:     "flow-1234567890abcdef",
		SymbolPath: "EmailSignupNotifier.submit",
		Name:       "Agent Suggested Title",
		Rules:      []string{"Rule A"},
		Author:     "agent-01",
	})
	if err != nil {
		t.Fatalf("Append session draft failed: %v", err)
	}

	// Append Step Approved
	err = el.Append(fusion.Event{
		Type:       fusion.EventStepApproved,
		FlowID:     "flow-1234567890abcdef",
		SymbolPath: "AuthRepository.signup",
		Name:       "Human Approved Title",
		Rules:      []string{"Approved Rule 1"},
		Author:     "human-reviewer",
	})
	if err != nil {
		t.Fatalf("Append approved step failed: %v", err)
	}

	approved, session, err := el.MaterializeView()
	if err != nil {
		t.Fatalf("MaterializeView failed: %v", err)
	}

	if len(session) != 1 || session["EmailSignupNotifier.submit"].Name != "Agent Suggested Title" {
		t.Errorf("session map mismatch: %+v", session)
	}
	if len(approved) != 1 || approved["AuthRepository.signup"].Name != "Human Approved Title" {
		t.Errorf("approved map mismatch: %+v", approved)
	}
}

func TestFuseCarriesDescription(t *testing.T) {
	sliced := &slicing.SlicedPayload{
		CandidateID:     "cand-desc-000001",
		Language:        "dart",
		EntrySymbolPath: "lib/src/sample.dart#Sample.submit",
		Steps: []slicing.SliceStep{{Ordinal: 1, Kind: "mutation", Description: "상태를 갱신한다", SymbolPath: "Sample.submit", Anchor: slicing.Anchor{RepoRelativePath: "lib/src/sample.dart", ByteRange: [2]int{0, 10}, FileHash: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", SpanHash: "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210", EnclosingSymbolPath: "Sample.submit", CanonicalAstFingerprint: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}},
	}
	spec, err := fusion.Fuse(sliced, fusion.FuseOptions{CustomDescription: "이메일 인증 후 세션을 생성한다."})
	if err != nil { t.Fatalf("Fuse: %v", err) }
	if spec.Description != "이메일 인증 후 세션을 생성한다." {
		t.Errorf("description = %q", spec.Description)
	}
	// empty description omitted via omitempty — Marshal should not emit field when empty
	spec2, _ := fusion.Fuse(sliced, fusion.FuseOptions{})
	if spec2.Description != "" {
		t.Errorf("empty description must stay empty, got %q", spec2.Description)
	}
}
