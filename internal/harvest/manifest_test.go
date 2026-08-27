package harvest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseManifestValid(t *testing.T) {
	src := `# codeflow.flows.yaml — pins, excludes, renames (decision #14)
flows:
  - entry: lib/features/auth/email_signup_notifier.dart#EmailSignupNotifier.submit
    name: 회원가입            # optional display name
  - entry: 'lib/features/cart/cart_bloc.dart#CartBloc._onItemAdded'

excluded:
  - lib/features/settings/settings_notifier.dart#SettingsNotifier.toggleDarkMode
  - "lib/main.dart#ShopHomeScreen.onCheckoutPressed"   # quoted + trailing comment
`
	m, err := ParseManifest(src)
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	if len(m.Flows) != 2 {
		t.Fatalf("flows = %d items, want 2: %+v", len(m.Flows), m.Flows)
	}
	if m.Flows[0].Entry != "lib/features/auth/email_signup_notifier.dart#EmailSignupNotifier.submit" {
		t.Errorf("flows[0].Entry = %q", m.Flows[0].Entry)
	}
	if m.Flows[0].Name != "회원가입" {
		t.Errorf("flows[0].Name = %q, want 회원가입", m.Flows[0].Name)
	}
	if m.Flows[1].Entry != "lib/features/cart/cart_bloc.dart#CartBloc._onItemAdded" || m.Flows[1].Name != "" {
		t.Errorf("flows[1] = %+v, want quoted entry without name", m.Flows[1])
	}
	wantExcluded := []string{
		"lib/features/settings/settings_notifier.dart#SettingsNotifier.toggleDarkMode",
		"lib/main.dart#ShopHomeScreen.onCheckoutPressed",
	}
	if len(m.Excluded) != len(wantExcluded) {
		t.Fatalf("excluded = %v", m.Excluded)
	}
	for i, w := range wantExcluded {
		if m.Excluded[i] != w {
			t.Errorf("excluded[%d] = %q, want %q", i, m.Excluded[i], w)
		}
	}
}

func TestParseManifestMalformedReportsLineNumbers(t *testing.T) {
	cases := []struct {
		name    string
		src     string
		wantErr string
	}{
		{
			name:    "unknown top-level key",
			src:     "profles:\n  - entry: lib/a.dart#A.b\n",
			wantErr: "line 1: unknown top-level key \"profles\"",
		},
		{
			name:    "flow item without entry at EOF",
			src:     "flows:\n  - name: only-name\n",
			wantErr: "line 2: flow item has no 'entry:' key",
		},
		{
			name:    "unknown flow key on continuation line",
			src:     "flows:\n  - entry: lib/a.dart#A.b\n    nmae: x\n",
			wantErr: "line 3: unknown flow key \"nmae\"",
		},
		{
			name:    "list item outside any section",
			src:     "- entry: lib/a.dart#A.b\n",
			wantErr: "line 1: list item outside a 'flows:'/'excluded:' section",
		},
		{
			name:    "entry not canonical shape",
			src:     "flows:\n  - entry: features/a.dart\n",
			wantErr: "line 2: entry \"features/a.dart\" is not a canonical",
		},
		{
			name:    "tab indentation",
			src:     "flows:\n\t- entry: lib/a.dart#A.b\n",
			wantErr: "line 2: tab indentation is not allowed",
		},
		{
			name:    "unterminated quote",
			src:     "flows:\n  - entry: 'lib/a.dart#A.b\n",
			wantErr: "line 2: unterminated ' quote",
		},
		{
			name:    "inline collection after section key",
			src:     "flows: []\n",
			wantErr: "line 1: expected top-level 'flows:' or 'excluded:'",
		},
		{
			name:    "bare dash without key under flows",
			src:     "flows:\n  - lib/a.dart#A.b\n",
			wantErr: "line 2: flow items must start with '- entry:",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseManifest(tc.src)
			if err == nil {
				t.Fatalf("ParseManifest succeeded, want error containing %q", tc.wantErr)
			}
			if got := err.Error(); !strings.Contains(got, tc.wantErr) {
				t.Fatalf("error = %q, want it to contain %q", got, tc.wantErr)
			}
		})
	}
}

func TestLoadManifestMissingFileIsEmptyNoError(t *testing.T) {
	dir := t.TempDir()
	m, err := LoadManifest(dir)
	if err != nil {
		t.Fatalf("LoadManifest(missing): %v", err)
	}
	if m == nil || len(m.Flows) != 0 || len(m.Excluded) != 0 {
		t.Fatalf("manifest = %+v, want empty non-nil", m)
	}
}

func TestLoadManifestReadsFileAndReportsPathOnErrors(t *testing.T) {
	dir := t.TempDir()

	good := filepath.Join(dir, ManifestFileName)
	valid := "flows:\n  - entry: lib/a.dart#A.b\n    name: Alpha\nexcluded:\n  - lib/b.dart#B.c\n"
	if err := os.WriteFile(good, []byte(valid), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := LoadManifest(dir)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if len(m.Flows) != 1 || m.Flows[0].Name != "Alpha" || len(m.Excluded) != 1 {
		t.Fatalf("manifest = %+v", m)
	}

	bad := filepath.Join(dir, ManifestFileName)
	if err := os.WriteFile(bad, []byte("oops: nope\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = LoadManifest(dir)
	if err == nil {
		t.Fatal("LoadManifest(bad file) succeeded")
	}
	if !strings.Contains(err.Error(), ManifestFileName) || !strings.Contains(err.Error(), "line 1") {
		t.Fatalf("error = %q, want path + line number", err)
	}
}

func candidatesForApply() []Candidate {
	mk := func(id, path string) Candidate {
		return Candidate{
			CandidateID:        id,
			TriggerClass:       "user_action",
			MarkerKind:         "notifier_method",
			EntrySymbolPath:    path,
			IntentSignals:      IntentSignals{ClassName: "C", DerivedName: "Original", DocLine: nil, PackageName: "p"},
			Score:              0.5,
			FanIn:              0,
			BoundaryReachable:  false,
			RootEquivalenceKey: "C",
			DedupedInto:        nil,
			TieBreakRank:       0,
			ManifestOverride:   "none",
		}
	}
	return []Candidate{
		mk("cand-0000000000000001", "lib/excluded.dart#Thing.drop"),
		mk("cand-0000000000000002", "lib/pinned.dart#Thing.rename"),
		mk("cand-0000000000000003", "lib/pinned2.dart#Thing.keepname"),
		mk("cand-0000000000000004", "lib/other.dart#Thing.untouched"),
	}
}

func TestManifestApplyExclusionPinningNaming(t *testing.T) {
	m := &Manifest{
		Flows: []PinnedFlow{
			{Entry: "lib/pinned.dart#Thing.rename", Name: "회원가입"},
			{Entry: "lib/pinned2.dart#Thing.keepname"}, // no name → keep derivedName
		},
		Excluded: []string{"lib/excluded.dart#Thing.drop"},
	}
	cs := candidatesForApply()
	m.Apply(cs)

	if cs[0].ManifestOverride != "excluded" {
		t.Errorf("excluded candidate override = %q", cs[0].ManifestOverride)
	}
	if cs[1].ManifestOverride != "pinned" {
		t.Errorf("pinned candidate override = %q", cs[1].ManifestOverride)
	}
	if cs[1].IntentSignals.DerivedName != "회원가입" {
		t.Errorf("pinned name override = %q, want 회원가입", cs[1].IntentSignals.DerivedName)
	}
	if cs[2].ManifestOverride != "pinned" || cs[2].IntentSignals.DerivedName != "Original" {
		t.Errorf("nameless pin = %+v, want pinned with derivedName kept", cs[2])
	}
	if cs[3].ManifestOverride != "none" || cs[3].IntentSignals.DerivedName != "Original" {
		t.Errorf("unmatched candidate changed: %+v", cs[3])
	}
}

func TestManifestApplyExclusionWinsOverPin(t *testing.T) {
	path := "lib/both.dart#Thing.x"
	m := &Manifest{
		Flows:    []PinnedFlow{{Entry: path, Name: "x"}},
		Excluded: []string{path},
	}
	cs := []Candidate{candidatesForApply()[0]}
	cs[0].EntrySymbolPath = path
	m.Apply(cs)
	if cs[0].ManifestOverride != "excluded" {
		t.Fatalf("override = %q, want excluded to win over pinned", cs[0].ManifestOverride)
	}
}

func TestManifestIsPinned(t *testing.T) {
	m := &Manifest{Flows: []PinnedFlow{{Entry: "lib/a.dart#A.b"}}}
	if !m.IsPinned("lib/a.dart#A.b") {
		t.Error("IsPinned = false for listed entry")
	}
	if m.IsPinned("lib/other.dart#O.x") {
		t.Error("IsPinned = true for unknown entry")
	}
}

func TestParseManifestLaneOverrides(t *testing.T) {
	src := `flows:
  - entry: lib/a.dart#A.b

laneOverrides:
  - symbol: JoinRepository.cancel
    lane: data
  - symbol: 'Panel.show'
    lane: ui
`
	m, err := ParseManifest(src)
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	if len(m.LaneOverrides) != 2 {
		t.Fatalf("laneOverrides = %d items, want 2: %+v", len(m.LaneOverrides), m.LaneOverrides)
	}
	if m.LaneOverrides[0].Symbol != "JoinRepository.cancel" || m.LaneOverrides[0].Lane != "data" {
		t.Errorf("laneOverrides[0] = %+v", m.LaneOverrides[0])
	}
	if m.LaneOverrides[1].Symbol != "Panel.show" || m.LaneOverrides[1].Lane != "ui" {
		t.Errorf("laneOverrides[1] = %+v", m.LaneOverrides[1])
	}
	got := m.LaneOverrideMap()
	if got["JoinRepository.cancel"] != "data" || got["Panel.show"] != "ui" {
		t.Errorf("LaneOverrideMap = %v", got)
	}
}

func TestParseManifestLaneOverrideRejectsBadLane(t *testing.T) {
	src := `laneOverrides:
  - symbol: X.y
    lane: Not-A-Lane
`
	if _, err := ParseManifest(src); err == nil || !strings.Contains(err.Error(), "single lowercase word") {
		t.Fatalf("err = %v, want lane shape error", err)
	}
}

func TestWriteLaneOverrideCreatesFileAndRoundTrips(t *testing.T) {
	dir := t.TempDir()
	if err := WriteLaneOverride(dir, "Vault.put", "data"); err != nil {
		t.Fatalf("WriteLaneOverride (create): %v", err)
	}
	m, err := LoadManifest(dir)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if len(m.Flows) != 0 || len(m.Excluded) != 0 {
		t.Fatalf("unexpected sections: %+v", m)
	}
	if m.LaneOverrideMap()["Vault.put"] != "data" {
		t.Fatalf("override map = %v", m.LaneOverrideMap())
	}

	// Second write appends a second override; first survives.
	if err := WriteLaneOverride(dir, "Panel.show", "ui"); err != nil {
		t.Fatalf("WriteLaneOverride (append): %v", err)
	}
	m, _ = LoadManifest(dir)
	mp := m.LaneOverrideMap()
	if mp["Vault.put"] != "data" || mp["Panel.show"] != "ui" {
		t.Fatalf("override map after append = %v", mp)
	}

	// Third write replaces the lane of an existing symbol in place.
	if err := WriteLaneOverride(dir, "Vault.put", "external"); err != nil {
		t.Fatalf("WriteLaneOverride (replace): %v", err)
	}
	m, _ = LoadManifest(dir)
	if got := m.LaneOverrideMap()["Vault.put"]; got != "external" {
		t.Fatalf("replaced lane = %q, want external; overrides=%+v", got, m.LaneOverrides)
	}
	if len(m.LaneOverrides) != 2 {
		t.Fatalf("replace duplicated entries: %+v", m.LaneOverrides)
	}
}

func TestWriteLaneOverridePreservesExistingSections(t *testing.T) {
	dir := t.TempDir()
	src := "# header comment\nflows:\n  - entry: lib/a.dart#A.b\n    name: 회원가입\n"
	if err := os.WriteFile(filepath.Join(dir, ManifestFileName), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteLaneOverride(dir, "Keeper.watch", "state"); err != nil {
		t.Fatalf("WriteLaneOverride: %v", err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, ManifestFileName))
	if !strings.HasPrefix(string(raw), "# header comment") {
		t.Errorf("header lost:\n%s", raw)
	}
	if !strings.Contains(string(raw), "name: 회원가입") {
		t.Errorf("existing flow item lost:\n%s", raw)
	}
	m, err := ParseManifest(string(raw))
	if err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	if len(m.Flows) != 1 || m.Flows[0].Name != "회원가입" {
		t.Errorf("flows damaged: %+v", m.Flows)
	}
	if m.LaneOverrideMap()["Keeper.watch"] != "state" {
		t.Errorf("override missing: %+v", m.LaneOverrides)
	}
}

func TestWriteLaneOverrideAppendsBeforeFollowingSection(t *testing.T) {
	// Regression: laneOverrides followed by flows — a new override must be
	// spliced INSIDE its own section, never into the next one.
	dir := t.TempDir()
	src := "laneOverrides:\n  - symbol: A.a\n    lane: ui\nflows:\n  - entry: lib/x.dart#X.y\n    name: 첫 흐름\n"
	if err := os.WriteFile(filepath.Join(dir, ManifestFileName), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteLaneOverride(dir, "B.b", "data"); err != nil {
		t.Fatalf("WriteLaneOverride: %v", err)
	}
	m, err := LoadManifest(dir)
	if err != nil {
		t.Fatalf("LoadManifest after splice: %v", err)
	}
	if m.LaneOverrideMap()["B.b"] != "data" || m.LaneOverrideMap()["A.a"] != "ui" {
		t.Fatalf("overrides = %+v", m.LaneOverrides)
	}
	if len(m.Flows) != 1 || m.Flows[0].Entry != "lib/x.dart#X.y" || m.Flows[0].Name != "첫 흐름" {
		t.Fatalf("following section damaged: %+v", m.Flows)
	}
}
