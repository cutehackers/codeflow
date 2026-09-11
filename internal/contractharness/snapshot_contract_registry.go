package contractharness

import (
	"encoding/json"
	"fmt"
)

// VS02ContractRegistryEntry records the public producer/consumer seam and
// valid/invalid fixtures for one R2 analyzer boundary contract.
type VS02ContractRegistryEntry struct {
	ID                    string
	SchemaID              string
	Producer              string
	Consumer              string
	ValidFixture          string
	InvalidFixture        string
	InvalidBasisFixture   string
	InvalidClosureFixture string
	InvalidRangeFixture   string
	InvalidBoundFixture   string
}

const VS02EvidenceRegistryID = "rflsc-r2-vs-02"

var VS02ContractRegistry = []VS02ContractRegistryEntry{
	{ID: "rflsc.analyzer-request.v2", SchemaID: BaseURL + "rflsc.analyzer-request.v2.schema.json", Producer: "core snapshot lease", Consumer: "dart/typescript/go adapter", ValidFixture: "rflsc.analyzer-request.v2/valid/request.json", InvalidFixture: "rflsc.analyzer-request.v2/invalid/missing-snapshot.json"},
	{ID: "rflsc.analyzer-result.v2", SchemaID: BaseURL + "rflsc.analyzer-result.v2.schema.json", Producer: "dart/typescript/go adapter", Consumer: "core semantic validator", ValidFixture: "rflsc.analyzer-result.v2/valid/result.json", InvalidFixture: "rflsc.analyzer-result.v2/invalid/bad-basis.json", InvalidBasisFixture: "rflsc.analyzer-result.v2/invalid/bad-basis.json"},
	{ID: "rflsc.analysis-read-set.v2", SchemaID: BaseURL + "rflsc.analysis-read-set.v2.schema.json", Producer: "adapter measured reads", Consumer: "closure validator", ValidFixture: "rflsc.analysis-read-set.v2/valid/read-set.json", InvalidFixture: "rflsc.analysis-read-set.v2/invalid/missing-measured.json"},
	{ID: "rflsc.observation-closure.v2", SchemaID: BaseURL + "rflsc.observation-closure.v2.schema.json", Producer: "adapter observation collector", Consumer: "core publication gate", ValidFixture: "rflsc.observation-closure.v2/valid/closed.json", InvalidFixture: "rflsc.observation-closure.v2/invalid/closed-unmeasured.json", InvalidClosureFixture: "rflsc.observation-closure.v2/invalid/closed-unmeasured.json"},
	{ID: "rflsc.evidence.v2", SchemaID: BaseURL + "rflsc.evidence.v2.schema.json", Producer: "snapshot evidence extractor", Consumer: "core evidence promotion", ValidFixture: "rflsc.evidence.v2/valid/evidence.json", InvalidFixture: "rflsc.evidence.v2/invalid/path-traversal.json", InvalidRangeFixture: "rflsc.evidence.v2/invalid/path-traversal.json"},
	{ID: "rflsc.adapter-capability-matrix.v1", SchemaID: BaseURL + "rflsc.adapter-capability-matrix.v1.schema.json", Producer: "conformance harness", Consumer: "release capability evaluator", ValidFixture: "rflsc.adapter-capability-matrix.v1/valid/matrix.json", InvalidFixture: "rflsc.adapter-capability-matrix.v1/invalid/forged-measured.json"},
	{ID: "rflsc.adapter-diagnostic.v2", SchemaID: BaseURL + "rflsc.adapter-diagnostic.v2.schema.json", Producer: "protocol client and adapters", Consumer: "diagnostics sink", ValidFixture: "rflsc.adapter-diagnostic.v2/valid/oversize.json", InvalidFixture: "rflsc.adapter-diagnostic.v2/invalid/unbounded.json", InvalidBoundFixture: "rflsc.adapter-diagnostic.v2/invalid/unbounded.json"},
}

func ValidateVS02RegistryEntry(entry VS02ContractRegistryEntry, valid []byte) error {
	if entry.ID == "" || entry.SchemaID == "" || entry.Producer == "" || entry.Consumer == "" || entry.ValidFixture == "" || entry.InvalidFixture == "" {
		return fmt.Errorf("VS-02 contract registry entry is incomplete: %+v", entry)
	}
	if err := Validate(entry.SchemaID, valid); err != nil {
		return fmt.Errorf("%s valid fixture: %w", entry.ID, err)
	}
	return nil
}

// VS02CapabilityMatrixDocument is the registry representation of measured
// language capabilities. It is kept separate from adapter result fields so a
// declaration cannot silently become a measurement.
type VS02CapabilityMatrixDocument struct {
	SchemaID      string `json:"schemaId"`
	SchemaVersion int    `json:"schemaVersion"`
	Measurements  []struct {
		Adapter       string   `json:"adapter"`
		Status        string   `json:"status"`
		MeasurementID string   `json:"measurementId"`
		Features      []string `json:"features"`
	} `json:"measurements"`
}

func ValidateVS02CapabilityMatrix(data []byte) error {
	if err := Validate(BaseURL+"rflsc.adapter-capability-matrix.v1.schema.json", data); err != nil {
		return err
	}
	var doc VS02CapabilityMatrixDocument
	if err := json.Unmarshal(data, &doc); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, measurement := range doc.Measurements {
		if (measurement.Status != "measured" && measurement.Status != "unsupported") || measurement.MeasurementID == "" || seen[measurement.Adapter] {
			return fmt.Errorf("capability matrix has missing/duplicate measurement for %q", measurement.Adapter)
		}
		if measurement.Status == "measured" && len(measurement.Features) == 0 {
			return fmt.Errorf("capability matrix measured entry %q has no features", measurement.Adapter)
		}
		seen[measurement.Adapter] = true
	}
	for _, language := range []string{"dart", "typescript", "go"} {
		if !seen[language] {
			return fmt.Errorf("capability matrix missing %s", language)
		}
	}
	return nil
}
