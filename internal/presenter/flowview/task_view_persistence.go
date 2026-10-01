package flowview

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"codeflow/internal/collector/contractharness"
	"codeflow/internal/collector/fusion"
	"codeflow/internal/curator/semantic"
)

var errUnsupportedFlowSequenceSchema = errors.New("unsupported_flow_sequence_schema")

func matchFlowID(storedID, requestedID string) bool {
	if storedID == requestedID {
		return true
	}
	sNorm := strings.TrimPrefix(strings.TrimPrefix(storedID, "cand-"), "flow-")
	rNorm := strings.TrimPrefix(strings.TrimPrefix(requestedID, "cand-"), "flow-")
	return sNorm != "" && rNorm != "" && sNorm == rNorm
}

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
	if err := validateRequestedFocus(result, &mapIR); err != nil {
		return nil, fmt.Errorf("requestedFocus: %w", err)
	}
	flowSequence, supplied := result["flowSequence"]
	if !supplied {
		flowSequence = semantic.BuildFlowSequence(&mapIR)
	}
	data, err := json.Marshal(flowSequence)
	if err != nil {
		return nil, err
	}
	if err := contractharness.ValidateFlowSequence(data); err != nil {
		return nil, fmt.Errorf("flowSequence: %w", err)
	}
	var sequence semantic.FlowSequence
	if err := json.Unmarshal(data, &sequence); err != nil {
		return nil, fmt.Errorf("decode FlowSequence: %w", err)
	}
	if err := semantic.ValidateFlowSequenceReferences(&mapIR, &sequence); err != nil {
		return nil, fmt.Errorf("flowSequence: %w", err)
	}
	if err := validateCodeReadingPathForSave(result, &mapIR, &sequence); err != nil {
		return nil, fmt.Errorf("codeReadingPath: %w", err)
	}
	if err := validateRequestedFlowOverviewForSave(result, &mapIR, &sequence); err != nil {
		return nil, fmt.Errorf("requestedFlowOverview: %w", err)
	}
	// Preserve caller-owned data, including optional compatible fields, until
	// validation and persistence have succeeded.
	view := make(map[string]any, len(result)+1)
	for key, value := range result {
		view[key] = value
	}
	view["flowSequence"] = flowSequence
	stripDeprecatedArchitecture(view)
	applyCodeReadingPathLimitation(view, &mapIR)
	raw, err = json.Marshal(view)
	if err != nil {
		return nil, err
	}
	id, clean, err := s.storage.SaveView(ctx, raw)
	if err != nil {
		return nil, fmt.Errorf("save FlowView: %w", err)
	}
	var persistedView map[string]any
	if err := json.Unmarshal(clean, &persistedView); err != nil {
		return nil, err
	}
	persistedView["viewId"] = id
	return persistedView, nil
}

func validateRequestedFocus(result map[string]any, mapIR *semantic.SemanticMapIR) error {
	requestRaw, err := json.Marshal(result["request"])
	if err != nil {
		return err
	}
	var request semantic.FeatureQueryParams
	if err := json.Unmarshal(requestRaw, &request); err != nil {
		return err
	}
	verified, err := semantic.ResolveRequestedFlowFocus(mapIR, request.FocusSymbol, request.FocusReason)
	if err != nil {
		return err
	}
	provided, exists := result["requestedFocus"]
	if verified == nil {
		if exists && provided != nil {
			return fmt.Errorf("focus is present without a verified selection")
		}
		return nil
	}
	if !exists || provided == nil {
		return fmt.Errorf("verified selection is missing")
	}
	raw, err := json.Marshal(provided)
	if err != nil {
		return err
	}
	var selected semantic.RequestedFlowFocus
	if err := json.Unmarshal(raw, &selected); err != nil {
		return err
	}
	if selected.SymbolPath != verified.SymbolPath || selected.Reason != verified.Reason ||
		selected.CallStepID != verified.CallStepID || selected.EntryStepID != verified.EntryStepID ||
		selected.SnapshotID != verified.SnapshotID || !slices.Equal(selected.EvidenceRefs, verified.EvidenceRefs) {
		return fmt.Errorf("selection does not match analysis evidence")
	}
	return nil
}

// TaskViewURL returns the interactive web UI URL pointing to a persisted task view.
func (s *Server) TaskViewURL(id string) string {
	return s.URL() + "&viewId=" + url.QueryEscape(id)
}

func (s *Server) serveTaskViewList(w http.ResponseWriter, r *http.Request) {
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

func markTaskViewSourceContextMissing(result map[string]any, repoRoot string) {
	if result == nil {
		return
	}
	contexts, _ := result["flowContexts"].(map[string]any)
	sources, _ := result["sourceFiles"].(map[string]any)

	missingDisk := false
	if repoRoot != "" && (len(contexts) > 0 || len(sources) > 0) {
		anyFileFound := false
		fileCheckedCount := 0
		checkPath := func(p string) {
			if p == "" {
				return
			}
			fileCheckedCount++
			fullPath := filepath.Join(repoRoot, p)
			if _, err := os.Stat(fullPath); err == nil {
				anyFileFound = true
			}
		}
		for path := range sources {
			checkPath(path)
		}
		if fileCheckedCount == 0 {
			for _, c := range contexts {
				if cmap, ok := c.(map[string]any); ok {
					if cp, ok := cmap["canonicalPath"].(string); ok {
						checkPath(cp)
					} else if fp, ok := cmap["filePath"].(string); ok {
						checkPath(fp)
					}
				}
			}
		}
		if fileCheckedCount > 0 && !anyFileFound {
			missingDisk = true
		}
	}

	if (len(contexts) == 0 && len(sources) == 0) || missingDisk {
		result["sourceContextMissing"] = true
		result["needsReanalysis"] = true
		if result["sourceNotice"] == nil || result["sourceNotice"] == "" || missingDisk {
			if missingDisk {
				result["sourceNotice"] = "과거 분석의 소스 파일이 디스크에서 삭제되었습니다. 현재 코드를 확인하려면 다시 분석을 실행하세요."
			} else {
				result["sourceNotice"] = "과거 분석에 보존된 소스 문맥이 없어 재분석이 필요합니다. 현재 코드를 확인하려면 다시 분석을 실행하세요."
			}
		}
	}
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
	flowSequence, ok := result["flowSequence"]
	if !ok {
		return nil, errUnsupportedFlowSequenceSchema
	}
	flowSequenceRaw, err := json.Marshal(flowSequence)
	if err != nil {
		return nil, fmt.Errorf("validate FlowSequence: %w", err)
	}
	if err := contractharness.ValidateFlowSequence(flowSequenceRaw); err != nil {
		return nil, fmt.Errorf("%w: %v", errUnsupportedFlowSequenceSchema, err)
	}
	result["viewId"] = id

	FormatTaskViewResponse(result, s.repoRoot)
	return result, nil
}

func stripDeprecatedArchitecture(m map[string]any) {
	if m == nil {
		return
	}
	delete(m, "architecture")
	if fs, ok := m["flowSequence"].(map[string]any); ok {
		delete(fs, "architecture")
		if frames, ok := fs["frames"].([]any); ok {
			for _, f := range frames {
				if fMap, ok := f.(map[string]any); ok {
					delete(fMap, "architecture")
				}
			}
		}
	}
	if sm, ok := m["semanticMap"].(map[string]any); ok {
		delete(sm, "architecture")
	}
}

// FormatTaskViewResponse applies current read semantics to one decoded saved
// View. Callers own the map; the stored JSON is never rewritten.
func FormatTaskViewResponse(view map[string]any, repoRoot string) {
	stripDeprecatedArchitecture(view)
	if view == nil {
		return
	}
	if view["requestedFlowOverview"] != nil {
		var model semantic.SemanticMapIR
		var sequence semantic.FlowSequence
		modelErr := decodeTaskViewPart(view["semanticMap"], &model)
		sequenceErr := decodeTaskViewPart(view["flowSequence"], &sequence)
		if modelErr != nil || sequenceErr != nil || validateRequestedFlowOverviewForSave(view, &model, &sequence) != nil {
			delete(view, "requestedFlowOverview")
			view["requestedFlowOverviewLimitation"] = "저장된 처리 구간 개요가 근거 검사를 통과하지 못했습니다. 문장별 탐색에서 원본 분석을 확인하세요."
		}
	}
	if view["codeReadingPath"] != nil {
		var model semantic.SemanticMapIR
		var sequence semantic.FlowSequence
		modelErr := decodeTaskViewPart(view["semanticMap"], &model)
		sequenceErr := decodeTaskViewPart(view["flowSequence"], &sequence)
		if modelErr != nil || sequenceErr != nil || validateCodeReadingPathForSave(view, &model, &sequence) != nil {
			delete(view, "codeReadingPath")
			view["codeReadingPathLimitation"] = "저장된 코드 읽기 경로가 현재 근거 검사를 통과하지 못했습니다. 전체 분석을 표시합니다."
		} else {
			applyCodeReadingPathLimitation(view, &model)
		}
	}
	if resolution, hasResolution := view["flowResolution"]; !hasResolution || resolution == nil {
		view["flowResolutionMissing"] = true
		view["resolutionLimitation"] = "과거 분석으로 요청 확정 기록이 존재하지 않습니다."
	}
	markTaskViewSourceContextMissing(view, repoRoot)
}

func (s *Server) serveTaskViewDetail(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	result, err := s.RestoreTaskView(r.Context(), r.URL.Query().Get("viewId"))
	if err != nil {
		status := http.StatusNotFound
		if errors.Is(err, errUnsupportedFlowSequenceSchema) {
			status = http.StatusBadRequest
		}
		writeTaskViewError(w, fmt.Errorf("저장된 분석을 열 수 없습니다: %w", err), status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}

func (s *Server) serveTaskViewComparison(w http.ResponseWriter, r *http.Request) {
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
			Map          semantic.SemanticMapIR `json:"semanticMap"`
			FlowSequence json.RawMessage        `json:"flowSequence"`
		}
		if err := json.Unmarshal(raw, &result); err != nil {
			return nil, err
		}
		if len(result.FlowSequence) == 0 {
			return nil, errUnsupportedFlowSequenceSchema
		}
		if err := contractharness.ValidateFlowSequence(result.FlowSequence); err != nil {
			return nil, fmt.Errorf("%w: %v", errUnsupportedFlowSequenceSchema, err)
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

// RestoreFlowView builds a current FlowView result from the active FlowSpec.
// Stored views are used only when they contain the current FlowSequence schema.
func (s *Server) RestoreFlowView(ctx context.Context, flowID string) (map[string]any, error) {
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
		flowSequenceRaw, err := json.Marshal(doc["flowSequence"])
		if err != nil || contractharness.ValidateFlowSequence(flowSequenceRaw) != nil {
			continue
		}
		req, _ := doc["request"].(map[string]any)
		storedFlowID := fmt.Sprint(req["flowId"])
		if storedFlowID == "" || storedFlowID == "<nil>" {
			storedFlowID = fmt.Sprint(doc["flowId"])
		}
		if matchFlowID(storedFlowID, flowID) {
			doc["viewId"] = view.ViewID
			if strings.HasPrefix(flowID, "flow-") && strings.HasPrefix(storedFlowID, "cand-") {
				doc["flowId"] = flowID
			}
			FormatTaskViewResponse(doc, s.repoRoot)
			return doc, nil
		}

		// Also check entry symbol match if flowID was computed from entry symbol
		if m, ok := doc["semanticMap"].(map[string]any); ok {
			if steps, ok := m["steps"].([]any); ok && len(steps) > 0 {
				if firstStep, ok := steps[0].(map[string]any); ok {
					if anchor, ok := firstStep["anchor"].(map[string]any); ok {
						repoRel, _ := anchor["repoRelativePath"].(string)
						enclosing, _ := anchor["enclosingSymbolPath"].(string)
						if repoRel != "" && enclosing != "" {
							if matchFlowID(fusion.ComputeFlowID(repoRel+"#"+enclosing), flowID) {
								doc["viewId"] = view.ViewID
								if strings.HasPrefix(flowID, "flow-") && strings.HasPrefix(storedFlowID, "cand-") {
									doc["flowId"] = flowID
								}
								FormatTaskViewResponse(doc, s.repoRoot)
								return doc, nil
							}
						}
					}
				}
			}
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
		altID := ""
		if strings.HasPrefix(flowID, "flow-") {
			altID = "cand-" + strings.TrimPrefix(flowID, "flow-")
		} else if strings.HasPrefix(flowID, "cand-") {
			altID = "flow-" + strings.TrimPrefix(flowID, "cand-")
		}
		if altID != "" {
			raw, err = s.storage.ReadFlowSpec(pointer.GenerationID, altID)
		}
	}
	if err != nil {
		return nil, err
	}
	var spec fusion.FlowSpec
	if err := json.Unmarshal(raw, &spec); err != nil {
		return nil, err
	}
	m := semantic.ProjectFlowSpec(&spec, pointer.GenerationID)

	entry := ""
	if len(spec.Steps) > 0 {
		entry = spec.Steps[0].Anchor.RepoRelativePath + "#" + spec.Steps[0].Anchor.EnclosingSymbolPath
	}
	h := sha256.Sum256([]byte(flowID))
	recViewID := fmt.Sprintf("flow-recovered-%s", hex.EncodeToString(h[:8]))
	res := map[string]any{
		"viewId":               recViewID,
		"flowId":               flowID,
		"semanticMap":          m,
		"flowSequence":         semantic.BuildFlowSequence(m),
		"flowContexts":         map[string]any{},
		"sourceFiles":          map[string]any{},
		"request":              map[string]any{"flowId": flowID, "entrySymbol": entry},
		"sourceNotice":         "과거 분석에 보존된 소스 문맥이 없어 재분석이 필요합니다. 현재 코드를 확인하려면 다시 분석을 실행하세요.",
		"sourceContextMissing": true,
		"needsReanalysis":      true,
	}
	resolution := spec.FlowResolution
	if resolution != nil {
		res["flowResolution"] = resolution
		res["request"] = map[string]any{
			"request":     resolution.RawRequest,
			"flowId":      resolution.FlowID,
			"entrySymbol": resolution.EntrySymbolPath,
		}
	}
	FormatTaskViewResponse(res, s.repoRoot)
	return res, nil
}
