package semantic

import (
	"testing"
)

func TestInvestigateFailure_MissingPreconditions(t *testing.T) {
	_, err := InvestigateFailure(FailureTarget{}, "debug", nil, nil, FailureOptions{})
	if err == nil {
		t.Fatal("expected missing_precondition error when failure target is empty")
	}
	if err.Error() != "missing_precondition: debug query requires error, symptom, or failureEvidenceId" {
		t.Errorf("unexpected error message: %v", err)
	}

	_, err = InvestigateFailure(FailureTarget{}, "incident", nil, nil, FailureOptions{})
	if err == nil {
		t.Fatal("expected missing_precondition error for incident without traceId")
	}
	if err.Error() != "missing_precondition: incident query requires traceId or incidentEvidenceId" {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestInvestigateFailure_DebugReverseCause(t *testing.T) {
	mapIR := &SemanticMapIR{
		MapID:           "map-fail",
		GenerationID:    "gen-fail",
		ComputedBasisID: "basis-fail",
		Steps: []SemanticStep{
			{
				StepID:        "step-throw",
				Name:          "PG 결제 거부",
				TechnicalName: "PaymentGateway.charge",
			},
			{
				StepID:        "step-handle",
				Name:          "결제 재시도 핸들러",
				TechnicalName: "PaymentService.handleRetry",
			},
		},
		Edges: []SemanticEdge{
			{
				FromStepID:       "step-throw",
				ToStepID:         "step-handle",
				ToSymbolPath:     "PaymentService.handleRetry",
				Kind:             "thrown_to",
				ResolutionStatus: "verified",
			},
		},
	}

	target := FailureTarget{
		Error: "CardDeclinedException",
	}

	res, err := InvestigateFailure(target, "debug", mapIR, nil, FailureOptions{})
	if err != nil {
		t.Fatalf("InvestigateFailure failed: %v", err)
	}

	if res.Mode != "debug" {
		t.Errorf("expected mode debug, got %s", res.Mode)
	}
	if len(res.Nodes) == 0 {
		t.Fatal("expected nodes in failure path trace")
	}
	if res.Summary.Description == "" {
		t.Error("expected non-empty summary description")
	}
}

func TestInvestigateFailure_IncidentTimelineAndConflict(t *testing.T) {
	target := FailureTarget{
		IncidentTraceID: "trace-inc-99",
	}

	obs := &RuntimeObservation{
		ObservationID:  "obs-1",
		Scenario:       "prod_checkout_hang",
		Environment:    "node20-linux-x86_64",
		IsolationLevel: "no_egress_sandbox",
		TraceCoverage: TraceCoverage{
			SpansCovered: 10,
			TotalSpans:   10,
			Ratio:        1.0,
		},
	}

	res, err := InvestigateFailure(target, "incident", nil, obs, FailureOptions{})
	if err != nil {
		t.Fatalf("InvestigateFailure incident failed: %v", err)
	}

	if res.Mode != "incident" {
		t.Errorf("expected mode incident, got %s", res.Mode)
	}
	if len(res.Timeline) == 0 {
		t.Fatal("expected timeline items in incident trace")
	}
	// Verify chronological order
	for i := 1; i < len(res.Timeline); i++ {
		if res.Timeline[i].Timestamp < res.Timeline[i-1].Timestamp {
			t.Errorf("timeline items out of order: %s < %s", res.Timeline[i].Timestamp, res.Timeline[i-1].Timestamp)
		}
	}
}

func TestInvestigateFailure_TrustedLocalApprovalGuard(t *testing.T) {
	target := FailureTarget{
		IncidentTraceID: "trace-local",
	}

	// Unapproved trusted_local
	obsUnapproved := &RuntimeObservation{
		ObservationID:  "obs-unapproved",
		Scenario:       "local_test",
		Environment:    "darwin-arm64",
		IsolationLevel: "trusted_local",
		TraceCoverage: TraceCoverage{
			SpansCovered: 1,
			TotalSpans:   1,
			Ratio:        1.0,
		},
		TrustedLocalApproval: nil,
	}

	_, err := InvestigateFailure(target, "incident", nil, obsUnapproved, FailureOptions{})
	if err == nil {
		t.Fatal("expected failure when trusted_local has no approval")
	}
	if err.Error() != "blocked: trusted_local execution requires explicit user approval" {
		t.Errorf("unexpected error: %v", err)
	}
}
