package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const flowViewInputFixture = `{"flowId":"flow-import","frames":[{"frameID":"custom-purpose","ordinal":1,"role":"process","title":"요청 처리","stepRefs":["call","return"],"primaryStepRef":"return","status":"partial","frameMatchKey":"process|run"}],"steps":[{"stepId":"call","ordinal":1,"kind":"call","name":"execute","anchor":{"repoRelativePath":"flow.ts","enclosingSymbolPath":"run"}},{"stepId":"return","ordinal":2,"kind":"return","name":"return value","anchor":{"repoRelativePath":"flow.ts","enclosingSymbolPath":"run"}}],"edges":[{"fromStepId":"call","toStepId":"return","kind":"control_flow","resolutionStatus":"resolved"}],"summaryLimitations":[{"code":"grouping_evidence_missing","message":"목적 근거 일부 미확인","frameRefs":["custom-purpose"]}]}`

func TestFlowViewInputPreservesCuratedMembership(t *testing.T) {
	view, err := decodeFlowViewInput([]byte(flowViewInputFixture))
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	var actual map[string]any
	if err := json.Unmarshal(encoded, &actual); err != nil {
		t.Fatal(err)
	}
	sequence := actual["flowSequence"].(map[string]any)
	frame := sequence["frames"].([]any)[0].(map[string]any)
	if frame["frameID"] != "custom-purpose" || frame["primaryStepRef"] != "return" || len(frame["stepRefs"].([]any)) != 2 {
		t.Fatal("input was recurred or lost membership")
	}
	if actual["sourceContextMissing"] != true || actual["needsReanalysis"] != true {
		t.Fatal("missing source context was hidden")
	}
	if len(sequence["summaryLimitations"].([]any)) != 1 {
		t.Fatal("summary limitation lost")
	}
	second, err := decodeFlowViewInput([]byte(flowViewInputFixture))
	if err != nil {
		t.Fatal(err)
	}
	secondJSON, err := json.Marshal(second)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encoded, secondJSON) {
		t.Fatal("import identity is nondeterministic")
	}
	actual["viewId"] = "foreign-store-id"
	actual["optionalExtension"] = "preserved"
	input, err := json.Marshal(actual)
	if err != nil {
		t.Fatal(err)
	}
	full, err := decodeFlowViewInput(input)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := full["viewId"]; exists {
		t.Fatal("foreign view identity retained")
	}
	if _, exists := full["optionalExtension"]; exists {
		t.Fatal("unvalidated external metadata retained")
	}
}

func TestFlowViewInputRejectsInvalidReferences(t *testing.T) {
	for _, tc := range []struct{ name, input string }{
		{"no steps", `{"flowId":"flow-import","frames":[{"frameID":"fake"}]}`},
		{"missing child", strings.Replace(flowViewInputFixture, `"stepRefs":["call","return"]`, `"stepRefs":["missing","return"]`, 1)},
		{"duplicate child", strings.Replace(flowViewInputFixture, `"stepRefs":["call","return"]`, `"stepRefs":["return","return"]`, 1)},
		{"missing edge target", strings.Replace(flowViewInputFixture, `"toStepId":"return"`, `"toStepId":"missing"`, 1)},
		{"evidence promotion", strings.Replace(flowViewInputFixture, `"status":"partial"`, `"status":"verified"`, 1)},
		{"null", `null`},
		{"array", `[]`},
		{"missing frames", `{"flowId":"flow-import"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := decodeFlowViewInput([]byte(tc.input)); err == nil {
				t.Fatal("invalid input accepted")
			}
		})
	}
	redacted, err := decodeFlowViewInput([]byte(strings.Replace(flowViewInputFixture, "요청 처리", "password='private-title'", 1)))
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(redacted)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "private-title") {
		t.Fatal("secret retained in imported view")
	}
}

func TestFlowViewCLIInputOpensExactStoredView(t *testing.T) {
	bin := buildBinary(t)
	for _, mode := range []string{"stdin", "file", "external-proof"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			input := []byte(flowViewInputFixture)
			if mode == "external-proof" {
				input = flowViewUntrustedProofFixture(t)
			}
			target := "-"
			if mode != "stdin" {
				target = filepath.Join(root, "flow.json")
				if err := os.WriteFile(target, input, 0600); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, bin, "view", target, "--port", "0", "--token", "input-review-token")
			command.Dir = root
			command.Env = append(os.Environ(), "CODEFLOW_NONINTERACTIVE=1")
			if mode == "stdin" {
				command.Stdin = strings.NewReader(flowViewInputFixture)
			}
			pipe, err := command.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			var stderr bytes.Buffer
			command.Stderr = &stderr
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = command.Process.Kill(); _ = command.Wait() }()
			var viewURL string
			scanner := bufio.NewScanner(pipe)
			for scanner.Scan() {
				line := strings.TrimSpace(scanner.Text())
				if strings.HasPrefix(line, "http://") {
					viewURL = line
					break
				}
			}
			if viewURL == "" {
				t.Fatalf("input view URL missing: %s", stderr.String())
			}
			parsed, err := url.Parse(viewURL)
			if err != nil {
				t.Fatal(err)
			}
			if parsed.Query().Get("viewId") == "" {
				t.Fatal("CLI opened home instead of supplied hierarchy")
			}
			parsed.Path = "/api/view"
			client := &http.Client{Timeout: 5 * time.Second}
			response, err := client.Get(parsed.String())
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != http.StatusOK {
				t.Fatalf("HTTP status %d", response.StatusCode)
			}
			var view struct {
				FlowSequence struct {
					Frames []struct {
						FrameID, PrimaryStepRef string
						StepRefs                []string
					}
				}
			}
			body, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatal(err)
			}
			for _, forbidden := range []string{"fabricated_source", `"authority":"current"`, `"status":"confirmed"`, `"sourceValidationStatus":"verified"`} {
				if bytes.Contains(body, []byte(forbidden)) {
					t.Fatalf("HTTP retained external proof: %s", forbidden)
				}
			}
			var stored map[string]any
			if err := json.Unmarshal(body, &stored); err != nil {
				t.Fatal(err)
			}
			if stored["needsReanalysis"] != true || stored["sourceContextMissing"] != true {
				t.Fatal("HTTP hid unverified input state")
			}
			if err := json.Unmarshal(body, &view); err != nil {
				t.Fatal(err)
			}
			if len(view.FlowSequence.Frames) != 1 || view.FlowSequence.Frames[0].FrameID != "custom-purpose" || view.FlowSequence.Frames[0].PrimaryStepRef != "return" || len(view.FlowSequence.Frames[0].StepRefs) != 2 {
				t.Fatalf("CLI lost supplied hierarchy: %+v", view)
			}
		})
	}
}

func flowViewUntrustedProofFixture(t *testing.T) []byte {
	t.Helper()
	base, err := decodeFlowViewInput([]byte(flowViewInputFixture))
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(base)
	if err != nil {
		t.Fatal(err)
	}
	var input map[string]any
	if err := json.Unmarshal(data, &input); err != nil {
		t.Fatal(err)
	}
	input["flowContexts"] = map[string]any{"return": map[string]any{"precision": "exact", "sourceValidationStatus": "verified", "displayedLines": []any{map[string]any{"lineNumber": 1, "text": "fabricated_source()", "isHit": true}}}}
	input["sourceFiles"] = map[string]any{"flow.ts": []any{"fabricated_source()"}}
	input["proofManifest"] = map[string]any{"status": "passed"}
	model := input["semanticMap"].(map[string]any)
	model["authority"] = "current"
	model["freshness"] = "current"
	model["settlement"] = "passed"
	model["requirementAlignment"] = []any{map[string]any{"criterionId": "fake", "status": "confirmed"}}
	data, err = json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestFlowViewInputDoesNotTrustImportedProofOrRenderedSource(t *testing.T) {
	view, err := decodeFlowViewInput(flowViewUntrustedProofFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"flowContexts", "sourceFiles", "proofManifest"} {
		if _, exists := view[key]; exists {
			t.Fatalf("external source/proof claims retained: %s", key)
		}
	}
	encoded, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte("fabricated_source")) || bytes.Contains(encoded, []byte(`"authority":"current"`)) || bytes.Contains(encoded, []byte(`"status":"confirmed"`)) {
		t.Fatal("untrusted source/proof retained")
	}
}
