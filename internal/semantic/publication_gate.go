package semantic

import (
	"sort"
	"strings"
	"time"

	"codeflow/internal/workspace"
)

// PublicationGate evaluates whether a compiled SemanticMapIR satisfies the 6 Current Publication
// subgates (Raw §18.1) against the live workspace state and CausalObservationClosure.
type PublicationGate struct{}

// NewPublicationGate creates a new publication gate evaluator.
func NewPublicationGate() *PublicationGate {
	return &PublicationGate{}
}

// Evaluate evaluates Snapshot, Closure, Evidence, Semantic Atomicity, Task Relevance, and Comprehension
// gates. If all pass, returns eligibility="passed". Otherwise, returns eligibility="rejected" and a VerifiedGap.
func (g *PublicationGate) Evaluate(
	mapIR *SemanticMapIR,
	closure *CausalObservationClosure,
	delta *workspace.WorkspaceDelta,
	liveHeadSnapshot *workspace.WorkspaceSnapshot,
	intent *TaskIntent,
) (CurrentPublicationResult, *VerifiedGap) {
	result := CurrentPublicationResult{
		SnapshotGate:          "passed",
		ClosureGate:           "passed",
		EvidenceGate:          "passed",
		SemanticAtomicityGate: "passed",
		TaskRelevanceGate:     "passed",
		ComprehensionGate:     "passed",
	}

	var conflictCauses []string
	affectedScopeMap := make(map[string]bool)

	// 1. Snapshot Gate (Raw §18.1)
	if liveHeadSnapshot == nil {
		result.SnapshotGate = "failed"
		conflictCauses = append(conflictCauses, "liveHead snapshot is unavailable")
	}

	// 2. Closure Gate (Raw §7.4, §18.1, INV-08)
	if closure == nil {
		result.ClosureGate = "failed"
		conflictCauses = append(conflictCauses, "causal observation closure is missing")
	} else if closure.ClosureStatus != "closed" {
		result.ClosureGate = "failed"
		reasons := "closure is open"
		if len(closure.IncompleteReasons) > 0 {
			reasons += ": " + strings.Join(closure.IncompleteReasons, ", ")
		}
		conflictCauses = append(conflictCauses, reasons)
	} else if delta != nil {
		// (a) Positive dependencies intersection
		for _, changed := range delta.ChangedPaths {
			for _, ref := range closure.PositiveDependencies.DocumentRevisionRefs {
				if changed == ref || strings.Contains(ref, changed) || strings.Contains(changed, ref) {
					result.ClosureGate = "failed"
					conflictCauses = append(conflictCauses, "workspace delta intersects positive dependency: "+changed)
					affectedScopeMap[changed] = true
				}
			}
		}

		// (b) Negative observations intersection
		for _, neg := range closure.NegativeObservations {
			pathsToCheck := append([]string{}, delta.AddedPaths...)
			pathsToCheck = append(pathsToCheck, delta.ModifiedPaths...)
			for _, p := range pathsToCheck {
				if neg.ScopeRef != "" && (p == neg.ScopeRef || strings.HasPrefix(p, neg.ScopeRef)) {
					result.ClosureGate = "failed"
					conflictCauses = append(conflictCauses, "workspace delta satisfies negative observation in scope: "+neg.ScopeRef)
					affectedScopeMap[p] = true
				}
			}
		}

		// (c) Membership observations intersection
		for _, mem := range closure.MembershipObservations {
			pathsToCheck := append([]string{}, delta.AddedPaths...)
			pathsToCheck = append(pathsToCheck, delta.DeletedPaths...)
			for _, p := range pathsToCheck {
				if mem.ContainerRef != "" && (p == mem.ContainerRef || strings.HasPrefix(p, mem.ContainerRef)) {
					result.ClosureGate = "failed"
					conflictCauses = append(conflictCauses, "workspace delta changes membership of container: "+mem.ContainerRef)
					affectedScopeMap[p] = true
				}
			}
		}

		// (d) Dependency frontiers intersection
		for _, frontier := range closure.DependencyFrontiers {
			for _, p := range delta.ChangedPaths {
				if frontier.BoundaryRef != "" && (p == frontier.BoundaryRef || strings.HasPrefix(p, frontier.BoundaryRef)) {
					result.ClosureGate = "failed"
					conflictCauses = append(conflictCauses, "workspace delta crosses dependency frontier: "+frontier.BoundaryRef)
					affectedScopeMap[p] = true
				}
			}
		}
	}

	// 3. Evidence Gate (Raw §18.1, INV-03)
	if mapIR == nil || len(mapIR.Steps) == 0 {
		result.EvidenceGate = "failed"
		conflictCauses = append(conflictCauses, "map has no verified steps")
	} else {
		for _, step := range mapIR.Steps {
			if step.Anchor.RepoRelativePath == "" || len(step.EvidenceRefs) == 0 {
				result.EvidenceGate = "failed"
				conflictCauses = append(conflictCauses, "step "+step.StepID+" is missing ground evidence")
				break
			}
		}
	}

	// 4. Semantic Atomicity Gate (Raw §18.1, D13)
	if mapIR != nil {
		seenOrdinals := make(map[int]bool)
		for _, step := range mapIR.Steps {
			if seenOrdinals[step.Ordinal] {
				result.SemanticAtomicityGate = "failed"
				conflictCauses = append(conflictCauses, "duplicate step ordinal in map")
				break
			}
			seenOrdinals[step.Ordinal] = true
		}
	}

	// 5. Task Relevance Gate (Raw §18.1, D2)
	if intent != nil && mapIR != nil {
		if mapIR.Task.TaskID != intent.TaskID || mapIR.Task.IntentRevision != intent.Revision {
			result.TaskRelevanceGate = "failed"
			conflictCauses = append(conflictCauses, "map task context does not match intent")
		}
	}

	// 6. Comprehension Gate (Raw §18.1, D6)
	if mapIR != nil {
		if mapIR.Summary.Requested == "" || mapIR.Summary.Current == "" {
			result.ComprehensionGate = "failed"
			conflictCauses = append(conflictCauses, "summary statement is missing")
		}
	}

	allPassed := result.SnapshotGate == "passed" &&
		result.ClosureGate == "passed" &&
		result.EvidenceGate == "passed" &&
		result.SemanticAtomicityGate == "passed" &&
		result.TaskRelevanceGate == "passed" &&
		result.ComprehensionGate == "passed"

	if allPassed {
		result.Eligibility = "passed"
		return result, nil
	}

	result.Eligibility = "rejected"

	affectedScope := make([]string, 0, len(affectedScopeMap))
	for p := range affectedScopeMap {
		affectedScope = append(affectedScope, p)
	}
	sort.Strings(affectedScope)

	latestSnapID := ""
	if liveHeadSnapshot != nil {
		latestSnapID = liveHeadSnapshot.SnapshotID
	} else if delta != nil {
		latestSnapID = delta.ToSnapshotID
	}

	lastVerifiedGenID := ""
	if mapIR != nil {
		lastVerifiedGenID = mapIR.GenerationID
	}

	gap := &VerifiedGap{
		Freshness:         "last_verified",
		Activity:          "editing",
		LastVerifiedGenID: lastVerifiedGenID,
		LatestSnapshotID:  latestSnapID,
		AffectedScope:     affectedScope,
		AnalysisLagMs:     0,
		PendingRevisions:  0,
		IntersectedCauses: conflictCauses,
		Timestamp:         time.Now().UTC(),
	}

	return result, gap
}

// EvaluateSettlement evaluates whether the map satisfies Settlement Gate requirements (Raw §10.11, §18.1, INV-24, D27, D31).
func (g *PublicationGate) EvaluateSettlement(mapIR *SemanticMapIR) SettlementEvaluation {
	if mapIR == nil {
		return SettlementEvaluation{Gate: "pending", BlockingObligationRefs: []string{}}
	}

	// Q1 or Q2 cannot pass settlement regardless of verified obligations (VS04-A5, Raw §18.1)
	if mapIR.Quality.Stage != "Q3" && mapIR.Quality.Stage != "Q4" {
		return SettlementEvaluation{
			Gate:                   "pending",
			BlockingObligationRefs: []string{},
		}
	}

	var blockingRefs []string
	for _, ob := range mapIR.Quality.CriticalObligations {
		if ob.Required && ob.Status != "verified" {
			blockingRefs = append(blockingRefs, ob.ObligationID)
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
