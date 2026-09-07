// Command codeflow-go-adapter is the native Go adapter for VS-01.
// It intentionally uses only the Go standard library at this boundary.
package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"codeflow/internal/secret"
)

const (
	jsonRPCVersion          = "2.0"
	protocolVersion         = 1
	adapterVersion          = "0.1.0"
	analyzerVersion         = "go-structural/0.1.0"
	maxMessageBytes         = int64(1 << 20)
	maxHeaderBytes          = 8 << 10
	analysisSchemaID        = "https://codeflow.local/schemas/adapter-analysis.schema.json"
	readSetSchemaID         = "https://codeflow.local/schemas/analysis-read-set.schema.json"
	closureSchemaID         = "https://codeflow.local/schemas/causal-observation-closure.schema.json"
	analyzerRequestSchemaID = "https://codeflow.local/schemas/rflsc.analyzer-request.v2.schema.json"
	analyzerResultSchemaID  = "https://codeflow.local/schemas/rflsc.analyzer-result.v2.schema.json"
	readSetV2SchemaID       = "https://codeflow.local/schemas/rflsc.analysis-read-set.v2.schema.json"
	closureV2SchemaID       = "https://codeflow.local/schemas/rflsc.observation-closure.v2.schema.json"
)

type request struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      string         `json:"id"`
	Method  string         `json:"method"`
	Params  map[string]any `json:"params"`
}

type server struct {
	mu       sync.Mutex
	out      *bufio.Writer
	cancelMu sync.Mutex
	cancel   map[string]context.CancelFunc
	active   chan struct{}
}

func main() {
	s := &server{out: bufio.NewWriter(os.Stdout), cancel: map[string]context.CancelFunc{}, active: make(chan struct{}, 64)}
	br := bufio.NewReaderSize(os.Stdin, 64<<10)
	for {
		body, err := readFrame(br, maxMessageBytes)
		if err != nil {
			if err != io.EOF {
				s.errorResponse("", "E_BAD_REQUEST", err.Error(), false)
			}
			return
		}
		var req request
		if err := json.Unmarshal(body, &req); err != nil {
			s.errorResponse("", "E_BAD_REQUEST", "request body is not valid JSON", false)
			continue
		}
		if req.Method == "$/cancelRequest" {
			s.cancelRequest(req.Params)
			continue
		}
		if req.JSONRPC != jsonRPCVersion || req.ID == "" || req.Method == "" {
			s.errorResponse(req.ID, "E_BAD_REQUEST", "invalid JSON-RPC request", false)
			continue
		}
		select {
		case s.active <- struct{}{}:
		default:
			s.errorResponse(req.ID, "E_BACKPRESSURE", "adapter in-flight bound exceeded", true)
			continue
		}
		ctx, cancel := context.WithCancel(context.Background())
		s.cancelMu.Lock()
		s.cancel[req.ID] = cancel
		s.cancelMu.Unlock()
		go func(req request, ctx context.Context) {
			defer func() {
				cancel()
				s.cancelMu.Lock()
				delete(s.cancel, req.ID)
				s.cancelMu.Unlock()
				<-s.active
			}()
			s.handle(ctx, req)
		}(req, ctx)
	}
}

func (s *server) handle(ctx context.Context, req request) {
	if req.Method == "initialize" || req.Method == "ping" {
		s.success(req.ID, map[string]any{
			"adapterVersion": adapterVersion, "protocolVersion": protocolVersion,
			"protocolVersions": []int{protocolVersion}, "analyzerVersion": analyzerVersion,
			"schemaId": analysisSchemaID, "schemaVersion": 1, "capabilities": capabilities(),
		})
		return
	}
	if req.Method == "shutdown" {
		s.success(req.ID, map[string]any{"acknowledged": true})
		time.Sleep(5 * time.Millisecond)
		os.Exit(0)
		return
	}
	if req.Method != "detect" && req.Method != "harvest_candidates" && req.Method != "slice" {
		s.errorResponse(req.ID, "E_BAD_REQUEST", fmt.Sprintf("unknown method %q", req.Method), false)
		return
	}
	if req.Method == "detect" || req.Method == "harvest_candidates" || req.Method == "slice" {
		if err := validateAnalyzerRequestV2(req.ID, req.Method, req.Params); err != nil {
			s.errorResponse(req.ID, "E_BAD_REQUEST", err.Error(), false)
			return
		}
	}
	if delay, ok := req.Params["delayMs"].(float64); ok && delay > 0 {
		select {
		case <-time.After(time.Duration(delay) * time.Millisecond):
		case <-ctx.Done():
			s.errorResponse(req.ID, "E_CANCELLED", "request cancelled", false)
			return
		}
	}
	if hanging, _ := req.Params["hang"].(bool); hanging {
		<-ctx.Done()
		s.errorResponse(req.ID, "E_CANCELLED", "request cancelled", false)
		return
	}
	if batchID, _ := req.Params["batchId"].(string); batchID != "" {
		s.notification("codeflow/batchAck", map[string]any{"batchId": batchID, "acknowledged": true})
	}
	analysisParams := flattenOperationParams(req.Params)
	result, err := analyze(req.Method, analysisParams)
	if err != nil {
		s.errorResponse(req.ID, errorCode(err), err.Error(), false)
		return
	}
	s.notification("$/progress", map[string]any{"id": req.ID, "stage": "complete"})
	if req.Method == "detect" || req.Method == "harvest_candidates" || req.Method == "slice" {
		result = analyzerResultV2(req.ID, req.Method, req.Params, result)
	}
	s.success(req.ID, result)
}

func validateAnalyzerRequestV2(id, operation string, params map[string]any) error {
	if stringFromMap(params, "schemaId") != analyzerRequestSchemaID || int64FromAny(params["schemaVersion"]) != 2 {
		return fmt.Errorf("analysis request must use rflsc.analyzer-request.v2")
	}
	if stringFromMap(params, "requestId") != id {
		return fmt.Errorf("analysis requestId must match JSON-RPC id")
	}
	if stringFromMap(params, "operation") != operation {
		return fmt.Errorf("analysis operation does not match JSON-RPC method")
	}
	snapshot, ok := params["snapshot"].(map[string]any)
	if !ok {
		return fmt.Errorf("analysis snapshot is required")
	}
	for _, field := range []string{"snapshotId", "workspaceEpoch", "computedBasisId", "rootTreeId", "dependencyFingerprint", "documents", "files", "repositoryPathWriteAudit"} {
		if _, exists := snapshot[field]; !exists {
			return fmt.Errorf("analysis snapshot is missing %s", field)
		}
	}
	return nil
}

func flattenOperationParams(params map[string]any) map[string]any {
	result := make(map[string]any, len(params))
	for key, value := range params {
		result[key] = value
	}
	if payload, ok := params["payload"].(map[string]any); ok {
		for key, value := range payload {
			result[key] = value
		}
	}
	return result
}

func analyzerResultV2(requestID, operation string, params, legacy map[string]any) map[string]any {
	snapshot, _ := params["snapshot"].(map[string]any)
	readSet, _ := legacy["analysisReadSet"].(map[string]any)
	closure, _ := legacy["causalObservationClosure"].(map[string]any)
	capability, _ := legacy["capabilityProfile"].(map[string]any)
	coverage, _ := closure["coverageBoundary"].(map[string]any)
	if coverage == nil {
		coverage, _ = capability["coverageBoundary"].(map[string]any)
	}
	if coverage == nil {
		coverage = map[string]any{"includedSourceRoots": []string{"."}, "measured": true}
	}
	readSetV2 := map[string]any{
		"schemaId": readSetV2SchemaID, "schemaVersion": 2,
		"readSetId": readSet["readSetId"], "computedBasisId": snapshot["computedBasisId"],
		"workspaceEpoch": snapshot["workspaceEpoch"], "documents": readSet["documents"],
		"negativeObservations":   readSet["negativeObservations"],
		"membershipObservations": readSet["membershipObservations"],
		"dependencyFrontiers":    readSet["dependencyFrontiers"],
	}
	closureV2 := map[string]any{
		"schemaId": closureV2SchemaID, "schemaVersion": 2,
		"closureId": closure["closureId"], "analysisReadSetId": readSetV2["readSetId"],
		"computedBasisId": snapshot["computedBasisId"], "workspaceEpoch": snapshot["workspaceEpoch"],
		"closureStatus":          closure["closureStatus"],
		"negativeObservations":   readSetV2["negativeObservations"],
		"membershipObservations": readSetV2["membershipObservations"],
		"dependencyFrontiers":    readSetV2["dependencyFrontiers"],
		"requiredObservations":   closure["requiredObservations"],
		"measuredObservations":   closure["measuredObservations"],
		"incompleteReasons":      closure["incompleteReasons"],
	}
	payload := make(map[string]any, len(legacy))
	for key, value := range legacy {
		switch key {
		case "schemaId", "schemaVersion", "operation", "computedBasisId", "workspaceEpoch", "analysisReadSet", "causalObservationClosure", "capabilityProfile", "analyzerVersion", "diagnostics", "snapshotId", "rootTreeId", "dependencyFingerprint", "configurationFingerprint":
		default:
			payload[key] = value
		}
	}
	features, _ := capability["features"].([]string)
	unsupported, _ := capability["unsupported"].([]string)
	if features == nil {
		features = []string{"snapshot_bytes"}
	}
	if unsupported == nil {
		unsupported = []string{}
	}
	return map[string]any{
		"schemaId": analyzerResultSchemaID, "schemaVersion": 2, "requestId": requestID,
		"operation": operation, "adapterVersion": adapterVersion, "analyzerRevision": analyzerVersion,
		"workspaceEpoch": snapshot["workspaceEpoch"], "computedBasisId": snapshot["computedBasisId"],
		"snapshotId": snapshot["snapshotId"], "snapshotTreeDigest": snapshot["rootTreeId"],
		"dependencyFingerprint": snapshot["dependencyFingerprint"], "analysisReadSet": readSetV2,
		"causalObservationClosure": closureV2,
		"capabilityProfile":        map[string]any{"adapter": "go", "adapterVersion": adapterVersion, "analyzerRevision": analyzerVersion, "features": features, "unsupported": unsupported},
		"coverage":                 coverage,
		"diagnostics":              []any{}, "payload": payload,
	}
}

func capabilities() map[string]any {
	return map[string]any{"cancellation": true, "progress": true, "batchAck": true, "snapshotOverlay": true, "analysisMetadata": true, "maxMessageBytes": maxMessageBytes, "maxInFlight": 64}
}

func analyze(operation string, params map[string]any) (map[string]any, error) {
	if _, ok := params["snapshot"].(map[string]any); ok {
		return analyzeV2(operation, params)
	}
	root, _ := params["repoRoot"].(string)
	root = filepath.Clean(root)
	overlay := overlayFromParams(params)
	if overlay == nil {
		return nil, fmt.Errorf("snapshot.files (immutable protocol content) is required")
	}
	meta := metadata(params, operation, root, overlay)
	switch operation {
	case "detect":
		matched := hasFile(root, "go.mod", overlay) || len(goFiles(root, overlay)) > 0
		result := map[string]any{"matched": matched, "language": "go", "confident": matched}
		merge(result, meta)
		return result, nil
	case "harvest_candidates":
		candidates := harvest(root, overlay)
		result := map[string]any{"candidates": candidates}
		merge(result, meta)
		return result, nil
	case "slice":
		candidateID, _ := params["candidateId"].(string)
		entry, _ := params["entrySymbolPath"].(string)
		if candidateID == "" || entry == "" {
			return nil, fmt.Errorf("params.candidateId and entrySymbolPath are required")
		}
		result, err := slice(root, overlay, candidateID, entry)
		if err != nil {
			return nil, err
		}
		merge(result, meta)
		return result, nil
	}
	return nil, fmt.Errorf("unsupported operation")
}

func overlayFromParams(params map[string]any) map[string]string {
	var source any = params["contentOverlay"]
	if source == nil {
		if snapshot, ok := params["snapshot"].(map[string]any); ok {
			source = snapshot["contentOverlay"]
			if source == nil {
				source = snapshot["files"]
			}
		}
	}
	raw, ok := source.(map[string]any)
	if !ok {
		return nil
	}
	out := map[string]string{}
	for key, value := range raw {
		rel := filepath.ToSlash(filepath.Clean(key))
		if rel == "." || filepath.IsAbs(rel) || strings.HasPrefix(rel, "../") || strings.Contains(rel, "/../") {
			continue
		}
		if content, ok := value.(string); ok {
			out[rel] = content
			continue
		}
		if object, ok := value.(map[string]any); ok {
			if content, ok := object["content"].(string); ok {
				out[rel] = content
			}
		}
	}
	return out
}

type document struct {
	Path, ContentHash string
	RevisionID        string
	ContentID         string
	DocumentVersion   int
	ByteLength        int
}

type trackedDocument struct {
	Content  string
	Identity documentIdentity
}

// analysisTracker records only source/configuration interactions performed by
// the current v2 operation. Snapshot files are available to the adapter, but
// availability is not evidence that the analyzer read them.
type analysisTracker struct {
	params     map[string]any
	operation  string
	root       string
	overlay    map[string]string
	identities map[string]documentIdentity
	documents  map[string]trackedDocument
	missing    map[string]map[string]any
	membership []map[string]any
	frontiers  []map[string]any
	coverage   map[string]bool
}

func newAnalysisTracker(params map[string]any, operation, root string, overlay map[string]string) *analysisTracker {
	return &analysisTracker{
		params: params, operation: operation, root: root, overlay: overlay,
		identities: snapshotDocumentIdentities(params), documents: map[string]trackedDocument{},
		missing: map[string]map[string]any{}, membership: []map[string]any{}, frontiers: []map[string]any{},
		coverage: map[string]bool{},
	}
}

func (t *analysisTracker) read(rel string) (string, bool) {
	rel = normalizeTrackerPath(rel)
	if rel == "" {
		return "", false
	}
	content, ok := t.overlay[rel]
	if !ok {
		t.recordMissing(rel)
		return "", false
	}
	t.documents[rel] = trackedDocument{Content: content, Identity: t.identities[rel]}
	t.coverage[trackerSourceRoot(rel)] = true
	delete(t.missing, rel)
	return content, true
}

func (t *analysisTracker) recordMissing(rel string) {
	rel = normalizeTrackerPath(rel)
	if rel == "" {
		return
	}
	if _, read := t.documents[rel]; read {
		return
	}
	if _, exists := t.missing[rel]; exists {
		return
	}
	snapshotID := "captured source scope"
	if snapshot, ok := t.params["snapshot"].(map[string]any); ok {
		if value, ok := snapshot["snapshotId"].(string); ok && value != "" {
			snapshotID = "immutable snapshot " + value
		}
	}
	t.missing[rel] = map[string]any{
		"kind": "negative_lookup", "path": rel,
		"valueHash": digest([]byte(rel + ":absent")),
		"detail":    fmt.Sprintf("%s absent from %s during %s", rel, snapshotID, t.operation), "measured": true,
	}
}

func (t *analysisTracker) enumerateGoFiles() []string {
	paths := make([]string, 0, len(t.overlay))
	for rel := range t.overlay {
		rel = normalizeTrackerPath(rel)
		if rel != "" && strings.HasSuffix(rel, ".go") {
			paths = append(paths, rel)
		}
	}
	sort.Strings(paths)
	t.membership = []map[string]any{{
		"kind": "source_membership", "path": ".",
		"valueHash": digest([]byte(strings.Join(paths, "\n"))),
		"detail":    "membership measured from the source enumeration used by the operation", "measured": true,
	}}
	t.coverage["."] = true
	return paths
}

func (t *analysisTracker) recordDependency(rel string) {
	rel = normalizeTrackerPath(rel)
	doc, ok := t.documents[rel]
	if !ok {
		return
	}
	t.frontiers = append(t.frontiers, map[string]any{
		"kind": "dependency_frontier", "path": rel,
		"valueHash": digest([]byte(doc.Content)),
		"detail":    "dependency/configuration frontier established by the analyzer", "measured": true,
	})
}

func (t *analysisTracker) metadata(params map[string]any) map[string]any {
	paths := make([]string, 0, len(t.documents))
	for path := range t.documents {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	docs := make([]map[string]any, 0, len(paths))
	for _, path := range paths {
		doc := t.documents[path]
		contentHash := digest([]byte(doc.Content))
		item := map[string]any{"path": path, "contentHash": contentHash, "byteLength": len([]byte(doc.Content))}
		if doc.Identity.RevisionID != "" {
			item["documentRevisionId"] = doc.Identity.RevisionID
		}
		if doc.Identity.ContentID != "" {
			item["contentId"] = doc.Identity.ContentID
		}
		if doc.Identity.DocumentVersion > 0 {
			item["documentVersion"] = doc.Identity.DocumentVersion
		}
		docs = append(docs, item)
	}
	basis, _ := params["computedBasisId"].(string)
	if basis == "" {
		if snapshot, ok := params["snapshot"].(map[string]any); ok {
			basis, _ = snapshot["computedBasisId"].(string)
		}
	}
	epoch := int64Param(params, "workspaceEpoch")
	if epoch == 0 {
		if snapshot, ok := params["snapshot"].(map[string]any); ok {
			epoch = int64FromAny(snapshot["workspaceEpoch"])
		}
	}
	readSetID := "readset-" + digest([]byte(fmt.Sprintf("%s:%d:%s", basis, epoch, t.operation)))[:24]
	closureID := "closure-" + digest([]byte(readSetID + ":" + t.operation))[:24]
	negative := make([]map[string]any, 0, len(t.missing))
	for _, item := range t.missing {
		negative = append(negative, item)
	}
	sort.Slice(negative, func(i, j int) bool { return negative[i]["path"].(string) < negative[j]["path"].(string) })
	measured := []string{}
	if len(negative) > 0 {
		measured = append(measured, "negative_lookup")
	}
	if len(t.membership) > 0 {
		measured = append(measured, "membership")
	}
	if len(t.frontiers) > 0 {
		measured = append(measured, "dependency_frontier")
	}
	required := stringParams(params, "requiredObservations")
	if required == nil {
		required = []string{}
	}
	unsupported := []string{"runtime_observation", "dynamic_resolution"}
	incomplete := []string{}
	for _, want := range required {
		if !containsString(measured, want) {
			if containsString(unsupported, want) {
				incomplete = append(incomplete, want+" is unsupported and was not measured")
			} else {
				incomplete = append(incomplete, want+" is not measured for "+t.operation)
			}
		}
	}
	capability := map[string]any{
		"adapter": "go", "adapterVersion": adapterVersion, "analyzerRevision": analyzerVersion,
		"features":    []string{"symbols", "calls", "snapshot_overlay", "negative_lookup", "membership", "dependency_frontier"},
		"unsupported": unsupported, "protocolVersions": []int{protocolVersion},
		"coverageBoundary": map[string]any{"includedSourceRoots": []string{"."}, "excludedReasons": []any{}, "measured": true},
	}
	readSet := map[string]any{
		"schemaId": readSetSchemaID, "schemaVersion": 1, "readSetId": readSetID,
		"computedBasisId": basis, "workspaceEpoch": epoch, "documents": docs,
		"indexes": []any{}, "negativeObservations": negative, "membershipObservations": t.membership,
		"dependencyFrontiers": t.frontiers, "requiredObservations": required,
		"adapterVersions": map[string]string{"go": adapterVersion},
	}
	includedRoots := make([]string, 0, len(t.coverage))
	for root := range t.coverage {
		includedRoots = append(includedRoots, root)
	}
	if len(includedRoots) == 0 {
		includedRoots = append(includedRoots, ".")
	}
	sort.Strings(includedRoots)
	coverageBoundary := map[string]any{"includedSourceRoots": includedRoots, "excludedReasons": []any{}, "measured": true}
	capability["coverageBoundary"] = coverageBoundary
	closure := map[string]any{
		"schemaId": closureSchemaID, "schemaVersion": 1, "closureId": closureID,
		"analysisReadSetId": readSetID, "computedBasisId": basis, "workspaceEpoch": epoch,
		"closureStatus": func() string {
			if len(incomplete) > 0 {
				return "open"
			}
			return "closed"
		}(),
		"negativeObservations": negative, "membershipObservations": t.membership,
		"dependencyFrontiers": t.frontiers, "requiredObservations": required,
		"measuredObservations": measured, "capabilityProfile": capability,
		"coverageBoundary": capability["coverageBoundary"], "incompleteReasons": incomplete,
	}
	return map[string]any{
		"schemaId": analysisSchemaID, "schemaVersion": 1, "operation": t.operation,
		"computedBasisId": basis, "workspaceEpoch": epoch, "analysisReadSet": readSet,
		"causalObservationClosure": closure, "capabilityProfile": capability,
		"analyzerVersion": analyzerVersion, "diagnostics": []any{},
	}
}

func trackerSourceRoot(rel string) string {
	if index := strings.IndexByte(rel, '/'); index > 0 {
		return rel[:index]
	}
	return "."
}

func normalizeTrackerPath(rel string) string {
	rel = filepath.ToSlash(filepath.Clean(rel))
	if rel == "." || filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, "../") || strings.Contains(rel, "/../") {
		return ""
	}
	return rel
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func analyzeV2(operation string, params map[string]any) (map[string]any, error) {
	root, _ := params["repoRoot"].(string)
	root = filepath.Clean(root)
	overlay := overlayFromParams(params)
	if overlay == nil {
		return nil, fmt.Errorf("snapshot.files (immutable protocol content) is required")
	}
	tracker := newAnalysisTracker(params, operation, root, overlay)
	var result map[string]any
	switch operation {
	case "detect":
		_, ok := tracker.read("go.mod")
		if ok {
			tracker.recordDependency("go.mod")
		} else {
			// A Go source-only snapshot is still a Go project. The source
			// enumeration is the actual observation needed for that fallback,
			// while the files remain unread unless a later operation parses them.
			ok = len(tracker.enumerateGoFiles()) > 0
		}
		result = map[string]any{"matched": ok, "language": "go", "confident": ok}
	case "harvest_candidates":
		if _, ok := tracker.read("go.mod"); ok {
			tracker.recordDependency("go.mod")
		}
		candidates := []map[string]any{}
		for _, rel := range tracker.enumerateGoFiles() {
			content, ok := tracker.read(rel)
			if !ok {
				continue
			}
			fileSet := token.NewFileSet()
			file, err := parser.ParseFile(fileSet, rel, content, 0)
			if err != nil {
				continue
			}
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Recv != nil {
					continue
				}
				trigger, marker := markerFor(fn.Name.Name)
				if trigger != "" {
					candidates = append(candidates, candidate(rel+"#"+fn.Name.Name, fn.Name.Name, trigger, marker))
				}
			}
		}
		sort.Slice(candidates, func(i, j int) bool {
			return candidates[i]["entrySymbolPath"].(string) < candidates[j]["entrySymbolPath"].(string)
		})
		result = map[string]any{"candidates": candidates}
	case "slice":
		entry, _ := params["entrySymbolPath"].(string)
		candidateID, _ := params["candidateId"].(string)
		if _, ok := tracker.read("go.mod"); ok {
			tracker.recordDependency("go.mod")
		}
		parts := strings.SplitN(entry, "#", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid entrySymbolPath")
		}
		content, ok := tracker.read(parts[0])
		if !ok {
			return nil, fmt.Errorf("entry source file not found: %s", parts[0])
		}
		hash := digest([]byte(content))
		symbol := parts[1]
		result = map[string]any{"candidateId": candidateID, "language": "go", "entrySymbolPath": entry,
			"steps": []any{map[string]any{"ordinal": 1, "kind": "call", "description": symbol, "symbolPath": symbol,
				"anchor": map[string]any{"repoRelativePath": parts[0], "byteRange": []int{0, len([]byte(content))}, "fileHash": hash, "spanHash": hash, "enclosingSymbolPath": symbol, "canonicalAstFingerprint": hash}}},
			"edges": []any{}, "truncated": false, "visitedCycleDetected": false, "redactedCount": 0}
	default:
		return nil, fmt.Errorf("unsupported operation")
	}
	merge(result, tracker.metadata(params))
	return result, nil
}

func metadata(params map[string]any, operation, root string, overlay map[string]string) map[string]any {
	docs := []document{}
	identities := snapshotDocumentIdentities(params)
	if overlay != nil {
		keys := make([]string, 0, len(overlay))
		for key := range overlay {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			content := overlay[key]
			identity := identities[key]
			docs = append(docs, document{Path: key, ContentHash: digest([]byte(content)), RevisionID: identity.RevisionID, ContentID: identity.ContentID, DocumentVersion: identity.DocumentVersion, ByteLength: len([]byte(content))})
			if len(docs) >= 4096 {
				break
			}
		}
	}
	basis, _ := params["computedBasisId"].(string)
	if basis == "" {
		if snap, ok := params["snapshot"].(map[string]any); ok {
			basis, _ = snap["computedBasisId"].(string)
		}
	}
	if basis == "" {
		var b strings.Builder
		for _, doc := range docs {
			fmt.Fprintf(&b, "%s:%s\n", doc.Path, doc.ContentHash)
		}
		basis = digest([]byte(b.String()))
	}
	epoch := int64Param(params, "workspaceEpoch")
	if epoch == 0 {
		if snap, ok := params["snapshot"].(map[string]any); ok {
			epoch = int64FromAny(snap["workspaceEpoch"])
		}
	}
	readSetID := "readset-" + digest([]byte(fmt.Sprintf("%s:%d:%s", basis, epoch, operation)))[:24]
	closureID := "closure-" + digest([]byte(readSetID + ":" + operation))[:24]
	docObjects := make([]map[string]any, 0, len(docs))
	for _, doc := range docs {
		object := map[string]any{"path": doc.Path, "contentHash": doc.ContentHash, "byteLength": doc.ByteLength}
		if doc.RevisionID != "" {
			object["documentRevisionId"] = doc.RevisionID
		}
		if doc.ContentID != "" {
			object["contentId"] = doc.ContentID
		}
		if doc.DocumentVersion > 0 {
			object["documentVersion"] = doc.DocumentVersion
		}
		docObjects = append(docObjects, object)
	}
	required := stringParams(params, "requiredObservations")
	if required == nil {
		required = []string{}
	}
	membership := []any{map[string]any{"kind": "source_membership", "path": ".", "valueHash": digest([]byte(strings.Join(documentPaths(docs), "\n"))), "measured": true}}
	dependencyPath := operation
	if _, ok := overlay["go.mod"]; ok {
		dependencyPath = "go.mod"
	}
	frontier := []any{map[string]any{"kind": "dependency_frontier", "path": dependencyPath, "valueHash": digest([]byte(dependencyPath + ":" + basis)), "measured": true}}
	negative := []any{}
	for _, candidate := range []string{"go.mod", "go.work"} {
		if _, exists := overlay[candidate]; exists {
			continue
		}
		negative = append(negative, map[string]any{
			"kind": "negative_lookup", "path": candidate,
			"valueHash": digest([]byte(candidate + ":absent")),
			"detail":    "absence measured in complete snapshot content", "measured": true,
		})
		break
	}
	status, incomplete, measured := closureState(required, negative, membership, frontier)
	profile := map[string]any{
		"adapter": "go", "adapterVersion": adapterVersion, "analyzerRevision": analyzerVersion,
		"features":    []string{"symbols", "calls", "snapshot_overlay", "negative_lookup", "membership", "dependency_frontier"},
		"unsupported": []string{"runtime_observation", "dynamic_resolution"}, "protocolVersions": []int{protocolVersion},
		"coverageBoundary": map[string]any{"includedSourceRoots": []string{"."}, "excludedReasons": []any{}, "measured": true},
	}
	readSet := map[string]any{"schemaId": readSetSchemaID, "schemaVersion": 1, "readSetId": readSetID, "computedBasisId": basis, "workspaceEpoch": epoch, "documents": docObjects, "indexes": []any{}, "negativeObservations": negative, "membershipObservations": membership, "dependencyFrontiers": frontier, "requiredObservations": required, "adapterVersions": map[string]string{"go": adapterVersion}}
	closure := map[string]any{"schemaId": closureSchemaID, "schemaVersion": 1, "closureId": closureID, "analysisReadSetId": readSetID, "computedBasisId": basis, "workspaceEpoch": epoch, "closureStatus": status, "negativeObservations": negative, "membershipObservations": membership, "dependencyFrontiers": frontier, "requiredObservations": required, "measuredObservations": measured, "capabilityProfile": profile, "coverageBoundary": profile["coverageBoundary"], "incompleteReasons": incomplete}
	result := map[string]any{"schemaId": analysisSchemaID, "schemaVersion": 1, "operation": operation, "computedBasisId": basis, "workspaceEpoch": epoch, "analysisReadSet": readSet, "causalObservationClosure": closure, "capabilityProfile": profile, "analyzerVersion": analyzerVersion, "diagnostics": []any{}}
	if snapshot, ok := params["snapshot"].(map[string]any); ok {
		for _, key := range []string{"snapshotId", "rootTreeId", "dependencyFingerprint", "configurationFingerprint"} {
			if value, exists := snapshot[key]; exists {
				result[key] = value
			}
		}
	}
	return result
}

type documentIdentity struct {
	RevisionID      string
	ContentID       string
	DocumentVersion int
}

func snapshotDocumentIdentities(params map[string]any) map[string]documentIdentity {
	out := map[string]documentIdentity{}
	snapshot, ok := params["snapshot"].(map[string]any)
	if !ok {
		return out
	}
	documents, ok := snapshot["documents"].([]any)
	if !ok {
		return out
	}
	for _, raw := range documents {
		object, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		path, _ := object["path"].(string)
		if path == "" {
			continue
		}
		identity := documentIdentity{}
		identity.RevisionID, _ = object["documentRevisionId"].(string)
		identity.ContentID, _ = object["contentId"].(string)
		identity.DocumentVersion = int(int64FromAny(object["documentVersion"]))
		out[path] = identity
	}
	return out
}

func stringParams(params map[string]any, key string) []string {
	raw, ok := params[key].([]any)
	if !ok {
		if typed, typedOK := params[key].([]string); typedOK {
			return append([]string(nil), typed...)
		}
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, value := range raw {
		if text, ok := value.(string); ok && text != "" {
			out = append(out, text)
		}
	}
	return out
}

func stringFromMap(params map[string]any, key string) string {
	value, _ := params[key].(string)
	return value
}

func documentPaths(documents []document) []string {
	out := make([]string, 0, len(documents))
	for _, doc := range documents {
		out = append(out, doc.Path)
	}
	sort.Strings(out)
	return out
}

func closureState(required []string, negative, membership, frontier []any) (string, []string, []string) {
	measured := []string{}
	if len(membership) > 0 {
		measured = append(measured, "membership")
	}
	if len(frontier) > 0 {
		measured = append(measured, "dependency_frontier")
	}
	if len(negative) > 0 {
		measured = append(measured, "negative_lookup")
	}
	incomplete := []string{}
	for _, want := range required {
		found := false
		for _, got := range measured {
			if got == want {
				found = true
				break
			}
		}
		if !found {
			incomplete = append(incomplete, want+" is not measured")
		}
	}
	if len(incomplete) > 0 {
		return "open", incomplete, measured
	}
	return "closed", []string{}, measured
}

func int64Param(params map[string]any, key string) int64 { return int64FromAny(params[key]) }
func int64FromAny(value any) int64 {
	switch n := value.(type) {
	case float64:
		return int64(n)
	case int:
		return int64(n)
	case int64:
		return n
	}
	return 0
}
func merge(dst, src map[string]any) {
	for key, value := range src {
		dst[key] = value
	}
}
func digest(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }
func errorCode(err error) string {
	if strings.Contains(err.Error(), "required") {
		return "E_BAD_REQUEST"
	}
	return "E_ADAPTER_INTERNAL"
}
func hasFile(root, rel string, overlay map[string]string) bool {
	if overlay != nil {
		_, ok := overlay[filepath.ToSlash(rel)]
		return ok
	}
	return false
}

func goFiles(root string, overlay map[string]string) []string {
	if overlay != nil {
		out := []string{}
		for rel := range overlay {
			if strings.HasSuffix(rel, ".go") {
				out = append(out, rel)
			}
		}
		sort.Strings(out)
		return out
	}
	return nil
}

func harvest(root string, overlay map[string]string) []map[string]any {
	result := []map[string]any{}
	for _, rel := range goFiles(root, overlay) {
		content, ok := readSource(root, overlay, rel)
		if !ok {
			continue
		}
		fileSet := token.NewFileSet()
		file, err := parser.ParseFile(fileSet, rel, content, 0)
		if err != nil {
			continue
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil {
				continue
			}
			trigger, marker := markerFor(fn.Name.Name)
			if trigger == "" {
				continue
			}
			entry := rel + "#" + fn.Name.Name
			result = append(result, candidate(entry, fn.Name.Name, trigger, marker))
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i]["entrySymbolPath"].(string) < result[j]["entrySymbolPath"].(string)
	})
	return result
}

func markerFor(name string) (string, string) {
	switch {
	case strings.HasPrefix(name, "Handle"):
		return "user_action", "route_callback"
	case name == "Run" || name == "Execute":
		return "use_case_invocation", "usecase_call"
	case strings.HasPrefix(name, "Serve") || strings.HasPrefix(name, "Consume"):
		return "system_event", "lifecycle_callback"
	case strings.HasPrefix(name, "Set") || strings.HasPrefix(name, "Update"):
		return "state_transition", "state_mutation"
	}
	return "", ""
}

func candidate(entry, name, trigger, marker string) map[string]any {
	return map[string]any{"candidateId": "cand-" + digest([]byte(entry))[:16], "triggerClass": trigger, "markerKind": marker, "entrySymbolPath": entry, "intentSignals": map[string]any{"className": name, "derivedName": name, "docLine": nil, "packageName": "go"}, "score": 0.5, "fanIn": 1, "boundaryReachable": true, "rootEquivalenceKey": name, "tieBreakRank": 0, "manifestOverride": "none", "dedupedInto": nil}
}

func slice(root string, overlay map[string]string, candidateID, entry string) (map[string]any, error) {
	parts := strings.SplitN(entry, "#", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid entrySymbolPath")
	}
	content, ok := readSource(root, overlay, parts[0])
	if !ok {
		return nil, fmt.Errorf("entry source file not found: %s", parts[0])
	}
	hash := digest([]byte(content))
	symbol := parts[1]
	steps := []any{map[string]any{"ordinal": 1, "kind": "call", "description": symbol, "symbolPath": symbol, "anchor": map[string]any{"repoRelativePath": parts[0], "byteRange": []int{0, len([]byte(content))}, "fileHash": hash, "spanHash": hash, "enclosingSymbolPath": symbol, "canonicalAstFingerprint": hash}}}
	return map[string]any{"candidateId": candidateID, "language": "go", "entrySymbolPath": entry, "steps": steps, "edges": []any{}, "truncated": false, "visitedCycleDetected": false, "redactedCount": 0}, nil
}

func readSource(root string, overlay map[string]string, rel string) (string, bool) {
	rel = filepath.ToSlash(filepath.Clean(rel))
	if overlay != nil {
		value, ok := overlay[rel]
		return value, ok
	}
	return "", false
}

func (s *server) cancelRequest(params map[string]any) {
	id, _ := params["id"].(string)
	s.cancelMu.Lock()
	cancel := s.cancel[id]
	s.cancelMu.Unlock()
	if cancel != nil {
		cancel()
	}
}
func (s *server) success(id string, result map[string]any) {
	s.write(map[string]any{"jsonrpc": jsonRPCVersion, "id": id, "result": result})
}
func (s *server) errorResponse(id, code, message string, retryable bool) {
	safe := redactDiagnostic(message)
	s.write(map[string]any{"jsonrpc": jsonRPCVersion, "id": id, "error": map[string]any{"code": -32000, "message": safe, "data": map[string]any{"code": code, "retryable": retryable, "detail": safe}}})
}

func redactDiagnostic(message string) string {
	clean, _, err := secret.RedactJSON([]byte(message))
	if err != nil {
		clean = []byte(secret.Redact(message).Text)
	}
	safe := string(clean)
	if len(safe) > 512 {
		return safe[:512]
	}
	return safe
}
func (s *server) notification(method string, params map[string]any) {
	s.write(map[string]any{"jsonrpc": jsonRPCVersion, "method": method, "params": params})
}
func (s *server) write(value map[string]any) {
	body := boundedResponseBody(value, maxMessageBytes)
	if body == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, _ = fmt.Fprintf(s.out, "Content-Length: %d\r\n\r\n", len(body))
	_, _ = s.out.Write(body)
	_ = s.out.Flush()
}

// boundedResponseBody serializes one response and enforces the common body
// bound before it reaches the framed writer. An oversized response is
// replaced by one small typed error. The fallback is built directly rather
// than recursively passing through write, so a bound failure cannot produce
// an unbounded error loop.
func boundedResponseBody(value map[string]any, max int64) []byte {
	if max <= 0 {
		max = maxMessageBytes
	}
	body, err := json.Marshal(value)
	if err == nil && int64(len(body)) <= max {
		return body
	}
	id, _ := value["id"].(string)
	fallback := map[string]any{
		"jsonrpc": jsonRPCVersion,
		"id":      id,
		"error": map[string]any{
			"code":    -32000,
			"message": "adapter response exceeds maxMessageBytes",
			"data": map[string]any{
				"code":      "E_ADAPTER_INTERNAL",
				"retryable": false,
			},
		},
	}
	body, err = json.Marshal(fallback)
	if err == nil && int64(len(body)) <= max {
		return body
	}
	// An untrusted id can consume the remaining bound. Drop it and retry the
	// fixed-size typed error before declaring the negotiated bound too small.
	fallback["id"] = ""
	body, err = json.Marshal(fallback)
	if err != nil || int64(len(body)) > max {
		return nil
	}
	return body
}

func readFrame(br *bufio.Reader, max int64) ([]byte, error) {
	length := int64(-1)
	headerSize := 0
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return nil, err
		}
		headerSize += len(line)
		if headerSize > maxHeaderBytes {
			return nil, fmt.Errorf("frame header exceeds bound")
		}
		line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		if line == "" {
			break
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok || !strings.EqualFold(strings.TrimSpace(key), "Content-Length") {
			continue
		}
		n, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		if err != nil || n < 0 {
			return nil, fmt.Errorf("invalid Content-Length")
		}
		length = n
	}
	if length < 0 {
		return nil, fmt.Errorf("missing Content-Length")
	}
	if length > max {
		return nil, fmt.Errorf("frame exceeds maxMessageBytes")
	}
	body := make([]byte, length)
	_, err := io.ReadFull(br, body)
	return body, err
}
