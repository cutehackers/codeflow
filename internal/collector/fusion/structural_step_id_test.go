package fusion_test

import (
	"testing"

	"codeflow/internal/collector/fusion"
	"codeflow/internal/collector/slicing"
)

func TestComputeDisambiguatedStructuralStepIDSeparatesIdenticalExpressions(t *testing.T) {
	first := slicing.Anchor{
		RepoRelativePath:        "lib/rules.dart",
		EnclosingSymbolPath:     "Rules.apply",
		CanonicalAstFingerprint: "same-expression",
		ByteRange:               [2]int{100, 120},
	}
	second := first
	second.ByteRange = [2]int{200, 220}

	firstID, err := fusion.ComputeDisambiguatedStructuralStepID("flow-123", first, "Rules.apply", "call")
	if err != nil {
		t.Fatal(err)
	}
	secondID, err := fusion.ComputeDisambiguatedStructuralStepID("flow-123", second, "Rules.apply", "call")
	if err != nil {
		t.Fatal(err)
	}
	if firstID == secondID {
		t.Fatalf("identical expressions at different source spans share %q", firstID)
	}
}
