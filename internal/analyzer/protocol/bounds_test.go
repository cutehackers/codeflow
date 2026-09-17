package protocol

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

func TestOutboundFrameBoundExactAndOversize(t *testing.T) {
	const limit = int64(64)
	exact := bytes.Repeat([]byte("x"), int(limit))
	var out bytes.Buffer
	if err := writeFrameBounded(&out, exact, limit); err != nil {
		t.Fatalf("exact-bound frame rejected: %v", err)
	}
	if !strings.HasPrefix(out.String(), "Content-Length: 64\r\n\r\n") {
		t.Fatalf("exact-bound frame header = %q", out.String()[:minLen(out.Len(), 32)])
	}
	before := out.Len()
	var typed *Error
	err := writeFrameBounded(&out, append(exact, 'y'), limit)
	if !errors.As(err, &typed) || typed.Code != EBadRequest {
		t.Fatalf("oversized outbound frame error = %v, want E_BAD_REQUEST", err)
	}
	if out.Len() != before {
		t.Fatalf("oversized outbound frame wrote %d bytes", out.Len()-before)
	}
}

func TestOutboundNotificationUsesNegotiatedBound(t *testing.T) {
	var out bytes.Buffer
	err := writeJSONRPCNotificationBounded(context.Background(), &out, "codeflow/diagnostic", map[string]any{
		"detail": strings.Repeat("diagnostic", 32),
	}, 64)
	var typed *Error
	if !errors.As(err, &typed) || typed.Code != EBadRequest {
		t.Fatalf("oversized notification error = %v, want E_BAD_REQUEST", err)
	}
	if out.Len() != 0 {
		t.Fatalf("oversized notification wrote %d bytes", out.Len())
	}
}

func TestInboundDeclaredOversizeRejectsBeforeBodyAllocation(t *testing.T) {
	header := []byte("Content-Length: 9223372036854775807\r\n\r\n")
	_, err := readNextFrame(bufio.NewReader(bytes.NewReader(header)), 64)
	if !errors.Is(err, errFrameTooLarge) {
		t.Fatalf("declared oversized frame error = %v, want errFrameTooLarge", err)
	}
}

func minLen(a, b int) int {
	if a < b {
		return a
	}
	return b
}
