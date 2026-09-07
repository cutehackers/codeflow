package protocol

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"
)

type fakeModelHostClient struct {
	ModelHostClient
}

func TestModelHostAttestationBindsExactSpawnedInstanceAcrossClose(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") == "1" {
		return
	}
	host, err := SpawnModelHost(context.Background(), ModelHostConfig{
		BinPath: os.Args[0], Args: []string{"-test.run=TestModelHostHelper"},
		Env: []string{"CODEFLOW_MODEL_HOST_HELPER=1"}, DefaultTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("spawn model host: %v", err)
	}
	attestation := AttestModelHost(host)
	if attestation == nil || !VerifyCoreHostAttestation(host, attestation) {
		t.Fatal("a spawned model host did not receive a valid Core attestation")
	}
	displayID := ModelHostCoreDisplayID(host, attestation)
	if displayID == "" {
		t.Fatal("a spawned model host did not receive an opaque Core display identity")
	}
	attestationCopy := *attestation
	if !VerifyCoreHostAttestation(host, &attestationCopy) {
		t.Fatal("copying an attestation lost the exact host binding")
	}
	if AttestModelHost(fakeModelHostClient{ModelHostClient: host}) != nil || ModelHostCoreDisplayID(fakeModelHostClient{ModelHostClient: host}, attestation) != "" {
		t.Fatal("a fake ModelHostClient wrapper reused the exact-host authority")
	}
	other, err := SpawnModelHost(context.Background(), ModelHostConfig{
		BinPath: os.Args[0], Args: []string{"-test.run=TestModelHostHelper"},
		Env: []string{"CODEFLOW_MODEL_HOST_HELPER=1"}, DefaultTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("spawn second model host: %v", err)
	}
	if VerifyCoreHostAttestation(other, attestation) {
		_ = other.Close()
		t.Fatal("an attestation was replayed to a different spawned host")
	}
	otherAttestation := AttestModelHost(other)
	if otherAttestation == nil || ModelHostCoreDisplayID(other, otherAttestation) == "" || ModelHostCoreDisplayID(other, otherAttestation) == displayID {
		_ = other.Close()
		t.Fatal("independent model hosts did not receive distinct opaque display identities")
	}
	if ModelHostCoreDisplayID(other, attestation) != "" || ModelHostCoreDisplayID(host, otherAttestation) != "" {
		_ = other.Close()
		t.Fatal("a cross-host attestation produced a display identity")
	}
	if err := other.Close(); err != nil {
		t.Fatalf("close second spawned model host: %v", err)
	}

	// Copy the security-bearing values without copying ModelHost's mutexes.
	// The copy has the original identity pointer, so it must fail the exact
	// instance check even though it carries the same authority value.
	copiedHost := &ModelHost{authority: host.authority, identity: host.identity}
	if AttestModelHost(copiedHost) != nil || VerifyCoreHostAttestation(copiedHost, attestation) {
		t.Fatal("a copied ModelHost value reused the original host authority")
	}

	if err := host.Close(); err != nil {
		t.Fatalf("close spawned model host: %v", err)
	}
	if !VerifyCoreHostAttestation(host, attestation) {
		t.Fatal("closing the exact spawned host invalidated its in-memory attestation")
	}
	if ModelHostCoreDisplayID(host, attestation) != displayID {
		t.Fatal("closing the exact spawned host changed its Core display identity")
	}

	data, err := json.Marshal(attestation)
	if err != nil {
		t.Fatalf("marshal attestation: %v", err)
	}
	var roundTripped CoreHostAttestation
	if err := json.Unmarshal(data, &roundTripped); err != nil {
		t.Fatalf("unmarshal attestation: %v", err)
	}
	if VerifyCoreHostAttestation(host, &roundTripped) {
		t.Fatal("JSON round-trip manufactured a valid Core attestation")
	}
	if ModelHostCoreDisplayID(host, &roundTripped) != "" {
		t.Fatal("JSON round-trip manufactured a valid Core display identity")
	}

	var nilHost *ModelHost
	if AttestModelHost(nilHost) != nil || VerifyCoreHostAttestation(nilHost, attestation) {
		t.Fatal("nil model host received or verified a Core attestation")
	}
}
