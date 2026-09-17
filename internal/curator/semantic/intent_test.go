package semantic

import (
	"encoding/json"
	"strings"
	"testing"

	"codeflow/internal/collector/contractharness"
)

// TestVS02A1_TaskIntentCreation verifies criterion VS02-A1:
// WHEN 사용자가 feature request를 제출하면, THE system SHALL immutable rawRequest,
// normalized intent, acceptance criteria와 intentStatus를 분리한 Task Intent를 생성한다.
func TestVS02A1_TaskIntentCreation(t *testing.T) {
	rawInput := "사용자 이메일 회원가입 흐름을 분석해줘"

	intent, err := NormalizeTaskIntent(rawInput, IntentOptions{
		Mode: "feature",
	})
	if err != nil {
		t.Fatalf("NormalizeTaskIntent failed: %v", err)
	}

	// 1. Immutable rawRequest must be preserved exactly
	if intent.Request.RawRequest != rawInput {
		t.Errorf("rawRequest mismatch: got %q, want %q", intent.Request.RawRequest, rawInput)
	}

	// 2. Normalized intent must be present and distinct
	if intent.NormalizedIntent.ExpectedOutcome == "" {
		t.Error("normalized expectedOutcome must not be empty")
	}

	// 3. Acceptance criteria must be present
	if len(intent.AcceptanceCriteria) == 0 {
		t.Error("expected at least one acceptance criterion")
	}

	// 4. Lifecycle: default parsed, needs_confirmation when unresolved interpretations present
	if intent.IntentStatus != "parsed" {
		t.Errorf("expected intentStatus 'parsed', got %q", intent.IntentStatus)
	}

	// 5. Schema validation via contractharness
	data, err := json.Marshal(intent)
	if err != nil {
		t.Fatalf("marshal intent: %v", err)
	}
	if err := contractharness.ValidateTaskIntent(data); err != nil {
		t.Fatalf("task intent failed schema validation: %v", err)
	}

	// 6. Empty request rejection
	_, err = NormalizeTaskIntent("", IntentOptions{Mode: "feature"})
	if err == nil {
		t.Error("expected error for empty raw request")
	}

	// 7. Ambiguity marks needs_confirmation
	ambiguousInput := "회원가입인지 로그인인지 둘 중 하나 흐름 보여줘"
	ambIntent, err := NormalizeTaskIntent(ambiguousInput, IntentOptions{
		Mode: "feature",
	})
	if err != nil {
		t.Fatalf("ambiguous NormalizeTaskIntent failed: %v", err)
	}
	if ambIntent.IntentStatus != "needs_confirmation" {
		t.Errorf("expected ambiguous request to have intentStatus 'needs_confirmation', got %q", ambIntent.IntentStatus)
	}
	if len(ambIntent.NormalizedIntent.UnresolvedInterpretations) == 0 {
		t.Error("expected unresolvedInterpretations to be recorded")
	}
}

func TestVS04A11_TaskIntentPreservesRawAndRequiresExplicitConfirmation(t *testing.T) {
	raw := "  회원가입인지 로그인인지 흐름을 보여줘\n"
	intent, err := NormalizeTaskIntent(raw, IntentOptions{Mode: "feature"})
	if err != nil {
		t.Fatal(err)
	}
	if intent.Request.RawRequest != raw {
		t.Fatalf("raw request was normalized: got %q want %q", intent.Request.RawRequest, raw)
	}
	if intent.NormalizedIntent.ExpectedOutcome == raw || intent.NormalizedIntent.ExpectedOutcome != strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(raw), "보여줘")) {
		t.Fatalf("normalized outcome must be trimmed independently: got %q", intent.NormalizedIntent.ExpectedOutcome)
	}
	if intent.IntentStatus != "needs_confirmation" {
		t.Fatalf("ambiguous intent status = %q", intent.IntentStatus)
	}

	before, err := json.Marshal(intent)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ConfirmTaskIntent(intent, "", ""); err == nil {
		t.Fatal("confirmation without an explicit candidate scope must not be accepted")
	}

	confirmed, err := ConfirmTaskIntent(intent, "flow-signup", "auth")
	if err != nil {
		t.Fatal(err)
	}
	if confirmed == intent || confirmed.Revision != intent.Revision+1 {
		t.Fatalf("confirmation did not create an immutable next revision: got revision %d", confirmed.Revision)
	}
	if confirmed.IntentStatus != "user_confirmed" || confirmed.ScopeHints == nil || len(confirmed.ScopeHints.EntrySymbols) != 1 || confirmed.ScopeHints.EntrySymbols[0] != "flow-signup" {
		t.Fatalf("confirmation did not bind the selected scope: %+v", confirmed)
	}
	after, err := json.Marshal(intent)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) || intent.IntentStatus != "needs_confirmation" {
		t.Fatal("confirming a revision mutated the prior intent")
	}

	revised, err := ReviseTaskIntent(intent, "  로그인 흐름만 보여줘  ", IntentOptions{Mode: "feature"})
	if err != nil {
		t.Fatal(err)
	}
	if revised.Revision != intent.Revision+1 || revised.Request.RawRequest != "  로그인 흐름만 보여줘  " {
		t.Fatalf("revision did not preserve exact new raw request: %+v", revised)
	}
	if intent.Request.RawRequest != raw || intent.Revision != 1 {
		t.Fatal("revision mutated the prior intent")
	}
}
