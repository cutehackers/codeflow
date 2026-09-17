// Package evidence reference verification for content-addressed release envelopes.
package evidence

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// ArtifactRef computes the content-addressed artifact reference (sha256:<hex>)
// for a release evidence document, canonicalized with artifactRef removed.
func ArtifactRef(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return ArtifactRefJSON(data)
}

// ArtifactRefJSON computes the content-addressed artifact reference from raw JSON bytes.
func ArtifactRefJSON(data []byte) (string, error) {
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

// VerifyArtifactRef checks that the value's content-addressed reference matches claimed.
func VerifyArtifactRef(value any, claimed string) error {
	actual, err := ArtifactRef(value)
	if err != nil {
		return err
	}
	if actual != claimed {
		return fmt.Errorf("release artifact content/ref mismatch: claimed %s, actual %s", claimed, actual)
	}
	return nil
}

// VerifyArtifactRefJSON checks that raw JSON's content-addressed reference matches claimed.
func VerifyArtifactRefJSON(data []byte, claimed string) error {
	actual, err := ArtifactRefJSON(data)
	if err != nil {
		return err
	}
	if actual != claimed {
		return fmt.Errorf("release artifact content/ref mismatch: claimed %s, actual %s", claimed, actual)
	}
	return nil
}

// Aliases for convenience and compatibility.
func Ref(value any) (string, error)                { return ArtifactRef(value) }
func RefJSON(data []byte) (string, error)          { return ArtifactRefJSON(data) }
func Verify(value any, claimed string) error       { return VerifyArtifactRef(value, claimed) }
func VerifyJSON(data []byte, claimed string) error { return VerifyArtifactRefJSON(data, claimed) }
