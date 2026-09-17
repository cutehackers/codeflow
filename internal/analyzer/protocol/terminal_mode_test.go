package protocol

import (
	"context"
	"testing"
	"time"
)

func TestConnTerminalModeDistinguishesDeadlineAndCancellation(t *testing.T) {
	bin := buildMockAdapter(t)
	snapshot, err := NewSnapshot(1, map[string]string{"main.go": "package main\n"}, "terminal-mode-basis")
	if err != nil {
		t.Fatal(err)
	}

	t.Run("expired-deadline-is-timeout", func(t *testing.T) {
		conn, err := Spawn(context.Background(), Config{BinPath: bin, Env: faultEnv(map[string]string{"MOCK_HANG_OPS": "detect"})})
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
		defer cancel()
		if err := conn.Call(ctx, OpDetect, snapshot.Params(), nil); err == nil {
			t.Fatal("expired deadline unexpectedly succeeded")
		} else {
			wantCode(t, err, ETimeout)
		}
		evidence := conn.MountPermissionEvidence()
		if !hasTerminalMode(evidence.TerminalModes, "timeout") || hasTerminalMode(evidence.TerminalModes, "cancel") {
			t.Fatalf("expired deadline was misclassified: %+v", evidence.TerminalModes)
		}
	})

	t.Run("explicit-cancel-is-cancel", func(t *testing.T) {
		conn, err := Spawn(context.Background(), Config{BinPath: bin, Env: faultEnv(map[string]string{"MOCK_HANG_OPS": "detect"})})
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- conn.Call(ctx, OpDetect, snapshot.Params(), nil) }()
		time.Sleep(50 * time.Millisecond)
		cancel()
		select {
		case err := <-done:
			wantCode(t, err, ECancelled)
		case <-time.After(3 * time.Second):
			t.Fatal("explicit cancellation did not settle")
		}
		evidence := conn.MountPermissionEvidence()
		if !hasTerminalMode(evidence.TerminalModes, "cancel") || hasTerminalMode(evidence.TerminalModes, "timeout") {
			t.Fatalf("explicit cancellation was misclassified: %+v", evidence.TerminalModes)
		}
	})
}

func hasTerminalMode(modes []string, want string) bool {
	for _, mode := range modes {
		if mode == want {
			return true
		}
	}
	return false
}
