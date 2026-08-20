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

	flowcore "codeflow/core/internal/core"
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
}
func conversation(t *testing.T, repo, era, call string) []map[string]any {
	t.Helper()
	input := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"` + era + `"}}` + "\n"
	if call != "" {
		input += `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":` + call + `}` + "\n"
	}
	var out bytes.Buffer
	if err := (Server{Repo: repo}).Serve(context.Background(), strings.NewReader(input), &out); err != nil {
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
