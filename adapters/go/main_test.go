package main

import (
	"encoding/json"
	"testing"

	"codeflow/internal/contractharness"
)

func TestAnalysisMetadataBindsOverlayBasis(t *testing.T) {
	params := map[string]any{
		"repoRoot":        "/does/not/exist",
		"computedBasisId": "basis-go-test",
		"workspaceEpoch":  9,
		"contentOverlay": map[string]any{
			"go.mod":      "module example.test\n",
			"cmd/main.go": "package main\nfunc Handle() {}\n",
		},
	}
	result, err := analyze("detect", params)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if err := contractharness.ValidateAdapterAnalysis(raw, "detect", "basis-go-test", 9); err != nil {
		t.Fatalf("metadata validation failed: %v", err)
	}
	readSet := result["analysisReadSet"].(map[string]any)
	if len(readSet["documents"].([]map[string]any)) != 2 {
		t.Fatalf("overlay documents = %v, want two documents", readSet["documents"])
	}
}

func TestSliceReadsOverlayWithoutWorktreeFallback(t *testing.T) {
	params := map[string]any{
		"repoRoot":        "/does/not/exist",
		"candidateId":     "cand-1234567890abcdef",
		"entrySymbolPath": "lib/main.go#Handle",
		"computedBasisId": "basis-overlay",
		"contentOverlay": map[string]any{
			"lib/main.go": "package main\nfunc Handle() {}\n",
		},
	}
	result, err := analyze("slice", params)
	if err != nil {
		t.Fatal(err)
	}
	if result["computedBasisId"] != "basis-overlay" || result["workspaceEpoch"] != int64(0) {
		t.Fatalf("slice metadata = %v", result)
	}
}
