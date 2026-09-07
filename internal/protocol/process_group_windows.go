//go:build windows

package protocol

import (
	"os/exec"
	"strconv"
	"syscall"
)

// CREATE_NEW_PROCESS_GROUP gives taskkill a private process tree rooted at
// the adapter. taskkill /T is the Windows process-tree equivalent of the
// Unix process-group kill used by the runtime lifecycle.
const createNewProcessGroup = 0x00000200

func configureProcessGroup(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNewProcessGroup}
}

func signalProcessGroupTermination(cmd *exec.Cmd) modelHostProcessGroupSignal {
	if cmd == nil || cmd.Process == nil {
		return modelHostProcessGroupSignal{}
	}
	if err := exec.Command("taskkill", "/PID", strconv.Itoa(cmd.Process.Pid), "/T", "/F").Run(); err == nil {
		return modelHostProcessGroupSignal{}
	}
	return modelHostProcessGroupSignal{processKillErr: cmd.Process.Kill()}
}

func verifyProcessGroupGone(_ *exec.Cmd) error {
	return nil
}

func terminateProcessGroup(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	if err := exec.Command("taskkill", "/PID", strconv.Itoa(cmd.Process.Pid), "/T", "/F").Run(); err == nil {
		return nil
	}
	return cmd.Process.Kill()
}
