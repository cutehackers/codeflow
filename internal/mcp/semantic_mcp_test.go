package mcp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"codeflow/internal/mcp"
)

func getModuleRoot(t *testing.T) string {
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

func TestVS02A7_MCPSemanticTools(t *testing.T) {
	root := getModuleRoot(t)
	repoRoot := filepath.Join(root, "test", "fixtures", "nextjs-app-fixture")
	tsAdapterSpec := "noderun:" + filepath.Join(root, "adapters", "typescript")

	srv, err := mcp.NewServer(mcp.Config{
		RepoRoot:     repoRoot,
		Language:     "typescript",
		AdapterSpec:  tsAdapterSpec,
		RequireToken: false,
	})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}
	defer srv.Close()

	// Helper to send a JSON-RPC request and read response
	callRPC := func(method string, params any) map[string]any {
		reqDoc := map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"method":  method,
			"params":  params,
		}
		raw, _ := json.Marshal(reqDoc)
		in := bytes.NewReader(append(raw, '\n'))
		var out bytes.Buffer

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		done := make(chan error, 1)
		go func() {
			done <- srv.Serve(ctx, in, &out)
		}()
		<-done

		var resp map[string]any
		_ = json.Unmarshal(out.Bytes(), &resp)
		return resp
	}

	// 1. Verify tools/list contains query_task_view and get_current_answer
	listResp := callRPC("tools/list", map[string]any{})
	resObj, _ := listResp["result"].(map[string]any)
	toolsList, _ := resObj["tools"].([]any)

	foundQuery := false
	foundAnswer := false
	for _, tool := range toolsList {
		tm, _ := tool.(map[string]any)
		name, _ := tm["name"].(string)
		if name == "query_task_view" {
			foundQuery = true
		}
		if name == "get_current_answer" {
			foundAnswer = true
		}
	}
	if !foundQuery {
		t.Error("query_task_view tool not found in tools/list")
	}
	if !foundAnswer {
		t.Error("get_current_answer tool not found in tools/list")
	}

	// 2. Test query_task_view with missing precondition
	badCall := callRPC("tools/call", map[string]any{
		"name": "query_task_view",
		"arguments": map[string]any{
			"query": map[string]any{
				"schemaId":      "https://codeflow.local/schemas/task-view-query.schema.json",
				"schemaVersion": 1,
				"mode":          "feature",
				"feature":       map[string]any{},
			},
			"target": repoRoot,
		},
	})
	contentList, _ := badCall["result"].(map[string]any)["content"].([]any)
	if len(contentList) == 0 {
		t.Fatal("expected error content for missing precondition")
	}
	errText := contentList[0].(map[string]any)["text"].(string)
	if !strings.Contains(errText, "missing_precondition") {
		t.Errorf("expected missing_precondition error, got: %s", errText)
	}

	// 3. Test query_task_view with ambiguous query
	ambCall := callRPC("tools/call", map[string]any{
		"name": "query_task_view",
		"arguments": map[string]any{
			"query": map[string]any{
				"schemaId":      "https://codeflow.local/schemas/task-view-query.schema.json",
				"schemaVersion": 1,
				"mode":          "feature",
				"feature": map[string]any{
					"request": "checkout",
				},
			},
			"target": repoRoot,
		},
	})
	ambContent, _ := ambCall["result"].(map[string]any)["content"].([]any)
	ambText := ambContent[0].(map[string]any)["text"].(string)
	if !strings.Contains(ambText, "ambiguous_target") {
		t.Errorf("expected ambiguous_target error for 'checkout', got: %s", ambText)
	}

	// 4. Test query_task_view with unambiguous entrySymbol
	validCall := callRPC("tools/call", map[string]any{
		"name": "query_task_view",
		"arguments": map[string]any{
			"query": map[string]any{
				"schemaId":      "https://codeflow.local/schemas/task-view-query.schema.json",
				"schemaVersion": 1,
				"mode":          "feature",
				"feature": map[string]any{
					"entrySymbol": "app/page.tsx#HomePage.handleQuickCheckout",
				},
			},
			"target": repoRoot,
		},
	})
	valResult, _ := validCall["result"].(map[string]any)
	valContent, _ := valResult["content"].([]any)
	if len(valContent) == 0 {
		t.Fatalf("expected valid result content, got: %+v", validCall)
	}
	resText := valContent[0].(map[string]any)["text"].(string)

	var payload map[string]any
	if err := json.Unmarshal([]byte(resText), &payload); err != nil {
		t.Fatalf("failed to parse result JSON: %v, raw text: %s", err, resText)
	}

	// Verify required sections in result
	if _, ok := payload["currentAnswer"]; !ok {
		t.Errorf("missing currentAnswer in query_task_view response: %s", resText)
	}
	if _, ok := payload["semanticMap"]; !ok {
		t.Errorf("missing semanticMap in query_task_view response: %s", resText)
	}
	if _, ok := payload["projection"]; !ok {
		t.Errorf("missing projection in query_task_view response: %s", resText)
	}

	// 5. Test get_current_answer tool with unambiguous entry
	ansCall := callRPC("tools/call", map[string]any{
		"name": "get_current_answer",
		"arguments": map[string]any{
			"query":  "app/page.tsx#HomePage.handleQuickCheckout",
			"target": repoRoot,
		},
	})
	ansResult, _ := ansCall["result"].(map[string]any)
	ansContent, _ := ansResult["content"].([]any)
	if len(ansContent) == 0 {
		t.Fatalf("expected answer result content, got: %+v", ansCall)
	}
	ansText := ansContent[0].(map[string]any)["text"].(string)
	var ansPayload map[string]any
	if err := json.Unmarshal([]byte(ansText), &ansPayload); err != nil {
		t.Fatalf("failed to parse answer JSON: %v, raw: %s", err, ansText)
	}
	if _, ok := ansPayload["current"]; !ok {
		t.Errorf("missing 'current' field in get_current_answer response: %s", ansText)
	}
}
