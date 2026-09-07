package flowview

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codeflow/internal/fusion"
	"codeflow/internal/semantic"
	"codeflow/internal/slicing"
	"codeflow/internal/testfixture"
)

func TestFlowViewWorkspaceEndpoints(t *testing.T) {
	root := copyFixtureWithoutCodeflow(t, "nextjs-app-fixture")
	if _, err := os.Stat(filepath.Join(root, ".codeflow")); !os.IsNotExist(err) {
		t.Fatalf("fixture copy is not pristine: .codeflow stat error=%v", err)
	}

	srv, err := NewServer(Config{
		RepoRoot: root,
		Port:     0,
	})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}
	defer func() { _ = srv.Shutdown(context.Background()) }()

	// 1. GET /api/workspace/activity
	reqAct := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/workspace/activity?token="+srv.AuthToken(), nil)
	recAct := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(recAct, reqAct)

	if recAct.Code != http.StatusOK {
		t.Fatalf("expected 200 for activity, got %d: %s", recAct.Code, recAct.Body.String())
	}
	var actDoc map[string]any
	if err := json.Unmarshal(recAct.Body.Bytes(), &actDoc); err != nil {
		t.Fatalf("unmarshal activity response: %v", err)
	}
	if actDoc["activity"] != "idle" {
		t.Errorf("expected initial activity idle, got %v", actDoc["activity"])
	}

	// 2. POST /api/workspace/edit
	editBody, _ := json.Marshal(map[string]any{
		"path":            "app/page.tsx",
		"content":         "export default function Test() { return <p>Test</p>; }",
		"documentVersion": 1,
		"source":          "agent_transaction",
	})
	reqEdit := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/workspace/edit?token="+srv.AuthToken(), bytes.NewReader(editBody))
	recEdit := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(recEdit, reqEdit)

	if recEdit.Code != http.StatusOK {
		t.Fatalf("expected 200 for edit, got %d: %s", recEdit.Code, recEdit.Body.String())
	}
	var editDoc map[string]any
	if err := json.Unmarshal(recEdit.Body.Bytes(), &editDoc); err != nil {
		t.Fatalf("unmarshal edit response: %v", err)
	}
	if _, ok := editDoc["revision"]; !ok {
		t.Error("missing revision in edit response")
	}
	if _, ok := editDoc["snapshot"]; !ok {
		t.Error("missing snapshot in edit response")
	}

	// 3. GET /api/workspace/activity after edit -> activity should be editing
	reqAct2 := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/workspace/activity?token="+srv.AuthToken(), nil)
	recAct2 := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(recAct2, reqAct2)

	var actDoc2 map[string]any
	_ = json.Unmarshal(recAct2.Body.Bytes(), &actDoc2)
	if actDoc2["activity"] != "editing" {
		t.Errorf("expected activity editing after edit, got %v", actDoc2["activity"])
	}

	// 4. GET /api/workspace/proof
	reqProof := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/workspace/proof?token="+srv.AuthToken(), nil)
	recProof := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(recProof, reqProof)
	if recProof.Code != http.StatusOK {
		t.Fatalf("expected 200 for proof, got %d: %s", recProof.Code, recProof.Body.String())
	}

	// 5. GET /api/workspace/stream with lastEventId triggering snapshot_sync.
	// Use a real network server and cancel the request before closing it. A
	// ResponseRecorder read racing a long-lived SSE handler is not a valid test
	// of the stream and fails under -race.
	streamServer := httptest.NewServer(srv.httpServer.Handler)
	defer streamServer.Close()
	streamCtx, cancelStream := context.WithCancel(context.Background())
	reqStream, err := http.NewRequestWithContext(streamCtx, http.MethodGet, streamServer.URL+"/api/workspace/stream?token="+srv.AuthToken()+"&lastEventId=ev-unknown", nil)
	if err != nil {
		t.Fatal(err)
	}
	respStream, err := streamServer.Client().Do(reqStream)
	if err != nil {
		cancelStream()
		t.Fatalf("open workspace stream: %v", err)
	}
	reader := bufio.NewScanner(respStream.Body)
	foundSync := false
	for reader.Scan() {
		if strings.Contains(reader.Text(), "snapshot_sync") {
			foundSync = true
			break
		}
	}
	cancelStream()
	_ = respStream.Body.Close()
	if !foundSync {
		t.Fatalf("workspace stream did not emit snapshot_sync")
	}
}

// copyFixtureWithoutCodeflow gives mutating FlowView endpoint tests an
// isolated repository. The checked-in fixture must remain source-only because
// SnapshotEngine and publication tests create runtime state under .codeflow.
func copyFixtureWithoutCodeflow(t *testing.T, fixture string) string {
	t.Helper()
	sourceRoot, err := filepath.Abs(filepath.Join("../../test/fixtures", fixture))
	if err != nil {
		t.Fatal(err)
	}
	destination := t.TempDir()
	err = filepath.WalkDir(sourceRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(sourceRoot, path)
		if err != nil {
			return err
		}
		if rel == ".codeflow" || strings.HasPrefix(rel, ".codeflow"+string(filepath.Separator)) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if rel == "." {
			return nil
		}
		target := filepath.Join(destination, rel)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		mode := info.Mode().Perm()
		if mode == 0 {
			mode = 0o644
		}
		return os.WriteFile(target, data, mode)
	})
	if err != nil {
		t.Fatalf("copy fixture %s: %v", fixture, err)
	}
	return destination
}

func TestFlowViewReviewEndpoint(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "codeflow-test-review-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	srv, err := NewServer(Config{
		RepoRoot: tmpDir,
		Port:     0,
	})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	// 1. Missing precondition (VS05-A2)
	reqMissing := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/task/review?token="+srv.AuthToken(), nil)
	recMissing := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(recMissing, reqMissing)
	if recMissing.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing params, got %d", recMissing.Code)
	}

	// 2. Seed maps
	baseMap := &semantic.SemanticMapIR{
		MapID:           "map-base",
		GenerationID:    "gen-base",
		ComputedBasisID: "basis-base",
		SchemaID:        "https://codeflow.local/schemas/semantic-map-ir.schema.json",
		SchemaVersion:   1,
		Task:            semantic.MapTaskContext{TaskID: "task-flowview-review", IntentRevision: 1, Mode: "review"},
		Basis:           semantic.MapBasisContext{RepositoryID: tmpDir, WorkspaceEpoch: 1, ComputedWorkspaceSnapshotID: "snap-base", SnapshotTreeID: "tree-base", ComputedBasisID: "basis-base"},
		Steps: []semantic.SemanticStep{
			{
				StepID:             "step-login",
				StructuralIdentity: "src/auth.ts\x00Auth.login\x00Auth.login\x00call",
				Name:               "로그인",
				TechnicalName:      "Auth.login",
				Anchor:             slicing.Anchor{RepoRelativePath: "src/auth.ts", EnclosingSymbolPath: "Auth.login"},
				Rules:              []string{"AC-1"},
				EvidenceRefs:       []string{"ev-1"},
			},
		},
	}

	currMap := &semantic.SemanticMapIR{
		MapID:           "map-curr",
		GenerationID:    "gen-curr",
		ComputedBasisID: "basis-curr",
		SchemaID:        "https://codeflow.local/schemas/semantic-map-ir.schema.json",
		SchemaVersion:   1,
		Task:            semantic.MapTaskContext{TaskID: "task-flowview-review", IntentRevision: 1, Mode: "review"},
		Basis:           semantic.MapBasisContext{RepositoryID: tmpDir, WorkspaceEpoch: 1, ComputedWorkspaceSnapshotID: "snap-curr", SnapshotTreeID: "tree-curr", ComputedBasisID: "basis-curr"},
		Evidence: []semantic.SemanticEvidence{
			{EvidenceID: "ev-1", ValidationStatus: "verified"},
			{EvidenceID: "ev-2", ValidationStatus: "verified"},
		},
		Steps: []semantic.SemanticStep{
			{
				StepID:             "step-login",
				StructuralIdentity: "src/auth.ts\x00Auth.login\x00Auth.login\x00call",
				Name:               "로그인",
				TechnicalName:      "Auth.login",
				Anchor:             slicing.Anchor{RepoRelativePath: "src/auth.ts", EnclosingSymbolPath: "Auth.login"},
				Rules:              []string{"AC-1"},
				EvidenceRefs:       []string{"ev-1"},
			},
			{
				StepID:             "step-mfa",
				StructuralIdentity: "src/auth.ts\x00Auth.mfa\x00Auth.mfa\x00call",
				Name:               "2차 인증",
				TechnicalName:      "Auth.mfa",
				Anchor:             slicing.Anchor{RepoRelativePath: "src/auth.ts", EnclosingSymbolPath: "Auth.mfa"},
				Rules:              []string{"AC-2"},
				EvidenceRefs:       []string{"ev-2"},
			},
		},
	}

	incompatMap := &semantic.SemanticMapIR{
		MapID:           "map-incompat",
		GenerationID:    "gen-incompat",
		ComputedBasisID: "basis-incompat",
		SchemaID:        "https://codeflow.local/schemas/semantic-map-ir.schema.json",
		SchemaVersion:   1,
		Task:            semantic.MapTaskContext{TaskID: "task-flowview-review", IntentRevision: 1, Mode: "review"},
		Basis:           semantic.MapBasisContext{RepositoryID: tmpDir, WorkspaceEpoch: 999, ComputedWorkspaceSnapshotID: "snap-incompat", SnapshotTreeID: "tree-incompat", ComputedBasisID: "basis-incompat"}, // Mismatch!
	}

	srv.mu.Lock()
	srv.mapCache["base"] = baseMap
	srv.mapCache["curr"] = currMap
	srv.mapCache["incompat"] = incompatMap
	srv.mu.Unlock()

	// 3. Incomparable basis (VS05-A1, A2)
	reqIncompat := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/task/review?baseline=base&current=incompat&token="+srv.AuthToken(), nil)
	recIncompat := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(recIncompat, reqIncompat)
	if recIncompat.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for epoch mismatch, got %d: %s", recIncompat.Code, recIncompat.Body.String())
	}

	// 4. Successful review query (VS05-A3, A5, A8)
	reqOK := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/task/review?baseline=base&current=curr&token="+srv.AuthToken(), nil)
	recOK := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(recOK, reqOK)
	if recOK.Code != http.StatusOK {
		t.Fatalf("expected 200 for review query, got %d: %s", recOK.Code, recOK.Body.String())
	}

	var resp struct {
		SemanticDelta        semantic.SemanticDeltaIR        `json:"semanticDelta"`
		RequirementAlignment []semantic.RequirementAlignment `json:"requirementAlignment"`
		ChangePulse          []map[string]any                `json:"changePulse"`
	}
	if err := json.Unmarshal(recOK.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal review response: %v", err)
	}

	if len(resp.SemanticDelta.Changes) != 1 {
		t.Errorf("expected 1 change (added step), got %d", len(resp.SemanticDelta.Changes))
	}
	if len(resp.RequirementAlignment) < 1 {
		t.Errorf("expected requirement alignments, got none")
	}
	if len(resp.ChangePulse) != 1 {
		t.Errorf("expected 1 change pulse item, got %d", len(resp.ChangePulse))
	}
}

func TestFlowViewImpactEndpoint(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "codeflow-test-impact-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	srv, err := NewServer(Config{
		RepoRoot: tmpDir,
		Port:     0,
	})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	// 1. Missing precondition (VS06-A2)
	reqMissing := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/task/impact?token="+srv.AuthToken(), nil)
	recMissing := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(recMissing, reqMissing)
	if recMissing.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing params, got %d", recMissing.Code)
	}

	// 2. A current query cannot inherit authority from an arbitrary cache entry.
	activeMap := &semantic.SemanticMapIR{
		SchemaID:                   semantic.SemanticMapSchemaID,
		MapID:                      "map-checkout",
		GenerationID:               "gen-100",
		ComputedBasisID:            "basis-100",
		ValidatedAgainstSnapshotID: "snapshot-100",
		SchemaVersion:              2,
		Basis:                      semantic.MapBasisContext{RepositoryID: "repo-100", WorktreeID: "worktree-100", WorkspaceEpoch: 1, ComputedWorkspaceSnapshotID: "snapshot-100", ComputedBasisID: "basis-100", SnapshotTreeID: "tree-100"},
		Task:                       semantic.MapTaskContext{TaskID: "task-100", IntentRevision: 1, IntentStatus: "parsed", Mode: "feature"},
		Coverage: &semantic.CoverageBoundary{
			IncludedSourceRoots: []string{"src"},
			ExcludedReasons:     []string{},
		},
		Steps: []semantic.SemanticStep{
			{
				StepID:             "step-checkout",
				StructuralIdentity: "checkout|src/order.go|OrderService.checkout|action",
				Ordinal:            1,
				Name:               "체크아웃",
				TechnicalName:      "OrderService.checkout",
				Anchor:             slicing.Anchor{RepoRelativePath: "src/order.go", ByteRange: [2]int{0, 10}, FileHash: "hash-100", SpanHash: "span-100", EnclosingSymbolPath: "OrderService.checkout", CanonicalAstFingerprint: "ast-100"},
			},
		},
		Edges:    []semantic.SemanticEdge{},
		Unknowns: []fusion.Unknown{},
		Evidence: []semantic.SemanticEvidence{},
	}

	srv.mu.Lock()
	srv.mapCache[activeMap.GenerationID] = activeMap
	srv.mapCache[activeMap.ComputedBasisID] = activeMap
	srv.mu.Unlock()

	currentURL := "http://127.0.0.1/api/task/impact?symbolId=OrderService.checkout&computedBasisId=basis-100&generationId=gen-100&freshness=current&maxDepth=3&maxNodes=50&relationKinds=calls&token=" + srv.AuthToken()
	reqCurrent := httptest.NewRequest(http.MethodGet, currentURL, nil)
	recCurrent := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(recCurrent, reqCurrent)
	if recCurrent.Code != http.StatusConflict {
		t.Fatalf("expected 409 without validated current proof, got %d: %s", recCurrent.Code, recCurrent.Body.String())
	}

	// 3. An exact historical map remains explicit and capability-incomplete.
	historicalURL := "http://127.0.0.1/api/task/impact?symbolId=OrderService.checkout&computedBasisId=basis-100&generationId=gen-100&freshness=historical&maxDepth=3&maxNodes=50&relationKinds=calls&token=" + srv.AuthToken()
	reqOK := httptest.NewRequest(http.MethodGet, historicalURL, nil)
	recOK := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(recOK, reqOK)
	if recOK.Code != http.StatusOK {
		t.Fatalf("expected 200 for impact query, got %d: %s", recOK.Code, recOK.Body.String())
	}

	var graph semantic.ChangeImpactGraph
	if err := json.Unmarshal(recOK.Body.Bytes(), &graph); err != nil {
		t.Fatalf("unmarshal impact response: %v", err)
	}
	if graph.Target.SymbolID != "OrderService.checkout" {
		t.Errorf("expected symbolId OrderService.checkout, got %s", graph.Target.SymbolID)
	}
	if graph.Freshness != "historical" {
		t.Errorf("expected historical freshness, got %s", graph.Freshness)
	}
	if !graph.IndirectImpact.Bounded {
		t.Error("expected bounded indirect impact")
	}
	if graph.IndirectImpact.CompletedWithinCoverage || graph.UnknownCount == 0 {
		t.Fatalf("historical map without capability proof must remain incomplete: %+v", graph)
	}
}

func TestFlowViewImpactTriggerUsesLoadedIdentityAndExplicitBounds(t *testing.T) {
	start := strings.Index(IndexHTML, "function impactQueryFromLoadedView")
	if start < 0 {
		t.Fatal("embedded impact query builder is missing")
	}
	end := strings.Index(IndexHTML[start:], "function renderChangeImpact")
	if end < 0 {
		t.Fatal("embedded impact query builder is missing")
	}
	impactScript := IndexHTML[start : start+end]

	for _, required := range []string{
		"query.set('symbolId',symbol)",
		"query.set('computedBasisId',basis)",
		"query.set('generationId',generation)",
		"query.set('freshness'",
		"query.set('maxDepth',String(IMPACT_MAX_DEPTH))",
		"query.set('maxNodes',String(IMPACT_MAX_NODES))",
		"IMPACT_RELATION_KINDS.forEach",
	} {
		if !strings.Contains(impactScript, required) {
			t.Errorf("impact trigger does not expose explicit %q", required)
		}
	}
	if strings.Contains(impactScript, "HomePage.handleQuickCheckout") {
		t.Error("impact trigger must not fabricate a fallback target")
	}
	if strings.Contains(impactScript, "selectedStep ?") {
		t.Error("impact trigger must not reference the old undefined selectedStep variable")
	}
}

func TestFlowViewFailureEndpoints(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "codeflow-test-failure-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	srv, err := NewServer(Config{
		RepoRoot: tmpDir,
		Port:     0,
	})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	// 1. Missing precondition on /api/task/debug
	reqMissingDebug := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/task/debug?token="+srv.AuthToken(), nil)
	recMissingDebug := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(recMissingDebug, reqMissingDebug)
	if recMissingDebug.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing debug params, got %d", recMissingDebug.Code)
	}

	// 2. Legacy debug fields cannot select a graph or proof implicitly.
	reqLegacyDebug := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/task/debug?error=DbConnectionTimeout&token="+srv.AuthToken(), nil)
	recLegacyDebug := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(recLegacyDebug, reqLegacyDebug)
	if recLegacyDebug.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for legacy debug query without v2 identity, got %d: %s", recLegacyDebug.Code, recLegacyDebug.Body.String())
	}

	// 3. A trace identifier alone cannot authorize runtime evidence.
	reqLegacyInc := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/task/incident?traceId=trace-inc-10&token="+srv.AuthToken(), nil)
	recLegacyInc := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(recLegacyInc, reqLegacyInc)
	if recLegacyInc.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for incident query without v2 scope and observation identity, got %d: %s", recLegacyInc.Code, recLegacyInc.Body.String())
	}
}

func TestFlowViewApprovalEndpoints(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "codeflow-test-appr-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	srv, err := NewServer(Config{
		RepoRoot: tmpDir,
		Port:     0,
	})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	// 1. GET /api/semantic/evidence-pack
	reqEv := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/semantic/evidence-pack?symbolPath=PaymentService.process&token="+srv.AuthToken(), nil)
	recEv := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(recEv, reqEv)
	if recEv.Code != http.StatusOK {
		t.Fatalf("expected 200 for evidence-pack, got %d: %s", recEv.Code, recEv.Body.String())
	}
	var pack semantic.EvidencePack
	if err := json.Unmarshal(recEv.Body.Bytes(), &pack); err != nil {
		t.Fatalf("unmarshal evidence pack: %v", err)
	}
	if len(pack.Items) == 0 {
		t.Error("expected items in evidence pack")
	}

	// 2. POST /api/semantic/approve must not synthesize a proposal from IDs.
	apprBody := `{"commandId":"command-checkout","proposalId":"prop-checkout","evidencePackId":"pack-not-persisted","computedBasisId":"basis-checkout","generationId":"generation-checkout","intentRevision":1,"decision":"approve","idempotencyKey":"key-checkout","expectedApprovalVersion":0,"expectedState":"none"}`
	reqAppr := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/semantic/approve?token="+srv.AuthToken(), strings.NewReader(apprBody))
	recAppr := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(recAppr, reqAppr)
	if recAppr.Code != http.StatusNotFound || !strings.Contains(recAppr.Body.String(), `"code":"approval_unavailable"`) {
		t.Fatalf("expected safe unavailable for an unpersisted approval pair, got %d: %s", recAppr.Code, recAppr.Body.String())
	}
}

func TestFlowViewOnboardingEndpoint(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "codeflow-test-onb-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	srv, err := NewServer(Config{
		RepoRoot: tmpDir,
		Port:     0,
	})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	// A repository id alone must not select a synthetic workspace/domain set.
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/task/onboarding?repositoryId=shop-core&token="+srv.AuthToken(), nil)
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for onboarding without exact basis, got %d: %s", rec.Code, rec.Body.String())
	}
	var missing map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &missing); err != nil {
		t.Fatalf("unmarshal missing-precondition response: %v", err)
	}
	if missing["code"] != "missing_precondition" {
		t.Fatalf("expected missing_precondition, got %v", missing)
	}

	// Install one explicit historical semantic map in the read-only cache. The
	// endpoint may expose this exact basis, but it must retain the same map and
	// generation identity for level 1 and level 2.
	entry := "src/orders/controller.ts#Checkout.submit"
	entrySymbol := "Checkout.submit"
	srv.mapCache = map[string]*semantic.SemanticMapIR{
		"generation-historical": {
			SchemaID: semantic.SemanticMapSchemaID, SchemaVersion: semantic.SemanticSchemaVersion,
			MapID: "map-historical", GenerationID: "generation-historical", ComputedBasisID: "basis-historical",
			ValidatedAgainstSnapshotID: "snapshot-historical", Freshness: "historical", Authority: "historical",
			Basis:    semantic.MapBasisContext{RepositoryID: "shop-core", ComputedWorkspaceSnapshotID: "snapshot-historical", ComputedBasisID: "basis-historical", SnapshotTreeID: "tree-historical"},
			Steps:    []semantic.SemanticStep{{StepID: "step-submit", StructuralIdentity: entry, Anchor: slicing.Anchor{RepoRelativePath: "src/orders/controller.ts", EnclosingSymbolPath: entrySymbol, ByteRange: [2]int{0, 1}}, Name: "submit", EvidenceRefs: []string{"evidence-entry"}}},
			Evidence: []semantic.SemanticEvidence{{EvidenceID: "evidence-entry", Kind: "source", SourceAuthority: "code", ComputedBasisID: "basis-historical", SnapshotID: "snapshot-historical", ValidationStatus: "verified", RedactionStatus: "clean", Anchor: slicing.Anchor{RepoRelativePath: "src/orders/controller.ts", EnclosingSymbolPath: entrySymbol, ByteRange: [2]int{0, 1}}}},
			Coverage: &semantic.CoverageBoundary{IncludedSourceRoots: []string{"src"}},
		},
	}
	url := "http://127.0.0.1/api/task/onboarding?repositoryId=shop-core&freshness=historical&computedBasisId=basis-historical&generationId=generation-historical&validatedAgainstSnapshotId=snapshot-historical&token=" + srv.AuthToken()
	req = httptest.NewRequest(http.MethodGet, url, nil)
	rec = httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for explicit historical onboarding, got %d: %s", rec.Code, rec.Body.String())
	}
	var ov OnboardingOverview
	if err := json.Unmarshal(rec.Body.Bytes(), &ov); err != nil {
		t.Fatalf("unmarshal onboarding overview: %v", err)
	}
	if ov.RepositoryID != "shop-core" || ov.GenerationID != "generation-historical" || ov.ComputedBasisID != "basis-historical" || ov.ValidatedAgainstSnapshotID != "snapshot-historical" || ov.Freshness != "historical" {
		t.Fatalf("historical basis identity was not preserved: %+v", ov)
	}
	if len(ov.Domains) != 1 || ov.Domains[0].Name != "Orders" || ov.Domains[0].EpistemicState != "candidate" {
		t.Fatalf("expected evidence-backed inferred domain, got %+v", ov.Domains)
	}
	if strings.Contains(rec.Body.String(), "AuthController") || strings.Contains(rec.Body.String(), "Payment") || strings.Contains(rec.Body.String(), "Catalog") {
		t.Fatal("onboarding response contains a fixed production domain")
	}

	catURL := url + "&level=2&domain=" + ov.Domains[0].DomainID
	req = httptest.NewRequest(http.MethodGet, catURL, nil)
	rec = httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for level-2 historical onboarding, got %d: %s", rec.Code, rec.Body.String())
	}
	var cat OnboardingCatalog
	if err := json.Unmarshal(rec.Body.Bytes(), &cat); err != nil {
		t.Fatalf("unmarshal onboarding catalog: %v", err)
	}
	if cat.GenerationID != ov.GenerationID || cat.ComputedBasisID != ov.ComputedBasisID || cat.ValidatedAgainstSnapshotID != ov.ValidatedAgainstSnapshotID || cat.Freshness != ov.Freshness || len(cat.Flows) != 1 || cat.Flows[0].GroundedMapID != "map-historical" || cat.Flows[0].EntrySymbol == "" {
		t.Fatalf("level-2 catalog did not preserve canonical generation: %+v", cat)
	}
}

func TestFlowViewReleaseCapabilityEndpoint(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "codeflow-test-rel-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	srv, err := NewServer(Config{
		RepoRoot: tmpDir,
		Port:     0,
	})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/release/capability?token="+srv.AuthToken(), strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for release capability, got %d: %s", rec.Code, rec.Body.String())
	}

	var res semantic.ReleaseEvaluation
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("unmarshal release capability response: %v", err)
	}

	if res.BenchmarkReport.Status != "incomplete" || res.BenchmarkReport.ReleaseReady {
		t.Fatalf("missing evidence must return incomplete: %+v", res.BenchmarkReport)
	}
	if len(res.BenchmarkReport.Metrics) != 0 || len(res.CapabilityMatrix.Capabilities) != 0 {
		t.Fatalf("missing evidence must not generate metrics or capability claims: %+v", res)
	}

	explicitInput, err := json.Marshal(testfixture.VS10ReleaseEvaluationInput())
	if err != nil {
		t.Fatal(err)
	}
	req = httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/release/capability?token="+srv.AuthToken(), bytes.NewReader(explicitInput))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("explicit evidence request failed with %d: %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.BenchmarkReport.Status != "incomplete" || res.BenchmarkReport.ReleaseReady || res.CapabilityMatrix.ReleaseReady {
		t.Fatalf("default REST server trusted caller decision labels: %+v", res)
	}

	configured, err := NewServer(Config{RepoRoot: tmpDir, Port: 0, ReleaseThresholdDecisions: testfixture.VS10ReleaseThresholdDecisions()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = configured.Shutdown(context.Background()) }()
	req = httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/release/capability?token="+configured.AuthToken(), bytes.NewReader(explicitInput))
	rec = httptest.NewRecorder()
	configured.httpServer.Handler.ServeHTTP(rec, req)
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if !res.BenchmarkReport.ReleaseReady || !res.CapabilityMatrix.ReleaseReady {
		t.Fatalf("trusted configured decisions did not cross the REST boundary: %+v", res)
	}

	tampered := testfixture.VS10ReleaseEvaluationInput()
	tampered.Thresholds.Thresholds[0].DecisionRef = "decision:unapproved"
	tamperedInput, err := json.Marshal(tampered)
	if err != nil {
		t.Fatal(err)
	}
	req = httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/release/capability?token="+configured.AuthToken(), bytes.NewReader(tamperedInput))
	rec = httptest.NewRecorder()
	configured.httpServer.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("tampered evidence must return an inspectable result, got %d: %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.BenchmarkReport.Status != "incomplete" || res.BenchmarkReport.ReleaseReady {
		t.Fatalf("REST accepted evidence whose content did not match artifactRef: %+v", res.BenchmarkReport)
	}
}
