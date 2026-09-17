package contractharness

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"reflect"
	"sort"
	"strings"

	"codeflow/internal/collector/evidence"
	"codeflow/schemas"
)

const VS10EvidenceRegistryID = "rflsc-r2-vs-10"

type VS10ContractRegistryEntry struct {
	ID                        string
	SchemaID                  string
	Producer                  string
	Consumer                  string
	ValidFixture              string
	InvalidEmptySampleFixture string
	InvalidEvidenceFixture    string
	InvalidProfileFixture     string
}

var VS10ContractRegistry = []VS10ContractRegistryEntry{
	{ID: "rflsc.release-profile.v2", SchemaID: BaseURL + "rflsc.release-profile.v2.schema.json", Producer: "release maintainer profile declaration", Consumer: "release capability evaluator", ValidFixture: "rflsc.release-profile.v2/valid/profile.json", InvalidEmptySampleFixture: "rflsc.release-profile.v2/invalid/empty-capabilities.json", InvalidEvidenceFixture: "rflsc.release-profile.v2/invalid/mutable-reference.json", InvalidProfileFixture: "rflsc.release-profile.v2/invalid/missing-hardware.json"},
	{ID: "rflsc.scenario-manifest.v2", SchemaID: BaseURL + "rflsc.scenario-manifest.v2.schema.json", Producer: "versioned release corpus", Consumer: "release corpus runner", ValidFixture: "rflsc.scenario-manifest.v2/valid/manifest.json", InvalidEmptySampleFixture: "rflsc.scenario-manifest.v2/invalid/empty-scenarios.json", InvalidEvidenceFixture: "rflsc.scenario-manifest.v2/invalid/mutable-fixture-reference.json", InvalidProfileFixture: "rflsc.scenario-manifest.v2/invalid/missing-profile.json"},
	{ID: "rflsc.execution-report.v2", SchemaID: BaseURL + "rflsc.execution-report.v2.schema.json", Producer: "declared corpus runner", Consumer: "release capability evaluator", ValidFixture: "rflsc.execution-report.v2/valid/report.json", InvalidEmptySampleFixture: "rflsc.execution-report.v2/invalid/empty-samples.json", InvalidEvidenceFixture: "rflsc.execution-report.v2/invalid/mutable-evidence-reference.json", InvalidProfileFixture: "rflsc.execution-report.v2/invalid/missing-profile.json"},
	{ID: "rflsc.release-benchmark-report.v2", SchemaID: BaseURL + "rflsc.release-benchmark-report.v2.schema.json", Producer: "release capability evaluator", Consumer: "MCP and FlowView release views", ValidFixture: "rflsc.release-benchmark-report.v2/valid/report.json", InvalidEmptySampleFixture: "rflsc.release-benchmark-report.v2/invalid/empty-metric-bindings.json", InvalidEvidenceFixture: "rflsc.release-benchmark-report.v2/invalid/mutable-gate-reference.json", InvalidProfileFixture: "rflsc.release-benchmark-report.v2/invalid/missing-profile.json"},
	{ID: "rflsc.release-capability-matrix.v2", SchemaID: BaseURL + "rflsc.release-capability-matrix.v2.schema.json", Producer: "release capability evaluator", Consumer: "MCP and FlowView support state", ValidFixture: "rflsc.release-capability-matrix.v2/valid/matrix.json", InvalidEmptySampleFixture: "rflsc.release-capability-matrix.v2/invalid/empty-capabilities.json", InvalidEvidenceFixture: "rflsc.release-capability-matrix.v2/invalid/ga-without-evidence.json", InvalidProfileFixture: "rflsc.release-capability-matrix.v2/invalid/missing-profile.json"},
}

func ValidateVS10ContractRegistry() error {
	seen := map[string]bool{}
	for _, entry := range VS10ContractRegistry {
		if entry.ID == "" || entry.SchemaID != BaseURL+entry.ID+".schema.json" || entry.Producer == "" || entry.Consumer == "" || seen[entry.ID] {
			return fmt.Errorf("invalid or duplicate VS-10 registry entry %q", entry.ID)
		}
		seen[entry.ID] = true
		valid, err := readVS10Fixture(entry.ValidFixture)
		if err != nil {
			return err
		}
		if err := ValidateVS10Contract(entry.SchemaID, valid); err != nil {
			return fmt.Errorf("valid fixture %s: %w", entry.ValidFixture, err)
		}
		for _, invalidFixture := range []string{entry.InvalidEmptySampleFixture, entry.InvalidEvidenceFixture, entry.InvalidProfileFixture} {
			invalid, err := readVS10Fixture(invalidFixture)
			if err != nil {
				return err
			}
			if err := ValidateVS10Contract(entry.SchemaID, invalid); err == nil {
				return fmt.Errorf("invalid fixture unexpectedly validated: %s", invalidFixture)
			}
		}
	}
	return nil
}

func readVS10Fixture(name string) ([]byte, error) {
	data, err := fs.ReadFile(schemas.FixturesFS, path.Join("fixtures", name))
	if err != nil {
		return nil, fmt.Errorf("read VS-10 fixture %s: %w", name, err)
	}
	return data, nil
}

func ValidateVS10Contract(schemaID string, data []byte) error {
	if err := Validate(schemaID, data); err != nil {
		return err
	}
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		return err
	}
	if claimed, ok := document["artifactRef"].(string); ok {
		if err := evidence.VerifyJSON(data, claimed); err != nil {
			return err
		}
	}
	switch schemaID {
	case BaseURL + "rflsc.release-profile.v2.schema.json":
		return validateVS10Profile(document)
	case BaseURL + "rflsc.scenario-manifest.v2.schema.json":
		return validateVS10ScenarioManifest(document)
	case BaseURL + "rflsc.execution-report.v2.schema.json":
		return validateVS10ExecutionReport(document)
	case BaseURL + "rflsc.release-benchmark-report.v2.schema.json":
		return validateVS10BenchmarkReport(document)
	case BaseURL + "rflsc.release-capability-matrix.v2.schema.json":
		return validateVS10CapabilityMatrix(document)
	default:
		return fmt.Errorf("unknown VS-10 schema %q", schemaID)
	}
}

func validateVS10Profile(document map[string]any) error {
	if err := uniqueVS10ObjectField(document["toolchains"], "toolchainId"); err != nil {
		return err
	}
	return uniqueVS10ObjectField(document["capabilities"], "capabilityId")
}

var vs10ScenarioKinds = []string{"rapid_edit", "multi_file", "rename_delete", "syntax_error", "branch_switch", "watcher_gap", "open_closure", "late_result", "cas_conflict", "adapter_crash", "model_crash", "reconnect"}

func validateVS10ScenarioManifest(document map[string]any) error {
	if err := uniqueVS10ObjectField(document["scenarios"], "scenarioId"); err != nil {
		return err
	}
	kinds := map[string]bool{}
	for _, item := range document["scenarios"].([]any) {
		kinds[item.(map[string]any)["kind"].(string)] = true
	}
	for _, kind := range vs10ScenarioKinds {
		if !kinds[kind] {
			return fmt.Errorf("required release scenario kind is missing: %s", kind)
		}
	}
	return nil
}

func validateVS10ExecutionReport(document map[string]any) error {
	if err := uniqueVS10ObjectField(document["scenarioResults"], "traceId"); err != nil {
		return err
	}
	if err := uniqueVS10CompositeField(document["metricSeries"], "capabilityId", "metric"); err != nil {
		return err
	}
	reportToolchains := map[string]bool{}
	for _, toolchainID := range document["toolchainIds"].([]any) {
		reportToolchains[toolchainID.(string)] = true
	}
	for _, item := range document["scenarioResults"].([]any) {
		result := item.(map[string]any)
		if !reportToolchains[result["toolchainId"].(string)] {
			return fmt.Errorf("scenario result uses a toolchain outside the report declaration")
		}
	}
	if err := validateVS10MetricSeries(document["metricSeries"].([]any), document["profileId"].(string), document["scenarioResults"].([]any), reportToolchains); err != nil {
		return err
	}
	return validateVS10InvariantChecks(document["invariantChecks"].([]any), document["scenarioResults"].([]any))
}

func validateVS10InvariantChecks(checks, scenarioResults []any) error {
	requiredKinds := map[string]bool{"proof_less_current": true, "false_settlement": true, "cross_generation_mix": true, "fabricated_evidence": true, "fabricated_runtime": true, "unsafe_approval": true, "secret_leak": true, "path_leak": true, "race": true, "unrecovered_event_gap": true}
	traces := map[string]map[string]any{}
	capabilities := map[string]bool{}
	for _, item := range scenarioResults {
		result := item.(map[string]any)
		traces[result["traceId"].(string)] = result
		for _, capability := range result["capabilityIds"].([]any) {
			capabilities[capability.(string)] = true
		}
	}
	counts := map[string]int{}
	for _, item := range checks {
		check := item.(map[string]any)
		kind, capabilityID, traceID := check["kind"].(string), check["capabilityId"].(string), check["traceId"].(string)
		trace, ok := traces[traceID]
		if !ok || trace["scenarioId"] != check["scenarioId"] || !vs10AnyString(trace["capabilityIds"].([]any), capabilityID) {
			return fmt.Errorf("invariant check is not bound to its scenario trace and capability: %s/%s", capabilityID, kind)
		}
		counts[capabilityID+"\x00"+kind]++
	}
	for capabilityID := range capabilities {
		for kind := range requiredKinds {
			if counts[capabilityID+"\x00"+kind] != 1 {
				return fmt.Errorf("capability %s lacks exactly one executed %s invariant check", capabilityID, kind)
			}
		}
	}
	return nil
}

func vs10AnyString(values []any, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func validateVS10MetricSeries(series []any, profileID string, scenarioResults []any, reportToolchains map[string]bool) error {
	required := map[string]string{"activity_latency_ms": "latency", "current_or_gap_latency_ms": "latency", "precision": "quality", "recall": "quality", "semantic_delta": "quality", "alignment_validity": "quality", "unknown_coverage": "quality", "comprehension": "quality", "peak_memory_bytes": "resource"}
	traces := map[string]map[string]any{}
	capabilities := map[string]bool{}
	for _, item := range scenarioResults {
		result := item.(map[string]any)
		traces[result["traceId"].(string)] = result
		for _, capability := range result["capabilityIds"].([]any) {
			capabilities[capability.(string)] = true
		}
	}
	populations := map[string][]string{}
	seen := map[string]bool{}
	seenBindings := map[string]bool{}
	for _, item := range series {
		metric := item.(map[string]any)
		name := metric["metric"].(string)
		capabilityID := metric["capabilityId"].(string)
		category := metric["category"].(string)
		if required[name] != category {
			return fmt.Errorf("metric %s category %s is invalid", name, category)
		}
		seen[capabilityID+"\x00"+name] = true
		for _, rawSample := range metric["samples"].([]any) {
			sample := rawSample.(map[string]any)
			binding := strings.Join([]string{sample["scenarioId"].(string), sample["traceRef"].(string), sample["toolchainId"].(string)}, "\x00")
			bindingKey := strings.Join([]string{capabilityID, name, sample["profileId"].(string), sample["scenarioId"].(string), sample["traceId"].(string), sample["traceRef"].(string), sample["toolchainId"].(string)}, "\x00")
			if seenBindings[bindingKey] {
				return fmt.Errorf("metric %s repeats a capability/trace sample binding", name)
			}
			seenBindings[bindingKey] = true
			if !validVS10MetricNumber(category, sample["value"].(float64)) {
				return fmt.Errorf("metric %s contains an impossible sample", name)
			}
			trace, ok := traces[sample["traceId"].(string)]
			traceBinding := ""
			if ok {
				traceBinding = strings.Join([]string{trace["scenarioId"].(string), trace["traceRef"].(string), trace["toolchainId"].(string)}, "\x00")
			}
			if sample["profileId"].(string) != profileID || !reportToolchains[sample["toolchainId"].(string)] || traceBinding != binding || !ok || !vs10AnyString(trace["capabilityIds"].([]any), capabilityID) {
				return fmt.Errorf("metric %s sample is not bound to its profile and scenario trace", name)
			}
			populations[capabilityID+"\x00"+name] = append(populations[capabilityID+"\x00"+name], sample["traceId"].(string)+"\x00"+binding)
		}
	}
	for capabilityID := range capabilities {
		for name := range required {
			if !seen[capabilityID+"\x00"+name] {
				return fmt.Errorf("capability %s required separate metric is missing: %s", capabilityID, name)
			}
		}
		activityKey := capabilityID + "\x00activity_latency_ms"
		currentKey := capabilityID + "\x00current_or_gap_latency_ms"
		sort.Strings(populations[activityKey])
		sort.Strings(populations[currentKey])
		if strings.Join(populations[activityKey], "\n") != strings.Join(populations[currentKey], "\n") {
			return fmt.Errorf("capability %s latency metrics do not share one end-to-end trace population", capabilityID)
		}
	}
	return nil
}

func validateVS10BenchmarkReport(document map[string]any) error {
	status := document["status"].(string)
	ready := document["releaseReady"].(bool)
	if ready != (status == "passed") {
		return fmt.Errorf("releaseReady does not match passed status")
	}
	if status == "incomplete" {
		if len(document["reasons"].([]any)) == 0 {
			return fmt.Errorf("incomplete report has no reason")
		}
		return nil
	}
	if err := uniqueVS10CompositeField(document["metrics"], "capabilityId", "metric"); err != nil {
		return err
	}
	metrics := map[string]map[string]any{}
	for _, item := range document["metrics"].([]any) {
		metric := item.(map[string]any)
		metrics[metric["capabilityId"].(string)+"\x00"+metric["metric"].(string)] = metric
	}
	required := map[string]string{"activity_latency_ms": "latency", "current_or_gap_latency_ms": "latency", "precision": "quality", "recall": "quality", "semantic_delta": "quality", "alignment_validity": "quality", "unknown_coverage": "quality", "comprehension": "quality", "peak_memory_bytes": "resource"}
	capabilityIDs := map[string]bool{}
	for key, metric := range metrics {
		capabilityID := metric["capabilityId"].(string)
		capabilityIDs[capabilityID] = true
		name := metric["metric"].(string)
		if required[name] != metric["category"] {
			return fmt.Errorf("required separate metric is missing or misclassified: %s", key)
		}
		if !validVS10MetricNumber(metric["category"].(string), metric["value"].(float64)) {
			return fmt.Errorf("evaluated metric contains an impossible value: %s", key)
		}
	}
	for capabilityID := range capabilityIDs {
		for name, category := range required {
			metric, ok := metrics[capabilityID+"\x00"+name]
			if !ok || metric["category"] != category {
				return fmt.Errorf("capability %s required separate metric is missing or misclassified: %s", capabilityID, name)
			}
		}
		activity := metrics[capabilityID+"\x00activity_latency_ms"]
		current := metrics[capabilityID+"\x00current_or_gap_latency_ms"]
		if !reflect.DeepEqual(activity["traceIds"], current["traceIds"]) || !reflect.DeepEqual(activity["scenarioIds"], current["scenarioIds"]) {
			return fmt.Errorf("capability %s evaluated latency metrics do not share one end-to-end trace population", capabilityID)
		}
	}
	seenGates := map[string]bool{}
	gatedMetrics := map[string]bool{}
	for _, item := range document["gates"].([]any) {
		gate := item.(map[string]any)
		key := gate["capabilityId"].(string) + "\x00" + gate["metric"].(string)
		if seenGates[key] {
			return fmt.Errorf("duplicate gate %s", key)
		}
		seenGates[key] = true
		metric, ok := metrics[key]
		if !ok || gate["profileId"] != metric["profileId"] || gate["capabilityId"] != metric["capabilityId"] || !reflect.DeepEqual(gate["scenarioIds"], metric["scenarioIds"]) || !reflect.DeepEqual(gate["traceIds"], metric["traceIds"]) || !reflect.DeepEqual(gate["toolchainIds"], metric["toolchainIds"]) || !reflect.DeepEqual(gate["evidenceRefs"], metric["evidenceRefs"]) {
			return fmt.Errorf("gate %s is not bound to its evaluated metric", key)
		}
		gatedMetrics[key] = true
		if ready && !gate["passed"].(bool) {
			return fmt.Errorf("release-ready report contains a failed gate")
		}
	}
	if ready {
		for capabilityID := range capabilityIDs {
			for name := range required {
				if !gatedMetrics[capabilityID+"\x00"+name] {
					return fmt.Errorf("release-ready report has no gate for %s/%s", capabilityID, name)
				}
			}
		}
	}
	return nil
}

func validVS10MetricNumber(category string, value float64) bool {
	switch category {
	case "quality":
		return value >= 0 && value <= 1
	case "latency", "resource":
		return value >= 0
	default:
		return false
	}
}

func validateVS10CapabilityMatrix(document map[string]any) error {
	if err := uniqueVS10ObjectField(document["capabilities"], "capabilityId"); err != nil {
		return err
	}
	ready := document["releaseReady"].(bool)
	for _, item := range document["capabilities"].([]any) {
		capability := item.(map[string]any)
		state := capability["state"].(string)
		if ready && capability["state"].(string) != "ga" {
			return fmt.Errorf("release-ready matrix contains non-GA capability")
		}
		if state == "ga" {
			if capability["profileId"] == nil || capability["corpusId"] == nil || capability["corpusVersion"] == nil || len(capability["toolchainIds"].([]any)) == 0 || len(capability["scenarioIds"].([]any)) == 0 || len(capability["evidenceRefs"].([]any)) == 0 || len(capability["failureReasons"].([]any)) != 0 || len(capability["recoveryConditions"].([]any)) != 0 {
				return fmt.Errorf("GA capability lacks scope/evidence or contains unresolved failures")
			}
		} else if len(capability["failureReasons"].([]any)) == 0 || len(capability["recoveryConditions"].([]any)) == 0 {
			return fmt.Errorf("non-GA capability lacks failure reason or recovery condition")
		}
	}
	return nil
}

func uniqueVS10ObjectField(value any, field string) error {
	items, ok := value.([]any)
	if !ok {
		return fmt.Errorf("%s collection is missing", field)
	}
	seen := map[string]bool{}
	for _, item := range items {
		object, ok := item.(map[string]any)
		if !ok {
			return fmt.Errorf("%s collection contains a non-object", field)
		}
		identity, _ := object[field].(string)
		if identity == "" || seen[identity] {
			return fmt.Errorf("%s identity is empty or duplicate: %q", field, identity)
		}
		seen[identity] = true
	}
	return nil
}

func uniqueVS10CompositeField(value any, fields ...string) error {
	items, ok := value.([]any)
	if !ok {
		return fmt.Errorf("composite identity collection is missing")
	}
	seen := map[string]bool{}
	for _, item := range items {
		object, ok := item.(map[string]any)
		if !ok {
			return fmt.Errorf("composite identity collection contains a non-object")
		}
		parts := make([]string, 0, len(fields))
		for _, field := range fields {
			part, _ := object[field].(string)
			if part == "" {
				return fmt.Errorf("%s composite identity contains an empty field", strings.Join(fields, "+"))
			}
			parts = append(parts, part)
		}
		identity := strings.Join(parts, "\x00")
		if seen[identity] {
			return fmt.Errorf("%s composite identity is duplicate: %q", strings.Join(fields, "+"), identity)
		}
		seen[identity] = true
	}
	return nil
}
