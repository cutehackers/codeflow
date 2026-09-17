package mcp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"codeflow/internal/agentgateway/mcp"
)

func moduleRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot determine source location")
	}
	root, err := filepath.Abs(filepath.Join(filepath.Dir(thisFile), "..", "..", ".."))
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

	// 1. Initialize call and tools/list
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
	if len(respList.Result.Tools) != 8 {
		t.Errorf("expected exactly 8 MCP tools, got %d", len(respList.Result.Tools))
	}
	expectedTools := map[string]bool{
		"publish_core_flow": true,
		"harvest_flows":     true,
		"get_flow_payload":  true,
		"analyze_flow":      true,
		"submit_flow_draft": true,
		"approve_step":      true,
		"report_unknowns":   true,
		"open_review":       true,
	}
	for _, tool := range respList.Result.Tools {
		if !expectedTools[tool.Name] {
			t.Errorf("unexpected tool in list: %s", tool.Name)
		}
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

	// 3. Call removed tool -> must return -32601 Method not found
	inBuf3 := &bytes.Buffer{}
	outBuf3 := &bytes.Buffer{}
	reqRemoved := `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"submit_semantic_approval","arguments":{}}}` + "\n"
	inBuf3.WriteString(reqRemoved)

	_ = srv.Serve(ctx, inBuf3, outBuf3)
	var respRemoved struct {
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(outBuf3.Bytes(), &respRemoved)
	if respRemoved.Error == nil || respRemoved.Error.Code != -32601 {
		t.Errorf("expected error code -32601 for removed tool call, got: %+v", respRemoved.Error)
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
