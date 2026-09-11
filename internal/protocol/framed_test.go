package protocol

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"codeflow/internal/contractharness"
)

func TestVS01JSONRPCFrameRoundTripUsesUTF8ByteLength(t *testing.T) {
	body := []byte(`{"jsonrpc":"2.0","id":"req-1","method":"initialize","params":{"label":"\uC548\uB155"}}`)
	var framed bytes.Buffer
	if err := writeFrame(&framed, body); err != nil {
		t.Fatalf("writeFrame() error = %v", err)
	}
	wire := framed.String()
	got, err := readFrame(&framed, 1024)
	if err != nil {
		t.Fatalf("readFrame() error = %v", err)
	}
	if string(got) != string(body) {
		t.Fatalf("readFrame() = %q, want %q", got, body)
	}
	if !strings.HasPrefix(wire, "Content-Length: ") || !strings.Contains(wire, "\r\n\r\n") {
		t.Fatalf("frame = %q, want Content-Length framing", wire)
	}
}

func TestVS01LogicalPingMapsToInitialize(t *testing.T) {
	if got := rpcMethodForOp(OpPing); got != OpInitialize {
		t.Fatalf("rpcMethodForOp(%q) = %q, want %q", OpPing, got, OpInitialize)
	}
}

func TestVS01AdapterAnalysisRejectsMismatchedBasis(t *testing.T) {
	raw := json.RawMessage(`{
		"schemaId":"https://codeflow.local/schemas/adapter-analysis.schema.json",
		"schemaVersion":1,
		"operation":"slice",
		"computedBasisId":"basis-1",
		"workspaceEpoch":7,
		"analysisReadSet":{"schemaId":"https://codeflow.local/schemas/analysis-read-set.schema.json","schemaVersion":1,"readSetId":"readset-1","computedBasisId":"basis-1","workspaceEpoch":7,"documents":[],"negativeObservations":[],"membershipObservations":[],"dependencyFrontiers":[]},
		"causalObservationClosure":{"schemaId":"https://codeflow.local/schemas/causal-observation-closure.schema.json","schemaVersion":1,"closureId":"closure-1","analysisReadSetId":"readset-1","computedBasisId":"basis-1","workspaceEpoch":7,"closureStatus":"closed","negativeObservations":[],"membershipObservations":[],"dependencyFrontiers":[],"capabilityProfile":{},"coverageBoundary":{}},
		"capabilityProfile":{},
		"analyzerVersion":"test/1",
		"diagnostics":[]
	}`)
	if err := contractharness.ValidateAdapterAnalysis(raw, "slice", "basis-2", 7); err == nil {
		t.Fatal("ValidateAdapterAnalysis() error = nil, want mismatched basis error")
	}
}

func TestVS01CancelNotificationIsJSONRPC(t *testing.T) {
	var framed bytes.Buffer
	if err := writeJSONRPCNotification(context.Background(), &framed, "$/cancelRequest", map[string]any{"id": "req-1"}); err != nil {
		t.Fatalf("writeJSONRPCNotification() error = %v", err)
	}
	body, err := readFrame(&framed, 1024)
	if err != nil {
		t.Fatalf("readFrame() error = %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if got["method"] != "$/cancelRequest" || got["jsonrpc"] != JSONRPCVersion {
		t.Fatalf("notification = %#v", got)
	}
}
