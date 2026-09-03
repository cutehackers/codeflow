package contractharness

import (
	"encoding/json"
	"fmt"
)

// AdapterAnalysisSchemaID is the registered schema identity for every
// adapter analysis result. Producers and consumers must use this exact id.
const AdapterAnalysisSchemaID = BaseURL + "adapter-analysis.schema.json"

const (
	analysisReadSetSchemaID = BaseURL + "analysis-read-set.schema.json"
	closureSchemaID         = BaseURL + "causal-observation-closure.schema.json"
)

// AdapterAnalysisSemanticError identifies a cross-field mismatch that JSON
// Schema alone cannot express, such as a closure from a different snapshot.
type AdapterAnalysisSemanticError struct {
	Field string
	Want  string
	Got   string
}

func (e *AdapterAnalysisSemanticError) Error() string {
	return fmt.Sprintf("adapter-analysis semantic mismatch at %s: want %s, got %s", e.Field, e.Want, e.Got)
}

// ValidateAdapterAnalysis validates both the registered schema and the
// fields that bind an analysis result to its operation and snapshot basis.
// An empty expectedBasis skips the basis comparison, and a negative expected
// epoch skips the epoch comparison for callers that do not own a snapshot.
func ValidateAdapterAnalysis(data []byte, operation, expectedBasis string, expectedEpoch int64) error {
	if err := Validate(AdapterAnalysisSchemaID, data); err != nil {
		return fmt.Errorf("adapter-analysis schema: %w", err)
	}

	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("adapter-analysis parse: %w", err)
	}
	if got := stringField(doc, "schemaId"); got != AdapterAnalysisSchemaID {
		return &AdapterAnalysisSemanticError{Field: "schemaId", Want: AdapterAnalysisSchemaID, Got: got}
	}
	if got := numberField(doc, "schemaVersion"); got != 1 {
		return &AdapterAnalysisSemanticError{Field: "schemaVersion", Want: "1", Got: fmt.Sprint(got)}
	}
	if operation != "" {
		if got := stringField(doc, "operation"); got != operation {
			return &AdapterAnalysisSemanticError{Field: "operation", Want: operation, Got: got}
		}
	}
	if expectedBasis != "" {
		if got := stringField(doc, "computedBasisId"); got != expectedBasis {
			return &AdapterAnalysisSemanticError{Field: "computedBasisId", Want: expectedBasis, Got: got}
		}
	}
	if expectedEpoch >= 0 {
		if got := numberField(doc, "workspaceEpoch"); got != expectedEpoch {
			return &AdapterAnalysisSemanticError{Field: "workspaceEpoch", Want: fmt.Sprint(expectedEpoch), Got: fmt.Sprint(got)}
		}
	}

	readSet, ok := doc["analysisReadSet"].(map[string]any)
	if !ok {
		return &AdapterAnalysisSemanticError{Field: "analysisReadSet", Want: "object", Got: "missing or non-object"}
	}
	closure, ok := doc["causalObservationClosure"].(map[string]any)
	if !ok {
		return &AdapterAnalysisSemanticError{Field: "causalObservationClosure", Want: "object", Got: "missing or non-object"}
	}
	if got := stringField(readSet, "schemaId"); got != analysisReadSetSchemaID {
		return &AdapterAnalysisSemanticError{Field: "analysisReadSet.schemaId", Want: analysisReadSetSchemaID, Got: got}
	}
	if got := numberField(readSet, "schemaVersion"); got != 1 {
		return &AdapterAnalysisSemanticError{Field: "analysisReadSet.schemaVersion", Want: "1", Got: fmt.Sprint(got)}
	}
	if got := stringField(closure, "schemaId"); got != closureSchemaID {
		return &AdapterAnalysisSemanticError{Field: "causalObservationClosure.schemaId", Want: closureSchemaID, Got: got}
	}
	if got := numberField(closure, "schemaVersion"); got != 1 {
		return &AdapterAnalysisSemanticError{Field: "causalObservationClosure.schemaVersion", Want: "1", Got: fmt.Sprint(got)}
	}
	for _, field := range []string{"computedBasisId", "workspaceEpoch"} {
		if got, want := stringField(readSet, field), stringField(doc, "computedBasisId"); field == "computedBasisId" && got != want {
			return &AdapterAnalysisSemanticError{Field: "analysisReadSet." + field, Want: want, Got: got}
		}
		if field == "workspaceEpoch" && numberField(readSet, field) != numberField(doc, "workspaceEpoch") {
			return &AdapterAnalysisSemanticError{Field: "analysisReadSet." + field, Want: fmt.Sprint(numberField(doc, "workspaceEpoch")), Got: fmt.Sprint(numberField(readSet, field))}
		}
		if got, want := stringField(closure, field), stringField(doc, "computedBasisId"); field == "computedBasisId" && got != want {
			return &AdapterAnalysisSemanticError{Field: "causalObservationClosure." + field, Want: want, Got: got}
		}
		if field == "workspaceEpoch" && numberField(closure, field) != numberField(doc, "workspaceEpoch") {
			return &AdapterAnalysisSemanticError{Field: "causalObservationClosure." + field, Want: fmt.Sprint(numberField(doc, "workspaceEpoch")), Got: fmt.Sprint(numberField(closure, field))}
		}
	}
	readSetID := stringField(readSet, "readSetId")
	if readSetID == "" {
		return &AdapterAnalysisSemanticError{Field: "analysisReadSet.readSetId", Want: "non-empty", Got: ""}
	}
	if got := stringField(closure, "analysisReadSetId"); got != readSetID {
		return &AdapterAnalysisSemanticError{Field: "causalObservationClosure.analysisReadSetId", Want: readSetID, Got: got}
	}
	if status := stringField(closure, "closureStatus"); status != "closed" && status != "open" {
		return &AdapterAnalysisSemanticError{Field: "causalObservationClosure.closureStatus", Want: "closed or open", Got: status}
	}
	if stringField(doc, "analyzerVersion") == "" {
		return &AdapterAnalysisSemanticError{Field: "analyzerVersion", Want: "non-empty", Got: ""}
	}
	return nil
}

func stringField(doc map[string]any, name string) string {
	v, _ := doc[name].(string)
	return v
}

func numberField(doc map[string]any, name string) int64 {
	v, ok := doc[name].(float64)
	if !ok {
		return -1
	}
	return int64(v)
}
