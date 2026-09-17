package semantic

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
)

var (
	ErrMissingPrecondition = errors.New("missing_precondition")
	ErrIncomparableBasis   = errors.New("incomparable_basis")
)

// ValidateComparableBases requires explicit immutable identity on both maps.
// Epoch, repository, snapshot tree, dependency and schema mismatches are not
// comparable, including when one side omitted an identity field.
func ValidateComparableBases(baselineMap, currentMap *SemanticMapIR) error {
	if baselineMap == nil || currentMap == nil {
		return ErrMissingPrecondition
	}
	if baselineMap.MapID == "" || currentMap.MapID == "" || baselineMap.GenerationID == "" || currentMap.GenerationID == "" {
		return ErrMissingPrecondition
	}
	if baselineMap.ComputedBasisID == "" || currentMap.ComputedBasisID == "" {
		return ErrMissingPrecondition
	}
	if baselineMap.Basis.ComputedWorkspaceSnapshotID == "" || currentMap.Basis.ComputedWorkspaceSnapshotID == "" || baselineMap.Basis.SnapshotTreeID == "" || currentMap.Basis.SnapshotTreeID == "" || baselineMap.Basis.RepositoryID == "" || currentMap.Basis.RepositoryID == "" {
		return ErrMissingPrecondition
	}
	if baselineMap.SchemaID == "" || currentMap.SchemaID == "" {
		return ErrMissingPrecondition
	}
	if baselineMap.SchemaID != currentMap.SchemaID || baselineMap.SchemaVersion != currentMap.SchemaVersion {
		return fmt.Errorf("%w: schema identity mismatch", ErrIncomparableBasis)
	}
	if baselineMap.Basis.RepositoryID != currentMap.Basis.RepositoryID {
		return fmt.Errorf("%w: repository or basis identity mismatch", ErrIncomparableBasis)
	}
	if baselineMap.Task.Mode == "" || currentMap.Task.Mode == "" {
		return ErrMissingPrecondition
	}
	if baselineMap.Task.Mode != currentMap.Task.Mode {
		return fmt.Errorf("%w: task mode mismatch", ErrIncomparableBasis)
	}
	if baselineMap.Task.TaskID == "" || currentMap.Task.TaskID == "" || baselineMap.Task.TaskID == "task-unknown" || currentMap.Task.TaskID == "task-unknown" {
		return ErrMissingPrecondition
	}
	if baselineMap.Task.TaskID != currentMap.Task.TaskID {
		return fmt.Errorf("%w: task scope mismatch", ErrIncomparableBasis)
	}
	if baselineMap.Basis.WorkspaceEpoch != currentMap.Basis.WorkspaceEpoch {
		return fmt.Errorf("%w: workspace epoch mismatch (%d vs %d)", ErrIncomparableBasis, baselineMap.Basis.WorkspaceEpoch, currentMap.Basis.WorkspaceEpoch)
	}
	return nil
}

// ComputeSemanticDelta compares two explicit candidate generations. It uses a
// one-to-many structural index and never treats a symbol-name coincidence as a
// proven rename.
func ComputeSemanticDelta(comparisonID string, baselineMap, currentMap *SemanticMapIR) (*SemanticDeltaIR, error) {
	if err := ValidateComparableBases(baselineMap, currentMap); err != nil {
		return nil, err
	}
	delta := &SemanticDeltaIR{
		SchemaID:                          SemanticDeltaSchemaID,
		SchemaVersion:                     SemanticSchemaVersion,
		ComparisonID:                      comparisonID,
		TaskIntentRevision:                currentMap.Task.IntentRevision,
		BaselineComputedBasisID:           baselineMap.ComputedBasisID,
		CurrentComputedBasisID:            currentMap.ComputedBasisID,
		CurrentValidatedAgainstSnapshotID: currentMap.ValidatedAgainstSnapshotID,
		FromGeneration:                    baselineMap.GenerationID,
		ToGeneration:                      currentMap.GenerationID,
		Status:                            "comparable",
		Changes:                           []DeltaChange{},
		StructuralSummary:                 &StructuralSummary{},
	}

	baselineByID, err := uniqueStepIndex(baselineMap.Steps, func(step SemanticStep) string { return step.StepID })
	if err != nil {
		return nil, err
	}
	baselineByStructural, err := structuralIndex(baselineMap.Steps)
	if err != nil {
		return nil, err
	}
	matchedBaseline := make(map[string]bool, len(baselineMap.Steps))

	for _, current := range currentMap.Steps {
		previous, matched, ambiguous := matchStep(current, baselineByID, baselineByStructural, matchedBaseline)
		if ambiguous {
			candidates := make([]string, 0)
			for _, candidate := range baselineByStructural[stepStructuralKey(current)] {
				if !matchedBaseline[candidate.StepID] {
					candidates = append(candidates, candidate.StepID)
				}
			}
			sort.Strings(candidates)
			delta.Changes = append(delta.Changes, DeltaChange{DeltaID: deterministicDeltaID("ambiguous", current.StepID), Kind: "ambiguous_move", TargetStepID: current.StepID, Summary: "구조적 대상이 여러 baseline step과 일치하여 이동을 확정할 수 없음", CandidateStepRefs: candidates, StructuralChanges: []string{"ambiguous structural identity"}, EpistemicStatus: "unknown", ValidationStatus: "pending", MoveStatus: "ambiguous"})
			delta.StructuralSummary.CollapsedStructuralCount++
			continue
		}
		if !matched {
			delta.Changes = append(delta.Changes, DeltaChange{DeltaID: deterministicDeltaID("added", current.StepID), Kind: "added_behavior", TargetStepID: current.StepID, Summary: fmt.Sprintf("새 행동 추가됨: %s (%s)", current.Name, current.TechnicalName), RequirementRefs: append([]string(nil), current.Rules...), StructuralChanges: []string{"new structural target"}, EvidenceRefs: append([]string(nil), current.EvidenceRefs...), EpistemicStatus: "observed", ValidationStatus: "verified", ToStepID: current.StepID, MoveStatus: "not_applicable"})
			delta.StructuralSummary.AddedStepsCount++
			continue
		}
		matchedBaseline[previous.StepID] = true
		if changedBehavior(previous, current) {
			facets := behavioralChangeFacets(previous, current)
			delta.Changes = append(delta.Changes, DeltaChange{DeltaID: deterministicDeltaID("changed", current.StepID), Kind: "changed_rule", TargetStepID: current.StepID, Summary: fmt.Sprintf("%s: %s", behavioralChangeSummary(facets), current.Name), RequirementRefs: append([]string(nil), current.Rules...), StructuralChanges: facets, EvidenceRefs: append([]string(nil), current.EvidenceRefs...), EpistemicStatus: "observed", ValidationStatus: "verified", FromStepID: previous.StepID, ToStepID: current.StepID, MoveStatus: moveStatus(previous, current)})
			delta.StructuralSummary.ChangedStepsCount++
			continue
		}
		if !reflect.DeepEqual(previous.EvidenceRefs, current.EvidenceRefs) {
			delta.Changes = append(delta.Changes, DeltaChange{DeltaID: deterministicDeltaID("evidence", current.StepID), Kind: "evidence_updated", TargetStepID: current.StepID, Summary: fmt.Sprintf("근거 갱신됨: %s", current.Name), RequirementRefs: append([]string(nil), current.Rules...), EvidenceRefs: append([]string(nil), current.EvidenceRefs...), EpistemicStatus: "observed", ValidationStatus: "verified", FromStepID: previous.StepID, ToStepID: current.StepID, MoveStatus: moveStatus(previous, current)})
			continue
		}
		if structuralChanged(previous, current) {
			delta.Changes = append(delta.Changes, DeltaChange{DeltaID: deterministicDeltaID("structural", current.StepID), Kind: "structural_only", TargetStepID: current.StepID, Summary: fmt.Sprintf("구조적 위치 변경됨: %s", current.Name), StructuralChanges: []string{"source range moved"}, EvidenceRefs: append([]string(nil), current.EvidenceRefs...), EpistemicStatus: "observed", ValidationStatus: "verified", FromStepID: previous.StepID, ToStepID: current.StepID, MoveStatus: "proven"})
			delta.StructuralSummary.CollapsedStructuralCount++
		}
	}

	for _, previous := range baselineMap.Steps {
		if matchedBaseline[previous.StepID] {
			continue
		}
		delta.Changes = append(delta.Changes, DeltaChange{DeltaID: deterministicDeltaID("removed", previous.StepID), Kind: "removed_behavior", TargetStepID: previous.StepID, Summary: fmt.Sprintf("행동 제거됨: %s (%s)", previous.Name, previous.TechnicalName), RequirementRefs: append([]string(nil), previous.Rules...), StructuralChanges: []string{"structural target removed"}, EvidenceRefs: append([]string(nil), previous.EvidenceRefs...), EpistemicStatus: "observed", ValidationStatus: "verified", FromStepID: previous.StepID, MoveStatus: "not_applicable"})
		delta.StructuralSummary.RemovedStepsCount++
	}
	for _, relation := range callRelationChanges(baselineMap.Edges, currentMap.Edges) {
		delta.Changes = append(delta.Changes, relation)
		delta.StructuralSummary.CollapsedStructuralCount++
	}
	return delta, nil
}

func uniqueStepIndex(steps []SemanticStep, key func(SemanticStep) string) (map[string]SemanticStep, error) {
	result := make(map[string]SemanticStep, len(steps))
	for _, step := range steps {
		id := key(step)
		if id == "" {
			return nil, ErrMissingPrecondition
		}
		if _, exists := result[id]; exists {
			return nil, fmt.Errorf("%w: duplicate step id %s", ErrIncomparableBasis, id)
		}
		result[id] = step
	}
	return result, nil
}

func structuralIndex(steps []SemanticStep) (map[string][]SemanticStep, error) {
	result := make(map[string][]SemanticStep)
	for _, step := range steps {
		key := stepStructuralKey(step)
		if key == "" {
			return nil, fmt.Errorf("%w: step %s lacks structural identity", ErrMissingPrecondition, step.StepID)
		}
		result[key] = append(result[key], step)
	}
	for key := range result {
		sort.Slice(result[key], func(i, j int) bool { return result[key][i].StepID < result[key][j].StepID })
	}
	return result, nil
}

func matchStep(current SemanticStep, byID map[string]SemanticStep, byStructural map[string][]SemanticStep, matched map[string]bool) (SemanticStep, bool, bool) {
	if previous, ok := byID[current.StepID]; ok && !matched[previous.StepID] {
		return previous, true, false
	}
	candidates := []SemanticStep{}
	for _, candidate := range byStructural[stepStructuralKey(current)] {
		if !matched[candidate.StepID] {
			candidates = append(candidates, candidate)
		}
	}
	if len(candidates) == 1 {
		return candidates[0], true, false
	}
	if len(candidates) > 1 {
		return SemanticStep{}, false, true
	}
	return SemanticStep{}, false, false
}

func stepStructuralKey(step SemanticStep) string {
	if step.StructuralIdentity != "" {
		return step.StructuralIdentity
	}
	if step.Anchor.RepoRelativePath == "" || step.Anchor.EnclosingSymbolPath == "" && step.TechnicalName == "" {
		return ""
	}
	return strings.Join([]string{step.Anchor.RepoRelativePath, step.Anchor.EnclosingSymbolPath, step.TechnicalName, step.Kind}, "\x00")
}

func changedBehavior(previous, current SemanticStep) bool {
	return !reflect.DeepEqual(previous.Rules, current.Rules) || !reflect.DeepEqual(previous.Branch, current.Branch) || !reflect.DeepEqual(previous.SideEffect, current.SideEffect) || !reflect.DeepEqual(previous.StateDelta, current.StateDelta) || previous.Kind != current.Kind
}

func behavioralChangeFacets(previous, current SemanticStep) []string {
	facets := make([]string, 0, 4)
	if previous.Kind != current.Kind || !reflect.DeepEqual(previous.Rules, current.Rules) {
		facets = append(facets, "behavior changed")
	}
	if !reflect.DeepEqual(previous.Branch, current.Branch) {
		facets = append(facets, "branch changed")
	}
	if !reflect.DeepEqual(previous.StateDelta, current.StateDelta) {
		facets = append(facets, "state changed")
	}
	if !reflect.DeepEqual(previous.SideEffect, current.SideEffect) {
		facets = append(facets, "external effect changed")
	}
	return facets
}

func behavioralChangeSummary(facets []string) string {
	labels := make([]string, 0, len(facets))
	for _, facet := range facets {
		switch facet {
		case "behavior changed":
			labels = append(labels, "행동")
		case "branch changed":
			labels = append(labels, "분기")
		case "state changed":
			labels = append(labels, "상태")
		case "external effect changed":
			labels = append(labels, "외부 효과")
		}
	}
	return strings.Join(labels, "·") + " 변경됨"
}

func callRelationChanges(baseline, current []SemanticEdge) []DeltaChange {
	baselineByIdentity := make(map[string]SemanticEdge, len(baseline))
	currentByIdentity := make(map[string]SemanticEdge, len(current))
	identities := make(map[string]struct{}, len(baseline)+len(current))
	for _, edge := range baseline {
		identity := callRelationIdentity(edge)
		baselineByIdentity[identity] = edge
		identities[identity] = struct{}{}
	}
	for _, edge := range current {
		identity := callRelationIdentity(edge)
		currentByIdentity[identity] = edge
		identities[identity] = struct{}{}
	}
	ordered := make([]string, 0, len(identities))
	for identity := range identities {
		ordered = append(ordered, identity)
	}
	sort.Strings(ordered)

	changes := make([]DeltaChange, 0)
	for _, identity := range ordered {
		previous, hadPrevious := baselineByIdentity[identity]
		next, hasNext := currentByIdentity[identity]
		if hadPrevious && hasNext && reflect.DeepEqual(previous, next) {
			continue
		}
		target := next.FromStepID
		if target == "" {
			target = previous.FromStepID
		}
		change := "changed"
		summary := "호출 관계 변경됨"
		fromStepID, toStepID := previous.FromStepID, next.ToStepID
		if !hadPrevious {
			change, summary = "added", "호출 관계 추가됨"
			fromStepID = ""
		} else if !hasNext {
			change, summary = "removed", "호출 관계 제거됨"
			toStepID = ""
		}
		changes = append(changes, DeltaChange{
			DeltaID:           deterministicDeltaID("relation-"+change, identity),
			Kind:              "structural_only",
			TargetStepID:      target,
			Summary:           summary,
			StructuralChanges: []string{"call relation " + change},
			EpistemicStatus:   "observed",
			ValidationStatus:  "verified",
			FromStepID:        fromStepID,
			ToStepID:          toStepID,
			MoveStatus:        "not_applicable",
		})
	}
	return changes
}

func callRelationIdentity(edge SemanticEdge) string {
	target := edge.ToStepID
	if target == "" {
		target = edge.ToSymbolPath
	}
	return edge.FromStepID + "\x00" + target
}

func structuralChanged(previous, current SemanticStep) bool {
	return previous.Anchor.RepoRelativePath != current.Anchor.RepoRelativePath || previous.Anchor.ByteRange != current.Anchor.ByteRange || !reflect.DeepEqual(previous.Anchor.SymbolRange, current.Anchor.SymbolRange)
}

func moveStatus(previous, current SemanticStep) string {
	if structuralChanged(previous, current) {
		return "proven"
	}
	return "not_applicable"
}

func deterministicDeltaID(kind, stepID string) string {
	h := sha256.Sum256([]byte(kind + "\x00" + stepID))
	return "delta-" + hex.EncodeToString(h[:])[:20]
}

// stepFingerprint remains as a compatibility helper for consumers that used
// the old name. It now returns the complete structural key, never a symbol-only
// identity.
func stepFingerprint(st SemanticStep) string { return stepStructuralKey(st) }
