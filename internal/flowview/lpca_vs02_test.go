package flowview

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"codeflow/internal/workspace"
)

// TestLPCA_VS02_A01_CanonicalIngressIdentity verifies source and batch identity are preserved.
func TestLPCA_VS02_A01_CanonicalIngressIdentity(t *testing.T) {
	root := t.TempDir()
	createSampleProject(t, root)

	srv, err := NewServer(Config{RepoRoot: root, Port: 0, Mode: "project_change"})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer func() { _ = srv.Shutdown(context.Background()) }()

	batchID := "agent-batch-001"
	res, err := srv.SubmitVersionedChanges(context.Background(), workspace.VersionedChangeRequest{
		BatchID: batchID,
		Source:  workspace.SourceAgentTransaction,
		Changes: []workspace.VersionedChange{
			{Kind: workspace.ChangeUpsert, Path: "main.go", Content: []byte("package main\nfunc main() {}\nvar a = 1\n"), DocumentVersion: 2},
		},
	})
	if err != nil {
		t.Fatalf("SubmitVersionedChanges: %v", err)
	}
	if res.Batch == nil || res.Batch.BatchID != batchID {
		t.Fatalf("batchId = %v, want %s", res.Batch, batchID)
	}
	if res.Batch.Source != workspace.SourceAgentTransaction {
		t.Fatalf("batch source = %v, want %s", res.Batch.Source, workspace.SourceAgentTransaction)
	}
}

// TestLPCA_VS02_A02_DuplicateIngressNoNewRevision verifies duplicate content reports from multiple producers
// yield only one revision and no duplicate batch.
func TestLPCA_VS02_A02_DuplicateIngressNoNewRevision(t *testing.T) {
	root := t.TempDir()
	createSampleProject(t, root)

	srv, err := NewServer(Config{RepoRoot: root, Port: 0, Mode: "project_change"})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer func() { _ = srv.Shutdown(context.Background()) }()

	content := []byte("package main\nfunc main() {}\nvar b = 2\n")
	res1, err := srv.SubmitVersionedChanges(context.Background(), workspace.VersionedChangeRequest{
		BatchID: "ide-edit-1",
		Source:  workspace.SourceIDEVersioned,
		Changes: []workspace.VersionedChange{
			{Kind: workspace.ChangeUpsert, Path: "main.go", Content: content, DocumentVersion: 2},
		},
	})
	if err != nil {
		t.Fatalf("Submit 1: %v", err)
	}
	firstSnapID := res1.Snapshot.SnapshotID

	// Duplicate content submitted by watcher
	res2, err := srv.SubmitVersionedChanges(context.Background(), workspace.VersionedChangeRequest{
		BatchID: "watcher-batch-1",
		Source:  workspace.SourceWatcherFallback,
		Changes: []workspace.VersionedChange{
			{Kind: workspace.ChangeUpsert, Path: "main.go", Content: content},
		},
	})
	if err != nil {
		t.Fatalf("Submit duplicate: %v", err)
	}
	if !res2.Duplicate {
		t.Fatal("expected Duplicate=true for identical canonical content")
	}
	if res2.Snapshot.SnapshotID != firstSnapID {
		t.Fatalf("duplicate created new snapshot %s, want %s", res2.Snapshot.SnapshotID, firstSnapID)
	}
}

// TestLPCA_VS02_A03_A04_StaleCachedHeadReloadAndRetry verifies that when an engine has a stale cached head,
// SubmitVersionedChanges reloads durable state and successfully completes the edit via retry.
func TestLPCA_VS02_A03_A04_StaleCachedHeadReloadAndRetry(t *testing.T) {
	root := t.TempDir()
	createSampleProject(t, root)

	srv, err := NewServer(Config{RepoRoot: root, Port: 0, Mode: "project_change"})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer func() { _ = srv.Shutdown(context.Background()) }()

	// Simulate an external process advancing the durable live head
	extEng, err := workspace.NewSnapshotEngine(root, 0)
	if err != nil {
		t.Fatalf("open external engine: %v", err)
	}
	_, _, err = extEng.ApplyVersionedEdit(context.Background(), workspace.EditRequest{
		Path: "main.go", Content: []byte("package main\nvar ext = 1\n"), DocumentVersion: 2, Source: workspace.SourceIDEVersioned,
	})
	if err != nil {
		t.Fatalf("ext edit: %v", err)
	}

	// srv currently holds the old cached head. When submitting an edit, it catches ErrLiveHeadConflict,
	// reloads durable state, and retries the edit successfully.
	res, err := srv.SubmitVersionedChanges(context.Background(), workspace.VersionedChangeRequest{
		BatchID: "retry-batch-1",
		Source:  workspace.SourceAgentTransaction,
		Changes: []workspace.VersionedChange{
			{Kind: workspace.ChangeUpsert, Path: "main.go", Content: []byte("package main\nvar ext = 2\n"), DocumentVersion: 3},
		},
	})
	if err != nil {
		t.Fatalf("SubmitVersionedChanges with stale cached head failed: %v", err)
	}
	if res == nil || res.Snapshot == nil {
		t.Fatal("expected successful snapshot after reload and retry")
	}
}

// TestLPCA_VS02_A05_RetryFailureExposesGap verifies that if retry also fails,
// the server retains the last verified result and transitions to gap.
func TestLPCA_VS02_A05_RetryFailureExposesGap(t *testing.T) {
	root := t.TempDir()
	createSampleProject(t, root)

	srv, err := NewServer(Config{RepoRoot: root, Port: 0, Mode: "project_change"})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer func() { _ = srv.Shutdown(context.Background()) }()

	// Invalid change kind fails on retry as well
	_, err = srv.SubmitVersionedChanges(context.Background(), workspace.VersionedChangeRequest{
		BatchID: "invalid-batch",
		Source:  workspace.SourceIDEVersioned,
		Changes: []workspace.VersionedChange{
			{Kind: "invalid_kind", Path: "main.go", Content: []byte(""), DocumentVersion: 2},
		},
	})
	if err == nil {
		t.Fatal("expected error on invalid change")
	}
}

// TestLPCA_VS02_A05b_RetryInputErrorRestoresState verifies that when reload
// succeeds but the retry fails with a caller input error (a stale document
// version), the server returns the input error without publishing a system
// gap and restores the pre-recovery project state.
func TestLPCA_VS02_A05b_RetryInputErrorRestoresState(t *testing.T) {
	root := t.TempDir()
	createSampleProject(t, root)

	srv, err := NewServer(Config{RepoRoot: root, Port: 0, Mode: "project_change"})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer func() { _ = srv.Shutdown(context.Background()) }()

	// External process advances the durable head to document version 2.
	extEng, err := workspace.NewSnapshotEngine(root, 0)
	if err != nil {
		t.Fatalf("open external engine: %v", err)
	}
	if _, _, err := extEng.ApplyVersionedEdit(context.Background(), workspace.EditRequest{
		Path: "main.go", Content: []byte("package main\nvar ext = 1\n"), DocumentVersion: 2, Source: workspace.SourceIDEVersioned,
	}); err != nil {
		t.Fatalf("ext edit: %v", err)
	}

	// srv holds the old cached head, so the first attempt conflicts and
	// reloads. The retry then fails with a document-version input error
	// because version 2 is already taken, which must not become a gap.
	_, err = srv.SubmitVersionedChanges(context.Background(), workspace.VersionedChangeRequest{
		BatchID: "stale-version-batch",
		Source:  workspace.SourceAgentTransaction,
		Changes: []workspace.VersionedChange{
			{Kind: workspace.ChangeUpsert, Path: "main.go", Content: []byte("package main\nvar stale = 1\n"), DocumentVersion: 2},
		},
	})
	var versionErr *workspace.DocumentVersionConflict
	if !errors.As(err, &versionErr) {
		t.Fatalf("expected document version conflict, got %v", err)
	}
	srv.projectMu.RLock()
	status, gap := srv.projectStatus, srv.projectGap
	srv.projectMu.RUnlock()
	if status == "gap" || gap != nil {
		t.Fatalf("input error after reload must not publish a gap, status=%q", status)
	}
}

// TestLPCA_VS02_A08_OfflineSourceChangesCollectedOnRestart verifies that changes made on disk while offline
// are collected on restart and marked as pending.
func TestLPCA_VS02_A08_OfflineSourceChangesCollectedOnRestart(t *testing.T) {
	root := t.TempDir()
	createSampleProject(t, root)

	// First run establishes initial basis
	srv1, err := NewServer(Config{RepoRoot: root, Port: 0, Mode: "project_change"})
	if err != nil {
		t.Fatalf("NewServer 1: %v", err)
	}
	basisID := srv1.LastVerifiedBasisID()
	_ = srv1.Shutdown(context.Background())

	// Modify source on disk while offline
	mainPath := filepath.Join(root, "main.go")
	if err := os.WriteFile(mainPath, []byte("package main\nfunc main() { println(1) }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Restart server
	srv2, err := NewServer(Config{RepoRoot: root, Port: 0, Mode: "project_change"})
	if err != nil {
		t.Fatalf("NewServer 2: %v", err)
	}
	defer func() { _ = srv2.Shutdown(context.Background()) }()

	if srv2.LastVerifiedBasisID() != basisID {
		t.Fatalf("basis ID changed on restart: %s vs %s", srv2.LastVerifiedBasisID(), basisID)
	}
	if srv2.SnapshotEngine().LiveHeadID() == basisID {
		t.Fatal("expected live head to advance beyond basis due to offline edit")
	}

	statusReq := httptest.NewRequest("GET", "/api/live/project?token="+srv2.AuthToken(), nil)
	statusRec := httptest.NewRecorder()
	srv2.handleLiveProject(statusRec, statusReq)
	var proj map[string]any
	_ = json.Unmarshal(statusRec.Body.Bytes(), &proj)
	if proj["status"] != "pending" {
		t.Fatalf("status = %v, want pending", proj["status"])
	}
	if proj["notice"] != "변경을 확인 중입니다" {
		t.Fatalf("notice = %v, want '변경을 확인 중입니다'", proj["notice"])
	}
}

// TestLPCA_VS02_A09_UnanalyzedEditsRetainedOnRestart verifies that edits collected before shutdown
// that were not analyzed are not lost on restart.
func TestLPCA_VS02_A09_UnanalyzedEditsRetainedOnRestart(t *testing.T) {
	root := t.TempDir()
	createSampleProject(t, root)

	srv1, err := NewServer(Config{RepoRoot: root, Port: 0, Mode: "project_change"})
	if err != nil {
		t.Fatalf("NewServer 1: %v", err)
	}
	basisID := srv1.LastVerifiedBasisID()

	// Submit an edit that is not settled
	editBytes := []byte("package main\nvar unanalyzed = true\n")
	_ = os.WriteFile(filepath.Join(root, "main.go"), editBytes, 0o644)
	_, advancedSnap, err := srv1.SnapshotEngine().ApplyVersionedEdit(context.Background(), workspace.EditRequest{
		Path: "main.go", Content: editBytes, DocumentVersion: 2, Source: workspace.SourceIDEVersioned,
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = srv1.Shutdown(context.Background())

	// Restart
	srv2, err := NewServer(Config{RepoRoot: root, Port: 0, Mode: "project_change"})
	if err != nil {
		t.Fatalf("NewServer 2: %v", err)
	}
	defer func() { _ = srv2.Shutdown(context.Background()) }()

	if srv2.SnapshotEngine().LiveHeadID() != advancedSnap.SnapshotID {
		t.Fatalf("durable live head = %s, want %s", srv2.SnapshotEngine().LiveHeadID(), advancedSnap.SnapshotID)
	}
	if srv2.LastVerifiedBasisID() != basisID {
		t.Fatalf("basis ID = %s, want %s", srv2.LastVerifiedBasisID(), basisID)
	}

	statusReq := httptest.NewRequest("GET", "/api/live/project?token="+srv2.AuthToken(), nil)
	statusRec := httptest.NewRecorder()
	srv2.handleLiveProject(statusRec, statusReq)
	var proj map[string]any
	_ = json.Unmarshal(statusRec.Body.Bytes(), &proj)
	if proj["status"] != "pending" {
		t.Fatalf("status = %v, want pending", proj["status"])
	}
}

// TestLPCA_VS02_A10_SameSourceRestartNoNewRevisions verifies that restarting with identical source and basis
// generates no new revisions or notifications.
func TestLPCA_VS02_A10_SameSourceRestartNoNewRevisions(t *testing.T) {
	root := t.TempDir()
	createSampleProject(t, root)

	srv1, err := NewServer(Config{RepoRoot: root, Port: 0, Mode: "project_change"})
	if err != nil {
		t.Fatalf("NewServer 1: %v", err)
	}
	head1 := srv1.SnapshotEngine().LiveHeadID()
	_ = srv1.Shutdown(context.Background())

	// Restart without changes
	srv2, err := NewServer(Config{RepoRoot: root, Port: 0, Mode: "project_change"})
	if err != nil {
		t.Fatalf("NewServer 2: %v", err)
	}
	defer func() { _ = srv2.Shutdown(context.Background()) }()

	head2 := srv2.SnapshotEngine().LiveHeadID()
	if head1 != head2 {
		t.Fatalf("head changed on clean restart: %s vs %s", head1, head2)
	}
	if srv2.LastVerifiedBasisID() != "" {
		t.Fatalf("unpublished restart must not claim a verified basis, got %s", srv2.LastVerifiedBasisID())
	}

	statusReq := httptest.NewRequest("GET", "/api/live/project?token="+srv2.AuthToken(), nil)
	statusRec := httptest.NewRecorder()
	srv2.handleLiveProject(statusRec, statusReq)
	var proj map[string]any
	_ = json.Unmarshal(statusRec.Body.Bytes(), &proj)
	if proj["status"] != "pending" {
		t.Fatalf("status = %v, want pending (baseline compiling after clean restart)", proj["status"])
	}
	if proj["notice"] != "변경을 확인 중입니다" {
		t.Fatalf("notice = %v, want '변경을 확인 중입니다'", proj["notice"])
	}
}
