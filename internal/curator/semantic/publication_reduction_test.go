package semantic

import (
	"strings"
	"testing"
	"time"

	"codeflow/internal/analyzer/workspace"
	"codeflow/internal/collector/evidence"
	"codeflow/internal/collector/slicing"
)

func TestVS03PublicationGateRejectsIdentityDriftAndReportsMeasuredGap(t *testing.T) {
	gate := NewPublicationGate()
	mapIR := newPublicationTestMap("basis-a", "snap-a", "task-a", 4)
	closure := newPublicationTestClosure("basis-a", "read-a", "closure-a", "task-a", 4)
	input := PublicationInput{
		Map:                          mapIR,
		Closure:                      closure,
		Delta:                        &workspace.WorkspaceDelta{FromSnapshotID: "snap-a", ToSnapshotID: "snap-b"},
		CapturedSnapshot:             &workspace.WorkspaceSnapshot{SnapshotID: "snap-a", ComputedBasisID: "basis-a", WorkspaceEpoch: 4, RootTreeID: "tree-a", RepositoryID: "repo-a", WorktreeID: "worktree-a"},
		LiveHeadSnapshot:             &workspace.WorkspaceSnapshot{SnapshotID: "snap-b", ComputedBasisID: "basis-b", WorkspaceEpoch: 4, RootTreeID: "tree-b", RepositoryID: "repo-a", WorktreeID: "worktree-a"},
		Intent:                       &TaskIntent{TaskID: "task-a", Revision: 4},
		RepositoryID:                 "repo-a",
		WorktreeID:                   "worktree-a",
		QueryHash:                    "query-a",
		ExpectedPreviousGenerationID: "gen-previous",
		Metrics:                      PublicationMetrics{LagMs: 17, PendingRevisions: 3, MeasuredAt: time.Unix(100, 0).UTC()},
		GenerationID:                 mapIR.GenerationID,
		ArtifactDigests:              map[string]string{"semanticMap": strings.Repeat("a", 64)},
	}

	result, gap := gate.EvaluateCurrent(input)
	if result.Eligibility == "passed" || gap == nil {
		t.Fatalf("identity drift must reject current publication, result=%+v gap=%+v", result, gap)
	}
	if result.SnapshotGate != "failed" {
		t.Fatalf("expected snapshot gate failure, got %+v", result)
	}
	if gap.AnalysisLagMs != 17 || gap.PendingRevisions != 3 {
		t.Fatalf("gap metrics must come from measured input, got lag=%d pending=%d", gap.AnalysisLagMs, gap.PendingRevisions)
	}
	if !strings.Contains(strings.Join(gap.IntersectedCauses, " "), "snapshot") {
		t.Fatalf("gap must explain identity failure, got %v", gap.IntersectedCauses)
	}
}

func TestVS03PublicationGate_AllStrictIdentityAndObservationGatesPass(t *testing.T) {
	gate := NewPublicationGate()
	mapIR := newPublicationTestMap("basis-a", "snap-a", "task-a", 2)
	closure := newPublicationTestClosure("basis-a", "read-a", "closure-a", "task-a", 2)
	closure.ClosureDigest, _ = ClosureDigest(*closure)
	snapshot := &workspace.WorkspaceSnapshot{SchemaID: "https://codeflow.local/schemas/rflsc.workspace-snapshot.v2.schema.json", SchemaVersion: 2, SnapshotID: "snap-a", ComputedBasisID: "basis-a", WorkspaceEpoch: 2, RootTreeID: "tree-a", RepositoryID: "repo-a", WorktreeID: "worktree-a"}
	snapshot.DependencyFingerprint = "dep-a"
	mapIR.Basis.DependencyFingerprint = "dep-a"
	input := PublicationInput{
		Map: mapIR, Closure: closure,
		Delta:            &workspace.WorkspaceDelta{FromSnapshotID: "snap-a", ToSnapshotID: "snap-a"},
		CapturedSnapshot: snapshot, LiveHeadSnapshot: snapshot,
		Intent:       &TaskIntent{TaskID: "task-a", Revision: 2, Mode: "feature"},
		RepositoryID: "repo-a", WorktreeID: "worktree-a", DependencyFingerprint: "dep-a", QueryHash: "query-a", GenerationID: mapIR.GenerationID,
		ArtifactDigests: map[string]string{"semanticMap": strings.Repeat("a", 64), "evidenceIndex": strings.Repeat("b", 64)},
		Metrics:         PublicationMetrics{LagMs: 9, PendingRevisions: 0, MeasuredAt: time.Unix(102, 0).UTC()},
	}
	canonical := evidence.Result{
		SchemaID: evidence.AnalyzerResultSchemaID, SchemaVersion: evidence.SchemaVersion, RequestID: "request-a", Operation: "detect",
		AdapterVersion: "adapter/1", AnalyzerRevision: "analyzer/1", WorkspaceEpoch: 2, ComputedBasisID: "basis-a", SnapshotID: "snap-a", SnapshotTreeDigest: "tree-a", DependencyFingerprint: "dep-a",
		ReadSet:    evidence.AnalysisReadSet{SchemaID: evidence.ReadSetSchemaID, SchemaVersion: evidence.SchemaVersion, ReadSetID: "read-a", ComputedBasisID: "basis-a", WorkspaceEpoch: 2, Documents: []evidence.ReadDocument{}, NegativeObservations: []evidence.Observation{}, MembershipObservations: []evidence.Observation{{Kind: "membership", Path: ".", ValueHash: "membership-a", Measured: true}}, DependencyFrontiers: []evidence.Observation{}},
		Closure:    evidence.ObservationClosure{SchemaID: evidence.ClosureSchemaID, SchemaVersion: evidence.SchemaVersion, ClosureID: "closure-a", AnalysisReadSetID: "read-a", ComputedBasisID: "basis-a", WorkspaceEpoch: 2, Status: "closed", RequiredObservations: []string{"membership"}, MeasuredObservations: []string{"membership"}, NegativeObservations: []evidence.Observation{}, MembershipObservations: []evidence.Observation{{Kind: "membership", Path: ".", ValueHash: "membership-a", Measured: true}}, DependencyFrontiers: []evidence.Observation{}, ClosureDigest: "closure-digest-a"},
		Capability: evidence.CapabilityProfile{Adapter: "typescript", AnalyzerRevision: "analyzer/1", Features: []string{"snapshot_bytes"}},
		Coverage:   evidence.Coverage{IncludedSourceRoots: []string{"."}, Measured: true}, Payload: []byte(`{"language":"typescript","confident":true}`), Diagnostics: []evidence.Diagnostic{},
	}
	request, err := evidence.NewAnalyzerRequest(canonical.RequestID, canonical.Operation, evidence.SnapshotInput{SnapshotID: "snap-a", ComputedBasisID: "basis-a", RootTreeID: "tree-a", DependencyFingerprint: "dep-a", ConfigurationFingerprint: "cfg-a", WorkspaceEpoch: 2, Documents: []evidence.SnapshotDocument{}}, nil, []string{"membership"})
	if err != nil {
		t.Fatal(err)
	}
	closure.CanonicalResult = &canonical
	closure.ClosureDigest = canonical.Closure.ClosureDigest
	input.AnalysisRequest = &request
	input.AnalysisResult = &canonical
	input.CapabilityProfileDigest, err = CanonicalCapabilityProfileDigest(canonical.Capability)
	if err != nil {
		t.Fatal(err)
	}
	result, gap := gate.EvaluateCurrent(input)
	if result.Eligibility != "passed" || gap != nil {
		t.Fatalf("complete strict input should pass, result=%+v gap=%+v", result, gap)
	}
	for name, mutate := range map[string]func(*workspace.WorkspaceDelta){
		"index":           func(delta *workspace.WorkspaceDelta) { delta.IndexChanged = true },
		"resolution":      func(delta *workspace.WorkspaceDelta) { delta.ResolutionChanged = true },
		"capability":      func(delta *workspace.WorkspaceDelta) { delta.CapabilityChanged = true },
		"configuration":   func(delta *workspace.WorkspaceDelta) { delta.ConfigurationChanged = true },
		"public-contract": func(delta *workspace.WorkspaceDelta) { delta.PublicContractChanged = true },
	} {
		t.Run("reject-changed-"+name, func(t *testing.T) {
			mutated := input
			delta := *input.Delta
			mutate(&delta)
			mutated.Delta = &delta
			result, gap := gate.EvaluateCurrent(mutated)
			if result.Eligibility == "passed" || gap == nil || result.ClosureGate != "failed" {
				t.Fatalf("%s change after closure must fail closed: result=%+v gap=%+v", name, result, gap)
			}
		})
	}
	for name, digest := range map[string]string{"missing": "", "forged": strings.Repeat("f", 64)} {
		t.Run(name, func(t *testing.T) {
			mutated := input
			mutated.CapabilityProfileDigest = digest
			result, gap := gate.EvaluateCurrent(mutated)
			if result.Eligibility == "passed" || gap == nil || result.ClosureGate != "failed" {
				t.Fatalf("%s capability digest must fail closed: result=%+v gap=%+v", name, result, gap)
			}
		})
	}
}

func TestVS03PublicationGateRejectsOpenClosureAndChangedMembership(t *testing.T) {
	gate := NewPublicationGate()
	mapIR := newPublicationTestMap("basis-a", "snap-a", "task-a", 1)
	closure := newPublicationTestClosure("basis-a", "read-a", "closure-a", "task-a", 1)
	closure.ClosureStatus = "open"
	closure.IncompleteReasons = []string{"membership observation unmeasured"}
	input := PublicationInput{
		Map: mapIR, Closure: closure,
		Delta:            &workspace.WorkspaceDelta{FromSnapshotID: "snap-a", ToSnapshotID: "snap-b", AddedPaths: []string{"pkg/new.go"}, ChangedPaths: []string{"pkg/new.go"}, MembershipChanged: true},
		CapturedSnapshot: &workspace.WorkspaceSnapshot{SnapshotID: "snap-a", ComputedBasisID: "basis-a", WorkspaceEpoch: 1, RootTreeID: "tree-a", RepositoryID: "repo-a", WorktreeID: "worktree-a"},
		LiveHeadSnapshot: &workspace.WorkspaceSnapshot{SnapshotID: "snap-a", ComputedBasisID: "basis-a", WorkspaceEpoch: 1, RootTreeID: "tree-a", RepositoryID: "repo-a", WorktreeID: "worktree-a"},
		Intent:           &TaskIntent{TaskID: "task-a", Revision: 1}, RepositoryID: "repo-a", WorktreeID: "worktree-a", QueryHash: "query-a",
		Metrics:      PublicationMetrics{LagMs: 5, PendingRevisions: 1, MeasuredAt: time.Unix(101, 0).UTC()},
		GenerationID: mapIR.GenerationID, ArtifactDigests: map[string]string{"semanticMap": strings.Repeat("a", 64)},
	}
	result, gap := gate.EvaluateCurrent(input)
	if result.Eligibility == "passed" || gap == nil || result.ClosureGate != "failed" {
		t.Fatalf("open/incomplete closure must reject current, result=%+v gap=%+v", result, gap)
	}
	if !strings.Contains(strings.Join(gap.IntersectedCauses, " "), "membership") {
		t.Fatalf("gap must retain closure reason, got %v", gap.IntersectedCauses)
	}
}

func TestVS03SchedulerRetainsNewestCheckpointWhenQueueIsFull(t *testing.T) {
	s := NewCoalescingScheduler(CoalescingConfig{MaxQueueSize: 1})
	defer s.Close()
	first := &workspace.WorkspaceSnapshot{SnapshotID: "snap-first"}
	latest := &workspace.WorkspaceSnapshot{SnapshotID: "snap-latest"}
	s.NotifyEdit(first)
	s.triggerCheckpoint("test")
	s.NotifyEdit(latest)
	s.triggerCheckpoint("test")
	seen := map[string]bool{}
	for len(seen) < 2 {
		select {
		case snap := <-s.Checkpoints():
			if snap != nil {
				seen[snap.SnapshotID] = true
			}
		default:
			if !seen[latest.SnapshotID] {
				t.Fatalf("newest checkpoint was dropped, seen=%v", seen)
			}
			return
		}
	}
}

func newPublicationTestMap(basis, snapshot, task string, rev int) *SemanticMapIR {
	return &SemanticMapIR{
		SchemaID: SemanticMapSchemaID, SchemaVersion: 2, MapID: "map-a", GenerationID: "gen-a", ComputedBasisID: basis,
		ValidatedAgainstSnapshotID: snapshot, PublicationKind: "checkpoint", Freshness: "historical", Settlement: "pending", EnrichmentStatus: "available",
		Task:    MapTaskContext{TaskID: task, IntentRevision: rev, Mode: "feature"},
		Basis:   MapBasisContext{RepositoryID: "repo-a", WorktreeID: "worktree-a", WorkspaceEpoch: int64(rev), ComputedWorkspaceSnapshotID: snapshot, ComputedBasisID: basis, SnapshotTreeID: "tree-a", AnalysisReadSetID: "read-a", CausalObservationClosureID: "closure-a"},
		Summary: MapSummary{Requested: "request", Current: "candidate"}, Quality: MapQuality{Stage: "Q3"}, Authority: "candidate",
		Steps:    []SemanticStep{{StepID: "step-a", StructuralIdentity: "task-a/source/a->target/a:call", Ordinal: 1, Name: "step", Anchor: slicing.Anchor{RepoRelativePath: "pkg/a.go", ByteRange: [2]int{0, 1}}, EvidenceRefs: []string{"evidence-a"}}},
		Evidence: []SemanticEvidence{{EvidenceID: "evidence-a", Kind: "source", SourceAuthority: "code", ComputedBasisID: basis, SnapshotID: snapshot, ValidationStatus: "verified", Anchor: slicing.Anchor{RepoRelativePath: "pkg/a.go", ByteRange: [2]int{0, 1}}}},
	}
}

func newPublicationTestClosure(basis, readSet, closureID, task string, rev int) *CausalObservationClosure {
	return &CausalObservationClosure{SchemaID: ObservationClosureSchemaID, SchemaVersion: 2, ClosureID: closureID, ComputedBasisID: basis, TaskIntentRevision: rev, NormalizedQueryHash: "query-a", AnalysisReadSetID: readSet, ClosureStatus: "closed", PositiveDependencies: PositiveDependencies{DocumentRevisionRefs: []string{"pkg/a.go@rev-a"}, ConfigurationFingerprint: "cfg-a"}, NegativeObservations: []NegativeObservation{}, MembershipObservations: []MembershipObservation{}, DependencyFrontiers: []DependencyFrontier{}, ClosureDigest: "closure-digest-a"}
}
