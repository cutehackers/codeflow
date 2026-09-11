package watch

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
)

// defaultNativeDebounce is the quiet window that coalesces bursts of
// filesystem events (save storms, editor atomic renames) into one ChangeSet.
// CODEFLOW_WATCH_DEBOUNCE_MS overrides it in milliseconds for tests and slow
// filesystems; unset or invalid values keep the default.
const defaultNativeDebounce = 1 * time.Second

// nativeDebounceInterval resolves the debounce once per watcher lifetime so
// the per-event path stays allocation-free and race-safe.

var ignoredDirNames = map[string]bool{
	"node_modules": true,
	".git":         true,
	".dart_tool":   true,
	"build":        true,
	"dist":         true,
	".codeflow":    true,
}

// nativeAvailable reports whether the fsnotify backend should be attempted.
// CODEFLOW_WATCH_POLL=1 forces the polling fallback (tests, exotic filesystems).
func nativeAvailable() bool {
	return os.Getenv("CODEFLOW_WATCH_POLL") != "1"
}

func nativeDebounceInterval() time.Duration {
	if raw := strings.TrimSpace(os.Getenv("CODEFLOW_WATCH_DEBOUNCE_MS")); raw != "" {
		if ms, err := strconv.Atoi(raw); err == nil && ms > 0 {
			return time.Duration(ms) * time.Millisecond
		}
	}
	return defaultNativeDebounce
}

// watchNative serves WatchChanges from OS filesystem notifications with the
// same ChangeSet seam and first-tick-baseline semantics as the poll loop.
// Any setup failure returns an error so the caller falls back to polling.
func watchNative(ctx context.Context, repoRoot string, onChange func(ChangeSet)) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer watcher.Close()
	watchedDirs := make(map[string]bool)
	if err := addRecursive(watcher, watchedDirs, repoRoot); err != nil {
		return err
	}
	watchGitIdentity(watcher, watchedDirs, repoRoot)
	_, lastMtimes, err := collectSourceFilesWithMtime(repoRoot, nil, nil)
	if err != nil {
		return err
	}
	lastRepositoryIdentity := repositoryIdentityFingerprint(repoRoot)
	pending := make(map[string]struct{})
	identityDirty := false
	debounce := nativeDebounceInterval()
	var timer *time.Timer
	var timerCh <-chan time.Time
	arm := func() {
		if timer == nil {
			timer = time.NewTimer(debounce)
		} else {
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(debounce)
		}
		timerCh = timer.C
	}
	flush := func() {
		timerCh = nil
		watchGitIdentity(watcher, watchedDirs, repoRoot)
		if len(pending) == 0 && !identityDirty {
			return
		}
		changed := make([]string, 0, len(pending))
		for p := range pending {
			changed = append(changed, p)
		}
		clear(pending)
		_, currMtimes, err := collectSourceFilesWithMtime(repoRoot, nil, nil)
		if err != nil {
			return
		}
		repositoryIdentity := repositoryIdentityFingerprint(repoRoot)
		if repositoryIdentity != lastRepositoryIdentity {
			identityDirty = false
			lastRepositoryIdentity = repositoryIdentity
			lastMtimes = currMtimes
			sort.Strings(changed)
			onChange(ChangeSet{Changed: changed, Reconcile: true, Reason: "repository identity changed"})
			return
		}
		identityDirty = false
		if len(changed) == 0 {
			lastMtimes = currMtimes
			return
		}
		delta := mtimeDelta(currMtimes, lastMtimes)
		lastMtimes = currMtimes
		if len(delta) == 0 {
			delta = changed
		}
		sort.Strings(delta)
		onChange(ChangeSet{Changed: delta})
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err, ok := <-watcher.Errors:
			if !ok {
				return context.Canceled
			}
			if err != nil {
				return err
			}
		case ev, ok := <-watcher.Events:
			if !ok {
				return context.Canceled
			}
			rel, err := filepath.Rel(repoRoot, ev.Name)
			if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
				continue
			}
			rel = filepath.ToSlash(rel)
			if rel == ".git" || strings.HasPrefix(rel, ".git/") {
				identityDirty = true
				arm()
				continue
			}
			info, statErr := os.Stat(ev.Name)
			if statErr == nil && info.IsDir() {
				if ev.Op&(fsnotify.Create|fsnotify.Rename) != 0 {
					_ = addRecursive(watcher, watchedDirs, ev.Name)
				}
				continue
			}
			if !defaultSourceExtensions[filepath.Ext(rel)] {
				continue
			}
			pending[rel] = struct{}{}
			arm()
		case <-timerCh:
			flush()
		}
	}
}

// watchGitIdentity registers the repository identity inputs (HEAD,
// commondir, gitdir, worktree gitfile) that the source enumeration skips.
// Identity changes surface as reconciliation signals on flush.
func watchGitIdentity(watcher *fsnotify.Watcher, watchedDirs map[string]bool, repoRoot string) {
	gitDir := filepath.Join(repoRoot, ".git")
	if info, err := os.Stat(gitDir); err == nil && info.IsDir() {
		if !watchedDirs[gitDir] {
			if watcher.Add(gitDir) == nil {
				watchedDirs[gitDir] = true
			}
		}
		return
	}
	if _, err := os.Stat(filepath.Join(repoRoot, ".git")); err == nil {
		if !watchedDirs[repoRoot] {
			if watcher.Add(repoRoot) == nil {
				watchedDirs[repoRoot] = true
			}
		}
	}
}

// addRecursive registers dir and all non-ignored subdirectories.
func addRecursive(watcher *fsnotify.Watcher, watchedDirs map[string]bool, dir string) error {
	return filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() {
			return nil
		}
		if ignoredDirNames[filepath.Base(p)] && p != dir {
			return filepath.SkipDir
		}
		if watchedDirs[p] {
			return nil
		}
		if err := watcher.Add(p); err != nil {
			return nil
		}
		watchedDirs[p] = true
		return nil
	})
}
