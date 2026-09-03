package semantic

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codeflow/internal/slicing"
)

// TestVS02A5_A8_EvidenceCodeLensAndRedaction tests criteria VS02-A5 and VS02-A8:
// - Step Evidence Anchor and CodeLens provided in task context
// - Secret redaction on evidence payloads
// - Product source remains strictly read-only
func TestVS02A5_A8_EvidenceCodeLensAndRedaction(t *testing.T) {
	tmpDir := t.TempDir()
	sourceRelPath := "lib/auth_controller.dart"
	sourceFile := filepath.Join(tmpDir, sourceRelPath)
	if err := os.MkdirAll(filepath.Dir(sourceFile), 0755); err != nil {
		t.Fatal(err)
	}

	initialSource := `// Auth Controller
class AuthController {
  final apiKey = "sk-live-secret-key-12345678901234567890";
  void login(String user, String pass) {
    print("logging in");
  }
}`
	if err := os.WriteFile(sourceFile, []byte(initialSource), 0644); err != nil {
		t.Fatal(err)
	}

	hBefore := sha256.Sum256([]byte(initialSource))
	hashHex := hex.EncodeToString(hBefore[:])

	// Create step with secret text in description/side effect
	step := slicing.SliceStep{
		Ordinal:     1,
		Description: "Call API with apiKey=sk-live-secret-key-12345678901234567890",
		Kind:        "call",
		SymbolPath:  "AuthController.login",
		Anchor: slicing.Anchor{
			RepoRelativePath:        sourceRelPath,
			ByteRange:               [2]int{20, 100},
			FileHash:                hashHex,
			SpanHash:                hashHex,
			EnclosingSymbolPath:     "AuthController.login",
			CanonicalAstFingerprint: hashHex,
		},
	}

	slicePayload := &slicing.SlicedPayload{
		EntrySymbolPath: "AuthController.login",
		Steps:           []slicing.SliceStep{step},
	}

	target := &ResolvedTarget{
		EntrySymbolPath: "AuthController.login",
		FlowID:          "flow-auth-login",
		Title:           "Login Flow",
	}

	intent, err := NormalizeTaskIntent("login with apiKey=sk-live-secret-key-12345678901234567890", IntentOptions{
		Mode: "feature",
	})
	if err != nil {
		t.Fatal(err)
	}
	if intent.Mode != "feature" {
		t.Errorf("expected mode feature, got %q", intent.Mode)
	}

	// 1. Compile with redaction enforcement
	compiledEvidence, err := ExtractAndRedactEvidence(target, slicePayload, tmpDir)
	if err != nil {
		t.Fatalf("ExtractAndRedactEvidence failed: %v", err)
	}

	if len(compiledEvidence) == 0 {
		t.Fatal("expected at least 1 evidence record")
	}

	ev := compiledEvidence[0]

	// 2. Verify CodeLens is populated
	if ev.CodeLens == nil {
		t.Fatal("expected CodeLens in evidence record")
	}
	if ev.CodeLens.Path != sourceRelPath || ev.CodeLens.StartLine < 1 {
		t.Errorf("invalid CodeLens: %+v", ev.CodeLens)
	}

	// 3. Verify Secret Redaction (VS02-A8)
	if strings.Contains(ev.Snippet, "sk-live-secret-key") {
		t.Errorf("secret was not redacted from snippet: %q", ev.Snippet)
	}
	if ev.RedactionStatus != "passed" {
		t.Errorf("expected redactionStatus 'passed', got %q", ev.RedactionStatus)
	}

	// 4. Verify Product source was NOT modified (VS02-A8: strictly read-only)
	currentData, err := os.ReadFile(sourceFile)
	if err != nil {
		t.Fatal(err)
	}
	hAfter := sha256.Sum256(currentData)
	if hBefore != hAfter {
		t.Fatal("product source file was modified by analysis! Must be read-only")
	}
}
