package analyzer

// This file contains the executable behavior shared by the named VS-02
// implementation tests and the contract evidence registry. Keeping the
// checks here prevents the registry from becoming a second, weaker test
// implementation.

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codeflow/internal/analyzer/workspace"
	"codeflow/internal/collector/evidence"
)

// Evidence identifies the implementation behavior and the process that ran
// it. Registry callers fill the same shape as named package tests.
type Evidence struct {
	Criterion                string
	ImplementationTestID     string
	ImplementationPackage    string
	ExecutionPackage         string
	ExecutionBinary          string
	ExecutionID              string
	SnapshotTreeDigest       string
	RepositoryPathWriteAudit workspace.SourceWriteAudit
	MountPermissionEvidence  evidence.MountPermissionEvidence
	ObjectRefs               []string
}

var implementationTestIDs = map[string]string{
	"VS02-A1":  "codeflow/internal/collector/verification/analyzer.TestRFLSCR2VS02_A01",
	"VS02-A2":  "codeflow/internal/collector/verification/analyzer.TestRFLSCR2VS02_A02",
	"VS02-A3":  "codeflow/internal/collector/verification/analyzer.TestRFLSCR2VS02_A03",
	"VS02-A4":  "codeflow/internal/collector/verification/analyzer.TestRFLSCR2VS02_A04",
	"VS02-A5":  "codeflow/internal/collector/verification/analyzer.TestRFLSCR2VS02_A05",
	"VS02-A6":  "codeflow/internal/collector/verification/analyzer.TestRFLSCR2VS02_A06",
	"VS02-A7":  "codeflow/internal/collector/verification/analyzer.TestRFLSCR2VS02_A07",
	"VS02-A8":  "codeflow/internal/collector/verification/analyzer.TestRFLSCR2VS02_A08",
	"VS02-A9":  "codeflow/internal/collector/verification/analyzer.TestRFLSCR2VS02_A09",
	"VS02-A10": "codeflow/internal/collector/verification/analyzer.TestRFLSCR2VS02_A10",
	"VS02-A11": "codeflow/internal/collector/verification/analyzer.TestRFLSCR2VS02_A11",
}

func runnerLease(t *testing.T, disk, selected string) (workspace.SnapshotLease, string) {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, "lib", "feature.go")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(disk), 0o644); err != nil {
		t.Fatal(err)
	}
	engine, err := workspace.NewSnapshotEngine(root, 7)
	if err != nil {
		t.Fatal(err)
	}
	_, snapshot, err := engine.ApplyVersionedEdit(context.Background(), workspace.EditRequest{
		Path: "lib/feature.go", Content: []byte(selected), DocumentVersion: 1,
		Source: workspace.SourceIDEVersioned,
	})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := engine.SnapshotVFS(snapshot.SnapshotID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lease.Close() })
	return lease, root
}

func runnerInput(t *testing.T) (evidence.SnapshotInput, workspace.SourceWriteAudit) {
	t.Helper()
	lease, _ := runnerLease(t, "package disk\n", "package snapshot\nfunc Run() {}\n")
	input, err := evidence.SnapshotInputFromLease(lease)
	if err != nil {
		t.Fatal(err)
	}
	return input, input.SourceWriteAudit
}

func runnerEvidence(t *testing.T, criterion string, input evidence.SnapshotInput) Evidence {
	t.Helper()
	return Evidence{
		Criterion:                criterion,
		ImplementationTestID:     implementationTestIDs[criterion],
		SnapshotTreeDigest:       input.RootTreeID,
		RepositoryPathWriteAudit: input.SourceWriteAudit,
		MountPermissionEvidence: evidence.MountPermissionEvidence{
			SourceDelivery:                 "protocol_snapshot_bytes",
			SourceMount:                    "not_mounted",
			WorkingDirectoryMode:           "not_applicable",
			WorkingDirectoryPermission:     "not_applicable",
			ReadOnlySource:                 true,
			Disposable:                     false,
			RepositoryPathExposed:          false,
			DependencyEnvironmentPreserved: false,
			CleanupVerified:                false,
		},
		ObjectRefs: []string{"snapshot:" + input.SnapshotID, "tree:" + input.RootTreeID, "audit:" + input.SourceWriteAudit.CapturedSnapshotTreeDigest},
	}
}

func runnerRequest(t *testing.T, input evidence.SnapshotInput, required ...string) evidence.AnalyzerRequest {
	t.Helper()
	request, err := evidence.NewAnalyzerRequest("runner-"+t.Name(), "slice", input, []string{"lib/feature.go"}, required)
	if err != nil {
		t.Fatal(err)
	}
	return request
}

func runnerReadDocuments(input evidence.SnapshotInput) []evidence.ReadDocument {
	documents := make([]evidence.ReadDocument, 0, len(input.Documents))
	for _, doc := range input.Documents {
		documents = append(documents, evidence.ReadDocument{
			Path: doc.Path, DocumentRevisionID: doc.RevisionID, ContentID: doc.ContentID,
			ContentHash: doc.ContentID, DocumentVersion: doc.DocumentVersion, ByteLength: doc.ByteLength,
		})
	}
	return documents
}

func measuredObservation(kind string) evidence.Observation {
	return evidence.Observation{Kind: kind, Path: "lib/feature.go", ValueHash: "measured-" + kind, Measured: true}
}

func runnerValidResult(t *testing.T, request evidence.AnalyzerRequest) evidence.Result {
	t.Helper()
	readSet := evidence.AnalysisReadSet{
		SchemaID: evidence.ReadSetSchemaID, SchemaVersion: evidence.SchemaVersion, ReadSetID: "runner-read-set",
		ComputedBasisID: request.Snapshot.ComputedBasisID, WorkspaceEpoch: request.Snapshot.WorkspaceEpoch,
		Documents: runnerReadDocuments(request.Snapshot),
	}
	closure := evidence.ObservationClosure{
		SchemaID: evidence.ClosureSchemaID, SchemaVersion: evidence.SchemaVersion, ClosureID: "runner-closure",
		AnalysisReadSetID: readSet.ReadSetID, ComputedBasisID: request.Snapshot.ComputedBasisID,
		WorkspaceEpoch: request.Snapshot.WorkspaceEpoch, Status: "open",
		NegativeObservations: []evidence.Observation{}, MembershipObservations: []evidence.Observation{}, DependencyFrontiers: []evidence.Observation{},
		RequiredObservations: append([]string{}, request.RequiredObservations...), MeasuredObservations: []string{}, IncompleteReasons: []string{"scope is bounded"},
	}
	return evidence.Result{
		SchemaID: evidence.AnalyzerResultSchemaID, SchemaVersion: evidence.SchemaVersion, RequestID: request.RequestID,
		Operation: request.Operation, AdapterVersion: "runner-adapter/2", AnalyzerRevision: "runner-analyzer/2",
		WorkspaceEpoch: request.Snapshot.WorkspaceEpoch, ComputedBasisID: request.Snapshot.ComputedBasisID,
		SnapshotID: request.Snapshot.SnapshotID, SnapshotTreeDigest: request.Snapshot.RootTreeID,
		DependencyFingerprint: request.Snapshot.DependencyFingerprint, ReadSet: readSet, Closure: closure,
		Capability: evidence.CapabilityProfile{Adapter: "runner", AdapterVersion: "2", AnalyzerRevision: "runner-analyzer/2", Features: []string{"snapshot_bytes"}},
		Coverage:   evidence.Coverage{IncludedSourceRoots: []string{"lib"}, Measured: true},
		Payload:    json.RawMessage(`{"candidateId":"cand-runner0001","language":"go","entrySymbolPath":"lib/feature.go#Run","steps":[{"ordinal":1,"kind":"call","description":"runner","symbolPath":"Run","anchor":{"repoRelativePath":"lib/feature.go","byteRange":[0,1],"fileHash":"0000000000000000000000000000000000000000000000000000000000000000","spanHash":"0000000000000000000000000000000000000000000000000000000000000000","enclosingSymbolPath":"Run","canonicalAstFingerprint":"0000000000000000000000000000000000000000000000000000000000000000"}}],"edges":[],"truncated":false,"visitedCycleDetected":false,"redactedCount":0}`),
	}
}

// RunA01 proves that the adapter envelope is sourced from the immutable lease
// and has no repository-root live-disk dependency.
func RunA01(t *testing.T) Evidence {
	t.Helper()
	input, _ := runnerInput(t)
	doc, ok := input.Document("lib/feature.go")
	if !ok || string(doc.Bytes) != "package snapshot\nfunc Run() {}\n" {
		t.Fatalf("snapshot bytes missing from input: %+v", doc)
	}
	request := runnerRequest(t, input)
	if _, ok := request.Params()["repoRoot"]; ok {
		t.Fatal("analyzer request must not expose repoRoot")
	}
	if _, ok := request.Params()["snapshot"].(map[string]any)["files"]; !ok {
		t.Fatal("analyzer request must carry snapshot files")
	}
	return runnerEvidence(t, "VS02-A1", input)
}

// RunA02 proves that all returned documents are measured against snapshot
// identities and that positive observation data survives validation.
func RunA02(t *testing.T) Evidence {
	t.Helper()
	input, _ := runnerInput(t)
	// Exercise the real adapter source abstractions. A constructed evidence.Result cannot
	// prove that a read, miss, membership enumeration, or frontier was measured.
	runnerAdapterObservationTests(t)
	return runnerEvidence(t, "VS02-A2", input)
}

// RunA03 proves that a closed result cannot claim unmeasured required facts.
func RunA03(t *testing.T) Evidence {
	t.Helper()
	input, _ := runnerInput(t)
	// The same production adapter tests include required-observation gaps and
	// assert that every unsupported or unmeasured requirement remains open.
	runnerAdapterObservationTests(t)
	return runnerEvidence(t, "VS02-A3", input)
}

// RunA04 proves cross-artifact identity checks at the promotion gate.
func RunA04(t *testing.T) Evidence {
	t.Helper()
	input, _ := runnerInput(t)
	request := runnerRequest(t, input)
	result := runnerValidResult(t, request)
	result.ReadSet.ComputedBasisID = "other-basis"
	if err := evidence.ValidateResult(request, result); err == nil {
		t.Fatal("cross-basis read set was accepted")
	}
	return runnerEvidence(t, "VS02-A4", input)
}

// RunA05 proves exact evidence extraction from selected snapshot bytes.
func RunA05(t *testing.T) Evidence {
	t.Helper()
	input, _ := runnerInput(t)
	doc, _ := input.Document("lib/feature.go")
	if _, err := evidence.ExtractEvidence(input, []evidence.EvidenceAnchor{{EvidenceID: "runner-evidence", Path: doc.Path, RevisionID: doc.RevisionID, StartByte: 0, EndByte: len(doc.Bytes)}}); err != nil {
		t.Fatalf("snapshot evidence rejected: %v", err)
	}
	return runnerEvidence(t, "VS02-A5", input)
}

// RunA06 proves missing revisions and out-of-bounds ranges fail closed with
// typed evidence errors.
func RunA06(t *testing.T) Evidence {
	t.Helper()
	input, _ := runnerInput(t)
	_, err := evidence.ExtractEvidence(input, []evidence.EvidenceAnchor{{EvidenceID: "runner-invalid", Path: "lib/feature.go", RevisionID: "wrong", StartByte: 0, EndByte: 1}})
	var typed *evidence.EvidenceError
	if err == nil || !errors.As(err, &typed) {
		t.Fatalf("invalid evidence did not return typed failure: %v", err)
	}
	return runnerEvidence(t, "VS02-A6", input)
}

// RunA07 executes the Core and all adapter diagnostic redaction seams. The
// subprocess-backed checks exercise malformed input, nested structured data,
// split stderr, and the bounded egress paths before this evidence is emitted.
func RunA07(t *testing.T) Evidence {
	t.Helper()
	input, _ := runnerInput(t)
	runnerGoTest(t, "./internal/collector/secret", "TestRedact(MalformedJSONQuotedKeyBeforeClip|RedactAndClipNestedDiagnosticsBeforeBound|RedactAndClipPointerStructuredDiagnostics)")
	runnerGoTest(t, "./internal/analyzer/protocol", "TestProtocol(ReadLoopRedactsMalformedJSONBeforeDiagnosticClip|StderrTailRedactsAcrossChunksAndTailCutoff|StderrTailDoesNotFinalizeLookbehindBetweenChunks|StderrEOFFinalizesSafeLookbehindButDropsSecret|StderrRedactsLongCredentialKeySplitBeyondLookbehind)|TestRPCErrorStructuredDataRedactsBeforeDecodeAndClip")
	runnerGoTest(t, "./adapters/go", "^TestProductionDiagnosticRedactionPath$")
	runnerCommand(t, "node", "TypeScript adapter diagnostic redaction", "test/secret_redaction.test.js", "adapters/typescript")
	runnerCommand(t, "dart", "Dart adapter diagnostic redaction", "test", "test/protocol_test.dart", "--plain-name", "production RPC diagnostics redact malformed quoted keys before clipping", "adapters/dart")
	return runnerEvidence(t, "VS02-A7", input)
}

// RunA08 executes the real subprocess lifecycle conformance probes. The
// protocol package owns the process and pending-call assertions, so this
// shared runner invokes those executable checks instead of replacing them
// with a context-only simulation.
func RunA08(t *testing.T) Evidence {
	t.Helper()
	input, _ := runnerInput(t)
	runnerGoTest(t, "./internal/analyzer/protocol", "TestMockAdapterConformance/(Timeout|CancelMidCall|CrashRestartOnce)")
	runnerGoTest(t, "./internal/analyzer/protocol", "^TestSourceReadOnlyAdapterLifecycle$")
	return runnerEvidence(t, "VS02-A8", input)
}

// RunA09 executes the negotiated bound at the Core and each adapter's real
// response writer, including exact-boundary and oversized fallbacks.
func RunA09(t *testing.T) Evidence {
	t.Helper()
	input, _ := runnerInput(t)
	runnerGoTest(t, "./internal/analyzer/protocol", "^TestOutbound(FrameBoundExactAndOversize|NotificationUsesNegotiatedBound)$")
	runnerGoTest(t, "./adapters/go", "^TestProduction(ResponseWriterEnforcesExactAndOversizedBounds|FramingRejectsOversizedBeforeAllocation)$")
	runnerCommand(t, "node", "TypeScript adapter response bounds", "test/protocol_wire.test.js", "adapters/typescript")
	runnerCommand(t, "dart", "Dart adapter response bounds", "test", "test/protocol_test.dart", "--plain-name", "production response writer enforces exact and oversized bounds", "adapters/dart")
	return runnerEvidence(t, "VS02-A9", input)
}

// RunA10 executes initialize and snapshot conformance probes for Dart,
// TypeScript/JavaScript and Go. The protocol integration test derives the
// matrix from those returned identities and leaves missing capability
// observations unsupported.
func RunA10(t *testing.T) Evidence {
	t.Helper()
	input, _ := runnerInput(t)
	runnerGoTest(t, "./internal/analyzer/protocol", "^TestSupportedAdapterInitializeCapabilityMeasurements$")
	return runnerEvidence(t, "VS02-A10", input)
}

// RunA11 executes the real adapter boundary against one VS-01 lease and
// checks source integrity after success, cancellation, timeout and crash
// retry. The subprocess test also asserts the old process is reaped and all
// pending callers are resolved.
func RunA11(t *testing.T) Evidence {
	t.Helper()
	input, audit := runnerInput(t)
	if audit.CapturedSnapshotTreeDigest != input.RootTreeID || audit.CodeFlowWriteCount != 0 || audit.SourceIntegrityViolation || len(audit.RepositoryPathWrites) != 0 {
		t.Fatalf("unexpected snapshot write audit: %+v", audit)
	}
	artifactPath := filepath.Join(t.TempDir(), "rflsc-a11-isolation.json")
	runnerGoTestEnv(t, "./internal/analyzer/protocol", "^(TestAdapterProcessIsolationAndCleanup|TestConnPublishesMountPermissionEvidenceAfterCleanup|TestSourceReadOnlyAdapterLifecycle)$", map[string]string{
		"CODEFLOW_A11_EVIDENCE_PATH":        artifactPath,
		"CODEFLOW_A11_EXPECTED_TREE_DIGEST": input.RootTreeID,
	})
	data, err := os.ReadFile(artifactPath)
	if err != nil {
		t.Fatalf("A11 lifecycle did not emit evidence artifact: %v", err)
	}
	var artifact evidence.IsolationLifecycleEvidence
	if err := json.Unmarshal(data, &artifact); err != nil {
		t.Fatalf("decode A11 lifecycle evidence: %v", err)
	}
	if err := artifact.Validate(); err != nil {
		t.Fatalf("invalid A11 lifecycle evidence: %v", err)
	}
	if artifact.SnapshotID != input.SnapshotID || artifact.SnapshotTreeDigest != input.RootTreeID || artifact.RepositoryPathWriteAudit.CapturedSnapshotTreeDigest != input.RootTreeID {
		t.Fatalf("A11 lifecycle evidence is not bound to runner lease: artifact=%+v input=%+v", artifact, input)
	}
	evidence := runnerEvidence(t, "VS02-A11", input)
	evidence.MountPermissionEvidence = artifact.MountPermissionEvidence
	evidence.ObjectRefs = append(evidence.ObjectRefs, artifact.ArtifactRefs...)
	return evidence
}

func runnerGoTest(t *testing.T, packagePattern, testPattern string) {
	runnerGoTestEnv(t, packagePattern, testPattern, nil)
}

func runnerGoTestEnv(t *testing.T, packagePattern, testPattern string, environment map[string]string) {
	t.Helper()
	root := runnerRepositoryRoot(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "test", packagePattern, "-run", testPattern, "-count=1")
	cmd.Dir = root
	cmd.Env = runnerEnvironment(environment)
	if output, err := cmd.CombinedOutput(); err != nil {
		if ctx.Err() != nil {
			t.Fatalf("%s lifecycle test timed out", packagePattern)
		}
		t.Fatalf("%s lifecycle test failed: %v\n%s", packagePattern, err, output)
	}
}

func runnerEnvironment(overrides map[string]string) []string {
	entries := append([]string(nil), os.Environ()...)
	for key, value := range overrides {
		prefix := key + "="
		filtered := entries[:0]
		for _, entry := range entries {
			if !strings.HasPrefix(entry, prefix) {
				filtered = append(filtered, entry)
			}
		}
		entries = append(filtered, key+"="+value)
	}
	return entries
}

func runnerAdapterObservationTests(t *testing.T) {
	t.Helper()
	runnerGoTest(t, "./adapters/go", "^TestV2ObservationTrackerUsesActualOperationReads$")
	runnerCommand(t, "node", "TypeScript adapter observation tracker", "test/protocol_wire.test.js", "adapters/typescript")
	runnerCommand(t, "dart", "Dart adapter observation tracker", "test", "test/protocol_test.dart", "--plain-name", "v2 observations track actual Dart operation reads", "adapters/dart")
}

func runnerCommand(t *testing.T, binary, label string, args ...string) {
	t.Helper()
	root := runnerRepositoryRoot(t)
	dir := root
	if len(args) > 0 {
		last := args[len(args)-1]
		if last == "adapters/typescript" || last == "adapters/dart" {
			dir = filepath.Join(root, last)
			args = args[:len(args)-1]
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir = dir
	if output, err := cmd.CombinedOutput(); err != nil {
		if ctx.Err() != nil {
			t.Fatalf("%s timed out", label)
		}
		t.Fatalf("%s failed: %v\n%s", label, err, output)
	}
}

func runnerRepositoryRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above runner package")
		}
		dir = parent
	}
}
