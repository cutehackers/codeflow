package contractharness

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

// VS03ContractRegistryEntry describes one of the eight durable/currentness
// contracts. The two invalid fixtures are deliberately distinct: one breaks
// identity binding and one breaks a state transition or gate invariant.
type VS03ContractRegistryEntry struct {
	ID                     string
	SchemaID               string
	Producer               string
	Consumer               string
	ValidFixture           string
	InvalidIdentityFixture string
	InvalidStateFixture    string
	// AdditionalInvalidFixtures cover independent required-field and digest
	// mutations that must remain rejected even when the cross-identity/state
	// fixtures are otherwise well-formed.
	AdditionalInvalidFixtures []string
}

// VS03EvidenceRegistryID is the stable evidence registry identity from the
// approved VS-03 contract.
const VS03EvidenceRegistryID = "rflsc-r2-vs-03"

// VS03ContractRegistry is ordered to match the contract's required registry
// list. Fixture paths are relative to schemas/fixtures.
var VS03ContractRegistry = []VS03ContractRegistryEntry{
	{
		ID: "rflsc.activity-state.v2", SchemaID: BaseURL + "rflsc.activity-state.v2.schema.json",
		Producer: "workspace.SnapshotEngine activity ledger", Consumer: "automatic scheduler and FlowView activity rail",
		ValidFixture: "rflsc.activity-state.v2/valid/activity.json", InvalidIdentityFixture: "rflsc.activity-state.v2/invalid/identity.json", InvalidStateFixture: "rflsc.activity-state.v2/invalid/state.json",
	},
	{
		ID: "rflsc.publication-candidate.v2", SchemaID: BaseURL + "rflsc.publication-candidate.v2.schema.json",
		Producer: "semantic compiler publication gate", Consumer: "settlement and durable publication transaction",
		ValidFixture: "rflsc.publication-candidate.v2/valid/candidate.json", InvalidIdentityFixture: "rflsc.publication-candidate.v2/invalid/identity.json", InvalidStateFixture: "rflsc.publication-candidate.v2/invalid/state.json",
	},
	{
		ID: "rflsc.generation-proof-manifest.v2", SchemaID: BaseURL + "rflsc.generation-proof-manifest.v2.schema.json",
		Producer: "storage.PublishGeneration proof builder", Consumer: "active pointer, current/gap query and FlowView",
		ValidFixture: "rflsc.generation-proof-manifest.v2/valid/manifest.json", InvalidIdentityFixture: "rflsc.generation-proof-manifest.v2/invalid/identity.json", InvalidStateFixture: "rflsc.generation-proof-manifest.v2/invalid/state.json",
		AdditionalInvalidFixtures: []string{
			"rflsc.generation-proof-manifest.v2/invalid/missing-capability-profile-digest.json",
			"rflsc.generation-proof-manifest.v2/invalid/invalid-capability-profile-digest.json",
			"rflsc.generation-proof-manifest.v2/invalid/missing-semantic-map-ref.json",
			"rflsc.generation-proof-manifest.v2/invalid/invalid-semantic-map-ref.json",
			"rflsc.generation-proof-manifest.v2/invalid/missing-projection-ref.json",
			"rflsc.generation-proof-manifest.v2/invalid/invalid-projection-ref.json",
			"rflsc.generation-proof-manifest.v2/invalid/missing-analysis-read-set-ref.json",
			"rflsc.generation-proof-manifest.v2/invalid/invalid-analysis-read-set-ref.json",
			"rflsc.generation-proof-manifest.v2/invalid/missing-observation-closure-ref.json",
			"rflsc.generation-proof-manifest.v2/invalid/invalid-observation-closure-ref.json",
			"rflsc.generation-proof-manifest.v2/invalid/missing-analyzer-result-ref.json",
			"rflsc.generation-proof-manifest.v2/invalid/invalid-analyzer-result-ref.json",
		},
	},
	{
		ID: "rflsc.active-pointer.v2", SchemaID: BaseURL + "rflsc.active-pointer.v2.schema.json",
		Producer: "storage.PublishGeneration CAS pointer", Consumer: "current generation reader and reconnect full sync",
		ValidFixture: "rflsc.active-pointer.v2/valid/pointer.json", InvalidIdentityFixture: "rflsc.active-pointer.v2/invalid/identity.json", InvalidStateFixture: "rflsc.active-pointer.v2/invalid/state.json",
	},
	{
		ID: "rflsc.verified-gap.v2", SchemaID: BaseURL + "rflsc.verified-gap.v2.schema.json",
		Producer: "semantic.PublicationGate verified-gap result", Consumer: "gap query, activity rail and FlowView status",
		ValidFixture: "rflsc.verified-gap.v2/valid/gap.json", InvalidIdentityFixture: "rflsc.verified-gap.v2/invalid/identity.json", InvalidStateFixture: "rflsc.verified-gap.v2/invalid/state.json",
	},
	{
		ID: "rflsc.settlement.v2", SchemaID: BaseURL + "rflsc.settlement.v2.schema.json",
		Producer: "semantic.PublicationGate settlement evaluator", Consumer: "proof manifest and Q3/Q4 publication",
		ValidFixture: "rflsc.settlement.v2/valid/settlement.json", InvalidIdentityFixture: "rflsc.settlement.v2/invalid/identity.json", InvalidStateFixture: "rflsc.settlement.v2/invalid/state.json",
	},
	{
		ID: "rflsc.event-envelope.v2", SchemaID: BaseURL + "rflsc.event-envelope.v2.schema.json",
		Producer: "flowview.EventHub durable event ledger", Consumer: "SSE replay/full-sync subscriber",
		ValidFixture: "rflsc.event-envelope.v2/valid/event.json", InvalidIdentityFixture: "rflsc.event-envelope.v2/invalid/identity.json", InvalidStateFixture: "rflsc.event-envelope.v2/invalid/state.json",
	},
	{
		ID: "rflsc.flowview-view-state.v2", SchemaID: BaseURL + "rflsc.flowview-view-state.v2.schema.json",
		Producer: "FlowView view-state projection", Consumer: "browser selection/status renderer",
		ValidFixture: "rflsc.flowview-view-state.v2/valid/state.json", InvalidIdentityFixture: "rflsc.flowview-view-state.v2/invalid/identity.json", InvalidStateFixture: "rflsc.flowview-view-state.v2/invalid/state.json",
	},
}

// ValidateVS03RegistryEntry validates the schema and the semantic invariants
// for all three fixture classes. This is intentionally stronger than calling
// Validate alone because JSON Schema cannot express cross-artifact identity or
// gate/state transitions.
func ValidateVS03RegistryEntry(entry VS03ContractRegistryEntry, valid, invalidIdentity, invalidState []byte) error {
	if entry.ID == "" || entry.SchemaID == "" || entry.Producer == "" || entry.Consumer == "" || entry.ValidFixture == "" || entry.InvalidIdentityFixture == "" || entry.InvalidStateFixture == "" {
		return fmt.Errorf("VS-03 contract registry entry is incomplete: %+v", entry)
	}
	expected := BaseURL + entry.ID + ".schema.json"
	if entry.SchemaID != expected {
		return fmt.Errorf("%s schema identity mismatch: got %q want %q", entry.ID, entry.SchemaID, expected)
	}
	validator := publicationContractValidatorFor(entry.ID)
	if validator == nil {
		return fmt.Errorf("%s has no VS-03 semantic validator", entry.ID)
	}
	if err := validator(valid); err != nil {
		return fmt.Errorf("%s valid fixture: %w", entry.ID, err)
	}
	if err := validator(invalidIdentity); err == nil {
		return fmt.Errorf("%s cross-identity fixture unexpectedly passed validation", entry.ID)
	}
	if err := validator(invalidState); err == nil {
		return fmt.Errorf("%s invalid-state fixture unexpectedly passed validation", entry.ID)
	}
	return nil
}

type publicationContractValidator func([]byte) error

func publicationContractValidatorFor(id string) publicationContractValidator {
	switch id {
	case "rflsc.activity-state.v2":
		return ValidateActivityStateV2
	case "rflsc.publication-candidate.v2":
		return ValidatePublicationCandidateV2
	case "rflsc.generation-proof-manifest.v2":
		return ValidateGenerationProofManifestV2
	case "rflsc.active-pointer.v2":
		return ValidateActivePointerV2
	case "rflsc.verified-gap.v2":
		return ValidateVerifiedGapV2
	case "rflsc.settlement.v2":
		return ValidateSettlementV2
	case "rflsc.event-envelope.v2":
		return ValidateEventEnvelopeV2
	case "rflsc.flowview-view-state.v2":
		return ValidateFlowViewViewStateV2
	default:
		return nil
	}
}

// ValidatorForVS03Contract exposes the same strict validator used by the
// registry so additional fixture variants cannot bypass semantic checks.
func ValidatorForVS03Contract(id string) func([]byte) error {
	return publicationContractValidatorFor(id)
}

func parseDocument(schemaID string, data []byte) (map[string]any, error) {
	if err := Validate(schemaID, data); err != nil {
		return nil, err
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse %s: %w", schemaID, err)
	}
	return doc, nil
}

func extractPublicationString(doc map[string]any, key string) string {
	v, _ := doc[key].(string)
	return v
}

func extractPublicationNumber(doc map[string]any, key string) (float64, bool) {
	v, ok := doc[key].(float64)
	return v, ok
}

func extractPublicationObject(doc map[string]any, key string) map[string]any {
	v, _ := doc[key].(map[string]any)
	return v
}

func extractPublicationArray(doc map[string]any, key string) []any {
	v, _ := doc[key].([]any)
	return v
}

func allVS03GatesPassed(gates map[string]any) bool {
	for _, key := range []string{"snapshotGate", "closureGate", "evidenceGate", "semanticAtomicityGate", "taskRelevanceGate", "comprehensionGate"} {
		if gates[key] != "passed" {
			return false
		}
	}
	return true
}

func anyVS03GateFailed(gates map[string]any) bool {
	for _, key := range []string{"snapshotGate", "closureGate", "evidenceGate", "semanticAtomicityGate", "taskRelevanceGate", "comprehensionGate"} {
		if gates[key] == "failed" {
			return true
		}
	}
	return false
}

func ValidateActivityStateV2(data []byte) error {
	doc, err := parseDocument(BaseURL+"rflsc.activity-state.v2.schema.json", data)
	if err != nil {
		return err
	}
	lag, _ := extractPublicationNumber(doc, "analysisLagMs")
	pending, _ := extractPublicationNumber(doc, "pendingRevisions")
	activity := extractPublicationString(doc, "activity")
	trace := extractPublicationString(doc, "traceId")
	currentSnapshot := extractPublicationString(doc, "currentSnapshotId")
	if lag < 0 || pending < 0 || (activity == "idle" && pending != 0) || (pending > 0 && (trace == "" || currentSnapshot == "")) || (activity != "idle" && (trace == "" || currentSnapshot == "")) {
		return fmt.Errorf("activity state has an impossible ledger state")
	}
	return nil
}

func ValidatePublicationCandidateV2(data []byte) error {
	doc, err := parseDocument(BaseURL+"rflsc.publication-candidate.v2.schema.json", data)
	if err != nil {
		return err
	}
	gates := extractPublicationObject(doc, "currentPublication")
	status := extractPublicationString(doc, "status")
	if status == "eligible" && !allVS03GatesPassed(gates) {
		return fmt.Errorf("eligible candidate has a failed publication gate")
	}
	if status == "rejected" && !anyVS03GateFailed(gates) {
		return fmt.Errorf("rejected candidate has no failed publication gate")
	}
	if extractPublicationString(doc, "authority") != "candidate" || extractPublicationString(doc, "freshness") == "" || extractPublicationString(doc, "mapRef") == extractPublicationString(doc, "closureRef") {
		return fmt.Errorf("candidate identity or artifact roles are inconsistent")
	}
	return nil
}

func ValidateGenerationProofManifestV2(data []byte) error {
	doc, err := parseDocument(BaseURL+"rflsc.generation-proof-manifest.v2.schema.json", data)
	if err != nil {
		return err
	}
	gates := extractPublicationObject(doc, "currentPublication")
	if extractPublicationString(gates, "eligibility") == "passed" && !allVS03GatesPassed(gates) {
		return fmt.Errorf("current proof says passed while a gate failed")
	}
	if extractPublicationString(gates, "eligibility") == "rejected" && !anyVS03GateFailed(gates) {
		return fmt.Errorf("rejected proof has no failed gate")
	}
	if extractPublicationString(doc, "validatedAgainstSnapshotId") != extractPublicationString(doc, "expectedLiveHeadSnapshotId") {
		return fmt.Errorf("proof snapshot is not bound to expected live head")
	}
	if extractPublicationString(doc, "computedSnapshotId") == "" {
		return fmt.Errorf("proof computed snapshot identity is missing")
	}
	if extractPublicationString(doc, "computedSnapshotId") == extractPublicationString(doc, "computedBasisId") {
		return fmt.Errorf("proof computed snapshot identity was populated with the basis identity")
	}
	if !isVS03Digest(extractPublicationString(doc, "capabilityProfileDigest")) {
		return fmt.Errorf("proof capability profile digest is not a canonical sha256 digest")
	}
	refs := extractPublicationObject(doc, "artifactRefs")
	for _, name := range []string{"semanticMap", "projection", "analysisReadSet", "observationClosure", "analyzerResult"} {
		if !isVS03CASRef(extractPublicationString(refs, name)) {
			return fmt.Errorf("proof canonical artifact ref %q is missing or malformed", name)
		}
	}
	for _, name := range []string{"semanticDelta", "evidenceIndex"} {
		if ref := extractPublicationString(refs, name); ref != "" && !isVS03CASRef(ref) {
			return fmt.Errorf("proof optional artifact ref %q is malformed", name)
		}
	}
	settlement := extractPublicationObject(doc, "settlementEvaluation")
	gate := extractPublicationString(settlement, "gate")
	blocking := extractPublicationArray(settlement, "blockingObligationRefs")
	evaluatedAt, hasEvaluatedAt := settlement["evaluatedAt"]
	if (gate == "passed" || gate == "failed") && (!hasEvaluatedAt || evaluatedAt == nil) {
		return fmt.Errorf("settlement %s lacks evaluation timestamp", gate)
	}
	if gate == "passed" && len(blocking) != 0 {
		return fmt.Errorf("passed settlement has blocking obligations")
	}
	if gate == "failed" && len(blocking) == 0 {
		return fmt.Errorf("failed settlement has no blocking obligation")
	}
	if extractPublicationString(doc, "generationId") == extractPublicationString(doc, "expectedPreviousGenerationId") && extractPublicationString(doc, "expectedPreviousGenerationId") != "" {
		return fmt.Errorf("proof predecessor equals new generation")
	}
	return nil
}

func isVS03Digest(value string) bool {
	if len(value) != 64 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32
}

func isVS03CASRef(value string) bool {
	const prefix = "cas:sha256:"
	return strings.HasPrefix(value, prefix) && isVS03Digest(strings.TrimPrefix(value, prefix))
}

func ValidateActivePointerV2(data []byte) error {
	doc, err := parseDocument(BaseURL+"rflsc.active-pointer.v2.schema.json", data)
	if err != nil {
		return err
	}
	if extractPublicationString(doc, "validatedAgainstSnapshotId") != extractPublicationString(doc, "expectedLiveHeadSnapshotId") {
		return fmt.Errorf("active pointer is not bound to captured live head")
	}
	if extractPublicationString(doc, "validatedAgainstSnapshotId") == extractPublicationString(doc, "computedBasisId") {
		return fmt.Errorf("active pointer snapshot identity was populated with the basis identity")
	}
	if previous, ok := doc["expectedPreviousGenerationId"].(string); ok && previous != "" && previous == extractPublicationString(doc, "generationId") {
		return fmt.Errorf("active pointer predecessor equals generation")
	}
	if ref := extractPublicationString(doc, "proofManifestRef"); ref != "" && ref != extractPublicationString(doc, "manifestObjectRef") {
		return fmt.Errorf("active pointer proof and manifest refs differ")
	}
	if extractPublicationString(doc, "authority") == "current_proof" && extractPublicationString(doc, "freshness") != "current" {
		return fmt.Errorf("current proof pointer is not current")
	}
	return nil
}

func ValidateVerifiedGapV2(data []byte) error {
	doc, err := parseDocument(BaseURL+"rflsc.verified-gap.v2.schema.json", data)
	if err != nil {
		return err
	}
	lag, _ := extractPublicationNumber(doc, "analysisLagMs")
	pending, _ := extractPublicationNumber(doc, "pendingRevisions")
	if extractPublicationString(doc, "freshness") != "last_verified" || lag < 0 || pending < 0 || len(extractPublicationArray(doc, "intersectedCauses")) == 0 {
		return fmt.Errorf("verified gap has invalid freshness or measured gap state")
	}
	return nil
}

func ValidateSettlementV2(data []byte) error {
	doc, err := parseDocument(BaseURL+"rflsc.settlement.v2.schema.json", data)
	if err != nil {
		return err
	}
	stage, gate := extractPublicationString(doc, "qualityStage"), extractPublicationString(doc, "gate")
	if (stage == "Q1" || stage == "Q2") && gate != "pending" {
		return fmt.Errorf("%s settlement cannot be %s", stage, gate)
	}
	if (stage == "Q3" || stage == "Q4") && gate == "pending" {
		return fmt.Errorf("%s settlement cannot remain pending", stage)
	}
	seen := map[string]bool{}
	verifiedRequired := 0
	blocking := extractPublicationArray(doc, "blockingObligationRefs")
	obligations := extractPublicationArray(doc, "obligations")
	byID := map[string]map[string]any{}
	for _, raw := range obligations {
		ob, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("settlement obligation is not an object")
		}
		id := extractPublicationString(ob, "obligationId")
		if seen[id] {
			return fmt.Errorf("duplicate settlement obligation %q", id)
		}
		seen[id], byID[id] = true, ob
		if required, _ := ob["required"].(bool); required {
			if extractPublicationString(ob, "status") == "verified" {
				verifiedRequired++
			}
		}
	}
	evaluatedAt, hasEvaluatedAt := doc["evaluatedAt"]
	if gate == "passed" {
		if len(blocking) != 0 || verifiedRequired != countRequiredVS03(obligations) || !hasEvaluatedAt || evaluatedAt == nil {
			return fmt.Errorf("passed settlement lacks complete verified obligations")
		}
		for _, raw := range obligations {
			ob := raw.(map[string]any)
			if required, _ := ob["required"].(bool); required && (extractPublicationString(ob, "status") == "unknown" || extractPublicationString(ob, "status") == "conflict") {
				return fmt.Errorf("passed settlement contains unresolved obligation")
			}
		}
	}
	if gate == "failed" {
		if !hasEvaluatedAt || evaluatedAt == nil || len(blocking) == 0 {
			return fmt.Errorf("failed settlement lacks evaluation or blocking obligation")
		}
		for _, raw := range blocking {
			id, _ := raw.(string)
			ob, ok := byID[id]
			if !ok || (func() bool { b, _ := ob["required"].(bool); return !b })() || extractPublicationString(ob, "status") == "verified" {
				return fmt.Errorf("blocking obligation %q is not a required unresolved obligation", id)
			}
		}
	}
	return nil
}

func countRequiredVS03(values []any) int {
	n := 0
	for _, raw := range values {
		if ob, ok := raw.(map[string]any); ok {
			if required, _ := ob["required"].(bool); required {
				n++
			}
		}
	}
	return n
}

func ValidateEventEnvelopeV2(data []byte) error {
	doc, err := parseDocument(BaseURL+"rflsc.event-envelope.v2.schema.json", data)
	if err != nil {
		return err
	}
	switch extractPublicationString(doc, "eventType") {
	case "generation.published":
		for _, key := range []string{"generationId", "computedBasisId", "validatedAgainstSnapshotId"} {
			if v, ok := doc[key]; !ok || v == nil || (key == "generationId" && extractPublicationString(doc, key) == "") {
				return fmt.Errorf("published event lacks %s identity", key)
			}
		}
	case "generation.gap":
		dataObj := extractPublicationObject(doc, "data")
		if extractPublicationString(dataObj, "freshness") != "last_verified" {
			return fmt.Errorf("gap event lacks last_verified state")
		}
	}
	if ref := extractPublicationString(doc, "payloadRef"); ref != "" && !strings.HasPrefix(ref, "cas:") {
		return fmt.Errorf("event payload ref is not immutable")
	}
	return nil
}

// ValidateFlowViewStateV2 is a spelling-compatible alias for callers that use
// the shorter state name.
func ValidateFlowViewStateV2(data []byte) error { return ValidateFlowViewViewStateV2(data) }

func ValidateFlowViewViewStateV2(data []byte) error {
	doc, err := parseDocument(BaseURL+"rflsc.flowview-view-state.v2.schema.json", data)
	if err != nil {
		return err
	}
	identityLoss, _ := doc["identityLoss"].(bool)
	preserved, _ := doc["preserved"].(bool)
	if identityLoss && preserved {
		return fmt.Errorf("view state cannot report identity loss and preservation together")
	}
	visible := map[string]bool{}
	for _, raw := range extractPublicationArray(doc, "visibleStepRefs") {
		if ref, ok := raw.(string); ok {
			visible[ref] = true
		}
	}
	if selected, ok := doc["selectedStepId"].(string); ok && selected != "" && !visible[selected] {
		return fmt.Errorf("selected step %q is not visible", selected)
	}
	selectedIdentity := extractPublicationString(doc, "selectedStructuralIdentity")
	selectedID := extractPublicationString(doc, "selectedStepId")
	if selectedID != "" && selectedIdentity == "" {
		return fmt.Errorf("selected step lacks structural identity")
	}
	if anchor := extractPublicationObject(doc, "logicalScrollAnchor"); anchor != nil {
		if !visible[extractPublicationString(anchor, "stepId")] {
			return fmt.Errorf("logical scroll anchor is not visible")
		}
		anchorIdentity := extractPublicationString(anchor, "structuralIdentity")
		if anchorIdentity == "" || selectedIdentity == "" || anchorIdentity != selectedIdentity {
			return fmt.Errorf("logical scroll anchor is not bound to selected structural identity")
		}
	}
	for _, raw := range extractPublicationArray(doc, "preservedStepRefs") {
		ref, _ := raw.(string)
		if !visible[ref] {
			return fmt.Errorf("preserved step %q is not visible", ref)
		}
	}
	if extractPublicationString(doc, "displayBasis") == "current" {
		verified, _ := doc["currentProofVerified"].(bool)
		if !verified || extractPublicationString(doc, "proofManifestRef") == "" {
			return fmt.Errorf("current view lacks current proof identity")
		}
	}
	return nil
}
