package harvest

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// ManifestFileName is the repo-root override file (design-v2 결정 #14).
// The manifest pins flows, excludes them, or renames them — and always
// wins over automatic scoring (design-v2 §10 매니페스트 오버라이드).
const ManifestFileName = "codeflow.flows.yaml"

// PinnedFlow is one entry under `flows:`.
type PinnedFlow struct {
	Entry string // canonical "<file>.dart#<symbol>" path
	Name  string // optional display name; overrides intentSignals.derivedName
}

// Manifest is the parsed codeflow.flows.yaml. A missing file means "no
// overrides": LoadManifest returns a zero-value manifest and no error.
type Manifest struct {
	Flows    []PinnedFlow
	Excluded []string
}

// LoadManifest reads <repoRoot>/codeflow.flows.yaml. A missing file yields
// an empty manifest with nil error; any other read failure or parse
// failure surfaces as an error carrying the file path (and line number for
// parse failures).
func LoadManifest(repoRoot string) (*Manifest, error) {
	path := filepath.Join(repoRoot, ManifestFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return &Manifest{}, nil
		}
		return nil, fmt.Errorf("load %s: %w", path, err)
	}
	m, err := ParseManifest(string(data))
	if err != nil {
		return nil, fmt.Errorf("load %s: %w", path, err)
	}
	return m, nil
}

// entryShapeRe mirrors identity.schema.json $defs.canonicalEntrySymbolPath:
// '<repoRelativeFile>.dart#<TopLevelSymbol>(.<Member>)*'.
var entryShapeRe = regexp.MustCompile(
	`^[A-Za-z0-9_./-]+\.dart#[A-Za-z_$][A-Za-z0-9_$]*(\.[A-Za-z_$][A-Za-z0-9_$]*)*$`)

// ParseManifest parses the minimal YAML subset of codeflow.flows.yaml.
// The grammar (decision #14 v1 — deliberately tiny, zero dependencies):
//
//	document := (section)+                       sections in any order, repeatable
//	section  := 'flows:' NL | 'excluded:' NL     nothing may follow the colon
//	item     := '-' 'entry:' value ('name:' value)?   under flows:, first key inline
//	           | '-' value                              under excluded:, plain scalar
//	value    := bare | 'single quoted' | "double quoted"
//
// Comments (# preceded by whitespace or at line start), blank lines and
// continuation lines indented deeper than their '- ' item are supported;
// tab indentation is rejected. Entry values must be canonical
// "<file>.dart#<symbol>" paths. Every violation is reported as
// "line N: …" so users can fix the file without a YAML manual.
func ParseManifest(src string) (*Manifest, error) {
	fail := func(line int, format string, args ...any) error {
		return fmt.Errorf("line %d: %s", line, fmt.Sprintf(format, args...))
	}

	m := &Manifest{}
	section := ""       // "", "flows" or "excluded"
	var cur *PinnedFlow // flow item currently being assembled
	curStartLine := 0   // line where the current '- ' item began

	finishItem := func() error {
		if cur == nil {
			return nil
		}
		if cur.Entry == "" {
			return fail(curStartLine, "flow item has no 'entry:' key")
		}
		m.Flows = append(m.Flows, *cur)
		cur = nil
		curStartLine = 0
		return nil
	}

	for i, raw := range strings.Split(src, "\n") {
		lineNo := i + 1
		line := strings.TrimRight(stripComment(raw), " \t\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		if strings.HasPrefix(raw, "\t") || strings.HasPrefix(strings.TrimLeft(line, " "), "\t") {
			return nil, fail(lineNo, "tab indentation is not allowed; use spaces")
		}
		spaces := len(line) - len(strings.TrimLeft(line, " "))
		trimmed := strings.TrimSpace(line)

		switch {
		case spaces == 0 && !strings.HasPrefix(trimmed, "-"):
			if err := finishItem(); err != nil {
				return nil, err
			}
			if !strings.HasSuffix(trimmed, ":") {
				return nil, fail(lineNo, "expected top-level 'flows:' or 'excluded:', got %q", trimmed)
			}
			key := strings.TrimSpace(strings.TrimSuffix(trimmed, ":"))
			switch key {
			case "flows":
				section = "flows"
			case "excluded":
				section = "excluded"
			default:
				return nil, fail(lineNo, "unknown top-level key %q (allowed: flows, excluded)", key)
			}

		case strings.HasPrefix(trimmed, "-"):
			if section == "" {
				return nil, fail(lineNo, "list item outside a 'flows:'/'excluded:' section")
			}
			if spaces == 0 {
				return nil, fail(lineNo, "list items must be indented under '%s:'", section)
			}
			if err := finishItem(); err != nil {
				return nil, err
			}
			body := strings.TrimSpace(strings.TrimPrefix(trimmed, "-"))

			if section == "excluded" {
				entry, err := unquoteScalar(body)
				if err != nil {
					return nil, fail(lineNo, "%v", err)
				}
				if !entryShapeRe.MatchString(entry) {
					return nil, fail(lineNo, "excluded entry %q is not a canonical '<file>.dart#<symbol>' path", entry)
				}
				m.Excluded = append(m.Excluded, entry)
				continue
			}

			key, value, ok := splitKeyValue(body)
			if !ok {
				return nil, fail(lineNo, "flow items must start with '- entry: <file>.dart#<symbol>'")
			}
			item := &PinnedFlow{}
			if err := assignFlowKey(item, key, value, lineNo, fail); err != nil {
				return nil, err
			}
			curStartLine = lineNo
			cur = item

		default:
			if section == "" {
				return nil, fail(lineNo, "unexpected content outside a 'flows:'/'excluded:' section: %q", trimmed)
			}
			if section == "excluded" {
				return nil, fail(lineNo, "excluded entries are single-line scalars; got %q", trimmed)
			}
			if cur == nil {
				return nil, fail(lineNo, "indented line %q does not belong to a '- entry:' item", trimmed)
			}
			key, value, ok := splitKeyValue(trimmed)
			if !ok {
				return nil, fail(lineNo, "expected 'key: value', got %q", trimmed)
			}
			if err := assignFlowKey(cur, key, value, lineNo, fail); err != nil {
				return nil, err
			}
		}
	}
	if err := finishItem(); err != nil {
		return nil, err
	}
	return m, nil
}

func assignFlowKey(item *PinnedFlow, key, value string, lineNo int, fail func(int, string, ...any) error) error {
	v, err := unquoteScalar(value)
	if err != nil {
		return fail(lineNo, "%v", err)
	}
	switch key {
	case "entry":
		if !entryShapeRe.MatchString(v) {
			return fail(lineNo, "entry %q is not a canonical '<file>.dart#<symbol>' path", v)
		}
		item.Entry = v
	case "name":
		item.Name = v
	default:
		return fail(lineNo, "unknown flow key %q (allowed: entry, name)", key)
	}
	return nil
}

// splitKeyValue splits "key: value" on the first colon. ok=false when the
// shape does not match.
func splitKeyValue(s string) (key, value string, ok bool) {
	i := strings.Index(s, ":")
	if i <= 0 {
		return "", "", false
	}
	k := strings.TrimSpace(s[:i])
	if k == "" || strings.ContainsAny(k, " \t'\"") {
		return "", "", false
	}
	return k, strings.TrimSpace(s[i+1:]), true
}

// unquoteScalar trims surrounding single/double quotes. Single quotes use
// YAML doubling (” → '); double quotes accept Go-style escapes via
// strconv.Unquote and fall back to the literal interior when they contain
// non-Go escapes.
func unquoteScalar(s string) (string, error) {
	s = strings.TrimSpace(s)
	if len(s) < 2 {
		return s, nil
	}
	switch q := s[0]; q {
	case '\'', '"':
		if s[len(s)-1] != q {
			return "", fmt.Errorf("unterminated %c quote in %q", q, s)
		}
		inner := s[1 : len(s)-1]
		if q == '\'' {
			return strings.ReplaceAll(inner, "''", "'"), nil
		}
		if unquoted, err := strconv.Unquote(s); err == nil {
			return unquoted, nil
		}
		return inner, nil
	default:
		if strings.ContainsAny(s, "'\"") {
			return "", fmt.Errorf("mixed quoting in %q", s)
		}
		return s, nil
	}
}

// stripComment removes a trailing comment: '#' begins a comment only at
// line start or when preceded by whitespace, and never inside quotes —
// this keeps canonical entry paths (which contain '#') intact.
func stripComment(line string) string {
	var inQ byte
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case inQ == '\'':
			if c == '\'' {
				inQ = 0
			}
		case inQ == '"':
			if c == '\\' {
				i++
			} else if c == '"' {
				inQ = 0
			}
		case c == '\'' || c == '"':
			inQ = c
		case c == '#':
			if i == 0 || line[i-1] == ' ' || line[i-1] == '\t' {
				return line[:i]
			}
		}
	}
	return line
}

// Apply stamps manifestOverride onto candidates (design-v2 §10): entries
// listed under excluded become "excluded"; entries under flows become
// "pinned" with the optional display name overriding
// intentSignals.derivedName. When an entry appears in both lists,
// exclusion wins (deny overrides allow). Unmatched candidates stay "none".
// Apply never adds or removes candidates; see Finalize for the
// pin-forces-inclusion rule.
func (m *Manifest) Apply(cs []Candidate) {
	excl := make(map[string]struct{}, len(m.Excluded))
	for _, e := range m.Excluded {
		excl[e] = struct{}{}
	}
	pins := make(map[string]string, len(m.Flows))
	for _, f := range m.Flows {
		pins[f.Entry] = f.Name
	}
	for i := range cs {
		c := &cs[i]
		if _, ok := excl[c.EntrySymbolPath]; ok {
			c.ManifestOverride = "excluded"
			continue
		}
		if name, ok := pins[c.EntrySymbolPath]; ok {
			c.ManifestOverride = "pinned"
			if name != "" {
				c.IntentSignals.DerivedName = name
			}
		}
	}
}

// IsPinned reports whether the canonical path appears under flows:.
func (m *Manifest) IsPinned(path string) bool {
	for _, f := range m.Flows {
		if f.Entry == path {
			return true
		}
	}
	return false
}
