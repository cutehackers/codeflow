// Package evidence provides approval test evidence serialization.
package evidence

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

const (
	ApprovalEvidencePrefix = "VS09_OBSERVED_EVIDENCE="
	VS09EvidencePrefix     = ApprovalEvidencePrefix
)

type ApprovalArtifact struct {
	Role  string `json:"role"`
	Ref   string `json:"ref"`
	Bytes []byte `json:"bytes"`
}

type ApprovalRecord struct {
	Challenge     string             `json:"challenge"`
	Criterion     string             `json:"criterion"`
	Package       string             `json:"package"`
	Binary        string             `json:"binary"`
	BinaryDigest  string             `json:"binaryDigest"`
	ExecutionID   string             `json:"executionId"`
	ObservationID int                `json:"observationId"`
	Artifacts     []ApprovalArtifact `json:"artifacts"`
}

var approvalObservations struct {
	sync.Mutex
	next map[string]int
}

var approvalBinaryIdentity struct {
	sync.Once
	digest string
	err    error
}

// ObserveApproval retains the actual bytes passed by the public behavioral test.
// The registry, not this producer, assigns pass after observing test2json completion.
func ObserveApproval(t *testing.T, pkg string, criteria []string, values map[string]any) {
	t.Helper()
	criterion := os.Getenv("CODEFLOW_VS09_CRITERION")
	if criterion == "" {
		return
	}
	matched := false
	for _, value := range criteria {
		if value == criterion {
			matched = true
		}
	}
	if !matched {
		return
	}
	record := ApprovalRecord{
		Challenge:   os.Getenv("CODEFLOW_VS09_CHALLENGE"),
		Criterion:   criterion,
		Package:     pkg,
		Binary:      filepath.Base(os.Args[0]),
		ExecutionID: t.Name(),
	}
	approvalBinaryIdentity.Do(func() {
		binary, err := os.ReadFile(os.Args[0])
		approvalBinaryIdentity.err = err
		if err == nil {
			digest := sha256.Sum256(binary)
			approvalBinaryIdentity.digest = hex.EncodeToString(digest[:])
		}
	})
	if approvalBinaryIdentity.err != nil {
		t.Fatal(approvalBinaryIdentity.err)
	}
	record.BinaryDigest = approvalBinaryIdentity.digest
	approvalObservations.Lock()
	if approvalObservations.next == nil {
		approvalObservations.next = map[string]int{}
	}
	approvalObservations.next[t.Name()]++
	record.ObservationID = approvalObservations.next[t.Name()]
	approvalObservations.Unlock()
	for role, value := range values {
		data, ok := value.([]byte)
		if !ok {
			var err error
			data, err = json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
		}
		digest := sha256.Sum256(data)
		record.Artifacts = append(record.Artifacts, ApprovalArtifact{
			Role:  role,
			Ref:   "sha256:" + hex.EncodeToString(digest[:]),
			Bytes: append([]byte(nil), data...),
		})
	}
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	t.Log(ApprovalEvidencePrefix + string(data))
}

// ObserveApprovalEvidence is an alias for ObserveApproval.
func ObserveApprovalEvidence(t *testing.T, pkg string, criteria []string, values map[string]any) {
	ObserveApproval(t, pkg, criteria, values)
}

// FaultOverlay adds the private fault bridge only to a disposable test build.
// No normal product build contains the injected function or test source.
func FaultOverlay(t *testing.T, root string) string {
	t.Helper()
	base := filepath.Join(root, "internal", "flowview", "testdata", "flowcontext")
	values := map[string]string{
		filepath.Join(root, "internal", "semantic", "zz_vs09_fault_bridge.go"):       filepath.Join(base, "fault_bridge.go.txt"),
		filepath.Join(root, "internal", "flowview", "zz_vs09_fault_overlay_test.go"): filepath.Join(base, "fault_overlay_test.go.txt"),
	}
	data, err := json.Marshal(map[string]any{"Replace": values})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "overlay.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
