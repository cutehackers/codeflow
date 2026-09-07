package flowview

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"codeflow/internal/semantic"
)

func TestFlowViewSemanticApprovalRejectsCallerFieldsAndMapsDenials(t *testing.T) {
	root := t.TempDir()
	server, err := NewServer(Config{RepoRoot: root, Port: 0, AuthToken: "flowview-token"})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer server.Shutdown(context.Background())
	fixture := completeUnattestedAvailableResult(t)
	setFlowViewApprovalServiceStore(t, server, &flowViewApprovalPairStore{stored: &semantic.StoredProposal{
		WorkspaceID: server.approvalWorkspaceID, Proposal: fixture.Proposal, Pack: fixture.Pack,
	}})

	call := func(approver string) *httptest.ResponseRecorder {
		body := flowViewApprovalBody(fixture, "auth-"+approver, "approve", approver != "", approver)
		req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/semantic/approve?token="+server.AuthToken(), strings.NewReader(body))
		req.Host = "127.0.0.1"
		rec := httptest.NewRecorder()
		server.httpServer.Handler.ServeHTTP(rec, req)
		return rec
	}

	rec := call("")
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), `"code":"approval_unavailable"`) {
		t.Fatalf("empty approver response=%d body=%s, want bounded unavailable response", rec.Code, rec.Body.String())
	}

	rec = call("caller-spoof")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("spoofed approver response=%d body=%s, want rejected draft", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), root) {
		t.Fatalf("spoof response exposed workspace path: %s", rec.Body.String())
	}

	server.approvalGate = nil
	rec = call("")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("missing auth response=%d body=%s, want 401 and no submit", rec.Code, rec.Body.String())
	}

	server.approvalGate = semantic.NewApprovalAccessGate(semantic.NewLocalProcessApprovalAuthenticator(), nil)
	rec = call("")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("missing authorizer response=%d body=%s, want 403 and no submit", rec.Code, rec.Body.String())
	}

	server.approvalGate = semantic.NewApprovalAccessGate(semantic.NewLocalProcessApprovalAuthenticator(), semantic.NewApprovalWorkspaceAuthorizer(t.TempDir()))
	server.approvalWorkspaceID = "workspace-other"
	rec = call("")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("workspace mismatch response=%d body=%s, want 403 and no submit", rec.Code, rec.Body.String())
	}
}

func TestFlowViewSemanticApprovalRequiresDurableStoredProposalPair(t *testing.T) {
	root := t.TempDir()
	server, err := NewServer(Config{RepoRoot: root, Port: 0, AuthToken: "flowview-token"})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer server.Shutdown(context.Background())

	call := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/semantic/approve?token="+server.AuthToken(), strings.NewReader(body))
		req.Host = "127.0.0.1"
		recorder := httptest.NewRecorder()
		server.httpServer.Handler.ServeHTTP(recorder, req)
		return recorder
	}

	recorder := call(flowViewApprovalBodyValues("command-missing", "proposal-not-persisted", "pack-not-persisted", "basis-missing", "generation-missing", 1, "approve", "key-missing", 0, "none", nil))
	if recorder.Code != http.StatusNotFound || !strings.Contains(recorder.Body.String(), `"code":"approval_unavailable"`) {
		t.Fatalf("missing durable pair response=%d body=%s, want safe unavailable", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), root) {
		t.Fatalf("missing durable pair leaked root: body=%s", recorder.Body.String())
	}

	recorder = call(`{"commandId":"command-missing","proposalId":"proposal-not-persisted","computedBasisId":"basis-missing","generationId":"generation-missing","intentRevision":1,"decision":"approve","idempotencyKey":"key-missing","expectedApprovalVersion":0,"expectedState":"none"}`)
	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), `"code":"missing_precondition"`) {
		t.Fatalf("missing evidence-pack identity response=%d body=%s, want strict draft precondition error", recorder.Code, recorder.Body.String())
	}
}

func TestFlowViewSemanticApprovalRejectsMismatchedStoredPair(t *testing.T) {
	root := t.TempDir()
	server, err := NewServer(Config{RepoRoot: root, Port: 0, AuthToken: "flowview-token"})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer server.Shutdown(context.Background())
	fixture := completeUnattestedAvailableResult(t)

	cases := []struct {
		name   string
		mutate func(*semantic.StoredProposal)
	}{
		{
			name: "workspace identity",
			mutate: func(stored *semantic.StoredProposal) {
				stored.WorkspaceID = "workspace-returned-other"
			},
		},
		{
			name: "proposal identity",
			mutate: func(stored *semantic.StoredProposal) {
				proposal := *stored.Proposal
				proposal.ProposalID = "proposal-returned-other"
				stored.Proposal = &proposal
			},
		},
		{
			name: "evidence pack identity",
			mutate: func(stored *semantic.StoredProposal) {
				pack := *stored.Pack
				pack.EvidencePackID = "pack-returned-other"
				stored.Pack = &pack
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stored := &semantic.StoredProposal{
				WorkspaceID: server.approvalWorkspaceID,
				Proposal:    fixture.Proposal,
				Pack:        fixture.Pack,
			}
			tc.mutate(stored)
			setFlowViewApprovalServiceStore(t, server, &flowViewApprovalReturningStore{stored: stored})

			body := flowViewApprovalBody(fixture, "mismatch-"+tc.name, "approve", false, "")
			req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/semantic/approve?token="+server.AuthToken(), strings.NewReader(body))
			req.Host = "127.0.0.1"
			recorder := httptest.NewRecorder()
			server.httpServer.Handler.ServeHTTP(recorder, req)
			if recorder.Code != http.StatusNotFound && recorder.Code != http.StatusBadRequest {
				t.Fatalf("mismatched stored pair response=%d body=%s, want safe service rejection", recorder.Code, recorder.Body.String())
			}
			if strings.Contains(recorder.Body.String(), "returned-other") || strings.Contains(recorder.Body.String(), root) {
				t.Fatalf("mismatched stored pair leaked identity/path: %s", recorder.Body.String())
			}
		})
	}
}

func TestFlowViewConfiguredRootSymlinkRemainsAuthorized(t *testing.T) {
	root := t.TempDir()
	parent := t.TempDir()
	alias := filepath.Join(parent, "configured-root")
	if err := symlinkForApprovalTest(root, alias); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	server, err := NewServer(Config{RepoRoot: alias, Port: 0, AuthToken: "flowview-token"})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer server.Shutdown(context.Background())
	fixture := completeUnattestedAvailableResult(t)
	setFlowViewApprovalServiceStore(t, server, &flowViewApprovalPairStore{stored: &semantic.StoredProposal{
		WorkspaceID: server.approvalWorkspaceID, Proposal: fixture.Proposal, Pack: fixture.Pack,
	}})
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/semantic/approve?token="+server.AuthToken(), strings.NewReader(flowViewApprovalBody(fixture, "alias", "approve", false, "")))
	req.Host = "127.0.0.1"
	rec := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound && rec.Code != http.StatusBadRequest {
		t.Fatalf("configured root alias response=%d body=%s, want service rejection after authorization", rec.Code, rec.Body.String())
	}
}

func TestFlowViewSemanticApprovalAuthorizationDenialDoesNotLoadProposal(t *testing.T) {
	root := t.TempDir()
	server, err := NewServer(Config{RepoRoot: root, Port: 0, AuthToken: "flowview-token"})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer server.Shutdown(context.Background())
	fixture := completeUnattestedAvailableResult(t)
	store := &flowViewApprovalCountingStore{stored: &semantic.StoredProposal{
		WorkspaceID: server.approvalWorkspaceID,
		Proposal:    fixture.Proposal,
		Pack:        fixture.Pack,
	}}
	setFlowViewApprovalServiceStore(t, server, store)
	call := func(approver, token string) *httptest.ResponseRecorder {
		body := map[string]any{
			"commandId":               "command-denial-" + token,
			"proposalId":              fixture.Proposal.ProposalID,
			"evidencePackId":          fixture.Pack.EvidencePackID,
			"computedBasisId":         fixture.Proposal.ComputedBasisID,
			"generationId":            fixture.Proposal.GenerationID,
			"intentRevision":          int64(1),
			"decision":                "approve",
			"idempotencyKey":          "key-denial-" + token,
			"expectedApprovalVersion": int64(0),
			"expectedState":           "none",
		}
		if approver != "" {
			body["approver"] = approver
		}
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal approval body: %v", err)
		}
		req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/semantic/approve?token="+token, strings.NewReader(string(encoded)))
		req.Host = "127.0.0.1"
		recorder := httptest.NewRecorder()
		server.httpServer.Handler.ServeHTTP(recorder, req)
		return recorder
	}
	assertDeniedWithoutLoad := func(name string, wantStatus int, request func() *httptest.ResponseRecorder) {
		beforeLoads := store.loads.Load()
		recorder := request()
		if recorder.Code != wantStatus {
			t.Fatalf("%s status=%d body=%s, want %d", name, recorder.Code, recorder.Body.String(), wantStatus)
		}
		if got := store.loads.Load(); got != beforeLoads {
			t.Fatalf("%s loaded proposal %d times, want no additional loads", name, got-beforeLoads)
		}
		approvalDB := filepath.Join(root, ".codeflow", "approval-transactions", "approval-transactions.sqlite3")
		if _, err := os.Stat(approvalDB); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s created approval transaction database: %v", name, err)
		}
	}

	server.approvalGate = nil
	assertDeniedWithoutLoad("missing authenticator", http.StatusUnauthorized, func() *httptest.ResponseRecorder { return call("", server.AuthToken()) })

	server.approvalGate = semantic.NewApprovalAccessGate(semantic.NewLocalProcessApprovalAuthenticator(), nil)
	assertDeniedWithoutLoad("missing authorizer", http.StatusForbidden, func() *httptest.ResponseRecorder { return call("", server.AuthToken()) })

	workspaceID := server.approvalWorkspaceID
	server.approvalGate = semantic.NewApprovalAccessGate(semantic.NewLocalProcessApprovalAuthenticator(), semantic.NewApprovalWorkspaceAuthorizer(root))
	server.approvalWorkspaceID = workspaceID + "-mismatch"
	assertDeniedWithoutLoad("workspace mismatch", http.StatusForbidden, func() *httptest.ResponseRecorder { return call("", server.AuthToken()) })
	server.approvalWorkspaceID = workspaceID

	assertDeniedWithoutLoad("caller-supplied actor field", http.StatusBadRequest, func() *httptest.ResponseRecorder { return call("caller-spoof", server.AuthToken()) })
	assertDeniedWithoutLoad("invalid token", http.StatusUnauthorized, func() *httptest.ResponseRecorder { return call("", "wrong-token") })

	if recorder := call("", server.AuthToken()); recorder.Code != http.StatusNotFound || !strings.Contains(recorder.Body.String(), `"code":"approval_unavailable"`) {
		t.Fatalf("authorized control response=%d body=%s, want bounded unavailable response", recorder.Code, recorder.Body.String())
	}
	if store.loads.Load() != 1 {
		t.Fatalf("authorized control counts = loads %d, want one", store.loads.Load())
	}
}

func symlinkForApprovalTest(target, link string) error {
	return os.Symlink(target, link)
}

type flowViewApprovalPairStore struct {
	stored *semantic.StoredProposal
}

type flowViewApprovalReturningStore struct {
	stored *semantic.StoredProposal
}

type flowViewApprovalCountingStore struct {
	stored *semantic.StoredProposal
	loads  atomic.Int64
}

func setFlowViewApprovalServiceStore(t *testing.T, server *Server, store semantic.ProposalStore) {
	t.Helper()
	if server == nil {
		t.Fatal("approval test server is nil")
	}
	service, err := semantic.NewApprovalExecutionService(server.repoRoot, server.engine, store)
	if err != nil {
		t.Fatalf("create approval test service: %v", err)
	}
	server.proposalStore = store
	server.approvalService = service
}

func (*flowViewApprovalPairStore) SaveEnrichmentResult(context.Context, string, *semantic.EnrichmentResult) error {
	return errors.New("approval test store is read-only")
}

func (*flowViewApprovalReturningStore) SaveEnrichmentResult(context.Context, string, *semantic.EnrichmentResult) error {
	return errors.New("approval test store is read-only")
}

func (*flowViewApprovalCountingStore) SaveEnrichmentResult(context.Context, string, *semantic.EnrichmentResult) error {
	return errors.New("approval test store is read-only")
}

func (s *flowViewApprovalReturningStore) Load(context.Context, string, string, string) (*semantic.StoredProposal, error) {
	if s == nil {
		return nil, nil
	}
	return s.stored, nil
}

func (s *flowViewApprovalCountingStore) Load(_ context.Context, workspaceID, proposalID, evidencePackID string) (*semantic.StoredProposal, error) {
	s.loads.Add(1)
	if s == nil || s.stored == nil || s.stored.WorkspaceID != workspaceID || s.stored.Proposal == nil || s.stored.Pack == nil || s.stored.Proposal.ProposalID != proposalID || s.stored.Pack.EvidencePackID != evidencePackID {
		return nil, &semantic.ProposalNotFoundError{WorkspaceID: workspaceID, ProposalID: proposalID, EvidencePackID: evidencePackID}
	}
	return s.stored, nil
}

func (s *flowViewApprovalPairStore) Load(_ context.Context, workspaceID, proposalID, evidencePackID string) (*semantic.StoredProposal, error) {
	if s == nil || s.stored == nil || s.stored.WorkspaceID != workspaceID || s.stored.Proposal == nil || s.stored.Pack == nil || s.stored.Proposal.ProposalID != proposalID || s.stored.Pack.EvidencePackID != evidencePackID {
		return nil, &semantic.ProposalNotFoundError{WorkspaceID: workspaceID, ProposalID: proposalID, EvidencePackID: evidencePackID}
	}
	return s.stored, nil
}

func flowViewApprovalBody(fixture semantic.EnrichmentResult, key, decision string, includeApprover bool, approver string) string {
	body := map[string]any{
		"commandId":               "command-" + key,
		"proposalId":              fixture.Proposal.ProposalID,
		"evidencePackId":          fixture.Pack.EvidencePackID,
		"computedBasisId":         fixture.Proposal.ComputedBasisID,
		"generationId":            fixture.Proposal.GenerationID,
		"intentRevision":          int64(1),
		"decision":                decision,
		"idempotencyKey":          "idempotency-" + key,
		"expectedApprovalVersion": int64(0),
		"expectedState":           "none",
	}
	if includeApprover {
		body["approver"] = approver
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

func flowViewApprovalBodyValues(commandID, proposalID, evidencePackID, basisID, generationID string, intentRevision int64, decision, idempotencyKey string, expectedVersion int64, expectedState string, predecessor *string) string {
	body := map[string]any{
		"commandId": commandID, "proposalId": proposalID, "evidencePackId": evidencePackID,
		"computedBasisId": basisID, "generationId": generationID, "intentRevision": intentRevision,
		"decision": decision, "idempotencyKey": idempotencyKey, "expectedApprovalVersion": expectedVersion,
		"expectedState": expectedState,
	}
	if predecessor != nil {
		body["predecessorApprovalId"] = *predecessor
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

func TestEmbeddedSemanticApprovalRequestOmitsCallerApprover(t *testing.T) {
	const endpoint = "api('/api/semantic/approve'"
	const bodyMarker = "body: JSON.stringify({"
	const bodyClose = "\n      })"

	endpointAt := strings.Index(IndexHTML, endpoint)
	if endpointAt < 0 {
		t.Fatalf("embedded approval endpoint %q not found", endpoint)
	}
	bodyOffset := strings.Index(IndexHTML[endpointAt:], bodyMarker)
	if bodyOffset < 0 {
		t.Fatalf("embedded approval request body not found after %q", endpoint)
	}
	bodyStart := endpointAt + bodyOffset
	bodyEndOffset := strings.Index(IndexHTML[bodyStart:], bodyClose)
	if bodyEndOffset < 0 {
		t.Fatalf("embedded approval request body close %q not found", bodyClose)
	}
	body := IndexHTML[bodyStart : bodyStart+bodyEndOffset]
	if strings.Contains(body, "approver") {
		t.Fatalf("embedded approval request supplies caller approver: %s", body)
	}
	for _, field := range []string{
		"commandId:", "proposalId:", "evidencePackId:", "computedBasisId:", "generationId:",
		"intentRevision:", "decision:", "idempotencyKey:", "expectedApprovalVersion:", "expectedState:",
	} {
		if !strings.Contains(body, field) {
			t.Fatalf("embedded approval request lost required v2 field %q: %s", field, body)
		}
	}
	if strings.Contains(body, "approved") || strings.Contains(body, "rejected") || strings.Contains(body, "modified") {
		t.Fatalf("embedded approval request uses legacy decision value: %s", body)
	}
	if strings.Contains(body, "targetSym") || strings.Contains(body, "prop-')") {
		t.Fatalf("embedded approval request synthesizes proposal identity: %s", body)
	}
	if strings.HasSuffix(strings.TrimSpace(body), ",") {
		t.Fatalf("embedded approval request has a trailing property comma: %s", body)
	}
}

func TestEmbeddedEvidencePackIdentityUsesDurableResultAndClears(t *testing.T) {
	const marker = `<span id="evidence-pack-id" style="font-family:monospace">`
	markerAt := strings.Index(IndexHTML, marker)
	if markerAt < 0 {
		t.Fatalf("evidence-pack-id element not found")
	}
	valueStart := markerAt + len(marker)
	valueEndOffset := strings.Index(IndexHTML[valueStart:], "</span>")
	if valueEndOffset < 0 {
		t.Fatalf("evidence-pack-id element is not closed")
	}
	if got := IndexHTML[valueStart : valueStart+valueEndOffset]; got != "unavailable" {
		t.Fatalf("initial evidence-pack-id display = %q, want explicit unavailable state", got)
	}
	if strings.Contains(IndexHTML, "pack-default") {
		t.Fatal("embedded UI still contains synthetic pack-default identity")
	}

	functionBody := func(name string) string {
		start := strings.Index(IndexHTML, "function "+name)
		if start < 0 {
			t.Fatalf("embedded function %q not found", name)
		}
		body := IndexHTML[start:]
		if end := strings.Index(body, "\nfunction "); end >= 0 {
			body = body[:end]
		}
		return body
	}
	render := functionBody("renderSemanticEnrichment(data)")
	if !strings.Contains(render, "const durablePackID=status==='available'&&enrichment.pack&&typeof enrichment.pack.evidencePackId==='string'?enrichment.pack.evidencePackId:'';") {
		t.Fatalf("renderSemanticEnrichment does not gate the pack identity on an available durable pack: %s", render)
	}
	if !strings.Contains(render, "setSemanticEvidencePackIdentity") {
		t.Fatalf("renderSemanticEnrichment does not update the evidence-pack-id display: %s", render)
	}
	identitySetter := functionBody("setSemanticEvidencePackIdentity")
	if !strings.Contains(identitySetter, "viewState={...viewState,evidencePackId:value}") || !strings.Contains(identitySetter, "document.getElementById('evidence-pack-id')") || !strings.Contains(identitySetter, "display.textContent=value||'unavailable'") {
		t.Fatalf("evidence-pack identity setter does not clear/update state and DOM safely: %s", identitySetter)
	}
	clear := functionBody("clearSemanticEnrichmentIdentity")
	if !strings.Contains(clear, "setSemanticEvidencePackIdentity('')") || !strings.Contains(clear, "viewState={...viewState,proposalId:'',approvalCommandId:'',approvalIdempotencyKey:'',approvalDecision:'',approvalExpectedVersion:null,approvalExpectedState:'',approvalPredecessorApprovalId:'',approvalVersion:0,approvalState:'none',activeApprovalId:''}") {
		t.Fatalf("clearSemanticEnrichmentIdentity does not clear both stored identities: %s", clear)
	}
	begin := functionBody("beginSemanticRequest")
	if !strings.Contains(begin, "semanticRequestGeneration+=1") || !strings.Contains(begin, "clearSemanticEnrichmentIdentity()") {
		t.Fatalf("beginSemanticRequest does not advance and clear request identity: %s", begin)
	}
	for _, name := range []string{"handleSemanticQuery(event,preserveSelection=false)", "selectSpecificEntry(entrySymbol)"} {
		body := functionBody(name)
		if !strings.Contains(body, "clearSemanticEnrichmentIdentity()") {
			t.Fatalf("%s does not clear pack identity on request failure/reset: %s", name, body)
		}
	}
}

func TestEmbeddedSemanticRequestClearsEvidencePackBeforeAwait(t *testing.T) {
	functionBody := func(name string) string {
		start := strings.Index(IndexHTML, "function "+name)
		if start < 0 {
			t.Fatalf("embedded function %q not found", name)
		}
		body := IndexHTML[start:]
		if end := strings.Index(body, "\nfunction "); end >= 0 {
			body = body[:end]
		}
		return body
	}

	for _, name := range []string{"handleSemanticQuery(event,preserveSelection=false)", "selectSpecificEntry(entrySymbol)"} {
		body := functionBody(name)
		awaitAt := strings.Index(body, "await api(")
		if awaitAt < 0 {
			t.Fatalf("%s does not issue an async semantic request: %s", name, body)
		}
		beginAt := strings.Index(body[:awaitAt], "beginSemanticRequest()")
		if beginAt < 0 {
			t.Fatalf("%s can retain a previous evidence-pack identity while fetch is pending: begin-before-await=%d", name, beginAt)
		}
	}

	approval := functionBody("submitProposalApproval(decision)")
	if !strings.Contains(approval, "!proposalId||!evidencePackId") || !strings.Contains(approval, "return;") {
		t.Fatalf("approval action lacks the empty-identity guard: %s", approval)
	}
}

func TestEmbeddedOverlappingSemanticRequestsKeepLatestIdentity(t *testing.T) {
	nodePath, err := exec.LookPath("node")
	if err != nil {
		t.Skipf("node is required for the controlled-promise embedded UI harness: %v", err)
	}

	functionSource := func(name string) string {
		start := -1
		for _, prefix := range []string{"async function ", "function "} {
			if candidate := strings.Index(IndexHTML, prefix+name); candidate >= 0 && (start < 0 || candidate < start) {
				start = candidate
			}
		}
		if start < 0 {
			t.Fatalf("embedded function %q not found", name)
		}
		body := IndexHTML[start:]
		end := -1
		for _, marker := range []string{"\nasync function ", "\nfunction "} {
			if candidate := strings.Index(body, marker); candidate >= 0 && (end < 0 || candidate < end) {
				end = candidate
			}
		}
		if end >= 0 {
			body = body[:end]
		}
		return body
	}

	stateStart := strings.Index(IndexHTML, "let viewState={")
	if stateStart < 0 {
		t.Fatal("embedded viewState declaration not found")
	}
	stateEndOffset := strings.Index(IndexHTML[stateStart:], "\n};")
	if stateEndOffset < 0 {
		t.Fatal("embedded viewState declaration is not closed")
	}
	stateSource := IndexHTML[stateStart : stateStart+stateEndOffset+len("\n};")]
	statusStart := strings.Index(IndexHTML, "const ENRICHMENT_STATUSES=")
	if statusStart < 0 {
		t.Fatal("embedded enrichment status declaration not found")
	}
	statusEndOffset := strings.Index(IndexHTML[statusStart:], "\n")
	if statusEndOffset < 0 {
		t.Fatal("embedded enrichment status declaration is not closed")
	}
	statusSource := IndexHTML[statusStart : statusStart+statusEndOffset]

	script := strings.Join([]string{
		"'use strict';",
		statusSource,
		stateSource,
		"let semanticRequestGeneration=0;",
		functionSource("enrichmentEnvelope"),
		functionSource("enrichmentDisplayValue"),
		functionSource("isDisplayOnlyInferredProposal"),
		functionSource("setSemanticEvidencePackIdentity"),
		functionSource("clearSemanticEnrichmentIdentity"),
		functionSource("beginSemanticRequest"),
		functionSource("isLatestSemanticRequest"),
		functionSource("renderSemanticEnrichment"),
		"const elements=new Map();",
		"const queryInput={value:''};",
		"const rendered=[];",
		"const alerts=[];",
		"const apiCalls=[];",
		"const pending=[];",
		"function element(id){if(!elements.has(id))elements.set(id,{style:{},dataset:{},textContent:'',hidden:false,disabled:false,setAttribute(){}});return elements.get(id);}",
		"globalThis.document={getElementById(id){return id==='query-input'?queryInput:element(id);}};",
		"globalThis.alert=(value)=>alerts.push(value);",
		"function showDisambiguation(){}",
		"function response(tag){return {ok:true,json:async()=>({tag:tag,enrichment:{state:{status:'available'},proposal:{proposalId:'proposal-'+tag},pack:{evidencePackId:'pack-'+tag}}})};}",
		"function api(url){apiCalls.push(url);return new Promise((resolve,reject)=>pending.push({resolve:resolve,reject:reject,url:url}));}",
		"function renderSemanticTaskView(data){rendered.push(data.tag);renderSemanticEnrichment(data);}",
		functionSource("handleSemanticQuery"),
		functionSource("selectSpecificEntry"),
		functionSource("submitProposalApproval"),
		"function assert(condition,message){if(!condition)throw new Error(message);}",
		"async function runQueryPair(reverse){const offset=pending.length;rendered.length=0;viewState={...viewState,proposalId:'old-proposal',evidencePackId:'old-pack'};element('evidence-pack-id').textContent='old-pack';queryInput.value='A';const a=handleSemanticQuery(null);queryInput.value='B';const b=handleSemanticQuery(null);assert(viewState.proposalId===''&&viewState.evidencePackId===''&&element('evidence-pack-id').textContent==='unavailable','request start did not clear prior identity');assert(apiCalls.length===offset+2,'unexpected request count at start');if(reverse){pending[offset+1].resolve(response('B'));await b;assert(rendered.length===1&&rendered[0]==='B'&&viewState.proposalId==='proposal-B'&&viewState.evidencePackId==='pack-B','latest B did not render first');pending[offset].resolve(response('A'));await a;assert(rendered.length===1&&rendered[0]==='B'&&viewState.proposalId==='proposal-B'&&viewState.evidencePackId==='pack-B','stale A overwrote B');}else{pending[offset].resolve(response('A'));await a;assert(rendered.length===0&&viewState.proposalId===''&&viewState.evidencePackId===''&&element('evidence-pack-id').textContent==='unavailable','stale A rendered while B was pending');await submitProposalApproval('approved');assert(apiCalls.length===offset+2,'approval API was called for stale A while B was pending');pending[offset+1].resolve(response('B'));await b;assert(rendered.length===1&&rendered[0]==='B'&&viewState.proposalId==='proposal-B'&&viewState.evidencePackId==='pack-B','latest B did not render after A');}await Promise.resolve();}",
		"async function runSelectPair(){const offset=pending.length;rendered.length=0;viewState={...viewState,proposalId:'old-proposal',evidencePackId:'old-pack'};element('evidence-pack-id').textContent='old-pack';const a=selectSpecificEntry('A');const b=selectSpecificEntry('B');assert(viewState.proposalId===''&&viewState.evidencePackId===''&&element('evidence-pack-id').textContent==='unavailable','select start did not clear prior identity');assert(apiCalls.length===offset+2,'unexpected select request count at start');pending[offset+1].resolve(response('B'));await b;pending[offset].resolve(response('A'));await a;assert(rendered.length===1&&rendered[0]==='B'&&viewState.proposalId==='proposal-B'&&viewState.evidencePackId==='pack-B','select stale A overwrote latest B');}",
		"(async()=>{await runQueryPair(false);await runQueryPair(true);await runSelectPair();if(alerts.length!==0)throw new Error('unexpected alert during stale request handling');})().catch(error=>{console.error(error.stack||error);process.exitCode=1;});",
	}, "\n")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, nodePath, "--input-type=commonjs")
	cmd.Stdin = strings.NewReader(script)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("controlled embedded request harness failed: %v\n%s", err, output)
	}
}
