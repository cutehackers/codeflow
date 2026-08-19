// Package delta compares deterministic behavior flows. It intentionally does
// not inspect a textual file diff.
package delta

import (
	"sort"

	"codeflow/core/internal/flowir"
)

type Pair struct {
	Before string `json:"before"`
	After  string `json:"after"`
}
type Delta struct {
	BaselineRevision   string                 `json:"baseline_revision"`
	CurrentRevision    string                 `json:"current_revision"`
	AddedSteps         []string               `json:"added_steps"`
	RemovedSteps       []string               `json:"removed_steps"`
	ChangedResults     []Pair                 `json:"changed_results"`
	ChangedStates      []Pair                 `json:"changed_states"`
	ChangedBranches    []Pair                 `json:"changed_branches"`
	ChangedCausalEdges []Pair                 `json:"changed_causal_edges"`
	NewUnknowns        []flowir.UnknownDetail `json:"new_unknowns"`
}

func Compare(before, after flowir.Document) Delta {
	d := Delta{BaselineRevision: before.Basis.HeadRevision, CurrentRevision: after.Basis.HeadRevision}
	pairs, removed, added := match(before.Current.Steps, after.Current.Steps)
	for _, step := range removed {
		d.RemovedSteps = append(d.RemovedSteps, step.ID)
	}
	for _, step := range added {
		d.AddedSteps = append(d.AddedSteps, step.ID)
	}
	for _, p := range pairs {
		if !same(p.before.ResultFacts, p.after.ResultFacts) {
			d.ChangedResults = append(d.ChangedResults, Pair{p.before.ID, p.after.ID})
		}
		if !sameBranches(p.before.Branches, p.after.Branches) {
			d.ChangedBranches = append(d.ChangedBranches, Pair{p.before.ID, p.after.ID})
		}
		if !same(stateSignature(before, p.before), stateSignature(after, p.after)) {
			d.ChangedStates = append(d.ChangedStates, Pair{p.before.ID, p.after.ID})
		}
	}
	known := map[string]bool{}
	for _, u := range before.Unknowns {
		known[u.ID] = true
	}
	for _, u := range after.Unknowns {
		if !known[u.ID] {
			d.NewUnknowns = append(d.NewUnknowns, u)
		}
	}
	beforeEdges, afterEdges := edgeKeys(before.CausalEdges), edgeKeys(after.CausalEdges)
	for key := range beforeEdges {
		if !afterEdges[key] {
			d.ChangedCausalEdges = append(d.ChangedCausalEdges, Pair{Before: key, After: ""})
		}
	}
	for key := range afterEdges {
		if !beforeEdges[key] {
			d.ChangedCausalEdges = append(d.ChangedCausalEdges, Pair{Before: "", After: key})
		}
	}
	sort.Slice(d.ChangedCausalEdges, func(i, j int) bool {
		return d.ChangedCausalEdges[i].Before+d.ChangedCausalEdges[i].After < d.ChangedCausalEdges[j].Before+d.ChangedCausalEdges[j].After
	})
	sort.Strings(d.AddedSteps)
	sort.Strings(d.RemovedSteps)
	return d
}

func stateSignature(document flowir.Document, step flowir.Step) []string {
	facts := map[string]flowir.Fact{}
	for _, fact := range document.Facts {
		facts[fact.ID] = fact
	}
	var signature []string
	for _, id := range append(append([]string{}, step.BehaviorFacts...), step.ResultFacts...) {
		fact := facts[id]
		switch fact.Kind {
		case "state_transition", "notifier_state_transition", "provider_dependency", "listener_condition", "unknown_state":
			signature = append(signature, fact.Kind+"\x00"+fact.Subject+"\x00"+fact.Object)
		}
	}
	sort.Strings(signature)
	return signature
}

func edgeKeys(edges []flowir.CausalEdge) map[string]bool {
	out := make(map[string]bool, len(edges))
	for _, edge := range edges {
		out[edge.FromFact+"\x00"+edge.Kind+"\x00"+edge.ToFact] = true
	}
	return out
}

type matched struct{ before, after flowir.Step }

func match(before, after []flowir.Step) ([]matched, []flowir.Step, []flowir.Step) {
	bb, aa := buckets(before), buckets(after)
	keys := map[string]bool{}
	for k := range bb {
		keys[k] = true
	}
	for k := range aa {
		keys[k] = true
	}
	var out []matched
	var removed, added []flowir.Step
	for key := range keys {
		left, right := bb[key], aa[key]
		if len(left) == 1 && len(right) == 1 {
			out = append(out, matched{left[0], right[0]})
			continue
		}
		usedL, usedR := map[int]bool{}, map[int]bool{}
		// Duplicate behavior keys may match only when a symbol is uniquely shared.
		uniquePair(left, right, func(s flowir.Step) string { return anchor(s).Symbol }, usedL, usedR, &out)
		// The remaining ties may match only when structural fingerprints are unique.
		uniquePair(left, right, func(s flowir.Step) string { return anchor(s).Fingerprint }, usedL, usedR, &out)
		for i, step := range left {
			if !usedL[i] {
				removed = append(removed, step)
			}
		}
		for i, step := range right {
			if !usedR[i] {
				added = append(added, step)
			}
		}
	}
	return out, removed, added
}
func buckets(steps []flowir.Step) map[string][]flowir.Step {
	m := map[string][]flowir.Step{}
	for _, s := range steps {
		m[s.BehaviorKey] = append(m[s.BehaviorKey], s)
	}
	return m
}
func anchor(step flowir.Step) flowir.Anchor {
	if len(step.PrimaryEvidence) > 0 {
		return step.PrimaryEvidence[0]
	}
	return flowir.Anchor{}
}
func uniquePair(left, right []flowir.Step, key func(flowir.Step) string, usedL, usedR map[int]bool, out *[]matched) {
	for i, l := range left {
		if usedL[i] || key(l) == "" {
			continue
		}
		candidates := []int{}
		for j, r := range right {
			if !usedR[j] && key(r) == key(l) {
				candidates = append(candidates, j)
			}
		}
		reverse := 0
		for k, b := range left {
			if !usedL[k] && key(b) == key(l) {
				reverse++
			}
		}
		if len(candidates) == 1 && reverse == 1 {
			j := candidates[0]
			usedL[i] = true
			usedR[j] = true
			*out = append(*out, matched{l, right[j]})
		}
	}
}
func same(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
func sameBranches(a, b []flowir.Branch) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].ID != b[i].ID || !same(a[i].OutcomeStepIDs, b[i].OutcomeStepIDs) {
			return false
		}
	}
	return true
}
