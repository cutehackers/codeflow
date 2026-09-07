package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"codeflow/internal/contractharness"
	"codeflow/internal/rflscvs09evidence"
	"codeflow/internal/semantic"
	"codeflow/internal/workspace"
)

type mcpApprovalHistoryToolCallResponse struct {
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

func TestMCPApprovalHistoryToolIsAdvertisedAndServesDurableHistory(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") == "mcp" {
		t.Skip("helper process")
	}
	listServer, err := NewServer(Config{RepoRoot: t.TempDir()})
	if err != nil {
		t.Fatalf("NewServer for tools/list: %v", err)
	}
	listResponse := serveMCPApprovalHistoryRequest(t, listServer, `{"jsonrpc":"2.0","id":"list","method":"tools/list","params":{}}`)
	_ = listServer.Close()

	var list struct {
		Result struct {
			Tools []struct {
				Name        string         `json:"name"`
				Description string         `json:"description"`
				InputSchema map[string]any `json:"inputSchema"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(listResponse, &list); err != nil {
		t.Fatalf("decode tools/list response: %v output=%s", err, listResponse)
	}
	var historyTool *struct {
		Name        string         `json:"name"`
		Description string         `json:"description"`
		InputSchema map[string]any `json:"inputSchema"`
	}
	for index := range list.Result.Tools {
		tool := &list.Result.Tools[index]
		if tool.Name == "get_semantic_approval_history" {
			historyTool = (*struct {
				Name        string         `json:"name"`
				Description string         `json:"description"`
				InputSchema map[string]any `json:"inputSchema"`
			})(tool)
			break
		}
	}
	if historyTool == nil {
		t.Fatal("tools/list did not advertise get_semantic_approval_history")
	}
	if !strings.Contains(strings.ToLower(historyTool.Description), "read-only") || !strings.Contains(strings.ToLower(historyTool.Description), "durable") {
		t.Fatalf("history tool description = %q, want read-only durable history wording", historyTool.Description)
	}
	if historyTool.InputSchema["type"] != "object" {
		t.Fatalf("history inputSchema.type = %#v, want object", historyTool.InputSchema["type"])
	}
	if additional, ok := historyTool.InputSchema["additionalProperties"].(bool); !ok || additional {
		t.Fatalf("history inputSchema.additionalProperties = %#v, want false", historyTool.InputSchema["additionalProperties"])
	}
	required, ok := historyTool.InputSchema["required"].([]any)
	if !ok || len(required) != 2 || required[0] != "proposalId" || required[1] != "evidencePackId" {
		t.Fatalf("history required = %#v, want proposalId/evidencePackId", historyTool.InputSchema["required"])
	}
	properties, ok := historyTool.InputSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("history properties = %#v, want object", historyTool.InputSchema["properties"])
	}
	wantProperties := map[string]struct{}{"proposalId": {}, "evidencePackId": {}, "target": {}, "token": {}}
	if len(properties) != len(wantProperties) {
		t.Fatalf("history properties = %#v, want exactly proposalId/evidencePackId/target/token", properties)
	}
	expectedPropertyKeys := map[string]map[string]struct{}{
		"proposalId":     {"type": {}, "minLength": {}, "maxLength": {}, "description": {}},
		"evidencePackId": {"type": {}, "minLength": {}, "maxLength": {}, "description": {}},
		"target":         {"type": {}, "maxLength": {}, "description": {}},
		"token":          {"type": {}, "minLength": {}, "maxLength": {}, "description": {}},
	}
	for name, expectedKeys := range expectedPropertyKeys {
		raw, ok := properties[name]
		if !ok {
			t.Fatalf("history schema is missing required property %q", name)
		}
		property, ok := raw.(map[string]any)
		if !ok || property["type"] != "string" {
			t.Fatalf("history property %q = %#v, want bounded string", name, raw)
		}
		if len(property) != len(expectedKeys) {
			t.Fatalf("history property %q keys = %#v, want exact bounded contract keys", name, property)
		}
		for key := range property {
			if _, ok := expectedKeys[key]; !ok {
				t.Fatalf("history property %q has unexpected schema key %q", name, key)
			}
		}
		description, ok := property["description"].(string)
		if !ok || strings.TrimSpace(description) == "" {
			t.Fatalf("history property %q description = %#v, want nonblank", name, property["description"])
		}
		maxLength, ok := property["maxLength"].(float64)
		if !ok || (name == "proposalId" && maxLength != 256) || (name == "evidencePackId" && maxLength != 256) || (name == "target" && maxLength != 4096) || (name == "token" && maxLength != 256) {
			t.Fatalf("history property %q maxLength = %#v, want exact bounded contract value", name, property["maxLength"])
		}
		minLength, hasMinLength := property["minLength"].(float64)
		if name == "target" {
			if _, present := property["minLength"]; present {
				t.Fatalf("history target unexpectedly declares minLength = %#v", property["minLength"])
			}
		} else if !hasMinLength || minLength != 1 {
			t.Fatalf("history property %q minLength = %#v, want exactly 1", name, property["minLength"])
		}
	}

	fixture, enrichment := newMCPApprovalHistoryDurableFixture(t)
	defer fixture.server.Close()

	historyArgs, err := json.Marshal(map[string]any{
		"target": fixture.root, "proposalId": enrichment.Proposal.ProposalID, "evidencePackId": enrichment.Pack.EvidencePackID,
	})
	if err != nil {
		t.Fatalf("marshal history arguments: %v", err)
	}
	historyResponse := serveMCPApprovalHistoryRequest(t, fixture.server, mcpApprovalHistoryToolCall("get_semantic_approval_history", historyArgs))
	historyText := requireMCPApprovalHistorySuccess(t, historyResponse)
	if err := contractharness.ValidateVS09Contract(semantic.ApprovalHistoryV1SchemaID, []byte(historyText)); err != nil {
		t.Fatalf("history contract validation: %v text=%s", err, historyText)
	}
	var historyDocument map[string]json.RawMessage
	if err := json.Unmarshal([]byte(historyText), &historyDocument); err != nil {
		t.Fatalf("decode history document: %v", err)
	}
	wantKeys := map[string]struct{}{"schemaId": {}, "schemaVersion": {}, "target": {}, "events": {}, "aggregate": {}, "freshness": {}}
	if len(historyDocument) != len(wantKeys) {
		t.Fatalf("history keys = %#v, want exactly six canonical fields", historyDocument)
	}
	for key := range wantKeys {
		if _, ok := historyDocument[key]; !ok {
			t.Fatalf("history missing canonical key %q", key)
		}
	}
	var history semantic.ApprovalHistoryResult
	if err := json.Unmarshal([]byte(historyText), &history); err != nil {
		t.Fatalf("decode canonical history result: %v", err)
	}
	if history.Freshness != "current" || len(history.Events) != 1 || history.Aggregate.State != "active" || history.Aggregate.Version != 1 {
		t.Fatalf("history result = %+v, want current version-one active approval", history)
	}
}

func TestMCPApprovalHistoryToolRestartsAndTracksLiveHeadReadOnly(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") == "mcp" {
		t.Skip("helper process")
	}
	fixture, enrichment := newMCPApprovalHistoryDurableFixture(t)
	defer func() { _ = fixture.server.Close() }()
	historyArgs, err := json.Marshal(map[string]any{
		"target": fixture.root, "proposalId": enrichment.Proposal.ProposalID, "evidencePackId": enrichment.Pack.EvidencePackID,
	})
	if err != nil {
		t.Fatalf("marshal history arguments: %v", err)
	}
	query := func(server *Server) ([]byte, semantic.ApprovalHistoryResult) {
		raw := serveMCPApprovalHistoryRequest(t, server, mcpApprovalHistoryToolCall("get_semantic_approval_history", historyArgs))
		text := requireMCPApprovalHistorySuccess(t, raw)
		var result semantic.ApprovalHistoryResult
		if err := json.Unmarshal([]byte(text), &result); err != nil {
			t.Fatalf("decode history result: %v text=%s", err, text)
		}
		return []byte(text), result
	}
	_, before := query(fixture.server)
	if before.Freshness != "current" {
		t.Fatalf("before live-head advance freshness = %q, want current", before.Freshness)
	}
	storage, err := fixture.server.getStorage(fixture.root)
	if err != nil {
		t.Fatalf("get approval storage: %v", err)
	}
	transactionPath := filepath.Join(fixture.root, ".codeflow", "approval-transactions", "approval-transactions.sqlite3")
	transactionBefore, err := os.ReadFile(transactionPath)
	if err != nil {
		t.Fatalf("read transaction before live-head advance: %v", err)
	}
	pointerPath := filepath.Join(storage.BaseDir(), "active-pointer.json")
	pointerBefore, err := os.ReadFile(pointerPath)
	if err != nil {
		t.Fatalf("read pointer before live-head advance: %v", err)
	}
	proofBefore, err := storage.ReadValidatedActiveProofBundle()
	if err != nil {
		t.Fatalf("read proof before live-head advance: %v", err)
	}
	engine, err := fixture.server.getSnapshotEngine(fixture.root)
	if err != nil {
		t.Fatalf("get snapshot engine: %v", err)
	}
	if _, _, err := engine.ApplyVersionedEdit(context.Background(), workspace.EditRequest{Path: "main.go", Content: []byte("package main\n\nfunc Changed() {}\n"), DocumentVersion: 2, Source: workspace.SourceIDEVersioned}); err != nil {
		t.Fatalf("advance live head: %v", err)
	}
	transactionTreeBeforeHistoricalQuery, err := snapshotApprovalHistoryTree(filepath.Join(fixture.root, ".codeflow", "approval-transactions"))
	if err != nil {
		t.Fatalf("snapshot transaction tree before historical query: %v", err)
	}
	afterBody, after := query(fixture.server)
	if after.Freshness != "historical" {
		t.Fatalf("after live-head advance freshness = %q, want historical", after.Freshness)
	}
	transactionTreeAfterHistoricalQuery, err := snapshotApprovalHistoryTree(filepath.Join(fixture.root, ".codeflow", "approval-transactions"))
	if err != nil {
		t.Fatalf("snapshot transaction tree after historical query: %v", err)
	}
	if !approvalHistoryTreeEqual(transactionTreeBeforeHistoricalQuery, transactionTreeAfterHistoricalQuery) {
		t.Fatal("historical history query changed the managed approval transaction tree")
	}
	if !reflect.DeepEqual(before.Target, after.Target) || !reflect.DeepEqual(before.Events, after.Events) || !reflect.DeepEqual(before.Aggregate, after.Aggregate) {
		t.Fatal("live-head advance changed durable approval history fields")
	}
	transactionAfter, err := os.ReadFile(transactionPath)
	if err != nil {
		t.Fatalf("read transaction after history query: %v", err)
	}
	if !bytes.Equal(transactionBefore, transactionAfter) {
		t.Fatal("history query changed approval transaction bytes")
	}
	pointerAfter, err := os.ReadFile(pointerPath)
	if err != nil {
		t.Fatalf("read pointer after history query: %v", err)
	}
	if !bytes.Equal(pointerBefore, pointerAfter) {
		t.Fatal("history query changed active pointer bytes")
	}
	proofAfter, err := storage.ReadValidatedActiveProofBundle()
	if err != nil {
		t.Fatalf("read proof after history query: %v", err)
	}
	if proofBefore == nil || proofAfter == nil || !reflect.DeepEqual(proofBefore.Manifest, proofAfter.Manifest) || !reflect.DeepEqual(proofBefore.Pointer, proofAfter.Pointer) || !bytes.Equal(proofBefore.ManifestBytes, proofAfter.ManifestBytes) || !bytes.Equal(proofBefore.SemanticMap, proofAfter.SemanticMap) || !bytes.Equal(proofBefore.AnalyzerResult, proofAfter.AnalyzerResult) || !bytes.Equal(proofBefore.SemanticDelta, proofAfter.SemanticDelta) {
		t.Fatal("history query changed validated proof bytes")
	}
	if err := fixture.server.Close(); err != nil {
		t.Fatalf("close server before restart: %v", err)
	}
	restarted, err := NewServer(Config{RepoRoot: fixture.root})
	if err != nil {
		t.Fatalf("restart MCP server: %v", err)
	}
	defer restarted.Close()
	restartedTransactionRoot := filepath.Join(fixture.root, ".codeflow", "approval-transactions")
	restartedTreeBeforeQuery, err := snapshotApprovalHistoryTree(restartedTransactionRoot)
	if err != nil {
		t.Fatalf("snapshot transaction tree before restarted query: %v", err)
	}
	restartedBody, restartedResult := query(restarted)
	restartedTreeAfterQuery, err := snapshotApprovalHistoryTree(restartedTransactionRoot)
	if err != nil {
		t.Fatalf("snapshot transaction tree after restarted query: %v", err)
	}
	if !approvalHistoryTreeEqual(restartedTreeBeforeQuery, restartedTreeAfterQuery) {
		t.Fatal("restarted history query changed the managed approval transaction tree")
	}
	if !bytes.Equal(afterBody, restartedBody) || !reflect.DeepEqual(after, restartedResult) {
		t.Fatalf("restart changed historical history: before=%s after=%s", afterBody, restartedBody)
	}
	rflscvs09evidence.Observe(t, "codeflow/internal/mcp", []string{"VS09-A9"}, map[string]any{"before.history": before, "after.history": after, "restart.history": restartedBody, "before.transactions": transactionBefore, "after.transactions": transactionAfter, "before.proof": proofBefore, "after.proof": proofAfter})
}

func TestMCPApprovalHistoryStrictArgumentsFailClosedBeforeRead(t *testing.T) {
	maxUnicodeID := strings.Repeat("é", 256)
	overUnicodeID := strings.Repeat("é", 257)
	oversized := append([]byte(`{"proposalId":"proposal-1","evidencePackId":"pack-1"`), []byte(strings.Repeat(" ", 64<<10))...)
	oversized = append(oversized, '}')
	maxProposal, _ := json.Marshal(map[string]string{"proposalId": maxUnicodeID, "evidencePackId": "pack-1"})
	maxPack, _ := json.Marshal(map[string]string{"proposalId": "proposal-1", "evidencePackId": maxUnicodeID})
	overProposal, _ := json.Marshal(map[string]string{"proposalId": overUnicodeID, "evidencePackId": "pack-1"})
	overPack, _ := json.Marshal(map[string]string{"proposalId": "proposal-1", "evidencePackId": overUnicodeID})
	cases := []struct {
		name       string
		rawArgs    []byte
		wantCode   string
		allowParse bool
	}{
		{name: "duplicate key", rawArgs: []byte(`{"proposalId":"proposal-1","proposalId":"proposal-2","evidencePackId":"pack-1"}`), wantCode: "approval_invalid"},
		{name: "unknown key", rawArgs: []byte(`{"proposalId":"proposal-1","evidencePackId":"pack-1","workspaceId":"spoofed"}`), wantCode: "approval_invalid"},
		{name: "proposal type mismatch", rawArgs: []byte(`{"proposalId":1,"evidencePackId":"pack-1"}`), wantCode: "approval_invalid"},
		{name: "target type mismatch", rawArgs: []byte(`{"proposalId":"proposal-1","evidencePackId":"pack-1","target":1}`), wantCode: "approval_invalid"},
		{name: "token type mismatch", rawArgs: []byte(`{"proposalId":"proposal-1","evidencePackId":"pack-1","token":1}`), wantCode: "approval_invalid"},
		{name: "trailing JSON value", rawArgs: []byte(`{"proposalId":"proposal-1","evidencePackId":"pack-1"}{}`), wantCode: "approval_invalid", allowParse: true},
		{name: "arguments string instead of object", rawArgs: []byte(`"{} {}"`), wantCode: "approval_invalid"},
		{name: "non-object array", rawArgs: []byte(`[]`), wantCode: "approval_invalid"},
		{name: "non-object null", rawArgs: []byte(`null`), wantCode: "approval_invalid"},
		{name: "invalid UTF-8", rawArgs: append(append([]byte(`{"proposalId":"`), 0xff), []byte(`","evidencePackId":"pack-1"}`)...), wantCode: "approval_invalid", allowParse: true},
		{name: "256 unicode proposal reaches lookup", rawArgs: maxProposal, wantCode: "approval_unavailable"},
		{name: "256 unicode evidence pack reaches lookup", rawArgs: maxPack, wantCode: "approval_unavailable"},
		{name: "257 unicode proposal", rawArgs: overProposal, wantCode: "approval_invalid"},
		{name: "257 unicode evidence pack", rawArgs: overPack, wantCode: "approval_invalid"},
		{name: "oversized raw payload", rawArgs: oversized, wantCode: "approval_invalid"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			server, err := NewServer(Config{RepoRoot: root})
			if err != nil {
				t.Fatalf("NewServer: %v", err)
			}
			defer server.Close()
			raw := serveMCPApprovalHistoryRequest(t, server, mcpApprovalHistoryToolCall("get_semantic_approval_history", tc.rawArgs))
			response := decodeMCPApprovalHistoryResponse(t, raw)
			if response.Error != nil {
				if !tc.allowParse || response.Error.Code != -32700 {
					t.Fatalf("MCP response error = %+v output=%s, want bounded %s content", response.Error, raw, tc.wantCode)
				}
				assertMCPApprovalHistoryStateAbsent(t, root)
				return
			}
			if !response.Result.IsError || len(response.Result.Content) != 1 || response.Result.Content[0].Type != "text" {
				t.Fatalf("MCP response = %s, want one bounded error content", raw)
			}
			var payload map[string]string
			if err := json.Unmarshal([]byte(response.Result.Content[0].Text), &payload); err != nil {
				t.Fatalf("error content is not JSON: %v text=%s", err, response.Result.Content[0].Text)
			}
			if payload["code"] != tc.wantCode {
				t.Fatalf("error code = %q, want %q payload=%#v", payload["code"], tc.wantCode, payload)
			}
			assertMCPApprovalHistoryStateAbsent(t, root)
		})
	}
}

func TestMCPApprovalHistoryToolAuthAndReadErrorsAreBounded(t *testing.T) {
	cases := []struct {
		name          string
		requireToken  bool
		configuredTok string
		requestTok    *string
		missingGate   bool
		target        string
		wantCode      string
	}{
		{name: "missing MCP token", requireToken: true, configuredTok: "history-mcp-token", wantCode: "approval_unauthenticated"},
		{name: "wrong MCP token", requireToken: true, configuredTok: "history-mcp-token", requestTok: stringPointer("wrong-token"), wantCode: "approval_unauthenticated"},
		{name: "missing approval gate", missingGate: true, wantCode: "approval_unauthenticated"},
		{name: "hostile target", target: "outside", wantCode: "approval_unauthorized"},
		{name: "missing exact pair", wantCode: "approval_unavailable"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			server, err := NewServer(Config{RepoRoot: root, RequireToken: tc.requireToken, AuthToken: tc.configuredTok})
			if err != nil {
				t.Fatalf("NewServer: %v", err)
			}
			defer server.Close()
			if tc.missingGate {
				server.approvalGate = nil
			}
			target := root
			if tc.target != "" {
				target = filepath.Join(root, "outside")
			}
			args := map[string]any{"proposalId": "proposal-1", "evidencePackId": "pack-1", "target": target}
			if tc.requestTok != nil {
				args["token"] = *tc.requestTok
			}
			rawArgs, err := json.Marshal(args)
			if err != nil {
				t.Fatalf("marshal arguments: %v", err)
			}
			raw := serveMCPApprovalHistoryRequest(t, server, mcpApprovalHistoryToolCall("get_semantic_approval_history", rawArgs))
			requireMCPApprovalHistoryError(t, raw, tc.wantCode)
			if tc.wantCode != "approval_unavailable" {
				assertMCPApprovalHistoryStateAbsent(t, root)
			}
		})
	}

	t.Run("corrupt transaction maps to invalid", func(t *testing.T) {
		root := t.TempDir()
		server, err := NewServer(Config{RepoRoot: root})
		if err != nil {
			t.Fatalf("NewServer: %v", err)
		}
		defer server.Close()
		databasePath := filepath.Join(root, ".codeflow", "approval-transactions", "approval-transactions.sqlite3")
		if err := os.MkdirAll(filepath.Dir(databasePath), 0o700); err != nil {
			t.Fatalf("create approval database directory: %v", err)
		}
		if err := os.WriteFile(databasePath, []byte("corrupt approval database"), 0o600); err != nil {
			t.Fatalf("write corrupt approval database: %v", err)
		}
		transactionRoot := filepath.Dir(databasePath)
		before, err := snapshotApprovalHistoryTree(transactionRoot)
		if err != nil {
			t.Fatalf("snapshot corrupt approval transaction tree before query: %v", err)
		}
		args, err := json.Marshal(map[string]any{"proposalId": "proposal-1", "evidencePackId": "pack-1", "target": root})
		if err != nil {
			t.Fatalf("marshal corrupt query: %v", err)
		}
		raw := serveMCPApprovalHistoryRequest(t, server, mcpApprovalHistoryToolCall("get_semantic_approval_history", args))
		requireMCPApprovalHistoryError(t, raw, "approval_invalid")
		after, err := snapshotApprovalHistoryTree(transactionRoot)
		if err != nil {
			t.Fatalf("snapshot corrupt approval transaction tree after query: %v", err)
		}
		if !approvalHistoryTreeEqual(before, after) {
			t.Fatal("corrupt history query changed the managed approval transaction tree")
		}
	})
}

func TestMCPApprovalHistoryErrorResponseDoesNotLeakRequestCanaries(t *testing.T) {
	root := filepath.Join(t.TempDir(), "approval-history-repo-canary-7f4d")
	configuredToken := "configured-history-token-canary-29b1"
	wrongToken := "wrong-history-token-canary-5a80"
	proposalID := "proposal-history-canary-6e13"
	evidencePackID := "evidence-history-canary-8c42"
	server, err := NewServer(Config{RepoRoot: root, RequireToken: true, AuthToken: configuredToken})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer server.Close()
	args, err := json.Marshal(map[string]string{
		"target":         root,
		"token":          wrongToken,
		"proposalId":     proposalID,
		"evidencePackId": evidencePackID,
	})
	if err != nil {
		t.Fatalf("marshal canary query: %v", err)
	}
	raw := serveMCPApprovalHistoryRequest(t, server, mcpApprovalHistoryToolCall("get_semantic_approval_history", args))
	requireMCPApprovalHistoryError(t, raw, "approval_unauthenticated", root, configuredToken, wrongToken, proposalID, evidencePackID)
}

func TestValidateMCPApprovalHistoryErrorRejectsNonCanonicalResponses(t *testing.T) {
	valid := `{"jsonrpc":"2.0","id":"approval-history","result":{"content":[{"type":"text","text":"{\"code\":\"approval_invalid\",\"message\":\"approval request is invalid\"}"}],"isError":true}}`
	cases := []struct {
		name string
		raw  string
	}{
		{name: "top-level extra", raw: `{"jsonrpc":"2.0","id":"approval-history","result":{"content":[{"type":"text","text":"{\"code\":\"approval_invalid\",\"message\":\"approval request is invalid\"}"}],"isError":true},"extra":true}`},
		{name: "top-level duplicate", raw: `{"jsonrpc":"2.0","id":"approval-history","id":"approval-history","result":{"content":[{"type":"text","text":"{\"code\":\"approval_invalid\",\"message\":\"approval request is invalid\"}"}],"isError":true}}`},
		{name: "result extra", raw: `{"jsonrpc":"2.0","id":"approval-history","result":{"content":[{"type":"text","text":"{\"code\":\"approval_invalid\",\"message\":\"approval request is invalid\"}"}],"isError":true,"extra":false}}`},
		{name: "content extra", raw: `{"jsonrpc":"2.0","id":"approval-history","result":{"content":[{"type":"text","text":"{\"code\":\"approval_invalid\",\"message\":\"approval request is invalid\"}","extra":false}],"isError":true}}`},
		{name: "payload extra", raw: `{"jsonrpc":"2.0","id":"approval-history","result":{"content":[{"type":"text","text":"{\"code\":\"approval_invalid\",\"message\":\"approval request is invalid\",\"extra\":\"nope\"}"}],"isError":true}}`},
		{name: "payload duplicate", raw: `{"jsonrpc":"2.0","id":"approval-history","result":{"content":[{"type":"text","text":"{\"code\":\"approval_invalid\",\"code\":\"approval_invalid\",\"message\":\"approval request is invalid\"}"}],"isError":true}}`},
		{name: "type mismatch", raw: `{"jsonrpc":"2.0","id":"approval-history","result":{"content":[{"type":"text","text":1}],"isError":true}}`},
		{name: "trailing JSON", raw: valid + `{}`},
	}
	if err := validateMCPApprovalHistoryError([]byte(valid), "approval_invalid"); err != nil {
		t.Fatalf("canonical error response rejected: %v", err)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateMCPApprovalHistoryError([]byte(tc.raw), "approval_invalid"); err == nil {
				t.Fatalf("non-canonical error response was accepted: %s", tc.raw)
			}
		})
	}
}

func stringPointer(value string) *string {
	return &value
}

func validateMCPApprovalHistoryError(raw []byte, wantCode string, canaries ...string) error {
	if !utf8.Valid(raw) {
		return errors.New("MCP error response is not valid UTF-8")
	}
	for _, canary := range canaries {
		if canary != "" && bytes.Contains(raw, []byte(canary)) {
			return errors.New("MCP error response contains a forbidden canary")
		}
	}
	wantMessage := map[string]string{
		"approval_invalid":         "approval request is invalid",
		"approval_unavailable":     "approval service is unavailable",
		"approval_unauthenticated": "approval authentication is required",
		"approval_unauthorized":    "approval workspace is not authorized",
	}
	wantMessageValue, ok := wantMessage[wantCode]
	if !ok {
		return errors.New("MCP error response has an unknown expected code")
	}
	top, err := parseStrictMCPObject(raw, map[string]struct{}{
		"jsonrpc": {}, "id": {}, "result": {},
	}, "jsonrpc", "id", "result")
	if err != nil {
		return err
	}
	if value, err := decodeStrictMCPString(top["jsonrpc"]); err != nil || value != "2.0" {
		return errors.New("MCP error response has an invalid JSON-RPC version")
	}
	if value, err := decodeStrictMCPString(top["id"]); err != nil || value != "approval-history" {
		return errors.New("MCP error response has an invalid request ID")
	}
	result, err := parseStrictMCPObject(top["result"], map[string]struct{}{
		"content": {}, "isError": {},
	}, "content", "isError")
	if err != nil {
		return err
	}
	isError, err := decodeStrictMCPBool(result["isError"])
	if err != nil || !isError {
		return errors.New("MCP error response is not marked as an error")
	}
	content, err := decodeStrictMCPArray(result["content"])
	if err != nil || len(content) != 1 {
		return errors.New("MCP error response content is not exactly one item")
	}
	contentItem, err := parseStrictMCPObject(content[0], map[string]struct{}{
		"type": {}, "text": {},
	}, "type", "text")
	if err != nil {
		return err
	}
	if value, err := decodeStrictMCPString(contentItem["type"]); err != nil || value != "text" {
		return errors.New("MCP error response content has an invalid type")
	}
	errorText, err := decodeStrictMCPString(contentItem["text"])
	if err != nil {
		return errors.New("MCP error response content text is not a string")
	}
	payload, err := parseStrictMCPObject([]byte(errorText), map[string]struct{}{
		"code": {}, "message": {},
	}, "code", "message")
	if err != nil {
		return err
	}
	code, err := decodeStrictMCPString(payload["code"])
	if err != nil || code != wantCode {
		return errors.New("MCP error response has an unexpected code")
	}
	message, err := decodeStrictMCPString(payload["message"])
	if err != nil || message != wantMessageValue {
		return errors.New("MCP error response has an unexpected message")
	}
	return nil
}

func parseStrictMCPObject(raw []byte, allowed map[string]struct{}, required ...string) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	opening, err := decoder.Token()
	if err != nil {
		return nil, errors.New("MCP object is malformed")
	}
	if delimiter, ok := opening.(json.Delim); !ok || delimiter != '{' {
		return nil, errors.New("MCP value is not an object")
	}
	fields := make(map[string]json.RawMessage, len(allowed))
	for decoder.More() {
		keyToken, err := decoder.Token()
		key, ok := keyToken.(string)
		if err != nil || !ok {
			return nil, errors.New("MCP object has an invalid key")
		}
		if _, ok := allowed[key]; !ok {
			return nil, errors.New("MCP object has an unknown field")
		}
		if _, duplicate := fields[key]; duplicate {
			return nil, errors.New("MCP object has a duplicate field")
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, errors.New("MCP object has an invalid value")
		}
		fields[key] = append(json.RawMessage(nil), value...)
	}
	closing, err := decoder.Token()
	if err != nil {
		return nil, errors.New("MCP object is incomplete")
	}
	if delimiter, ok := closing.(json.Delim); !ok || delimiter != '}' {
		return nil, errors.New("MCP object has an invalid closing delimiter")
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, errors.New("MCP object has trailing data")
	}
	for _, key := range required {
		if _, ok := fields[key]; !ok {
			return nil, errors.New("MCP object is missing a required field")
		}
	}
	return fields, nil
}

func decodeStrictMCPArray(raw []byte) ([]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	opening, err := decoder.Token()
	if err != nil {
		return nil, errors.New("MCP array is malformed")
	}
	if delimiter, ok := opening.(json.Delim); !ok || delimiter != '[' {
		return nil, errors.New("MCP value is not an array")
	}
	items := make([]json.RawMessage, 0)
	for decoder.More() {
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, errors.New("MCP array has an invalid item")
		}
		items = append(items, append(json.RawMessage(nil), value...))
	}
	closing, err := decoder.Token()
	if err != nil {
		return nil, errors.New("MCP array is incomplete")
	}
	if delimiter, ok := closing.(json.Delim); !ok || delimiter != ']' {
		return nil, errors.New("MCP array has an invalid closing delimiter")
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, errors.New("MCP array has trailing data")
	}
	return items, nil
}

func decodeStrictMCPString(raw []byte) (string, error) {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return "", errors.New("MCP string is null")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	var value string
	if err := decoder.Decode(&value); err != nil {
		return "", errors.New("MCP value is not a string")
	}
	if _, err := decoder.Token(); err != io.EOF {
		return "", errors.New("MCP string has trailing data")
	}
	return value, nil
}

func decodeStrictMCPBool(raw []byte) (bool, error) {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return false, errors.New("MCP bool is null")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	var value bool
	if err := decoder.Decode(&value); err != nil {
		return false, errors.New("MCP value is not a bool")
	}
	if _, err := decoder.Token(); err != io.EOF {
		return false, errors.New("MCP bool has trailing data")
	}
	return value, nil
}

func requireMCPApprovalHistoryError(t *testing.T, raw []byte, wantCode string, canaries ...string) {
	t.Helper()
	if err := validateMCPApprovalHistoryError(raw, wantCode, canaries...); err != nil {
		t.Fatalf("MCP error response is not bounded %s: %v raw=%s", wantCode, err, raw)
	}
}

func assertMCPApprovalHistoryStateAbsent(t *testing.T, root string) {
	t.Helper()
	databasePath := filepath.Join(root, ".codeflow", "approval-transactions", "approval-transactions.sqlite3")
	if _, err := os.Stat(databasePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("approval transaction state exists after strict rejection: %v", err)
	}
	proposalPath := filepath.Join(root, ".codeflow", "semantic-proposals")
	if _, err := os.Stat(proposalPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("proposal state exists after strict rejection: %v", err)
	}
}

func mcpApprovalHistoryToolCall(name string, rawArguments []byte) string {
	nameJSON, _ := json.Marshal(name)
	return `{"jsonrpc":"2.0","id":"approval-history","method":"tools/call","params":{"name":` + string(nameJSON) + `,"arguments":` + string(rawArguments) + `}}`
}

func serveMCPApprovalHistoryRequest(t *testing.T, server *Server, request string) []byte {
	t.Helper()
	var output bytes.Buffer
	if err := server.Serve(context.Background(), strings.NewReader(request+"\n"), &output); err != nil {
		t.Fatalf("Serve request: %v output=%s", err, output.Bytes())
	}
	return output.Bytes()
}

func decodeMCPApprovalHistoryResponse(t *testing.T, raw []byte) mcpApprovalHistoryToolCallResponse {
	t.Helper()
	var response mcpApprovalHistoryToolCallResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatalf("decode MCP response: %v output=%s", err, raw)
	}
	return response
}

func requireMCPApprovalHistorySuccess(t *testing.T, raw []byte) string {
	t.Helper()
	response := decodeMCPApprovalHistoryResponse(t, raw)
	if response.Error != nil || response.Result.IsError || len(response.Result.Content) != 1 || response.Result.Content[0].Type != "text" {
		t.Fatalf("MCP response = %s, want one successful text content", raw)
	}
	return response.Result.Content[0].Text
}

func newMCPApprovalHistoryDurableFixture(t *testing.T) (*mcpEnrichmentFactoryFixture, semantic.EnrichmentResult) {
	t.Helper()
	fixture, enrichment := newMCPApprovalEnrichmentFixture(t)
	approvalArgs, err := json.Marshal(mcpApprovalRequestArgs(fixture.root, enrichment, "mcp-history"))
	if err != nil {
		t.Fatalf("marshal approval arguments: %v", err)
	}
	approvalResponse := serveMCPApprovalHistoryRequest(t, fixture.server, mcpApprovalHistoryToolCall("submit_semantic_approval", approvalArgs))
	response := decodeMCPApprovalHistoryResponse(t, approvalResponse)
	if response.Result.IsError || response.Error != nil {
		t.Fatalf("durable approval response = %s, want success", approvalResponse)
	}
	return fixture, enrichment
}

func newMCPApprovalEnrichmentFixture(t *testing.T) (*mcpEnrichmentFactoryFixture, semantic.EnrichmentResult) {
	t.Helper()
	fixture := newMCPEnrichmentFactoryFixture(t)
	enrichmentArgs, err := json.Marshal(map[string]any{
		"target": fixture.root, "generationId": fixture.generation, "targetStepId": fixture.stepID, "promptRevision": "prompt-mcp-history",
	})
	if err != nil {
		t.Fatalf("marshal enrichment arguments: %v", err)
	}
	enrichmentResponse := serveMCPApprovalHistoryRequest(t, fixture.server, mcpApprovalHistoryToolCall("request_semantic_enrichment", enrichmentArgs))
	enrichmentText := requireMCPApprovalHistorySuccess(t, enrichmentResponse)
	var enrichment semantic.EnrichmentResult
	if err := json.Unmarshal([]byte(enrichmentText), &enrichment); err != nil {
		t.Fatalf("decode enrichment result: %v text=%s", err, enrichmentText)
	}
	if enrichment.Proposal == nil || enrichment.Pack == nil || enrichment.Proposal.ProposalID == "" || enrichment.Pack.EvidencePackID == "" {
		t.Fatalf("enrichment result = %+v, want durable proposal and evidence pack", enrichment)
	}
	return fixture, enrichment
}
