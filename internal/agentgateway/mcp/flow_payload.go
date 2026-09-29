package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"codeflow/internal/collector/contractharness"
	"codeflow/internal/collector/fusion"
	"codeflow/internal/curator"
	"codeflow/internal/curator/semantic"
	"codeflow/internal/presenter/flowview"
)

// flowViewPayload reads one immutable analysis without starting an analyzer or
// replacing it with whichever analysis of the same flow was saved most recently.
func (s *Server) flowViewPayload(ctx context.Context, args map[string]any, viewID string) (any, error) {
	for _, field := range []string{"flowId", "entrySymbolPath"} {
		if value, _ := args[field].(string); value != "" {
			return nil, fmt.Errorf("viewId cannot be combined with %s", field)
		}
	}
	target := s.resolveTarget(args["target"])
	store, err := s.getStorage(target)
	if err != nil {
		return nil, err
	}
	raw, err := store.ReadView(ctx, viewID)
	if err != nil {
		return nil, fmt.Errorf("read FlowView: %w", err)
	}
	var view map[string]any
	if err := json.Unmarshal(raw, &view); err != nil {
		return nil, fmt.Errorf("decode FlowView: %w", err)
	}
	data, err := json.Marshal(view["flowSequence"])
	if err != nil {
		return nil, fmt.Errorf("decode FlowSequence: %w", err)
	}
	if err := contractharness.ValidateFlowSequence(data); err != nil {
		return nil, fmt.Errorf("FlowSequence: %w", err)
	}
	flowview.FormatTaskViewResponse(view, target)
	if format, _ := args["format"].(string); format != "compact" && args["compact"] != true {
		view["viewId"] = viewID
		return view, nil
	}
	var sequence semantic.FlowSequence
	if err := json.Unmarshal(data, &sequence); err != nil {
		return nil, fmt.Errorf("decode FlowSequence: %w", err)
	}
	modelData, err := json.Marshal(view["semanticMap"])
	if err != nil {
		return nil, fmt.Errorf("decode source analysis: %w", err)
	}
	var model semantic.SemanticMapIR
	if err := json.Unmarshal(modelData, &model); err != nil {
		return nil, fmt.Errorf("decode source analysis: %w", err)
	}
	compact, err := curator.BuildCompactAnalysisPayload(&model, &sequence)
	if err != nil {
		resolutionMissing, _ := view["flowResolutionMissing"].(bool)
		sourceMissing, _ := view["sourceContextMissing"].(bool)
		if !resolutionMissing || !sourceMissing {
			return nil, err
		}
		// Historical views can retain a valid hierarchy without a complete
		// semantic map. Keep only that hierarchy and its limitations.
		compact = curator.BuildCompactPayload(&sequence, -1, -1)
	}
	if resolution := view["flowResolution"]; resolution != nil {
		resolutionBytes, err := json.Marshal(resolution)
		if err != nil {
			return nil, fmt.Errorf("encode flow resolution: %w", err)
		}
		var proven fusion.FlowResolution
		if err := json.Unmarshal(resolutionBytes, &proven); err != nil {
			return nil, fmt.Errorf("decode flow resolution: %w", err)
		}
		compact.FlowResolution = &proven
	}
	if requestedFocus := view["requestedFocus"]; requestedFocus != nil {
		focusBytes, err := json.Marshal(requestedFocus)
		if err != nil {
			return nil, fmt.Errorf("encode requested focus: %w", err)
		}
		var selected semantic.RequestedFlowFocus
		if err := json.Unmarshal(focusBytes, &selected); err != nil {
			return nil, fmt.Errorf("decode requested focus: %w", err)
		}
		compact.RequestedFocus = &selected
	}
	if readingPath := view["codeReadingPath"]; readingPath != nil {
		pathBytes, err := json.Marshal(readingPath)
		if err != nil {
			return nil, fmt.Errorf("encode code reading path: %w", err)
		}
		var path semantic.CodeReadingPath
		if err := json.Unmarshal(pathBytes, &path); err != nil {
			return nil, fmt.Errorf("decode code reading path: %w", err)
		}
		compact.CodeReadingPath = &path
	}
	compact.FlowResolutionMissing, _ = view["flowResolutionMissing"].(bool)
	compact.ResolutionLimitation, _ = view["resolutionLimitation"].(string)
	compact.CodeReadingPathLimitation, _ = view["codeReadingPathLimitation"].(string)
	compact.SourceContextMissing, _ = view["sourceContextMissing"].(bool)
	compact.NeedsReanalysis, _ = view["needsReanalysis"].(bool)
	return compact, nil
}
