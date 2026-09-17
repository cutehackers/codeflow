package collector_test

import (
	"testing"

	"codeflow/internal/collector"
)

func TestCollector_MaskSecret(t *testing.T) {
	c := collector.NewCollector("/test/repo")
	masked := c.MaskSecret("api_key=sk-1234567890abcdef1234567890abcdef")
	if masked == "api_key=sk-1234567890abcdef1234567890abcdef" {
		t.Fatalf("secret was not redacted: %s", masked)
	}
}
