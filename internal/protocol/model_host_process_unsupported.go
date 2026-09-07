//go:build !darwin || !cgo

package protocol

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"time"
)

func startModelHostProcess(string, []string, []string, string, ModelHostResourceLimits) (*exec.Cmd, io.WriteCloser, io.ReadCloser, io.ReadCloser, error) {
	return nil, nil, nil, nil, UnsupportedVersionError("model host resource limiter is unavailable on this platform")
}

func currentModelHostPhysicalMemoryUsage() (uint64, error) {
	return 0, errors.New("model host physical memory usage observation is unavailable")
}

func modelHostPhysicalMemoryUsage(int) (uint64, error) {
	return 0, errors.New("model host physical memory usage observation is unavailable")
}

func modelHostCPUTimeUsage(int) (time.Duration, error) {
	return 0, errors.New("model host CPU time observation is unavailable")
}

func modelHostResetCPUTimeLimitSignal() {}

func modelHostProcessExitedDueToCPUResourceLimit(*os.ProcessState, uint64) bool {
	return false
}

func modelHostResourceLimitBackend() string {
	return ""
}
