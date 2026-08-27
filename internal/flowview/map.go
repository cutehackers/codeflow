package flowview

import (
	"encoding/json"
	"sort"
	"strings"

	"codeflow/internal/fusion"
)

// ArchitectureMap is the whole-project summary served by GET /api/map: every
// published flow merged into adaptive lanes of components (unique enclosing
// symbols) plus the cross-lane relations between them. Shape follows the
// documented ArchitectureSlice direction (production design §9.8) while all
// fields stay additive to the flowview contract.
type ArchitectureMap struct {
	GenerationID string         `json:"generationId,omitempty"`
	Lanes        []MapLane      `json:"lanes"`
	Components   []MapComponent `json:"components"`
	EntryPoints  []string       `json:"entryPoints"`
	Relations    []MapRelation  `json:"relations"`
}

// MapLane is one adaptive lane: a canonical layer id plus the project's own
// label for it.
type MapLane struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// MapComponent is one architectural building block: a symbol observed in at
// least one published flow.
type MapComponent struct {
	SymbolPath string   `json:"symbolPath"`
	Path       string   `json:"path,omitempty"`
	Layer      string   `json:"layer"`
	Confidence float64  `json:"confidence"`
	Uncertain  bool     `json:"uncertain,omitempty"`
	Signature  string   `json:"signature,omitempty"`
	Line       int      `json:"line,omitempty"`
	Flows      []string `json:"flows"`
	StepCount  int      `json:"stepCount"`
	Importance float64  `json:"importance"`
}

// MapRelation aggregates identical delegation edges across flows.
type MapRelation struct {
	FromSymbolPath string   `json:"fromSymbolPath"`
	ToSymbolPath   string   `json:"toSymbolPath"`
	Kind           string   `json:"kind"`
	Count          int      `json:"count"`
	Flows          []string `json:"flows"`
}

const signatureComponentCap = 200

// maxCoverageFiles bounds how many slice-cache payloads one map render reads.
const maxCoverageFiles = 200

// synthesizeCoverageDocs converts cached SlicedPayload bytes into
// FlowSpec-shaped documents so structural facts from candidates that were
// sliced but never published still feed the project map. Synthetic flows use
// the reserved "coverage:<candidateId>" id; steps carry no provenance so
// they never outrank published evidence. Edge targets are normalized to bare
// symbols to match graph vertices.
func synthesizeCoverageDocs(payloads [][]byte) [][]byte {
	type covAnchor struct {
		RepoRelativePath    string `json:"repoRelativePath,omitempty"`
		EnclosingSymbolPath string `json:"enclosingSymbolPath,omitempty"`
	}
	type covStep struct {
		Ordinal int       `json:"ordinal"`
		Name    string    `json:"name"`
		Anchor  covAnchor `json:"anchor"`
	}
	type covEdge struct {
		Kind             string `json:"kind"`
		ToSymbolPath     string `json:"toSymbolPath"`
		ResolutionStatus string `json:"resolutionStatus"`
		StepOrdinal      *int   `json:"stepOrdinal,omitempty"`
	}
	type covSpec struct {
		FlowID string    `json:"flowId"`
		Title  string    `json:"title"`
		Steps  []covStep `json:"steps"`
		Edges  []covEdge `json:"edges,omitempty"`
	}

	seen := map[string]bool{}
	out := make([][]byte, 0, len(payloads))
	for _, p := range payloads {
		var sp struct {
			CandidateID     string    `json:"candidateId"`
			Steps           []covStep `json:"steps"`
			Edges           []covEdge `json:"edges"`
			EntrySymbolPath string    `json:"entrySymbolPath"`
		}
		if err := json.Unmarshal(p, &sp); err != nil || sp.CandidateID == "" || seen[sp.CandidateID] {
			continue
		}
		seen[sp.CandidateID] = true

		spec := covSpec{
			FlowID: coverageFlowPrefix + sp.CandidateID,
			Title:  "구조 조사 " + sp.CandidateID,
			Steps:  make([]covStep, 0, len(sp.Steps)),
		}
		for _, s := range sp.Steps {
			if s.Anchor.EnclosingSymbolPath == "" {
				continue
			}
			s.Anchor.EnclosingSymbolPath = normSymbol(s.Anchor.EnclosingSymbolPath)
			spec.Steps = append(spec.Steps, s)
		}
		if len(spec.Steps) == 0 {
			continue
		}
		for _, e := range sp.Edges {
			if e.Kind == "" || e.Kind == "unknown_edge" || e.ToSymbolPath == "" {
				continue
			}
			e.ToSymbolPath = normSymbol(e.ToSymbolPath)
			spec.Edges = append(spec.Edges, e)
		}
		b, err := json.Marshal(spec)
		if err != nil {
			continue
		}
		out = append(out, b)
	}
	return out
}

// coverageFlowPrefix marks synthetic map-only flows derived from slice facts.
const coverageFlowPrefix = "coverage:"

// buildArchitectureMap merges raw FlowSpec documents into the project map.
// overrides are manual lane assignments from codeflow.flows.yaml and win
// before any inference runs. Signatures are best-effort read-time excerpts
// bounded to the signatureComponentCap most important components.
func buildArchitectureMap(repoRoot, generationID string, docs [][]byte, overrides map[string]string, entryPoints []string) *ArchitectureMap {
	graph := buildGraph(docs, overrides)
	plan := inferLanes(graph, overrides)

	m := &ArchitectureMap{
		GenerationID: generationID,
		Lanes:        []MapLane{},
		Components:   []MapComponent{},
		EntryPoints:  dedupeSorted(entryPoints),
		Relations:    []MapRelation{},
	}
	for _, id := range plan.Order {
		m.Lanes = append(m.Lanes, MapLane{ID: id, Label: plan.Labels[id]})
	}

	anchors := collectAnchors(docs)

	keys := make([]string, 0, len(graph))
	for k := range graph {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	type scored struct {
		sym string
		imp float64
	}
	ranked := make([]scored, 0, len(keys))
	for _, k := range keys {
		n := graph[k]
		flows := sortedKeys(n.flows)
		importance := float64(n.stepCount) + 3*float64(len(flows))
		if n.provenance == "approved" {
			importance += 2
		}
		c := MapComponent{
			SymbolPath: k,
			Layer:      n.layer,
			Confidence: float64(n.confidence) / 100,
			Uncertain:  n.uncertain,
			Flows:      flows,
			StepCount:  n.stepCount,
			Importance: importance,
		}
		if paths := sortedKeys(n.paths); len(paths) > 0 {
			c.Path = paths[0]
		}
		m.Components = append(m.Components, c)
		ranked = append(ranked, scored{k, importance})
	}

	// Signature extraction performs file reads: bound it to the most
	// important components so a large index cannot turn one request into a
	// whole-repository scan.
	sort.SliceStable(ranked, func(i, j int) bool { return ranked[i].imp > ranked[j].imp })
	if len(ranked) > signatureComponentCap {
		ranked = ranked[:signatureComponentCap]
	}
	for _, sc := range ranked {
		a, ok := anchors[sc.sym]
		if !ok || a.path == "" {
			continue
		}
		sig := extractSignature(repoRoot, a.path, a.byteOffset, a.lineHint, lastSegment(sc.sym))
		for i := range m.Components {
			if m.Components[i].SymbolPath == sc.sym {
				m.Components[i].Signature = sig.Signature
				m.Components[i].Line = sig.Line
				break
			}
		}
	}

	sort.SliceStable(m.Components, func(i, j int) bool {
		return m.Components[i].SymbolPath < m.Components[j].SymbolPath
	})

	m.Relations = aggregateRelations(docs)
	return m
}

type anchorFacts struct {
	path       string
	byteOffset int
	lineHint   int
}

// collectAnchors maps each enclosing symbol to its first-seen anchor facts so
// downstream file access stays deterministic across duplicate steps.
func collectAnchors(docs [][]byte) map[string]anchorFacts {
	out := map[string]anchorFacts{}
	for _, doc := range docs {
		var spec fusion.FlowSpec
		if err := json.Unmarshal(doc, &spec); err != nil {
			continue
		}
		for i := range spec.Steps {
			s := &spec.Steps[i]
			sym := s.Anchor.EnclosingSymbolPath
			if sym == "" {
				continue
			}
			if _, ok := out[sym]; ok {
				continue
			}
			a := anchorFacts{path: s.Anchor.RepoRelativePath}
			if s.Anchor.SymbolRange != nil && s.Anchor.SymbolRange[0] > 0 {
				a.byteOffset = s.Anchor.SymbolRange[0]
			}
			if s.CodeLens != nil && s.CodeLens.StartLine > 0 {
				a.lineHint = s.CodeLens.StartLine
			}
			out[sym] = a
		}
	}
	return out
}

// aggregateRelations merges delegation edges from every flow, keyed by
// (from, to, kind), preserving first-seen order then sorting by weight.
func aggregateRelations(docs [][]byte) []MapRelation {
	type relKey struct{ from, to, kind string }
	counts := map[relKey]*MapRelation{}
	order := []relKey{}
	for _, doc := range docs {
		var spec fusion.FlowSpec
		if err := json.Unmarshal(doc, &spec); err != nil {
			continue
		}
		fromByOrdinal := map[int]string{}
		for _, s := range spec.Steps {
			if s.Anchor.EnclosingSymbolPath != "" {
				fromByOrdinal[s.Ordinal] = normSymbol(s.Anchor.EnclosingSymbolPath)
			}
		}
		for _, e := range spec.Edges {
			if e.Kind == "unknown_edge" || e.ToSymbolPath == "" {
				continue // unresolved hints must not fabricate structure
			}
			from := ""
			if e.StepOrdinal != nil {
				from = fromByOrdinal[*e.StepOrdinal]
			}
			if from == "" {
				continue
			}
			k := relKey{from, normSymbol(e.ToSymbolPath), e.Kind}
			r, ok := counts[k]
			if !ok {
				r = &MapRelation{FromSymbolPath: from, ToSymbolPath: k.to, Kind: e.Kind, Flows: []string{}}
				counts[k] = r
				order = append(order, k)
			}
			r.Count++
			if spec.FlowID != "" && !containsString(r.Flows, spec.FlowID) {
				r.Flows = append(r.Flows, spec.FlowID)
			}
		}
	}
	out := make([]MapRelation, 0, len(order))
	for _, k := range order {
		out = append(out, *counts[k])
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		if out[i].FromSymbolPath != out[j].FromSymbolPath {
			return out[i].FromSymbolPath < out[j].FromSymbolPath
		}
		return out[i].ToSymbolPath < out[j].ToSymbolPath
	})
	return out
}

// lastSegment returns the trailing identifier of a dotted symbol path.
func lastSegment(sym string) string {
	if i := strings.LastIndex(sym, "."); i >= 0 {
		return sym[i+1:]
	}
	return sym
}

func dedupeSorted(in []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, v := range in {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

func containsString(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
