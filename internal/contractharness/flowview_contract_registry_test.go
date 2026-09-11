package contractharness

import (
	"bytes"
	"testing"
)

func TestVS10ContractRegistryValidatesEmbeddedFixtures(t *testing.T) {
	if len(VS10ContractRegistry) != 5 {
		t.Fatalf("VS-10 registry entries=%d want 5", len(VS10ContractRegistry))
	}
	if err := ValidateVS10ContractRegistry(); err != nil {
		t.Fatalf("VS-10 contract registry validation failed: %v", err)
	}
}

func TestVS10ContractRegistryRejectsContentRefMismatch(t *testing.T) {
	for _, entry := range VS10ContractRegistry[:3] {
		t.Run(entry.ID, func(t *testing.T) {
			fixture, err := readVS10Fixture(entry.ValidFixture)
			if err != nil {
				t.Fatal(err)
			}
			tampered := bytes.Replace(fixture, []byte("synthetic"), []byte("tampered"), 1)
			if bytes.Equal(tampered, fixture) {
				t.Fatal("fixture did not contain the tamper target")
			}
			if err := ValidateVS10Contract(entry.SchemaID, tampered); err == nil {
				t.Fatal("content changed without changing artifactRef, but validation passed")
			}
		})
	}
}
