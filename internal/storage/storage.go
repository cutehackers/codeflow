// Package storage manages the .codeflow disk layout, caching, and atomic commit pointer
// (design §6.1, tickets 09/10).
package storage

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"

	"codeflow/internal/secret"
)

// Storage handles reads and writes under <repoRoot>/.codeflow.
type Storage struct {
	repoRoot string
	baseDir  string
}

// Pointer describes the currently published generation.
type Pointer struct {
	GenerationID     string    `json:"generationId"`
	PublishedAt      time.Time `json:"publishedAt"`
	BasisFingerprint string    `json:"basisFingerprint"`
	FlowCount        int       `json:"flowCount"`
}

// FlowSummary is a lightweight entry in the generation index.
type FlowSummary struct {
	FlowID          string `json:"flowId"`
	Title           string `json:"title"`
	Description     string `json:"description,omitempty"`
	EntrySymbolPath string `json:"entrySymbolPath"`
	StepCount       int    `json:"stepCount"`
	HasStaleSteps   bool   `json:"hasStaleSteps"`
	HasUnknownSteps bool   `json:"hasUnknownSteps"`
}

// GenerationIndex maps all published flows in a generation.
type GenerationIndex struct {
	GenerationID     string        `json:"generationId"`
	PublishedAt      time.Time     `json:"publishedAt"`
	BasisFingerprint string        `json:"basisFingerprint"`
	Flows            []FlowSummary `json:"flows"`
}

// New creates a Storage manager for the given repo root.
func New(repoRoot string) *Storage {
	return &Storage{
		repoRoot: repoRoot,
		baseDir:  filepath.Join(repoRoot, ".codeflow"),
	}
}

// BaseDir returns the .codeflow directory path.
func (s *Storage) BaseDir() string {
	return s.baseDir
}

// InitLayout initializes the standard .codeflow directory structure.
func (s *Storage) InitLayout() error {
	dirs := []string{
		filepath.Join(s.baseDir, "facts", "ast"),
		filepath.Join(s.baseDir, "facts", "slice"),
		filepath.Join(s.baseDir, "semantics", "events"),
		filepath.Join(s.baseDir, "semantics", "views"),
		filepath.Join(s.baseDir, "ir"),
		filepath.Join(s.baseDir, "generations"),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return fmt.Errorf("init directory %s: %w", d, err)
		}
	}
	return nil
}

// ComputeWorktreeFingerprint calculates a deterministic sha256 fingerprint of the files
// in the repo (or given paths).
func ComputeWorktreeFingerprint(repoRoot string, relPaths []string) (string, error) {
	paths := make([]string, len(relPaths))
	copy(paths, relPaths)
	sort.Strings(paths)

	h := sha256.New()
	for _, p := range paths {
		fullPath := filepath.Join(repoRoot, p)
		data, err := os.ReadFile(fullPath)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return "", fmt.Errorf("read %s: %w", p, err)
		}
		fileHash := sha256.Sum256(data)
		fmt.Fprintf(h, "%s:%s\n", p, hex.EncodeToString(fileHash[:]))
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// ReadPointer loads the current pointer.json, or returns nil if none published yet.
func (s *Storage) ReadPointer() (*Pointer, error) {
	path := filepath.Join(s.baseDir, "pointer.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read pointer: %w", err)
	}
	var p Pointer
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("unmarshal pointer: %w", err)
	}
	return &p, nil
}

// ReadLatestIndex loads the index.json of the currently active generation.
func (s *Storage) ReadLatestIndex() (*GenerationIndex, error) {
	ptr, err := s.ReadPointer()
	if err != nil {
		return nil, err
	}
	if ptr == nil {
		return nil, nil
	}
	indexPath := filepath.Join(s.baseDir, "generations", ptr.GenerationID, "index.json")
	data, err := os.ReadFile(indexPath)
	if err != nil {
		return nil, fmt.Errorf("read generation index %s: %w", indexPath, err)
	}
	var idx GenerationIndex
	if err := json.Unmarshal(data, &idx); err != nil {
		return nil, fmt.Errorf("unmarshal generation index: %w", err)
	}
	return &idx, nil
}

// ReadFlowSpec loads a specific FlowSpec JSON for a generation.
func (s *Storage) ReadFlowSpec(genID, flowID string) ([]byte, error) {
	flowPath := filepath.Join(s.baseDir, "generations", genID, "flows", flowID+".json")
	data, err := os.ReadFile(flowPath)
	if err != nil {
		return nil, fmt.Errorf("read flowspec %s: %w", flowPath, err)
	}
	return data, nil
}

// ReadActiveFlowSpec loads a specific FlowSpec JSON from the currently active generation.
func (s *Storage) ReadActiveFlowSpec(flowID string) ([]byte, error) {
	ptr, err := s.ReadPointer()
	if err != nil {
		return nil, err
	}
	if ptr == nil {
		return nil, fmt.Errorf("no active generation published")
	}
	return s.ReadFlowSpec(ptr.GenerationID, flowID)
}

// SliceCacheKey computes the deterministic cache key for a slice operation.
// It hashes fileByteHash+"||"+candidateID+"||"+versionInfo+"||"+optsHash where
// optsHash is sha256 of opts JSON if present (pass empty string if nil).
// The hash input is sorted/deterministic; callers should compute optsHash
// as hex(sha256(json.Marshal(opts))) or "" when opts is nil.
func SliceCacheKey(fileByteHash, candidateID, versionInfo string, optsHash ...string) string {
	extra := ""
	if len(optsHash) > 0 {
		extra = optsHash[0]
	}
	h := sha256.Sum256([]byte(fileByteHash + "||" + candidateID + "||" + versionInfo + "||" + extra))
	return hex.EncodeToString(h[:])
}

// ReadSliceCache retrieves cached SlicedPayload bytes if present.
func (s *Storage) ReadSliceCache(cacheKey string) ([]byte, bool) {
	path := filepath.Join(s.baseDir, "facts", "slice", cacheKey+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	return data, true
}

// WriteSliceCache stores SlicedPayload bytes into the fact cache.
func (s *Storage) WriteSliceCache(cacheKey string, data []byte) error {
	path := filepath.Join(s.baseDir, "facts", "slice", cacheKey+".json")
	return atomicWrite(path, data)
}

// AtomicWrite atomically writes data to targetPath via a temporary file.
func atomicWrite(targetPath string, data []byte) error {
	dir := filepath.Dir(targetPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create dir %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".tmp-atomic-*")
	if err != nil {
		return fmt.Errorf("create temp in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, targetPath); err != nil {
		return fmt.Errorf("rename %s to %s: %w", tmpName, targetPath, err)
	}
	return nil
}

// StagingSession coordinates a new generation build and atomic commit.
type StagingSession struct {
	storage          *Storage
	generationID     string
	stagingDir       string
	flowsDir         string
	summaries        []FlowSummary
	basisFingerprint string
}

// BeginGeneration creates a new staging session in .codeflow/staging-<genId>.
func (s *Storage) BeginGeneration(basisFingerprint string) (*StagingSession, error) {
	if err := s.InitLayout(); err != nil {
		return nil, err
	}
	genID := fmt.Sprintf("gen-%d", time.Now().UTC().UnixNano())
	stagingDir := filepath.Join(s.baseDir, "staging-"+genID)
	flowsDir := filepath.Join(stagingDir, "flows")

	if err := os.MkdirAll(flowsDir, 0o755); err != nil {
		return nil, fmt.Errorf("create staging flows dir: %w", err)
	}

	return &StagingSession{
		storage:          s,
		generationID:     genID,
		stagingDir:       stagingDir,
		flowsDir:         flowsDir,
		basisFingerprint: basisFingerprint,
	}, nil
}

// AddFlowSpec writes one sanitized FlowSpec into the staging session.
func (sess *StagingSession) AddFlowSpec(flowID string, flowSpecJSON []byte, summary FlowSummary) error {
	// Redact secrets single gate
	cleanJSON, _, err := secret.RedactJSON(flowSpecJSON)
	if err != nil {
		return fmt.Errorf("redact flowspec %s: %w", flowID, err)
	}

	targetPath := filepath.Join(sess.flowsDir, flowID+".json")
	if err := os.WriteFile(targetPath, cleanJSON, 0o644); err != nil {
		return fmt.Errorf("write staging flow %s: %w", flowID, err)
	}
	sess.summaries = append(sess.summaries, summary)
	return nil
}

// Discard cleans up the staging directory without publishing.
func (sess *StagingSession) Discard() {
	_ = os.RemoveAll(sess.stagingDir)
}

// Commit publishes the generation atomically: writes index.json, renames staging dir
// to generations/<genId>, and updates pointer.json atomically.
func (sess *StagingSession) Commit() error {
	defer sess.Discard()

	// 1. Write index.json inside staging
	index := GenerationIndex{
		GenerationID:     sess.generationID,
		PublishedAt:      time.Now().UTC(),
		BasisFingerprint: sess.basisFingerprint,
		Flows:            sess.summaries,
	}
	indexBytes, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal generation index: %w", err)
	}
	indexPath := filepath.Join(sess.stagingDir, "index.json")
	if err := os.WriteFile(indexPath, indexBytes, 0o644); err != nil {
		return fmt.Errorf("write staging index: %w", err)
	}

	// 2. Rename staging dir -> generations/<genId>
	finalGenDir := filepath.Join(sess.storage.baseDir, "generations", sess.generationID)
	if err := os.Rename(sess.stagingDir, finalGenDir); err != nil {
		return fmt.Errorf("rename staging to %s: %w", finalGenDir, err)
	}

	// 3. Atomically write pointer.json
	ptr := Pointer{
		GenerationID:     sess.generationID,
		PublishedAt:      index.PublishedAt,
		BasisFingerprint: sess.basisFingerprint,
		FlowCount:        len(sess.summaries),
	}
	ptrBytes, err := json.MarshalIndent(ptr, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal pointer: %w", err)
	}

	ptrPath := filepath.Join(sess.storage.baseDir, "pointer.json")
	if err := atomicWrite(ptrPath, ptrBytes); err != nil {
		return fmt.Errorf("write pointer: %w", err)
	}

	return nil
}
