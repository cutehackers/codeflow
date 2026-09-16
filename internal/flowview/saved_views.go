package flowview

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"

	"codeflow/internal/contractharness"
	"codeflow/internal/fusion"
	"codeflow/internal/secret"
	"codeflow/internal/semantic"
)

// SaveTaskView is shared by HTTP and MCP. Scene construction and persistence
// always use the same analyzed map and source contexts, never browser guesses.
func (s *Server) SaveTaskView(ctx context.Context, result map[string]any) (map[string]any, error) {
	raw, err := json.Marshal(result["semanticMap"])
	if err != nil {
		return nil, err
	}
	var mapIR semantic.SemanticMapIR
	if err := json.Unmarshal(raw, &mapIR); err != nil {
		return nil, err
	}
	storyboard := semantic.BuildStoryboard(&mapIR)
	data, err := json.Marshal(storyboard)
	if err != nil {
		return nil, err
	}
	if err := contractharness.ValidateStoryboard(data); err != nil {
		return nil, fmt.Errorf("storyboard: %w", err)
	}
	result["storyboard"] = storyboard
	raw, err = json.Marshal(result)
	if err != nil {
		return nil, err
	}
	id, clean, err := s.storage.SaveView(ctx, raw)
	if err != nil {
		return nil, fmt.Errorf("save FlowView: %w", err)
	}
	var saved map[string]any
	if err := json.Unmarshal(clean, &saved); err != nil {
		return nil, err
	}
	saved["viewId"] = id
	return saved, nil
}

func (s *Server) SavedViewURL(id string) string {
	return s.URL() + "&viewId=" + url.QueryEscape(id)
}

func (s *Server) handleSavedViews(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	views, err := s.storage.ListViews(r.Context())
	if err != nil {
		writeTaskViewError(w, err, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"views": views})
}

// RestoreTaskView reads one immutable result without contacting an adapter.
func (s *Server) RestoreTaskView(ctx context.Context, id string) (map[string]any, error) {
	raw, err := s.storage.ReadView(ctx, id)
	if err != nil {
		return nil, err
	}
	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	result["viewId"] = id
	return result, nil
}

func (s *Server) handleSavedView(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	result, err := s.RestoreTaskView(r.Context(), r.URL.Query().Get("viewId"))
	if err != nil {
		writeTaskViewError(w, fmt.Errorf("저장된 분석을 열 수 없습니다: %w", err), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}

func (s *Server) handleSavedComparison(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	readMap := func(id string) (*semantic.SemanticMapIR, error) {
		raw, err := s.storage.ReadView(r.Context(), id)
		if err != nil {
			return nil, err
		}
		var result struct {
			Map semantic.SemanticMapIR `json:"semanticMap"`
		}
		if err := json.Unmarshal(raw, &result); err != nil {
			return nil, err
		}
		return &result.Map, nil
	}
	baseline, err := readMap(r.URL.Query().Get("baselineId"))
	if err != nil {
		writeTaskViewError(w, err, http.StatusBadRequest)
		return
	}
	current, err := readMap(r.URL.Query().Get("viewId"))
	if err != nil {
		writeTaskViewError(w, err, http.StatusBadRequest)
		return
	}
	delta, err := semantic.ComputeSemanticDelta("comparison-"+r.URL.Query().Get("viewId"), baseline, current)
	if err != nil {
		writeTaskViewError(w, err, http.StatusConflict)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(delta)
}

// RestoreLegacyFlow adapts only persisted facts. Missing historical source is
// explicit and never replaced with source from the current working tree.
func (s *Server) RestoreLegacyFlow(ctx context.Context, flowID string) (map[string]any, error) {
	if flowID == "" || filepath.Base(flowID) != flowID || strings.ContainsAny(flowID, "/\\") || flowID == "." || flowID == ".." {
		return nil, fmt.Errorf("invalid flow ID")
	}
	views, err := s.storage.ListViews(ctx)
	if err != nil {
		return nil, err
	}
	for _, view := range views {
		raw, err := s.storage.ReadView(ctx, view.ViewID)
		if err != nil {
			return nil, err
		}
		var doc map[string]any
		if err := json.Unmarshal(raw, &doc); err != nil {
			return nil, err
		}
		req, _ := doc["request"].(map[string]any)
		if req["flowId"] == flowID {
			doc["viewId"] = view.ViewID
			return doc, nil
		}
	}
	pointer, err := s.storage.ReadPointer()
	if err != nil {
		return nil, err
	}
	if pointer == nil {
		return nil, fmt.Errorf("saved flow not found")
	}
	raw, err := s.storage.ReadFlowSpec(pointer.GenerationID, flowID)
	if err != nil {
		return nil, err
	}
	var spec fusion.FlowSpec
	if err := json.Unmarshal(raw, &spec); err != nil {
		return nil, err
	}
	m := &semantic.SemanticMapIR{GenerationID: pointer.GenerationID, ComputedBasisID: spec.BasisSha, Summary: semantic.MapSummary{Requested: spec.Title, Current: spec.Description}, Steps: []semantic.SemanticStep{}, Edges: []semantic.SemanticEdge{}, Unknowns: spec.Unknowns, Freshness: "historical", Authority: "historical"}
	for _, step := range spec.Steps {
		id := fusion.ComputeStepID(spec.FlowID, step.Ordinal, step.Anchor.EnclosingSymbolPath)
		if step.StepID != nil {
			id = *step.StepID
		}
		m.Steps = append(m.Steps, semantic.SemanticStep{StepID: id, Ordinal: step.Ordinal, Name: step.Name, TechnicalName: step.Anchor.EnclosingSymbolPath, Anchor: step.Anchor, Branch: step.Branch, SideEffect: step.SideEffect, StateDelta: step.StateDelta, Kind: step.Kind, Layer: step.Layer, Rules: step.Rules})
	}
	for _, edge := range spec.Edges {
		var from *semantic.SemanticStep
		var targets []semantic.SemanticStep
		for i := range m.Steps {
			step := &m.Steps[i]
			if edge.StepOrdinal != nil && step.Ordinal == *edge.StepOrdinal {
				from = step
			}
			if edge.ToSymbolPath == step.TechnicalName || edge.ToSymbolPath == step.Anchor.RepoRelativePath+"#"+step.TechnicalName {
				targets = append(targets, *step)
			}
		}
		if from != nil && len(targets) == 1 && edge.ResolutionStatus == "resolved" {
			m.Edges = append(m.Edges, semantic.SemanticEdge{FromStepID: from.StepID, ToStepID: targets[0].StepID, ToSymbolPath: edge.ToSymbolPath, Kind: edge.Kind, ResolutionStatus: edge.ResolutionStatus})
		} else {
			m.Unknowns = append(m.Unknowns, fusion.Unknown{Subject: edge.ToSymbolPath, Reason: "과거 분석의 직접 연결 대상을 확인할 수 없습니다."})
		}
	}

	entry := ""
	if len(spec.Steps) > 0 {
		entry = spec.Steps[0].Anchor.RepoRelativePath + "#" + spec.Steps[0].Anchor.EnclosingSymbolPath
	}
	return map[string]any{"semanticMap": m, "storyboard": semantic.BuildStoryboard(m), "flowContexts": map[string]any{}, "request": map[string]any{"flowId": flowID, "entrySymbol": entry}, "sourceNotice": "과거 분석의 소스 문맥이 보존되지 않았습니다. 현재 코드는 다시 분석해 확인하세요."}, nil
}

func (s *Server) handleLegacyView(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	doc, err := s.RestoreLegacyFlow(r.Context(), r.URL.Query().Get("flowId"))
	if err != nil {
		writeTaskViewError(w, err, http.StatusNotFound)
		return
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		writeTaskViewError(w, err, http.StatusInternalServerError)
		return
	}
	clean, _, err := secret.RedactJSON(raw)
	if err != nil {
		writeTaskViewError(w, err, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(clean)
}
