package semantic

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

const validApprovalCommandDraftJSON = `{"commandId":"command-draft","proposalId":"proposal-1","evidencePackId":"pack-1","computedBasisId":"basis-1","generationId":"generation-1","intentRevision":1,"decision":"approve","idempotencyKey":"idempotency-draft","expectedApprovalVersion":0,"expectedState":"none"}`

func TestParseApprovalCommandDraftJSONStrict(t *testing.T) {
	valid := []byte(validApprovalCommandDraftJSON)
	tests := []struct {
		name string
		data []byte
	}{
		{name: "duplicate field", data: []byte(strings.Replace(validApprovalCommandDraftJSON, `"decision":"approve"`, `"decision":"approve","decision":"approve"`, 1))},
		{name: "duplicate nested field", data: []byte(strings.Replace(validApprovalCommandDraftJSON, `"decision":"approve"`, `"decision":{"nested":1,"nested":2}`, 1))},
		{name: "unknown field", data: []byte(strings.TrimSuffix(validApprovalCommandDraftJSON, "}") + `,"unexpected":true}`)},
		{name: "trailing document", data: append(append([]byte{}, valid...), []byte(` {}`)...)},
		{name: "missing zero-valued version", data: []byte(strings.Replace(validApprovalCommandDraftJSON, `,"expectedApprovalVersion":0`, "", 1))},
		{name: "numeric type mismatch", data: []byte(strings.Replace(validApprovalCommandDraftJSON, `"intentRevision":1`, `"intentRevision":"1"`, 1))},
		{name: "authority field", data: []byte(strings.TrimSuffix(validApprovalCommandDraftJSON, "}") + `,"actorId":"caller"}`)},
		{name: "optional null", data: []byte(strings.TrimSuffix(validApprovalCommandDraftJSON, "}") + `,"editedText":null}`)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			draft, err := ParseApprovalCommandDraftJSON(test.data)
			if !errors.Is(err, ErrApprovalCommandInvalid) {
				t.Fatalf("draft=%+v err=%v, want ErrApprovalCommandInvalid", draft, err)
			}
		})
	}

	invalidUTF8 := append([]byte(`{"commandId":"command-`), 0xff)
	invalidUTF8 = append(invalidUTF8, []byte(`","proposalId":"proposal-1"}`)...)
	if _, err := ParseApprovalCommandDraftJSON(invalidUTF8); !errors.Is(err, ErrApprovalCommandInvalid) {
		t.Fatalf("invalid UTF-8 error = %v, want ErrApprovalCommandInvalid", err)
	}

	draft, err := ParseApprovalCommandDraftJSON(valid)
	if err != nil {
		t.Fatalf("valid draft parse: %v", err)
	}
	if draft.ExpectedApprovalVersion != 0 || draft.CommandID != "command-draft" || draft.Decision != "approve" {
		t.Fatalf("valid draft = %+v, want zero expected version and exact fields", draft)
	}
	access := approvalCommandTestAccess(t)
	bound, err := BindApprovalCommandV2(access, draft)
	if err != nil {
		t.Fatalf("valid parsed draft did not bind: %v", err)
	}
	if err := bound.Validate(); err != nil {
		t.Fatalf("bound parsed draft failed authoritative validation: %v", err)
	}
}

func TestParseApprovalCommandDraftEnvelopeJSONStrict(t *testing.T) {
	data := []byte(strings.TrimSuffix(validApprovalCommandDraftJSON, "}") + `,"target":".","token":"mcp-token"}`)
	envelope, err := ParseApprovalCommandDraftEnvelopeJSON(data)
	if err != nil {
		t.Fatalf("valid envelope parse: %v", err)
	}
	if envelope.Draft.CommandID != "command-draft" || envelope.Target == nil || *envelope.Target != "." || envelope.Token == nil || *envelope.Token != "mcp-token" {
		t.Fatalf("envelope = %+v, want exact draft and transport metadata", envelope)
	}

	for _, data := range [][]byte{
		[]byte(strings.TrimSuffix(validApprovalCommandDraftJSON, "}") + `,"target":7}`),
		[]byte(strings.TrimSuffix(validApprovalCommandDraftJSON, "}") + `,"token":null}`),
		[]byte(strings.TrimSuffix(validApprovalCommandDraftJSON, "}") + `,"unexpected":true}`),
	} {
		if _, err := ParseApprovalCommandDraftEnvelopeJSON(data); !errors.Is(err, ErrApprovalCommandInvalid) {
			t.Fatalf("invalid envelope %s error = %v, want ErrApprovalCommandInvalid", data, err)
		}
	}

	// Re-encoding a parsed draft must preserve the optional-field presence
	// semantics. In particular, an explicit zero expected version remains a
	// required field rather than being treated as absent.
	edited := "edited"
	draft := ApprovalCommandDraft{
		CommandID: "command-draft", ProposalID: "proposal-1", EvidencePackID: "pack-1",
		ComputedBasisID: "basis-1", GenerationID: "generation-1", IntentRevision: 1,
		Decision: "edit_then_approve", EditedText: &edited, IdempotencyKey: "idempotency-draft",
		ExpectedApprovalVersion: 1, ExpectedState: "active",
	}
	raw, err := json.Marshal(draft)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseApprovalCommandDraftJSON(raw)
	if err != nil {
		t.Fatalf("optional draft parse: %v", err)
	}
	if parsed.EditedText == nil || *parsed.EditedText != edited {
		t.Fatalf("parsed optional edited text = %+v, want %q", parsed.EditedText, edited)
	}
	if bytes.Equal(raw, []byte(validApprovalCommandDraftJSON)) {
		t.Fatal("optional draft unexpectedly equal to approve fixture")
	}
}
