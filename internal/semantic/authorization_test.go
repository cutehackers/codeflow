package semantic

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestLocalProcessApprovalAuthenticatorStableOSPrincipal(t *testing.T) {
	principal := func() (string, error) { return "uid:fixture-principal", nil }
	first := newLocalProcessApprovalAuthenticator(principal, func() (string, error) {
		return "session-fixture-one", nil
	})
	second := newLocalProcessApprovalAuthenticator(principal, func() (string, error) {
		return "session-fixture-two", nil
	})
	firstClaims, err := first.Authenticate(context.Background())
	if err != nil {
		t.Fatalf("first fixture authentication: %v", err)
	}
	secondClaims, err := second.Authenticate(context.Background())
	if err != nil {
		t.Fatalf("second fixture authentication: %v", err)
	}
	if firstClaims.ActorID == "" || firstClaims.ActorID != secondClaims.ActorID {
		t.Fatalf("same OS principal actor IDs = %q/%q, want stable identity", firstClaims.ActorID, secondClaims.ActorID)
	}
	if firstClaims.SessionID == secondClaims.SessionID {
		t.Fatalf("different authenticator sessions reused %q", firstClaims.SessionID)
	}
	if got, err := first.Authenticate(context.Background()); err != nil || got != firstClaims {
		t.Fatalf("same authenticator claims = %+v/%v, want stable claims %+v", got, err, firstClaims)
	}

	different := newLocalProcessApprovalAuthenticator(func() (string, error) {
		return "uid:other-principal", nil
	}, func() (string, error) {
		return "session-fixture-three", nil
	})
	differentClaims, err := different.Authenticate(context.Background())
	if err != nil {
		t.Fatalf("different fixture authentication: %v", err)
	}
	if firstClaims.ActorID == differentClaims.ActorID {
		t.Fatalf("different OS principals share actor ID %q", firstClaims.ActorID)
	}
	if len(firstClaims.ActorID) > approvalActorIDMax || len(firstClaims.SessionID) > approvalSessionIDMax {
		t.Fatalf("fixture authority is unbounded: %+v", firstClaims)
	}
	if strings.ContainsAny(firstClaims.ActorID+firstClaims.SessionID, "/\\") {
		t.Fatalf("fixture authority contains path separator: %+v", firstClaims)
	}
}

func TestApprovalAccessGateUsesUnicodeCodePointBoundsAndRejectsInvalidUTF8(t *testing.T) {
	root := t.TempDir()
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("canonical test root: %v", err)
	}
	actorClaims := ApprovalActorClaims{ActorID: strings.Repeat("界", 256), SessionID: strings.Repeat("始", 256)}
	workspaceClaims := ApprovalWorkspaceClaims{WorkspaceID: strings.Repeat("承", 256), CanonicalRepoRoot: canonicalRoot}
	gate := NewApprovalAccessGate(
		approvalAuthenticatorFunc(func(context.Context) (ApprovalActorClaims, error) { return actorClaims, nil }),
		approvalAuthorizerFunc(func(context.Context, ApprovalActorClaims, string, string) (ApprovalWorkspaceClaims, error) {
			return workspaceClaims, nil
		}),
	)
	access, err := gate.AuthenticateAndAuthorize(context.Background(), workspaceClaims.WorkspaceID, root)
	if err != nil {
		t.Fatalf("256-rune authority claims rejected: %v", err)
	}
	if access.Actor().ActorID != actorClaims.ActorID || access.Actor().SessionID != actorClaims.SessionID || access.Workspace().WorkspaceID() != workspaceClaims.WorkspaceID {
		t.Fatalf("256-rune authority claims changed: access=%+v", access)
	}

	tooLongActor := strings.Repeat("界", 257)
	tooLongSession := strings.Repeat("始", 257)
	tooLongWorkspace := strings.Repeat("承", 257)
	for _, test := range []struct {
		name      string
		actor     ApprovalActorClaims
		workspace ApprovalWorkspaceClaims
		want      error
	}{
		{name: "invalid actor UTF-8", actor: ApprovalActorClaims{ActorID: string([]byte{'a', 0xff}), SessionID: actorClaims.SessionID}, workspace: workspaceClaims, want: ErrApprovalUnauthenticated},
		{name: "invalid session UTF-8", actor: ApprovalActorClaims{ActorID: actorClaims.ActorID, SessionID: string([]byte{'s', 0xff})}, workspace: workspaceClaims, want: ErrApprovalUnauthenticated},
		{name: "invalid workspace UTF-8", actor: actorClaims, workspace: ApprovalWorkspaceClaims{WorkspaceID: string([]byte{'w', 0xff}), CanonicalRepoRoot: canonicalRoot}, want: ErrApprovalUnauthorized},
		{name: "actor code point overflow", actor: ApprovalActorClaims{ActorID: tooLongActor, SessionID: actorClaims.SessionID}, workspace: workspaceClaims, want: ErrApprovalUnauthenticated},
		{name: "session code point overflow", actor: ApprovalActorClaims{ActorID: actorClaims.ActorID, SessionID: tooLongSession}, workspace: workspaceClaims, want: ErrApprovalUnauthenticated},
		{name: "workspace code point overflow", actor: actorClaims, workspace: ApprovalWorkspaceClaims{WorkspaceID: tooLongWorkspace, CanonicalRepoRoot: canonicalRoot}, want: ErrApprovalUnauthorized},
	} {
		t.Run(test.name, func(t *testing.T) {
			caseGate := NewApprovalAccessGate(
				approvalAuthenticatorFunc(func(context.Context) (ApprovalActorClaims, error) { return test.actor, nil }),
				approvalAuthorizerFunc(func(context.Context, ApprovalActorClaims, string, string) (ApprovalWorkspaceClaims, error) {
					return test.workspace, nil
				}),
			)
			_, err := caseGate.AuthenticateAndAuthorize(context.Background(), workspaceClaims.WorkspaceID, root)
			if !errors.Is(err, test.want) {
				t.Fatalf("claim validation error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestLocalProcessApprovalAuthenticatorDefaultDoesNotExposeRawIdentity(t *testing.T) {
	authenticator := NewLocalProcessApprovalAuthenticator()
	claims, err := authenticator.Authenticate(context.Background())
	if err != nil {
		t.Fatalf("default local authentication: %v", err)
	}
	if !validApprovalOpaqueID(claims.ActorID, approvalActorIDMax) || !validApprovalOpaqueID(claims.SessionID, approvalSessionIDMax) {
		t.Fatalf("default claims are not bounded opaque IDs: %+v", claims)
	}
	if !strings.HasPrefix(claims.ActorID, "local-") || !strings.HasPrefix(claims.SessionID, "session-") {
		t.Fatalf("default claims do not use bounded Core IDs: %+v", claims)
	}
	other, err := NewLocalProcessApprovalAuthenticator().Authenticate(context.Background())
	if err != nil {
		t.Fatalf("second default local authentication: %v", err)
	}
	if claims.ActorID != other.ActorID || claims.SessionID == other.SessionID {
		t.Fatalf("default authenticator authority = %+v/%+v, want stable actor and fresh session", claims, other)
	}
	if home := os.Getenv("HOME"); home != "" && (strings.Contains(claims.ActorID, home) || strings.Contains(claims.SessionID, home)) {
		t.Fatalf("default claims exposed home identity: %+v", claims)
	}
}

func TestLocalProcessApprovalAuthenticatorFailuresAreTypedAndRedacted(t *testing.T) {
	sentinel := errors.New("fixture principal failure")
	tests := []struct {
		name    string
		lookup  approvalOSPrincipalLookup
		session approvalSessionGenerator
	}{
		{
			name: "principal lookup failure",
			lookup: func() (string, error) {
				return "", sentinel
			},
			session: func() (string, error) { return "session-unused", nil },
		},
		{
			name:    "empty principal",
			lookup:  func() (string, error) { return "", nil },
			session: func() (string, error) { return "session-unused", nil },
		},
		{
			name:   "session entropy failure",
			lookup: func() (string, error) { return "uid:fixture", nil },
			session: func() (string, error) {
				return "", errors.New("session entropy failure")
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			authenticator := newLocalProcessApprovalAuthenticator(test.lookup, test.session)
			_, err := authenticator.Authenticate(context.Background())
			if !errors.Is(err, ErrApprovalUnauthenticated) {
				t.Fatalf("authentication error = %v, want ErrApprovalUnauthenticated", err)
			}
			var typed *ApprovalUnauthenticatedError
			if !errors.As(err, &typed) {
				t.Fatalf("authentication error type = %T, want ApprovalUnauthenticatedError", err)
			}
			if strings.Contains(err.Error(), "fixture principal failure") || strings.Contains(err.Error(), "session entropy failure") {
				t.Fatalf("authentication error leaked local failure detail: %v", err)
			}
		})
	}
}

func TestApprovalAccessGateAcceptsIndependentClaimImplementations(t *testing.T) {
	root := t.TempDir()
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("canonical test root: %v", err)
	}
	actorClaims := ApprovalActorClaims{ActorID: "actor-independent", SessionID: "session-independent"}
	workspaceClaims := ApprovalWorkspaceClaims{WorkspaceID: "workspace-independent", CanonicalRepoRoot: canonicalRoot}
	authenticatorCalls := 0
	authorizerCalls := 0
	gate := NewApprovalAccessGate(
		approvalAuthenticatorFunc(func(context.Context) (ApprovalActorClaims, error) {
			authenticatorCalls++
			return actorClaims, nil
		}),
		approvalAuthorizerFunc(func(_ context.Context, got ApprovalActorClaims, workspaceID, target string) (ApprovalWorkspaceClaims, error) {
			authorizerCalls++
			if got != actorClaims {
				return ApprovalWorkspaceClaims{}, errors.New("actor claim was changed before authorizer")
			}
			if workspaceID != workspaceClaims.WorkspaceID || target != root {
				return ApprovalWorkspaceClaims{}, errors.New("request binding was changed before authorizer")
			}
			return workspaceClaims, nil
		}),
	)
	access, err := gate.AuthenticateAndAuthorize(context.Background(), workspaceClaims.WorkspaceID, root)
	if err != nil {
		t.Fatalf("independent claim gate: %v", err)
	}
	if authenticatorCalls != 1 || authorizerCalls != 1 {
		t.Fatalf("dependency calls = %d/%d, want one each", authenticatorCalls, authorizerCalls)
	}
	if access.Actor().ActorID != actorClaims.ActorID || access.Actor().SessionID != actorClaims.SessionID || access.Workspace().WorkspaceID() != workspaceClaims.WorkspaceID || access.Workspace().CanonicalRepoRoot() != canonicalRoot {
		t.Fatalf("gate access = %+v, want independent claims bound by gate", access)
	}
	if !access.actor.valid() || !access.workspace.validFor(access.actor) {
		t.Fatal("gate did not issue private final authority seals")
	}

	mutatedActor := access.Actor()
	mutatedActor.ActorID = "caller-spoof"
	mutatedWorkspace := access.Workspace()
	mutatedWorkspace.repoRoot = "caller-spoof"
	if access.Actor().ActorID == mutatedActor.ActorID || access.Workspace().CanonicalRepoRoot() == mutatedWorkspace.CanonicalRepoRoot() {
		t.Fatal("access result exposed mutable authority state")
	}
}

func TestApprovalAccessGateRejectsInvalidClaimsAndMissingDependencies(t *testing.T) {
	root := t.TempDir()
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("canonical test root: %v", err)
	}
	validActor := ApprovalActorClaims{ActorID: "actor-valid", SessionID: "session-valid"}
	validWorkspace := ApprovalWorkspaceClaims{WorkspaceID: "workspace-valid", CanonicalRepoRoot: canonicalRoot}
	validAuth := approvalAuthenticatorFunc(func(context.Context) (ApprovalActorClaims, error) { return validActor, nil })
	validAuthorizer := approvalAuthorizerFunc(func(context.Context, ApprovalActorClaims, string, string) (ApprovalWorkspaceClaims, error) {
		return validWorkspace, nil
	})

	tests := []struct {
		name             string
		gate             *ApprovalAccessGate
		workspaceID      string
		target           string
		wantSentinel     error
		wantUnauth       bool
		wantUnauthorized bool
	}{
		{
			name:         "missing authenticator",
			gate:         NewApprovalAccessGate(nil, validAuthorizer),
			workspaceID:  validWorkspace.WorkspaceID,
			target:       root,
			wantSentinel: ErrApprovalUnauthenticated,
			wantUnauth:   true,
		},
		{
			name:             "missing authorizer",
			gate:             NewApprovalAccessGate(validAuth, nil),
			workspaceID:      validWorkspace.WorkspaceID,
			target:           root,
			wantSentinel:     ErrApprovalUnauthorized,
			wantUnauthorized: true,
		},
		{
			name: "empty actor",
			gate: NewApprovalAccessGate(approvalAuthenticatorFunc(func(context.Context) (ApprovalActorClaims, error) {
				return ApprovalActorClaims{SessionID: validActor.SessionID}, nil
			}), validAuthorizer),
			workspaceID:  validWorkspace.WorkspaceID,
			target:       root,
			wantSentinel: ErrApprovalUnauthenticated,
			wantUnauth:   true,
		},
		{
			name: "oversized session",
			gate: NewApprovalAccessGate(approvalAuthenticatorFunc(func(context.Context) (ApprovalActorClaims, error) {
				return ApprovalActorClaims{ActorID: validActor.ActorID, SessionID: strings.Repeat("s", approvalSessionIDMax+1)}, nil
			}), validAuthorizer),
			workspaceID:  validWorkspace.WorkspaceID,
			target:       root,
			wantSentinel: ErrApprovalUnauthenticated,
			wantUnauth:   true,
		},
		{
			name: "oversized actor",
			gate: NewApprovalAccessGate(approvalAuthenticatorFunc(func(context.Context) (ApprovalActorClaims, error) {
				return ApprovalActorClaims{ActorID: strings.Repeat("a", approvalActorIDMax+1), SessionID: validActor.SessionID}, nil
			}), validAuthorizer),
			workspaceID:  validWorkspace.WorkspaceID,
			target:       root,
			wantSentinel: ErrApprovalUnauthenticated,
			wantUnauth:   true,
		},
		{
			name: "unsafe session",
			gate: NewApprovalAccessGate(approvalAuthenticatorFunc(func(context.Context) (ApprovalActorClaims, error) {
				return ApprovalActorClaims{ActorID: validActor.ActorID, SessionID: "session/escape"}, nil
			}), validAuthorizer),
			workspaceID:  validWorkspace.WorkspaceID,
			target:       root,
			wantSentinel: ErrApprovalUnauthenticated,
			wantUnauth:   true,
		},
		{
			name: "authorizer returns empty workspace",
			gate: NewApprovalAccessGate(validAuth, approvalAuthorizerFunc(func(context.Context, ApprovalActorClaims, string, string) (ApprovalWorkspaceClaims, error) {
				return ApprovalWorkspaceClaims{}, nil
			})),
			workspaceID:      validWorkspace.WorkspaceID,
			target:           root,
			wantSentinel:     ErrApprovalUnauthorized,
			wantUnauthorized: true,
		},
		{
			name: "authorizer returns mismatched workspace",
			gate: NewApprovalAccessGate(validAuth, approvalAuthorizerFunc(func(context.Context, ApprovalActorClaims, string, string) (ApprovalWorkspaceClaims, error) {
				return ApprovalWorkspaceClaims{WorkspaceID: "workspace-other", CanonicalRepoRoot: canonicalRoot}, nil
			})),
			workspaceID:      validWorkspace.WorkspaceID,
			target:           root,
			wantSentinel:     ErrApprovalUnauthorized,
			wantUnauthorized: true,
		},
		{
			name: "authorizer returns oversized workspace",
			gate: NewApprovalAccessGate(validAuth, approvalAuthorizerFunc(func(context.Context, ApprovalActorClaims, string, string) (ApprovalWorkspaceClaims, error) {
				return ApprovalWorkspaceClaims{WorkspaceID: strings.Repeat("w", approvalWorkspaceIDMax+1), CanonicalRepoRoot: canonicalRoot}, nil
			})),
			workspaceID:      validWorkspace.WorkspaceID,
			target:           root,
			wantSentinel:     ErrApprovalUnauthorized,
			wantUnauthorized: true,
		},
		{
			name: "authorizer returns unsafe workspace",
			gate: NewApprovalAccessGate(validAuth, approvalAuthorizerFunc(func(context.Context, ApprovalActorClaims, string, string) (ApprovalWorkspaceClaims, error) {
				unsafeRoot := canonicalRoot + string(filepath.Separator) + ".." + string(filepath.Separator) + filepath.Base(canonicalRoot)
				return ApprovalWorkspaceClaims{WorkspaceID: validWorkspace.WorkspaceID, CanonicalRepoRoot: unsafeRoot}, nil
			})),
			workspaceID:      validWorkspace.WorkspaceID,
			target:           root,
			wantSentinel:     ErrApprovalUnauthorized,
			wantUnauthorized: true,
		},
		{
			name: "unsafe actor claim",
			gate: NewApprovalAccessGate(approvalAuthenticatorFunc(func(context.Context) (ApprovalActorClaims, error) {
				return ApprovalActorClaims{ActorID: "actor/escape", SessionID: validActor.SessionID}, nil
			}), validAuthorizer),
			workspaceID:  validWorkspace.WorkspaceID,
			target:       root,
			wantSentinel: ErrApprovalUnauthenticated,
			wantUnauth:   true,
		},
		{
			name:             "unsafe target",
			gate:             NewApprovalAccessGate(validAuth, validAuthorizer),
			workspaceID:      validWorkspace.WorkspaceID,
			target:           filepath.Join("..", filepath.Base(root)),
			wantSentinel:     ErrApprovalUnauthorized,
			wantUnauthorized: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := test.gate.AuthenticateAndAuthorize(context.Background(), test.workspaceID, test.target)
			if !errors.Is(err, test.wantSentinel) {
				t.Fatalf("gate error = %v, want %v", err, test.wantSentinel)
			}
			if test.wantUnauth {
				var typed *ApprovalUnauthenticatedError
				if !errors.As(err, &typed) {
					t.Fatalf("gate error type = %T, want ApprovalUnauthenticatedError", err)
				}
			}
			if test.wantUnauthorized {
				var typed *ApprovalUnauthorizedError
				if !errors.As(err, &typed) {
					t.Fatalf("gate error type = %T, want ApprovalUnauthorizedError", err)
				}
			}
			if strings.Contains(err.Error(), root) {
				t.Fatalf("gate error exposed workspace path: %v", err)
			}
		})
	}
}

func TestApprovalWorkspaceAuthorizerBindsExactIdentityAndCanonicalRoot(t *testing.T) {
	root := t.TempDir()
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("canonical root: %v", err)
	}
	authenticator := newLocalProcessApprovalAuthenticator(func() (string, error) { return "uid:workspace-fixture", nil }, func() (string, error) { return "session-workspace", nil })
	actor, err := authenticator.Authenticate(context.Background())
	if err != nil {
		t.Fatalf("authenticate fixture actor: %v", err)
	}
	authorizer := NewApprovalWorkspaceAuthorizer(root)
	workspaceID := authorizer.WorkspaceID()
	if workspaceID == "" || !validApprovalOpaqueID(workspaceID, approvalWorkspaceIDMax) {
		t.Fatalf("workspace identity = %q, want bounded opaque ID", workspaceID)
	}
	if got := approvalWorkspaceIDForCanonicalRoot(canonicalRoot); got != workspaceID {
		t.Fatalf("workspace identity helper = %q, authorizer identity = %q", got, workspaceID)
	}

	allowed, err := authorizer.Authorize(context.Background(), actor, workspaceID, root)
	if err != nil {
		t.Fatalf("configured root authorization: %v", err)
	}
	if allowed.CanonicalRepoRoot != canonicalRoot || allowed.WorkspaceID != workspaceID {
		t.Fatalf("authorized claims = %+v, want exact canonical root and identity", allowed)
	}

	aliasParent := t.TempDir()
	alias := filepath.Join(aliasParent, "configured-root-alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := authorizer.Authorize(context.Background(), actor, workspaceID, alias); err != nil {
		t.Fatalf("configured-root symlink alias: %v", err)
	}

	other := t.TempDir()
	otherAlias := filepath.Join(aliasParent, "other-root-alias")
	if err := os.Symlink(other, otherAlias); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	for _, test := range []struct {
		name   string
		id     string
		target string
	}{
		{name: "workspace identity mismatch", id: workspaceID + "-other", target: root},
		{name: "other absolute root", id: workspaceID, target: other},
		{name: "relative parent escape", id: workspaceID, target: filepath.Join("..", filepath.Base(other))},
		{name: "symlink other root", id: workspaceID, target: otherAlias},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := authorizer.Authorize(context.Background(), actor, test.id, test.target)
			if !errors.Is(err, ErrApprovalUnauthorized) {
				t.Fatalf("authorization error = %v, want ErrApprovalUnauthorized", err)
			}
			if strings.Contains(err.Error(), root) || strings.Contains(err.Error(), other) {
				t.Fatalf("authorization error exposed filesystem path: %v", err)
			}
		})
	}
}

func TestApprovalAccessGateDefensiveAuthorityIsConcurrentAndStable(t *testing.T) {
	root := t.TempDir()
	authenticator := newLocalProcessApprovalAuthenticator(func() (string, error) { return "uid:concurrent-fixture", nil }, func() (string, error) { return "session-concurrent", nil })
	authorizer := NewApprovalWorkspaceAuthorizer(root)
	gate := NewApprovalAccessGate(authenticator, authorizer)
	workspaceID := authorizer.WorkspaceID()
	first, err := gate.AuthenticateAndAuthorize(context.Background(), workspaceID, root)
	if err != nil {
		t.Fatalf("first gate result: %v", err)
	}
	if !first.actor.valid() || !first.workspace.validFor(first.actor) {
		t.Fatal("first gate result lacks private authority binding")
	}

	const callers = 8
	type outcome struct {
		access ApprovalAccess
		err    error
	}
	results := make(chan outcome, callers)
	var group sync.WaitGroup
	for i := 0; i < callers; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			access, err := gate.AuthenticateAndAuthorize(context.Background(), workspaceID, root)
			results <- outcome{access: access, err: err}
		}()
	}
	group.Wait()
	close(results)
	for result := range results {
		if result.err != nil {
			t.Fatalf("concurrent gate error: %v", result.err)
		}
		if result.access.Actor().ActorID != first.Actor().ActorID || result.access.Actor().SessionID != first.Actor().SessionID || result.access.Workspace().WorkspaceID() != workspaceID || !result.access.actor.valid() || !result.access.workspace.validFor(result.access.actor) {
			t.Fatalf("concurrent gate binding = %+v, want stable Core authority", result.access)
		}
	}
}

type approvalAuthenticatorFunc func(context.Context) (ApprovalActorClaims, error)

func (f approvalAuthenticatorFunc) Authenticate(ctx context.Context) (ApprovalActorClaims, error) {
	return f(ctx)
}

type approvalAuthorizerFunc func(context.Context, ApprovalActorClaims, string, string) (ApprovalWorkspaceClaims, error)

func (f approvalAuthorizerFunc) Authorize(ctx context.Context, actor ApprovalActorClaims, workspaceID, target string) (ApprovalWorkspaceClaims, error) {
	return f(ctx, actor, workspaceID, target)
}
