package contractharness

import (
	"encoding/json"
	"testing"
)

func TestGenerationProofValidators(t *testing.T) {
	validManifest := map[string]any{
		"schemaId":                       "https://codeflow.local/schemas/generation-proof-manifest.schema.json",
		"schemaVersion":                  1,
		"proofId":                        "proof-1",
		"generationId":                   "gen-1",
		"computedBasisId":                "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		"validatedAgainstSnapshotId":     "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		"taskIntentRevision":             1,
		"normalizedQueryHash":            "a1b2c3d4e5f67890a1b2c3d4e5f67890a1b2c3d4e5f67890a1b2c3d4e5f67890",
		"analysisReadSetId":              "rs-1",
		"causalObservationClosureId":     "close-1",
		"causalObservationClosureDigest": "f0e1d2c3b4a59687f0e1d2c3b4a59687f0e1d2c3b4a59687f0e1d2c3b4a59687",
		"currentPublication": map[string]any{
			"eligibility":           "passed",
			"snapshotGate":          "passed",
			"closureGate":           "passed",
			"evidenceGate":          "passed",
			"semanticAtomicityGate": "passed",
			"taskRelevanceGate":     "passed",
			"comprehensionGate":     "passed",
		},
		"settlementEvaluation": map[string]any{
			"gate":                   "pending",
			"evaluatedAt":            nil,
			"blockingObligationRefs": []string{"ob-1"},
		},
		"artifactRefs": map[string]any{
			"semanticMap": "cas:sha256:1111111111111111111111111111111111111111111111111111111111111111",
		},
		"expectedLiveHeadSnapshotId": "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		"publishedAt":                "2026-09-03T12:00:00Z",
	}

	data, _ := json.Marshal(validManifest)
	if err := ValidateGenerationProofManifest(data); err != nil {
		t.Fatalf("expected valid manifest to pass: %v", err)
	}

	// Invariant: eligibility passed but subgate failed
	invalidGate := validManifest
	invalidGate["currentPublication"] = map[string]any{
		"eligibility":           "passed",
		"snapshotGate":          "passed",
		"closureGate":           "failed", // mismatch!
		"evidenceGate":          "passed",
		"semanticAtomicityGate": "passed",
		"taskRelevanceGate":     "passed",
		"comprehensionGate":     "passed",
	}
	data, _ = json.Marshal(invalidGate)
	if err := ValidateGenerationProofManifest(data); err == nil {
		t.Fatalf("expected error when eligibility is passed but subgate failed")
	}

	// Invariant: settlement passed but blockingObligationRefs is non-empty
	invalidSettlement := validManifest
	invalidSettlement["currentPublication"] = map[string]any{
		"eligibility":           "passed",
		"snapshotGate":          "passed",
		"closureGate":           "passed",
		"evidenceGate":          "passed",
		"semanticAtomicityGate": "passed",
		"taskRelevanceGate":     "passed",
		"comprehensionGate":     "passed",
	}
	invalidSettlement["settlementEvaluation"] = map[string]any{
		"gate":                   "passed",
		"evaluatedAt":            "2026-09-03T12:00:00Z",
		"blockingObligationRefs": []string{"ob-unresolved"}, // should be empty!
	}
	data, _ = json.Marshal(invalidSettlement)
	if err := ValidateGenerationProofManifest(data); err == nil {
		t.Fatalf("expected error when settlement is passed with blocking obligations")
	}

	// ActivePointer test
	validPtr := map[string]any{
		"schemaId":                   "https://codeflow.local/schemas/active-pointer.schema.json",
		"schemaVersion":              1,
		"generationId":               "gen-1",
		"manifestObjectRef":          "cas:1",
		"publishedAt":                "2026-09-03T12:00:00Z",
		"computedBasisId":            "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		"validatedAgainstSnapshotId": "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		"expectedLiveHeadSnapshotId": "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		"workspaceEpoch":             "epoch-1",
		"taskIntentRevision":         1,
		"normalizedQueryHash":        "a1b2c3d4e5f67890a1b2c3d4e5f67890a1b2c3d4e5f67890a1b2c3d4e5f67890",
		"flowCount":                  1,
	}
	pData, _ := json.Marshal(validPtr)
	if err := ValidateActivePointer(pData); err != nil {
		t.Fatalf("expected valid pointer to pass: %v", err)
	}

	// EventEnvelope test
	validEnv := map[string]any{
		"schemaId":      "https://codeflow.local/schemas/event-envelope.schema.json",
		"schemaVersion": 1,
		"streamId":      "stream-1",
		"sequence":      1,
		"eventId":       "ev-1",
		"eventType":     "generation.published",
		"occurredAt":    "2026-09-03T12:00:00Z",
	}
	eData, _ := json.Marshal(validEnv)
	if err := ValidateEventEnvelope(eData); err != nil {
		t.Fatalf("expected valid event envelope to pass: %v", err)
	}
}
