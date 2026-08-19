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
	Start  int           `json:"start_line,omitempty"`
	End    int           `json:"end_line,omitempty"`
	// EditorURL is created only after the repository-relative anchor and raw
	// bytes pass the same validation as the displayed source lens.
	EditorURL string `json:"editor_url,omitempty"`
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
	result.Code = strings.Join(lines[start-1:end], "\n")
	result.EditorURL = (&url.URL{Scheme: "vscode", Host: "file", Path: clean + fmt.Sprintf(":%d:1", line)}).String()
	return result
}
