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
)

const (
	jsonRPCVersion   = "2.0"
	protocolVersion  = 1
	adapterVersion   = "0.1.0"
	analyzerVersion  = "go-structural/0.1.0"
	maxMessageBytes  = int64(1 << 20)
	maxHeaderBytes   = 8 << 10
	analysisSchemaID = "https://codeflow.local/schemas/adapter-analysis.schema.json"
	readSetSchemaID  = "https://codeflow.local/schemas/analysis-read-set.schema.json"
	closureSchemaID  = "https://codeflow.local/schemas/causal-observation-closure.schema.json"
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
	result, err := analyze(req.Method, req.Params)
	if err != nil {
		s.errorResponse(req.ID, errorCode(err), err.Error(), false)
		return
	}
	s.notification("$/progress", map[string]any{"id": req.ID, "stage": "complete"})
	s.success(req.ID, result)
}

func capabilities() map[string]any {
	return map[string]any{"cancellation": true, "progress": true, "batchAck": true, "snapshotOverlay": true, "analysisMetadata": true, "maxMessageBytes": maxMessageBytes, "maxInFlight": 64}
}

func analyze(operation string, params map[string]any) (map[string]any, error) {
	root, _ := params["repoRoot"].(string)
	if operation != "detect" && strings.TrimSpace(root) == "" {
		return nil, fmt.Errorf("params.repoRoot (non-empty string) is required")
	}
	root = filepath.Clean(root)
	overlay := overlayFromParams(params)
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
	ByteLength        int
}

func metadata(params map[string]any, operation, root string, overlay map[string]string) map[string]any {
	docs := []document{}
	if overlay != nil {
		keys := make([]string, 0, len(overlay))
		for key := range overlay {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			content := overlay[key]
			docs = append(docs, document{key, digest([]byte(content)), len([]byte(content))})
			if len(docs) >= 4096 {
				break
			}
		}
	} else {
		paths := []string{"go.mod"}
		if operation == "slice" {
			if entry, ok := params["entrySymbolPath"].(string); ok {
				paths = append(paths, strings.Split(entry, "#")[0])
			}
		}
		if operation == "harvest_candidates" {
			paths = goFiles(root, nil)
		}
		seen := map[string]bool{}
		for _, rel := range paths {
			rel = filepath.ToSlash(rel)
			if seen[rel] {
				continue
			}
			seen[rel] = true
			data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
			if err == nil {
				docs = append(docs, document{rel, digest(data), len(data)})
			}
		}
		sort.Slice(docs, func(i, j int) bool { return docs[i].Path < docs[j].Path })
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
		docObjects = append(docObjects, map[string]any{"path": doc.Path, "contentHash": doc.ContentHash, "byteLength": doc.ByteLength})
	}
	profile := map[string]any{"adapter": "go", "features": []string{"symbols", "calls", "snapshot_overlay", "negative_lookup", "membership", "dependency_frontier"}, "protocolVersions": []int{protocolVersion}, "coverageBoundary": map[string]any{"includedSourceRoots": []string{"."}, "excludedReasons": []any{}}}
	readSet := map[string]any{"schemaId": readSetSchemaID, "schemaVersion": 1, "readSetId": readSetID, "computedBasisId": basis, "workspaceEpoch": epoch, "documents": docObjects, "indexes": []any{}, "negativeObservations": []any{}, "membershipObservations": []any{map[string]any{"kind": "source_membership", "path": "."}}, "dependencyFrontiers": []any{map[string]any{"kind": "dependency_frontier", "path": operation}}, "adapterVersions": map[string]string{"go": adapterVersion}}
	closure := map[string]any{"schemaId": closureSchemaID, "schemaVersion": 1, "closureId": closureID, "analysisReadSetId": readSetID, "computedBasisId": basis, "workspaceEpoch": epoch, "closureStatus": "closed", "negativeObservations": []any{}, "membershipObservations": readSet["membershipObservations"], "dependencyFrontiers": readSet["dependencyFrontiers"], "capabilityProfile": profile, "coverageBoundary": profile["coverageBoundary"], "incompleteReasons": []any{}}
	return map[string]any{"schemaId": analysisSchemaID, "schemaVersion": 1, "operation": operation, "computedBasisId": basis, "workspaceEpoch": epoch, "analysisReadSet": readSet, "causalObservationClosure": closure, "capabilityProfile": profile, "analyzerVersion": analyzerVersion, "diagnostics": []any{}}
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
	_, err := os.Stat(filepath.Join(root, rel))
	return err == nil
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
	var out []string
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			if entry != nil && entry.IsDir() && (entry.Name() == ".git" || entry.Name() == "vendor" || strings.HasPrefix(entry.Name(), ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(entry.Name(), ".go") && !strings.HasSuffix(entry.Name(), "_test.go") {
			rel, e := filepath.Rel(root, path)
			if e == nil {
				out = append(out, filepath.ToSlash(rel))
			}
		}
		return nil
	})
	sort.Strings(out)
	return out
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
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	return string(data), err == nil
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
	s.write(map[string]any{"jsonrpc": jsonRPCVersion, "id": id, "error": map[string]any{"code": -32000, "message": message, "data": map[string]any{"code": code, "retryable": retryable}}})
}
func (s *server) notification(method string, params map[string]any) {
	s.write(map[string]any{"jsonrpc": jsonRPCVersion, "method": method, "params": params})
}
func (s *server) write(value map[string]any) {
	body, err := json.Marshal(value)
	if err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, _ = fmt.Fprintf(s.out, "Content-Length: %d\r\n\r\n", len(body))
	_, _ = s.out.Write(body)
	_ = s.out.Flush()
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
