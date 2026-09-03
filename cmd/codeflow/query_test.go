package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIQueryCommand(t *testing.T) {
	repoRoot, err := filepath.Abs("../../test/fixtures/nextjs-app-fixture")
	if err != nil {
		t.Fatal(err)
	}
	moduleRoot, _ := filepath.Abs("../..")
	t.Setenv("CODEFLOW_ADAPTER_TYPESCRIPT_BIN", "noderun:"+filepath.Join(moduleRoot, "adapters", "typescript"))

	// 1. Missing precondition
	var outBuf bytes.Buffer
	var errBuf bytes.Buffer
	code := executeQuery([]string{repoRoot}, &outBuf, &errBuf)
	if code == 0 {
		t.Errorf("expected non-zero exit code for missing precondition, got %d", code)
	}
	if !strings.Contains(errBuf.String(), "missing_precondition") {
		t.Errorf("expected missing_precondition in stderr, got: %s", errBuf.String())
	}

	// 2. Ambiguous target
	outBuf.Reset()
	errBuf.Reset()
	code = executeQuery([]string{repoRoot, "--request", "checkout"}, &outBuf, &errBuf)
	if code == 0 {
		t.Errorf("expected non-zero exit code for ambiguous target, got %d", code)
	}
	if !strings.Contains(errBuf.String(), "ambiguous_target") {
		t.Errorf("expected ambiguous_target in stderr, got: %s", errBuf.String())
	}

	// 3. Unambiguous query with JSON output
	outBuf.Reset()
	errBuf.Reset()
	code = executeQuery([]string{repoRoot, "--entry", "app/page.tsx#HomePage.handleQuickCheckout", "--json"}, &outBuf, &errBuf)
	if code != 0 {
		t.Fatalf("expected 0 exit code, got %d. stderr: %s", code, errBuf.String())
	}

	var jsonOutput map[string]any
	if err := json.Unmarshal(outBuf.Bytes(), &jsonOutput); err != nil {
		t.Fatalf("failed to parse JSON output: %v, raw: %s", err, outBuf.String())
	}
	if _, ok := jsonOutput["currentAnswer"]; !ok {
		t.Error("expected currentAnswer in JSON output")
	}
	if _, ok := jsonOutput["semanticMap"]; !ok {
		t.Error("expected semanticMap in JSON output")
	}

	// 4. Human readable output format (presentation order: Answer -> Flow -> Evidence -> Unknowns)
	outBuf.Reset()
	errBuf.Reset()
	code = executeQuery([]string{repoRoot, "--entry", "app/page.tsx#HomePage.handleQuickCheckout"}, &outBuf, &errBuf)
	if code != 0 {
		t.Fatalf("expected 0 exit code, got %d. stderr: %s", code, errBuf.String())
	}
	text := outBuf.String()
	idxAnswer := strings.Index(text, "Current Answer")
	idxFlow := strings.Index(text, "Semantic Flow Rail")
	idxEvidence := strings.Index(text, "Evidence Dock")
	idxUnknowns := strings.Index(text, "Unknowns")

	if idxAnswer < 0 || idxFlow < 0 || idxEvidence < 0 || idxUnknowns < 0 {
		t.Fatalf("missing required sections in output: %s", text)
	}
	if !(idxAnswer < idxFlow && idxFlow < idxEvidence && idxEvidence < idxUnknowns) {
		t.Errorf("presentation order violation: expected Answer < Flow < Evidence < Unknowns; got indices %d, %d, %d, %d",
			idxAnswer, idxFlow, idxEvidence, idxUnknowns)
	}
}
