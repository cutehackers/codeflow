//go:build unix

package dartadapter

import (
	"os/exec"
	"testing"
)

func TestAnalyzerRunsOutsideInteractiveCoreProcessGroup(t *testing.T) {
	command := exec.Command("unused")
	isolateProcessGroup(command)
	if command.SysProcAttr == nil || !command.SysProcAttr.Setpgid {
		t.Fatal("Dart Analyzer would receive the terminal Ctrl-C before Core shutdown")
	}
}
