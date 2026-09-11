package flowview

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"codeflow/internal/contractharness"
	"codeflow/internal/evidence"
	"codeflow/internal/semantic"
	"codeflow/internal/storage"
)

// handleTaskImpact serves an evidence-bounded impact graph. Current reads use
// the strict active proof bundle; historical reads require an exact cached map
// identity and remain capability-incomplete unless a proof is available.
func (s *Server) handleTaskImpact(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	query, err := impactQueryFromRequest(r)
	if err != nil {
		writeImpactError(w, err)
		return
	}

	mapIR, result, delta, currentProof, err := s.resolveImpactProof(query)
	if err != nil {
		writeImpactError(w, err)
		return
	}
	supported := []string{}
	coverageComplete := false
	if result != nil {
		supported = semantic.SupportedImpactRelationKinds(result.Capability.Features, query.RelationKinds)
		excluded := append([]string{}, result.Coverage.ExcludedReasons...)
		if mapIR.Coverage == nil {
			excluded = append(excluded, "semantic_map_coverage_unmeasured")
		} else {
			excluded = append(excluded, mapIR.Coverage.ExcludedReasons...)
		}
		coverageComplete = semantic.ImpactCoverageComplete(result.Coverage.Measured, excluded, result.Closure.Status, query.RelationKinds, supported)
	}
	batchRefs, err := s.impactBatchStepRefs(query.Target, delta, mapIR)
	if err != nil {
		writeImpactError(w, err)
		return
	}
	query.ChangeBatchSteps = batchRefs
	query.CoverageComplete = coverageComplete
	query.CurrentProofValid = currentProof
	encodedQuery, err := json.Marshal(query)
	if err != nil {
		writeImpactError(w, fmt.Errorf("internal_error: marshal impact query: %w", err))
		return
	}
	if err := contractharness.ValidateImpactQueryV2(encodedQuery); err != nil {
		writeImpactError(w, fmt.Errorf("invalid_precondition: %w", err))
		return
	}

	graph, err := semantic.ComputeChangeImpact(query.Target, mapIR, semantic.ImpactOptions{
		MaxDepth:                   query.MaxDepth,
		MaxNodes:                   query.MaxNodes,
		ComputedBasisID:            query.ComputedBasisID,
		GenerationID:               query.GenerationID,
		ValidatedAgainstSnapshotID: mapIR.ValidatedAgainstSnapshotID,
		Freshness:                  query.Freshness,
		CurrentProofVerified:       currentProof,
		ChangeBatchStepRefs:        batchRefs,
		RelationKinds:              query.RelationKinds,
		SupportedRelationKinds:     supported,
		CoverageComplete:           coverageComplete,
	})
	if err != nil {
		writeImpactError(w, err)
		return
	}
	encodedGraph, err := json.Marshal(graph)
	if err != nil {
		writeImpactError(w, fmt.Errorf("internal_error: marshal impact graph: %w", err))
		return
	}
	if err := contractharness.ValidateChangeImpactGraphV2(encodedGraph); err != nil {
		writeImpactError(w, fmt.Errorf("internal_error: validate impact graph: %w", err))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(encodedGraph)
}

func impactQueryFromRequest(r *http.Request) (semantic.ImpactQuery, error) {
	values := r.URL.Query()
	target := semantic.ImpactTarget{SymbolID: strings.TrimSpace(values.Get("symbolId")), ChangeBatchID: strings.TrimSpace(values.Get("changeBatchId"))}
	if (target.SymbolID == "") == (target.ChangeBatchID == "") {
		return semantic.ImpactQuery{}, errors.New("missing_precondition: exactly one of symbolId or changeBatchId is required")
	}
	basis := strings.TrimSpace(values.Get("computedBasisId"))
	generation := strings.TrimSpace(values.Get("generationId"))
	freshness := strings.TrimSpace(values.Get("freshness"))
	if basis == "" || generation == "" || freshness == "" {
		return semantic.ImpactQuery{}, errors.New("missing_precondition: computedBasisId, generationId and freshness are required")
	}
	maxDepth, err := parseImpactBound(values.Get("maxDepth"), "maxDepth", 5)
	if err != nil {
		return semantic.ImpactQuery{}, err
	}
	maxNodes, err := parseImpactBound(values.Get("maxNodes"), "maxNodes", 50)
	if err != nil {
		return semantic.ImpactQuery{}, err
	}
	relations := impactRelationQueryValues(values["relationKinds"])
	if len(relations) == 0 {
		return semantic.ImpactQuery{}, errors.New("missing_precondition: relationKinds is required")
	}
	return semantic.ImpactQuery{SchemaID: semantic.ImpactQuerySchemaID, SchemaVersion: 2, Target: target, ComputedBasisID: basis, GenerationID: generation, Freshness: freshness, MaxDepth: maxDepth, MaxNodes: maxNodes, RelationKinds: relations}, nil
}

func parseImpactBound(raw, name string, maximum int) (int, error) {
	if strings.TrimSpace(raw) == "" {
		return 0, fmt.Errorf("missing_precondition: %s is required", name)
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 || value > maximum {
		return 0, fmt.Errorf("invalid_precondition: %s must be an integer between 1 and %d", name, maximum)
	}
	return value, nil
}

func impactRelationQueryValues(values []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, value := range values {
		for _, relation := range strings.Split(value, ",") {
			relation = strings.TrimSpace(relation)
			if relation != "" && !seen[relation] {
				seen[relation] = true
				out = append(out, relation)
			}
		}
	}
	return out
}

func (s *Server) resolveImpactProof(query semantic.ImpactQuery) (*semantic.SemanticMapIR, *evidence.Result, *semantic.SemanticDeltaIR, bool, error) {
	if query.Freshness == "current" {
		bundle, err := s.storage.ReadValidatedActiveProofBundle()
		if err != nil {
			return nil, nil, nil, false, fmt.Errorf("current_proof_unavailable: %w", err)
		}
		if bundle == nil {
			return nil, nil, nil, false, errors.New("current_proof_unavailable: no validated active proof exists")
		}
		if s == nil || s.engine == nil {
			return nil, nil, nil, false, errors.New("current_proof_unavailable: live workspace head is unavailable")
		}
		liveHead := s.engine.LiveHead()
		if liveHead == nil {
			return nil, nil, nil, false, errors.New("current_proof_unavailable: workspace has no measured live snapshot")
		}
		if bundle.Pointer.ExpectedLiveHeadSnapshotID == "" || liveHead.SnapshotID != bundle.Pointer.ExpectedLiveHeadSnapshotID {
			return nil, nil, nil, false, fmt.Errorf("current_proof_unavailable: active proof expected live head %q but workspace live head is %q", bundle.Pointer.ExpectedLiveHeadSnapshotID, liveHead.SnapshotID)
		}
		if bundle.Pointer.ComputedBasisID != query.ComputedBasisID || bundle.Pointer.GenerationID != query.GenerationID {
			return nil, nil, nil, false, errors.New("incomparable_basis: requested identity does not match the active proof")
		}
		mapIR, result, delta, err := decodeImpactBundle(bundle)
		return mapIR, result, delta, err == nil, err
	}
	if query.Freshness != "historical" {
		return nil, nil, nil, false, errors.New("invalid_precondition: freshness must be current or historical")
	}
	s.mu.Lock()
	mapIR := s.mapCache[query.GenerationID]
	if mapIR == nil {
		mapIR = s.mapCache[query.ComputedBasisID]
	}
	s.mu.Unlock()
	if mapIR == nil {
		return nil, nil, nil, false, errors.New("missing_precondition: exact historical generation is not available")
	}
	if mapIR.GenerationID != query.GenerationID || mapIR.ComputedBasisID != query.ComputedBasisID {
		return nil, nil, nil, false, errors.New("incomparable_basis: historical map identity does not match the request")
	}
	return mapIR, nil, nil, false, nil
}

func decodeImpactBundle(bundle *storage.ValidatedActiveProofBundle) (*semantic.SemanticMapIR, *evidence.Result, *semantic.SemanticDeltaIR, error) {
	var mapIR semantic.SemanticMapIR
	if err := json.Unmarshal(bundle.SemanticMap, &mapIR); err != nil {
		return nil, nil, nil, fmt.Errorf("current_proof_unavailable: decode semantic map: %w", err)
	}
	var result evidence.Result
	if err := json.Unmarshal(bundle.AnalyzerResult, &result); err != nil {
		return nil, nil, nil, fmt.Errorf("current_proof_unavailable: decode analyzer result: %w", err)
	}
	var delta *semantic.SemanticDeltaIR
	if len(bundle.SemanticDelta) > 0 {
		if err := contractharness.ValidateSemanticDeltaIR(bundle.SemanticDelta); err != nil {
			return nil, nil, nil, fmt.Errorf("current_proof_unavailable: semantic delta contract: %w", err)
		}
		delta = &semantic.SemanticDeltaIR{}
		if err := json.Unmarshal(bundle.SemanticDelta, delta); err != nil {
			return nil, nil, nil, fmt.Errorf("current_proof_unavailable: decode semantic delta: %w", err)
		}
	}
	return &mapIR, &result, delta, nil
}

func (s *Server) impactBatchStepRefs(target semantic.ImpactTarget, delta *semantic.SemanticDeltaIR, mapIR *semantic.SemanticMapIR) ([]string, error) {
	if target.ChangeBatchID == "" {
		return nil, nil
	}
	if delta == nil {
		return nil, errors.New("missing_precondition: change batch requires a validated semantic delta")
	}
	if delta.Status != "comparable" || delta.ToGeneration != mapIR.GenerationID || delta.CurrentComputedBasisID != mapIR.ComputedBasisID || delta.CurrentValidatedAgainstSnapshotID != "" && delta.CurrentValidatedAgainstSnapshotID != mapIR.ValidatedAgainstSnapshotID {
		return nil, errors.New("incomparable_basis: change batch does not belong to the requested generation")
	}
	batch, err := s.engine.GetBatch(target.ChangeBatchID)
	if err != nil {
		return nil, fmt.Errorf("missing_precondition: change batch %q is not available: %w", target.ChangeBatchID, err)
	}
	if batch.Status != "committed" {
		return nil, fmt.Errorf("missing_precondition: change batch %q is not committed", target.ChangeBatchID)
	}
	if batch.WorkspaceEpoch != mapIR.Basis.WorkspaceEpoch {
		return nil, errors.New("incomparable_basis: change batch workspace epoch does not match semantic map")
	}
	changedPaths := map[string]bool{}
	batchRevisionIDs := map[string]bool{}
	for _, revisionID := range batch.Revisions {
		revision, revisionErr := s.engine.GetRevision(revisionID)
		if revisionErr != nil {
			return nil, fmt.Errorf("missing_precondition: change batch revision %q is unavailable: %w", revisionID, revisionErr)
		}
		path := filepath.ToSlash(filepath.Clean(revision.Path))
		if path == "." || path == "" || filepath.IsAbs(path) || strings.HasPrefix(path, "../") {
			return nil, errors.New("invalid_graph: change batch revision path is invalid")
		}
		batchRevisionIDs[revisionID] = true
		changedPaths[path] = true
	}
	if len(changedPaths) == 0 {
		return nil, errors.New("missing_precondition: change batch has no revisions")
	}
	known := map[string]semantic.SemanticStep{}
	for _, step := range mapIR.Steps {
		known[step.StepID] = step
	}
	evidenceByID := map[string]semantic.SemanticEvidence{}
	for _, evidence := range mapIR.Evidence {
		if evidence.EvidenceID != "" {
			evidenceByID[evidence.EvidenceID] = evidence
		}
	}
	seen := map[string]bool{}
	refs := []string{}
	for _, change := range delta.Changes {
		if change.ValidationStatus != "verified" || change.EpistemicStatus == "unknown" || change.EpistemicStatus == "unobserved" {
			continue
		}
		ref := change.TargetStepID
		if change.ToStepID != "" {
			ref = change.ToStepID
		}
		step, exists := known[ref]
		if exists && changedPaths[filepath.ToSlash(filepath.Clean(step.Anchor.RepoRelativePath))] && impactStepHasBatchRevision(step, evidenceByID, batchRevisionIDs) && !seen[ref] {
			seen[ref] = true
			refs = append(refs, ref)
		}
	}
	if len(refs) == 0 {
		return nil, errors.New("not_observed: change batch has no target in the requested generation")
	}
	return refs, nil
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

func writeImpactError(w http.ResponseWriter, err error) {
	code := "invalid_precondition"
	status := http.StatusBadRequest
	message := err.Error()
	for _, candidate := range []string{"missing_precondition", "invalid_precondition", "incomparable_basis", "current_proof_unavailable", "not_observed", "invalid_graph", "invalid_authority", "internal_error"} {
		if strings.HasPrefix(message, candidate+":") || message == candidate {
			code = candidate
			break
		}
	}
	switch code {
	case "incomparable_basis":
		status = http.StatusUnprocessableEntity
	case "current_proof_unavailable":
		status = http.StatusConflict
	case "not_observed":
		status = http.StatusNotFound
	case "internal_error":
		status = http.StatusInternalServerError
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"code": code, "message": message})
}
