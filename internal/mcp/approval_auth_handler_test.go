package mcp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"codeflow/internal/semantic"
)

func TestMCPSubmitSemanticApprovalRejectsCallerAuthorityFields(t *testing.T) {
	root := t.TempDir()
	server, err := NewServer(Config{RepoRoot: root, RequireToken: false})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer server.Close()
	fixture := completeUnattestedAvailableResultForMCP(t)
	server.proposalStore = &mcpApprovalPairStore{stored: &semantic.StoredProposal{
		WorkspaceID: server.approvalWorkspaceID, Proposal: fixture.Proposal, Pack: fixture.Pack,
	}}

	access, err := server.approvalGate.AuthenticateAndAuthorize(context.Background(), server.approvalWorkspaceID, root)
	if err != nil {
		t.Fatalf("derive authenticated actor for fixture: %v", err)
	}
	actorID := access.Actor().ActorID

	spoofed := mcpApprovalRequestArgs(root, fixture, "spoofed-approval")
	spoofed["approver"] = "caller-spoof"
	if _, err := server.executeTool(context.Background(), "submit_semantic_approval", spoofed); err == nil {
		t.Fatal("caller-supplied approver unexpectedly accepted")
	}
	invalid := mcpApprovalRequestArgs(root, fixture, "invalid-approver")
	invalid["approver"] = map[string]any{"actor": actorID}
	if _, err := server.executeTool(context.Background(), "submit_semantic_approval", invalid); err == nil {
		t.Fatal("non-string caller approver unexpectedly accepted")
	}
	unknown := mcpApprovalRequestArgs(root, fixture, "unknown-field")
	unknown["unexpected"] = true
	if _, err := server.executeTool(context.Background(), "submit_semantic_approval", unknown); err == nil {
		t.Fatal("arbitrary approval field unexpectedly accepted")
	}
	_ = actorID
}

func TestMCPSubmitSemanticApprovalRequiresDurableStoredProposalPair(t *testing.T) {
	root := t.TempDir()
	server, err := NewServer(Config{RepoRoot: root, RequireToken: false})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer server.Close()

	missing := map[string]any{
		"target": root, "commandId": "missing-pair-command", "proposalId": "proposal-not-persisted", "evidencePackId": "pack-not-persisted",
		"computedBasisId": "basis-missing", "generationId": "generation-missing", "intentRevision": int64(1), "decision": "approve",
		"idempotencyKey": "missing-pair-key", "expectedApprovalVersion": int64(0), "expectedState": "none",
	}
	if _, err := server.executeTool(context.Background(), "submit_semantic_approval", missing); !errors.Is(err, semantic.ErrApprovalExecutionUnavailable) {
		t.Fatalf("missing durable pair error = %v, want ErrApprovalExecutionUnavailable", err)
	}
	missing["evidencePackId"] = ""
	if _, err := server.executeTool(context.Background(), "submit_semantic_approval", missing); err == nil {
		t.Fatal("missing evidence-pack identity unexpectedly accepted")
	}
}

func TestMCPSubmitSemanticApprovalRejectsMismatchedStoredPair(t *testing.T) {
	root := t.TempDir()
	fixture := completeUnattestedAvailableResultForMCP(t)

	cases := []struct {
		name   string
		mutate func(*semantic.StoredProposal)
	}{
		{
			name: "workspace identity",
			mutate: func(stored *semantic.StoredProposal) {
				stored.WorkspaceID = "workspace-returned-other"
			},
		},
		{
			name: "proposal identity",
			mutate: func(stored *semantic.StoredProposal) {
				proposal := *stored.Proposal
				proposal.ProposalID = "proposal-returned-other"
				stored.Proposal = &proposal
			},
		},
		{
			name: "evidence pack identity",
			mutate: func(stored *semantic.StoredProposal) {
				pack := *stored.Pack
				pack.EvidencePackID = "pack-returned-other"
				stored.Pack = &pack
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server, err := NewServer(Config{RepoRoot: root, RequireToken: false})
			if err != nil {
				t.Fatalf("NewServer: %v", err)
			}
			defer server.Close()
			stored := &semantic.StoredProposal{
				WorkspaceID: server.approvalWorkspaceID,
				Proposal:    fixture.Proposal,
				Pack:        fixture.Pack,
			}
			tc.mutate(stored)
			server.proposalStore = &mcpApprovalReturningStore{stored: stored}

			value, err := server.executeTool(context.Background(), "submit_semantic_approval", mcpApprovalRequestArgs(root, fixture, "mismatch-"+tc.name))
			if !errors.Is(err, semantic.ErrApprovalExecutionInvalid) {
				t.Fatalf("mismatched stored pair result = %#v err=%v, want ErrApprovalExecutionInvalid", value, err)
			}
			if strings.Contains(err.Error(), "returned-other") {
				t.Fatalf("mismatched stored pair leaked returned identity: %v", err)
			}
		})
	}
}

func TestMCPSubmitSemanticApprovalRejectsMissingAuthAndNonWorkspaceTargets(t *testing.T) {
	root := t.TempDir()
	server, err := NewServer(Config{RepoRoot: root, RequireToken: false})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer server.Close()

	server.approvalGate = nil
	_, err = server.executeTool(context.Background(), "submit_semantic_approval", map[string]any{
		"target": root, "proposalId": "proposal-no-auth", "decision": "approved",
	})
	if !errors.Is(err, semantic.ErrApprovalUnauthenticated) {
		t.Fatalf("missing approval gate error = %v, want ErrApprovalUnauthenticated", err)
	}
	server.approvalGate = semantic.NewApprovalAccessGate(semantic.NewLocalProcessApprovalAuthenticator(), nil)
	_, err = server.executeTool(context.Background(), "submit_semantic_approval", map[string]any{
		"target": root, "proposalId": "proposal-no-authorizer", "decision": "approved",
	})
	if !errors.Is(err, semantic.ErrApprovalUnauthorized) {
		t.Fatalf("missing workspace authorizer error = %v, want ErrApprovalUnauthorized", err)
	}
	server, err = NewServer(Config{RepoRoot: root, RequireToken: false})
	if err != nil {
		t.Fatalf("NewServer for target tests: %v", err)
	}
	defer server.Close()
	fixture := completeUnattestedAvailableResultForMCP(t)
	server.proposalStore = &mcpApprovalPairStore{stored: &semantic.StoredProposal{
		WorkspaceID: server.approvalWorkspaceID, Proposal: fixture.Proposal, Pack: fixture.Pack,
	}}
	other := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "nested"), 0o700); err != nil {
		t.Fatalf("create nested target: %v", err)
	}
	aliasParent := t.TempDir()
	otherAlias := filepath.Join(aliasParent, "other-root")
	if err := os.Symlink(other, otherAlias); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	for _, target := range []string{
		filepath.Join(root, "nested"),
		"nested",
		other,
		filepath.Join("..", filepath.Base(other)),
		otherAlias,
	} {
		_, err := server.executeTool(context.Background(), "submit_semantic_approval", map[string]any{
			"target": target, "proposalId": fixture.Proposal.ProposalID, "evidencePackId": fixture.Pack.EvidencePackID, "decision": "approved",
		})
		if !errors.Is(err, semantic.ErrApprovalUnauthorized) {
			t.Fatalf("target %q error = %v, want ErrApprovalUnauthorized", target, err)
		}
		if strings.Contains(err.Error(), root) || strings.Contains(err.Error(), other) {
			t.Fatalf("target %q error exposed filesystem path: %v", target, err)
		}
	}
}

func TestMCPRequireTokenDoesNotReplaceSemanticApprovalAuth(t *testing.T) {
	root := t.TempDir()
	server, err := NewServer(Config{RepoRoot: root, RequireToken: true, AuthToken: "mcp-token"})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer server.Close()
	fixture := completeUnattestedAvailableResultForMCP(t)
	server.proposalStore = &mcpApprovalPairStore{stored: &semantic.StoredProposal{
		WorkspaceID: server.approvalWorkspaceID, Proposal: fixture.Proposal, Pack: fixture.Pack,
	}}
	_, err = server.executeTool(context.Background(), "submit_semantic_approval", map[string]any{
		"target": root, "proposalId": "proposal-tokenless", "decision": "approved",
	})
	if err == nil || errors.Is(err, semantic.ErrApprovalUnauthenticated) || errors.Is(err, semantic.ErrApprovalUnauthorized) {
		t.Fatalf("tokenless request error = %v, want MCP token rejection before semantic gate", err)
	}

	_, err = server.executeTool(context.Background(), "submit_semantic_approval", map[string]any{
		"target": root, "commandId": "token-command", "proposalId": fixture.Proposal.ProposalID, "evidencePackId": fixture.Pack.EvidencePackID,
		"computedBasisId": fixture.Proposal.ComputedBasisID, "generationId": fixture.Proposal.GenerationID, "intentRevision": int64(1),
		"decision": "approve", "idempotencyKey": "token-key", "expectedApprovalVersion": int64(0), "expectedState": "none", "token": "mcp-token",
	})
	if err != nil {
		if !errors.Is(err, semantic.ErrApprovalExecutionUnavailable) && !errors.Is(err, semantic.ErrApprovalExecutionInvalid) {
			t.Fatalf("token-authenticated request: %v", err)
		}
	}
}

func TestMCPRejectsRequiredEmptyAuthToken(t *testing.T) {
	_, err := NewServer(Config{RepoRoot: t.TempDir(), RequireToken: true})
	if err == nil {
		t.Fatal("NewServer accepted RequireToken=true with an empty AuthToken")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "auth token") {
		t.Fatalf("NewServer error = %v, want an auth-token configuration error", err)
	}
}

func TestMCPRequiredEmptyAuthTokenFailsClosedDefensively(t *testing.T) {
	root := t.TempDir()
	server, err := NewServer(Config{RepoRoot: root, RequireToken: false})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer server.Close()

	server.cfg.RequireToken = true
	server.cfg.AuthToken = ""
	if err := server.checkAuth("caller-supplied-token"); err == nil {
		t.Fatal("checkAuth accepted a request with a required empty AuthToken")
	} else if strings.Contains(err.Error(), "caller-supplied-token") {
		t.Fatalf("checkAuth error exposed supplied token: %v", err)
	}

	_, err = server.executeTool(context.Background(), "submit_semantic_approval", map[string]any{
		"target": root, "proposalId": "proposal-empty-required-token", "decision": "approved", "token": "caller-supplied-token",
	})
	if err == nil {
		t.Fatal("semantic approval reached the mutation path with a required empty AuthToken")
	}
	if strings.Contains(err.Error(), "caller-supplied-token") {
		t.Fatalf("semantic approval error exposed supplied token: %v", err)
	}
}

func TestMCPSubmitSemanticApprovalRejectsRawParentTraversal(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "nested")
	if err := os.Mkdir(nested, 0o700); err != nil {
		t.Fatalf("create nested target: %v", err)
	}
	server, err := NewServer(Config{RepoRoot: root, RequireToken: false})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer server.Close()
	fixture := completeUnattestedAvailableResultForMCP(t)
	server.proposalStore = &mcpApprovalPairStore{stored: &semantic.StoredProposal{
		WorkspaceID: server.approvalWorkspaceID, Proposal: fixture.Proposal, Pack: fixture.Pack,
	}}

	separator := string(os.PathSeparator)
	relativeTraversal := "nested" + separator + ".."
	absoluteTraversal := root + separator + "nested" + separator + ".."
	for _, target := range []any{nil, "", ".", root} {
		args := mcpApprovalRequestArgs(root, fixture, "target-allowed")
		if target != nil {
			args["target"] = target
		}
		if _, err := server.executeTool(context.Background(), "submit_semantic_approval", args); !errors.Is(err, semantic.ErrApprovalExecutionUnavailable) {
			t.Fatalf("configured workspace target %#v error = %v, want authorized service attempt", target, err)
		}
	}
	for _, target := range []string{relativeTraversal, absoluteTraversal} {
		_, err := server.executeTool(context.Background(), "submit_semantic_approval", map[string]any{
			"target": target, "proposalId": fixture.Proposal.ProposalID, "evidencePackId": fixture.Pack.EvidencePackID, "decision": "approved",
		})
		if !errors.Is(err, semantic.ErrApprovalUnauthorized) {
			t.Fatalf("raw traversal target %q error = %v, want ErrApprovalUnauthorized", target, err)
		}
		if strings.Contains(err.Error(), root) || strings.Contains(err.Error(), target) {
			t.Fatalf("raw traversal target %q error exposed a path: %v", target, err)
		}
	}

	aliasParent := t.TempDir()
	rootAlias := filepath.Join(aliasParent, "configured-root-alias")
	if err := os.Symlink(root, rootAlias); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	_, err = server.executeTool(context.Background(), "submit_semantic_approval", func() map[string]any {
		args := mcpApprovalRequestArgs(rootAlias, fixture, "target-alias")
		return args
	}())
	if !errors.Is(err, semantic.ErrApprovalExecutionUnavailable) {
		t.Fatalf("same-root symlink target error = %v, want authorized service attempt", err)
	}
}

func TestMCPSubmitSemanticApprovalAuthorizationDenialDoesNotLoadProposal(t *testing.T) {
	root := t.TempDir()
	server, err := NewServer(Config{RepoRoot: root, RequireToken: false})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer server.Close()
	fixture := completeUnattestedAvailableResultForMCP(t)
	store := &mcpApprovalCountingStore{stored: &semantic.StoredProposal{
		WorkspaceID: server.approvalWorkspaceID,
		Proposal:    fixture.Proposal,
		Pack:        fixture.Pack,
	}}
	server.proposalStore = store
	args := func(target any) map[string]any {
		result := map[string]any{
			"target":         target,
			"proposalId":     fixture.Proposal.ProposalID,
			"evidencePackId": fixture.Pack.EvidencePackID,
			"decision":       "approved",
		}
		return result
	}
	assertDeniedWithoutLoad := func(name string, want error, request map[string]any) {
		beforeLoads := store.loads.Load()
		_, err := server.executeTool(context.Background(), "submit_semantic_approval", request)
		if !errors.Is(err, want) {
			t.Fatalf("%s error = %v, want %v", name, err, want)
		}
		if got := store.loads.Load(); got != beforeLoads {
			t.Fatalf("%s loaded proposal %d times, want no additional loads", name, got-beforeLoads)
		}
		approvalDB := filepath.Join(root, ".codeflow", "approval-transactions", "approval-transactions.sqlite3")
		if _, err := os.Stat(approvalDB); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s created approval transaction database: %v", name, err)
		}
	}

	server.approvalGate = nil
	assertDeniedWithoutLoad("missing authenticator", semantic.ErrApprovalUnauthenticated, args(root))

	server.approvalGate = semantic.NewApprovalAccessGate(semantic.NewLocalProcessApprovalAuthenticator(), nil)
	assertDeniedWithoutLoad("missing authorizer", semantic.ErrApprovalUnauthorized, args(root))

	workspaceID := server.approvalWorkspaceID
	server.approvalGate = semantic.NewApprovalAccessGate(semantic.NewLocalProcessApprovalAuthenticator(), semantic.NewApprovalWorkspaceAuthorizer(root))
	server.approvalWorkspaceID = workspaceID + "-mismatch"
	assertDeniedWithoutLoad("workspace mismatch", semantic.ErrApprovalUnauthorized, args(root))
	server.approvalWorkspaceID = workspaceID

	assertDeniedWithoutLoad("outside target", semantic.ErrApprovalUnauthorized, args(t.TempDir()))

	tokenServer, err := NewServer(Config{RepoRoot: t.TempDir(), RequireToken: true, AuthToken: "mcp-token"})
	if err != nil {
		t.Fatalf("NewServer token server: %v", err)
	}
	defer tokenServer.Close()
	tokenStore := &mcpApprovalCountingStore{stored: store.stored}
	tokenServer.proposalStore = tokenStore
	if _, err := tokenServer.executeTool(context.Background(), "submit_semantic_approval", args(tokenServer.cfg.RepoRoot)); err == nil {
		t.Fatal("missing MCP token unexpectedly succeeded")
	}
	if tokenStore.loads.Load() != 0 {
		t.Fatalf("missing MCP token reached proposal load seam: loads=%d", tokenStore.loads.Load())
	}
	if _, err := os.Stat(filepath.Join(tokenServer.cfg.RepoRoot, ".codeflow", "approval-transactions", "approval-transactions.sqlite3")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing MCP token created approval transaction database: %v", err)
	}
}

type mcpApprovalPairStore struct {
	stored *semantic.StoredProposal
}

type mcpApprovalReturningStore struct {
	stored *semantic.StoredProposal
}

type mcpApprovalCountingStore struct {
	stored *semantic.StoredProposal
	loads  atomic.Int64
}

func (*mcpApprovalPairStore) SaveEnrichmentResult(context.Context, string, *semantic.EnrichmentResult) error {
	return errors.New("approval test store is read-only")
}

func (*mcpApprovalReturningStore) SaveEnrichmentResult(context.Context, string, *semantic.EnrichmentResult) error {
	return errors.New("approval test store is read-only")
}

func (*mcpApprovalCountingStore) SaveEnrichmentResult(context.Context, string, *semantic.EnrichmentResult) error {
	return errors.New("approval test store is read-only")
}

func (s *mcpApprovalReturningStore) Load(context.Context, string, string, string) (*semantic.StoredProposal, error) {
	if s == nil {
		return nil, nil
	}
	return s.stored, nil
}

func (s *mcpApprovalCountingStore) Load(_ context.Context, workspaceID, proposalID, evidencePackID string) (*semantic.StoredProposal, error) {
	s.loads.Add(1)
	if s == nil || s.stored == nil || s.stored.WorkspaceID != workspaceID || s.stored.Proposal == nil || s.stored.Pack == nil || s.stored.Proposal.ProposalID != proposalID || s.stored.Pack.EvidencePackID != evidencePackID {
		return nil, &semantic.ProposalNotFoundError{WorkspaceID: workspaceID, ProposalID: proposalID, EvidencePackID: evidencePackID}
	}
	return s.stored, nil
}

func (s *mcpApprovalPairStore) Load(_ context.Context, workspaceID, proposalID, evidencePackID string) (*semantic.StoredProposal, error) {
	if s == nil || s.stored == nil || s.stored.WorkspaceID != workspaceID || s.stored.Proposal == nil || s.stored.Pack == nil || s.stored.Proposal.ProposalID != proposalID || s.stored.Pack.EvidencePackID != evidencePackID {
		return nil, &semantic.ProposalNotFoundError{WorkspaceID: workspaceID, ProposalID: proposalID, EvidencePackID: evidencePackID}
	}
	return s.stored, nil
}

func mcpApprovalRequestArgs(target string, fixture semantic.EnrichmentResult, key string) map[string]any {
	return map[string]any{
		"target":                  target,
		"commandId":               "command-" + key,
		"proposalId":              fixture.Proposal.ProposalID,
		"evidencePackId":          fixture.Pack.EvidencePackID,
		"computedBasisId":         fixture.Proposal.ComputedBasisID,
		"generationId":            fixture.Proposal.GenerationID,
		"intentRevision":          int64(1),
		"decision":                "approve",
		"idempotencyKey":          "idempotency-" + key,
		"expectedApprovalVersion": int64(0),
		"expectedState":           "none",
	}
}
