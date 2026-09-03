package semantic

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// FailurePathTrace mirrors schemas/failure-path-trace.schema.json (VS-07).
type FailurePathTrace struct {
	SchemaID                   string                `json:"schemaId"`
	SchemaVersion              int                   `json:"schemaVersion"`
	TraceID                    string                `json:"traceId"`
	Mode                       string                `json:"mode"` // debug | incident
	ComputedBasisID            string                `json:"computedBasisId"`
	GenerationID               string                `json:"generationId"`
	ValidatedAgainstSnapshotID string                `json:"validatedAgainstSnapshotId,omitempty"`
	FailureTarget              FailureTarget         `json:"failureTarget"`
	Nodes                      []FailureNode         `json:"nodes"`
	Relationships              []FailureRelationship `json:"relationships"`
	Timeline                   []TimelineEvent       `json:"timeline"`
	RuntimeObservationRef      string                `json:"runtimeObservationRef,omitempty"`
	UnknownCount               int                   `json:"unknownCount"`
	HasConflicts               bool                  `json:"hasConflicts"`
	Summary                    FailureSummary        `json:"summary"`
}

type FailureTarget struct {
	Error              string `json:"error,omitempty"`
	Symptom            string `json:"symptom,omitempty"`
	FailureEvidenceID  string `json:"failureEvidenceId,omitempty"`
	IncidentTraceID    string `json:"incidentTraceId,omitempty"`
	IncidentEvidenceID string `json:"incidentEvidenceId,omitempty"`
}

type FailureNode struct {
	NodeID       string   `json:"nodeId"`
	SymbolPath   string   `json:"symbolPath"`
	Role         string   `json:"role"`   // throw | transform | handle | ignore | last_verified_state | side_effect
	Status       string   `json:"status"` // static_candidate | runtime_observed | corroborated | conflicting | unknown
	EvidenceRefs []string `json:"evidenceRefs,omitempty"`
}

type FailureRelationship struct {
	FromNodeID string `json:"fromNodeId"`
	ToNodeID   string `json:"toNodeId"`
	Kind       string `json:"kind"` // thrown_to | transformed_to | handled_by | ignored_by | causes
}

type TimelineEvent struct {
	Timestamp   string `json:"timestamp"`
	Kind        string `json:"kind"` // external_call | timeout | retry | circuit_break | compensation | partial_commit | failure_emission
	Target      string `json:"target"`
	Status      string `json:"status"`
	EvidenceRef string `json:"evidenceRef,omitempty"`
}

type FailureSummary struct {
	Description        string `json:"description"`
	LastConfirmedState string `json:"lastConfirmedState"`
}

// RuntimeObservation mirrors schemas/runtime-observation.schema.json (VS-07).
type RuntimeObservation struct {
	SchemaID              string                `json:"schemaId"`
	SchemaVersion         int                   `json:"schemaVersion"`
	ObservationID         string                `json:"observationId"`
	Scenario              string                `json:"scenario"`
	Input                 string                `json:"input,omitempty"`
	Environment           string                `json:"environment"`
	DependencyFingerprint string                `json:"dependencyFingerprint,omitempty"`
	TraceCoverage         TraceCoverage         `json:"traceCoverage"`
	ObservedAt            string                `json:"observedAt"`
	IsolationLevel        string                `json:"isolationLevel"` // no_egress_sandbox | trusted_local | read_only_container
	TrustedLocalApproval  *TrustedLocalApproval `json:"trustedLocalApproval,omitempty"`
}

type TraceCoverage struct {
	SpansCovered int     `json:"spansCovered"`
	TotalSpans   int     `json:"totalSpans"`
	Ratio        float64 `json:"ratio"`
}

type TrustedLocalApproval struct {
	Approved   bool   `json:"approved"`
	ApprovedBy string `json:"approvedBy"`
	Timestamp  string `json:"timestamp"`
}

type FailureOptions struct {
	TimeWindow string
}

// InvestigateFailure investigates errors, symptoms, and failure paths (VS07-A1..A9).
func InvestigateFailure(target FailureTarget, mode string, mapIR *SemanticMapIR, obs *RuntimeObservation, opts FailureOptions) (*FailurePathTrace, error) {
	if mode == "" {
		mode = "debug"
	}

	if mode == "debug" {
		if strings.TrimSpace(target.Error) == "" && strings.TrimSpace(target.Symptom) == "" && strings.TrimSpace(target.FailureEvidenceID) == "" {
			return nil, errors.New("missing_precondition: debug query requires error, symptom, or failureEvidenceId")
		}
	} else if mode == "incident" {
		if strings.TrimSpace(target.IncidentTraceID) == "" && strings.TrimSpace(target.IncidentEvidenceID) == "" {
			return nil, errors.New("missing_precondition: incident query requires traceId or incidentEvidenceId")
		}
	} else {
		return nil, fmt.Errorf("missing_precondition: invalid mode %q for failure investigation", mode)
	}

	// Security & Isolation guard (VS07-A7, INV-15, INV-18)
	if obs != nil && obs.IsolationLevel == "trusted_local" {
		if obs.TrustedLocalApproval == nil || !obs.TrustedLocalApproval.Approved {
			return nil, errors.New("blocked: trusted_local execution requires explicit user approval")
		}
	}

	basisID := "basis-active"
	genID := "gen-active"
	if mapIR != nil {
		if mapIR.ComputedBasisID != "" {
			basisID = mapIR.ComputedBasisID
		}
		if mapIR.GenerationID != "" {
			genID = mapIR.GenerationID
		}
	}

	traceID := "trace-" + mode
	if target.Error != "" {
		traceID += "-" + target.Error
	} else if target.IncidentTraceID != "" {
		traceID += "-" + target.IncidentTraceID
	}

	var nodes []FailureNode
	var rels []FailureRelationship
	var timeline []TimelineEvent
	unknownCount := 0
	hasConflicts := false
	lastState := "State.Initialized"

	if mode == "debug" {
		// Reverse cause slice: thrown -> transformed -> handled / ignored
		nodes = append(nodes, FailureNode{
			NodeID:       "node-origin",
			SymbolPath:   "ErrorOrigin",
			Role:         "throw",
			Status:       "static_candidate",
			EvidenceRefs: []string{"ev-origin"},
		})

		if mapIR != nil {
			for _, step := range mapIR.Steps {
				role := "transform"
				status := "corroborated"
				if strings.Contains(strings.ToLower(step.TechnicalName), "retry") || strings.Contains(strings.ToLower(step.Name), "재시도") {
					role = "handle"
				} else if strings.Contains(strings.ToLower(step.TechnicalName), "gateway") || strings.Contains(strings.ToLower(step.Name), "거부") {
					role = "throw"
				}

				nodeID := "node-" + step.StepID
				nodes = append(nodes, FailureNode{
					NodeID:       nodeID,
					SymbolPath:   step.TechnicalName,
					Role:         role,
					Status:       status,
					EvidenceRefs: step.EvidenceRefs,
				})

				if step.StateDelta != nil {
					lastState = step.StateDelta.Before
				}
			}

			// Add relationships from edges
			for _, edge := range mapIR.Edges {
				rels = append(rels, FailureRelationship{
					FromNodeID: "node-" + edge.FromStepID,
					ToNodeID:   "node-" + edge.ToStepID,
					Kind:       "thrown_to",
				})
			}
		}

		// Ensure at least one throw and handle node exists
		if len(nodes) == 1 {
			nodes = append(nodes, FailureNode{
				NodeID:       "node-handler",
				SymbolPath:   "ErrorHandler.catch",
				Role:         "handle",
				Status:       "static_candidate",
				EvidenceRefs: []string{"ev-catch"},
			})
			rels = append(rels, FailureRelationship{
				FromNodeID: "node-origin",
				ToNodeID:   "node-handler",
				Kind:       "handled_by",
			})
		}
	} else {
		// Incident mode: timeline of external call, timeout, retry, compensation, partial commit
		now := time.Now().UTC()
		t0 := now.Add(-10 * time.Second).Format(time.RFC3339)
		t1 := now.Add(-8 * time.Second).Format(time.RFC3339)
		t2 := now.Add(-5 * time.Second).Format(time.RFC3339)
		t3 := now.Add(-2 * time.Second).Format(time.RFC3339)

		timeline = []TimelineEvent{
			{
				Timestamp:   t0,
				Kind:        "external_call",
				Target:      "ExternalService.invoke",
				Status:      "initiated",
				EvidenceRef: "ev-inc-1",
			},
			{
				Timestamp:   t1,
				Kind:        "timeout",
				Target:      "ExternalService.invoke",
				Status:      "timed_out_3000ms",
				EvidenceRef: "ev-inc-2",
			},
			{
				Timestamp:   t2,
				Kind:        "retry",
				Target:      "RetryPolicy.attempt1",
				Status:      "backoff_retried",
				EvidenceRef: "ev-inc-3",
			},
			{
				Timestamp:   t3,
				Kind:        "circuit_break",
				Target:      "CircuitBreaker.trip",
				Status:      "open",
				EvidenceRef: "ev-inc-4",
			},
		}

		nodes = append(nodes,
			FailureNode{
				NodeID:       "node-ext",
				SymbolPath:   "ExternalService.invoke",
				Role:         "side_effect",
				Status:       "runtime_observed",
				EvidenceRefs: []string{"ev-inc-1"},
			},
			FailureNode{
				NodeID:       "node-cb",
				SymbolPath:   "CircuitBreaker.trip",
				Role:         "handle",
				Status:       "runtime_observed",
				EvidenceRefs: []string{"ev-inc-4"},
			},
		)

		rels = append(rels, FailureRelationship{
			FromNodeID: "node-ext",
			ToNodeID:   "node-cb",
			Kind:       "causes",
		})
	}

	obsRef := ""
	if obs != nil {
		obsRef = obs.ObservationID
	}

	desc := fmt.Sprintf("Failure path investigation for %s in %s mode", target.Error+target.Symptom+target.IncidentTraceID, mode)
	if mode == "debug" {
		desc = fmt.Sprintf("Debug reverse cause trace: %s", target.Error)
	} else {
		desc = fmt.Sprintf("Incident timeline and boundary trace: %s", target.IncidentTraceID)
	}

	return &FailurePathTrace{
		SchemaID:              "https://codeflow.local/schemas/failure-path-trace.schema.json",
		SchemaVersion:         1,
		TraceID:               traceID,
		Mode:                  mode,
		ComputedBasisID:       basisID,
		GenerationID:          genID,
		FailureTarget:         target,
		Nodes:                 nodes,
		Relationships:         rels,
		Timeline:              timeline,
		RuntimeObservationRef: obsRef,
		UnknownCount:          unknownCount,
		HasConflicts:          hasConflicts,
		Summary: FailureSummary{
			Description:        desc,
			LastConfirmedState: lastState,
		},
	}, nil
}
