package mcp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
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
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(lines[1]), &respList); err != nil {
		t.Fatalf("unmarshal tools/list response: %v", err)
	}
	if len(respList.Result.Tools) != 7 {
		t.Errorf("expected 7 MCP tools, got %d", len(respList.Result.Tools))
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
