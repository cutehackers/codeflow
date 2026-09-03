package semantic

import (
	"encoding/json"
	"errors"
	"testing"

	"codeflow/internal/contractharness"
	"codeflow/internal/harvest"
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
