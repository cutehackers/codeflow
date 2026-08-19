package flowir

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixtureDocument(t *testing.T) Document {
	t.Helper()
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "lib"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "lib", "signup.dart"), []byte("void signup() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	doc, err := Fixture(repo, Basis{Repository: repo, HeadRevision: "test", WorktreeFingerprint: Hash(repo), Manifest: []ManifestEntry{}})
	if err != nil {
		t.Fatal(err)
	}
	return doc
}
func TestCanonicalJSONIgnoresFactOrderAndPublicationData(t *testing.T) {
	doc := fixtureDocument(t)
	first, err := CanonicalJSON(doc)
	if err != nil {
		t.Fatal(err)
	}
	doc.Facts[0], doc.Facts[2] = doc.Facts[2], doc.Facts[0]
	second, err := CanonicalJSON(doc)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatal("shuffled fact set must serialize deterministically")
	}
	if strings.Contains(string(first), "generated_at") || strings.Contains(string(first), "runtime_status") {
		t.Fatal("publication metadata must not be in deterministic FlowIR")
	}
}

func TestCanonicalJSONSortsSetSemanticsWithoutMutatingDocument(t *testing.T) {
	doc := fixtureDocument(t)
	doc.Architecture.Components = []string{"ui", "state", "ui"}
	doc.Unknowns = []UnknownDetail{{
		ID:                Hash("canonical-debt"),
		Question:          "What is missing?",
		Reason:            "missing_relation",
		RelatedSteps:      []string{doc.Current.Steps[1].ID, doc.Current.Steps[0].ID},
		Evidence:          doc.Current.Steps[0].PrimaryEvidence,
		SuggestedEvidence: []string{"test", "contract", "test"},
	}}
	originalFirst := doc.Architecture.Components[0]
	first, err := CanonicalJSON(doc)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Architecture.Components[0] != originalFirst || len(doc.Architecture.Components) != 3 {
		t.Fatal("canonicalization mutated the caller's document")
	}
	doc.Architecture.Components = []string{"ui", "state"}
	doc.Unknowns[0].RelatedSteps[0], doc.Unknowns[0].RelatedSteps[1] = doc.Unknowns[0].RelatedSteps[1], doc.Unknowns[0].RelatedSteps[0]
	doc.Unknowns[0].SuggestedEvidence = []string{"contract", "test"}
	second, err := CanonicalJSON(doc)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatalf("set-semantic fields changed canonical bytes\n%s\n%s", first, second)
	}
	if originalFirst != "ui" {
		t.Fatal("test setup changed unexpectedly")
	}
}

func TestCausalEdgeIDTreatsConditionsAsASet(t *testing.T) {
	left := CausalEdgeID("a", "b", "guards", []string{"second", "first"})
	right := CausalEdgeID("a", "b", "guards", []string{"first", "second"})
	if left != right {
		t.Fatalf("condition ordering changed causal identity: %s != %s", left, right)
	}
}
func TestValidateRejectsInvalidEvidenceCombinations(t *testing.T) {
	base := fixtureDocument(t)
	cases := []struct {
		name   string
		mutate func(*Document)
	}{
		{"session observed", func(d *Document) {
			d.Facts[0].Evidence = []Anchor{{Kind: "session", Path: "session.json", FileHash: "x"}}
		}},
		{"stale current", func(d *Document) { d.Facts[0].Status = Stale }},
		{"resolved proof without symbol", func(d *Document) { d.Facts[0].Proof = "resolved_ast" }},
		{"unsupported proof layer", func(d *Document) { d.Facts[0].Proof = "regex_guess" }},
		{"visible without route", func(d *Document) {
			for i := range d.Facts {
				if d.Facts[i].Kind == "visible_result" {
					d.Facts[i].Evidence = []Anchor{{Kind: "code", Path: "x", FileHash: "x", Symbol: "helper"}}
				}
			}
		}},
		{"more than three anchors", func(d *Document) {
			a := d.Current.Steps[0].PrimaryEvidence[0]
			d.Current.Steps[0].PrimaryEvidence = []Anchor{a, a, a, a}
		}},
		{"anchorless branch", func(d *Document) {
			condition := Fact{ID: "condition", Kind: "condition", Subject: "x", Status: Observed, Evidence: d.Facts[0].Evidence}
			d.Facts = append(d.Facts, condition)
			d.Current.Steps[0].Branches = []Branch{{ID: "b", ConditionFact: "condition", Status: Observed}}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := base
			d.Facts = append([]Fact(nil), base.Facts...)
			d.Current.Steps = append([]Step(nil), base.Current.Steps...)
			tc.mutate(&d)
			if err := Validate(d); err == nil {
				t.Fatal("expected invalid FlowIR")
			}
		})
	}
}

func TestValidateRejectsGuessedBranchIdentityAndOutcome(t *testing.T) {
	doc := fixtureDocument(t)
	condition := Fact{ID: "condition", Kind: "condition", Subject: "SignupPage.submit", Status: Observed, Evidence: doc.Facts[0].Evidence}
	doc.Facts = append(doc.Facts, condition)
	branch := Branch{ConditionFact: condition.ID, OutcomeStepIDs: []string{doc.Current.Steps[1].ID}, Evidence: condition.Evidence, Status: Observed}
	branch.ID = BranchID(branch.ConditionFact, []string{doc.Current.Steps[1].BehaviorKey})
	doc.Current.Steps[0].Branches = []Branch{branch}
	if err := Validate(doc); err != nil {
		t.Fatalf("valid deterministic branch rejected: %v", err)
	}
	doc.Current.Steps[0].Branches[0].ID = "guessed-branch"
	if err := Validate(doc); err == nil {
		t.Fatal("guessed branch identity must be rejected")
	}
	doc.Current.Steps[0].Branches[0].ID = branch.ID
	doc.Current.Steps[0].Branches[0].OutcomeStepIDs = []string{"made-up-step"}
	if err := Validate(doc); err == nil {
		t.Fatal("branch outcome must name a real ordered step")
	}
}

func TestCausalEdgesAndCognitiveDebtAreEvidenceBound(t *testing.T) {
	doc := fixtureDocument(t)
	from := doc.Current.EntryPointFact
	to := doc.Current.Steps[0].BehaviorFacts[0]
	edge := CausalEdge{FromFact: from, ToFact: to, Kind: "causes", Evidence: doc.Current.Steps[0].PrimaryEvidence, Status: Observed}
	edge.ID = CausalEdgeID(edge.FromFact, edge.ToFact, edge.Kind, nil)
	doc.CausalEdges = []CausalEdge{edge}
	doc.Unknowns = []UnknownDetail{{ID: Hash("unknown", "debt"), Question: "What completes this transition?", Reason: "missing_relation", RelatedSteps: []string{doc.Current.Steps[0].ID}, RelatedEdges: []string{edge.ID}, Evidence: edge.Evidence, DebtState: "open", ResolutionCriteria: []string{"A current test proves the target."}}}
	if err := Validate(doc); err != nil {
		t.Fatalf("valid causal debt rejected: %v", err)
	}
	doc.CausalEdges[0].ID = "guessed"
	if err := Validate(doc); err == nil {
		t.Fatal("non-deterministic causal identity must be rejected")
	}
}
