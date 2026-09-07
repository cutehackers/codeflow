//go:build !windows && !aix && !darwin && !dragonfly && !freebsd && !illumos && !linux && !netbsd && !openbsd && !solaris

package protocol

import "os/exec"

func configureProcessGroup(_ *exec.Cmd) {}

func signalProcessGroupTermination(cmd *exec.Cmd) modelHostProcessGroupSignal {
	if cmd == nil || cmd.Process == nil {
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
	return cmd.Process.Kill()
}
