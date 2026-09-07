package contractharness_test

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	harness "codeflow/internal/contractharness"
	"codeflow/internal/protocol"
	"codeflow/internal/rflscvs08runner"
)

type vs08EvidenceRecord struct {
	Criterion                   string                                      `json:"criterion"`
	ImplementationTestID        string                                      `json:"implementationTestId"`
	ImplementationPackage       string                                      `json:"implementationPackage"`
	ExecutionPackage            string                                      `json:"executionPackage"`
	ExecutionBinary             string                                      `json:"executionBinary"`
	ExecutionID                 string                                      `json:"executionId"`
	Result                      string                                      `json:"result"`
	ExecutionCompleted          bool                                        `json:"executionCompleted"`
	SnapshotID                  string                                      `json:"snapshotId"`
	ComputedBasisID             string                                      `json:"computedBasisId"`
	GenerationID                string                                      `json:"generationId"`
	PackDigest                  string                                      `json:"packDigest"`
	ModelHostAuditApplicability rflscvs08runner.ModelHostAuditApplicability `json:"modelHostAuditApplicability"`
	ModelHostOutcome            string                                      `json:"modelHostOutcome"`
	MountAudit                  protocol.ModelHostIsolationEvidence         `json:"mountCapabilityAudit"`
	CapabilityAudit             protocol.ModelHostCapability                `json:"capabilityAudit"`
	RepositoryWriteAudit        []string                                    `json:"repositoryWriteAudit"`
	ObjectRefs                  []string                                    `json:"artifactRefs"`
	ModelHostLifecycle          []rflscvs08runner.ModelHostObservation      `json:"modelHostLifecycle,omitempty"`
	ConcurrentPackAudit         *rflscvs08runner.ConcurrentPackEditAudit    `json:"concurrentPackAudit,omitempty"`
	ConcurrentPackEditVerified  bool                                        `json:"concurrentPackEditVerified,omitempty"`
	runnerObserved              bool
}

const (
	vs08ImplementationPackage = "codeflow/internal/rflscvs08runner"
	vs08ExecutionPackage      = "codeflow/internal/contractharness"
)

func TestRFLSCR2VS08_EvidenceRegistry(t *testing.T) {
	if harness.VS08EvidenceRegistryID != "rflsc-r2-vs-08" {
		t.Fatalf("unexpected VS-08 evidence registry ID: %q", harness.VS08EvidenceRegistryID)
	}
	runners := map[string]func(*testing.T) rflscvs08runner.Evidence{
		"VS08-A1":  rflscvs08runner.RunA01,
		"VS08-A2":  rflscvs08runner.RunA02,
		"VS08-A3":  rflscvs08runner.RunA03,
		"VS08-A4":  rflscvs08runner.RunA04,
		"VS08-A5":  rflscvs08runner.RunA05,
		"VS08-A6":  rflscvs08runner.RunA06,
		"VS08-A7":  rflscvs08runner.RunA07,
		"VS08-A8":  rflscvs08runner.RunA08,
		"VS08-A9":  rflscvs08runner.RunA09,
		"VS08-A10": rflscvs08runner.RunA10,
	}
	if len(runners) != len(harness.VS08EvidenceCriteria) {
		t.Fatalf("VS-08 runner count=%d want=%d", len(runners), len(harness.VS08EvidenceCriteria))
	}
	expectedIDs := map[string]string{}
	for _, criterion := range harness.VS08EvidenceCriteria {
		expectedIDs[criterion] = expectedVS08ImplementationID(criterion)
	}
	records := make([]vs08EvidenceRecord, 0, len(harness.VS08EvidenceCriteria))
	for _, criterion := range harness.VS08EvidenceCriteria {
		runner := runners[criterion]
		if runner == nil {
			t.Fatalf("missing VS-08 runner for %s", criterion)
		}
		var record vs08EvidenceRecord
		passed := t.Run(criterion, func(st *testing.T) {
			shared := runner(st)
			if err := rflscvs08runner.ValidateEvidence(shared); err != nil {
				st.Fatalf("runner evidence: %v", err)
			}
			record = vs08Record(st, shared)
			if record.ImplementationTestID != expectedIDs[criterion] {
				st.Fatalf("implementation test identity = %q want %q", record.ImplementationTestID, expectedIDs[criterion])
			}
			if err := validateVS08EvidenceRecord(criterion, st.Name(), record); err != nil {
				st.Fatal(err)
			}
			record.runnerObserved = true
			record.ExecutionCompleted = true
			record.Result = "pass"
		})
		if !passed {
			t.Fatalf("criterion %s did not pass", criterion)
		}
		if !record.runnerObserved || record.Result != "pass" || !record.ExecutionCompleted {
			t.Fatalf("criterion %s was not a completed passing execution: %+v", criterion, record)
		}
		t.Logf("evidence=%s", mustJSONVS08(record))
		records = append(records, record)
	}
	if err := validateVS08RegistryRecords(records, harness.VS08EvidenceCriteria); err != nil {
		t.Fatal(err)
	}
}

func vs08Record(t *testing.T, shared rflscvs08runner.Evidence) vs08EvidenceRecord {
	t.Helper()
	return vs08EvidenceRecord{
		Criterion: shared.Criterion, ImplementationTestID: shared.ImplementationTestID, ImplementationPackage: shared.ImplementationPackage,
		ExecutionPackage: vs08ExecutionPackage, ExecutionBinary: filepath.Base(os.Args[0]), ExecutionID: t.Name(),
		SnapshotID: shared.SnapshotID, ComputedBasisID: shared.ComputedBasisID, GenerationID: shared.GenerationID, PackDigest: shared.PackDigest,
		ModelHostAuditApplicability: shared.ModelHostAuditApplicability, ModelHostOutcome: shared.ModelHostOutcome,
		MountAudit: cloneVS08IsolationEvidence(shared.MountAudit), CapabilityAudit: cloneVS08Capability(shared.CapabilityAudit), RepositoryWriteAudit: cloneVS08Strings(shared.RepositoryWriteAudit), ObjectRefs: cloneVS08Strings(shared.ObjectRefs),
		ModelHostLifecycle: cloneVS08Observations(shared.ModelHostLifecycle), ConcurrentPackAudit: cloneVS08ConcurrentPackAudit(shared.ConcurrentPackAudit), ConcurrentPackEditVerified: shared.ConcurrentPackEditVerified,
	}
}

func cloneVS08Strings(values []string) []string {
	if values == nil {
		return nil
	}
	clone := make([]string, len(values))
	copy(clone, values)
	return clone
}

func cloneVS08IsolationEvidence(value protocol.ModelHostIsolationEvidence) protocol.ModelHostIsolationEvidence {
	clone := value
	clone.RepositoryWriteAttempts = cloneVS08Strings(value.RepositoryWriteAttempts)
	if value.ResourceLimits != nil {
		limits := *value.ResourceLimits
		clone.ResourceLimits = &limits
	}
	return clone
}

func cloneVS08Capability(value protocol.ModelHostCapability) protocol.ModelHostCapability {
	clone := value
	clone.Capabilities = cloneVS08Strings(value.Capabilities)
	if value.IsolationProbe != nil {
		probe := *value.IsolationProbe
		clone.IsolationProbe = &probe
	}
	if value.ResourceLimits != nil {
		limits := *value.ResourceLimits
		clone.ResourceLimits = &limits
	}
	return clone
}

func cloneVS08Observation(value rflscvs08runner.ModelHostObservation) rflscvs08runner.ModelHostObservation {
	clone := value
	clone.Isolation = cloneVS08IsolationEvidence(value.Isolation)
	clone.Capability = cloneVS08Capability(value.Capability)
	return clone
}

func cloneVS08Observations(values []rflscvs08runner.ModelHostObservation) []rflscvs08runner.ModelHostObservation {
	if values == nil {
		return nil
	}
	clone := make([]rflscvs08runner.ModelHostObservation, len(values))
	for index := range values {
		clone[index] = cloneVS08Observation(values[index])
	}
	return clone
}

func TestVS08RecordDeepCopiesNestedModelHostEvidence(t *testing.T) {
	shared := rflscvs08runner.RunA03(t)
	shared.ModelHostLifecycle[0].Isolation.RepositoryWriteAttempts = []string{"source-lifecycle-attempt"}
	shared.ModelHostLifecycle[0].Isolation.ResourceLimits = &protocol.ModelHostResourceLimitEvidence{Version: 1, Backend: "source-lifecycle"}
	shared.ModelHostLifecycle[0].Capability.Capabilities = []string{"source-lifecycle-capability"}
	shared.ModelHostLifecycle[0].Capability.IsolationProbe = &protocol.ModelHostIsolationProbe{RepositoryReadAttempt: "source-lifecycle"}
	shared.ModelHostLifecycle[0].Capability.ResourceLimits = &protocol.ModelHostResourceLimitEvidence{Version: 1, Backend: "source-lifecycle-capability"}
	shared.MountAudit = shared.ModelHostLifecycle[0].Isolation
	shared.CapabilityAudit = shared.ModelHostLifecycle[0].Capability
	shared.RepositoryWriteAudit = []string{"source-audit-attempt"}
	shared.ObjectRefs = []string{"source-object-ref"}
	record := vs08Record(t, shared)
	if record.MountAudit.ResourceLimits == shared.MountAudit.ResourceLimits || record.CapabilityAudit.ResourceLimits == shared.CapabilityAudit.ResourceLimits || record.ModelHostLifecycle[0].Isolation.ResourceLimits == shared.ModelHostLifecycle[0].Isolation.ResourceLimits || record.ModelHostLifecycle[0].Capability.ResourceLimits == shared.ModelHostLifecycle[0].Capability.ResourceLimits || record.CapabilityAudit.IsolationProbe == shared.CapabilityAudit.IsolationProbe {
		t.Fatal("VS-08 record shares nested pointers with source evidence")
	}
	if record.MountAudit.ResourceLimits == record.ModelHostLifecycle[0].Isolation.ResourceLimits || record.CapabilityAudit.ResourceLimits == record.ModelHostLifecycle[0].Capability.ResourceLimits || record.CapabilityAudit.IsolationProbe == record.ModelHostLifecycle[0].Capability.IsolationProbe {
		t.Fatal("VS-08 record top-level audit shares nested pointers with lifecycle observation")
	}

	shared.MountAudit.RepositoryWriteAttempts[0] = "source-mutated"
	shared.MountAudit.ResourceLimits.Backend = "source-mutated"
	shared.CapabilityAudit.Capabilities[0] = "source-mutated"
	shared.CapabilityAudit.IsolationProbe.RepositoryReadAttempt = "source-mutated"
	shared.CapabilityAudit.ResourceLimits.Backend = "source-mutated"
	shared.ModelHostLifecycle[0].Isolation.RepositoryWriteAttempts[0] = "source-mutated"
	shared.ModelHostLifecycle[0].Isolation.ResourceLimits.Backend = "source-mutated"
	shared.ModelHostLifecycle[0].Capability.Capabilities[0] = "source-mutated"
	shared.ModelHostLifecycle[0].Capability.IsolationProbe.RepositoryReadAttempt = "source-mutated"
	shared.ModelHostLifecycle[0].Capability.ResourceLimits.Backend = "source-mutated"
	shared.RepositoryWriteAudit[0] = "source-mutated"
	shared.ObjectRefs[0] = "source-mutated"
	if record.MountAudit.ResourceLimits.Backend != "source-lifecycle" || record.CapabilityAudit.Capabilities[0] != "source-lifecycle-capability" || record.CapabilityAudit.IsolationProbe.RepositoryReadAttempt != "source-lifecycle" || record.ModelHostLifecycle[0].Isolation.RepositoryWriteAttempts[0] != "source-lifecycle-attempt" || record.ModelHostLifecycle[0].Capability.ResourceLimits.Backend != "source-lifecycle-capability" || record.RepositoryWriteAudit[0] != "source-audit-attempt" || record.ObjectRefs[0] != "source-object-ref" {
		t.Fatal("source evidence mutation changed converted VS-08 record")
	}

	record.MountAudit.RepositoryWriteAttempts[0] = "record-mutated"
	record.MountAudit.ResourceLimits.Backend = "record-mutated"
	record.CapabilityAudit.Capabilities[0] = "record-mutated"
	record.CapabilityAudit.IsolationProbe.RepositoryReadAttempt = "record-mutated"
	record.CapabilityAudit.ResourceLimits.Backend = "record-mutated"
	record.ModelHostLifecycle[0].Isolation.RepositoryWriteAttempts[0] = "record-mutated"
	record.ModelHostLifecycle[0].Capability.Capabilities[0] = "record-mutated"
	record.RepositoryWriteAudit[0] = "record-mutated"
	record.ObjectRefs[0] = "record-mutated"
	if shared.MountAudit.ResourceLimits.Backend != "source-mutated" || shared.CapabilityAudit.Capabilities[0] != "source-mutated" || shared.CapabilityAudit.IsolationProbe.RepositoryReadAttempt != "source-mutated" || shared.ModelHostLifecycle[0].Isolation.RepositoryWriteAttempts[0] != "source-mutated" || shared.ModelHostLifecycle[0].Capability.Capabilities[0] != "source-mutated" || shared.RepositoryWriteAudit[0] != "source-mutated" || shared.ObjectRefs[0] != "source-mutated" {
		t.Fatal("converted VS-08 record mutation changed source evidence")
	}
}

func TestVS08RecordDeepCopiesConcurrentPackAuditPreservesBinding(t *testing.T) {
	shared := rflscvs08runner.RunA10(t)
	if shared.ConcurrentPackAudit == nil {
		t.Fatal("A10 did not produce a concurrent-pack audit")
	}
	shared.ConcurrentPackAudit.First.Capability.Capabilities = []string{"source-first-capability"}
	shared.ConcurrentPackAudit.Later.Capability.Capabilities = []string{"source-later-capability"}
	record := vs08Record(t, shared)
	if err := validateVS08ConcurrentPackEditAudit(shared.SnapshotID, shared.ComputedBasisID, shared.GenerationID, shared.PackDigest, *record.ConcurrentPackAudit); err != nil {
		t.Fatalf("converted concurrent-pack audit lost its exact Core binding: %v", err)
	}
	if record.ConcurrentPackAudit.First.Capability.ResourceLimits == shared.ConcurrentPackAudit.First.Capability.ResourceLimits || record.ConcurrentPackAudit.First.Capability.IsolationProbe == shared.ConcurrentPackAudit.First.Capability.IsolationProbe || record.ConcurrentPackAudit.Later.Capability.ResourceLimits == shared.ConcurrentPackAudit.Later.Capability.ResourceLimits || record.ConcurrentPackAudit.Later.Capability.IsolationProbe == shared.ConcurrentPackAudit.Later.Capability.IsolationProbe {
		t.Fatal("converted concurrent-pack audit shares nested pointers with source evidence")
	}

	record.ConcurrentPackAudit.First.Capability.Capabilities[0] = "record-mutated"
	record.ConcurrentPackAudit.First.Capability.IsolationProbe.RepositoryReadAttempt = "record-mutated"
	record.ConcurrentPackAudit.First.Capability.ResourceLimits.Backend = "record-mutated"
	record.ConcurrentPackAudit.Later.Capability.Capabilities[0] = "record-mutated"
	record.ConcurrentPackAudit.Later.Capability.IsolationProbe.RepositoryReadAttempt = "record-mutated"
	record.ConcurrentPackAudit.Later.Capability.ResourceLimits.Backend = "record-mutated"
	if shared.ConcurrentPackAudit.First.Capability.Capabilities[0] != "source-first-capability" || shared.ConcurrentPackAudit.First.Capability.IsolationProbe.RepositoryReadAttempt == "record-mutated" || shared.ConcurrentPackAudit.First.Capability.ResourceLimits.Backend == "record-mutated" || shared.ConcurrentPackAudit.Later.Capability.Capabilities[0] != "source-later-capability" || shared.ConcurrentPackAudit.Later.Capability.IsolationProbe.RepositoryReadAttempt == "record-mutated" || shared.ConcurrentPackAudit.Later.Capability.ResourceLimits.Backend == "record-mutated" {
		t.Fatal("converted concurrent-pack audit mutation changed source evidence")
	}
	shared.ConcurrentPackAudit.First.Capability.Capabilities[0] = "source-mutated"
	shared.ConcurrentPackAudit.First.Capability.IsolationProbe.RepositoryReadAttempt = "source-mutated"
	shared.ConcurrentPackAudit.First.Capability.ResourceLimits.Backend = "source-mutated"
	if record.ConcurrentPackAudit.First.Capability.Capabilities[0] == "source-mutated" || record.ConcurrentPackAudit.First.Capability.IsolationProbe.RepositoryReadAttempt == "source-mutated" || record.ConcurrentPackAudit.First.Capability.ResourceLimits.Backend == "source-mutated" {
		t.Fatal("source concurrent-pack audit mutation changed converted record")
	}
}

func validateVS08ArtifactRefs(record vs08EvidenceRecord) error {
	expected := []string{
		"snapshot:" + record.SnapshotID,
		"basis:" + record.ComputedBasisID,
		"generation:" + record.GenerationID,
		"pack:" + record.PackDigest,
		"evidence:e-submit",
	}
	if len(record.ObjectRefs) != len(expected) {
		return fmt.Errorf("VS-08 artifact refs must contain exactly %d entries", len(expected))
	}
	allowed := make(map[string]struct{}, len(expected))
	for _, ref := range expected {
		allowed[ref] = struct{}{}
	}
	seen := make(map[string]struct{}, len(record.ObjectRefs))
	for _, ref := range record.ObjectRefs {
		if _, ok := allowed[ref]; !ok {
			return fmt.Errorf("VS-08 artifact ref %q is not bound to the evidence identity", ref)
		}
		if _, duplicate := seen[ref]; duplicate {
			return fmt.Errorf("VS-08 duplicate artifact ref %q", ref)
		}
		seen[ref] = struct{}{}
	}
	for _, ref := range expected {
		if _, ok := seen[ref]; !ok {
			return fmt.Errorf("VS-08 missing artifact ref %q", ref)
		}
	}
	return nil
}

func validateVS08EvidenceRecord(criterion, executionID string, record vs08EvidenceRecord) error {
	if record.Criterion != criterion || record.ImplementationTestID != expectedVS08ImplementationID(criterion) || record.ImplementationPackage != vs08ImplementationPackage || record.ExecutionPackage != vs08ExecutionPackage || record.ExecutionID != executionID || strings.TrimSpace(record.ExecutionID) == "" || strings.TrimSpace(executionID) == "" || strings.TrimSpace(record.ExecutionBinary) == "" {
		return fmt.Errorf("VS-08 execution identity mismatch: %+v", record)
	}
	if record.SnapshotID == "" || record.ComputedBasisID == "" || record.GenerationID == "" || record.PackDigest == "" {
		return fmt.Errorf("VS-08 immutable identity is incomplete: %+v", record)
	}
	if err := validateVS08ArtifactRefs(record); err != nil {
		return err
	}
	if len(record.RepositoryWriteAudit) != 0 {
		return fmt.Errorf("VS-08 repository write or artifact audit is incomplete: %+v", record)
	}
	applicability, outcome, ok := rflscvs08runner.ModelHostAuditExpectationForCriterion(criterion)
	if !ok {
		return fmt.Errorf("VS-08 has no model-host applicability for %s", criterion)
	}
	if record.ModelHostAuditApplicability != applicability || record.ModelHostOutcome != outcome {
		return fmt.Errorf("VS-08 %s model-host applicability/outcome = %q/%q want %q/%q", criterion, record.ModelHostAuditApplicability, record.ModelHostOutcome, applicability, outcome)
	}
	if applicability == rflscvs08runner.ModelHostAuditNotApplicable {
		if !reflect.DeepEqual(record.MountAudit, protocol.ModelHostIsolationEvidence{}) || !reflect.DeepEqual(record.CapabilityAudit, protocol.ModelHostCapability{}) || len(record.ModelHostLifecycle) != 0 || record.ConcurrentPackAudit != nil || record.ConcurrentPackEditVerified {
			return fmt.Errorf("VS-08 %s is not model-host applicable but contains measured host evidence", criterion)
		}
		return nil
	}
	expectedModes := expectedVS08ModelHostModes(criterion)
	if len(record.ModelHostLifecycle) != len(expectedModes) {
		return fmt.Errorf("VS-08 %s observed lifecycle has %d entries want %d", criterion, len(record.ModelHostLifecycle), len(expectedModes))
	}
	if criterion == "VS08-A10" {
		if record.ConcurrentPackAudit == nil || !record.ConcurrentPackEditVerified || record.ConcurrentPackEditVerified != record.ConcurrentPackAudit.Verified() {
			return fmt.Errorf("VS-08 A10 concurrent-pack audit is missing or boolean-only: %+v", record)
		}
	} else if record.ConcurrentPackAudit != nil || record.ConcurrentPackEditVerified {
		return fmt.Errorf("VS-08 %s contains unrelated concurrent-pack host evidence", criterion)
	}
	seenModes := make(map[string]struct{}, len(record.ModelHostLifecycle))
	for _, observation := range record.ModelHostLifecycle {
		if _, duplicate := seenModes[observation.Mode]; duplicate {
			return fmt.Errorf("VS-08 %s duplicate lifecycle mode %q", criterion, observation.Mode)
		}
		seenModes[observation.Mode] = struct{}{}
		if err := validateVS08ModelHostObservation(record.PackDigest, observation); err != nil {
			return fmt.Errorf("VS-08 %s lifecycle observation: %w", criterion, err)
		}
	}
	for _, mode := range expectedModes {
		if _, present := seenModes[mode]; !present {
			return fmt.Errorf("VS-08 %s missing lifecycle mode %q", criterion, mode)
		}
	}
	primaryMode := expectedVS08ModelHostPrimaryMode(criterion)
	var primary *rflscvs08runner.ModelHostObservation
	for index := range record.ModelHostLifecycle {
		if record.ModelHostLifecycle[index].Mode == primaryMode {
			primary = &record.ModelHostLifecycle[index]
			break
		}
	}
	if primary == nil {
		return fmt.Errorf("VS-08 %s is missing its %s primary lifecycle observation", criterion, primaryMode)
	}
	if !reflect.DeepEqual(record.MountAudit, primary.Isolation) || !reflect.DeepEqual(record.CapabilityAudit, primary.Capability) || len(record.RepositoryWriteAudit) != len(primary.Isolation.RepositoryWriteAttempts) {
		return fmt.Errorf("VS-08 %s top-level host audit does not match primary observation", criterion)
	}
	for index := range record.RepositoryWriteAudit {
		if record.RepositoryWriteAudit[index] != primary.Isolation.RepositoryWriteAttempts[index] {
			return fmt.Errorf("VS-08 %s top-level repository audit does not match primary observation", criterion)
		}
	}
	if criterion == "VS08-A10" {
		if err := validateVS08ConcurrentPackEditAudit(record.SnapshotID, record.ComputedBasisID, record.GenerationID, record.PackDigest, *record.ConcurrentPackAudit); err != nil {
			return fmt.Errorf("VS-08 A10 concurrent-pack audit: %w", err)
		}
		if record.ConcurrentPackAudit.First.SnapshotID != record.SnapshotID || record.ConcurrentPackAudit.First.ComputedBasisID != record.ComputedBasisID || record.ConcurrentPackAudit.First.GenerationID != record.GenerationID || record.ConcurrentPackAudit.First.PackDigest != record.PackDigest {
			return fmt.Errorf("VS-08 A10 concurrent-pack first execution does not match primary host evidence")
		}
	}
	return nil
}

func validateVS08ConcurrentPackEditAudit(snapshotID, computedBasisID, generationID, packDigest string, audit rflscvs08runner.ConcurrentPackEditAudit) error {
	return rflscvs08runner.ValidateConcurrentPackEditAudit(snapshotID, computedBasisID, generationID, packDigest, audit)
}

func expectedVS08ModelHostModes(criterion string) []string {
	switch criterion {
	case "VS08-A3":
		return []string{"success", "timeout", "cancel"}
	case "VS08-A7":
		return []string{"timeout"}
	case "VS08-A10":
		return []string{"success", "failure", "crash", "timeout", "cancel"}
	default:
		return nil
	}
}

func TestValidateVS08EvidenceRecordRejectsIdentityAndArtifactRefMutations(t *testing.T) {
	bases := []vs08EvidenceRecord{vs08Record(t, rflscvs08runner.RunA01(t)), vs08Record(t, rflscvs08runner.RunA03(t))}
	mutations := []struct {
		name   string
		mutate func(*vs08EvidenceRecord)
	}{
		{name: "implementation test alias", mutate: func(value *vs08EvidenceRecord) { value.ImplementationTestID = expectedVS08ImplementationID("VS08-A2") }},
		{name: "empty execution id", mutate: func(value *vs08EvidenceRecord) { value.ExecutionID = "" }},
		{name: "empty artifact ref", mutate: func(value *vs08EvidenceRecord) { value.ObjectRefs[0] = "" }},
		{name: "duplicate artifact ref", mutate: func(value *vs08EvidenceRecord) { value.ObjectRefs[4] = value.ObjectRefs[0] }},
		{name: "wrong artifact prefix", mutate: func(value *vs08EvidenceRecord) { value.ObjectRefs[0] = "tree:" + value.SnapshotID }},
		{name: "wrong artifact value", mutate: func(value *vs08EvidenceRecord) { value.ObjectRefs[0] = "snapshot:other" }},
		{name: "other identity alias", mutate: func(value *vs08EvidenceRecord) { value.ObjectRefs[0] = "snapshot:" + value.ComputedBasisID }},
		{name: "extra artifact ref", mutate: func(value *vs08EvidenceRecord) { value.ObjectRefs = append(value.ObjectRefs, "extra:unexpected") }},
		{name: "missing artifact ref", mutate: func(value *vs08EvidenceRecord) { value.ObjectRefs = value.ObjectRefs[:4] }},
	}
	for _, base := range bases {
		for _, mutation := range mutations {
			t.Run(base.Criterion+"/"+mutation.name, func(t *testing.T) {
				mutated := base
				mutated.ObjectRefs = cloneVS08Strings(base.ObjectRefs)
				mutation.mutate(&mutated)
				if err := validateVS08EvidenceRecord(mutated.Criterion, mutated.ExecutionID, mutated); err == nil {
					t.Fatalf("%s accepted fabricated identity/artifact refs", mutation.name)
				}
			})
		}
	}
}

func expectedVS08ModelHostPrimaryMode(criterion string) string {
	switch criterion {
	case "VS08-A7":
		return "timeout"
	case "VS08-A3", "VS08-A10":
		return "success"
	default:
		return ""
	}
}

func validateVS08ModelHostObservation(packDigest string, observation rflscvs08runner.ModelHostObservation) error {
	if observation.Applicability != rflscvs08runner.ModelHostAuditApplicable || observation.Mode == "" || observation.RequestID == "" {
		return fmt.Errorf("model-host observation applicability or identity is incomplete")
	}
	if observation.Isolation.PackDigest != packDigest || observation.Isolation.ReceivedRequestID != observation.RequestID || observation.Isolation.ReceivedPackDigest != packDigest || observation.Isolation.SourceDelivery != "bounded_evidence_pack" || observation.Isolation.SourceMount != "not_mounted" || observation.Isolation.WorkingDirectoryMode != "process_private_disposable" || observation.Isolation.WorkingDirectoryPermission != "0700" || !observation.Isolation.Disposable || observation.Isolation.RepositoryPathExposed || observation.Isolation.RepositoryWriteCapability || len(observation.Isolation.RepositoryWriteAttempts) != 0 || observation.Isolation.RepositoryWriteAuditStatus != protocol.ModelHostRepositoryWriteAuditCapabilityEnforced || observation.Isolation.DisposableWriteAttempt != "blocked" || !observation.Isolation.CoreTrustedProbe || !observation.Isolation.CleanupVerified || observation.Isolation.IsolationBackend != "sandbox-exec" || observation.Isolation.EnforcementStatus != "enforced" || observation.Isolation.NetworkPolicy != "deny_all" || observation.Isolation.RepositoryReadAttempt != "blocked" || observation.Isolation.RepositoryWriteAttempt != "blocked" || observation.Isolation.NetworkAttempt != "blocked" || observation.Isolation.SentinelBeforeDigest == "" || observation.Isolation.SentinelBeforeDigest != observation.Isolation.SentinelAfterDigest || !observation.Isolation.SentinelUnchanged || observation.Isolation.RuntimePolicyBinding != protocol.ModelHostRuntimePolicyBindingExact || observation.Isolation.ProbeScope != protocol.ModelHostProbeScopeSharedDenyBase || observation.Isolation.RuntimePolicySharedBaseDigest == "" || observation.Isolation.ProbePolicyDigest == "" || observation.Isolation.ProbeSharedBaseDigest == "" || observation.Isolation.ProbeSharedBaseDigest != observation.Isolation.RuntimePolicySharedBaseDigest {
		return fmt.Errorf("model-host isolation observation is incomplete")
	}
	if observation.Capability.Status != "measured" || !observation.Capability.Measured || !observation.Capability.SchemaConstrained || !observation.Capability.Cancellation || !observation.Capability.IsMeasured() {
		return fmt.Errorf("model-host capability observation is not measured")
	}
	if err := validateVS08ModelHostAuditBinding(observation.Capability, observation.Isolation); err != nil {
		return fmt.Errorf("model-host policy/resource observation: %w", err)
	}
	switch observation.Mode {
	case "success":
		if observation.Error != "" || observation.ErrorClass != "" || observation.Isolation.TerminalStatus != "success" {
			return fmt.Errorf("success model-host observation is invalid")
		}
	case "failure", "crash", "timeout", "cancel":
		if observation.Error == "" || observation.ErrorClass != observation.Mode || observation.Isolation.TerminalStatus != observation.Mode {
			return fmt.Errorf("%s model-host observation is invalid", observation.Mode)
		}
	default:
		return fmt.Errorf("unknown model-host lifecycle mode %q", observation.Mode)
	}
	return nil
}

func validateVS08ModelHostAuditBinding(capability protocol.ModelHostCapability, isolation protocol.ModelHostIsolationEvidence) error {
	if !capability.IsMeasured() {
		return fmt.Errorf("capability is not semantically measured")
	}
	if err := protocol.ValidateModelHostIsolationEvidence(isolation); err != nil {
		return fmt.Errorf("isolation evidence is semantically invalid: %w", err)
	}
	if err := protocol.ValidateModelHostPolicyIdentityBinding(capability, isolation); err != nil {
		return fmt.Errorf("runtime and trusted-probe policy identity binding: %w", err)
	}
	if capability.ResourceLimits == nil || isolation.ResourceLimits == nil {
		return fmt.Errorf("resource-limit evidence is missing")
	}
	if err := protocol.ValidateModelHostResourceLimitEvidence(*capability.ResourceLimits); err != nil {
		return fmt.Errorf("capability resource limits: %w", err)
	}
	if err := protocol.ValidateModelHostResourceLimitEvidence(*isolation.ResourceLimits); err != nil {
		return fmt.Errorf("isolation resource limits: %w", err)
	}
	for _, evidence := range []*protocol.ModelHostResourceLimitEvidence{capability.ResourceLimits, isolation.ResourceLimits} {
		if evidence.EnforcementStatus != protocol.ModelHostResourceEnforcementEnforced || evidence.Backend != protocol.ModelHostResourceBackendDarwinHostTree || evidence.Applied.ProcessCount != 1 || evidence.Declared.CPUTimeSeconds != evidence.Applied.CPUTimeSeconds || evidence.Declared.MemoryBytes != evidence.Applied.MemoryBytes {
			return fmt.Errorf("resource limits are not exactly applied and enforced")
		}
	}
	if *capability.ResourceLimits != *isolation.ResourceLimits {
		return fmt.Errorf("capability and isolation resource limits differ")
	}
	return nil
}

func TestVS08A10RegistryRejectsEveryRuntimePolicyIdentityMutation(t *testing.T) {
	shared := rflscvs08runner.RunA03(t)
	base := vs08Record(t, shared)
	newDigest := "sha256:" + strings.Repeat("f", 64)
	tests := []struct {
		name   string
		mutate func(*vs08EvidenceRecord)
	}{
		{name: "capability runtime policy binding", mutate: func(value *vs08EvidenceRecord) {
			value.CapabilityAudit.RuntimePolicyBinding = "tampered_runtime_binding"
		}},
		{name: "capability runtime shared-base digest", mutate: func(value *vs08EvidenceRecord) {
			value.CapabilityAudit.RuntimePolicySharedBaseDigest = newDigest
			value.CapabilityAudit.ProbeSharedBaseDigest = newDigest
		}},
		{name: "capability probe scope", mutate: func(value *vs08EvidenceRecord) { value.CapabilityAudit.ProbeScope = "tampered_probe_scope" }},
		{name: "capability probe policy digest", mutate: func(value *vs08EvidenceRecord) { value.CapabilityAudit.ProbePolicyDigest = newDigest }},
		{name: "capability probe shared-base digest", mutate: func(value *vs08EvidenceRecord) {
			value.CapabilityAudit.RuntimePolicySharedBaseDigest = newDigest
			value.CapabilityAudit.ProbeSharedBaseDigest = newDigest
		}},
		{name: "isolation runtime policy binding", mutate: func(value *vs08EvidenceRecord) { value.MountAudit.RuntimePolicyBinding = "tampered_runtime_binding" }},
		{name: "isolation runtime shared-base digest", mutate: func(value *vs08EvidenceRecord) {
			value.MountAudit.RuntimePolicySharedBaseDigest = newDigest
			value.MountAudit.ProbeSharedBaseDigest = newDigest
		}},
		{name: "isolation probe scope", mutate: func(value *vs08EvidenceRecord) { value.MountAudit.ProbeScope = "tampered_probe_scope" }},
		{name: "isolation probe policy digest", mutate: func(value *vs08EvidenceRecord) { value.MountAudit.ProbePolicyDigest = newDigest }},
		{name: "isolation probe shared-base digest", mutate: func(value *vs08EvidenceRecord) {
			value.MountAudit.RuntimePolicySharedBaseDigest = newDigest
			value.MountAudit.ProbeSharedBaseDigest = newDigest
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mutated := base
			mutated.RepositoryWriteAudit = append([]string(nil), base.RepositoryWriteAudit...)
			mutated.ObjectRefs = append([]string(nil), base.ObjectRefs...)
			test.mutate(&mutated)
			if err := validateVS08EvidenceRecord(mutated.Criterion, mutated.ExecutionID, mutated); err == nil {
				t.Fatalf("policy identity mutation %q was accepted", test.name)
			}
		})
	}
}

func TestVS08A10RegistryRejectsLifecyclePolicyIdentityMutation(t *testing.T) {
	shared := rflscvs08runner.RunA10(t)
	base := vs08Record(t, shared)
	newDigest := "sha256:" + strings.Repeat("e", 64)
	tests := []struct {
		name   string
		mutate func(*rflscvs08runner.ModelHostObservation)
	}{
		{name: "capability runtime policy binding", mutate: func(value *rflscvs08runner.ModelHostObservation) {
			value.Capability.RuntimePolicyBinding = "tampered_runtime_binding"
		}},
		{name: "capability runtime shared-base digest", mutate: func(value *rflscvs08runner.ModelHostObservation) {
			value.Capability.RuntimePolicySharedBaseDigest = newDigest
			value.Capability.ProbeSharedBaseDigest = newDigest
		}},
		{name: "capability probe scope", mutate: func(value *rflscvs08runner.ModelHostObservation) {
			value.Capability.ProbeScope = "tampered_probe_scope"
		}},
		{name: "capability probe policy digest", mutate: func(value *rflscvs08runner.ModelHostObservation) { value.Capability.ProbePolicyDigest = newDigest }},
		{name: "capability probe shared-base digest", mutate: func(value *rflscvs08runner.ModelHostObservation) {
			value.Capability.RuntimePolicySharedBaseDigest = newDigest
			value.Capability.ProbeSharedBaseDigest = newDigest
		}},
		{name: "isolation runtime policy binding", mutate: func(value *rflscvs08runner.ModelHostObservation) {
			value.Isolation.RuntimePolicyBinding = "tampered_runtime_binding"
		}},
		{name: "isolation runtime shared-base digest", mutate: func(value *rflscvs08runner.ModelHostObservation) {
			value.Isolation.RuntimePolicySharedBaseDigest = newDigest
			value.Isolation.ProbeSharedBaseDigest = newDigest
		}},
		{name: "isolation probe scope", mutate: func(value *rflscvs08runner.ModelHostObservation) { value.Isolation.ProbeScope = "tampered_probe_scope" }},
		{name: "isolation probe policy digest", mutate: func(value *rflscvs08runner.ModelHostObservation) { value.Isolation.ProbePolicyDigest = newDigest }},
		{name: "isolation probe shared-base digest", mutate: func(value *rflscvs08runner.ModelHostObservation) {
			value.Isolation.RuntimePolicySharedBaseDigest = newDigest
			value.Isolation.ProbeSharedBaseDigest = newDigest
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mutated := base
			mutated.RepositoryWriteAudit = append([]string(nil), base.RepositoryWriteAudit...)
			mutated.ObjectRefs = append([]string(nil), base.ObjectRefs...)
			mutated.ModelHostLifecycle = append([]rflscvs08runner.ModelHostObservation(nil), base.ModelHostLifecycle...)
			test.mutate(&mutated.ModelHostLifecycle[0])
			if err := validateVS08EvidenceRecord(mutated.Criterion, mutated.ExecutionID, mutated); err == nil {
				t.Fatalf("lifecycle policy identity mutation %q was accepted", test.name)
			}
		})
	}
}

func TestVS08EvidenceRegistryRejectsModelHostApplicabilityMutation(t *testing.T) {
	tests := []struct {
		name   string
		base   func(*testing.T) rflscvs08runner.Evidence
		mutate func(*vs08EvidenceRecord)
	}{
		{name: "A1 measured host claim", base: rflscvs08runner.RunA01, mutate: func(value *vs08EvidenceRecord) {
			value.ModelHostAuditApplicability = rflscvs08runner.ModelHostAuditApplicable
			value.ModelHostOutcome = "observed"
		}},
		{name: "A4 unavailable contradiction", base: rflscvs08runner.RunA04, mutate: func(value *vs08EvidenceRecord) {
			value.ModelHostOutcome = "observed"
		}},
		{name: "A3 missing observed modes", base: rflscvs08runner.RunA03, mutate: func(value *vs08EvidenceRecord) {
			value.ModelHostLifecycle = append([]rflscvs08runner.ModelHostObservation(nil), value.ModelHostLifecycle[:1]...)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			shared := test.base(t)
			record := vs08Record(t, shared)
			test.mutate(&record)
			if err := validateVS08EvidenceRecord(record.Criterion, record.ExecutionID, record); err == nil {
				t.Fatalf("model-host applicability mutation was accepted: %+v", record)
			}
		})
	}
}

func TestVS08EvidenceRecordSerializesModelHostApplicability(t *testing.T) {
	record := vs08Record(t, rflscvs08runner.RunA01(t))
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]any
	if err := json.Unmarshal(data, &object); err != nil {
		t.Fatal(err)
	}
	if object["modelHostAuditApplicability"] != string(rflscvs08runner.ModelHostAuditNotApplicable) || object["modelHostOutcome"] != "not_applicable" {
		t.Fatalf("serialized VS-08 applicability is incomplete: %s", data)
	}
}

func cloneVS08ConcurrentPackExecution(value rflscvs08runner.ConcurrentPackExecutionEvidence) rflscvs08runner.ConcurrentPackExecutionEvidence {
	clone := value
	clone.Isolation = cloneVS08IsolationEvidence(value.Isolation)
	clone.Capability = cloneVS08Capability(value.Capability)
	return clone
}

func cloneVS08ConcurrentPackAudit(value *rflscvs08runner.ConcurrentPackEditAudit) *rflscvs08runner.ConcurrentPackEditAudit {
	if value == nil {
		return nil
	}
	clone := *value
	clone.First = cloneVS08ConcurrentPackExecution(value.First)
	clone.Later = cloneVS08ConcurrentPackExecution(value.Later)
	return &clone
}

func TestVS08A10RegistryRejectsConcurrentPackAuditMutation(t *testing.T) {
	base := vs08Record(t, rflscvs08runner.RunA10(t))
	tests := []struct {
		name   string
		mutate func(*vs08EvidenceRecord)
	}{
		{name: "boolean only", mutate: func(value *vs08EvidenceRecord) { value.ConcurrentPackAudit = nil }},
		{name: "missing first execution", mutate: func(value *vs08EvidenceRecord) {
			value.ConcurrentPackAudit.First = rflscvs08runner.ConcurrentPackExecutionEvidence{}
		}},
		{name: "missing later execution", mutate: func(value *vs08EvidenceRecord) {
			value.ConcurrentPackAudit.Later = rflscvs08runner.ConcurrentPackExecutionEvidence{}
		}},
		{name: "reused host identity", mutate: func(value *vs08EvidenceRecord) {
			value.ConcurrentPackAudit.Later.HostIdentity = value.ConcurrentPackAudit.First.HostIdentity
		}},
		{name: "arbitrary distinct host identity", mutate: func(value *vs08EvidenceRecord) {
			value.ConcurrentPackAudit.Later.HostIdentity = "model-host-ffffffffffffffff"
		}},
		{name: "reused request identity", mutate: func(value *vs08EvidenceRecord) {
			value.ConcurrentPackAudit.Later.RequestID = value.ConcurrentPackAudit.First.RequestID
		}},
		{name: "wrong receipt", mutate: func(value *vs08EvidenceRecord) {
			value.ConcurrentPackAudit.First.Isolation.ReceivedRequestID = "request-other"
		}},
		{name: "unchecked cleanup", mutate: func(value *vs08EvidenceRecord) { value.ConcurrentPackAudit.Later.Isolation.CleanupVerified = false }},
		{name: "identity alias", mutate: func(value *vs08EvidenceRecord) {
			value.ConcurrentPackAudit.Later.EvidencePackID = value.ConcurrentPackAudit.First.EvidencePackID
		}},
		{name: "identity proof alias", mutate: func(value *vs08EvidenceRecord) {
			value.ConcurrentPackAudit.Later.PackContentDigest = value.ConcurrentPackAudit.First.PackContentDigest
		}},
		{name: "policy mutation", mutate: func(value *vs08EvidenceRecord) { value.ConcurrentPackAudit.Later.Capability.ProbeScope = "tampered" }},
		{name: "resource mutation", mutate: func(value *vs08EvidenceRecord) {
			value.ConcurrentPackAudit.Later.Capability.ResourceLimits.Applied.MemoryBytes++
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mutated := base
			mutated.ConcurrentPackAudit = cloneVS08ConcurrentPackAudit(base.ConcurrentPackAudit)
			test.mutate(&mutated)
			if err := validateVS08EvidenceRecord(mutated.Criterion, mutated.ExecutionID, mutated); err == nil {
				t.Fatalf("concurrent-pack audit mutation %q was accepted", test.name)
			}
		})
	}
}

func TestVS08A10RegistryRejectsConcurrentPackAuditJSONRoundTrip(t *testing.T) {
	base := vs08Record(t, rflscvs08runner.RunA10(t))
	data, err := json.Marshal(base.ConcurrentPackAudit)
	if err != nil {
		t.Fatalf("marshal concurrent-pack audit: %v", err)
	}
	var roundTripped rflscvs08runner.ConcurrentPackEditAudit
	if err := json.Unmarshal(data, &roundTripped); err != nil {
		t.Fatalf("unmarshal concurrent-pack audit: %v", err)
	}
	if err := validateVS08ConcurrentPackEditAudit(base.SnapshotID, base.ComputedBasisID, base.GenerationID, base.PackDigest, roundTripped); err == nil {
		t.Fatal("JSON round-trip fabricated exact Core host authorities")
	}
}

func validateVS08RegistryRecords(records []vs08EvidenceRecord, expected []string) error {
	expectedSet := make(map[string]struct{}, len(expected))
	counts := make(map[string]int, len(expected))
	for _, criterion := range expected {
		if _, exists := expectedSet[criterion]; exists {
			return fmt.Errorf("VS-08 expected criteria contain duplicate %q", criterion)
		}
		expectedSet[criterion] = struct{}{}
	}
	for _, record := range records {
		if _, ok := expectedSet[record.Criterion]; !ok {
			return fmt.Errorf("VS-08 registry has unknown or zero-match criterion %q", record.Criterion)
		}
		counts[record.Criterion]++
		if !record.runnerObserved || !record.ExecutionCompleted || record.Result != "pass" {
			return fmt.Errorf("VS-08 registry has not-run criterion %q", record.Criterion)
		}
		if err := validateVS08EvidenceRecord(record.Criterion, record.ExecutionID, record); err != nil {
			return fmt.Errorf("VS-08 registry record %s: %w", record.Criterion, err)
		}
		applicability, outcome, ok := rflscvs08runner.ModelHostAuditExpectationForCriterion(record.Criterion)
		if !ok || record.ModelHostAuditApplicability != applicability || record.ModelHostOutcome != outcome {
			return fmt.Errorf("VS-08 registry has invalid model-host applicability for %s", record.Criterion)
		}
		if applicability == rflscvs08runner.ModelHostAuditNotApplicable && (len(record.ModelHostLifecycle) != 0 || !reflect.DeepEqual(record.MountAudit, protocol.ModelHostIsolationEvidence{}) || !reflect.DeepEqual(record.CapabilityAudit, protocol.ModelHostCapability{}) || record.ConcurrentPackAudit != nil || record.ConcurrentPackEditVerified) {
			return fmt.Errorf("VS-08 registry has host evidence for non-applicable criterion %s", record.Criterion)
		}
		if record.Criterion == "VS08-A10" {
			if record.ConcurrentPackAudit == nil || !record.ConcurrentPackEditVerified || record.ConcurrentPackEditVerified != record.ConcurrentPackAudit.Verified() {
				return fmt.Errorf("VS-08 registry A10 concurrent-pack audit is missing or boolean-only")
			}
			if err := validateVS08ConcurrentPackEditAudit(record.SnapshotID, record.ComputedBasisID, record.GenerationID, record.PackDigest, *record.ConcurrentPackAudit); err != nil {
				return fmt.Errorf("VS-08 registry A10 concurrent-pack audit: %w", err)
			}
		}
	}
	if len(records) != len(expected) {
		return fmt.Errorf("VS-08 registry has missing or duplicate records: got %d want %d", len(records), len(expected))
	}
	for _, criterion := range expected {
		if counts[criterion] != 1 {
			return fmt.Errorf("VS-08 registry requires exactly one record for %s, got %d", criterion, counts[criterion])
		}
	}
	return nil
}

func TestVS08RegistryRejectsFabricatedObservedRecordIdentityAndRefs(t *testing.T) {
	base := vs08Record(t, rflscvs08runner.RunA01(t))
	base.runnerObserved = true
	base.ExecutionCompleted = true
	base.Result = "pass"
	mutations := []struct {
		name   string
		mutate func(*vs08EvidenceRecord)
	}{
		{name: "implementation test alias", mutate: func(value *vs08EvidenceRecord) { value.ImplementationTestID = expectedVS08ImplementationID("VS08-A2") }},
		{name: "empty execution id", mutate: func(value *vs08EvidenceRecord) { value.ExecutionID = "" }},
		{name: "empty artifact ref", mutate: func(value *vs08EvidenceRecord) { value.ObjectRefs[0] = "" }},
		{name: "duplicate artifact ref", mutate: func(value *vs08EvidenceRecord) { value.ObjectRefs[4] = value.ObjectRefs[0] }},
		{name: "wrong artifact prefix", mutate: func(value *vs08EvidenceRecord) { value.ObjectRefs[0] = "tree:" + value.SnapshotID }},
		{name: "wrong artifact value", mutate: func(value *vs08EvidenceRecord) { value.ObjectRefs[0] = "snapshot:other" }},
		{name: "other identity alias", mutate: func(value *vs08EvidenceRecord) { value.ObjectRefs[0] = "snapshot:" + value.ComputedBasisID }},
		{name: "extra artifact ref", mutate: func(value *vs08EvidenceRecord) { value.ObjectRefs = append(value.ObjectRefs, "extra:unexpected") }},
		{name: "missing artifact ref", mutate: func(value *vs08EvidenceRecord) { value.ObjectRefs = value.ObjectRefs[:4] }},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			mutated := base
			mutated.ObjectRefs = cloneVS08Strings(base.ObjectRefs)
			mutation.mutate(&mutated)
			if err := validateVS08RegistryRecords([]vs08EvidenceRecord{mutated}, []string{"VS08-A1"}); err == nil {
				t.Fatalf("%s fabricated observed/pass record was accepted", mutation.name)
			}
		})
	}
}

func expectedVS08ImplementationID(criterion string) string {
	value, _ := strconv.Atoi(strings.TrimPrefix(criterion, "VS08-A"))
	return fmt.Sprintf("codeflow/internal/rflscvs08runner.TestRFLSCR2VS08_A%02d", value)
}

func mustJSONVS08(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprintf("<json error: %v>", err)
	}
	return string(data)
}

func TestVS08EvidenceRegistryRejectsMissingDuplicateNotRunAndFabricated(t *testing.T) {
	valid := func(criterion string, observed bool) vs08EvidenceRecord {
		return vs08EvidenceRecord{Criterion: criterion, ImplementationTestID: expectedVS08ImplementationID(criterion), ImplementationPackage: vs08ImplementationPackage, ExecutionPackage: vs08ExecutionPackage, ExecutionBinary: "contractharness.test", ExecutionID: "run-" + criterion, Result: "pass", ExecutionCompleted: true, runnerObserved: observed, SnapshotID: "snapshot-vs08", ComputedBasisID: "basis-vs08", GenerationID: "generation-vs08", PackDigest: "pack-vs08", ModelHostAuditApplicability: rflscvs08runner.ModelHostAuditNotApplicable, ModelHostOutcome: "not_applicable", ObjectRefs: []string{"snapshot:snapshot-vs08", "basis:basis-vs08", "generation:generation-vs08", "pack:pack-vs08", "evidence:e-vs08"}}
	}
	if err := validateVS08RegistryRecords([]vs08EvidenceRecord{valid("VS08-A1", true)}, []string{"VS08-A1", "VS08-A2"}); err == nil {
		t.Fatal("missing criterion was accepted")
	}
	if err := validateVS08RegistryRecords([]vs08EvidenceRecord{valid("VS08-A1", true), valid("VS08-A1", true)}, []string{"VS08-A1", "VS08-A2"}); err == nil {
		t.Fatal("duplicate criterion was accepted")
	}
	if err := validateVS08RegistryRecords([]vs08EvidenceRecord{valid("VS08-A1", false), valid("VS08-A2", true)}, []string{"VS08-A1", "VS08-A2"}); err == nil {
		t.Fatal("not-run criterion was accepted")
	}
	if err := validateVS08RegistryRecords(nil, []string{"VS08-A1"}); err == nil {
		t.Fatal("zero-match registry was accepted")
	}
	if err := validateVS08RegistryRecords([]vs08EvidenceRecord{valid("VS08-A1", false)}, []string{"VS08-A1"}); err == nil {
		t.Fatal("fabricated passing evidence was accepted")
	}
}

// TestRFLSCR2VS08ModelHostHelper is the supervised fake executable used by
// the shared VS08 runner when the registry invokes it from this test binary.
// Keeping the helper in the binary under test makes the registry's process
// boundary observable without mounting the repository or using a production
// model executable.
func TestRFLSCR2VS08ModelHostHelper(t *testing.T) {
	if os.Getenv("CODEFLOW_VS08_MODEL_HOST_HELPER") != "1" {
		return
	}
	reader := bufio.NewReader(os.Stdin)
	for {
		body, err := readContractVS08Frame(reader)
		if err != nil {
			return
		}
		var request struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      string          `json:"id"`
			Method  string          `json:"method"`
			Params  json.RawMessage `json:"params"`
		}
		if json.Unmarshal(body, &request) != nil {
			return
		}
		if request.Method == "initialize" {
			response := protocol.ModelHostResponse{
				SchemaID: protocol.ModelHostResponseSchemaID, SchemaVersion: protocol.ModelHostProtocolVersion,
				RequestID: request.ID, Status: "ok",
				Capability: func() protocol.ModelHostCapability {
					probe := contractVS08ModelHostFilesystemProbe()
					return protocol.ModelHostCapability{Status: "measured", ModelID: "registry-fake", Revision: "test", License: "MIT", Checksum: "sha256:registry-fake", Runtime: "go-test", DataBoundary: "bounded-pack", Measured: true, SchemaConstrained: true, Cancellation: true, MaxRequestBytes: protocol.DefaultMaxMessageSizeBytes, MaxResponseBytes: protocol.DefaultMaxMessageSizeBytes, IsolationBackend: "sandbox-exec", IsolationEnforced: true, IsolationProbe: &probe, NetworkPolicy: "deny_all"}
				}(),
			}
			if !writeContractVS08Result(request.ID, response) {
				return
			}
			continue
		}
		if request.Method != protocol.ModelHostEnrichMethod {
			return
		}
		var params protocol.ModelHostRequest
		if json.Unmarshal(request.Params, &params) != nil || len(params.EvidencePack) == 0 {
			return
		}
		if !writeContractVS08Receipt(params.RequestID, params.PackDigest) {
			return
		}
		if recordPath := os.Getenv("CODEFLOW_VS08_MODEL_HOST_RECORD"); recordPath != "" {
			file, err := os.OpenFile(recordPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
			if err != nil {
				return
			}
			_, _ = fmt.Fprintf(file, "%s|%s\n", params.PackDigest, params.EvidencePack)
			_ = file.Close()
		}
		switch mode := os.Getenv("CODEFLOW_VS08_MODEL_HOST_MODE"); mode {
		case "crash":
			os.Exit(17)
		case "failure":
			if !writeContractVS08Error(request.ID, -32001, "registry fake failure") {
				return
			}
			continue
		case "timeout", "cancel":
			time.Sleep(2 * time.Second)
			continue
		}
		if delay := os.Getenv("CODEFLOW_VS08_MODEL_HOST_DELAY"); delay != "" {
			if duration, parseErr := time.ParseDuration(delay); parseErr == nil {
				time.Sleep(duration)
			}
		}
		response := protocol.ModelHostResponse{
			SchemaID: protocol.ModelHostResponseSchemaID, SchemaVersion: protocol.ModelHostProtocolVersion,
			RequestID: request.ID, Status: "accepted",
			Proposal: json.RawMessage(`{"schemaId":"https://codeflow.local/schemas/rflsc.semantic-proposal.v2.schema.json","schemaVersion":2}`),
		}
		if !writeContractVS08Result(request.ID, response) {
			return
		}
	}
}

const contractVS08ModelHostProbeSentinelContents = "codeflow-model-host-sentinel-v1\n"

func contractVS08ModelHostProbeSentinelPath() string {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		return ""
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(source), "../.."))
	return filepath.Join(repoRoot, "internal", "protocol", "testdata", "model-host-sentinel.txt")
}

func contractVS08ModelHostFilesystemProbe() protocol.ModelHostIsolationProbe {
	path := contractVS08ModelHostProbeSentinelPath()
	expected := []byte(contractVS08ModelHostProbeSentinelContents)
	beforeBytes := expected
	readAttempt := "blocked"
	if data, err := os.ReadFile(path); err == nil {
		beforeBytes = data
		readAttempt = "allowed"
	}
	beforeSum := sha256.Sum256(beforeBytes)
	probe := protocol.ModelHostIsolationProbe{RepositoryReadAttempt: readAttempt, SentinelBeforeDigest: "sha256:" + hex.EncodeToString(beforeSum[:])}
	writeAttempt := "blocked"
	writePath := filepath.Join("/tmp", "codeflow-model-host-probe-write-"+strconv.Itoa(os.Getpid()))
	if file, err := os.OpenFile(writePath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600); err == nil {
		writeAttempt = "allowed"
		_ = file.Close()
		_ = os.Remove(writePath)
	}
	probe.RepositoryWriteAttempt = writeAttempt
	probe.NetworkAttempt = "blocked"
	listener, listenErr := net.Listen("tcp4", "127.0.0.1:0")
	if listenErr == nil {
		defer listener.Close()
		connection, dialErr := net.DialTimeout("tcp4", listener.Addr().String(), 50*time.Millisecond)
		if dialErr == nil {
			probe.NetworkAttempt = "allowed"
			_ = connection.Close()
		}
	}
	afterBytes := beforeBytes
	if data, err := os.ReadFile(path); err == nil {
		afterBytes = data
	}
	afterSum := sha256.Sum256(afterBytes)
	probe.SentinelAfterDigest = "sha256:" + hex.EncodeToString(afterSum[:])
	probe.SentinelUnchanged = probe.SentinelBeforeDigest == probe.SentinelAfterDigest
	return probe
}

func readContractVS08Frame(reader *bufio.Reader) ([]byte, error) {
	contentLength := -1
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		key, value, ok := strings.Cut(line, ":")
		if ok && strings.EqualFold(strings.TrimSpace(key), "Content-Length") {
			contentLength, err = strconv.Atoi(strings.TrimSpace(value))
			if err != nil {
				return nil, err
			}
		}
	}
	if contentLength < 0 {
		return nil, fmt.Errorf("missing content length")
	}
	body := make([]byte, contentLength)
	_, err := io.ReadFull(reader, body)
	return body, err
}

func writeContractVS08Result(id string, result protocol.ModelHostResponse) bool {
	body, err := json.Marshal(struct {
		JSONRPC string                     `json:"jsonrpc"`
		ID      string                     `json:"id"`
		Result  protocol.ModelHostResponse `json:"result"`
	}{protocol.JSONRPCVersion, id, result})
	if err != nil {
		return false
	}
	if _, err := fmt.Fprintf(os.Stdout, "Content-Length: %d\r\n\r\n", len(body)); err != nil {
		return false
	}
	_, err = os.Stdout.Write(body)
	return err == nil
}

func writeContractVS08Receipt(requestID, packDigest string) bool {
	body, err := json.Marshal(struct {
		JSONRPC string `json:"jsonrpc"`
		Method  string `json:"method"`
		Params  struct {
			RequestID  string `json:"requestId"`
			PackDigest string `json:"packDigest"`
		} `json:"params"`
	}{JSONRPC: protocol.JSONRPCVersion, Method: "request_received", Params: struct {
		RequestID  string `json:"requestId"`
		PackDigest string `json:"packDigest"`
	}{RequestID: requestID, PackDigest: packDigest}})
	if err != nil {
		return false
	}
	if _, err := fmt.Fprintf(os.Stdout, "Content-Length: %d\r\n\r\n", len(body)); err != nil {
		return false
	}
	_, err = os.Stdout.Write(body)
	return err == nil
}

func writeContractVS08Error(id string, code int, message string) bool {
	body, err := json.Marshal(struct {
		JSONRPC string `json:"jsonrpc"`
		ID      string `json:"id"`
		Error   any    `json:"error"`
	}{protocol.JSONRPCVersion, id, map[string]any{"code": code, "message": message}})
	if err != nil {
		return false
	}
	if _, err := fmt.Fprintf(os.Stdout, "Content-Length: %d\r\n\r\n", len(body)); err != nil {
		return false
	}
	_, err = os.Stdout.Write(body)
	return err == nil
}
