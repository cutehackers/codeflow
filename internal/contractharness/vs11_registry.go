package contractharness

import (
	"fmt"
	"strings"

	"codeflow/internal/rflscvs11evidence"
)

// VS11EvidenceRegistryID is the stable evidence registry identity from the
// approved VS-11 contract.
const VS11EvidenceRegistryID = rflscvs11evidence.VS11EvidenceRegistryID

var VS11EvidenceCriteria = rflscvs11evidence.VS11EvidenceCriteria

// ValidateVS11EvidenceRecord verifies all invariant requirements for a single
// execution record from the VS-11 evidence registry.
func ValidateVS11EvidenceRecord(criterion, executionID string, record rflscvs11evidence.Record, expectedPkg, expectedBinary string) error {
	if record.Criterion != criterion {
		return fmt.Errorf("criterion identity mismatch: got %q want %q", record.Criterion, criterion)
	}
	if record.Package != expectedPkg {
		return fmt.Errorf("package identity mismatch: got %q want %q", record.Package, expectedPkg)
	}
	if expectedBinary != "" && record.Binary != expectedBinary {
		return fmt.Errorf("binary identity mismatch: got %q want %q", record.Binary, expectedBinary)
	}
	if strings.TrimSpace(record.BinaryDigest) == "" {
		return fmt.Errorf("missing binary digest: %+v", record)
	}
	if record.ExecutionID != executionID {
		return fmt.Errorf("execution identity mismatch: got %q want %q", record.ExecutionID, executionID)
	}
	if record.SnapshotID == "" || record.SnapshotTreeDigest == "" {
		return fmt.Errorf("immutable snapshot identity is incomplete: %+v", record)
	}
	if len(record.Artifacts) < 2 {
		return fmt.Errorf("insufficient immutable artifacts: count=%d want>=2", len(record.Artifacts))
	}
	seenRefs := make(map[string]bool, len(record.Artifacts))
	for _, art := range record.Artifacts {
		if strings.TrimSpace(art.Role) == "" || strings.TrimSpace(art.Ref) == "" || len(art.Bytes) == 0 {
			return fmt.Errorf("invalid artifact in record: %+v", art)
		}
		if seenRefs[art.Ref] {
			return fmt.Errorf("duplicate artifact ref: %q", art.Ref)
		}
		seenRefs[art.Ref] = true
	}
	return nil
}
