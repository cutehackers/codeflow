// Package rflscvs09evidence contains test evidence serialization only. It is
// imported by approval tests, never by product handlers or authority code.
package rflscvs09evidence

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

const Prefix = "VS09_OBSERVED_EVIDENCE="

type Artifact struct {
	Role  string `json:"role"`
	Ref   string `json:"ref"`
	Bytes []byte `json:"bytes"`
}

type Record struct {
	Challenge     string     `json:"challenge"`
	Criterion     string     `json:"criterion"`
	Package       string     `json:"package"`
	Binary        string     `json:"binary"`
	BinaryDigest  string     `json:"binaryDigest"`
	ExecutionID   string     `json:"executionId"`
	ObservationID int        `json:"observationId"`
	Artifacts     []Artifact `json:"artifacts"`
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

// Observe retains the actual bytes passed by the public behavioral test. The
// registry, not this producer, assigns pass after observing test2json completion.
func Observe(t *testing.T, pkg string, criteria []string, values map[string]any) {
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
	record := Record{Challenge: os.Getenv("CODEFLOW_VS09_CHALLENGE"), Criterion: criterion, Package: pkg, Binary: filepath.Base(os.Args[0]), ExecutionID: t.Name()}
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
	record.BinaryDigest = binaryIdentity.digest
	observations.Lock()
	if observations.next == nil {
		observations.next = map[string]int{}
	}
	observations.next[t.Name()]++
	record.ObservationID = observations.next[t.Name()]
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
		record.Artifacts = append(record.Artifacts, Artifact{Role: role, Ref: "sha256:" + hex.EncodeToString(digest[:]), Bytes: append([]byte(nil), data...)})
	}
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	t.Log(Prefix + string(data))
}
