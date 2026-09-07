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
	"sort"
	"strings"
	"time"

	"codeflow/internal/contractharness"
	"codeflow/internal/fusion"
	"codeflow/internal/protocol"
	"codeflow/internal/secret"
	"codeflow/internal/storage"
	"codeflow/internal/workspace"
)

// EvidencePackV2SchemaID identifies the bounded, snapshot-grounded evidence
// pack sent to an optional semantic enrichment host.
const EvidencePackV2SchemaID = "https://codeflow.local/schemas/rflsc.evidence-pack.v2.schema.json"

const (
	ModelHostRequestV2SchemaID          = "https://codeflow.local/schemas/rflsc.model-host-request.v2.schema.json"
	ModelHostResponseV2SchemaID         = "https://codeflow.local/schemas/rflsc.model-host-response.v2.schema.json"
	SemanticProposalV2SchemaID          = "https://codeflow.local/schemas/rflsc.semantic-proposal.v2.schema.json"
	EnrichmentStateV2SchemaID           = "https://codeflow.local/schemas/rflsc.enrichment-state.v2.schema.json"
	ModelActivationDisclosureV1SchemaID = "https://codeflow.local/schemas/rflsc.model-activation-disclosure.v1.schema.json"
	SemanticProposalSchemaProfile       = "rflsc.semantic-proposal.v2"
)

// EvidencePackRequest describes the immutable evidence scope for one target.
type EvidencePackRequest struct {
	Map      *SemanticMapIR
	Snapshot protocol.Snapshot
	// CurrentProof and CurrentPointer are supplied only after the VS03
	// publication reader has validated the active pointer/manifest chain. A
	// matching map and snapshot without this proof must remain ineligible for a
	// verified enrichment pack.
	CurrentProof       *storage.GenerationProofManifest
	CurrentProofBytes  []byte
	CurrentPointer     *storage.ActivePointer
	SemanticMapBytes   []byte
	LiveHeadSnapshotID string
	SnapshotFiles      map[string]string
	SnapshotID         string
	ComputedBasisID    string
	GenerationID       string
	RepositoryID       string
	WorktreeID         string
	TargetStepIDs      []string
	TargetSymbolPath   string
	ScopePaths         []string
	PromptRevision     string
	MaxItems           int
	MaxBytes           int
}

// EvidencePackV2 is an explicit name for the v2 compatibility seam. The
// fields remain source-compatible with the legacy EvidencePack type while v2
// callers receive a distinct schema identity.
type EvidencePackV2 = EvidencePack

const (
	defaultEvidencePackMaxItems = 32
	defaultEvidencePackMaxBytes = 32 << 10
)

// BuildEvidencePackV2 builds a deterministic pack from the current immutable
// snapshot only. The map's evidence references are the allow-list. A caller
// cannot supply arbitrary content or a repository path and have it included.
func BuildEvidencePackV2(req EvidencePackRequest) (*EvidencePack, error) {
	if req.Map == nil {
		return nil, errors.New("evidence pack: semantic map is required")
	}
	if err := validateCurrentEvidenceBinding(req); err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.Map.ComputedBasisID) == "" || strings.TrimSpace(req.Map.GenerationID) == "" || strings.TrimSpace(req.Map.ValidatedAgainstSnapshotID) == "" {
		return nil, errors.New("evidence pack: map is missing immutable basis, generation, or snapshot identity")
	}

	snapshotID := firstNonEmpty(req.SnapshotID, req.Snapshot.SnapshotID)
	basisID := firstNonEmpty(req.ComputedBasisID, req.Snapshot.ComputedBasisID)
	generationID := firstNonEmpty(req.GenerationID, req.Map.GenerationID)
	if snapshotID == "" || basisID == "" {
		return nil, errors.New("evidence pack: captured snapshot and basis identities are required")
	}
	if req.SnapshotID != "" && req.Snapshot.SnapshotID != "" && req.SnapshotID != req.Snapshot.SnapshotID {
		return nil, errors.New("evidence pack: request snapshot identity disagrees with captured snapshot")
	}
	if req.ComputedBasisID != "" && req.Snapshot.ComputedBasisID != "" && req.ComputedBasisID != req.Snapshot.ComputedBasisID {
		return nil, errors.New("evidence pack: request basis identity disagrees with captured snapshot")
	}
	if snapshotID != req.Map.ValidatedAgainstSnapshotID {
		return nil, fmt.Errorf("evidence pack: snapshot %q does not match map snapshot %q", snapshotID, req.Map.ValidatedAgainstSnapshotID)
	}
	if basisID != req.Map.ComputedBasisID {
		return nil, fmt.Errorf("evidence pack: basis %q does not match map basis %q", basisID, req.Map.ComputedBasisID)
	}
	if generationID != req.Map.GenerationID {
		return nil, fmt.Errorf("evidence pack: generation %q does not match map generation %q", generationID, req.Map.GenerationID)
	}
	if strings.TrimSpace(snapshotID) == "" || strings.TrimSpace(basisID) == "" || strings.TrimSpace(generationID) == "" {
		return nil, errors.New("evidence pack: immutable identity is required")
	}

	files := req.Snapshot.Files
	if len(files) == 0 {
		files = req.Snapshot.ContentOverlay
	}
	if len(files) == 0 && len(req.SnapshotFiles) > 0 {
		files = req.SnapshotFiles
	}
	if len(files) == 0 {
		return nil, errors.New("evidence pack: immutable snapshot bytes are required")
	}

	scope, err := normalizePackPaths(req.ScopePaths)
	if err != nil {
		return nil, err
	}
	if len(scope) == 0 && req.Map.Coverage != nil {
		scope, err = normalizePackPaths(req.Map.Coverage.IncludedSourceRoots)
		if err != nil {
			return nil, err
		}
	}
	if len(scope) == 0 {
		scope = []string{"."}
	}

	stepsByID := make(map[string]SemanticStep, len(req.Map.Steps))
	for _, step := range req.Map.Steps {
		if strings.TrimSpace(step.StepID) == "" {
			return nil, errors.New("evidence pack: map contains a step without an ID")
		}
		if _, exists := stepsByID[step.StepID]; exists {
			return nil, fmt.Errorf("evidence pack: duplicate step %q", step.StepID)
		}
		stepsByID[step.StepID] = step
	}
	targetIDs := append([]string(nil), req.TargetStepIDs...)
	if len(targetIDs) == 0 && strings.TrimSpace(req.TargetSymbolPath) != "" {
		for _, step := range req.Map.Steps {
			if step.Name == req.TargetSymbolPath || step.TechnicalName == req.TargetSymbolPath || step.StructuralIdentity == req.TargetSymbolPath {
				targetIDs = append(targetIDs, step.StepID)
			}
		}
	}
	if len(targetIDs) == 0 {
		return nil, errors.New("evidence pack: at least one target step is required")
	}
	targetIDs = uniqueSortedStrings(targetIDs)

	evidenceByID := make(map[string]SemanticEvidence, len(req.Map.Evidence))
	for _, evidence := range req.Map.Evidence {
		if strings.TrimSpace(evidence.EvidenceID) == "" {
			return nil, errors.New("evidence pack: map contains evidence without an ID")
		}
		if _, exists := evidenceByID[evidence.EvidenceID]; exists {
			return nil, fmt.Errorf("evidence pack: duplicate evidence %q", evidence.EvidenceID)
		}
		evidenceByID[evidence.EvidenceID] = evidence
	}

	selectedRefs := make(map[string]struct{})
	selectedSteps := make([]SemanticStep, 0, len(targetIDs))
	for _, stepID := range targetIDs {
		step, ok := stepsByID[stepID]
		if !ok {
			return nil, fmt.Errorf("evidence pack: target step %q does not exist", stepID)
		}
		selectedSteps = append(selectedSteps, step)
		for _, ref := range step.EvidenceRefs {
			if strings.TrimSpace(ref) == "" {
				return nil, fmt.Errorf("evidence pack: target step %q has an empty evidence reference", stepID)
			}
			selectedRefs[ref] = struct{}{}
		}
	}
	if len(selectedRefs) == 0 {
		return nil, errors.New("evidence pack: target has no evidence references")
	}

	maxItems := req.MaxItems
	if maxItems <= 0 {
		maxItems = defaultEvidencePackMaxItems
	}
	maxBytes := req.MaxBytes
	if maxBytes <= 0 {
		maxBytes = defaultEvidencePackMaxBytes
	}
	if maxBytes < 1 {
		return nil, errors.New("evidence pack: byte budget must be positive")
	}

	refs := make([]string, 0, len(selectedRefs))
	for ref := range selectedRefs {
		refs = append(refs, ref)
	}
	sort.Strings(refs)
	items := make([]EvidenceItem, 0, len(refs))
	redactedCount := 0
	usedBytes := 0
	for _, ref := range refs {
		evidence, ok := evidenceByID[ref]
		if !ok {
			return nil, fmt.Errorf("evidence pack: target references missing evidence %q", ref)
		}
		if evidence.ValidationStatus != "verified" {
			return nil, fmt.Errorf("evidence pack: evidence %q is not currently verified", ref)
		}
		if evidence.SnapshotID != snapshotID || evidence.ComputedBasisID != basisID {
			return nil, fmt.Errorf("evidence pack: evidence %q is outside the current snapshot or basis", ref)
		}
		path, err := workspace.NormalizeRepositoryPath(evidence.Anchor.RepoRelativePath)
		if err != nil {
			return nil, fmt.Errorf("evidence pack: evidence %q path: %w", ref, err)
		}
		if !pathInScope(path, scope) {
			return nil, fmt.Errorf("evidence pack: evidence %q is outside requested scope", ref)
		}
		content, ok := files[path]
		if !ok {
			return nil, fmt.Errorf("evidence pack: snapshot has no bytes for %q", path)
		}
		fileHash := sha256.Sum256([]byte(content))
		if !strings.EqualFold(evidence.Anchor.FileHash, hex.EncodeToString(fileHash[:])) {
			return nil, fmt.Errorf("evidence pack: evidence %q file hash does not match captured bytes", ref)
		}
		start, end := evidence.Anchor.ByteRange[0], evidence.Anchor.ByteRange[1]
		if start < 0 || end < start || end > len([]byte(content)) {
			return nil, fmt.Errorf("evidence pack: evidence %q has invalid byte range", ref)
		}
		raw := []byte(content)[start:end]
		clean, count, err := secret.RedactJSON(raw)
		if err != nil {
			return nil, fmt.Errorf("evidence pack: redact evidence %q: %w", ref, err)
		}
		redactedCount += count
		usedBytes += len(clean)
		if len(items) >= maxItems || usedBytes > maxBytes {
			return nil, fmt.Errorf("evidence pack: bounded budget exceeded at evidence %q", ref)
		}
		itemDigest := sha256.Sum256(clean)
		items = append(items, EvidenceItem{
			EvidenceID: ref, Kind: evidence.Kind, Source: path, Content: string(clean), Verified: true,
			SnapshotID: snapshotID, ComputedBasisID: basisID, DocumentRevisionID: evidence.DocumentRevisionID,
			ContentDigest: hex.EncodeToString(itemDigest[:]), ByteRange: evidence.Anchor.ByteRange,
		})
	}
	if len(items) == 0 {
		return nil, errors.New("evidence pack: no verified evidence selected")
	}

	targetSymbol := strings.TrimSpace(req.TargetSymbolPath)
	if targetSymbol == "" && len(selectedSteps) > 0 {
		targetSymbol = firstNonEmpty(selectedSteps[0].TechnicalName, selectedSteps[0].Name, selectedSteps[0].StructuralIdentity)
	}
	if err := validateTargetSymbolPath(targetSymbol); err != nil {
		return nil, err
	}
	pack := &EvidencePack{
		SchemaID: EvidencePackV2SchemaID, SchemaVersion: 2,
		EvidencePackID: "", TargetSymbolPath: targetSymbol, ComputedBasisID: basisID,
		GenerationID: generationID, Items: items,
		RedactionStatus: func() string {
			if redactedCount > 0 {
				return "redacted"
			}
			return "clean"
		}(),
		RepositoryID: firstNonEmpty(req.RepositoryID, req.Map.Basis.RepositoryID),
		WorktreeID:   firstNonEmpty(req.WorktreeID, req.Map.Basis.WorktreeID),
		SnapshotID:   snapshotID, WorkspaceEpoch: req.Snapshot.WorkspaceEpoch,
		TargetStepIDs: targetIDs, ScopePaths: scope, PackDigest: "", PromptRevision: req.PromptRevision,
	}
	pack.EvidencePackID = deterministicPackID(pack)
	pack.PackDigest = digestPack(pack)
	if len(marshalPack(pack)) > maxBytes {
		return nil, fmt.Errorf("evidence pack: serialized pack exceeds byte budget")
	}
	return pack, nil
}

// validateCurrentEvidenceBinding reuses the VS03 identity chain at the
// enrichment boundary. Current verified Evidence requires the canonical map,
// the computed snapshot, the active proof/manifest identities, and the live
// head to agree. Caller-provided labels alone cannot establish currentness.
func validateCurrentEvidenceBinding(req EvidencePackRequest) error {
	if req.Map.SchemaID != SemanticMapSchemaID || req.Map.SchemaVersion != SemanticSchemaVersion {
		return errors.New("evidence pack: map schema identity is not canonical")
	}
	if req.Map.Freshness != "current" {
		return errors.New("evidence pack: only a current map may supply verified enrichment evidence")
	}
	proof := req.CurrentProof
	if proof == nil {
		return errors.New("evidence pack: current publication proof is required")
	}
	pointer := req.CurrentPointer
	if pointer == nil {
		return errors.New("evidence pack: active pointer is required")
	}
	if proof.SchemaID != GenerationProofSchemaID || proof.SchemaVersion != SemanticSchemaVersion || strings.TrimSpace(proof.ProofID) == "" {
		return errors.New("evidence pack: current publication proof schema or identity is invalid")
	}
	if pointer.SchemaID != ActivePointerSchemaID || pointer.SchemaVersion != SemanticSchemaVersion || pointer.ManifestObjectRef == "" || pointer.GenerationID == "" || pointer.ComputedBasisID == "" || pointer.ValidatedAgainstSnapshotID == "" || pointer.ExpectedLiveHeadSnapshotID == "" || pointer.RepositoryID == "" || pointer.WorktreeID == "" || pointer.TaskID == "" {
		return errors.New("evidence pack: active pointer schema or identity is invalid")
	}
	if len(req.SemanticMapBytes) == 0 || len(req.CurrentProofBytes) == 0 {
		return errors.New("evidence pack: exact semantic-map and proof artifact bytes are required")
	}
	if storage.ArtifactCASRef(req.SemanticMapBytes) != proof.ArtifactRefs.SemanticMap {
		return errors.New("evidence pack: semantic-map artifact bytes do not match current proof reference")
	}
	if storage.ArtifactCASRef(req.CurrentProofBytes) != pointer.ManifestObjectRef {
		return errors.New("evidence pack: proof artifact bytes do not match active pointer reference")
	}
	var artifactMap SemanticMapIR
	if err := json.Unmarshal(req.SemanticMapBytes, &artifactMap); err != nil {
		return fmt.Errorf("evidence pack: decode semantic-map artifact bytes: %w", err)
	}
	canonicalMapBytes, err := json.Marshal(&artifactMap)
	if err != nil {
		return fmt.Errorf("evidence pack: canonicalize semantic-map artifact bytes: %w", err)
	}
	requestedMapBytes, err := json.Marshal(req.Map)
	if err != nil {
		return fmt.Errorf("evidence pack: canonicalize requested semantic map: %w", err)
	}
	if !bytes.Equal(canonicalMapBytes, requestedMapBytes) || !isCanonicalJSONArtifact(req.SemanticMapBytes, &artifactMap) {
		return errors.New("evidence pack: supplied semantic map does not exactly match canonical artifact bytes")
	}
	if artifactMap.SchemaID != SemanticMapSchemaID || artifactMap.SchemaVersion != SemanticSchemaVersion || artifactMap.MapID != req.Map.MapID || artifactMap.GenerationID != req.Map.GenerationID || artifactMap.ComputedBasisID != req.Map.ComputedBasisID || artifactMap.ValidatedAgainstSnapshotID != req.Map.ValidatedAgainstSnapshotID {
		return errors.New("evidence pack: semantic-map artifact identity does not match requested map")
	}
	var artifactProof storage.GenerationProofManifest
	if err := json.Unmarshal(req.CurrentProofBytes, &artifactProof); err != nil {
		return fmt.Errorf("evidence pack: decode proof artifact bytes: %w", err)
	}
	canonicalProofBytes, err := json.Marshal(&artifactProof)
	if err != nil {
		return fmt.Errorf("evidence pack: canonicalize proof artifact bytes: %w", err)
	}
	requestedProofBytes, err := json.Marshal(proof)
	if err != nil {
		return fmt.Errorf("evidence pack: canonicalize requested proof: %w", err)
	}
	if !bytes.Equal(canonicalProofBytes, requestedProofBytes) || !isCanonicalJSONArtifact(req.CurrentProofBytes, &artifactProof) {
		return errors.New("evidence pack: supplied proof does not exactly match canonical artifact bytes")
	}
	if artifactProof.SchemaID != GenerationProofSchemaID || artifactProof.SchemaVersion != SemanticSchemaVersion || artifactProof.ProofID != proof.ProofID || artifactProof.GenerationID != proof.GenerationID || artifactProof.ComputedBasisID != proof.ComputedBasisID || artifactProof.ComputedSnapshotID != proof.ComputedSnapshotID || artifactProof.ValidatedAgainstSnapshotID != proof.ValidatedAgainstSnapshotID || artifactProof.ExpectedLiveHeadSnapshotID != proof.ExpectedLiveHeadSnapshotID {
		return errors.New("evidence pack: proof artifact bytes do not match current proof")
	}
	if proof.GenerationID != req.Map.GenerationID || proof.ComputedBasisID != req.Map.ComputedBasisID || proof.ComputedSnapshotID != req.Snapshot.SnapshotID || proof.ValidatedAgainstSnapshotID != req.Map.ValidatedAgainstSnapshotID || proof.ValidatedAgainstSnapshotID != req.Snapshot.SnapshotID || proof.ExpectedLiveHeadSnapshotID != req.Snapshot.SnapshotID || pointer.GenerationID != proof.GenerationID || pointer.ComputedBasisID != proof.ComputedBasisID || pointer.ValidatedAgainstSnapshotID != proof.ValidatedAgainstSnapshotID || pointer.ExpectedLiveHeadSnapshotID != proof.ExpectedLiveHeadSnapshotID || pointer.WorkspaceEpoch != proof.WorkspaceEpoch {
		return errors.New("evidence pack: current publication proof does not bind map, snapshot, or live head")
	}
	if req.LiveHeadSnapshotID == "" || req.LiveHeadSnapshotID != proof.ExpectedLiveHeadSnapshotID {
		return errors.New("evidence pack: live head snapshot identity is missing or mismatched")
	}
	if req.Map.Basis.RepositoryID == "" || req.Map.Basis.WorktreeID == "" || req.Map.Basis.ComputedWorkspaceSnapshotID == "" || req.Map.Basis.ComputedWorkspaceSnapshotID != proof.ComputedSnapshotID || req.Map.Basis.ComputedBasisID != proof.ComputedBasisID || req.Map.Basis.WorkspaceEpoch != proof.WorkspaceEpoch || req.Snapshot.WorkspaceEpoch != proof.WorkspaceEpoch || req.Snapshot.RepositoryID != pointer.RepositoryID || req.Snapshot.WorktreeID != pointer.WorktreeID || req.Map.Basis.RepositoryID != pointer.RepositoryID || req.Map.Basis.WorktreeID != pointer.WorktreeID || req.Map.Task.TaskID != pointer.TaskID || req.Map.Task.IntentRevision != pointer.TaskIntentRevision {
		return errors.New("evidence pack: map basis does not bind current proof workspace identity")
	}
	gates := proof.CurrentPublication
	if gates.Eligibility != "passed" || gates.SnapshotGate != "passed" || gates.ClosureGate != "passed" || gates.EvidenceGate != "passed" || gates.SemanticAtomicityGate != "passed" || gates.TaskRelevanceGate != "passed" || gates.ComprehensionGate != "passed" {
		return errors.New("evidence pack: current publication proof has not passed every gate")
	}
	if proof.ArtifactRefs.SemanticMap == "" || proof.ArtifactRefs.AnalysisReadSet == "" || proof.ArtifactRefs.ObservationClosure == "" || proof.ArtifactRefs.AnalyzerResult == "" {
		return errors.New("evidence pack: current publication proof is missing canonical artifact references")
	}
	return nil
}

// isCanonicalJSONArtifact accepts the two canonical encodings used by the
// immutable artifact stores: compact JSON for content-addressed map objects
// and indented JSON for persisted proof manifests. Arbitrary formatting is
// rejected while preserving the exact bytes used for the CAS reference.
func isCanonicalJSONArtifact(raw []byte, value any) bool {
	compact, err := json.Marshal(value)
	if err != nil {
		return false
	}
	if bytes.Equal(raw, compact) {
		return true
	}
	indented, err := json.MarshalIndent(value, "", "  ")
	return err == nil && bytes.Equal(raw, indented)
}

func uniqueSortedStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			seen[value] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for value := range seen {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func normalizePackPaths(paths []string) ([]string, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	normalized := make([]string, 0, len(paths))
	for _, raw := range paths {
		if raw == "." {
			normalized = append(normalized, raw)
			continue
		}
		path, err := workspace.NormalizeRepositoryPath(raw)
		if err != nil {
			return nil, fmt.Errorf("evidence pack: scope path: %w", err)
		}
		normalized = append(normalized, path)
	}
	return uniqueSortedStrings(normalized), nil
}

func pathInScope(path string, scope []string) bool {
	if len(scope) == 0 {
		return true
	}
	for _, root := range scope {
		if root == "." {
			return true
		}
		if path == root || strings.HasPrefix(path, root+"/") {
			return true
		}
	}
	return false
}

func validateTargetSymbolPath(symbolPath string) error {
	if strings.TrimSpace(symbolPath) == "" {
		return errors.New("evidence pack: target symbol path is required")
	}
	if strings.ContainsAny(symbolPath, "\x00\\") || strings.HasPrefix(symbolPath, "/") {
		return fmt.Errorf("evidence pack: target symbol path is unsafe: %q", symbolPath)
	}
	for _, segment := range strings.Split(symbolPath, "/") {
		if segment == ".." {
			return fmt.Errorf("evidence pack: target symbol path is unsafe: %q", symbolPath)
		}
	}
	return nil
}

func marshalPack(pack *EvidencePack) []byte {
	if pack == nil {
		return nil
	}
	data, err := json.Marshal(pack)
	if err != nil {
		return nil
	}
	return data
}

func deterministicPackID(pack *EvidencePack) string {
	if pack == nil {
		return ""
	}
	copy := *pack
	copy.EvidencePackID = ""
	copy.PackDigest = ""
	sum := sha256.Sum256(marshalPack(&copy))
	return "pack-v2-" + hex.EncodeToString(sum[:])[:24]
}

func digestPack(pack *EvidencePack) string {
	if pack == nil {
		return ""
	}
	copy := *pack
	copy.PackDigest = ""
	sum := sha256.Sum256(marshalPack(&copy))
	return hex.EncodeToString(sum[:])
}

// ValidateEvidencePackV2 validates the cross-field guarantees that cannot be
// expressed by a JSON schema alone. It is safe to call immediately before a
// model or browser egress boundary.
func ValidateEvidencePackV2(pack *EvidencePack) error {
	if pack == nil {
		return errors.New("evidence pack: pack is required")
	}
	if pack.SchemaID != EvidencePackV2SchemaID || pack.SchemaVersion != 2 {
		return errors.New("evidence pack: unsupported schema")
	}
	if pack.EvidencePackID == "" || pack.TargetSymbolPath == "" || pack.ComputedBasisID == "" || pack.GenerationID == "" || pack.SnapshotID == "" || pack.PackDigest == "" || pack.RepositoryID == "" || pack.WorktreeID == "" || pack.WorkspaceEpoch < 0 || len(pack.TargetStepIDs) == 0 || len(pack.ScopePaths) == 0 {
		return errors.New("evidence pack: immutable identity is incomplete")
	}
	if err := validateTargetSymbolPath(pack.TargetSymbolPath); err != nil {
		return err
	}
	seenTargetIDs := make(map[string]struct{}, len(pack.TargetStepIDs))
	for _, targetID := range pack.TargetStepIDs {
		if strings.TrimSpace(targetID) == "" {
			return errors.New("evidence pack: target step identity is incomplete")
		}
		if _, exists := seenTargetIDs[targetID]; exists {
			return fmt.Errorf("evidence pack: duplicate target step %q", targetID)
		}
		seenTargetIDs[targetID] = struct{}{}
	}
	seenScopePaths := make(map[string]struct{}, len(pack.ScopePaths))
	for _, rawPath := range pack.ScopePaths {
		if rawPath == "." {
			if _, exists := seenScopePaths[rawPath]; exists {
				return fmt.Errorf("evidence pack: duplicate scope path %q", rawPath)
			}
			seenScopePaths[rawPath] = struct{}{}
			continue
		}
		path, err := workspace.NormalizeRepositoryPath(rawPath)
		if err != nil || path != rawPath {
			return fmt.Errorf("evidence pack: scope path %q is not canonical", rawPath)
		}
		if _, exists := seenScopePaths[path]; exists {
			return fmt.Errorf("evidence pack: duplicate scope path %q", path)
		}
		seenScopePaths[path] = struct{}{}
	}
	if len(pack.Items) == 0 || len(pack.Items) > defaultEvidencePackMaxItems {
		return errors.New("evidence pack: item bound is invalid")
	}
	if pack.RedactionStatus != "clean" && pack.RedactionStatus != "redacted" {
		return errors.New("evidence pack: redaction status is invalid")
	}
	seen := make(map[string]struct{}, len(pack.Items))
	for _, item := range pack.Items {
		if item.EvidenceID == "" || item.Source == "" || item.Content == "" || !item.Verified {
			return fmt.Errorf("evidence pack: item %q is not verified and complete", item.EvidenceID)
		}
		if _, ok := seen[item.EvidenceID]; ok {
			return fmt.Errorf("evidence pack: duplicate item %q", item.EvidenceID)
		}
		seen[item.EvidenceID] = struct{}{}
		path, err := workspace.NormalizeRepositoryPath(item.Source)
		if err != nil {
			return fmt.Errorf("evidence pack: item %q path: %w", item.EvidenceID, err)
		}
		if path != item.Source {
			return fmt.Errorf("evidence pack: item %q path is not canonical", item.EvidenceID)
		}
		if item.SnapshotID != pack.SnapshotID || item.ComputedBasisID != pack.ComputedBasisID {
			return fmt.Errorf("evidence pack: item %q identity does not match pack", item.EvidenceID)
		}
		if item.ByteRange[0] < 0 || item.ByteRange[1] < 0 || item.ByteRange[1] < item.ByteRange[0] {
			return fmt.Errorf("evidence pack: item %q has an invalid byte range", item.EvidenceID)
		}
		contentDigest := sha256.Sum256([]byte(item.Content))
		if item.ContentDigest == "" || !strings.EqualFold(item.ContentDigest, hex.EncodeToString(contentDigest[:])) {
			return fmt.Errorf("evidence pack: item %q content digest does not match content bytes", item.EvidenceID)
		}
		clean, _, err := secret.RedactJSON([]byte(item.Content))
		if err != nil {
			return fmt.Errorf("evidence pack: item %q redaction: %w", item.EvidenceID, err)
		}
		if string(clean) != item.Content {
			return fmt.Errorf("evidence pack: item %q contains unredacted secret-bearing content", item.EvidenceID)
		}
	}
	if got := deterministicPackID(pack); got != pack.EvidencePackID {
		return fmt.Errorf("evidence pack: identity mismatch: got %s want %s", pack.EvidencePackID, got)
	}
	if got := digestPack(pack); got != pack.PackDigest {
		return fmt.Errorf("evidence pack: digest mismatch: got %s want %s", pack.PackDigest, got)
	}
	return nil
}

// Proposal categories are intentionally closed. A model cannot add a new
// authority-bearing category through free-form text.
var AllowedProposalCategories = map[string]struct{}{
	"entry": {}, "business_rule": {}, "orchestration": {}, "mutation": {},
	"infrastructure": {}, "exit": {},
}

// ProposalValidationContext identifies the current map and the exact pack a
// proposal must reference.
type ProposalValidationContext struct {
	Map                    *SemanticMapIR
	Pack                   *EvidencePack
	TargetStepIDs          []string
	TargetSymbolPath       string
	ExpectedPackDigest     string
	ExpectedModelID        string
	ExpectedModelRevision  string
	ExpectedPromptRevision string
	ExpectedSchemaProfile  string
}

// ValidateModelProposalV2 rejects unsupported authority, targets, evidence
// references and basis identities before a proposal is exposed as inferred.
func ValidateModelProposalV2(proposal *ModelProposal, ctx ProposalValidationContext) error {
	if proposal == nil {
		return errors.New("semantic proposal: proposal is required")
	}
	if proposal.SchemaID != SemanticProposalV2SchemaID || proposal.SchemaVersion != 2 {
		return errors.New("semantic proposal: unsupported schema")
	}
	if proposal.ProposalID == "" || proposal.ComputedBasisID == "" || proposal.GenerationID == "" || proposal.SnapshotID == "" || proposal.TargetStepID == "" || proposal.TargetSymbolPath == "" {
		return errors.New("semantic proposal: immutable target identity is incomplete")
	}
	if proposal.EpistemicStatus != "inferred" || (proposal.Authority != "model" && proposal.Authority != "inferred") {
		return errors.New("semantic proposal: authority must remain inferred model output")
	}
	if proposal.ClaimScope != "expression_only" && proposal.ClaimScope != "display_only" {
		return errors.New("semantic proposal: claim scope must be display-only expression text")
	}
	if _, ok := AllowedProposalCategories[proposal.ProposedCategory]; !ok {
		return fmt.Errorf("semantic proposal: category %q is not allowed", proposal.ProposedCategory)
	}
	if ctx.Map == nil || ctx.Pack == nil {
		return errors.New("semantic proposal: map and evidence pack are required")
	}
	if err := ValidateEvidencePackV2(ctx.Pack); err != nil {
		return fmt.Errorf("semantic proposal: invalid evidence pack: %w", err)
	}
	if proposal.ComputedBasisID != ctx.Map.ComputedBasisID || proposal.ComputedBasisID != ctx.Pack.ComputedBasisID || proposal.GenerationID != ctx.Map.GenerationID || proposal.GenerationID != ctx.Pack.GenerationID || proposal.SnapshotID != ctx.Map.ValidatedAgainstSnapshotID || proposal.SnapshotID != ctx.Pack.SnapshotID {
		return errors.New("semantic proposal: basis, generation, or snapshot does not match current map")
	}
	if proposal.PackDigest == "" || proposal.ModelID == "" || proposal.ModelRevision == "" || proposal.PromptRevision == "" || proposal.SchemaProfile == "" {
		return errors.New("semantic proposal: model provenance is incomplete")
	}
	if proposal.PackDigest != ctx.Pack.PackDigest {
		return errors.New("semantic proposal: evidence pack digest does not match current pack")
	}
	if ctx.ExpectedPackDigest != "" && proposal.PackDigest != ctx.ExpectedPackDigest {
		return errors.New("semantic proposal: evidence pack digest does not match expected provenance")
	}
	if ctx.ExpectedModelID != "" && proposal.ModelID != ctx.ExpectedModelID {
		return errors.New("semantic proposal: model identity does not match expected provenance")
	}
	if ctx.ExpectedModelRevision != "" && proposal.ModelRevision != ctx.ExpectedModelRevision {
		return errors.New("semantic proposal: model revision does not match expected provenance")
	}
	if proposal.PromptRevision != ctx.ExpectedPromptRevision {
		return errors.New("semantic proposal: prompt revision does not match expected provenance")
	}
	expectedSchemaProfile := ctx.ExpectedSchemaProfile
	if expectedSchemaProfile == "" {
		expectedSchemaProfile = SemanticProposalSchemaProfile
	}
	if proposal.SchemaProfile != expectedSchemaProfile {
		return errors.New("semantic proposal: schema profile does not match expected provenance")
	}
	targetAllowed := ctx.TargetSymbolPath == "" || proposal.TargetSymbolPath == ctx.TargetSymbolPath
	stepAllowed := len(ctx.TargetStepIDs) == 0
	var targetStep *SemanticStep
	for i := range ctx.Map.Steps {
		step := &ctx.Map.Steps[i]
		if step.StepID == proposal.TargetStepID {
			targetStep = step
			break
		}
	}
	if targetStep == nil {
		return fmt.Errorf("semantic proposal: target step %q does not exist", proposal.TargetStepID)
	}
	for _, id := range ctx.TargetStepIDs {
		if id == proposal.TargetStepID {
			stepAllowed = true
			break
		}
	}
	if !targetAllowed || !stepAllowed {
		return errors.New("semantic proposal: target is outside the requested scope")
	}
	if targetStep.Name != proposal.TargetSymbolPath && targetStep.TechnicalName != proposal.TargetSymbolPath && targetStep.StructuralIdentity != proposal.TargetSymbolPath {
		return errors.New("semantic proposal: target symbol does not match the canonical step")
	}
	items := make(map[string]EvidenceItem, len(ctx.Pack.Items))
	for _, item := range ctx.Pack.Items {
		items[item.EvidenceID] = item
	}
	if len(proposal.EvidenceRefs) == 0 {
		return errors.New("semantic proposal: at least one evidence reference is required")
	}
	seen := make(map[string]struct{}, len(proposal.EvidenceRefs))
	for _, ref := range proposal.EvidenceRefs {
		if _, ok := seen[ref]; ok {
			return fmt.Errorf("semantic proposal: duplicate evidence reference %q", ref)
		}
		item, ok := items[ref]
		if !ok || !item.Verified {
			return fmt.Errorf("semantic proposal: evidence reference %q is not in the bounded pack", ref)
		}
		seen[ref] = struct{}{}
	}
	if proposal.FactDigest == "" || proposal.ObligationDigest == "" || proposal.AlignmentDigest == "" || proposal.SettlementDigest == "" {
		return errors.New("semantic proposal: Q3 canonical digests are required")
	}
	digests, err := CanonicalQ3Digests(ctx.Map)
	if err != nil {
		return err
	}
	if proposal.FactDigest != digests.Fact || proposal.ObligationDigest != digests.Obligation || proposal.AlignmentDigest != digests.Alignment || proposal.SettlementDigest != digests.Settlement {
		return errors.New("semantic proposal: Q3 canonical digest mismatch")
	}
	return nil
}

// ValidateModelProposal is the short public validator name.
func ValidateModelProposal(proposal *ModelProposal, ctx ProposalValidationContext) error {
	return ValidateModelProposalV2(proposal, ctx)
}

// Q3CanonicalDigests are the immutable digest anchors used by Q4 display
// proposals. They deliberately match the existing refinement gate.
type Q3CanonicalDigests struct {
	Fact       string `json:"factDigest"`
	Obligation string `json:"obligationDigest"`
	Alignment  string `json:"alignmentDigest"`
	Settlement string `json:"settlementDigest"`
}

// CanonicalQ3Digests exposes the same digest construction used by late
// refinement publication, without allowing proposal code to mutate Q3 data.
func CanonicalQ3Digests(mapIR *SemanticMapIR) (Q3CanonicalDigests, error) {
	digests, err := q3Digests(mapIR)
	if err != nil {
		return Q3CanonicalDigests{}, err
	}
	return Q3CanonicalDigests{Fact: digests.Fact, Obligation: digests.Obligation, Alignment: digests.Alignment, Settlement: digests.Settlement}, nil
}

// SemanticProposalView is a display-only Q4 projection over an unchanged Q3
// map. Proposal text is never copied into Fact, obligation, alignment or
// settlement fields.
type SemanticProposalView struct {
	Map      *SemanticMapIR     `json:"map"`
	Proposal *ModelProposal     `json:"proposal"`
	Digests  Q3CanonicalDigests `json:"q3Digests"`
}

func AcceptSemanticProposal(mapIR *SemanticMapIR, proposal *ModelProposal, pack *EvidencePack) (*SemanticProposalView, error) {
	if mapIR == nil {
		return nil, errors.New("semantic proposal: map is required")
	}
	ctx := ProposalValidationContext{Map: mapIR, Pack: pack, TargetStepIDs: packTargetStepIDs(pack), ExpectedSchemaProfile: SemanticProposalSchemaProfile, ExpectedPromptRevision: proposalPromptRevision(proposal)}
	if pack != nil {
		ctx.TargetSymbolPath = pack.TargetSymbolPath
		ctx.ExpectedPackDigest = pack.PackDigest
	}
	if err := ValidateModelProposalV2(proposal, ctx); err != nil {
		return nil, err
	}
	before, err := CanonicalQ3Digests(mapIR)
	if err != nil {
		return nil, err
	}
	clone := cloneSemanticMap(mapIR)
	clone.EnrichmentStatus = "available"
	after, err := CanonicalQ3Digests(clone)
	if err != nil {
		return nil, err
	}
	if before != after {
		return nil, errors.New("semantic proposal: accepting Q4 proposal changed a Q3 canonical digest")
	}
	proposalCopy := *proposal
	return &SemanticProposalView{Map: clone, Proposal: &proposalCopy, Digests: before}, nil
}

// DeterministicFallback is the no-host or failed-enrichment result. It is
// derived only from canonical step names and references and is never inferred.
type DeterministicFallback struct {
	SchemaID         string   `json:"schemaId"`
	SchemaVersion    int      `json:"schemaVersion"`
	FallbackID       string   `json:"fallbackId"`
	TargetStepID     string   `json:"targetStepId"`
	TargetSymbolPath string   `json:"targetSymbolPath"`
	Title            string   `json:"title"`
	Category         string   `json:"category"`
	Authority        string   `json:"authority"`
	EpistemicStatus  string   `json:"epistemicStatus"`
	EvidenceRefs     []string `json:"evidenceRefs,omitempty"`
	Reason           string   `json:"reason"`
}

func BuildDeterministicFallback(mapIR *SemanticMapIR, targetStepID, reason string) *DeterministicFallback {
	fallback := &DeterministicFallback{SchemaID: SemanticProposalV2SchemaID, SchemaVersion: 2, Authority: "deterministic", EpistemicStatus: "deterministic", Reason: reason}
	if mapIR == nil {
		return nil
	}
	matched := false
	for _, step := range mapIR.Steps {
		if targetStepID != "" && step.StepID != targetStepID {
			continue
		}
		matched = true
		fallback.TargetStepID = step.StepID
		fallback.TargetSymbolPath = firstNonEmpty(step.TechnicalName, step.Name, step.StructuralIdentity)
		fallback.Title = firstNonEmpty(step.Name, step.TechnicalName, step.StepID)
		fallback.Category = deterministicCategory(step.Kind)
		fallback.EvidenceRefs = append([]string(nil), step.EvidenceRefs...)
		break
	}
	if !matched {
		return nil
	}
	if fallback.FallbackID == "" {
		h := sha256.Sum256([]byte(mapIR.MapID + "\x00" + fallback.TargetStepID + "\x00" + fallback.Title))
		fallback.FallbackID = "fallback-v2-" + hex.EncodeToString(h[:])[:24]
	}
	return fallback
}

func completeDeterministicFallback(mapIR *SemanticMapIR, targetStepID, reason string) *DeterministicFallback {
	fallback := BuildDeterministicFallback(mapIR, targetStepID, reason)
	if fallback == nil || fallback.FallbackID == "" || fallback.TargetStepID == "" || fallback.TargetSymbolPath == "" || fallback.Title == "" || fallback.Category == "" {
		return nil
	}
	return fallback
}

func deterministicCategory(kind string) string {
	switch strings.ToLower(kind) {
	case "entry", "user_action", "trigger", "route":
		return "entry"
	case "guard", "branch", "rule", "validation":
		return "business_rule"
	case "call", "orchestration", "workflow", "transform":
		return "orchestration"
	case "mutation", "effect", "external_effect", "write":
		return "mutation"
	case "exit", "result", "return":
		return "exit"
	default:
		return "infrastructure"
	}
}

func packTargetStepIDs(pack *EvidencePack) []string {
	if pack == nil {
		return nil
	}
	return append([]string(nil), pack.TargetStepIDs...)
}

func cloneSemanticMap(mapIR *SemanticMapIR) *SemanticMapIR {
	if mapIR == nil {
		return nil
	}
	clone := *mapIR
	if mapIR.Steps != nil {
		clone.Steps = append([]SemanticStep{}, mapIR.Steps...)
	}
	if mapIR.Edges != nil {
		clone.Edges = append([]SemanticEdge{}, mapIR.Edges...)
	}
	if mapIR.Evidence != nil {
		clone.Evidence = append([]SemanticEvidence{}, mapIR.Evidence...)
	}
	if mapIR.RequirementAlignment != nil {
		clone.RequirementAlignment = append([]RequirementAlignment{}, mapIR.RequirementAlignment...)
	}
	if mapIR.Unknowns != nil {
		clone.Unknowns = append([]fusion.Unknown{}, mapIR.Unknowns...)
	}
	if mapIR.BoundaryTargets != nil {
		clone.BoundaryTargets = append([]string{}, mapIR.BoundaryTargets...)
	}
	if mapIR.Quality.CriticalObligations != nil {
		clone.Quality.CriticalObligations = append([]CriticalObligation{}, mapIR.Quality.CriticalObligations...)
	}
	if mapIR.Quality.Degradations != nil {
		clone.Quality.Degradations = append([]QualityDegradation{}, mapIR.Quality.Degradations...)
	}
	if mapIR.Coverage != nil {
		coverage := *mapIR.Coverage
		coverage.IncludedSourceRoots = append([]string(nil), mapIR.Coverage.IncludedSourceRoots...)
		coverage.ExcludedReasons = append([]string(nil), mapIR.Coverage.ExcludedReasons...)
		clone.Coverage = &coverage
	}
	return &clone
}

// EnrichmentState is the status projection consumed by FlowView and MCP.
type EnrichmentState struct {
	SchemaID      string                              `json:"schemaId"`
	SchemaVersion int                                 `json:"schemaVersion"`
	Status        string                              `json:"status"`
	Reason        string                              `json:"reason,omitempty"`
	PackDigest    string                              `json:"packDigest,omitempty"`
	ProposalID    string                              `json:"proposalId,omitempty"`
	Fallback      *DeterministicFallback              `json:"fallback,omitempty"`
	Capability    protocol.ModelHostCapability        `json:"capability"`
	Isolation     protocol.ModelHostIsolationEvidence `json:"isolation,omitempty"`
	UpdatedAt     string                              `json:"updatedAt"`
}

// MarshalJSON omits the optional isolation object until a model-host request
// has actually been attempted. A value-typed struct otherwise defeats
// omitempty and emits empty fields such as an invalid empty pack digest.
func (s EnrichmentState) MarshalJSON() ([]byte, error) {
	type stateWire struct {
		SchemaID      string                               `json:"schemaId"`
		SchemaVersion int                                  `json:"schemaVersion"`
		Status        string                               `json:"status"`
		Reason        string                               `json:"reason,omitempty"`
		PackDigest    string                               `json:"packDigest,omitempty"`
		ProposalID    string                               `json:"proposalId,omitempty"`
		Fallback      *DeterministicFallback               `json:"fallback,omitempty"`
		Capability    protocol.ModelHostCapability         `json:"capability"`
		Isolation     *protocol.ModelHostIsolationEvidence `json:"isolation,omitempty"`
		UpdatedAt     string                               `json:"updatedAt"`
	}
	var isolation *protocol.ModelHostIsolationEvidence
	if hasIsolationEvidence(s.Isolation) {
		value := s.Isolation
		isolation = &value
	}
	return json.Marshal(stateWire{
		SchemaID: s.SchemaID, SchemaVersion: s.SchemaVersion, Status: s.Status,
		Reason: s.Reason, PackDigest: s.PackDigest, ProposalID: s.ProposalID,
		Fallback: s.Fallback, Capability: s.Capability, Isolation: isolation, UpdatedAt: s.UpdatedAt,
	})
}

func hasIsolationEvidence(evidence protocol.ModelHostIsolationEvidence) bool {
	return evidence.SourceDelivery != "" || evidence.SourceMount != "" || evidence.WorkingDirectoryMode != "" || evidence.WorkingDirectoryPermission != "" || evidence.Disposable || evidence.RepositoryPathExposed || evidence.RepositoryWriteCapability || len(evidence.RepositoryWriteAttempts) > 0 || evidence.RepositoryWriteAuditStatus != "" || evidence.PackDigest != "" || evidence.CapabilityStatus != "" || evidence.TerminalStatus != "" || evidence.CleanupVerified || evidence.IsolationBackend != "" || evidence.EnforcementStatus != "" || evidence.RepositoryReadAttempt != "" || evidence.RepositoryWriteAttempt != "" || evidence.SentinelBeforeDigest != "" || evidence.SentinelAfterDigest != "" || evidence.SentinelUnchanged || evidence.NetworkAttempt != "" || evidence.NetworkPolicy != "" || evidence.PolicyDigest != "" || evidence.RuntimePolicyBinding != "" || evidence.RuntimePolicySharedBaseDigest != "" || evidence.ProbeScope != "" || evidence.ProbePolicyDigest != "" || evidence.ProbeSharedBaseDigest != "" || evidence.CoreTrustedProbe || evidence.ResourceLimits != nil
}

type EnrichmentRequest struct {
	EvidencePack EvidencePackRequest
	// modelHost is an unexported pure-validation seam. Production callers must
	// use ModelHostFactory so Core owns a fresh supervised host per operation.
	modelHost        protocol.ModelHostClient
	ModelHostFactory protocol.ModelHostFactory
	PromptRevision   string
	MaxAttempts      int
}

type EnrichmentResult struct {
	State       EnrichmentState        `json:"state"`
	Pack        *EvidencePack          `json:"pack,omitempty"`
	Proposal    *ModelProposal         `json:"proposal,omitempty"`
	Fallback    *DeterministicFallback `json:"fallback,omitempty"`
	View        *SemanticProposalView  `json:"-"`
	coreHost    protocol.ModelHostClient
	coreTrust   *protocol.CoreHostAttestation
	contentSeal enrichmentContentSeal
}

// enrichmentContentSeal is an in-memory Core-owned binding for the exact
// proposal and evidence-pack bytes accepted by the supervised enrichment
// operation. It is deliberately not serialized or exported. A caller can copy
// an EnrichmentResult value, but changing any exported proposal or pack field
// invalidates this binding before persistence or public egress.
type enrichmentContentSeal struct {
	proposalDigest [sha256.Size]byte
	packDigest     [sha256.Size]byte
	sealed         bool
}

func sealEnrichmentContent(proposal *ModelProposal, pack *EvidencePack) (enrichmentContentSeal, error) {
	if proposal == nil || pack == nil {
		return enrichmentContentSeal{}, errors.New("enrichment content seal requires proposal and evidence pack")
	}
	proposalBytes, err := json.Marshal(proposal)
	if err != nil {
		return enrichmentContentSeal{}, fmt.Errorf("marshal proposal for content seal: %w", err)
	}
	packBytes, err := json.Marshal(pack)
	if err != nil {
		return enrichmentContentSeal{}, fmt.Errorf("marshal evidence pack for content seal: %w", err)
	}
	return enrichmentContentSeal{proposalDigest: sha256.Sum256(proposalBytes), packDigest: sha256.Sum256(packBytes), sealed: true}, nil
}

func (seal enrichmentContentSeal) verify(proposal *ModelProposal, pack *EvidencePack) error {
	if !seal.sealed {
		return errors.New("accepted enrichment content seal is missing")
	}
	current, err := sealEnrichmentContent(proposal, pack)
	if err != nil {
		return err
	}
	if current.proposalDigest != seal.proposalDigest || current.packDigest != seal.packDigest {
		return errors.New("accepted enrichment content seal does not match current proposal or evidence pack bytes")
	}
	return nil
}

// ValidateEnrichmentState checks the public status envelope before it crosses
// a FlowView or MCP boundary. Unsupported states remain explicit failures and
// cannot be interpreted as an inferred proposal.
func ValidateEnrichmentState(state EnrichmentState) error {
	if state.SchemaID != EnrichmentStateV2SchemaID || state.SchemaVersion != 2 || state.UpdatedAt == "" {
		return errors.New("enrichment state: unsupported or incomplete schema")
	}
	switch state.Status {
	case "not_requested", "pending", "available", "timed_out", "unavailable":
	default:
		return fmt.Errorf("enrichment state: unsupported status %q", state.Status)
	}
	if state.Capability.Status == "" {
		return errors.New("enrichment state: capability status is required")
	}
	if state.Status == "available" && state.ProposalID == "" {
		return errors.New("enrichment state: available result requires a proposal identity")
	}
	if state.Status == "available" && !state.Capability.IsMeasured() {
		return errors.New("enrichment state: available result requires measured capability")
	}
	if state.Status == "available" {
		if state.PackDigest == "" {
			return errors.New("enrichment state: available result requires an evidence-pack digest")
		}
		if err := protocol.ValidateModelHostIsolationEvidenceForPack(state.Isolation, state.Capability, state.PackDigest, true); err != nil {
			return fmt.Errorf("enrichment state: available result has invalid isolation evidence: %w", err)
		}
	}
	if state.PackDigest != "" && state.Isolation.PackDigest != "" && state.PackDigest != state.Isolation.PackDigest {
		return errors.New("enrichment state: isolation pack digest does not match state")
	}
	return nil
}

// ValidateEnrichmentResult is the final typed egress check for optional
// enrichment. It validates any included pack and prevents an available state
// from being emitted without its corresponding proposal object.
func ValidateEnrichmentResult(result *EnrichmentResult) error {
	if result == nil {
		return errors.New("enrichment result: result is required")
	}
	if err := ValidateEnrichmentState(result.State); err != nil {
		return err
	}
	if result.State.Status == "available" || result.Proposal != nil {
		if !protocol.VerifyCoreHostAttestation(result.coreHost, result.coreTrust) {
			return errors.New("enrichment result: available proposal lacks Core-supervised host authority")
		}
	}
	if result.State.Status == "available" {
		if err := result.contentSeal.verify(result.Proposal, result.Pack); err != nil {
			return fmt.Errorf("enrichment result: accepted proposal content is not Core-bound: %w", err)
		}
	}
	if result.Pack != nil {
		if err := ValidateEvidencePackV2(result.Pack); err != nil {
			return err
		}
		if result.State.PackDigest != result.Pack.PackDigest {
			return errors.New("enrichment result: pack digest does not match state")
		}
	}
	if result.State.Status == "available" && result.Proposal == nil {
		return errors.New("enrichment result: available result requires a proposal")
	}
	if result.Proposal != nil && result.State.Status != "available" {
		return errors.New("enrichment result: proposal requires an available state")
	}
	if result.Proposal != nil && result.Pack == nil {
		return errors.New("enrichment result: proposal requires its evidence pack")
	}
	if result.Proposal != nil && result.State.ProposalID != result.Proposal.ProposalID {
		return errors.New("enrichment result: proposal identity does not match state")
	}
	if result.Proposal != nil {
		if result.View != nil && result.View.Map != nil {
			if err := ValidateModelProposalV2(result.Proposal, ProposalValidationContext{
				Map: result.View.Map, Pack: result.Pack, TargetStepIDs: result.Pack.TargetStepIDs, TargetSymbolPath: result.Pack.TargetSymbolPath,
				ExpectedPackDigest: result.Pack.PackDigest, ExpectedModelID: result.State.Capability.ModelID, ExpectedModelRevision: result.State.Capability.Revision,
				ExpectedPromptRevision: result.Proposal.PromptRevision, ExpectedSchemaProfile: SemanticProposalSchemaProfile,
			}); err != nil {
				return fmt.Errorf("enrichment result: proposal grounding: %w", err)
			}
		} else if err := validateProposalPackBinding(result.Proposal, result.Pack); err != nil {
			return err
		}
	}
	return nil
}

func validateProposalPackBinding(proposal *ModelProposal, pack *EvidencePack) error {
	if proposal.PackDigest == "" || proposal.ModelID == "" || proposal.ModelRevision == "" || proposal.PromptRevision == "" || proposal.SchemaProfile == "" {
		return errors.New("enrichment result: proposal provenance is incomplete")
	}
	if proposal.PackDigest != pack.PackDigest || proposal.TargetSymbolPath != pack.TargetSymbolPath || proposal.ComputedBasisID != pack.ComputedBasisID || proposal.GenerationID != pack.GenerationID || proposal.SnapshotID != pack.SnapshotID {
		return errors.New("enrichment result: proposal does not match evidence-pack provenance")
	}
	if pack.PromptRevision != "" && proposal.PromptRevision != pack.PromptRevision {
		return errors.New("enrichment result: proposal prompt revision does not match evidence-pack provenance")
	}
	allowedSteps := make(map[string]struct{}, len(pack.TargetStepIDs))
	for _, stepID := range pack.TargetStepIDs {
		allowedSteps[stepID] = struct{}{}
	}
	if _, ok := allowedSteps[proposal.TargetStepID]; !ok {
		return errors.New("enrichment result: proposal target is outside the evidence-pack scope")
	}
	items := make(map[string]EvidenceItem, len(pack.Items))
	for _, item := range pack.Items {
		items[item.EvidenceID] = item
	}
	if len(proposal.EvidenceRefs) == 0 {
		return errors.New("enrichment result: proposal evidence references are required")
	}
	for _, ref := range proposal.EvidenceRefs {
		item, ok := items[ref]
		if !ok || !item.Verified {
			return fmt.Errorf("enrichment result: proposal evidence reference %q is outside the evidence pack", ref)
		}
	}
	return nil
}

// ValidateEnrichmentResultContract applies the registered JSON schemas to the
// individual public artifacts after the typed cross-field checks.
func ValidateEnrichmentResultContract(result *EnrichmentResult) error {
	if err := ValidateEnrichmentResult(result); err != nil {
		return err
	}
	stateBytes, err := json.Marshal(result.State)
	if err != nil {
		return err
	}
	if err := contractharness.Validate(EnrichmentStateV2SchemaID, stateBytes); err != nil {
		return fmt.Errorf("enrichment state schema: %w", err)
	}
	if result.Pack != nil {
		packBytes, err := json.Marshal(result.Pack)
		if err != nil {
			return err
		}
		if err := contractharness.Validate(EvidencePackV2SchemaID, packBytes); err != nil {
			return fmt.Errorf("evidence pack schema: %w", err)
		}
	}
	if result.Proposal != nil {
		proposalBytes, err := json.Marshal(result.Proposal)
		if err != nil {
			return err
		}
		if err := contractharness.Validate(SemanticProposalV2SchemaID, proposalBytes); err != nil {
			return fmt.Errorf("semantic proposal schema: %w", err)
		}
	}
	return nil
}

// MarshalEnrichmentResultEgress is the single public JSON egress gate shared
// by FlowView and MCP. It deliberately validates the typed result before
// redaction, then strictly decodes and validates the redacted bytes again.
// Required identity and provenance are compared across both representations so
// redaction can never turn a damaged result into an exposed result.
func MarshalEnrichmentResultEgress(result *EnrichmentResult) ([]byte, error) {
	if err := ValidateEnrichmentResultContract(result); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(struct {
		State    EnrichmentState        `json:"state"`
		Pack     *EvidencePack          `json:"pack"`
		Proposal *ModelProposal         `json:"proposal"`
		Fallback *DeterministicFallback `json:"fallback"`
	}{State: result.State, Pack: result.Pack, Proposal: result.Proposal, Fallback: result.Fallback})
	if err != nil {
		return nil, err
	}
	clean, _, err := secret.RedactJSON(raw)
	if err != nil {
		return nil, err
	}
	var decoded EnrichmentResult
	if err := decodeStrictJSON(clean, &decoded); err != nil {
		return nil, fmt.Errorf("decode redacted enrichment result: %w", err)
	}
	// The opaque authority is deliberately restored only from the already
	// validated in-memory result. It never comes from JSON, so a round-trip
	// cannot manufacture trust while the public egress still requires the
	// original Core-supervised result.
	decoded.coreHost = result.coreHost
	decoded.coreTrust = result.coreTrust
	decoded.contentSeal = result.contentSeal
	if err := ValidateEnrichmentResultContract(&decoded); err != nil {
		return nil, fmt.Errorf("redacted enrichment result validation: %w", err)
	}
	if err := validateEnrichmentEgressIdentity(result, &decoded); err != nil {
		return nil, err
	}
	return clean, nil
}

func validateEnrichmentEgressIdentity(before, after *EnrichmentResult) error {
	if before == nil || after == nil {
		return errors.New("enrichment egress identity: result is required")
	}
	if before.State.SchemaID != after.State.SchemaID || before.State.SchemaVersion != after.State.SchemaVersion || before.State.Status != after.State.Status || before.State.UpdatedAt != after.State.UpdatedAt || before.State.PackDigest != after.State.PackDigest || before.State.ProposalID != after.State.ProposalID {
		return errors.New("enrichment egress identity was changed by redaction")
	}
	if !sameModelHostCapabilityIdentity(before.State.Capability, after.State.Capability) || !sameIsolationIdentity(before.State.Isolation, after.State.Isolation) {
		return errors.New("enrichment egress capability or isolation provenance was changed by redaction")
	}
	if !samePackIdentity(before.Pack, after.Pack) {
		return errors.New("enrichment egress evidence-pack identity was changed by redaction")
	}
	if !sameProposalIdentity(before.Proposal, after.Proposal) || !sameFallbackIdentity(before.Fallback, after.Fallback) {
		return errors.New("enrichment egress proposal identity was changed by redaction")
	}
	return nil
}

func sameModelHostCapabilityIdentity(before, after protocol.ModelHostCapability) bool {
	if before.Status != after.Status || before.ModelID != after.ModelID || before.Revision != after.Revision || before.License != after.License || before.Checksum != after.Checksum || before.Runtime != after.Runtime || before.DataBoundary != after.DataBoundary || before.SchemaConstrained != after.SchemaConstrained || before.Cancellation != after.Cancellation || before.Measured != after.Measured || before.MaxRequestBytes != after.MaxRequestBytes || before.MaxResponseBytes != after.MaxResponseBytes || before.IsolationBackend != after.IsolationBackend || before.IsolationEnforced != after.IsolationEnforced || before.NetworkPolicy != after.NetworkPolicy || before.PolicyDigest != after.PolicyDigest || before.RuntimePolicyBinding != after.RuntimePolicyBinding || before.RuntimePolicySharedBaseDigest != after.RuntimePolicySharedBaseDigest || before.ProbeScope != after.ProbeScope || before.ProbePolicyDigest != after.ProbePolicyDigest || before.ProbeSharedBaseDigest != after.ProbeSharedBaseDigest {
		return false
	}
	if !sameModelHostResourceLimitEvidence(before.ResourceLimits, after.ResourceLimits) {
		return false
	}
	if len(before.Capabilities) != len(after.Capabilities) {
		return false
	}
	for i := range before.Capabilities {
		if before.Capabilities[i] != after.Capabilities[i] {
			return false
		}
	}
	if (before.IsolationProbe == nil) != (after.IsolationProbe == nil) {
		return false
	}
	if before.IsolationProbe != nil && *before.IsolationProbe != *after.IsolationProbe {
		return false
	}
	return true
}

func sameIsolationIdentity(before, after protocol.ModelHostIsolationEvidence) bool {
	if before.SourceDelivery != after.SourceDelivery || before.SourceMount != after.SourceMount || before.WorkingDirectoryMode != after.WorkingDirectoryMode || before.WorkingDirectoryPermission != after.WorkingDirectoryPermission || before.Disposable != after.Disposable || before.RepositoryPathExposed != after.RepositoryPathExposed || before.RepositoryWriteCapability != after.RepositoryWriteCapability || before.RepositoryWriteAuditStatus != after.RepositoryWriteAuditStatus || before.PackDigest != after.PackDigest || before.ReceivedRequestID != after.ReceivedRequestID || before.ReceivedPackDigest != after.ReceivedPackDigest || before.CapabilityStatus != after.CapabilityStatus || before.TerminalStatus != after.TerminalStatus || before.CleanupVerified != after.CleanupVerified || before.IsolationBackend != after.IsolationBackend || before.EnforcementStatus != after.EnforcementStatus || before.RepositoryReadAttempt != after.RepositoryReadAttempt || before.RepositoryWriteAttempt != after.RepositoryWriteAttempt || before.DisposableWriteAttempt != after.DisposableWriteAttempt || before.SentinelBeforeDigest != after.SentinelBeforeDigest || before.SentinelAfterDigest != after.SentinelAfterDigest || before.SentinelUnchanged != after.SentinelUnchanged || before.NetworkAttempt != after.NetworkAttempt || before.NetworkPolicy != after.NetworkPolicy || before.PolicyDigest != after.PolicyDigest || before.RuntimePolicyBinding != after.RuntimePolicyBinding || before.RuntimePolicySharedBaseDigest != after.RuntimePolicySharedBaseDigest || before.ProbeScope != after.ProbeScope || before.ProbePolicyDigest != after.ProbePolicyDigest || before.ProbeSharedBaseDigest != after.ProbeSharedBaseDigest || before.CoreTrustedProbe != after.CoreTrustedProbe || len(before.RepositoryWriteAttempts) != len(after.RepositoryWriteAttempts) {
		return false
	}
	if !sameModelHostResourceLimitEvidence(before.ResourceLimits, after.ResourceLimits) {
		return false
	}
	for i := range before.RepositoryWriteAttempts {
		if before.RepositoryWriteAttempts[i] != after.RepositoryWriteAttempts[i] {
			return false
		}
	}
	return true
}

func sameModelHostResourceLimitEvidence(before, after *protocol.ModelHostResourceLimitEvidence) bool {
	if before == nil || after == nil {
		return before == nil && after == nil
	}
	return *before == *after
}

func samePackIdentity(before, after *EvidencePack) bool {
	if before == nil || after == nil {
		return before == nil && after == nil
	}
	return before.EvidencePackID == after.EvidencePackID && before.PackDigest == after.PackDigest && before.TargetSymbolPath == after.TargetSymbolPath && before.ComputedBasisID == after.ComputedBasisID && before.GenerationID == after.GenerationID && before.RepositoryID == after.RepositoryID && before.WorktreeID == after.WorktreeID && before.SnapshotID == after.SnapshotID && before.WorkspaceEpoch == after.WorkspaceEpoch
}

func sameProposalIdentity(before, after *ModelProposal) bool {
	if before == nil || after == nil {
		return before == nil && after == nil
	}
	return before.SchemaID == after.SchemaID && before.SchemaVersion == after.SchemaVersion && before.ProposalID == after.ProposalID && before.ModelID == after.ModelID && before.ModelRevision == after.ModelRevision && before.PromptRevision == after.PromptRevision && before.SchemaProfile == after.SchemaProfile && before.PackDigest == after.PackDigest && before.ComputedBasisID == after.ComputedBasisID && before.GenerationID == after.GenerationID && before.SnapshotID == after.SnapshotID && before.TargetStepID == after.TargetStepID && before.TargetSymbolPath == after.TargetSymbolPath && before.Authority == after.Authority && before.ClaimScope == after.ClaimScope
}

func sameFallbackIdentity(before, after *DeterministicFallback) bool {
	if before == nil || after == nil {
		return before == nil && after == nil
	}
	return before.SchemaID == after.SchemaID && before.SchemaVersion == after.SchemaVersion && before.FallbackID == after.FallbackID && before.TargetStepID == after.TargetStepID && before.TargetSymbolPath == after.TargetSymbolPath && before.Authority == after.Authority && before.EpistemicStatus == after.EpistemicStatus
}

type SemanticEnrichmentRequest = EnrichmentRequest
type SemanticEnrichmentResult = EnrichmentResult

// RunSemanticEnrichment executes optional enrichment without changing the
// deterministic map. When ModelHostFactory is present it is the only source
// used for the operation host, and that fresh supervised host is closed before
// this function returns. Only a supervised *protocol.ModelHost returned by
// SpawnModelHost can produce an available result. Invalid responses are
// retried at most once, then become an explicit unavailable state with a
// deterministic fallback.
func RunSemanticEnrichment(ctx context.Context, req EnrichmentRequest) EnrichmentResult {
	return runSemanticEnrichment(ctx, req, true)
}

func runSemanticEnrichment(ctx context.Context, req EnrichmentRequest, requireCoreTrust bool) (result EnrichmentResult) {
	result = EnrichmentResult{State: EnrichmentState{SchemaID: EnrichmentStateV2SchemaID, SchemaVersion: 2, Status: "not_requested", Capability: protocol.ModelHostCapability{Status: "unavailable"}, UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano)}}
	// A supplied host belongs to this enrichment operation. Factory-created
	// hosts are installed only after the evidence pack has passed validation and
	// are closed on every return, including capability and request failures.
	modelHost := req.modelHost
	hostOwned := modelHost != nil
	hostClosed := false
	closeHost := func() error {
		if modelHost == nil || !hostOwned || hostClosed {
			return nil
		}
		hostClosed = true
		return modelHost.Close()
	}
	defer func() {
		if modelHost == nil || !hostOwned || hostClosed {
			return
		}
		cleanupErr := closeHost()
		result.State.Isolation = modelHost.IsolationEvidence()
		if cleanupErr == nil && !result.State.Isolation.CleanupVerified {
			cleanupErr = errors.New("model host cleanup evidence was not verified")
		}
		if cleanupErr != nil {
			result.Proposal = nil
			result.View = nil
			result.State.Status = "unavailable"
			if result.State.Reason == "" {
				result.State.Reason = fmt.Sprintf("model host cleanup: %v", cleanupErr)
			} else {
				result.State.Reason += "; model host cleanup: " + cleanupErr.Error()
			}
		}
	}()
	if req.EvidencePack.Map == nil {
		result.State.Status = "unavailable"
		result.State.Reason = "semantic map is unavailable"
		return result
	}
	result.Fallback = completeDeterministicFallback(req.EvidencePack.Map, requestedTargetStepID(req.EvidencePack), "enrichment unavailable")
	result.State.Fallback = result.Fallback
	pack, packErr := BuildEvidencePackV2(req.EvidencePack)
	if packErr != nil {
		result.State.Status = "unavailable"
		result.State.Reason = packErr.Error()
		if result.Fallback != nil {
			result.Fallback.Reason = packErr.Error()
		}
		return result
	}
	result.Pack = pack
	result.State.PackDigest = pack.PackDigest
	if err := ValidateEvidencePackV2(pack); err != nil {
		result.State.Status = "unavailable"
		result.State.Reason = err.Error()
		if result.Fallback != nil {
			result.Fallback.Reason = err.Error()
		}
		return result
	}
	if req.ModelHostFactory != nil && req.modelHost != nil {
		result.State.Status = "unavailable"
		result.State.Reason = "both model host and model host factory were supplied"
		return result
	}
	ctx = enrichmentContext(ctx)
	if req.ModelHostFactory != nil {
		var spawnErr error
		spawnedHost, spawnErr := req.ModelHostFactory(ctx)
		modelHost = spawnedHost
		if spawnErr != nil {
			reason := fmt.Sprintf("spawn model host: %v", spawnErr)
			// A factory may have started a supervised host before reporting
			// failure. Core may close that host only after claiming it. If the
			// pointer is already claimed, it belongs to another operation and
			// must remain untouched.
			if spawnedHost != nil && spawnedHost.ClaimOperation() {
				hostOwned = true
				cleanupErr := closeHost()
				result.State.Isolation = spawnedHost.IsolationEvidence()
				if cleanupErr == nil && !result.State.Isolation.CleanupVerified {
					cleanupErr = errors.New("model host cleanup evidence was not verified")
				}
				if cleanupErr != nil {
					reason += "; model host cleanup: " + cleanupErr.Error()
				}
			}
			result.State.Status = "unavailable"
			result.State.Reason = reason
			if result.Fallback != nil {
				result.Fallback.Reason = result.State.Reason
			}
			return result
		}
		if modelHost == nil {
			result.State.Status = "unavailable"
			result.State.Reason = "spawn model host: factory returned no host"
			if result.Fallback != nil {
				result.Fallback.Reason = result.State.Reason
			}
			return result
		}
		// A concrete supervised host is operation-owned only after Core grants
		// its exclusive claim. A reused pointer fails closed and is deliberately
		// left untouched so the first operation can finish and close it.
		if !spawnedHost.ClaimOperation() {
			hostOwned = false
			result.State.Status = "unavailable"
			result.State.Reason = "model host is already claimed by another operation"
			if result.Fallback != nil {
				result.Fallback.Reason = result.State.Reason
			}
			return result
		}
		hostOwned = true
	}
	if modelHost == nil {
		result.State.Capability = protocol.ModelHostCapability{Status: "unavailable"}
		result.State.Status = "unavailable"
		result.State.Reason = "no configured model host"
		return result
	}
	attestation := protocol.AttestModelHost(modelHost)
	if requireCoreTrust && attestation == nil {
		result.State.Capability = protocol.ModelHostCapability{Status: "unavailable"}
		result.State.Status = "unavailable"
		result.State.Reason = "model host is not Core-supervised"
		return result
	}
	result.coreHost = modelHost
	result.coreTrust = attestation
	capability := modelHost.Capability()
	result.State.Capability = capability
	if !capability.IsMeasured() {
		result.State.Status = "unavailable"
		result.State.Reason = "model host capability is absent or unmeasured"
		return result
	}
	requestID := "enrichment-" + pack.EvidencePackID
	stepID := firstTargetStepID(pack.TargetStepIDs)
	target := pack.TargetSymbolPath
	promptRevision := firstNonEmpty(req.PromptRevision, pack.PromptRevision)
	hostRequest, err := protocol.NewModelHostRequest(requestID, stepID, target, promptRevision, pack.PackDigest, pack, capability.MaxResponseBytes)
	if err != nil {
		result.State.Status = "unavailable"
		result.State.Reason = err.Error()
		return result
	}
	hostRequest.PromptRevision = promptRevision
	if requestBytes, marshalErr := json.Marshal(hostRequest); marshalErr != nil {
		result.State.Status = "unavailable"
		result.State.Reason = marshalErr.Error()
		return result
	} else if validateErr := contractharness.Validate(ModelHostRequestV2SchemaID, requestBytes); validateErr != nil {
		result.State.Status = "unavailable"
		result.State.Reason = fmt.Sprintf("model host request schema: %v", validateErr)
		return result
	}
	attempts := req.MaxAttempts
	if attempts <= 0 || attempts > 2 {
		attempts = 2
	}
	finalizeHost := func(requireReceipt bool) error {
		if closeErr := closeHost(); closeErr != nil {
			return fmt.Errorf("model host cleanup: %w", closeErr)
		}
		result.State.Isolation = modelHost.IsolationEvidence()
		if !result.State.Isolation.CleanupVerified {
			return errors.New("model host cleanup evidence was not verified")
		}
		if requireReceipt {
			return protocol.ValidateModelHostIsolationEvidenceForRequest(result.State.Isolation, capability, hostRequest.RequestID, pack.PackDigest, true)
		}
		return protocol.ValidateModelHostIsolationEvidenceForLifecycle(result.State.Isolation, capability, pack.PackDigest, true)
	}
	markSourceIntegrityViolation := func(err error) {
		result.Proposal = nil
		result.View = nil
		result.State.Status = "unavailable"
		result.State.Reason = "source_integrity_violation: " + err.Error()
		// Invalid evidence is never re-emitted as if it were trusted. The
		// deterministic fallback remains available to the caller.
		result.State.Isolation = protocol.ModelHostIsolationEvidence{}
	}
	appendCleanupFailure := func(cleanupErr error) {
		if cleanupErr == nil {
			return
		}
		if result.State.Reason == "" {
			result.State.Reason = "model host cleanup: " + cleanupErr.Error()
		} else {
			result.State.Reason += "; model host cleanup: " + cleanupErr.Error()
		}
	}
	retryInvalid := func(reason string, attempt int) bool {
		result.State.Reason = reason
		if attempt+1 >= attempts {
			return true
		}
		corrected, correctionErr := correctedModelHostRequest(hostRequest, attempt+1, reason)
		if correctionErr != nil {
			result.State.Reason = correctionErr.Error()
			return false
		}
		hostRequest = corrected
		return true
	}
	for attempt := 0; attempt < attempts; attempt++ {
		response, callErr := modelHost.Enrich(ctx, hostRequest)
		result.State.Isolation = modelHost.IsolationEvidence()
		if callErr != nil && (errors.Is(callErr, protocol.ErrTimeout) || errors.Is(callErr, protocol.ErrCancelled)) {
			if isolationErr := finalizeHost(false); isolationErr != nil {
				markSourceIntegrityViolation(isolationErr)
				return result
			}
			result.State.Status, result.State.Reason = enrichmentStatusForError(callErr)
			return result
		}
		if isolationErr := protocol.ValidateModelHostIsolationEvidenceForRequest(result.State.Isolation, capability, hostRequest.RequestID, pack.PackDigest, false); isolationErr != nil {
			cleanupErr := closeHost()
			if cleanupErr == nil && !modelHost.IsolationEvidence().CleanupVerified {
				cleanupErr = errors.New("model host cleanup evidence was not verified")
			}
			markSourceIntegrityViolation(isolationErr)
			appendCleanupFailure(cleanupErr)
			return result
		}
		if callErr != nil {
			if isolationErr := finalizeHost(true); isolationErr != nil {
				markSourceIntegrityViolation(isolationErr)
				return result
			}
			result.State.Status, result.State.Reason = enrichmentStatusForError(callErr)
			return result
		}
		if response.SchemaID != ModelHostResponseV2SchemaID || response.SchemaVersion != 2 || (response.Status != "accepted" && response.Status != "ok") {
			if !retryInvalid("model host response schema or status is invalid", attempt) {
				if isolationErr := finalizeHost(true); isolationErr != nil {
					markSourceIntegrityViolation(isolationErr)
					return result
				}
				return result
			}
			continue
		}
		responseBytes, marshalErr := json.Marshal(response)
		if marshalErr != nil {
			if !retryInvalid(fmt.Sprintf("marshal model host response: %v", marshalErr), attempt) {
				if isolationErr := finalizeHost(true); isolationErr != nil {
					markSourceIntegrityViolation(isolationErr)
					return result
				}
				return result
			}
			continue
		}
		if validateErr := contractharness.Validate(ModelHostResponseV2SchemaID, responseBytes); validateErr != nil {
			if !retryInvalid(fmt.Sprintf("model host response schema: %v", validateErr), attempt) {
				if isolationErr := finalizeHost(true); isolationErr != nil {
					markSourceIntegrityViolation(isolationErr)
					return result
				}
				return result
			}
			continue
		}
		if response.RequestID != "" && response.RequestID != hostRequest.RequestID {
			if !retryInvalid("model host response request identity is invalid", attempt) {
				if isolationErr := finalizeHost(true); isolationErr != nil {
					markSourceIntegrityViolation(isolationErr)
					return result
				}
				return result
			}
			continue
		}
		var proposal ModelProposal
		if err := decodeStrictJSON(response.Proposal, &proposal); err != nil {
			if !retryInvalid(fmt.Sprintf("decode model proposal: %v", err), attempt) {
				if isolationErr := finalizeHost(true); isolationErr != nil {
					markSourceIntegrityViolation(isolationErr)
					return result
				}
				return result
			}
			continue
		}
		if err := ValidateModelProposalV2(&proposal, ProposalValidationContext{
			Map: req.EvidencePack.Map, Pack: pack, TargetStepIDs: pack.TargetStepIDs, TargetSymbolPath: pack.TargetSymbolPath,
			ExpectedPackDigest: pack.PackDigest, ExpectedModelID: capability.ModelID, ExpectedModelRevision: capability.Revision,
			ExpectedPromptRevision: promptRevision, ExpectedSchemaProfile: SemanticProposalSchemaProfile,
		}); err != nil {
			if !retryInvalid(err.Error(), attempt) {
				if isolationErr := finalizeHost(true); isolationErr != nil {
					markSourceIntegrityViolation(isolationErr)
					return result
				}
				return result
			}
			continue
		}
		if isolationErr := finalizeHost(true); isolationErr != nil {
			markSourceIntegrityViolation(isolationErr)
			return result
		}
		result.Proposal = &proposal
		result.State.Status = "available"
		result.State.ProposalID = proposal.ProposalID
		result.State.Reason = "model proposal validated"
		result.Fallback = nil
		result.State.Fallback = nil
		view, viewErr := AcceptSemanticProposal(req.EvidencePack.Map, &proposal, pack)
		if viewErr != nil {
			result.Proposal = nil
			result.State.Status = "unavailable"
			result.State.Reason = viewErr.Error()
			return result
		}
		result.View = view
		contentSeal, sealErr := sealEnrichmentContent(result.Proposal, result.Pack)
		if sealErr != nil {
			result.Proposal = nil
			result.View = nil
			result.State.Status = "unavailable"
			result.State.Reason = sealErr.Error()
			return result
		}
		result.contentSeal = contentSeal
		return result
	}
	if isolationErr := finalizeHost(true); isolationErr != nil {
		markSourceIntegrityViolation(isolationErr)
		return result
	}
	result.State.Status = "unavailable"
	if result.State.Reason == "" {
		result.State.Reason = "model proposal rejected after bounded retry"
	}
	return result
}

func correctedModelHostRequest(request protocol.ModelHostRequest, attempt int, reason string) (protocol.ModelHostRequest, error) {
	if attempt != 1 {
		return protocol.ModelHostRequest{}, errors.New("model host correction attempt is outside the bounded retry")
	}
	cleanReason := secret.Redact(reason)
	if cleanReason.Count > 0 || strings.ContainsAny(reason, "/\\") {
		cleanReason.Text = "response failed declared schema or provenance validation"
	}
	if strings.TrimSpace(cleanReason.Text) == "" {
		cleanReason.Text = "response failed declared schema or provenance validation"
	}
	if len([]byte(cleanReason.Text)) > 512 {
		cleanReason.Text = string([]byte(cleanReason.Text)[:512])
	}
	corrected := request
	corrected.RequestID = request.RequestID + "-correction-1"
	corrected.CorrectionAttempt = attempt
	corrected.CorrectionReason = cleanReason.Text
	corrected.CorrectionFor = request.RequestID
	data, err := json.Marshal(corrected)
	if err != nil {
		return protocol.ModelHostRequest{}, fmt.Errorf("model host correction request: %w", err)
	}
	if err := contractharness.Validate(ModelHostRequestV2SchemaID, data); err != nil {
		return protocol.ModelHostRequest{}, fmt.Errorf("model host correction request schema: %w", err)
	}
	return corrected, nil
}

func decodeStrictJSON(data []byte, target any) error {
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return err
	}
	return errors.New("multiple JSON values")
}

func enrichmentContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

func firstTargetStepID(ids []string) string {
	if len(ids) == 0 {
		return ""
	}
	return ids[0]
}

func proposalPromptRevision(proposal *ModelProposal) string {
	if proposal == nil {
		return ""
	}
	return proposal.PromptRevision
}

func requestedTargetStepID(request EvidencePackRequest) string {
	if target := firstTargetStepID(request.TargetStepIDs); target != "" {
		return target
	}
	for _, step := range request.Map.Steps {
		if request.TargetSymbolPath != "" && (step.Name == request.TargetSymbolPath || step.TechnicalName == request.TargetSymbolPath || step.StructuralIdentity == request.TargetSymbolPath) {
			return step.StepID
		}
	}
	return ""
}

func enrichmentStatusForError(err error) (string, string) {
	var protocolErr *protocol.Error
	if errors.As(err, &protocolErr) {
		switch protocolErr.Code {
		case protocol.ETimeout:
			return "timed_out", protocolErr.Error()
		case protocol.ECancelled:
			return "unavailable", protocolErr.Error()
		case protocol.ECrashed:
			return "unavailable", protocolErr.Error()
		}
	}
	return "unavailable", err.Error()
}

// ApplyEnrichmentState updates only the enrichment field of a map copy. All
// deterministic Q3 and coverage fields remain byte-for-byte unchanged.
func ApplyEnrichmentState(mapIR *SemanticMapIR, state EnrichmentState) (*SemanticMapIR, error) {
	if mapIR == nil {
		return nil, errors.New("enrichment state: map is required")
	}
	switch state.Status {
	case "not_requested", "pending", "available", "timed_out", "unavailable":
	default:
		return nil, fmt.Errorf("enrichment state: unsupported status %q", state.Status)
	}
	clone := cloneSemanticMap(mapIR)
	clone.EnrichmentStatus = state.Status
	return clone, nil
}

// ModelArtifact contains identity and runtime details shown before activation.
type ModelArtifact struct {
	ModelID      string   `json:"modelId"`
	Revision     string   `json:"revision"`
	License      string   `json:"license"`
	Checksum     string   `json:"checksum"`
	Runtime      string   `json:"runtime"`
	DataBoundary string   `json:"dataBoundary"`
	Capabilities []string `json:"capabilities,omitempty"`
}

type ModelCapabilityState struct {
	Status       string   `json:"status"`
	ModelID      string   `json:"modelId,omitempty"`
	Revision     string   `json:"revision,omitempty"`
	Capabilities []string `json:"capabilities,omitempty"`
}

type ModelActivationDisclosure struct {
	SchemaID             string   `json:"schemaId"`
	SchemaVersion        int      `json:"schemaVersion"`
	DisclosureID         string   `json:"disclosureId"`
	ModelID              string   `json:"modelId"`
	Revision             string   `json:"revision"`
	License              string   `json:"license"`
	Checksum             string   `json:"checksum"`
	Runtime              string   `json:"runtime"`
	DataBoundary         string   `json:"dataBoundary"`
	CurrentCapabilities  []string `json:"currentCapabilities"`
	ProposedCapabilities []string `json:"proposedCapabilities"`
	CapabilityChange     []string `json:"capabilityChange"`
	ChoiceRequired       bool     `json:"choiceRequired"`
	Choice               string   `json:"choice,omitempty"`
	Approved             bool     `json:"approved"`
}

func NewModelActivationDisclosure(artifact ModelArtifact, current ModelCapabilityState) (*ModelActivationDisclosure, error) {
	if artifact.ModelID == "" || artifact.Revision == "" || artifact.License == "" || artifact.Checksum == "" || artifact.Runtime == "" || artifact.DataBoundary == "" {
		return nil, errors.New("model activation disclosure: model identity, license, checksum, runtime, and data boundary are required")
	}
	currentCaps := uniqueSortedStrings(current.Capabilities)
	proposedCaps := uniqueSortedStrings(artifact.Capabilities)
	change := capabilityChanges(currentCaps, proposedCaps)
	hash := sha256.Sum256([]byte(artifact.ModelID + "\x00" + artifact.Revision + "\x00" + artifact.Checksum))
	return &ModelActivationDisclosure{
		SchemaID: ModelActivationDisclosureV1SchemaID, SchemaVersion: 1,
		DisclosureID: "disclosure-" + hex.EncodeToString(hash[:])[:24], ModelID: artifact.ModelID,
		Revision: artifact.Revision, License: artifact.License, Checksum: artifact.Checksum,
		Runtime: artifact.Runtime, DataBoundary: artifact.DataBoundary,
		CurrentCapabilities: currentCaps, ProposedCapabilities: proposedCaps, CapabilityChange: change,
		ChoiceRequired: true, Approved: false,
	}, nil
}

func BuildModelActivationDisclosure(artifact ModelArtifact, current ModelCapabilityState) (*ModelActivationDisclosure, error) {
	return NewModelActivationDisclosure(artifact, current)
}

func ValidateModelActivationDisclosure(disclosure *ModelActivationDisclosure) error {
	if disclosure == nil || disclosure.SchemaID != ModelActivationDisclosureV1SchemaID || disclosure.SchemaVersion != 1 || disclosure.DisclosureID == "" || disclosure.ModelID == "" || disclosure.Revision == "" || disclosure.License == "" || disclosure.Checksum == "" || disclosure.Runtime == "" || disclosure.DataBoundary == "" {
		return errors.New("model activation disclosure: incomplete or unsupported disclosure")
	}
	if !disclosure.ChoiceRequired || (disclosure.Choice != "" && disclosure.Choice != "activate" && disclosure.Choice != "decline") {
		return errors.New("model activation disclosure: explicit choice is required")
	}
	if disclosure.Choice == "" && disclosure.Approved {
		return errors.New("model activation disclosure: approval requires an explicit choice")
	}
	if disclosure.Choice == "activate" && !disclosure.Approved {
		return errors.New("model activation disclosure: activation choice is not approved")
	}
	if disclosure.Choice == "decline" && disclosure.Approved {
		return errors.New("model activation disclosure: declined activation cannot be approved")
	}
	return nil
}

func ResolveModelActivation(disclosure *ModelActivationDisclosure, choice string) (*ModelActivationDisclosure, error) {
	if disclosure == nil {
		return nil, errors.New("model activation disclosure: disclosure is required")
	}
	copy := *disclosure
	if choice != "activate" && choice != "decline" {
		return nil, errors.New("model activation disclosure: choice must be activate or decline")
	}
	copy.Choice = choice
	copy.Approved = choice == "activate"
	if err := ValidateModelActivationDisclosure(&copy); err != nil {
		return nil, err
	}
	return &copy, nil
}

func capabilityChanges(current, proposed []string) []string {
	currentSet := make(map[string]struct{}, len(current))
	proposedSet := make(map[string]struct{}, len(proposed))
	for _, value := range current {
		currentSet[value] = struct{}{}
	}
	for _, value := range proposed {
		proposedSet[value] = struct{}{}
	}
	changes := make([]string, 0)
	for value := range proposedSet {
		if _, ok := currentSet[value]; !ok {
			changes = append(changes, "added:"+value)
		}
	}
	for value := range currentSet {
		if _, ok := proposedSet[value]; !ok {
			changes = append(changes, "removed:"+value)
		}
	}
	sort.Strings(changes)
	return changes
}
