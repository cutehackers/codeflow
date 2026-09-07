package semantic

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"codeflow/internal/protocol"
)

func TestDurableProposalStorePersistsValidatedEnrichmentAcrossReopen(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") == "semantic" {
		return
	}
	result := validEnrichmentResultForProposalStore(t)
	root := t.TempDir()
	store := NewDurableProposalStore(root)
	if err := store.SaveEnrichmentResult(context.Background(), "workspace-f1", &result); err != nil {
		t.Fatalf("save validated enrichment: %v", err)
	}
	loaded, err := store.Load(context.Background(), "workspace-f1", result.Proposal.ProposalID, result.Pack.EvidencePackID)
	if err != nil {
		t.Fatalf("load persisted proposal: %v", err)
	}
	if loaded.Proposal.ProposalID != result.Proposal.ProposalID || loaded.Pack.EvidencePackID != result.Pack.EvidencePackID {
		t.Fatalf("loaded identity = proposal %q pack %q", loaded.Proposal.ProposalID, loaded.Pack.EvidencePackID)
	}

	// Re-opening the store models a process restart. The lookup remains ID-only
	// and must recover the exact same immutable pair from disk.
	reopened := NewDurableProposalStore(root)
	recovered, err := reopened.Load(context.Background(), "workspace-f1", result.Proposal.ProposalID, result.Pack.EvidencePackID)
	if err != nil {
		t.Fatalf("load after reopening store: %v", err)
	}
	if recovered.Proposal.PackDigest != result.Proposal.PackDigest || recovered.Pack.PackDigest != result.Pack.PackDigest {
		t.Fatalf("recovered provenance = proposal %q pack %q, want %q", recovered.Proposal.PackDigest, recovered.Pack.PackDigest, result.Pack.PackDigest)
	}

	// Store boundaries own immutable snapshots. Neither the caller's result nor
	// one loaded result may mutate another representation.
	result.Proposal.ProposedTitle = "caller mutation"
	loaded.Proposal.ProposedTitle = "loaded mutation"
	recoveredAgain, err := reopened.Load(context.Background(), "workspace-f1", recovered.Proposal.ProposalID, recovered.Pack.EvidencePackID)
	if err != nil {
		t.Fatalf("reload after caller mutations: %v", err)
	}
	if recoveredAgain.Proposal.ProposedTitle == "caller mutation" || recoveredAgain.Proposal.ProposedTitle == "loaded mutation" {
		t.Fatal("durable proposal store returned mutable aliases")
	}

	if _, err := reopened.Load(context.Background(), "other-workspace", recovered.Proposal.ProposalID, recovered.Pack.EvidencePackID); !errors.Is(err, ErrProposalNotFound) {
		t.Fatalf("wrong workspace error = %v, want ErrProposalNotFound", err)
	}
	if _, err := reopened.Load(context.Background(), "workspace-f1", recovered.Proposal.ProposalID, "pack-missing"); !errors.Is(err, ErrProposalNotFound) {
		t.Fatalf("wrong pack error = %v, want ErrProposalNotFound", err)
	}
}

func TestDurableProposalStoreRejectsInvalidEnrichmentWithoutPersistence(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") == "semantic" {
		return
	}
	result := validEnrichmentResultForProposalStore(t)
	root := t.TempDir()
	store := NewDurableProposalStore(root)
	result.Proposal.Authority = "verified"
	if err := store.SaveEnrichmentResult(context.Background(), "workspace-f1", &result); !errors.Is(err, ErrProposalInvalid) {
		t.Fatalf("invalid proposal save error = %v, want ErrProposalInvalid", err)
	}
	if _, err := store.Load(context.Background(), "workspace-f1", result.Proposal.ProposalID, result.Pack.EvidencePackID); !errors.Is(err, ErrProposalNotFound) {
		t.Fatalf("invalid save left durable record: %v", err)
	}
}

func TestDurableProposalStoreRejectsIdentityAndProvenanceMutation(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") == "semantic" {
		return
	}
	result := validEnrichmentResultForProposalStore(t)
	store := NewDurableProposalStore(t.TempDir())
	const workspaceID = "workspace-f1"
	if err := store.SaveEnrichmentResult(context.Background(), workspaceID, &result); err != nil {
		t.Fatalf("save validated enrichment: %v", err)
	}
	path := store.recordPath(workspaceID, result.Proposal.ProposalID, result.Pack.EvidencePackID)
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read durable record: %v", err)
	}

	mutateAndWrite := func(t *testing.T, mutate func(*durableProposalRecord)) {
		t.Helper()
		var record durableProposalRecord
		if err := json.Unmarshal(original, &record); err != nil {
			t.Fatalf("decode durable record: %v", err)
		}
		mutate(&record)
		data, err := json.Marshal(record)
		if err != nil {
			t.Fatalf("encode mutated durable record: %v", err)
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatalf("write mutated durable record: %v", err)
		}
	}
	restore := func(t *testing.T) {
		t.Helper()
		if err := os.WriteFile(path, original, 0o600); err != nil {
			t.Fatalf("restore durable record: %v", err)
		}
	}

	t.Run("proposal identity mismatch", func(t *testing.T) {
		mutateAndWrite(t, func(record *durableProposalRecord) {
			record.Proposal.ProposalID = "proposal-other"
		})
		_, err := store.Load(context.Background(), workspaceID, result.Proposal.ProposalID, result.Pack.EvidencePackID)
		var invalid *ProposalInvalidError
		if !errors.As(err, &invalid) || !errors.Is(err, ErrProposalInvalid) {
			t.Fatalf("identity mismatch error = %v, want typed ErrProposalInvalid", err)
		}
		restore(t)
	})

	t.Run("proposal pack provenance mismatch", func(t *testing.T) {
		mutateAndWrite(t, func(record *durableProposalRecord) {
			record.Proposal.PackDigest = "f" + strings.Repeat("0", 63)
		})
		_, err := store.Load(context.Background(), workspaceID, result.Proposal.ProposalID, result.Pack.EvidencePackID)
		var invalid *ProposalInvalidError
		if !errors.As(err, &invalid) || !errors.Is(err, ErrProposalInvalid) || !strings.Contains(err.Error(), "provenance") {
			t.Fatalf("provenance mismatch error = %v, want typed provenance invalid", err)
		}
		restore(t)
	})
}

func TestDurableProposalStoreConcurrentInstancesKeepIdentityImmutable(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") == "semantic" {
		return
	}
	result := validEnrichmentResultForProposalStoreWithTitle(t, "Submit checkout")
	other := validEnrichmentResultForProposalStoreWithTitle(t, "concurrent alternate title")
	if err := validateAvailableEnrichmentResult(&result); err != nil {
		t.Fatalf("baseline accepted result validation: %v", err)
	}
	if err := validateAvailableEnrichmentResult(&other); err != nil {
		t.Fatalf("alternate accepted result validation: %v", err)
	}
	if !reflect.DeepEqual(result.Pack, other.Pack) {
		t.Fatalf("concurrent candidates use different evidence packs: baseline=%+v alternate=%+v", result.Pack, other.Pack)
	}
	if result.Proposal.ProposalID != other.Proposal.ProposalID || result.Pack.EvidencePackID != other.Pack.EvidencePackID {
		t.Fatalf("concurrent candidates use different identities: baseline=%s/%s alternate=%s/%s", result.Proposal.ProposalID, result.Pack.EvidencePackID, other.Proposal.ProposalID, other.Pack.EvidencePackID)
	}
	root := t.TempDir()
	stores := []*DurableProposalStore{NewDurableProposalStore(root), NewDurableProposalStore(root)}
	type saveOutcome struct {
		title string
		err   error
	}
	results := make(chan saveOutcome, len(stores))
	start := make(chan struct{})
	var group sync.WaitGroup
	for index, store := range stores {
		group.Add(1)
		go func(index int, store *DurableProposalStore) {
			defer group.Done()
			<-start
			candidate := &result
			if index == 1 {
				candidate = &other
			}
			results <- saveOutcome{title: candidate.Proposal.ProposedTitle, err: store.SaveEnrichmentResult(context.Background(), "workspace-f1", candidate)}
		}(index, store)
	}
	close(start)
	group.Wait()
	close(results)

	var successes, immutableConflicts int
	var winnerTitle string
	for outcome := range results {
		if outcome.err == nil {
			successes++
			winnerTitle = outcome.title
			continue
		}
		if errors.Is(outcome.err, ErrProposalInvalid) && strings.Contains(outcome.err.Error(), "durable proposal identity is immutable") && !strings.Contains(outcome.err.Error(), "seal") {
			immutableConflicts++
			continue
		}
		t.Fatalf("concurrent save error = %v, want only immutable conflict", outcome.err)
	}
	if successes != 1 || immutableConflicts != 1 {
		t.Fatalf("concurrent saves = successes %d immutable conflicts %d, want 1/1", successes, immutableConflicts)
	}
	reopened := NewDurableProposalStore(root)
	loaded, err := reopened.Load(context.Background(), "workspace-f1", result.Proposal.ProposalID, result.Pack.EvidencePackID)
	if err != nil {
		t.Fatalf("load concurrent winner: %v", err)
	}
	var winner *EnrichmentResult
	if winnerTitle == result.Proposal.ProposedTitle {
		winner = &result
	} else if winnerTitle == other.Proposal.ProposedTitle {
		winner = &other
	} else {
		t.Fatalf("winner title %q is neither immutable candidate", winnerTitle)
	}
	if !reflect.DeepEqual(loaded.Proposal, winner.Proposal) || !reflect.DeepEqual(loaded.Pack, winner.Pack) {
		t.Fatalf("loaded winner does not match winning candidate: loaded=%+v candidate=%+v", loaded, winner)
	}
}

func TestProposalStoreIsReplaceableAndApprovalLookupIsIdentityOnly(t *testing.T) {
	result := validEnrichmentResultForProposalStore(t)
	store := &proposalStoreTestDouble{
		stored: &StoredProposal{
			WorkspaceID: "workspace-seam",
			Proposal:    cloneModelProposal(result.Proposal),
			Pack:        cloneEvidencePack(result.Pack),
		},
	}
	loaded, err := LoadProposalForApproval(context.Background(), store, "workspace-seam", result.Proposal.ProposalID, result.Pack.EvidencePackID)
	if err != nil {
		t.Fatalf("identity-only lookup: %v", err)
	}
	if store.saveCalls != 0 || store.workspaceID != "workspace-seam" || store.proposalID != result.Proposal.ProposalID || store.evidencePackID != result.Pack.EvidencePackID {
		t.Fatalf("lookup used unexpected store path: calls=%d workspace=%q proposal=%q pack=%q", store.saveCalls, store.workspaceID, store.proposalID, store.evidencePackID)
	}
	loaded.Proposal.ProposalID = "caller-mutation"
	loadedAgain, err := LoadProposalForApproval(context.Background(), store, "workspace-seam", result.Proposal.ProposalID, result.Pack.EvidencePackID)
	if err != nil {
		t.Fatalf("second identity-only lookup: %v", err)
	}
	if loadedAgain.Proposal.ProposalID != result.Proposal.ProposalID {
		t.Fatalf("replaceable store returned caller alias %q", loadedAgain.Proposal.ProposalID)
	}
}

func TestProposalStoreRejectsPostAcceptanceProposalMutation(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") == "semantic" {
		return
	}
	result := validEnrichmentResultForProposalStore(t)
	result.Proposal.ProposedTitle = "caller changed accepted proposal"
	root := t.TempDir()
	store := NewDurableProposalStore(root)
	err := store.SaveEnrichmentResult(context.Background(), "workspace-f1", &result)
	var invalid *ProposalInvalidError
	if !errors.As(err, &invalid) || !errors.Is(err, ErrProposalInvalid) || !strings.Contains(err.Error(), "seal") {
		t.Fatalf("post-acceptance mutation error = %v, want sealed ErrProposalInvalid", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".codeflow", "semantic-proposals")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("post-acceptance mutation created store state: %v", err)
	}

	packMutation := validEnrichmentResultForProposalStore(t)
	packMutation.Pack = cloneEvidencePack(packMutation.Pack)
	packMutation.Pack.Items[0].Content = "caller changed accepted evidence"
	packRoot := t.TempDir()
	err = NewDurableProposalStore(packRoot).SaveEnrichmentResult(context.Background(), "workspace-f1", &packMutation)
	if !errors.As(err, &invalid) || !errors.Is(err, ErrProposalInvalid) || !strings.Contains(err.Error(), "seal") {
		t.Fatalf("post-acceptance pack mutation error = %v, want sealed ErrProposalInvalid", err)
	}
}

func TestLoadProposalForApprovalRejectsUntrustedStoreValue(t *testing.T) {
	cases := []struct {
		name   string
		stored *StoredProposal
	}{
		{name: "nil value"},
		{name: "workspace mismatch", stored: &StoredProposal{WorkspaceID: "other-workspace", Proposal: &ModelProposal{ProposalID: "proposal-seam"}, Pack: &EvidencePack{EvidencePackID: "pack-seam"}}},
		{name: "incomplete proposal pair", stored: &StoredProposal{WorkspaceID: "workspace-seam", Proposal: &ModelProposal{ProposalID: "proposal-seam"}, Pack: &EvidencePack{EvidencePackID: "pack-seam"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &untrustedProposalStore{stored: tc.stored}
			loaded, err := LoadProposalForApproval(context.Background(), store, "workspace-seam", "proposal-seam", "pack-seam")
			var invalid *ProposalInvalidError
			if loaded != nil || !errors.As(err, &invalid) || !errors.Is(err, ErrProposalInvalid) {
				t.Fatalf("untrusted store result = %+v error=%v, want typed invalid", loaded, err)
			}
		})
	}
}

type untrustedProposalStore struct {
	stored *StoredProposal
}

func (*untrustedProposalStore) SaveEnrichmentResult(context.Context, string, *EnrichmentResult) error {
	return nil
}

func (s *untrustedProposalStore) Load(context.Context, string, string, string) (*StoredProposal, error) {
	return s.stored, nil
}

func TestProposalStoreRejectsSymlinkedStoreRoot(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") == "semantic" {
		return
	}
	result := validEnrichmentResultForProposalStore(t)
	backing := t.TempDir()
	linkParent := t.TempDir()
	linkedRoot := filepath.Join(linkParent, "semantic-proposals")
	if err := os.Symlink(backing, linkedRoot); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	err := NewFileProposalStore(linkedRoot).SaveEnrichmentResult(context.Background(), "workspace-f1", &result)
	var invalid *ProposalInvalidError
	if !errors.As(err, &invalid) || !errors.Is(err, ErrProposalInvalid) {
		t.Fatalf("symlinked store root error = %v, want typed invalid", err)
	}
	entries, readErr := os.ReadDir(backing)
	if readErr != nil {
		t.Fatalf("read symlink backing root: %v", readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("symlinked store root was followed, entries=%v", entries)
	}
}

func TestDurableProposalStoreCanonicalizesRepositoryRootSymlink(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") == "semantic" {
		return
	}
	result := validEnrichmentResultForProposalStore(t)
	backing := t.TempDir()
	linkParent := t.TempDir()
	linkedRoot := filepath.Join(linkParent, "repo-link")
	if err := os.Symlink(backing, linkedRoot); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	store := NewDurableProposalStore(linkedRoot)
	if err := store.SaveEnrichmentResult(context.Background(), "workspace-f1", &result); err != nil {
		t.Fatalf("save through repository-root symlink: %v", err)
	}
	if _, err := store.Load(context.Background(), "workspace-f1", result.Proposal.ProposalID, result.Pack.EvidencePackID); err != nil {
		t.Fatalf("load through repository-root symlink: %v", err)
	}
}

func TestProposalStoreRejectsSymlinkedRecord(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") == "semantic" {
		return
	}
	result := validEnrichmentResultForProposalStore(t)
	store := NewFileProposalStore(t.TempDir())
	const workspaceID = "workspace-f1"
	if err := store.SaveEnrichmentResult(context.Background(), workspaceID, &result); err != nil {
		t.Fatalf("save baseline record: %v", err)
	}
	path := store.recordPath(workspaceID, result.Proposal.ProposalID, result.Pack.EvidencePackID)
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read baseline record: %v", err)
	}
	outside := filepath.Join(t.TempDir(), "outside.json")
	if err := os.WriteFile(outside, original, 0o600); err != nil {
		t.Fatalf("write symlink target: %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove baseline record: %v", err)
	}
	if err := os.Symlink(outside, path); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := store.Load(context.Background(), workspaceID, result.Proposal.ProposalID, result.Pack.EvidencePackID); !errors.Is(err, ErrProposalInvalid) {
		t.Fatalf("symlinked record load error = %v, want ErrProposalInvalid", err)
	}
	if err := store.SaveEnrichmentResult(context.Background(), workspaceID, &result); !errors.Is(err, ErrProposalInvalid) {
		t.Fatalf("symlinked record save error = %v, want ErrProposalInvalid", err)
	}
}

func TestProposalStoreTightensManagedPermissions(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") == "semantic" {
		return
	}
	result := validEnrichmentResultForProposalStore(t)
	root := t.TempDir()
	if err := os.Chmod(root, 0o755); err != nil {
		t.Fatalf("make permissive store root: %v", err)
	}
	store := NewFileProposalStore(root)
	const workspaceID = "workspace-f1"
	if err := store.SaveEnrichmentResult(context.Background(), workspaceID, &result); err != nil {
		t.Fatalf("save with permissive store root: %v", err)
	}
	path := store.recordPath(workspaceID, result.Proposal.ProposalID, result.Pack.EvidencePackID)
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatalf("make permissive record: %v", err)
	}
	if err := os.Chmod(root, 0o755); err != nil {
		t.Fatalf("reopen permissive store root: %v", err)
	}
	if err := store.SaveEnrichmentResult(context.Background(), workspaceID, &result); err != nil {
		t.Fatalf("resave permissive record: %v", err)
	}
	rootInfo, err := os.Stat(root)
	if err != nil {
		t.Fatalf("stat store root: %v", err)
	}
	if rootInfo.Mode().Perm() != 0o700 {
		t.Fatalf("store root mode = %o, want 700", rootInfo.Mode().Perm())
	}
	recordInfo, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat proposal record: %v", err)
	}
	if recordInfo.Mode().Perm() != 0o600 {
		t.Fatalf("proposal record mode = %o, want 600", recordInfo.Mode().Perm())
	}
}

func TestPersistAvailableEnrichmentUsesReplaceableStore(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") == "semantic" {
		return
	}
	result := validEnrichmentResultForProposalStore(t)
	store := &proposalStoreTestDouble{}
	if err := PersistAvailableEnrichment(context.Background(), store, "workspace-f1", &result); err != nil {
		t.Fatalf("persist available enrichment: %v", err)
	}
	if store.saveCalls != 1 || store.savedWorkspaceID != "workspace-f1" || store.saved == nil || store.saved.Proposal.ProposalID != result.Proposal.ProposalID || store.saved.Pack.EvidencePackID != result.Pack.EvidencePackID {
		t.Fatalf("replaceable persistence call = calls %d workspace %q saved=%v", store.saveCalls, store.savedWorkspaceID, store.saved)
	}
}

func TestRunSemanticEnrichmentAndPersistUsesStoreAfterActualSuccess(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") == "semantic" {
		return
	}
	request, host := newProposalStoreEnrichmentRun(t)
	store := &proposalStoreTestDouble{}
	result, err := RunSemanticEnrichmentAndPersist(context.Background(), EnrichmentRequest{
		EvidencePack: request, ModelHostFactory: func(context.Context) (*protocol.ModelHost, error) { return host, nil }, PromptRevision: "prompt-store-f1",
	}, store, "workspace-f1")
	if err != nil {
		t.Fatalf("run and persist actual enrichment: %v", err)
	}
	if result.State.Status != "available" || result.Proposal == nil || result.Pack == nil {
		t.Fatalf("actual enrichment result = %+v", result)
	}
	if store.saveCalls != 1 || store.saved == nil || store.saved.Proposal.ProposalID != result.Proposal.ProposalID || store.saved.Pack.EvidencePackID != result.Pack.EvidencePackID {
		t.Fatalf("production persistence seam = calls %d saved=%v", store.saveCalls, store.saved)
	}
}

type proposalStoreTestDouble struct {
	stored           *StoredProposal
	workspaceID      string
	proposalID       string
	evidencePackID   string
	saveCalls        int
	savedWorkspaceID string
	saved            *EnrichmentResult
}

func (s *proposalStoreTestDouble) SaveEnrichmentResult(_ context.Context, workspaceID string, result *EnrichmentResult) error {
	s.saveCalls++
	if s.saved == nil {
		s.savedWorkspaceID = workspaceID
		s.saved = result
		return nil
	}
	return errors.New("test double save called more than once")
}

func (s *proposalStoreTestDouble) Load(_ context.Context, workspaceID, proposalID, evidencePackID string) (*StoredProposal, error) {
	s.workspaceID, s.proposalID, s.evidencePackID = workspaceID, proposalID, evidencePackID
	if s.stored == nil || s.stored.WorkspaceID != workspaceID || s.stored.Proposal.ProposalID != proposalID || s.stored.Pack.EvidencePackID != evidencePackID {
		return nil, &ProposalNotFoundError{WorkspaceID: workspaceID, ProposalID: proposalID, EvidencePackID: evidencePackID}
	}
	return &StoredProposal{WorkspaceID: s.stored.WorkspaceID, Proposal: cloneModelProposal(s.stored.Proposal), Pack: cloneEvidencePack(s.stored.Pack)}, nil
}

func validEnrichmentResultForProposalStore(t *testing.T) EnrichmentResult {
	return validEnrichmentResultForProposalStoreWithTitle(t, "Submit checkout")
}

func validEnrichmentResultForProposalStoreWithTitle(t *testing.T, title string) EnrichmentResult {
	t.Helper()
	request, host := newProposalStoreEnrichmentRunWithTitle(t, title)
	result := RunSemanticEnrichment(context.Background(), EnrichmentRequest{
		EvidencePack: request, ModelHostFactory: func(context.Context) (*protocol.ModelHost, error) { return host, nil }, PromptRevision: "prompt-store-f1",
	})
	if result.State.Status != "available" || result.Proposal == nil || result.Pack == nil {
		t.Fatalf("actual enrichment result = %+v", result)
	}
	return result
}

func newProposalStoreEnrichmentRun(t *testing.T) (EvidencePackRequest, *protocol.ModelHost) {
	return newProposalStoreEnrichmentRunWithTitle(t, "Submit checkout")
}

func newProposalStoreEnrichmentRunWithTitle(t *testing.T, title string) (EvidencePackRequest, *protocol.ModelHost) {
	t.Helper()
	snapshot, mapIR, request := enrichmentTestInput(t)
	pack, err := BuildEvidencePackV2(request)
	if err != nil {
		t.Fatal(err)
	}
	digests, err := CanonicalQ3Digests(mapIR)
	if err != nil {
		t.Fatal(err)
	}
	proposal, err := json.Marshal(ModelProposal{
		SchemaID: SemanticProposalV2SchemaID, SchemaVersion: 2, ProposalID: "proposal-store-f1",
		ComputedBasisID: snapshot.ComputedBasisID, GenerationID: mapIR.GenerationID, SnapshotID: snapshot.SnapshotID,
		TargetStepID: "step-a03", TargetSymbolPath: "Submit", ProposedTitle: title,
		ProposedCategory: "entry", EpistemicStatus: "inferred", Authority: "model", ClaimScope: "display_only",
		ModelID: "actual-test-model", ModelRevision: "r1", PromptRevision: "prompt-store-f1", SchemaProfile: SemanticProposalSchemaProfile, PackDigest: pack.PackDigest,
		EvidenceRefs: []string{"e-a03"}, FactDigest: digests.Fact, ObligationDigest: digests.Obligation,
		AlignmentDigest: digests.Alignment, SettlementDigest: digests.Settlement,
	})
	if err != nil {
		t.Fatal(err)
	}
	proposalEncoded := base64.RawStdEncoding.EncodeToString(proposal)
	host, err := protocol.SpawnModelHost(context.Background(), protocol.ModelHostConfig{
		BinPath: os.Args[0], Args: []string{"-test.run=TestSemanticModelHostHelper"},
		Env: []string{"CODEFLOW_MODEL_HOST_HELPER=semantic", "CODEFLOW_VS08_MODEL_HOST_RELEASE=" + proposalEncoded}, DefaultTimeout: 3 * time.Second,
	})
	if err != nil {
		t.Fatalf("spawn actual proposal-store host: %v", err)
	}
	return request, host
}
