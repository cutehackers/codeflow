package contractharness

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateSemanticDeltaIR(t *testing.T) {
	schemas := SchemasDir()
	if schemas == "" {
		t.Fatal("schemas dir not found")
	}

	validPath := filepath.Join(schemas, "fixtures", "semantic-delta-ir", "valid", "payment-retry-delta.json")
	validData, err := os.ReadFile(validPath)
	if err != nil {
		t.Fatalf("read valid fixture: %v", err)
	}

	if err := ValidateSemanticDeltaIR(validData); err != nil {
		t.Errorf("expected valid fixture to pass, got: %v", err)
	}

	// Test invariant: evidence_updated with empty evidenceRefs
	badEvidenceUpdated := []byte(`{
		"schemaId": "codeflow.semantic-delta-ir",
		"schemaVersion": 1,
		"comparisonId": "comp-1",
		"taskIntentRevision": 1,
		"baselineComputedBasisId": "b1",
		"currentComputedBasisId": "b2",
		"fromGeneration": "g1",
		"toGeneration": "g2",
		"changes": [
			{
				"deltaId": "d1",
				"kind": "evidence_updated",
				"targetStepId": "s1",
				"summary": "evidence updated",
				"epistemicStatus": "observed",
				"validationStatus": "verified",
				"evidenceRefs": []
			}
		]
	}`)
	if err := ValidateSemanticDeltaIR(badEvidenceUpdated); err == nil {
		t.Error("expected error for evidence_updated with empty evidenceRefs, got nil")
	}
}

func TestValidateRequirementAlignment(t *testing.T) {
	schemas := SchemasDir()
	if schemas == "" {
		t.Fatal("schemas dir not found")
	}

	validPath := filepath.Join(schemas, "fixtures", "requirement-alignment", "valid", "confirmed-alignment.json")
	validData, err := os.ReadFile(validPath)
	if err != nil {
		t.Fatalf("read valid fixture: %v", err)
	}

	if err := ValidateRequirementAlignment(validData); err != nil {
		t.Errorf("expected valid fixture to pass, got: %v", err)
	}

	// Test invariant: confirmed without evidenceRefs must fail
	noEvidenceConfirmed := []byte(`{
		"schemaId": "codeflow.requirement-alignment",
		"schemaVersion": 1,
		"criterionId": "AC-1",
		"status": "confirmed",
		"coveredStepRefs": ["s1"],
		"evidenceRefs": [],
		"missingEvidence": [],
		"computedBasisId": "b1"
	}`)
	if err := ValidateRequirementAlignment(noEvidenceConfirmed); err == nil {
		t.Error("expected error for confirmed status without evidenceRefs, got nil")
	}

	// Test invariant: confirmed with missingEvidence must fail
	missingEvidenceConfirmed := []byte(`{
		"schemaId": "codeflow.requirement-alignment",
		"schemaVersion": 1,
		"criterionId": "AC-1",
		"status": "confirmed",
		"coveredStepRefs": ["s1"],
		"evidenceRefs": ["ev1"],
		"missingEvidence": ["boundary test missing"],
		"computedBasisId": "b1"
	}`)
	if err := ValidateRequirementAlignment(missingEvidenceConfirmed); err == nil {
		t.Error("expected error for confirmed status with missingEvidence, got nil")
	}
}
