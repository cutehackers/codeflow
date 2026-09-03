package contractharness

import (
	"encoding/json"
	"fmt"
)

// ValidateGenerationProofManifest validates raw JSON against generation-proof-manifest.schema.json
// and enforces semantic gate invariants (SID-C2 & Raw §10.11).
func ValidateGenerationProofManifest(data []byte) error {
	schemaID := BaseURL + "generation-proof-manifest.schema.json"
	if err := Validate(schemaID, data); err != nil {
		return fmt.Errorf("generation-proof-manifest schema violation: %w", err)
	}

	var manifest struct {
		CurrentPublication struct {
			Eligibility          string `json:"eligibility"`
			SnapshotGate         string `json:"snapshotGate"`
			ClosureGate          string `json:"closureGate"`
			EvidenceGate         string `json:"evidenceGate"`
			SemanticAtomicityGate string `json:"semanticAtomicityGate"`
			TaskRelevanceGate    string `json:"taskRelevanceGate"`
			ComprehensionGate    string `json:"comprehensionGate"`
		} `json:"currentPublication"`
		SettlementEvaluation struct {
			Gate                   string   `json:"gate"`
			EvaluatedAt            *string  `json:"evaluatedAt"`
			BlockingObligationRefs []string `json:"blockingObligationRefs"`
		} `json:"settlementEvaluation"`
	}

	if err := json.Unmarshal(data, &manifest); err != nil {
		return fmt.Errorf("parse generation-proof-manifest JSON: %w", err)
	}

	// Semantic invariant 1: currentPublication.eligibility == passed requires all subgates to be passed
	if manifest.CurrentPublication.Eligibility == "passed" {
		subgates := map[string]string{
			"snapshotGate":          manifest.CurrentPublication.SnapshotGate,
			"closureGate":           manifest.CurrentPublication.ClosureGate,
			"evidenceGate":          manifest.CurrentPublication.EvidenceGate,
			"semanticAtomicityGate": manifest.CurrentPublication.SemanticAtomicityGate,
			"taskRelevanceGate":     manifest.CurrentPublication.TaskRelevanceGate,
			"comprehensionGate":     manifest.CurrentPublication.ComprehensionGate,
		}
		for name, val := range subgates {
			if val != "passed" {
				return fmt.Errorf("currentPublication eligibility is 'passed' but subgate %s is %q", name, val)
			}
		}
	}

	// Semantic invariant 2: settlement == passed requires empty blockingObligationRefs
	if manifest.SettlementEvaluation.Gate == "passed" {
		if len(manifest.SettlementEvaluation.BlockingObligationRefs) > 0 {
			return fmt.Errorf("settlementEvaluation.gate is 'passed' but blockingObligationRefs is non-empty (%d items)",
				len(manifest.SettlementEvaluation.BlockingObligationRefs))
		}
	}

	// Semantic invariant 3: settlement == failed requires non-empty blockingObligationRefs and non-nil evaluatedAt
	if manifest.SettlementEvaluation.Gate == "failed" {
		if manifest.SettlementEvaluation.EvaluatedAt == nil || *manifest.SettlementEvaluation.EvaluatedAt == "" {
			return fmt.Errorf("settlementEvaluation.gate is 'failed' but evaluatedAt is null/empty")
		}
		if len(manifest.SettlementEvaluation.BlockingObligationRefs) == 0 {
			return fmt.Errorf("settlementEvaluation.gate is 'failed' but blockingObligationRefs is empty")
		}
	}

	return nil
}

// ValidateActivePointer validates raw JSON against active-pointer.schema.json.
func ValidateActivePointer(data []byte) error {
	schemaID := BaseURL + "active-pointer.schema.json"
	if err := Validate(schemaID, data); err != nil {
		return fmt.Errorf("active-pointer schema violation: %w", err)
	}

	var ptr struct {
		GenerationID      string `json:"generationId"`
		ManifestObjectRef string `json:"manifestObjectRef"`
		FlowCount         int    `json:"flowCount"`
	}
	if err := json.Unmarshal(data, &ptr); err != nil {
		return fmt.Errorf("parse active-pointer JSON: %w", err)
	}
	if ptr.GenerationID == "" {
		return fmt.Errorf("active-pointer: generationId must not be empty")
	}
	if ptr.ManifestObjectRef == "" {
		return fmt.Errorf("active-pointer: manifestObjectRef must not be empty")
	}
	if ptr.FlowCount < 0 {
		return fmt.Errorf("active-pointer: flowCount must be >= 0, got %d", ptr.FlowCount)
	}
	return nil
}

// ValidateEventEnvelope validates raw JSON against event-envelope.schema.json.
func ValidateEventEnvelope(data []byte) error {
	schemaID := BaseURL + "event-envelope.schema.json"
	if err := Validate(schemaID, data); err != nil {
		return fmt.Errorf("event-envelope schema violation: %w", err)
	}

	var env struct {
		Sequence  int    `json:"sequence"`
		EventID   string `json:"eventId"`
		EventType string `json:"eventType"`
	}
	if err := json.Unmarshal(data, &env); err != nil {
		return fmt.Errorf("parse event-envelope JSON: %w", err)
	}
	if env.Sequence < 1 {
		return fmt.Errorf("event-envelope: sequence must be >= 1, got %d", env.Sequence)
	}
	if env.EventID == "" {
		return fmt.Errorf("event-envelope: eventId must not be empty")
	}
	return nil
}
