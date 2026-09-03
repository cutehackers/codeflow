package storage

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var (
	// ErrCASConflict is returned when active pointer compare-and-swap fails due to
	// liveHead or previous generation mismatch (Raw §6.9, §10.11, INV-10).
	ErrCASConflict = errors.New("active pointer CAS conflict: expected liveHead or previous generation mismatch")

	casLock sync.Mutex
)

// GenerationProofManifest represents the canonical proof manifest for a published generation.
type GenerationProofManifest struct {
	SchemaID                       string                   `json:"schemaId"`
	SchemaVersion                  int                      `json:"schemaVersion"`
	ProofID                        string                   `json:"proofId"`
	GenerationID                   string                   `json:"generationId"`
	ComputedBasisID                string                   `json:"computedBasisId"`
	ValidatedAgainstSnapshotID     string                   `json:"validatedAgainstSnapshotId"`
	ValidatedWorkspaceDeltaID      *string                  `json:"validatedWorkspaceDeltaId,omitempty"`
	TaskIntentRevision             int                      `json:"taskIntentRevision"`
	NormalizedQueryHash            string                   `json:"normalizedQueryHash"`
	AnalysisReadSetID              string                   `json:"analysisReadSetId"`
	CausalObservationClosureID     string                   `json:"causalObservationClosureId"`
	CausalObservationClosureDigest string                   `json:"causalObservationClosureDigest,omitempty"`
	CapabilityProfileDigest        string                   `json:"capabilityProfileDigest,omitempty"`
	CurrentPublication             CurrentPublicationResult `json:"currentPublication"`
	SettlementEvaluation           SettlementEvaluation     `json:"settlementEvaluation"`
	ArtifactRefs                   ArtifactRefs             `json:"artifactRefs"`
	ExpectedLiveHeadSnapshotID     string                   `json:"expectedLiveHeadSnapshotId"`
	ExpectedPreviousGenerationID   *string                  `json:"expectedPreviousGenerationId,omitempty"`
	PublishedAt                    time.Time                `json:"publishedAt"`
}

// CurrentPublicationResult captures the 6 subgate outcomes for Current Publication Gate.
type CurrentPublicationResult struct {
	Eligibility           string `json:"eligibility"`           // passed | rejected
	SnapshotGate          string `json:"snapshotGate"`          // passed | failed
	ClosureGate           string `json:"closureGate"`           // passed | failed
	EvidenceGate          string `json:"evidenceGate"`          // passed | failed
	SemanticAtomicityGate string `json:"semanticAtomicityGate"` // passed | failed
	TaskRelevanceGate     string `json:"taskRelevanceGate"`     // passed | failed
	ComprehensionGate     string `json:"comprehensionGate"`     // passed | failed
}

// SettlementEvaluation captures settlement status.
type SettlementEvaluation struct {
	Gate                   string     `json:"gate"` // pending | passed | failed
	EvaluatedAt            *time.Time `json:"evaluatedAt,omitempty"`
	BlockingObligationRefs []string   `json:"blockingObligationRefs"`
}

// ArtifactRefs holds content-addressed references for canonical artifacts.
type ArtifactRefs struct {
	SemanticMap   string `json:"semanticMap"`
	SemanticDelta string `json:"semanticDelta,omitempty"`
	EvidenceIndex string `json:"evidenceIndex,omitempty"`
	Projection    string `json:"projection,omitempty"`
}

// ActivePointer represents the atomic active pointer with CAS fields (Raw §10.11).
type ActivePointer struct {
	SchemaID                     string    `json:"schemaId"`
	SchemaVersion                int       `json:"schemaVersion"`
	GenerationID                 string    `json:"generationId"`
	ManifestObjectRef            string    `json:"manifestObjectRef"`
	PublishedAt                  time.Time `json:"publishedAt"`
	ComputedBasisID              string    `json:"computedBasisId"`
	ValidatedAgainstSnapshotID   string    `json:"validatedAgainstSnapshotId"`
	ExpectedLiveHeadSnapshotID   string    `json:"expectedLiveHeadSnapshotId"`
	ExpectedPreviousGenerationID *string   `json:"expectedPreviousGenerationId,omitempty"`
	WorkspaceEpoch               string    `json:"workspaceEpoch"`
	TaskIntentRevision           int       `json:"taskIntentRevision"`
	NormalizedQueryHash          string    `json:"normalizedQueryHash"`
	FlowCount                    int       `json:"flowCount"`
}

// ReadActivePointer loads active-pointer.json, or returns nil if none exists yet.
func (s *Storage) ReadActivePointer() (*ActivePointer, error) {
	path := filepath.Join(s.baseDir, "active-pointer.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read active pointer: %w", err)
	}
	var p ActivePointer
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("unmarshal active pointer: %w", err)
	}
	return &p, nil
}

// CompareAndSwapActivePointer atomically updates the active pointer if and only if
// expectedLiveHeadSnapshotID matches the current state and expectedPreviousGenerationID matches
// the current active generation ID (Raw §6.9, §10.11, INV-10).
func (s *Storage) CompareAndSwapActivePointer(expectedLiveHeadSnapshotID, expectedPreviousGenerationID string, newPointer *ActivePointer) error {
	casLock.Lock()
	defer casLock.Unlock()

	current, err := s.ReadActivePointer()
	if err != nil {
		return err
	}

	if current == nil {
		if expectedPreviousGenerationID != "" && expectedPreviousGenerationID != "*" {
			return ErrCASConflict
		}
	} else {
		if expectedPreviousGenerationID == "" {
			return ErrCASConflict
		}
		if expectedPreviousGenerationID != "*" && current.GenerationID != expectedPreviousGenerationID {
			return ErrCASConflict
		}
		if expectedLiveHeadSnapshotID != "" && current.ExpectedLiveHeadSnapshotID != "" && current.ExpectedLiveHeadSnapshotID != expectedLiveHeadSnapshotID {
			return ErrCASConflict
		}
	}

	if newPointer.PublishedAt.IsZero() {
		newPointer.PublishedAt = time.Now().UTC()
	}
	if newPointer.SchemaID == "" {
		newPointer.SchemaID = "https://codeflow.local/schemas/active-pointer.schema.json"
	}
	if newPointer.SchemaVersion == 0 {
		newPointer.SchemaVersion = 1
	}

	data, err := json.MarshalIndent(newPointer, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal active pointer: %w", err)
	}

	path := filepath.Join(s.baseDir, "active-pointer.json")
	if err := atomicWrite(path, data); err != nil {
		return fmt.Errorf("write active pointer: %w", err)
	}

	// Backward compatibility: also sync to legacy pointer.json
	ptr := Pointer{
		GenerationID:     newPointer.GenerationID,
		PublishedAt:      newPointer.PublishedAt,
		BasisFingerprint: newPointer.ComputedBasisID,
		FlowCount:        newPointer.FlowCount,
	}
	ptrData, _ := json.MarshalIndent(ptr, "", "  ")
	_ = atomicWrite(filepath.Join(s.baseDir, "pointer.json"), ptrData)

	return nil
}

// WriteManifestCAS writes a GenerationProofManifest to CAS storage and returns its reference.
func (s *Storage) WriteManifestCAS(manifest *GenerationProofManifest) (string, error) {
	if manifest.PublishedAt.IsZero() {
		manifest.PublishedAt = time.Now().UTC()
	}
	if manifest.SchemaID == "" {
		manifest.SchemaID = "https://codeflow.local/schemas/generation-proof-manifest.schema.json"
	}
	if manifest.SchemaVersion == 0 {
		manifest.SchemaVersion = 1
	}

	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal manifest: %w", err)
	}

	h := sha256.Sum256(data)
	hashHex := hex.EncodeToString(h[:])

	casDir := filepath.Join(s.baseDir, "cas")
	if err := os.MkdirAll(casDir, 0o755); err != nil {
		return "", fmt.Errorf("create cas dir: %w", err)
	}

	targetPath := filepath.Join(casDir, hashHex+".json")
	if err := atomicWrite(targetPath, data); err != nil {
		return "", fmt.Errorf("write manifest cas: %w", err)
	}

	return "cas:sha256:" + hashHex, nil
}

// ReadManifestCAS retrieves a GenerationProofManifest from CAS.
func (s *Storage) ReadManifestCAS(casRef string) (*GenerationProofManifest, error) {
	hashHex := strings.TrimPrefix(casRef, "cas:sha256:")
	hashHex = strings.TrimPrefix(hashHex, "cas:")
	if len(hashHex) != 64 {
		return nil, fmt.Errorf("invalid manifest cas ref: %q", casRef)
	}

	targetPath := filepath.Join(s.baseDir, "cas", hashHex+".json")
	data, err := os.ReadFile(targetPath)
	if err != nil {
		return nil, fmt.Errorf("read manifest cas %s: %w", targetPath, err)
	}

	var m GenerationProofManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("unmarshal manifest cas: %w", err)
	}
	return &m, nil
}

// ReadActiveProofManifest loads the GenerationProofManifest currently pointed to by active-pointer.json.
func (s *Storage) ReadActiveProofManifest() (*GenerationProofManifest, error) {
	ptr, err := s.ReadActivePointer()
	if err != nil {
		return nil, err
	}
	if ptr == nil || ptr.ManifestObjectRef == "" {
		return nil, nil
	}
	return s.ReadManifestCAS(ptr.ManifestObjectRef)
}
