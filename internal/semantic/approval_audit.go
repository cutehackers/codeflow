package semantic

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"

	"codeflow/internal/secret"
)

// MarshalRedactedApprovalEventAudit returns the only public audit projection
// of a validated approval meaning mutation. It contains the v2 lifecycle event
// only. The mutation itself remains a sealed internal value and cannot be
// serialized through its MarshalJSON method.
func MarshalRedactedApprovalEventAudit(mutation *ValidatedApprovalMutation) ([]byte, error) {
	if mutation == nil {
		return nil, newApprovalMeaningMutationError("audit")
	}
	if err := mutation.Validate(); err != nil {
		return nil, newApprovalMeaningMutationError("audit")
	}
	event := mutation.Event()
	if event == nil {
		return nil, newApprovalMeaningMutationError("audit")
	}
	raw, err := json.Marshal(event)
	if err != nil {
		return nil, newApprovalMeaningMutationError("audit")
	}
	redacted, redactionCount, err := secret.RedactJSON(raw)
	if err != nil || redactionCount != 0 {
		return nil, newApprovalMeaningMutationError("audit")
	}
	decoded, err := decodeApprovalEventAudit(redacted)
	if err != nil {
		return nil, newApprovalMeaningMutationError("audit")
	}
	if err := validateApprovalLifecycleEvent(decoded); err != nil || decoded != *event {
		return nil, newApprovalMeaningMutationError("audit")
	}
	return redacted, nil
}

func decodeApprovalEventAudit(data []byte) (ApprovalEventV2, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var event ApprovalEventV2
	if err := decoder.Decode(&event); err != nil {
		return ApprovalEventV2{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return ApprovalEventV2{}, errors.New("trailing approval event document")
		}
		return ApprovalEventV2{}, err
	}
	return event, nil
}
