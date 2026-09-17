package contractharness

import (
	"encoding/json"
	"errors"
	"io/fs"
	"testing"

	"codeflow/schemas"
)

func TestVS09ContractRegistry(t *testing.T) {
	expected := []string{
		"rflsc.approval-command.v2",
		"rflsc.approval-event.v2",
		"rflsc.approval-aggregate.v2",
		"rflsc.approval-idempotency-result.v1",
		"rflsc.approval-outbox.v1",
		"rflsc.approval-history.v1",
	}
	if len(VS09ContractRegistry) != len(expected) {
		t.Fatalf("VS-09 registry count=%d want=%d", len(VS09ContractRegistry), len(expected))
	}
	for index, entry := range VS09ContractRegistry {
		if entry.ID != expected[index] {
			t.Fatalf("VS-09 registry entry %d = %q want %q", index, entry.ID, expected[index])
		}
	}
}

func TestVS09ApprovalHistoryProducerConsumerBoundary(t *testing.T) {
	entry := VS09ContractRegistry[len(VS09ContractRegistry)-1]
	if entry.ID != "rflsc.approval-history.v1" {
		t.Fatalf("history registry entry = %q, want rflsc.approval-history.v1", entry.ID)
	}
	if entry.Producer != "semantic approval history query service" {
		t.Fatalf("history producer = %q", entry.Producer)
	}
	if entry.Consumer != "MCP/FlowView approval history adapters" {
		t.Fatalf("history consumer = %q", entry.Consumer)
	}
	data, err := fs.ReadFile(schemas.FixturesFS, "fixtures/"+entry.ValidFixture)
	if err != nil {
		t.Fatal(err)
	}
	validator := ValidatorForVS09Contract(entry.SchemaID)
	if validator == nil {
		t.Fatal("history registry entry has no semantic validator")
	}
	if err := validator(data); err != nil {
		t.Fatalf("history producer payload rejected by consumer boundary: %v", err)
	}
}

func TestVS09ApprovalHistoryRejectsContradictoryPredecessor(t *testing.T) {
	entry := VS09ContractRegistry[len(VS09ContractRegistry)-1]
	data, err := fs.ReadFile(schemas.FixturesFS, "fixtures/"+entry.ValidFixture)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	event := document["events"].([]any)[0].(map[string]any)
	aggregate := document["aggregate"].(map[string]any)
	history := aggregate["history"].([]any)
	// Turn the one-event approval fixture into a structurally valid reject from
	// genesis, then retain a foreign predecessor as the single contradiction.
	event["decision"] = "reject"
	event["approvedText"] = ""
	event["lifecycleRelation"] = "reject"
	event["predecessorApprovalId"] = "approval-foreign"
	aggregate["state"] = "rejected"
	aggregate["lastDecision"] = "reject"
	delete(aggregate, "activeApprovalId")
	history[1].(map[string]any)["state"] = "rejected"
	history[1].(map[string]any)["decision"] = "reject"
	mutated, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if err := Validate(entry.SchemaID, mutated); err != nil {
		t.Fatalf("contradictory predecessor should pass structural schema validation: %v", err)
	}
	err = ValidateVS09Contract(entry.SchemaID, mutated)
	if err == nil {
		t.Fatal("contradictory predecessor unexpectedly passed semantic validation")
	}
	var semanticErr *VS09ValidationError
	if !errors.As(err, &semanticErr) {
		t.Fatalf("contradictory predecessor did not produce typed semantic error: %v", err)
	}
	if semanticErr.Category != "transition" || semanticErr.Field != "/events/0/predecessorApprovalId" {
		t.Fatalf("contradictory predecessor targeted %s %s", semanticErr.Category, semanticErr.Field)
	}
}

func TestVS09ApprovalHistoryCrossChecksEventIdentityAndHistory(t *testing.T) {
	entry := VS09ContractRegistry[len(VS09ContractRegistry)-1]
	data, err := fs.ReadFile(schemas.FixturesFS, "fixtures/"+entry.ValidFixture)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name     string
		mutate   func(map[string]any)
		category string
		field    string
	}{
		{
			name: "event aggregate identity",
			mutate: func(document map[string]any) {
				event := document["events"].([]any)[0].(map[string]any)
				event["aggregateId"] = "aggregate-other"
			},
			category: "reference", field: "/events/0/aggregateId",
		},
		{
			name: "event target identity",
			mutate: func(document map[string]any) {
				event := document["events"].([]any)[0].(map[string]any)
				event["workspaceId"] = "workspace-other"
			},
			category: "authority", field: "/events/0/workspaceId",
		},
		{
			name: "event history event identity",
			mutate: func(document map[string]any) {
				event := document["events"].([]any)[0].(map[string]any)
				event["eventId"] = "event-other"
			},
			category: "reference", field: "/aggregate/history/1/eventId",
		},
		{
			name: "event history approval identity",
			mutate: func(document map[string]any) {
				event := document["events"].([]any)[0].(map[string]any)
				event["approvalId"] = "approval-other"
			},
			category: "reference", field: "/aggregate/history/1/approvalId",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var document map[string]any
			if err := json.Unmarshal(data, &document); err != nil {
				t.Fatal(err)
			}
			test.mutate(document)
			mutated, err := json.Marshal(document)
			if err != nil {
				t.Fatal(err)
			}
			if err := Validate(entry.SchemaID, mutated); err != nil {
				t.Fatalf("mutation should pass structural schema validation: %v", err)
			}
			err = ValidateVS09Contract(entry.SchemaID, mutated)
			if err == nil {
				t.Fatal("single-leaf cross-field mutation unexpectedly passed semantic validation")
			}
			var semanticErr *VS09ValidationError
			if !errors.As(err, &semanticErr) {
				t.Fatalf("cross-field mutation did not produce typed semantic error: %v", err)
			}
			if semanticErr.Category != test.category || semanticErr.Field != test.field {
				t.Fatalf("cross-field mutation targeted %s %s, got %s %s", test.category, test.field, semanticErr.Category, semanticErr.Field)
			}
		})
	}
}

func TestVS09ContractRegistryValidatesAllFixtureBytes(t *testing.T) {
	if err := ValidateVS09ContractRegistry(); err != nil {
		t.Fatalf("VS-09 registry fixture validation failed: %v", err)
	}
}

func TestVS09ApprovalValidatorsRejectSingleLeafMutations(t *testing.T) {
	tests := []struct {
		name     string
		entry    VS09ContractRegistryEntry
		mutate   func(map[string]any)
		category string
		field    string
	}{
		{
			name:     "command authority",
			entry:    VS09ContractRegistry[0],
			mutate:   func(document map[string]any) { document["actorId"] = "" },
			category: "authority",
			field:    "/actorId",
		},
		{
			name:     "event reference",
			entry:    VS09ContractRegistry[1],
			mutate:   func(document map[string]any) { document["proposalId"] = "" },
			category: "reference",
			field:    "/proposalId",
		},
		{
			name:  "aggregate version",
			entry: VS09ContractRegistry[2],
			mutate: func(document map[string]any) {
				history := document["history"].([]any)
				history[1].(map[string]any)["version"] = float64(2)
			},
			category: "version",
			field:    "/history/1/version",
		},
		{
			name:  "idempotency reference",
			entry: VS09ContractRegistry[3],
			mutate: func(document map[string]any) {
				document["originalCommand"].(map[string]any)["proposalId"] = ""
			},
			category: "reference",
			field:    "/originalCommand/proposalId",
		},
		{
			name:  "outbox transition",
			entry: VS09ContractRegistry[4],
			mutate: func(document map[string]any) {
				document["deliveryState"] = "published"
			},
			category: "transition",
			field:    "/deliveryState",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			data, err := fs.ReadFile(schemas.FixturesFS, "fixtures/"+test.entry.ValidFixture)
			if err != nil {
				t.Fatal(err)
			}
			var document map[string]any
			if err := json.Unmarshal(data, &document); err != nil {
				t.Fatal(err)
			}
			test.mutate(document)
			mutated, err := json.Marshal(document)
			if err != nil {
				t.Fatal(err)
			}
			err = ValidatorForVS09Contract(test.entry.SchemaID)(mutated)
			if err == nil {
				t.Fatal("single-leaf semantic mutation unexpectedly passed")
			}
			var semanticErr *VS09ValidationError
			if !errors.As(err, &semanticErr) {
				t.Fatalf("mutation did not produce typed semantic error: %v", err)
			}
			if semanticErr.Category != test.category || semanticErr.Field != test.field {
				t.Fatalf("mutation targeted %s %s, got %s %s", test.category, test.field, semanticErr.Category, semanticErr.Field)
			}
		})
	}
}

func TestVS09ApprovalCommandOptionalPresenceMatrix(t *testing.T) {
	data, err := fs.ReadFile(schemas.FixturesFS, "fixtures/"+VS09ContractRegistry[0].ValidFixture)
	if err != nil {
		t.Fatal(err)
	}
	var base map[string]any
	if err := json.Unmarshal(data, &base); err != nil {
		t.Fatal(err)
	}

	const predecessor = "approval-previous"
	const editedText = "edited semantic meaning"
	tests := []struct {
		name          string
		decision      string
		expectedState string
		expectedVer   float64
		predecessor   *string
		editedText    *string
	}{
		{name: "approve", decision: "approve", expectedState: "none", expectedVer: 0},
		{name: "edit_then_approve", decision: "edit_then_approve", expectedState: "active", expectedVer: 1, predecessor: stringPointer(predecessor), editedText: stringPointer(editedText)},
		{name: "reject", decision: "reject", expectedState: "none", expectedVer: 0, predecessor: stringPointer(predecessor)},
		{name: "revoke", decision: "revoke", expectedState: "active", expectedVer: 1, predecessor: stringPointer(predecessor)},
		{name: "supersede", decision: "supersede", expectedState: "active", expectedVer: 1, predecessor: stringPointer(predecessor)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			document := cloneVS09CommandDocument(base)
			document["decision"] = test.decision
			document["expectedState"] = test.expectedState
			document["expectedApprovalVersion"] = test.expectedVer
			if test.predecessor == nil {
				delete(document, "predecessorApprovalId")
			} else {
				document["predecessorApprovalId"] = *test.predecessor
			}
			if test.editedText == nil {
				delete(document, "editedText")
			} else {
				document["editedText"] = *test.editedText
			}

			valid, err := json.Marshal(document)
			if err != nil {
				t.Fatal(err)
			}
			if err := ValidateVS09Contract(ApprovalCommandV2SchemaID, valid); err != nil {
				t.Fatalf("valid optional presence matrix entry rejected: %v", err)
			}

			invalidDocument := cloneVS09CommandDocument(document)
			invalidDocument["editedText"] = ""
			invalid, err := json.Marshal(invalidDocument)
			if err != nil {
				t.Fatal(err)
			}
			err = ValidateVS09Contract(ApprovalCommandV2SchemaID, invalid)
			if err == nil {
				t.Fatal("explicitly empty editedText unexpectedly passed")
			}
			var semanticErr *VS09ValidationError
			if !errors.As(err, &semanticErr) {
				t.Fatalf("empty editedText did not produce typed semantic error: %v", err)
			}
			if semanticErr.Category != "transition" || semanticErr.Field != "/editedText" {
				t.Fatalf("empty editedText targeted %s %s, want transition /editedText", semanticErr.Category, semanticErr.Field)
			}
		})
	}
}

func TestVS09ApprovalAggregateRejectsFabricatedHistoryGraph(t *testing.T) {
	data, err := fs.ReadFile(schemas.FixturesFS, "fixtures/"+VS09ContractRegistry[2].ValidFixture)
	if err != nil {
		t.Fatal(err)
	}
	var base map[string]any
	if err := json.Unmarshal(data, &base); err != nil {
		t.Fatal(err)
	}

	terminalSuccessor := func(document map[string]any, terminalState, terminalDecision, marker string) {
		history := document["history"].([]any)
		if terminalState == "rejected" {
			first := history[1].(map[string]any)
			first["state"] = terminalState
			first["decision"] = terminalDecision
			delete(document, "activeApprovalId")
		} else {
			history = append(history, map[string]any{
				"version": float64(2), "state": terminalState, "eventId": marker + "-terminal-event", "approvalId": marker + "-terminal-approval", "decision": terminalDecision,
			})
		}
		history = append(history, map[string]any{
			"version": float64(len(history)), "state": "rejected", "eventId": marker + "-event", "approvalId": marker + "-approval", "decision": "reject",
		})
		document["history"] = history
		document["version"] = float64(len(history) - 1)
		document["state"] = "rejected"
		document["lastEventId"] = marker + "-event"
		document["lastDecision"] = "reject"
		delete(document, "activeApprovalId")
	}

	tests := []struct {
		name     string
		mutate   func(map[string]any)
		category string
		field    string
	}{
		{
			name: "genesis approval id",
			mutate: func(document map[string]any) {
				document["history"].([]any)[0].(map[string]any)["approvalId"] = "fabricated-genesis-approval"
			},
			category: "reference",
			field:    "/history/0/approvalId",
		},
		{
			name: "non-genesis approval id",
			mutate: func(document map[string]any) {
				history := document["history"].([]any)
				delete(history[1].(map[string]any), "approvalId")
				history[1].(map[string]any)["state"] = "rejected"
				history[1].(map[string]any)["decision"] = "reject"
				document["history"] = history
				document["state"] = "rejected"
				document["lastDecision"] = "reject"
				delete(document, "activeApprovalId")
			},
			category: "reference",
			field:    "/history/1/approvalId",
		},
		{
			name: "successor after rejected",
			mutate: func(document map[string]any) {
				terminalSuccessor(document, "rejected", "reject", "fabricated-after-rejected")
			},
			category: "transition",
			field:    "/history/2/state",
		},
		{
			name: "successor after revoked",
			mutate: func(document map[string]any) {
				terminalSuccessor(document, "revoked", "revoke", "fabricated-after-revoked")
			},
			category: "transition",
			field:    "/history/3/state",
		},
		{
			name: "successor after superseded",
			mutate: func(document map[string]any) {
				terminalSuccessor(document, "superseded", "supersede", "fabricated-after-superseded")
			},
			category: "transition",
			field:    "/history/3/state",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			document := cloneVS09CommandDocument(base)
			test.mutate(document)
			mutated, err := json.Marshal(document)
			if err != nil {
				t.Fatal(err)
			}
			err = ValidateVS09Contract(ApprovalAggregateV2SchemaID, mutated)
			if err == nil {
				t.Fatal("fabricated aggregate history unexpectedly passed")
			}
			var semanticErr *VS09ValidationError
			if !errors.As(err, &semanticErr) {
				t.Fatalf("fabricated aggregate did not produce typed semantic error: %v", err)
			}
			if semanticErr.Category != test.category || semanticErr.Field != test.field {
				t.Fatalf("fabricated history targeted %s %s, got %s %s", test.category, test.field, semanticErr.Category, semanticErr.Field)
			}
		})
	}
}

func TestVS09ApprovalAggregateRejectsDuplicateHistoryIdentity(t *testing.T) {
	data, err := fs.ReadFile(schemas.FixturesFS, "fixtures/"+VS09ContractRegistry[2].ValidFixture)
	if err != nil {
		t.Fatal(err)
	}
	var base map[string]any
	if err := json.Unmarshal(data, &base); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		mutate func(map[string]any)
		field  string
	}{
		{
			name: "event id",
			mutate: func(document map[string]any) {
				history := document["history"].([]any)
				history[1].(map[string]any)["eventId"] = history[0].(map[string]any)["eventId"]
				document["lastEventId"] = history[0].(map[string]any)["eventId"]
			},
			field: "/history/1/eventId",
		},
		{
			name: "approval id",
			mutate: func(document map[string]any) {
				history := document["history"].([]any)
				history = append(history, map[string]any{
					"version": float64(2), "state": "active", "eventId": "event-2", "approvalId": "approval-1", "decision": "edit_then_approve",
				})
				document["history"] = history
				document["version"] = float64(2)
				document["state"] = "active"
				document["lastEventId"] = "event-2"
				document["lastDecision"] = "edit_then_approve"
				document["activeApprovalId"] = "approval-1"
			},
			field: "/history/2/approvalId",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			document := cloneVS09CommandDocument(base)
			test.mutate(document)
			mutated, err := json.Marshal(document)
			if err != nil {
				t.Fatal(err)
			}
			err = ValidateVS09Contract(ApprovalAggregateV2SchemaID, mutated)
			if err == nil {
				t.Fatal("duplicate aggregate identity unexpectedly passed")
			}
			var semanticErr *VS09ValidationError
			if !errors.As(err, &semanticErr) {
				t.Fatalf("duplicate aggregate identity did not produce typed semantic error: %v", err)
			}
			if semanticErr.Category != "reference" || semanticErr.Field != test.field {
				t.Fatalf("duplicate identity targeted reference %s, got %s %s", test.field, semanticErr.Category, semanticErr.Field)
			}
		})
	}
}

func cloneVS09CommandDocument(document map[string]any) map[string]any {
	data, _ := json.Marshal(document)
	var clone map[string]any
	_ = json.Unmarshal(data, &clone)
	return clone
}

func stringPointer(value string) *string { return &value }
