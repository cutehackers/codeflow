// Package publication contains the executable acceptance runners for the R2
// live publication contract. The runners use the public workspace,
// semantic, storage, and FlowView seams so the evidence registry cannot pass
// by checking only caller-created JSON values.
package publication

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"codeflow/internal/contractharness"
	"codeflow/internal/evidence"
	"codeflow/internal/flowview"
	"codeflow/internal/fusion"
	"codeflow/internal/protocol"
	"codeflow/internal/semantic"
	"codeflow/internal/slicing"
	"codeflow/internal/storage"
	"codeflow/internal/watch"
	"codeflow/internal/workspace"
)

// Evidence identifies the implementation criterion and the immutable
// artifacts used by its public-seam check.  The contract harness adds the
// execution package, test binary, and subtest ID after this value is returned.
type Evidence struct {
	Criterion             string
	ImplementationTestID  string
	ImplementationPackage string
	SnapshotTreeDigest    string
	ObjectRefs            []string
	Trace                 *TraceEvidence
	ViewCorpus            *ViewCorpusEvidence
}

// TraceEvidence is the measured, same-trace evidence for VS03-A13.  The two
// distributions are intentionally kept separate so one fast phase cannot hide
// a slow phase in a combined average.
type TraceEvidence struct {
	TraceIDs              []string  `json:"traceIds"`
	ActivitySamplesMs     []float64 `json:"activitySamplesMs"`
	CurrentOrGapSamplesMs []float64 `json:"currentOrGapSamplesMs"`
	ActivityP95Ms         float64   `json:"activityP95Ms"`
	CurrentOrGapP95Ms     float64   `json:"currentOrGapP95Ms"`
	Profile               string    `json:"profile"`
}

// ViewCorpusEvidence is the measured versioned corpus for VS03-A14.
// Identity-loss updates are reported separately and are excluded from the
// preservation denominator by contract.
type ViewCorpusEvidence struct {
	CorpusVersion        string  `json:"corpusVersion"`
	EligibleUpdates      int     `json:"eligibleUpdates"`
	PreservedUpdates     int     `json:"preservedUpdates"`
	ExcludedIdentityLoss int     `json:"excludedIdentityLoss"`
	Numerator            int     `json:"numerator"`
	Denominator          int     `json:"denominator"`
	PreservationPercent  float64 `json:"preservationPercent"`
}

var implementationTestIDs = map[string]string{
	"VS03-A1":  "codeflow/internal/verification/publication.TestRFLSCR2VS03_A01",
	"VS03-A2":  "codeflow/internal/verification/publication.TestRFLSCR2VS03_A02",
	"VS03-A3":  "codeflow/internal/verification/publication.TestRFLSCR2VS03_A03",
	"VS03-A4":  "codeflow/internal/verification/publication.TestRFLSCR2VS03_A04",
	"VS03-A5":  "codeflow/internal/verification/publication.TestRFLSCR2VS03_A05",
	"VS03-A6":  "codeflow/internal/verification/publication.TestRFLSCR2VS03_A06",
	"VS03-A7":  "codeflow/internal/verification/publication.TestRFLSCR2VS03_A07",
	"VS03-A8":  "codeflow/internal/verification/publication.TestRFLSCR2VS03_A08",
	"VS03-A9":  "codeflow/internal/verification/publication.TestRFLSCR2VS03_A09",
	"VS03-A10": "codeflow/internal/verification/publication.TestRFLSCR2VS03_A10",
	"VS03-A11": "codeflow/internal/verification/publication.TestRFLSCR2VS03_A11",
	"VS03-A12": "codeflow/internal/verification/publication.TestRFLSCR2VS03_A12",
	"VS03-A13": "codeflow/internal/verification/publication.TestRFLSCR2VS03_A13",
	"VS03-A14": "codeflow/internal/verification/publication.TestRFLSCR2VS03_A14",
	"VS03-A15": "codeflow/internal/verification/publication.TestRFLSCR2VS03_A15",
	"VS03-A16": "codeflow/internal/verification/publication.TestRFLSCR2VS03_A16",
	"VS03-A17": "codeflow/internal/verification/publication.TestRFLSCR2VS03_A17",
	"VS03-A18": "codeflow/internal/verification/publication.TestRFLSCR2VS03_A18",
	"VS03-A19": "codeflow/internal/verification/publication.TestRFLSCR2VS03_A19",
	"VS03-A20": "codeflow/internal/verification/publication.TestRFLSCR2VS03_A20",
}

const (
	vs03RepositoryID = "repo-vs03"
	vs03WorktreeID   = "worktree-vs03"
	vs03TaskID       = "task-vs03"
	vs03QueryHash    = "1111111111111111111111111111111111111111111111111111111111111111"
	vs03Config       = "config-vs03"
)

func evidenceFor(criterion, tree string, refs ...string) Evidence {
	all := []string{"snapshot:snapshot-vs03", "tree:" + tree, "basis:basis-vs03"}
	all = append(all, refs...)
	return Evidence{Criterion: criterion, ImplementationTestID: implementationTestIDs[criterion], ImplementationPackage: "codeflow/internal/verification/publication", SnapshotTreeDigest: tree, ObjectRefs: all}
}

func newWorkspaceEngine(t *testing.T, epoch int64) (*workspace.SnapshotEngine, string) {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "service.go"), []byte("package service\n\nfunc Submit() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	engine, err := workspace.NewSnapshotEngine(root, epoch)
	if err != nil {
		t.Fatal(err)
	}
	return engine, root
}

// RunA01 proves that activity acknowledgement is updated by edit ingress and
// is observable even before any semantic generation exists.
func RunA01(t *testing.T) Evidence {
	t.Helper()
	engine, _ := newWorkspaceEngine(t, 3)
	if before := engine.CurrentActivity(); before.PendingRevisions != 0 || before.CurrentSnapshotID != "" {
		t.Fatalf("activity must start independent of semantic generation: %+v", before)
	}
	_, snap, err := engine.ApplyVersionedEdit(context.Background(), workspace.EditRequest{Path: "service.go", Content: []byte("package service\n\nfunc Submit() { changed() }\n"), DocumentVersion: 1, Source: workspace.SourceIDEVersioned})
	if err != nil {
		t.Fatal(err)
	}
	act := engine.CurrentActivity()
	if act.Activity != "editing" || act.PendingRevisions < 1 || act.CurrentSnapshotID != snap.SnapshotID || act.WorkspaceEpoch != 3 || act.Timestamp.IsZero() {
		t.Fatalf("edit acknowledgement is incomplete: activity=%+v snapshot=%+v", act, snap)
	}
	return evidenceFor("VS03-A1", snap.RootTreeID, "activity:"+snap.SnapshotID, "revision:"+snap.ChangedEntries[0].DocumentRevisionID)
}

// RunA02 proves an active feature query and the production edit ingress reach
// the always-on FlowView consumer and emit a durable current-or-gap result.
func RunA02(t *testing.T) Evidence {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "service.go"), []byte("package service\n\nfunc HandleSubmit() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	server, err := flowview.NewServer(flowview.Config{RepoRoot: root, Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	requestText := "show HandleSubmit flow"
	intent, err := semantic.NormalizeTaskIntent(requestText, semantic.IntentOptions{TaskID: "task-vs03-a02", Revision: 1, Mode: "feature"})
	if err != nil || intent == nil || intent.IntentStatus != "parsed" {
		t.Fatalf("active task intent was not normalized: intent=%+v err=%v", intent, err)
	}
	if err := server.RememberTaskQuery(&semantic.TaskViewQuery{SchemaID: "https://codeflow.local/schemas/task-view-query.schema.json", SchemaVersion: 1, Mode: "feature", Feature: &semantic.FeatureQueryParams{Request: requestText, EntrySymbol: "service.go#HandleSubmit", Domain: "service"}}, requestText); err != nil {
		t.Fatal(err)
	}
	server.Start()
	defer server.Shutdown(context.Background())
	_, snap, err := server.SubmitVersionedEdit(context.Background(), workspace.EditRequest{Path: "service.go", Content: []byte("package service\nfunc HandleSubmit(){background()}\n"), DocumentVersion: 1, Source: workspace.SourceIDEVersioned})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	var activity workspace.ActivityStatus
	ledgerPath := filepath.Join(root, ".codeflow", "semantics", "events", "event-ledger.jsonl")
	for time.Now().Before(deadline) {
		activity = server.CurrentActivity()
		if pipelineErr := server.LastPipelineError(); pipelineErr != nil {
			t.Fatalf("automatic consumer reported a terminal pipeline error: %v; activity=%+v", pipelineErr, activity)
		}
		ledger, readErr := os.ReadFile(ledgerPath)
		if !activity.CurrentOrGapAt.IsZero() && readErr == nil && (strings.Contains(string(ledger), "generation.gap") || strings.Contains(string(ledger), "generation.published")) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if activity.CurrentSnapshotID != snap.SnapshotID || activity.TraceID == "" || activity.CurrentOrGapAt.IsZero() || activity.CurrentOrGapLatencyMs < 0 {
		t.Fatalf("automatic consumer did not settle a same-snapshot current-or-gap result: pipelineErr=%v activity=%+v snapshot=%+v", server.LastPipelineError(), activity, snap)
	}
	ledger, err := os.ReadFile(ledgerPath)
	if err != nil || (!strings.Contains(string(ledger), "generation.gap") && !strings.Contains(string(ledger), "generation.published")) {
		t.Fatalf("automatic consumer did not persist current-or-gap event in canonical ledger: err=%v ledger=%q", err, ledger)
	}
	return evidenceFor("VS03-A2", snap.RootTreeID, "edit-ingress:"+snap.SnapshotID, "consumer:always-on", "current-or-gap:measured", "event-ledger:current-or-gap")
}

// RunA03 proves latest-wins scheduling while the production SnapshotEngine
// computes the transition dimensions from real filesystem snapshots.
func RunA03(t *testing.T) Evidence {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"service.go": "package service\nfunc Submit(){initial()}\n",
		"rename.go":  "package service\nfunc RenameSource(){}\n",
		"removed.go": "package service\nfunc Removed(){}\n",
		"go.mod":     "module example.com/vs03\n\ngo 1.22\n\nrequire example.com/dep v1.0.0\n",
	}
	for path, content := range files {
		if err := os.WriteFile(filepath.Join(root, path), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	engine, err := workspace.NewSnapshotEngine(root, 1)
	if err != nil {
		t.Fatal(err)
	}
	scheduler := semantic.NewCoalescingScheduler(semantic.CoalescingConfig{QuietWindow: 10 * time.Millisecond, MaxWait: 100 * time.Millisecond, MaxQueueSize: 1})
	defer scheduler.Close()
	_, first, err := engine.ApplyVersionedEdit(context.Background(), workspace.EditRequest{Path: "service.go", Content: []byte("package service\nfunc Submit(){first()}\n"), DocumentVersion: 1, Source: workspace.SourceIDEVersioned})
	if err != nil {
		t.Fatal(err)
	}
	_, second, err := engine.ApplyVersionedEdit(context.Background(), workspace.EditRequest{Path: "service.go", Content: []byte("package service\nfunc Submit(){second()}\n"), DocumentVersion: 2, Source: workspace.SourceIDEVersioned})
	if err != nil {
		t.Fatal(err)
	}
	scheduler.NotifyEdit(first)
	scheduler.NotifyEdit(second)
	// Consume the public checkpoint selected after the quiet window. The second
	// notification must replace the first one in the same coalescing cycle.
	var latest *workspace.WorkspaceSnapshot
	select {
	case latest = <-scheduler.Checkpoints():
	case <-time.After(500 * time.Millisecond):
		t.Fatal("latest-wins checkpoint was not selected")
	}
	if latest == nil || latest.SnapshotID != second.SnapshotID || latest.Sequence <= first.Sequence {
		t.Fatalf("latest checkpoint was not retained: first=%+v second=%+v latest=%+v", first, second, latest)
	}
	delta, err := engine.ComputeDelta(first.SnapshotID, second.SnapshotID)
	if err != nil || delta == nil || len(delta.ModifiedPaths) != 1 || delta.ModifiedPaths[0] != "service.go" {
		t.Fatalf("revision delta was not preserved: delta=%+v err=%v", delta, err)
	}
	// Change the repository itself, then let the production whole-tree
	// reconciliation discover add/delete/rename without caller-supplied flags.
	if err := os.Rename(filepath.Join(root, "rename.go"), filepath.Join(root, "renamed.go")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "removed.go")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "added.go"), []byte("package service\nfunc Added(){}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/vs03\n\ngo 1.22\n\nrequire example.com/dep v1.1.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "service.go"), []byte("package service\n\nimport \"context\"\n\nfunc Submit(ctx context.Context) error {\n\treturn nil\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	third, err := engine.Reconcile(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	transition, err := engine.ComputeDelta(second.SnapshotID, third.SnapshotID)
	if err != nil || transition == nil {
		t.Fatalf("real reconciliation delta unavailable: delta=%+v err=%v", transition, err)
	}
	if !containsString(transition.AddedPaths, "added.go") || !containsString(transition.AddedPaths, "renamed.go") || !containsString(transition.DeletedPaths, "removed.go") || !containsString(transition.DeletedPaths, "rename.go") {
		t.Fatalf("real add/delete/rename transition was not measured: %+v", transition)
	}
	if !containsString(transition.RenamedPaths, "rename.go") || !containsString(transition.RenamedPaths, "renamed.go") || !transition.MembershipChanged || !transition.ResolutionChanged || !transition.IndexChanged || !transition.PublicContractChanged {
		t.Fatalf("real membership/rename/resolution/index/public-contract dimensions were not measured: %+v", transition)
	}
	scheduler.NotifyEdit(first)
	scheduler.NotifyEdit(second)
	scheduler.NotifyEdit(third)
	select {
	case selected := <-scheduler.Checkpoints():
		if selected == nil || selected.SnapshotID != third.SnapshotID {
			t.Fatalf("coalescer did not retain the newest real transition: selected=%+v want=%s", selected, third.SnapshotID)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("latest real transition was not selected")
	}
	return evidenceFor("VS03-A3", third.RootTreeID, "checkpoint:"+third.SnapshotID, "delta:modified:service.go,go.mod", "delta:added:added.go,renamed.go", "delta:deleted:removed.go,rename.go", "delta:renamed:rename.go->renamed.go", "delta:membership", "delta:resolution", "delta:index", "delta:public-contract")
}

// RunA04 exercises the six publication gates with a complete input and then
// rejects a cross-artifact identity drift before current eligibility.
func RunA04(t *testing.T) Evidence {
	t.Helper()
	fixture := publicationFixture(t)
	request, vs02Result, canonicalClosure := canonicalVS02Evidence(t, fixture.snapshot)
	fixture.input.AnalysisRequest = &request
	fixture.input.AnalysisResult = &vs02Result
	fixture.input.Closure = canonicalClosure
	fixture.input.Map.Basis.AnalysisReadSetID = canonicalClosure.AnalysisReadSetID
	fixture.input.Map.Basis.CausalObservationClosureID = canonicalClosure.ClosureID
	fixture.input.CapabilityProfileDigest, _ = semantic.CanonicalCapabilityProfileDigest(vs02Result.Capability)
	gate := semantic.NewPublicationGate()
	result, gap := gate.EvaluateCurrent(fixture.input)
	if gap != nil || result.Eligibility != "passed" || result.SnapshotGate != "passed" || result.ClosureGate != "passed" || result.EvidenceGate != "passed" || result.SemanticAtomicityGate != "passed" || result.TaskRelevanceGate != "passed" || result.ComprehensionGate != "passed" {
		t.Fatalf("complete six-gate publication input did not pass: result=%+v gap=%+v", result, gap)
	}
	mutated := fixture.input
	mutated.QueryHash = "query-drift"
	result, gap = gate.EvaluateCurrent(mutated)
	if result.Eligibility == "passed" || gap == nil || result.ClosureGate != "failed" {
		t.Fatalf("cross-artifact query identity drift was accepted: result=%+v gap=%+v", result, gap)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*semantic.PublicationInput)
	}{
		{name: "missing-request", mutate: func(in *semantic.PublicationInput) { in.AnalysisRequest = nil }},
		{name: "missing-result", mutate: func(in *semantic.PublicationInput) { in.AnalysisResult = nil }},
		{name: "workspace-epoch-drift", mutate: func(in *semantic.PublicationInput) {
			copy := *in.AnalysisResult
			copy.WorkspaceEpoch++
			in.AnalysisResult = &copy
			in.Closure.CanonicalResult = &copy
		}},
		{name: "measured-observation-dropped", mutate: func(in *semantic.PublicationInput) {
			copy := *in.AnalysisResult
			copy.Closure.MeasuredObservations = nil
			in.AnalysisResult = &copy
			in.Closure.CanonicalResult = &copy
		}},
		{name: "analyzer-capability-revision-drift", mutate: func(in *semantic.PublicationInput) {
			copy := *in.AnalysisResult
			copy.Capability.AnalyzerRevision = "analyzer-vs03/drift"
			in.AnalysisResult = &copy
			in.Closure.CanonicalResult = &copy
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := fixture.input
			tc.mutate(&in)
			got, gotGap := gate.EvaluateCurrent(in)
			if got.Eligibility == "passed" || gotGap == nil || got.ClosureGate != "failed" {
				t.Fatalf("canonical VS-02 %s was accepted after identity loss: result=%+v gap=%+v", tc.name, got, gotGap)
			}
		})
	}
	return evidenceFor("VS03-A4", fixture.tree, "gates:snapshot,closure,evidence,atomicity,relevance,comprehension", "identity:query-drift-rejected")
}

// RunA05 proves open closure and the measured dimensions from a real workspace
// transition produce a last-verified gap rather than current authority.
func RunA05(t *testing.T) Evidence {
	t.Helper()
	root := t.TempDir()
	for path, content := range map[string]string{
		"service.go": "package service\nfunc Submit(){initial()}\n",
		"rename.go":  "package service\nfunc RenameSource(){}\n",
		"removed.go": "package service\nfunc Removed(){}\n",
		"go.mod":     "module example.com/vs03\n\ngo 1.22\n\nrequire example.com/dep v1.0.0\n",
	} {
		if err := os.WriteFile(filepath.Join(root, path), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	engine, err := workspace.NewSnapshotEngine(root, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.BindWorkspaceIdentity(workspace.WorkspaceIdentity{RepositoryID: vs03RepositoryID, WorktreeID: vs03WorktreeID, ConfigurationFingerprint: "config-vs03-before"}); err != nil {
		t.Fatal(err)
	}
	_, before, err := engine.ApplyVersionedEdit(context.Background(), workspace.EditRequest{Path: "service.go", Content: []byte("package service\nfunc Submit(){before()}\n"), DocumentVersion: 1, Source: workspace.SourceIDEVersioned})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(root, "rename.go"), filepath.Join(root, "renamed.go")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "removed.go")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "added.go"), []byte("package service\nfunc Added(){}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "service.go"), []byte("package service\nfunc Submit(){after()}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/vs03\n\ngo 1.22\n\nrequire example.com/dep v1.1.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "service.go"), []byte("package service\n\nimport \"context\"\n\nfunc Submit(ctx context.Context) error {\n\treturn nil\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.SetWorkspaceIdentity(workspace.WorkspaceIdentity{RepositoryID: vs03RepositoryID, WorktreeID: vs03WorktreeID, ConfigurationFingerprint: "config-vs03-after"}); err != nil {
		t.Fatal(err)
	}
	after, err := engine.Reconcile(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	delta, err := engine.ComputeDelta(before.SnapshotID, after.SnapshotID)
	if err != nil || delta == nil {
		t.Fatalf("real transition delta unavailable: delta=%+v err=%v", delta, err)
	}
	if !containsString(delta.ModifiedPaths, "service.go") || !containsString(delta.ModifiedPaths, "go.mod") || !containsString(delta.AddedPaths, "added.go") || !containsString(delta.AddedPaths, "renamed.go") || !containsString(delta.DeletedPaths, "removed.go") || !containsString(delta.DeletedPaths, "rename.go") || !containsString(delta.RenamedPaths, "rename.go") || !containsString(delta.RenamedPaths, "renamed.go") || !delta.MembershipChanged || !delta.ResolutionChanged || !delta.IndexChanged || !delta.PublicContractChanged || !delta.ConfigurationChanged {
		t.Fatalf("real transition did not preserve measured delta dimensions: %+v", delta)
	}

	mapIR := candidateMap(after.ComputedBasisID, after.SnapshotID, "generation-vs03-a5", 1, "Q3")
	mapIR.Basis.WorkspaceEpoch = after.WorkspaceEpoch
	mapIR.Basis.SnapshotTreeID = after.RootTreeID
	mapIR.Basis.RepositoryID = after.RepositoryID
	mapIR.Basis.WorktreeID = after.WorktreeID
	mapIR.Basis.DependencyFingerprint = after.DependencyFingerprint
	mapIR.Basis.ConfigurationFingerprint = after.ConfigurationFingerprint
	mapIR.Evidence[0].ComputedBasisID = after.ComputedBasisID
	mapIR.Evidence[0].SnapshotID = after.SnapshotID
	closure := &semantic.CausalObservationClosure{
		SchemaID: semantic.ObservationClosureSchemaID, SchemaVersion: semantic.SemanticSchemaVersion,
		ClosureID: "closure-vs03-a5", ComputedBasisID: after.ComputedBasisID, TaskIntentRevision: 1,
		NormalizedQueryHash: vs03QueryHash, AnalysisReadSetID: "readset-vs03-a5", ClosureStatus: "open",
		PositiveDependencies:   semantic.PositiveDependencies{DocumentRevisionRefs: []string{"service.go"}, ConfigurationFingerprint: "config-vs03-before"},
		MembershipObservations: []semantic.MembershipObservation{{Kind: "directory_files", ContainerRef: ".", MembershipDigest: "membership-before"}},
		DependencyFrontiers:    []semantic.DependencyFrontier{{Direction: "callees", RootRef: "service.go", BoundaryRef: "service.go", GraphRevision: "graph-before"}},
		IncompleteReasons:      []string{"membership, dependency, and configuration observations are stale"},
	}
	closure.ClosureDigest, err = semantic.ClosureDigest(*closure)
	if err != nil {
		t.Fatal(err)
	}
	request, vs02Result, canonicalClosure := canonicalVS02Evidence(t, after)
	vs02Result.Closure.Status = "open"
	vs02Result.Closure.IncompleteReasons = []string{"membership, dependency, and configuration observations are stale"}
	canonicalClosure.ClosureStatus = "open"
	canonicalClosure.IncompleteReasons = append([]string(nil), vs02Result.Closure.IncompleteReasons...)
	canonicalClosure.ClosureDigest = vs02Result.Closure.ClosureDigest
	canonicalClosure.CanonicalResult = &vs02Result
	mapIR.Basis.AnalysisReadSetID = canonicalClosure.AnalysisReadSetID
	mapIR.Basis.CausalObservationClosureID = canonicalClosure.ClosureID
	activity := engine.CurrentActivity()
	result, gap := semantic.NewPublicationGate().EvaluateCurrent(semantic.PublicationInput{
		Map: mapIR, Closure: canonicalClosure, Delta: delta, CapturedSnapshot: after, LiveHeadSnapshot: after,
		Intent:       &semantic.TaskIntent{TaskID: vs03TaskID, Revision: 1, Mode: "feature", IntentStatus: "user_confirmed"},
		RepositoryID: after.RepositoryID, WorktreeID: after.WorktreeID, DependencyFingerprint: after.DependencyFingerprint,
		QueryHash: vs03QueryHash, GenerationID: mapIR.GenerationID,
		ArtifactDigests: map[string]string{"semanticMap": strings.Repeat("a", 64)},
		AnalysisRequest: &request, AnalysisResult: &vs02Result,
		CapabilityProfileDigest: func() string {
			digest, _ := semantic.CanonicalCapabilityProfileDigest(vs02Result.Capability)
			return digest
		}(),
		Metrics: semantic.PublicationMetrics{LagMs: activity.AnalysisLagMs, PendingRevisions: activity.PendingRevisions, MeasuredAt: activity.Timestamp, Activity: activity.Activity, TraceID: activity.TraceID},
	})
	if result.Eligibility == "passed" || result.ClosureGate != "failed" || gap == nil || gap.Freshness != "last_verified" || gap.AnalysisLagMs < 0 || gap.PendingRevisions < 0 || gap.TraceID == "" || len(gap.IntersectedCauses) == 0 {
		t.Fatalf("closure/delta gap was not measured: result=%+v gap=%+v activity=%+v", result, gap, activity)
	}
	return evidenceFor("VS03-A5", after.RootTreeID, "gap:last_verified", "gap:measured", "closure:open", "delta:modified-added-deleted-renamed-membership-resolution-index-public-contract-configuration")
}

// RunA06 exercises the independent Settlement Gate through Q1/Q2 pending,
// Q3 passed, and explicit Q3 failure states.
func RunA06(t *testing.T) Evidence {
	t.Helper()
	base := candidateMap("basis-vs03", "snapshot-vs03", "generation-vs03", 1, "Q2")
	gate := semantic.NewPublicationGate()
	if got := gate.EvaluateSettlement(base); got.Gate != "pending" {
		t.Fatalf("Q2 settlement must remain pending: %+v", got)
	}
	pass := candidateMap("basis-vs03", "snapshot-vs03", "generation-vs03-q3", 1, "Q3")
	pass.Quality.CriticalObligations = []semantic.CriticalObligation{{ObligationID: "ob-entry", Kind: "entry", Required: true, Status: "verified"}, {ObligationID: "ob-result", Kind: "result", Required: true, Status: "verified"}}
	if got := gate.EvaluateSettlement(pass); got.Gate != "passed" || len(got.BlockingObligationRefs) != 0 {
		t.Fatalf("complete Q3 settlement did not pass: %+v", got)
	}
	fail := candidateMap("basis-vs03", "snapshot-vs03", "generation-vs03-q3-fail", 1, "Q3")
	fail.Quality.CriticalObligations = []semantic.CriticalObligation{{ObligationID: "ob-entry", Kind: "entry", Required: true, Status: "verified"}, {ObligationID: "ob-result", Kind: "result", Required: true, Status: "unknown"}}
	fail.Quality.UnresolvedCriticalCount = 1
	settled := gate.EvaluateSettlement(fail)
	if settled.Gate != "failed" || len(settled.BlockingObligationRefs) != 1 || settled.BlockingObligationRefs[0] != "ob-result" || settled.EvaluatedAt == nil {
		t.Fatalf("explicit Q3 settlement failure was not reported: %+v", settled)
	}
	return evidenceFor("VS03-A6", "tree-vs03", "settlement:Q2=pending", "settlement:Q3=passed", "settlement:Q3=failed:ob-result")
}

// RunA07 checks that a Q4 late refinement keeps the previously evaluated
// settlement result unchanged for the same basis.
func RunA07(t *testing.T) Evidence {
	t.Helper()
	q3 := candidateMap("basis-vs03", "snapshot-vs03", "generation-vs03-q3", 1, "Q3")
	q3.Quality.CriticalObligations = []semantic.CriticalObligation{{ObligationID: "ob-entry", Kind: "entry", Required: true, Status: "verified"}}
	q4 := *q3
	q4.GenerationID = "generation-vs03-q4"
	q4.Quality.Stage = "Q4"
	q4.Summary.Current = "refined explanation only"
	gate := semantic.NewPublicationGate()
	before := gate.EvaluateSettlement(q3)
	after := gate.EvaluateSettlement(&q4)
	if before.Gate != "passed" || after.Gate != before.Gate || len(after.BlockingObligationRefs) != len(before.BlockingObligationRefs) {
		t.Fatalf("Q4 refinement changed Q3 settlement fact: before=%+v after=%+v", before, after)
	}
	q3.Settlement = before.Gate
	q4.Settlement = after.Gate
	// The refinement coordinator is the public persistence seam.  A same-basis
	// late result must still be admitted only against the active generation's
	// expected predecessor; a stale predecessor is rejected.
	root := t.TempDir()
	st := storage.New(root)
	if err := st.InitLayout(); err != nil {
		t.Fatal(err)
	}
	authority, head := newPublicationAuthority(t, root, workspace.WorkspaceIdentity{RepositoryID: vs03RepositoryID, WorktreeID: vs03WorktreeID, ConfigurationFingerprint: vs03Config}, "service.go", []byte("package service\nfunc Submit(){}\n"))
	q3.ComputedBasisID = head.ComputedBasisID
	q3.ValidatedAgainstSnapshotID = head.SnapshotID
	q3.Basis.ComputedBasisID = head.ComputedBasisID
	q3.Basis.ComputedWorkspaceSnapshotID = head.SnapshotID
	q3.Basis.SnapshotTreeID = head.RootTreeID
	q3.Basis.RepositoryID = head.RepositoryID
	q3.Basis.WorktreeID = head.WorktreeID
	q3.Basis.WorkspaceEpoch = head.WorkspaceEpoch
	q3.Basis.DependencyFingerprint = head.DependencyFingerprint
	q3.Basis.ConfigurationFingerprint = head.ConfigurationFingerprint
	q3.Evidence[0].ComputedBasisID = head.ComputedBasisID
	q3.Evidence[0].SnapshotID = head.SnapshotID
	q3.GenerationID = "generation-vs03-q3"
	q3.MapID = "map-vs03-generation-vs03-q3"
	q4 = *q3
	q4.GenerationID = "generation-vs03-q4"
	q4.MapID = "map-vs03-generation-vs03-q4"
	tx := publicationTransaction(q3.GenerationID, head.SnapshotID, head.ComputedBasisID, nil, true)
	replaceSemanticMapArtifact(&tx, q3)
	tx.Manifest.SettlementEvaluation = storage.SettlementEvaluation{Gate: before.Gate, EvaluatedAt: before.EvaluatedAt, BlockingObligationRefs: append([]string{}, before.BlockingObligationRefs...)}
	request, result, closure := canonicalVS02Evidence(t, head, authority)
	alignRefinementEvidence(&request, &result, closure, head, tx.Manifest.AnalysisReadSetID, tx.Manifest.CausalObservationClosureID)
	capabilityDigest, err := semantic.CanonicalCapabilityProfileDigest(result.Capability)
	if err != nil {
		t.Fatal(err)
	}
	tx.Manifest.CapabilityProfileDigest = capabilityDigest
	tx.Manifest.CausalObservationClosureDigest = closure.ClosureDigest
	replaceCanonicalProofArtifacts(&tx, &request, &result, closure, q3)
	bindAuthoritativeHead(&tx, authority, head.SnapshotID)
	if _, err := st.PublishGeneration(tx); err != nil {
		t.Fatal(err)
	}
	coord := semantic.NewRefinementCoordinator(st)
	previousMapBytes := append([]byte(nil), tx.Artifacts[tx.Manifest.ArtifactRefs.SemanticMap]...)
	q4.Basis.AnalysisReadSetID = closure.AnalysisReadSetID
	q4.Basis.CausalObservationClosureID = closure.ClosureID
	projection := semantic.BuildFlowViewProjection(&q4)
	delta := &workspace.WorkspaceDelta{FromSnapshotID: head.SnapshotID, ToSnapshotID: head.SnapshotID, AddedPaths: []string{}, ModifiedPaths: []string{}, DeletedPaths: []string{}, ChangedPaths: []string{}}
	input := semantic.LateRefinementInput{Map: &q4, Projection: projection, Closure: closure, Delta: delta, CapturedSnapshot: head, LiveHeadSnapshot: head, Intent: &semantic.TaskIntent{TaskID: vs03TaskID, Revision: 1, Mode: "feature", IntentStatus: "user_confirmed"}, AnalysisRequest: &request, AnalysisResult: &result, PreviousMapBytes: previousMapBytes, ArtifactBytes: map[string][]byte{}, ArtifactDigests: map[string]string{}, Event: generationPublishedEvent(2, q4.ComputedBasisID, head.SnapshotID, q4.GenerationID), RepositoryID: head.RepositoryID, WorktreeID: head.WorktreeID, QueryHash: vs03QueryHash, ExpectedPreviousID: q3.GenerationID, Metrics: semantic.PublicationMetrics{LagMs: 27, PendingRevisions: 0, MeasuredAt: time.Now().UTC(), Activity: "editing", TraceID: "trace-vs03-a07"}, LiveHeadCommit: authority.WithLiveHead}
	if _, err := coord.PublishLateRefinementV2(input); err != nil {
		t.Fatalf("same-basis Q4 refinement was rejected: %v", err)
	}
	if _, err := coord.PublishLateRefinementV2(input); err == nil {
		t.Fatal("late refinement with stale predecessor was accepted")
	}
	return evidenceFor("VS03-A7", head.RootTreeID, "settlement-digest:preserved", "refinement:same-basis", "cas:refinement", "event:atomic-generation-published")
}

// RunA08 publishes a complete proof, pointer, and event via the single
// durable transaction seam and verifies all three objects after commit.
func RunA08(t *testing.T) Evidence {
	t.Helper()
	root := t.TempDir()
	st := storage.New(root)
	if err := st.InitLayout(); err != nil {
		t.Fatal(err)
	}
	authority, head := newPublicationAuthority(t, root, workspace.WorkspaceIdentity{RepositoryID: vs03RepositoryID, WorktreeID: vs03WorktreeID, ConfigurationFingerprint: vs03Config}, "service.go", []byte("package service\nfunc Submit(){}\n"))
	tx := publicationTransaction("generation-vs03-published", head.SnapshotID, head.ComputedBasisID, nil, true)
	bindAuthoritativeHead(&tx, authority, head.SnapshotID)
	commit, err := st.PublishGeneration(tx)
	if err != nil || commit.ManifestRef == "" || commit.Pointer == nil || len(commit.Event) == 0 {
		t.Fatalf("durable publication failed: commit=%+v err=%v", commit, err)
	}
	ptr, err := st.ReadActivePointer()
	if err != nil || ptr == nil || ptr.GenerationID != tx.Pointer.GenerationID || ptr.ManifestObjectRef != commit.ManifestRef {
		t.Fatalf("active pointer was not committed atomically: ptr=%+v err=%v", ptr, err)
	}
	manifest, validatedPointer, err := st.ReadValidatedActiveProofManifest()
	if err != nil || manifest == nil || validatedPointer == nil || manifest.GenerationID != tx.Manifest.GenerationID || validatedPointer.GenerationID != ptr.GenerationID {
		t.Fatalf("validated proof manifest was not committed: manifest=%+v pointer=%+v err=%v", manifest, validatedPointer, err)
	}
	ledgerPath := filepath.Join(st.BaseDir(), "semantics", "events", "event-ledger.jsonl")
	ledger, err := os.ReadFile(ledgerPath)
	if err != nil || strings.Count(strings.TrimSpace(string(ledger)), "\n") != 0 || !strings.Contains(string(ledger), "event-1") {
		t.Fatalf("publication event visibility is not atomic: ledger=%q err=%v", ledger, err)
	}
	// A new storage handle must observe the same durable ledger after a restart.
	restarted := storage.New(root)
	restartedLedger, err := os.ReadFile(filepath.Join(restarted.BaseDir(), "semantics", "events", "event-ledger.jsonl"))
	if err != nil || !strings.Contains(string(restartedLedger), "event-1") {
		t.Fatalf("committed publication event was not restart-visible: ledger=%q err=%v", restartedLedger, err)
	}
	// A current proof is the complete immutable artifact graph. Corrupting any
	// canonical CAS object must make the strict restart reader fail closed, and
	// restoring the original bytes must make the same proof readable again.
	for _, tc := range []struct {
		name string
		ref  string
	}{
		{name: "semantic-map", ref: manifest.ArtifactRefs.SemanticMap},
		{name: "projection", ref: manifest.ArtifactRefs.Projection},
		{name: "analysis-read-set", ref: manifest.ArtifactRefs.AnalysisReadSet},
		{name: "observation-closure", ref: manifest.ArtifactRefs.ObservationClosure},
		{name: "analyzer-result", ref: manifest.ArtifactRefs.AnalyzerResult},
	} {
		t.Run("strict-artifact-"+tc.name, func(t *testing.T) {
			path := filepath.Join(st.BaseDir(), "cas", strings.TrimPrefix(tc.ref, "cas:sha256:")+".artifact")
			original, readErr := os.ReadFile(path)
			if readErr != nil {
				t.Fatal(readErr)
			}
			corrupt := append([]byte(nil), original...)
			if len(corrupt) == 0 {
				t.Fatal("canonical artifact is empty")
			}
			corrupt[0] ^= 0xff
			if writeErr := os.WriteFile(path, corrupt, 0o600); writeErr != nil {
				t.Fatal(writeErr)
			}
			if _, _, readErr := st.ReadValidatedActiveProofManifest(); readErr == nil {
				t.Fatal("strict proof reader accepted a corrupted " + tc.name + " artifact")
			}
			if writeErr := os.WriteFile(path, original, 0o600); writeErr != nil {
				t.Fatal(writeErr)
			}
			if _, _, readErr := st.ReadValidatedActiveProofManifest(); readErr != nil {
				t.Fatalf("strict proof reader did not recover after restoring %s: %v", tc.name, readErr)
			}
		})
	}
	manifestPath := filepath.Join(st.BaseDir(), "cas", strings.TrimPrefix(commit.ManifestRef, "cas:sha256:")+".json")
	manifestBytes, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*storage.GenerationProofManifest)
	}{
		{name: "capability-digest", mutate: func(in *storage.GenerationProofManifest) { in.CapabilityProfileDigest = strings.Repeat("0", 64) }},
		{name: "read-set-identity", mutate: func(in *storage.GenerationProofManifest) { in.AnalysisReadSetID = "readset-drift" }},
		{name: "projection-ref", mutate: func(in *storage.GenerationProofManifest) { in.ArtifactRefs.Projection = in.ArtifactRefs.SemanticMap }},
	} {
		t.Run("strict-manifest-"+tc.name, func(t *testing.T) {
			var mutated storage.GenerationProofManifest
			if err := json.Unmarshal(manifestBytes, &mutated); err != nil {
				t.Fatal(err)
			}
			tc.mutate(&mutated)
			corrupt, err := json.MarshalIndent(&mutated, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(manifestPath, corrupt, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, _, err := st.ReadValidatedActiveProofManifest(); err == nil {
				t.Fatal("strict proof reader accepted a mutated " + tc.name + " manifest identity")
			}
			if err := os.WriteFile(manifestPath, manifestBytes, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, _, err := st.ReadValidatedActiveProofManifest(); err != nil {
				t.Fatalf("strict proof reader did not recover after restoring %s: %v", tc.name, err)
			}
		})
	}
	return evidenceFor("VS03-A8", "tree-vs03", "manifest:"+commit.ManifestRef, "pointer:"+ptr.GenerationID, "event-ledger:event-1", "restart:visible", "strict-artifact-mutations:map,projection,readset,closure,result", "strict-manifest-mutations:capability,readset,projection")
}

// RunA09 injects failures at every publication boundary and verifies the
// transaction journal removes every uncommitted object while preserving an
// unrelated pre-existing CAS object. Each boundary is retried after restart.
func RunA09(t *testing.T) Evidence {
	t.Helper()
	faults := []string{"manifest", "artifact", "event", "pointer", "fsync"}
	for _, fault := range faults {
		fault := fault
		if !t.Run(fault, func(t *testing.T) {
			root := t.TempDir()
			st := storage.New(root)
			if err := st.InitLayout(); err != nil {
				t.Fatal(err)
			}
			preexisting := []byte("pre-existing-cas-" + fault)
			preRef, err := st.WriteArtifactCAS(preexisting)
			if err != nil {
				t.Fatal(err)
			}
			authority, head := newPublicationAuthority(t, root, workspace.WorkspaceIdentity{RepositoryID: vs03RepositoryID, WorktreeID: vs03WorktreeID, ConfigurationFingerprint: vs03Config}, "service.go", []byte("package service\nfunc Submit(){}\n"))
			tx := publicationTransaction("generation-vs03-fault-"+fault, head.SnapshotID, head.ComputedBasisID, nil, true)
			bindAuthoritativeHead(&tx, authority, head.SnapshotID)
			newArtifactRef := tx.Manifest.ArtifactRefs.SemanticMap
			st.SetPublicationFault(fault)
			if _, err := st.PublishGeneration(tx); err == nil {
				t.Fatalf("injected %s failure unexpectedly published", fault)
			}
			st.SetPublicationFault("")

			assertNoActivePublication := func(label string, current *storage.Storage) {
				t.Helper()
				ptr, readErr := current.ReadActivePointer()
				if readErr != nil || ptr != nil {
					t.Fatalf("%s exposed active pointer: ptr=%+v err=%v", label, ptr, readErr)
				}
				manifest, validatedPtr, readErr := current.ReadValidatedActiveProofManifest()
				if readErr != nil || manifest != nil || validatedPtr != nil {
					t.Fatalf("%s exposed active proof: manifest=%+v pointer=%+v err=%v", label, manifest, validatedPtr, readErr)
				}
				casManifests, globErr := filepath.Glob(filepath.Join(current.BaseDir(), "cas", "*.json"))
				if globErr != nil || len(casManifests) != 0 {
					t.Fatalf("%s exposed manifest CAS: files=%v err=%v", label, casManifests, globErr)
				}
				if _, statErr := os.Stat(filepath.Join(current.BaseDir(), "cas", strings.TrimPrefix(newArtifactRef, "cas:sha256:")+".artifact")); !errors.Is(statErr, os.ErrNotExist) {
					t.Fatalf("%s exposed new artifact %s: err=%v", label, newArtifactRef, statErr)
				}
				ledgerPath := filepath.Join(current.BaseDir(), "semantics", "events", "event-ledger.jsonl")
				ledger, ledgerErr := os.ReadFile(ledgerPath)
				if ledgerErr == nil && len(strings.TrimSpace(string(ledger))) != 0 {
					t.Fatalf("%s exposed ghost event: %q", label, ledger)
				}
				if ledgerErr != nil && !errors.Is(ledgerErr, os.ErrNotExist) {
					t.Fatalf("%s could not inspect event ledger: %v", label, ledgerErr)
				}
				prePath := filepath.Join(current.BaseDir(), "cas", strings.TrimPrefix(preRef, "cas:sha256:")+".artifact")
				preBytes, preErr := os.ReadFile(prePath)
				if preErr != nil || !bytes.Equal(preBytes, preexisting) {
					t.Fatalf("%s damaged pre-existing CAS %s: bytes=%q err=%v", label, preRef, preBytes, preErr)
				}
			}
			assertNoActivePublication("rollback", st)

			restarted := storage.New(root)
			if err := restarted.RecoverPendingPublication(); err != nil {
				t.Fatal(err)
			}
			assertNoActivePublication("restart recovery", restarted)
			restartedAuthority, restartedHead := newPublicationAuthorityFromExisting(t, root)
			if restartedHead.SnapshotID != head.SnapshotID {
				t.Fatalf("restart changed authoritative workspace head: before=%s after=%s", head.SnapshotID, restartedHead.SnapshotID)
			}
			bindAuthoritativeHead(&tx, restartedAuthority, head.SnapshotID)
			if _, err := restarted.PublishGeneration(tx); err != nil {
				t.Fatalf("publication retry after restart failed: %v", err)
			}
			manifest, ptr, err := restarted.ReadValidatedActiveProofManifest()
			if err != nil || manifest == nil || ptr == nil || manifest.GenerationID != tx.Manifest.GenerationID || ptr.GenerationID != tx.Pointer.GenerationID {
				t.Fatalf("successful retry did not expose validated proof: manifest=%+v pointer=%+v err=%v", manifest, ptr, err)
			}
			ledger, err := os.ReadFile(filepath.Join(restarted.BaseDir(), "semantics", "events", "event-ledger.jsonl"))
			if err != nil || !strings.Contains(string(ledger), "event-1") {
				t.Fatalf("successful retry did not expose durable event: ledger=%q err=%v", ledger, err)
			}
			prePath := filepath.Join(restarted.BaseDir(), "cas", strings.TrimPrefix(preRef, "cas:sha256:")+".artifact")
			if preBytes, err := os.ReadFile(prePath); err != nil || !bytes.Equal(preBytes, preexisting) {
				t.Fatalf("successful retry damaged pre-existing CAS: bytes=%q err=%v", preBytes, err)
			}
			// The captured live head is checked at the same transaction boundary,
			// even after a valid publication exists.
			if _, _, err := restartedAuthority.ApplyVersionedEdit(context.Background(), workspace.EditRequest{Path: "service.go", Content: []byte("package service\nfunc Submit(){advanced()}\n"), DocumentVersion: 2, Source: workspace.SourceIDEVersioned}); err != nil {
				t.Fatal(err)
			}
			stale := publicationTransaction("generation-vs03-stale-"+fault, head.SnapshotID, head.ComputedBasisID, nil, true)
			stale.ExpectedPreviousGenerationID = tx.Manifest.GenerationID
			stale.Pointer.ExpectedPreviousGenerationID = &stale.ExpectedPreviousGenerationID
			bindAuthoritativeHead(&stale, restartedAuthority, head.SnapshotID)
			if _, err := restarted.PublishGeneration(stale); err == nil {
				t.Fatalf("stale captured head was not rejected: %v", err)
			}
		}) {
			return evidenceFor("VS03-A9", "tree-vs03", "fault-boundary:"+fault)
		}
	}
	return evidenceFor("VS03-A9", "tree-vs03", "fault-boundaries:manifest,artifact,event,pointer,fsync", "rollback:no-pointer-no-manifest-no-artifact-no-event", "restart:recovery", "retry:committed", "head:cas-conflict")
}

// RunA10 verifies same-basis late refinement admission and the rejection
// boundaries around it through the production coordinator seam. Every invalid
// input is exercised against a fresh durable fixture and must leave the active
// proof, CAS, and event ledger byte-for-byte unchanged.
func RunA10(t *testing.T) Evidence {
	t.Helper()
	type lateFixture struct {
		storage    *storage.Storage
		authority  *workspace.SnapshotEngine
		head       *workspace.WorkspaceSnapshot
		input      semantic.LateRefinementInput
		baseMap    *semantic.SemanticMapIR
		baseLedger []byte
		baseCAS    []string
	}

	criteria := []semantic.AcceptanceCriterion{{ID: "AC-submit", Text: "Submit", RequiredEvidenceKinds: []string{"source"}}}
	newFixture := func(t *testing.T) lateFixture {
		t.Helper()
		root := t.TempDir()
		st := storage.New(root)
		if err := st.InitLayout(); err != nil {
			t.Fatal(err)
		}
		authority, head := newPublicationAuthority(t, root, workspace.WorkspaceIdentity{
			RepositoryID: vs03RepositoryID, WorktreeID: vs03WorktreeID, ConfigurationFingerprint: vs03Config,
		}, "service.go", []byte("package service\nfunc Submit(){}\n"))
		base := candidateMap(head.ComputedBasisID, head.SnapshotID, "generation-vs03-base", 1, "Q3")
		base.Steps[0].Rules = []string{"AC-submit"}
		base.Basis.WorkspaceEpoch = head.WorkspaceEpoch
		base.Basis.RepositoryID = head.RepositoryID
		base.Basis.WorktreeID = head.WorktreeID
		base.Basis.SnapshotTreeID = head.RootTreeID
		base.Basis.DependencyFingerprint = head.DependencyFingerprint
		base.Basis.ConfigurationFingerprint = head.ConfigurationFingerprint
		base.Quality.CriticalObligations = []semantic.CriticalObligation{{ObligationID: "ob-entry", Kind: "entry", Required: true, Status: "verified"}}
		baseSettlement := semantic.NewPublicationGate().EvaluateSettlement(base)
		if baseSettlement.Gate != "passed" {
			t.Fatalf("Q3 fixture settlement did not pass its explicit obligation check: %+v", baseSettlement)
		}
		base.Settlement = baseSettlement.Gate
		base.RequirementAlignment = semantic.ComputeRequirementAlignment(criteria, base, semantic.AlignmentOptions{})
		tx := publicationTransaction(base.GenerationID, head.SnapshotID, head.ComputedBasisID, nil, true)
		replaceSemanticMapArtifact(&tx, base)
		tx.Manifest.SettlementEvaluation = storage.SettlementEvaluation{Gate: baseSettlement.Gate, EvaluatedAt: baseSettlement.EvaluatedAt, BlockingObligationRefs: append([]string{}, baseSettlement.BlockingObligationRefs...)}
		request, result, closure := canonicalVS02Evidence(t, head, authority)
		alignRefinementEvidence(&request, &result, closure, head, tx.Manifest.AnalysisReadSetID, tx.Manifest.CausalObservationClosureID)
		capabilityDigest, err := semantic.CanonicalCapabilityProfileDigest(result.Capability)
		if err != nil {
			t.Fatal(err)
		}
		tx.Manifest.CapabilityProfileDigest = capabilityDigest
		tx.Manifest.CausalObservationClosureDigest = closure.ClosureDigest
		replaceCanonicalProofArtifacts(&tx, &request, &result, closure, base)
		previousMapBytes := append([]byte(nil), tx.Artifacts[tx.Manifest.ArtifactRefs.SemanticMap]...)
		bindAuthoritativeHead(&tx, authority, head.SnapshotID)
		if _, err := st.PublishGeneration(tx); err != nil {
			t.Fatal(err)
		}
		late := *base
		late.GenerationID = "generation-vs03-late"
		late.MapID = "map-vs03-generation-vs03-late"
		late.Quality.Stage = "Q4"
		late.Summary.Current = "refined explanation only"
		late.Basis.AnalysisReadSetID = closure.AnalysisReadSetID
		late.Basis.CausalObservationClosureID = closure.ClosureID
		projection := semantic.BuildFlowViewProjection(&late)
		delta := &workspace.WorkspaceDelta{FromSnapshotID: head.SnapshotID, ToSnapshotID: head.SnapshotID, AddedPaths: []string{}, ModifiedPaths: []string{}, DeletedPaths: []string{}, ChangedPaths: []string{}}
		input := semantic.LateRefinementInput{
			Map: &late, Projection: projection, Closure: closure, Delta: delta,
			CapturedSnapshot: head, LiveHeadSnapshot: head,
			Intent:          &semantic.TaskIntent{TaskID: vs03TaskID, Revision: 1, Mode: "feature", IntentStatus: "user_confirmed"},
			AnalysisRequest: &request, AnalysisResult: &result, PreviousMapBytes: previousMapBytes,
			Event:        generationPublishedEvent(2, late.ComputedBasisID, head.SnapshotID, late.GenerationID),
			RepositoryID: head.RepositoryID, WorktreeID: head.WorktreeID, QueryHash: vs03QueryHash,
			ExpectedPreviousID: base.GenerationID,
			Metrics:            semantic.PublicationMetrics{LagMs: 27, PendingRevisions: 0, MeasuredAt: time.Now().UTC(), Activity: "editing", TraceID: "trace-vs03-a10"},
			LiveHeadCommit:     authority.WithLiveHead,
		}
		ledgerPath := filepath.Join(st.BaseDir(), "semantics", "events", "event-ledger.jsonl")
		ledger, err := os.ReadFile(ledgerPath)
		if err != nil {
			t.Fatal(err)
		}
		cas, err := filepath.Glob(filepath.Join(st.BaseDir(), "cas", "*"))
		if err != nil {
			t.Fatal(err)
		}
		sort.Strings(cas)
		return lateFixture{storage: st, authority: authority, head: head, input: input, baseMap: base, baseLedger: ledger, baseCAS: cas}
	}

	fixture := newFixture(t)
	coord := semantic.NewRefinementCoordinator(fixture.storage)
	beforeDigests := vs03Q3DigestTuple(fixture.baseMap)
	commit, err := coord.PublishLateRefinementV2(fixture.input)
	if err != nil {
		t.Fatalf("same-basis late result was not admitted through V2 seam: %v", err)
	}
	if commit.ManifestRef == "" || commit.Pointer == nil || len(commit.Event) == 0 {
		t.Fatalf("late refinement returned incomplete publication commit: %+v", commit)
	}
	manifest, pointer, err := fixture.storage.ReadValidatedActiveProofManifest()
	if err != nil || manifest == nil || pointer == nil || manifest.GenerationID != fixture.input.Map.GenerationID || pointer.GenerationID != fixture.input.Map.GenerationID {
		t.Fatalf("late refinement did not expose validated current proof: manifest=%+v pointer=%+v err=%v", manifest, pointer, err)
	}
	if manifest.ExpectedPreviousGenerationID == nil || *manifest.ExpectedPreviousGenerationID != fixture.baseMap.GenerationID || manifest.SettlementEvaluation.Gate != "passed" {
		t.Fatalf("late refinement proof lost predecessor or settlement facts: %+v", manifest)
	}
	if manifest.ArtifactRefs.SemanticMap == "" || manifest.ArtifactRefs.Projection == "" || manifest.ArtifactRefs.AnalysisReadSet == "" || manifest.ArtifactRefs.ObservationClosure == "" || manifest.ArtifactRefs.AnalyzerResult == "" {
		t.Fatalf("late refinement proof omitted canonical artifact refs: %+v", manifest.ArtifactRefs)
	}
	mapRef := strings.TrimPrefix(manifest.ArtifactRefs.SemanticMap, "cas:sha256:")
	publishedBytes, err := os.ReadFile(filepath.Join(fixture.storage.BaseDir(), "cas", mapRef+".artifact"))
	if err != nil {
		t.Fatalf("read published late semantic-map CAS: %v", err)
	}
	var publishedMap semantic.SemanticMapIR
	if err := json.Unmarshal(publishedBytes, &publishedMap); err != nil {
		t.Fatalf("decode published late semantic-map CAS: %v", err)
	}
	if got := vs03Q3DigestTuple(&publishedMap); got != beforeDigests {
		t.Fatalf("late refinement changed Q3 fact/alignment/obligation/settlement digest: before=%v after=%v", beforeDigests, got)
	}

	negativeCases := []struct {
		name   string
		mutate func(*semantic.LateRefinementInput)
	}{
		{name: "missing-closure", mutate: func(in *semantic.LateRefinementInput) { in.Closure = nil }},
		{name: "missing-request", mutate: func(in *semantic.LateRefinementInput) { in.AnalysisRequest = nil }},
		{name: "missing-result", mutate: func(in *semantic.LateRefinementInput) { in.AnalysisResult = nil }},
		{name: "workspace-epoch-drift", mutate: func(in *semantic.LateRefinementInput) { in.Map.Basis.WorkspaceEpoch++ }},
		{name: "intent-revision-drift", mutate: func(in *semantic.LateRefinementInput) { in.Intent.Revision++ }},
		{name: "query-drift", mutate: func(in *semantic.LateRefinementInput) { in.QueryHash = strings.Repeat("2", 64) }},
		{name: "repository-drift", mutate: func(in *semantic.LateRefinementInput) { in.RepositoryID = "repo-vs03-drift" }},
		{name: "worktree-drift", mutate: func(in *semantic.LateRefinementInput) { in.WorktreeID = "worktree-vs03-drift" }},
		{name: "delta-snapshot-drift", mutate: func(in *semantic.LateRefinementInput) { in.Delta.ToSnapshotID = "snapshot-vs03-drift" }},
		{name: "live-head-drift", mutate: func(in *semantic.LateRefinementInput) {
			copy := *in.LiveHeadSnapshot
			copy.SnapshotID = "snapshot-vs03-live-drift"
			in.LiveHeadSnapshot = &copy
		}},
		{name: "predecessor-drift", mutate: func(in *semantic.LateRefinementInput) { in.ExpectedPreviousID = "generation-vs03-other" }},
		{name: "missing-live-head-authority", mutate: func(in *semantic.LateRefinementInput) { in.LiveHeadCommit = nil }},
		{name: "fact-digest-drift", mutate: func(in *semantic.LateRefinementInput) { in.Map.Steps[0].Name = "Other" }},
		{name: "alignment-digest-drift", mutate: func(in *semantic.LateRefinementInput) { in.Map.RequirementAlignment[0].Notes = "alignment drift" }},
		{name: "obligation-digest-drift", mutate: func(in *semantic.LateRefinementInput) {
			in.Map.Quality.CriticalObligations = append(in.Map.Quality.CriticalObligations, semantic.CriticalObligation{ObligationID: "ob-drift", Kind: "entry", Required: true, Status: "verified"})
		}},
		{name: "settlement-digest-drift", mutate: func(in *semantic.LateRefinementInput) { in.Map.Settlement = "failed" }},
		{name: "event-identity-drift", mutate: func(in *semantic.LateRefinementInput) {
			var event map[string]any
			if err := json.Unmarshal(in.Event, &event); err != nil {
				t.Fatalf("decode event fixture: %v", err)
			}
			event["eventType"] = "activity.updated"
			in.Event = mustMarshal(event)
		}},
	}
	for _, tc := range negativeCases {
		t.Run(tc.name, func(t *testing.T) {
			fresh := newFixture(t)
			beforeManifest, beforePointer, err := fresh.storage.ReadValidatedActiveProofManifest()
			if err != nil || beforeManifest == nil || beforePointer == nil {
				t.Fatalf("read negative baseline proof: manifest=%+v pointer=%+v err=%v", beforeManifest, beforePointer, err)
			}
			tc.mutate(&fresh.input)
			if _, err := semantic.NewRefinementCoordinator(fresh.storage).PublishLateRefinementV2(fresh.input); err == nil {
				t.Fatalf("invalid late refinement %s was accepted", tc.name)
			}
			afterManifest, afterPointer, readErr := fresh.storage.ReadValidatedActiveProofManifest()
			if readErr != nil || afterManifest == nil || afterPointer == nil || afterManifest.GenerationID != beforeManifest.GenerationID || afterPointer.GenerationID != beforePointer.GenerationID {
				t.Fatalf("invalid late refinement changed active proof: before=%+v/%+v after=%+v/%+v err=%v", beforeManifest, beforePointer, afterManifest, afterPointer, readErr)
			}
			ledgerPath := filepath.Join(fresh.storage.BaseDir(), "semantics", "events", "event-ledger.jsonl")
			ledger, readErr := os.ReadFile(ledgerPath)
			if readErr != nil || !bytes.Equal(ledger, fresh.baseLedger) {
				t.Fatalf("invalid late refinement changed durable event ledger: before=%q after=%q err=%v", fresh.baseLedger, ledger, readErr)
			}
			cas, globErr := filepath.Glob(filepath.Join(fresh.storage.BaseDir(), "cas", "*"))
			if globErr != nil {
				t.Fatal(globErr)
			}
			sort.Strings(cas)
			if strings.Join(cas, "\n") != strings.Join(fresh.baseCAS, "\n") {
				t.Fatalf("invalid late refinement left new CAS objects: before=%v after=%v", fresh.baseCAS, cas)
			}
		})
	}
	return evidenceFor("VS03-A10", fixture.head.RootTreeID, "refinement:v2-production-seam", "basis:same", "closure:lossless", "q3-digests:fact-alignment-obligation-settlement-preserved", "negative:18-boundaries-rejected", "atomicity:no-pointer-no-manifest-no-artifact-no-event")
}

// RunA11 verifies HTTP replay, full-sync fallback after ring eviction, and
// cursor continuation after the server restarts. The full-sync payload must
// contain the validated proof, activity, and terminal current-or-gap state.
func RunA11(t *testing.T) Evidence {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "service.go"), []byte("package service\nfunc HandleSubmit(){}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	storageHandle := storage.New(root)
	if err := storageHandle.InitLayout(); err != nil {
		t.Fatal(err)
	}
	authority, head := newPublicationAuthority(t, root, defaultLiveIdentityForTest(root), "bootstrap.go", []byte("package service\nfunc Bootstrap(){}\n"))
	initial := publicationTransaction("generation-vs03-stream", head.SnapshotID, head.ComputedBasisID, nil, true)
	bindAuthoritativeHead(&initial, authority, head.SnapshotID)
	initial.Event, _ = json.Marshal(map[string]any{"schemaId": "https://codeflow.local/schemas/rflsc.event-envelope.v2.schema.json", "schemaVersion": 2, "streamId": "flowview-live-stream", "sequence": 1, "eventId": "event-1", "eventType": "generation.published", "occurredAt": time.Unix(1, 0).UTC(), "computedBasisId": head.ComputedBasisID, "validatedAgainstSnapshotId": head.SnapshotID, "generationId": "generation-vs03-stream"})
	if _, err := storageHandle.PublishGeneration(initial); err != nil {
		t.Fatal(err)
	}
	server, err := flowview.NewServer(flowview.Config{RepoRoot: root, Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	server.Start()
	ledgerPath := filepath.Join(root, ".codeflow", "semantics", "events", "event-ledger.jsonl")
	defer server.Shutdown(context.Background())
	// A direct valid publication seeds last_verified authority. The restarted
	// server has no task query, so these real edit ingresses exercise measured
	// generation.gap while producing enough durable events to evict event-1.
	for version := 1; version <= 51; version++ {
		content := []byte(fmt.Sprintf("package service\nfunc HandleSubmit(){step%d()}\n", version))
		if _, _, err := server.SubmitVersionedEdit(context.Background(), workspace.EditRequest{Path: "service.go", Content: content, DocumentVersion: version, Source: workspace.SourceIDEVersioned}); err != nil {
			server.Shutdown(context.Background())
			t.Fatalf("edit %d failed: %v", version, err)
		}
	}
	// Let the always-on consumer finish the latest checkpoint so its durable
	// activity object carries a measured terminal current-or-gap timestamp.
	terminalDeadline := time.Now().Add(6 * time.Second)
	var terminal workspace.ActivityStatus
	for time.Now().Before(terminalDeadline) {
		terminal = server.CurrentActivity()
		if !terminal.CurrentOrGapAt.IsZero() {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if terminal.CurrentOrGapAt.IsZero() || terminal.TraceID == "" {
		server.Shutdown(context.Background())
		t.Fatalf("stream full-sync prerequisite has no terminal current-or-gap activity: %+v", terminal)
	}
	eventIDs, err := durableEventIDs(ledgerPath)
	if err != nil || len(eventIDs) < 3 {
		server.Shutdown(context.Background())
		t.Fatalf("durable event ledger is too short for replay boundaries: ids=%v err=%v", eventIDs, err)
	}
	eventEnvelopes, err := durableEventEnvelopes(ledgerPath)
	if err != nil {
		server.Shutdown(context.Background())
		t.Fatal(err)
	}
	gapEvents := 0
	for _, envelope := range eventEnvelopes {
		if envelope.EventType == "generation.gap" {
			gapEvents++
		}
	}
	if gapEvents == 0 {
		server.Shutdown(context.Background())
		t.Fatalf("gap setup did not persist a generation.gap event: envelopes=%d", len(eventEnvelopes))
	}
	client := &http.Client{}
	fullCtx, fullCancel := context.WithTimeout(context.Background(), 3*time.Second)
	fullReq, err := http.NewRequestWithContext(fullCtx, http.MethodGet, "http://"+server.Addr()+"/api/workspace/stream?token="+server.AuthToken(), nil)
	if err != nil {
		fullCancel()
		server.Shutdown(context.Background())
		t.Fatal(err)
	}
	fullReq.Header.Set("Last-Event-ID", eventIDs[0])
	fullResp, err := client.Do(fullReq)
	if err != nil {
		fullCancel()
		server.Shutdown(context.Background())
		t.Fatal(err)
	}
	fullBlock, err := readSSEBlock(bufio.NewReader(fullResp.Body))
	_ = fullResp.Body.Close()
	fullCancel()
	if err != nil {
		server.Shutdown(context.Background())
		t.Fatalf("full-sync stream did not return an SSE block: %v", err)
	}
	fullEnvelope, err := parseSSEEnvelope(fullBlock)
	if err != nil || fullEnvelope.EventType != "snapshot_sync" {
		server.Shutdown(context.Background())
		t.Fatalf("full-sync stream did not return a canonical snapshot_sync envelope: envelope=%+v err=%v block=%s", fullEnvelope, err, fullBlock)
	}
	if err := contractharness.ValidateEventEnvelopeV2(mustMarshal(fullEnvelope)); err != nil {
		server.Shutdown(context.Background())
		t.Fatalf("full-sync event envelope failed v2 validation: %v", err)
	}
	fullData, ok := fullEnvelope.Data.(map[string]any)
	if !ok || fullData["activeManifest"] == nil || fullData["activePointer"] == nil || fullData["activity"] == nil || fullData["verifiedGap"] == nil {
		server.Shutdown(context.Background())
		t.Fatalf("full-sync payload lacks validated proof/activity/gap: %s", fullBlock)
	}
	manifestBytes := mustMarshal(fullData["activeManifest"])
	if err := contractharness.ValidateGenerationProofManifestV2(manifestBytes); err != nil {
		server.Shutdown(context.Background())
		t.Fatalf("full-sync active manifest failed v2 validation: %v", err)
	}
	pointerBytes := mustMarshal(fullData["activePointer"])
	if err := contractharness.ValidateActivePointerV2(pointerBytes); err != nil {
		server.Shutdown(context.Background())
		t.Fatalf("full-sync active pointer failed v2 validation: %v", err)
	}
	gapBytes := mustMarshal(fullData["verifiedGap"])
	if err := contractharness.ValidateVerifiedGapV2(gapBytes); err != nil {
		server.Shutdown(context.Background())
		t.Fatalf("full-sync verified gap failed v2 validation: %v", err)
	}
	var gapDoc map[string]any
	if err := json.Unmarshal(gapBytes, &gapDoc); err != nil {
		server.Shutdown(context.Background())
		t.Fatal(err)
	}
	for _, key := range []string{"affectedScope", "intersectedCauses", "latestSnapshotId", "analysisLagMs", "pendingRevisions", "traceId"} {
		if value, exists := gapDoc[key]; !exists || value == nil {
			server.Shutdown(context.Background())
			t.Fatalf("full-sync verified gap omitted measured field %q: %s", key, gapBytes)
		}
	}
	if vs03StringFromMap(gapDoc, "traceId") != terminal.TraceID {
		server.Shutdown(context.Background())
		t.Fatalf("full-sync verified gap trace does not match terminal activity: gap=%s activity=%s", gapBytes, terminal.TraceID)
	}
	terminalGapBytes := append([]byte(nil), gapBytes...)
	manifest, pointer, err := storageHandle.ReadValidatedActiveProofManifest()
	if err != nil || manifest == nil || pointer == nil {
		server.Shutdown(context.Background())
		t.Fatalf("full-sync proof is not validated at the storage boundary: manifest=%+v pointer=%+v err=%v", manifest, pointer, err)
	}
	// First reconnect from a retained cursor and capture the newest durable
	// event. This cursor is then reused after a process restart.
	retainedCtx, retainedCancel := context.WithTimeout(context.Background(), 3*time.Second)
	retainedReq, err := http.NewRequestWithContext(retainedCtx, http.MethodGet, "http://"+server.Addr()+"/api/workspace/stream?token="+server.AuthToken(), nil)
	if err != nil {
		retainedCancel()
		server.Shutdown(context.Background())
		t.Fatal(err)
	}
	retainedReq.Header.Set("Last-Event-ID", eventIDs[len(eventIDs)-2])
	retainedResp, err := client.Do(retainedReq)
	if err != nil {
		retainedCancel()
		server.Shutdown(context.Background())
		t.Fatal(err)
	}
	retainedBlock, err := readSSEBlock(bufio.NewReader(retainedResp.Body))
	_ = retainedResp.Body.Close()
	retainedCancel()
	if err != nil || strings.Contains(retainedBlock, "snapshot_sync") || !strings.Contains(retainedBlock, eventIDs[len(eventIDs)-1]) {
		server.Shutdown(context.Background())
		t.Fatalf("retained cursor did not replay the next durable event: block=%q err=%v", retainedBlock, err)
	}
	lastCursor := eventIDs[len(eventIDs)-1]
	if err := server.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}

	restarted, err := flowview.NewServer(flowview.Config{RepoRoot: root, Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	restarted.Start()
	defer restarted.Shutdown(context.Background())
	// The terminal gap must survive process restart as the same canonical
	// payload, not merely as an activity flag. An evicted cursor forces the
	// restarted server through its full-sync seam before normal replay.
	restartSyncCtx, restartSyncCancel := context.WithTimeout(context.Background(), 3*time.Second)
	restartSyncReq, err := http.NewRequestWithContext(restartSyncCtx, http.MethodGet, "http://"+restarted.Addr()+"/api/workspace/stream?token="+restarted.AuthToken(), nil)
	if err != nil {
		restartSyncCancel()
		t.Fatal(err)
	}
	restartSyncReq.Header.Set("Last-Event-ID", eventIDs[0])
	restartSyncResp, err := client.Do(restartSyncReq)
	if err != nil {
		restartSyncCancel()
		t.Fatal(err)
	}
	restartSyncBlock, err := readSSEBlock(bufio.NewReader(restartSyncResp.Body))
	_ = restartSyncResp.Body.Close()
	restartSyncCancel()
	if err != nil {
		t.Fatalf("restarted full-sync stream did not return an SSE block: %v", err)
	}
	restartSyncEnvelope, err := parseSSEEnvelope(restartSyncBlock)
	if err != nil || restartSyncEnvelope.EventType != "snapshot_sync" {
		t.Fatalf("restarted stream did not return snapshot_sync: envelope=%+v err=%v block=%s", restartSyncEnvelope, err, restartSyncBlock)
	}
	if err := contractharness.ValidateEventEnvelopeV2(mustMarshal(restartSyncEnvelope)); err != nil {
		t.Fatalf("restarted full-sync envelope failed v2 validation: %v", err)
	}
	restartSyncData, ok := restartSyncEnvelope.Data.(map[string]any)
	if !ok || restartSyncData["verifiedGap"] == nil {
		t.Fatalf("restarted full-sync omitted terminal verified gap: %s", restartSyncBlock)
	}
	restartGapBytes := mustMarshal(restartSyncData["verifiedGap"])
	if !bytes.Equal(restartGapBytes, terminalGapBytes) {
		t.Fatalf("restarted full-sync changed terminal verified gap: before=%s after=%s", terminalGapBytes, restartGapBytes)
	}
	if err := contractharness.ValidateVerifiedGapV2(restartGapBytes); err != nil {
		t.Fatalf("restarted full-sync verified gap failed v2 validation: %v", err)
	}
	reconnectCtx, reconnectCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer reconnectCancel()
	reconnectReq, err := http.NewRequestWithContext(reconnectCtx, http.MethodGet, "http://"+restarted.Addr()+"/api/workspace/stream?token="+restarted.AuthToken(), nil)
	if err != nil {
		t.Fatal(err)
	}
	reconnectReq.Header.Set("Last-Event-ID", lastCursor)
	responseCh := make(chan struct {
		resp *http.Response
		err  error
	}, 1)
	go func() {
		resp, err := client.Do(reconnectReq)
		responseCh <- struct {
			resp *http.Response
			err  error
		}{resp: resp, err: err}
	}()
	time.Sleep(100 * time.Millisecond)
	_, nextSnapshot, err := restarted.SubmitVersionedEdit(context.Background(), workspace.EditRequest{Path: "service.go", Content: []byte("package service\nfunc HandleSubmit(){after-restart()}\n"), DocumentVersion: 54, Source: workspace.SourceIDEVersioned})
	if err != nil {
		t.Fatal(err)
	}
	var reconnectResult struct {
		resp *http.Response
		err  error
	}
	select {
	case reconnectResult = <-responseCh:
	case <-time.After(3 * time.Second):
		t.Fatal("reconnect did not return after next durable event")
	}
	if reconnectResult.err != nil || reconnectResult.resp == nil {
		t.Fatalf("reconnect HTTP request failed: %v", reconnectResult.err)
	}
	reconnectReader := bufio.NewReader(reconnectResult.resp.Body)
	lastSequence := 0
	if _, err := fmt.Sscanf(lastCursor, "event-%d", &lastSequence); err != nil {
		_ = reconnectResult.resp.Body.Close()
		t.Fatalf("parse reconnect cursor %q: %v", lastCursor, err)
	}
	seen := make([]string, 0, 8)
	foundTerminal := false
	for !foundTerminal {
		reconnectBlock, readErr := readSSEBlock(reconnectReader)
		if readErr != nil {
			_ = reconnectResult.resp.Body.Close()
			t.Fatalf("reconnect ended before the next snapshot terminal event: seen=%v err=%v", seen, readErr)
		}
		reconnectEnvelope, parseErr := parseSSEEnvelope(reconnectBlock)
		if parseErr != nil {
			_ = reconnectResult.resp.Body.Close()
			t.Fatalf("parse replayed reconnect event: block=%q err=%v", reconnectBlock, parseErr)
		}
		if reconnectEnvelope.EventType == "snapshot_sync" {
			_ = reconnectResult.resp.Body.Close()
			t.Fatalf("retained reconnect cursor unexpectedly fell back to full sync: seen=%v", seen)
		}
		if reconnectEnvelope.Sequence != lastSequence+1 {
			_ = reconnectResult.resp.Body.Close()
			t.Fatalf("reconnect replay sequence is not contiguous: previous=%d current=%d seen=%v", lastSequence, reconnectEnvelope.Sequence, seen)
		}
		lastSequence = reconnectEnvelope.Sequence
		seen = append(seen, fmt.Sprintf("%d:%s", reconnectEnvelope.Sequence, reconnectEnvelope.EventType))
		if reconnectEnvelope.ValidatedAgainstSnapshotID != nil && *reconnectEnvelope.ValidatedAgainstSnapshotID == nextSnapshot.SnapshotID && (reconnectEnvelope.EventType == "generation.published" || reconnectEnvelope.EventType == "generation.gap") {
			foundTerminal = true
		}
	}
	_ = reconnectResult.resp.Body.Close()
	return evidenceFor("VS03-A11", "tree-vs03", "http:replay", "http:full-sync", "full-sync:validated-proof-activity-gap", "restart:cursor="+lastCursor, "restart:next-event="+nextSnapshot.SnapshotID)
}

// RunA12 exercises the real HTTP stream past the server write-deadline window,
// then forces subscriber overflow and verifies the reconnect boundary.
func RunA12(t *testing.T) Evidence {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "service.go"), []byte("package service\nfunc HandleSubmit(){}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	server, err := flowview.NewServer(flowview.Config{RepoRoot: root, Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	server.Start()
	defer server.Shutdown(context.Background())
	client := &http.Client{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, _, err := server.SubmitVersionedEdit(context.Background(), workspace.EditRequest{Path: "service.go", Content: []byte("package service\nfunc HandleSubmit(){initial()}\n"), DocumentVersion: 1, Source: workspace.SourceIDEVersioned}); err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+server.Addr()+"/api/workspace/stream?token="+server.AuthToken()+"&lastEventId=event-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		t.Fatalf("stream returned status %d", resp.StatusCode)
	}
	defer resp.Body.Close()
	chunks := make(chan string, 64)
	readerDone := make(chan error, 1)
	go func() {
		reader := bufio.NewReader(resp.Body)
		var block strings.Builder
		for {
			line, readErr := reader.ReadString('\n')
			if len(line) != 0 {
				block.WriteString(line)
				if line == "\n" {
					chunks <- block.String()
					block.Reset()
				}
			}
			if readErr != nil {
				if !errors.Is(readErr, io.EOF) && !errors.Is(readErr, context.Canceled) {
					readerDone <- readErr
				} else {
					readerDone <- nil
				}
				return
			}
		}
	}()
	// The old production defect used a 15-second response write deadline. Hold
	// the same stream beyond it before requiring the next durable event.
	const hold = 16 * time.Second
	time.Sleep(hold)
	_, secondSnap, err := server.SubmitVersionedEdit(context.Background(), workspace.EditRequest{Path: "service.go", Content: []byte("package service\nfunc HandleSubmit(){after()}\n"), DocumentVersion: 2, Source: workspace.SourceIDEVersioned})
	if err != nil {
		t.Fatal(err)
	}
	sawHeartbeat := false
	sawAfterHoldEvent := false
	deadline := time.After(3 * time.Second)
	for !sawAfterHoldEvent {
		select {
		case block := <-chunks:
			if strings.Contains(block, ": heartbeat") {
				sawHeartbeat = true
			}
			if strings.Contains(block, secondSnap.SnapshotID) {
				sawAfterHoldEvent = true
			}
		case err := <-readerDone:
			if err != nil {
				t.Fatalf("stream terminated before post-deadline event: %v", err)
			}
			t.Fatal("stream terminated before post-deadline event")
		case <-deadline:
			t.Fatal("stream did not deliver an event after the 15-second deadline window")
		}
	}
	if !sawHeartbeat {
		t.Fatal("long-lived stream did not deliver a heartbeat")
	}
	cancel()
	select {
	case err := <-readerDone:
		if err != nil {
			t.Fatalf("stream reader did not close cleanly: %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("stream worker did not terminate after disconnect")
	}
	// A slow subscriber is disconnected after its bounded channel fills. Its
	// old cursor is outside the replay ring, so reconnect must request a full
	// snapshot sync rather than silently resuming from an arbitrary event.
	overflowHub := flowview.NewEventHub("overflow-vs03", 2)
	if _, err := overflowHub.PublishChecked("not-an-event", map[string]any{"i": 0}, nil, nil, nil); err == nil {
		t.Fatal("non-canonical event type was persisted")
	}
	basis := strings.Repeat("1", 64)
	if _, err := overflowHub.PublishChecked("generation.published", map[string]any{"i": 0}, &basis, nil, nil); err == nil {
		t.Fatal("generation event with incomplete identity was persisted")
	}
	if _, err := overflowHub.PublishChecked("activity.updated", "not-an-object", nil, nil, nil); err == nil {
		t.Fatal("event with non-object payload was persisted")
	}
	ch, _, _, cancelHub := overflowHub.Subscribe("")
	for i := 0; i < 80; i++ {
		if _, err := overflowHub.PublishChecked("activity.updated", map[string]any{"i": i}, nil, nil, nil); err != nil {
			t.Fatalf("valid overflow event %d was rejected: %v", i, err)
		}
	}
	select {
	case _, ok := <-ch:
		if ok {
			for range ch {
			}
		}
	case <-time.After(500 * time.Millisecond):
		cancelHub()
		t.Fatal("overflow subscriber was not disconnected")
	}
	cancelHub()
	_, replay, needsSync, cancelReconnect := overflowHub.Subscribe("event-1")
	cancelReconnect()
	if !needsSync || len(replay) != 0 {
		t.Fatalf("overflow reconnect did not require full snapshot sync: needsSync=%t replay=%+v", needsSync, replay)
	}
	syncEnv := overflowHub.SnapshotSync(map[string]any{"validatedProof": true, "activity": "idle"})
	if syncEnv == nil || syncEnv.EventType != "snapshot_sync" || syncEnv.Sequence != 80 {
		t.Fatalf("overflow reconnect returned invalid snapshot sync: %+v", syncEnv)
	}
	return evidenceFor("VS03-A12", "tree-vs03", "stream:held-16s", "heartbeat:received", "event:post-deadline", "subscriber:overflow-disconnect", "reconnect:full-sync")
}

// RunA13 measures separate activity-ack and current-or-gap distributions from
// the same trace IDs and computes each P95 independently. Every sample uses
// the public active-query/edit ingress and the server's always-on checkpoint
// consumer. The terminal may be current or a measured verified gap.
func RunA13(t *testing.T) Evidence {
	t.Helper()
	const samples = 24
	type traceSample struct {
		traceID               string
		activityLatencyMs     float64
		currentOrGapLatencyMs float64
		snapshotID            string
		treeID                string
		basisID               string
	}
	results := make(chan traceSample, samples)
	errorsCh := make(chan error, samples)
	var wg sync.WaitGroup
	for i := 0; i < samples; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			root := filepath.Join(t.TempDir(), fmt.Sprintf("sample-%02d", i))
			if err := os.MkdirAll(root, 0o755); err != nil {
				errorsCh <- err
				return
			}
			if err := os.WriteFile(filepath.Join(root, "service.go"), []byte("package service\nfunc HandleSubmit() {}\n"), 0o644); err != nil {
				errorsCh <- err
				return
			}
			server, err := flowview.NewServer(flowview.Config{RepoRoot: root, Port: 0})
			if err != nil {
				errorsCh <- err
				return
			}
			requestText := "show HandleSubmit flow"
			intent, err := semantic.NormalizeTaskIntent(requestText, semantic.IntentOptions{TaskID: fmt.Sprintf("task-vs03-a13-%02d", i), Revision: 1, Mode: "feature"})
			if err != nil || intent == nil || intent.IntentStatus != "parsed" {
				errorsCh <- fmt.Errorf("sample %d active task intent was not normalized: intent=%+v err=%v", i, intent, err)
				return
			}
			query := &semantic.TaskViewQuery{SchemaID: "https://codeflow.local/schemas/task-view-query.schema.json", SchemaVersion: 1, Mode: "feature", Feature: &semantic.FeatureQueryParams{Request: requestText, EntrySymbol: "service.go#HandleSubmit", Domain: "service"}}
			if err := server.RememberTaskQuery(query, requestText); err != nil {
				errorsCh <- fmt.Errorf("sample %d active task query was not registered: %w", i, err)
				return
			}
			server.Start()
			defer func() { _ = server.Shutdown(context.Background()) }()
			_, snap, err := server.SubmitVersionedEdit(context.Background(), workspace.EditRequest{Path: "service.go", Content: []byte(fmt.Sprintf("package service\nfunc HandleSubmit(){step%d()}\n", i)), DocumentVersion: 1, Source: workspace.SourceIDEVersioned})
			if err != nil {
				errorsCh <- err
				return
			}
			ack := server.CurrentActivity()
			if ack.TraceID == "" || ack.ActivityLatencyMs < 0 || ack.CurrentSnapshotID != snap.SnapshotID {
				errorsCh <- fmt.Errorf("sample %d has incomplete edit acknowledgement: %+v", i, ack)
				return
			}
			deadline := time.Now().Add(5 * time.Second)
			var terminal workspace.ActivityStatus
			for time.Now().Before(deadline) {
				terminal = server.CurrentActivity()
				if pipelineErr := server.LastPipelineError(); pipelineErr != nil {
					errorsCh <- fmt.Errorf("sample %d live pipeline failed: %w; activity=%+v", i, pipelineErr, terminal)
					return
				}
				if !terminal.CurrentOrGapAt.IsZero() {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if terminal.CurrentOrGapAt.IsZero() || terminal.CurrentOrGapLatencyMs < 0 || terminal.TraceID != ack.TraceID {
				errorsCh <- fmt.Errorf("sample %d has no same-trace current-or-gap terminal: pipelineErr=%v ack=%+v terminal=%+v", i, server.LastPipelineError(), ack, terminal)
				return
			}
			ledger, err := os.ReadFile(filepath.Join(root, ".codeflow", "semantics", "events", "event-ledger.jsonl"))
			if err != nil || (!strings.Contains(string(ledger), "generation.gap") && !strings.Contains(string(ledger), "generation.published")) {
				errorsCh <- fmt.Errorf("sample %d has no durable current-or-gap event: err=%v ledger=%q", i, err, ledger)
				return
			}
			results <- traceSample{traceID: ack.TraceID, activityLatencyMs: float64(ack.ActivityLatencyMs), currentOrGapLatencyMs: float64(terminal.CurrentOrGapLatencyMs), snapshotID: snap.SnapshotID, treeID: snap.RootTreeID, basisID: snap.ComputedBasisID}
		}(i)
	}
	wg.Wait()
	close(results)
	close(errorsCh)
	for err := range errorsCh {
		t.Fatal(err)
	}
	traces := make([]string, 0, samples)
	activity := make([]float64, 0, samples)
	currentOrGap := make([]float64, 0, samples)
	var first traceSample
	for sample := range results {
		if len(traces) == 0 {
			first = sample
		}
		traces = append(traces, sample.traceID)
		activity = append(activity, sample.activityLatencyMs)
		currentOrGap = append(currentOrGap, sample.currentOrGapLatencyMs)
	}
	activityP95 := p95(activity)
	gapP95 := p95(currentOrGap)
	if len(traces) < 20 || len(activity) != len(traces) || len(currentOrGap) != len(traces) || activityP95 > 300 || gapP95 > 3000 {
		t.Fatalf("measured P95 exceeds supported profile: activity=%v gap=%v n=%d", activityP95, gapP95, len(traces))
	}
	return Evidence{Criterion: "VS03-A13", ImplementationTestID: implementationTestIDs["VS03-A13"], ImplementationPackage: "codeflow/internal/verification/publication", SnapshotTreeDigest: first.treeID, ObjectRefs: []string{"snapshot:" + first.snapshotID, "tree:" + first.treeID, "basis:" + first.basisID, "trace:same-trace-distributions", "profile:local-supported", "measurement:activity-ack", "measurement:current-or-gap"}, Trace: &TraceEvidence{TraceIDs: traces, ActivitySamplesMs: activity, CurrentOrGapSamplesMs: currentOrGap, ActivityP95Ms: activityP95, CurrentOrGapP95Ms: gapP95, Profile: "local-supported"}}
}

// RunA14 measures selected-step and logical-anchor preservation over a
// versioned eligible-update corpus.  The structural identity is checked by
// the production SemanticDelta comparator; identity-loss updates are excluded
// explicitly and never inflate the denominator.
func RunA14(t *testing.T) Evidence {
	t.Helper()
	const corpusVersion = "flowview-selection-corpus-v1"
	const eligible = 100
	const identityLoss = 2
	base := candidateMap("basis-vs03", "snapshot-vs03", "generation-vs03-base", 1, "Q3")
	base.Steps = append(base.Steps,
		semantic.SemanticStep{StepID: "step-result", StructuralIdentity: "service.go#Submit:result", Ordinal: 2, Name: "Result", TechnicalName: "service.go#Result", Kind: "result", Anchor: slicing.Anchor{RepoRelativePath: "service.go", EnclosingSymbolPath: "service.go#Result", ByteRange: [2]int{10, 20}}, EvidenceRefs: []string{"evidence-result"}},
	)
	base.Evidence = append(base.Evidence, semantic.SemanticEvidence{EvidenceID: "evidence-result", Kind: "source", SourceAuthority: "code", ComputedBasisID: base.ComputedBasisID, SnapshotID: base.ValidatedAgainstSnapshotID, ValidationStatus: "verified", Anchor: base.Steps[1].Anchor})
	previousSelection := flowview.LogicalViewSelection{
		SelectedStepID:             base.Steps[0].StepID,
		SelectedStructuralIdentity: base.Steps[0].StructuralIdentity,
		ScrollAnchor:               &flowview.LogicalScrollAnchor{StepID: base.Steps[0].StepID, StructuralIdentity: base.Steps[0].StructuralIdentity, OffsetPx: 24},
	}
	preserved := 0
	for i := 0; i < eligible; i++ {
		current := *base
		current.GenerationID = fmt.Sprintf("generation-vs03-%03d", i+1)
		current.ComputedBasisID = fmt.Sprintf("basis-vs03-%03d", i+1)
		current.ValidatedAgainstSnapshotID = fmt.Sprintf("snapshot-vs03-%03d", i+1)
		current.Basis = base.Basis
		current.Basis.ComputedBasisID = current.ComputedBasisID
		current.Basis.ComputedWorkspaceSnapshotID = current.ValidatedAgainstSnapshotID
		current.Basis.SnapshotTreeID = fmt.Sprintf("tree-vs03-%03d", i+1)
		current.Steps = append([]semantic.SemanticStep(nil), base.Steps...)
		current.Steps[0].Anchor.ByteRange = [2]int{i + 1, i + 2}
		current.Evidence = append([]semantic.SemanticEvidence(nil), base.Evidence...)
		current.Evidence[0].ComputedBasisID = current.ComputedBasisID
		current.Evidence[0].SnapshotID = current.ValidatedAgainstSnapshotID
		delta, err := semantic.ComputeSemanticDelta(fmt.Sprintf("selection-%03d", i), base, &current)
		if err != nil {
			t.Fatal(err)
		}
		projection := semantic.BuildFlowViewProjection(&current)
		if err := semantic.ValidateFlowViewProjectionAgainstMap(projection, &current); err != nil {
			t.Fatal(err)
		}
		nextIdentities := make([]flowview.ViewStepIdentity, 0, len(current.Steps))
		for _, step := range current.Steps {
			nextIdentities = append(nextIdentities, flowview.ViewStepIdentity{StepID: step.StepID, StructuralIdentity: step.StructuralIdentity})
		}
		preservedSelection := flowview.PreserveLogicalViewSelection(previousSelection, nextIdentities)
		if !preservedSelection.Preserved || preservedSelection.SelectedStepID != current.Steps[0].StepID || preservedSelection.ScrollAnchor == nil || preservedSelection.ScrollAnchor.StructuralIdentity != base.Steps[0].StructuralIdentity || projection == nil || delta == nil {
			t.Fatalf("production view-state seam lost an eligible selection at corpus item %d: result=%+v delta=%+v projection=%+v", i, preservedSelection, delta, projection)
		}
		if containsString(projection.VisibleStepRefs, preservedSelection.SelectedStepID) {
			preserved++
		}
	}
	// Removed and duplicate structural identities are explicit loss outcomes and
	// are not admitted to the eligible-update denominator.
	for i := 0; i < identityLoss; i++ {
		current := *base
		current.GenerationID = fmt.Sprintf("generation-vs03-identity-loss-%d", i)
		current.ComputedBasisID = fmt.Sprintf("basis-vs03-identity-loss-%d", i)
		current.ValidatedAgainstSnapshotID = fmt.Sprintf("snapshot-vs03-identity-loss-%d", i)
		current.Basis = base.Basis
		current.Basis.ComputedBasisID = current.ComputedBasisID
		current.Basis.ComputedWorkspaceSnapshotID = current.ValidatedAgainstSnapshotID
		current.Basis.SnapshotTreeID = fmt.Sprintf("tree-vs03-identity-loss-%d", i)
		if i == 0 {
			current.Steps = []semantic.SemanticStep{base.Steps[1]}
		} else {
			current.Steps = []semantic.SemanticStep{base.Steps[0], base.Steps[0]}
		}
		current.Evidence = append([]semantic.SemanticEvidence(nil), base.Evidence[1])
		if _, err := semantic.ComputeSemanticDelta(fmt.Sprintf("selection-identity-loss-%03d", i), base, &current); err != nil {
			t.Fatal(err)
		}
		nextIdentities := make([]flowview.ViewStepIdentity, 0, len(current.Steps))
		for _, step := range current.Steps {
			nextIdentities = append(nextIdentities, flowview.ViewStepIdentity{StepID: step.StepID, StructuralIdentity: step.StructuralIdentity})
		}
		lost := flowview.PreserveLogicalViewSelection(previousSelection, nextIdentities)
		if !lost.IdentityLoss || lost.Preserved {
			t.Fatalf("production view-state seam accepted identity-loss corpus item %d: %+v", i, lost)
		}
	}
	denominator := eligible
	if preserved != denominator {
		t.Fatalf("selected step or logical anchor was not preserved in eligible corpus: %d/%d", preserved, denominator)
	}
	percent := float64(preserved) * 100 / float64(denominator)
	if percent < 99 || identityLoss <= 0 {
		t.Fatalf("selection preservation corpus below contract threshold: %.2f%% excluded=%d", percent, identityLoss)
	}
	corpus := &ViewCorpusEvidence{CorpusVersion: corpusVersion, EligibleUpdates: eligible, PreservedUpdates: preserved, ExcludedIdentityLoss: identityLoss, Numerator: preserved, Denominator: denominator, PreservationPercent: percent}
	e := evidenceFor("VS03-A14", "tree-vs03", "corpus:"+corpusVersion, fmt.Sprintf("numerator:%d", preserved), fmt.Sprintf("denominator:%d", denominator), fmt.Sprintf("excluded_identity_loss:%d", identityLoss))
	e.ViewCorpus = corpus
	return e
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// RunA15 verifies the narrow promotion seam. A candidate becomes confirmed
// only when the exact current semantic-map CAS object, its validated proof and
// pointer, and every required basis-bound verified Evidence item agree. The
// negative corpus keeps the proof identities separate from the map so a
// caller-created status or reference cannot launder candidate authority.
func RunA15(t *testing.T) Evidence {
	t.Helper()
	root := t.TempDir()
	st := storage.New(root)
	if err := st.InitLayout(); err != nil {
		t.Fatal(err)
	}
	authority, head := newPublicationAuthority(t, root, workspace.WorkspaceIdentity{
		RepositoryID: vs03RepositoryID, WorktreeID: vs03WorktreeID, ConfigurationFingerprint: vs03Config,
	}, "service.go", []byte("package service\nfunc Submit(){}\n"))

	mapIR := candidateMap(head.ComputedBasisID, head.SnapshotID, "generation-vs03-current", 1, "Q3")
	mapIR.Steps[0].Rules = []string{"AC-submit"}
	mapIR.Basis.SnapshotTreeID = head.RootTreeID
	tx := publicationTransaction(mapIR.GenerationID, head.SnapshotID, head.ComputedBasisID, nil, true)
	replaceSemanticMapArtifact(&tx, mapIR)
	bindAuthoritativeHead(&tx, authority, head.SnapshotID)
	if _, err := st.PublishGeneration(tx); err != nil {
		t.Fatalf("publish current proof fixture: %v", err)
	}
	currentMapBytes := append([]byte(nil), tx.Artifacts[tx.Manifest.ArtifactRefs.SemanticMap]...)
	var currentMap semantic.SemanticMapIR
	if err := json.Unmarshal(currentMapBytes, &currentMap); err != nil {
		t.Fatalf("decode current semantic-map fixture: %v", err)
	}
	manifest, pointer, err := st.ReadValidatedActiveProofManifest()
	if err != nil || manifest == nil || pointer == nil {
		t.Fatalf("read validated current proof fixture: manifest=%+v pointer=%+v err=%v", manifest, pointer, err)
	}

	criteria := []semantic.AcceptanceCriterion{{ID: "AC-submit", Text: "Submit", RequiredEvidenceKinds: []string{"source"}}}
	alignments := semantic.ComputeRequirementAlignment(criteria, &currentMap, semantic.AlignmentOptions{
		AgentDeclarations: []string{"candidate says AC-submit is confirmed"},
		ModelProposals:    []string{"model says AC-submit is confirmed"},
	})
	if len(alignments) != 1 || alignments[0].Status != "partial" || alignments[0].Reason != "awaiting_current_proof" || alignments[0].Authority != "candidate" || len(alignments[0].EvidenceRefs) == 0 {
		t.Fatalf("candidate was not held at awaiting_current_proof before promotion: %+v", alignments)
	}
	confirmed := semantic.PromoteRequirementAlignmentsWithCurrentProof(criteria, &currentMap, manifest, pointer, semantic.AlignmentOptions{
		AgentDeclarations: []string{"candidate says AC-submit is confirmed"},
		ModelProposals:    []string{"model says AC-submit is confirmed"},
	})
	if len(confirmed) != 1 || confirmed[0].Status != "confirmed" || confirmed[0].Authority != "current_proof" || confirmed[0].Reason != "" || len(confirmed[0].MissingEvidence) != 0 {
		t.Fatalf("valid current proof and required evidence were not promoted: %+v", confirmed)
	}

	cloneMap := func(in *semantic.SemanticMapIR) *semantic.SemanticMapIR {
		data, marshalErr := json.Marshal(in)
		if marshalErr != nil {
			t.Fatalf("clone semantic map: %v", marshalErr)
		}
		var out semantic.SemanticMapIR
		if unmarshalErr := json.Unmarshal(data, &out); unmarshalErr != nil {
			t.Fatalf("decode cloned semantic map: %v", unmarshalErr)
		}
		return &out
	}
	cloneProof := func(in *storage.GenerationProofManifest) *storage.GenerationProofManifest {
		if in == nil {
			return nil
		}
		out := *in
		return &out
	}
	cloneActive := func(in *storage.ActivePointer) *storage.ActivePointer {
		if in == nil {
			return nil
		}
		out := *in
		return &out
	}
	// When a negative case changes the map, update only the claimed CAS
	// reference. This isolates evidence/identity rejection from an unrelated
	// stale-CAS rejection and proves the promotion seam rechecks its contents.
	claimedMapRef := func(in *semantic.SemanticMapIR, proof *storage.GenerationProofManifest) {
		if in == nil || proof == nil {
			return
		}
		data, marshalErr := json.Marshal(in)
		if marshalErr != nil {
			t.Fatalf("marshal negative semantic map: %v", marshalErr)
		}
		proof.ArtifactRefs.SemanticMap = storage.ArtifactCASRef(data)
	}
	notConfirmed := func(name string, in *semantic.SemanticMapIR, proof *storage.GenerationProofManifest, active *storage.ActivePointer) {
		t.Helper()
		got := semantic.PromoteRequirementAlignmentsWithCurrentProof(criteria, in, proof, active, semantic.AlignmentOptions{})
		if len(got) != 1 || got[0].Status == "confirmed" || got[0].Authority == "current_proof" {
			t.Fatalf("negative promotion case %s was confirmed: %+v", name, got)
		}
	}

	negativeCases := []struct {
		name   string
		mutate func(*semantic.SemanticMapIR, *storage.GenerationProofManifest, *storage.ActivePointer)
	}{
		{name: "missing-proof", mutate: func(_ *semantic.SemanticMapIR, proof *storage.GenerationProofManifest, active *storage.ActivePointer) {
			_ = proof
			_ = active
		}},
		{name: "basis-mismatch", mutate: func(in *semantic.SemanticMapIR, proof *storage.GenerationProofManifest, _ *storage.ActivePointer) {
			in.ComputedBasisID = "basis-vs03-drift"
			in.Basis.ComputedBasisID = in.ComputedBasisID
			claimedMapRef(in, proof)
		}},
		{name: "generation-mismatch", mutate: func(_ *semantic.SemanticMapIR, _ *storage.GenerationProofManifest, active *storage.ActivePointer) {
			active.GenerationID = "generation-vs03-other"
		}},
		{name: "intent-mismatch", mutate: func(_ *semantic.SemanticMapIR, _ *storage.GenerationProofManifest, active *storage.ActivePointer) {
			active.TaskIntentRevision++
		}},
		{name: "query-mismatch", mutate: func(_ *semantic.SemanticMapIR, proof *storage.GenerationProofManifest, _ *storage.ActivePointer) {
			proof.NormalizedQueryHash = strings.Repeat("2", 64)
		}},
		{name: "missing-verified-evidence", mutate: func(in *semantic.SemanticMapIR, proof *storage.GenerationProofManifest, _ *storage.ActivePointer) {
			in.Evidence = nil
			claimedMapRef(in, proof)
		}},
		{name: "ref-string-only", mutate: func(in *semantic.SemanticMapIR, proof *storage.GenerationProofManifest, _ *storage.ActivePointer) {
			in.Steps[0].EvidenceRefs = []string{"evidence-only-reference"}
			claimedMapRef(in, proof)
		}},
		{name: "stale-evidence", mutate: func(in *semantic.SemanticMapIR, proof *storage.GenerationProofManifest, _ *storage.ActivePointer) {
			in.Evidence[0].SnapshotID = "snapshot-vs03-stale"
			claimedMapRef(in, proof)
		}},
		{name: "conflicting-evidence", mutate: func(in *semantic.SemanticMapIR, proof *storage.GenerationProofManifest, _ *storage.ActivePointer) {
			in.Evidence[0].ValidationStatus = "conflicting"
			claimedMapRef(in, proof)
		}},
		{name: "wrong-authority-evidence", mutate: func(in *semantic.SemanticMapIR, proof *storage.GenerationProofManifest, _ *storage.ActivePointer) {
			in.Evidence[0].SourceAuthority = "model"
			claimedMapRef(in, proof)
		}},
	}
	for _, tc := range negativeCases {
		t.Run(tc.name, func(t *testing.T) {
			in := cloneMap(&currentMap)
			proof := cloneProof(manifest)
			active := cloneActive(pointer)
			if tc.name == "missing-proof" {
				notConfirmed(tc.name, in, nil, nil)
				return
			}
			tc.mutate(in, proof, active)
			notConfirmed(tc.name, in, proof, active)
		})
	}

	return evidenceFor("VS03-A15", head.RootTreeID, "alignment:confirmed", "authority:current-proof", "proof:validated", "evidence:verified-required", "negative:10-identity-and-evidence-cases")
}

// RunA16 proves the immutable Static FlowView and the event-driven Live
// Semantic View are separate public HTTP surfaces.
func RunA16(t *testing.T) Evidence {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "service.go"), []byte("package service\nfunc Submit() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv, err := flowview.NewServer(flowview.Config{RepoRoot: root, Port: 0, AuthToken: "vs03-a16"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.SubmitVersionedChanges(context.Background(), workspace.VersionedChangeRequest{BatchID: "seed-a16", Source: workspace.SourceWatcherFallback, Changes: []workspace.VersionedChange{{Kind: workspace.ChangeUpsert, Path: "service.go", Content: []byte("package service\nfunc Submit() {}\n")}}}); err != nil {
		t.Fatal(err)
	}
	srv.Start()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			t.Errorf("shutdown static/live fixture: %v", err)
		}
	}()

	readPage := func(rawURL string) string {
		t.Helper()
		response, err := http.Get(rawURL)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("GET %s returned %d", rawURL, response.StatusCode)
		}
		body, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		return string(body)
	}
	staticHTML := readPage(srv.URL())
	liveHTML := readPage(strings.Replace(srv.URL(), "/?token=", "/live?token=", 1))
	staticMarkup := strings.SplitN(staticHTML, "<script", 2)[0]
	initStart := strings.Index(staticHTML, "async function init(){")
	if initStart < 0 {
		t.Fatal("Static FlowView initializer was not found")
	}
	initEnd := strings.Index(staticHTML[initStart:], "\n}\n\nasync function loadFlow")
	if initEnd < 0 {
		t.Fatal("Static FlowView initializer boundary was not found")
	}
	initializer := staticHTML[initStart : initStart+initEnd]
	if strings.Contains(staticMarkup, "workspace-activity-badge") || strings.Contains(staticMarkup, "workspace-pending-count") || strings.Contains(initializer, "loadWorkspaceActivity()") {
		t.Fatal("Static FlowView still exposes mutable workspace state or starts the workspace stream")
	}
	if !strings.Contains(initializer, "initLiveStream()") || (!strings.Contains(initializer, "'/live'") && !strings.Contains(initializer, "liveParam")) {
		t.Fatal("Static FlowView must gate the workspace stream on live mode instead of always connecting or never offering it")
	}
	if !strings.Contains(liveHTML, `data-view="live-semantic-map"`) || !strings.Contains(liveHTML, "new EventSource('/api/workspace/stream") {
		t.Fatal("Live Semantic View does not own the workspace stream")
	}
	head := srv.SnapshotEngine().LiveHead()
	if head == nil {
		t.Fatal("static/live fixture has no immutable snapshot")
	}
	return evidenceFor("VS03-A16", head.RootTreeID, "surface:static-immutable", "surface:live-event-driven", "snapshot-boundary:"+head.SnapshotID)
}

// RunA17 exercises the authenticated HTTP ingress with VS Code operations,
// one multi-file agent batch, and a duplicate watcher capture.
func RunA17(t *testing.T) Evidence {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "service.go"), []byte("package service\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv, err := flowview.NewServer(flowview.Config{RepoRoot: root, Port: 0, AuthToken: "vs03-a17"})
	if err != nil {
		t.Fatal(err)
	}
	srv.Start()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			t.Errorf("shutdown ingress fixture: %v", err)
		}
	}()

	endpoint := strings.Replace(srv.URL(), "/?token=", "/api/workspace/edit?token=", 1)
	unauthorized, err := http.Post("http://"+srv.Addr()+"/api/workspace/edit", "application/json", strings.NewReader(`{"path":"denied.go","content":"package denied","documentVersion":1}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = unauthorized.Body.Close()
	if unauthorized.StatusCode == http.StatusOK {
		t.Fatal("workspace ingress accepted an unauthenticated edit")
	}
	type ingressResponse struct {
		Batch     *workspace.ChangeBatch        `json:"batch"`
		Revisions []*workspace.DocumentRevision `json:"revisions"`
		Snapshot  *workspace.WorkspaceSnapshot  `json:"snapshot"`
		Duplicate bool                          `json:"duplicate"`
	}
	post := func(body map[string]any) ingressResponse {
		t.Helper()
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		response, err := http.Post(endpoint, "application/json", bytes.NewReader(encoded))
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			message, _ := io.ReadAll(response.Body)
			t.Fatalf("workspace ingress returned %d: %s", response.StatusCode, message)
		}
		var result ingressResponse
		if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	agentContent := "package agent\nfunc First() {}\n"
	agent := post(map[string]any{
		"batchId": "agent-completed-write-a17", "source": workspace.SourceAgentTransaction,
		"changes": []map[string]any{
			{"kind": workspace.ChangeCreate, "path": "agent_first.go", "content": agentContent, "documentVersion": 1},
			{"kind": workspace.ChangeCreate, "path": "agent_second.go", "content": "package agent\nfunc Second() {}\n", "documentVersion": 1},
		},
	})
	if agent.Batch == nil || agent.Batch.BatchID != "agent-completed-write-a17" || agent.Batch.Source != workspace.SourceAgentTransaction || len(agent.Revisions) != 2 {
		t.Fatalf("agent batch identity was not preserved: %+v", agent)
	}
	duplicate := post(map[string]any{
		"batchId": "watcher-duplicate-a17", "source": workspace.SourceWatcherFallback,
		"changes": []map[string]any{{"kind": workspace.ChangeUpsert, "path": "agent_first.go", "content": agentContent}},
	})
	if !duplicate.Duplicate || duplicate.Snapshot == nil || duplicate.Snapshot.SnapshotID != agent.Snapshot.SnapshotID {
		t.Fatalf("watcher duplicate created a second snapshot: %+v", duplicate)
	}

	created := post(map[string]any{"batchId": "vscode-create-a17", "source": workspace.SourceIDEVersioned, "changes": []map[string]any{{"kind": workspace.ChangeCreate, "path": "vscode.go", "content": "package vscode\n", "documentVersion": 1}}})
	updated := post(map[string]any{"batchId": "vscode-upsert-a17", "source": workspace.SourceIDEVersioned, "changes": []map[string]any{{"kind": workspace.ChangeUpsert, "path": "vscode.go", "content": "package vscode\nfunc Saved() {}\n", "documentVersion": 2}}})
	renamed := post(map[string]any{"batchId": "vscode-rename-a17", "source": workspace.SourceIDEVersioned, "changes": []map[string]any{{"kind": workspace.ChangeRename, "oldPath": "vscode.go", "path": "vscode_saved.go", "content": "package vscode\nfunc Saved() {}\n", "documentVersion": 1}}})
	deleted := post(map[string]any{"batchId": "vscode-delete-a17", "source": workspace.SourceIDEVersioned, "changes": []map[string]any{{"kind": workspace.ChangeDelete, "path": "vscode_saved.go"}}})
	for name, result := range map[string]ingressResponse{"create": created, "upsert": updated, "rename": renamed, "delete": deleted} {
		if result.Batch == nil || result.Batch.Source != workspace.SourceIDEVersioned || result.Batch.BatchID == "" || result.Snapshot == nil {
			t.Fatalf("VS Code %s lost source or batch identity: %+v", name, result)
		}
	}
	if _, exists := deleted.Snapshot.Entries["vscode_saved.go"]; exists {
		t.Fatal("VS Code delete did not remove the canonical path")
	}
	return evidenceFor("VS03-A17", deleted.Snapshot.RootTreeID, "batch:"+agent.Batch.BatchID, "source:"+agent.Batch.Source, "duplicate:"+duplicate.Snapshot.SnapshotID, "operations:create-upsert-rename-delete")
}

// RunA18 proves the coordinator watcher captures stable repository bytes,
// recognizes a measured rename, ignores excluded paths, and reports a
// repository identity transition as reconciliation.
func RunA18(t *testing.T) Evidence {
	t.Helper()
	t.Setenv("CODEFLOW_WATCH_DEBOUNCE_MS", "10")
	root := t.TempDir()
	original := []byte("package service\nfunc Submit() {}\n")
	if err := os.WriteFile(filepath.Join(root, "service.go"), original, 0o644); err != nil {
		t.Fatal(err)
	}
	srv, err := flowview.NewServer(flowview.Config{RepoRoot: root, Port: 0, AuthToken: "vs03-a18", WorkspaceWatchInterval: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	srv.Start()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			t.Errorf("shutdown watcher fixture: %v", err)
		}
	}()
	waitHead := func(predicate func(*workspace.WorkspaceSnapshot) bool) *workspace.WorkspaceSnapshot {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if head := srv.SnapshotEngine().LiveHead(); head != nil && predicate(head) {
				return head
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("coordinator watcher did not publish the expected snapshot")
		return nil
	}
	// Let the first poll establish its comparison baseline.
	time.Sleep(35 * time.Millisecond)
	changed := []byte("package service\nfunc Submit() { Changed() }\n")
	if err := os.WriteFile(filepath.Join(root, "service.go"), changed, 0o644); err != nil {
		t.Fatal(err)
	}
	changedDigest := sha256.Sum256(changed)
	head := waitHead(func(snapshot *workspace.WorkspaceSnapshot) bool {
		entry, ok := snapshot.Entries["service.go"]
		return ok && entry.ContentID == hex.EncodeToString(changedDigest[:])
	})
	if err := os.Rename(filepath.Join(root, "service.go"), filepath.Join(root, "renamed.go")); err != nil {
		t.Fatal(err)
	}
	head = waitHead(func(snapshot *workspace.WorkspaceSnapshot) bool {
		_, oldExists := snapshot.Entries["service.go"]
		entry, newExists := snapshot.Entries["renamed.go"]
		return !oldExists && newExists && entry.ContentID == hex.EncodeToString(changedDigest[:])
	})
	if err := os.MkdirAll(filepath.Join(root, "node_modules", "ignored"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "node_modules", "ignored", "ignored.go"), []byte("package ignored\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.go")
	if err := os.WriteFile(outside, []byte("package outside\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	time.Sleep(40 * time.Millisecond)
	head = srv.SnapshotEngine().LiveHead()
	if _, exists := head.Entries["node_modules/ignored/ignored.go"]; exists {
		t.Fatal("ignored source path entered the workspace ingress")
	}
	if _, exists := head.Entries[outside]; exists {
		t.Fatal("outside path entered the workspace ingress")
	}

	identityRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(identityRoot, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(identityRoot, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(identityRoot, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	watchCtx, cancelWatch := context.WithCancel(context.Background())
	defer cancelWatch()
	signals := make(chan watch.ChangeSet, 2)
	go func() {
		_ = watch.WatchChanges(watchCtx, identityRoot, 10*time.Millisecond, func(change watch.ChangeSet) { signals <- change })
	}()
	time.Sleep(35 * time.Millisecond)
	if err := os.WriteFile(filepath.Join(identityRoot, ".git", "HEAD"), []byte("ref: refs/heads/feature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	select {
	case signal := <-signals:
		if !signal.Reconcile || signal.Reason != "repository identity changed" {
			t.Fatalf("branch/worktree transition was not a reconciliation signal: %+v", signal)
		}
	case <-time.After(time.Second):
		t.Fatal("watcher did not report repository identity reconciliation")
	}
	return evidenceFor("VS03-A18", head.RootTreeID, "capture:stat-read-stat", "rename:measured", "ignored:excluded", "reconcile:repository-identity")
}

// RunA19 publishes one complete persisted proof-backed Live view and retrieves
// it by the exact event identities through the production HTTP endpoint.
func RunA19(t *testing.T) Evidence {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "service.go"), []byte("package service\nfunc Submit() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv, err := flowview.NewServer(flowview.Config{RepoRoot: root, Port: 0, AuthToken: "vs03-a19"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.SubmitVersionedChanges(context.Background(), workspace.VersionedChangeRequest{BatchID: "seed-a19", Source: workspace.SourceWatcherFallback, Changes: []workspace.VersionedChange{{Kind: workspace.ChangeUpsert, Path: "service.go", Content: []byte("package service\nfunc Submit() {}\n")}}}); err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			t.Errorf("shutdown generation fixture: %v", err)
		}
	}()
	head := srv.SnapshotEngine().LiveHead()
	if head == nil {
		t.Fatal("generation fixture has no live head")
	}
	generationID := "generation-vs03-a19"
	mapIR := candidateMap(head.ComputedBasisID, head.SnapshotID, generationID, 1, "Q3")
	mapIR.ValidatedAgainstSnapshotID = head.SnapshotID
	mapIR.Basis.RepositoryID = head.RepositoryID
	mapIR.Basis.WorktreeID = head.WorktreeID
	mapIR.Basis.WorkspaceEpoch = head.WorkspaceEpoch
	mapIR.Basis.ComputedWorkspaceSnapshotID = head.SnapshotID
	mapIR.Basis.ComputedBasisID = head.ComputedBasisID
	mapIR.Basis.SnapshotTreeID = head.RootTreeID
	mapIR.Basis.DependencyFingerprint = head.DependencyFingerprint
	mapIR.Basis.ConfigurationFingerprint = head.ConfigurationFingerprint
	tx := publicationTransaction(generationID, head.SnapshotID, head.ComputedBasisID, nil, true)
	tx.Event = generationPublishedEvent(3, head.ComputedBasisID, head.SnapshotID, generationID)
	replaceSemanticMapArtifact(&tx, mapIR)
	tx.Manifest.WorkspaceEpoch = head.WorkspaceEpoch
	tx.Manifest.ComputedSnapshotID = head.SnapshotID
	tx.Manifest.ValidatedAgainstSnapshotID = head.SnapshotID
	tx.Pointer.WorkspaceEpoch = head.WorkspaceEpoch
	tx.Pointer.RepositoryID = head.RepositoryID
	tx.Pointer.WorktreeID = head.WorktreeID
	liveView, err := json.Marshal(map[string]any{
		"schemaId": "codeflow.live-generation-view", "schemaVersion": 1,
		"generationId": generationID, "computedBasisId": head.ComputedBasisID, "snapshotId": head.SnapshotID,
		"flowContexts": map[string]any{},
	})
	if err != nil {
		t.Fatal(err)
	}
	liveViewRef := storage.ArtifactCASRef(liveView)
	tx.Manifest.ArtifactRefs.LiveView = liveViewRef
	tx.Artifacts[liveViewRef] = liveView
	bindAuthoritativeHead(&tx, srv.SnapshotEngine(), head.SnapshotID)
	store := storage.New(root)
	if _, err := store.PublishGeneration(tx); err != nil {
		t.Fatalf("publish generation-bound Live view: %v", err)
	}
	srv.Start()
	exactURL := strings.Replace(srv.URL(), "/?token=", "/api/live/generation?generationId="+generationID+"&computedBasisId="+head.ComputedBasisID+"&snapshotId="+head.SnapshotID+"&token=", 1)
	response, err := http.Get(exactURL)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		message, _ := io.ReadAll(response.Body)
		_ = response.Body.Close()
		t.Fatalf("exact generation endpoint returned %d: %s", response.StatusCode, message)
	}
	var exact struct {
		SemanticMap   semantic.SemanticMapIR          `json:"semanticMap"`
		Projection    semantic.FlowViewProjection     `json:"projection"`
		ProofManifest storage.GenerationProofManifest `json:"proofManifest"`
		ViewState     map[string]any                  `json:"viewState"`
	}
	if err := json.NewDecoder(response.Body).Decode(&exact); err != nil {
		_ = response.Body.Close()
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if exact.SemanticMap.GenerationID != generationID || exact.ProofManifest.ComputedBasisID != head.ComputedBasisID || exact.Projection.GenerationID != generationID {
		t.Fatalf("generation endpoint mixed persisted identities: %+v", exact)
	}
	mismatchURL := strings.Replace(exactURL, "snapshotId="+head.SnapshotID, "snapshotId=snapshot-mismatch", 1)
	mismatch, err := http.Get(mismatchURL)
	if err != nil {
		t.Fatal(err)
	}
	var mismatchBody map[string]any
	_ = json.NewDecoder(mismatch.Body).Decode(&mismatchBody)
	_ = mismatch.Body.Close()
	if mismatch.StatusCode != http.StatusConflict || mismatchBody["code"] != "generation_unavailable" {
		t.Fatalf("proof mismatch did not fail closed: status=%d body=%v", mismatch.StatusCode, mismatchBody)
	}
	eventStart := strings.Index(flowview.LiveSemanticHTML, "async function receivePublishedGeneration")
	if eventStart < 0 {
		t.Fatal("Live published-event handler was not found")
	}
	eventEnd := strings.Index(flowview.LiveSemanticHTML[eventStart:], "function startLive")
	if eventEnd < 0 {
		t.Fatal("Live published-event handler boundary was not found")
	}
	eventHandler := flowview.LiveSemanticHTML[eventStart : eventStart+eventEnd]
	if !strings.Contains(eventHandler, "/api/live/generation?") || strings.Contains(eventHandler, "/api/task/view") || strings.Contains(eventHandler, "query(") {
		t.Fatal("published-event rendering is not bound exclusively to the persisted generation endpoint")
	}
	return evidenceFor("VS03-A19", head.RootTreeID, "generation:"+generationID, "proof:"+tx.Manifest.ProofID, "live-view:"+liveViewRef, "mismatch:generation-unavailable")
}

// RunA20 binds the Change Pulse to verified semantic facets and checks the
// production selection transition plus explicit retain/apply UI states.
func RunA20(t *testing.T) Evidence {
	t.Helper()
	baseline := candidateMap("basis-vs03-a20-before", "snapshot-vs03-a20-before", "generation-vs03-a20-before", 1, "Q3")
	current := candidateMap("basis-vs03-a20-after", "snapshot-vs03-a20-after", "generation-vs03-a20-after", 2, "Q3")
	baseline.Task.TaskID, current.Task.TaskID = "task-vs03-a20", "task-vs03-a20"
	baseline.Basis.RepositoryID, current.Basis.RepositoryID = "repo-vs03-a20", "repo-vs03-a20"
	baseline.Basis.WorkspaceEpoch, current.Basis.WorkspaceEpoch = 7, 7
	baseline.Basis.SnapshotTreeID, current.Basis.SnapshotTreeID = "tree-vs03-a20", "tree-vs03-a20"
	baseBranch, currentBranch := "amount > 0", "amount >= 100"
	baseEffect, currentEffect := "charge gateway", "authorize gateway"
	baseline.Steps[0].Branch = &baseBranch
	baseline.Steps[0].StateDelta = &fusion.StateDelta{Before: "pending", After: "paid"}
	baseline.Steps[0].SideEffect = &baseEffect
	baseline.Steps[0].Rules = []string{"charge"}
	current.Steps[0].Branch = &currentBranch
	current.Steps[0].StateDelta = &fusion.StateDelta{Before: "pending", After: "authorized"}
	current.Steps[0].SideEffect = &currentEffect
	current.Steps[0].Rules = []string{"authorize then charge"}
	baseline.Edges = []semantic.SemanticEdge{{FromStepID: "step-submit", ToStepID: "step-submit", Kind: "calls", ResolutionStatus: "resolved"}}
	current.Edges = []semantic.SemanticEdge{{FromStepID: "step-submit", ToStepID: "step-submit", Kind: "dispatches", ResolutionStatus: "resolved"}}
	delta, err := semantic.ComputeSemanticDelta("comparison-vs03-a20", baseline, current)
	if err != nil {
		t.Fatal(err)
	}
	facets := map[string]bool{}
	for _, change := range delta.Changes {
		if change.TargetStepID == "" {
			t.Fatalf("semantic change has no affected step: %+v", change)
		}
		for _, facet := range change.StructuralChanges {
			facets[facet] = true
		}
	}
	for _, required := range []string{"behavior changed", "branch changed", "state changed", "external effect changed", "call relation changed"} {
		if !facets[required] {
			t.Fatalf("verified Semantic Delta omitted %q: %+v", required, delta.Changes)
		}
	}
	previous := flowview.LogicalViewSelection{SelectedStepID: "step-old", SelectedStructuralIdentity: "service.go#Submit:entry", ScrollAnchor: &flowview.LogicalScrollAnchor{StepID: "step-old", StructuralIdentity: "service.go#Submit:entry", OffsetPx: 18}}
	preserved := flowview.PreserveLogicalViewSelection(previous, []flowview.ViewStepIdentity{{StepID: "step-new", StructuralIdentity: "service.go#Submit:entry"}})
	if !preserved.Preserved || preserved.SelectedStepID != "step-new" || preserved.ScrollAnchor == nil || preserved.ScrollAnchor.OffsetPx != 18 {
		t.Fatalf("compatible selection/read position was not preserved: %+v", preserved)
	}
	removed := flowview.PreserveLogicalViewSelection(previous, []flowview.ViewStepIdentity{{StepID: "different", StructuralIdentity: "different"}})
	if !removed.IdentityLoss || removed.Preserved {
		t.Fatalf("removed selected identity did not retain an explicit loss state: %+v", removed)
	}
	html := flowview.LiveSemanticHTML
	for _, required := range []string{"added_behavior", "changed_rule", "removed_behavior", "evidence_updated", "structural_only", "ambiguous_move", "data-delta-step", "읽기 고정 중이라 이전 흐름을 유지합니다", "선택한 단계가 새 흐름에서 제거되었습니다", "최신 변경을 확인하지 못했습니다. 이전 코드 표시 중", "연결 끊김 · 최신 변경을 확인할 수 없습니다"} {
		if !strings.Contains(html, required) {
			t.Fatalf("Live Change Pulse is missing %q", required)
		}
	}
	for _, forbidden := range []string{"analysisLagMs", "pendingRevisions", "workspaceEpoch"} {
		if strings.Contains(html, forbidden) {
			t.Fatalf("Live primary view exposes compiler telemetry %q", forbidden)
		}
	}
	return evidenceFor("VS03-A20", current.Basis.SnapshotTreeID, "delta:"+delta.ComparisonID, "selection:preserved", "identity-loss:explicit", "pulse:semantic-only")
}

type publicationFixtureData struct {
	input    semantic.PublicationInput
	snapshot *workspace.WorkspaceSnapshot
	tree     string
}

// canonicalVS02Evidence builds the same lossless request/result pair accepted
// at the VS-02 boundary. A04 passes both values into EvaluateCurrent so the
// current gate cannot claim authority from a hand-shaped semantic closure.
func canonicalVS02Evidence(t *testing.T, snapshot *workspace.WorkspaceSnapshot, engines ...*workspace.SnapshotEngine) (evidence.AnalyzerRequest, evidence.Result, *semantic.CausalObservationClosure) {
	t.Helper()
	if snapshot == nil {
		t.Fatal("canonical VS-02 evidence requires a captured snapshot")
	}
	var input evidence.SnapshotInput
	var err error
	if len(engines) > 0 && engines[0] != nil {
		lease, leaseErr := engines[0].SnapshotVFS(snapshot.SnapshotID)
		if leaseErr != nil {
			t.Fatalf("retain canonical VS-02 snapshot lease: %v", leaseErr)
		}
		protocolSnapshot, snapshotErr := protocol.SnapshotFromLease(lease)
		_ = lease.Close()
		if snapshotErr != nil {
			t.Fatalf("build canonical VS-02 snapshot from lease: %v", snapshotErr)
		}
		input, err = protocolSnapshot.AnalyzerInput()
	} else {
		input, err = (&protocol.Snapshot{
			SnapshotID:               snapshot.SnapshotID,
			ComputedBasisID:          snapshot.ComputedBasisID,
			WorkspaceEpoch:           snapshot.WorkspaceEpoch,
			RootTreeID:               snapshot.RootTreeID,
			ConfigurationFingerprint: snapshot.ConfigurationFingerprint,
			DependencyFingerprint:    snapshot.DependencyFingerprint,
			RepositoryID:             snapshot.RepositoryID,
			WorktreeID:               snapshot.WorktreeID,
			Files:                    map[string]string{},
			SourceWriteAudit:         snapshot.RepositoryPathWriteAudit,
		}).AnalyzerInput()
	}
	if err != nil {
		t.Fatalf("build canonical VS-02 snapshot input: %v", err)
	}
	request, err := evidence.NewAnalyzerRequest("request-vs03-a04", "slice", input, nil, []string{"negative_lookup"})
	if err != nil {
		t.Fatalf("build canonical VS-02 request: %v", err)
	}
	observation := evidence.Observation{Kind: "negative_lookup", Path: "service.go", ValueHash: "negative-vs03-a04", Measured: true}
	closureDigest := strings.Repeat("c", 64)
	readDocuments := make([]evidence.ReadDocument, 0, len(input.Documents))
	positiveDocumentRefs := make([]string, 0, len(input.Documents))
	for _, document := range input.Documents {
		readDocuments = append(readDocuments, evidence.ReadDocument{
			Path: document.Path, DocumentRevisionID: document.RevisionID, ContentID: document.ContentID,
			ContentHash: document.ContentID, DocumentVersion: document.DocumentVersion, ByteLength: document.ByteLength,
		})
		ref := document.Path
		if document.RevisionID != "" {
			ref += "@" + document.RevisionID
		}
		positiveDocumentRefs = append(positiveDocumentRefs, ref)
	}
	semanticNegativeObservations := []semantic.NegativeObservation{{
		Kind: observation.Kind, Selector: observation.Path, ScopeRef: observation.Path,
		ObservedAgainstIndexRevision: observation.ValueHash,
	}}
	result := evidence.Result{
		SchemaID: evidence.AnalyzerResultSchemaID, SchemaVersion: evidence.SchemaVersion,
		RequestID: request.RequestID, Operation: request.Operation,
		AdapterVersion: "adapter-vs03/2", AnalyzerRevision: "analyzer-vs03/2",
		WorkspaceEpoch: input.WorkspaceEpoch, ComputedBasisID: input.ComputedBasisID,
		SnapshotID: input.SnapshotID, SnapshotTreeDigest: input.RootTreeID,
		DependencyFingerprint: input.DependencyFingerprint,
		ReadSet: evidence.AnalysisReadSet{
			SchemaID: evidence.ReadSetSchemaID, SchemaVersion: evidence.SchemaVersion,
			ReadSetID: "readset-vs03-a04", ComputedBasisID: input.ComputedBasisID, WorkspaceEpoch: input.WorkspaceEpoch,
			Documents: readDocuments, NegativeObservations: []evidence.Observation{observation},
			MembershipObservations: []evidence.Observation{}, DependencyFrontiers: []evidence.Observation{},
		},
		Closure: evidence.ObservationClosure{
			SchemaID: evidence.ClosureSchemaID, SchemaVersion: evidence.SchemaVersion,
			ClosureID: "closure-vs03-a04", AnalysisReadSetID: "readset-vs03-a04", ComputedBasisID: input.ComputedBasisID,
			WorkspaceEpoch: input.WorkspaceEpoch, Status: "closed",
			NegativeObservations: []evidence.Observation{observation}, MembershipObservations: []evidence.Observation{}, DependencyFrontiers: []evidence.Observation{},
			RequiredObservations: []string{"negative_lookup"}, MeasuredObservations: []string{"negative_lookup"}, ClosureDigest: closureDigest,
		},
		Capability:  evidence.CapabilityProfile{Adapter: "adapter-vs03", AdapterVersion: "adapter-vs03/2", AnalyzerRevision: "analyzer-vs03/2", Features: []string{"snapshot_bytes"}},
		Coverage:    evidence.Coverage{IncludedSourceRoots: []string{"."}, Measured: true},
		Diagnostics: []evidence.Diagnostic{},
		Payload:     json.RawMessage(`{"candidateId":"cand-vs03a040","language":"go","entrySymbolPath":"service.go#Submit","steps":[{"ordinal":1,"kind":"call","description":"Submit","symbolPath":"Submit","anchor":{"repoRelativePath":"service.go","byteRange":[0,1],"fileHash":"0000000000000000000000000000000000000000000000000000000000000000","spanHash":"0000000000000000000000000000000000000000000000000000000000000000","enclosingSymbolPath":"Submit","canonicalAstFingerprint":"0000000000000000000000000000000000000000000000000000000000000000"}}],"edges":[],"truncated":false,"visitedCycleDetected":false,"redactedCount":0}`),
	}
	resultBytes, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal canonical VS-02 analyzer result: %v", err)
	}
	if err := contractharness.Validate(evidence.AnalyzerResultSchemaID, resultBytes); err != nil {
		t.Fatalf("canonical VS-02 analyzer result schema: %v", err)
	}
	if err := evidence.ValidateResult(request, result); err != nil {
		t.Fatalf("canonical VS-02 analyzer result semantics: %v", err)
	}
	semanticClosure := &semantic.CausalObservationClosure{
		SchemaID: semantic.ObservationClosureSchemaID, SchemaVersion: semantic.SemanticSchemaVersion,
		ClosureID: result.Closure.ClosureID, ComputedBasisID: result.Closure.ComputedBasisID, WorkspaceEpoch: result.Closure.WorkspaceEpoch,
		TaskIntentRevision: 1, NormalizedQueryHash: vs03QueryHash,
		RequiredObservations: append([]string(nil), result.Closure.RequiredObservations...), MeasuredObservations: append([]string(nil), result.Closure.MeasuredObservations...),
		AnalysisReadSetID: result.Closure.AnalysisReadSetID, ClosureStatus: result.Closure.Status, IncompleteReasons: []string{}, ClosureDigest: closureDigest,
		PositiveDependencies: semantic.PositiveDependencies{DocumentRevisionRefs: positiveDocumentRefs, ConfigurationFingerprint: snapshot.ConfigurationFingerprint},
		NegativeObservations: semanticNegativeObservations, MembershipObservations: []semantic.MembershipObservation{}, DependencyFrontiers: []semantic.DependencyFrontier{},
		CapabilityProfile: &semantic.CapabilityProfile{Adapter: result.Capability.Adapter, Features: append([]string(nil), result.Capability.Features...)},
		CoverageBoundary:  &semantic.CoverageBoundary{IncludedSourceRoots: []string{"."}, ExcludedReasons: []string{}}, CanonicalResult: &result,
	}
	return request, result, semanticClosure
}

func publicationFixture(t *testing.T) publicationFixtureData {
	t.Helper()
	mapIR := candidateMap("basis-vs03", "snapshot-vs03", "generation-vs03", 1, "Q3")
	closure := &semantic.CausalObservationClosure{
		SchemaID: semantic.ObservationClosureSchemaID, SchemaVersion: semantic.SemanticSchemaVersion,
		ClosureID: "closure-vs03", ComputedBasisID: mapIR.ComputedBasisID, TaskIntentRevision: 1,
		NormalizedQueryHash: vs03QueryHash, AnalysisReadSetID: "readset-vs03", ClosureStatus: "closed",
		PositiveDependencies: semantic.PositiveDependencies{DocumentRevisionRefs: []string{"service.go@rev-vs03"}, ConfigurationFingerprint: vs03Config},
		NegativeObservations: []semantic.NegativeObservation{}, MembershipObservations: []semantic.MembershipObservation{}, DependencyFrontiers: []semantic.DependencyFrontier{}, IncompleteReasons: []string{},
	}
	closure.ClosureDigest, _ = semantic.ClosureDigest(*closure)
	snapshot := &workspace.WorkspaceSnapshot{SchemaID: "https://codeflow.local/schemas/rflsc.workspace-snapshot.v2.schema.json", SchemaVersion: 2, SnapshotID: "snapshot-vs03", WorkspaceEpoch: 1, Sequence: 1, LiveHead: true, ComputedBasisID: "basis-vs03", RootTreeID: "tree-vs03", DependencyFingerprint: "deps-vs03", ConfigurationFingerprint: vs03Config, RepositoryID: vs03RepositoryID, WorktreeID: vs03WorktreeID, Entries: map[string]workspace.SnapshotEntry{}, ChangedEntries: []workspace.SnapshotChange{}, RepositoryPathWriteAudit: workspace.SourceWriteAudit{CapturedSnapshotTreeDigest: "tree-vs03"}}
	intent := &semantic.TaskIntent{TaskID: vs03TaskID, Revision: 1, Mode: "feature", IntentStatus: "user_confirmed"}
	delta := &workspace.WorkspaceDelta{FromSnapshotID: snapshot.SnapshotID, ToSnapshotID: snapshot.SnapshotID, AddedPaths: []string{}, ModifiedPaths: []string{}, DeletedPaths: []string{}, ChangedPaths: []string{}}
	input := semantic.PublicationInput{Map: mapIR, Closure: closure, Delta: delta, CapturedSnapshot: snapshot, LiveHeadSnapshot: snapshot, Intent: intent, RepositoryID: vs03RepositoryID, WorktreeID: vs03WorktreeID, DependencyFingerprint: "deps-vs03", QueryHash: vs03QueryHash, ExpectedPreviousGenerationID: "generation-previous", GenerationID: mapIR.GenerationID, ArtifactDigests: map[string]string{"semanticMap": strings.Repeat("a", 64), "evidenceIndex": strings.Repeat("b", 64)}, Metrics: semantic.PublicationMetrics{LagMs: 27, PendingRevisions: 4, MeasuredAt: time.Unix(100, 0).UTC(), Activity: "editing"}}
	return publicationFixtureData{input: input, snapshot: snapshot, tree: snapshot.RootTreeID}
}

func candidateMap(basis, snapshot, generation string, revision int, stage string) *semantic.SemanticMapIR {
	anchor := slicing.Anchor{RepoRelativePath: "service.go", EnclosingSymbolPath: "service.go#Submit", ByteRange: [2]int{0, 10}, FileHash: strings.Repeat("0", 64), SpanHash: strings.Repeat("1", 64), CanonicalAstFingerprint: strings.Repeat("2", 64)}
	return &semantic.SemanticMapIR{SchemaID: semantic.SemanticMapSchemaID, SchemaVersion: semantic.SemanticSchemaVersion, MapID: "map-vs03-" + generation, GenerationID: generation, ComputedBasisID: basis, ValidatedAgainstSnapshotID: snapshot, GenerationSequence: revision, PublicationKind: "checkpoint", Freshness: "historical", Settlement: "pending", EnrichmentStatus: "available", Quality: semantic.MapQuality{Stage: stage, CriticalObligations: []semantic.CriticalObligation{}}, Task: semantic.MapTaskContext{TaskID: vs03TaskID, IntentRevision: revision, IntentStatus: "user_confirmed", Mode: "feature"}, Basis: semantic.MapBasisContext{RepositoryID: vs03RepositoryID, WorktreeID: vs03WorktreeID, WorkspaceEpoch: 1, ComputedWorkspaceSnapshotID: snapshot, ComputedBasisID: basis, SnapshotTreeID: "tree-vs03", DependencyFingerprint: "deps-vs03", ConfigurationFingerprint: vs03Config, AnalysisReadSetID: "readset-vs03", CausalObservationClosureID: "closure-vs03"}, Summary: semantic.MapSummary{Requested: "requested submit flow", Current: "candidate submit flow"}, Steps: []semantic.SemanticStep{{StepID: "step-submit", StructuralIdentity: "service.go#Submit:entry", Ordinal: 1, Name: "Submit", TechnicalName: "service.go#Submit", Kind: "user_action", Anchor: anchor, EvidenceRefs: []string{"evidence-submit"}}}, Edges: []semantic.SemanticEdge{}, Evidence: []semantic.SemanticEvidence{{EvidenceID: "evidence-submit", Kind: "source", SourceAuthority: "code", ComputedBasisID: basis, SnapshotID: snapshot, ValidationStatus: "verified", Anchor: anchor, ByteRange: [2]int{0, 10}, LineRange: [2]int{1, 1}}}, Unknowns: []fusion.Unknown{}, Coverage: &semantic.CoverageBoundary{IncludedSourceRoots: []string{"."}, ExcludedReasons: []string{}}, Authority: "candidate"}
}

// newPublicationAuthority creates the same immutable workspace head that a
// publication transaction claims. The validator passed to storage reads the
// engine's protected LiveHeadID at commit time, so a caller cannot turn a
// captured string into authority after the head has advanced.
func newPublicationAuthority(t *testing.T, root string, identity workspace.WorkspaceIdentity, bootstrapPath string, bootstrapContent []byte) (*workspace.SnapshotEngine, *workspace.WorkspaceSnapshot) {
	t.Helper()
	engine, err := workspace.NewSnapshotEngine(root, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.BindWorkspaceIdentity(identity); err != nil {
		t.Fatal(err)
	}
	_, snapshot, err := engine.ApplyVersionedEdit(context.Background(), workspace.EditRequest{Path: bootstrapPath, Content: bootstrapContent, DocumentVersion: 1, Source: workspace.SourceIDEVersioned})
	if err != nil {
		t.Fatal(err)
	}
	return engine, snapshot
}

func newPublicationAuthorityFromExisting(t *testing.T, root string) (*workspace.SnapshotEngine, *workspace.WorkspaceSnapshot) {
	t.Helper()
	engine, err := workspace.NewSnapshotEngine(root, 0)
	if err != nil {
		t.Fatal(err)
	}
	head := engine.LiveHead()
	if head == nil {
		t.Fatal("restarted workspace has no authoritative live head")
	}
	return engine, head
}

func bindAuthoritativeHead(tx *storage.PublicationTransaction, engine *workspace.SnapshotEngine, capturedSnapshotID string) {
	tx.ExpectedLiveHeadSnapshotID = capturedSnapshotID
	// This is the captured observation. The closure below is the authoritative
	// read used by PublishGeneration while its publication lock is held.
	tx.ActualLiveHeadSnapshotID = capturedSnapshotID
	tx.LiveHeadCommit = func(expectedID string, commit func() error) error {
		if engine == nil {
			return storage.ErrCASConflict
		}
		return engine.WithLiveHead(expectedID, commit)
	}
}

func defaultLiveIdentityForTest(root string) workspace.WorkspaceIdentity {
	abs, _ := filepath.Abs(root)
	digest := sha256.Sum256([]byte(abs))
	hexDigest := hex.EncodeToString(digest[:])
	return workspace.WorkspaceIdentity{RepositoryID: "repo-" + hexDigest[:24], WorktreeID: "worktree-" + hexDigest[24:], ConfigurationFingerprint: "config-" + hexDigest[:32]}
}

func publicationTransaction(generation, snapshot, basis string, previous *string, includeEvent bool) storage.PublicationTransaction {
	prev := previous
	mapIR := candidateMap(basis, snapshot, generation, 1, "Q3")
	semanticMapBytes, _ := json.Marshal(mapIR)
	semanticMapRef := storage.ArtifactCASRef(semanticMapBytes)
	artifactBytes, artifactRefs, capabilityDigest := canonicalProofArtifactBundle(generation, snapshot, basis, mapIR)
	artifactRefs.SemanticMap = semanticMapRef
	manifest := &storage.GenerationProofManifest{SchemaID: "https://codeflow.local/schemas/rflsc.generation-proof-manifest.v2.schema.json", SchemaVersion: 2, ProofID: "proof-" + generation, GenerationID: generation, ComputedBasisID: basis, ComputedSnapshotID: snapshot, ValidatedAgainstSnapshotID: snapshot, TaskIntentRevision: 1, NormalizedQueryHash: vs03QueryHash, AnalysisReadSetID: "readset-vs03", CausalObservationClosureID: "closure-vs03", CausalObservationClosureDigest: strings.Repeat("c", 64), CapabilityProfileDigest: capabilityDigest, WorkspaceEpoch: 1, CurrentPublication: storage.CurrentPublicationResult{Eligibility: "passed", SnapshotGate: "passed", ClosureGate: "passed", EvidenceGate: "passed", SemanticAtomicityGate: "passed", TaskRelevanceGate: "passed", ComprehensionGate: "passed"}, SettlementEvaluation: storage.SettlementEvaluation{Gate: "pending", BlockingObligationRefs: []string{}}, ArtifactRefs: artifactRefs, ExpectedLiveHeadSnapshotID: snapshot, ExpectedPreviousGenerationID: prev, PublishedAt: time.Unix(1, 0).UTC()}
	ptr := &storage.ActivePointer{SchemaID: "https://codeflow.local/schemas/rflsc.active-pointer.v2.schema.json", SchemaVersion: 2, GenerationID: generation, ComputedBasisID: basis, ValidatedAgainstSnapshotID: snapshot, ExpectedLiveHeadSnapshotID: snapshot, ExpectedPreviousGenerationID: prev, WorkspaceEpoch: 1, TaskIntentRevision: 1, NormalizedQueryHash: vs03QueryHash, FlowCount: 1, RepositoryID: vs03RepositoryID, WorktreeID: vs03WorktreeID, TaskID: vs03TaskID, PublishedAt: time.Unix(1, 0).UTC()}
	artifacts := map[string][]byte{semanticMapRef: semanticMapBytes}
	for ref, data := range artifactBytes {
		artifacts[ref] = data
	}
	tx := storage.PublicationTransaction{Manifest: manifest, Pointer: ptr, Artifacts: artifacts, ExpectedLiveHeadSnapshotID: snapshot, ActualLiveHeadSnapshotID: snapshot}
	if includeEvent {
		tx.Event, _ = json.Marshal(map[string]any{"schemaId": "https://codeflow.local/schemas/rflsc.event-envelope.v2.schema.json", "schemaVersion": 2, "streamId": "stream-vs03", "sequence": 1, "eventId": "event-1", "eventType": "generation.published", "occurredAt": time.Unix(1, 0).UTC(), "computedBasisId": basis, "validatedAgainstSnapshotId": snapshot, "generationId": generation})
	}
	return tx
}

// canonicalProofArtifactBundle creates the complete v2 analysis bundle used
// by every publication fixture. A manifest that only points at its semantic
// map is not a current proof, so even the generic transaction helper carries
// read-set, closure, analyzer-result, and projection objects with matching
// identities and content-addressed references.
func canonicalProofArtifactBundle(generation, snapshot, basis string, mapIR *semantic.SemanticMapIR) (map[string][]byte, storage.ArtifactRefs, string) {
	if mapIR == nil {
		panic("canonical proof artifacts require a semantic map")
	}
	observation := evidence.Observation{Kind: "negative_lookup", Path: "service.go", ValueHash: "negative-vs03", Measured: true}
	readSet := evidence.AnalysisReadSet{
		SchemaID: evidence.ReadSetSchemaID, SchemaVersion: evidence.SchemaVersion,
		ReadSetID: "readset-vs03", ComputedBasisID: basis, WorkspaceEpoch: mapIR.Basis.WorkspaceEpoch,
		Documents: []evidence.ReadDocument{}, NegativeObservations: []evidence.Observation{observation},
		MembershipObservations: []evidence.Observation{}, DependencyFrontiers: []evidence.Observation{},
	}
	closure := evidence.ObservationClosure{
		SchemaID: evidence.ClosureSchemaID, SchemaVersion: evidence.SchemaVersion,
		ClosureID: "closure-vs03", AnalysisReadSetID: readSet.ReadSetID, ComputedBasisID: basis, WorkspaceEpoch: mapIR.Basis.WorkspaceEpoch,
		Status: "closed", NegativeObservations: []evidence.Observation{observation},
		MembershipObservations: []evidence.Observation{}, DependencyFrontiers: []evidence.Observation{},
		RequiredObservations: []string{"negative_lookup"}, MeasuredObservations: []string{"negative_lookup"},
		ClosureDigest: strings.Repeat("c", 64),
	}
	result := evidence.Result{
		SchemaID: evidence.AnalyzerResultSchemaID, SchemaVersion: evidence.SchemaVersion,
		RequestID: "request-vs03-" + generation, Operation: "detect", AdapterVersion: "adapter-vs03/2", AnalyzerRevision: "analyzer-vs03/2",
		WorkspaceEpoch: mapIR.Basis.WorkspaceEpoch, ComputedBasisID: basis, SnapshotID: snapshot, SnapshotTreeDigest: mapIR.Basis.SnapshotTreeID,
		DependencyFingerprint: mapIR.Basis.DependencyFingerprint, ReadSet: readSet, Closure: closure,
		Capability: evidence.CapabilityProfile{Adapter: "adapter-vs03", AdapterVersion: "adapter-vs03/2", AnalyzerRevision: "analyzer-vs03/2", Features: []string{"snapshot_bytes"}},
		Coverage:   evidence.Coverage{IncludedSourceRoots: []string{"."}, Measured: true}, Diagnostics: []evidence.Diagnostic{},
		Payload: json.RawMessage(`{"language":"go","confident":true}`),
	}
	projection := semantic.BuildFlowViewProjection(mapIR)
	return canonicalProofArtifactsFromResult(&result, projection)
}

func canonicalProofArtifactsFromResult(result *evidence.Result, projection *semantic.FlowViewProjection) (map[string][]byte, storage.ArtifactRefs, string) {
	if result == nil || projection == nil {
		panic("canonical proof artifacts require analyzer result and projection")
	}
	readSetBytes, err := json.Marshal(result.ReadSet)
	if err != nil {
		panic(err)
	}
	closureBytes, err := json.Marshal(result.Closure)
	if err != nil {
		panic(err)
	}
	resultBytes, err := json.Marshal(result)
	if err != nil {
		panic(err)
	}
	projectionBytes, err := json.Marshal(projection)
	if err != nil {
		panic(err)
	}
	readSetRef := storage.ArtifactCASRef(readSetBytes)
	closureRef := storage.ArtifactCASRef(closureBytes)
	resultRef := storage.ArtifactCASRef(resultBytes)
	projectionRef := storage.ArtifactCASRef(projectionBytes)
	capabilityDigest, err := semantic.CanonicalCapabilityProfileDigest(result.Capability)
	if err != nil {
		panic(err)
	}
	return map[string][]byte{
		readSetRef: readSetBytes, closureRef: closureBytes, resultRef: resultBytes, projectionRef: projectionBytes,
	}, storage.ArtifactRefs{Projection: projectionRef, AnalysisReadSet: readSetRef, ObservationClosure: closureRef, AnalyzerResult: resultRef}, capabilityDigest
}

// replaceCanonicalProofArtifacts swaps the complete bundle after a runner
// has attached the real VS-02 request/result/closure. This keeps A07/A10
// bound to measured evidence rather than the generic fixture bundle.
func replaceCanonicalProofArtifacts(tx *storage.PublicationTransaction, request *evidence.AnalyzerRequest, result *evidence.Result, closure *semantic.CausalObservationClosure, mapIR *semantic.SemanticMapIR) {
	if tx == nil || tx.Manifest == nil || result == nil || closure == nil || mapIR == nil {
		panic("canonical proof artifact replacement requires transaction, evidence, and map")
	}
	if request != nil && request.RequestID != result.RequestID {
		panic("canonical proof request/result identity mismatch")
	}
	projection := semantic.BuildFlowViewProjection(mapIR)
	artifactBytes, refs, capabilityDigest := canonicalProofArtifactsFromResult(result, projection)
	for _, ref := range []string{tx.Manifest.ArtifactRefs.Projection, tx.Manifest.ArtifactRefs.AnalysisReadSet, tx.Manifest.ArtifactRefs.ObservationClosure, tx.Manifest.ArtifactRefs.AnalyzerResult} {
		if ref != "" {
			delete(tx.Artifacts, ref)
		}
	}
	for ref, data := range artifactBytes {
		tx.Artifacts[ref] = data
	}
	tx.Manifest.ArtifactRefs.Projection = refs.Projection
	tx.Manifest.ArtifactRefs.AnalysisReadSet = refs.AnalysisReadSet
	tx.Manifest.ArtifactRefs.ObservationClosure = refs.ObservationClosure
	tx.Manifest.ArtifactRefs.AnalyzerResult = refs.AnalyzerResult
	tx.Manifest.AnalysisReadSetID = result.ReadSet.ReadSetID
	tx.Manifest.CausalObservationClosureID = result.Closure.ClosureID
	tx.Manifest.CausalObservationClosureDigest = result.Closure.ClosureDigest
	tx.Manifest.CapabilityProfileDigest = capabilityDigest
}

func replaceSemanticMapArtifact(tx *storage.PublicationTransaction, mapIR *semantic.SemanticMapIR) {
	if tx == nil || tx.Manifest == nil || mapIR == nil {
		panic("publication semantic map replacement requires transaction, manifest, and map")
	}
	data, err := json.Marshal(mapIR)
	if err != nil {
		panic(err)
	}
	if tx.Artifacts == nil {
		tx.Artifacts = make(map[string][]byte)
	}
	delete(tx.Artifacts, tx.Manifest.ArtifactRefs.SemanticMap)
	ref := storage.ArtifactCASRef(data)
	tx.Manifest.ArtifactRefs.SemanticMap = ref
	tx.Artifacts[ref] = data
	// The strict restart reader validates the complete artifact graph, not only
	// the map bytes. Rebuild the fixture bundle whenever the map is replaced so
	// its projection/result identities (tree, dependency, generation, basis)
	// cannot describe the old map.
	for _, oldRef := range []string{tx.Manifest.ArtifactRefs.Projection, tx.Manifest.ArtifactRefs.AnalysisReadSet, tx.Manifest.ArtifactRefs.ObservationClosure, tx.Manifest.ArtifactRefs.AnalyzerResult} {
		if oldRef != "" {
			delete(tx.Artifacts, oldRef)
		}
	}
	bundle, refs, capabilityDigest := canonicalProofArtifactBundle(mapIR.GenerationID, mapIR.ValidatedAgainstSnapshotID, mapIR.ComputedBasisID, mapIR)
	for bundleRef, bundleData := range bundle {
		tx.Artifacts[bundleRef] = bundleData
	}
	tx.Manifest.ArtifactRefs.Projection = refs.Projection
	tx.Manifest.ArtifactRefs.AnalysisReadSet = refs.AnalysisReadSet
	tx.Manifest.ArtifactRefs.ObservationClosure = refs.ObservationClosure
	tx.Manifest.ArtifactRefs.AnalyzerResult = refs.AnalyzerResult
	tx.Manifest.AnalysisReadSetID = "readset-vs03"
	tx.Manifest.CausalObservationClosureID = "closure-vs03"
	tx.Manifest.CausalObservationClosureDigest = strings.Repeat("c", 64)
	tx.Manifest.CapabilityProfileDigest = capabilityDigest
}

func alignRefinementEvidence(_ *evidence.AnalyzerRequest, result *evidence.Result, closure *semantic.CausalObservationClosure, snapshot *workspace.WorkspaceSnapshot, readSetID, closureID string) {
	if result == nil || closure == nil || snapshot == nil {
		panic("refinement evidence alignment requires result, closure, and snapshot")
	}
	result.ReadSet.ReadSetID = readSetID
	result.Closure.ClosureID = closureID
	result.Closure.AnalysisReadSetID = readSetID
	closure.ClosureID = closureID
	closure.AnalysisReadSetID = readSetID
	closure.ComputedBasisID = snapshot.ComputedBasisID
	closure.WorkspaceEpoch = snapshot.WorkspaceEpoch
	closure.TaskIntentRevision = 1
	closure.NormalizedQueryHash = vs03QueryHash
	closure.CanonicalResult = result
}

func generationPublishedEvent(sequence int, basis, snapshot, generation string) []byte {
	data, err := json.Marshal(map[string]any{
		"schemaId":                   semantic.EventEnvelopeSchemaID,
		"schemaVersion":              semantic.SemanticSchemaVersion,
		"streamId":                   "stream-vs03",
		"sequence":                   sequence,
		"eventId":                    fmt.Sprintf("event-%d", sequence),
		"eventType":                  "generation.published",
		"occurredAt":                 time.Now().UTC(),
		"computedBasisId":            basis,
		"validatedAgainstSnapshotId": snapshot,
		"generationId":               generation,
	})
	if err != nil {
		panic(err)
	}
	return data
}

// vs03Q3DigestTuple mirrors the production coordinator's four immutable Q3
// slices. It is intentionally local to the acceptance runner so the test
// compares the persisted late map with the original map without importing an
// unexported production helper.
func vs03Q3DigestTuple(m *semantic.SemanticMapIR) [4]string {
	digest := func(value any) string {
		data, err := json.Marshal(value)
		if err != nil {
			panic(err)
		}
		sum := sha256.Sum256(data)
		return hex.EncodeToString(sum[:])
	}
	if m == nil {
		return [4]string{}
	}
	return [4]string{
		digest(struct {
			Steps    []semantic.SemanticStep     `json:"steps"`
			Edges    []semantic.SemanticEdge     `json:"edges"`
			Evidence []semantic.SemanticEvidence `json:"evidence"`
		}{m.Steps, m.Edges, m.Evidence}),
		digest(m.RequirementAlignment),
		digest(struct {
			Obligations []semantic.CriticalObligation `json:"obligations"`
			Unresolved  int                           `json:"unresolved"`
			Conflicting int                           `json:"conflicting"`
		}{m.Quality.CriticalObligations, m.Quality.UnresolvedCriticalCount, m.Quality.ConflictingCriticalCount}),
		digest(m.Settlement),
	}
}

func cloneManifest(in *storage.GenerationProofManifest) *storage.GenerationProofManifest {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}

func clonePointer(in *storage.ActivePointer) *storage.ActivePointer {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}

func durableEventIDs(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0)
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var envelope struct {
			EventID string `json:"eventId"`
		}
		if err := json.Unmarshal([]byte(line), &envelope); err != nil {
			return nil, err
		}
		if envelope.EventID == "" {
			return nil, fmt.Errorf("durable event has empty eventId")
		}
		ids = append(ids, envelope.EventID)
	}
	return ids, nil
}

func durableEventEnvelopes(path string) ([]*semantic.EventEnvelope, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	envelopes := make([]*semantic.EventEnvelope, 0)
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var envelope semantic.EventEnvelope
		if err := json.Unmarshal([]byte(line), &envelope); err != nil {
			return nil, err
		}
		envelopes = append(envelopes, &envelope)
	}
	return envelopes, nil
}

func mustMarshal(value any) []byte {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return data
}

func vs03StringFromMap(values map[string]any, key string) string {
	value, _ := values[key].(string)
	return value
}

func waitForDurableEventType(path, eventType string, timeout time.Duration, minimum int) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil {
			count := 0
			for _, line := range strings.Split(string(data), "\n") {
				if strings.TrimSpace(line) == "" {
					continue
				}
				var envelope struct {
					EventType string `json:"eventType"`
				}
				if json.Unmarshal([]byte(line), &envelope) == nil && envelope.EventType == eventType {
					count++
				}
			}
			if count >= minimum {
				return nil
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	return fmt.Errorf("durable event type %q did not reach count %d", eventType, minimum)
}

func parseSSEEnvelope(block string) (*semantic.EventEnvelope, error) {
	for _, line := range strings.Split(block, "\n") {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var envelope semantic.EventEnvelope
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &envelope); err != nil {
			return nil, err
		}
		return &envelope, nil
	}
	return nil, fmt.Errorf("SSE block has no data envelope")
}

func readSSEBlock(reader *bufio.Reader) (string, error) {
	if reader == nil {
		return "", fmt.Errorf("SSE reader is nil")
	}
	var block strings.Builder
	for {
		line, err := reader.ReadString('\n')
		if len(line) != 0 {
			block.WriteString(line)
			if line == "\n" {
				return block.String(), nil
			}
		}
		if err != nil {
			return block.String(), fmt.Errorf("read SSE block: %w", err)
		}
	}
}

func p95(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	index := int(float64(len(sorted)-1) * 0.95)
	return sorted[index]
}

// Keep the file's helper imports and production digest types anchored even as
// production packages evolve their concrete representations.
