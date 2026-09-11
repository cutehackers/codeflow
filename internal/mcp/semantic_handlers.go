package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"codeflow/internal/contractharness"
	"codeflow/internal/flowview"
	"codeflow/internal/protocol"
	"codeflow/internal/semantic"
)

// captureAnalysisSnapshot establishes one VS-01 lease for the complete MCP
// analysis request. The lease is retained until every adapter call and
// evidence extraction has finished.
func (s *Server) captureAnalysisSnapshot(ctx context.Context, targetRoot string) (protocol.Snapshot, func(), error) {
	engine, err := s.getSnapshotEngine(targetRoot)
	if err != nil {
		return protocol.Snapshot{}, nil, fmt.Errorf("get snapshot engine: %w", err)
	}
	// Reconcile at the request boundary so a persisted head from an earlier
	// process cannot silently become the analysis basis for the current tree.
	head, err := engine.ReconcileIfChanged(ctx, nil)
	if err != nil {
		return protocol.Snapshot{}, nil, fmt.Errorf("capture workspace snapshot: %w", err)
	}
	lease, err := engine.SnapshotVFS(head.SnapshotID)
	if err != nil {
		return protocol.Snapshot{}, nil, fmt.Errorf("retain workspace snapshot: %w", err)
	}
	snapshot, err := protocol.SnapshotFromLease(lease)
	if err != nil {
		_ = lease.Close()
		return protocol.Snapshot{}, nil, fmt.Errorf("convert workspace snapshot: %w", err)
	}
	return snapshot, func() { _ = lease.Close() }, nil
}

func (s *Server) handleQueryTaskView(ctx context.Context, args map[string]any) (any, error) {
	if err := s.checkAuth(args["token"]); err != nil {
		return nil, coreFlowError("unauthorized", err.Error(), nil, false)
	}

	targetRoot := s.resolveTarget(args["target"])

	rawQuery, ok := args["query"]
	if !ok || rawQuery == nil {
		return nil, coreFlowError(semantic.ErrCodeMissingPrecondition, "missing 'query' argument", nil, false)
	}
	// Failure and incident investigation use the explicit VS-06 v2 query
	// boundary. Do this before the legacy TaskViewQuery decoder because the
	// legacy schema has permissive debug/incident shapes and must never create
	// a synthetic map or observation.
	if failureModeQuery(rawQuery) {
		failureArgs := map[string]any{
			"query":  rawQuery,
			"target": targetRoot,
			"token":  args["token"],
		}
		for _, key := range []string{"semanticMap", "map", "proof", "proofManifest", "generationProof", "pointer", "activePointer", "runtime", "runtimeConsent", "consent", "executionId", "nonce", "operation"} {
			if value, present := args[key]; present {
				failureArgs[key] = value
			}
		}
		res, err := s.handleFailureV2Request(ctx, failureArgs)
		if err != nil {
			return nil, coreFlowError("failure_error", err.Error(), nil, false)
		}
		return res, nil
	}
	// Onboarding has a v2 identity contract that is richer than the historical
	// TaskViewQuery onboarding block.  Route map-shaped onboarding queries before
	// the legacy decoder so exact basis/proof fields are not rejected as unknown
	// properties or silently discarded.  The helper still validates the legacy
	// envelope after removing only the explicitly supported v2 additions.
	if onboardingQuery, ok := onboardingQueryObject(rawQuery); ok && isOnboardingQuery(onboardingQuery) {
		res, err := s.handleOnboardingTaskViewQuery(ctx, targetRoot, args["token"], onboardingQuery)
		if err != nil {
			return nil, coreFlowError("onboarding_error", err.Error(), nil, false)
		}
		return res, nil
	}

	queryBytes, err := json.Marshal(rawQuery)
	if err != nil {
		return nil, coreFlowError(semantic.ErrCodeMissingPrecondition, fmt.Sprintf("invalid query JSON: %v", err), nil, false)
	}

	if err := contractharness.ValidateTaskViewQuery(queryBytes); err != nil {
		return nil, coreFlowError(semantic.ErrCodeMissingPrecondition, fmt.Sprintf("query schema/precondition validation failed: %v", err), nil, false)
	}

	var query semantic.TaskViewQuery
	if err := json.Unmarshal(queryBytes, &query); err != nil {
		return nil, coreFlowError(semantic.ErrCodeMissingPrecondition, fmt.Sprintf("unmarshal query: %v", err), nil, false)
	}
	var liveReviewURL func(request, entrySymbol string) (string, error)
	if query.Mode == "feature" && query.Feature != nil {
		coordinator, err := s.getLiveCoordinator(targetRoot)
		if err != nil {
			return nil, coreFlowError("live_coordinator_error", err.Error(), nil, false)
		}
		if err := coordinator.RememberTaskQuery(&query, query.Feature.Request); err != nil {
			return nil, coreFlowError(semantic.ErrCodeMissingPrecondition, err.Error(), nil, false)
		}
		liveReviewURL = func(request, entrySymbol string) (string, error) {
			viewURL, err := url.Parse(coordinator.URL())
			if err != nil {
				return "", fmt.Errorf("build FlowView URL: %w", err)
			}
			params := viewURL.Query()
			viewURL.Path = "/"
			viewURL.RawQuery = params.Encode()
			return viewURL.String(), nil
		}
	}

	if query.Mode == "impact" {
		symID := ""
		batchID := ""
		basisID := ""
		generationID := ""
		freshness := ""
		maxDepth := 0
		maxNodes := 0
		var relationKinds []string
		if query.Impact != nil {
			symID = query.Impact.SymbolID
			batchID = query.Impact.ChangeBatchID
			basisID = query.Impact.ComputedBasisID
			generationID = query.Impact.GenerationID
			freshness = query.Impact.Freshness
			maxDepth = query.Impact.MaxDepth
			maxNodes = query.Impact.MaxNodes
			relationKinds = append([]string(nil), query.Impact.RelationKinds...)
		}
		res, err := s.handleGetChangeImpact(ctx, map[string]any{
			"symbolId":        symID,
			"changeBatchId":   batchID,
			"computedBasisId": basisID,
			"generationId":    generationID,
			"freshness":       freshness,
			"maxDepth":        maxDepth,
			"maxNodes":        maxNodes,
			"relationKinds":   relationKinds,
			"target":          targetRoot,
			"token":           args["token"],
		})
		if err != nil {
			return nil, coreFlowError("impact_error", err.Error(), nil, false)
		}
		return res, nil
	}

	if query.Mode == "debug" || query.Mode == "incident" {
		errStr := ""
		symptom := ""
		failEvID := ""
		traceID := ""
		incEvID := ""
		if query.Debug != nil {
			errStr = query.Debug.Error
			symptom = query.Debug.Symptom
			failEvID = query.Debug.FailureEvidenceID
		}
		if query.Incident != nil {
			traceID = query.Incident.TraceID
			incEvID = query.Incident.IncidentEvidenceID
		}
		res, err := s.handleInvestigateFailure(ctx, map[string]any{
			"mode":               query.Mode,
			"error":              errStr,
			"symptom":            symptom,
			"failureEvidenceId":  failEvID,
			"traceId":            traceID,
			"incidentEvidenceId": incEvID,
			"target":             targetRoot,
		})
		if err != nil {
			return nil, coreFlowError("failure_error", err.Error(), nil, false)
		}
		return res, nil
	}

	if query.Mode == "onboarding" {
		if query.Onboarding == nil || query.Onboarding.RepositoryID == "" {
			return nil, coreFlowError(semantic.ErrCodeMissingPrecondition, "onboarding requires repositoryId", nil, false)
		}
		onboardingArgs := map[string]any{
			"repositoryId": query.Onboarding.RepositoryID,
			"domain":       query.Onboarding.Domain,
			"target":       targetRoot,
			"token":        args["token"],
		}
		// The normalized TaskViewQuery keeps the portable basis selector in the
		// common envelope. Translate it into the public onboarding identity while
		// preserving an explicit caller-provided v2 field when present.
		if query.Common != nil && query.Common.BasisSelector != nil {
			sel := query.Common.BasisSelector
			switch sel.Kind {
			case "active":
				onboardingArgs["freshness"] = "current"
			case "generation", "workspaceSnapshot":
				onboardingArgs["freshness"] = "historical"
				if sel.ID != "" {
					onboardingArgs["generationId"] = sel.ID
				}
			}
		}
		if raw, ok := rawQuery.(map[string]any); ok {
			if rawOnboarding, ok := raw["onboarding"].(map[string]any); ok {
				for _, key := range []string{"freshness", "computedBasisId", "basisId", "generationId", "genId", "validatedAgainstSnapshotId", "snapshotId", "maxVisibleCoreSteps", "level"} {
					if value, exists := rawOnboarding[key]; exists {
						onboardingArgs[key] = value
					}
				}
			}
			if rawCommon, ok := raw["common"].(map[string]any); ok {
				if rawSelector, ok := rawCommon["basisSelector"].(map[string]any); ok {
					if value, exists := rawSelector["kind"]; exists {
						switch fmt.Sprint(value) {
						case "active":
							onboardingArgs["freshness"] = "current"
						case "generation", "workspaceSnapshot":
							onboardingArgs["freshness"] = "historical"
						}
					}
					if value, exists := rawSelector["id"]; exists && onboardingArgs["generationId"] == nil {
						onboardingArgs["generationId"] = value
					}
				}
			}
		}
		res, err := s.handleExploreProjectDomains(ctx, onboardingArgs)
		if err != nil {
			return nil, coreFlowError("onboarding_error", err.Error(), nil, false)
		}
		return res, nil
	}

	snapshot, releaseSnapshot, err := s.captureAnalysisSnapshot(ctx, targetRoot)
	if err != nil {
		return nil, coreFlowError("snapshot_error", err.Error(), nil, false)
	}
	defer releaseSnapshot()

	_, harvester, slicer, err := s.getPoolAndRunnersForSnapshot(ctx, targetRoot, "", &snapshot)
	if err != nil {
		return nil, coreFlowError("adapter_error", fmt.Sprintf("adapter error: %v", err), nil, false)
	}

	candidates, err := harvester.RunWithSnapshot(ctx, targetRoot, snapshot)
	if err != nil {
		return nil, coreFlowError("harvest_failed", fmt.Sprintf("harvest candidates: %v", err), nil, false)
	}

	resolved, err := semantic.ResolveFeatureQueryTarget(&query, candidates)
	if err != nil {
		var qErr *semantic.QueryError
		if errors.As(err, &qErr) {
			var details []map[string]any
			for _, c := range qErr.CandidateTargets {
				details = append(details, map[string]any{"candidate": c})
			}
			return nil, coreFlowError(qErr.Code, qErr.Message, details, false)
		}
		return nil, coreFlowError(semantic.ErrCodeMissingPrecondition, err.Error(), nil, false)
	}

	slicePayload, err := slicer.SliceWithSnapshot(ctx, targetRoot, resolved.CandidateID, resolved.EntrySymbolPath, nil, snapshot)
	if err != nil {
		return nil, coreFlowError("slice_failed", fmt.Sprintf("slice target %s: %v", resolved.EntrySymbolPath, err), nil, false)
	}

	reqText := ""
	if query.Feature != nil && query.Feature.Request != "" {
		reqText = query.Feature.Request
	} else {
		reqText = resolved.Title
	}

	intent, err := semantic.NormalizeTaskIntent(reqText, semantic.IntentOptions{
		Mode: query.Mode,
	})
	if err != nil {
		return nil, coreFlowError("intent_error", fmt.Sprintf("normalize intent: %v", err), nil, false)
	}
	snapshotInput, err := snapshot.AnalyzerInput()
	if err != nil {
		return nil, coreFlowError("snapshot_error", fmt.Sprintf("validated snapshot input: %v", err), nil, false)
	}

	mapIR, proj, err := semantic.CompileDeterministicFeatureMap(resolved, intent, slicePayload, semantic.CompileOptions{
		ComputedBasisID: snapshot.ComputedBasisID, WorkspaceEpoch: snapshot.WorkspaceEpoch,
		ValidatedAgainstSnapshotID: snapshot.SnapshotID, SnapshotID: snapshot.SnapshotID, SnapshotTreeID: snapshot.RootTreeID,
		RepositoryID: snapshot.RepositoryID, WorktreeID: snapshot.WorktreeID, DependencyFingerprint: snapshot.DependencyFingerprint, ConfigurationFingerprint: snapshot.ConfigurationFingerprint,
		AdapterVersion: slicePayload.AdapterVersion, AnalyzerRevision: slicePayload.AnalyzerVersion,
		AnalysisReadSetID: semantic.MetadataString(slicePayload.AnalysisReadSet, "readSetId"), CausalObservationClosureID: semantic.MetadataString(slicePayload.CausalObservationClosure, "closureId"),
		SnapshotFiles: snapshot.Files, SnapshotInput: &snapshotInput,
	})
	if err != nil {
		return nil, coreFlowError("compile_failed", fmt.Sprintf("compile deterministic map: %v", err), nil, false)
	}
	mapBytes, err := json.Marshal(mapIR)
	if err != nil {
		return nil, coreFlowError("compile_failed", fmt.Sprintf("marshal semantic map: %v", err), nil, false)
	}
	if err := contractharness.ValidateSemanticMapIR(mapBytes); err != nil {
		return nil, coreFlowError("compile_failed", fmt.Sprintf("semantic map contract: %v", err), nil, false)
	}
	projectionBytes, err := json.Marshal(proj)
	if err != nil {
		return nil, coreFlowError("compile_failed", fmt.Sprintf("marshal projection: %v", err), nil, false)
	}
	if err := contractharness.ValidateFlowViewProjection(projectionBytes); err != nil {
		return nil, coreFlowError("compile_failed", fmt.Sprintf("projection contract: %v", err), nil, false)
	}
	s.rememberSemanticMap(targetRoot, mapIR)

	evidenceRecords, err := semantic.ExtractAndRedactEvidenceFromProtocolSnapshot(resolved, slicePayload, snapshot)
	if err != nil {
		evidenceRecords = []semantic.EvidenceRecord{}
	}

	response := map[string]any{
		"candidateAnswer": map[string]string{
			"requested":  mapIR.Summary.Requested,
			"candidate":  mapIR.Summary.Current,
			"authority":  mapIR.Authority,
			"freshness":  mapIR.Freshness,
			"settlement": mapIR.Settlement,
		},
		"taskIntent":          intent,
		"agentReportedStatus": nil,
		"semanticMap":         mapIR,
		"projection":          proj,
		"evidence":            evidenceRecords,
		"unknowns":            mapIR.Unknowns,
	}
	if liveReviewURL != nil {
		viewURL, err := liveReviewURL(reqText, resolved.EntrySymbolPath)
		if err != nil {
			return nil, coreFlowError("live_coordinator_error", err.Error(), nil, false)
		}
		// A feature query establishes the Live Semantic Map before edits arrive.
		// The URL restores that exact request in FlowView, so opening it does not
		// require a user to repeat the query before live updates are visible.
		response["flowView"] = map[string]any{
			"status":   "ready",
			"mode":     "live_semantic_map",
			"autoOpen": true,
			"url":      viewURL,
			"template": flowview.LiveSemanticTemplate,
		}
	}
	return response, nil
}

// onboardingQueryObject normalizes JSON-like inputs used by executeTool.  MCP
// decoding normally supplies map[string]any, while unit/in-process callers may
// pass a typed query struct.  Non-object values remain on the legacy path so
// its schema error stays the public diagnostic.
func onboardingQueryObject(raw any) (map[string]any, bool) {
	if object, ok := raw.(map[string]any); ok {
		return object, true
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return nil, false
	}
	var object map[string]any
	if err := json.Unmarshal(data, &object); err != nil || object == nil {
		return nil, false
	}
	return object, true
}

func isOnboardingQuery(query map[string]any) bool {
	mode, _ := query["mode"].(string)
	schemaID, _ := query["schemaId"].(string)
	return mode == "onboarding" || schemaID == semantic.OnboardingQuerySchemaID
}

// handleOnboardingTaskViewQuery accepts both the normalized v2 query itself
// and the legacy TaskViewQuery envelope carrying the v2 identity fields in its
// onboarding block.  All forms converge on the same strict MCP onboarding
// parser and FlowView/semantic projector.
func (s *Server) handleOnboardingTaskViewQuery(ctx context.Context, targetRoot string, token any, raw map[string]any) (any, error) {
	schemaID, _ := raw["schemaId"].(string)
	if schemaID == semantic.OnboardingQuerySchemaID {
		data, err := json.Marshal(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid_precondition: onboarding query is not valid JSON: %w", err)
		}
		if err := contractharness.ValidateOnboardingQueryV2(data); err != nil {
			return nil, fmt.Errorf("missing_precondition: onboarding query schema/precondition validation failed: %w", err)
		}
		var query semantic.OnboardingQueryV2
		if err := json.Unmarshal(data, &query); err != nil {
			return nil, fmt.Errorf("invalid_precondition: unmarshal onboarding query: %w", err)
		}
		args := map[string]any{
			"repositoryId":               query.RepositoryID,
			"domain":                     query.Domain,
			"freshness":                  query.Freshness,
			"computedBasisId":            query.ComputedBasisID,
			"generationId":               query.GenerationID,
			"validatedAgainstSnapshotId": query.ValidatedAgainstSnapshotID,
			"level":                      query.Level,
			"target":                     targetRoot,
			"token":                      token,
		}
		if query.DisplayBudget != nil {
			args["displayBudget"] = map[string]any{
				"targetMin":   query.DisplayBudget.TargetMin,
				"targetMax":   query.DisplayBudget.TargetMax,
				"enforcement": query.DisplayBudget.Enforcement,
			}
		}
		return s.handleExploreProjectDomains(ctx, args)
	}

	mode, _ := raw["mode"].(string)
	if mode != "onboarding" {
		return nil, fmt.Errorf("missing_precondition: onboarding query mode is required")
	}
	nested, ok := raw["onboarding"].(map[string]any)
	if !ok {
		// Let the legacy validator produce the canonical shape error for a query
		// whose onboarding block is absent or not an object.
		data, err := json.Marshal(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid_precondition: onboarding query is not valid JSON: %w", err)
		}
		if err := contractharness.ValidateTaskViewQuery(data); err != nil {
			return nil, fmt.Errorf("missing_precondition: query schema/precondition validation failed: %w", err)
		}
		return nil, fmt.Errorf("missing_precondition: onboarding requires repositoryId")
	}

	// Validate the legacy envelope with a sanitized onboarding block.  The
	// schema predates the v2 exact identity fields and would otherwise reject
	// them even though this seam explicitly supports them.
	clean := make(map[string]any, len(raw))
	for key, value := range raw {
		clean[key] = value
	}
	cleanNested := make(map[string]any, 2)
	for _, key := range []string{"repositoryId", "domain"} {
		if value, present := nested[key]; present {
			cleanNested[key] = value
		}
	}
	clean["onboarding"] = cleanNested
	data, err := json.Marshal(clean)
	if err != nil {
		return nil, fmt.Errorf("invalid_precondition: onboarding query is not valid JSON: %w", err)
	}
	if err := contractharness.ValidateTaskViewQuery(data); err != nil {
		return nil, fmt.Errorf("missing_precondition: query schema/precondition validation failed: %w", err)
	}

	args, err := onboardingArgsFromTaskViewQuery(raw, nested, targetRoot, token)
	if err != nil {
		return nil, err
	}
	return s.handleExploreProjectDomains(ctx, args)
}

func onboardingArgsFromTaskViewQuery(raw, nested map[string]any, targetRoot string, token any) (map[string]any, error) {
	args := map[string]any{"target": targetRoot, "token": token}
	allowedFields := map[string]bool{
		"repositoryId": true, "domain": true, "freshness": true,
		"computedBasisId": true, "basisId": true, "generationId": true,
		"genId": true, "validatedAgainstSnapshotId": true, "snapshotId": true,
		"level": true, "maxVisibleCoreSteps": true, "displayBudget": true,
	}
	for key := range nested {
		if !allowedFields[key] {
			return nil, fmt.Errorf("invalid_precondition: unsupported onboarding field %q", key)
		}
	}
	for _, key := range []string{
		"repositoryId", "domain", "freshness", "computedBasisId", "basisId",
		"generationId", "genId", "validatedAgainstSnapshotId", "snapshotId",
		"level", "maxVisibleCoreSteps",
	} {
		if value, present := nested[key]; present {
			if err := validateOnboardingTaskViewField(key, value); err != nil {
				return nil, err
			}
			args[key] = value
		}
	}
	if budget, present := nested["displayBudget"]; present {
		budgetMap, ok := budget.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("invalid_precondition: displayBudget must be an object")
		}
		for key := range budgetMap {
			if key != "targetMin" && key != "targetMax" && key != "enforcement" {
				return nil, fmt.Errorf("invalid_precondition: unsupported onboarding displayBudget field %q", key)
			}
		}
		if _, err := parseMCPDisplayBudget(budgetMap); err != nil {
			return nil, err
		}
		args["displayBudget"] = budgetMap
		if value, present := budgetMap["targetMax"]; present {
			if err := validateOnboardingTaskViewField("maxVisibleCoreSteps", value); err != nil {
				return nil, err
			}
			if existing, exists := args["maxVisibleCoreSteps"]; exists && fmt.Sprint(existing) != fmt.Sprint(value) {
				return nil, fmt.Errorf("invalid_precondition: conflicting maxVisibleCoreSteps and displayBudget.targetMax")
			}
			args["maxVisibleCoreSteps"] = value
		}
	}

	common, commonOK := raw["common"].(map[string]any)
	if _, present := raw["common"]; present && !commonOK {
		return nil, fmt.Errorf("missing_precondition: query schema/precondition validation failed: common must be an object")
	}
	if commonOK {
		selector, selectorOK := common["basisSelector"].(map[string]any)
		if _, present := common["basisSelector"]; present && !selectorOK {
			return nil, fmt.Errorf("missing_precondition: query schema/precondition validation failed: basisSelector must be an object")
		}
		if selectorOK {
			kind, ok := selector["kind"].(string)
			if !ok || kind == "" {
				return nil, fmt.Errorf("invalid_precondition: basisSelector.kind must be a string")
			}
			selectorID, hasID := selector["id"]
			if hasID {
				if _, ok := selectorID.(string); !ok {
					return nil, fmt.Errorf("invalid_precondition: basisSelector.id must be a string")
				}
			}
			freshness, generationKey, snapshotKey := "", "", ""
			switch kind {
			case "active":
				freshness = "current"
			case "generation":
				freshness, generationKey = "historical", "generationId"
			case "workspaceSnapshot":
				freshness, snapshotKey = "historical", "snapshotId"
			default:
				return nil, fmt.Errorf("invalid_precondition: unsupported basisSelector.kind %q", kind)
			}
			if existing, exists := args["freshness"]; exists && fmt.Sprint(existing) != freshness {
				return nil, fmt.Errorf("invalid_precondition: conflicting onboarding freshness and basisSelector.kind")
			}
			args["freshness"] = freshness
			if hasID && strings.TrimSpace(fmt.Sprint(selectorID)) != "" {
				key := generationKey
				if key == "" {
					key = snapshotKey
				}
				if existing, exists := args[key]; exists && fmt.Sprint(existing) != fmt.Sprint(selectorID) {
					return nil, fmt.Errorf("invalid_precondition: conflicting onboarding basis identity and basisSelector.id")
				}
				args[key] = selectorID
			}
		}
		if filters, filtersOK := common["filters"].(map[string]any); filtersOK {
			if value, present := filters["maxVisibleCoreSteps"]; present {
				if err := validateOnboardingTaskViewField("maxVisibleCoreSteps", value); err != nil {
					return nil, err
				}
				if existing, exists := args["maxVisibleCoreSteps"]; exists && fmt.Sprint(existing) != fmt.Sprint(value) {
					return nil, fmt.Errorf("invalid_precondition: conflicting maxVisibleCoreSteps and common.filters.maxVisibleCoreSteps")
				}
				args["maxVisibleCoreSteps"] = value
			}
		}
	}
	return args, nil
}

func validateOnboardingTaskViewField(key string, value any) error {
	switch key {
	case "repositoryId", "domain", "freshness", "computedBasisId", "basisId", "generationId", "genId", "validatedAgainstSnapshotId", "snapshotId":
		if _, ok := value.(string); !ok {
			return fmt.Errorf("invalid_precondition: onboarding %s must be a string", key)
		}
	case "level", "maxVisibleCoreSteps":
		n, err := integerArgument(map[string]any{key: value}, key)
		if err != nil {
			return fmt.Errorf("invalid_precondition: onboarding %s must be an integer", key)
		}
		if key == "maxVisibleCoreSteps" && n <= 0 {
			return fmt.Errorf("invalid_precondition: onboarding %s must be a positive integer", key)
		}
	default:
		return fmt.Errorf("invalid_precondition: unsupported onboarding field %q", key)
	}
	return nil
}

func (s *Server) handleGetCurrentAnswer(ctx context.Context, args map[string]any) (any, error) {
	if err := s.checkAuth(args["token"]); err != nil {
		return nil, coreFlowError("unauthorized", err.Error(), nil, false)
	}

	// VS04 only produces candidate/historical artifacts. Current Answer is a
	// VS03 publication and cannot be synthesized by rerunning candidate
	// analysis, so the public seam returns a typed, actionable result.
	return map[string]any{
		"code":    "no_current_proof",
		"message": "no VS-03 current publication proof is available",
	}, nil
}
