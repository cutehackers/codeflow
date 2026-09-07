package semantic

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"codeflow/internal/contractharness"
)

var (
	// ErrProposalNotFound is returned when the exact workspace/proposal/pack
	// identity has no durable record.
	ErrProposalNotFound = errors.New("proposal not found")
	// ErrProposalInvalid is returned when a stored or incoming proposal pair
	// fails validation or does not bind to the requested identity.
	ErrProposalInvalid = errors.New("proposal is invalid")

	errProposalStorePathUnsafe  = errors.New("proposal store path is unsafe")
	errProposalRecordPathUnsafe = errors.New("proposal store record path is unsafe")
)

// ProposalNotFoundError preserves the lookup identity while allowing callers
// to use errors.Is(err, ErrProposalNotFound).
type ProposalNotFoundError struct {
	WorkspaceID    string
	ProposalID     string
	EvidencePackID string
}

func (e *ProposalNotFoundError) Error() string {
	return fmt.Sprintf("proposal not found for workspace %q proposal %q evidence pack %q", e.WorkspaceID, e.ProposalID, e.EvidencePackID)
}

func (e *ProposalNotFoundError) Unwrap() error { return ErrProposalNotFound }

// ProposalInvalidError identifies an invalid incoming or durable proposal
// pair. It is intentionally separate from not-found so approval callers cannot
// turn a corrupt record into a newly synthesized approval target.
type ProposalInvalidError struct {
	WorkspaceID    string
	ProposalID     string
	EvidencePackID string
	Reason         string
	err            error
}

func (e *ProposalInvalidError) Error() string {
	if e.Reason == "" {
		return fmt.Sprintf("proposal invalid for workspace %q proposal %q evidence pack %q", e.WorkspaceID, e.ProposalID, e.EvidencePackID)
	}
	return fmt.Sprintf("proposal invalid for workspace %q proposal %q evidence pack %q: %s", e.WorkspaceID, e.ProposalID, e.EvidencePackID, e.Reason)
}

func (e *ProposalInvalidError) Unwrap() error {
	if e.err != nil {
		return errors.Join(ErrProposalInvalid, e.err)
	}
	return ErrProposalInvalid
}

// StoredProposal is the only value returned by the approval lookup seam. Both
// objects are owned snapshots and are never aliases of a caller or of another
// lookup result.
type StoredProposal struct {
	WorkspaceID string
	Proposal    *ModelProposal
	Pack        *EvidencePack
}

// ProposalStore is the replaceable approval read/write boundary. Lookup is
// identity-only: callers provide workspace, proposal and evidence-pack IDs,
// never caller-constructed proposal or pack objects.
type ProposalStore interface {
	SaveEnrichmentResult(context.Context, string, *EnrichmentResult) error
	Load(context.Context, string, string, string) (*StoredProposal, error)
}

// PersistAvailableEnrichment is the Core-owned persistence boundary for an
// available VS08 result. It accepts the result produced by the supervised
// enrichment path and never accepts caller-constructed proposal or pack
// objects.
func PersistAvailableEnrichment(ctx context.Context, store ProposalStore, workspaceID string, result *EnrichmentResult) error {
	if err := proposalContextErr(ctx); err != nil {
		return err
	}
	if strings.TrimSpace(workspaceID) == "" {
		return newProposalInvalidError(workspaceID, proposalIDFromResult(result), packIDFromResult(result), "workspace identity is required", nil)
	}
	if err := validateAvailableEnrichmentResult(result); err != nil {
		return newProposalInvalidError(workspaceID, proposalIDFromResult(result), packIDFromResult(result), err.Error(), err)
	}
	if store == nil {
		return newProposalInvalidError(workspaceID, result.Proposal.ProposalID, result.Pack.EvidencePackID, "proposal store is required", nil)
	}
	return store.SaveEnrichmentResult(ctx, workspaceID, result)
}

// RunSemanticEnrichmentAndPersist executes the normal supervised enrichment
// path and persists only an available result. Non-available results retain
// their deterministic fallback and do not create a proposal record.
func RunSemanticEnrichmentAndPersist(ctx context.Context, req EnrichmentRequest, store ProposalStore, workspaceID string) (EnrichmentResult, error) {
	result := RunSemanticEnrichment(ctx, req)
	if result.State.Status != "available" {
		return result, nil
	}
	if err := PersistAvailableEnrichment(ctx, store, workspaceID, &result); err != nil {
		return result, err
	}
	return result, nil
}

// LoadProposalForApproval is the shared ID-only lookup helper for future
// approval handlers. It deliberately has no proposal or pack object argument.
func LoadProposalForApproval(ctx context.Context, store ProposalStore, workspaceID, proposalID, evidencePackID string) (*StoredProposal, error) {
	if err := proposalContextErr(ctx); err != nil {
		return nil, err
	}
	if strings.TrimSpace(workspaceID) == "" || strings.TrimSpace(proposalID) == "" || strings.TrimSpace(evidencePackID) == "" {
		return nil, newProposalInvalidError(workspaceID, proposalID, evidencePackID, "workspace, proposal and evidence-pack identities are required", nil)
	}
	if store == nil {
		return nil, newProposalInvalidError(workspaceID, proposalID, evidencePackID, "proposal store is required", nil)
	}
	stored, err := store.Load(ctx, workspaceID, proposalID, evidencePackID)
	if err != nil {
		return nil, err
	}
	validated, err := validateStoredProposalLookup(stored, workspaceID, proposalID, evidencePackID)
	if err != nil {
		return nil, newProposalInvalidError(workspaceID, proposalID, evidencePackID, err.Error(), err)
	}
	return validated, nil
}

// DurableProposalStore stores verified proposal/pack pairs under a repository
// root. The path is content-addressed by all lookup identities, so arbitrary
// IDs cannot escape the disposable storage directory.
type DurableProposalStore struct {
	root    string
	initErr error
	mu      sync.RWMutex
}

// NewDurableProposalStore creates a file-backed proposal store rooted at
// <repoRoot>/.codeflow/semantic-proposals.
func NewDurableProposalStore(repoRoot string) *DurableProposalStore {
	// A repository root may itself be a legitimate symlink. Resolve that one
	// boundary first, then treat the managed .codeflow descendants as trusted
	// paths that must never contain a symlink.
	canonicalRoot := repoRoot
	if resolved, err := filepath.EvalSymlinks(repoRoot); err == nil {
		canonicalRoot = resolved
	}
	return newProposalStore(filepath.Join(canonicalRoot, ".codeflow", "semantic-proposals"))
}

// NewProposalStore is the concise constructor used by production callers.
func NewProposalStore(repoRoot string) *DurableProposalStore {
	return NewDurableProposalStore(repoRoot)
}

// NewFileProposalStore creates a file-backed store at an explicit directory.
// It is useful for a replaceable local test seam and does not change the
// public JSON or approval contract.
func NewFileProposalStore(directory string) *DurableProposalStore {
	return newProposalStore(directory)
}

func newProposalStore(directory string) *DurableProposalStore {
	root := filepath.Clean(directory)
	var initErr error
	if info, err := os.Lstat(root); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			initErr = fmt.Errorf("%w: symlink component %q", errProposalStorePathUnsafe, root)
		} else if !info.IsDir() {
			initErr = fmt.Errorf("%w: non-directory component %q", errProposalStorePathUnsafe, root)
		} else if resolved, resolveErr := filepath.EvalSymlinks(root); resolveErr == nil {
			// A normal explicit directory may live below an OS-managed alias
			// such as macOS /var. Store the canonical directory, while the
			// store directory itself remains required to be non-symlinked.
			root = resolved
		}
	} else if errors.Is(err, os.ErrNotExist) {
		parent := filepath.Dir(root)
		if info, parentErr := os.Lstat(parent); parentErr == nil && info.Mode()&os.ModeSymlink != 0 {
			initErr = fmt.Errorf("%w: symlink component %q", errProposalStorePathUnsafe, parent)
		} else if parentErr != nil && !errors.Is(parentErr, os.ErrNotExist) {
			initErr = parentErr
		} else if resolvedParent, resolveErr := filepath.EvalSymlinks(parent); resolveErr == nil {
			root = filepath.Join(resolvedParent, filepath.Base(root))
		}
	} else {
		initErr = err
	}
	if initErr == nil {
		initErr = validateProposalStorePath(root)
	}
	return &DurableProposalStore{root: root, initErr: initErr}
}

type durableProposalRecord struct {
	StoreVersion int            `json:"storeVersion"`
	WorkspaceID  string         `json:"workspaceId"`
	Proposal     *ModelProposal `json:"proposal"`
	Pack         *EvidencePack  `json:"pack"`
}

func (s *DurableProposalStore) SaveEnrichmentResult(ctx context.Context, workspaceID string, result *EnrichmentResult) error {
	if err := proposalContextErr(ctx); err != nil {
		return err
	}
	if s == nil {
		return newProposalInvalidError(workspaceID, proposalIDFromResult(result), packIDFromResult(result), "proposal store is required", nil)
	}
	if strings.TrimSpace(workspaceID) == "" {
		return newProposalInvalidError(workspaceID, "", "", "workspace identity is required", nil)
	}
	// Preserve the existing authority chain before any bytes are persisted. The
	// result's private Core attestation and View.Map are required for this call.
	if err := validateAvailableEnrichmentResult(result); err != nil {
		return newProposalInvalidError(workspaceID, proposalIDFromResult(result), packIDFromResult(result), err.Error(), err)
	}

	record := durableProposalRecord{StoreVersion: 1, WorkspaceID: workspaceID, Proposal: cloneModelProposal(result.Proposal), Pack: cloneEvidencePack(result.Pack)}
	data, err := json.Marshal(record)
	if err != nil {
		return newProposalInvalidError(workspaceID, result.Proposal.ProposalID, result.Pack.EvidencePackID, "marshal durable proposal record: "+err.Error(), err)
	}
	target := s.recordPath(workspaceID, result.Proposal.ProposalID, result.Pack.EvidencePackID)

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureProposalStoreRoot(true); err != nil {
		return newProposalInvalidError(workspaceID, result.Proposal.ProposalID, result.Pack.EvidencePackID, err.Error(), err)
	}
	if existing, err := readProposalRecord(target, true); err == nil {
		if bytes.Equal(existing, data) {
			return nil
		}
		return newProposalInvalidError(workspaceID, result.Proposal.ProposalID, result.Pack.EvidencePackID, "durable proposal identity is immutable", nil)
	} else if !errors.Is(err, os.ErrNotExist) {
		return newProposalInvalidError(workspaceID, result.Proposal.ProposalID, result.Pack.EvidencePackID, err.Error(), err)
	}
	if err := atomicProposalWrite(target, data); err != nil {
		if errors.Is(err, os.ErrExist) {
			existing, readErr := readProposalRecord(target, true)
			if readErr == nil && bytes.Equal(existing, data) {
				return nil
			}
			if readErr != nil {
				return newProposalInvalidError(workspaceID, result.Proposal.ProposalID, result.Pack.EvidencePackID, readErr.Error(), readErr)
			}
			return newProposalInvalidError(workspaceID, result.Proposal.ProposalID, result.Pack.EvidencePackID, "durable proposal identity is immutable", nil)
		}
		return fmt.Errorf("persist proposal record: %w", err)
	}
	return nil
}

func validateAvailableEnrichmentResult(result *EnrichmentResult) error {
	if result == nil || result.State.Status != "available" || result.Proposal == nil || result.Pack == nil {
		return errors.New("only an available enrichment result with proposal and evidence pack may be stored")
	}
	if err := ValidateEnrichmentResultContract(result); err != nil {
		return err
	}
	if result.View == nil || result.View.Map == nil {
		return errors.New("validated enrichment result has no semantic map authority")
	}
	proposalCtx := ProposalValidationContext{
		Map: result.View.Map, Pack: result.Pack, TargetStepIDs: result.Pack.TargetStepIDs, TargetSymbolPath: result.Pack.TargetSymbolPath,
		ExpectedPackDigest: result.Pack.PackDigest, ExpectedModelID: result.State.Capability.ModelID, ExpectedModelRevision: result.State.Capability.Revision,
		ExpectedPromptRevision: result.Proposal.PromptRevision, ExpectedSchemaProfile: SemanticProposalSchemaProfile,
	}
	if err := ValidateModelProposalV2(result.Proposal, proposalCtx); err != nil {
		return err
	}
	return ValidateEvidencePackV2(result.Pack)
}

// Save is a compatibility spelling for code that treats the store as a
// generic persistence boundary. It still accepts only a validated enrichment
// result and cannot persist caller-synthesized proposal/pack objects.
func (s *DurableProposalStore) Save(ctx context.Context, workspaceID string, result *EnrichmentResult) error {
	return s.SaveEnrichmentResult(ctx, workspaceID, result)
}

func (s *DurableProposalStore) Load(ctx context.Context, workspaceID, proposalID, evidencePackID string) (*StoredProposal, error) {
	if err := proposalContextErr(ctx); err != nil {
		return nil, err
	}
	if s == nil {
		return nil, newProposalInvalidError(workspaceID, proposalID, evidencePackID, "proposal store is required", nil)
	}
	if strings.TrimSpace(workspaceID) == "" || strings.TrimSpace(proposalID) == "" || strings.TrimSpace(evidencePackID) == "" {
		return nil, newProposalInvalidError(workspaceID, proposalID, evidencePackID, "workspace, proposal and evidence-pack identities are required", nil)
	}
	target := s.recordPath(workspaceID, proposalID, evidencePackID)
	if err := s.ensureProposalStoreRoot(false); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, &ProposalNotFoundError{WorkspaceID: workspaceID, ProposalID: proposalID, EvidencePackID: evidencePackID}
		}
		return nil, newProposalInvalidError(workspaceID, proposalID, evidencePackID, err.Error(), err)
	}
	s.mu.RLock()
	data, err := readProposalRecord(target, true)
	s.mu.RUnlock()
	if errors.Is(err, os.ErrNotExist) {
		return nil, &ProposalNotFoundError{WorkspaceID: workspaceID, ProposalID: proposalID, EvidencePackID: evidencePackID}
	}
	if err != nil {
		return nil, newProposalInvalidError(workspaceID, proposalID, evidencePackID, "read durable proposal record: "+err.Error(), err)
	}
	var record durableProposalRecord
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&record); err != nil {
		return nil, newProposalInvalidError(workspaceID, proposalID, evidencePackID, "decode durable proposal record: "+err.Error(), err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return nil, newProposalInvalidError(workspaceID, proposalID, evidencePackID, "decode durable proposal record: "+err.Error(), err)
	}
	if record.StoreVersion != 1 || record.WorkspaceID != workspaceID || record.Proposal == nil || record.Pack == nil {
		return nil, newProposalInvalidError(workspaceID, proposalID, evidencePackID, "durable proposal record identity or version is incomplete", nil)
	}
	if record.Proposal.ProposalID != proposalID || record.Pack.EvidencePackID != evidencePackID {
		return nil, newProposalInvalidError(workspaceID, proposalID, evidencePackID, "durable proposal record does not match lookup identity", nil)
	}
	validated, err := validateStoredProposalLookup(&StoredProposal{WorkspaceID: workspaceID, Proposal: record.Proposal, Pack: record.Pack}, workspaceID, proposalID, evidencePackID)
	if err != nil {
		return nil, newProposalInvalidError(workspaceID, proposalID, evidencePackID, err.Error(), err)
	}
	return validated, nil
}

func validateStoredProposalLookup(stored *StoredProposal, workspaceID, proposalID, evidencePackID string) (*StoredProposal, error) {
	if stored == nil || stored.Proposal == nil || stored.Pack == nil {
		return nil, errors.New("proposal store returned an incomplete proposal pair")
	}
	if stored.WorkspaceID != workspaceID || stored.Proposal.ProposalID != proposalID || stored.Pack.EvidencePackID != evidencePackID {
		return nil, errors.New("proposal store returned a pair that does not match lookup identity")
	}
	if err := validateStoredProposalPair(stored.Proposal, stored.Pack); err != nil {
		return nil, err
	}
	return &StoredProposal{WorkspaceID: stored.WorkspaceID, Proposal: cloneModelProposal(stored.Proposal), Pack: cloneEvidencePack(stored.Pack)}, nil
}

func validateStoredProposalPair(proposal *ModelProposal, pack *EvidencePack) error {
	if err := ValidateEvidencePackV2(pack); err != nil {
		return err
	}
	proposalBytes, err := json.Marshal(proposal)
	if err != nil {
		return err
	}
	if err := contractharness.Validate(SemanticProposalV2SchemaID, proposalBytes); err != nil {
		return fmt.Errorf("semantic proposal schema: %w", err)
	}
	if err := validateProposalPackBinding(proposal, pack); err != nil {
		return err
	}
	return nil
}

func (s *DurableProposalStore) recordPath(workspaceID, proposalID, evidencePackID string) string {
	key := sha256.Sum256([]byte(workspaceID + "\x00" + proposalID + "\x00" + evidencePackID))
	return filepath.Join(s.root, hex.EncodeToString(key[:])+".json")
}

func (s *DurableProposalStore) ensureProposalStoreRoot(create bool) error {
	if s == nil {
		return errProposalStorePathUnsafe
	}
	if s.initErr != nil {
		return s.initErr
	}
	if err := validateProposalStorePath(s.root); err != nil {
		return err
	}
	info, err := os.Lstat(s.root)
	if errors.Is(err, os.ErrNotExist) {
		if !create {
			return os.ErrNotExist
		}
		if err := os.MkdirAll(s.root, 0o700); err != nil {
			return err
		}
		if err := validateProposalStorePath(s.root); err != nil {
			return err
		}
		info, err = os.Lstat(s.root)
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("%w: managed store root %q must be a directory", errProposalStorePathUnsafe, s.root)
	}
	if err := os.Chmod(s.root, 0o700); err != nil {
		return err
	}
	info, err = os.Lstat(s.root)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() || info.Mode().Perm() != 0o700 {
		return fmt.Errorf("%w: managed store root %q is not owner-only", errProposalStorePathUnsafe, s.root)
	}
	return nil
}

func validateProposalStorePath(path string) error {
	path = filepath.Clean(path)
	for current := path; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("%w: symlink component %q", errProposalStorePathUnsafe, current)
			}
			if !info.IsDir() {
				return fmt.Errorf("%w: non-directory component %q", errProposalStorePathUnsafe, current)
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return nil
		}
	}
}

func readProposalRecord(path string, tightenPermissions bool) ([]byte, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, os.ErrNotExist
	}
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: record %q must be a regular non-symlink file", errProposalRecordPathUnsafe, path)
	}
	if tightenPermissions {
		if info.Mode().Perm() != 0o600 {
			if err := os.Chmod(path, 0o600); err != nil {
				return nil, err
			}
		}
	} else if info.Mode().Perm() != 0o600 {
		return nil, fmt.Errorf("%w: record %q must be owner-only", errProposalRecordPathUnsafe, path)
	}
	// Re-check after chmod so a replaced target cannot be treated as the
	// validated record. The trusted-root and non-symlink checks are repeated
	// immediately before opening the file.
	info, err = os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		return nil, fmt.Errorf("%w: record %q changed while being validated", errProposalRecordPathUnsafe, path)
	}
	return os.ReadFile(path)
}

func atomicProposalWrite(target string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(target), ".proposal-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	// Link is intentionally used instead of Rename. Rename would allow two
	// independent store instances to race and replace an immutable record after
	// both observed it as absent. A hard-link publication succeeds exactly once
	// and leaves the winner's bytes untouched for the loser to compare.
	if err := os.Link(tmpName, target); err != nil {
		return err
	}
	if err := syncProposalDirectory(filepath.Dir(target)); err != nil {
		return err
	}
	return nil
}

func proposalContextErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("trailing JSON value")
		}
		return err
	}
	return nil
}

func newProposalInvalidError(workspaceID, proposalID, evidencePackID, reason string, err error) error {
	return &ProposalInvalidError{WorkspaceID: workspaceID, ProposalID: proposalID, EvidencePackID: evidencePackID, Reason: reason, err: err}
}

func proposalIDFromResult(result *EnrichmentResult) string {
	if result == nil || result.Proposal == nil {
		return ""
	}
	return result.Proposal.ProposalID
}

func packIDFromResult(result *EnrichmentResult) string {
	if result == nil || result.Pack == nil {
		return ""
	}
	return result.Pack.EvidencePackID
}

func cloneModelProposal(proposal *ModelProposal) *ModelProposal {
	if proposal == nil {
		return nil
	}
	clone := *proposal
	clone.EvidenceRefs = append([]string(nil), proposal.EvidenceRefs...)
	return &clone
}

func cloneEvidencePack(pack *EvidencePack) *EvidencePack {
	if pack == nil {
		return nil
	}
	clone := *pack
	clone.TargetStepIDs = append([]string(nil), pack.TargetStepIDs...)
	clone.ScopePaths = append([]string(nil), pack.ScopePaths...)
	clone.Items = make([]EvidenceItem, len(pack.Items))
	copy(clone.Items, pack.Items)
	return &clone
}
