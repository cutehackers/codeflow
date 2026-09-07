package mcp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"codeflow/internal/semantic"
	"codeflow/internal/slicing"
	"codeflow/internal/storage"
	"codeflow/internal/testfixture"
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

	// 3. A legacy, incomplete publication cannot be exposed as current proof.
	// The public MCP boundary must use the strict reader instead of returning
	// pointer and manifest fields that merely claim eligibility.
	if _, err := srv.executeTool(ctx, "get_generation_proof", map[string]any{"target": tempDir}); err == nil || !strings.Contains(err.Error(), "read validated current proof") {
		t.Fatalf("incomplete proof must fail closed at public MCP boundary, got %v", err)
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
	baseMap := explicitComparableSemanticMap(tempDir, "gen-1", "basis-1", "snap-1", "tree-1", 1, 1)
	currMap := explicitComparableSemanticMap(tempDir, "gen-2", "basis-2", "snap-2", "tree-2", 1, 2)
	srv.rememberSemanticMap(tempDir, baseMap)
	srv.rememberSemanticMap(tempDir, currMap)

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

func explicitComparableSemanticMap(repositoryID, generationID, basisID, snapshotID, treeID string, epoch, steps int) *semantic.SemanticMapIR {
	items := make([]semantic.SemanticStep, 0, steps)
	for i := 1; i <= steps; i++ {
		items = append(items, semantic.SemanticStep{
			StepID: fmt.Sprintf("step-%d", i), StructuralIdentity: fmt.Sprintf("src/file.ts\x00Service.step%d\x00Service.step%d\x00call", i, i),
			Ordinal: i, Name: fmt.Sprintf("step %d", i), TechnicalName: fmt.Sprintf("Service.step%d", i), Kind: "call",
			Anchor: slicing.Anchor{RepoRelativePath: "src/file.ts", EnclosingSymbolPath: fmt.Sprintf("Service.step%d", i)},
		})
	}
	return &semantic.SemanticMapIR{
		SchemaID: "https://codeflow.local/schemas/semantic-map-ir.schema.json", SchemaVersion: 1,
		MapID: "map-" + generationID, GenerationID: generationID, ComputedBasisID: basisID, ValidatedAgainstSnapshotID: snapshotID,
		Task:  semantic.MapTaskContext{TaskID: "task-mcp", IntentRevision: 1, Mode: "feature"},
		Basis: semantic.MapBasisContext{RepositoryID: repositoryID, WorkspaceEpoch: int64(epoch), ComputedWorkspaceSnapshotID: snapshotID, SnapshotTreeID: treeID, ComputedBasisID: basisID},
		Steps: items, RequirementAlignment: []semantic.RequirementAlignment{{CriterionID: "AC-1", Status: "partial", Authority: "candidate", Reason: "awaiting_current_proof"}},
	}
}

func TestVS05_MCPChangeImpactRequiresProofAndUsesExplicitHistoricalMap(t *testing.T) {
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
	defer srv.Close()

	ctx := context.Background()

	// VS05-A1: the seam must not invent a default target.
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

	// Explicit identity and bounds are required before a proof can be read.
	resMissingIdentity, err := srv.executeTool(ctx, "get_change_impact", map[string]any{
		"target": tempDir, "symbolId": "PaymentService.process",
	})
	if err != nil {
		t.Fatalf("unexpected identity validation error: %v", err)
	}
	identityDoc, ok := resMissingIdentity.(map[string]any)
	if !ok || identityDoc["code"] != "missing_precondition" {
		t.Fatalf("expected explicit identity precondition, got: %+v", resMissingIdentity)
	}

	// A current query with explicit identity still fails closed without a
	// validated publication. It must not synthesize an "active" map.
	resNoProof, err := srv.executeTool(ctx, "get_change_impact", map[string]any{
		"target": tempDir, "symbolId": "PaymentService.process", "computedBasisId": "basis-1", "generationId": "gen-1",
		"freshness": "current", "maxDepth": float64(2), "maxNodes": float64(10), "relationKinds": []any{"calls"},
	})
	if err != nil {
		t.Fatalf("unexpected no-proof execution error: %v", err)
	}
	noProofDoc, ok := resNoProof.(map[string]any)
	if !ok || noProofDoc["code"] != "missing_precondition" {
		t.Fatalf("expected missing current proof, got: %+v", resNoProof)
	}

	// Historical queries use an explicitly cached v2 map. No analyzer
	// capability is attached to this cache, so the result remains bounded and
	// reports a coverage frontier instead of claiming repository-wide absence.
	historical := explicitComparableSemanticMap(tempDir, "gen-historical", "basis-historical", "snap-historical", "tree-historical", 1, 1)
	historical.SchemaID = semantic.SemanticMapSchemaID
	historical.SchemaVersion = semantic.SemanticSchemaVersion
	historical.Freshness = "historical"
	historical.Authority = "historical"
	historical.Coverage = &semantic.CoverageBoundary{IncludedSourceRoots: []string{"src"}, ExcludedReasons: []string{"adapter_capability_not_persisted"}}
	srv.rememberSemanticMap(tempDir, historical)
	resImpact, err := srv.executeTool(ctx, "get_change_impact", map[string]any{
		"target": tempDir, "symbolId": "Service.step1", "computedBasisId": "basis-historical", "generationId": "gen-historical",
		"freshness": "historical", "maxDepth": float64(2), "maxNodes": float64(10), "relationKinds": []any{"calls"},
	})
	if err != nil {
		t.Fatalf("historical get_change_impact failed: %v", err)
	}
	impactGraph, ok := resImpact.(map[string]any)
	if !ok {
		t.Fatalf("expected canonical impact payload, got %T: %+v", resImpact, resImpact)
	}
	if impactGraph["schemaId"] != semantic.ChangeImpactGraphSchemaID {
		t.Errorf("expected v2 impact schema, got %v", impactGraph["schemaId"])
	}
	target, ok := impactGraph["target"].(map[string]any)
	if !ok || target["symbolId"] != "Service.step1" {
		t.Errorf("expected explicit historical target, got %v", impactGraph["target"])
	}
	if impactGraph["unknownCount"] == float64(0) {
		t.Errorf("historical cache without capability must expose an unknown frontier: %+v", impactGraph)
	}
}

func TestVS05_MCPTaskViewImpactForwardsExplicitContract(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "codeflow-mcp-task-impact-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	srv, err := NewServer(Config{RepoRoot: tempDir, AuthToken: "task-impact-token", RequireToken: true})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}
	defer srv.Close()

	historical := explicitComparableSemanticMap(tempDir, "gen-task-impact", "basis-task-impact", "snap-task-impact", "tree-task-impact", 1, 1)
	historical.SchemaID = semantic.SemanticMapSchemaID
	historical.SchemaVersion = semantic.SemanticSchemaVersion
	historical.Freshness = "historical"
	historical.Authority = "historical"
	historical.Coverage = &semantic.CoverageBoundary{IncludedSourceRoots: []string{"src"}, ExcludedReasons: []string{"adapter_capability_not_persisted"}}
	srv.rememberSemanticMap(tempDir, historical)

	ctx := context.Background()
	missing, err := srv.executeTool(ctx, "query_task_view", map[string]any{
		"token":  "task-impact-token",
		"target": tempDir,
		"query": map[string]any{
			"schemaId":      "https://codeflow.local/schemas/task-view-query.schema.json",
			"schemaVersion": 1,
			"mode":          "impact",
			"impact":        map[string]any{"symbolId": "Service.step1"},
		},
	})
	if err == nil || !strings.Contains(err.Error(), semantic.ErrCodeMissingPrecondition) {
		t.Fatalf("expected Task View impact schema precondition, got result=%+v error=%v", missing, err)
	}

	res, err := srv.executeTool(ctx, "query_task_view", map[string]any{
		"token":  "task-impact-token",
		"target": tempDir,
		"query": map[string]any{
			"schemaId":      "https://codeflow.local/schemas/task-view-query.schema.json",
			"schemaVersion": 1,
			"mode":          "impact",
			"impact": map[string]any{
				"symbolId":        "Service.step1",
				"computedBasisId": "basis-task-impact",
				"generationId":    "gen-task-impact",
				"freshness":       "historical",
				"maxDepth":        4,
				"maxNodes":        7,
				"relationKinds":   []string{"calls"},
			},
		},
	})
	if err != nil {
		t.Fatalf("Task View impact execution failed: %v", err)
	}
	graph, ok := res.(map[string]any)
	if !ok {
		t.Fatalf("expected impact graph, got %T: %+v", res, res)
	}
	if graph["computedBasisId"] != "basis-task-impact" || graph["generationId"] != "gen-task-impact" || graph["freshness"] != "historical" {
		t.Fatalf("Task View impact identity was not forwarded exactly: %+v", graph)
	}
	indirect, ok := graph["indirectImpact"].(map[string]any)
	if !ok || indirect["maxDepth"] != float64(4) || indirect["maxNodes"] != float64(7) {
		t.Fatalf("Task View impact bounds were not forwarded exactly: %+v", graph["indirectImpact"])
	}
}

func TestVS07_MCPInvestigateFailure(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "codeflow-mcp-failure-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	fixture := newMCPFailureFixture("snapshot-vs06")
	srv, err := NewServer(Config{
		RepoRoot:     tempDir,
		RequireToken: false,
		RuntimeObservationProvider: RuntimeObservationProviderFunc(func(_ context.Context, request RuntimeObservationRequest) (*semantic.RuntimeObservationV2, error) {
			if request.ObservationID != fixture.observation.ObservationID {
				t.Fatalf("provider received observation id %q, want %q", request.ObservationID, fixture.observation.ObservationID)
			}
			return fixture.observation, nil
		}),
	})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}
	defer srv.Close()

	ctx := context.Background()

	// The legacy top-level mode/error shape is no longer an authority boundary.
	// A request without an explicit v2 query receives a typed precondition.
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

	// Explicit historical map and proof identities are required. The trace is
	// returned by the semantic v2 reverse path, not a synthetic origin.
	resDebug, err := srv.executeTool(ctx, "investigate_failure", map[string]any{
		"target":      tempDir,
		"query":       fixture.debugQuery,
		"semanticMap": fixture.mapIR,
		"proof":       fixture.proof,
	})
	if err != nil {
		t.Fatalf("investigate_failure debug failed: %v", err)
	}
	debugTrace, ok := resDebug.(*semantic.FailurePathTraceV2)
	if !ok {
		t.Fatalf("expected *semantic.FailurePathTraceV2, got %T", resDebug)
	}
	if debugTrace.SchemaID != semantic.FailurePathTraceSchemaID || debugTrace.SchemaVersion != semantic.FailureContractSchemaVersion || debugTrace.Mode != "debug" {
		t.Errorf("expected canonical debug trace, got %+v", debugTrace)
	}
	if len(debugTrace.Nodes) != 3 || len(debugTrace.Relationships) != 2 {
		t.Errorf("expected canonical reverse path, got nodes=%d relationships=%d", len(debugTrace.Nodes), len(debugTrace.Relationships))
	}
	for _, node := range debugTrace.Nodes {
		if node.NodeID == "node-origin" || node.SymbolPath == "ErrorOrigin" || len(node.EvidenceRefs) == 0 {
			t.Fatalf("synthetic or evidence-free node escaped: %+v", node)
		}
	}

	// Incident evidence is resolved by identifier through the trusted provider.
	// The only timeline event is the event returned by that provider.
	resInc, err := srv.executeTool(ctx, "investigate_failure", map[string]any{
		"target":      tempDir,
		"query":       fixture.incidentQuery,
		"semanticMap": fixture.mapIR,
		"proof":       fixture.proof,
	})
	if err != nil {
		t.Fatalf("investigate_failure incident failed: %v", err)
	}
	incTrace, ok := resInc.(*semantic.FailurePathTraceV2)
	if !ok {
		t.Fatalf("expected *semantic.FailurePathTraceV2, got %T", resInc)
	}
	if incTrace.Mode != "incident" || incTrace.RuntimeObservationRef != fixture.observation.ObservationID {
		t.Errorf("expected scoped incident trace, got %+v", incTrace)
	}
	if len(incTrace.Timeline) != 1 || incTrace.Timeline[0].EventID != "event-failure" {
		t.Errorf("incident trace contains synthetic or missing events: %+v", incTrace.Timeline)
	}

	// query_task_view uses the same explicit v2 failure seam.
	resQuery, err := srv.executeTool(ctx, "query_task_view", map[string]any{
		"target":      tempDir,
		"query":       fixture.debugQuery,
		"semanticMap": fixture.mapIR,
		"proof":       fixture.proof,
	})
	if err != nil {
		t.Fatalf("query_task_view v2 debug failed: %v", err)
	}
	qTrace, ok := resQuery.(*semantic.FailurePathTraceV2)
	if !ok {
		t.Fatalf("expected *semantic.FailurePathTraceV2 from query_task_view, got %T", resQuery)
	}
	if qTrace.Mode != "debug" || qTrace.ComputedBasisID != fixture.debugQuery.ComputedBasisID {
		t.Errorf("query_task_view did not preserve v2 identity: %+v", qTrace)
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
	approvalFixture := completeUnattestedAvailableResultForMCP(t)
	srv.proposalStore = &mcpApprovalPairStore{stored: &semantic.StoredProposal{
		WorkspaceID: srv.approvalWorkspaceID, Proposal: approvalFixture.Proposal, Pack: approvalFixture.Pack,
	}}

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

	// 3. submit_semantic_approval missing command fields is rejected before
	// any approval execution work.
	if _, err := srv.executeTool(ctx, "submit_semantic_approval", map[string]any{
		"target": tempDir, "proposalId": "",
	}); err == nil {
		t.Fatal("incomplete approval draft unexpectedly succeeded")
	}

	// 4. A complete draft still requires a published current proof and the
	// durable proposal pair. This fixture has neither, so execution fails
	// closed instead of synthesizing a legacy approval.
	if _, err := srv.executeTool(ctx, "submit_semantic_approval", mcpApprovalRequestArgs(tempDir, approvalFixture, "legacy-test")); !errors.Is(err, semantic.ErrApprovalExecutionUnavailable) {
		t.Fatalf("complete draft error = %v, want ErrApprovalExecutionUnavailable", err)
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

	// A repository id without an exact basis must not select a synthetic
	// workspace/domain set.
	_, err = srv.executeTool(ctx, "explore_project_domains", map[string]any{
		"target":       tempDir,
		"repositoryId": "shop-app",
		"level":        float64(1),
	})
	if err == nil || !strings.Contains(err.Error(), "freshness is required") {
		t.Fatalf("expected strict freshness precondition, got %v", err)
	}

	// An explicit historical identity still cannot succeed without an exact
	// cached semantic map. The old fixed Order/Catalog candidates are gone.
	_, err = srv.executeTool(ctx, "explore_project_domains", map[string]any{
		"target":                     tempDir,
		"level":                      float64(2),
		"domain":                     "src/orders",
		"repositoryId":               "shop-app",
		"freshness":                  "historical",
		"computedBasisId":            "basis-historical",
		"generationId":               "generation-historical",
		"validatedAgainstSnapshotId": "snapshot-historical",
	})
	if err == nil || !strings.Contains(err.Error(), "historical semantic map") {
		t.Fatalf("expected unavailable historical basis, got %v", err)
	}

	// query_task_view onboarding follows the same precondition and does not
	// default repositoryId or basis.
	_, err = srv.executeTool(ctx, "query_task_view", map[string]any{
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
		if !strings.Contains(err.Error(), "freshness is required") {
			t.Fatalf("expected strict query_task_view basis precondition, got %v", err)
		}
	} else {
		t.Fatal("expected query_task_view onboarding to require an exact basis")
	}

	// The task-view seam also accepts the normalized onboarding v2 query. It
	// must reach the exact historical resolver instead of being rejected by the
	// legacy task-view schema or silently losing its identity fields.
	_, err = srv.executeTool(ctx, "query_task_view", map[string]any{
		"target": tempDir,
		"query": map[string]any{
			"schemaId":                   semantic.OnboardingQuerySchemaID,
			"schemaVersion":              semantic.SemanticSchemaVersion,
			"repositoryId":               "shop-app",
			"computedBasisId":            "basis-historical",
			"generationId":               "generation-historical",
			"validatedAgainstSnapshotId": "snapshot-historical",
			"freshness":                  "historical",
			"level":                      1,
		},
	})
	if err == nil || !strings.Contains(err.Error(), "historical semantic map") {
		t.Fatalf("expected v2 query_task_view to require the selected historical map, got %v", err)
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

	res, err := srv.executeTool(ctx, "validate_release_capability", map[string]any{})
	if err != nil {
		t.Fatalf("validate_release_capability failed: %v", err)
	}

	evaluation, ok := res.(*semantic.ReleaseEvaluation)
	if !ok {
		t.Fatalf("expected *semantic.ReleaseEvaluation, got %T", res)
	}
	if evaluation.BenchmarkReport.Status != "incomplete" || evaluation.BenchmarkReport.ReleaseReady {
		t.Fatalf("missing evidence must be inspectable and incomplete: %+v", evaluation.BenchmarkReport)
	}
	if len(evaluation.BenchmarkReport.Metrics) != 0 || len(evaluation.CapabilityMatrix.Capabilities) != 0 {
		t.Fatalf("missing evidence must not generate metrics or capability claims: %+v", evaluation)
	}

	res, err = srv.executeTool(ctx, "validate_release_capability", map[string]any{"evaluation": testfixture.VS10ReleaseEvaluationInput()})
	if err != nil {
		t.Fatalf("explicit evidence evaluation failed: %v", err)
	}
	evaluation, ok = res.(*semantic.ReleaseEvaluation)
	if !ok || evaluation.BenchmarkReport.Status != "incomplete" || evaluation.BenchmarkReport.ReleaseReady {
		t.Fatalf("default MCP server trusted caller decision labels: %#v", res)
	}

	configured, err := NewServer(Config{
		RepoRoot:                  tempDir,
		RequireToken:              false,
		ReleaseThresholdDecisions: testfixture.VS10ReleaseThresholdDecisions(),
	})
	if err != nil {
		t.Fatalf("configured NewServer failed: %v", err)
	}
	res, err = configured.executeTool(ctx, "validate_release_capability", map[string]any{"evaluation": testfixture.VS10ReleaseEvaluationInput()})
	if err != nil {
		t.Fatalf("configured explicit evidence evaluation failed: %v", err)
	}
	evaluation, ok = res.(*semantic.ReleaseEvaluation)
	if !ok || !evaluation.BenchmarkReport.ReleaseReady || !evaluation.CapabilityMatrix.ReleaseReady {
		t.Fatalf("trusted configured decisions did not cross the MCP boundary: %#v", res)
	}

	tampered := testfixture.VS10ReleaseEvaluationInput()
	tampered.Profile.OS = "linux"
	res, err = configured.executeTool(ctx, "validate_release_capability", map[string]any{"evaluation": tampered})
	if err != nil {
		t.Fatalf("tampered evidence must return an inspectable result: %v", err)
	}
	evaluation = res.(*semantic.ReleaseEvaluation)
	if evaluation.BenchmarkReport.Status != "incomplete" || evaluation.BenchmarkReport.ReleaseReady {
		t.Fatalf("MCP accepted evidence whose content did not match artifactRef: %+v", evaluation.BenchmarkReport)
	}
}
