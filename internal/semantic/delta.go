package semantic

import (
	"errors"
	"fmt"
	"reflect"
)

var (
	ErrMissingPrecondition = errors.New("missing_precondition")
	ErrIncomparableBasis   = errors.New("incomparable_basis")
)

// ValidateComparableBases verifies that baseline and current are non-nil,
// share compatible repository/workspace epochs, and have compatible schemas (Raw §8.2, VS05-A1, A2).
func ValidateComparableBases(baselineMap, currentMap *SemanticMapIR) error {
	if baselineMap == nil || currentMap == nil {
		return ErrMissingPrecondition
	}
	if baselineMap.MapID == "" || currentMap.MapID == "" {
		return ErrMissingPrecondition
	}

	// Incompatible repository / epoch check
	if baselineMap.Basis.WorkspaceEpoch != 0 && currentMap.Basis.WorkspaceEpoch != 0 {
		if baselineMap.Basis.WorkspaceEpoch != currentMap.Basis.WorkspaceEpoch {
			return fmt.Errorf("%w: workspace epoch mismatch (%d vs %d)",
				ErrIncomparableBasis, baselineMap.Basis.WorkspaceEpoch, currentMap.Basis.WorkspaceEpoch)
		}
	}

	// Schema compatibility
	if baselineMap.SchemaVersion != currentMap.SchemaVersion {
		return fmt.Errorf("%w: schema version mismatch (%d vs %d)",
			ErrIncomparableBasis, baselineMap.SchemaVersion, currentMap.SchemaVersion)
	}

	return nil
}

// ComputeSemanticDelta compares baseline and current SemanticMapIRs and generates
// a structured SemanticDeltaIR distinguishing added, changed, removed, and evidence-updated behaviors (VS05-A3, A4).
func ComputeSemanticDelta(comparisonID string, baselineMap, currentMap *SemanticMapIR) (*SemanticDeltaIR, error) {
	if err := ValidateComparableBases(baselineMap, currentMap); err != nil {
		return nil, err
	}

	delta := &SemanticDeltaIR{
		SchemaID:                          "codeflow.semantic-delta-ir",
		SchemaVersion:                     1,
		ComparisonID:                      comparisonID,
		TaskIntentRevision:                currentMap.Task.IntentRevision,
		BaselineComputedBasisID:           baselineMap.ComputedBasisID,
		CurrentComputedBasisID:            currentMap.ComputedBasisID,
		CurrentValidatedAgainstSnapshotID: currentMap.ValidatedAgainstSnapshotID,
		FromGeneration:                    baselineMap.GenerationID,
		ToGeneration:                      currentMap.GenerationID,
		Status:                            "comparable",
		Changes:                           make([]DeltaChange, 0),
		StructuralSummary:                 &StructuralSummary{},
	}

	// Index baseline steps by StepID and secondary fingerprint (symbolPath/technicalName)
	baselineByID := make(map[string]SemanticStep)
	baselineByFingerprint := make(map[string]SemanticStep)
	for _, st := range baselineMap.Steps {
		baselineByID[st.StepID] = st
		fp := stepFingerprint(st)
		if fp != "" {
			baselineByFingerprint[fp] = st
		}
	}

	// Index current steps by StepID and fingerprint
	currentByID := make(map[string]SemanticStep)
	currentByFingerprint := make(map[string]SemanticStep)
	for _, st := range currentMap.Steps {
		currentByID[st.StepID] = st
		fp := stepFingerprint(st)
		if fp != "" {
			currentByFingerprint[fp] = st
		}
	}

	matchedBaselineIDs := make(map[string]bool)

	// Process current steps (added, changed, evidence_updated, structural_only)
	for _, currStep := range currentMap.Steps {
		var prevStep SemanticStep
		var found bool

		if b, ok := baselineByID[currStep.StepID]; ok {
			prevStep = b
			found = true
			matchedBaselineIDs[currStep.StepID] = true
		} else if b, ok := baselineByFingerprint[stepFingerprint(currStep)]; ok {
			// Stable identity rename/move match (SID-C6)
			prevStep = b
			found = true
			matchedBaselineIDs[b.StepID] = true
		}

		if !found {
			// Added behavior (VS05-A3)
			delta.Changes = append(delta.Changes, DeltaChange{
				DeltaID:           fmt.Sprintf("delta-add-%s", currStep.StepID),
				Kind:              "added_behavior",
				TargetStepID:      currStep.StepID,
				Summary:           fmt.Sprintf("새 행동 추가됨: %s (%s)", currStep.Name, currStep.TechnicalName),
				RequirementRefs:   currStep.Rules,
				StructuralChanges: []string{fmt.Sprintf("step %s declared at %s", currStep.StepID, currStep.Anchor.RepoRelativePath)},
				EvidenceRefs:      currStep.EvidenceRefs,
				EpistemicStatus:   "observed",
				ValidationStatus:  "verified",
			})
			delta.StructuralSummary.AddedStepsCount++
			continue
		}

		// Existing step: compare rules, branch, stateDelta, sideEffect
		ruleChanged := !reflect.DeepEqual(currStep.Rules, prevStep.Rules)
		branchChanged := (currStep.Branch != nil && prevStep.Branch != nil && *currStep.Branch != *prevStep.Branch) ||
			(currStep.Branch != nil && prevStep.Branch == nil) || (currStep.Branch == nil && prevStep.Branch != nil)
		sideEffectChanged := (currStep.SideEffect != nil && prevStep.SideEffect != nil && *currStep.SideEffect != *prevStep.SideEffect) ||
			(currStep.SideEffect != nil && prevStep.SideEffect == nil) || (currStep.SideEffect == nil && prevStep.SideEffect != nil)
		stateDeltaChanged := !reflect.DeepEqual(currStep.StateDelta, prevStep.StateDelta)

		if ruleChanged || branchChanged || sideEffectChanged || stateDeltaChanged {
			// Changed rule/behavior (VS05-A3, A4)
			delta.Changes = append(delta.Changes, DeltaChange{
				DeltaID:           fmt.Sprintf("delta-change-%s", currStep.StepID),
				Kind:              "changed_rule",
				TargetStepID:      currStep.StepID,
				Summary:           fmt.Sprintf("규칙/상태 전이 변경됨: %s (%s)", currStep.Name, currStep.TechnicalName),
				RequirementRefs:   currStep.Rules,
				StructuralChanges: []string{fmt.Sprintf("modified rules/branches for %s", currStep.StepID)},
				EvidenceRefs:      currStep.EvidenceRefs,
				EpistemicStatus:   "observed",
				ValidationStatus:  "verified",
			})
			delta.StructuralSummary.ChangedStepsCount++
			continue
		}

		// Check if evidence changed
		evidenceChanged := !reflect.DeepEqual(currStep.EvidenceRefs, prevStep.EvidenceRefs)
		if evidenceChanged && len(currStep.EvidenceRefs) > 0 {
			delta.Changes = append(delta.Changes, DeltaChange{
				DeltaID:           fmt.Sprintf("delta-ev-%s", currStep.StepID),
				Kind:              "evidence_updated",
				TargetStepID:      currStep.StepID,
				Summary:           fmt.Sprintf("근거 갱신됨: %s", currStep.Name),
				RequirementRefs:   currStep.Rules,
				EvidenceRefs:      currStep.EvidenceRefs,
				EpistemicStatus:   "observed",
				ValidationStatus:  "verified",
			})
			continue
		}

		// Check if line/byte range moved (structural only)
		byteMoved := currStep.Anchor.ByteRange != prevStep.Anchor.ByteRange
		if byteMoved {
			delta.StructuralSummary.CollapsedStructuralCount++
		}
	}

	// Check for removed steps in baseline
	for _, bStep := range baselineMap.Steps {
		if !matchedBaselineIDs[bStep.StepID] {
			delta.Changes = append(delta.Changes, DeltaChange{
				DeltaID:           fmt.Sprintf("delta-remove-%s", bStep.StepID),
				Kind:              "removed_behavior",
				TargetStepID:      bStep.StepID,
				Summary:           fmt.Sprintf("행동 제거됨: %s (%s)", bStep.Name, bStep.TechnicalName),
				RequirementRefs:   bStep.Rules,
				StructuralChanges: []string{fmt.Sprintf("step %s removed from %s", bStep.StepID, bStep.Anchor.RepoRelativePath)},
				EvidenceRefs:      bStep.EvidenceRefs,
				EpistemicStatus:   "observed",
				ValidationStatus:  "verified",
			})
			delta.StructuralSummary.RemovedStepsCount++
		}
	}

	return delta, nil
}

func stepFingerprint(st SemanticStep) string {
	if st.Anchor.EnclosingSymbolPath != "" {
		return st.Anchor.EnclosingSymbolPath
	}
	if st.Anchor.CanonicalAstFingerprint != "" {
		return st.Anchor.CanonicalAstFingerprint
	}
	if st.TechnicalName != "" && st.Anchor.RepoRelativePath != "" {
		return fmt.Sprintf("%s::%s", st.Anchor.RepoRelativePath, st.TechnicalName)
	}
	return st.Name
}
