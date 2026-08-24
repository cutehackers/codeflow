package harvest

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// buildScoreRepo writes a deterministic fixture tree:
//
//	lib/feat/alpha.dart        defines SignupFlow + UserRepository (boundary word)
//	lib/feat/beta.dart         defines CheckoutUseCase (no boundary evidence)
//	lib/feat/gamma.dart        imports data/user_repository.dart (boundary via import basename)
//	lib/feat/delta.dart        imports ui/theme.dart (no boundary evidence)
//	lib/feat/saturated.dart    25x SaturatedThing occurrences
//	lib/feat/refs.dart         2x SignupFlow + 15x CheckoutUseCase
//	lib/gen/other.g.dart       5x SignupFlow  (generated → must be ignored)
//	lib/gen/other.freezed.dart 3x CheckoutUseCase (generated → must be ignored)
func buildScoreRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write := func(rel, content string) {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("lib/feat/alpha.dart", "// alpha\n\nclass SignupFlow {}\n\nclass UserRepository {}\n")
	write("lib/feat/beta.dart", "// beta\n\nclass CheckoutUseCase {}\n")
	write("lib/feat/gamma.dart", "import 'data/user_repository.dart';\n\nclass Gamma {}\n")
	write("lib/feat/delta.dart", "import 'ui/theme.dart';\n\nclass Delta {}\n")
	write("lib/feat/saturated.dart",
		"// SaturatedThing SaturatedThing SaturatedThing SaturatedThing SaturatedThing\n"+
			"// SaturatedThing SaturatedThing SaturatedThing SaturatedThing SaturatedThing\n"+
			"// SaturatedThing SaturatedThing SaturatedThing SaturatedThing SaturatedThing\n"+
			"// SaturatedThing SaturatedThing SaturatedThing SaturatedThing SaturatedThing\n"+
			"// SaturatedThing SaturatedThing SaturatedThing SaturatedThing SaturatedThing\n"+
			"class SaturatedThing {}\n") // 25 occurrences + decl = 26
	write("lib/feat/refs.dart", "SignupFlow SignupFlow\nCheckoutUseCase CheckoutUseCase CheckoutUseCase CheckoutUseCase CheckoutUseCase\n"+
		"CheckoutUseCase CheckoutUseCase CheckoutUseCase CheckoutUseCase CheckoutUseCase CheckoutUseCase CheckoutUseCase CheckoutUseCase CheckoutUseCase CheckoutUseCase\n")
	write("lib/gen/other.g.dart", "// GENERATED\nSignupFlow SignupFlow SignupFlow SignupFlow SignupFlow\n")
	write("lib/gen/other.freezed.dart", "// GENERATED\nCheckoutUseCase CheckoutUseCase CheckoutUseCase\n")
	return root
}

func TestScoreAllGoldenMath(t *testing.T) {
	root := buildScoreRepo(t)
	idx, err := loadSourceIndex(root, "lib")
	if err != nil {
		t.Fatalf("loadSourceIndex: %v", err)
	}

	cs := []Candidate{
		{MarkerKind: "notifier_method", EntrySymbolPath: "lib/feat/alpha.dart#SignupFlow.begin",
			IntentSignals: IntentSignals{ClassName: "SignupFlow"}},
		{MarkerKind: "usecase_call", EntrySymbolPath: "lib/feat/beta.dart#CheckoutUseCase.run",
			IntentSignals: IntentSignals{ClassName: "CheckoutUseCase"}},
		{MarkerKind: "state_mutation", EntrySymbolPath: "lib/feat/saturated.dart#SaturatedThing.bump",
			IntentSignals: IntentSignals{ClassName: "SaturatedThing"}},
		{MarkerKind: "bloc_handler", EntrySymbolPath: "lib/feat/gamma.dart#Gamma.load",
			IntentSignals: IntentSignals{ClassName: "Gamma"}},
	}
	ScoreAll(cs, idx)

	// Golden case 1: notifier base .8*0.85 + fanIn 3/20*0.1 (.015) + boundary .05.
	// fanIn counts word-boundary hits across non-generated files only:
	// decl in alpha.dart (1) + refs.dart (2); the .g.dart copies are excluded.
	if cs[0].FanIn != 3 {
		t.Errorf("fanIn = %d, want 3", cs[0].FanIn)
	}
	if !cs[0].BoundaryReachable {
		t.Error("alpha.dart mentions UserRepository; boundaryReachable = false")
	}
	if want := 0.8*scoreBaseWeight + 3.0/fanInSaturatingCount*fanInMaxContribution + boundaryBonus; math.Abs(cs[0].Score-want) > 1e-12 {
		t.Errorf("score = %.17g, want %.17g (0.745 nominal)", cs[0].Score, want)
	}

	// Golden case 2: usecase base .9*0.85 + fanIn 16/20*0.1 (.08), no boundary.
	if cs[1].FanIn != 16 {
		t.Errorf("fanIn = %d, want 16", cs[1].FanIn)
	}
	if cs[1].BoundaryReachable {
		t.Error("beta.dart has no boundary evidence; boundaryReachable = true")
	}
	if want := 0.9*scoreBaseWeight + 16.0/fanInSaturatingCount*fanInMaxContribution; math.Abs(cs[1].Score-want) > 1e-12 {
		t.Errorf("score = %.17g, want %.17g (0.845 nominal)", cs[1].Score, want)
	}

	// Fan-in saturation: min(fanIn/20, 1)*0.1 caps the contribution at +0.10.
	if cs[2].FanIn != 26 { // 25 comment hits + 1 declaration
		t.Errorf("fanIn = %d, want 26", cs[2].FanIn)
	}
	if want := 0.45*scoreBaseWeight + fanInMaxContribution; math.Abs(cs[2].Score-want) > 1e-12 {
		t.Errorf("saturated score = %.17g, want %.17g", cs[2].Score, want)
	}

	// Boundary via imported file basename (case-insensitive snake_case).
	if !cs[3].BoundaryReachable {
		t.Error("gamma.dart imports user_repository.dart; boundaryReachable = false")
	}

	// Negative control: import basename without any boundary suffix.
	negs := []Candidate{{MarkerKind: "route_callback", EntrySymbolPath: "lib/feat/delta.dart#Delta.go",
		IntentSignals: IntentSignals{ClassName: "Delta"}}}
	ScoreAll(negs, idx)
	if negs[0].BoundaryReachable {
		t.Error("delta.dart imports theme.dart; boundaryReachable = true")
	}
	if want := 0.6*scoreBaseWeight + 1.0/fanInSaturatingCount*fanInMaxContribution; math.Abs(negs[0].Score-want) > 1e-12 {
		t.Errorf("plain score = %.17g, want %.17g", negs[0].Score, want)
	}
}

func TestCandidateScoreClampsToUnitRange(t *testing.T) {
	// The strongest real combination (usecase base + saturated fan-in +
	// boundary) tops out at 0.915 — the clamp must not distort it.
	if got := candidateScore(0.9, 100000, true); math.Abs(got-0.915) > 1e-12 {
		t.Errorf("candidateScore realistic max = %v, want 0.915", got)
	}
	// Out-of-range bases (defensive: adapter kinds are enum-closed) still
	// clamp to [0,1].
	if got := candidateScore(2.5, 100000, true); got != 1.0 {
		t.Errorf("candidateScore overflow clamp = %v, want 1", got)
	}
	if got := candidateScore(-3, 0, false); got != 0 {
		t.Errorf("candidateScore floor = %v, want 0", got)
	}
}

func TestScoreAllDeterministicBytes(t *testing.T) {
	root := buildScoreRepo(t)
	idx, err := loadSourceIndex(root, "lib")
	if err != nil {
		t.Fatalf("loadSourceIndex: %v", err)
	}
	mk := func() []Candidate {
		return []Candidate{
			{MarkerKind: "notifier_method", EntrySymbolPath: "lib/feat/alpha.dart#SignupFlow.begin",
				IntentSignals: IntentSignals{ClassName: "SignupFlow"}, Score: 0.5},
			{MarkerKind: "usecase_call", EntrySymbolPath: "lib/feat/beta.dart#CheckoutUseCase.run",
				IntentSignals: IntentSignals{ClassName: "CheckoutUseCase"}, FanIn: 99, BoundaryReachable: true},
		}
	}
	a, b := mk(), mk()
	ScoreAll(a, idx)
	ScoreAll(b, idx)
	ab, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	if !bytes.Equal(ab, bb) {
		t.Fatalf("scoring not deterministic:\n%s\n%s", ab, bb)
	}
	if a[1].FanIn != 16 || a[1].BoundaryReachable {
		t.Errorf("placeholder values not overwritten: %+v", a[1])
	}
}

func TestDedupAndTieBreakGroupingAndOrdering(t *testing.T) {
	cs := []Candidate{
		{CandidateID: "cand-bbbbbbbbbbbbbbb1", MarkerKind: "notifier_method", Score: 0.9,
			RootEquivalenceKey: "K", EntrySymbolPath: "lib/b.dart#B.go"},
		{CandidateID: "cand-aaaaaaaaaaaaaaaa2", MarkerKind: "usecase_call", Score: 0.9,
			RootEquivalenceKey: "K", EntrySymbolPath: "lib/a.dart#A.go"},
		{CandidateID: "cand-cccccccccccccccc3", MarkerKind: "notifier_method", Score: 0.8,
			RootEquivalenceKey: "K", EntrySymbolPath: "lib/c.dart#C.go"},
		{CandidateID: "cand-zzzzzzzzzzzzzzzz4", MarkerKind: "route_callback", Score: 0.95,
			RootEquivalenceKey: "Z", EntrySymbolPath: "lib/z.dart#Z.x"},
	}
	DedupAndTieBreak(cs)

	wantOrder := []string{"cand-zzzzzzzzzzzzzzzz4", "cand-aaaaaaaaaaaaaaaa2", "cand-bbbbbbbbbbbbbbb1", "cand-cccccccccccccccc3"}
	for i, w := range wantOrder {
		if cs[i].CandidateID != w {
			t.Fatalf("order[%d] = %s, want %s (full: %v)", i, cs[i].CandidateID, w, ids(cs))
		}
	}
	// Representatives (z of group Z, a of group K) keep root status; losers
	// keep their payload slot, flagged into their representative — never
	// silently discarded.
	if cs[0].DedupedInto != nil || cs[1].DedupedInto != nil {
		t.Errorf("representatives lost root status: %v / %v", deref(cs[0].DedupedInto), deref(cs[1].DedupedInto))
	}
	if deref(cs[2].DedupedInto) != "cand-aaaaaaaaaaaaaaaa2" {
		t.Errorf("dedupedInto = %v, want cand-aaaaaaaaaaaaaaaa2", deref(cs[2].DedupedInto))
	}
	// tieBreakRank is assigned within each rootEquivalenceKey group:
	// Z:(z=0), K:(a=0, b=1, c=2).
	for i, want := range []int{0, 0, 1, 2} {
		if cs[i].TieBreakRank != want {
			t.Errorf("cs[%d] (%s).TieBreakRank = %d, want %d", i, cs[i].CandidateID, cs[i].TieBreakRank, want)
		}
	}
}

func TestTieBreakEqualScoreFallsToSpecificityThenPath(t *testing.T) {
	cs := []Candidate{
		{CandidateID: "cand-notifier000000001", MarkerKind: "notifier_method", Score: 0.75,
			RootEquivalenceKey: "K", EntrySymbolPath: "lib/a.dart#A.alpha"},
		{CandidateID: "cand-usecase000000002", MarkerKind: "usecase_call", Score: 0.75,
			RootEquivalenceKey: "K", EntrySymbolPath: "lib/z.dart#Z.omega"},
		{CandidateID: "cand-route00000000003", MarkerKind: "route_callback", Score: 0.75,
			RootEquivalenceKey: "K", EntrySymbolPath: "lib/m.dart#M.mid"},
	}
	DedupAndTieBreak(cs)
	want := []string{"cand-usecase000000002", "cand-notifier000000001", "cand-route00000000003"}
	for i, w := range want {
		if cs[i].CandidateID != w {
			t.Fatalf("order[%d] = %s, want %s", i, cs[i].CandidateID, w)
		}
	}
	if cs[0].TieBreakRank != 0 || cs[1].TieBreakRank != 1 || cs[2].TieBreakRank != 2 {
		t.Errorf("ranks = %d,%d,%d", cs[0].TieBreakRank, cs[1].TieBreakRank, cs[2].TieBreakRank)
	}
}

func TestFinalizeRestoresPinnedLosersAndResorts(t *testing.T) {
	repID := "cand-representative1"
	loser := Candidate{CandidateID: "cand-loserpinned00001", MarkerKind: "notifier_method", Score: 0.7,
		RootEquivalenceKey: "K", EntrySymbolPath: "lib/l.dart#L.go",
		DedupedInto: &repID, ManifestOverride: "pinned"}
	rep := Candidate{CandidateID: repID, MarkerKind: "usecase_call", Score: 0.9,
		RootEquivalenceKey: "K", EntrySymbolPath: "lib/r.dart#R.go"}
	lowUnflagged := Candidate{CandidateID: "cand-lowunflagged0001", MarkerKind: "state_mutation", Score: 0.1,
		RootEquivalenceKey: "Q", EntrySymbolPath: "lib/q.dart#Q.go"}

	cs := []Candidate{loser, rep, lowUnflagged}
	out := Finalize(cs)

	if out[0].CandidateID != repID || out[1].CandidateID != loser.CandidateID || out[2].CandidateID != lowUnflagged.CandidateID {
		t.Fatalf("order = %v", ids(out))
	}
	if out[1].DedupedInto != nil {
		t.Errorf("pinned loser still dedupedInto=%v; pinning must force inclusion", deref(out[1].DedupedInto))
	}
}

func TestMarkerSpecificityTableMatchesTicketConstants(t *testing.T) {
	want := map[string]float64{
		"usecase_call":       0.90,
		"notifier_method":    0.80,
		"bloc_handler":       0.75,
		"route_callback":     0.60,
		"lifecycle_callback": 0.50,
		"state_mutation":     0.45,
	}
	if len(markerSpecificity) != len(want) {
		t.Fatalf("markerSpecificity has %d entries, want %d", len(markerSpecificity), len(want))
	}
	// Walk kinds from most to least specific so the strict-ordering
	// assertion is independent of map iteration order.
	kinds := make([]string, 0, len(want))
	for kind := range want {
		kinds = append(kinds, kind)
	}
	sort.Slice(kinds, func(i, j int) bool { return want[kinds[i]] > want[kinds[j]] })
	prevBase := math.Inf(1)
	prevRank := math.MaxInt
	for _, kind := range kinds {
		base := want[kind]
		if markerSpecificity[kind] != base {
			t.Errorf("markerSpecificity[%s] = %v, want %v", kind, markerSpecificity[kind], base)
		}
		rank := markerRank(kind)
		if rank >= prevRank || base >= prevBase {
			t.Errorf("%s breaks strict ordering: rank %d (prev %d), base %v (prev %v)", kind, rank, prevRank, base, prevBase)
		}
		prevRank, prevBase = rank, base
	}
	if markerRank("unknown_kind") != -1 {
		t.Error("unknown kinds must sort last")
	}
}

func ids(cs []Candidate) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.CandidateID
	}
	return out
}

func deref(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}
