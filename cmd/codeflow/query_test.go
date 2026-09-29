package main

import (
	"bytes"
	"encoding/json"
	"os"
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
	code = executeQuery([]string{repoRoot, "--entry", "app/page.tsx#HomePage.onQuickCheckout", "--json"}, &outBuf, &errBuf)
	if code != 0 {
		t.Fatalf("expected 0 exit code, got %d. stderr: %s", code, errBuf.String())
	}

	var jsonOutput map[string]any
	if err := json.Unmarshal(outBuf.Bytes(), &jsonOutput); err != nil {
		t.Fatalf("failed to parse JSON output: %v, raw: %s", err, outBuf.String())
	}
	if _, ok := jsonOutput["candidateAnswer"]; !ok {
		t.Error("expected candidateAnswer in JSON output")
	}
	if _, ok := jsonOutput["semanticMap"]; !ok {
		t.Error("expected semanticMap in JSON output")
	}

	// 4. Human readable output format (presentation order: Answer -> Flow -> Evidence -> Unknowns)
	outBuf.Reset()
	errBuf.Reset()
	code = executeQuery([]string{repoRoot, "--entry", "app/page.tsx#HomePage.onQuickCheckout"}, &outBuf, &errBuf)
	if code != 0 {
		t.Fatalf("expected 0 exit code, got %d. stderr: %s", code, errBuf.String())
	}
	text := outBuf.String()
	idxAnswer := strings.Index(text, "Candidate Answer")
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

func TestCLIQuerySelectsEntryLanguageInMixedRepository(t *testing.T) {
	root := t.TempDir()
	for path, source := range map[string]string{
		"go.mod":       "module example.com/mixed\n\ngo 1.26\n",
		"package.json": `{"name":"mixed-repository","version":"1.0.0"}`,
		"checkout.go":  "package mixed\n\ntype Service struct{}\n\nfunc (s *Service) ProcessCheckout(amount int) int { return amount + 1 }\n",
		"src/order.ts": "export function processOrder(amount: number) { return amount + 2; }\n",
	} {
		fullPath := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fullPath, []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	moduleRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEFLOW_ADAPTER_GO_BIN", "gorun:"+filepath.Join(moduleRoot, "adapters", "go"))
	t.Setenv("CODEFLOW_ADAPTER_TYPESCRIPT_BIN", "noderun:"+filepath.Join(moduleRoot, "adapters", "typescript"))
	for _, tc := range []struct{ name, flag, value, path string }{
		{"go entry", "--entry", "checkout.go#Service.ProcessCheckout", "checkout.go"},
		{"typescript entry", "--entry", "src/order.ts#processOrder", "src/order.ts"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var output, errors bytes.Buffer
			if code := executeQuery([]string{root, tc.flag, tc.value, "--json"}, &output, &errors); code != 0 {
				t.Fatalf("query failed: %s", errors.String())
			}
			var result struct {
				SemanticMap struct {
					Steps []struct {
						Anchor struct {
							RepoRelativePath string `json:"repoRelativePath"`
						} `json:"anchor"`
					} `json:"steps"`
				} `json:"semanticMap"`
			}
			if err := json.Unmarshal(output.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if len(result.SemanticMap.Steps) == 0 || result.SemanticMap.Steps[0].Anchor.RepoRelativePath != tc.path {
				t.Fatalf("query selected the wrong source for %s: %+v", tc.value, result.SemanticMap.Steps)
			}
		})
	}
	var output, errors bytes.Buffer
	if code := executeQuery([]string{root, "--request", "ProcessCheckout"}, &output, &errors); code == 0 ||
		!strings.Contains(errors.String(), "checkout.go#Service.ProcessCheckout") {
		t.Fatalf("natural request must expose the Go candidate without inferring its intent: %s", errors.String())
	}
}
