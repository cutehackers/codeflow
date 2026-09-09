package contractharness_test

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	harness "codeflow/internal/contractharness"
	"codeflow/internal/rflscvs03"
	"codeflow/schemas"
)

const (
	vs03ImplementationPackage = "codeflow/internal/rflscvs03"
	vs03ExecutionPackage      = "codeflow/internal/contractharness"
)

type vs03Evidence struct {
	Criterion             string                        `json:"criterion"`
	ImplementationTestID  string                        `json:"implementationTestId"`
	ImplementationPackage string                        `json:"implementationPackage"`
	ExecutionPackage      string                        `json:"executionPackage"`
	ExecutionBinary       string                        `json:"executionBinary"`
	ExecutionID           string                        `json:"executionId"`
	Result                string                        `json:"result"`
	ExecutionCompleted    bool                          `json:"executionCompleted"`
	SnapshotTreeDigest    string                        `json:"snapshotTreeDigest"`
	ObjectRefs            []string                      `json:"objectRefs"`
	Trace                 *rflscvs03.TraceEvidence      `json:"trace,omitempty"`
	ViewCorpus            *rflscvs03.ViewCorpusEvidence `json:"viewCorpus,omitempty"`
}

func TestRFLSCR2VS03_EvidenceRegistry(t *testing.T) {
	if harness.VS03EvidenceRegistryID != "rflsc-r2-vs-03" {
		t.Fatalf("unexpected VS-03 evidence registry ID: %q", harness.VS03EvidenceRegistryID)
	}
	if err := harness.EnsureAllCompiled(); err != nil {
		t.Fatalf("compile contract registry: %v", err)
	}

	expectedContracts := []string{
		"rflsc.activity-state.v2", "rflsc.publication-candidate.v2", "rflsc.generation-proof-manifest.v2", "rflsc.active-pointer.v2",
		"rflsc.verified-gap.v2", "rflsc.settlement.v2", "rflsc.event-envelope.v2", "rflsc.flowview-view-state.v2",
	}
	if len(harness.VS03ContractRegistry) != len(expectedContracts) {
		t.Fatalf("VS-03 contract registry count=%d want=%d", len(harness.VS03ContractRegistry), len(expectedContracts))
	}
	seenContracts := map[string]bool{}
	for i, entry := range harness.VS03ContractRegistry {
		if entry.ID != expectedContracts[i] {
			t.Fatalf("unexpected VS-03 registry order or ID at %d: %+v", i, entry)
		}
		if seenContracts[entry.ID] {
			t.Fatalf("duplicate VS-03 contract registry entry: %s", entry.ID)
		}
		seenContracts[entry.ID] = true
		valid, err := readVS03Fixture(entry.ValidFixture)
		if err != nil {
			t.Fatalf("read valid fixture %s: %v", entry.ID, err)
		}
		invalidIdentity, err := readVS03Fixture(entry.InvalidIdentityFixture)
		if err != nil {
			t.Fatalf("read cross-identity fixture %s: %v", entry.ID, err)
		}
		invalidState, err := readVS03Fixture(entry.InvalidStateFixture)
		if err != nil {
			t.Fatalf("read invalid-state fixture %s: %v", entry.ID, err)
		}
		if err := harness.ValidateVS03RegistryEntry(entry, valid, invalidIdentity, invalidState); err != nil {
			t.Fatalf("VS-03 contract fixture validation failed for %s: %v", entry.ID, err)
		}
		for _, fixturePath := range entry.AdditionalInvalidFixtures {
			fixture, err := readVS03Fixture(fixturePath)
			if err != nil {
				t.Fatalf("read additional invalid fixture %s for %s: %v", fixturePath, entry.ID, err)
			}
			validator := harness.ValidatorForVS03Contract(entry.ID)
			if err := validator(fixture); err == nil {
				t.Fatalf("additional invalid fixture unexpectedly passed for %s: %s", entry.ID, fixturePath)
			}
		}
	}
	// A step's renderer-local ID may change after a compatible update. The
	// structural identity and logical anchor are the stable binding, so this
	// state must remain valid even though selectedStepId and anchor.stepId are
	// replaced together.
	viewFixture, err := readVS03Fixture("rflsc.flowview-view-state.v2/valid/state.json")
	if err != nil {
		t.Fatalf("read view-state compatibility fixture: %v", err)
	}
	var compatible map[string]any
	if err := json.Unmarshal(viewFixture, &compatible); err != nil {
		t.Fatalf("decode view-state compatibility fixture: %v", err)
	}
	compatible["selectedStepId"] = "step-result-after-update"
	compatible["visibleStepRefs"] = []any{"step-entry", "step-result-after-update"}
	compatible["preservedStepRefs"] = []any{"step-result-after-update"}
	compatible["logicalScrollAnchor"] = map[string]any{"stepId": "step-result-after-update", "structuralIdentity": "service.go#Submit:result", "offsetPx": 12.5}
	compatibleBytes, err := json.Marshal(compatible)
	if err != nil {
		t.Fatalf("encode view-state compatibility fixture: %v", err)
	}
	if err := harness.ValidateFlowViewStateV2(compatibleBytes); err != nil {
		t.Fatalf("compatible step-id update lost structural identity binding: %v", err)
	}

	expectedCriteria := []string{
		"VS03-A1", "VS03-A2", "VS03-A3", "VS03-A4", "VS03-A5", "VS03-A6", "VS03-A7", "VS03-A8",
		"VS03-A9", "VS03-A10", "VS03-A11", "VS03-A12", "VS03-A13", "VS03-A14", "VS03-A15", "VS03-A16",
		"VS03-A17", "VS03-A18", "VS03-A19", "VS03-A20",
	}
	runners := map[string]func(*testing.T) rflscvs03.Evidence{
		"VS03-A1": rflscvs03.RunA01, "VS03-A2": rflscvs03.RunA02, "VS03-A3": rflscvs03.RunA03,
		"VS03-A4": rflscvs03.RunA04, "VS03-A5": rflscvs03.RunA05, "VS03-A6": rflscvs03.RunA06,
		"VS03-A7": rflscvs03.RunA07, "VS03-A8": rflscvs03.RunA08, "VS03-A9": rflscvs03.RunA09,
		"VS03-A10": rflscvs03.RunA10, "VS03-A11": rflscvs03.RunA11, "VS03-A12": rflscvs03.RunA12,
		"VS03-A13": rflscvs03.RunA13, "VS03-A14": rflscvs03.RunA14, "VS03-A15": rflscvs03.RunA15,
		"VS03-A16": rflscvs03.RunA16, "VS03-A17": rflscvs03.RunA17, "VS03-A18": rflscvs03.RunA18,
		"VS03-A19": rflscvs03.RunA19, "VS03-A20": rflscvs03.RunA20,
	}
	if len(runners) != len(expectedCriteria) {
		t.Fatalf("VS-03 evidence runner count=%d want=%d", len(runners), len(expectedCriteria))
	}
	expectedImplementation := expectedVS03ImplementationIDs()
	records := make([]vs03Evidence, 0, len(expectedCriteria))
	for _, criterion := range expectedCriteria {
		runner, ok := runners[criterion]
		if !ok || runner == nil {
			t.Fatalf("missing VS-03 evidence runner for %s", criterion)
		}
		var record vs03Evidence
		passed := t.Run(criterion, func(st *testing.T) {
			shared := runner(st)
			record = vs03ExecutionRecord(st, shared)
			if err := validateVS03EvidenceRecord(criterion, st.Name(), record, expectedImplementation); err != nil {
				st.Fatal(err)
			}
			// The registry records pass only after the production seam returned and
			// all immutable identity checks completed.
			record.ExecutionCompleted = true
			record.Result = "pass"
			if err := validateVS03EvidenceRecord(criterion, st.Name(), record, expectedImplementation); err != nil {
				st.Fatal(err)
			}
		})
		if !passed {
			t.Fatalf("criterion %s was not executed successfully", criterion)
		}
		if record.Result != "pass" || !record.ExecutionCompleted {
			t.Fatalf("criterion %s has no completed passing execution: %+v", criterion, record)
		}
		t.Logf("evidence=%s", mustJSONVS03(record))
		records = append(records, record)
	}

	counts := map[string]int{}
	for _, record := range records {
		counts[record.Criterion]++
	}
	if len(counts) != len(expectedCriteria) {
		t.Fatalf("VS-03 evidence registry has missing, zero-match, or unknown criteria: %+v", counts)
	}
	for _, criterion := range expectedCriteria {
		if counts[criterion] != 1 {
			t.Fatalf("VS-03 evidence registry requires exactly one executed record for %s, got %d", criterion, counts[criterion])
		}
	}
}

func readVS03Fixture(name string) ([]byte, error) {
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("fixture path is empty")
	}
	return fs.ReadFile(schemas.FixturesFS, filepath.ToSlash(filepath.Join("fixtures", name)))
}

func expectedVS03ImplementationIDs() map[string]string {
	return map[string]string{
		"VS03-A1": "codeflow/internal/rflscvs03.TestRFLSCR2VS03_A01", "VS03-A2": "codeflow/internal/rflscvs03.TestRFLSCR2VS03_A02",
		"VS03-A3": "codeflow/internal/rflscvs03.TestRFLSCR2VS03_A03", "VS03-A4": "codeflow/internal/rflscvs03.TestRFLSCR2VS03_A04",
		"VS03-A5": "codeflow/internal/rflscvs03.TestRFLSCR2VS03_A05", "VS03-A6": "codeflow/internal/rflscvs03.TestRFLSCR2VS03_A06",
		"VS03-A7": "codeflow/internal/rflscvs03.TestRFLSCR2VS03_A07", "VS03-A8": "codeflow/internal/rflscvs03.TestRFLSCR2VS03_A08",
		"VS03-A9": "codeflow/internal/rflscvs03.TestRFLSCR2VS03_A09", "VS03-A10": "codeflow/internal/rflscvs03.TestRFLSCR2VS03_A10",
		"VS03-A11": "codeflow/internal/rflscvs03.TestRFLSCR2VS03_A11", "VS03-A12": "codeflow/internal/rflscvs03.TestRFLSCR2VS03_A12",
		"VS03-A13": "codeflow/internal/rflscvs03.TestRFLSCR2VS03_A13", "VS03-A14": "codeflow/internal/rflscvs03.TestRFLSCR2VS03_A14",
		"VS03-A15": "codeflow/internal/rflscvs03.TestRFLSCR2VS03_A15",
		"VS03-A16": "codeflow/internal/rflscvs03.TestRFLSCR2VS03_A16", "VS03-A17": "codeflow/internal/rflscvs03.TestRFLSCR2VS03_A17",
		"VS03-A18": "codeflow/internal/rflscvs03.TestRFLSCR2VS03_A18", "VS03-A19": "codeflow/internal/rflscvs03.TestRFLSCR2VS03_A19",
		"VS03-A20": "codeflow/internal/rflscvs03.TestRFLSCR2VS03_A20",
	}
}

func vs03ExecutionRecord(t *testing.T, shared rflscvs03.Evidence) vs03Evidence {
	t.Helper()
	return vs03Evidence{
		Criterion: shared.Criterion, ImplementationTestID: shared.ImplementationTestID, ImplementationPackage: shared.ImplementationPackage,
		ExecutionPackage: vs03ExecutionPackage, ExecutionBinary: filepath.Base(os.Args[0]), ExecutionID: t.Name(),
		SnapshotTreeDigest: shared.SnapshotTreeDigest, ObjectRefs: append([]string(nil), shared.ObjectRefs...), Trace: shared.Trace, ViewCorpus: shared.ViewCorpus,
	}
}

func validateVS03EvidenceRecord(criterion, executionID string, record vs03Evidence, expected map[string]string) error {
	if record.Criterion != criterion || record.ImplementationTestID != expected[criterion] || record.ImplementationPackage != vs03ImplementationPackage {
		return fmt.Errorf("implementation identity mismatch: %+v", record)
	}
	if record.ExecutionPackage != vs03ExecutionPackage || record.ExecutionBinary == "" || record.ExecutionID != executionID {
		return fmt.Errorf("execution identity mismatch: %+v", record)
	}
	if record.Result != "" && record.Result != "pass" {
		return fmt.Errorf("unknown execution result: %+v", record)
	}
	if record.Result == "pass" && !record.ExecutionCompleted {
		return fmt.Errorf("pass was recorded before execution completed: %+v", record)
	}
	if record.ExecutionCompleted && record.Result != "pass" {
		return fmt.Errorf("execution completion has no passing result: %+v", record)
	}
	if record.SnapshotTreeDigest == "" || len(record.ObjectRefs) < 3 {
		return fmt.Errorf("immutable artifact evidence is incomplete: %+v", record)
	}
	seen := map[string]bool{}
	hasSnapshot, hasTree, hasBasis := false, false, false
	for _, ref := range record.ObjectRefs {
		parts := strings.SplitN(ref, ":", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" || seen[ref] {
			return fmt.Errorf("invalid or duplicate immutable artifact ref %q", ref)
		}
		seen[ref] = true
		switch parts[0] {
		case "snapshot":
			hasSnapshot = true
		case "tree":
			if parts[1] != record.SnapshotTreeDigest {
				return fmt.Errorf("tree ref %q does not match digest %q", ref, record.SnapshotTreeDigest)
			}
			hasTree = true
		case "basis":
			hasBasis = true
		}
	}
	if !hasSnapshot || !hasTree || !hasBasis {
		return fmt.Errorf("snapshot, tree and basis refs are required: %+v", record.ObjectRefs)
	}
	if criterion == "VS03-A13" {
		if err := validateVS03Trace(record.Trace); err != nil {
			return err
		}
	}
	if criterion == "VS03-A14" {
		if err := validateVS03Corpus(record.ViewCorpus); err != nil {
			return err
		}
	}
	return nil
}

func validateVS03Trace(trace *rflscvs03.TraceEvidence) error {
	if trace == nil || trace.Profile == "" || len(trace.TraceIDs) < 20 || len(trace.TraceIDs) != len(trace.ActivitySamplesMs) || len(trace.TraceIDs) != len(trace.CurrentOrGapSamplesMs) {
		return fmt.Errorf("A13 requires at least 20 same-trace paired observations")
	}
	seen := map[string]bool{}
	for _, id := range trace.TraceIDs {
		if id == "" || seen[id] {
			return fmt.Errorf("A13 trace IDs must be unique and non-empty")
		}
		seen[id] = true
	}
	activityP95 := p95VS03(trace.ActivitySamplesMs)
	gapP95 := p95VS03(trace.CurrentOrGapSamplesMs)
	if trace.ActivityP95Ms != activityP95 || trace.CurrentOrGapP95Ms != gapP95 || trace.ActivityP95Ms > 300 || trace.CurrentOrGapP95Ms > 3000 {
		return fmt.Errorf("A13 separate P95 profile failed: activity=%v/%v currentOrGap=%v/%v", trace.ActivityP95Ms, activityP95, trace.CurrentOrGapP95Ms, gapP95)
	}
	return nil
}

func p95VS03(values []float64) float64 {
	ordered := append([]float64(nil), values...)
	sort.Float64s(ordered)
	if len(ordered) == 0 {
		return 0
	}
	return ordered[int(float64(len(ordered)-1)*0.95)]
}

func validateVS03Corpus(corpus *rflscvs03.ViewCorpusEvidence) error {
	if corpus == nil || corpus.CorpusVersion == "" || corpus.EligibleUpdates <= 0 || corpus.Denominator != corpus.EligibleUpdates || corpus.Numerator != corpus.PreservedUpdates || corpus.PreservedUpdates < 0 || corpus.PreservedUpdates > corpus.Denominator || corpus.ExcludedIdentityLoss <= 0 {
		return fmt.Errorf("A14 corpus numerator/denominator/excluded identity-loss evidence is incomplete")
	}
	actual := float64(corpus.Numerator) * 100 / float64(corpus.Denominator)
	if corpus.PreservationPercent != actual || actual < 99 {
		return fmt.Errorf("A14 preservation threshold failed: %+v", *corpus)
	}
	return nil
}

func mustJSONVS03(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprintf("<json error: %v>", err)
	}
	return string(data)
}

func TestVS03EvidenceRegistryRejectsFabricatedPass(t *testing.T) {
	record := vs03Evidence{
		Criterion: "VS03-A1", ImplementationTestID: "codeflow/internal/rflscvs03.TestRFLSCR2VS03_A01", ImplementationPackage: vs03ImplementationPackage,
		ExecutionPackage: vs03ExecutionPackage, ExecutionBinary: "contractharness.test", ExecutionID: "fake-execution", Result: "pass",
		SnapshotTreeDigest: "tree-vs03", ObjectRefs: []string{"snapshot:snapshot-vs03", "tree:tree-vs03", "basis:basis-vs03"},
	}
	if err := validateVS03EvidenceRecord(record.Criterion, record.ExecutionID, record, expectedVS03ImplementationIDs()); err == nil {
		t.Fatal("fabricated pass without completed production execution was accepted")
	}
}
