package flowview

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// LockLiveCoordinatorStartup serializes coordinator creation across processes.
// It uses a separate file from record publication so Server.Start can claim its
// record while the caller holds the startup lock. The caller must release it.
func LockLiveCoordinatorStartup(ctx context.Context, repoRoot string) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path := coordinatorFilePath(repoRoot) + ".startup.lock"
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, fmt.Errorf("create coordinator startup lock dir: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("open coordinator startup lock: %w", err)
	}
	acquisitionCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	for {
		if err := acquisitionCtx.Err(); err != nil {
			_ = file.Close()
			return nil, fmt.Errorf("acquire coordinator startup lock: %w", err)
		}
		acquired, lockErr := tryCoordinatorFileLock(file)
		if lockErr != nil {
			_ = file.Close()
			return nil, fmt.Errorf("lock coordinator startup: %w", lockErr)
		}
		if acquired {
			return func() { unlockCoordinatorFile(file); _ = file.Close() }, nil
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-acquisitionCtx.Done():
			timer.Stop()
		case <-timer.C:
		}
	}
}
