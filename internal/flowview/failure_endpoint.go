package flowview

// This file is the FlowView HTTP seam for the evidence-bounded failure
// investigation contract. It accepts explicit v2 identity and scope, selects
// one canonical graph, and keeps runtime authority behind server-owned seams.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"time"

	"codeflow/internal/contractharness"
	"codeflow/internal/protocol"
	oneshootRuntime "codeflow/internal/runtime"
	"codeflow/internal/secret"
	"codeflow/internal/semantic"
	"codeflow/internal/storage"
)

// RuntimeObservationRequest is the only request supplied to a trusted
// observation provider. The caller supplies an identifier and exact query
// scope, never a runtime observation object.
type RuntimeObservationRequest struct {
	ObservationID string
	Query         semantic.FailureQueryV2
	TargetRoot    string
}

// RuntimeObservationProvider resolves an observation identifier to a
// server-trusted, scoped observation. The FlowView boundary validates the
// returned object before it reaches the semantic failure seam.
type RuntimeObservationProvider interface {
	ResolveRuntimeObservation(context.Context, RuntimeObservationRequest) (*semantic.RuntimeObservationV2, error)
}

// RuntimeObservationProviderFunc adapts a function to RuntimeObservationProvider.
type RuntimeObservationProviderFunc func(context.Context, RuntimeObservationRequest) (*semantic.RuntimeObservationV2, error)

func (f RuntimeObservationProviderFunc) ResolveRuntimeObservation(ctx context.Context, request RuntimeObservationRequest) (*semantic.RuntimeObservationV2, error) {
	if f == nil {
		return nil, errors.New("runtime observation provider is nil")
	}
	return f(ctx, request)
}

// RuntimeObservationStore is an identifier-only store seam. It is separate
// from RuntimeObservationProvider so deployments can inject either a
// scope-aware provider or a server-owned store.
type RuntimeObservationStore interface {
	GetRuntimeObservation(context.Context, string) (*semantic.RuntimeObservationV2, error)
}

// RuntimeObservationStoreFunc adapts an identifier lookup function to
// RuntimeObservationStore.
type RuntimeObservationStoreFunc func(context.Context, string) (*semantic.RuntimeObservationV2, error)

func (f RuntimeObservationStoreFunc) GetRuntimeObservation(ctx context.Context, id string) (*semantic.RuntimeObservationV2, error) {
	if f == nil {
		return nil, errors.New("runtime observation store is nil")
	}
	return f(ctx, id)
}

// RuntimeOneShotExecutor is the narrow FlowView hook for a fresh trusted_local
// execution. It receives immutable snapshot bytes and exact consent.
type RuntimeOneShotExecutor interface {
	Execute(context.Context, oneshootRuntime.ExecutionRequest) (oneshootRuntime.ExecutionResult, error)
}

// RuntimeExecutor is the descriptive name used by FlowView callers.
type RuntimeExecutor = RuntimeOneShotExecutor

// RuntimeOneShotExecutorFunc adapts a one-shot execution function.
type RuntimeOneShotExecutorFunc func(context.Context, oneshootRuntime.ExecutionRequest) (oneshootRuntime.ExecutionResult, error)

func (f RuntimeOneShotExecutorFunc) Execute(ctx context.Context, request oneshootRuntime.ExecutionRequest) (oneshootRuntime.ExecutionResult, error) {
	if f == nil {
		return oneshootRuntime.ExecutionResult{}, errors.New("runtime executor is nil")
	}
	return f(ctx, request)
}

type failureProofSelection struct {
	Map      *semantic.SemanticMapIR
	Manifest *storage.GenerationProofManifest
	Pointer  *storage.ActivePointer
	Current  bool
}

type failureHTTPInput struct {
	Query          semantic.FailureQueryV2
	Runtime        failureRuntimeInput
	RuntimeConsent *oneshootRuntime.RuntimeConsent
}

type failureRuntimeInput struct {
	Execute              bool
	ExecutionID          string
	Nonce                string
	Operation            string
	Params               map[string]any
	Payload              map[string]any
	TaskScope            []string
	RequiredObservations []string
	CapabilityRequest    []string
}

// failureHTTPDisclosureError carries the minimum approved runtime boundary
// alongside a blocked error. A failure response must remain actionable without
// exposing the complete consent or an executor diagnostic. The wrapped error
// remains available for status-code selection and redacted messaging.
type failureHTTPDisclosureError struct {
	cause      error
	disclosure map[string]any
}

func (e *failureHTTPDisclosureError) Error() string {
	if e == nil || e.cause == nil {
		return "blocked: runtime execution was not promoted"
	}
	return e.cause.Error()
}

func (e *failureHTTPDisclosureError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func (e *failureHTTPDisclosureError) failureDisclosure() map[string]any {
	if e == nil {
		return nil
	}
	return e.disclosure
}

func failureHTTPBlockedWithConsent(message string, cause error, consent *oneshootRuntime.RuntimeConsent) error {
	disclosure := map[string]any{}
	if consent != nil {
		disclosure = map[string]any{
			"command":        consent.Command,
			"args":           consent.Args,
			"accessScope":    consent.AccessScope,
			"isolationScope": consent.IsolationScope,
		}
	}
	if message == "" {
		message = "runtime execution was not promoted"
	}
	if !strings.HasPrefix(strings.ToLower(message), "blocked:") {
		message = "blocked: " + message
	}
	if cause == nil {
		cause = errors.New(message)
	} else {
		cause = fmt.Errorf("%s: %w", message, cause)
	}
	return &failureHTTPDisclosureError{cause: cause, disclosure: disclosure}
}

func failureHTTPBlockedWithObservation(message string, cause error, observation *semantic.RuntimeObservationV2) error {
	err := failureHTTPBlockedWithConsent(message, cause, nil)
	if observation == nil {
		return err
	}
	if disclosed, ok := err.(*failureHTTPDisclosureError); ok {
		disclosed.disclosure["runtimeObservationId"] = observation.ObservationID
		disclosed.disclosure["isolationLevel"] = observation.IsolationLevel
	}
	return err
}

// handleFailureV2 is shared by the debug and incident HTTP endpoints. Both
// methods accept GET for the embedded UI and POST for callers that need to
// carry a RuntimeConsent object. Every request still goes through the same
// explicit v2 identity and proof checks.
func (s *Server) handleFailureV2(w http.ResponseWriter, r *http.Request, mode string) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		writeFailureHTTPError(w, errors.New("invalid_precondition: failure endpoint only accepts GET or POST"))
		return
	}

	input, err := parseFailureHTTPInput(r, mode)
	if err != nil {
		writeFailureHTTPError(w, err)
		return
	}
	selection, err := s.selectFailureProof(r.Context(), input.Query)
	if err != nil {
		writeFailureHTTPError(w, err)
		return
	}

	query := input.Query
	var observation *semantic.RuntimeObservationV2
	var isolation *oneshootRuntime.RuntimeIsolationResult
	if mode == "incident" {
		observation, err = s.resolveFailureObservation(r.Context(), query)
		if err != nil {
			writeFailureHTTPError(w, err)
			return
		}
		if observation == nil {
			writeFailureHTTPError(w, errors.New("not_observed: trusted runtime observation is unavailable"))
			return
		}
		if observation.IsolationLevel == "blocked" {
			writeFailureHTTPError(w, failureHTTPBlockedWithObservation("runtime isolation did not permit evidence promotion", nil, observation))
			return
		}
		// A runtime observation may fill the discriminator when the request
		// intentionally identifies only the server-side observation ID. The
		// provider is trusted for this identity, never for caller authority.
		incident := *query.Incident
		if incident.TraceID == "" {
			incident.TraceID = observation.TraceID
		}
		if incident.IncidentEvidenceID == "" {
			incident.IncidentEvidenceID = observation.IncidentEvidenceID
		}
		query.Incident = &incident

		if observation.IsolationLevel == "trusted_local" {
			isolation, err = s.executeTrustedLocal(r.Context(), selection, observation, input)
			if err != nil {
				writeFailureHTTPError(w, err)
				return
			}
			if isolation == nil || isolation.EvidencePromotion != oneshootRuntime.RuntimePromotionEligible {
				writeFailureHTTPError(w, errors.New("blocked: trusted_local runtime evidence is not promotion eligible"))
				return
			}
		}
	}

	trace, err := semantic.InvestigateFailureEvidenceBounded(semantic.FailureInvestigationInput{
		Query:              query,
		Map:                selection.Map,
		RuntimeObservation: observation,
	})
	if err != nil {
		writeFailureHTTPError(w, err)
		return
	}

	response, err := failureResponse(trace, observation, isolation)
	if err != nil {
		writeFailureHTTPError(w, fmt.Errorf("internal_error: failure trace failed egress validation: %w", err))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}

func parseFailureHTTPInput(r *http.Request, mode string) (failureHTTPInput, error) {
	if r.Method == http.MethodGet {
		query := failureQueryFromValues(r.URL.Query(), mode)
		if err := validateFailureHTTPIdentity(query); err != nil {
			return failureHTTPInput{}, err
		}
		return failureHTTPInput{Query: query}, nil
	}

	limited := io.LimitReader(r.Body, 2<<20)
	data, err := io.ReadAll(limited)
	if err != nil {
		return failureHTTPInput{}, fmt.Errorf("invalid_precondition: read failure query: %w", err)
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return failureHTTPInput{}, errors.New("missing_precondition: POST failure requests require an explicit v2 query")
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return failureHTTPInput{}, fmt.Errorf("invalid_precondition: failure request is not valid JSON: %w", err)
	}
	for _, key := range []string{"observation", "runtimeObservation", "runtime_observation"} {
		if _, ok := raw[key]; ok {
			return failureHTTPInput{}, errors.New("invalid_precondition: runtime observations must be resolved by the server from an observation ID")
		}
	}

	queryRaw := data
	if nested, ok := raw["query"]; ok {
		queryRaw = nested
	}
	var queryObject map[string]json.RawMessage
	if err := json.Unmarshal(queryRaw, &queryObject); err != nil {
		return failureHTTPInput{}, fmt.Errorf("invalid_precondition: failure query is not a JSON object: %w", err)
	}
	for _, key := range []string{"observation", "runtimeObservation", "runtime_observation"} {
		if _, ok := queryObject[key]; ok {
			return failureHTTPInput{}, errors.New("invalid_precondition: runtime observations must be resolved by the server from an observation ID")
		}
	}
	var query semantic.FailureQueryV2
	if err := json.Unmarshal(queryRaw, &query); err != nil {
		return failureHTTPInput{}, fmt.Errorf("invalid_precondition: failure query could not be decoded: %w", err)
	}
	if query.Mode != mode {
		return failureHTTPInput{}, fmt.Errorf("invalid_precondition: endpoint mode %q does not match query mode %q", mode, query.Mode)
	}
	if query.SchemaID == "" {
		query.SchemaID = semantic.FailureQuerySchemaID
	}
	if query.SchemaVersion == 0 {
		query.SchemaVersion = semantic.FailureContractSchemaVersion
	}
	if err := validateFailureHTTPIdentity(query); err != nil {
		return failureHTTPInput{}, err
	}

	input := failureHTTPInput{Query: query, Runtime: decodeFailureRuntimeInput(raw)}
	if consentRaw, ok := raw["runtimeConsent"]; ok {
		var consent oneshootRuntime.RuntimeConsent
		if err := json.Unmarshal(consentRaw, &consent); err != nil {
			return failureHTTPInput{}, errors.New("blocked: runtime consent is not valid JSON")
		}
		input.RuntimeConsent = &consent
	}
	if executeRaw, ok := raw["execute"]; ok {
		var execute bool
		if err := json.Unmarshal(executeRaw, &execute); err != nil {
			return failureHTTPInput{}, errors.New("invalid_precondition: execute must be boolean")
		}
		input.Runtime.Execute = execute
	}
	return input, nil
}

func failureQueryFromValues(values map[string][]string, mode string) semantic.FailureQueryV2 {
	get := func(name string) string {
		return strings.TrimSpace(firstFailureValue(values[name]))
	}
	basis := get("computedBasisId")
	generation := get("generationId")
	snapshot := get("validatedAgainstSnapshotId")
	freshness := get("freshness")
	query := semantic.FailureQueryV2{
		SchemaID:                   semantic.FailureQuerySchemaID,
		SchemaVersion:              semantic.FailureContractSchemaVersion,
		Mode:                       mode,
		ComputedBasisID:            basis,
		GenerationID:               generation,
		ValidatedAgainstSnapshotID: snapshot,
		Freshness:                  freshness,
	}
	if mode == "debug" {
		query.Debug = &semantic.FailureDebugQuery{
			Error:             get("error"),
			Symptom:           get("symptom"),
			FailureEvidenceID: get("failureEvidenceId"),
			EvidenceRefs:      failureListValues(values["evidenceRefs"]),
		}
		return query
	}
	query.Incident = &semantic.FailureIncidentQuery{
		TraceID:               get("traceId"),
		IncidentEvidenceID:    get("incidentEvidenceId"),
		RuntimeObservationID:  get("runtimeObservationId"),
		Scenario:              get("scenario"),
		Environment:           get("environment"),
		DependencyFingerprint: get("dependencyFingerprint"),
		TimeWindow: semantic.FailureTimeWindow{
			From: firstFailureNonEmpty(get("timeWindowFrom"), get("from")),
			To:   firstFailureNonEmpty(get("timeWindowTo"), get("to")),
		},
	}
	return query
}

func validateFailureHTTPIdentity(query semantic.FailureQueryV2) error {
	if query.SchemaID != semantic.FailureQuerySchemaID || query.SchemaVersion != semantic.FailureContractSchemaVersion {
		return errors.New("invalid_precondition: failure query must use rflsc.failure-query.v2")
	}
	if strings.TrimSpace(query.ComputedBasisID) == "" || strings.TrimSpace(query.GenerationID) == "" || strings.TrimSpace(query.ValidatedAgainstSnapshotID) == "" {
		return errors.New("missing_precondition: computedBasisId, generationId and validatedAgainstSnapshotId are required")
	}
	if query.Freshness != "current" && query.Freshness != "historical" {
		return errors.New("invalid_precondition: freshness must be current or historical")
	}
	if query.Mode == "debug" {
		return semantic.ValidateFailureQueryV2(query)
	}
	if query.Mode != "incident" || query.Incident == nil {
		return errors.New("invalid_precondition: failure query mode is not a supported discriminator")
	}
	incident := query.Incident
	if incident.TraceID == "" && incident.IncidentEvidenceID == "" && incident.RuntimeObservationID == "" {
		return errors.New("missing_precondition: incident query requires traceId, incidentEvidenceId, or runtimeObservationId")
	}
	if incident.Scenario == "" || incident.Environment == "" || incident.DependencyFingerprint == "" {
		return errors.New("missing_precondition: incident query requires scenario, environment and dependency fingerprint")
	}
	if _, err := parseFailureWindowHTTP(incident.TimeWindow); err != nil {
		return fmt.Errorf("missing_precondition: incident query requires a valid time window: %w", err)
	}
	// The semantic validator intentionally requires a trace or incident
	// Evidence discriminator. A server-side observation ID is allowed to fill
	// that identity only after the trusted provider returns the observation.
	if incident.TraceID == "" && incident.IncidentEvidenceID == "" {
		return nil
	}
	return semantic.ValidateFailureQueryV2(query)
}

func decodeFailureRuntimeInput(raw map[string]json.RawMessage) failureRuntimeInput {
	input := failureRuntimeInput{Params: map[string]any{}, Payload: map[string]any{}}
	var doc map[string]json.RawMessage
	if nested, ok := raw["runtime"]; ok {
		_ = json.Unmarshal(nested, &doc)
	}
	readString := func(name string) string {
		for _, source := range []map[string]json.RawMessage{doc, raw} {
			if value, ok := source[name]; ok {
				var text string
				if json.Unmarshal(value, &text) == nil {
					return strings.TrimSpace(text)
				}
			}
		}
		return ""
	}
	input.ExecutionID = readString("executionId")
	input.Nonce = readString("nonce")
	input.Operation = readString("operation")
	if value, ok := doc["params"]; ok {
		_ = json.Unmarshal(value, &input.Params)
	}
	if value, ok := doc["payload"]; ok {
		_ = json.Unmarshal(value, &input.Payload)
	}
	input.TaskScope = decodeFailureStringList(doc["taskScope"])
	input.RequiredObservations = decodeFailureStringList(doc["requiredObservations"])
	input.CapabilityRequest = decodeFailureStringList(doc["capabilityRequest"])
	if value, ok := raw["execute"]; ok {
		_ = json.Unmarshal(value, &input.Execute)
	}
	return input
}

func decodeFailureStringList(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var values []string
	if json.Unmarshal(raw, &values) != nil {
		return nil
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			result = append(result, value)
		}
	}
	return result
}

func (s *Server) selectFailureProof(ctx context.Context, query semantic.FailureQueryV2) (*failureProofSelection, error) {
	if query.Freshness == "current" {
		if s == nil || s.storage == nil {
			return nil, errors.New("current_proof_unavailable: validated current proof storage is unavailable")
		}
		bundle, err := s.storage.ReadValidatedActiveProofBundle()
		if err != nil {
			return nil, fmt.Errorf("current_proof_unavailable: %w", err)
		}
		if bundle == nil || bundle.Manifest == nil || bundle.Pointer == nil {
			return nil, errors.New("current_proof_unavailable: no validated current proof is published")
		}
		if s.engine == nil {
			return nil, errors.New("current_proof_unavailable: live workspace head is unavailable")
		}
		// ReconcileIfChanged recaptures direct worktree edits but preserves the
		// existing immutable head for an unchanged tree. A plain Reconcile would
		// manufacture a new identity on every read and invalidate a valid proof.
		liveHead, err := s.engine.ReconcileIfChanged(ctx, nil)
		if err != nil {
			return nil, fmt.Errorf("current_proof_unavailable: reconcile live workspace head: %w", err)
		}
		if liveHead == nil {
			return nil, errors.New("current_proof_unavailable: workspace has no measured live snapshot")
		}
		if bundle.Pointer.ExpectedLiveHeadSnapshotID == "" || bundle.Pointer.ExpectedLiveHeadSnapshotID != liveHead.SnapshotID || bundle.Pointer.ValidatedAgainstSnapshotID != liveHead.SnapshotID || bundle.Manifest.ExpectedLiveHeadSnapshotID != liveHead.SnapshotID || bundle.Manifest.ValidatedAgainstSnapshotID != liveHead.SnapshotID {
			return nil, fmt.Errorf("stale_live_head: validated current proof does not match live workspace head %q", liveHead.SnapshotID)
		}
		var mapIR semantic.SemanticMapIR
		if err := json.Unmarshal(bundle.SemanticMap, &mapIR); err != nil {
			return nil, fmt.Errorf("invalid_graph: decode validated current semantic map: %w", err)
		}
		if err := contractharness.ValidateSemanticMapIR(bundle.SemanticMap); err != nil {
			return nil, fmt.Errorf("invalid_graph: validated current semantic map contract: %w", err)
		}
		if mapIR.ComputedBasisID != query.ComputedBasisID || mapIR.GenerationID != query.GenerationID || mapIR.ValidatedAgainstSnapshotID != query.ValidatedAgainstSnapshotID {
			return nil, errors.New("incomparable_basis: current semantic map identity does not match the query")
		}
		if bundle.Manifest.GenerationID != query.GenerationID || bundle.Manifest.ComputedBasisID != query.ComputedBasisID || bundle.Manifest.ComputedSnapshotID != mapIR.ValidatedAgainstSnapshotID || bundle.Pointer.GenerationID != query.GenerationID || bundle.Pointer.ComputedBasisID != query.ComputedBasisID {
			return nil, errors.New("incomparable_basis: current proof identity does not match the query")
		}
		if mapIR.Freshness != "historical" && mapIR.Freshness != "current" {
			return nil, errors.New("invalid_graph: current semantic map freshness is not canonical")
		}
		// The persisted map describes the computation snapshot as historical.
		// Current authority is supplied by the validated proof and live-head
		// check above, so make only a defensive freshness projection for the
		// semantic v2 failure query.
		mapCopy := mapIR
		mapCopy.Freshness = "current"
		return &failureProofSelection{Map: &mapCopy, Manifest: bundle.Manifest, Pointer: bundle.Pointer, Current: true}, nil
	}
	if query.Freshness != "historical" {
		return nil, errors.New("invalid_precondition: freshness must be current or historical")
	}
	s.mu.Lock()
	mapIR := s.mapCache[query.GenerationID]
	if mapIR == nil {
		mapIR = s.mapCache[query.ComputedBasisID]
	}
	s.mu.Unlock()
	if mapIR == nil {
		return nil, errors.New("missing_precondition: exact historical generation is not available")
	}
	if mapIR.ComputedBasisID != query.ComputedBasisID || mapIR.GenerationID != query.GenerationID || mapIR.ValidatedAgainstSnapshotID != query.ValidatedAgainstSnapshotID || mapIR.Freshness != "historical" {
		return nil, errors.New("incomparable_basis: historical semantic map identity does not match the query")
	}
	mapCopy := *mapIR
	return &failureProofSelection{Map: &mapCopy}, nil
}

func (s *Server) resolveFailureObservation(ctx context.Context, query semantic.FailureQueryV2) (*semantic.RuntimeObservationV2, error) {
	if query.Incident == nil || strings.TrimSpace(query.Incident.RuntimeObservationID) == "" {
		return nil, errors.New("missing_precondition: incident query requires runtimeObservationId so the server can resolve trusted observation evidence")
	}
	id := query.Incident.RuntimeObservationID
	request := RuntimeObservationRequest{ObservationID: id, Query: query, TargetRoot: s.repoRoot}
	provider := firstFailureConfigured(s.runtimeObservationProvider, s.observationProvider)
	if !isNilFailureValue(provider) {
		observation, err := callFailureObservationProvider(ctx, provider, request)
		if err != nil {
			return nil, fmt.Errorf("unknown: trusted runtime observation provider failed: %w", err)
		}
		return validateResolvedFailureObservation(observation, id)
	}
	store := firstFailureConfigured(s.runtimeObservationStore, s.observationStore)
	if !isNilFailureValue(store) {
		observation, err := callFailureObservationStore(ctx, store, id)
		if err != nil {
			return nil, fmt.Errorf("unknown: trusted runtime observation store failed: %w", err)
		}
		return validateResolvedFailureObservation(observation, id)
	}
	return nil, errors.New("unknown: trusted runtime observation provider/store is unavailable")
}

func validateResolvedFailureObservation(observation *semantic.RuntimeObservationV2, requestedID string) (*semantic.RuntimeObservationV2, error) {
	if observation == nil {
		return nil, errors.New("not_observed: trusted runtime observation resolved to nil")
	}
	if observation.ObservationID != requestedID {
		return nil, errors.New("incomparable_basis: trusted runtime observation identity does not match requested observationId")
	}
	data, err := json.Marshal(observation)
	if err != nil {
		return nil, fmt.Errorf("invalid_evidence: marshal trusted runtime observation: %w", err)
	}
	if err := semantic.ValidateRuntimeObservationV2(*observation); err != nil {
		return nil, err
	}
	if err := contractharness.ValidateRuntimeObservationV2(data); err != nil {
		return nil, fmt.Errorf("invalid_evidence: trusted runtime observation contract: %w", err)
	}
	clean, _, err := secret.RedactJSON(data)
	if err != nil {
		return nil, fmt.Errorf("blocked: redact trusted runtime observation: %w", err)
	}
	var out semantic.RuntimeObservationV2
	if err := json.Unmarshal(clean, &out); err != nil {
		return nil, fmt.Errorf("blocked: decode redacted runtime observation: %w", err)
	}
	if err := contractharness.ValidateRuntimeObservationV2(clean); err != nil {
		return nil, fmt.Errorf("blocked: redacted runtime observation contract: %w", err)
	}
	if err := semantic.ValidateRuntimeObservationV2(out); err != nil {
		return nil, fmt.Errorf("blocked: redacted runtime observation semantic validation: %w", err)
	}
	return &out, nil
}

func callFailureObservationProvider(ctx context.Context, provider any, request RuntimeObservationRequest) (*semantic.RuntimeObservationV2, error) {
	switch p := provider.(type) {
	case RuntimeObservationProvider:
		return p.ResolveRuntimeObservation(ctx, request)
	case func(context.Context, RuntimeObservationRequest) (*semantic.RuntimeObservationV2, error):
		return p(ctx, request)
	case interface {
		Resolve(context.Context, RuntimeObservationRequest) (*semantic.RuntimeObservationV2, error)
	}:
		return p.Resolve(ctx, request)
	case interface {
		ResolveRuntimeObservation(context.Context, string) (*semantic.RuntimeObservationV2, error)
	}:
		return p.ResolveRuntimeObservation(ctx, request.ObservationID)
	case func(context.Context, string) (*semantic.RuntimeObservationV2, error):
		return p(ctx, request.ObservationID)
	default:
		return nil, fmt.Errorf("unsupported runtime observation provider type %T", provider)
	}
}

func callFailureObservationStore(ctx context.Context, store any, id string) (*semantic.RuntimeObservationV2, error) {
	switch p := store.(type) {
	case RuntimeObservationStore:
		return p.GetRuntimeObservation(ctx, id)
	case func(context.Context, string) (*semantic.RuntimeObservationV2, error):
		return p(ctx, id)
	case interface {
		Get(context.Context, string) (*semantic.RuntimeObservationV2, error)
	}:
		return p.Get(ctx, id)
	case interface {
		Lookup(context.Context, string) (*semantic.RuntimeObservationV2, error)
	}:
		return p.Lookup(ctx, id)
	default:
		return nil, fmt.Errorf("unsupported runtime observation store type %T", store)
	}
}

func (s *Server) executeTrustedLocal(ctx context.Context, selection *failureProofSelection, observation *semantic.RuntimeObservationV2, input failureHTTPInput) (*oneshootRuntime.RuntimeIsolationResult, error) {
	rawConsent := input.RuntimeConsent
	if rawConsent == nil {
		rawConsent = cloneRuntimeConsent(s.runtimeConsent)
	}
	if rawConsent == nil {
		return nil, errors.New("blocked: trusted_local execution requires an exact RuntimeConsentV1")
	}
	consentData, err := json.Marshal(rawConsent)
	if err != nil {
		return nil, errors.New("blocked: runtime consent is not valid JSON")
	}
	if err := contractharness.ValidateRuntimeConsentV1(consentData); err != nil {
		return nil, fmt.Errorf("blocked: runtime consent is not a valid RuntimeConsentV1: %w", err)
	}
	var consent oneshootRuntime.RuntimeConsent
	if err := json.Unmarshal(consentData, &consent); err != nil {
		return nil, errors.New("blocked: runtime consent could not be decoded")
	}
	disclosureConsent := &consent
	if s.runtimeConsent != nil {
		// A mismatched caller consent must not control the disclosure. Use the
		// server-approved boundary for the blocked response instead.
		disclosureConsent = s.runtimeConsent
	}
	blocked := func(message string, cause error) error {
		return failureHTTPBlockedWithConsent(message, cause, disclosureConsent)
	}
	if err := consent.Validate(time.Now().UTC()); err != nil {
		return nil, blocked("runtime consent is not currently valid", err)
	}
	if observation == nil || observation.TrustedLocalApproval == nil || !observation.TrustedLocalApproval.Approved {
		return nil, blocked("trusted_local execution requires server-validated approval", nil)
	}
	if observation.TrustedLocalApproval.ApprovedBy != consent.ActorID {
		return nil, blocked("runtime consent actor does not match trusted_local approval", nil)
	}
	executor := firstFailureConfigured(s.runtimeExecutor, s.oneShotExecutor)
	if isNilFailureValue(executor) {
		return nil, blocked("trusted_local one-shot executor is unavailable", nil)
	}
	snapshot, release, err := s.snapshotForFailureExecution(selection)
	if err != nil {
		return nil, blocked("immutable execution snapshot is unavailable", err)
	}
	defer release()
	nonce := input.Runtime.Nonce
	if nonce == "" {
		nonce = consent.Nonce
	}
	if nonce != consent.Nonce {
		return nil, blocked("runtime consent nonce does not match the execution request", nil)
	}
	if !reflect.DeepEqual(s.runtimeExecutionSpec, oneshootRuntime.RuntimeExecutionSpec{}) {
		if err := consent.Matches(s.runtimeExecutionSpec, snapshot.SnapshotID, snapshot.RootTreeID, nonce); err != nil {
			return nil, blocked("runtime consent does not match configured command or isolation scope", err)
		}
	}
	request := oneshootRuntime.ExecutionRequest{
		ExecutionID:          input.Runtime.ExecutionID,
		Nonce:                nonce,
		Consent:              consent,
		Snapshot:             snapshot,
		Operation:            input.Runtime.Operation,
		Params:               input.Runtime.Params,
		TaskScope:            input.Runtime.TaskScope,
		RequiredObservations: input.Runtime.RequiredObservations,
		CapabilityRequest:    input.Runtime.CapabilityRequest,
		Payload:              input.Runtime.Payload,
	}
	result, execErr := callFailureRuntimeExecutor(ctx, executor, request)
	if execErr != nil {
		return nil, blocked("trusted_local executor returned a terminal error", execErr)
	}
	if err := validateFailureIsolationResult(result.Isolation); err != nil {
		return nil, blocked("trusted_local isolation result is not valid", err)
	}
	if err := matchFailureIsolationIdentity(result.Isolation, consent, snapshot, nonce); err != nil {
		return nil, blocked("trusted_local isolation result does not match exact consent or immutable snapshot", err)
	}
	if result.Isolation.EvidencePromotion != oneshootRuntime.RuntimePromotionEligible {
		return nil, blocked("trusted_local runtime evidence promotion is blocked", nil)
	}
	clean, err := validateAndRedactFailureIsolation(result.Isolation)
	if err != nil {
		return nil, fmt.Errorf("blocked: runtime isolation result failed egress validation: %w", err)
	}
	return &clean, nil
}

func (s *Server) snapshotForFailureExecution(selection *failureProofSelection) (protocol.Snapshot, func(), error) {
	if s == nil || s.engine == nil || selection == nil || selection.Map == nil {
		return protocol.Snapshot{}, nil, errors.New("snapshot engine and canonical map are required")
	}
	lease, err := s.engine.SnapshotVFS(selection.Map.ValidatedAgainstSnapshotID)
	if err != nil {
		return protocol.Snapshot{}, nil, err
	}
	snapshot, err := protocol.SnapshotFromLease(lease)
	if err != nil {
		_ = lease.Close()
		return protocol.Snapshot{}, nil, err
	}
	return snapshot, func() { _ = lease.Close() }, nil
}

func callFailureRuntimeExecutor(ctx context.Context, executor any, request oneshootRuntime.ExecutionRequest) (oneshootRuntime.ExecutionResult, error) {
	switch e := executor.(type) {
	case RuntimeOneShotExecutor:
		return e.Execute(ctx, request)
	case func(context.Context, oneshootRuntime.ExecutionRequest) (oneshootRuntime.ExecutionResult, error):
		return e(ctx, request)
	case interface {
		ExecuteOneShot(context.Context, oneshootRuntime.ExecutionRequest) (oneshootRuntime.ExecutionResult, error)
	}:
		return e.ExecuteOneShot(ctx, request)
	default:
		return oneshootRuntime.ExecutionResult{}, fmt.Errorf("unsupported runtime executor type %T", executor)
	}
}

func validateFailureIsolationResult(result oneshootRuntime.RuntimeIsolationResult) error {
	data, err := json.Marshal(result)
	if err != nil {
		return err
	}
	if err := contractharness.ValidateRuntimeIsolationResultV1(data); err != nil {
		return err
	}
	return result.Validate()
}

func matchFailureIsolationIdentity(result oneshootRuntime.RuntimeIsolationResult, consent oneshootRuntime.RuntimeConsent, snapshot protocol.Snapshot, nonce string) error {
	if result.ConsentID != consent.ConsentID || result.ActorID != consent.ActorID || result.Nonce != nonce {
		return errors.New("runtime isolation result consent identity differs from approved consent")
	}
	if result.SnapshotID != snapshot.SnapshotID || result.SnapshotTreeDigest != snapshot.RootTreeID || result.RecomputedTreeDigest != snapshot.RootTreeID {
		return errors.New("runtime isolation result snapshot identity differs from immutable execution input")
	}
	if result.Command != consent.Command || !reflect.DeepEqual(result.Args, consent.Args) || result.CommandDigest != oneshootRuntime.CommandDigest(consent.Command, consent.Args) {
		return errors.New("runtime isolation result command differs from approved command")
	}
	if result.AccessScope != consent.AccessScope || result.IsolationScope != consent.IsolationScope {
		return errors.New("runtime isolation result access or isolation scope differs from approved scope")
	}
	return nil
}

func validateAndRedactFailureIsolation(result oneshootRuntime.RuntimeIsolationResult) (oneshootRuntime.RuntimeIsolationResult, error) {
	if err := validateFailureIsolationResult(result); err != nil {
		return oneshootRuntime.RuntimeIsolationResult{}, err
	}
	data, err := json.Marshal(result)
	if err != nil {
		return oneshootRuntime.RuntimeIsolationResult{}, err
	}
	clean, _, err := secret.RedactJSON(data)
	if err != nil {
		return oneshootRuntime.RuntimeIsolationResult{}, err
	}
	var out oneshootRuntime.RuntimeIsolationResult
	if err := json.Unmarshal(clean, &out); err != nil {
		return oneshootRuntime.RuntimeIsolationResult{}, err
	}
	if err := validateFailureIsolationResult(out); err != nil {
		return oneshootRuntime.RuntimeIsolationResult{}, err
	}
	return out, nil
}

func failureResponse(trace *semantic.FailurePathTraceV2, observation *semantic.RuntimeObservationV2, isolation *oneshootRuntime.RuntimeIsolationResult) (any, error) {
	cleanTrace, err := validateAndRedactFailureTrace(trace)
	if err != nil {
		return nil, err
	}
	traceData, err := json.Marshal(cleanTrace)
	if err != nil {
		return nil, err
	}
	traceData, err = ensureFailureTraceSchemaFields(traceData)
	if err != nil {
		return nil, err
	}
	var envelope map[string]any
	if err := json.Unmarshal(traceData, &envelope); err != nil {
		return nil, err
	}
	if observation != nil {
		observationData, err := json.Marshal(observation)
		if err != nil {
			return nil, err
		}
		cleanObservation, _, err := secret.RedactJSON(observationData)
		if err != nil {
			return nil, err
		}
		var safeObservation any
		if err := json.Unmarshal(cleanObservation, &safeObservation); err != nil {
			return nil, err
		}
		envelope["runtimeObservation"] = safeObservation
	}
	if isolation != nil {
		cleanIsolation, err := validateAndRedactFailureIsolation(*isolation)
		if err != nil {
			return nil, err
		}
		envelope["runtimeIsolation"] = cleanIsolation
		envelope["command"] = cleanIsolation.Command
		envelope["args"] = cleanIsolation.Args
		envelope["accessScope"] = cleanIsolation.AccessScope
		envelope["isolationScope"] = cleanIsolation.IsolationScope
	}
	envelope["corroborated"] = failureTraceCorroboratedHTTP(cleanTrace)
	return envelope, nil
}

func failureTraceCorroboratedHTTP(trace *semantic.FailurePathTraceV2) bool {
	if trace == nil || trace.RuntimeObservationRef == "" || trace.HasConflicts {
		return false
	}
	for _, node := range trace.Nodes {
		if node.Status == "corroborated" {
			return true
		}
	}
	return false
}

func validateAndRedactFailureTrace(trace *semantic.FailurePathTraceV2) (*semantic.FailurePathTraceV2, error) {
	if trace == nil {
		return nil, errors.New("failure trace is nil")
	}
	// The v2 egress schema distinguishes an observed empty frontier from an
	// omitted frontier. Keep all bounded collections explicit before the
	// contract validator sees the response.
	if trace.UnknownFrontier == nil {
		trace.UnknownFrontier = []semantic.FailureFrontier{}
	}
	if trace.RecoveryStates == nil {
		trace.RecoveryStates = []semantic.FailureRecoveryState{}
	}
	if trace.Timeline == nil {
		trace.Timeline = []semantic.TimelineEvent{}
	}
	for i := range trace.Timeline {
		// The schema allows one evidenceRef or an evidenceRefs array. The
		// semantic producer keeps both for Go convenience, while the strict
		// egress validator treats the same reference in both fields as a
		// duplicate. Preserve the complete array and omit the scalar copy.
		if len(trace.Timeline[i].EvidenceRefs) > 0 {
			trace.Timeline[i].EvidenceRef = ""
		}
	}
	if err := semantic.ValidateFailurePathTraceV2(*trace); err != nil {
		return nil, err
	}
	data, err := json.Marshal(trace)
	if err != nil {
		return nil, err
	}
	clean, _, err := secret.RedactJSON(data)
	if err != nil {
		return nil, err
	}
	// The Go contract uses omitempty for empty collections while the public
	// v2 JSON schema requires unknownFrontier to be explicit. Validate the
	// schema-shaped representation and preserve that field at HTTP egress.
	clean, err = ensureFailureTraceSchemaFields(clean)
	if err != nil {
		return nil, err
	}
	var out semantic.FailurePathTraceV2
	if err := json.Unmarshal(clean, &out); err != nil {
		return nil, err
	}
	if err := semantic.ValidateFailurePathTraceV2(out); err != nil {
		return nil, err
	}
	if err := contractharness.ValidateFailurePathTraceV2(clean); err != nil {
		return nil, err
	}
	return &out, nil
}

func ensureFailureTraceSchemaFields(data []byte) ([]byte, error) {
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	if _, ok := doc["unknownFrontier"]; !ok {
		doc["unknownFrontier"] = []any{}
	}
	if _, ok := doc["recoveryStates"]; !ok {
		doc["recoveryStates"] = []any{}
	}
	if _, ok := doc["timeline"]; !ok {
		doc["timeline"] = []any{}
	}
	return json.Marshal(doc)
}

func writeFailureHTTPError(w http.ResponseWriter, err error) {
	if err == nil {
		err = errors.New("unknown: failure investigation failed")
	}
	message := secret.Redact(err.Error()).Text
	code := failureHTTPErrorCode(err)
	status := http.StatusBadRequest
	switch code {
	case "incomparable_basis", "invalid_scope", "invalid_evidence", "invalid_anchor", "invalid_graph":
		status = http.StatusUnprocessableEntity
	case "current_proof_unavailable", "stale_live_head", "unknown":
		status = http.StatusConflict
	case "not_observed":
		status = http.StatusNotFound
	case "blocked", "invalid_authority":
		status = http.StatusForbidden
	case "internal_error":
		status = http.StatusInternalServerError
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	response := map[string]any{"code": code, "status": failureHTTPStatus(code), "state": failureHTTPStatus(code), "message": message, "corroborated": false}
	var disclosed interface{ failureDisclosure() map[string]any }
	if errors.As(err, &disclosed) {
		if safe, ok := secret.RedactAndClip(disclosed.failureDisclosure(), 512, 64).(map[string]any); ok {
			for _, key := range []string{"command", "args", "accessScope", "isolationScope", "runtimeObservationId", "isolationLevel"} {
				if value, present := safe[key]; present {
					response[key] = value
				}
			}
		}
	}
	_ = json.NewEncoder(w).Encode(response)
}

func failureHTTPErrorCode(err error) string {
	if err == nil {
		return "unknown"
	}
	var queryErr *semantic.QueryError
	if errors.As(err, &queryErr) && queryErr.Code != "" {
		return queryErr.Code
	}
	text := strings.ToLower(err.Error())
	for _, code := range []string{"missing_precondition", "invalid_precondition", "incomparable_basis", "current_proof_unavailable", "stale_live_head", "not_observed", "invalid_scope", "invalid_evidence", "invalid_anchor", "invalid_graph", "invalid_authority", "blocked", "internal_error", "unknown"} {
		if strings.Contains(text, code) {
			return code
		}
	}
	return "unknown"
}

func failureHTTPStatus(code string) string {
	if code == "blocked" || code == "invalid_authority" {
		return "blocked"
	}
	if code == "unknown" {
		return "unknown"
	}
	return "invalid"
}

func cloneRuntimeConsent(in *oneshootRuntime.RuntimeConsent) *oneshootRuntime.RuntimeConsent {
	if in == nil {
		return nil
	}
	out := *in
	out.Args = append([]string(nil), in.Args...)
	return &out
}

func firstFailureConfigured(values ...any) any {
	for _, value := range values {
		if !isNilFailureValue(value) {
			return value
		}
	}
	return nil
}

func isNilFailureValue(value any) bool {
	if value == nil {
		return true
	}
	ref := reflect.ValueOf(value)
	switch ref.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return ref.IsNil()
	default:
		return false
	}
}

func firstFailureValue(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func firstFailureNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func failureListValues(values []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, value := range values {
		for _, item := range strings.Split(value, ",") {
			item = strings.TrimSpace(item)
			if item != "" && !seen[item] {
				seen[item] = true
				out = append(out, item)
			}
		}
	}
	return out
}

func parseFailureWindowHTTP(window semantic.FailureTimeWindow) (time.Time, error) {
	if strings.TrimSpace(window.From) == "" || strings.TrimSpace(window.To) == "" {
		return time.Time{}, errors.New("from and to are required")
	}
	from, err := time.Parse(time.RFC3339Nano, window.From)
	if err != nil {
		return time.Time{}, errors.New("from is not RFC3339")
	}
	to, err := time.Parse(time.RFC3339Nano, window.To)
	if err != nil || to.Before(from) {
		return time.Time{}, errors.New("to is invalid or before from")
	}
	return from, nil
}
