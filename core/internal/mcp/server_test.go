package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
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
	for _, name := range []string{"current", "diff", "step", "unknowns", "refresh", "open"} {
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
