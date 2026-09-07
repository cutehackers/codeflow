package semantic

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"codeflow/internal/contractharness"
)

func TestMarshalRedactedApprovalEventAuditEmitsOnlyValidatedEvent(t *testing.T) {
	command, stored, before, after, event, aggregate := approvalMeaningMutationTestInputs(t)
	validated, err := ValidateApprovalMeaningMutation(command, stored, before, after, event, aggregate)
	if err != nil {
		t.Fatalf("valid mutation: %v", err)
	}
	raw, err := MarshalRedactedApprovalEventAudit(validated)
	if err != nil {
		t.Fatalf("marshal redacted approval event audit: %v", err)
	}
	if err := contractharness.ValidateVS09Contract(ApprovalEventV2SchemaID, raw); err != nil {
		t.Fatalf("redacted audit failed approval-event contract: %v", err)
	}
	var got ApprovalEventV2
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode redacted audit: %v", err)
	}
	if want := validated.Event(); want == nil || got != *want {
		t.Fatalf("redacted audit event = %#v, want %#v", got, want)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("decode redacted audit fields: %v", err)
	}
	forbiddenFields := []string{"proposal", "pack", "packDigest", "evidence", "rationale", "command", "idempotencyKey", "aggregate", "history"}
	for _, field := range forbiddenFields {
		if _, ok := fields[field]; ok {
			t.Fatalf("redacted audit exposed forbidden field %q: %s", field, raw)
		}
	}
	for _, forbiddenValue := range []string{"idempotency-1", "checkout.go", "e-a03", "func Submit"} {
		if strings.Contains(string(raw), forbiddenValue) {
			t.Fatalf("redacted audit exposed forbidden value %q: %s", forbiddenValue, raw)
		}
	}
	if _, err := json.Marshal(validated); err == nil {
		t.Fatal("validated mutation unexpectedly exposed a direct JSON payload")
	} else {
		assertApprovalMeaningInvalid(t, err)
	}
}

func TestMarshalRedactedApprovalEventAuditRejectsRedactedIdentity(t *testing.T) {
	tests := []struct {
		name    string
		actor   string
		session string
		workID  string
	}{
		{name: "actor", actor: `apiKey: "secret-actor"`},
		{name: "session", session: `token: "secret-session"`},
		{name: "workspace", workID: `password: "secret-workspace"`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			access := approvalAuditAccess(t, test.actor, test.session, test.workID)
			command, stored, before, after, event, aggregate := approvalMeaningMutationTestInputsForDecisionWithAccess(t, "approve", access)
			validated, err := ValidateApprovalMeaningMutation(command, stored, before, after, event, aggregate)
			if err != nil {
				t.Fatalf("valid secret-identity mutation: %v", err)
			}
			raw, err := MarshalRedactedApprovalEventAudit(validated)
			if err == nil {
				t.Fatalf("redacted identity unexpectedly emitted audit: %s", raw)
			}
			assertApprovalMeaningInvalid(t, err)
			for _, secretValue := range []string{test.actor, test.session, test.workID} {
				if secretValue != "" && strings.Contains(err.Error(), secretValue) {
					t.Fatalf("audit error leaked identity value: %v", err)
				}
			}
		})
	}
}

func TestMarshalRedactedApprovalEventAuditRejectsNilUnsealedAndTamperedMutation(t *testing.T) {
	if raw, err := MarshalRedactedApprovalEventAudit(nil); err == nil || raw != nil {
		t.Fatalf("nil mutation audit = %s/%v, want bounded failure", raw, err)
	} else {
		assertApprovalMeaningInvalid(t, err)
	}

	command, stored, before, after, event, aggregate := approvalMeaningMutationTestInputs(t)
	validated, err := ValidateApprovalMeaningMutation(command, stored, before, after, event, aggregate)
	if err != nil {
		t.Fatalf("valid mutation: %v", err)
	}
	validated.sealed = false
	if raw, err := MarshalRedactedApprovalEventAudit(validated); err == nil || raw != nil {
		t.Fatalf("unsealed mutation audit = %s/%v, want bounded failure", raw, err)
	} else {
		assertApprovalMeaningInvalid(t, err)
	}
	validated.sealed = true
	validated.event.ActorID = "tampered-actor"
	if raw, err := MarshalRedactedApprovalEventAudit(validated); err == nil || raw != nil {
		t.Fatalf("tampered mutation audit = %s/%v, want bounded failure", raw, err)
	} else {
		assertApprovalMeaningInvalid(t, err)
	}
	if _, err := json.Marshal(validated); err == nil {
		t.Fatal("tampered mutation unexpectedly exposed direct JSON")
	}
}

func approvalAuditAccess(t *testing.T, actorID, sessionID, workspaceID string) ApprovalAccess {
	t.Helper()
	root := t.TempDir()
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("canonical audit root: %v", err)
	}
	if actorID == "" {
		actorID = "actor-audit"
	}
	if sessionID == "" {
		sessionID = "session-audit"
	}
	if workspaceID == "" {
		workspaceID = "workspace-audit"
	}
	actor := ApprovalActorClaims{ActorID: actorID, SessionID: sessionID}
	workspace := ApprovalWorkspaceClaims{WorkspaceID: workspaceID, CanonicalRepoRoot: canonicalRoot}
	access, err := NewApprovalAccessGate(
		approvalAuthenticatorFunc(func(context.Context) (ApprovalActorClaims, error) { return actor, nil }),
		approvalAuthorizerFunc(func(context.Context, ApprovalActorClaims, string, string) (ApprovalWorkspaceClaims, error) {
			return workspace, nil
		}),
	).AuthenticateAndAuthorize(context.Background(), workspaceID, root)
	if err != nil {
		t.Fatalf("audit access: %v", err)
	}
	return access
}
