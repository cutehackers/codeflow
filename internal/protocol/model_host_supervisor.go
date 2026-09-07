package protocol

import (
	"errors"
	"fmt"
	"io"
	"os/exec"
	"syscall"
	"time"
)

// modelHostSupervisor is the instance-owned boundary between Core's model
// host lifecycle and platform process/resource operations. It is deliberately
// package-private so callers outside protocol cannot inject a fake supervisor
// or manufacture resource evidence through the public model-host API.
//
// A supervisor is captured by one ModelHost instance. In particular, memory
// observations and the backend identity used for resource evidence must come
// from the same supervisor that started the child process.
type modelHostSupervisor interface {
	Start(command string, args, env []string, workDir string, limits ModelHostResourceLimits) (*exec.Cmd, io.WriteCloser, io.ReadCloser, io.ReadCloser, error)
	PhysicalMemoryUsage(pid int) (uint64, error)
	ResourceLimitBackend() string
}

// modelHostCPUTimeObserver is an optional platform capability. It is kept
// separate from modelHostSupervisor so existing test-only supervisors need not
// manufacture CPU telemetry. The default Darwin supervisor implements it with
// Core-side process accounting.
type modelHostCPUTimeObserver interface {
	CPUTimeUsage(pid int) (time.Duration, error)
}

// defaultModelHostSupervisor delegates to the platform implementation. It is
// a value, not a mutable package-global, so every host captures an independent
// default boundary while retaining the platform's existing enforcement.
type defaultModelHostSupervisor struct{}

func (defaultModelHostSupervisor) Start(command string, args, env []string, workDir string, limits ModelHostResourceLimits) (*exec.Cmd, io.WriteCloser, io.ReadCloser, io.ReadCloser, error) {
	return startModelHostProcess(command, args, env, workDir, limits)
}

func (defaultModelHostSupervisor) PhysicalMemoryUsage(pid int) (uint64, error) {
	return modelHostPhysicalMemoryUsage(pid)
}

func (defaultModelHostSupervisor) CPUTimeUsage(pid int) (time.Duration, error) {
	return modelHostCPUTimeUsage(pid)
}

func (defaultModelHostSupervisor) ResourceLimitBackend() string {
	return modelHostResourceLimitBackend()
}

func modelHostSupervisorForConfig(cfg ModelHostConfig) modelHostSupervisor {
	if cfg.supervisor != nil {
		return cfg.supervisor
	}
	return defaultModelHostSupervisor{}
}

func validModelHostSupervisorBackend(backend string) bool {
	return backend == ModelHostResourceBackendDarwinHostTree
}

// cleanupStartedModelHostProcess releases a process and its parent-side
// descriptors when a supervisor has started a child but Core cannot complete
// host construction. This keeps partial supervisor results from becoming an
// unowned process or descriptor leak.
func cleanupStartedModelHostProcess(cmd *exec.Cmd, stdin io.WriteCloser, stdout, stderr io.ReadCloser) error {
	var cleanupErrors []error
	closeDescriptor := func(name string, closer io.Closer) {
		if closer == nil {
			return
		}
		if err := closer.Close(); err != nil {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("close model host %s: %w", name, err))
		}
	}
	closeDescriptor("stdin", stdin)
	closeDescriptor("stdout", stdout)
	closeDescriptor("stderr", stderr)
	if cmd == nil || cmd.Process == nil {
		return errors.Join(cleanupErrors...)
	}
	signalResult := signalProcessGroupTermination(cmd)
	if signalResult.processKillErr != nil {
		cleanupErrors = append(cleanupErrors, signalResult.processKillErr)
	}
	processState, err := modelHostProcessWait(cmd.Process)
	cmd.ProcessState = processState
	if err != nil {
		cleanupErrors = append(cleanupErrors, fmt.Errorf("wait for model host process: %w", err))
	}
	groupVerifyErr := verifyProcessGroupGone(cmd)
	if groupVerifyErr != nil {
		if signalResult.groupKillErr != nil {
			cleanupErrors = append(cleanupErrors, signalResult.groupKillErr)
		}
		cleanupErrors = append(cleanupErrors, groupVerifyErr)
	} else if signalResult.groupKillErr != nil && !errors.Is(signalResult.groupKillErr, syscall.EPERM) {
		// A stale process-group id may report EPERM after the direct child has
		// been reaped. The successful bounded absence check proves cleanup in
		// that case, matching normal lifecycle termination semantics.
		cleanupErrors = append(cleanupErrors, signalResult.groupKillErr)
	}
	return errors.Join(cleanupErrors...)
}
