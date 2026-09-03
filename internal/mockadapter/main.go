// Command mockadapter is a deterministic CodeFlow adapter used by the Go
// protocol conformance suite. It speaks JSON-RPC 2.0 over Content-Length
// framed stdio and preserves the suite's delay, hang, flood, and crash faults.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
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
	maxMessageBytes = int64(1 << 20)
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

	delayMs      int
	hangAll      bool
	hangOps      map[string]bool
	floodBytes   int
	crashN       int
	statePath    string
	perProcCount int
}

func main() {
	if envBool("MOCK_EXIT_ON_STARTUP") {
		os.Exit(1)
	}
	s := &server{
		out:        bufio.NewWriter(os.Stdout),
		cancel:     make(map[string]context.CancelFunc),
		delayMs:    envInt("MOCK_DELAY_MS", 0),
		hangAll:    envBool("MOCK_HANG"),
		hangOps:    map[string]bool{},
		floodBytes: envInt("MOCK_FLOOD_BYTES", 0),
		crashN:     envInt("MOCK_CRASH_AFTER_N_REQUESTS", 0),
		statePath:  os.Getenv("MOCK_CRASH_STATE_FILE"),
	}
	for _, op := range strings.Split(os.Getenv("MOCK_HANG_OPS"), ",") {
		if op = strings.TrimSpace(op); op != "" {
			s.hangOps[op] = true
		}
	}

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

func (s *server) handle(ctx context.Context, req rpcRequest) bool {
	method := req.Method
	if method == "ping" {
		method = "initialize"
	}
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
		addMetadata(result, "detect", req.Params)
		return s.replyOK(req.ID, result)
	case "harvest_candidates":
		result := map[string]any{
			"repoRoot": stringParam(req.Params, "repoRoot"),
			"candidates": []map[string]any{
				{"id": "cand-mock-001", "symbol": "MockFeatureA.doWork", "candidateId": "cand-mock-001", "triggerClass": "user_action", "markerKind": "route_callback", "entrySymbolPath": "mock.dart#MockFeatureA.doWork", "intentSignals": map[string]any{"className": "MockFeatureA", "derivedName": "Do work", "docLine": nil, "packageName": "mock"}, "score": 0.9, "fanIn": 1, "boundaryReachable": true, "rootEquivalenceKey": "doWork", "tieBreakRank": 0, "manifestOverride": "none", "dedupedInto": nil},
				{"id": "cand-mock-002", "symbol": "MockFeatureB.handle", "candidateId": "cand-mock-002", "triggerClass": "use_case_invocation", "markerKind": "usecase_call", "entrySymbolPath": "mock.dart#MockFeatureB.handle", "intentSignals": map[string]any{"className": "MockFeatureB", "derivedName": "Handle", "docLine": nil, "packageName": "mock"}, "score": 0.7, "fanIn": 1, "boundaryReachable": true, "rootEquivalenceKey": "handle", "tieBreakRank": 0, "manifestOverride": "none", "dedupedInto": nil},
			},
		}
		addMetadata(result, "harvest_candidates", req.Params)
		return s.replyOK(req.ID, result)
	case "slice":
		result := map[string]any{
			"candidateId":     stringParam(req.Params, "candidateId"),
			"entrySymbolPath": stringParam(req.Params, "entrySymbolPath"),
			"repoRoot":        stringParam(req.Params, "repoRoot"),
			"content":         "mock slice payload",
		}
		addMetadata(result, "slice", req.Params)
		return s.replyOK(req.ID, result)
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

func addMetadata(result map[string]any, operation string, raw json.RawMessage) {
	basis := stringParam(raw, "computedBasisId")
	epoch := int64Param(raw, "workspaceEpoch")
	if snap, ok := objectParam(raw, "snapshot"); ok {
		if basis == "" {
			basis = stringFromMap(snap, "computedBasisId")
		}
		if epoch == 0 {
			epoch = int64FromMap(snap, "workspaceEpoch")
		}
	}
	if basis == "" {
		basis = "basis-mock"
	}
	readSetID := "readset-mock-" + basis
	closureID := "closure-mock-" + basis
	readSet := map[string]any{
		"schemaId":               "https://codeflow.local/schemas/analysis-read-set.schema.json",
		"schemaVersion":          1,
		"readSetId":              readSetID,
		"computedBasisId":        basis,
		"workspaceEpoch":         epoch,
		"documents":              []any{},
		"indexes":                []any{},
		"negativeObservations":   []any{},
		"membershipObservations": []any{},
		"dependencyFrontiers":    []any{},
		"adapterVersions":        map[string]string{"mock": adapterVersion},
	}
	closure := map[string]any{
		"schemaId":               "https://codeflow.local/schemas/causal-observation-closure.schema.json",
		"schemaVersion":          1,
		"closureId":              closureID,
		"analysisReadSetId":      readSetID,
		"computedBasisId":        basis,
		"workspaceEpoch":         epoch,
		"closureStatus":          "closed",
		"negativeObservations":   []any{},
		"membershipObservations": []any{},
		"dependencyFrontiers":    []any{},
		"capabilityProfile":      map[string]any{"adapter": "mock", "features": []string{"symbols", "calls", "snapshot_overlay"}, "protocolVersions": []int{protocolVersion}},
		"coverageBoundary":       map[string]any{"includedSourceRoots": []string{"."}, "excludedReasons": []any{}},
	}
	result["schemaId"] = "https://codeflow.local/schemas/adapter-analysis.schema.json"
	result["schemaVersion"] = 1
	result["operation"] = operation
	result["computedBasisId"] = basis
	result["workspaceEpoch"] = epoch
	result["analysisReadSet"] = readSet
	result["causalObservationClosure"] = closure
	result["capabilityProfile"] = closure["capabilityProfile"]
	result["analyzerVersion"] = analyzerVersion
	result["diagnostics"] = []any{}
}

func stringParam(raw json.RawMessage, key string) string {
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return ""
	}
	return stringFromMap(m, key)
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
