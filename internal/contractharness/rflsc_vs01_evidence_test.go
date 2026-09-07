package contractharness

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"codeflow/internal/rflscvs01"
	"codeflow/internal/workspace"
	"codeflow/schemas"
)

const (
	vs01ImplementationPackage = "codeflow/internal/workspace"
	vs01ExecutionPackage      = "codeflow/internal/contractharness"
)

// vs01Evidence records both identities involved in registry execution. The
// implementation ID points to the named workspace test that owns the shared
// criterion behavior. Execution fields identify the registry subtest and
// binary that actually ran that behavior in this process.
type vs01Evidence struct {
	Criterion                string                     `json:"criterion"`
	TestName                 string                     `json:"testName"`
	Package                  string                     `json:"package"`
	ImplementationTestID     string                     `json:"implementationTestId"`
	ExecutionPackage         string                     `json:"executionPackage"`
	ExecutionBinary          string                     `json:"executionBinary"`
	ExecutionID              string                     `json:"executionId"`
	Result                   string                     `json:"result"`
	SnapshotTreeDigest       string                     `json:"snapshotTreeDigest"`
	RepositoryPathWriteAudit workspace.SourceWriteAudit `json:"repositoryPathWriteAudit"`
	ObjectRefs               []string                   `json:"objectRefs"`
}

func TestRFLSCR2VS01_EvidenceRegistry(t *testing.T) {
	if VS01EvidenceRegistryID == "" {
		t.Fatal("VS-01 evidence registry ID must be declared")
	}
	t.Logf("evidenceRegistry=%s", VS01EvidenceRegistryID)
	if err := EnsureAllCompiled(); err != nil {
		t.Fatalf("compile contract registry: %v", err)
	}
	for _, entry := range VS01ContractRegistry {
		valid, err := fs.ReadFile(schemas.FixturesFS, filepath.ToSlash(filepath.Join("fixtures", entry.ValidFixture)))
		if err != nil {
			t.Fatalf("read valid fixture %s: %v", entry.ID, err)
		}
		if err := ValidateVS01RegistryEntry(entry, valid); err != nil {
			t.Fatalf("valid fixture rejected for %s: %v", entry.ID, err)
		}
		for _, invalidFixture := range []string{entry.InvalidPathFixture, entry.InvalidEpochFixture, entry.InvalidMutationFixture} {
			invalid, readErr := fs.ReadFile(schemas.FixturesFS, filepath.ToSlash(filepath.Join("fixtures", invalidFixture)))
			if readErr != nil {
				t.Fatalf("read invalid fixture %s for %s: %v", invalidFixture, entry.ID, readErr)
			}
			if err := Validate(entry.SchemaID, invalid); err == nil {
				t.Fatalf("invalid fixture unexpectedly passed for %s: %s", entry.ID, invalidFixture)
			}
		}
	}

	expected := []string{"VS01-A1", "VS01-A2", "VS01-A3", "VS01-A4", "VS01-A5", "VS01-A6", "VS01-A7", "VS01-A8", "VS01-A9", "VS01-A10"}
	runners := map[string]func(*testing.T) rflscvs01.Evidence{
		"VS01-A1":  rflscvs01.RunA01,
		"VS01-A2":  rflscvs01.RunA02,
		"VS01-A3":  rflscvs01.RunA03,
		"VS01-A4":  rflscvs01.RunA04,
		"VS01-A5":  rflscvs01.RunA05,
		"VS01-A6":  rflscvs01.RunA06,
		"VS01-A7":  rflscvs01.RunA07,
		"VS01-A8":  rflscvs01.RunA08,
		"VS01-A9":  rflscvs01.RunA09,
		"VS01-A10": rflscvs01.RunA10,
	}
	if len(runners) != len(expected) {
		t.Fatalf("evidence registry runner count mismatch: got %d want %d", len(runners), len(expected))
	}
	records := make([]vs01Evidence, 0, len(expected))
	for _, criterion := range expected {
		runner, ok := runners[criterion]
		if !ok || runner == nil {
			t.Fatalf("missing evidence runner for %s", criterion)
		}
		var record vs01Evidence
		passed := t.Run(criterion, func(t *testing.T) {
			shared := runner(t)
			record = evidenceRecord(t, shared)
			validateEvidenceRecord(t, criterion, t.Name(), record)
		})
		if !passed || record.Criterion == "" {
			t.Fatalf("criterion %s was not executed successfully", criterion)
		}
		// This assignment occurs only after the shared public-seam runner and
		// all identity checks in the registry subtest returned successfully.
		record.Result = "pass"
		t.Logf("evidence=%s", mustJSON(record))
		records = append(records, record)
	}

	counts := make(map[string]int, len(records))
	for _, record := range records {
		counts[record.Criterion]++
	}
	if len(counts) != len(expected) {
		t.Fatalf("evidence registry has zero-match or unknown criteria: %+v", counts)
	}
	for _, criterion := range expected {
		if counts[criterion] != 1 {
			t.Fatalf("evidence registry requires exactly one executed record for %s, got %d", criterion, counts[criterion])
		}
	}
	for _, record := range records {
		if record.Result != "pass" {
			t.Fatalf("evidence registry recorded a non-passing execution for %s: %+v", record.Criterion, record)
		}
	}
}

func evidenceRecord(t *testing.T, shared rflscvs01.Evidence) vs01Evidence {
	t.Helper()
	return vs01Evidence{
		Criterion:                shared.Criterion,
		TestName:                 shared.ImplementationTestID,
		Package:                  vs01ImplementationPackage,
		ImplementationTestID:     shared.ImplementationTestID,
		ExecutionPackage:         vs01ExecutionPackage,
		ExecutionBinary:          filepath.Base(os.Args[0]),
		ExecutionID:              t.Name(),
		Result:                   "",
		SnapshotTreeDigest:       shared.SnapshotTreeDigest,
		RepositoryPathWriteAudit: shared.RepositoryPathWriteAudit,
		ObjectRefs:               append([]string(nil), shared.ObjectRefs...),
	}
}

func validateEvidenceRecord(t *testing.T, criterion, executionID string, record vs01Evidence) {
	t.Helper()
	expectedImplementation := map[string]string{
		"VS01-A1":  "codeflow/internal/workspace.TestRFLSCR2VS01_A01",
		"VS01-A2":  "codeflow/internal/workspace.TestRFLSCR2VS01_A02",
		"VS01-A3":  "codeflow/internal/workspace.TestRFLSCR2VS01_A03",
		"VS01-A4":  "codeflow/internal/workspace.TestRFLSCR2VS01_A04",
		"VS01-A5":  "codeflow/internal/workspace.TestRFLSCR2VS01_A05",
		"VS01-A6":  "codeflow/internal/workspace.TestRFLSCR2VS01_A06",
		"VS01-A7":  "codeflow/internal/workspace.TestRFLSCR2VS01_A07",
		"VS01-A8":  "codeflow/internal/workspace.TestRFLSCR2VS01_A08",
		"VS01-A9":  "codeflow/internal/workspace.TestRFLSCR2VS01_A09",
		"VS01-A10": "codeflow/internal/workspace.TestRFLSCR2VS01_A10",
	}
	if record.Criterion != criterion || record.TestName != expectedImplementation[criterion] ||
		record.Package != vs01ImplementationPackage || record.ImplementationTestID != expectedImplementation[criterion] ||
		record.ExecutionPackage != vs01ExecutionPackage || record.ExecutionBinary == "" || record.ExecutionID != executionID ||
		record.Result != "" || record.SnapshotTreeDigest == "" || len(record.ObjectRefs) == 0 {
		t.Fatalf("incomplete or inconsistent evidence for %s: %+v", criterion, record)
	}
	for index, ref := range record.ObjectRefs {
		if ref == "" {
			t.Fatalf("evidence object ref %d is empty for %s", index, criterion)
		}
	}
}

func mustJSON(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("<json error: %v>", err)
	}
	return string(data)
}
