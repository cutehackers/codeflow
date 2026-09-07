package semantic

import "testing"

func TestModelActivationDisclosure_RequiresExplicitChoiceAndShowsCapabilityChange(t *testing.T) {
	disclosure, err := NewModelActivationDisclosure(ModelArtifact{
		ModelID: "local-slm", Revision: "rev-7", License: "Apache-2.0", Checksum: "sha256:abc",
		Runtime: "sandbox-v2", DataBoundary: "local-only", Capabilities: []string{"semantic_proposal", "cancellation"},
	}, ModelCapabilityState{Status: "unsupported", Capabilities: []string{"cancellation"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateModelActivationDisclosure(disclosure); err != nil {
		t.Fatalf("initial disclosure: %v", err)
	}
	if len(disclosure.CapabilityChange) != 1 || disclosure.CapabilityChange[0] != "added:semantic_proposal" {
		t.Fatalf("capability change = %v", disclosure.CapabilityChange)
	}
	if _, err := ResolveModelActivation(disclosure, ""); err == nil {
		t.Fatal("activation without explicit choice was accepted")
	}
	declined, err := ResolveModelActivation(disclosure, "decline")
	if err != nil || declined.Approved || declined.Choice != "decline" {
		t.Fatalf("declined disclosure = %+v, err=%v", declined, err)
	}
	approved, err := ResolveModelActivation(disclosure, "activate")
	if err != nil || !approved.Approved || approved.Choice != "activate" {
		t.Fatalf("approved disclosure = %+v, err=%v", approved, err)
	}
}

func TestModelActivationDisclosure_RejectsIncompleteIdentity(t *testing.T) {
	if _, err := NewModelActivationDisclosure(ModelArtifact{ModelID: "named-only"}, ModelCapabilityState{}); err == nil {
		t.Fatal("incomplete model identity was accepted")
	}
}
