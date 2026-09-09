// Command mockadapter is a deterministic CodeFlow adapter used by the Go
// protocol conformance suite. It speaks JSON-RPC 2.0 over Content-Length
// framed stdio and preserves the suite's delay, hang, flood, and crash faults.
package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	jsonRPCVersion  = "2.0"
	protocolVersion = 1
	adapterVersion  = "mock-1.0"
	analyzerVersion = "mock-analyzer/1"
	maxMessageBytes = int64(128 << 20)
)

type crashState struct {
	Count int  `json:"count"`
	Fired bool `json:"fired"`
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      string          `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type server struct {
	out      *bufio.Writer
	mu       sync.Mutex
	cancelMu sync.Mutex
	cancel   map[string]context.CancelFunc

	delayMs       int
	hangAll       bool
	hangOps       map[string]bool
	floodBytes    int
	crashN        int
	statePath     string
	writeRelative bool
	cwdLog        string
	badTreeDigest bool
	badSnapshotID bool
	perProcCount  int
}

func main() {
	if envBool("MOCK_CHILD_MODE") {
		for {
			time.Sleep(time.Hour)
		}
	}
	if envBool("MOCK_EXIT_ON_STARTUP") {
		os.Exit(1)
	}
	if pidLog := os.Getenv("MOCK_PID_LOG"); pidLog != "" {
		if f, err := os.OpenFile(pidLog, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600); err == nil {
			_, _ = fmt.Fprintf(f, "%d\n", os.Getpid())
			_ = f.Close()
		}
	}
	s := &server{
		out:           bufio.NewWriter(os.Stdout),
		cancel:        make(map[string]context.CancelFunc),
		delayMs:       envInt("MOCK_DELAY_MS", 0),
		hangAll:       envBool("MOCK_HANG"),
		hangOps:       map[string]bool{},
		floodBytes:    envInt("MOCK_FLOOD_BYTES", 0),
		crashN:        envInt("MOCK_CRASH_AFTER_N_REQUESTS", 0),
		statePath:     os.Getenv("MOCK_CRASH_STATE_FILE"),
		writeRelative: envBool("MOCK_WRITE_RELATIVE"),
		cwdLog:        os.Getenv("MOCK_CWD_LOG"),
		badTreeDigest: envBool("MOCK_BAD_SNAPSHOT_TREE_DIGEST"),
		badSnapshotID: envBool("MOCK_BAD_SNAPSHOT_ID"),
	}
	for _, op := range strings.Split(os.Getenv("MOCK_HANG_OPS"), ",") {
		if op = strings.TrimSpace(op); op != "" {
			s.hangOps[op] = true
		}
	}
	s.startChild()

	br := bufio.NewReaderSize(os.Stdin, 64<<10)
	for {
		body, err := readFrame(br, maxMessageBytes)
		if err != nil {
			if err != io.EOF {
				_ = s.replyError("", "E_BAD_REQUEST", err.Error())
			}
			return
		}
		if len(bytes.TrimSpace(body)) == 0 {
			continue
		}
		var req rpcRequest
		if err := json.Unmarshal(body, &req); err != nil {
			_ = s.replyError("", "E_BAD_REQUEST", "request body is not valid JSON")
			continue
		}
		if req.Method == "$/cancelRequest" {
			s.cancelRequest(req.Params)
			continue
		}
		if req.JSONRPC != jsonRPCVersion || req.ID == "" || req.Method == "" {
			_ = s.replyError(req.ID, "E_BAD_REQUEST", "invalid JSON-RPC request")
			continue
		}
		ctx, cancel := context.WithCancel(context.Background())
		s.cancelMu.Lock()
		s.cancel[req.ID] = cancel
		s.cancelMu.Unlock()
		go func(req rpcRequest, ctx context.Context) {
			defer func() {
				cancel()
				s.cancelMu.Lock()
				delete(s.cancel, req.ID)
				s.cancelMu.Unlock()
			}()
			if s.handle(ctx, req) {
				return
			}
		}(req, ctx)
	}
}

func (s *server) startChild() {
	if !envBool("MOCK_SPAWN_CHILD") {
		return
	}
	childEnv := append(os.Environ(), "MOCK_CHILD_MODE=1")
	child := exec.Command(os.Args[0])
	child.Env = childEnv
	child.Stdin = nil
	child.Stdout = io.Discard
	child.Stderr = io.Discard
	if err := child.Start(); err != nil {
		return
	}
	if pidLog := os.Getenv("MOCK_CHILD_PID_LOG"); pidLog != "" {
		if f, err := os.OpenFile(pidLog, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600); err == nil {
			_, _ = fmt.Fprintf(f, "%d\n", child.Process.Pid)
			_ = f.Close()
		}
	}
}

func (s *server) handle(ctx context.Context, req rpcRequest) bool {
	method := req.Method
	if method == "ping" {
		method = "initialize"
	}
	s.recordRelativeWrite(method)
	if method != "initialize" && method != "shutdown" && s.shouldCrash() {
		os.Exit(137)
	}
	if s.hangAll || s.hangOps[method] || s.hangOps[req.Method] {
		select {
		case <-ctx.Done():
			_ = s.replyError(req.ID, "E_CANCELLED", "request cancelled")
			return true
		}
	}
	if s.delayMs > 0 {
		select {
		case <-time.After(time.Duration(s.delayMs) * time.Millisecond):
		case <-ctx.Done():
			_ = s.replyError(req.ID, "E_CANCELLED", "request cancelled")
			return true
		}
	}
	if s.floodBytes > 0 && method != "initialize" && method != "shutdown" {
		return s.emitFlood(req.ID)
	}
	if batchID := stringParam(req.Params, "batchId"); batchID != "" {
		_ = s.replyNotification("codeflow/batchAck", map[string]any{"batchId": batchID, "acknowledged": true})
	}
	if method != "initialize" {
		_ = s.replyNotification("$/progress", map[string]any{"id": req.ID, "stage": "complete"})
	}

	switch method {
	case "initialize":
		version := protocolVersion
		if v := os.Getenv("MOCK_PROTOCOL_VERSION"); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				version = n
			}
		}
		return s.replyOK(req.ID, map[string]any{
			"adapterVersion":   adapterVersion,
			"protocolVersion":  version,
			"protocolVersions": []int{protocolVersion},
			"analyzerVersion":  analyzerVersion,
			"capabilities":     capabilities(),
		})
	case "detect":
		result := map[string]any{"language": "mock", "confident": true, "pid": os.Getpid()}
		return s.replyOK(req.ID, s.addMetadata(req.ID, "detect", req.Params, result))
	case "harvest_candidates":
		result := map[string]any{
			"candidates": []map[string]any{
				{"candidateId": "cand-mock0001", "triggerClass": "user_action", "markerKind": "route_callback", "entrySymbolPath": "mock.dart#MockFeatureA.doWork", "intentSignals": map[string]any{"className": "MockFeatureA", "derivedName": "Do work", "docLine": nil, "packageName": "mock"}, "score": 0.9, "fanIn": 1, "boundaryReachable": true, "rootEquivalenceKey": "doWork", "tieBreakRank": 0, "manifestOverride": "none", "dedupedInto": nil},
				{"candidateId": "cand-mock0002", "triggerClass": "use_case_invocation", "markerKind": "usecase_call", "entrySymbolPath": "mock.dart#MockFeatureB.handle", "intentSignals": map[string]any{"className": "MockFeatureB", "derivedName": "Handle", "docLine": nil, "packageName": "mock"}, "score": 0.7, "fanIn": 1, "boundaryReachable": true, "rootEquivalenceKey": "handle", "tieBreakRank": 0, "manifestOverride": "none", "dedupedInto": nil},
			},
		}
		return s.replyOK(req.ID, s.addMetadata(req.ID, "harvest_candidates", req.Params, result))
	case "slice":
		result := map[string]any{
			"candidateId":     stringParam(req.Params, "candidateId"),
			"entrySymbolPath": stringParam(req.Params, "entrySymbolPath"),
			"language":        "dart",
			"steps": []map[string]any{{
				"ordinal": 1, "kind": "call", "description": "mock slice payload",
				"symbolPath": "Mock.run",
				"anchor": map[string]any{
					"repoRelativePath": "mock.dart", "byteRange": []int{0, 1},
					"fileHash": strings.Repeat("0", 64), "spanHash": strings.Repeat("0", 64),
					"enclosingSymbolPath": "Mock.run", "canonicalAstFingerprint": strings.Repeat("0", 64),
				},
			}},
			"edges": []any{}, "truncated": false, "visitedCycleDetected": false, "redactedCount": 0,
		}
		return s.replyOK(req.ID, s.addMetadata(req.ID, "slice", req.Params, result))
	case "shutdown":
		ok := s.replyOK(req.ID, map[string]any{"acknowledged": true})
		if ok {
			time.Sleep(5 * time.Millisecond)
			os.Exit(0)
		}
		return false
	default:
		return s.replyError(req.ID, "E_BAD_REQUEST", fmt.Sprintf("unknown method %q", req.Method))
	}
}

func (s *server) recordRelativeWrite(operation string) {
	if !s.writeRelative {
		return
	}
	// This hook deliberately uses relative paths. The protocol test adapter
	// must never receive a repository root, so these writes prove that the
	// process cwd is disposable rather than mutating the source worktree.
	_ = os.WriteFile("adapter-relative-write.txt", []byte(operation), 0o600)
	if s.cwdLog == "" {
		return
	}
	cwd, err := os.Getwd()
	if err != nil {
		return
	}
	_, writeErr := os.Stat("adapter-relative-write.txt")
	f, err := os.OpenFile(s.cwdLog, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	_, _ = fmt.Fprintf(f, "%s\t%s\t%s\t%t\n", cwd, os.Getenv("PWD"), os.Getenv("OLDPWD"), writeErr == nil)
	_ = f.Close()
}

func capabilities() map[string]any {
	return map[string]any{
		"cancellation":     true,
		"progress":         true,
		"batchAck":         true,
		"snapshotOverlay":  true,
		"analysisMetadata": true,
		"maxMessageBytes":  maxMessageBytes,
		"maxInFlight":      64,
	}
}

func (s *server) addMetadata(requestID, operation string, raw json.RawMessage, payload map[string]any) map[string]any {
	snap, _ := objectParam(raw, "snapshot")
	basis := stringFromMap(snap, "computedBasisId")
	epoch := int64FromMap(snap, "workspaceEpoch")
	snapshotID := stringFromMap(snap, "snapshotId")
	rootTreeID := stringFromMap(snap, "rootTreeId")
	dependencyFingerprint := stringFromMap(snap, "dependencyFingerprint")
	if s.badTreeDigest {
		rootTreeID = strings.Repeat("f", 64)
	}
	if s.badSnapshotID {
		snapshotID = "snapshot-mock-mismatch"
	}
	readSetID := "readset-mock-" + digest([]byte(requestID + ":" + operation))[:24]
	closureID := "closure-mock-" + digest([]byte(readSetID))[:24]
	files := snapshotFiles(raw)
	identities := snapshotDocumentIdentities(raw)
	documents := make([]any, 0, len(files))
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		content := files[path]
		document := map[string]any{
			"path": path, "contentHash": digest([]byte(content)), "byteLength": len([]byte(content)),
		}
		if identity, ok := identities[path]; ok {
			document["documentRevisionId"] = identity["documentRevisionId"]
			document["contentId"] = identity["contentId"]
			document["documentVersion"] = identity["documentVersion"]
		}
		documents = append(documents, document)
	}
	readSet := map[string]any{
		"schemaId":               "https://codeflow.local/schemas/rflsc.analysis-read-set.v2.schema.json",
		"schemaVersion":          2,
		"readSetId":              readSetID,
		"computedBasisId":        basis,
		"workspaceEpoch":         epoch,
		"documents":              documents,
		"negativeObservations":   []any{},
		"membershipObservations": []any{},
		"dependencyFrontiers":    []any{},
	}
	required := stringParams(raw, "requiredObservations")
	if required == nil {
		required = []string{}
	}
	incomplete := make([]string, 0, len(required))
	for _, requirement := range required {
		incomplete = append(incomplete, requirement+" is not measured")
	}
	profile := map[string]any{
		"adapter": "mock", "adapterVersion": adapterVersion, "analyzerRevision": analyzerVersion,
		"features": []string{"snapshot_bytes"}, "unsupported": []string{"negative_lookup", "membership", "dependency_frontier", "runtime_observation", "dynamic_resolution"},
	}
	closure := map[string]any{
		"schemaId":               "https://codeflow.local/schemas/rflsc.observation-closure.v2.schema.json",
		"schemaVersion":          2,
		"closureId":              closureID,
		"analysisReadSetId":      readSetID,
		"computedBasisId":        basis,
		"workspaceEpoch":         epoch,
		"closureStatus":          "open",
		"negativeObservations":   []any{},
		"membershipObservations": []any{},
		"dependencyFrontiers":    []any{},
		"requiredObservations":   required,
		"measuredObservations":   []string{},
		"incompleteReasons":      incomplete,
	}
	return map[string]any{
		"schemaId":                 "https://codeflow.local/schemas/rflsc.analyzer-result.v2.schema.json",
		"schemaVersion":            2,
		"requestId":                requestID,
		"operation":                operation,
		"adapterVersion":           adapterVersion,
		"analyzerRevision":         analyzerVersion,
		"workspaceEpoch":           epoch,
		"computedBasisId":          basis,
		"snapshotId":               snapshotID,
		"snapshotTreeDigest":       rootTreeID,
		"dependencyFingerprint":    dependencyFingerprint,
		"analysisReadSet":          readSet,
		"causalObservationClosure": closure,
		"capabilityProfile":        profile,
		"coverage":                 map[string]any{"includedSourceRoots": []string{"."}, "measured": true},
		"diagnostics":              []any{},
		"payload":                  payload,
	}
}

func snapshotFiles(raw json.RawMessage) map[string]string {
	params := map[string]any{}
	if json.Unmarshal(raw, &params) != nil {
		return nil
	}
	var source any = params["files"]
	if source == nil {
		source = params["contentOverlay"]
	}
	if snapshot, ok := params["snapshot"].(map[string]any); ok {
		if source == nil {
			source = snapshot["files"]
		}
		if source == nil {
			source = snapshot["contentOverlay"]
		}
	}
	entries, ok := source.(map[string]any)
	if !ok {
		return nil
	}
	out := make(map[string]string, len(entries))
	for path, rawContent := range entries {
		if content, ok := rawContent.(string); ok {
			out[path] = content
			continue
		}
		if object, ok := rawContent.(map[string]any); ok {
			if content, ok := object["content"].(string); ok {
				out[path] = content
			}
		}
	}
	return out
}

func snapshotDocumentIdentities(raw json.RawMessage) map[string]map[string]any {
	params := map[string]any{}
	if json.Unmarshal(raw, &params) != nil {
		return nil
	}
	snapshot, _ := params["snapshot"].(map[string]any)
	items, _ := snapshot["documents"].([]any)
	out := make(map[string]map[string]any, len(items))
	for _, item := range items {
		document, ok := item.(map[string]any)
		if !ok {
			continue
		}
		path, _ := document["path"].(string)
		if path == "" {
			continue
		}
		out[path] = document
	}
	return out
}

func digest(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

func stringParam(raw json.RawMessage, key string) string {
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return ""
	}
	if value := stringFromMap(m, key); value != "" {
		return value
	}
	if payload, ok := m["payload"].(map[string]any); ok {
		return stringFromMap(payload, key)
	}
	return ""
}

func stringParams(raw json.RawMessage, key string) []string {
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return nil
	}
	value := m[key]
	if value == nil {
		if payload, ok := m["payload"].(map[string]any); ok {
			value = payload[key]
		}
	}
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	result := make([]string, 0, len(items))
	for _, item := range items {
		if text, ok := item.(string); ok && text != "" {
			result = append(result, text)
		}
	}
	return result
}

func int64Param(raw json.RawMessage, key string) int64 {
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return 0
	}
	return int64FromMap(m, key)
}

func objectParam(raw json.RawMessage, key string) (map[string]any, bool) {
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return nil, false
	}
	v, ok := m[key].(map[string]any)
	return v, ok
}

func stringFromMap(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return v
}

func int64FromMap(m map[string]any, key string) int64 {
	v, ok := m[key].(float64)
	if !ok {
		return 0
	}
	return int64(v)
}

func (s *server) cancelRequest(raw json.RawMessage) {
	id := stringParam(raw, "id")
	s.cancelMu.Lock()
	cancel := s.cancel[id]
	s.cancelMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (s *server) shouldCrash() bool {
	if s.crashN <= 0 {
		return false
	}
	if s.statePath == "" {
		s.perProcCount++
		return s.perProcCount >= s.crashN
	}
	st := loadCrashState(s.statePath)
	st.Count++
	fire := !st.Fired && st.Count >= s.crashN
	if fire {
		st.Fired = true
	}
	saveCrashState(s.statePath, st)
	return fire
}

func loadCrashState(path string) crashState {
	var st crashState
	b, err := os.ReadFile(path)
	if err == nil {
		_ = json.Unmarshal(b, &st)
	}
	return st
}

func saveCrashState(path string, st crashState) {
	if dir := filepath.Dir(path); dir != "." {
		_ = os.MkdirAll(dir, 0o755)
	}
	b, err := json.Marshal(st)
	if err != nil {
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return
	}
	_ = os.Rename(tmp, path)
}

func (s *server) emitFlood(id string) bool {
	pad := bytes.Repeat([]byte("x"), s.floodBytes)
	return s.replyRaw(id, map[string]any{"padding": string(pad)})
}

func (s *server) replyOK(id string, result map[string]any) bool {
	return s.replyRaw(id, result)
}

func (s *server) replyRaw(id string, result map[string]any) bool {
	b, err := json.Marshal(map[string]any{"jsonrpc": jsonRPCVersion, "id": id, "result": result})
	if err != nil {
		return false
	}
	return s.writeBody(b)
}

func (s *server) replyError(id, code, msg string) bool {
	b, err := json.Marshal(map[string]any{
		"jsonrpc": jsonRPCVersion, "id": id,
		"error": map[string]any{"code": -32000, "message": msg, "data": map[string]any{"code": code, "retryable": code == "E_TIMEOUT" || code == "E_BACKPRESSURE"}},
	})
	if err != nil {
		return false
	}
	return s.writeBody(b)
}

func (s *server) replyNotification(method string, params map[string]any) bool {
	b, err := json.Marshal(map[string]any{"jsonrpc": jsonRPCVersion, "method": method, "params": params})
	if err != nil {
		return false
	}
	return s.writeBody(b)
}

func (s *server) writeBody(body []byte) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(body))
	if _, err := s.out.WriteString(header); err != nil {
		return false
	}
	if _, err := s.out.Write(body); err != nil {
		return false
	}
	return s.out.Flush() == nil
}

func writeAll(w io.Writer, b []byte) error {
	for len(b) > 0 {
		n, err := w.Write(b)
		if n > 0 {
			b = b[n:]
		}
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}

func readFrame(br *bufio.Reader, max int64) ([]byte, error) {
	length := int64(-1)
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return nil, err
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
		return nil, fmt.Errorf("frame exceeds %d bytes", max)
	}
	body := make([]byte, length)
	_, err := io.ReadFull(br, body)
	return body, err
}

func envBool(k string) bool {
	v := os.Getenv(k)
	return v == "1" || strings.EqualFold(v, "true")
}

func envInt(k string, def int) int {
	if v := os.Getenv(k); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}
