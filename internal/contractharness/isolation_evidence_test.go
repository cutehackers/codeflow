package contractharness_test

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	harness "codeflow/internal/contractharness"
	"codeflow/internal/verification/isolation"
	"codeflow/internal/workspace"
	"codeflow/schemas"
)

const (
	isolationImplementationPackage = "codeflow/internal/verification/isolation"
	isolationExecutionPackage      = "codeflow/internal/contractharness"
)

type isolationEvidence struct {
	Criterion                string                     `json:"criterion"`
	ImplementationTestID     string                     `json:"implementationTestId"`
	ImplementationPackage    string                     `json:"implementationPackage"`
	ExecutionPackage         string                     `json:"executionPackage"`
	ExecutionBinary          string                     `json:"executionBinary"`
	ExecutionID              string                     `json:"executionId"`
	Result                   string                     `json:"result"`
	ExecutionCompleted       bool                       `json:"executionCompleted"`
	SnapshotID               string                     `json:"snapshotId"`
	SnapshotTreeDigest       string                     `json:"snapshotTreeDigest"`
	InputTreeVerified        bool                       `json:"inputTreeVerified"`
	CopyOnWriteLayerDisposed bool                       `json:"copyOnWriteLayerDisposed"`
	RepositoryPathWriteAudit workspace.SourceWriteAudit `json:"repositoryPathWriteAudit"`
	RuntimeOutcomes          []isolation.RuntimeOutcome `json:"runtimeOutcomes"`
	ObjectRefs               []string                   `json:"objectRefs"`
}

func readVS06Fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := fs.ReadFile(schemas.FixturesFS, "fixtures/"+name)
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return data
}

func cloneVS06Document(t *testing.T, data []byte) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	return doc
}

func marshalVS06Document(t *testing.T, doc map[string]any) []byte {
	t.Helper()
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("encode fixture: %v", err)
	}
	return data
}

func TestRFLSCR2VS06_EvidenceRegistry(t *testing.T) {
	if harness.VS06EvidenceRegistryID != "rflsc-r2-vs-06" {
		t.Fatalf("unexpected VS-06 evidence registry ID: %q", harness.VS06EvidenceRegistryID)
	}
	if err := harness.EnsureAllCompiled(); err != nil {
		t.Fatalf("compile contract registry: %v", err)
	}
	expected := []string{
		"rflsc.failure-query.v2",
		"rflsc.failure-path-trace.v2",
		"rflsc.runtime-observation.v2",
		"rflsc.runtime-consent.v1",
		"rflsc.runtime-isolation-result.v1",
	}
	if len(harness.VS06ContractRegistry) != len(expected) {
		t.Fatalf("VS-06 contract registry count=%d want=%d", len(harness.VS06ContractRegistry), len(expected))
	}
	for index, entry := range harness.VS06ContractRegistry {
		if entry.ID != expected[index] {
			t.Fatalf("unexpected VS-06 registry order or ID at %d: %+v", index, entry)
		}
		valid := readVS06Fixture(t, entry.ValidFixture)
		invalidScope := readVS06Fixture(t, entry.InvalidScopeFixture)
		invalidAuthority := readVS06Fixture(t, entry.InvalidAuthorityFixture)
		invalidAnchor := readVS06Fixture(t, entry.InvalidAnchorFixture)
		if err := harness.ValidateVS06RegistryEntry(entry, valid, invalidScope, invalidAuthority, invalidAnchor); err != nil {
			t.Fatalf("VS-06 contract fixture validation failed for %s: %v", entry.ID, err)
		}
	}

	expectedCriteria := []string{"VS06-A1", "VS06-A2", "VS06-A3", "VS06-A4", "VS06-A5", "VS06-A6", "VS06-A7", "VS06-A8", "VS06-A9"}
	runners := map[string]func(*testing.T) isolation.Evidence{
		"VS06-A1": isolation.RunA01,
		"VS06-A2": isolation.RunA02,
		"VS06-A3": isolation.RunA03,
		"VS06-A4": isolation.RunA04,
		"VS06-A5": isolation.RunA05,
		"VS06-A6": isolation.RunA06,
		"VS06-A7": isolation.RunA07,
		"VS06-A8": isolation.RunA08,
		"VS06-A9": isolation.RunA09,
	}
	if len(runners) != len(expectedCriteria) {
		t.Fatalf("VS-06 evidence runner count=%d want=%d", len(runners), len(expectedCriteria))
	}
	expectedImplementation := expectedVS06ImplementationIDs()
	records := make([]isolationEvidence, 0, len(expectedCriteria))
	for _, criterion := range expectedCriteria {
		runner, ok := runners[criterion]
		if !ok || runner == nil {
			t.Fatalf("missing VS-06 evidence runner for %s", criterion)
		}
		var record isolationEvidence
		passed := t.Run(criterion, func(st *testing.T) {
			shared := runner(st)
			if err := isolation.ValidateEvidence(shared); err != nil {
				st.Fatalf("runner returned invalid evidence: %v", err)
			}
			record = buildIsolationRecord(st, shared)
			if err := validateVS06EvidenceRecord(criterion, st.Name(), record, expectedImplementation); err != nil {
				st.Fatal(err)
			}
			// A pass is recorded only after the production runner has returned and
			// all identity, digest, cleanup and audit checks have completed.
			record.ExecutionCompleted = true
			record.Result = "pass"
			if err := validateVS06CompletedEvidenceRecord(criterion, st.Name(), record, expectedImplementation); err != nil {
				st.Fatal(err)
			}
		})
		if !passed {
			t.Fatalf("criterion %s was not executed successfully", criterion)
		}
		if record.Result != "pass" || !record.ExecutionCompleted {
			t.Fatalf("criterion %s has no completed passing execution: %+v", criterion, record)
		}
		t.Logf("evidence=%s", mustJSONVS06(record))
		records = append(records, record)
	}

	if err := validateVS06RegistryRecords(records, expectedCriteria); err != nil {
		t.Fatal(err)
	}
}

func expectedVS06ImplementationIDs() map[string]string {
	return map[string]string{
		"VS06-A1": "codeflow/internal/verification/isolation.TestRFLSCR2VS06_A01",
		"VS06-A2": "codeflow/internal/verification/isolation.TestRFLSCR2VS06_A02",
		"VS06-A3": "codeflow/internal/verification/isolation.TestRFLSCR2VS06_A03",
		"VS06-A4": "codeflow/internal/verification/isolation.TestRFLSCR2VS06_A04",
		"VS06-A5": "codeflow/internal/verification/isolation.TestRFLSCR2VS06_A05",
		"VS06-A6": "codeflow/internal/verification/isolation.TestRFLSCR2VS06_A06",
		"VS06-A7": "codeflow/internal/verification/isolation.TestRFLSCR2VS06_A07",
		"VS06-A8": "codeflow/internal/verification/isolation.TestRFLSCR2VS06_A08",
		"VS06-A9": "codeflow/internal/verification/isolation.TestRFLSCR2VS06_A09",
	}
}

func buildIsolationRecord(t *testing.T, shared isolation.Evidence) isolationEvidence {
	t.Helper()
	audit := shared.RepositoryPathWriteAudit
	audit.RepositoryPathWrites = append([]string(nil), shared.RepositoryPathWriteAudit.RepositoryPathWrites...)
	return isolationEvidence{
		Criterion:                shared.Criterion,
		ImplementationTestID:     shared.ImplementationTestID,
		ImplementationPackage:    shared.ImplementationPackage,
		ExecutionPackage:         isolationExecutionPackage,
		ExecutionBinary:          filepath.Base(os.Args[0]),
		ExecutionID:              t.Name(),
		SnapshotID:               shared.SnapshotID,
		SnapshotTreeDigest:       shared.SnapshotTreeDigest,
		InputTreeVerified:        shared.InputTreeVerified,
		CopyOnWriteLayerDisposed: shared.CopyOnWriteLayerDisposed,
		RepositoryPathWriteAudit: audit,
		RuntimeOutcomes:          append([]isolation.RuntimeOutcome(nil), shared.RuntimeOutcomes...),
		ObjectRefs:               append([]string(nil), shared.ObjectRefs...),
	}
}

func validateVS06EvidenceRecord(criterion, executionID string, record isolationEvidence, expected map[string]string) error {
	if record.Criterion != criterion {
		return fmt.Errorf("criterion identity mismatch: got %q want %q", record.Criterion, criterion)
	}
	if record.ImplementationTestID != expected[criterion] || record.ImplementationPackage != isolationImplementationPackage {
		return fmt.Errorf("implementation identity mismatch: %+v", record)
	}
	if record.ExecutionPackage != isolationExecutionPackage || record.ExecutionBinary == "" || record.ExecutionID != executionID {
		return fmt.Errorf("execution identity mismatch: %+v", record)
	}
	if record.Result != "" && record.Result != "pass" {
		return fmt.Errorf("unknown execution result: %+v", record)
	}
	if record.Result == "pass" && !record.ExecutionCompleted {
		return fmt.Errorf("pass was recorded before execution completed: %+v", record)
	}
	if record.ExecutionCompleted && record.Result != "pass" {
		return fmt.Errorf("execution completion has no passing result: %+v", record)
	}
	if record.SnapshotID == "" || record.SnapshotTreeDigest == "" || !record.InputTreeVerified || len(record.ObjectRefs) < 4 {
		return fmt.Errorf("immutable input evidence is incomplete: %+v", record)
	}
	seen := make(map[string]bool, len(record.ObjectRefs))
	hasSnapshot, hasTree, hasBasis, hasArtifact := false, false, false, false
	for _, ref := range record.ObjectRefs {
		parts := strings.SplitN(ref, ":", 2)
		if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" || seen[ref] {
			return fmt.Errorf("invalid or duplicate immutable artifact ref %q", ref)
		}
		seen[ref] = true
		switch parts[0] {
		case "snapshot":
			if parts[1] != record.SnapshotID {
				return fmt.Errorf("snapshot ref %q does not match snapshot ID %q", ref, record.SnapshotID)
			}
			hasSnapshot = true
		case "tree":
			if parts[1] != record.SnapshotTreeDigest {
				return fmt.Errorf("tree ref %q does not match snapshot tree digest %q", ref, record.SnapshotTreeDigest)
			}
			hasTree = true
		case "basis":
			hasBasis = true
		default:
			hasArtifact = true
		}
	}
	if !hasSnapshot || !hasTree || !hasBasis || !hasArtifact {
		return fmt.Errorf("snapshot, tree, basis and criterion artifact refs are required: %+v", record.ObjectRefs)
	}
	if criterion == "VS06-A9" {
		if !record.CopyOnWriteLayerDisposed || record.RepositoryPathWriteAudit.CapturedSnapshotTreeDigest != record.SnapshotTreeDigest || record.RepositoryPathWriteAudit.SourceIntegrityViolation || record.RepositoryPathWriteAudit.CodeFlowWriteCount != 0 || len(record.RepositoryPathWriteAudit.RepositoryPathWrites) != 0 {
			return fmt.Errorf("A09 cleanup/write audit proof is incomplete: %+v", record)
		}
		statuses := map[string]bool{"success": false, "failure": false, "timeout": false, "cancel": false}
		seenNames := map[string]bool{}
		for _, outcome := range record.RuntimeOutcomes {
			if outcome.Name == "" || seenNames[outcome.Name] || outcome.ExecutionID == "" || !outcome.InputTreeVerified || !outcome.CleanupVerified || !outcome.SourceReadOnly || !outcome.Disposable || outcome.RepositoryPathExposed {
				return fmt.Errorf("A09 runtime outcome is missing, duplicated or unverified: %+v", outcome)
			}
			seenNames[outcome.Name] = true
			if _, ok := statuses[outcome.Status]; ok {
				statuses[outcome.Status] = true
			}
		}
		for name, observed := range statuses {
			if !observed {
				return fmt.Errorf("A09 runtime terminal mode %q was not executed", name)
			}
		}
		if !seenNames["replay"] || !seenNames["concurrent-live-edit"] {
			return fmt.Errorf("A09 replay and concurrent live-edit evidence is missing: %+v", seenNames)
		}
		var auditViolation, reconciled bool
		for _, outcome := range record.RuntimeOutcomes {
			auditViolation = auditViolation || outcome.SourceWriteAuditStatus == "source_integrity_violation" && outcome.EvidencePromotion == "blocked"
			reconciled = reconciled || outcome.Name == "concurrent-live-edit" && outcome.ConcurrentClassification == "reconciled_unattributed"
		}
		if !auditViolation || !reconciled {
			return fmt.Errorf("A09 audit violation or concurrent reconciliation evidence is missing: %+v", record.RuntimeOutcomes)
		}
	}
	return nil
}

func validateVS06CompletedEvidenceRecord(criterion, executionID string, record isolationEvidence, expected map[string]string) error {
	if err := validateVS06EvidenceRecord(criterion, executionID, record, expected); err != nil {
		return err
	}
	if !record.ExecutionCompleted || record.Result != "pass" {
		return fmt.Errorf("evidence record is not a completed passing execution: %+v", record)
	}
	return nil
}

func validateVS06RegistryRecords(records []isolationEvidence, expectedCriteria []string) error {
	expected := make(map[string]bool, len(expectedCriteria))
	for _, criterion := range expectedCriteria {
		if expected[criterion] {
			return fmt.Errorf("VS-06 registry expectation contains duplicate criterion %q", criterion)
		}
		expected[criterion] = true
	}
	counts := make(map[string]int, len(records))
	for _, record := range records {
		if !expected[record.Criterion] {
			return fmt.Errorf("VS-06 evidence registry has zero-match or unknown criterion %q", record.Criterion)
		}
		counts[record.Criterion]++
	}
	if len(records) != len(expectedCriteria) {
		return fmt.Errorf("VS-06 evidence registry has missing or duplicate records: got %d want %d (counts=%v)", len(records), len(expectedCriteria), counts)
	}
	for _, criterion := range expectedCriteria {
		if counts[criterion] != 1 {
			return fmt.Errorf("VS-06 evidence registry requires exactly one executed record for %s, got %d", criterion, counts[criterion])
		}
	}
	return nil
}

func mustJSONVS06(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprintf("<json error: %v>", err)
	}
	return string(data)
}

func TestVS06EvidenceRegistryRejectsFabricatedPass(t *testing.T) {
	record := isolationEvidence{
		Criterion: "VS06-A1", ImplementationTestID: "codeflow/internal/verification/isolation.TestRFLSCR2VS06_A01", ImplementationPackage: isolationImplementationPackage,
		ExecutionPackage: isolationExecutionPackage, ExecutionBinary: "contractharness.test", ExecutionID: "fake-execution", Result: "pass",
		SnapshotID: "snapshot-vs06", SnapshotTreeDigest: "tree-vs06", InputTreeVerified: true,
		ObjectRefs: []string{"snapshot:snapshot-vs06", "tree:tree-vs06", "basis:basis-vs06", "graph:map-vs06"},
	}
	if err := validateVS06EvidenceRecord(record.Criterion, record.ExecutionID, record, expectedVS06ImplementationIDs()); err == nil {
		t.Fatal("fabricated pass without completed production execution was accepted")
	}
}

func TestVS06EvidenceRegistryRejectsIncompleteEvidence(t *testing.T) {
	base := isolationEvidence{
		Criterion: "VS06-A1", ImplementationTestID: "codeflow/internal/verification/isolation.TestRFLSCR2VS06_A01", ImplementationPackage: isolationImplementationPackage,
		ExecutionPackage: isolationExecutionPackage, ExecutionBinary: "contractharness.test", ExecutionID: "completed-vs06-a1", Result: "pass", ExecutionCompleted: true,
		SnapshotID: "snapshot-vs06", SnapshotTreeDigest: "tree-vs06", InputTreeVerified: true,
		ObjectRefs: []string{"snapshot:snapshot-vs06", "tree:tree-vs06", "basis:basis-vs06", "graph:map-vs06"},
	}
	cases := []struct {
		name   string
		mutate func(*isolationEvidence)
	}{
		{name: "not-run", mutate: func(record *isolationEvidence) {
			record.ExecutionCompleted = false
			record.Result = ""
		}},
		{name: "zero-match", mutate: func(record *isolationEvidence) {
			record.Criterion = "VS06-A0"
		}},
		{name: "duplicate-evidence-ref", mutate: func(record *isolationEvidence) {
			record.ObjectRefs = append(record.ObjectRefs, "graph:map-vs06")
		}},
		{name: "missing-evidence-ref", mutate: func(record *isolationEvidence) {
			record.ObjectRefs = []string{"snapshot:snapshot-vs06", "tree:tree-vs06", "basis:basis-vs06"}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			record := base
			record.ObjectRefs = append([]string(nil), base.ObjectRefs...)
			tc.mutate(&record)
			if err := validateVS06CompletedEvidenceRecord("VS06-A1", record.ExecutionID, record, expectedVS06ImplementationIDs()); err == nil {
				t.Fatalf("invalid evidence mutation was accepted: %+v", record)
			}
		})
	}
}

func TestVS06EvidenceRegistryRejectsMissingDuplicateAndZeroMatchRecords(t *testing.T) {
	base := isolationEvidence{Criterion: "VS06-A1"}
	cases := []struct {
		name    string
		records []isolationEvidence
	}{
		{name: "missing", records: []isolationEvidence{base}},
		{name: "duplicate", records: []isolationEvidence{base, base}},
		{name: "zero-match", records: []isolationEvidence{{Criterion: "VS06-A0"}, base}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateVS06RegistryRecords(tc.records, []string{"VS06-A1", "VS06-A2"}); err == nil {
				t.Fatalf("invalid registry record set was accepted: %+v", tc.records)
			}
		})
	}
}

// These checks intentionally mutate documents in ways that remain valid JSON
// Schema instances. They pin the semantic validator rules at the boundary.
func TestVS06SemanticValidatorsRejectSchemaValidCrossFieldViolations(t *testing.T) {
	t.Run("incident time window is ordered", func(t *testing.T) {
		raw := readVS06Fixture(t, "rflsc.failure-query.v2/valid/debug.json")
		doc := cloneVS06Document(t, raw)
		doc["mode"] = "incident"
		delete(doc, "debug")
		doc["incident"] = map[string]any{
			"traceId":               "trace-vs06",
			"scenario":              "checkout",
			"environment":           "staging",
			"dependencyFingerprint": "deps-vs06",
			"timeWindow": map[string]any{
				"from": "2026-09-05T00:05:00Z",
				"to":   "2026-09-05T00:00:00Z",
			},
		}
		if err := harness.ValidateFailureQueryV2(marshalVS06Document(t, doc)); err == nil {
			t.Fatal("expected reversed incident time window to fail semantic validation")
		}
	})

	t.Run("confirmed nodes remain connected", func(t *testing.T) {
		raw := readVS06Fixture(t, "rflsc.failure-path-trace.v2/valid/debug-trace.json")
		doc := cloneVS06Document(t, raw)
		doc["relationships"] = []any{}
		doc["coverage"] = map[string]any{
			"complete": true, "confirmedNodeCount": 2, "confirmedEdgeCount": 0, "unresolvedEdgeCount": 0,
		}
		if err := harness.ValidateFailurePathTraceV2(marshalVS06Document(t, doc)); err == nil {
			t.Fatal("expected disconnected confirmed nodes to fail semantic validation")
		}
	})

	t.Run("coverage and unknown counts match", func(t *testing.T) {
		raw := readVS06Fixture(t, "rflsc.failure-path-trace.v2/valid/debug-trace.json")
		doc := cloneVS06Document(t, raw)
		coverage := doc["coverage"].(map[string]any)
		coverage["confirmedNodeCount"] = float64(1)
		if err := harness.ValidateFailurePathTraceV2(marshalVS06Document(t, doc)); err == nil {
			t.Fatal("expected mismatched coverage count to fail semantic validation")
		}
	})

	t.Run("runtime status requires runtime evidence", func(t *testing.T) {
		raw := readVS06Fixture(t, "rflsc.failure-path-trace.v2/valid/debug-trace.json")
		doc := cloneVS06Document(t, raw)
		nodes := doc["nodes"].([]any)
		node := nodes[0].(map[string]any)
		node["status"] = "runtime_observed"
		if err := harness.ValidateFailurePathTraceV2(marshalVS06Document(t, doc)); err == nil {
			t.Fatal("expected static-only evidence to fail runtime_observed status")
		}
	})

	t.Run("runtime event stays inside scope", func(t *testing.T) {
		raw := readVS06Fixture(t, "rflsc.runtime-observation.v2/valid/scoped-trace.json")
		doc := cloneVS06Document(t, raw)
		events := doc["events"].([]any)
		events[0].(map[string]any)["timestamp"] = "2026-09-05T00:06:00Z"
		if err := harness.ValidateRuntimeObservationV2(marshalVS06Document(t, doc)); err == nil {
			t.Fatal("expected out-of-window runtime event to fail semantic validation")
		}
	})

	t.Run("runtime evidence keeps dependency scope", func(t *testing.T) {
		raw := readVS06Fixture(t, "rflsc.runtime-observation.v2/valid/scoped-trace.json")
		doc := cloneVS06Document(t, raw)
		evidence := doc["evidence"].([]any)
		evidence[0].(map[string]any)["dependencyFingerprint"] = "different-dependency"
		if err := harness.ValidateRuntimeObservationV2(marshalVS06Document(t, doc)); err == nil {
			t.Fatal("expected dependency scope mismatch to fail semantic validation")
		}
	})
}
