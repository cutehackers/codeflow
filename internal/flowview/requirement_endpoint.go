package flowview

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"codeflow/internal/semantic"
)

const maxRequirementCriteria = 50
const maxRequirementTextLength = 2000

type requirementCriterionInput struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

type requirementEvidenceRequest struct {
	GenerationID        string                      `json:"generationId"`
	RequirementRevision string                      `json:"requirementRevision"`
	Criteria            []requirementCriterionInput `json:"criteria"`
}

// handleTaskRequirement serves explicit requirement-to-code evidence lookup.
// The caller supplies requirement text with an explicit revision and the
// generation to inspect. Nothing is cached across revisions: each call binds
// its result to the supplied revision and the stored map's snapshot basis,
// so an edited requirement never inherits an older result. Matches stay
// candidate-level; promotion requires proof the endpoint never grants.
func (s *Server) handleTaskRequirement(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body requirementEvidenceRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeImpactError(w, errors.New("missing_precondition: requirement request body is required"))
		return
	}
	generationID := strings.TrimSpace(body.GenerationID)
	revision := strings.TrimSpace(body.RequirementRevision)
	if generationID == "" || revision == "" {
		writeImpactError(w, errors.New("missing_precondition: generationId and requirementRevision are required"))
		return
	}
	criteria, err := normalizeRequirementCriteria(body.Criteria)
	if err != nil {
		writeImpactError(w, err)
		return
	}

	s.mu.Lock()
	mapIR := s.mapCache[generationID]
	s.mu.Unlock()
	if mapIR == nil {
		writeImpactError(w, errors.New("missing_precondition: requested generation is not available"))
		return
	}

	alignments := semantic.ComputeRequirementAlignment(criteria, mapIR, semantic.AlignmentOptions{})

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"generationId":               mapIR.GenerationID,
		"computedBasisId":            mapIR.ComputedBasisID,
		"validatedAgainstSnapshotId": mapIR.ValidatedAgainstSnapshotID,
		"requirementRevision":        revision,
		"coverage":                   mapIR.Coverage,
		"alignments":                 alignments,
	})
}

func normalizeRequirementCriteria(inputs []requirementCriterionInput) ([]semantic.AcceptanceCriterion, error) {
	if len(inputs) == 0 {
		return nil, errors.New("missing_precondition: at least one requirement criterion is required")
	}
	if len(inputs) > maxRequirementCriteria {
		return nil, errors.New("invalid_precondition: too many requirement criteria")
	}
	criteria := make([]semantic.AcceptanceCriterion, 0, len(inputs))
	for _, input := range inputs {
		id := strings.TrimSpace(input.ID)
		text := strings.TrimSpace(input.Text)
		if id == "" || text == "" {
			return nil, errors.New("missing_precondition: requirement criterion id and text are required")
		}
		if len([]rune(text)) > maxRequirementTextLength {
			return nil, errors.New("invalid_precondition: requirement criterion text is too long")
		}
		criteria = append(criteria, semantic.AcceptanceCriterion{ID: id, Text: text})
	}
	return criteria, nil
}
