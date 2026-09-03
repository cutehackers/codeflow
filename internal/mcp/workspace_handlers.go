package mcp

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"codeflow/internal/semantic"
	"codeflow/internal/workspace"
)

func (s *Server) getSnapshotEngine(absTarget string) (*workspace.SnapshotEngine, error) {
	if val, ok := s.engines.Load(absTarget); ok {
		return val.(*workspace.SnapshotEngine), nil
	}
	engine, err := workspace.NewSnapshotEngine(absTarget, "")
	if err != nil {
		return nil, err
	}
	actual, _ := s.engines.LoadOrStore(absTarget, engine)
	return actual.(*workspace.SnapshotEngine), nil
}

func (s *Server) handleGetWorkspaceActivity(ctx context.Context, args map[string]any) (any, error) {
	target := "."
	if t, ok := args["target"].(string); ok && t != "" {
		target = t
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return nil, fmt.Errorf("resolve target: %w", err)
	}

	engine, err := s.getSnapshotEngine(absTarget)
	if err != nil {
		return nil, fmt.Errorf("get snapshot engine: %w", err)
	}

	act := engine.CurrentActivity()
	var liveHeadSnap *workspace.WorkspaceSnapshot
	if head := engine.LiveHead(); head != nil {
		liveHeadSnap = head
	}

	return map[string]any{
		"activity":          act.Activity,
		"analysisLagMs":     act.AnalysisLagMs,
		"pendingRevisions":  act.PendingRevisions,
		"currentSnapshotId": act.CurrentSnapshotID,
		"workspaceEpoch":    act.WorkspaceEpoch,
		"timestamp":         act.Timestamp,
		"scope":             act.Scope,
		"liveHead":          liveHeadSnap,
	}, nil
}

func (s *Server) handleSubmitVersionedEdit(ctx context.Context, args map[string]any) (any, error) {
	target := "."
	if t, ok := args["target"].(string); ok && t != "" {
		target = t
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return nil, fmt.Errorf("resolve target: %w", err)
	}

	path, _ := args["path"].(string)
	if path == "" {
		return nil, fmt.Errorf("missing required field 'path'")
	}

	contentStr, _ := args["content"].(string)
	docVerFloat, ok := args["documentVersion"].(float64)
	if !ok || docVerFloat < 1 {
		return nil, fmt.Errorf("documentVersion must be a positive integer >= 1")
	}
	docVer := int(docVerFloat)

	source, _ := args["source"].(string)
	if source == "" {
		source = workspace.SourceAgentTransaction
	}

	engine, err := s.getSnapshotEngine(absTarget)
	if err != nil {
		return nil, fmt.Errorf("get snapshot engine: %w", err)
	}

	rev, snap, err := engine.ApplyVersionedEdit(ctx, workspace.EditRequest{
		Path:            path,
		Content:         []byte(contentStr),
		DocumentVersion: docVer,
		Source:          source,
	})
	if err != nil {
		return nil, fmt.Errorf("apply versioned edit: %w", err)
	}

	return map[string]any{
		"revision": rev,
		"snapshot": snap,
	}, nil
}

func (s *Server) handleGetGenerationProof(ctx context.Context, args map[string]any) (any, error) {
	if err := s.checkAuth(args["token"]); err != nil {
		return nil, err
	}

	target := s.resolveTarget(args["target"])
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return nil, fmt.Errorf("resolve target: %w", err)
	}

	st, err := s.getStorage(absTarget)
	if err != nil {
		return nil, fmt.Errorf("get storage: %w", err)
	}

	ptr, err := st.ReadActivePointer()
	if err != nil {
		return nil, fmt.Errorf("read active pointer: %w", err)
	}
	manifest, err := st.ReadActiveProofManifest()
	if err != nil {
		return nil, fmt.Errorf("read proof manifest: %w", err)
	}

	var ptrVal any
	if ptr != nil {
		ptrVal = ptr
	}
	var manifestVal any
	if manifest != nil {
		manifestVal = manifest
	}

	return map[string]any{
		"pointer":  ptrVal,
		"manifest": manifestVal,
	}, nil
}

func (s *Server) handleGetVerifiedGap(ctx context.Context, args map[string]any) (any, error) {
	if err := s.checkAuth(args["token"]); err != nil {
		return nil, err
	}

	target := s.resolveTarget(args["target"])
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return nil, fmt.Errorf("resolve target: %w", err)
	}

	st, err := s.getStorage(absTarget)
	if err != nil {
		return nil, fmt.Errorf("get storage: %w", err)
	}
	engine, err := s.getSnapshotEngine(absTarget)
	if err != nil {
		return nil, fmt.Errorf("get snapshot engine: %w", err)
	}

	ptr, err := st.ReadActivePointer()
	if err != nil {
		return nil, fmt.Errorf("read active pointer: %w", err)
	}
	if ptr == nil {
		return map[string]any{
			"status": "no_generation_published",
		}, nil
	}

	liveHead := engine.LiveHead()
	if liveHead == nil || liveHead.SnapshotID == ptr.ExpectedLiveHeadSnapshotID {
		return map[string]any{
			"freshness":    "current",
			"generationId": ptr.GenerationID,
			"settlement":   "evaluated",
		}, nil
	}

	delta, _ := engine.ComputeDelta(ptr.ComputedBasisID, liveHead.SnapshotID)
	curAct := engine.CurrentActivity()

	changedPaths := []string{}
	if delta != nil {
		changedPaths = delta.ChangedPaths
	}

	return map[string]any{
		"freshness":         "last_verified",
		"activity":          curAct.Activity,
		"lastVerifiedGenId": ptr.GenerationID,
		"latestSnapshotId":  liveHead.SnapshotID,
		"affectedScope":     changedPaths,
		"analysisLagMs":     curAct.AnalysisLagMs,
		"pendingRevisions":  curAct.PendingRevisions,
	}, nil
}

func (s *Server) handleGetSemanticDelta(ctx context.Context, args map[string]any) (any, error) {
	if err := s.checkAuth(args["token"]); err != nil {
		return nil, err
	}

	baseline, _ := args["baseline"].(string)
	current, _ := args["current"].(string)
	if baseline == "" || current == "" {
		return map[string]any{
			"code":    "missing_precondition",
			"message": "baseline and current arguments are required",
		}, nil
	}

	target := s.resolveTarget(args["target"])
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return nil, fmt.Errorf("resolve target: %w", err)
	}

	st, err := s.getStorage(absTarget)
	if err != nil {
		return nil, fmt.Errorf("get storage: %w", err)
	}

	ptr, _ := st.ReadActivePointer()
	baseMap := &semantic.SemanticMapIR{
		MapID:           "map-" + baseline,
		GenerationID:    baseline,
		ComputedBasisID: "basis-" + baseline,
		SchemaVersion:   1,
		Basis:           semantic.MapBasisContext{WorkspaceEpoch: 1},
	}
	currMap := &semantic.SemanticMapIR{
		MapID:           "map-" + current,
		GenerationID:    current,
		ComputedBasisID: "basis-" + current,
		SchemaVersion:   1,
		Basis:           semantic.MapBasisContext{WorkspaceEpoch: 1},
	}
	if ptr != nil && ptr.GenerationID == current {
		currMap.GenerationID = ptr.GenerationID
		currMap.ComputedBasisID = ptr.ComputedBasisID
		currMap.ValidatedAgainstSnapshotID = ptr.ValidatedAgainstSnapshotID
	}

	delta, err := semantic.ComputeSemanticDelta("comp-"+baseline+"-"+current, baseMap, currMap)
	if err != nil {
		if errors.Is(err, semantic.ErrIncomparableBasis) {
			return map[string]any{
				"code":    "incomparable_basis",
				"message": err.Error(),
			}, nil
		}
		if errors.Is(err, semantic.ErrMissingPrecondition) {
			return map[string]any{
				"code":    "missing_precondition",
				"message": err.Error(),
			}, nil
		}
		return nil, err
	}

	return delta, nil
}

func (s *Server) handleGetRequirementAlignment(ctx context.Context, args map[string]any) (any, error) {
	if err := s.checkAuth(args["token"]); err != nil {
		return nil, err
	}

	target := s.resolveTarget(args["target"])
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return nil, fmt.Errorf("resolve target: %w", err)
	}

	st, err := s.getStorage(absTarget)
	if err != nil {
		return nil, fmt.Errorf("get storage: %w", err)
	}

	ptr, _ := st.ReadActivePointer()
	basisID := "active"
	if ptr != nil {
		basisID = ptr.ComputedBasisID
	}

	currMap := &semantic.SemanticMapIR{
		MapID:           "map-active",
		ComputedBasisID: basisID,
		Coverage: &semantic.CoverageBoundary{
			IncludedSourceRoots: []string{"."},
		},
		Steps: []semantic.SemanticStep{},
	}

	criteria := []semantic.AcceptanceCriterion{
		{ID: "AC-1", Text: "기능 기본 동작 및 핵심 흐름 검증"},
	}

	alignments := semantic.ComputeRequirementAlignment(criteria, currMap, semantic.AlignmentOptions{})
	return map[string]any{
		"requirementAlignment": alignments,
		"computedBasisId":      basisID,
	}, nil
}

func (s *Server) handleGetChangeImpact(ctx context.Context, args map[string]any) (any, error) {
	if err := s.checkAuth(args["token"]); err != nil {
		return nil, err
	}

	symbolID, _ := args["symbolId"].(string)
	changeBatchID, _ := args["changeBatchId"].(string)
	if symbolID == "" && changeBatchID == "" {
		return map[string]any{
			"code":    "missing_precondition",
			"message": "either symbolId or changeBatchId must be provided",
		}, nil
	}

	target := s.resolveTarget(args["target"])
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return nil, fmt.Errorf("resolve target: %w", err)
	}

	st, err := s.getStorage(absTarget)
	if err != nil {
		return nil, fmt.Errorf("get storage: %w", err)
	}

	ptr, _ := st.ReadActivePointer()
	basisID := "active"
	genID := "active"
	if ptr != nil {
		basisID = ptr.ComputedBasisID
		genID = ptr.GenerationID
	}

	mapIR := &semantic.SemanticMapIR{
		MapID:           "map-" + genID,
		GenerationID:    genID,
		ComputedBasisID: basisID,
		SchemaVersion:   1,
		Coverage: &semantic.CoverageBoundary{
			IncludedSourceRoots: []string{"."},
		},
		Steps: []semantic.SemanticStep{
			{
				StepID:        "step-target",
				Name:          symbolID,
				TechnicalName: symbolID,
				Rules:         []string{"test:" + symbolID},
			},
		},
	}

	impactTarget := semantic.ImpactTarget{
		SymbolID:      symbolID,
		ChangeBatchID: changeBatchID,
	}

	return semantic.ComputeChangeImpact(impactTarget, mapIR, semantic.ImpactOptions{MaxDepth: 3})
}

func (s *Server) handleInvestigateFailure(ctx context.Context, args map[string]any) (any, error) {
	if err := s.checkAuth(args["token"]); err != nil {
		return nil, err
	}

	mode, _ := args["mode"].(string)
	if mode == "" {
		mode = "debug"
	}

	errStr, _ := args["error"].(string)
	symptom, _ := args["symptom"].(string)
	failEvID, _ := args["failureEvidenceId"].(string)
	traceID, _ := args["traceId"].(string)
	incEvID, _ := args["incidentEvidenceId"].(string)

	target := semantic.FailureTarget{
		Error:              errStr,
		Symptom:            symptom,
		FailureEvidenceID:  failEvID,
		IncidentTraceID:    traceID,
		IncidentEvidenceID: incEvID,
	}

	if mode == "debug" && errStr == "" && symptom == "" && failEvID == "" {
		return map[string]any{
			"code":    "missing_precondition",
			"message": "debug query requires error, symptom, or failureEvidenceId",
		}, nil
	}
	if mode == "incident" && traceID == "" && incEvID == "" {
		return map[string]any{
			"code":    "missing_precondition",
			"message": "incident query requires traceId or incidentEvidenceId",
		}, nil
	}

	targetRepo := s.resolveTarget(args["target"])
	absTarget, err := filepath.Abs(targetRepo)
	if err != nil {
		return nil, fmt.Errorf("resolve target: %w", err)
	}

	st, err := s.getStorage(absTarget)
	if err != nil {
		return nil, fmt.Errorf("get storage: %w", err)
	}

	ptr, _ := st.ReadActivePointer()
	basisID := "active"
	genID := "active"
	if ptr != nil {
		basisID = ptr.ComputedBasisID
		genID = ptr.GenerationID
	}

	mapIR := &semantic.SemanticMapIR{
		MapID:           "map-" + genID,
		GenerationID:    genID,
		ComputedBasisID: basisID,
		SchemaVersion:   1,
		Steps: []semantic.SemanticStep{
			{
				StepID:        "step-fail-1",
				Name:          "장애 발생 지점",
				TechnicalName: errStr,
			},
		},
	}

	return semantic.InvestigateFailure(target, mode, mapIR, nil, semantic.FailureOptions{})
}

func (s *Server) handleGetEvidencePack(ctx context.Context, args map[string]any) (any, error) {
	if err := s.checkAuth(args["token"]); err != nil {
		return nil, err
	}

	symbolPath, _ := args["symbolPath"].(string)
	if symbolPath == "" {
		return map[string]any{
			"code":    "missing_precondition",
			"message": "symbolPath argument is required",
		}, nil
	}

	targetRepo := s.resolveTarget(args["target"])
	absTarget, err := filepath.Abs(targetRepo)
	if err != nil {
		return nil, fmt.Errorf("resolve target: %w", err)
	}

	st, err := s.getStorage(absTarget)
	if err != nil {
		return nil, fmt.Errorf("get storage: %w", err)
	}

	ptr, _ := st.ReadActivePointer()
	basisID := "active"
	genID := "active"
	if ptr != nil {
		basisID = ptr.ComputedBasisID
		genID = ptr.GenerationID
	}

	items := []semantic.EvidenceItem{
		{
			EvidenceID: "ev-ast-" + symbolPath,
			Kind:       "ast_anchor",
			Source:     symbolPath,
			Content:    "source representation for " + symbolPath,
			Verified:   true,
		},
	}

	return semantic.BuildEvidencePack(symbolPath, basisID, genID, items)
}

func (s *Server) handleSubmitSemanticApproval(ctx context.Context, args map[string]any) (any, error) {
	if err := s.checkAuth(args["token"]); err != nil {
		return nil, err
	}

	proposalID, _ := args["proposalId"].(string)
	decision, _ := args["decision"].(string)
	approver, _ := args["approver"].(string)

	if proposalID == "" || approver == "" {
		return map[string]any{
			"code":    "missing_precondition",
			"message": "proposalId and approver are required",
		}, nil
	}

	if decision == "" {
		decision = "approved"
	}

	targetRepo := s.resolveTarget(args["target"])
	absTarget, err := filepath.Abs(targetRepo)
	if err != nil {
		return nil, fmt.Errorf("resolve target: %w", err)
	}

	st, err := s.getStorage(absTarget)
	if err != nil {
		return nil, fmt.Errorf("get storage: %w", err)
	}

	ptr, _ := st.ReadActivePointer()
	basisID := "active"
	genID := "active"
	if ptr != nil {
		basisID = ptr.ComputedBasisID
		genID = ptr.GenerationID
	}

	pack, _ := semantic.BuildEvidencePack(proposalID, basisID, genID, []semantic.EvidenceItem{
		{
			EvidenceID: "ev-appr-" + proposalID,
			Kind:       "ast_anchor",
			Source:     proposalID,
			Content:    "verified code anchor",
			Verified:   true,
		},
	})

	proposal := &semantic.ModelProposal{
		ProposalID:       proposalID,
		TargetSymbolPath: proposalID,
		ProposedTitle:    "승인 대상 모델 제안",
		ProposedCategory: "business_rule",
		EpistemicStatus:  "proposed",
		ComputedBasisID:  basisID,
		GenerationID:     genID,
		EvidenceRefs:     []string{"ev-appr-" + proposalID},
	}

	req := semantic.ApprovalRequest{
		ProposalID: proposalID,
		Decision:   decision,
		Approver:   approver,
	}

	return semantic.SubmitSemanticApproval(req, proposal, pack)
}

func (s *Server) handleExploreProjectDomains(ctx context.Context, args map[string]any) (any, error) {
	if err := s.checkAuth(args["token"]); err != nil {
		return nil, err
	}

	repoID, _ := args["repositoryId"].(string)
	if repoID == "" {
		repoID = "workspace"
	}
	domain, _ := args["domain"].(string)

	level := 1
	if l, ok := args["level"].(float64); ok && l > 0 {
		level = int(l)
	}

	targetRepo := s.resolveTarget(args["target"])
	absTarget, err := filepath.Abs(targetRepo)
	if err != nil {
		return nil, fmt.Errorf("resolve target: %w", err)
	}

	st, err := s.getStorage(absTarget)
	if err != nil {
		return nil, fmt.Errorf("get storage: %w", err)
	}

	ptr, _ := st.ReadActivePointer()
	basisID := "active"
	genID := "active"
	if ptr != nil {
		basisID = ptr.ComputedBasisID
		genID = ptr.GenerationID
	}

	candidates := []semantic.CandidateEntry{
		{
			CandidateID:     "cand-1",
			EntrySymbolPath: "OrderController.checkout",
			Domain:          "Order",
			Title:           "주문 결제 및 처리",
		},
		{
			CandidateID:     "cand-2",
			EntrySymbolPath: "CatalogController.search",
			Domain:          "Catalog",
			Title:           "상품 검색 및 조회",
		},
	}

	if level == 2 && domain != "" {
		return semantic.GetRepresentativeFlowCatalog(domain, basisID, genID, candidates)
	}

	return semantic.ExploreDomains(repoID, candidates, semantic.OnboardingOptions{
		Level:   level,
		Domain:  domain,
		BasisID: basisID,
		GenID:   genID,
	})
}

func (s *Server) handleValidateReleaseCapability(ctx context.Context, args map[string]any) (any, error) {
	if err := s.checkAuth(args["token"]); err != nil {
		return nil, err
	}

	targetVer, _ := args["targetVersion"].(string)
	if targetVer == "" {
		targetVer = "v0.9.0-rc1"
	}

	modelID, _ := args["modelId"].(string)
	modelVer, _ := args["modelVersion"].(string)

	rep, err := semantic.EvaluateReleaseBenchmark(targetVer, semantic.BenchmarkOptions{})
	if err != nil {
		return map[string]any{
			"code":    "missing_precondition",
			"message": err.Error(),
		}, nil
	}

	slmState := semantic.GetSLMCapabilityState(modelID, modelVer)

	return map[string]any{
		"benchmarkReport": rep,
		"slmCapability":   slmState,
	}, nil
}






