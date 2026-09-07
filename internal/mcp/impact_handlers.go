package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"strings"

	"codeflow/internal/contractharness"
	"codeflow/internal/rflscvs02"
	"codeflow/internal/secret"
	"codeflow/internal/semantic"
	"codeflow/internal/storage"
)

// impactRequest is the decoded public MCP request. The query identity is
// deliberately kept separate from the map identity loaded from the proof
// bundle so a caller cannot make an unverified map look current.
type impactRequest struct {
	Target        semantic.ImpactTarget
	BasisID       string
	GenerationID  string
	Freshness     string
	MaxDepth      int
	MaxNodes      int
	RelationKinds []string
}

// handleGetChangeImpact is the MCP public boundary for VS-05. It only uses a
// validated current proof bundle or an explicitly cached historical map. It
// never creates a target, generation, evidence item, or capability profile.
func (s *Server) handleGetChangeImpact(ctx context.Context, args map[string]any) (any, error) {
	if err := s.checkAuth(args["token"]); err != nil {
		return nil, coreFlowError("unauthorized", err.Error(), nil, false)
	}

	request, err := parseImpactRequest(args)
	if err != nil {
		return impactFailureFromError(err), nil
	}

	targetRoot := s.resolveTarget(args["target"])
	st, err := s.getStorage(targetRoot)
	if err != nil {
		return nil, coreFlowError("storage_error", fmt.Sprintf("get impact storage: %v", err), nil, false)
	}

	mapIR, capability, deltaBytes, coverageComplete, proofVerified, err := s.loadImpactBasis(ctx, targetRoot, st, request)
	if err != nil {
		return impactFailureFromError(err), nil
	}
	if err := validateImpactRequest(request, coverageComplete, proofVerified); err != nil {
		return impactFailureFromError(err), nil
	}

	if request.Target.ChangeBatchID != "" {
		refs, resolveErr := s.resolveImpactChangeBatch(ctx, targetRoot, request, mapIR, deltaBytes)
		if resolveErr != nil {
			return impactFailureFromError(resolveErr), nil
		}
		requestTarget := request.Target
		impactOptions := semantic.ImpactOptions{
			MaxDepth:                   request.MaxDepth,
			MaxNodes:                   request.MaxNodes,
			ComputedBasisID:            request.BasisID,
			GenerationID:               request.GenerationID,
			ValidatedAgainstSnapshotID: mapIR.ValidatedAgainstSnapshotID,
			Freshness:                  request.Freshness,
			CurrentProofVerified:       proofVerified,
			ChangeBatchStepRefs:        refs,
			RelationKinds:              request.RelationKinds,
			SupportedRelationKinds:     semantic.SupportedImpactRelationKinds(capability.Features, request.RelationKinds),
			CoverageComplete:           coverageComplete,
		}
		return s.computeImpactEgress(requestTarget, mapIR, impactOptions)
	}

	impactOptions := semantic.ImpactOptions{
		MaxDepth:                   request.MaxDepth,
		MaxNodes:                   request.MaxNodes,
		ComputedBasisID:            request.BasisID,
		GenerationID:               request.GenerationID,
		ValidatedAgainstSnapshotID: mapIR.ValidatedAgainstSnapshotID,
		Freshness:                  request.Freshness,
		CurrentProofVerified:       proofVerified,
		RelationKinds:              request.RelationKinds,
		SupportedRelationKinds:     semantic.SupportedImpactRelationKinds(capability.Features, request.RelationKinds),
		CoverageComplete:           coverageComplete,
	}
	return s.computeImpactEgress(request.Target, mapIR, impactOptions)
}

func (s *Server) computeImpactEgress(target semantic.ImpactTarget, mapIR *semantic.SemanticMapIR, options semantic.ImpactOptions) (any, error) {
	graph, err := semantic.ComputeChangeImpact(target, mapIR, options)
	if err != nil {
		return impactFailureFromError(err), nil
	}

	payload, err := marshalAndValidateImpactGraph(graph)
	if err != nil {
		return impactFailureFromError(err), nil
	}
	return payload, nil
}

func marshalAndValidateImpactGraph(graph *semantic.ChangeImpactGraph) (map[string]any, error) {
	if graph == nil {
		return nil, fmt.Errorf("schema_validation_failed: impact graph is empty")
	}
	raw, err := json.Marshal(graph)
	if err != nil {
		return nil, fmt.Errorf("schema_validation_failed: marshal impact output: %w", err)
	}
	redacted, _, err := secret.RedactJSON(raw)
	if err != nil {
		return nil, fmt.Errorf("schema_validation_failed: redact impact output: %w", err)
	}
	if err := contractharness.ValidateChangeImpactGraphV2(redacted); err != nil {
		return nil, fmt.Errorf("schema_validation_failed: change-impact-graph.v2 output: %w", err)
	}
	var output map[string]any
	if err := json.Unmarshal(redacted, &output); err != nil {
		return nil, fmt.Errorf("schema_validation_failed: decode redacted impact output: %w", err)
	}
	return output, nil
}

func parseImpactRequest(args map[string]any) (*impactRequest, error) {
	symbolID := strings.TrimSpace(stringArgument(args, "symbolId"))
	batchID := strings.TrimSpace(stringArgument(args, "changeBatchId"))
	if symbolID == "" && batchID == "" {
		return nil, fmt.Errorf("missing_precondition: either symbolId or changeBatchId must be provided")
	}
	if symbolID != "" && batchID != "" {
		return nil, fmt.Errorf("invalid_precondition: symbolId and changeBatchId are mutually exclusive")
	}

	basisID := strings.TrimSpace(stringArgument(args, "computedBasisId"))
	if basisID == "" {
		return nil, fmt.Errorf("missing_precondition: computedBasisId is required")
	}
	generationID := strings.TrimSpace(stringArgument(args, "generationId"))
	if generationID == "" {
		return nil, fmt.Errorf("missing_precondition: generationId is required")
	}
	freshness := strings.TrimSpace(stringArgument(args, "freshness"))
	if freshness == "" {
		return nil, fmt.Errorf("missing_precondition: freshness is required")
	}

	maxDepth, ok := impactIntegerArgument(args, "maxDepth")
	if !ok {
		return nil, fmt.Errorf("missing_precondition: maxDepth is required and must be an integer")
	}
	maxNodes, ok := impactIntegerArgument(args, "maxNodes")
	if !ok {
		return nil, fmt.Errorf("missing_precondition: maxNodes is required and must be an integer")
	}
	relationKinds, ok := impactStringSliceArgument(args, "relationKinds")
	if !ok || len(relationKinds) == 0 {
		return nil, fmt.Errorf("missing_precondition: relationKinds is required and must not be empty")
	}

	return &impactRequest{
		Target:        semantic.ImpactTarget{SymbolID: symbolID, ChangeBatchID: batchID},
		BasisID:       basisID,
		GenerationID:  generationID,
		Freshness:     freshness,
		MaxDepth:      maxDepth,
		MaxNodes:      maxNodes,
		RelationKinds: relationKinds,
	}, nil
}

func validateImpactRequest(request *impactRequest, coverageComplete, currentProofVerified bool) error {
	if request == nil {
		return fmt.Errorf("missing_precondition: impact request is required")
	}
	target := map[string]any{}
	if request.Target.SymbolID != "" {
		target["symbolId"] = request.Target.SymbolID
	} else {
		target["changeBatchId"] = request.Target.ChangeBatchID
	}
	doc := map[string]any{
		"schemaId":             semantic.ImpactQuerySchemaID,
		"schemaVersion":        2,
		"target":               target,
		"computedBasisId":      request.BasisID,
		"generationId":         request.GenerationID,
		"freshness":            request.Freshness,
		"maxDepth":             request.MaxDepth,
		"maxNodes":             request.MaxNodes,
		"relationKinds":        request.RelationKinds,
		"coverageComplete":     coverageComplete,
		"currentProofVerified": currentProofVerified,
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		return fmt.Errorf("invalid_precondition: marshal impact query: %w", err)
	}
	if err := contractharness.ValidateImpactQueryV2(raw); err != nil {
		return fmt.Errorf("invalid_precondition: impact query contract: %w", err)
	}
	return nil
}

func (s *Server) loadImpactBasis(ctx context.Context, targetRoot string, st *storage.Storage, request *impactRequest) (*semantic.SemanticMapIR, rflscvs02.CapabilityProfile, []byte, bool, bool, error) {
	if request == nil {
		return nil, rflscvs02.CapabilityProfile{}, nil, false, false, fmt.Errorf("missing_precondition: impact request is required")
	}
	if request.Freshness == "current" {
		bundle, err := st.ReadValidatedActiveProofBundle()
		if err != nil {
			return nil, rflscvs02.CapabilityProfile{}, nil, false, false, fmt.Errorf("invalid_authority: validated current proof is unavailable: %w", err)
		}
		if bundle == nil || bundle.Manifest == nil || bundle.Pointer == nil {
			return nil, rflscvs02.CapabilityProfile{}, nil, false, false, fmt.Errorf("missing_precondition: no validated current proof is published")
		}
		engine, engineErr := s.getSnapshotEngine(targetRoot)
		if engineErr != nil {
			return nil, rflscvs02.CapabilityProfile{}, nil, false, false, fmt.Errorf("invalid_authority: live workspace head is unavailable: %w", engineErr)
		}
		liveHead := engine.LiveHead()
		if liveHead == nil {
			return nil, rflscvs02.CapabilityProfile{}, nil, false, false, fmt.Errorf("invalid_authority: workspace has no measured live snapshot")
		}
		if bundle.Pointer.ExpectedLiveHeadSnapshotID == "" || liveHead.SnapshotID != bundle.Pointer.ExpectedLiveHeadSnapshotID {
			return nil, rflscvs02.CapabilityProfile{}, nil, false, false, fmt.Errorf("invalid_authority: active proof expected live head %q but workspace live head is %q", bundle.Pointer.ExpectedLiveHeadSnapshotID, liveHead.SnapshotID)
		}
		if bundle.Pointer.ComputedBasisID != request.BasisID || bundle.Pointer.GenerationID != request.GenerationID || bundle.Manifest.ComputedBasisID != request.BasisID || bundle.Manifest.GenerationID != request.GenerationID {
			return nil, rflscvs02.CapabilityProfile{}, nil, false, false, fmt.Errorf("incomparable_basis: requested impact identity does not match the validated current proof")
		}

		mapIR, result, err := decodeValidatedImpactArtifacts(bundle.SemanticMap, bundle.AnalyzerResult)
		if err != nil {
			return nil, rflscvs02.CapabilityProfile{}, nil, false, false, fmt.Errorf("invalid_graph: validated current impact artifacts are unusable: %w", err)
		}
		if mapIR.GenerationID != request.GenerationID || mapIR.ComputedBasisID != request.BasisID {
			return nil, rflscvs02.CapabilityProfile{}, nil, false, false, fmt.Errorf("incomparable_basis: semantic map identity does not match the requested impact basis")
		}
		supported := semantic.SupportedImpactRelationKinds(result.Capability.Features, request.RelationKinds)
		excludedReasons := impactCoverageExcludedReasons(mapIR, result.Coverage.ExcludedReasons)
		coverageComplete := semantic.ImpactCoverageComplete(result.Coverage.Measured, excludedReasons, result.Closure.Status, request.RelationKinds, supported)
		return mapIR, result.Capability, bundle.SemanticDelta, coverageComplete, true, nil
	}

	if request.Freshness != "historical" {
		return nil, rflscvs02.CapabilityProfile{}, nil, false, false, fmt.Errorf("invalid_precondition: freshness must be current or historical")
	}
	mapIR, ok := s.loadSemanticMap(targetRoot, request.GenerationID)
	if !ok || mapIR == nil {
		return nil, rflscvs02.CapabilityProfile{}, nil, false, false, fmt.Errorf("missing_precondition: historical semantic map %q is not available", request.GenerationID)
	}
	if mapIR.SchemaID != semantic.SemanticMapSchemaID || mapIR.SchemaVersion != semantic.SemanticSchemaVersion || mapIR.GenerationID != request.GenerationID || mapIR.ComputedBasisID != request.BasisID || mapIR.ValidatedAgainstSnapshotID == "" {
		return nil, rflscvs02.CapabilityProfile{}, nil, false, false, fmt.Errorf("incomparable_basis: historical semantic map identity does not match the requested basis")
	}
	if mapIR.Freshness != "historical" {
		return nil, rflscvs02.CapabilityProfile{}, nil, false, false, fmt.Errorf("invalid_authority: historical impact requires an explicitly historical semantic map")
	}
	// The in-memory historical cache has no persisted analyzer capability
	// profile. Keep every requested relation unsupported instead of turning a
	// cache hit into an invented capability claim.
	return cloneSemanticMapForImpact(mapIR), rflscvs02.CapabilityProfile{}, nil, false, false, nil
}

func decodeValidatedImpactArtifacts(mapBytes, resultBytes []byte) (*semantic.SemanticMapIR, rflscvs02.Result, error) {
	if len(mapBytes) == 0 || len(resultBytes) == 0 {
		return nil, rflscvs02.Result{}, fmt.Errorf("semantic map and analyzer result artifacts are required")
	}
	if err := contractharness.ValidateSemanticMapIR(mapBytes); err != nil {
		return nil, rflscvs02.Result{}, fmt.Errorf("semantic map contract: %w", err)
	}
	if err := contractharness.Validate(rflscvs02.AnalyzerResultSchemaID, resultBytes); err != nil {
		return nil, rflscvs02.Result{}, fmt.Errorf("analyzer result contract: %w", err)
	}
	var mapIR semantic.SemanticMapIR
	if err := json.Unmarshal(mapBytes, &mapIR); err != nil {
		return nil, rflscvs02.Result{}, fmt.Errorf("decode semantic map: %w", err)
	}
	var result rflscvs02.Result
	if err := json.Unmarshal(resultBytes, &result); err != nil {
		return nil, rflscvs02.Result{}, fmt.Errorf("decode analyzer result: %w", err)
	}
	if mapIR.SchemaID != semantic.SemanticMapSchemaID || mapIR.SchemaVersion != semantic.SemanticSchemaVersion || mapIR.GenerationID == "" || mapIR.ComputedBasisID == "" || mapIR.ValidatedAgainstSnapshotID == "" {
		return nil, rflscvs02.Result{}, fmt.Errorf("semantic map identity is incomplete")
	}
	if result.ComputedBasisID != mapIR.ComputedBasisID || result.SnapshotID != mapIR.Basis.ComputedWorkspaceSnapshotID || result.SnapshotID == "" {
		return nil, rflscvs02.Result{}, fmt.Errorf("analyzer result identity does not match semantic map")
	}
	return &mapIR, result, nil
}

func (s *Server) resolveImpactChangeBatch(ctx context.Context, targetRoot string, request *impactRequest, mapIR *semantic.SemanticMapIR, deltaBytes []byte) ([]string, error) {
	if request == nil || request.Target.ChangeBatchID == "" {
		return nil, fmt.Errorf("missing_precondition: change batch target is required")
	}
	if len(deltaBytes) == 0 {
		return nil, fmt.Errorf("missing_precondition: validated semantic delta is required for changeBatchId")
	}
	var delta semantic.SemanticDeltaIR
	if err := contractharness.ValidateSemanticDeltaIR(deltaBytes); err != nil {
		return nil, fmt.Errorf("invalid_graph: semantic delta contract: %w", err)
	}
	if err := json.Unmarshal(deltaBytes, &delta); err != nil {
		return nil, fmt.Errorf("invalid_graph: decode semantic delta: %w", err)
	}
	if delta.Status != "comparable" || delta.ToGeneration != mapIR.GenerationID || delta.CurrentComputedBasisID != mapIR.ComputedBasisID || delta.CurrentValidatedAgainstSnapshotID != "" && delta.CurrentValidatedAgainstSnapshotID != mapIR.ValidatedAgainstSnapshotID {
		return nil, fmt.Errorf("incomparable_basis: semantic delta does not match the requested generation")
	}

	engine, err := s.getSnapshotEngine(targetRoot)
	if err != nil {
		return nil, fmt.Errorf("missing_precondition: change batch storage is unavailable: %w", err)
	}
	batch, err := engine.GetBatch(request.Target.ChangeBatchID)
	if err != nil {
		return nil, fmt.Errorf("missing_precondition: change batch %q is not available: %w", request.Target.ChangeBatchID, err)
	}
	if batch.Status != "committed" {
		return nil, fmt.Errorf("missing_precondition: change batch %q is not committed", request.Target.ChangeBatchID)
	}
	if batch.WorkspaceEpoch != mapIR.Basis.WorkspaceEpoch {
		return nil, fmt.Errorf("incomparable_basis: change batch workspace epoch does not match semantic map")
	}

	changedPaths := map[string]bool{}
	batchRevisionIDs := map[string]bool{}
	for _, revisionID := range batch.Revisions {
		revision, revisionErr := engine.GetRevision(revisionID)
		if revisionErr != nil {
			return nil, fmt.Errorf("missing_precondition: change batch revision %q is unavailable: %w", revisionID, revisionErr)
		}
		path := filepath.ToSlash(filepath.Clean(revision.Path))
		if path == "." || path == "" || filepath.IsAbs(path) || strings.HasPrefix(path, "../") {
			return nil, fmt.Errorf("invalid_graph: change batch revision path is invalid")
		}
		batchRevisionIDs[revisionID] = true
		changedPaths[path] = true
	}
	if len(changedPaths) == 0 {
		return nil, fmt.Errorf("missing_precondition: change batch has no revisions")
	}

	stepsByID := map[string]semantic.SemanticStep{}
	for _, step := range mapIR.Steps {
		stepsByID[step.StepID] = step
	}
	evidenceByID := map[string]semantic.SemanticEvidence{}
	for _, evidence := range mapIR.Evidence {
		if evidence.EvidenceID != "" {
			evidenceByID[evidence.EvidenceID] = evidence
		}
	}
	refs := map[string]bool{}
	for _, change := range delta.Changes {
		if change.ValidationStatus != "verified" || change.EpistemicStatus == "unknown" || change.EpistemicStatus == "unobserved" {
			continue
		}
		stepID := change.ToStepID
		if stepID == "" {
			stepID = change.TargetStepID
		}
		step, ok := stepsByID[stepID]
		if !ok {
			continue
		}
		if changedPaths[filepath.ToSlash(filepath.Clean(step.Anchor.RepoRelativePath))] && impactStepHasBatchRevision(step, evidenceByID, batchRevisionIDs) {
			refs[stepID] = true
		}
	}
	if len(refs) == 0 {
		return nil, fmt.Errorf("missing_precondition: semantic delta has no verified changed step for change batch %q", request.Target.ChangeBatchID)
	}
	out := make([]string, 0, len(refs))
	for ref := range refs {
		out = append(out, ref)
	}
	sort.Strings(out)
	return out, nil
}

func impactStepHasBatchRevision(step semantic.SemanticStep, evidenceByID map[string]semantic.SemanticEvidence, batchRevisionIDs map[string]bool) bool {
	if len(step.EvidenceRefs) == 0 {
		return true
	}
	for _, evidenceID := range step.EvidenceRefs {
		if evidence, ok := evidenceByID[evidenceID]; ok && batchRevisionIDs[evidence.DocumentRevisionID] {
			return true
		}
	}
	return false
}

func cloneSemanticMapForImpact(input *semantic.SemanticMapIR) *semantic.SemanticMapIR {
	if input == nil {
		return nil
	}
	output := *input
	output.Steps = append([]semantic.SemanticStep(nil), input.Steps...)
	output.Edges = append([]semantic.SemanticEdge(nil), input.Edges...)
	return &output
}

func mapCoverageExcludedReasons(mapIR *semantic.SemanticMapIR) []string {
	if mapIR == nil || mapIR.Coverage == nil {
		return []string{"coverage_unmeasured"}
	}
	return mapIR.Coverage.ExcludedReasons
}

func impactCoverageExcludedReasons(mapIR *semantic.SemanticMapIR, resultReasons []string) []string {
	seen := map[string]bool{}
	reasons := make([]string, 0, len(resultReasons)+1)
	for _, reason := range append(append([]string{}, resultReasons...), mapCoverageExcludedReasons(mapIR)...) {
		reason = strings.TrimSpace(reason)
		if reason == "" || seen[reason] {
			continue
		}
		seen[reason] = true
		reasons = append(reasons, reason)
	}
	sort.Strings(reasons)
	return reasons
}

func stringArgument(args map[string]any, key string) string {
	value, _ := args[key].(string)
	return value
}

func impactIntegerArgument(args map[string]any, key string) (int, bool) {
	value, ok := args[key]
	if !ok {
		return 0, false
	}
	switch typed := value.(type) {
	case int:
		return typed, true
	case int64:
		if typed < math.MinInt || typed > math.MaxInt {
			return 0, false
		}
		return int(typed), true
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) || typed != math.Trunc(typed) || typed < math.MinInt || typed > math.MaxInt {
			return 0, false
		}
		return int(typed), true
	default:
		return 0, false
	}
}

func impactStringSliceArgument(args map[string]any, key string) ([]string, bool) {
	value, ok := args[key]
	if !ok {
		return nil, false
	}
	var values []string
	switch typed := value.(type) {
	case []string:
		values = append([]string(nil), typed...)
	case []any:
		values = make([]string, 0, len(typed))
		for _, item := range typed {
			text, ok := item.(string)
			if !ok {
				return nil, false
			}
			values = append(values, text)
		}
	default:
		return nil, false
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			return nil, false
		}
		seen[value] = true
		out = append(out, value)
	}
	sort.Strings(out)
	return out, true
}

func impactFailureFromError(err error) map[string]any {
	code := "impact_error"
	message := "impact query failed"
	if err != nil {
		message = err.Error()
		if index := strings.IndexByte(message, ':'); index > 0 {
			candidate := strings.TrimSpace(message[:index])
			if isImpactErrorCode(candidate) {
				code = candidate
				message = strings.TrimSpace(message[index+1:])
			}
		}
	}
	return map[string]any{
		"code":      code,
		"message":   boundedDiagnostic(message, 512),
		"retryable": false,
	}
}

func isImpactErrorCode(code string) bool {
	switch code {
	case "missing_precondition", "invalid_precondition", "invalid_authority", "invalid_graph", "incomparable_basis", "unsupported_capability", "not_observed", "schema_validation_failed":
		return true
	default:
		return false
	}
}
