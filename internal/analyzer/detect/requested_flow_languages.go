package detect

import (
	"sort"
	"strings"
)

// RequestedEntryLanguage selects the adapter for an entry whose source is in
// the captured snapshot. Unknown entries retain the repository fallback.
func RequestedEntryLanguage(entry string, files map[string]string) string {
	if path, _, ok := strings.Cut(entry, "#"); ok {
		if _, captured := files[path]; captured {
			if language := DetectByExtension(path); language != "" && language != "unknown" {
				return language
			}
		}
	}
	language := DetectSnapshot(files).Language
	if language == "" || language == "unknown" {
		return "typescript"
	}
	return language
}

// RequestedFlowCandidateLanguages gathers only the entry language for an
// explicit target, or every supported source language for an unresolved request.
func RequestedFlowCandidateLanguages(entry string, files map[string]string) []string {
	if strings.TrimSpace(entry) != "" {
		return []string{RequestedEntryLanguage(entry, files)}
	}
	present := make(map[string]bool)
	for path := range files {
		switch language := DetectByExtension(path); language {
		case "dart", "go", "typescript":
			present[language] = true
		}
	}
	languages := make([]string, 0, len(present))
	for language := range present {
		languages = append(languages, language)
	}
	sort.Strings(languages)
	if len(languages) == 0 {
		return []string{RequestedEntryLanguage("", files)}
	}
	return languages
}
