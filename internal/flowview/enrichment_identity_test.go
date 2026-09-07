package flowview

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"codeflow/internal/protocol"
	"codeflow/internal/semantic"
)

func TestFlowViewSemanticEnrichmentRequiresExactCachedIdentity(t *testing.T) {
	mapA := &semantic.SemanticMapIR{GenerationID: "generation-a", ComputedBasisID: "basis-a"}
	mapB := &semantic.SemanticMapIR{GenerationID: "generation-b", ComputedBasisID: "basis-b"}
	var factoryCalls atomic.Int32
	srv := &Server{
		mapCache: map[string]*semantic.SemanticMapIR{
			mapA.GenerationID:    mapA,
			mapA.ComputedBasisID: mapA,
			mapB.GenerationID:    mapB,
			mapB.ComputedBasisID: mapB,
		},
		modelHostFactory: func(context.Context) (*protocol.ModelHost, error) {
			factoryCalls.Add(1)
			return nil, errors.New("factory must not run")
		},
	}

	cases := []struct {
		name, generationID, basisID string
	}{
		{name: "unknown generation", generationID: "generation-unknown"},
		{name: "conflicting generation and basis", generationID: mapA.GenerationID, basisID: mapB.ComputedBasisID},
		{name: "multiple cached generations without identity"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := srv.cachedSemanticMap(tc.generationID, tc.basisID); got != nil {
				t.Fatalf("cached map = %+v, want unavailable", got)
			}
			body, err := json.Marshal(map[string]string{"generationId": tc.generationID, "computedBasisId": tc.basisID})
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodPost, "/api/semantic/enrich", bytes.NewReader(body))
			recorder := httptest.NewRecorder()
			srv.handleSemanticEnrichment(recorder, req)
			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", recorder.Code)
			}
			var result semantic.EnrichmentResult
			if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if result.State.Status != "unavailable" || result.Fallback != nil {
				t.Fatalf("result = %+v, want unavailable without fallback authority", result)
			}
		})
	}
	if got := factoryCalls.Load(); got != 0 {
		t.Fatalf("factory calls = %d, want 0", got)
	}
}
