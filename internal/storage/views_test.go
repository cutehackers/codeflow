package storage

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestTaskViewPersistence(t *testing.T) {
	root := t.TempDir()
	ctx := context.Background()
	store := New(root)
	payload := []byte(`{"token":"do-not-store","semanticMap":{"generationId":"gen-a","summary":{"requested":"Checkout"}},"flowContexts":{"a":{"displayedLines":[{"text":"return order;"}]}}}`)
	id, clean, err := store.SaveView(ctx, payload)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(clean, []byte("do-not-store")) {
		t.Fatal("token persisted")
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			got, _, err := store.SaveView(ctx, payload)
			if err != nil || got != id {
				t.Errorf("concurrent save: %s %v", got, err)
			}
		})
	}
	wg.Wait()
	reopened := New(root)
	got, err := reopened.ReadView(ctx, id)
	if err != nil || !bytes.Equal(got, clean) {
		t.Fatalf("restoration: %v", err)
	}
	list, err := reopened.ListViews(ctx)
	if err != nil || len(list) != 1 || list[0].Title != "Checkout" {
		t.Fatalf("list: %+v %v", list, err)
	}
	for _, invalid := range []string{"../pointer", "", id + "/.."} {
		if _, err := reopened.ReadView(ctx, invalid); err == nil {
			t.Errorf("accepted %q", invalid)
		}
	}
	if err := os.WriteFile(filepath.Join(root, ".codeflow", "semantics", "views", id+".json"), []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.ReadView(ctx, id); err == nil {
		t.Fatal("accepted corrupted result")
	}
}

func TestTaskViewRejectsOutsideSymlink(t *testing.T) {
	root := t.TempDir()
	if err := os.Symlink(t.TempDir(), filepath.Join(root, ".codeflow")); err != nil {
		t.Skip(err)
	}
	if _, _, err := New(root).SaveView(context.Background(), []byte(`{"semanticMap":{}}`)); err == nil {
		t.Fatal("escaped repository")
	}
}
