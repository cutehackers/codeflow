//go:build !windows

package flowview

import (
	"errors"
	"os"
	"syscall"
)

func tryCoordinatorFileLock(file *os.File) (bool, error) {
	err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
		return false, nil
	}
	return err == nil, err
}

func unlockCoordinatorFile(file *os.File) { _ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN) }
