package semantic

import (
	"errors"
	"fmt"
	"strings"
)

// ChangeImpactGraph mirrors schemas/change-impact-graph.schema.json (VS-06).
type ChangeImpactGraph struct {
	SchemaID                       string               `json:"schemaId"`
	SchemaVersion                  int                  `json:"schemaVersion"`
	ImpactGraphID                  string               `json:"impactGraphId"`
	Target                         ImpactTarget         `json:"target"`
	ComputedBasisID                string               `json:"computedBasisId"`
	GenerationID                   string               `json:"generationId"`
	ValidatedAgainstSnapshotID     string               `json:"validatedAgainstSnapshotId,omitempty"`
	Freshness                      string               `json:"freshness"` // current | last_verified
	DirectImpact                   DirectImpact         `json:"directImpact"`
	IndirectImpact                 IndirectImpact       `json:"indirectImpact"`
	UnresolvedBoundaries           []UnresolvedBoundary `json:"unresolvedBoundaries"`
	AdditionalExplorationAvailable bool                 `json:"additionalExplorationAvailable"`
	UnknownCount                   int                  `json:"unknownCount"`
	CoverageBoundary               *CoverageBoundary    `json:"coverageBoundary,omitempty"`
	Evidence                       []SemanticEvidence   `json:"evidence,omitempty"`
}

type ImpactTarget struct {
	SymbolID      string `json:"symbolId,omitempty"`
	ChangeBatchID string `json:"changeBatchId,omitempty"`
}

type DirectImpact struct {
	Callers         []ImpactCallerNode     `json:"callers"`
	StateMutations  []StateMutationImpact `json:"stateMutations"`
	ExternalEffects []ExternalEffectImpact `json:"externalEffects"`
	RelatedFlows    []RelatedFlowImpact   `json:"relatedFlows"`
	Tests           []TestImpact          `json:"tests"`
}

type IndirectImpact struct {
	Callers         []ImpactCallerNode     `json:"callers"`
	StateMutations  []StateMutationImpact `json:"stateMutations"`
	ExternalEffects []ExternalEffectImpact `json:"externalEffects"`
	RelatedFlows    []RelatedFlowImpact   `json:"relatedFlows"`
	Tests           []TestImpact          `json:"tests"`
	MaxDepth        int                   `json:"maxDepth"`
	TotalNodeCount  int                   `json:"totalNodeCount"`
	Bounded         bool                  `json:"bounded"`
}

type ImpactCallerNode struct {
	SymbolPath   string   `json:"symbolPath"`
	Name         string   `json:"name"`
	RelationKind string   `json:"relationKind"` // calls | overrides | instantiates
	FilePath     string   `json:"filePath,omitempty"`
	Depth        int      `json:"depth,omitempty"`
	EvidenceRefs []string `json:"evidenceRefs,omitempty"`
}

type StateMutationImpact struct {
	TargetState  string   `json:"targetState"`
	MutationKind string   `json:"mutationKind,omitempty"`
	EvidenceRefs []string `json:"evidenceRefs,omitempty"`
}

type ExternalEffectImpact struct {
	EffectKind   string   `json:"effectKind"` // api | database | message_queue | filesystem
	Target       string   `json:"target"`
	EvidenceRefs []string `json:"evidenceRefs,omitempty"`
}

type RelatedFlowImpact struct {
	FlowID string `json:"flowId"`
	Title  string `json:"title"`
}

type TestImpact struct {
	TestSymbolPath string `json:"testSymbolPath"`
	TestFile       string `json:"testFile"`
}

type UnresolvedBoundary struct {
	BoundaryType string `json:"boundaryType"` // unresolved_dynamic_caller | unsupported_capability | open_closure | missing_frontier
	Target       string `json:"target"`
	Description  string `json:"description"`
}

type ImpactOptions struct {
	MaxDepth int
}

// ComputeChangeImpact performs bounded caller reverse and effect/test forward traversal (VS06-A1..A8).
func ComputeChangeImpact(target ImpactTarget, mapIR *SemanticMapIR, opts ImpactOptions) (*ChangeImpactGraph, error) {
	if strings.TrimSpace(target.SymbolID) == "" && strings.TrimSpace(target.ChangeBatchID) == "" {
		return nil, errors.New("missing_precondition: either symbolId or changeBatchId must be provided")
	}

	maxDepth := opts.MaxDepth
	if maxDepth <= 0 || maxDepth > 5 {
		maxDepth = 2
	}

	graphID := "impact-" + target.SymbolID
	if target.SymbolID == "" {
		graphID = "impact-batch-" + target.ChangeBatchID
	}

	basisID := "basis-active"
	genID := "gen-active"
	freshness := "current"
	var coverage *CoverageBoundary

	if mapIR != nil {
		if mapIR.ComputedBasisID != "" {
			basisID = mapIR.ComputedBasisID
		}
		if mapIR.GenerationID != "" {
			genID = mapIR.GenerationID
		}
		coverage = mapIR.Coverage
	}

	direct := DirectImpact{
		Callers:         []ImpactCallerNode{},
		StateMutations:  []StateMutationImpact{},
		ExternalEffects: []ExternalEffectImpact{},
		RelatedFlows:    []RelatedFlowImpact{},
		Tests:           []TestImpact{},
	}

	indirect := IndirectImpact{
		Callers:         []ImpactCallerNode{},
		StateMutations:  []StateMutationImpact{},
		ExternalEffects: []ExternalEffectImpact{},
		RelatedFlows:    []RelatedFlowImpact{},
		Tests:           []TestImpact{},
		MaxDepth:        maxDepth,
		TotalNodeCount:  0,
		Bounded:         true,
	}

	var unresolved []UnresolvedBoundary
	var evidenceList []SemanticEvidence
	unknownCount := 0

	if mapIR != nil {
		targetSym := target.SymbolID
		var matchingSteps []SemanticStep
		for _, s := range mapIR.Steps {
			if s.TechnicalName == targetSym || s.Anchor.EnclosingSymbolPath == targetSym || strings.Contains(s.Anchor.EnclosingSymbolPath, targetSym) {
				matchingSteps = append(matchingSteps, s)
			}
		}

		// Traverse edges to find callers (reverse slice)
		for _, edge := range mapIR.Edges {
			for _, m := range matchingSteps {
				if edge.ToStepID == m.StepID || edge.ToSymbolPath == m.Anchor.EnclosingSymbolPath || edge.ToSymbolPath == m.TechnicalName {
					var fromStep *SemanticStep
					for _, s := range mapIR.Steps {
						if s.StepID == edge.FromStepID {
							fromStep = &s
							break
						}
					}
					callerPath := edge.ToSymbolPath
					callerName := edge.ToSymbolPath
					filePath := ""
					if fromStep != nil {
						callerPath = fromStep.Anchor.EnclosingSymbolPath
						callerName = fromStep.Name
						filePath = fromStep.Anchor.RepoRelativePath
					}
					direct.Callers = append(direct.Callers, ImpactCallerNode{
						SymbolPath:   callerPath,
						Name:         callerName,
						RelationKind: "calls",
						FilePath:     filePath,
						Depth:        1,
					})
				}
			}
		}

		// Forward effects: state mutations, external effects, tests, related flows
		for _, m := range matchingSteps {
			if m.StateDelta != nil {
				direct.StateMutations = append(direct.StateMutations, StateMutationImpact{
					TargetState:  fmt.Sprintf("%s → %s", m.StateDelta.Before, m.StateDelta.After),
					MutationKind: "transition",
					EvidenceRefs: m.EvidenceRefs,
				})
			}
			if m.SideEffect != nil && *m.SideEffect != "" {
				eff := *m.SideEffect
				effKind := "api"
				effTarget := eff
				if strings.HasPrefix(eff, "api:") {
					effKind = "api"
					effTarget = strings.TrimPrefix(eff, "api:")
				} else if strings.HasPrefix(eff, "db:") {
					effKind = "database"
					effTarget = strings.TrimPrefix(eff, "db:")
				}
				direct.ExternalEffects = append(direct.ExternalEffects, ExternalEffectImpact{
					EffectKind:   effKind,
					Target:       effTarget,
					EvidenceRefs: m.EvidenceRefs,
				})
			}
			for _, r := range m.Rules {
				if strings.HasPrefix(r, "test:") || strings.Contains(strings.ToLower(r), "test") {
					direct.Tests = append(direct.Tests, TestImpact{
						TestSymbolPath: r,
						TestFile:       m.Anchor.RepoRelativePath,
					})
				}
			}
			direct.RelatedFlows = append(direct.RelatedFlows, RelatedFlowImpact{
				FlowID: mapIR.MapID,
				Title:  mapIR.Summary.Requested,
			})
		}

		// Check for unresolved dynamic dispatch or unknowns in mapIR
		for _, unk := range mapIR.Unknowns {
			if strings.Contains(strings.ToLower(unk.Reason), "dynamic") || strings.Contains(strings.ToLower(unk.Reason), "reflection") {
				unresolved = append(unresolved, UnresolvedBoundary{
					BoundaryType: "unresolved_dynamic_caller",
					Target:       unk.Subject,
					Description:  unk.Reason,
				})
				unknownCount++
			}
		}

		evidenceList = append(evidenceList, mapIR.Evidence...)
	}

	if unknownCount == 0 && len(unresolved) > 0 {
		unknownCount = len(unresolved)
	}

	indirect.TotalNodeCount = len(direct.Callers) + len(direct.StateMutations) + len(direct.ExternalEffects)

	return &ChangeImpactGraph{
		SchemaID:                       "https://codeflow.local/schemas/change-impact-graph.schema.json",
		SchemaVersion:                  1,
		ImpactGraphID:                  graphID,
		Target:                         target,
		ComputedBasisID:                basisID,
		GenerationID:                   genID,
		Freshness:                      freshness,
		DirectImpact:                   direct,
		IndirectImpact:                 indirect,
		UnresolvedBoundaries:           unresolved,
		AdditionalExplorationAvailable: false,
		UnknownCount:                   unknownCount,
		CoverageBoundary:               coverage,
		Evidence:                       evidenceList,
	}, nil
}
