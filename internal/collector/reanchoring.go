package collector

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"codeflow/internal/collector/fusion"
	"codeflow/internal/collector/slicing"
)

// ReanchorResult represents the result of verifying and adjusting an anchor against disk.
type ReanchorResult struct {
	Anchor     slicing.Anchor
	IsAdjusted bool
	IsDemoted  bool
	Status     string // verified | adjusted | unknown_boundary
}

// VerifyAndReanchorAnchor validates an anchor against current disk content.
// If byte offsets match the span hash, it is marked "verified".
// If the content shifted due to uncommitted edits, it scans the file (<5ms)
// to re-anchor to the updated symbol position and marks it "adjusted".
// If the symbol has been completely deleted, it safely demotes to "unknown_boundary"
// without panic or crash.
func (c *Collector) VerifyAndReanchorAnchor(repoRoot string, anchor slicing.Anchor) ReanchorResult {
	if repoRoot == "" {
		repoRoot = c.repoRoot
	}
	absPath := filepath.Join(repoRoot, anchor.RepoRelativePath)
	content, err := os.ReadFile(absPath)
	if err != nil {
		return ReanchorResult{
			Anchor:    anchor,
			IsDemoted: true,
			Status:    "unknown_boundary",
		}
	}

	fileH := sha256.Sum256(content)
	fileSHA := hex.EncodeToString(fileH[:])
	if anchor.FileHash != "" && fileSHA == anchor.FileHash {
		return ReanchorResult{
			Anchor: anchor,
			Status: "verified",
		}
	}

	start := anchor.ByteRange[0]
	end := anchor.ByteRange[1]

	// Check if current byte range matches span hash
	if start >= 0 && end <= len(content) && start <= end {
		span := content[start:end]
		h := sha256.Sum256(span)
		spanSHA := hex.EncodeToString(h[:])
		if anchor.SpanHash != "" && spanSHA == anchor.SpanHash {
			return ReanchorResult{
				Anchor: anchor,
				Status: "verified",
			}
		}
	}

	// Stale offset detected -> Attempt regex re-anchoring (<5ms)
	symName := anchor.EnclosingSymbolPath
	if idx := strings.LastIndex(symName, "#"); idx != -1 {
		symName = symName[idx+1:]
	}
	if idx := strings.LastIndex(symName, "."); idx != -1 {
		symName = symName[idx+1:]
	}
	symName = strings.TrimSpace(symName)

	if symName != "" {
		// Try Go function / method declaration with optional receiver: func (s *Service) Method(
		goPattern := `(?m)^[ \t]*func[ \t]+(?:\([^)]+\)[ \t]+)?` + regexp.QuoteMeta(symName) + `\b`
		// Try general / TS / Dart / Python declaration pattern
		genPattern := `(?m)^[ \t]*(?:(?:export[ \t]+)?(?:default[ \t]+)?(?:async[ \t]+)?(?:def|func|function\*?|pub[ \t]+func|pub[ \t]+fn|class|type|interface|struct|enum)[ \t]+)?(?:(?:public|private|protected|static|readonly|get|set|override|final|late)[ \t]+)*(?:[A-Za-z0-9_<>, ?*\[\]]+[ \t]+)?` + regexp.QuoteMeta(symName) + `[ \t]*(?:\(|<|=|{|:)`

		var matchLoc []int
		if goRe, err := regexp.Compile(goPattern); err == nil {
			matchLoc = goRe.FindIndex(content)
		}
		if matchLoc == nil {
			if genRe, err := regexp.Compile(genPattern); err == nil {
				matchLoc = genRe.FindIndex(content)
			}
		}

		if matchLoc != nil {
			newStart := matchLoc[0]
			newEnd := matchLoc[1]
			eol := strings.IndexByte(string(content[newStart:]), '\n')
			if eol != -1 {
				newEnd = newStart + eol
			}
			if newEnd > len(content) {
				newEnd = len(content)
			}

			newSpan := content[newStart:newEnd]
			h := sha256.Sum256(newSpan)
			newSpanSHA := hex.EncodeToString(h[:])

			newAnchor := anchor
			newAnchor.ByteRange = [2]int{newStart, newEnd}
			newAnchor.SpanHash = newSpanSHA
			newAnchor.FileHash = fileSHA

			return ReanchorResult{
				Anchor:     newAnchor,
				IsAdjusted: true,
				Status:     "adjusted",
			}
		}

		// Fallback: search for exact symbol name boundary on non-comment lines
		fallbackPattern := `(?m)^[ \t]*(?![/][/*])[^\n]*\b` + regexp.QuoteMeta(symName) + `\b`
		if fbRe, err := regexp.Compile(fallbackPattern); err == nil {
			if loc := fbRe.FindIndex(content); loc != nil {
				newStart := loc[0]
				newEnd := loc[1]
				eol := strings.IndexByte(string(content[newStart:]), '\n')
				if eol != -1 {
					newEnd = newStart + eol
				}
				if newEnd > len(content) {
					newEnd = len(content)
				}
				newSpan := content[newStart:newEnd]
				h := sha256.Sum256(newSpan)
				newSpanSHA := hex.EncodeToString(h[:])

				newAnchor := anchor
				newAnchor.ByteRange = [2]int{newStart, newEnd}
				newAnchor.SpanHash = newSpanSHA
				newAnchor.FileHash = fileSHA

				return ReanchorResult{
					Anchor:     newAnchor,
					IsAdjusted: true,
					Status:     "adjusted",
				}
			}
		}
	}

	// Symbol completely deleted or unrecognizable: Safe Demotion to unknown_boundary
	return ReanchorResult{
		Anchor:    anchor,
		IsDemoted: true,
		Status:    "unknown_boundary",
	}
}

// ReanchorFlowStep checks and re-anchors a FlowStep against disk.
// If the symbol is deleted, the step is safely demoted to kind "boundary" and freshness "orphaned".
func (c *Collector) ReanchorFlowStep(repoRoot string, step fusion.FlowStep) fusion.FlowStep {
	res := c.VerifyAndReanchorAnchor(repoRoot, step.Anchor)
	step.Anchor = res.Anchor

	if res.IsDemoted {
		step.Kind = "boundary"
		step.Freshness = "orphaned"
		step.Confidence = 0.0
		reason := "심볼 삭제 또는 위치 불일치로 경계 관문으로 안전 강등됨"
		step.SideEffect = &reason
	} else if res.IsAdjusted {
		step.Freshness = "fresh"
	}
	return step
}
