// Package rflscvs02 contains the R2 VS-02 snapshot/analyzer boundary.  The
// package is deliberately independent of any language parser.  It owns the
// immutable input envelope, semantic checks on analyzer output, and evidence
// extraction from the bytes that were actually supplied to the analyzer.
package rflscvs02

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"codeflow/internal/secret"
	"codeflow/internal/workspace"
)

const (
	AnalyzerRequestSchemaID  = "https://codeflow.local/schemas/rflsc.analyzer-request.v2.schema.json"
	AnalyzerResultSchemaID   = "https://codeflow.local/schemas/rflsc.analyzer-result.v2.schema.json"
	ReadSetSchemaID          = "https://codeflow.local/schemas/rflsc.analysis-read-set.v2.schema.json"
	ClosureSchemaID          = "https://codeflow.local/schemas/rflsc.observation-closure.v2.schema.json"
	EvidenceSchemaID         = "https://codeflow.local/schemas/rflsc.evidence.v2.schema.json"
	CapabilityMatrixSchemaID = "https://codeflow.local/schemas/rflsc.adapter-capability-matrix.v1.schema.json"
	DiagnosticSchemaID       = "https://codeflow.local/schemas/rflsc.adapter-diagnostic.v2.schema.json"
	SchemaVersion            = 2
	// This must match the core transport and every production language adapter.
	// A smaller adapter declaration rejects the connection during capability
	// negotiation before Live Semantic Map analysis can start.
	DefaultMaxMessageBytes = int64(128 << 20)
	MaxDiagnosticBytes     = 512
)

// SnapshotDocument is a defensive copy of one document in the selected VS-01
// snapshot. Bytes are the only source used by evidence extraction.
type SnapshotDocument struct {
	Path            string `json:"path"`
	RevisionID      string `json:"documentRevisionId"`
	ContentID       string `json:"contentId"`
	DocumentVersion int    `json:"documentVersion"`
	ByteLength      int    `json:"byteLength"`
	Bytes           []byte `json:"content,omitempty"`
}

// SnapshotInput is the immutable snapshot basis handed to an adapter.
type SnapshotInput struct {
	SnapshotID               string                     `json:"snapshotId"`
	WorkspaceEpoch           int64                      `json:"workspaceEpoch"`
	ComputedBasisID          string                     `json:"computedBasisId"`
	RootTreeID               string                     `json:"rootTreeId"`
	ConfigurationFingerprint string                     `json:"configurationFingerprint,omitempty"`
	DependencyFingerprint    string                     `json:"dependencyFingerprint"`
	Documents                []SnapshotDocument         `json:"documents"`
	SourceWriteAudit         workspace.SourceWriteAudit `json:"repositoryPathWriteAudit"`
}

// SnapshotInputFromLease copies every selected document and identity field
// from a VS-01 lease. The lease must expose the complete document inventory.
func SnapshotInputFromLease(lease workspace.SnapshotLease) (SnapshotInput, error) {
	if lease == nil {
		return SnapshotInput{}, errors.New("snapshot lease is required")
	}
	documents := lease.Documents()
	sort.Slice(documents, func(i, j int) bool { return documents[i].Path < documents[j].Path })
	input := SnapshotInput{
		SnapshotID:               lease.SnapshotID(),
		WorkspaceEpoch:           lease.WorkspaceEpoch(),
		ComputedBasisID:          lease.ComputedBasisID(),
		RootTreeID:               lease.RootTreeID(),
		ConfigurationFingerprint: lease.ConfigurationFingerprint(),
		Documents:                make([]SnapshotDocument, 0, len(documents)),
		SourceWriteAudit:         lease.SourceWriteAudit(),
	}
	if input.SnapshotID == "" || input.ComputedBasisID == "" || input.RootTreeID == "" || input.WorkspaceEpoch < 0 {
		return SnapshotInput{}, errors.New("snapshot lease has incomplete immutable identity")
	}
	input.DependencyFingerprint = dependencyFingerprint(input.RootTreeID, input.ConfigurationFingerprint)
	for _, meta := range documents {
		if !validRelativePath(meta.Path) || meta.ContentID == "" || meta.RevisionID == "" {
			return SnapshotInput{}, fmt.Errorf("snapshot document has invalid identity: %+v", meta)
		}
		data, err := lease.ReadFile(meta.Path)
		if err != nil {
			return SnapshotInput{}, fmt.Errorf("read snapshot document %s: %w", meta.Path, err)
		}
		data = append([]byte(nil), data...)
		if digestBytes(data) != meta.ContentID {
			return SnapshotInput{}, fmt.Errorf("snapshot document %s content digest mismatch", meta.Path)
		}
		if meta.ByteLength != 0 && meta.ByteLength != len(data) {
			return SnapshotInput{}, fmt.Errorf("snapshot document %s byte length mismatch", meta.Path)
		}
		input.Documents = append(input.Documents, SnapshotDocument{
			Path: meta.Path, RevisionID: meta.RevisionID, ContentID: meta.ContentID,
			DocumentVersion: meta.DocumentVersion, ByteLength: len(data), Bytes: data,
		})
	}
	if input.SourceWriteAudit.CapturedSnapshotTreeDigest == "" {
		input.SourceWriteAudit.CapturedSnapshotTreeDigest = input.RootTreeID
	}
	return input, nil
}

// SnapshotInputFromContent adapts an already captured protocol content map to
// the analyzer contract. Callers must provide the identities captured with
// those bytes. This is a conversion seam, not a source reader.
func SnapshotInputFromContent(snapshotID, basis, rootTree, configuration, dependency string, epoch int64, files map[string]string) (SnapshotInput, error) {
	if snapshotID == "" {
		snapshotID = basis
	}
	if rootTree == "" {
		rootTree = basis
	}
	if dependency == "" {
		dependency = dependencyFingerprint(rootTree, configuration)
	}
	if snapshotID == "" || basis == "" || rootTree == "" || dependency == "" || epoch < 0 {
		return SnapshotInput{}, errors.New("captured snapshot identity is incomplete")
	}
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	documents := make([]SnapshotDocument, 0, len(paths))
	for _, path := range paths {
		if !validRelativePath(path) {
			return SnapshotInput{}, fmt.Errorf("captured snapshot path is invalid: %s", path)
		}
		bytes := []byte(files[path])
		contentID := digestBytes(bytes)
		documents = append(documents, SnapshotDocument{
			Path: path, RevisionID: "rev-" + contentID, ContentID: contentID,
			DocumentVersion: 1, ByteLength: len(bytes), Bytes: append([]byte(nil), bytes...),
		})
	}
	return SnapshotInput{
		SnapshotID: snapshotID, WorkspaceEpoch: epoch, ComputedBasisID: basis,
		RootTreeID: rootTree, ConfigurationFingerprint: configuration,
		DependencyFingerprint: dependency, Documents: documents,
		SourceWriteAudit: workspace.SourceWriteAudit{CapturedSnapshotTreeDigest: rootTree},
	}, nil
}

func dependencyFingerprint(rootTree, configuration string) string {
	return digestBytes([]byte(rootTree + "\x00" + configuration))
}

func digestBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func validRelativePath(path string) bool {
	if path == "" || strings.HasPrefix(path, "/") || strings.Contains(path, "\\") {
		return false
	}
	clean := strings.TrimPrefix(path, "./")
	if clean == "" || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || strings.Contains(clean, "/../") {
		return false
	}
	return clean == path
}

func (s SnapshotInput) Document(path string) (SnapshotDocument, bool) {
	for _, doc := range s.Documents {
		if doc.Path == path {
			copy := doc
			copy.Bytes = append([]byte(nil), doc.Bytes...)
			return copy, true
		}
	}
	return SnapshotDocument{}, false
}

// AnalyzerRequest is the v2 protocol payload. Snapshot files are duplicated
// as a map for simple adapters, while Documents retain revision/read identity.
type AnalyzerRequest struct {
	SchemaID             string         `json:"schemaId"`
	SchemaVersion        int            `json:"schemaVersion"`
	RequestID            string         `json:"requestId"`
	Operation            string         `json:"operation"`
	Snapshot             SnapshotInput  `json:"snapshot"`
	TaskScope            []string       `json:"taskScope,omitempty"`
	RequiredObservations []string       `json:"requiredObservations,omitempty"`
	CapabilityRequest    []string       `json:"capabilityRequest,omitempty"`
	MaxMessageBytes      int64          `json:"maxMessageBytes"`
	Payload              map[string]any `json:"payload,omitempty"`
}

func NewAnalyzerRequest(requestID, operation string, snapshot SnapshotInput, taskScope, requiredObservations []string) (AnalyzerRequest, error) {
	if strings.TrimSpace(requestID) == "" || strings.TrimSpace(operation) == "" {
		return AnalyzerRequest{}, errors.New("requestId and operation are required")
	}
	if snapshot.SnapshotID == "" || snapshot.ComputedBasisID == "" || snapshot.RootTreeID == "" || snapshot.DependencyFingerprint == "" {
		return AnalyzerRequest{}, errors.New("snapshot identity is required")
	}
	return AnalyzerRequest{
		SchemaID: AnalyzerRequestSchemaID, SchemaVersion: SchemaVersion, RequestID: requestID, Operation: operation,
		Snapshot: cloneSnapshotInput(snapshot), TaskScope: append([]string(nil), taskScope...),
		RequiredObservations: append([]string(nil), requiredObservations...), MaxMessageBytes: DefaultMaxMessageBytes,
	}, nil
}

func cloneSnapshotInput(in SnapshotInput) SnapshotInput {
	out := in
	out.Documents = make([]SnapshotDocument, len(in.Documents))
	for i, doc := range in.Documents {
		out.Documents[i] = doc
		out.Documents[i].Bytes = append([]byte(nil), doc.Bytes...)
	}
	out.SourceWriteAudit.RepositoryPathWrites = append([]string(nil), in.SourceWriteAudit.RepositoryPathWrites...)
	return out
}

func (r AnalyzerRequest) Params() map[string]any {
	files := make(map[string]string, len(r.Snapshot.Documents))
	documents := make([]map[string]any, 0, len(r.Snapshot.Documents))
	for _, doc := range r.Snapshot.Documents {
		files[doc.Path] = string(doc.Bytes)
		documents = append(documents, map[string]any{
			"path": doc.Path, "documentRevisionId": doc.RevisionID, "contentId": doc.ContentID,
			"documentVersion": doc.DocumentVersion, "contentHash": doc.ContentID, "byteLength": len(doc.Bytes),
		})
	}
	snapshot := map[string]any{
		"schemaId": AnalyzerRequestSchemaID, "schemaVersion": SchemaVersion,
		"snapshotId": r.Snapshot.SnapshotID, "workspaceEpoch": r.Snapshot.WorkspaceEpoch,
		"computedBasisId": r.Snapshot.ComputedBasisID, "rootTreeId": r.Snapshot.RootTreeID,
		"configurationFingerprint": r.Snapshot.ConfigurationFingerprint,
		"dependencyFingerprint":    r.Snapshot.DependencyFingerprint,
		"documents":                documents, "files": files,
		"repositoryPathWriteAudit": r.Snapshot.SourceWriteAudit,
	}
	params := map[string]any{
		"schemaId": r.SchemaID, "schemaVersion": r.SchemaVersion, "requestId": r.RequestID,
		"operation": r.Operation, "snapshot": snapshot, "maxMessageBytes": r.MaxMessageBytes,
		// repoRoot is intentionally absent. Adapters receive protocol content only.
	}
	if len(r.TaskScope) > 0 {
		params["taskScope"] = append([]string(nil), r.TaskScope...)
	}
	if len(r.RequiredObservations) > 0 {
		params["requiredObservations"] = append([]string(nil), r.RequiredObservations...)
	}
	if len(r.CapabilityRequest) > 0 {
		params["capabilityRequest"] = append([]string(nil), r.CapabilityRequest...)
	}
	if len(r.Payload) > 0 {
		params["payload"] = cloneAnyMap(r.Payload)
	}
	return params
}

func cloneAnyMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

// ReadDocument is the measured read-set form of a snapshot document.
type ReadDocument struct {
	Path               string `json:"path"`
	DocumentRevisionID string `json:"documentRevisionId,omitempty"`
	ContentID          string `json:"contentId,omitempty"`
	ContentHash        string `json:"contentHash"`
	DocumentVersion    int    `json:"documentVersion,omitempty"`
	ByteLength         int    `json:"byteLength"`
}

type Observation struct {
	Kind      string `json:"kind"`
	Path      string `json:"path"`
	ValueHash string `json:"valueHash,omitempty"`
	Detail    string `json:"detail,omitempty"`
	Measured  bool   `json:"measured"`
}

type AnalysisReadSet struct {
	SchemaID               string         `json:"schemaId"`
	SchemaVersion          int            `json:"schemaVersion"`
	ReadSetID              string         `json:"readSetId"`
	ComputedBasisID        string         `json:"computedBasisId"`
	WorkspaceEpoch         int64          `json:"workspaceEpoch"`
	Documents              []ReadDocument `json:"documents"`
	NegativeObservations   []Observation  `json:"negativeObservations"`
	MembershipObservations []Observation  `json:"membershipObservations"`
	DependencyFrontiers    []Observation  `json:"dependencyFrontiers"`
	ReadSetDigest          string         `json:"readSetDigest,omitempty"`
}

type ObservationClosure struct {
	SchemaID               string        `json:"schemaId"`
	SchemaVersion          int           `json:"schemaVersion"`
	ClosureID              string        `json:"closureId"`
	AnalysisReadSetID      string        `json:"analysisReadSetId"`
	ComputedBasisID        string        `json:"computedBasisId"`
	WorkspaceEpoch         int64         `json:"workspaceEpoch"`
	Status                 string        `json:"closureStatus"`
	NegativeObservations   []Observation `json:"negativeObservations"`
	MembershipObservations []Observation `json:"membershipObservations"`
	DependencyFrontiers    []Observation `json:"dependencyFrontiers"`
	RequiredObservations   []string      `json:"requiredObservations,omitempty"`
	MeasuredObservations   []string      `json:"measuredObservations,omitempty"`
	IncompleteReasons      []string      `json:"incompleteReasons,omitempty"`
	ClosureDigest          string        `json:"closureDigest,omitempty"`
}

type Coverage struct {
	IncludedSourceRoots []string `json:"includedSourceRoots"`
	ExcludedReasons     []string `json:"excludedReasons,omitempty"`
	Measured            bool     `json:"measured"`
}

type CapabilityProfile struct {
	Adapter          string   `json:"adapter"`
	AdapterVersion   string   `json:"adapterVersion,omitempty"`
	AnalyzerRevision string   `json:"analyzerRevision"`
	Features         []string `json:"features"`
	Unsupported      []string `json:"unsupported,omitempty"`
}

func (p CapabilityProfile) Supports(name string) bool {
	for _, feature := range p.Features {
		if feature == name {
			return true
		}
	}
	return false
}

type Fact struct {
	FactID       string   `json:"factId"`
	Kind         string   `json:"kind"`
	SymbolPath   string   `json:"symbolPath,omitempty"`
	Path         string   `json:"path,omitempty"`
	EvidenceRefs []string `json:"evidenceRefs,omitempty"`
}

type Result struct {
	SchemaID              string             `json:"schemaId"`
	SchemaVersion         int                `json:"schemaVersion"`
	RequestID             string             `json:"requestId"`
	Operation             string             `json:"operation"`
	AdapterVersion        string             `json:"adapterVersion"`
	AnalyzerRevision      string             `json:"analyzerRevision"`
	WorkspaceEpoch        int64              `json:"workspaceEpoch"`
	ComputedBasisID       string             `json:"computedBasisId"`
	SnapshotID            string             `json:"snapshotId"`
	SnapshotTreeDigest    string             `json:"snapshotTreeDigest"`
	DependencyFingerprint string             `json:"dependencyFingerprint"`
	ReadSet               AnalysisReadSet    `json:"analysisReadSet"`
	Closure               ObservationClosure `json:"causalObservationClosure"`
	Capability            CapabilityProfile  `json:"capabilityProfile"`
	Coverage              Coverage           `json:"coverage"`
	Facts                 []Fact             `json:"facts,omitempty"`
	Diagnostics           []Diagnostic       `json:"diagnostics"`
	Payload               json.RawMessage    `json:"payload"`
}

type Diagnostic struct {
	Severity string `json:"severity"`
	Code     string `json:"code,omitempty"`
	Message  string `json:"message"`
	Path     string `json:"path,omitempty"`
}

// MountPermissionEvidence records the executable boundary used by an
// adapter/helper and the cleanup result for its disposable process area.
// SourceDelivery is protocol_snapshot_bytes when source never enters the
// subprocess through a repository path. SourceMount stays not_mounted for
// that transport mode and is intentionally explicit for registry consumers.
type MountPermissionEvidence struct {
	SourceDelivery                 string   `json:"sourceDelivery"`
	SourceMount                    string   `json:"sourceMount"`
	WorkingDirectoryMode           string   `json:"workingDirectoryMode"`
	WorkingDirectoryPermission     string   `json:"workingDirectoryPermission"`
	ReadOnlySource                 bool     `json:"readOnlySource"`
	Disposable                     bool     `json:"disposable"`
	RepositoryPathExposed          bool     `json:"repositoryPathExposed"`
	DependencyEnvironmentPreserved bool     `json:"dependencyEnvironmentPreserved"`
	CleanupVerified                bool     `json:"cleanupVerified"`
	TerminalModes                  []string `json:"terminalModes,omitempty"`
}

// IsolationLifecycleEvidence is the machine-readable artifact emitted by the
// production subprocess lifecycle probe. It binds the process-boundary proof
// to the immutable snapshot and to the terminal modes that were actually
// exercised. A registry must consume this artifact instead of accepting a
// caller-constructed passing claim.
const IsolationLifecycleEvidenceSchemaID = "https://codeflow.local/schemas/rflsc.a11-isolation-evidence.v2.schema.json"

type IsolationLifecycleEvidence struct {
	SchemaID                   string                     `json:"schemaId"`
	SchemaVersion              int                        `json:"schemaVersion"`
	ExecutionID                string                     `json:"executionId"`
	SnapshotID                 string                     `json:"snapshotId"`
	SnapshotTreeDigest         string                     `json:"snapshotTreeDigest"`
	RepositoryPathWriteAudit   workspace.SourceWriteAudit `json:"repositoryPathWriteAudit"`
	MountPermissionEvidence    MountPermissionEvidence    `json:"mountPermissionEvidence"`
	RelativeWriteObserved      bool                       `json:"relativeWriteObserved"`
	FailedSpawnCleanupVerified bool                       `json:"failedSpawnCleanupVerified"`
	ProductionTests            []string                   `json:"productionTests"`
	ArtifactRefs               []string                   `json:"artifactRefs"`
}

// Validate rejects an isolation artifact unless it proves every terminal mode
// required by A11 and remains bound to a clean immutable snapshot audit.
func (e IsolationLifecycleEvidence) Validate() error {
	if e.SchemaID != IsolationLifecycleEvidenceSchemaID || e.SchemaVersion != SchemaVersion {
		return errors.New("isolation evidence has an unexpected schema")
	}
	if e.ExecutionID == "" || e.SnapshotID == "" || e.SnapshotTreeDigest == "" {
		return errors.New("isolation evidence is missing immutable identity")
	}
	if e.RepositoryPathWriteAudit.CapturedSnapshotTreeDigest != e.SnapshotTreeDigest || e.RepositoryPathWriteAudit.SourceIntegrityViolation || e.RepositoryPathWriteAudit.CodeFlowWriteCount != 0 || len(e.RepositoryPathWriteAudit.RepositoryPathWrites) != 0 {
		return errors.New("isolation evidence has a source write audit violation")
	}
	isolation := e.MountPermissionEvidence
	if isolation.SourceDelivery != "protocol_snapshot_bytes" || isolation.SourceMount != "not_mounted" || isolation.WorkingDirectoryMode != "process_private_disposable" || isolation.WorkingDirectoryPermission != "0700" || !isolation.ReadOnlySource || !isolation.Disposable || isolation.RepositoryPathExposed || !isolation.DependencyEnvironmentPreserved || !isolation.CleanupVerified {
		return errors.New("isolation evidence does not prove the disposable read-only boundary")
	}
	requiredModes := map[string]bool{"success": false, "cancel": false, "timeout": false, "crash": false}
	for _, mode := range isolation.TerminalModes {
		if _, ok := requiredModes[mode]; !ok {
			return fmt.Errorf("isolation evidence contains unknown terminal mode %q", mode)
		}
		if requiredModes[mode] {
			return fmt.Errorf("isolation evidence contains duplicate terminal mode %q", mode)
		}
		requiredModes[mode] = true
	}
	for mode, observed := range requiredModes {
		if !observed {
			return fmt.Errorf("isolation evidence is missing terminal mode %q", mode)
		}
	}
	if !e.RelativeWriteObserved || !e.FailedSpawnCleanupVerified {
		return errors.New("isolation evidence is missing disposable write or failed-spawn cleanup proof")
	}
	requiredTests := map[string]bool{
		"protocol.TestSourceReadOnlyAdapterLifecycle":                   false,
		"protocol.TestAdapterProcessIsolationAndCleanup":                false,
		"protocol.TestConnPublishesMountPermissionEvidenceAfterCleanup": false,
	}
	for _, testName := range e.ProductionTests {
		if _, ok := requiredTests[testName]; !ok {
			return fmt.Errorf("isolation evidence contains unknown production test %q", testName)
		}
		if requiredTests[testName] {
			return fmt.Errorf("isolation evidence contains duplicate production test %q", testName)
		}
		requiredTests[testName] = true
	}
	for testName, observed := range requiredTests {
		if !observed {
			return fmt.Errorf("isolation evidence is missing production test %q", testName)
		}
	}
	if len(e.ArtifactRefs) == 0 {
		return errors.New("isolation evidence has no artifact references")
	}
	for _, ref := range e.ArtifactRefs {
		if strings.TrimSpace(ref) == "" {
			return errors.New("isolation evidence has an empty artifact reference")
		}
	}
	return nil
}

type SemanticValidationError struct {
	Field  string
	Reason string
}

func (e *SemanticValidationError) Error() string {
	return fmt.Sprintf("VS-02 semantic validation failed at %s: %s", e.Field, e.Reason)
}

// ValidateResult rejects a result before Fact/Evidence promotion.
func ValidateResult(request AnalyzerRequest, result Result) error {
	if result.SchemaID != AnalyzerResultSchemaID {
		return &SemanticValidationError{"schemaId", "unexpected schema"}
	}
	if result.SchemaVersion != SchemaVersion {
		return &SemanticValidationError{"schemaVersion", "unexpected version"}
	}
	if len(result.Payload) == 0 || string(result.Payload) == "null" {
		return &SemanticValidationError{"payload", "operation payload is required"}
	}
	var payload map[string]any
	if err := json.Unmarshal(result.Payload, &payload); err != nil || payload == nil {
		return &SemanticValidationError{"payload", "operation payload must be a JSON object"}
	}
	checks := []struct{ field, got, want string }{
		{"requestId", result.RequestID, request.RequestID}, {"operation", result.Operation, request.Operation},
		{"computedBasisId", result.ComputedBasisID, request.Snapshot.ComputedBasisID}, {"snapshotId", result.SnapshotID, request.Snapshot.SnapshotID},
		{"snapshotTreeDigest", result.SnapshotTreeDigest, request.Snapshot.RootTreeID}, {"dependencyFingerprint", result.DependencyFingerprint, request.Snapshot.DependencyFingerprint},
	}
	for _, check := range checks {
		if check.got != check.want {
			return &SemanticValidationError{check.field, fmt.Sprintf("want %q got %q", check.want, check.got)}
		}
	}
	if result.AnalyzerRevision == "" || result.AdapterVersion == "" {
		return &SemanticValidationError{"analyzerRevision", "analyzer and adapter revision are required"}
	}
	if result.WorkspaceEpoch != request.Snapshot.WorkspaceEpoch {
		return &SemanticValidationError{"workspaceEpoch", fmt.Sprintf("want %d got %d", request.Snapshot.WorkspaceEpoch, result.WorkspaceEpoch)}
	}
	if result.ReadSet.ReadSetID == "" || result.ReadSet.ComputedBasisID != request.Snapshot.ComputedBasisID || result.ReadSet.WorkspaceEpoch != request.Snapshot.WorkspaceEpoch {
		return &SemanticValidationError{"analysisReadSet", "identity does not match request snapshot"}
	}
	if result.ReadSet.SchemaID != ReadSetSchemaID || result.ReadSet.SchemaVersion != SchemaVersion {
		return &SemanticValidationError{"analysisReadSet.schemaId", "read set schema identity is required"}
	}
	if result.Closure.ClosureID == "" || result.Closure.AnalysisReadSetID != result.ReadSet.ReadSetID || result.Closure.ComputedBasisID != request.Snapshot.ComputedBasisID || result.Closure.WorkspaceEpoch != request.Snapshot.WorkspaceEpoch {
		return &SemanticValidationError{"causalObservationClosure", "closure links do not match read set/request"}
	}
	if result.Closure.SchemaID != ClosureSchemaID || result.Closure.SchemaVersion != SchemaVersion {
		return &SemanticValidationError{"causalObservationClosure.schemaId", "closure schema identity is required"}
	}
	if !sameStrings(result.Closure.RequiredObservations, request.RequiredObservations) {
		return &SemanticValidationError{"causalObservationClosure.requiredObservations", "does not match request requirements"}
	}
	if result.Closure.Status != "open" && result.Closure.Status != "closed" {
		return &SemanticValidationError{"causalObservationClosure.closureStatus", "must be open or closed"}
	}
	if result.Capability.Adapter == "" || result.Capability.AnalyzerRevision == "" {
		return &SemanticValidationError{"capabilityProfile", "adapter and analyzerRevision are required"}
	}
	if result.Capability.AnalyzerRevision != result.AnalyzerRevision {
		return &SemanticValidationError{"capabilityProfile.analyzerRevision", "does not match analyzerRevision"}
	}
	if len(result.Capability.Features) == 0 {
		return &SemanticValidationError{"capabilityProfile.features", "at least one measured capability is required"}
	}
	for _, feature := range result.Capability.Features {
		if result.Capability.Supports(feature) && containsCapability(result.Capability.Unsupported, feature) {
			return &SemanticValidationError{"capabilityProfile.unsupported", "cannot overlap supported features"}
		}
	}
	if !result.Coverage.Measured {
		return &SemanticValidationError{"coverage.measured", "coverage must be measured"}
	}
	if len(result.Coverage.IncludedSourceRoots) == 0 {
		return &SemanticValidationError{"coverage.includedSourceRoots", "at least one measured source root is required"}
	}
	if err := validateReadDocuments(request.Snapshot, result.ReadSet.Documents); err != nil {
		return err
	}
	if err := validateObservationLists(result.ReadSet, result.Closure); err != nil {
		return err
	}
	for _, required := range request.RequiredObservations {
		if !observationMeasured(required, result.ReadSet, result.Closure) {
			if result.Closure.Status == "closed" {
				return &SemanticValidationError{"causalObservationClosure.closureStatus", "required observation " + required + " was not measured"}
			}
			if !containsReason(result.Closure.IncompleteReasons, required) {
				return &SemanticValidationError{"causalObservationClosure.incompleteReasons", "open closure must explain missing " + required}
			}
		}
	}
	return nil
}

func containsCapability(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func validateReadDocuments(snapshot SnapshotInput, docs []ReadDocument) error {
	want := make(map[string]SnapshotDocument, len(snapshot.Documents))
	for _, doc := range snapshot.Documents {
		want[doc.Path] = doc
	}
	seen := make(map[string]bool, len(docs))
	for _, doc := range docs {
		if !validRelativePath(doc.Path) || seen[doc.Path] {
			return &SemanticValidationError{"analysisReadSet.documents", "invalid or duplicate path"}
		}
		seen[doc.Path] = true
		base, ok := want[doc.Path]
		if !ok || base.ContentID == "" || doc.ContentHash == "" || doc.ContentHash != base.ContentID || doc.ByteLength != base.ByteLength {
			return &SemanticValidationError{"analysisReadSet.documents." + doc.Path, "document identity/content does not match snapshot"}
		}
		if digestBytes(base.Bytes) != base.ContentID || doc.ContentID != base.ContentID {
			return &SemanticValidationError{"analysisReadSet.documents." + doc.Path + ".contentId", "does not match snapshot content identity"}
		}
		if base.RevisionID == "" || doc.DocumentRevisionID != base.RevisionID {
			return &SemanticValidationError{"analysisReadSet.documents." + doc.Path + ".documentRevisionId", "does not match snapshot revision identity"}
		}
		if base.DocumentVersion <= 0 || doc.DocumentVersion != base.DocumentVersion {
			return &SemanticValidationError{"analysisReadSet.documents." + doc.Path + ".documentVersion", "does not match snapshot document version"}
		}
		if len(base.Bytes) != base.ByteLength {
			return &SemanticValidationError{"analysisReadSet.documents." + doc.Path + ".byteLength", "snapshot byte length is inconsistent with captured bytes"}
		}
	}
	return nil
}

func validateObservationLists(readSet AnalysisReadSet, closure ObservationClosure) error {
	for _, item := range []struct {
		field string
		want  []Observation
		got   []Observation
	}{
		{"negativeObservations", readSet.NegativeObservations, closure.NegativeObservations},
		{"membershipObservations", readSet.MembershipObservations, closure.MembershipObservations},
		{"dependencyFrontiers", readSet.DependencyFrontiers, closure.DependencyFrontiers},
	} {
		if !sameObservations(item.want, item.got) {
			return &SemanticValidationError{"causalObservationClosure." + item.field, "observation list does not exactly match analysis read set"}
		}
	}
	derived := measuredObservationNames(readSet)
	declared := append([]string(nil), closure.MeasuredObservations...)
	if hasDuplicateStrings(declared) || !sameStringSet(derived, declared) {
		return &SemanticValidationError{"causalObservationClosure.measuredObservations", "must be derived from measured read-set observations"}
	}
	return nil
}

func sameObservations(want, got []Observation) bool {
	if len(want) != len(got) {
		return false
	}
	for i := range want {
		if want[i] != got[i] {
			return false
		}
		if !validRelativePath(want[i].Path) && want[i].Path != "." {
			return false
		}
	}
	return true
}

func measuredObservationNames(readSet AnalysisReadSet) []string {
	var names []string
	appendMeasured := func(category string, observations []Observation) {
		for _, observation := range observations {
			if !observation.Measured {
				continue
			}
			name := observation.Kind
			switch category {
			case "negative_lookup":
				if name == "negative_lookup" {
					name = category
				}
			case "membership":
				if name == "source_membership" || name == "membership" {
					name = category
				}
			case "dependency_frontier":
				if name == "dependency_frontier" {
					name = category
				}
			}
			if name != "" && !containsString(names, name) {
				names = append(names, name)
			}
		}
	}
	appendMeasured("negative_lookup", readSet.NegativeObservations)
	appendMeasured("membership", readSet.MembershipObservations)
	appendMeasured("dependency_frontier", readSet.DependencyFrontiers)
	return names
}

func sameStrings(want, got []string) bool {
	if len(want) != len(got) {
		return false
	}
	for i := range want {
		if want[i] != got[i] {
			return false
		}
	}
	return true
}

func sameStringSet(want, got []string) bool {
	if len(want) != len(got) {
		return false
	}
	for _, value := range want {
		if !containsString(got, value) {
			return false
		}
	}
	return true
}

func hasDuplicateStrings(values []string) bool {
	seen := map[string]bool{}
	for _, value := range values {
		if seen[value] {
			return true
		}
		seen[value] = true
	}
	return false
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func observationMeasured(name string, readSet AnalysisReadSet, closure ObservationClosure) bool {
	var observations []Observation
	switch name {
	case "negative_lookup":
		observations = readSet.NegativeObservations
	case "membership":
		observations = readSet.MembershipObservations
	case "dependency_frontier":
		observations = readSet.DependencyFrontiers
	default:
		for _, f := range readSet.NegativeObservations {
			if f.Kind == name && f.Measured {
				return true
			}
		}
		for _, f := range readSet.MembershipObservations {
			if f.Kind == name && f.Measured {
				return true
			}
		}
		for _, f := range readSet.DependencyFrontiers {
			if f.Kind == name && f.Measured {
				return true
			}
		}
		for _, f := range closure.MeasuredObservations {
			if f == name {
				return true
			}
		}
		return false
	}
	for _, observation := range observations {
		if observation.Measured {
			return true
		}
	}
	return false
}

func containsReason(reasons []string, name string) bool {
	for _, reason := range reasons {
		if strings.Contains(strings.ToLower(reason), strings.ToLower(name)) {
			return true
		}
	}
	return false
}

// EvidenceAnchor addresses a source range in a validated snapshot document.
type EvidenceAnchor struct {
	EvidenceID string
	Path       string
	RevisionID string
	// FileHash and SpanHash are optional at this low-level protocol boundary
	// for compatibility with older callers. Production semantic evidence
	// always supplies both, and when supplied they must match the selected
	// snapshot bytes before a record can be promoted to verified.
	FileHash  string
	SpanHash  string
	StartByte int
	EndByte   int
}

type EvidenceRecord struct {
	SchemaID           string `json:"schemaId"`
	SchemaVersion      int    `json:"schemaVersion"`
	EvidenceID         string `json:"evidenceId"`
	SnapshotID         string `json:"snapshotId"`
	ComputedBasisID    string `json:"computedBasisId"`
	DocumentRevisionID string `json:"documentRevisionId"`
	Path               string `json:"path"`
	ByteRange          [2]int `json:"byteRange"`
	LineRange          [2]int `json:"lineRange"`
	Snippet            string `json:"snippet"`
	ValidationStatus   string `json:"validationStatus"`
	RedactionStatus    string `json:"redactionStatus"`
}

type EvidenceError struct{ Code, Path, Detail string }

func (e *EvidenceError) Error() string {
	return fmt.Sprintf("evidence_%s: %s (%s)", e.Code, e.Detail, e.Path)
}

func ExtractEvidence(snapshot SnapshotInput, anchors []EvidenceAnchor) ([]EvidenceRecord, error) {
	if err := validateSnapshotInput(snapshot); err != nil {
		return nil, &EvidenceError{Code: "invalid_snapshot", Detail: err.Error()}
	}
	result := make([]EvidenceRecord, 0, len(anchors))
	for _, anchor := range anchors {
		if anchor.EvidenceID == "" {
			return nil, &EvidenceError{"invalid_anchor", anchor.Path, "evidence id is required"}
		}
		if !validRelativePath(anchor.Path) {
			return nil, &EvidenceError{"path_rejected", anchor.Path, "path must be repository-relative"}
		}
		doc, ok := snapshot.Document(anchor.Path)
		if !ok {
			return nil, &EvidenceError{"unknown_file", anchor.Path, "file is not in selected snapshot"}
		}
		if anchor.RevisionID == "" || anchor.RevisionID != doc.RevisionID {
			return nil, &EvidenceError{"unknown_revision", anchor.Path, "revision does not match selected snapshot"}
		}
		if anchor.FileHash != "" && anchor.FileHash != digestBytes(doc.Bytes) {
			return nil, &EvidenceError{"unknown_revision", anchor.Path, "file hash does not match selected snapshot"}
		}
		if anchor.StartByte < 0 || anchor.EndByte < anchor.StartByte || anchor.EndByte > len(doc.Bytes) {
			return nil, &EvidenceError{"invalid_range", anchor.Path, "byte range is outside snapshot bytes"}
		}
		if !utf8.Valid(doc.Bytes[anchor.StartByte:anchor.EndByte]) {
			return nil, &EvidenceError{"invalid_range", anchor.Path, "range splits invalid UTF-8"}
		}
		if anchor.SpanHash != "" && anchor.SpanHash != digestBytes(doc.Bytes[anchor.StartByte:anchor.EndByte]) {
			return nil, &EvidenceError{"invalid_anchor", anchor.Path, "span hash does not match selected snapshot range"}
		}
		lineStart := 1 + countNewlines(doc.Bytes[:anchor.StartByte])
		lineEnd := lineStart
		if anchor.EndByte > anchor.StartByte {
			lineEnd = 1 + countNewlines(doc.Bytes[:anchor.EndByte-1])
		}
		raw := doc.Bytes[anchor.StartByte:anchor.EndByte]
		redacted := secret.Redact(string(raw))
		redactionStatus := "clean"
		if redacted.Count > 0 {
			redactionStatus = "redacted"
		}
		result = append(result, EvidenceRecord{SchemaID: EvidenceSchemaID, SchemaVersion: SchemaVersion, EvidenceID: anchor.EvidenceID, SnapshotID: snapshot.SnapshotID, ComputedBasisID: snapshot.ComputedBasisID, DocumentRevisionID: doc.RevisionID, Path: doc.Path, ByteRange: [2]int{anchor.StartByte, anchor.EndByte}, LineRange: [2]int{lineStart, lineEnd}, Snippet: redacted.Text, ValidationStatus: "verified", RedactionStatus: redactionStatus})
	}
	return result, nil
}

// validateSnapshotInput is the final evidence boundary. SnapshotInput is
// exported for protocol adapters and tests, so extraction cannot assume that
// a caller constructed it through one of the defensive constructors. Every
// byte range must be resolved against content whose identity and length still
// agree with the captured snapshot metadata.
func validateSnapshotInput(snapshot SnapshotInput) error {
	if snapshot.SnapshotID == "" || snapshot.ComputedBasisID == "" || snapshot.RootTreeID == "" || snapshot.DependencyFingerprint == "" || snapshot.WorkspaceEpoch < 0 {
		return errors.New("snapshot identity is incomplete")
	}
	seen := make(map[string]struct{}, len(snapshot.Documents))
	for _, document := range snapshot.Documents {
		if !validRelativePath(document.Path) {
			return fmt.Errorf("document path is invalid: %q", document.Path)
		}
		if _, exists := seen[document.Path]; exists {
			return fmt.Errorf("duplicate document path: %q", document.Path)
		}
		seen[document.Path] = struct{}{}
		if document.RevisionID == "" || document.ContentID == "" {
			return fmt.Errorf("document %s identity is incomplete", document.Path)
		}
		if document.DocumentVersion < 1 {
			return fmt.Errorf("document %s version is invalid", document.Path)
		}
		if document.ByteLength != len(document.Bytes) {
			return fmt.Errorf("document %s byte length does not match captured bytes", document.Path)
		}
		if digestBytes(document.Bytes) != document.ContentID {
			return fmt.Errorf("document %s content identity does not match captured bytes", document.Path)
		}
	}
	return nil
}

func countNewlines(data []byte) int { return strings.Count(string(data), "\n") }

// RedactJSON applies the key/value scanner before clipping. The result is
// bounded by bytes and never returns the unredacted source on failure.
func RedactJSON(raw []byte, maxBytes int) ([]byte, int, error) {
	if maxBytes <= 0 {
		maxBytes = MaxDiagnosticBytes
	}
	clean, count, err := secret.RedactJSON(raw)
	if err != nil {
		return nil, 0, err
	}
	if len(clean) > maxBytes {
		clean = append([]byte(nil), clean[:maxBytes]...)
	}
	return clean, count, nil
}

type CapabilityMeasurement struct {
	Adapter          string   `json:"adapter"`
	AdapterVersion   string   `json:"adapterVersion,omitempty"`
	ProtocolVersion  int      `json:"protocolVersion,omitempty"`
	AnalyzerRevision string   `json:"analyzerRevision"`
	MeasurementID    string   `json:"measurementId"`
	Status           string   `json:"status"`
	Features         []string `json:"features"`
	Unsupported      []string `json:"unsupported"`
	Evidence         []string `json:"evidence,omitempty"`
}

func (m CapabilityMeasurement) Supports(feature string) bool {
	for _, f := range m.Features {
		if f == feature {
			return true
		}
	}
	return false
}

// InitializeCapabilityEvidence is the small, serializable subset of an
// initialize response needed to derive the capability matrix. A declaration
// is measured only after the adapter passed protocol conformance and exposed
// every required snapshot/observation capability.
type InitializeCapabilityEvidence struct {
	Adapter           string
	AdapterVersion    string
	AnalyzerRevision  string
	ProtocolVersion   int
	Cancellation      bool
	Progress          bool
	BatchAck          bool
	SnapshotOverlay   bool
	AnalysisMetadata  bool
	FlowContext       bool
	MaxMessageBytes   int64
	ConformancePassed bool
	// ConformanceProbeID and ConformanceObservations are populated only by an
	// executable probe that exercised the adapter's snapshot boundary. The
	// initialize capability bit alone cannot promote these relations.
	ConformanceProbeID      string
	ConformanceObservations []string
	OpenOnMissing           bool
	ReadOnlySource          bool
}

var requiredCapabilityObservations = []string{
	"snapshot_bytes", "read_set", "membership", "negative_lookup", "dependency_frontier",
}

func CapabilityMeasurementFromInitialize(evidence InitializeCapabilityEvidence) CapabilityMeasurement {
	features := []string{}
	provenance := []string{}
	if evidence.AdapterVersion != "" || evidence.AnalyzerRevision != "" || evidence.ProtocolVersion > 0 {
		provenance = append(provenance, "initialize")
	}
	if evidence.ConformancePassed {
		provenance = append(provenance, "protocol_conformance")
		if evidence.ConformanceProbeID != "" {
			provenance = append(provenance, "probe:"+evidence.ConformanceProbeID)
		}
		observations := append([]string(nil), evidence.ConformanceObservations...)
		sort.Strings(observations)
		for _, observation := range observations {
			provenance = append(provenance, "observation:"+observation)
		}
		if evidence.OpenOnMissing {
			provenance = append(provenance, "open_on_missing")
		}
		if evidence.ReadOnlySource {
			provenance = append(provenance, "read_only_source")
		}
	}
	if len(provenance) == 0 {
		provenance = append(provenance, "missing_or_unprobed")
	}
	conformanceComplete := capabilityConformanceComplete(evidence)
	if evidence.SnapshotOverlay && conformanceComplete && containsAllCapabilityObservations(evidence.ConformanceObservations, "snapshot_bytes") {
		features = append(features, "snapshot_bytes")
	}
	if evidence.AnalysisMetadata && conformanceComplete {
		features = append(features, "read_set", "membership", "negative_lookup", "dependency_frontier")
	}
	if conformanceComplete && evidence.SnapshotOverlay && evidence.AnalysisMetadata && evidence.ReadOnlySource {
		features = append(features, "read_only_source")
	}
	status := "unsupported"
	if conformanceComplete && evidence.Adapter != "" && evidence.AdapterVersion != "" && evidence.AnalyzerRevision != "" && evidence.ProtocolVersion > 0 && evidence.Cancellation && evidence.Progress && evidence.BatchAck && evidence.SnapshotOverlay && evidence.AnalysisMetadata && evidence.MaxMessageBytes > 0 && evidence.ReadOnlySource {
		status = "measured"
	}
	unsupported := []string{"runtime_observation", "dynamic_resolution"}
	if status != "measured" {
		unsupported = append(unsupported, "snapshot_bytes", "read_set", "membership", "negative_lookup", "dependency_frontier", "read_only_source")
		features = []string{}
	}
	observationsForID := append([]string(nil), evidence.ConformanceObservations...)
	sort.Strings(observationsForID)
	measurementMaterial := fmt.Sprintf("%s|%s|%s|%d|%t|%t|%t|%t|%t|%d|%t|probe=%s|observations=%s|open=%t|readonly=%t", evidence.Adapter, evidence.AdapterVersion, evidence.AnalyzerRevision, evidence.ProtocolVersion, evidence.Cancellation, evidence.Progress, evidence.BatchAck, evidence.SnapshotOverlay, evidence.AnalysisMetadata, evidence.MaxMessageBytes, evidence.ConformancePassed, evidence.ConformanceProbeID, strings.Join(observationsForID, ","), evidence.OpenOnMissing, evidence.ReadOnlySource)
	measurementID := digestBytes([]byte(measurementMaterial))
	return CapabilityMeasurement{
		Adapter: evidence.Adapter, AdapterVersion: evidence.AdapterVersion, ProtocolVersion: evidence.ProtocolVersion,
		AnalyzerRevision: evidence.AnalyzerRevision, MeasurementID: "rflsc-init-" + measurementID[:24], Status: status,
		Features: features, Unsupported: unsupported,
		Evidence: provenance,
	}
}

func capabilityConformanceComplete(evidence InitializeCapabilityEvidence) bool {
	if !evidence.ConformancePassed || evidence.ConformanceProbeID == "" || !evidence.OpenOnMissing || !evidence.ReadOnlySource {
		return false
	}
	if len(evidence.ConformanceObservations) != len(requiredCapabilityObservations) {
		return false
	}
	return containsAllCapabilityObservations(evidence.ConformanceObservations, requiredCapabilityObservations...)
}

func containsAllCapabilityObservations(observations []string, required ...string) bool {
	seen := make(map[string]bool, len(observations))
	for _, observation := range observations {
		seen[observation] = true
	}
	for _, observation := range required {
		if !seen[observation] {
			return false
		}
	}
	return true
}

// CapabilityMatrixFromInitializeEvidence derives entries only from actual
// initialize/conformance observations. Missing languages are explicit
// unsupported entries rather than fabricated empty-success measurements.
func CapabilityMatrixFromInitializeEvidence(evidence []InitializeCapabilityEvidence) map[string]CapabilityMeasurement {
	result := make(map[string]CapabilityMeasurement, 3)
	for _, item := range evidence {
		if item.Adapter == "" {
			continue
		}
		result[item.Adapter] = CapabilityMeasurementFromInitialize(item)
	}
	for _, language := range []string{"dart", "typescript", "go"} {
		if _, ok := result[language]; !ok {
			result[language] = CapabilityMeasurementFromInitialize(InitializeCapabilityEvidence{Adapter: language})
		}
	}
	return result
}

type BoundError struct{ Limit, Size int64 }

func (e *BoundError) Error() string {
	return fmt.Sprintf("message exceeds negotiated bound: %d > %d bytes", e.Size, e.Limit)
}

func MarshalBoundedRequest(request AnalyzerRequest) ([]byte, error) {
	limit := request.MaxMessageBytes
	if limit <= 0 {
		limit = DefaultMaxMessageBytes
	}
	raw, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > limit {
		return nil, &BoundError{Limit: limit, Size: int64(len(raw))}
	}
	return raw, nil
}
