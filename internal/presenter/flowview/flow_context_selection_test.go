package flowview

import (
	"strings"
	"testing"

	"codeflow/internal/collector/slicing"
	"codeflow/internal/curator/semantic"
)

func TestFlowContextSelectionKinds(t *testing.T) {
	const source = "function checkout() { const label = '한글 😀'; if (ready) { return calculate(label); } }"
	for _, tc := range []struct {
		kind, text string
		exact      bool
	}{
		{"expression", "calculate(label)", true},
		{"control_header", "if (ready)", true},
		{"statement", "return calculate(label);", true},
		{"invented", "calculate(label)", false},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			start := strings.Index(source, tc.text)
			span := [2]int{start, start + len(tc.text)}
			hash := sha256Hex([]byte(source))
			anchor := slicing.Anchor{RepoRelativePath: "entry.ts", ByteRange: span, FileHash: hash, SpanHash: sha256Hex([]byte(tc.text))}
			step := semantic.SemanticStep{StepID: "selected", Anchor: anchor, EvidenceRefs: []string{"evidence"}}
			m := &semantic.SemanticMapIR{GenerationID: "generation", ValidatedAgainstSnapshotID: "snapshot", Steps: []semantic.SemanticStep{step}, Evidence: []semantic.SemanticEvidence{{EvidenceID: "evidence", SnapshotID: "snapshot", Anchor: anchor, ByteRange: span, ValidationStatus: "unknown", SourceValidationStatus: "verified"}}}
			meta := &slicing.FlowContextMetadata{CanonicalPath: "entry.ts", SnapshotID: "snapshot", SourceHash: hash, Statement: slicing.StatementNodeMetadata{NodeKind: tc.kind, ByteRange: span}, StructuralContext: slicing.StructuralContextMetadata{Status: "none"}, Callable: slicing.CallableMetadata{SignatureByteRange: [2]int{0, 20}, ByteRange: [2]int{0, len(source)}}}
			got := DeriveFlowContext(DeriveFlowContextParams{Step: step, SemanticMap: m, Metadata: meta, AdapterHasFlowContext: true, SourceSnapshotID: "snapshot", SnapshotFiles: map[string][]byte{"entry.ts": []byte(source)}})
			if (got.Precision == PrecisionExact) != tc.exact {
				t.Fatalf("precision=%s: %s", got.Precision, got.SourceLimitation)
			}
			if tc.exact {
				if got.Statement.NodeKind != tc.kind || len(got.DisplayedLines) != 1 || got.DisplayedLines[0].Selection == nil || got.DisplayedLines[0].Selection.Text != tc.text {
					t.Fatalf("wrong selection: %+v", got)
				}
				if got.SourceValidationStatus != "verified" {
					t.Fatal("source verification result lost")
				}
				if got.EvidenceStatus != "unknown" {
					t.Fatal("exact location promoted graph evidence")
				}
			}
		})
	}
}

func TestFlowContextShowsSourceBackedLoopHeader(t *testing.T) {
	const source = "void scan() { for (final item in items) { visit(item); } }"
	const header = "for (final item in items)"
	const wholeLoop = "for (final item in items) { visit(item); }"
	span := [2]int{strings.Index(source, header), strings.Index(source, header) + len(header)}
	loopSpan := [2]int{strings.Index(source, wholeLoop), strings.Index(source, wholeLoop) + len(wholeLoop)}
	hash := sha256Hex([]byte(source))
	anchor := slicing.Anchor{RepoRelativePath: "entry.dart", ByteRange: span, FileHash: hash, SpanHash: sha256Hex([]byte(header))}
	step := semantic.SemanticStep{StepID: "loop-header", Anchor: anchor, EvidenceRefs: []string{"loop-evidence"}}
	m := &semantic.SemanticMapIR{GenerationID: "generation", ValidatedAgainstSnapshotID: "snapshot", Steps: []semantic.SemanticStep{step}, Evidence: []semantic.SemanticEvidence{{EvidenceID: "loop-evidence", SnapshotID: "snapshot", Anchor: anchor, ByteRange: span, ValidationStatus: "verified", SourceValidationStatus: "verified"}}}
	meta := &slicing.FlowContextMetadata{CanonicalPath: "entry.dart", SnapshotID: "snapshot", SourceHash: hash, Statement: slicing.StatementNodeMetadata{NodeKind: "statement", ByteRange: span}, StructuralContext: slicing.StructuralContextMetadata{Status: "present", NodeKind: "loop", ByteRange: &loopSpan}, Callable: slicing.CallableMetadata{SignatureByteRange: [2]int{0, len("void scan()")}, ByteRange: [2]int{0, len(source)}}}
	got := DeriveFlowContext(DeriveFlowContextParams{Step: step, SemanticMap: m, Metadata: meta, AdapterHasFlowContext: true, SourceSnapshotID: "snapshot", SnapshotFiles: map[string][]byte{"entry.dart": []byte(source)}})
	if got.Precision != PrecisionExact || got.Statement == nil || got.Statement.ByteRange != span || got.StructuralContext.NodeKind != "loop" {
		t.Fatalf("loop source context lost: %+v", got)
	}
	if len(got.DisplayedLines) != 1 || got.DisplayedLines[0].Selection == nil || got.DisplayedLines[0].Selection.Text != header {
		t.Fatalf("loop header not highlighted: %+v", got.DisplayedLines)
	}
}
