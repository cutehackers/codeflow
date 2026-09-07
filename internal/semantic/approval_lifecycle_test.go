package semantic

import (
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"codeflow/internal/contractharness"
)

func TestReduceApprovalCommandV2GenesisApprove(t *testing.T) {
	command, access := lifecycleTestCommand(t, "approve", "none", 0, nil, nil)
	genesis, err := NewApprovalGenesisAggregateV2(command, "aggregate-1", "event-genesis")
	if err != nil {
		t.Fatalf("NewApprovalGenesisAggregateV2: %v", err)
	}
	metadata := lifecycleTestMetadata("aggregate-1", "event-approve", "approval-approve")
	metadata.StoredProposalText = "stored approved proposal"
	event, next, err := ReduceApprovalCommandV2(genesis, command, metadata)
	if err != nil {
		t.Fatalf("ReduceApprovalCommandV2: %v", err)
	}
	if event == nil || next == nil {
		t.Fatalf("successful reduce returned event=%v aggregate=%v", event != nil, next != nil)
	}
	if event.SchemaID != contractharness.ApprovalEventV2SchemaID || event.SchemaVersion != 2 {
		t.Fatalf("event schema identity = %q/%d", event.SchemaID, event.SchemaVersion)
	}
	if event.EventID != metadata.EventID || event.ApprovalID != metadata.ApprovalID || event.AggregateID != metadata.AggregateID || event.AggregateVersion != 1 {
		t.Fatalf("event identity/version = %#v", event)
	}
	if event.Decision != "approve" || event.LifecycleRelation != "initial" || event.ApprovedText != metadata.StoredProposalText || event.PredecessorApprovalID != "" {
		t.Fatalf("event transition = %#v", event)
	}
	lifecycleAssertEventAuthority(t, event, command, access)
	if next.SchemaID != contractharness.ApprovalAggregateV2SchemaID || next.SchemaVersion != 2 || next.AggregateID != metadata.AggregateID {
		t.Fatalf("next aggregate identity = %#v", next)
	}
	if next.Version != 1 || next.State != "active" || next.LastEventID != metadata.EventID || next.LastDecision != "approve" || next.ActiveApprovalID != metadata.ApprovalID {
		t.Fatalf("next aggregate transition = %#v", next)
	}
	if len(next.History) != 2 || next.History[0] != (ApprovalHistoryEntry{Version: 0, State: "none", EventID: "event-genesis", Decision: "none"}) || next.History[1] != (ApprovalHistoryEntry{Version: 1, State: "active", EventID: metadata.EventID, ApprovalID: metadata.ApprovalID, Decision: "approve"}) {
		t.Fatalf("next aggregate history = %#v", next.History)
	}
	lifecycleAssertContracts(t, event, next)
}

func TestReduceApprovalCommandV2TransitionMatrix(t *testing.T) {
	tests := []struct {
		name          string
		decision      string
		currentActive bool
		withPred      bool
		wantState     string
		wantRelation  string
		wantText      string
	}{
		{name: "reject none", decision: "reject", wantState: "rejected", wantRelation: "reject"},
		{name: "edit active", decision: "edit_then_approve", currentActive: true, withPred: true, wantState: "active", wantRelation: "edit", wantText: "edited proposal"},
		{name: "reject active", decision: "reject", currentActive: true, withPred: true, wantState: "rejected", wantRelation: "reject"},
		{name: "revoke active", decision: "revoke", currentActive: true, withPred: true, wantState: "revoked", wantRelation: "revoke"},
		{name: "supersede active", decision: "supersede", currentActive: true, withPred: true, wantState: "superseded", wantRelation: "supersede"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var current ApprovalAggregateV2
			var access ApprovalAccess
			var predecessor *string
			if test.currentActive {
				var active *ApprovalAggregateV2
				active, access = lifecycleTestActiveAggregate(t)
				current = *active
				if test.withPred {
					pred := current.ActiveApprovalID
					predecessor = &pred
				}
			} else {
				command, testAccess := lifecycleTestCommand(t, "approve", "none", 0, nil, nil)
				access = testAccess
				var err error
				current, err = NewApprovalGenesisAggregateV2(command, "aggregate-1", "event-genesis")
				if err != nil {
					t.Fatalf("NewApprovalGenesisAggregateV2: %v", err)
				}
			}

			edited := "edited proposal"
			state, version := current.State, current.Version
			command, err := lifecycleTestCommandForAccess(t, access, test.decision, state, version, predecessor, func(d *ApprovalCommandDraft) {
				if test.decision == "edit_then_approve" {
					d.EditedText = &edited
				}
			})
			if err != nil {
				t.Fatalf("lifecycleTestCommandForAccess: %v", err)
			}
			metadata := lifecycleTestMetadata(current.AggregateID, "event-"+test.decision, "approval-"+test.decision)
			if test.decision == "approve" {
				metadata.StoredProposalText = "stored approved proposal"
			}
			event, next, err := ReduceApprovalCommandV2(current, command, metadata)
			if err != nil {
				t.Fatalf("ReduceApprovalCommandV2: %v", err)
			}
			if event == nil || next == nil {
				t.Fatalf("successful reduce returned event=%v aggregate=%v", event != nil, next != nil)
			}
			if event.Decision != test.decision || event.LifecycleRelation != test.wantRelation || event.ApprovedText != test.wantText || event.AggregateVersion != current.Version+1 {
				t.Fatalf("event = %#v", event)
			}
			if test.decision == "reject" && test.wantText != "" {
				t.Fatalf("reject unexpectedly has approved text: %#v", event)
			}
			if next.State != test.wantState || next.Version != current.Version+1 || next.LastDecision != test.decision || next.ActiveApprovalID != func() string {
				if test.wantState == "active" {
					return metadata.ApprovalID
				}
				return ""
			}() {
				t.Fatalf("next aggregate = %#v", next)
			}
			if test.withPred && event.PredecessorApprovalID != *predecessor {
				t.Fatalf("event predecessor = %q want %q", event.PredecessorApprovalID, *predecessor)
			}
			lifecycleAssertEventAuthority(t, event, command, access)
			lifecycleAssertContracts(t, event, next)
		})
	}
}

func TestReduceApprovalCommandV2RejectsRejectActiveWithoutPredecessorOnlyWhenProvidedWrong(t *testing.T) {
	active, access := lifecycleTestActiveAggregate(t)
	command, err := lifecycleTestCommandForAccess(t, access, "reject", active.State, active.Version, nil, nil)
	if err != nil {
		t.Fatalf("lifecycleTestCommandForAccess: %v", err)
	}
	event, next, err := ReduceApprovalCommandV2(*active, command, lifecycleTestMetadata(active.AggregateID, "event-reject-no-pred", "approval-reject-no-pred"))
	if err != nil {
		t.Fatalf("reject active without predecessor should be allowed: %v", err)
	}
	if event == nil || next == nil || next.State != "rejected" || event.PredecessorApprovalID != "" {
		t.Fatalf("reject active result = event=%#v aggregate=%#v", event, next)
	}
}

func TestReduceApprovalCommandV2RejectsStaleAndReferenceConflicts(t *testing.T) {
	active, access := lifecycleTestActiveAggregate(t)
	stale, err := lifecycleTestCommandForAccess(t, access, "reject", "none", 0, nil, nil)
	if err != nil {
		t.Fatalf("stale command: %v", err)
	}
	event, next, err := ReduceApprovalCommandV2(*active, stale, lifecycleTestMetadata(active.AggregateID, "event-stale", "approval-stale"))
	lifecycleAssertConflict(t, err, "active", active.Version)
	if event != nil || next != nil {
		t.Fatalf("stale state/version produced a result: event=%#v aggregate=%#v", event, next)
	}

	staleVersion, err := lifecycleTestCommandForAccess(t, access, "reject", active.State, active.Version-1, nil, nil)
	if err != nil {
		t.Fatalf("stale version command: %v", err)
	}
	event, next, err = ReduceApprovalCommandV2(*active, staleVersion, lifecycleTestMetadata(active.AggregateID, "event-stale-version", "approval-stale-version"))
	lifecycleAssertConflict(t, err, "active", active.Version)
	if event != nil || next != nil {
		t.Fatalf("stale version produced a result: event=%#v aggregate=%#v", event, next)
	}

	identityFields := []struct {
		name   string
		mutate func(*ApprovalAggregateV2)
	}{
		{name: "workspace", mutate: func(value *ApprovalAggregateV2) { value.WorkspaceID = "other-workspace" }},
		{name: "proposal", mutate: func(value *ApprovalAggregateV2) { value.ProposalID = "other-proposal" }},
		{name: "pack", mutate: func(value *ApprovalAggregateV2) { value.EvidencePackID = "other-pack" }},
		{name: "basis", mutate: func(value *ApprovalAggregateV2) { value.ComputedBasisID = "other-basis" }},
		{name: "generation", mutate: func(value *ApprovalAggregateV2) { value.GenerationID = "other-generation" }},
		{name: "intent", mutate: func(value *ApprovalAggregateV2) { value.IntentRevision = 2 }},
	}
	for _, test := range identityFields {
		t.Run("aggregate identity "+test.name, func(t *testing.T) {
			command, err := lifecycleTestCommandForAccess(t, access, "approve", "none", 0, nil, nil)
			if err != nil {
				t.Fatalf("command: %v", err)
			}
			genesis, err := NewApprovalGenesisAggregateV2(command, "aggregate-1", "event-genesis")
			if err != nil {
				t.Fatalf("genesis: %v", err)
			}
			mutated := genesis
			test.mutate(&mutated)
			event, next, err := ReduceApprovalCommandV2(mutated, command, lifecycleTestMetadata("aggregate-1", "event-identity", "approval-identity"))
			lifecycleAssertReferenceConflict(t, err)
			if event != nil || next != nil {
				t.Fatalf("identity mismatch produced a result: event=%#v aggregate=%#v", event, next)
			}
		})
	}

	command, err := lifecycleTestCommandForAccess(t, access, "approve", "none", 0, nil, nil)
	if err != nil {
		t.Fatalf("command: %v", err)
	}
	genesis, err := NewApprovalGenesisAggregateV2(command, "aggregate-1", "event-genesis")
	if err != nil {
		t.Fatalf("genesis: %v", err)
	}
	event, next, err = ReduceApprovalCommandV2(genesis, command, lifecycleTestMetadata("other-aggregate", "event-reference", "approval-reference"))
	lifecycleAssertReferenceConflict(t, err)
	if event != nil || next != nil {
		t.Fatalf("metadata aggregate mismatch produced a result: event=%#v aggregate=%#v", event, next)
	}
}

func TestReduceApprovalCommandV2RejectsPredecessorAndInvalidInput(t *testing.T) {
	active, access := lifecycleTestActiveAggregate(t)
	wrongPredecessor := "approval-not-current"
	for _, decision := range []string{"edit_then_approve", "revoke", "supersede"} {
		t.Run(decision+" predecessor", func(t *testing.T) {
			var edited *string
			if decision == "edit_then_approve" {
				value := "edited proposal"
				edited = &value
			}
			command, err := lifecycleTestCommandForAccess(t, access, decision, active.State, active.Version, &wrongPredecessor, func(d *ApprovalCommandDraft) {
				d.EditedText = edited
			})
			if err != nil {
				t.Fatalf("command: %v", err)
			}
			event, next, err := ReduceApprovalCommandV2(*active, command, lifecycleTestMetadata(active.AggregateID, "event-wrong-pred", "approval-wrong-pred"))
			lifecycleAssertReferenceConflict(t, err)
			if event != nil || next != nil {
				t.Fatalf("wrong predecessor produced a result: event=%#v aggregate=%#v", event, next)
			}
		})
	}

	command, err := lifecycleTestCommandForAccess(t, access, "reject", "none", 0, &wrongPredecessor, nil)
	if err != nil {
		t.Fatalf("command: %v", err)
	}
	genesis, err := NewApprovalGenesisAggregateV2(command, "aggregate-1", "event-genesis")
	if err != nil {
		t.Fatalf("genesis: %v", err)
	}
	event, next, err := ReduceApprovalCommandV2(genesis, command, lifecycleTestMetadata(genesis.AggregateID, "event-reject-pred", "approval-reject-pred"))
	lifecycleAssertReferenceConflict(t, err)
	if event != nil || next != nil {
		t.Fatalf("reject none predecessor produced a result: event=%#v aggregate=%#v", event, next)
	}

	if event, next, err := ReduceApprovalCommandV2(genesis, nil, lifecycleTestMetadata(genesis.AggregateID, "event-nil-command", "approval-nil-command")); err == nil || event != nil || next != nil {
		t.Fatalf("nil command result = event=%#v aggregate=%#v err=%v", event, next, err)
	} else {
		lifecycleAssertInvalid(t, err)
	}
	command.command.Decision = "tampered decision"
	if event, next, err := ReduceApprovalCommandV2(genesis, command, lifecycleTestMetadata(genesis.AggregateID, "event-tampered", "approval-tampered")); err == nil || event != nil || next != nil {
		t.Fatalf("tampered command result = event=%#v aggregate=%#v err=%v", event, next, err)
	} else {
		lifecycleAssertInvalid(t, err)
	}
	invalidCurrent := genesis
	invalidCurrent.History = nil
	if event, next, err := ReduceApprovalCommandV2(invalidCurrent, lifecycleTestCommandForAccessMust(t, access, "approve", "none", 0, nil, nil), lifecycleTestMetadata(genesis.AggregateID, "event-invalid-current", "approval-invalid-current")); err == nil || event != nil || next != nil {
		t.Fatalf("invalid current result = event=%#v aggregate=%#v err=%v", event, next, err)
	} else {
		lifecycleAssertInvalid(t, err)
	}
}

func TestReduceApprovalCommandV2RejectsFabricatedHistoryGraph(t *testing.T) {
	approveCommand, access := lifecycleTestCommand(t, "approve", "none", 0, nil, nil)
	genesis, err := NewApprovalGenesisAggregateV2(approveCommand, "aggregate-graph", "event-graph-genesis")
	if err != nil {
		t.Fatalf("genesis: %v", err)
	}

	rejectCommand := lifecycleTestCommandForAccessMust(t, access, "reject", "none", 0, nil, nil)
	_, rejected, err := ReduceApprovalCommandV2(genesis, rejectCommand, lifecycleTestMetadata(genesis.AggregateID, "event-graph-reject", "approval-graph-reject"))
	if err != nil {
		t.Fatalf("reject genesis: %v", err)
	}

	active, activeAccess := lifecycleTestActiveAggregate(t)
	activePredecessor := active.ActiveApprovalID
	revokeCommand := lifecycleTestCommandForAccessMust(t, activeAccess, "revoke", active.State, active.Version, &activePredecessor, nil)
	_, revoked, err := ReduceApprovalCommandV2(*active, revokeCommand, lifecycleTestMetadata(active.AggregateID, "event-graph-revoke", "approval-graph-revoke"))
	if err != nil {
		t.Fatalf("revoke active: %v", err)
	}
	supersedeCommand := lifecycleTestCommandForAccessMust(t, activeAccess, "supersede", active.State, active.Version, &activePredecessor, nil)
	_, superseded, err := ReduceApprovalCommandV2(*active, supersedeCommand, lifecycleTestMetadata(active.AggregateID, "event-graph-supersede", "approval-graph-supersede"))
	if err != nil {
		t.Fatalf("supersede active: %v", err)
	}

	missingNonGenesisApproval := cloneApprovalAggregateV2(*rejected)
	missingNonGenesisApproval.History[1].ApprovalID = ""
	terminalSuccessor := func(source ApprovalAggregateV2, state, decision, marker string) ApprovalAggregateV2 {
		result := cloneApprovalAggregateV2(source)
		result.History = append(result.History, ApprovalHistoryEntry{
			Version: result.Version + 1, State: state, EventID: marker + "-event", ApprovalID: marker + "-approval", Decision: decision,
		})
		result.Version++
		result.State = state
		result.LastEventID = marker + "-event"
		result.LastDecision = decision
		result.ActiveApprovalID = ""
		return result
	}

	tests := []struct {
		name    string
		current ApprovalAggregateV2
		marker  string
	}{
		{name: "genesis approval id", current: func() ApprovalAggregateV2 {
			result := cloneApprovalAggregateV2(genesis)
			result.History[0].ApprovalID = "fabricated-genesis-approval"
			return result
		}(), marker: "fabricated-genesis-approval"},
		{name: "non-genesis approval id", current: missingNonGenesisApproval, marker: "fabricated-non-genesis-approval"},
		{name: "successor after rejected", current: terminalSuccessor(*rejected, "rejected", "reject", "fabricated-after-rejected"), marker: "fabricated-after-rejected"},
		{name: "successor after revoked", current: terminalSuccessor(*revoked, "revoked", "revoke", "fabricated-after-revoked"), marker: "fabricated-after-revoked"},
		{name: "successor after superseded", current: terminalSuccessor(*superseded, "superseded", "supersede", "fabricated-after-superseded"), marker: "fabricated-after-superseded"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			command := lifecycleTestCommandForAccessMust(t, access, "approve", "none", 0, nil, nil)
			metadata := lifecycleTestMetadata(test.current.AggregateID, test.marker+"-request", test.marker+"-new-approval")
			metadata.StoredProposalText = "stored proposal"
			event, next, err := ReduceApprovalCommandV2(test.current, command, metadata)
			lifecycleAssertInvalid(t, err)
			if event != nil || next != nil {
				t.Fatalf("fabricated aggregate produced a result: event=%#v aggregate=%#v", event, next)
			}
			if strings.Contains(err.Error(), test.marker) {
				t.Fatalf("invalid aggregate identity leaked in error: %v", err)
			}
		})
	}
}

func TestReduceApprovalCommandV2RejectsDuplicateHistoryIdentity(t *testing.T) {
	active, access := lifecycleTestActiveAggregate(t)
	predecessor := active.ActiveApprovalID
	command, err := lifecycleTestCommandForAccess(t, access, "edit_then_approve", active.State, active.Version, &predecessor, func(draft *ApprovalCommandDraft) {
		edited := "duplicate identity edit"
		draft.EditedText = &edited
	})
	if err != nil {
		t.Fatalf("command: %v", err)
	}

	tests := []struct {
		name       string
		mutateMeta func(*ApprovalTransitionMetadata, ApprovalAggregateV2)
	}{
		{name: "event id", mutateMeta: func(metadata *ApprovalTransitionMetadata, current ApprovalAggregateV2) {
			metadata.EventID = current.History[0].EventID
		}},
		{name: "approval id", mutateMeta: func(metadata *ApprovalTransitionMetadata, current ApprovalAggregateV2) {
			metadata.ApprovalID = current.History[1].ApprovalID
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			metadata := lifecycleTestMetadata(active.AggregateID, "event-duplicate-request", "approval-duplicate-request")
			test.mutateMeta(&metadata, *active)
			event, next, err := ReduceApprovalCommandV2(*active, command, metadata)
			lifecycleAssertInvalid(t, err)
			if event != nil || next != nil {
				t.Fatalf("duplicate identity produced a result: event=%#v aggregate=%#v", event, next)
			}
		})
	}
}

func TestReduceApprovalCommandV2UsesUnicodeCodePointTextBounds(t *testing.T) {
	command, _ := lifecycleTestCommand(t, "approve", "none", 0, nil, nil)
	genesis, err := NewApprovalGenesisAggregateV2(command, "aggregate-unicode-text", "event-unicode-genesis")
	if err != nil {
		t.Fatalf("genesis: %v", err)
	}
	validText := strings.Repeat("한", approvalLifecycleMaxText)
	metadata := lifecycleTestMetadata(genesis.AggregateID, "event-unicode-text", "approval-unicode-text")
	metadata.StoredProposalText = validText
	event, next, err := ReduceApprovalCommandV2(genesis, command, metadata)
	if err != nil {
		t.Fatalf("4096-rune proposal text rejected: %v", err)
	}
	if event == nil || next == nil || event.ApprovedText != validText {
		t.Fatalf("4096-rune proposal text was not preserved: event=%#v aggregate=%#v", event, next)
	}
	var encoded map[string]any
	raw, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}
	if err := json.Unmarshal(raw, &encoded); err != nil {
		t.Fatalf("unmarshal event: %v", err)
	}
	if encoded["approvedText"] != validText {
		t.Fatalf("JSON changed proposal text: got %q want %q", encoded["approvedText"], validText)
	}
	lifecycleAssertContracts(t, event, next)

	tooLong := lifecycleTestMetadata(genesis.AggregateID, "event-unicode-text-too-long", "approval-unicode-text-too-long")
	tooLong.StoredProposalText = strings.Repeat("한", approvalLifecycleMaxText+1)
	event, next, err = ReduceApprovalCommandV2(genesis, command, tooLong)
	lifecycleAssertInvalid(t, err)
	if event != nil || next != nil {
		t.Fatalf("4097-rune proposal text produced a result: event=%#v aggregate=%#v", event, next)
	}

	invalidUTF8 := lifecycleTestMetadata(genesis.AggregateID, "event-unicode-text-invalid", "approval-unicode-text-invalid")
	invalidUTF8.StoredProposalText = string([]byte{'v', 0xff, 'x'})
	event, next, err = ReduceApprovalCommandV2(genesis, command, invalidUTF8)
	lifecycleAssertInvalid(t, err)
	if event != nil || next != nil {
		t.Fatalf("invalid UTF-8 proposal text produced a result: event=%#v aggregate=%#v", event, next)
	}
}

func TestReduceApprovalCommandV2UsesUnicodeCodePointIDBounds(t *testing.T) {
	command, _ := lifecycleTestCommand(t, "approve", "none", 0, nil, nil)
	aggregateID := strings.Repeat("界", approvalLifecycleMaxID)
	genesisEventID := strings.Repeat("始", approvalLifecycleMaxID)
	eventID := strings.Repeat("事", approvalLifecycleMaxID)
	approvalID := strings.Repeat("承", approvalLifecycleMaxID)
	genesis, err := NewApprovalGenesisAggregateV2(command, aggregateID, genesisEventID)
	if err != nil {
		t.Fatalf("256-rune aggregate/event IDs rejected: %v", err)
	}
	metadata := lifecycleTestMetadata(aggregateID, eventID, approvalID)
	metadata.StoredProposalText = "stored proposal"
	event, next, err := ReduceApprovalCommandV2(genesis, command, metadata)
	if err != nil {
		t.Fatalf("256-rune transition IDs rejected: %v", err)
	}
	if event == nil || next == nil || event.EventID != eventID || event.ApprovalID != approvalID || event.AggregateID != aggregateID {
		t.Fatalf("transition IDs were not preserved: event=%#v aggregate=%#v", event, next)
	}
	var encoded map[string]any
	raw, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}
	if err := json.Unmarshal(raw, &encoded); err != nil {
		t.Fatalf("unmarshal event: %v", err)
	}
	for _, test := range []struct {
		field string
		want  string
	}{
		{field: "eventId", want: eventID},
		{field: "approvalId", want: approvalID},
		{field: "aggregateId", want: aggregateID},
	} {
		if encoded[test.field] != test.want {
			t.Fatalf("JSON changed %s: got %q want %q", test.field, encoded[test.field], test.want)
		}
	}
	lifecycleAssertContracts(t, event, next)

	tooLong := strings.Repeat("界", approvalLifecycleMaxID+1)
	if aggregate, err := NewApprovalGenesisAggregateV2(command, tooLong, "event-valid"); err == nil || !reflect.DeepEqual(aggregate, ApprovalAggregateV2{}) {
		t.Fatalf("257-rune aggregate ID unexpectedly accepted: aggregate=%#v err=%v", aggregate, err)
	}
	if aggregate, err := NewApprovalGenesisAggregateV2(command, "aggregate-valid", tooLong); err == nil || !reflect.DeepEqual(aggregate, ApprovalAggregateV2{}) {
		t.Fatalf("257-rune genesis event ID unexpectedly accepted: aggregate=%#v err=%v", aggregate, err)
	}
	for _, test := range []struct {
		name       string
		mutateMeta func(*ApprovalTransitionMetadata)
	}{
		{name: "event ID", mutateMeta: func(value *ApprovalTransitionMetadata) { value.EventID = tooLong }},
		{name: "approval ID", mutateMeta: func(value *ApprovalTransitionMetadata) { value.ApprovalID = tooLong }},
	} {
		t.Run("257-rune "+test.name, func(t *testing.T) {
			metadata := lifecycleTestMetadata("aggregate-valid", "event-valid", "approval-valid")
			metadata.StoredProposalText = "stored proposal"
			test.mutateMeta(&metadata)
			event, next, err := ReduceApprovalCommandV2(func() ApprovalAggregateV2 {
				value, err := NewApprovalGenesisAggregateV2(command, "aggregate-valid", "event-valid-genesis")
				if err != nil {
					t.Fatalf("genesis: %v", err)
				}
				return value
			}(), command, metadata)
			lifecycleAssertInvalid(t, err)
			if event != nil || next != nil {
				t.Fatalf("257-rune %s produced a result: event=%#v aggregate=%#v", test.name, event, next)
			}
		})
	}
}

func TestReduceApprovalCommandV2RejectsInvalidUTF8LifecycleIDs(t *testing.T) {
	command, _ := lifecycleTestCommand(t, "approve", "none", 0, nil, nil)
	invalidID := string([]byte{'i', 0xff, 'd'})
	if aggregate, err := NewApprovalGenesisAggregateV2(command, invalidID, "event-valid"); err == nil || !reflect.DeepEqual(aggregate, ApprovalAggregateV2{}) {
		t.Fatalf("invalid UTF-8 aggregate ID unexpectedly accepted: aggregate=%#v err=%v", aggregate, err)
	}
	if aggregate, err := NewApprovalGenesisAggregateV2(command, "aggregate-valid", invalidID); err == nil || !reflect.DeepEqual(aggregate, ApprovalAggregateV2{}) {
		t.Fatalf("invalid UTF-8 genesis event ID unexpectedly accepted: aggregate=%#v err=%v", aggregate, err)
	}
	for _, test := range []struct {
		name       string
		mutateMeta func(*ApprovalTransitionMetadata)
	}{
		{name: "event ID", mutateMeta: func(value *ApprovalTransitionMetadata) { value.EventID = invalidID }},
		{name: "approval ID", mutateMeta: func(value *ApprovalTransitionMetadata) { value.ApprovalID = invalidID }},
	} {
		t.Run(test.name, func(t *testing.T) {
			genesis, err := NewApprovalGenesisAggregateV2(command, "aggregate-valid", "event-valid-genesis")
			if err != nil {
				t.Fatalf("genesis: %v", err)
			}
			metadata := lifecycleTestMetadata(genesis.AggregateID, "event-valid", "approval-valid")
			metadata.StoredProposalText = "stored proposal"
			test.mutateMeta(&metadata)
			event, next, err := ReduceApprovalCommandV2(genesis, command, metadata)
			lifecycleAssertInvalid(t, err)
			if event != nil || next != nil {
				t.Fatalf("invalid UTF-8 %s produced a result: event=%#v aggregate=%#v", test.name, event, next)
			}
		})
	}
	invalidCurrent, err := NewApprovalGenesisAggregateV2(command, "aggregate-valid", "event-valid-genesis")
	if err != nil {
		t.Fatalf("genesis: %v", err)
	}
	invalidCurrent.History[0].EventID = invalidID
	metadata := lifecycleTestMetadata(invalidCurrent.AggregateID, "event-valid", "approval-valid")
	metadata.StoredProposalText = "stored proposal"
	event, next, err := ReduceApprovalCommandV2(invalidCurrent, command, metadata)
	lifecycleAssertInvalid(t, err)
	if event != nil || next != nil {
		t.Fatalf("invalid UTF-8 current history ID produced a result: event=%#v aggregate=%#v", event, next)
	}
}

func TestReduceApprovalCommandV2PreservesUnicodeEditedText(t *testing.T) {
	active, access := lifecycleTestActiveAggregate(t)
	predecessor := active.ActiveApprovalID
	editedText := strings.Repeat("編", approvalLifecycleMaxText)
	command, err := lifecycleTestCommandForAccess(t, access, "edit_then_approve", active.State, active.Version, &predecessor, func(draft *ApprovalCommandDraft) {
		draft.EditedText = &editedText
	})
	if err != nil {
		t.Fatalf("bind 4096-rune edited text: %v", err)
	}
	event, next, err := ReduceApprovalCommandV2(*active, command, lifecycleTestMetadata(active.AggregateID, "event-unicode-edit", "approval-unicode-edit"))
	if err != nil {
		t.Fatalf("4096-rune edited text rejected: %v", err)
	}
	if event == nil || next == nil || event.ApprovedText != editedText {
		t.Fatalf("edited text was not preserved: event=%#v aggregate=%#v", event, next)
	}
	var encoded map[string]any
	raw, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}
	if err := json.Unmarshal(raw, &encoded); err != nil {
		t.Fatalf("unmarshal event: %v", err)
	}
	if encoded["approvedText"] != editedText {
		t.Fatalf("JSON changed edited text: got %q want %q", encoded["approvedText"], editedText)
	}
	lifecycleAssertContracts(t, event, next)

	tooLong := strings.Repeat("編", approvalLifecycleMaxText+1)
	draft := approvalCommandTestDraft()
	draft.CommandID = "command-unicode-edit-too-long"
	draft.Decision = "edit_then_approve"
	draft.ExpectedState = active.State
	draft.ExpectedApprovalVersion = active.Version
	draft.PredecessorApprovalID = &predecessor
	draft.EditedText = &tooLong
	if bound, err := BindApprovalCommandV2(access, draft); err == nil || bound != nil {
		t.Fatalf("4097-rune edited text unexpectedly bound: command=%#v err=%v", bound, err)
	}
}

func TestReduceApprovalCommandV2RejectsNonRFC3339OccurredAt(t *testing.T) {
	command, _ := lifecycleTestCommand(t, "approve", "none", 0, nil, nil)
	genesis, err := NewApprovalGenesisAggregateV2(command, "aggregate-time-invalid", "event-time-genesis")
	if err != nil {
		t.Fatalf("genesis: %v", err)
	}
	for _, test := range []struct {
		name string
		id   string
		when time.Time
	}{
		{name: "year above RFC3339 range", id: "year-above-rfc3339", when: time.Date(10000, time.January, 1, 0, 0, 0, 0, time.UTC)},
		{name: "negative year", id: "negative-year", when: time.Date(-1, time.January, 1, 0, 0, 0, 0, time.UTC)},
	} {
		t.Run(test.name, func(t *testing.T) {
			metadata := lifecycleTestMetadata(genesis.AggregateID, "event-time-"+test.id, "approval-time-"+test.id)
			metadata.StoredProposalText = "stored proposal"
			metadata.OccurredAt = test.when
			event, next, err := ReduceApprovalCommandV2(genesis, command, metadata)
			lifecycleAssertInvalid(t, err)
			if event != nil || next != nil {
				t.Fatalf("non-RFC3339 timestamp produced a result: event=%#v aggregate=%#v", event, next)
			}
		})
	}

	metadata := lifecycleTestMetadata(genesis.AggregateID, "event-time-direct", "approval-time-direct")
	metadata.StoredProposalText = "stored proposal"
	event, _, err := ReduceApprovalCommandV2(genesis, command, metadata)
	if err != nil {
		t.Fatalf("valid timestamp setup: %v", err)
	}
	event.Timestamp = "+10000-01-01T00:00:00Z"
	if err := validateApprovalLifecycleEvent(*event); err == nil {
		t.Fatal("non-RFC3339 event timestamp unexpectedly passed direct validation")
	} else if !errors.Is(err, ErrApprovalLifecycleInvalid) {
		t.Fatalf("direct invalid timestamp error = %v, want ErrApprovalLifecycleInvalid", err)
	}
}

func TestValidateApprovalLifecycleEventRejectsNonCanonicalRFC3339Timestamp(t *testing.T) {
	command, _ := lifecycleTestCommand(t, "approve", "none", 0, nil, nil)
	genesis, err := NewApprovalGenesisAggregateV2(command, "aggregate-time-noncanonical", "event-time-noncanonical-genesis")
	if err != nil {
		t.Fatalf("genesis: %v", err)
	}
	metadata := lifecycleTestMetadata(genesis.AggregateID, "event-time-noncanonical", "approval-time-noncanonical")
	metadata.StoredProposalText = "stored proposal"
	event, _, err := ReduceApprovalCommandV2(genesis, command, metadata)
	if err != nil {
		t.Fatalf("valid timestamp setup: %v", err)
	}
	if event == nil {
		t.Fatal("valid timestamp setup returned nil event")
	}
	if err := validateApprovalLifecycleEvent(*event); err != nil {
		t.Fatalf("canonical reducer event rejected by direct validation: %v", err)
	}

	const nonCanonical = "2026-09-06T12:00:00.000Z"
	parsed, err := time.Parse(time.RFC3339Nano, nonCanonical)
	if err != nil {
		t.Fatalf("noncanonical setup timestamp must parse: %v", err)
	}
	canonical, err := time.Parse(time.RFC3339Nano, event.Timestamp)
	if err != nil {
		t.Fatalf("canonical event timestamp must parse: %v", err)
	}
	if !parsed.Equal(canonical) {
		t.Fatalf("noncanonical timestamp changed the instant: parsed=%v canonical=%v", parsed, canonical)
	}
	if event.Timestamp == nonCanonical {
		t.Fatalf("reducer emitted noncanonical timestamp %q", event.Timestamp)
	}

	mutated := *event
	mutated.Timestamp = nonCanonical
	if err := validateApprovalLifecycleEvent(mutated); err == nil {
		t.Fatal("parseable but noncanonical RFC3339 timestamp unexpectedly passed direct validation")
	} else if !errors.Is(err, ErrApprovalLifecycleInvalid) {
		t.Fatalf("noncanonical timestamp error = %v, want ErrApprovalLifecycleInvalid", err)
	}
}

func TestReduceApprovalCommandV2CanonicalizesRFC3339Timestamp(t *testing.T) {
	command, _ := lifecycleTestCommand(t, "approve", "none", 0, nil, nil)
	genesis, err := NewApprovalGenesisAggregateV2(command, "aggregate-time-valid", "event-time-valid-genesis")
	if err != nil {
		t.Fatalf("genesis: %v", err)
	}
	zone := time.FixedZone("fixture-offset", 5*60*60+30*60)
	when := time.Date(9999, time.December, 31, 23, 59, 58, 987654321, zone)
	metadata := lifecycleTestMetadata(genesis.AggregateID, "event-time-valid", "approval-time-valid")
	metadata.StoredProposalText = "stored proposal"
	metadata.OccurredAt = when
	event, next, err := ReduceApprovalCommandV2(genesis, command, metadata)
	if err != nil {
		t.Fatalf("valid timezone timestamp rejected: %v", err)
	}
	wantTimestamp := when.UTC().Format(time.RFC3339Nano)
	if event == nil || next == nil || event.Timestamp != wantTimestamp {
		t.Fatalf("timestamp = %q want canonical UTC %q", event.Timestamp, wantTimestamp)
	}
	parsed, err := time.Parse(time.RFC3339Nano, event.Timestamp)
	if err != nil || !parsed.Equal(when) || parsed.Format(time.RFC3339Nano) != event.Timestamp {
		t.Fatalf("timestamp round-trip = %v/%v, want instant %v and canonical format %q", parsed, err, when, event.Timestamp)
	}
	lifecycleAssertContracts(t, event, next)

	monotonic := time.Now().Add(17 * time.Millisecond)
	metadata = lifecycleTestMetadata(genesis.AggregateID, "event-time-monotonic", "approval-time-monotonic")
	metadata.StoredProposalText = "stored proposal"
	metadata.OccurredAt = monotonic
	event, _, err = ReduceApprovalCommandV2(genesis, command, metadata)
	if err != nil {
		t.Fatalf("monotonic timestamp rejected: %v", err)
	}
	parsed, err = time.Parse(time.RFC3339Nano, event.Timestamp)
	if err != nil || !parsed.Equal(monotonic) {
		t.Fatalf("monotonic timestamp round-trip = %v/%v, want instant %v", parsed, err, monotonic)
	}
}

func TestReduceApprovalCommandV2RejectsInvalidTransitionMetadata(t *testing.T) {
	command, access := lifecycleTestCommand(t, "approve", "none", 0, nil, nil)
	genesis, err := NewApprovalGenesisAggregateV2(command, "aggregate-1", "event-genesis")
	if err != nil {
		t.Fatalf("genesis: %v", err)
	}
	valid := lifecycleTestMetadata(genesis.AggregateID, "event-valid", "approval-valid")
	tests := []struct {
		name   string
		mutate func(*ApprovalTransitionMetadata)
	}{
		{name: "missing event id", mutate: func(value *ApprovalTransitionMetadata) { value.EventID = "" }},
		{name: "missing approval id", mutate: func(value *ApprovalTransitionMetadata) { value.ApprovalID = "" }},
		{name: "missing aggregate id", mutate: func(value *ApprovalTransitionMetadata) { value.AggregateID = "" }},
		{name: "zero occurred time", mutate: func(value *ApprovalTransitionMetadata) { value.OccurredAt = time.Time{} }},
		{name: "missing stored proposal text", mutate: func(value *ApprovalTransitionMetadata) { value.StoredProposalText = "" }},
		{name: "oversized event id", mutate: func(value *ApprovalTransitionMetadata) { value.EventID = strings.Repeat("x", 257) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			metadata := valid
			test.mutate(&metadata)
			event, next, err := ReduceApprovalCommandV2(genesis, command, metadata)
			lifecycleAssertInvalid(t, err)
			if event != nil || next != nil {
				t.Fatalf("invalid metadata produced a result: event=%#v aggregate=%#v", event, next)
			}
		})
	}

	for _, decision := range []string{"reject", "revoke", "supersede"} {
		t.Run(decision+" unexpected stored text", func(t *testing.T) {
			current := genesis
			commandAccess := access
			var predecessor *string
			if decision != "reject" {
				active, activeAccess := lifecycleTestActiveAggregate(t)
				current = *active
				commandAccess = activeAccess
				value := current.ActiveApprovalID
				predecessor = &value
			}
			command, err := lifecycleTestCommandForAccess(t, commandAccess, decision, current.State, current.Version, predecessor, nil)
			if err != nil {
				t.Fatalf("command: %v", err)
			}
			metadata := lifecycleTestMetadata(current.AggregateID, "event-"+decision, "approval-"+decision)
			metadata.StoredProposalText = "unexpected text"
			event, next, err := ReduceApprovalCommandV2(current, command, metadata)
			lifecycleAssertInvalid(t, err)
			if event != nil || next != nil {
				t.Fatalf("unexpected stored text produced a result: event=%#v aggregate=%#v", event, next)
			}
		})
	}

	_ = access
}

func TestReduceApprovalCommandV2PreservesInputAndResultIsolation(t *testing.T) {
	command, _ := lifecycleTestCommand(t, "approve", "none", 0, nil, nil)
	genesis, err := NewApprovalGenesisAggregateV2(command, "aggregate-1", "event-genesis")
	if err != nil {
		t.Fatalf("genesis: %v", err)
	}
	event, next, err := ReduceApprovalCommandV2(genesis, command, func() ApprovalTransitionMetadata {
		metadata := lifecycleTestMetadata(genesis.AggregateID, "event-isolated", "approval-isolated")
		metadata.StoredProposalText = "stored approved proposal"
		return metadata
	}())
	if err != nil {
		t.Fatalf("reduce: %v", err)
	}
	originalGenesis := genesis
	originalEvent := *event
	originalNext := *next
	next.History[0].State = "mutated result"
	next.History[1].Decision = "mutated result"
	if !reflect.DeepEqual(genesis, originalGenesis) {
		t.Fatalf("mutating returned history changed input genesis: got %#v want %#v", genesis, originalGenesis)
	}
	if event.EventID != originalEvent.EventID || event.ApprovedText != originalEvent.ApprovedText {
		t.Fatalf("mutating returned aggregate changed event: got %#v want %#v", event, originalEvent)
	}
	mutatedInput := genesis
	mutatedInput.History[0].EventID = "mutated input"
	if next.History[0].EventID != originalNext.History[0].EventID {
		t.Fatalf("mutating input history changed returned aggregate: got %#v want %#v", next.History[0], originalNext.History[0])
	}
}

func TestReduceApprovalCommandV2RejectsHistoryAndVersionBounds(t *testing.T) {
	command, access := lifecycleTestCommand(t, "approve", "none", 0, nil, nil)
	genesis, err := NewApprovalGenesisAggregateV2(command, "aggregate-1", "event-genesis")
	if err != nil {
		t.Fatalf("genesis: %v", err)
	}
	current := genesis
	current.History = make([]ApprovalHistoryEntry, 256)
	current.History[0] = ApprovalHistoryEntry{Version: 0, State: "none", EventID: "event-genesis", Decision: "none"}
	for index := 1; index < len(current.History); index++ {
		decision := "edit_then_approve"
		if index == 1 {
			decision = "approve"
		}
		current.History[index] = ApprovalHistoryEntry{Version: int64(index), State: "active", EventID: "event-history-" + strconv.Itoa(index), ApprovalID: "approval-history-" + strconv.Itoa(index), Decision: decision}
	}
	current.Version = 255
	current.State = "active"
	current.LastEventID = current.History[255].EventID
	current.LastDecision = current.History[255].Decision
	current.ActiveApprovalID = current.History[255].ApprovalID
	predecessor := current.ActiveApprovalID
	command, err = lifecycleTestCommandForAccess(t, access, "edit_then_approve", current.State, current.Version, &predecessor, func(d *ApprovalCommandDraft) {
		edited := "edited proposal"
		d.EditedText = &edited
	})
	if err != nil {
		t.Fatalf("command: %v", err)
	}
	event, next, err := ReduceApprovalCommandV2(current, command, lifecycleTestMetadata(current.AggregateID, "event-history-overflow", "approval-history-overflow"))
	lifecycleAssertInvalid(t, err)
	if event != nil || next != nil {
		t.Fatalf("history bound produced a result: event=%#v aggregate=%#v", event, next)
	}

	tooLarge := current
	tooLarge.Version = 1_000_000_000
	event, next, err = ReduceApprovalCommandV2(tooLarge, command, lifecycleTestMetadata(current.AggregateID, "event-version-overflow", "approval-version-overflow"))
	lifecycleAssertInvalid(t, err)
	if event != nil || next != nil {
		t.Fatalf("version bound produced a result: event=%#v aggregate=%#v", event, next)
	}
}

func TestNewApprovalGenesisAggregateV2RejectsInvalidInputs(t *testing.T) {
	if aggregate, err := NewApprovalGenesisAggregateV2(nil, "aggregate-1", "event-genesis"); err == nil || !reflect.DeepEqual(aggregate, ApprovalAggregateV2{}) {
		t.Fatalf("nil command genesis result = %#v err=%v", aggregate, err)
	}
	command, _ := lifecycleTestCommand(t, "approve", "none", 0, nil, nil)
	for _, test := range []struct {
		name        string
		aggregateID string
		genesisID   string
	}{
		{name: "missing aggregate", aggregateID: "", genesisID: "event-genesis"},
		{name: "missing genesis event", aggregateID: "aggregate-1", genesisID: ""},
		{name: "unsafe aggregate", aggregateID: "../aggregate", genesisID: "event-genesis"},
	} {
		t.Run(test.name, func(t *testing.T) {
			aggregate, err := NewApprovalGenesisAggregateV2(command, test.aggregateID, test.genesisID)
			if err == nil || !reflect.DeepEqual(aggregate, ApprovalAggregateV2{}) {
				t.Fatalf("invalid genesis result = %#v err=%v", aggregate, err)
			}
		})
	}
}

func lifecycleTestCommand(t *testing.T, decision, state string, version int64, predecessor, edited *string) (*ValidatedApprovalCommandV2, ApprovalAccess) {
	t.Helper()
	access := approvalCommandTestAccess(t)
	command, err := lifecycleTestCommandForAccess(t, access, decision, state, version, predecessor, func(draft *ApprovalCommandDraft) {
		draft.EditedText = edited
	})
	if err != nil {
		t.Fatalf("lifecycle command: %v", err)
	}
	return command, access
}

func lifecycleTestCommandForAccess(t *testing.T, access ApprovalAccess, decision, state string, version int64, predecessor *string, customize func(*ApprovalCommandDraft)) (*ValidatedApprovalCommandV2, error) {
	t.Helper()
	draft := approvalCommandTestDraft()
	draft.CommandID = "command-lifecycle-" + decision
	draft.IdempotencyKey = "idempotency-lifecycle-" + decision
	draft.Decision = decision
	draft.ExpectedState = state
	draft.ExpectedApprovalVersion = version
	draft.PredecessorApprovalID = predecessor
	if customize != nil {
		customize(&draft)
	}
	return BindApprovalCommandV2(access, draft)
}

func lifecycleTestCommandForAccessMust(t *testing.T, access ApprovalAccess, decision, state string, version int64, predecessor, edited *string) *ValidatedApprovalCommandV2 {
	t.Helper()
	command, err := lifecycleTestCommandForAccess(t, access, decision, state, version, predecessor, func(draft *ApprovalCommandDraft) {
		draft.EditedText = edited
	})
	if err != nil {
		t.Fatalf("lifecycle command: %v", err)
	}
	return command
}

func lifecycleTestActiveAggregate(t *testing.T) (*ApprovalAggregateV2, ApprovalAccess) {
	t.Helper()
	command, access := lifecycleTestCommand(t, "approve", "none", 0, nil, nil)
	genesis, err := NewApprovalGenesisAggregateV2(command, "aggregate-1", "event-genesis")
	if err != nil {
		t.Fatalf("genesis: %v", err)
	}
	metadata := lifecycleTestMetadata(genesis.AggregateID, "event-approve", "approval-approve")
	metadata.StoredProposalText = "stored approved proposal"
	_, active, err := ReduceApprovalCommandV2(genesis, command, metadata)
	if err != nil {
		t.Fatalf("approve genesis: %v", err)
	}
	return active, access
}

func lifecycleTestMetadata(aggregateID, eventID, approvalID string) ApprovalTransitionMetadata {
	return ApprovalTransitionMetadata{
		EventID:     eventID,
		ApprovalID:  approvalID,
		AggregateID: aggregateID,
		OccurredAt:  time.Date(2026, time.September, 6, 12, 0, 0, 0, time.UTC),
	}
}

func lifecycleAssertEventAuthority(t *testing.T, event *ApprovalEventV2, command *ValidatedApprovalCommandV2, access ApprovalAccess) {
	t.Helper()
	value := command.Command()
	if event.ActorID != access.Actor().ActorID || event.SessionID != access.Actor().SessionID || event.WorkspaceID != access.Workspace().WorkspaceID() || event.ActorID != value.ActorID || event.SessionID != value.SessionID || event.WorkspaceID != value.WorkspaceID || event.ProposalID != value.ProposalID || event.EvidencePackID != value.EvidencePackID || event.ComputedBasisID != value.ComputedBasisID || event.GenerationID != value.GenerationID || event.IntentRevision != value.IntentRevision {
		t.Fatalf("event authority/reference mismatch: event=%#v command=%#v", event, value)
	}
}

func lifecycleAssertContracts(t *testing.T, event *ApprovalEventV2, aggregate *ApprovalAggregateV2) {
	t.Helper()
	eventData, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}
	if err := contractharness.ValidateVS09Contract(contractharness.ApprovalEventV2SchemaID, eventData); err != nil {
		t.Fatalf("event contract validation: %v", err)
	}
	aggregateData, err := json.Marshal(aggregate)
	if err != nil {
		t.Fatalf("marshal aggregate: %v", err)
	}
	if err := contractharness.ValidateVS09Contract(contractharness.ApprovalAggregateV2SchemaID, aggregateData); err != nil {
		t.Fatalf("aggregate contract validation: %v", err)
	}
}

func lifecycleAssertInvalid(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected approval lifecycle invalid error")
	}
	if !errors.Is(err, ErrApprovalLifecycleInvalid) {
		t.Fatalf("error = %v, want ErrApprovalLifecycleInvalid", err)
	}
	var typed *ApprovalLifecycleError
	if !errors.As(err, &typed) {
		t.Fatalf("error = %v, want typed ApprovalLifecycleError", err)
	}
}

func lifecycleAssertConflict(t *testing.T, err error, state string, version int64) {
	t.Helper()
	if err == nil {
		t.Fatal("expected approval lifecycle conflict error")
	}
	if !errors.Is(err, ErrApprovalLifecycleConflict) {
		t.Fatalf("error = %v, want ErrApprovalLifecycleConflict", err)
	}
	var typed *ApprovalLifecycleError
	if !errors.As(err, &typed) {
		t.Fatalf("error = %v, want typed ApprovalLifecycleError", err)
	}
	if typed.CurrentState != state || typed.CurrentVersion != version {
		t.Fatalf("conflict current state/version = %q/%d want %q/%d", typed.CurrentState, typed.CurrentVersion, state, version)
	}
}

func lifecycleAssertReferenceConflict(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected approval lifecycle reference conflict")
	}
	if !errors.Is(err, ErrApprovalLifecycleConflict) || !errors.Is(err, ErrApprovalLifecycleReferenceConflict) {
		t.Fatalf("error = %v, want lifecycle/reference conflict", err)
	}
	var typed *ApprovalLifecycleError
	if !errors.As(err, &typed) || typed.Kind != "reference" {
		t.Fatalf("error = %v, want reference ApprovalLifecycleError", err)
	}
}
