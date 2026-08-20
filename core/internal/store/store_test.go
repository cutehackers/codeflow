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
	var autoVacuum int
	if err := db.db.QueryRow("PRAGMA auto_vacuum").Scan(&autoVacuum); err != nil || autoVacuum != 2 {
		t.Fatalf("incremental cleanup is not enabled: mode=%d err=%v", autoVacuum, err)
	}
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
	publication, err := db.Publication(context.Background())
	if err != nil || publication.PublishedAt != "first" || publication.Status != "ready" {
		t.Fatalf("small publication metadata unavailable: %#v err=%v", publication, err)
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

func TestPublishBatchIsAtomicAndRequiresOneBasis(t *testing.T) {
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
	first, err := flowir.Fixture(repo, flowir.Basis{Repository: repo, HeadRevision: "same", WorktreeFingerprint: flowir.Hash(repo), Manifest: []flowir.ManifestEntry{}})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.PublishBatch(context.Background(), []flowir.Document{first}, "old", "ready"); err != nil {
		t.Fatal(err)
	}
	second := first
	second.Current.ID, second.Current.FlowKey = "route:/home", "route:/home"
	second.Architecture.EntryPoints = []string{"route:/home"}
	flowir.DeriveScenarios(&second)
	invalid := second
	invalid.SchemaVersion = "invalid"
	if err := db.PublishBatch(context.Background(), []flowir.Document{first, invalid}, "partial", "ready"); err == nil {
		t.Fatal("one invalid flow must reject the complete batch")
	}
	stored, at, _, err := db.GetBatch(context.Background())
	if err != nil || at != "old" || len(stored) != 1 || stored[0].Current.ID != first.Current.ID {
		t.Fatalf("partial batch became visible: at=%q flows=%#v err=%v", at, stored, err)
	}
	if _, err := db.db.Exec(`CREATE TRIGGER fail_second_snapshot BEFORE INSERT ON snapshots WHEN NEW.flow_id='route:/home' BEGIN SELECT RAISE(FAIL, 'injected batch failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := db.PublishBatch(context.Background(), []flowir.Document{first, second}, "mid-transaction", "ready"); err == nil {
		t.Fatal("injected second-flow failure must abort the batch")
	}
	var firstPublishedAt string
	if err := db.db.QueryRow(`SELECT published_at FROM snapshots WHERE flow_id=?`, first.Current.ID).Scan(&firstPublishedAt); err != nil || firstPublishedAt != "old" {
		t.Fatalf("first flow update escaped rolled-back batch: at=%q err=%v", firstPublishedAt, err)
	}
	if _, err := db.db.Exec(`DROP TRIGGER fail_second_snapshot`); err != nil {
		t.Fatal(err)
	}
	if err := db.PublishBatch(context.Background(), []flowir.Document{first, second}, "new", "ready"); err != nil {
		t.Fatal(err)
	}
	stored, at, _, err = db.GetBatch(context.Background())
	if err != nil || at != "new" || len(stored) != 2 || stored[1].Current.ID != "route:/home" {
		t.Fatalf("complete batch missing: at=%q flows=%#v err=%v", at, stored, err)
	}
	otherBasis := second
	otherBasis.Basis.WorktreeFingerprint = "different"
	if err := db.PublishBatch(context.Background(), []flowir.Document{first, otherBasis}, "mixed", "ready"); err == nil {
		t.Fatal("mixed basis must be rejected before publication")
	}
	if err := db.PublishBatch(context.Background(), []flowir.Document{second}, "replace", "ready"); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.db.QueryRow(`SELECT count(*) FROM snapshots`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("inactive snapshots accumulated: count=%d err=%v", count, err)
	}
}
