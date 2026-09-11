package contractharness

import (
	"encoding/json"
	"fmt"
	"math"
	"path"
	"strings"
	"time"
)

const (
	FailureQueryV2SchemaID           = BaseURL + "rflsc.failure-query.v2.schema.json"
	FailurePathTraceV2SchemaID       = BaseURL + "rflsc.failure-path-trace.v2.schema.json"
	RuntimeObservationV2SchemaID     = BaseURL + "rflsc.runtime-observation.v2.schema.json"
	RuntimeConsentV1SchemaID         = BaseURL + "rflsc.runtime-consent.v1.schema.json"
	RuntimeIsolationResultV1SchemaID = BaseURL + "rflsc.runtime-isolation-result.v1.schema.json"
)

// VS06ContractRegistryEntry records the producer, consumer, and fixture
// evidence for one evidence-bounded failure contract. Fixture paths are
// relative to schemas/fixtures.
type VS06ContractRegistryEntry struct {
	ID                      string
	SchemaID                string
	Producer                string
	Consumer                string
	ValidFixture            string
	InvalidScopeFixture     string
	InvalidAuthorityFixture string
	InvalidAnchorFixture    string
}

// VS06EvidenceRegistryID is the stable evidence registry identity from the
// approved VS-06 contract.
const VS06EvidenceRegistryID = "rflsc-r2-vs-06"

// VS06ContractRegistry is ordered to match the required public contract list.
var VS06ContractRegistry = []VS06ContractRegistryEntry{
	{
		ID:                      "rflsc.failure-query.v2",
		SchemaID:                FailureQueryV2SchemaID,
		Producer:                "failure query decoder",
		Consumer:                "semantic failure investigation and MCP/FlowView failure seams",
		ValidFixture:            "rflsc.failure-query.v2/valid/debug.json",
		InvalidScopeFixture:     "rflsc.failure-query.v2/invalid/incident-missing-scope.json",
		InvalidAuthorityFixture: "rflsc.failure-query.v2/invalid/dual-discriminator.json",
		InvalidAnchorFixture:    "rflsc.failure-query.v2/invalid/whitespace-basis.json",
	},
	{
		ID:                      "rflsc.failure-path-trace.v2",
		SchemaID:                FailurePathTraceV2SchemaID,
		Producer:                "semantic evidence-bounded failure path projection",
		Consumer:                "FailurePathTrace task view, MCP and FlowView failure endpoint",
		ValidFixture:            "rflsc.failure-path-trace.v2/valid/debug-trace.json",
		InvalidScopeFixture:     "rflsc.failure-path-trace.v2/invalid/mismatched-counts.json",
		InvalidAuthorityFixture: "rflsc.failure-path-trace.v2/invalid/synthetic-origin.json",
		InvalidAnchorFixture:    "rflsc.failure-path-trace.v2/invalid/unsafe-evidence-anchor.json",
	},
	{
		ID:                      "rflsc.runtime-observation.v2",
		SchemaID:                RuntimeObservationV2SchemaID,
		Producer:                "scoped trace provider",
		Consumer:                "incident failure correlation and runtime observation seam",
		ValidFixture:            "rflsc.runtime-observation.v2/valid/scoped-trace.json",
		InvalidScopeFixture:     "rflsc.runtime-observation.v2/invalid/event-outside-window.json",
		InvalidAuthorityFixture: "rflsc.runtime-observation.v2/invalid/trusted-local-unapproved.json",
		InvalidAnchorFixture:    "rflsc.runtime-observation.v2/invalid/unsafe-anchor.json",
	},
	{
		ID:                      "rflsc.runtime-consent.v1",
		SchemaID:                RuntimeConsentV1SchemaID,
		Producer:                "runtime consent boundary",
		Consumer:                "one-shot runtime executor",
		ValidFixture:            "rflsc.runtime-consent.v1/valid/consent.json",
		InvalidScopeFixture:     "rflsc.runtime-consent.v1/invalid/snapshot-mismatch.json",
		InvalidAuthorityFixture: "rflsc.runtime-consent.v1/invalid/not-approved.json",
		InvalidAnchorFixture:    "rflsc.runtime-consent.v1/invalid/expired.json",
	},
	{
		ID:                      "rflsc.runtime-isolation-result.v1",
		SchemaID:                RuntimeIsolationResultV1SchemaID,
		Producer:                "one-shot runtime executor",
		Consumer:                "runtime evidence promotion gate",
		ValidFixture:            "rflsc.runtime-isolation-result.v1/valid/success.json",
		InvalidScopeFixture:     "rflsc.runtime-isolation-result.v1/invalid/tree-mismatch.json",
		InvalidAuthorityFixture: "rflsc.runtime-isolation-result.v1/invalid/caller-result.json",
		InvalidAnchorFixture:    "rflsc.runtime-isolation-result.v1/invalid/audit-unavailable-promoted.json",
	},
}

// ValidateVS06RegistryEntry validates one valid fixture and each distinct
// invalid scope, authority, and anchor fixture through the strict production
// validator for that boundary.
func ValidateVS06RegistryEntry(entry VS06ContractRegistryEntry, valid, invalidScope, invalidAuthority, invalidAnchor []byte) error {
	if entry.ID == "" || entry.SchemaID == "" || entry.Producer == "" || entry.Consumer == "" || entry.ValidFixture == "" || entry.InvalidScopeFixture == "" || entry.InvalidAuthorityFixture == "" || entry.InvalidAnchorFixture == "" {
		return fmt.Errorf("VS-06 contract registry entry is incomplete: %+v", entry)
	}
	expected := BaseURL + entry.ID + ".schema.json"
	if entry.SchemaID != expected {
		return fmt.Errorf("%s schema identity mismatch: got %q want %q", entry.ID, entry.SchemaID, expected)
	}
	validator := ValidatorForVS06Contract(entry.ID)
	if validator == nil {
		return fmt.Errorf("%s has no VS-06 semantic validator", entry.ID)
	}
	if err := validator(valid); err != nil {
		return fmt.Errorf("%s valid fixture: %w", entry.ID, err)
	}
	for label, data := range map[string][]byte{
		"scope":     invalidScope,
		"authority": invalidAuthority,
		"anchor":    invalidAnchor,
	} {
		if err := validator(data); err == nil {
			return fmt.Errorf("%s invalid %s fixture unexpectedly passed validation", entry.ID, label)
		}
	}
	return nil
}

// ValidatorForVS06Contract exposes the strict validators used by the registry.
func ValidatorForVS06Contract(id string) func([]byte) error {
	switch id {
	case "rflsc.failure-query.v2":
		return ValidateFailureQueryV2
	case "rflsc.failure-path-trace.v2":
		return ValidateFailurePathTraceV2
	case "rflsc.runtime-observation.v2":
		return ValidateRuntimeObservationV2
	case "rflsc.runtime-consent.v1":
		return ValidateRuntimeConsentV1
	case "rflsc.runtime-isolation-result.v1":
		return ValidateRuntimeIsolationResultV1
	default:
		return nil
	}
}

func validatorForVS06Contract(id string) func([]byte) error {
	return ValidatorForVS06Contract(id)
}

func validateVS06Document(schemaID string, data []byte) (map[string]any, error) {
	if err := Validate(schemaID, data); err != nil {
		return nil, err
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse %s: %w", schemaID, err)
	}
	if doc == nil {
		return nil, fmt.Errorf("%s instance must be an object", schemaID)
	}
	return doc, nil
}

func v06String(doc map[string]any, key string) string {
	value, _ := doc[key].(string)
	return value
}

func v06Object(doc map[string]any, key string) map[string]any {
	value, _ := doc[key].(map[string]any)
	return value
}

func v06Array(doc map[string]any, key string) []any {
	value, _ := doc[key].([]any)
	return value
}

func v06NonEmpty(doc map[string]any, key string) bool {
	value := v06String(doc, key)
	return value != "" && strings.TrimSpace(value) == value
}

// ValidateFailureQueryV2 validates the discriminated debug/incident query,
// including exact basis identity and the complete incident runtime scope.
func ValidateFailureQueryV2(data []byte) error {
	doc, err := validateVS06Document(FailureQueryV2SchemaID, data)
	if err != nil {
		return fmt.Errorf("failure-query.v2 schema: %w", err)
	}
	for _, field := range []string{"computedBasisId", "generationId", "validatedAgainstSnapshotId"} {
		if !v06NonEmpty(doc, field) {
			return fmt.Errorf("failure-query.v2 %s must be an exact non-empty identity", field)
		}
	}
	if freshness := v06String(doc, "freshness"); freshness != "current" && freshness != "historical" {
		return fmt.Errorf("failure-query.v2 freshness must be current or historical")
	}

	debug := v06Object(doc, "debug")
	incident := v06Object(doc, "incident")
	switch v06String(doc, "mode") {
	case "debug":
		if debug == nil || incident != nil {
			return fmt.Errorf("failure-query.v2 debug discriminator is inconsistent")
		}
		if !v06NonEmpty(debug, "error") && !v06NonEmpty(debug, "symptom") && !v06NonEmpty(debug, "failureEvidenceId") && len(v06Array(debug, "evidenceRefs")) == 0 {
			return fmt.Errorf("failure-query.v2 debug requires error, symptom, failure Evidence, or evidenceRefs")
		}
		return nil
	case "incident":
		if incident == nil || debug != nil {
			return fmt.Errorf("failure-query.v2 incident discriminator is inconsistent")
		}
		if !v06NonEmpty(incident, "traceId") && !v06NonEmpty(incident, "incidentEvidenceId") && !v06NonEmpty(incident, "runtimeObservationId") {
			return fmt.Errorf("failure-query.v2 incident requires trace, incident Evidence, or runtime observation identity")
		}
		for _, field := range []string{"scenario", "environment", "dependencyFingerprint"} {
			if !v06NonEmpty(incident, field) {
				return fmt.Errorf("failure-query.v2 incident %s must be non-empty", field)
			}
		}
		if _, _, err := validateVS06TimeWindow(v06Object(incident, "timeWindow")); err != nil {
			return fmt.Errorf("failure-query.v2 incident time window: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("failure-query.v2 mode is not debug or incident")
	}
}

// ValidateRuntimeObservationV2 validates the supplied runtime scope and event
// evidence. It never consults the current clock, filesystem, or an executor.
func ValidateRuntimeObservationV2(data []byte) error {
	doc, err := validateVS06Document(RuntimeObservationV2SchemaID, data)
	if err != nil {
		return fmt.Errorf("runtime-observation.v2 schema: %w", err)
	}
	for _, field := range []string{"observationId", "traceId", "scenario", "environment", "dependencyFingerprint", "computedBasisId", "generationId", "validatedAgainstSnapshotId", "observedAt"} {
		if !v06NonEmpty(doc, field) {
			return fmt.Errorf("runtime-observation.v2 %s must be an exact non-empty identity", field)
		}
	}
	if freshness := v06String(doc, "freshness"); freshness != "current" && freshness != "historical" {
		return fmt.Errorf("runtime-observation.v2 freshness must be current or historical")
	}
	if _, err := time.Parse(time.RFC3339Nano, v06String(doc, "observedAt")); err != nil {
		return fmt.Errorf("runtime-observation.v2 observedAt is not RFC3339: %w", err)
	}
	from, to, err := validateVS06TimeWindow(v06Object(doc, "timeWindow"))
	if err != nil {
		return fmt.Errorf("runtime-observation.v2 time window: %w", err)
	}
	if err := validateVS06TraceCoverage(v06Object(doc, "traceCoverage")); err != nil {
		return fmt.Errorf("runtime-observation.v2 trace coverage: %w", err)
	}
	if err := validateVS06TrustedLocalApproval(doc); err != nil {
		return err
	}

	evidence, err := validateVS06RuntimeEvidence(doc, from, to)
	if err != nil {
		return err
	}
	events := v06Array(doc, "events")
	eventIDs := map[string]bool{}
	for index, raw := range events {
		event, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("runtime-observation.v2 event %d is not an object", index)
		}
		eventID := v06String(event, "eventId")
		if eventID == "" || strings.TrimSpace(eventID) != eventID || eventIDs[eventID] {
			return fmt.Errorf("runtime-observation.v2 event %d has duplicate or empty event identity", index)
		}
		eventIDs[eventID] = true
		stamp, parseErr := time.Parse(time.RFC3339Nano, v06String(event, "timestamp"))
		if parseErr != nil || stamp.Before(from) || stamp.After(to) {
			return fmt.Errorf("runtime-observation.v2 event %q is outside the declared time window", eventID)
		}
		refs, refErr := v06EvidenceRefs(event)
		if refErr != nil {
			return fmt.Errorf("runtime-observation.v2 event %q: %w", eventID, refErr)
		}
		if len(refs) == 0 {
			return fmt.Errorf("runtime-observation.v2 event %q has no Evidence reference", eventID)
		}
		if len(evidence) == 0 {
			return fmt.Errorf("runtime-observation.v2 event %q has no supplied Evidence set", eventID)
		}
		for _, ref := range refs {
			if _, ok := evidence[ref]; !ok {
				return fmt.Errorf("runtime-observation.v2 event %q references unknown Evidence %q", eventID, ref)
			}
		}
		if anchor, present := event["anchor"]; present {
			if err := validateVS06Anchor(anchor, true); err != nil {
				return fmt.Errorf("runtime-observation.v2 event %q anchor: %w", eventID, err)
			}
		}
		if v06String(event, "nodeId") == "node-origin" {
			return fmt.Errorf("runtime-observation.v2 event %q uses synthetic origin identity", eventID)
		}
	}
	return nil
}

func validateVS06TraceCoverage(coverage map[string]any) error {
	if coverage == nil {
		return fmt.Errorf("traceCoverage is required")
	}
	covered, coveredOK := v06Int(coverage["spansCovered"])
	total, totalOK := v06Int(coverage["totalSpans"])
	ratio, ratioOK := coverage["ratio"].(float64)
	if !coveredOK || !totalOK || !ratioOK || total < 1 || covered < 0 || covered > total || ratio < 0 || ratio > 1 || math.IsNaN(ratio) || math.IsInf(ratio, 0) {
		return fmt.Errorf("spansCovered, totalSpans and ratio are outside their declared bounds")
	}
	return nil
}

func validateVS06TrustedLocalApproval(doc map[string]any) error {
	if v06String(doc, "isolationLevel") != "trusted_local" {
		return nil
	}
	approval := v06Object(doc, "trustedLocalApproval")
	if approval == nil {
		return fmt.Errorf("runtime-observation.v2 trusted_local requires explicit approval")
	}
	approved, ok := approval["approved"].(bool)
	if !ok || !approved || !v06NonEmpty(approval, "approvedBy") {
		return fmt.Errorf("runtime-observation.v2 trusted_local approval must be explicit and approved by an actor")
	}
	if _, err := time.Parse(time.RFC3339Nano, v06String(approval, "timestamp")); err != nil {
		return fmt.Errorf("runtime-observation.v2 trusted_local approval timestamp is not RFC3339: %w", err)
	}
	return nil
}

func validateVS06RuntimeEvidence(doc map[string]any, from, to time.Time) (map[string]map[string]any, error) {
	items := v06Array(doc, "evidence")
	result := make(map[string]map[string]any, len(items))
	for index, raw := range items {
		item, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("runtime-observation.v2 Evidence %d is not an object", index)
		}
		id := v06String(item, "evidenceId")
		if !v06NonEmpty(item, "evidenceId") || result[id] != nil {
			return nil, fmt.Errorf("runtime-observation.v2 Evidence ids must be unique and exact")
		}
		result[id] = item
		for _, field := range []string{"computedBasisId", "scenario", "environment", "dependencyFingerprint", "timestamp", "validationStatus", "redactionStatus"} {
			if !v06NonEmpty(item, field) {
				return nil, fmt.Errorf("runtime-observation.v2 Evidence %q has incomplete %s", id, field)
			}
		}
		if v06String(item, "computedBasisId") != v06String(doc, "computedBasisId") || v06String(item, "scenario") != v06String(doc, "scenario") || v06String(item, "environment") != v06String(doc, "environment") || v06String(item, "dependencyFingerprint") != v06String(doc, "dependencyFingerprint") {
			return nil, fmt.Errorf("runtime-observation.v2 Evidence %q is outside the observation scope", id)
		}
		for field, expected := range map[string]string{
			"observationId":              v06String(doc, "observationId"),
			"traceId":                    v06String(doc, "traceId"),
			"generationId":               v06String(doc, "generationId"),
			"validatedAgainstSnapshotId": v06String(doc, "validatedAgainstSnapshotId"),
			"snapshotId":                 v06String(doc, "validatedAgainstSnapshotId"),
		} {
			if _, present := item[field]; present && (v06String(item, field) == "" || v06String(item, field) != expected) {
				return nil, fmt.Errorf("runtime-observation.v2 Evidence %q has mismatched %s", id, field)
			}
		}
		if v06String(item, "validationStatus") != "verified" || !validV06RedactionStatus(v06String(item, "redactionStatus")) {
			return nil, fmt.Errorf("runtime-observation.v2 Evidence %q has unsupported validation or redaction status", id)
		}
		stamp, err := time.Parse(time.RFC3339Nano, v06String(item, "timestamp"))
		if err != nil || stamp.Before(from) || stamp.After(to) {
			return nil, fmt.Errorf("runtime-observation.v2 Evidence %q is outside the declared time window", id)
		}
		if anchor, present := item["anchor"]; present {
			if err := validateVS06Anchor(anchor, true); err != nil {
				return nil, fmt.Errorf("runtime-observation.v2 Evidence %q anchor: %w", id, err)
			}
		}
	}
	return result, nil
}

// ValidateFailurePathTraceV2 validates graph identity, connected path
// relationships, evidence provenance, runtime status claims, and exact
// unknown/coverage accounting.
func ValidateFailurePathTraceV2(data []byte) error {
	doc, err := validateVS06Document(FailurePathTraceV2SchemaID, data)
	if err != nil {
		return fmt.Errorf("failure-path-trace.v2 schema: %w", err)
	}
	for _, field := range []string{"traceId", "computedBasisId", "generationId", "validatedAgainstSnapshotId"} {
		if !v06NonEmpty(doc, field) {
			return fmt.Errorf("failure-path-trace.v2 %s must be an exact non-empty identity", field)
		}
	}
	if freshness := v06String(doc, "freshness"); freshness != "current" && freshness != "historical" {
		return fmt.Errorf("failure-path-trace.v2 freshness must be current or historical")
	}
	mode := v06String(doc, "mode")
	if mode != "debug" && mode != "incident" {
		return fmt.Errorf("failure-path-trace.v2 mode is not debug or incident")
	}

	staticEvidence, runtimeEvidence, allEvidence, err := validateVS06TraceEvidence(doc)
	if err != nil {
		return err
	}
	if err := validateVS06FailureTarget(doc, mode, allEvidence); err != nil {
		return err
	}
	nodes, err := validateVS06FailureNodes(doc, staticEvidence, runtimeEvidence, allEvidence)
	if err != nil {
		return err
	}
	relationships, err := validateVS06FailureRelationships(doc, nodes, staticEvidence, allEvidence)
	if err != nil {
		return err
	}
	if err := validateVS06ConnectedPath(nodes, relationships); err != nil {
		return err
	}
	if err := validateVS06Timeline(doc, mode, runtimeEvidence, allEvidence); err != nil {
		return err
	}
	if err := validateVS06RecoveryStates(doc, runtimeEvidence, staticEvidence, allEvidence); err != nil {
		return err
	}
	frontierCount, err := validateVS06Frontiers(doc, nodes, allEvidence)
	if err != nil {
		return err
	}
	if err := validateVS06ConflictFlag(doc, allEvidence); err != nil {
		return err
	}
	return validateVS06Coverage(doc, len(nodes), len(relationships), frontierCount)
}

func validateVS06TraceEvidence(doc map[string]any) (map[string]map[string]any, map[string]map[string]any, map[string]string, error) {
	staticItems := v06Array(doc, "staticEvidence")
	runtimeItems := v06Array(doc, "runtimeEvidence")
	if len(staticItems) == 0 {
		return nil, nil, nil, fmt.Errorf("failure-path-trace.v2 requires anchored static Evidence")
	}
	static := make(map[string]map[string]any, len(staticItems))
	runtime := make(map[string]map[string]any, len(runtimeItems))
	kinds := make(map[string]string, len(staticItems)+len(runtimeItems))
	for index, raw := range staticItems {
		item, ok := raw.(map[string]any)
		if !ok {
			return nil, nil, nil, fmt.Errorf("failure-path-trace.v2 static Evidence %d is not an object", index)
		}
		id := v06String(item, "evidenceId")
		if !v06NonEmpty(item, "evidenceId") || kinds[id] != "" {
			return nil, nil, nil, fmt.Errorf("failure-path-trace.v2 Evidence ids must be unique and exact")
		}
		if err := validateVS06StaticEvidence(item, doc); err != nil {
			return nil, nil, nil, err
		}
		static[id] = item
		kinds[id] = "static"
	}
	for index, raw := range runtimeItems {
		item, ok := raw.(map[string]any)
		if !ok {
			return nil, nil, nil, fmt.Errorf("failure-path-trace.v2 runtime Evidence %d is not an object", index)
		}
		id := v06String(item, "evidenceId")
		if !v06NonEmpty(item, "evidenceId") || kinds[id] != "" {
			return nil, nil, nil, fmt.Errorf("failure-path-trace.v2 Evidence ids must be unique and exact")
		}
		if err := validateVS06RuntimeTraceEvidence(item, doc); err != nil {
			return nil, nil, nil, err
		}
		runtime[id] = item
		kinds[id] = "runtime"
	}
	return static, runtime, kinds, nil
}

func validateVS06StaticEvidence(item, trace map[string]any) error {
	for _, field := range []string{"evidenceId", "kind", "sourceAuthority", "computedBasisId", "snapshotId", "validationStatus", "redactionStatus"} {
		if !v06NonEmpty(item, field) {
			return fmt.Errorf("failure-path-trace.v2 static Evidence %q has incomplete %s", v06String(item, "evidenceId"), field)
		}
	}
	if v06String(item, "computedBasisId") != v06String(trace, "computedBasisId") || v06String(item, "snapshotId") != v06String(trace, "validatedAgainstSnapshotId") {
		return fmt.Errorf("failure-path-trace.v2 static Evidence %q is outside the trace basis or snapshot", v06String(item, "evidenceId"))
	}
	if v06String(item, "validationStatus") != "verified" || !validV06RedactionStatus(v06String(item, "redactionStatus")) {
		return fmt.Errorf("failure-path-trace.v2 static Evidence %q has unsupported validation or redaction status", v06String(item, "evidenceId"))
	}
	if err := validateVS06Anchor(item["anchor"], true); err != nil {
		return fmt.Errorf("failure-path-trace.v2 static Evidence %q anchor: %w", v06String(item, "evidenceId"), err)
	}
	for _, field := range []string{"byteRange", "lineRange"} {
		if raw, present := item[field]; present && !validVS06Range(raw, field == "lineRange") {
			return fmt.Errorf("failure-path-trace.v2 static Evidence %q has invalid %s", v06String(item, "evidenceId"), field)
		}
	}
	if authority := strings.ToLower(strings.TrimSpace(v06String(item, "sourceAuthority"))); authority == "agent" || authority == "model" || authority == "assistant" || authority == "synthetic" || authority == "unknown" {
		return fmt.Errorf("failure-path-trace.v2 static Evidence %q has non-authoritative source", v06String(item, "evidenceId"))
	}
	return nil
}

func validateVS06RuntimeTraceEvidence(item, trace map[string]any) error {
	for _, field := range []string{"evidenceId", "computedBasisId", "scenario", "environment", "dependencyFingerprint", "timestamp", "validationStatus", "redactionStatus"} {
		if !v06NonEmpty(item, field) {
			return fmt.Errorf("failure-path-trace.v2 runtime Evidence %q has incomplete %s", v06String(item, "evidenceId"), field)
		}
	}
	if v06String(item, "computedBasisId") != v06String(trace, "computedBasisId") || v06String(item, "validationStatus") != "verified" || !validV06RedactionStatus(v06String(item, "redactionStatus")) {
		return fmt.Errorf("failure-path-trace.v2 runtime Evidence %q has mismatched basis, validation, or redaction status", v06String(item, "evidenceId"))
	}
	if generation := v06String(item, "generationId"); generation != "" && generation != v06String(trace, "generationId") {
		return fmt.Errorf("failure-path-trace.v2 runtime Evidence %q has mismatched generation", v06String(item, "evidenceId"))
	}
	if snapshot := v06String(item, "snapshotId"); snapshot != "" && snapshot != v06String(trace, "validatedAgainstSnapshotId") {
		return fmt.Errorf("failure-path-trace.v2 runtime Evidence %q has mismatched snapshot", v06String(item, "evidenceId"))
	}
	if validated := v06String(item, "validatedAgainstSnapshotId"); validated != "" && validated != v06String(trace, "validatedAgainstSnapshotId") {
		return fmt.Errorf("failure-path-trace.v2 runtime Evidence %q has mismatched validated snapshot", v06String(item, "evidenceId"))
	}
	if _, err := time.Parse(time.RFC3339Nano, v06String(item, "timestamp")); err != nil {
		return fmt.Errorf("failure-path-trace.v2 runtime Evidence %q timestamp is not RFC3339", v06String(item, "evidenceId"))
	}
	if anchor, present := item["anchor"]; present {
		if err := validateVS06Anchor(anchor, true); err != nil {
			return fmt.Errorf("failure-path-trace.v2 runtime Evidence %q anchor: %w", v06String(item, "evidenceId"), err)
		}
	}
	return nil
}

func validateVS06FailureTarget(doc map[string]any, mode string, evidenceKinds map[string]string) error {
	target := v06Object(doc, "failureTarget")
	if target == nil {
		return fmt.Errorf("failure-path-trace.v2 failureTarget is required")
	}
	debugPresent := v06NonEmpty(target, "error") || v06NonEmpty(target, "symptom") || v06NonEmpty(target, "failureEvidenceId")
	incidentPresent := v06NonEmpty(target, "incidentTraceId") || v06NonEmpty(target, "incidentEvidenceId")
	if mode == "debug" {
		if !debugPresent || incidentPresent {
			return fmt.Errorf("failure-path-trace.v2 debug target is not discriminated")
		}
		if id := v06String(target, "failureEvidenceId"); id != "" && evidenceKinds[id] != "static" {
			return fmt.Errorf("failure-path-trace.v2 debug target references non-static or unknown Evidence %q", id)
		}
		return nil
	}
	if !incidentPresent || debugPresent {
		return fmt.Errorf("failure-path-trace.v2 incident target is not discriminated")
	}
	return nil
}

func validateVS06FailureNodes(doc map[string]any, static, runtime map[string]map[string]any, evidenceKinds map[string]string) (map[string]map[string]any, error) {
	nodes := make(map[string]map[string]any)
	for index, raw := range v06Array(doc, "nodes") {
		node, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("failure-path-trace.v2 node %d is not an object", index)
		}
		id, symbol, status := v06String(node, "nodeId"), v06String(node, "symbolPath"), v06String(node, "status")
		if !v06NonEmpty(node, "nodeId") || !v06NonEmpty(node, "symbolPath") || id == "node-origin" || strings.HasPrefix(id, "node-origin/") || symbol == "ErrorOrigin" || nodes[id] != nil {
			return nil, fmt.Errorf("failure-path-trace.v2 contains an invalid, duplicate, or synthetic node %q", id)
		}
		refs, err := v06EvidenceRefs(node)
		if err != nil {
			return nil, fmt.Errorf("failure-path-trace.v2 node %q: %w", id, err)
		}
		if status != "unknown" && len(refs) == 0 {
			return nil, fmt.Errorf("failure-path-trace.v2 node %q has no Evidence refs for status %q", id, status)
		}
		staticRefs, runtimeRefs := 0, 0
		for _, ref := range refs {
			switch evidenceKinds[ref] {
			case "static":
				staticRefs++
			case "runtime":
				runtimeRefs++
			default:
				return nil, fmt.Errorf("failure-path-trace.v2 node %q references unknown Evidence %q", id, ref)
			}
		}
		switch status {
		case "static_candidate":
			if staticRefs == 0 || runtimeRefs != 0 {
				return nil, fmt.Errorf("failure-path-trace.v2 static_candidate node %q requires static-only Evidence", id)
			}
		case "runtime_observed":
			if runtimeRefs == 0 {
				return nil, fmt.Errorf("failure-path-trace.v2 runtime_observed node %q requires runtime Evidence", id)
			}
		case "corroborated", "conflicting":
			if staticRefs == 0 || runtimeRefs == 0 {
				return nil, fmt.Errorf("failure-path-trace.v2 %s node %q requires both static and runtime Evidence", status, id)
			}
		}
		if staticRefs > 0 {
			matchedAnchor := false
			for _, ref := range refs {
				item := static[ref]
				if item == nil {
					continue
				}
				anchor := v06Object(item, "anchor")
				if v06String(anchor, "enclosingSymbolPath") == symbol {
					matchedAnchor = true
					break
				}
			}
			if !matchedAnchor {
				return nil, fmt.Errorf("failure-path-trace.v2 node %q symbolPath is not bound to its static Evidence anchor", id)
			}
		}
		nodes[id] = node
	}
	return nodes, nil
}

func validateVS06FailureRelationships(doc map[string]any, nodes, static map[string]map[string]any, evidenceKinds map[string]string) ([]map[string]any, error) {
	relations := v06Array(doc, "relationships")
	seen := map[string]bool{}
	for index, raw := range relations {
		relation, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("failure-path-trace.v2 relationship %d is not an object", index)
		}
		from, to := v06String(relation, "fromNodeId"), v06String(relation, "toNodeId")
		if from == "" || to == "" || from == to || nodes[from] == nil || nodes[to] == nil {
			return nil, fmt.Errorf("failure-path-trace.v2 relationship %s->%s has a dangling endpoint or self-loop", from, to)
		}
		refs, err := v06EvidenceRefs(relation)
		if err != nil {
			return nil, fmt.Errorf("failure-path-trace.v2 relationship %s->%s: %w", from, to, err)
		}
		if len(refs) == 0 {
			return nil, fmt.Errorf("failure-path-trace.v2 relationship %s->%s has no confirmed-hop Evidence", from, to)
		}
		key := from + "\x00" + to + "\x00" + v06String(relation, "kind")
		if seen[key] {
			return nil, fmt.Errorf("failure-path-trace.v2 contains duplicate relationship %s->%s", from, to)
		}
		seen[key] = true
		staticRefs := 0
		for _, ref := range refs {
			switch evidenceKinds[ref] {
			case "static":
				staticRefs++
			case "runtime":
			default:
				return nil, fmt.Errorf("failure-path-trace.v2 relationship %s->%s references unknown Evidence %q", from, to, ref)
			}
		}
		if staticRefs == 0 {
			return nil, fmt.Errorf("failure-path-trace.v2 relationship %s->%s is not confirmed by static Evidence", from, to)
		}
	}
	return relationsAsObjects(relations)
}

func relationsAsObjects(relations []any) ([]map[string]any, error) {
	result := make([]map[string]any, 0, len(relations))
	for _, raw := range relations {
		relation, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("failure-path-trace.v2 relationship is not an object")
		}
		result = append(result, relation)
	}
	return result, nil
}

func validateVS06ConnectedPath(nodes map[string]map[string]any, relations []map[string]any) error {
	if len(nodes) < 2 {
		return nil
	}
	parent := make(map[string]string, len(nodes))
	for id := range nodes {
		parent[id] = id
	}
	var find func(string) string
	find = func(id string) string {
		root := id
		for parent[root] != root {
			root = parent[root]
		}
		for parent[id] != id {
			next := parent[id]
			parent[id] = root
			id = next
		}
		return root
	}
	union := func(left, right string) {
		leftRoot, rightRoot := find(left), find(right)
		if leftRoot != rightRoot {
			parent[rightRoot] = leftRoot
		}
	}
	for _, relation := range relations {
		union(v06String(relation, "fromNodeId"), v06String(relation, "toNodeId"))
	}
	first := ""
	for id := range nodes {
		first = id
		break
	}
	root := find(first)
	for id := range nodes {
		if find(id) != root {
			return fmt.Errorf("failure-path-trace.v2 confirmed nodes are disconnected")
		}
	}
	return nil
}

func validateVS06Timeline(doc map[string]any, mode string, runtime map[string]map[string]any, all map[string]string) error {
	timeline := v06Array(doc, "timeline")
	if mode == "debug" && (len(timeline) > 0 || len(v06Array(doc, "runtimeEvidence")) > 0 || v06String(doc, "runtimeObservationRef") != "") {
		return fmt.Errorf("failure-path-trace.v2 debug output cannot claim runtime observation")
	}
	if len(timeline) == 0 && len(v06Array(doc, "runtimeEvidence")) == 0 {
		return nil
	}
	if v06String(doc, "runtimeObservationRef") == "" {
		return fmt.Errorf("failure-path-trace.v2 runtime claims require runtimeObservationRef")
	}
	seenEvents := map[string]bool{}
	for index, raw := range timeline {
		event, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("failure-path-trace.v2 timeline event %d is not an object", index)
		}
		if eventID := v06String(event, "eventId"); eventID != "" {
			if seenEvents[eventID] {
				return fmt.Errorf("failure-path-trace.v2 timeline event ids must be unique")
			}
			seenEvents[eventID] = true
		}
		if _, err := time.Parse(time.RFC3339Nano, v06String(event, "timestamp")); err != nil {
			return fmt.Errorf("failure-path-trace.v2 timeline timestamp is not RFC3339")
		}
		refs, err := v06EvidenceRefs(event)
		if err != nil {
			return fmt.Errorf("failure-path-trace.v2 timeline event: %w", err)
		}
		if len(refs) == 0 {
			return fmt.Errorf("failure-path-trace.v2 timeline event has no Evidence")
		}
		for _, ref := range refs {
			if all[ref] != "runtime" || runtime[ref] == nil {
				return fmt.Errorf("failure-path-trace.v2 timeline event references non-runtime or unknown Evidence %q", ref)
			}
			item := runtime[ref]
			for field := range map[string]bool{"scenario": true, "environment": true, "dependencyFingerprint": true} {
				if _, present := event[field]; present && (v06String(event, field) == "" || v06String(event, field) != v06String(item, field)) {
					return fmt.Errorf("failure-path-trace.v2 timeline event has mismatched %s", field)
				}
			}
			if observationID := v06String(item, "observationId"); observationID != "" && observationID != v06String(doc, "runtimeObservationRef") {
				return fmt.Errorf("failure-path-trace.v2 runtime Evidence is bound to another observation")
			}
		}
		if anchor, present := event["anchor"]; present {
			if err := validateVS06Anchor(anchor, true); err != nil {
				return fmt.Errorf("failure-path-trace.v2 timeline anchor: %w", err)
			}
		}
	}
	return nil
}

func validateVS06RecoveryStates(doc map[string]any, runtime, static map[string]map[string]any, all map[string]string) error {
	seen := map[string]bool{}
	for _, raw := range v06Array(doc, "recoveryStates") {
		state, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("failure-path-trace.v2 recovery state is not an object")
		}
		kind := v06String(state, "kind")
		if seen[kind] {
			return fmt.Errorf("failure-path-trace.v2 recovery state %q is duplicated", kind)
		}
		seen[kind] = true
		refs, err := v06EvidenceRefs(state)
		if err != nil {
			return fmt.Errorf("failure-path-trace.v2 recovery state %q: %w", kind, err)
		}
		switch v06String(state, "status") {
		case "runtime_observed":
			if len(refs) == 0 {
				return fmt.Errorf("failure-path-trace.v2 observed recovery state %q has no Evidence", kind)
			}
			for _, ref := range refs {
				if all[ref] != "runtime" || runtime[ref] == nil {
					return fmt.Errorf("failure-path-trace.v2 observed recovery state %q requires runtime Evidence", kind)
				}
			}
		case "conflicting":
			staticRefs, runtimeRefs := 0, 0
			for _, ref := range refs {
				switch all[ref] {
				case "static":
					staticRefs++
				case "runtime":
					runtimeRefs++
				default:
					return fmt.Errorf("failure-path-trace.v2 conflicting recovery state %q references unknown Evidence", kind)
				}
			}
			if staticRefs == 0 || runtimeRefs == 0 {
				return fmt.Errorf("failure-path-trace.v2 conflicting recovery state %q requires static and runtime Evidence", kind)
			}
		case "possible_unknown":
			if len(refs) != 0 {
				return fmt.Errorf("failure-path-trace.v2 possible_unknown recovery state %q cannot claim Evidence", kind)
			}
		}
	}
	return nil
}

func validateVS06Frontiers(doc map[string]any, nodes map[string]map[string]any, all map[string]string) (int, error) {
	seen := map[string]bool{}
	frontiers := v06Array(doc, "unknownFrontier")
	for _, raw := range frontiers {
		frontier, ok := raw.(map[string]any)
		if !ok {
			return 0, fmt.Errorf("failure-path-trace.v2 unknown frontier is not an object")
		}
		id := v06String(frontier, "frontierId")
		if !v06NonEmpty(frontier, "frontierId") || seen[id] || !v06NonEmpty(frontier, "lastConfirmedNodeId") || nodes[v06String(frontier, "lastConfirmedNodeId")] == nil || !v06NonEmpty(frontier, "target") || !v06NonEmpty(frontier, "reason") {
			return 0, fmt.Errorf("failure-path-trace.v2 unknown frontier is incomplete or not tied to a confirmed node")
		}
		seen[id] = true
		refs, err := v06EvidenceRefs(frontier)
		if err != nil {
			return 0, fmt.Errorf("failure-path-trace.v2 unknown frontier %q: %w", id, err)
		}
		for _, ref := range refs {
			if all[ref] == "" {
				return 0, fmt.Errorf("failure-path-trace.v2 unknown frontier %q references unknown Evidence %q", id, ref)
			}
		}
	}
	return len(frontiers), nil
}

func validateVS06ConflictFlag(doc map[string]any, all map[string]string) error {
	foundConflict := false
	for _, raw := range v06Array(doc, "nodes") {
		node, _ := raw.(map[string]any)
		if v06String(node, "status") == "conflicting" {
			foundConflict = true
		}
	}
	for _, raw := range v06Array(doc, "recoveryStates") {
		state, _ := raw.(map[string]any)
		if v06String(state, "status") == "conflicting" {
			foundConflict = true
		}
	}
	got, ok := doc["hasConflicts"].(bool)
	if !ok || got != foundConflict {
		return fmt.Errorf("failure-path-trace.v2 hasConflicts does not match conflicting Evidence state")
	}
	_ = all
	return nil
}

func validateVS06Coverage(doc map[string]any, nodeCount, edgeCount, frontierCount int) error {
	coverage := v06Object(doc, "coverage")
	if coverage == nil {
		return fmt.Errorf("failure-path-trace.v2 coverage boundary is required")
	}
	confirmedNodes, nodesOK := v06Int(coverage["confirmedNodeCount"])
	confirmedEdges, edgesOK := v06Int(coverage["confirmedEdgeCount"])
	unresolvedEdges, unresolvedOK := v06Int(coverage["unresolvedEdgeCount"])
	if !nodesOK || !edgesOK || !unresolvedOK || confirmedNodes != int64(nodeCount) || confirmedEdges != int64(edgeCount) || unresolvedEdges != int64(frontierCount) {
		return fmt.Errorf("failure-path-trace.v2 coverage counts do not match confirmed graph and unknown frontier")
	}
	unknownNodes := 0
	for _, raw := range v06Array(doc, "nodes") {
		node, _ := raw.(map[string]any)
		if v06String(node, "status") == "unknown" {
			unknownNodes++
		}
	}
	possibleRecovery := 0
	for _, raw := range v06Array(doc, "recoveryStates") {
		state, _ := raw.(map[string]any)
		if v06String(state, "status") == "possible_unknown" {
			possibleRecovery++
		}
	}
	unknownCount, unknownOK := v06Int(doc["unknownCount"])
	if !unknownOK || unknownCount != int64(frontierCount+unknownNodes+possibleRecovery) {
		return fmt.Errorf("failure-path-trace.v2 unknownCount does not match unknown frontier, nodes, and recovery states")
	}
	complete, completeOK := coverage["complete"].(bool)
	wantComplete := unknownCount == 0
	if !completeOK || complete != wantComplete {
		return fmt.Errorf("failure-path-trace.v2 coverage complete flag does not match unknownCount")
	}
	if !wantComplete && len(v06Array(coverage, "excludedReasons")) == 0 {
		return fmt.Errorf("failure-path-trace.v2 incomplete coverage requires excludedReasons")
	}
	return nil
}

func v06EvidenceRefs(doc map[string]any) ([]string, error) {
	refs := make([]string, 0)
	seen := map[string]bool{}
	if raw, present := doc["evidenceRef"]; present {
		ref, ok := raw.(string)
		if !ok || strings.TrimSpace(ref) == "" || seen[ref] {
			return nil, fmt.Errorf("evidenceRef is empty, invalid, or duplicated")
		}
		seen[ref] = true
		refs = append(refs, ref)
	}
	if raw, present := doc["evidenceRefs"]; present {
		items, ok := raw.([]any)
		if !ok {
			return nil, fmt.Errorf("evidenceRefs is not an array")
		}
		for _, rawRef := range items {
			ref, ok := rawRef.(string)
			if !ok || strings.TrimSpace(ref) == "" || seen[ref] {
				return nil, fmt.Errorf("evidenceRefs contains an empty, invalid, or duplicated reference")
			}
			seen[ref] = true
			refs = append(refs, ref)
		}
	}
	return refs, nil
}

func validateVS06TimeWindow(window map[string]any) (time.Time, time.Time, error) {
	if window == nil {
		return time.Time{}, time.Time{}, fmt.Errorf("timeWindow is required")
	}
	fromText, toText := v06String(window, "from"), v06String(window, "to")
	if fromText == "" || toText == "" || strings.TrimSpace(fromText) != fromText || strings.TrimSpace(toText) != toText {
		return time.Time{}, time.Time{}, fmt.Errorf("from and to must be exact non-empty timestamps")
	}
	from, err := time.Parse(time.RFC3339Nano, fromText)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("from is not RFC3339: %w", err)
	}
	to, err := time.Parse(time.RFC3339Nano, toText)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("to is not RFC3339: %w", err)
	}
	if to.Before(from) {
		return time.Time{}, time.Time{}, fmt.Errorf("to is before from")
	}
	return from, to, nil
}

func validateVS06Anchor(raw any, required bool) error {
	if raw == nil {
		if required {
			return fmt.Errorf("anchor is required")
		}
		return nil
	}
	anchor, ok := raw.(map[string]any)
	if !ok {
		return fmt.Errorf("anchor is not an object")
	}
	relativePath := v06String(anchor, "repoRelativePath")
	if relativePath == "" || strings.TrimSpace(relativePath) != relativePath || strings.HasPrefix(relativePath, "/") || strings.Contains(relativePath, "\\") || strings.Contains(relativePath, "\x00") || relativePath == "." || path.Clean(relativePath) != relativePath || strings.HasPrefix(relativePath, "../") || strings.Contains(relativePath, "/../") {
		return fmt.Errorf("repoRelativePath is not a safe repo-relative path")
	}
	for _, field := range []string{"fileHash", "spanHash", "enclosingSymbolPath"} {
		value := v06String(anchor, field)
		if value == "" || strings.TrimSpace(value) != value {
			return fmt.Errorf("anchor %s is required", field)
		}
	}
	if !validVS06Range(anchor["byteRange"], false) {
		return fmt.Errorf("byteRange must be a positive half-open range")
	}
	if rawRange, present := anchor["symbolRange"]; present && !validVS06Range(rawRange, false) {
		return fmt.Errorf("symbolRange must be a positive half-open range")
	}
	return nil
}

func validVS06Range(raw any, oneBased bool) bool {
	items, ok := raw.([]any)
	if !ok || len(items) != 2 {
		return false
	}
	start, startOK := v06Int(items[0])
	end, endOK := v06Int(items[1])
	minimum := int64(0)
	if oneBased {
		minimum = 1
	}
	return startOK && endOK && start >= minimum && end > start
}

func v06Int(raw any) (int64, bool) {
	value, ok := raw.(float64)
	if !ok || math.IsNaN(value) || math.IsInf(value, 0) || math.Trunc(value) != value {
		return 0, false
	}
	return int64(value), true
}

func validV06RedactionStatus(status string) bool {
	return status == "clean" || status == "redacted" || status == "passed"
}
