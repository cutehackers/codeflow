package storage_test

import (
	"errors"
	"sync"
	"testing"
	"time"

	"codeflow/internal/storage"
)

func TestWithValidatedActiveApprovalIdentityReturnsExactCurrentProof(t *testing.T) {
	root := t.TempDir()
	st := storage.New(root)
	if err := st.InitLayout(); err != nil {
		t.Fatal(err)
	}
	previous := ""
	tx := publicationTx("generation-approval", "snapshot-approval", &previous)
	liveHead := "snapshot-approval"
	tx.LiveHeadCommit = publicationAuthority(&liveHead)
	if _, err := st.PublishGeneration(tx); err != nil {
		t.Fatalf("publish active proof: %v", err)
	}

	var got storage.ValidatedActiveApprovalIdentity
	called := false
	if err := st.WithValidatedActiveApprovalIdentity(func(identity storage.ValidatedActiveApprovalIdentity) error {
		called = true
		got = identity
		return nil
	}); err != nil {
		t.Fatalf("read validated approval identity: %v", err)
	}
	if !called {
		t.Fatal("validated approval identity callback was not called")
	}
	want := storage.ValidatedActiveApprovalIdentity{
		ComputedBasisID:            "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		GenerationID:               "generation-approval",
		ValidatedSnapshotID:        "snapshot-approval",
		ExpectedLiveHeadSnapshotID: "snapshot-approval",
		MapID:                      "map-generation-approval",
		TaskID:                     "task-test",
		IntentRevision:             1,
		WorkspaceEpoch:             1,
	}
	if got != want {
		t.Fatalf("validated approval identity = %+v, want %+v", got, want)
	}
}

func TestWithValidatedActiveApprovalIdentityRejectsMissingProofAndNilCallback(t *testing.T) {
	st := storage.New(t.TempDir())
	if err := st.WithValidatedActiveApprovalIdentity(nil); err == nil {
		t.Fatal("nil callback was accepted")
	}
	called := false
	err := st.WithValidatedActiveApprovalIdentity(func(storage.ValidatedActiveApprovalIdentity) error {
		called = true
		return nil
	})
	if err == nil {
		t.Fatal("missing active proof was accepted")
	}
	if errors.Is(err, storage.ErrCASConflict) {
		t.Fatalf("missing proof returned an unrelated CAS conflict: %v", err)
	}
	if called {
		t.Fatal("missing active proof invoked callback")
	}
}

func TestWithValidatedActiveApprovalIdentityHoldsPublicationLockThroughCallback(t *testing.T) {
	root := t.TempDir()
	st := storage.New(root)
	if err := st.InitLayout(); err != nil {
		t.Fatal(err)
	}
	previous := ""
	first := publicationTx("generation-lock", "snapshot-lock", &previous)
	liveHead := "snapshot-lock"
	first.LiveHeadCommit = publicationAuthority(&liveHead)
	if _, err := st.PublishGeneration(first); err != nil {
		t.Fatalf("publish initial proof: %v", err)
	}

	entered := make(chan struct{})
	release := make(chan struct{})
	identityErr := make(chan error, 1)
	var once sync.Once
	go func() {
		identityErr <- st.WithValidatedActiveApprovalIdentity(func(storage.ValidatedActiveApprovalIdentity) error {
			once.Do(func() { close(entered) })
			<-release
			return nil
		})
	}()
	select {
	case err := <-identityErr:
		t.Fatalf("identity callback returned before it entered: %v", err)
	case <-entered:
	}

	nextPrevious := "generation-lock"
	next := publicationTxWithSequence("generation-lock-next", "snapshot-lock-next", &nextPrevious, 2)
	next.ExpectedPreviousGenerationID = nextPrevious
	nextLiveHead := "snapshot-lock-next"
	next.LiveHeadCommit = publicationAuthority(&nextLiveHead)
	published := make(chan error, 1)
	go func() {
		_, err := st.PublishGeneration(next)
		published <- err
	}()
	select {
	case err := <-published:
		close(release)
		t.Fatalf("publication completed while identity callback held lock: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	if err := <-identityErr; err != nil {
		t.Fatalf("identity callback: %v", err)
	}
	if err := <-published; err != nil {
		t.Fatalf("publication after identity callback: %v", err)
	}
}
