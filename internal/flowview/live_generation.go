package flowview

import (
	"encoding/json"
	"net/http"
	"strings"

	"codeflow/internal/semantic"
	"codeflow/internal/storage"
)

type liveGenerationView struct {
	SemanticMap   semantic.SemanticMapIR            `json:"semanticMap"`
	Projection    semantic.FlowViewProjection       `json:"projection"`
	SemanticDelta *semantic.SemanticDeltaIR         `json:"semanticDelta,omitempty"`
	ProofManifest storage.GenerationProofManifest   `json:"proofManifest"`
	FlowContexts  map[string]*FlowContextProjection `json:"flowContexts"`
	ViewState     map[string]any                    `json:"viewState"`
}

func (s *Server) handleLiveGeneration(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	generationID := strings.TrimSpace(r.URL.Query().Get("generationId"))
	basisID := strings.TrimSpace(r.URL.Query().Get("computedBasisId"))
	snapshotID := strings.TrimSpace(r.URL.Query().Get("snapshotId"))
	if generationID == "" || basisID == "" || snapshotID == "" {
		writeGenerationUnavailable(w)
		return
	}
	bundle, err := s.storage.ReadValidatedActiveProofBundle()
	if err != nil || bundle == nil || bundle.Manifest == nil || bundle.Pointer == nil ||
		bundle.Manifest.GenerationID != generationID || bundle.Manifest.ComputedBasisID != basisID ||
		bundle.Manifest.ValidatedAgainstSnapshotID != snapshotID || bundle.Pointer.GenerationID != generationID ||
		bundle.Pointer.ComputedBasisID != basisID || bundle.Pointer.ValidatedAgainstSnapshotID != snapshotID {
		writeGenerationUnavailable(w)
		return
	}
	var view liveGenerationView
	if err := json.Unmarshal(bundle.SemanticMap, &view.SemanticMap); err != nil {
		writeGenerationUnavailable(w)
		return
	}
	if err := json.Unmarshal(bundle.Projection, &view.Projection); err != nil {
		writeGenerationUnavailable(w)
		return
	}
	var persisted persistedLiveView
	if len(bundle.LiveView) == 0 || json.Unmarshal(bundle.LiveView, &persisted) != nil ||
		persisted.GenerationID != generationID || persisted.ComputedBasisID != basisID || persisted.SnapshotID != bundle.Manifest.ComputedSnapshotID {
		writeGenerationUnavailable(w)
		return
	}
	view.FlowContexts = persisted.FlowContexts
	if len(bundle.SemanticDelta) > 0 {
		var delta semantic.SemanticDeltaIR
		if err := json.Unmarshal(bundle.SemanticDelta, &delta); err != nil {
			writeGenerationUnavailable(w)
			return
		}
		view.SemanticDelta = &delta
	}
	view.ProofManifest = *bundle.Manifest
	view.ViewState = map[string]any{
		"generationId":               generationID,
		"computedBasisId":            basisID,
		"validatedAgainstSnapshotId": snapshotID,
		"readingFixed":               false,
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(view)
}

func writeGenerationUnavailable(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusConflict)
	_ = json.NewEncoder(w).Encode(map[string]string{"code": "generation_unavailable", "message": "요청한 검증 세대의 저장된 화면을 사용할 수 없습니다."})
}
