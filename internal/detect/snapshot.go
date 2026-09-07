package detect

// DetectSnapshot performs language detection over an immutable VS-01 content
// map. It mirrors the marker precedence of DetectAll without touching the
// live worktree.
func DetectSnapshot(files map[string]string) Detection {
	if data, ok := files[pubspecFileName]; ok {
		return Detection{
			Language: "dart", ProjectName: ParsePubspecName([]byte(data)), Confident: true,
			Extensions: []string{".dart"}, SourceDirs: []string{"lib"},
		}
	}
	if data, ok := files[packageJSONFileName]; ok {
		return Detection{
			Language: "typescript", ProjectName: ParsePackageJSONName([]byte(data)), Confident: true,
			Extensions: []string{".ts", ".tsx", ".js", ".jsx"}, SourceDirs: []string{"src", "app", "lib"},
		}
	}
	if _, ok := files[tsconfigFileName]; ok {
		return Detection{
			Language: "typescript", Confident: true,
			Extensions: []string{".ts", ".tsx", ".js", ".jsx"}, SourceDirs: []string{"src", "app", "lib"},
		}
	}
	if _, ok := files[buildGradleKtsFile]; ok {
		return Detection{Language: "kotlin", Confident: true, Extensions: []string{".kt", ".kts", ".java"}, SourceDirs: []string{"src/main/kotlin", "src/main/java", "app/src/main"}}
	}
	if _, ok := files[buildGradleFileName]; ok {
		return Detection{Language: "kotlin", Confident: true, Extensions: []string{".kt", ".kts", ".java"}, SourceDirs: []string{"src/main/kotlin", "src/main/java", "app/src/main"}}
	}
	if _, ok := files[pomXMLFileName]; ok {
		return Detection{Language: "kotlin", Confident: true, Extensions: []string{".kt", ".kts", ".java"}, SourceDirs: []string{"src/main/kotlin", "src/main/java", "app/src/main"}}
	}
	if _, ok := files[packageSwiftFile]; ok {
		return Detection{Language: "swift", Confident: true, Extensions: []string{".swift"}, SourceDirs: []string{"Sources", "src"}}
	}
	if _, ok := files[pyprojectFileName]; ok {
		return Detection{Language: "python", Confident: true, Extensions: []string{".py"}, SourceDirs: []string{"src", "app"}}
	}
	if _, ok := files[requirementsTxtFile]; ok {
		return Detection{Language: "python", Confident: true, Extensions: []string{".py"}, SourceDirs: []string{"src", "app"}}
	}
	if _, ok := files[goModFileName]; ok {
		return Detection{Language: "go", Confident: true, Extensions: []string{".go"}, SourceDirs: []string{"pkg", "internal", "cmd", "."}}
	}
	if _, ok := files[cargoTomlFileName]; ok {
		return Detection{Language: "rust", Confident: true, Extensions: []string{".rs"}, SourceDirs: []string{"src"}}
	}
	return Detection{
		Language: "unknown", Confident: false,
		Extensions: []string{".dart", ".ts", ".tsx", ".js", ".jsx", ".kt", ".swift", ".py", ".go", ".rs"},
		SourceDirs: []string{"lib", "src", "app"},
	}
}
