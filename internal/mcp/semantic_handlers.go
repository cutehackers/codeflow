package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"codeflow/internal/contractharness"
	"codeflow/internal/semantic"
)

func (s *Server) handleQueryTaskView(ctx context.Context, args map[string]any) (any, error) {
	if err := s.checkAuth(args["token"]); err != nil {
		return nil, coreFlowError("unauthorized", err.Error(), nil, false)
	}

	targetRoot := s.resolveTarget(args["target"])

	rawQuery, ok := args["query"]
	if !ok || rawQuery == nil {
		return nil, coreFlowError(semantic.ErrCodeMissingPrecondition, "missing 'query' argument", nil, false)
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

	if query.Mode == "impact" {
		symID := ""
		batchID := ""
		if query.Impact != nil {
			symID = query.Impact.SymbolID
			batchID = query.Impact.ChangeBatchID
		}
		res, err := s.handleGetChangeImpact(ctx, map[string]any{
			"symbolId":      symID,
			"changeBatchId": batchID,
			"target":        targetRoot,
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
		repoID := "workspace"
		domain := ""
		if query.Onboarding != nil {
			if query.Onboarding.RepositoryID != "" {
				repoID = query.Onboarding.RepositoryID
			}
			domain = query.Onboarding.Domain
		}
		res, err := s.handleExploreProjectDomains(ctx, map[string]any{
			"repositoryId": repoID,
			"domain":       domain,
			"target":       targetRoot,
		})
		if err != nil {
			return nil, coreFlowError("onboarding_error", err.Error(), nil, false)
		}
		return res, nil
	}

	_, harvester, slicer, err := s.getPoolAndRunners(ctx, targetRoot, "")
	if err != nil {
		return nil, coreFlowError("adapter_error", fmt.Sprintf("adapter error: %v", err), nil, false)
	}

	candidates, err := harvester.Run(ctx, targetRoot)
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

	slicePayload, err := slicer.Slice(ctx, targetRoot, resolved.CandidateID, resolved.EntrySymbolPath, nil)
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

	mapIR, proj, err := semantic.CompileDeterministicFeatureMap(resolved, intent, slicePayload, semantic.CompileOptions{
		ComputedBasisID: slicePayload.ComputedBasisID,
		WorkspaceEpoch:  slicePayload.WorkspaceEpoch,
	})
	if err != nil {
		return nil, coreFlowError("compile_failed", fmt.Sprintf("compile deterministic map: %v", err), nil, false)
	}

	evidenceRecords, err := semantic.ExtractAndRedactEvidence(resolved, slicePayload, targetRoot)
	if err != nil {
		evidenceRecords = []semantic.EvidenceRecord{}
	}

	return map[string]any{
		"currentAnswer": map[string]string{
			"requested": mapIR.Summary.Requested,
			"current":   mapIR.Summary.Current,
		},
		"taskIntent":  intent,
		"semanticMap": mapIR,
		"projection":  proj,
		"evidence":    evidenceRecords,
		"unknowns":    mapIR.Unknowns,
	}, nil
}

func (s *Server) handleGetCurrentAnswer(ctx context.Context, args map[string]any) (any, error) {
	if err := s.checkAuth(args["token"]); err != nil {
		return nil, coreFlowError("unauthorized", err.Error(), nil, false)
	}

	targetRoot := s.resolveTarget(args["target"])
	queryStr, _ := args["query"].(string)
	flowID, _ := args["flowId"].(string)

	query := &semantic.TaskViewQuery{
		SchemaID:      "https://codeflow.local/schemas/task-view-query.schema.json",
		SchemaVersion: 1,
		Mode:          "feature",
		Feature: &semantic.FeatureQueryParams{
			Request: queryStr,
			FlowID:  flowID,
		},
	}

	qArg := map[string]any{
		"query":  query,
		"target": targetRoot,
	}

	res, err := s.handleQueryTaskView(ctx, qArg)
	if err != nil {
		return nil, err
	}

	resMap, ok := res.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("unexpected response format")
	}

	ans, _ := resMap["currentAnswer"].(map[string]string)
	mapIR, _ := resMap["semanticMap"].(*semantic.SemanticMapIR)

	stage := "Q2"
	basis := "basis-current"
	if mapIR != nil {
		stage = mapIR.Quality.Stage
		basis = mapIR.ComputedBasisID
	}

	return map[string]any{
		"requested":    ans["requested"],
		"current":      ans["current"],
		"qualityStage": stage,
		"basisId":      basis,
	}, nil
}
