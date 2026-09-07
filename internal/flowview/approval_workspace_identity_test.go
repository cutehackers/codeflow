package flowview

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFlowViewTaskViewResponseCarriesCoreApprovalWorkspaceIdentity(t *testing.T) {
	root := copyFixtureWithoutCodeflow(t, "nextjs-app-fixture")
	moduleRoot, _ := filepath.Abs("../..")
	t.Setenv("CODEFLOW_ADAPTER_TYPESCRIPT_BIN", "noderun:"+filepath.Join(moduleRoot, "adapters", "typescript"))

	srv, err := NewServer(Config{RepoRoot: root, Port: 0})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}
	t.Cleanup(func() { _ = srv.httpServer.Shutdown(context.Background()) })

	identity := srv.engine.WorkspaceIdentity()
	identity.Branch = "approval-workspace-identity-test"
	if change, err := srv.engine.SetWorkspaceIdentity(identity); err != nil {
		t.Fatalf("advance workspace identity: %v", err)
	} else if change.NewEpoch == 0 {
		t.Fatalf("expected nonzero workspace epoch after identity transition: %+v", change)
	}

	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/task/view?token="+srv.AuthToken()+"&entrySymbol=app/page.tsx%23HomePage.handleQuickCheckout", nil)
	rec := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for valid query, got %d: %s", rec.Code, rec.Body.String())
	}
	var response map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode task-view response: %v", err)
	}
	workspaceID, ok := response["workspaceId"].(string)
	if !ok || strings.TrimSpace(workspaceID) == "" {
		t.Fatalf("task-view response did not expose a nonempty Core workspaceId: %v", response["workspaceId"])
	}
	if workspaceID != srv.approvalWorkspaceID {
		t.Fatalf("task-view response workspaceId=%q, want Core identity %q", workspaceID, srv.approvalWorkspaceID)
	}
	semanticMap, ok := response["semanticMap"].(map[string]any)
	if !ok {
		t.Fatalf("task-view response semanticMap has unexpected shape: %T", response["semanticMap"])
	}
	if _, exists := semanticMap["workspaceId"]; exists {
		t.Fatal("Core workspaceId must remain a task-view envelope field, not a SemanticMapIR field")
	}
	basis, ok := semanticMap["basis"].(map[string]any)
	if !ok {
		t.Fatalf("task-view response semanticMap.basis has unexpected shape: %T", semanticMap["basis"])
	}
	epoch, ok := basis["workspaceEpoch"].(float64)
	if !ok || epoch <= 0 {
		t.Fatalf("task-view response did not preserve nonzero workspace epoch: %v", basis["workspaceEpoch"])
	}
}

func TestEmbeddedTaskViewWorkspaceIdentityBindsApprovalReceiptAndHistory(t *testing.T) {
	nodePath, err := exec.LookPath("node")
	if err != nil {
		t.Skipf("node is required for the controlled workspace identity harness: %v", err)
	}

	functionSource := func(name string) string {
		start := -1
		for _, prefix := range []string{"async function ", "function "} {
			if candidate := strings.Index(IndexHTML, prefix+name); candidate >= 0 && (start < 0 || candidate < start) {
				start = candidate
			}
		}
		if start < 0 {
			return ""
		}
		body := IndexHTML[start:]
		end := -1
		for _, marker := range []string{"\nasync function ", "\nfunction ", "\nconst ", "\nlet "} {
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
	parts := []string{
		"'use strict';",
		stateSource,
		"let semanticRequestGeneration=1;let currentSpec=null,selected=0,currentFlowId='';",
		"const ENRICHMENT_STATUSES=['not_requested','pending','available','timed_out','unavailable'];",
		"const APPROVAL_HISTORY_SCHEMA_ID='https://codeflow.local/schemas/rflsc.approval-history.v1.schema.json';const APPROVAL_HISTORY_SCHEMA_VERSION=1;const APPROVAL_EVENT_SCHEMA_ID='https://codeflow.local/schemas/rflsc.approval-event.v2.schema.json';const APPROVAL_AGGREGATE_SCHEMA_ID='https://codeflow.local/schemas/rflsc.approval-aggregate.v2.schema.json';const APPROVAL_HISTORY_STATES=['none','active','rejected','revoked','superseded'];const APPROVAL_HISTORY_DECISIONS=['approve','edit_then_approve','reject','revoke','supersede'];const APPROVAL_HISTORY_RELATIONS=['initial','edit','reject','revoke','supersede'];const APPROVAL_HISTORY_MAX_RECORDS=256;let approvalHistoryModel=null;let approvalHistoryLoadSequence=0;let approvalHistoryMutationSequence=0;",
		"const APPROVAL_EXECUTION_SCHEMA_IDS=Object.freeze({event:'https://codeflow.local/schemas/rflsc.approval-event.v2.schema.json',aggregate:'https://codeflow.local/schemas/rflsc.approval-aggregate.v2.schema.json',idempotency:'https://codeflow.local/schemas/rflsc.approval-idempotency-result.v1.schema.json',outbox:'https://codeflow.local/schemas/rflsc.approval-outbox.v1.schema.json'});const APPROVAL_EXECUTION_DECISIONS=Object.freeze(['approve','edit_then_approve','reject','revoke','supersede']);const APPROVAL_EXECUTION_STATES=Object.freeze(['none','active','rejected','revoked','superseded']);const APPROVAL_EXECUTION_RELATIONS=Object.freeze(['initial','edit','reject','revoke','supersede']);const APPROVAL_EXECUTION_DIGEST=/^sha256:[0-9a-f]{64}$/;const APPROVAL_EXECUTION_MAX_VERSION=1000000000;const APPROVAL_VALIDATED_EXECUTION_RESULTS=new WeakSet();",
	}
	for _, name := range []string{
		"enrichmentEnvelope(data)",
		"enrichmentDisplayValue(value,fallback)",
		"isDisplayOnlyInferredProposal(proposal)",
		"setSemanticEvidencePackIdentity(packID)",
		"structuralIdentityOf(st)",
		"updateViewStateGeneration(data)",
		"renderSemanticEnrichment(data)",
		"renderSemanticTaskView(data,preserveSelection=false)",
		"approvalHistoryExactKeys(value,required,optional)",
		"approvalHistoryValidID(value,allowEmpty=false,max=256)",
		"approvalHistoryValidInteger(value,min,max)",
		"approvalHistoryIdentity()",
		"approvalHistoryIdentityIsCurrent(identity)",
		"approvalHistoryValidText(value,max)",
		"approvalHistoryValidTimestamp(value)",
		"approvalHistoryTransition(state,active,event)",
		"validateApprovalHistoryPayload(payload,identity)",
		"approvalHistoryStateLabel(state)",
		"approvalHistoryDecisionLabel(decision)",
		"renderApprovalHistoryDisplay(events,aggregate,freshness)",
		"clearApprovalHistoryUI(freshness)",
		"applyApprovalHistoryPayload(payload)",
		"appendApprovalHistoryLocalReceipt(result)",
		"appendApprovalHistoryReceipt(result)",
		"secureApprovalActionID(prefix)",
		"approvalPendingRequestMatches(pending,semantic)",
		"approvalExecutionExactKeys(value,required,optional)",
		"approvalExecutionValidText(value,max,required)",
		"approvalExecutionValidID(value)",
		"approvalExecutionValidInteger(value,min,max)",
		"approvalExecutionValidDigest(value)",
		"approvalExecutionValidTimestamp(value)",
		"approvalExecutionPreviousAggregateMatchesView(previous,semantic)",
		"approvalExecutionCurrentWorkspaceID()",
		"approvalExecutionValidPendingRequest(pending,semantic)",
		"approvalExecutionExpectedTransition(previous,pending)",
		"approvalExecutionHistoryEntryEqual(left,right)",
		"approvalExecutionEventIsNew(event,previous)",
		"validateApprovalExecutionResult(result,pending,semantic,previous)",
		"approvalEditedTextLength(value)",
		"approvalLifecycleAdmission(decision,editedText)",
		"approvalAdmissionMessage(decision,editedText)",
		"approvalOperationIsCurrent(requestGeneration,pendingRequest,semantic)",
		"submitProposalApproval(decision)",
	} {
		source := functionSource(name)
		if source == "" {
			t.Fatalf("embedded function %q not found", name)
		}
		parts = append(parts, source)
	}

	approvalResponseSource := `function approvalResponse(payload,workspace,replayed){const eventId='event-'+payload.commandId,approvalId='approval-'+payload.commandId,aggregateId='aggregate-'+payload.proposalId,requestDigest='sha256:'+'0'.repeat(64),payloadDigest='sha256:'+'1'.repeat(64),timestamp='2026-09-07T00:00:00Z',event={schemaId:APPROVAL_EXECUTION_SCHEMA_IDS.event,schemaVersion:2,eventId:eventId,approvalId:approvalId,aggregateId:aggregateId,aggregateVersion:1,actorId:'actor-workspace-r1',sessionId:'session-workspace-r1',workspaceId:workspace,proposalId:payload.proposalId,evidencePackId:payload.evidencePackId,computedBasisId:payload.computedBasisId,generationId:payload.generationId,intentRevision:payload.intentRevision,decision:'approve',approvedText:'approved semantic text',timestamp:timestamp,lifecycleRelation:'initial'},history=[{version:0,state:'none',eventId:'genesis-'+aggregateId,decision:'none'},{version:1,state:'active',eventId:eventId,approvalId:approvalId,decision:'approve'}],aggregate={schemaId:APPROVAL_EXECUTION_SCHEMA_IDS.aggregate,schemaVersion:2,aggregateId:aggregateId,workspaceId:workspace,proposalId:payload.proposalId,evidencePackId:payload.evidencePackId,computedBasisId:payload.computedBasisId,generationId:payload.generationId,intentRevision:payload.intentRevision,version:1,state:'active',lastEventId:eventId,lastDecision:'approve',activeApprovalId:approvalId,history:history},idempotencyResult={schemaId:APPROVAL_EXECUTION_SCHEMA_IDS.idempotency,schemaVersion:1,idempotencyKey:payload.idempotencyKey,commandId:payload.commandId,actorId:'actor-workspace-r1',sessionId:'session-workspace-r1',workspaceId:workspace,proposalId:payload.proposalId,evidencePackId:payload.evidencePackId,computedBasisId:payload.computedBasisId,generationId:payload.generationId,requestDigest:requestDigest,outcome:'committed',approvalId:approvalId,eventId:eventId,aggregateId:aggregateId,aggregateVersion:1,state:'active',committedAt:timestamp,originalCommand:{commandId:payload.commandId,idempotencyKey:payload.idempotencyKey,actorId:'actor-workspace-r1',workspaceId:workspace,proposalId:payload.proposalId,decision:'approve',expectedApprovalVersion:0},originalResult:{outcome:'committed',approvalId:approvalId,eventId:eventId,aggregateId:aggregateId,aggregateVersion:1,state:'active'}},outbox={schemaId:APPROVAL_EXECUTION_SCHEMA_IDS.outbox,schemaVersion:1,outboxId:'outbox-'+eventId,eventId:eventId,aggregateId:aggregateId,aggregateVersion:1,workspaceId:workspace,payloadDigest:payloadDigest,committedAt:timestamp,deliveryState:'pending',committedEvent:{eventId:eventId,approvalId:approvalId,aggregateId:aggregateId,aggregateVersion:1,workspaceId:workspace,decision:'approve',payloadDigest:payloadDigest}};return {receipt:{idempotencyResult:idempotencyResult,event:event,aggregate:aggregate,outbox:outbox,replayed:!!replayed},generationId:payload.generationId,computedBasisId:payload.computedBasisId,validatedSnapshotId:'snapshot-workspace-r1',intentRevision:payload.intentRevision,freshness:'current'};}`

	script := strings.Join(append(parts,
		"const elements=new Map();const calls=[];",
		"let uuidCalls=0;Object.defineProperty(globalThis,'crypto',{configurable:true,writable:true,value:{randomUUID(){uuidCalls+=1;return 'uuid-'+String(uuidCalls);}}});",
		"function element(id){if(!elements.has(id)){const node={id:id,style:{},dataset:{},_text:'',hidden:false,disabled:false,value:'',children:[],setAttribute(){},appendChild(child){this.children.push(child);}};Object.defineProperty(node,'textContent',{get(){return this._text;},set(value){this._text=String(value);if(id==='approval-history-events')this.children=[];}});elements.set(id,node);}return elements.get(id);}",
		"globalThis.document={getElementById:id=>element(id),createElement:tag=>({tagName:tag,textContent:'',children:[],setAttribute(){},appendChild(child){this.children.push(child);}})};",
		"globalThis.window=globalThis;",
		"function renderAll(){};function renderRequirementAlignment(){};function setViewStateStatus(){};function hideVerifiedGap(){};function showVerifiedGap(){};function syncApprovalControls(){};function loadApprovalHistory(){};",
		"let responseMode='valid';function api(url,opts){const call={url:url,opts:opts||{}};calls.push(call);if(url!=='/api/semantic/approve')throw new Error('unexpected API call: '+url);const body=JSON.parse(call.opts.body);if(responseMode==='mismatch')return Promise.resolve({ok:true,status:200,json:async()=>approvalResponse(body,'workspace-other',false)});if(responseMode==='empty')return Promise.resolve({ok:true,status:200,json:async()=>approvalResponse(body,'',false)});return Promise.resolve({ok:true,status:200,json:async()=>approvalResponse(body,'workspace-core-r1-1',false)});}",
		approvalResponseSource,
		"function assert(condition,message){if(!condition)throw new Error(message);}",
		"const taskViewResponse={workspaceId:'workspace-core-r1-1',candidateAnswer:{requested:'show approval',candidate:'approval history'},taskIntent:{revision:7,request:{rawRequest:'show approval'}},semanticMap:{mapId:'map-core-r1-1',generationId:'generation-core-r1-1',computedBasisId:'basis-core-r1-1',validatedAgainstSnapshotId:'snapshot-workspace-r1',freshness:'current',summary:{requested:'show approval',current:'approval history'},basis:{workspaceEpoch:41,dependencyFingerprint:'dependency-core-r1-1'},quality:{stage:'Q3'},settlement:'passed',task:{taskId:'task-core-r1-1',intentRevision:7},steps:[{stepId:'step-core-r1-1',ordinal:1,name:'approval',technicalName:'Approval',structuralIdentity:'approval|history'}]},enrichment:{status:'available',proposal:{proposalId:'proposal-core-r1-1'},pack:{evidencePackId:'pack-core-r1-1'}}};",
		"function resetApproval(){approvalHistoryModel=null;clearApprovalHistoryUI('current');viewState={...viewState,proposalId:'proposal-core-r1-1',evidencePackId:'pack-core-r1-1',computedBasisId:'basis-core-r1-1',generationId:'generation-core-r1-1',validatedAgainstSnapshotId:'snapshot-workspace-r1',intentRevision:7,approvalVersion:0,approvalState:'none',approvalExpectedVersion:0,approvalExpectedState:'none',approvalPredecessorApprovalId:'',activeApprovalId:'',approvalPendingRequest:null,approvalCommandId:'',approvalIdempotencyKey:'',approvalDecision:''};}",
		"async function run(){renderSemanticTaskView(taskViewResponse,false);assert(currentSpec&&currentSpec.workspaceId==='workspace-core-r1-1','task-view workspace identity did not reach currentSpec: '+JSON.stringify(currentSpec));assert(currentSpec.workspaceEpoch===41,'task-view workspace epoch did not reach currentSpec: '+JSON.stringify(currentSpec));resetApproval();responseMode='valid';await submitProposalApproval('approve');assert(calls.length===1&&calls[0].url==='/api/semantic/approve','valid receipt made an unexpected API call');assert(approvalHistoryModel&&approvalHistoryModel.target.workspaceId==='workspace-core-r1-1'&&approvalHistoryModel.target.workspaceEpoch===41,'valid first receipt did not create an exact workspace-bound history projection');assert(approvalHistoryModel.events.length===1&&approvalHistoryModel.aggregate.version===1&&viewState.approvalState==='active','valid first receipt did not hydrate history/lifecycle');const exactHistory=JSON.parse(JSON.stringify(approvalHistoryModel));const exactIdentity=approvalHistoryIdentity();assert(validateApprovalHistoryPayload(exactHistory,exactIdentity),'exact workspace history was rejected');const mismatchHistory=JSON.parse(JSON.stringify(exactHistory));mismatchHistory.target.workspaceId='workspace-other';assert(!validateApprovalHistoryPayload(mismatchHistory,exactIdentity),'mismatched history workspace was accepted');const emptyHistory=JSON.parse(JSON.stringify(exactHistory));emptyHistory.target.workspaceId='';assert(!validateApprovalHistoryPayload(emptyHistory,exactIdentity),'empty history workspace was accepted');currentSpec.workspaceId='';const emptyIdentity=approvalHistoryIdentity();assert(!validateApprovalHistoryPayload(exactHistory,emptyIdentity),'history was accepted without a current workspace identity');currentSpec.workspaceId='workspace-core-r1-1';resetApproval();responseMode='mismatch';await submitProposalApproval('approve');const mismatchPending=viewState.approvalPendingRequest;assert(viewState.approvalState==='none'&&viewState.approvalVersion===0&&mismatchPending,'mismatched receipt mutated lifecycle state');responseMode='empty';await submitProposalApproval('approve');assert(viewState.approvalState==='none'&&viewState.approvalVersion===0&&viewState.approvalPendingRequest===mismatchPending,'empty receipt workspace changed frozen pending state');currentSpec.workspaceId='workspace-other';responseMode='valid';await submitProposalApproval('approve');assert(viewState.approvalState==='none'&&viewState.approvalVersion===0&&viewState.approvalPendingRequest===mismatchPending,'receipt for a workspace different from currentSpec was accepted');currentSpec.workspaceId='';await submitProposalApproval('approve');assert(viewState.approvalState==='none'&&viewState.approvalVersion===0&&viewState.approvalPendingRequest===mismatchPending,'receipt without a current workspace identity was accepted');currentSpec.workspaceId='workspace-core-r1-1';await submitProposalApproval('approve');assert(viewState.approvalState==='active'&&viewState.approvalVersion===1&&viewState.approvalPendingRequest===null,'exact workspace retry was not accepted');}",
		"run().catch(error=>{console.error(error.stack||error);process.exitCode=1;});",
	), "\n")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, nodePath, "--input-type=commonjs")
	cmd.Stdin = strings.NewReader(script)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("controlled workspace identity harness failed: %v\n%s", err, output)
	}
}
