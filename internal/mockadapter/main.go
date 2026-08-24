// Command mockadapter is a mock CodeFlow language adapter used by the
// protocol conformance suite and future integration tests. It speaks
// the adapter protocol (schemas/adapter-protocol.schema.json): one JSON
// request per stdin line, one JSON response per stdout line.
//
// Ops implemented:
//
//	ping               → {"adapterVersion":"mock-1.0","protocolVersion":1}
//	detect             → {"language":"mock","confident":true,"pid":<pid>}
//	harvest_candidates → small fixed candidate set
//	slice              → echoes candidateId/entrySymbolPath/repoRoot
//	shutdown           → ok response, then exit 0
//
// Fault injection via environment variables (conformance suite):
//
//	MOCK_EXIT_ON_STARTUP=1      die immediately on startup (spawn failure)
//	MOCK_DELAY_MS=n             sleep n ms before every response
//	MOCK_HANG=1                 never respond to anything (incl. ping)
//	MOCK_HANG_OPS=a,b           never respond to listed ops only
//	MOCK_FLOOD_BYTES=m          emit an oversized single line instead of
//	                            a proper response (framing violation)
//	MOCK_PROTOCOL_VERSION=n     lie about protocolVersion in ping result
//	                            (version-negotiation failure)
//	MOCK_CRASH_AFTER_N_REQUESTS=k  os.Exit(137) without responding when the
//	                            k-th counted request arrives. Counted
//	                            requests exclude ping/shutdown so a fresh
//	                            process replays its handshake safely.
//	MOCK_CRASH_STATE_FILE=path  makes the crash counter persist ACROSS
//	                            processes ({"count":n,"fired":bool}) and
//	                            fires exactly once ever — this is what
//	                            lets restart-once recovery actually
//	                            recover; without it the deterministic
//	                            per-process counter would reproduce the
//	                            same crash in every respawn.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const adapterVersion = "mock-1.0"

type crashState struct {
	Count int  `json:"count"`
	Fired bool `json:"fired"`
}

type server struct {
	out        *bufio.Writer
	delayMs    int
	hangAll    bool
	hangOps    map[string]bool
	floodBytes int
	crashN     int
	statePath  string

	perProcCount int // per-process counted requests
}

func main() {
	if envBool("MOCK_EXIT_ON_STARTUP") {
		os.Exit(1)
	}
	s := &server{
		out:        bufio.NewWriter(os.Stdout),
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

	// Keep the runtime alive while hung on a nil-channel receive;
	// otherwise Go's deadlock detector would kill the process and turn
	// E_TIMEOUT expectations into EOF/E_CRASHED failures.
	go func() {
		for {
			time.Sleep(time.Hour)
		}
	}()

	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 64<<10), 16<<20)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		if !s.handle(line) {
			break
		}
	}
	if err := sc.Err(); err != nil {
		os.Exit(1)
	}
}

// handle processes one request line; returns false to stop the loop.
func (s *server) handle(line []byte) bool {
	var req struct {
		V      int             `json:"v"`
		ID     string          `json:"id"`
		Op     string          `json:"op"`
		Params json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(line, &req); err != nil || req.ID == "" {
		return true // unparseable noise: drop silently
	}

	if req.Op == "cancel" {
		return true // advisory control hint outside the v1 schema: ignore
	}
	if req.V != 1 {
		s.replyError(req.ID, "E_BAD_REQUEST", fmt.Sprintf("unsupported protocol version %d", req.V))
		return s.flush()
	}

	switch req.Op {
	case "ping":
		// Handshake never counts toward crash counters.
	case "shutdown":
		s.replyOK(req.ID, map[string]any{})
		s.flush()
		os.Exit(0)
	default:
		if s.shouldCrash() {
			// Crash WITHOUT responding, per the fault contract.
			os.Exit(137)
		}
	}

	if s.hangAll || s.hangOps[req.Op] {
		select {} // never respond; keep-alive goroutine prevents deadlock panic
	}
	if s.delayMs > 0 {
		time.Sleep(time.Duration(s.delayMs) * time.Millisecond)
	}
	if s.floodBytes > 0 && req.Op != "ping" && req.Op != "shutdown" {
		s.emitFlood()
		return s.flush()
	}

	switch req.Op {
	case "ping":
		version := 1
		if v := os.Getenv("MOCK_PROTOCOL_VERSION"); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				version = n
			}
		}
		s.replyOK(req.ID, map[string]any{
			"adapterVersion":  adapterVersion,
			"protocolVersion": version,
		})
	case "detect":
		pid := os.Getpid()
		s.replyOK(req.ID, map[string]any{
			"language":  "mock",
			"confident": true,
			"pid":       pid,
		})
	case "harvest_candidates":
		var params struct {
			RepoRoot string `json:"repoRoot"`
		}
		_ = json.Unmarshal(req.Params, &params)
		s.replyOK(req.ID, map[string]any{
			"repoRoot": params.RepoRoot,
			"candidates": []map[string]any{
				{"id": "cand-mock-001", "symbol": "MockFeatureA.doWork", "score": 0.9},
				{"id": "cand-mock-002", "symbol": "MockFeatureB.handle", "score": 0.7},
			},
		})
	case "slice":
		var params struct {
			CandidateID     string `json:"candidateId"`
			EntrySymbolPath string `json:"entrySymbolPath"`
			RepoRoot        string `json:"repoRoot"`
		}
		_ = json.Unmarshal(req.Params, &params)
		s.replyOK(req.ID, map[string]any{
			"candidateId":     params.CandidateID,
			"entrySymbolPath": params.EntrySymbolPath,
			"repoRoot":        params.RepoRoot,
			"content":         "mock slice payload",
		})
	default:
		s.replyError(req.ID, "E_BAD_REQUEST", fmt.Sprintf("unknown op %q", req.Op))
	}
	return s.flush()
}

// shouldCrash maintains the counted-request budget. Ping/shutdown are
// excluded above. Global state mode fires exactly once ever; per-process
// mode fires deterministically in every process at count k.
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
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
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

func (s *server) emitFlood() {
	pad := bytes.Repeat([]byte("x"), s.floodBytes)
	fmt.Fprintf(s.out, `{"id":"flood","ok":true,"result":{"padding":"%s"}}`+"\n", pad)
}

func (s *server) replyOK(id string, result map[string]any) {
	b, _ := json.Marshal(map[string]any{"id": id, "ok": true, "result": result})
	s.out.Write(b)
	s.out.WriteByte('\n')
}

func (s *server) replyError(id, code, msg string) {
	b, _ := json.Marshal(map[string]any{
		"id": id, "ok": false,
		"err": map[string]any{"code": code, "message": msg, "retryable": false},
	})
	s.out.Write(b)
	s.out.WriteByte('\n')
}

func (s *server) flush() bool {
	return s.out.Flush() == nil
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
