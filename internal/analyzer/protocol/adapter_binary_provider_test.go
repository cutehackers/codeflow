package protocol

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
)

var (
	goAdapterBinaryOnce  sync.Once
	goAdapterBinaryPath  string
	goAdapterBinaryError error

	mockAdapterBinaryOnce  sync.Once
	mockAdapterBinaryPath  string
	mockAdapterBinaryError error
)

func provideGoAdapterBinary(t *testing.T) string {
	t.Helper()
	goAdapterBinaryOnce.Do(func() {
		root := repoRootDir(t)
		dir, err := os.MkdirTemp("", "codeflow-protocol-go-adapter-*")
		if err != nil {
			goAdapterBinaryError = err
			return
		}
		bin := filepath.Join(dir, "codeflow-go-adapter")
		cmd := exec.Command("go", "build", "-o", bin, "./adapters/go")
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
		if out, err := cmd.CombinedOutput(); err != nil {
			goAdapterBinaryError = fmt.Errorf("build Go adapter: %w\n%s", err, out)
			return
		}
		goAdapterBinaryPath = bin
	})
	if goAdapterBinaryError != nil {
		t.Fatalf("provide Go adapter binary: %v", goAdapterBinaryError)
	}
	return goAdapterBinaryPath
}

func provideMockAdapterBinary(t *testing.T) string {
	t.Helper()
	mockAdapterBinaryOnce.Do(func() {
		root := repoRootDir(t)
		dir, err := os.MkdirTemp("", "codeflow-protocol-mockadapter-*")
		if err != nil {
			mockAdapterBinaryError = err
			return
		}
		bin := filepath.Join(dir, "mockadapter")
		cmd := exec.Command("go", "build", "-o", bin, "./internal/analyzer/mockadapter")
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
		if out, err := cmd.CombinedOutput(); err != nil {
			mockAdapterBinaryError = fmt.Errorf("build mock adapter: %w\n%s", err, out)
			return
		}
		mockAdapterBinaryPath = bin
	})
	if mockAdapterBinaryError != nil {
		t.Fatalf("provide mock adapter binary: %v", mockAdapterBinaryError)
	}
	return mockAdapterBinaryPath
}
