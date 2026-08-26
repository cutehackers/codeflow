package fusion_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codeflow/internal/fusion"
	"codeflow/internal/slicing"
)

// buildSymbolFixture writes a Dart-like file whose submit() symbol spans
// lines symStart..symEnd (1-indexed) with the focus statement on focusLine.
// It returns byte offsets for the focus statement and the symbol range.
func buildSymbolFixture(t *testing.T, symStart, symEnd, focusLine int) (dir, rel string, focusBytes, symbolBytes [2]int) {
	t.Helper()
	dir = t.TempDir()
	rel = "lib/src/sample.dart"
	if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, rel)), 0o755); err != nil {
		t.Fatal(err)
	}

	var b strings.Builder
	total := symEnd + 1
	for i := 1; i <= total; i++ {
		switch i {
		case symStart:
			b.WriteString("Future<void> submit() async {\n")
		case focusLine:
			b.WriteString("  state = 'submitting';\n")
		case symEnd:
			b.WriteString("}\n")
		default:
			b.WriteString("  // filler line\n")
		}
	}
	content := b.String()
	if err := os.WriteFile(filepath.Join(dir, rel), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	focusText := "  state = 'submitting';"
	focusStart := strings.Index(content, focusText)
	symbolStart := strings.Index(content, "Future<void> submit()")
	// Adapter contract: symbol end is the offset just past the closing brace.
	symbolEnd := symbolStart + strings.Index(content[symbolStart:], "}") + 1
	focusBytes = [2]int{focusStart, focusStart + len(focusText)}
	symbolBytes = [2]int{symbolStart, symbolEnd}
	return dir, rel, focusBytes, symbolBytes
}

func fuseOneStep(t *testing.T, dir, rel string, focusBytes, symbolBytes [2]int, withSymbol bool) *fusion.FlowSpec {
	t.Helper()
	anchor := slicing.Anchor{
		RepoRelativePath:        rel,
		ByteRange:               focusBytes,
		FileHash:                "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		SpanHash:                "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210",
		EnclosingSymbolPath:     "Sample.submit",
		CanonicalAstFingerprint: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}
	if withSymbol {
		sr := symbolBytes
		anchor.SymbolRange = &sr
	}
	sliced := &slicing.SlicedPayload{
		CandidateID:     "cand-codelens-00000001",
		Language:        "dart",
		EntrySymbolPath: rel + "#Sample.submit",
		Steps: []slicing.SliceStep{{
			Ordinal:     1,
			Kind:        "mutation",
			Description: "상태를 갱신한다",
			SymbolPath:  "Sample.submit",
			Anchor:      anchor,
		}},
	}
	spec, err := fusion.Fuse(sliced, fusion.FuseOptions{RepoRoot: dir})
	if err != nil {
		t.Fatalf("Fuse: %v", err)
	}
	if len(spec.Steps) != 1 || spec.Steps[0].CodeLens == nil {
		t.Fatalf("expected one fused step with code lens: %+v", spec.Steps)
	}
	return spec
}

func TestCodeLensSymbolScopedView(t *testing.T) {
	// Symbol spans lines 10..130 (121 lines > cap 120); focus on line 50.
	dir, rel, focusBytes, symbolBytes := buildSymbolFixture(t, 10, 130, 50)
	spec := fuseOneStep(t, dir, rel, focusBytes, symbolBytes, true)

	lens := spec.Steps[0].CodeLens
	if lens.StartLine != 50 || lens.EndLine != 50 {
		t.Errorf("focus = [%d,%d], want [50,50]", lens.StartLine, lens.EndLine)
	}
	viewLen := lens.ViewEndLine - lens.ViewStartLine + 1
	if viewLen > 120 {
		t.Errorf("view length %d exceeds cap 120", viewLen)
	}
	if lens.ViewStartLine != 10 {
		t.Errorf("view start = %d, want 10 (clamped to symbol start)", lens.ViewStartLine)
	}
	if lens.ViewStartLine > lens.StartLine || lens.ViewEndLine < lens.EndLine {
		t.Errorf("view [%d,%d] does not contain focus [%d,%d]",
			lens.ViewStartLine, lens.ViewEndLine, lens.StartLine, lens.EndLine)
	}
}

func TestCodeLensSmallSymbolView(t *testing.T) {
	// Symbol spans lines 10..20 (11 lines, under the cap): view == symbol.
	dir, rel, focusBytes, symbolBytes := buildSymbolFixture(t, 10, 20, 12)
	spec := fuseOneStep(t, dir, rel, focusBytes, symbolBytes, true)

	lens := spec.Steps[0].CodeLens
	if lens.ViewStartLine != 10 || lens.ViewEndLine != 20 {
		t.Errorf("view = [%d,%d], want [10,20]", lens.ViewStartLine, lens.ViewEndLine)
	}
	if lens.StartLine != 12 {
		t.Errorf("focus start = %d, want 12", lens.StartLine)
	}
}

func TestCodeLensCapStaysInsideSymbol(t *testing.T) {
	// Symbol spans lines 100..500 (401 lines), focus near the end at line 450:
	// the capped window must not leak past the closing brace.
	dir, rel, focusBytes, symbolBytes := buildSymbolFixture(t, 100, 500, 450)
	spec := fuseOneStep(t, dir, rel, focusBytes, symbolBytes, true)

	lens := spec.Steps[0].CodeLens
	if lens.ViewEndLine > 500 {
		t.Errorf("view end = %d, must stay inside symbol (≤ 500)", lens.ViewEndLine)
	}
	if lens.ViewStartLine < 100 {
		t.Errorf("view start = %d, must stay inside symbol (≥ 100)", lens.ViewStartLine)
	}
	if lens.ViewStartLine > lens.StartLine || lens.ViewEndLine < lens.EndLine {
		t.Errorf("view [%d,%d] does not contain focus [%d,%d]",
			lens.ViewStartLine, lens.ViewEndLine, lens.StartLine, lens.EndLine)
	}
	if viewLen := lens.ViewEndLine - lens.ViewStartLine + 1; viewLen > 120 {
		t.Errorf("view length %d exceeds cap 120", viewLen)
	}
}

func TestCodeLensFallbackMarginWithoutSymbolRange(t *testing.T) {
	// No symbol range: view falls back to focus ± 12 lines.
	dir, rel, focusBytes, symbolBytes := buildSymbolFixture(t, 10, 130, 50)
	spec := fuseOneStep(t, dir, rel, focusBytes, symbolBytes, false)

	lens := spec.Steps[0].CodeLens
	if lens.ViewStartLine != 38 || lens.ViewEndLine != 62 {
		t.Errorf("fallback view = [%d,%d], want [38,62]", lens.ViewStartLine, lens.ViewEndLine)
	}
}

func TestFusionCarriesKindEdgesTruncated(t *testing.T) {
	ord := 1
	sliced := &slicing.SlicedPayload{
		CandidateID:     "cand-kind-edges-000001",
		Language:        "dart",
		EntrySymbolPath: "lib/src/sample.dart#Sample.submit",
		Steps: []slicing.SliceStep{{
			Ordinal:     1,
			Kind:        "guard",
			Description: "값이 없으면 중단한다",
			SymbolPath:  "Sample.submit",
			Anchor: slicing.Anchor{
				RepoRelativePath:        "lib/src/sample.dart",
				ByteRange:               [2]int{0, 10},
				FileHash:                "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
				SpanHash:                "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210",
				EnclosingSymbolPath:     "Sample.submit",
				CanonicalAstFingerprint: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			},
		}},
		Edges: []slicing.SliceEdge{
			{Kind: "boundary_call", ToSymbolPath: "lib/src/api.dart#Api.run", ResolutionStatus: "resolved", Depth: 1, StepOrdinal: &ord},
			{Kind: "unknown_edge", ToSymbolPath: "lib/src/sample.dart#unresolved_dynamic.call", ResolutionStatus: "unresolved_dynamic", Depth: 1},
		},
		Truncated: true,
	}
	spec, err := fusion.Fuse(sliced, fusion.FuseOptions{})
	if err != nil {
		t.Fatalf("Fuse: %v", err)
	}
	if spec.Steps[0].Kind != "guard" {
		t.Errorf("step kind = %q, want guard", spec.Steps[0].Kind)
	}
	if !spec.Truncated {
		t.Error("spec.truncated lost in fusion")
	}
	if len(spec.Edges) != 2 {
		t.Fatalf("edges = %d, want 2", len(spec.Edges))
	}
	if spec.Edges[0].StepOrdinal == nil || *spec.Edges[0].StepOrdinal != 1 {
		t.Errorf("edge stepOrdinal = %v, want 1", spec.Edges[0].StepOrdinal)
	}
	if spec.Edges[1].StepOrdinal != nil {
		t.Errorf("edge without ordinal must stay nil, got %v", *spec.Edges[1].StepOrdinal)
	}
	// Unresolved edges still surface as unknowns (honesty path preserved).
	if len(spec.Unknowns) == 0 {
		t.Error("unresolved_dynamic edge must produce an unknown")
	}
}
