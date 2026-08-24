// Package protocol implements the CORE side of the CodeFlow adapter
// protocol (design-v2 §5.2): NDJSON over stdio to a persistent adapter
// subprocess, with request-id correlation, typed error codes,
// per-request timeout and cancellation, crash detection with a
// restart-once policy (§12), max-message-size enforcement, and
// in-flight backpressure. The wire contract is
// schemas/adapter-protocol.schema.json.
package protocol

import (
	"encoding/json"
	"errors"
	"fmt"
)

// ProtocolVersion is the major protocol version spoken by this CORE
// build. Adapters echo it in the ping handshake result; any other major
// version fails with E_UNSUPPORTED_VERSION (contract §6).
const ProtocolVersion = 1

// Wire operation names (the closed op enum of the contract).
const (
	OpDetect            = "detect"
	OpHarvestCandidates = "harvest_candidates"
	OpSlice             = "slice"
	OpPing              = "ping"
	OpShutdown          = "shutdown"
)

// Ops is the closed set of valid wire operations.
var Ops = map[string]bool{
	OpDetect:            true,
	OpHarvestCandidates: true,
	OpSlice:             true,
	OpPing:              true,
	OpShutdown:          true,
}

// ErrorCode is one of the typed error codes mandated by the contract.
// Free-form codes are a violation and are rejected at decode time.
type ErrorCode string

// The complete error-code enum of the contract.
const (
	ETimeout            ErrorCode = "E_TIMEOUT"
	ECancelled          ErrorCode = "E_CANCELLED"
	ECrashed            ErrorCode = "E_CRASHED"
	EBackpressure       ErrorCode = "E_BACKPRESSURE"
	EBadRequest         ErrorCode = "E_BAD_REQUEST"
	EUnsupportedVersion ErrorCode = "E_UNSUPPORTED_VERSION"
	EAdapterInternal    ErrorCode = "E_ADAPTER_INTERNAL"
)

// IsValidErrorCode reports whether c is part of the contract enum.
func IsValidErrorCode(c ErrorCode) bool {
	switch c {
	case ETimeout, ECancelled, ECrashed, EBackpressure,
		EBadRequest, EUnsupportedVersion, EAdapterInternal:
		return true
	}
	return false
}

// Error is the typed protocol error. It marshals onto the wire exactly
// as the schema's err object; Detail is coerced to a string on the wire
// because the contract allows only string-or-null there.
type Error struct {
	Code      ErrorCode `json:"code"`
	Message   string    `json:"message"`
	Retryable bool      `json:"retryable"`
	Detail    any       `json:"-"`
}

// Sentinel errors, one per contract code. Protocol errors returned by
// this package are fresh instances, but errors.Is works against these
// sentinels by code equality via (*Error).Is.
var (
	ErrTimeout            = &Error{Code: ETimeout, Message: "request timed out before the adapter responded", Retryable: true}
	ErrCancelled          = &Error{Code: ECancelled, Message: "request cancelled"}
	ErrCrashed            = &Error{Code: ECrashed, Message: "adapter process crashed or connection lost"}
	ErrBackpressure       = &Error{Code: EBackpressure, Message: "too many in-flight requests", Retryable: true}
	ErrBadRequest         = &Error{Code: EBadRequest, Message: "malformed request envelope"}
	ErrUnsupportedVersion = &Error{Code: EUnsupportedVersion, Message: "unsupported adapter protocol version"}
	ErrAdapterInternal    = &Error{Code: EAdapterInternal, Message: "adapter internal error"}
)

// defaultRetryable mirrors the contract note: retryable=true is
// expected for E_BACKPRESSURE and transient E_TIMEOUT.
var defaultRetryable = map[ErrorCode]bool{
	ETimeout:      true,
	EBackpressure: true,
}

// NewError builds a fresh *Error with the code's default retryability.
func NewError(code ErrorCode, message string, detail any) *Error {
	return &Error{Code: code, Message: message, Retryable: defaultRetryable[code], Detail: detail}
}

// Per-code constructors. All return fresh values safe to mutate.

func TimeoutError(detail any) *Error {
	return NewError(ETimeout, ErrTimeout.Message, detail)
}

func CancelledError(detail any) *Error {
	return NewError(ECancelled, ErrCancelled.Message, detail)
}

func CrashedError(detail any) *Error {
	return NewError(ECrashed, ErrCrashed.Message, detail)
}

func BackpressureError(detail any) *Error {
	return NewError(EBackpressure, ErrBackpressure.Message, detail)
}

func BadRequestError(detail any) *Error {
	return NewError(EBadRequest, ErrBadRequest.Message, detail)
}

func UnsupportedVersionError(detail any) *Error {
	return NewError(EUnsupportedVersion, ErrUnsupportedVersion.Message, detail)
}

func AdapterInternalError(detail any) *Error {
	return NewError(EAdapterInternal, ErrAdapterInternal.Message, detail)
}

// Error implements the error interface.
func (e *Error) Error() string {
	s := fmt.Sprintf("%s: %s", e.Code, e.Message)
	if e.Detail != nil {
		s += fmt.Sprintf(" (%v)", e.Detail)
	}
	return s
}

// Is reports code equality so errors.Is(err, protocol.ErrTimeout) and
// friends work on freshly constructed errors of the same code.
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && t.Code == e.Code
}

// MarshalJSON renders the wire shape: detail becomes a string (or is
// omitted when nil), because the contract types detail as string|null.
func (e *Error) MarshalJSON() ([]byte, error) {
	wire := struct {
		Code      ErrorCode `json:"code"`
		Message   string    `json:"message"`
		Retryable bool      `json:"retryable"`
		Detail    *string   `json:"detail,omitempty"`
	}{Code: e.Code, Message: e.Message, Retryable: e.Retryable}
	if e.Detail != nil {
		d := fmt.Sprintf("%v", e.Detail)
		wire.Detail = &d
	}
	return json.Marshal(wire)
}

// UnmarshalJSON parses the wire shape and rejects unknown codes —
// free-form codes are a contract violation.
func (e *Error) UnmarshalJSON(b []byte) error {
	var wire struct {
		Code      ErrorCode `json:"code"`
		Message   string    `json:"message"`
		Retryable bool      `json:"retryable"`
		Detail    *string   `json:"detail"`
	}
	if err := json.Unmarshal(b, &wire); err != nil {
		return err
	}
	if !IsValidErrorCode(wire.Code) {
		return fmt.Errorf("adapter-protocol: unknown error code %q (not in contract enum)", string(wire.Code))
	}
	e.Code = wire.Code
	e.Message = wire.Message
	e.Retryable = wire.Retryable
	if wire.Detail != nil {
		e.Detail = *wire.Detail
	}
	return nil
}

// IsRetryable unwraps err looking for a *Error and reports its
// Retryable flag; non-protocol errors are not retryable.
func IsRetryable(err error) bool {
	var perr *Error
	if errors.As(err, &perr) {
		return perr.Retryable
	}
	return false
}

// RequestEnvelope is the v1 request frame: {"v":1,"id","op","params"}.
type RequestEnvelope struct {
	V      int             `json:"v"`
	ID     string          `json:"id"`
	Op     string          `json:"op"`
	Params json.RawMessage `json:"params"`
}

// MarshalJSON guarantees params is always present and an object, even
// when Params is nil (emitted as {}).
func (e RequestEnvelope) MarshalJSON() ([]byte, error) {
	params := e.Params
	if len(params) == 0 {
		params = json.RawMessage("{}")
	}
	return json.Marshal(struct {
		V      int             `json:"v"`
		ID     string          `json:"id"`
		Op     string          `json:"op"`
		Params json.RawMessage `json:"params"`
	}{e.V, e.ID, e.Op, params})
}

// OkEnvelope is the success response frame {"id","ok":true,"result"}.
type OkEnvelope struct {
	ID     string          `json:"id"`
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result"`
}

// ErrEnvelope is the error response frame {"id","ok":false,"err"}.
type ErrEnvelope struct {
	ID  string `json:"id"`
	OK  bool   `json:"ok"`
	Err *Error `json:"err"`
}

// VersionInfo is the ping handshake payload ($defs.versionNegotiation).
type VersionInfo struct {
	AdapterVersion  string `json:"adapterVersion"`
	ProtocolVersion int    `json:"protocolVersion"`
}

// ValidateEnvelope validates any single raw envelope against the wire
// contract: request | ok response | error response. It returns a *Error
// with E_UNSUPPORTED_VERSION for a wrong major version and E_BAD_REQUEST
// for every other shape violation.
func ValidateEnvelope(raw []byte) error {
	var probe struct {
		V      *int             `json:"v"`
		ID     *string          `json:"id"`
		Op     *string          `json:"op"`
		Params json.RawMessage  `json:"params"`
		OK     *bool            `json:"ok"`
		Result *json.RawMessage `json:"result"`
		ErrRaw *json.RawMessage `json:"err"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return BadRequestError(fmt.Sprintf("envelope is not a JSON object: %v", err))
	}
	if probe.OK == nil {
		return validateRequestShape(probe.V, probe.ID, probe.Op, probe.Params)
	}
	return validateResponseShape(probe.ID, *probe.OK, probe.Result, probe.ErrRaw)
}

func validateRequestShape(v *int, id *string, op *string, params json.RawMessage) error {
	switch {
	case v == nil:
		return BadRequestError("request envelope missing v")
	case *v != ProtocolVersion:
		return UnsupportedVersionError(fmt.Sprintf("request v=%d, CORE speaks v=%d", *v, ProtocolVersion))
	case id == nil || *id == "":
		return BadRequestError("request envelope missing id")
	case op == nil || !Ops[*op]:
		opName := "<missing>"
		if op != nil {
			opName = *op
		}
		return BadRequestError(fmt.Sprintf("unknown op %q (must be one of detect|harvest_candidates|slice|ping|shutdown)", opName))
	}
	if !isJSONObject(params) {
		return BadRequestError("params must be a JSON object")
	}
	return nil
}

func validateResponseShape(id *string, ok bool, result, errRaw *json.RawMessage) error {
	if id == nil || *id == "" {
		return BadRequestError("response envelope missing id")
	}
	if ok {
		if result == nil || !isJSONObject(*result) {
			return BadRequestError("ok response requires a result object")
		}
		return nil
	}
	if errRaw == nil {
		return BadRequestError("error response requires err")
	}
	var perr Error
	if err := json.Unmarshal(*errRaw, &perr); err != nil {
		return BadRequestError(fmt.Sprintf("invalid err object: %v", err))
	}
	if perr.Message == "" {
		return BadRequestError("err.message must be non-empty")
	}
	return nil
}

// ValidateRequest validates an already-decoded request envelope.
func ValidateRequest(e *RequestEnvelope) error {
	v := e.V
	id, op := e.ID, e.Op
	return validateRequestShape(&v, &id, &op, e.Params)
}

func isJSONObject(raw json.RawMessage) bool {
	for _, b := range raw {
		switch b {
		case ' ', '\t', '\r', '\n':
			continue
		case '{':
			return true
		default:
			return false
		}
	}
	return false
}
