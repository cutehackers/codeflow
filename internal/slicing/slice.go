// Package slicing orchestrates AST structural slicing through language adapters
// (design §4.2, ticket 07/08/09, schemas/sliced-payload.schema.json).
package slicing

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"codeflow/internal/contractharness"
	"codeflow/internal/protocol"
	"codeflow/internal/secret"
	"codeflow/internal/storage"
)

// Anchor represents an exact byte-range anchor within a source file.
type Anchor struct {
	RepoRelativePath        string `json:"repoRelativePath"`
	ByteRange               [2]int `json:"byteRange"`
	FileHash                string `json:"fileHash"`
	SpanHash                string `json:"spanHash"`
	EnclosingSymbolPath     string `json:"enclosingSymbolPath"`
	CanonicalAstFingerprint string `json:"canonicalAstFingerprint"`
	// SymbolRange is an OPTIONAL presentation hint: [startByte, endByte) of the
	// enclosing symbol (signature line through closing brace). Never used for
	// identity; freshness/relink ignore it.
	SymbolRange *[2]int `json:"symbolRange,omitempty"`
}

// SliceStep represents a single guard, mutation, call, or branch step extracted from AST.
type SliceStep struct {
	Ordinal        int     `json:"ordinal"`
	Kind           string  `json:"kind"`
	Description    string  `json:"description"`
	SymbolPath     string  `json:"symbolPath"`
	Anchor         Anchor  `json:"anchor"`
	GuardCondition *string `json:"guardCondition,omitempty"`
	StateBefore    *string `json:"stateBefore,omitempty"`
	StateAfter     *string `json:"stateAfter,omitempty"`
	EffectTarget   *string `json:"effectTarget,omitempty"`
	Layer          string  `json:"layer,omitempty"`
}

// SliceEdge represents a call link between symbols/files or boundaries.
type SliceEdge struct {
	Kind             string `json:"kind"`
	ToSymbolPath     string `json:"toSymbolPath"`
	ResolutionStatus string `json:"resolutionStatus"`
	Depth            int    `json:"depth"`
	// StepOrdinal is OPTIONAL: 1-based ordinal of the step that produced this
	// edge. Absent in older adapter payloads — consumers must not guess.
	StepOrdinal *int `json:"stepOrdinal,omitempty"`
	ToLayer     string `json:"toLayer,omitempty"`
}

// SlicedPayload is the language-neutral contract output returned by adapters.
type SlicedPayload struct {
	CandidateID          string      `json:"candidateId"`
	Language             string      `json:"language"`
	EntrySymbolPath      string      `json:"entrySymbolPath"`
	Steps                []SliceStep `json:"steps"`
	Edges                []SliceEdge `json:"edges"`
	Truncated            bool        `json:"truncated"`
	VisitedCycleDetected bool        `json:"visitedCycleDetected"`
	RedactedCount        int         `json:"redactedCount"`
}

// Runner orchestrates slicing requests across adapter processes.
type Runner struct {
	pool *protocol.Pool
}

// NewRunner creates a Slicing Runner wrapping an adapter process pool.
func NewRunner(pool *protocol.Pool) *Runner {
	return &Runner{pool: pool}
}

// Slice executes a structural slice on the specified candidate entry point.
// It implements a best-effort slice cache: computes a deterministic cache key
// from fileByteHash||candidateId||versionInfo||optsHash (storage.SliceCacheKey),
// checks storage.ReadSliceCache on hit returns cached payload, on miss after
// validation writes the result via WriteSliceCache. Cache I/O is best-effort
// and never fails the slice.
func (r *Runner) Slice(ctx context.Context, repoRoot, candidateID, entrySymbolPath string, opts map[string]any) (*SlicedPayload, error) {
	// Best-effort cache lookup before calling adapter.
	if repoRoot != "" {
		cacheKey := computeSliceCacheKey(repoRoot, candidateID, entrySymbolPath, opts)
		if data, ok := storage.New(repoRoot).ReadSliceCache(cacheKey); ok {
			// Validate cached bytes still pass redaction + schema before returning.
			sanitizedBytes, _, err := secret.RedactJSON(data)
			if err == nil {
				if err := contractharness.Validate(contractharness.BaseURL+"sliced-payload.schema.json", sanitizedBytes); err == nil {
					var payload SlicedPayload
					if err := json.Unmarshal(sanitizedBytes, &payload); err == nil {
						return &payload, nil
					}
				}
			}
		}
	}

	proc, err := r.pool.Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("slicing pool get: %w", err)
	}
	defer r.pool.Put(proc)

	params := map[string]any{
		"repoRoot":        repoRoot,
		"candidateId":     candidateID,
		"entrySymbolPath": entrySymbolPath,
	}
	if opts != nil {
		params["opts"] = opts
	}

	var rawResult json.RawMessage
	if err := proc.Call(ctx, "slice", params, &rawResult); err != nil {
		return nil, fmt.Errorf("slice call failed for %s: %w", entrySymbolPath, err)
	}

	// Secret scanner gate
	sanitizedBytes, _, err := secret.RedactJSON(rawResult)
	if err != nil {
		return nil, fmt.Errorf("secret redaction: %w", err)
	}

	// Contract harness validation
	if err := contractharness.Validate(contractharness.BaseURL+"sliced-payload.schema.json", sanitizedBytes); err != nil {
		return nil, fmt.Errorf("sliced-payload schema validation failed: %w", err)
	}

	var payload SlicedPayload
	if err := json.Unmarshal(sanitizedBytes, &payload); err != nil {
		return nil, fmt.Errorf("unmarshal sliced payload: %w", err)
	}

	// Best-effort cache write after successful validation.
	if repoRoot != "" {
		cacheKey := computeSliceCacheKey(repoRoot, candidateID, entrySymbolPath, opts)
		_ = storage.New(repoRoot).WriteSliceCache(cacheKey, sanitizedBytes)
	}

	return &payload, nil
}

// computeSliceCacheKey builds the deterministic cache key for a slice request.
// fileByteHash is sha256 of the entry file bytes (or empty if unreadable),
// candidateID and entrySymbolPath identify the candidate, versionInfo is a
// static version string for cache invalidation, optsHash is sha256 of opts JSON
// (empty if opts is nil). The final key is storage.SliceCacheKey(...).
func computeSliceCacheKey(repoRoot, candidateID, entrySymbolPath string, opts map[string]any) string {
	fileByteHash := ""
	if idx := strings.Index(entrySymbolPath, "#"); idx >= 0 {
		relPath := entrySymbolPath[:idx]
		fullPath := filepath.Join(repoRoot, relPath)
		if data, err := os.ReadFile(fullPath); err == nil {
			h := sha256.Sum256(data)
			fileByteHash = hex.EncodeToString(h[:])
		}
	}
	versionInfo := "v3"
	optsHash := ""
	if opts != nil {
		if b, err := json.Marshal(opts); err == nil {
			h := sha256.Sum256(b)
			optsHash = hex.EncodeToString(h[:])
		}
	}
	return storage.SliceCacheKey(fileByteHash, candidateID, versionInfo, optsHash)
}

// ComputeBasisSha computes the document-level basisSha over the read-set file hashes.
// Keys are sorted before hashing to ensure deterministic output (like
// storage.ComputeWorktreeFingerprint).
func ComputeBasisSha(fileHashes map[string]string) string {
	keys := make([]string, 0, len(fileHashes))
	for k := range fileHashes {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	h := sha256.New()
	for _, path := range keys {
		h.Write([]byte(path + ":" + fileHashes[path] + "\n"))
	}
	return hex.EncodeToString(h.Sum(nil))
}
