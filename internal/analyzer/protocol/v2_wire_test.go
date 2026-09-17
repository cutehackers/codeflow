package protocol

import (
	"encoding/json"
	"strings"
	"testing"

	"codeflow/internal/collector/contractharness"
	"codeflow/internal/collector/evidence"
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
	if got, want := params["schemaId"], evidence.AnalyzerRequestSchemaID; got != want {
		t.Fatalf("schemaId = %v, want %v", got, want)
	}
	if got, want := params["schemaVersion"], float64(evidence.SchemaVersion); got != want {
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
	request, err := analyzerRequestForCall("cf-gate", OpDetect, params, evidence.DefaultMaxMessageBytes)
	if err != nil {
		t.Fatal(err)
	}
	result := validV2Result(request)
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var detected detResult
	if err := finishCall(&reply{ok: true, result: raw}, &detected, OpDetect, params, request.RequestID, evidence.DefaultMaxMessageBytes); err != nil {
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
	request, err := analyzerRequestForCall("cf-adversarial", OpDetect, params, evidence.DefaultMaxMessageBytes)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		mutate func(*evidence.Result)
	}{
		{"requestId", func(result *evidence.Result) { result.RequestID = "cf-other" }},
		{"analyzerRevision", func(result *evidence.Result) {
			result.AnalyzerRevision = "analyzer-other"
		}},
		{"snapshotId", func(result *evidence.Result) { result.SnapshotID = "snapshot-other" }},
		{"snapshotTreeDigest", func(result *evidence.Result) { result.SnapshotTreeDigest = "tree-other" }},
		{"dependencyFingerprint", func(result *evidence.Result) { result.DependencyFingerprint = "dependency-other" }},
		{"readSetIdentity", func(result *evidence.Result) { result.ReadSet.ComputedBasisID = "basis-other" }},
		{"closureDrift", func(result *evidence.Result) {
			result.Closure.MembershipObservations = []evidence.Observation{{Kind: "membership", Path: ".", Measured: true}}
		}},
		{"payloadType", func(result *evidence.Result) { result.Payload = json.RawMessage(`{"language":7,"confident":true}`) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := validV2Result(request)
			tc.mutate(&result)
			raw, err := json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}
			if err := finishCall(&reply{ok: true, result: raw}, nil, OpDetect, params, request.RequestID, evidence.DefaultMaxMessageBytes); err == nil {
				t.Fatal("adversarial v2 result was accepted")
			}
		})
	}
}

func TestAnalysisRequestRejectsIncompleteSnapshotAndRetryChangesOnlyRequestID(t *testing.T) {
	if _, err := analyzerRequestForCall("cf-missing", OpDetect, map[string]any{
		"snapshot": map[string]any{"computedBasisId": "basis-only", "workspaceEpoch": 1, "files": map[string]any{}},
	}, evidence.DefaultMaxMessageBytes); err == nil {
		t.Fatal("incomplete snapshot identity was accepted")
	}
	snapshot, err := NewSnapshot(1, map[string]string{"main.go": "package main\n"}, "basis-retry")
	if err != nil {
		t.Fatal(err)
	}
	c := &Conn{cfg: Config{MaxMessageSizeBytes: evidence.DefaultMaxMessageBytes}}
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

func validV2Result(request evidence.AnalyzerRequest) evidence.Result {
	return evidence.Result{
		SchemaID: evidence.AnalyzerResultSchemaID, SchemaVersion: evidence.SchemaVersion,
		RequestID: request.RequestID, Operation: request.Operation, AdapterVersion: "adapter/test",
		AnalyzerRevision: "analyzer/test", WorkspaceEpoch: request.Snapshot.WorkspaceEpoch,
		ComputedBasisID: request.Snapshot.ComputedBasisID, SnapshotID: request.Snapshot.SnapshotID,
		SnapshotTreeDigest: request.Snapshot.RootTreeID, DependencyFingerprint: request.Snapshot.DependencyFingerprint,
		ReadSet: evidence.AnalysisReadSet{
			SchemaID: evidence.ReadSetSchemaID, SchemaVersion: evidence.SchemaVersion,
			ReadSetID: "readset-gate", ComputedBasisID: request.Snapshot.ComputedBasisID,
			WorkspaceEpoch: request.Snapshot.WorkspaceEpoch, Documents: []evidence.ReadDocument{},
			NegativeObservations: []evidence.Observation{}, MembershipObservations: []evidence.Observation{}, DependencyFrontiers: []evidence.Observation{},
		},
		Closure: evidence.ObservationClosure{
			SchemaID: evidence.ClosureSchemaID, SchemaVersion: evidence.SchemaVersion,
			ClosureID: "closure-gate", AnalysisReadSetID: "readset-gate",
			ComputedBasisID: request.Snapshot.ComputedBasisID, WorkspaceEpoch: request.Snapshot.WorkspaceEpoch,
			Status: "open", NegativeObservations: []evidence.Observation{}, MembershipObservations: []evidence.Observation{}, DependencyFrontiers: []evidence.Observation{},
			RequiredObservations: []string{}, MeasuredObservations: []string{}, IncompleteReasons: []string{},
		},
		Capability:  evidence.CapabilityProfile{Adapter: "go", AdapterVersion: "adapter/test", AnalyzerRevision: "analyzer/test", Features: []string{"snapshot_bytes"}},
		Coverage:    evidence.Coverage{IncludedSourceRoots: []string{"."}, Measured: true},
		Diagnostics: []evidence.Diagnostic{}, Payload: json.RawMessage(`{"language":"go","confident":true}`),
	}
}

func TestV2SchemaRejectsUnmodeledPayload(t *testing.T) {
	snapshot, err := NewSnapshot(1, map[string]string{"main.go": "package main\n"}, "basis-schema")
	if err != nil {
		t.Fatal(err)
	}
	request, err := analyzerRequestForCall("cf-schema", OpDetect, snapshot.Params(), evidence.DefaultMaxMessageBytes)
	if err != nil {
		t.Fatal(err)
	}
	result := validV2Result(request)
	result.Payload = json.RawMessage(`{"language":"go","confident":true,"unexpected":true}`)
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if err := contractharness.Validate(evidence.AnalyzerResultSchemaID, data); err == nil {
		t.Fatal("unmodeled operation payload unexpectedly passed schema")
	}
}
