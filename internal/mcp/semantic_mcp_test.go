package mcp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"codeflow/internal/flowview"
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
	if _, ok := payload["candidateAnswer"]; !ok {
		t.Errorf("missing candidateAnswer in query_task_view response: %s", resText)
	}
	if _, ok := payload["semanticMap"]; !ok {
		t.Errorf("missing semanticMap in query_task_view response: %s", resText)
	}
	if _, ok := payload["projection"]; !ok {
		t.Errorf("missing projection in query_task_view response: %s", resText)
	}
	flowView, ok := payload["flowView"].(map[string]any)
	if !ok {
		t.Fatalf("missing Live Semantic Map FlowView response: %s", resText)
	}
	if flowView["status"] != "ready" || flowView["mode"] != "live_semantic_map" || flowView["autoOpen"] != true {
		t.Errorf("unexpected Live Semantic Map FlowView state: %+v", flowView)
	}
	flowViewURL, _ := flowView["url"].(string)
	if flowView["template"] != flowview.LiveSemanticTemplate {
		t.Fatalf("MCP must identify the designated Live template: %+v", flowView)
	}
	parsedViewURL, parseErr := url.Parse(flowViewURL)
	if parseErr != nil || (parsedViewURL.Path != "/" && parsedViewURL.Path != "") {
		t.Fatalf("feature request must return the static FlowView URL: %v (path: %s)", parseErr, parsedViewURL.Path)
	}
	viewResponse, err := http.Get(flowViewURL)
	if err != nil {
		t.Fatalf("MCP returned an unreachable FlowView screen: %v", err)
	}
	viewBody, readErr := io.ReadAll(viewResponse.Body)
	viewResponse.Body.Close()
	if readErr != nil || viewResponse.StatusCode != http.StatusOK || !bytes.Contains(viewBody, []byte(`CODEFLOW · FLOWVIEW`)) {
		t.Fatalf("MCP URL did not serve static FlowView: status %d, read error %v", viewResponse.StatusCode, readErr)
	}

	// Coordinator /live endpoint serves the Live Semantic Map
	liveURL := parsedViewURL.Scheme + "://" + parsedViewURL.Host + "/live"
	if tok := parsedViewURL.Query().Get("token"); tok != "" {
		liveURL += "?token=" + tok
	}
	liveResponse, err := http.Get(liveURL)
	if err != nil {
		t.Fatalf("coordinator did not serve /live: %v", err)
	}
	liveBody, readErr := io.ReadAll(liveResponse.Body)
	liveResponse.Body.Close()
	if readErr != nil || liveResponse.StatusCode != http.StatusOK || !bytes.Contains(liveBody, []byte(`data-view="live-semantic-map"`)) {
		t.Fatalf("coordinator URL did not serve the Live Semantic Map: status %d, read error %v", liveResponse.StatusCode, readErr)
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
	if ansPayload["code"] != "no_current_proof" {
		t.Errorf("expected typed no_current_proof response, got: %s", ansText)
	}
}
