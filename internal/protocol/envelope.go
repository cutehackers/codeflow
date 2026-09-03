// Package protocol implements the CORE side of the CodeFlow adapter
// protocol. JSON-RPC 2.0 bodies are carried by Content-Length framed stdio.
// The logical Op* names remain at the Go package seam for existing callers.
package protocol

import (
	"encoding/json"
	"errors"
	"fmt"

	"codeflow/internal/secret"
)

const (
	// ProtocolVersion is the CodeFlow adapter capability version. JSON-RPC's
	// protocol marker is JSONRPCVersion and is intentionally separate.
	ProtocolVersion = 1
	JSONRPCVersion  = "2.0"
)

const (
	OpInitialize        = "initialize"
	OpDetect            = "detect"
	OpHarvestCandidates = "harvest_candidates"
	OpSlice             = "slice"
	OpPing              = "ping" // compatibility alias for initialize
	OpShutdown          = "shutdown"
)

// Ops is the logical operation set exposed by Pool.Call. Ping is retained as
// a compatibility alias but maps to the initialize method on the transport.
var Ops = map[string]bool{
	OpInitialize: true, OpDetect: true, OpHarvestCandidates: true,
	OpSlice: true, OpPing: true, OpShutdown: true,
}

func rpcMethodForOp(op string) string {
	if op == OpPing {
		return OpInitialize
	}
	return op
}

type ErrorCode string

const (
	ETimeout            ErrorCode = "E_TIMEOUT"
	ECancelled          ErrorCode = "E_CANCELLED"
	ECrashed            ErrorCode = "E_CRASHED"
	EBackpressure       ErrorCode = "E_BACKPRESSURE"
	EBadRequest         ErrorCode = "E_BAD_REQUEST"
	EUnsupportedVersion ErrorCode = "E_UNSUPPORTED_VERSION"
	EAdapterInternal    ErrorCode = "E_ADAPTER_INTERNAL"
)

func IsValidErrorCode(c ErrorCode) bool {
	switch c {
	case ETimeout, ECancelled, ECrashed, EBackpressure,
		EBadRequest, EUnsupportedVersion, EAdapterInternal:
		return true
	}
	return false
}

type Error struct {
	Code      ErrorCode `json:"code"`
	Message   string    `json:"message"`
	Retryable bool      `json:"retryable"`
	Detail    any       `json:"-"`
}

var (
	ErrTimeout            = &Error{Code: ETimeout, Message: "request timed out before the adapter responded", Retryable: true}
	ErrCancelled          = &Error{Code: ECancelled, Message: "request cancelled"}
	ErrCrashed            = &Error{Code: ECrashed, Message: "adapter process crashed or connection lost"}
	ErrBackpressure       = &Error{Code: EBackpressure, Message: "too many in-flight requests", Retryable: true}
	ErrBadRequest         = &Error{Code: EBadRequest, Message: "malformed request envelope"}
	ErrUnsupportedVersion = &Error{Code: EUnsupportedVersion, Message: "unsupported adapter protocol version"}
	ErrAdapterInternal    = &Error{Code: EAdapterInternal, Message: "adapter internal error"}
)

var defaultRetryable = map[ErrorCode]bool{ETimeout: true, EBackpressure: true}

func NewError(code ErrorCode, message string, detail any) *Error {
	return &Error{Code: code, Message: message, Retryable: defaultRetryable[code], Detail: detail}
}
func TimeoutError(detail any) *Error   { return NewError(ETimeout, ErrTimeout.Message, detail) }
func CancelledError(detail any) *Error { return NewError(ECancelled, ErrCancelled.Message, detail) }
func CrashedError(detail any) *Error   { return NewError(ECrashed, ErrCrashed.Message, detail) }
func BackpressureError(detail any) *Error {
	return NewError(EBackpressure, ErrBackpressure.Message, detail)
}
func BadRequestError(detail any) *Error { return NewError(EBadRequest, ErrBadRequest.Message, detail) }
func UnsupportedVersionError(detail any) *Error {
	return NewError(EUnsupportedVersion, ErrUnsupportedVersion.Message, detail)
}
func AdapterInternalError(detail any) *Error {
	return NewError(EAdapterInternal, ErrAdapterInternal.Message, detail)
}

func (e *Error) Error() string {
	s := fmt.Sprintf("%s: %s", e.Code, e.Message)
	if e.Detail != nil {
		s += fmt.Sprintf(" (%v)", e.Detail)
	}
	return s
}

func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && t.Code == e.Code
}

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
	e.Code, e.Message, e.Retryable, e.Detail = wire.Code, wire.Message, wire.Retryable, nil
	if wire.Detail != nil {
		e.Detail = *wire.Detail
	}
	return nil
}

func IsRetryable(err error) bool {
	var perr *Error
	return errors.As(err, &perr) && perr.Retryable
}

// RequestEnvelope is a source-compatible Go seam. New values marshal as
// JSON-RPC 2.0. A value decoded from the historical v/op form is re-encoded
// in that form so the old fixture and helper tests remain useful while the
// subprocess transport has moved to framed JSON-RPC.
type RequestEnvelope struct {
	JSONRPC string          `json:"jsonrpc,omitempty"`
	ID      string          `json:"id"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params"`
	V       int             `json:"-"`
	Op      string          `json:"-"`
	legacy  bool
}

func (e RequestEnvelope) MarshalJSON() ([]byte, error) {
	params := e.Params
	if len(params) == 0 {
		params = json.RawMessage("{}")
	}
	if e.legacy {
		return json.Marshal(struct {
			V      int             `json:"v"`
			ID     string          `json:"id"`
			Op     string          `json:"op"`
			Params json.RawMessage `json:"params"`
		}{protocolVersionOrDefault(e.V), e.ID, logicalOp(e), params})
	}
	method := e.Method
	if method == "" {
		method = rpcMethodForOp(e.Op)
	}
	return json.Marshal(struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      string          `json:"id"`
		Method  string          `json:"method"`
		Params  json.RawMessage `json:"params"`
	}{JSONRPCVersion, e.ID, method, params})
}

func logicalOp(e RequestEnvelope) string {
	if e.Op != "" {
		return e.Op
	}
	return e.Method
}

func protocolVersionOrDefault(v int) int {
	if v == 0 {
		return ProtocolVersion
	}
	return v
}

func (e *RequestEnvelope) UnmarshalJSON(b []byte) error {
	var wire struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      string          `json:"id"`
		Method  string          `json:"method"`
		Params  json.RawMessage `json:"params"`
		V       int             `json:"v"`
		Op      string          `json:"op"`
	}
	if err := json.Unmarshal(b, &wire); err != nil {
		return err
	}
	e.JSONRPC, e.ID, e.Method, e.Params = wire.JSONRPC, wire.ID, wire.Method, wire.Params
	e.V, e.Op, e.legacy = wire.V, wire.Op, wire.JSONRPC == ""
	if e.Method != "" {
		e.Op = e.Method
		e.V = ProtocolVersion
	}
	return nil
}

type OkEnvelope struct {
	JSONRPC string          `json:"jsonrpc,omitempty"`
	ID      string          `json:"id"`
	Result  json.RawMessage `json:"result"`
	OK      bool            `json:"-"`
	legacy  bool
}

func (e OkEnvelope) MarshalJSON() ([]byte, error) {
	result := e.Result
	if len(result) == 0 {
		result = json.RawMessage("{}")
	}
	if e.legacy {
		return json.Marshal(struct {
			ID     string          `json:"id"`
			OK     bool            `json:"ok"`
			Result json.RawMessage `json:"result"`
		}{e.ID, true, result})
	}
	return json.Marshal(struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      string          `json:"id"`
		Result  json.RawMessage `json:"result"`
	}{JSONRPCVersion, e.ID, result})
}

func (e *OkEnvelope) UnmarshalJSON(b []byte) error {
	var wire struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      string          `json:"id"`
		Result  json.RawMessage `json:"result"`
		OK      *bool           `json:"ok"`
	}
	if err := json.Unmarshal(b, &wire); err != nil {
		return err
	}
	e.JSONRPC, e.ID, e.Result, e.OK = wire.JSONRPC, wire.ID, wire.Result, true
	e.legacy = wire.JSONRPC == ""
	return nil
}

type ErrEnvelope struct {
	JSONRPC string `json:"jsonrpc,omitempty"`
	ID      string `json:"id"`
	Err     *Error `json:"-"`
	OK      bool   `json:"-"`
	legacy  bool
}

func (e ErrEnvelope) MarshalJSON() ([]byte, error) {
	if e.Err == nil {
		e.Err = BadRequestError("missing error")
	}
	if e.legacy {
		return json.Marshal(struct {
			ID  string `json:"id"`
			OK  bool   `json:"ok"`
			Err *Error `json:"err"`
		}{e.ID, false, e.Err})
	}
	return json.Marshal(rpcErrorEnvelope{JSONRPC: JSONRPCVersion, ID: e.ID, Error: rpcErrorFor(e.Err)})
}

func (e *ErrEnvelope) UnmarshalJSON(b []byte) error {
	var wire struct {
		JSONRPC string    `json:"jsonrpc"`
		ID      string    `json:"id"`
		Error   *rpcError `json:"error"`
		Err     *Error    `json:"err"`
	}
	if err := json.Unmarshal(b, &wire); err != nil {
		return err
	}
	e.JSONRPC, e.ID, e.OK, e.legacy = wire.JSONRPC, wire.ID, false, wire.JSONRPC == ""
	if wire.Error != nil {
		e.Err = errorFromRPC(*wire.Error)
	} else {
		e.Err = wire.Err
	}
	return nil
}

type VersionInfo struct {
	AdapterVersion   string       `json:"adapterVersion"`
	ProtocolVersion  int          `json:"protocolVersion"`
	ProtocolVersions []int        `json:"protocolVersions,omitempty"`
	AnalyzerVersion  string       `json:"analyzerVersion,omitempty"`
	Capabilities     Capabilities `json:"capabilities,omitempty"`
	SchemaID         string       `json:"schemaId,omitempty"`
	SchemaVersion    int          `json:"schemaVersion,omitempty"`
}

type Capabilities struct {
	Cancellation     bool  `json:"cancellation"`
	Progress         bool  `json:"progress"`
	BatchAck         bool  `json:"batchAck"`
	SnapshotOverlay  bool  `json:"snapshotOverlay"`
	AnalysisMetadata bool  `json:"analysisMetadata"`
	MaxMessageBytes  int64 `json:"maxMessageBytes"`
	MaxInFlight      int   `json:"maxInFlight"`
}

type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

type rpcErrorEnvelope struct {
	JSONRPC string    `json:"jsonrpc"`
	ID      string    `json:"id"`
	Error   *rpcError `json:"error"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      string          `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *rpcError       `json:"error"`
}

func rpcCodeFor(err *Error) int {
	if err == nil {
		return -32603
	}
	switch err.Code {
	case EBadRequest, EUnsupportedVersion:
		return -32602
	case EAdapterInternal, ECrashed:
		return -32603
	default:
		return -32000
	}
}

func rpcErrorFor(err *Error) *rpcError {
	data, _ := json.Marshal(err)
	return &rpcError{Code: rpcCodeFor(err), Message: err.Message, Data: data}
}

func errorFromRPC(err rpcError) *Error {
	message := secret.Redact(err.Message).Text
	if len(err.Data) != 0 && string(err.Data) != "null" {
		var domain Error
		if json.Unmarshal(err.Data, &domain) == nil && IsValidErrorCode(domain.Code) {
			if domain.Message == "" {
				domain.Message = message
			} else {
				domain.Message = secret.Redact(domain.Message).Text
			}
			if detail, ok := domain.Detail.(string); ok {
				domain.Detail = secret.Redact(detail).Text
			}
			return &domain
		}
	}
	switch err.Code {
	case -32602:
		return BadRequestError(message)
	case -32603:
		return AdapterInternalError(message)
	default:
		return AdapterInternalError(message)
	}
}

// ValidateEnvelope validates a JSON-RPC 2.0 body. The legacy branch is kept
// for existing direct fixture helpers. Production framed readers use the
// JSON-RPC branch only.
func ValidateEnvelope(raw []byte) error {
	var probe struct {
		JSONRPC *string          `json:"jsonrpc"`
		ID      *string          `json:"id"`
		Method  *string          `json:"method"`
		Params  json.RawMessage  `json:"params"`
		Result  *json.RawMessage `json:"result"`
		Error   *json.RawMessage `json:"error"`
		V       *int             `json:"v"`
		Op      *string          `json:"op"`
		OK      *bool            `json:"ok"`
		ErrRaw  *json.RawMessage `json:"err"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return BadRequestError(fmt.Sprintf("envelope is not a JSON object: %v", err))
	}
	if probe.JSONRPC == nil {
		return validateLegacyEnvelope(probe.V, probe.ID, probe.Op, probe.Params, probe.OK, probe.Result, probe.ErrRaw)
	}
	if *probe.JSONRPC != JSONRPCVersion {
		return UnsupportedVersionError(fmt.Sprintf("jsonrpc=%q, expected %q", *probe.JSONRPC, JSONRPCVersion))
	}
	if probe.Method != nil {
		if probe.ID == nil {
			return validateNotificationShape(*probe.Method, probe.Params)
		}
		return validateRPCRequestShape(probe.ID, *probe.Method, probe.Params)
	}
	return validateRPCResponseShape(probe.ID, probe.Result, probe.Error)
}

func validateRPCRequestShape(id *string, method string, params json.RawMessage) error {
	if id == nil || *id == "" {
		return BadRequestError("JSON-RPC request missing id")
	}
	if method != OpInitialize && !Ops[method] {
		return BadRequestError(fmt.Sprintf("unknown method %q", method))
	}
	if !isJSONObject(params) {
		return BadRequestError("params must be a JSON object")
	}
	return nil
}

func validateNotificationShape(method string, params json.RawMessage) error {
	switch method {
	case "$/cancelRequest", "$/progress", "codeflow/diagnostic", "codeflow/batchAck":
	default:
		return BadRequestError(fmt.Sprintf("unknown notification %q", method))
	}
	if !isJSONObject(params) {
		return BadRequestError("notification params must be a JSON object")
	}
	return nil
}

func validateRPCResponseShape(id *string, result, rawErr *json.RawMessage) error {
	if id == nil || *id == "" {
		return BadRequestError("JSON-RPC response missing id")
	}
	if (result == nil) == (rawErr == nil) {
		return BadRequestError("JSON-RPC response requires exactly one of result or error")
	}
	if result != nil && !isJSONObject(*result) {
		return BadRequestError("JSON-RPC result must be an object")
	}
	if rawErr != nil {
		var e rpcError
		if err := json.Unmarshal(*rawErr, &e); err != nil || e.Message == "" {
			return BadRequestError("invalid JSON-RPC error object")
		}
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

func validateLegacyEnvelope(v *int, id *string, op *string, params json.RawMessage, ok *bool, result *json.RawMessage, rawErr *json.RawMessage) error {
	if ok != nil {
		if id == nil || *id == "" {
			return BadRequestError("response envelope missing id")
		}
		if *ok {
			return validateResponseShape(id, true, result, nil)
		}
		return validateResponseShape(id, false, nil, rawErr)
	}
	if v == nil {
		return BadRequestError("request envelope missing protocol version")
	}
	if *v != ProtocolVersion {
		return UnsupportedVersionError(fmt.Sprintf("request v=%d, CORE speaks v%d", *v, ProtocolVersion))
	}
	if id == nil || *id == "" || op == nil || *op == "" {
		return BadRequestError("request envelope missing id or op")
	}
	method := rpcMethodForOp(*op)
	if method != OpInitialize && !Ops[method] {
		return BadRequestError(fmt.Sprintf("unknown op %q", *op))
	}
	if !isJSONObject(params) {
		return BadRequestError("params must be a JSON object")
	}
	return nil
}

func ValidateRequest(e *RequestEnvelope) error {
	if e == nil {
		return BadRequestError("nil request envelope")
	}
	method := e.Method
	if method == "" {
		if e.V != 0 && e.V != ProtocolVersion {
			return UnsupportedVersionError(fmt.Sprintf("request v=%d, CORE speaks v%d", e.V, ProtocolVersion))
		}
		method = rpcMethodForOp(e.Op)
	}
	id := e.ID
	return validateRPCRequestShape(&id, method, e.Params)
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
