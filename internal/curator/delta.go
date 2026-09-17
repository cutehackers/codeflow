package curator

import (
	"codeflow/internal/collector/fusion"
)

// SemanticDelta captures immutable behavioral differences between baseline and current flow specs.
type SemanticDelta struct {
	BaselineFlowID  string            `json:"baselineFlowId"`
	CurrentFlowID   string            `json:"currentFlowId"`
	AddedSteps      []fusion.FlowStep `json:"addedSteps"`
	RemovedSteps    []fusion.FlowStep `json:"removedSteps"`
	ModifiedSteps   []ModifiedStep    `json:"modifiedSteps"`
	AddedUnknowns   []fusion.Unknown  `json:"addedUnknowns"`
	RemovedUnknowns []fusion.Unknown  `json:"removedUnknowns"`
}

// ModifiedStep represents a step present in both baseline and current with altered properties.
type ModifiedStep struct {
	Ordinal      int             `json:"ordinal"`
	BaselineStep fusion.FlowStep `json:"baselineStep"`
	CurrentStep  fusion.FlowStep `json:"currentStep"`
	DiffSummary  string          `json:"diffSummary"`
}

// ComputeDelta calculates differences between baseline and current flow specs without mutable transactions.
func ComputeDelta(baseline, current fusion.FlowSpec) SemanticDelta {
	delta := SemanticDelta{
		BaselineFlowID:  baseline.FlowID,
		CurrentFlowID:   current.FlowID,
		AddedSteps:      make([]fusion.FlowStep, 0),
		RemovedSteps:    make([]fusion.FlowStep, 0),
		ModifiedSteps:   make([]ModifiedStep, 0),
		AddedUnknowns:   make([]fusion.Unknown, 0),
		RemovedUnknowns: make([]fusion.Unknown, 0),
	}

	baseStepMap := make(map[string]fusion.FlowStep, len(baseline.Steps))
	for _, s := range baseline.Steps {
		baseStepMap[s.Anchor.EnclosingSymbolPath] = s
	}

	currStepMap := make(map[string]fusion.FlowStep, len(current.Steps))
	for _, s := range current.Steps {
		currStepMap[s.Anchor.EnclosingSymbolPath] = s
		if base, ok := baseStepMap[s.Anchor.EnclosingSymbolPath]; !ok {
			delta.AddedSteps = append(delta.AddedSteps, s)
		} else if base.Name != s.Name || base.Kind != s.Kind {
			delta.ModifiedSteps = append(delta.ModifiedSteps, ModifiedStep{
				Ordinal:      s.Ordinal,
				BaselineStep: base,
				CurrentStep:  s,
				DiffSummary:  "step changed",
			})
		}
	}

	for _, s := range baseline.Steps {
		if _, ok := currStepMap[s.Anchor.EnclosingSymbolPath]; !ok {
			delta.RemovedSteps = append(delta.RemovedSteps, s)
		}
	}

	baseUnkMap := make(map[string]fusion.Unknown, len(baseline.Unknowns))
	for _, u := range baseline.Unknowns {
		baseUnkMap[u.Subject] = u
	}
	for _, u := range current.Unknowns {
		if _, ok := baseUnkMap[u.Subject]; !ok {
			delta.AddedUnknowns = append(delta.AddedUnknowns, u)
		}
	}
	for _, u := range baseline.Unknowns {
		found := false
		for _, curU := range current.Unknowns {
			if curU.Subject == u.Subject {
				found = true
				break
			}
		}
		if !found {
			delta.RemovedUnknowns = append(delta.RemovedUnknowns, u)
		}
	}

	return delta
}
