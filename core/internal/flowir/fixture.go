package flowir

import (
	"fmt"
	"os"
	"path/filepath"
)

// Fixture is a controlled provider used only for the G02 walking skeleton.
func Fixture(repo string, basis Basis) (Document, error) {
	path := filepath.Join(repo, "lib", "signup.dart")
	bytes, err := os.ReadFile(path)
	if err != nil {
		return Document{}, fmt.Errorf("read controlled fixture evidence %s: %w", path, err)
	}
	start, end := 0, len(bytes)
	anchor := Anchor{Kind: "code", Path: "lib/signup.dart", Symbol: "route", LineRange: []int{1, lineCount(bytes)}, ByteRange: []int{start, end}, FileHash: SHA256Bytes(bytes), SpanHash: SHA256Bytes(bytes[start:end]), Fingerprint: "fixture-signup-v1", Revision: "fixture-current"}
	entry := Fact{ID: Hash(SchemaVersion, "entry_point", "route:/signup", "", "fixture-entry"), Kind: "entry_point", Subject: "route:/signup", Evidence: []Anchor{anchor}, Status: Observed}
	action := Fact{ID: Hash(SchemaVersion, "call", "signup-button", "submit", "fixture-action"), Kind: "call", Subject: "signup-button", Object: "submit", Evidence: []Anchor{anchor}, Status: Observed}
	result := Fact{ID: Hash(SchemaVersion, "visible_result", "route:/signup", "route:/welcome", "fixture-result"), Kind: "visible_result", Subject: "route:/signup", Object: "route:/welcome", Evidence: []Anchor{anchor}, Status: Observed}
	step1 := Step{ID: Hash("route:/signup", entry.ID, action.ID), BehaviorKey: "route:/signup:user:submit:signup", Order: 1, Actor: "user", TriggerFact: entry.ID, BehaviorFacts: []string{action.ID}, PrimaryEvidence: []Anchor{anchor}, Status: Observed}
	step2 := Step{ID: Hash("route:/signup", action.ID, result.ID), BehaviorKey: "route:/signup:system:route_transition:route:/welcome", Order: 2, Actor: "system", TriggerFact: action.ID, ResultFacts: []string{result.ID}, PrimaryEvidence: []Anchor{anchor}, Status: Observed}
	return Document{SchemaVersion: SchemaVersion, Basis: basis, Facts: []Fact{result, entry, action}, Current: Flow{ID: "route:/signup", FlowKey: "route:/signup", EntryPointFact: entry.ID, Steps: []Step{step1, step2}, Status: Observed}}, nil
}

func lineCount(bytes []byte) int {
	n := 1
	for _, b := range bytes {
		if b == '\n' {
			n++
		}
	}
	return n
}
