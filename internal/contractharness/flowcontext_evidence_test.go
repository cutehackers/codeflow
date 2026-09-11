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
	"testing"
	"time"

	harness "codeflow/internal/contractharness"
	"codeflow/internal/evidence"
	"codeflow/internal/semantic"
)

type flowContextExecution struct {
	Package, Test string
	Observations  int
	Overlay       bool
}

var flowContextExecutions = map[string]flowContextExecution{
	"VS09-A1":  {"codeflow/internal/mcp", "TestMCPApprovalPublicAuthAndMissingPairCreateNoRecords", 2, false},
	"VS09-A2":  {"codeflow/internal/mcp", "TestMCPApprovalPublicAuthAndMissingPairCreateNoRecords", 2, false},
	"VS09-A3":  {"codeflow/internal/mcp", "TestMCPApprovalRejectsEachCorruptStoredPairWithoutMutation", 10, false},
	"VS09-A4":  {"codeflow/internal/mcp", "TestMCPApprovalRejectsEachCorruptStoredPairWithoutMutation", 3, false},
	"VS09-A5":  {"codeflow/internal/mcp", "TestMCPApprovalMutationPublishesOnceAcrossRetryAndRestart", 1, false},
	"VS09-A6":  {"codeflow/internal/mcp", "TestMCPApprovalAllLifecycleCommandsPreserveCanonicalProofAndGuards", 9, false},
	"VS09-A7":  {"codeflow/internal/mcp", "TestMCPApprovalMutationPublishesOnceAcrossRetryAndRestart", 1, false},
	"VS09-A8":  {"codeflow/internal/mcp", "TestMCPApprovalAllLifecycleCommandsPreserveCanonicalProofAndGuards", 9, false},
	"VS09-A9":  {"codeflow/internal/mcp", "TestMCPApprovalHistoryToolRestartsAndTracksLiveHeadReadOnly", 1, false},
	"VS09-A10": {"codeflow/internal/mcp", "TestMCPApprovalAllLifecycleCommandsPreserveCanonicalProofAndGuards", 54, false},
	"VS09-A11": {"codeflow/internal/mcp", "TestApprovalPublicConflictReportsWinnerVersionAndPublishesOnce", 4, false},
	"VS09-A12": {"codeflow/internal/flowview", "TestApprovalPublicPersistenceFaultsOverlay", 10, true},
	"VS09-A13": {"codeflow/internal/flowview", "TestApprovalPublicPersistenceFaultsOverlay", 10, true},
}

type flowContextTestEvent struct{ Action, Package, Test, Output string }
type flowContextObservedRun struct {
	Events                          []flowContextTestEvent
	Records                         []evidence.ApprovalRecord
	Binary, BinaryDigest, Challenge string
}
type vs09CommitObservation struct {
	EventID         string `json:"sseEventId"`
	ApprovalEventID string `json:"approvalEventId"`
	AggregateID     string `json:"aggregateId"`
	Version         int64  `json:"committedVersion"`
	Sequence        int    `json:"sseSequence"`
	ArtifactRef     string `json:"artifactRef"`
}

func TestRFLSCR2VS09_A01(t *testing.T) { runFlowContextCriterion(t, "VS09-A1") }
func TestRFLSCR2VS09_A02(t *testing.T) { runFlowContextCriterion(t, "VS09-A2") }
func TestRFLSCR2VS09_A03(t *testing.T) { runFlowContextCriterion(t, "VS09-A3") }
func TestRFLSCR2VS09_A04(t *testing.T) { runFlowContextCriterion(t, "VS09-A4") }
func TestRFLSCR2VS09_A05(t *testing.T) { runFlowContextCriterion(t, "VS09-A5") }
func TestRFLSCR2VS09_A06(t *testing.T) { runFlowContextCriterion(t, "VS09-A6") }
func TestRFLSCR2VS09_A07(t *testing.T) { runFlowContextCriterion(t, "VS09-A7") }
func TestRFLSCR2VS09_A08(t *testing.T) { runFlowContextCriterion(t, "VS09-A8") }
func TestRFLSCR2VS09_A09(t *testing.T) { runFlowContextCriterion(t, "VS09-A9") }
func TestRFLSCR2VS09_A10(t *testing.T) { runFlowContextCriterion(t, "VS09-A10") }
func TestRFLSCR2VS09_A11(t *testing.T) { runFlowContextCriterion(t, "VS09-A11") }
func TestRFLSCR2VS09_A12(t *testing.T) { runFlowContextCriterion(t, "VS09-A12") }
func TestRFLSCR2VS09_A13(t *testing.T) { runFlowContextCriterion(t, "VS09-A13") }

func TestRFLSCR2VS09_EvidenceRegistry(t *testing.T) {
	if harness.VS09EvidenceRegistryID != "rflsc-r2-vs-09" || len(flowContextExecutions) != 13 {
		t.Fatal("missing VS09 executable registrations")
	}
	for i := 1; i <= 13; i++ {
		criterion := fmt.Sprintf("VS09-A%d", i)
		if !t.Run(criterion, func(t *testing.T) { runFlowContextCriterion(t, criterion) }) {
			t.Fatalf("criterion %s did not complete", criterion)
		}
	}
}

func runFlowContextCriterion(t *testing.T, criterion string) flowContextObservedRun {
	t.Helper()
	execution, ok := flowContextExecutions[criterion]
	if !ok {
		t.Fatalf("unregistered criterion %s", criterion)
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	binary := filepath.Join(t.TempDir(), "flowcontext-evidence.test")
	args := []string{"test", "-c", "-o", binary, execution.Package}
	if execution.Overlay {
		args = append(args, "-overlay", evidence.FaultOverlay(t, root))
	}
	if evidence.RaceEnabled {
		args = append(args, "-race")
	}
	build := exec.CommandContext(ctx, "go", args...)
	build.Dir = root
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("compile observed test binary: %v\n%s", err, output)
	}
	binaryBytes, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(binaryBytes)
	challengeBytes := make([]byte, 32)
	if _, err := rand.Read(challengeBytes); err != nil {
		t.Fatal(err)
	}
	run := flowContextObservedRun{Binary: filepath.Base(binary), BinaryDigest: hex.EncodeToString(digest[:]), Challenge: hex.EncodeToString(challengeBytes)}
	command := exec.CommandContext(ctx, "go", "tool", "test2json", "-p", execution.Package, binary, "-test.v=test2json", "-test.run=^"+execution.Test+"$", "-test.count=1", "-test.timeout=120s")
	command.Dir = filepath.Join(root, strings.TrimPrefix(execution.Package, "codeflow/"))
	command.Env = append(os.Environ(), "CODEFLOW_VS09_CRITERION="+criterion, "CODEFLOW_VS09_CHALLENGE="+run.Challenge)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("public execution %s: %v\n%s", execution.Test, err, output)
	}
	decoder := json.NewDecoder(bytes.NewReader(output))
	logs := map[string]string{}
	for {
		var event flowContextTestEvent
		err := decoder.Decode(&event)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("invalid test2json output: %v\n%s", err, output)
		}
		run.Events = append(run.Events, event)
		logs[event.Test] += event.Output
	}
	for _, log := range logs {
		for _, line := range strings.Split(log, "\n") {
			offset := strings.Index(line, evidence.ApprovalEvidencePrefix)
			if offset < 0 {
				continue
			}
			var record evidence.ApprovalRecord
			if err := json.Unmarshal([]byte(line[offset+len(evidence.ApprovalEvidencePrefix):]), &record); err != nil {
				t.Fatalf("invalid observed record: %v", err)
			}
			run.Records = append(run.Records, record)
		}
	}
	commits, err := validateVS09ObservedRun(criterion, execution, run)
	if err != nil {
		t.Fatal(err)
	}
	refs := []string{}
	historyVersions := []map[string]any{}
	for _, record := range run.Records {
		observed, _ := json.Marshal(record)
		// Keep immutable content with its refs in verbose execution evidence,
		// rather than emitting unresolvable digest-only success claims.
		t.Log("observed=" + string(observed))
		for _, artifact := range record.Artifacts {
			refs = append(refs, artifact.Ref)
			if strings.Contains(artifact.Role, "history") {
				var history semantic.ApprovalHistoryResult
				if json.Unmarshal(artifact.Bytes, &history) == nil && history.SchemaID == semantic.ApprovalHistoryV1SchemaID {
					if err := harness.ValidateVS09Contract(semantic.ApprovalHistoryV1SchemaID, artifact.Bytes); err != nil {
						t.Fatalf("history evidence contract: %v", err)
					}
					historyVersions = append(historyVersions, map[string]any{"aggregateId": history.Aggregate.AggregateID, "committedVersion": history.Aggregate.Version, "freshness": history.Freshness, "artifactRef": artifact.Ref})
				}
			}
		}
	}
	applicability := "observed approval broadcasts"
	if len(commits) == 0 {
		applicability = "N/A: rejected commands or read-only history do not publish approval events"
	}
	data, _ := json.Marshal(map[string]any{"criterion": criterion, "implementationTestId": execution.Test, "implementationPackage": execution.Package, "executionBinary": run.Binary, "executionBinaryDigest": run.BinaryDigest, "executionId": t.Name(), "result": "pass", "observations": len(run.Records), "artifactRefs": refs, "commits": commits, "historyVersions": historyVersions, "sseApplicability": applicability, "raceInstrumented": evidence.RaceEnabled})
	t.Log("evidence=" + string(data))
	return run
}

func validateVS09ObservedRun(criterion string, expected flowContextExecution, run flowContextObservedRun) ([]vs09CommitObservation, error) {
	if run.Challenge == "" || len(run.BinaryDigest) != 64 || run.Binary == "" {
		return nil, fmt.Errorf("missing execution challenge/binary identity")
	}
	runs := map[string]int{}
	passes := map[string]int{}
	logs := map[string]string{}
	for _, event := range run.Events {
		logs[event.Test] += event.Output
		if event.Package != expected.Package {
			return nil, fmt.Errorf("unexpected execution package %s", event.Package)
		}
		if event.Action == "skip" || event.Action == "fail" {
			return nil, fmt.Errorf("nonpassing execution %s", event.Test)
		}
		if event.Test != "" && event.Test != expected.Test && !strings.HasPrefix(event.Test, expected.Test+"/") {
			return nil, fmt.Errorf("unexpected test identity %s", event.Test)
		}
		if event.Action == "run" {
			runs[event.Test]++
		}
		if event.Action == "pass" {
			passes[event.Test]++
		}
	}
	if runs[expected.Test] != 1 || passes[expected.Test] != 1 || passes[""] != 1 {
		return nil, fmt.Errorf("required public test did not execute and pass exactly once")
	}
	if len(run.Records) != expected.Observations {
		return nil, fmt.Errorf("%s observed records=%d want %d", criterion, len(run.Records), expected.Observations)
	}
	observedDigests := map[string]int{}
	for _, log := range logs {
		for _, line := range strings.Split(log, "\n") {
			offset := strings.Index(line, evidence.ApprovalEvidencePrefix)
			if offset < 0 {
				continue
			}
			var record evidence.ApprovalRecord
			if err := json.Unmarshal([]byte(line[offset+len(evidence.ApprovalEvidencePrefix):]), &record); err != nil {
				return nil, fmt.Errorf("malformed executed evidence")
			}
			data, _ := json.Marshal(record)
			digest := sha256.Sum256(data)
			observedDigests[hex.EncodeToString(digest[:])]++
		}
	}
	seen := map[string]bool{}
	roles := map[string]bool{}
	commits := []vs09CommitObservation{}
	seenCommits := map[string]bool{}
	for _, record := range run.Records {
		data, _ := json.Marshal(record)
		digest := sha256.Sum256(data)
		if observedDigests[hex.EncodeToString(digest[:])] != 1 {
			return nil, fmt.Errorf("record bytes were not observed exactly once in executed output")
		}
		if record.Criterion != criterion || record.Challenge != run.Challenge || record.Package != expected.Package || record.Binary != run.Binary || record.BinaryDigest != run.BinaryDigest || record.ObservationID < 1 {
			return nil, fmt.Errorf("fabricated or unbound evidence identity")
		}
		if runs[record.ExecutionID] != 1 || passes[record.ExecutionID] != 1 {
			return nil, fmt.Errorf("record has no observed passing test: %s", record.ExecutionID)
		}
		key := fmt.Sprintf("%s/%d", record.ExecutionID, record.ObservationID)
		if seen[key] {
			return nil, fmt.Errorf("duplicate observation %s", key)
		}
		seen[key] = true
		artifactRoles := map[string]bool{}
		for _, artifact := range record.Artifacts {
			if artifact.Role == "" || artifactRoles[artifact.Role] || len(artifact.Bytes) == 0 {
				return nil, fmt.Errorf("missing or duplicate artifact role")
			}
			artifactRoles[artifact.Role] = true
			roles[artifact.Role] = true
			digest := sha256.Sum256(artifact.Bytes)
			if artifact.Ref != "sha256:"+hex.EncodeToString(digest[:]) {
				return nil, fmt.Errorf("artifact content binding mismatch")
			}
			if strings.HasSuffix(artifact.Role, ".sse") || artifact.Role == "sse" {
				if err := harness.ValidateEventEnvelopeV2(artifact.Bytes); err != nil {
					return nil, fmt.Errorf("observed SSE schema: %w", err)
				}
				var envelope semantic.EventEnvelope
				if err := json.Unmarshal(artifact.Bytes, &envelope); err != nil {
					return nil, err
				}
				if envelope.EventType == "approval.updated" {
					var data struct {
						ApprovalEvent semantic.ApprovalOutboxCommittedEventV1 `json:"approvalEvent"`
					}
					raw, _ := json.Marshal(envelope.Data)
					if err := json.Unmarshal(raw, &data); err != nil {
						return nil, err
					}
					if data.ApprovalEvent.EventID == "" || data.ApprovalEvent.AggregateVersion < 1 || envelope.Sequence < 1 {
						return nil, fmt.Errorf("approval metrics lack actual event/version/sequence")
					}
					commitKey := data.ApprovalEvent.AggregateID + "/" + envelope.EventID
					if !seenCommits[commitKey] {
						commits = append(commits, vs09CommitObservation{envelope.EventID, data.ApprovalEvent.EventID, data.ApprovalEvent.AggregateID, data.ApprovalEvent.AggregateVersion, envelope.Sequence, artifact.Ref})
						seenCommits[commitKey] = true
					}
				}
			}
		}
	}
	paired := false
	for role := range roles {
		if strings.HasPrefix(role, "before.") && roles["after."+strings.TrimPrefix(role, "before.")] {
			paired = true
		}
	}
	if !paired {
		return nil, fmt.Errorf("missing immutable before/after artifact binding")
	}
	if criterion != "VS09-A1" && criterion != "VS09-A2" && criterion != "VS09-A9" && len(commits) == 0 {
		return nil, fmt.Errorf("criterion requires real observed approval SSE/version")
	}
	return commits, nil
}

func TestVS09ApprovalEvidenceRejectsUnobservedAndTamperedRecords(t *testing.T) {
	// The adversarial starting point is a fresh real execution, not a fixture
	// whose self-declared pass could make the registry tautological.
	good := runFlowContextCriterion(t, "VS09-A1")
	for _, tc := range []struct {
		name   string
		mutate func(*flowContextObservedRun)
	}{
		{"no-test-run", func(r *flowContextObservedRun) { r.Events = nil }},
		{"missing-record", func(r *flowContextObservedRun) { r.Records = r.Records[1:] }},
		{"duplicate-record", func(r *flowContextObservedRun) { r.Records[1] = r.Records[0] }},
		{"skipped", func(r *flowContextObservedRun) {
			r.Events = append(r.Events, flowContextTestEvent{Action: "skip", Package: flowContextExecutions["VS09-A1"].Package, Test: flowContextExecutions["VS09-A1"].Test})
		}},
		{"failed", func(r *flowContextObservedRun) {
			r.Events = append(r.Events, flowContextTestEvent{Action: "fail", Package: flowContextExecutions["VS09-A1"].Package, Test: flowContextExecutions["VS09-A1"].Test})
		}},
		{"zero-match", func(r *flowContextObservedRun) {
			r.Events = []flowContextTestEvent{{Action: "pass", Package: flowContextExecutions["VS09-A1"].Package}}
		}},
		{"criterion", func(r *flowContextObservedRun) { r.Records[0].Criterion = "VS09-A13" }},
		{"challenge", func(r *flowContextObservedRun) { r.Records[0].Challenge = "fabricated" }},
		{"package", func(r *flowContextObservedRun) { r.Records[0].Package = "fabricated/package" }},
		{"binary", func(r *flowContextObservedRun) { r.Records[0].Binary = "fabricated.test" }},
		{"binary-digest", func(r *flowContextObservedRun) { r.Records[0].BinaryDigest = strings.Repeat("0", 64) }},
		{"test-id", func(r *flowContextObservedRun) { r.Records[0].ExecutionID = "TestFabricated" }},
		{"artifact-ref", func(r *flowContextObservedRun) { r.Records[0].Artifacts[0].Ref = "sha256:" + strings.Repeat("0", 64) }},
		{"artifact-bytes", func(r *flowContextObservedRun) { r.Records[0].Artifacts[0].Bytes = []byte("fabricated") }},
		{"artifact-rehashed", func(r *flowContextObservedRun) {
			artifact := &r.Records[0].Artifacts[0]
			artifact.Bytes = []byte("fabricated")
			digest := sha256.Sum256(artifact.Bytes)
			artifact.Ref = "sha256:" + hex.EncodeToString(digest[:])
		}},
		{"missing-artifacts", func(r *flowContextObservedRun) {
			for i := range r.Records {
				r.Records[i].Artifacts = nil
			}
		}},
		{"duplicate-artifact", func(r *flowContextObservedRun) {
			r.Records[0].Artifacts = append(r.Records[0].Artifacts, r.Records[0].Artifacts[0])
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, _ := json.Marshal(good)
			var changed flowContextObservedRun
			if err := json.Unmarshal(raw, &changed); err != nil {
				t.Fatal(err)
			}
			tc.mutate(&changed)
			if _, err := validateVS09ObservedRun("VS09-A1", flowContextExecutions["VS09-A1"], changed); err == nil {
				t.Fatal("tampered evidence accepted")
			}
		})
	}
}
