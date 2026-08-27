package flowview

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"testing"
)

// evalCase is one hand-labeled ground-truth fact about the eval_app fixture
// (testdata/eval_app): a project whose naming deliberately avoids every
// classic architecture keyword. The gate: the adaptive engine must place at
// least 90% of symbols correctly using structure (entries, call edges,
// observed effects) instead of names — this is the R1 regression harness.
type evalCase struct {
	symbol        string
	wantLayer     string
	wantUncertain bool
}

var evalGroundTruth = []evalCase{
	{"Panel.show", LayerUI, false},              // published entry point
	{"AdminSheet.show", LayerUI, false},         // second published entry point
	{"Dispatcher.run", LayerApplication, false}, // structural middle, unnamed
	{"Ledger.commit", LayerData, false},         // pure propagation: caller=middle, callee=persist
	{"Vault.put", LayerData, false},             // /persist/ path segment
	{"Gateway.send", LayerExternal, false},      // observed side effect
	{"Keeper.watch", LayerState, false},         // observed state delta
	{"Util.doIt", LayerApplication, true},       // nothing known: honest uncertainty
}

func evalDocA() []byte {
	return []byte(`{
		"flowId": "flow-evalaaaaaaaaaa1",
		"title": "panel journey",
		"steps": [
			{"ordinal": 1, "anchor": {"repoRelativePath": "lib/screens/a_panel.dart", "enclosingSymbolPath": "Panel.show"}},
			{"ordinal": 2, "anchor": {"repoRelativePath": "lib/core/dispatcher.dart", "enclosingSymbolPath": "Dispatcher.run"}},
			{"ordinal": 3, "anchor": {"repoRelativePath": "lib/core/ledger.dart", "enclosingSymbolPath": "Ledger.commit"}},
			{"ordinal": 4, "anchor": {"repoRelativePath": "lib/persist/vault.dart", "enclosingSymbolPath": "Vault.put"}},
			{"ordinal": 5, "anchor": {"repoRelativePath": "lib/net/gateway.dart", "enclosingSymbolPath": "Gateway.send"}, "sideEffect": "network send"},
			{"ordinal": 6, "anchor": {"repoRelativePath": "lib/shared/keeper.dart", "enclosingSymbolPath": "Keeper.watch"}, "stateDelta": {"before": "idle", "after": "watching"}},
			{"ordinal": 7, "anchor": {"repoRelativePath": "lib/misc/util.dart", "enclosingSymbolPath": "Util.doIt"}}
		],
		"edges": [
			{"kind": "resolved_cross_file", "toSymbolPath": "Dispatcher.run", "resolutionStatus": "resolved", "stepOrdinal": 1},
			{"kind": "resolved_cross_file", "toSymbolPath": "Ledger.commit", "resolutionStatus": "resolved", "stepOrdinal": 2},
			{"kind": "resolved_cross_file", "toSymbolPath": "Vault.put", "resolutionStatus": "resolved", "stepOrdinal": 3},
			{"kind": "boundary_call", "toSymbolPath": "Gateway.send", "resolutionStatus": "resolved", "stepOrdinal": 2},
			{"kind": "resolved_cross_file", "toSymbolPath": "Keeper.watch", "resolutionStatus": "resolved", "stepOrdinal": 2}
		]
	}`)
}

func evalDocB() []byte {
	return []byte(`{
		"flowId": "flow-evalbbbbbbbbbb2",
		"title": "admin journey",
		"steps": [
			{"ordinal": 1, "anchor": {"repoRelativePath": "lib/screens/b_sheet.dart", "enclosingSymbolPath": "AdminSheet.show"}},
			{"ordinal": 2, "anchor": {"repoRelativePath": "lib/core/dispatcher.dart", "enclosingSymbolPath": "Dispatcher.run"}},
			{"ordinal": 3, "anchor": {"repoRelativePath": "lib/persist/vault.dart", "enclosingSymbolPath": "Vault.put"}}
		],
		"edges": [
			{"kind": "resolved_cross_file", "toSymbolPath": "Dispatcher.run", "resolutionStatus": "resolved", "stepOrdinal": 1},
			{"kind": "resolved_cross_file", "toSymbolPath": "Vault.put", "resolutionStatus": "resolved", "stepOrdinal": 2}
		]
	}`)
}

// runEvalEngine executes the adaptive engine over both eval documents and
// returns the resulting per-symbol assignment.
func runEvalEngine(t *testing.T, docs ...[]byte) map[string]*symbolNode {
	t.Helper()
	graph := buildGraph(docs, nil)
	inferLanes(graph, nil)
	return graph
}

func TestEvalFixtureNamingIsKeywordFree(t *testing.T) {
	// Guard the fixture itself: every ground-truth symbol must be invisible
	// to the CLASSIC keyword classifier, otherwise the eval proves nothing.
	for _, ec := range evalGroundTruth {
		if _, convention := InferLayer("", ec.symbol, false, false); convention != "application" && convention != "external" && convention != "state" {
			t.Errorf("fixture symbol %q already matches classic keyword %q; rename it to keep the eval honest", ec.symbol, convention)
		}
	}
}

func TestEvalAdaptiveAccuracyGate(t *testing.T) {
	graph := runEvalEngine(t, evalDocA(), evalDocB())

	const accuracyGatePct = 90
	correct := 0
	for _, ec := range evalGroundTruth {
		n, ok := graph[ec.symbol]
		if !ok {
			t.Errorf("symbol %q missing from merged graph", ec.symbol)
			continue
		}
		if n.layer == ec.wantLayer {
			correct++
		} else {
			t.Errorf("symbol %q: layer = %q (conf %d), want %q", ec.symbol, n.layer, n.confidence, ec.wantLayer)
		}
		if n.uncertain != ec.wantUncertain {
			t.Errorf("symbol %q: uncertain = %v, want %v (conf %d)", ec.symbol, n.uncertain, ec.wantUncertain, n.confidence)
		}
	}
	got := 100 * correct / len(evalGroundTruth)
	if got < accuracyGatePct {
		t.Fatalf("adaptive classification accuracy %d%% < gate %d%% (%d/%d)", got, accuracyGatePct, correct, len(evalGroundTruth))
	}
}

func TestEvalEngineDeterministic(t *testing.T) {
	render := func() string {
		graph := runEvalEngine(t, evalDocA(), evalDocB())
		type row struct {
			Symbol string `json:"symbol"`
			Layer  string `json:"layer"`
			Conf   int    `json:"conf"`
			Uncert bool   `json:"uncertain"`
		}
		rows := []row{}
		keys := make([]string, 0, len(graph))
		for k := range graph {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			n := graph[k]
			rows = append(rows, row{Symbol: k, Layer: n.layer, Conf: n.confidence, Uncert: n.uncertain})
		}
		b, err := json.Marshal(rows)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		return string(b)
	}
	first := render()
	for i := 0; i < 5; i++ {
		if again := render(); again != first {
			t.Fatalf("engine nondeterministic on run %d:\n%s\nvs\n%s", i+2, again, first)
		}
	}
}

func TestEngineManualOverrideWins(t *testing.T) {
	docs := [][]byte{evalDocA()}
	overrides := map[string]string{"Keeper.watch": LayerData} // contradict the state-delta evidence
	graph := buildGraph(docs, overrides)
	inferLanes(graph, overrides)
	n := graph["Keeper.watch"]
	if n == nil || n.layer != LayerData {
		status := "missing"
		if n != nil {
			status = fmt.Sprintf("got %q", n.layer)
		}
		t.Fatalf("manual override must win outright; %s", status)
	}
	if n.confidence != confOverride || n.uncertain {
		t.Errorf("override confidence = %d uncertain=%v, want %d false", n.confidence, n.uncertain, confOverride)
	}
}

func TestEngineRejectsSingleVoterInheritance(t *testing.T) {
	doc := []byte(`{
		"flowId": "flow-singlevoter0001",
		"steps": [
			{"ordinal": 1, "anchor": {"repoRelativePath": "lib/screens/a_panel.dart", "enclosingSymbolPath": "Panel.show"}},
			{"ordinal": 2, "anchor": {"repoRelativePath": "lib/misc/mystery.dart", "enclosingSymbolPath": "Mystery.run"}}
		],
		"edges": [
			{"kind": "resolved_cross_file", "toSymbolPath": "Mystery.run", "resolutionStatus": "resolved", "stepOrdinal": 1}
		]
	}`)
	graph := runEvalEngine(t, doc)
	n := graph["Mystery.run"]
	if n == nil {
		t.Fatal("Mystery.run missing")
	}
	// One ui caller is not enough to make a leaf UI: rejected vote falls
	// through to the honest default instead of boundary inheritance.
	if n.layer != LayerApplication {
		t.Errorf("leaf with a single ui voter: layer = %q, want %q", n.layer, LayerApplication)
	}
	if !n.uncertain {
		t.Errorf("leaf with a single ui voter should be flagged uncertain")
	}
}

func TestApplyLayersWithOverridesDecoratesConfidence(t *testing.T) {
	out := applyLayersWith(evalDocA(), map[string]string{"Util.doIt": LayerExternal})
	var doc struct {
		Lanes []struct {
			ID string `json:"id"`
		} `json:"lanes"`
		Steps []struct {
			Name   string `json:"name"`
			Anchor struct {
				EnclosingSymbolPath string `json:"enclosingSymbolPath"`
			} `json:"anchor"`
			Layer           string   `json:"layer"`
			LayerConfidence *float64 `json:"layerConfidence"`
			LayerUncertain  bool     `json:"layerUncertain"`
		} `json:"steps"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("decorated spec invalid JSON: %v", err)
	}
	if len(doc.Steps) != 7 {
		t.Fatalf("steps lost during decoration: %d", len(doc.Steps))
	}
	bySym := map[string]*struct {
		Layer     string
		Conf      *float64
		Uncertain bool
	}{}
	for i := range doc.Steps {
		s := &doc.Steps[i]
		bySym[s.Anchor.EnclosingSymbolPath] = &struct {
			Layer     string
			Conf      *float64
			Uncertain bool
		}{s.Layer, s.LayerConfidence, s.LayerUncertain}
	}
	if u := bySym["Util.doIt"]; u == nil || u.Layer != LayerExternal || u.Conf == nil || *u.Conf != 1.0 {
		t.Errorf("overridden Util.doIt = %+v, want external conf 1.0", u)
	}
	if p := bySym["Panel.show"]; p == nil || p.Layer != LayerUI || p.Conf == nil || *p.Conf < 0.74 {
		t.Errorf("Panel.show = %+v, want ui conf >=0.75", p)
	}
	if u2 := bySym["Util.doIt"]; u2 != nil && u2.Uncertain {
		t.Errorf("manual override must clear uncertainty")
	}
	laneIDs := map[string]bool{}
	for _, l := range doc.Lanes {
		laneIDs[l.ID] = true
	}
	for _, want := range []string{LayerUI, LayerApplication, LayerState, LayerData, LayerExternal} {
		if !laneIDs[want] {
			t.Errorf("lane %q missing from decorated doc", want)
		}
	}
}

func TestApplyLayersBytesStableWhenUnchanged(t *testing.T) {
	in := evalDocA()
	out := applyLayers(in)
	var probe map[string]any
	if err := json.Unmarshal(out, &probe); err != nil {
		t.Fatalf("output invalid JSON: %v", err)
	}
	if !bytes.Contains(out, []byte(`"layerConfidence"`)) {
		t.Errorf("expected layerConfidence decoration")
	}
}
