package flowview

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"codeflow/internal/contractharness"
	"codeflow/internal/protocol"
	"codeflow/internal/semantic"
	"codeflow/internal/slicing"
	"codeflow/internal/workspace"
)

func TestApprovalHistoryEndpointRejectsWrongMethodWithBoundedJSON(t *testing.T) {
	srv, err := NewServer(Config{RepoRoot: t.TempDir(), Port: 0, AuthToken: "approval-history-endpoint-token"})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}
	defer func() { _ = srv.Shutdown(context.Background()) }()

	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/semantic/approval-history?proposalId=proposal-1&evidencePackId=pack-1&token="+srv.AuthToken(), nil)
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, body = %s, want 405", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("content type = %q, want application/json", got)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode bounded method error: %v, body=%s", err, rec.Body.String())
	}
	if body["code"] != "method_not_allowed" || body["message"] != "method not allowed" {
		t.Fatalf("method error body = %#v, want bounded method_not_allowed", body)
	}
}

func TestApprovalHistoryEndpointReturnsCanonicalHistoryAfterDurableCommitAndRestart(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") == "flowview" {
		t.Skip("helper process")
	}
	srv, root, result := newFlowViewApprovalHistoryFixture(t)

	srv.Start()
	_, firstResult := getFlowViewApprovalHistory(t, srv, result.Proposal.ProposalID, result.Pack.EvidencePackID)
	if firstResult.Freshness != "current" {
		t.Fatalf("initial endpoint freshness = %q, want current", firstResult.Freshness)
	}
	missingBody, missingStatus, missingContentType := requestFlowViewApprovalHistory(t, srv, result.Proposal.ProposalID+"-missing", result.Pack.EvidencePackID)
	if missingStatus != http.StatusNotFound || missingContentType != "application/json" || !strings.Contains(string(missingBody), `"code":"approval_unavailable"`) {
		t.Fatalf("missing exact approval history response = status %d content-type %q body=%s, want bounded 404 approval_unavailable", missingStatus, missingContentType, missingBody)
	}
	transactionPath := filepath.Join(root, ".codeflow", "approval-transactions", "approval-transactions.sqlite3")
	transactionBefore, err := os.ReadFile(transactionPath)
	if err != nil {
		t.Fatalf("read approval transaction before corruption: %v", err)
	}
	if err := os.WriteFile(transactionPath, []byte("corrupt approval history storage"), 0o600); err != nil {
		t.Fatalf("corrupt approval transaction for endpoint: %v", err)
	}
	corruptBody, corruptStatus, corruptContentType := requestFlowViewApprovalHistory(t, srv, result.Proposal.ProposalID, result.Pack.EvidencePackID)
	if err := os.WriteFile(transactionPath, transactionBefore, 0o600); err != nil {
		t.Fatalf("restore approval transaction after endpoint corruption test: %v", err)
	}
	if corruptStatus != http.StatusBadRequest || corruptContentType != "application/json" || !strings.Contains(string(corruptBody), `"code":"approval_invalid"`) {
		t.Fatalf("corrupt approval history response = status %d content-type %q body=%s, want bounded 400 approval_invalid", corruptStatus, corruptContentType, corruptBody)
	}
	proofBefore, err := srv.storage.ReadValidatedActiveProofBundle()
	if err != nil {
		t.Fatalf("read proof before live-head advance: %v", err)
	}
	pointerBefore, err := os.ReadFile(filepath.Join(srv.storage.BaseDir(), "active-pointer.json"))
	if err != nil {
		t.Fatalf("read pointer before live-head advance: %v", err)
	}
	transactionBefore, err = os.ReadFile(transactionPath)
	if err != nil {
		t.Fatalf("read transaction before live-head advance: %v", err)
	}
	if _, _, err := srv.engine.ApplyVersionedEdit(context.Background(), workspace.EditRequest{Path: "main.go", Content: []byte("package main\n\nfunc Changed() {}\n"), DocumentVersion: 2, Source: workspace.SourceIDEVersioned}); err != nil {
		t.Fatalf("advance live head: %v", err)
	}
	historicalBody, historicalResult := getFlowViewApprovalHistory(t, srv, result.Proposal.ProposalID, result.Pack.EvidencePackID)
	if historicalResult.Freshness != "historical" {
		t.Fatalf("historical endpoint freshness = %q, want historical body=%s", historicalResult.Freshness, historicalBody)
	}
	transactionAfter, err := os.ReadFile(transactionPath)
	if err != nil {
		t.Fatalf("read transaction after historical query: %v", err)
	}
	if !bytes.Equal(transactionBefore, transactionAfter) {
		t.Fatal("historical approval history query changed transaction bytes")
	}
	pointerAfter, err := os.ReadFile(filepath.Join(srv.storage.BaseDir(), "active-pointer.json"))
	if err != nil {
		t.Fatalf("read pointer after historical query: %v", err)
	}
	if !bytes.Equal(pointerBefore, pointerAfter) {
		t.Fatal("historical approval history query changed active pointer bytes")
	}
	proofAfter, err := srv.storage.ReadValidatedActiveProofBundle()
	if err != nil {
		t.Fatalf("read proof after historical query: %v", err)
	}
	if proofBefore == nil || proofAfter == nil || !reflect.DeepEqual(proofBefore.Manifest, proofAfter.Manifest) || !reflect.DeepEqual(proofBefore.Pointer, proofAfter.Pointer) || !bytes.Equal(proofBefore.ManifestBytes, proofAfter.ManifestBytes) || !bytes.Equal(proofBefore.SemanticMap, proofAfter.SemanticMap) || !bytes.Equal(proofBefore.AnalyzerResult, proofAfter.AnalyzerResult) || !bytes.Equal(proofBefore.SemanticDelta, proofAfter.SemanticDelta) {
		t.Fatal("historical approval history query changed validated proof bytes")
	}
	if err := srv.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown first server: %v", err)
	}

	restarted, err := NewServer(Config{RepoRoot: root, Port: 0, AuthToken: "approval-history-restart-token"})
	if err != nil {
		t.Fatalf("restart FlowView server: %v", err)
	}
	defer func() { _ = restarted.Shutdown(context.Background()) }()
	restarted.Start()
	secondBody, secondResult := getFlowViewApprovalHistory(t, restarted, result.Proposal.ProposalID, result.Pack.EvidencePackID)
	if !bytes.Equal(historicalBody, secondBody) {
		t.Fatalf("restart changed historical canonical history bytes:\nfirst=%s\nsecond=%s", historicalBody, secondBody)
	}
	if !reflect.DeepEqual(historicalResult, secondResult) {
		t.Fatalf("restart changed historical history result:\nfirst=%+v\nsecond=%+v", historicalResult, secondResult)
	}
}

func TestApprovalHistoryEndpointRejectsStrictlyInvalidQueriesWithoutApprovalState(t *testing.T) {
	cases := []struct {
		name  string
		query string
	}{
		{name: "missing proposal", query: "evidencePackId=pack-1"},
		{name: "missing evidence pack", query: "proposalId=proposal-1"},
		{name: "blank proposal", query: "proposalId=%20&evidencePackId=pack-1"},
		{name: "blank evidence pack", query: "proposalId=proposal-1&evidencePackId=%20"},
		{name: "duplicate proposal", query: "proposalId=proposal-1&proposalId=proposal-2&evidencePackId=pack-1"},
		{name: "duplicate evidence pack", query: "proposalId=proposal-1&evidencePackId=pack-1&evidencePackId=pack-2"},
		{name: "oversized proposal", query: "proposalId=" + strings.Repeat("p", 257) + "&evidencePackId=pack-1"},
		{name: "oversized evidence pack", query: "proposalId=proposal-1&evidencePackId=" + strings.Repeat("p", 257)},
		{name: "malformed encoding", query: "proposalId=proposal-%ZZ&evidencePackId=pack-1"},
		{name: "unknown repository root", query: "proposalId=proposal-1&evidencePackId=pack-1&repoRoot=/tmp/escape"},
		{name: "unknown workspace", query: "proposalId=proposal-1&evidencePackId=pack-1&workspaceId=other"},
		{name: "unknown actor", query: "proposalId=proposal-1&evidencePackId=pack-1&actorId=other"},
		{name: "duplicate token", query: "proposalId=proposal-1&evidencePackId=pack-1&token=one&token=two"},
		{name: "blank token", query: "proposalId=proposal-1&evidencePackId=pack-1&token="},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			srv, err := NewServer(Config{RepoRoot: root, Port: 0, AuthToken: "approval-history-query-token"})
			if err != nil {
				t.Fatalf("NewServer failed: %v", err)
			}
			defer func() { _ = srv.Shutdown(context.Background()) }()
			req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/semantic/approval-history?"+tc.query, nil)
			req.Header.Set("X-CodeFlow-Token", srv.AuthToken())
			rec := httptest.NewRecorder()
			srv.httpServer.Handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("query status = %d, body = %s, want bounded 400", rec.Code, rec.Body.String())
			}
			if rec.Header().Get("Content-Type") != "application/json" || !strings.Contains(rec.Body.String(), `"code":"approval_invalid"`) {
				t.Fatalf("query error = content-type %q body %s, want bounded approval_invalid JSON", rec.Header().Get("Content-Type"), rec.Body.String())
			}
			if strings.Contains(rec.Body.String(), tc.query) || strings.Contains(rec.Body.String(), "/tmp/escape") || strings.Contains(rec.Body.String(), "other") {
				t.Fatalf("query error leaked request data: %s", rec.Body.String())
			}
			assertApprovalHistoryStateAbsent(t, root)
		})
	}
}

func TestApprovalHistoryEndpointUsesCanonicalLifecycleIDValidation(t *testing.T) {
	maxUnicodeID := strings.Repeat("é", 256)
	overUnicodeID := strings.Repeat("é", 257)
	invalidUTF8 := string([]byte{0xff})
	cases := []struct {
		name       string
		proposalID string
		packID     string
		wantStatus int
		wantCode   string
	}{
		{name: "proposal accepts 256 unicode code points", proposalID: maxUnicodeID, packID: "pack-1", wantStatus: http.StatusNotFound, wantCode: "approval_unavailable"},
		{name: "evidence pack accepts 256 unicode code points", proposalID: "proposal-1", packID: maxUnicodeID, wantStatus: http.StatusNotFound, wantCode: "approval_unavailable"},
		{name: "proposal rejects 257 unicode code points", proposalID: overUnicodeID, packID: "pack-1", wantStatus: http.StatusBadRequest, wantCode: "approval_invalid"},
		{name: "evidence pack rejects 257 unicode code points", proposalID: "proposal-1", packID: overUnicodeID, wantStatus: http.StatusBadRequest, wantCode: "approval_invalid"},
		{name: "rejects invalid UTF-8", proposalID: invalidUTF8, packID: "pack-1", wantStatus: http.StatusBadRequest, wantCode: "approval_invalid"},
		{name: "rejects leading unicode whitespace", proposalID: "\u2003proposal-1", packID: "pack-1", wantStatus: http.StatusBadRequest, wantCode: "approval_invalid"},
		{name: "rejects trailing unicode whitespace", proposalID: "proposal-1\u2003", packID: "pack-1", wantStatus: http.StatusBadRequest, wantCode: "approval_invalid"},
		{name: "rejects control character", proposalID: "proposal-\x01", packID: "pack-1", wantStatus: http.StatusBadRequest, wantCode: "approval_invalid"},
		{name: "rejects slash", proposalID: "proposal/1", packID: "pack-1", wantStatus: http.StatusBadRequest, wantCode: "approval_invalid"},
		{name: "rejects backslash", proposalID: "proposal\\1", packID: "pack-1", wantStatus: http.StatusBadRequest, wantCode: "approval_invalid"},
		{name: "rejects NUL", proposalID: "proposal\x001", packID: "pack-1", wantStatus: http.StatusBadRequest, wantCode: "approval_invalid"},
		{name: "rejects DEL", proposalID: "proposal\x7f1", packID: "pack-1", wantStatus: http.StatusBadRequest, wantCode: "approval_invalid"},
		{name: "rejects dot dot", proposalID: "proposal..1", packID: "pack-1", wantStatus: http.StatusBadRequest, wantCode: "approval_invalid"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			srv, err := NewServer(Config{RepoRoot: root, Port: 0, AuthToken: "approval-history-id-validation-token"})
			if err != nil {
				t.Fatalf("NewServer failed: %v", err)
			}
			defer func() { _ = srv.Shutdown(context.Background()) }()
			rawQuery := "proposalId=" + url.QueryEscape(tc.proposalID) + "&evidencePackId=" + url.QueryEscape(tc.packID)
			req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/semantic/approval-history?"+rawQuery, nil)
			req.Header.Set("X-CodeFlow-Token", srv.AuthToken())
			rec := httptest.NewRecorder()
			srv.httpServer.Handler.ServeHTTP(rec, req)
			body := rec.Body.String()
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, body = %s, want %d", rec.Code, body, tc.wantStatus)
			}
			if rec.Header().Get("Content-Type") != "application/json" || !strings.Contains(body, `"code":"`+tc.wantCode+`"`) {
				t.Fatalf("response = content-type %q body %s, want bounded %s JSON", rec.Header().Get("Content-Type"), body, tc.wantCode)
			}
			if tc.wantStatus == http.StatusBadRequest {
				assertApprovalHistoryStateAbsent(t, root)
			}
		})
	}
}

func TestFlowViewApprovalHistorySecurityRequiresExactLoopbackAuthority(t *testing.T) {
	cases := []struct {
		name       string
		host       string
		origin     string
		referer    string
		wantStatus int
	}{
		{name: "exact localhost with port", host: "localhost:43125", wantStatus: http.StatusNotFound},
		{name: "exact IPv4 with port", host: "127.0.0.1:43125", wantStatus: http.StatusNotFound},
		{name: "exact bare IPv6 loopback", host: "::1", wantStatus: http.StatusNotFound},
		{name: "exact bracketed IPv6 loopback", host: "[::1]", wantStatus: http.StatusNotFound},
		{name: "exact bracketed IPv6 loopback with port", host: "[::1]:43125", wantStatus: http.StatusNotFound},
		{name: "host suffix attack", host: "localhost.evil.example", wantStatus: http.StatusForbidden},
		{name: "host IPv4 suffix attack", host: "127.0.0.1.evil.example", wantStatus: http.StatusForbidden},
		{name: "host nested brackets", host: "[[::1]]", wantStatus: http.StatusForbidden},
		{name: "host trailing bracket", host: "[::1]]", wantStatus: http.StatusForbidden},
		{name: "host leading nested bracket", host: "[[::1]", wantStatus: http.StatusForbidden},
		{name: "host trailing bracket after port", host: "[::1]:80]", wantStatus: http.StatusForbidden},
		{name: "origin suffix attack", host: "localhost:43125", origin: "http://localhost.evil.example", wantStatus: http.StatusForbidden},
		{name: "origin exact localhost with port", host: "localhost:43125", origin: "http://localhost:43125", wantStatus: http.StatusNotFound},
		{name: "origin exact IPv4 with port", host: "127.0.0.1:43125", origin: "http://127.0.0.1:43125", wantStatus: http.StatusNotFound},
		{name: "origin exact bracketed IPv6", host: "[::1]", origin: "http://[::1]", wantStatus: http.StatusNotFound},
		{name: "origin nested brackets", host: "[::1]", origin: "http://[[::1]]", wantStatus: http.StatusForbidden},
		{name: "origin trailing bracket", host: "[::1]", origin: "http://[::1]]", wantStatus: http.StatusForbidden},
		{name: "origin leading nested bracket", host: "[::1]", origin: "http://[[::1]", wantStatus: http.StatusForbidden},
		{name: "origin trailing bracket after port", host: "[::1]", origin: "http://[::1]:80]", wantStatus: http.StatusForbidden},
		{name: "referer suffix attack", host: "localhost:43125", referer: "http://localhost.evil.example/view", wantStatus: http.StatusForbidden},
		{name: "referer exact bracketed IPv6", host: "[::1]", referer: "http://[::1]/view", wantStatus: http.StatusNotFound},
		{name: "referer nested brackets", host: "[::1]", referer: "http://[[::1]]/view", wantStatus: http.StatusForbidden},
		{name: "referer trailing bracket", host: "[::1]", referer: "http://[::1]]/view", wantStatus: http.StatusForbidden},
		{name: "referer leading nested bracket", host: "[::1]", referer: "http://[[::1]/view", wantStatus: http.StatusForbidden},
		{name: "referer trailing bracket after port", host: "[::1]", referer: "http://[::1]:80]/view", wantStatus: http.StatusForbidden},
		{name: "invalid host port", host: "localhost:not-a-port", wantStatus: http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			srv, err := NewServer(Config{RepoRoot: root, Port: 0, AuthToken: "approval-history-security-token"})
			if err != nil {
				t.Fatalf("NewServer failed: %v", err)
			}
			defer func() { _ = srv.Shutdown(context.Background()) }()
			req := httptest.NewRequest(http.MethodGet, "http://flowview.test/api/semantic/approval-history?proposalId=proposal-1&evidencePackId=pack-1", nil)
			if req == nil {
				t.Fatal("build request returned nil")
			}
			req.Host = tc.host
			req.Header.Set("X-CodeFlow-Token", srv.AuthToken())
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			if tc.referer != "" {
				req.Header.Set("Referer", tc.referer)
			}
			if req.Host != tc.host {
				t.Fatalf("request Host = %q, want raw %q", req.Host, tc.host)
			}
			if tc.origin != "" && req.Header.Get("Origin") != tc.origin {
				t.Fatalf("request Origin = %q, want raw %q", req.Header.Get("Origin"), tc.origin)
			}
			if tc.referer != "" && req.Header.Get("Referer") != tc.referer {
				t.Fatalf("request Referer = %q, want raw %q", req.Header.Get("Referer"), tc.referer)
			}
			response := httptest.NewRecorder()
			srv.httpServer.Handler.ServeHTTP(response, req)
			body := response.Body.String()
			if response.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d, body=%s", response.Code, tc.wantStatus, body)
			}
			if tc.wantStatus == http.StatusForbidden {
				assertApprovalHistoryStateAbsent(t, root)
				return
			}
			if response.Header().Get("Content-Type") != "application/json" || !strings.Contains(body, `"code":"approval_unavailable"`) {
				t.Fatalf("accepted loopback authority response has content-type=%q body=%s, want bounded unavailable JSON", response.Header().Get("Content-Type"), body)
			}
		})
	}
}

func TestFlowViewApprovalHistoryRejectsMissingOrWrongTokenBeforeRead(t *testing.T) {
	cases := []struct {
		name       string
		header     string
		queryToken string
	}{
		{name: "missing token"},
		{name: "wrong header token", header: "wrong-token"},
		{name: "wrong query token", queryToken: "wrong-token"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			srv, err := NewServer(Config{RepoRoot: root, Port: 0, AuthToken: "approval-history-auth-token"})
			if err != nil {
				t.Fatalf("NewServer failed: %v", err)
			}
			defer func() { _ = srv.Shutdown(context.Background()) }()
			httpServer := httptest.NewServer(srv.httpServer.Handler)
			defer httpServer.Close()
			endpoint := httpServer.URL + "/api/semantic/approval-history?proposalId=proposal-1&evidencePackId=pack-1"
			if tc.queryToken != "" {
				endpoint += "&token=" + url.QueryEscape(tc.queryToken)
			}
			req, err := http.NewRequest(http.MethodGet, endpoint, nil)
			if err != nil {
				t.Fatalf("build request: %v", err)
			}
			if tc.header != "" {
				req.Header.Set("X-CodeFlow-Token", tc.header)
			}
			response, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("approval history request: %v", err)
			}
			body := readHTTPBody(t, response)
			response.Body.Close()
			if response.StatusCode != http.StatusUnauthorized {
				t.Fatalf("status = %d, body = %s, want 401", response.StatusCode, body)
			}
			if strings.Contains(body, "proposal-1") || strings.Contains(body, "pack-1") || strings.Contains(body, root) {
				t.Fatalf("authorization error leaked request or filesystem data: %s", body)
			}
			assertApprovalHistoryStateAbsent(t, root)
		})
	}
}

func TestFlowViewApprovalHistoryAuthorizationPrecedesDurableRead(t *testing.T) {
	root := t.TempDir()
	srv, err := NewServer(Config{RepoRoot: root, Port: 0, AuthToken: "approval-history-auth-order-token"})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}
	defer func() { _ = srv.Shutdown(context.Background()) }()
	transactionPath := filepath.Join(root, ".codeflow", "approval-transactions", "approval-transactions.sqlite3")
	if err := os.MkdirAll(filepath.Dir(transactionPath), 0o700); err != nil {
		t.Fatalf("create approval transaction fixture: %v", err)
	}
	if err := os.WriteFile(transactionPath, []byte("corrupt approval database"), 0o600); err != nil {
		t.Fatalf("write corrupt approval transaction fixture: %v", err)
	}
	before, err := os.ReadFile(transactionPath)
	if err != nil {
		t.Fatalf("read approval transaction fixture: %v", err)
	}
	srv.approvalGate = nil
	httpServer := httptest.NewServer(srv.httpServer.Handler)
	defer httpServer.Close()
	req, err := http.NewRequest(http.MethodGet, httpServer.URL+"/api/semantic/approval-history?proposalId=proposal-1&evidencePackId=pack-1", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("X-CodeFlow-Token", srv.AuthToken())
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("approval history request: %v", err)
	}
	body := readHTTPBody(t, response)
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized || !strings.Contains(body, "Unauthorized") && !strings.Contains(body, "approval_unauthenticated") {
		t.Fatalf("authorization-before-read response = %d body=%s, want bounded unauthorized result", response.StatusCode, body)
	}
	after, err := os.ReadFile(transactionPath)
	if err != nil {
		t.Fatalf("read approval transaction after unauthorized request: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("unauthorized approval history request changed transaction bytes")
	}
}

func TestFlowViewApprovalHistoryMapsCanceledRequestToBoundedUnavailable(t *testing.T) {
	srv, err := NewServer(Config{RepoRoot: t.TempDir(), Port: 0, AuthToken: "approval-history-cancel-token"})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}
	defer func() { _ = srv.Shutdown(context.Background()) }()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/semantic/approval-history?proposalId=proposal-1&evidencePackId=pack-1", nil).WithContext(ctx)
	req.Header.Set("X-CodeFlow-Token", srv.AuthToken())
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestTimeout {
		t.Fatalf("canceled request status = %d, body = %s, want 408", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Content-Type") != "application/json" || !strings.Contains(rec.Body.String(), `"code":"approval_unavailable"`) {
		t.Fatalf("canceled request response = content-type %q body %s, want bounded approval_unavailable JSON", rec.Header().Get("Content-Type"), rec.Body.String())
	}
}

func readHTTPBody(t *testing.T, response *http.Response) string {
	t.Helper()
	var body bytes.Buffer
	if _, err := body.ReadFrom(response.Body); err != nil {
		t.Fatalf("read HTTP response: %v", err)
	}
	return body.String()
}

func assertApprovalHistoryStateAbsent(t *testing.T, root string) {
	t.Helper()
	transactionPath := filepath.Join(root, ".codeflow", "approval-transactions", "approval-transactions.sqlite3")
	if _, err := os.Stat(transactionPath); !os.IsNotExist(err) {
		t.Fatalf("approval transaction state after invalid query: err=%v, want absent", err)
	}
	proposalDir := filepath.Join(root, ".codeflow", "semantic-proposals")
	if _, err := os.Stat(proposalDir); !os.IsNotExist(err) {
		t.Fatalf("proposal state after invalid query: err=%v, want absent", err)
	}
}

func getFlowViewApprovalHistory(t *testing.T, srv *Server, proposalID, evidencePackID string) ([]byte, semantic.ApprovalHistoryResult) {
	t.Helper()
	body, status, contentType := requestFlowViewApprovalHistory(t, srv, proposalID, evidencePackID)
	if status != http.StatusOK {
		t.Fatalf("approval history status = %d, body = %s", status, body)
	}
	if contentType != "application/json" {
		t.Fatalf("approval history content type = %q, want application/json", contentType)
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(body, &document); err != nil {
		t.Fatalf("decode approval history JSON: %v", err)
	}
	wantKeys := map[string]struct{}{"schemaId": {}, "schemaVersion": {}, "target": {}, "events": {}, "aggregate": {}, "freshness": {}}
	if len(document) != len(wantKeys) {
		t.Fatalf("approval history keys = %v, want exactly six canonical keys", document)
	}
	for key := range wantKeys {
		if _, ok := document[key]; !ok {
			t.Fatalf("approval history missing canonical key %q", key)
		}
	}
	if err := contractharness.ValidateVS09Contract(semantic.ApprovalHistoryV1SchemaID, body); err != nil {
		t.Fatalf("approval history contract validation: %v", err)
	}
	var result semantic.ApprovalHistoryResult
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("decode canonical approval history result: %v", err)
	}
	return body, result
}

func requestFlowViewApprovalHistory(t *testing.T, srv *Server, proposalID, evidencePackID string) ([]byte, int, string) {
	t.Helper()
	endpoint := "http://" + srv.Addr() + "/api/semantic/approval-history?proposalId=" + url.QueryEscape(proposalID) + "&evidencePackId=" + url.QueryEscape(evidencePackID) + "&token=" + url.QueryEscape(srv.AuthToken())
	response, err := http.Get(endpoint)
	if err != nil {
		t.Fatalf("GET approval history: %v", err)
	}
	defer response.Body.Close()
	var body bytes.Buffer
	if _, err := body.ReadFrom(response.Body); err != nil {
		t.Fatalf("read approval history response: %v", err)
	}
	return body.Bytes(), response.StatusCode, response.Header.Get("Content-Type")
}

func newFlowViewApprovalHistoryFixture(t *testing.T) (*Server, string, semantic.EnrichmentResult) {
	t.Helper()
	srv, root, result := newFlowViewApprovalProposalFixture(t)
	commitFlowViewApprovalHistoryFixture(t, srv, root, result)
	return srv, root, result
}

func newFlowViewApprovalProposalFixture(t *testing.T) (*Server, string, semantic.EnrichmentResult) {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var digestMu sync.RWMutex
	var q3 semantic.Q3CanonicalDigests
	disposableRoot := t.TempDir()
	factory := protocol.ModelHostFactory(func(ctx context.Context) (*protocol.ModelHost, error) {
		digestMu.RLock()
		current := q3
		digestMu.RUnlock()
		host, err := protocol.SpawnModelHost(ctx, protocol.ModelHostConfig{
			BinPath: os.Args[0], Args: []string{"-test.run=TestFlowViewModelHostHelper"}, DisposableRoot: disposableRoot,
			Env: []string{
				"CODEFLOW_MODEL_HOST_HELPER=flowview",
				"CODEFLOW_VS08_MODEL_HOST_RELEASE=" + strings.Join([]string{current.Fact, current.Obligation, current.Alignment, current.Settlement}, "|"),
			},
			DefaultTimeout: 3 * time.Second,
		})
		return host, err
	})
	srv, err := NewServer(Config{RepoRoot: root, Port: 0, AuthToken: "approval-history-endpoint-token", ModelHostFactory: factory})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}
	query := &semantic.TaskViewQuery{SchemaID: semantic.FeatureQuerySchemaID, SchemaVersion: 2, Mode: "feature", Feature: &semantic.FeatureQueryParams{Request: "show main"}}
	if err := srv.RememberTaskQuery(query, query.Feature.Request); err != nil {
		_ = srv.Shutdown(context.Background())
		t.Fatalf("remember task query: %v", err)
	}
	srv.compileCandidate = func(ctx context.Context, snapshot protocol.Snapshot, query *semantic.TaskViewQuery) (*semantic.SemanticMapIR, *semantic.FlowViewProjection, *slicing.SlicedPayload, *semantic.ResolvedTarget, *semantic.TaskIntent, *semantic.CausalObservationClosure, error) {
		mapIR, projection, payload, target, intent, closure, err := liveDeltaCandidate(ctx, snapshot, query, "generation-approval-history-endpoint")
		if err != nil {
			return nil, nil, nil, nil, nil, nil, err
		}
		mapIR.Freshness = "current"
		mapIR.Quality.Stage = "Q3"
		digests, err := semantic.CanonicalQ3Digests(mapIR)
		if err != nil {
			return nil, nil, nil, nil, nil, nil, err
		}
		digestMu.Lock()
		q3 = digests
		digestMu.Unlock()
		return mapIR, projection, payload, target, intent, closure, nil
	}
	_, snapshot, err := srv.SubmitVersionedEdit(context.Background(), workspace.EditRequest{Path: "main.go", Content: []byte("package main\n"), DocumentVersion: 1, Source: workspace.SourceIDEVersioned})
	if err != nil {
		_ = srv.Shutdown(context.Background())
		t.Fatalf("submit fixture edit: %v", err)
	}
	if err := srv.processCheckpoint(context.Background(), snapshot); err != nil {
		_ = srv.Shutdown(context.Background())
		t.Fatalf("publish fixture: %v", err)
	}
	publishedMap := srv.cachedSemanticMap("generation-approval-history-endpoint", "")
	if publishedMap == nil {
		_ = srv.Shutdown(context.Background())
		t.Fatal("published fixture did not populate semantic map")
	}
	publishedDigests, err := semantic.CanonicalQ3Digests(publishedMap)
	if err != nil {
		_ = srv.Shutdown(context.Background())
		t.Fatalf("published Q3 digests: %v", err)
	}
	digestMu.Lock()
	q3 = publishedDigests
	digestMu.Unlock()

	enrichRequest := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/semantic/enrich?token="+srv.AuthToken(), strings.NewReader(`{"generationId":"generation-approval-history-endpoint","targetStepId":"step-live-delta","promptRevision":"prompt-approval-history-endpoint"}`))
	enrichResponse := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(enrichResponse, enrichRequest)
	if enrichResponse.Code != http.StatusOK {
		_ = srv.Shutdown(context.Background())
		t.Fatalf("enrichment status = %d, body = %s", enrichResponse.Code, enrichResponse.Body.String())
	}
	var result semantic.EnrichmentResult
	if err := json.Unmarshal(enrichResponse.Body.Bytes(), &result); err != nil {
		_ = srv.Shutdown(context.Background())
		t.Fatalf("decode enrichment result: %v", err)
	}
	if result.State.Status != "available" || result.Proposal == nil || result.Pack == nil {
		_ = srv.Shutdown(context.Background())
		t.Fatalf("enrichment result = %+v, want available proposal and pack", result.State)
	}
	return srv, root, result
}

func commitFlowViewApprovalHistoryFixture(t *testing.T, srv *Server, root string, result semantic.EnrichmentResult) {
	t.Helper()
	if srv.approvalService == nil {
		_ = srv.Shutdown(context.Background())
		t.Fatal("approval service is nil")
	}
	access, err := srv.authorizeSemanticApproval(context.Background(), root)
	if err != nil {
		_ = srv.Shutdown(context.Background())
		t.Fatalf("authorize fixture approval: %v", err)
	}
	draft, err := semantic.ParseApprovalCommandDraftJSON([]byte(flowViewApprovalBody(result, "history-endpoint-commit", "approve", false, "")))
	if err != nil {
		_ = srv.Shutdown(context.Background())
		t.Fatalf("parse fixture approval draft: %v", err)
	}
	executionResult, err := srv.approvalService.Execute(context.Background(), access, draft)
	if err != nil {
		_ = srv.Shutdown(context.Background())
		t.Fatalf("execute fixture approval: %v", err)
	}
	if executionResult.Receipt.Outbox.DeliveryState != "pending" || executionResult.Receipt.Outbox.PublishedAt != "" {
		_ = srv.Shutdown(context.Background())
		t.Fatalf("fixture approval outbox = %+v, want pending", executionResult.Receipt.Outbox)
	}
	transactionStore := semantic.NewApprovalTransactionStore(root, srv.engine)
	snapshotAfterCommit, err := transactionStore.Snapshot(context.Background())
	if err != nil {
		_ = srv.Shutdown(context.Background())
		t.Fatalf("snapshot after durable approval: %v", err)
	}
	if len(snapshotAfterCommit.Events) != 1 || len(snapshotAfterCommit.Aggregates) != 1 || len(snapshotAfterCommit.IdempotencyResults) != 1 || len(snapshotAfterCommit.Outbox) != 1 {
		_ = srv.Shutdown(context.Background())
		t.Fatalf("durable approval rows = events %d aggregates %d idempotency %d outbox %d, want one each", len(snapshotAfterCommit.Events), len(snapshotAfterCommit.Aggregates), len(snapshotAfterCommit.IdempotencyResults), len(snapshotAfterCommit.Outbox))
	}
}
