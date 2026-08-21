package codegraph

// This owned fallback is deliberately structural, not semantic. It reports
// only a literal/const GoRoute declaration and its literal builder class as a
// bounded relationship. Anything dynamic stays outside this bridge.

import (
	"fmt"
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

// DartStructuralDomainSubgraph discovers relationships across the repository
// related to a set of domain seed keywords (e.g. push, token, auth, pay, cart).
// It traverses callers, callees, state emissions, and stream bindings.
func DartStructuralDomainSubgraph(repository string, seeds []string, depth int) ([]Relationship, error) {
	if depth <= 0 {
		depth = 2
	}
	if depth > 4 {
		depth = 4
	}
	revision := gitRevision(repository)
	if revision == "" {
		revision = "local"
	}
	files, err := dartFiles(repository)
	if err != nil {
		return nil, &Failure{"DART_STRUCTURAL_UNAVAILABLE", err.Error()}
	}
	contents := map[string][]byte{}
	for _, path := range files {
		b, _ := os.ReadFile(filepath.Join(repository, filepath.FromSlash(path)))
		if len(b) > 0 {
			contents[path] = b
		}
	}

	normalizedSeeds := make([]string, 0, len(seeds))
	for _, s := range seeds {
		trimmed := strings.ToLower(strings.TrimSpace(s))
		if len(trimmed) >= 2 {
			normalizedSeeds = append(normalizedSeeds, trimmed)
		}
	}
	if len(normalizedSeeds) == 0 {
		return nil, &Failure{"SEEDS_REQUIRED", "at least one non-empty search seed is required"}
	}

	// 1. Find all seed anchors across files
	type symbolLocation struct {
		path      string
		symbol    string
		class     string
		byteStart int
		byteEnd   int
		source    []byte
	}

	matchSeed := func(name string) bool {
		lower := strings.ToLower(name)
		for _, seed := range normalizedSeeds {
			if strings.Contains(lower, seed) {
				return true
			}
		}
		return false
	}

	seedLocations := []symbolLocation{}
	methodDeclRegex := regexp.MustCompile(`(?m)^\s*(?:@\w+\s+)*(?:(?:Future|Stream|void|[A-Za-z_][A-Za-z0-9_<>?]*)\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*\([^)]*\)\s*(?:async\s*)?[{=>]`)
	classDeclRegex := regexp.MustCompile(`(?m)^class\s+([A-Za-z_][A-Za-z0-9_]*)`)

	for path, b := range contents {
		source := string(b)
		// Match classes
		for _, cm := range classDeclRegex.FindAllStringSubmatchIndex(source, -1) {
			className := source[cm[2]:cm[3]]
			if matchSeed(className) {
				seedLocations = append(seedLocations, symbolLocation{
					path:      path,
					symbol:    className,
					class:     className,
					byteStart: cm[0],
					byteEnd:   cm[1],
					source:    b,
				})
			}
		}
		// Match methods/functions
		for _, mm := range methodDeclRegex.FindAllStringSubmatchIndex(source, -1) {
			methodName := source[mm[2]:mm[3]]
			if matchSeed(methodName) {
				// find enclosing class
				prefix := source[:mm[0]]
				classMatches := classDeclRegex.FindAllStringSubmatchIndex(prefix, -1)
				currentClass := "top-level"
				if len(classMatches) > 0 {
					lastClass := classMatches[len(classMatches)-1]
					currentClass = prefix[lastClass[2]:lastClass[3]]
				}
				seedLocations = append(seedLocations, symbolLocation{
					path:      path,
					symbol:    currentClass + "." + methodName,
					class:     currentClass,
					byteStart: mm[0],
					byteEnd:   mm[1],
					source:    b,
				})
			}
		}
	}

	if len(seedLocations) == 0 {
		// Fallback: look for general substring occurrences in identifiers
		identRegex := regexp.MustCompile(`\b([A-Za-z_][A-Za-z0-9_]*)\b`)
		for path, b := range contents {
			source := string(b)
			for _, m := range identRegex.FindAllStringSubmatchIndex(source, -1) {
				ident := source[m[2]:m[3]]
				if matchSeed(ident) {
					seedLocations = append(seedLocations, symbolLocation{
						path:      path,
						symbol:    ident,
						class:     "ident",
						byteStart: m[0],
						byteEnd:   m[1],
						source:    b,
					})
					if len(seedLocations) >= 10 {
						break
					}
				}
			}
		}
	}

	rels := []Relationship{}
	seenRels := map[string]bool{}
	addRel := func(kind string, from, to Anchor) {
		key := fmt.Sprintf("%s:%s:%s->%s:%s", kind, from.Path, from.Symbol, to.Path, to.Symbol)
		if seenRels[key] || (from.Path == to.Path && from.Symbol == to.Symbol) {
			return
		}
		seenRels[key] = true
		rels = append(rels, Relationship{Kind: kind, From: from, To: to})
	}

	// 2. Traversal: expand callers & callees of seed locations
	for _, loc := range seedLocations {
		fromAnchor := makeAnchor(loc.path, loc.symbol, loc.source, loc.byteStart, loc.byteEnd, revision)

		// A. Find Callers (who invokes loc.symbol or loc.class across all files)
		searchNames := []string{loc.symbol}
		if parts := strings.Split(loc.symbol, "."); len(parts) == 2 {
			searchNames = append(searchNames, parts[1])
		}
		if loc.class != "" && loc.class != "top-level" && loc.class != "ident" {
			searchNames = append(searchNames, loc.class)
		}

		for otherPath, otherBytes := range contents {
			otherSource := string(otherBytes)
			for _, searchName := range searchNames {
				callRegex := regexp.MustCompile(`\b` + regexp.QuoteMeta(searchName) + `\b`)
				for _, match := range callRegex.FindAllStringSubmatchIndex(otherSource, -1) {
					if otherPath == loc.path && match[0] >= loc.byteStart && match[1] <= loc.byteEnd {
						continue // skip self definition
					}
					// Find enclosing method/class of caller
					prefix := otherSource[:match[0]]
					callerClass := "top-level"
					if cMatches := classDeclRegex.FindAllStringSubmatchIndex(prefix, -1); len(cMatches) > 0 {
						lastC := cMatches[len(cMatches)-1]
						callerClass = prefix[lastC[2]:lastC[3]]
					}
					callerMethod := "body"
					if mMatches := methodDeclRegex.FindAllStringSubmatchIndex(prefix, -1); len(mMatches) > 0 {
						lastM := mMatches[len(mMatches)-1]
						callerMethod = prefix[lastM[2]:lastM[3]]
					}
					callerSymbol := callerClass + "." + callerMethod
					callerAnchor := makeAnchor(otherPath, callerSymbol, otherBytes, match[0], match[1], revision)
					addRel("call", callerAnchor, fromAnchor)
				}
			}
		}

		// B. Find Callees inside loc.source (what does loc call?)
		sourceStr := string(loc.source)
		sliceStart := loc.byteStart
		sliceEnd := loc.byteEnd + 1500
		if sliceEnd > len(sourceStr) {
			sliceEnd = len(sourceStr)
		}
		bodySnippet := sourceStr[sliceStart:sliceEnd]

		invocRegex := regexp.MustCompile(`(?:([A-Za-z_][A-Za-z0-9_]*)\.)?([A-Za-z_][A-Za-z0-9_]*)\s*\(`)
		for _, invMatch := range invocRegex.FindAllStringSubmatchIndex(bodySnippet, -1) {
			var receiverName, calleeName string
			if invMatch[2] >= 0 && invMatch[3] >= 0 {
				receiverName = bodySnippet[invMatch[2]:invMatch[3]]
			}
			if invMatch[4] >= 0 && invMatch[5] >= 0 {
				calleeName = bodySnippet[invMatch[4]:invMatch[5]]
			}
			if calleeName == "if" || calleeName == "for" || calleeName == "while" || calleeName == "switch" || calleeName == "catch" {
				continue
			}

			// If receiver is a class (e.g. DeviceRepository.saveToken)
			if receiverName != "" && receiverName != "this" && receiverName != "super" {
				for targetPath, targetBytes := range contents {
					targetSource := string(targetBytes)
					if strings.Contains(targetSource, "class "+receiverName) {
						targetAnchor := makeAnchor(targetPath, receiverName, targetBytes, 0, len(targetBytes), revision)
						addRel("call", fromAnchor, targetAnchor)
					}
				}
			}

			// Search for definition of callee across files
			for targetPath, targetBytes := range contents {
				targetSource := string(targetBytes)
				defMatch := regexp.MustCompile(`(?m)^\s*(?:(?:static|final|const|Future|Stream|void|[A-Za-z_][A-Za-z0-9_<>?]*)\s+)*` + regexp.QuoteMeta(calleeName) + `\s*\([^)]*\)\s*[{=>]`).FindStringSubmatchIndex(targetSource)
				if defMatch != nil {
					targetAnchor := makeAnchor(targetPath, calleeName, targetBytes, defMatch[0], defMatch[1], revision)
					addRel("call", fromAnchor, targetAnchor)
					break
				}
			}
		}

		// C. Find Event / State / Stream Bindings
		// Look for `.listen(callback)` or `emit(...)` or `notifyListeners()` or `add(event)`
		streamMatch := regexp.MustCompile(`\.listen\s*\(\s*([A-Za-z_][A-Za-z0-9_]*)`).FindAllStringSubmatchIndex(bodySnippet, -1)
		for _, sm := range streamMatch {
			cbName := bodySnippet[sm[2]:sm[3]]
			for targetPath, targetBytes := range contents {
				targetSource := string(targetBytes)
				if strings.Contains(targetSource, cbName+"(") {
					cbAnchor := makeAnchor(targetPath, cbName, targetBytes, 0, len(targetBytes), revision)
					addRel("stream_listen", fromAnchor, cbAnchor)
				}
			}
		}
	}

	return rels, nil
}
