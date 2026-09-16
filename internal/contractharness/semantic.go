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
	TaskIntentV2SchemaID       = BaseURL + "rflsc.task-intent.v2.schema.json"
	FeatureQueryV2SchemaID     = BaseURL + "rflsc.feature-query.v2.schema.json"
	ReviewQueryV2SchemaID      = BaseURL + "rflsc.review-query.v2.schema.json"
	SemanticMapV2SchemaID      = BaseURL + "rflsc.semantic-map-ir.v2.schema.json"
	FlowProjectionV2SchemaID   = BaseURL + "rflsc.flowview-projection.v2.schema.json"
	StoryboardSchemaID         = BaseURL + "storyboard.schema.json"
)

// ValidateStoryboard verifies schema compliance for Storyboard.
func ValidateStoryboard(data []byte) error {
	if err := Validate(StoryboardSchemaID, data); err != nil {
		return fmt.Errorf("storyboard schema: %w", err)
	}
	return nil
}

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
	schemaID := TaskIntentSchemaID
	var header struct {
		SchemaID string `json:"schemaId"`
	}
	if err := json.Unmarshal(data, &header); err == nil && header.SchemaID == TaskIntentV2SchemaID {
		schemaID = TaskIntentV2SchemaID
	}
	if err := Validate(schemaID, data); err != nil {
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
	schemaID := TaskViewQuerySchemaID
	var header struct {
		SchemaID string `json:"schemaId"`
	}
	if err := json.Unmarshal(data, &header); err == nil {
		switch header.SchemaID {
		case FeatureQueryV2SchemaID:
			schemaID = FeatureQueryV2SchemaID
		case ReviewQueryV2SchemaID:
			schemaID = ReviewQueryV2SchemaID
		}
	}
	if err := Validate(schemaID, data); err != nil {
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
	schemaID := SemanticMapIRSchemaID
	var header struct {
		SchemaID string `json:"schemaId"`
	}
	if err := json.Unmarshal(data, &header); err == nil && header.SchemaID == SemanticMapV2SchemaID {
		schemaID = SemanticMapV2SchemaID
	}
	if err := Validate(schemaID, data); err != nil {
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
	if settlement == "passed" {
		if quality == nil {
			return &SemanticValidationError{Scope: "semantic-map-ir", Message: "passed settlement requires quality evidence"}
		}
		stage, _ := quality["stage"].(string)
		if stage != "Q3" && stage != "Q4" {
			return &SemanticValidationError{Scope: "semantic-map-ir", Message: "settlement may be passed only at Q3 or Q4"}
		}
		unresolved, unresolvedOK := quality["unresolvedCriticalCount"].(float64)
		conflicting, conflictingOK := quality["conflictingCriticalCount"].(float64)
		if !unresolvedOK || int(unresolved) != 0 || !conflictingOK || int(conflicting) != 0 {
			return &SemanticValidationError{Scope: "semantic-map-ir", Message: "settlement cannot be passed with unresolved or conflicting critical counts"}
		}
		obligations, _ := quality["criticalObligations"].([]any)
		for _, rawObligation := range obligations {
			obligation, ok := rawObligation.(map[string]any)
			if !ok {
				return &SemanticValidationError{Scope: "semantic-map-ir", Message: "critical obligation is not an object"}
			}
			required, _ := obligation["required"].(bool)
			status, _ := obligation["status"].(string)
			if required && status != "verified" {
				return &SemanticValidationError{Scope: "semantic-map-ir", Message: "settlement cannot be passed with an unverified required obligation"}
			}
		}
	}
	if schemaID == SemanticMapV2SchemaID {
		authority, _ := doc["authority"].(string)
		if authority != "candidate" && authority != "historical" {
			return &SemanticValidationError{Scope: "semantic-map-ir.v2", Message: "VS04 map authority must be candidate or historical"}
		}
		if err := validateVS04SemanticMap(doc); err != nil {
			return err
		}
	}
	return nil
}

// validateVS04SemanticMap enforces the identity graph that JSON Schema cannot
// express. A map is one manifest: every edge endpoint and evidence reference
// must resolve inside that manifest, and nested basis identities must agree.
func validateVS04SemanticMap(doc map[string]any) error {
	scope := "semantic-map-ir.v2"
	mapBasis, _ := doc["computedBasisId"].(string)
	snapshotID, _ := doc["validatedAgainstSnapshotId"].(string)
	basis, ok := doc["basis"].(map[string]any)
	if !ok {
		return &SemanticValidationError{Scope: scope, Message: "basis object is required"}
	}
	if got, _ := basis["computedBasisId"].(string); got != mapBasis {
		return &SemanticValidationError{Scope: scope, Message: "map and basis computedBasisId differ"}
	}
	if got, _ := basis["computedWorkspaceSnapshotId"].(string); got != snapshotID {
		return &SemanticValidationError{Scope: scope, Message: "map and basis snapshot identity differ"}
	}

	stepIDs := make(map[string]struct{})
	steps, _ := doc["steps"].([]any)
	for _, raw := range steps {
		step, ok := raw.(map[string]any)
		if !ok {
			return &SemanticValidationError{Scope: scope, Message: "step is not an object"}
		}
		id, _ := step["stepId"].(string)
		if id == "" {
			return &SemanticValidationError{Scope: scope, Message: "stepId must be non-empty"}
		}
		if _, exists := stepIDs[id]; exists {
			return &SemanticValidationError{Scope: scope, Message: fmt.Sprintf("duplicate stepId %q", id)}
		}
		stepIDs[id] = struct{}{}
	}

	evidenceIDs := make(map[string]struct{})
	evidence, _ := doc["evidence"].([]any)
	for _, raw := range evidence {
		ev, ok := raw.(map[string]any)
		if !ok {
			return &SemanticValidationError{Scope: scope, Message: "evidence is not an object"}
		}
		id, _ := ev["evidenceId"].(string)
		if id == "" {
			return &SemanticValidationError{Scope: scope, Message: "evidenceId must be non-empty"}
		}
		if _, exists := evidenceIDs[id]; exists {
			return &SemanticValidationError{Scope: scope, Message: fmt.Sprintf("duplicate evidenceId %q", id)}
		}
		evidenceIDs[id] = struct{}{}
		if got, _ := ev["computedBasisId"].(string); got != mapBasis {
			return &SemanticValidationError{Scope: scope, Message: fmt.Sprintf("evidence %q is not bound to map basis", id)}
		}
		if got, _ := ev["snapshotId"].(string); got != snapshotID {
			return &SemanticValidationError{Scope: scope, Message: fmt.Sprintf("evidence %q is not bound to map snapshot", id)}
		}
	}

	for _, raw := range steps {
		step := raw.(map[string]any)
		refs, _ := step["evidenceRefs"].([]any)
		for _, ref := range refs {
			refID, _ := ref.(string)
			if _, exists := evidenceIDs[refID]; !exists {
				return &SemanticValidationError{Scope: scope, Message: fmt.Sprintf("step %q references missing evidence %q", step["stepId"], refID)}
			}
		}
	}

	edges, _ := doc["edges"].([]any)
	for _, raw := range edges {
		edge, ok := raw.(map[string]any)
		if !ok {
			return &SemanticValidationError{Scope: scope, Message: "edge is not an object"}
		}
		from, _ := edge["fromStepId"].(string)
		to, _ := edge["toStepId"].(string)
		if _, exists := stepIDs[from]; !exists {
			return &SemanticValidationError{Scope: scope, Message: fmt.Sprintf("edge source %q is not a canonical step", from)}
		}
		if _, exists := stepIDs[to]; !exists {
			return &SemanticValidationError{Scope: scope, Message: fmt.Sprintf("edge target %q is not a canonical step", to)}
		}
	}
	return nil
}

// ValidateFlowViewProjection verifies projection against D32 preservation rules.
func ValidateFlowViewProjection(data []byte) error {
	schemaID := FlowViewProjectionSchemaID
	var header struct {
		SchemaID string `json:"schemaId"`
	}
	if err := json.Unmarshal(data, &header); err == nil && header.SchemaID == FlowProjectionV2SchemaID {
		schemaID = FlowProjectionV2SchemaID
	}
	if err := Validate(schemaID, data); err != nil {
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
	if schemaID == FlowProjectionV2SchemaID {
		unknown, _ := doc["unknownBoundaryRefs"].([]any)
		for _, ref := range unknown {
			if s, ok := ref.(string); !ok || s == "" {
				return &SemanticValidationError{Scope: "flow-view-projection.v2", Message: "unknown boundary refs must be non-empty strings"}
			}
		}
	}
	return nil
}
