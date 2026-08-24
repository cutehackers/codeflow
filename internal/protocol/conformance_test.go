package protocol

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// RunConformance is the reusable adapter-protocol conformance suite
// (ticket 05). It gates any adapter binary speaking the contract —
// the mock today, real Dart/Kotlin adapters later — including the fault
// hooks below, which are part of this conformance contract and must be
// implemented equivalently by future adapters:
//
//	MOCK_EXIT_ON_STARTUP, MOCK_DELAY_MS, MOCK_HANG, MOCK_HANG_OPS,
//	MOCK_FLOOD_BYTES, MOCK_PROTOCOL_VERSION,
//	MOCK_CRASH_AFTER_N_REQUESTS (+ MOCK_CRASH_STATE_FILE for the
//	fire-once cross-process counter that makes restart-once recovery
//	observable).
//
// Cases exercised:
//  1. handshake + version negotiation ok
//  2. detect / harvest_candidates / slice round-trips (echo contracts)
//  3. version mismatch → E_UNSUPPORTED_VERSION at spawn
//  4. timeout (hanging op) → retryable E_TIMEOUT
//  5. cancellation mid-call → prompt E_CANCELLED
//  6. crash mid-stream via Pool → auto-restart ONCE, request succeeds
//  7. double crash → E_CRASHED surfaced to caller
//  8. oversized inbound line → connection broken, E_CRASHED
//  9. startup crash → spawn error
//
// 10. concurrent calls correlate ids/results across 5 goroutines
// 11. in-flight overflow → E_BACKPRESSURE
func RunConformance(t *testing.T, binPath string) {
	t.Run("Handshake", func(t *testing.T) { confHandshake(t, binPath) })
	t.Run("RoundTrips", func(t *testing.T) { confRoundTrips(t, binPath) })
	t.Run("VersionMismatch", func(t *testing.T) { confVersionMismatch(t, binPath) })
	t.Run("Timeout", func(t *testing.T) { confTimeout(t, binPath) })
	t.Run("CancelMidCall", func(t *testing.T) { confCancel(t, binPath) })
	t.Run("CrashRestartOnce", func(t *testing.T) { confCrashRestartOnce(t, binPath) })
	t.Run("DoubleCrashSurfacesECrashed", func(t *testing.T) { confDoubleCrash(t, binPath) })
	t.Run("FloodBreaksConnection", func(t *testing.T) { confFlood(t, binPath) })
	t.Run("StartupCrash", func(t *testing.T) { confStartupCrash(t, binPath) })
	t.Run("ConcurrentCorrelation", func(t *testing.T) { confConcurrent(t, binPath) })
	t.Run("Backpressure", func(t *testing.T) { confBackpressure(t, binPath) })
}

// TestMockAdapterConformance builds the mock binary into a temp dir so
// the suite is self-contained, then runs the reusable suite against it.
func TestMockAdapterConformance(t *testing.T) {
	bin := buildMockAdapter(t)
	RunConformance(t, bin)
}

func buildMockAdapter(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "mockadapter")
	cmd := exec.Command("go", "build", "-o", bin, "./internal/mockadapter")
	cmd.Dir = repoRootDir(t)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build mockadapter: %v\n%s", err, out)
	}
	return bin
}

func repoRootDir(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for p := dir; ; p = filepath.Dir(p) {
		if _, err := os.Stat(filepath.Join(p, "go.mod")); err == nil {
			return p
		}
		if p == filepath.Dir(p) {
			t.Fatal("go.mod not found above package dir")
		}
	}
}

// faultEnv merges overrides onto the parent environment without relying
// on duplicate-key resolution order inside the child process.
func faultEnv(overrides map[string]string) []string {
	base := os.Environ()
	for k := range overrides {
		prefix := k + "="
		var kept []string
		for _, kv := range base {
			if !strings.HasPrefix(kv, prefix) {
				kept = append(kept, kv)
			}
		}
		base = kept
	}
	keys := make([]string, 0, len(overrides))
	for k := range overrides {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		base = append(base, k+"="+overrides[k])
	}
	return base
}

func spawnConn(t *testing.T, bin string, env map[string]string, mutate func(*Config)) *Conn {
	t.Helper()
	cfg := Config{BinPath: bin}
	if len(env) > 0 {
		cfg.Env = faultEnv(env)
	}
	if mutate != nil {
		mutate(&cfg)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	c, err := Spawn(ctx, cfg)
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	return c
}

func wantCode(t *testing.T, err error, code ErrorCode) *Error {
	t.Helper()
	if err == nil {
		t.Fatalf("want %s, got nil error", code)
	}
	var perr *Error
	if !errors.As(err, &perr) {
		t.Fatalf("want protocol error, got %T: %v", err, err)
	}
	if perr.Code != code {
		t.Fatalf("want %s, got %s (%v)", code, perr.Code, err)
	}
	return perr
}

type detResult struct {
	Language  string `json:"language"`
	Confident bool   `json:"confident"`
	PID       int    `json:"pid"`
}

type harvestResult struct {
	Candidates []struct {
		ID     string  `json:"id"`
		Symbol string  `json:"symbol"`
		Score  float64 `json:"score"`
	} `json:"candidates"`
}

type sliceResult struct {
	CandidateID     string `json:"candidateId"`
	EntrySymbolPath string `json:"entrySymbolPath"`
	RepoRoot        string `json:"repoRoot"`
	Content         string `json:"content"`
}
