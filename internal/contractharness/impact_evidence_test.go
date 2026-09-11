package contractharness_test

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	harness "codeflow/internal/contractharness"
	"codeflow/internal/verification/impact"
	"codeflow/schemas"
)

const (
	impactImplementationPackage = "codeflow/internal/verification/impact"
	impactExecutionPackage      = "codeflow/internal/contractharness"
)

type impactEvidence struct {
	Criterion             string   `json:"criterion"`
	ImplementationTestID  string   `json:"implementationTestId"`
	ImplementationPackage string   `json:"implementationPackage"`
	ExecutionPackage      string   `json:"executionPackage"`
	ExecutionBinary       string   `json:"executionBinary"`
	ExecutionID           string   `json:"executionId"`
	Result                string   `json:"result"`
	ExecutionCompleted    bool     `json:"executionCompleted"`
	SnapshotTreeDigest    string   `json:"snapshotTreeDigest"`
	ObjectRefs            []string `json:"objectRefs"`
	runnerObserved        bool
}

func TestRFLSCR2VS05_EvidenceRegistry(t *testing.T) {
	if harness.VS05EvidenceRegistryID != "rflsc-r2-vs-05" {
		t.Fatalf("unexpected VS-05 evidence registry ID: %q", harness.VS05EvidenceRegistryID)
	}
	if err := harness.EnsureAllCompiled(); err != nil {
		t.Fatalf("compile contract registry: %v", err)
	}
	expectedContracts := []string{
		"rflsc.impact-query.v2",
		"rflsc.change-impact-graph.v2",
		"rflsc.impact-frontier.v1",
	}
	if len(harness.VS05ContractRegistry) != len(expectedContracts) {
		t.Fatalf("VS-05 contract registry count=%d want=%d", len(harness.VS05ContractRegistry), len(expectedContracts))
	}
	seenContracts := map[string]bool{}
	for index, entry := range harness.VS05ContractRegistry {
		if entry.ID != expectedContracts[index] {
			t.Fatalf("unexpected VS-05 registry order or ID at %d: %+v", index, entry)
		}
		if seenContracts[entry.ID] {
			t.Fatalf("duplicate VS-05 contract registry entry: %s", entry.ID)
		}
		seenContracts[entry.ID] = true
		valid, err := readVS05Fixture(entry.ValidFixture)
		if err != nil {
			t.Fatalf("read valid fixture %s: %v", entry.ID, err)
		}
		invalidDirection, err := readVS05Fixture(entry.InvalidDirectionFixture)
		if err != nil {
			t.Fatalf("read invalid direction fixture %s: %v", entry.ID, err)
		}
		invalidIdentity, err := readVS05Fixture(entry.InvalidIdentityFixture)
		if err != nil {
			t.Fatalf("read invalid identity fixture %s: %v", entry.ID, err)
		}
		invalidEvidence, err := readVS05Fixture(entry.InvalidEvidenceFixture)
		if err != nil {
			t.Fatalf("read invalid Evidence fixture %s: %v", entry.ID, err)
		}
		if err := harness.ValidateVS05RegistryEntry(entry, valid, invalidDirection, invalidIdentity, invalidEvidence); err != nil {
			t.Fatalf("VS-05 contract fixture validation failed for %s: %v", entry.ID, err)
		}
	}

	expectedCriteria := []string{"VS05-A1", "VS05-A2", "VS05-A3", "VS05-A4", "VS05-A5", "VS05-A6", "VS05-A7"}
	runners := map[string]func(*testing.T) impact.Evidence{
		"VS05-A1": impact.RunA01,
		"VS05-A2": impact.RunA02,
		"VS05-A3": impact.RunA03,
		"VS05-A4": impact.RunA04,
		"VS05-A5": impact.RunA05,
		"VS05-A6": impact.RunA06,
		"VS05-A7": impact.RunA07,
	}
	expectedImplementation := expectedVS05ImplementationIDs()
	records := make([]impactEvidence, 0, len(expectedCriteria))
	for _, criterion := range expectedCriteria {
		runner := runners[criterion]
		if runner == nil {
			t.Fatalf("missing VS-05 evidence runner for %s", criterion)
		}
		var record impactEvidence
		passed := t.Run(criterion, func(st *testing.T) {
			shared := runner(st)
			record = vs05ExecutionRecord(st, shared)
			if err := validateVS05EvidenceRecord(criterion, st.Name(), record, expectedImplementation); err != nil {
				st.Fatal(err)
			}
			record.runnerObserved = true
			record.ExecutionCompleted = true
			record.Result = "pass"
			if err := validateVS05EvidenceRecord(criterion, st.Name(), record, expectedImplementation); err != nil {
				st.Fatal(err)
			}
		})
		if !passed {
			t.Fatalf("criterion %s was not executed successfully", criterion)
		}
		if record.Result != "pass" || !record.ExecutionCompleted {
			t.Fatalf("criterion %s has no completed passing execution: %+v", criterion, record)
		}
		records = append(records, record)
	}
	if err := validateCompletedVS05Records(records, expectedCriteria, expectedImplementation); err != nil {
		t.Fatal(err)
	}
}

func TestVS05EvidenceRegistryRejectsMissingDuplicateNotRunAndFabricatedPass(t *testing.T) {
	expectedCriteria := []string{"VS05-A1", "VS05-A2"}
	expectedImplementation := expectedVS05ImplementationIDs()
	valid := func(criterion, executionID string) impactEvidence {
		return impactEvidence{
			Criterion: criterion, ImplementationTestID: expectedImplementation[criterion],
			ImplementationPackage: impactImplementationPackage, ExecutionPackage: impactExecutionPackage,
			ExecutionBinary: "contractharness.test", ExecutionID: executionID, Result: "pass",
			ExecutionCompleted: true, SnapshotTreeDigest: "tree-impact",
			ObjectRefs: []string{"snapshot:snapshot-impact", "tree:tree-impact", "basis:basis-impact", "impactGraph:impact-test"},
		}
	}
	cases := []struct {
		name    string
		records []impactEvidence
	}{
		{name: "missing", records: []impactEvidence{valid("VS05-A1", "run-a1")}},
		{name: "duplicate", records: []impactEvidence{valid("VS05-A1", "run-a1"), valid("VS05-A1", "run-a1b")}},
		{name: "not-run", records: []impactEvidence{{Criterion: "VS05-A1"}}},
		{name: "zero-match", records: nil},
		{name: "fabricated-pass", records: []impactEvidence{valid("VS05-A1", "fake-execution")}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if err := validateCompletedVS05Records(testCase.records, expectedCriteria, expectedImplementation); err == nil {
				t.Fatalf("invalid registry state %s was accepted", testCase.name)
			}
		})
	}
}

func TestValidateChangeImpactGraphV2EnforcesPublicClaimPaths(t *testing.T) {
	raw, err := readVS05Fixture("rflsc.change-impact-graph.v2/valid/graph.json")
	if err != nil {
		t.Fatal(err)
	}
	var graph map[string]any
	if err := json.Unmarshal(raw, &graph); err != nil {
		t.Fatal(err)
	}
	directImpact, ok := graph["directImpact"].(map[string]any)
	if !ok {
		t.Fatal("valid fixture has no directImpact object")
	}
	callers, ok := directImpact["callers"].([]any)
	if !ok || len(callers) == 0 {
		t.Fatal("valid fixture has no direct caller claim")
	}
	caller, ok := callers[0].(map[string]any)
	if !ok {
		t.Fatal("valid fixture caller is not an object")
	}

	cases := []struct {
		name   string
		mutate func(map[string]any)
		want   bool
	}{
		{
			name: "caller path terminus must match symbolPath",
			mutate: func(doc map[string]any) {
				path := append([]any(nil), caller["path"].([]any)...)
				path[len(path)-1] = "Wrong.caller"
				callerCopy := cloneVS05Map(caller)
				callerCopy["path"] = path
				directCopy := cloneVS05Map(directImpact)
				directCopy["callers"] = []any{callerCopy}
				doc["directImpact"] = directCopy
			},
			want: false,
		},
		{
			name: "symbol target must start every claim path",
			mutate: func(doc map[string]any) {
				path := append([]any(nil), caller["path"].([]any)...)
				path[0] = "Wrong.target"
				callerCopy := cloneVS05Map(caller)
				callerCopy["path"] = path
				directCopy := cloneVS05Map(directImpact)
				directCopy["callers"] = []any{callerCopy}
				doc["directImpact"] = directCopy
			},
			want: false,
		},
		{
			name: "depth must equal path edge count",
			mutate: func(doc map[string]any) {
				path := append([]any(nil), caller["path"].([]any)...)
				path = append(path, "Extra.step")
				callerCopy := cloneVS05Map(caller)
				callerCopy["path"] = path
				directCopy := cloneVS05Map(directImpact)
				directCopy["callers"] = []any{callerCopy}
				doc["directImpact"] = directCopy
			},
			want: false,
		},
		{
			name: "caller cannot be the changed callee",
			mutate: func(doc map[string]any) {
				callerCopy := cloneVS05Map(caller)
				callerCopy["symbolPath"] = "PaymentService.process"
				callerCopy["path"] = []any{"PaymentService.process", "PaymentService.process"}
				directCopy := cloneVS05Map(directImpact)
				directCopy["callers"] = []any{callerCopy}
				doc["directImpact"] = directCopy
			},
			want: false,
		},
		{
			name: "batch target does not require batch id in path",
			mutate: func(doc map[string]any) {
				doc["target"] = map[string]any{"changeBatchId": "batch-impact"}
			},
			want: true,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			doc := cloneVS05Map(graph)
			testCase.mutate(doc)
			data, err := json.Marshal(doc)
			if err != nil {
				t.Fatal(err)
			}
			err = harness.ValidateChangeImpactGraphV2(data)
			if (err == nil) != testCase.want {
				t.Fatalf("validator result=%v, want valid=%t", err, testCase.want)
			}
		})
	}
}

func TestValidateChangeImpactGraphV2BindsTerminalClaimsToEvidenceAnchors(t *testing.T) {
	raw, err := readVS05Fixture("rflsc.change-impact-graph.v2/valid/graph.json")
	if err != nil {
		t.Fatal(err)
	}
	var graph map[string]any
	if err := json.Unmarshal(raw, &graph); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{
			name: "terminal claim must end at its terminal symbol",
			mutate: func(doc map[string]any) {
				indirect := cloneVS05Map(doc["indirectImpact"].(map[string]any))
				effects := append([]any(nil), indirect["externalEffects"].([]any)...)
				effect := cloneVS05Map(effects[0].(map[string]any))
				effect["terminalSymbolPath"] = "Wrong.effect"
				effects[0] = effect
				indirect["externalEffects"] = effects
				doc["indirectImpact"] = indirect
			},
		},
		{
			name: "claim evidence must anchor the terminal symbol",
			mutate: func(doc map[string]any) {
				evidence := append([]any(nil), doc["evidence"].([]any)...)
				for index, rawEvidence := range evidence {
					item := cloneVS05Map(rawEvidence.(map[string]any))
					if item["evidenceId"] == "ev-effect" {
						anchor := cloneVS05Map(item["anchor"].(map[string]any))
						anchor["enclosingSymbolPath"] = "PaymentState.markPaid"
						item["anchor"] = anchor
						evidence[index] = item
					}
				}
				doc["evidence"] = evidence
			},
		},
		{
			name: "caller evidence must anchor its declared file",
			mutate: func(doc map[string]any) {
				evidence := append([]any(nil), doc["evidence"].([]any)...)
				for index, rawEvidence := range evidence {
					item := cloneVS05Map(rawEvidence.(map[string]any))
					if item["evidenceId"] == "ev-caller" {
						anchor := cloneVS05Map(item["anchor"].(map[string]any))
						anchor["repoRelativePath"] = "src/other.go"
						item["anchor"] = anchor
						evidence[index] = item
					}
				}
				doc["evidence"] = evidence
			},
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			doc := cloneVS05Map(graph)
			testCase.mutate(doc)
			data, err := json.Marshal(doc)
			if err != nil {
				t.Fatal(err)
			}
			if err := harness.ValidateChangeImpactGraphV2(data); err == nil {
				t.Fatalf("invalid terminal claim was accepted")
			}
		})
	}
}

func cloneVS05Map(input map[string]any) map[string]any {
	output := make(map[string]any, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}

func readVS05Fixture(name string) ([]byte, error) {
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("fixture path is empty")
	}
	return fs.ReadFile(schemas.FixturesFS, filepath.ToSlash(filepath.Join("fixtures", name)))
}

func expectedVS05ImplementationIDs() map[string]string {
	return map[string]string{
		"VS05-A1": "codeflow/internal/verification/impact.TestRFLSCR2VS05_A01",
		"VS05-A2": "codeflow/internal/verification/impact.TestRFLSCR2VS05_A02",
		"VS05-A3": "codeflow/internal/verification/impact.TestRFLSCR2VS05_A03",
		"VS05-A4": "codeflow/internal/verification/impact.TestRFLSCR2VS05_A04",
		"VS05-A5": "codeflow/internal/verification/impact.TestRFLSCR2VS05_A05",
		"VS05-A6": "codeflow/internal/verification/impact.TestRFLSCR2VS05_A06",
		"VS05-A7": "codeflow/internal/verification/impact.TestRFLSCR2VS05_A07",
	}
}

func vs05ExecutionRecord(t *testing.T, shared impact.Evidence) impactEvidence {
	t.Helper()
	return impactEvidence{
		Criterion:             shared.Criterion,
		ImplementationTestID:  shared.ImplementationTestID,
		ImplementationPackage: shared.ImplementationPackage,
		ExecutionPackage:      impactExecutionPackage,
		ExecutionBinary:       filepath.Base(os.Args[0]),
		ExecutionID:           t.Name(),
		SnapshotTreeDigest:    shared.SnapshotTreeDigest,
		ObjectRefs:            append([]string(nil), shared.ObjectRefs...),
	}
}

func validateVS05EvidenceRecord(criterion, executionID string, record impactEvidence, expected map[string]string) error {
	if record.Criterion != criterion {
		return fmt.Errorf("criterion identity mismatch: got %q want %q", record.Criterion, criterion)
	}
	if expected[criterion] == "" || record.ImplementationTestID != expected[criterion] || record.ImplementationPackage != impactImplementationPackage {
		return fmt.Errorf("implementation identity mismatch: %+v", record)
	}
	if record.ExecutionPackage != impactExecutionPackage || record.ExecutionBinary == "" || record.ExecutionID != executionID {
		return fmt.Errorf("execution identity mismatch: %+v", record)
	}
	if record.Result != "" && record.Result != "pass" {
		return fmt.Errorf("unknown execution result: %+v", record)
	}
	if record.Result == "pass" && (!record.ExecutionCompleted || !record.runnerObserved) {
		return fmt.Errorf("pass was recorded before a completed shared runner execution: %+v", record)
	}
	if record.ExecutionCompleted && record.Result != "pass" {
		return fmt.Errorf("execution completion has no passing result: %+v", record)
	}
	if record.SnapshotTreeDigest == "" || len(record.ObjectRefs) < 4 {
		return fmt.Errorf("immutable artifact evidence is incomplete: %+v", record)
	}
	seen := make(map[string]bool, len(record.ObjectRefs))
	hasSnapshot, hasTree, hasBasis, hasGraph, hasEvidence := false, false, false, false, false
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
				return fmt.Errorf("tree artifact ref %q does not match snapshotTreeDigest %q", ref, record.SnapshotTreeDigest)
			}
			hasTree = true
		case "basis":
			hasBasis = true
		case "impactGraph":
			hasGraph = true
		case "evidence":
			hasEvidence = true
		}
	}
	if !hasSnapshot || !hasTree || !hasBasis || !hasGraph || !hasEvidence {
		return fmt.Errorf("snapshot, tree, basis, impactGraph and Evidence artifact refs are required: %+v", record.ObjectRefs)
	}
	return nil
}

func validateCompletedVS05Records(records []impactEvidence, expectedCriteria []string, expected map[string]string) error {
	if len(records) != len(expectedCriteria) {
		return fmt.Errorf("VS-05 evidence registry has %d records, want %d", len(records), len(expectedCriteria))
	}
	expectedSet := make(map[string]bool, len(expectedCriteria))
	for _, criterion := range expectedCriteria {
		if expectedSet[criterion] {
			return fmt.Errorf("duplicate expected VS-05 criterion %s", criterion)
		}
		expectedSet[criterion] = true
	}
	counts := make(map[string]int, len(records))
	executionIDs := make(map[string]bool, len(records))
	for _, record := range records {
		if !expectedSet[record.Criterion] {
			return fmt.Errorf("unknown or zero-match VS-05 criterion %q", record.Criterion)
		}
		if err := validateVS05EvidenceRecord(record.Criterion, record.ExecutionID, record, expected); err != nil {
			return err
		}
		if record.Result != "pass" || !record.ExecutionCompleted || !record.runnerObserved {
			return fmt.Errorf("criterion %s was not completed by a shared runner", record.Criterion)
		}
		counts[record.Criterion]++
		if executionIDs[record.ExecutionID] {
			return fmt.Errorf("duplicate VS-05 execution ID %q", record.ExecutionID)
		}
		executionIDs[record.ExecutionID] = true
	}
	for _, criterion := range expectedCriteria {
		if counts[criterion] != 1 {
			return fmt.Errorf("VS-05 criterion %s executed %d times, want exactly once", criterion, counts[criterion])
		}
	}
	return nil
}
