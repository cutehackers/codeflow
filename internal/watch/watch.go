// Package watch implements polling-based worktree change detection (ticket 10 minimal).
// Full incremental design (fsnotify + debounced Reconcile→impact→recompile with R4 cache) requires
// separate detailed design for debounce, ignore patterns, and adapter invalidation; this minimal
// watcher provides the polling primitive that FlowView auto-refresh and future watch loops can build on.
package watch

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"time"

	"codeflow/internal/storage"
)

// Watch polls repoRoot/lib every interval, computing a worktree fingerprint via storage.ComputeWorktreeFingerprint.
// When the fingerprint changes, onChange is called with the current file list. Blocks until ctx cancelled.
//
// Optimization: maintains lastMtimes map and lastFingerprint to avoid ReadFile+sha256 when no mtime
// changed. Polling defaults to 500ms if interval <=0. First tick establishes baseline without callback.
func Watch(ctx context.Context, repoRoot string, interval time.Duration, onChange func(changed []string)) error {
	if interval <= 0 {
		interval = 500 * time.Millisecond
	}
	var lastFingerprint string
	lastMtimes := make(map[string]time.Time)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			files, currMtimes, err := collectDartFilesWithMtime(repoRoot)
			if err != nil {
				continue
			}

			// Determine mtime delta.
			changedFiles := mtimeDelta(currMtimes, lastMtimes)

			// If file count same and no mtime changed, skip fingerprint recompute entirely (no ReadFile).
			if len(changedFiles) == 0 && len(currMtimes) == len(lastMtimes) {
				continue
			}

			// Any mtime change (or added/removed) -> compute fingerprint.
			fp, err := storage.ComputeWorktreeFingerprint(repoRoot, files)
			if err != nil {
				continue
			}

			// First tick sets baseline without triggering onChange.
			if lastFingerprint == "" {
				lastFingerprint = fp
				lastMtimes = currMtimes
				continue
			}

			if fp != lastFingerprint {
				// Fingerprint changed -> notify with mtime-delta files (or all if delta empty but fingerprint changed).
				toCall := changedFiles
				if len(toCall) == 0 {
					toCall = files
				}
				sort.Strings(toCall)
				onChange(toCall)
			}
			// Update after handling.
			lastFingerprint = fp
			lastMtimes = currMtimes
		}
	}
}

// mtimeDelta returns paths that have mtime delta between current and last (added, modified, or deleted).
func mtimeDelta(curr, last map[string]time.Time) []string {
	var changed []string
	for p, t := range curr {
		if old, ok := last[p]; !ok || !old.Equal(t) {
			changed = append(changed, p)
		}
	}
	for p := range last {
		if _, ok := curr[p]; !ok {
			changed = append(changed, p)
		}
	}
	return changed
}

func collectDartFiles(repoRoot string) ([]string, error) {
	libRoot := filepath.Join(repoRoot, "lib")
	var out []string
	err := filepath.Walk(libRoot, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			return nil
		}
		if filepath.Ext(p) == ".dart" {
			rel, _ := filepath.Rel(repoRoot, p)
			out = append(out, rel)
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}

func collectDartFilesWithMtime(repoRoot string) ([]string, map[string]time.Time, error) {
	libRoot := filepath.Join(repoRoot, "lib")
	mTimes := make(map[string]time.Time)
	var out []string
	err := filepath.Walk(libRoot, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			return nil
		}
		if filepath.Ext(p) == ".dart" {
			rel, _ := filepath.Rel(repoRoot, p)
			out = append(out, rel)
			mTimes[rel] = info.ModTime()
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return nil, nil, err
	}
	sort.Strings(out)
	// Ensure mTimes only contains returned files (already) and is consistent.
	return out, mTimes, nil
}
