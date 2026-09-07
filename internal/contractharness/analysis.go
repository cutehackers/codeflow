package contractharness

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
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
	return validateAdapterAnalysis(data, operation, expectedBasis, expectedEpoch, nil)
}

// ValidateAdapterAnalysisAgainstSnapshot applies the legacy v1 metadata
// checks plus the immutable snapshot read-set and observation closure gate.
// The params value is the exact request payload sent to the adapter. A read
// document is accepted only when its hash and byte length match captured
// protocol content.
func ValidateAdapterAnalysisAgainstSnapshot(data []byte, operation, expectedBasis string, expectedEpoch int64, params any) error {
	return validateAdapterAnalysis(data, operation, expectedBasis, expectedEpoch, params)
}

func validateAdapterAnalysis(data []byte, operation, expectedBasis string, expectedEpoch int64, params any) error {
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
	strictSnapshot := hasSnapshotContent(params)
	if params != nil {
		if err := validateCapabilityCoverage(doc, closure, strictSnapshot); err != nil {
			return err
		}
	}
	if err := validateSnapshotIdentity(doc, params); err != nil {
		return err
	}
	if err := validateReadSetContent(readSet, params, strictSnapshot); err != nil {
		return err
	}
	if err := validateObservationClosure(doc, readSet, closure, params, strictSnapshot); err != nil {
		return err
	}
	return nil
}

// validateCapabilityCoverage is intentionally a semantic gate instead of a
// schema-only check. Adapter results are promoted only when the adapter has
// identified itself, declared mutually consistent support, and measured the
// source boundary it actually analyzed. The closure repeats these fields so a
// consumer cannot promote a top-level claim while retaining an unmeasured
// causal closure.
func validateCapabilityCoverage(doc, closure map[string]any, strictSnapshot bool) error {
	profile, ok := doc["capabilityProfile"].(map[string]any)
	if !ok {
		return &AdapterAnalysisSemanticError{Field: "capabilityProfile", Want: "object", Got: "missing or non-object"}
	}
	analyzerRevision := stringField(doc, "analyzerVersion")
	if err := validateCapabilityProfile(profile, "capabilityProfile", analyzerRevision, strictSnapshot); err != nil {
		return err
	}
	closureProfile, ok := closure["capabilityProfile"].(map[string]any)
	if !ok {
		return &AdapterAnalysisSemanticError{Field: "causalObservationClosure.capabilityProfile", Want: "object", Got: "missing or non-object"}
	}
	if err := validateCapabilityProfile(closureProfile, "causalObservationClosure.capabilityProfile", analyzerRevision, strictSnapshot); err != nil {
		return err
	}
	for _, field := range []string{"adapter", "adapterVersion", "analyzerRevision", "features", "unsupported", "protocolVersions"} {
		want := stringField(profile, field)
		if field == "features" || field == "unsupported" || field == "protocolVersions" {
			if strictSnapshot && !jsonValuesEqual(profile[field], closureProfile[field]) {
				return &AdapterAnalysisSemanticError{Field: "causalObservationClosure.capabilityProfile." + field, Want: "same as capabilityProfile", Got: fmt.Sprint(closureProfile[field])}
			}
			continue
		}
		if want == "" {
			if strictSnapshot {
				return &AdapterAnalysisSemanticError{Field: "capabilityProfile." + field, Want: "non-empty", Got: ""}
			}
			continue
		}
		if got := stringField(closureProfile, field); got != want {
			return &AdapterAnalysisSemanticError{Field: "causalObservationClosure.capabilityProfile." + field, Want: want, Got: got}
		}
	}
	if strictSnapshot && !jsonValuesEqual(profile["coverageBoundary"], closure["coverageBoundary"]) {
		return &AdapterAnalysisSemanticError{Field: "causalObservationClosure.coverageBoundary", Want: "same as capabilityProfile.coverageBoundary", Got: fmt.Sprint(closure["coverageBoundary"])}
	}
	return nil
}

func validateCapabilityProfile(profile map[string]any, field, analyzerRevision string, strictSnapshot bool) error {
	if stringField(profile, "adapter") == "" {
		return &AdapterAnalysisSemanticError{Field: field + ".adapter", Want: "non-empty", Got: ""}
	}
	features := stringList(profile["features"])
	if len(features) == 0 {
		return &AdapterAnalysisSemanticError{Field: field + ".features", Want: "at least one measured capability", Got: "empty or missing"}
	}
	unsupported := stringList(profile["unsupported"])
	if strictSnapshot {
		if _, exists := profile["adapterVersion"]; !exists || stringField(profile, "adapterVersion") == "" {
			return &AdapterAnalysisSemanticError{Field: field + ".adapterVersion", Want: "non-empty", Got: fmt.Sprint(profile["adapterVersion"])}
		}
		if _, exists := profile["analyzerRevision"]; !exists || stringField(profile, "analyzerRevision") == "" {
			return &AdapterAnalysisSemanticError{Field: field + ".analyzerRevision", Want: analyzerRevision, Got: fmt.Sprint(profile["analyzerRevision"])}
		}
		if _, exists := profile["unsupported"]; !exists {
			return &AdapterAnalysisSemanticError{Field: field + ".unsupported", Want: "declared array", Got: "missing"}
		}
	}
	for _, feature := range features {
		if containsString(unsupported, feature) {
			return &AdapterAnalysisSemanticError{Field: field + ".unsupported", Want: "disjoint from features", Got: feature}
		}
	}
	if declared := stringField(profile, "analyzerRevision"); declared != "" && declared != analyzerRevision {
		return &AdapterAnalysisSemanticError{Field: field + ".analyzerRevision", Want: analyzerRevision, Got: declared}
	}
	boundary, ok := profile["coverageBoundary"].(map[string]any)
	if !ok {
		return &AdapterAnalysisSemanticError{Field: field + ".coverageBoundary", Want: "object", Got: "missing or non-object"}
	}
	measured, ok := boundary["measured"].(bool)
	if !ok || !measured {
		return &AdapterAnalysisSemanticError{Field: field + ".coverageBoundary.measured", Want: "true", Got: fmt.Sprint(boundary["measured"])}
	}
	roots := stringList(boundary["includedSourceRoots"])
	if len(roots) == 0 {
		return &AdapterAnalysisSemanticError{Field: field + ".coverageBoundary.includedSourceRoots", Want: "at least one measured source root", Got: "empty or missing"}
	}
	return nil
}

func validateSnapshotIdentity(doc map[string]any, params any) error {
	request := map[string]any{}
	raw, _ := json.Marshal(params)
	_ = json.Unmarshal(raw, &request)
	snapshot, _ := request["snapshot"].(map[string]any)
	if snapshot == nil {
		return nil
	}
	for _, field := range []string{"snapshotId", "rootTreeId", "dependencyFingerprint", "configurationFingerprint"} {
		want, ok := snapshot[field].(string)
		if !ok || want == "" {
			continue
		}
		got, exists := doc[field]
		if !exists {
			return &AdapterAnalysisSemanticError{Field: field, Want: want, Got: "missing"}
		}
		gotString, ok := got.(string)
		if !ok || gotString != want {
			return &AdapterAnalysisSemanticError{Field: field, Want: want, Got: fmt.Sprint(got)}
		}
	}
	return nil
}

func hasSnapshotContent(params any) bool {
	request := map[string]any{}
	raw, _ := json.Marshal(params)
	if json.Unmarshal(raw, &request) != nil {
		return false
	}
	hasEntries := func(value any) bool {
		entries, ok := value.(map[string]any)
		return ok && len(entries) > 0
	}
	if hasEntries(request["files"]) || hasEntries(request["contentOverlay"]) {
		return true
	}
	snapshot, _ := request["snapshot"].(map[string]any)
	if snapshot == nil {
		return false
	}
	return hasEntries(snapshot["files"]) || hasEntries(snapshot["contentOverlay"])
}

func snapshotFiles(params any) map[string]string {
	request := map[string]any{}
	raw, _ := json.Marshal(params)
	if json.Unmarshal(raw, &request) != nil {
		return nil
	}
	files := map[string]string{}
	add := func(value any) {
		if entries, ok := value.(map[string]any); ok {
			for path, content := range entries {
				if text, ok := content.(string); ok {
					files[path] = text
				}
			}
		}
	}
	add(request["files"])
	add(request["contentOverlay"])
	if snapshot, ok := request["snapshot"].(map[string]any); ok {
		add(snapshot["files"])
		add(snapshot["contentOverlay"])
	}
	return files
}

func validateReadSetContent(readSet map[string]any, params any, strictSnapshot bool) error {
	files := snapshotFiles(params)
	if len(files) == 0 {
		return nil
	}
	identities := snapshotDocumentIdentities(params)
	docs, ok := readSet["documents"].([]any)
	if !ok {
		return &AdapterAnalysisSemanticError{Field: "analysisReadSet.documents", Want: "array", Got: "missing or non-array"}
	}
	for _, item := range docs {
		doc, ok := item.(map[string]any)
		if !ok {
			return &AdapterAnalysisSemanticError{Field: "analysisReadSet.documents", Want: "objects", Got: fmt.Sprint(item)}
		}
		path, _ := doc["path"].(string)
		if strictSnapshot && !validRelativeAnalysisPath(path) {
			return &AdapterAnalysisSemanticError{Field: "analysisReadSet.documents." + path, Want: "repository-relative path", Got: path}
		}
		content, exists := files[path]
		if !exists {
			return &AdapterAnalysisSemanticError{Field: "analysisReadSet.documents." + path, Want: "captured snapshot file", Got: "missing"}
		}
		hash := sha256.Sum256([]byte(content))
		wantHash := hex.EncodeToString(hash[:])
		if got, _ := doc["contentHash"].(string); got != wantHash {
			return &AdapterAnalysisSemanticError{Field: "analysisReadSet.documents." + path + ".contentHash", Want: wantHash, Got: fmt.Sprint(doc["contentHash"])}
		}
		if identity, expected := identities[path]; expected {
			if got, _ := doc["documentRevisionId"].(string); got != identity.RevisionID {
				return &AdapterAnalysisSemanticError{Field: "analysisReadSet.documents." + path + ".documentRevisionId", Want: identity.RevisionID, Got: fmt.Sprint(doc["documentRevisionId"])}
			}
			if got, _ := doc["contentId"].(string); got != identity.ContentID {
				return &AdapterAnalysisSemanticError{Field: "analysisReadSet.documents." + path + ".contentId", Want: identity.ContentID, Got: fmt.Sprint(doc["contentId"])}
			}
			if got, _ := numberValue(doc["documentVersion"]); got != identity.DocumentVersion {
				return &AdapterAnalysisSemanticError{Field: "analysisReadSet.documents." + path + ".documentVersion", Want: fmt.Sprint(identity.DocumentVersion), Got: fmt.Sprint(doc["documentVersion"])}
			}
		}
		if rawLength, exists := doc["byteLength"]; exists {
			length, ok := rawLength.(float64)
			if !ok || int(length) != len([]byte(content)) {
				return &AdapterAnalysisSemanticError{Field: "analysisReadSet.documents." + path + ".byteLength", Want: fmt.Sprint(len([]byte(content))), Got: fmt.Sprint(rawLength)}
			}
		} else if strictSnapshot {
			return &AdapterAnalysisSemanticError{Field: "analysisReadSet.documents." + path + ".byteLength", Want: fmt.Sprint(len([]byte(content))), Got: "missing"}
		}
	}
	return nil
}

type snapshotDocumentIdentity struct {
	RevisionID      string
	ContentID       string
	DocumentVersion int64
}

func snapshotDocumentIdentities(params any) map[string]snapshotDocumentIdentity {
	request := map[string]any{}
	raw, _ := json.Marshal(params)
	if json.Unmarshal(raw, &request) != nil {
		return nil
	}
	snapshot, _ := request["snapshot"].(map[string]any)
	if snapshot == nil {
		return nil
	}
	documents, _ := snapshot["documents"].([]any)
	result := make(map[string]snapshotDocumentIdentity, len(documents))
	for _, item := range documents {
		doc, _ := item.(map[string]any)
		path, _ := doc["path"].(string)
		if path == "" {
			continue
		}
		version, _ := numberValue(doc["documentVersion"])
		result[path] = snapshotDocumentIdentity{
			RevisionID: stringField(doc, "documentRevisionId"), ContentID: stringField(doc, "contentId"), DocumentVersion: version,
		}
	}
	return result
}

func numberValue(value any) (int64, bool) {
	switch n := value.(type) {
	case float64:
		return int64(n), n == float64(int64(n))
	case int:
		return int64(n), true
	case int64:
		return n, true
	default:
		return 0, false
	}
}

func validateObservationClosure(doc, readSet, closure map[string]any, params any, strictSnapshot bool) error {
	for _, field := range []string{"negativeObservations", "membershipObservations", "dependencyFrontiers"} {
		read, readOK := readSet[field].([]any)
		closeList, closeOK := closure[field].([]any)
		if !readOK || !closeOK || len(read) != len(closeList) {
			return &AdapterAnalysisSemanticError{Field: "causalObservationClosure." + field, Want: "same measured list as analysisReadSet", Got: "mismatch"}
		}
		for index, item := range read {
			observation, ok := item.(map[string]any)
			if !ok {
				return &AdapterAnalysisSemanticError{Field: "analysisReadSet." + field, Want: "observation objects", Got: fmt.Sprint(item)}
			}
			if strictSnapshot {
				measured, exists := observation["measured"]
				if !exists {
					return &AdapterAnalysisSemanticError{Field: "analysisReadSet." + field, Want: "measured boolean", Got: "missing"}
				}
				if _, ok := measured.(bool); !ok {
					return &AdapterAnalysisSemanticError{Field: "analysisReadSet." + field, Want: "measured boolean", Got: fmt.Sprint(measured)}
				}
			}
			if measured, exists := observation["measured"]; exists && measured != true && !strictSnapshot {
				return &AdapterAnalysisSemanticError{Field: "analysisReadSet." + field, Want: "measured observation", Got: fmt.Sprint(observation)}
			}
			closeObservation, _ := closeList[index].(map[string]any)
			if closeObservation == nil {
				return &AdapterAnalysisSemanticError{Field: "causalObservationClosure." + field, Want: "observation object", Got: "missing"}
			}
			if strictSnapshot && !jsonValuesEqual(observation, closeObservation) {
				return &AdapterAnalysisSemanticError{Field: "causalObservationClosure." + field, Want: "same observation list as analysisReadSet", Got: fmt.Sprint(closeObservation)}
			}
			if !strictSnapshot {
				kind, _ := observation["kind"].(string)
				closeKind, _ := closeObservation["kind"].(string)
				if kind != "" && closeKind != kind {
					return &AdapterAnalysisSemanticError{Field: "causalObservationClosure." + field, Want: kind, Got: closeKind}
				}
			}
		}
	}
	required := stringList(closure["requiredObservations"])
	request := map[string]any{}
	raw, _ := json.Marshal(params)
	_ = json.Unmarshal(raw, &request)
	requestRequired := stringList(request["requiredObservations"])
	if strictSnapshot {
		if !sameStringSet(required, requestRequired) {
			return &AdapterAnalysisSemanticError{Field: "causalObservationClosure.requiredObservations", Want: "same required observations as request", Got: fmt.Sprint(required)}
		}
	} else if len(required) == 0 {
		required = requestRequired
	}
	measured := stringList(closure["measuredObservations"])
	for _, field := range []struct {
		name string
		key  string
	}{
		{"negative_lookup", "negativeObservations"},
		{"membership", "membershipObservations"},
		{"dependency_frontier", "dependencyFrontiers"},
	} {
		items, _ := readSet[field.key].([]any)
		for _, item := range items {
			observation, _ := item.(map[string]any)
			if observation != nil && observation["measured"] == true {
				measured = appendUnique(measured, field.name)
			}
		}
	}
	status := stringField(closure, "closureStatus")
	if strictSnapshot {
		expectedMeasured := measuredObservationNames(readSet)
		if !sameStringSet(measured, expectedMeasured) {
			return &AdapterAnalysisSemanticError{Field: "causalObservationClosure.measuredObservations", Want: "measured observation names", Got: fmt.Sprint(measured)}
		}
		if status == "open" && len(stringList(closure["incompleteReasons"])) == 0 {
			return &AdapterAnalysisSemanticError{Field: "causalObservationClosure.incompleteReasons", Want: "reason for open closure", Got: "empty"}
		}
	}
	for _, want := range required {
		if containsString(measured, want) {
			continue
		}
		if status == "closed" {
			return &AdapterAnalysisSemanticError{Field: "causalObservationClosure.closureStatus", Want: "open when required observation is unmeasured", Got: "closed without " + want}
		}
		if !containsReason(stringList(closure["incompleteReasons"]), want) {
			return &AdapterAnalysisSemanticError{Field: "causalObservationClosure.incompleteReasons", Want: "reason for " + want, Got: "missing"}
		}
	}
	if status == "closed" && len(measured) == 0 && len(required) > 0 {
		return &AdapterAnalysisSemanticError{Field: "causalObservationClosure.measuredObservations", Want: "non-empty", Got: "empty"}
	}
	return nil
}

func validRelativeAnalysisPath(path string) bool {
	return path != "" && !strings.HasPrefix(path, "/") && !strings.Contains(path, "\\") && path != "." && path != ".." && !strings.HasPrefix(path, "../") && !strings.Contains(path, "/../")
}

func jsonValuesEqual(left, right any) bool {
	lb, lerr := json.Marshal(left)
	rb, rerr := json.Marshal(right)
	return lerr == nil && rerr == nil && bytes.Equal(lb, rb)
}

func sameStringSet(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	seen := make(map[string]bool, len(left))
	for _, value := range left {
		seen[value] = true
	}
	for _, value := range right {
		if !seen[value] {
			return false
		}
	}
	return true
}

func measuredObservationNames(readSet map[string]any) []string {
	measured := []string{}
	for _, field := range []struct {
		name string
		key  string
	}{
		{"negative_lookup", "negativeObservations"},
		{"membership", "membershipObservations"},
		{"dependency_frontier", "dependencyFrontiers"},
	} {
		items, _ := readSet[field.key].([]any)
		for _, item := range items {
			observation, _ := item.(map[string]any)
			if observation != nil && observation["measured"] == true {
				measured = appendUnique(measured, field.name)
			}
		}
	}
	return measured
}

func stringList(value any) []string {
	items, ok := value.([]any)
	if !ok {
		if typed, ok := value.([]string); ok {
			return append([]string(nil), typed...)
		}
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		if text, ok := item.(string); ok && text != "" {
			out = append(out, text)
		}
	}
	return out
}

func appendUnique(items []string, value string) []string {
	if containsString(items, value) {
		return items
	}
	return append(items, value)
}

func containsString(items []string, value string) bool {
	for _, item := range items {
		if strings.EqualFold(item, value) {
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
