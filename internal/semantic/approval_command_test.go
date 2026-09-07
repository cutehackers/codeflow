package semantic

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestBindApprovalCommandV2RejectsInvalidUTF8DraftFields(t *testing.T) {
	access := approvalCommandTestAccess(t)
	invalid := string([]byte{'v', 0xff, 'x'})
	tests := []struct {
		name   string
		mutate func(*ApprovalCommandDraft)
	}{
		{name: "command id", mutate: func(draft *ApprovalCommandDraft) { draft.CommandID = invalid }},
		{name: "proposal id", mutate: func(draft *ApprovalCommandDraft) { draft.ProposalID = invalid }},
		{name: "evidence pack id", mutate: func(draft *ApprovalCommandDraft) { draft.EvidencePackID = invalid }},
		{name: "computed basis id", mutate: func(draft *ApprovalCommandDraft) { draft.ComputedBasisID = invalid }},
		{name: "generation id", mutate: func(draft *ApprovalCommandDraft) { draft.GenerationID = invalid }},
		{name: "decision", mutate: func(draft *ApprovalCommandDraft) { draft.Decision = invalid }},
		{name: "idempotency key", mutate: func(draft *ApprovalCommandDraft) { draft.IdempotencyKey = invalid }},
		{name: "expected state", mutate: func(draft *ApprovalCommandDraft) { draft.ExpectedState = invalid }},
		{name: "edited text", mutate: func(draft *ApprovalCommandDraft) {
			draft.Decision = "edit_then_approve"
			draft.ExpectedState = "active"
			draft.ExpectedApprovalVersion = 1
			predecessor := "approval-previous"
			draft.PredecessorApprovalID = &predecessor
			draft.EditedText = &invalid
		}},
		{name: "predecessor", mutate: func(draft *ApprovalCommandDraft) {
			draft.Decision = "edit_then_approve"
			draft.ExpectedState = "active"
			draft.ExpectedApprovalVersion = 1
			edited := "edited semantic meaning"
			draft.EditedText = &edited
			draft.PredecessorApprovalID = &invalid
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			draft := approvalCommandTestDraft()
			test.mutate(&draft)
			validated, err := BindApprovalCommandV2(access, draft)
			assertApprovalCommandInvalid(t, err)
			if validated != nil {
				t.Fatalf("invalid UTF-8 %s unexpectedly returned a validated command", test.name)
			}
		})
	}
}

func TestParseAndBindApprovalCommandV2RejectsInvalidUTF8BytesBeforeReplacement(t *testing.T) {
	access := approvalCommandTestAccess(t)
	validated, err := BindApprovalCommandV2(access, approvalCommandTestDraft())
	if err != nil {
		t.Fatalf("BindApprovalCommandV2: %v", err)
	}
	data, err := json.Marshal(validated.Command())
	if err != nil {
		t.Fatalf("marshal command: %v", err)
	}
	invalidProposal := append([]byte(`"proposal-`), 0xff)
	invalidProposal = append(invalidProposal, []byte(`1"`)...)
	mutated := bytes.Replace(data, []byte(`"proposal-1"`), invalidProposal, 1)
	if bytes.Equal(mutated, data) {
		t.Fatal("invalid UTF-8 fixture did not modify proposalId")
	}
	parsed, err := ParseAndBindApprovalCommandV2(mutated, access)
	assertApprovalCommandInvalid(t, err)
	if parsed != nil {
		t.Fatal("raw invalid UTF-8 unexpectedly returned a validated command")
	}
}

func TestApprovalCommandV2PreservesUnicodeCodePointBounds(t *testing.T) {
	root := t.TempDir()
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("canonical test root: %v", err)
	}
	actorClaims := ApprovalActorClaims{ActorID: strings.Repeat("界", 256), SessionID: strings.Repeat("始", 256)}
	workspaceClaims := ApprovalWorkspaceClaims{WorkspaceID: strings.Repeat("承", 256), CanonicalRepoRoot: canonicalRoot}
	access, err := NewApprovalAccessGate(
		approvalAuthenticatorFunc(func(context.Context) (ApprovalActorClaims, error) { return actorClaims, nil }),
		approvalAuthorizerFunc(func(context.Context, ApprovalActorClaims, string, string) (ApprovalWorkspaceClaims, error) {
			return workspaceClaims, nil
		}),
	).AuthenticateAndAuthorize(context.Background(), workspaceClaims.WorkspaceID, root)
	if err != nil {
		t.Fatalf("256-rune authority claims: %v", err)
	}
	draft := ApprovalCommandDraft{
		CommandID:               strings.Repeat("命", 256),
		ProposalID:              strings.Repeat("提", 256),
		EvidencePackID:          strings.Repeat("包", 256),
		ComputedBasisID:         strings.Repeat("基", 256),
		GenerationID:            strings.Repeat("生", 256),
		IntentRevision:          1,
		Decision:                "approve",
		IdempotencyKey:          strings.Repeat("幂", 256),
		ExpectedApprovalVersion: 0,
		ExpectedState:           "none",
	}
	validated, err := BindApprovalCommandV2(access, draft)
	if err != nil {
		t.Fatalf("256-rune command values: %v", err)
	}
	command := validated.Command()
	if command.ActorID != actorClaims.ActorID || command.SessionID != actorClaims.SessionID || command.WorkspaceID != workspaceClaims.WorkspaceID || command.CommandID != draft.CommandID || command.ProposalID != draft.ProposalID || command.EvidencePackID != draft.EvidencePackID || command.ComputedBasisID != draft.ComputedBasisID || command.GenerationID != draft.GenerationID || command.IdempotencyKey != draft.IdempotencyKey {
		t.Fatalf("256-rune command values changed: %#v", command)
	}
	raw, err := validated.MarshalJSON()
	if err != nil {
		t.Fatalf("marshal 256-rune command: %v", err)
	}
	parsed, err := ParseAndBindApprovalCommandV2(raw, access)
	if err != nil {
		t.Fatalf("parse 256-rune command: %v", err)
	}
	assertParsedApprovalCommand(t, parsed, command, access)
}

func TestBindApprovalCommandV2RejectsUnicodeCodePointOverflow(t *testing.T) {
	access := approvalCommandTestAccess(t)
	tooLong := strings.Repeat("界", 257)
	tooLongText := strings.Repeat("編", 4097)
	tests := []struct {
		name   string
		mutate func(*ApprovalCommandDraft)
	}{
		{name: "command id", mutate: func(draft *ApprovalCommandDraft) { draft.CommandID = tooLong }},
		{name: "proposal id", mutate: func(draft *ApprovalCommandDraft) { draft.ProposalID = tooLong }},
		{name: "evidence pack id", mutate: func(draft *ApprovalCommandDraft) { draft.EvidencePackID = tooLong }},
		{name: "computed basis id", mutate: func(draft *ApprovalCommandDraft) { draft.ComputedBasisID = tooLong }},
		{name: "generation id", mutate: func(draft *ApprovalCommandDraft) { draft.GenerationID = tooLong }},
		{name: "idempotency key", mutate: func(draft *ApprovalCommandDraft) { draft.IdempotencyKey = tooLong }},
		{name: "edited text", mutate: func(draft *ApprovalCommandDraft) {
			draft.Decision = "edit_then_approve"
			draft.ExpectedState = "active"
			draft.ExpectedApprovalVersion = 1
			predecessor := "approval-previous"
			draft.PredecessorApprovalID = &predecessor
			draft.EditedText = &tooLongText
		}},
		{name: "predecessor", mutate: func(draft *ApprovalCommandDraft) {
			draft.Decision = "edit_then_approve"
			draft.ExpectedState = "active"
			draft.ExpectedApprovalVersion = 1
			edited := "edited semantic meaning"
			draft.EditedText = &edited
			draft.PredecessorApprovalID = &tooLong
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			draft := approvalCommandTestDraft()
			test.mutate(&draft)
			validated, err := BindApprovalCommandV2(access, draft)
			assertApprovalCommandInvalid(t, err)
			if validated != nil {
				t.Fatalf("257-rune %s unexpectedly returned a validated command", test.name)
			}
		})
	}
}

func TestBindApprovalCommandV2BindsCoreAuthorityForEveryDecision(t *testing.T) {
	access := approvalCommandTestAccess(t)
	pred := "approval-previous"
	edited := "edited semantic meaning"
	tests := []struct {
		name            string
		decision        string
		expectedVersion int64
		expectedState   string
		predecessor     *string
		editedText      *string
	}{
		{name: "approve", decision: "approve", expectedState: "none"},
		{name: "edit_then_approve", decision: "edit_then_approve", expectedVersion: 1, expectedState: "active", predecessor: &pred, editedText: &edited},
		{name: "reject", decision: "reject", expectedState: "none"},
		{name: "revoke", decision: "revoke", expectedVersion: 1, expectedState: "active", predecessor: &pred},
		{name: "supersede", decision: "supersede", expectedVersion: 1, expectedState: "active", predecessor: &pred},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			validated, err := BindApprovalCommandV2(access, ApprovalCommandDraft{
				CommandID:               "command-" + test.name,
				ProposalID:              "proposal-1",
				EvidencePackID:          "pack-1",
				ComputedBasisID:         "basis-1",
				GenerationID:            "generation-1",
				IntentRevision:          1,
				Decision:                test.decision,
				EditedText:              test.editedText,
				IdempotencyKey:          "idempotency-" + test.name,
				ExpectedApprovalVersion: test.expectedVersion,
				ExpectedState:           test.expectedState,
				PredecessorApprovalID:   test.predecessor,
			})
			if err != nil {
				t.Fatalf("BindApprovalCommandV2: %v", err)
			}
			if err := validated.Validate(); err != nil {
				t.Fatalf("validated command failed self-validation: %v", err)
			}
			command := validated.Command()
			if command.SchemaID != ApprovalCommandV2SchemaID || command.SchemaVersion != 2 {
				t.Fatalf("schema identity = %q/%d, want %q/2", command.SchemaID, command.SchemaVersion, ApprovalCommandV2SchemaID)
			}
			if command.ActorID != access.Actor().ActorID || command.SessionID != access.Actor().SessionID || command.WorkspaceID != access.Workspace().WorkspaceID() {
				t.Fatalf("command authority does not match Core access: actor=%q session=%q workspace=%q", command.ActorID, command.SessionID, command.WorkspaceID)
			}
			if command.Decision != test.decision || command.ExpectedState != test.expectedState || command.ExpectedApprovalVersion != test.expectedVersion {
				t.Fatalf("command transition = %#v, want decision=%q state=%q version=%d", command, test.decision, test.expectedState, test.expectedVersion)
			}
		})
	}
}

func TestBindApprovalCommandV2DoesNotDefaultMissingDecision(t *testing.T) {
	access := approvalCommandTestAccess(t)
	draft := approvalCommandTestDraft()
	draft.Decision = ""
	_, err := BindApprovalCommandV2(access, draft)
	assertApprovalCommandInvalid(t, err)
}

func TestApprovalCommandV2StrictJSONRejectsAuthorityUnknownDuplicateAndTrailingInput(t *testing.T) {
	access := approvalCommandTestAccess(t)
	validated, err := BindApprovalCommandV2(access, approvalCommandTestDraft())
	if err != nil {
		t.Fatalf("BindApprovalCommandV2: %v", err)
	}
	data, err := json.Marshal(validated.Command())
	if err != nil {
		t.Fatalf("marshal command: %v", err)
	}

	duplicateActor := strings.TrimSuffix(string(data), "}") + `,"actorId":"another-actor"}`
	for _, test := range []struct {
		name string
		data []byte
	}{
		{name: "duplicate authority key", data: []byte(duplicateActor)},
		{name: "trailing document", data: append(append([]byte{}, data...), []byte("\n{}")...)},
		{name: "unknown field", data: append(append([]byte{}, data[:len(data)-1]...), []byte(`,"unknown":"value"}`)...)},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := ParseAndBindApprovalCommandV2(test.data, access)
			assertApprovalCommandInvalid(t, err)
		})
	}

	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"actorId", "sessionId", "workspaceId"} {
		mutated := cloneApprovalCommandDocument(document)
		mutated[field] = "caller-forged-" + field
		mutatedData, err := json.Marshal(mutated)
		if err != nil {
			t.Fatal(err)
		}
		_, err = ParseAndBindApprovalCommandV2(mutatedData, access)
		assertApprovalCommandInvalid(t, err)
		if strings.Contains(err.Error(), "caller-forged") {
			t.Fatalf("authority mismatch error exposed caller value: %v", err)
		}
	}
}

func TestApprovalCommandV2OptionalPresenceMatrix(t *testing.T) {
	access := approvalCommandTestAccess(t)
	pred := "approval-previous"
	edited := "edited semantic meaning"
	tests := []struct {
		name            string
		decision        string
		expectedState   string
		expectedVersion int64
		predecessor     *string
		editedText      *string
	}{
		{name: "approve", decision: "approve", expectedState: "none"},
		{name: "edit_then_approve", decision: "edit_then_approve", expectedState: "active", expectedVersion: 1, predecessor: &pred, editedText: &edited},
		{name: "reject", decision: "reject", expectedState: "none", predecessor: &pred},
		{name: "revoke", decision: "revoke", expectedState: "active", expectedVersion: 1, predecessor: &pred},
		{name: "supersede", decision: "supersede", expectedState: "active", expectedVersion: 1, predecessor: &pred},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			draft := approvalCommandTestDraft()
			draft.CommandID = "command-" + test.name
			draft.IdempotencyKey = "idempotency-" + test.name
			draft.Decision = test.decision
			draft.ExpectedState = test.expectedState
			draft.ExpectedApprovalVersion = test.expectedVersion
			draft.PredecessorApprovalID = test.predecessor
			draft.EditedText = test.editedText

			validated, err := BindApprovalCommandV2(access, draft)
			if err != nil {
				t.Fatalf("valid optional presence matrix entry rejected: %v", err)
			}
			expected := validated.Command()
			data, err := json.Marshal(expected)
			if err != nil {
				t.Fatal(err)
			}
			parsed, err := ParseAndBindApprovalCommandV2(data, access)
			if err != nil {
				t.Fatalf("valid raw optional presence matrix entry rejected: %v", err)
			}
			assertParsedApprovalCommand(t, parsed, expected, access)

			invalidDraft := draft
			empty := ""
			invalidDraft.EditedText = &empty
			_, err = BindApprovalCommandV2(access, invalidDraft)
			assertApprovalCommandTransitionInvalid(t, err)

			var document map[string]any
			if err := json.Unmarshal(data, &document); err != nil {
				t.Fatal(err)
			}
			document["editedText"] = ""
			invalidData, err := json.Marshal(document)
			if err != nil {
				t.Fatal(err)
			}
			_, err = ParseAndBindApprovalCommandV2(invalidData, access)
			assertApprovalCommandTransitionInvalid(t, err)
		})
	}
}

func TestApprovalCommandV2StrictJSONRejectsSchemaReferenceVersionAndTransitionMutations(t *testing.T) {
	access := approvalCommandTestAccess(t)
	validated, err := BindApprovalCommandV2(access, approvalCommandTestDraft())
	if err != nil {
		t.Fatalf("BindApprovalCommandV2: %v", err)
	}
	data, err := json.Marshal(validated.Command())
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name  string
		field string
		value any
	}{
		{name: "schema id", field: "schemaId", value: "https://codeflow.local/schemas/other.json"},
		{name: "schema version", field: "schemaVersion", value: 1},
		{name: "missing required decision", field: "decision", value: nil},
		{name: "negative intent revision", field: "intentRevision", value: -1},
		{name: "overflow approval version", field: "expectedApprovalVersion", value: float64(1000000001)},
		{name: "invalid transition state", field: "expectedState", value: "active"},
		{name: "missing proposal reference", field: "proposalId", value: ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mutated := cloneApprovalCommandDocument(document)
			if test.value == nil {
				delete(mutated, test.field)
			} else {
				mutated[test.field] = test.value
			}
			mutatedData, err := json.Marshal(mutated)
			if err != nil {
				t.Fatal(err)
			}
			_, err = ParseAndBindApprovalCommandV2(mutatedData, access)
			assertApprovalCommandInvalid(t, err)
		})
	}
	for _, test := range []struct {
		name  string
		value any
	}{
		{name: "null optional edited text", value: nil},
		{name: "wrong optional edited text type", value: 7},
	} {
		t.Run(test.name, func(t *testing.T) {
			mutated := cloneApprovalCommandDocument(document)
			mutated["editedText"] = test.value
			mutatedData, err := json.Marshal(mutated)
			if err != nil {
				t.Fatal(err)
			}
			_, err = ParseAndBindApprovalCommandV2(mutatedData, access)
			assertApprovalCommandInvalid(t, err)
		})
	}
}

func TestApprovalCommandV2DefensiveValueAndAccessValidation(t *testing.T) {
	access := approvalCommandTestAccess(t)
	validated, err := BindApprovalCommandV2(access, approvalCommandTestDraft())
	if err != nil {
		t.Fatalf("BindApprovalCommandV2: %v", err)
	}
	copyValue := validated.Command()
	copyValue.ActorID = "caller-forged"
	copyValue.ProposalID = "caller-proposal"
	copyValue.EditedText = approvalCommandStringPointer("caller-edited")
	if err := validated.Validate(); err != nil {
		t.Fatalf("mutating defensive command copy invalidated authority: %v", err)
	}
	original := validated.Command()
	if original.ActorID != access.Actor().ActorID || original.ProposalID == "caller-proposal" || original.EditedText != nil {
		t.Fatalf("command accessor leaked mutable state: %#v", original)
	}

	if _, err := BindApprovalCommandV2(ApprovalAccess{}, approvalCommandTestDraft()); err == nil {
		t.Fatal("forged zero ApprovalAccess unexpectedly bound a command")
	} else {
		assertApprovalCommandInvalid(t, err)
	}
	otherAccess := approvalCommandTestAccess(t)
	if _, err := BindApprovalCommandV2(ApprovalAccess{actor: access.actor, workspace: otherAccess.workspace}, approvalCommandTestDraft()); err == nil {
		t.Fatal("cross-gate forged ApprovalAccess unexpectedly bound a command")
	} else {
		assertApprovalCommandInvalid(t, err)
	}
}

func TestApprovalCommandV2ClonesOptionalInputsAndOutputs(t *testing.T) {
	access := approvalCommandTestAccess(t)
	originalEdited := "original edited semantic meaning"
	originalPredecessor := "original-approval-previous"
	draft := approvalCommandTestDraft()
	draft.Decision = "edit_then_approve"
	draft.ExpectedState = "active"
	draft.ExpectedApprovalVersion = 1
	draft.EditedText = &originalEdited
	draft.PredecessorApprovalID = &originalPredecessor
	validated, err := BindApprovalCommandV2(access, draft)
	if err != nil {
		t.Fatalf("BindApprovalCommandV2: %v", err)
	}

	originalEdited = "mutated after bind"
	originalPredecessor = "mutated after bind"
	assertApprovalCommandV2OptionalValues(t, validated, "original edited semantic meaning", "original-approval-previous")
	if err := validated.Validate(); err != nil {
		t.Fatalf("mutating original draft pointers invalidated validated command: %v", err)
	}
	if _, err := validated.MarshalJSON(); err != nil {
		t.Fatalf("mutating original draft pointers invalidated MarshalJSON: %v", err)
	}

	commandCopy := validated.Command()
	*commandCopy.EditedText = "mutated command copy"
	*commandCopy.PredecessorApprovalID = "mutated command copy"
	valueCopy := validated.Value()
	*valueCopy.EditedText = "mutated value copy"
	*valueCopy.PredecessorApprovalID = "mutated value copy"
	assertApprovalCommandV2OptionalValues(t, validated, "original edited semantic meaning", "original-approval-previous")
	if err := validated.Validate(); err != nil {
		t.Fatalf("mutating accessor pointers invalidated validated command: %v", err)
	}
	if _, err := validated.MarshalJSON(); err != nil {
		t.Fatalf("mutating accessor pointers invalidated MarshalJSON: %v", err)
	}
}

func TestApprovalCommandV2RejectsEveryInternalTamper(t *testing.T) {
	const sensitive = "approval-command-tamper-secret"
	tests := []struct {
		name   string
		mutate func(*ValidatedApprovalCommandV2)
	}{
		{name: "schema id", mutate: func(v *ValidatedApprovalCommandV2) { v.command.SchemaID = sensitive }},
		{name: "schema version", mutate: func(v *ValidatedApprovalCommandV2) { v.command.SchemaVersion = 99 }},
		{name: "command id", mutate: func(v *ValidatedApprovalCommandV2) { v.command.CommandID = sensitive }},
		{name: "actor id", mutate: func(v *ValidatedApprovalCommandV2) { v.command.ActorID = sensitive }},
		{name: "session id", mutate: func(v *ValidatedApprovalCommandV2) { v.command.SessionID = sensitive }},
		{name: "workspace id", mutate: func(v *ValidatedApprovalCommandV2) { v.command.WorkspaceID = sensitive }},
		{name: "proposal id", mutate: func(v *ValidatedApprovalCommandV2) { v.command.ProposalID = sensitive }},
		{name: "evidence pack id", mutate: func(v *ValidatedApprovalCommandV2) { v.command.EvidencePackID = sensitive }},
		{name: "computed basis id", mutate: func(v *ValidatedApprovalCommandV2) { v.command.ComputedBasisID = sensitive }},
		{name: "generation id", mutate: func(v *ValidatedApprovalCommandV2) { v.command.GenerationID = sensitive }},
		{name: "intent revision", mutate: func(v *ValidatedApprovalCommandV2) { v.command.IntentRevision = 2 }},
		{name: "decision", mutate: func(v *ValidatedApprovalCommandV2) { v.command.Decision = "approve" }},
		{name: "edited text pointee", mutate: func(v *ValidatedApprovalCommandV2) { *v.command.EditedText = sensitive }},
		{name: "idempotency key", mutate: func(v *ValidatedApprovalCommandV2) { v.command.IdempotencyKey = sensitive }},
		{name: "expected approval version", mutate: func(v *ValidatedApprovalCommandV2) { v.command.ExpectedApprovalVersion = 2 }},
		{name: "expected state", mutate: func(v *ValidatedApprovalCommandV2) { v.command.ExpectedState = "none" }},
		{name: "predecessor pointee", mutate: func(v *ValidatedApprovalCommandV2) { *v.command.PredecessorApprovalID = sensitive }},
		{name: "access actor id", mutate: func(v *ValidatedApprovalCommandV2) { v.access.actor.ActorID = sensitive }},
		{name: "access session id", mutate: func(v *ValidatedApprovalCommandV2) { v.access.actor.SessionID = sensitive }},
		{name: "access workspace id", mutate: func(v *ValidatedApprovalCommandV2) { v.access.workspace.workspaceID = sensitive }},
		{name: "actor seal id", mutate: func(v *ValidatedApprovalCommandV2) { v.access.actor.seal.actorID = sensitive }},
		{name: "actor seal session", mutate: func(v *ValidatedApprovalCommandV2) { v.access.actor.seal.sessionID = sensitive }},
		{name: "actor seal gate", mutate: func(v *ValidatedApprovalCommandV2) { v.access.actor.seal.gate = &ApprovalAccessGate{} }},
		{name: "workspace seal root", mutate: func(v *ValidatedApprovalCommandV2) { v.access.workspace.seal.repoRoot = sensitive }},
		{name: "workspace seal id", mutate: func(v *ValidatedApprovalCommandV2) { v.access.workspace.seal.workspaceID = sensitive }},
		{name: "workspace seal actor", mutate: func(v *ValidatedApprovalCommandV2) { v.access.workspace.seal.actor.ActorID = sensitive }},
		{name: "workspace seal gate", mutate: func(v *ValidatedApprovalCommandV2) { v.access.workspace.seal.gate = &ApprovalAccessGate{} }},
		{name: "command digest", mutate: func(v *ValidatedApprovalCommandV2) { v.digest[0] ^= 0xff }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			validated := newApprovalCommandWithOptionals(t)
			test.mutate(validated)
			assertTamperedApprovalCommandRejected(t, validated, sensitive)
		})
	}
}

func TestApprovalCommandV2NilReceiverFailsClosed(t *testing.T) {
	var validated *ValidatedApprovalCommandV2
	assertApprovalCommandInvalid(t, validated.Validate())
	if got := validated.Command(); !reflect.DeepEqual(got, ApprovalCommandV2{}) {
		t.Fatalf("nil receiver Command = %#v, want zero command", got)
	}
	if got := validated.Value(); !reflect.DeepEqual(got, ApprovalCommandV2{}) {
		t.Fatalf("nil receiver Value = %#v, want zero command", got)
	}
	if got := validated.Access(); !reflect.DeepEqual(got, ApprovalAccess{}) {
		t.Fatalf("nil receiver Access = %#v, want zero access", got)
	}
	_, err := validated.MarshalJSON()
	assertApprovalCommandInvalid(t, err)
}

func newApprovalCommandWithOptionals(t *testing.T) *ValidatedApprovalCommandV2 {
	t.Helper()
	access := approvalCommandTestAccess(t)
	edited := "original edited semantic meaning"
	predecessor := "original-approval-previous"
	draft := approvalCommandTestDraft()
	draft.Decision = "edit_then_approve"
	draft.ExpectedState = "active"
	draft.ExpectedApprovalVersion = 1
	draft.EditedText = &edited
	draft.PredecessorApprovalID = &predecessor
	validated, err := BindApprovalCommandV2(access, draft)
	if err != nil {
		t.Fatalf("BindApprovalCommandV2: %v", err)
	}
	return validated
}

func assertApprovalCommandV2OptionalValues(t *testing.T, validated *ValidatedApprovalCommandV2, edited, predecessor string) {
	t.Helper()
	command := validated.Command()
	if command.EditedText == nil || *command.EditedText != edited {
		t.Fatalf("editedText = %#v, want %q", command.EditedText, edited)
	}
	if command.PredecessorApprovalID == nil || *command.PredecessorApprovalID != predecessor {
		t.Fatalf("predecessorApprovalId = %#v, want %q", command.PredecessorApprovalID, predecessor)
	}
}

func assertTamperedApprovalCommandRejected(t *testing.T, validated *ValidatedApprovalCommandV2, sensitive string) {
	t.Helper()
	err := validated.Validate()
	assertApprovalCommandInvalid(t, err)
	if strings.Contains(err.Error(), sensitive) {
		t.Fatalf("tamper validation error leaked sensitive value: %v", err)
	}
	_, err = validated.MarshalJSON()
	assertApprovalCommandInvalid(t, err)
	if strings.Contains(err.Error(), sensitive) {
		t.Fatalf("tamper MarshalJSON error leaked sensitive value: %v", err)
	}
}

func TestApprovalCommandV2HonorsNumericBounds(t *testing.T) {
	access := approvalCommandTestAccess(t)
	draft := approvalCommandTestDraft()
	draft.IntentRevision = 1000000
	draft.Decision = "reject"
	draft.ExpectedState = "active"
	draft.ExpectedApprovalVersion = 1000000000
	if _, err := BindApprovalCommandV2(access, draft); err != nil {
		t.Fatalf("maximum in-range command values rejected: %v", err)
	}
	for _, test := range []struct {
		name  string
		field string
		value int64
	}{
		{name: "zero intent revision", field: "intent", value: 0},
		{name: "negative intent revision", field: "intent", value: -1},
		{name: "intent revision overflow", field: "intent", value: 1000001},
		{name: "negative approval version", field: "version", value: -1},
		{name: "approval version overflow", field: "version", value: 1000000001},
	} {
		t.Run(test.name, func(t *testing.T) {
			mutated := draft
			if test.field == "intent" {
				mutated.IntentRevision = test.value
			} else {
				mutated.ExpectedApprovalVersion = test.value
			}
			_, err := BindApprovalCommandV2(access, mutated)
			assertApprovalCommandInvalid(t, err)
		})
	}
}

func TestApprovalCommandV2ConcurrentValidationIsStable(t *testing.T) {
	access := approvalCommandTestAccess(t)
	validated, err := BindApprovalCommandV2(access, approvalCommandTestDraft())
	if err != nil {
		t.Fatalf("BindApprovalCommandV2: %v", err)
	}
	const workers = 32
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := validated.Validate(); err != nil {
				errs <- err
				return
			}
			value := validated.Value()
			value.CommandID = "worker-mutated"
			errs <- nil
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent validation error: %v", err)
		}
	}
}

func approvalCommandTestAccess(t *testing.T) ApprovalAccess {
	t.Helper()
	root := t.TempDir()
	authorizer := NewApprovalWorkspaceAuthorizer(root)
	access, err := NewApprovalAccessGate(NewLocalProcessApprovalAuthenticator(), authorizer).AuthenticateAndAuthorize(context.Background(), authorizer.WorkspaceID(), root)
	if err != nil {
		t.Fatalf("create approval access: %v", err)
	}
	return access
}

func approvalCommandTestDraft() ApprovalCommandDraft {
	return ApprovalCommandDraft{
		CommandID:               "command-1",
		ProposalID:              "proposal-1",
		EvidencePackID:          "pack-1",
		ComputedBasisID:         "basis-1",
		GenerationID:            "generation-1",
		IntentRevision:          1,
		Decision:                "approve",
		IdempotencyKey:          "idempotency-1",
		ExpectedApprovalVersion: 0,
		ExpectedState:           "none",
	}
}

func cloneApprovalCommandDocument(document map[string]any) map[string]any {
	data, _ := json.Marshal(document)
	var clone map[string]any
	_ = json.Unmarshal(data, &clone)
	return clone
}

func approvalCommandStringPointer(value string) *string { return &value }

func assertApprovalCommandInvalid(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected approval command validation error")
	}
	if !errors.Is(err, ErrApprovalCommandInvalid) {
		t.Fatalf("error = %v, want ErrApprovalCommandInvalid", err)
	}
	var typed *ApprovalCommandError
	if !errors.As(err, &typed) {
		t.Fatalf("error = %v, want typed ApprovalCommandError", err)
	}
}

func assertApprovalCommandTransitionInvalid(t *testing.T, err error) {
	t.Helper()
	assertApprovalCommandInvalid(t, err)
	var typed *ApprovalCommandError
	if !errors.As(err, &typed) || typed.Kind != "transition" {
		t.Fatalf("error = %v, want transition ApprovalCommandError", err)
	}
}

func assertParsedApprovalCommand(t *testing.T, parsed *ValidatedApprovalCommandV2, expected ApprovalCommandV2, access ApprovalAccess) {
	t.Helper()
	if parsed == nil {
		t.Fatal("valid raw command returned a nil validated result without an error")
	}
	if err := parsed.Validate(); err != nil {
		t.Fatalf("parsed command failed validation: %v", err)
	}
	got := parsed.Command()
	// DeepEqual compares pointed-to values as well as nil/non-nil presence, so
	// this catches optional-field loss without requiring pointer identity.
	if !reflect.DeepEqual(got, expected) {
		t.Fatalf("parsed command changed serialized source: got %#v want %#v", got, expected)
	}
	if !reflect.DeepEqual(parsed.Access(), access) {
		t.Fatalf("parsed command access does not match supplied Core access")
	}
}
