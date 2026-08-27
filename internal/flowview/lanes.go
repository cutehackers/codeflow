package flowview

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"
)

// Architecture map v2 engine: adaptive lanes decided by the target project,
// not by a fixed keyword guess alone (design: R1 resolution).
//
// Evidence priority per symbol (highest wins):
//  1. manual laneOverride        -> confidence 1.00
//  2. observed side effect       -> external       0.90
//  3. strong keyword/path match  -> matched layer  0.80 (state delta 0.85)
//  4. published entry point      -> ui             0.75
//  5. neighbor majority vote on the cross-flow call graph
//     (callee votes weighted 1.0x, caller votes 0.6x; middle nodes never
//     inherit boundary lanes; >=2 distinct voters and >=60% mass required)
//     ->                                              0.65
//  6. structural middle (has callers and callees) -> application 0.55
//  7. nothing matched             -> application    0.50, flagged uncertain
//
// All weights are integer-quantized (percent) so vote tallies are exact and
// the whole pipeline is deterministic: sorted iterations, simultaneous
// updates, canonical tie-breaks.
const (
	confOverride         = 100
	confSideEffect       = 90
	confStateDelta       = 85
	confKeyword          = 80
	confEntrySeed        = 75
	confVotes            = 65
	confStructuralMiddle = 55
	confDefault          = 50

	voteCalleeWeight = 100 // a node trusts what it calls most
	voteCallerWeight = 60  // being called is weaker evidence
	minVoters        = 2   // single-neighbor inheritance is rejected
	minMassSharePct  = 60  // winning layer needs >=60% of vote mass
	// Interior symbols that both call and are called must see a
	// supermajority before votes move them: a mixed orchestrator calling
	// persistence AND state belongs to neither.
	minMassShareMiddlePct = 75
	propagationRounds     = 3
)

// boundaryLayers are the outermost lanes; interior symbols (symbols that both
// call and are called) never inherit them through votes, because a UI node
// calling an orchestrator does not make the orchestrator UI.
var boundaryLayers = map[string]bool{
	LayerUI:       true,
	LayerExternal: true,
}

// Extended project vocabulary beyond InferLayer's classic keywords: real
// projects mark layers with many words; these fire only when InferLayer has
// no opinion (its convention came back generic "application").
var (
	externalSymbolKeywords = []string{"gateway", "apiclient", "httpclient", "retrofit", "dio", "endpoint", "socket"}
	externalPathSegments   = []string{"/network/", "/http/", "/grpc/", "/socket/", "/remote/"}
	dataPathSegments       = []string{"/persist/", "/database/", "/storage/"}
)

// LanePlan is the adaptive lane layout derived from a set of flows.
type LanePlan struct {
	Order  []string          // present lane ids, top-to-bottom
	Labels map[string]string // lane id -> display label
}

// symbolNode is the graph vertex for one enclosing symbol, merged across all
// flows that touch it.
type symbolNode struct {
	symbol        string
	paths         map[string]bool
	conventions   []string // per-step classification conventions (for lane labels)
	hasStateDelta bool
	hasSideEffect bool
	isEntry       bool // ordinal-1 step in any flow
	provenance    string
	layer         string // "" while unassigned
	confidence    int
	uncertain     bool
	locked        bool // assigned by rules 1-5 (votes may not move it)
	inDeg, outDeg int
	callers       map[string]bool
	callees       map[string]bool
	flows         map[string]bool
	stepCount     int
}

func newSymbolNode(symbol string) *symbolNode {
	return &symbolNode{
		symbol:      symbol,
		paths:       map[string]bool{},
		callers:     map[string]bool{},
		callees:     map[string]bool{},
		flows:       map[string]bool{},
		conventions: []string{},
	}
}

// buildGraph parses raw FlowSpec JSON documents and merges their steps and
// resolved edges into one cross-flow symbol graph.
func buildGraph(docs [][]byte, overrides map[string]string) map[string]*symbolNode {
	nodes := map[string]*symbolNode{}
	node := func(sym string) *symbolNode {
		if sym == "" {
			return nil
		}
		n, ok := nodes[sym]
		if !ok {
			n = newSymbolNode(sym)
			nodes[sym] = n
		}
		return n
	}

	for _, doc := range docs {
		var spec struct {
			FlowID string `json:"flowId"`
			Steps  []struct {
				Ordinal    int            `json:"ordinal"`
				Provenance string         `json:"provenance"`
				StateDelta map[string]any `json:"stateDelta,omitempty"`
				SideEffect any            `json:"sideEffect,omitempty"`
				Anchor     struct {
					RepoRelativePath    string `json:"repoRelativePath"`
					EnclosingSymbolPath string `json:"enclosingSymbolPath"`
				} `json:"anchor"`
			} `json:"steps"`
			Edges []struct {
				Kind         string `json:"kind"`
				ToSymbolPath string `json:"toSymbolPath"`
				StepOrdinal  *int   `json:"stepOrdinal,omitempty"`
			} `json:"edges"`
		}
		if err := json.Unmarshal(doc, &spec); err != nil {
			continue
		}

		for _, s := range spec.Steps {
			sym := normSymbol(s.Anchor.EnclosingSymbolPath)
			key := sym
			if s.Anchor.EnclosingSymbolPath == "" {
				sym = ""
				key = "(anon)" + s.Anchor.RepoRelativePath + "#" + strconv.Itoa(s.Ordinal)
			}
			n := node(key)
			n.paths[s.Anchor.RepoRelativePath] = true
			n.flows[spec.FlowID] = true
			n.stepCount++
			if s.Ordinal == 1 {
				n.isEntry = true
			}
			if s.StateDelta != nil {
				n.hasStateDelta = true
			}
			if s.SideEffect != nil {
				n.hasSideEffect = true
			}
			if provRank(s.Provenance) > provRank(n.provenance) {
				n.provenance = s.Provenance
			}
			if _, conv := classifyNode(n, overrides); conv != "" {
				n.conventions = append(n.conventions, conv)
			}
		}

		byOrdinal := map[int]*symbolNode{}
		for _, s := range spec.Steps {
			if s.Anchor.EnclosingSymbolPath != "" {
				byOrdinal[s.Ordinal] = nodes[normSymbol(s.Anchor.EnclosingSymbolPath)]
			}
		}
		for _, e := range spec.Edges {
			if e.Kind == "unknown_edge" || e.ToSymbolPath == "" {
				continue // unresolved hints must not fabricate structure
			}
			from := (*symbolNode)(nil)
			if e.StepOrdinal != nil {
				from = byOrdinal[*e.StepOrdinal]
			}
			to := node(normSymbol(e.ToSymbolPath))
			if from == nil || to == nil || from == to {
				continue
			}
			if !from.callees[to.symbol] {
				from.callees[to.symbol] = true
				from.outDeg++
			}
			if !to.callers[from.symbol] {
				to.callers[from.symbol] = true
				to.inDeg++
			}
		}
	}

	// Manual overrides lock nodes before anything else can vote.
	for sym, lane := range overrides {
		if n, ok := nodes[sym]; ok && validLayerName(lane) {
			n.layer = lane
			n.confidence = confOverride
			n.locked = true
			n.uncertain = false
		}
	}
	return nodes
}

// classifyNode assigns evidence-based layers to one node following the
// documented priority, returning the layer and the convention that matched
// ("" when no rule fired). Rules 1-4 run inline; vote/structural/default
// resolution runs in inferLanes/resolveByVotes.
func classifyNode(n *symbolNode, overrides map[string]string) (string, string) {
	if lane, ok := overrides[n.symbol]; ok && validLayerName(lane) {
		n.confidence = confOverride
		return lane, "manual"
	}
	if n.hasSideEffect {
		n.confidence = confSideEffect
		return LayerExternal, "external"
	}
	path := strings.ToLower(joinPaths(n))
	sym := strings.ToLower(n.symbol)

	// Extended vocabulary first: gateway-style outbound clients.
	if containsAny(sym, externalSymbolKeywords...) || containsAny(path, externalPathSegments...) {
		n.confidence = confKeyword
		return LayerExternal, "api"
	}
	if containsAny(path, dataPathSegments...) {
		n.confidence = confKeyword
		return LayerData, "persist"
	}

	layer, convention := InferLayer(joinPaths(n), n.symbol, n.hasStateDelta, false)
	switch convention {
	case "application":
		// InferLayer had no opinion; fall through to weaker evidence.
	default:
		if n.hasStateDelta && convention == "state" {
			n.confidence = confStateDelta
		} else {
			n.confidence = confKeyword
		}
		return layer, convention
	}

	if n.isEntry {
		n.confidence = confEntrySeed
		return LayerUI, "entry"
	}
	n.confidence = confDefault
	return "", ""
}

// inferLanes runs the full pipeline over the merged graph and derives the
// adaptive LanePlan: which lanes exist and what the project calls them.
func inferLanes(nodes map[string]*symbolNode, overrides map[string]string) LanePlan {
	unresolved := []*symbolNode{}
	keys := make([]string, 0, len(nodes))
	for k := range nodes {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		n := nodes[k]
		if n.layer != "" {
			continue
		}
		layer, _ := classifyNode(n, overrides)
		if layer != "" {
			n.layer = layer
			n.locked = true
		} else {
			unresolved = append(unresolved, n)
		}
	}

	// Majority voting runs BEFORE the structural-middle fallback so symbols
	// like persistence writers (called by an orchestrator, calling storage)
	// can still be pulled into their true lane by their callees.
	resolveByVotes(nodes, unresolved)

	// Remaining structural middles become application: they orchestrate in
	// both directions without any stronger evidence.
	for _, n := range unresolved {
		if n.layer == "" && n.inDeg >= 1 && n.outDeg >= 1 {
			n.layer = LayerApplication
			n.confidence = confStructuralMiddle
			n.locked = true
		}
	}

	// Everything still unlabeled is honestly unknown: application by
	// contract default, flagged uncertain so the UI never shows a confident
	// guess.
	for _, n := range unresolved {
		if n.layer == "" {
			n.layer = LayerApplication
			n.confidence = confDefault
			n.uncertain = true
		}
	}

	return lanePlanFrom(nodes)
}

// resolveByVotes runs simultaneous majority-vote rounds over the call graph.
// Simultaneous updates + fixed round count + integer masses = deterministic.
func resolveByVotes(nodes map[string]*symbolNode, unresolved []*symbolNode) {
	for round := 0; round < propagationRounds; round++ {
		type outcome struct {
			layer  string
			mass   int
			total  int
			voters int
		}
		results := map[string]*outcome{}

		for _, n := range unresolved {
			if n.layer != "" || n.locked {
				continue
			}
			masses := map[string]int{}
			voterSet := map[string]bool{}
			consider := func(other *symbolNode, weight int) {
				if other.layer == "" || other.confidence <= 0 {
					return
				}
				cand := other.layer
				if n.inDeg >= 1 && n.outDeg >= 1 && boundaryLayers[cand] {
					return // middle rule: no boundary inheritance
				}
				masses[cand] += weight * other.confidence
				voterSet[other.symbol] = true
			}
			for _, c := range sortedKeys(n.callees) {
				consider(nodes[c], voteCalleeWeight)
			}
			for _, c := range sortedKeys(n.callers) {
				consider(nodes[c], voteCallerWeight)
			}

			best, bestMass, total := "", 0, 0
			for _, l := range LayerOrder {
				m := masses[l]
				total += m
				if m > bestMass {
					best, bestMass = l, m
				}
			}
			if best == "" || total == 0 || len(voterSet) == 0 {
				continue
			}
			// Accept when >=2 distinct neighbors agree, or when the node is
			// a pure wrapper around exactly ONE strong callee (confidence
			// >= keyword level): a lone caller can never drag a symbol into
			// its own lane, and the bypass dies at depth one because vote
			// results carry only confVotes (< keyword level).
			soleCallee := len(voterSet) == 1 && len(n.callees) == 1
			uncontested := soleCallee && bestMass == total && bestMass >= confKeyword*voteCalleeWeight
			if len(voterSet) < minVoters && !uncontested {
				continue
			}
			shareNeeded := minMassSharePct
			if n.inDeg >= 1 && n.outDeg >= 1 {
				shareNeeded = minMassShareMiddlePct
			}
			if bestMass*100 >= total*shareNeeded {
				results[n.symbol] = &outcome{layer: best, mass: bestMass, total: total, voters: len(voterSet)}
			}
		}

		if len(results) == 0 {
			return
		}
		for sym, r := range results {
			n := nodes[sym]
			n.layer = r.layer
			n.confidence = confVotes
			n.locked = true
		}
	}
}

// lanePlanFrom derives present lanes in canonical order with labels named by
// the project's dominant conventions.
func lanePlanFrom(nodes map[string]*symbolNode) LanePlan {
	present := map[string]bool{}
	conventions := map[string][]string{}
	for _, n := range nodes {
		if n.layer == "" {
			continue
		}
		present[n.layer] = true
		conventions[n.layer] = append(conventions[n.layer], n.conventions...)
	}
	labels := map[string]string{}
	order := []string{}
	for _, l := range LayerOrder {
		if !present[l] {
			continue
		}
		order = append(order, l)
		labels[l] = laneLabel(l, conventions[l])
	}
	return LanePlan{Order: order, Labels: labels}
}

// applyLayersWith decorates one FlowSpec JSON document using the shared
// engine: per-step layer, confidence and uncertainty, plus the adaptive
// lanes array. extraDocs contribute graph evidence without being decorated.
// The stored spec on disk is never modified.
func applyLayersWith(specJSON []byte, overrides map[string]string, extraDocs ...[]byte) []byte {
	var doc map[string]any
	if err := json.Unmarshal(specJSON, &doc); err != nil {
		return specJSON
	}
	if _, ok := doc["steps"].([]any); !ok {
		return specJSON
	}

	docs := append([][]byte{specJSON}, extraDocs...)
	graph := buildGraph(docs, overrides)
	inferLanes(graph, overrides)

	plan := lanePlanFrom(graph)
	lanes := make([]any, 0, len(plan.Order))
	for _, id := range plan.Order {
		lanes = append(lanes, map[string]any{"id": id, "label": plan.Labels[id]})
	}

	out, err := json.Marshal(decorateFromGraph(specJSON, graph, lanes))
	if err != nil {
		return specJSON
	}
	return out
}

// decorateFromGraph stamps one raw document with shared engine results:
// per-step layer/confidence/uncertainty plus the precomputed lanes array.
// docBytes is re-parsed so callers keep their original bytes untouched.
func decorateFromGraph(docBytes []byte, graph map[string]*symbolNode, lanes []any) map[string]any {
	var doc map[string]any
	if err := json.Unmarshal(docBytes, &doc); err != nil {
		return doc
	}
	rawSteps, ok := doc["steps"].([]any)
	if !ok {
		doc["lanes"] = lanes
		return doc
	}

	type laneAcc struct{ conventions []string }
	acc := map[string]*laneAcc{}
	for _, rawStep := range rawSteps {
		step, ok := rawStep.(map[string]any)
		if !ok {
			continue
		}
		sym := ""
		if anchor, ok := step["anchor"].(map[string]any); ok {
			sym, _ = anchor["enclosingSymbolPath"].(string)
		}
		key := normSymbol(sym)
		if sym == "" {
			path, _ := anchorPath(step)
			ord := 0
			if v, ok := step["ordinal"].(float64); ok {
				ord = int(v)
			}
			key = "(anon)" + path + "#" + strconv.Itoa(ord)
		}
		n, ok := graph[key]
		if !ok || n.layer == "" {
			continue
		}
		step["layer"] = n.layer
		step["layerConfidence"] = float64(n.confidence) / 100
		if n.uncertain {
			step["layerUncertain"] = true
		}
		if acc[n.layer] == nil {
			acc[n.layer] = &laneAcc{}
		}
		acc[n.layer].conventions = append(acc[n.layer].conventions, n.conventions...)
	}
	doc["lanes"] = lanes
	return doc
}

// applyLayers preserves the original single-doc entry point.
func applyLayers(specJSON []byte) []byte {
	return applyLayersWith(specJSON, nil)
}

// decorateAll classifies the whole generation once and decorates every flow
// document from the shared graph — the per-request cost the server caches.
func decorateAll(docs [][]byte, overrides map[string]string) map[string][]byte {
	graph := buildGraph(docs, overrides)
	inferLanes(graph, overrides)
	plan := lanePlanFrom(graph)

	lanes := []any{}
	for _, id := range plan.Order {
		lanes = append(lanes, map[string]any{"id": id, "label": plan.Labels[id]})
	}

	out := make(map[string][]byte, len(docs))
	for _, doc := range docs {
		decorated, err := json.Marshal(decorateFromGraph(doc, graph, lanes))
		if err != nil {
			continue
		}
		out[docFlowID(doc)] = decorated
	}
	return out
}

func docFlowID(doc []byte) string {
	var probe struct {
		FlowID string `json:"flowId"`
	}
	if err := json.Unmarshal(doc, &probe); err != nil {
		return ""
	}
	return probe.FlowID
}

// --- small deterministic helpers ---

// normSymbol canonicalizes a symbol reference: edge targets may arrive
// file-qualified ("lib/a.dart#Class.method") while step anchors are bare
// ("Class.method"). Everything through the last '#' is stripped so both
// forms resolve to the same graph vertex. Bare inputs pass through.
func normSymbol(s string) string {
	if i := strings.LastIndex(s, "#"); i >= 0 {
		return s[i+1:]
	}
	return s
}

func anchorPath(step map[string]any) (string, bool) {
	if anchor, ok := step["anchor"].(map[string]any); ok {
		p, _ := anchor["repoRelativePath"].(string)
		return p, p != ""
	}
	return "", false
}

func joinPaths(n *symbolNode) string {
	ps := make([]string, 0, len(n.paths))
	for p := range n.paths {
		ps = append(ps, p)
	}
	sort.Strings(ps)
	return strings.Join(ps, "|")
}

func provRank(p string) int {
	switch p {
	case "approved":
		return 3
	case "session":
		return 2
	case "derived":
		return 1
	}
	return 0
}

func validLayerName(l string) bool {
	for _, known := range LayerOrder {
		if l == known {
			return true
		}
	}
	return false
}

func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
