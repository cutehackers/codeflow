// Package detect identifies the target project's language from well-known
// project markers. Dart detection is pubspec.yaml presence plus a minimal,
// dependency-free parse of its `name:` line.
package detect

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const pubspecFileName = "pubspec.yaml"

// Detection is the outcome of probing a repository for a known project type.
type Detection struct {
	Language    string // "dart", or "unknown" when nothing matched
	ProjectName string // parsed from the project marker; may be empty
	Confident   bool   // true when the language marker was actually found
}

// Detect probes repoRoot for any supported project type. Currently only Dart;
// unknown languages come back unconfident so callers can proceed with care.
func Detect(repoRoot string) Detection {
	return DetectDart(repoRoot)
}

// DetectDart reports a Dart/Flutter project when pubspec.yaml exists.
// The project name is extracted with a minimal regex over the file; no YAML
// dependency is introduced.
func DetectDart(repoRoot string) Detection {
	path := filepath.Join(repoRoot, pubspecFileName)
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return Detection{Language: "dart", Confident: false}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Detection{Language: "dart", Confident: true}
	}
	return Detection{
		Language:    "dart",
		ProjectName: ParsePubspecName(data),
		Confident:   true,
	}
}

// nameLinePattern matches the top-level `name:` line of a pubspec.yaml
// (column zero, as required by YAML for root-level keys), capturing the
// bare/quoted value while ignoring trailing comments.
var nameLinePattern = regexp.MustCompile(`(?m)^name[ \t]*:[ \t]*['"]?([A-Za-z0-9_][A-Za-z0-9_.\-]*)['"]?[ \t]*(?:#.*)?$`)

// ParsePubspecName extracts the value of the top-level `name:` line from
// pubspec.yaml content, stripping optional quotes and trailing comments.
// It returns "" when no name line is present.
func ParsePubspecName(data []byte) string {
	match := nameLinePattern.FindSubmatch(data)
	if match == nil {
		return ""
	}
	return strings.TrimSpace(string(match[1]))
}
