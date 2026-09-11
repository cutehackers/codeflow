// Package watch implements worktree change detection with native OS
// notifications and a polling fallback behind the ChangeSet seam.
package watch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"
	"time"

	"codeflow/internal/storage"
)

// ChangeSet is a filesystem signal for the live coordinator. Changed paths
// still require stable capture. Reconcile indicates that event metadata is
// insufficient, as with branch or worktree identity changes.
type ChangeSet struct {
	Changed   []string
	Reconcile bool
	Reason    string
}

// Watch polls repoRoot/lib every interval, computing a worktree fingerprint via storage.ComputeWorktreeFingerprint.
// When the fingerprint changes, onChange is called with the current file list. Blocks until ctx cancelled.
//
// Optimization: maintains lastMtimes map and lastFingerprint to avoid ReadFile+sha256 when no mtime
// changed. Polling defaults to 500ms if interval <=0. First tick establishes baseline without callback.
func Watch(ctx context.Context, repoRoot string, interval time.Duration, onChange func(changed []string)) error {
	return WatchChanges(ctx, repoRoot, interval, func(change ChangeSet) {
		if onChange != nil {
			onChange(change.Changed)
		}
	})
}

// WatchChanges serves capture signals from native OS notifications when
// available and falls back to the polling loop otherwise. Both backends
// share the ChangeSet seam and first-tick-baseline semantics.
func WatchChanges(ctx context.Context, repoRoot string, interval time.Duration, onChange func(ChangeSet)) error {
	if nativeAvailable() {
		if err := watchNative(ctx, repoRoot, onChange); err == nil {
			return nil
		}
	}
	return watchPoll(ctx, repoRoot, interval, onChange)
}

func watchPoll(ctx context.Context, repoRoot string, interval time.Duration, onChange func(ChangeSet)) error {
	if interval <= 0 {
		interval = 500 * time.Millisecond
	}
	var lastFingerprint string
	var lastRepositoryIdentity string
	lastMtimes := make(map[string]time.Time)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			files, currMtimes, err := collectSourceFilesWithMtime(repoRoot, nil, nil)
			if err != nil {
				continue
			}
			repositoryIdentity := repositoryIdentityFingerprint(repoRoot)

			// Determine mtime delta.
			changedFiles := mtimeDelta(currMtimes, lastMtimes)

			// If file count same and no mtime changed, skip fingerprint recompute entirely (no ReadFile).
			if len(changedFiles) == 0 && len(currMtimes) == len(lastMtimes) && repositoryIdentity == lastRepositoryIdentity {
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
				lastRepositoryIdentity = repositoryIdentity
				continue
			}

			if repositoryIdentity != lastRepositoryIdentity {
				onChange(ChangeSet{Changed: changedFiles, Reconcile: true, Reason: "repository identity changed"})
			} else if fp != lastFingerprint {
				// Fingerprint changed -> notify with mtime-delta files (or all if delta empty but fingerprint changed).
				toCall := changedFiles
				if len(toCall) == 0 {
					toCall = files
				}
				sort.Strings(toCall)
				onChange(ChangeSet{Changed: toCall})
			}
			// Update after handling.
			lastFingerprint = fp
			lastMtimes = currMtimes
			lastRepositoryIdentity = repositoryIdentity
		}
	}
}

func repositoryIdentityFingerprint(repoRoot string) string {
	h := sha256.New()
	for _, name := range []string{"HEAD", "commondir", "gitdir"} {
		data, err := os.ReadFile(filepath.Join(repoRoot, ".git", name))
		if err == nil {
			_, _ = h.Write([]byte(name))
			_, _ = h.Write(data)
		}
	}
	if data, err := os.ReadFile(filepath.Join(repoRoot, ".git")); err == nil {
		_, _ = h.Write([]byte("gitfile"))
		_, _ = h.Write(data)
	}
	return hex.EncodeToString(h.Sum(nil))
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

var defaultSourceExtensions = map[string]bool{
	".dart":  true,
	".ts":    true,
	".tsx":   true,
	".js":    true,
	".jsx":   true,
	".kt":    true,
	".kts":   true,
	".java":  true,
	".swift": true,
	".py":    true,
	".go":    true,
	".rs":    true,
	".yaml":  true,
	".json":  true,
	".mod":   true,
}

func collectSourceFilesWithMtime(repoRoot string, dirs []string, exts []string) ([]string, map[string]time.Time, error) {
	if len(dirs) == 0 {
		dirs = []string{"."}
	}
	extMap := defaultSourceExtensions
	if len(exts) > 0 {
		extMap = make(map[string]bool, len(exts))
		for _, e := range exts {
			extMap[e] = true
		}
	}

	mTimes := make(map[string]time.Time)
	var out []string
	seen := make(map[string]bool)

	for _, dir := range dirs {
		searchPath := filepath.Join(repoRoot, dir)
		if _, err := os.Stat(searchPath); err != nil {
			continue
		}
		_ = filepath.Walk(searchPath, func(p string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if info.IsDir() {
				// Skip node_modules, build, .git, .dart_tool, dist
				base := filepath.Base(p)
				if base == "node_modules" || base == ".git" || base == ".dart_tool" || base == "build" || base == "dist" || base == ".codeflow" {
					return filepath.SkipDir
				}
				return nil
			}
			ext := filepath.Ext(p)
			if extMap[ext] {
				rel, err := filepath.Rel(repoRoot, p)
				if err == nil && !seen[rel] {
					seen[rel] = true
					out = append(out, rel)
					mTimes[rel] = info.ModTime()
				}
			}
			return nil
		})
	}

	sort.Strings(out)
	return out, mTimes, nil
}

func collectDartFiles(repoRoot string) ([]string, error) {
	files, _, err := collectSourceFilesWithMtime(repoRoot, []string{"lib"}, []string{".dart"})
	return files, err
}

func collectDartFilesWithMtime(repoRoot string) ([]string, map[string]time.Time, error) {
	return collectSourceFilesWithMtime(repoRoot, []string{"lib"}, []string{".dart"})
}
