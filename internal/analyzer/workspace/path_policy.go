package workspace

import (
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// NormalizeRepositoryPath validates and canonicalizes a repository-relative
// path. It rejects absolute paths and traversal before any filesystem call.
func NormalizeRepositoryPath(relPath string) (string, error) {
	if relPath == "" {
		return "", &PathPolicyError{Path: relPath, Reason: "path must not be empty"}
	}
	if !utf8.ValidString(relPath) {
		return "", &PathPolicyError{Path: relPath, Reason: "path is not valid UTF-8"}
	}
	if strings.IndexByte(relPath, 0) >= 0 {
		return "", &PathPolicyError{Path: relPath, Reason: "path contains NUL"}
	}
	// Accept slash-separated protocol paths only. Backslashes are rejected
	// rather than interpreted differently on different host platforms.
	if strings.Contains(relPath, "\\") || filepath.IsAbs(relPath) || path.IsAbs(relPath) {
		return "", &PathPolicyError{Path: relPath, Reason: "path must be repository-relative"}
	}
	parts := strings.Split(relPath, "/")
	for _, part := range parts {
		if part == ".." {
			return "", &PathPolicyError{Path: relPath, Reason: "path traversal is not allowed"}
		}
	}
	norm := path.Clean(strings.Join(parts, "/"))
	if norm == "." || norm == "" || norm == ".." || strings.HasPrefix(norm, "../") {
		return "", &PathPolicyError{Path: relPath, Reason: "path escapes repository root"}
	}
	return norm, nil
}

func validateRepositoryPath(root, relPath string, allowMissing bool) (string, error) {
	norm, err := NormalizeRepositoryPath(relPath)
	if err != nil {
		return "", err
	}
	full := filepath.Join(root, filepath.FromSlash(norm))
	// EvalSymlinks requires the final path to exist. Resolve the nearest
	// existing parent for newly-created virtual edit paths.
	checkPath := full
	if allowMissing {
		for {
			if _, statErr := os.Lstat(checkPath); statErr == nil {
				break
			} else if !os.IsNotExist(statErr) {
				return "", statErr
			}
			parent := filepath.Dir(checkPath)
			if parent == checkPath {
				break
			}
			checkPath = parent
		}
	}
	resolved, err := filepath.EvalSymlinks(checkPath)
	if err != nil {
		if allowMissing && os.IsNotExist(err) {
			resolved = checkPath
		} else {
			return "", &PathPolicyError{Path: relPath, Reason: "cannot resolve path"}
		}
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	inside, err := filepath.Rel(resolvedRoot, resolved)
	if err != nil || inside == ".." || strings.HasPrefix(inside, ".."+string(filepath.Separator)) || filepath.IsAbs(inside) {
		return "", &PathPolicyError{Path: relPath, Reason: "symlink escapes repository root"}
	}
	return norm, nil
}
