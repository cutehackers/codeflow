package flowview

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"codeflow/internal/curator"
)

type semanticLabelsHTTPRequest struct {
	FlowID     string                      `json:"flowID"`
	SnapshotID string                      `json:"snapshotID"`
	Frames     []curator.FlowSequenceFrame `json:"frames,omitempty"`
}

type semanticLabelsHTTPResponse struct {
	RequestID  string                   `json:"requestID"`
	FlowID     string                   `json:"flowID"`
	SnapshotID string                   `json:"snapshotID"`
	Status     string                   `json:"status"` // proposed | fallback | timed_out
	Labels     []curator.FlowFrameLabel `json:"labels"`
}

func (s *Server) serveSemanticLabels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req semanticLabelsHTTPRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err.Error() != "EOF" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if req.FlowID == "" || req.SnapshotID == "" || len(req.Frames) == 0 {
		http.Error(w, "flowID, snapshotID, and frames are required", http.StatusBadRequest)
		return
	}
	requestID := fmt.Sprintf("label-%d", time.Now().UnixNano())

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

	labeler := curator.NewSLMLabeler(cfg)
	if !labeler.IsAvailable(r.Context()) {
		_ = json.NewEncoder(w).Encode(semanticLabelsHTTPResponse{
			RequestID: requestID, FlowID: req.FlowID, SnapshotID: req.SnapshotID,
			Status: "fallback", Labels: fallbackLabels(req.Frames),
		})
		return
	}

	labels, err := labeler.LabelFlowSequence(r.Context(), req.Frames)
	if err != nil || len(labels) == 0 {
		_ = json.NewEncoder(w).Encode(semanticLabelsHTTPResponse{
			RequestID: requestID, FlowID: req.FlowID, SnapshotID: req.SnapshotID,
			Status: "fallback", Labels: fallbackLabels(req.Frames),
		})
		return
	}

	_ = json.NewEncoder(w).Encode(semanticLabelsHTTPResponse{
		RequestID: requestID, FlowID: req.FlowID, SnapshotID: req.SnapshotID,
		Status: labelStatus(labels), Labels: labels,
	})
}

func fallbackLabels(frames []curator.FlowSequenceFrame) []curator.FlowFrameLabel {
	labels := make([]curator.FlowFrameLabel, len(frames))
	for i, frame := range frames {
		labels[i] = curator.FlowFrameLabel{FrameID: frame.FrameID, Text: frame.Title, Status: "fallback"}
	}
	return labels
}

func labelStatus(labels []curator.FlowFrameLabel) string {
	for _, label := range labels {
		if label.Status == "timed_out" {
			return "timed_out"
		}
	}
	for _, label := range labels {
		if label.Status == "fallback" {
			return "fallback"
		}
	}
	return "proposed"
}
