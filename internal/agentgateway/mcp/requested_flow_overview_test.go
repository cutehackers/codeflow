package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"codeflow/internal/presenter/flowview"
)

func TestRequestedFlowViewReusesActiveCoordinatorWithoutChangingEngine(t *testing.T) {
	root := t.TempDir()
	fv, err := flowview.NewServer(flowview.Config{RepoRoot: root, Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	fv.Start()
	defer fv.Shutdown(context.Background())
	srv, err := NewServer(Config{RepoRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	base, token, err := srv.requestedFlowViewEndpoint(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if base != "http://"+fv.Addr() || token != fv.AuthToken() {
		t.Fatal("immutable view operation did not reuse browser coordinator")
	}
	if _, exists := srv.liveServers.Load(root); exists {
		t.Fatal("immutable view operation initialized another snapshot engine")
	}
	if _, err := fv.SnapshotEngine().ReconcileIfChanged(context.Background(), nil); err != nil {
		t.Fatalf("browser snapshot engine cannot analyze after MCP operation: %v", err)
	}
}

func TestRequestedFlowViewRejectsExternalAndRedirectedCoordinators(t *testing.T) {
	for _, test := range []struct {
		name     string
		url      string
		redirect bool
	}{
		{name: "external", url: "https://example.com/?token=private"},
		{name: "redirect", redirect: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			endpoint := test.url
			if test.redirect {
				target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					t.Error("coordinator redirect forwarded authentication")
					w.WriteHeader(200)
				}))
				defer target.Close()
				source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusFound) }))
				defer source.Close()
				endpoint = source.URL
			}
			if err := flowview.WriteLiveCoordinator(root, flowview.LiveCoordinatorRecord{URL: endpoint, Token: "private", RepoRoot: root, PID: os.Getpid()}); err != nil {
				t.Fatal(err)
			}
			srv, err := NewServer(Config{RepoRoot: root})
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := srv.requestedFlowViewEndpoint(context.Background(), root); err == nil {
				t.Fatal("unsafe coordinator accepted")
			}
		})
	}
}

func TestRequestedFlowCoordinatorConcurrentCreationUsesOneEngine(t *testing.T) {
	root := t.TempDir()
	srv, err := NewServer(Config{RepoRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	type result struct {
		server *flowview.Server
		err    error
	}
	results := make(chan result, 8)
	gate := make(chan struct{})
	for range 8 {
		go func() { <-gate; server, err := srv.getLiveCoordinator(root); results <- result{server, err} }()
	}
	close(gate)
	var first *flowview.Server
	for range 8 {
		result := <-results
		if result.err != nil {
			t.Fatal(result.err)
		}
		if first == nil {
			first = result.server
		}
		if result.server != first {
			t.Fatal("concurrent requests initialized different coordinators and snapshot engines")
		}
	}
	if _, err := first.SnapshotEngine().ReconcileIfChanged(context.Background(), nil); err != nil {
		t.Fatalf("single coordinator engine cannot analyze: %v", err)
	}
}

func TestRequestedFlowCoordinatorProcessFallback(t *testing.T) {
	if os.Getenv("CODEFLOW_VIEW_COORDINATOR_PROCESS") != "fallback" {
		return
	}
	root := os.Getenv("CODEFLOW_VIEW_COORDINATOR_ROOT")
	name := os.Getenv("CODEFLOW_VIEW_COORDINATOR_NAME")
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(root, "fallback-start")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("fallback start timed out")
		}
		time.Sleep(10 * time.Millisecond)
	}
	srv, err := NewServer(Config{RepoRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	base, token, err := srv.requestedFlowViewEndpoint(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	_, owns := srv.liveServers.Load(canonicalRoot)
	data, err := json.Marshal(map[string]any{"base": base, "token": token, "owns": owns})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "fallback-result-"+name), data, 0600); err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(root, "fallback-stop")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("fallback stop timed out")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestRequestedFlowCoordinatorFallbackSerializesAcrossProcesses(t *testing.T) {
	root := t.TempDir()
	type processResult struct {
		output string
		err    error
	}
	outputs := make(chan processResult, 2)
	for _, name := range []string{"one", "two"} {
		cmd := exec.Command(os.Args[0], "-test.run=^TestRequestedFlowCoordinatorProcessFallback$")
		cmd.Env = append(os.Environ(), "CODEFLOW_VIEW_COORDINATOR_PROCESS=fallback", "CODEFLOW_VIEW_COORDINATOR_ROOT="+root, "CODEFLOW_VIEW_COORDINATOR_NAME="+name)
		go func() { output, err := cmd.CombinedOutput(); outputs <- processResult{string(output), err} }()
	}
	defer func() { _ = os.WriteFile(filepath.Join(root, "fallback-stop"), nil, 0600) }()
	if err := os.WriteFile(filepath.Join(root, "fallback-start"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	results := []struct {
		Base  string `json:"base"`
		Token string `json:"token"`
		Owns  bool   `json:"owns"`
	}{{}, {}}
	for i, name := range []string{"one", "two"} {
		deadline := time.Now().Add(10 * time.Second)
		for {
			data, err := os.ReadFile(filepath.Join(root, "fallback-result-"+name))
			if err == nil {
				if err := json.Unmarshal(data, &results[i]); err != nil {
					t.Fatal(err)
				}
				break
			}
			select {
			case output := <-outputs:
				t.Fatalf("fallback subprocess exited before publication: %v %s", output.err, output.output)
			default:
			}
			if time.Now().After(deadline) {
				t.Fatal("fallback publication timed out")
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	if results[0].Base != results[1].Base || results[0].Token != results[1].Token || results[0].Owns == results[1].Owns {
		t.Fatalf("fallback created competing coordinator engines: %+v", results)
	}
	record, err := flowview.ReadLiveCoordinator(root)
	if err != nil || record == nil || fmt.Sprintf("http://%s", mustCoordinatorHost(t, record.URL)) != results[0].Base {
		t.Fatalf("fallback registration mismatch: %+v %v", record, err)
	}
	if err := os.WriteFile(filepath.Join(root, "fallback-stop"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		output := <-outputs
		if output.err != nil {
			t.Fatalf("fallback process: %v %s", output.err, output.output)
		}
	}
}

func mustCoordinatorHost(t *testing.T, address string) string {
	t.Helper()
	parsed, err := url.Parse(address)
	if err != nil {
		t.Fatal(err)
	}
	return parsed.Host
}

func TestRequestedFlowCoordinatorCanceledStartupKeepsWorkspaceInactive(t *testing.T) {
	root := t.TempDir()
	unlock, err := flowview.LockLiveCoordinatorStartup(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	srv, err := NewServer(Config{RepoRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, _, err := srv.requestedFlowViewEndpoint(ctx, root); err == nil {
		t.Fatal("canceled startup created a coordinator")
	}
	count := 0
	srv.liveServers.Range(func(_, _ any) bool { count++; return true })
	if count != 0 {
		t.Fatal("canceled startup initialized a snapshot engine")
	}
	record, err := flowview.ReadLiveCoordinator(root)
	if err != nil || record != nil {
		t.Fatalf("canceled startup published a coordinator: %+v %v", record, err)
	}
}

func TestRequestedFlowOverviewMCPPreservesOptionalCategories(t *testing.T) {
	for _, categorized := range []bool{false, true} {
		t.Run(fmt.Sprintf("categorized=%t", categorized), func(t *testing.T) {
			root := t.TempDir()
			categories := []any{map[string]any{"categoryId": "checkout", "title": "Checkout", "segmentIds": []any{"checkout-fee"}}}
			endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/views" {
					return
				}
				if r.URL.Path != "/api/view/overview" {
					t.Errorf("unexpected path: %s", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
					return
				}
				var proposal map[string]any
				if err := json.NewDecoder(r.Body).Decode(&proposal); err != nil {
					t.Error(err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				supplied, present := proposal["categories"]
				if present != categorized || (categorized && !reflect.DeepEqual(supplied, categories)) {
					t.Errorf("MCP changed optional category proposal: %+v", proposal)
				}
				overview := map[string]any{"schemaVersion": 1}
				if categorized {
					overview["schemaVersion"] = 2
					overview["categories"] = supplied
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"viewId": "overview-view", "requestedFlowOverview": overview})
			}))
			defer endpoint.Close()
			if err := flowview.WriteLiveCoordinator(root, flowview.LiveCoordinatorRecord{URL: endpoint.URL, RepoRoot: root, Token: "local-token"}); err != nil {
				t.Fatal(err)
			}
			srv, err := NewServer(Config{RepoRoot: root})
			if err != nil {
				t.Fatal(err)
			}
			defer srv.Close()
			args := map[string]any{"viewId": "analysis-view", "segments": []any{}, "relations": []any{}}
			if categorized {
				args["categories"] = categories
			}
			result, err := srv.processRequestedFlowOverview(context.Background(), args)
			if err != nil {
				t.Fatal(err)
			}
			overview := result.(map[string]any)["requestedFlowOverview"].(map[string]any)
			if categorized && !reflect.DeepEqual(overview["categories"], categories) {
				t.Fatalf("MCP omitted category references: %+v", overview)
			}
			if !categorized && overview["categories"] != nil {
				t.Fatalf("legacy response acquired categories: %+v", overview)
			}
		})
	}
}

func TestRequestedFlowOverviewMCPCategorySchemaIsOptionalAndExplicit(t *testing.T) {
	schema := requestedFlowOverviewInputSchema(map[string]any{"type": "string"})
	for _, required := range schema["required"].([]string) {
		if required == "categories" {
			t.Fatal("legacy proposal now requires categories")
		}
	}
	categories := schema["properties"].(map[string]any)["categories"].(map[string]any)
	category := categories["items"].(map[string]any)
	if categories["minItems"] != 1 || category["additionalProperties"] != false {
		t.Fatal("category schema allows empty or unsupported category fields")
	}
	if !reflect.DeepEqual(category["required"], []string{"categoryId", "title", "segmentIds"}) {
		t.Fatalf("category contract differs: %+v", category)
	}
}

func TestRequestedFlowViewLargeResponse(t *testing.T) {
	for _, operation := range []string{"open", "curate"} {
		t.Run(operation, func(t *testing.T) {
			root := t.TempDir()
			viewID := strings.Repeat("a", 64)
			overview := map[string]any{"schemaVersion": float64(2), "categories": []any{map[string]any{"categoryId": "camera"}}}
			endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/views" {
					_ = json.NewEncoder(w).Encode(map[string]any{"views": []any{}})
					return
				}
				if r.Header.Get("X-CodeFlow-Token") != "local-token" || (r.URL.Path != "/api/view" && r.URL.Path != "/api/view/overview") {
					t.Errorf("unexpected request: %s", r.URL.Path)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"viewId": viewID, "requestedFlowOverview": overview, "sourceFiles": map[string]any{"camera.ts": []any{map[string]any{"lineNumber": 1, "text": "// " + strings.Repeat("x", (8<<20)+4096)}}}})
			}))
			defer endpoint.Close()
			if err := flowview.WriteLiveCoordinator(root, flowview.LiveCoordinatorRecord{URL: endpoint.URL, RepoRoot: root, Token: "local-token"}); err != nil {
				t.Fatal(err)
			}
			srv, err := NewServer(Config{RepoRoot: root})
			if err != nil {
				t.Fatal(err)
			}
			defer srv.Close()
			var result any
			if operation == "open" {
				result, err = srv.openRequestedFlowView(context.Background(), root, viewID)
			} else {
				result, err = srv.processRequestedFlowOverview(context.Background(), map[string]any{"target": root, "viewId": viewID, "segments": []any{}, "relations": []any{}})
			}
			if err != nil {
				t.Fatalf("large View %s failed: %v", operation, err)
			}
			response := result.(map[string]any)
			wantStatus := "ready"
			if operation == "open" {
				wantStatus = "intermediate" // The mocked overview has no verified analysis.
			}
			if response["viewId"] != viewID || response["status"] != wantStatus {
				t.Fatalf("large View identity was lost: %+v", response)
			}
			if operation == "curate" && !reflect.DeepEqual(response["requestedFlowOverview"], overview) {
				t.Fatalf("large View overview was lost: %+v", response["requestedFlowOverview"])
			}
		})
	}
}

func TestRequestedFlowViewWithoutOverviewCannotReportCompletion(t *testing.T) {
	for _, legacyPath := range []bool{false, true} {
		t.Run(fmt.Sprintf("legacyPath=%t", legacyPath), func(t *testing.T) {
			root := t.TempDir()
			viewID := strings.Repeat("b", 64)
			endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/views" {
					return
				}
				view := map[string]any{"viewId": viewID, "presentationStatus": "ready"}
				if legacyPath {
					view["codeReadingPath"] = map[string]any{"nodes": []any{}}
				}
				if err := json.NewEncoder(w).Encode(view); err != nil {
					t.Error(err)
				}
			}))
			defer endpoint.Close()
			if err := flowview.WriteLiveCoordinator(root, flowview.LiveCoordinatorRecord{URL: endpoint.URL, RepoRoot: root, Token: "local-token"}); err != nil {
				t.Fatal(err)
			}
			srv, err := NewServer(Config{RepoRoot: root})
			if err != nil {
				t.Fatal(err)
			}
			defer srv.Close()
			result, err := srv.executeTool(context.Background(), "open_review", map[string]any{"target": root, "viewId": viewID})
			if err != nil {
				t.Fatal(err)
			}
			response := result.(map[string]any)
			if response["status"] != "intermediate" || response["nextAction"] == nil {
				t.Fatalf("uncurated view reported completion: %+v", response)
			}
		})
	}
}
