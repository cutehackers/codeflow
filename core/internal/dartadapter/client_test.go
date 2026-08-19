package dartadapter

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func executable(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "adapter")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDiscoverClassifiesMalformedAndDeadlineOutput(t *testing.T) {
	malformed := executable(t, "echo '{not json}'\n")
	_, err := Discover(context.Background(), malformed, t.TempDir())
	if failure := AsFailure(err); failure == nil || failure.Code != "ADAPTER_MALFORMED" {
		t.Fatalf("%v", err)
	}
	timeout := executable(t, "sleep 2\n")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, err = Discover(ctx, timeout, t.TempDir())
	if failure := AsFailure(err); failure == nil || failure.Code != "ADAPTER_TIMEOUT" {
		t.Fatalf("%v", err)
	}
}

func TestTimeoutTerminatesClientSoLateOutputCannotCorrelateToNextRequest(t *testing.T) {
	// The child deliberately emits a response after the caller's deadline.
	// A client must become unusable, rather than let that late response satisfy
	// a later request with a different id.
	late := executable(t, "read line; sleep 1; echo '{\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{}}'\n")
	client, err := Start(context.Background(), late)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	var ignored any
	if failure := AsFailure(client.call(ctx, "initialize", map[string]any{}, &ignored)); failure == nil || failure.Code != "ADAPTER_TIMEOUT" {
		t.Fatalf("%v", failure)
	}
	if failure := AsFailure(client.call(context.Background(), "initialize", map[string]any{}, &ignored)); failure == nil || failure.Code != "ADAPTER_UNAVAILABLE" {
		t.Fatalf("%v", failure)
	}
}
