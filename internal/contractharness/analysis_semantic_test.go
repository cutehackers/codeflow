package contractharness

import (
	"encoding/json"
	"testing"
)

func productionAnalysisResult(capability, closureCapability map[string]any) []byte {
	result := map[string]any{
		"schemaId": AdapterAnalysisSchemaID, "schemaVersion": 1, "operation": "slice",
		"computedBasisId": "basis-1", "workspaceEpoch": 7, "analyzerVersion": "go-structural/0.1.0",
		"analysisReadSet": map[string]any{
			"schemaId": analysisReadSetSchemaID, "schemaVersion": 1, "readSetId": "rs-1",
			"computedBasisId": "basis-1", "workspaceEpoch": 7,
			"documents": []any{}, "negativeObservations": []any{}, "membershipObservations": []any{}, "dependencyFrontiers": []any{},
		},
		"causalObservationClosure": map[string]any{
			"schemaId": closureSchemaID, "schemaVersion": 1, "closureId": "close-1", "analysisReadSetId": "rs-1",
			"computedBasisId": "basis-1", "workspaceEpoch": 7, "closureStatus": "open",
			"negativeObservations": []any{}, "membershipObservations": []any{}, "dependencyFrontiers": []any{},
			"capabilityProfile": closureCapability,
			"coverageBoundary":  closureCapability["coverageBoundary"],
			"incompleteReasons": []any{"scope is bounded"},
		},
		"capabilityProfile": capability,
		"diagnostics":       []any{},
	}
	raw, _ := json.Marshal(result)
	return raw
}

func productionCapability() map[string]any {
	return map[string]any{
		"adapter": "go", "adapterVersion": "0.1.0", "analyzerRevision": "go-structural/0.1.0",
		"features":         []any{"snapshot_overlay", "membership"},
		"unsupported":      []any{"runtime_observation"},
		"coverageBoundary": map[string]any{"includedSourceRoots": []any{"."}, "measured": true},
	}
}

func strictSnapshotParams() map[string]any {
	return map[string]any{
		"requiredObservations": []any{},
		"snapshot": map[string]any{
			"computedBasisId": "basis-1", "workspaceEpoch": 7,
			"files": map[string]any{"lib/main.go": "package main\n"},
		},
	}
}

func TestValidateAdapterAnalysisAgainstSnapshotRejectsUnmeasuredClosedResult(t *testing.T) {
	result := map[string]any{
		"schemaId": AdapterAnalysisSchemaID, "schemaVersion": 1, "operation": "slice",
		"computedBasisId": "basis-1", "workspaceEpoch": 7, "analyzerVersion": "go/2",
		"analysisReadSet": map[string]any{
			"schemaId": analysisReadSetSchemaID, "schemaVersion": 1, "readSetId": "rs-1",
			"computedBasisId": "basis-1", "workspaceEpoch": 7,
			"documents":            []any{map[string]any{"path": "lib/main.go", "contentHash": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "byteLength": 14}},
			"negativeObservations": []any{}, "membershipObservations": []any{}, "dependencyFrontiers": []any{},
		},
		"causalObservationClosure": map[string]any{
			"schemaId": closureSchemaID, "schemaVersion": 1, "closureId": "close-1", "analysisReadSetId": "rs-1",
			"computedBasisId": "basis-1", "workspaceEpoch": 7, "closureStatus": "closed",
			"negativeObservations": []any{}, "membershipObservations": []any{}, "dependencyFrontiers": []any{},
			"requiredObservations": []any{"membership"}, "measuredObservations": []any{},
			"capabilityProfile": map[string]any{"adapter": "go", "features": []any{"membership"}}, "coverageBoundary": map[string]any{"measured": true},
		},
		"capabilityProfile": map[string]any{"adapter": "go", "features": []any{"membership"}},
		"diagnostics":       []any{},
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	params := map[string]any{"snapshot": map[string]any{"computedBasisId": "basis-1", "workspaceEpoch": 7, "files": map[string]any{"lib/main.go": "package main\n"}}}
	if err := ValidateAdapterAnalysisAgainstSnapshot(raw, "slice", "basis-1", 7, params); err == nil {
		t.Fatal("closed result with unmeasured required observation was accepted")
	}
}

func TestValidateAdapterAnalysisAgainstSnapshotRejectsReadSetContentMismatch(t *testing.T) {
	result := map[string]any{
		"schemaId": AdapterAnalysisSchemaID, "schemaVersion": 1, "operation": "detect",
		"computedBasisId": "basis-1", "workspaceEpoch": 7, "analyzerVersion": "go/2",
		"analysisReadSet": map[string]any{
			"schemaId": analysisReadSetSchemaID, "schemaVersion": 1, "readSetId": "rs-1", "computedBasisId": "basis-1", "workspaceEpoch": 7,
			"documents": []any{map[string]any{"path": "go.mod", "contentHash": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "byteLength": 4}}, "negativeObservations": []any{}, "membershipObservations": []any{}, "dependencyFrontiers": []any{},
		},
		"causalObservationClosure": map[string]any{
			"schemaId": closureSchemaID, "schemaVersion": 1, "closureId": "close-1", "analysisReadSetId": "rs-1", "computedBasisId": "basis-1", "workspaceEpoch": 7, "closureStatus": "closed", "negativeObservations": []any{}, "membershipObservations": []any{}, "dependencyFrontiers": []any{}, "capabilityProfile": map[string]any{}, "coverageBoundary": map[string]any{},
		},
		"capabilityProfile": map[string]any{}, "diagnostics": []any{},
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	params := map[string]any{"snapshot": map[string]any{"computedBasisId": "basis-1", "workspaceEpoch": 7, "files": map[string]any{"go.mod": "module example\n"}}}
	if err := ValidateAdapterAnalysisAgainstSnapshot(raw, "detect", "basis-1", 7, params); err == nil {
		t.Fatal("read-set content mismatch was accepted")
	}
}

func TestValidateAdapterAnalysisAgainstSnapshotRequiresMeasuredCapabilityAndCoverage(t *testing.T) {
	params := map[string]any{"snapshot": map[string]any{"computedBasisId": "basis-1", "workspaceEpoch": 7}}
	missing := productionAnalysisResult(map[string]any{}, map[string]any{})
	if err := ValidateAdapterAnalysisAgainstSnapshot(missing, "slice", "basis-1", 7, params); err == nil {
		t.Fatal("missing capability profile fields were accepted")
	}

	badCoverage := productionCapability()
	badCoverage["coverageBoundary"] = map[string]any{"includedSourceRoots": []any{"."}, "measured": false}
	if err := ValidateAdapterAnalysisAgainstSnapshot(productionAnalysisResult(badCoverage, badCoverage), "slice", "basis-1", 7, params); err == nil {
		t.Fatal("unmeasured coverage boundary was accepted")
	}
}

func TestValidateAdapterAnalysisAgainstSnapshotRejectsCapabilityConflictAndClosureDrift(t *testing.T) {
	params := map[string]any{"snapshot": map[string]any{"computedBasisId": "basis-1", "workspaceEpoch": 7}}
	conflict := productionCapability()
	conflict["unsupported"] = []any{"membership"}
	if err := ValidateAdapterAnalysisAgainstSnapshot(productionAnalysisResult(conflict, conflict), "slice", "basis-1", 7, params); err == nil {
		t.Fatal("capability listed as both supported and unsupported was accepted")
	}

	top := productionCapability()
	closure := productionCapability()
	closure["adapter"] = "other-adapter"
	if err := ValidateAdapterAnalysisAgainstSnapshot(productionAnalysisResult(top, closure), "slice", "basis-1", 7, params); err == nil {
		t.Fatal("closure capability drift was accepted")
	}
}

func TestValidateAdapterAnalysisAgainstSnapshotStrictlyBindsObservationClosure(t *testing.T) {
	params := strictSnapshotParams()
	result := map[string]any{}
	if err := json.Unmarshal(productionAnalysisResult(productionCapability(), productionCapability()), &result); err != nil {
		t.Fatal(err)
	}
	readSet := result["analysisReadSet"].(map[string]any)
	closure := result["causalObservationClosure"].(map[string]any)
	observation := map[string]any{"kind": "source_membership", "path": ".", "valueHash": "hash-a", "measured": true}
	readSet["membershipObservations"] = []any{observation}
	closure["membershipObservations"] = []any{map[string]any{"kind": "source_membership", "path": ".", "valueHash": "hash-b", "measured": true}}
	closure["measuredObservations"] = []any{"membership"}
	closure["closureStatus"] = "closed"
	closure["requiredObservations"] = []any{}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateAdapterAnalysisAgainstSnapshot(raw, "slice", "basis-1", 7, params); err == nil {
		t.Fatal("closure observation content drift was accepted")
	}
}

func TestValidateAdapterAnalysisAgainstSnapshotStrictlyRequiresMeasuredObservationFields(t *testing.T) {
	params := strictSnapshotParams()
	result := map[string]any{}
	if err := json.Unmarshal(productionAnalysisResult(productionCapability(), productionCapability()), &result); err != nil {
		t.Fatal(err)
	}
	readSet := result["analysisReadSet"].(map[string]any)
	closure := result["causalObservationClosure"].(map[string]any)
	observation := map[string]any{"kind": "source_membership", "path": ".", "valueHash": "hash-a"}
	readSet["membershipObservations"] = []any{observation}
	closure["membershipObservations"] = []any{observation}
	closure["closureStatus"] = "open"
	closure["incompleteReasons"] = []any{"membership is not measured"}
	closure["requiredObservations"] = []any{"membership"}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateAdapterAnalysisAgainstSnapshot(raw, "slice", "basis-1", 7, params); err == nil {
		t.Fatal("observation without measured boolean was accepted")
	}
}

func TestValidateAdapterAnalysisAgainstSnapshotStrictlyRequiresCapabilityIdentity(t *testing.T) {
	params := strictSnapshotParams()
	capability := productionCapability()
	delete(capability, "adapterVersion")
	if err := ValidateAdapterAnalysisAgainstSnapshot(productionAnalysisResult(capability, capability), "slice", "basis-1", 7, params); err == nil {
		t.Fatal("capability without adapterVersion was accepted")
	}
}
