package flowview

import (
	"os"
	"path/filepath"
	"strings"
)

// signatureResult is the read-time code evidence attached to a map component:
// the enclosing symbol's declaration line(s) and the 1-based line where it
// starts. Extraction is best-effort — any read failure yields an empty
// signature rather than a guess, so stale or missing files simply render
// without code.
type signatureResult struct {
	Signature string
	Line      int
}

const (
	signatureMaxLines    = 5
	signatureMaxRunes    = 200
	signatureMaxFileSize = 512 << 10 // refuse to scan huge files
)

// extractSignature reads the declaration of an enclosing symbol.
//
// lineHint: 1-based line of the symbol's focus range (from CodeLens when the
// spec carries one, else derived from the anchor). byteOffset: optional byte
// offset of the enclosing-symbol range (Anchor.SymbolRange start), which
// wins over lineHint because it survives unrelated edits above the symbol.
// symbolHint: last segment of the enclosing symbol path; used to pick the
// right declaration when several candidates sit above the focus line.
func extractSignature(repoRoot, relPath string, byteOffset int, lineHint int, symbolHint string) signatureResult {
	if relPath == "" || strings.Contains(relPath, "..") {
		return signatureResult{}
	}
	full := filepath.Join(repoRoot, filepath.Clean(relPath))
	info, err := os.Stat(full)
	if err != nil || !info.Mode().IsRegular() || info.Size() > signatureMaxFileSize {
		return signatureResult{}
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return signatureResult{}
	}

	startLine := lineHint
	if byteOffset > 0 && byteOffset < len(data) {
		startLine = 1 + strings.Count(string(data[:byteOffset]), "\n")
	}
	if startLine < 1 {
		startLine = 1
	}

	lines := strings.Split(string(data), "\n")
	if startLine > len(lines) {
		return signatureResult{}
	}

	// Specs often anchor the STEP's statement line — or a symbol range that
	// begins at preceding doc comments — rather than the declaration header.
	// When the resolved line does not name the enclosing symbol, walk up to
	// the closest declaration that does; failing that, fall back to the
	// shallowest-indented nearby declaration (headers outdent statements and
	// inner closures); failing even that, scan the whole file for the symbol
	// header. A missed fixup keeps the original line — never a wrong guess.
	if headerMissingHint(lines, startLine, symbolHint) {
		if resolved := resolveDeclarationLine(lines, startLine, symbolHint); resolved > 0 {
			startLine = resolved
		}
	}

	collected := make([]string, 0, signatureMaxLines)
	for i := startLine - 1; i < len(lines) && len(collected) < signatureMaxLines; i++ {
		l := strings.TrimSpace(lines[i])
		if l == "" {
			continue
		}
		collected = append(collected, l)
		if strings.HasSuffix(l, "{") || strings.HasSuffix(l, ";") {
			break
		}
		// A lone closing paren can terminate a multi-line parameter list.
		if strings.HasSuffix(l, ")") && len(collected) >= 2 {
			break
		}
	}
	sig := strings.Join(collected, " ")
	sig = strings.Join(strings.Fields(sig), " ")
	if idx := strings.Index(sig, "{"); idx >= 0 {
		sig = strings.TrimSpace(sig[:idx+1])
	}
	runes := []rune(sig)
	if len(runes) > signatureMaxRunes {
		sig = string(runes[:signatureMaxRunes]) + "…"
	}
	return signatureResult{Signature: sig, Line: startLine}
}

// looksLikeDeclaration reports whether a trimmed source line opens a symbol
// declaration (class/member/function header). Conservative by design: a
// missed declaration keeps the focus line, a false hit merely shifts the
// excerpt window.
func looksLikeDeclaration(l string) bool {
	if l == "" || strings.HasPrefix(l, "//") || strings.HasPrefix(l, "*") {
		return false
	}
	if !strings.HasSuffix(l, "{") && !strings.HasSuffix(l, "(") {
		return false
	}
	if strings.HasSuffix(l, "(") {
		// Parameters continue on the next line; reject control flow like
		// "switch (x)" by requiring an identifier-ish head.
		head := strings.TrimSuffix(l, "(")
		head = strings.TrimSpace(head)
		return head != "" && !isControlKeyword(head)
	}
	head := strings.TrimSuffix(l, "{")
	for _, kw := range []string{"class ", "mixin ", "extension ", "enum "} {
		if strings.HasPrefix(head, kw) {
			return true
		}
	}
	if isControlKeyword(head) {
		return false
	}
	return strings.Contains(head, "(") && !strings.HasPrefix(head, "}") && !strings.Contains(head, ";")
}

// isControlKeyword reports whether a declaration-looking line actually opens
// a branch/loop body ("if (…) {", "for … {"), which must never be taken for
// a symbol header.
func isControlKeyword(head string) bool {
	first := head
	if i := strings.IndexAny(head, " \t("); i >= 0 {
		first = head[:i]
	}
	switch first {
	case "if", "for", "while", "switch", "catch", "try", "else", "do":
		return true
	}
	return false
}

// headerMissingHint reports whether the line at startLine is not already the
// symbol's declaration header (or no hint is known).
func headerMissingHint(lines []string, startLine int, symbolHint string) bool {
	if symbolHint == "" {
		return false
	}
	if startLine < 1 || startLine > len(lines) {
		return true
	}
	return !strings.Contains(lines[startLine-1], symbolHint)
}

const resolveScanWindow = 40

// resolveDeclarationLine finds the 1-based line of the declaration that
// opens the enclosing symbol. Priority: (1) nearest-above line naming the
// symbol, (2) any line in the file naming it, (3) shallowest-indented
// nearby header — member/class headers outdent statements and inner
// closures. Returns 0 when nothing better than the current line exists.
func resolveDeclarationLine(lines []string, startLine int, symbolHint string) int {
	lo := startLine - resolveScanWindow
	if lo < 1 {
		lo = 1
	}
	bestLine, bestIndent := 0, 1<<30
	for i := startLine - 1; i >= lo; i-- {
		raw := lines[i-1]
		t := strings.TrimSpace(raw)
		if !looksLikeDeclaration(t) {
			continue
		}
		if strings.Contains(t, symbolHint) {
			return i // closest declaration naming the symbol wins outright
		}
		if ind := len(raw) - len(strings.TrimLeft(raw, " \t")); ind < bestIndent {
			bestLine, bestIndent = i, ind
		}
	}
	// Whole-file search: some anchors point far from their header, and a
	// hint match anywhere beats an indentation guess.
	for i := range lines {
		t := strings.TrimSpace(lines[i])
		if strings.Contains(t, symbolHint) && looksLikeDeclaration(t) {
			return i + 1
		}
	}
	return bestLine
}
