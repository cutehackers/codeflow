package semantic

// This file owns the evidence-bounded failure seam.  The v1 implementation in
// failure.go remains readable for older callers, but all callers that provide a
// canonical v2 map must go through this implementation.  In particular, this
// code never invents an origin node, a timestamp, a runtime event, or a
// recovery result.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"

	"codeflow/internal/secret"
	"codeflow/internal/slicing"
)

const (
	FailureQuerySchemaID         = "https://codeflow.local/schemas/rflsc.failure-query.v2.schema.json"
	FailurePathTraceSchemaID     = "https://codeflow.local/schemas/rflsc.failure-path-trace.v2.schema.json"
	RuntimeObservationSchemaID   = "https://codeflow.local/schemas/rflsc.runtime-observation.v2.schema.json"
	FailureContractSchemaVersion = 2
)

// FailurePathTraceV2 and RuntimeObservationV2 are aliases so existing Go
// callers can migrate without copying fields.  Their v2 schema identity is
// still required by the explicit seam and the output constructors below.
type FailurePathTraceV2 = FailurePathTrace
type RuntimeObservationV2 = RuntimeObservation
type FailureQuery = FailureQueryV2
type DebugFailureQuery = FailureDebugQuery
type IncidentFailureQuery = FailureIncidentQuery
type RuntimeEvent = RuntimeObservationEvent
type FailureRuntimeEvidence = RuntimeEvidence

// FailureQueryV2 is the discriminated debug/incident query.  Basis identity is
// deliberately top-level so a consumer cannot select a graph and silently
// fall back to an active generation.
type FailureQueryV2 struct {
	SchemaID                   string                `json:"schemaId"`
	SchemaVersion              int                   `json:"schemaVersion"`
	Mode                       string                `json:"mode"`
	ComputedBasisID            string                `json:"computedBasisId"`
	GenerationID               string                `json:"generationId"`
	ValidatedAgainstSnapshotID string                `json:"validatedAgainstSnapshotId"`
	Freshness                  string                `json:"freshness"`
	Common                     *FailureCommonQuery   `json:"common,omitempty"`
	Debug                      *FailureDebugQuery    `json:"debug,omitempty"`
	Incident                   *FailureIncidentQuery `json:"incident,omitempty"`
}

type FailureCommonQuery struct {
	TaskID         string `json:"taskId,omitempty"`
	IntentRevision int    `json:"intentRevision,omitempty"`
}

type FailureDebugQuery struct {
	Error             string   `json:"error,omitempty"`
	Symptom           string   `json:"symptom,omitempty"`
	FailureEvidenceID string   `json:"failureEvidenceId,omitempty"`
	EvidenceRefs      []string `json:"evidenceRefs,omitempty"`
}

type FailureIncidentQuery struct {
	TraceID               string            `json:"traceId,omitempty"`
	IncidentEvidenceID    string            `json:"incidentEvidenceId,omitempty"`
	RuntimeObservationID  string            `json:"runtimeObservationId,omitempty"`
	Scenario              string            `json:"scenario"`
	Environment           string            `json:"environment"`
	DependencyFingerprint string            `json:"dependencyFingerprint"`
	TimeWindow            FailureTimeWindow `json:"timeWindow"`
}

// FailureTimeWindow is closed on both ends.  A runtime event is accepted only
// when its timestamp is within this declared interval.
type FailureTimeWindow struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// RuntimeObservationEvent is an event supplied by a trace/runtime provider.
// No event is created by the semantic compiler.  EvidenceRefs are required by
// the v2 validator before the event can affect runtime status.
type RuntimeObservationEvent struct {
	EventID           string          `json:"eventId"`
	Timestamp         string          `json:"timestamp"`
	Kind              string          `json:"kind"`
	Target            string          `json:"target"`
	SymbolPath        string          `json:"symbolPath,omitempty"`
	NodeID            string          `json:"nodeId,omitempty"`
	Status            string          `json:"status"`
	Outcome           string          `json:"outcome,omitempty"`
	StaticExpectation string          `json:"staticExpectation,omitempty"`
	EvidenceRef       string          `json:"evidenceRef,omitempty"`
	EvidenceRefs      []string        `json:"evidenceRefs,omitempty"`
	Anchor            *slicing.Anchor `json:"anchor,omitempty"`
}

// RuntimeEvidence is an immutable, scoped runtime evidence descriptor.  It
// may carry an optional source/runtime anchor, but no anchor is trusted until
// its path and range pass ValidateRuntimeObservationV2.
type RuntimeEvidence struct {
	EvidenceID                 string          `json:"evidenceId"`
	ObservationID              string          `json:"observationId,omitempty"`
	TraceID                    string          `json:"traceId,omitempty"`
	GenerationID               string          `json:"generationId,omitempty"`
	ComputedBasisID            string          `json:"computedBasisId"`
	SnapshotID                 string          `json:"snapshotId,omitempty"`
	ValidatedAgainstSnapshotID string          `json:"validatedAgainstSnapshotId,omitempty"`
	Scenario                   string          `json:"scenario"`
	Environment                string          `json:"environment"`
	DependencyFingerprint      string          `json:"dependencyFingerprint"`
	Timestamp                  string          `json:"timestamp"`
	Anchor                     *slicing.Anchor `json:"anchor,omitempty"`
	ValidationStatus           string          `json:"validationStatus"`
	RedactionStatus            string          `json:"redactionStatus"`
}

type FailureFrontier struct {
	FrontierID          string   `json:"frontierId"`
	LastConfirmedNodeID string   `json:"lastConfirmedNodeId"`
	Target              string   `json:"target"`
	RelationKind        string   `json:"relationKind,omitempty"`
	Reason              string   `json:"reason"`
	Path                []string `json:"path,omitempty"`
	EvidenceRefs        []string `json:"evidenceRefs,omitempty"`
}

type FailureRecoveryState struct {
	Kind         string   `json:"kind"`
	Status       string   `json:"status"` // runtime_observed | possible_unknown | conflicting
	Reason       string   `json:"reason"`
	EvidenceRefs []string `json:"evidenceRefs,omitempty"`
}

type FailureCoverage struct {
	IncludedSourceRoots []string `json:"includedSourceRoots,omitempty"`
	ExcludedReasons     []string `json:"excludedReasons,omitempty"`
	Complete            bool     `json:"complete"`
	ConfirmedNodeCount  int      `json:"confirmedNodeCount"`
	ConfirmedEdgeCount  int      `json:"confirmedEdgeCount"`
	UnresolvedEdgeCount int      `json:"unresolvedEdgeCount"`
}

type FailureInvestigationOptions struct {
	// SourceEvidence augments mapIR.Evidence only when an item is referenced by
	// a canonical step and has exactly the requested basis/snapshot.
	SourceEvidence []SemanticEvidence
}

// FailureInvestigationInput is a stable alternative for callers that prefer a
// single request object at the runtime/public boundary.
type FailureInvestigationInput struct {
	Query              FailureQueryV2
	Map                *SemanticMapIR
	SourceEvidence     []SemanticEvidence
	RuntimeObservation *RuntimeObservationV2
}

// NewFailureQueryV2 builds an explicit query from the legacy target shape. It
// never supplies defaults for any identity or incident scope.
func NewFailureQueryV2(mode string, target FailureTarget, basisID, generationID, snapshotID, freshness string, incidentScope *FailureIncidentQuery) FailureQueryV2 {
	query := FailureQueryV2{
		SchemaID:                   FailureQuerySchemaID,
		SchemaVersion:              FailureContractSchemaVersion,
		Mode:                       mode,
		ComputedBasisID:            basisID,
		GenerationID:               generationID,
		ValidatedAgainstSnapshotID: snapshotID,
		Freshness:                  freshness,
	}
	if mode == "debug" {
		query.Debug = &FailureDebugQuery{Error: target.Error, Symptom: target.Symptom, FailureEvidenceID: target.FailureEvidenceID}
	} else if mode == "incident" {
		if incidentScope == nil {
			incidentScope = &FailureIncidentQuery{}
		}
		copyScope := *incidentScope
		if copyScope.TraceID == "" {
			copyScope.TraceID = target.IncidentTraceID
		}
		if copyScope.IncidentEvidenceID == "" {
			copyScope.IncidentEvidenceID = target.IncidentEvidenceID
		}
		query.Incident = &copyScope
	}
	return query
}

// InvestigateFailureEvidenceBounded executes the v2 seam using one input
// object. It is intentionally free of filesystem, clock, adapter, or command
// execution dependencies.
func InvestigateFailureEvidenceBounded(input FailureInvestigationInput) (*FailurePathTraceV2, error) {
	return InvestigateFailureV2(input.Query, input.Map, input.SourceEvidence, input.RuntimeObservation, FailureInvestigationOptions{})
}

// InvestigateFailureWithEvidence is the named seam for callers that already
// have a canonical map and separately resolved source Evidence.
func InvestigateFailureWithEvidence(query FailureQueryV2, mapIR *SemanticMapIR, sourceEvidence []SemanticEvidence, observation *RuntimeObservationV2) (*FailurePathTraceV2, error) {
	return InvestigateFailureV2(query, mapIR, sourceEvidence, observation, FailureInvestigationOptions{})
}

// InvestigateFailureV2 investigates only evidence tied to the supplied query
// and canonical map. All identity, graph, scope, and anchor checks happen
// before a trace is returned.
func InvestigateFailureV2(query FailureQueryV2, mapIR *SemanticMapIR, sourceEvidence []SemanticEvidence, observation *RuntimeObservationV2, _ FailureInvestigationOptions) (*FailurePathTraceV2, error) {
	if err := ValidateFailureQueryV2(query); err != nil {
		return nil, err
	}
	if err := validateFailureMapIdentity(mapIR, query); err != nil {
		return nil, err
	}

	evidenceByID, err := failureEvidenceIndex(mapIR, sourceEvidence, query)
	if err != nil {
		return nil, err
	}
	steps, err := failureStepIndex(mapIR, evidenceByID)
	if err != nil {
		return nil, err
	}

	target, startID, err := resolveFailureStart(query, mapIR, steps, evidenceByID)
	if err != nil {
		return nil, err
	}

	nodes, relationships, frontiers, staticEvidence, coverage := reverseFailurePath(mapIR, steps, evidenceByID, startID)
	if len(nodes) == 0 {
		return nil, errors.New("missing_precondition: failure start did not resolve to a canonical graph node")
	}

	trace := &FailurePathTrace{
		SchemaID:                   FailurePathTraceSchemaID,
		SchemaVersion:              FailureContractSchemaVersion,
		TraceID:                    failureTraceID(query, startID),
		Mode:                       query.Mode,
		ComputedBasisID:            query.ComputedBasisID,
		GenerationID:               query.GenerationID,
		ValidatedAgainstSnapshotID: query.ValidatedAgainstSnapshotID,
		FailureTarget:              target,
		Nodes:                      nodes,
		Relationships:              relationships,
		Timeline:                   []TimelineEvent{},
		UnknownCount:               len(frontiers),
		HasConflicts:               false,
		StaticEvidence:             staticEvidence,
		UnknownFrontier:            frontiers,
		Coverage:                   coverage,
		Summary: FailureSummary{
			Description:        failureDescription(query, startID),
			LastConfirmedState: failureLastState(startID, steps),
		},
		Freshness: query.Freshness,
	}

	if query.Mode == "debug" {
		trace.RecoveryStates = unknownRecoveryStates("runtime observation was not supplied")
		trace.UnknownCount += len(trace.RecoveryStates)
	} else {
		if observation == nil {
			return nil, errors.New("missing_precondition: incident query requires a supplied runtime observation")
		}
		if err := validateIncidentObservation(query, *observation); err != nil {
			return nil, err
		}
		trace.RuntimeObservationRef = observation.ObservationID
		trace.RuntimeEvidence = append([]RuntimeEvidence(nil), observation.Evidence...)
		correlateRuntimeEvidence(trace, steps, observation)
		trace.RecoveryStates = recoveryStatesFromObservation(observation.Events)
		for _, state := range trace.RecoveryStates {
			if state.Status == "possible_unknown" {
				trace.UnknownCount++
			}
		}
		trace.Coverage.UnresolvedEdgeCount += len(trace.UnknownFrontier)
	}

	trace.Coverage.Complete = len(trace.UnknownFrontier) == 0 && trace.UnknownCount == 0
	if !trace.Coverage.Complete {
		trace.Coverage.ExcludedReasons = appendUnique(trace.Coverage.ExcludedReasons, "failure path or runtime recovery is not fully observed")
	}
	if err := ValidateFailurePathTraceV2(*trace); err != nil {
		return nil, err
	}
	return redactFailureTrace(trace)
}

// ValidateFailureQueryV2 applies the discriminated preconditions that cannot
// be represented safely by a permissive legacy TaskViewQuery.
func ValidateFailureQueryV2(query FailureQueryV2) error {
	if query.SchemaID != FailureQuerySchemaID || query.SchemaVersion != FailureContractSchemaVersion {
		return errors.New("invalid_precondition: failure query must use rflsc.failure-query.v2")
	}
	if query.Mode != "debug" && query.Mode != "incident" {
		return fmt.Errorf("invalid_precondition: unsupported failure query mode %q", query.Mode)
	}
	if strings.TrimSpace(query.ComputedBasisID) == "" || strings.TrimSpace(query.GenerationID) == "" || strings.TrimSpace(query.ValidatedAgainstSnapshotID) == "" {
		return errors.New("missing_precondition: failure query requires exact basis, generation and snapshot identity")
	}
	if query.Freshness != "current" && query.Freshness != "historical" {
		return errors.New("invalid_precondition: failure query freshness must be current or historical")
	}
	if query.Mode == "debug" {
		if query.Debug == nil || query.Incident != nil {
			return errors.New("missing_precondition: debug failure query requires a debug discriminator")
		}
		if strings.TrimSpace(query.Debug.Error) == "" && strings.TrimSpace(query.Debug.Symptom) == "" && strings.TrimSpace(query.Debug.FailureEvidenceID) == "" && len(query.Debug.EvidenceRefs) == 0 {
			return errors.New("missing_precondition: debug query requires error, symptom, or failureEvidenceId")
		}
		return nil
	}
	if query.Incident == nil || query.Debug != nil {
		return errors.New("missing_precondition: incident failure query requires an incident discriminator")
	}
	incident := query.Incident
	if strings.TrimSpace(incident.TraceID) == "" && strings.TrimSpace(incident.IncidentEvidenceID) == "" {
		return errors.New("missing_precondition: incident query requires traceId or incidentEvidenceId")
	}
	if strings.TrimSpace(incident.Scenario) == "" || strings.TrimSpace(incident.Environment) == "" || strings.TrimSpace(incident.DependencyFingerprint) == "" {
		return errors.New("missing_precondition: incident query requires scenario, environment and dependency fingerprint")
	}
	if _, _, err := parseFailureWindow(incident.TimeWindow); err != nil {
		return fmt.Errorf("missing_precondition: incident query requires a valid time window: %w", err)
	}
	return nil
}

// ValidateRuntimeObservationV2 validates runtime identity, scope, supplied
// event evidence, and anchors without consulting the clock or filesystem.
func ValidateRuntimeObservationV2(observation RuntimeObservationV2) error {
	if observation.SchemaID != RuntimeObservationSchemaID || observation.SchemaVersion != FailureContractSchemaVersion {
		return errors.New("invalid_precondition: runtime observation must use rflsc.runtime-observation.v2")
	}
	if strings.TrimSpace(observation.ObservationID) == "" || strings.TrimSpace(observation.TraceID) == "" || strings.TrimSpace(observation.Scenario) == "" || strings.TrimSpace(observation.Environment) == "" || strings.TrimSpace(observation.DependencyFingerprint) == "" {
		return errors.New("missing_precondition: runtime observation identity and scope are required")
	}
	if strings.TrimSpace(observation.ComputedBasisID) == "" || strings.TrimSpace(observation.GenerationID) == "" || strings.TrimSpace(observation.ValidatedAgainstSnapshotID) == "" {
		return errors.New("missing_precondition: runtime observation requires exact basis, generation and snapshot identity")
	}
	if observation.Freshness != "current" && observation.Freshness != "historical" {
		return errors.New("invalid_precondition: runtime observation freshness must be current or historical")
	}
	if _, _, err := parseFailureWindow(observation.TimeWindow); err != nil {
		return fmt.Errorf("invalid_precondition: runtime observation time window: %w", err)
	}
	if _, err := time.Parse(time.RFC3339Nano, observation.ObservedAt); err != nil {
		return fmt.Errorf("invalid_anchor: runtime observation observedAt is not RFC3339: %w", err)
	}
	if observation.TraceCoverage.TotalSpans < 1 || observation.TraceCoverage.SpansCovered < 0 || observation.TraceCoverage.SpansCovered > observation.TraceCoverage.TotalSpans || observation.TraceCoverage.Ratio < 0 || observation.TraceCoverage.Ratio > 1 {
		return errors.New("invalid_precondition: runtime trace coverage is outside its declared bounds")
	}
	if observation.IsolationLevel == "trusted_local" && (observation.TrustedLocalApproval == nil || !observation.TrustedLocalApproval.Approved) {
		return errors.New("blocked: trusted_local execution requires explicit user approval")
	}
	if observation.IsolationLevel == "trusted_local" {
		approval := observation.TrustedLocalApproval
		if strings.TrimSpace(approval.ApprovedBy) == "" {
			return errors.New("blocked: trusted_local approval requires an approving actor")
		}
		if _, err := time.Parse(time.RFC3339Nano, approval.Timestamp); err != nil {
			return fmt.Errorf("invalid_anchor: trusted_local approval timestamp is not RFC3339: %w", err)
		}
	}

	evidenceIDs := map[string]bool{}
	for _, evidence := range observation.Evidence {
		if strings.TrimSpace(evidence.EvidenceID) == "" || evidenceIDs[evidence.EvidenceID] {
			return errors.New("invalid_evidence: runtime evidence ids must be unique and non-empty")
		}
		evidenceIDs[evidence.EvidenceID] = true
		if evidence.ComputedBasisID != observation.ComputedBasisID || evidence.TraceID != "" && evidence.TraceID != observation.TraceID || evidence.Scenario != observation.Scenario || evidence.Environment != observation.Environment || evidence.DependencyFingerprint != observation.DependencyFingerprint || evidence.ValidationStatus != "verified" || !validRedactionStatus(evidence.RedactionStatus) {
			return fmt.Errorf("invalid_evidence: runtime evidence %q is outside the observation scope", evidence.EvidenceID)
		}
		if evidence.GenerationID != "" && evidence.GenerationID != observation.GenerationID {
			return fmt.Errorf("incomparable_basis: runtime evidence %q has a different generation", evidence.EvidenceID)
		}
		if evidence.SnapshotID != "" && evidence.SnapshotID != observation.ValidatedAgainstSnapshotID {
			return fmt.Errorf("incomparable_basis: runtime evidence %q has a different snapshot", evidence.EvidenceID)
		}
		if evidence.ValidatedAgainstSnapshotID != "" && evidence.ValidatedAgainstSnapshotID != observation.ValidatedAgainstSnapshotID {
			return fmt.Errorf("incomparable_basis: runtime evidence %q has a different validated snapshot", evidence.EvidenceID)
		}
		if evidence.Timestamp != "" {
			if _, err := time.Parse(time.RFC3339Nano, evidence.Timestamp); err != nil {
				return fmt.Errorf("invalid_anchor: runtime evidence %q has invalid timestamp", evidence.EvidenceID)
			}
		}
		if evidence.Anchor != nil && !validFailureAnchor(*evidence.Anchor) {
			return fmt.Errorf("invalid_anchor: runtime evidence %q has an invalid source anchor", evidence.EvidenceID)
		}
	}
	eventIDs := map[string]bool{}
	for index, event := range observation.Events {
		if strings.TrimSpace(event.EventID) == "" || strings.TrimSpace(event.Timestamp) == "" || strings.TrimSpace(event.Kind) == "" || strings.TrimSpace(event.Target) == "" || strings.TrimSpace(event.Status) == "" {
			return fmt.Errorf("missing_precondition: runtime event %d has incomplete identity", index)
		}
		if eventIDs[event.EventID] {
			return fmt.Errorf("invalid_evidence: runtime event %q has duplicate identity", event.EventID)
		}
		eventIDs[event.EventID] = true
		stamp, err := time.Parse(time.RFC3339Nano, event.Timestamp)
		if err != nil {
			return fmt.Errorf("invalid_anchor: runtime event %q has invalid timestamp", event.EventID)
		}
		from, to, _ := parseFailureWindow(observation.TimeWindow)
		if stamp.Before(from) || stamp.After(to) {
			return fmt.Errorf("invalid_scope: runtime event %q is outside the declared time window", event.EventID)
		}
		if event.Anchor != nil && !validFailureAnchor(*event.Anchor) {
			return fmt.Errorf("invalid_anchor: runtime event %q has an invalid source anchor", event.EventID)
		}
		refs := runtimeEventEvidenceRefs(event)
		if len(refs) == 0 {
			return fmt.Errorf("invalid_evidence: runtime event %q has no Evidence reference", event.EventID)
		}
		if len(observation.Evidence) == 0 {
			return fmt.Errorf("invalid_evidence: runtime event %q has no supplied Evidence set", event.EventID)
		}
		for _, ref := range refs {
			if !evidenceIDs[ref] {
				return fmt.Errorf("invalid_evidence: runtime event %q references unknown Evidence %q", event.EventID, ref)
			}
		}
	}
	return nil
}

// ValidateFailurePathTraceV2 validates the egress representation. It rejects
// default origins/self-loops, dangling relations, missing hop Evidence, and
// status claims unsupported by supplied runtime Evidence.
func ValidateFailurePathTraceV2(trace FailurePathTraceV2) error {
	if trace.SchemaID != FailurePathTraceSchemaID || trace.SchemaVersion != FailureContractSchemaVersion {
		return errors.New("invalid_precondition: failure path trace must use rflsc.failure-path-trace.v2")
	}
	if trace.Mode != "debug" && trace.Mode != "incident" {
		return errors.New("invalid_precondition: failure path trace has unsupported mode")
	}
	if strings.TrimSpace(trace.ComputedBasisID) == "" || strings.TrimSpace(trace.GenerationID) == "" || strings.TrimSpace(trace.ValidatedAgainstSnapshotID) == "" || (trace.Freshness != "current" && trace.Freshness != "historical") {
		return errors.New("missing_precondition: failure path trace identity is incomplete")
	}
	if len(trace.Nodes) == 0 {
		return errors.New("missing_precondition: failure path trace has no confirmed start node")
	}
	if strings.TrimSpace(trace.TraceID) == "" {
		return errors.New("missing_precondition: failure path trace requires trace identity")
	}
	nodeIDs := map[string]bool{}
	for _, node := range trace.Nodes {
		if strings.TrimSpace(node.NodeID) == "" || strings.TrimSpace(node.SymbolPath) == "" || node.NodeID == "node-origin" || node.SymbolPath == "ErrorOrigin" || node.Role == "" || node.Status == "" {
			return errors.New("invalid_identity: failure path contains an invalid or synthetic node")
		}
		if nodeIDs[node.NodeID] {
			return fmt.Errorf("invalid_identity: duplicate failure node %q", node.NodeID)
		}
		nodeIDs[node.NodeID] = true
		if len(node.EvidenceRefs) == 0 {
			return fmt.Errorf("invalid_evidence: node %q has no Evidence", node.NodeID)
		}
		if (node.Status == "runtime_observed" || node.Status == "corroborated" || node.Status == "conflicting") && len(node.EvidenceRefs) == 0 {
			return fmt.Errorf("invalid_evidence: node %q has runtime/conflict status without Evidence", node.NodeID)
		}
	}
	for _, evidence := range trace.StaticEvidence {
		if strings.TrimSpace(evidence.EvidenceID) == "" || evidence.ComputedBasisID != trace.ComputedBasisID || evidence.SnapshotID != trace.ValidatedAgainstSnapshotID || evidence.ValidationStatus != "verified" || !validRedactionStatus(evidence.RedactionStatus) || !validFailureAnchor(evidence.Anchor) {
			return fmt.Errorf("invalid_evidence: trace Evidence %q is outside its exact basis or redaction boundary", evidence.EvidenceID)
		}
	}
	for _, evidence := range trace.RuntimeEvidence {
		if strings.TrimSpace(evidence.EvidenceID) == "" || evidence.ComputedBasisID != trace.ComputedBasisID || evidence.ValidationStatus != "verified" || !validRedactionStatus(evidence.RedactionStatus) {
			return fmt.Errorf("invalid_evidence: runtime trace Evidence %q is outside its exact basis or redaction boundary", evidence.EvidenceID)
		}
		if evidence.GenerationID != "" && evidence.GenerationID != trace.GenerationID {
			return fmt.Errorf("incomparable_basis: runtime trace Evidence %q has a different generation", evidence.EvidenceID)
		}
		if evidence.SnapshotID != "" && evidence.SnapshotID != trace.ValidatedAgainstSnapshotID {
			return fmt.Errorf("incomparable_basis: runtime trace Evidence %q has a different snapshot", evidence.EvidenceID)
		}
		if evidence.ValidatedAgainstSnapshotID != "" && evidence.ValidatedAgainstSnapshotID != trace.ValidatedAgainstSnapshotID {
			return fmt.Errorf("incomparable_basis: runtime trace Evidence %q has a different validated snapshot", evidence.EvidenceID)
		}
		if evidence.Anchor != nil && !validFailureAnchor(*evidence.Anchor) {
			return fmt.Errorf("invalid_anchor: runtime trace Evidence %q has an invalid source anchor", evidence.EvidenceID)
		}
	}
	foundConflict := false
	for _, relation := range trace.Relationships {
		if relation.FromNodeID == relation.ToNodeID {
			return fmt.Errorf("invalid_identity: failure relation %q is a self-loop", relation.FromNodeID)
		}
		if !nodeIDs[relation.FromNodeID] || !nodeIDs[relation.ToNodeID] {
			return errors.New("invalid_identity: failure relation endpoint is not a canonical node")
		}
		if len(relation.EvidenceRefs) == 0 {
			return fmt.Errorf("invalid_evidence: confirmed failure relation %s->%s has no Evidence", relation.FromNodeID, relation.ToNodeID)
		}
	}
	for _, node := range trace.Nodes {
		if node.Status == "conflicting" {
			foundConflict = true
		}
	}
	if foundConflict != trace.HasConflicts {
		return errors.New("invalid_status: hasConflicts does not match conflicting nodes")
	}
	for _, event := range trace.Timeline {
		if strings.TrimSpace(event.Timestamp) == "" || strings.TrimSpace(event.Kind) == "" || strings.TrimSpace(event.Target) == "" || strings.TrimSpace(event.Status) == "" || len(runtimeTimelineEvidenceRefs(event)) == 0 {
			return errors.New("invalid_evidence: runtime timeline event is incomplete or lacks Evidence")
		}
		if _, err := time.Parse(time.RFC3339Nano, event.Timestamp); err != nil {
			return errors.New("invalid_anchor: runtime timeline timestamp is not RFC3339")
		}
		if event.Anchor != nil && !validFailureAnchor(*event.Anchor) {
			return errors.New("invalid_anchor: runtime timeline source anchor is invalid")
		}
	}
	for _, frontier := range trace.UnknownFrontier {
		if strings.TrimSpace(frontier.FrontierID) == "" || strings.TrimSpace(frontier.LastConfirmedNodeID) == "" || !nodeIDs[frontier.LastConfirmedNodeID] || strings.TrimSpace(frontier.Target) == "" || strings.TrimSpace(frontier.Reason) == "" {
			return errors.New("invalid_identity: unknown frontier is incomplete")
		}
	}
	if trace.Coverage == nil {
		return errors.New("missing_precondition: failure coverage boundary is required")
	}
	minimumUnknown := len(trace.UnknownFrontier)
	for _, state := range trace.RecoveryStates {
		if state.Status == "possible_unknown" {
			minimumUnknown++
		}
	}
	if trace.UnknownCount != minimumUnknown {
		return fmt.Errorf("invalid_status: unknownCount must equal unresolved frontier and recovery state count, got %d want %d", trace.UnknownCount, minimumUnknown)
	}
	return nil
}

func validateFailureMapIdentity(mapIR *SemanticMapIR, query FailureQueryV2) error {
	if mapIR == nil {
		return errors.New("missing_precondition: a canonical semantic map is required")
	}
	if mapIR.SchemaID != SemanticMapSchemaID || mapIR.SchemaVersion != SemanticSchemaVersion || strings.TrimSpace(mapIR.GenerationID) == "" || strings.TrimSpace(mapIR.ComputedBasisID) == "" || strings.TrimSpace(mapIR.ValidatedAgainstSnapshotID) == "" {
		return errors.New("invalid_graph: semantic map identity is incomplete or non-canonical")
	}
	if mapIR.ComputedBasisID != query.ComputedBasisID || mapIR.GenerationID != query.GenerationID || mapIR.ValidatedAgainstSnapshotID != query.ValidatedAgainstSnapshotID || mapIR.Freshness != query.Freshness {
		return errors.New("incomparable_basis: failure query identity does not match semantic map")
	}
	if mapIR.Authority != "candidate" && mapIR.Authority != "historical" {
		return errors.New("invalid_authority: failure investigation requires candidate or historical graph authority")
	}
	return nil
}

func failureEvidenceIndex(mapIR *SemanticMapIR, extra []SemanticEvidence, query FailureQueryV2) (map[string]SemanticEvidence, error) {
	index := map[string]SemanticEvidence{}
	for _, evidence := range mapIR.Evidence {
		if evidence.EvidenceID == "" {
			return nil, errors.New("invalid_evidence: semantic map contains empty Evidence identity")
		}
		if _, exists := index[evidence.EvidenceID]; exists {
			return nil, fmt.Errorf("invalid_evidence: duplicate semantic map Evidence %q", evidence.EvidenceID)
		}
		index[evidence.EvidenceID] = evidence
	}
	for _, evidence := range extra {
		if evidence.EvidenceID == "" {
			return nil, errors.New("invalid_evidence: supplied source Evidence has empty identity")
		}
		if existing, exists := index[evidence.EvidenceID]; exists && !sameSemanticEvidence(existing, evidence) {
			return nil, fmt.Errorf("invalid_evidence: supplied Evidence %q conflicts with canonical map Evidence", evidence.EvidenceID)
		}
		index[evidence.EvidenceID] = evidence
	}
	for id, evidence := range index {
		if evidence.ComputedBasisID != query.ComputedBasisID || evidence.SnapshotID != query.ValidatedAgainstSnapshotID || evidence.ValidationStatus != "verified" || !validRedactionStatus(evidence.RedactionStatus) || !validFailureAnchor(evidence.Anchor) {
			return nil, fmt.Errorf("invalid_evidence: Evidence %q is not verified against the requested basis/snapshot", id)
		}
	}
	return index, nil
}

func failureStepIndex(mapIR *SemanticMapIR, evidenceByID map[string]SemanticEvidence) (map[string]SemanticStep, error) {
	steps := map[string]SemanticStep{}
	for _, step := range mapIR.Steps {
		if strings.TrimSpace(step.StepID) == "" || steps[step.StepID].StepID != "" {
			return nil, errors.New("invalid_graph: semantic map contains duplicate or empty step identity")
		}
		if strings.TrimSpace(step.TechnicalName) == "" && strings.TrimSpace(step.Name) == "" {
			return nil, fmt.Errorf("invalid_graph: step %q has no canonical symbol path", step.StepID)
		}
		if len(step.EvidenceRefs) == 0 {
			return nil, fmt.Errorf("invalid_evidence: step %q has no Evidence refs", step.StepID)
		}
		for _, ref := range step.EvidenceRefs {
			if _, ok := evidenceByID[ref]; !ok {
				return nil, fmt.Errorf("invalid_evidence: step %q references unknown Evidence %q", step.StepID, ref)
			}
		}
		if !validFailureAnchor(step.Anchor) {
			return nil, fmt.Errorf("invalid_anchor: step %q has an invalid source anchor", step.StepID)
		}
		steps[step.StepID] = step
	}
	for _, edge := range mapIR.Edges {
		from, fromOK := steps[edge.FromStepID]
		to, toOK := steps[edge.ToStepID]
		if !fromOK || !toOK {
			return nil, fmt.Errorf("invalid_graph: edge endpoint %q -> %q is not canonical", edge.FromStepID, edge.ToStepID)
		}
		if strings.TrimSpace(edge.ToSymbolPath) == "" || !failureEdgeTargetMatches(edge.ToSymbolPath, to) {
			return nil, fmt.Errorf("invalid_graph: edge target %q does not match canonical step %q", edge.ToSymbolPath, edge.ToStepID)
		}
		if !verifiedFailureEdge(edge.ResolutionStatus) {
			_ = from
		}
	}
	return steps, nil
}

func failureEdgeTargetMatches(symbol string, step SemanticStep) bool {
	if symbol == canonicalStepSymbol(step) || symbol == step.Anchor.EnclosingSymbolPath {
		return true
	}
	canonical := step.Anchor.RepoRelativePath + "#" + canonicalStepSymbol(step)
	return symbol == canonical
}

func resolveFailureStart(query FailureQueryV2, mapIR *SemanticMapIR, steps map[string]SemanticStep, evidenceByID map[string]SemanticEvidence) (FailureTarget, string, error) {
	if query.Mode == "debug" {
		debug := query.Debug
		target := FailureTarget{Error: debug.Error, Symptom: debug.Symptom, FailureEvidenceID: debug.FailureEvidenceID}
		requestedEvidence := append([]string(nil), debug.EvidenceRefs...)
		if debug.FailureEvidenceID != "" {
			requestedEvidence = append(requestedEvidence, debug.FailureEvidenceID)
		}
		var candidates []string
		for id, step := range steps {
			matched := false
			for _, ref := range requestedEvidence {
				if containsString(step.EvidenceRefs, ref) {
					matched = true
				}
			}
			if strings.TrimSpace(debug.Error) != "" && failureTextMatches(debug.Error, step, evidenceByID) {
				matched = true
			}
			if strings.TrimSpace(debug.Symptom) != "" && failureTextMatches(debug.Symptom, step, evidenceByID) {
				matched = true
			}
			if matched {
				candidates = append(candidates, id)
			}
		}
		if len(candidates) == 0 {
			return FailureTarget{}, "", errors.New("missing_precondition: debug target is not tied to verified failure Evidence")
		}
		if len(candidates) > 1 {
			sort.Strings(candidates)
			return FailureTarget{}, "", &QueryError{Code: ErrCodeAmbiguousTarget, Message: "debug Evidence resolves to multiple graph nodes", CandidateTargets: candidates}
		}
		return target, candidates[0], nil
	}
	incident := query.Incident
	target := FailureTarget{IncidentTraceID: incident.TraceID, IncidentEvidenceID: incident.IncidentEvidenceID}
	// Incident starts at a supplied trace only after the runtime observation is
	// validated. Static path selection uses an explicit failure Evidence ref if
	// present and otherwise the first runtime event is resolved later.
	if incident.RuntimeObservationID != "" {
		target.IncidentEvidenceID = incident.RuntimeObservationID
	}
	for id := range steps {
		if incident.IncidentEvidenceID != "" {
			for _, ref := range steps[id].EvidenceRefs {
				if ref == incident.IncidentEvidenceID {
					return target, id, nil
				}
			}
		}
	}
	// An incident trace may not have a static failure anchor. In that case the
	// runtime observation is correlated against the first supplied event, but a
	// static node must still exist before any output is emitted.
	for id, step := range steps {
		if step.Kind == "failure" || strings.Contains(strings.ToLower(step.TechnicalName), "fail") || strings.Contains(strings.ToLower(step.Name), "error") {
			return target, id, nil
		}
	}
	return target, firstStepID(steps), nil
}

func reverseFailurePath(mapIR *SemanticMapIR, steps map[string]SemanticStep, evidenceByID map[string]SemanticEvidence, startID string) ([]FailureNode, []FailureRelationship, []FailureFrontier, []SemanticEvidence, *FailureCoverage) {
	orderedIDs := []string{}
	seen := map[string]bool{}
	queue := []string{startID}
	frontiers := []FailureFrontier{}
	frontierKeys := map[string]bool{}
	relationships := []FailureRelationship{}
	relKeys := map[string]bool{}
	staticEvidence := []SemanticEvidence{}
	staticEvidenceSeen := map[string]bool{}
	addFrontier := func(lastID, target, relationKind, reason string, refs []string) {
		key := strings.Join([]string{lastID, target, relationKind, reason}, "\x00")
		if frontierKeys[key] {
			return
		}
		frontierKeys[key] = true
		frontiers = append(frontiers, FailureFrontier{FrontierID: fmt.Sprintf("frontier-%03d", len(frontiers)+1), LastConfirmedNodeID: "node-" + lastID, Target: target, RelationKind: relationKind, Reason: reason, Path: failurePathSymbols(orderedIDs, steps), EvidenceRefs: dedupeStrings(refs)})
	}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if seen[current] {
			continue
		}
		step, ok := steps[current]
		if !ok {
			continue
		}
		seen[current] = true
		orderedIDs = append(orderedIDs, current)
		for _, ref := range step.EvidenceRefs {
			if evidence, ok := evidenceByID[ref]; ok && !staticEvidenceSeen[ref] {
				staticEvidenceSeen[ref] = true
				staticEvidence = append(staticEvidence, evidence)
			}
		}
		for _, edge := range mapIR.Edges {
			if edge.ToStepID != current {
				continue
			}
			from, fromOK := steps[edge.FromStepID]
			if !fromOK {
				addFrontier(current, edge.ToSymbolPath, edge.Kind, "relation source is outside the canonical graph", step.EvidenceRefs)
				continue
			}
			if !verifiedFailureEdge(edge.ResolutionStatus) {
				addFrontier(current, canonicalStepSymbol(from), edge.Kind, "relation is unresolved by the canonical graph", append(step.EvidenceRefs, from.EvidenceRefs...))
				continue
			}
			refs := append([]string{}, step.EvidenceRefs...)
			refs = append(refs, from.EvidenceRefs...)
			relation := FailureRelationship{FromNodeID: "node-" + from.StepID, ToNodeID: "node-" + step.StepID, Kind: failureRelationKind(edge.Kind, from, step), EvidenceRefs: dedupeStrings(refs)}
			relKey := relation.FromNodeID + "\x00" + relation.ToNodeID
			if !relKeys[relKey] {
				relKeys[relKey] = true
				relationships = append(relationships, relation)
			}
			if !seen[from.StepID] {
				queue = append(queue, from.StepID)
			}
		}
	}
	for _, unknown := range mapIR.Unknowns {
		if strings.TrimSpace(unknown.Subject) == "" || strings.TrimSpace(unknown.Reason) == "" {
			continue
		}
		addFrontier(startID, unknown.Subject, "unknown", unknown.Reason, steps[startID].EvidenceRefs)
	}
	sort.SliceStable(relationships, func(i, j int) bool {
		if relationships[i].FromNodeID != relationships[j].FromNodeID {
			return relationships[i].FromNodeID < relationships[j].FromNodeID
		}
		return relationships[i].ToNodeID < relationships[j].ToNodeID
	})
	nodes := make([]FailureNode, 0, len(orderedIDs))
	for _, id := range orderedIDs {
		step := steps[id]
		nodes = append(nodes, FailureNode{NodeID: "node-" + id, SymbolPath: canonicalStepSymbol(step), Role: failureRole(step), Status: "static_candidate", EvidenceRefs: dedupeStrings(step.EvidenceRefs)})
	}
	coverage := &FailureCoverage{Complete: len(frontiers) == 0, ConfirmedNodeCount: len(nodes), ConfirmedEdgeCount: len(relationships), UnresolvedEdgeCount: len(frontiers)}
	if mapIR.Coverage != nil {
		coverage.IncludedSourceRoots = append([]string(nil), mapIR.Coverage.IncludedSourceRoots...)
		coverage.ExcludedReasons = append([]string(nil), mapIR.Coverage.ExcludedReasons...)
	} else {
		coverage.ExcludedReasons = []string{"semantic map did not declare a coverage boundary"}
	}
	return nodes, relationships, frontiers, staticEvidence, coverage
}

func validateIncidentObservation(query FailureQueryV2, observation RuntimeObservationV2) error {
	if err := ValidateRuntimeObservationV2(observation); err != nil {
		return err
	}
	incident := query.Incident
	if incident.TraceID != "" && observation.TraceID != incident.TraceID {
		return errors.New("incomparable_basis: runtime observation traceId does not match incident query")
	}
	if incident.IncidentEvidenceID != "" && observation.IncidentEvidenceID != incident.IncidentEvidenceID {
		return errors.New("incomparable_basis: runtime observation incident Evidence does not match incident query")
	}
	if observation.ComputedBasisID != query.ComputedBasisID || observation.GenerationID != query.GenerationID || observation.ValidatedAgainstSnapshotID != query.ValidatedAgainstSnapshotID || observation.Freshness != query.Freshness {
		return errors.New("incomparable_basis: runtime observation identity does not match incident query")
	}
	if observation.Scenario != incident.Scenario || observation.Environment != incident.Environment || observation.DependencyFingerprint != incident.DependencyFingerprint || observation.TimeWindow != incident.TimeWindow {
		return errors.New("invalid_scope: runtime observation does not match incident scenario, environment, dependency or time window")
	}
	return nil
}

func correlateRuntimeEvidence(trace *FailurePathTrace, steps map[string]SemanticStep, observation *RuntimeObservationV2) {
	for index, event := range observation.Events {
		refs := runtimeEventEvidenceRefs(event)
		timeline := TimelineEvent{EventID: event.EventID, Timestamp: event.Timestamp, Kind: event.Kind, Target: event.Target, Status: event.Status, EvidenceRef: firstString(refs...), EvidenceRefs: refs, SymbolPath: event.SymbolPath, Scenario: observation.Scenario, Environment: observation.Environment, DependencyFingerprint: observation.DependencyFingerprint, Anchor: event.Anchor}
		trace.Timeline = append(trace.Timeline, timeline)
		stepID, ok := correlateEventStep(event, steps)
		if !ok {
			last := trace.Nodes[0].NodeID
			trace.UnknownFrontier = append(trace.UnknownFrontier, FailureFrontier{FrontierID: fmt.Sprintf("frontier-runtime-%03d", index+1), LastConfirmedNodeID: last, Target: firstNonEmpty(event.SymbolPath, event.Target), RelationKind: event.Kind, Reason: "runtime event has no canonical static node", EvidenceRefs: refs})
			continue
		}
		for i := range trace.Nodes {
			if trace.Nodes[i].NodeID != "node-"+stepID {
				continue
			}
			trace.Nodes[i].EvidenceRefs = dedupeStrings(append(trace.Nodes[i].EvidenceRefs, refs...))
			if runtimeEventConflicts(event) {
				trace.Nodes[i].Status = "conflicting"
				trace.HasConflicts = true
			} else if trace.Nodes[i].Status != "conflicting" {
				trace.Nodes[i].Status = "corroborated"
			}
		}
	}
	sort.SliceStable(trace.Timeline, func(i, j int) bool {
		if trace.Timeline[i].Timestamp != trace.Timeline[j].Timestamp {
			return trace.Timeline[i].Timestamp < trace.Timeline[j].Timestamp
		}
		return trace.Timeline[i].EventID < trace.Timeline[j].EventID
	})
	trace.UnknownCount = len(trace.UnknownFrontier)
}

func recoveryStatesFromObservation(events []RuntimeObservationEvent) []FailureRecoveryState {
	const recoveryKinds = "timeout,retry,circuit_break,compensation"
	wanted := strings.Split(recoveryKinds, ",")
	states := make([]FailureRecoveryState, 0, len(wanted))
	for _, kind := range wanted {
		found := false
		var refs []string
		for _, event := range events {
			if event.Kind == kind {
				refs = append(refs, runtimeEventEvidenceRefs(event)...)
				if !runtimeEventConflicts(event) {
					found = true
				}
			}
		}
		if found {
			states = append(states, FailureRecoveryState{Kind: kind, Status: "runtime_observed", Reason: "supplied runtime event is inside the declared scope", EvidenceRefs: dedupeStrings(refs)})
		} else {
			states = append(states, FailureRecoveryState{Kind: kind, Status: "possible_unknown", Reason: "no supplied runtime event establishes whether this behavior executed"})
		}
	}
	return states
}

func unknownRecoveryStates(reason string) []FailureRecoveryState {
	return []FailureRecoveryState{
		{Kind: "timeout", Status: "possible_unknown", Reason: reason},
		{Kind: "retry", Status: "possible_unknown", Reason: reason},
		{Kind: "circuit_break", Status: "possible_unknown", Reason: reason},
		{Kind: "compensation", Status: "possible_unknown", Reason: reason},
	}
}

func redactFailureTrace(trace *FailurePathTrace) (*FailurePathTrace, error) {
	raw, err := json.Marshal(trace)
	if err != nil {
		return nil, fmt.Errorf("redact failure trace: %w", err)
	}
	clean, _, err := secret.RedactJSON(raw)
	if err != nil {
		return nil, fmt.Errorf("redact failure trace: %w", err)
	}
	var out FailurePathTrace
	if err := json.Unmarshal(clean, &out); err != nil {
		return nil, fmt.Errorf("decode redacted failure trace: %w", err)
	}
	return &out, nil
}

func parseFailureWindow(window FailureTimeWindow) (time.Time, time.Time, error) {
	if strings.TrimSpace(window.From) == "" || strings.TrimSpace(window.To) == "" {
		return time.Time{}, time.Time{}, errors.New("from and to are required")
	}
	from, err := time.Parse(time.RFC3339Nano, window.From)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("from is not RFC3339")
	}
	to, err := time.Parse(time.RFC3339Nano, window.To)
	if err != nil || to.Before(from) {
		return time.Time{}, time.Time{}, fmt.Errorf("to is invalid or before from")
	}
	return from, to, nil
}

func validFailureAnchor(anchor slicing.Anchor) bool {
	rel := strings.TrimSpace(anchor.RepoRelativePath)
	if rel == "" || rel != anchor.RepoRelativePath || strings.HasPrefix(rel, "/") || strings.Contains(rel, "\\") || strings.Contains(rel, "\x00") || path.Clean(rel) != rel || rel == "." || strings.HasPrefix(rel, "../") || strings.Contains(rel, "/../") {
		return false
	}
	if strings.TrimSpace(anchor.EnclosingSymbolPath) == "" || strings.TrimSpace(anchor.FileHash) == "" || strings.TrimSpace(anchor.SpanHash) == "" {
		return false
	}
	return anchor.ByteRange[0] >= 0 && anchor.ByteRange[1] > anchor.ByteRange[0]
}

func validRedactionStatus(status string) bool {
	return status == "clean" || status == "redacted" || status == "passed"
}

func verifiedFailureEdge(status string) bool {
	return status == "verified" || status == "resolved"
}

func canonicalStepSymbol(step SemanticStep) string {
	if strings.TrimSpace(step.TechnicalName) != "" {
		return step.TechnicalName
	}
	return step.Name
}

func failureTextMatches(text string, step SemanticStep, evidenceByID map[string]SemanticEvidence) bool {
	needle := strings.TrimSpace(text)
	if strings.EqualFold(needle, canonicalStepSymbol(step)) || strings.EqualFold(needle, step.Name) || strings.EqualFold(needle, step.Anchor.EnclosingSymbolPath) {
		return true
	}
	for _, ref := range step.EvidenceRefs {
		evidence := evidenceByID[ref]
		if strings.Contains(strings.ToLower(evidence.DocumentRevisionID), strings.ToLower(needle)) {
			return true
		}
	}
	return false
}

func failureRole(step SemanticStep) string {
	kind := strings.ToLower(step.Kind + " " + step.TechnicalName + " " + step.Name)
	switch {
	case strings.Contains(kind, "failure"), strings.Contains(kind, "throw"), strings.Contains(kind, "error"):
		return "throw"
	case strings.Contains(kind, "handle"), strings.Contains(kind, "retry"), strings.Contains(kind, "catch"):
		return "handle"
	case strings.Contains(kind, "effect"), strings.Contains(kind, "external"), strings.Contains(kind, "side"):
		return "side_effect"
	case strings.Contains(kind, "ignore"):
		return "ignore"
	default:
		return "transform"
	}
}

func failureRelationKind(edgeKind string, from, to SemanticStep) string {
	lower := strings.ToLower(edgeKind + " " + from.Kind + " " + to.Kind)
	switch {
	case strings.Contains(lower, "throw"):
		return "thrown_to"
	case strings.Contains(lower, "transform"):
		return "transformed_to"
	case strings.Contains(lower, "handle"):
		return "handled_by"
	case strings.Contains(lower, "ignore"):
		return "ignored_by"
	default:
		return "causes"
	}
}

func correlateEventStep(event RuntimeObservationEvent, steps map[string]SemanticStep) (string, bool) {
	for id, step := range steps {
		if event.NodeID != "" && (event.NodeID == id || event.NodeID == "node-"+id) {
			return id, true
		}
		for _, symbol := range []string{event.SymbolPath, event.Target} {
			if symbol != "" && (symbol == canonicalStepSymbol(step) || symbol == step.Anchor.EnclosingSymbolPath) {
				return id, true
			}
		}
	}
	return "", false
}

func runtimeEventConflicts(event RuntimeObservationEvent) bool {
	status := strings.ToLower(strings.TrimSpace(firstNonEmpty(event.Outcome, event.Status, event.StaticExpectation)))
	switch status {
	case "conflicting", "conflict", "not_observed", "not-executed", "not_executed", "absent", "skipped", "bypassed", "false":
		return true
	default:
		return false
	}
}

func runtimeEventEvidenceRefs(event RuntimeObservationEvent) []string {
	refs := append([]string(nil), event.EvidenceRefs...)
	if event.EvidenceRef != "" {
		refs = append(refs, event.EvidenceRef)
	}
	return dedupeStrings(refs)
}

func runtimeTimelineEvidenceRefs(event TimelineEvent) []string {
	refs := append([]string(nil), event.EvidenceRefs...)
	if event.EvidenceRef != "" {
		refs = append(refs, event.EvidenceRef)
	}
	return dedupeStrings(refs)
}

func failureTraceID(query FailureQueryV2, startID string) string {
	raw := strings.Join([]string{query.Mode, query.ComputedBasisID, query.GenerationID, query.ValidatedAgainstSnapshotID, query.Freshness, startID}, "\x00")
	h := sha256.Sum256([]byte(raw))
	return "failure-" + hex.EncodeToString(h[:])[:24]
}

func failureDescription(query FailureQueryV2, startID string) string {
	if query.Mode == "debug" {
		return "Evidence-bounded reverse failure path from " + startID
	}
	return "Evidence-bounded incident path for trace " + query.Incident.TraceID
}

func failureLastState(startID string, steps map[string]SemanticStep) string {
	step := steps[startID]
	if step.StateDelta != nil {
		if strings.TrimSpace(step.StateDelta.After) != "" {
			return step.StateDelta.After
		}
		if strings.TrimSpace(step.StateDelta.Before) != "" {
			return step.StateDelta.Before
		}
	}
	return canonicalStepSymbol(step)
}

func failurePathSymbols(ids []string, steps map[string]SemanticStep) []string {
	path := make([]string, 0, len(ids))
	for _, id := range ids {
		if step, ok := steps[id]; ok {
			path = append(path, canonicalStepSymbol(step))
		}
	}
	return path
}

func sameSemanticEvidence(left, right SemanticEvidence) bool {
	return left.EvidenceID == right.EvidenceID && left.ComputedBasisID == right.ComputedBasisID && left.SnapshotID == right.SnapshotID && left.Anchor == right.Anchor && left.ValidationStatus == right.ValidationStatus && left.RedactionStatus == right.RedactionStatus
}

func firstStepID(steps map[string]SemanticStep) string {
	ids := make([]string, 0, len(steps))
	for id := range steps {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	if len(ids) == 0 {
		return ""
	}
	return ids[0]
}

func firstString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func firstNonEmpty(values ...string) string { return firstString(values...) }

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func isCanonicalFailureMap(mapIR *SemanticMapIR) bool {
	return mapIR != nil && mapIR.SchemaID == SemanticMapSchemaID && mapIR.SchemaVersion == SemanticSchemaVersion
}

func isCanonicalRuntimeObservation(observation *RuntimeObservation) bool {
	return observation != nil && observation.SchemaID == RuntimeObservationSchemaID && observation.SchemaVersion == FailureContractSchemaVersion
}
