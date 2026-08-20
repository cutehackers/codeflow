//go:build !unix

package dartadapter

import "os/exec"

func isolateProcessGroup(_ *exec.Cmd) {}
