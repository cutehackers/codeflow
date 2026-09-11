package contractharness_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	harness "codeflow/internal/contractharness"
	"codeflow/internal/verification/catalog"
)

const (
	catalogImplementationPackage = "codeflow/internal/verification/catalog"
	catalogExecutionPackage      = "codeflow/internal/contractharness"
)

type catalogEvidence struct {
	Criterion             string   `json:"criterion"`
	ImplementationTestID  string   `json:"implementationTestId"`
	ImplementationPackage string   `json:"implementationPackage"`
	ExecutionPackage      string   `json:"executionPackage"`
	ExecutionBinary       string   `json:"executionBinary"`
	ExecutionID           string   `json:"executionId"`
	Result                string   `json:"result"`
	ExecutionCompleted    bool     `json:"executionCompleted"`
	SnapshotID            string   `json:"snapshotId"`
	SnapshotTreeDigest    string   `json:"snapshotTreeDigest"`
	ComputedBasisID       string   `json:"computedBasisId"`
	GenerationID          string   `json:"generationId"`
	ObjectRefs            []string `json:"objectRefs"`
	runnerObserved        bool
}

func TestRFLSCR2VS07_EvidenceRegistry(t *testing.T) {
	if harness.VS07EvidenceRegistryID != "rflsc-r2-vs-07" {
		t.Fatalf("unexpected VS-07 evidence registry ID: %q", harness.VS07EvidenceRegistryID)
	}
	expectedCriteria := append([]string(nil), harness.VS07EvidenceCriteria...)
	runners := map[string]func(*testing.T) catalog.Evidence{
		"VS07-A1": catalog.RunA01,
		"VS07-A2": catalog.RunA02,
		"VS07-A3": catalog.RunA03,
		"VS07-A4": catalog.RunA04,
		"VS07-A5": catalog.RunA05,
		"VS07-A6": catalog.RunA06,
		"VS07-A7": catalog.RunA07,
	}
	if len(runners) != len(expectedCriteria) {
		t.Fatalf("VS-07 evidence runner count=%d want=%d", len(runners), len(expectedCriteria))
	}
	expectedImplementation := expectedVS07ImplementationIDs()
	records := make([]catalogEvidence, 0, len(expectedCriteria))
	for _, criterion := range expectedCriteria {
		runner := runners[criterion]
		if runner == nil {
			t.Fatalf("missing VS-07 evidence runner for %s", criterion)
		}
		var record catalogEvidence
		passed := t.Run(criterion, func(st *testing.T) {
			shared := runner(st)
			if err := catalog.ValidateEvidence(shared); err != nil {
				st.Fatalf("runner returned invalid evidence: %v", err)
			}
			record = vs07ExecutionRecord(st, shared)
			if err := validateVS07EvidenceRecord(criterion, st.Name(), record, expectedImplementation); err != nil {
				st.Fatal(err)
			}
			// The registry marks a record as passing only after the implementation
			// runner has returned and all identity/ref checks have completed.
			record.runnerObserved = true
			record.ExecutionCompleted = true
			record.Result = "pass"
			if err := validateVS07CompletedEvidenceRecord(criterion, st.Name(), record, expectedImplementation); err != nil {
				st.Fatal(err)
			}
		})
		if !passed {
			t.Fatalf("criterion %s was not executed successfully", criterion)
		}
		if !record.runnerObserved || record.Result != "pass" || !record.ExecutionCompleted {
			t.Fatalf("criterion %s has no completed passing execution: %+v", criterion, record)
		}
		t.Logf("evidence=%s", mustJSONVS07(record))
		records = append(records, record)
	}
	if err := validateVS07RegistryRecords(records, expectedCriteria); err != nil {
		t.Fatal(err)
	}
}

func expectedVS07ImplementationIDs() map[string]string {
	return map[string]string{
		"VS07-A1": "codeflow/internal/verification/catalog.TestRFLSCR2VS07_A01",
		"VS07-A2": "codeflow/internal/verification/catalog.TestRFLSCR2VS07_A02",
		"VS07-A3": "codeflow/internal/verification/catalog.TestRFLSCR2VS07_A03",
		"VS07-A4": "codeflow/internal/verification/catalog.TestRFLSCR2VS07_A04",
		"VS07-A5": "codeflow/internal/verification/catalog.TestRFLSCR2VS07_A05",
		"VS07-A6": "codeflow/internal/verification/catalog.TestRFLSCR2VS07_A06",
		"VS07-A7": "codeflow/internal/verification/catalog.TestRFLSCR2VS07_A07",
	}
}

func vs07ExecutionRecord(t *testing.T, shared catalog.Evidence) catalogEvidence {
	t.Helper()
	return catalogEvidence{
		Criterion: shared.Criterion, ImplementationTestID: shared.ImplementationTestID,
		ImplementationPackage: shared.ImplementationPackage, ExecutionPackage: catalogExecutionPackage,
		ExecutionBinary: filepath.Base(os.Args[0]), ExecutionID: t.Name(), SnapshotID: shared.SnapshotID,
		SnapshotTreeDigest: shared.SnapshotTreeDigest, ComputedBasisID: shared.ComputedBasisID,
		GenerationID: shared.GenerationID, ObjectRefs: append([]string(nil), shared.ObjectRefs...),
	}
}

func validateVS07EvidenceRecord(criterion, executionID string, record catalogEvidence, expected map[string]string) error {
	if record.Criterion != criterion {
		return fmt.Errorf("criterion identity mismatch: got %q want %q", record.Criterion, criterion)
	}
	if record.ImplementationTestID != expected[criterion] || record.ImplementationPackage != catalogImplementationPackage {
		return fmt.Errorf("implementation identity mismatch: %+v", record)
	}
	if record.ExecutionPackage != catalogExecutionPackage || strings.TrimSpace(record.ExecutionBinary) == "" || record.ExecutionID != executionID {
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
	if record.SnapshotID == "" || record.SnapshotTreeDigest == "" || record.ComputedBasisID == "" || record.GenerationID == "" {
		return fmt.Errorf("immutable onboarding identity is incomplete: %+v", record)
	}
	if len(record.ObjectRefs) < 5 {
		return fmt.Errorf("immutable onboarding refs are incomplete: %+v", record.ObjectRefs)
	}
	seen := make(map[string]bool, len(record.ObjectRefs))
	hasSnapshot, hasTree, hasBasis, hasGraph, hasArtifact := false, false, false, false, false
	for _, ref := range record.ObjectRefs {
		parts := strings.SplitN(ref, ":", 2)
		if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" || seen[ref] {
			return fmt.Errorf("invalid or duplicate immutable ref %q", ref)
		}
		seen[ref] = true
		switch parts[0] {
		case "snapshot":
			if parts[1] != record.SnapshotID {
				return fmt.Errorf("snapshot ref %q does not match %q", ref, record.SnapshotID)
			}
			hasSnapshot = true
		case "tree":
			if parts[1] != record.SnapshotTreeDigest {
				return fmt.Errorf("tree ref %q does not match %q", ref, record.SnapshotTreeDigest)
			}
			hasTree = true
		case "basis":
			if parts[1] != record.ComputedBasisID {
				return fmt.Errorf("basis ref %q does not match %q", ref, record.ComputedBasisID)
			}
			hasBasis = true
		case "graph":
			hasGraph = true
		default:
			hasArtifact = true
		}
	}
	if !hasSnapshot || !hasTree || !hasBasis || !hasGraph || !hasArtifact {
		return fmt.Errorf("snapshot, tree, basis, graph and criterion refs are required: %+v", record.ObjectRefs)
	}
	return nil
}

func validateVS07CompletedEvidenceRecord(criterion, executionID string, record catalogEvidence, expected map[string]string) error {
	if err := validateVS07EvidenceRecord(criterion, executionID, record, expected); err != nil {
		return err
	}
	if !record.runnerObserved {
		return fmt.Errorf("record is fabricated: no implementation runner was observed")
	}
	if !record.ExecutionCompleted || record.Result != "pass" {
		return fmt.Errorf("evidence record is not a completed passing execution: %+v", record)
	}
	return nil
}

func validateVS07RegistryRecords(records []catalogEvidence, expectedCriteria []string) error {
	expected := make(map[string]bool, len(expectedCriteria))
	for _, criterion := range expectedCriteria {
		if expected[criterion] {
			return fmt.Errorf("VS-07 expected criteria contain duplicate %q", criterion)
		}
		expected[criterion] = true
	}
	counts := make(map[string]int, len(records))
	for _, record := range records {
		if !expected[record.Criterion] {
			return fmt.Errorf("VS-07 registry has zero-match or unknown criterion %q", record.Criterion)
		}
		counts[record.Criterion]++
		if !record.runnerObserved || !record.ExecutionCompleted || record.Result != "pass" {
			return fmt.Errorf("VS-07 registry has not-run criterion %q", record.Criterion)
		}
	}
	if len(records) != len(expectedCriteria) {
		return fmt.Errorf("VS-07 registry has missing or duplicate records: got %d want %d (counts=%v)", len(records), len(expectedCriteria), counts)
	}
	for _, criterion := range expectedCriteria {
		if counts[criterion] != 1 {
			return fmt.Errorf("VS-07 registry requires exactly one executed record for %s, got %d", criterion, counts[criterion])
		}
	}
	return nil
}

func mustJSONVS07(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprintf("<json error: %v>", err)
	}
	return string(data)
}

func TestVS07EvidenceRegistryRejectsMissingDuplicateNotRunAndFabricatedPass(t *testing.T) {
	expectedCriteria := []string{"VS07-A1", "VS07-A2"}
	expectedImplementation := expectedVS07ImplementationIDs()
	valid := func(criterion, executionID string) catalogEvidence {
		return catalogEvidence{
			Criterion: criterion, ImplementationTestID: expectedImplementation[criterion], ImplementationPackage: catalogImplementationPackage,
			ExecutionPackage: catalogExecutionPackage, ExecutionBinary: "contractharness.test", ExecutionID: executionID,
			Result: "pass", ExecutionCompleted: true, runnerObserved: true,
			SnapshotID: "snapshot-vs07", SnapshotTreeDigest: "tree-vs07", ComputedBasisID: "basis-vs07", GenerationID: "generation-vs07",
			ObjectRefs: []string{"snapshot:snapshot-vs07", "tree:tree-vs07", "basis:basis-vs07", "graph:map-vs07", "overview:domain-vs07"},
		}
	}
	cases := []struct {
		name    string
		records []catalogEvidence
	}{
		{name: "missing", records: []catalogEvidence{valid("VS07-A1", "run-a1")}},
		{name: "duplicate", records: []catalogEvidence{valid("VS07-A1", "run-a1"), valid("VS07-A1", "run-a1b")}},
		{name: "not-run", records: []catalogEvidence{{Criterion: "VS07-A1"}}},
		{name: "zero-match", records: nil},
		{name: "fabricated-pass", records: []catalogEvidence{func() catalogEvidence {
			record := valid("VS07-A1", "fake-execution")
			record.runnerObserved = false
			return record
		}()}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if testCase.name == "fabricated-pass" {
				if err := validateVS07CompletedEvidenceRecord("VS07-A1", testCase.records[0].ExecutionID, testCase.records[0], expectedImplementation); err == nil {
					t.Fatalf("fabricated pass was accepted: %+v", testCase.records[0])
				}
				return
			}
			if err := validateVS07RegistryRecords(testCase.records, expectedCriteria); err == nil {
				t.Fatalf("invalid registry state %s was accepted: %+v", testCase.name, testCase.records)
			}
		})
	}
}

func TestVS07EvidenceRegistryRejectsDuplicateImmutableRef(t *testing.T) {
	record := catalogEvidence{
		Criterion: "VS07-A1", ImplementationTestID: expectedVS07ImplementationIDs()["VS07-A1"], ImplementationPackage: catalogImplementationPackage,
		ExecutionPackage: catalogExecutionPackage, ExecutionBinary: "contractharness.test", ExecutionID: "run-a1", Result: "pass", ExecutionCompleted: true, runnerObserved: true,
		SnapshotID: "snapshot-vs07", SnapshotTreeDigest: "tree-vs07", ComputedBasisID: "basis-vs07", GenerationID: "generation-vs07",
		ObjectRefs: []string{"snapshot:snapshot-vs07", "tree:tree-vs07", "basis:basis-vs07", "graph:map-vs07", "overview:domain-vs07", "graph:map-vs07"},
	}
	if err := validateVS07EvidenceRecord(record.Criterion, record.ExecutionID, record, expectedVS07ImplementationIDs()); err == nil {
		t.Fatalf("duplicate immutable reference was accepted: %+v", record)
	}
}
