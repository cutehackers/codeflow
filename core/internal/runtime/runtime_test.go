package runtime

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLockLiveAndStaleRecoveryUsePIDAndFingerprint(t *testing.T) {
	repo := t.TempDir()
	first, err := Acquire(repo)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Release()
	if _, err := Acquire(repo); err != ErrRunning {
		t.Fatalf("live matching lock = %v", err)
	}
	first.Release()
	dir := filepath.Join(repo, ".codeflow")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	stale, _ := json.Marshal(Lock{PID: 999999, RepositoryFingerprint: Fingerprint(repo)})
	if err := os.WriteFile(filepath.Join(dir, "codeflow.lock"), stale, 0600); err != nil {
		t.Fatal(err)
	}
	h, err := Acquire(repo)
	if err != nil {
		t.Fatalf("dead matching lock must recover: %v", err)
	}
	h.Release()
	wrong, _ := json.Marshal(Lock{PID: os.Getpid(), RepositoryFingerprint: "sha256:other"})
	if err := os.WriteFile(filepath.Join(dir, "codeflow.lock"), wrong, 0600); err != nil {
		t.Fatal(err)
	}
	h, err = Acquire(repo)
	if err != nil {
		t.Fatalf("wrong-fingerprint lock must recover: %v", err)
	}
	h.Release()
}

func TestReadStateRejectsIncompatibleRuntimeVersion(t *testing.T) {
	repo := t.TempDir()
	dir := filepath.Join(repo, ".codeflow")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(State{PID: os.Getpid(), Port: 1234, AuthToken: "token", RepositoryFingerprint: Fingerprint(repo), RuntimeVersion: "old"})
	if err := os.WriteFile(filepath.Join(dir, "runtime.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadState(repo); err == nil {
		t.Fatal("old runtime version must not be reused")
	}
}
