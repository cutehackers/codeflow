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
	"strings"
	"sync"
	"time"

	"codeflow/internal/contractharness"
	"codeflow/internal/evidence"
)

var (
	// ErrCASConflict is returned when active pointer compare-and-swap fails due to
	// liveHead or previous generation mismatch (Raw §6.9, §10.11, INV-10).
	ErrCASConflict       = errors.New("active pointer CAS conflict: expected liveHead or previous generation mismatch")
	ErrIncompatibleEpoch = errors.New("incompatible workspace epoch")

	casLock sync.Mutex
)

// GenerationProofManifest represents the canonical proof manifest for a published generation.
type GenerationProofManifest struct {
	SchemaID        string `json:"schemaId"`
	SchemaVersion   int    `json:"schemaVersion"`
	ProofID         string `json:"proofId"`
	GenerationID    string `json:"generationId"`
	ComputedBasisID string `json:"computedBasisId"`
	// ComputedSnapshotID is the immutable snapshot used to build the map and
	// basis. ValidatedAgainstSnapshotID is the later workspace head checked by
	// the publication gate, so the two identities are intentionally distinct.
	ComputedSnapshotID             string                   `json:"computedSnapshotId"`
	ValidatedAgainstSnapshotID     string                   `json:"validatedAgainstSnapshotId"`
	ValidatedWorkspaceDeltaID      *string                  `json:"validatedWorkspaceDeltaId,omitempty"`
	TaskIntentRevision             int                      `json:"taskIntentRevision"`
	NormalizedQueryHash            string                   `json:"normalizedQueryHash"`
	AnalysisReadSetID              string                   `json:"analysisReadSetId"`
	CausalObservationClosureID     string                   `json:"causalObservationClosureId"`
	CausalObservationClosureDigest string                   `json:"causalObservationClosureDigest,omitempty"`
	CapabilityProfileDigest        string                   `json:"capabilityProfileDigest,omitempty"`
	WorkspaceEpoch                 int64                    `json:"workspaceEpoch"`
	CurrentPublication             CurrentPublicationResult `json:"currentPublication"`
	SettlementEvaluation           SettlementEvaluation     `json:"settlementEvaluation"`
	ArtifactRefs                   ArtifactRefs             `json:"artifactRefs"`
	ExpectedLiveHeadSnapshotID     string                   `json:"expectedLiveHeadSnapshotId"`
	ExpectedPreviousGenerationID   *string                  `json:"expectedPreviousGenerationId"`
	PublishedAt                    time.Time                `json:"publishedAt"`
}

// CurrentPublicationResult captures the 6 subgate outcomes for Current Publication Gate.
type CurrentPublicationResult struct {
	Eligibility           string `json:"eligibility"`           // passed | rejected
	SnapshotGate          string `json:"snapshotGate"`          // passed | failed
	ClosureGate           string `json:"closureGate"`           // passed | failed
	EvidenceGate          string `json:"evidenceGate"`          // passed | failed
	SemanticAtomicityGate string `json:"semanticAtomicityGate"` // passed | failed
	TaskRelevanceGate     string `json:"taskRelevanceGate"`     // passed | failed
	ComprehensionGate     string `json:"comprehensionGate"`     // passed | failed
}

// SettlementEvaluation captures settlement status.
type SettlementEvaluation struct {
	Gate                   string     `json:"gate"` // pending | passed | failed
	EvaluatedAt            *time.Time `json:"evaluatedAt,omitempty"`
	BlockingObligationRefs []string   `json:"blockingObligationRefs"`
}

// ArtifactRefs holds content-addressed references for canonical artifacts.
type ArtifactRefs struct {
	SemanticMap        string `json:"semanticMap"`
	SemanticDelta      string `json:"semanticDelta,omitempty"`
	EvidenceIndex      string `json:"evidenceIndex,omitempty"`
	Projection         string `json:"projection,omitempty"`
	AnalysisReadSet    string `json:"analysisReadSet,omitempty"`
	ObservationClosure string `json:"observationClosure,omitempty"`
	AnalyzerResult     string `json:"analyzerResult,omitempty"`
	LiveView           string `json:"liveView,omitempty"`
}

// ActivePointer represents the atomic active pointer with CAS fields (Raw §10.11).
type ActivePointer struct {
	SchemaID                     string    `json:"schemaId"`
	SchemaVersion                int       `json:"schemaVersion"`
	GenerationID                 string    `json:"generationId"`
	ManifestObjectRef            string    `json:"manifestObjectRef"`
	PublishedAt                  time.Time `json:"publishedAt"`
	ComputedBasisID              string    `json:"computedBasisId"`
	ValidatedAgainstSnapshotID   string    `json:"validatedAgainstSnapshotId"`
	ExpectedLiveHeadSnapshotID   string    `json:"expectedLiveHeadSnapshotId"`
	ExpectedPreviousGenerationID *string   `json:"expectedPreviousGenerationId"`
	WorkspaceEpoch               int64     `json:"workspaceEpoch"`
	TaskIntentRevision           int       `json:"taskIntentRevision"`
	NormalizedQueryHash          string    `json:"normalizedQueryHash"`
	FlowCount                    int       `json:"flowCount"`
	// These fields bind the selected proof to one repository/worktree/task
	// scope. They are intentionally part of the pointer, not caller-only
	// metadata, so a query for another task cannot inherit current authority.
	RepositoryID string `json:"repositoryId"`
	WorktreeID   string `json:"worktreeId"`
	TaskID       string `json:"taskId"`
}

// ValidatedActiveApprovalIdentity is the immutable identity projection that
// an approval transaction may use for currentness checks. It is constructed
// only from a pointer, proof manifest, and semantic-map artifact that have
// passed the complete validated active-proof reader.
type ValidatedActiveApprovalIdentity struct {
	ComputedBasisID            string
	GenerationID               string
	ValidatedSnapshotID        string
	ExpectedLiveHeadSnapshotID string
	MapID                      string
	TaskID                     string
	IntentRevision             int64
	WorkspaceEpoch             int64
}

type semanticMapArtifactIdentity struct {
	SchemaID                   string `json:"schemaId"`
	SchemaVersion              int    `json:"schemaVersion"`
	MapID                      string `json:"mapId"`
	GenerationID               string `json:"generationId"`
	ComputedBasisID            string `json:"computedBasisId"`
	ValidatedAgainstSnapshotID string `json:"validatedAgainstSnapshotId"`
	Basis                      struct {
		RepositoryID                string `json:"repositoryId"`
		WorktreeID                  string `json:"worktreeId"`
		ComputedWorkspaceSnapshotID string `json:"computedWorkspaceSnapshotId"`
		ComputedBasisID             string `json:"computedBasisId"`
		WorkspaceEpoch              int64  `json:"workspaceEpoch"`
		AnalysisReadSetID           string `json:"analysisReadSetId"`
		CausalObservationClosureID  string `json:"causalObservationClosureId"`
	} `json:"basis"`
	Task struct {
		TaskID         string `json:"taskId"`
		IntentRevision int    `json:"intentRevision"`
	} `json:"task"`
}

func validateSemanticMapArtifactIdentity(artifact []byte, manifest *GenerationProofManifest) error {
	if manifest == nil {
		return fmt.Errorf("publication manifest is required for semantic-map validation")
	}
	var doc semanticMapArtifactIdentity
	if err := json.Unmarshal(artifact, &doc); err != nil {
		return fmt.Errorf("decode active semantic-map artifact: %w", err)
	}
	if doc.SchemaID != "https://codeflow.local/schemas/rflsc.semantic-map-ir.v2.schema.json" || doc.SchemaVersion != 2 {
		return fmt.Errorf("semantic-map artifact schema identity is not canonical")
	}
	if strings.TrimSpace(doc.MapID) == "" || doc.GenerationID != manifest.GenerationID || doc.ComputedBasisID != manifest.ComputedBasisID {
		return fmt.Errorf("semantic-map artifact generation or basis identity does not match proof manifest")
	}
	if manifest.ComputedSnapshotID == "" || doc.ValidatedAgainstSnapshotID != manifest.ComputedSnapshotID || doc.Basis.ComputedWorkspaceSnapshotID != manifest.ComputedSnapshotID {
		return fmt.Errorf("semantic-map artifact computed snapshot identity does not match proof manifest")
	}
	if doc.Basis.ComputedBasisID != manifest.ComputedBasisID || doc.Basis.WorkspaceEpoch != manifest.WorkspaceEpoch || doc.Basis.AnalysisReadSetID != manifest.AnalysisReadSetID || doc.Basis.CausalObservationClosureID != manifest.CausalObservationClosureID || doc.Task.IntentRevision != manifest.TaskIntentRevision {
		return fmt.Errorf("semantic-map artifact causal identity does not match proof manifest")
	}
	return nil
}

func validatePersistedSemanticMapApprovalIdentity(doc persistedSemanticMapIdentity, manifest *GenerationProofManifest, pointer *ActivePointer) error {
	if manifest == nil || pointer == nil {
		return fmt.Errorf("active approval identity proof is incomplete")
	}
	if strings.TrimSpace(doc.MapID) == "" || strings.TrimSpace(doc.GenerationID) == "" || strings.TrimSpace(doc.ComputedBasisID) == "" || strings.TrimSpace(doc.ValidatedAgainstSnapshotID) == "" {
		return fmt.Errorf("active approval identity semantic-map identity is incomplete")
	}
	if manifest.GenerationID == "" || manifest.ComputedBasisID == "" || manifest.ComputedSnapshotID == "" || manifest.TaskIntentRevision < 1 || manifest.WorkspaceEpoch < 0 {
		return fmt.Errorf("active approval identity proof identity is incomplete")
	}
	if pointer.GenerationID == "" || pointer.ComputedBasisID == "" || pointer.ValidatedAgainstSnapshotID == "" || pointer.RepositoryID == "" || pointer.WorktreeID == "" || pointer.TaskID == "" || pointer.TaskIntentRevision < 1 || pointer.WorkspaceEpoch < 0 {
		return fmt.Errorf("active approval identity pointer identity is incomplete")
	}
	if doc.GenerationID != manifest.GenerationID || doc.GenerationID != pointer.GenerationID || doc.ComputedBasisID != manifest.ComputedBasisID || doc.ComputedBasisID != pointer.ComputedBasisID {
		return fmt.Errorf("active approval identity generation or basis mismatch")
	}
	// The map is built from ComputedSnapshotID. The pointer and manifest also
	// retain the later validated/live snapshot, which is intentionally a
	// separate identity in the publication contract.
	if doc.ValidatedAgainstSnapshotID != manifest.ComputedSnapshotID || doc.Basis.ComputedWorkspaceSnapshotID != manifest.ComputedSnapshotID {
		return fmt.Errorf("active approval identity computed snapshot mismatch")
	}
	if doc.Basis.ComputedBasisID != manifest.ComputedBasisID || doc.Basis.WorkspaceEpoch != manifest.WorkspaceEpoch || doc.Basis.WorkspaceEpoch != pointer.WorkspaceEpoch || doc.Basis.AnalysisReadSetID != manifest.AnalysisReadSetID || doc.Basis.CausalObservationClosureID != manifest.CausalObservationClosureID {
		return fmt.Errorf("active approval identity causal basis mismatch")
	}
	if doc.Basis.RepositoryID == "" || doc.Basis.WorktreeID == "" || doc.Task.TaskID == "" || doc.Basis.RepositoryID != pointer.RepositoryID || doc.Basis.WorktreeID != pointer.WorktreeID || doc.Task.TaskID != pointer.TaskID {
		return fmt.Errorf("active approval identity workspace scope mismatch")
	}
	if doc.Task.IntentRevision < 1 || int64(doc.Task.IntentRevision) != int64(manifest.TaskIntentRevision) || int64(doc.Task.IntentRevision) != int64(pointer.TaskIntentRevision) {
		return fmt.Errorf("active approval identity intent revision mismatch")
	}
	return nil
}

// persistedSemanticMapIdentity contains the portions of the canonical map
// needed by the restart-time proof reader. Keeping this small, storage-local
// projection avoids importing semantic (which depends on storage) while still
// allowing the reader to verify the identity graph and settlement state.
type persistedSemanticMapIdentity struct {
	SchemaID                   string `json:"schemaId"`
	SchemaVersion              int    `json:"schemaVersion"`
	MapID                      string `json:"mapId"`
	GenerationID               string `json:"generationId"`
	ComputedBasisID            string `json:"computedBasisId"`
	ValidatedAgainstSnapshotID string `json:"validatedAgainstSnapshotId"`
	Settlement                 string `json:"settlement"`
	Basis                      struct {
		RepositoryID                string `json:"repositoryId"`
		WorktreeID                  string `json:"worktreeId"`
		WorkspaceEpoch              int64  `json:"workspaceEpoch"`
		ComputedWorkspaceSnapshotID string `json:"computedWorkspaceSnapshotId"`
		ComputedBasisID             string `json:"computedBasisId"`
		SnapshotTreeID              string `json:"snapshotTreeId"`
		DependencyFingerprint       string `json:"dependencyFingerprint"`
		AnalysisReadSetID           string `json:"analysisReadSetId"`
		CausalObservationClosureID  string `json:"causalObservationClosureId"`
	} `json:"basis"`
	Task struct {
		TaskID         string `json:"taskId"`
		IntentRevision int    `json:"intentRevision"`
	} `json:"task"`
	Quality struct {
		Stage                    string `json:"stage"`
		UnresolvedCriticalCount  int    `json:"unresolvedCriticalCount"`
		ConflictingCriticalCount int    `json:"conflictingCriticalCount"`
		CriticalObligations      []struct {
			ObligationID string `json:"obligationId"`
			Required     bool   `json:"required"`
			Status       string `json:"status"`
		} `json:"criticalObligations"`
	} `json:"quality"`
	Coverage struct {
		IncludedSourceRoots []string `json:"includedSourceRoots"`
		ExcludedReasons     []string `json:"excludedReasons"`
	} `json:"coverage"`
	Steps []struct {
		StepID string `json:"stepId"`
	} `json:"steps"`
	BoundaryTargets []string `json:"boundaryTargets"`
}

type persistedProjectionIdentity struct {
	SchemaID            string   `json:"schemaId"`
	SchemaVersion       int      `json:"schemaVersion"`
	ProjectionID        string   `json:"projectionId"`
	GenerationID        string   `json:"generationId"`
	ComputedBasisID     string   `json:"computedBasisId"`
	VisibleStepRefs     []string `json:"visibleStepRefs"`
	PreservedStepRefs   []string `json:"preservedStepRefs"`
	UnknownBoundaryRefs []string `json:"unknownBoundaryRefs"`
	FoldedSubflows      []struct {
		FoldID          string `json:"foldId"`
		EntryStepRef    string `json:"entryStepRef"`
		ExitStepRef     string `json:"exitStepRef"`
		DrilldownTarget string `json:"drilldownTarget"`
	} `json:"foldedSubflows"`
}

func parseCASDigest(ref string) (string, error) {
	const prefix = "cas:sha256:"
	if !strings.HasPrefix(ref, prefix) {
		return "", fmt.Errorf("invalid content-addressed reference %q", ref)
	}
	digest := strings.TrimPrefix(ref, prefix)
	if len(digest) != sha256.Size*2 {
		return "", fmt.Errorf("invalid content-addressed reference %q", ref)
	}
	if _, err := hex.DecodeString(digest); err != nil {
		return "", fmt.Errorf("invalid content-addressed reference %q", ref)
	}
	return digest, nil
}

func (s *Storage) readManifestCASBytesUnlocked(casRef string) ([]byte, error) {
	digest, err := parseCASDigest(casRef)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(s.baseDir, "cas", digest+".json"))
	if err != nil {
		return nil, fmt.Errorf("read manifest cas %s: %w", casRef, err)
	}
	if ArtifactCASRef(data) != casRef {
		return nil, fmt.Errorf("manifest %s digest mismatch", casRef)
	}
	return data, nil
}

func (s *Storage) readArtifactCASUnlocked(ref string) ([]byte, error) {
	digest, err := parseCASDigest(ref)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(s.baseDir, "cas", digest+".artifact"))
	if err != nil {
		return nil, fmt.Errorf("read artifact %s: %w", ref, err)
	}
	if ArtifactCASRef(data) != ref {
		return nil, fmt.Errorf("artifact %s digest mismatch", ref)
	}
	return data, nil
}

func samePersistedObservations(left, right []evidence.Observation) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func samePersistedReadDocuments(left, right []evidence.ReadDocument) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func samePersistedStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func containsPersistedString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func measuredPersistedObservationNames(readSet evidence.AnalysisReadSet) []string {
	seen := make(map[string]bool)
	names := make([]string, 0, 3)
	appendMeasured := func(kind string, observations []evidence.Observation) {
		for _, observation := range observations {
			if !observation.Measured {
				continue
			}
			name := observation.Kind
			switch kind {
			case "negative_lookup":
				if name == "negative_lookup" {
					name = kind
				}
			case "membership":
				if name == "membership" || name == "source_membership" {
					name = kind
				}
			case "dependency_frontier":
				if name == "dependency_frontier" {
					name = kind
				}
			}
			if name != "" && !seen[name] {
				seen[name] = true
				names = append(names, name)
			}
		}
	}
	appendMeasured("negative_lookup", readSet.NegativeObservations)
	appendMeasured("membership", readSet.MembershipObservations)
	appendMeasured("dependency_frontier", readSet.DependencyFrontiers)
	return names
}

func canonicalCapabilityProfileDigest(profile evidence.CapabilityProfile) string {
	data, err := json.Marshal(profile)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func sameSettlementRefs(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func validatePersistedSettlement(mapDoc persistedSemanticMapIdentity, settlement SettlementEvaluation) error {
	if settlement.Gate != "pending" && settlement.Gate != "passed" && settlement.Gate != "failed" {
		return fmt.Errorf("active settlement gate is invalid: %q", settlement.Gate)
	}
	if mapDoc.Settlement != settlement.Gate {
		return fmt.Errorf("semantic-map settlement %q does not match proof settlement %q", mapDoc.Settlement, settlement.Gate)
	}
	if settlement.Gate == "passed" {
		if settlement.EvaluatedAt == nil || len(settlement.BlockingObligationRefs) != 0 {
			return fmt.Errorf("passed settlement lacks exact timestamp or has blocking obligations")
		}
		if mapDoc.Quality.UnresolvedCriticalCount != 0 || mapDoc.Quality.ConflictingCriticalCount != 0 {
			return fmt.Errorf("passed settlement has unresolved or conflicting critical counts")
		}
	}
	if settlement.Gate == "failed" {
		if settlement.EvaluatedAt == nil || len(settlement.BlockingObligationRefs) == 0 {
			return fmt.Errorf("failed settlement lacks timestamp or blocking obligations")
		}
	}
	blocking := make([]string, 0, len(mapDoc.Quality.CriticalObligations))
	for _, obligation := range mapDoc.Quality.CriticalObligations {
		if obligation.Required && obligation.Status != "verified" {
			blocking = append(blocking, obligation.ObligationID)
		}
	}
	if !sameSettlementRefs(blocking, settlement.BlockingObligationRefs) {
		return fmt.Errorf("semantic-map blocking obligations do not match proof settlement")
	}
	if settlement.Gate == "passed" {
		for _, obligation := range mapDoc.Quality.CriticalObligations {
			if obligation.Required && obligation.Status != "verified" {
				return fmt.Errorf("passed settlement contains unresolved required obligation %q", obligation.ObligationID)
			}
		}
	}
	return nil
}

func validatePersistedProjection(data []byte, manifest *GenerationProofManifest, mapDoc persistedSemanticMapIdentity) error {
	if err := contractharness.ValidateFlowViewProjection(data); err != nil {
		return fmt.Errorf("projection contract: %w", err)
	}
	var projection persistedProjectionIdentity
	if err := json.Unmarshal(data, &projection); err != nil {
		return fmt.Errorf("decode projection artifact: %w", err)
	}
	if projection.GenerationID != manifest.GenerationID || projection.ComputedBasisID != manifest.ComputedBasisID || projection.ProjectionID == "" {
		return fmt.Errorf("projection identity does not match active proof")
	}
	stepIDs := make(map[string]bool, len(mapDoc.Steps))
	for _, step := range mapDoc.Steps {
		if step.StepID == "" || stepIDs[step.StepID] {
			return fmt.Errorf("semantic-map step identity is missing or duplicated")
		}
		stepIDs[step.StepID] = true
	}
	for _, ref := range append(append([]string{}, projection.VisibleStepRefs...), projection.PreservedStepRefs...) {
		if !stepIDs[ref] {
			return fmt.Errorf("projection references unknown step %q", ref)
		}
	}
	for _, folded := range projection.FoldedSubflows {
		if folded.EntryStepRef != "" && !stepIDs[folded.EntryStepRef] {
			return fmt.Errorf("projection fold references unknown entry step %q", folded.EntryStepRef)
		}
		if folded.ExitStepRef != "" && !stepIDs[folded.ExitStepRef] {
			return fmt.Errorf("projection fold references unknown exit step %q", folded.ExitStepRef)
		}
	}
	for _, ref := range projection.UnknownBoundaryRefs {
		if !containsPersistedString(mapDoc.BoundaryTargets, ref) {
			return fmt.Errorf("projection references unknown boundary %q", ref)
		}
	}
	return nil
}

func validatePersistedAnalysisBundle(readSetData, closureData, resultData []byte, manifest *GenerationProofManifest, mapDoc persistedSemanticMapIdentity) error {
	if manifest == nil {
		return fmt.Errorf("active proof manifest is required")
	}
	var readSet evidence.AnalysisReadSet
	if err := contractharness.Validate(evidence.ReadSetSchemaID, readSetData); err != nil {
		return fmt.Errorf("analysis read-set contract: %w", err)
	}
	if err := json.Unmarshal(readSetData, &readSet); err != nil {
		return fmt.Errorf("decode analysis read-set artifact: %w", err)
	}
	if readSet.SchemaID != evidence.ReadSetSchemaID || readSet.SchemaVersion != evidence.SchemaVersion || readSet.ReadSetID != manifest.AnalysisReadSetID || readSet.ComputedBasisID != manifest.ComputedBasisID || readSet.WorkspaceEpoch != manifest.WorkspaceEpoch {
		return fmt.Errorf("analysis read-set identity does not match active proof")
	}
	for _, document := range readSet.Documents {
		if document.Path == "" || document.DocumentRevisionID == "" || document.ContentID == "" || document.ContentHash == "" || document.ContentID != document.ContentHash || document.DocumentVersion < 1 || document.ByteLength < 0 {
			return fmt.Errorf("analysis read-set contains an incomplete document identity")
		}
	}
	var closure evidence.ObservationClosure
	if err := contractharness.Validate(evidence.ClosureSchemaID, closureData); err != nil {
		return fmt.Errorf("observation closure contract: %w", err)
	}
	if err := json.Unmarshal(closureData, &closure); err != nil {
		return fmt.Errorf("decode observation closure artifact: %w", err)
	}
	if closure.SchemaID != evidence.ClosureSchemaID || closure.SchemaVersion != evidence.SchemaVersion || closure.ClosureID != manifest.CausalObservationClosureID || closure.AnalysisReadSetID != readSet.ReadSetID || closure.ComputedBasisID != manifest.ComputedBasisID || closure.WorkspaceEpoch != manifest.WorkspaceEpoch {
		return fmt.Errorf("observation closure identity does not match active proof")
	}
	if closure.ClosureDigest == "" || manifest.CausalObservationClosureDigest != closure.ClosureDigest {
		return fmt.Errorf("observation closure digest does not match active proof")
	}
	if closure.Status != "closed" {
		return fmt.Errorf("active proof requires a closed observation closure")
	}
	if len(closure.IncompleteReasons) != 0 {
		return fmt.Errorf("closed observation closure contains incomplete reasons")
	}
	if !samePersistedObservations(readSet.NegativeObservations, closure.NegativeObservations) || !samePersistedObservations(readSet.MembershipObservations, closure.MembershipObservations) || !samePersistedObservations(readSet.DependencyFrontiers, closure.DependencyFrontiers) {
		return fmt.Errorf("observation closure drifted from its analysis read-set")
	}
	measured := measuredPersistedObservationNames(readSet)
	if !samePersistedStrings(measured, closure.MeasuredObservations) {
		return fmt.Errorf("observation closure measured observations are not derived from read-set")
	}
	for _, required := range closure.RequiredObservations {
		if !containsPersistedString(closure.MeasuredObservations, required) {
			return fmt.Errorf("observation closure required observation %q was not measured", required)
		}
	}
	var result evidence.Result
	if err := contractharness.Validate(evidence.AnalyzerResultSchemaID, resultData); err != nil {
		return fmt.Errorf("analyzer result contract: %w", err)
	}
	if err := json.Unmarshal(resultData, &result); err != nil {
		return fmt.Errorf("decode analyzer result artifact: %w", err)
	}
	if result.SchemaID != evidence.AnalyzerResultSchemaID || result.SchemaVersion != evidence.SchemaVersion || result.RequestID == "" || result.AdapterVersion == "" || result.AnalyzerRevision == "" || result.WorkspaceEpoch != manifest.WorkspaceEpoch || result.ComputedBasisID != manifest.ComputedBasisID || result.SnapshotID != manifest.ComputedSnapshotID || result.SnapshotTreeDigest != mapDoc.Basis.SnapshotTreeID || result.DependencyFingerprint != mapDoc.Basis.DependencyFingerprint {
		return fmt.Errorf("analyzer result identity does not match active proof")
	}
	if result.Capability.Adapter == "" || result.Capability.AnalyzerRevision != result.AnalyzerRevision || len(result.Capability.Features) == 0 || canonicalCapabilityProfileDigest(result.Capability) == "" || canonicalCapabilityProfileDigest(result.Capability) != manifest.CapabilityProfileDigest {
		return fmt.Errorf("analyzer result capability profile does not match active proof")
	}
	for _, feature := range result.Capability.Features {
		if containsPersistedString(result.Capability.Unsupported, feature) {
			return fmt.Errorf("analyzer result capability marks %q both supported and unsupported", feature)
		}
	}
	if !samePersistedReadDocuments(readSet.Documents, result.ReadSet.Documents) || !samePersistedObservations(readSet.NegativeObservations, result.ReadSet.NegativeObservations) || !samePersistedObservations(readSet.MembershipObservations, result.ReadSet.MembershipObservations) || !samePersistedObservations(readSet.DependencyFrontiers, result.ReadSet.DependencyFrontiers) {
		return fmt.Errorf("analyzer result read-set drifted from persisted read-set")
	}
	if result.Closure.ClosureID != closure.ClosureID || result.Closure.AnalysisReadSetID != closure.AnalysisReadSetID || result.Closure.ComputedBasisID != closure.ComputedBasisID || result.Closure.WorkspaceEpoch != closure.WorkspaceEpoch || result.Closure.Status != closure.Status || result.Closure.ClosureDigest != closure.ClosureDigest || !samePersistedStrings(result.Closure.RequiredObservations, closure.RequiredObservations) || !samePersistedStrings(result.Closure.MeasuredObservations, closure.MeasuredObservations) || !samePersistedStrings(result.Closure.IncompleteReasons, closure.IncompleteReasons) || !samePersistedObservations(result.Closure.NegativeObservations, closure.NegativeObservations) || !samePersistedObservations(result.Closure.MembershipObservations, closure.MembershipObservations) || !samePersistedObservations(result.Closure.DependencyFrontiers, closure.DependencyFrontiers) {
		return fmt.Errorf("analyzer result closure drifted from persisted closure")
	}
	if !result.Coverage.Measured || len(result.Coverage.IncludedSourceRoots) == 0 {
		return fmt.Errorf("analyzer result coverage is not measured")
	}
	if !samePersistedStrings(result.Coverage.IncludedSourceRoots, mapDoc.Coverage.IncludedSourceRoots) || !samePersistedStrings(result.Coverage.ExcludedReasons, mapDoc.Coverage.ExcludedReasons) {
		return fmt.Errorf("analyzer result coverage drifted from semantic-map coverage")
	}
	return nil
}

func validateOptionalArtifactIdentity(name string, data []byte, manifest *GenerationProofManifest) error {
	if manifest == nil {
		return fmt.Errorf("active proof manifest is required for %s artifact", name)
	}
	if name == "semanticDelta" {
		return validateSemanticDeltaArtifactIdentity(data, manifest)
	}
	var identity struct {
		SchemaID                   string `json:"schemaId"`
		SchemaVersion              int    `json:"schemaVersion"`
		GenerationID               string `json:"generationId"`
		ComputedBasisID            string `json:"computedBasisId"`
		BaselineComputedBasisID    string `json:"baselineComputedBasisId"`
		CurrentComputedBasisID     string `json:"currentComputedBasisId"`
		CurrentValidatedSnapshotID string `json:"currentValidatedAgainstSnapshotId"`
		SnapshotID                 string `json:"snapshotId"`
	}
	if err := json.Unmarshal(data, &identity); err != nil {
		return fmt.Errorf("decode active %s artifact: %w", name, err)
	}
	if identity.GenerationID != "" && identity.GenerationID != manifest.GenerationID {
		return fmt.Errorf("active %s artifact generation identity does not match proof", name)
	}
	if identity.ComputedBasisID != "" && identity.ComputedBasisID != manifest.ComputedBasisID {
		return fmt.Errorf("active %s artifact basis identity does not match proof", name)
	}
	if identity.BaselineComputedBasisID != "" && identity.CurrentComputedBasisID != "" && (identity.BaselineComputedBasisID != manifest.ComputedBasisID || identity.CurrentComputedBasisID != manifest.ComputedBasisID) {
		return fmt.Errorf("active %s artifact basis identities do not match proof", name)
	}
	if identity.CurrentValidatedSnapshotID != "" && identity.CurrentValidatedSnapshotID != manifest.ValidatedAgainstSnapshotID {
		return fmt.Errorf("active %s artifact validated snapshot identity does not match proof", name)
	}
	if identity.SnapshotID != "" && identity.SnapshotID != manifest.ComputedSnapshotID {
		return fmt.Errorf("active %s artifact snapshot identity does not match proof", name)
	}
	return nil
}

func validateSemanticDeltaArtifactIdentity(data []byte, manifest *GenerationProofManifest) error {
	if manifest == nil {
		return fmt.Errorf("active proof manifest is required for semanticDelta artifact")
	}
	if err := contractharness.ValidateSemanticDeltaIR(data); err != nil {
		return fmt.Errorf("active semanticDelta artifact contract: %w", err)
	}
	var identity struct {
		SchemaID                   string `json:"schemaId"`
		SchemaVersion              int    `json:"schemaVersion"`
		BaselineComputedBasisID    string `json:"baselineComputedBasisId"`
		CurrentComputedBasisID     string `json:"currentComputedBasisId"`
		CurrentValidatedSnapshotID string `json:"currentValidatedAgainstSnapshotId"`
		FromGeneration             string `json:"fromGeneration"`
		ToGeneration               string `json:"toGeneration"`
		Status                     string `json:"status"`
	}
	if err := json.Unmarshal(data, &identity); err != nil {
		return fmt.Errorf("decode active semanticDelta artifact: %w", err)
	}
	if identity.SchemaID != "https://codeflow.local/schemas/rflsc.semantic-delta-ir.v2.schema.json" || identity.SchemaVersion != 2 {
		return fmt.Errorf("active semanticDelta artifact schema identity is not canonical")
	}
	if identity.Status != "comparable" {
		return fmt.Errorf("active semanticDelta artifact is not comparable")
	}
	if identity.FromGeneration == "" || identity.ToGeneration == "" || identity.ToGeneration != manifest.GenerationID {
		return fmt.Errorf("active semanticDelta artifact generation identity does not match proof")
	}
	if manifest.ExpectedPreviousGenerationID == nil || *manifest.ExpectedPreviousGenerationID == "" || identity.FromGeneration != *manifest.ExpectedPreviousGenerationID {
		return fmt.Errorf("active semanticDelta artifact predecessor generation does not match proof")
	}
	if identity.BaselineComputedBasisID == "" || identity.CurrentComputedBasisID != manifest.ComputedBasisID {
		return fmt.Errorf("active semanticDelta artifact basis identity does not match proof")
	}
	if identity.CurrentValidatedSnapshotID == "" || identity.CurrentValidatedSnapshotID != manifest.ComputedSnapshotID {
		return fmt.Errorf("active semanticDelta artifact computed snapshot identity does not match proof")
	}
	return nil
}

// ReadActivePointer loads active-pointer.json, or returns nil if none exists yet.
func (s *Storage) ReadActivePointer() (*ActivePointer, error) {
	casLock.Lock()
	defer casLock.Unlock()
	if err := s.recoverPublication(); err != nil {
		return nil, err
	}
	return s.readActivePointerUnlocked()
}

func (s *Storage) readActivePointerUnlocked() (*ActivePointer, error) {
	path := filepath.Join(s.baseDir, "active-pointer.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read active pointer: %w", err)
	}
	var raw struct {
		WorkspaceEpoch json.RawMessage `json:"workspaceEpoch"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("unmarshal active pointer: %w", err)
	}
	if len(raw.WorkspaceEpoch) > 0 && raw.WorkspaceEpoch[0] == '"' {
		ref, archiveErr := s.archiveIncompatible("active-pointer.json", data)
		if archiveErr != nil {
			return nil, archiveErr
		}
		if removeErr := os.Remove(path); removeErr != nil && !errors.Is(removeErr, fs.ErrNotExist) {
			return nil, fmt.Errorf("quarantine incompatible active pointer: %w", removeErr)
		}
		return nil, &IncompatibleEpochError{ArtifactPath: path, HistoricalRef: ref}
	}
	var p ActivePointer
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("unmarshal active pointer: %w", err)
	}
	return &p, nil
}

// IncompatibleEpochError identifies a preserved legacy payload rejected at
// the canonical integer boundary.
type IncompatibleEpochError struct {
	ArtifactPath  string
	HistoricalRef string
}

func (e *IncompatibleEpochError) Error() string {
	return fmt.Sprintf("%v: legacy string epoch artifact %s preserved as %s", ErrIncompatibleEpoch, e.ArtifactPath, e.HistoricalRef)
}

func (e *IncompatibleEpochError) Unwrap() error { return ErrIncompatibleEpoch }

func (s *Storage) archiveIncompatible(name string, data []byte) (string, error) {
	dir := filepath.Join(s.baseDir, "historical", "incompatible")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create incompatible history: %w", err)
	}
	name = strings.ReplaceAll(filepath.Base(name), string(filepath.Separator), "_")
	ref := filepath.Join(dir, fmt.Sprintf("%s-%d.json", name, time.Now().UTC().UnixNano()))
	if err := atomicWrite(ref, data); err != nil {
		return "", fmt.Errorf("preserve incompatible artifact: %w", err)
	}
	return ref, nil
}

// CompareAndSwapActivePointer atomically updates the active pointer if and only if
// expectedLiveHeadSnapshotID matches the current state and expectedPreviousGenerationID matches
// the current active generation ID (Raw §6.9, §10.11, INV-10).
func (s *Storage) CompareAndSwapActivePointer(expectedLiveHeadSnapshotID, expectedPreviousGenerationID string, newPointer *ActivePointer) error {
	casLock.Lock()
	defer casLock.Unlock()

	if newPointer == nil {
		return fmt.Errorf("new active pointer must not be nil")
	}
	if newPointer.WorkspaceEpoch < 0 {
		return fmt.Errorf("workspace epoch must be non-negative, got %d", newPointer.WorkspaceEpoch)
	}
	if err := s.recoverPublication(); err != nil {
		return err
	}
	current, err := s.readActivePointerUnlocked()
	if err != nil {
		return err
	}

	if current == nil {
		if expectedPreviousGenerationID != "" && expectedPreviousGenerationID != "*" {
			return ErrCASConflict
		}
	} else {
		if expectedPreviousGenerationID == "" {
			return ErrCASConflict
		}
		if expectedPreviousGenerationID != "*" && current.GenerationID != expectedPreviousGenerationID {
			return ErrCASConflict
		}
		if expectedLiveHeadSnapshotID != "" && current.ExpectedLiveHeadSnapshotID != "" && current.ExpectedLiveHeadSnapshotID != expectedLiveHeadSnapshotID {
			return ErrCASConflict
		}
	}

	if newPointer.PublishedAt.IsZero() {
		newPointer.PublishedAt = time.Now().UTC()
	}
	if newPointer.SchemaID == "" {
		newPointer.SchemaID = "https://codeflow.local/schemas/active-pointer.schema.json"
	}
	if newPointer.SchemaVersion == 0 {
		newPointer.SchemaVersion = 2
	}

	data, err := json.MarshalIndent(newPointer, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal active pointer: %w", err)
	}

	path := filepath.Join(s.baseDir, "active-pointer.json")
	if err := atomicWrite(path, data); err != nil {
		return fmt.Errorf("write active pointer: %w", err)
	}

	// Backward compatibility: also sync to legacy pointer.json
	ptr := Pointer{
		GenerationID:     newPointer.GenerationID,
		PublishedAt:      newPointer.PublishedAt,
		BasisFingerprint: newPointer.ComputedBasisID,
		FlowCount:        newPointer.FlowCount,
		WorkspaceEpoch:   newPointer.WorkspaceEpoch,
	}
	ptrData, _ := json.MarshalIndent(ptr, "", "  ")
	_ = atomicWrite(filepath.Join(s.baseDir, "pointer.json"), ptrData)

	return nil
}

// WriteManifestCAS writes a GenerationProofManifest to CAS storage and returns its reference.
func (s *Storage) WriteManifestCAS(manifest *GenerationProofManifest) (string, error) {
	if manifest == nil {
		return "", fmt.Errorf("manifest must not be nil")
	}
	if manifest.WorkspaceEpoch < 0 {
		return "", fmt.Errorf("workspace epoch must be non-negative, got %d", manifest.WorkspaceEpoch)
	}
	if manifest.PublishedAt.IsZero() {
		manifest.PublishedAt = time.Now().UTC()
	}
	if manifest.SchemaID == "" {
		manifest.SchemaID = "https://codeflow.local/schemas/generation-proof-manifest.schema.json"
	}
	if manifest.SchemaVersion == 0 {
		manifest.SchemaVersion = 1
	}

	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal manifest: %w", err)
	}

	h := sha256.Sum256(data)
	hashHex := hex.EncodeToString(h[:])

	casDir := filepath.Join(s.baseDir, "cas")
	if err := os.MkdirAll(casDir, 0o755); err != nil {
		return "", fmt.Errorf("create cas dir: %w", err)
	}

	targetPath := filepath.Join(casDir, hashHex+".json")
	if err := atomicWrite(targetPath, data); err != nil {
		return "", fmt.Errorf("write manifest cas: %w", err)
	}

	return "cas:sha256:" + hashHex, nil
}

// WriteArtifactCAS stores an immutable analysis artifact and returns its
// content-addressed reference. It is used for semantic maps and indexes before
// the publication transaction commits the manifest and active pointer.
func (s *Storage) WriteArtifactCAS(data []byte) (string, error) {
	if len(data) == 0 {
		return "", fmt.Errorf("artifact must not be empty")
	}
	ref := ArtifactCASRef(data)
	hashHex := strings.TrimPrefix(ref, "cas:sha256:")
	if err := atomicWrite(filepath.Join(s.baseDir, "cas", hashHex+".artifact"), data); err != nil {
		return "", fmt.Errorf("write artifact cas: %w", err)
	}
	return ref, nil
}

// ArtifactCASRef returns the immutable content address used by publication
// transactions for analysis artifacts.
func ArtifactCASRef(data []byte) string {
	h := sha256.Sum256(data)
	return "cas:sha256:" + hex.EncodeToString(h[:])
}

// ReadManifestCAS retrieves a GenerationProofManifest from CAS.
func (s *Storage) ReadManifestCAS(casRef string) (*GenerationProofManifest, error) {
	casLock.Lock()
	defer casLock.Unlock()
	if err := s.recoverPublication(); err != nil {
		return nil, err
	}
	return s.readManifestCASUnlocked(casRef)
}

func (s *Storage) readManifestCASUnlocked(casRef string) (*GenerationProofManifest, error) {
	hashHex := strings.TrimPrefix(casRef, "cas:sha256:")
	hashHex = strings.TrimPrefix(hashHex, "cas:")
	if len(hashHex) != 64 {
		return nil, fmt.Errorf("invalid manifest cas ref: %q", casRef)
	}
	if _, err := hex.DecodeString(hashHex); err != nil {
		return nil, fmt.Errorf("invalid manifest cas ref: %q", casRef)
	}

	targetPath := filepath.Join(s.baseDir, "cas", hashHex+".json")
	data, err := os.ReadFile(targetPath)
	if err != nil {
		return nil, fmt.Errorf("read manifest cas %s: %w", targetPath, err)
	}

	var m GenerationProofManifest
	var raw struct {
		WorkspaceEpoch json.RawMessage `json:"workspaceEpoch"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("unmarshal manifest CAS envelope: %w", err)
	}
	if len(raw.WorkspaceEpoch) > 0 && raw.WorkspaceEpoch[0] == '"' {
		ref, archiveErr := s.archiveIncompatible("manifest-"+hashHex, data)
		if archiveErr != nil {
			return nil, archiveErr
		}
		if removeErr := os.Remove(targetPath); removeErr != nil && !errors.Is(removeErr, fs.ErrNotExist) {
			return nil, fmt.Errorf("quarantine incompatible manifest: %w", removeErr)
		}
		return nil, &IncompatibleEpochError{ArtifactPath: targetPath, HistoricalRef: ref}
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("unmarshal manifest cas: %w", err)
	}
	if m.WorkspaceEpoch < 0 {
		return nil, fmt.Errorf("manifest CAS has negative workspaceEpoch")
	}
	return &m, nil
}

// RecoverPendingPublication rolls back an incomplete publication journal. It
// is called during server startup before event replay and is also safe for
// callers that want an explicit crash-recovery boundary.
func (s *Storage) RecoverPendingPublication() error {
	casLock.Lock()
	defer casLock.Unlock()
	return s.recoverPublication()
}

// ReadActiveProofManifest loads the GenerationProofManifest currently pointed to by active-pointer.json.
func (s *Storage) ReadActiveProofManifest() (*GenerationProofManifest, error) {
	ptr, err := s.ReadActivePointer()
	if err != nil {
		return nil, err
	}
	if ptr == nil || ptr.ManifestObjectRef == "" {
		return nil, nil
	}
	return s.ReadManifestCAS(ptr.ManifestObjectRef)
}

// ReadValidatedActiveProofManifest reads the only proof that is eligible to
// establish current authority. It validates the pointer/manifest identity
// chain and the content-addressed semantic-map artifact while holding the
// same publication lock used by writers. Legacy readers may continue to use
// ReadActiveProofManifest for historical inspection, but product current
// consumers must use this strict boundary.
func (s *Storage) ReadValidatedActiveProofManifest() (*GenerationProofManifest, *ActivePointer, error) {
	casLock.Lock()
	defer casLock.Unlock()
	if err := s.recoverPublication(); err != nil {
		return nil, nil, err
	}
	return s.readValidatedActiveProofManifestUnlocked()
}

func (s *Storage) readValidatedActiveProofManifestUnlocked() (*GenerationProofManifest, *ActivePointer, error) {
	ptr, err := s.readActivePointerUnlocked()
	if err != nil {
		return nil, nil, err
	}
	if ptr == nil {
		return nil, nil, nil
	}
	pointerData, err := os.ReadFile(filepath.Join(s.baseDir, "active-pointer.json"))
	if err != nil {
		return nil, ptr, fmt.Errorf("read active pointer bytes: %w", err)
	}
	if err := contractharness.ValidateActivePointerV2(pointerData); err != nil {
		return nil, ptr, fmt.Errorf("active pointer contract: %w", err)
	}
	const pointerSchema = "https://codeflow.local/schemas/rflsc.active-pointer.v2.schema.json"
	const manifestSchema = "https://codeflow.local/schemas/rflsc.generation-proof-manifest.v2.schema.json"
	if ptr.SchemaID != pointerSchema || ptr.SchemaVersion != 2 {
		return nil, ptr, fmt.Errorf("active pointer schema identity is not canonical")
	}
	if ptr.GenerationID == "" || ptr.ManifestObjectRef == "" || ptr.ComputedBasisID == "" || ptr.ValidatedAgainstSnapshotID == "" || ptr.ExpectedLiveHeadSnapshotID == "" || ptr.NormalizedQueryHash == "" || ptr.RepositoryID == "" || ptr.WorktreeID == "" || ptr.TaskID == "" {
		return nil, ptr, fmt.Errorf("active pointer identity is incomplete")
	}
	manifestData, err := s.readManifestCASBytesUnlocked(ptr.ManifestObjectRef)
	if err != nil {
		return nil, ptr, err
	}
	if err := contractharness.ValidateGenerationProofManifestV2(manifestData); err != nil {
		return nil, ptr, fmt.Errorf("generation proof manifest contract: %w", err)
	}
	var manifest GenerationProofManifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		return nil, ptr, fmt.Errorf("decode active proof manifest: %w", err)
	}
	if manifest.SchemaID != manifestSchema || manifest.SchemaVersion != 2 {
		return nil, ptr, fmt.Errorf("generation proof schema identity is not canonical")
	}
	if manifest.GenerationID != ptr.GenerationID || manifest.ComputedBasisID != ptr.ComputedBasisID || manifest.ValidatedAgainstSnapshotID != ptr.ValidatedAgainstSnapshotID || manifest.ExpectedLiveHeadSnapshotID != ptr.ExpectedLiveHeadSnapshotID || manifest.WorkspaceEpoch != ptr.WorkspaceEpoch || manifest.TaskIntentRevision != ptr.TaskIntentRevision || manifest.NormalizedQueryHash != ptr.NormalizedQueryHash {
		return nil, ptr, fmt.Errorf("active pointer and proof manifest identities do not match")
	}
	if manifest.ComputedSnapshotID == "" {
		return nil, ptr, fmt.Errorf("active proof is missing computed snapshot identity")
	}
	if (manifest.ExpectedPreviousGenerationID == nil) != (ptr.ExpectedPreviousGenerationID == nil) || manifest.ExpectedPreviousGenerationID != nil && *manifest.ExpectedPreviousGenerationID != *ptr.ExpectedPreviousGenerationID {
		return nil, ptr, fmt.Errorf("active pointer and proof predecessor identities do not match")
	}
	publication := manifest.CurrentPublication
	if publication.Eligibility != "passed" || publication.SnapshotGate != "passed" || publication.ClosureGate != "passed" || publication.EvidenceGate != "passed" || publication.SemanticAtomicityGate != "passed" || publication.TaskRelevanceGate != "passed" || publication.ComprehensionGate != "passed" {
		return nil, ptr, fmt.Errorf("active proof has not passed every current publication gate")
	}
	if manifest.AnalysisReadSetID == "" || manifest.CausalObservationClosureID == "" || manifest.CausalObservationClosureDigest == "" || manifest.CapabilityProfileDigest == "" || manifest.ArtifactRefs.SemanticMap == "" || manifest.ArtifactRefs.Projection == "" || manifest.ArtifactRefs.AnalysisReadSet == "" || manifest.ArtifactRefs.ObservationClosure == "" || manifest.ArtifactRefs.AnalyzerResult == "" {
		return nil, ptr, fmt.Errorf("active proof is missing closure or artifact identity")
	}
	artifactRef := manifest.ArtifactRefs.SemanticMap
	artifact, err := s.readArtifactCASUnlocked(artifactRef)
	if err != nil {
		return nil, ptr, fmt.Errorf("read active semantic-map artifact: %w", err)
	}
	if err := contractharness.ValidateSemanticMapIR(artifact); err != nil {
		return nil, ptr, fmt.Errorf("active semantic-map artifact contract: %w", err)
	}
	if err := validateSemanticMapArtifactIdentity(artifact, &manifest); err != nil {
		return nil, ptr, err
	}
	var mapDoc persistedSemanticMapIdentity
	if err := json.Unmarshal(artifact, &mapDoc); err != nil {
		return nil, ptr, fmt.Errorf("decode active semantic-map identity: %w", err)
	}
	if err := validatePersistedSemanticMapApprovalIdentity(mapDoc, &manifest, ptr); err != nil {
		return nil, ptr, err
	}
	if err := validatePersistedSettlement(mapDoc, manifest.SettlementEvaluation); err != nil {
		return nil, ptr, err
	}
	readSetData, err := s.readArtifactCASUnlocked(manifest.ArtifactRefs.AnalysisReadSet)
	if err != nil {
		return nil, ptr, err
	}
	closureData, err := s.readArtifactCASUnlocked(manifest.ArtifactRefs.ObservationClosure)
	if err != nil {
		return nil, ptr, err
	}
	resultData, err := s.readArtifactCASUnlocked(manifest.ArtifactRefs.AnalyzerResult)
	if err != nil {
		return nil, ptr, err
	}
	if err := validatePersistedAnalysisBundle(readSetData, closureData, resultData, &manifest, mapDoc); err != nil {
		return nil, ptr, err
	}
	projectionData, err := s.readArtifactCASUnlocked(manifest.ArtifactRefs.Projection)
	if err != nil {
		return nil, ptr, err
	}
	if err := validatePersistedProjection(projectionData, &manifest, mapDoc); err != nil {
		return nil, ptr, err
	}
	for name, ref := range map[string]string{
		"semanticDelta": manifest.ArtifactRefs.SemanticDelta,
		"evidenceIndex": manifest.ArtifactRefs.EvidenceIndex,
		"liveView":      manifest.ArtifactRefs.LiveView,
	} {
		if ref == "" {
			continue
		}
		data, err := s.readArtifactCASUnlocked(ref)
		if err != nil {
			return nil, ptr, fmt.Errorf("read active %s artifact: %w", name, err)
		}
		if err := validateOptionalArtifactIdentity(name, data, &manifest); err != nil {
			return nil, ptr, err
		}
	}
	return &manifest, ptr, nil
}

// WithValidatedActiveApprovalIdentity runs callback against the exact
// current proof identity while holding the publication CAS lock. This keeps
// an approval currentness check and a concurrent generation publication from
// observing different active proof generations.
func (s *Storage) WithValidatedActiveApprovalIdentity(callback func(ValidatedActiveApprovalIdentity) error) error {
	if s == nil {
		return errors.New("validated active approval identity storage is unavailable")
	}
	if callback == nil {
		return errors.New("validated active approval identity callback is required")
	}
	casLock.Lock()
	defer casLock.Unlock()
	if err := s.recoverPublication(); err != nil {
		return err
	}
	manifest, pointer, err := s.readValidatedActiveProofManifestUnlocked()
	if err != nil {
		return err
	}
	if manifest == nil || pointer == nil {
		return errors.New("validated active approval proof is unavailable")
	}
	mapBytes, err := s.readArtifactCASUnlocked(manifest.ArtifactRefs.SemanticMap)
	if err != nil {
		return fmt.Errorf("read validated approval semantic-map artifact: %w", err)
	}
	var mapDoc persistedSemanticMapIdentity
	if err := json.Unmarshal(mapBytes, &mapDoc); err != nil {
		return fmt.Errorf("decode validated approval semantic-map identity: %w", err)
	}
	if err := validatePersistedSemanticMapApprovalIdentity(mapDoc, manifest, pointer); err != nil {
		return err
	}
	identity := ValidatedActiveApprovalIdentity{
		ComputedBasisID:            mapDoc.ComputedBasisID,
		GenerationID:               mapDoc.GenerationID,
		ValidatedSnapshotID:        mapDoc.ValidatedAgainstSnapshotID,
		ExpectedLiveHeadSnapshotID: pointer.ExpectedLiveHeadSnapshotID,
		MapID:                      mapDoc.MapID,
		TaskID:                     mapDoc.Task.TaskID,
		IntentRevision:             int64(mapDoc.Task.IntentRevision),
		WorkspaceEpoch:             mapDoc.Basis.WorkspaceEpoch,
	}
	return callback(identity)
}

// ValidatedActiveProofBundle returns the exact immutable artifacts needed by
// read-only projections after the complete current proof has passed strict
// validation. The bundle is read under the publication lock, so a caller
// cannot combine a map from one active generation with a pointer from another.
type ValidatedActiveProofBundle struct {
	Manifest       *GenerationProofManifest
	ManifestBytes  []byte
	Pointer        *ActivePointer
	SemanticMap    []byte
	Projection     []byte
	LiveView       []byte
	AnalyzerResult []byte
	SemanticDelta  []byte
}

func (s *Storage) ReadValidatedActiveProofBundle() (*ValidatedActiveProofBundle, error) {
	casLock.Lock()
	defer casLock.Unlock()
	if err := s.recoverPublication(); err != nil {
		return nil, err
	}
	manifest, pointer, err := s.readValidatedActiveProofManifestUnlocked()
	if err != nil || manifest == nil || pointer == nil {
		return nil, err
	}
	manifestBytes, err := s.readManifestCASBytesUnlocked(pointer.ManifestObjectRef)
	if err != nil {
		return nil, fmt.Errorf("read validated proof manifest bytes: %w", err)
	}
	mapBytes, err := s.readArtifactCASUnlocked(manifest.ArtifactRefs.SemanticMap)
	if err != nil {
		return nil, fmt.Errorf("read validated semantic-map artifact: %w", err)
	}
	resultBytes, err := s.readArtifactCASUnlocked(manifest.ArtifactRefs.AnalyzerResult)
	if err != nil {
		return nil, fmt.Errorf("read validated analyzer-result artifact: %w", err)
	}
	projectionBytes, err := s.readArtifactCASUnlocked(manifest.ArtifactRefs.Projection)
	if err != nil {
		return nil, fmt.Errorf("read validated projection artifact: %w", err)
	}
	var liveViewBytes []byte
	if manifest.ArtifactRefs.LiveView != "" {
		liveViewBytes, err = s.readArtifactCASUnlocked(manifest.ArtifactRefs.LiveView)
		if err != nil {
			return nil, fmt.Errorf("read validated Live view artifact: %w", err)
		}
	}
	var deltaBytes []byte
	if manifest.ArtifactRefs.SemanticDelta != "" {
		deltaBytes, err = s.readArtifactCASUnlocked(manifest.ArtifactRefs.SemanticDelta)
		if err != nil {
			return nil, fmt.Errorf("read validated semantic-delta artifact: %w", err)
		}
	}
	return &ValidatedActiveProofBundle{
		Manifest:       manifest,
		ManifestBytes:  append([]byte(nil), manifestBytes...),
		Pointer:        pointer,
		SemanticMap:    append([]byte(nil), mapBytes...),
		Projection:     append([]byte(nil), projectionBytes...),
		LiveView:       append([]byte(nil), liveViewBytes...),
		AnalyzerResult: append([]byte(nil), resultBytes...),
		SemanticDelta:  append([]byte(nil), deltaBytes...),
	}, nil
}
