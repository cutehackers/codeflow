package semantic

import (
	"encoding/json"
	"errors"
	"testing"

	"codeflow/internal/collector/contractharness"
	"codeflow/internal/collector/harvest"
)

// TestVS02A2_QueryPreconditionsAndAmbiguity tests criterion VS02-A2:
// IF feature query의 시작 조건이 없거나 여러 target이 동등하게 일치하면,
// THEN THE system SHALL 각각 missing_precondition 또는 ambiguous_target을 반환하고
// 임의의 scope를 선택하지 않는다.
func TestVS02A2_QueryPreconditionsAndAmbiguity(t *testing.T) {
	// 1. Missing precondition when no start condition provided
	emptyQuery := TaskViewQuery{
		SchemaID:      "https://codeflow.local/schemas/task-view-query.schema.json",
		SchemaVersion: 1,
		Mode:          "feature",
		Feature:       &FeatureQueryParams{}, // empty
	}

	_, err := ResolveFeatureQueryTarget(&emptyQuery, nil)
	if err == nil {
		t.Fatal("expected error for empty feature query")
	}
	var qErr *QueryError
	if !errors.As(err, &qErr) || qErr.Code != ErrCodeMissingPrecondition {
		t.Fatalf("expected error code %q, got %v", ErrCodeMissingPrecondition, err)
	}

	// 1-b. Unmatched natural language query returns candidate targets
	unmatchedQuery := TaskViewQuery{
		SchemaID:      "https://codeflow.local/schemas/task-view-query.schema.json",
		SchemaVersion: 1,
		Mode:          "feature",
		Feature: &FeatureQueryParams{
			Request: "세균전 게임 흐름",
		},
	}
	sampleCands := []harvest.Candidate{
		{EntrySymbolPath: "game/controller.go#Start", IntentSignals: harvest.IntentSignals{DerivedName: "Start"}},
	}
	_, err = ResolveFeatureQueryTarget(&unmatchedQuery, sampleCands)
	if err == nil {
		t.Fatal("expected error for unmatched query")
	}
	if !errors.As(err, &qErr) || len(qErr.CandidateTargets) != 1 || qErr.CandidateTargets[0] != "game/controller.go#Start" {
		t.Fatalf("expected CandidateTargets to contain available candidates, got %+v", qErr)
	}

	// 2. Ambiguous target when multiple candidates match equally
	ambiguousQuery := TaskViewQuery{
		SchemaID:      "https://codeflow.local/schemas/task-view-query.schema.json",
		SchemaVersion: 1,
		Mode:          "feature",
		Feature: &FeatureQueryParams{
			Request: "checkout",
		},
	}
	// Two candidates with identical score matching "checkout"
	mockCandidates := []harvest.Candidate{
		{
			CandidateID:     "cand-1",
			EntrySymbolPath: "app/checkout/QuickCheckout.submit",
			Score:           0.85,
			IntentSignals: harvest.IntentSignals{
				DerivedName: "quick checkout",
			},
		},
		{
			CandidateID:     "cand-2",
			EntrySymbolPath: "app/checkout/StandardCheckout.submit",
			Score:           0.85,
			IntentSignals: harvest.IntentSignals{
				DerivedName: "standard checkout",
			},
		},
	}

	_, err = ResolveFeatureQueryTarget(&ambiguousQuery, mockCandidates)
	if err == nil {
		t.Fatal("expected ambiguous_target error when multiple equal candidates match")
	}
	if !errors.As(err, &qErr) || qErr.Code != ErrCodeAmbiguousTarget {
		t.Fatalf("expected error code %q, got %v", ErrCodeAmbiguousTarget, err)
	}
	if len(qErr.CandidateTargets) != 2 {
		t.Errorf("expected 2 candidate targets listed in ambiguous error, got %d", len(qErr.CandidateTargets))
	}

	// 3. Unique match resolves successfully without guessing
	uniqueQuery := TaskViewQuery{
		SchemaID:      "https://codeflow.local/schemas/task-view-query.schema.json",
		SchemaVersion: 1,
		Mode:          "feature",
		Feature: &FeatureQueryParams{
			EntrySymbol: "app/checkout/QuickCheckout.submit",
		},
	}
	target, err := ResolveFeatureQueryTarget(&uniqueQuery, mockCandidates)
	if err != nil {
		t.Fatalf("unexpected error for explicit entrySymbol: %v", err)
	}
	if target.EntrySymbolPath != "app/checkout/QuickCheckout.submit" {
		t.Errorf("expected target %q, got %q", "app/checkout/QuickCheckout.submit", target.EntrySymbolPath)
	}

	// 4. Schema validation for valid query
	data, err := json.Marshal(uniqueQuery)
	if err != nil {
		t.Fatal(err)
	}
	if err := contractharness.ValidateTaskViewQuery(data); err != nil {
		t.Fatalf("query schema validation failed: %v", err)
	}
}

func TestResolveFeatureQueryTarget_ZeroEntrypoints(t *testing.T) {
	// Query with request text but 0 candidates in repository
	query := TaskViewQuery{
		SchemaID:      "https://codeflow.local/schemas/task-view-query.schema.json",
		SchemaVersion: 1,
		Mode:          "feature",
		Feature: &FeatureQueryParams{
			Request: "사용자 로그인 흐름",
		},
	}

	_, err := ResolveFeatureQueryTarget(&query, nil)
	if err == nil {
		t.Fatal("expected error when candidates list is empty")
	}

	var qErr *QueryError
	if !errors.As(err, &qErr) {
		t.Fatalf("expected *QueryError, got %T: %v", err, err)
	}
	if qErr.Code != ErrCodeNoEntrypointsFound {
		t.Fatalf("expected error code %q, got %q", ErrCodeNoEntrypointsFound, qErr.Code)
	}

	// But if an explicit entrySymbol is provided, it should succeed directly even with 0 candidates
	explicitQuery := TaskViewQuery{
		SchemaID:      "https://codeflow.local/schemas/task-view-query.schema.json",
		SchemaVersion: 1,
		Mode:          "feature",
		Feature: &FeatureQueryParams{
			EntrySymbol: "auth/service.go#Login",
		},
	}
	target, err := ResolveFeatureQueryTarget(&explicitQuery, nil)
	if err != nil {
		t.Fatalf("explicit entrySymbol should bypass empty candidates, got: %v", err)
	}
	if target.EntrySymbolPath != "auth/service.go#Login" {
		t.Fatalf("expected entrySymbol %q, got %q", "auth/service.go#Login", target.EntrySymbolPath)
	}
}
