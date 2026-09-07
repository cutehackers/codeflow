package protocol

import (
	"bufio"
	"context"
	cryptorand "crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"codeflow/internal/contractharness"
	"codeflow/internal/secret"
)

const (
	ModelHostRequestSchemaID                        = "https://codeflow.local/schemas/rflsc.model-host-request.v2.schema.json"
	ModelHostResponseSchemaID                       = "https://codeflow.local/schemas/rflsc.model-host-response.v2.schema.json"
	ModelHostProtocolVersion                        = 2
	ModelHostEnrichMethod                           = "semantic_enrich"
	modelHostReceiptMethod                          = "request_received"
	modelHostSHA256HexLength                        = 64
	modelHostRepositoryWriteCapabilityViolation     = "repository-sentinel-write-capability"
	ModelHostRepositoryWriteAuditCapabilityEnforced = "capability_enforced"
	ModelHostRepositoryWriteAuditAttributed         = "attributed_attempt"
	ModelHostRepositoryWriteAuditIndeterminate      = "indeterminate"
	// ModelHostRuntimePolicyBindingExact means PolicyDigest identifies the
	// exact runtime sandbox profile bytes and the -f/profile/executable
	// arguments Core passed to sandbox-exec. It is not a digest of a shared
	// probe policy.
	ModelHostRuntimePolicyBindingExact = "exact_profile_bytes_and_exec_args"
	// ModelHostProbeScopeSharedDenyBase limits CoreTrustedProbe to the deny
	// base properties shared by the independently generated probe profile. It
	// does not attest the runtime executable's exact profile.
	ModelHostProbeScopeSharedDenyBase = "shared_deny_base"
	// These names are deliberately relative to the private model-host cwd. A
	// child never receives the repository path through argv, env, or a request.
	// Core creates the target and validates its identity before trusting an
	// attempt record.
	modelHostRuntimeRepositoryWriteTargetName    = ".codeflow-model-host-repository-write-target-v1"
	modelHostRuntimeRepositoryWriteChallengeName = ".codeflow-model-host-repository-write-challenge-v1"
)

// ModelHostResourceLimitsVersion identifies the Core-owned resource limit
// contract. CPU and the per-UID process-count defense are applied by the
// child before sandbox-exec starts the configured host process. The sandbox's
// process-fork denial establishes the request-scoped tree bound, and Core
// monitors the direct child's physical memory.
const ModelHostResourceLimitsVersion = 1

const (
	ModelHostResourceEnforcementEnforced    = "enforced"
	ModelHostResourceEnforcementUnsupported = "unsupported"
	ModelHostResourceEnforcementUnavailable = "unavailable"

	// ModelHostResourceBackendDarwinHostTree is the only backend that can
	// establish an enforced request-scoped process-tree bound. Darwin's
	// RLIMIT_NPROC is per-UID and is therefore defense-in-depth only. The
	// sandbox's process-fork denial bounds this host tree to one process.
	ModelHostResourceBackendDarwinHostTree = "darwin.rlimit_cpu_rss.no_fork"
	// ModelHostResourceBackendDarwinRSSWatchdog is retained as a source-level
	// compatibility alias for callers that used the original name. Its value
	// identifies the corrected no-fork host-tree backend.
	ModelHostResourceBackendDarwinRSSWatchdog = ModelHostResourceBackendDarwinHostTree

	defaultModelHostCPUTimeSeconds    uint64 = 60
	defaultModelHostMemoryBytes       uint64 = 1 << 30
	defaultModelHostProcessCount      uint64 = 64
	maxModelHostCPUTimeSeconds        uint64 = 24 * 60 * 60
	maxModelHostMemoryBytes           uint64 = 16 << 30
	maxModelHostProcessCount          uint64 = 4096
	modelHostResourceProcessTreeBound uint64 = 1
)

// ErrModelHostResourceLimit identifies a Core-observed model-host resource
// limit termination. It is intentionally separate from a generic crash or
// request timeout so callers cannot infer enforcement from child output.
var ErrModelHostResourceLimit = errors.New("model host resource limit exceeded")

// ModelHostResourceLimitKind identifies which Core-owned resource boundary
// terminated a supervised model host.
type ModelHostResourceLimitKind string

const (
	ModelHostResourceLimitMemory ModelHostResourceLimitKind = "memory"
	ModelHostResourceLimitCPU    ModelHostResourceLimitKind = "cpu"
)

// ModelHostResourceLimitError is the typed cause attached to the protocol
// failure returned after Core observes a resource-limit termination.
type ModelHostResourceLimitError struct {
	Kind   ModelHostResourceLimitKind
	Detail error
}

func (e *ModelHostResourceLimitError) Error() string {
	if e == nil {
		return ErrModelHostResourceLimit.Error()
	}
	if e.Detail == nil {
		return fmt.Sprintf("model host %s resource limit exceeded", e.Kind)
	}
	return fmt.Sprintf("model host %s resource limit exceeded: %v", e.Kind, e.Detail)
}

func (e *ModelHostResourceLimitError) Unwrap() error {
	if e == nil {
		return nil
	}
	return errors.Join(ErrModelHostResourceLimit, e.Detail)
}

func newModelHostResourceLimitFailure(kind ModelHostResourceLimitKind, detail error) *Error {
	cause := &ModelHostResourceLimitError{Kind: kind, Detail: detail}
	failure := CrashedError(cause.Error())
	failure.cause = cause
	return failure
}

// ModelHostResourceLimits is the typed declaration supplied by Core. The
// values are intentionally finite. ProcessCount is the requested Darwin
// RLIMIT_NPROC defense limit, which is per-UID and is not a host-tree bound.
type ModelHostResourceLimits struct {
	Version        int    `json:"version"`
	CPUTimeSeconds uint64 `json:"cpuTimeSeconds"`
	MemoryBytes    uint64 `json:"memoryBytes"`
	ProcessCount   uint64 `json:"processCount"`
}

// ModelHostResourceLimitEvidence binds the requested limits to the values
// applied by Core and records whether the OS enforcement was established.
// Applied.ProcessCount is the actual request-scoped host-tree bound. On the
// Darwin backend it is always one because the sandbox denies process-fork.
// Declared.ProcessCount remains the per-UID RLIMIT_NPROC defense request.
// Child-reported values are never copied into this structure.
type ModelHostResourceLimitEvidence struct {
	Version           int                     `json:"version"`
	Declared          ModelHostResourceLimits `json:"declared"`
	Applied           ModelHostResourceLimits `json:"applied"`
	EnforcementStatus string                  `json:"enforcementStatus"`
	Backend           string                  `json:"backend"`
}

// DefaultModelHostResourceLimits returns a bounded default suitable for a
// normal local model host. Callers may override it through ModelHostConfig,
// subject to ValidateModelHostResourceLimits.
func DefaultModelHostResourceLimits() ModelHostResourceLimits {
	return ModelHostResourceLimits{
		Version: ModelHostResourceLimitsVersion, CPUTimeSeconds: defaultModelHostCPUTimeSeconds,
		MemoryBytes: defaultModelHostMemoryBytes, ProcessCount: defaultModelHostProcessCount,
	}
}

func (l ModelHostResourceLimits) withDefaults() ModelHostResourceLimits {
	if l.Version == 0 {
		l.Version = ModelHostResourceLimitsVersion
	}
	defaults := DefaultModelHostResourceLimits()
	if l.CPUTimeSeconds == 0 {
		l.CPUTimeSeconds = defaults.CPUTimeSeconds
	}
	if l.MemoryBytes == 0 {
		l.MemoryBytes = defaults.MemoryBytes
	}
	if l.ProcessCount == 0 {
		l.ProcessCount = defaults.ProcessCount
	}
	return l
}

// ValidateModelHostResourceLimits rejects missing, unversioned, or
// unreasonable overrides before a model host process is started.
func ValidateModelHostResourceLimits(limits ModelHostResourceLimits) error {
	if limits.Version != ModelHostResourceLimitsVersion {
		return BadRequestError("model host resource limits have an unsupported version")
	}
	if limits.CPUTimeSeconds == 0 || limits.CPUTimeSeconds > maxModelHostCPUTimeSeconds {
		return BadRequestError("model host CPU time limit is outside the supported bound")
	}
	if limits.MemoryBytes == 0 || limits.MemoryBytes > maxModelHostMemoryBytes {
		return BadRequestError("model host memory limit is outside the supported bound")
	}
	if limits.ProcessCount == 0 || limits.ProcessCount > maxModelHostProcessCount {
		return BadRequestError("model host process-count limit is outside the supported bound")
	}
	return nil
}

func (l ModelHostResourceLimits) valid() bool {
	return ValidateModelHostResourceLimits(l) == nil
}

func (l ModelHostResourceLimits) equal(other ModelHostResourceLimits) bool {
	return l == other
}

// ValidateModelHostResourceLimitEvidence verifies both the typed values and
// the Core-applied status. A measured capability cannot rely on a declared
// value when the applied value differs or enforcement is unavailable.
func ValidateModelHostResourceLimitEvidence(evidence ModelHostResourceLimitEvidence) error {
	if evidence.Version != ModelHostResourceLimitsVersion {
		return BadRequestError("model host resource-limit evidence has an unsupported version")
	}
	if err := ValidateModelHostResourceLimits(evidence.Declared); err != nil {
		return fmt.Errorf("model host declared resource limits: %w", err)
	}
	if err := ValidateModelHostResourceLimits(evidence.Applied); err != nil {
		return fmt.Errorf("model host applied resource limits: %w", err)
	}
	if evidence.EnforcementStatus != ModelHostResourceEnforcementEnforced && evidence.EnforcementStatus != ModelHostResourceEnforcementUnsupported && evidence.EnforcementStatus != ModelHostResourceEnforcementUnavailable {
		return BadRequestError("model host resource-limit evidence has an unknown enforcement status")
	}
	if evidence.EnforcementStatus == ModelHostResourceEnforcementEnforced && evidence.Backend != ModelHostResourceBackendDarwinHostTree {
		return BadRequestError("model host resource-limit evidence has an unsupported enforcement backend")
	}
	if evidence.EnforcementStatus == ModelHostResourceEnforcementEnforced {
		if evidence.Applied.ProcessCount != modelHostResourceProcessTreeBound {
			return BadRequestError("model host applied process-count is not the single-process sandbox tree bound")
		}
		if evidence.Declared.CPUTimeSeconds != evidence.Applied.CPUTimeSeconds || evidence.Declared.MemoryBytes != evidence.Applied.MemoryBytes {
			return BadRequestError("model host applied CPU or memory limit differs from the declared limit")
		}
	}
	return nil
}

func (e ModelHostResourceLimitEvidence) validEnforced() bool {
	return e.EnforcementStatus == ModelHostResourceEnforcementEnforced && e.Backend == ModelHostResourceBackendDarwinHostTree && e.Applied.ProcessCount == modelHostResourceProcessTreeBound && e.Declared.CPUTimeSeconds == e.Applied.CPUTimeSeconds && e.Declared.MemoryBytes == e.Applied.MemoryBytes && ValidateModelHostResourceLimitEvidence(e) == nil
}

func cloneModelHostResourceLimitEvidence(evidence *ModelHostResourceLimitEvidence) *ModelHostResourceLimitEvidence {
	if evidence == nil {
		return nil
	}
	clone := *evidence
	return &clone
}

// ModelHostCapability is reported by a configured host during the handshake.
// A model identifier alone is never treated as evidence of capability. The
// host must declare measured status and the required protocol guarantees.
type ModelHostCapability struct {
	Status            string                   `json:"status"` // measured | unsupported | unavailable
	ModelID           string                   `json:"modelId"`
	Revision          string                   `json:"revision"`
	License           string                   `json:"license"`
	Checksum          string                   `json:"checksum"`
	Runtime           string                   `json:"runtime"`
	DataBoundary      string                   `json:"dataBoundary"`
	Capabilities      []string                 `json:"capabilities,omitempty"`
	SchemaConstrained bool                     `json:"schemaConstrained"`
	Cancellation      bool                     `json:"cancellation"`
	Measured          bool                     `json:"measured"`
	MaxRequestBytes   int64                    `json:"maxRequestBytes"`
	MaxResponseBytes  int64                    `json:"maxResponseBytes"`
	IsolationBackend  string                   `json:"isolationBackend,omitempty"`
	IsolationEnforced bool                     `json:"isolationEnforced,omitempty"`
	IsolationProbe    *ModelHostIsolationProbe `json:"isolationProbe,omitempty"`
	NetworkPolicy     string                   `json:"networkPolicy,omitempty"`
	// PolicyDigest is assigned from the Core-created sandbox policy, never
	// trusted from the child capability response.
	PolicyDigest string `json:"policyDigest,omitempty"`
	// The following fields are populated by Core after initialize. They keep
	// exact runtime policy identity separate from the independently executed
	// trusted probe and are not accepted from the child response.
	RuntimePolicyBinding          string                          `json:"runtimePolicyBinding,omitempty"`
	RuntimePolicySharedBaseDigest string                          `json:"runtimePolicySharedBaseDigest,omitempty"`
	ProbeScope                    string                          `json:"probeScope,omitempty"`
	ProbePolicyDigest             string                          `json:"probePolicyDigest,omitempty"`
	ProbeSharedBaseDigest         string                          `json:"probeSharedBaseDigest,omitempty"`
	ResourceLimits                *ModelHostResourceLimitEvidence `json:"resourceLimits,omitempty"`
}

// ModelHostIsolationProbe is reported by the supervised child after it has
// attempted to access a repository sentinel without receiving a repository
// path through its environment or working directory.
type ModelHostIsolationProbe struct {
	RepositoryReadAttempt  string `json:"repositoryReadAttempt"`            // blocked | allowed
	RepositoryWriteAttempt string `json:"repositoryWriteAttempt"`           // non-mutating test -w result: blocked | allowed
	DisposableWriteAttempt string `json:"disposableWriteAttempt,omitempty"` // bounded disposable-target write probe
	SentinelBeforeDigest   string `json:"sentinelBeforeDigest"`
	SentinelAfterDigest    string `json:"sentinelAfterDigest"`
	SentinelUnchanged      bool   `json:"sentinelUnchanged"`
	NetworkAttempt         string `json:"networkAttempt"` // blocked | allowed
}

// modelHostRuntimeRepositoryWriteAudit is Core-created state for one private
// host cwd. The target is a Core-created FIFO audit endpoint. A matching event
// is conservative attribution from the child to this request, not a syscall
// trace. Core independently checks the repository sentinel digest and the
// trusted sandbox policy, so no repository path is exposed to the child.
type modelHostRuntimeRepositoryWriteAudit struct {
	targetPath    string
	targetReader  *os.File
	challengePath string
	sentinelPath  string
	beforeDigest  string
	challenge     string
	policyDigest  string
	closeOnce     sync.Once
	closeErr      error
}

// Close releases the Core-owned runtime audit endpoint. The setup owns the
// endpoint until it transfers it to a ModelHost, and every owner can safely
// call Close at most once even when cleanup is retried.
func (a *modelHostRuntimeRepositoryWriteAudit) Close() error {
	if a == nil {
		return nil
	}
	a.closeOnce.Do(func() {
		if a.targetReader != nil {
			a.closeErr = a.targetReader.Close()
		}
	})
	return a.closeErr
}

func (s *modelHostIsolationSetup) takeRuntimeRepositoryWriteAudit() *modelHostRuntimeRepositoryWriteAudit {
	if s == nil {
		return nil
	}
	audit := s.runtimeRepositoryWriteAudit
	s.runtimeRepositoryWriteAudit = nil
	return audit
}

func (s *modelHostIsolationSetup) closeRuntimeRepositoryWriteAudit() error {
	if s == nil {
		return nil
	}
	return s.takeRuntimeRepositoryWriteAudit().Close()
}

// ValidateModelHostIsolationProbe accepts only an observed blocked probe with
// an unchanged sentinel. A missing or permissive probe cannot establish the
// repository boundary.
func ValidateModelHostIsolationProbe(probe ModelHostIsolationProbe) error {
	if probe.RepositoryReadAttempt != "blocked" || probe.RepositoryWriteAttempt != "blocked" || probe.NetworkAttempt != "blocked" {
		return errors.New("model host isolation probe did not block repository read, write, and network attempts")
	}
	if probe.DisposableWriteAttempt != "" && probe.DisposableWriteAttempt != "blocked" {
		return errors.New("model host isolation probe permitted a disposable-target write")
	}
	if probe.SentinelBeforeDigest == "" || probe.SentinelAfterDigest == "" || probe.SentinelBeforeDigest != probe.SentinelAfterDigest || !probe.SentinelUnchanged {
		return errors.New("model host isolation probe did not prove sentinel immutability")
	}
	return nil
}

func (c ModelHostCapability) IsMeasured() bool {
	if c.Status != "measured" || !c.Measured || !c.SchemaConstrained || !c.Cancellation || c.MaxRequestBytes <= 0 || c.MaxResponseBytes <= 0 || c.ModelID == "" || c.Revision == "" || c.License == "" || c.Checksum == "" || c.Runtime == "" || c.DataBoundary == "" || !validModelHostPolicyDigest(c.PolicyDigest) || c.ResourceLimits == nil || !c.ResourceLimits.validEnforced() {
		return false
	}
	if c.IsolationBackend == "sandbox-exec" {
		return c.RuntimePolicyBinding == ModelHostRuntimePolicyBindingExact && validModelHostPolicyDigest(c.RuntimePolicySharedBaseDigest) && c.ProbeScope == ModelHostProbeScopeSharedDenyBase && validModelHostPolicyDigest(c.ProbePolicyDigest) && validModelHostPolicyDigest(c.ProbeSharedBaseDigest) && c.ProbePolicyDigest != c.PolicyDigest && c.ProbeSharedBaseDigest == c.RuntimePolicySharedBaseDigest
	}
	return true
}

// ModelHostConfig contains only process-level host configuration. There is no
// repository root field because the host must receive bytes, not a mount.
type ModelHostConfig struct {
	BinPath          string
	Args             []string
	Env              []string
	DisposableRoot   string
	AllowedReadPaths []string
	MaxRequestBytes  int64
	MaxResponseBytes int64
	DefaultTimeout   time.Duration
	ResourceLimits   ModelHostResourceLimits
	// supervisor is intentionally package-private. Production callers can
	// configure the host, but only protocol-owned tests can replace the
	// process/resource boundary.
	supervisor modelHostSupervisor
	// removeWorkDir is package-private so tests can exercise cleanup failures
	// without a process-wide mutable removal hook.
	removeWorkDir func(string) error
}

// ModelHostFactory creates one Core-supervised model host for one enrichment
// operation. Callers must not retain or reuse the returned host after that
// operation has closed it.
type ModelHostFactory func(context.Context) (*ModelHost, error)

// NewModelHostFactory binds a host configuration to the request-scoped
// factory contract. SpawnModelHost remains the only constructor that can mint
// an actual ModelHost authority.
func NewModelHostFactory(cfg ModelHostConfig) ModelHostFactory {
	return func(ctx context.Context) (*ModelHost, error) {
		return SpawnModelHost(ctx, cfg)
	}
}

func (c ModelHostConfig) withDefaults() ModelHostConfig {
	if c.MaxRequestBytes <= 0 {
		c.MaxRequestBytes = DefaultMaxMessageSizeBytes
	}
	if c.MaxResponseBytes <= 0 {
		c.MaxResponseBytes = DefaultMaxMessageSizeBytes
	}
	if c.DefaultTimeout <= 0 {
		c.DefaultTimeout = DefaultCallTimeout
	}
	c.ResourceLimits = c.ResourceLimits.withDefaults()
	return c
}

// ModelHostRequest is the only request body sent to a semantic model host.
// EvidencePack contains a redacted bounded pack serialized by the Core seam.
type ModelHostRequest struct {
	SchemaID          string          `json:"schemaId"`
	SchemaVersion     int             `json:"schemaVersion"`
	RequestID         string          `json:"requestId"`
	Operation         string          `json:"operation"`
	EvidencePack      json.RawMessage `json:"evidencePack,omitempty"`
	PackDigest        string          `json:"packDigest,omitempty"`
	TargetStepID      string          `json:"targetStepId,omitempty"`
	TargetSymbolPath  string          `json:"targetSymbolPath,omitempty"`
	PromptRevision    string          `json:"promptRevision,omitempty"`
	CorrectionAttempt int             `json:"correctionAttempt,omitempty"`
	CorrectionReason  string          `json:"correctionReason,omitempty"`
	CorrectionFor     string          `json:"correctionFor,omitempty"`
	MaxResponseBytes  int64           `json:"maxResponseBytes"`
}

// NewModelHostRequest serializes a typed bounded pack without giving the host
// a reference to the Core's map or repository.
func NewModelHostRequest(requestID, targetStepID, targetSymbolPath, promptRevision, packDigest string, pack any, maxResponseBytes int64) (ModelHostRequest, error) {
	if requestID == "" || targetStepID == "" || targetSymbolPath == "" || packDigest == "" {
		return ModelHostRequest{}, BadRequestError("model host request: request identity, target, and pack digest are required")
	}
	if pack == nil {
		return ModelHostRequest{}, BadRequestError("model host request: evidence pack is required")
	}
	data, err := json.Marshal(pack)
	if err != nil {
		return ModelHostRequest{}, fmt.Errorf("model host request: marshal evidence pack: %w", err)
	}
	if int64(len(data)) > DefaultMaxMessageSizeBytes {
		return ModelHostRequest{}, BackpressureError("model host request: evidence pack exceeds default bound")
	}
	if maxResponseBytes <= 0 {
		maxResponseBytes = DefaultMaxMessageSizeBytes
	}
	return ModelHostRequest{
		SchemaID: ModelHostRequestSchemaID, SchemaVersion: ModelHostProtocolVersion,
		RequestID: requestID, Operation: ModelHostEnrichMethod, EvidencePack: data,
		PackDigest: packDigest, TargetStepID: targetStepID, TargetSymbolPath: targetSymbolPath,
		PromptRevision: promptRevision, MaxResponseBytes: maxResponseBytes,
	}, nil
}

// ModelHostResponse is validated by the semantic layer before any proposal is
// exposed. The protocol layer only transports bounded bytes and status.
type ModelHostResponse struct {
	SchemaID      string              `json:"schemaId"`
	SchemaVersion int                 `json:"schemaVersion"`
	RequestID     string              `json:"requestId"`
	Status        string              `json:"status"` // accepted | rejected | unavailable
	Proposal      json.RawMessage     `json:"proposal,omitempty"`
	Error         string              `json:"error,omitempty"`
	Capability    ModelHostCapability `json:"capability,omitempty"`
}

// MarshalJSON keeps the optional initialize-only capability out of accepted
// enrichment responses. A value-typed struct does not honor omitempty by
// itself, and emitting an empty capability would fail the response schema.
func (r ModelHostResponse) MarshalJSON() ([]byte, error) {
	type responseWire struct {
		SchemaID      string               `json:"schemaId"`
		SchemaVersion int                  `json:"schemaVersion"`
		RequestID     string               `json:"requestId"`
		Status        string               `json:"status"`
		Proposal      json.RawMessage      `json:"proposal,omitempty"`
		Error         string               `json:"error,omitempty"`
		Capability    *ModelHostCapability `json:"capability,omitempty"`
	}
	var capability *ModelHostCapability
	if r.Capability.Status != "" {
		value := r.Capability
		// Runtime/probe policy binding is Core-owned enrichment evidence, not
		// part of the child initialize response contract. Do not accidentally
		// put it on the wire if a caller marshals a Core capability value.
		value.RuntimePolicyBinding = ""
		value.RuntimePolicySharedBaseDigest = ""
		value.ProbeScope = ""
		value.ProbePolicyDigest = ""
		value.ProbeSharedBaseDigest = ""
		capability = &value
	}
	return json.Marshal(responseWire{
		SchemaID: r.SchemaID, SchemaVersion: r.SchemaVersion, RequestID: r.RequestID,
		Status: r.Status, Proposal: r.Proposal, Error: r.Error, Capability: capability,
	})
}

// ModelHostIsolationEvidence proves the host receives a bounded pack in a
// private disposable process environment. PolicyDigest and
// RuntimePolicyBinding identify the exact runtime sandbox profile and its
// invocation. CoreTrustedProbe is intentionally scoped by ProbeScope to the
// shared deny base of a separate probe profile. RepositoryWriteAuditStatus
// keeps the trusted capability proof distinct from a runtime attributed
// attempt or an observation failure.
type ModelHostIsolationEvidence struct {
	SourceDelivery             string   `json:"sourceDelivery"`
	SourceMount                string   `json:"sourceMount"`
	WorkingDirectoryMode       string   `json:"workingDirectoryMode"`
	WorkingDirectoryPermission string   `json:"workingDirectoryPermission"`
	Disposable                 bool     `json:"disposable"`
	RepositoryPathExposed      bool     `json:"repositoryPathExposed"`
	RepositoryWriteCapability  bool     `json:"repositoryWriteCapability"`
	RepositoryWriteAttempts    []string `json:"repositoryWriteAttempts"`
	RepositoryWriteAuditStatus string   `json:"repositoryWriteAuditStatus,omitempty"`
	PackDigest                 string   `json:"packDigest"`
	ReceivedRequestID          string   `json:"receivedRequestId,omitempty"`
	ReceivedPackDigest         string   `json:"receivedPackDigest,omitempty"`
	CapabilityStatus           string   `json:"capabilityStatus"`
	TerminalStatus             string   `json:"terminalStatus"`
	CleanupVerified            bool     `json:"cleanupVerified"`
	IsolationBackend           string   `json:"isolationBackend,omitempty"`
	EnforcementStatus          string   `json:"enforcementStatus,omitempty"`
	RepositoryReadAttempt      string   `json:"repositoryReadAttempt,omitempty"`
	RepositoryWriteAttempt     string   `json:"repositoryWriteAttempt,omitempty"`
	DisposableWriteAttempt     string   `json:"disposableWriteAttempt,omitempty"`
	SentinelBeforeDigest       string   `json:"sentinelBeforeDigest,omitempty"`
	SentinelAfterDigest        string   `json:"sentinelAfterDigest,omitempty"`
	SentinelUnchanged          bool     `json:"sentinelUnchanged,omitempty"`
	NetworkAttempt             string   `json:"networkAttempt,omitempty"`
	NetworkPolicy              string   `json:"networkPolicy,omitempty"`
	// PolicyDigest is the exact Core-created runtime sandbox profile digest.
	// RuntimePolicyBinding records that the digest was bound to the exact
	// profile path and executable arguments passed to sandbox-exec.
	PolicyDigest                  string `json:"policyDigest,omitempty"`
	RuntimePolicyBinding          string `json:"runtimePolicyBinding,omitempty"`
	RuntimePolicySharedBaseDigest string `json:"runtimePolicySharedBaseDigest,omitempty"`
	// Probe metadata is deliberately separate from PolicyDigest. The trusted
	// probe runs a different executable/profile and only proves the shared
	// deny base, never the runtime profile's exact clauses.
	ProbeScope            string                          `json:"probeScope,omitempty"`
	ProbePolicyDigest     string                          `json:"probePolicyDigest,omitempty"`
	ProbeSharedBaseDigest string                          `json:"probeSharedBaseDigest,omitempty"`
	CoreTrustedProbe      bool                            `json:"coreTrustedProbe,omitempty"`
	ResourceLimits        *ModelHostResourceLimitEvidence `json:"resourceLimits,omitempty"`
}

// CoreHostAttestation is an opaque, in-memory proof that a model host was
// created by SpawnModelHost. Its fields intentionally cannot be populated by
// callers outside this package and it is never serialized into enrichment
// state or model-host evidence.
type CoreHostAttestation struct {
	host      *ModelHost
	authority *modelHostAuthority
}

type modelHostAuthority struct{}

// AttestModelHost returns an opaque authority only for an actual supervised
// ModelHost created by SpawnModelHost. Interface implementations and copied
// ModelHost values cannot mint or reuse this authority.
func AttestModelHost(client ModelHostClient) *CoreHostAttestation {
	host, ok := client.(*ModelHost)
	if !ok || host == nil || host.authority == nil || host.identity != host {
		return nil
	}
	return &CoreHostAttestation{host: host, authority: host.authority}
}

// VerifyCoreHostAttestation binds an opaque authority to the exact supervised
// host instance that performed the enrichment call.
func VerifyCoreHostAttestation(client ModelHostClient, attestation *CoreHostAttestation) bool {
	host, ok := client.(*ModelHost)
	return ok && host != nil && host.authority != nil && host.identity == host && attestation != nil && attestation.host == host && attestation.authority == host.authority
}

// ModelHostCoreDisplayID returns an opaque, Core-issued display identity only
// when the attestation is bound to this exact supervised host. The ID is not
// an authority by itself and is intentionally omitted from protocol JSON.
func ModelHostCoreDisplayID(client ModelHostClient, attestation *CoreHostAttestation) string {
	host, ok := client.(*ModelHost)
	if !ok || host == nil || host.authority == nil || host.identity != host || !VerifyCoreHostAttestation(host, attestation) {
		return ""
	}
	return host.displayID
}

// ValidateModelHostIsolationEvidence is the Core-side guard used before a
// host result can be exposed. It rejects any evidence that claims a mounted
// repository or repository write capability.
func ValidateModelHostIsolationEvidence(evidence ModelHostIsolationEvidence) error {
	if evidence.SourceDelivery != "bounded_evidence_pack" || evidence.SourceMount != "not_mounted" || evidence.WorkingDirectoryMode != "process_private_disposable" || !evidence.Disposable || evidence.RepositoryPathExposed || evidence.RepositoryWriteCapability {
		return errors.New("model host isolation evidence does not prove a disposable no-mount boundary")
	}
	if evidence.IsolationBackend == "sandbox-exec" && evidence.CoreTrustedProbe {
		switch evidence.RepositoryWriteAuditStatus {
		case ModelHostRepositoryWriteAuditCapabilityEnforced:
			// The trusted sandbox policy and sentinel digest prove that
			// repository-write capability is denied. Runtime event absence is
			// not itself proof that no attempted syscall occurred.
		case ModelHostRepositoryWriteAuditAttributed:
			return errors.New("model host isolation evidence attributes a repository-write attempt")
		case ModelHostRepositoryWriteAuditIndeterminate:
			return errors.New("model host isolation repository-write audit is indeterminate")
		default:
			return errors.New("model host isolation evidence has no repository-write audit status")
		}
		if err := validateModelHostPolicyEvidenceBinding(evidence); err != nil {
			return err
		}
	}
	if len(evidence.RepositoryWriteAttempts) != 0 {
		return errors.New("model host reported repository-path write attempts")
	}
	if evidence.PackDigest == "" {
		return errors.New("model host isolation evidence has no evidence pack digest")
	}
	if evidence.CapabilityStatus == "measured" || evidence.IsolationBackend != "" || evidence.EnforcementStatus != "" {
		if evidence.IsolationBackend == "" || evidence.EnforcementStatus != "enforced" || evidence.NetworkPolicy != "deny_all" || !validModelHostPolicyDigest(evidence.PolicyDigest) {
			return errors.New("model host isolation evidence has no enforced backend or network policy")
		}
		if evidence.ResourceLimits == nil || !evidence.ResourceLimits.validEnforced() {
			return errors.New("model host isolation evidence has no enforced resource limits")
		}
		if err := ValidateModelHostIsolationProbe(ModelHostIsolationProbe{
			RepositoryReadAttempt: evidence.RepositoryReadAttempt, RepositoryWriteAttempt: evidence.RepositoryWriteAttempt,
			DisposableWriteAttempt: evidence.DisposableWriteAttempt,
			SentinelBeforeDigest:   evidence.SentinelBeforeDigest, SentinelAfterDigest: evidence.SentinelAfterDigest,
			SentinelUnchanged: evidence.SentinelUnchanged, NetworkAttempt: evidence.NetworkAttempt,
		}); err != nil {
			return err
		}
	}
	return nil
}

// validateModelHostPolicyEvidenceBinding keeps the exact runtime policy
// identity separate from the independently generated trusted-probe policy.
// The runtime profile's exact bytes and invocation are represented by
// PolicyDigest plus RuntimePolicyBinding. ProbePolicyDigest identifies only
// the probe profile, while both shared-base digests must agree.
func validateModelHostPolicyEvidenceBinding(evidence ModelHostIsolationEvidence) error {
	if evidence.RuntimePolicyBinding != ModelHostRuntimePolicyBindingExact {
		return errors.New("model host isolation evidence has no exact runtime policy binding")
	}
	if !validModelHostPolicyDigest(evidence.PolicyDigest) || !validModelHostPolicyDigest(evidence.RuntimePolicySharedBaseDigest) {
		return errors.New("model host isolation evidence has an invalid runtime policy digest binding")
	}
	if evidence.ProbeScope != ModelHostProbeScopeSharedDenyBase {
		return errors.New("model host isolation evidence has an invalid trusted-probe scope")
	}
	if !validModelHostPolicyDigest(evidence.ProbePolicyDigest) || !validModelHostPolicyDigest(evidence.ProbeSharedBaseDigest) {
		return errors.New("model host isolation evidence has an invalid trusted-probe policy digest binding")
	}
	if evidence.ProbePolicyDigest == evidence.PolicyDigest {
		return errors.New("model host isolation evidence conflates runtime and trusted-probe policy digests")
	}
	if evidence.ProbeSharedBaseDigest != evidence.RuntimePolicySharedBaseDigest {
		return errors.New("model host isolation evidence trusted-probe shared-base digest differs from runtime")
	}
	return nil
}

// ValidateModelHostPolicyIdentityBinding verifies that the Core-measured
// capability and the isolation evidence describe the same exact runtime and
// trusted-probe policy identities. Consumers that record A3/A10 evidence must
// use this binding in addition to validating each value at its own boundary.
func ValidateModelHostPolicyIdentityBinding(capability ModelHostCapability, evidence ModelHostIsolationEvidence) error {
	if !validModelHostPolicyDigest(capability.PolicyDigest) || !validModelHostPolicyDigest(evidence.PolicyDigest) || capability.PolicyDigest != evidence.PolicyDigest {
		return errors.New("model host capability and isolation policy digests differ")
	}
	if err := validateModelHostCapabilityPolicyBinding(capability); err != nil {
		return err
	}
	if err := validateModelHostPolicyEvidenceBinding(evidence); err != nil {
		return err
	}
	if capability.RuntimePolicyBinding != evidence.RuntimePolicyBinding || capability.RuntimePolicySharedBaseDigest != evidence.RuntimePolicySharedBaseDigest || capability.ProbeScope != evidence.ProbeScope || capability.ProbePolicyDigest != evidence.ProbePolicyDigest || capability.ProbeSharedBaseDigest != evidence.ProbeSharedBaseDigest {
		return errors.New("model host capability and isolation policy identities differ")
	}
	return nil
}

func validateModelHostCapabilityPolicyBinding(capability ModelHostCapability) error {
	if capability.RuntimePolicyBinding != ModelHostRuntimePolicyBindingExact {
		return errors.New("model host capability has no exact runtime policy binding")
	}
	if !validModelHostPolicyDigest(capability.RuntimePolicySharedBaseDigest) {
		return errors.New("model host capability has an invalid runtime policy digest binding")
	}
	if capability.ProbeScope != ModelHostProbeScopeSharedDenyBase {
		return errors.New("model host capability has an invalid trusted-probe scope")
	}
	if !validModelHostPolicyDigest(capability.ProbePolicyDigest) || !validModelHostPolicyDigest(capability.ProbeSharedBaseDigest) {
		return errors.New("model host capability has an invalid trusted-probe policy digest binding")
	}
	if capability.ProbePolicyDigest == capability.PolicyDigest {
		return errors.New("model host capability conflates runtime and trusted-probe policy digests")
	}
	if capability.ProbeSharedBaseDigest != capability.RuntimePolicySharedBaseDigest {
		return errors.New("model host capability trusted-probe shared-base digest differs from runtime")
	}
	return nil
}

// ValidateModelHostIsolationEvidenceForPack is the strict Core-side gate for
// a measured enrichment result. It binds the observed launcher evidence to
// the capability and exact pack that were used for the call. cleanupRequired
// is false while a bounded retry may still be in flight and true before an
// available result can be exposed.
func ValidateModelHostIsolationEvidenceForPack(evidence ModelHostIsolationEvidence, capability ModelHostCapability, packDigest string, cleanupRequired bool) error {
	return validateModelHostIsolationEvidenceForRequest(evidence, capability, "", packDigest, cleanupRequired, true)
}

// ValidateModelHostIsolationEvidenceForRequest additionally binds the
// Core-observed child receipt to the exact request identity. An evidence pack
// digest copied from the outbound request is not sufficient evidence.
func ValidateModelHostIsolationEvidenceForRequest(evidence ModelHostIsolationEvidence, capability ModelHostCapability, requestID, packDigest string, cleanupRequired bool) error {
	return validateModelHostIsolationEvidenceForRequest(evidence, capability, requestID, packDigest, cleanupRequired, true)
}

// ValidateModelHostIsolationEvidenceForLifecycle validates a terminal timeout
// or cancellation where no child receipt is expected. The capability, pack,
// policy, resource, and cleanup bindings remain mandatory. A receipt is still
// required by the request validator for every response or failure response.
func ValidateModelHostIsolationEvidenceForLifecycle(evidence ModelHostIsolationEvidence, capability ModelHostCapability, packDigest string, cleanupRequired bool) error {
	if evidence.TerminalStatus != "timeout" && evidence.TerminalStatus != "cancel" {
		return errors.New("model host lifecycle evidence without a receipt requires timeout or cancel terminal status")
	}
	return validateModelHostIsolationEvidenceForRequest(evidence, capability, "", packDigest, cleanupRequired, false)
}

func validateModelHostIsolationEvidenceForRequest(evidence ModelHostIsolationEvidence, capability ModelHostCapability, requestID, packDigest string, cleanupRequired, requireReceipt bool) error {
	if err := ValidateModelHostIsolationEvidence(evidence); err != nil {
		return err
	}
	if !capability.IsMeasured() {
		return errors.New("model host isolation evidence requires a measured capability")
	}
	if !evidence.CoreTrustedProbe {
		return errors.New("model host isolation evidence is not backed by a Core-trusted probe")
	}
	if packDigest == "" || evidence.PackDigest == "" || evidence.PackDigest != packDigest {
		return errors.New("model host isolation evidence pack digest does not match the request")
	}
	if requireReceipt {
		if evidence.ReceivedRequestID == "" || evidence.ReceivedPackDigest == "" || evidence.ReceivedPackDigest != packDigest {
			return errors.New("model host isolation evidence has no matching child receipt")
		}
		if requestID != "" && evidence.ReceivedRequestID != requestID {
			return errors.New("model host isolation evidence request receipt does not match the request")
		}
	}
	if evidence.CapabilityStatus != capability.Status || evidence.IsolationBackend == "" || evidence.IsolationBackend != capability.IsolationBackend || evidence.EnforcementStatus != "enforced" || !capability.IsolationEnforced || evidence.NetworkPolicy != "deny_all" || capability.NetworkPolicy != "deny_all" || !validModelHostPolicyDigest(capability.PolicyDigest) || !validModelHostPolicyDigest(evidence.PolicyDigest) || evidence.PolicyDigest != capability.PolicyDigest || capability.RuntimePolicyBinding != evidence.RuntimePolicyBinding || capability.RuntimePolicySharedBaseDigest != evidence.RuntimePolicySharedBaseDigest || capability.ProbeScope != evidence.ProbeScope || capability.ProbePolicyDigest != evidence.ProbePolicyDigest || capability.ProbeSharedBaseDigest != evidence.ProbeSharedBaseDigest || capability.IsolationProbe == nil {
		return errors.New("model host isolation evidence is not bound to the measured capability")
	}
	if capability.ResourceLimits == nil || evidence.ResourceLimits == nil || !capability.ResourceLimits.validEnforced() || !evidence.ResourceLimits.validEnforced() || *capability.ResourceLimits != *evidence.ResourceLimits {
		return errors.New("model host isolation evidence is not bound to the enforced resource limits")
	}
	if !sameModelHostIsolationOutcome(*capability.IsolationProbe, ModelHostIsolationProbe{
		RepositoryReadAttempt: evidence.RepositoryReadAttempt, RepositoryWriteAttempt: evidence.RepositoryWriteAttempt,
		SentinelBeforeDigest: evidence.SentinelBeforeDigest, SentinelAfterDigest: evidence.SentinelAfterDigest,
		SentinelUnchanged: evidence.SentinelUnchanged, NetworkAttempt: evidence.NetworkAttempt,
	}) {
		return errors.New("model host isolation evidence does not match the Core-validated probe")
	}
	if evidence.TerminalStatus != "success" && evidence.TerminalStatus != "failure" && evidence.TerminalStatus != "crash" && evidence.TerminalStatus != "timeout" && evidence.TerminalStatus != "cancel" {
		return errors.New("model host isolation evidence has no terminal lifecycle status")
	}
	if cleanupRequired && !evidence.CleanupVerified {
		return errors.New("model host isolation evidence has no verified cleanup")
	}
	return nil
}

// ModelHostClient is the internal semantic enrichment boundary. Production
// uses the concrete *ModelHost returned by SpawnModelHost. Test-only semantic
// helpers may use fakes for pure validation coverage, but the public
// enrichment path requires a Core-owned attestation from that supervised
// host.
type ModelHostClient interface {
	Enrich(context.Context, ModelHostRequest) (ModelHostResponse, error)
	Capability() ModelHostCapability
	IsolationEvidence() ModelHostIsolationEvidence
	Close() error
}

type modelHostFrame struct {
	body []byte
	err  error
}

// ModelHost is a supervised, single-flight model host process.
type ModelHost struct {
	cfg                                 ModelHostConfig
	supervisor                          modelHostSupervisor
	cmd                                 *exec.Cmd
	stdin                               io.WriteCloser
	stdout                              io.ReadCloser
	workDir                             string
	workPerm                            string
	displayID                           string
	runtimeRepositoryWriteAudit         *modelHostRuntimeRepositoryWriteAudit
	runtimeRepositoryWriteAuditObserved bool
	authority                           *modelHostAuthority
	identity                            *ModelHost

	mu               sync.Mutex
	writeMu          sync.Mutex
	callMu           sync.Mutex
	closed           bool
	broken           error
	cap              ModelHostCapability
	evidence         ModelHostIsolationEvidence
	frames           chan modelHostFrame
	waitDone         chan struct{}
	capReady         chan struct{}
	readerDone       chan struct{}
	readerStop       chan struct{}
	memoryStop       chan struct{}
	memoryDone       chan struct{}
	memoryReady      chan struct{}
	memoryReadyErr   error
	resourceLimitErr error
	// resourceLimitClassificationSuppressed is set only for an intentional
	// Core lifecycle close (success, timeout, cancellation, or an explicit
	// Close before a request starts). A crash/EOF path keeps classification
	// enabled until the child has been reaped, so an RLIMIT_CPU SIGKILL cannot
	// be converted to a generic frame EOF while reapProcess is still pending.
	resourceLimitClassificationSuppressed bool
	processReaped                         bool
	processWaitErr                        error
	processGroupGone                      bool
	processGroupErr                       error
	workDirRemoved                        bool
	cleanupErr                            error
	memoryJoined                          bool
	readerJoined                          bool
	capOnce                               sync.Once
	readerStopOnce                        sync.Once
	memoryStopOnce                        sync.Once
	memoryReadyOnce                       sync.Once
	responseBound                         int64
	cleanOnce                             sync.Once
	ids                                   atomic.Uint64
	operationClaim                        atomic.Bool
}

var (
	modelHostTerminateProcessGroup = terminateProcessGroup
	modelHostProcessWait           = func(process *os.Process) (*os.ProcessState, error) { return process.Wait() }
	modelHostLifecycleWaitTimeout  = 3 * time.Second
	modelHostPostIsolationHook     atomic.Value // stores func()
)

func init() {
	modelHostPostIsolationHook.Store(func() {})
}

func runModelHostPostIsolationHook() {
	modelHostPostIsolationHook.Load().(func())()
}

func newModelHostDisplayID() (string, error) {
	var token [16]byte
	if _, err := cryptorand.Read(token[:]); err != nil {
		return "", fmt.Errorf("generate model host display identity: %w", err)
	}
	return "mh-" + hex.EncodeToString(token[:]), nil
}

// SpawnModelHost starts and handshakes with a configured model host. An
// unmeasured or incomplete capability is rejected before enrichment calls.
func SpawnModelHost(ctx context.Context, cfg ModelHostConfig) (*ModelHost, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cfg = cfg.withDefaults()
	if cfg.BinPath == "" {
		return nil, UnsupportedVersionError("model host is not configured")
	}
	if err := ValidateModelHostResourceLimits(cfg.ResourceLimits); err != nil {
		return nil, err
	}
	supervisor := modelHostSupervisorForConfig(cfg)
	configuredBackend := supervisor.ResourceLimitBackend()
	if !validModelHostSupervisorBackend(configuredBackend) {
		return nil, UnsupportedVersionError(fmt.Sprintf("model host resource limiter backend %q is unavailable", configuredBackend))
	}
	workDir, err := os.MkdirTemp(cfg.DisposableRoot, "codeflow-model-host-")
	if err != nil {
		return nil, CrashedError(fmt.Sprintf("create disposable model host cwd: %v", err))
	}
	removeWorkDir := cfg.removeWorkDir
	if removeWorkDir == nil {
		removeWorkDir = os.RemoveAll
	}
	cleanup := func(primary error) error {
		if removeErr := removeWorkDir(workDir); removeErr != nil {
			return errors.Join(primary, fmt.Errorf("remove disposable model host cwd: %w", removeErr))
		}
		return primary
	}
	environment, err := modelHostEnvironment(cfg.Env, workDir)
	if err != nil {
		return nil, cleanup(err)
	}
	isolation, err := prepareModelHostIsolation(cfg, workDir)
	if err != nil {
		return nil, cleanup(err)
	}
	cleanupIsolation := func(primary error) error {
		if auditErr := isolation.closeRuntimeRepositoryWriteAudit(); auditErr != nil {
			primary = errors.Join(primary, fmt.Errorf("close model host runtime repository-write audit: %w", auditErr))
		}
		return cleanup(primary)
	}
	runModelHostPostIsolationHook()
	if err := ctx.Err(); err != nil {
		return nil, cleanupIsolation(err)
	}
	if err := validateModelHostIsolationBeforeStart(isolation); err != nil {
		return nil, cleanupIsolation(UnsupportedVersionError(fmt.Sprintf("model host runtime sandbox profile changed before process start: %v", err)))
	}
	displayID, err := newModelHostDisplayID()
	if err != nil {
		return nil, cleanupIsolation(CrashedError(err.Error()))
	}
	cmd, stdin, stdout, stderr, err := supervisor.Start(isolation.command, isolation.args, environment, workDir, cfg.ResourceLimits)
	if err != nil {
		if cleanupErr := cleanupStartedModelHostProcess(cmd, stdin, stdout, stderr); cleanupErr != nil {
			err = errors.Join(err, cleanupErr)
		}
		return nil, cleanupIsolation(err)
	}
	backend := supervisor.ResourceLimitBackend()
	if !validModelHostSupervisorBackend(backend) || backend != configuredBackend {
		cleanupErr := cleanupStartedModelHostProcess(cmd, stdin, stdout, stderr)
		backendErr := UnsupportedVersionError(fmt.Sprintf("model host resource limiter backend changed from %q to %q", configuredBackend, backend))
		return nil, cleanupIsolation(errors.Join(backendErr, cleanupErr))
	}
	runtimeRepositoryWriteAudit := isolation.takeRuntimeRepositoryWriteAudit()
	appliedResourceLimits := cfg.ResourceLimits
	appliedResourceLimits.ProcessCount = modelHostResourceProcessTreeBound
	resourceEvidence := ModelHostResourceLimitEvidence{
		Version: ModelHostResourceLimitsVersion, Declared: cfg.ResourceLimits, Applied: appliedResourceLimits,
		EnforcementStatus: ModelHostResourceEnforcementEnforced, Backend: backend,
	}
	perm := ""
	if info, statErr := os.Stat(workDir); statErr == nil {
		perm = fmt.Sprintf("%04o", info.Mode().Perm())
	}
	host := &ModelHost{
		cfg: cfg, supervisor: supervisor, cmd: cmd, stdin: stdin, stdout: stdout, workDir: workDir,
		displayID:                   displayID,
		runtimeRepositoryWriteAudit: runtimeRepositoryWriteAudit,
		workPerm:                    perm, frames: make(chan modelHostFrame, 1), waitDone: make(chan struct{}),
		capReady: make(chan struct{}), readerDone: make(chan struct{}), readerStop: make(chan struct{}),
		memoryStop: make(chan struct{}), memoryDone: make(chan struct{}), memoryReady: make(chan struct{}),
		responseBound: cfg.MaxResponseBytes,
		evidence: ModelHostIsolationEvidence{
			SourceMount: "not_mounted", WorkingDirectoryMode: "process_private_disposable",
			WorkingDirectoryPermission: perm, Disposable: true,
			RepositoryPathExposed:      isolation.trustedProbe.RepositoryReadAttempt != "blocked",
			RepositoryWriteCapability:  isolation.trustedProbe.RepositoryWriteAttempt != "blocked",
			RepositoryWriteAttempts:    modelHostRepositoryWriteAttempts(isolation.trustedProbe),
			RepositoryWriteAuditStatus: ModelHostRepositoryWriteAuditCapabilityEnforced,
			IsolationBackend:           isolation.backend, EnforcementStatus: isolation.enforcement,
			NetworkPolicy:                 isolation.networkPolicy,
			PolicyDigest:                  isolation.policyDigest,
			RuntimePolicyBinding:          isolation.runtimePolicyBinding,
			RuntimePolicySharedBaseDigest: isolation.runtimePolicySharedBaseDigest,
			ProbeScope:                    isolation.probeScope,
			ProbePolicyDigest:             isolation.probePolicyDigest,
			ProbeSharedBaseDigest:         isolation.probeSharedBaseDigest,
			RepositoryReadAttempt:         isolation.trustedProbe.RepositoryReadAttempt,
			RepositoryWriteAttempt:        isolation.trustedProbe.RepositoryWriteAttempt,
			DisposableWriteAttempt:        isolation.trustedProbe.DisposableWriteAttempt,
			SentinelBeforeDigest:          isolation.trustedProbe.SentinelBeforeDigest,
			SentinelAfterDigest:           isolation.trustedProbe.SentinelAfterDigest,
			SentinelUnchanged:             isolation.trustedProbe.SentinelUnchanged,
			NetworkAttempt:                isolation.trustedProbe.NetworkAttempt,
			CoreTrustedProbe:              true,
		},
	}
	host.authority = &modelHostAuthority{}
	host.identity = host
	go host.readLoop()
	go host.memoryWatchdog()
	go host.reapProcess()
	go func() { _, _ = io.Copy(io.Discard, stderr) }()
	closeInitializationFailure := func(primary error) (*ModelHost, error) {
		return nil, errors.Join(primary, host.Close())
	}
	// Core must observe the direct child's memory usage at least once before
	// exposing any capability or resource-limit evidence. This is bounded so
	// an unavailable observation fails closed instead of becoming readiness.
	if err := host.waitForMemoryReady(ctx); err != nil {
		if errors.Is(err, ErrTimeout) || errors.Is(err, ErrCancelled) {
			return closeInitializationFailure(err)
		}
		return closeInitializationFailure(host.failResourceLimit(ModelHostResourceLimitMemory, err))
	}
	host.mu.Lock()
	host.evidence.ResourceLimits = cloneModelHostResourceLimitEvidence(&resourceEvidence)
	host.mu.Unlock()

	requestID := host.nextID()
	resp, err := host.call(ctx, ModelHostRequest{
		SchemaID: ModelHostRequestSchemaID, SchemaVersion: ModelHostProtocolVersion,
		RequestID: requestID, Operation: "initialize", MaxResponseBytes: cfg.MaxResponseBytes,
	})
	if err != nil {
		return closeInitializationFailure(err)
	}
	capability, err := decodeModelHostCapability(resp)
	if err != nil {
		return closeInitializationFailure(err)
	}
	// The sandbox policy digest is Core-owned. A child-provided value cannot
	// establish which policy actually governed this host instance.
	capability.PolicyDigest = isolation.policyDigest
	capability.RuntimePolicyBinding = isolation.runtimePolicyBinding
	capability.RuntimePolicySharedBaseDigest = isolation.runtimePolicySharedBaseDigest
	capability.ProbeScope = isolation.probeScope
	capability.ProbePolicyDigest = isolation.probePolicyDigest
	capability.ProbeSharedBaseDigest = isolation.probeSharedBaseDigest
	// Resource limits are also Core-owned. A child-declared resource object
	// cannot establish which values Core applied before exec or monitored.
	capabilityResourceEvidence := resourceEvidence
	capability.ResourceLimits = &capabilityResourceEvidence
	host.mu.Lock()
	host.cap = capability
	host.evidence.CapabilityStatus = capability.Status
	host.mu.Unlock()
	if !capability.IsMeasured() {
		host.capOnce.Do(func() { close(host.capReady) })
		return closeInitializationFailure(UnsupportedVersionError("model host capability is absent or unmeasured"))
	}
	if capability.IsolationBackend != isolation.backend || !capability.IsolationEnforced || capability.IsolationProbe == nil || capability.NetworkPolicy != isolation.networkPolicy || !sameModelHostIsolationOutcome(*capability.IsolationProbe, isolation.trustedProbe) {
		host.capOnce.Do(func() { close(host.capReady) })
		return closeInitializationFailure(UnsupportedVersionError("model host capability does not declare the enforced filesystem boundary"))
	}
	if err := ValidateModelHostIsolationProbe(*capability.IsolationProbe); err != nil {
		host.capOnce.Do(func() { close(host.capReady) })
		return closeInitializationFailure(UnsupportedVersionError("model host filesystem isolation probe was not blocked"))
	}
	responseBound := cfg.MaxResponseBytes
	if capability.MaxResponseBytes > 0 && capability.MaxResponseBytes < responseBound {
		responseBound = capability.MaxResponseBytes
	}
	host.mu.Lock()
	host.responseBound = responseBound
	host.mu.Unlock()
	host.capOnce.Do(func() { close(host.capReady) })
	return host, nil
}

func modelHostEnvironment(env []string, workDir string) ([]string, error) {
	// A model host receives no ambient parent environment. Every variable must
	// be explicitly allowlisted and repository-sensitive variables are rejected
	// instead of silently being forwarded or overwritten.
	allowed := map[string]bool{
		"PATH": true, "LANG": true, "TZ": true,
		"CODEFLOW_MODEL_HOST_HELPER": true, "CODEFLOW_MODEL_HOST_MODE": true,
		"CODEFLOW_MODEL_HOST_DELAY": true, "CODEFLOW_MODEL_HOST_RECORD": true,
		"CODEFLOW_MODEL_HOST_ENV_RECORD": true, "CODEFLOW_MODEL_HOST_MAX_REQUEST": true,
		"CODEFLOW_MODEL_HOST_MAX_RESPONSE": true,
		"CODEFLOW_VS08_MODEL_HOST_HELPER":  true, "CODEFLOW_VS08_MODEL_HOST_MODE": true,
		"CODEFLOW_VS08_MODEL_HOST_DELAY": true, "CODEFLOW_VS08_MODEL_HOST_RECORD": true,
		"CODEFLOW_VS08_MODEL_HOST_RELEASE": true,
	}
	base := make([]string, 0, len(env)+6)
	seen := make(map[string]struct{}, len(env)+6)
	for _, entry := range env {
		key, value, ok := strings.Cut(entry, "=")
		upper := strings.ToUpper(strings.TrimSpace(key))
		if !ok || upper == "" || strings.ContainsAny(key, "=\x00") || strings.ContainsRune(value, '\x00') {
			return nil, BadRequestError("model host environment contains a malformed entry")
		}
		if modelHostSecretEnvironmentKey(upper) {
			return nil, BadRequestError(fmt.Sprintf("model host environment key %q is secret-bearing", key))
		}
		if modelHostRepositoryEnvironmentKey(upper) {
			return nil, BadRequestError(fmt.Sprintf("model host environment key %q exposes a repository path", key))
		}
		if !allowed[upper] && !strings.HasPrefix(upper, "LC_") {
			return nil, BadRequestError(fmt.Sprintf("model host environment key %q is not allowlisted", key))
		}
		if secret.Redact(value).Count > 0 {
			return nil, BadRequestError(fmt.Sprintf("model host environment value for %q is secret-bearing", key))
		}
		if modelHostUnsafeEnvironmentValue(upper, value, workDir) {
			return nil, BadRequestError(fmt.Sprintf("model host environment value for %q is an unsafe absolute path", key))
		}
		if _, exists := seen[upper]; exists {
			return nil, BadRequestError(fmt.Sprintf("model host environment key %q is duplicated", key))
		}
		seen[upper] = struct{}{}
		base = append(base, key+"="+value)
	}
	for _, key := range []string{"HOME", "TMPDIR", "TMP", "TEMP", "PWD", "OLDPWD", "CODEFLOW_ADAPTER_WORKDIR"} {
		base = append(base, key+"="+workDir)
	}
	return base, nil
}

func modelHostSecretEnvironmentKey(key string) bool {
	upper := strings.ToUpper(strings.TrimSpace(key))
	for _, marker := range []string{"SECRET", "TOKEN", "PASSWORD", "PASSWD", "API_KEY", "PRIVATE_KEY", "CREDENTIAL", "AUTHORIZATION", "AUTH_TOKEN"} {
		if upper == marker || strings.Contains(upper, marker) {
			return true
		}
	}
	return false
}

func modelHostUnsafeEnvironmentValue(key, value, workDir string) bool {
	if value == "" {
		return false
	}
	if key == "PATH" {
		for _, entry := range filepath.SplitList(value) {
			if entry == "" || !filepath.IsAbs(entry) || modelHostCredentialPath(entry) || modelHostRepositoryPath(entry) {
				return true
			}
		}
		return false
	}
	if !filepath.IsAbs(value) {
		return false
	}
	if modelHostCredentialPath(value) || modelHostRepositoryPath(value) {
		return true
	}
	clean := filepath.Clean(value)
	return !modelHostPathWithin(clean, filepath.Clean(os.TempDir())) && !modelHostPathWithin(clean, filepath.Clean(workDir))
}

func modelHostRepositoryPath(value string) bool {
	if !filepath.IsAbs(value) {
		return false
	}
	clean := filepath.Clean(value)
	if cwd, err := os.Getwd(); err == nil && (modelHostPathWithin(clean, cwd) || modelHostPathWithin(cwd, clean)) {
		return true
	}
	if resolved, err := filepath.EvalSymlinks(clean); err == nil {
		if cwd, cwdErr := os.Getwd(); cwdErr == nil && (modelHostPathWithin(resolved, cwd) || modelHostPathWithin(cwd, resolved)) {
			return true
		}
		clean = filepath.Clean(resolved)
	}
	lower := strings.ToLower(filepath.ToSlash(clean))
	for _, marker := range []string{"/repository/", "/repo/", "/worktree/", "/workspace/", "/project/"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func modelHostCredentialPath(value string) bool {
	if !filepath.IsAbs(value) {
		return false
	}
	components := strings.Split(strings.ToLower(filepath.ToSlash(filepath.Clean(value))), "/")
	for index, component := range components {
		if component == "" || component == "." || component == ".." {
			continue
		}
		switch component {
		case ".ssh", ".aws", ".azure", ".kube", ".gnupg", ".password-store", "keychains",
			".netrc", ".npmrc", ".pypirc", ".git-credentials", "login.keychain",
			"credentials", "credential", "secrets", "secret", "password", "passwords",
			"token", "tokens", "private_key", "private-key", "id_rsa", "id_ed25519":
			return true
		}
		if strings.HasPrefix(component, ".env") || strings.HasPrefix(component, "credentials.") || strings.HasPrefix(component, "credential.") || strings.HasPrefix(component, "secrets.") || strings.HasPrefix(component, "secret.") || strings.HasPrefix(component, "password.") || strings.HasPrefix(component, "passwords.") || strings.HasPrefix(component, "token.") || strings.HasPrefix(component, "tokens.") || strings.HasPrefix(component, "login.keychain") {
			return true
		}
		if component == ".docker" {
			return true
		}
		if component == ".config" && index+1 < len(components) && components[index+1] == "gcloud" {
			return true
		}
		if strings.HasPrefix(component, ".env") {
			return true
		}
	}
	return false
}

// validateModelHostArguments rejects secret-bearing arguments and every
// absolute path embedded in an argument before the child is started. The
// allowlist for readable files is intentionally independent from argv.
func validateModelHostArguments(args []string, repositoryRoot string) error {
	for _, arg := range args {
		if strings.ContainsRune(arg, '\x00') {
			return BadRequestError("model host argument contains NUL")
		}
		if modelHostSecretArgument(arg) {
			return BadRequestError("model host argument is secret-bearing")
		}
		for _, candidate := range modelHostArgumentPaths(arg) {
			if modelHostArgumentPathUnsafe(candidate, repositoryRoot) {
				return BadRequestError("model host argument exposes a protected path")
			}
		}
	}
	return nil
}

func modelHostSecretArgument(arg string) bool {
	if secret.Redact(arg).Count > 0 {
		return true
	}
	key := arg
	if equals := strings.IndexAny(key, "=:"); equals >= 0 {
		key = key[:equals]
	}
	key = strings.TrimLeft(key, "-")
	for _, component := range strings.FieldsFunc(strings.ToLower(key), func(r rune) bool {
		return r == '-' || r == '_' || r == '.'
	}) {
		switch component {
		case "secret", "token", "password", "passwd", "credential", "credentials", "apikey", "api", "authorization", "privatekey", "auth":
			return true
		}
	}
	return false
}

func modelHostArgumentPaths(arg string) []string {
	paths := make([]string, 0, 1)
	if filepath.IsAbs(arg) {
		paths = append(paths, filepath.Clean(arg))
	}
	for index := 0; index < len(arg); index++ {
		if arg[index] != '/' || (index > 0 && arg[index-1] == '/') {
			continue
		}
		end := index
		for end < len(arg) {
			switch arg[end] {
			case '\x00', ' ', '\t', '\r', '\n', '"', '\'', ',', ';', '|', '&', '(', ')', '[', ']', '{', '}':
				break
			default:
				end++
				continue
			}
			break
		}
		candidate := strings.Trim(arg[index:end], "\"'")
		if filepath.IsAbs(candidate) {
			paths = append(paths, filepath.Clean(candidate))
		}
		index = end
	}
	return paths
}

func modelHostArgumentPathUnsafe(candidate, repositoryRoot string) bool {
	if modelHostCredentialPath(candidate) || modelHostRepositoryPath(candidate) {
		return true
	}
	clean := filepath.Clean(candidate)
	if modelHostPathWithin(clean, repositoryRoot) || modelHostPathWithin(repositoryRoot, clean) {
		return true
	}
	if resolved, ok := modelHostResolveExistingPrefix(clean); ok {
		if modelHostCredentialPath(resolved) || modelHostRepositoryPath(resolved) || modelHostPathWithin(resolved, repositoryRoot) || modelHostPathWithin(repositoryRoot, resolved) {
			return true
		}
	}
	return false
}

func modelHostResolveExistingPrefix(value string) (string, bool) {
	current := filepath.Clean(value)
	suffix := make([]string, 0, 4)
	for {
		if resolved, err := filepath.EvalSymlinks(current); err == nil {
			for index := len(suffix) - 1; index >= 0; index-- {
				resolved = filepath.Join(resolved, suffix[index])
			}
			return filepath.Clean(resolved), true
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", false
		}
		suffix = append(suffix, filepath.Base(current))
		current = parent
	}
}

func sameModelHostIsolationProbe(before, after ModelHostIsolationProbe) bool {
	return before.RepositoryReadAttempt == after.RepositoryReadAttempt &&
		before.RepositoryWriteAttempt == after.RepositoryWriteAttempt &&
		before.DisposableWriteAttempt == after.DisposableWriteAttempt &&
		before.SentinelBeforeDigest == after.SentinelBeforeDigest &&
		before.SentinelAfterDigest == after.SentinelAfterDigest &&
		before.SentinelUnchanged == after.SentinelUnchanged &&
		before.NetworkAttempt == after.NetworkAttempt
}

func modelHostRepositoryWriteAttempts(probe ModelHostIsolationProbe) []string {
	if probe.RepositoryWriteAttempt == "blocked" {
		return []string{}
	}
	return []string{modelHostRepositoryWriteCapabilityViolation}
}

func sameModelHostIsolationOutcome(before, after ModelHostIsolationProbe) bool {
	return before.RepositoryReadAttempt == after.RepositoryReadAttempt &&
		before.RepositoryWriteAttempt == after.RepositoryWriteAttempt &&
		before.SentinelUnchanged == after.SentinelUnchanged &&
		before.NetworkAttempt == after.NetworkAttempt
}

func validModelHostPolicyDigest(value string) bool {
	if !strings.HasPrefix(value, "sha256:") || len(value) != len("sha256:")+modelHostSHA256HexLength {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil
}

func modelHostPathWithin(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func modelHostRepositoryEnvironmentKey(key string) bool {
	upper := strings.ToUpper(strings.TrimSpace(key))
	if upper == "" {
		return true
	}
	for _, marker := range []string{
		"HOME", "PWD", "OLDPWD", "TMPDIR", "TMP", "TEMP", "CODEFLOW_ADAPTER_WORKDIR",
		"REPO_ROOT", "REPOSITORY_ROOT", "WORKTREE_ROOT", "WORKSPACE_ROOT",
		"SOURCE_ROOT", "PROJECT_ROOT", "GIT_DIR", "GIT_WORK_TREE", "GITHUB_WORKSPACE",
		"INIT_CWD", "NPM_CONFIG_LOCAL_PREFIX",
	} {
		if upper == marker || strings.Contains(upper, marker) {
			return true
		}
	}
	return false
}

// NewModelHost is a compatibility name for SpawnModelHost.
func NewModelHost(ctx context.Context, cfg ModelHostConfig) (*ModelHost, error) {
	return SpawnModelHost(ctx, cfg)
}

func (h *ModelHost) nextID() string {
	return fmt.Sprintf("model-host-%06d", h.ids.Add(1))
}

// ClaimOperation grants the one operation owner for this supervised host.
// ModelHost instances are request-scoped and cannot be reused. A failed claim
// never grants ownership, so a caller that loses a reuse race must not close a
// host owned by the operation that won the claim.
func (h *ModelHost) ClaimOperation() bool {
	if h == nil || !h.operationClaim.CompareAndSwap(false, true) {
		return false
	}
	h.mu.Lock()
	closed := h.closed
	h.mu.Unlock()
	return !closed
}

func (h *ModelHost) reapProcess() {
	if h == nil {
		return
	}
	var processState *os.ProcessState
	var waitErr error
	if h.cmd == nil || h.cmd.Process == nil {
		h.mu.Lock()
		h.processReaped = true
		h.processGroupGone = true
		h.processWaitErr = nil
		h.refreshCleanupVerifiedLocked()
		h.mu.Unlock()
	} else {
		processState, waitErr = modelHostProcessWait(h.cmd.Process)
		h.mu.Lock()
		h.cmd.ProcessState = processState
		h.processReaped = waitErr == nil
		h.processWaitErr = waitErr
		if !h.resourceLimitClassificationSuppressed && h.resourceLimitErr == nil && waitErr == nil && modelHostProcessExitedDueToCPUResourceLimit(processState, h.cfg.ResourceLimits.CPUTimeSeconds) {
			h.resourceLimitErr = newModelHostResourceLimitFailure(ModelHostResourceLimitCPU, fmt.Errorf("model host CPU time limit was exceeded"))
			h.broken = h.resourceLimitErr
			h.evidence.TerminalStatus = "failure"
		}
		h.refreshCleanupVerifiedLocked()
		h.mu.Unlock()
	}
	if h.waitDone != nil {
		close(h.waitDone)
	}
}

func (h *ModelHost) refreshCleanupVerifiedLocked() {
	h.evidence.CleanupVerified = h.processReaped && h.processGroupGone && h.workDirRemoved && h.memoryJoined && h.readerJoined && h.cleanupErr == nil
}

func (h *ModelHost) markReaderJoined() {
	if h == nil {
		return
	}
	h.mu.Lock()
	h.readerJoined = true
	h.refreshCleanupVerifiedLocked()
	h.mu.Unlock()
	if h.readerDone != nil {
		close(h.readerDone)
	}
}

func (h *ModelHost) markMemoryJoined() {
	if h == nil {
		return
	}
	h.mu.Lock()
	h.memoryJoined = true
	h.refreshCleanupVerifiedLocked()
	h.mu.Unlock()
	if h.memoryDone != nil {
		close(h.memoryDone)
	}
}

func (h *ModelHost) recordReaderJoined(joined bool) {
	if h == nil {
		return
	}
	h.mu.Lock()
	h.readerJoined = joined
	h.refreshCleanupVerifiedLocked()
	h.mu.Unlock()
}

func (h *ModelHost) recordMemoryJoined(joined bool) {
	if h == nil {
		return
	}
	h.mu.Lock()
	h.memoryJoined = joined
	h.refreshCleanupVerifiedLocked()
	h.mu.Unlock()
}

func (h *ModelHost) readLoop() {
	if h.readerDone != nil {
		defer h.markReaderJoined()
	}
	br := bufio.NewReaderSize(h.stdout, 64<<10)
	first := true
	for {
		bound := h.cfg.MaxResponseBytes
		if !first {
			select {
			case <-h.capReady:
			case <-h.waitDone:
			case <-h.readerStop:
				return
			}
			h.mu.Lock()
			bound = h.responseBound
			h.mu.Unlock()
		}
		body, err := readNextFrame(br, bound)
		if err != nil {
			h.mu.Lock()
			if !h.closed && h.resourceLimitErr == nil {
				h.broken = CrashedError(fmt.Sprintf("model host frame read: %v", err))
			}
			h.mu.Unlock()
			h.deliverFrame(modelHostFrame{err: err})
			return
		}
		first = false
		if !h.deliverFrame(modelHostFrame{body: body}) {
			return
		}
	}
}

// memoryWatchdog observes the direct model process from Core. The sandbox
// denies process-fork, so a direct-child observation covers the entire model
// process tree without running an unsafe supervisor after a multithreaded Go
// fork. The child itself reports no authoritative resource values.
func (h *ModelHost) memoryWatchdog() {
	if h == nil {
		return
	}
	if h.memoryDone != nil {
		defer h.markMemoryJoined()
	}
	if h.cmd == nil || h.cmd.Process == nil {
		h.markMemoryReady(CrashedError("model host memory watchdog has no child process"))
		return
	}
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	pid := h.cmd.Process.Pid
	supervisor := h.supervisor
	if supervisor == nil {
		supervisor = defaultModelHostSupervisor{}
	}
	for {
		select {
		case <-h.waitDone:
			h.markMemoryReady(CrashedError("model host exited before memory readiness"))
			return
		case <-h.memoryStop:
			h.markMemoryReady(CrashedError("model host memory watchdog stopped before readiness"))
			return
		case <-ticker.C:
			if cpuObserver, ok := supervisor.(modelHostCPUTimeObserver); ok {
				cpuUsage, cpuErr := cpuObserver.CPUTimeUsage(pid)
				if cpuErr == nil && cpuUsage >= time.Duration(h.cfg.ResourceLimits.CPUTimeSeconds)*time.Second {
					h.failResourceLimit(ModelHostResourceLimitCPU, fmt.Errorf("model host CPU time limit exceeded: %s >= %ds", cpuUsage, h.cfg.ResourceLimits.CPUTimeSeconds))
					return
				}
			}
			usage, err := supervisor.PhysicalMemoryUsage(pid)
			if err != nil {
				if h.modelProcessAlreadyDone() {
					h.markMemoryReady(CrashedError("model host exited before memory readiness"))
					return
				}
				// Once the first observation has established readiness, an
				// observation error can race a process exit. Give the reaper a
				// bounded opportunity to classify that exit before treating the
				// observation itself as a memory enforcement failure. Before
				// readiness the shorter bound keeps SpawnModelHost fail-closed.
				wait := 50 * time.Millisecond
				select {
				case <-h.memoryReady:
					wait = modelHostLifecycleWaitTimeout
				default:
				}
				timer := time.NewTimer(wait)
				select {
				case <-h.waitDone:
					timer.Stop()
					if resourceErr := h.resourceLimitFailure(); resourceErr != nil {
						return
					}
					h.markMemoryReady(CrashedError("model host exited before memory readiness"))
					return
				case <-h.memoryStop:
					timer.Stop()
					return
				case <-timer.C:
				}
				if h.modelProcessAlreadyDone() {
					h.markMemoryReady(CrashedError("model host exited before memory readiness"))
					return
				}
				h.failResourceLimit(ModelHostResourceLimitMemory, fmt.Errorf("model host physical memory usage could not be observed: %w", err))
				return
			}
			if usage > h.cfg.ResourceLimits.MemoryBytes {
				h.failResourceLimit(ModelHostResourceLimitMemory, fmt.Errorf("model host physical memory limit exceeded: %d > %d", usage, h.cfg.ResourceLimits.MemoryBytes))
				return
			}
			h.markMemoryReady(nil)
		}
	}
}

func (h *ModelHost) modelProcessAlreadyDone() bool {
	if h == nil {
		return true
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.closed || h.processReaped || h.cmd == nil || h.cmd.Process == nil || h.cmd.ProcessState != nil
}

const modelHostMemoryReadinessTimeout = time.Second

func (h *ModelHost) markMemoryReady(err error) {
	if h == nil || h.memoryReady == nil {
		return
	}
	h.memoryReadyOnce.Do(func() {
		h.mu.Lock()
		h.memoryReadyErr = err
		h.mu.Unlock()
		close(h.memoryReady)
	})
}

func (h *ModelHost) waitForMemoryReady(ctx context.Context) error {
	if h == nil || h.memoryReady == nil {
		return CrashedError("model host memory readiness is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	timeout := modelHostMemoryReadinessTimeout
	if h.cfg.DefaultTimeout > 0 && h.cfg.DefaultTimeout < timeout {
		timeout = h.cfg.DefaultTimeout
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-h.memoryReady:
		h.mu.Lock()
		err := h.memoryReadyErr
		h.mu.Unlock()
		return err
	case <-timer.C:
		return CrashedError("model host memory readiness timed out")
	case <-ctx.Done():
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return TimeoutError("model host memory readiness timed out")
		}
		return CancelledError(fmt.Sprintf("model host memory readiness cancelled: %v", ctx.Err()))
	}
}

func (h *ModelHost) failResourceLimit(kind ModelHostResourceLimitKind, err error) error {
	if h == nil {
		return nil
	}
	if err == nil {
		err = errors.New("model host resource limit enforcement failed")
	}
	failure := newModelHostResourceLimitFailure(kind, err)
	h.mu.Lock()
	if h.closed {
		existing := h.resourceLimitErr
		h.mu.Unlock()
		return existing
	}
	if h.resourceLimitErr != nil {
		existing := h.resourceLimitErr
		h.mu.Unlock()
		return existing
	}
	h.resourceLimitErr = failure
	h.broken = failure
	h.evidence.TerminalStatus = "failure"
	h.mu.Unlock()
	h.markMemoryReady(failure)
	if h.stdin != nil {
		_ = h.stdin.Close()
	}
	terminationErr := modelHostTerminateProcessGroup(h.cmd)
	h.mu.Lock()
	// Close retries a failed termination while the child is still live and
	// reports that failure to its caller. Do not retain a stale signal error
	// when the exact child has already exited and the sandbox forbids children.
	h.processGroupGone = h.cmd == nil || h.cmd.Process == nil || terminationErr == nil || h.processReaped
	h.refreshCleanupVerifiedLocked()
	h.mu.Unlock()
	if h.stdout != nil {
		_ = h.stdout.Close()
	}
	return failure
}

// waitForProcessExitClassification gives the reaper a bounded opportunity to
// inspect ProcessState before a pipe EOF or request deadline is converted to a
// terminal error. A child that remains alive is still terminated by Close on
// the caller's path, so this wait cannot make a malicious child unbounded.
func (h *ModelHost) waitForProcessExitClassification() error {
	if h == nil {
		return nil
	}
	if resourceErr := h.resourceLimitFailure(); resourceErr != nil {
		return resourceErr
	}
	if h.waitDone == nil {
		return nil
	}
	timer := time.NewTimer(100 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-h.waitDone:
		return h.resourceLimitFailure()
	case <-timer.C:
		return h.resourceLimitFailure()
	}
}

func (h *ModelHost) resourceLimitFailure() error {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.resourceLimitErr
}

func (h *ModelHost) deliverFrame(frame modelHostFrame) bool {
	if h == nil {
		return false
	}
	// Prefer delivery whenever the capacity-one queue has room. Once the host
	// is closing, a full queue may abort a late frame so the reader cannot be
	// stranded behind a caller that has already selected cancellation/timeout.
	select {
	case h.frames <- frame:
		return true
	default:
	}
	select {
	case h.frames <- frame:
		return true
	case <-h.readerStop:
		return false
	}
}

func (h *ModelHost) call(ctx context.Context, request ModelHostRequest) (ModelHostResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	h.callMu.Lock()
	defer h.callMu.Unlock()
	h.mu.Lock()
	if h.closed {
		err := h.broken
		if err == nil {
			err = CrashedError("model host is closed")
		}
		h.mu.Unlock()
		return ModelHostResponse{}, err
	}
	if h.broken != nil {
		err := h.broken
		h.mu.Unlock()
		return ModelHostResponse{}, err
	}
	h.mu.Unlock()
	if request.RequestID == "" {
		request.RequestID = h.nextID()
	}
	if request.SchemaID == "" {
		request.SchemaID = ModelHostRequestSchemaID
	}
	if request.SchemaVersion == 0 {
		request.SchemaVersion = ModelHostProtocolVersion
	}
	if request.MaxResponseBytes <= 0 {
		request.MaxResponseBytes = h.cfg.MaxResponseBytes
	}
	params, err := json.Marshal(request)
	if err != nil {
		return ModelHostResponse{}, BadRequestError(fmt.Sprintf("marshal model host request: %v", err))
	}
	envelope, err := marshalModelHostEnvelope(request, params)
	if err != nil {
		return ModelHostResponse{}, BadRequestError(fmt.Sprintf("marshal model host envelope: %v", err))
	}
	requestBound := h.requestBound()
	if int64(len(envelope)) > requestBound {
		return ModelHostResponse{}, BackpressureError(fmt.Sprintf("model host request envelope of %d bytes exceeds bound %d", len(envelope), requestBound))
	}
	h.writeMu.Lock()
	err = writeFrameBounded(h.stdin, envelope, requestBound)
	h.writeMu.Unlock()
	if err != nil {
		if resourceErr := h.waitForProcessExitClassification(); resourceErr != nil {
			h.finishTerminalForRequest(request, "failure")
			return ModelHostResponse{}, resourceErr
		}
		h.finishTerminalForRequest(request, "crash")
		if resourceErr := h.resourceLimitFailure(); resourceErr != nil {
			return ModelHostResponse{}, resourceErr
		}
		return ModelHostResponse{}, CrashedError(fmt.Sprintf("model host request write: %v", err))
	}
	waitDone := h.waitDone
	deadline := h.cfg.DefaultTimeout
	if d, ok := ctx.Deadline(); ok {
		deadline = time.Until(d)
	}
	if deadline <= 0 {
		h.finishTerminalForRequest(request, "timeout")
		return ModelHostResponse{}, TimeoutError("model host request deadline expired")
	}
	timer := time.NewTimer(deadline)
	defer timer.Stop()
	for {
		select {
		case <-waitDone:
			waitDone = nil
			if resourceErr := h.resourceLimitFailure(); resourceErr != nil {
				h.finishTerminalForRequest(request, "failure")
				return ModelHostResponse{}, resourceErr
			}
			h.finishTerminalForRequest(request, "crash")
			return ModelHostResponse{}, CrashedError("model host process exited before responding")
		case frame := <-h.frames:
			if frame.err != nil {
				if resourceErr := h.waitForProcessExitClassification(); resourceErr != nil {
					h.finishTerminalForRequest(request, "failure")
					return ModelHostResponse{}, resourceErr
				}
				h.finishTerminalForRequest(request, "crash")
				if resourceErr := h.resourceLimitFailure(); resourceErr != nil {
					return ModelHostResponse{}, resourceErr
				}
				return ModelHostResponse{}, CrashedError(frame.err.Error())
			}
			var rpc struct {
				JSONRPC string          `json:"jsonrpc"`
				ID      string          `json:"id,omitempty"`
				Method  string          `json:"method,omitempty"`
				Params  json.RawMessage `json:"params,omitempty"`
				Result  json.RawMessage `json:"result"`
				Error   json.RawMessage `json:"error"`
			}
			if err := json.Unmarshal(frame.body, &rpc); err != nil || rpc.JSONRPC != JSONRPCVersion {
				h.finishTerminalForRequest(request, "crash")
				return ModelHostResponse{}, CrashedError("model host returned malformed response")
			}
			if rpc.Method == modelHostReceiptMethod {
				var receipt struct {
					RequestID  string `json:"requestId"`
					PackDigest string `json:"packDigest"`
				}
				if rpc.ID != "" || json.Unmarshal(rpc.Params, &receipt) != nil || receipt.RequestID != request.RequestID || receipt.PackDigest != request.PackDigest {
					h.finishTerminalForRequest(request, "crash")
					return ModelHostResponse{}, CrashedError("model host request receipt mismatch")
				}
				h.mu.Lock()
				if h.evidence.ReceivedRequestID != "" {
					h.mu.Unlock()
					h.finishTerminalForRequest(request, "crash")
					return ModelHostResponse{}, CrashedError("model host sent duplicate request receipt")
				}
				h.evidence.ReceivedRequestID = receipt.RequestID
				h.evidence.ReceivedPackDigest = receipt.PackDigest
				h.mu.Unlock()
				continue
			}
			if rpc.ID != request.RequestID {
				h.finishTerminalForRequest(request, "crash")
				return ModelHostResponse{}, CrashedError("model host returned malformed response")
			}
			if request.Operation == OpInitialize {
				if err := contractharness.Validate(ModelHostResponseSchemaID, rpc.Result); err != nil {
					return ModelHostResponse{}, BadRequestError(fmt.Sprintf("model host initialize response schema: %v", err))
				}
			}
			if len(rpc.Error) > 0 && string(rpc.Error) != "null" {
				var detail any
				_ = json.Unmarshal(rpc.Error, &detail)
				return ModelHostResponse{}, AdapterInternalError(detail)
			}
			var response ModelHostResponse
			if err := json.Unmarshal(rpc.Result, &response); err != nil {
				return ModelHostResponse{}, CrashedError(fmt.Sprintf("decode model host response: %v", err))
			}
			if request.Operation == OpInitialize {
				if response.RequestID == "" {
					return ModelHostResponse{}, CrashedError("model host initialize response request identity is empty")
				}
				if response.RequestID != request.RequestID {
					return ModelHostResponse{}, CrashedError("model host initialize response request identity mismatch")
				}
				if response.Status != "ok" {
					return ModelHostResponse{}, CrashedError(fmt.Sprintf("model host initialize response status %q is not ok", response.Status))
				}
			} else if response.RequestID != "" && response.RequestID != request.RequestID {
				return ModelHostResponse{}, CrashedError("model host response request identity mismatch")
			}
			return response, nil
		case <-timer.C:
			if resourceErr := h.waitForProcessExitClassification(); resourceErr != nil {
				h.finishTerminalForRequest(request, "failure")
				return ModelHostResponse{}, resourceErr
			}
			h.finishTerminalForRequest(request, "timeout")
			return ModelHostResponse{}, TimeoutError("model host request timed out")
		case <-ctx.Done():
			if resourceErr := h.waitForProcessExitClassification(); resourceErr != nil {
				h.finishTerminalForRequest(request, "failure")
				return ModelHostResponse{}, resourceErr
			}
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				h.finishTerminalForRequest(request, "timeout")
				return ModelHostResponse{}, TimeoutError("model host request timed out")
			}
			h.finishTerminalForRequest(request, "cancel")
			return ModelHostResponse{}, CancelledError(fmt.Sprintf("model host request cancelled: %v", ctx.Err()))
		}
	}
}

func marshalModelHostEnvelope(request ModelHostRequest, params []byte) ([]byte, error) {
	return json.Marshal(struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      string          `json:"id"`
		Method  string          `json:"method"`
		Params  json.RawMessage `json:"params"`
	}{JSONRPCVersion, request.RequestID, request.Operation, params})
}

func (h *ModelHost) requestBound() int64 {
	bound := h.cfg.MaxRequestBytes
	h.mu.Lock()
	measured := h.cap.MaxRequestBytes
	h.mu.Unlock()
	if measured > 0 && measured < bound {
		bound = measured
	}
	if bound <= 0 {
		return DefaultMaxMessageSizeBytes
	}
	return bound
}

func (h *ModelHost) finishTerminal(mode string) {
	h.mu.Lock()
	if mode != "" {
		h.evidence.TerminalStatus = mode
	}
	h.mu.Unlock()
	_ = h.Close()
}

// finishTerminalForRequest observes the Core-owned runtime write target before
// Close removes the disposable cwd. This ordering also covers timeout,
// cancellation, and malformed/crashed response paths.
func (h *ModelHost) finishTerminalForRequest(request ModelHostRequest, mode string) {
	_ = h.observeRuntimeRepositoryWriteAttempt(request)
	h.finishTerminal(mode)
}

func (h *ModelHost) recordRuntimeRepositoryWriteAttempt(capability bool) {
	if h == nil {
		return
	}
	h.mu.Lock()
	h.evidence.RepositoryWriteAuditStatus = ModelHostRepositoryWriteAuditAttributed
	if capability {
		h.evidence.RepositoryWriteCapability = true
	}
	for _, attempt := range h.evidence.RepositoryWriteAttempts {
		if attempt == modelHostRepositoryWriteCapabilityViolation {
			h.refreshCleanupVerifiedLocked()
			h.mu.Unlock()
			return
		}
	}
	h.evidence.RepositoryWriteAttempts = append(h.evidence.RepositoryWriteAttempts, modelHostRepositoryWriteCapabilityViolation)
	h.refreshCleanupVerifiedLocked()
	h.mu.Unlock()
}

func (h *ModelHost) markRuntimeRepositoryWriteAuditIndeterminate() {
	if h == nil {
		return
	}
	h.mu.Lock()
	if h.evidence.RepositoryWriteAuditStatus != ModelHostRepositoryWriteAuditAttributed {
		h.evidence.RepositoryWriteAuditStatus = ModelHostRepositoryWriteAuditIndeterminate
	}
	h.refreshCleanupVerifiedLocked()
	h.mu.Unlock()
}

func (h *ModelHost) markRuntimeRepositoryWriteAuditObserved() {
	if h == nil {
		return
	}
	h.mu.Lock()
	h.runtimeRepositoryWriteAuditObserved = true
	h.mu.Unlock()
}

// observeRuntimeRepositoryWriteAttempt attributes only an attempt against the
// fixed Core-created target. The child cannot choose an evidence string or a
// source path. Core binds the conservative FIFO event to the current request
// and policy and checks the repository sentinel digest before recording the
// fixed audit. The trusted sandbox probe, rather than event absence, is the
// proof that the child has no repository-write capability.
func (h *ModelHost) observeRuntimeRepositoryWriteAttempt(request ModelHostRequest) error {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	audit := h.runtimeRepositoryWriteAudit
	policyDigest := h.evidence.PolicyDigest
	workDirRemoved := h.workDirRemoved
	h.mu.Unlock()
	if audit == nil {
		h.markRuntimeRepositoryWriteAuditIndeterminate()
		return errors.New("runtime repository-write audit is unavailable")
	}
	if workDirRemoved {
		h.mu.Lock()
		alreadyObserved := h.runtimeRepositoryWriteAuditObserved
		h.mu.Unlock()
		if alreadyObserved {
			return nil
		}
		h.markRuntimeRepositoryWriteAuditIndeterminate()
		return errors.New("runtime repository-write audit work directory was removed before observation")
	}
	if audit.policyDigest == "" || policyDigest == "" || audit.policyDigest != policyDigest {
		h.markRuntimeRepositoryWriteAuditIndeterminate()
		h.markRuntimeRepositoryWriteAuditObserved()
		return errors.New("runtime repository-write audit policy identity mismatch")
	}
	challenge, err := os.ReadFile(audit.challengePath)
	if err != nil || strings.TrimSpace(string(challenge)) != audit.challenge {
		h.markRuntimeRepositoryWriteAuditIndeterminate()
		h.markRuntimeRepositoryWriteAuditObserved()
		if err != nil {
			return fmt.Errorf("runtime repository-write audit challenge: %w", err)
		}
		return errors.New("runtime repository-write audit challenge was changed")
	}
	event, observed, err := readModelHostRuntimeRepositoryWriteEvent(audit.targetReader, audit.challenge+"\x00"+request.RequestID)
	if err != nil {
		h.markRuntimeRepositoryWriteAuditIndeterminate()
		h.markRuntimeRepositoryWriteAuditObserved()
		return fmt.Errorf("read runtime repository-write audit event: %w", err)
	}
	sentinelAfter, err := os.ReadFile(audit.sentinelPath)
	if err != nil {
		h.markRuntimeRepositoryWriteAuditIndeterminate()
		h.markRuntimeRepositoryWriteAuditObserved()
		return fmt.Errorf("read repository sentinel after model-host request: %w", err)
	}
	afterDigestBytes := sha256.Sum256(sentinelAfter)
	afterDigest := "sha256:" + hex.EncodeToString(afterDigestBytes[:])
	if afterDigest != audit.beforeDigest {
		h.recordRuntimeRepositoryWriteAttempt(true)
		h.markRuntimeRepositoryWriteAuditObserved()
		return errors.New("model host changed the repository sentinel")
	}
	if !observed {
		h.markRuntimeRepositoryWriteAuditObserved()
		return nil
	}
	h.recordRuntimeRepositoryWriteAttempt(false)
	h.markRuntimeRepositoryWriteAuditObserved()
	if event != audit.challenge+"\x00"+request.RequestID {
		return errors.New("runtime repository-write audit event identity mismatch")
	}
	return errors.New("model host attempted the repository-write target")
}

func readModelHostRuntimeRepositoryWriteEvent(reader *os.File, expected string) (string, bool, error) {
	if reader == nil {
		return "", false, errors.New("runtime repository-write audit reader is unavailable")
	}
	buffer := make([]byte, len(expected)+1)
	deadline := time.Now().Add(50 * time.Millisecond)
	for {
		count, err := reader.Read(buffer)
		if count > 0 {
			return string(buffer[:count]), true, nil
		}
		if err != nil && !errors.Is(err, syscall.EAGAIN) && !errors.Is(err, syscall.EWOULDBLOCK) {
			return "", false, err
		}
		if time.Now().After(deadline) {
			return "", false, nil
		}
		time.Sleep(time.Millisecond)
	}
}

func decodeModelHostCapability(response ModelHostResponse) (ModelHostCapability, error) {
	if response.Capability.Status == "" {
		return ModelHostCapability{}, BadRequestError("model host initialize response has no declared capability")
	}
	return response.Capability, nil
}

func (h *ModelHost) Enrich(ctx context.Context, request ModelHostRequest) (ModelHostResponse, error) {
	if request.Operation == "" {
		request.Operation = ModelHostEnrichMethod
	}
	if request.RequestID == "" {
		request.RequestID = h.nextID()
	}
	if len(request.EvidencePack) == 0 {
		return ModelHostResponse{}, BadRequestError("model host request has no evidence pack")
	}
	if request.PackDigest == "" {
		return ModelHostResponse{}, BadRequestError("model host request has no evidence pack digest")
	}
	h.mu.Lock()
	h.evidence.SourceDelivery = "bounded_evidence_pack"
	h.evidence.SourceMount = "not_mounted"
	h.evidence.PackDigest = request.PackDigest
	h.evidence.ReceivedRequestID = ""
	h.evidence.ReceivedPackDigest = ""
	h.mu.Unlock()
	response, err := h.call(ctx, request)
	if !errors.Is(err, ErrTimeout) && !errors.Is(err, ErrCancelled) {
		auditErr := h.observeRuntimeRepositoryWriteAttempt(request)
		if auditErr != nil {
			if err == nil {
				err = CrashedError(fmt.Sprintf("model host repository-write audit: %v", auditErr))
			} else {
				err = errors.Join(err, auditErr)
			}
		}
	}
	if !h.receivedReceiptMatches(request) && !errors.Is(err, ErrBackpressure) && !errors.Is(err, ErrTimeout) && !errors.Is(err, ErrCancelled) {
		if resourceErr := h.resourceLimitFailure(); resourceErr != nil {
			h.finishTerminalForRequest(request, "failure")
			return ModelHostResponse{}, resourceErr
		}
		h.finishTerminalForRequest(request, "crash")
		return ModelHostResponse{}, CrashedError("model host did not acknowledge the received request")
	}
	if err != nil {
		h.mu.Lock()
		if h.evidence.TerminalStatus == "" {
			h.evidence.TerminalStatus = "failure"
		}
		h.mu.Unlock()
		_ = h.Close()
		return response, err
	}
	h.mu.Lock()
	h.evidence.TerminalStatus = "success"
	h.mu.Unlock()
	return response, nil
}

func (h *ModelHost) receivedReceiptMatches(request ModelHostRequest) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.evidence.ReceivedRequestID == request.RequestID && h.evidence.ReceivedPackDigest == request.PackDigest
}

func (h *ModelHost) Capability() ModelHostCapability {
	if h == nil {
		return ModelHostCapability{Status: "unavailable"}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	capability := h.cap
	capability.Capabilities = append([]string(nil), h.cap.Capabilities...)
	capability.IsolationProbe = nil
	if h.cap.IsolationProbe != nil {
		probe := *h.cap.IsolationProbe
		capability.IsolationProbe = &probe
	}
	capability.ResourceLimits = cloneModelHostResourceLimitEvidence(h.cap.ResourceLimits)
	return capability
}

func (h *ModelHost) IsolationEvidence() ModelHostIsolationEvidence {
	if h == nil {
		return ModelHostIsolationEvidence{}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	ev := h.evidence
	ev.RepositoryWriteAttempts = append([]string{}, ev.RepositoryWriteAttempts...)
	ev.ResourceLimits = cloneModelHostResourceLimitEvidence(h.evidence.ResourceLimits)
	return ev
}

// ReadDisposableFile reads a relative observation file created in the
// model-host working directory. It never permits a caller to escape that
// directory and is intended for bounded launcher evidence collection before
// terminal cleanup.
func (h *ModelHost) ReadDisposableFile(name string) ([]byte, error) {
	if h == nil || h.workDir == "" || filepath.IsAbs(name) {
		return nil, BadRequestError("model host observation path is invalid")
	}
	clean := filepath.Clean(name)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return nil, BadRequestError("model host observation path escapes disposable directory")
	}
	return os.ReadFile(filepath.Join(h.workDir, clean))
}

// WriteDisposableFile creates a bounded control file inside the model-host
// working directory. It is limited to relative paths so the caller cannot
// turn the host's disposable directory into an arbitrary write primitive.
func (h *ModelHost) WriteDisposableFile(name string, data []byte) error {
	if h == nil || h.workDir == "" || filepath.IsAbs(name) {
		return BadRequestError("model host observation path is invalid")
	}
	clean := filepath.Clean(name)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return BadRequestError("model host observation path escapes disposable directory")
	}
	if int64(len(data)) > DefaultMaxMessageSizeBytes {
		return BackpressureError("model host observation file exceeds bound")
	}
	return os.WriteFile(filepath.Join(h.workDir, clean), data, 0o600)
}

func (h *ModelHost) cleanupWorkDir() error {
	if h == nil {
		return nil
	}
	h.cleanOnce.Do(func() {
		h.mu.Lock()
		audit := h.runtimeRepositoryWriteAudit
		h.mu.Unlock()
		var cleanupErr error
		if audit != nil {
			if err := audit.Close(); err != nil {
				cleanupErr = fmt.Errorf("close model host runtime repository-write audit: %w", err)
			}
		}
		removed := h.workDir == ""
		if !removed {
			err := os.RemoveAll(h.workDir)
			_, statErr := os.Stat(h.workDir)
			removed = err == nil && errors.Is(statErr, os.ErrNotExist)
		}
		h.mu.Lock()
		h.cleanupErr = cleanupErr
		h.workDirRemoved = removed
		h.refreshCleanupVerifiedLocked()
		h.mu.Unlock()
	})
	h.mu.Lock()
	cleanupErr := h.cleanupErr
	h.mu.Unlock()
	return cleanupErr
}

func (h *ModelHost) stopReader() {
	if h == nil || h.readerStop == nil {
		return
	}
	h.readerStopOnce.Do(func() { close(h.readerStop) })
}

func (h *ModelHost) stopMemoryWatchdog() {
	if h == nil || h.memoryStop == nil {
		return
	}
	h.memoryStopOnce.Do(func() { close(h.memoryStop) })
}

func (h *ModelHost) waitMemoryWatchdog(timeout time.Duration) error {
	if h == nil {
		return nil
	}
	if h.memoryDone == nil {
		h.recordMemoryJoined(true)
		return nil
	}
	if timeout <= 0 {
		timeout = modelHostLifecycleWaitTimeout
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-h.memoryDone:
		h.recordMemoryJoined(true)
		return nil
	case <-timer.C:
		h.recordMemoryJoined(false)
		return CrashedError("model host memory watchdog did not terminate before close deadline")
	}
}

func (h *ModelHost) waitReader(timeout time.Duration) error {
	if h == nil {
		return nil
	}
	if h.readerDone == nil {
		h.recordReaderJoined(true)
		return nil
	}
	if timeout <= 0 {
		timeout = modelHostLifecycleWaitTimeout
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-h.readerDone:
		h.recordReaderJoined(true)
		return nil
	case <-timer.C:
		h.recordReaderJoined(false)
		return CrashedError("model host reader did not terminate before close deadline")
	}
}

func (h *ModelHost) Close() error {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	alreadyClosed := h.closed
	if !alreadyClosed {
		h.closed = true
		if h.evidence.TerminalStatus == "" {
			h.evidence.TerminalStatus = "closed"
			h.resourceLimitClassificationSuppressed = true
		}
		switch h.evidence.TerminalStatus {
		case "success", "timeout", "cancel", "closed":
			h.resourceLimitClassificationSuppressed = true
		}
	}
	if h.cmd == nil || h.cmd.Process == nil {
		h.processReaped = true
		h.processGroupGone = h.processGroupErr == nil
	}
	if h.readerDone == nil {
		h.readerJoined = true
	}
	if h.memoryDone == nil {
		h.memoryJoined = true
	}
	h.refreshCleanupVerifiedLocked()
	h.mu.Unlock()
	h.stopReader()
	h.stopMemoryWatchdog()
	var closeErrors []error
	if !alreadyClosed {
		if h.stdin != nil {
			_ = h.stdin.Close()
		}
		h.mu.Lock()
		processAlreadyReaped := h.processReaped
		h.mu.Unlock()
		if !processAlreadyReaped {
			terminationErr := modelHostTerminateProcessGroup(h.cmd)
			h.mu.Lock()
			if terminationErr != nil {
				h.processGroupErr = errors.Join(h.processGroupErr, terminationErr)
			}
			h.processGroupGone = h.cmd == nil || h.cmd.Process == nil || h.processGroupErr == nil
			h.refreshCleanupVerifiedLocked()
			h.mu.Unlock()
			if terminationErr != nil {
				closeErrors = append(closeErrors, fmt.Errorf("terminate model host process group: %w", terminationErr))
			}
		} else {
			h.mu.Lock()
			h.processGroupGone = h.processGroupErr == nil
			h.refreshCleanupVerifiedLocked()
			h.mu.Unlock()
		}
		if h.stdout != nil {
			_ = h.stdout.Close()
		}
	}
	if h.waitDone != nil {
		timeout := modelHostLifecycleWaitTimeout
		if timeout <= 0 {
			timeout = 3 * time.Second
		}
		timer := time.NewTimer(timeout)
		select {
		case <-h.waitDone:
			h.mu.Lock()
			waitErr := h.processWaitErr
			processReaped := h.processReaped
			h.mu.Unlock()
			if waitErr != nil {
				closeErrors = append(closeErrors, fmt.Errorf("wait for model host process: %w", waitErr))
			}
			if !processReaped {
				closeErrors = append(closeErrors, errors.New("model host process was not reaped"))
			}
		case <-timer.C:
			waitErr := CrashedError("model host process did not terminate before close deadline")
			h.mu.Lock()
			h.processReaped = false
			h.processWaitErr = waitErr
			h.refreshCleanupVerifiedLocked()
			h.mu.Unlock()
			closeErrors = append(closeErrors, waitErr)
		}
		timer.Stop()
	} else if h.cmd != nil && h.cmd.Process != nil {
		closeErrors = append(closeErrors, errors.New("model host process wait is unavailable"))
		h.mu.Lock()
		h.processReaped = false
		h.refreshCleanupVerifiedLocked()
		h.mu.Unlock()
	}
	if err := h.waitMemoryWatchdog(modelHostLifecycleWaitTimeout); err != nil {
		closeErrors = append(closeErrors, err)
	}
	if err := h.cleanupWorkDir(); err != nil {
		closeErrors = append(closeErrors, err)
	}
	if err := h.waitReader(modelHostLifecycleWaitTimeout); err != nil {
		closeErrors = append(closeErrors, err)
	}
	h.mu.Lock()
	h.refreshCleanupVerifiedLocked()
	cleanupVerified := h.evidence.CleanupVerified
	groupErr := h.processGroupErr
	h.mu.Unlock()
	if groupErr != nil && len(closeErrors) == 0 {
		closeErrors = append(closeErrors, fmt.Errorf("model host process group cleanup was not verified: %w", groupErr))
	}
	if !cleanupVerified && len(closeErrors) == 0 {
		closeErrors = append(closeErrors, errors.New("model host cleanup could not be verified"))
	}
	return errors.Join(closeErrors...)
}
