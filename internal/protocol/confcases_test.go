package protocol

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func confHandshake(t *testing.T, bin string) {
	c := spawnConn(t, bin, nil, nil)
	defer c.Close()
	vi, err := c.Ping(context.Background())
	if err != nil {
		t.Fatalf("ping: %v", err)
	}
	if vi.AdapterVersion == "" {
		t.Fatal("handshake returned empty adapterVersion")
	}
	if vi.ProtocolVersion != ProtocolVersion {
		t.Fatalf("handshake negotiated v%d, want v%d", vi.ProtocolVersion, ProtocolVersion)
	}
}

func confRoundTrips(t *testing.T, bin string) {
	c := spawnConn(t, bin, nil, nil)
	defer c.Close()

	var det detResult
	if err := c.Call(context.Background(), OpDetect, map[string]any{}, &det); err != nil {
		t.Fatalf("detect: %v", err)
	}
	if det.Language == "" || !det.Confident {
		t.Fatalf("detect result unconvincing: %+v", det)
	}

	var hv harvestResult
	if err := c.Call(context.Background(), OpHarvestCandidates,
		map[string]any{"repoRoot": "."}, &hv); err != nil {
		t.Fatalf("harvest_candidates: %v", err)
	}
	if len(hv.Candidates) < 2 {
		t.Fatalf("expected several candidates, got %+v", hv)
	}
	for _, cand := range hv.Candidates {
		if cand.ID == "" {
			t.Fatalf("candidate with empty id: %+v", hv.Candidates)
		}
	}

	target := hv.Candidates[0]
	sliceParams := map[string]any{
		"repoRoot":        ".",
		"candidateId":     target.ID,
		"entrySymbolPath": "mock.dart#Mock#run",
	}
	var sl sliceResult
	if err := c.Call(context.Background(), OpSlice, sliceParams, &sl); err != nil {
		t.Fatalf("slice: %v", err)
	}
	if sl.CandidateID != target.ID ||
		sl.EntrySymbolPath != "mock.dart#Mock#run" ||
		sl.RepoRoot != "." {
		t.Fatalf("slice echo mismatch: sent %+v got %+v", sliceParams, sl)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Call(ctx, OpShutdown, map[string]any{}, nil); err != nil {
		t.Fatalf("shutdown op: %v", err)
	}
}

func confVersionMismatch(t *testing.T, bin string) {
	cfg := Config{
		BinPath: bin,
		Env:     faultEnv(map[string]string{"MOCK_PROTOCOL_VERSION": "2"}),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_, err := Spawn(ctx, cfg)
	perr := wantCode(t, err, EUnsupportedVersion)
	if IsRetryable(perr) {
		t.Fatal("version mismatch must not be retryable")
	}
}

func confTimeout(t *testing.T, bin string) {
	c := spawnConn(t, bin, map[string]string{"MOCK_HANG_OPS": "slice"}, func(c *Config) {
		c.DefaultTimeout = 250 * time.Millisecond
	})
	defer c.Close()

	start := time.Now()
	err := c.Call(context.Background(), OpSlice, map[string]any{"candidateId": "x"}, nil)
	perr := wantCode(t, err, ETimeout)
	if !IsRetryable(perr) {
		t.Fatal("E_TIMEOUT should be retryable")
	}
	if el := time.Since(start); el > 3*time.Second {
		t.Fatalf("timeout took %v; per-request deadline not enforced", el)
	}
	// Note: the mock serves requests sequentially, so the still-hung
	// slice op legitimately blocks subsequent ops on this connection;
	// per-request deadline + typed error is what's under test here.
}

func confCancel(t *testing.T, bin string) {
	c := spawnConn(t, bin, map[string]string{"MOCK_HANG_OPS": "harvest_candidates"}, nil)
	defer c.Close()

	ctx, cancel := context.WithCancel(context.Background())
	start := time.Now()
	done := make(chan error, 1)
	go func() {
		done <- c.Call(ctx, OpHarvestCandidates, map[string]any{"repoRoot": "."}, nil)
	}()
	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		wantCode(t, err, ECancelled)
	case <-time.After(3 * time.Second):
		t.Fatal("cancellation did not return promptly")
	}
	if el := time.Since(start); el > 2*time.Second {
		t.Fatalf("cancel took %v", el)
	}
}

// confCrashRestartOnce: the adapter crashes without responding on its
// first-ever counted request (global fire-once counter). Pool must
// transparently restart once and the retried request must succeed.
func confCrashRestartOnce(t *testing.T, bin string) {
	stateFile := filepath.Join(t.TempDir(), "crash-state.json")
	pool := NewPool(Config{
		BinPath: bin,
		Env: faultEnv(map[string]string{
			"MOCK_CRASH_AFTER_N_REQUESTS": "1",
			"MOCK_CRASH_STATE_FILE":       stateFile,
		}),
	}, 1)
	defer pool.Close()

	var first detResult
	if err := pool.Call(context.Background(), OpDetect, map[string]any{}, &first); err != nil {
		t.Fatalf("restart-once recovery failed: %v", err)
	}
	if first.PID <= 0 {
		t.Fatalf("detect missing pid: %+v", first)
	}

	var second detResult
	if err := pool.Call(context.Background(), OpDetect, map[string]any{}, &second); err != nil {
		t.Fatalf("post-restart reuse failed: %v", err)
	}
	if second.PID != first.PID {
		t.Fatalf("pool should reuse restarted process: pid %d then %d", first.PID, second.PID)
	}

	// count 1 = the crashing request in proc A; counts 2 and 3 = the
	// retried request and the reuse call, both served by proc B.
	raw, err := os.ReadFile(stateFile)
	if err != nil {
		t.Fatalf("read crash state: %v", err)
	}
	if want := `{"count":3,"fired":true}`; string(raw) != want {
		t.Fatalf("crash state %s, want %s", raw, want)
	}
}

// confDoubleCrash: deterministic per-process crash on every counted
// request. Restart-once budget is spent; E_CRASHED must surface.
func confDoubleCrash(t *testing.T, bin string) {
	pool := NewPool(Config{
		BinPath: bin,
		Env:     faultEnv(map[string]string{"MOCK_CRASH_AFTER_N_REQUESTS": "1"}),
	}, 1)
	defer pool.Close()

	err := pool.Call(context.Background(), OpDetect, map[string]any{}, nil)
	wantCode(t, err, ECrashed)
}

func confFlood(t *testing.T, bin string) {
	const maxMsg = 4096
	c := spawnConn(t, bin, map[string]string{"MOCK_FLOOD_BYTES": "65536"}, func(c *Config) {
		c.MaxMessageSizeBytes = maxMsg
	})
	defer c.Close()

	err := c.Call(context.Background(), OpSlice, map[string]any{"candidateId": "flooded"}, nil)
	wantCode(t, err, ECrashed)
	if c.Broken() == nil {
		t.Fatal("connection must be marked broken after oversize line")
	}
}

func confStartupCrash(t *testing.T, bin string) {
	cfg := Config{
		BinPath: bin,
		Env:     faultEnv(map[string]string{"MOCK_EXIT_ON_STARTUP": "1"}),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_, err := Spawn(ctx, cfg)
	perr := wantCode(t, err, ECrashed)
	if perr.Detail == nil {
		t.Fatal("spawn failure should carry detail")
	}
}

func confConcurrent(t *testing.T, bin string) {
	c := spawnConn(t, bin, map[string]string{"MOCK_DELAY_MS": "20"}, nil)
	defer c.Close()

	const n = 5
	ids := make([]string, n)
	results := make([]sliceResult, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ids[i] = "cand-concurrent-" + string(rune('a'+i))
			params := map[string]any{
				"repoRoot":        ".",
				"candidateId":     ids[i],
				"entrySymbolPath": "mock.dart#Concurrent",
			}
			errs[i] = c.Call(context.Background(), OpSlice, params, &results[i])
		}(i)
	}
	wg.Wait()

	seen := map[string]bool{}
	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("call %d: %v", i, errs[i])
		}
		if results[i].CandidateID != ids[i] {
			t.Fatalf("call %d: response crossed wires: sent %q got %q",
				i, ids[i], results[i].CandidateID)
		}
		if seen[ids[i]] {
			t.Fatalf("duplicate candidate id %q", ids[i])
		}
		seen[ids[i]] = true
	}
}

func confBackpressure(t *testing.T, bin string) {
	c := spawnConn(t, bin, map[string]string{"MOCK_HANG_OPS": "slice"}, func(c *Config) {
		c.MaxInFlight = 2
	})
	defer c.Close()

	const n = 4
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 600*time.Millisecond)
			defer cancel()
			errs <- c.Call(ctx, OpSlice, map[string]any{"candidateId": "bp"}, nil)
		}()
	}

	backpressure, timeouts := 0, 0
	for i := 0; i < n; i++ {
		select {
		case err := <-errs:
			switch {
			case err == nil:
				t.Fatal("hanging op cannot succeed")
			default:
				var perr *Error
				if !errors.As(err, &perr) {
					t.Fatalf("non-protocol error: %v", err)
				}
				switch perr.Code {
				case EBackpressure:
					backpressure++
					if !IsRetryable(perr) {
						t.Fatal("E_BACKPRESSURE should be retryable")
					}
				case ETimeout:
					timeouts++
				default:
					t.Fatalf("unexpected error: %v", err)
				}
			}
		case <-time.After(5 * time.Second):
			t.Fatal("backpressure case did not settle")
		}
	}
	if backpressure != n/2 || timeouts != n/2 {
		t.Fatalf("want %d backpressures + %d timeouts, got %d + %d",
			n/2, n/2, backpressure, timeouts)
	}
}
