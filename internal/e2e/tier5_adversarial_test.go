package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codeflow/internal/fusion"
	"codeflow/internal/mcp"
)

// ---------------------------------------------------------------------------
// TIER 5: Adversarial Coverage Hardening & Stress Testing
// ---------------------------------------------------------------------------

func TestTier5_Adversarial_OversizedArtifactRejected(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skipf("node not found: %v", err)
	}
	root := moduleRoot(t)
	appDir := filepath.Join(root, "testdata", "ts_example_app")

	srv, err := mcp.NewServer(mcp.Config{
		RepoRoot:     appDir,
		Language:     "typescript",
		RequireToken: false,
	})
	if err != nil {
		t.Fatalf("mcp.NewServer failed: %v", err)
	}
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Generate a huge artifact (> 512 KiB)
	largeString := strings.Repeat("A", 600*1024)
	artifact := map[string]any{
		"flowId":          "flow-oversized000001",
		"entrySymbolPath": "src/features/auth/LoginView.tsx#handleSubmit",
		"title":           "Large Artifact",
		"description":     largeString,
		"steps": []map[string]any{
			{
				"ordinal": 1,
				"name":    "Large Step",
				"layer":   "presentation",
				"kind":    "guard",
				"anchor": map[string]any{
					"repoRelativePath":        "src/features/auth/LoginView.tsx",
					"byteRange":               []int{0, 10},
					"fileHash":                "0000000000000000000000000000000000000000000000000000000000000000",
					"spanHash":                "0000000000000000000000000000000000000000000000000000000000000000",
					"enclosingSymbolPath":     "handleSubmit",
					"canonicalAstFingerprint": "0000000000000000000000000000000000000000000000000000000000000000",
				},
			},
		},
	}

	pubReq := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "publish_core_flow",
			"arguments": map[string]any{
				"artifact": artifact,
			},
		},
	}
	pubBytes, _ := json.Marshal(pubReq)
	inBuf := bytes.NewBuffer(append(pubBytes, '\n'))
	outBuf := &bytes.Buffer{}

	if err := srv.Serve(ctx, inBuf, outBuf); err != nil && err != io.EOF {
		t.Fatalf("srv.Serve error: %v", err)
	}

	var pubResp struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	_ = json.Unmarshal(outBuf.Bytes(), &pubResp)
	if !pubResp.Result.IsError {
		t.Fatal("expected oversized artifact to be rejected with isError=true")
	}
	if !strings.Contains(pubResp.Result.Content[0].Text, "artifact_too_large") {
		t.Errorf("expected 'artifact_too_large' error code, got: %s", pubResp.Result.Content[0].Text)
	}
}

func TestTier5_Adversarial_MalformedJSONRPCResilience(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skipf("node not found: %v", err)
	}
	root := moduleRoot(t)
	appDir := filepath.Join(root, "testdata", "ts_example_app")

	srv, err := mcp.NewServer(mcp.Config{
		RepoRoot:     appDir,
		Language:     "typescript",
		RequireToken: false,
	})
	if err != nil {
		t.Fatalf("mcp.NewServer failed: %v", err)
	}
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Feed malformed JSON and garbage lines
	inputs := "not-json\n{malformed-json\n{\"jsonrpc\":\"2.0\",\"id\":99,\"method\":\"unknown_op\"}\n"
	inBuf := bytes.NewBufferString(inputs)
	outBuf := &bytes.Buffer{}

	if err := srv.Serve(ctx, inBuf, outBuf); err != nil && err != io.EOF {
		t.Fatalf("srv.Serve error on malformed input: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(outBuf.String()), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected error responses for malformed lines, got %d lines: %s", len(lines), outBuf.String())
	}
}

func TestTier5_Adversarial_AnchorVerificationFailures(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skipf("node not found: %v", err)
	}
	root := moduleRoot(t)
	appDir := filepath.Join(root, "testdata", "ts_example_app")

	srv, err := mcp.NewServer(mcp.Config{
		RepoRoot:     appDir,
		Language:     "typescript",
		RequireToken: false,
	})
	if err != nil {
		t.Fatalf("mcp.NewServer failed: %v", err)
	}
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 1. Missing file in anchor
	artifact := map[string]any{
		"flowId":          "flow-badanchor000001",
		"entrySymbolPath": "src/features/auth/LoginView.tsx#handleSubmit",
		"title":           "Bad Anchor",
		"steps": []map[string]any{
			{
				"ordinal": 1,
				"name":    "Step 1",
				"layer":   "presentation",
				"kind":    "guard",
				"anchor": map[string]any{
					"repoRelativePath":        "non_existent_file.ts",
					"byteRange":               []int{0, 10},
					"fileHash":                "0000000000000000000000000000000000000000000000000000000000000000",
					"spanHash":                "0000000000000000000000000000000000000000000000000000000000000000",
					"enclosingSymbolPath":     "handleSubmit",
					"canonicalAstFingerprint": "0000000000000000000000000000000000000000000000000000000000000000",
				},
			},
		},
	}

	pubReq := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "publish_core_flow",
			"arguments": map[string]any{
				"artifact": artifact,
			},
		},
	}
	pubBytes, _ := json.Marshal(pubReq)
	inBuf := bytes.NewBuffer(append(pubBytes, '\n'))
	outBuf := &bytes.Buffer{}

	_ = srv.Serve(ctx, inBuf, outBuf)

	var pubResp struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	_ = json.Unmarshal(outBuf.Bytes(), &pubResp)
	if !pubResp.Result.IsError {
		t.Fatal("expected missing file anchor to fail verification")
	}
	if !strings.Contains(pubResp.Result.Content[0].Text, "anchor_verification_failed") {
		t.Errorf("expected anchor_verification_failed, got: %s", pubResp.Result.Content[0].Text)
	}
}

func TestTier5_Adversarial_LayerOrderMonotonicityViolations(t *testing.T) {
	steps := []struct {
		Layer string
		Kind  string
	}{
		{Layer: "presentation", Kind: "guard"},
		{Layer: "data", Kind: "mutation"},
		{Layer: "controller", Kind: "call"}, // backward jump from data to controller without branch!
	}
	declared := []string{"presentation", "controller", "usecase", "domain", "data"}

	cfg, err := fusion.LoadLayersConfig("")
	if err != nil {
		t.Fatalf("LoadLayersConfig failed: %v", err)
	}
	cfg.StrictOrder = true

	_, err = fusion.ValidateLayerOrder(steps, declared, cfg)
	if err == nil {
		t.Fatal("expected strict layer order violation error for backward jump, got nil")
	}
	if !strings.Contains(err.Error(), "layer_order_violation") {
		t.Errorf("expected 'layer_order_violation' in error, got: %v", err)
	}

	// Now with StrictOrder: false, it should return warnings instead of error
	cfg.StrictOrder = false
	warnings, err := fusion.ValidateLayerOrder(steps, declared, cfg)
	if err != nil {
		t.Fatalf("expected no error when StrictOrder=false, got: %v", err)
	}
	if len(warnings) == 0 {
		t.Fatal("expected warnings for backward jump when StrictOrder=false")
	}
}
