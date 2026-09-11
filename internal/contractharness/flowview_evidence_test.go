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
	"codeflow/internal/evidence"
)

const flowviewImplementationPackage = "codeflow/internal/semantic"

var flowviewExecutions = map[string]string{
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

type flowviewTestEvent struct {
	Action  string `json:"Action"`
	Package string `json:"Package"`
	Test    string `json:"Test"`
	Output  string `json:"Output"`
}

type flowviewCriterionEvidence struct {
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

type flowviewArtifactEnvelope struct {
	ArtifactRef string `json:"artifactRef"`
}

type flowviewEvaluationInputEnvelope struct {
	Profile       *flowviewArtifactEnvelope  `json:"profile"`
	Corpus        *flowviewArtifactEnvelope  `json:"corpus"`
	Reports       []flowviewArtifactEnvelope `json:"reports"`
	Thresholds    *flowviewArtifactEnvelope  `json:"thresholds"`
	ChildEvidence []flowviewArtifactEnvelope `json:"childEvidence"`
}

type flowviewEvidenceRecord struct {
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

func TestRFLSCR2VS10_A01(t *testing.T) { runFlowviewCriterion(t, "VS10-A1") }
func TestRFLSCR2VS10_A02(t *testing.T) { runFlowviewCriterion(t, "VS10-A2") }
func TestRFLSCR2VS10_A03(t *testing.T) { runFlowviewCriterion(t, "VS10-A3") }
func TestRFLSCR2VS10_A04(t *testing.T) { runFlowviewCriterion(t, "VS10-A4") }
func TestRFLSCR2VS10_A05(t *testing.T) { runFlowviewCriterion(t, "VS10-A5") }
func TestRFLSCR2VS10_A06(t *testing.T) { runFlowviewCriterion(t, "VS10-A6") }
func TestRFLSCR2VS10_A07(t *testing.T) { runFlowviewCriterion(t, "VS10-A7") }
func TestRFLSCR2VS10_A08(t *testing.T) { runFlowviewCriterion(t, "VS10-A8") }
func TestRFLSCR2VS10_A09(t *testing.T) { runFlowviewCriterion(t, "VS10-A9") }

func TestRFLSCR2VS10_EvidenceRegistry(t *testing.T) {
	if harness.VS10EvidenceRegistryID != "rflsc-r2-vs-10" || len(flowviewExecutions) != 9 {
		t.Fatal("VS-10 executable evidence registry is incomplete")
	}
	for index := 1; index <= 9; index++ {
		criterion := fmt.Sprintf("VS10-A%d", index)
		if !t.Run(criterion, func(t *testing.T) { runFlowviewCriterion(t, criterion) }) {
			t.Fatalf("criterion %s did not complete", criterion)
		}
	}
}

func runFlowviewCriterion(t *testing.T, criterion string) flowviewEvidenceRecord {
	t.Helper()
	testID, ok := flowviewExecutions[criterion]
	if !ok {
		t.Fatalf("unregistered VS-10 criterion %s", criterion)
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	binary := filepath.Join(t.TempDir(), "flowview-evidence.test")
	build := exec.CommandContext(ctx, "go", "test", "-c", "-o", binary, flowviewImplementationPackage)
	build.Dir = root
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("compile VS-10 observed test binary: %v\n%s", err, output)
	}
	binaryBytes, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	binaryDigest := sha256.Sum256(binaryBytes)
	exactCommand := "go tool test2json -p " + flowviewImplementationPackage + " " + filepath.Base(binary) + " -test.v=test2json -test.run=^" + testID + "$ -test.count=1"
	command := exec.CommandContext(ctx, "go", "tool", "test2json", "-p", flowviewImplementationPackage, binary, "-test.v=test2json", "-test.run=^"+testID+"$", "-test.count=1")
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
		var event flowviewTestEvent
		err := decoder.Decode(&event)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("decode test2json output: %v", err)
		}
		if event.Package != flowviewImplementationPackage {
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
	var observed flowviewCriterionEvidence
	if err := json.Unmarshal([]byte(strings.TrimSpace(markerJSON)), &observed); err != nil {
		t.Fatalf("decode criterion evidence marker: %v", err)
	}
	criterionEvidence := &observed
	expectedBase := strings.ToLower(criterion)
	if criterionEvidence.Criterion != criterion || criterionEvidence.InputArtifact != expectedBase+"-input.json" || criterionEvidence.OutputArtifact != expectedBase+"-output.json" || !flowviewImmutableRef(criterionEvidence.InputRef) || !flowviewImmutableRef(criterionEvidence.OutputRef) {
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
	inputRef, err := evidence.RefJSON(inputBytes)
	if err != nil || inputRef != criterionEvidence.InputRef {
		t.Fatalf("criterion input ref was not derived from executed bytes: ref=%s err=%v", inputRef, err)
	}
	outputRef, err := evidence.RefJSON(outputBytes)
	if err != nil || outputRef != criterionEvidence.OutputRef {
		t.Fatalf("criterion output ref was not derived from executed bytes: ref=%s err=%v", outputRef, err)
	}
	var inputEnvelope flowviewEvaluationInputEnvelope
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
	actualReportRefs := flowviewEnvelopeRefs(inputEnvelope.Reports)
	actualChildRefs := flowviewEnvelopeRefs(inputEnvelope.ChildEvidence)
	if criterionEvidence.ProfileRef != actualProfileRef || criterionEvidence.CorpusRef != actualCorpusRef || criterionEvidence.ThresholdRef != actualThresholdRef || !slices.Equal(criterionEvidence.ExecutionReportRefs, actualReportRefs) || !slices.Equal(criterionEvidence.ChildEvidenceRefs, actualChildRefs) {
		t.Fatalf("criterion marker refs do not match executed input bytes: marker=%+v", criterionEvidence)
	}
	if !flowviewImmutableRef(actualProfileRef) || !flowviewImmutableRef(actualCorpusRef) || !flowviewImmutableRef(actualThresholdRef) || len(actualReportRefs) == 0 || len(actualChildRefs) == 0 {
		t.Fatalf("executed criterion input lacks required artifact refs: %+v", inputEnvelope)
	}
	for _, ref := range append(append([]string{}, criterionEvidence.ExecutionReportRefs...), criterionEvidence.ChildEvidenceRefs...) {
		if !flowviewImmutableRef(ref) {
			t.Fatalf("%s emitted mutable criterion evidence ref %q", testID, ref)
		}
	}
	outputDigest := sha256.Sum256(output)
	record := flowviewEvidenceRecord{
		Criterion: criterion, ImplementationPackage: flowviewImplementationPackage, ImplementationTestID: testID,
		ExecutionBinary: filepath.Base(binary), ExecutionBinaryDigest: "sha256:" + hex.EncodeToString(binaryDigest[:]), ExecutionID: t.Name(), ExactCommand: exactCommand, Result: "pass",
		InputRef: criterionEvidence.InputRef, ProfileRef: criterionEvidence.ProfileRef, CorpusRef: criterionEvidence.CorpusRef, ExecutionReportRefs: criterionEvidence.ExecutionReportRefs,
		ThresholdRef: criterionEvidence.ThresholdRef, ChildEvidenceRefs: criterionEvidence.ChildEvidenceRefs, OutputRef: criterionEvidence.OutputRef, TestOutputRef: "sha256:" + hex.EncodeToString(outputDigest[:]),
		EvidenceClass: "synthetic_evaluator_fixture", ProductionBenchmark: false,
	}
	encoded, _ := json.Marshal(record)
	t.Log("evidence=" + string(encoded))
	return record
}

func flowviewEnvelopeRefs(values []flowviewArtifactEnvelope) []string {
	refs := make([]string, 0, len(values))
	for _, value := range values {
		refs = append(refs, value.ArtifactRef)
	}
	return refs
}

func flowviewImmutableRef(ref string) bool {
	if len(ref) != len("sha256:")+64 || !strings.HasPrefix(ref, "sha256:") {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(ref, "sha256:"))
	return err == nil
}
