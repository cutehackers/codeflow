package semantic

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

const (
	approvalActorIDMax       = 256
	approvalSessionIDMax     = 256
	approvalWorkspaceIDMax   = 256
	approvalWorkspacePathMax = 4096
)

var (
	// ErrApprovalUnauthenticated identifies an approval request for which Core
	// could not establish a trusted local actor and session.
	ErrApprovalUnauthenticated = errors.New("approval unauthenticated")
	// ErrApprovalUnauthorized identifies an authenticated actor that is not
	// authorized for the exact configured workspace.
	ErrApprovalUnauthorized = errors.New("approval unauthorized")
)

// ApprovalUnauthenticatedError preserves a typed access failure for local
// callers and protocol adapters without exposing local identity details.
type ApprovalUnauthenticatedError struct {
	Reason string
	err    error
}

func (e *ApprovalUnauthenticatedError) Error() string {
	if e == nil || e.Reason == "" {
		return ErrApprovalUnauthenticated.Error()
	}
	return e.Reason
}

func (e *ApprovalUnauthenticatedError) Unwrap() error {
	if e == nil || e.err == nil {
		return ErrApprovalUnauthenticated
	}
	return errors.Join(ErrApprovalUnauthenticated, e.err)
}

// ApprovalUnauthorizedError preserves a typed workspace authorization
// failure without exposing configured or requested filesystem paths.
type ApprovalUnauthorizedError struct {
	Reason string
	err    error
}

func (e *ApprovalUnauthorizedError) Error() string {
	if e == nil || e.Reason == "" {
		return ErrApprovalUnauthorized.Error()
	}
	return e.Reason
}

func (e *ApprovalUnauthorizedError) Unwrap() error {
	if e == nil || e.err == nil {
		return ErrApprovalUnauthorized
	}
	return errors.Join(ErrApprovalUnauthorized, e.err)
}

// ApprovalActorClaims is the plain validated claim returned by a trusted Core
// authenticator. It has no private seal because the access gate owns the final
// authority seal and independent test authenticators must be replaceable.
type ApprovalActorClaims struct {
	ActorID   string
	SessionID string
}

// AuthenticatedApprovalActor is issued by ApprovalAccessGate after it has
// validated authenticator claims. The private seal distinguishes this value
// from caller-constructed claims at the final approval boundary.
type AuthenticatedApprovalActor struct {
	ActorID   string `json:"-"`
	SessionID string `json:"-"`
	seal      *approvalActorSeal
}

type approvalActorSeal struct {
	actorID   string
	sessionID string
	gate      *ApprovalAccessGate
}

func (a AuthenticatedApprovalActor) valid() bool {
	return a.seal != nil && a.seal.gate != nil && a.seal.actorID == a.ActorID && a.seal.sessionID == a.SessionID && validApprovalOpaqueID(a.ActorID, approvalActorIDMax) && validApprovalOpaqueID(a.SessionID, approvalSessionIDMax)
}

// ApprovalAuthenticator is the Core authentication boundary. It accepts only
// request context and never caller-provided approver, actor, or session text.
type ApprovalAuthenticator interface {
	Authenticate(context.Context) (ApprovalActorClaims, error)
}

var _ ApprovalAuthenticator = (*LocalProcessApprovalAuthenticator)(nil)

// LocalProcessApprovalAuthenticator binds one Core process actor to one
// Core-issued session. The actor is a domain-separated hash of the operating
// system principal, and the session is generated with crypto/rand once per
// authenticator instance.
type LocalProcessApprovalAuthenticator struct {
	actorID   string
	sessionID string
	initErr   error
}

type approvalOSPrincipalLookup func() (string, error)
type approvalSessionGenerator func() (string, error)

// NewLocalProcessApprovalAuthenticator creates the default local
// authenticator. Lookup and entropy failures are retained and returned as
// typed unauthenticated results by Authenticate.
func NewLocalProcessApprovalAuthenticator() *LocalProcessApprovalAuthenticator {
	return newLocalProcessApprovalAuthenticator(defaultApprovalOSPrincipal, issueApprovalSessionID)
}

func newLocalProcessApprovalAuthenticator(lookup approvalOSPrincipalLookup, session approvalSessionGenerator) *LocalProcessApprovalAuthenticator {
	authenticator := &LocalProcessApprovalAuthenticator{}
	if lookup == nil || session == nil {
		authenticator.initErr = errors.New("local approval authority dependency is unavailable")
		return authenticator
	}
	principal, err := lookup()
	if err != nil {
		authenticator.initErr = err
		return authenticator
	}
	authenticator.actorID, err = approvalActorIDFromOSPrincipal(principal)
	if err != nil {
		authenticator.initErr = err
		return authenticator
	}
	authenticator.sessionID, err = session()
	if err != nil {
		authenticator.initErr = err
	}
	return authenticator
}

func (a *LocalProcessApprovalAuthenticator) Authenticate(ctx context.Context) (ApprovalActorClaims, error) {
	if a == nil {
		return ApprovalActorClaims{}, &ApprovalUnauthenticatedError{Reason: "approval authenticator is unavailable"}
	}
	if err := approvalContextError(ctx); err != nil {
		return ApprovalActorClaims{}, &ApprovalUnauthenticatedError{Reason: "approval authentication context is unavailable", err: safeApprovalCause(err)}
	}
	if a.initErr != nil || !validApprovalOpaqueID(a.actorID, approvalActorIDMax) || !validApprovalOpaqueID(a.sessionID, approvalSessionIDMax) {
		return ApprovalActorClaims{}, &ApprovalUnauthenticatedError{Reason: "local approval identity is unavailable", err: safeApprovalCause(a.initErr)}
	}
	return ApprovalActorClaims{ActorID: a.actorID, SessionID: a.sessionID}, nil
}

func defaultApprovalOSPrincipal() (string, error) {
	principal, err := user.Current()
	if err != nil {
		return "", errors.New("operating system approval principal is unavailable")
	}
	// Uid is the POSIX UID on Unix and the SID-like stable account identifier
	// on Windows. It is hashed immediately and never appears in an error or
	// payload. Username is intentionally not a fallback authority.
	if strings.TrimSpace(principal.Uid) == "" {
		return "", errors.New("operating system approval principal is incomplete")
	}
	return principal.Uid, nil
}

func approvalActorIDFromOSPrincipal(principal string) (string, error) {
	if strings.TrimSpace(principal) == "" || strings.IndexByte(principal, 0) >= 0 {
		return "", errors.New("operating system approval principal is empty")
	}
	digest := sha256.Sum256(append([]byte("codeflow/approval-actor/v1\x00"), []byte(principal)...))
	return "local-" + hex.EncodeToString(digest[:16]), nil
}

func issueApprovalSessionID() (string, error) {
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", errors.New("local approval session entropy is unavailable")
	}
	return "session-" + hex.EncodeToString(nonce[:]), nil
}

// ApprovalWorkspaceClaims is the plain validated claim returned by a trusted
// workspace authorizer. The gate issues the final private workspace seal.
type ApprovalWorkspaceClaims struct {
	WorkspaceID       string
	CanonicalRepoRoot string
}

// AuthorizedApprovalWorkspace is the gate-issued exact workspace value. Its
// private seal prevents replacement with caller-constructed path or ID text.
type AuthorizedApprovalWorkspace struct {
	repoRoot    string
	workspaceID string
	seal        *approvalWorkspaceSeal
}

type approvalWorkspaceSeal struct {
	repoRoot    string
	workspaceID string
	actor       AuthenticatedApprovalActor
	gate        *ApprovalAccessGate
}

// CanonicalRepoRoot returns the trusted configured root for an already
// authorized internal operation. It is not a payload field.
func (w AuthorizedApprovalWorkspace) CanonicalRepoRoot() string { return w.repoRoot }

// WorkspaceID returns the Core-derived bounded workspace identity.
func (w AuthorizedApprovalWorkspace) WorkspaceID() string { return w.workspaceID }

func (w AuthorizedApprovalWorkspace) validFor(actor AuthenticatedApprovalActor) bool {
	return w.seal != nil && w.seal.gate != nil && w.seal.repoRoot == w.repoRoot && w.seal.workspaceID == w.workspaceID && w.seal.actor.valid() && w.seal.actor.ActorID == actor.ActorID && w.seal.actor.SessionID == actor.SessionID && validApprovalOpaqueID(w.workspaceID, approvalWorkspaceIDMax) && validApprovalWorkspacePath(w.repoRoot)
}

// WorkspaceAuthorizer is the replaceable Core workspace authorization
// boundary. Implementations return plain claims; the access gate owns the
// final authority seal.
type WorkspaceAuthorizer interface {
	Authorize(context.Context, ApprovalActorClaims, string, string) (ApprovalWorkspaceClaims, error)
}

var _ WorkspaceAuthorizer = (*ApprovalWorkspaceAuthorizer)(nil)

// ApprovalWorkspaceAuthorizer binds authorization to one canonical repository
// root and one Core-derived workspace ID.
type ApprovalWorkspaceAuthorizer struct {
	configuredRoot string
	canonicalRoot  string
	workspaceID    string
	initErr        error
}

// NewApprovalWorkspaceAuthorizer creates an exact-root authorizer. Any root
// canonicalization failure is retained so later authorization fails closed.
func NewApprovalWorkspaceAuthorizer(repoRoot string) *ApprovalWorkspaceAuthorizer {
	authorizer := &ApprovalWorkspaceAuthorizer{configuredRoot: repoRoot}
	canonicalRoot, err := canonicalApprovalWorkspaceRoot(repoRoot)
	if err != nil {
		authorizer.initErr = err
		return authorizer
	}
	authorizer.canonicalRoot = canonicalRoot
	authorizer.workspaceID = approvalWorkspaceIDForCanonicalRoot(canonicalRoot)
	return authorizer
}

func approvalWorkspaceIDForCanonicalRoot(canonicalRoot string) string {
	digest := sha256.Sum256(append([]byte("codeflow/approval-workspace/v1\x00"), []byte(canonicalRoot)...))
	return "workspace-" + hex.EncodeToString(digest[:16])
}

// WorkspaceID returns the configured Core-derived identity without granting
// authorization. It is intended for trusted server setup and tests.
func (a *ApprovalWorkspaceAuthorizer) WorkspaceID() string {
	if a == nil {
		return ""
	}
	return a.workspaceID
}

func (a *ApprovalWorkspaceAuthorizer) Authorize(ctx context.Context, actor ApprovalActorClaims, requestedWorkspaceID, requestedTarget string) (ApprovalWorkspaceClaims, error) {
	if a == nil {
		return ApprovalWorkspaceClaims{}, &ApprovalUnauthorizedError{Reason: "workspace authorizer is unavailable"}
	}
	if a.initErr != nil || !validApprovalOpaqueID(a.workspaceID, approvalWorkspaceIDMax) || !validApprovalWorkspacePath(a.canonicalRoot) {
		return ApprovalWorkspaceClaims{}, &ApprovalUnauthorizedError{Reason: "configured workspace authorization is unavailable", err: safeApprovalCause(a.initErr)}
	}
	if err := approvalContextError(ctx); err != nil {
		return ApprovalWorkspaceClaims{}, &ApprovalUnauthorizedError{Reason: "workspace authorization context is unavailable", err: safeApprovalCause(err)}
	}
	if err := validateApprovalActorClaims(actor); err != nil {
		return ApprovalWorkspaceClaims{}, &ApprovalUnauthorizedError{Reason: "authenticated actor claims are invalid", err: err}
	}
	if strings.TrimSpace(requestedWorkspaceID) == "" || requestedWorkspaceID != a.workspaceID {
		return ApprovalWorkspaceClaims{}, &ApprovalUnauthorizedError{Reason: "requested workspace identity is not authorized"}
	}
	target := requestedTarget
	if target == "" || target == "." {
		target = a.canonicalRoot
	}
	if hasApprovalParentTraversal(target) {
		return ApprovalWorkspaceClaims{}, &ApprovalUnauthorizedError{Reason: "requested workspace target escapes the configured workspace"}
	}
	if !filepath.IsAbs(target) {
		return ApprovalWorkspaceClaims{}, &ApprovalUnauthorizedError{Reason: "requested workspace target is not the configured workspace"}
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return ApprovalWorkspaceClaims{}, &ApprovalUnauthorizedError{Reason: "requested workspace target is invalid"}
	}
	canonicalTarget, err := filepath.EvalSymlinks(absTarget)
	if err != nil {
		return ApprovalWorkspaceClaims{}, &ApprovalUnauthorizedError{Reason: "requested workspace target is unavailable"}
	}
	canonicalTarget = filepath.Clean(canonicalTarget)
	info, err := os.Stat(canonicalTarget)
	if err != nil || !info.IsDir() || canonicalTarget != a.canonicalRoot {
		return ApprovalWorkspaceClaims{}, &ApprovalUnauthorizedError{Reason: "requested workspace is not authorized"}
	}
	return ApprovalWorkspaceClaims{WorkspaceID: a.workspaceID, CanonicalRepoRoot: a.canonicalRoot}, nil
}

func canonicalApprovalWorkspaceRoot(repoRoot string) (string, error) {
	if strings.TrimSpace(repoRoot) == "" {
		return "", errors.New("configured workspace root is empty")
	}
	absRoot, err := filepath.Abs(repoRoot)
	if err != nil {
		return "", errors.New("configured workspace root is unavailable")
	}
	info, err := os.Stat(absRoot)
	if err != nil {
		return "", errors.New("configured workspace root is unavailable")
	}
	if !info.IsDir() {
		return "", errors.New("configured workspace root is not a directory")
	}
	canonicalRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		return "", errors.New("configured workspace identity is unavailable")
	}
	return filepath.Clean(canonicalRoot), nil
}

func hasApprovalParentTraversal(path string) bool {
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if part == ".." {
			return true
		}
	}
	return false
}

func validApprovalOpaqueID(value string, max int) bool {
	if !utf8.ValidString(value) || strings.TrimSpace(value) == "" || utf8.RuneCountInString(value) > max || strings.TrimSpace(value) != value || strings.Contains(value, "..") {
		return false
	}
	for _, r := range value {
		if r == '/' || r == '\\' || r == 0 || r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func validApprovalWorkspacePath(path string) bool {
	if strings.TrimSpace(path) == "" || len(path) > approvalWorkspacePathMax || !filepath.IsAbs(path) || strings.ContainsRune(path, 0) || hasApprovalParentTraversal(path) {
		return false
	}
	for _, r := range path {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return filepath.Clean(path) == path
}

func validateApprovalActorClaims(claims ApprovalActorClaims) error {
	if !validApprovalOpaqueID(claims.ActorID, approvalActorIDMax) {
		return errors.New("actor identity claim is invalid")
	}
	if !validApprovalOpaqueID(claims.SessionID, approvalSessionIDMax) {
		return errors.New("session identity claim is invalid")
	}
	return nil
}

func validateApprovalWorkspaceClaims(claims ApprovalWorkspaceClaims) error {
	if !validApprovalOpaqueID(claims.WorkspaceID, approvalWorkspaceIDMax) {
		return errors.New("workspace identity claim is invalid")
	}
	if !validApprovalWorkspacePath(claims.CanonicalRepoRoot) {
		return errors.New("workspace root claim is invalid")
	}
	canonical, err := filepath.EvalSymlinks(claims.CanonicalRepoRoot)
	if err != nil || filepath.Clean(canonical) != claims.CanonicalRepoRoot {
		return errors.New("workspace root claim is not canonical")
	}
	info, err := os.Stat(claims.CanonicalRepoRoot)
	if err != nil || !info.IsDir() {
		return errors.New("workspace root claim is unavailable")
	}
	return nil
}

// ApprovalAccess is the defensive result of actor authentication and exact
// workspace authorization. Its private fields contain gate-issued seals.
type ApprovalAccess struct {
	actor     AuthenticatedApprovalActor
	workspace AuthorizedApprovalWorkspace
}

// Actor returns the Core-issued actor value. The returned value is a copy.
func (a ApprovalAccess) Actor() AuthenticatedApprovalActor { return a.actor }

// Workspace returns the Core-bound workspace value. The returned value is a
// copy and its private seal remains immutable.
func (a ApprovalAccess) Workspace() AuthorizedApprovalWorkspace { return a.workspace }

// ApprovalAccessGate performs authentication and exact workspace
// authorization. It accepts no caller approver, actor, or session values.
type ApprovalAccessGate struct {
	authenticator ApprovalAuthenticator
	authorizer    WorkspaceAuthorizer
}

// NewApprovalAccessGate creates a gate over the trusted Core dependencies.
func NewApprovalAccessGate(authenticator ApprovalAuthenticator, authorizer WorkspaceAuthorizer) *ApprovalAccessGate {
	return &ApprovalAccessGate{authenticator: authenticator, authorizer: authorizer}
}

// AuthenticateAndAuthorize returns a defensive Core-bound access value. The
// requested workspace ID and target are untrusted request values.
func (g *ApprovalAccessGate) AuthenticateAndAuthorize(ctx context.Context, requestedWorkspaceID, requestedTarget string) (ApprovalAccess, error) {
	if g == nil || g.authenticator == nil {
		return ApprovalAccess{}, &ApprovalUnauthenticatedError{Reason: "approval authenticator is unavailable"}
	}
	if err := approvalContextError(ctx); err != nil {
		return ApprovalAccess{}, &ApprovalUnauthenticatedError{Reason: "approval request context is unavailable", err: safeApprovalCause(err)}
	}
	claims, err := g.authenticator.Authenticate(ctx)
	if err != nil {
		return ApprovalAccess{}, &ApprovalUnauthenticatedError{Reason: "approval authentication failed", err: safeApprovalCause(err)}
	}
	if err := validateApprovalActorClaims(claims); err != nil {
		return ApprovalAccess{}, &ApprovalUnauthenticatedError{Reason: "approval authenticator returned invalid claims", err: err}
	}
	if strings.TrimSpace(requestedWorkspaceID) == "" {
		return ApprovalAccess{}, &ApprovalUnauthorizedError{Reason: "requested workspace identity is not authorized"}
	}
	if g.authorizer == nil {
		return ApprovalAccess{}, &ApprovalUnauthorizedError{Reason: "workspace authorizer is unavailable"}
	}
	workspaceClaims, err := g.authorizer.Authorize(ctx, claims, requestedWorkspaceID, requestedTarget)
	if err != nil {
		return ApprovalAccess{}, &ApprovalUnauthorizedError{Reason: "workspace authorization failed", err: safeApprovalCause(err)}
	}
	if err := validateApprovalWorkspaceClaims(workspaceClaims); err != nil {
		return ApprovalAccess{}, &ApprovalUnauthorizedError{Reason: "workspace authorizer returned invalid claims", err: err}
	}
	if workspaceClaims.WorkspaceID != requestedWorkspaceID {
		return ApprovalAccess{}, &ApprovalUnauthorizedError{Reason: "workspace authorizer returned a mismatched identity"}
	}
	if target := requestedTarget; target != "" && target != "." {
		if hasApprovalParentTraversal(target) || !filepath.IsAbs(target) {
			return ApprovalAccess{}, &ApprovalUnauthorizedError{Reason: "requested workspace target is invalid"}
		}
		canonicalTarget, err := filepath.EvalSymlinks(target)
		if err != nil || filepath.Clean(canonicalTarget) != workspaceClaims.CanonicalRepoRoot {
			return ApprovalAccess{}, &ApprovalUnauthorizedError{Reason: "workspace target does not match authorized workspace"}
		}
	}
	if err := approvalContextError(ctx); err != nil {
		return ApprovalAccess{}, &ApprovalUnauthenticatedError{Reason: "approval request context is unavailable", err: safeApprovalCause(err)}
	}
	actor := AuthenticatedApprovalActor{
		ActorID:   claims.ActorID,
		SessionID: claims.SessionID,
		seal:      &approvalActorSeal{actorID: claims.ActorID, sessionID: claims.SessionID, gate: g},
	}
	workspace := AuthorizedApprovalWorkspace{
		repoRoot:    workspaceClaims.CanonicalRepoRoot,
		workspaceID: workspaceClaims.WorkspaceID,
		seal:        &approvalWorkspaceSeal{repoRoot: workspaceClaims.CanonicalRepoRoot, workspaceID: workspaceClaims.WorkspaceID, actor: actor, gate: g},
	}
	return ApprovalAccess{actor: actor, workspace: workspace}, nil
}

func approvalContextError(ctx context.Context) error {
	if ctx == nil {
		return errors.New("context is nil")
	}
	return ctx.Err()
}

func safeApprovalCause(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	return nil
}
