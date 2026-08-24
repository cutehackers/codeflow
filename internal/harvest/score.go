package harvest

import (
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// Scoring constants (design-v2 §10 자동 점수순: 마커 구체성 × 진입점 팬인
// × 경계 도달성; ticket 06 fixed values):
//
//	score = clamp(base*0.85 + min(fanIn/20, 1)*0.10 + boundary*0.05, 0..1)
const (
	scoreBaseWeight      = 0.85 // weight of the marker-specificity base
	fanInSaturatingCount = 20.0 // fanIn at/above which the fan-in term saturates
	fanInMaxContribution = 0.10 // ceiling of the fan-in contribution
	boundaryBonus        = 0.05 // added when boundaryReachable is true
)

// markerSpecificity maps markerKind to its specificity base. The order
// encodes design §4.2/§10: usecase_call (유스케이스 실행) is the strongest
// root evidence, then notifier methods, bloc handlers, route callbacks,
// system events (lifecycle_callback), and finally state-transition points
// (state_mutation) — which per R11 are steps inside a root flow rather
// than roots themselves.
var markerSpecificity = map[string]float64{
	"usecase_call":       0.90,
	"notifier_method":    0.80,
	"bloc_handler":       0.75,
	"route_callback":     0.60,
	"lifecycle_callback": 0.50, // system_event trigger class
	"state_mutation":     0.45, // state_transition trigger class
}

// markerRank is the tie-break ordering key derived from
// markerSpecificity: higher rank = more specific. Unknown kinds sort last.
func markerRank(kind string) int {
	switch kind {
	case "usecase_call":
		return 5
	case "notifier_method":
		return 4
	case "bloc_handler":
		return 3
	case "route_callback":
		return 2
	case "lifecycle_callback":
		return 1
	case "state_mutation":
		return 0
	default:
		return -1
	}
}

// boundarySuffixes mark external-boundary collaborators (design-v2 §4.2
// Stage 2 경계 마커). The adapter profiles ship Repository/ApiClient;
// DataSource and Service round out the CORE-side heuristic set.
var boundarySuffixes = []string{"Repository", "ApiClient", "DataSource", "Service"}

var (
	boundaryWordRe = regexp.MustCompile(`\b\w*(?:Repository|ApiClient|DataSource|Service)\b`)
	importLineRe   = regexp.MustCompile(`(?m)^\s*(?:import|export|part)\s+['"]([^'"]+)['"]`)

	classWordMu sync.Mutex
	classWordRe = map[string]*regexp.Regexp{}
)

// classBoundaryRe returns \b<name>\b for word-boundary occurrence counting.
func classBoundaryRe(name string) *regexp.Regexp {
	classWordMu.Lock()
	defer classWordMu.Unlock()
	if re, ok := classWordRe[name]; ok {
		return re
	}
	re := regexp.MustCompile(`\b` + regexp.QuoteMeta(name) + `\b`)
	classWordRe[name] = re
	return re
}

// sourceFile is one non-generated Dart source under <repoRoot>/<libSubdir>.
type sourceFile struct {
	rel     string // slash-separated path relative to repoRoot
	content string
}

// sourceIndex preloads every non-generated *.dart file once so scoring
// never re-reads the tree. Generated files (*.g.dart, *.freezed.dart) are
// excluded: they are machine-written mirrors, not human fan-in evidence
// (same denylist the adapter uses when walking).
type sourceIndex struct {
	files   []sourceFile
	byRel   map[string]string
	libRoot string // libSubdir prefix in slash form, e.g. "lib/"
}

func loadSourceIndex(repoRoot, libSubdir string) (*sourceIndex, error) {
	root := filepath.Join(repoRoot, libSubdir)
	prefix := filepath.ToSlash(libSubdir)
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	idx := &sourceIndex{byRel: map[string]string{}, libRoot: prefix}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".dart") {
			return nil
		}
		rel, rerr := filepath.Rel(repoRoot, path)
		if rerr != nil {
			return fmt.Errorf("relativize %s: %w", path, rerr)
		}
		rel = filepath.ToSlash(rel)
		if isGeneratedDart(rel) {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}
		idx.files = append(idx.files, sourceFile{rel: rel, content: string(data)})
		idx.byRel[rel] = idx.files[len(idx.files)-1].content
		return nil
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("walk %s: %w", root, err)
	}
	sort.Slice(idx.files, func(i, j int) bool { return idx.files[i].rel < idx.files[j].rel })
	return idx, nil
}

func isGeneratedDart(relSlash string) bool {
	return strings.HasSuffix(relSlash, ".g.dart") || strings.HasSuffix(relSlash, ".freezed.dart")
}

// fanIn counts word-boundary occurrences of className across every indexed
// file (the declaring file's own occurrences included — a raw, fully
// deterministic count).
func (idx *sourceIndex) fanIn(className string) int {
	if className == "" {
		return 0
	}
	re := classBoundaryRe(className)
	n := 0
	for _, f := range idx.files {
		n += len(re.FindAllStringIndex(f.content, -1))
	}
	return n
}

// entryFile extracts the "<file>.dart" part of a canonical entry path.
func entryFile(entrySymbolPath string) string {
	i := strings.IndexByte(entrySymbolPath, '#')
	if i < 0 {
		return entrySymbolPath
	}
	return entrySymbolPath[:i]
}

// boundaryReachable reports whether the entry file touches an external
// boundary: its own text mentions any word ending in a boundary suffix, or
// it imports/exports/parts a file whose basename contains one
// (case-insensitive — Dart basenames are conventionally lower_snake_case).
func (idx *sourceIndex) boundaryReachable(entrySymbolPath string) bool {
	content, ok := idx.byRel[entryFile(entrySymbolPath)]
	if !ok {
		return false
	}
	if boundaryWordRe.MatchString(content) {
		return true
	}
	for _, m := range importLineRe.FindAllStringSubmatch(content, -1) {
		base := strings.ToLower(filepath.Base(filepath.FromSlash(m[1])))
		for _, suf := range boundarySuffixes {
			if strings.Contains(base, strings.ToLower(suf)) {
				return true
			}
		}
	}
	return false
}

// candidateScore computes the deterministic final score:
// clamp(base*scoreBaseWeight + min(fanIn/fanInSaturatingCount, 1)*fanInMaxContribution + boundary*boundaryBonus, 0, 1).
func candidateScore(base float64, fanIn int, boundary bool) float64 {
	s := base*scoreBaseWeight +
		math.Min(float64(fanIn)/fanInSaturatingCount, 1)*fanInMaxContribution
	if boundary {
		s += boundaryBonus
	}
	return math.Min(1, math.Max(0, s))
}

// ScoreAll recomputes fanIn, boundaryReachable and score on every candidate
// in place, overwriting the adapter's placeholder values. It reads each
// source file exactly once via idx.
func ScoreAll(cs []Candidate, idx *sourceIndex) {
	for i := range cs {
		c := &cs[i]
		base := markerSpecificity[c.MarkerKind] // unknown kinds → 0
		c.FanIn = idx.fanIn(c.IntentSignals.ClassName)
		c.BoundaryReachable = idx.boundaryReachable(c.EntrySymbolPath)
		c.Score = candidateScore(base, c.FanIn, c.BoundaryReachable)
	}
}

// prefer reports whether candidate a ranks strictly before b in the R11
// priority order: score desc → marker specificity desc → lexicographic
// entrySymbolPath asc. It is a strict total order over harvested
// candidates (entry paths are unique per symbol).
func prefer(a, b Candidate) bool {
	if a.Score != b.Score {
		return a.Score > b.Score
	}
	ra, rb := markerRank(a.MarkerKind), markerRank(b.MarkerKind)
	if ra != rb {
		return ra > rb
	}
	return a.EntrySymbolPath < b.EntrySymbolPath
}

// DedupAndTieBreak performs root-equivalence dedup (R11): candidates are
// grouped by RootEquivalenceKey, the highest-scored member becomes the
// group representative, and every other member gets dedupedInto set to the
// representative's candidateId (kept in the payload — nothing is silently
// discarded). tieBreakRank 0..n-1 is then assigned inside each group by
// the same order, and the whole payload is returned sorted by the global
// priority order. Ties on score fall to marker specificity first, then
// entrySymbolPath.
func DedupAndTieBreak(cs []Candidate) {
	groups := map[string][]*Candidate{}
	var keys []string
	for i := range cs {
		k := cs[i].RootEquivalenceKey
		if _, seen := groups[k]; !seen {
			keys = append(keys, k)
		}
		groups[k] = append(groups[k], &cs[i])
	}
	sort.Strings(keys)
	for _, k := range keys {
		g := groups[k]
		sort.SliceStable(g, func(i, j int) bool { return prefer(*g[i], *g[j]) })
		repID := g[0].CandidateID
		for rank, member := range g {
			member.TieBreakRank = rank
			if rank > 0 {
				id := repID
				member.DedupedInto = &id
			}
		}
	}
	sort.SliceStable(cs, func(i, j int) bool { return prefer(cs[i], cs[j]) })
}

// Finalize enforces the last manifest rule — pinning forces inclusion even
// when dedup dropped the candidate (매니페스트는 항상 우선) — by clearing
// dedupedInto on pinned members so they stand as roots again, then
// re-sorts the payload into the final priority order.
func Finalize(cs []Candidate) []Candidate {
	for i := range cs {
		c := &cs[i]
		if c.ManifestOverride == "pinned" && c.DedupedInto != nil {
			c.DedupedInto = nil
		}
	}
	sort.SliceStable(cs, func(i, j int) bool { return prefer(cs[i], cs[j]) })
	return cs
}
