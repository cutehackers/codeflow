package mcp_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codeflow/internal/mcp"
)

// helper to compute anchor for a file span
func computeAnchor(t *testing.T, repoRoot, repoRel string, start, end int, enclosing string) map[string]any {
	t.Helper()
	full := filepath.Join(repoRoot, repoRel)
	data, err := os.ReadFile(full)
	if err != nil {
		t.Fatalf("read %s: %v", full, err)
	}
	if start < 0 || end > len(data) || start > end {
		t.Fatalf("invalid range %d:%d for file len %d", start, end, len(data))
	}
	fileHash := sha256.Sum256(data)
	spanHash := sha256.Sum256(data[start:end])
	// canonicalAstFingerprint placeholder: same as spanHash for test
	canonical := hex.EncodeToString(spanHash[:])
	return map[string]any{
		"repoRelativePath":        repoRel,
		"byteRange":               []int{start, end},
		"fileHash":                hex.EncodeToString(fileHash[:]),
		"spanHash":                hex.EncodeToString(spanHash[:]),
		"enclosingSymbolPath":     enclosing,
		"canonicalAstFingerprint": canonical,
	}
}

func writeTestFile(t *testing.T, repoRoot, rel, content string) {
	t.Helper()
	full := filepath.Join(repoRoot, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestPublishCoreFlow_HappyPathAndIdempotent(t *testing.T) {
	repoRoot := t.TempDir()
	// Create codeflow.layers.yaml (use default fixture copy)
	// Use the repo's codeflow.layers.yaml as template
	// For test, just create minimal valid one
	os.WriteFile(filepath.Join(repoRoot, "codeflow.layers.yaml"), []byte("version: 1\nstrictOrder: true\nallowUnknownLayer: false\nlayers:\n  - name: presentation\n  - name: controller\n  - name: usecase\n  - name: data\n  - name: external\n"), 0644)

	content := "class JoinController {\n  void submit() {\n    print('hello');\n  }\n}\nclass SignupUseCase {\n  void call() {\n    print('usecase');\n  }\n}\n"
	writeTestFile(t, repoRoot, "lib/features/auth/join_controller.dart", content)
	writeTestFile(t, repoRoot, "lib/features/auth/signup_usecase.dart", content)

	// Compute anchors: pick a span inside each file
	anc1 := computeAnchor(t, repoRoot, "lib/features/auth/join_controller.dart", 0, 10, "JoinController.submit")
	anc2 := computeAnchor(t, repoRoot, "lib/features/auth/signup_usecase.dart", 0, 10, "SignupUseCase.call")

	cfg := mcp.Config{RepoRoot: repoRoot}
	srv, err := mcp.NewServer(cfg)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer srv.Close()
	ctx := context.Background()

	artifact := map[string]any{
		"entrySymbolPath": "lib/features/auth/join_controller.dart#JoinController.submit",
		"title":           "회원가입 핵심 흐름",
		"description":     "가입",
		"layers":          []string{"presentation", "usecase"},
		"steps": []map[string]any{
			{"ordinal": 1, "name": "입력 검증", "layer": "presentation", "kind": "guard", "anchor": anc1},
			{"ordinal": 2, "name": "가입 위임", "layer": "usecase", "kind": "call", "anchor": anc2},
		},
		"edges": []map[string]any{
			{"stepOrdinal": 1, "toSymbolPath": "lib/features/auth/signup_usecase.dart#SignupUseCase.call", "toLayer": "usecase", "kind": "resolved_cross_file", "resolutionStatus": "resolved"},
		},
	}

	// First publish via JSON-RPC Serve
	payload, _ := json.Marshal(map[string]any{"artifact": artifact})
	// Use direct execute via Serve
	var buf bytes.Buffer
	req := map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "publish_core_flow", "arguments": map[string]any{"artifact": artifact}}}
	line, _ := json.Marshal(req)
	buf.Write(line)
	buf.WriteByte('\n')
	var out bytes.Buffer
	if err := srv.Serve(ctx, &buf, &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	_ = payload // ensure payload used
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected 1 response, got %d: %v", len(lines), out.String())
	}
	var resp struct {
		Result struct {
			Content []struct{ Text string `json:"text"` } `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Result.IsError {
		t.Fatalf("expected success, got error: %v", resp.Result.Content[0].Text)
	}
	var success map[string]any
	if err := json.Unmarshal([]byte(resp.Result.Content[0].Text), &success); err != nil {
		t.Fatalf("unmarshal success payload: %v", err)
	}
	if success["status"] != "published" {
		t.Errorf("status = %v want published", success["status"])
	}
	if int(success["stepCount"].(float64)) != 2 {
		t.Errorf("stepCount = %v want 2", success["stepCount"])
	}
	flowID := success["flowId"].(string)

	// Second publish same artifact → idempotent (replace, not duplicate)
	buf.Reset()
	out.Reset()
	buf.Write(line)
	buf.WriteByte('\n')
	if err := srv.Serve(ctx, &buf, &out); err != nil {
		t.Fatalf("second Serve: %v", err)
	}
	lines = strings.Split(strings.TrimSpace(out.String()), "\n")
	var resp2 struct {
		Result struct {
			Content []struct{ Text string `json:"text"` } `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	json.Unmarshal([]byte(lines[0]), &resp2)
	if resp2.Result.IsError {
		t.Fatalf("second publish should succeed: %v", resp2.Result.Content[0].Text)
	}
	var success2 map[string]any
	json.Unmarshal([]byte(resp2.Result.Content[0].Text), &success2)
	if success2["flowId"] != flowID {
		t.Errorf("idempotent flowId mismatch %v vs %v", success2["flowId"], flowID)
	}

	// Verify get_flow_payload returns stored spec with layer
	buf.Reset()
	out.Reset()
	req2 := map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": map[string]any{"name": "get_flow_payload", "arguments": map[string]any{"flowId": flowID}}}
	line2, _ := json.Marshal(req2)
	buf.Write(line2)
	buf.WriteByte('\n')
	srv.Serve(ctx, &buf, &out)
	lines = strings.Split(strings.TrimSpace(out.String()), "\n")
	var resp3 struct {
		Result struct {
			Content []struct{ Text string `json:"text"` } `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	json.Unmarshal([]byte(lines[0]), &resp3)
	if resp3.Result.IsError {
		t.Fatalf("get_flow_payload failed: %v", resp3.Result.Content[0].Text)
	}
	var spec map[string]any
	json.Unmarshal([]byte(resp3.Result.Content[0].Text), &spec)
	steps := spec["steps"].([]any)
	if len(steps) != 2 {
		t.Fatalf("steps len %d", len(steps))
	}
	firstStep := steps[0].(map[string]any)
	if firstStep["layer"] != "presentation" {
		t.Errorf("layer = %v want presentation", firstStep["layer"])
	}
}

func TestPublishCoreFlow_Errors(t *testing.T) {
	repoRoot := t.TempDir()
	os.WriteFile(filepath.Join(repoRoot, "codeflow.layers.yaml"), []byte("version: 1\nstrictOrder: true\nallowUnknownLayer: false\nlayers:\n  - name: presentation\n  - name: controller\n  - name: usecase\n  - name: data\n"), 0644)
	content := "class A { void foo() {} }"
	writeTestFile(t, repoRoot, "lib/a.dart", content)
	ancOK := computeAnchor(t, repoRoot, "lib/a.dart", 0, 5, "A.foo")

	cfg := mcp.Config{RepoRoot: repoRoot}
	srv, err := mcp.NewServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	call := func(art map[string]any, token any) (bool, string) {
		args := map[string]any{"artifact": art}
		if token != nil {
			args["token"] = token
		}
		// Build JSON-RPC
		var buf bytes.Buffer
		req := map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "publish_core_flow", "arguments": args}}
		line, _ := json.Marshal(req)
		buf.Write(line)
		buf.WriteByte('\n')
		var out bytes.Buffer
		srv.Serve(ctx, &buf, &out)
		var resp struct {
			Result struct {
				Content []struct{ Text string `json:"text"` } `json:"content"`
				IsError bool `json:"isError"`
			} `json:"result"`
		}
		json.Unmarshal([]byte(strings.TrimSpace(out.String())), &resp)
		if len(resp.Result.Content) == 0 {
			return false, ""
		}
		return resp.Result.IsError, resp.Result.Content[0].Text
	}

	t.Run("schema_validation_failed missing layer", func(t *testing.T) {
		art := map[string]any{
			"entrySymbolPath": "lib/a.dart#A.foo",
			"title":           "t",
			"steps": []map[string]any{
				{"ordinal": 1, "name": "s1", "kind": "call", "anchor": ancOK}, // missing layer
			},
		}
		isErr, txt := call(art, nil)
		if !isErr {
			t.Fatalf("expected error, got success %v", txt)
		}
		if !strings.Contains(txt, "schema_validation_failed") {
			t.Errorf("expected schema_validation_failed, got %v", txt)
		}
	})

	t.Run("anchor_verification_failed span mismatch", func(t *testing.T) {
		badAnc := computeAnchor(t, repoRoot, "lib/a.dart", 0, 5, "A.foo")
		// corrupt spanHash
		badAnc["spanHash"] = strings.Repeat("0", 64)
		art := map[string]any{
			"entrySymbolPath": "lib/a.dart#A.foo",
			"title":           "t",
			"steps": []map[string]any{
				{"ordinal": 1, "name": "s1", "layer": "presentation", "kind": "call", "anchor": badAnc},
			},
		}
		isErr, txt := call(art, nil)
		if !isErr || !strings.Contains(txt, "anchor_verification_failed") {
			t.Errorf("expected anchor_verification_failed, got %v", txt)
		}
	})

	t.Run("anchor file_not_found", func(t *testing.T) {
		anc := computeAnchor(t, repoRoot, "lib/a.dart", 0, 5, "A.foo")
		anc["repoRelativePath"] = "lib/missing.dart"
		art := map[string]any{
			"entrySymbolPath": "lib/a.dart#A.foo",
			"title":           "t",
			"steps": []map[string]any{
				{"ordinal": 1, "name": "s1", "layer": "presentation", "kind": "call", "anchor": anc},
			},
		}
		isErr, txt := call(art, nil)
		if !isErr || !strings.Contains(txt, "anchor_verification_failed") {
			t.Errorf("expected anchor_verification_failed file_not_found, got %v", txt)
		}
	})

	t.Run("layer_order_violation strict", func(t *testing.T) {
		anc1 := computeAnchor(t, repoRoot, "lib/a.dart", 0, 5, "A.foo")
		anc2 := computeAnchor(t, repoRoot, "lib/a.dart", 5, 10, "A.foo")
		art := map[string]any{
			"entrySymbolPath": "lib/a.dart#A.foo",
			"title":           "t",
			"layers":          []string{"presentation", "usecase"},
			"steps": []map[string]any{
				{"ordinal": 1, "name": "s1", "layer": "usecase", "kind": "call", "anchor": anc1},
				{"ordinal": 2, "name": "s2", "layer": "presentation", "kind": "call", "anchor": anc2},
			},
		}
		isErr, txt := call(art, nil)
		if !isErr || !strings.Contains(txt, "layer_order_violation") {
			t.Errorf("expected layer_order_violation, got %v", txt)
		}
	})

	t.Run("artifact_too_large", func(t *testing.T) {
		anc := computeAnchor(t, repoRoot, "lib/a.dart", 0, 5, "A.foo")
		huge := strings.Repeat("x", 600*1024)
		art := map[string]any{
			"entrySymbolPath": "lib/a.dart#A.foo",
			"title":           huge,
			"steps": []map[string]any{
				{"ordinal": 1, "name": "s1", "layer": "presentation", "kind": "call", "anchor": anc},
			},
		}
		isErr, txt := call(art, nil)
		if !isErr || !strings.Contains(txt, "artifact_too_large") {
			t.Errorf("expected artifact_too_large, got %v", txt)
		}
	})

	t.Run("layers_config_invalid", func(t *testing.T) {
		repo2 := t.TempDir()
		writeTestFile(t, repo2, "lib/a.dart", content)
		os.WriteFile(filepath.Join(repo2, "codeflow.layers.yaml"), []byte("version: 1\nlayers: [invalid"), 0644)
		anc := computeAnchor(t, repo2, "lib/a.dart", 0, 5, "A.foo")
		cfg2 := mcp.Config{RepoRoot: repo2}
		srv2, _ := mcp.NewServer(cfg2)
		defer srv2.Close()
		art := map[string]any{
			"entrySymbolPath": "lib/a.dart#A.foo",
			"title":           "t",
			"steps": []map[string]any{
				{"ordinal": 1, "name": "s1", "layer": "presentation", "kind": "call", "anchor": anc},
			},
		}
		var buf bytes.Buffer
		req := map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "publish_core_flow", "arguments": map[string]any{"artifact": art}}}
		line, _ := json.Marshal(req)
		buf.Write(line)
		buf.WriteByte('\n')
		var out bytes.Buffer
		ctx2, cancel2 := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel2()
		srv2.Serve(ctx2, &buf, &out)
		var resp struct {
			Result struct {
				Content []struct{ Text string `json:"text"` } `json:"content"`
				IsError bool `json:"isError"`
			} `json:"result"`
		}
		json.Unmarshal([]byte(strings.TrimSpace(out.String())), &resp)
		if !resp.Result.IsError || !strings.Contains(resp.Result.Content[0].Text, "layers_config_invalid") {
			t.Errorf("expected layers_config_invalid, got %v", resp.Result.Content[0].Text)
		}
	})
}

func TestPublishCoreFlow_Unauthorized(t *testing.T) {
	repoRoot := t.TempDir()
	os.WriteFile(filepath.Join(repoRoot, "codeflow.layers.yaml"), []byte("version: 1\nlayers:\n  - name: presentation\n"), 0644)
	writeTestFile(t, repoRoot, "lib/a.dart", "class A { void foo() {} }")
	anc := computeAnchor(t, repoRoot, "lib/a.dart", 0, 5, "A.foo")
	cfg := mcp.Config{RepoRoot: repoRoot, AuthToken: "secret", RequireToken: true}
	srv, _ := mcp.NewServer(cfg)
	defer srv.Close()
	art := map[string]any{
		"entrySymbolPath": "lib/a.dart#A.foo",
		"title":           "t",
		"steps": []map[string]any{
			{"ordinal": 1, "name": "s1", "layer": "presentation", "kind": "call", "anchor": anc},
		},
	}
	var buf bytes.Buffer
	req := map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "publish_core_flow", "arguments": map[string]any{"artifact": art}}}
	line, _ := json.Marshal(req)
	buf.Write(line)
	buf.WriteByte('\n')
	var out bytes.Buffer
	srv.Serve(context.Background(), &buf, &out)
	var resp struct {
		Result struct {
			Content []struct{ Text string `json:"text"` } `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	json.Unmarshal([]byte(strings.TrimSpace(out.String())), &resp)
	if !resp.Result.IsError || !strings.Contains(resp.Result.Content[0].Text, "unauthorized") {
		t.Errorf("expected unauthorized, got %v", resp.Result.Content[0].Text)
	}
}
