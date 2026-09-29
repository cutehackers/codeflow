package flowview

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"

	"codeflow/internal/collector/contractharness"
	"codeflow/internal/curator/semantic"
)

func validateCodeReadingPathForSave(result map[string]any, model *semantic.SemanticMapIR, sequence *semantic.FlowSequence) error {
	provided, exists := result["codeReadingPath"]
	if !exists || provided == nil {
		return nil
	}
	raw, err := json.Marshal(provided)
	if err != nil {
		return err
	}
	if err := contractharness.ValidateCodeReadingPath(raw); err != nil {
		return err
	}
	var path semantic.CodeReadingPath
	if err := json.Unmarshal(raw, &path); err != nil {
		return err
	}
	var resolution struct {
		Status     string `json:"status"`
		RawRequest string `json:"rawRequest"`
	}
	if err := decodeTaskViewPart(result["flowResolution"], &resolution); err != nil {
		return err
	}
	if resolution.Status != "resolved" || strings.TrimSpace(resolution.RawRequest) == "" {
		return fmt.Errorf("code reading path requires a resolved request")
	}
	var focus *semantic.RequestedFlowFocus
	if result["requestedFocus"] != nil {
		focus = &semantic.RequestedFlowFocus{}
		if err := decodeTaskViewPart(result["requestedFocus"], focus); err != nil {
			return err
		}
	}
	selections := make([]semantic.CodeReadingSelection, 0, len(path.Nodes))
	for _, node := range path.Nodes {
		selection := semantic.CodeReadingSelection{
			StepID: node.StepID, Role: node.Role, RequestPart: node.RequestPart,
			InclusionReason: node.InclusionReason,
		}
		selections = append(selections, selection)
	}
	relations := make([]semantic.CodeReadingRelationSelection, 0, len(path.Relations))
	for _, relation := range path.Relations {
		relations = append(relations, semantic.CodeReadingRelationSelection{
			FromStepID: relation.FromNodeID, ToStepID: relation.ToNodeID, EdgeIndexes: relation.EdgeIndexes,
		})
	}
	verified, err := semantic.BuildCodeReadingPath(model, sequence, resolution.RawRequest, focus, selections, relations)
	if err != nil {
		return err
	}
	var contexts map[string]FlowContextProjection
	if err := decodeTaskViewPart(result["flowContexts"], &contexts); err != nil {
		return err
	}
	if err := verifyCodeReadingPathContexts(verified, model, contexts); err != nil {
		return err
	}
	if !reflect.DeepEqual(path, *verified) {
		return fmt.Errorf("code reading path differs from the verified source and relations")
	}
	return nil
}

func decodeTaskViewPart(value any, destination any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, destination)
}

// A selected decision may intentionally omit an outcome outside the request.
// Keep that selection visible, but do not present it as every confirmed route.
func codeReadingPathBranchLimitation(model *semantic.SemanticMapIR, path *semantic.CodeReadingPath) string {
	if model == nil || path == nil {
		return ""
	}
	selected := make(map[string]bool, len(path.Nodes))
	for _, node := range path.Nodes {
		selected[node.StepID] = true
	}
	used := make(map[int]bool)
	for _, relation := range path.Relations {
		for _, index := range relation.EdgeIndexes {
			used[index] = true
			if index >= 0 && index < len(model.Edges) {
				selected[model.Edges[index].FromStepID] = true
				selected[model.Edges[index].ToStepID] = true
			}
		}
	}
	count := 0
	for _, step := range model.Steps {
		if !selected[step.StepID] || (step.Kind != "guard" && step.Kind != "branch" && step.Kind != "decision") {
			continue
		}
		for index, edge := range model.Edges {
			if edge.FromStepID != step.StepID || edge.ResolutionStatus != "resolved" || used[index] {
				continue
			}
			for _, condition := range edge.Conditions {
				if condition.StepID == step.StepID {
					count++
					break
				}
			}
		}
	}
	if count == 0 {
		return ""
	}
	return fmt.Sprintf("확인된 분기 %d개가 기본 코드 읽기 경로에서 빠져 있습니다. 각 관문의 실행 타임라인과 다른 확인된 코드에서 볼 수 있습니다.", count)
}

func applyCodeReadingPathLimitation(view map[string]any, model *semantic.SemanticMapIR) {
	if view["codeReadingPath"] == nil {
		return
	}
	var path semantic.CodeReadingPath
	if err := decodeTaskViewPart(view["codeReadingPath"], &path); err != nil {
		return
	}
	if limitation := codeReadingPathBranchLimitation(model, &path); limitation != "" {
		view["codeReadingPathLimitation"] = limitation
	} else {
		delete(view, "codeReadingPathLimitation")
	}
}

func verifyCodeReadingContext(node semantic.CodeReadingNode, generationID, snapshotID string, contexts map[string]FlowContextProjection) error {
	context, exists := contexts[node.StepID]
	if !exists || context.StepID != node.StepID || context.GenerationID != generationID ||
		context.SnapshotID != snapshotID || context.Precision != PrecisionExact || context.SourceValidationStatus != "verified" ||
		context.CanonicalPath != node.SourceAnchor.RepoRelativePath || context.Statement == nil ||
		context.Statement.ByteRange != node.SourceAnchor.ByteRange || context.SourceLimitation != "" {
		return fmt.Errorf("invalid_evidence: selected code %q cannot open the exact CodeView source", node.StepID)
	}
	if context.Statement.LineRange[0] < 1 || context.Statement.LineRange[1] < context.Statement.LineRange[0] {
		return fmt.Errorf("invalid_evidence: selected code %q has no valid CodeView line range", node.StepID)
	}
	spanLength := node.SourceAnchor.ByteRange[1] - node.SourceAnchor.ByteRange[0]
	if spanLength <= 0 || context.Statement.LineRange[1]-context.Statement.LineRange[0] >= spanLength {
		return fmt.Errorf("invalid_evidence: selected code %q has an inconsistent CodeView line range", node.StepID)
	}
	hits := make(map[int]*FlowContextLine, len(context.DisplayedLines))
	for index := range context.DisplayedLines {
		line := &context.DisplayedLines[index]
		if !line.IsHit || line.Selection == nil {
			continue
		}
		if hits[line.LineNumber] != nil {
			return fmt.Errorf("invalid_evidence: selected code %q has duplicate CodeView highlights", node.StepID)
		}
		hits[line.LineNumber] = line
	}
	selected := make([]string, 0, context.Statement.LineRange[1]-context.Statement.LineRange[0]+1)
	for lineNumber := context.Statement.LineRange[0]; lineNumber <= context.Statement.LineRange[1]; lineNumber++ {
		hit := hits[lineNumber]
		if hit == nil || hit.Selection.Before+hit.Selection.Text+hit.Selection.After != hit.Text {
			return fmt.Errorf("invalid_evidence: selected code %q is not fully highlighted in CodeView", node.StepID)
		}
		selected = append(selected, hit.Selection.Text)
	}
	span := strings.Join(selected, "\n")
	if len(span)+1 == spanLength {
		span += "\n"
	}
	if len(span) != spanLength || sha256Hex([]byte(span)) != node.SourceAnchor.SpanHash {
		return fmt.Errorf("invalid_evidence: selected code %q does not match the CodeView source span", node.StepID)
	}
	return nil
}

func verifyCodeReadingPathContexts(path *semantic.CodeReadingPath, model *semantic.SemanticMapIR, contexts map[string]FlowContextProjection) error {
	steps := make(map[string]semantic.SemanticStep, len(model.Steps))
	for _, step := range model.Steps {
		steps[step.StepID] = step
	}
	checked := make(map[string]bool, len(path.Nodes))
	for index := range path.Nodes {
		if err := verifyCodeReadingContext(path.Nodes[index], model.GenerationID, path.SnapshotID, contexts); err != nil {
			return err
		}
		path.Nodes[index].CodeViewAvailable = true
		checked[path.Nodes[index].StepID] = true
	}
	for _, relation := range path.Relations {
		for _, edgeIndex := range relation.EdgeIndexes[:len(relation.EdgeIndexes)-1] {
			stepID := model.Edges[edgeIndex].ToStepID
			if checked[stepID] {
				continue
			}
			step, exists := steps[stepID]
			if !exists {
				return fmt.Errorf("invalid_evidence: intermediate code point %q is missing", stepID)
			}
			if err := verifyCodeReadingContext(semantic.CodeReadingNode{StepID: stepID, SourceAnchor: step.Anchor}, model.GenerationID, path.SnapshotID, contexts); err != nil {
				return err
			}
			checked[stepID] = true
		}
	}
	return nil
}

func (s *Server) serveCodeReadingPath(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var input struct {
		ViewID     string                                  `json:"viewId"`
		Selections []semantic.CodeReadingSelection         `json:"selections"`
		Relations  []semantic.CodeReadingRelationSelection `json:"relations"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || strings.TrimSpace(input.ViewID) == "" {
		writeTaskViewError(w, fmt.Errorf("invalid_precondition: viewId and selections are required"), http.StatusBadRequest)
		return
	}
	raw, err := s.storage.ReadView(r.Context(), input.ViewID)
	if err != nil {
		writeTaskViewError(w, err, http.StatusBadRequest)
		return
	}
	var view map[string]any
	if err := json.Unmarshal(raw, &view); err != nil {
		writeTaskViewError(w, err, http.StatusInternalServerError)
		return
	}
	var model semantic.SemanticMapIR
	var sequence semantic.FlowSequence
	if err := decodeTaskViewPart(view["semanticMap"], &model); err != nil {
		writeTaskViewError(w, err, http.StatusBadRequest)
		return
	}
	if err := decodeTaskViewPart(view["flowSequence"], &sequence); err != nil {
		writeTaskViewError(w, err, http.StatusBadRequest)
		return
	}
	var resolution struct {
		RawRequest string `json:"rawRequest"`
	}
	if err := decodeTaskViewPart(view["flowResolution"], &resolution); err != nil {
		writeTaskViewError(w, err, http.StatusBadRequest)
		return
	}
	var focus *semantic.RequestedFlowFocus
	if view["requestedFocus"] != nil {
		focus = &semantic.RequestedFlowFocus{}
		if err := decodeTaskViewPart(view["requestedFocus"], focus); err != nil {
			writeTaskViewError(w, err, http.StatusBadRequest)
			return
		}
	}
	path, err := semantic.BuildCodeReadingPath(&model, &sequence, resolution.RawRequest, focus, input.Selections, input.Relations)
	if err != nil {
		writeTaskViewError(w, err, http.StatusBadRequest)
		return
	}
	var contexts map[string]FlowContextProjection
	if err := decodeTaskViewPart(view["flowContexts"], &contexts); err != nil {
		writeTaskViewError(w, err, http.StatusBadRequest)
		return
	}
	if err := verifyCodeReadingPathContexts(path, &model, contexts); err != nil {
		writeTaskViewError(w, err, http.StatusBadRequest)
		return
	}
	view["codeReadingPath"] = path
	delete(view, "viewId")
	saved, err := s.SaveTaskView(r.Context(), view)
	if err != nil {
		writeTaskViewError(w, err, http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(saved)
}
