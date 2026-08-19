package codegraph

// This owned fallback is deliberately structural, not semantic. It reports
// only a literal/const GoRoute declaration and its literal builder class as a
// bounded relationship. Anything dynamic stays outside this bridge.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

func DartStructuralRelationships(repository, flowID string) ([]Relationship, error) {
	wanted := strings.TrimPrefix(flowID, "route:")
	revision := gitRevision(repository)
	if revision == "" {
		return nil, &Failure{"DART_STRUCTURAL_UNAVAILABLE", "repository has no current Git revision"}
	}
	files, err := dartFiles(repository)
	if err != nil {
		return nil, &Failure{"DART_STRUCTURAL_UNAVAILABLE", err.Error()}
	}
	consts := map[string]string{}
	contents := map[string][]byte{}
	for _, path := range files {
		b, _ := os.ReadFile(filepath.Join(repository, filepath.FromSlash(path)))
		contents[path] = b
		for _, m := range regexp.MustCompile(`(?m)const\s+(String\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*=\s*['"](/[^'"]+)['"]`).FindAllSubmatch(b, -1) {
			consts[string(m[2])] = string(m[3])
		}
	}
	for path, b := range contents {
		source := string(b)
		for _, m := range regexp.MustCompile(`GoRoute\s*\([[:space:]]*path\s*:\s*([A-Za-z_][A-Za-z0-9_]*|['"][^'"]+['"])`).FindAllStringSubmatchIndex(source, -1) {
			raw := source[m[2]:m[3]]
			route := strings.Trim(raw, "'\"")
			if v, ok := consts[raw]; ok {
				route = v
			}
			if route != wanted {
				continue
			}
			tail := source[m[1]:]
			if len(tail) > 1200 {
				tail = tail[:1200]
			}
			builder := regexp.MustCompile(`(?:=>|return)\s*(?:const\s+)?(\w+)`).FindStringSubmatchIndex(tail)
			if builder == nil {
				return nil, &Failure{"DART_STRUCTURAL_UNKNOWN", "route builder is not a direct class construction"}
			}
			class := tail[builder[2]:builder[3]]
			targetPath, targetBytes := findClass(contents, class)
			if targetPath == "" {
				return nil, &Failure{"DART_STRUCTURAL_UNKNOWN", "route builder has no unique current Dart declaration"}
			}
			rels := []Relationship{{Kind: "call", From: makeAnchor(path, "route:"+route, b, m[0], m[1], revision), To: makeAnchor(targetPath, class, targetBytes, 0, len(targetBytes), revision)}}
			// Include one direct event-controller edge only when the page dispatches
			// a const event and exactly one current controller declares that provider
			// and routes the same event to a direct handler. Semantic interpretation
			// remains in the Dart adapter; this merely supplies the bounded source
			// slice needed to validate every later fact.
			if controllerPath, controllerBytes := directEventController(contents, targetBytes); controllerPath != "" {
				rels = append(rels, Relationship{Kind: "call", From: makeAnchor(targetPath, class, targetBytes, 0, len(targetBytes), revision), To: makeAnchor(controllerPath, "event_controller", controllerBytes, 0, len(controllerBytes), revision)})
			}
			// The RouteDestination seam is an explicit, statically named bridge.
			// Include it only when all three implementation files are present.
			for _, suffix := range []string{"app_router.dart", "route_destination_resolver.dart", "content_routes.dart"} {
				if p, bytes := findSuffix(contents, suffix); p != "" {
					rels = append(rels, Relationship{Kind: "call", From: makeAnchor(targetPath, class, targetBytes, 0, len(targetBytes), revision), To: makeAnchor(p, suffix, bytes, 0, len(bytes), revision)})
				}
			}
			return rels, nil
		}
	}
	return nil, &Failure{"DART_STRUCTURAL_UNKNOWN", "no literal or const GoRoute matches " + flowID}
}

func directEventController(all map[string][]byte, page []byte) (string, []byte) {
	dispatch := regexp.MustCompile(`ref\s*\.\s*dispatch\s*\(\s*(\w+Provider)\s*,\s*const\s+(\w+Event)\s*\(\s*\)\s*\)`).FindSubmatch(page)
	if dispatch == nil {
		return "", nil
	}
	provider, event := string(dispatch[1]), string(dispatch[2])
	var found string
	var selected []byte
	for path, source := range all {
		if !strings.Contains(string(source), "final "+provider) {
			continue
		}
		// The graph boundary adds only the unique provider declaration to the
		// validated slice. The Dart adapter proves the exact case, assignment,
		// listener, and route before anything becomes an observed fact.
		if !regexp.MustCompile(`case\s+final\s+` + regexp.QuoteMeta(event) + `\s+\w+\s*:`).Match(source) {
			continue
		}
		if found != "" {
			return "", nil
		}
		found, selected = path, source
	}
	return found, selected
}
func findSuffix(all map[string][]byte, suffix string) (string, []byte) {
	var found string
	var bytes []byte
	for p, b := range all {
		if strings.HasSuffix(p, suffix) {
			if found != "" {
				return "", nil
			}
			found, bytes = p, b
		}
	}
	return found, bytes
}
func dartFiles(root string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		if d.IsDir() {
			if rel != "." && (strings.HasPrefix(rel, ".") || strings.Contains(rel, ".dart_tool")) {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, ".dart") && !strings.Contains(path, ".g.dart") {
			if strings.Contains(filepath.ToSlash(rel), "/test/") || strings.HasPrefix(filepath.ToSlash(rel), "test/") {
				return nil
			}
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	return out, err
}
func findClass(all map[string][]byte, class string) (string, []byte) {
	needle := []byte("class " + class)
	var found string
	var bytes []byte
	for p, b := range all {
		if strings.Contains(string(b), string(needle)) {
			if found != "" {
				return "", nil
			}
			found, bytes = p, b
		}
	}
	return found, bytes
}
func makeAnchor(path, symbol string, b []byte, start, end int, revision string) Anchor {
	return Anchor{Path: path, Symbol: symbol, ByteStart: start, ByteEnd: end, FileHash: sha256sum(b), Revision: revision}
}
