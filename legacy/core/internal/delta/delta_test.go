package delta

import (
	"codeflow/core/internal/flowir"
	"testing"
)

func step(id, key, symbol, fingerprint, result string) flowir.Step {
	return flowir.Step{ID: id, BehaviorKey: key, ResultFacts: []string{result}, PrimaryEvidence: []flowir.Anchor{{Symbol: symbol, Fingerprint: fingerprint}}}
}
func doc(rev string, steps []flowir.Step) flowir.Document {
	return flowir.Document{Basis: flowir.Basis{HeadRevision: rev}, Current: flowir.Flow{Steps: steps}}
}
func TestDuplicateBehaviorKeyOnlyMatchesUniqueSymbolOrFingerprint(t *testing.T) {
	before := doc("base", []flowir.Step{step("b1", "same", "A", "one", "r"), step("b2", "same", "A", "two", "r")})
	after := doc("now", []flowir.Step{step("a1", "same", "A", "one", "r"), step("a2", "same", "A", "two", "r")})
	d := Compare(before, after)
	if len(d.AddedSteps) != 0 || len(d.RemovedSteps) != 0 {
		t.Fatalf("fingerprint ties should match %#v", d)
	}
	after.Current.Steps[0].PrimaryEvidence[0].Fingerprint = "three"
	after.Current.Steps[1].PrimaryEvidence[0].Fingerprint = "four"
	d = Compare(before, after)
	if len(d.AddedSteps) != 2 || len(d.RemovedSteps) != 2 {
		t.Fatalf("ambiguous duplicate keys must be deletion plus addition %#v", d)
	}
}
func TestDeltaReportsDeletedStepsAndChangedResultsAndNewUnknowns(t *testing.T) {
	before := doc("base", []flowir.Step{step("b", "key", "S", "f", "old"), step("gone", "gone", "G", "g", "x")})
	after := doc("now", []flowir.Step{step("a", "key", "S", "f", "new")})
	after.Unknowns = []flowir.UnknownDetail{{ID: "u", Question: "?", Reason: "missing_relation", RelatedSteps: []string{"a"}, Evidence: []flowir.Anchor{{FileHash: "h"}}}}
	d := Compare(before, after)
	if len(d.RemovedSteps) != 1 || len(d.ChangedResults) != 1 || len(d.NewUnknowns) != 1 {
		t.Fatalf("bad delta %#v", d)
	}
}

func TestDeltaReportsChangedCausalRelations(t *testing.T) {
	before := doc("base", nil)
	after := doc("now", nil)
	before.CausalEdges = []flowir.CausalEdge{{FromFact: "event", ToFact: "loading", Kind: "changes_state"}}
	after.CausalEdges = []flowir.CausalEdge{{FromFact: "event", ToFact: "ready", Kind: "changes_state"}}

	d := Compare(before, after)
	if len(d.ChangedCausalEdges) != 2 {
		t.Fatalf("causal removal and addition must both be visible: %#v", d.ChangedCausalEdges)
	}
}

func TestDeltaReportsStateChangeEvenWhenVisibleResultIsStable(t *testing.T) {
	before := doc("base", []flowir.Step{step("before", "submit", "S", "f", "visible")})
	after := doc("now", []flowir.Step{step("after", "submit", "S", "f", "visible")})
	before.Current.Steps[0].BehaviorFacts = []string{"state-before"}
	after.Current.Steps[0].BehaviorFacts = []string{"state-after"}
	before.Facts = []flowir.Fact{{ID: "state-before", Kind: "state_transition", Subject: "Controller.submit", Object: "state:idle"}}
	after.Facts = []flowir.Fact{{ID: "state-after", Kind: "state_transition", Subject: "Controller.submit", Object: "state:submitted"}}

	d := Compare(before, after)
	if len(d.ChangedResults) != 0 || len(d.ChangedStates) != 1 || d.ChangedStates[0].After != "after" {
		t.Fatalf("state delta must remain visible independently of route result: %#v", d)
	}
}
