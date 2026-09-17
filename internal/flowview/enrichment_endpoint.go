package flowview

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"codeflow/internal/curator"
)

type semanticEnrichmentHTTPRequest struct {
	FrameID      string              `json:"frameId,omitempty"`
	TargetStepID string              `json:"targetStepId,omitempty"`
	Frames       []curator.FlowFrame `json:"frames,omitempty"`
}

type semanticEnrichmentHTTPResponse struct {
	Status     string                      `json:"status"` // enriched | fallback | unavailable
	Narratives []curator.EnrichedNarrative `json:"narratives,omitempty"`
}

func (s *Server) serveSemanticEnrichment(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req semanticEnrichmentHTTPRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err.Error() != "EOF" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")

	// Explicit opt-in principle (Option A): disabled by default for predictable static AST + CodeGraph pipeline.
	cfg := curator.SLMConfig{
		Enabled:   false,
		Endpoint:  "http://localhost:11434/v1",
		Model:     "qwen2.5-coder:1.5b",
		TimeoutMs: 500,
	}

	if s.repoRoot != "" {
		cfgPath := filepath.Join(s.repoRoot, "codeflow.config.json")
		if data, err := os.ReadFile(cfgPath); err == nil {
			var configWrapper struct {
				SLM *curator.SLMConfig `json:"slm"`
			}
			if err := json.Unmarshal(data, &configWrapper); err == nil && configWrapper.SLM != nil {
				if configWrapper.SLM.Endpoint == "" {
					configWrapper.SLM.Endpoint = cfg.Endpoint
				}
				if configWrapper.SLM.Model == "" {
					configWrapper.SLM.Model = cfg.Model
				}
				if configWrapper.SLM.TimeoutMs <= 0 {
					configWrapper.SLM.TimeoutMs = cfg.TimeoutMs
				}
				cfg = *configWrapper.SLM
			}
		}
	}

	// Environment variable overrides config file (e.g. for CI or CLI flags)
	if envOptIn := os.Getenv("CODEFLOW_SLM_ENABLED"); envOptIn != "" {
		cfg.Enabled = (envOptIn == "1" || strings.EqualFold(envOptIn, "true"))
	}

	enricher := curator.NewExternalHTTPEnricher(cfg)
	if !enricher.IsAvailable(r.Context()) {
		_ = json.NewEncoder(w).Encode(semanticEnrichmentHTTPResponse{
			Status: "unavailable",
		})
		return
	}

	narratives, err := enricher.EnrichStoryboard(r.Context(), req.Frames)
	if err != nil || len(narratives) == 0 {
		_ = json.NewEncoder(w).Encode(semanticEnrichmentHTTPResponse{
			Status: "fallback",
		})
		return
	}

	_ = json.NewEncoder(w).Encode(semanticEnrichmentHTTPResponse{
		Status:     "enriched",
		Narratives: narratives,
	})
}
