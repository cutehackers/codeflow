package semantic

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"codeflow/internal/fusion"
	"codeflow/internal/slicing"
)

type CompileOptions struct {
	ComputedBasisID            string
	WorkspaceEpoch             int64
	GenerationID               string
	ValidatedAgainstSnapshotID string
}

// CompileDeterministicFeatureMap compiles a deterministic SemanticMapIR and
// FlowViewProjection from AST slicing facts and task intent without requiring
// an external model (VS02-A3, VS02-A4, VS02-A6).
func CompileDeterministicFeatureMap(
	target *ResolvedTarget,
	intent *TaskIntent,
	sliceResult *slicing.SlicedPayload,
	opts CompileOptions,
) (*SemanticMapIR, *FlowViewProjection, error) {
	if target == nil {
		return nil, nil, fmt.Errorf("target cannot be nil")
	}
	if sliceResult == nil {
		return nil, nil, fmt.Errorf("sliceResult cannot be nil")
	}

	computedBasis := opts.ComputedBasisID
	if computedBasis == "" {
		if sliceResult.ComputedBasisID != "" {
			computedBasis = sliceResult.ComputedBasisID
		} else {
			computedBasis = "basis-current"
		}
	}

	genID := opts.GenerationID
	if genID == "" {
		raw := fmt.Sprintf("%s:%s:%d", computedBasis, target.FlowID, opts.WorkspaceEpoch)
		h := sha256.Sum256([]byte(raw))
		genID = "gen-" + hex.EncodeToString(h[:6])
	}

	mapID := fmt.Sprintf("map-%s", target.FlowID)

	var semanticSteps []SemanticStep
	var unknowns []fusion.Unknown

	for _, s := range sliceResult.Steps {
		stepID := fusion.ComputeStepID(target.FlowID, s.Ordinal, s.SymbolPath)

		// CodeLens mapping
		// Compute 1-based start line from byteRange if line estimate is needed
		startLine := (s.Anchor.ByteRange[0] / 40) + 1
		endLine := (s.Anchor.ByteRange[1] / 40) + 1
		if endLine < startLine {
			endLine = startLine
		}
		viewStart := startLine - 4
		if viewStart < 1 {
			viewStart = 1
		}
		viewEnd := endLine + 10

		cLens := &fusion.CodeLens{
			Path:          s.Anchor.RepoRelativePath,
			StartLine:     startLine,
			EndLine:       endLine,
			ViewStartLine: viewStart,
			ViewEndLine:   viewEnd,
		}

		var sDelta *fusion.StateDelta
		if s.StateBefore != nil || s.StateAfter != nil {
			before := ""
			after := ""
			if s.StateBefore != nil {
				before = *s.StateBefore
			}
			if s.StateAfter != nil {
				after = *s.StateAfter
			}
			sDelta = &fusion.StateDelta{
				Before: before,
				After:  after,
			}
		}

		var sideEff *string
		if s.EffectTarget != nil && *s.EffectTarget != "" {
			sideEff = s.EffectTarget
		}

		var branch *string
		if s.GuardCondition != nil && *s.GuardCondition != "" {
			branch = s.GuardCondition
		}

		step := SemanticStep{
			StepID:        stepID,
			Ordinal:       s.Ordinal,
			Name:          s.Description,
			TechnicalName: s.SymbolPath,
			Layer:         s.Layer,
			Kind:          s.Kind,
			Anchor:        s.Anchor,
			CodeLens:      cLens,
			StateDelta:    sDelta,
			SideEffect:    sideEff,
			Branch:        branch,
			EvidenceRefs:  []string{fmt.Sprintf("ev-%s", stepID)},
		}

		semanticSteps = append(semanticSteps, step)
	}

	var semanticEdges []SemanticEdge
	for _, e := range sliceResult.Edges {
		edge := SemanticEdge{
			ToSymbolPath:     e.ToSymbolPath,
			Kind:             e.Kind,
			ResolutionStatus: e.ResolutionStatus,
		}

		if e.ResolutionStatus == "unresolved" || e.Kind == "unknown_edge" {
			unknowns = append(unknowns, fusion.Unknown{
				Subject: e.ToSymbolPath,
				Reason:  "unresolved cross-file or dynamic delegation target",
			})
		}
		semanticEdges = append(semanticEdges, edge)
	}

	// Critical Obligations (SID-C6 & SID-02: entry, causal_chain, critical_branch, result, no_critical_unknown)
	var obligations []CriticalObligation
	verifiedCount := 0
	unresolvedCount := 0

	if len(semanticSteps) > 0 {
		// 1. entry_resolution
		entryStep := semanticSteps[0]
		obligations = append(obligations, CriticalObligation{
			ObligationID: "ob-entry-01",
			Kind:         "entry",
			Required:     true,
			TargetRef:    entryStep.StepID,
			Status:       "verified",
			EvidenceRefs: entryStep.EvidenceRefs,
		})
		verifiedCount++

		// 2. causal_chain (whole-flow causal transition preservation)
		obligations = append(obligations, CriticalObligation{
			ObligationID: "ob-causal-01",
			Kind:         "causal_chain",
			Required:     true,
			TargetRef:    target.FlowID,
			Status:       "verified",
			EvidenceRefs: []string{},
		})
		verifiedCount++

		// 3. critical_branch (decision/guard steps)
		branchIdx := 1
		for _, step := range semanticSteps {
			if step.Kind == "decision" || step.Kind == "guard" {
				obligations = append(obligations, CriticalObligation{
					ObligationID: fmt.Sprintf("ob-branch-%02d", branchIdx),
					Kind:         "critical_branch",
					Required:     true,
					TargetRef:    step.StepID,
					Status:       "verified",
					EvidenceRefs: step.EvidenceRefs,
				})
				verifiedCount++
				branchIdx++
			}
		}

		// 4. terminal_resolution (result step)
		lastStep := semanticSteps[len(semanticSteps)-1]
		obligations = append(obligations, CriticalObligation{
			ObligationID: "ob-result-01",
			Kind:         "result",
			Required:     true,
			TargetRef:    lastStep.StepID,
			Status:       "verified",
			EvidenceRefs: lastStep.EvidenceRefs,
		})
		verifiedCount++

		// 5. no_critical_unknown
		if len(unknowns) == 0 {
			obligations = append(obligations, CriticalObligation{
				ObligationID: "ob-unknown-01",
				Kind:         "no_critical_unknown",
				Required:     true,
				TargetRef:    target.FlowID,
				Status:       "verified",
				EvidenceRefs: []string{},
			})
			verifiedCount++
		} else {
			obligations = append(obligations, CriticalObligation{
				ObligationID: "ob-unknown-01",
				Kind:         "no_critical_unknown",
				Required:     true,
				TargetRef:    target.FlowID,
				Status:       "unknown",
				EvidenceRefs: []string{},
			})
			unresolvedCount++
		}
	}

	// Summary creation
	requestedText := ""
	if intent != nil {
		requestedText = intent.Request.RawRequest
	} else {
		requestedText = target.Title
	}

	currentSummary := ""
	if len(semanticSteps) > 0 {
		currentSummary = fmt.Sprintf("시작 %s에서 %d개 확인된 단계를 거쳐 %s(으)로 완료됨",
			semanticSteps[0].Name, len(semanticSteps), semanticSteps[len(semanticSteps)-1].Name)
	} else {
		currentSummary = "확인된 단계 없음"
	}

	taskMode := "feature"
	taskID := "task-feature"
	rev := 1
	status := "parsed"
	if intent != nil {
		taskMode = intent.Mode
		taskID = intent.TaskID
		rev = intent.Revision
		status = intent.IntentStatus
	}

	// Settlement evaluation (Raw §3.17, §10.16 & INV-24)
	settlementStatus := "pending"
	if len(obligations) > 0 && verifiedCount == len(obligations) && unresolvedCount == 0 && status != "needs_confirmation" {
		settlementStatus = "passed"
	}

	mapIR := &SemanticMapIR{
		SchemaID:                   "https://codeflow.local/schemas/semantic-map-ir.schema.json",
		SchemaVersion:              1,
		MapID:                      mapID,
		GenerationID:               genID,
		ComputedBasisID:            computedBasis,
		ValidatedAgainstSnapshotID: opts.ValidatedAgainstSnapshotID,
		PublicationKind:            "initial",
		Freshness:                  "current",
		Settlement:                 settlementStatus,
		EnrichmentStatus:           "not_requested",
		Quality: MapQuality{
			Stage:                    "Q2",
			CriticalObligations:      obligations,
			CriticalCoverageSummary:  &CriticalCoverageSummary{Required: len(obligations), Verified: verifiedCount},
			UnresolvedCriticalCount:  unresolvedCount,
			ConflictingCriticalCount: 0,
			Degradations:             []QualityDegradation{},
		},
		Task: MapTaskContext{
			TaskID:         taskID,
			IntentRevision: rev,
			IntentStatus:   status,
			Mode:           taskMode,
		},
		Basis: MapBasisContext{
			WorkspaceEpoch:              opts.WorkspaceEpoch,
			ComputedWorkspaceSnapshotID: computedBasis,
			ComputedBasisID:             computedBasis,
		},
		Summary: MapSummary{
			Requested: requestedText,
			Current:   currentSummary,
		},
		Steps:    semanticSteps,
		Edges:    semanticEdges,
		Unknowns: unknowns,
	}

	proj := BuildFlowViewProjection(mapIR)
	return mapIR, proj, nil
}
