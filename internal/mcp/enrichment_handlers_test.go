package mcp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"codeflow/internal/protocol"
	"codeflow/internal/semantic"
)

func TestRequestSemanticEnrichment_ReturnsExplicitUnavailableWithoutCachedMap(t *testing.T) {
	root := t.TempDir()
	srv, err := NewServer(Config{RepoRoot: root, RequireToken: false})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}
	defer srv.Close()

	value, err := srv.executeTool(context.Background(), "request_semantic_enrichment", map[string]any{
		"target": root,
	})
	if err != nil {
		t.Fatalf("executeTool failed: %v", err)
	}
	result, ok := value.(*semantic.EnrichmentResult)
	if !ok {
		t.Fatalf("result type = %T, want *semantic.EnrichmentResult", value)
	}
	if result.State.Status != "unavailable" || result.State.Capability.Status != "unavailable" {
		t.Fatalf("state = %+v, want unavailable with unavailable capability", result.State)
	}
	if result.Pack != nil || result.Proposal != nil {
		t.Fatalf("unavailable result exposed model artifacts: %+v", result)
	}

	public, err := marshalPublicMCPResult(result)
	if err != nil {
		t.Fatalf("marshal public result: %v", err)
	}
	var envelope map[string]any
	if err := json.Unmarshal(public, &envelope); err != nil {
		t.Fatalf("decode public result: %v", err)
	}
	state, ok := envelope["state"].(map[string]any)
	if !ok || state["status"] != "unavailable" {
		t.Fatalf("public state = %#v", envelope["state"])
	}
}

func TestMCPSubmitSemanticApprovalUsesDurableExecutionService(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") == "mcp" {
		t.Skip("helper process")
	}
	fixture := newMCPEnrichmentFactoryFixture(t)
	defer fixture.server.Close()

	value, err := fixture.server.executeTool(context.Background(), "request_semantic_enrichment", map[string]any{
		"target": fixture.root, "generationId": fixture.generation, "targetStepId": fixture.stepID, "promptRevision": "prompt-mcp-persistence",
	})
	if err != nil {
		t.Fatalf("MCP enrichment failed: %v", err)
	}
	result, ok := value.(*semantic.EnrichmentResult)
	if !ok || result == nil || result.State.Status != "available" || result.Proposal == nil || result.Pack == nil {
		t.Fatalf("MCP enrichment result = %#v, want available proposal and pack", value)
	}

	// A fresh durable store instance must be sufficient for an approval lookup.
	store := semantic.NewDurableProposalStore(fixture.root)
	loaded, err := semantic.LoadProposalForApproval(context.Background(), store, fixture.server.approvalWorkspaceID, result.Proposal.ProposalID, result.Pack.EvidencePackID)
	if err != nil {
		t.Fatalf("restart proposal lookup: %v", err)
	}
	if loaded == nil || loaded.WorkspaceID != fixture.server.approvalWorkspaceID || !reflect.DeepEqual(loaded.Proposal, result.Proposal) || !reflect.DeepEqual(loaded.Pack, result.Pack) {
		t.Fatalf("restart proposal pair = %#v, want exact available result", loaded)
	}

	// The approval handler uses the exact pair from a fresh store instance and
	// commits through the v2 execution service.
	fixture.server.proposalStore = store
	approvalArgs := mcpApprovalRequestArgs(fixture.root, *result, "mcp-restart")
	approvalValue, err := fixture.server.executeTool(context.Background(), "submit_semantic_approval", approvalArgs)
	if err != nil {
		t.Fatalf("approval from restarted durable store failed: %v", err)
	}
	firstApproval, ok := approvalValue.(semantic.ApprovalExecutionResult)
	if !ok || firstApproval.Receipt.Replayed || firstApproval.Receipt.Event.EventID == "" || firstApproval.Receipt.Aggregate.Version != 1 {
		t.Fatalf("first approval result = %#v, want non-replayed version-one commit", approvalValue)
	}
	access, err := fixture.server.approvalGate.AuthenticateAndAuthorize(context.Background(), fixture.server.approvalWorkspaceID, fixture.root)
	if err != nil {
		t.Fatalf("derive approval authority: %v", err)
	}
	if event := firstApproval.Receipt.Event; event.ActorID != access.Actor().ActorID || event.SessionID != access.Actor().SessionID || event.WorkspaceID != access.Workspace().WorkspaceID() {
		t.Fatalf("approval event authority = actor %q session %q workspace %q, want gate-derived actor/session/workspace", event.ActorID, event.SessionID, event.WorkspaceID)
	}
	wire, err := json.Marshal(firstApproval)
	if err != nil {
		t.Fatalf("marshal approval result: %v", err)
	}
	var wireObject map[string]any
	if err := json.Unmarshal(wire, &wireObject); err != nil {
		t.Fatalf("decode approval result: %v", err)
	}
	for _, key := range []string{"receipt", "generationId", "computedBasisId", "validatedSnapshotId", "intentRevision", "freshness"} {
		if _, ok := wireObject[key]; !ok {
			t.Fatalf("approval result missing lower-camel key %q: %s", key, wire)
		}
	}
	if _, ok := wireObject["Receipt"]; ok {
		t.Fatalf("approval result exposed upper-case field: %s", wire)
	}
	engine, err := fixture.server.getSnapshotEngine(fixture.root)
	if err != nil {
		t.Fatalf("snapshot engine for approval audit: %v", err)
	}
	transactionStore := semantic.NewApprovalTransactionStore(fixture.root, engine)
	beforeApprovalSnapshot, err := transactionStore.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("approval snapshot: %v", err)
	}
	if len(beforeApprovalSnapshot.Events) != 1 || len(beforeApprovalSnapshot.Aggregates) != 1 || len(beforeApprovalSnapshot.IdempotencyResults) != 1 || len(beforeApprovalSnapshot.Outbox) != 1 {
		t.Fatalf("approval snapshot counts = events %d aggregates %d idempotency %d outbox %d, want 1 each", len(beforeApprovalSnapshot.Events), len(beforeApprovalSnapshot.Aggregates), len(beforeApprovalSnapshot.IdempotencyResults), len(beforeApprovalSnapshot.Outbox))
	}
	retryValue, err := fixture.server.executeTool(context.Background(), "submit_semantic_approval", approvalArgs)
	if err != nil {
		t.Fatalf("exact approval retry failed: %v", err)
	}
	retryApproval, ok := retryValue.(semantic.ApprovalExecutionResult)
	firstReplayComparable := firstApproval
	firstReplayComparable.Receipt.Replayed = true
	if !ok || !retryApproval.Receipt.Replayed || !reflect.DeepEqual(firstReplayComparable, retryApproval) || !reflect.DeepEqual(firstApproval.Receipt.EventPayload, retryApproval.Receipt.EventPayload) {
		t.Fatalf("exact approval retry = %#v, want same result with Replayed=true", retryValue)
	}
	afterApprovalSnapshot, err := transactionStore.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("approval retry snapshot: %v", err)
	}
	if len(afterApprovalSnapshot.Events) != 1 || len(afterApprovalSnapshot.Aggregates) != 1 || len(afterApprovalSnapshot.IdempotencyResults) != 1 || len(afterApprovalSnapshot.Outbox) != 1 {
		t.Fatalf("approval retry changed durable counts: events %d aggregates %d idempotency %d outbox %d", len(afterApprovalSnapshot.Events), len(afterApprovalSnapshot.Aggregates), len(afterApprovalSnapshot.IdempotencyResults), len(afterApprovalSnapshot.Outbox))
	}
	oldDecision := mcpApprovalRequestArgs(fixture.root, *result, "mcp-old-decision")
	oldDecision["decision"] = "approved"
	if _, err := fixture.server.executeTool(context.Background(), "submit_semantic_approval", oldDecision); !errors.Is(err, semantic.ErrApprovalExecutionInvalid) {
		t.Fatalf("legacy decision error = %v, want ErrApprovalExecutionInvalid", err)
	}
	stale := mcpApprovalRequestArgs(fixture.root, *result, "mcp-stale")
	stale["generationId"] = result.Proposal.GenerationID + "-stale"
	if _, err := fixture.server.executeTool(context.Background(), "submit_semantic_approval", stale); !errors.Is(err, semantic.ErrApprovalExecutionConflict) {
		t.Fatalf("stale generation error = %v, want ErrApprovalExecutionConflict", err)
	}
	unchangedSnapshot, err := transactionStore.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("snapshot after rejected commands: %v", err)
	}
	if len(unchangedSnapshot.Events) != 1 || len(unchangedSnapshot.Aggregates) != 1 || len(unchangedSnapshot.IdempotencyResults) != 1 || len(unchangedSnapshot.Outbox) != 1 {
		t.Fatalf("rejected commands changed durable counts: events %d aggregates %d idempotency %d outbox %d", len(unchangedSnapshot.Events), len(unchangedSnapshot.Aggregates), len(unchangedSnapshot.IdempotencyResults), len(unchangedSnapshot.Outbox))
	}

	if _, err := fixture.server.executeTool(context.Background(), "submit_semantic_approval", func() map[string]any {
		args := mcpApprovalRequestArgs(fixture.root, *result, "mcp-mismatch")
		args["proposalId"] = result.Proposal.ProposalID + "-wrong"
		return args
	}()); !errors.Is(err, semantic.ErrApprovalExecutionUnavailable) {
		t.Fatalf("mismatched approval lookup error = %v, want ErrApprovalExecutionUnavailable", err)
	}

	records, err := filepath.Glob(filepath.Join(fixture.root, ".codeflow", "semantic-proposals", "*.json"))
	if err != nil || len(records) != 1 {
		t.Fatalf("durable proposal records = %v, err=%v, want one record", records, err)
	}
	if err := os.WriteFile(records[0], []byte(`{"storeVersion":1,"workspaceId":"corrupt"}`), 0o600); err != nil {
		t.Fatalf("corrupt durable proposal record: %v", err)
	}
	corruptArgs := mcpApprovalRequestArgs(fixture.root, *result, "mcp-corrupt")
	if _, err := fixture.server.executeTool(context.Background(), "submit_semantic_approval", corruptArgs); !errors.Is(err, semantic.ErrApprovalExecutionInvalid) || !errors.Is(err, semantic.ErrProposalInvalid) {
		t.Fatalf("corrupt approval lookup error = %v, want typed ErrProposalInvalid and ErrApprovalExecutionInvalid", err)
	}
}

func TestMCPServeSemanticApprovalRejectsDuplicateAndUnknownRawArguments(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") == "mcp" {
		t.Skip("helper process")
	}
	fixture := newMCPEnrichmentFactoryFixture(t)
	defer fixture.server.Close()
	value, err := fixture.server.executeTool(context.Background(), "request_semantic_enrichment", map[string]any{
		"target": fixture.root, "generationId": fixture.generation, "targetStepId": fixture.stepID, "promptRevision": "prompt-mcp-raw-draft",
	})
	if err != nil {
		t.Fatalf("MCP enrichment failed: %v", err)
	}
	result, ok := value.(*semantic.EnrichmentResult)
	if !ok || result == nil || result.State.Status != "available" || result.Proposal == nil || result.Pack == nil {
		t.Fatalf("MCP enrichment result = %#v, want available proposal and pack", value)
	}
	args, err := json.Marshal(mcpApprovalRequestArgs(fixture.root, *result, "mcp-raw-draft"))
	if err != nil {
		t.Fatalf("marshal approval arguments: %v", err)
	}
	duplicate := bytes.Replace(args, []byte(`"decision":"approve"`), []byte(`"decision":"approve","decision":"approve"`), 1)
	if bytes.Equal(duplicate, args) {
		t.Fatal("duplicate decision fixture did not modify raw arguments")
	}
	unknown := append(append([]byte{}, args[:len(args)-1]...), []byte(`,"unexpected":true}`)...)
	serve := func(rawArgs []byte) {
		rpc := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"submit_semantic_approval","arguments":` + string(rawArgs) + `}}` + "\n"
		var out bytes.Buffer
		if err := fixture.server.Serve(context.Background(), strings.NewReader(rpc), &out); err != nil {
			t.Fatalf("Serve: %v", err)
		}
		var response struct {
			Result struct {
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
				IsError bool `json:"isError"`
			} `json:"result"`
		}
		if err := json.Unmarshal(out.Bytes(), &response); err != nil {
			t.Fatalf("decode raw approval response: %v output=%s", err, out.Bytes())
		}
		if !response.Result.IsError || len(response.Result.Content) != 1 || response.Result.Content[0].Text == "" {
			t.Fatalf("raw approval response = %s, want one MCP error content", out.Bytes())
		}
		if strings.Contains(response.Result.Content[0].Text, fixture.root) {
			t.Fatalf("raw approval error leaked repository path: %s", response.Result.Content[0].Text)
		}
		approvalDB := filepath.Join(fixture.root, ".codeflow", "approval-transactions", "approval-transactions.sqlite3")
		if _, err := os.Stat(approvalDB); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("raw draft rejection created approval transaction database: %v", err)
		}
	}
	serve(duplicate)
	serve(unknown)
}

func TestMCPServeSemanticApprovalUsesRawArgumentsSuccessControl(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") == "mcp" {
		t.Skip("helper process")
	}
	fixture := newMCPEnrichmentFactoryFixture(t)
	defer fixture.server.Close()
	value, err := fixture.server.executeTool(context.Background(), "request_semantic_enrichment", map[string]any{
		"target": fixture.root, "generationId": fixture.generation, "targetStepId": fixture.stepID, "promptRevision": "prompt-mcp-serve-approval",
	})
	if err != nil {
		t.Fatalf("MCP enrichment failed: %v", err)
	}
	result, ok := value.(*semantic.EnrichmentResult)
	if !ok || result == nil || result.State.Status != "available" || result.Proposal == nil || result.Pack == nil {
		t.Fatalf("MCP enrichment result = %#v, want available proposal and pack", value)
	}

	args, err := json.Marshal(mcpApprovalRequestArgs(fixture.root, *result, "mcp-serve-success"))
	if err != nil {
		t.Fatalf("marshal approval arguments: %v", err)
	}
	duplicate := bytes.Replace(args, []byte(`"decision":"approve"`), []byte(`"decision":"approve","decision":"approve"`), 1)
	if bytes.Equal(duplicate, args) {
		t.Fatal("duplicate decision fixture did not modify raw arguments")
	}
	unknown := append(append([]byte{}, args[:len(args)-1]...), []byte(`,"unexpected":true}`)...)

	type serveContent struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	type serveResult struct {
		Content []serveContent `json:"content"`
		IsError bool           `json:"isError"`
	}
	type serveResponse struct {
		Result serveResult `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	serve := func(rawArgs []byte) (serveResponse, error) {
		rpc := `{"jsonrpc":"2.0","id":"serve-approval","method":"tools/call","params":{"name":"submit_semantic_approval","arguments":` + string(rawArgs) + `}}` + "\n"
		var out bytes.Buffer
		if err := fixture.server.Serve(context.Background(), strings.NewReader(rpc), &out); err != nil {
			return serveResponse{}, err
		}
		var response serveResponse
		if err := json.Unmarshal(out.Bytes(), &response); err != nil {
			return serveResponse{}, fmt.Errorf("decode Serve response: %w", err)
		}
		return response, nil
	}
	assertRejected := func(name string, rawArgs []byte) {
		response, err := serve(rawArgs)
		if err != nil {
			t.Fatalf("%s Serve: %v", name, err)
		}
		if response.Error != nil || !response.Result.IsError || len(response.Result.Content) != 1 || response.Result.Content[0].Text == "" {
			t.Fatalf("%s response = %+v, want one MCP error content", name, response)
		}
		if strings.Contains(response.Result.Content[0].Text, fixture.root) {
			t.Fatalf("%s error leaked repository path: %s", name, response.Result.Content[0].Text)
		}
	}
	assertZeroApprovalRows := func(name string) {
		engine, err := fixture.server.getSnapshotEngine(fixture.root)
		if err != nil {
			t.Fatalf("%s snapshot engine: %v", name, err)
		}
		transactions := semantic.NewApprovalTransactionStore(fixture.root, engine)
		snapshot, err := transactions.Snapshot(context.Background())
		if err != nil {
			t.Fatalf("%s approval snapshot: %v", name, err)
		}
		if len(snapshot.Events) != 0 || len(snapshot.Aggregates) != 0 || len(snapshot.IdempotencyResults) != 0 || len(snapshot.Outbox) != 0 {
			t.Fatalf("%s published approval rows: events=%d aggregates=%d idempotency=%d outbox=%d", name, len(snapshot.Events), len(snapshot.Aggregates), len(snapshot.IdempotencyResults), len(snapshot.Outbox))
		}
	}
	assertRejected("duplicate before success", duplicate)
	assertZeroApprovalRows("duplicate before success")
	assertRejected("unknown before success", unknown)
	assertZeroApprovalRows("unknown before success")

	validResponse, err := serve(args)
	if err != nil {
		t.Fatalf("valid Serve approval: %v", err)
	}
	if validResponse.Error != nil || validResponse.Result.IsError || len(validResponse.Result.Content) != 1 || validResponse.Result.Content[0].Type != "text" {
		t.Fatalf("valid Serve response = %+v, want successful text result", validResponse)
	}
	var wireResult map[string]json.RawMessage
	if err := json.Unmarshal([]byte(validResponse.Result.Content[0].Text), &wireResult); err != nil {
		t.Fatalf("decode lower-camel approval result: %v text=%s", err, validResponse.Result.Content[0].Text)
	}
	for _, key := range []string{"receipt", "generationId", "computedBasisId", "validatedSnapshotId", "intentRevision", "freshness"} {
		if _, ok := wireResult[key]; !ok {
			t.Fatalf("Serve approval result missing lower-camel key %q: %s", key, validResponse.Result.Content[0].Text)
		}
	}
	if _, ok := wireResult["Receipt"]; ok {
		t.Fatalf("Serve approval result exposed upper-case field: %s", validResponse.Result.Content[0].Text)
	}
	var approval semantic.ApprovalExecutionResult
	if err := json.Unmarshal([]byte(validResponse.Result.Content[0].Text), &approval); err != nil {
		t.Fatalf("decode approval result: %v", err)
	}
	if approval.Receipt.Replayed || approval.Receipt.Event.EventID == "" || approval.Receipt.Event.ApprovalID == "" || approval.Receipt.Aggregate.Version != 1 || approval.Receipt.Aggregate.State != "active" {
		t.Fatalf("Serve approval result = %+v, want non-replayed active version-one result", approval)
	}
	access, err := fixture.server.approvalGate.AuthenticateAndAuthorize(context.Background(), fixture.server.approvalWorkspaceID, fixture.root)
	if err != nil {
		t.Fatalf("derive approval authority: %v", err)
	}
	event := approval.Receipt.Event
	if event.ActorID != access.Actor().ActorID || event.SessionID != access.Actor().SessionID || event.WorkspaceID != access.Workspace().WorkspaceID() {
		t.Fatalf("Serve approval attribution = actor %q session %q workspace %q, want gate-derived values", event.ActorID, event.SessionID, event.WorkspaceID)
	}
	if event.ProposalID != result.Proposal.ProposalID || event.EvidencePackID != result.Pack.EvidencePackID || event.ComputedBasisID != result.Proposal.ComputedBasisID || event.GenerationID != result.Proposal.GenerationID {
		t.Fatalf("Serve approval references = proposal %q pack %q basis %q generation %q, want durable pair", event.ProposalID, event.EvidencePackID, event.ComputedBasisID, event.GenerationID)
	}
	if approval.GenerationID != result.Proposal.GenerationID || approval.ComputedBasisID != result.Proposal.ComputedBasisID || approval.ValidatedSnapshotID == "" || approval.Freshness != "current" {
		t.Fatalf("Serve approval proof identity = %+v, want current durable proof", approval)
	}

	engine, err := fixture.server.getSnapshotEngine(fixture.root)
	if err != nil {
		t.Fatalf("snapshot engine after Serve approval: %v", err)
	}
	transactions := semantic.NewApprovalTransactionStore(fixture.root, engine)
	beforeMalformed, err := transactions.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("approval snapshot after success: %v", err)
	}
	if len(beforeMalformed.Events) != 1 || len(beforeMalformed.Aggregates) != 1 || len(beforeMalformed.IdempotencyResults) != 1 || len(beforeMalformed.Outbox) != 1 {
		t.Fatalf("Serve approval row counts = events=%d aggregates=%d idempotency=%d outbox=%d, want 1 each", len(beforeMalformed.Events), len(beforeMalformed.Aggregates), len(beforeMalformed.IdempotencyResults), len(beforeMalformed.Outbox))
	}
	assertRejected("duplicate after success", duplicate)
	assertRejected("unknown after success", unknown)
	afterMalformed, err := transactions.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("approval snapshot after malformed calls: %v", err)
	}
	if !reflect.DeepEqual(beforeMalformed, afterMalformed) {
		t.Fatalf("malformed Serve calls changed durable approval snapshot: before=%+v after=%+v", beforeMalformed, afterMalformed)
	}
}

func TestMCPUnavailableEnrichmentDoesNotPersistProposal(t *testing.T) {
	root := t.TempDir()
	srv, err := NewServer(Config{RepoRoot: root})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}
	defer srv.Close()

	value, err := srv.executeTool(context.Background(), "request_semantic_enrichment", map[string]any{
		"target": root, "generationId": "generation-unavailable", "computedBasisId": "basis-unavailable", "targetStepId": "step-unavailable",
	})
	if err != nil {
		t.Fatalf("unavailable enrichment failed: %v", err)
	}
	result, ok := value.(*semantic.EnrichmentResult)
	if !ok || result == nil || result.State.Status != "unavailable" || result.Proposal != nil || result.Pack != nil {
		t.Fatalf("unavailable enrichment result = %#v", value)
	}
	_, err = semantic.LoadProposalForApproval(context.Background(), semantic.NewDurableProposalStore(root), srv.approvalWorkspaceID, "proposal-unavailable", "pack-unavailable")
	if !errors.Is(err, semantic.ErrProposalNotFound) {
		t.Fatalf("unavailable proposal lookup error = %v, want ErrProposalNotFound", err)
	}
}

func TestMCPEnrichmentPersistenceFailureIsSafeError(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") == "mcp" {
		t.Skip("helper process")
	}
	fixture := newMCPEnrichmentFactoryFixture(t)
	defer fixture.server.Close()
	fixture.server.proposalStore = mcpFailingProposalStore{}

	value, err := fixture.server.executeTool(context.Background(), "request_semantic_enrichment", map[string]any{
		"target": fixture.root, "generationId": fixture.generation, "targetStepId": fixture.stepID, "promptRevision": "prompt-mcp-persistence-error",
	})
	if err == nil {
		t.Fatalf("persistence failure returned success value %#v", value)
	}
	if strings.Contains(err.Error(), "private-persistence-canary") || strings.Contains(err.Error(), "/private/secret") {
		t.Fatalf("persistence failure leaked storage detail: %v", err)
	}
	if !errors.Is(err, semantic.ErrProposalInvalid) {
		t.Fatalf("persistence failure = %v, want typed proposal error", err)
	}
}

func TestMCPEnrichmentRejectsExternalTargetBeforeModelAndPersistence(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") == "mcp" {
		t.Skip("helper process")
	}
	external := newMCPEnrichmentFactoryFixture(t)
	defer external.server.Close()
	configuredRoot := t.TempDir()
	server, err := NewServer(Config{RepoRoot: configuredRoot, ModelHostFactory: external.server.cfg.ModelHostFactory})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}
	defer server.Close()
	mapIR, ok := external.server.loadEnrichmentSemanticMap(external.root, external.generation, external.basis)
	if !ok {
		t.Fatal("external fixture semantic map unavailable")
	}
	server.rememberSemanticMap(external.root, mapIR)

	value, err := server.executeTool(context.Background(), "request_semantic_enrichment", map[string]any{
		"target": external.root, "generationId": external.generation, "computedBasisId": external.basis, "targetStepId": external.stepID, "promptRevision": "prompt-mcp-external",
	})
	if err == nil {
		t.Fatalf("external target returned success value %#v", value)
	}
	if !errors.Is(err, semantic.ErrApprovalUnauthorized) {
		t.Fatalf("external target error = %v, want ErrApprovalUnauthorized", err)
	}
	if external.calls.Load() != 0 {
		t.Fatalf("external target spawned %d model hosts before authorization", external.calls.Load())
	}
	for name, root := range map[string]string{"configured": configuredRoot, "external": external.root} {
		if _, statErr := os.Stat(filepath.Join(root, ".codeflow", "semantic-proposals")); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("%s proposal store = %v, want no persisted record", name, statErr)
		}
	}
}

func TestMCPEnrichmentRejectsNonCanonicalWorkspaceTargetsBeforeModel(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") == "mcp" {
		t.Skip("helper process")
	}
	fixture := newMCPEnrichmentFactoryFixture(t)
	defer fixture.server.Close()
	other := newMCPEnrichmentFactoryFixture(t)
	defer other.server.Close()
	server := fixture.server
	otherMap, ok := other.server.loadEnrichmentSemanticMap(other.root, other.generation, other.basis)
	if !ok {
		t.Fatal("other workspace semantic map unavailable")
	}
	server.rememberSemanticMap(other.root, otherMap)
	nested := filepath.Join(fixture.root, "nested-target")
	if err := os.Mkdir(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	otherAlias := filepath.Join(t.TempDir(), "other-workspace-alias")
	if err := os.Symlink(other.root, otherAlias); err != nil {
		t.Skipf("other-root symlink unavailable: %v", err)
	}
	traversal := fixture.root + string(filepath.Separator) + ".." + string(filepath.Separator) + filepath.Base(fixture.root)
	cases := []struct {
		name   string
		target string
	}{
		{name: "nested absolute", target: nested},
		{name: "raw parent traversal", target: traversal},
		{name: "other absolute root", target: other.root},
		{name: "other root symlink", target: otherAlias},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			value, err := server.executeTool(context.Background(), "request_semantic_enrichment", map[string]any{
				"target": tc.target, "generationId": fixture.generation, "computedBasisId": fixture.basis, "targetStepId": fixture.stepID, "promptRevision": "prompt-mcp-target-boundary",
			})
			if err == nil || !errors.Is(err, semantic.ErrApprovalUnauthorized) {
				t.Fatalf("target %q error = %v, value = %#v, want typed unauthorized", tc.target, err, value)
			}
		})
	}
	if got := fixture.calls.Load(); got != 0 {
		t.Fatalf("non-canonical targets spawned %d model hosts, want 0", got)
	}
	if got := other.calls.Load(); got != 0 {
		t.Fatalf("other-root target spawned %d model hosts, want 0", got)
	}
}

func TestMCPEnrichmentMissingApprovalGateFailsClosedBeforeModel(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") == "mcp" {
		t.Skip("helper process")
	}
	fixture := newMCPEnrichmentFactoryFixture(t)
	defer fixture.server.Close()
	fixture.server.approvalGate = nil
	value, err := fixture.server.executeTool(context.Background(), "request_semantic_enrichment", map[string]any{
		"target": fixture.root, "generationId": fixture.generation, "computedBasisId": fixture.basis, "targetStepId": fixture.stepID, "promptRevision": "prompt-mcp-missing-gate",
	})
	if err == nil || !errors.Is(err, semantic.ErrApprovalUnauthenticated) {
		t.Fatalf("missing approval gate error = %v, value = %#v, want typed unauthenticated", err, value)
	}
	if got := fixture.calls.Load(); got != 0 {
		t.Fatalf("missing approval gate spawned %d model hosts, want 0", got)
	}
}

func TestMCPEnrichmentAllowsConfiguredRootSymlinkAliasAndPersistsCanonicalNamespace(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") == "mcp" {
		t.Skip("helper process")
	}
	fixture := newMCPEnrichmentFactoryFixture(t)
	defer fixture.server.Close()
	alias := filepath.Join(t.TempDir(), "workspace-alias")
	if err := os.Symlink(fixture.root, alias); err != nil {
		t.Skipf("same-root symlink unavailable: %v", err)
	}
	value, err := fixture.server.executeTool(context.Background(), "request_semantic_enrichment", map[string]any{
		"target": alias, "generationId": fixture.generation, "computedBasisId": fixture.basis, "targetStepId": fixture.stepID, "promptRevision": "prompt-mcp-alias",
	})
	if err != nil {
		t.Fatalf("same-root alias enrichment failed: %v", err)
	}
	result, ok := value.(*semantic.EnrichmentResult)
	if !ok || result == nil || result.State.Status != "available" || result.Proposal == nil || result.Pack == nil {
		t.Fatalf("same-root alias result = %#v, want available result", value)
	}
	loaded, err := semantic.LoadProposalForApproval(context.Background(), semantic.NewDurableProposalStore(fixture.root), fixture.server.approvalWorkspaceID, result.Proposal.ProposalID, result.Pack.EvidencePackID)
	if err != nil {
		t.Fatalf("canonical namespace lookup: %v", err)
	}
	if loaded == nil || !reflect.DeepEqual(loaded.Proposal, result.Proposal) || !reflect.DeepEqual(loaded.Pack, result.Pack) {
		t.Fatalf("canonical namespace pair = %#v, want exact result", loaded)
	}
}

type mcpFailingProposalStore struct{}

func (mcpFailingProposalStore) SaveEnrichmentResult(context.Context, string, *semantic.EnrichmentResult) error {
	return errors.New("private-persistence-canary at /private/secret")
}

func (mcpFailingProposalStore) Load(context.Context, string, string, string) (*semantic.StoredProposal, error) {
	return nil, &semantic.ProposalNotFoundError{}
}

func TestMarshalPublicEnrichmentRejectsRedactionOfRequiredIdentity(t *testing.T) {
	result := &semantic.EnrichmentResult{State: semantic.EnrichmentState{
		SchemaID: semantic.EnrichmentStateV2SchemaID, SchemaVersion: 2, Status: "unavailable",
		ProposalID: "token:required-identity", Capability: protocol.ModelHostCapability{Status: "unavailable"},
		UpdatedAt: "2026-09-05T00:00:00Z",
	}}
	public, err := marshalPublicMCPResult(result)
	if err == nil {
		t.Fatalf("redaction-damaged identity was accepted: %s", public)
	}
	if len(public) != 0 {
		t.Fatalf("pre-redaction result was returned on failure: %s", public)
	}
}

func TestMarshalPublicEnrichmentRejectsCompleteUnattestedAvailableValue(t *testing.T) {
	value := completeUnattestedAvailableResultForMCP(t)
	if err := semantic.ValidateEnrichmentState(value.State); err != nil {
		t.Fatalf("complete spoof state failed value validation before attestation: %v", err)
	}
	result := &value
	public, err := marshalPublicMCPResult(result)
	if err == nil {
		t.Fatalf("complete unattested available value reached MCP egress: %s", public)
	}
	if !strings.Contains(err.Error(), "Core-supervised host authority") {
		t.Fatalf("egress failed for a reason other than missing Core authority: %v", err)
	}
	if len(public) != 0 {
		t.Fatalf("pre-validation result was returned on trust failure: %s", public)
	}
}

func completeUnattestedAvailableResultForMCP(t *testing.T) semantic.EnrichmentResult {
	t.Helper()
	const content = "func Submit() {}"
	contentSum := sha256.Sum256([]byte(content))
	pack := &semantic.EvidencePack{
		SchemaID: semantic.EvidencePackV2SchemaID, SchemaVersion: 2,
		TargetSymbolPath: "Submit", ComputedBasisID: "basis-f22", GenerationID: "generation-f22",
		RepositoryID: "repo-f22", WorktreeID: "worktree-f22", SnapshotID: "snapshot-f22", WorkspaceEpoch: 1,
		TargetStepIDs: []string{"step-submit"}, ScopePaths: []string{"internal"}, RedactionStatus: "clean",
		Items: []semantic.EvidenceItem{{EvidenceID: "e-submit", Kind: "source", Source: "internal/checkout.go", Content: content, Verified: true, SnapshotID: "snapshot-f22", ComputedBasisID: "basis-f22", ContentDigest: hex.EncodeToString(contentSum[:]), ByteRange: [2]int{0, len(content)}}},
	}
	pack.EvidencePackID = testDeterministicPackIDForMCP(pack)
	pack.PackDigest = testPackDigestForMCP(pack)
	if err := semantic.ValidateEvidencePackV2(pack); err != nil {
		t.Fatalf("test evidence pack is not complete: %v", err)
	}
	sentinel := "sha256:" + strings.Repeat("b", 64)
	capability := protocol.ModelHostCapability{
		Status: "measured", Measured: true, SchemaConstrained: true, Cancellation: true,
		MaxRequestBytes: 1 << 20, MaxResponseBytes: 1 << 20, ModelID: "spoof", Revision: "r1", License: "MIT", Checksum: "sha256:spoof", Runtime: "test", DataBoundary: "bounded-pack",
		IsolationBackend: "fake-sandbox", IsolationEnforced: true, NetworkPolicy: "deny_all", PolicyDigest: "sha256:" + strings.Repeat("a", 64),
		ResourceLimits: testModelHostResourceLimitEvidenceForMCP(),
		IsolationProbe: &protocol.ModelHostIsolationProbe{
			RepositoryReadAttempt: "blocked", RepositoryWriteAttempt: "blocked", DisposableWriteAttempt: "blocked",
			SentinelBeforeDigest: sentinel, SentinelAfterDigest: sentinel, SentinelUnchanged: true, NetworkAttempt: "blocked",
		},
	}
	proposal := &semantic.ModelProposal{
		SchemaID: semantic.SemanticProposalV2SchemaID, SchemaVersion: 2, ProposalID: "proposal-f22",
		ComputedBasisID: pack.ComputedBasisID, GenerationID: pack.GenerationID, SnapshotID: pack.SnapshotID,
		TargetStepID: "step-submit", TargetSymbolPath: pack.TargetSymbolPath, ProposedTitle: "Submit checkout", ProposedCategory: "entry",
		EpistemicStatus: "inferred", Authority: "model", ClaimScope: "display_only", ModelID: capability.ModelID, ModelRevision: capability.Revision,
		PromptRevision: "prompt-f22", SchemaProfile: semantic.SemanticProposalSchemaProfile, PackDigest: pack.PackDigest,
		EvidenceRefs: []string{"e-submit"}, FactDigest: strings.Repeat("0", 64), ObligationDigest: strings.Repeat("0", 64), AlignmentDigest: strings.Repeat("0", 64), SettlementDigest: strings.Repeat("0", 64),
	}
	requestID := "request-f22"
	return semantic.EnrichmentResult{
		State: semantic.EnrichmentState{
			SchemaID: semantic.EnrichmentStateV2SchemaID, SchemaVersion: 2, Status: "available",
			ProposalID: proposal.ProposalID, PackDigest: pack.PackDigest, UpdatedAt: "2026-09-05T00:00:00Z",
			Capability: capability,
			Isolation: protocol.ModelHostIsolationEvidence{
				SourceDelivery: "bounded_evidence_pack", SourceMount: "not_mounted", WorkingDirectoryMode: "process_private_disposable", WorkingDirectoryPermission: "0700", Disposable: true,
				RepositoryPathExposed: false, RepositoryWriteCapability: false, RepositoryWriteAttempts: []string{}, RepositoryWriteAuditStatus: protocol.ModelHostRepositoryWriteAuditCapabilityEnforced, PackDigest: pack.PackDigest, ReceivedRequestID: requestID, ReceivedPackDigest: pack.PackDigest, CapabilityStatus: "measured", TerminalStatus: "success", CleanupVerified: true,
				IsolationBackend: "fake-sandbox", EnforcementStatus: "enforced", RepositoryReadAttempt: "blocked", RepositoryWriteAttempt: "blocked", DisposableWriteAttempt: "blocked",
				SentinelBeforeDigest: sentinel, SentinelAfterDigest: sentinel, SentinelUnchanged: true, NetworkAttempt: "blocked", NetworkPolicy: "deny_all", PolicyDigest: "sha256:" + strings.Repeat("a", 64), CoreTrustedProbe: true,
				ResourceLimits: testModelHostResourceLimitEvidenceForMCP(),
			},
		},
		Pack: pack, Proposal: proposal,
	}
}

func testModelHostResourceLimitEvidenceForMCP() *protocol.ModelHostResourceLimitEvidence {
	limits := protocol.DefaultModelHostResourceLimits()
	applied := limits
	applied.ProcessCount = 1
	return &protocol.ModelHostResourceLimitEvidence{
		Version: protocol.ModelHostResourceLimitsVersion, Declared: limits, Applied: applied,
		EnforcementStatus: protocol.ModelHostResourceEnforcementEnforced,
		Backend:           protocol.ModelHostResourceBackendDarwinHostTree,
	}
}

func testDeterministicPackIDForMCP(pack *semantic.EvidencePack) string {
	copy := *pack
	copy.EvidencePackID = ""
	copy.PackDigest = ""
	sum := sha256.Sum256(mustMarshalTestJSONForMCP(&copy))
	return "pack-v2-" + hex.EncodeToString(sum[:])[:24]
}

func testPackDigestForMCP(pack *semantic.EvidencePack) string {
	copy := *pack
	copy.PackDigest = ""
	sum := sha256.Sum256(mustMarshalTestJSONForMCP(&copy))
	return hex.EncodeToString(sum[:])
}

func mustMarshalTestJSONForMCP(value any) []byte {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return data
}
