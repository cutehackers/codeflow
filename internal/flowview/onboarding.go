package flowview

// This file owns the public onboarding projection.  Onboarding is deliberately
// a read-only projection over the published generation index and the canonical
// semantic map.  It must never manufacture a domain, flow, basis, or evidence
// record when the repository has not supplied those facts.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"codeflow/internal/contractharness"
	"codeflow/internal/fusion"
	"codeflow/internal/secret"
	"codeflow/internal/semantic"
	"codeflow/internal/slicing"
	"codeflow/internal/storage"
)

// Public onboarding identifiers are aliases of the semantic package contract.
// FlowView owns transport parsing and repository/basis resolution only. The
// semantic package owns the v2 projection, ranking and public fact model.
const (
	OnboardingQuerySchemaID          = semantic.OnboardingQuerySchemaID
	OnboardingOverviewSchemaID       = semantic.DomainOverviewSchemaID
	OnboardingCandidateSchemaID      = semantic.DomainCandidateSchemaID
	OnboardingCatalogSchemaID        = semantic.RepresentativeFlowCatalogSchemaID
	OnboardingCandidateSchemaVersion = semantic.SemanticSchemaVersion
)

// Compatibility aliases keep the HTTP/MCP seam source-compatible while
// ensuring the returned JSON is produced by semantic's v2 projector.
type OnboardingOverview = semantic.DomainOverviewV2
type OnboardingCatalog = semantic.RepresentativeFlowCatalogV2

// OnboardingRequest is the normalized public request for both MCP and HTTP.
// Current requests must carry exact proof identity. Historical requests must
// carry an exact generation, basis, and snapshot identity.
type OnboardingRequest struct {
	RepositoryID        string
	Domain              string
	Level               int
	MaxVisibleCoreSteps int
	// MaxVisibleCoreStepsProvided distinguishes an omitted optional budget
	// from an explicit zero. The latter is invalid at every public seam.
	MaxVisibleCoreStepsProvided bool
	DisplayBudget               *semantic.DisplayBudget
	Freshness                   string
	ComputedBasisID             string
	GenerationID                string
	SnapshotID                  string
}

type onboardingFlowSource struct {
	summary storage.FlowSummary
	spec    *fusion.FlowSpec
	entry   string
	module  string
	refs    []string
	steps   int
	unknown int
	stale   int
}

type onboardingBasis struct {
	query        semantic.OnboardingQueryV2
	mapIR        *semantic.SemanticMapIR
	currentProof *semantic.GenerationProofManifest
	flows        []onboardingFlowSource
}

// ExploreOnboarding is the shared MCP/FlowView seam.  It returns a v2 public
// projection and does not expose a mutable semantic map or a source snapshot.
func (s *Server) ExploreOnboarding(ctx context.Context, req OnboardingRequest) (any, error) {
	if req.MaxVisibleCoreStepsProvided || req.MaxVisibleCoreSteps != 0 {
		if req.MaxVisibleCoreSteps <= 0 {
			return nil, errors.New("invalid_precondition: maxVisibleCoreSteps must be a positive integer")
		}
	}
	budget := semantic.DefaultDisplayBudget()
	if req.DisplayBudget != nil {
		if req.DisplayBudget.TargetMin < 1 || req.DisplayBudget.TargetMax < 1 || req.DisplayBudget.TargetMax < req.DisplayBudget.TargetMin || req.DisplayBudget.Enforcement != "soft" {
			return nil, errors.New("invalid_precondition: displayBudget must be a complete soft budget")
		}
		budget = *req.DisplayBudget
	} else {
		var budgetErr error
		budget, budgetErr = semantic.NormalizeDisplayBudget(req.MaxVisibleCoreSteps)
		if budgetErr != nil {
			return nil, budgetErr
		}
	}
	basis, err := s.resolveOnboardingBasis(ctx, req)
	if err != nil {
		return nil, err
	}
	if req.Level <= 0 {
		req.Level = 1
	}
	if req.Level != 1 && req.Level != 2 {
		return nil, fmt.Errorf("invalid_precondition: onboarding level must be 1 or 2")
	}

	query := basis.query
	query.Level = req.Level
	query.Domain = strings.TrimSpace(req.Domain)
	query.DisplayBudget = &budget
	candidates := onboardingCandidates(basis)
	request := semantic.OnboardingRequestV2{Query: query, Map: basis.mapIR, Candidates: candidates, CurrentProof: basis.currentProof}
	if req.Level == 2 {
		if strings.TrimSpace(req.Domain) == "" {
			return nil, errors.New("missing_precondition: level 2 onboarding requires a domain filter")
		}
		return semantic.GetRepresentativeFlowCatalogV2(request, req.Domain)
	}
	return semantic.ExploreDomainsV2(request)
}

func (s *Server) resolveOnboardingBasis(ctx context.Context, req OnboardingRequest) (*onboardingBasis, error) {
	repoID := strings.TrimSpace(req.RepositoryID)
	if repoID == "" {
		return nil, errors.New("missing_precondition: repositoryId is required")
	}
	freshness := strings.ToLower(strings.TrimSpace(req.Freshness))
	if freshness == "" {
		return nil, errors.New("missing_precondition: freshness is required and must be current or historical")
	}
	if freshness != "current" && freshness != "historical" {
		return nil, errors.New("invalid_precondition: freshness must be current or historical")
	}
	if s == nil || s.storage == nil || s.engine == nil {
		return nil, errors.New("unavailable: onboarding workspace is not initialized")
	}

	if freshness == "current" {
		return s.resolveCurrentOnboardingBasis(ctx, req, repoID)
	}
	return s.resolveHistoricalOnboardingBasis(req, repoID)
}

func (s *Server) resolveCurrentOnboardingBasis(ctx context.Context, req OnboardingRequest, repoID string) (*onboardingBasis, error) {
	// ReconcileIfChanged preserves a valid proof when the tree is unchanged,
	// while still measuring direct worktree edits before validating current.
	head, err := s.engine.ReconcileIfChanged(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("reconcile live head: %w", err)
	}
	bundle, err := s.storage.ReadValidatedActiveProofBundle()
	if err != nil {
		return nil, fmt.Errorf("current_proof_unavailable: %w", err)
	}
	if bundle == nil || bundle.Manifest == nil || bundle.Pointer == nil {
		return nil, errors.New("missing_precondition: no validated current proof is published")
	}
	// The active proof is the authority for current identity. Callers may use
	// basisSelector.kind=active without repeating these IDs, but any IDs they
	// do provide are compared below and cannot override the proof.
	if req.ComputedBasisID == "" {
		req.ComputedBasisID = bundle.Pointer.ComputedBasisID
	}
	if req.GenerationID == "" {
		req.GenerationID = bundle.Pointer.GenerationID
	}
	if req.SnapshotID == "" {
		req.SnapshotID = bundle.Pointer.ValidatedAgainstSnapshotID
	}
	if req.ComputedBasisID == "" || req.GenerationID == "" || req.SnapshotID == "" {
		return nil, errors.New("missing_precondition: current proof identity is incomplete")
	}
	liveHead := s.engine.LiveHead()
	if liveHead == nil || head == nil {
		return nil, errors.New("current_proof_unavailable: live workspace head is unavailable")
	}
	if bundle.Pointer.ExpectedLiveHeadSnapshotID == "" || liveHead.SnapshotID != bundle.Pointer.ExpectedLiveHeadSnapshotID || head.SnapshotID != bundle.Pointer.ExpectedLiveHeadSnapshotID {
		return nil, fmt.Errorf("stale_live_head: current proof expected live head %q but workspace live head is %q", bundle.Pointer.ExpectedLiveHeadSnapshotID, liveHead.SnapshotID)
	}
	if bundle.Pointer.RepositoryID != "" && bundle.Pointer.RepositoryID != repoID {
		return nil, errors.New("incomparable_basis: repositoryId does not match the current proof")
	}
	if bundle.Pointer.ComputedBasisID != req.ComputedBasisID || bundle.Pointer.GenerationID != req.GenerationID || bundle.Pointer.ValidatedAgainstSnapshotID != req.SnapshotID {
		return nil, errors.New("incomparable_basis: requested identity does not match the validated current proof")
	}

	var mapIR semantic.SemanticMapIR
	if err := json.Unmarshal(bundle.SemanticMap, &mapIR); err != nil {
		return nil, fmt.Errorf("invalid_graph: decode current semantic map: %w", err)
	}
	if err := contractharness.ValidateSemanticMapIR(bundle.SemanticMap); err != nil {
		return nil, fmt.Errorf("invalid_graph: current semantic map contract: %w", err)
	}
	if mapIR.GenerationID != req.GenerationID || mapIR.ComputedBasisID != req.ComputedBasisID || mapIR.ValidatedAgainstSnapshotID != req.SnapshotID {
		return nil, errors.New("incomparable_basis: semantic map identity does not match the current proof")
	}
	idx, err := s.storage.ReadLatestIndex()
	if err != nil {
		return nil, fmt.Errorf("read current generation index: %w", err)
	}
	if idx != nil && idx.GenerationID != req.GenerationID {
		return nil, errors.New("incomparable_basis: generation index does not match the current proof")
	}
	flows, err := collectOnboardingFlows(s.storage, idx, &mapIR)
	if err != nil {
		return nil, err
	}
	return &onboardingBasis{
		query: semantic.OnboardingQueryV2{
			SchemaID: semantic.OnboardingQuerySchemaID, SchemaVersion: semantic.SemanticSchemaVersion,
			RepositoryID: repoID, ComputedBasisID: req.ComputedBasisID, GenerationID: req.GenerationID,
			ValidatedAgainstSnapshotID: req.SnapshotID, Freshness: "current", Level: 1,
		},
		mapIR: &mapIR, currentProof: semanticProofForOnboarding(bundle.Manifest), flows: flows,
	}, nil
}

// semanticProofForOnboarding converts the storage proof value into semantic's
// validation model without exposing storage internals at the public seam.
func semanticProofForOnboarding(manifest *storage.GenerationProofManifest) *semantic.GenerationProofManifest {
	if manifest == nil {
		return nil
	}
	return &semantic.GenerationProofManifest{
		SchemaID: manifest.SchemaID, SchemaVersion: manifest.SchemaVersion, ProofID: manifest.ProofID,
		GenerationID: manifest.GenerationID, ComputedBasisID: manifest.ComputedBasisID,
		ComputedSnapshotID: manifest.ComputedSnapshotID, ValidatedAgainstSnapshotID: manifest.ValidatedAgainstSnapshotID,
		ValidatedWorkspaceDeltaID: manifest.ValidatedWorkspaceDeltaID, TaskIntentRevision: manifest.TaskIntentRevision,
		NormalizedQueryHash: manifest.NormalizedQueryHash, AnalysisReadSetID: manifest.AnalysisReadSetID,
		CausalObservationClosureID: manifest.CausalObservationClosureID, CausalObservationClosureDigest: manifest.CausalObservationClosureDigest,
		CapabilityProfileDigest: manifest.CapabilityProfileDigest, WorkspaceEpoch: manifest.WorkspaceEpoch,
		CurrentPublication: semantic.CurrentPublicationResult{
			Eligibility: manifest.CurrentPublication.Eligibility, SnapshotGate: manifest.CurrentPublication.SnapshotGate,
			ClosureGate: manifest.CurrentPublication.ClosureGate, EvidenceGate: manifest.CurrentPublication.EvidenceGate,
			SemanticAtomicityGate: manifest.CurrentPublication.SemanticAtomicityGate,
			TaskRelevanceGate:     manifest.CurrentPublication.TaskRelevanceGate, ComprehensionGate: manifest.CurrentPublication.ComprehensionGate,
		},
		SettlementEvaluation: semantic.SettlementEvaluation{
			Gate: manifest.SettlementEvaluation.Gate, EvaluatedAt: manifest.SettlementEvaluation.EvaluatedAt,
			BlockingObligationRefs: append([]string(nil), manifest.SettlementEvaluation.BlockingObligationRefs...),
		},
		ArtifactRefs: semantic.ArtifactRefs{
			SemanticMap: manifest.ArtifactRefs.SemanticMap, SemanticDelta: manifest.ArtifactRefs.SemanticDelta,
			EvidenceIndex: manifest.ArtifactRefs.EvidenceIndex, Projection: manifest.ArtifactRefs.Projection,
			AnalysisReadSet: manifest.ArtifactRefs.AnalysisReadSet, ObservationClosure: manifest.ArtifactRefs.ObservationClosure,
			AnalyzerResult: manifest.ArtifactRefs.AnalyzerResult,
		},
		ExpectedLiveHeadSnapshotID:   manifest.ExpectedLiveHeadSnapshotID,
		ExpectedPreviousGenerationID: manifest.ExpectedPreviousGenerationID, PublishedAt: manifest.PublishedAt,
	}
}

func (s *Server) resolveHistoricalOnboardingBasis(req OnboardingRequest, repoID string) (*onboardingBasis, error) {
	if strings.TrimSpace(req.ComputedBasisID) == "" || strings.TrimSpace(req.GenerationID) == "" || strings.TrimSpace(req.SnapshotID) == "" {
		return nil, errors.New("missing_precondition: historical onboarding requires computedBasisId, generationId and validatedAgainstSnapshotId")
	}

	s.mu.Lock()
	mapIR := s.mapCache[req.GenerationID]
	if mapIR == nil {
		mapIR = s.mapCache[req.ComputedBasisID]
	}
	if mapIR != nil {
		copyMap := *mapIR
		mapIR = &copyMap
	}
	s.mu.Unlock()
	if mapIR == nil {
		return nil, errors.New("missing_precondition: exact historical semantic map is not available")
	}
	if mapIR.GenerationID != req.GenerationID || mapIR.ComputedBasisID != req.ComputedBasisID || mapIR.ValidatedAgainstSnapshotID != req.SnapshotID {
		return nil, errors.New("incomparable_basis: historical map identity does not match the request")
	}
	if mapIR.Freshness != "historical" && mapIR.Freshness != "candidate" {
		return nil, errors.New("invalid_authority: historical onboarding requires a historical or candidate semantic map")
	}
	if mapIR.Basis.RepositoryID != "" && mapIR.Basis.RepositoryID != repoID {
		return nil, errors.New("incomparable_basis: repositoryId does not match the historical map")
	}
	idx, err := s.storage.ReadGenerationIndex(req.GenerationID)
	if err != nil {
		return nil, fmt.Errorf("unavailable: read requested historical generation index %q: %w", req.GenerationID, err)
	}
	if idx == nil {
		// The map remains usable, but the absence of its generation index is a
		// measured coverage gap. Never manufacture one representative flow or
		// claim that the historical generation is complete.
		if mapIR.Coverage == nil {
			mapIR.Coverage = &semantic.CoverageBoundary{}
		}
		mapIR.Coverage = &semantic.CoverageBoundary{
			IncludedSourceRoots: append([]string(nil), mapIR.Coverage.IncludedSourceRoots...),
			ExcludedReasons:     append(append([]string(nil), mapIR.Coverage.ExcludedReasons...), "requested historical generation index is unavailable"),
		}
		mapIR.Unknowns = append(append([]fusion.Unknown(nil), mapIR.Unknowns...), fusion.Unknown{
			Subject: "generation-index/" + req.GenerationID,
			Reason:  "requested historical generation index is unavailable",
		})
	}
	flows, err := collectOnboardingFlows(s.storage, idx, mapIR)
	if err != nil {
		return nil, err
	}
	return &onboardingBasis{
		query: semantic.OnboardingQueryV2{
			SchemaID: semantic.OnboardingQuerySchemaID, SchemaVersion: semantic.SemanticSchemaVersion,
			RepositoryID: repoID, ComputedBasisID: req.ComputedBasisID, GenerationID: req.GenerationID,
			ValidatedAgainstSnapshotID: req.SnapshotID, Freshness: "historical", Level: 1,
		},
		mapIR: mapIR, flows: flows,
	}, nil
}

func collectOnboardingFlows(st *storage.Storage, idx *storage.GenerationIndex, mapIR *semantic.SemanticMapIR) ([]onboardingFlowSource, error) {
	var out []onboardingFlowSource
	if idx != nil {
		for _, summary := range idx.Flows {
			if strings.TrimSpace(summary.FlowID) == "" || strings.TrimSpace(summary.EntrySymbolPath) == "" {
				continue
			}
			var spec fusion.FlowSpec
			var specPtr *fusion.FlowSpec
			raw, err := st.ReadFlowSpec(idx.GenerationID, summary.FlowID)
			if err == nil && json.Unmarshal(raw, &spec) == nil {
				specPtr = &spec
			}
			refs, steps, unknown, stale := flowEvidence(specPtr, mapIR, summary.EntrySymbolPath)
			out = append(out, onboardingFlowSource{
				summary: summary,
				spec:    specPtr,
				entry:   summary.EntrySymbolPath,
				module:  moduleForSymbol(summary.EntrySymbolPath),
				refs:    refs,
				steps:   steps,
				unknown: unknown,
				stale:   stale,
			})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].entry != out[j].entry {
			return out[i].entry < out[j].entry
		}
		return out[i].summary.FlowID < out[j].summary.FlowID
	})
	return out, nil
}

func flowEvidence(spec *fusion.FlowSpec, mapIR *semantic.SemanticMapIR, entry string) (refs []string, steps, unknown, stale int) {
	seen := map[string]bool{}
	add := func(ref string) {
		ref = strings.TrimSpace(ref)
		if ref != "" && !seen[ref] {
			seen[ref] = true
			refs = append(refs, ref)
		}
	}
	if spec != nil {
		steps = len(spec.Steps)
		for _, st := range spec.Steps {
			for _, rule := range st.Rules {
				_ = rule
			}
			if st.Freshness == "stale" || st.Freshness == "orphaned" {
				stale++
			}
			if st.Freshness == "unknown" || st.Provenance == "unknown" {
				unknown++
			}
		}
		if spec.Truncated {
			unknown++
		}
	}
	if mapIR != nil {
		if steps == 0 {
			steps = len(mapIR.Steps)
		}
		for _, st := range mapIR.Steps {
			if entry != "" && canonicalOnboardingEntry(st) != entry && len(mapIR.Steps) > 1 {
				// The map is still useful for basis identity, but only evidence
				// attached to the selected entry is reported for this flow.
				continue
			}
			for _, ref := range st.EvidenceRefs {
				add(ref)
			}
		}
		unknown += len(mapIR.Unknowns)
		if mapIR.Coverage != nil && len(mapIR.Coverage.ExcludedReasons) > 0 {
			unknown += len(mapIR.Coverage.ExcludedReasons)
		}
	}
	sort.Strings(refs)
	return refs, steps, unknown, stale
}

func moduleForSymbol(symbol string) string {
	symbol = strings.TrimSpace(strings.ReplaceAll(symbol, "\\", "/"))
	if symbol == "" {
		return ""
	}
	file := strings.SplitN(symbol, "#", 2)[0]
	file = strings.TrimPrefix(file, "./")
	parts := strings.Split(file, "/")
	for i, part := range parts {
		if part == "" {
			continue
		}
		if part == "packages" && i+1 < len(parts) {
			return "packages/" + parts[i+1]
		}
		if part == "src" || part == "lib" || part == "app" || part == "cmd" || part == "internal" || part == "pkg" {
			if i+1 < len(parts) {
				return part + "/" + parts[i+1]
			}
			return part
		}
	}
	if len(parts) > 1 {
		return parts[0]
	}
	return ""
}

// onboardingCandidates adapts immutable generation index entries and map
// steps to semantic's v2 CandidateEntry input. It does not project domains or
// rank flows. Those decisions remain in internal/semantic/onboarding_v2.go.
func onboardingCandidates(basis *onboardingBasis) []semantic.CandidateEntry {
	if basis == nil || basis.mapIR == nil {
		return nil
	}
	byEntry := make(map[string]onboardingFlowSource, len(basis.flows))
	for _, flow := range basis.flows {
		if flow.entry != "" {
			byEntry[flow.entry] = flow
		}
	}
	result := make([]semantic.CandidateEntry, 0, len(basis.flows))
	seen := make(map[string]bool, len(basis.mapIR.Steps))
	add := func(step semantic.SemanticStep, flow onboardingFlowSource, hasFlow bool) {
		entry := canonicalOnboardingEntry(step)
		if entry == "" || seen[entry] {
			return
		}
		seen[entry] = true
		candidate := semantic.CandidateEntry{
			CandidateID:     step.StepID,
			EntrySymbolPath: entry,
			SourceRoot:      sourceRootForSymbol(entry),
			Module:          moduleForSymbol(entry),
			Package:         packageForSymbol(entry),
			EvidenceRefs:    append([]string(nil), step.EvidenceRefs...),
			KeyMutations:    mapStepMutations(step),
			SelectionScore:  onboardingStepScore(step, flow, hasFlow),
		}
		if hasFlow {
			candidate.CandidateID = nonEmptyCandidateID(flow.summary.FlowID, step.StepID)
			candidate.Title = firstNonEmpty(flow.summary.Title, step.Name, entry)
			candidate.ResultEvidenceRefs = resultEvidenceForFlow(basis.mapIR, flow, entry)
			candidate.Rationale = onboardingCandidateRationale(flow, step)
		}
		// A module/package is a signal for rationale and grouping, not a
		// business domain label. Domain stays empty unless a trusted index
		// source supplies one in a future adapter contract.
		result = append(result, candidate)
	}
	if len(byEntry) > 0 {
		for _, step := range basis.mapIR.Steps {
			entry := canonicalOnboardingEntry(step)
			if flow, ok := byEntry[entry]; ok {
				add(step, flow, true)
			}
		}
	} else if len(basis.mapIR.Steps) > 0 {
		// Without an index, expose the complete measured map inventory as
		// candidate entries. Coverage remains visibly partial through the
		// generation-index unknown added by the historical resolver.
		for _, step := range basis.mapIR.Steps {
			add(step, onboardingFlowSource{}, false)
		}
	}
	// Keep index-only candidates that have no matching map step out of the
	// semantic request. Core validation intentionally treats them as unmapped,
	// but accepting them would expose a flow not grounded in the canonical map.
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].EntrySymbolPath != result[j].EntrySymbolPath {
			return result[i].EntrySymbolPath < result[j].EntrySymbolPath
		}
		return result[i].CandidateID < result[j].CandidateID
	})
	return result
}

func canonicalOnboardingEntry(step semantic.SemanticStep) string {
	path := strings.TrimSpace(strings.ReplaceAll(step.Anchor.RepoRelativePath, "\\", "/"))
	symbol := strings.TrimSpace(step.Anchor.EnclosingSymbolPath)
	if path != "" && symbol != "" {
		return path + "#" + symbol
	}
	if strings.TrimSpace(step.StructuralIdentity) != "" && !strings.ContainsRune(step.StructuralIdentity, '\x00') {
		return strings.TrimSpace(step.StructuralIdentity)
	}
	return ""
}

func sourceRootForSymbol(symbol string) string {
	file := strings.SplitN(strings.TrimPrefix(strings.ReplaceAll(symbol, "\\", "/"), "./"), "#", 2)[0]
	if index := strings.IndexByte(file, '/'); index >= 0 {
		return file[:index]
	}
	return file
}

func packageForSymbol(symbol string) string {
	file := strings.SplitN(strings.TrimPrefix(strings.ReplaceAll(symbol, "\\", "/"), "./"), "#", 2)[0]
	parts := strings.Split(file, "/")
	if len(parts) < 2 {
		return ""
	}
	if parts[0] == "packages" {
		return strings.Join(parts[:2], "/")
	}
	return parts[0]
}

func mapStepMutations(step semantic.SemanticStep) []string {
	result := make([]string, 0, 2)
	if step.StateDelta != nil {
		if strings.TrimSpace(step.StateDelta.After) != "" {
			result = append(result, step.StateDelta.After)
		}
	}
	if step.SideEffect != nil && strings.TrimSpace(*step.SideEffect) != "" {
		result = append(result, *step.SideEffect)
	}
	sort.Strings(result)
	return result
}

func resultEvidenceForEntry(mapIR *semantic.SemanticMapIR, entry string) []string {
	if mapIR == nil {
		return nil
	}
	steps := make([]semantic.SemanticStep, 0)
	for _, step := range mapIR.Steps {
		if canonicalOnboardingEntry(step) == entry {
			steps = append(steps, step)
		}
	}
	if len(steps) == 0 {
		return nil
	}
	last := steps[len(steps)-1]
	return append([]string(nil), last.EvidenceRefs...)
}

func resultEvidenceForFlow(mapIR *semantic.SemanticMapIR, flow onboardingFlowSource, entry string) []string {
	if mapIR == nil {
		return nil
	}
	if flow.spec != nil && len(flow.spec.Steps) > 0 {
		last := flow.spec.Steps[len(flow.spec.Steps)-1]
		lastEntry := canonicalOnboardingAnchor(last.Anchor)
		if lastEntry != "" {
			if refs := resultEvidenceForEntry(mapIR, lastEntry); len(refs) > 0 {
				return refs
			}
		}
	}
	return resultEvidenceForEntry(mapIR, entry)
}

func canonicalOnboardingAnchor(anchor slicing.Anchor) string {
	path := strings.TrimSpace(strings.ReplaceAll(anchor.RepoRelativePath, "\\", "/"))
	symbol := strings.TrimSpace(anchor.EnclosingSymbolPath)
	if path == "" || symbol == "" {
		return ""
	}
	return path + "#" + symbol
}

func onboardingStepScore(step semantic.SemanticStep, flow onboardingFlowSource, hasFlow bool) float64 {
	score := float64(len(step.EvidenceRefs) * 2)
	if step.StateDelta != nil || step.SideEffect != nil {
		score += 1
	}
	if hasFlow {
		score += float64(flow.steps)
		score -= float64(flow.unknown * 2)
		score -= float64(flow.stale)
	}
	if score < 0 {
		return 0
	}
	return score
}

func onboardingCandidateRationale(flow onboardingFlowSource, step semantic.SemanticStep) string {
	return fmt.Sprintf("selected from generation index entry %s, module %s and %d referenced map Evidence items", flow.entry, moduleForSymbol(flow.entry), len(step.EvidenceRefs))
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func nonEmptyCandidateID(primary, fallback string) string {
	if strings.TrimSpace(primary) != "" {
		return primary
	}
	return fallback
}

// redactOnboardingJSON is the final public egress gate used by HTTP and MCP.
// It removes secrets from Evidence content and keeps the response JSON-only.
func redactOnboardingJSON(value any) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("marshal onboarding output: %w", err)
	}
	clean, _, err := secret.RedactJSON(raw)
	if err != nil {
		return nil, fmt.Errorf("redact onboarding output: %w", err)
	}
	if err := validateRedactedOnboardingJSON(clean); err != nil {
		return nil, fmt.Errorf("validate onboarding output after redaction: %w", err)
	}
	return clean, nil
}

// RedactAndValidateOnboardingJSON is the shared final egress gate for the
// HTTP and MCP onboarding surfaces. It returns decoded sanitized JSON so MCP
// cannot accidentally return the pre-redaction typed value.
func RedactAndValidateOnboardingJSON(value any) (any, error) {
	clean, err := redactOnboardingJSON(value)
	if err != nil {
		return nil, err
	}
	var decoded any
	if err := json.Unmarshal(clean, &decoded); err != nil {
		return nil, fmt.Errorf("decode sanitized onboarding output: %w", err)
	}
	return decoded, nil
}

func validateRedactedOnboardingJSON(data []byte) error {
	var header struct {
		SchemaID string `json:"schemaId"`
	}
	if err := json.Unmarshal(data, &header); err != nil {
		return fmt.Errorf("onboarding output is not JSON: %w", err)
	}
	if strings.TrimSpace(header.SchemaID) == "" {
		return errors.New("onboarding output schemaId is required")
	}
	switch header.SchemaID {
	case semantic.DomainOverviewSchemaID:
		return contractharness.ValidateDomainOverviewV2(data)
	case semantic.RepresentativeFlowCatalogSchemaID:
		return contractharness.ValidateRepresentativeFlowCatalogV2(data)
	case semantic.OnboardingFlowDrilldownSchemaID:
		return contractharness.ValidateOnboardingFlowDrilldownV2(data)
	default:
		return fmt.Errorf("unsupported onboarding output schemaId %q", header.SchemaID)
	}
}

// onboardingQueryValues parses HTTP query identity.  Keeping this parser in
// the public seam makes it impossible for GET and POST callers to skip the
// exact-basis requirement by relying on a default active value.
func onboardingQueryValues(values url.Values) (OnboardingRequest, error) {
	level := 1
	if raw := strings.TrimSpace(values.Get("level")); raw != "" {
		n, err := parsePositiveInt(raw)
		if err != nil {
			return OnboardingRequest{}, fmt.Errorf("invalid_precondition: level must be a positive integer")
		}
		level = n
	}
	budget := 0
	if rawValues, present := values["maxVisibleCoreSteps"]; present {
		raw := ""
		if len(rawValues) > 0 {
			raw = strings.TrimSpace(rawValues[0])
		}
		n, err := parsePositiveInt(raw)
		if err != nil {
			return OnboardingRequest{}, fmt.Errorf("invalid_precondition: maxVisibleCoreSteps must be a positive integer")
		}
		budget = n
	}
	return OnboardingRequest{
		RepositoryID:                values.Get("repositoryId"),
		Domain:                      values.Get("domain"),
		Level:                       level,
		MaxVisibleCoreSteps:         budget,
		MaxVisibleCoreStepsProvided: func() bool { _, ok := values["maxVisibleCoreSteps"]; return ok }(),
		Freshness:                   values.Get("freshness"),
		ComputedBasisID:             firstQueryValue(values, "computedBasisId", "basisId"),
		GenerationID:                firstQueryValue(values, "generationId", "genId"),
		SnapshotID:                  firstQueryValue(values, "validatedAgainstSnapshotId", "snapshotId"),
	}, nil
}

func firstQueryValue(values url.Values, names ...string) string {
	for _, name := range names {
		if value := strings.TrimSpace(values.Get(name)); value != "" {
			return value
		}
	}
	return ""
}

func mergeOnboardingRequest(base OnboardingRequest, values map[string]any) (OnboardingRequest, error) {
	if value, ok := values["repositoryId"].(string); ok && strings.TrimSpace(value) != "" {
		base.RepositoryID = value
	}
	if value, ok := values["domain"].(string); ok && strings.TrimSpace(value) != "" {
		base.Domain = value
	}
	if value, ok := values["freshness"].(string); ok && strings.TrimSpace(value) != "" {
		base.Freshness = value
	}
	if value, ok := values["computedBasisId"].(string); ok && strings.TrimSpace(value) != "" {
		base.ComputedBasisID = value
	}
	if value, ok := values["generationId"].(string); ok && strings.TrimSpace(value) != "" {
		base.GenerationID = value
	}
	if value, ok := values["validatedAgainstSnapshotId"].(string); ok && strings.TrimSpace(value) != "" {
		base.SnapshotID = value
	}
	if value, ok := values["snapshotId"].(string); ok && strings.TrimSpace(value) != "" {
		base.SnapshotID = value
	}
	if value, ok := values["basisId"].(string); ok && strings.TrimSpace(value) != "" {
		base.ComputedBasisID = value
	}
	if value, ok := values["genId"].(string); ok && strings.TrimSpace(value) != "" {
		base.GenerationID = value
	}
	if value, ok := values["level"].(float64); ok && value > 0 {
		base.Level = int(value)
	}
	if value, ok := values["level"].(int); ok && value > 0 {
		base.Level = value
	}
	if value, present := values["maxVisibleCoreSteps"]; present {
		budget, err := parseOnboardingBudgetValue(value)
		if err != nil {
			return OnboardingRequest{}, fmt.Errorf("invalid_precondition: maxVisibleCoreSteps must be a positive integer")
		}
		base.MaxVisibleCoreSteps = budget
		base.MaxVisibleCoreStepsProvided = true
	}
	if budgetValue, present := values["displayBudget"]; present {
		budget, err := parseOnboardingDisplayBudget(budgetValue)
		if err != nil {
			return OnboardingRequest{}, err
		}
		if base.MaxVisibleCoreStepsProvided && base.MaxVisibleCoreSteps != budget.TargetMax {
			return OnboardingRequest{}, fmt.Errorf("invalid_precondition: conflicting maxVisibleCoreSteps and displayBudget.targetMax")
		}
		base.DisplayBudget = &budget
		base.MaxVisibleCoreSteps = budget.TargetMax
	}
	return base, nil
}

// mergeOnboardingEnvelope accepts both the direct HTTP shape and the
// TaskViewQuery envelope.  Basis selectors are translated only to a
// freshness lane and generation identity.  They never synthesize a basis or
// bypass the exact identity checks in resolveOnboardingBasis.
func mergeOnboardingEnvelope(base OnboardingRequest, values map[string]any) (OnboardingRequest, error) {
	var err error
	base, err = mergeOnboardingRequest(base, values)
	if err != nil {
		return OnboardingRequest{}, err
	}
	if nested, ok := values["onboarding"].(map[string]any); ok {
		base, err = mergeOnboardingRequest(base, nested)
		if err != nil {
			return OnboardingRequest{}, err
		}
	}
	if common, ok := values["common"].(map[string]any); ok {
		if selector, ok := common["basisSelector"].(map[string]any); ok {
			switch strings.TrimSpace(fmt.Sprint(selector["kind"])) {
			case "active":
				base.Freshness = "current"
			case "generation", "workspaceSnapshot":
				base.Freshness = "historical"
				if id := strings.TrimSpace(fmt.Sprint(selector["id"])); id != "" && base.GenerationID == "" {
					base.GenerationID = id
				}
			}
		}
		if filters, ok := common["filters"].(map[string]any); ok {
			base, err = mergeOnboardingRequest(base, filters)
			if err != nil {
				return OnboardingRequest{}, err
			}
		}
	}
	return base, nil
}

func parseOnboardingBudgetValue(value any) (int, error) {
	switch number := value.(type) {
	case int:
		if number <= 0 {
			return 0, errors.New("budget must be positive")
		}
		return number, nil
	case int8:
		return parseOnboardingBudgetValue(int(number))
	case int16:
		return parseOnboardingBudgetValue(int(number))
	case int32:
		return parseOnboardingBudgetValue(int(number))
	case int64:
		if number <= 0 || int64(int(number)) != number {
			return 0, errors.New("budget must be positive")
		}
		return int(number), nil
	case float64:
		if number <= 0 || number != float64(int(number)) {
			return 0, errors.New("budget must be a positive integer")
		}
		return int(number), nil
	case float32:
		return parseOnboardingBudgetValue(float64(number))
	case string:
		return parsePositiveInt(number)
	default:
		return 0, errors.New("budget must be a positive integer")
	}
}

func parseOnboardingDisplayBudget(value any) (semantic.DisplayBudget, error) {
	budgetMap, ok := value.(map[string]any)
	if !ok {
		return semantic.DisplayBudget{}, fmt.Errorf("invalid_precondition: displayBudget must be an object")
	}
	minValue, minOK := budgetMap["targetMin"]
	maxValue, maxOK := budgetMap["targetMax"]
	enforcementValue, enforcementOK := budgetMap["enforcement"]
	if !minOK || !maxOK || !enforcementOK {
		return semantic.DisplayBudget{}, fmt.Errorf("invalid_precondition: displayBudget must include targetMin, targetMax and enforcement")
	}
	targetMin, err := parseOnboardingBudgetValue(minValue)
	if err != nil {
		return semantic.DisplayBudget{}, fmt.Errorf("invalid_precondition: displayBudget.targetMin must be a positive integer")
	}
	targetMax, err := parseOnboardingBudgetValue(maxValue)
	if err != nil {
		return semantic.DisplayBudget{}, fmt.Errorf("invalid_precondition: displayBudget.targetMax must be a positive integer")
	}
	enforcement, ok := enforcementValue.(string)
	if !ok || enforcement != "soft" {
		return semantic.DisplayBudget{}, fmt.Errorf("invalid_precondition: displayBudget.enforcement must be soft")
	}
	if targetMin > targetMax {
		return semantic.DisplayBudget{}, fmt.Errorf("invalid_precondition: displayBudget targetMin must not exceed targetMax")
	}
	return semantic.DisplayBudget{TargetMin: targetMin, TargetMax: targetMax, Enforcement: enforcement}, nil
}

func parsePositiveInt(raw string) (int, error) {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || value < 1 {
		return 0, errors.New("invalid integer")
	}
	return value, nil
}
