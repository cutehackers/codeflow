package watch

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

func TestMtimeDelta(t *testing.T) {
	t0 := time.Now()
	t1 := t0.Add(1 * time.Second)
	t2 := t0.Add(2 * time.Second)

	curr := map[string]time.Time{
		"a.dart": t0,
		"b.dart": t2, // modified from t1
		"c.dart": t0, // newly added
	}
	last := map[string]time.Time{
		"a.dart": t0,
		"b.dart": t1,
		"d.dart": t0, // deleted
	}

	delta := mtimeDelta(curr, last)
	sort.Strings(delta)

	want := []string{"b.dart", "c.dart", "d.dart"}
	if len(delta) != len(want) {
		t.Fatalf("delta len = %d, want %d: %v", len(delta), len(want), delta)
	}
	for i := range want {
		if delta[i] != want[i] {
			t.Errorf("delta[%d] = %q, want %q", i, delta[i], want[i])
		}
	}
}

func TestCollectSourceFilesWithMtime(t *testing.T) {
	dir := t.TempDir()

	write := func(rel string) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		_ = os.WriteFile(p, []byte("code"), 0o644)
	}

	write("lib/auth.dart")
	write("src/index.ts")
	write("src/App.tsx")
	write("node_modules/pkg/index.js")
	write(".git/HEAD")
	write("build/output.js")
	write(".codeflow/workspace.json")

	files, mtimes, err := collectSourceFilesWithMtime(dir, nil, nil)
	if err != nil {
		t.Fatalf("collectSourceFilesWithMtime failed: %v", err)
	}

	// Should contain lib/auth.dart, src/index.ts, src/App.tsx, and exclude node_modules, .git, build, .codeflow
	foundMap := make(map[string]bool)
	for _, f := range files {
		foundMap[f] = true
	}

	if !foundMap["lib/auth.dart"] {
		t.Errorf("expected lib/auth.dart in files: %v", files)
	}
	if !foundMap["src/index.ts"] {
		t.Errorf("expected src/index.ts in files: %v", files)
	}
	if !foundMap["src/App.tsx"] {
		t.Errorf("expected src/App.tsx in files: %v", files)
	}
	if foundMap["node_modules/pkg/index.js"] {
		t.Errorf("node_modules should be ignored: %v", files)
	}
	if foundMap[".git/HEAD"] {
		t.Errorf(".git should be ignored: %v", files)
	}
	if foundMap["build/output.js"] {
		t.Errorf("build should be ignored: %v", files)
	}
	if len(mtimes) != len(files) {
		t.Errorf("mtimes len %d != files len %d", len(mtimes), len(files))
	}
}

func TestCollectDartFiles(t *testing.T) {
	dir := t.TempDir()

	write := func(rel string) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		_ = os.WriteFile(p, []byte("code"), 0o644)
	}

	write("lib/auth.dart")
	write("src/index.ts")

	dartFiles, err := collectDartFiles(dir)
	if err != nil {
		t.Fatalf("collectDartFiles failed: %v", err)
	}
	if len(dartFiles) != 1 || dartFiles[0] != "lib/auth.dart" {
		t.Fatalf("collectDartFiles = %v, want [lib/auth.dart]", dartFiles)
	}

	dartFilesWithMtimes, mtimes, err := collectDartFilesWithMtime(dir)
	if err != nil {
		t.Fatalf("collectDartFilesWithMtime failed: %v", err)
	}
	if len(dartFilesWithMtimes) != 1 || len(mtimes) != 1 {
		t.Fatalf("expected 1 file with mtime: %v, %v", dartFilesWithMtimes, mtimes)
	}
}

func TestWatchDetectsChange(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "src", "main.ts")
	_ = os.MkdirAll(filepath.Dir(filePath), 0o755)
	_ = os.WriteFile(filePath, []byte("console.log('v1');"), 0o644)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	changedCh := make(chan []string, 5)
	onChange := func(changed []string) {
		changedCh <- changed
	}

	go func() {
		_ = Watch(ctx, dir, 50*time.Millisecond, onChange)
	}()

	// Wait for baseline tick
	time.Sleep(120 * time.Millisecond)

	// Modify the file
	_ = os.WriteFile(filePath, []byte("console.log('v2-updated');"), 0o644)

	select {
	case changed := <-changedCh:
		if len(changed) == 0 {
			t.Fatal("onChange received empty changed list")
		}
		found := false
		for _, c := range changed {
			if c == "src/main.ts" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("src/main.ts not found in changed: %v", changed)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for watch change notification")
	}
}
