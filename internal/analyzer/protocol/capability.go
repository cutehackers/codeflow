package protocol

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"codeflow/internal/collector/contractharness"
	"codeflow/internal/collector/evidence"
)

const (
	// DefaultCapabilityMeasurementTTL bounds how long a conformance result may
	// be reused without another explicit refresh. Capability discovery is not
	// part of ordinary analysis requests.
	DefaultCapabilityMeasurementTTL = 24 * time.Hour

	capabilityMatrixSchemaVersion = 1
)

var supportedCapabilityAdapters = []string{"dart", "typescript", "go"}

// CapabilityProvenance identifies the initialize/conformance evidence behind
// one published capability entry. Timestamps are strings in the public
// document so zero values can be omitted and the document remains schema
// valid.
type CapabilityProvenance struct {
	Status     string   `json:"status"`
	MeasuredAt string   `json:"measuredAt,omitempty"`
	ExpiresAt  string   `json:"expiresAt,omitempty"`
	Evidence   []string `json:"evidence,omitempty"`
}

// CapabilityMatrixSnapshot is the Core-owned, product-consumable capability
// publication. It contains an explicit entry for every supported adapter.
type CapabilityMatrixSnapshot struct {
	SchemaID      string                           `json:"schemaId"`
	SchemaVersion int                              `json:"schemaVersion"`
	GeneratedAt   string                           `json:"generatedAt,omitempty"`
	ExpiresAt     string                           `json:"expiresAt,omitempty"`
	Measurements  []evidence.CapabilityMeasurement `json:"measurements"`
	Provenance    map[string]CapabilityProvenance  `json:"provenance,omitempty"`
}

// Measurement returns a defensive copy of one adapter's current capability.
func (s CapabilityMatrixSnapshot) Measurement(adapter string) evidence.CapabilityMeasurement {
	for _, measurement := range s.Measurements {
		if measurement.Adapter == adapter {
			measurement.Features = append([]string(nil), measurement.Features...)
			measurement.Unsupported = append([]string(nil), measurement.Unsupported...)
			measurement.Evidence = append([]string(nil), measurement.Evidence...)
			return measurement
		}
	}
	return evidence.CapabilityMeasurement{}
}

type capabilityRecord struct {
	measurement evidence.CapabilityMeasurement
	measuredAt  time.Time
	expiresAt   time.Time
}

// CapabilityRegistry stores verified initialize/conformance measurements. It
// is intentionally separate from request routing so normal analysis calls do
// not execute a conformance subprocess probe.
type CapabilityRegistry struct {
	mu      sync.RWMutex
	ttl     time.Duration
	records map[string]capabilityRecord
}

// NewCapabilityRegistry creates a Core-owned capability publication store.
// A non-positive TTL selects the explicit default lifetime.
func NewCapabilityRegistry(ttl time.Duration) *CapabilityRegistry {
	if ttl <= 0 {
		ttl = DefaultCapabilityMeasurementTTL
	}
	return &CapabilityRegistry{ttl: ttl, records: make(map[string]capabilityRecord, len(supportedCapabilityAdapters))}
}

// publishMeasurement publishes one already-derived measurement. It is
// package-private because the measured state must originate from the
// canonical executable conformance path.
func (r *CapabilityRegistry) publishMeasurement(measurement evidence.CapabilityMeasurement, measuredAt time.Time, proof CapabilityConformanceEvidence) error {
	if r == nil {
		return fmt.Errorf("capability registry is nil")
	}
	if measuredAt.IsZero() {
		measuredAt = time.Now().UTC()
	}
	measurement = normalizeCapabilityMeasurement(measurement)
	if !isSupportedCapabilityAdapter(measurement.Adapter) {
		return fmt.Errorf("unsupported capability adapter %q", measurement.Adapter)
	}
	if measurement.MeasurementID == "" {
		return fmt.Errorf("capability measurement %q has no measurementId", measurement.Adapter)
	}
	if measurement.Status != "measured" && measurement.Status != "unsupported" {
		return fmt.Errorf("capability measurement %q has invalid status %q", measurement.Adapter, measurement.Status)
	}
	if measurement.Status == "measured" {
		if !proof.complete() {
			return fmt.Errorf("capability measurement %q lacks canonical executable proof", measurement.Adapter)
		}
		if measurement.AdapterVersion == "" || measurement.AnalyzerRevision == "" || measurement.ProtocolVersion <= 0 || len(measurement.Features) == 0 {
			return fmt.Errorf("capability measurement %q lacks complete initialize evidence", measurement.Adapter)
		}
		if !hasCapabilityEvidence(measurement.Evidence, "initialize") || !hasCapabilityEvidence(measurement.Evidence, "protocol_conformance") {
			return fmt.Errorf("capability measurement %q lacks initialize or executable conformance provenance", measurement.Adapter)
		}
		if !hasCapabilityEvidencePrefix(measurement.Evidence, "probe:") || !hasCapabilityEvidence(measurement.Evidence, "open_on_missing") || !hasCapabilityEvidence(measurement.Evidence, "read_only_source") {
			return fmt.Errorf("capability measurement %q lacks canonical conformance proof", measurement.Adapter)
		}
		for _, observation := range canonicalCapabilityObservations {
			if !hasCapabilityEvidence(measurement.Evidence, "observation:"+observation) {
				return fmt.Errorf("capability measurement %q lacks conformance observation %q", measurement.Adapter, observation)
			}
		}
	}

	record := capabilityRecord{measurement: measurement}
	if measurement.Status == "measured" {
		record.measuredAt = measuredAt.UTC()
		record.expiresAt = record.measuredAt.Add(r.ttl)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	previous, hadPrevious := r.records[measurement.Adapter]
	r.records[measurement.Adapter] = record
	if _, err := r.matrixJSONAtLocked(measuredAt); err != nil {
		if hadPrevious {
			r.records[measurement.Adapter] = previous
		} else {
			delete(r.records, measurement.Adapter)
		}
		return err
	}
	return nil
}

// Invalidate removes a cached measurement after an adapter configuration
// change. Reads then expose the explicit unsupported/unprobed entry.
func (r *CapabilityRegistry) Invalidate(adapter string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	delete(r.records, adapter)
	r.mu.Unlock()
}

// Snapshot returns the current matrix using the local clock for expiry.
func (r *CapabilityRegistry) Snapshot() CapabilityMatrixSnapshot {
	return r.SnapshotAt(time.Now().UTC())
}

// SnapshotAt returns a point-in-time view without running any adapter probe.
func (r *CapabilityRegistry) SnapshotAt(at time.Time) CapabilityMatrixSnapshot {
	if r == nil {
		return emptyCapabilityMatrix(at)
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	r.mu.RLock()
	snapshot := r.snapshotAtLocked(at)
	r.mu.RUnlock()
	return snapshot
}

// MatrixJSON returns the schema-validated publication document.
func (r *CapabilityRegistry) MatrixJSON() ([]byte, error) {
	return r.MatrixJSONAt(time.Now().UTC())
}

// MatrixJSONAt returns the schema-validated publication at a selected time.
func (r *CapabilityRegistry) MatrixJSONAt(at time.Time) ([]byte, error) {
	if r == nil {
		return nil, fmt.Errorf("capability registry is nil")
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	r.mu.RLock()
	raw, err := r.matrixJSONAtLocked(at)
	r.mu.RUnlock()
	return raw, err
}

func (r *CapabilityRegistry) matrixJSONAtLocked(at time.Time) ([]byte, error) {
	raw, err := json.Marshal(r.snapshotAtLocked(at))
	if err != nil {
		return nil, fmt.Errorf("marshal capability matrix: %w", err)
	}
	if err := contractharness.Validate(evidence.CapabilityMatrixSchemaID, raw); err != nil {
		return nil, fmt.Errorf("capability matrix schema validation failed: %w", err)
	}
	return raw, nil
}

func (r *CapabilityRegistry) snapshotAtLocked(at time.Time) CapabilityMatrixSnapshot {
	snapshot := emptyCapabilityMatrix(at)
	var earliestExpiry time.Time
	for _, adapter := range supportedCapabilityAdapters {
		record, ok := r.records[adapter]
		if !ok {
			snapshot.Measurements = append(snapshot.Measurements, unsupportedCapabilityMeasurement(adapter, "missing_or_unprobed"))
			snapshot.Provenance[adapter] = CapabilityProvenance{Status: "unsupported", Evidence: []string{"missing_or_unprobed"}}
			continue
		}
		measurement := normalizeCapabilityMeasurement(record.measurement)
		provenance := CapabilityProvenance{Status: measurement.Status, Evidence: append([]string(nil), measurement.Evidence...)}
		if record.measuredAt.IsZero() {
			measurement.Status = "unsupported"
			measurement.Features = []string{}
		}
		if measurement.Status == "measured" {
			provenance.MeasuredAt = record.measuredAt.UTC().Format(time.RFC3339Nano)
			provenance.ExpiresAt = record.expiresAt.UTC().Format(time.RFC3339Nano)
			if !at.Before(record.expiresAt) {
				measurement = expiredCapabilityMeasurement(measurement)
				provenance.Status = "unsupported"
				provenance.Evidence = appendUniqueCapabilityString(provenance.Evidence, "expired_measurement")
			} else if earliestExpiry.IsZero() || record.expiresAt.Before(earliestExpiry) {
				earliestExpiry = record.expiresAt
			}
		}
		snapshot.Measurements = append(snapshot.Measurements, measurement)
		snapshot.Provenance[adapter] = provenance
	}
	if !earliestExpiry.IsZero() {
		snapshot.ExpiresAt = earliestExpiry.UTC().Format(time.RFC3339Nano)
	}
	return snapshot
}

func emptyCapabilityMatrix(at time.Time) CapabilityMatrixSnapshot {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	return CapabilityMatrixSnapshot{
		SchemaID: evidence.CapabilityMatrixSchemaID, SchemaVersion: capabilityMatrixSchemaVersion,
		GeneratedAt: at.UTC().Format(time.RFC3339Nano), Measurements: make([]evidence.CapabilityMeasurement, 0, len(supportedCapabilityAdapters)),
		Provenance: make(map[string]CapabilityProvenance, len(supportedCapabilityAdapters)),
	}
}

func normalizeCapabilityMeasurement(measurement evidence.CapabilityMeasurement) evidence.CapabilityMeasurement {
	measurement.Features = append([]string{}, measurement.Features...)
	measurement.Unsupported = append([]string{}, measurement.Unsupported...)
	measurement.Evidence = append([]string{}, measurement.Evidence...)
	return measurement
}

func unsupportedCapabilityMeasurement(adapter, reason string) evidence.CapabilityMeasurement {
	measurement := evidence.CapabilityMeasurementFromInitialize(evidence.InitializeCapabilityEvidence{Adapter: adapter})
	measurement.Evidence = appendUniqueCapabilityString(measurement.Evidence, reason)
	return measurement
}

func expiredCapabilityMeasurement(measurement evidence.CapabilityMeasurement) evidence.CapabilityMeasurement {
	measurement.Status = "unsupported"
	measurement.Features = []string{}
	measurement.Unsupported = appendUniqueCapabilityString(measurement.Unsupported, "expired_measurement")
	measurement.Evidence = appendUniqueCapabilityString(measurement.Evidence, "expired_measurement")
	return measurement
}

func appendUniqueCapabilityString(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func hasCapabilityEvidence(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func hasCapabilityEvidencePrefix(values []string, prefix string) bool {
	for _, value := range values {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}

func isSupportedCapabilityAdapter(adapter string) bool {
	for _, supported := range supportedCapabilityAdapters {
		if adapter == supported {
			return true
		}
	}
	return false
}

// CapabilityConformanceEvidence is produced by an executable probe. The
// unexported validated bit prevents a caller from constructing a measured
// report without using the canonical probe helper in this package.
type CapabilityConformanceEvidence struct {
	ProbeID              string
	MeasuredObservations []string
	OpenOnMissing        bool
	ReadOnlySource       bool
	validated            bool
}

// CapabilityConformanceProbe exercises one adapter connection and returns
// the observations it actually established.
type CapabilityConformanceProbe func(context.Context, *Conn) (CapabilityConformanceEvidence, error)

var canonicalCapabilityObservations = []string{
	"snapshot_bytes", "read_set", "membership", "negative_lookup", "dependency_frontier",
}

func (e CapabilityConformanceEvidence) complete() bool {
	if !e.validated || e.ProbeID == "" || !e.OpenOnMissing || !e.ReadOnlySource {
		return false
	}
	if len(e.MeasuredObservations) != len(canonicalCapabilityObservations) {
		return false
	}
	seen := make(map[string]bool, len(e.MeasuredObservations))
	for _, observation := range e.MeasuredObservations {
		if seen[observation] {
			return false
		}
		seen[observation] = true
	}
	for _, observation := range canonicalCapabilityObservations {
		if !seen[observation] {
			return false
		}
	}
	return true
}

// CanonicalCapabilityConformanceProbe executes the same bounded v2 checks for
// every supported adapter. It proves snapshot reads, read-set metadata,
// membership, a real negative lookup, a dependency frontier, and an open
// closure when an unsupported observation is requested.
func CanonicalCapabilityConformanceProbe(snapshot Snapshot, adapter string) CapabilityConformanceProbe {
	return func(ctx context.Context, conn *Conn) (CapabilityConformanceEvidence, error) {
		if !isSupportedCapabilityAdapter(adapter) {
			return CapabilityConformanceEvidence{}, fmt.Errorf("unsupported capability adapter %q", adapter)
		}
		if conn == nil {
			return CapabilityConformanceEvidence{}, fmt.Errorf("capability conformance connection is nil")
		}
		if _, err := capabilityProbeDetect(ctx, conn, snapshot, nil); err != nil {
			return CapabilityConformanceEvidence{}, err
		}
		if err := capabilityProbeHarvest(ctx, conn, snapshot); err != nil {
			return CapabilityConformanceEvidence{}, err
		}
		missing, err := capabilitySnapshotWithoutDependency(snapshot, adapter)
		if err != nil {
			return CapabilityConformanceEvidence{}, err
		}
		missingResult, err := capabilityProbeDetect(ctx, conn, missing, []string{"negative_lookup"})
		if err != nil {
			return CapabilityConformanceEvidence{}, fmt.Errorf("negative lookup probe: %w", err)
		}
		if !hasMeasuredCapabilityObservation(missingResult.ReadSet.NegativeObservations, "negative_lookup") {
			return CapabilityConformanceEvidence{}, fmt.Errorf("negative lookup probe returned no measured missing observation")
		}
		openResult, err := capabilityProbeDetect(ctx, conn, missing, []string{"dependency_frontier"})
		if err != nil {
			return CapabilityConformanceEvidence{}, fmt.Errorf("open closure probe: %w", err)
		}
		if openResult.Closure.Status != "open" || !containsCapabilityReason(openResult.Closure.IncompleteReasons, "dependency_frontier") {
			return CapabilityConformanceEvidence{}, fmt.Errorf("required unmeasured dependency frontier did not remain open: status=%s reasons=%v", openResult.Closure.Status, openResult.Closure.IncompleteReasons)
		}
		if err := capabilityProbeSlice(ctx, conn, snapshot, adapter); err != nil {
			return CapabilityConformanceEvidence{}, err
		}
		audit := snapshot.SourceWriteAudit
		if audit.CodeFlowWriteCount != 0 || audit.SourceIntegrityViolation || len(audit.RepositoryPathWrites) != 0 || audit.CapturedSnapshotTreeDigest == "" || audit.CapturedSnapshotTreeDigest != snapshot.RootTreeID {
			return CapabilityConformanceEvidence{}, fmt.Errorf("snapshot source write audit is not read-only: %+v", audit)
		}
		observations := append([]string(nil), canonicalCapabilityObservations...)
		sort.Strings(observations)
		return CapabilityConformanceEvidence{
			ProbeID:              "canonical-v2-observation-probe/" + adapter,
			MeasuredObservations: observations,
			OpenOnMissing:        true,
			ReadOnlySource:       true,
			validated:            true,
		}, nil
	}
}

func capabilityProbeDetect(ctx context.Context, conn *Conn, snapshot Snapshot, required []string) (evidence.Result, error) {
	params := snapshot.Params()
	if required != nil {
		params["requiredObservations"] = append([]string(nil), required...)
	}
	var result evidence.Result
	if err := conn.Call(ctx, OpDetect, params, &result); err != nil {
		return evidence.Result{}, err
	}
	if len(result.ReadSet.Documents) == 0 && len(required) == 0 {
		return evidence.Result{}, fmt.Errorf("detect probe did not measure any snapshot document")
	}
	return result, nil
}

func capabilityProbeHarvest(ctx context.Context, conn *Conn, snapshot Snapshot) error {
	params := snapshot.Params()
	params["requiredObservations"] = []string{"membership", "dependency_frontier"}
	var result evidence.Result
	if err := conn.Call(ctx, OpHarvestCandidates, params, &result); err != nil {
		return fmt.Errorf("harvest observation probe: %w", err)
	}
	if len(result.ReadSet.Documents) == 0 || !hasMeasuredCapabilityObservation(result.ReadSet.MembershipObservations, "membership") || !hasMeasuredCapabilityObservation(result.ReadSet.DependencyFrontiers, "dependency_frontier") || result.Closure.Status != "closed" {
		return fmt.Errorf("harvest observation probe was not closed with measured membership/frontier: status=%s read=%+v closure=%+v", result.Closure.Status, result.ReadSet, result.Closure)
	}
	return nil
}

func capabilityProbeSlice(ctx context.Context, conn *Conn, snapshot Snapshot, adapter string) error {
	entry, ok := capabilitySliceEntry(snapshot, adapter)
	if !ok {
		return fmt.Errorf("slice probe has no source entry for %s", adapter)
	}
	params := snapshot.Params()
	params["payload"] = map[string]any{"candidateId": "cand-capability0001", "entrySymbolPath": entry}
	var result evidence.Result
	if err := conn.Call(ctx, OpSlice, params, &result); err != nil {
		return fmt.Errorf("slice observation probe: %w", err)
	}
	if len(result.ReadSet.Documents) == 0 {
		return fmt.Errorf("slice probe did not measure its source document")
	}
	return nil
}

func capabilitySnapshotWithoutDependency(snapshot Snapshot, adapter string) (Snapshot, error) {
	files := cloneFiles(snapshot.Files)
	if len(files) == 0 {
		files = cloneFiles(snapshot.ContentOverlay)
	}
	var excluded []string
	switch adapter {
	case "dart":
		excluded = []string{"pubspec.yaml"}
	case "typescript":
		excluded = []string{"package.json", "tsconfig.json", "jsconfig.json"}
	case "go":
		excluded = []string{"go.mod", "go.work"}
	}
	for _, path := range excluded {
		delete(files, path)
	}
	return NewSnapshot(snapshot.WorkspaceEpoch, files, snapshot.ComputedBasisID+"-missing-"+adapter)
}

func capabilitySliceEntry(snapshot Snapshot, adapter string) (string, bool) {
	files := snapshot.Files
	if len(files) == 0 {
		files = snapshot.ContentOverlay
	}
	paths := make([]string, 0, len(files))
	for path := range files {
		switch adapter {
		case "dart":
			if strings.HasSuffix(path, ".dart") {
				paths = append(paths, path)
			}
		case "typescript":
			if strings.HasSuffix(path, ".ts") || strings.HasSuffix(path, ".tsx") || strings.HasSuffix(path, ".js") || strings.HasSuffix(path, ".jsx") {
				paths = append(paths, path)
			}
		case "go":
			if strings.HasSuffix(path, ".go") {
				paths = append(paths, path)
			}
		}
	}
	sort.Strings(paths)
	if len(paths) == 0 {
		return "", false
	}
	symbol := "main"
	if adapter == "typescript" {
		symbol = "probe"
	}
	return filepath.ToSlash(paths[0]) + "#" + symbol, true
}

func hasMeasuredCapabilityObservation(observations []evidence.Observation, want string) bool {
	for _, observation := range observations {
		if !observation.Measured {
			continue
		}
		kind := observation.Kind
		if want == "membership" && (kind == "membership" || kind == "source_membership") {
			return true
		}
		if kind == want {
			return true
		}
	}
	return false
}

func containsCapabilityReason(reasons []string, want string) bool {
	for _, reason := range reasons {
		if strings.Contains(reason, want) {
			return true
		}
	}
	return false
}

// CapabilityEvidenceFromVersionInfo converts the actual initialize response
// negotiated by a connection into the VS-02 measurement input. The caller
// must set conformancePassed only after the adapter's executable conformance
// probes have succeeded.
func CapabilityEvidenceFromVersionInfo(adapter string, info VersionInfo, conformancePassed bool) evidence.InitializeCapabilityEvidence {
	return evidence.InitializeCapabilityEvidence{
		Adapter: adapter, AdapterVersion: info.AdapterVersion, AnalyzerRevision: info.AnalyzerVersion,
		ProtocolVersion: info.ProtocolVersion, Cancellation: info.Capabilities.Cancellation,
		Progress: info.Capabilities.Progress, BatchAck: info.Capabilities.BatchAck,
		SnapshotOverlay: info.Capabilities.SnapshotOverlay, AnalysisMetadata: info.Capabilities.AnalysisMetadata,
		FlowContext:     info.Capabilities.FlowContext,
		MaxMessageBytes: info.Capabilities.MaxMessageBytes, ConformancePassed: conformancePassed,
	}
}

// MeasureCapability is retained for source compatibility with older callers,
// but a callback that only returns error cannot prove the claimed observation
// relations. Such calls therefore remain explicitly unsupported.
func (p *Pool) MeasureCapability(ctx context.Context, adapter string, probe func(*Conn) error) (evidence.CapabilityMeasurement, error) {
	measurement, _, err := p.measureCapabilityWithReport(ctx, adapter, func(_ context.Context, conn *Conn) (CapabilityConformanceEvidence, error) {
		if probe == nil {
			return CapabilityConformanceEvidence{}, nil
		}
		return CapabilityConformanceEvidence{}, probe(conn)
	})
	return measurement, err
}

// MeasureCapabilityWithConformance obtains a fresh initialize result and
// requires the executable probe to return a validated observation report.
func (p *Pool) MeasureCapabilityWithConformance(ctx context.Context, adapter string, probe CapabilityConformanceProbe) (evidence.CapabilityMeasurement, error) {
	measurement, _, err := p.measureCapabilityWithReport(ctx, adapter, probe)
	return measurement, err
}

func (p *Pool) measureCapabilityWithReport(ctx context.Context, adapter string, probe CapabilityConformanceProbe) (evidence.CapabilityMeasurement, CapabilityConformanceEvidence, error) {
	conn, err := p.Get(ctx)
	if err != nil {
		return evidence.CapabilityMeasurement{}, CapabilityConformanceEvidence{}, err
	}
	defer p.Put(conn)
	info := conn.Version()
	conformance := CapabilityConformanceEvidence{}
	var probeErr error
	if probe != nil {
		conformance, probeErr = probe(ctx, conn)
	}
	capEvidence := CapabilityEvidenceFromVersionInfo(adapter, info, probeErr == nil && conformance.complete())
	capEvidence.ConformanceProbeID = conformance.ProbeID
	capEvidence.ConformanceObservations = append([]string(nil), conformance.MeasuredObservations...)
	capEvidence.OpenOnMissing = conformance.OpenOnMissing
	capEvidence.ReadOnlySource = conformance.ReadOnlySource
	measurement := evidence.CapabilityMeasurementFromInitialize(capEvidence)
	if measurement.Status != "measured" {
		if probeErr != nil {
			return measurement, conformance, fmt.Errorf("adapter %s capability measurement is %s: %w", adapter, measurement.Status, probeErr)
		}
		return measurement, conformance, fmt.Errorf("adapter %s capability measurement is %s", adapter, measurement.Status)
	}
	return measurement, conformance, nil
}

// CapabilityRegistry returns the Core-owned capability publication store.
// Reading it does not execute adapter probes.
func (r *AdapterRegistry) CapabilityRegistry() *CapabilityRegistry {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.capabilityRegistry == nil {
		r.capabilityRegistry = NewCapabilityRegistry(DefaultCapabilityMeasurementTTL)
	}
	return r.capabilityRegistry
}

// RefreshCapability performs one explicit initialize plus executable
// conformance measurement and publishes its result. Ordinary Call requests
// never invoke this method implicitly.
func (r *AdapterRegistry) RefreshCapability(ctx context.Context, lang string, probe CapabilityConformanceProbe) (evidence.CapabilityMeasurement, error) {
	registry := r.CapabilityRegistry()
	pool, err := r.GetPool(lang)
	if err != nil {
		measurement := unsupportedCapabilityMeasurement(lang, "refresh_failed")
		_ = registry.publishMeasurement(measurement, time.Now().UTC(), CapabilityConformanceEvidence{})
		return measurement, err
	}
	measurement, proof, measureErr := pool.measureCapabilityWithReport(ctx, lang, probe)
	if measurement.Adapter == "" {
		measurement = unsupportedCapabilityMeasurement(lang, "refresh_failed")
	}
	publishErr := registry.publishMeasurement(measurement, time.Now().UTC(), proof)
	if measureErr != nil {
		return measurement, measureErr
	}
	if publishErr != nil {
		return measurement, publishErr
	}
	return measurement, nil
}

// CapabilityMatrix returns the latest cached initialize/conformance matrix.
func (r *AdapterRegistry) CapabilityMatrix() CapabilityMatrixSnapshot {
	return r.CapabilityRegistry().Snapshot()
}

// CapabilityMatrixJSON returns the schema-validated matrix for product
// consumers that publish or persist the capability document.
func (r *AdapterRegistry) CapabilityMatrixJSON() ([]byte, error) {
	return r.CapabilityRegistry().MatrixJSON()
}
