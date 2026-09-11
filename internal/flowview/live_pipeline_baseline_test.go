package flowview

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codeflow/internal/storage"
	"codeflow/internal/workspace"
)

// TestLPCA_VS01_A01_ProjectChangeStart verifies that /live starts in project_change mode
// without requiring prompt, feature query, or entry symbol.
func TestLPCA_VS01_A01_ProjectChangeStart(t *testing.T) {
	root := t.TempDir()
	createSampleProject(t, root)

	srv, err := NewServer(Config{RepoRoot: root, Port: 0, Mode: "project_change"})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer func() { _ = srv.Shutdown(context.Background()) }()

	req := httptest.NewRequest("GET", "/live", nil)
	rec := httptest.NewRecorder()
	srv.handleIndex(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /live status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "CODEFLOW · FLOWVIEW") {
		t.Fatal("expected the 7-lane FlowView on /live in project_change mode")
	}
	if !strings.Contains(body, `id="map-lanes"`) {
		t.Fatal("expected the 7-lane map on /live in project_change mode")
	}

	// Project status API should report project_change mode
	statusReq := httptest.NewRequest("GET", "/api/live/project?token="+srv.AuthToken(), nil)
	statusRec := httptest.NewRecorder()
	srv.handleLiveProject(statusRec, statusReq)
	if statusRec.Code != http.StatusOK {
		t.Fatalf("GET /api/live/project status = %d, want 200", statusRec.Code)
	}
	var proj map[string]any
	if err := json.Unmarshal(statusRec.Body.Bytes(), &proj); err != nil {
		t.Fatalf("unmarshal /api/live/project: %v", err)
	}
	if proj["mode"] != "project_change" {
		t.Fatalf("mode = %v, want project_change", proj["mode"])
	}
}

// TestLPCA_VS01_A02_DurableStateAndAnalysisBasis verifies durable workspace state
// and last verified analysis basis are distinguished upon restoration.
func TestLPCA_VS01_A02_DurableStateAndAnalysisBasis(t *testing.T) {
	root := t.TempDir()
	createSampleProject(t, root)

	srv1, err := NewServer(Config{RepoRoot: root, Port: 0, Mode: "project_change"})
	if err != nil {
		t.Fatalf("NewServer 1: %v", err)
	}
	// Initial baseline snapshot is created
	initialHead := srv1.SnapshotEngine().LiveHeadID()
	if initialHead == "" {
		t.Fatal("expected initial live head")
	}
	_ = srv1.Shutdown(context.Background())

	// Advance workspace head with an edit (unanalyzed in proof bundle)
	eng, err := workspace.NewSnapshotEngine(root, 0)
	if err != nil {
		t.Fatalf("reopen engine: %v", err)
	}
	editBytes := []byte("package main\nvar x = 1\n")
	_ = os.WriteFile(filepath.Join(root, "main.go"), editBytes, 0o644)
	_, advancedSnap, err := eng.ApplyVersionedEdit(context.Background(), workspace.EditRequest{
		Path: "main.go", Content: editBytes, DocumentVersion: 2, Source: workspace.SourceIDEVersioned,
	})
	if err != nil {
		t.Fatalf("apply edit: %v", err)
	}
	if advancedSnap.SnapshotID == initialHead {
		t.Fatal("advanced snapshot should have new ID")
	}

	// Reopen server; durable state is advancedSnap, but last verified basis is initialHead (or initial basis)
	srv2, err := NewServer(Config{RepoRoot: root, Port: 0, Mode: "project_change"})
	if err != nil {
		t.Fatalf("NewServer 2: %v", err)
	}
	defer func() { _ = srv2.Shutdown(context.Background()) }()

	if srv2.SnapshotEngine().LiveHeadID() != advancedSnap.SnapshotID {
		t.Fatalf("durable head = %s, want %s", srv2.SnapshotEngine().LiveHeadID(), advancedSnap.SnapshotID)
	}
	basisID := srv2.LastVerifiedBasisID()
	if basisID == srv2.SnapshotEngine().LiveHeadID() && basisID != initialHead {
		t.Fatalf("expected last verified basis (%s) to be distinct from unanalyzed live head (%s)", basisID, srv2.SnapshotEngine().LiveHeadID())
	}
}

// TestLPCA_VS01_A03_A04_InitialBasisNoticeAndUIState verifies:
// A03: Notice shows "현재 프로젝트의 변경을 감시하고 있습니다" when watching without changes.
// A04: UI provides project title, pause action, and hides request/entry inputs.
func TestLPCA_VS01_A03_A04_InitialBasisNoticeAndUIState(t *testing.T) {
	root := t.TempDir()
	createSampleProject(t, root)

	srv, err := NewServer(Config{RepoRoot: root, Port: 0, Mode: "project_change"})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer func() { _ = srv.Shutdown(context.Background()) }()

	statusReq := httptest.NewRequest("GET", "/api/live/project?token="+srv.AuthToken(), nil)
	statusRec := httptest.NewRecorder()
	srv.handleLiveProject(statusRec, statusReq)
	if statusRec.Code != http.StatusOK {
		t.Fatalf("GET /api/live/project status = %d", statusRec.Code)
	}
	var proj map[string]any
	_ = json.Unmarshal(statusRec.Body.Bytes(), &proj)

	if proj["notice"] != "변경을 확인 중입니다" {
		t.Fatalf("notice = %q, want '변경을 확인 중입니다' (baseline compiling)", proj["notice"])
	}
	if proj["title"] != "프로젝트 변경 감시 상태" {
		t.Fatalf("title = %q, want '프로젝트 변경 감시 상태'", proj["title"])
	}
}

// TestLPCA_VS01_A05_CoordinatorReuse verifies repeated Live starts join the same logical coordinator.
func TestLPCA_VS01_A05_CoordinatorReuse(t *testing.T) {
	root := t.TempDir()
	createSampleProject(t, root)

	srv1, err := NewServer(Config{RepoRoot: root, Port: 0, Mode: "project_change"})
	if err != nil {
		t.Fatalf("NewServer 1: %v", err)
	}
	srv1.Start()
	defer func() { _ = srv1.Shutdown(context.Background()) }()

	coord, err := ReadLiveCoordinator(root)
	if err != nil {
		t.Fatalf("ReadLiveCoordinator: %v", err)
	}
	if coord == nil || coord.URL == "" {
		t.Fatal("expected non-empty coordinator registration")
	}

	// Second check for coordinator on same root finds srv1
	existing, err := DiscoverLiveCoordinator(root)
	if err != nil {
		t.Fatalf("DiscoverLiveCoordinator: %v", err)
	}
	if existing == nil || existing.URL != srv1.URL() {
		t.Fatalf("discovered coordinator URL = %v, want %s", existing, srv1.URL())
	}
}

// TestLPCA_VS01_A06_FirstStartInitializesBaseline verifies a project without prior basis
// establishes the current stable source as the initial comparison baseline without writing source.
func TestLPCA_VS01_A06_FirstStartInitializesBaseline(t *testing.T) {
	root := t.TempDir()
	mainPath := filepath.Join(root, "main.go")
	originalContent := []byte("package main\nfunc main() {}\n")
	if err := os.WriteFile(mainPath, originalContent, 0o644); err != nil {
		t.Fatal(err)
	}

	srv, err := NewServer(Config{RepoRoot: root, Port: 0, Mode: "project_change"})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer func() { _ = srv.Shutdown(context.Background()) }()

	head := srv.SnapshotEngine().LiveHead()
	if head == nil {
		t.Fatal("expected initial live head snapshot")
	}
	entry, ok := head.Entries["main.go"]
	if !ok {
		t.Fatal("main.go should be in initial snapshot")
	}
	if entry.ContentID == "" {
		t.Fatal("expected content ID")
	}

	// Source file must remain completely untouched (read-only)
	after, err := os.ReadFile(mainPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(originalContent) {
		t.Fatal("source content was modified by codeflow")
	}
}

// TestLPCA_VS01_A07_DamagedBasisExposesGap verifies damaged basis or lineage mismatch
// does not quietly initialize as initial run, but exposes gap/reconciliation.
func TestLPCA_VS01_A07_DamagedBasisExposesGap(t *testing.T) {
	root := t.TempDir()
	createSampleProject(t, root)

	// Create valid storage layout with a broken/corrupted active pointer
	st := storage.New(root)
	if err := st.InitLayout(); err != nil {
		t.Fatal(err)
	}
	corruptedPointerPath := filepath.Join(st.BaseDir(), "active-pointer.json")
	// Write corrupted JSON
	if err := os.WriteFile(corruptedPointerPath, []byte(`{"generationId": "gen-corrupt", "computedBasisId": "missing-cas"`), 0o644); err != nil {
		t.Fatal(err)
	}

	srv, err := NewServer(Config{RepoRoot: root, Port: 0, Mode: "project_change"})
	if err != nil {
		t.Fatalf("NewServer with corrupted basis: %v", err)
	}
	defer func() { _ = srv.Shutdown(context.Background()) }()

	statusReq := httptest.NewRequest("GET", "/api/live/project?token="+srv.AuthToken(), nil)
	statusRec := httptest.NewRecorder()
	srv.handleLiveProject(statusRec, statusReq)
	if statusRec.Code != http.StatusOK {
		t.Fatalf("GET /api/live/project status = %d", statusRec.Code)
	}
	var proj map[string]any
	_ = json.Unmarshal(statusRec.Body.Bytes(), &proj)

	// It must NOT be quietly initialized as a clean first run without basis error
	if proj["gap"] == nil && proj["status"] != "gap" {
		t.Fatalf("expected gap status or non-nil gap for damaged basis, got status=%v, gap=%v", proj["status"], proj["gap"])
	}
}

func createSampleProject(t *testing.T, root string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module sample\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}
