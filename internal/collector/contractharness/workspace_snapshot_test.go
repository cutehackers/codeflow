package contractharness

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWorkspaceSnapshotValidators(t *testing.T) {
	schemasDir := SchemasDir()
	if schemasDir == "" {
		t.Skip("schemas directory not found")
	}

	// 1. DocumentRevision
	validRev, err := os.ReadFile(filepath.Join(schemasDir, "fixtures/document-revision/valid/agent-revision.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateDocumentRevision(validRev); err != nil {
		t.Errorf("expected valid DocumentRevision to pass, got %v", err)
	}

	invalidRev, err := os.ReadFile(filepath.Join(schemasDir, "fixtures/document-revision/invalid/missing-source.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateDocumentRevision(invalidRev); err == nil {
		t.Error("expected invalid DocumentRevision to fail")
	}

	// 2. WorkspaceSnapshot
	validSnap, err := os.ReadFile(filepath.Join(schemasDir, "fixtures/workspace-snapshot/valid/snapshot.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateWorkspaceSnapshot(validSnap); err != nil {
		t.Errorf("expected valid WorkspaceSnapshot to pass, got %v", err)
	}

	invalidSnap, err := os.ReadFile(filepath.Join(schemasDir, "fixtures/workspace-snapshot/invalid/negative-sequence.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateWorkspaceSnapshot(invalidSnap); err == nil {
		t.Error("expected invalid WorkspaceSnapshot to fail")
	}

	// 3. ChangeBatch
	validBatch, err := os.ReadFile(filepath.Join(schemasDir, "fixtures/change-batch/valid/committed-batch.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateChangeBatch(validBatch); err != nil {
		t.Errorf("expected valid ChangeBatch to pass, got %v", err)
	}

	invalidBatch, err := os.ReadFile(filepath.Join(schemasDir, "fixtures/change-batch/invalid/empty-revisions.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateChangeBatch(invalidBatch); err == nil {
		t.Error("expected invalid ChangeBatch to fail")
	}
}
