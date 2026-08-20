//go:build unix

package dartadapter

import (
	"os/exec"
	"syscall"
)

// isolateProcessGroup keeps an interactive Ctrl-C aimed at the foreground
// CodeFlow process from killing the owned Analyzer before Core can perform its
// JSON-RPC shutdown and reap it deterministically.
func isolateProcessGroup(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}
