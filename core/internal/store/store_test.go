package store

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"codeflow/core/internal/flowir"
	_ "modernc.org/sqlite"
)

func TestWALAndFailedPublicationPreservesPreviousSnapshot(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "lib"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "lib", "signup.dart"), []byte("void signup() {}"), 0644); err != nil {
		t.Fatal(err)
	}
	doc, err := flowir.Fixture(repo, flowir.Basis{Repository: repo, HeadRevision: "test", WorktreeFingerprint: flowir.Hash(repo), Manifest: []flowir.ManifestEntry{}})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Publish(context.Background(), doc, "first", "ready"); err != nil {
		t.Fatal(err)
	}
	other, err := sql.Open("sqlite", filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	var mode string
	if err := other.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil || mode != "wal" {
		t.Fatalf("journal=%q err=%v", mode, err)
	}
	doc.SchemaVersion = "wrong"
	if err := db.Publish(context.Background(), doc, "bad", "ready"); err == nil {
		t.Fatal("invalid publication must fail")
	}
	stored, at, _, err := db.Get(context.Background(), "route:/signup")
	if err != nil {
		t.Fatal(err)
	}
	if at != "first" || stored.SchemaVersion != flowir.SchemaVersion {
		t.Fatalf("snapshot was not atomic: at=%s doc=%#v", at, stored)
	}
}

func TestDebtReviewIsSeparateAndResolvesOnlyWhenUnknownDisappears(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "lib"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "lib", "signup.dart"), []byte("void signup() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	doc, err := flowir.Fixture(repo, flowir.Basis{Repository: repo, HeadRevision: "test", WorktreeFingerprint: flowir.Hash(repo), Manifest: []flowir.ManifestEntry{}})
	if err != nil {
		t.Fatal(err)
	}
	anchor := doc.Current.Steps[0].PrimaryEvidence[0]
	doc.Unknowns = []flowir.UnknownDetail{{ID: flowir.Hash("debt"), Question: "Which result?", Reason: "missing_relation", RelatedSteps: []string{doc.Current.Steps[0].ID}, Evidence: []flowir.Anchor{anchor}, DebtState: "open"}}
	if err := db.Publish(context.Background(), doc, "first", "ready"); err != nil {
		t.Fatal(err)
	}
	canonical, _ := flowir.CanonicalJSON(doc)
	if err := db.ReviewDebt(context.Background(), doc.Current.ID, doc.Unknowns[0].ID, "accepted", "reviewed"); err != nil {
		t.Fatal(err)
	}
	stored, _, _, _ := db.Get(context.Background(), doc.Current.ID)
	afterReview, _ := flowir.CanonicalJSON(stored)
	if string(canonical) != string(afterReview) {
		t.Fatal("review state must not mutate deterministic FlowIR")
	}
	reviews, _ := db.DebtReviews(context.Background(), doc.Current.ID)
	if len(reviews) != 1 || reviews[0].State != "accepted" {
		t.Fatalf("review=%#v", reviews)
	}
	doc.Unknowns = nil
	if err := db.Publish(context.Background(), doc, "second", "ready"); err != nil {
		t.Fatal(err)
	}
	reviews, _ = db.DebtReviews(context.Background(), doc.Current.ID)
	if reviews[0].State != "resolved" {
		t.Fatalf("disappeared debt must resolve: %#v", reviews)
	}
}
