package flowview

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// VS09-BR09/A11: an authenticated different-key loser receives the
// committed aggregate version through the public HTTP error payload.
func TestApprovalRESTConflictReturnsCommittedVersionWithoutMutation(t *testing.T) {
	srv, _, enrichment := newFlowViewApprovalHistoryFixture(t)
	defer srv.Shutdown(context.Background())
	srv.Start()
	before, _ := getFlowViewApprovalHistory(t, srv, enrichment.Proposal.ProposalID, enrichment.Pack.EvidencePackID)
	headBefore := srv.hub.headSeq
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/semantic/approve?token="+url.QueryEscape(srv.AuthToken()), strings.NewReader(flowViewApprovalBody(enrichment, "different-key-after-winner", "approve", false, "")))
	req.Host = "127.0.0.1"
	response := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(response, req)
	if response.Code != http.StatusConflict {
		t.Fatalf("status=%d, body=%s", response.Code, response.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["code"] != "approval_conflict" || payload["message"] != "approval command could not be executed" || payload["currentVersion"] != float64(1) || len(payload) != 3 {
		t.Fatalf("expected bounded conflict with committed version 1, got %s", response.Body.String())
	}
	after, _ := getFlowViewApprovalHistory(t, srv, enrichment.Proposal.ProposalID, enrichment.Pack.EvidencePackID)
	if !bytes.Equal(before, after) || srv.hub.headSeq != headBefore {
		t.Fatal("different-key loser changed durable history or broadcast another event")
	}
}
