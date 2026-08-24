package protocol

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// fixturesDir locates schemas/fixtures/adapter-protocol by walking up
// from the package dir until a go.mod root is found — robust to where
// the test binary is invoked from.
func fixturesDir(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for p := dir; ; p = filepath.Dir(p) {
		cand := filepath.Join(p, "schemas", "fixtures", "adapter-protocol")
		if st, serr := os.Stat(cand); serr == nil && st.IsDir() {
			return cand
		}
		if p == filepath.Dir(p) {
			t.Fatal("schemas/fixtures/adapter-protocol not found above package dir")
		}
	}
}

func readFixtures(t *testing.T, sub string) map[string][]byte {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(fixturesDir(t), sub))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][]byte{}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(fixturesDir(t), sub, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		out[e.Name()] = b
	}
	return out
}

func asJSONMap(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return m
}

func TestValidFixtureRoundTrip(t *testing.T) {
	for name, raw := range readFixtures(t, "valid") {
		t.Run(name, func(t *testing.T) {
			if err := ValidateEnvelope(raw); err != nil {
				t.Fatalf("valid fixture rejected: %v", err)
			}

			var out []byte
			var probe struct {
				OK *bool `json:"ok"`
			}
			if err := json.Unmarshal(raw, &probe); err != nil {
				t.Fatal(err)
			}
			switch {
			case probe.OK == nil: // request
				var env RequestEnvelope
				if err := json.Unmarshal(raw, &env); err != nil {
					t.Fatal(err)
				}
				out, _ = json.Marshal(env)
			case *probe.OK: // ok response
				var env OkEnvelope
				if err := json.Unmarshal(raw, &env); err != nil {
					t.Fatal(err)
				}
				out, _ = json.Marshal(env)
			default: // error response
				var env ErrEnvelope
				if err := json.Unmarshal(raw, &env); err != nil {
					t.Fatal(err)
				}
				out, _ = json.Marshal(env)
			}

			orig := asJSONMap(t, raw)
			rt := asJSONMap(t, out)
			if !reflect.DeepEqual(orig, rt) {
				t.Fatalf("round-trip drift:\n orig: %v\n rt:   %v", orig, rt)
			}
		})
	}
}

func TestInvalidFixturesRejected(t *testing.T) {
	for name, raw := range readFixtures(t, "invalid") {
		t.Run(name, func(t *testing.T) {
			if err := ValidateEnvelope(raw); err == nil {
				t.Fatal("invalid fixture accepted")
			}
		})
	}
}

func TestValidateEnvelopeTypedCodes(t *testing.T) {
	v2 := []byte(`{"v":2,"id":"a","op":"ping","params":{}}`)
	err := ValidateEnvelope(v2)
	var perr *Error
	if !errors.As(err, &perr) || perr.Code != EUnsupportedVersion {
		t.Fatalf("want E_UNSUPPORTED_VERSION, got %v", err)
	}
	badOp := []byte(`{"v":1,"id":"a","op":"parse_transcript","params":{}}`)
	if err := ValidateEnvelope(badOp); !errors.As(err, &perr) || perr.Code != EBadRequest {
		t.Fatalf("want E_BAD_REQUEST, got %v", err)
	}
	paramsArray := []byte(`{"v":1,"id":"a","op":"slice","params":[]}`)
	if err := ValidateEnvelope(paramsArray); !errors.As(err, &perr) || perr.Code != EBadRequest {
		t.Fatalf("want E_BAD_REQUEST for non-object params, got %v", err)
	}
}

func TestErrorWireShape(t *testing.T) {
	e := &Error{Code: EBackpressure, Message: "queue full", Retryable: true, Detail: "depth 129 > 128"}
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"code": "E_BACKPRESSURE", "message": "queue full",
		"retryable": true, "detail": "depth 129 > 128",
	}
	if !reflect.DeepEqual(m, want) {
		t.Fatalf("wire shape mismatch:\n got %v\nwant %v", m, want)
	}

	// Non-string detail is coerced to string on the wire.
	b, _ = json.Marshal(&Error{Code: ECrashed, Message: "m", Detail: 137})
	m = asJSONMap(t, b)
	if m["detail"] != "137" {
		t.Fatalf("want detail coerced to \"137\", got %v", m["detail"])
	}

	// Nil detail omitted.
	b, _ = json.Marshal(&Error{Code: ECancelled, Message: "m"})
	m = asJSONMap(t, b)
	if _, present := m["detail"]; present {
		t.Fatalf("nil detail must be omitted, got %v", m)
	}

	// Round-trip preserves string detail.
	var back Error
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if back.Code != ECancelled || back.Message != "m" || back.Detail != nil {
		t.Fatalf("bad decode: %+v", &back)
	}
}

func TestUnknownErrorCodeRejectedOnDecode(t *testing.T) {
	raw := []byte(`{"id":"x","ok":false,"err":{"code":"E_KABOOM","message":"boom","retryable":false}}`)
	var env ErrEnvelope
	if err := json.Unmarshal(raw, &env); err == nil {
		t.Fatal("unknown error code must fail decode")
	}
	if err := ValidateEnvelope(raw); err == nil {
		t.Fatal("unknown error code must fail validation")
	}
}

func TestSentinelsAndIsRetryable(t *testing.T) {
	retryableByCode := map[ErrorCode]bool{
		ETimeout: true, EBackpressure: true,
		ECancelled: false, ECrashed: false, EBadRequest: false,
		EUnsupportedVersion: false, EAdapterInternal: false,
	}
	sentinels := map[ErrorCode]*Error{
		ETimeout: ErrTimeout, ECancelled: ErrCancelled, ECrashed: ErrCrashed,
		EBackpressure: ErrBackpressure, EBadRequest: ErrBadRequest,
		EUnsupportedVersion: ErrUnsupportedVersion, EAdapterInternal: ErrAdapterInternal,
	}
	for code, s := range sentinels {
		if s.Code != code {
			t.Fatalf("sentinel %s has code %s", code, s.Code)
		}
		if s.Retryable != retryableByCode[code] {
			t.Fatalf("%s retryable=%v, want %v", code, s.Retryable, retryableByCode[code])
		}
		fresh := NewError(code, "fresh", nil)
		if !errors.Is(fresh, s) {
			t.Fatalf("errors.Is must match sentinel by code for %s", code)
		}
		wrapped := fmt.Errorf("wrapped: %w", fresh)
		if IsRetryable(wrapped) != retryableByCode[code] {
			t.Fatalf("IsRetryable(%s) = %v", code, IsRetryable(wrapped))
		}
	}
	if IsRetryable(errors.New("plain")) {
		t.Fatal("non-protocol errors are not retryable")
	}
}

func TestRequestEnvelopeNilParamsMarshalsEmptyObject(t *testing.T) {
	env := RequestEnvelope{V: ProtocolVersion, ID: "cf-1", Op: OpPing}
	b, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	m := asJSONMap(t, b)
	params, ok := m["params"].(map[string]any)
	if !ok {
		t.Fatalf("params must marshal to an object, got %v", m["params"])
	}
	if len(params) != 0 {
		t.Fatalf("params should be empty object, got %v", params)
	}
	if err := ValidateEnvelope(b); err != nil {
		t.Fatalf("nil-params envelope must validate: %v", err)
	}
}
