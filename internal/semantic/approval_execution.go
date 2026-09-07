package semantic

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"

	"codeflow/internal/contractharness"
	"codeflow/internal/storage"
	"codeflow/internal/workspace"
)

var (
	// ErrApprovalExecutionInvalid identifies an execution request or a
	// Core-owned execution dependency that cannot be admitted.
	ErrApprovalExecutionInvalid = errors.New("approval execution invalid")
	// ErrApprovalExecutionUnavailable identifies an unavailable or incomplete
	// durable proposal/proof dependency. No approval transaction is published
	// for this class of failure.
	ErrApprovalExecutionUnavailable = errors.New("approval execution unavailable")
	// ErrApprovalExecutionConflict identifies a stale command or current proof
	// identity that cannot be applied to the requested target.
	ErrApprovalExecutionConflict = errors.New("approval execution conflict")
)

// ApprovalExecutionError is a bounded adapter error for the internal durable
// execution service. Its text never includes request values, proposal text,
// evidence, actor identity, or filesystem paths. The cause remains available
// to trusted callers through errors.Is/As.
type ApprovalExecutionError struct {
	Kind  string
	cause error
}

func (e *ApprovalExecutionError) Error() string {
	if e == nil || strings.TrimSpace(e.Kind) == "" {
		return ErrApprovalExecutionInvalid.Error()
	}
	return "approval execution " + e.Kind
}

func (e *ApprovalExecutionError) Unwrap() error {
	if e == nil {
		return ErrApprovalExecutionInvalid
	}
	base := ErrApprovalExecutionInvalid
	switch e.Kind {
	case "unavailable":
		base = ErrApprovalExecutionUnavailable
	case "conflict":
		base = ErrApprovalExecutionConflict
	}
	if e.cause != nil {
		return errors.Join(base, e.cause)
	}
	return base
}

func approvalExecutionInvalid(cause error) error {
	return &ApprovalExecutionError{Kind: "invalid", cause: cause}
}

func approvalExecutionUnavailable(cause error) error {
	return &ApprovalExecutionError{Kind: "unavailable", cause: cause}
}

func approvalExecutionConflict(cause error) error {
	return &ApprovalExecutionError{Kind: "conflict", cause: cause}
}

// ApprovalExecutionResult contains the receipt and only the bounded current
// proof identity needed by local query consumers. It does not expose the
// proposal, evidence pack, semantic map, or Core authority seals.
type ApprovalExecutionResult struct {
	Receipt             ApprovalCommitReceipt `json:"receipt"`
	GenerationID        string                `json:"generationId"`
	ComputedBasisID     string                `json:"computedBasisId"`
	ValidatedSnapshotID string                `json:"validatedSnapshotId"`
	IntentRevision      int64                 `json:"intentRevision"`
	Freshness           string                `json:"freshness"`
}

const (
	approvalExecutionFreshnessCurrent    = "current"
	approvalExecutionFreshnessHistorical = "historical"
)

// ApprovalExecutionService is the semantic-owned durable approval execution
// boundary. It owns one canonical repository root, the root-coordinated
// SnapshotEngine supplied by the caller, the ID-only proposal store, and the
// current-authority transaction store. MCP and FlowView approval handlers use
// this service as their sole mutation path.
type ApprovalExecutionService struct {
	repoRoot        string
	engine          *workspace.SnapshotEngine
	activeStorage   *storage.Storage
	proposalStore   ProposalStore
	transactions    *ApprovalTransactionStore
	historyReader   approvalHistorySnapshotReader
	historyLiveHead func(string, func() error) error
}

// NewApprovalExecutionService creates the one production execution service
// for a canonical repository root and its root-coordinated workspace engine.
// A missing proposal store or an engine for another root is rejected.
func NewApprovalExecutionService(repoRoot string, engine *workspace.SnapshotEngine, proposalStore ProposalStore) (*ApprovalExecutionService, error) {
	if strings.TrimSpace(repoRoot) == "" || engine == nil || proposalStore == nil {
		return nil, approvalExecutionInvalid(nil)
	}
	canonicalRoot, err := canonicalApprovalWorkspaceRoot(repoRoot)
	if err != nil || engine.CanonicalRoot() != canonicalRoot {
		return nil, approvalExecutionInvalid(nil)
	}
	transactions := NewApprovalTransactionStore(canonicalRoot, engine)
	if transactions == nil || transactions.initErr != nil {
		return nil, approvalExecutionUnavailable(nil)
	}
	return &ApprovalExecutionService{repoRoot: canonicalRoot, engine: engine, activeStorage: storage.New(canonicalRoot), proposalStore: proposalStore, transactions: transactions, historyReader: transactions, historyLiveHead: engine.WithLiveHead}, nil
}

// Execute binds untrusted draft intent to the supplied gate-issued access,
// loads the exact durable proposal/pack, validates the current published
// proof and map, reduces one lifecycle command, seals the presentation-only
// map transition, and commits it through the current-authority transaction.
// Every failure before Commit leaves the approval store untouched.
func (s *ApprovalExecutionService) Execute(ctx context.Context, access ApprovalAccess, draft ApprovalCommandDraft) (ApprovalExecutionResult, error) {
	var zero ApprovalExecutionResult
	if s == nil || s.engine == nil || s.proposalStore == nil || s.transactions == nil || s.repoRoot == "" {
		return zero, approvalExecutionInvalid(nil)
	}
	if err := approvalContextError(ctx); err != nil {
		return zero, err
	}
	if !validApprovalExecutionAccess(access, s.repoRoot) {
		return zero, approvalExecutionInvalid(nil)
	}
	command, err := BindApprovalCommandV2(access, draft)
	if err != nil {
		return zero, approvalExecutionInvalid(err)
	}
	commandValue := command.Command()

	// Idempotency is the only lookup that intentionally precedes the current
	// proof and proposal reads. An exact retry must return the authoritative
	// durable result even after the active workspace proof has advanced. The
	// snapshot read is read-only and does not create an approval artifact.
	transactionSnapshot, err := s.transactions.Snapshot(ctx)
	if err != nil {
		return zero, approvalExecutionUnavailable(err)
	}
	if replay, found, replayErr := approvalExecutionReplay(transactionSnapshot, commandValue); found {
		if replayErr != nil {
			return zero, replayErr
		}
		target, targetErr := approvalExecutionReplayTarget(transactionSnapshot, commandValue)
		if targetErr != nil {
			return zero, approvalExecutionUnavailable(nil)
		}
		freshness, _ := s.projectApprovalFreshness(ctx, target)
		replay.Freshness = freshness
		return replay, nil
	}

	stored, err := LoadProposalForApproval(ctx, s.proposalStore, commandValue.WorkspaceID, commandValue.ProposalID, commandValue.EvidencePackID)
	if err != nil {
		if errors.Is(err, ErrProposalInvalid) {
			return zero, approvalExecutionInvalid(err)
		}
		return zero, approvalExecutionUnavailable(err)
	}
	if stored == nil || stored.Proposal == nil || stored.Pack == nil {
		return zero, approvalExecutionUnavailable(nil)
	}

	if s.activeStorage == nil {
		return zero, approvalExecutionUnavailable(nil)
	}
	bundle, err := s.activeStorage.ReadValidatedActiveProofBundle()
	if err != nil {
		return zero, approvalExecutionUnavailable(nil)
	}
	before, err := decodeApprovalExecutionMap(bundle)
	if err != nil {
		return zero, approvalExecutionUnavailable(nil)
	}
	if err := approvalExecutionProofMatchesCommand(bundle, before, commandValue, stored); err != nil {
		return zero, approvalExecutionConflict(err)
	}
	if err := approvalContextError(ctx); err != nil {
		return zero, err
	}

	current := findApprovalExecutionAggregate(transactionSnapshot, commandValue)
	aggregateID := approvalExecutionAggregateID(commandValue)
	if current != nil {
		aggregateID = current.AggregateID
	}
	var base ApprovalAggregateV2
	if current == nil {
		genesisID := approvalExecutionGenesisID(commandValue)
		base, err = NewApprovalGenesisAggregateV2(command, aggregateID, genesisID)
		if err != nil {
			return zero, approvalExecutionInvalid(err)
		}
	} else {
		base = cloneApprovalAggregateV2(*current)
	}

	metadata := ApprovalTransitionMetadata{
		EventID: approvalExecutionEventID(commandValue), ApprovalID: approvalExecutionApprovalID(commandValue),
		AggregateID: aggregateID, OccurredAt: time.Now().UTC(),
	}
	if commandValue.Decision == "approve" {
		metadata.StoredProposalText = stored.Proposal.ProposedTitle
	}
	event, aggregate, err := ReduceApprovalCommandV2(base, command, metadata)
	if err != nil {
		if errors.Is(err, ErrApprovalLifecycleConflict) || errors.Is(err, ErrApprovalLifecycleReferenceConflict) {
			return zero, approvalExecutionConflict(err)
		}
		return zero, approvalExecutionInvalid(err)
	}
	if event == nil || aggregate == nil {
		return zero, approvalExecutionInvalid(nil)
	}

	after := cloneApprovalMeaningMap(before)
	if after == nil {
		return zero, approvalExecutionUnavailable(nil)
	}
	// Approval only makes the already validated Q3 map available to the
	// presentation layer. All Q3 content, freshness, settlement, task, and
	// publication identities remain unchanged.
	after.EnrichmentStatus = "available"
	mutation, err := ValidateApprovalMeaningMutation(command, stored, before, after, event, aggregate)
	if err != nil {
		return zero, approvalExecutionInvalid(err)
	}
	receipt, err := s.transactions.Commit(ctx, mutation)
	if err != nil {
		if errors.Is(err, ErrApprovalTransactionConflict) || errors.Is(err, ErrApprovalTransactionWorkspaceLiveHeadConflict) {
			return zero, approvalExecutionConflict(err)
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return zero, err
		}
		return zero, approvalExecutionUnavailable(err)
	}
	target := approvalExecutionTargetForMap(commandValue, before)
	freshness, _ := s.projectApprovalFreshness(ctx, target)
	return ApprovalExecutionResult{
		Receipt: receipt, GenerationID: before.GenerationID, ComputedBasisID: before.ComputedBasisID,
		ValidatedSnapshotID: before.ValidatedAgainstSnapshotID, IntentRevision: int64(before.Task.IntentRevision), Freshness: freshness,
	}, nil
}

func approvalExecutionTargetForMap(command ApprovalCommandV2, mapIR *SemanticMapIR) approvalTransactionTarget {
	if mapIR == nil {
		return approvalTransactionTarget{}
	}
	return approvalTransactionTarget{
		WorkspaceID:         command.WorkspaceID,
		ProposalID:          command.ProposalID,
		EvidencePackID:      command.EvidencePackID,
		ComputedBasisID:     command.ComputedBasisID,
		GenerationID:        command.GenerationID,
		IntentRevision:      command.IntentRevision,
		ValidatedSnapshotID: mapIR.ValidatedAgainstSnapshotID,
		WorkspaceEpoch:      mapIR.Basis.WorkspaceEpoch,
		MapID:               mapIR.MapID,
		TaskID:              mapIR.Task.TaskID,
	}
}

func approvalExecutionReplayTarget(snapshot ApprovalTransactionSnapshot, command ApprovalCommandV2) (approvalTransactionTarget, error) {
	expected := approvalTransactionTarget{
		WorkspaceID: command.WorkspaceID, ProposalID: command.ProposalID, EvidencePackID: command.EvidencePackID,
		ComputedBasisID: command.ComputedBasisID, GenerationID: command.GenerationID, IntentRevision: command.IntentRevision,
	}
	var target *approvalTransactionTarget
	for index := range snapshot.targets {
		candidate := snapshot.targets[index].Target
		if !approvalTransactionTargetCoreEqual(candidate, expected) {
			continue
		}
		if target != nil {
			return approvalTransactionTarget{}, errors.New("duplicate approval execution target")
		}
		copy := candidate
		target = &copy
	}
	if target == nil {
		return approvalTransactionTarget{}, errors.New("approval execution target is unavailable")
	}
	return *target, nil
}

// projectApprovalFreshness is the shared read-only projection of a persisted
// approval target's relation to the currently validated active proof. It does
// not mutate the semantic map, transaction store, aggregate, event, outbox,
// or idempotency result. The live-head boundary is entered before the second
// active-proof read so a proof publication cannot race this relation check.
func (s *ApprovalExecutionService) projectApprovalFreshness(ctx context.Context, target approvalTransactionTarget) (string, error) {
	const historical = approvalExecutionFreshnessHistorical
	if s == nil || s.historyLiveHead == nil || s.activeStorage == nil || !validApprovalTransactionTarget(target) {
		return historical, nil
	}
	if err := approvalContextError(ctx); err != nil {
		return historical, err
	}
	workspaceID := approvalWorkspaceIDForCanonicalRoot(s.repoRoot)
	if !validApprovalOpaqueID(workspaceID, approvalWorkspaceIDMax) || target.WorkspaceID != workspaceID {
		return historical, nil
	}

	var preliminary storage.ValidatedActiveApprovalIdentity
	if err := s.activeStorage.WithValidatedActiveApprovalIdentity(func(identity storage.ValidatedActiveApprovalIdentity) error {
		preliminary = identity
		return nil
	}); err != nil {
		if contextErr := approvalContextError(ctx); contextErr != nil {
			return historical, contextErr
		}
		return historical, nil
	}
	if err := approvalContextError(ctx); err != nil {
		return historical, err
	}
	if preliminary.ExpectedLiveHeadSnapshotID == "" {
		return historical, nil
	}

	matched := false
	authorityErr := s.historyLiveHead(preliminary.ExpectedLiveHeadSnapshotID, func() error {
		if err := approvalContextError(ctx); err != nil {
			return err
		}
		// The storage identity reader validates the publication graph, while
		// the execution map reader additionally requires a current Q3 map.
		// Read both while the engine boundary is held so a historical or
		// otherwise corrupt active artifact can never project as current.
		bundle, err := s.activeStorage.ReadValidatedActiveProofBundle()
		if err != nil {
			return err
		}
		if _, err := decodeApprovalExecutionMap(bundle); err != nil {
			return err
		}
		return s.activeStorage.WithValidatedActiveApprovalIdentity(func(identity storage.ValidatedActiveApprovalIdentity) error {
			if err := approvalContextError(ctx); err != nil {
				return err
			}
			if identity.ExpectedLiveHeadSnapshotID != preliminary.ExpectedLiveHeadSnapshotID {
				return nil
			}
			matched = approvalExecutionActiveIdentityMatchesTarget(identity, target)
			return nil
		})
	})
	if authorityErr != nil {
		if contextErr := approvalContextError(ctx); contextErr != nil {
			return historical, contextErr
		}
		return historical, nil
	}
	if err := approvalContextError(ctx); err != nil {
		return historical, err
	}
	if matched {
		return approvalExecutionFreshnessCurrent, nil
	}
	return historical, nil
}

func approvalExecutionActiveIdentityMatchesTarget(identity storage.ValidatedActiveApprovalIdentity, target approvalTransactionTarget) bool {
	return target.ComputedBasisID == identity.ComputedBasisID &&
		target.GenerationID == identity.GenerationID &&
		target.ValidatedSnapshotID == identity.ValidatedSnapshotID &&
		target.MapID == identity.MapID &&
		target.TaskID == identity.TaskID &&
		target.IntentRevision == identity.IntentRevision &&
		target.WorkspaceEpoch == identity.WorkspaceEpoch
}

func approvalExecutionReplay(snapshot ApprovalTransactionSnapshot, command ApprovalCommandV2) (ApprovalExecutionResult, bool, error) {
	var zero ApprovalExecutionResult
	digest, err := approvalCommandIdempotencyDigest(command)
	if err != nil {
		return zero, false, approvalExecutionInvalid(nil)
	}
	wantDigest := "sha256:" + hex.EncodeToString(digest[:])
	for _, result := range snapshot.IdempotencyResults {
		if result.WorkspaceID != command.WorkspaceID || result.IdempotencyKey != command.IdempotencyKey {
			continue
		}
		if result.RequestDigest != wantDigest {
			return zero, true, approvalExecutionConflict(nil)
		}
		expectedTarget := approvalTransactionTarget{
			WorkspaceID: command.WorkspaceID, ProposalID: command.ProposalID, EvidencePackID: command.EvidencePackID,
			ComputedBasisID: command.ComputedBasisID, GenerationID: command.GenerationID, IntentRevision: command.IntentRevision,
		}
		var target *approvalTransactionTargetRecord
		for index := range snapshot.targets {
			candidate := snapshot.targets[index]
			if !approvalTransactionTargetCoreEqual(candidate.Target, expectedTarget) {
				continue
			}
			if target != nil {
				return zero, true, approvalExecutionUnavailable(nil)
			}
			target = cloneApprovalTransactionTargetRecord(&candidate)
		}
		if target == nil || !validApprovalTransactionTarget(target.Target) || !validApprovalTransactionTargetDigest(target.Target, target.TargetDigest) || !validApprovalLifecycleID(target.GenesisEventID) || !approvalTransactionTargetCoreEqual(target.Target, expectedTarget) || !approvalTransactionTargetAggregateIdentityMatches(target.Target, target.Aggregate) || target.Aggregate.AggregateID != result.AggregateID || result.AggregateVersion < 1 || result.AggregateVersion > target.Aggregate.Version {
			return zero, true, approvalExecutionUnavailable(nil)
		}
		state := approvalTransactionState{
			StoreVersion:       approvalTransactionStoreVersion,
			Events:             append([]ApprovalEventV2(nil), snapshot.Events...),
			Aggregates:         append([]ApprovalAggregateV2(nil), snapshot.Aggregates...),
			IdempotencyResults: append([]ApprovalIdempotencyResultV1(nil), snapshot.IdempotencyResults...),
			Outbox:             append([]ApprovalOutboxV1(nil), snapshot.Outbox...),
			Targets:            append([]approvalTransactionTargetRecord(nil), snapshot.targets...),
		}
		receipt := receiptFromApprovalTransactionState(state, result, true)
		if receipt.Event.EventID == "" || receipt.Aggregate.AggregateID == "" || receipt.Outbox.EventID == "" || receipt.IdempotencyResult.RequestDigest != wantDigest || receipt.Event.AggregateID != target.Aggregate.AggregateID || receipt.Event.AggregateVersion != result.AggregateVersion || receipt.Aggregate.Version != result.AggregateVersion || receipt.Aggregate.State != result.State || receipt.Outbox.AggregateID != target.Aggregate.AggregateID || receipt.Outbox.AggregateVersion != result.AggregateVersion || receipt.Outbox.WorkspaceID != target.Target.WorkspaceID {
			return zero, true, approvalExecutionUnavailable(nil)
		}
		return ApprovalExecutionResult{
			Receipt: receipt, GenerationID: target.Target.GenerationID, ComputedBasisID: target.Target.ComputedBasisID,
			// The replay helper has no proof/storage boundary. Execute replaces
			// this conservative value with the shared read-time projection before
			// returning a replay result.
			ValidatedSnapshotID: target.Target.ValidatedSnapshotID, IntentRevision: target.Target.IntentRevision, Freshness: approvalExecutionFreshnessHistorical,
		}, true, nil
	}
	return zero, false, nil
}

func validApprovalExecutionAccess(access ApprovalAccess, repoRoot string) bool {
	return access.actor.valid() && access.workspace.validFor(access.actor) && access.workspace.repoRoot == repoRoot && access.workspace.workspaceID == approvalWorkspaceIDForCanonicalRoot(repoRoot)
}

func decodeApprovalExecutionMap(bundle *storage.ValidatedActiveProofBundle) (*SemanticMapIR, error) {
	if bundle == nil || bundle.Manifest == nil || bundle.Pointer == nil || len(bundle.SemanticMap) == 0 {
		return nil, errors.New("active proof is incomplete")
	}
	if err := contractharness.ValidateSemanticMapIR(bundle.SemanticMap); err != nil {
		return nil, err
	}
	var before SemanticMapIR
	if err := decodeStrictJSON(bundle.SemanticMap, &before); err != nil {
		return nil, err
	}
	if err := validateApprovalMeaningMapContract(&before); err != nil {
		return nil, err
	}
	if before.Freshness != "current" || before.Quality.Stage != "Q3" || before.MapID == "" || before.GenerationID == "" || before.ComputedBasisID == "" || before.ValidatedAgainstSnapshotID == "" {
		return nil, errors.New("active proof map is not current Q3")
	}
	manifest, pointer := bundle.Manifest, bundle.Pointer
	if manifest.GenerationID != before.GenerationID || manifest.ComputedBasisID != before.ComputedBasisID || manifest.ComputedSnapshotID != before.ValidatedAgainstSnapshotID || manifest.TaskIntentRevision != before.Task.IntentRevision || manifest.WorkspaceEpoch != before.Basis.WorkspaceEpoch {
		return nil, errors.New("active proof map identity is inconsistent")
	}
	if pointer.GenerationID != before.GenerationID || pointer.ComputedBasisID != before.ComputedBasisID || pointer.ValidatedAgainstSnapshotID != before.ValidatedAgainstSnapshotID || pointer.TaskIntentRevision != before.Task.IntentRevision || pointer.WorkspaceEpoch != before.Basis.WorkspaceEpoch || pointer.TaskID != before.Task.TaskID {
		return nil, errors.New("active pointer map identity is inconsistent")
	}
	if pointer.ExpectedLiveHeadSnapshotID == "" || manifest.ExpectedLiveHeadSnapshotID != pointer.ExpectedLiveHeadSnapshotID {
		return nil, errors.New("active proof live-head identity is incomplete")
	}
	return &before, nil
}

func approvalExecutionProofMatchesCommand(bundle *storage.ValidatedActiveProofBundle, before *SemanticMapIR, command ApprovalCommandV2, stored *StoredProposal) error {
	if bundle == nil || bundle.Manifest == nil || bundle.Pointer == nil || before == nil || stored == nil || stored.Proposal == nil || stored.Pack == nil {
		return errors.New("approval execution proof is incomplete")
	}
	if command.ComputedBasisID != before.ComputedBasisID || command.GenerationID != before.GenerationID || command.IntentRevision != int64(before.Task.IntentRevision) {
		return errors.New("approval execution command is stale")
	}
	if stored.WorkspaceID != command.WorkspaceID || stored.Proposal.ProposalID != command.ProposalID || stored.Pack.EvidencePackID != command.EvidencePackID || stored.Proposal.ComputedBasisID != command.ComputedBasisID || stored.Proposal.GenerationID != command.GenerationID || stored.Pack.ComputedBasisID != command.ComputedBasisID || stored.Pack.GenerationID != command.GenerationID {
		return errors.New("approval execution proposal identity is inconsistent")
	}
	if stored.Proposal.SnapshotID != before.ValidatedAgainstSnapshotID || stored.Pack.SnapshotID != before.ValidatedAgainstSnapshotID {
		return errors.New("approval execution proposal snapshot is stale")
	}
	return nil
}

func findApprovalExecutionAggregate(snapshot ApprovalTransactionSnapshot, command ApprovalCommandV2) *ApprovalAggregateV2 {
	for index := range snapshot.Aggregates {
		aggregate := snapshot.Aggregates[index]
		if aggregate.WorkspaceID == command.WorkspaceID && aggregate.ProposalID == command.ProposalID && aggregate.EvidencePackID == command.EvidencePackID && aggregate.ComputedBasisID == command.ComputedBasisID && aggregate.GenerationID == command.GenerationID && aggregate.IntentRevision == command.IntentRevision {
			clone := cloneApprovalAggregateV2(aggregate)
			return &clone
		}
	}
	return nil
}

func approvalExecutionAggregateID(command ApprovalCommandV2) string {
	return approvalExecutionDerivedID("aggregate", command.WorkspaceID, command.ProposalID, command.EvidencePackID, command.ComputedBasisID, command.GenerationID, command.IntentRevision)
}

func approvalExecutionGenesisID(command ApprovalCommandV2) string {
	return approvalExecutionDerivedID("genesis", command.WorkspaceID, command.ProposalID, command.EvidencePackID, command.ComputedBasisID, command.GenerationID, command.IntentRevision)
}

func approvalExecutionEventID(command ApprovalCommandV2) string {
	digest, err := approvalCommandDigest(command)
	if err != nil {
		return ""
	}
	return "event-" + hex.EncodeToString(digest[:])
}

func approvalExecutionApprovalID(command ApprovalCommandV2) string {
	digest, err := approvalCommandDigest(command)
	if err != nil {
		return ""
	}
	return "approval-" + hex.EncodeToString(digest[:])
}

func approvalExecutionDerivedID(kind string, values ...interface{}) string {
	hash := sha256.New()
	hash.Write([]byte("codeflow/approval-execution/" + kind + "/v1\x00"))
	for _, value := range values {
		switch typed := value.(type) {
		case string:
			hash.Write([]byte(typed))
		case int64:
			hash.Write([]byte(typedString(typed)))
		}
		hash.Write([]byte{0})
	}
	return kind + "-" + hex.EncodeToString(hash.Sum(nil))
}

func typedString(value int64) string {
	return strconv.FormatInt(value, 10)
}
