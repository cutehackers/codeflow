package semantic

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codeflow/internal/analyzer/protocol"
	"codeflow/internal/collector/evidence"
	"codeflow/internal/collector/slicing"
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
	spanStart, spanEnd := 20, 100
	hSpan := sha256.Sum256([]byte(initialSource)[spanStart:spanEnd])
	spanHashHex := hex.EncodeToString(hSpan[:])

	// Create step with secret text in description/side effect
	step := slicing.SliceStep{
		Ordinal:     1,
		Description: "Call API with apiKey=sk-live-secret-key-12345678901234567890",
		Kind:        "call",
		SymbolPath:  "AuthController.login",
		Anchor: slicing.Anchor{
			RepoRelativePath:        sourceRelPath,
			ByteRange:               [2]int{spanStart, spanEnd},
			FileHash:                hashHex,
			SpanHash:                spanHashHex,
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

func TestVS02A5EvidenceStaysBoundToCapturedSnapshotAfterDiskMutation(t *testing.T) {
	tmpDir := t.TempDir()
	relPath := "lib/feature.dart"
	fullPath := filepath.Join(tmpDir, relPath)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		t.Fatal(err)
	}
	original := "class Feature {\n  void run() {\n    return;\n  }\n}\n"
	if err := os.WriteFile(fullPath, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	snapshot, err := protocol.CaptureSnapshot(tmpDir, 3)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fullPath, []byte("class Feature {\n  void run() {\n    throw changed;\n  }\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	originalSum := sha256.Sum256([]byte(original))
	originalHash := hex.EncodeToString(originalSum[:])
	payload := &slicing.SlicedPayload{Steps: []slicing.SliceStep{{
		Ordinal:     1,
		Description: "must not be used as source evidence",
		Anchor: slicing.Anchor{
			RepoRelativePath: relPath,
			ByteRange:        [2]int{0, len(original)},
			FileHash:         originalHash,
			SpanHash:         originalHash,
		},
	}}}
	target := &ResolvedTarget{FlowID: "flow-feature"}
	records, err := ExtractAndRedactEvidenceFromProtocolSnapshot(target, payload, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Snippet != original {
		t.Fatalf("evidence was not snapshot-bound: %+v", records)
	}
	if strings.Contains(records[0].Snippet, "changed") {
		t.Fatalf("evidence leaked post-capture disk content: %q", records[0].Snippet)
	}
}

func TestVS02A5SemanticEvidenceRequiresMatchingAnchorIdentity(t *testing.T) {
	root := t.TempDir()
	relPath := "lib/feature.dart"
	fullPath := filepath.Join(root, relPath)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		t.Fatal(err)
	}
	content := "class Feature {\n  void run() {}\n}\n"
	if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	snapshot, err := protocol.CaptureSnapshot(root, 1)
	if err != nil {
		t.Fatal(err)
	}
	target := &ResolvedTarget{FlowID: "flow-feature"}
	base := slicing.SliceStep{Ordinal: 1, Anchor: slicing.Anchor{
		RepoRelativePath: relPath,
		ByteRange:        [2]int{0, len(content)},
		FileHash:         "wrong-file-hash",
		SpanHash:         "wrong-span-hash",
	}}
	_, err = ExtractAndRedactEvidenceFromProtocolSnapshot(target, &slicing.SlicedPayload{Steps: []slicing.SliceStep{base}}, snapshot)
	if err == nil {
		t.Fatal("semantic evidence accepted stale anchor hashes")
	}
	var typed *evidence.EvidenceError
	if !errors.As(err, &typed) || typed.Code != "unknown_revision" {
		t.Fatalf("stale anchor returned wrong typed error: %T %v", err, err)
	}

	base.Anchor.FileHash = ""
	_, err = ExtractAndRedactEvidenceFromProtocolSnapshot(target, &slicing.SlicedPayload{Steps: []slicing.SliceStep{base}}, snapshot)
	if err == nil {
		t.Fatal("semantic evidence accepted anchor without identity hashes")
	}
	if !errors.As(err, &typed) || typed.Code != "invalid_anchor" {
		t.Fatalf("missing anchor hash returned wrong typed error: %T %v", err, err)
	}
}
