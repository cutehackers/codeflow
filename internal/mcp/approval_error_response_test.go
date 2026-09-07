package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"codeflow/internal/semantic"
)

type mcpApprovalErrorRPCResponse struct {
	Result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	} `json:"result"`
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

type blockingMCPApprovalAuthenticator struct {
	entered chan struct{}
	once    sync.Once
}

func (a *blockingMCPApprovalAuthenticator) Authenticate(ctx context.Context) (semantic.ApprovalActorClaims, error) {
	a.once.Do(func() { close(a.entered) })
	<-ctx.Done()
	return semantic.ApprovalActorClaims{}, errors.Join(semantic.ErrApprovalUnauthenticated, ctx.Err())
}

func serveMCPApprovalRequest(ctx context.Context, server *Server, rawArguments []byte) (mcpApprovalErrorRPCResponse, error) {
	rpc := `{"jsonrpc":"2.0","id":"approval-context-error","method":"tools/call","params":{"name":"submit_semantic_approval","arguments":` + string(rawArguments) + `}}` + "\n"
	var output bytes.Buffer
	serveErr := server.Serve(ctx, strings.NewReader(rpc), &output)
	var response mcpApprovalErrorRPCResponse
	if err := json.Unmarshal(output.Bytes(), &response); err != nil {
		return response, err
	}
	return response, serveErr
}

func mcpContextCancellationApprovalArguments(t *testing.T, root string) []byte {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"target":                  root,
		"commandId":               "command-context-error",
		"proposalId":              "proposal-context-error",
		"evidencePackId":          "pack-context-error",
		"computedBasisId":         "basis-context-error",
		"generationId":            "generation-context-error",
		"intentRevision":          int64(1),
		"decision":                "approve",
		"idempotencyKey":          "key-context-error",
		"expectedApprovalVersion": int64(0),
		"expectedState":           "none",
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func assertMCPApprovalErrorResponse(t *testing.T, response mcpApprovalErrorRPCResponse, root, code string) {
	t.Helper()
	wantMessage := map[string]string{
		"approval_unavailable":     "approval service is unavailable",
		"approval_unauthenticated": "approval authentication is required",
	}
	if response.Error != nil {
		t.Fatalf("approval response returned JSON-RPC error: %+v", response.Error)
	}
	if !response.Result.IsError || len(response.Result.Content) != 1 || response.Result.Content[0].Type != "text" {
		t.Fatalf("approval response = %+v, want one error text content", response)
	}
	text := response.Result.Content[0].Text
	var payload map[string]any
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		t.Fatalf("approval error content is not JSON: %v text=%q", err, text)
	}
	want := map[string]any{"code": code, "message": wantMessage[code]}
	if !reflect.DeepEqual(payload, want) {
		t.Fatalf("approval error payload = %#v, want %#v", payload, want)
	}
	if strings.HasPrefix(text, "Error:") || strings.Contains(text, root) {
		t.Fatalf("approval error leaked transport/error detail: %q", text)
	}
}

func assertMCPApprovalStoreEmpty(t *testing.T, server *Server, root string) {
	t.Helper()
	databasePath := filepath.Join(root, ".codeflow", "approval-transactions", "approval-transactions.sqlite3")
	if _, err := os.Stat(databasePath); errors.Is(err, os.ErrNotExist) {
		return
	} else if err != nil {
		t.Fatalf("approval database stat: %v", err)
	}
	engine, err := server.getSnapshotEngine(root)
	if err != nil {
		t.Fatalf("approval engine: %v", err)
	}
	snapshot, err := semantic.NewApprovalTransactionStore(root, engine).Snapshot(context.Background())
	if err != nil {
		t.Fatalf("approval snapshot: %v", err)
	}
	if len(snapshot.Events) != 0 || len(snapshot.Aggregates) != 0 || len(snapshot.IdempotencyResults) != 0 || len(snapshot.Outbox) != 0 {
		t.Fatalf("published approval rows: events=%d aggregates=%d idempotency=%d outbox=%d", len(snapshot.Events), len(snapshot.Aggregates), len(snapshot.IdempotencyResults), len(snapshot.Outbox))
	}
}

func TestClassifyMCPApprovalErrorContextPrecedesAuth(t *testing.T) {
	for _, test := range []struct {
		name      string
		authCause error
		cause     error
	}{
		{name: "unauthenticated-canceled", authCause: semantic.ErrApprovalUnauthenticated, cause: context.Canceled},
		{name: "unauthenticated-deadline", authCause: semantic.ErrApprovalUnauthenticated, cause: context.DeadlineExceeded},
		{name: "unauthorized-canceled", authCause: semantic.ErrApprovalUnauthorized, cause: context.Canceled},
		{name: "unauthorized-deadline", authCause: semantic.ErrApprovalUnauthorized, cause: context.DeadlineExceeded},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := errors.Join(test.authCause, test.cause)
			if got := classifyMCPApprovalError(err); got != "approval_unavailable" {
				t.Fatalf("classifyMCPApprovalError(%v) = %q, want approval_unavailable", test.cause, got)
			}
		})
	}
}

func TestMCPServeSemanticApprovalContextCancellationIsUnavailable(t *testing.T) {
	root := t.TempDir()
	server, err := NewServer(Config{RepoRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	authenticator := &blockingMCPApprovalAuthenticator{entered: make(chan struct{})}
	server.approvalGate = semantic.NewApprovalAccessGate(authenticator, semantic.NewApprovalWorkspaceAuthorizer(root))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	rawArguments := mcpContextCancellationApprovalArguments(t, root)
	var response mcpApprovalErrorRPCResponse
	var serveErr error
	go func() {
		response, serveErr = serveMCPApprovalRequest(ctx, server, rawArguments)
		close(done)
	}()
	select {
	case <-authenticator.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("approval authenticator did not enter blocking boundary")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return after approval context cancellation")
	}
	if serveErr != nil {
		t.Fatalf("Serve returned error after writing approval result: %v", serveErr)
	}
	assertMCPApprovalErrorResponse(t, response, root, "approval_unavailable")
	assertMCPApprovalStoreEmpty(t, server, root)
}

func TestMCPServeSemanticApprovalErrorResponses(t *testing.T) {
	wantMessage := map[string]string{
		"approval_invalid":         "approval request is invalid",
		"approval_conflict":        "approval request conflicts with current state",
		"approval_unavailable":     "approval service is unavailable",
		"approval_unauthenticated": "approval authentication is required",
		"approval_unauthorized":    "approval workspace is not authorized",
	}

	assertError := func(t *testing.T, response mcpApprovalErrorRPCResponse, root, code string) {
		t.Helper()
		if response.Error != nil {
			t.Fatalf("approval response returned JSON-RPC error: %+v", response.Error)
		}
		if !response.Result.IsError || len(response.Result.Content) != 1 || response.Result.Content[0].Type != "text" {
			t.Fatalf("approval response = %+v, want one error text content", response)
		}
		text := response.Result.Content[0].Text
		var payload map[string]any
		if err := json.Unmarshal([]byte(text), &payload); err != nil {
			t.Fatalf("approval error content is not JSON: %v text=%q", err, text)
		}
		want := map[string]any{"code": code, "message": wantMessage[code]}
		if !reflect.DeepEqual(payload, want) {
			t.Fatalf("approval error payload = %#v, want %#v", payload, want)
		}
		if strings.HasPrefix(text, "Error:") || strings.Contains(text, root) {
			t.Fatalf("approval error leaked transport/error detail: %q", text)
		}
	}
	assertEmptyStore := func(t *testing.T, server *Server, root, name string) {
		t.Helper()
		databasePath := filepath.Join(root, ".codeflow", "approval-transactions", "approval-transactions.sqlite3")
		if _, err := os.Stat(databasePath); errors.Is(err, os.ErrNotExist) {
			return
		} else if err != nil {
			t.Fatalf("%s approval database stat: %v", name, err)
		}
		engine, err := server.getSnapshotEngine(root)
		if err != nil {
			t.Fatalf("%s approval engine: %v", name, err)
		}
		snapshot, err := semantic.NewApprovalTransactionStore(root, engine).Snapshot(context.Background())
		if err != nil {
			t.Fatalf("%s approval snapshot: %v", name, err)
		}
		if len(snapshot.Events) != 0 || len(snapshot.Aggregates) != 0 || len(snapshot.IdempotencyResults) != 0 || len(snapshot.Outbox) != 0 {
			t.Fatalf("%s published approval rows: events=%d aggregates=%d idempotency=%d outbox=%d", name, len(snapshot.Events), len(snapshot.Aggregates), len(snapshot.IdempotencyResults), len(snapshot.Outbox))
		}
	}

	serve := func(t *testing.T, server *Server, rawArguments []byte) mcpApprovalErrorRPCResponse {
		t.Helper()
		rpc := `{"jsonrpc":"2.0","id":"approval-error","method":"tools/call","params":{"name":"submit_semantic_approval","arguments":` + string(rawArguments) + `}}` + "\n"
		var output bytes.Buffer
		if err := server.Serve(context.Background(), strings.NewReader(rpc), &output); err != nil {
			t.Fatalf("Serve approval request: %v", err)
		}
		var response mcpApprovalErrorRPCResponse
		if err := json.Unmarshal(output.Bytes(), &response); err != nil {
			t.Fatalf("decode approval response: %v output=%s", err, output.Bytes())
		}
		return response
	}
	validArguments := func(root string) []byte {
		raw, err := json.Marshal(map[string]any{
			"target":                  root,
			"commandId":               "command-error-response",
			"proposalId":              "proposal-error-response",
			"evidencePackId":          "pack-error-response",
			"computedBasisId":         "basis-error-response",
			"generationId":            "generation-error-response",
			"intentRevision":          int64(1),
			"decision":                "approve",
			"idempotencyKey":          "key-error-response",
			"expectedApprovalVersion": int64(0),
			"expectedState":           "none",
		})
		if err != nil {
			t.Fatalf("marshal approval request: %v", err)
		}
		return raw
	}

	t.Run("invalid", func(t *testing.T) {
		root := t.TempDir()
		server, err := NewServer(Config{RepoRoot: root})
		if err != nil {
			t.Fatal(err)
		}
		defer server.Close()
		valid := validArguments(root)
		duplicate := bytes.Replace(valid, []byte(`"decision":"approve"`), []byte(`"decision":"approve","decision":"approve"`), 1)
		assertError(t, serve(t, server, duplicate), root, "approval_invalid")
		assertEmptyStore(t, server, root, "invalid")
	})

	t.Run("unauthenticated", func(t *testing.T) {
		root := t.TempDir()
		server, err := NewServer(Config{RepoRoot: root, RequireToken: true, AuthToken: "approval-token"})
		if err != nil {
			t.Fatal(err)
		}
		defer server.Close()
		assertError(t, serve(t, server, validArguments(root)), root, "approval_unauthenticated")
		assertEmptyStore(t, server, root, "unauthenticated")
	})

	t.Run("unauthorized", func(t *testing.T) {
		root := t.TempDir()
		outside := t.TempDir()
		server, err := NewServer(Config{RepoRoot: root})
		if err != nil {
			t.Fatal(err)
		}
		defer server.Close()
		assertError(t, serve(t, server, validArguments(outside)), root, "approval_unauthorized")
		assertEmptyStore(t, server, root, "unauthorized")
	})

	t.Run("unavailable", func(t *testing.T) {
		root := t.TempDir()
		server, err := NewServer(Config{RepoRoot: root})
		if err != nil {
			t.Fatal(err)
		}
		defer server.Close()
		assertError(t, serve(t, server, validArguments(root)), root, "approval_unavailable")
		assertEmptyStore(t, server, root, "unavailable")
	})

	t.Run("conflict", func(t *testing.T) {
		fixture := newMCPEnrichmentFactoryFixture(t)
		defer fixture.server.Close()
		value, err := fixture.server.executeTool(context.Background(), "request_semantic_enrichment", map[string]any{
			"target": fixture.root, "generationId": fixture.generation, "targetStepId": fixture.stepID, "promptRevision": "prompt-error-conflict",
		})
		if err != nil {
			t.Fatalf("MCP enrichment failed: %v", err)
		}
		result, ok := value.(*semantic.EnrichmentResult)
		if !ok || result == nil || result.Proposal == nil || result.Pack == nil || result.State.Status != "available" {
			t.Fatalf("MCP enrichment result = %#v, want available durable pair", value)
		}
		arguments := mcpApprovalRequestArgs(fixture.root, *result, "mcp-error-conflict")
		valid, err := json.Marshal(arguments)
		if err != nil {
			t.Fatal(err)
		}
		first := serve(t, fixture.server, valid)
		if first.Error != nil || first.Result.IsError {
			t.Fatalf("valid approval response = %+v, want success", first)
		}
		engine, err := fixture.server.getSnapshotEngine(fixture.root)
		if err != nil {
			t.Fatal(err)
		}
		transactions := semantic.NewApprovalTransactionStore(fixture.root, engine)
		before, err := transactions.Snapshot(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if len(before.Events) != 1 || len(before.Aggregates) != 1 || len(before.IdempotencyResults) != 1 || len(before.Outbox) != 1 {
			t.Fatalf("initial approval rows = events=%d aggregates=%d idempotency=%d outbox=%d, want 1 each", len(before.Events), len(before.Aggregates), len(before.IdempotencyResults), len(before.Outbox))
		}
		arguments["generationId"] = result.Proposal.GenerationID + "-stale"
		stale, err := json.Marshal(arguments)
		if err != nil {
			t.Fatal(err)
		}
		assertError(t, serve(t, fixture.server, stale), fixture.root, "approval_conflict")
		after, err := transactions.Snapshot(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(before, after) {
			t.Fatalf("conflict changed durable approval snapshot: before=%+v after=%+v", before, after)
		}
	})
}
