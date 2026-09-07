package semantic

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"codeflow/internal/contractharness"
	"codeflow/internal/fusion"
)

func TestValidateApprovalMeaningMutationAcceptsStoredQ3BoundResult(t *testing.T) {
	command, stored, before, after, event, aggregate := approvalMeaningMutationTestInputs(t)
	validated, err := ValidateApprovalMeaningMutation(command, stored, before, after, event, aggregate)
	if err != nil {
		t.Fatalf("valid approval meaning mutation rejected: %v", err)
	}
	if validated == nil {
		t.Fatal("valid approval meaning mutation returned nil")
	}
	if err := validated.Validate(); err != nil {
		t.Fatalf("validated approval meaning mutation failed validation: %v", err)
	}
}

func TestValidateApprovalMeaningMutationAcceptsEveryLifecycleDecision(t *testing.T) {
	for _, decision := range []string{"approve", "edit_then_approve", "reject", "revoke", "supersede"} {
		t.Run(decision, func(t *testing.T) {
			command, stored, before, after, event, aggregate := approvalMeaningMutationTestInputsForDecision(t, decision)
			validated, err := ValidateApprovalMeaningMutation(command, stored, before, after, event, aggregate)
			if err != nil || validated == nil {
				t.Fatalf("decision %s validation = %v/%v", decision, validated, err)
			}
			if err := validated.Validate(); err != nil {
				t.Fatalf("decision %s sealed validation: %v", decision, err)
			}
		})
	}
}

func TestValidateApprovalMeaningMutationRejectsQ3AndLifecycleDrift(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ValidatedApprovalCommandV2, *StoredProposal, *SemanticMapIR, *SemanticMapIR, *ApprovalEventV2, *ApprovalAggregateV2)
	}{
		{name: "before fact", mutate: func(_ *ValidatedApprovalCommandV2, _ *StoredProposal, before, _ *SemanticMapIR, _ *ApprovalEventV2, _ *ApprovalAggregateV2) {
			before.Steps[0].Name = "changed fact"
		}},
		{name: "after fact", mutate: func(_ *ValidatedApprovalCommandV2, _ *StoredProposal, _, after *SemanticMapIR, _ *ApprovalEventV2, _ *ApprovalAggregateV2) {
			after.Steps[0].Name = "changed fact"
		}},
		{name: "before freshness", mutate: func(_ *ValidatedApprovalCommandV2, _ *StoredProposal, before, _ *SemanticMapIR, _ *ApprovalEventV2, _ *ApprovalAggregateV2) {
			before.Freshness = "historical"
		}},
		{name: "after settlement", mutate: func(_ *ValidatedApprovalCommandV2, _ *StoredProposal, _, after *SemanticMapIR, _ *ApprovalEventV2, _ *ApprovalAggregateV2) {
			after.Settlement = "failed"
		}},
		{name: "proposal q3 digest", mutate: func(_ *ValidatedApprovalCommandV2, stored *StoredProposal, _, _ *SemanticMapIR, _ *ApprovalEventV2, _ *ApprovalAggregateV2) {
			stored.Proposal.FactDigest = "0" + stored.Proposal.FactDigest[1:]
		}},
		{name: "pack item", mutate: func(_ *ValidatedApprovalCommandV2, stored *StoredProposal, _, _ *SemanticMapIR, _ *ApprovalEventV2, _ *ApprovalAggregateV2) {
			stored.Pack.Items[0].Content = "mutated content"
		}},
		{name: "event authority", mutate: func(_ *ValidatedApprovalCommandV2, _ *StoredProposal, _, _ *SemanticMapIR, event *ApprovalEventV2, _ *ApprovalAggregateV2) {
			event.ActorID = "other-actor"
		}},
		{name: "aggregate version", mutate: func(_ *ValidatedApprovalCommandV2, _ *StoredProposal, _, _ *SemanticMapIR, _ *ApprovalEventV2, aggregate *ApprovalAggregateV2) {
			aggregate.Version++
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			command, stored, before, after, event, aggregate := approvalMeaningMutationTestInputs(t)
			test.mutate(command, stored, before, after, event, aggregate)
			validated, err := ValidateApprovalMeaningMutation(command, stored, before, after, event, aggregate)
			if err == nil || validated != nil {
				t.Fatalf("drift %s accepted: result=%#v err=%v", test.name, validated, err)
			}
			assertApprovalMeaningInvalid(t, err)
		})
	}
}

func TestValidateApprovalMeaningMutationRejectsSecretApprovedMeaning(t *testing.T) {
	tests := []struct {
		name   string
		secret string
		setup  func(*ValidatedApprovalCommandV2, *StoredProposal, *ApprovalEventV2)
	}{
		{
			name:   "approve proposed title key value",
			secret: `apiKey: "approval-secret"`,
			setup: func(_ *ValidatedApprovalCommandV2, stored *StoredProposal, event *ApprovalEventV2) {
				stored.Proposal.ProposedTitle = `apiKey: "approval-secret"`
				event.ApprovedText = stored.Proposal.ProposedTitle
			},
		},
		{
			name:   "approve proposed title JSON key value",
			secret: `{"clientSecret":"approval-secret"}`,
			setup: func(_ *ValidatedApprovalCommandV2, stored *StoredProposal, event *ApprovalEventV2) {
				stored.Proposal.ProposedTitle = `{"clientSecret":"approval-secret"}`
				event.ApprovedText = stored.Proposal.ProposedTitle
			},
		},
		{
			name:   "edit then approve edited text",
			secret: `{"password":"approval-secret"}`,
			setup: func(command *ValidatedApprovalCommandV2, _ *StoredProposal, event *ApprovalEventV2) {
				edited := `{"password":"approval-secret"}`
				command.command.EditedText = &edited
				digest, err := approvalCommandDigest(command.command)
				if err != nil {
					t.Fatalf("seal edited command: %v", err)
				}
				command.digest = digest
				event.ApprovedText = edited
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			decision := "approve"
			if strings.HasPrefix(test.name, "edit") {
				decision = "edit_then_approve"
			}
			command, stored, before, after, event, aggregate := approvalMeaningMutationTestInputsForDecision(t, decision)
			test.setup(command, stored, event)
			validated, err := ValidateApprovalMeaningMutation(command, stored, before, after, event, aggregate)
			if err == nil || validated != nil {
				t.Fatalf("secret-bearing meaning accepted: result=%#v err=%v", validated, err)
			}
			assertApprovalMeaningInvalid(t, err)
			if strings.Contains(err.Error(), test.secret) {
				t.Fatalf("secret-bearing error leaked source text: %v", err)
			}
		})
	}
}

func TestValidatedApprovalMeaningMutationMarshalJSONFailsClosed(t *testing.T) {
	command, stored, before, after, event, aggregate := approvalMeaningMutationTestInputs(t)
	validated, err := ValidateApprovalMeaningMutation(command, stored, before, after, event, aggregate)
	if err != nil {
		t.Fatalf("valid mutation: %v", err)
	}
	raw, err := json.Marshal(validated)
	if err == nil {
		t.Fatalf("sealed mutation unexpectedly marshaled: %s", raw)
	}
	assertApprovalMeaningInvalid(t, err)
	if strings.Contains(string(raw), stored.Proposal.ProposedTitle) {
		t.Fatalf("marshal error returned raw approved meaning: %s", raw)
	}
}

func TestValidatedApprovalMeaningMutationSecretCannotEnterThroughCopies(t *testing.T) {
	command, stored, before, after, event, aggregate := approvalMeaningMutationTestInputs(t)
	validated, err := ValidateApprovalMeaningMutation(command, stored, before, after, event, aggregate)
	if err != nil {
		t.Fatalf("valid mutation: %v", err)
	}
	secretText := `token: "approval-secret"`

	stored.Proposal.ProposedTitle = secretText
	event.ApprovedText = secretText
	if err := validated.Validate(); err != nil {
		t.Fatalf("caller source mutation changed sealed result: %v", err)
	}
	if got := validated.Proposal().ProposedTitle; got == secretText {
		t.Fatal("caller source secret entered sealed proposal")
	}

	proposal := validated.Proposal()
	proposal.ProposedTitle = secretText
	accessorEvent := validated.Event()
	accessorEvent.ApprovedText = secretText
	if err := validated.Validate(); err != nil {
		t.Fatalf("accessor secret mutation changed sealed result: %v", err)
	}

	validated.event.ApprovedText = secretText
	err = validated.Validate()
	assertApprovalMeaningInvalid(t, err)
	if strings.Contains(err.Error(), secretText) {
		t.Fatalf("tamper error leaked approved meaning: %v", err)
	}
}

func TestValidateApprovalMeaningMutationRejectsNonCanonicalMapState(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*SemanticMapIR, *SemanticMapIR)
	}{
		{name: "before schema contract", mutate: func(before, _ *SemanticMapIR) {
			before.Basis.DependencyFingerprint = ""
		}},
		{name: "after schema contract", mutate: func(_ *SemanticMapIR, after *SemanticMapIR) {
			after.Authority = "invalid"
		}},
		{name: "before empty stage", mutate: func(before, _ *SemanticMapIR) {
			before.Quality.Stage = ""
		}},
		{name: "before q1 stage", mutate: func(before, _ *SemanticMapIR) {
			before.Quality.Stage = "Q1"
		}},
		{name: "before q2 stage", mutate: func(before, _ *SemanticMapIR) {
			before.Quality.Stage = "Q2"
		}},
		{name: "before q4 stage", mutate: func(before, _ *SemanticMapIR) {
			before.Quality.Stage = "Q4"
		}},
		{name: "before non-current status", mutate: func(before, _ *SemanticMapIR) {
			before.EnrichmentStatus = "timed_out"
		}},
		{name: "after q4 stage", mutate: func(_ *SemanticMapIR, after *SemanticMapIR) {
			after.Quality.Stage = "Q4"
		}},
		{name: "after q2 stage", mutate: func(_ *SemanticMapIR, after *SemanticMapIR) {
			after.Quality.Stage = "Q2"
		}},
		{name: "after pending status", mutate: func(_, after *SemanticMapIR) {
			after.EnrichmentStatus = "pending"
		}},
		{name: "after unavailable status", mutate: func(_, after *SemanticMapIR) {
			after.EnrichmentStatus = "unavailable"
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			command, stored, before, after, event, aggregate := approvalMeaningMutationTestInputs(t)
			test.mutate(before, after)
			if strings.HasPrefix(test.name, "after ") {
				if before.Quality.Stage != "Q3" {
					t.Fatalf("after-only mutation changed before quality stage to %q", before.Quality.Stage)
				}
				if err := validateApprovalMeaningMapContract(before); err != nil {
					t.Fatalf("after-only mutation changed the valid before map: %v", err)
				}
			}
			if test.name == "after schema contract" {
				if err := validateApprovalMeaningMapContract(after); err == nil {
					t.Fatal("after schema mutation unexpectedly passed the canonical map contract")
				}
			}
			validated, err := ValidateApprovalMeaningMutation(command, stored, before, after, event, aggregate)
			if err == nil || validated != nil {
				t.Fatalf("non-canonical map state %s accepted: result=%#v err=%v", test.name, validated, err)
			}
			assertApprovalMeaningInvalid(t, err)
		})
	}
}

func TestApprovalMeaningMutationFixtureMapsPassCanonicalContract(t *testing.T) {
	_, _, before, after, _, _ := approvalMeaningMutationTestInputs(t)
	for _, mapIR := range []*SemanticMapIR{before, after} {
		data, err := json.Marshal(mapIR)
		if err != nil {
			t.Fatalf("marshal fixture map: %v", err)
		}
		if err := contractharness.ValidateSemanticMapIR(data); err != nil {
			t.Fatalf("fixture map failed canonical contract: %v", err)
		}
	}
}

func TestValidatedApprovalMeaningMutationDefendsCopiesAndSeal(t *testing.T) {
	command, stored, before, after, event, aggregate := approvalMeaningMutationTestInputs(t)
	validated, err := ValidateApprovalMeaningMutation(command, stored, before, after, event, aggregate)
	if err != nil {
		t.Fatalf("valid mutation: %v", err)
	}
	stored.Proposal.ProposedTitle = "caller mutation"
	stored.Pack.Items[0].Content = "caller mutation"
	before.Steps[0].Name = "caller mutation"
	after.Steps[0].Name = "caller mutation"
	event.ApprovedText = "caller mutation"
	aggregate.History[0].EventID = "caller mutation"
	if err := validated.Validate(); err != nil {
		t.Fatalf("source mutation changed sealed result: %v", err)
	}

	proposal := validated.Proposal()
	proposal.ProposedTitle = "accessor mutation"
	pack := validated.Pack()
	pack.Items[0].Content = "accessor mutation"
	mapBefore := validated.BeforeMap()
	mapBefore.Steps[0].Name = "accessor mutation"
	if mapBefore.Steps[0].CodeLens != nil {
		mapBefore.Steps[0].CodeLens.Path = "accessor mutation"
	}
	mapAfter := validated.AfterMap()
	mapAfter.Summary.Current = "accessor mutation"
	gotEvent := validated.Event()
	gotEvent.ApprovedText = "accessor mutation"
	gotAggregate := validated.Aggregate()
	gotAggregate.History[0].EventID = "accessor mutation"
	gotCommand := validated.Command()
	gotCommand.CommandID = "accessor mutation"
	if err := validated.Validate(); err != nil {
		t.Fatalf("accessor mutation changed sealed result: %v", err)
	}

	rawEvidence, err := json.Marshal(validated.Evidence())
	if err != nil {
		t.Fatalf("marshal authority evidence: %v", err)
	}
	if strings.Contains(string(rawEvidence), "Submit checkout") || strings.Contains(string(rawEvidence), "caller mutation") || strings.Contains(string(rawEvidence), "e-a03") {
		t.Fatalf("authority evidence contains raw meaning/content: %s", rawEvidence)
	}
	if _, err := json.Marshal(validated); err == nil {
		t.Fatal("sealed mutation unexpectedly exposed a JSON payload")
	} else {
		assertApprovalMeaningInvalid(t, err)
	}

	validated.event.ApprovedText = "tampered"
	assertApprovalMeaningInvalid(t, validated.Validate())
	if _, err := json.Marshal(validated); err == nil {
		t.Fatal("tampered mutation unexpectedly marshaled")
	}
}

func approvalMeaningMutationTestInputs(t *testing.T) (*ValidatedApprovalCommandV2, *StoredProposal, *SemanticMapIR, *SemanticMapIR, *ApprovalEventV2, *ApprovalAggregateV2) {
	return approvalMeaningMutationTestInputsForDecision(t, "approve")
}

func approvalMeaningMutationTestInputsForDecision(t *testing.T, decision string) (*ValidatedApprovalCommandV2, *StoredProposal, *SemanticMapIR, *SemanticMapIR, *ApprovalEventV2, *ApprovalAggregateV2) {
	return approvalMeaningMutationTestInputsForDecisionWithAccess(t, decision, approvalCommandTestAccess(t))
}

func approvalMeaningMutationTestInputsForDecisionWithAccess(t *testing.T, decision string, access ApprovalAccess) (*ValidatedApprovalCommandV2, *StoredProposal, *SemanticMapIR, *SemanticMapIR, *ApprovalEventV2, *ApprovalAggregateV2) {
	t.Helper()
	snapshot, before, request := enrichmentTestInput(t)
	before.PublicationKind = "initial"
	before.Settlement = "pending"
	before.EnrichmentStatus = "available"
	before.Quality.Stage = "Q3"
	before.Quality.UnresolvedCriticalCount = 0
	before.Quality.ConflictingCriticalCount = 0
	before.Basis.DependencyFingerprint = "dependency-meaning"
	before.Basis.AnalysisReadSetID = "read-set-meaning"
	before.Basis.CausalObservationClosureID = "closure-meaning"
	before.Edges = []SemanticEdge{}
	before.Unknowns = []fusion.Unknown{}
	before.Coverage = &CoverageBoundary{IncludedSourceRoots: []string{"."}, ExcludedReasons: []string{"outside-scope"}}
	before.Authority = "candidate"
	before.Steps[0].StructuralIdentity = "Submit"
	before.Steps[0].Ordinal = 1
	before.Steps[0].Anchor.SpanHash = before.Steps[0].Anchor.FileHash
	before.Evidence[0].DocumentRevisionID = "revision-meaning"
	before.Evidence[0].LineRange = [2]int{1, 1}
	request = bindCurrentEvidenceRequest(snapshot, before, "step-a03")
	pack, err := BuildEvidencePackV2(request)
	if err != nil {
		t.Fatalf("build evidence pack: %v", err)
	}
	digests, err := CanonicalQ3Digests(before)
	if err != nil {
		t.Fatalf("Q3 digests: %v", err)
	}
	proposal := &ModelProposal{
		SchemaID: SemanticProposalV2SchemaID, SchemaVersion: 2, ProposalID: "proposal-meaning",
		ComputedBasisID: before.ComputedBasisID, GenerationID: before.GenerationID, SnapshotID: snapshot.SnapshotID,
		TargetStepID: "step-a03", TargetSymbolPath: "Submit", ProposedTitle: "Submit checkout",
		ProposedCategory: "entry", EpistemicStatus: "inferred", Authority: "model", ClaimScope: "display_only",
		ModelID: "meaning-model", ModelRevision: "r1", PromptRevision: "prompt-meaning", SchemaProfile: SemanticProposalSchemaProfile, PackDigest: pack.PackDigest,
		EvidenceRefs: []string{"e-a03"}, FactDigest: digests.Fact, ObligationDigest: digests.Obligation,
		AlignmentDigest: digests.Alignment, SettlementDigest: digests.Settlement,
	}
	stored := &StoredProposal{WorkspaceID: access.Workspace().WorkspaceID(), Proposal: proposal, Pack: pack}
	draft := approvalCommandTestDraft()
	draft.CommandID = "command-meaning-" + decision
	draft.ProposalID = proposal.ProposalID
	draft.EvidencePackID = pack.EvidencePackID
	draft.ComputedBasisID = before.ComputedBasisID
	draft.GenerationID = before.GenerationID
	draft.IntentRevision = int64(before.Task.IntentRevision)
	draft.Decision = decision
	var predecessor string
	if decision == "approve" || decision == "reject" {
		draft.ExpectedState = "none"
		draft.ExpectedApprovalVersion = 0
	} else {
		draft.ExpectedState = "active"
		draft.ExpectedApprovalVersion = 1
		predecessor = "approval-meaning-approve"
		draft.PredecessorApprovalID = &predecessor
	}
	if decision == "edit_then_approve" {
		edited := "edited meaning"
		draft.EditedText = &edited
	}
	command, err := BindApprovalCommandV2(access, draft)
	if err != nil {
		t.Fatalf("bind approval command: %v", err)
	}
	genesis, err := NewApprovalGenesisAggregateV2(command, "aggregate-meaning", "event-meaning-genesis")
	if decision == "approve" || decision == "reject" {
		if err != nil {
			t.Fatalf("approval genesis: %v", err)
		}
		metadata := lifecycleTestMetadata(genesis.AggregateID, "event-meaning-"+decision, "approval-meaning-"+decision)
		if decision == "approve" {
			metadata.StoredProposalText = proposal.ProposedTitle
		}
		event, aggregate, reduceErr := ReduceApprovalCommandV2(genesis, command, metadata)
		if reduceErr != nil {
			t.Fatalf("approval transition: %v", reduceErr)
		}
		after := cloneSemanticMap(before)
		after.EnrichmentStatus = "available"
		return command, stored, before, after, event, aggregate
	}

	// Active-state transitions use a separate initial command and aggregate.
	initialDraft := approvalCommandTestDraft()
	initialDraft.CommandID = "command-meaning-initial"
	initialDraft.ProposalID = proposal.ProposalID
	initialDraft.EvidencePackID = pack.EvidencePackID
	initialDraft.ComputedBasisID = before.ComputedBasisID
	initialDraft.GenerationID = before.GenerationID
	initialDraft.IntentRevision = int64(before.Task.IntentRevision)
	initial, err := BindApprovalCommandV2(access, initialDraft)
	if err != nil {
		t.Fatalf("bind initial command: %v", err)
	}
	genesis, err = NewApprovalGenesisAggregateV2(initial, "aggregate-meaning", "event-meaning-genesis")
	if err != nil {
		t.Fatalf("approval genesis: %v", err)
	}
	initialMetadata := lifecycleTestMetadata(genesis.AggregateID, "event-meaning-initial", "approval-meaning-approve")
	initialMetadata.StoredProposalText = proposal.ProposedTitle
	_, active, err := ReduceApprovalCommandV2(genesis, initial, initialMetadata)
	if err != nil {
		t.Fatalf("initial approval transition: %v", err)
	}
	metadata := lifecycleTestMetadata(active.AggregateID, "event-meaning-"+decision, "approval-meaning-"+decision)
	event, aggregate, err := ReduceApprovalCommandV2(*active, command, metadata)
	if err != nil {
		t.Fatalf("approval transition: %v", err)
	}
	after := cloneSemanticMap(before)
	after.EnrichmentStatus = "available"
	return command, stored, before, after, event, aggregate
}

func assertApprovalMeaningInvalid(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected approval meaning mutation error")
	}
	if !errors.Is(err, ErrApprovalMeaningMutationInvalid) {
		t.Fatalf("error = %v, want ErrApprovalMeaningMutationInvalid", err)
	}
	var typed *ApprovalMeaningMutationError
	if !errors.As(err, &typed) {
		t.Fatalf("error type = %T, want ApprovalMeaningMutationError", err)
	}
}
