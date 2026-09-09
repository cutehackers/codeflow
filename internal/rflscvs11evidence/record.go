// Package rflscvs11evidence contains test evidence serialization only. It is
// imported by flow context tests, never by product handlers or authority code.
package rflscvs11evidence

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
	Prefix                 = "VS11_OBSERVED_EVIDENCE="
	VS11EvidenceRegistryID = "rflsc-r2-vs-11"
)

var VS11EvidenceCriteria = []string{
	"VS11-A1", "VS11-A2", "VS11-A3", "VS11-A4",
	"VS11-A5", "VS11-A6", "VS11-A7", "VS11-A8",
}

type Artifact struct {
	Role  string `json:"role"`
	Ref   string `json:"ref"`
	Bytes []byte `json:"bytes"`
}

type Record struct {
	Challenge           string     `json:"challenge"`
	Criterion           string     `json:"criterion"`
	Package             string     `json:"package"`
	Binary              string     `json:"binary"`
	BinaryDigest        string     `json:"binaryDigest"`
	ExecutionID         string     `json:"executionId"`
	ObservationID       int        `json:"observationId"`
	SnapshotID          string     `json:"snapshotId"`
	SnapshotTreeDigest  string     `json:"snapshotTreeDigest"`
	Precision           string     `json:"precision"`
	Expansion           string     `json:"expansion"`
	StatementSelected   string     `json:"statementSelected,omitempty"`
	StructuralContext   string     `json:"structuralContext,omitempty"`
	CallableSignature   string     `json:"callableSignature,omitempty"`
	DirectRelation      string     `json:"directRelation,omitempty"`
	SourceLimitation    string     `json:"sourceLimitation,omitempty"`
	DisplayedLinesCount int        `json:"displayedLinesCount"`
	Artifacts           []Artifact `json:"artifacts"`
}

var observations struct {
	sync.Mutex
	next map[string]int
}

var binaryIdentity struct {
	sync.Once
	digest string
	err    error
}

// Observe records immutable evidence produced by acceptance tests.
func Observe(t *testing.T, pkg string, criteria []string, rec Record, values map[string]any) {
	t.Helper()
	activeCriterion := os.Getenv("CODEFLOW_VS11_CRITERION")
	if activeCriterion != "" {
		matched := false
		for _, value := range criteria {
			if value == activeCriterion {
				matched = true
				break
			}
		}
		if !matched {
			return
		}
		rec.Criterion = activeCriterion
	} else if len(criteria) > 0 {
		rec.Criterion = criteria[0]
	}

	rec.Challenge = os.Getenv("CODEFLOW_VS11_CHALLENGE")
	rec.Package = pkg
	rec.Binary = filepath.Base(os.Args[0])
	rec.ExecutionID = t.Name()

	binaryIdentity.Do(func() {
		binary, err := os.ReadFile(os.Args[0])
		binaryIdentity.err = err
		if err == nil {
			digest := sha256.Sum256(binary)
			binaryIdentity.digest = hex.EncodeToString(digest[:])
		}
	})
	if binaryIdentity.err != nil {
		t.Fatal(binaryIdentity.err)
	}
	rec.BinaryDigest = binaryIdentity.digest

	observations.Lock()
	if observations.next == nil {
		observations.next = map[string]int{}
	}
	observations.next[t.Name()]++
	rec.ObservationID = observations.next[t.Name()]
	observations.Unlock()

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
		rec.Artifacts = append(rec.Artifacts, Artifact{
			Role:  role,
			Ref:   "sha256:" + hex.EncodeToString(digest[:]),
			Bytes: append([]byte(nil), data...),
		})
	}

	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	t.Log(Prefix + string(data))
}
