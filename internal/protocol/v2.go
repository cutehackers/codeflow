package protocol

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeflow/internal/contractharness"
	"codeflow/internal/rflscvs02"
)

// analyzerRequestForCall is the one Core conversion point from the public Go
// operation seam to the v2 analyzer contract. It captures no source data. The
// only bytes accepted here are the snapshot bytes already present in params.
func analyzerRequestForCall(requestID, operation string, params any, maxMessageBytes int64) (rflscvs02.AnalyzerRequest, error) {
	m, err := objectParams(params)
	if err != nil {
		return rflscvs02.AnalyzerRequest{}, err
	}
	if _, ok := m["snapshot"]; !ok {
		return rflscvs02.AnalyzerRequest{}, BadRequestError("analysis snapshot is required")
	}
	snapshot := objectValue(m["snapshot"])
	if len(snapshot) == 0 {
		return rflscvs02.AnalyzerRequest{}, BadRequestError("analysis snapshot must be an object")
	}
	for _, field := range []string{"snapshotId", "workspaceEpoch", "computedBasisId", "rootTreeId", "dependencyFingerprint", "documents", "files", "repositoryPathWriteAudit"} {
		if _, ok := snapshot[field]; !ok {
			return rflscvs02.AnalyzerRequest{}, BadRequestError("analysis snapshot is missing " + field)
		}
	}
	files, err := snapshotFiles(m, snapshot)
	if err != nil {
		return rflscvs02.AnalyzerRequest{}, err
	}

	basis := stringValue(snapshot["computedBasisId"])
	if basis == "" {
		return rflscvs02.AnalyzerRequest{}, BadRequestError("analysis snapshot computedBasisId is empty")
	}
	snapshotID := stringValue(snapshot["snapshotId"])
	if snapshotID == "" {
		return rflscvs02.AnalyzerRequest{}, BadRequestError("analysis snapshot snapshotId is empty")
	}
	rootTreeID := stringValue(snapshot["rootTreeId"])
	if rootTreeID == "" {
		return rflscvs02.AnalyzerRequest{}, BadRequestError("analysis snapshot rootTreeId is empty")
	}
	configuration := stringValue(snapshot["configurationFingerprint"])
	dependency := stringValue(snapshot["dependencyFingerprint"])
	epochValue, epochPresent := snapshot["workspaceEpoch"]
	if !epochPresent {
		return rflscvs02.AnalyzerRequest{}, BadRequestError("analysis snapshot workspaceEpoch is missing")
	}
	epoch := int64Value(epochValue)
	if epoch < 0 {
		return rflscvs02.AnalyzerRequest{}, BadRequestError("analysis snapshot workspaceEpoch is invalid")
	}
	if dependency == "" {
		return rflscvs02.AnalyzerRequest{}, BadRequestError("analysis snapshot dependencyFingerprint is empty")
	}
	input, err := rflscvs02.SnapshotInputFromContent(snapshotID, basis, rootTreeID, configuration, dependency, epoch, files)
	if err != nil {
		return rflscvs02.AnalyzerRequest{}, BadRequestError(err.Error())
	}
	if err := bindSnapshotDocuments(&input, snapshot["documents"]); err != nil {
		return rflscvs02.AnalyzerRequest{}, BadRequestError(err.Error())
	}
	if err := requireSnapshotAudit(&input, snapshot["repositoryPathWriteAudit"]); err != nil {
		return rflscvs02.AnalyzerRequest{}, BadRequestError(err.Error())
	}

	taskScope := stringSlice(m["taskScope"])
	required := stringSlice(m["requiredObservations"])
	request, err := rflscvs02.NewAnalyzerRequest(requestID, operation, input, taskScope, required)
	if err != nil {
		return rflscvs02.AnalyzerRequest{}, BadRequestError(err.Error())
	}
	request.CapabilityRequest = stringSlice(m["capabilityRequest"])
	if maxMessageBytes <= 0 {
		maxMessageBytes = DefaultMaxMessageSizeBytes
	}
	request.MaxMessageBytes = maxMessageBytes
	request.Payload = operationPayload(m)
	encoded, err := json.Marshal(request.Params())
	if err != nil {
		return rflscvs02.AnalyzerRequest{}, BadRequestError(fmt.Sprintf("marshal analyzer request: %v", err))
	}
	if err := contractharness.Validate(rflscvs02.AnalyzerRequestSchemaID, encoded); err != nil {
		return rflscvs02.AnalyzerRequest{}, BadRequestError(fmt.Sprintf("analyzer request v2 schema rejected: %v", err))
	}
	return request, nil
}

func objectParams(params any) (map[string]any, error) {
	if params == nil {
		return map[string]any{}, nil
	}
	data, err := json.Marshal(params)
	if err != nil {
		return nil, BadRequestError(fmt.Sprintf("marshal params: %v", err))
	}
	var object map[string]any
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.UseNumber()
	if err := decoder.Decode(&object); err != nil || object == nil {
		return nil, BadRequestError("analysis params must be a JSON object")
	}
	return object, nil
}

func objectValue(value any) map[string]any {
	if object, ok := value.(map[string]any); ok && object != nil {
		return object
	}
	return map[string]any{}
}

func snapshotFiles(_ map[string]any, snapshot map[string]any) (map[string]string, error) {
	source, exists := snapshot["files"]
	if !exists {
		return nil, BadRequestError("snapshot files are required")
	}
	entries, ok := source.(map[string]any)
	if !ok {
		return nil, BadRequestError("snapshot files must be an object")
	}
	files := make(map[string]string, len(entries))
	for path, raw := range entries {
		if content, ok := raw.(string); ok {
			files[path] = content
			continue
		}
		return nil, BadRequestError(fmt.Sprintf("snapshot file %q must contain string content", path))
	}
	return files, nil
}

func bindSnapshotDocuments(input *rflscvs02.SnapshotInput, raw any) error {
	if raw == nil {
		return fmt.Errorf("snapshot documents are required")
	}
	items, ok := raw.([]any)
	if !ok {
		return fmt.Errorf("snapshot documents must be an array")
	}
	seen := make(map[string]bool, len(items))
	for _, item := range items {
		object, ok := item.(map[string]any)
		if !ok {
			return fmt.Errorf("snapshot document must be an object")
		}
		path := stringValue(object["path"])
		if path == "" || seen[path] {
			return fmt.Errorf("snapshot document path is missing or duplicated: %q", path)
		}
		seen[path] = true
		index := -1
		for i := range input.Documents {
			if input.Documents[i].Path == path {
				index = i
				break
			}
		}
		if index < 0 {
			return fmt.Errorf("snapshot document %s has no captured bytes", path)
		}
		doc := &input.Documents[index]
		contentHash := stringValue(object["contentHash"])
		if contentHash == "" || contentHash != doc.ContentID {
			return fmt.Errorf("snapshot document %s content hash does not match captured bytes", path)
		}
		contentID := stringValue(object["contentId"])
		if contentID == "" || contentID != doc.ContentID {
			return fmt.Errorf("snapshot document %s content identity does not match captured bytes", path)
		}
		if value, exists := object["byteLength"]; !exists || int64Value(value) != int64(doc.ByteLength) {
			return fmt.Errorf("snapshot document %s byte length does not match captured bytes", path)
		}
		if revision := stringValue(object["documentRevisionId"]); revision != "" {
			doc.RevisionID = revision
		} else {
			return fmt.Errorf("snapshot document %s revision identity is required", path)
		}
		if version := int64Value(object["documentVersion"]); version > 0 {
			doc.DocumentVersion = int(version)
		} else {
			return fmt.Errorf("snapshot document %s document version is required", path)
		}
	}
	if len(seen) != len(input.Documents) {
		return fmt.Errorf("snapshot documents and files must be one-to-one")
	}
	return nil
}

func requireSnapshotAudit(input *rflscvs02.SnapshotInput, raw any) error {
	object, ok := raw.(map[string]any)
	if !ok {
		return fmt.Errorf("snapshot repositoryPathWriteAudit is required")
	}
	for _, field := range []string{"codeflowWriteCount", "sourceIntegrityViolation", "capturedSnapshotTreeDigest"} {
		if _, present := object[field]; !present {
			return fmt.Errorf("snapshot repositoryPathWriteAudit is missing %s", field)
		}
	}
	bindSnapshotAudit(input, object)
	if input.SourceWriteAudit.CapturedSnapshotTreeDigest != input.RootTreeID {
		return fmt.Errorf("snapshot write audit tree digest does not match rootTreeId")
	}
	return nil
}

func bindSnapshotAudit(input *rflscvs02.SnapshotInput, raw any) {
	if object, ok := raw.(map[string]any); ok {
		input.SourceWriteAudit.CodeFlowWriteCount = int(int64Value(object["codeflowWriteCount"]))
		input.SourceWriteAudit.SourceIntegrityViolation, _ = object["sourceIntegrityViolation"].(bool)
		input.SourceWriteAudit.CapturedSnapshotTreeDigest = stringValue(object["capturedSnapshotTreeDigest"])
		if writes, ok := object["repositoryPathWrites"].([]any); ok {
			for _, write := range writes {
				if path, ok := write.(string); ok {
					input.SourceWriteAudit.RepositoryPathWrites = append(input.SourceWriteAudit.RepositoryPathWrites, path)
				}
			}
		}
	}
	if input.SourceWriteAudit.CapturedSnapshotTreeDigest == "" {
		input.SourceWriteAudit.CapturedSnapshotTreeDigest = input.RootTreeID
	}
}

func operationPayload(params map[string]any) map[string]any {
	if supplied := objectValue(params["payload"]); len(supplied) > 0 {
		return supplied
	}
	const excluded = "schemaId schemaVersion requestId operation snapshot files contentOverlay repoRoot computedBasisId workspaceEpoch taskScope requiredObservations capabilityRequest maxMessageBytes"
	exclude := make(map[string]bool)
	for _, key := range strings.Fields(excluded) {
		exclude[key] = true
	}
	payload := make(map[string]any)
	for key, value := range params {
		if !exclude[key] {
			payload[key] = value
		}
	}
	return payload
}

func stringValue(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	return ""
}

func int64Value(value any) int64 {
	switch number := value.(type) {
	case json.Number:
		parsed, _ := number.Int64()
		return parsed
	case float64:
		return int64(number)
	case int:
		return int64(number)
	case int64:
		return number
	case int32:
		return int64(number)
	}
	return 0
}

func stringSlice(value any) []string {
	var output []string
	switch values := value.(type) {
	case []any:
		for _, value := range values {
			if text, ok := value.(string); ok && text != "" {
				output = append(output, text)
			}
		}
	case []string:
		for _, text := range values {
			if text != "" {
				output = append(output, text)
			}
		}
	}
	return output
}

func validateAnalysisPayload(operation string, payload json.RawMessage) error {
	if len(payload) == 0 || string(payload) == "null" {
		return fmt.Errorf("operation payload is missing")
	}
	var object map[string]any
	if err := json.Unmarshal(payload, &object); err != nil || object == nil {
		return fmt.Errorf("operation payload must be an object")
	}
	switch operation {
	case OpDetect:
		if _, ok := object["language"].(string); !ok {
			return fmt.Errorf("detect payload.language must be a string")
		}
		if _, ok := object["confident"].(bool); !ok {
			return fmt.Errorf("detect payload.confident must be a boolean")
		}
	case OpHarvestCandidates:
		candidates, ok := object["candidates"]
		if !ok {
			return fmt.Errorf("harvest payload.candidates is required")
		}
		items, ok := candidates.([]any)
		if !ok {
			return fmt.Errorf("harvest payload.candidates must be an array")
		}
		for index, item := range items {
			encoded, err := json.Marshal(item)
			if err != nil {
				return fmt.Errorf("candidate %d: %w", index, err)
			}
			if err := contractharness.Validate(contractharness.BaseURL+"candidate.schema.json", encoded); err != nil {
				return fmt.Errorf("candidate %d violates schema: %w", index, err)
			}
		}
	case OpSlice:
		encoded, err := json.Marshal(object)
		if err != nil {
			return err
		}
		if err := contractharness.Validate(contractharness.BaseURL+"sliced-payload.schema.json", encoded); err != nil {
			return fmt.Errorf("slice payload violates schema: %w", err)
		}
	default:
		return fmt.Errorf("unsupported analysis operation %q", operation)
	}
	return nil
}

func analyzerRequestForResponse(requestID, operation string, params any, maxMessageBytes int64) (rflscvs02.AnalyzerRequest, error) {
	return analyzerRequestForCall(requestID, operation, params, maxMessageBytes)
}
