package agentgateway_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"codeflow/internal/agentgateway"
)

func TestAgentGateway_ToolsListAndUnknownRejection(t *testing.T) {
	tempDir := t.TempDir()
	srv, err := agentgateway.NewServer(agentgateway.Config{
		RepoRoot:     tempDir,
		RequireToken: false,
	})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var inBuf, outBuf bytes.Buffer

	// 1. Initialize
	inBuf.WriteString(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}` + "\n")
	// 2. tools/list
	inBuf.WriteString(`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}` + "\n")
	// 3. call removed legacy tool
	inBuf.WriteString(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"submit_semantic_approval","arguments":{}}}` + "\n")

	if err := srv.Serve(ctx, &inBuf, &outBuf); err != nil {
		t.Fatalf("Serve failed: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(outBuf.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 responses, got %d: %s", len(lines), outBuf.String())
	}

	// Verify tools/list has exactly the 8 core tools
	var listResp struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(lines[1]), &listResp); err != nil {
		t.Fatalf("unmarshal tools/list: %v", err)
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

	if len(listResp.Result.Tools) != 8 {
		t.Fatalf("expected exactly 8 tools, got %d", len(listResp.Result.Tools))
	}
	for _, tool := range listResp.Result.Tools {
		if !expectedTools[tool.Name] {
			t.Errorf("unexpected tool in list: %s", tool.Name)
		}
	}

	// Verify call to removed tool returns -32601 Method not found
	var callResp struct {
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(lines[2]), &callResp); err != nil {
		t.Fatalf("unmarshal tool call error: %v", err)
	}
	if callResp.Error == nil || callResp.Error.Code != -32601 {
		t.Fatalf("expected error code -32601, got: %+v", callResp.Error)
	}
}
