package flowview

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"codeflow/internal/contractharness"
	"codeflow/internal/protocol"
	"codeflow/internal/semantic"
	"codeflow/internal/storage"
)

type semanticEnrichmentHTTPRequest struct {
	GenerationID     string   `json:"generationId,omitempty"`
	ComputedBasisID  string   `json:"computedBasisId,omitempty"`
	TargetStepID     string   `json:"targetStepId,omitempty"`
	TargetStepIDs    []string `json:"targetStepIds,omitempty"`
	TargetSymbolPath string   `json:"targetSymbolPath,omitempty"`
	ScopePaths       []string `json:"scopePaths,omitempty"`
	PromptRevision   string   `json:"promptRevision,omitempty"`
}

// handleSemanticEnrichment is the optional FlowView Q4 seam. It resolves the
// deterministic map and immutable snapshot already owned by this server and
// never accepts a caller-supplied map, source bytes, or repository path for a
// model request.
func (s *Server) handleSemanticEnrichment(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		semanticJSONError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	var request semanticEnrichmentHTTPRequest
	decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		semanticJSONError(w, http.StatusBadRequest, "invalid_request", fmt.Sprintf("decode enrichment request: %v", err))
		return
	}
	mapIR := s.cachedSemanticMap(request.GenerationID, request.ComputedBasisID)
	if mapIR == nil {
		writeSemanticEnrichment(w, semantic.EnrichmentResult{State: semantic.EnrichmentState{
			SchemaID: semantic.EnrichmentStateV2SchemaID, SchemaVersion: 2, Status: "unavailable",
			Reason: "no current deterministic semantic map is available", Capability: protocolUnavailableCapability(), UpdatedAt: nowRFC3339Nano(),
		}})
		return
	}
	requestCtx, modelHostFactory, releaseModelHost, err := s.beginModelHostRequest(r.Context())
	if err != nil {
		fallback := semantic.BuildDeterministicFallback(mapIR, request.TargetStepID, err.Error())
		writeSemanticEnrichment(w, semantic.EnrichmentResult{State: semantic.EnrichmentState{
			SchemaID: semantic.EnrichmentStateV2SchemaID, SchemaVersion: 2, Status: "unavailable",
			Reason: err.Error(), Capability: protocolUnavailableCapability(), Fallback: fallback, UpdatedAt: nowRFC3339Nano(),
		}, Fallback: fallback})
		return
	}
	defer releaseModelHost()
	snapshot, _, release, err := s.captureAnalysisSnapshot(requestCtx)
	if err != nil {
		fallback := semantic.BuildDeterministicFallback(mapIR, request.TargetStepID, err.Error())
		result := semantic.EnrichmentResult{State: semantic.EnrichmentState{SchemaID: semantic.EnrichmentStateV2SchemaID, SchemaVersion: 2, Status: "unavailable", Reason: err.Error(), Capability: protocolUnavailableCapability(), Fallback: fallback, UpdatedAt: nowRFC3339Nano()}, Fallback: fallback}
		writeSemanticEnrichment(w, result)
		return
	}
	defer release()
	publication := s.currentSemanticPublication()
	targetIDs := append([]string(nil), request.TargetStepIDs...)
	if request.TargetStepID != "" {
		targetIDs = append(targetIDs, request.TargetStepID)
	}
	var currentProof *storage.GenerationProofManifest
	var currentProofBytes, semanticMapBytes []byte
	var currentPointer *storage.ActivePointer
	if publication != nil {
		currentProof, currentProofBytes, currentPointer, semanticMapBytes = publication.Manifest, publication.ManifestBytes, publication.Pointer, publication.SemanticMap
	}
	result, persistErr := semantic.RunSemanticEnrichmentAndPersist(requestCtx, semantic.EnrichmentRequest{
		EvidencePack:     semantic.EvidencePackRequest{Map: mapIR, Snapshot: snapshot, CurrentProof: currentProof, CurrentProofBytes: currentProofBytes, CurrentPointer: currentPointer, SemanticMapBytes: semanticMapBytes, LiveHeadSnapshotID: snapshot.SnapshotID, TargetStepIDs: targetIDs, TargetSymbolPath: request.TargetSymbolPath, ScopePaths: request.ScopePaths, PromptRevision: request.PromptRevision},
		ModelHostFactory: modelHostFactory, PromptRevision: request.PromptRevision,
	}, s.proposalStore, s.approvalWorkspaceID)
	if persistErr != nil {
		semanticJSONError(w, http.StatusInternalServerError, "enrichment_persistence_failed", "available semantic enrichment could not be persisted")
		return
	}
	writeSemanticEnrichment(w, result)
}

// currentSemanticPublication is the only FlowView source for current proof
// authority. A failed or absent publication is deliberately represented by
// nil values so the semantic seam returns its deterministic fallback.
func (s *Server) currentSemanticPublication() *storage.ValidatedActiveProofBundle {
	if s == nil || s.storage == nil {
		return nil
	}
	bundle, err := s.storage.ReadValidatedActiveProofBundle()
	if err != nil {
		return nil
	}
	return bundle
}

func (s *Server) cachedSemanticMap(generationID, basisID string) *semantic.SemanticMapIR {
	s.mu.Lock()
	// An explicit identity is an exact lookup, not a hint. When both values
	// are present the same map must satisfy both bindings. In particular, do
	// not fall through to an arbitrary map entry when a requested generation or
	// basis is unknown.
	if generationID != "" || basisID != "" {
		candidates := make([]*semantic.SemanticMapIR, 0, 2)
		if generationID != "" {
			candidates = append(candidates, s.mapCache[generationID])
		}
		if basisID != "" {
			candidates = append(candidates, s.mapCache[basisID])
		}
		for _, mapIR := range candidates {
			if semanticMapMatchesIdentity(mapIR, generationID, basisID) {
				s.mu.Unlock()
				return mapIR
			}
		}
	}
	s.mu.Unlock()
	// A lazily-created coordinator may not yet have an in-memory map cache,
	// while its durable publication is already authoritative. Recover only the
	// validated active semantic map, and require the caller's identity to match
	// before using it for enrichment.
	if s.storage != nil {
		if bundle, err := s.storage.ReadValidatedActiveProofBundle(); err == nil && bundle != nil && len(bundle.SemanticMap) > 0 {
			if bundle.Pointer != nil && bundle.Manifest != nil {
				if err := contractharness.ValidateSemanticMapIR(bundle.SemanticMap); err != nil {
					return nil
				}
				var mapIR semantic.SemanticMapIR
				if err := json.Unmarshal(bundle.SemanticMap, &mapIR); err == nil &&
					mapIR.GenerationID == bundle.Pointer.GenerationID && mapIR.ComputedBasisID == bundle.Pointer.ComputedBasisID &&
					mapIR.GenerationID == bundle.Manifest.GenerationID && mapIR.ComputedBasisID == bundle.Manifest.ComputedBasisID &&
					semanticMapMatchesIdentity(&mapIR, generationID, basisID) {
					return &mapIR
				}
			}
		}
	}
	return nil
}

func semanticMapMatchesIdentity(mapIR *semantic.SemanticMapIR, generationID, basisID string) bool {
	if mapIR == nil {
		return false
	}
	return (generationID == "" || mapIR.GenerationID == generationID) &&
		(basisID == "" || mapIR.ComputedBasisID == basisID)
}

func writeSemanticEnrichment(w http.ResponseWriter, result semantic.EnrichmentResult) {
	clean, err := semantic.MarshalEnrichmentResultEgress(&result)
	if err != nil {
		semanticJSONError(w, http.StatusInternalServerError, "invalid_enrichment_result", err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(clean)
}

func semanticJSONError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"code": code, "message": message})
}

func nowRFC3339Nano() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}

func protocolUnavailableCapability() protocol.ModelHostCapability {
	return protocol.ModelHostCapability{Status: "unavailable"}
}
