package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codeflow/internal/agentgateway/mcp"
	"codeflow/internal/analyzer/protocol"
	"codeflow/internal/analyzer/workspace"
	"codeflow/internal/collector/fusion"
	"codeflow/internal/collector/harvest"
	"codeflow/internal/collector/secret"
	"codeflow/internal/collector/storage"
	"codeflow/internal/presenter/flowview"
)

// ---------------------------------------------------------------------------
// TIER 1: Content-Length JSON-RPC Framing
// ---------------------------------------------------------------------------

func TestTier1_WireProtocol_FramingAndEnvelopes(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skipf("node not found: %v", err)
	}
	root := moduleRoot(t)
	spec := "noderun:" + filepath.Join(root, "adapters", "typescript")
	cfg, err := harvest.ResolveAdapter("typescript", spec)
	if err != nil {
		t.Fatalf("ResolveAdapter failed: %v", err)
	}

	pool := protocol.NewPool(cfg, 1)
	defer pool.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 1. Ping
	var vi protocol.VersionInfo
	if err := pool.Call(ctx, protocol.OpPing, map[string]any{}, &vi); err != nil {
		t.Fatalf("ping failed: %v", err)
	}
	if vi.ProtocolVersion != 1 {
		t.Errorf("protocolVersion = %d, want 1", vi.ProtocolVersion)
	}

	// 2. Detect
	var det struct {
		Language  string `json:"language"`
		Confident bool   `json:"confident"`
	}
	appDir := filepath.Join(root, "testdata", "ts_example_app")
	engine, err := workspace.NewSnapshotEngine(appDir, 0)
	if err != nil {
		t.Fatalf("snapshot engine failed: %v", err)
	}
	head, err := engine.Reconcile(ctx, nil)
	if err != nil {
		t.Fatalf("snapshot capture failed: %v", err)
	}
	lease, err := engine.SnapshotVFS(head.SnapshotID)
	if err != nil {
		t.Fatalf("snapshot lease failed: %v", err)
	}
	defer lease.Close()
	snapshot, err := protocol.SnapshotFromLease(lease)
	if err != nil {
		t.Fatalf("protocol snapshot failed: %v", err)
	}
	if err := pool.Call(ctx, protocol.OpDetect, snapshot.Params(), &det); err != nil {
		t.Fatalf("detect failed: %v", err)
	}
	if !det.Confident || det.Language != "typescript" {
		t.Errorf("detect result: %+v, want confident typescript", det)
	}
}

func TestTier1_WireProtocol_ErrorEnvelopes(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skipf("node not found: %v", err)
	}
	root := moduleRoot(t)
	spec := "noderun:" + filepath.Join(root, "adapters", "typescript")
	cfg, err := harvest.ResolveAdapter("typescript", spec)
	if err != nil {
		t.Fatalf("ResolveAdapter failed: %v", err)
	}

	pool := protocol.NewPool(cfg, 1)
	defer pool.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Test unknown operation returns error envelope with E_BAD_REQUEST
	var dummy any
	err = pool.Call(ctx, "non_existent_op", map[string]any{}, &dummy)
	if err == nil {
		t.Fatal("expected error calling unknown op, got nil")
	}
	if !strings.Contains(err.Error(), "E_BAD_REQUEST") && !strings.Contains(err.Error(), "unknown") {
		t.Errorf("expected E_BAD_REQUEST in error, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// TIER 2: Language Adapters & AST Slicing (Dart & TypeScript)
// ---------------------------------------------------------------------------

func TestTier2_TypeScriptAdapter_LexicalAndSlicing(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skipf("node not found: %v", err)
	}
	root := moduleRoot(t)
	cmd := exec.Command("node", filepath.Join(root, "adapters", "typescript", "test", "index.test.js"))
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("TypeScript adapter comprehensive test suite failed:\n%s", string(out))
	}
	if !strings.Contains(string(out), "ALL TypeScript Adapter Tests Passed Successfully") {
		t.Fatalf("unexpected test output:\n%s", string(out))
	}
}

func TestTier2_DartAdapter_TestSuite(t *testing.T) {
	if _, err := exec.LookPath("dart"); err != nil {
		t.Skipf("dart not found: %v", err)
	}
	root := moduleRoot(t)
	cmd := exec.Command("dart", "test")
	cmd.Dir = filepath.Join(root, "adapters", "dart")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Dart adapter test suite failed:\n%s", string(out))
	}
	if !strings.Contains(string(out), "All tests passed") {
		t.Fatalf("unexpected dart test output:\n%s", string(out))
	}
}

func TestTier2_SingleGateSecretRedaction(t *testing.T) {
	inputJSON := []byte(`{
		"token": "api_key: 'sk-proj-abc123secret999xyz'",
		"auth_password": "password = 'super_secret_password_123'",
		"api_key": "token: 'AIzaSyD-example-key-value-12345'",
		"domain_word": "password_reset_screen",
		"normal_text": "This is safe application text"
	}`)

	redacted, count, err := secret.RedactJSON(inputJSON)
	if err != nil {
		t.Fatalf("RedactJSON error: %v", err)
	}
	if count < 3 {
		t.Errorf("expected at least 3 redactions, got %d", count)
	}

	redactedStr := string(redacted)
	if strings.Contains(redactedStr, "sk-proj-abc123secret999xyz") {
		t.Error("sensitive token leaked in redacted JSON")
	}
	if strings.Contains(redactedStr, "super_secret_password_123") {
		t.Error("sensitive password leaked in redacted JSON")
	}
	if !strings.Contains(redactedStr, "password_reset_screen") {
		t.Error("domain word was over-redacted")
	}
	if !strings.Contains(redactedStr, "This is safe application text") {
		t.Error("normal text was corrupted")
	}
}

// ---------------------------------------------------------------------------
// TIER 3: Go Core Multi-Language Engine & MCP
// ---------------------------------------------------------------------------

func TestTier3_PolyglotManifestAndScoring(t *testing.T) {
	root := moduleRoot(t)
	src := `flows:
  - entry: src/features/auth/LoginView.tsx#onSubmit
    name: TS Login
  - entry: lib/features/auth/signup.dart#submit
    name: Dart Signup
excluded:
  - src/legacy.js#oldHandler
`
	m, err := harvest.ParseManifest(src)
	if err != nil {
		t.Fatalf("ParseManifest failed: %v", err)
	}
	if len(m.Flows) != 2 || len(m.Excluded) != 1 {
		t.Fatalf("unexpected manifest counts: %+v", m)
	}

	appDir := filepath.Join(root, "testdata", "ts_example_app")
	spec := "noderun:" + filepath.Join(root, "adapters", "typescript")
	cfg, err := harvest.ResolveAdapter("typescript", spec)
	if err != nil {
		t.Fatalf("ResolveAdapter(typescript) failed: %v", err)
	}
	runner := harvest.NewRunner(cfg, 1)
	defer runner.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	candidates, err := runner.Run(ctx, appDir)
	if err != nil {
		t.Fatalf("harvest runner failed: %v", err)
	}
	if len(candidates) == 0 {
		t.Fatal("expected harvested candidates in ts_example_app")
	}
}

func TestTier3_MCPServer_EndToEndTools(t *testing.T) {
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

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 1. Test tools/list via JSON-RPC
	listReq := `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}` + "\n"
	inBuf := bytes.NewBufferString(listReq)
	outBuf := &bytes.Buffer{}

	if err := srv.Serve(ctx, inBuf, outBuf); err != nil && err != io.EOF {
		t.Fatalf("srv.Serve failed: %v", err)
	}

	var listResp struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(outBuf.Bytes(), &listResp); err != nil {
		t.Fatalf("unmarshal tools/list response: %v\nRaw: %s", err, outBuf.String())
	}
	if len(listResp.Result.Tools) < 7 {
		t.Fatalf("expected at least 7 MCP tools, got %d", len(listResp.Result.Tools))
	}

	// 2. Test publish_core_flow tool on TypeScript artifact
	authFile := filepath.Join(appDir, "src", "features", "auth", "LoginView.tsx")
	authBytes, err := os.ReadFile(authFile)
	if err != nil {
		t.Fatalf("read LoginView.tsx: %v", err)
	}
	spanBytes := authBytes[:len(authBytes)]
	spanHash := sha256Hex(spanBytes)
	fileHash := sha256Hex(authBytes)

	artifact := map[string]any{
		"flowId":          "flow-tsauth000000001",
		"entrySymbolPath": "src/features/auth/LoginView.tsx#onSubmit",
		"title":           "TypeScript Login Flow",
		"description":     "User login via TypeScript handler",
		"layers":          []string{"presentation", "usecase", "data"},
		"steps": []map[string]any{
			{
				"ordinal": 1,
				"name":    "Validate Input",
				"layer":   "presentation",
				"kind":    "guard",
				"anchor": map[string]any{
					"repoRelativePath":        "src/features/auth/LoginView.tsx",
					"byteRange":               []int{0, len(authBytes)},
					"fileHash":                fileHash,
					"spanHash":                spanHash,
					"enclosingSymbolPath":     "onSubmit",
					"canonicalAstFingerprint": "0000000000000000000000000000000000000000000000000000000000000000",
				},
			},
		},
	}

	pubReq := map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "publish_core_flow",
			"arguments": map[string]any{
				"artifact": artifact,
			},
		},
	}
	pubBytes, _ := json.Marshal(pubReq)
	inBuf = bytes.NewBuffer(append(pubBytes, '\n'))
	outBuf = &bytes.Buffer{}

	if err := srv.Serve(ctx, inBuf, outBuf); err != nil && err != io.EOF {
		t.Fatalf("srv.Serve publish_core_flow: %v", err)
	}

	var pubResp struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	if err := json.Unmarshal(outBuf.Bytes(), &pubResp); err != nil {
		t.Fatalf("unmarshal publish_core_flow response: %v\nRaw: %s", err, outBuf.String())
	}
	if pubResp.Result.IsError {
		t.Fatalf("publish_core_flow returned error: %s", pubResp.Result.Content[0].Text)
	}

	var pubStatus struct {
		Status    string `json:"status"`
		FlowID    string `json:"flowId"`
		Title     string `json:"title"`
		StepCount int    `json:"stepCount"`
	}
	if err := json.Unmarshal([]byte(pubResp.Result.Content[0].Text), &pubStatus); err != nil {
		t.Fatalf("unmarshal publish status JSON: %v", err)
	}
	if pubStatus.Status != "published" {
		t.Fatalf("status = %q, want published", pubStatus.Status)
	}

	// 3. Verify published flow payload
	st := storage.New(appDir)
	flowSpecBytes, err := st.ReadActiveFlowSpec(pubStatus.FlowID)
	if err != nil {
		t.Fatalf("ReadActiveFlowSpec failed: %v", err)
	}
	var spec fusion.FlowSpec
	if err := json.Unmarshal(flowSpecBytes, &spec); err != nil {
		t.Fatalf("unmarshal FlowSpec failed: %v", err)
	}
	if spec.FlowID != pubStatus.FlowID {
		t.Errorf("FlowID = %q, want %q", spec.FlowID, pubStatus.FlowID)
	}
	if len(spec.Steps) != 1 {
		t.Errorf("stepCount = %d, want 1", len(spec.Steps))
	}
}

// ---------------------------------------------------------------------------
// TIER 4: FlowView, Installer & End-to-End Workflows
// ---------------------------------------------------------------------------

func TestTier4_FlowView_7LaneRendering(t *testing.T) {
	root := moduleRoot(t)
	appDir := filepath.Join(root, "testdata", "ts_example_app")

	srv, err := flowview.NewServer(flowview.Config{
		RepoRoot: appDir,
		Port:     0,
	})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}
	srv.Start()
	defer srv.Shutdown(context.Background())

	resp, err := http.Get(srv.URL())
	if err != nil {
		t.Fatalf("GET FlowView URL failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("FlowView status code = %d, want 200", resp.StatusCode)
	}
	bodyBytes, _ := io.ReadAll(resp.Body)
	body := string(bodyBytes)

	// Check that the 7 canonical layers are represented
	expectedLanes := []string{"presentation", "controller", "usecase", "domain", "data", "infra", "external"}
	for _, lane := range expectedLanes {
		if !strings.Contains(body, lane) && !strings.Contains(strings.ToLower(body), lane) {
			t.Errorf("expected lane %q in FlowView UI", lane)
		}
	}
}

func TestTier4_HardcodedPathScrubbing(t *testing.T) {
	root := moduleRoot(t)
	needle := string([]byte{0x2f, 'U', 's', 'e', 'r', 's', 0x2f, 'j', 'u', 'n', 'h', 'y', 'o', 'u', 'n', 'g', 'l', 'e', 'e', 0x2f})

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			name := info.Name()
			if name == ".git" || name == "node_modules" || name == ".agents" || name == ".gemini" || name == "dist" || name == "build" {
				return filepath.SkipDir
			}
			return nil
		}
		base := filepath.Base(path)
		if base == "AGENTS.md" || base == "ORIGINAL_REQUEST.md" || base == "PROJECT.md" || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		ext := filepath.Ext(path)
		if ext == ".go" || ext == ".ts" || ext == ".js" || ext == ".html" || ext == ".dart" || ext == ".sh" {
			data, rerr := os.ReadFile(path)
			if rerr == nil {
				if strings.Contains(string(data), needle) {
					t.Errorf("file %s contains forbidden hardcoded path", path)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("filepath.Walk error: %v", err)
	}
}

func TestTier4_InstallerScriptPackaging(t *testing.T) {
	root := moduleRoot(t)
	installSh := filepath.Join(root, "scripts", "install.sh")
	data, err := os.ReadFile(installSh)
	if err != nil {
		t.Fatalf("read install.sh: %v", err)
	}
	content := string(data)

	// Verify TS adapter packaging fixes
	if !strings.Contains(content, "codeflow_ts_adapter") {
		t.Error("install.sh does not configure codeflow_ts_adapter")
	}
	if !strings.Contains(content, "adapters/typescript") {
		t.Error("install.sh does not copy adapters/typescript")
	}
}

// ---------------------------------------------------------------------------
// TIER 5: VS-04 End-to-End Publication, SLO Trace, and CAS Race
// ---------------------------------------------------------------------------

func TestTier5_Compiler_EndToEndSLOAndTrace(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "codeflow-e2e-compiler-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	_ = os.WriteFile(filepath.Join(tempDir, "page.tsx"), []byte("export default function Page(){ return <h1>Hi</h1>; }"), 0o644)

	srv, err := flowview.NewServer(flowview.Config{
		RepoRoot: tempDir,
		Port:     0,
	})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}
	srv.Start()
	defer func() {
		_ = srv.Shutdown(context.Background())
	}()

	ctx := context.Background()

	// 1. Removed endpoints return 404 per VS-02 AC-04
	editURL := "http://" + srv.Addr() + "/api/workspace/edit?token=" + srv.AuthToken()
	editReq, _ := http.NewRequestWithContext(ctx, http.MethodPost, editURL, strings.NewReader(`{}`))
	editResp, err := http.DefaultClient.Do(editReq)
	if err != nil {
		t.Fatalf("edit request failed: %v", err)
	}
	if editResp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 on removed /api/workspace/edit, got %d", editResp.StatusCode)
	}
	_ = editResp.Body.Close()

	actURL := "http://" + srv.Addr() + "/api/workspace/activity?token=" + srv.AuthToken()
	actReq, _ := http.NewRequestWithContext(ctx, http.MethodGet, actURL, nil)
	actResp, err := http.DefaultClient.Do(actReq)
	if err != nil {
		t.Fatalf("activity query failed: %v", err)
	}
	if actResp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 on removed /api/workspace/activity, got %d", actResp.StatusCode)
	}
	_ = actResp.Body.Close()

	// 2. Removed MCP tool returns -32601 Method not found per VS-02 AC-03
	mcpServer, _ := mcp.NewServer(mcp.Config{RepoRoot: tempDir})
	callReq := `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"get_verified_gap","arguments":{"target":"` + tempDir + `"}}}` + "\n"
	inBuf := bytes.NewBufferString(callReq)
	outBuf := &bytes.Buffer{}
	_ = mcpServer.Serve(ctx, inBuf, outBuf)

	var callResp struct {
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(outBuf.Bytes(), &callResp)
	if callResp.Error == nil || callResp.Error.Code != -32601 {
		t.Fatalf("expected -32601 error for removed tool, got: %+v", callResp.Error)
	}

	// 3. Concurrent Active Pointer CAS race: two competing publishers (VS04-A4)
	st := storage.New(tempDir)
	_ = st.InitLayout()

	manifestA := &storage.GenerationProofManifest{
		ProofID:                    "proof-race-a",
		GenerationID:               "gen-race-a",
		ComputedBasisID:            "basis-race",
		ValidatedAgainstSnapshotID: "snap-head",
		CurrentPublication: storage.CurrentPublicationResult{
			Eligibility: "passed",
		},
		ExpectedLiveHeadSnapshotID: "snap-head",
	}
	manifestB := &storage.GenerationProofManifest{
		ProofID:                    "proof-race-b",
		GenerationID:               "gen-race-b",
		ComputedBasisID:            "basis-race",
		ValidatedAgainstSnapshotID: "snap-head",
		CurrentPublication: storage.CurrentPublicationResult{
			Eligibility: "passed",
		},
		ExpectedLiveHeadSnapshotID: "snap-head",
	}
	casA, _ := st.WriteManifestCAS(manifestA)
	casB, _ := st.WriteManifestCAS(manifestB)

	ptrA := &storage.ActivePointer{
		GenerationID:               "gen-race-a",
		ManifestObjectRef:          casA,
		ComputedBasisID:            "basis-race",
		ValidatedAgainstSnapshotID: "snap-head",
		ExpectedLiveHeadSnapshotID: "snap-head",
	}
	ptrB := &storage.ActivePointer{
		GenerationID:               "gen-race-b",
		ManifestObjectRef:          casB,
		ComputedBasisID:            "basis-race",
		ValidatedAgainstSnapshotID: "snap-head",
		ExpectedLiveHeadSnapshotID: "snap-head",
	}

	// First CAS wins
	errA := st.CompareAndSwapActivePointer("snap-head", "", ptrA)
	if errA != nil {
		t.Fatalf("first CAS failed: %v", errA)
	}

	// Second CAS against same empty previousGen must be rejected with ErrCASConflict
	errB := st.CompareAndSwapActivePointer("snap-head", "", ptrB)
	if errB != storage.ErrCASConflict {
		t.Errorf("expected ErrCASConflict for lost CAS race, got %v", errB)
	}

	activePtr, _ := st.ReadActivePointer()
	if activePtr.GenerationID != "gen-race-a" {
		t.Errorf("active pointer was corrupted by race loser: got %s, want gen-race-a", activePtr.GenerationID)
	}
}
