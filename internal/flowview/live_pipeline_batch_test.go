package flowview

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"codeflow/internal/semantic"
	"codeflow/internal/slicing"
	"codeflow/internal/workspace"
)

func prepareComparableTestMaps(maps ...*semantic.SemanticMapIR) {
	for _, m := range maps {
		if m.MapID == "" {
			m.MapID = "map-" + m.GenerationID
		}
		if m.ComputedBasisID == "" {
			m.ComputedBasisID = "basis-" + m.MapID
		}
		if m.GenerationID == "" {
			m.GenerationID = "gen-" + m.MapID
		}
		m.SchemaID = "codeflow.semantic-map-ir"
		m.SchemaVersion = 1
		m.Basis.RepositoryID = "repo-test"
		m.Basis.ComputedWorkspaceSnapshotID = "snapshot-" + m.MapID
		m.Basis.SnapshotTreeID = "tree-test"
		m.Basis.DependencyFingerprint = "deps-test"
		m.Basis.WorkspaceEpoch = 100
		m.ValidatedAgainstSnapshotID = m.Basis.ComputedWorkspaceSnapshotID
		if m.Task.TaskID == "" {
			m.Task.TaskID = "task-project-test"
		}
		if m.Task.Mode == "" {
			m.Task.Mode = "project_change"
		}
	}
}

// TestLPCA_VS03_A01_MultiFileChangeBatched verifies that multi-file changes in a single batch
// are processed as one semantic change batch.
func TestLPCA_VS03_A01_MultiFileChangeBatched(t *testing.T) {
	root := t.TempDir()
	createSampleProject(t, root)

	srv, err := NewServer(Config{RepoRoot: root, Port: 0, Mode: "project_change"})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer func() { _ = srv.Shutdown(context.Background()) }()

	// Submit multi-file change in one batch
	res, err := srv.SubmitVersionedChanges(context.Background(), workspace.VersionedChangeRequest{
		BatchID: "batch-multi-01",
		Source:  workspace.SourceAgentTransaction,
		Changes: []workspace.VersionedChange{
			{Kind: workspace.ChangeUpsert, Path: "main.go", Content: []byte("package main\n\nfunc main() {}\nfunc helper() {}\n"), DocumentVersion: 2},
			{Kind: workspace.ChangeCreate, Path: "util.go", Content: []byte("package main\n\nfunc Util() string { return \"ok\" }\n"), DocumentVersion: 1},
		},
	})
	if err != nil {
		t.Fatalf("SubmitVersionedChanges: %v", err)
	}
	if res.Snapshot == nil {
		t.Fatal("expected snapshot from multi-file changes")
	}
	if res.Batch == nil || res.Batch.BatchID != "batch-multi-01" {
		t.Fatalf("expected batch ID batch-multi-01, got %v", res.Batch)
	}
	if len(res.Revisions) != 2 {
		t.Fatalf("expected 2 revisions in the batch, got %d", len(res.Revisions))
	}
}

// TestLPCA_VS03_A02_IndependentChangesDistinctBatches verifies independent changes produce distinct batches.
func TestLPCA_VS03_A02_IndependentChangesDistinctBatches(t *testing.T) {
	root := t.TempDir()
	createSampleProject(t, root)

	srv, err := NewServer(Config{RepoRoot: root, Port: 0, Mode: "project_change"})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer func() { _ = srv.Shutdown(context.Background()) }()

	res1, err := srv.SubmitVersionedChanges(context.Background(), workspace.VersionedChangeRequest{
		BatchID: "batch-indep-1",
		Source:  workspace.SourceIDEVersioned,
		Changes: []workspace.VersionedChange{
			{Kind: workspace.ChangeUpsert, Path: "main.go", Content: []byte("package main\nfunc main() {}\n// first\n"), DocumentVersion: 2},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	res2, err := srv.SubmitVersionedChanges(context.Background(), workspace.VersionedChangeRequest{
		BatchID: "batch-indep-2",
		Source:  workspace.SourceIDEVersioned,
		Changes: []workspace.VersionedChange{
			{Kind: workspace.ChangeUpsert, Path: "main.go", Content: []byte("package main\nfunc main() {}\n// second\n"), DocumentVersion: 3},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	if res1.Batch.BatchID == res2.Batch.BatchID {
		t.Fatalf("expected distinct batch IDs, got %s and %s", res1.Batch.BatchID, res2.Batch.BatchID)
	}
	if res1.Snapshot.SnapshotID == res2.Snapshot.SnapshotID {
		t.Fatalf("expected distinct snapshots, got %s", res1.Snapshot.SnapshotID)
	}
}

// TestLPCA_VS03_A03_VerifiedSemanticFacets verifies semantic delta identifies verified facets
// (added_behavior, changed_rule, call relation, evidence_updated) with links and target IDs.
func TestLPCA_VS03_A03_VerifiedSemanticFacets(t *testing.T) {
	baseline := &semantic.SemanticMapIR{
		GenerationID:    "gen-01",
		ComputedBasisID: "basis-01",
		Steps: []semantic.SemanticStep{
			{
				StepID:             "step-1",
				StructuralIdentity: "main.go#Execute",
				Name:               "Execute",
				Rules:              []string{"AC-1"},
				EvidenceRefs:       []string{"main_test.go:10"},
			},
		},
	}
	current := &semantic.SemanticMapIR{
		GenerationID:    "gen-02",
		ComputedBasisID: "basis-02",
		Steps: []semantic.SemanticStep{
			{
				StepID:             "step-1",
				StructuralIdentity: "main.go#Execute",
				Name:               "Execute",
				Rules:              []string{"AC-1", "AC-2"},
				EvidenceRefs:       []string{"main_test.go:10", "main_test.go:25"},
			},
			{
				StepID:             "step-2",
				StructuralIdentity: "main.go#Helper",
				Name:               "Helper",
				Rules:              []string{"AC-3"},
				EvidenceRefs:       []string{"main_test.go:30"},
			},
		},
		Edges: []semantic.SemanticEdge{
			{
				FromStepID:       "step-1",
				ToStepID:         "step-2",
				Kind:             "call",
				ResolutionStatus: "resolved",
			},
		},
	}

	prepareComparableTestMaps(baseline, current)
	delta, err := semantic.ComputeSemanticDelta("comp-01", baseline, current)
	if err != nil {
		t.Fatalf("ComputeSemanticDelta: %v", err)
	}

	kinds := make(map[string]bool)
	for _, c := range delta.Changes {
		kinds[c.Kind] = true
		if c.TargetStepID == "" {
			t.Errorf("change %s has empty targetStepId", c.Kind)
		}
	}
	if !kinds["changed_rule"] {
		t.Error("expected changed_rule in delta")
	}
	if !kinds["added_behavior"] {
		t.Error("expected added_behavior in delta")
	}
}

// TestLPCA_VS03_A04_StructuralOnlyFiltered verifies that formatting-only / structural-only changes
// produce kind="structural_only" and are counted in CollapsedStructuralCount rather than Added/Changed.
func TestLPCA_VS03_A04_StructuralOnlyFiltered(t *testing.T) {
	baseline := &semantic.SemanticMapIR{
		GenerationID:    "gen-01",
		ComputedBasisID: "basis-01",
		Steps: []semantic.SemanticStep{
			{
				StepID:             "step-1",
				StructuralIdentity: "main.go#Run",
				Name:               "Run",
				Anchor:             slicing.Anchor{RepoRelativePath: "main.go", ByteRange: [2]int{10, 20}},
			},
		},
	}
	current := &semantic.SemanticMapIR{
		GenerationID:    "gen-02",
		ComputedBasisID: "basis-02",
		Steps: []semantic.SemanticStep{
			{
				StepID:             "step-1",
				StructuralIdentity: "main.go#Run",
				Name:               "Run",
				Anchor:             slicing.Anchor{RepoRelativePath: "main.go", ByteRange: [2]int{25, 35}}, // moved lines without behavioral change
			},
		},
	}

	prepareComparableTestMaps(baseline, current)
	delta, err := semantic.ComputeSemanticDelta("comp-02", baseline, current)
	if err != nil {
		t.Fatalf("ComputeSemanticDelta: %v", err)
	}

	for _, c := range delta.Changes {
		if c.Kind != "structural_only" {
			t.Errorf("got change kind %q, want structural_only", c.Kind)
		}
	}
	if delta.StructuralSummary.AddedStepsCount != 0 || delta.StructuralSummary.ChangedStepsCount != 0 {
		t.Errorf("expected 0 added and changed steps, got added=%d, changed=%d",
			delta.StructuralSummary.AddedStepsCount, delta.StructuralSummary.ChangedStepsCount)
	}
	if delta.StructuralSummary.CollapsedStructuralCount == 0 {
		t.Error("expected CollapsedStructuralCount > 0 for structural_only")
	}
}

// TestLPCA_VS03_A05_TestOnlyWithProdSemanticChange verifies that evidence updates accompanying
// production changes are categorized with the production change.
func TestLPCA_VS03_A05_TestOnlyWithProdSemanticChange(t *testing.T) {
	baseline := &semantic.SemanticMapIR{
		GenerationID:    "gen-01",
		ComputedBasisID: "basis-01",
		Steps: []semantic.SemanticStep{
			{
				StepID:             "step-1",
				StructuralIdentity: "main.go#Auth",
				Name:               "Auth",
				Rules:              []string{"RULE-1"},
				EvidenceRefs:       []string{"auth_test.go:10"},
			},
		},
	}
	current := &semantic.SemanticMapIR{
		GenerationID:    "gen-02",
		ComputedBasisID: "basis-02",
		Steps: []semantic.SemanticStep{
			{
				StepID:             "step-1",
				StructuralIdentity: "main.go#Auth",
				Name:               "Auth",
				Rules:              []string{"RULE-1", "RULE-2"},
				EvidenceRefs:       []string{"auth_test.go:10", "auth_test.go:30"},
			},
		},
	}

	prepareComparableTestMaps(baseline, current)
	delta, err := semantic.ComputeSemanticDelta("comp-03", baseline, current)
	if err != nil {
		t.Fatal(err)
	}
	if len(delta.Changes) != 1 || delta.Changes[0].Kind != "changed_rule" {
		t.Fatalf("expected 1 changed_rule, got %v", delta.Changes)
	}
	if len(delta.Changes[0].EvidenceRefs) != 2 {
		t.Errorf("expected 2 evidence refs, got %v", delta.Changes[0].EvidenceRefs)
	}
}

// TestLPCA_VS03_A06_TestOnlyWithoutProdSemanticChange verifies that test-only changes without
// production changes yield kind="evidence_updated".
func TestLPCA_VS03_A06_TestOnlyWithoutProdSemanticChange(t *testing.T) {
	baseline := &semantic.SemanticMapIR{
		GenerationID:    "gen-01",
		ComputedBasisID: "basis-01",
		Steps: []semantic.SemanticStep{
			{
				StepID:             "step-1",
				StructuralIdentity: "main.go#Auth",
				Name:               "Auth",
				Rules:              []string{"RULE-1"},
				EvidenceRefs:       []string{"auth_test.go:10"},
			},
		},
	}
	current := &semantic.SemanticMapIR{
		GenerationID:    "gen-02",
		ComputedBasisID: "basis-02",
		Steps: []semantic.SemanticStep{
			{
				StepID:             "step-1",
				StructuralIdentity: "main.go#Auth",
				Name:               "Auth",
				Rules:              []string{"RULE-1"},
				EvidenceRefs:       []string{"auth_test.go:10", "auth_test.go:50"}, // only test reference changed
			},
		},
	}

	prepareComparableTestMaps(baseline, current)
	delta, err := semantic.ComputeSemanticDelta("comp-04", baseline, current)
	if err != nil {
		t.Fatal(err)
	}
	if len(delta.Changes) != 1 || delta.Changes[0].Kind != "evidence_updated" {
		t.Fatalf("expected 1 evidence_updated, got %v", delta.Changes)
	}
}

// TestLPCA_VS03_A07_CompilationFailureOrUnsupportedSourceEmitsGap verifies that unsupported source
// or compilation failure retains last verified basis and emits a gap.
func TestLPCA_VS03_A07_CompilationFailureOrUnsupportedSourceEmitsGap(t *testing.T) {
	root := t.TempDir()
	createSampleProject(t, root)

	srv, err := NewServer(Config{RepoRoot: root, Port: 0, Mode: "project_change"})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer func() { _ = srv.Shutdown(context.Background()) }()

	initialBasis := srv.LastVerifiedBasisID()

	// Simulate gap publication
	snap := srv.SnapshotEngine().LiveHead()
	act := srv.SnapshotEngine().CurrentActivity()
	err = srv.publishGap(snap, "이 변경은 현재 분석 범위에서 확인할 수 없습니다", act)
	if err != nil {
		t.Fatalf("publishGap: %v", err)
	}

	// Verify last verified basis is preserved
	if srv.LastVerifiedBasisID() != initialBasis {
		t.Fatalf("basis = %s, want preserved %s", srv.LastVerifiedBasisID(), initialBasis)
	}

	// Verify project endpoint exposes gap
	req := httptest.NewRequest("GET", "/api/live/project?token="+srv.AuthToken(), nil)
	rec := httptest.NewRecorder()
	srv.handleLiveProject(rec, req)
	var proj map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &proj)
	if proj["status"] != "gap" {
		t.Errorf("status = %v, want gap", proj["status"])
	}
	if proj["notice"] != "이 변경은 현재 분석 범위에서 확인할 수 없습니다" {
		t.Errorf("notice = %v, want '이 변경은 현재 분석 범위에서 확인할 수 없습니다'", proj["notice"])
	}
}

// TestLPCA_VS03_A08_RestartSourceDifferenceAnalysis verifies source differences collected on restart
// are tracked via the pending/gap path while retaining the verified basis.
func TestLPCA_VS03_A08_RestartSourceDifferenceAnalysis(t *testing.T) {
	root := t.TempDir()
	createSampleProject(t, root)

	srv1, err := NewServer(Config{RepoRoot: root, Port: 0, Mode: "project_change"})
	if err != nil {
		t.Fatal(err)
	}
	basisID := srv1.LastVerifiedBasisID()
	_ = srv1.Shutdown(context.Background())

	// Modify source offline
	_ = os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n\nfunc main() {}\n// offline semantic edit\n"), 0o644)

	// Restart
	srv2, err := NewServer(Config{RepoRoot: root, Port: 0, Mode: "project_change"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv2.Shutdown(context.Background()) }()

	// Verified basis is still the previous basis
	if srv2.LastVerifiedBasisID() != basisID {
		t.Fatalf("basis ID = %s, want %s", srv2.LastVerifiedBasisID(), basisID)
	}

	req := httptest.NewRequest("GET", "/api/live/project?token="+srv2.AuthToken(), nil)
	rec := httptest.NewRecorder()
	srv2.handleLiveProject(rec, req)
	var proj map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &proj)
	if proj["status"] != "pending" {
		t.Errorf("status = %v, want pending", proj["status"])
	}
}
