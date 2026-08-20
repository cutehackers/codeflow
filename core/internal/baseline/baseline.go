// Package baseline resolves immutable Git revisions and materializes the
// corresponding tree from local Git objects. It never checks out a revision
// or contacts a remote.
package baseline

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"codeflow/core/internal/flowir"
)

type Mirror struct {
	Revision string
	Root     string
	Basis    flowir.Basis
}

const (
	retainedMirrors     = 3
	hardRetainedMirrors = 8
)

type CacheEntry struct {
	Revision string    `json:"revision"`
	Bytes    int64     `json:"bytes"`
	LastUsed time.Time `json:"last_used"`
}

type CacheReport struct {
	Root           string       `json:"root"`
	TotalBytes     int64        `json:"total_bytes"`
	RetentionLimit int          `json:"retention_limit"`
	HardLimit      int          `json:"hard_retention_limit"`
	Baselines      []CacheEntry `json:"baselines"`
}

func Resolve(repo, revision string) (string, error) {
	if revision == "" {
		return "", fmt.Errorf("BASELINE_REQUIRED: select a local Git revision")
	}
	out, err := git(repo, "rev-parse", "--verify", revision+"^{commit}")
	if err != nil {
		return "", fmt.Errorf("BASELINE_NOT_FOUND: %s is not a local commit", revision)
	}
	return strings.TrimSpace(string(out)), nil
}

// Materialize writes only CodeFlow's cache. The tree is read with ls-tree and
// cat-file; checkout, worktree mutation, and network access are not used.
func Materialize(repo, revision string) (Mirror, error) {
	sha, err := Resolve(repo, revision)
	if err != nil {
		return Mirror{}, err
	}
	parent := filepath.Join(repo, ".codeflow", "cache", "baselines")
	root := filepath.Join(parent, sha)
	entries, err := tree(repo, sha)
	if err != nil {
		return Mirror{}, err
	}
	if err := os.MkdirAll(parent, 0755); err != nil {
		return Mirror{}, err
	}
	staging, err := os.MkdirTemp(parent, ".mirror-")
	if err != nil {
		return Mirror{}, err
	}
	defer os.RemoveAll(staging)
	manifest := make([]flowir.ManifestEntry, 0, len(entries))
	for _, entry := range entries {
		if !analysisInput(entry.path) {
			continue
		}
		if strings.HasPrefix(entry.path, "../") || filepath.IsAbs(entry.path) {
			return Mirror{}, fmt.Errorf("invalid Git tree path %q", entry.path)
		}
		bytes, err := git(repo, "cat-file", "blob", entry.object)
		if err != nil {
			return Mirror{}, fmt.Errorf("read baseline blob %s: %w", entry.path, err)
		}
		full := filepath.Join(staging, filepath.FromSlash(entry.path))
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			return Mirror{}, err
		}
		mode, err := strconv.ParseUint(entry.mode, 8, 32)
		if err != nil {
			return Mirror{}, err
		}
		kind := "file"
		if entry.mode == "120000" {
			kind = "symlink"
			if _, err := os.Lstat(full); os.IsNotExist(err) {
				if err := os.Symlink(string(bytes), full); err != nil {
					return Mirror{}, err
				}
			}
		} else if _, err := os.Lstat(full); os.IsNotExist(err) {
			if err := os.WriteFile(full, bytes, os.FileMode(mode)&0777); err != nil {
				return Mirror{}, err
			}
		}
		manifest = append(manifest, flowir.ManifestEntry{Path: entry.path, Type: kind, Mode: fmt.Sprintf("%04o", mode&0777), FileHash: flowir.SHA256Bytes(bytes), GitState: "clean", Generated: false})
	}
	// Publish a complete mirror in one rename. Rebuilding also migrates legacy
	// mirrors that copied unrelated documentation, binaries, and assets.
	old := root + ".old"
	_ = os.RemoveAll(old)
	if _, statErr := os.Lstat(root); statErr == nil {
		if err := os.Rename(root, old); err != nil {
			return Mirror{}, err
		}
	}
	if err := os.Rename(staging, root); err != nil {
		_ = os.Rename(old, root)
		return Mirror{}, err
	}
	_ = os.RemoveAll(old)
	now := time.Now()
	_ = os.Chtimes(root, now, now)
	_ = prune(repo, sha, retainedMirrors)
	sort.Slice(manifest, func(i, j int) bool { return manifest[i].Path < manifest[j].Path })
	parts := []string{sha}
	for _, e := range manifest {
		parts = append(parts, e.Path, e.Type, e.Mode, e.FileHash)
	}
	return Mirror{Revision: sha, Root: root, Basis: flowir.Basis{Repository: root, HeadRevision: sha, BaselineRevision: sha, WorktreeFingerprint: flowir.Hash(parts...), Dirty: false, Manifest: manifest}}, nil
}

// analysisInput is deliberately narrow: a baseline mirror exists only to run
// the Dart analyzer and CodeFlow's repository contracts. It is not a checkout.
func analysisInput(path string) bool {
	path = filepath.ToSlash(path)
	base := filepath.Base(path)
	if strings.HasSuffix(base, ".dart") {
		return true
	}
	switch base {
	case "pubspec.yaml", "pubspec_overrides.yaml", "analysis_options.yaml", "codeflow.yaml", "codeflow.external-contracts.json":
		return true
	default:
		return false
	}
}

func InspectCache(repo string) (CacheReport, error) {
	root := filepath.Join(repo, ".codeflow", "cache", "baselines")
	report := CacheReport{Root: root, RetentionLimit: retainedMirrors, HardLimit: hardRetainedMirrors, Baselines: []CacheEntry{}}
	items, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return report, nil
	}
	if err != nil {
		return report, err
	}
	for _, item := range items {
		if !item.IsDir() || !revisionName(item.Name()) {
			continue
		}
		info, err := item.Info()
		if err != nil {
			return report, err
		}
		bytes, err := directoryBytes(filepath.Join(root, item.Name()))
		if err != nil {
			return report, err
		}
		report.TotalBytes += bytes
		report.Baselines = append(report.Baselines, CacheEntry{Revision: item.Name(), Bytes: bytes, LastUsed: info.ModTime()})
	}
	sort.Slice(report.Baselines, func(i, j int) bool { return report.Baselines[i].LastUsed.After(report.Baselines[j].LastUsed) })
	return report, nil
}

// CleanCache deletes only reconstructable baseline mirrors. State, runtime,
// knowledge, configuration, and product files are outside this boundary.
func CleanCache(repo string) error {
	return os.RemoveAll(filepath.Join(repo, ".codeflow", "cache", "baselines"))
}

func prune(repo, keep string, limit int) error {
	report, err := InspectCache(repo)
	if err != nil {
		return err
	}
	selected := map[string]bool{}
	if keep != "" {
		selected[keep] = true
	}
	for _, entry := range report.Baselines {
		if len(selected) < limit {
			selected[entry.Revision] = true
		}
	}
	retained := 0
	for _, entry := range report.Baselines {
		if selected[entry.Revision] {
			retained++
			continue
		}
		// A recently touched mirror may belong to a concurrent comparison.
		// That grace period is itself hard-bounded, so repeated comparisons can
		// never grow the reconstructable cache without limit.
		if time.Since(entry.LastUsed) < time.Hour && retained < hardRetainedMirrors {
			retained++
			continue
		}
		if err := os.RemoveAll(filepath.Join(report.Root, entry.Revision)); err != nil {
			return err
		}
	}
	return nil
}

func revisionName(name string) bool {
	if len(name) != 40 && len(name) != 64 {
		return false
	}
	for _, r := range name {
		if !strings.ContainsRune("0123456789abcdef", r) {
			return false
		}
	}
	return true
}

func directoryBytes(root string) (int64, error) {
	var total int64
	err := filepath.Walk(root, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			total += info.Size()
		}
		return nil
	})
	return total, err
}

type treeEntry struct{ mode, object, path string }

func tree(repo, sha string) ([]treeEntry, error) {
	out, err := git(repo, "ls-tree", "-r", "-z", "--full-tree", sha)
	if err != nil {
		return nil, err
	}
	var result []treeEntry
	for _, raw := range strings.Split(string(out), "\x00") {
		if raw == "" {
			continue
		}
		parts := strings.SplitN(raw, "\t", 2)
		fields := strings.Fields(parts[0])
		if len(parts) != 2 || len(fields) != 3 || fields[1] != "blob" {
			continue
		}
		result = append(result, treeEntry{fields[0], fields[2], filepath.ToSlash(parts[1])})
	}
	return result, nil
}
func git(repo string, args ...string) ([]byte, error) {
	return exec.Command("git", append([]string{"-C", repo}, args...)...).Output()
}
