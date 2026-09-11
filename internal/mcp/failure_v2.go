package mcp

// This file contains the public VS-06 MCP boundary.  The MCP server is an
// adapter boundary, not an evidence authority: it accepts an explicit v2
// query, selects a canonical map/proof, resolves runtime observations through
// an injected trusted provider, and only then calls the semantic v2 seam.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"codeflow/internal/contractharness"
	"codeflow/internal/flowview"
	"codeflow/internal/protocol"
	oneshoRuntime "codeflow/internal/runtime"
	"codeflow/internal/secret"
	"codeflow/internal/semantic"
	"codeflow/internal/storage"
)

// RuntimeObservationRequest is the only request supplied to a trusted
// observation provider.  ObservationID is an identifier, not an observation
// payload, and Query carries the exact scope that the provider result must
// satisfy before it can enter the semantic seam.
type RuntimeObservationRequest struct {
	ObservationID string
	Query         semantic.FailureQueryV2
	TargetRoot    string
}

// RuntimeObservationProvider resolves an observation identifier to a
// server-trusted, scoped RuntimeObservationV2.  A provider never receives
// authority to mark an observation corroborated.  The MCP boundary validates
// the returned object and its relation to the query first.
type RuntimeObservationProvider interface {
	ResolveRuntimeObservation(context.Context, RuntimeObservationRequest) (*semantic.RuntimeObservationV2, error)
}

// RuntimeObservationProviderFunc adapts a function to
// RuntimeObservationProvider.
type RuntimeObservationProviderFunc func(context.Context, RuntimeObservationRequest) (*semantic.RuntimeObservationV2, error)

func (f RuntimeObservationProviderFunc) ResolveRuntimeObservation(ctx context.Context, request RuntimeObservationRequest) (*semantic.RuntimeObservationV2, error) {
	if f == nil {
		return nil, errors.New("runtime observation provider is nil")
	}
	return f(ctx, request)
}

// RuntimeObservationStore is a convenient identifier-only store seam.  It is
// deliberately separate from RuntimeObservationProvider so deployments can
// inject either a scope-aware provider or a server-owned observation store.
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

// RuntimeOneShotExecutor is the narrow MCP hook for a fresh trusted_local
// execution.  The hook receives the immutable snapshot and exact consent.
// It must return the executor-owned RuntimeIsolationResultV1.
type RuntimeOneShotExecutor interface {
	Execute(context.Context, oneshoRuntime.ExecutionRequest) (oneshoRuntime.ExecutionResult, error)
}

// RuntimeOneShotExecutorFunc adapts a one-shot execution function to the MCP
// hook.
type RuntimeOneShotExecutorFunc func(context.Context, oneshoRuntime.ExecutionRequest) (oneshoRuntime.ExecutionResult, error)

func (f RuntimeOneShotExecutorFunc) Execute(ctx context.Context, request oneshoRuntime.ExecutionRequest) (oneshoRuntime.ExecutionResult, error) {
	if f == nil {
		return oneshoRuntime.ExecutionResult{}, errors.New("runtime executor is nil")
	}
	return f(ctx, request)
}

type failureProofSelection struct {
	Map      *semantic.SemanticMapIR
	Manifest *storage.GenerationProofManifest
	Pointer  *storage.ActivePointer
	Current  bool
}

func failureModeQuery(raw any) bool {
	obj, ok := raw.(map[string]any)
	if !ok {
		data, err := json.Marshal(raw)
		if err != nil {
			return false
		}
		if err := json.Unmarshal(data, &obj); err != nil {
			return false
		}
	}
	mode, _ := obj["mode"].(string)
	if mode == "debug" || mode == "incident" {
		return true
	}
	schemaID, _ := obj["schemaId"].(string)
	return schemaID == semantic.FailureQuerySchemaID
}

// handleFailureV2Request is shared by investigate_failure and the failure
// modes of query_task_view.  It never falls back to the legacy synthetic
// handler or to a default active generation.
func (s *Server) handleFailureV2Request(ctx context.Context, args map[string]any) (any, error) {
	if err := s.checkAuth(args["token"]); err != nil {
		return failureStatus("unauthorized", "authentication is required", err), nil
	}

	rawQuery, ok := args["query"]
	if !ok || rawQuery == nil {
		return failureStatus("missing_precondition", "investigation requires an explicit rflsc.failure-query.v2 query", nil), nil
	}
	queryBytes, err := json.Marshal(rawQuery)
	if err != nil {
		return failureStatus("invalid_precondition", "failure query is not valid JSON", err), nil
	}
	if err := contractharness.ValidateFailureQueryV2(queryBytes); err != nil {
		return failureStatus("missing_precondition", "failure query schema/precondition validation failed", err), nil
	}
	var query semantic.FailureQueryV2
	if err := json.Unmarshal(queryBytes, &query); err != nil {
		return failureStatus("invalid_precondition", "failure query could not be decoded", err), nil
	}
	// Incident runtime scope is not complete unless the public request names
	// the server-owned observation.  A trace id alone must not authorize a
	// caller-provided observation.
	if query.Mode == "incident" && query.Incident != nil && strings.TrimSpace(query.Incident.RuntimeObservationID) == "" {
		return failureStatus("missing_precondition", "incident query requires runtimeObservationId so the server can resolve trusted observation evidence", nil), nil
	}
	if _, present := args["runtimeObservation"]; present {
		return failureStatus("invalid_precondition", "runtimeObservation payloads are not accepted at the public MCP boundary; provide runtimeObservationId", nil), nil
	}

	targetRoot := s.resolveTarget(args["target"])
	selection, status := s.selectFailureProof(ctx, targetRoot, query, args)
	if status != nil {
		return status, nil
	}
	if selection == nil || selection.Map == nil {
		return failureStatus("missing_precondition", "a canonical semantic map is required", nil), nil
	}

	var observation *semantic.RuntimeObservationV2
	var isolation *oneshoRuntime.RuntimeIsolationResult
	if query.Mode == "incident" {
		observation, status = s.resolveFailureObservation(ctx, targetRoot, query)
		if status != nil {
			return enrichFailureStatusWithConsent(status, args), nil
		}
		if observation == nil {
			return failureStatus("unknown", "trusted runtime observation is unavailable", nil), nil
		}
		if observation.IsolationLevel == "blocked" {
			return failureStatusWithObservation("blocked", "trusted runtime observation is blocked and cannot corroborate a failure", nil, observation), nil
		}
		// A server-trusted observation may be the only incident discriminator.
		// Bind the provider's trace/evidence identity before entering the
		// semantic seam. Caller-supplied identity is still checked above when it
		// is present, while an observation ID alone cannot fabricate a trace.
		incident := *query.Incident
		if incident.TraceID == "" {
			incident.TraceID = observation.TraceID
		}
		if incident.IncidentEvidenceID == "" {
			incident.IncidentEvidenceID = observation.IncidentEvidenceID
		}
		query.Incident = &incident
		if observation.IsolationLevel == "trusted_local" {
			isolation, status = s.executeTrustedLocal(ctx, targetRoot, selection, query, observation, args)
			if status != nil {
				return status, nil
			}
			// Runtime evidence is admitted only after the observed isolation
			// result explicitly says promotion is eligible.
			if isolation == nil || isolation.EvidencePromotion != oneshoRuntime.RuntimePromotionEligible {
				return failureStatus("blocked", "trusted_local runtime evidence is not promotion eligible", nil), nil
			}
		}
	}

	trace, err := semantic.InvestigateFailureEvidenceBounded(semantic.FailureInvestigationInput{
		Query:              query,
		Map:                selection.Map,
		RuntimeObservation: observation,
	})
	if err != nil {
		return failureStatus(failureErrorCode(err), "failure investigation could not be established from canonical evidence", err), nil
	}
	trace, err = validateAndRedactFailureTrace(trace)
	if err != nil {
		return failureStatus("blocked", "failure trace failed egress validation", err), nil
	}

	if isolation != nil {
		redacted, err := validateAndRedactIsolation(*isolation)
		if err != nil {
			return failureStatus("blocked", "runtime isolation result failed egress validation", err), nil
		}
		return map[string]any{
			"trace":            trace,
			"runtimeIsolation": redacted,
			"command":          redacted.Command,
			"args":             redacted.Args,
			"accessScope":      redacted.AccessScope,
			"isolationScope":   redacted.IsolationScope,
			"corroborated":     failureTraceCorroborated(trace),
		}, nil
	}
	return trace, nil
}

func failureTraceCorroborated(trace *semantic.FailurePathTraceV2) bool {
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

func failureStatus(code, message string, cause error) map[string]any {
	status := "unknown"
	if code == "blocked" || code == "unauthorized" {
		status = "blocked"
	}
	doc := map[string]any{
		"code":         code,
		"status":       status,
		"state":        status,
		"message":      message,
		"corroborated": false,
	}
	if cause != nil {
		// Failure status is itself a public MCP payload. Keep wrapped provider,
		// storage, and validator diagnostics bounded and pass them through the
		// same secret gate as successful traces before exposing them as detail.
		doc["detail"] = boundedDiagnostic(cause.Error(), 512)
	}
	return doc
}

// failureStatusWithConsent keeps the trusted_local disclosure contract true
// even when execution is blocked before an executor-owned isolation result is
// available. The consent is not returned as a whole because it contains
// actor/nonce metadata. Only the command and the approved boundary are
// disclosed, and the values pass through the shared redaction/size gate.
func failureStatusWithConsent(code, message string, cause error, consent *oneshoRuntime.RuntimeConsent) map[string]any {
	doc := failureStatus(code, message, cause)
	if consent == nil {
		return doc
	}
	safe, _ := secret.RedactAndClip(map[string]any{
		"command":        consent.Command,
		"args":           consent.Args,
		"accessScope":    consent.AccessScope,
		"isolationScope": consent.IsolationScope,
	}, 512, 64).(map[string]any)
	for _, key := range []string{"command", "args", "accessScope", "isolationScope"} {
		if value, ok := safe[key]; ok {
			doc[key] = value
		}
	}
	return doc
}

// failureStatusWithObservation exposes only the server-trusted observation
// identity and isolation decision. The observation payload itself is never
// copied into a blocked status, and any secret-bearing provider diagnostics
// remain governed by failureStatus.
func failureStatusWithObservation(code, message string, cause error, observation *semantic.RuntimeObservationV2) map[string]any {
	doc := failureStatus(code, message, cause)
	if observation == nil {
		return doc
	}
	doc["runtimeObservationId"] = observation.ObservationID
	doc["isolationLevel"] = observation.IsolationLevel
	return doc
}

// enrichFailureStatusWithConsent adds the approved runtime boundary to a
// blocked provider/observation result. Observation validation happens before
// execution, so trusted_local approval failures otherwise have no executor
// result from which the public caller can learn the requested scope.
func enrichFailureStatusWithConsent(status map[string]any, args map[string]any) map[string]any {
	if status == nil || args == nil {
		return status
	}
	raw := firstArgument(args, "runtimeConsent", "consent")
	if raw == nil {
		return status
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return status
	}
	var consent oneshoRuntime.RuntimeConsent
	if err := json.Unmarshal(data, &consent); err != nil || consent.Command == "" {
		return status
	}
	safe, _ := secret.RedactAndClip(map[string]any{
		"command":        consent.Command,
		"args":           consent.Args,
		"accessScope":    consent.AccessScope,
		"isolationScope": consent.IsolationScope,
	}, 512, 64).(map[string]any)
	for _, key := range []string{"command", "args", "accessScope", "isolationScope"} {
		if value, ok := safe[key]; ok {
			status[key] = value
		}
	}
	return status
}

func failureErrorCode(err error) string {
	if err == nil {
		return "unknown"
	}
	var queryErr *semantic.QueryError
	if errors.As(err, &queryErr) && queryErr.Code != "" {
		return queryErr.Code
	}
	text := strings.ToLower(err.Error())
	switch {
	case strings.Contains(text, "blocked"):
		return "blocked"
	case strings.Contains(text, "incomparable_basis"):
		return "incomparable_basis"
	case strings.Contains(text, "invalid_scope"):
		return "invalid_scope"
	case strings.Contains(text, "invalid_evidence"):
		return "invalid_evidence"
	case strings.Contains(text, "invalid_anchor"):
		return "invalid_anchor"
	case strings.Contains(text, "missing_precondition"):
		return "missing_precondition"
	default:
		return "unknown"
	}
}

func (s *Server) selectFailureProof(ctx context.Context, targetRoot string, query semantic.FailureQueryV2, args map[string]any) (*failureProofSelection, map[string]any) {
	if query.Freshness == "current" {
		st, err := s.getStorage(targetRoot)
		if err != nil {
			return nil, failureStatus("unknown", "validated current proof storage is unavailable", err)
		}
		bundle, err := st.ReadValidatedActiveProofBundle()
		if err != nil {
			return nil, failureStatus("blocked", "validated current proof is unavailable", err)
		}
		if bundle == nil || bundle.Manifest == nil || bundle.Pointer == nil {
			return nil, failureStatus("missing_precondition", "no validated current proof is published", nil)
		}
		engine, err := s.getSnapshotEngine(targetRoot)
		if err != nil {
			return nil, failureStatus("unknown", "live workspace head is unavailable", err)
		}
		// ReconcileIfChanged recaptures direct worktree edits but preserves the
		// existing immutable head for an unchanged tree. A plain Reconcile would
		// manufacture a new identity on every read and invalidate a valid proof.
		head, err := engine.ReconcileIfChanged(ctx, nil)
		if err != nil || head == nil {
			return nil, failureStatus("unknown", "live workspace head is unavailable", err)
		}
		if bundle.Pointer.ExpectedLiveHeadSnapshotID != head.SnapshotID || bundle.Pointer.ValidatedAgainstSnapshotID != head.SnapshotID {
			return nil, failureStatus("stale_live_head", "validated current proof does not match the live workspace head", nil)
		}
		var mapIR semantic.SemanticMapIR
		if err := json.Unmarshal(bundle.SemanticMap, &mapIR); err != nil {
			return nil, failureStatus("invalid_graph", "validated current semantic map could not be decoded", err)
		}
		if err := contractharness.ValidateSemanticMapIR(bundle.SemanticMap); err != nil {
			return nil, failureStatus("invalid_graph", "validated current semantic map failed contract validation", err)
		}
		if err := exactFailureIdentity(query, &mapIR, bundle.Manifest, bundle.Pointer); err != nil {
			return nil, failureStatus(failureErrorCode(err), "failure query does not match the validated current proof", err)
		}
		// VS04 persists the computation map as historical. Current authority is
		// established by the validated proof and live-head match above. Project
		// freshness only on this defensive in-memory copy so the v2 semantic
		// seam can bind its trace to the current query without rewriting storage.
		mapIR.Freshness = "current"
		return &failureProofSelection{Map: &mapIR, Manifest: bundle.Manifest, Pointer: bundle.Pointer, Current: true}, nil
	}

	mapIR, err := decodeExplicitSemanticMap(args)
	if err != nil {
		return nil, failureStatus("missing_precondition", "historical failure queries require an explicit semantic map", err)
	}
	manifest, pointer, err := decodeExplicitProof(args)
	if err != nil {
		return nil, failureStatus("missing_precondition", "historical failure queries require an explicit proof manifest", err)
	}
	if err := contractharness.ValidateSemanticMapIR(mustJSON(mapIR)); err != nil {
		return nil, failureStatus("invalid_graph", "explicit historical semantic map failed contract validation", err)
	}
	if err := contractharness.ValidateGenerationProofManifestV2(mustJSON(manifest)); err != nil {
		return nil, failureStatus("invalid_precondition", "explicit historical proof manifest failed contract validation", err)
	}
	if pointer != nil {
		if err := contractharness.ValidateActivePointerV2(mustJSON(pointer)); err != nil {
			return nil, failureStatus("invalid_precondition", "explicit historical proof pointer failed contract validation", err)
		}
	}
	if err := exactFailureIdentity(query, mapIR, manifest, pointer); err != nil {
		return nil, failureStatus(failureErrorCode(err), "historical map/proof identity does not match the failure query", err)
	}
	return &failureProofSelection{Map: mapIR, Manifest: manifest, Pointer: pointer}, nil
}

func exactFailureIdentity(query semantic.FailureQueryV2, mapIR *semantic.SemanticMapIR, manifest *storage.GenerationProofManifest, pointer *storage.ActivePointer) error {
	if mapIR == nil || manifest == nil {
		return errors.New("missing_precondition: map and proof manifest are required")
	}
	if mapIR.SchemaID != semantic.SemanticMapSchemaID || mapIR.SchemaVersion != semantic.SemanticSchemaVersion {
		return errors.New("invalid_graph: semantic map schema identity is not canonical")
	}
	if mapIR.ComputedBasisID != query.ComputedBasisID || mapIR.GenerationID != query.GenerationID || mapIR.ValidatedAgainstSnapshotID != query.ValidatedAgainstSnapshotID {
		return errors.New("incomparable_basis: map identity does not match query basis, generation, snapshot, or freshness")
	}
	if query.Freshness == "historical" && mapIR.Freshness != "historical" {
		return errors.New("incomparable_basis: historical query requires a historical semantic map")
	}
	if query.Freshness == "current" && mapIR.Freshness != "historical" && mapIR.Freshness != "current" {
		return errors.New("invalid_graph: current proof semantic map freshness is not canonical")
	}
	if query.Freshness != "current" && query.Freshness != "historical" {
		return errors.New("invalid_precondition: unsupported failure query freshness")
	}
	if manifest.GenerationID != query.GenerationID || manifest.ComputedBasisID != query.ComputedBasisID || manifest.ValidatedAgainstSnapshotID != query.ValidatedAgainstSnapshotID {
		if query.Freshness == "current" {
			// Current queries identify the immutable map computation snapshot.
			// The manifest's ValidatedAgainstSnapshotID identifies the live head,
			// which was checked against the pointer and reconciled head before this
			// identity comparison.
			if manifest.GenerationID != query.GenerationID || manifest.ComputedBasisID != query.ComputedBasisID {
				return errors.New("incomparable_basis: proof manifest identity does not match query")
			}
		} else {
			return errors.New("incomparable_basis: proof manifest identity does not match query")
		}
	}
	if manifest.ComputedSnapshotID == "" || manifest.ComputedSnapshotID != mapIR.ValidatedAgainstSnapshotID || manifest.ComputedSnapshotID != query.ValidatedAgainstSnapshotID {
		return errors.New("incomparable_basis: proof computed snapshot does not match semantic map snapshot")
	}
	if pointer != nil {
		if pointer.GenerationID != query.GenerationID || pointer.ComputedBasisID != query.ComputedBasisID {
			return errors.New("incomparable_basis: proof pointer identity does not match query")
		}
		if query.Freshness == "historical" && pointer.ValidatedAgainstSnapshotID != query.ValidatedAgainstSnapshotID {
			return errors.New("incomparable_basis: proof pointer identity does not match query")
		}
		if pointer.ExpectedLiveHeadSnapshotID != manifest.ExpectedLiveHeadSnapshotID || pointer.WorkspaceEpoch != manifest.WorkspaceEpoch || pointer.TaskIntentRevision != manifest.TaskIntentRevision || pointer.NormalizedQueryHash != manifest.NormalizedQueryHash {
			return errors.New("incomparable_basis: proof pointer and manifest identities differ")
		}
		if pointer.ManifestObjectRef == "" {
			return errors.New("invalid_precondition: historical proof pointer is incomplete")
		}
	}
	return nil
}

func decodeExplicitSemanticMap(args map[string]any) (*semantic.SemanticMapIR, error) {
	raw := firstArgument(args, "semanticMap", "map")
	if raw == nil {
		return nil, errors.New("semanticMap is required")
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	var mapIR semantic.SemanticMapIR
	if err := json.Unmarshal(data, &mapIR); err != nil {
		return nil, err
	}
	return &mapIR, nil
}

func decodeExplicitProof(args map[string]any) (*storage.GenerationProofManifest, *storage.ActivePointer, error) {
	raw := firstArgument(args, "proof", "proofManifest", "generationProof")
	if raw == nil {
		return nil, nil, errors.New("proof is required")
	}
	proofObject, _ := raw.(map[string]any)
	if obj, ok := raw.(map[string]any); ok {
		if nested, present := obj["manifest"]; present {
			raw = nested
		}
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return nil, nil, err
	}
	var manifest storage.GenerationProofManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, nil, err
	}

	var rawPointer any
	if proofObject != nil {
		rawPointer = proofObject["pointer"]
		if rawPointer == nil {
			rawPointer = proofObject["activePointer"]
		}
	}
	if rawPointer == nil {
		rawPointer = firstArgument(args, "pointer", "activePointer")
	}
	if rawPointer == nil {
		// Historical consumers need the immutable map and proof manifest
		// identities, but do not need an active pointer. Current consumers use
		// the storage bundle above and always have a strict pointer.
		return &manifest, nil, nil
	}
	pointerData, err := json.Marshal(rawPointer)
	if err != nil {
		return nil, nil, err
	}
	var pointer storage.ActivePointer
	if err := json.Unmarshal(pointerData, &pointer); err != nil {
		return nil, nil, err
	}
	return &manifest, &pointer, nil
}

func firstArgument(args map[string]any, names ...string) any {
	for _, name := range names {
		if value, ok := args[name]; ok && value != nil {
			return value
		}
	}
	return nil
}

func mustJSON(value any) []byte {
	data, _ := json.Marshal(value)
	return data
}

func (s *Server) resolveFailureObservation(ctx context.Context, targetRoot string, query semantic.FailureQueryV2) (*semantic.RuntimeObservationV2, map[string]any) {
	id := query.Incident.RuntimeObservationID
	request := RuntimeObservationRequest{ObservationID: id, Query: query, TargetRoot: targetRoot}

	provider := firstConfigured(s.cfg.RuntimeObservationProvider, s.cfg.ObservationProvider)
	if !isNilValue(provider) {
		observation, err := callObservationProvider(ctx, provider, request)
		if err != nil {
			return nil, failureStatus("unknown", "trusted runtime observation provider failed", err)
		}
		return validateResolvedObservation(observation, query)
	}
	store := firstConfigured(s.cfg.RuntimeObservationStore, s.cfg.ObservationStore)
	if !isNilValue(store) {
		observation, err := callObservationStore(ctx, store, id)
		if err != nil {
			return nil, failureStatus("unknown", "trusted runtime observation store failed", err)
		}
		return validateResolvedObservation(observation, query)
	}
	return nil, failureStatus("unknown", "trusted runtime observation provider/store is unavailable", nil)
}

func validateResolvedObservation(observation *semantic.RuntimeObservationV2, query semantic.FailureQueryV2) (*semantic.RuntimeObservationV2, map[string]any) {
	if observation == nil {
		return nil, failureStatus("unknown", "trusted runtime observation resolved to nil", nil)
	}
	if query.Incident == nil || observation.ObservationID != query.Incident.RuntimeObservationID {
		return nil, failureStatus("incomparable_basis", "trusted runtime observation identity does not match requested observationId", nil)
	}
	data := mustJSON(observation)
	if err := contractharness.ValidateRuntimeObservationV2(data); err != nil {
		code := "invalid_evidence"
		if observation.IsolationLevel == "trusted_local" {
			code = "blocked"
		}
		return nil, failureStatus(code, "trusted runtime observation failed contract validation", err)
	}
	if err := semantic.ValidateRuntimeObservationV2(*observation); err != nil {
		code := failureErrorCode(err)
		if observation.IsolationLevel == "trusted_local" && code == "unknown" {
			code = "blocked"
		}
		return nil, failureStatus(code, "trusted runtime observation failed semantic validation", err)
	}
	incident := query.Incident
	if incident.TraceID != "" && observation.TraceID != incident.TraceID {
		return nil, failureStatus("incomparable_basis", "trusted runtime observation traceId does not match incident query", nil)
	}
	if incident.IncidentEvidenceID != "" && observation.IncidentEvidenceID != incident.IncidentEvidenceID {
		return nil, failureStatus("incomparable_basis", "trusted runtime observation incident Evidence does not match incident query", nil)
	}
	if observation.ComputedBasisID != query.ComputedBasisID || observation.GenerationID != query.GenerationID || observation.ValidatedAgainstSnapshotID != query.ValidatedAgainstSnapshotID || observation.Freshness != query.Freshness {
		return nil, failureStatus("incomparable_basis", "trusted runtime observation identity does not match incident query", nil)
	}
	if observation.Scenario != incident.Scenario || observation.Environment != incident.Environment || observation.DependencyFingerprint != incident.DependencyFingerprint || observation.TimeWindow != incident.TimeWindow {
		return nil, failureStatus("invalid_scope", "trusted runtime observation does not match incident scope", nil)
	}
	redacted, err := redactJSON(data)
	if err != nil {
		return nil, failureStatus("blocked", "trusted runtime observation failed redaction", err)
	}
	var clean semantic.RuntimeObservationV2
	if err := json.Unmarshal(redacted, &clean); err != nil {
		return nil, failureStatus("blocked", "redacted runtime observation could not be decoded", err)
	}
	// Redaction is a transformation at a public boundary. Re-run both the
	// wire-contract and semantic validators after it so a redaction-induced
	// invalid anchor or identity cannot escape through MCP.
	if err := contractharness.ValidateRuntimeObservationV2(redacted); err != nil {
		return nil, failureStatus("blocked", "redacted runtime observation failed contract validation", err)
	}
	if err := semantic.ValidateRuntimeObservationV2(clean); err != nil {
		return nil, failureStatus("blocked", "redacted runtime observation failed semantic validation", err)
	}
	return &clean, nil
}

func callObservationProvider(ctx context.Context, provider any, request RuntimeObservationRequest) (*semantic.RuntimeObservationV2, error) {
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

func callObservationStore(ctx context.Context, store any, id string) (*semantic.RuntimeObservationV2, error) {
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

func firstConfigured(values ...any) any {
	for _, value := range values {
		if !isNilValue(value) {
			return value
		}
	}
	return nil
}

func isNilValue(value any) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	default:
		return false
	}
}

func (s *Server) executeTrustedLocal(ctx context.Context, targetRoot string, selection *failureProofSelection, query semantic.FailureQueryV2, observation *semantic.RuntimeObservationV2, args map[string]any) (*oneshoRuntime.RuntimeIsolationResult, map[string]any) {
	rawConsent := firstArgument(args, "runtimeConsent", "consent")
	if rawConsent == nil && s.cfg.RuntimeConsent != nil {
		rawConsent = s.cfg.RuntimeConsent
	}
	if rawConsent == nil {
		return nil, failureStatus("blocked", "trusted_local execution requires an exact RuntimeConsentV1", nil)
	}
	consentData, err := json.Marshal(rawConsent)
	if err != nil {
		return nil, failureStatus("blocked", "runtime consent is not valid JSON", err)
	}
	if err := contractharness.ValidateRuntimeConsentV1(consentData); err != nil {
		return nil, failureStatus("blocked", "runtime consent is not a valid RuntimeConsentV1", err)
	}
	var consent oneshoRuntime.RuntimeConsentV1
	if err := json.Unmarshal(consentData, &consent); err != nil {
		return nil, failureStatus("blocked", "runtime consent could not be decoded", err)
	}
	disclosureConsent := &consent
	if s.cfg.RuntimeConsent != nil {
		// When a caller supplied a consent that does not match the server's
		// approved consent, disclose only the server-approved command/boundary.
		disclosureConsent = s.cfg.RuntimeConsent
	}
	blocked := func(message string, cause error) (*oneshoRuntime.RuntimeIsolationResult, map[string]any) {
		return nil, failureStatusWithConsent("blocked", message, cause, disclosureConsent)
	}
	if s.cfg.RuntimeConsent != nil && !reflect.DeepEqual(consent, *s.cfg.RuntimeConsent) {
		return blocked("runtime consent does not match the server-approved consent", nil)
	}
	if err := consent.Validate(time.Now().UTC()); err != nil {
		return blocked("runtime consent is not currently valid", err)
	}
	if observation == nil || observation.TrustedLocalApproval == nil || !observation.TrustedLocalApproval.Approved {
		return blocked("trusted_local execution requires server-validated approval", nil)
	}
	if observation.TrustedLocalApproval.ApprovedBy != consent.ActorID {
		return blocked("runtime consent actor does not match trusted_local approval", nil)
	}

	executor := firstConfigured(s.cfg.RuntimeExecutor, s.cfg.OneShotExecutor)
	if isNilValue(executor) {
		return blocked("trusted_local one-shot executor is unavailable", nil)
	}
	snapshot, release, err := s.snapshotForFailureExecution(ctx, targetRoot, selection)
	if err != nil {
		return blocked("immutable execution snapshot is unavailable", err)
	}
	defer release()

	runtimeInput := decodeRuntimeInput(args)
	nonce := runtimeInput.Nonce
	if nonce == "" {
		nonce = consent.Nonce
	}
	if nonce != consent.Nonce {
		return blocked("runtime consent nonce does not match the execution request", nil)
	}
	if !reflect.DeepEqual(s.cfg.RuntimeExecutionSpec, oneshoRuntime.RuntimeExecutionSpec{}) {
		if err := consent.Matches(s.cfg.RuntimeExecutionSpec, snapshot.SnapshotID, snapshot.RootTreeID, nonce); err != nil {
			return blocked("runtime consent does not match configured command or isolation scope", err)
		}
	}
	req := oneshoRuntime.ExecutionRequest{
		ExecutionID:          runtimeInput.ExecutionID,
		Nonce:                nonce,
		Consent:              consent,
		Snapshot:             snapshot,
		Operation:            runtimeInput.Operation,
		Params:               runtimeInput.Params,
		TaskScope:            runtimeInput.TaskScope,
		RequiredObservations: runtimeInput.RequiredObservations,
		CapabilityRequest:    runtimeInput.CapabilityRequest,
		Payload:              runtimeInput.Payload,
	}
	result, execErr := callRuntimeExecutor(ctx, executor, req)
	isolation := result.Isolation
	if err := validateIsolationResult(isolation); err != nil {
		if execErr != nil {
			return nil, failureStatus("blocked", "trusted_local executor failed before promotion", fmt.Errorf("%v: %w", execErr, err))
		}
		return nil, failureStatus("blocked", "trusted_local isolation result is not valid", err)
	}
	if err := matchIsolationIdentity(isolation, consent, snapshot, nonce); err != nil {
		return nil, failureStatusWithIsolation("blocked", "trusted_local isolation result does not match the exact consent or immutable snapshot", err, isolation)
	}
	if execErr != nil || isolation.EvidencePromotion != oneshoRuntime.RuntimePromotionEligible {
		message := "trusted_local runtime evidence promotion is blocked"
		if execErr != nil {
			message = "trusted_local executor returned a terminal error"
		}
		return nil, failureStatusWithIsolation("blocked", message, execErr, isolation)
	}
	return &isolation, nil
}

type runtimeInput struct {
	ExecutionID          string
	Nonce                string
	Operation            string
	Params               map[string]any
	Payload              map[string]any
	TaskScope            []string
	RequiredObservations []string
	CapabilityRequest    []string
}

func decodeRuntimeInput(args map[string]any) runtimeInput {
	in := runtimeInput{Params: map[string]any{}, Payload: map[string]any{}}
	raw, _ := args["runtime"].(map[string]any)
	getString := func(name string) string {
		if value, ok := raw[name].(string); ok {
			return value
		}
		if value, ok := args[name].(string); ok {
			return value
		}
		return ""
	}
	in.ExecutionID = getString("executionId")
	in.Nonce = getString("nonce")
	in.Operation = getString("operation")
	if value, ok := raw["params"].(map[string]any); ok {
		in.Params = value
	}
	if value, ok := raw["payload"].(map[string]any); ok {
		in.Payload = value
	}
	if value, ok := raw["taskScope"].([]any); ok {
		in.TaskScope = stringsFromAny(value)
	}
	if value, ok := raw["requiredObservations"].([]any); ok {
		in.RequiredObservations = stringsFromAny(value)
	}
	if value, ok := raw["capabilityRequest"].([]any); ok {
		in.CapabilityRequest = stringsFromAny(value)
	}
	return in
}

func stringsFromAny(values []any) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if text, ok := value.(string); ok && text != "" {
			out = append(out, text)
		}
	}
	return out
}

func callRuntimeExecutor(ctx context.Context, executor any, request oneshoRuntime.ExecutionRequest) (oneshoRuntime.ExecutionResult, error) {
	switch e := executor.(type) {
	case RuntimeOneShotExecutor:
		return e.Execute(ctx, request)
	case func(context.Context, oneshoRuntime.ExecutionRequest) (oneshoRuntime.ExecutionResult, error):
		return e(ctx, request)
	case interface {
		ExecuteOneShot(context.Context, oneshoRuntime.ExecutionRequest) (oneshoRuntime.ExecutionResult, error)
	}:
		return e.ExecuteOneShot(ctx, request)
	default:
		return oneshoRuntime.ExecutionResult{}, fmt.Errorf("unsupported runtime executor type %T", executor)
	}
}

func (s *Server) snapshotForFailureExecution(ctx context.Context, targetRoot string, selection *failureProofSelection) (protocol.Snapshot, func(), error) {
	engine, err := s.getSnapshotEngine(targetRoot)
	if err != nil {
		return protocol.Snapshot{}, nil, err
	}
	snapshotID := selection.Map.ValidatedAgainstSnapshotID
	if selection.Manifest != nil && selection.Manifest.ComputedSnapshotID != "" {
		snapshotID = selection.Manifest.ComputedSnapshotID
	}
	lease, err := engine.SnapshotVFS(snapshotID)
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

func validateIsolationResult(result oneshoRuntime.RuntimeIsolationResult) error {
	data := mustJSON(result)
	if err := contractharness.ValidateRuntimeIsolationResultV1(data); err != nil {
		return err
	}
	return result.Validate()
}

func matchIsolationIdentity(result oneshoRuntime.RuntimeIsolationResult, consent oneshoRuntime.RuntimeConsent, snapshot protocol.Snapshot, nonce string) error {
	if result.ConsentID != consent.ConsentID || result.ActorID != consent.ActorID || result.Nonce != nonce {
		return errors.New("runtime isolation result consent identity differs from the approved consent")
	}
	if result.SnapshotID != snapshot.SnapshotID || result.SnapshotTreeDigest != snapshot.RootTreeID || result.RecomputedTreeDigest != snapshot.RootTreeID {
		return errors.New("runtime isolation result snapshot identity differs from immutable execution input")
	}
	if result.Command != consent.Command || !reflect.DeepEqual(result.Args, consent.Args) || result.CommandDigest != oneshoRuntime.CommandDigest(consent.Command, consent.Args) {
		return errors.New("runtime isolation result command differs from approved command")
	}
	if result.AccessScope != consent.AccessScope || result.IsolationScope != consent.IsolationScope {
		return errors.New("runtime isolation result access or isolation scope differs from approved scope")
	}
	return nil
}

func failureStatusWithIsolation(code, message string, cause error, isolation oneshoRuntime.RuntimeIsolationResult) map[string]any {
	doc := failureStatus(code, message, cause)
	if redacted, err := validateAndRedactIsolation(isolation); err == nil {
		doc["runtimeIsolation"] = redacted
		doc["command"] = redacted.Command
		doc["args"] = redacted.Args
		doc["accessScope"] = redacted.AccessScope
		doc["isolationScope"] = redacted.IsolationScope
	}
	return doc
}

func validateAndRedactIsolation(result oneshoRuntime.RuntimeIsolationResult) (oneshoRuntime.RuntimeIsolationResult, error) {
	if err := validateIsolationResult(result); err != nil {
		return oneshoRuntime.RuntimeIsolationResult{}, err
	}
	clean, err := redactJSON(mustJSON(result))
	if err != nil {
		return oneshoRuntime.RuntimeIsolationResult{}, err
	}
	var out oneshoRuntime.RuntimeIsolationResult
	if err := json.Unmarshal(clean, &out); err != nil {
		return oneshoRuntime.RuntimeIsolationResult{}, err
	}
	if err := validateIsolationResult(out); err != nil {
		return oneshoRuntime.RuntimeIsolationResult{}, err
	}
	return out, nil
}

func validateAndRedactFailureTrace(trace *semantic.FailurePathTraceV2) (*semantic.FailurePathTraceV2, error) {
	if trace == nil {
		return nil, errors.New("failure trace is nil")
	}
	if err := semantic.ValidateFailurePathTraceV2(*trace); err != nil {
		return nil, err
	}
	clean, err := redactJSON(mustJSON(trace))
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
	return &out, nil
}

// marshalPublicMCPResult is the final JSON egress gate for failure traces.
// The semantic Go type keeps source compatibility with v1 callers and uses
// omitempty on a few slices, while the v2 wire contract requires those arrays
// to be present even when they are empty. Add only the required empty arrays
// here, after semantic validation and redaction, without fabricating evidence.
func marshalPublicMCPResult(value any) ([]byte, error) {
	if trace, ok := value.(*semantic.FailurePathTraceV2); ok {
		return marshalPublicFailureTrace(trace)
	}
	if enrichment, ok := value.(*semantic.EnrichmentResult); ok {
		return marshalPublicEnrichmentResult(enrichment)
	}
	if enrichment, ok := value.(semantic.EnrichmentResult); ok {
		return marshalPublicEnrichmentResult(&enrichment)
	}
	if envelope, ok := value.(map[string]any); ok {
		if trace, ok := envelope["trace"].(*semantic.FailurePathTraceV2); ok {
			copyEnvelope := make(map[string]any, len(envelope))
			for key, item := range envelope {
				copyEnvelope[key] = item
			}
			traceJSON, err := marshalPublicFailureTrace(trace)
			if err != nil {
				return nil, err
			}
			var publicTrace map[string]any
			if err := json.Unmarshal(traceJSON, &publicTrace); err != nil {
				return nil, err
			}
			copyEnvelope["trace"] = publicTrace
			return json.Marshal(copyEnvelope)
		}
	}
	// Onboarding is another public Evidence boundary. Inspect the exact schema
	// identity and reuse FlowView's redaction-then-v2-validation gate. A broad
	// substring match would allow an unregistered shape to bypass validation.
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var object map[string]any
	if err := json.Unmarshal(raw, &object); err == nil {
		schemaID, _ := object["schemaId"].(string)
		switch schemaID {
		case semantic.DomainOverviewSchemaID, semantic.RepresentativeFlowCatalogSchemaID, semantic.OnboardingFlowDrilldownSchemaID:
			sanitized, err := flowview.RedactAndValidateOnboardingJSON(value)
			if err != nil {
				return nil, err
			}
			return json.Marshal(sanitized)
		}
	}
	return raw, nil
}

func marshalPublicEnrichmentResult(result *semantic.EnrichmentResult) ([]byte, error) {
	return semantic.MarshalEnrichmentResultEgress(result)
}

func marshalPublicFailureTrace(trace *semantic.FailurePathTraceV2) ([]byte, error) {
	if trace == nil {
		return nil, errors.New("failure trace is nil")
	}
	if err := semantic.ValidateFailurePathTraceV2(*trace); err != nil {
		return nil, err
	}
	clean, err := redactJSON(mustJSON(trace))
	if err != nil {
		return nil, err
	}
	var doc map[string]any
	if err := json.Unmarshal(clean, &doc); err != nil {
		return nil, err
	}
	for _, field := range []string{"unknownFrontier", "recoveryStates"} {
		if _, present := doc[field]; !present {
			doc[field] = []any{}
		}
	}
	clean, err = json.Marshal(doc)
	if err != nil {
		return nil, err
	}
	if err := contractharness.ValidateFailurePathTraceV2(clean); err != nil {
		return nil, err
	}
	return clean, nil
}

func redactJSON(data []byte) ([]byte, error) {
	clean, _, err := secret.RedactJSON(data)
	if err != nil {
		return nil, err
	}
	return clean, nil
}
