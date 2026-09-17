package protocol

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestProtocolReadLoopRedactsMalformedJSONBeforeDiagnosticClip(t *testing.T) {
	secretValue := strings.Repeat("malformed-secret-", 200)
	body := []byte(`{"apiKey":"` + secretValue)
	var framed bytes.Buffer
	if err := writeFrame(&framed, body); err != nil {
		t.Fatal(err)
	}
	c := &Conn{
		cfg:      Config{MaxMessageSizeBytes: int64(len(body) + 64)},
		pending:  map[string]chan *reply{},
		waitDone: make(chan struct{}),
	}
	c.readLoop(&framed)

	broken := c.Broken()
	if broken == nil {
		t.Fatal("malformed adapter body did not break the connection")
	}
	diagnostic := broken.Error()
	if strings.Contains(diagnostic, secretValue[:32]) {
		t.Fatalf("malformed JSON secret leaked through clipped diagnostic: %q", diagnostic)
	}
	if !strings.Contains(diagnostic, "***REDACTED***") {
		t.Fatalf("malformed JSON diagnostic lost redaction marker: %q", diagnostic)
	}
}

func TestProtocolStderrTailRedactsAcrossChunksAndTailCutoff(t *testing.T) {
	secretValue := strings.Repeat("stderr-secret-", 900)
	c := &Conn{}
	tail := tailBuffer{c: c}
	if _, err := tail.Write([]byte(`prefix {"apiKey":"` + secretValue[:len(secretValue)/2])); err != nil {
		t.Fatal(err)
	}
	if _, err := tail.Write([]byte(secretValue[len(secretValue)/2:] + `"}`)); err != nil {
		t.Fatal(err)
	}
	diagnostic := c.StderrTail()
	if strings.Contains(diagnostic, secretValue[:32]) {
		t.Fatalf("chunk-split stderr secret leaked: %q", diagnostic)
	}
	if !strings.Contains(diagnostic, "***REDACTED***") {
		t.Fatalf("chunk-split stderr diagnostic lost redaction marker: %q", diagnostic)
	}

	c = &Conn{}
	tail = tailBuffer{c: c}
	prefix := strings.Repeat("safe ", stderrTailBytes/5)
	if _, err := tail.Write([]byte(prefix + `{"apiKey":"` + secretValue + `"}`)); err != nil {
		t.Fatal(err)
	}
	diagnostic = c.StderrTail()
	if strings.Contains(diagnostic, secretValue[:32]) {
		t.Fatalf("tail-cutoff stderr secret leaked: %q", diagnostic)
	}
}

func TestProtocolStderrTailDoesNotFinalizeLookbehindBetweenChunks(t *testing.T) {
	secretValue := strings.Repeat("interleaved-secret-", 80)
	c := &Conn{}
	tail := tailBuffer{c: c}
	if _, err := tail.Write([]byte(`prefix {"apiK`)); err != nil {
		t.Fatal(err)
	}
	// Reading diagnostics while a possible credential key is split across
	// chunks must not clear the redactor's lookbehind state.
	if diagnostic := c.StderrTail(); strings.Contains(diagnostic, "apiK") {
		t.Fatalf("partial credential key escaped diagnostics: %q", diagnostic)
	}
	if _, err := tail.Write([]byte(`ey":"` + secretValue + `"}`)); err != nil {
		t.Fatal(err)
	}
	diagnostic := c.StderrTail()
	if strings.Contains(diagnostic, secretValue[:32]) {
		t.Fatalf("interleaved stderr secret leaked: %q", diagnostic)
	}
	if !strings.Contains(diagnostic, "***REDACTED***") {
		t.Fatalf("interleaved stderr diagnostic lost redaction marker: %q", diagnostic)
	}
}

func TestProtocolStderrEOFFinalizesSafeLookbehindButDropsSecret(t *testing.T) {
	c := &Conn{}
	tail := tailBuffer{c: c}
	if _, err := tail.Write([]byte("safe trailing diagnostic")); err != nil {
		t.Fatal(err)
	}
	if got := c.StderrTail(); got != "" {
		t.Fatalf("pending bytes were exposed before EOF: %q", got)
	}
	c.finalizeStderr()
	if got := c.StderrTail(); got != "safe trailing diagnostic" {
		t.Fatalf("safe EOF tail = %q", got)
	}

	c = &Conn{}
	tail = tailBuffer{c: c}
	secretValue := strings.Repeat("eof-secret-", 80)
	if _, err := tail.Write([]byte(`{"apiKey":"` + secretValue)); err != nil {
		t.Fatal(err)
	}
	c.finalizeStderr()
	if got := c.StderrTail(); strings.Contains(got, secretValue[:32]) {
		t.Fatalf("unterminated EOF secret leaked: %q", got)
	}
}

func TestProtocolStderrRedactsLongCredentialKeySplitBeyondLookbehind(t *testing.T) {
	key := "password" + strings.Repeat("x", diagnosticStreamLookbehind+80)
	secretValue := strings.Repeat("long-key-secret-", 1000)
	c := &Conn{}
	tail := tailBuffer{c: c}
	if _, err := tail.Write([]byte(`prefix {"` + key)); err != nil {
		t.Fatal(err)
	}
	if _, err := tail.Write([]byte(`":"` + secretValue + `"}`)); err != nil {
		t.Fatal(err)
	}
	diagnostic := c.StderrTail()
	if strings.Contains(diagnostic, secretValue[:32]) {
		t.Fatalf("long credential key split beyond lookbehind leaked secret: %q", diagnostic)
	}
	if !strings.Contains(diagnostic, "***REDACTED***") {
		t.Fatalf("long credential key split diagnostic lost redaction marker: %q", diagnostic)
	}
}

func TestRPCErrorStructuredDataRedactsBeforeDecodeAndClip(t *testing.T) {
	secretValue := strings.Repeat("nested-rpc-secret-", 200)
	rawData, err := json.Marshal(map[string]any{
		"code":      "E_ADAPTER_INTERNAL",
		"message":   "adapter failed",
		"retryable": false,
		"detail": map[string]any{
			"outer": []any{
				map[string]any{"clientSecret": secretValue},
				map[string]any{"safe": strings.Repeat("diagnostic-", 100)},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := errorFromRPC(rpcError{Code: -32000, Message: "adapter failed", Data: rawData})
	if got == nil || got.Code != EAdapterInternal {
		t.Fatalf("structured RPC error = %+v", got)
	}
	if got.Detail == nil {
		t.Fatal("structured RPC detail was discarded")
	}
	encoded, err := json.Marshal(got.Detail)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), secretValue[:32]) {
		t.Fatalf("structured RPC secret leaked after decode: %s", encoded)
	}

	for name, detail := range map[string]any{
		"typed map": map[string]string{"apiKey": secretValue},
		"typed struct": struct {
			ClientSecret string `json:"clientSecret"`
		}{ClientSecret: secretValue},
		"typed pointer struct": &struct {
			ClientSecret string `json:"clientSecret"`
		}{ClientSecret: secretValue},
	} {
		t.Run(name, func(t *testing.T) {
			wire := rpcErrorFor(NewError(EAdapterInternal, "adapter failed", detail))
			if strings.Contains(string(wire.Data), secretValue[:32]) {
				t.Fatalf("typed structured RPC secret leaked: %s", wire.Data)
			}
		})
	}
}
