package workspacewatch

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWatcherNotifiesOnlyForManifestEligiblePaths(t *testing.T) {
	repo := t.TempDir()
	for _, dir := range []string{"lib", ".codeflow", "build"} {
		if err := os.MkdirAll(filepath.Join(repo, dir), 0755); err != nil {
			t.Fatal(err)
		}
	}
	notifications := make(chan struct{}, 8)
	watcher, err := Start(repo, func() { notifications <- struct{}{} })
	if err != nil {
		t.Fatal(err)
	}
	defer watcher.Close()

	for _, path := range []string{filepath.Join(repo, ".codeflow", "runtime.json"), filepath.Join(repo, "build", "generated.dart")} {
		if err := os.WriteFile(path, []byte("tool churn"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case <-notifications:
		t.Fatal("excluded tool/build churn scheduled product reconciliation")
	case <-time.After(200 * time.Millisecond):
	}

	if err := os.WriteFile(filepath.Join(repo, "lib", "page.dart"), []byte("void main() {}"), 0644); err != nil {
		t.Fatal(err)
	}
	select {
	case <-notifications:
	case <-time.After(2 * time.Second):
		t.Fatal("product source change did not schedule reconciliation")
	}
}

func TestWatcherAddsNewProductDirectories(t *testing.T) {
	repo := t.TempDir()
	notifications := make(chan struct{}, 16)
	watcher, err := Start(repo, func() { notifications <- struct{}{} })
	if err != nil {
		t.Fatal(err)
	}
	defer watcher.Close()

	dir := filepath.Join(repo, "packages", "feature")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	// Directory creation itself is already a valid change notification. Drain
	// it, then prove a later write beneath the new tree is watched as well.
	time.Sleep(100 * time.Millisecond)
	for len(notifications) > 0 {
		<-notifications
	}
	if err := os.WriteFile(filepath.Join(dir, "page.dart"), []byte("class Page {}"), 0644); err != nil {
		t.Fatal(err)
	}
	select {
	case <-notifications:
	case <-time.After(2 * time.Second):
		t.Fatal("new product directory was not added to the watcher")
	}
}
