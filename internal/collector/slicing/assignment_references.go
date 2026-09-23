package slicing

import "fmt"

// ValidateAssignmentSource checks the source identity of a direct call result
// consumed by an assignment. Execution connectivity is checked by the curator.
func ValidateAssignmentSource(source, target SliceStep) error {
	a, b := source.Anchor, target.Anchor
	if source.Kind != "call" || target.Kind != "mutation" || source.Ordinal < 1 || source.Ordinal >= target.Ordinal || source.InvocationID == "" || source.InvocationID != target.InvocationID || a.FileHash == "" || a.FileHash != b.FileHash || a.RepoRelativePath == "" || a.RepoRelativePath != b.RepoRelativePath || a.EnclosingSymbolPath != b.EnclosingSymbolPath || a.ByteRange[0] <= b.ByteRange[0] || a.ByteRange[1] != b.ByteRange[1] || a.ByteRange[1] <= a.ByteRange[0] {
		return fmt.Errorf("invalid assignment value source for step %d", target.Ordinal)
	}
	return nil
}
