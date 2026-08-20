// Package workspacewatch turns filesystem activity into reconciliation
// notifications. Events are never evidence: manifest capture remains the only
// authority for bytes, Git state, and snapshot identity.
package workspacewatch

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"codeflow/core/internal/manifest"
	"github.com/fsnotify/fsnotify"
)

type Watcher struct {
	root   string
	inner  *fsnotify.Watcher
	notify func()
	done   chan struct{}
	once   sync.Once
	wg     sync.WaitGroup
}

func Start(root string, notify func()) (*Watcher, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if resolved, resolveErr := filepath.EvalSymlinks(root); resolveErr == nil {
		root = resolved
	}
	inner, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	w := &Watcher{root: root, inner: inner, notify: notify, done: make(chan struct{})}
	if err := w.addTree(root); err != nil {
		_ = inner.Close()
		return nil, err
	}
	w.wg.Add(1)
	go w.run()
	return w, nil
}

func (w *Watcher) Close() error {
	var err error
	w.once.Do(func() {
		close(w.done)
		err = w.inner.Close()
		w.wg.Wait()
	})
	return err
}

func (w *Watcher) run() {
	defer w.wg.Done()
	for {
		select {
		case <-w.done:
			return
		case event, ok := <-w.inner.Events:
			if !ok {
				return
			}
			if w.excluded(event.Name) {
				continue
			}
			if event.Has(fsnotify.Create) {
				if info, err := os.Lstat(event.Name); err == nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
					_ = w.addTree(event.Name)
				}
			}
			if event.Op&(fsnotify.Create|fsnotify.Write|fsnotify.Remove|fsnotify.Rename|fsnotify.Chmod) != 0 && w.notify != nil {
				w.notify()
			}
		case _, ok := <-w.inner.Errors:
			if !ok {
				return
			}
			// A watcher error is not source evidence. The explicit refresh path
			// remains the recovery mechanism for missed notifications.
		}
	}
}

func (w *Watcher) addTree(root string) error {
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return filepath.SkipDir
		}
		rel, err := filepath.Rel(w.root, path)
		if err != nil {
			return err
		}
		if rel != "." && manifest.Excluded(filepath.ToSlash(rel), true) {
			return filepath.SkipDir
		}
		return w.inner.Add(path)
	})
}

func (w *Watcher) excluded(path string) bool {
	rel, err := filepath.Rel(w.root, path)
	if err != nil {
		return true
	}
	if rel == "." {
		return false
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return true
	}
	info, statErr := os.Lstat(path)
	return manifest.Excluded(filepath.ToSlash(rel), statErr == nil && info.IsDir())
}
