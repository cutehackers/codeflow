package workspace_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"codeflow/internal/contractharness"
	"codeflow/internal/workspace"
)

func TestVS03A6_ActivityAcknowledgement(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "codeflow-act-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	engine, err := workspace.NewSnapshotEngine(tempDir, 1)
	if err != nil {
		t.Fatalf("NewSnapshotEngine failed: %v", err)
	}

	// 1. Initial activity is idle
	initialAct := engine.CurrentActivity()
	if initialAct.Activity != "idle" {
		t.Errorf("expected initial activity idle, got %s", initialAct.Activity)
	}

	// 2. Measure acknowledgement latency of applying edit
	start := time.Now()
	_, snap, err := engine.ApplyVersionedEdit(context.Background(), workspace.EditRequest{
		Path:            "src/index.ts",
		Content:         []byte("console.log('hello');"),
		DocumentVersion: 1,
		Source:          workspace.SourceAgentTransaction,
	})
	if err != nil {
		t.Fatalf("ApplyVersionedEdit failed: %v", err)
	}

	act := engine.CurrentActivity()
	elapsed := time.Since(start)

	// VS03-A6 assertion: P95 <= 300ms latency
	if elapsed > 300*time.Millisecond {
		t.Errorf("acknowledgement latency %v exceeded 300ms budget", elapsed)
	}

	if act.Activity != "editing" {
		t.Errorf("expected activity editing, got %s", act.Activity)
	}
	if len(act.Scope) != 1 || act.Scope[0] != "src/index.ts" {
		t.Errorf("expected scope [src/index.ts], got %v", act.Scope)
	}
	if act.CurrentSnapshotID != snap.SnapshotID {
		t.Errorf("expected currentSnapshotId %s, got %s", snap.SnapshotID, act.CurrentSnapshotID)
	}

	// 3. State transition to analyzing
	engine.SetActivity("analyzing")
	analyzingAct := engine.CurrentActivity()
	if analyzingAct.Activity != "analyzing" {
		t.Errorf("expected activity analyzing, got %s", analyzingAct.Activity)
	}
}

func TestActivityStateV2EnvelopeValidatesAcrossLifecycle(t *testing.T) {
	engine, err := workspace.NewSnapshotEngine(t.TempDir(), 1)
	if err != nil {
		t.Fatal(err)
	}
	validate := func(label string) {
		t.Helper()
		data, err := json.Marshal(engine.CurrentActivity())
		if err != nil {
			t.Fatalf("%s marshal: %v", label, err)
		}
		if err := contractharness.ValidateActivityStateV2(data); err != nil {
			t.Fatalf("%s activity contract: %v (%s)", label, err, data)
		}
	}
	validate("initial idle")
	_, snap, err := engine.ApplyVersionedEdit(context.Background(), workspace.EditRequest{Path: "main.ts", Content: []byte("export const value = 1;"), DocumentVersion: 1, Source: workspace.SourceAgentTransaction})
	if err != nil {
		t.Fatal(err)
	}
	validate("editing")
	engine.AcknowledgeEdit(snap.SnapshotID)
	validate("acknowledged")
	engine.BeginAnalysis(snap.SnapshotID, engine.CurrentActivity().TraceID)
	validate("analyzing")
	engine.EndAnalysis(snap.SnapshotID, true)
	validate("terminal idle")
}
