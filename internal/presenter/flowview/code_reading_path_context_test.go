package flowview

import (
	"strings"
	"testing"

	"codeflow/internal/collector/slicing"
	"codeflow/internal/curator/semantic"
)

func TestCodeReadingPathRequiresCompleteExactCodeViewHighlight(t *testing.T) {
	const source = "first();\nsecond();"
	node := semantic.CodeReadingNode{
		StepID: "step-1",
		SourceAnchor: slicing.Anchor{
			RepoRelativePath: "lib/flow.dart",
			ByteRange:        [2]int{0, len(source)},
			SpanHash:         sha256Hex([]byte(source)),
		},
	}
	context := FlowContextProjection{
		StepID: "step-1", GenerationID: "generation-1", SnapshotID: "snapshot-1",
		Precision: PrecisionExact, SourceValidationStatus: "verified",
		CanonicalPath: "lib/flow.dart",
		Statement:     &StatementProjection{ByteRange: node.SourceAnchor.ByteRange, LineRange: [2]int{1, 2}},
		DisplayedLines: []FlowContextLine{
			{LineNumber: 1, Text: "first();", IsHit: true, Selection: &FlowContextSelection{Text: "first();"}},
			{LineNumber: 2, Text: "second();", IsHit: true, Selection: &FlowContextSelection{Text: "second();"}},
		},
	}
	verify := func(projection FlowContextProjection) error {
		return verifyCodeReadingContext(node, "generation-1", "snapshot-1", map[string]FlowContextProjection{"step-1": projection})
	}
	if err := verify(context); err != nil {
		t.Fatalf("complete exact highlight was rejected: %v", err)
	}

	incomplete := context
	incomplete.DisplayedLines = append([]FlowContextLine(nil), context.DisplayedLines...)
	incomplete.DisplayedLines[1].Selection = nil
	if err := verify(incomplete); err == nil || !strings.Contains(err.Error(), "not fully highlighted") {
		t.Fatalf("partial highlight was accepted: %v", err)
	}

	wrongSource := context
	wrongSource.DisplayedLines = append([]FlowContextLine(nil), context.DisplayedLines...)
	wrongSource.DisplayedLines[1].Text = "other();"
	wrongSource.DisplayedLines[1].Selection = &FlowContextSelection{Text: "other();"}
	if err := verify(wrongSource); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("wrong highlighted source was accepted: %v", err)
	}

	withFinalNewline := node
	withFinalNewline.SourceAnchor.ByteRange[1]++
	withFinalNewline.SourceAnchor.SpanHash = sha256Hex([]byte(source + "\n"))
	context.Statement = &StatementProjection{ByteRange: withFinalNewline.SourceAnchor.ByteRange, LineRange: [2]int{1, 2}}
	if err := verifyCodeReadingContext(withFinalNewline, "generation-1", "snapshot-1", map[string]FlowContextProjection{"step-1": context}); err != nil {
		t.Fatalf("exact highlight with a trailing newline was rejected: %v", err)
	}
}
