package semantic

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"sort"
	"strings"

	"codeflow/internal/contractharness"
	"codeflow/internal/releaseartifact"
)

const (
	ReleaseProfileV2SchemaID          = "https://codeflow.local/schemas/rflsc.release-profile.v2.schema.json"
	ScenarioManifestV2SchemaID        = "https://codeflow.local/schemas/rflsc.scenario-manifest.v2.schema.json"
	ExecutionReportV2SchemaID         = "https://codeflow.local/schemas/rflsc.execution-report.v2.schema.json"
	ReleaseBenchmarkReportV2SchemaID  = "https://codeflow.local/schemas/rflsc.release-benchmark-report.v2.schema.json"
	ReleaseCapabilityMatrixV2SchemaID = "https://codeflow.local/schemas/rflsc.release-capability-matrix.v2.schema.json"
	ReleaseSchemaVersion              = 2
)

// ReleaseEvaluationInput contains only declared, versioned evidence. The
// evaluator does not run scenarios or manufacture omitted values.
type ReleaseEvaluationInput struct {
	TargetVersion string                `json:"targetVersion"`
	EvaluationID  string                `json:"evaluationId"`
	EvaluatedAt   string                `json:"evaluatedAt"`
	Profile       *ReleaseProfile       `json:"profile"`
	Corpus        *ScenarioManifest     `json:"corpus"`
	Reports       []ExecutionReport     `json:"reports"`
	Thresholds    *ApprovedThresholdSet `json:"thresholds"`
	ChildEvidence []ChildEvidence       `json:"childEvidence"`
}

type ReleaseProfile struct {
	SchemaID      string                  `json:"schemaId"`
	SchemaVersion int                     `json:"schemaVersion"`
	TargetVersion string                  `json:"targetVersion"`
	ProfileID     string                  `json:"profileId"`
	ArtifactRef   string                  `json:"artifactRef"`
	OS            string                  `json:"os"`
	Architecture  string                  `json:"architecture"`
	Hardware      HardwareProfile         `json:"hardware"`
	Repository    RepositoryProfile       `json:"repository"`
	Toolchains    []ReleaseToolchain      `json:"toolchains"`
	ActiveScope   string                  `json:"activeScope"`
	LoadProfile   string                  `json:"loadProfile"`
	Browser       string                  `json:"browser,omitempty"`
	Capabilities  []CapabilityDeclaration `json:"capabilities"`
}

type HardwareProfile struct {
	CPU         string `json:"cpu"`
	LogicalCPUs int    `json:"logicalCpus"`
	MemoryBytes int64  `json:"memoryBytes"`
}

type RepositoryProfile struct {
	Shape      string   `json:"shape"`
	FileCount  int      `json:"fileCount"`
	Bytes      int64    `json:"bytes"`
	Languages  []string `json:"languages"`
	FixtureRef string   `json:"fixtureRef"`
}

type ReleaseToolchain struct {
	ToolchainID string `json:"toolchainId"`
	Name        string `json:"name"`
	Version     string `json:"version"`
	ArtifactRef string `json:"artifactRef"`
}

type CapabilityDeclaration struct {
	CapabilityID      string   `json:"capabilityId"`
	RequiredScenarios []string `json:"requiredScenarios"`
	ChildEvidenceIDs  []string `json:"childEvidenceIds"`
}

type ScenarioManifest struct {
	SchemaID      string            `json:"schemaId"`
	SchemaVersion int               `json:"schemaVersion"`
	CorpusID      string            `json:"corpusId"`
	CorpusVersion string            `json:"corpusVersion"`
	ArtifactRef   string            `json:"artifactRef"`
	ProfileID     string            `json:"profileId"`
	ProfileRef    string            `json:"profileRef"`
	Scenarios     []ReleaseScenario `json:"scenarios"`
}

type ReleaseScenario struct {
	ScenarioID    string   `json:"scenarioId"`
	Kind          string   `json:"kind"`
	CapabilityIDs []string `json:"capabilityIds"`
	FixtureRef    string   `json:"fixtureRef"`
}

type ExecutionReport struct {
	SchemaID        string               `json:"schemaId"`
	SchemaVersion   int                  `json:"schemaVersion"`
	ReportID        string               `json:"reportId"`
	ArtifactRef     string               `json:"artifactRef"`
	ProfileID       string               `json:"profileId"`
	ProfileRef      string               `json:"profileRef"`
	CorpusID        string               `json:"corpusId"`
	CorpusVersion   string               `json:"corpusVersion"`
	CorpusRef       string               `json:"corpusRef"`
	Command         string               `json:"command"`
	ExecutedAt      string               `json:"executedAt"`
	ToolchainIDs    []string             `json:"toolchainIds"`
	ScenarioResults []ScenarioExecution  `json:"scenarioResults"`
	MetricSeries    []MetricSeries       `json:"metricSeries"`
	InvariantChecks []HardInvariantCheck `json:"invariantChecks"`
}

type ScenarioExecution struct {
	ScenarioID    string   `json:"scenarioId"`
	TraceID       string   `json:"traceId"`
	TraceRef      string   `json:"traceRef"`
	ToolchainID   string   `json:"toolchainId"`
	CapabilityIDs []string `json:"capabilityIds"`
	Passed        bool     `json:"passed"`
	FailureReason string   `json:"failureReason,omitempty"`
	Recovery      string   `json:"recovery,omitempty"`
}

type MetricSeries struct {
	Metric       string              `json:"metric"`
	CapabilityID string              `json:"capabilityId"`
	Category     string              `json:"category"`
	Unit         string              `json:"unit"`
	Samples      []MetricObservation `json:"samples"`
}

type MetricObservation struct {
	ScenarioID  string  `json:"scenarioId"`
	TraceID     string  `json:"traceId"`
	TraceRef    string  `json:"traceRef"`
	ToolchainID string  `json:"toolchainId"`
	ProfileID   string  `json:"profileId"`
	EvidenceRef string  `json:"evidenceRef"`
	Value       float64 `json:"value"`
}

type HardInvariantCheck struct {
	Kind              string `json:"kind"`
	CapabilityID      string `json:"capabilityId"`
	ScenarioID        string `json:"scenarioId"`
	TraceID           string `json:"traceId"`
	EvidenceRef       string `json:"evidenceRef"`
	Result            string `json:"result"`
	FailureReason     string `json:"failureReason,omitempty"`
	RecoveryCondition string `json:"recoveryCondition,omitempty"`
}

type ApprovedThresholdSet struct {
	ProfileID     string              `json:"profileId"`
	ProfileRef    string              `json:"profileRef"`
	CorpusID      string              `json:"corpusId"`
	CorpusVersion string              `json:"corpusVersion"`
	CorpusRef     string              `json:"corpusRef"`
	ArtifactRef   string              `json:"artifactRef"`
	Thresholds    []ApprovedThreshold `json:"thresholds"`
}

type ApprovedThreshold struct {
	Metric       string  `json:"metric"`
	CapabilityID string  `json:"capabilityId"`
	Operator     string  `json:"operator"`
	Value        float64 `json:"value"`
	Unit         string  `json:"unit"`
	DecisionRef  string  `json:"decisionRef"`
}

// ApprovedThresholdDecision is trusted evaluator configuration. It is never
// accepted from ReleaseEvaluationInput.
type ApprovedThresholdDecision struct {
	DecisionRef   string  `json:"decisionRef"`
	Metric        string  `json:"metric"`
	CapabilityID  string  `json:"capabilityId"`
	ProfileID     string  `json:"profileId"`
	ProfileRef    string  `json:"profileRef"`
	CorpusID      string  `json:"corpusId"`
	CorpusVersion string  `json:"corpusVersion"`
	CorpusRef     string  `json:"corpusRef"`
	Operator      string  `json:"operator"`
	Value         float64 `json:"value"`
	Unit          string  `json:"unit"`
}

type ApprovedThresholdDecisionSet struct {
	ArtifactRef string                      `json:"artifactRef"`
	Decisions   []ApprovedThresholdDecision `json:"decisions"`
}

// ThresholdDecisionResolver resolves locally approved numeric decisions.
type ThresholdDecisionResolver interface {
	ResolveThresholdDecision(decisionRef string) (ApprovedThresholdDecision, bool)
}

type releaseThresholdDecisionRegistry struct {
	decisions map[string]ApprovedThresholdDecision
}

// NewReleaseThresholdDecisionRegistry creates trusted local evaluator
// configuration. Reserved parent decisions cannot be redefined.
func NewReleaseThresholdDecisionRegistry(records []ApprovedThresholdDecision) (ThresholdDecisionResolver, error) {
	registry := &releaseThresholdDecisionRegistry{decisions: make(map[string]ApprovedThresholdDecision, len(records))}
	for _, record := range records {
		if record.DecisionRef == "" || record.Metric == "" || record.CapabilityID == "" || record.ProfileID == "" || !isImmutableReleaseRef(record.ProfileRef) || record.CorpusID == "" || record.CorpusVersion == "" || !isImmutableReleaseRef(record.CorpusRef) || record.Operator == "" || record.Unit == "" || record.DecisionRef == "parent:Raw-16" || record.DecisionRef == "decision:D9" || record.DecisionRef == "decision:D36" {
			return nil, fmt.Errorf("invalid or reserved release threshold decision: %q", record.DecisionRef)
		}
		category, knownMetric := requiredReleaseMetricCategories[record.Metric]
		expectedOperator := "lte"
		if category == "quality" {
			expectedOperator = "gte"
		}
		if !knownMetric || record.Operator != expectedOperator || !validReleaseMetricValue(category, record.Value) {
			return nil, fmt.Errorf("invalid release threshold decision semantics: %s/%s", record.DecisionRef, record.Metric)
		}
		if _, duplicate := registry.decisions[record.DecisionRef]; duplicate {
			return nil, fmt.Errorf("duplicate release threshold decision: %s", record.DecisionRef)
		}
		registry.decisions[record.DecisionRef] = record
	}
	return registry, nil
}

// LoadReleaseThresholdDecisionRegistry loads a content-addressed local
// maintainer configuration. Request payloads cannot select this file.
func LoadReleaseThresholdDecisionRegistry(path string) (ThresholdDecisionResolver, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read release threshold decisions: %w", err)
	}
	var set ApprovedThresholdDecisionSet
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&set); err != nil {
		return nil, fmt.Errorf("decode release threshold decisions: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("decode release threshold decisions: expected one JSON object")
	}
	if !isImmutableReleaseRef(set.ArtifactRef) {
		return nil, fmt.Errorf("release threshold decisions lack an immutable artifactRef")
	}
	if err := releaseartifact.Verify(set, set.ArtifactRef); err != nil {
		return nil, fmt.Errorf("verify release threshold decisions: %w", err)
	}
	return NewReleaseThresholdDecisionRegistry(set.Decisions)
}

func (r *releaseThresholdDecisionRegistry) ResolveThresholdDecision(decisionRef string) (ApprovedThresholdDecision, bool) {
	if r == nil {
		return ApprovedThresholdDecision{}, false
	}
	record, ok := r.decisions[decisionRef]
	return record, ok
}

type ChildEvidence struct {
	EvidenceID         string                  `json:"evidenceId"`
	SliceID            string                  `json:"sliceId"`
	ContractRef        string                  `json:"contractRef"`
	ArtifactRef        string                  `json:"artifactRef"`
	EvidenceRegistryID string                  `json:"evidenceRegistryId"`
	TargetVersion      string                  `json:"targetVersion"`
	SourceRef          string                  `json:"sourceRef"`
	ImplementationRef  string                  `json:"implementationRef"`
	ExecutionID        string                  `json:"executionId"`
	BinaryDigest       string                  `json:"binaryDigest"`
	Acceptance         []AcceptanceExecution   `json:"acceptance"`
	Verification       []VerificationExecution `json:"verification"`
}

type AcceptanceExecution struct {
	AcceptanceID string `json:"acceptanceId"`
	TestID       string `json:"testId"`
	Package      string `json:"package"`
	RunCount     int    `json:"runCount"`
	PassCount    int    `json:"passCount"`
	Result       string `json:"result"`
	EvidenceRef  string `json:"evidenceRef"`
}

type VerificationExecution struct {
	CheckID      string `json:"checkId"`
	ExactCommand string `json:"exactCommand"`
	Result       string `json:"result"`
	EvidenceRef  string `json:"evidenceRef"`
}

type ReleaseEvaluation struct {
	BenchmarkReport  *ReleaseBenchmarkReportV2  `json:"benchmarkReport"`
	CapabilityMatrix *ReleaseCapabilityMatrixV2 `json:"capabilityMatrix"`
}

type ReleaseBenchmarkReportV2 struct {
	SchemaID          string            `json:"schemaId"`
	SchemaVersion     int               `json:"schemaVersion"`
	ReportID          string            `json:"reportId"`
	TargetVersion     string            `json:"targetVersion"`
	EvaluatedAt       string            `json:"evaluatedAt"`
	Status            string            `json:"status"`
	ProfileRef        string            `json:"profileRef,omitempty"`
	CorpusRef         string            `json:"corpusRef,omitempty"`
	ExecutionRefs     []string          `json:"executionRefs"`
	ThresholdRef      string            `json:"thresholdRef,omitempty"`
	ChildEvidenceRefs []string          `json:"childEvidenceRefs"`
	Metrics           []EvaluatedMetric `json:"metrics"`
	Gates             []EvaluatedGate   `json:"gates"`
	Reasons           []string          `json:"reasons"`
	ReleaseReady      bool              `json:"releaseReady"`
}

type EvaluatedMetric struct {
	Metric       string   `json:"metric"`
	CapabilityID string   `json:"capabilityId"`
	Category     string   `json:"category"`
	Unit         string   `json:"unit"`
	Aggregation  string   `json:"aggregation"`
	Value        float64  `json:"value"`
	ProfileID    string   `json:"profileId"`
	ScenarioIDs  []string `json:"scenarioIds"`
	TraceIDs     []string `json:"traceIds"`
	ToolchainIDs []string `json:"toolchainIds"`
	EvidenceRefs []string `json:"evidenceRefs"`
}

type EvaluatedGate struct {
	Metric       string   `json:"metric"`
	CapabilityID string   `json:"capabilityId"`
	Passed       bool     `json:"passed"`
	DecisionRef  string   `json:"decisionRef"`
	ThresholdRef string   `json:"thresholdRef"`
	ProfileID    string   `json:"profileId"`
	ScenarioIDs  []string `json:"scenarioIds"`
	TraceIDs     []string `json:"traceIds"`
	ToolchainIDs []string `json:"toolchainIds"`
	EvidenceRefs []string `json:"evidenceRefs"`
	Reason       string   `json:"reason,omitempty"`
}

type ReleaseCapabilityMatrixV2 struct {
	SchemaID      string            `json:"schemaId"`
	SchemaVersion int               `json:"schemaVersion"`
	MatrixID      string            `json:"matrixId"`
	TargetVersion string            `json:"targetVersion"`
	ProfileID     string            `json:"profileId,omitempty"`
	CorpusID      string            `json:"corpusId,omitempty"`
	CorpusVersion string            `json:"corpusVersion,omitempty"`
	Capabilities  []CapabilityState `json:"capabilities"`
	ReleaseReady  bool              `json:"releaseReady"`
}

type CapabilityState struct {
	CapabilityID       string   `json:"capabilityId"`
	State              string   `json:"state"`
	ProfileID          string   `json:"profileId,omitempty"`
	CorpusID           string   `json:"corpusId,omitempty"`
	CorpusVersion      string   `json:"corpusVersion,omitempty"`
	ToolchainIDs       []string `json:"toolchainIds"`
	ScenarioIDs        []string `json:"scenarioIds"`
	EvidenceRefs       []string `json:"evidenceRefs"`
	FailureReasons     []string `json:"failureReasons"`
	RecoveryConditions []string `json:"recoveryConditions"`
}

// EvaluateReleaseCapability evaluates pre-existing evidence only.
func EvaluateReleaseCapability(input ReleaseEvaluationInput) (*ReleaseEvaluation, error) {
	return EvaluateReleaseCapabilityWithThresholdDecisions(input, nil)
}

// EvaluateReleaseCapabilityWithThresholdDecisions evaluates evidence with a
// trusted local decision resolver in addition to the two built-in parent SLOs.
func EvaluateReleaseCapabilityWithThresholdDecisions(input ReleaseEvaluationInput, resolver ThresholdDecisionResolver) (*ReleaseEvaluation, error) {
	missing := make([]string, 0, 5)
	if input.Profile == nil {
		missing = append(missing, "release profile")
	}
	if input.Corpus == nil {
		missing = append(missing, "versioned corpus")
	}
	if len(input.Reports) == 0 {
		missing = append(missing, "executed report")
	}
	if input.Thresholds == nil {
		missing = append(missing, "approved thresholds")
	}
	if len(input.ChildEvidence) == 0 {
		missing = append(missing, "child evidence")
	}
	if len(missing) > 0 {
		return validateReleaseEvaluationOutput(incompleteReleaseEvaluation(input, missing))
	}
	if invalid := validateReleaseEvidenceIdentities(input); len(invalid) > 0 {
		return validateReleaseEvaluationOutput(incompleteReleaseEvaluation(input, invalid))
	}
	if missingScenarios := validateReleaseScenarioCoverage(input); len(missingScenarios) > 0 {
		return validateReleaseEvaluationOutput(incompleteReleaseEvaluation(input, missingScenarios))
	}
	if missingChecks := validateReleaseInvariantCoverage(input); len(missingChecks) > 0 {
		return validateReleaseEvaluationOutput(incompleteReleaseEvaluation(input, missingChecks))
	}
	childIncomplete, childFailures := evaluateReleaseChildEvidence(input)
	if len(childIncomplete) > 0 {
		return validateReleaseEvaluationOutput(incompleteReleaseEvaluation(input, childIncomplete))
	}
	metrics, metricReasons := evaluateReleaseMetrics(input)
	if len(metricReasons) > 0 {
		return validateReleaseEvaluationOutput(incompleteReleaseEvaluation(input, metricReasons))
	}
	gates, thresholdReasons := evaluateReleaseGates(input, metrics, resolver)
	if len(thresholdReasons) > 0 {
		return validateReleaseEvaluationOutput(incompleteReleaseEvaluation(input, thresholdReasons))
	}
	result := measuredReleaseEvaluation(input, metrics)
	applyReleaseGates(result, input, gates)
	applyReleaseChildFailures(result, input, childFailures)
	return validateReleaseEvaluationOutput(result)
}

func validateReleaseInvariantCoverage(input ReleaseEvaluationInput) []string {
	counts := map[string]int{}
	for _, report := range input.Reports {
		for _, check := range report.InvariantChecks {
			counts[check.CapabilityID+"\x00"+check.Kind]++
		}
	}
	reasons := []string{}
	for _, capability := range input.Profile.Capabilities {
		for kind := range hardReleaseFailureKinds {
			count := counts[capability.CapabilityID+"\x00"+kind]
			if count != 1 {
				reasons = append(reasons, fmt.Sprintf("capability %s requires exactly one executed %s invariant check; observed %d", capability.CapabilityID, kind, count))
			}
		}
	}
	sort.Strings(reasons)
	return reasons
}

func validateReleaseEvaluationOutput(result *ReleaseEvaluation) (*ReleaseEvaluation, error) {
	benchmark, err := json.Marshal(result.BenchmarkReport)
	if err != nil {
		return nil, fmt.Errorf("encode release benchmark report: %w", err)
	}
	if err := contractharness.ValidateVS10Contract(ReleaseBenchmarkReportV2SchemaID, benchmark); err != nil {
		return nil, fmt.Errorf("invalid release benchmark report: %w", err)
	}
	matrix, err := json.Marshal(result.CapabilityMatrix)
	if err != nil {
		return nil, fmt.Errorf("encode release capability matrix: %w", err)
	}
	if err := contractharness.ValidateVS10Contract(ReleaseCapabilityMatrixV2SchemaID, matrix); err != nil {
		return nil, fmt.Errorf("invalid release capability matrix: %w", err)
	}
	return result, nil
}

var requiredReleaseScenarioKindSet = map[string]bool{
	"rapid_edit":    true,
	"multi_file":    true,
	"rename_delete": true,
	"syntax_error":  true,
	"branch_switch": true,
	"watcher_gap":   true,
	"open_closure":  true,
	"late_result":   true,
	"cas_conflict":  true,
	"adapter_crash": true,
	"model_crash":   true,
	"reconnect":     true,
}

var requiredReleaseMetricCategories = map[string]string{
	"activity_latency_ms":       "latency",
	"current_or_gap_latency_ms": "latency",
	"precision":                 "quality",
	"recall":                    "quality",
	"semantic_delta":            "quality",
	"alignment_validity":        "quality",
	"unknown_coverage":          "quality",
	"comprehension":             "quality",
	"peak_memory_bytes":         "resource",
}

func validateReleaseScenarioCoverage(input ReleaseEvaluationInput) []string {
	kinds := map[string]bool{}
	scenarioKinds := map[string]string{}
	declaredForCapability := map[string]map[string]bool{}
	for _, scenario := range input.Corpus.Scenarios {
		kinds[scenario.Kind] = true
		scenarioKinds[scenario.ScenarioID] = scenario.Kind
		for _, capabilityID := range scenario.CapabilityIDs {
			if declaredForCapability[capabilityID] == nil {
				declaredForCapability[capabilityID] = map[string]bool{}
			}
			declaredForCapability[capabilityID][scenario.Kind] = true
		}
	}
	executedKinds := map[string]bool{}
	executedForCapability := map[string]map[string]bool{}
	for _, report := range input.Reports {
		for _, result := range report.ScenarioResults {
			kind := scenarioKinds[result.ScenarioID]
			executedKinds[kind] = true
			for _, capabilityID := range result.CapabilityIDs {
				if executedForCapability[capabilityID] == nil {
					executedForCapability[capabilityID] = map[string]bool{}
				}
				executedForCapability[capabilityID][kind] = true
			}
		}
	}
	reasons := []string{}
	for kind := range requiredReleaseScenarioKindSet {
		if !kinds[kind] {
			reasons = append(reasons, "required scenario is absent from the versioned corpus: "+kind)
			continue
		}
		if !executedKinds[kind] {
			reasons = append(reasons, "required scenario has no executed report: "+kind)
		}
	}
	for _, capability := range input.Profile.Capabilities {
		for _, kind := range capability.RequiredScenarios {
			if !declaredForCapability[capability.CapabilityID][kind] || !executedForCapability[capability.CapabilityID][kind] {
				reasons = append(reasons, "capability "+capability.CapabilityID+" lacks required scenario evidence: "+kind)
			}
		}
	}
	sort.Strings(reasons)
	return uniqueReleaseStrings(reasons)
}

type releaseChildFailure struct {
	reason   string
	recovery string
}

type releaseChildRequirement struct {
	sliceID         string
	contractRef     string
	registryID      string
	acceptanceCount int
}

var requiredReleaseChildren = []releaseChildRequirement{
	{sliceID: "VS-01", contractRef: "REQUESTED-FLOW-LIVE-SEMANTIC-COMPILER-R2-VS-01", registryID: "rflsc-r2-vs-01", acceptanceCount: 10},
	{sliceID: "VS-02", contractRef: "REQUESTED-FLOW-LIVE-SEMANTIC-COMPILER-R2-VS-02", registryID: "rflsc-r2-vs-02", acceptanceCount: 11},
	{sliceID: "VS-03", contractRef: "REQUESTED-FLOW-LIVE-SEMANTIC-COMPILER-R2-VS-03", registryID: "rflsc-r2-vs-03", acceptanceCount: 15},
	{sliceID: "VS-04", contractRef: "REQUESTED-FLOW-LIVE-SEMANTIC-COMPILER-R2-VS-04", registryID: "rflsc-r2-vs-04", acceptanceCount: 14},
	{sliceID: "VS-05", contractRef: "REQUESTED-FLOW-LIVE-SEMANTIC-COMPILER-R2-VS-05", registryID: "rflsc-r2-vs-05", acceptanceCount: 7},
	{sliceID: "VS-06", contractRef: "REQUESTED-FLOW-LIVE-SEMANTIC-COMPILER-R2-VS-06", registryID: "rflsc-r2-vs-06", acceptanceCount: 9},
	{sliceID: "VS-07", contractRef: "REQUESTED-FLOW-LIVE-SEMANTIC-COMPILER-R2-VS-07", registryID: "rflsc-r2-vs-07", acceptanceCount: 7},
	{sliceID: "VS-08", contractRef: "REQUESTED-FLOW-LIVE-SEMANTIC-COMPILER-R2-VS-08", registryID: "rflsc-r2-vs-08", acceptanceCount: 10},
	{sliceID: "VS-09", contractRef: "REQUESTED-FLOW-LIVE-SEMANTIC-COMPILER-R2-VS-09", registryID: "rflsc-r2-vs-09", acceptanceCount: 13},
}

var requiredChildVerificationChecks = map[string][]string{
	"VS-01": {"registry", "acceptance", "protocol", "static", "build", "regression", "security", "migration", "concurrency"},
	"VS-02": {"registry", "acceptance", "dart", "typescript", "go", "static", "build", "security", "concurrency_reliability", "regression"},
	"VS-03": {"registry", "acceptance", "contract", "static", "build", "concurrency", "reliability", "browser", "a11y", "regression"},
	"VS-04": {"registry", "acceptance", "contract", "static", "build", "regression", "browser", "a11y", "security"},
	"VS-05": {"registry", "acceptance", "full_slice", "static", "build", "security", "regression"},
	"VS-06": {"registry", "acceptance", "full_slice", "static", "build", "security", "concurrency_reliability", "regression"},
	"VS-07": {"registry", "acceptance", "full_slice", "static", "build", "security", "browser", "a11y", "regression"},
	"VS-08": {"registry", "acceptance", "full_slice", "static", "build", "security", "concurrency_reliability", "browser", "a11y", "regression"},
	"VS-09": {"registry", "acceptance", "full_slice", "static", "build", "security", "migration_persistence", "concurrency_reliability", "browser", "a11y", "regression"},
}

func evaluateReleaseChildEvidence(input ReleaseEvaluationInput) ([]string, map[string][]releaseChildFailure) {
	incomplete := []string{}
	failures := map[string][]releaseChildFailure{}
	byID := map[string]ChildEvidence{}
	bySlice := map[string]ChildEvidence{}
	requirementBySlice := map[string]releaseChildRequirement{}
	for _, requirement := range requiredReleaseChildren {
		requirementBySlice[requirement.sliceID] = requirement
	}
	for _, evidence := range input.ChildEvidence {
		if _, duplicate := byID[evidence.EvidenceID]; duplicate {
			incomplete = append(incomplete, "duplicate child evidence identity: "+evidence.EvidenceID)
		}
		byID[evidence.EvidenceID] = evidence
		if _, duplicate := bySlice[evidence.SliceID]; duplicate {
			incomplete = append(incomplete, "duplicate child slice evidence: "+evidence.SliceID)
		}
		bySlice[evidence.SliceID] = evidence
		if evidence.EvidenceRegistryID == "" || evidence.ExecutionID == "" || !isImmutableReleaseRef(evidence.BinaryDigest) {
			incomplete = append(incomplete, "child evidence lacks an executed registry or binary identity: "+evidence.EvidenceID)
		}
		if len(evidence.Acceptance) == 0 {
			incomplete = append(incomplete, "child evidence has no executed acceptance: "+evidence.EvidenceID)
		}
		if len(evidence.Verification) == 0 {
			incomplete = append(incomplete, "child evidence has no Verification Plan execution: "+evidence.EvidenceID)
		}
		seenAcceptance := map[string]bool{}
		for _, acceptance := range evidence.Acceptance {
			if acceptance.AcceptanceID == "" || acceptance.TestID == "" || acceptance.Package == "" || acceptance.RunCount != 1 || acceptance.PassCount < 0 || acceptance.PassCount > 1 {
				incomplete = append(incomplete, "child acceptance did not execute exactly once: "+evidence.EvidenceID)
				continue
			}
			if seenAcceptance[acceptance.AcceptanceID] {
				incomplete = append(incomplete, "duplicate child acceptance identity: "+acceptance.AcceptanceID)
			}
			seenAcceptance[acceptance.AcceptanceID] = true
			if acceptance.Result == "pass" && acceptance.PassCount == 1 {
				continue
			}
			if acceptance.Result == "fail" && acceptance.PassCount == 0 {
				failures[evidence.EvidenceID] = append(failures[evidence.EvidenceID], releaseChildFailure{
					reason: "child acceptance failed: " + acceptance.AcceptanceID, recovery: "fix the owning slice and rerun " + acceptance.TestID,
				})
				continue
			}
			incomplete = append(incomplete, "child acceptance result is not an observed pass or fail: "+acceptance.AcceptanceID)
		}
		seenChecks := map[string]bool{}
		for _, check := range evidence.Verification {
			if check.CheckID == "" || check.ExactCommand == "" || !isImmutableReleaseRef(check.EvidenceRef) || seenChecks[check.CheckID] {
				incomplete = append(incomplete, "child Verification Plan evidence is missing, mutable or duplicate: "+evidence.EvidenceID)
				continue
			}
			seenChecks[check.CheckID] = true
			if check.Result == "pass" {
				continue
			}
			if check.Result == "fail" {
				failures[evidence.EvidenceID] = append(failures[evidence.EvidenceID], releaseChildFailure{
					reason: "child verification failed: " + check.CheckID, recovery: "fix the owning slice and rerun " + check.ExactCommand,
				})
				continue
			}
			incomplete = append(incomplete, "child verification result is not executed: "+check.CheckID)
		}
	}
	for sliceID, evidence := range bySlice {
		requirement, expectedSlice := requirementBySlice[sliceID]
		if !expectedSlice {
			incomplete = append(incomplete, "child evidence names an unknown active R2 slice: "+sliceID)
			continue
		}
		if evidence.ContractRef != requirement.contractRef || evidence.EvidenceRegistryID != requirement.registryID {
			incomplete = append(incomplete, "child slice does not bind the active R2 contract and evidence registry: "+requirement.sliceID)
		}
		expectedAcceptance := map[string]string{}
		compactSliceID := strings.ReplaceAll(requirement.sliceID, "-", "")
		for criterion := 1; criterion <= requirement.acceptanceCount; criterion++ {
			acceptanceID := fmt.Sprintf("%s-A%d", compactSliceID, criterion)
			expectedAcceptance[acceptanceID] = fmt.Sprintf("TestRFLSCR2%s_A%02d", compactSliceID, criterion)
		}
		observedAcceptance := map[string]AcceptanceExecution{}
		for _, acceptance := range evidence.Acceptance {
			observedAcceptance[acceptance.AcceptanceID] = acceptance
		}
		for acceptanceID, testID := range expectedAcceptance {
			acceptance, present := observedAcceptance[acceptanceID]
			if !present || acceptance.TestID != testID {
				incomplete = append(incomplete, "required child acceptance is missing or misidentified: "+acceptanceID)
			}
		}
		for acceptanceID := range observedAcceptance {
			if _, expected := expectedAcceptance[acceptanceID]; !expected {
				incomplete = append(incomplete, "unexpected child acceptance identity: "+acceptanceID)
			}
		}
		expectedChecks := map[string]bool{}
		for _, checkID := range requiredChildVerificationChecks[requirement.sliceID] {
			expectedChecks[checkID] = true
		}
		observedChecks := map[string]bool{}
		for _, check := range evidence.Verification {
			observedChecks[check.CheckID] = true
		}
		for checkID := range expectedChecks {
			if !observedChecks[checkID] {
				incomplete = append(incomplete, "required child Verification Plan check is missing: "+requirement.sliceID+"/"+checkID)
			}
		}
		for checkID := range observedChecks {
			if !expectedChecks[checkID] {
				incomplete = append(incomplete, "unexpected child Verification Plan check identity: "+requirement.sliceID+"/"+checkID)
			}
		}
	}
	for _, capability := range input.Profile.Capabilities {
		if len(capability.ChildEvidenceIDs) == 0 {
			incomplete = append(incomplete, "capability has no declared child evidence: "+capability.CapabilityID)
		}
		seenEvidenceIDs := map[string]bool{}
		for _, evidenceID := range capability.ChildEvidenceIDs {
			if seenEvidenceIDs[evidenceID] {
				incomplete = append(incomplete, "capability "+capability.CapabilityID+" duplicates child evidence "+evidenceID)
			}
			seenEvidenceIDs[evidenceID] = true
			if _, ok := byID[evidenceID]; !ok {
				incomplete = append(incomplete, "capability "+capability.CapabilityID+" lacks child evidence "+evidenceID)
			}
		}
		if capability.CapabilityID == "full-product-release" {
			if len(capability.ChildEvidenceIDs) != len(requiredReleaseChildren) {
				incomplete = append(incomplete, "full-product-release requires exactly VS-01 through VS-09 child evidence")
			}
			for _, requirement := range requiredReleaseChildren {
				evidence, ok := bySlice[requirement.sliceID]
				if !ok || !releaseStringContains(capability.ChildEvidenceIDs, evidence.EvidenceID) {
					incomplete = append(incomplete, "full-product-release narrows away required child slice "+requirement.sliceID)
				}
			}
		}
	}
	sort.Strings(incomplete)
	return uniqueReleaseStrings(incomplete), failures
}

func applyReleaseChildFailures(result *ReleaseEvaluation, input ReleaseEvaluationInput, failures map[string][]releaseChildFailure) {
	if len(failures) == 0 {
		return
	}
	affected := false
	for index := range result.CapabilityMatrix.Capabilities {
		capability := &result.CapabilityMatrix.Capabilities[index]
		if capability.State == "blocked" {
			continue
		}
		var declaration CapabilityDeclaration
		for _, candidate := range input.Profile.Capabilities {
			if candidate.CapabilityID == capability.CapabilityID {
				declaration = candidate
				break
			}
		}
		for _, evidenceID := range declaration.ChildEvidenceIDs {
			for _, failure := range failures[evidenceID] {
				affected = true
				capability.State = "partial"
				capability.FailureReasons = append(capability.FailureReasons, failure.reason)
				capability.RecoveryConditions = append(capability.RecoveryConditions, failure.recovery)
				result.BenchmarkReport.Reasons = append(result.BenchmarkReport.Reasons, failure.reason)
			}
		}
		capability.FailureReasons = uniqueReleaseStrings(capability.FailureReasons)
		capability.RecoveryConditions = uniqueReleaseStrings(capability.RecoveryConditions)
	}
	if affected {
		result.BenchmarkReport.Status = "failed"
		result.BenchmarkReport.ReleaseReady = false
		result.CapabilityMatrix.ReleaseReady = false
	}
	result.BenchmarkReport.Reasons = uniqueReleaseStrings(result.BenchmarkReport.Reasons)
}

func evaluateReleaseGates(input ReleaseEvaluationInput, metrics []EvaluatedMetric, resolver ThresholdDecisionResolver) ([]EvaluatedGate, []string) {
	metricsByCapability := map[string]EvaluatedMetric{}
	for _, metric := range metrics {
		metricsByCapability[metric.CapabilityID+"\x00"+metric.Metric] = metric
	}
	capabilities := map[string]bool{}
	for _, capability := range input.Profile.Capabilities {
		capabilities[capability.CapabilityID] = true
	}
	thresholds := map[string]ApprovedThreshold{}
	reasons := []string{}
	for _, threshold := range input.Thresholds.Thresholds {
		metric, metricOK := metricsByCapability[threshold.CapabilityID+"\x00"+threshold.Metric]
		expectedOperator := "lte"
		if metric.Category == "quality" {
			expectedOperator = "gte"
		}
		if !metricOK || !capabilities[threshold.CapabilityID] || threshold.Unit != metric.Unit || threshold.Operator != expectedOperator || !validReleaseMetricValue(metric.Category, threshold.Value) || !releaseThresholdDecisionMatches(input, threshold, resolver) {
			reasons = append(reasons, "approved threshold is invalid or outside the declared metric/capability scope: "+threshold.CapabilityID+"/"+threshold.Metric)
			continue
		}
		key := threshold.CapabilityID + "\x00" + threshold.Metric
		if _, duplicate := thresholds[key]; duplicate {
			reasons = append(reasons, "duplicate approved threshold: "+threshold.CapabilityID+"/"+threshold.Metric)
		}
		thresholds[key] = threshold
	}
	for capabilityID := range capabilities {
		for metric := range requiredReleaseMetricCategories {
			key := capabilityID + "\x00" + metric
			if _, ok := thresholds[key]; !ok {
				reasons = append(reasons, "approved threshold is absent: "+capabilityID+"/"+metric)
			}
		}
	}
	if len(reasons) > 0 {
		sort.Strings(reasons)
		return nil, uniqueReleaseStrings(reasons)
	}
	keys := make([]string, 0, len(thresholds))
	for key := range thresholds {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	gates := make([]EvaluatedGate, 0, len(keys))
	for _, key := range keys {
		threshold := thresholds[key]
		metric := metricsByCapability[key]
		passed := metric.Value <= threshold.Value
		if threshold.Operator == "gte" {
			passed = metric.Value >= threshold.Value
		}
		reason := ""
		if !passed {
			reason = fmt.Sprintf("%s %s %g did not satisfy approved threshold %s %g", threshold.CapabilityID, threshold.Metric, metric.Value, threshold.Operator, threshold.Value)
		}
		gates = append(gates, EvaluatedGate{
			Metric: threshold.Metric, CapabilityID: threshold.CapabilityID, Passed: passed,
			DecisionRef: threshold.DecisionRef, ThresholdRef: input.Thresholds.ArtifactRef, ProfileID: input.Profile.ProfileID,
			ScenarioIDs: append([]string(nil), metric.ScenarioIDs...), TraceIDs: append([]string(nil), metric.TraceIDs...),
			ToolchainIDs: append([]string(nil), metric.ToolchainIDs...), EvidenceRefs: append([]string(nil), metric.EvidenceRefs...), Reason: reason,
		})
	}
	return gates, nil
}

func releaseThresholdDecisionMatches(input ReleaseEvaluationInput, threshold ApprovedThreshold, resolver ThresholdDecisionResolver) bool {
	builtins := map[string]ApprovedThresholdDecision{
		"parent:Raw-16": {DecisionRef: "parent:Raw-16", Metric: "activity_latency_ms", Operator: "lte", Value: 300, Unit: "ms"},
		"decision:D9":   {DecisionRef: "decision:D9", Metric: "current_or_gap_latency_ms", Operator: "lte", Value: 3000, Unit: "ms"},
	}
	decision, builtin := builtins[threshold.DecisionRef]
	ok := builtin
	if !builtin && resolver != nil {
		decision, ok = resolver.ResolveThresholdDecision(threshold.DecisionRef)
	}
	if !ok || decision.DecisionRef != threshold.DecisionRef || decision.Metric != threshold.Metric || decision.Operator != threshold.Operator || decision.Value != threshold.Value || decision.Unit != threshold.Unit {
		return false
	}
	return builtin || decision.CapabilityID == threshold.CapabilityID && decision.ProfileID == input.Profile.ProfileID && decision.ProfileRef == input.Profile.ArtifactRef && decision.CorpusID == input.Corpus.CorpusID && decision.CorpusVersion == input.Corpus.CorpusVersion && decision.CorpusRef == input.Corpus.ArtifactRef
}

func applyReleaseGates(result *ReleaseEvaluation, input ReleaseEvaluationInput, gates []EvaluatedGate) {
	result.BenchmarkReport.Gates = gates
	allReady := len(result.CapabilityMatrix.Capabilities) > 0
	for index := range result.CapabilityMatrix.Capabilities {
		capability := &result.CapabilityMatrix.Capabilities[index]
		if capability.State == "blocked" || capability.State == "unsupported" || capability.State == "partial" {
			allReady = false
			continue
		}
		passed := true
		for _, gate := range gates {
			if gate.CapabilityID != capability.CapabilityID {
				continue
			}
			capability.EvidenceRefs = append(capability.EvidenceRefs, gate.EvidenceRefs...)
			capability.EvidenceRefs = append(capability.EvidenceRefs, gate.ThresholdRef)
			if !gate.Passed {
				passed = false
				capability.FailureReasons = append(capability.FailureReasons, gate.Reason)
				capability.RecoveryConditions = append(capability.RecoveryConditions, "meet the approved threshold and rerun the declared corpus")
				result.BenchmarkReport.Reasons = append(result.BenchmarkReport.Reasons, gate.Reason)
			}
		}
		for _, evidenceID := range releaseCapabilityDeclaration(input.Profile.Capabilities, capability.CapabilityID).ChildEvidenceIDs {
			for _, evidence := range input.ChildEvidence {
				if evidence.EvidenceID == evidenceID {
					capability.EvidenceRefs = append(capability.EvidenceRefs, evidence.ArtifactRef)
				}
			}
		}
		capability.EvidenceRefs = uniqueReleaseStrings(capability.EvidenceRefs)
		if passed {
			capability.State = "ga"
			capability.FailureReasons = []string{}
			capability.RecoveryConditions = []string{}
		} else {
			capability.State = "partial"
			allReady = false
		}
	}
	result.BenchmarkReport.Reasons = uniqueReleaseStrings(result.BenchmarkReport.Reasons)
	result.BenchmarkReport.ReleaseReady = allReady
	result.CapabilityMatrix.ReleaseReady = allReady
	if allReady {
		result.BenchmarkReport.Status = "passed"
		result.BenchmarkReport.Reasons = []string{}
	} else if result.BenchmarkReport.Status != "failed" {
		result.BenchmarkReport.Status = "failed"
	}
}

func releaseCapabilityDeclaration(values []CapabilityDeclaration, capabilityID string) CapabilityDeclaration {
	for _, value := range values {
		if value.CapabilityID == capabilityID {
			return value
		}
	}
	return CapabilityDeclaration{}
}

type ReleaseEvaluationError struct {
	Category string
	Field    string
	Reason   string
}

func (e *ReleaseEvaluationError) Error() string {
	return e.Category + ": " + e.Field + ": " + e.Reason
}

func incompleteReleaseEvaluation(input ReleaseEvaluationInput, missing []string) *ReleaseEvaluation {
	profileID, corpusID, corpusVersion := "", "", ""
	capabilities := []CapabilityState{}
	if input.Profile != nil {
		profileID = input.Profile.ProfileID
		for _, declaration := range input.Profile.Capabilities {
			capabilities = append(capabilities, CapabilityState{
				CapabilityID: declaration.CapabilityID,
				State:        "unsupported",
				ProfileID:    profileID,
				ToolchainIDs: []string{},
				ScenarioIDs:  []string{},
				EvidenceRefs: []string{},
				FailureReasons: []string{
					"release evaluation is incomplete",
				},
				RecoveryConditions: []string{
					"supply " + strings.Join(missing, ", "),
				},
			})
		}
	}
	if input.Corpus != nil {
		corpusID = input.Corpus.CorpusID
		corpusVersion = input.Corpus.CorpusVersion
	}
	return &ReleaseEvaluation{
		BenchmarkReport: &ReleaseBenchmarkReportV2{
			SchemaID: ReleaseBenchmarkReportV2SchemaID, SchemaVersion: ReleaseSchemaVersion,
			ReportID: input.EvaluationID, TargetVersion: input.TargetVersion, EvaluatedAt: input.EvaluatedAt,
			Status: "incomplete", ExecutionRefs: []string{}, ChildEvidenceRefs: []string{}, Metrics: []EvaluatedMetric{}, Gates: []EvaluatedGate{}, Reasons: append([]string(nil), missing...), ReleaseReady: false,
		},
		CapabilityMatrix: &ReleaseCapabilityMatrixV2{
			SchemaID: ReleaseCapabilityMatrixV2SchemaID, SchemaVersion: ReleaseSchemaVersion,
			MatrixID: input.EvaluationID, TargetVersion: input.TargetVersion, ProfileID: profileID, CorpusID: corpusID, CorpusVersion: corpusVersion,
			Capabilities: capabilities, ReleaseReady: false,
		},
	}
}

func measuredReleaseEvaluation(input ReleaseEvaluationInput, metrics []EvaluatedMetric) *ReleaseEvaluation {
	executionRefs := make([]string, 0, len(input.Reports))
	for _, report := range input.Reports {
		executionRefs = append(executionRefs, report.ArtifactRef)
	}
	childRefs := make([]string, 0, len(input.ChildEvidence))
	for _, evidence := range input.ChildEvidence {
		childRefs = append(childRefs, evidence.ArtifactRef)
	}
	capabilities := make([]CapabilityState, 0, len(input.Profile.Capabilities))
	for _, declaration := range input.Profile.Capabilities {
		state := CapabilityState{
			CapabilityID: declaration.CapabilityID, State: "unsupported", ProfileID: input.Profile.ProfileID,
			CorpusID: input.Corpus.CorpusID, CorpusVersion: input.Corpus.CorpusVersion,
			FailureReasons: []string{"capability has no measured scenario execution"}, RecoveryConditions: []string{"execute the declared scenario corpus for this capability"},
		}
		measured, failed := 0, 0
		failedReasons, failedRecovery := []string{}, []string{}
		for _, report := range input.Reports {
			for _, execution := range report.ScenarioResults {
				if !releaseStringContains(execution.CapabilityIDs, declaration.CapabilityID) {
					continue
				}
				measured++
				state.ScenarioIDs = append(state.ScenarioIDs, execution.ScenarioID)
				state.ToolchainIDs = append(state.ToolchainIDs, execution.ToolchainID)
				state.EvidenceRefs = append(state.EvidenceRefs, execution.TraceRef, report.ArtifactRef)
				if !execution.Passed {
					failed++
					reason := execution.FailureReason
					if reason == "" {
						reason = "scenario failed without a declared reason: " + execution.ScenarioID
					}
					recovery := execution.Recovery
					if recovery == "" {
						recovery = "fix and rerun scenario: " + execution.ScenarioID
					}
					failedReasons = append(failedReasons, reason)
					failedRecovery = append(failedRecovery, recovery)
				}
			}
		}
		if measured > 0 {
			state.State = "experimental"
			state.FailureReasons = []string{"capability gates have not been evaluated"}
			state.RecoveryConditions = []string{"evaluate approved thresholds and child evidence"}
		}
		if failed > 0 {
			state.State = "partial"
			state.FailureReasons = failedReasons
			state.RecoveryConditions = failedRecovery
		}
		for _, report := range input.Reports {
			for _, check := range report.InvariantChecks {
				if check.CapabilityID != declaration.CapabilityID {
					continue
				}
				state.EvidenceRefs = append(state.EvidenceRefs, check.EvidenceRef)
				if check.Result != "fail" {
					continue
				}
				if state.State != "blocked" {
					state.State = "blocked"
					state.FailureReasons = nil
					state.RecoveryConditions = nil
				}
				state.ScenarioIDs = append(state.ScenarioIDs, check.ScenarioID)
				state.FailureReasons = append(state.FailureReasons, check.FailureReason)
				state.RecoveryConditions = append(state.RecoveryConditions, check.RecoveryCondition)
			}
		}
		state.ScenarioIDs = uniqueReleaseStrings(state.ScenarioIDs)
		state.ToolchainIDs = uniqueReleaseStrings(state.ToolchainIDs)
		state.EvidenceRefs = uniqueReleaseStrings(state.EvidenceRefs)
		state.FailureReasons = uniqueReleaseStrings(state.FailureReasons)
		state.RecoveryConditions = uniqueReleaseStrings(state.RecoveryConditions)
		capabilities = append(capabilities, state)
	}
	status := "evaluated"
	reasons := []string{"capability gates have not been evaluated"}
	for _, capability := range capabilities {
		if capability.State == "blocked" {
			status = "failed"
			reasons = append(reasons, capability.FailureReasons...)
		}
	}
	return &ReleaseEvaluation{
		BenchmarkReport: &ReleaseBenchmarkReportV2{
			SchemaID: ReleaseBenchmarkReportV2SchemaID, SchemaVersion: ReleaseSchemaVersion,
			ReportID: input.EvaluationID, TargetVersion: input.TargetVersion, EvaluatedAt: input.EvaluatedAt,
			Status: status, ProfileRef: input.Profile.ArtifactRef, CorpusRef: input.Corpus.ArtifactRef,
			ExecutionRefs: executionRefs, ThresholdRef: input.Thresholds.ArtifactRef, ChildEvidenceRefs: childRefs,
			Metrics: metrics, Gates: []EvaluatedGate{}, Reasons: uniqueReleaseStrings(reasons), ReleaseReady: false,
		},
		CapabilityMatrix: &ReleaseCapabilityMatrixV2{
			SchemaID: ReleaseCapabilityMatrixV2SchemaID, SchemaVersion: ReleaseSchemaVersion,
			MatrixID: input.EvaluationID, TargetVersion: input.TargetVersion, ProfileID: input.Profile.ProfileID,
			CorpusID: input.Corpus.CorpusID, CorpusVersion: input.Corpus.CorpusVersion,
			Capabilities: capabilities, ReleaseReady: false,
		},
	}
}

func evaluateReleaseMetrics(input ReleaseEvaluationInput) ([]EvaluatedMetric, []string) {
	seriesByMetric := map[string]MetricSeries{}
	for _, report := range input.Reports {
		for _, series := range report.MetricSeries {
			key := series.CapabilityID + "\x00" + series.Metric
			if existing, ok := seriesByMetric[key]; ok {
				existing.Samples = append(existing.Samples, series.Samples...)
				seriesByMetric[key] = existing
			} else {
				seriesByMetric[key] = series
			}
		}
	}
	missingMetrics := []string{}
	for _, capability := range input.Profile.Capabilities {
		for metric, category := range requiredReleaseMetricCategories {
			series, ok := seriesByMetric[capability.CapabilityID+"\x00"+metric]
			if !ok || len(series.Samples) == 0 {
				missingMetrics = append(missingMetrics, "capability "+capability.CapabilityID+" required separate metric is absent or empty: "+metric)
				continue
			}
			if series.Category != category {
				missingMetrics = append(missingMetrics, "capability "+capability.CapabilityID+" metric "+metric+" has category "+series.Category+", expected "+category)
			}
		}
	}
	if len(missingMetrics) > 0 {
		sort.Strings(missingMetrics)
		return nil, missingMetrics
	}
	for _, capability := range input.Profile.Capabilities {
		activity := seriesByMetric[capability.CapabilityID+"\x00activity_latency_ms"]
		currentOrGap := seriesByMetric[capability.CapabilityID+"\x00current_or_gap_latency_ms"]
		if !sameReleaseTracePopulation(activity.Samples, currentOrGap.Samples) {
			return nil, []string{"capability " + capability.CapabilityID + " activity and current-or-gap distributions do not use the same end-to-end traces"}
		}
	}
	names := make([]string, 0, len(seriesByMetric))
	for name := range seriesByMetric {
		names = append(names, name)
	}
	sort.Strings(names)
	metrics := make([]EvaluatedMetric, 0, len(names))
	for _, name := range names {
		series := seriesByMetric[name]
		if series.Metric == "" || series.CapabilityID == "" || series.Category == "" || series.Unit == "" || len(series.Samples) == 0 {
			return nil, []string{"metric series is missing identity, category, unit or samples"}
		}
		values := make([]float64, 0, len(series.Samples))
		scenarioIDs, traceIDs, toolchainIDs, evidenceRefs := []string{}, []string{}, []string{}, []string{}
		for _, sample := range series.Samples {
			if !validReleaseMetricValue(series.Category, sample.Value) {
				return nil, []string{"metric series contains an impossible sample: " + series.CapabilityID + "/" + series.Metric}
			}
			values = append(values, sample.Value)
			scenarioIDs = append(scenarioIDs, sample.ScenarioID)
			traceIDs = append(traceIDs, sample.TraceID)
			toolchainIDs = append(toolchainIDs, sample.ToolchainID)
			evidenceRefs = append(evidenceRefs, sample.EvidenceRef)
		}
		aggregation, value := "mean", releaseMean(values)
		if series.Metric == "activity_latency_ms" || series.Metric == "current_or_gap_latency_ms" {
			aggregation, value = "p95", releaseP95(values)
		}
		metrics = append(metrics, EvaluatedMetric{
			Metric: series.Metric, CapabilityID: series.CapabilityID, Category: series.Category, Unit: series.Unit, Aggregation: aggregation, Value: value,
			ProfileID: input.Profile.ProfileID, ScenarioIDs: uniqueReleaseStrings(scenarioIDs), TraceIDs: uniqueReleaseStrings(traceIDs),
			ToolchainIDs: uniqueReleaseStrings(toolchainIDs), EvidenceRefs: uniqueReleaseStrings(evidenceRefs),
		})
	}
	return metrics, nil
}

func validReleaseMetricValue(category string, value float64) bool {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return false
	}
	switch category {
	case "quality":
		return value >= 0 && value <= 1
	case "latency", "resource":
		return value >= 0
	default:
		return false
	}
}

func sameReleaseTracePopulation(left, right []MetricObservation) bool {
	if len(left) != len(right) {
		return false
	}
	counts := map[string]int{}
	key := func(value MetricObservation) string {
		return strings.Join([]string{value.ProfileID, value.ScenarioID, value.TraceID, value.TraceRef, value.ToolchainID}, "\x00")
	}
	for _, value := range left {
		counts[key(value)]++
	}
	for _, value := range right {
		counts[key(value)]--
	}
	for _, count := range counts {
		if count != 0 {
			return false
		}
	}
	return true
}

func sameReleaseStringSet(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	counts := map[string]int{}
	for _, value := range left {
		counts[value]++
	}
	for _, value := range right {
		counts[value]--
	}
	for _, count := range counts {
		if count != 0 {
			return false
		}
	}
	return true
}

func releaseP95(values []float64) float64 {
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	return sorted[int(math.Ceil(0.95*float64(len(sorted))))-1]
}

func releaseMean(values []float64) float64 {
	var sum float64
	for _, value := range values {
		sum += value
	}
	return sum / float64(len(values))
}

func validateReleaseEvidenceIdentities(input ReleaseEvaluationInput) []string {
	reasons := []string{}
	if strings.TrimSpace(input.TargetVersion) == "" || strings.TrimSpace(input.EvaluationID) == "" || strings.TrimSpace(input.EvaluatedAt) == "" {
		reasons = append(reasons, "release target, evaluation identity and timestamp are required")
	}
	profile := input.Profile
	corpus := input.Corpus
	if err := validateReleaseInputContract(ReleaseProfileV2SchemaID, profile); err != nil {
		reasons = append(reasons, "release profile contract is invalid: "+err.Error())
	}
	if profile.SchemaID != ReleaseProfileV2SchemaID || profile.SchemaVersion != ReleaseSchemaVersion || profile.TargetVersion != input.TargetVersion || profile.ProfileID == "" || !isImmutableReleaseRef(profile.ArtifactRef) {
		reasons = append(reasons, "release profile identity is invalid or mutable")
	}
	if err := releaseartifact.Verify(profile, profile.ArtifactRef); err != nil {
		reasons = append(reasons, "release profile content/ref mismatch")
	}
	toolchains := map[string]bool{}
	for _, toolchain := range profile.Toolchains {
		if toolchain.ToolchainID == "" || toolchain.Name == "" || toolchain.Version == "" || !isImmutableReleaseRef(toolchain.ArtifactRef) || toolchains[toolchain.ToolchainID] {
			reasons = append(reasons, "release toolchain identity is invalid, mutable or duplicate")
			continue
		}
		toolchains[toolchain.ToolchainID] = true
	}
	capabilities := map[string]bool{}
	for _, capability := range profile.Capabilities {
		if capability.CapabilityID == "" || capabilities[capability.CapabilityID] {
			reasons = append(reasons, "release capability identity is missing or duplicate")
			continue
		}
		capabilities[capability.CapabilityID] = true
	}
	if corpus.SchemaID != ScenarioManifestV2SchemaID || corpus.SchemaVersion != ReleaseSchemaVersion || corpus.CorpusID == "" || corpus.CorpusVersion == "" || corpus.ProfileID != profile.ProfileID || corpus.ProfileRef != profile.ArtifactRef || !isImmutableReleaseRef(corpus.ArtifactRef) {
		reasons = append(reasons, "scenario corpus identity does not bind to the release profile")
	}
	if err := releaseartifact.Verify(corpus, corpus.ArtifactRef); err != nil {
		reasons = append(reasons, "scenario corpus content/ref mismatch")
	}
	if err := validateReleaseInputContract(ScenarioManifestV2SchemaID, corpus); err != nil {
		reasons = append(reasons, "scenario corpus contract is invalid: "+err.Error())
	}
	scenarios := map[string]ReleaseScenario{}
	for _, scenario := range corpus.Scenarios {
		if scenario.ScenarioID == "" || scenario.Kind == "" || !isImmutableReleaseRef(scenario.FixtureRef) {
			reasons = append(reasons, "scenario identity is missing or mutable")
			continue
		}
		if _, exists := scenarios[scenario.ScenarioID]; exists {
			reasons = append(reasons, "scenario identity is duplicated: "+scenario.ScenarioID)
		}
		for _, capabilityID := range scenario.CapabilityIDs {
			if !capabilities[capabilityID] {
				reasons = append(reasons, "scenario names a capability outside the declared profile scope: "+capabilityID)
			}
		}
		scenarios[scenario.ScenarioID] = scenario
	}
	globalTraces := map[string]ScenarioExecution{}
	metricMetadata := map[string][2]string{}
	metricBindings := map[string]bool{}
	for reportIndex, report := range input.Reports {
		prefix := fmt.Sprintf("execution report %d", reportIndex)
		if err := validateReleaseInputContract(ExecutionReportV2SchemaID, report); err != nil {
			reasons = append(reasons, prefix+" contract is invalid: "+err.Error())
		}
		if report.SchemaID != ExecutionReportV2SchemaID || report.SchemaVersion != ReleaseSchemaVersion || report.ReportID == "" || report.Command == "" || report.ExecutedAt == "" || !isImmutableReleaseRef(report.ArtifactRef) {
			reasons = append(reasons, prefix+" identity is invalid or mutable")
		}
		if err := releaseartifact.Verify(report, report.ArtifactRef); err != nil {
			reasons = append(reasons, prefix+" content/ref mismatch")
		}
		if report.ProfileID != profile.ProfileID || report.ProfileRef != profile.ArtifactRef || report.CorpusID != corpus.CorpusID || report.CorpusVersion != corpus.CorpusVersion || report.CorpusRef != corpus.ArtifactRef {
			reasons = append(reasons, prefix+" is not bound to the declared profile and corpus")
		}
		reportToolchains := map[string]bool{}
		for _, toolchainID := range report.ToolchainIDs {
			if !toolchains[toolchainID] {
				reasons = append(reasons, prefix+" names an undeclared toolchain: "+toolchainID)
			}
			reportToolchains[toolchainID] = true
		}
		traces := map[string]ScenarioExecution{}
		for _, execution := range report.ScenarioResults {
			scenario, scenarioExists := scenarios[execution.ScenarioID]
			if !scenarioExists || execution.TraceID == "" || !isImmutableReleaseRef(execution.TraceRef) || !toolchains[execution.ToolchainID] {
				reasons = append(reasons, prefix+" contains an unbound scenario trace")
				continue
			}
			if !reportToolchains[execution.ToolchainID] {
				reasons = append(reasons, prefix+" contains a scenario result outside the report toolchain scope: "+execution.ToolchainID)
			}
			if execution.Passed && (execution.FailureReason != "" || execution.Recovery != "") {
				reasons = append(reasons, prefix+" passing scenario result contains failure data: "+execution.ScenarioID)
			}
			for _, capabilityID := range execution.CapabilityIDs {
				if !capabilities[capabilityID] || !releaseStringContains(scenario.CapabilityIDs, capabilityID) {
					reasons = append(reasons, prefix+" contains a scenario result outside the declared capability scope: "+capabilityID)
				}
			}
			if previous, duplicate := traces[execution.TraceID]; duplicate && (previous.ScenarioID != execution.ScenarioID || previous.TraceRef != execution.TraceRef || previous.ToolchainID != execution.ToolchainID) {
				reasons = append(reasons, prefix+" reuses a trace identity across different executions")
			}
			if previous, duplicate := globalTraces[execution.TraceID]; duplicate && (previous.ScenarioID != execution.ScenarioID || previous.TraceRef != execution.TraceRef || previous.ToolchainID != execution.ToolchainID || !sameReleaseStringSet(previous.CapabilityIDs, execution.CapabilityIDs)) {
				reasons = append(reasons, "input reuses a trace identity across different executions: "+execution.TraceID)
			}
			traces[execution.TraceID] = execution
			globalTraces[execution.TraceID] = execution
		}
		for _, series := range report.MetricSeries {
			metricKey := series.CapabilityID + "\x00" + series.Metric
			metadata := [2]string{series.Category, series.Unit}
			if previous, exists := metricMetadata[metricKey]; exists && previous != metadata {
				reasons = append(reasons, "metric "+series.CapabilityID+"/"+series.Metric+" changes category or unit across reports")
			} else {
				metricMetadata[metricKey] = metadata
			}
			if !capabilities[series.CapabilityID] {
				reasons = append(reasons, prefix+" metric "+series.Metric+" names an undeclared capability: "+series.CapabilityID)
			}
			for _, sample := range series.Samples {
				bindingKey := strings.Join([]string{metricKey, sample.ProfileID, sample.ScenarioID, sample.TraceID, sample.TraceRef, sample.ToolchainID}, "\x00")
				if metricBindings[bindingKey] {
					reasons = append(reasons, prefix+" metric "+series.Metric+" repeats a capability/trace sample binding")
				}
				metricBindings[bindingKey] = true
				execution, traceExists := traces[sample.TraceID]
				scenario := scenarios[sample.ScenarioID]
				if !traceExists || !releaseStringContains(execution.CapabilityIDs, series.CapabilityID) || !releaseStringContains(scenario.CapabilityIDs, series.CapabilityID) {
					reasons = append(reasons, prefix+" metric "+series.Metric+" is not bound to its declared capability: "+series.CapabilityID)
				}
			}
		}
		for _, check := range report.InvariantChecks {
			execution, traceExists := traces[check.TraceID]
			validResult := check.Result == "pass" || (check.Result == "fail" && check.FailureReason != "" && check.RecoveryCondition != "")
			if !hardReleaseFailureKinds[check.Kind] || check.CapabilityID == "" || !validResult || !isImmutableReleaseRef(check.EvidenceRef) {
				reasons = append(reasons, prefix+" contains an invalid executed hard-invariant check")
				continue
			}
			if check.Result == "pass" && (check.FailureReason != "" || check.RecoveryCondition != "") {
				reasons = append(reasons, prefix+" passing hard-invariant check contains failure data")
			}
			if !traceExists || execution.ScenarioID != check.ScenarioID || !releaseStringContains(execution.CapabilityIDs, check.CapabilityID) {
				reasons = append(reasons, prefix+" hard-invariant check is not bound to its scenario trace and capability")
			}
		}
		for _, series := range report.MetricSeries {
			for _, sample := range series.Samples {
				execution, traceExists := traces[sample.TraceID]
				if sample.ProfileID != profile.ProfileID || sample.ScenarioID == "" || sample.TraceID == "" || sample.ToolchainID == "" || !isImmutableReleaseRef(sample.TraceRef) || !isImmutableReleaseRef(sample.EvidenceRef) {
					reasons = append(reasons, prefix+" metric "+series.Metric+" has a missing or mutable binding")
					continue
				}
				if !reportToolchains[sample.ToolchainID] {
					reasons = append(reasons, prefix+" metric "+series.Metric+" sample is outside the report toolchain scope: "+sample.ToolchainID)
				}
				if _, scenarioExists := scenarios[sample.ScenarioID]; !scenarioExists || !toolchains[sample.ToolchainID] || !traceExists || execution.ScenarioID != sample.ScenarioID || execution.TraceRef != sample.TraceRef || execution.ToolchainID != sample.ToolchainID {
					reasons = append(reasons, prefix+" metric "+series.Metric+" does not bind to its declared scenario trace and toolchain")
				}
			}
		}
	}
	if input.Thresholds.ProfileID != profile.ProfileID || input.Thresholds.ProfileRef != profile.ArtifactRef || input.Thresholds.CorpusID != corpus.CorpusID || input.Thresholds.CorpusVersion != corpus.CorpusVersion || input.Thresholds.CorpusRef != corpus.ArtifactRef || !isImmutableReleaseRef(input.Thresholds.ArtifactRef) {
		reasons = append(reasons, "approved thresholds are not bound to the declared profile and corpus")
	}
	if err := releaseartifact.Verify(input.Thresholds, input.Thresholds.ArtifactRef); err != nil {
		reasons = append(reasons, "approved threshold content/ref mismatch")
	}
	for _, evidence := range input.ChildEvidence {
		if evidence.EvidenceID == "" || evidence.SliceID == "" || evidence.ContractRef == "" || evidence.ImplementationRef == "" || evidence.ExecutionID == "" || evidence.TargetVersion != input.TargetVersion || evidence.SourceRef != profile.Repository.FixtureRef || !isImmutableReleaseRef(evidence.ArtifactRef) {
			reasons = append(reasons, "child evidence identity is missing or mutable")
		}
		if err := releaseartifact.Verify(evidence, evidence.ArtifactRef); err != nil {
			reasons = append(reasons, "child evidence content/ref mismatch: "+evidence.EvidenceID)
		}
		for _, acceptance := range evidence.Acceptance {
			if !isImmutableReleaseRef(acceptance.EvidenceRef) {
				reasons = append(reasons, "child acceptance evidence reference is mutable")
			}
		}
	}
	return uniqueReleaseStrings(reasons)
}

func validateReleaseInputContract(schemaID string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return contractharness.ValidateVS10Contract(schemaID, data)
}

var hardReleaseFailureKinds = map[string]bool{
	"proof_less_current":    true,
	"false_settlement":      true,
	"cross_generation_mix":  true,
	"fabricated_evidence":   true,
	"fabricated_runtime":    true,
	"unsafe_approval":       true,
	"secret_leak":           true,
	"path_leak":             true,
	"race":                  true,
	"unrecovered_event_gap": true,
}

func releaseStringContains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func isImmutableReleaseRef(ref string) bool {
	const prefix = "sha256:"
	if !strings.HasPrefix(ref, prefix) || len(ref) != len(prefix)+64 {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(ref, prefix))
	return err == nil
}

func uniqueReleaseStrings(values []string) []string {
	result := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	return result
}
