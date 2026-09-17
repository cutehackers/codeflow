package flowview

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"

	"codeflow/internal/curator/semantic"
)

// analysisDescriptor exposes how one side of a comparison was selected and
// which snapshot it reads. Both sides of a comparison are explicit preserved
// generations; the baseline never advances because a new analysis succeeded.
func analysisDescriptor(mapIR *semantic.SemanticMapIR, selection string) map[string]any {
	requested := ""
	if mapIR.Summary.Requested != "" {
		requested = mapIR.Summary.Requested
	}
	return map[string]any{
		"generationId":               mapIR.GenerationID,
		"computedBasisId":            mapIR.ComputedBasisID,
		"validatedAgainstSnapshotId": mapIR.ValidatedAgainstSnapshotID,
		"requested":                  requested,
		"selection":                  selection,
		"partial":                    len(mapIR.Unknowns) > 0,
		"stepCount":                  len(mapIR.Steps),
	}
}

// pulseNavigationTarget resolves which side of a comparison a change should
// be explored from. Removals navigate the baseline, additions the current
// generation, and modifications either side's endpoint separately. Edges from
// different snapshots are never merged: the caller receives one side only.
func pulseNavigationTarget(ch semantic.DeltaChange, baseMap, currMap *semantic.SemanticMapIR) (string, string, *semantic.SemanticMapIR) {
	stepSymbol := func(mapIR *semantic.SemanticMapIR, stepID string) string {
		if mapIR == nil || stepID == "" {
			return ""
		}
		for _, step := range mapIR.Steps {
			if step.StepID == stepID && strings.TrimSpace(step.TechnicalName) != "" {
				return step.TechnicalName
			}
		}
		return ""
	}
	if ch.Kind == "removed_behavior" {
		for _, id := range []string{ch.FromStepID, ch.TargetStepID} {
			if symbol := stepSymbol(baseMap, id); symbol != "" {
				return symbol, "baseline", baseMap
			}
		}
		return "", "baseline", nil
	}
	for _, id := range []string{ch.ToStepID, ch.TargetStepID} {
		if symbol := stepSymbol(currMap, id); symbol != "" {
			return symbol, "current", currMap
		}
	}
	for _, id := range []string{ch.FromStepID, ch.TargetStepID} {
		if symbol := stepSymbol(baseMap, id); symbol != "" {
			return symbol, "baseline", baseMap
		}
	}
	return "", "unknown", nil
}

// serveTaskAnalyses lists actually preserved analyses. An empty list means
// no baseline exists; the caller must not synthesize one.
func (s *Server) serveTaskAnalyses(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.mu.Lock()
	seen := make(map[string]*semantic.SemanticMapIR, len(s.mapCache))
	for _, mapIR := range s.mapCache {
		if mapIR == nil || mapIR.GenerationID == "" {
			continue
		}
		seen[mapIR.GenerationID] = mapIR
	}
	s.mu.Unlock()
	analyses := make([]map[string]any, 0, len(seen))
	for _, mapIR := range seen {
		analyses = append(analyses, analysisDescriptor(mapIR, "preserved"))
	}
	sort.Slice(analyses, func(i, j int) bool {
		a, _ := analyses[i]["generationId"].(string)
		b, _ := analyses[j]["generationId"].(string)
		return a < b
	})
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"analyses": analyses})
}
