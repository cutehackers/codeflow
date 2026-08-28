package flowview

import (
	"os"
	"strings"
	"testing"
)

func TestExtractSignatureFromLineHint(t *testing.T) {
	repoRoot := "../../testdata/eval_app"
	got := extractSignature(repoRoot, "lib/persist/vault.dart", 0, 3, "Vault")
	if got.Line != 3 {
		t.Errorf("line = %d, want 3", got.Line)
	}
	if got.Signature == "" {
		t.Fatalf("signature empty")
	}
	if !strings.Contains(got.Signature, "class Vault") {
		t.Errorf("signature = %q, want the Vault declaration", got.Signature)
	}
}

func TestExtractSignaturePrefersByteOffset(t *testing.T) {
	repoRoot := "../../testdata/eval_app"
	data, err := os.ReadFile(repoRoot + "/lib/persist/vault.dart")
	if err != nil {
		t.Fatal(err)
	}
	idx := strings.Index(string(data), "void put(String key)")
	if idx < 0 {
		t.Fatal("probe string missing from fixture")
	}
	got := extractSignature(repoRoot, "lib/persist/vault.dart", idx, 0, "put")
	if got.Signature == "" {
		t.Fatal("byte-offset extraction produced nothing")
	}
	if !strings.Contains(got.Signature, "put") {
		t.Errorf("signature = %q, want the put declaration", got.Signature)
	}
}

func TestExtractSignatureMissingFileIsEmpty(t *testing.T) {
	got := extractSignature(t.TempDir(), "lib/nowhere.dart", 0, 1, "")
	if got.Signature != "" || got.Line != 0 {
		t.Errorf("missing file must yield empty result, got %+v", got)
	}
}

func TestExtractSignatureTraversalRejected(t *testing.T) {
	got := extractSignature(t.TempDir(), "../escape.dart", 0, 1, "")
	if got.Signature != "" {
		t.Errorf("path traversal must be rejected, got %+v", got)
	}
}

func TestBuildArchitectureMapAggregates(t *testing.T) {
	docs := [][]byte{evalDocA(), evalDocB()}
	m := buildArchitectureMap(t.TempDir(), "gen-test", docs,
		map[string]string{"Util.doIt": LayerExternal},
		[]string{"Panel.show", "AdminSheet.show", "Panel.show"})

	if m.GenerationID != "gen-test" {
		t.Errorf("generationId = %q", m.GenerationID)
	}
	laneIDs := map[string]bool{}
	for _, l := range m.Lanes {
		laneIDs[l.ID] = true
		if l.Label == "" {
			t.Errorf("lane %q has empty label", l.ID)
		}
	}
	for _, want := range []string{LayerPresentation, LayerUsecase, LayerState, LayerData, LayerExternal} {
		if !laneIDs[want] {
			t.Errorf("lane %q missing; got %v", want, m.Lanes)
		}
	}

	bySym := map[string]MapComponent{}
	for _, c := range m.Components {
		bySym[c.SymbolPath] = c
	}
	if len(bySym) != 8 {
		t.Errorf("components = %d unique symbols, want 8", len(bySym))
	}
	u := bySym["Util.doIt"]
	if u.Layer != LayerExternal || u.Confidence != 1.0 {
		t.Errorf("override not reflected on Util.doIt: %+v", u)
	}
	v := bySym["Vault.put"]
	if v.Path != "lib/persist/vault.dart" {
		t.Errorf("component path = %q", v.Path)
	}
	if len(v.Flows) != 2 {
		t.Errorf("Vault.put flows = %v, want both eval flows", v.Flows)
	}
	if v.Importance <= bySym["Keeper.watch"].Importance {
		t.Errorf("importance should reward cross-flow reuse: vault=%v keeper=%v", v.Importance, bySym["Keeper.watch"].Importance)
	}
	if len(m.EntryPoints) != 2 || m.EntryPoints[0] != "AdminSheet.show" {
		t.Errorf("entryPoints = %v, want deduped sorted pair", m.EntryPoints)
	}

	relByPair := map[string]MapRelation{}
	for _, r := range m.Relations {
		relByPair[r.FromSymbolPath+"->"+r.ToSymbolPath+"#"+r.Kind] = r
	}
	dr := relByPair["Panel.show->Dispatcher.run#resolved_cross_file"]
	if dr.Count != 1 || len(dr.Flows) != 1 {
		t.Errorf("panel relation = %+v", dr)
	}
	adminToDisp := relByPair["AdminSheet.show->Dispatcher.run#resolved_cross_file"]
	if adminToDisp.FromSymbolPath == "" {
		t.Errorf("admin relation missing")
	}
}

func TestExtractSignatureWalksUpToDeclaration(t *testing.T) {
	// keeper.dart: 'void watch' body line is deep inside; the extractor must
	// land on the method header, not a statement.
	repoRoot := "../../testdata/eval_app"
	data, err := os.ReadFile(repoRoot + "/lib/shared/keeper.dart")
	if err != nil {
		t.Fatal(err)
	}
	idx := strings.Index(string(data), "_phase = 'watching'")
	if idx < 0 {
		t.Fatal("probe missing")
	}
	// No byte offset: only a line hint inside the method body.
	got := extractSignature(repoRoot, "lib/shared/keeper.dart", 0, 8, "watch")
	if !strings.Contains(got.Signature, "void watch") && !strings.Contains(got.Signature, "class Keeper") {
		t.Errorf("signature = %q (%d), want an enclosing declaration", got.Signature, got.Line)
	}
	if got.Line > 6 {
		t.Errorf("line = %d, want at or above the watch declaration", got.Line)
	}
	_ = idx
}

func TestExtractSignatureRealWorldAnchors(t *testing.T) {
	// Pins the two failure shapes observed on example_app:
	//  1. multi-line parameter header ("Future<void> _onItemAdded(")
	//  2. focus line at file top far from a top-level function header
	repoRoot := "../../testdata/example_app"

	got := extractSignature(repoRoot, "lib/features/cart/cart_bloc.dart", 0, 38, "_onItemAdded")
	if !strings.Contains(got.Signature, "Future<void> _onItemAdded(") {
		t.Errorf("multi-line param header: sig=%q line=%d", got.Signature, got.Line)
	}

	got = extractSignature(repoRoot, "lib/main.dart", 0, 1, "firebaseMessagingBackgroundHandler")
	if !strings.Contains(got.Signature, "firebaseMessagingBackgroundHandler(") {
		t.Errorf("far-from-header top-level fn: sig=%q line=%d", got.Signature, got.Line)
	}
}

func TestCoverageDocsJoinPublishedGraph(t *testing.T) {
	// A sliced-but-never-published candidate introduces a new symbol AND a
	// file-qualified edge that must resolve against the bare published one.
	payload := []byte(`{
		"candidateId": "cand-xyz",
		"entrySymbolPath": "lib/extra/orphan.dart#Orphan.run",
		"steps": [
			{"ordinal": 1, "anchor": {"repoRelativePath": "lib/extra/orphan.dart", "enclosingSymbolPath": "lib/extra/orphan.dart#Orphan.run"}},
			{"ordinal": 2, "anchor": {"repoRelativePath": "lib/core/dispatcher.dart", "enclosingSymbolPath": "lib/core/dispatcher.dart#Dispatcher.run"}}
		],
		"edges": [
			{"kind": "resolved_cross_file", "toSymbolPath": "lib/core/dispatcher.dart#Dispatcher.run", "resolutionStatus": "resolved", "stepOrdinal": 1}
		]
	}`)
	docs := [][]byte{evalDocA()}
	all := append(append([][]byte{}, docs...), synthesizeCoverageDocs([][]byte{payload})...)
	m := buildArchitectureMap(t.TempDir(), "gen-cov", all, nil, []string{"Panel.show"})

	bySym := map[string]MapComponent{}
	for _, c := range m.Components {
		bySym[c.SymbolPath] = c
	}
	orphan, ok := bySym["Orphan.run"]
	if !ok {
		t.Fatalf("coverage symbol missing; have %d components", len(m.Components))
	}
	if len(orphan.Flows) != 1 || orphan.Flows[0] != "coverage:cand-xyz" {
		t.Errorf("orphan flows = %v", orphan.Flows)
	}

	// File-qualified slice edge resolves onto the bare published vertex.
	foundRel := false
	for _, r := range m.Relations {
		if r.FromSymbolPath == "Orphan.run" && r.ToSymbolPath == "Dispatcher.run" {
			foundRel = true
		}
	}
	if !foundRel {
		t.Errorf("normalized coverage relation missing; relations=%+v", m.Relations)
	}
}

func TestSynthesizeCoverageSkipsBrokenPayloads(t *testing.T) {
	out := synthesizeCoverageDocs([][]byte{
		[]byte(`{not json`),
		[]byte(`{"candidateId":""}`),
		[]byte(`{"candidateId":"empty-steps","steps":[]}`),
	})
	if len(out) != 0 {
		t.Errorf("expected nothing synthesized, got %d docs", len(out))
	}
}
