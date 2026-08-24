// Package lens re-reads a persisted FlowIR anchor without trusting a current
// path until its raw bytes still match the published manifest.
package lens

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"codeflow/core/internal/flowir"
)

type Source struct {
	Anchor flowir.Anchor `json:"anchor"`
	Status string        `json:"status"`
	Code   string        `json:"code,omitempty"`
	Lines  []DisplayLine `json:"lines,omitempty"`
	Start  int           `json:"start_line,omitempty"`
	End    int           `json:"end_line,omitempty"`
	// EditorURL is created only after the repository-relative anchor and raw
	// bytes pass the same validation as the displayed source lens.
	EditorURL string `json:"editor_url,omitempty"`
}

// DisplayLine is presentation-only. Text removes the window's common leading
// indentation so a deeply nested source slice starts at the left edge, while
// Code above retains the exact raw source window for API consumers.
type DisplayLine struct {
	Number   int    `json:"number"`
	Text     string `json:"text"`
	Selected bool   `json:"selected"`
}

func Read(basis flowir.Basis, anchor flowir.Anchor) Source {
	result := Source{Anchor: anchor}
	if anchor.Path == "" || filepath.IsAbs(anchor.Path) || strings.HasPrefix(filepath.ToSlash(anchor.Path), "../") {
		result.Status = "unavailable"
		return result
	}
	var expected string
	for _, entry := range basis.Manifest {
		if entry.Path == anchor.Path && entry.Type == "file" {
			expected = entry.FileHash
			break
		}
	}
	if expected == "" {
		result.Status = "unavailable"
		return result
	}
	full := filepath.Join(basis.Repository, filepath.FromSlash(anchor.Path))
	clean := filepath.Clean(full)
	root := filepath.Clean(basis.Repository) + string(os.PathSeparator)
	if !strings.HasPrefix(clean, root) {
		result.Status = "unavailable"
		return result
	}
	bytes, err := os.ReadFile(clean)
	if err != nil {
		result.Status = "unavailable"
		return result
	}
	if flowir.SHA256Bytes(bytes) != expected || (anchor.FileHash != "" && anchor.FileHash != expected) {
		result.Status = "stale"
		return result
	}
	lines := strings.Split(string(bytes), "\n")
	line := 1
	if len(anchor.LineRange) > 0 && anchor.LineRange[0] > 0 {
		line = anchor.LineRange[0]
	}
	start := line - 4
	if start < 1 {
		start = 1
	}
	end := start + 11
	if end > len(lines) {
		end = len(lines)
	}
	if end-start+1 < 5 && len(lines) >= 5 {
		start = end - 4
		if start < 1 {
			start = 1
		}
	}
	result.Status, result.Start, result.End = "ready", start, end
	window := lines[start-1 : end]
	result.Code = strings.Join(window, "\n")
	display := normalizeIndent(window)
	selectedStart, selectedEnd := line, line
	if len(anchor.LineRange) > 1 && anchor.LineRange[1] >= line {
		selectedEnd = anchor.LineRange[1]
	}
	result.Lines = make([]DisplayLine, len(display))
	for i, text := range display {
		number := start + i
		result.Lines[i] = DisplayLine{Number: number, Text: text, Selected: number >= selectedStart && number <= selectedEnd}
	}
	result.EditorURL = (&url.URL{Scheme: "vscode", Host: "file", Path: clean + fmt.Sprintf(":%d:1", line)}).String()
	return result
}

func normalizeIndent(lines []string) []string {
	indent := -1
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		current := leadingWhitespace(line)
		if indent == -1 || current < indent {
			indent = current
		}
	}
	if indent <= 0 {
		return append([]string(nil), lines...)
	}
	out := make([]string, len(lines))
	for i, line := range lines {
		cut := 0
		for cut < len(line) && cut < indent && (line[cut] == ' ' || line[cut] == '\t') {
			cut++
		}
		out[i] = line[cut:]
	}
	return out
}

func leadingWhitespace(line string) int {
	for i := 0; i < len(line); i++ {
		if line[i] != ' ' && line[i] != '\t' {
			return i
		}
	}
	return len(line)
}
