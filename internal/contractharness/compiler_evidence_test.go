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
	"codeflow/internal/verification/compiler"
	"codeflow/schemas"
)

const (
	compilerImplementationPackage = "codeflow/internal/verification/compiler"
	compilerExecutionPackage      = "codeflow/internal/contractharness"
)

type compilerEvidence struct {
	Criterion             string   `json:"criterion"`
	ImplementationTestID  string   `json:"implementationTestId"`
	ImplementationPackage string   `json:"implementationPackage"`
	ExecutionPackage      string   `json:"executionPackage"`
	ExecutionBinary       string   `json:"executionBinary"`
	ExecutionID           string   `json:"executionId"`
	Result                string   `json:"result"`
	ExecutionCompleted    bool     `json:"executionCompleted"`
	SnapshotTreeDigest    string   `json:"snapshotTreeDigest"`
	ObjectRefs            []string `json:"objectRefs"`
}

func TestRFLSCR2VS04_EvidenceRegistry(t *testing.T) {
	if harness.VS04EvidenceRegistryID != "rflsc-r2-vs-04" {
		t.Fatalf("unexpected VS-04 evidence registry ID: %q", harness.VS04EvidenceRegistryID)
	}
	if err := harness.EnsureAllCompiled(); err != nil {
		t.Fatalf("compile contract registry: %v", err)
	}

	expectedContracts := []string{
		"rflsc.task-intent.v2",
		"rflsc.feature-query.v2",
		"rflsc.review-query.v2",
		"rflsc.semantic-map-ir.v2",
		"rflsc.semantic-delta-ir.v2",
		"rflsc.requirement-alignment.v2",
		"rflsc.flowview-projection.v2",
	}
	if len(harness.VS04ContractRegistry) != len(expectedContracts) {
		t.Fatalf("VS-04 contract registry count=%d want=%d", len(harness.VS04ContractRegistry), len(expectedContracts))
	}
	contractIDs := make(map[string]bool, len(harness.VS04ContractRegistry))
	for index, entry := range harness.VS04ContractRegistry {
		if index >= len(expectedContracts) || entry.ID != expectedContracts[index] {
			t.Fatalf("unexpected VS-04 registry order or ID at %d: %+v", index, entry)
		}
		if contractIDs[entry.ID] {
			t.Fatalf("duplicate VS-04 contract registry entry: %s", entry.ID)
		}
		contractIDs[entry.ID] = true

		valid, err := readVS04Fixture(entry.ValidFixture)
		if err != nil {
			t.Fatalf("read valid fixture %s: %v", entry.ID, err)
		}
		invalidAuthority, err := readVS04Fixture(entry.InvalidAuthorityFixture)
		if err != nil {
			t.Fatalf("read invalid authority fixture %s: %v", entry.ID, err)
		}
		invalidIdentity, err := readVS04Fixture(entry.InvalidIdentityFixture)
		if err != nil {
			t.Fatalf("read invalid identity fixture %s: %v", entry.ID, err)
		}
		if err := harness.ValidateVS04RegistryEntry(entry, valid, invalidAuthority, invalidIdentity); err != nil {
			t.Fatalf("VS-04 contract fixture validation failed for %s: %v", entry.ID, err)
		}
	}

	expectedCriteria := []string{
		"VS04-A1", "VS04-A2", "VS04-A3", "VS04-A4", "VS04-A5", "VS04-A6", "VS04-A7",
		"VS04-A8", "VS04-A9", "VS04-A10", "VS04-A11", "VS04-A12", "VS04-A13", "VS04-A14",
	}
	runners := map[string]func(*testing.T) compiler.Evidence{
		"VS04-A1": compiler.RunA01, "VS04-A2": compiler.RunA02, "VS04-A3": compiler.RunA03,
		"VS04-A4": compiler.RunA04, "VS04-A5": compiler.RunA05, "VS04-A6": compiler.RunA06,
		"VS04-A7": compiler.RunA07, "VS04-A8": compiler.RunA08, "VS04-A9": compiler.RunA09,
		"VS04-A10": compiler.RunA10, "VS04-A11": compiler.RunA11, "VS04-A12": compiler.RunA12,
		"VS04-A13": compiler.RunA13, "VS04-A14": compiler.RunA14,
	}
	if len(runners) != len(expectedCriteria) {
		t.Fatalf("VS-04 evidence runner count=%d want=%d", len(runners), len(expectedCriteria))
	}

	expectedImplementation := expectedVS04ImplementationIDs()
	records := make([]compilerEvidence, 0, len(expectedCriteria))
	for _, criterion := range expectedCriteria {
		runner, ok := runners[criterion]
		if !ok || runner == nil {
			t.Fatalf("missing VS-04 evidence runner for %s", criterion)
		}
		var record compilerEvidence
		passed := t.Run(criterion, func(st *testing.T) {
			shared := runner(st)
			record = vs04ExecutionRecord(st, shared)
			if err := validateVS04EvidenceRecord(criterion, st.Name(), record, expectedImplementation); err != nil {
				st.Fatal(err)
			}
			// A passing result is assigned only after the shared runner and all
			// implementation/artifact identity checks have succeeded.
			record.ExecutionCompleted = true
			record.Result = "pass"
			if err := validateVS04EvidenceRecord(criterion, st.Name(), record, expectedImplementation); err != nil {
				st.Fatal(err)
			}
		})
		if !passed {
			t.Fatalf("criterion %s was not executed successfully", criterion)
		}
		if record.Result != "pass" || !record.ExecutionCompleted {
			t.Fatalf("criterion %s has no completed passing execution: %+v", criterion, record)
		}
		t.Logf("evidence=%s", mustJSONVS04(record))
		records = append(records, record)
	}

	counts := make(map[string]int, len(records))
	for _, record := range records {
		counts[record.Criterion]++
	}
	if len(counts) != len(expectedCriteria) {
		t.Fatalf("VS-04 evidence registry has zero-match or unknown criteria: %+v", counts)
	}
	for _, criterion := range expectedCriteria {
		if counts[criterion] != 1 {
			t.Fatalf("VS-04 evidence registry requires exactly one executed record for %s, got %d", criterion, counts[criterion])
		}
	}
}

func readVS04Fixture(name string) ([]byte, error) {
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("fixture path is empty")
	}
	return fs.ReadFile(schemas.FixturesFS, filepath.ToSlash(filepath.Join("fixtures", name)))
}

func expectedVS04ImplementationIDs() map[string]string {
	return map[string]string{
		"VS04-A1": "codeflow/internal/verification/compiler.TestRFLSCR2VS04_A01", "VS04-A2": "codeflow/internal/verification/compiler.TestRFLSCR2VS04_A02",
		"VS04-A3": "codeflow/internal/verification/compiler.TestRFLSCR2VS04_A03", "VS04-A4": "codeflow/internal/verification/compiler.TestRFLSCR2VS04_A04",
		"VS04-A5": "codeflow/internal/verification/compiler.TestRFLSCR2VS04_A05", "VS04-A6": "codeflow/internal/verification/compiler.TestRFLSCR2VS04_A06",
		"VS04-A7": "codeflow/internal/verification/compiler.TestRFLSCR2VS04_A07", "VS04-A8": "codeflow/internal/verification/compiler.TestRFLSCR2VS04_A08",
		"VS04-A9": "codeflow/internal/verification/compiler.TestRFLSCR2VS04_A09", "VS04-A10": "codeflow/internal/verification/compiler.TestRFLSCR2VS04_A10",
		"VS04-A11": "codeflow/internal/verification/compiler.TestRFLSCR2VS04_A11", "VS04-A12": "codeflow/internal/verification/compiler.TestRFLSCR2VS04_A12",
		"VS04-A13": "codeflow/internal/verification/compiler.TestRFLSCR2VS04_A13", "VS04-A14": "codeflow/internal/verification/compiler.TestRFLSCR2VS04_A14",
	}
}

func vs04ExecutionRecord(t *testing.T, shared compiler.Evidence) compilerEvidence {
	t.Helper()
	return compilerEvidence{
		Criterion:             shared.Criterion,
		ImplementationTestID:  shared.ImplementationTestID,
		ImplementationPackage: shared.ImplementationPackage,
		ExecutionPackage:      compilerExecutionPackage,
		ExecutionBinary:       filepath.Base(os.Args[0]),
		ExecutionID:           t.Name(),
		SnapshotTreeDigest:    shared.SnapshotTreeDigest,
		ObjectRefs:            append([]string(nil), shared.ObjectRefs...),
	}
}

func validateVS04EvidenceRecord(criterion, executionID string, record compilerEvidence, expectedImplementation map[string]string) error {
	if record.Criterion != criterion {
		return fmt.Errorf("criterion identity mismatch: got %q want %q", record.Criterion, criterion)
	}
	if record.ImplementationTestID != expectedImplementation[criterion] || record.ImplementationPackage != compilerImplementationPackage {
		return fmt.Errorf("implementation identity mismatch: %+v", record)
	}
	if record.ExecutionPackage != compilerExecutionPackage || record.ExecutionBinary == "" || record.ExecutionID != executionID {
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
	if record.SnapshotTreeDigest == "" || len(record.ObjectRefs) < 3 {
		return fmt.Errorf("immutable artifact evidence is incomplete: %+v", record)
	}
	seen := make(map[string]bool, len(record.ObjectRefs))
	hasSnapshot, hasTree, hasBasis := false, false, false
	for _, ref := range record.ObjectRefs {
		parts := strings.SplitN(ref, ":", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" || seen[ref] {
			return fmt.Errorf("invalid or duplicate immutable artifact ref %q", ref)
		}
		seen[ref] = true
		switch parts[0] {
		case "snapshot":
			hasSnapshot = true
		case "tree":
			if parts[1] != record.SnapshotTreeDigest {
				return fmt.Errorf("tree artifact ref %q does not match snapshotTreeDigest %q", ref, record.SnapshotTreeDigest)
			}
			hasTree = true
		case "basis":
			hasBasis = true
		}
	}
	if !hasSnapshot || !hasTree || !hasBasis {
		return fmt.Errorf("snapshot, tree and basis artifact refs are required: %+v", record.ObjectRefs)
	}
	return nil
}

func mustJSONVS04(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprintf("<json error: %v>", err)
	}
	return string(data)
}

func TestVS04EvidenceRegistryRejectsFabricatedPass(t *testing.T) {
	record := compilerEvidence{
		Criterion:             "VS04-A1",
		ImplementationTestID:  "codeflow/internal/verification/compiler.TestRFLSCR2VS04_A01",
		ImplementationPackage: compilerImplementationPackage,
		ExecutionPackage:      compilerExecutionPackage,
		ExecutionBinary:       "contractharness.test",
		ExecutionID:           "fake-execution",
		Result:                "pass",
		ExecutionCompleted:    false,
		SnapshotTreeDigest:    "tree-vs04",
		ObjectRefs:            []string{"snapshot:snapshot-vs04", "tree:tree-vs04", "basis:basis-vs04"},
	}
	if err := validateVS04EvidenceRecord(record.Criterion, record.ExecutionID, record, expectedVS04ImplementationIDs()); err == nil {
		t.Fatal("fabricated pass without completed production execution was accepted")
	}
}
