package flowview

import (
	"os"
	"strings"
	"testing"
)

func TestExtractSignatureFromLineHint(t *testing.T) {
	repoRoot := "../../../testdata/eval_app"
	got := extractSignature(repoRoot, "lib/persist/vault.dart", 0, 3, "Vault")
	if got.Line != 3 {
		t.Errorf("line = %d, want 3", got.Line)
	}
	if got.Signature == "" {
		t.Fatalf("signature empty")
	}
	if !strings.Contains(got.Signature, "class Vault") {
		t.Errorf("signature = %q, want the Vault declaration", got.Signature)
	}
}

func TestExtractSignaturePrefersByteOffset(t *testing.T) {
	repoRoot := "../../../testdata/eval_app"
	data, err := os.ReadFile(repoRoot + "/lib/persist/vault.dart")
	if err != nil {
		t.Fatal(err)
	}
	idx := strings.Index(string(data), "void put(String key)")
	if idx < 0 {
		t.Fatal("probe string missing from fixture")
	}
	got := extractSignature(repoRoot, "lib/persist/vault.dart", idx, 0, "put")
	if got.Signature == "" {
		t.Fatal("byte-offset extraction produced nothing")
	}
	if !strings.Contains(got.Signature, "put") {
		t.Errorf("signature = %q, want the put declaration", got.Signature)
	}
}

func TestExtractSignatureMissingFileIsEmpty(t *testing.T) {
	got := extractSignature(t.TempDir(), "lib/nowhere.dart", 0, 1, "")
	if got.Signature != "" || got.Line != 0 {
		t.Errorf("missing file must yield empty result, got %+v", got)
	}
}

func TestExtractSignatureTraversalRejected(t *testing.T) {
	got := extractSignature(t.TempDir(), "../escape.dart", 0, 1, "")
	if got.Signature != "" {
		t.Errorf("path traversal must be rejected, got %+v", got)
	}
}

func TestExtractSignatureWalksUpToDeclaration(t *testing.T) {
	repoRoot := "../../../testdata/eval_app"
	data, err := os.ReadFile(repoRoot + "/lib/shared/keeper.dart")
	if err != nil {
		t.Fatal(err)
	}
	idx := strings.Index(string(data), "_phase = 'watching'")
	if idx < 0 {
		t.Fatal("probe missing")
	}
	got := extractSignature(repoRoot, "lib/shared/keeper.dart", 0, 8, "watch")
	if !strings.Contains(got.Signature, "void watch") && !strings.Contains(got.Signature, "class Keeper") {
		t.Errorf("signature = %q (%d), want an enclosing declaration", got.Signature, got.Line)
	}
	if got.Line > 6 {
		t.Errorf("line = %d, want at or above the watch declaration", got.Line)
	}
	_ = idx
}

func TestExtractSignatureRealWorldAnchors(t *testing.T) {
	repoRoot := "../../../testdata/example_app"

	got := extractSignature(repoRoot, "lib/features/cart/cart_bloc.dart", 0, 38, "_onItemAdded")
	if !strings.Contains(got.Signature, "Future<void> _onItemAdded(") {
		t.Errorf("multi-line param header: sig=%q line=%d", got.Signature, got.Line)
	}

	got = extractSignature(repoRoot, "lib/main.dart", 0, 1, "firebaseMessagingBackgroundHandler")
	if !strings.Contains(got.Signature, "firebaseMessagingBackgroundHandler(") {
		t.Errorf("far-from-header top-level fn: sig=%q line=%d", got.Signature, got.Line)
	}
}
