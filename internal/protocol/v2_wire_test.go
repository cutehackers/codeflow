package protocol

import (
	"encoding/json"
	"strings"
	"testing"

	"codeflow/internal/contractharness"
	"codeflow/internal/rflscvs02"
)

func TestBuildAnalysisRequestUsesCanonicalV2Envelope(t *testing.T) {
	snapshot, err := NewSnapshot(7, map[string]string{
		"go.mod":    "module example.test\n",
		"main.go":   "package main\n",
		"README.md": "snapshot bytes\n",
	}, "basis-v2-wire")
	if err != nil {
		t.Fatal(err)
	}
	c := &Conn{cfg: Config{MaxMessageSizeBytes: DefaultAdapterMessageSizeBytes}}
	fr, err := c.buildRequest(OpDetect, snapshot.Params())
	if err != nil {
		t.Fatal(err)
	}
	var params map[string]any
	if err := json.Unmarshal(fr.env.Params, &params); err != nil {
		t.Fatal(err)
	}
	if got, want := params["schemaId"], rflscvs02.AnalyzerRequestSchemaID; got != want {
		t.Fatalf("schemaId = %v, want %v", got, want)
	}
	if got, want := params["schemaVersion"], float64(rflscvs02.SchemaVersion); got != want {
		t.Fatalf("schemaVersion = %v, want %v", got, want)
	}
	if got, want := params["requestId"], fr.env.ID; got != want {
		t.Fatalf("requestId = %v, want envelope id %v", got, want)
	}
	if got, want := params["operation"], OpDetect; got != want {
		t.Fatalf("operation = %v, want %v", got, want)
	}
	if _, ok := params["repoRoot"]; ok {
		t.Fatal("analysis request must not contain repoRoot")
	}
	if _, ok := params["snapshot"].(map[string]any); !ok {
		t.Fatalf("snapshot is not an object: %T", params["snapshot"])
	}
}

func TestBuildAnalysisRequestAcceptsTwelveMiBSnapshot(t *testing.T) {
	snapshot, err := NewSnapshot(7, map[string]string{
		"go.mod":       "module example.test\n",
		"src/large.go": strings.Repeat("x", 12<<20),
	}, "basis-large-wire")
	if err != nil {
		t.Fatal(err)
	}
	c := &Conn{cfg: Config{MaxMessageSizeBytes: DefaultAdapterMessageSizeBytes}}
	if _, err := c.buildRequest(OpDetect, snapshot.Params()); err != nil {
		t.Fatalf("12 MiB analysis request was rejected: %v", err)
	}
}

func TestAnalysisResponsePassesOneCanonicalSemanticGateAndUnwrapsPayload(t *testing.T) {
	snapshot, err := NewSnapshot(3, map[string]string{"main.go": "package main\n"}, "basis-gate")
	if err != nil {
		t.Fatal(err)
	}
	params := snapshot.Params()
	request, err := analyzerRequestForCall("cf-gate", OpDetect, params, rflscvs02.DefaultMaxMessageBytes)
	if err != nil {
		t.Fatal(err)
	}
	result := validV2Result(request)
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var detected detResult
	if err := finishCall(&reply{ok: true, result: raw}, &detected, OpDetect, params, request.RequestID, rflscvs02.DefaultMaxMessageBytes); err != nil {
		t.Fatalf("valid v2 result rejected: %v", err)
	}
	if detected.Language != "go" || !detected.Confident {
		t.Fatalf("unwrapped payload = %+v", detected)
	}
}

func TestAnalysisResponseRejectsIdentityClosureAndPayloadDrift(t *testing.T) {
	snapshot, err := NewSnapshot(3, map[string]string{"main.go": "package main\n"}, "basis-adversarial")
	if err != nil {
		t.Fatal(err)
	}
	params := snapshot.Params()
	request, err := analyzerRequestForCall("cf-adversarial", OpDetect, params, rflscvs02.DefaultMaxMessageBytes)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		mutate func(*rflscvs02.Result)
	}{
		{"requestId", func(result *rflscvs02.Result) { result.RequestID = "cf-other" }},
		{"analyzerRevision", func(result *rflscvs02.Result) {
			result.AnalyzerRevision = "analyzer-other"
		}},
		{"snapshotId", func(result *rflscvs02.Result) { result.SnapshotID = "snapshot-other" }},
		{"snapshotTreeDigest", func(result *rflscvs02.Result) { result.SnapshotTreeDigest = "tree-other" }},
		{"dependencyFingerprint", func(result *rflscvs02.Result) { result.DependencyFingerprint = "dependency-other" }},
		{"readSetIdentity", func(result *rflscvs02.Result) { result.ReadSet.ComputedBasisID = "basis-other" }},
		{"closureDrift", func(result *rflscvs02.Result) {
			result.Closure.MembershipObservations = []rflscvs02.Observation{{Kind: "membership", Path: ".", Measured: true}}
		}},
		{"payloadType", func(result *rflscvs02.Result) { result.Payload = json.RawMessage(`{"language":7,"confident":true}`) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := validV2Result(request)
			tc.mutate(&result)
			raw, err := json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}
			if err := finishCall(&reply{ok: true, result: raw}, nil, OpDetect, params, request.RequestID, rflscvs02.DefaultMaxMessageBytes); err == nil {
				t.Fatal("adversarial v2 result was accepted")
			}
		})
	}
}

func TestAnalysisRequestRejectsIncompleteSnapshotAndRetryChangesOnlyRequestID(t *testing.T) {
	if _, err := analyzerRequestForCall("cf-missing", OpDetect, map[string]any{
		"snapshot": map[string]any{"computedBasisId": "basis-only", "workspaceEpoch": 1, "files": map[string]any{}},
	}, rflscvs02.DefaultMaxMessageBytes); err == nil {
		t.Fatal("incomplete snapshot identity was accepted")
	}
	snapshot, err := NewSnapshot(1, map[string]string{"main.go": "package main\n"}, "basis-retry")
	if err != nil {
		t.Fatal(err)
	}
	c := &Conn{cfg: Config{MaxMessageSizeBytes: rflscvs02.DefaultMaxMessageBytes}}
	first, err := c.buildRequest(OpDetect, snapshot.Params())
	if err != nil {
		t.Fatal(err)
	}
	second, err := c.buildRequest(OpDetect, snapshot.Params())
	if err != nil {
		t.Fatal(err)
	}
	var firstParams, secondParams map[string]any
	if err := json.Unmarshal(first.env.Params, &firstParams); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(second.env.Params, &secondParams); err != nil {
		t.Fatal(err)
	}
	if firstParams["requestId"] == secondParams["requestId"] {
		t.Fatal("retry request reused requestId")
	}
	firstSnapshot := firstParams["snapshot"].(map[string]any)
	secondSnapshot := secondParams["snapshot"].(map[string]any)
	for _, field := range []string{"snapshotId", "rootTreeId", "computedBasisId", "dependencyFingerprint"} {
		if firstSnapshot[field] != secondSnapshot[field] {
			t.Fatalf("retry changed snapshot %s: %v -> %v", field, firstSnapshot[field], secondSnapshot[field])
		}
	}
}

func validV2Result(request rflscvs02.AnalyzerRequest) rflscvs02.Result {
	return rflscvs02.Result{
		SchemaID: rflscvs02.AnalyzerResultSchemaID, SchemaVersion: rflscvs02.SchemaVersion,
		RequestID: request.RequestID, Operation: request.Operation, AdapterVersion: "adapter/test",
		AnalyzerRevision: "analyzer/test", WorkspaceEpoch: request.Snapshot.WorkspaceEpoch,
		ComputedBasisID: request.Snapshot.ComputedBasisID, SnapshotID: request.Snapshot.SnapshotID,
		SnapshotTreeDigest: request.Snapshot.RootTreeID, DependencyFingerprint: request.Snapshot.DependencyFingerprint,
		ReadSet: rflscvs02.AnalysisReadSet{
			SchemaID: rflscvs02.ReadSetSchemaID, SchemaVersion: rflscvs02.SchemaVersion,
			ReadSetID: "readset-gate", ComputedBasisID: request.Snapshot.ComputedBasisID,
			WorkspaceEpoch: request.Snapshot.WorkspaceEpoch, Documents: []rflscvs02.ReadDocument{},
			NegativeObservations: []rflscvs02.Observation{}, MembershipObservations: []rflscvs02.Observation{}, DependencyFrontiers: []rflscvs02.Observation{},
		},
		Closure: rflscvs02.ObservationClosure{
			SchemaID: rflscvs02.ClosureSchemaID, SchemaVersion: rflscvs02.SchemaVersion,
			ClosureID: "closure-gate", AnalysisReadSetID: "readset-gate",
			ComputedBasisID: request.Snapshot.ComputedBasisID, WorkspaceEpoch: request.Snapshot.WorkspaceEpoch,
			Status: "open", NegativeObservations: []rflscvs02.Observation{}, MembershipObservations: []rflscvs02.Observation{}, DependencyFrontiers: []rflscvs02.Observation{},
			RequiredObservations: []string{}, MeasuredObservations: []string{}, IncompleteReasons: []string{},
		},
		Capability:  rflscvs02.CapabilityProfile{Adapter: "go", AdapterVersion: "adapter/test", AnalyzerRevision: "analyzer/test", Features: []string{"snapshot_bytes"}},
		Coverage:    rflscvs02.Coverage{IncludedSourceRoots: []string{"."}, Measured: true},
		Diagnostics: []rflscvs02.Diagnostic{}, Payload: json.RawMessage(`{"language":"go","confident":true}`),
	}
}

func TestV2SchemaRejectsUnmodeledPayload(t *testing.T) {
	snapshot, err := NewSnapshot(1, map[string]string{"main.go": "package main\n"}, "basis-schema")
	if err != nil {
		t.Fatal(err)
	}
	request, err := analyzerRequestForCall("cf-schema", OpDetect, snapshot.Params(), rflscvs02.DefaultMaxMessageBytes)
	if err != nil {
		t.Fatal(err)
	}
	result := validV2Result(request)
	result.Payload = json.RawMessage(`{"language":"go","confident":true,"unexpected":true}`)
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if err := contractharness.Validate(rflscvs02.AnalyzerResultSchemaID, data); err == nil {
		t.Fatal("unmodeled operation payload unexpectedly passed schema")
	}
}
