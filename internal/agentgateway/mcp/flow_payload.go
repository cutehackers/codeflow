package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"codeflow/internal/collector/contractharness"
	"codeflow/internal/curator"
	"codeflow/internal/curator/semantic"
)

// flowViewPayload reads one immutable analysis without starting an analyzer or
// replacing it with whichever analysis of the same flow was saved most recently.
func (s *Server) flowViewPayload(ctx context.Context, args map[string]any, viewID string) (any, error) {
	for _, field := range []string{"flowId", "entrySymbolPath"} {
		if value, _ := args[field].(string); value != "" {
			return nil, fmt.Errorf("viewId cannot be combined with %s", field)
		}
	}
	store, err := s.getStorage(s.resolveTarget(args["target"]))
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
	return curator.BuildCompactAnalysisPayload(&model, &sequence)
}
