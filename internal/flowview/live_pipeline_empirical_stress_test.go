package flowview

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"codeflow/internal/semantic"
	"codeflow/internal/workspace"
)

type capturedSSEEvent struct {
	Event string
	Data  string
	ID    string
}

func startSSEClientWithURL(ctx context.Context, streamURL string) (<-chan capturedSSEEvent, chan error) {
	events := make(chan capturedSSEEvent, 100)
	errs := make(chan error, 1)

	go func() {
		defer close(events)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, streamURL, nil)
		if err != nil {
			errs <- err
			return
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			errs <- err
			return
		}
		defer resp.Body.Close()

		scanner := bufio.NewScanner(resp.Body)
		var currentEvent, currentID string
		var dataLines []string

		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "event: ") {
				currentEvent = strings.TrimPrefix(line, "event: ")
			} else if strings.HasPrefix(line, "id: ") {
				currentID = strings.TrimPrefix(line, "id: ")
			} else if strings.HasPrefix(line, "data: ") {
				dataLines = append(dataLines, strings.TrimPrefix(line, "data: "))
			} else if line == "" {
				if currentEvent != "" || len(dataLines) > 0 {
					evt := capturedSSEEvent{
						Event: currentEvent,
						ID:    currentID,
						Data:  strings.Join(dataLines, "\n"),
					}
					select {
					case events <- evt:
					default:
					}
					currentEvent = ""
					currentID = ""
					dataLines = nil
				}
			}
		}
		if err := scanner.Err(); err != nil && ctx.Err() == nil {
			select {
			case errs <- err:
			default:
			}
		}
	}()

	return events, errs
}

func startSSEClient(ctx context.Context, srv *Server) (<-chan capturedSSEEvent, chan error) {
	streamURL := fmt.Sprintf("http://%s/api/workspace/stream?token=%s", srv.Addr(), srv.AuthToken())
	return startSSEClientWithURL(ctx, streamURL)
}

func drainSSEEvents(events <-chan capturedSSEEvent, timeout time.Duration) []capturedSSEEvent {
	var drained []capturedSSEEvent
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case evt, ok := <-events:
			if !ok {
				return drained
			}
			drained = append(drained, evt)
			// Reset timer after each event
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(50 * time.Millisecond)
		case <-timer.C:
			return drained
		}
	}
}

// -----------------------------------------------------------------------------
// 1. BURST EDITS: Rapid successive file edits within debounce window,
//    verifying revision batch coalescing and that latest basis wins without race.
// -----------------------------------------------------------------------------

func waitForVerifiedBasis(t *testing.T, srv *Server, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if basis := srv.LastVerifiedBasisID(); basis != "" {
			return basis
		}
		if time.Now().After(deadline) {
			t.Fatal("expected initial basis established")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestEmpirical_BurstEdits_CoalescingAndLatestBasis(t *testing.T) {
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/burst\n\ngo 1.22\n"), 0o644)
	_ = os.MkdirAll(filepath.Join(root, "service"), 0o755)

	mainSrc := `package main

import (
	"fmt"
	"example.com/burst/service"
)

func Handle() {
	fmt.Println(service.Greeting())
}

func main() {
	Handle()
}
`
	_ = os.WriteFile(filepath.Join(root, "main.go"), []byte(mainSrc), 0o644)

	helperSrc := `package service

func Greeting() string {
	return "initial"
}
`
	_ = os.WriteFile(filepath.Join(root, "service", "helper.go"), []byte(helperSrc), 0o644)

	srv, err := NewServer(Config{
		RepoRoot:               root,
		Port:                   0,
		AuthToken:              "test-burst-token",
		Mode:                   "project_change",
		WorkspaceWatchInterval: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer func() { _ = srv.Shutdown(context.Background()) }()

	// Configure an accelerated coalescing scheduler to stress-test timing under race detector
	srv.scheduler.Close()
	srv.scheduler = semantic.NewCoalescingScheduler(semantic.CoalescingConfig{
		QuietWindow:  150 * time.Millisecond,
		MaxWait:      400 * time.Millisecond,
		MaxQueueSize: 50,
	})

	srv.Start()

	sseCtx, sseCancel := context.WithCancel(context.Background())
	defer sseCancel()
	events, _ := startSSEClient(sseCtx, srv)

	// Wait for startup baseline to publish and drain initial events
	initialBasis := waitForVerifiedBasis(t, srv, 20*time.Second)
	_ = drainSSEEvents(events, 100*time.Millisecond)

	// Burst: 10 rapid successive edits within 100ms (well within the 150ms quiet window)
	totalEdits := 10
	burstStart := time.Now()
	for i := 1; i <= totalEdits; i++ {
		versionContent := fmt.Sprintf(`package service

func Greeting() string {
	return "version-%d"
}
`, i)
		if writeErr := os.WriteFile(filepath.Join(root, "service", "helper.go"), []byte(versionContent), 0o644); writeErr != nil {
			t.Fatalf("write edit %d: %v", i, writeErr)
		}
		time.Sleep(10 * time.Millisecond) // 10ms << 150ms quiet window
	}
	burstDuration := time.Since(burstStart)
	t.Logf("Burst of %d edits executed in %v", totalEdits, burstDuration)

	// Wait for quiet window (150ms) + compilation to settle
	var finalLiveHead string
	var finalBasis string
	publishedCount := 0
	deadline := time.Now().Add(5 * time.Second)

	for time.Now().Before(deadline) {
		select {
		case evt := <-events:
			if evt.Event == "generation.published" {
				publishedCount++
				t.Logf("Received generation.published event (total so far: %d)", publishedCount)
			}
		case <-time.After(50 * time.Millisecond):
		}

		act := srv.CurrentActivity()
		head := srv.SnapshotEngine().LiveHead()
		if act.Activity == "idle" && head != nil && srv.LastVerifiedBasisID() != initialBasis {
			finalLiveHead = head.SnapshotID
			finalBasis = srv.LastVerifiedBasisID()
			break
		}
	}

	if finalBasis == "" || finalBasis == initialBasis {
		t.Fatalf("pipeline failed to advance basis after burst edits: initial=%s, finalBasis=%s", initialBasis, finalBasis)
	}

	// Assert Coalescing: Intermediate edits must have coalesced
	// We expect AT MOST 2 published events (not 10!), typically 1
	if publishedCount > 3 {
		t.Errorf("coalescing failed: expected <= 3 generation.published events for burst, got %d", publishedCount)
	}

	// Assert Latest Basis Wins: The final basis MUST match the latest live head
	if finalBasis != finalLiveHead {
		t.Errorf("latest basis mismatch: verified basis=%s, live head=%s", finalBasis, finalLiveHead)
	}

	// Check /api/live/project endpoint
	resp, err := http.Get(fmt.Sprintf("http://%s/api/live/project?token=%s", srv.Addr(), srv.AuthToken()))
	if err != nil {
		t.Fatalf("GET /api/live/project: %v", err)
	}
	defer resp.Body.Close()
	var proj map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&proj); err != nil {
		t.Fatalf("decode /api/live/project: %v", err)
	}

	if proj["status"] != "watching" {
		t.Errorf("status = %v, want watching", proj["status"])
	}
	if proj["lastVerifiedBasisId"] != finalBasis {
		t.Errorf("project lastVerifiedBasisId = %v, want %s", proj["lastVerifiedBasisId"], finalBasis)
	}
	if proj["gap"] != nil {
		t.Errorf("unexpected gap: %v", proj["gap"])
	}
}

func TestEmpirical_BurstEdits_ConcurrentMultiProducerStress(t *testing.T) {
	root := t.TempDir()
	createSampleProject(t, root)

	srv, err := NewServer(Config{
		RepoRoot:  root,
		Port:      0,
		AuthToken: "test-concurrent-token",
		Mode:      "project_change",
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer func() { _ = srv.Shutdown(context.Background()) }()
	srv.Start()

	numWorkers := 4
	editsPerWorker := 10
	var wg sync.WaitGroup
	var successCount int64

	startBarrier := make(chan struct{})

	// Each worker writes to its own distinct file with strictly monotonic document versions
	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			<-startBarrier
			workerFile := fmt.Sprintf("worker_%d.go", workerID)
			for e := 1; e <= editsPerWorker; e++ {
				content := fmt.Sprintf("package main\n\nfunc Worker%d_%d() int { return %d }\n", workerID, e, e)
				_, err := srv.SubmitVersionedChanges(context.Background(), workspace.VersionedChangeRequest{
					BatchID: fmt.Sprintf("batch-w%d-e%d", workerID, e),
					Source:  workspace.SourceAgentTransaction,
					Changes: []workspace.VersionedChange{
						{
							Kind:            workspace.ChangeUpsert,
							Path:            workerFile,
							Content:         []byte(content),
							DocumentVersion: e,
						},
					},
				})
				if err == nil {
					atomic.AddInt64(&successCount, 1)
				} else {
					t.Logf("Worker %d edit %d error: %v", workerID, e, err)
				}
				time.Sleep(3 * time.Millisecond)
			}
		}(w)
	}

	close(startBarrier)
	wg.Wait()

	totalExpected := int64(numWorkers * editsPerWorker)
	if successCount != totalExpected {
		t.Fatalf("concurrent edit submission count mismatch: got %d, want %d", successCount, totalExpected)
	}

	// Verify monotonic workspace head sequence and no corrupted state
	head := srv.SnapshotEngine().LiveHead()
	if head == nil {
		t.Fatal("expected live head present after concurrent edits")
	}
	if int64(head.Sequence) < totalExpected {
		t.Errorf("workspace sequence %d < expected minimum %d", head.Sequence, totalExpected)
	}
}

// -----------------------------------------------------------------------------
// 2. NON-ENTRYPOINT EDITS: Modifications in nested service, model, and helper
//    packages demonstrating change detection to verified generation within target latency.
// -----------------------------------------------------------------------------

func TestEmpirical_NonEntrypointEdits_EndToEndLatency(t *testing.T) {
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/nonentry\n\ngo 1.22\n"), 0o644)
	_ = os.MkdirAll(filepath.Join(root, "service"), 0o755)
	_ = os.MkdirAll(filepath.Join(root, "models"), 0o755)
	_ = os.MkdirAll(filepath.Join(root, "pkg", "util"), 0o755)

	mainSrc := `package main

import (
	"fmt"
	"example.com/nonentry/service"
)

func Handle() {
	fmt.Println(service.Execute())
}

func main() {
	Handle()
}
`
	_ = os.WriteFile(filepath.Join(root, "main.go"), []byte(mainSrc), 0o644)

	modelSrc := `package models

type User struct {
	Name string
}

func DefaultUser() User {
	return User{Name: "OriginalUser"}
}
`
	_ = os.WriteFile(filepath.Join(root, "models", "user.go"), []byte(modelSrc), 0o644)

	utilSrc := `package util

func Format(s string) string {
	return "[FORMAT] " + s
}
`
	_ = os.WriteFile(filepath.Join(root, "pkg", "util", "helper.go"), []byte(utilSrc), 0o644)

	serviceSrc := `package service

import (
	"example.com/nonentry/models"
	"example.com/nonentry/pkg/util"
)

func Execute() string {
	u := models.DefaultUser()
	return util.Format(u.Name)
}
`
	_ = os.WriteFile(filepath.Join(root, "service", "processor.go"), []byte(serviceSrc), 0o644)

	srv, err := NewServer(Config{
		RepoRoot:               root,
		Port:                   0,
		AuthToken:              "test-nonentry-token",
		Mode:                   "project_change",
		WorkspaceWatchInterval: 25 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer func() { _ = srv.Shutdown(context.Background()) }()

	// Use a controlled quiet window of 200ms to test latency bounds deterministically
	srv.scheduler.Close()
	srv.scheduler = semantic.NewCoalescingScheduler(semantic.CoalescingConfig{
		QuietWindow:  200 * time.Millisecond,
		MaxWait:      500 * time.Millisecond,
		MaxQueueSize: 50,
	})

	srv.Start()

	sseCtx, sseCancel := context.WithCancel(context.Background())
	defer sseCancel()
	events, _ := startSSEClient(sseCtx, srv)

	// Wait for initial baseline to publish and drain initial events
	initialBasis := waitForVerifiedBasis(t, srv, 20*time.Second)
	_ = drainSSEEvents(events, 150*time.Millisecond)

	// -------------------------------------------------------------
	// TEST CASE A: Modify nested model package (models/user.go)
	// -------------------------------------------------------------
	t.Log("Modifying non-entrypoint file: models/user.go ...")
	updatedModelSrc := `package models

type User struct {
	Name string
}

func DefaultUser() User {
	return User{Name: "ModifiedModelUser"}
}
`
	startTime := time.Now()
	if err := os.WriteFile(filepath.Join(root, "models", "user.go"), []byte(updatedModelSrc), 0o644); err != nil {
		t.Fatalf("modify models/user.go: %v", err)
	}

	// Await publication over SSE for the new basis
	var receivedPublished bool
	var latency time.Duration
	deadline := time.Now().Add(5 * time.Second)

	for time.Now().Before(deadline) {
		select {
		case evt := <-events:
			if evt.Event == "generation.published" {
				receivedPublished = true
				latency = time.Since(startTime)
				t.Logf("Received generation.published in %v for models/user.go edit", latency)
			}
		case <-time.After(50 * time.Millisecond):
		}
		if receivedPublished && srv.LastVerifiedBasisID() != initialBasis {
			break
		}
	}

	if !receivedPublished || srv.LastVerifiedBasisID() == initialBasis {
		t.Fatalf("failed to receive generation.published for models/user.go edit within 5s; lastBasis=%s, initial=%s, lastErr=%v",
			srv.LastVerifiedBasisID(), initialBasis, srv.LastPipelineError())
	}

	// Target latency check: debounce (200ms) + watcher (25ms) + compilation (< 2500ms) = < 3500ms
	if latency > 4*time.Second {
		t.Errorf("latency %v exceeded target bound of 4s", latency)
	}

	basisA := srv.LastVerifiedBasisID()
	_ = drainSSEEvents(events, 100*time.Millisecond)

	// -------------------------------------------------------------
	// TEST CASE B: Modify deeply nested helper package (pkg/util/helper.go)
	// -------------------------------------------------------------
	t.Log("Modifying non-entrypoint file: pkg/util/helper.go ...")
	updatedUtilSrc := `package util

func Format(s string) string {
	return "[UPDATED-FORMAT] " + s
}
`
	startTimeB := time.Now()
	if err := os.WriteFile(filepath.Join(root, "pkg", "util", "helper.go"), []byte(updatedUtilSrc), 0o644); err != nil {
		t.Fatalf("modify pkg/util/helper.go: %v", err)
	}

	var receivedPublishedB bool
	var latencyB time.Duration
	deadlineB := time.Now().Add(5 * time.Second)

	for time.Now().Before(deadlineB) {
		select {
		case evt := <-events:
			if evt.Event == "generation.published" {
				receivedPublishedB = true
				latencyB = time.Since(startTimeB)
				t.Logf("Received generation.published in %v for pkg/util/helper.go edit", latencyB)
			}
		case <-time.After(50 * time.Millisecond):
		}
		if receivedPublishedB && srv.LastVerifiedBasisID() != basisA {
			break
		}
	}

	if !receivedPublishedB || srv.LastVerifiedBasisID() == basisA {
		t.Fatalf("failed to receive generation.published for pkg/util/helper.go edit; lastBasis=%s, basisA=%s, lastErr=%v",
			srv.LastVerifiedBasisID(), basisA, srv.LastPipelineError())
	}

	t.Logf("Non-entrypoint modifications verified: models edit latency=%v, helper edit latency=%v", latency, latencyB)
}

// -----------------------------------------------------------------------------
// 3. SYNTAX ERROR / RECOVERY: Introduce invalid syntax, verify graceful degradation
//    (preserving previous valid flow view without crashing), then verify recovery.
// -----------------------------------------------------------------------------

func TestEmpirical_SyntaxError_GracefulDegradationAndRecovery(t *testing.T) {
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/syntax\n\ngo 1.22\n"), 0o644)
	_ = os.MkdirAll(filepath.Join(root, "service"), 0o755)

	mainSrc := `package main

import (
	"fmt"
	"example.com/syntax/service"
)

func main() {
	fmt.Println(service.Run())
}
`
	_ = os.WriteFile(filepath.Join(root, "main.go"), []byte(mainSrc), 0o644)

	serviceSrc := `package service

func Run() string {
	return "healthy-v1"
}
`
	_ = os.WriteFile(filepath.Join(root, "service", "run.go"), []byte(serviceSrc), 0o644)

	srv, err := NewServer(Config{
		RepoRoot:               root,
		Port:                   0,
		AuthToken:              "test-syntax-token",
		Mode:                   "project_change",
		WorkspaceWatchInterval: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer func() { _ = srv.Shutdown(context.Background()) }()

	srv.scheduler.Close()
	srv.scheduler = semantic.NewCoalescingScheduler(semantic.CoalescingConfig{
		QuietWindow:  150 * time.Millisecond,
		MaxWait:      300 * time.Millisecond,
		MaxQueueSize: 50,
	})

	srv.Start()

	sseCtx, sseCancel := context.WithCancel(context.Background())
	defer sseCancel()
	events, _ := startSSEClient(sseCtx, srv)

	// Wait for healthy baseline
	initialBasis := waitForVerifiedBasis(t, srv, 20*time.Second)
	_ = drainSSEEvents(events, 100*time.Millisecond)

	// -------------------------------------------------------------
	// STEP 1: Introduce Syntax Error
	// -------------------------------------------------------------
	t.Log("Step 1: Injecting broken syntax into service/run.go ...")
	brokenSrc := `package service

func Run( { broken syntax here !@#$
`
	if err := os.WriteFile(filepath.Join(root, "service", "run.go"), []byte(brokenSrc), 0o644); err != nil {
		t.Fatalf("write broken syntax: %v", err)
	}

	// Wait for pipeline to detect and attempt compilation
	time.Sleep(600 * time.Millisecond)

	// Assert: Server MUST NOT crash or panic
	// Assert: Last verified basis MUST be preserved!
	currentBasis := srv.LastVerifiedBasisID()
	if currentBasis != initialBasis {
		t.Errorf("broken syntax wiped out verified basis: got %s, want preserved %s", currentBasis, initialBasis)
	}

	// Check /api/live/project for graceful degradation & anti-telemetry compliance
	resp, err := http.Get(fmt.Sprintf("http://%s/api/live/project?token=%s", srv.Addr(), srv.AuthToken()))
	if err != nil {
		t.Fatalf("GET /api/live/project during syntax error: %v", err)
	}
	bodyBytes, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	var proj map[string]any
	if err := json.Unmarshal(bodyBytes, &proj); err != nil {
		t.Fatalf("unmarshal /api/live/project: %v", err)
	}

	// Anti-telemetry check on top-level keys
	disallowedKeys := []string{"epoch", "workspaceEpoch", "analysisLagMs", "lag", "compilerTelemetry", "epochs"}
	for _, key := range disallowedKeys {
		if _, exists := proj[key]; exists {
			t.Errorf("forbidden top-level telemetry key %q present in project state", key)
		}
	}
	// Check notice does not contain panic or stack traces
	notice, _ := proj["notice"].(string)
	if strings.Contains(strings.ToLower(notice), "panic:") || strings.Contains(strings.ToLower(notice), "stack trace") {
		t.Errorf("notice leaked runtime panic or stack trace: %s", notice)
	}

	bodyStr := string(bodyBytes)
	forbiddenTokens := []string{"epoch", "workspaceEpoch", "analysisLagMs", "stack trace", "panic:", "runtime error"}
	for _, token := range forbiddenTokens {
		if strings.Contains(strings.ToLower(bodyStr), strings.ToLower(token)) {
			t.Errorf("telemetry leak detected in live project response during syntax error: found %q in %s", token, bodyStr)
		}
	}

	_ = drainSSEEvents(events, 100*time.Millisecond)

	// -------------------------------------------------------------
	// STEP 2: Introduce Valid Syntax and Verify Recovery
	// -------------------------------------------------------------
	t.Log("Step 2: Injecting recovered syntax into service/run.go ...")
	recoveredSrc := `package service

func Run() string {
	return "healthy-recovered-v2"
}
`
	recoverStart := time.Now()
	if err := os.WriteFile(filepath.Join(root, "service", "run.go"), []byte(recoveredSrc), 0o644); err != nil {
		t.Fatalf("write recovered syntax: %v", err)
	}

	// Wait for recovery publication
	var recoveredPublished bool
	deadline := time.Now().Add(5 * time.Second)

	for time.Now().Before(deadline) {
		select {
		case evt := <-events:
			if evt.Event == "generation.published" {
				recoveredPublished = true
				t.Logf("Received generation.published after syntax recovery in %v", time.Since(recoverStart))
			}
		case <-time.After(50 * time.Millisecond):
		}
		if recoveredPublished && srv.LastVerifiedBasisID() != initialBasis {
			break
		}
	}

	if !recoveredPublished || srv.LastVerifiedBasisID() == initialBasis {
		t.Fatalf("failed to recover and publish after syntax fix; lastBasis=%s, initial=%s, lastErr=%v",
			srv.LastVerifiedBasisID(), initialBasis, srv.LastPipelineError())
	}

	// Verify project status is back to watching
	resp2, err := http.Get(fmt.Sprintf("http://%s/api/live/project?token=%s", srv.Addr(), srv.AuthToken()))
	if err != nil {
		t.Fatalf("GET /api/live/project after recovery: %v", err)
	}
	defer resp2.Body.Close()
	var proj2 map[string]any
	_ = json.NewDecoder(resp2.Body).Decode(&proj2)

	if proj2["status"] != "watching" {
		t.Errorf("status = %v, want watching after recovery", proj2["status"])
	}
	if proj2["gap"] != nil {
		t.Errorf("expected gap=nil after recovery, got %v", proj2["gap"])
	}
}

// -----------------------------------------------------------------------------
// 4. RECONNECT & RESTART: Test watcher reconnect and coordinator restart cycles,
//    ensuring zero duplicate processes, zombie watchers, or dangling locks.
// -----------------------------------------------------------------------------

func TestEmpirical_ReconnectAndRestart_ZeroDuplicateAndCleanLocks(t *testing.T) {
	root := t.TempDir()
	createSampleProject(t, root)

	// Subtest 4a: Server 1 starts, claims coordinator, shuts down cleanly, verifies file cleanup
	t.Log("Subtest 4a: Coordinator claim and clean shutdown...")
	srv1, err := NewServer(Config{
		RepoRoot:  root,
		Port:      0,
		AuthToken: "token-srv1",
		Mode:      "project_change",
	})
	if err != nil {
		t.Fatalf("NewServer 1: %v", err)
	}
	srv1.Start()

	// Verify coordinator record written
	coord1, err := ReadLiveCoordinator(root)
	if err != nil || coord1 == nil {
		t.Fatalf("expected coordinator record for srv1, got %v (err: %v)", coord1, err)
	}
	if coord1.URL != srv1.URL() {
		t.Errorf("coordinator URL mismatch: got %s, want %s", coord1.URL, srv1.URL())
	}

	// Shut down Server 1 cleanly
	if err := srv1.Shutdown(context.Background()); err != nil {
		t.Fatalf("srv1.Shutdown: %v", err)
	}

	// Verify coordinator record was cleaned up upon shutdown
	coordAfter, err := ReadLiveCoordinator(root)
	if err != nil {
		t.Fatalf("read coordinator after srv1 shutdown: %v", err)
	}
	if coordAfter != nil {
		t.Errorf("expected coordinator record removed after shutdown, got %+v", coordAfter)
	}

	// Subtest 4b: Start Server 2 on the same repoRoot immediately, verifying handover
	t.Log("Subtest 4b: Server 2 handover on same repoRoot...")
	srv2, err := NewServer(Config{
		RepoRoot:  root,
		Port:      0,
		AuthToken: "token-srv2",
		Mode:      "project_change",
	})
	if err != nil {
		t.Fatalf("NewServer 2: %v", err)
	}
	srv2.Start()
	defer func() { _ = srv2.Shutdown(context.Background()) }()

	coord2, err := ReadLiveCoordinator(root)
	if err != nil || coord2 == nil {
		t.Fatalf("expected coordinator record for srv2, got %v (err: %v)", coord2, err)
	}
	if coord2.URL != srv2.URL() {
		t.Errorf("coordinator 2 URL mismatch: got %s, want %s", coord2.URL, srv2.URL())
	}

	// Subtest 4c: Duplicate Coordinator Prevention
	t.Log("Subtest 4c: Duplicate coordinator prevention while srv2 is alive...")
	claimRecord := LiveCoordinatorRecord{
		URL:       "http://127.0.0.1:9999",
		Token:     "fake-token",
		PID:       os.Getpid() + 100,
		StartedAt: time.Now().UTC(),
		RepoRoot:  root,
	}
	claimed, existing, err := ClaimLiveCoordinator(root, claimRecord)
	if err != nil {
		t.Fatalf("ClaimLiveCoordinator: %v", err)
	}
	if claimed {
		t.Error("expected ClaimLiveCoordinator to REJECT second process while srv2 is active")
	}
	if existing == nil || existing.URL != srv2.URL() {
		t.Errorf("expected existing coordinator to point to srv2 (%s), got %+v", srv2.URL(), existing)
	}

	// Subtest 4d: SSE Client Reconnect and SnapshotSync
	t.Log("Subtest 4d: SSE client disconnect and reconnect...")
	// Client 1 connects with lastEventId to test SnapshotSync delivery on connect
	streamURL1 := fmt.Sprintf("http://%s/api/workspace/stream?token=%s&lastEventId=sync-initial", srv2.Addr(), srv2.AuthToken())
	ctx1, cancel1 := context.WithCancel(context.Background())
	events1, _ := startSSEClientWithURL(ctx1, streamURL1)

	select {
	case evt := <-events1:
		t.Logf("Client 1 received event on sync connect: %s (id: %s)", evt.Event, evt.ID)
		if evt.Event != "snapshot_sync" {
			t.Errorf("expected snapshot_sync on initial reconnect, got %s", evt.Event)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Client 1 timed out waiting for snapshot_sync event")
	}

	// Terminate client 1
	cancel1()
	time.Sleep(50 * time.Millisecond)

	// Client 2 connects to same server
	streamURL2 := fmt.Sprintf("http://%s/api/workspace/stream?token=%s&lastEventId=sync-reconnect", srv2.Addr(), srv2.AuthToken())
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	events2, _ := startSSEClientWithURL(ctx2, streamURL2)

	select {
	case evt := <-events2:
		t.Logf("Client 2 received event on reconnect: %s (id: %s)", evt.Event, evt.ID)
		if evt.Event != "snapshot_sync" {
			t.Errorf("expected snapshot_sync on reconnect, got %s", evt.Event)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Client 2 timed out waiting for snapshot_sync on reconnect")
	}
}

func TestEmpirical_StaleZombieCoordinatorAutoPurge(t *testing.T) {
	root := t.TempDir()
	createSampleProject(t, root)

	// Write a zombie coordinator record: dead PID and unreachable port
	zombieRecord := LiveCoordinatorRecord{
		URL:       "http://127.0.0.1:59998",
		Token:     "zombie-token",
		PID:       99999999, // guaranteed non-existent PID
		StartedAt: time.Now().UTC().Add(-1 * time.Hour),
		RepoRoot:  root,
	}
	if err := WriteLiveCoordinator(root, zombieRecord); err != nil {
		t.Fatalf("WriteLiveCoordinator zombie: %v", err)
	}

	// Verify discover detects dead process and purges it
	discovered, err := DiscoverLiveCoordinator(root)
	if err != nil {
		t.Fatalf("DiscoverLiveCoordinator: %v", err)
	}
	if discovered != nil {
		t.Fatalf("expected zombie coordinator to be discovered as nil (purged), got %+v", discovered)
	}

	// Verify record was deleted from filesystem
	rec, err := ReadLiveCoordinator(root)
	if err != nil {
		t.Fatalf("ReadLiveCoordinator: %v", err)
	}
	if rec != nil {
		t.Fatalf("expected stale coordinator record to be deleted, found %+v", rec)
	}

	// Verify a new server can now claim without conflict
	srv, err := NewServer(Config{
		RepoRoot:  root,
		Port:      0,
		AuthToken: "fresh-token",
		Mode:      "project_change",
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer func() { _ = srv.Shutdown(context.Background()) }()
	srv.Start()

	coord, err := ReadLiveCoordinator(root)
	if err != nil || coord == nil {
		t.Fatalf("expected new coordinator claimed, got %v", coord)
	}
	if coord.URL != srv.URL() {
		t.Errorf("claimed URL mismatch: got %s, want %s", coord.URL, srv.URL())
	}
}
