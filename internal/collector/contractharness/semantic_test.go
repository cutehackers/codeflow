package contractharness

import (
	"strings"
	"testing"
)

func TestSemanticValidators(t *testing.T) {
	// 1. TaskIntent
	goodIntent := []byte(`{
		"schemaId": "https://codeflow.local/schemas/task-intent.schema.json",
		"schemaVersion": 1,
		"taskId": "task-01",
		"revision": 1,
		"request": {"rawRequest": "hello flow"},
		"normalizedIntent": {"expectedOutcome": "say hello"},
		"acceptanceCriteria": [{"id": "AC-1", "text": "greets"}],
		"intentStatus": "parsed",
		"mode": "feature"
	}`)
	if err := ValidateTaskIntent(goodIntent); err != nil {
		t.Fatalf("good intent failed: %v", err)
	}

	emptyRaw := []byte(`{
		"schemaId": "https://codeflow.local/schemas/task-intent.schema.json",
		"schemaVersion": 1,
		"taskId": "task-01",
		"revision": 1,
		"request": {"rawRequest": ""},
		"normalizedIntent": {"expectedOutcome": "say hello"},
		"acceptanceCriteria": [{"id": "AC-1", "text": "greets"}],
		"intentStatus": "parsed",
		"mode": "feature"
	}`)
	if err := ValidateTaskIntent(emptyRaw); err == nil {
		t.Fatal("expected empty rawRequest to fail validation")
	}

	// 2. TaskViewQuery
	goodQuery := []byte(`{
		"schemaId": "https://codeflow.local/schemas/task-view-query.schema.json",
		"schemaVersion": 1,
		"mode": "feature",
		"feature": {"request": "signup flow"}
	}`)
	if err := ValidateTaskViewQuery(goodQuery); err != nil {
		t.Fatalf("good query failed: %v", err)
	}

	// 3. FlowViewProjection preservation
	badProj := []byte(`{
		"schemaId": "https://codeflow.local/schemas/flow-view-projection.schema.json",
		"schemaVersion": 1,
		"projectionId": "proj-1",
		"generationId": "gen-1",
		"computedBasisId": "basis-1",
		"mode": "feature",
		"displayBudget": {"targetMin": 7, "targetMax": 15, "enforcement": "soft"},
		"visibleStepRefs": ["step-1"],
		"preservedStepRefs": ["step-1", "step-critical-missing"],
		"foldedSubflows": []
	}`)
	if err := ValidateFlowViewProjection(badProj); err == nil || !strings.Contains(err.Error(), "must be in visibleStepRefs") {
		t.Fatalf("expected preserved step failure, got: %v", err)
	}
}
