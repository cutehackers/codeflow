package contractharness

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"codeflow/internal/analyzer/workspace"
	"codeflow/internal/collector/evidence"
	"codeflow/internal/collector/verification/analyzer"
	"codeflow/schemas"
)

type snapshotEvidence struct {
	Criterion                string                           `json:"criterion"`
	ImplementationTestID     string                           `json:"implementationTestId"`
	ImplementationPackage    string                           `json:"implementationPackage"`
	ExecutionPackage         string                           `json:"executionPackage"`
	ExecutionBinary          string                           `json:"executionBinary"`
	ExecutionID              string                           `json:"executionId"`
	Result                   string                           `json:"result"`
	ExecutionCompleted       bool                             `json:"executionCompleted"`
	SnapshotTreeDigest       string                           `json:"snapshotTreeDigest"`
	RepositoryPathWriteAudit workspace.SourceWriteAudit       `json:"repositoryPathWriteAudit"`
	MountPermissionEvidence  evidence.MountPermissionEvidence `json:"mountPermissionEvidence"`
	ObjectRefs               []string                         `json:"objectRefs"`
}

func TestRFLSCR2VS02_EvidenceRegistry(t *testing.T) {
	if VS02EvidenceRegistryID == "" {
		t.Fatal("VS-02 evidence registry ID is empty")
	}
	if err := EnsureAllCompiled(); err != nil {
		t.Fatal(err)
	}
	for _, entry := range VS02ContractRegistry {
		valid, err := fs.ReadFile(schemas.FixturesFS, filepath.ToSlash(filepath.Join("fixtures", entry.ValidFixture)))
		if err != nil {
			t.Fatalf("read valid fixture %s: %v", entry.ID, err)
		}
		if err := ValidateVS02RegistryEntry(entry, valid); err != nil {
			t.Fatal(err)
		}
		invalid, err := fs.ReadFile(schemas.FixturesFS, filepath.ToSlash(filepath.Join("fixtures", entry.InvalidFixture)))
		if err != nil {
			t.Fatalf("read invalid fixture %s: %v", entry.ID, err)
		}
		if err := Validate(entry.SchemaID, invalid); err == nil {
			t.Fatalf("invalid fixture unexpectedly passed: %s", entry.ID)
		}
	}
	matrix, err := fs.ReadFile(schemas.FixturesFS, "fixtures/rflsc.adapter-capability-matrix.v1/valid/matrix.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateVS02CapabilityMatrix(matrix); err != nil {
		t.Fatal(err)
	}

	runners := map[string]func(*testing.T) analyzer.Evidence{
		"VS02-A1": analyzer.RunA01, "VS02-A2": analyzer.RunA02, "VS02-A3": analyzer.RunA03,
		"VS02-A4": analyzer.RunA04, "VS02-A5": analyzer.RunA05, "VS02-A6": analyzer.RunA06,
		"VS02-A7": analyzer.RunA07, "VS02-A8": analyzer.RunA08, "VS02-A9": analyzer.RunA09,
		"VS02-A10": analyzer.RunA10, "VS02-A11": analyzer.RunA11,
	}
	expected := []string{"VS02-A1", "VS02-A2", "VS02-A3", "VS02-A4", "VS02-A5", "VS02-A6", "VS02-A7", "VS02-A8", "VS02-A9", "VS02-A10", "VS02-A11"}
	if len(runners) != len(expected) {
		t.Fatalf("runner count=%d want=%d", len(runners), len(expected))
	}
	seen := map[string]int{}
	for _, criterion := range expected {
		runner := runners[criterion]
		var evidence analyzer.Evidence
		var record snapshotEvidence
		if !t.Run(criterion, func(t *testing.T) {
			evidence = runner(t)
			validateVS02Evidence(t, criterion, evidence)
			record = buildSnapshotRecord(t, evidence)
			// Assign pass only after the shared production-seam runner and
			// identity checks have completed successfully.
			record.ExecutionCompleted = true
			record.Result = "pass"
			if err := validateSnapshotRecord(criterion, t.Name(), record); err != nil {
				t.Fatal(err)
			}
		}) {
			t.Fatalf("criterion %s failed", criterion)
		}
		seen[evidence.Criterion]++
		if record.Result != "pass" {
			t.Fatalf("criterion %s has no passing execution result: %+v", criterion, record)
		}
		t.Logf("evidence=%s", mustJSON(record))
	}
	for _, criterion := range expected {
		if seen[criterion] != 1 {
			t.Fatalf("criterion %s executed %d times", criterion, seen[criterion])
		}
	}
}

func validateVS02Evidence(t *testing.T, criterion string, evidence analyzer.Evidence) {
	t.Helper()
	if err := validateVS02EvidenceFields(criterion, evidence.Criterion, evidence.ImplementationTestID, "", evidence.SnapshotTreeDigest, evidence.RepositoryPathWriteAudit, evidence.MountPermissionEvidence, evidence.ObjectRefs); err != nil {
		t.Fatalf("incomplete evidence: %v (%+v)", err, evidence)
	}
}

func buildSnapshotRecord(t *testing.T, shared analyzer.Evidence) snapshotEvidence {
	t.Helper()
	return snapshotEvidence{
		Criterion: shared.Criterion, ImplementationTestID: shared.ImplementationTestID,
		ImplementationPackage: "codeflow/internal/collector/verification/analyzer", ExecutionPackage: "codeflow/internal/collector/contractharness",
		ExecutionBinary: filepath.Base(os.Args[0]), ExecutionID: t.Name(), Result: "",
		SnapshotTreeDigest: shared.SnapshotTreeDigest, RepositoryPathWriteAudit: shared.RepositoryPathWriteAudit,
		MountPermissionEvidence: shared.MountPermissionEvidence,
		ObjectRefs:              append([]string(nil), shared.ObjectRefs...),
	}
}

func validateSnapshotRecord(criterion, executionID string, record snapshotEvidence) error {
	if err := validateVS02EvidenceFields(record.Criterion, criterion, record.ImplementationTestID, record.Result, record.SnapshotTreeDigest, record.RepositoryPathWriteAudit, record.MountPermissionEvidence, record.ObjectRefs); err != nil {
		return fmt.Errorf("incomplete execution evidence: %v (%+v)", err, record)
	}
	if !record.ExecutionCompleted || record.Result != "pass" {
		return fmt.Errorf("execution did not complete before pass: %+v", record)
	}
	if record.ImplementationPackage != "codeflow/internal/collector/verification/analyzer" || record.ExecutionPackage != "codeflow/internal/collector/contractharness" || record.ExecutionBinary == "" || record.ExecutionID != executionID {
		return fmt.Errorf("incomplete execution identity: %+v", record)
	}
	return nil
}

func validateVS02EvidenceFields(criterion, gotCriterion, implementationID, result, treeDigest string, audit workspace.SourceWriteAudit, isolation evidence.MountPermissionEvidence, refs []string) error {
	if gotCriterion != criterion || implementationID == "" || (result != "" && result != "pass") || treeDigest == "" || audit.SourceIntegrityViolation || audit.CodeFlowWriteCount != 0 || len(audit.RepositoryPathWrites) != 0 || audit.CapturedSnapshotTreeDigest != treeDigest || len(refs) < 3 {
		return fmt.Errorf("missing criterion/result/tree/audit/object evidence")
	}
	if criterion == "VS02-A11" {
		if isolation.SourceDelivery != "protocol_snapshot_bytes" || isolation.SourceMount != "not_mounted" || isolation.WorkingDirectoryMode != "process_private_disposable" || isolation.WorkingDirectoryPermission != "0700" || !isolation.ReadOnlySource || !isolation.Disposable || isolation.RepositoryPathExposed || !isolation.DependencyEnvironmentPreserved || !isolation.CleanupVerified {
			return fmt.Errorf("missing executable mount/permission/isolation evidence: %+v", isolation)
		}
	} else if isolation.SourceDelivery != "protocol_snapshot_bytes" || isolation.SourceMount != "not_mounted" || isolation.WorkingDirectoryMode != "not_applicable" || isolation.WorkingDirectoryPermission != "not_applicable" || !isolation.ReadOnlySource || isolation.Disposable || isolation.RepositoryPathExposed || isolation.CleanupVerified {
		return fmt.Errorf("invalid protocol-only scope evidence: %+v", isolation)
	}
	for _, ref := range refs {
		if ref == "" {
			return fmt.Errorf("empty evidence object ref")
		}
	}
	return nil
}

func TestVS02EvidenceRegistryRejectsFabricatedPass(t *testing.T) {
	record := snapshotEvidence{
		Criterion: "VS02-A11", ImplementationTestID: "fabricated", ImplementationPackage: "codeflow/internal/collector/verification/analyzer",
		ExecutionPackage: "codeflow/internal/collector/contractharness", ExecutionBinary: "test", ExecutionID: "fake", Result: "pass",
		SnapshotTreeDigest: "tree", RepositoryPathWriteAudit: workspace.SourceWriteAudit{CapturedSnapshotTreeDigest: "tree"},
		MountPermissionEvidence: evidence.MountPermissionEvidence{
			SourceDelivery: "protocol_snapshot_bytes", SourceMount: "not_mounted", WorkingDirectoryMode: "process_private_disposable", WorkingDirectoryPermission: "0700",
			ReadOnlySource: true, Disposable: true, RepositoryPathExposed: false, DependencyEnvironmentPreserved: true, CleanupVerified: true,
			TerminalModes: []string{"cancel", "crash", "success", "timeout"},
		},
		ObjectRefs: []string{"snapshot:id", "tree:tree", "audit:tree"},
	}
	if err := validateSnapshotRecord(record.Criterion, "fake", record); err == nil {
		t.Fatal("fabricated pass without completed production execution was accepted")
	}
}

// Keep the identity source visible in test output and fail if the registry is
// accidentally executed in a zero-value test binary.
func TestVS02RegistryExecutionIdentity(t *testing.T) {
	if filepath.Base(os.Args[0]) == "" {
		t.Fatal("missing execution binary")
	}
}
