package collector_test

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"

	"codeflow/internal/collector"
	"codeflow/internal/collector/fusion"
	"codeflow/internal/collector/slicing"
)

func TestReanchoring_VerifiedExactMatch(t *testing.T) {
	dir := t.TempDir()
	source := "package main\n\nfunc ProcessPayment() {\n\t// do payment\n}\n"
	filePath := filepath.Join(dir, "pay.go")
	if err := os.WriteFile(filePath, []byte(source), 0644); err != nil {
		t.Fatal(err)
	}

	start := 14
	end := 35
	span := source[start:end] // "func ProcessPayment()"
	h := sha256.Sum256([]byte(span))
	spanSHA := hex.EncodeToString(h[:])

	anchor := slicing.Anchor{
		RepoRelativePath:    "pay.go",
		ByteRange:           [2]int{start, end},
		SpanHash:            spanSHA,
		EnclosingSymbolPath: "pay.go#ProcessPayment",
	}

	c := collector.NewCollector(dir)
	res := c.VerifyAndReanchorAnchor(dir, anchor)

	if res.Status != "verified" {
		t.Fatalf("expected verified, got: %s", res.Status)
	}
	if res.IsAdjusted || res.IsDemoted {
		t.Errorf("unexpected adjusted=%v, demoted=%v", res.IsAdjusted, res.IsDemoted)
	}
}

func TestReanchoring_ShiftedOffset_ReanchorsUnder5ms(t *testing.T) {
	dir := t.TempDir()
	// New comments and imports inserted at the top, shifting ProcessPayment down by 120 bytes
	shiftedSource := "package main\n\n// Extra header comment\n// Another comment\nimport \"fmt\"\n\nfunc ProcessPayment() {\n\tfmt.Println(\"paid\")\n}\n"
	filePath := filepath.Join(dir, "pay.go")
	if err := os.WriteFile(filePath, []byte(shiftedSource), 0644); err != nil {
		t.Fatal(err)
	}

	// Old stale anchor pointing to the old byte offset (14..35)
	oldSpan := "func ProcessPayment()"
	h := sha256.Sum256([]byte(oldSpan))
	spanSHA := hex.EncodeToString(h[:])

	staleAnchor := slicing.Anchor{
		RepoRelativePath:    "pay.go",
		ByteRange:           [2]int{14, 35}, // Stale offset
		SpanHash:            spanSHA,
		EnclosingSymbolPath: "pay.go#ProcessPayment",
	}

	c := collector.NewCollector(dir)

	t0 := time.Now()
	res := c.VerifyAndReanchorAnchor(dir, staleAnchor)
	elapsed := time.Since(t0)

	if elapsed > 5*time.Millisecond {
		t.Errorf("re-anchoring exceeded 5ms budget: %v", elapsed)
	}
	if res.Status != "adjusted" || !res.IsAdjusted {
		t.Fatalf("expected adjusted status, got: %s (isAdjusted=%v)", res.Status, res.IsAdjusted)
	}

	// Verify the new byte range actually points to ProcessPayment
	newSpan := shiftedSource[res.Anchor.ByteRange[0]:res.Anchor.ByteRange[1]]
	if newSpan != "func ProcessPayment() {" {
		t.Errorf("unexpected re-anchored content: %q", newSpan)
	}
}

func TestReanchoring_DeletedSymbol_SafeDemotion(t *testing.T) {
	dir := t.TempDir()
	// ProcessPayment was deleted by the developer!
	modifiedSource := "package main\n\nfunc OtherFunction() {}\n"
	filePath := filepath.Join(dir, "pay.go")
	if err := os.WriteFile(filePath, []byte(modifiedSource), 0644); err != nil {
		t.Fatal(err)
	}

	staleAnchor := slicing.Anchor{
		RepoRelativePath:    "pay.go",
		ByteRange:           [2]int{14, 35},
		SpanHash:            "non-matching-hash",
		EnclosingSymbolPath: "pay.go#ProcessPayment",
	}

	c := collector.NewCollector(dir)
	res := c.VerifyAndReanchorAnchor(dir, staleAnchor)

	if res.Status != "unknown_boundary" || !res.IsDemoted {
		t.Fatalf("expected safe demotion to unknown_boundary, got: %s (isDemoted=%v)", res.Status, res.IsDemoted)
	}

	// Test ReanchorFlowStep demotion
	step := fusion.FlowStep{
		Name:       "Process Payment",
		Kind:       "process",
		Confidence: 0.95,
		Freshness:  "fresh",
		Anchor:     staleAnchor,
	}

	demotedStep := c.ReanchorFlowStep(dir, step)
	if demotedStep.Kind != "boundary" {
		t.Errorf("expected demoted step kind to be boundary, got: %s", demotedStep.Kind)
	}
	if demotedStep.Freshness != "orphaned" {
		t.Errorf("expected freshness orphaned, got: %s", demotedStep.Freshness)
	}
	if demotedStep.Confidence != 0.0 {
		t.Errorf("expected confidence 0.0, got: %f", demotedStep.Confidence)
	}
}

func TestReanchoring_GoMethodWithReceiver(t *testing.T) {
	dir := t.TempDir()
	// Comment with symbol name to ensure fallback doesn't match the comment instead of declaration
	source := "package main\n\n// ProcessPayment handles customer checkout\ntype PaymentService struct{}\n\nfunc (s *PaymentService) ProcessPayment() {\n\t// impl\n}\n"
	filePath := filepath.Join(dir, "pay.go")
	if err := os.WriteFile(filePath, []byte(source), 0644); err != nil {
		t.Fatal(err)
	}

	staleAnchor := slicing.Anchor{
		RepoRelativePath:    "pay.go",
		ByteRange:           [2]int{0, 10}, // Stale offset
		SpanHash:            "old-hash",
		EnclosingSymbolPath: "pay.go#ProcessPayment",
	}

	c := collector.NewCollector(dir)
	res := c.VerifyAndReanchorAnchor(dir, staleAnchor)

	if res.Status != "adjusted" || !res.IsAdjusted {
		t.Fatalf("expected adjusted, got status=%s (isAdjusted=%v)", res.Status, res.IsAdjusted)
	}

	newSpan := source[res.Anchor.ByteRange[0]:res.Anchor.ByteRange[1]]
	if newSpan != "func (s *PaymentService) ProcessPayment() {" {
		t.Errorf("expected method declaration with receiver, got: %q", newSpan)
	}
}

func TestReanchoring_FileHashVerified(t *testing.T) {
	dir := t.TempDir()
	source := "package main\n\nfunc Untouched() {}\n"
	filePath := filepath.Join(dir, "untouched.go")
	if err := os.WriteFile(filePath, []byte(source), 0644); err != nil {
		t.Fatal(err)
	}

	fileH := sha256.Sum256([]byte(source))
	fileSHA := hex.EncodeToString(fileH[:])

	anchor := slicing.Anchor{
		RepoRelativePath: "untouched.go",
		FileHash:         fileSHA,
		// SpanHash is empty, but FileHash matches
	}

	c := collector.NewCollector(dir)
	res := c.VerifyAndReanchorAnchor(dir, anchor)

	if res.Status != "verified" {
		t.Fatalf("expected verified based on FileHash match, got: %s", res.Status)
	}
}
