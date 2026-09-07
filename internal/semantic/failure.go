package semantic

import "codeflow/internal/slicing"

// FailurePathTrace is the Go representation shared by the v1 compatibility
// decoder and the explicit rflsc.failure-path-trace.v2 producer. Production
// output is always constructed by InvestigateFailureV2.
type FailurePathTrace struct {
	SchemaID                   string                 `json:"schemaId"`
	SchemaVersion              int                    `json:"schemaVersion"`
	TraceID                    string                 `json:"traceId"`
	Mode                       string                 `json:"mode"`
	ComputedBasisID            string                 `json:"computedBasisId"`
	GenerationID               string                 `json:"generationId"`
	ValidatedAgainstSnapshotID string                 `json:"validatedAgainstSnapshotId,omitempty"`
	Freshness                  string                 `json:"freshness,omitempty"`
	FailureTarget              FailureTarget          `json:"failureTarget"`
	Nodes                      []FailureNode          `json:"nodes"`
	Relationships              []FailureRelationship  `json:"relationships"`
	Timeline                   []TimelineEvent        `json:"timeline"`
	RuntimeObservationRef      string                 `json:"runtimeObservationRef,omitempty"`
	UnknownCount               int                    `json:"unknownCount"`
	HasConflicts               bool                   `json:"hasConflicts"`
	Summary                    FailureSummary         `json:"summary"`
	StaticEvidence             []SemanticEvidence     `json:"staticEvidence,omitempty"`
	RuntimeEvidence            []RuntimeEvidence      `json:"runtimeEvidence,omitempty"`
	UnknownFrontier            []FailureFrontier      `json:"unknownFrontier,omitempty"`
	RecoveryStates             []FailureRecoveryState `json:"recoveryStates,omitempty"`
	Coverage                   *FailureCoverage       `json:"coverage,omitempty"`
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
	Role         string   `json:"role"`
	Status       string   `json:"status"`
	EvidenceRefs []string `json:"evidenceRefs,omitempty"`
}

type FailureRelationship struct {
	FromNodeID   string   `json:"fromNodeId"`
	ToNodeID     string   `json:"toNodeId"`
	Kind         string   `json:"kind"`
	EvidenceRefs []string `json:"evidenceRefs,omitempty"`
}

type TimelineEvent struct {
	EventID               string          `json:"eventId,omitempty"`
	Timestamp             string          `json:"timestamp"`
	Kind                  string          `json:"kind"`
	Target                string          `json:"target"`
	Status                string          `json:"status"`
	EvidenceRef           string          `json:"evidenceRef,omitempty"`
	EvidenceRefs          []string        `json:"evidenceRefs,omitempty"`
	SymbolPath            string          `json:"symbolPath,omitempty"`
	Scenario              string          `json:"scenario,omitempty"`
	Environment           string          `json:"environment,omitempty"`
	DependencyFingerprint string          `json:"dependencyFingerprint,omitempty"`
	Anchor                *slicing.Anchor `json:"anchor,omitempty"`
}

type FailureSummary struct {
	Description        string `json:"description"`
	LastConfirmedState string `json:"lastConfirmedState"`
}

// RuntimeObservation is retained as the source-compatible Go name. A v2
// observation must contain the additional identity, scope, and supplied-event
// fields below and pass ValidateRuntimeObservationV2 before use.
type RuntimeObservation struct {
	SchemaID                   string                    `json:"schemaId"`
	SchemaVersion              int                       `json:"schemaVersion"`
	ObservationID              string                    `json:"observationId"`
	Scenario                   string                    `json:"scenario"`
	Input                      string                    `json:"input,omitempty"`
	Environment                string                    `json:"environment"`
	DependencyFingerprint      string                    `json:"dependencyFingerprint,omitempty"`
	TraceCoverage              TraceCoverage             `json:"traceCoverage"`
	ObservedAt                 string                    `json:"observedAt"`
	IsolationLevel             string                    `json:"isolationLevel"`
	TrustedLocalApproval       *TrustedLocalApproval     `json:"trustedLocalApproval,omitempty"`
	TraceID                    string                    `json:"traceId,omitempty"`
	IncidentEvidenceID         string                    `json:"incidentEvidenceId,omitempty"`
	ComputedBasisID            string                    `json:"computedBasisId,omitempty"`
	GenerationID               string                    `json:"generationId,omitempty"`
	ValidatedAgainstSnapshotID string                    `json:"validatedAgainstSnapshotId,omitempty"`
	Freshness                  string                    `json:"freshness,omitempty"`
	TimeWindow                 FailureTimeWindow         `json:"timeWindow,omitempty"`
	Events                     []RuntimeObservationEvent `json:"events,omitempty"`
	Evidence                   []RuntimeEvidence         `json:"evidence,omitempty"`
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

// FailureOptions remains source-compatible with the old seam. New callers
// should pass scope in FailureQueryV2 and use InvestigateFailureV2.
type FailureOptions struct {
	TimeWindow string
}

// InvestigateFailure is the compatibility-shaped entry point. It now fails
// closed unless the supplied map and observation are canonical v2 values. It
// does not create defaults, synthetic nodes, timestamps, or timeline events.
func InvestigateFailure(target FailureTarget, mode string, mapIR *SemanticMapIR, obs *RuntimeObservation, _ FailureOptions) (*FailurePathTrace, error) {
	query := NewFailureQueryV2(mode, target, mapIdentity(mapIR, func(m *SemanticMapIR) string { return m.ComputedBasisID }), mapIdentity(mapIR, func(m *SemanticMapIR) string { return m.GenerationID }), mapIdentity(mapIR, func(m *SemanticMapIR) string { return m.ValidatedAgainstSnapshotID }), mapFreshness(mapIR), nil)
	if mode == "incident" && obs != nil {
		query.Incident = &FailureIncidentQuery{TraceID: target.IncidentTraceID, IncidentEvidenceID: target.IncidentEvidenceID, RuntimeObservationID: obs.ObservationID, Scenario: obs.Scenario, Environment: obs.Environment, DependencyFingerprint: obs.DependencyFingerprint, TimeWindow: obs.TimeWindow}
	}
	return InvestigateFailureV2(query, mapIR, nil, obs, FailureInvestigationOptions{})
}

func mapIdentity(mapIR *SemanticMapIR, value func(*SemanticMapIR) string) string {
	if mapIR == nil {
		return ""
	}
	return value(mapIR)
}

func mapFreshness(mapIR *SemanticMapIR) string {
	if mapIR == nil {
		return ""
	}
	return mapIR.Freshness
}
