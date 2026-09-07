//go:build aix || darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || solaris

package protocol

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"
)

const processGroupExitWait = 2 * time.Second

var (
	modelHostKillProcessGroup     = func(pgid int, signal syscall.Signal) error { return syscall.Kill(-pgid, signal) }
	modelHostKillProcess          = func(process *os.Process) error { return process.Kill() }
	modelHostWaitProcessGroupExit = waitProcessGroupExit
)

func configureProcessGroup(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// signalProcessGroupTermination sends the terminal signal to the process
// group and direct child without waiting for either to disappear. The caller
// chooses when a direct Process.Wait and the bounded group-absence check occur.
func signalProcessGroupTermination(cmd *exec.Cmd) modelHostProcessGroupSignal {
	if cmd == nil || cmd.Process == nil {
		return modelHostProcessGroupSignal{}
	}
	pid := cmd.Process.Pid
	result := modelHostProcessGroupSignal{}
	groupKillErr := modelHostKillProcessGroup(pid, syscall.SIGKILL)
	if !errors.Is(groupKillErr, syscall.ESRCH) && groupKillErr != nil {
		result.groupKillErr = fmt.Errorf("kill model host process group: %w", groupKillErr)
	}
	if err := modelHostKillProcess(cmd.Process); err != nil && !errors.Is(err, syscall.ESRCH) && !errors.Is(err, os.ErrProcessDone) {
		result.processKillErr = fmt.Errorf("kill model host process: %w", err)
	}
	return result
}

// verifyProcessGroupGone performs only the bounded post-reap group absence
// check. It never waits for the direct child, so callers can guarantee one
// and only one Process.Wait before reaching this stage.
func verifyProcessGroupGone(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	return modelHostWaitProcessGroupExit(cmd.Process.Pid, processGroupExitWait)
}

// terminateProcessGroup kills the adapter and every descendant that remains
// in its process group. The bounded group wait completes before callers can
// remove the disposable working directory.
func terminateProcessGroup(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	signalResult := signalProcessGroupTermination(cmd)
	var terminationErrors []error
	if signalResult.processKillErr != nil {
		terminationErrors = append(terminationErrors, signalResult.processKillErr)
	}
	groupWaitErr := verifyProcessGroupGone(cmd)
	if groupWaitErr != nil {
		if signalResult.groupKillErr != nil {
			terminationErrors = append(terminationErrors, signalResult.groupKillErr)
		}
		terminationErrors = append(terminationErrors, groupWaitErr)
	} else if signalResult.groupKillErr != nil && !errors.Is(signalResult.groupKillErr, syscall.EPERM) {
		// Darwin can report EPERM for a stale process-group id while the
		// bounded wait has already established that the group is absent.
		terminationErrors = append(terminationErrors, signalResult.groupKillErr)
	}
	return errors.Join(terminationErrors...)
}

func waitProcessGroupExit(pgid int, timeout time.Duration) error {
	if pgid <= 0 {
		return errors.New("model host process group id is invalid")
	}
	if timeout <= 0 {
		timeout = processGroupExitWait
	}
	deadline := time.Now().Add(timeout)
	var observeErr error
	for {
		if err := syscall.Kill(-pgid, 0); err != nil {
			if errors.Is(err, syscall.ESRCH) {
				return nil
			}
			observeErr = fmt.Errorf("observe model host process group %d: %w", pgid, err)
		} else {
			observeErr = nil
		}
		if !time.Now().Before(deadline) {
			if observeErr != nil {
				return observeErr
			}
			return fmt.Errorf("model host process group %d did not exit before timeout", pgid)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
