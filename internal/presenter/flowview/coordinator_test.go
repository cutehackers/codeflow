package flowview

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestDiscoverLiveCoordinatorKeepsRecordOnAuthFailure verifies that a 401/403
// from the ping endpoint is treated as proof the endpoint is owned, not as a
// stale record. Deleting another owner's registration would break discovery
// for another process sharing the repository.
func TestDiscoverLiveCoordinatorKeepsRecordOnAuthFailure(t *testing.T) {
	root := t.TempDir()
	srv, err := NewServer(Config{RepoRoot: root, Port: 0, Mode: "project_change"})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer func() { _ = srv.Shutdown(context.Background()) }()
	srv.Start()

	bad := LiveCoordinatorRecord{
		URL:       srv.URL(),
		Token:     "mismatched-token",
		PID:       os.Getpid(),
		StartedAt: time.Now().UTC(),
		RepoRoot:  root,
	}
	if err := WriteLiveCoordinator(root, bad); err != nil {
		t.Fatalf("WriteLiveCoordinator: %v", err)
	}
	got, err := DiscoverLiveCoordinator(root)
	if err != nil {
		t.Fatalf("DiscoverLiveCoordinator: %v", err)
	}
	if got == nil {
		t.Fatal("expected coordinator with auth failure to stay active")
	}
	if _, err := ReadLiveCoordinator(root); err != nil {
		t.Fatalf("record must survive an auth failure: %v", err)
	}
}

// A subprocess uses a separate OS lock owner, so this catches a process-local
// mutex that appears correct under goroutine-only concurrency tests.
func TestLiveCoordinatorProcessClaim(t *testing.T) {
	if os.Getenv("CODEFLOW_COORDINATOR_PROCESS") != "claim" {
		return
	}
	root := os.Getenv("CODEFLOW_COORDINATOR_ROOT")
	gate := filepath.Join(root, "claim-start")
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(gate); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("claim start timed out")
		}
		time.Sleep(10 * time.Millisecond)
	}
	claimed, _, err := ClaimLiveCoordinator(root, LiveCoordinatorRecord{URL: os.Getenv("CODEFLOW_COORDINATOR_URL"), RepoRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("coordinator-claimed=%t\n", claimed)
}

func TestLiveCoordinatorClaimsSerializeAcrossProcesses(t *testing.T) {
	root := t.TempDir()
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/views" {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer first.Close()
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/views" {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer second.Close()
	type processResult struct {
		output string
		err    error
	}
	results := make(chan processResult, 2)
	for _, address := range []string{first.URL, second.URL} {
		cmd := exec.Command(os.Args[0], "-test.run=^TestLiveCoordinatorProcessClaim$")
		cmd.Env = append(os.Environ(), "CODEFLOW_COORDINATOR_PROCESS=claim", "CODEFLOW_COORDINATOR_ROOT="+root, "CODEFLOW_COORDINATOR_URL="+address)
		go func() { output, err := cmd.CombinedOutput(); results <- processResult{string(output), err} }()
	}
	if err := os.WriteFile(filepath.Join(root, "claim-start"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	winners := 0
	for range 2 {
		result := <-results
		if result.err != nil {
			t.Fatalf("claim process: %v %s", result.err, result.output)
		}
		if strings.Contains(result.output, "coordinator-claimed=true") {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("got %d claim winners, want one", winners)
	}
	record, err := DiscoverLiveCoordinator(root)
	if err != nil || record == nil || (record.URL != first.URL && record.URL != second.URL) {
		t.Fatalf("winner record missing: %+v %v", record, err)
	}
}

func TestLiveCoordinatorStaleDiscoveryCannotDeleteSuccessor(t *testing.T) {
	root := t.TempDir()
	pingStarted := make(chan struct{})
	releasePing := make(chan struct{})
	stale := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(pingStarted)
		<-releasePing
		w.WriteHeader(http.StatusNotFound)
	}))
	defer stale.Close()
	if err := WriteLiveCoordinator(root, LiveCoordinatorRecord{URL: stale.URL}); err != nil {
		t.Fatal(err)
	}
	discoveryDone := make(chan error, 1)
	go func() { _, err := DiscoverLiveCoordinator(root); discoveryDone <- err }()
	<-pingStarted
	successor := LiveCoordinatorRecord{URL: "http://127.0.0.1:12345", PID: os.Getpid()}
	writeDone := make(chan error, 1)
	go func() { writeDone <- WriteLiveCoordinator(root, successor) }()
	select {
	case err := <-writeDone:
		close(releasePing)
		t.Fatalf("successor write bypassed discovery lock: %v", err)
	case <-time.After(40 * time.Millisecond):
	}
	close(releasePing)
	if err := <-discoveryDone; err != nil {
		t.Fatal(err)
	}
	if err := <-writeDone; err != nil {
		t.Fatal(err)
	}
	record, err := ReadLiveCoordinator(root)
	if err != nil || record == nil || record.URL != successor.URL {
		t.Fatalf("stale cleanup removed successor: %+v %v", record, err)
	}
}

func TestLiveCoordinatorOwnedRemovalWaitsForSuccessorPublication(t *testing.T) {
	root := t.TempDir()
	owner := LiveCoordinatorRecord{URL: "http://127.0.0.1:12345", PID: os.Getpid()}
	if err := WriteLiveCoordinator(root, owner); err != nil {
		t.Fatal(err)
	}
	unlock, err := lockLiveCoordinator(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if unlock != nil {
			unlock()
		}
	}()
	removalDone := make(chan error, 1)
	go func() { removalDone <- RemoveLiveCoordinatorIfOwned(root, owner.URL, owner.PID) }()
	select {
	case err := <-removalDone:
		t.Fatalf("owned removal bypassed publication lock: %v", err)
	case <-time.After(40 * time.Millisecond):
	}
	successor := LiveCoordinatorRecord{URL: "http://127.0.0.1:23456", PID: owner.PID + 1}
	if err := writeLiveCoordinatorRecord(root, successor); err != nil {
		t.Fatal(err)
	}
	unlock()
	unlock = nil
	if err := <-removalDone; err != nil {
		t.Fatal(err)
	}
	record, err := ReadLiveCoordinator(root)
	if err != nil || record == nil || record.URL != successor.URL {
		t.Fatalf("old owner removed successor: %+v %v", record, err)
	}
	if err := RemoveLiveCoordinatorIfOwned(root, successor.URL, successor.PID); err != nil {
		t.Fatal(err)
	}
	record, err = ReadLiveCoordinator(root)
	if err != nil || record != nil {
		t.Fatalf("matching owner was not removed: %+v %v", record, err)
	}
}

func TestLiveCoordinatorLockAcquisitionIsBounded(t *testing.T) {
	root := t.TempDir()
	unlock, err := lockLiveCoordinator(root)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if err := WriteLiveCoordinator(root, LiveCoordinatorRecord{URL: "http://127.0.0.1:12345"}); err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("held lock did not time out: %v", err)
	}
	record, err := ReadLiveCoordinator(root)
	if err != nil || record != nil {
		t.Fatalf("timed-out writer published a record: %+v %v", record, err)
	}
}

func TestDiscoverLiveCoordinatorRejectsExternalURL(t *testing.T) {
	root := t.TempDir()
	if err := WriteLiveCoordinator(root, LiveCoordinatorRecord{URL: "https://example.com", Token: "private"}); err != nil {
		t.Fatal(err)
	}
	if _, err := DiscoverLiveCoordinator(root); err == nil {
		t.Fatal("external coordinator accepted")
	}
}
