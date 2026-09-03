package semantic

import (
	"fmt"
)

// BuildFlowViewProjection computes the FlowViewProjection for a SemanticMapIR.
// Implements D32 and VS02-A4:
// - Whole flow remains in SemanticMapIR.
// - FlowViewProjection applies soft 7~15 display budget.
// - If total steps < 7, all are shown without padding.
// - If total steps > 15, non-critical subflows are folded.
// - Preserved steps (entry, result, branch, failure, external effect, unknown boundary)
//   MUST NEVER be folded or hidden.
func BuildFlowViewProjection(mapIR *SemanticMapIR) *FlowViewProjection {
	steps := mapIR.Steps
	totalSteps := len(steps)

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
		if s.Kind == "guard" || s.Kind == "branch" || s.Branch != nil ||
			s.SideEffect != nil || s.Kind == "failure" {
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

	// 2. If total steps <= 15, no folding is needed
	if totalSteps <= 15 {
		visible := make([]string, totalSteps)
		for i, s := range steps {
			visible[i] = s.StepID
		}
		return &FlowViewProjection{
			SchemaID:          "https://codeflow.local/schemas/flow-view-projection.schema.json",
			SchemaVersion:     1,
			ProjectionID:      fmt.Sprintf("projection-%s-%s", mapIR.GenerationID, mapIR.Task.Mode),
			GenerationID:      mapIR.GenerationID,
			ComputedBasisID:   mapIR.ComputedBasisID,
			Mode:              mapIR.Task.Mode,
			DisplayBudget: DisplayBudget{
				TargetMin:   7,
				TargetMax:   15,
				Enforcement: "soft",
			},
			VisibleStepRefs:   visible,
			PreservedStepRefs: preservedRefs,
			FoldedSubflows:    []FoldedSubflow{},
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
		SchemaID:        "https://codeflow.local/schemas/flow-view-projection.schema.json",
		SchemaVersion:   1,
		ProjectionID:    fmt.Sprintf("projection-%s-%s", mapIR.GenerationID, mapIR.Task.Mode),
		GenerationID:    mapIR.GenerationID,
		ComputedBasisID: mapIR.ComputedBasisID,
		Mode:            mapIR.Task.Mode,
		DisplayBudget: DisplayBudget{
			TargetMin:   7,
			TargetMax:   15,
			Enforcement: "soft",
		},
		VisibleStepRefs:   visibleRefs,
		PreservedStepRefs: preservedRefs,
		FoldedSubflows:    folded,
	}
}
