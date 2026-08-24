// Package runtime manages the one local Core process permitted for a repository.
package runtime

import (
	"codeflow/core/internal/version"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
)

type State struct {
	PID                   int    `json:"pid"`
	Port                  int    `json:"port"`
	RepositoryFingerprint string `json:"repository_fingerprint"`
	AuthToken             string `json:"auth_token"`
	StartedAt             string `json:"started_at"`
	RuntimeVersion        string `json:"runtime_version"`
}
type Lock struct {
	PID                   int    `json:"pid"`
	RepositoryFingerprint string `json:"repository_fingerprint"`
}
type Handle struct {
	Dir   string
	State State
}

var ErrRunning = errors.New("a live CodeFlow Core already owns this repository")

func Fingerprint(repo string) string {
	sum := sha256.Sum256([]byte(filepath.Clean(repo)))
	return "sha256:" + hex.EncodeToString(sum[:])
}
func Acquire(repo string) (Handle, error) {
	dir := filepath.Join(repo, ".codeflow")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Handle{}, err
	}
	fingerprint := Fingerprint(repo)
	lockPath := filepath.Join(dir, "codeflow.lock")
	for attempt := 0; attempt < 2; attempt++ {
		data, err := json.Marshal(Lock{PID: os.Getpid(), RepositoryFingerprint: fingerprint})
		if err != nil {
			return Handle{}, err
		}
		file, err := os.OpenFile(lockPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			_, err = file.Write(data)
			closeErr := file.Close()
			if err != nil {
				return Handle{}, err
			}
			if closeErr != nil {
				return Handle{}, closeErr
			}
			return Handle{Dir: dir, State: State{PID: os.Getpid(), RepositoryFingerprint: fingerprint}}, nil
		}
		if !os.IsExist(err) {
			return Handle{}, err
		}
		existingBytes, readErr := os.ReadFile(lockPath)
		var existing Lock
		if readErr == nil && json.Unmarshal(existingBytes, &existing) == nil && existing.RepositoryFingerprint == fingerprint && processAlive(existing.PID) {
			return Handle{}, ErrRunning
		}
		if err := os.Remove(lockPath); err != nil && !os.IsNotExist(err) {
			return Handle{}, fmt.Errorf("recover stale lock: %w", err)
		}
	}
	return Handle{}, errors.New("could not acquire runtime lock")
}
func (h Handle) Write(port int, token string, startedAt string) error {
	h.State.Port, h.State.AuthToken, h.State.StartedAt, h.State.RuntimeVersion = port, token, startedAt, version.Runtime
	data, err := json.Marshal(h.State)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(h.Dir, "runtime.json"), data, 0o600)
}
func (h Handle) Release() {
	_ = os.Remove(filepath.Join(h.Dir, "runtime.json"))
	_ = os.Remove(filepath.Join(h.Dir, "codeflow.lock"))
}

// ReadState lets protocol front-ends attach to the one existing repository
// Core. It does not acquire a lock or open SQLite, so it cannot compete with
// the analysis runtime.
func ReadState(repo string) (State, error) {
	root, err := filepath.Abs(repo)
	if err != nil {
		return State{}, err
	}
	data, err := os.ReadFile(filepath.Join(root, ".codeflow", "runtime.json"))
	if err != nil {
		return State{}, fmt.Errorf("CORE_UNAVAILABLE: no repository Core is running")
	}
	var state State
	if json.Unmarshal(data, &state) != nil || state.Port <= 0 || state.AuthToken == "" || state.RuntimeVersion != version.Runtime || state.RepositoryFingerprint != Fingerprint(root) || !processAlive(state.PID) {
		return State{}, fmt.Errorf("CORE_UNAVAILABLE: runtime state is stale or invalid")
	}
	return state, nil
}
func Token() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}
func ParsePID(value string) int { n, _ := strconv.Atoi(value); return n }
