package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"codeflow/internal/flowview"
	"codeflow/internal/semantic"
	"codeflow/internal/workspace"
)

func (s *Server) getSnapshotEngine(absTarget string) (*workspace.SnapshotEngine, error) {
	if val, ok := s.liveServers.Load(absTarget); ok {
		if server, ok := val.(*flowview.Server); ok && server != nil && server.SnapshotEngine() != nil {
			return server.SnapshotEngine(), nil
		}
	}
	// All MCP workspace and analysis operations use the same live coordinator
	// as the HTTP surface. This lazy path prevents a query-before-edit from
	// creating a second snapshot lineage that can race the scheduler.
	coordinator, err := s.getLiveCoordinator(absTarget)
	if err != nil {
		return nil, err
	}
	return coordinator.SnapshotEngine(), nil
}

// getLiveCoordinator lazily creates the one production FlowView live
// coordinator for a repository. MCP and HTTP therefore share the snapshot
// engine, scheduler, event ledger, and checkpoint consumer.
func (s *Server) getLiveCoordinator(absTarget string) (*flowview.Server, error) {
	canonicalTarget, err := filepath.EvalSymlinks(absTarget)
	if err != nil {
		return nil, fmt.Errorf("resolve live coordinator workspace: %w", err)
	}
	absTarget = filepath.Clean(canonicalTarget)
	// Serialize lazy coordinator creation with Close. This prevents a request
	// racing shutdown from creating a server after shutdown has started.
	s.modelHostMu.Lock()
	defer s.modelHostMu.Unlock()
	if s.modelHostClosed || s.modelHostClosing.Load() {
		return nil, errMCPModelHostServerClosed
	}
	if val, ok := s.liveServers.Load(absTarget); ok {
		server, ok := val.(*flowview.Server)
		if !ok || server == nil {
			return nil, fmt.Errorf("invalid live coordinator for %s", absTarget)
		}
		return server, nil
	}
	var proposalStore semantic.ProposalStore
	if semantic.NewApprovalWorkspaceAuthorizer(absTarget).WorkspaceID() == s.approvalWorkspaceID {
		proposalStore = s.proposalStore
	}
	server, err := flowview.NewServer(flowview.Config{
		RepoRoot:                   absTarget,
		Port:                       0,
		ProposalStore:              proposalStore,
		RuntimeObservationProvider: s.cfg.RuntimeObservationProvider,
		RuntimeObservationStore:    s.cfg.RuntimeObservationStore,
		ObservationProvider:        s.cfg.ObservationProvider,
		ObservationStore:           s.cfg.ObservationStore,
		RuntimeExecutor:            s.cfg.RuntimeExecutor,
		OneShotExecutor:            s.cfg.OneShotExecutor,
		RuntimeExecutionSpec:       s.cfg.RuntimeExecutionSpec,
		RuntimeConsent:             s.cfg.RuntimeConsent,
		ReleaseThresholdDecisions:  s.cfg.ReleaseThresholdDecisions,
		ModelHostFactory:           s.coordinatorModelHostFactory(),
	})
	if err != nil {
		return nil, fmt.Errorf("start live coordinator: %w", err)
	}
	if s.modelHostClosing.Load() || s.modelHostClosed {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		shutdownErr := server.Shutdown(ctx)
		cancel()
		if shutdownErr != nil {
			return nil, errors.Join(errMCPModelHostServerClosed, shutdownErr)
		}
		return nil, errMCPModelHostServerClosed
	}
	server.Start()
	if s.modelHostClosing.Load() || s.modelHostClosed {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		shutdownErr := server.Shutdown(ctx)
		cancel()
		if shutdownErr != nil {
			return nil, errors.Join(errMCPModelHostServerClosed, shutdownErr)
		}
		return nil, errMCPModelHostServerClosed
	}
	actual, loaded := s.liveServers.LoadOrStore(absTarget, server)
	if s.modelHostClosing.Load() || s.modelHostClosed {
		if !loaded {
			s.liveServers.Delete(absTarget)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		shutdownErr := server.Shutdown(ctx)
		cancel()
		if shutdownErr != nil {
			return nil, errors.Join(errMCPModelHostServerClosed, shutdownErr)
		}
		return nil, errMCPModelHostServerClosed
	}
	if loaded {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = server.Shutdown(ctx)
		cancel()
		return actual.(*flowview.Server), nil
	}
	// Keep the compatibility open_review handle pointed at the same
	// coordinator when it is the first one created.
	s.fvMu.Lock()
	if s.fv == nil {
		s.fv = server
	}
	s.fvMu.Unlock()
	return server, nil
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

	coordinator, err := s.getLiveCoordinator(absTarget)
	if err != nil {
		return nil, fmt.Errorf("get snapshot engine: %w", err)
	}
	engine := coordinator.SnapshotEngine()

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

	source, _ := args["source"].(string)
	if source == "" {
		source = workspace.SourceAgentTransaction
	}

	coordinator, err := s.getLiveCoordinator(absTarget)
	if err != nil {
		return nil, fmt.Errorf("get snapshot engine: %w", err)
	}

	if rawChanges, ok := args["changes"].([]any); ok && len(rawChanges) > 0 {
		batchID, _ := args["batchId"].(string)
		if batchID == "" {
			return nil, fmt.Errorf("batchId is required for multi-file changes")
		}
		changes := make([]workspace.VersionedChange, 0, len(rawChanges))
		for index, raw := range rawChanges {
			item, ok := raw.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("changes[%d] must be an object", index)
			}
			kind, _ := item["kind"].(string)
			path, _ := item["path"].(string)
			oldPath, _ := item["oldPath"].(string)
			content, _ := item["content"].(string)
			contentID, _ := item["contentId"].(string)
			version := 0
			if value, ok := item["documentVersion"].(float64); ok {
				version = int(value)
			}
			changes = append(changes, workspace.VersionedChange{Kind: workspace.ChangeKind(kind), Path: path, OldPath: oldPath, Content: []byte(content), ContentID: contentID, DocumentVersion: version})
		}
		result, err := coordinator.SubmitVersionedChanges(ctx, workspace.VersionedChangeRequest{BatchID: batchID, Source: source, Changes: changes})
		if err != nil {
			return nil, fmt.Errorf("apply versioned change batch: %w", err)
		}
		return result, nil
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
	rev, snap, err := coordinator.SubmitVersionedEdit(ctx, workspace.EditRequest{Path: path, Content: []byte(contentStr), DocumentVersion: int(docVerFloat), Source: source})
	if err != nil {
		return nil, fmt.Errorf("apply versioned edit: %w", err)
	}
	return map[string]any{"revision": rev, "snapshot": snap}, nil
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

	manifest, ptr, err := st.ReadValidatedActiveProofManifest()
	if err != nil {
		return nil, fmt.Errorf("read validated current proof: %w", err)
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
	coordinator, err := s.getLiveCoordinator(absTarget)
	if err != nil {
		return nil, fmt.Errorf("get snapshot engine: %w", err)
	}
	engine := coordinator.SnapshotEngine()

	manifest, ptr, proofErr := st.ReadValidatedActiveProofManifest()
	if ptr == nil && proofErr == nil {
		return map[string]any{
			"status": "no_generation_published",
		}, nil
	}
	liveHead := engine.LiveHead()
	if proofErr != nil {
		// An invalid proof can never be reported as current. If a live head is
		// available, expose a measured non-current gap with the validation cause
		// so callers can repair the publication. A missing live head is a typed
		// unknown rather than a fabricated freshness claim.
		if liveHead == nil {
			return map[string]any{
				"code":      "invalid_current_proof",
				"message":   proofErr.Error(),
				"freshness": "unknown",
			}, nil
		}
		return map[string]any{
			"freshness":         "last_verified",
			"activity":          engine.CurrentActivity().Activity,
			"lastVerifiedGenId": ptr.GenerationID,
			"latestSnapshotId":  liveHead.SnapshotID,
			"affectedScope":     []string{},
			"analysisLagMs":     engine.CurrentActivity().AnalysisLagMs,
			"pendingRevisions":  engine.CurrentActivity().PendingRevisions,
			"intersectedCauses": []string{"invalid current proof: " + proofErr.Error()},
		}, nil
	}
	if manifest == nil || ptr == nil {
		return map[string]any{
			"code":      "invalid_current_proof",
			"freshness": "unknown",
			"message":   "validated proof is unavailable",
		}, nil
	}
	if liveHead == nil {
		return map[string]any{
			"code":      "no_live_head",
			"freshness": "unknown",
			"message":   "workspace has no measured live snapshot",
		}, nil
	}
	if liveHead.SnapshotID == ptr.ExpectedLiveHeadSnapshotID {
		return map[string]any{
			"freshness":    "current",
			"generationId": ptr.GenerationID,
			"settlement":   manifest.SettlementEvaluation.Gate,
		}, nil
	}

	delta, deltaErr := engine.ComputeDelta(ptr.ValidatedAgainstSnapshotID, liveHead.SnapshotID)
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
		"intersectedCauses": func() []string {
			if deltaErr != nil {
				return []string{"workspace delta unavailable: " + deltaErr.Error()}
			}
			return []string{"live head differs from validated proof"}
		}(),
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

	baseMap, baseOK := s.loadSemanticMap(absTarget, baseline)
	currMap, currOK := s.loadSemanticMap(absTarget, current)
	if !baseOK || !currOK {
		missing := baseline
		if !baseOK && !currOK {
			missing = baseline + " and " + current
		} else if !baseOK {
			missing = baseline
		} else {
			missing = current
		}
		return map[string]any{
			"code":    "missing_precondition",
			"message": fmt.Sprintf("semantic map artifact %q is not stored for this target", missing),
		}, nil
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

	basisID := ""
	if requested, ok := args["generation"].(string); ok && requested != "" {
		basisID = requested
	}
	lookupID := basisID
	if lookupID == "" {
		lookupID = "active"
	}
	currMap, ok := s.loadSemanticMap(absTarget, lookupID)
	if !ok {
		return map[string]any{
			"code":    "missing_precondition",
			"message": "a stored semantic map with requirement alignment is required",
		}, nil
	}
	if currMap.RequirementAlignment == nil {
		return map[string]any{
			"code":    "missing_precondition",
			"message": "stored semantic map has no requirement alignment artifact",
		}, nil
	}
	if basisID == "" {
		basisID = currMap.ComputedBasisID
	}
	return map[string]any{
		"requirementAlignment": currMap.RequirementAlignment,
		"computedBasisId":      basisID,
	}, nil
}

func (s *Server) handleInvestigateFailure(ctx context.Context, args map[string]any) (any, error) {
	return s.handleFailureV2Request(ctx, args)
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
			Content:    "current verified evidence unavailable",
			Verified:   false,
		},
	}

	return semantic.BuildEvidencePack(symbolPath, basisID, genID, items)
}

func (s *Server) handleSubmitSemanticApproval(ctx context.Context, args map[string]any) (any, error) {
	if err := s.checkAuth(args["token"]); err != nil {
		return nil, err
	}

	rawTarget := s.rawSemanticApprovalTarget(args["target"])
	access, err := s.authorizeSemanticApproval(ctx, rawTarget)
	if err != nil {
		return nil, err
	}
	draft, err := approvalCommandDraftFromArgs(args)
	if err != nil {
		return nil, err
	}
	targetRoot := access.Workspace().CanonicalRepoRoot()
	coordinator, err := s.getLiveCoordinator(targetRoot)
	if err != nil {
		return nil, semantic.ErrApprovalExecutionUnavailable
	}
	result, err := coordinator.SubmitSemanticApproval(ctx, access, draft)
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (s *Server) handleSubmitSemanticApprovalJSON(ctx context.Context, data []byte) (any, error) {
	envelope, err := semantic.ParseApprovalCommandDraftEnvelopeJSON(data)
	if err != nil {
		return nil, err
	}
	var token any
	if envelope.Token != nil {
		token = *envelope.Token
	}
	if err := s.checkAuth(token); err != nil {
		return nil, err
	}
	var targetArg any
	if envelope.Target != nil {
		targetArg = *envelope.Target
	}
	access, err := s.authorizeSemanticApproval(ctx, s.rawSemanticApprovalTarget(targetArg))
	if err != nil {
		return nil, err
	}
	targetRoot := access.Workspace().CanonicalRepoRoot()
	coordinator, err := s.getLiveCoordinator(targetRoot)
	if err != nil {
		return nil, semantic.ErrApprovalExecutionUnavailable
	}
	result, err := coordinator.SubmitSemanticApproval(ctx, access, envelope.Draft)
	if err != nil {
		return nil, err
	}
	return result, nil
}

func approvalCommandDraftFromArgs(args map[string]any) (semantic.ApprovalCommandDraft, error) {
	var zero semantic.ApprovalCommandDraft
	if args == nil {
		return zero, errors.New("invalid approval command draft")
	}
	allowed := map[string]struct{}{
		"target": {}, "token": {},
		"commandId": {}, "proposalId": {}, "evidencePackId": {}, "computedBasisId": {},
		"generationId": {}, "intentRevision": {}, "decision": {}, "editedText": {},
		"idempotencyKey": {}, "expectedApprovalVersion": {}, "expectedState": {},
		"predecessorApprovalId": {},
	}
	for key := range args {
		if _, ok := allowed[key]; !ok {
			return zero, errors.New("invalid approval command draft")
		}
	}
	stringValue := func(key string) (string, error) {
		raw, present := args[key]
		value, ok := raw.(string)
		if !present || !ok || strings.TrimSpace(value) == "" {
			return "", errors.New("invalid approval command draft")
		}
		return value, nil
	}
	commandID, err := stringValue("commandId")
	if err != nil {
		return zero, err
	}
	proposalID, err := stringValue("proposalId")
	if err != nil {
		return zero, err
	}
	packID, err := stringValue("evidencePackId")
	if err != nil {
		return zero, err
	}
	basisID, err := stringValue("computedBasisId")
	if err != nil {
		return zero, err
	}
	generationID, err := stringValue("generationId")
	if err != nil {
		return zero, err
	}
	decision, err := stringValue("decision")
	if err != nil {
		return zero, err
	}
	idempotencyKey, err := stringValue("idempotencyKey")
	if err != nil {
		return zero, err
	}
	expectedState, err := stringValue("expectedState")
	if err != nil {
		return zero, err
	}
	intentRevision, err := int64ApprovalArgument(args, "intentRevision")
	if err != nil {
		return zero, err
	}
	expectedVersion, err := int64ApprovalArgument(args, "expectedApprovalVersion")
	if err != nil {
		return zero, err
	}
	draft := semantic.ApprovalCommandDraft{
		CommandID: commandID, ProposalID: proposalID, EvidencePackID: packID,
		ComputedBasisID: basisID, GenerationID: generationID, IntentRevision: intentRevision,
		Decision: decision, IdempotencyKey: idempotencyKey, ExpectedApprovalVersion: expectedVersion,
		ExpectedState: expectedState,
	}
	for key, destination := range map[string]**string{
		"editedText":            &draft.EditedText,
		"predecessorApprovalId": &draft.PredecessorApprovalID,
	} {
		raw, present := args[key]
		if !present {
			continue
		}
		value, ok := raw.(string)
		if !ok {
			return zero, errors.New("invalid approval command draft")
		}
		copied := value
		*destination = &copied
	}
	return draft, nil
}

func int64ApprovalArgument(args map[string]any, key string) (int64, error) {
	raw, present := args[key]
	if !present {
		return 0, errors.New("invalid approval command draft")
	}
	switch value := raw.(type) {
	case int:
		return int64(value), nil
	case int8:
		return int64(value), nil
	case int16:
		return int64(value), nil
	case int32:
		return int64(value), nil
	case int64:
		return value, nil
	case uint:
		if uint64(value) > uint64(^uint64(0)>>1) {
			return 0, errors.New("invalid approval command draft")
		}
		return int64(value), nil
	case uint8:
		return int64(value), nil
	case uint16:
		return int64(value), nil
	case uint32:
		return int64(value), nil
	case uint64:
		if value > uint64(^uint64(0)>>1) {
			return 0, errors.New("invalid approval command draft")
		}
		return int64(value), nil
	case float64:
		if math.IsNaN(value) || math.IsInf(value, 0) || math.Trunc(value) != value || value < -float64(1<<63) || value >= float64(1<<63) {
			return 0, errors.New("invalid approval command draft")
		}
		return int64(value), nil
	case json.Number:
		parsed, err := strconv.ParseInt(string(value), 10, 64)
		if err != nil {
			return 0, errors.New("invalid approval command draft")
		}
		return parsed, nil
	default:
		return 0, errors.New("invalid approval command draft")
	}
}

// rawSemanticApprovalTarget preserves untrusted parent traversal segments for
// the approval authorization boundary. Other MCP tools intentionally retain
// resolveTarget's historical normalization behavior.
func (s *Server) rawSemanticApprovalTarget(targetArg any) string {
	configuredRoot := ""
	if s != nil {
		configuredRoot = s.cfg.RepoRoot
	}
	if configuredRoot != "" {
		if absRoot, err := filepath.Abs(configuredRoot); err == nil {
			configuredRoot = absRoot
		}
	}

	rawTarget, ok := targetArg.(string)
	if !ok || rawTarget == "" || rawTarget == "." {
		return configuredRoot
	}
	if filepath.IsAbs(rawTarget) || configuredRoot == "" {
		return rawTarget
	}
	return strings.TrimRight(configuredRoot, `/\`) + string(filepath.Separator) + rawTarget
}

// authorizeSemanticApproval authenticates the Core-owned local actor and
// binds the request to the server's exact configured workspace before any
// evidence or approval work is performed.
func (s *Server) authorizeSemanticApproval(ctx context.Context, target string) (semantic.ApprovalAccess, error) {
	if s == nil || s.approvalGate == nil {
		return semantic.ApprovalAccess{}, &semantic.ApprovalUnauthenticatedError{Reason: "approval authenticator is unavailable"}
	}
	access, err := s.approvalGate.AuthenticateAndAuthorize(ctx, s.approvalWorkspaceID, target)
	if err != nil {
		return semantic.ApprovalAccess{}, err
	}
	return access, nil
}

func (s *Server) handleExploreProjectDomains(ctx context.Context, args map[string]any) (any, error) {
	if err := s.checkAuth(args["token"]); err != nil {
		return nil, err
	}

	req, err := onboardingRequestFromArgs(args)
	if err != nil {
		return nil, err
	}
	targetRepo := s.resolveTarget(args["target"])
	absTarget, err := filepath.Abs(targetRepo)
	if err != nil {
		return nil, fmt.Errorf("resolve target: %w", err)
	}
	coordinator, err := s.getLiveCoordinator(absTarget)
	if err != nil {
		return nil, fmt.Errorf("get onboarding coordinator: %w", err)
	}
	return coordinator.ExploreOnboarding(ctx, req)
}

func (s *Server) handleValidateReleaseCapability(ctx context.Context, args map[string]any) (any, error) {
	if err := s.checkAuth(args["token"]); err != nil {
		return nil, err
	}

	var input semantic.ReleaseEvaluationInput
	if raw, present := args["evaluation"]; present && raw != nil {
		data, err := json.Marshal(raw)
		if err != nil {
			return nil, fmt.Errorf("encode release evaluation input: %w", err)
		}
		decoder := json.NewDecoder(strings.NewReader(string(data)))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil {
			return nil, fmt.Errorf("decode release evaluation input: %w", err)
		}
	}

	return semantic.EvaluateReleaseCapabilityWithThresholdDecisions(input, s.cfg.ReleaseThresholdDecisions)
}
