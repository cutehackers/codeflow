package semantic

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"reflect"
	"strings"

	"codeflow/internal/contractharness"
	"codeflow/internal/fusion"
	"codeflow/internal/secret"
)

var (
	// ErrApprovalMeaningMutationInvalid identifies a proposed approval meaning
	// change whose stored proposal, Q3 map, lifecycle output, or Core-bound
	// authority cannot be verified as one immutable operation.
	ErrApprovalMeaningMutationInvalid = errors.New("approval meaning mutation invalid")
	// ErrApprovalMutationInvalid is a short compatibility name for the same
	// fail-closed semantic boundary.
	ErrApprovalMutationInvalid = ErrApprovalMeaningMutationInvalid
)

// ApprovalMeaningMutationError is intentionally bounded. It reports only the
// validation class and never includes proposal text, evidence content, actor
// identity, workspace paths, or other request values.
type ApprovalMeaningMutationError struct {
	Kind string
}

func (e *ApprovalMeaningMutationError) Error() string {
	if e == nil || strings.TrimSpace(e.Kind) == "" {
		return ErrApprovalMeaningMutationInvalid.Error()
	}
	return ErrApprovalMeaningMutationInvalid.Error() + ": " + e.Kind
}

func (e *ApprovalMeaningMutationError) Unwrap() error { return ErrApprovalMeaningMutationInvalid }

func newApprovalMeaningMutationError(kind string) error {
	if strings.TrimSpace(kind) == "" {
		kind = "validation"
	}
	return &ApprovalMeaningMutationError{Kind: kind}
}

// ApprovalMeaningMutationInput collects the Core-owned values that make one
// approval meaning operation auditable. Callers should use
// ValidateApprovalMeaningMutation for the explicit argument form.
type ApprovalMeaningMutationInput struct {
	Command        *ValidatedApprovalCommandV2
	StoredProposal *StoredProposal
	Before         *SemanticMapIR
	After          *SemanticMapIR
	Event          *ApprovalEventV2
	Aggregate      *ApprovalAggregateV2
}

// ApprovalMeaningEvidence contains only authority-preservation anchors. It
// deliberately excludes raw proposal rationale, Evidence items, map content,
// and all other potentially sensitive material.
type ApprovalMeaningEvidence struct {
	FactDigest       string `json:"factDigest"`
	ObligationDigest string `json:"obligationDigest"`
	AlignmentDigest  string `json:"alignmentDigest"`
	SettlementDigest string `json:"settlementDigest"`
	PackDigest       string `json:"packDigest"`
	Freshness        string `json:"freshness"`
	Settlement       string `json:"settlement"`
	ComputedBasisID  string `json:"computedBasisId"`
	GenerationID     string `json:"generationId"`
	IntentRevision   int64  `json:"intentRevision"`
}

// These aliases make the evidence's authority-preservation role explicit to
// consumers without introducing another representation or schema.
type ApprovalMeaningAuthorityEvidence = ApprovalMeaningEvidence
type ApprovalAuthorityPreservationEvidence = ApprovalMeaningEvidence

// ValidatedApprovalMutation is an immutable, Core-sealed approval meaning
// operation. Its unexported fields prevent a caller outside this package from
// manufacturing a persistence-ready value. Every accessor returns a fresh
// defensive copy.
type ValidatedApprovalMutation struct {
	command   *ValidatedApprovalCommandV2
	stored    StoredProposal
	before    *SemanticMapIR
	after     *SemanticMapIR
	event     ApprovalEventV2
	aggregate ApprovalAggregateV2
	evidence  ApprovalMeaningEvidence
	digest    [sha256.Size]byte
	sealed    bool
}

// ValidateApprovalMeaningMutation verifies one complete approval meaning
// operation and returns a sealed value suitable for a later persistence
// boundary. The current-state recheck/transaction remains the responsibility
// of that later boundary.
func ValidateApprovalMeaningMutation(command *ValidatedApprovalCommandV2, stored *StoredProposal, before, after *SemanticMapIR, event *ApprovalEventV2, aggregate *ApprovalAggregateV2) (*ValidatedApprovalMutation, error) {
	evidence, err := validateApprovalMeaningMutationInputs(command, stored, before, after, event, aggregate)
	if err != nil {
		return nil, err
	}
	commandCopy, err := cloneValidatedApprovalMeaningCommand(command)
	if err != nil {
		return nil, newApprovalMeaningMutationError("command")
	}
	storedCopy := cloneApprovalMeaningStoredProposal(stored)
	beforeCopy := cloneApprovalMeaningMap(before)
	afterCopy := cloneApprovalMeaningMap(after)
	if storedCopy == nil || beforeCopy == nil || afterCopy == nil || event == nil || aggregate == nil {
		return nil, newApprovalMeaningMutationError("input")
	}
	result := &ValidatedApprovalMutation{
		command:   commandCopy,
		stored:    *storedCopy,
		before:    beforeCopy,
		after:     afterCopy,
		event:     *event,
		aggregate: cloneApprovalAggregateV2(*aggregate),
		evidence:  evidence,
		sealed:    true,
	}
	result.digest, err = result.contentDigest()
	if err != nil {
		return nil, newApprovalMeaningMutationError("integrity")
	}
	if err := result.Validate(); err != nil {
		return nil, err
	}
	return result, nil
}

// ValidateApprovalMeaningMutationV2 is an explicit versioned spelling of the
// public guard.
func ValidateApprovalMeaningMutationV2(command *ValidatedApprovalCommandV2, stored *StoredProposal, before, after *SemanticMapIR, event *ApprovalEventV2, aggregate *ApprovalAggregateV2) (*ValidatedApprovalMutation, error) {
	return ValidateApprovalMeaningMutation(command, stored, before, after, event, aggregate)
}

// ValidateApprovalMeaningMutationInput validates the equivalent structured
// input form without creating a second validation path.
func ValidateApprovalMeaningMutationInput(input ApprovalMeaningMutationInput) (*ValidatedApprovalMutation, error) {
	return ValidateApprovalMeaningMutation(input.Command, input.StoredProposal, input.Before, input.After, input.Event, input.Aggregate)
}

// Validate rechecks all Core authority, Q3, proposal, lifecycle, and content
// seal invariants after an accessor or a package-internal caller may have
// attempted to mutate the operation.
func (m *ValidatedApprovalMutation) Validate() error {
	if m == nil || !m.sealed || m.command == nil {
		return newApprovalMeaningMutationError("integrity")
	}
	if err := m.command.Validate(); err != nil {
		return newApprovalMeaningMutationError("authority")
	}
	wantEvidence, err := validateApprovalMeaningMutationInputs(m.command, &m.stored, m.before, m.after, &m.event, &m.aggregate)
	if err != nil {
		return err
	}
	if wantEvidence != m.evidence {
		return newApprovalMeaningMutationError("integrity")
	}
	wantDigest, err := m.contentDigest()
	if err != nil || wantDigest != m.digest {
		return newApprovalMeaningMutationError("integrity")
	}
	return nil
}

// MarshalJSON intentionally refuses all JSON serialization. This value is a
// sealed input for the persistence boundary, not a public payload. In
// particular, command edited text and lifecycle approved text must never be
// exposed through an accidental generic JSON egress path.
func (m *ValidatedApprovalMutation) MarshalJSON() ([]byte, error) {
	return nil, newApprovalMeaningMutationError("egress")
}

// Command returns a defensive copy of the bound command's wire value.
func (m *ValidatedApprovalMutation) Command() ApprovalCommandV2 {
	if m == nil || m.command == nil {
		return ApprovalCommandV2{}
	}
	return m.command.Command()
}

// ValidatedCommand returns a fresh sealed command copy for a later internal
// boundary. A nil result means this operation is invalid or absent.
func (m *ValidatedApprovalMutation) ValidatedCommand() *ValidatedApprovalCommandV2 {
	if m == nil || m.Validate() != nil {
		return nil
	}
	clone, err := cloneValidatedApprovalMeaningCommand(m.command)
	if err != nil {
		return nil
	}
	return clone
}

// Access returns the defensive Core-bound access value used by the command.
func (m *ValidatedApprovalMutation) Access() ApprovalAccess {
	if m == nil || m.command == nil {
		return ApprovalAccess{}
	}
	return cloneApprovalMeaningAccess(m.command.Access())
}

// StoredProposal returns a deep copy of the verified proposal/pack pair.
func (m *ValidatedApprovalMutation) StoredProposal() *StoredProposal {
	if m == nil {
		return nil
	}
	return cloneApprovalMeaningStoredProposal(&m.stored)
}

// Stored is a concise alias for StoredProposal.
func (m *ValidatedApprovalMutation) Stored() *StoredProposal { return m.StoredProposal() }

// Proposal returns a defensive copy of the stored model proposal.
func (m *ValidatedApprovalMutation) Proposal() *ModelProposal {
	stored := m.StoredProposal()
	if stored == nil {
		return nil
	}
	return stored.Proposal
}

// Pack returns a defensive copy of the stored Evidence Pack.
func (m *ValidatedApprovalMutation) Pack() *EvidencePack {
	stored := m.StoredProposal()
	if stored == nil {
		return nil
	}
	return stored.Pack
}

// BeforeMap returns a deep defensive copy of the current map.
func (m *ValidatedApprovalMutation) BeforeMap() *SemanticMapIR {
	if m == nil {
		return nil
	}
	return cloneApprovalMeaningMap(m.before)
}

// Before is an alias for BeforeMap.
func (m *ValidatedApprovalMutation) Before() *SemanticMapIR { return m.BeforeMap() }

// AfterMap returns a deep defensive copy of the post-approval map.
func (m *ValidatedApprovalMutation) AfterMap() *SemanticMapIR {
	if m == nil {
		return nil
	}
	return cloneApprovalMeaningMap(m.after)
}

// After is an alias for AfterMap.
func (m *ValidatedApprovalMutation) After() *SemanticMapIR { return m.AfterMap() }

// Event returns a defensive copy of the lifecycle event.
func (m *ValidatedApprovalMutation) Event() *ApprovalEventV2 {
	if m == nil {
		return nil
	}
	copy := m.event
	return &copy
}

// ApprovalEvent is an explicit alias for Event.
func (m *ValidatedApprovalMutation) ApprovalEvent() *ApprovalEventV2 { return m.Event() }

// Aggregate returns a deep defensive copy of the lifecycle aggregate.
func (m *ValidatedApprovalMutation) Aggregate() *ApprovalAggregateV2 {
	if m == nil {
		return nil
	}
	copy := cloneApprovalAggregateV2(m.aggregate)
	return &copy
}

// ApprovalAggregate is an explicit alias for Aggregate.
func (m *ValidatedApprovalMutation) ApprovalAggregate() *ApprovalAggregateV2 { return m.Aggregate() }

// Evidence returns a value copy containing only authority-preservation
// anchors. It has no pointers or mutable slices.
func (m *ValidatedApprovalMutation) Evidence() ApprovalMeaningEvidence {
	if m == nil {
		return ApprovalMeaningEvidence{}
	}
	return m.evidence
}

// AuthorityEvidence is an explicit alias for Evidence.
func (m *ValidatedApprovalMutation) AuthorityEvidence() ApprovalMeaningEvidence { return m.Evidence() }

// MeaningEvidence is an explicit alias for Evidence.
func (m *ValidatedApprovalMutation) MeaningEvidence() ApprovalMeaningEvidence { return m.Evidence() }

type approvalMeaningMutationSealEnvelope struct {
	Command         ApprovalCommandV2
	StoredWorkspace string
	Proposal        *ModelProposal
	Pack            *EvidencePack
	Before          *SemanticMapIR
	After           *SemanticMapIR
	Event           ApprovalEventV2
	Aggregate       ApprovalAggregateV2
	Evidence        ApprovalMeaningEvidence
}

func (m *ValidatedApprovalMutation) contentDigest() ([sha256.Size]byte, error) {
	if m == nil {
		return [sha256.Size]byte{}, errors.New("nil approval meaning mutation")
	}
	command := ApprovalCommandV2{}
	if m.command != nil {
		command = m.command.Command()
	}
	data, err := json.Marshal(approvalMeaningMutationSealEnvelope{
		Command: command, StoredWorkspace: m.stored.WorkspaceID, Proposal: m.stored.Proposal, Pack: m.stored.Pack,
		Before: m.before, After: m.after, Event: m.event, Aggregate: m.aggregate, Evidence: m.evidence,
	})
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	return sha256.Sum256(append([]byte("codeflow/approval-meaning-mutation/v1\x00"), data...)), nil
}

func validateApprovalMeaningMutationInputs(command *ValidatedApprovalCommandV2, stored *StoredProposal, before, after *SemanticMapIR, event *ApprovalEventV2, aggregate *ApprovalAggregateV2) (ApprovalMeaningEvidence, error) {
	if command == nil {
		return ApprovalMeaningEvidence{}, newApprovalMeaningMutationError("command")
	}
	if err := command.Validate(); err != nil {
		return ApprovalMeaningEvidence{}, newApprovalMeaningMutationError("authority")
	}
	if stored == nil || stored.Proposal == nil || stored.Pack == nil {
		return ApprovalMeaningEvidence{}, newApprovalMeaningMutationError("stored-proposal")
	}
	if before == nil || after == nil {
		return ApprovalMeaningEvidence{}, newApprovalMeaningError("map")
	}
	if event == nil || aggregate == nil {
		return ApprovalMeaningEvidence{}, newApprovalMeaningError("lifecycle")
	}
	value := command.Command()
	if stored.WorkspaceID != value.WorkspaceID {
		return ApprovalMeaningEvidence{}, newApprovalMeaningError("authority")
	}
	if err := validateStoredProposalPair(stored.Proposal, stored.Pack); err != nil {
		return ApprovalMeaningEvidence{}, newApprovalMeaningError("stored-proposal")
	}
	if err := validateApprovalMeaningBeforeMap(value, stored, before); err != nil {
		return ApprovalMeaningEvidence{}, err
	}
	digests, err := CanonicalQ3Digests(before)
	if err != nil {
		return ApprovalMeaningEvidence{}, newApprovalMeaningError("q3")
	}
	proposalContext := ProposalValidationContext{
		Map: before, Pack: stored.Pack, TargetStepIDs: stored.Pack.TargetStepIDs, TargetSymbolPath: stored.Pack.TargetSymbolPath,
		ExpectedPackDigest: stored.Pack.PackDigest, ExpectedModelID: stored.Proposal.ModelID, ExpectedModelRevision: stored.Proposal.ModelRevision,
		ExpectedPromptRevision: stored.Proposal.PromptRevision, ExpectedSchemaProfile: SemanticProposalSchemaProfile,
	}
	if err := ValidateModelProposalV2(stored.Proposal, proposalContext); err != nil {
		return ApprovalMeaningEvidence{}, newApprovalMeaningError("proposal")
	}
	if stored.Proposal.FactDigest != digests.Fact || stored.Proposal.ObligationDigest != digests.Obligation || stored.Proposal.AlignmentDigest != digests.Alignment || stored.Proposal.SettlementDigest != digests.Settlement {
		return ApprovalMeaningEvidence{}, newApprovalMeaningError("q3")
	}
	if err := validateApprovalMeaningAfterMap(before, after, digests); err != nil {
		return ApprovalMeaningEvidence{}, err
	}
	if err := validateApprovalMeaningLifecycle(value, stored.Proposal, event, aggregate); err != nil {
		return ApprovalMeaningEvidence{}, err
	}
	evidence := ApprovalMeaningEvidence{
		FactDigest: digests.Fact, ObligationDigest: digests.Obligation, AlignmentDigest: digests.Alignment, SettlementDigest: digests.Settlement,
		PackDigest: stored.Pack.PackDigest, Freshness: before.Freshness, Settlement: before.Settlement,
		ComputedBasisID: before.ComputedBasisID, GenerationID: before.GenerationID, IntentRevision: int64(before.Task.IntentRevision),
	}
	return evidence, nil
}

func newApprovalMeaningError(kind string) error { return newApprovalMeaningMutationError(kind) }

func validateApprovalMeaningBeforeMap(command ApprovalCommandV2, stored *StoredProposal, before *SemanticMapIR) error {
	if err := validateApprovalMeaningMapContract(before); err != nil {
		return newApprovalMeaningError("map")
	}
	proposal, pack := stored.Proposal, stored.Pack
	if before.SchemaID != SemanticMapSchemaID || before.SchemaVersion != SemanticSchemaVersion || strings.TrimSpace(before.MapID) == "" || before.GenerationID == "" || before.ComputedBasisID == "" || before.ValidatedAgainstSnapshotID == "" {
		return newApprovalMeaningError("map")
	}
	if before.Freshness != "current" || before.Quality.Stage != "Q3" || before.GenerationID != command.GenerationID || before.ComputedBasisID != command.ComputedBasisID || before.ValidatedAgainstSnapshotID != proposal.SnapshotID {
		return newApprovalMeaningError("identity")
	}
	if before.EnrichmentStatus != "not_requested" && before.EnrichmentStatus != "pending" && before.EnrichmentStatus != "available" {
		return newApprovalMeaningError("status")
	}
	if before.GenerationID != proposal.GenerationID || before.ComputedBasisID != proposal.ComputedBasisID || before.ValidatedAgainstSnapshotID != pack.SnapshotID || pack.GenerationID != command.GenerationID || pack.ComputedBasisID != command.ComputedBasisID {
		return newApprovalMeaningError("identity")
	}
	if before.Task.IntentRevision < 1 || int64(before.Task.IntentRevision) != command.IntentRevision {
		return newApprovalMeaningError("intent")
	}
	if before.Basis.ComputedBasisID != before.ComputedBasisID || before.Basis.ComputedWorkspaceSnapshotID != before.ValidatedAgainstSnapshotID || before.Basis.RepositoryID != pack.RepositoryID || before.Basis.WorktreeID != pack.WorktreeID || before.Basis.WorkspaceEpoch != pack.WorkspaceEpoch {
		return newApprovalMeaningError("identity")
	}
	if proposal.ProposalID != command.ProposalID || pack.EvidencePackID != command.EvidencePackID || proposal.PackDigest != pack.PackDigest || proposal.TargetSymbolPath != pack.TargetSymbolPath {
		return newApprovalMeaningError("reference")
	}
	return nil
}

func validateApprovalMeaningAfterMap(before, after *SemanticMapIR, beforeDigests Q3CanonicalDigests) error {
	if err := validateApprovalMeaningMapContract(after); err != nil {
		return newApprovalMeaningError("map")
	}
	if after.SchemaID != before.SchemaID || after.SchemaVersion != before.SchemaVersion || after.MapID != before.MapID || after.GenerationID != before.GenerationID || after.ComputedBasisID != before.ComputedBasisID || after.ValidatedAgainstSnapshotID != before.ValidatedAgainstSnapshotID || after.Freshness != before.Freshness || after.Settlement != before.Settlement {
		return newApprovalMeaningError("identity")
	}
	if after.EnrichmentStatus != "available" {
		return newApprovalMeaningError("status")
	}
	if after.Task != before.Task || after.Basis != before.Basis || after.PublicationKind != before.PublicationKind || after.GenerationSequence != before.GenerationSequence || after.DerivationParent != before.DerivationParent || after.Supersedes != before.Supersedes || after.Authority != before.Authority || !reflect.DeepEqual(after.BoundaryTargets, before.BoundaryTargets) || !reflect.DeepEqual(after.Unknowns, before.Unknowns) || !reflect.DeepEqual(after.Coverage, before.Coverage) {
		return newApprovalMeaningError("identity")
	}
	beforeQuality, afterQuality := before.Quality, after.Quality
	beforeQuality.Stage, afterQuality.Stage = "", ""
	if !reflect.DeepEqual(beforeQuality, afterQuality) {
		return newApprovalMeaningError("q3")
	}
	if before.Quality.Stage != after.Quality.Stage {
		return newApprovalMeaningError("presentation")
	}
	afterDigests, err := CanonicalQ3Digests(after)
	if err != nil || afterDigests != beforeDigests {
		return newApprovalMeaningError("q3")
	}
	return nil
}

func validateApprovalMeaningMapContract(value *SemanticMapIR) error {
	if value == nil {
		return errors.New("semantic map is required")
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return contractharness.ValidateSemanticMapIR(raw)
}

func validApprovalMeaningEnrichmentStatus(status string) bool {
	switch status {
	case "not_requested", "pending", "available", "timed_out", "unavailable":
		return true
	default:
		return false
	}
}

func validateApprovalMeaningLifecycle(command ApprovalCommandV2, proposal *ModelProposal, event *ApprovalEventV2, aggregate *ApprovalAggregateV2) error {
	if err := validateApprovalLifecycleEvent(*event); err != nil {
		return newApprovalMeaningError("event")
	}
	if err := validateApprovalLifecycleAggregate(*aggregate); err != nil {
		return newApprovalMeaningError("aggregate")
	}
	if event.ActorID != command.ActorID || event.SessionID != command.SessionID || event.WorkspaceID != command.WorkspaceID || event.ProposalID != command.ProposalID || event.EvidencePackID != command.EvidencePackID || event.ComputedBasisID != command.ComputedBasisID || event.GenerationID != command.GenerationID || event.IntentRevision != command.IntentRevision || event.Decision != command.Decision {
		return newApprovalMeaningError("authority")
	}
	if aggregate.WorkspaceID != command.WorkspaceID || aggregate.ProposalID != command.ProposalID || aggregate.EvidencePackID != command.EvidencePackID || aggregate.ComputedBasisID != command.ComputedBasisID || aggregate.GenerationID != command.GenerationID || aggregate.IntentRevision != command.IntentRevision || aggregate.LastEventID != event.EventID || aggregate.LastDecision != command.Decision || aggregate.AggregateID != event.AggregateID || event.AggregateVersion != aggregate.Version {
		return newApprovalMeaningError("identity")
	}
	if command.ExpectedApprovalVersion < 0 || command.ExpectedApprovalVersion >= approvalLifecycleMaxVersion || aggregate.Version != command.ExpectedApprovalVersion+1 || len(aggregate.History) == 0 {
		return newApprovalMeaningError("version")
	}
	last := aggregate.History[len(aggregate.History)-1]
	if last.Version != aggregate.Version || last.State != aggregate.State || last.EventID != event.EventID || last.ApprovalID != event.ApprovalID || last.Decision != command.Decision {
		return newApprovalMeaningError("lifecycle")
	}
	wantState, wantRelation, ok := approvalMeaningTransitionState(command)
	if !ok || aggregate.State != wantState || event.LifecycleRelation != wantRelation {
		return newApprovalMeaningError("transition")
	}
	wantPredecessor := ""
	if command.PredecessorApprovalID != nil {
		wantPredecessor = *command.PredecessorApprovalID
	}
	if event.PredecessorApprovalID != wantPredecessor {
		return newApprovalMeaningError("reference")
	}
	wantText := ""
	switch command.Decision {
	case "approve":
		wantText = proposal.ProposedTitle
	case "edit_then_approve":
		if command.EditedText == nil {
			return newApprovalMeaningError("transition")
		}
		wantText = *command.EditedText
	}
	if event.ApprovedText != wantText {
		return newApprovalMeaningError("transition")
	}
	if redacted := secret.Redact(wantText); redacted.Count > 0 || redacted.Text != wantText {
		return newApprovalMeaningError("secret")
	}
	if redacted := secret.Redact(event.ApprovedText); redacted.Count > 0 || redacted.Text != event.ApprovedText {
		return newApprovalMeaningError("secret")
	}
	if aggregate.State == "active" && aggregate.ActiveApprovalID != event.ApprovalID {
		return newApprovalMeaningError("reference")
	}
	if aggregate.State != "active" && aggregate.ActiveApprovalID != "" {
		return newApprovalMeaningError("reference")
	}
	return nil
}

func approvalMeaningTransitionState(command ApprovalCommandV2) (state, relation string, ok bool) {
	switch command.Decision {
	case "approve":
		return "active", "initial", true
	case "edit_then_approve":
		return "active", "edit", true
	case "reject":
		return "rejected", "reject", true
	case "revoke":
		return "revoked", "revoke", true
	case "supersede":
		return "superseded", "supersede", true
	default:
		return "", "", false
	}
}

func cloneValidatedApprovalMeaningCommand(command *ValidatedApprovalCommandV2) (*ValidatedApprovalCommandV2, error) {
	if command == nil || command.Validate() != nil {
		return nil, errors.New("invalid command")
	}
	value := command.Command()
	access := cloneApprovalMeaningAccess(command.Access())
	digest, err := approvalCommandDigest(value)
	if err != nil {
		return nil, err
	}
	clone := &ValidatedApprovalCommandV2{command: value, access: access, digest: digest}
	if clone.Validate() != nil {
		return nil, errors.New("invalid command clone")
	}
	return clone, nil
}

func cloneApprovalMeaningAccess(access ApprovalAccess) ApprovalAccess {
	actor := access.actor
	if actor.seal != nil {
		seal := *actor.seal
		actor.seal = &seal
	}
	workspace := access.workspace
	if workspace.seal != nil {
		seal := *workspace.seal
		seal.actor = actor
		workspace.seal = &seal
	}
	return ApprovalAccess{actor: actor, workspace: workspace}
}

func cloneApprovalMeaningStoredProposal(stored *StoredProposal) *StoredProposal {
	if stored == nil {
		return nil
	}
	clone := *stored
	clone.Proposal = cloneModelProposal(stored.Proposal)
	clone.Pack = cloneEvidencePack(stored.Pack)
	return &clone
}

func cloneApprovalMeaningMap(value *SemanticMapIR) *SemanticMapIR {
	if value == nil {
		return nil
	}
	clone := *value
	clone.Quality = cloneApprovalMeaningQuality(value.Quality)
	if value.Steps != nil {
		clone.Steps = make([]SemanticStep, len(value.Steps))
		for i, step := range value.Steps {
			clone.Steps[i] = step
			clone.Steps[i].Rules = append([]string(nil), step.Rules...)
			clone.Steps[i].EvidenceRefs = append([]string(nil), step.EvidenceRefs...)
			if step.CodeLens != nil {
				lens := *step.CodeLens
				clone.Steps[i].CodeLens = &lens
			}
			if step.StateDelta != nil {
				delta := *step.StateDelta
				clone.Steps[i].StateDelta = &delta
			}
			if step.SideEffect != nil {
				text := *step.SideEffect
				clone.Steps[i].SideEffect = &text
			}
			if step.Branch != nil {
				branch := *step.Branch
				clone.Steps[i].Branch = &branch
			}
			if step.Anchor.SymbolRange != nil {
				rangeCopy := *step.Anchor.SymbolRange
				clone.Steps[i].Anchor.SymbolRange = &rangeCopy
			}
		}
	}
	if value.Edges != nil {
		clone.Edges = make([]SemanticEdge, len(value.Edges))
		copy(clone.Edges, value.Edges)
	}
	if value.BoundaryTargets != nil {
		clone.BoundaryTargets = append([]string{}, value.BoundaryTargets...)
	}
	if value.RequirementAlignment != nil {
		clone.RequirementAlignment = make([]RequirementAlignment, len(value.RequirementAlignment))
		for i, alignment := range value.RequirementAlignment {
			clone.RequirementAlignment[i] = alignment
			clone.RequirementAlignment[i].CoveredStepRefs = append([]string(nil), alignment.CoveredStepRefs...)
			clone.RequirementAlignment[i].EvidenceRefs = append([]string(nil), alignment.EvidenceRefs...)
			clone.RequirementAlignment[i].MissingEvidence = append([]string(nil), alignment.MissingEvidence...)
			clone.RequirementAlignment[i].MissingTests = append([]string(nil), alignment.MissingTests...)
			clone.RequirementAlignment[i].MissingContracts = append([]string(nil), alignment.MissingContracts...)
			clone.RequirementAlignment[i].MissingRuntime = append([]string(nil), alignment.MissingRuntime...)
		}
	}
	if value.Evidence != nil {
		clone.Evidence = make([]SemanticEvidence, len(value.Evidence))
		for i, evidence := range value.Evidence {
			clone.Evidence[i] = evidence
			if evidence.Anchor.SymbolRange != nil {
				rangeCopy := *evidence.Anchor.SymbolRange
				clone.Evidence[i].Anchor.SymbolRange = &rangeCopy
			}
			if evidence.Producer != nil {
				producer := *evidence.Producer
				clone.Evidence[i].Producer = &producer
			}
		}
	}
	if value.Unknowns != nil {
		clone.Unknowns = append([]fusion.Unknown{}, value.Unknowns...)
	}
	if value.Coverage != nil {
		coverage := *value.Coverage
		if value.Coverage.IncludedSourceRoots != nil {
			coverage.IncludedSourceRoots = append([]string{}, value.Coverage.IncludedSourceRoots...)
		}
		if value.Coverage.ExcludedReasons != nil {
			coverage.ExcludedReasons = append([]string{}, value.Coverage.ExcludedReasons...)
		}
		clone.Coverage = &coverage
	}
	return &clone
}

func cloneApprovalMeaningQuality(value MapQuality) MapQuality {
	clone := value
	if value.CriticalObligations != nil {
		clone.CriticalObligations = make([]CriticalObligation, len(value.CriticalObligations))
		for i, obligation := range value.CriticalObligations {
			clone.CriticalObligations[i] = obligation
			clone.CriticalObligations[i].EvidenceRefs = append([]string(nil), obligation.EvidenceRefs...)
		}
	}
	if value.CriticalCoverageSummary != nil {
		coverage := *value.CriticalCoverageSummary
		clone.CriticalCoverageSummary = &coverage
	}
	if value.Degradations != nil {
		clone.Degradations = make([]QualityDegradation, len(value.Degradations))
		for i, degradation := range value.Degradations {
			clone.Degradations[i] = degradation
			clone.Degradations[i].ScopeRefs = append([]string(nil), degradation.ScopeRefs...)
		}
	}
	return clone
}
