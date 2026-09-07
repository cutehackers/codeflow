// Package releaseartifact verifies the content-addressed envelope used by
// VS-10 release evidence. Leaf refs remain opaque CAS identities; top-level
// evidence documents must match their own artifactRef.
package releaseartifact

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

func Ref(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return RefJSON(data)
}

func RefJSON(data []byte) (string, error) {
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		return "", fmt.Errorf("parse release artifact: %w", err)
	}
	delete(document, "artifactRef")
	canonical, err := json.Marshal(document)
	if err != nil {
		return "", fmt.Errorf("canonicalize release artifact: %w", err)
	}
	digest := sha256.Sum256(canonical)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func Verify(value any, claimed string) error {
	actual, err := Ref(value)
	if err != nil {
		return err
	}
	if actual != claimed {
		return fmt.Errorf("release artifact content/ref mismatch: claimed %s, actual %s", claimed, actual)
	}
	return nil
}

func VerifyJSON(data []byte, claimed string) error {
	actual, err := RefJSON(data)
	if err != nil {
		return err
	}
	if actual != claimed {
		return fmt.Errorf("release artifact content/ref mismatch: claimed %s, actual %s", claimed, actual)
	}
	return nil
}
