package semantic

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"codeflow/internal/evidence"
	"codeflow/internal/fusion"
	"codeflow/internal/protocol"
	"codeflow/internal/slicing"
)

// EvidenceIDForAnchor is the shared namespace for compiler and evidence
// extraction. It is content-anchor based, not ordinal based.
func EvidenceIDForAnchor(flowID string, anchor slicing.Anchor) string {
	raw := strings.Join([]string{flowID, anchor.RepoRelativePath, anchor.EnclosingSymbolPath, anchor.CanonicalAstFingerprint, anchor.FileHash, anchor.SpanHash, fmt.Sprint(anchor.ByteRange[0]), fmt.Sprint(anchor.ByteRange[1])}, "\x00")
	h := sha256.Sum256([]byte(raw))
	return "evidence-" + hex.EncodeToString(h[:])[:24]
}

// EvidenceRecord represents a validated, secret-redacted source or test evidence item.
type EvidenceRecord struct {
	EvidenceID         string           `json:"evidenceId"`
	Kind               string           `json:"kind"`            // source | compiler | test | runtime
	SourceAuthority    string           `json:"sourceAuthority"` // code | test | contract | runtime
	SnapshotID         string           `json:"snapshotId,omitempty"`
	ComputedBasisID    string           `json:"computedBasisId,omitempty"`
	DocumentRevisionID string           `json:"documentRevisionId,omitempty"`
	Anchor             slicing.Anchor   `json:"anchor"`
	CodeLens           *fusion.CodeLens `json:"codeLens,omitempty"`
	Snippet            string           `json:"snippet,omitempty"`
	ValidationStatus   string           `json:"validationStatus"`
	RedactionStatus    string           `json:"redactionStatus"`
}

// ExtractAndRedactEvidence extracts code evidence through one captured
// snapshot. It is retained as a compatibility boundary for callers that have
// not yet threaded a snapshot through their whole request. Production callers
// should use ExtractAndRedactEvidenceFromSnapshot so harvest, slicing, and
// evidence all share the same immutable bytes.
func ExtractAndRedactEvidence(target *ResolvedTarget, payload *slicing.SlicedPayload, repoRoot string) ([]EvidenceRecord, error) {
	if repoRoot == "" {
		return nil, fmt.Errorf("repoRoot is required only for the compatibility snapshot boundary")
	}
	snapshot, err := protocol.CaptureSnapshot(repoRoot, 0)
	if err != nil {
		return nil, fmt.Errorf("capture evidence snapshot: %w", err)
	}
	input, err := snapshot.AnalyzerInput()
	if err != nil {
		return nil, fmt.Errorf("build evidence snapshot input: %w", err)
	}
	return ExtractAndRedactEvidenceFromSnapshot(target, payload, input)
}

// ExtractAndRedactEvidenceFromSnapshot promotes only evidence ranges that
// were validated against the supplied immutable snapshot bytes. Missing
// documents, stale revisions, invalid ranges, and traversal paths are typed
// failures from evidence.ExtractEvidence. Descriptions are never used as
// source evidence.
func ExtractAndRedactEvidenceFromSnapshot(target *ResolvedTarget, payload *slicing.SlicedPayload, snapshot evidence.SnapshotInput) ([]EvidenceRecord, error) {
	if target == nil {
		return nil, fmt.Errorf("target cannot be nil")
	}
	if payload == nil {
		return nil, fmt.Errorf("payload cannot be nil")
	}

	anchors := make([]evidence.EvidenceAnchor, 0, len(payload.Steps))
	for _, step := range payload.Steps {
		relPath := step.Anchor.RepoRelativePath
		doc, ok := snapshot.Document(relPath)
		if !ok {
			return nil, &evidence.EvidenceError{Code: "unknown_file", Path: relPath, Detail: "file is not in selected snapshot"}
		}
		if strings.TrimSpace(step.Anchor.FileHash) == "" || strings.TrimSpace(step.Anchor.SpanHash) == "" {
			return nil, &evidence.EvidenceError{Code: "invalid_anchor", Path: relPath, Detail: "fileHash and spanHash are required for verified evidence"}
		}
		anchors = append(anchors, evidence.EvidenceAnchor{
			EvidenceID: EvidenceIDForAnchor(target.FlowID, step.Anchor),
			Path:       relPath,
			RevisionID: doc.RevisionID,
			FileHash:   step.Anchor.FileHash,
			SpanHash:   step.Anchor.SpanHash,
			StartByte:  step.Anchor.ByteRange[0],
			EndByte:    step.Anchor.ByteRange[1],
		})
	}

	validated, err := evidence.ExtractEvidence(snapshot, anchors)
	if err != nil {
		return nil, err
	}
	records := make([]EvidenceRecord, 0, len(validated))
	for i, evidence := range validated {
		step := payload.Steps[i]
		viewStart := evidence.LineRange[0] - 4
		if viewStart < 1 {
			viewStart = 1
		}
		redactionStatus := evidence.RedactionStatus
		if redactionStatus == "clean" || redactionStatus == "redacted" {
			// Preserve the legacy semantic response value while the v2
			// evidence contract retains its typed clean/redacted status.
			redactionStatus = "passed"
		}
		records = append(records, EvidenceRecord{
			EvidenceID:         evidence.EvidenceID,
			Kind:               "source",
			SourceAuthority:    "code",
			SnapshotID:         evidence.SnapshotID,
			ComputedBasisID:    evidence.ComputedBasisID,
			DocumentRevisionID: evidence.DocumentRevisionID,
			Anchor:             step.Anchor,
			CodeLens: &fusion.CodeLens{
				Path:          evidence.Path,
				StartLine:     evidence.LineRange[0],
				EndLine:       evidence.LineRange[1],
				ViewStartLine: viewStart,
				ViewEndLine:   evidence.LineRange[1] + 10,
			},
			Snippet:          evidence.Snippet,
			ValidationStatus: evidence.ValidationStatus,
			RedactionStatus:  redactionStatus,
		})
	}
	return records, nil
}

// ExtractAndRedactEvidenceFromProtocolSnapshot is a small adapter for the
// protocol snapshot used by Core orchestration. Conversion copies the already
// captured bytes and performs no filesystem reads.
func ExtractAndRedactEvidenceFromProtocolSnapshot(target *ResolvedTarget, payload *slicing.SlicedPayload, snapshot protocol.Snapshot) ([]EvidenceRecord, error) {
	input, err := snapshot.AnalyzerInput()
	if err != nil {
		return nil, fmt.Errorf("build evidence snapshot input: %w", err)
	}
	return ExtractAndRedactEvidenceFromSnapshot(target, payload, input)
}
