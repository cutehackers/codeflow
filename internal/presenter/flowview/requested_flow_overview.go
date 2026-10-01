package flowview

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"reflect"
	"strings"
	"unicode/utf16"

	"codeflow/internal/collector/contractharness"
	"codeflow/internal/curator/semantic"
)

// Digests preserve the exact safe display document produced from the original
// snapshot. They are created during analysis, never from an overview proposal.
func sourceFileDigest(lines []FlowContextLine) string {
	texts := make([]string, len(lines))
	for index, line := range lines {
		texts[index] = line.Text
	}
	return sha256Hex([]byte(strings.Join(texts, "\n")))
}

func buildSourceFileDigests(files map[string][]FlowContextLine) map[string]string {
	digests := make(map[string]string, len(files))
	for path, lines := range files {
		digests[path] = sourceFileDigest(lines)
	}
	return digests
}

// buildRequestedFlowOverview uses only retained analysis and snapshot projections.
// Neither proposals nor current working-tree files supply code text or ranges.
func buildRequestedFlowOverview(view map[string]any, model *semantic.SemanticMapIR, sequence *semantic.FlowSequence, segments []semantic.RequestedFlowSegmentSelection, relations []semantic.RequestedFlowRelationSelection, categories ...[]semantic.RequestedFlowCategory) (*semantic.RequestedFlowOverview, error) {
	var resolution struct {
		Status     string `json:"status"`
		RawRequest string `json:"rawRequest"`
	}
	if err := decodeTaskViewPart(view["flowResolution"], &resolution); err != nil {
		return nil, err
	}
	if resolution.Status != "resolved" {
		return nil, fmt.Errorf("invalid_precondition: overview requires a resolved request")
	}
	overview, err := semantic.BuildRequestedFlowOverview(model, sequence, resolution.RawRequest, segments, relations, categories...)
	if err != nil {
		return nil, err
	}
	var contexts map[string]FlowContextProjection
	var sources map[string][]FlowContextLine
	var digests map[string]string
	if err := decodeTaskViewPart(view["flowContexts"], &contexts); err != nil {
		return nil, err
	}
	if err := decodeTaskViewPart(view["sourceFiles"], &sources); err != nil {
		return nil, err
	}
	if err := decodeTaskViewPart(view["sourceFileDigests"], &digests); err != nil {
		return nil, err
	}
	steps := make(map[string]semantic.SemanticStep, len(model.Steps))
	for _, step := range model.Steps {
		steps[step.StepID] = step
	}
	for segmentIndex := range overview.Segments {
		segment := &overview.Segments[segmentIndex]
		for blockIndex := range segment.Blocks {
			block := &segment.Blocks[blockIndex]
			first := steps[block.StepIDs[0]].Anchor
			path := first.RepoRelativePath
			if filepath.IsAbs(path) || filepath.ToSlash(filepath.Clean(path)) != path || strings.Contains(path, "\\") || path == ".." || strings.HasPrefix(path, "../") {
				return nil, fmt.Errorf("invalid_evidence: block source must be repository relative")
			}
			source := sources[path]
			if len(source) == 0 {
				return nil, fmt.Errorf("invalid_evidence: block has no retained snapshot source")
			}
			// Require one contiguous document, never concatenate disjoint projections.
			for index, line := range source {
				if line.LineNumber != index+1 {
					return nil, fmt.Errorf("invalid_evidence: snapshot source is not continuous")
				}
			}
			redacted := false
			texts := make([]string, len(source))
			for index, line := range source {
				texts[index] = line.Text
			}
			for _, projection := range contexts {
				if projection.CanonicalPath == path && projection.SourceRedacted {
					redacted = true
				}
			}
			if redacted && digests[path] != sourceFileDigest(source) {
				return nil, fmt.Errorf("invalid_evidence: redacted snapshot file does not match preserved source digest")
			}
			if !redacted && sha256Hex([]byte(strings.Join(texts, "\n"))) != first.FileHash {
				return nil, fmt.Errorf("invalid_evidence: snapshot file does not match source hash")
			}
			block.RepoRelativePath, block.SymbolPath = path, first.EnclosingSymbolPath
			block.LineRange = [2]int{len(source), 1}
			for _, id := range block.StepIDs {
				step := steps[id]
				projection := contexts[id]
				if err := verifyCodeReadingContext(semantic.CodeReadingNode{StepID: id, SourceAnchor: step.Anchor}, model.GenerationID, overview.SnapshotID, contexts); err != nil {
					return nil, err
				}
				highlight := semantic.RequestedFlowHighlight{StepID: id, LineRange: projection.Statement.LineRange, ByteRange: projection.Statement.ByteRange, LineSelections: []semantic.RequestedFlowLineSelection{}}
				for _, line := range projection.DisplayedLines {
					if line.LineNumber < 1 || line.LineNumber > len(source) || source[line.LineNumber-1].Text != line.Text {
						return nil, fmt.Errorf("invalid_evidence: block differs from verified snapshot context")
					}
					if line.IsHit && line.Selection != nil {
						start := len(utf16.Encode([]rune(line.Selection.Before)))
						end := start + len(utf16.Encode([]rune(line.Selection.Text)))
						highlight.LineSelections = append(highlight.LineSelections, semantic.RequestedFlowLineSelection{LineNumber: line.LineNumber, StartColumn: start, EndColumn: end})
					}
				}
				block.Highlights = append(block.Highlights, highlight)
				if highlight.LineRange[0] < block.LineRange[0] {
					block.LineRange[0] = highlight.LineRange[0]
				}
				if highlight.LineRange[1] > block.LineRange[1] {
					block.LineRange[1] = highlight.LineRange[1]
				}
			}
			// Context remains visibly separate from included statement highlights.
			block.LineRange[0] = max(1, block.LineRange[0]-2)
			block.LineRange[1] = min(len(source), block.LineRange[1]+2)
		}
	}
	// Folded intermediate steps must remain openable with exact source evidence.
	for _, relation := range overview.Relations {
		if relation.Status != "verified" {
			continue
		}
		for _, edge := range relation.Edges {
			for _, id := range []string{edge.FromStepID, edge.ToStepID} {
				if err := verifyCodeReadingContext(semantic.CodeReadingNode{StepID: id, SourceAnchor: steps[id].Anchor}, model.GenerationID, overview.SnapshotID, contexts); err != nil {
					return nil, err
				}
			}
		}
	}
	return overview, nil
}

func validateRequestedFlowOverviewForSave(view map[string]any, model *semantic.SemanticMapIR, sequence *semantic.FlowSequence) error {
	if view["requestedFlowOverview"] == nil {
		return nil
	}
	raw, err := json.Marshal(view["requestedFlowOverview"])
	if err != nil {
		return err
	}
	if err := contractharness.ValidateRequestedFlowOverview(raw); err != nil {
		return err
	}
	var provided semantic.RequestedFlowOverview
	if err := json.Unmarshal(raw, &provided); err != nil {
		return err
	}
	segments := make([]semantic.RequestedFlowSegmentSelection, 0, len(provided.Segments))
	for _, segment := range provided.Segments {
		selection := semantic.RequestedFlowSegmentSelection{SegmentID: segment.SegmentID, Purpose: segment.Purpose, InclusionReason: segment.InclusionReason, StepIDs: segment.StepIDs, Blocks: []semantic.RequestedFlowBlockSelection{}}
		for _, block := range segment.Blocks {
			selection.Blocks = append(selection.Blocks, semantic.RequestedFlowBlockSelection{StepIDs: block.StepIDs})
		}
		segments = append(segments, selection)
	}
	// Explicit proposals precede graph facts. Single-edge graph facts are derived
	// again and excluded from proposal reconstruction by their dedicated marker.
	relations := []semantic.RequestedFlowRelationSelection{}
	for _, relation := range provided.Relations {
		if relation.Derived {
			continue
		}
		relations = append(relations, semantic.RequestedFlowRelationSelection{FromSegmentID: relation.FromSegmentID, ToSegmentID: relation.ToSegmentID, FromStepID: relation.FromStepID, ToStepID: relation.ToStepID, EdgeIndexes: relation.EdgeIndexes})
	}
	verified, err := buildRequestedFlowOverview(view, model, sequence, segments, relations, provided.Categories)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(provided, *verified) {
		return fmt.Errorf("invalid_evidence: overview differs from verified source and graph")
	}
	return nil
}

func (s *Server) serveRequestedFlowOverview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var input struct {
		ViewID     string                                    `json:"viewId"`
		Categories json.RawMessage                           `json:"categories"`
		Segments   []semantic.RequestedFlowSegmentSelection  `json:"segments"`
		Relations  []semantic.RequestedFlowRelationSelection `json:"relations"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || strings.TrimSpace(input.ViewID) == "" {
		writeTaskViewError(w, fmt.Errorf("invalid_precondition: viewId and segments are required"), http.StatusBadRequest)
		return
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		writeTaskViewError(w, fmt.Errorf("invalid_precondition: one proposal is required"), http.StatusBadRequest)
		return
	}
	raw, err := s.storage.ReadView(r.Context(), input.ViewID)
	if err != nil {
		writeTaskViewError(w, err, http.StatusBadRequest)
		return
	}
	var view map[string]any
	if err := json.Unmarshal(raw, &view); err != nil {
		writeTaskViewError(w, err, http.StatusBadRequest)
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
	var categories []semantic.RequestedFlowCategory
	if input.Categories != nil {
		if string(input.Categories) == "null" {
			writeTaskViewError(w, fmt.Errorf("invalid_precondition: categories must be a nonempty array"), http.StatusBadRequest)
			return
		}
		categoryDecoder := json.NewDecoder(bytes.NewReader(input.Categories))
		categoryDecoder.DisallowUnknownFields()
		if err := categoryDecoder.Decode(&categories); err != nil {
			writeTaskViewError(w, err, http.StatusBadRequest)
			return
		}
	}
	overview, err := buildRequestedFlowOverview(view, &model, &sequence, input.Segments, input.Relations, categories)
	if err != nil {
		writeTaskViewError(w, err, http.StatusBadRequest)
		return
	}
	view["requestedFlowOverview"] = overview
	delete(view, "viewId")
	result, err := s.SaveTaskView(r.Context(), view)
	if err != nil {
		writeTaskViewError(w, err, http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}
