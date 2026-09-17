package analyzer

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

// Common sentinel errors for CodeGraph operations.
var (
	ErrNotIndexed     = errors.New("codegraph index not found")
	ErrDatabaseLocked = errors.New("codegraph database locked or busy")
	ErrHeadMismatch   = errors.New("git HEAD does not match codegraph index")
)

// CandidateEntry represents an entrypoint discovered via CodeGraph static analysis.
type CandidateEntry struct {
	Name       string `json:"name"`
	SymbolPath string `json:"symbolPath"`
	File       string `json:"file"`
	Line       int    `json:"line"`
	Kind       string `json:"kind"`
}

// RadarNeighbors represents the 1-hop direct callers and callees for impact visualization.
type RadarNeighbors struct {
	DirectCallers []string `json:"directCallers"`
	DirectCallees []string `json:"directCallees"`
}

// CodeGraphClient defines the public interface for global static call-graph acceleration.
type CodeGraphClient interface {
	RepoRoot() string
	FindEntrypoints(ctx context.Context, repoRoot string) ([]CandidateEntry, error)
	ResolveDynamicDispatch(ctx context.Context, callerSymbol string) ([]string, error)
	GetDirectNeighbors(ctx context.Context, symbol string) (RadarNeighbors, error)
	IsHeadSynced(ctx context.Context, repoRoot string) (bool, error)
}

// DefaultCodeGraphClient provides high-speed queries against local CodeGraph SQLite indexes.
type DefaultCodeGraphClient struct {
	repoRoot string
	dbPath   string
}

// NewDefaultCodeGraphClient creates a DefaultCodeGraphClient targeting the given repository.
func NewDefaultCodeGraphClient(repoRoot string) *DefaultCodeGraphClient {
	return &DefaultCodeGraphClient{
		repoRoot: repoRoot,
		dbPath:   filepath.Join(repoRoot, ".codegraph", "codegraph.db"),
	}
}

// RepoRoot returns the configured repository root path.
func (c *DefaultCodeGraphClient) RepoRoot() string {
	return c.repoRoot
}

// ResolveGitHead resolves .git/HEAD into a 40-character commit SHA-1.
// Supports direct commits, loose ref files (refs/heads/...), and .git/packed-refs.
func ResolveGitHead(gitDir string) (string, error) {
	fi, err := os.Stat(gitDir)
	if err != nil {
		return "", fmt.Errorf("stat git dir: %w", err)
	}
	if !fi.IsDir() {
		// In git worktrees or submodules, .git is a file with "gitdir: <path>"
		data, err := os.ReadFile(gitDir)
		if err != nil {
			return "", fmt.Errorf("read git pointer: %w", err)
		}
		text := strings.TrimSpace(string(data))
		if strings.HasPrefix(text, "gitdir:") {
			realDir := strings.TrimSpace(strings.TrimPrefix(text, "gitdir:"))
			if !filepath.IsAbs(realDir) {
				realDir = filepath.Join(filepath.Dir(gitDir), realDir)
			}
			gitDir = realDir
		}
	}

	headFile := filepath.Join(gitDir, "HEAD")
	data, err := os.ReadFile(headFile)
	if err != nil {
		return "", fmt.Errorf("read HEAD: %w", err)
	}

	content := strings.TrimSpace(string(data))
	depth := 0

	for strings.HasPrefix(content, "ref:") && depth < 5 {
		depth++
		refPath := strings.TrimSpace(strings.TrimPrefix(content, "ref:"))

		// 1. Try loose ref file
		looseRef := filepath.Join(gitDir, refPath)
		if refData, err := os.ReadFile(looseRef); err == nil {
			content = strings.TrimSpace(string(refData))
			continue
		}

		// 2. Try packed-refs
		packedPath := filepath.Join(gitDir, "packed-refs")
		packedData, err := os.ReadFile(packedPath)
		if err != nil {
			return "", fmt.Errorf("resolve ref %s: %w", refPath, err)
		}

		found := false
		scanner := bufio.NewScanner(strings.NewReader(string(packedData)))
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if strings.HasPrefix(line, "#") || strings.HasPrefix(line, "^") || line == "" {
				continue
			}
			parts := strings.Fields(line)
			if len(parts) >= 2 && parts[1] == refPath {
				content = parts[0]
				found = true
				break
			}
		}
		if !found {
			return "", fmt.Errorf("ref %s not found in loose or packed-refs", refPath)
		}
	}

	if len(content) == 40 {
		return content, nil
	}
	return "", fmt.Errorf("invalid commit SHA: %q", content)
}

// IsHeadSynced checks whether the Git working tree HEAD matches the CodeGraph index.
// If .codegraph does not exist, returns (false, ErrNotIndexed).
// If the SQLite database is locked or busy, returns (false, ErrDatabaseLocked).
func (c *DefaultCodeGraphClient) IsHeadSynced(ctx context.Context, repoRoot string) (bool, error) {
	if repoRoot == "" {
		repoRoot = c.repoRoot
	}
	codegraphDir := filepath.Join(repoRoot, ".codegraph")
	if _, err := os.Stat(codegraphDir); os.IsNotExist(err) {
		return false, ErrNotIndexed
	}

	gitDir := filepath.Join(repoRoot, ".git")
	if _, err := os.Stat(gitDir); os.IsNotExist(err) {
		return false, nil
	}
	headSHA, err := ResolveGitHead(gitDir)
	if err != nil {
		return false, fmt.Errorf("resolve git head: %w", err)
	}

	// Check metadata files: head, commit, HEAD
	for _, fname := range []string{"head", "commit", "HEAD"} {
		path := filepath.Join(codegraphDir, fname)
		if data, err := os.ReadFile(path); err == nil {
			indexedSHA := strings.TrimSpace(string(data))
			if indexedSHA == headSHA {
				return true, nil
			}
			return false, nil
		}
	}

	// Check SQLite database meta table if present
	dbFile := filepath.Join(codegraphDir, "codegraph.db")
	if _, err := os.Stat(dbFile); err == nil {
		db, err := sql.Open("sqlite", "file:"+dbFile+"?mode=ro&_busy_timeout=100")
		if err != nil {
			return false, err
		}
		defer db.Close()

		var indexedSHA string
		row := db.QueryRowContext(ctx, "SELECT value FROM meta WHERE key IN ('head_commit', 'commit_sha', 'head') LIMIT 1")
		if err := row.Scan(&indexedSHA); err != nil {
			if strings.Contains(err.Error(), "busy") || strings.Contains(err.Error(), "locked") {
				return false, ErrDatabaseLocked
			}
			return false, nil
		}
		return indexedSHA == headSHA, nil
	}

	return false, nil
}

// FindEntrypoints rapidly queries entrypoints and route handlers (<5ms).
func (c *DefaultCodeGraphClient) FindEntrypoints(ctx context.Context, repoRoot string) ([]CandidateEntry, error) {
	if repoRoot == "" {
		repoRoot = c.repoRoot
	}
	dbFile := filepath.Join(repoRoot, ".codegraph", "codegraph.db")
	if _, err := os.Stat(dbFile); os.IsNotExist(err) {
		return nil, ErrNotIndexed
	}

	db, err := sql.Open("sqlite", "file:"+dbFile+"?mode=ro&_busy_timeout=100")
	if err != nil {
		return nil, err
	}
	defer db.Close()

	rows, err := db.QueryContext(ctx, `
		SELECT name, symbol_path, file, line, kind 
		FROM symbols 
		WHERE kind IN ('route', 'entry', 'handler', 'main', 'gateway')
		ORDER BY line ASC
	`)
	if err != nil {
		if strings.Contains(err.Error(), "busy") || strings.Contains(err.Error(), "locked") {
			return nil, ErrDatabaseLocked
		}
		return nil, err
	}
	defer rows.Close()

	var results []CandidateEntry
	for rows.Next() {
		var e CandidateEntry
		if err := rows.Scan(&e.Name, &e.SymbolPath, &e.File, &e.Line, &e.Kind); err != nil {
			return nil, err
		}
		results = append(results, e)
	}
	return results, nil
}

// ResolveDynamicDispatch queries concrete implementation callees for an interface or virtual call.
func (c *DefaultCodeGraphClient) ResolveDynamicDispatch(ctx context.Context, callerSymbol string) ([]string, error) {
	dbFile := filepath.Join(c.repoRoot, ".codegraph", "codegraph.db")
	if _, err := os.Stat(dbFile); os.IsNotExist(err) {
		return nil, ErrNotIndexed
	}

	db, err := sql.Open("sqlite", "file:"+dbFile+"?mode=ro&_busy_timeout=100")
	if err != nil {
		return nil, err
	}
	defer db.Close()

	rows, err := db.QueryContext(ctx, `
		SELECT callee_symbol 
		FROM callees 
		WHERE caller_symbol = ? AND kind = 'implementation'
	`, callerSymbol)
	if err != nil {
		if strings.Contains(err.Error(), "busy") || strings.Contains(err.Error(), "locked") {
			return nil, ErrDatabaseLocked
		}
		return nil, err
	}
	defer rows.Close()

	var callees []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		callees = append(callees, s)
	}
	return callees, nil
}

// GetDirectNeighbors returns direct callers and callees for impact radar visualization (<10ms).
func (c *DefaultCodeGraphClient) GetDirectNeighbors(ctx context.Context, symbol string) (RadarNeighbors, error) {
	var radar RadarNeighbors
	dbFile := filepath.Join(c.repoRoot, ".codegraph", "codegraph.db")
	if _, err := os.Stat(dbFile); os.IsNotExist(err) {
		return radar, ErrNotIndexed
	}

	db, err := sql.Open("sqlite", "file:"+dbFile+"?mode=ro&_busy_timeout=100")
	if err != nil {
		return radar, err
	}
	defer db.Close()

	// Direct callers
	callersRows, err := db.QueryContext(ctx, `
		SELECT caller_symbol FROM callers WHERE callee_symbol = ?
	`, symbol)
	if err == nil {
		defer callersRows.Close()
		for callersRows.Next() {
			var s string
			if err := callersRows.Scan(&s); err == nil {
				radar.DirectCallers = append(radar.DirectCallers, s)
			}
		}
	}

	// Direct callees
	calleesRows, err := db.QueryContext(ctx, `
		SELECT callee_symbol FROM callees WHERE caller_symbol = ?
	`, symbol)
	if err == nil {
		defer calleesRows.Close()
		for calleesRows.Next() {
			var s string
			if err := calleesRows.Scan(&s); err == nil {
				radar.DirectCallees = append(radar.DirectCallees, s)
			}
		}
	}

	return radar, nil
}
