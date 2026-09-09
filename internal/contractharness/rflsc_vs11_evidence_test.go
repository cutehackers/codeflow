package contractharness_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	harness "codeflow/internal/contractharness"
	"codeflow/internal/rflscvs11evidence"
)

type vs11Execution struct {
	Package string
	Test    string
}

var vs11Executions = map[string]vs11Execution{
	"VS11-A1": {"codeflow/internal/flowview", "TestRFLSCR2VS11_A01"},
	"VS11-A2": {"codeflow/internal/flowview", "TestRFLSCR2VS11_A02"},
	"VS11-A3": {"codeflow/internal/flowview", "TestRFLSCR2VS11_A03"},
	"VS11-A4": {"codeflow/internal/flowview", "TestRFLSCR2VS11_A04"},
	"VS11-A5": {"codeflow/internal/flowview", "TestRFLSCR2VS11_A05"},
	"VS11-A6": {"codeflow/internal/flowview", "TestRFLSCR2VS11_A06"},
	"VS11-A7": {"codeflow/internal/flowview", "TestRFLSCR2VS11_A07"},
	"VS11-A8": {"codeflow/internal/flowview", "TestRFLSCR2VS11_A08"},
}

type vs11TestEvent struct {
	Action  string
	Package string
	Test    string
	Output  string
}

type vs11ObservedRun struct {
	Events       []vs11TestEvent
	Records      []rflscvs11evidence.Record
	Binary       string
	BinaryDigest string
	Challenge    string
}

var vs11CompiledBinary struct {
	sync.Mutex
	binary       string
	binaryDigest string
	err          error
}

func getVS11CompiledBinary(t *testing.T, root string) (string, string) {
	t.Helper()
	vs11CompiledBinary.Lock()
	defer vs11CompiledBinary.Unlock()

	if vs11CompiledBinary.binary != "" && vs11CompiledBinary.err == nil {
		if _, err := os.Stat(vs11CompiledBinary.binary); err == nil {
			return vs11CompiledBinary.binary, vs11CompiledBinary.binaryDigest
		}
	}

	dir, err := os.MkdirTemp("", "vs11-evidence-bin-*")
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(dir, "vs11-evidence.test")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, "go", "test", "-c", "-o", binary, "codeflow/internal/flowview")
	cmd.Dir = root
	if output, err := cmd.CombinedOutput(); err != nil {
		vs11CompiledBinary.err = fmt.Errorf("compile flowview test binary: %v\n%s", err, output)
		t.Fatal(vs11CompiledBinary.err)
	}

	bytes, err := os.ReadFile(binary)
	if err != nil {
		vs11CompiledBinary.err = err
		t.Fatal(err)
	}
	digest := sha256.Sum256(bytes)
	vs11CompiledBinary.binary = binary
	vs11CompiledBinary.binaryDigest = hex.EncodeToString(digest[:])
	return vs11CompiledBinary.binary, vs11CompiledBinary.binaryDigest
}

func runVS11Criterion(t *testing.T, criterion string) vs11ObservedRun {
	t.Helper()
	execution, ok := vs11Executions[criterion]
	if !ok {
		t.Fatalf("unregistered VS-11 criterion %s", criterion)
	}

	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}

	binary, binaryDigest := getVS11CompiledBinary(t, root)

	challengeBytes := make([]byte, 32)
	if _, err := rand.Read(challengeBytes); err != nil {
		t.Fatal(err)
	}
	challenge := hex.EncodeToString(challengeBytes)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, "go", "tool", "test2json", "-p", execution.Package, binary,
		"-test.v=test2json", "-test.run=^"+execution.Test+"$", "-test.count=1", "-test.timeout=60s")
	cmd.Dir = root
	cmd.Env = append(os.Environ(),
		"CODEFLOW_VS11_CRITERION="+criterion,
		"CODEFLOW_VS11_CHALLENGE="+challenge,
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("execution %s failed: %v\n%s", execution.Test, err, output)
	}

	run := vs11ObservedRun{
		Binary:       filepath.Base(binary),
		BinaryDigest: binaryDigest,
		Challenge:    challenge,
	}

	decoder := json.NewDecoder(bytes.NewReader(output))
	logs := map[string]string{}
	for {
		var event vs11TestEvent
		if err := decoder.Decode(&event); err != nil {
			if err == io.EOF {
				break
			}
			t.Fatalf("decode test2json: %v", err)
		}
		run.Events = append(run.Events, event)
		if event.Output != "" {
			logs[event.Test] += event.Output
		}
	}

	for _, log := range logs {
		for _, line := range strings.Split(log, "\n") {
			idx := strings.Index(line, rflscvs11evidence.Prefix)
			if idx < 0 {
				continue
			}
			raw := line[idx+len(rflscvs11evidence.Prefix):]
			var record rflscvs11evidence.Record
			if err := json.Unmarshal([]byte(raw), &record); err != nil {
				t.Fatalf("parse observed record: %v\n%s", err, raw)
			}
			run.Records = append(run.Records, record)
		}
	}

	if len(run.Records) == 0 {
		t.Fatalf("zero-match: no evidence records observed for criterion %s in test %s", criterion, execution.Test)
	}

	for _, rec := range run.Records {
		if rec.Challenge != challenge {
			t.Fatalf("challenge mismatch: got %q want %q", rec.Challenge, challenge)
		}
		if err := harness.ValidateVS11EvidenceRecord(criterion, execution.Test, rec, execution.Package, run.Binary); err != nil {
			t.Fatalf("validate evidence record: %v", err)
		}
	}

	return run
}

func TestRFLSCR2VS11_EvidenceRegistry(t *testing.T) {
	if harness.VS11EvidenceRegistryID != "rflsc-r2-vs-11" {
		t.Fatalf("unexpected VS-11 evidence registry ID: %q", harness.VS11EvidenceRegistryID)
	}
	if len(vs11Executions) != 8 {
		t.Fatalf("expected 8 VS-11 executable registrations, got %d", len(vs11Executions))
	}

	allRecords := make([]rflscvs11evidence.Record, 0, len(harness.VS11EvidenceCriteria))
	seenCriteria := make(map[string]bool)

	for _, criterion := range harness.VS11EvidenceCriteria {
		if seenCriteria[criterion] {
			t.Fatalf("duplicate criterion in VS11EvidenceCriteria: %s", criterion)
		}
		seenCriteria[criterion] = true

		var run vs11ObservedRun
		passed := t.Run(criterion, func(st *testing.T) {
			run = runVS11Criterion(st, criterion)
		})
		if !passed {
			t.Fatalf("criterion %s failed execution", criterion)
		}
		for _, rec := range run.Records {
			allRecords = append(allRecords, rec)
			t.Logf("criterion=%s executionId=%s artifacts=%d precision=%s",
				rec.Criterion, rec.ExecutionID, len(rec.Artifacts), rec.Precision)
		}
	}

	if len(allRecords) < len(harness.VS11EvidenceCriteria) {
		t.Fatalf("missing criterion evidence records: got %d want >= %d", len(allRecords), len(harness.VS11EvidenceCriteria))
	}
}

func TestVS11EvidenceRegistryRejectsFabricatedPass(t *testing.T) {
	record := rflscvs11evidence.Record{
		Criterion:          "VS11-A1",
		Package:            "codeflow/internal/flowview",
		Binary:             "flowview.test",
		BinaryDigest:       "digest-test",
		ExecutionID:        "TestRFLSCR2VS11_A01",
		SnapshotID:         "snap-fake",
		SnapshotTreeDigest: "tree-fake",
		Artifacts: []rflscvs11evidence.Artifact{
			{Role: "test", Ref: "sha256:1", Bytes: []byte("1")},
			{Role: "test2", Ref: "sha256:2", Bytes: []byte("2")},
		},
	}

	// Criterion mismatch
	if err := harness.ValidateVS11EvidenceRecord("VS11-A2", "TestRFLSCR2VS11_A01", record, "codeflow/internal/flowview", "flowview.test"); err == nil {
		t.Fatal("expected error on criterion mismatch")
	}

	// Package mismatch
	if err := harness.ValidateVS11EvidenceRecord("VS11-A1", "TestRFLSCR2VS11_A01", record, "codeflow/internal/other", "flowview.test"); err == nil {
		t.Fatal("expected error on package mismatch")
	}

	// Missing binary digest
	badRecord := record
	badRecord.BinaryDigest = ""
	if err := harness.ValidateVS11EvidenceRecord("VS11-A1", "TestRFLSCR2VS11_A01", badRecord, "codeflow/internal/flowview", "flowview.test"); err == nil {
		t.Fatal("expected error on missing binary digest")
	}

	// Missing snapshot ID
	badRecord = record
	badRecord.SnapshotID = ""
	if err := harness.ValidateVS11EvidenceRecord("VS11-A1", "TestRFLSCR2VS11_A01", badRecord, "codeflow/internal/flowview", "flowview.test"); err == nil {
		t.Fatal("expected error on missing snapshot ID")
	}
}

func TestVS11EvidenceRegistryRejectsDuplicateImmutableRef(t *testing.T) {
	record := rflscvs11evidence.Record{
		Criterion:          "VS11-A1",
		Package:            "codeflow/internal/flowview",
		Binary:             "flowview.test",
		BinaryDigest:       "digest-test",
		ExecutionID:        "TestRFLSCR2VS11_A01",
		SnapshotID:         "snap-1",
		SnapshotTreeDigest: "tree-1",
		Artifacts: []rflscvs11evidence.Artifact{
			{Role: "art1", Ref: "sha256:same", Bytes: []byte("a")},
			{Role: "art2", Ref: "sha256:same", Bytes: []byte("b")},
		},
	}

	if err := harness.ValidateVS11EvidenceRecord("VS11-A1", "TestRFLSCR2VS11_A01", record, "codeflow/internal/flowview", "flowview.test"); err == nil {
		t.Fatal("expected error on duplicate immutable artifact ref")
	}
}
