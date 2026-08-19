package expectation

import (
	"testing"

	"codeflow/core/internal/flowir"
)

func TestVerifyRequiresResultsCausalRelationsAndApprovedDebt(t *testing.T) {
	max := 1
	doc := flowir.Document{
		Current:     flowir.Flow{ID: "route:/join"},
		Facts:       []flowir.Fact{{ID: "result", Kind: "visible_result", Object: "route:/auth", Status: flowir.Observed}},
		CausalEdges: []flowir.CausalEdge{{Kind: "changes_state", Status: flowir.Observed}},
		Unknowns:    []flowir.UnknownDetail{{Reason: "conditional_route_alternative"}},
	}
	file := File{Version: "1", Flows: map[string]Flow{"route:/join": {
		RequiredResults: []string{"route:/auth"}, RequiredCausalKinds: []string{"changes_state"}, AllowedDebtReasons: []string{"conditional_route_alternative"}, MaxOpenDebt: &max,
	}}}
	if report := Verify(file, doc); !report.Ready {
		t.Fatalf("valid expectation rejected: %#v", report)
	}
	doc.Unknowns = append(doc.Unknowns, flowir.UnknownDetail{Reason: "unsupported_riverpod_pattern"})
	report := Verify(file, doc)
	if report.Ready || len(report.Failures) != 2 {
		t.Fatalf("new/unbudgeted debt must fail: %#v", report)
	}
}
