// Package detect identifies the target project's language from well-known
// project markers (pubspec.yaml, package.json, build.gradle, Package.swift, etc.).
package detect

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	pubspecFileName     = "pubspec.yaml"
	packageJSONFileName = "package.json"
	tsconfigFileName    = "tsconfig.json"
	buildGradleFileName = "build.gradle"
	buildGradleKtsFile  = "build.gradle.kts"
	pomXMLFileName      = "pom.xml"
	packageSwiftFile    = "Package.swift"
	pyprojectFileName   = "pyproject.toml"
	requirementsTxtFile = "requirements.txt"
	goModFileName       = "go.mod"
	cargoTomlFileName   = "Cargo.toml"
)

// Detection is the outcome of probing a repository for a known project type.
type Detection struct {
	Language    string   // "dart", "typescript", "kotlin", "swift", "python", "go", "rust", or "unknown"
	ProjectName string   // parsed from the project marker; may be empty
	Confident   bool     // true when the language marker was actually found
	Extensions  []string // standard file extensions for this language
	SourceDirs  []string // standard source directories for this language
}

// Detect probes repoRoot for any supported project type.
// If multiple markers match, it returns the most specific confident detection.
func Detect(repoRoot string) Detection {
	all := DetectAll(repoRoot)
	if len(all) > 0 {
		return all[0]
	}
	return Detection{
		Language:   "unknown",
		Confident:  false,
		Extensions: []string{".dart", ".ts", ".tsx", ".js", ".jsx", ".kt", ".swift", ".py", ".go", ".rs"},
		SourceDirs: []string{"lib", "src", "app"},
	}
}

// DetectAll probes repoRoot and returns all confident detections.
func DetectAll(repoRoot string) []Detection {
	var results []Detection

	// 1. Dart / Flutter
	if d := DetectDart(repoRoot); d.Confident {
		results = append(results, d)
	}

	// 2. TypeScript / JavaScript
	if d := DetectTypeScript(repoRoot); d.Confident {
		results = append(results, d)
	}

	// 3. Kotlin / Java
	if d := DetectKotlin(repoRoot); d.Confident {
		results = append(results, d)
	}

	// 4. Swift
	if d := DetectSwift(repoRoot); d.Confident {
		results = append(results, d)
	}

	// 5. Python
	if d := DetectPython(repoRoot); d.Confident {
		results = append(results, d)
	}

	// 6. Go
	if d := DetectGo(repoRoot); d.Confident {
		results = append(results, d)
	}

	// 7. Rust
	if d := DetectRust(repoRoot); d.Confident {
		results = append(results, d)
	}

	return results
}

// DetectDart reports a Dart/Flutter project when pubspec.yaml exists.
func DetectDart(repoRoot string) Detection {
	path := filepath.Join(repoRoot, pubspecFileName)
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return Detection{Language: "dart", Confident: false, Extensions: []string{".dart"}, SourceDirs: []string{"lib"}}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Detection{Language: "dart", Confident: true, Extensions: []string{".dart"}, SourceDirs: []string{"lib"}}
	}
	return Detection{
		Language:    "dart",
		ProjectName: ParsePubspecName(data),
		Confident:   true,
		Extensions:  []string{".dart"},
		SourceDirs:  []string{"lib"},
	}
}

// DetectTypeScript reports a TypeScript/JavaScript project when package.json or tsconfig.json exists.
func DetectTypeScript(repoRoot string) Detection {
	pkgPath := filepath.Join(repoRoot, packageJSONFileName)
	tsconfigPath := filepath.Join(repoRoot, tsconfigFileName)

	pkgInfo, pkgErr := os.Stat(pkgPath)
	tsInfo, tsErr := os.Stat(tsconfigPath)

	hasPkg := pkgErr == nil && pkgInfo.Mode().IsRegular()
	hasTs := tsErr == nil && tsInfo.Mode().IsRegular()

	if !hasPkg && !hasTs {
		return Detection{Language: "typescript", Confident: false, Extensions: []string{".ts", ".tsx", ".js", ".jsx"}, SourceDirs: []string{"src", "app", "lib"}}
	}

	projectName := ""
	if hasPkg {
		if data, err := os.ReadFile(pkgPath); err == nil {
			projectName = ParsePackageJSONName(data)
		}
	}
	return Detection{
		Language:    "typescript",
		ProjectName: projectName,
		Confident:   true,
		Extensions:  []string{".ts", ".tsx", ".js", ".jsx"},
		SourceDirs:  []string{"src", "app", "lib"},
	}
}

// DetectKotlin reports a Kotlin/JVM project when Gradle or Maven build files exist.
func DetectKotlin(repoRoot string) Detection {
	markers := []string{buildGradleKtsFile, buildGradleFileName, pomXMLFileName}
	for _, m := range markers {
		path := filepath.Join(repoRoot, m)
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
			return Detection{
				Language:   "kotlin",
				Confident:  true,
				Extensions: []string{".kt", ".kts", ".java"},
				SourceDirs: []string{"src/main/kotlin", "src/main/java", "app/src/main"},
			}
		}
	}
	return Detection{Language: "kotlin", Confident: false, Extensions: []string{".kt", ".java"}, SourceDirs: []string{"src"}}
}

// DetectSwift reports a Swift/iOS project when Package.swift exists.
func DetectSwift(repoRoot string) Detection {
	path := filepath.Join(repoRoot, packageSwiftFile)
	if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
		return Detection{
			Language:   "swift",
			Confident:  true,
			Extensions: []string{".swift"},
			SourceDirs: []string{"Sources", "src"},
		}
	}
	return Detection{Language: "swift", Confident: false, Extensions: []string{".swift"}, SourceDirs: []string{"Sources"}}
}

// DetectPython reports a Python project when pyproject.toml or requirements.txt exists.
func DetectPython(repoRoot string) Detection {
	markers := []string{pyprojectFileName, requirementsTxtFile}
	for _, m := range markers {
		path := filepath.Join(repoRoot, m)
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
			return Detection{
				Language:   "python",
				Confident:  true,
				Extensions: []string{".py"},
				SourceDirs: []string{"src", "app"},
			}
		}
	}
	return Detection{Language: "python", Confident: false, Extensions: []string{".py"}, SourceDirs: []string{"src"}}
}

// DetectGo reports a Go project when go.mod exists.
func DetectGo(repoRoot string) Detection {
	path := filepath.Join(repoRoot, goModFileName)
	if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
		return Detection{
			Language:   "go",
			Confident:  true,
			Extensions: []string{".go"},
			SourceDirs: []string{"pkg", "internal", "cmd", "."},
		}
	}
	return Detection{Language: "go", Confident: false, Extensions: []string{".go"}, SourceDirs: []string{"."}}
}

// DetectRust reports a Rust project when Cargo.toml exists.
func DetectRust(repoRoot string) Detection {
	path := filepath.Join(repoRoot, cargoTomlFileName)
	if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
		return Detection{
			Language:   "rust",
			Confident:  true,
			Extensions: []string{".rs"},
			SourceDirs: []string{"src"},
		}
	}
	return Detection{Language: "rust", Confident: false, Extensions: []string{".rs"}, SourceDirs: []string{"src"}}
}

// DetectByExtension maps a source file path to its canonical language.
func DetectByExtension(filePath string) string {
	ext := strings.ToLower(filepath.Ext(filePath))
	switch ext {
	case ".dart":
		return "dart"
	case ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs":
		return "typescript"
	case ".kt", ".kts", ".java":
		return "kotlin"
	case ".swift":
		return "swift"
	case ".py":
		return "python"
	case ".go":
		return "go"
	case ".rs":
		return "rust"
	default:
		return "unknown"
	}
}

var nameLinePattern = regexp.MustCompile(`(?m)^name[ \t]*:[ \t]*['"]?([A-Za-z0-9_][A-Za-z0-9_.\-]*)['"]?[ \t]*(?:#.*)?$`)

// ParsePubspecName extracts the value of the top-level `name:` line from pubspec.yaml.
func ParsePubspecName(data []byte) string {
	match := nameLinePattern.FindSubmatch(data)
	if match == nil {
		return ""
	}
	return strings.TrimSpace(string(match[1]))
}

// ParsePackageJSONName extracts the "name" field from package.json without heavy JSON parsing.
func ParsePackageJSONName(data []byte) string {
	var pkg struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(data, &pkg); err == nil {
		return pkg.Name
	}
	return ""
}
