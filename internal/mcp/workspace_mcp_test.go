package mcp

import (
	"context"
	"os"
	"testing"

	"codeflow/internal/semantic"
	"codeflow/internal/storage"
)

func TestVS03_MCPWorkspaceTools(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "codeflow-mcp-ws-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	srv, err := NewServer(Config{
		RepoRoot:     tempDir,
		RequireToken: false,
	})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	ctx := context.Background()

	// 1. Initial activity query
	res1, err := srv.executeTool(ctx, "get_workspace_activity", map[string]any{
		"target": tempDir,
	})
	if err != nil {
		t.Fatalf("get_workspace_activity failed: %v", err)
	}
	act1, ok := res1.(map[string]any)
	if !ok {
		t.Fatalf("unexpected type for activity response: %T", res1)
	}
	if act1["activity"] != "idle" {
		t.Errorf("expected initial activity idle, got %v", act1["activity"])
	}

	// 2. Submit versioned edit
	resEdit, err := srv.executeTool(ctx, "submit_versioned_edit", map[string]any{
		"target":          tempDir,
		"path":            "src/app.ts",
		"content":         "export const ready = true;",
		"documentVersion": float64(1),
		"source":          "agent_transaction",
	})
	if err != nil {
		t.Fatalf("submit_versioned_edit failed: %v", err)
	}
	editDoc, ok := resEdit.(map[string]any)
	if !ok {
		t.Fatalf("unexpected type for edit response: %T", resEdit)
	}
	if _, ok := editDoc["revision"]; !ok {
		t.Error("missing revision in submit_versioned_edit response")
	}
	if _, ok := editDoc["snapshot"]; !ok {
		t.Error("missing snapshot in submit_versioned_edit response")
	}

	// 3. Query activity again -> should be "editing"
	res2, err := srv.executeTool(ctx, "get_workspace_activity", map[string]any{
		"target": tempDir,
	})
	if err != nil {
		t.Fatalf("get_workspace_activity 2 failed: %v", err)
	}
	act2 := res2.(map[string]any)
	if act2["activity"] != "editing" {
		t.Errorf("expected activity editing, got %v", act2["activity"])
	}
	if act2["currentSnapshotId"] == "" {
		t.Error("expected non-empty currentSnapshotId after edit")
	}
}

func TestVS04_MCPProofAndGapTools(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "codeflow-mcp-vs04-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	srv, err := NewServer(Config{
		RepoRoot:     tempDir,
		RequireToken: false,
	})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	ctx := context.Background()

	// 1. Initial state
	resProof, err := srv.executeTool(ctx, "get_generation_proof", map[string]any{"target": tempDir})
	if err != nil {
		t.Fatalf("get_generation_proof error: %v", err)
	}
	proofMap := resProof.(map[string]any)
	if proofMap["pointer"] != nil {
		t.Errorf("expected nil initial pointer, got %v", proofMap["pointer"])
	}

	resGap, err := srv.executeTool(ctx, "get_verified_gap", map[string]any{"target": tempDir})
	if err != nil {
		t.Fatalf("get_verified_gap error: %v", err)
	}
	gapMap := resGap.(map[string]any)
	if gapMap["status"] != "no_generation_published" {
		t.Errorf("expected no_generation_published, got %v", gapMap["status"])
	}

	// 2. Publish a generation directly to storage
	st, _ := srv.getStorage(tempDir)
	_ = st.InitLayout()
	manifest := &storage.GenerationProofManifest{
		ProofID:                    "proof-mcp-1",
		GenerationID:               "gen-mcp-1",
		ComputedBasisID:            "basis-1",
		ValidatedAgainstSnapshotID: "snap-1",
		CurrentPublication: storage.CurrentPublicationResult{
			Eligibility: "passed",
		},
		ExpectedLiveHeadSnapshotID: "snap-1",
	}
	casRef, _ := st.WriteManifestCAS(manifest)
	ptr := &storage.ActivePointer{
		GenerationID:               "gen-mcp-1",
		ManifestObjectRef:          casRef,
		ComputedBasisID:            "basis-1",
		ValidatedAgainstSnapshotID: "snap-1",
		ExpectedLiveHeadSnapshotID: "snap-1",
	}
	_ = st.CompareAndSwapActivePointer("snap-1", "", ptr)

	// 3. Query get_generation_proof again -> non-nil
	resProof2, err := srv.executeTool(ctx, "get_generation_proof", map[string]any{"target": tempDir})
	if err != nil {
		t.Fatalf("get_generation_proof 2 error: %v", err)
	}
	proofMap2 := resProof2.(map[string]any)
	if proofMap2["pointer"] == nil || proofMap2["manifest"] == nil {
		t.Fatalf("expected non-nil pointer and manifest, got %+v", proofMap2)
	}

	// 4. Submit edit so snapshot changes -> get_verified_gap should report last_verified
	_, _ = srv.executeTool(ctx, "submit_versioned_edit", map[string]any{
		"target":          tempDir,
		"path":            "src/file.ts",
		"content":         "console.log('hi');",
		"documentVersion": float64(1),
	})

	resGap2, err := srv.executeTool(ctx, "get_verified_gap", map[string]any{"target": tempDir})
	if err != nil {
		t.Fatalf("get_verified_gap 2 error: %v", err)
	}
	gapMap2 := resGap2.(map[string]any)
	if gapMap2["freshness"] != "last_verified" {
		t.Errorf("expected last_verified freshness, got %v", gapMap2["freshness"])
	}
	if gapMap2["lastVerifiedGenId"] != "gen-mcp-1" {
		t.Errorf("expected lastVerifiedGenId gen-mcp-1, got %v", gapMap2["lastVerifiedGenId"])
	}
}

func TestVS05_MCPSemanticDeltaAndAlignmentTools(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "codeflow-mcp-vs05-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	srv, err := NewServer(Config{
		RepoRoot:     tempDir,
		RequireToken: false,
	})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	ctx := context.Background()

	// 1. get_semantic_delta missing arguments
	resDeltaMissing, err := srv.executeTool(ctx, "get_semantic_delta", map[string]any{"target": tempDir})
	if err != nil {
		t.Fatalf("get_semantic_delta error: %v", err)
	}
	deltaMissingMap, ok := resDeltaMissing.(map[string]any)
	if !ok || deltaMissingMap["code"] != "missing_precondition" {
		t.Errorf("expected missing_precondition error for missing arguments, got %+v", resDeltaMissing)
	}

	// 2. get_semantic_delta valid
	resDelta, err := srv.executeTool(ctx, "get_semantic_delta", map[string]any{
		"target":   tempDir,
		"baseline": "gen-1",
		"current":  "gen-2",
	})
	if err != nil {
		t.Fatalf("get_semantic_delta valid call failed: %v", err)
	}
	deltaDoc, ok := resDelta.(*semantic.SemanticDeltaIR)
	if !ok {
		t.Fatalf("expected *semantic.SemanticDeltaIR, got %T: %+v", resDelta, resDelta)
	}
	if deltaDoc.Status != "comparable" {
		t.Errorf("expected delta status comparable, got %s", deltaDoc.Status)
	}

	// 3. get_requirement_alignment
	resAlign, err := srv.executeTool(ctx, "get_requirement_alignment", map[string]any{"target": tempDir})
	if err != nil {
		t.Fatalf("get_requirement_alignment failed: %v", err)
	}
	alignDoc, ok := resAlign.(map[string]any)
	if !ok {
		t.Fatalf("unexpected type for requirement alignment: %T", resAlign)
	}
	if alignDoc["computedBasisId"] == "" {
		t.Error("expected non-empty computedBasisId")
	}
}

func TestVS06_MCPChangeImpact(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "codeflow-mcp-impact-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	srv, err := NewServer(Config{
		RepoRoot:     tempDir,
		RequireToken: false,
	})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	ctx := context.Background()

	// 1. Missing precondition (VS06-A2)
	resMissing, err := srv.executeTool(ctx, "get_change_impact", map[string]any{
		"target": tempDir,
	})
	if err != nil {
		t.Fatalf("unexpected execution error for missing precondition: %v", err)
	}
	missingDoc, ok := resMissing.(map[string]any)
	if !ok || missingDoc["code"] != "missing_precondition" {
		t.Fatalf("expected missing_precondition code, got: %+v", resMissing)
	}

	// 2. Valid symbolId query (VS06-A1, A3, A4, A7)
	resImpact, err := srv.executeTool(ctx, "get_change_impact", map[string]any{
		"target":   tempDir,
		"symbolId": "PaymentService.process",
	})
	if err != nil {
		t.Fatalf("get_change_impact failed: %v", err)
	}
	impactGraph, ok := resImpact.(*semantic.ChangeImpactGraph)
	if !ok {
		t.Fatalf("expected *semantic.ChangeImpactGraph, got %T: %+v", resImpact, resImpact)
	}
	if impactGraph.Target.SymbolID != "PaymentService.process" {
		t.Errorf("expected symbolId PaymentService.process, got %s", impactGraph.Target.SymbolID)
	}
	if impactGraph.Freshness != "current" {
		t.Errorf("expected freshness current, got %s", impactGraph.Freshness)
	}
	if !impactGraph.IndirectImpact.Bounded {
		t.Errorf("expected indirect impact to be bounded")
	}

	// 3. query_task_view with mode=impact
	resQuery, err := srv.executeTool(ctx, "query_task_view", map[string]any{
		"target": tempDir,
		"query": map[string]any{
			"schemaId":      "https://codeflow.local/schemas/task-view-query.schema.json",
			"schemaVersion": 1,
			"mode":          "impact",
			"impact": map[string]any{
				"symbolId": "PaymentService.process",
			},
		},
	})
	if err != nil {
		t.Fatalf("query_task_view mode=impact failed: %v", err)
	}
	qGraph, ok := resQuery.(*semantic.ChangeImpactGraph)
	if !ok {
		t.Fatalf("expected *semantic.ChangeImpactGraph from query_task_view, got %T", resQuery)
	}
	if qGraph.Target.SymbolID != "PaymentService.process" {
		t.Errorf("expected symbolId PaymentService.process, got %s", qGraph.Target.SymbolID)
	}
}

func TestVS07_MCPInvestigateFailure(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "codeflow-mcp-failure-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	srv, err := NewServer(Config{
		RepoRoot:     tempDir,
		RequireToken: false,
	})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	ctx := context.Background()

	// 1. Missing precondition (VS07-A3)
	resMissing, err := srv.executeTool(ctx, "investigate_failure", map[string]any{
		"target": tempDir,
		"mode":   "debug",
	})
	if err != nil {
		t.Fatalf("unexpected error for missing precondition: %v", err)
	}
	missingDoc, ok := resMissing.(map[string]any)
	if !ok || missingDoc["code"] != "missing_precondition" {
		t.Fatalf("expected missing_precondition, got: %+v", resMissing)
	}

	// 2. Valid debug call (VS07-A1, A4)
	resDebug, err := srv.executeTool(ctx, "investigate_failure", map[string]any{
		"target": tempDir,
		"mode":   "debug",
		"error":  "NullPointerException",
	})
	if err != nil {
		t.Fatalf("investigate_failure debug failed: %v", err)
	}
	debugTrace, ok := resDebug.(*semantic.FailurePathTrace)
	if !ok {
		t.Fatalf("expected *semantic.FailurePathTrace, got %T", resDebug)
	}
	if debugTrace.Mode != "debug" {
		t.Errorf("expected mode debug, got %s", debugTrace.Mode)
	}
	if len(debugTrace.Nodes) == 0 {
		t.Error("expected nodes in failure trace")
	}

	// 3. Valid incident call (VS07-A2, A5)
	resInc, err := srv.executeTool(ctx, "investigate_failure", map[string]any{
		"target":  tempDir,
		"mode":    "incident",
		"traceId": "trace-tx-100",
	})
	if err != nil {
		t.Fatalf("investigate_failure incident failed: %v", err)
	}
	incTrace, ok := resInc.(*semantic.FailurePathTrace)
	if !ok {
		t.Fatalf("expected *semantic.FailurePathTrace, got %T", resInc)
	}
	if incTrace.Mode != "incident" {
		t.Errorf("expected mode incident, got %s", incTrace.Mode)
	}
	if len(incTrace.Timeline) == 0 {
		t.Error("expected timeline in incident trace")
	}

	// 4. query_task_view with mode=debug
	resQuery, err := srv.executeTool(ctx, "query_task_view", map[string]any{
		"target": tempDir,
		"query": map[string]any{
			"schemaId":      "https://codeflow.local/schemas/task-view-query.schema.json",
			"schemaVersion": 1,
			"mode":          "debug",
			"debug": map[string]any{
				"error": "NullPointerException",
			},
		},
	})
	if err != nil {
		t.Fatalf("query_task_view mode=debug failed: %v", err)
	}
	qTrace, ok := resQuery.(*semantic.FailurePathTrace)
	if !ok {
		t.Fatalf("expected *semantic.FailurePathTrace from query_task_view, got %T", resQuery)
	}
	if qTrace.Mode != "debug" {
		t.Errorf("expected mode debug, got %s", qTrace.Mode)
	}
}

func TestVS08_MCPApprovalAndEvidence(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "codeflow-mcp-appr-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	srv, err := NewServer(Config{
		RepoRoot:     tempDir,
		RequireToken: false,
	})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	ctx := context.Background()

	// 1. get_evidence_pack missing symbolPath (VS08-A6)
	resMissingEv, err := srv.executeTool(ctx, "get_evidence_pack", map[string]any{
		"target": tempDir,
	})
	if err != nil {
		t.Fatalf("unexpected execution error: %v", err)
	}
	missingEvDoc, ok := resMissingEv.(map[string]any)
	if !ok || missingEvDoc["code"] != "missing_precondition" {
		t.Fatalf("expected missing_precondition, got: %+v", resMissingEv)
	}

	// 2. get_evidence_pack valid (VS08-A5)
	resEv, err := srv.executeTool(ctx, "get_evidence_pack", map[string]any{
		"target":     tempDir,
		"symbolPath": "OrderService.checkout",
	})
	if err != nil {
		t.Fatalf("get_evidence_pack failed: %v", err)
	}
	pack, ok := resEv.(*semantic.EvidencePack)
	if !ok {
		t.Fatalf("expected *semantic.EvidencePack, got %T", resEv)
	}
	if len(pack.Items) == 0 {
		t.Error("expected evidence items in pack")
	}

	// 3. submit_semantic_approval missing approver (VS08-A6)
	resMissingAppr, err := srv.executeTool(ctx, "submit_semantic_approval", map[string]any{
		"target":     tempDir,
		"proposalId": "prop-1",
	})
	if err != nil {
		t.Fatalf("unexpected execution error: %v", err)
	}
	missingApprDoc, ok := resMissingAppr.(map[string]any)
	if !ok || missingApprDoc["code"] != "missing_precondition" {
		t.Fatalf("expected missing_precondition, got: %+v", resMissingAppr)
	}

	// 4. submit_semantic_approval valid (VS08-A2, A3)
	resAppr, err := srv.executeTool(ctx, "submit_semantic_approval", map[string]any{
		"target":     tempDir,
		"proposalId": "prop-1",
		"decision":   "approved",
		"approver":   "team-lead@company.corp",
	})
	if err != nil {
		t.Fatalf("submit_semantic_approval failed: %v", err)
	}
	appr, ok := resAppr.(*semantic.SemanticApproval)
	if !ok {
		t.Fatalf("expected *semantic.SemanticApproval, got %T", resAppr)
	}
	if appr.Decision != "approved" {
		t.Errorf("expected decision approved, got %s", appr.Decision)
	}
	if appr.Approver != "team-lead@company.corp" {
		t.Errorf("expected approver team-lead@company.corp, got %s", appr.Approver)
	}
	if appr.Freshness != "current" {
		t.Errorf("expected freshness current, got %s", appr.Freshness)
	}
}

func TestVS09_MCPOnboarding(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "codeflow-mcp-onb-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	srv, err := NewServer(Config{
		RepoRoot:     tempDir,
		RequireToken: false,
	})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	ctx := context.Background()

	// 1. Level 1: System / Domain Overview (VS09-A1, A2)
	resL1, err := srv.executeTool(ctx, "explore_project_domains", map[string]any{
		"target":       tempDir,
		"repositoryId": "shop-app",
		"level":        float64(1),
	})
	if err != nil {
		t.Fatalf("explore_project_domains L1 failed: %v", err)
	}
	overview, ok := resL1.(*semantic.DomainOverview)
	if !ok {
		t.Fatalf("expected *semantic.DomainOverview, got %T", resL1)
	}
	if overview.RepositoryID != "shop-app" {
		t.Errorf("expected repositoryId shop-app, got %s", overview.RepositoryID)
	}
	if len(overview.Domains) == 0 {
		t.Error("expected domains in overview")
	}

	// 2. Level 2: Representative Flow Catalog (VS09-A2, A3)
	resL2, err := srv.executeTool(ctx, "explore_project_domains", map[string]any{
		"target": tempDir,
		"level":  float64(2),
		"domain": "Order",
	})
	if err != nil {
		t.Fatalf("explore_project_domains L2 failed: %v", err)
	}
	catalog, ok := resL2.(*semantic.RepresentativeFlowCatalog)
	if !ok {
		t.Fatalf("expected *semantic.RepresentativeFlowCatalog, got %T", resL2)
	}
	if catalog.DomainID != "Order" {
		t.Errorf("expected domain Order, got %s", catalog.DomainID)
	}
	if len(catalog.Flows) == 0 {
		t.Error("expected flows in catalog")
	}

	// 3. query_task_view mode=onboarding
	resQuery, err := srv.executeTool(ctx, "query_task_view", map[string]any{
		"target": tempDir,
		"query": map[string]any{
			"schemaId":      "https://codeflow.local/schemas/task-view-query.schema.json",
			"schemaVersion": 1,
			"mode":          "onboarding",
			"onboarding": map[string]any{
				"repositoryId": "shop-app",
			},
		},
	})
	if err != nil {
		t.Fatalf("query_task_view mode=onboarding failed: %v", err)
	}
	qOverview, ok := resQuery.(*semantic.DomainOverview)
	if !ok {
		t.Fatalf("expected *semantic.DomainOverview from query_task_view, got %T", resQuery)
	}
	if qOverview.RepositoryID != "shop-app" {
		t.Errorf("expected repositoryId shop-app, got %s", qOverview.RepositoryID)
	}
}

func TestVS10_MCPReleaseCapability(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "codeflow-mcp-rel-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	srv, err := NewServer(Config{
		RepoRoot:     tempDir,
		RequireToken: false,
	})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	ctx := context.Background()

	// 1. validate_release_capability execution (VS10-A1, A2, A3)
	res, err := srv.executeTool(ctx, "validate_release_capability", map[string]any{
		"target":        tempDir,
		"targetVersion": "v0.9.0-rc1",
		"modelId":       "qwen2.5-coder-7b",
	})
	if err != nil {
		t.Fatalf("validate_release_capability failed: %v", err)
	}

	resMap, ok := res.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any, got %T", res)
	}

	rep, ok := resMap["benchmarkReport"].(*semantic.ReleaseBenchmarkReport)
	if !ok {
		t.Fatalf("expected *semantic.ReleaseBenchmarkReport, got %T", resMap["benchmarkReport"])
	}
	if !rep.ReleaseReady {
		t.Errorf("expected releaseReady true for rc1")
	}

	slm, ok := resMap["slmCapability"].(*semantic.SLMCapabilityState)
	if !ok {
		t.Fatalf("expected *semantic.SLMCapabilityState, got %T", resMap["slmCapability"])
	}
	if slm.ModelID != "qwen2.5-coder-7b" {
		t.Errorf("expected modelId qwen2.5-coder-7b, got %s", slm.ModelID)
	}
}







