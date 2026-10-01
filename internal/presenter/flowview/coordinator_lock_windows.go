package flowview

import (
	"os"
	"syscall"
	"unsafe"
)

var (
	coordinatorKernel     = syscall.NewLazyDLL("kernel32.dll")
	coordinatorLockFile   = coordinatorKernel.NewProc("LockFileEx")
	coordinatorUnlockFile = coordinatorKernel.NewProc("UnlockFileEx")
)

func tryCoordinatorFileLock(file *os.File) (bool, error) {
	var position syscall.Overlapped
	result, _, err := coordinatorLockFile.Call(file.Fd(), 3, 0, 1, 0, uintptr(unsafe.Pointer(&position)))
	if result != 0 {
		return true, nil
	}
	if err == syscall.Errno(33) {
		return false, nil
	}
	return false, err
}

func unlockCoordinatorFile(file *os.File) {
	var position syscall.Overlapped
	_, _, _ = coordinatorUnlockFile.Call(file.Fd(), 0, 1, 0, uintptr(unsafe.Pointer(&position)))
}
