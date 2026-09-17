package semantic

import (
	"encoding/json"
	"strings"

	"codeflow/internal/collector/storage"
)

// PromoteRequirementAlignmentsWithCurrentProof upgrades only alignment
// candidates whose exact semantic-map artifact is covered by a validated
// current proof. Callers must obtain manifest and pointer through
// storage.ReadValidatedActiveProofManifest; this function independently
// cross-checks their identities and the required Evidence before granting the
// narrower alignment authority.
func PromoteRequirementAlignmentsWithCurrentProof(
	criteria []AcceptanceCriterion,
	currentMap *SemanticMapIR,
	manifest *storage.GenerationProofManifest,
	pointer *storage.ActivePointer,
	opts AlignmentOptions,
) []RequirementAlignment {
	alignments := ComputeRequirementAlignment(criteria, currentMap, opts)
	if !currentProofMatchesAlignmentMap(currentMap, manifest, pointer) {
		for i := range alignments {
			if alignments[i].Reason == "awaiting_current_proof" {
				alignments[i].Reason = "current_proof_mismatch"
				alignments[i].Notes = "현재 proof와 map identity가 일치하지 않아 confirmed로 승격하지 않음"
			}
		}
		return alignments
	}

	evidenceByID := make(map[string]SemanticEvidence, len(currentMap.Evidence))
	for _, evidence := range currentMap.Evidence {
		if evidence.EvidenceID != "" {
			evidenceByID[evidence.EvidenceID] = evidence
		}
	}
	criteriaByID := make(map[string]AcceptanceCriterion, len(criteria))
	for _, criterion := range criteria {
		criteriaByID[criterion.ID] = criterion
	}
	for i := range alignments {
		alignment := &alignments[i]
		criterion, exists := criteriaByID[alignment.CriterionID]
		if !exists || alignment.Status != "partial" || alignment.Reason != "awaiting_current_proof" {
			continue
		}
		if !alignmentEvidenceComplete(criterion, *alignment, evidenceByID, currentMap.ComputedBasisID, manifest.ComputedSnapshotID) {
			alignment.Reason = "current_evidence_incomplete"
			alignment.Notes = "현재 proof는 유효하지만 필수 Evidence가 누락·불일치·충돌하여 confirmed로 승격하지 않음"
			continue
		}
		alignment.Status = "confirmed"
		alignment.Authority = "current_proof"
		alignment.Reason = ""
		alignment.MissingEvidence = []string{}
		alignment.MissingTests = []string{}
		alignment.MissingContracts = []string{}
		alignment.MissingRuntime = []string{}
		alignment.Notes = "현재 generation proof와 필수 verified Evidence로 확인됨"
	}
	return alignments
}

func currentProofMatchesAlignmentMap(currentMap *SemanticMapIR, manifest *storage.GenerationProofManifest, pointer *storage.ActivePointer) bool {
	if currentMap == nil || manifest == nil || pointer == nil {
		return false
	}
	publication := manifest.CurrentPublication
	if publication.Eligibility != "passed" || publication.SnapshotGate != "passed" || publication.ClosureGate != "passed" || publication.EvidenceGate != "passed" || publication.SemanticAtomicityGate != "passed" || publication.TaskRelevanceGate != "passed" || publication.ComprehensionGate != "passed" {
		return false
	}
	if manifest.SchemaID != GenerationProofSchemaID || manifest.SchemaVersion != SemanticSchemaVersion || pointer.SchemaID != ActivePointerSchemaID || pointer.SchemaVersion != SemanticSchemaVersion {
		return false
	}
	if currentMap.SchemaID != SemanticMapSchemaID || currentMap.SchemaVersion != SemanticSchemaVersion ||
		currentMap.GenerationID == "" || currentMap.GenerationID != manifest.GenerationID || currentMap.GenerationID != pointer.GenerationID ||
		currentMap.ComputedBasisID == "" || currentMap.ComputedBasisID != manifest.ComputedBasisID || currentMap.ComputedBasisID != pointer.ComputedBasisID ||
		currentMap.Basis.ComputedWorkspaceSnapshotID == "" || currentMap.Basis.ComputedWorkspaceSnapshotID != manifest.ComputedSnapshotID ||
		currentMap.Basis.WorkspaceEpoch != manifest.WorkspaceEpoch || currentMap.Basis.WorkspaceEpoch != pointer.WorkspaceEpoch ||
		currentMap.Task.IntentRevision != manifest.TaskIntentRevision || currentMap.Task.IntentRevision != pointer.TaskIntentRevision ||
		currentMap.Task.TaskID == "" || currentMap.Task.TaskID != pointer.TaskID ||
		currentMap.Basis.RepositoryID == "" || currentMap.Basis.RepositoryID != pointer.RepositoryID ||
		currentMap.Basis.WorktreeID == "" || currentMap.Basis.WorktreeID != pointer.WorktreeID {
		return false
	}
	if manifest.GenerationID != pointer.GenerationID || manifest.ComputedBasisID != pointer.ComputedBasisID ||
		manifest.ValidatedAgainstSnapshotID != pointer.ValidatedAgainstSnapshotID || manifest.ExpectedLiveHeadSnapshotID != pointer.ExpectedLiveHeadSnapshotID ||
		manifest.NormalizedQueryHash == "" || manifest.NormalizedQueryHash != pointer.NormalizedQueryHash ||
		manifest.AnalysisReadSetID == "" || manifest.CausalObservationClosureID == "" || manifest.CausalObservationClosureDigest == "" ||
		manifest.ArtifactRefs.SemanticMap == "" {
		return false
	}
	mapBytes, err := json.Marshal(currentMap)
	return err == nil && storage.ArtifactCASRef(mapBytes) == manifest.ArtifactRefs.SemanticMap
}

func alignmentEvidenceComplete(criterion AcceptanceCriterion, alignment RequirementAlignment, evidenceByID map[string]SemanticEvidence, basisID, snapshotID string) bool {
	if len(alignment.CoveredStepRefs) == 0 || len(alignment.EvidenceRefs) == 0 || len(alignment.MissingEvidence) != 0 || len(alignment.MissingTests) != 0 || len(alignment.MissingContracts) != 0 {
		return false
	}
	verifiedKinds := make(map[string]bool)
	for _, ref := range alignment.EvidenceRefs {
		evidence, exists := evidenceByID[ref]
		if !exists || evidence.ValidationStatus != "verified" || !allowedEvidenceAuthority(evidence.SourceAuthority) || evidence.ComputedBasisID != basisID || evidence.SnapshotID != snapshotID {
			return false
		}
		verifiedKinds[evidence.Kind] = true
	}
	for _, requiredKind := range criterion.RequiredEvidenceKinds {
		if !verifiedKinds[strings.TrimSpace(requiredKind)] {
			return false
		}
	}
	return true
}
