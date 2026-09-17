package semantic

import (
	"fmt"
)

// DefaultDisplayBudget is the soft FlowView budget used when a request does
// not supply an explicit maximum.  It is returned by value so callers cannot
// mutate the shared default.
func DefaultDisplayBudget() DisplayBudget {
	return DisplayBudget{TargetMin: 7, TargetMax: 15, Enforcement: "soft"}
}

// NormalizeDisplayBudget turns a public maximum into a complete soft budget.
// Zero means that the caller did not supply a maximum and therefore selects
// the normal 7~15 budget.  Explicit public parsers reject zero before this
// helper is called. Negative values are never meaningful.
func NormalizeDisplayBudget(maxVisibleCoreSteps int) (DisplayBudget, error) {
	if maxVisibleCoreSteps < 0 {
		return DisplayBudget{}, fmt.Errorf("invalid_precondition: maxVisibleCoreSteps must be positive")
	}
	if maxVisibleCoreSteps == 0 {
		return DefaultDisplayBudget(), nil
	}
	minimum := 7
	if maxVisibleCoreSteps < minimum {
		minimum = maxVisibleCoreSteps
	}
	return DisplayBudget{TargetMin: minimum, TargetMax: maxVisibleCoreSteps, Enforcement: "soft"}, nil
}

// ValidateFlowViewProjectionAgainstMap checks that a projection references
// only the canonical map it was derived from. This is the runtime seam used
// after compilation and by FlowView consumers before rendering.
func ValidateFlowViewProjectionAgainstMap(projection *FlowViewProjection, mapIR *SemanticMapIR) error {
	if projection == nil || mapIR == nil {
		return fmt.Errorf("missing_precondition: projection and semantic map are required")
	}
	if projection.GenerationID != mapIR.GenerationID || projection.ComputedBasisID != mapIR.ComputedBasisID {
		return fmt.Errorf("incomparable_basis: projection generation or basis does not match semantic map")
	}
	stepIDs := make(map[string]struct{}, len(mapIR.Steps))
	for _, step := range mapIR.Steps {
		if step.StepID == "" {
			return fmt.Errorf("invalid_identity: semantic map contains an empty step id")
		}
		stepIDs[step.StepID] = struct{}{}
	}
	boundaryIDs := make(map[string]struct{}, len(mapIR.BoundaryTargets))
	for _, ref := range mapIR.BoundaryTargets {
		if ref == "" {
			return fmt.Errorf("invalid_identity: semantic map contains an empty boundary target")
		}
		boundaryIDs[ref] = struct{}{}
	}
	for _, ref := range projection.VisibleStepRefs {
		if _, ok := stepIDs[ref]; !ok {
			return fmt.Errorf("invalid_identity: projection visible ref %q is not a canonical step", ref)
		}
	}
	for _, ref := range projection.PreservedStepRefs {
		if _, ok := stepIDs[ref]; !ok {
			return fmt.Errorf("invalid_identity: projection preserved ref %q is not a canonical step", ref)
		}
	}
	for _, ref := range projection.UnknownBoundaryRefs {
		if _, ok := boundaryIDs[ref]; !ok {
			return fmt.Errorf("invalid_identity: projection unknown boundary ref %q is not in semantic map", ref)
		}
	}
	for _, fold := range projection.FoldedSubflows {
		if fold.EntryStepRef != "" {
			if _, ok := stepIDs[fold.EntryStepRef]; !ok {
				return fmt.Errorf("invalid_identity: fold entry ref %q is not a canonical step", fold.EntryStepRef)
			}
		}
		if fold.ExitStepRef != "" {
			if _, ok := stepIDs[fold.ExitStepRef]; !ok {
				return fmt.Errorf("invalid_identity: fold exit ref %q is not a canonical step", fold.ExitStepRef)
			}
		}
	}
	return nil
}

// BuildFlowViewProjection computes the FlowViewProjection for a SemanticMapIR.
// Implements VS04-A9:
//   - Whole flow remains in SemanticMapIR.
//   - FlowViewProjection applies soft 7~15 display budget.
//   - If total steps < 7, all are shown without padding.
//   - If total steps > 15, non-critical subflows are folded.
//   - Preserved steps (entry, result, branch, failure, external effect, unknown boundary)
//     MUST NEVER be folded or hidden.
func BuildFlowViewProjection(mapIR *SemanticMapIR) *FlowViewProjection {
	return BuildFlowViewProjectionWithBudget(mapIR, DefaultDisplayBudget())
}

// BuildFlowViewProjectionWithBudget computes a projection for one canonical
// map and one complete display budget.  The map remains the source of truth:
// only non-critical intermediate steps may be folded, and every projection
// reference still points into the same generation and basis.
func BuildFlowViewProjectionWithBudget(mapIR *SemanticMapIR, budget DisplayBudget) *FlowViewProjection {
	if mapIR == nil {
		return nil
	}
	if budget.TargetMax < 1 || budget.TargetMin < 1 || budget.TargetMin > budget.TargetMax || (budget.Enforcement != "soft" && budget.Enforcement != "strict") {
		budget = DefaultDisplayBudget()
	}
	steps := mapIR.Steps
	totalSteps := len(steps)
	mode := mapIR.Task.Mode
	if mode != "feature" && mode != "review" && mode != "impact" && mode != "debug" && mode != "incident" && mode != "onboarding" {
		mode = "onboarding"
	}

	// 1. Identify critical preserved step IDs
	preservedMap := make(map[string]bool)
	var preservedRefs []string

	for i, s := range steps {
		isCritical := false
		// Entry step
		if i == 0 || s.Kind == "user_action" {
			isCritical = true
		}
		// Terminal / result step
		if i == totalSteps-1 || s.Kind == "mutation" || s.StateDelta != nil {
			isCritical = true
		}
		// Critical branch / failure / external effect
		if s.Kind == "guard" || s.Kind == "branch" || s.Kind == "decision" ||
			s.Kind == "failure" || s.Kind == "security" || s.Kind == "external_effect" ||
			s.SideEffect != nil || s.Kind == "external" {
			isCritical = true
		}
		// Step referencing an unknown or unresolved edge
		for _, u := range mapIR.Unknowns {
			if u.Subject == s.TechnicalName || u.Subject == s.Name {
				isCritical = true
			}
		}

		if isCritical {
			preservedMap[s.StepID] = true
			preservedRefs = append(preservedRefs, s.StepID)
		}
	}

	// 2. If all steps fit the requested budget, no folding is needed.  This is
	// deliberately based on the request, not the default 15-step budget.
	if totalSteps <= budget.TargetMax {
		visible := make([]string, totalSteps)
		for i, s := range steps {
			visible[i] = s.StepID
		}
		return &FlowViewProjection{
			SchemaID:            FlowViewProjectionSchemaID,
			SchemaVersion:       SemanticSchemaVersion,
			ProjectionID:        projectionID(mapIR, budget),
			GenerationID:        mapIR.GenerationID,
			ComputedBasisID:     mapIR.ComputedBasisID,
			Mode:                mode,
			DisplayBudget:       budget,
			VisibleStepRefs:     visible,
			PreservedStepRefs:   preservedRefs,
			UnknownBoundaryRefs: append([]string{}, mapIR.BoundaryTargets...),
			FoldedSubflows:      []FoldedSubflow{},
		}
	}

	// 3. Total steps > 15: fold non-critical intermediate subflows
	var visibleRefs []string
	var folded []FoldedSubflow
	foldCounter := 1

	i := 0
	for i < totalSteps {
		s := steps[i]
		if preservedMap[s.StepID] {
			visibleRefs = append(visibleRefs, s.StepID)
			i++
			continue
		}

		// Found a non-critical step: find the contiguous non-critical run
		runStart := i
		for i < totalSteps && !preservedMap[steps[i].StepID] {
			i++
		}
		runEnd := i // steps[runStart:runEnd] are non-critical
		hiddenCount := runEnd - runStart

		// Determine boundaries for the fold
		entryRef := ""
		if len(visibleRefs) > 0 {
			entryRef = visibleRefs[len(visibleRefs)-1]
		}
		exitRef := ""
		if runEnd < totalSteps {
			exitRef = steps[runEnd].StepID
		}

		// Only fold if we need to reduce towards the 15 budget and run has steps
		if hiddenCount > 0 {
			fold := FoldedSubflow{
				FoldID:          fmt.Sprintf("fold-%02d", foldCounter),
				EntryStepRef:    entryRef,
				ExitStepRef:     exitRef,
				HiddenCount:     hiddenCount,
				DrilldownTarget: fmt.Sprintf("subflow-%s-%02d", mapIR.MapID, foldCounter),
			}
			folded = append(folded, fold)
			foldCounter++
		}
	}

	// Ensure ALL preserved refs are present in visibleRefs (D32 verification)
	visSet := make(map[string]bool)
	for _, v := range visibleRefs {
		visSet[v] = true
	}
	for _, p := range preservedRefs {
		if !visSet[p] {
			visibleRefs = append(visibleRefs, p)
		}
	}

	return &FlowViewProjection{
		SchemaID:            FlowViewProjectionSchemaID,
		SchemaVersion:       SemanticSchemaVersion,
		ProjectionID:        projectionID(mapIR, budget),
		GenerationID:        mapIR.GenerationID,
		ComputedBasisID:     mapIR.ComputedBasisID,
		Mode:                mode,
		DisplayBudget:       budget,
		VisibleStepRefs:     visibleRefs,
		PreservedStepRefs:   preservedRefs,
		UnknownBoundaryRefs: append([]string{}, mapIR.BoundaryTargets...),
		FoldedSubflows:      folded,
	}
}

func projectionID(mapIR *SemanticMapIR, budget DisplayBudget) string {
	return fmt.Sprintf("projection-%s-%s-%d", mapIR.GenerationID, mapIR.Task.Mode, budget.TargetMax)
}
