package flowview

import "strings"

// ViewStepIdentity is the minimum stable identity needed to preserve a
// selection across compatible generations. StepID is generation-local while
// StructuralIdentity is stable across compatible rebuilds.
type ViewStepIdentity struct {
	StepID             string
	StructuralIdentity string
}

// LogicalScrollAnchor records the selected structural step's viewport offset.
// The offset is logical UI state, not source-code position.
type LogicalScrollAnchor struct {
	StepID             string
	StructuralIdentity string
	OffsetPx           float64
}

// LogicalViewSelection is the production transition result used by FlowView
// and the versioned A14 corpus. IdentityLoss is reported separately and is not
// counted in the compatible-update preservation denominator.
type LogicalViewSelection struct {
	SelectedStepID             string
	SelectedStructuralIdentity string
	ScrollAnchor               *LogicalScrollAnchor
	Preserved                  bool
	IdentityLoss               bool
}

// PreserveLogicalViewSelection resolves a prior selection in a new generation.
// It never falls back by ordinal, generated step id, symbol text, or nearest
// index because those choices would silently count an incompatible update as
// preserved. Duplicate structural identities are also treated as identity loss
// because the destination would be ambiguous.
func PreserveLogicalViewSelection(previous LogicalViewSelection, next []ViewStepIdentity) LogicalViewSelection {
	identity := strings.TrimSpace(previous.SelectedStructuralIdentity)
	if identity == "" {
		return LogicalViewSelection{}
	}

	match := ViewStepIdentity{}
	matches := 0
	for _, candidate := range next {
		if strings.TrimSpace(candidate.StructuralIdentity) == identity {
			match = candidate
			matches++
		}
	}
	if matches != 1 || strings.TrimSpace(match.StepID) == "" {
		return LogicalViewSelection{
			SelectedStructuralIdentity: identity,
			IdentityLoss:               true,
		}
	}

	result := LogicalViewSelection{
		SelectedStepID:             match.StepID,
		SelectedStructuralIdentity: identity,
		Preserved:                  true,
	}
	if previous.ScrollAnchor != nil &&
		strings.TrimSpace(previous.ScrollAnchor.StructuralIdentity) == identity &&
		previous.ScrollAnchor.OffsetPx >= 0 {
		result.ScrollAnchor = &LogicalScrollAnchor{
			StepID:             match.StepID,
			StructuralIdentity: identity,
			OffsetPx:           previous.ScrollAnchor.OffsetPx,
		}
	} else {
		// Selection alone is not enough for the A14 preservation claim. Keep the
		// selected identity resolved but report the transition as not preserved.
		result.Preserved = false
	}
	return result
}
