package contractharness

import (
	"encoding/json"
	"fmt"
)

// ValidateDocumentRevision validates raw JSON against document-revision.schema.json.
func ValidateDocumentRevision(data []byte) error {
	schemaID := BaseURL + "document-revision.schema.json"
	if err := Validate(schemaID, data); err != nil {
		return fmt.Errorf("document-revision schema violation: %w", err)
	}
	var rev struct {
		DocumentVersion int    `json:"documentVersion"`
		Path            string `json:"path"`
		ContentID       string `json:"contentId"`
	}
	if err := json.Unmarshal(data, &rev); err != nil {
		return fmt.Errorf("parse document-revision JSON: %w", err)
	}
	if rev.DocumentVersion < 1 {
		return fmt.Errorf("document-revision: documentVersion must be >= 1, got %d", rev.DocumentVersion)
	}
	if rev.Path == "" {
		return fmt.Errorf("document-revision: path must not be empty")
	}
	if len(rev.ContentID) != 64 {
		return fmt.Errorf("document-revision: contentId must be 64-char hex SHA256")
	}
	return nil
}

// ValidateWorkspaceSnapshot validates raw JSON against workspace-snapshot.schema.json.
func ValidateWorkspaceSnapshot(data []byte) error {
	schemaID := BaseURL + "workspace-snapshot.schema.json"
	if err := Validate(schemaID, data); err != nil {
		return fmt.Errorf("workspace-snapshot schema violation: %w", err)
	}
	var snap struct {
		Sequence        int               `json:"sequence"`
		ComputedBasisID string            `json:"computedBasisId"`
		WorkspaceEpoch  string            `json:"workspaceEpoch"`
		Entries         map[string]any    `json:"entries"`
	}
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("parse workspace-snapshot JSON: %w", err)
	}
	if snap.Sequence < 1 {
		return fmt.Errorf("workspace-snapshot: sequence must be >= 1, got %d", snap.Sequence)
	}
	if len(snap.ComputedBasisID) != 64 {
		return fmt.Errorf("workspace-snapshot: computedBasisId must be 64-char hex SHA256")
	}
	if snap.WorkspaceEpoch == "" {
		return fmt.Errorf("workspace-snapshot: workspaceEpoch must not be empty")
	}
	return nil
}

// ValidateChangeBatch validates raw JSON against change-batch.schema.json.
func ValidateChangeBatch(data []byte) error {
	schemaID := BaseURL + "change-batch.schema.json"
	if err := Validate(schemaID, data); err != nil {
		return fmt.Errorf("change-batch schema violation: %w", err)
	}
	var batch struct {
		BatchID   string   `json:"batchId"`
		Revisions []string `json:"revisions"`
		Status    string   `json:"status"`
	}
	if err := json.Unmarshal(data, &batch); err != nil {
		return fmt.Errorf("parse change-batch JSON: %w", err)
	}
	if batch.BatchID == "" {
		return fmt.Errorf("change-batch: batchId must not be empty")
	}
	if len(batch.Revisions) == 0 {
		return fmt.Errorf("change-batch: revisions must not be empty")
	}
	if batch.Status != "open" && batch.Status != "committed" && batch.Status != "aborted" {
		return fmt.Errorf("change-batch: invalid status %q", batch.Status)
	}
	return nil
}
