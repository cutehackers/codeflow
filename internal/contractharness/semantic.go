package contractharness

import (
	"encoding/json"
	"fmt"
)

const (
	TaskIntentSchemaID         = BaseURL + "task-intent.schema.json"
	TaskViewQuerySchemaID      = BaseURL + "task-view-query.schema.json"
	SemanticMapIRSchemaID      = BaseURL + "semantic-map-ir.schema.json"
	FlowViewProjectionSchemaID = BaseURL + "flow-view-projection.schema.json"
)

// SemanticValidationError records a semantic rule violation.
type SemanticValidationError struct {
	Scope   string
	Message string
}

func (e *SemanticValidationError) Error() string {
	return fmt.Sprintf("semantic validation error [%s]: %s", e.Scope, e.Message)
}

// ValidateTaskIntent verifies schema and cross-field rules for TaskIntent.
func ValidateTaskIntent(data []byte) error {
	if err := Validate(TaskIntentSchemaID, data); err != nil {
		return fmt.Errorf("task-intent schema: %w", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		return err
	}
	req, _ := doc["request"].(map[string]any)
	rawReq, _ := req["rawRequest"].(string)
	if rawReq == "" {
		return &SemanticValidationError{Scope: "task-intent", Message: "rawRequest must not be empty"}
	}
	return nil
}

// ValidateTaskViewQuery verifies schema and feature mode preconditions.
func ValidateTaskViewQuery(data []byte) error {
	if err := Validate(TaskViewQuerySchemaID, data); err != nil {
		return fmt.Errorf("task-view-query schema: %w", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		return err
	}
	mode, _ := doc["mode"].(string)
	if mode == "feature" {
		feat, _ := doc["feature"].(map[string]any)
		if feat == nil {
			return &SemanticValidationError{Scope: "task-view-query", Message: "mode feature requires feature block"}
		}
		req, _ := feat["request"].(string)
		fID, _ := feat["flowId"].(string)
		entry, _ := feat["entrySymbol"].(string)
		dom, _ := feat["domain"].(string)
		if req == "" && fID == "" && entry == "" && dom == "" {
			return &SemanticValidationError{Scope: "task-view-query", Message: "feature mode requires at least one start condition (request, flowId, entrySymbol, domain)"}
		}
	}
	return nil
}

// ValidateSemanticMapIR verifies schema and critical preservation invariants.
func ValidateSemanticMapIR(data []byte) error {
	if err := Validate(SemanticMapIRSchemaID, data); err != nil {
		return fmt.Errorf("semantic-map-ir schema: %w", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		return err
	}
	basis, _ := doc["computedBasisId"].(string)
	if basis == "" {
		return &SemanticValidationError{Scope: "semantic-map-ir", Message: "computedBasisId must be non-empty"}
	}
	settlement, _ := doc["settlement"].(string)
	quality, _ := doc["quality"].(map[string]any)
	if settlement == "passed" && quality != nil {
		unresolved, _ := quality["unresolvedCriticalCount"].(float64)
		if int(unresolved) > 0 {
			return &SemanticValidationError{Scope: "semantic-map-ir", Message: "settlement cannot be passed with unresolvedCriticalCount > 0"}
		}
	}
	return nil
}

// ValidateFlowViewProjection verifies projection against D32 preservation rules.
func ValidateFlowViewProjection(data []byte) error {
	if err := Validate(FlowViewProjectionSchemaID, data); err != nil {
		return fmt.Errorf("flow-view-projection schema: %w", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		return err
	}
	vis, _ := doc["visibleStepRefs"].([]any)
	pres, _ := doc["preservedStepRefs"].([]any)

	visSet := make(map[string]bool)
	for _, v := range vis {
		if s, ok := v.(string); ok {
			visSet[s] = true
		}
	}
	// D32 & VS02-A4: preservedStepRefs MUST all be in visibleStepRefs
	for _, p := range pres {
		if s, ok := p.(string); ok {
			if !visSet[s] {
				return &SemanticValidationError{
					Scope:   "flow-view-projection",
					Message: fmt.Sprintf("preserved step %q must be in visibleStepRefs", s),
				}
			}
		}
	}
	return nil
}
