package contractharness

import (
	"encoding/json"
	"fmt"
)

// ValidateFailurePathTrace validates raw JSON against failure-path-trace.schema.json
// and enforces failure path invariants (SID-C2, SID-07, VS-07, Raw §8.7, §8.8).
func ValidateFailurePathTrace(data []byte) error {
	schemaID := BaseURL + "failure-path-trace.schema.json"
	if err := Validate(schemaID, data); err != nil {
		return fmt.Errorf("failure-path-trace schema violation: %w", err)
	}

	var trace struct {
		TraceID      string `json:"traceId"`
		Mode         string `json:"mode"`
		HasConflicts bool   `json:"hasConflicts"`
		Nodes        []struct {
			NodeId       string   `json:"nodeId"`
			Status       string   `json:"status"`
			EvidenceRefs []string `json:"evidenceRefs"`
		} `json:"nodes"`
		Timeline []struct {
			Timestamp string `json:"timestamp"`
		} `json:"timeline"`
	}

	if err := json.Unmarshal(data, &trace); err != nil {
		return fmt.Errorf("parse failure-path-trace JSON: %w", err)
	}

	// Invariant (VS07-A4, A8): conflicting node requires hasConflicts=true
	foundConflict := false
	for _, n := range trace.Nodes {
		if n.Status == "conflicting" {
			foundConflict = true
		}
		if (n.Status == "runtime_observed" || n.Status == "corroborated") && len(n.EvidenceRefs) == 0 {
			return fmt.Errorf("node %q has status %q but 0 evidenceRefs", n.NodeId, n.Status)
		}
	}
	if foundConflict && !trace.HasConflicts {
		return fmt.Errorf("trace %q contains conflicting nodes but hasConflicts is false", trace.TraceID)
	}

	return nil
}

// ValidateRuntimeObservation validates raw JSON against runtime-observation.schema.json
// and enforces isolation invariants (SID-07, INV-15, INV-18, VS07-A7).
func ValidateRuntimeObservation(data []byte) error {
	schemaID := BaseURL + "runtime-observation.schema.json"
	if err := Validate(schemaID, data); err != nil {
		return fmt.Errorf("runtime-observation schema violation: %w", err)
	}

	var obs struct {
		ObservationID        string `json:"observationId"`
		IsolationLevel       string `json:"isolationLevel"`
		TrustedLocalApproval *struct {
			Approved bool `json:"approved"`
		} `json:"trustedLocalApproval"`
	}

	if err := json.Unmarshal(data, &obs); err != nil {
		return fmt.Errorf("parse runtime-observation JSON: %w", err)
	}

	// Invariant (VS07-A7): trusted_local requires explicit approval record
	if obs.IsolationLevel == "trusted_local" {
		if obs.TrustedLocalApproval == nil {
			return fmt.Errorf("observation %q has trusted_local isolation but missing trustedLocalApproval", obs.ObservationID)
		}
	}

	return nil
}
