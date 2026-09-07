package contractharness_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	harness "codeflow/internal/contractharness"
	"codeflow/internal/releaseartifact"
)

const vs10ImplementationPackage = "codeflow/internal/semantic"

var vs10Executions = map[string]string{
	"VS10-A1": "TestRFLSCR2VS10_A01",
	"VS10-A2": "TestRFLSCR2VS10_A02",
	"VS10-A3": "TestRFLSCR2VS10_A03",
	"VS10-A4": "TestRFLSCR2VS10_A04",
	"VS10-A5": "TestRFLSCR2VS10_A05",
	"VS10-A6": "TestRFLSCR2VS10_A06",
	"VS10-A7": "TestRFLSCR2VS10_A07",
	"VS10-A8": "TestRFLSCR2VS10_A08",
	"VS10-A9": "TestRFLSCR2VS10_A09",
}

type vs10TestEvent struct {
	Action  string `json:"Action"`
	Package string `json:"Package"`
	Test    string `json:"Test"`
	Output  string `json:"Output"`
}

type vs10CriterionEvidence struct {
	Criterion           string   `json:"criterion"`
	InputRef            string   `json:"inputRef"`
	ProfileRef          string   `json:"profileRef"`
	CorpusRef           string   `json:"corpusRef"`
	ExecutionReportRefs []string `json:"executionReportRefs"`
	ThresholdRef        string   `json:"thresholdRef"`
	ChildEvidenceRefs   []string `json:"childEvidenceRefs"`
	OutputRef           string   `json:"outputRef"`
	InputArtifact       string   `json:"inputArtifact"`
	OutputArtifact      string   `json:"outputArtifact"`
}

type vs10ArtifactEnvelope struct {
	ArtifactRef string `json:"artifactRef"`
}

type vs10EvaluationInputEnvelope struct {
	Profile       *vs10ArtifactEnvelope  `json:"profile"`
	Corpus        *vs10ArtifactEnvelope  `json:"corpus"`
	Reports       []vs10ArtifactEnvelope `json:"reports"`
	Thresholds    *vs10ArtifactEnvelope  `json:"thresholds"`
	ChildEvidence []vs10ArtifactEnvelope `json:"childEvidence"`
}

type vs10EvidenceRecord struct {
	Criterion             string   `json:"criterion"`
	ImplementationPackage string   `json:"implementationPackage"`
	ImplementationTestID  string   `json:"implementationTestId"`
	ExecutionBinary       string   `json:"executionBinary"`
	ExecutionBinaryDigest string   `json:"executionBinaryDigest"`
	ExecutionID           string   `json:"executionId"`
	ExactCommand          string   `json:"exactCommand"`
	Result                string   `json:"result"`
	InputRef              string   `json:"inputRef"`
	ProfileRef            string   `json:"profileRef"`
	CorpusRef             string   `json:"corpusRef"`
	ExecutionReportRefs   []string `json:"executionReportRefs"`
	ThresholdRef          string   `json:"thresholdRef"`
	ChildEvidenceRefs     []string `json:"childEvidenceRefs"`
	OutputRef             string   `json:"outputRef"`
	TestOutputRef         string   `json:"testOutputRef"`
	EvidenceClass         string   `json:"evidenceClass"`
	ProductionBenchmark   bool     `json:"productionBenchmark"`
}

func TestRFLSCR2VS10_A01(t *testing.T) { runVS10Criterion(t, "VS10-A1") }
func TestRFLSCR2VS10_A02(t *testing.T) { runVS10Criterion(t, "VS10-A2") }
func TestRFLSCR2VS10_A03(t *testing.T) { runVS10Criterion(t, "VS10-A3") }
func TestRFLSCR2VS10_A04(t *testing.T) { runVS10Criterion(t, "VS10-A4") }
func TestRFLSCR2VS10_A05(t *testing.T) { runVS10Criterion(t, "VS10-A5") }
func TestRFLSCR2VS10_A06(t *testing.T) { runVS10Criterion(t, "VS10-A6") }
func TestRFLSCR2VS10_A07(t *testing.T) { runVS10Criterion(t, "VS10-A7") }
func TestRFLSCR2VS10_A08(t *testing.T) { runVS10Criterion(t, "VS10-A8") }
func TestRFLSCR2VS10_A09(t *testing.T) { runVS10Criterion(t, "VS10-A9") }

func TestRFLSCR2VS10_EvidenceRegistry(t *testing.T) {
	if harness.VS10EvidenceRegistryID != "rflsc-r2-vs-10" || len(vs10Executions) != 9 {
		t.Fatal("VS-10 executable evidence registry is incomplete")
	}
	for index := 1; index <= 9; index++ {
		criterion := fmt.Sprintf("VS10-A%d", index)
		if !t.Run(criterion, func(t *testing.T) { runVS10Criterion(t, criterion) }) {
			t.Fatalf("criterion %s did not complete", criterion)
		}
	}
}

func runVS10Criterion(t *testing.T, criterion string) vs10EvidenceRecord {
	t.Helper()
	testID, ok := vs10Executions[criterion]
	if !ok {
		t.Fatalf("unregistered VS-10 criterion %s", criterion)
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	binary := filepath.Join(t.TempDir(), "vs10-evidence.test")
	build := exec.CommandContext(ctx, "go", "test", "-c", "-o", binary, vs10ImplementationPackage)
	build.Dir = root
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("compile VS-10 observed test binary: %v\n%s", err, output)
	}
	binaryBytes, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	binaryDigest := sha256.Sum256(binaryBytes)
	exactCommand := "go tool test2json -p " + vs10ImplementationPackage + " " + filepath.Base(binary) + " -test.v=test2json -test.run=^" + testID + "$ -test.count=1"
	command := exec.CommandContext(ctx, "go", "tool", "test2json", "-p", vs10ImplementationPackage, binary, "-test.v=test2json", "-test.run=^"+testID+"$", "-test.count=1")
	command.Dir = filepath.Join(root, "internal", "semantic")
	artifactDir := t.TempDir()
	command.Env = append(os.Environ(), "CODEFLOW_VS10_EVIDENCE_DIR="+artifactDir)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("execute %s: %v\n%s", testID, err, output)
	}
	runs, passes, packagePass := 0, 0, 0
	var testOutput strings.Builder
	decoder := json.NewDecoder(bytes.NewReader(output))
	for {
		var event vs10TestEvent
		err := decoder.Decode(&event)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("decode test2json output: %v", err)
		}
		if event.Package != vs10ImplementationPackage {
			t.Fatalf("unexpected package %q", event.Package)
		}
		if event.Action == "fail" || event.Action == "skip" {
			t.Fatalf("nonpassing event for %q", event.Test)
		}
		if event.Test == testID && event.Action == "run" {
			runs++
		}
		if event.Test == testID && event.Action == "pass" {
			passes++
		}
		if event.Test == testID {
			testOutput.WriteString(event.Output)
		}
		if event.Test == "" && event.Action == "pass" {
			packagePass++
		}
	}
	if runs != 1 || passes != 1 || packagePass != 1 {
		t.Fatalf("%s did not execute and pass exactly once: runs=%d passes=%d packagePass=%d", testID, runs, passes, packagePass)
	}
	const marker = "VS10_CRITERION_EVIDENCE "
	markerOffset := strings.Index(testOutput.String(), marker)
	if markerOffset < 0 {
		t.Fatalf("%s did not emit criterion evidence", testID)
	}
	markerJSON := testOutput.String()[markerOffset+len(marker):]
	if newline := strings.IndexByte(markerJSON, '\n'); newline >= 0 {
		markerJSON = markerJSON[:newline]
	}
	var observed vs10CriterionEvidence
	if err := json.Unmarshal([]byte(strings.TrimSpace(markerJSON)), &observed); err != nil {
		t.Fatalf("decode criterion evidence marker: %v", err)
	}
	criterionEvidence := &observed
	expectedBase := strings.ToLower(criterion)
	if criterionEvidence.Criterion != criterion || criterionEvidence.InputArtifact != expectedBase+"-input.json" || criterionEvidence.OutputArtifact != expectedBase+"-output.json" || !vs10ImmutableRef(criterionEvidence.InputRef) || !vs10ImmutableRef(criterionEvidence.OutputRef) {
		t.Fatalf("%s did not emit complete actual criterion evidence: %+v", testID, criterionEvidence)
	}
	inputBytes, err := os.ReadFile(filepath.Join(artifactDir, criterionEvidence.InputArtifact))
	if err != nil {
		t.Fatalf("read executed criterion input: %v", err)
	}
	outputBytes, err := os.ReadFile(filepath.Join(artifactDir, criterionEvidence.OutputArtifact))
	if err != nil {
		t.Fatalf("read executed criterion output: %v", err)
	}
	inputRef, err := releaseartifact.RefJSON(inputBytes)
	if err != nil || inputRef != criterionEvidence.InputRef {
		t.Fatalf("criterion input ref was not derived from executed bytes: ref=%s err=%v", inputRef, err)
	}
	outputRef, err := releaseartifact.RefJSON(outputBytes)
	if err != nil || outputRef != criterionEvidence.OutputRef {
		t.Fatalf("criterion output ref was not derived from executed bytes: ref=%s err=%v", outputRef, err)
	}
	var inputEnvelope vs10EvaluationInputEnvelope
	if err := json.Unmarshal(inputBytes, &inputEnvelope); err != nil {
		t.Fatalf("parse executed criterion input: %v", err)
	}
	actualProfileRef, actualCorpusRef, actualThresholdRef := "", "", ""
	if inputEnvelope.Profile != nil {
		actualProfileRef = inputEnvelope.Profile.ArtifactRef
	}
	if inputEnvelope.Corpus != nil {
		actualCorpusRef = inputEnvelope.Corpus.ArtifactRef
	}
	if inputEnvelope.Thresholds != nil {
		actualThresholdRef = inputEnvelope.Thresholds.ArtifactRef
	}
	actualReportRefs := vs10EnvelopeRefs(inputEnvelope.Reports)
	actualChildRefs := vs10EnvelopeRefs(inputEnvelope.ChildEvidence)
	if criterionEvidence.ProfileRef != actualProfileRef || criterionEvidence.CorpusRef != actualCorpusRef || criterionEvidence.ThresholdRef != actualThresholdRef || !slices.Equal(criterionEvidence.ExecutionReportRefs, actualReportRefs) || !slices.Equal(criterionEvidence.ChildEvidenceRefs, actualChildRefs) {
		t.Fatalf("criterion marker refs do not match executed input bytes: marker=%+v", criterionEvidence)
	}
	if !vs10ImmutableRef(actualProfileRef) || !vs10ImmutableRef(actualCorpusRef) || !vs10ImmutableRef(actualThresholdRef) || len(actualReportRefs) == 0 || len(actualChildRefs) == 0 {
		t.Fatalf("executed criterion input lacks required artifact refs: %+v", inputEnvelope)
	}
	for _, ref := range append(append([]string{}, criterionEvidence.ExecutionReportRefs...), criterionEvidence.ChildEvidenceRefs...) {
		if !vs10ImmutableRef(ref) {
			t.Fatalf("%s emitted mutable criterion evidence ref %q", testID, ref)
		}
	}
	outputDigest := sha256.Sum256(output)
	record := vs10EvidenceRecord{
		Criterion: criterion, ImplementationPackage: vs10ImplementationPackage, ImplementationTestID: testID,
		ExecutionBinary: filepath.Base(binary), ExecutionBinaryDigest: "sha256:" + hex.EncodeToString(binaryDigest[:]), ExecutionID: t.Name(), ExactCommand: exactCommand, Result: "pass",
		InputRef: criterionEvidence.InputRef, ProfileRef: criterionEvidence.ProfileRef, CorpusRef: criterionEvidence.CorpusRef, ExecutionReportRefs: criterionEvidence.ExecutionReportRefs,
		ThresholdRef: criterionEvidence.ThresholdRef, ChildEvidenceRefs: criterionEvidence.ChildEvidenceRefs, OutputRef: criterionEvidence.OutputRef, TestOutputRef: "sha256:" + hex.EncodeToString(outputDigest[:]),
		EvidenceClass: "synthetic_evaluator_fixture", ProductionBenchmark: false,
	}
	encoded, _ := json.Marshal(record)
	t.Log("evidence=" + string(encoded))
	return record
}

func vs10EnvelopeRefs(values []vs10ArtifactEnvelope) []string {
	refs := make([]string, 0, len(values))
	for _, value := range values {
		refs = append(refs, value.ArtifactRef)
	}
	return refs
}

func vs10ImmutableRef(ref string) bool {
	if len(ref) != len("sha256:")+64 || !strings.HasPrefix(ref, "sha256:") {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(ref, "sha256:"))
	return err == nil
}
