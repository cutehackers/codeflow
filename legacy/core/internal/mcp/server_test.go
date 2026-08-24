package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"codeflow/core/internal/compiler"
	flowcore "codeflow/core/internal/core"
	"codeflow/core/internal/entrypoint"
	"codeflow/core/internal/flowir"
	"codeflow/core/internal/subgraph"
)

func TestBothProtocolErasReturnTheSameCoreEnvelope(t *testing.T) {
	repo := fixture(t)
	core, err := flowcore.StartFixture(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	defer core.Close(context.Background())
	modern := conversation(t, repo, Modern, `{"name":"current","arguments":{"flow_id":"route:/signup"}}`)
	legacy := conversation(t, repo, Legacy, `{"name":"current","arguments":{"flow_id":"route:/signup"}}`)
	if len(modern) != 2 || modern[1]["error"] != nil || len(legacy) != 2 || legacy[1]["error"] != nil {
		t.Fatalf("modern=%#v legacy=%#v", modern, legacy)
	}
	if modern[1]["result"].(map[string]any)["structuredContent"].(map[string]any)["basis"].(map[string]any)["worktree_fingerprint"] != legacy[1]["result"].(map[string]any)["structuredContent"].(map[string]any)["basis"].(map[string]any)["worktree_fingerprint"] {
		t.Fatal("eras diverged from the same Core snapshot")
	}
	result := modern[1]["result"].(map[string]any)
	text := result["content"].([]any)[0].(map[string]any)["text"].(string)
	structured := result["structuredContent"].(map[string]any)
	encoded, _ := json.Marshal(structured)
	if len(text) > 2048 || strings.Contains(text, `"manifest"`) || strings.Contains(string(encoded), `"manifest":`) {
		t.Fatalf("MCP repeated the repository manifest in model context: text=%d structured=%d", len(text), len(encoded))
	}
	if structured["basis"].(map[string]any)["manifest_count"] == nil {
		t.Fatal("compact MCP basis lost manifest cardinality")
	}
	for _, name := range []string{"workspace", "current", "diff", "step", "unknowns", "refresh", "open"} {
		got := conversation(t, repo, Modern, `{"name":"`+name+`","arguments":{"flow_id":"route:/signup"}}`)
		if len(got) != 2 {
			t.Fatalf("tool %s did not answer", name)
		}
	}
}
func TestUnsupportedProtocolAndConcurrentClientsReuseOneRuntime(t *testing.T) {
	repo := fixture(t)
	core, err := flowcore.StartFixture(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	defer core.Close(context.Background())
	bad := conversation(t, repo, "2099-01-01", ``)
	if bad[0]["error"] == nil {
		t.Fatal("unsupported version must be typed")
	}
	var wg sync.WaitGroup
	results := make([][]map[string]any, 2)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = conversation(t, repo, Modern, `{"name":"current","arguments":{"flow_id":"route:/signup"}}`)
		}(i)
	}
	wg.Wait()
	for _, got := range results {
		if len(got) != 2 || got[1]["error"] != nil {
			t.Fatalf("concurrent MCP client failed %#v", got)
		}
	}
	if _, err := os.Stat(filepath.Join(repo, ".codeflow", "state.db")); err != nil {
		t.Fatalf("MCP must reuse Core state, not create a competing database: %v", err)
	}
}

func TestFirstRequestedFlowStartsAndStopsAnOwnedCore(t *testing.T) {
	repo := fixture(t)
	var starts int
	server := Server{Repo: repo, Start: func(ctx context.Context, selectors []string) (*flowcore.Core, *compiler.Problem, error) {
		starts++
		if len(selectors) != 1 || selectors[0] != "route:/signup" {
			t.Fatalf("selectors=%#v", selectors)
		}
		core, err := flowcore.StartFixture(ctx, repo)
		return core, nil, err
	}}
	input := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"` + Modern + `"}}` + "\n" +
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"current","arguments":{"flow_id":"route:/signup"}}}` + "\n"
	var output bytes.Buffer
	if err := server.Serve(context.Background(), strings.NewReader(input), &output); err != nil {
		t.Fatal(err)
	}
	if starts != 1 || !strings.Contains(output.String(), `"status":"observed"`) {
		t.Fatalf("starts=%d output=%s", starts, output.String())
	}
	if _, err := os.Stat(filepath.Join(repo, ".codeflow", "runtime.json")); !os.IsNotExist(err) {
		t.Fatalf("owned Core remained after MCP closed: %v", err)
	}
}

func TestPerFlowToolsRequireExactIdentifiers(t *testing.T) {
	repo := fixture(t)
	core, err := flowcore.StartFixture(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	defer core.Close(context.Background())
	missingFlow := conversation(t, repo, Modern, `{"name":"current","arguments":{}}`)
	if missingFlow[1]["error"].(map[string]any)["message"] != "FLOW_ID_REQUIRED" {
		t.Fatalf("missing flow id was guessed: %#v", missingFlow)
	}
	missingStep := conversation(t, repo, Modern, `{"name":"step","arguments":{"flow_id":"route:/signup"}}`)
	if missingStep[1]["error"].(map[string]any)["message"] != "STEP_ID_REQUIRED" {
		t.Fatalf("missing step id was guessed: %#v", missingStep)
	}
}

func TestWorkspaceAndOpenKeepTheRequestedMultiFlowBasis(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "lib"), 0755); err != nil {
		t.Fatal(err)
	}
	source := "final routes = [GoRoute(path: '/signup', builder: (c, s) => const SignupPage()), GoRoute(path: '/settings', builder: (c, s) => const SettingsPage())];\nclass SignupPage { const SignupPage(); void build() { ElevatedButton(onPressed: _go); } void _go() { context.go('/settings'); } }\nclass SettingsPage { const SettingsPage(); void build() { ElevatedButton(onPressed: _go); } void _go() { context.go('/signup'); } }\n"
	if err := os.WriteFile(filepath.Join(repo, "lib", "routes.dart"), []byte(source), 0644); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, output)
	}
	_ = exec.Command("git", "-C", repo, "add", ".").Run()
	_ = exec.Command("git", "-C", repo, "-c", "user.email=x@y.z", "-c", "user.name=x", "commit", "-qm", "fixture").Run()
	_, file, _, _ := runtime.Caller(0)
	adapter := "dart " + filepath.Join(filepath.Dir(file), "../../../adapters/dart/bin/codeflow-dart-adapter.dart")
	core, problem, err := flowcore.StartAnalysis(context.Background(), repo, flowcore.AnalysisOptions{Selectors: []string{"route:/signup", "route:/settings"}, AdapterCommand: adapter})
	if err != nil || problem != nil {
		t.Fatalf("start multi-flow Core: err=%v problem=%#v", err, problem)
	}
	defer core.Close(context.Background())

	workspace := conversation(t, repo, Modern, `{"name":"workspace","arguments":{}}`)
	structured := workspace[1]["result"].(map[string]any)["structuredContent"].(map[string]any)
	data := structured["data"].(map[string]any)
	flowIDs := data["flow_ids"].([]any)
	if len(flowIDs) != 2 || flowIDs[0] != "route:/signup" || flowIDs[1] != "route:/settings" {
		t.Fatalf("workspace flow order=%#v", flowIDs)
	}
	opened := conversation(t, repo, Modern, `{"name":"open","arguments":{"flow_id":"route:/settings"}}`)
	openData := opened[1]["result"].(map[string]any)["structuredContent"].(map[string]any)["data"].(map[string]any)
	if view, _ := openData["view_url"].(string); !strings.Contains(view, "flow=route%3A%2Fsettings") {
		t.Fatalf("open did not focus requested workspace flow: %q", view)
	}
	for _, tool := range tools {
		if tool["inputSchema"] == nil {
			t.Fatalf("MCP tool %s has no input schema", tool["name"])
		}
	}
	toolNames := map[string]bool{}
	for _, tool := range tools {
		toolNames[tool["name"].(string)] = true
	}
	for _, name := range []string{"entry_points", "domain_subgraph", "prepare_workspace", "business_journeys", "upsert_business_journey", "open_business_journey"} {
		if !toolNames[name] {
			t.Fatalf("MCP tool %q was not advertised", name)
		}
	}
}

func TestDomainSubgraphMCPTool(t *testing.T) {
	repo := fixture(t)
	server := &Server{Repo: repo}
	res := toolCall(t, server, "domain_subgraph", map[string]any{
		"query": "signup auth session",
		"depth": 2,
	})
	if res.Error != nil {
		t.Fatalf("domain_subgraph tool call failed: %#v", res.Error)
	}
	content := structuredContent(t, res)
	if content["status"] != "observed" {
		t.Fatalf("expected observed status, got: %#v", content["status"])
	}
	data, ok := content["data"].(map[string]any)
	if !ok {
		t.Fatalf("expected data map, got: %#v", content["data"])
	}
	if data["topic"] != "signup auth session" {
		t.Fatalf("expected topic, got: %#v", data["topic"])
	}
	if journey, ok := data["journey"].(*subgraph.DomainJourney); ok {
		if journey == nil || journey.Title == "" {
			t.Fatalf("expected valid journey struct: %#v", journey)
		}
	} else if jMap, ok := data["journey"].(map[string]any); ok {
		if jMap["title"] == "" {
			t.Fatalf("expected journey title in map: %#v", jMap)
		}
	} else {
		t.Fatalf("expected journey in data, got: %#v", data["journey"])
	}
}

func TestBusinessJourneyMCPRegistersWithoutExposingRuntimeCredentials(t *testing.T) {
	repo := fixture(t)
	core, err := flowcore.StartFixture(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	defer core.Close(context.Background())
	document, err := core.Document(context.Background())
	if err != nil || len(document.Scenarios) != 1 {
		t.Fatalf("fixture scenario=%#v err=%v", document.Scenarios, err)
	}
	server := &Server{Repo: repo}
	created := toolCall(t, server, "upsert_business_journey", map[string]any{
		"id": "complete-signup", "title": "가입을 완료합니다", "outcome": "가입 상태가 준비됩니다",
		"segments": []any{map[string]any{"flow_id": document.Current.ID, "scenario_id": document.Scenarios[0].ID}},
	})
	if created.Error != nil {
		t.Fatalf("BusinessJourney registration failed: %#v", created.Error)
	}
	registered := structuredContent(t, created)
	data, _ := registered["data"].(map[string]any)
	if registered["status"] != "ready" || data["id"] != "complete-signup" {
		t.Fatalf("BusinessJourney was not registered: %#v", registered)
	}
	encoded, _ := json.Marshal(created)
	if strings.Contains(string(encoded), core.Token) {
		t.Fatal("MCP response exposed the runtime credential")
	}
	listed := toolCall(t, server, "business_journeys", map[string]any{})
	journeys, _ := structuredContent(t, listed)["data"].([]any)
	if len(journeys) != 1 || journeys[0].(map[string]any)["id"] != "complete-signup" {
		t.Fatalf("BusinessJourney list=%#v", journeys)
	}
	opened := toolCall(t, server, "open_business_journey", map[string]any{"id": "complete-signup"})
	openData, _ := structuredContent(t, opened)["data"].(map[string]any)
	if view, _ := openData["view_url"].(string); !strings.Contains(view, "?journey=complete-signup") {
		t.Fatalf("BusinessJourney view was not focused: %q", view)
	}
	invalid := toolCall(t, server, "upsert_business_journey", map[string]any{
		"id": "invalid-signup", "title": "근거 없는 여정",
		"segments": []any{map[string]any{"flow_id": document.Current.ID, "scenario_id": "missing-scenario"}},
	})
	invalidContent := structuredContent(t, invalid)
	if invalid.Error != nil || invalidContent["status"] != "unknown" {
		t.Fatalf("Core validation was bypassed: response=%#v content=%#v", invalid, invalidContent)
	}
}

func TestBusinessJourneyToolsWorkThroughTheMCPProtocol(t *testing.T) {
	repo := fixture(t)
	core, err := flowcore.StartFixture(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	defer core.Close(context.Background())
	document, err := core.Document(context.Background())
	if err != nil || len(document.Scenarios) != 1 {
		t.Fatalf("fixture scenario=%#v err=%v", document.Scenarios, err)
	}
	var input bytes.Buffer
	encoder := json.NewEncoder(&input)
	for _, request := range []map[string]any{
		{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{"protocolVersion": Modern}},
		{"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": map[string]any{"name": "upsert_business_journey", "arguments": map[string]any{"id": "complete-signup", "title": "가입을 완료합니다", "segments": []any{map[string]any{"flow_id": document.Current.ID, "scenario_id": document.Scenarios[0].ID}}}}},
		{"jsonrpc": "2.0", "id": 3, "method": "tools/call", "params": map[string]any{"name": "open_business_journey", "arguments": map[string]any{"id": "complete-signup"}}},
	} {
		if err := encoder.Encode(request); err != nil {
			t.Fatal(err)
		}
	}
	var output bytes.Buffer
	if err := (&Server{Repo: repo}).Serve(context.Background(), &input, &output); err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimSpace(output.Bytes()), []byte("\n"))
	if len(lines) != 3 {
		t.Fatalf("MCP response count=%d output=%s", len(lines), output.String())
	}
	var opened map[string]any
	if err := json.Unmarshal(lines[2], &opened); err != nil {
		t.Fatal(err)
	}
	structured := opened["result"].(map[string]any)["structuredContent"].(map[string]any)
	openData := structured["data"].(map[string]any)
	if view, _ := openData["view_url"].(string); !strings.Contains(view, "?journey=complete-signup") {
		t.Fatalf("MCP protocol did not open the registered journey: %q", view)
	}
	if strings.Contains(output.String(), core.Token) {
		t.Fatal("MCP protocol exposed the runtime credential")
	}
}

func TestPrepareWorkspaceProtectsTheRunningScopeAndEntryPointsRemainDiscoverable(t *testing.T) {
	repo := fixture(t)
	core, err := flowcore.StartFixture(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	defer core.Close(context.Background())
	server := &Server{Repo: repo, Discover: func(context.Context) entrypoint.Result {
		return entrypoint.Result{State: entrypoint.Unknown, Candidates: []entrypoint.EntryPoint{{FlowID: "system:push-token:lib/push.dart:PushRegistration:_registerToken", Alias: "푸시 토큰 갱신", Anchor: flowir.Anchor{Path: "lib/push.dart"}}}}
	}}
	entries := toolCall(t, server, "entry_points", map[string]any{})
	entryData, _ := structuredContent(t, entries)["data"].(map[string]any)
	points, _ := entryData["entry_points"].([]entrypoint.EntryPoint)
	if len(points) != 1 || !strings.HasPrefix(points[0].FlowID, "system:push-token:") {
		t.Fatalf("system entry point was not exposed to MCP: %#v", entryData)
	}
	prepared := toolCall(t, server, "prepare_workspace", map[string]any{"flow_ids": []any{"route:/signup"}})
	if prepared.Error != nil || structuredContent(t, prepared)["status"] != "ready" {
		t.Fatalf("matching workspace was not prepared: %#v", prepared)
	}
	mismatched := toolCall(t, server, "prepare_workspace", map[string]any{"flow_ids": []any{"route:/other"}})
	if mismatched.Error == nil || mismatched.Error.Message != "WORKSPACE_SCOPE_MISMATCH" {
		t.Fatalf("running workspace was silently replaced: %#v", mismatched)
	}
}

func TestPrepareWorkspaceStartsOnlyTheRequestedScope(t *testing.T) {
	repo := fixture(t)
	starts := 0
	server := &Server{Repo: repo, Start: func(ctx context.Context, selectors []string) (*flowcore.Core, *compiler.Problem, error) {
		starts++
		if len(selectors) != 1 || selectors[0] != "route:/signup" {
			t.Fatalf("prepare workspace selectors=%#v", selectors)
		}
		core, err := flowcore.StartFixture(ctx, repo)
		return core, nil, err
	}}
	defer server.closeStarted()
	prepared := toolCall(t, server, "prepare_workspace", map[string]any{"flow_ids": []any{"route:/signup"}})
	if starts != 1 || prepared.Error != nil || structuredContent(t, prepared)["status"] != "ready" {
		t.Fatalf("prepare_workspace did not start the exact requested scope: starts=%d response=%#v", starts, prepared)
	}
}

func toolCall(t *testing.T, server *Server, name string, arguments map[string]any) response {
	t.Helper()
	return server.call(context.Background(), request{JSONRPC: "2.0", ID: 1, Params: map[string]any{"name": name, "arguments": arguments}})
}

func structuredContent(t *testing.T, result response) map[string]any {
	t.Helper()
	if result.Error != nil {
		t.Fatalf("MCP error: %#v", result.Error)
	}
	content, _ := result.Result.(map[string]any)["structuredContent"].(map[string]any)
	if content == nil {
		t.Fatalf("missing structured content: %#v", result)
	}
	return content
}

func conversation(t *testing.T, repo, era, call string) []map[string]any {
	t.Helper()
	input := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"` + era + `"}}` + "\n"
	if call != "" {
		input += `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":` + call + `}` + "\n"
	}
	var out bytes.Buffer
	server := Server{Repo: repo}
	if err := server.Serve(context.Background(), strings.NewReader(input), &out); err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimSpace(out.Bytes()), []byte("\n"))
	result := make([]map[string]any, 0, len(lines))
	for _, line := range lines {
		var v map[string]any
		if err := json.Unmarshal(line, &v); err != nil {
			t.Fatal(err)
		}
		result = append(result, v)
	}
	return result
}
func fixture(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "lib"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "lib/signup.dart"), []byte("void signup() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	return repo
}
