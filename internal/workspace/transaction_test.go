package workspace

import (
	"context"
	"os"
	"testing"
)

func TestVS03A3_MultiFileEditTransaction(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "codeflow-tx-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	engine, err := NewSnapshotEngine(tempDir, "epoch-tx-test")
	if err != nil {
		t.Fatalf("NewSnapshotEngine failed: %v", err)
	}

	ctx := context.Background()

	// Initial single edit to establish baseline snapshot
	_, initialSnap, err := engine.ApplyVersionedEdit(ctx, EditRequest{
		Path:            "lib/root.dart",
		Content:         []byte("// root"),
		DocumentVersion: 1,
		Source:          "ide_versioned",
	})
	if err != nil {
		t.Fatalf("initial edit failed: %v", err)
	}
	if initialSnap.Sequence != 1 {
		t.Fatalf("expected sequence 1, got %d", initialSnap.Sequence)
	}

	// 1. Begin multi-file transaction
	tx, err := engine.BeginTransaction("agent_transaction")
	if err != nil {
		t.Fatalf("BeginTransaction failed: %v", err)
	}

	// Stage two edits in the transaction
	err = tx.StageEdit(EditRequest{
		Path:            "lib/user.dart",
		Content:         []byte("class User { final String id; User(this.id); }"),
		DocumentVersion: 1,
	})
	if err != nil {
		t.Fatalf("StageEdit 1 failed: %v", err)
	}

	err = tx.StageEdit(EditRequest{
		Path:            "lib/cart.dart",
		Content:         []byte("class Cart { final List items = []; }"),
		DocumentVersion: 1,
	})
	if err != nil {
		t.Fatalf("StageEdit 2 failed: %v", err)
	}

	// While transaction is open, liveHead MUST NOT advance
	if engine.LiveHead().SnapshotID != initialSnap.SnapshotID {
		t.Errorf("liveHead advanced prematurely before transaction commit")
	}

	// 2. Commit transaction
	batch, txSnap, err := engine.CommitTransaction(ctx, tx)
	if err != nil {
		t.Fatalf("CommitTransaction failed: %v", err)
	}

	// VS03-A3: Transaction changes reflected in ONE snapshot sequence increment
	if txSnap.Sequence != 2 {
		t.Errorf("expected sequence to increment by exactly 1 to 2, got %d", txSnap.Sequence)
	}
	if !txSnap.LiveHead {
		t.Errorf("expected txSnap.LiveHead == true")
	}
	if engine.LiveHead().SnapshotID != txSnap.SnapshotID {
		t.Errorf("engine.LiveHead() not updated to txSnap")
	}

	// Both files must be present in entries
	if _, ok := txSnap.Entries["lib/user.dart"]; !ok {
		t.Errorf("missing lib/user.dart in txSnap entries")
	}
	if _, ok := txSnap.Entries["lib/cart.dart"]; !ok {
		t.Errorf("missing lib/cart.dart in txSnap entries")
	}
	if _, ok := txSnap.Entries["lib/root.dart"]; !ok {
		t.Errorf("missing previous lib/root.dart in txSnap entries")
	}

	// ChangeBatch assertions
	if batch.Status != "committed" {
		t.Errorf("expected batch status committed, got %s", batch.Status)
	}
	if len(batch.Revisions) != 2 {
		t.Errorf("expected 2 revisions in batch, got %d", len(batch.Revisions))
	}
	if batch.Source != "agent_transaction" {
		t.Errorf("expected batch source agent_transaction, got %s", batch.Source)
	}

	// 3. Test Abort Transaction
	txAbort, err := engine.BeginTransaction("agent_transaction")
	if err != nil {
		t.Fatalf("BeginTransaction 2 failed: %v", err)
	}
	_ = txAbort.StageEdit(EditRequest{
		Path:            "lib/temp.dart",
		Content:         []byte("will abort"),
		DocumentVersion: 1,
	})
	err = engine.AbortTransaction(txAbort)
	if err != nil {
		t.Fatalf("AbortTransaction failed: %v", err)
	}

	// LiveHead must remain txSnap (seq 2)
	if engine.LiveHead().SnapshotID != txSnap.SnapshotID {
		t.Errorf("liveHead changed after abort")
	}
}
