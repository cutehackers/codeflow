package mcp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"codeflow/internal/mcp"
)

func moduleRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot determine source location")
	}
	root, err := filepath.Abs(filepath.Join(filepath.Dir(thisFile), "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func dartOrSkip(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("dart"); err != nil {
		t.Skipf("dart SDK not found in PATH: %v", err)
	}
}

func TestMCPServerToolsAndExecution(t *testing.T) {
	dartOrSkip(t)
	root := moduleRoot(t)
	spec := "dartrun:" + filepath.Join(root, "adapters", "dart")

	tmpDir, err := os.MkdirTemp("", "codeflow-mcp-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	token := mcp.GenerateAuthToken()
	cfg := mcp.Config{
		RepoRoot:     filepath.Join(root, "testdata", "example_app"),
		AuthToken:    token,
		DartAdapter:  spec,
		RequireToken: true,
	}

	srv, err := mcp.NewServer(cfg)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 1. Initialize call
	inBuf := &bytes.Buffer{}
	outBuf := &bytes.Buffer{}

	reqInit := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}` + "\n"
	reqList := `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}` + "\n"
	inBuf.WriteString(reqInit)
	inBuf.WriteString(reqList)

	err = srv.Serve(ctx, inBuf, outBuf)
	if err != nil {
		t.Fatalf("Serve error: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(outBuf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 responses, got %d", len(lines))
	}

	var respList struct {
		Result struct {
			Tools []struct {
				Name        string `json:"name"`
				InputSchema struct {
					Properties map[string]any `json:"properties"`
				} `json:"inputSchema"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(lines[1]), &respList); err != nil {
		t.Fatalf("unmarshal tools/list response: %v", err)
	}
	if len(respList.Result.Tools) != 24 {
		t.Errorf("expected 24 MCP tools, got %d", len(respList.Result.Tools))
	}
	for _, tool := range respList.Result.Tools {
		if _, ok := tool.InputSchema.Properties["target"]; !ok {
			t.Errorf("tool %s missing 'target' in inputSchema.properties", tool.Name)
		}
	}

	// 2. Call submit_flow_draft without token -> must fail
	inBuf2 := &bytes.Buffer{}
	outBuf2 := &bytes.Buffer{}
	reqSubmitNoToken := `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"submit_flow_draft","arguments":{"artifact":{"foo":"bar"}}}}` + "\n"
	inBuf2.WriteString(reqSubmitNoToken)

	_ = srv.Serve(ctx, inBuf2, outBuf2)
	var respSubmit struct {
		Result struct {
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	_ = json.Unmarshal(outBuf2.Bytes(), &respSubmit)
	if !respSubmit.Result.IsError {
		t.Errorf("expected isError true for missing auth token")
	}
}

func TestMCPToolsListSemanticApprovalRequiresProposalAndEvidencePackID(t *testing.T) {
	srv, err := mcp.NewServer(mcp.Config{RepoRoot: t.TempDir()})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var in, out bytes.Buffer
	in.WriteString(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}` + "\n")
	if err := srv.Serve(ctx, &in, &out); err != nil {
		t.Fatalf("Serve tools/list: %v", err)
	}

	var response struct {
		Result struct {
			Tools []struct {
				Name        string `json:"name"`
				InputSchema struct {
					Type       string `json:"type"`
					Properties map[string]struct {
						Type string   `json:"type"`
						Enum []string `json:"enum"`
					} `json:"properties"`
					Required []string `json:"required"`
				} `json:"inputSchema"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out.Bytes(), &response); err != nil {
		t.Fatalf("unmarshal tools/list response: %v", err)
	}

	var approvalTool *struct {
		Name        string `json:"name"`
		InputSchema struct {
			Type       string `json:"type"`
			Properties map[string]struct {
				Type string   `json:"type"`
				Enum []string `json:"enum"`
			} `json:"properties"`
			Required []string `json:"required"`
		} `json:"inputSchema"`
	}
	for i := range response.Result.Tools {
		tool := &response.Result.Tools[i]
		if tool.Name == "submit_semantic_approval" {
			approvalTool = (*struct {
				Name        string `json:"name"`
				InputSchema struct {
					Type       string `json:"type"`
					Properties map[string]struct {
						Type string   `json:"type"`
						Enum []string `json:"enum"`
					} `json:"properties"`
					Required []string `json:"required"`
				} `json:"inputSchema"`
			})(tool)
			break
		}
	}
	if approvalTool == nil {
		t.Fatal("tools/list did not expose submit_semantic_approval")
	}
	if got := approvalTool.InputSchema.Type; got != "object" {
		t.Fatalf("submit_semantic_approval inputSchema.type = %q, want object", got)
	}
	requiredFields := []string{
		"commandId", "proposalId", "evidencePackId", "computedBasisId", "generationId",
		"intentRevision", "decision", "idempotencyKey", "expectedApprovalVersion", "expectedState",
	}
	wantTypes := map[string]string{
		"commandId": "string", "proposalId": "string", "evidencePackId": "string",
		"computedBasisId": "string", "generationId": "string", "intentRevision": "integer",
		"decision": "string", "editedText": "string", "idempotencyKey": "string",
		"expectedApprovalVersion": "integer", "expectedState": "string", "predecessorApprovalId": "string",
	}
	for name, wantType := range wantTypes {
		property, ok := approvalTool.InputSchema.Properties[name]
		if !ok || property.Type != wantType {
			t.Fatalf("submit_semantic_approval property %q = %#v, want %s", name, property, wantType)
		}
	}
	for _, forbidden := range []string{"approver", "actorId", "sessionId", "workspaceId", "modifiedValues"} {
		if _, ok := approvalTool.InputSchema.Properties[forbidden]; ok {
			t.Fatalf("submit_semantic_approval exposed forbidden caller field %q", forbidden)
		}
	}
	counts := make(map[string]int, len(approvalTool.InputSchema.Required))
	for _, name := range approvalTool.InputSchema.Required {
		counts[name]++
	}
	if len(approvalTool.InputSchema.Required) != len(requiredFields) {
		t.Fatalf("submit_semantic_approval required = %#v, want complete v2 draft fields", approvalTool.InputSchema.Required)
	}
	for _, name := range requiredFields {
		if counts[name] != 1 {
			t.Fatalf("submit_semantic_approval required %q count = %d, want exactly once", name, counts[name])
		}
	}
	wantDecisions := []string{"approve", "edit_then_approve", "reject", "revoke", "supersede"}
	if !reflect.DeepEqual(approvalTool.InputSchema.Properties["decision"].Enum, wantDecisions) {
		t.Fatalf("submit_semantic_approval decision enum = %#v, want %#v", approvalTool.InputSchema.Properties["decision"].Enum, wantDecisions)
	}
}

func TestMCPServeToolsListSemanticApprovalSchemaConstraints(t *testing.T) {
	srv, err := mcp.NewServer(mcp.Config{RepoRoot: t.TempDir()})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}
	defer srv.Close()

	var in, out bytes.Buffer
	in.WriteString(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}` + "\n")
	if err := srv.Serve(context.Background(), &in, &out); err != nil {
		t.Fatalf("Serve tools/list: %v", err)
	}
	var response map[string]any
	if err := json.Unmarshal(out.Bytes(), &response); err != nil {
		t.Fatalf("decode tools/list response: %v", err)
	}
	result, ok := response["result"].(map[string]any)
	if !ok {
		t.Fatalf("tools/list result = %#v", response["result"])
	}
	tools, ok := result["tools"].([]any)
	if !ok {
		t.Fatalf("tools/list tools = %#v", result["tools"])
	}
	var approval map[string]any
	for _, rawTool := range tools {
		tool, ok := rawTool.(map[string]any)
		if ok && tool["name"] == "submit_semantic_approval" {
			approval = tool
			break
		}
	}
	if approval == nil {
		t.Fatal("tools/list did not expose submit_semantic_approval")
	}
	schema, ok := approval["inputSchema"].(map[string]any)
	if !ok {
		t.Fatalf("approval inputSchema = %#v", approval["inputSchema"])
	}
	if schema["type"] != "object" {
		t.Fatalf("approval inputSchema.type = %#v, want object", schema["type"])
	}
	if additional, ok := schema["additionalProperties"].(bool); !ok || additional {
		t.Fatalf("approval inputSchema.additionalProperties = %#v, want false", schema["additionalProperties"])
	}
	wantRequired := []any{
		"commandId", "proposalId", "evidencePackId", "computedBasisId", "generationId",
		"intentRevision", "decision", "idempotencyKey", "expectedApprovalVersion", "expectedState",
	}
	if !reflect.DeepEqual(schema["required"], wantRequired) {
		t.Fatalf("approval required = %#v, want %#v", schema["required"], wantRequired)
	}
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("approval properties = %#v", schema["properties"])
	}
	wantProperties := map[string]struct{}{
		"commandId": {}, "proposalId": {}, "evidencePackId": {}, "computedBasisId": {}, "generationId": {},
		"intentRevision": {}, "decision": {}, "editedText": {}, "idempotencyKey": {},
		"expectedApprovalVersion": {}, "expectedState": {}, "predecessorApprovalId": {},
		"target": {}, "token": {},
	}
	if len(properties) != len(wantProperties) {
		t.Fatalf("approval property count = %d, want %d: %#v", len(properties), len(wantProperties), properties)
	}
	for name := range properties {
		if _, ok := wantProperties[name]; !ok {
			t.Fatalf("approval exposed unknown property %q", name)
		}
	}
	for _, forbidden := range []string{"actorId", "sessionId", "workspaceId", "approver", "modifiedValues"} {
		if _, ok := properties[forbidden]; ok {
			t.Fatalf("approval exposed forbidden authority property %q", forbidden)
		}
	}
	property := func(name string) map[string]any {
		t.Helper()
		raw, ok := properties[name]
		if !ok {
			t.Fatalf("approval property %q is missing", name)
		}
		value, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("approval property %q = %#v, want object", name, raw)
		}
		return value
	}
	assertStringBounds := func(name string, min, max float64, wantMin bool) {
		t.Helper()
		value := property(name)
		if value["type"] != "string" {
			t.Fatalf("approval property %q type = %#v, want string", name, value["type"])
		}
		gotMax, ok := value["maxLength"].(float64)
		if !ok || gotMax != max {
			t.Fatalf("approval property %q maxLength = %#v, want %v", name, value["maxLength"], max)
		}
		gotMin, hasMin := value["minLength"].(float64)
		if wantMin {
			if !hasMin || gotMin != min {
				t.Fatalf("approval property %q minLength = %#v, want %v", name, value["minLength"], min)
			}
		} else if hasMin {
			t.Fatalf("approval property %q unexpectedly has minLength=%v", name, gotMin)
		}
	}
	assertStringBounds("commandId", 1, 256, true)
	assertStringBounds("idempotencyKey", 1, 256, true)
	for _, name := range []string{"proposalId", "evidencePackId", "computedBasisId", "generationId", "predecessorApprovalId"} {
		assertStringBounds(name, 0, 256, false)
	}
	assertStringBounds("editedText", 0, 4096, false)
	intent := property("intentRevision")
	if intent["type"] != "integer" || intent["minimum"] != float64(1) || intent["maximum"] != float64(1000000) {
		t.Fatalf("approval intentRevision constraints = %#v, want integer [1,1000000]", intent)
	}
	version := property("expectedApprovalVersion")
	if version["type"] != "integer" || version["minimum"] != float64(0) || version["maximum"] != float64(1000000000) {
		t.Fatalf("approval expectedApprovalVersion constraints = %#v, want integer [0,1000000000]", version)
	}
	if decision := property("decision")["enum"]; !reflect.DeepEqual(decision, []any{"approve", "edit_then_approve", "reject", "revoke", "supersede"}) {
		t.Fatalf("approval decision enum = %#v, want exact v2 enum", decision)
	}
	if state := property("expectedState")["enum"]; !reflect.DeepEqual(state, []any{"none", "active", "rejected", "revoked", "superseded"}) {
		t.Fatalf("approval expectedState enum = %#v, want exact lifecycle states", state)
	}
	for _, transport := range []string{"target", "token"} {
		if property(transport)["type"] != "string" {
			t.Fatalf("approval transport property %q = %#v, want string", transport, property(transport)["type"])
		}
	}
}

func TestMCPServer_DetectionFailurePolicy(t *testing.T) {
	emptyTmpDir, err := os.MkdirTemp("", "codeflow-detect-fail-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(emptyTmpDir)

	srv, err := mcp.NewServer(mcp.Config{
		RepoRoot:     emptyTmpDir,
		RequireToken: false,
	})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	inBuf := &bytes.Buffer{}
	outBuf := &bytes.Buffer{}

	reqHarvest := `{"jsonrpc":"2.0","id":40,"method":"tools/call","params":{"name":"harvest_flows","arguments":{"target":"."}}}` + "\n"
	inBuf.WriteString(reqHarvest)

	if err := srv.Serve(ctx, inBuf, outBuf); err != nil {
		t.Fatalf("Serve error: %v", err)
	}

	var resp struct {
		Result struct {
			IsError bool `json:"isError"`
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if err := json.Unmarshal(outBuf.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal error response: %v, raw=%s", err, outBuf.String())
	}
	if !resp.Result.IsError {
		t.Fatalf("expected isError true when language detection is not confident on empty dir")
	}
	if len(resp.Result.Content) == 0 || !strings.Contains(resp.Result.Content[0].Text, "could not confidently detect project language") {
		t.Errorf("expected actionable detection failure message, got: %v", resp.Result.Content)
	}
}

func TestMCPServer_Bootstrap_EmptyCWD(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "codeflow-empty-cwd-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Start server in an empty CWD with no adapter configured
	srv, err := mcp.NewServer(mcp.Config{
		RepoRoot:     tmpDir,
		RequireToken: false,
	})
	if err != nil {
		t.Fatalf("NewServer failed on empty CWD: %v", err)
	}
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	inBuf := &bytes.Buffer{}
	outBuf := &bytes.Buffer{}

	reqInit := `{"jsonrpc":"2.0","id":10,"method":"initialize","params":{}}` + "\n"
	reqList := `{"jsonrpc":"2.0","id":11,"method":"tools/list","params":{}}` + "\n"
	inBuf.WriteString(reqInit)
	inBuf.WriteString(reqList)

	if err := srv.Serve(ctx, inBuf, outBuf); err != nil {
		t.Fatalf("Serve error: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(outBuf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 responses, got %d", len(lines))
	}

	// Verify .codeflow was NOT created in empty CWD during bootstrap
	if _, err := os.Stat(filepath.Join(tmpDir, ".codeflow")); !os.IsNotExist(err) {
		t.Errorf(".codeflow directory was unexpectedly created in CWD during bootstrap")
	}
}

func TestMCPServer_Error_MissingRuntime(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "codeflow-missing-runtime-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	srv, err := mcp.NewServer(mcp.Config{
		RepoRoot:     tmpDir,
		DartAdapter:  "/non/existent/dart_adapter_path",
		RequireToken: false,
	})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	inBuf := &bytes.Buffer{}
	outBuf := &bytes.Buffer{}

	// Calling harvest_flows when adapter path is invalid should return tool error without crashing
	reqHarvest := `{"jsonrpc":"2.0","id":20,"method":"tools/call","params":{"name":"harvest_flows","arguments":{"target":"."}}}` + "\n"
	inBuf.WriteString(reqHarvest)

	if err := srv.Serve(ctx, inBuf, outBuf); err != nil {
		t.Fatalf("Serve error: %v", err)
	}

	var resp struct {
		Result struct {
			IsError bool `json:"isError"`
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if err := json.Unmarshal(outBuf.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal error response: %v, raw=%s", err, outBuf.String())
	}
	if !resp.Result.IsError {
		t.Errorf("expected isError true when runtime/adapter is missing")
	}
}

func TestMCPServer_DynamicTarget(t *testing.T) {
	dartOrSkip(t)
	root := moduleRoot(t)
	spec := "dartrun:" + filepath.Join(root, "adapters", "dart")

	emptyTmpDir, err := os.MkdirTemp("", "codeflow-dynamic-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(emptyTmpDir)

	// Start server with empty CWD
	srv, err := mcp.NewServer(mcp.Config{
		RepoRoot:     emptyTmpDir,
		DartAdapter:  spec,
		RequireToken: false,
	})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	inBuf := &bytes.Buffer{}
	outBuf := &bytes.Buffer{}

	// Call harvest_flows passing dynamic target
	targetRepo := filepath.Join(root, "testdata", "example_app")
	reqArgs, _ := json.Marshal(map[string]any{
		"target": targetRepo,
	})
	reqCall := fmt.Sprintf(`{"jsonrpc":"2.0","id":30,"method":"tools/call","params":{"name":"harvest_flows","arguments":%s}}`+"\n", string(reqArgs))
	inBuf.WriteString(reqCall)

	if err := srv.Serve(ctx, inBuf, outBuf); err != nil {
		t.Fatalf("Serve error: %v", err)
	}

	var resp struct {
		Result struct {
			IsError bool `json:"isError"`
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if err := json.Unmarshal(outBuf.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v, raw=%s", err, outBuf.String())
	}
	if resp.Result.IsError {
		t.Fatalf("expected successful harvest on dynamic target, got error: %v", resp.Result.Content)
	}
}

func TestMCPServer_NotificationsAndLifecycle(t *testing.T) {
	srv, err := mcp.NewServer(mcp.Config{RepoRoot: t.TempDir()})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var inBuf, outBuf bytes.Buffer

	// Standard MCP client sequence:
	// 1. initialize (request)
	// 2. notifications/initialized (notification - MUST NOT reply)
	// 3. ping (request)
	// 4. notifications/cancelled (notification - MUST NOT reply)
	// 5. tools/list (request)
	inBuf.WriteString(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}` + "\n")
	inBuf.WriteString(`{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n")
	inBuf.WriteString(`{"jsonrpc":"2.0","id":2,"method":"ping","params":{}}` + "\n")
	inBuf.WriteString(`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":99}}` + "\n")
	inBuf.WriteString(`{"jsonrpc":"2.0","id":3,"method":"tools/list","params":{}}` + "\n")

	if err := srv.Serve(ctx, &inBuf, &outBuf); err != nil {
		t.Fatalf("Serve failed: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(outBuf.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected exactly 3 responses for 3 requests + 2 notifications, got %d:\n%s", len(lines), outBuf.String())
	}

	// Verify IDs match the 3 requests (1, 2, 3) in order
	var r1, r2, r3 struct {
		ID    int `json:"id"`
		Error any `json:"error"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &r1); err != nil || r1.ID != 1 {
		t.Errorf("expected response 1 to have ID 1, got ID=%d, err=%v", r1.ID, err)
	}
	if err := json.Unmarshal([]byte(lines[1]), &r2); err != nil || r2.ID != 2 {
		t.Errorf("expected response 2 to have ID 2, got ID=%d, err=%v", r2.ID, err)
	}
	if err := json.Unmarshal([]byte(lines[2]), &r3); err != nil || r3.ID != 3 {
		t.Errorf("expected response 3 to have ID 3, got ID=%d, err=%v", r3.ID, err)
	}
}
