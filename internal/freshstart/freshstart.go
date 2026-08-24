// Package freshstart implements decision #16 (design-v2 §15): CodeFlow v2
// starts fresh — legacy v1 artifacts inside .codeflow are detected and
// removed wholesale; there is no importer.
package freshstart

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"codeflow/internal/workspace"
)

const codeflowDirName = ".codeflow"

// v1TopLevelMarkers are entry names the v1 core wrote directly under
// .codeflow of a target repo (runtime.json, codeflow.lock, state.db,
// cache/baselines, knowledge). None of them are valid v2 layout names, so
// their presence is unambiguous evidence of v1 leftovers.
var v1TopLevelMarkers = map[string]bool{
	"runtime.json":  true,
	"codeflow.lock": true,
	"state.db":      true,
	"cache":         true,
	"knowledge":     true,
}

// ScanV1Remnants returns the absolute paths of v1 leftovers under
// repoRoot/.codeflow. Heuristics:
//
//   - any file or directory whose name contains "flowir" (v1 FlowIR dumps),
//     anywhere in the tree;
//   - any top-level v1 marker name listed above;
//   - an ir/ directory when no valid v2 workspace.json exists (an ir/ tree
//     in a v2-initialized workspace is legitimate publish output).
//
// A missing or non-directory .codeflow yields a nil slice with no error.
func ScanV1Remnants(repoRoot string) ([]string, error) {
	cfDir := filepath.Join(repoRoot, codeflowDirName)
	info, err := os.Stat(cfDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("scan v1 remnants: %w", err)
	}
	if !info.IsDir() {
		return nil, nil
	}

	v2Initialized := hasV2Workspace(cfDir)
	remnants := make([]string, 0, 4)
	seen := map[string]bool{}
	add := func(path string) {
		if path != "" && path != cfDir && !seen[path] {
			seen[path] = true
			remnants = append(remnants, path)
		}
	}

	entries, err := os.ReadDir(cfDir)
	if err != nil {
		return nil, fmt.Errorf("scan v1 remnants: read %s: %w", cfDir, err)
	}
	for _, entry := range entries {
		name := entry.Name()
		full := filepath.Join(cfDir, name)
		switch {
		case strings.Contains(strings.ToLower(name), "flowir"):
			add(full)
		case v1TopLevelMarkers[name]:
			add(full)
		case name == "ir" && entry.IsDir() && !v2Initialized:
			add(full)
		}
	}

	walkErr := filepath.WalkDir(cfDir, func(path string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if path == cfDir {
			return nil
		}
		if seen[path] {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if strings.Contains(strings.ToLower(d.Name()), "flowir") {
			add(path)
			if d.IsDir() {
				return fs.SkipDir
			}
		}
		return nil
	})
	if walkErr != nil {
		return nil, fmt.Errorf("scan v1 remnants: walk %s: %w", cfDir, walkErr)
	}

	slices.Sort(remnants)
	return remnants, nil
}

// Purge deletes every path in list after verifying that each one lies
// strictly inside repoRoot/.codeflow. Any escaping path aborts the whole
// purge before a single deletion happens.
func Purge(repoRoot string, list []string) error {
	cfRoot, err := filepath.Abs(filepath.Join(repoRoot, codeflowDirName))
	if err != nil {
		return fmt.Errorf("purge v1 remnants: resolve %s: %w", codeflowDirName, err)
	}
	validated := make([]string, 0, len(list))
	for _, path := range list {
		if err := ensureInsideCodeflow(cfRoot, path); err != nil {
			return fmt.Errorf("purge v1 remnants: refusing %q: %w", path, err)
		}
		validated = append(validated, path)
	}
	var errs []error
	for _, path := range validated {
		if err := os.RemoveAll(path); err != nil {
			errs = append(errs, fmt.Errorf("purge %s: %w", path, err))
		}
	}
	return errors.Join(errs...)
}

func ensureInsideCodeflow(cfRoot, target string) error {
	abs, err := filepath.Abs(target)
	if err != nil {
		return fmt.Errorf("resolve path: %w", err)
	}
	rel, err := filepath.Rel(cfRoot, abs)
	if err != nil {
		return fmt.Errorf("relate to %s: %w", cfRoot, err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return fmt.Errorf("path escapes %s", codeflowDirName)
	}
	if rel == "." {
		return fmt.Errorf("refusing to delete the entire %s directory", codeflowDirName)
	}
	return nil
}

// hasV2Workspace reports whether cfDir holds a workspace.json that parses
// and declares the current schema version.
func hasV2Workspace(cfDir string) bool {
	data, err := os.ReadFile(filepath.Join(cfDir, workspace.FileName))
	if err != nil {
		return false
	}
	var probe struct {
		SchemaVersion string `json:"schemaVersion"`
	}
	if json.Unmarshal(data, &probe) != nil {
		return false
	}
	return probe.SchemaVersion == workspace.SchemaVersion
}
