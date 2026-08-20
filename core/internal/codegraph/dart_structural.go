package codegraph

// This owned fallback is deliberately structural, not semantic. It reports
// only a literal/const GoRoute declaration and its literal builder class as a
// bounded relationship. Anything dynamic stays outside this bridge.

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
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
			if pageBuilder := regexp.MustCompile(`pageBuilder\s*:\s*\([^)]*\)\s*=>[^;]{0,800}?child\s*:\s*const\s+([A-Za-z_][A-Za-z0-9_]*)`).FindStringSubmatchIndex(tail); pageBuilder != nil {
				builder = pageBuilder
			}
			if builder == nil {
				return nil, &Failure{"DART_STRUCTURAL_UNKNOWN", "route builder is not a direct class construction"}
			}
			class := tail[builder[2]:builder[3]]
			targetPath, targetBytes := findClass(contents, class)
			if targetPath == "" {
				return nil, &Failure{"DART_STRUCTURAL_UNKNOWN", "route builder has no unique current Dart declaration"}
			}
			rels := []Relationship{{Kind: "call", From: makeAnchor(path, "route:"+route, b, m[0], m[1], revision), To: makeAnchor(targetPath, class, targetBytes, 0, len(targetBytes), revision)}}
			// Add only source files reached by concrete symbols referenced from the
			// selected page. This is a candidate-slice operation, not semantic proof:
			// the Dart adapter must still resolve every element before it can publish
			// an observed fact. Unlike the previous suffix list this works for any
			// provider, event, destination type, resolver, and route constant names.
			for _, support := range semanticSupportFiles(contents, targetPath, targetBytes) {
				rels = append(rels, Relationship{Kind: "call", From: makeAnchor(targetPath, class, targetBytes, 0, len(targetBytes), revision), To: makeAnchor(support.path, support.symbol, support.bytes, 0, len(support.bytes), revision)})
			}
			return rels, nil
		}
	}
	// go_router_builder keeps the public route declaration in a typed
	// annotation and the concrete page in that route class's build method. Admit
	// only the bounded shape where the annotation path and one direct page
	// construction are both present in current source.
	typed := regexp.MustCompile(`@TypedGoRoute<([A-Za-z_][A-Za-z0-9_]*)>\s*\(\s*path\s*:\s*([A-Za-z_][A-Za-z0-9_]*|['"][^'"]+['"])\s*\)`)
	for path, b := range contents {
		source := string(b)
		for _, match := range typed.FindAllStringSubmatchIndex(source, -1) {
			routeClass := source[match[2]:match[3]]
			raw := source[match[4]:match[5]]
			route := strings.Trim(raw, "'\"")
			if value, ok := consts[raw]; ok {
				route = value
			}
			if route != wanted {
				continue
			}
			classStart := strings.Index(source[match[1]:], "class "+routeClass)
			if classStart < 0 {
				return nil, &Failure{"DART_STRUCTURAL_UNKNOWN", "typed route class is not declared beside its annotation"}
			}
			classStart += match[1]
			tail := source[classStart:]
			if len(tail) > 1600 {
				tail = tail[:1600]
			}
			builder := regexp.MustCompile(`Widget\s+build\s*\([^)]*\)\s*=>\s*const\s+([A-Za-z_][A-Za-z0-9_]*)`).FindStringSubmatchIndex(tail)
			if builder == nil {
				return nil, &Failure{"DART_STRUCTURAL_UNKNOWN", "typed route build is not a direct const page construction"}
			}
			pageClass := tail[builder[2]:builder[3]]
			targetPath, targetBytes := findClass(contents, pageClass)
			if targetPath == "" {
				return nil, &Failure{"DART_STRUCTURAL_UNKNOWN", "typed route page has no unique current Dart declaration"}
			}
			return []Relationship{{Kind: "call", From: makeAnchor(path, "route:"+route, b, match[0], match[1], revision), To: makeAnchor(targetPath, pageClass, targetBytes, 0, len(targetBytes), revision)}}, nil
		}
	}
	return nil, &Failure{"DART_STRUCTURAL_UNKNOWN", "no literal or const GoRoute matches " + flowID}
}

func directEventController(all map[string][]byte, page []byte) (string, []byte) {
	dispatch := regexp.MustCompile(`ref\s*\.\s*dispatch\s*\(\s*([A-Za-z_]\w*)\s*,\s*const\s+([A-Za-z_]\w*)\s*\(\s*\)\s*\)`).FindSubmatch(page)
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

type structuralSupport struct {
	path   string
	symbol string
	bytes  []byte
}

// semanticSupportFiles expands the page slice through identifiers that are
// visible in its source. It deliberately stops after the event controller and
// destination/route-constant seams; recursive repository-wide call-graph
// discovery belongs to the resolved Dart adapter, not this lightweight bridge.
func semanticSupportFiles(all map[string][]byte, pagePath string, page []byte) []structuralSupport {
	selected := map[string]structuralSupport{}
	add := func(path, symbol string, bytes []byte) {
		if path == "" || path == pagePath {
			return
		}
		selected[path] = structuralSupport{path: path, symbol: symbol, bytes: bytes}
	}
	if path, bytes := directEventController(all, page); path != "" {
		add(path, "event_controller", bytes)
	}

	// Direct route identifiers such as `go(loginRoute)` are included only when
	// one current file owns the literal declaration.
	for _, match := range regexp.MustCompile(`\.\s*(?:go|push)\s*\(\s*([A-Za-z_][A-Za-z0-9_]*)\s*\)`).FindAllSubmatch(page, -1) {
		name := string(match[1])
		if path, bytes := uniqueRouteConstantFile(all, name); path != "" {
			add(path, "route_constant:"+name, bytes)
		}
	}

	// Destination objects such as `go(const DashboardDestination())` expand to
	// a unique switch/arrow resolver and then to that resolver's literal route
	// constant. Ambiguous definitions add nothing and therefore fail closed.
	for _, match := range regexp.MustCompile(`\.\s*go\s*\(\s*const\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(`).FindAllSubmatch(page, -1) {
		destination := string(match[1])
		resolverPath, resolverBytes, routeName := uniqueDestinationResolver(all, destination)
		if resolverPath == "" {
			continue
		}
		add(resolverPath, "destination_resolver:"+destination, resolverBytes)
		if path, bytes := uniqueRouteConstantFile(all, routeName); path != "" {
			add(path, "route_constant:"+routeName, bytes)
		}
	}

	paths := make([]string, 0, len(selected))
	for path := range selected {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	result := make([]structuralSupport, 0, len(paths))
	for _, path := range paths {
		result = append(result, selected[path])
	}
	return result
}

func uniqueRouteConstantFile(all map[string][]byte, name string) (string, []byte) {
	expression := regexp.MustCompile(`(?m)const\s+(?:String\s+)?` + regexp.QuoteMeta(name) + `\s*=\s*['"]/[^'"]+['"]`)
	var path string
	var bytes []byte
	for candidate, source := range all {
		if !expression.Match(source) {
			continue
		}
		if path != "" {
			return "", nil
		}
		path, bytes = candidate, source
	}
	return path, bytes
}

func uniqueDestinationResolver(all map[string][]byte, destination string) (string, []byte, string) {
	expression := regexp.MustCompile(`\b` + regexp.QuoteMeta(destination) + `\s*\(\s*\)\s*=>\s*([A-Za-z_][A-Za-z0-9_]*)`)
	var path, routeName string
	var bytes []byte
	for candidate, source := range all {
		match := expression.FindSubmatch(source)
		if match == nil {
			continue
		}
		if path != "" {
			return "", nil, ""
		}
		path, bytes, routeName = candidate, source, string(match[1])
	}
	return path, bytes, routeName
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
