package lens

import (
	"codeflow/core/internal/flowir"
	"os"
	"path/filepath"
	"testing"
)

func TestReadVerifiesCurrentBytesAndBoundsWindow(t *testing.T) {
	repo := t.TempDir()
	path := filepath.Join(repo, "lib/a.dart")
	_ = os.MkdirAll(filepath.Dir(path), 0755)
	content := []byte("1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n11\n12\n13\n14\n15\n16\n17\n18\n19\n20\n21\n")
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatal(err)
	}
	basis := flowir.Basis{Repository: repo, Manifest: []flowir.ManifestEntry{{Path: "lib/a.dart", Type: "file", FileHash: flowir.SHA256Bytes(content)}}}
	a := flowir.Anchor{Path: "lib/a.dart", FileHash: flowir.SHA256Bytes(content), LineRange: []int{12, 12}}
	got := Read(basis, a)
	if got.Status != "ready" || got.Start != 8 || got.End != 19 || got.Code == "" || got.EditorURL != "vscode://file"+filepath.ToSlash(path)+":12:1" {
		t.Fatalf("%#v", got)
	}
	if err := os.WriteFile(path, []byte("changed"), 0644); err != nil {
		t.Fatal(err)
	}
	if got := Read(basis, a); got.Status != "stale" || got.Code != "" || got.EditorURL != "" {
		t.Fatalf("%#v", got)
	}
	if got := Read(basis, flowir.Anchor{Path: "../secret", FileHash: a.FileHash}); got.Status != "unavailable" || got.Code != "" {
		t.Fatalf("%#v", got)
	}
}
