package slicing_test

import (
	"codeflow/internal/collector/slicing"
	"testing"
)

func TestAssignmentSourceReferences(t *testing.T) {
	for _, name := range []string{"valid", "missing", "later", "other invocation", "not call", "other file", "different hash", "not contained"} {
		t.Run(name, func(t *testing.T) {
			from := 1
			a := slicing.Anchor{RepoRelativePath: "flow.ts", EnclosingSymbolPath: "run", FileHash: "hash", ByteRange: [2]int{12, 18}}
			b := a
			b.ByteRange = [2]int{4, 18}
			steps := []slicing.SliceStep{{Ordinal: 1, Kind: "call", InvocationID: "run", SymbolPath: "run", Anchor: a}, {Ordinal: 2, Kind: "mutation", InvocationID: "run", SymbolPath: "run", Anchor: b, AssignmentSourceOrdinal: &from}}
			switch name {
			case "missing":
				from = 99
			case "later":
				from = 2
			case "other invocation":
				steps[0].InvocationID = "other"
			case "not call":
				steps[0].Kind = "mutation"
			case "other file":
				steps[0].Anchor.RepoRelativePath = "other.ts"
			case "different hash":
				steps[0].Anchor.FileHash = "other"
			case "not contained":
				steps[0].Anchor.ByteRange = [2]int{0, 3}
			}
			err := slicing.ValidateExecutionReferences(steps, nil)
			if (err == nil) != (name == "valid") {
				t.Fatalf("validation=%v", err)
			}
		})
	}
}
