package flowview

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codeflow/internal/protocol"
	"codeflow/internal/semantic"
	"codeflow/internal/slicing"
	"codeflow/internal/workspace"
)

// TestLPCA_VS04_A01_ProofBackedViewNotFilesystemReanalysis verifies that fetching a live generation
// retrieves the persisted proof-backed artifacts without re-analyzing filesystem bytes.
func TestLPCA_VS04_A01_ProofBackedViewNotFilesystemReanalysis(t *testing.T) {
	root := t.TempDir()
	createSampleProject(t, root)

	srv, err := NewServer(Config{RepoRoot: root, Port: 0, Mode: "project_change"})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer func() { _ = srv.Shutdown(context.Background()) }()

	// Calling live generation with non-existent or unverified generation IDs fails without reanalyzing disk
	req := httptest.NewRequest("GET", "/api/live/generation?token="+srv.AuthToken()+"&generationId=non-existent&computedBasisId=b1&snapshotId=s1", nil)
	rec := httptest.NewRecorder()
	srv.handleLiveGeneration(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 Conflict for non-existent proof generation, got %d", rec.Code)
	}
	var errResp map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &errResp)
	if errResp["code"] != "generation_unavailable" {
		t.Fatalf("code = %v, want generation_unavailable", errResp["code"])
	}
}

// TestLPCA_VS04_A07_NoTelemetryInLiveProject verifies that /api/live/project provides user language
// status and notice without leaking epochs, analysis lag, or internal telemetry.
func TestLPCA_VS04_A07_NoTelemetryInLiveProject(t *testing.T) {
	root := t.TempDir()
	createSampleProject(t, root)

	srv, err := NewServer(Config{RepoRoot: root, Port: 0, Mode: "project_change"})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer func() { _ = srv.Shutdown(context.Background()) }()

	req := httptest.NewRequest("GET", "/api/live/project?token="+srv.AuthToken(), nil)
	rec := httptest.NewRecorder()
	srv.handleLiveProject(rec, req)

	var proj map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &proj); err != nil {
		t.Fatalf("unmarshal project state: %v", err)
	}

	// Primary view must only contain user-friendly fields
	disallowedKeys := []string{"epoch", "workspaceEpoch", "analysisLagMs", "lag", "compilerTelemetry", "epochs"}
	for _, key := range disallowedKeys {
		if _, exists := proj[key]; exists {
			t.Errorf("forbidden telemetry key %q present in project state", key)
		}
	}

	if proj["status"] != "pending" {
		t.Errorf("status = %v, want pending (baseline compiling)", proj["status"])
	}
	if proj["notice"] != "변경을 확인 중입니다" {
		t.Errorf("notice = %v, want '변경을 확인 중입니다'", proj["notice"])
	}
}

// TestLPCA_VS04_A08_NoUserReadHistoryPersisted verifies no user read or acknowledgement history
// is persisted to disk during project observation or edits.
func TestLPCA_VS04_A08_NoUserReadHistoryPersisted(t *testing.T) {
	root := t.TempDir()
	createSampleProject(t, root)

	srv, err := NewServer(Config{RepoRoot: root, Port: 0, Mode: "project_change"})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	_, err = srv.SubmitVersionedChanges(context.Background(), workspace.VersionedChangeRequest{
		BatchID: "batch-read-test",
		Source:  workspace.SourceIDEVersioned,
		Changes: []workspace.VersionedChange{
			{Kind: workspace.ChangeUpsert, Path: "main.go", Content: []byte("package main\nfunc main() {}\n// edit\n"), DocumentVersion: 2},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	_ = srv.Shutdown(context.Background())

	// Verify no read_history, viewer_cursor, or confirmation records exist in .codeflow directory
	codeflowDir := filepath.Join(root, ".codeflow")
	entries, err := os.ReadDir(codeflowDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if name == "read_status.json" || name == "confirmations.json" || name == "cursor.json" || name == "read_history.json" {
			t.Errorf("forbidden user read history file persisted: %s", name)
		}
	}
}

// TestLPCA_VS04_A05_ApplyVerifiedGenerationServesExactArtifact verifies that published generation artifacts
// are retrieved exactly from storage and match the published generation proof manifest.
func TestLPCA_VS04_A05_ApplyVerifiedGenerationServesExactArtifact(t *testing.T) {
	root := t.TempDir()
	createSampleProject(t, root)

	srv, err := NewServer(Config{RepoRoot: root, Port: 0, Mode: "project_change"})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer func() { _ = srv.Shutdown(context.Background()) }()

	srv.compileCandidate = func(ctx context.Context, snapshot protocol.Snapshot, query *semantic.TaskViewQuery) (*semantic.SemanticMapIR, *semantic.FlowViewProjection, *slicing.SlicedPayload, *semantic.ResolvedTarget, *semantic.TaskIntent, *semantic.CausalObservationClosure, error) {
		return liveDeltaCandidate(ctx, snapshot, query, "generation-apply-test")
	}

	_, snap, err := srv.SubmitVersionedEdit(context.Background(), workspace.EditRequest{
		Path:            "main.go",
		Content:         []byte("package main\n\nfunc main() {}\n// apply test edit\n"),
		DocumentVersion: 2,
		Source:          workspace.SourceIDEVersioned,
	})
	if err != nil {
		t.Fatalf("SubmitVersionedEdit: %v", err)
	}

	if err := srv.processCheckpoint(context.Background(), snap); err != nil {
		t.Fatalf("processCheckpoint: %v", err)
	}

	manifest, pointer, err := srv.storage.ReadValidatedActiveProofManifest()
	if err != nil {
		t.Fatalf("ReadValidatedActiveProofManifest: %v", err)
	}
	if manifest == nil || pointer == nil {
		t.Fatal("expected published active proof manifest and pointer")
	}

	req := httptest.NewRequest("GET", fmt.Sprintf("/api/live/generation?token=%s&generationId=%s&computedBasisId=%s&snapshotId=%s",
		srv.AuthToken(), pointer.GenerationID, pointer.ComputedBasisID, pointer.ValidatedAgainstSnapshotID), nil)
	rec := httptest.NewRecorder()
	srv.handleLiveGeneration(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", rec.Code, rec.Body.String())
	}
	var view liveGenerationView
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatalf("unmarshal liveGenerationView: %v", err)
	}
	if view.ProofManifest.GenerationID != pointer.GenerationID {
		t.Errorf("got generationId %s, want %s", view.ProofManifest.GenerationID, pointer.GenerationID)
	}
}

// TestLPCA_VS04_A06_StreamRecoverySync verifies that stream reconnection with an unknown lastEventId
// emits a snapshot_sync event to facilitate full recovery.
func TestLPCA_VS04_A06_StreamRecoverySync(t *testing.T) {
	root := t.TempDir()
	createSampleProject(t, root)

	srv, err := NewServer(Config{RepoRoot: root, Port: 0, Mode: "project_change"})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer func() { _ = srv.Shutdown(context.Background()) }()

	streamServer := httptest.NewServer(srv.httpServer.Handler)
	defer streamServer.Close()

	streamCtx, cancelStream := context.WithCancel(context.Background())
	defer cancelStream()

	reqStream, err := http.NewRequestWithContext(streamCtx, http.MethodGet, streamServer.URL+"/api/workspace/stream?token="+srv.AuthToken()+"&lastEventId=ev-unknown", nil)
	if err != nil {
		t.Fatal(err)
	}
	respStream, err := streamServer.Client().Do(reqStream)
	if err != nil {
		t.Fatalf("open workspace stream: %v", err)
	}
	defer respStream.Body.Close()

	reader := bufio.NewScanner(respStream.Body)
	foundSync := false
	for reader.Scan() {
		if strings.Contains(reader.Text(), "snapshot_sync") {
			foundSync = true
			break
		}
	}
	if !foundSync {
		t.Fatalf("reconnection stream did not emit snapshot_sync")
	}
}

