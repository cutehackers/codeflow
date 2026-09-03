package watch

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestVS03A4_WatcherStatBeforeReadStatAfter(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "codeflow-watch-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	filePath := filepath.Join(tempDir, "service.go")
	initialContent := []byte("package service\n\nfunc Run() {}\n")
	if err := os.WriteFile(filePath, initialContent, 0o644); err != nil {
		t.Fatal(err)
	}

	// 1. Successful capture: stat-before matches stat-after
	res := CaptureFileWithStatCheck(filePath, 3)
	if res.Error != nil {
		t.Fatalf("expected successful capture, got error: %v", res.Error)
	}
	if res.Conflict {
		t.Errorf("expected no conflict")
	}
	if res.Deleted {
		t.Errorf("expected not deleted")
	}

	expectedHash := sha256.Sum256(initialContent)
	expectedContentID := hex.EncodeToString(expectedHash[:])
	if res.ContentID != expectedContentID {
		t.Errorf("expected contentId %s, got %s", expectedContentID, res.ContentID)
	}

	// 2. Deleted file capture
	if err := os.Remove(filePath); err != nil {
		t.Fatal(err)
	}
	delRes := CaptureFileWithStatCheck(filePath, 3)
	if !delRes.Deleted {
		t.Errorf("expected delRes.Deleted == true for removed file")
	}
}
