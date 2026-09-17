// Package slicing orchestrates AST structural slicing through language adapters
// (design §4.2, ticket 07/08/09, schemas/sliced-payload.schema.json).
package slicing

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"codeflow/internal/analyzer/protocol"
	"codeflow/internal/collector/contractharness"
	"codeflow/internal/collector/evidence"
	"codeflow/internal/collector/secret"
	"codeflow/internal/collector/storage"
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

// StatementNodeMetadata represents verified statement AST node metadata.
type StatementNodeMetadata struct {
	NodeKind  string `json:"nodeKind"` // must be "statement"
	ByteRange [2]int `json:"byteRange"`
	LineRange [2]int `json:"lineRange"`
}

// StructuralContextMetadata represents enclosing condition, callback, or builder metadata.
type StructuralContextMetadata struct {
	Status    string  `json:"status"`             // "present" | "none"
	NodeKind  string  `json:"nodeKind,omitempty"` // "condition" | "callback" | "builder"
	ByteRange *[2]int `json:"byteRange,omitempty"`
	LineRange *[2]int `json:"lineRange,omitempty"`
}

// CallableMetadata represents enclosing callable signature and span metadata.
type CallableMetadata struct {
	Signature          string `json:"signature"`
	SignatureByteRange [2]int `json:"signatureByteRange"`
	SignatureLineRange [2]int `json:"signatureLineRange"`
	ByteRange          [2]int `json:"byteRange"`
	LineRange          [2]int `json:"lineRange"`
}

// FlowContextMetadata is the additive Flow Context capability metadata emitted by adapters.
type FlowContextMetadata struct {
	Statement         StatementNodeMetadata     `json:"statement"`
	StructuralContext StructuralContextMetadata `json:"structuralContext"`
	Callable          CallableMetadata          `json:"callable"`
	CanonicalPath     string                    `json:"canonicalPath"`
	SnapshotID        string                    `json:"snapshotId"`
	SourceHash        string                    `json:"sourceHash"`
}

// SliceStep represents a single guard, mutation, call, or branch step extracted from AST.
type SliceStep struct {
	Ordinal        int                  `json:"ordinal"`
	Kind           string               `json:"kind"`
	Description    string               `json:"description"`
	SymbolPath     string               `json:"symbolPath"`
	Anchor         Anchor               `json:"anchor"`
	GuardCondition *string              `json:"guardCondition,omitempty"`
	StateBefore    *string              `json:"stateBefore,omitempty"`
	StateAfter     *string              `json:"stateAfter,omitempty"`
	EffectTarget   *string              `json:"effectTarget,omitempty"`
	Layer          string               `json:"layer,omitempty"`
	FlowContext    *FlowContextMetadata `json:"flowContext,omitempty"`
}

// SliceEdge represents a call link between symbols/files or boundaries.
type SliceEdge struct {
	Kind             string `json:"kind"`
	ToSymbolPath     string `json:"toSymbolPath"`
	ResolutionStatus string `json:"resolutionStatus"`
	Depth            int    `json:"depth"`
	// StepOrdinal is OPTIONAL: 1-based ordinal of the step that produced this
	// edge. Absent in older adapter payloads — consumers must not guess.
	StepOrdinal *int   `json:"stepOrdinal,omitempty"`
	ToLayer     string `json:"toLayer,omitempty"`
}

// SlicedPayload is the language-neutral contract output returned by adapters.
type SlicedPayload struct {
	CandidateID              string         `json:"candidateId"`
	Language                 string         `json:"language"`
	EntrySymbolPath          string         `json:"entrySymbolPath"`
	Steps                    []SliceStep    `json:"steps"`
	Edges                    []SliceEdge    `json:"edges"`
	Truncated                bool           `json:"truncated"`
	VisitedCycleDetected     bool           `json:"visitedCycleDetected"`
	RedactedCount            int            `json:"redactedCount"`
	SchemaID                 string         `json:"schemaId,omitempty"`
	SchemaVersion            int            `json:"schemaVersion,omitempty"`
	Operation                string         `json:"operation,omitempty"`
	ComputedBasisID          string         `json:"computedBasisId,omitempty"`
	WorkspaceEpoch           int64          `json:"workspaceEpoch,omitempty"`
	SnapshotID               string         `json:"snapshotId,omitempty"`
	RootTreeID               string         `json:"rootTreeId,omitempty"`
	DependencyFingerprint    string         `json:"dependencyFingerprint,omitempty"`
	AnalysisReadSet          map[string]any `json:"analysisReadSet,omitempty"`
	CausalObservationClosure map[string]any `json:"causalObservationClosure,omitempty"`
	CapabilityProfile        map[string]any `json:"capabilityProfile,omitempty"`
	AnalyzerVersion          string         `json:"analyzerVersion,omitempty"`
	Diagnostics              []any          `json:"diagnostics,omitempty"`
	// ValidatedResult is populated only by the protocol v2 analysis gate. It is
	// deliberately excluded from the operation payload and cache JSON. The
	// compiler re-validates this envelope against the immutable snapshot before
	// promoting any semantic evidence.
	ValidatedResult *evidence.Result `json:"-"`
	AdapterVersion  string           `json:"-"`
}

// BindValidatedResult retains the single v2 result envelope that was accepted
// by the protocol semantic gate. Callers must not construct this marker for an
// unvalidated payload. The compiler performs a second snapshot-bound check.
func (p *SlicedPayload) BindValidatedResult(result evidence.Result) error {
	if p == nil {
		return fmt.Errorf("sliced payload is nil")
	}
	if result.Operation != protocol.OpSlice || result.SchemaID != evidence.AnalyzerResultSchemaID || result.SchemaVersion != evidence.SchemaVersion {
		return fmt.Errorf("validated result is not a v2 slice envelope")
	}
	var operation SlicedPayload
	if err := json.Unmarshal(result.Payload, &operation); err != nil {
		return fmt.Errorf("validated slice payload is invalid: %w", err)
	}
	if operation.CandidateID != p.CandidateID || operation.EntrySymbolPath != p.EntrySymbolPath || len(operation.Steps) != len(p.Steps) || len(operation.Edges) != len(p.Edges) {
		return fmt.Errorf("validated slice payload does not match operation result")
	}
	p.AnalysisReadSet = mapFromJSON(result.ReadSet)
	p.CausalObservationClosure = mapFromJSON(result.Closure)
	p.CapabilityProfile = mapFromJSON(result.Capability)
	p.AnalyzerVersion = result.AnalyzerRevision
	p.AdapterVersion = result.AdapterVersion
	p.ComputedBasisID = result.ComputedBasisID
	p.WorkspaceEpoch = result.WorkspaceEpoch
	p.SnapshotID = result.SnapshotID
	p.RootTreeID = result.SnapshotTreeDigest
	p.DependencyFingerprint = result.DependencyFingerprint
	copyResult := result
	copyResult.Payload = append([]byte(nil), result.Payload...)
	p.ValidatedResult = &copyResult
	return nil
}

func mapFromJSON(value any) map[string]any {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil
	}
	return out
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
	snapshot, err := protocol.CaptureSnapshot(repoRoot, 0)
	if err != nil {
		return nil, fmt.Errorf("capture analysis snapshot: %w", err)
	}
	return r.SliceWithSnapshot(ctx, repoRoot, candidateID, entrySymbolPath, opts, snapshot)
}

// SliceWithSnapshot executes a structural slice against an explicit immutable
// basis and optional content overlay.
func (r *Runner) SliceWithSnapshot(ctx context.Context, repoRoot, candidateID, entrySymbolPath string, opts map[string]any, snapshot protocol.Snapshot) (*SlicedPayload, error) {
	// Best-effort cache lookup before calling adapter.
	if repoRoot != "" {
		cacheKey := computeSliceCacheKeyForSnapshot(snapshot, candidateID, entrySymbolPath, opts)
		if data, ok := storage.New(repoRoot).ReadSliceCache(cacheKey); ok {
			if payload, ok := cachedSlicePayload(snapshot, candidateID, entrySymbolPath, data); ok {
				return payload, nil
			}
		}
	}

	proc, err := r.pool.Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("slicing pool get: %w", err)
	}
	defer r.pool.Put(proc)

	params := snapshot.Params()
	params["candidateId"] = candidateID
	params["entrySymbolPath"] = entrySymbolPath
	params["requiredObservations"] = []string{"negative_lookup", "membership", "dependency_frontier"}
	if opts != nil {
		params["opts"] = opts
	}

	var envelope evidence.Result
	if err := proc.Call(ctx, "slice", params, &envelope); err != nil {
		return nil, fmt.Errorf("slice call failed for %s: %w", entrySymbolPath, err)
	}

	// The protocol call has already schema- and semantically-validated the v2
	// envelope. Redact once more before operation-payload persistence and bind
	// the accepted metadata to the unwrapped payload returned to Core.
	sanitizedBytes, _, err := secret.RedactJSON(envelope.Payload)
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
	if err := payload.BindValidatedResult(envelope); err != nil {
		return nil, fmt.Errorf("bind validated slice result: %w", err)
	}

	// Best-effort cache write after successful validation.
	if repoRoot != "" {
		cacheKey := computeSliceCacheKeyForSnapshot(snapshot, candidateID, entrySymbolPath, opts)
		if encoded, encodeErr := json.Marshal(envelope); encodeErr == nil {
			if cached, _, redactErr := secret.RedactJSON(encoded); redactErr == nil {
				_ = storage.New(repoRoot).WriteSliceCache(cacheKey, cached)
			}
		}
	}

	return &payload, nil
}

// cachedSlicePayload accepts only a complete canonical v2 result envelope.
// The request id is intentionally taken from the cache entry because it is a
// per-call transport identity. All snapshot, read-set, closure, capability,
// coverage, and payload identities are still checked against this snapshot.
func cachedSlicePayload(snapshot protocol.Snapshot, candidateID, entrySymbolPath string, data []byte) (*SlicedPayload, bool) {
	sanitized, _, err := secret.RedactJSON(data)
	if err != nil {
		return nil, false
	}
	if err := contractharness.Validate(evidence.AnalyzerResultSchemaID, sanitized); err != nil {
		return nil, false
	}
	var envelope evidence.Result
	if err := json.Unmarshal(sanitized, &envelope); err != nil || envelope.Operation != protocol.OpSlice || envelope.RequestID == "" {
		return nil, false
	}
	input, err := snapshot.AnalyzerInput()
	if err != nil {
		return nil, false
	}
	request, err := evidence.NewAnalyzerRequest(envelope.RequestID, protocol.OpSlice, input, nil, envelope.Closure.RequiredObservations)
	if err != nil || evidence.ValidateResult(request, envelope) != nil {
		return nil, false
	}
	if err := contractharness.Validate(contractharness.BaseURL+"sliced-payload.schema.json", envelope.Payload); err != nil {
		return nil, false
	}
	var payload SlicedPayload
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil || payload.CandidateID != candidateID || payload.EntrySymbolPath != entrySymbolPath {
		return nil, false
	}
	if err := payload.BindValidatedResult(envelope); err != nil {
		return nil, false
	}
	return &payload, true
}

// computeSliceCacheKey is retained for callers that only have a basis. It no
// longer reads the live worktree. Analysis callers use
// computeSliceCacheKeyForSnapshot so the entry hash comes from captured bytes.
func computeSliceCacheKey(repoRoot, candidateID, entrySymbolPath string, opts map[string]any, basis ...string) string {
	fileByteHash := ""
	versionInfo := "v4-ast-context"
	if len(basis) > 0 && basis[0] != "" {
		versionInfo += "|" + basis[0]
	}
	optsHash := ""
	if opts != nil {
		if b, err := json.Marshal(opts); err == nil {
			h := sha256.Sum256(b)
			optsHash = hex.EncodeToString(h[:])
		}
	}
	return storage.SliceCacheKey(fileByteHash, candidateID, versionInfo, optsHash)
}

func computeSliceCacheKeyForSnapshot(snapshot protocol.Snapshot, candidateID, entrySymbolPath string, opts map[string]any) string {
	fileByteHash := ""
	if idx := strings.Index(entrySymbolPath, "#"); idx >= 0 {
		relPath := filepath.ToSlash(filepath.Clean(entrySymbolPath[:idx]))
		files := snapshot.Files
		if len(files) == 0 {
			files = snapshot.ContentOverlay
		}
		if content, ok := files[relPath]; ok {
			h := sha256.Sum256([]byte(content))
			fileByteHash = hex.EncodeToString(h[:])
		}
	}
	versionInfo := "v4-ast-context"
	if snapshot.ComputedBasisID != "" {
		versionInfo += "|" + snapshot.ComputedBasisID
	}
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
