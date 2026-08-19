// Package manifest captures a consistent, evidence-only view of a worktree.
package manifest

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"codeflow/core/internal/flowir"
)

var ErrChanging = errors.New("worktree changed while CodeFlow was observing it")

// Options exists primarily to make the observation boundary testable. Hooks
// are called only between the first observation and its mandatory re-read.
type Options struct{ BeforeVerify func() }

// Capture reads the worktree, verifies every relevant observed path once, and
// retries the complete observation one time. It never changes the repository.
func Capture(repo string) (flowir.Basis, error) { return CaptureWithOptions(repo, Options{}) }

func CaptureWithOptions(repo string, options Options) (flowir.Basis, error) {
	root, err := filepath.Abs(repo)
	if err != nil {
		return flowir.Basis{}, err
	}
	for attempt := 0; attempt < 2; attempt++ {
		basis, err := observe(root)
		if err != nil {
			return flowir.Basis{}, err
		}
		if options.BeforeVerify != nil {
			options.BeforeVerify()
		}
		if verify(root, basis.Manifest) == nil {
			// A second Git/filesystem listing catches paths that appeared or
			// disappeared between the initial walk and the hash re-read.
			rechecked, checkErr := observe(root)
			if checkErr == nil && rechecked.WorktreeFingerprint == basis.WorktreeFingerprint && rechecked.HeadRevision == basis.HeadRevision {
				return basis, nil
			}
		}
	}
	return flowir.Basis{}, ErrChanging
}

func observe(root string) (flowir.Basis, error) {
	git := inspectGit(root)
	entries := make([]flowir.ManifestEntry, 0)
	err := filepath.WalkDir(root, func(full string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if full == root {
			return nil
		}
		rel, err := filepath.Rel(root, full)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if excludedDirectory(rel) {
				return filepath.SkipDir
			}
			return nil
		}
		if excludedFile(rel) {
			return nil
		}
		entry, err := fileEntry(root, rel, d, git.states[rel])
		if err != nil {
			return err
		}
		entries = append(entries, entry)
		return nil
	})
	if err != nil {
		return flowir.Basis{}, fmt.Errorf("walk worktree: %w", err)
	}
	// A deletion has no filesystem entry, but it is still authoritative Git evidence.
	for path, state := range git.states {
		if state != "deleted" || contains(entries, path) || excludedFile(path) {
			continue
		}
		entries = append(entries, flowir.ManifestEntry{Path: path, Type: "missing", Mode: "0000", GitState: "deleted", Generated: generated(path)})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	dirty := !git.available
	for _, entry := range entries {
		if entry.GitState != "clean" {
			dirty = true
			break
		}
	}
	return flowir.Basis{Repository: root, HeadRevision: git.head, WorktreeFingerprint: fingerprint(entries), Dirty: dirty, Manifest: entries}, nil
}

func fileEntry(root, rel string, d fs.DirEntry, state string) (flowir.ManifestEntry, error) {
	info, err := d.Info()
	if err != nil {
		return flowir.ManifestEntry{}, err
	}
	entry := flowir.ManifestEntry{Path: rel, Mode: fmt.Sprintf("%04o", info.Mode().Perm()), GitState: state, Generated: generated(rel)}
	if entry.GitState == "" {
		entry.GitState = "untracked"
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			return flowir.ManifestEntry{}, err
		}
		entry.Type, entry.FileHash = "symlink", flowir.SHA256Bytes([]byte(target))
		return entry, nil
	}
	if !info.Mode().IsRegular() {
		entry.Type = "other"
		return entry, nil
	}
	bytes, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return flowir.ManifestEntry{}, err
	}
	entry.Type, entry.FileHash = "file", flowir.SHA256Bytes(bytes)
	return entry, nil
}

func verify(root string, entries []flowir.ManifestEntry) error {
	for _, expected := range entries {
		full := filepath.Join(root, filepath.FromSlash(expected.Path))
		info, err := os.Lstat(full)
		if expected.Type == "missing" {
			if err == nil {
				return ErrChanging
			}
			if os.IsNotExist(err) {
				continue
			}
			return ErrChanging
		}
		if err != nil {
			return ErrChanging
		}
		mode := fmt.Sprintf("%04o", info.Mode().Perm())
		if mode != expected.Mode {
			return ErrChanging
		}
		var actualType, actualHash string
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(full)
			if err != nil {
				return ErrChanging
			}
			actualType, actualHash = "symlink", flowir.SHA256Bytes([]byte(target))
		} else if info.Mode().IsRegular() {
			bytes, err := os.ReadFile(full)
			if err != nil {
				return ErrChanging
			}
			actualType, actualHash = "file", flowir.SHA256Bytes(bytes)
		} else {
			actualType = "other"
		}
		if actualType != expected.Type || actualHash != expected.FileHash {
			return ErrChanging
		}
	}
	return nil
}

type gitInfo struct {
	available bool
	head      string
	states    map[string]string
}

func inspectGit(root string) gitInfo {
	result := gitInfo{head: "unavailable", states: map[string]string{}}
	head, err := exec.Command("git", "-C", root, "rev-parse", "HEAD").Output()
	if err != nil {
		return result
	}
	result.available, result.head = true, strings.TrimSpace(string(head))
	tracked, err := exec.Command("git", "-C", root, "ls-files", "-z").Output()
	if err == nil {
		for _, path := range splitZero(tracked) {
			result.states[path] = "clean"
		}
	}
	status, err := exec.Command("git", "-C", root, "status", "--porcelain=v1", "-z", "--untracked-files=all").Output()
	if err != nil {
		return result
	}
	fields := splitZero(status)
	for i := 0; i < len(fields); i++ {
		field := fields[i]
		if len(field) < 4 {
			continue
		}
		xy, name := field[:2], filepath.ToSlash(field[3:])
		state := porcelainState(xy)
		if state == "renamed" {
			result.states[name] = "renamed"
			// In -z porcelain output the original name follows a rename record.
			if i+1 < len(fields) {
				i++
			}
			continue
		}
		result.states[name] = state
	}
	return result
}
func splitZero(raw []byte) []string {
	out := []string{}
	for _, part := range strings.Split(string(raw), "\x00") {
		if part != "" {
			out = append(out, filepath.ToSlash(part))
		}
	}
	return out
}
func porcelainState(xy string) string {
	if xy == "??" {
		return "untracked"
	}
	if strings.Contains(xy, "R") || strings.Contains(xy, "C") {
		return "renamed"
	}
	if strings.Contains(xy, "D") {
		return "deleted"
	}
	if strings.Contains(xy, "A") {
		return "added"
	}
	if strings.Contains(xy, "M") || strings.Contains(xy, "T") {
		return "modified"
	}
	return "clean"
}
func excludedDirectory(path string) bool {
	base := filepath.Base(path)
	return base == ".git" || base == ".codeflow" || base == "build" || base == ".dart_tool" || base == "node_modules" || base == ".idea" || base == ".gradle"
}
func excludedFile(path string) bool {
	base := filepath.Base(path)
	return base == ".env" || strings.HasPrefix(base, ".env.") || strings.HasSuffix(base, ".pem") || strings.HasSuffix(base, ".key")
}
func generated(path string) bool {
	return strings.HasSuffix(path, ".g.dart") || strings.HasSuffix(path, ".freezed.dart") || strings.HasSuffix(path, ".gen.dart")
}
func contains(entries []flowir.ManifestEntry, path string) bool {
	for _, e := range entries {
		if e.Path == path {
			return true
		}
	}
	return false
}
func fingerprint(entries []flowir.ManifestEntry) string {
	h := sha256.New()
	for _, e := range entries {
		for _, part := range []string{e.Path, e.Type, e.Mode, e.FileHash, e.GitState, fmt.Sprintf("%t", e.Generated)} {
			_, _ = h.Write([]byte(part))
			_, _ = h.Write([]byte{0})
		}
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}
