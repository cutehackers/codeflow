package semantic

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"codeflow/internal/analyzer/workspace"
	"codeflow/internal/collector/contractharness"
	"codeflow/internal/collector/evidence"
)

// PublicationGate evaluates whether a compiled SemanticMapIR satisfies the 6 Current Publication
// subgates (Raw §18.1) against the live workspace state and CausalObservationClosure.
type PublicationGate struct{}

// PublicationMetrics are captured by the workspace activity ledger at the
// same checkpoint as the analysis result. A zero value is intentionally not a
// measurement. Gap objects use -1 when a producer did not provide metrics so
// callers cannot mistake an absent observation for a fast analysis.
type PublicationMetrics struct {
	LagMs            int64
	PendingRevisions int
	MeasuredAt       time.Time
	Activity         string
	TraceID          string
}

// PublicationInput is the complete, snapshot-bound input to the VS03 current
// publication gate. The older Evaluate method remains as a compatibility
// reader for VS04 tests, while production publication must use EvaluateCurrent.
type PublicationInput struct {
	Map                          *SemanticMapIR
	Closure                      *CausalObservationClosure
	Delta                        *workspace.WorkspaceDelta
	CapturedSnapshot             *workspace.WorkspaceSnapshot
	LiveHeadSnapshot             *workspace.WorkspaceSnapshot
	Intent                       *TaskIntent
	RepositoryID                 string
	WorktreeID                   string
	DependencyFingerprint        string
	QueryHash                    string
	ExpectedPreviousGenerationID string
	GenerationID                 string
	ArtifactDigests              map[string]string
	AnalysisRequest              *evidence.AnalyzerRequest
	AnalysisResult               *evidence.Result
	// CapabilityProfileDigest is optional at the compatibility gate boundary,
	// but when supplied it must be the digest of the complete canonical
	// AnalyzerResult capability profile. Strict publication paths always supply
	// it after deriving it from AnalysisResult.
	CapabilityProfileDigest string
	Metrics                 PublicationMetrics
}

// CanonicalCapabilityProfileDigest returns the content digest of the complete
// v2 capability profile carried by an analyzer result. JSON encoding of the
// typed profile is deterministic, so the digest cannot be replaced by a
// digest of only the feature names.
func CanonicalCapabilityProfileDigest(profile evidence.CapabilityProfile) (string, error) {
	data, err := json.Marshal(profile)
	if err != nil {
		return "", fmt.Errorf("marshal capability profile: %w", err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// EvaluateCurrent validates every identity and causal gate required before a
// generation may become current. It is intentionally stricter than Evaluate:
// current authority is a proof operation, not a property inferred from a
// candidate map's text.
func (g *PublicationGate) EvaluateCurrent(input PublicationInput) (CurrentPublicationResult, *VerifiedGap) {
	result := CurrentPublicationResult{
		SnapshotGate:          "passed",
		ClosureGate:           "passed",
		EvidenceGate:          "passed",
		SemanticAtomicityGate: "passed",
		TaskRelevanceGate:     "passed",
		ComprehensionGate:     "passed",
	}
	causes := make([]string, 0, 8)
	affected := map[string]bool{}
	fail := func(gate *string, cause string, scope ...string) {
		*gate = "failed"
		causes = append(causes, cause)
		for _, p := range scope {
			if p != "" {
				affected[p] = true
			}
		}
	}

	// Snapshot identity is checked as a chain. A matching basis string alone is
	// not sufficient because it may have been copied onto bytes from another
	// lease.
	if input.Map == nil || input.CapturedSnapshot == nil || input.LiveHeadSnapshot == nil {
		fail(&result.SnapshotGate, "snapshot identity is incomplete")
	} else {
		m := input.Map
		captured := input.CapturedSnapshot
		live := input.LiveHeadSnapshot
		if m.SchemaID != SemanticMapSchemaID || m.SchemaVersion != SemanticSchemaVersion {
			fail(&result.SnapshotGate, "semantic map schema identity is not canonical")
		}
		if captured.SchemaID != "https://codeflow.local/schemas/rflsc.workspace-snapshot.v2.schema.json" || captured.SchemaVersion != 2 || live.SchemaID != captured.SchemaID || live.SchemaVersion != captured.SchemaVersion {
			fail(&result.SnapshotGate, "workspace snapshot schema identity is not canonical")
		}
		if m.ValidatedAgainstSnapshotID == "" || m.ValidatedAgainstSnapshotID != captured.SnapshotID || m.Basis.ComputedWorkspaceSnapshotID != captured.SnapshotID {
			fail(&result.SnapshotGate, "map and captured snapshot identity mismatch")
		}
		if m.ComputedBasisID == "" || m.ComputedBasisID != captured.ComputedBasisID || m.Basis.ComputedBasisID != captured.ComputedBasisID {
			fail(&result.SnapshotGate, "computed basis identity mismatch")
		}
		if m.Basis.SnapshotTreeID == "" || m.Basis.SnapshotTreeID != captured.RootTreeID {
			fail(&result.SnapshotGate, "snapshot tree identity mismatch")
		}
		if m.Basis.DependencyFingerprint == "" || captured.DependencyFingerprint == "" || m.Basis.DependencyFingerprint != captured.DependencyFingerprint || input.DependencyFingerprint == "" || input.DependencyFingerprint != captured.DependencyFingerprint {
			fail(&result.SnapshotGate, "dependency fingerprint identity mismatch")
		}
		if m.Basis.WorkspaceEpoch != captured.WorkspaceEpoch || live.WorkspaceEpoch != captured.WorkspaceEpoch || captured.WorkspaceEpoch < 0 {
			fail(&result.SnapshotGate, "workspace epoch identity mismatch")
		}
		if strings.TrimSpace(input.RepositoryID) == "" || m.Basis.RepositoryID != input.RepositoryID {
			fail(&result.SnapshotGate, "repository identity mismatch")
		}
		if strings.TrimSpace(input.WorktreeID) == "" || m.Basis.WorktreeID == "" {
			fail(&result.SnapshotGate, "worktree identity is unmeasured")
		} else if m.Basis.WorktreeID != input.WorktreeID {
			fail(&result.SnapshotGate, "worktree identity mismatch")
		}
		if captured.RepositoryID == "" || captured.RepositoryID != input.RepositoryID || captured.WorktreeID == "" || captured.WorktreeID != input.WorktreeID {
			fail(&result.SnapshotGate, "captured snapshot is not bound to repository and worktree identity")
		}
		if input.Delta == nil || input.Delta.FromSnapshotID != captured.SnapshotID || input.Delta.ToSnapshotID != live.SnapshotID {
			fail(&result.SnapshotGate, "workspace delta is not bound to captured and live snapshots")
		}
	}

	if input.AnalysisResult != nil {
		resultBytes, err := json.Marshal(input.AnalysisResult)
		if err != nil {
			fail(&result.ClosureGate, "canonical analyzer result cannot be marshaled")
		} else if err := contractharness.Validate(evidence.AnalyzerResultSchemaID, resultBytes); err != nil {
			fail(&result.ClosureGate, "canonical analyzer result schema is invalid")
		}
		if input.AnalysisRequest == nil {
			fail(&result.ClosureGate, "canonical analyzer request is missing")
		} else if err := evidence.ValidateResult(*input.AnalysisRequest, *input.AnalysisResult); err != nil {
			fail(&result.ClosureGate, "canonical analyzer result is not bound to its request")
		}
		if capabilityDigest, digestErr := CanonicalCapabilityProfileDigest(input.AnalysisResult.Capability); digestErr != nil {
			fail(&result.ClosureGate, "canonical analyzer capability profile cannot be digested")
		} else if input.CapabilityProfileDigest == "" {
			fail(&result.ClosureGate, "canonical analyzer capability profile digest is missing")
		} else if input.CapabilityProfileDigest != capabilityDigest {
			fail(&result.ClosureGate, "capability profile digest is not bound to canonical analyzer result")
		}
	}
	if input.AnalysisRequest == nil || input.AnalysisResult == nil {
		fail(&result.ClosureGate, "canonical VS-02 analyzer request and result are required for current publication")
	}
	if input.Map == nil || input.Closure == nil {
		fail(&result.ClosureGate, "causal observation closure or map is missing")
	} else {
		m, c := input.Map, input.Closure
		if c.ClosureID == "" || c.AnalysisReadSetID == "" || m.Basis.AnalysisReadSetID == "" || c.AnalysisReadSetID != m.Basis.AnalysisReadSetID {
			fail(&result.ClosureGate, "analysis read-set identity is missing or mismatched")
		}
		if c.ComputedBasisID != m.ComputedBasisID || c.TaskIntentRevision != m.Task.IntentRevision {
			fail(&result.ClosureGate, "closure basis or intent identity mismatch")
		}
		if c.NormalizedQueryHash == "" || input.QueryHash == "" || c.NormalizedQueryHash != input.QueryHash {
			fail(&result.ClosureGate, "closure query identity is missing or mismatched")
		}
		if c.ClosureDigest == "" {
			fail(&result.ClosureGate, "closure digest is missing")
		} else if c.CanonicalResult == nil {
			if digest, err := ClosureDigest(*c); err != nil || digest != c.ClosureDigest {
				fail(&result.ClosureGate, "closure digest does not bind the observation set")
			}
		} else if input.AnalysisResult == nil || c.CanonicalResult.RequestID != input.AnalysisResult.RequestID || c.CanonicalResult.Closure.ClosureDigest != c.ClosureDigest {
			fail(&result.ClosureGate, "closure digest does not bind the observation set")
		}
		if c.ClosureStatus != "closed" {
			reasons := strings.Join(c.IncompleteReasons, ", ")
			if reasons == "" {
				reasons = "closure is open"
			}
			fail(&result.ClosureGate, "causal observation closure is not closed: "+reasons)
		}
		if input.Delta != nil {
			for _, p := range append(append(append([]string{}, input.Delta.ChangedPaths...), input.Delta.AddedPaths...), input.Delta.ModifiedPaths...) {
				if intersectsClosurePath(c, p) {
					fail(&result.ClosureGate, "workspace delta intersects causal observation: "+p, p)
				}
			}
			for _, p := range input.Delta.DeletedPaths {
				if intersectsClosurePath(c, p) {
					fail(&result.ClosureGate, "workspace deletion intersects causal observation: "+p, p)
				}
			}
			for _, p := range input.Delta.RenamedPaths {
				if intersectsClosurePath(c, p) {
					fail(&result.ClosureGate, "workspace rename intersects causal observation: "+p, p)
				}
			}
			if input.Delta.MembershipChanged && len(c.MembershipObservations) == 0 {
				fail(&result.ClosureGate, "workspace membership observation is missing")
			} else if input.Delta.MembershipChanged {
				fail(&result.ClosureGate, "workspace membership changed after closure observation")
			}
			if input.Delta.IndexChanged {
				fail(&result.ClosureGate, "workspace index changed after closure observation")
			}
			if input.Delta.ResolutionChanged {
				fail(&result.ClosureGate, "workspace resolution changed after closure observation")
			}
			if input.Delta.CapabilityChanged {
				fail(&result.ClosureGate, "workspace capability changed after closure observation")
			}
			if input.Delta.ConfigurationChanged {
				fail(&result.ClosureGate, "workspace configuration changed after closure observation")
			}
			if input.Delta.PublicContractChanged {
				fail(&result.ClosureGate, "workspace public contract changed after closure observation")
			}
		}
	}

	if input.Map == nil || len(input.Map.Steps) == 0 || len(input.Map.Evidence) == 0 {
		fail(&result.EvidenceGate, "map has no validated evidence")
	} else {
		evidence := make(map[string]SemanticEvidence, len(input.Map.Evidence))
		for _, ev := range input.Map.Evidence {
			if ev.EvidenceID == "" || ev.ValidationStatus != "verified" || ev.SnapshotID != input.Map.ValidatedAgainstSnapshotID || ev.ComputedBasisID != "" && ev.ComputedBasisID != input.Map.ComputedBasisID {
				fail(&result.EvidenceGate, "evidence is not bound to the published snapshot")
			}
			if _, exists := evidence[ev.EvidenceID]; exists {
				fail(&result.EvidenceGate, "duplicate evidence identity: "+ev.EvidenceID)
			}
			evidence[ev.EvidenceID] = ev
		}
		for _, step := range input.Map.Steps {
			if step.StepID == "" || step.Anchor.RepoRelativePath == "" || len(step.EvidenceRefs) == 0 {
				fail(&result.EvidenceGate, "step lacks verified evidence: "+step.StepID)
			}
			for _, ref := range step.EvidenceRefs {
				if _, ok := evidence[ref]; !ok {
					fail(&result.EvidenceGate, "step references unknown evidence: "+ref)
				}
			}
		}
	}

	if input.Map == nil {
		fail(&result.SemanticAtomicityGate, "semantic map is missing")
	} else {
		steps := make(map[string]bool, len(input.Map.Steps))
		for _, step := range input.Map.Steps {
			if steps[step.StepID] {
				fail(&result.SemanticAtomicityGate, "duplicate step identity: "+step.StepID)
			}
			steps[step.StepID] = true
		}
		for _, edge := range input.Map.Edges {
			if !steps[edge.FromStepID] || !steps[edge.ToStepID] {
				fail(&result.SemanticAtomicityGate, "edge endpoint is outside canonical map")
			}
		}
	}

	if input.Map == nil || input.Intent == nil {
		fail(&result.TaskRelevanceGate, "task intent or map is missing")
	} else {
		if input.Intent.IntentStatus == "needs_confirmation" || input.Intent.TaskID == "" || input.Map.Task.TaskID != input.Intent.TaskID || input.Map.Task.IntentRevision != input.Intent.Revision || input.Map.Task.Mode != input.Intent.Mode {
			fail(&result.TaskRelevanceGate, "map is not bound to a confirmed task intent")
		}
		if strings.TrimSpace(input.QueryHash) == "" {
			fail(&result.TaskRelevanceGate, "normalized query identity is missing")
		}
	}
	if input.Map != nil {
		if input.GenerationID == "" || input.GenerationID != input.Map.GenerationID {
			fail(&result.SemanticAtomicityGate, "generation identity is missing or mismatched")
		}
		if input.ExpectedPreviousGenerationID == input.Map.GenerationID && input.ExpectedPreviousGenerationID != "" {
			fail(&result.SemanticAtomicityGate, "new generation cannot equal expected previous generation")
		}
		if len(input.ArtifactDigests) == 0 {
			fail(&result.SemanticAtomicityGate, "artifact digests are unmeasured")
		} else {
			for name, digest := range input.ArtifactDigests {
				if strings.TrimSpace(name) == "" || strings.TrimSpace(digest) == "" || len(digest) != 64 {
					fail(&result.SemanticAtomicityGate, "artifact digest is missing or malformed: "+name)
				}
			}
		}
	}

	if input.Map == nil || input.Map.Summary.Requested == "" || input.Map.Summary.Current == "" {
		fail(&result.ComprehensionGate, "candidate comprehension summary is missing")
	}

	if result.SnapshotGate == "passed" && result.ClosureGate == "passed" && result.EvidenceGate == "passed" && result.SemanticAtomicityGate == "passed" && result.TaskRelevanceGate == "passed" && result.ComprehensionGate == "passed" {
		result.Eligibility = "passed"
		return result, nil
	}

	result.Eligibility = "rejected"
	scope := make([]string, 0, len(affected))
	for p := range affected {
		scope = append(scope, p)
	}
	sort.Strings(scope)
	latest := ""
	if input.LiveHeadSnapshot != nil {
		latest = input.LiveHeadSnapshot.SnapshotID
	}
	last := ""
	if input.Map != nil {
		last = input.Map.GenerationID
	}
	lag, pending := int64(-1), -1
	gapTimestamp := time.Now().UTC()
	if !input.Metrics.MeasuredAt.IsZero() {
		lag, pending = input.Metrics.LagMs, input.Metrics.PendingRevisions
		gapTimestamp = input.Metrics.MeasuredAt
	}
	activity := input.Metrics.Activity
	if activity == "" {
		activity = "editing"
	}
	closureRef := ""
	if input.Closure != nil {
		closureRef = input.Closure.ClosureID
	}
	return result, &VerifiedGap{SchemaID: "https://codeflow.local/schemas/rflsc.verified-gap.v2.schema.json", SchemaVersion: 2, Freshness: "last_verified", Activity: activity, LastVerifiedGenID: last, LatestSnapshotID: latest, WorkspaceEpoch: func() int64 {
		if input.LiveHeadSnapshot != nil {
			return input.LiveHeadSnapshot.WorkspaceEpoch
		}
		return 0
	}(), AffectedScope: scope, AnalysisLagMs: lag, PendingRevisions: pending, IntersectedCauses: dedupeStrings(causes), Timestamp: gapTimestamp, TraceID: input.Metrics.TraceID, ClosureRef: closureRef}
}

func intersectsClosurePath(c *CausalObservationClosure, path string) bool {
	path = strings.TrimPrefix(strings.ReplaceAll(path, "\\", "/"), "./")
	path = strings.TrimPrefix(path, "/")
	if path == "" {
		return false
	}
	within := func(scope string) bool {
		scope = strings.TrimPrefix(strings.ReplaceAll(scope, "\\", "/"), "./")
		scope = strings.TrimPrefix(scope, "/")
		if scope == "." || scope == "" {
			return true
		}
		return path == scope || strings.HasPrefix(path, scope+"/")
	}
	for _, ref := range c.PositiveDependencies.DocumentRevisionRefs {
		documentPath := ref
		if idx := strings.LastIndex(ref, "@"); idx > 0 {
			documentPath = ref[:idx]
		}
		if within(documentPath) {
			return true
		}
	}
	for _, n := range c.NegativeObservations {
		if n.ScopeRef != "" && within(n.ScopeRef) {
			return true
		}
	}
	for _, m := range c.MembershipObservations {
		if m.ContainerRef != "" && within(m.ContainerRef) {
			return true
		}
	}
	for _, f := range c.DependencyFrontiers {
		if f.BoundaryRef != "" && within(f.BoundaryRef) {
			return true
		}
	}
	return false
}

func dedupeStrings(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, v := range in {
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

// ClosureDigest returns a stable digest for a closure with its digest field
// blanked. Producers and publication readers use this to bind the proof to the
// exact observation set, rather than to a caller-supplied label.
func ClosureDigest(c CausalObservationClosure) (string, error) {
	c.ClosureDigest = ""
	b, err := json.Marshal(c)
	if err != nil {
		return "", fmt.Errorf("marshal closure: %w", err)
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}

// NewPublicationGate creates a new publication gate evaluator.
func NewPublicationGate() *PublicationGate {
	return &PublicationGate{}
}

// Evaluate is a deliberately rejecting compatibility boundary. Current
// publication requires the complete PublicationInput and callers must use
// EvaluateCurrent. Keeping this legacy shape from producing a passed result
// prevents an unbound map from becoming current through an older call path.
func (g *PublicationGate) Evaluate(
	mapIR *SemanticMapIR,
	closure *CausalObservationClosure,
	delta *workspace.WorkspaceDelta,
	liveHeadSnapshot *workspace.WorkspaceSnapshot,
	intent *TaskIntent,
) (CurrentPublicationResult, *VerifiedGap) {
	return g.EvaluateCurrent(PublicationInput{
		Map: mapIR, Closure: closure, Delta: delta, LiveHeadSnapshot: liveHeadSnapshot,
		Intent: intent, Metrics: PublicationMetrics{Activity: "editing"},
	})
}

// EvaluateSettlement evaluates whether the map satisfies Settlement Gate requirements (Raw §10.11, §18.1, INV-24, D27, D31).
func (g *PublicationGate) EvaluateSettlement(mapIR *SemanticMapIR) SettlementEvaluation {
	if mapIR == nil {
		return SettlementEvaluation{Gate: "pending", BlockingObligationRefs: []string{}}
	}

	blockingRefs := []string{}
	for _, ob := range mapIR.Quality.CriticalObligations {
		if ob.Required && ob.Status != "verified" {
			blockingRefs = append(blockingRefs, ob.ObligationID)
		}
	}

	// Q1 or Q2 cannot pass settlement regardless of verified obligations (VS04-A5, Raw §18.1)
	if mapIR.Quality.Stage != "Q3" && mapIR.Quality.Stage != "Q4" {
		return SettlementEvaluation{
			Gate:                   "pending",
			BlockingObligationRefs: blockingRefs,
		}
	}

	now := time.Now().UTC()
	if len(blockingRefs) > 0 || mapIR.Quality.UnresolvedCriticalCount > 0 || mapIR.Quality.ConflictingCriticalCount > 0 {
		return SettlementEvaluation{
			Gate:                   "failed",
			EvaluatedAt:            &now,
			BlockingObligationRefs: blockingRefs,
		}
	}

	return SettlementEvaluation{
		Gate:                   "passed",
		EvaluatedAt:            &now,
		BlockingObligationRefs: []string{},
	}
}
