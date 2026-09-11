package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
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

func TestProductionFramingRejectsOversizedBeforeAllocation(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "codeflow-go-adapter")
	build := exec.Command("go", "build", "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build adapter: %v\n%s", err, out)
	}
	frame := []byte(fmt.Sprintf("Content-Length: %d\r\n\r\n", maxMessageBytes+1))
	cmd := exec.Command(bin)
	cmd.Stdin = bytes.NewReader(frame)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("oversized adapter request exited with error: %v", err)
	}
	if len(out) > 4096 {
		t.Fatalf("oversized diagnostic was not bounded: %d bytes", len(out))
	}
	if !strings.Contains(string(out), "maxMessageBytes") {
		t.Fatalf("oversized response missing bound diagnostic: %s", out)
	}
	if _, err := os.Stat(bin); err != nil {
		t.Fatalf("adapter binary disappeared: %v", err)
	}
}

func TestProductionDiagnosticRedactionPath(t *testing.T) {
	secretValue := strings.Repeat("go-adapter-secret-", 200)
	message := `{"databasePassword":"` + secretValue
	var output bytes.Buffer
	server := &server{out: bufio.NewWriter(&output)}
	server.errorResponse("diag-1", "E_ADAPTER_INTERNAL", message, false)
	body, err := readFrame(bufio.NewReader(bytes.NewReader(output.Bytes())), maxMessageBytes)
	if err != nil {
		t.Fatalf("read adapter error frame: %v", err)
	}
	if strings.Contains(string(body), secretValue[:32]) {
		t.Fatalf("adapter diagnostic secret leaked before egress: %s", body)
	}
	if !strings.Contains(string(body), "***REDACTED***") {
		t.Fatalf("adapter diagnostic lacks redaction marker: %s", body)
	}
}

func TestProductionResponseWriterEnforcesExactAndOversizedBounds(t *testing.T) {
	const testMessageBytes = int64(1 << 20)
	if maxMessageBytes != 128<<20 {
		t.Fatalf("production maxMessageBytes = %d, want %d", maxMessageBytes, 128<<20)
	}
	base := map[string]any{
		"jsonrpc": "2.0",
		"id":      "bound-1",
		"result":  map[string]any{"padding": ""},
	}
	baseBody, err := json.Marshal(base)
	if err != nil {
		t.Fatal(err)
	}
	base["result"].(map[string]any)["padding"] = strings.Repeat("x", int(testMessageBytes)-len(baseBody))
	exactBody := boundedResponseBody(base, testMessageBytes)
	if int64(len(exactBody)) != testMessageBytes {
		t.Fatalf("exact-bound response length = %d, want %d", len(exactBody), testMessageBytes)
	}

	var output bytes.Buffer
	s := &server{out: bufio.NewWriter(&output)}
	s.writeWithLimit(base, testMessageBytes)
	framedExact, err := readFrame(bufio.NewReader(bytes.NewReader(output.Bytes())), testMessageBytes)
	if err != nil {
		t.Fatalf("read exact response frame: %v", err)
	}
	if int64(len(framedExact)) != testMessageBytes {
		t.Fatalf("written exact response length = %d, want %d", len(framedExact), testMessageBytes)
	}

	oversized := map[string]any{
		"jsonrpc": "2.0",
		"id":      "bound-2",
		"result":  map[string]any{"padding": strings.Repeat("x", int(testMessageBytes)+1)},
	}
	output.Reset()
	s = &server{out: bufio.NewWriter(&output)}
	s.writeWithLimit(oversized, testMessageBytes)
	framedFallback, err := readFrame(bufio.NewReader(bytes.NewReader(output.Bytes())), testMessageBytes)
	if err != nil {
		t.Fatalf("read oversized fallback frame: %v", err)
	}
	if int64(len(framedFallback)) > testMessageBytes {
		t.Fatalf("oversized fallback length = %d, exceeds %d", len(framedFallback), testMessageBytes)
	}
	var decoded map[string]any
	if err := json.Unmarshal(framedFallback, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["error"] == nil || decoded["result"] != nil {
		t.Fatalf("oversized response was not converted to typed error: %s", framedFallback)
	}

	// A large request id must not make the bounded fallback itself
	// unwriteable. The fallback may drop the untrusted id to preserve a typed
	// bounded response.
	hugeID := strings.Repeat("i", 512)
	minimalFallback := boundedResponseBody(map[string]any{
		"jsonrpc": "2.0",
		"id":      hugeID,
		"result":  map[string]any{"padding": strings.Repeat("x", 512)},
	}, 256)
	if minimalFallback == nil || len(minimalFallback) > 256 {
		t.Fatalf("huge-id fallback = %d bytes, want bounded response", len(minimalFallback))
	}
	var minimalDecoded map[string]any
	if err := json.Unmarshal(minimalFallback, &minimalDecoded); err != nil {
		t.Fatal(err)
	}
	if minimalDecoded["id"] != "" || minimalDecoded["error"] == nil {
		t.Fatalf("huge-id fallback = %s, want empty-id typed error", minimalFallback)
	}
	productionHugeID := strings.Repeat("i", int(testMessageBytes)-64)
	productionFallback := boundedResponseBody(map[string]any{
		"jsonrpc": "2.0", "id": productionHugeID,
		"result": map[string]any{"padding": strings.Repeat("x", 256)},
	}, testMessageBytes)
	if productionFallback == nil || int64(len(productionFallback)) > testMessageBytes {
		t.Fatalf("production huge-id fallback = %d bytes, want bounded response", len(productionFallback))
	}
	if tooSmall := boundedResponseBody(map[string]any{"id": "x", "result": strings.Repeat("x", 256)}, 64); tooSmall != nil {
		t.Fatalf("non-negotiated small bound produced an oversized fallback: %d bytes", len(tooSmall))
	}
}

func v2TrackerResult(t *testing.T, id, operation string, files map[string]string, required []string, payload map[string]any) map[string]any {
	t.Helper()
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	documents := make([]any, 0, len(paths))
	protocolFiles := make(map[string]any, len(files))
	var tree strings.Builder
	for _, path := range paths {
		content := files[path]
		hash := digest([]byte(content))
		protocolFiles[path] = content
		documents = append(documents, map[string]any{
			"path": path, "documentRevisionId": "rev-" + hash, "contentId": hash,
			"documentVersion": 1, "contentHash": hash, "byteLength": len([]byte(content)),
		})
		fmt.Fprintf(&tree, "%s:%s\n", path, hash)
	}
	treeDigest := digest([]byte(tree.String()))
	params := map[string]any{
		"schemaId": analyzerRequestSchemaID, "schemaVersion": 2,
		"requestId": id, "operation": operation,
		"requiredObservations": required,
		"snapshot": map[string]any{
			"schemaId": analyzerRequestSchemaID, "schemaVersion": 2,
			"snapshotId": "snapshot-" + treeDigest[:12], "workspaceEpoch": int64(7),
			"computedBasisId": "basis-" + treeDigest[:12], "rootTreeId": treeDigest,
			"dependencyFingerprint": "dependency-" + treeDigest[:12],
			"documents":             documents, "files": protocolFiles,
			"repositoryPathWriteAudit": map[string]any{
				"codeflowWriteCount": 0, "sourceIntegrityViolation": false,
				"capturedSnapshotTreeDigest": treeDigest,
			},
		},
		"payload": payload,
	}
	legacy, err := analyze(operation, flattenOperationParams(params))
	if err != nil {
		t.Fatalf("analyze %s: %v", operation, err)
	}
	result := analyzerResultV2(id, operation, params, legacy)
	closure := result["causalObservationClosure"].(map[string]any)
	got := closure["closureDigest"]
	unsigned := make(map[string]any, len(closure))
	for key, value := range closure {
		if key != "closureDigest" {
			unsigned[key] = value
		}
	}
	encoded, err := json.Marshal(map[string]any{"readSet": result["analysisReadSet"], "closure": unsigned})
	if err != nil || got != digest(encoded) {
		t.Fatalf("v2 closure must bind its read set and observations: %v", got)
	}
	return result
}

func mapList(value any) []map[string]any {
	switch typed := value.(type) {
	case []map[string]any:
		return typed
	case []any:
		out := make([]map[string]any, 0, len(typed))
		for _, item := range typed {
			if object, ok := item.(map[string]any); ok {
				out = append(out, object)
			}
		}
		return out
	default:
		return nil
	}
}

func v2ReadSet(result map[string]any) map[string]any {
	return result["analysisReadSet"].(map[string]any)
}

func v2Closure(result map[string]any) map[string]any {
	return result["causalObservationClosure"].(map[string]any)
}

func TestV2ObservationTrackerUsesActualOperationReads(t *testing.T) {
	detect := v2TrackerResult(t, "detect-missing", "detect", map[string]string{
		"README.md": "not consulted",
	}, []string{"negative_lookup", "membership", "dependency_frontier"}, nil)
	detectSet := v2ReadSet(detect)
	if got := mapList(detectSet["documents"]); len(got) != 0 {
		t.Fatalf("detect read documents = %v, want no fabricated reads", got)
	}
	negative := mapList(detectSet["negativeObservations"])
	if len(negative) != 1 || negative[0]["path"] != "go.mod" || !strings.Contains(negative[0]["detail"].(string), "snapshot-") {
		t.Fatalf("detect negative lookup = %v, want measured go.mod snapshot miss", negative)
	}
	if len(mapList(detectSet["membershipObservations"])) != 1 || len(mapList(detectSet["dependencyFrontiers"])) != 0 {
		t.Fatalf("detect observations do not match source fallback = %v", detectSet)
	}
	if v2Closure(detect)["closureStatus"] != "open" {
		t.Fatalf("detect falsely closed: %v", v2Closure(detect))
	}

	harvestFiles := map[string]string{
		"go.mod":      "module example.test\n",
		"cmd/main.go": "package main\nfunc Handle() {}\n",
		"README.md":   "not consulted",
	}
	harvest := v2TrackerResult(t, "harvest", "harvest_candidates", harvestFiles, []string{"negative_lookup"}, nil)
	harvestSet := v2ReadSet(harvest)
	readPaths := make([]string, 0)
	for _, doc := range mapList(harvestSet["documents"]) {
		readPaths = append(readPaths, doc["path"].(string))
	}
	if !reflect.DeepEqual(readPaths, []string{"cmd/main.go", "go.mod"}) {
		t.Fatalf("harvest read documents = %v, want only go.mod and enumerated Go source", readPaths)
	}
	if len(mapList(harvestSet["membershipObservations"])) != 1 || len(mapList(harvestSet["dependencyFrontiers"])) != 1 {
		t.Fatalf("harvest tracker observations = %v", harvestSet)
	}
	if v2Closure(harvest)["closureStatus"] != "closed" || !containsString(v2Closure(harvest)["measuredObservations"].([]string), "negative_lookup") {
		t.Fatalf("harvest with zero misses did not close with measured negative_lookup: %v", v2Closure(harvest))
	}
	harvestExtra := v2TrackerResult(t, "harvest-extra", "harvest_candidates", map[string]string{
		"go.mod":       harvestFiles["go.mod"],
		"cmd/main.go":  harvestFiles["cmd/main.go"],
		"cmd/extra.go": "package main\nfunc Extra() {}\n",
	}, nil, nil)
	membership := mapList(harvestSet["membershipObservations"])[0]["valueHash"]
	extraMembership := mapList(v2ReadSet(harvestExtra)["membershipObservations"])[0]["valueHash"]
	if membership == extraMembership {
		t.Fatalf("membership digest did not change after source enumeration changed")
	}

	slice := v2TrackerResult(t, "slice", "slice", map[string]string{
		"go.mod":      harvestFiles["go.mod"],
		"cmd/main.go": harvestFiles["cmd/main.go"],
		"README.md":   harvestFiles["README.md"],
	}, []string{"membership"}, map[string]any{
		"candidateId": "candidate-v2", "entrySymbolPath": "cmd/main.go#Handle",
	})
	sliceSet := v2ReadSet(slice)
	if got := mapList(sliceSet["documents"]); len(got) != 2 {
		t.Fatalf("slice read documents = %v, want go.mod and entry source", got)
	}
	if len(mapList(sliceSet["membershipObservations"])) != 1 || len(mapList(sliceSet["dependencyFrontiers"])) != 1 {
		t.Fatalf("slice observations do not match actual reads: %v", sliceSet)
	}
	if v2Closure(slice)["closureStatus"] != "closed" {
		t.Fatalf("slice did not close after entry membership measurement: %v", v2Closure(slice))
	}

	unsupported := v2TrackerResult(t, "unsupported", "harvest_candidates", harvestFiles, []string{"runtime_observation"}, nil)
	if v2Closure(unsupported)["closureStatus"] != "open" || !strings.Contains(v2Closure(unsupported)["incompleteReasons"].([]string)[0], "runtime_observation") {
		t.Fatalf("unsupported observation was not explicit/open: %v", v2Closure(unsupported))
	}
}

func TestV2DetectUsesGoSourceMembershipWhenGoModIsMissing(t *testing.T) {
	result := v2TrackerResult(t, "detect-go-source-only", "detect", map[string]string{
		"cmd/main.go": "package main\nfunc Handle() {}\n",
	}, []string{"membership", "negative_lookup"}, nil)

	payload := result["payload"].(map[string]any)
	if payload["matched"] != true || payload["confident"] != true {
		t.Fatalf("source-only Go snapshot detection = %v, want matched and confident", payload)
	}
	readSet := v2ReadSet(result)
	if got := mapList(readSet["documents"]); len(got) != 0 {
		t.Fatalf("source-only detect read documents = %v, want enumeration without source reads", got)
	}
	negative := mapList(readSet["negativeObservations"])
	if len(negative) != 1 || negative[0]["path"] != "go.mod" {
		t.Fatalf("source-only detect negative lookup = %v, want measured go.mod miss", negative)
	}
	membership := mapList(readSet["membershipObservations"])
	if len(membership) != 1 || membership[0]["path"] != "." {
		t.Fatalf("source-only detect membership = %v, want measured source enumeration", membership)
	}
	if v2Closure(result)["closureStatus"] != "closed" {
		t.Fatalf("source-only detect with measured requirements was not closed: %v", v2Closure(result))
	}
}

func TestV2DetectKeepsEmptyOrNonGoSnapshotFalseWithMeasuredObservations(t *testing.T) {
	for name, files := range map[string]map[string]string{
		"empty":  {},
		"non-go": {"README.md": "not a Go source file\n"},
	} {
		t.Run(name, func(t *testing.T) {
			result := v2TrackerResult(t, "detect-no-go-"+name, "detect", files, []string{"membership", "negative_lookup"}, nil)
			payload := result["payload"].(map[string]any)
			if payload["matched"] != false || payload["confident"] != false {
				t.Fatalf("%s snapshot detection = %v, want unmatched and not confident", name, payload)
			}
			readSet := v2ReadSet(result)
			if got := mapList(readSet["documents"]); len(got) != 0 {
				t.Fatalf("%s detect read documents = %v, want no fabricated reads", name, got)
			}
			negative := mapList(readSet["negativeObservations"])
			if len(negative) != 1 || negative[0]["path"] != "go.mod" {
				t.Fatalf("%s detect negative lookup = %v, want measured go.mod miss", name, negative)
			}
			membership := mapList(readSet["membershipObservations"])
			if len(membership) != 1 || membership[0]["path"] != "." {
				t.Fatalf("%s detect membership = %v, want measured empty source enumeration", name, membership)
			}
			if v2Closure(result)["closureStatus"] != "closed" {
				t.Fatalf("%s detect with measured requirements was not closed: %v", name, v2Closure(result))
			}
		})
	}
}

func TestV2DetectDoesNotClaimDependencyFrontierWithoutGoMod(t *testing.T) {
	result := v2TrackerResult(t, "detect-go-frontier-open", "detect", map[string]string{
		"main.go": "package main\nfunc Handle() {}\n",
	}, []string{"membership", "negative_lookup", "dependency_frontier"}, nil)

	closure := v2Closure(result)
	if closure["closureStatus"] != "open" {
		t.Fatalf("source-only detect falsely closed without a dependency frontier: %v", closure)
	}
	reasons := closure["incompleteReasons"].([]string)
	if len(reasons) != 1 || !strings.Contains(reasons[0], "dependency_frontier") {
		t.Fatalf("source-only detect closure reasons = %v, want dependency_frontier", reasons)
	}
	measured := closure["measuredObservations"].([]string)
	if containsString(measured, "dependency_frontier") {
		t.Fatalf("source-only detect fabricated dependency frontier: %v", measured)
	}
}

func TestV2SliceClosesWithZeroMissesOnCleanSnapshot(t *testing.T) {
	result := v2TrackerResult(t, "slice-clean-close", "slice", map[string]string{
		"go.mod":  "module example.com/clean\n\ngo 1.24\n",
		"main.go": "package main\n\nfunc Handle() {}\n",
	}, []string{"negative_lookup", "membership", "dependency_frontier"}, map[string]any{
		"candidateId": "clean#Handle", "entrySymbolPath": "main.go#Handle",
	})
	closure := v2Closure(result)
	if closure["closureStatus"] != "closed" {
		t.Fatalf("clean slice did not close: %v", closure)
	}
	measured := closure["measuredObservations"].([]string)
	if !containsString(measured, "negative_lookup") {
		t.Fatalf("clean slice left negative_lookup unmeasured: %v", measured)
	}
	if negatives := mapList(closure["negativeObservations"]); len(negatives) != 1 || negatives[0]["kind"] != "negative_lookup" || !strings.Contains(negatives[0]["detail"].(string), "zero-miss:") {
		t.Fatalf("clean slice must carry exactly one zero-miss marker: %v", negatives)
	}
}
