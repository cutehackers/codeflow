package flowview

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestEmbeddedSemanticApprovalIgnoresStaleCompletions(t *testing.T) {
	nodePath, err := exec.LookPath("node")
	if err != nil {
		t.Skipf("node is required for the controlled approval harness: %v", err)
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
	approvalSource := functionSource("submitProposalApproval(decision)")
	if approvalSource == "" {
		t.Fatal("embedded approval function not found")
	}
	secureIDSource := functionSource("secureApprovalActionID(prefix)")
	if secureIDSource == "" {
		secureIDSource = "function secureApprovalActionID(){return 'fallback';}"
	}
	pendingMatchSource := functionSource("approvalPendingRequestMatches(pending,semantic)")
	if pendingMatchSource == "" {
		pendingMatchSource = "function approvalPendingRequestMatches(){return false;}"
	}
	errorMessageSource := functionSource("approvalErrorMessage(payload)")
	if errorMessageSource == "" {
		errorMessageSource = "function approvalErrorMessage(){return 'approval request failed; please retry';}"
	}
	operationSource := functionSource("approvalOperationIsCurrent(requestGeneration,pendingRequest,semantic)")
	if operationSource == "" {
		operationSource = "function approvalOperationIsCurrent(){return true;}"
	}
	approvalErrorConstants := ""
	if !strings.Contains(pendingMatchSource, "const APPROVAL_ERROR_MESSAGES") && !strings.Contains(errorMessageSource, "const APPROVAL_ERROR_MESSAGES") {
		approvalErrorConstants = "const APPROVAL_ERROR_MESSAGES=Object.freeze({approval_invalid:'approval request is invalid',approval_conflict:'approval request conflicts with current state',approval_unavailable:'approval service is unavailable',approval_unauthenticated:'approval authentication is required',approval_unauthorized:'approval workspace is not authorized'});"
	}
	executionConstants := "const APPROVAL_EXECUTION_SCHEMA_IDS=Object.freeze({event:'https://codeflow.local/schemas/rflsc.approval-event.v2.schema.json',aggregate:'https://codeflow.local/schemas/rflsc.approval-aggregate.v2.schema.json',idempotency:'https://codeflow.local/schemas/rflsc.approval-idempotency-result.v1.schema.json',outbox:'https://codeflow.local/schemas/rflsc.approval-outbox.v1.schema.json'});const APPROVAL_EXECUTION_DECISIONS=Object.freeze(['approve','edit_then_approve','reject','revoke','supersede']);const APPROVAL_EXECUTION_STATES=Object.freeze(['none','active','rejected','revoked','superseded']);const APPROVAL_EXECUTION_RELATIONS=Object.freeze(['initial','edit','reject','revoke','supersede']);const APPROVAL_EXECUTION_DIGEST=/^sha256:[0-9a-f]{64}$/;const APPROVAL_EXECUTION_MAX_VERSION=1000000000;const APPROVAL_VALIDATED_EXECUTION_RESULTS=new WeakSet();"
	executionFunctions := []string{
		"approvalExecutionExactKeys(value,required,optional)", "approvalExecutionValidText(value,max,required)",
		"approvalExecutionValidID(value)", "approvalExecutionValidInteger(value,min,max)",
		"approvalExecutionValidDigest(value)", "approvalExecutionValidTimestamp(value)",
		"approvalExecutionPreviousAggregateMatchesView(previous,semantic)", "approvalExecutionCurrentWorkspaceID()",
		"approvalExecutionValidPendingRequest(pending,semantic)", "approvalExecutionExpectedTransition(previous,pending)",
		"approvalExecutionHistoryEntryEqual(left,right)", "approvalExecutionEventIsNew(event,previous)",
		"validateApprovalExecutionResult(result,pending,semantic,previous)",
	}
	executionSources := make([]string, 0, len(executionFunctions))
	for _, name := range executionFunctions {
		source := functionSource(name)
		if source == "" {
			t.Fatalf("embedded execution function %q not found", name)
		}
		executionSources = append(executionSources, source)
	}
	approvalResponseSource := `function completeResult(payload,label,version){const eventId='event-'+label,approvalId='approval-'+label,aggregateId='aggregate-'+label,timestamp='2026-09-07T00:00:00Z',event={schemaId:APPROVAL_EXECUTION_SCHEMA_IDS.event,schemaVersion:2,eventId:eventId,approvalId:approvalId,aggregateId:aggregateId,aggregateVersion:version,actorId:'actor-'+label,sessionId:'session-'+label,workspaceId:'workspace-'+label,proposalId:payload.proposalId,evidencePackId:payload.evidencePackId,computedBasisId:payload.computedBasisId,generationId:payload.generationId,intentRevision:payload.intentRevision,decision:payload.decision,approvedText:'approved semantic text',timestamp:timestamp,lifecycleRelation:'initial'},history=[{version:0,state:'none',eventId:'genesis-'+label,decision:'none'},{version:version,state:'active',eventId:eventId,approvalId:approvalId,decision:payload.decision}],aggregate={schemaId:APPROVAL_EXECUTION_SCHEMA_IDS.aggregate,schemaVersion:2,aggregateId:aggregateId,workspaceId:'workspace-'+label,proposalId:payload.proposalId,evidencePackId:payload.evidencePackId,computedBasisId:payload.computedBasisId,generationId:payload.generationId,intentRevision:payload.intentRevision,version:version,state:'active',lastEventId:eventId,lastDecision:payload.decision,history:history,activeApprovalId:approvalId},requestDigest='sha256:'+'0'.repeat(64),payloadDigest='sha256:'+'1'.repeat(64),idempotencyResult={schemaId:APPROVAL_EXECUTION_SCHEMA_IDS.idempotency,schemaVersion:1,idempotencyKey:payload.idempotencyKey,commandId:payload.commandId,actorId:'actor-'+label,sessionId:'session-'+label,workspaceId:'workspace-'+label,proposalId:payload.proposalId,evidencePackId:payload.evidencePackId,computedBasisId:payload.computedBasisId,generationId:payload.generationId,requestDigest:requestDigest,outcome:'committed',approvalId:approvalId,eventId:eventId,aggregateId:aggregateId,aggregateVersion:version,state:'active',committedAt:timestamp,originalCommand:{commandId:payload.commandId,idempotencyKey:payload.idempotencyKey,actorId:'actor-'+label,workspaceId:'workspace-'+label,proposalId:payload.proposalId,decision:payload.decision,expectedApprovalVersion:payload.expectedApprovalVersion},originalResult:{outcome:'committed',approvalId:approvalId,eventId:eventId,aggregateId:aggregateId,aggregateVersion:version,state:'active'}},outbox={schemaId:APPROVAL_EXECUTION_SCHEMA_IDS.outbox,schemaVersion:1,outboxId:'outbox-'+eventId,eventId:eventId,aggregateId:aggregateId,aggregateVersion:version,workspaceId:'workspace-'+label,payloadDigest:payloadDigest,committedAt:timestamp,deliveryState:'pending',committedEvent:{eventId:eventId,approvalId:approvalId,aggregateId:aggregateId,aggregateVersion:version,workspaceId:'workspace-'+label,decision:payload.decision,payloadDigest:payloadDigest}};return {receipt:{idempotencyResult:idempotencyResult,event:event,aggregate:aggregate,outbox:outbox,replayed:false},generationId:payload.generationId,computedBasisId:payload.computedBasisId,validatedSnapshotId:'snapshot-'+label,intentRevision:payload.intentRevision,freshness:'current'};}`

	setEvidenceSource := functionSource("setSemanticEvidencePackIdentity(packID)")
	if setEvidenceSource == "" {
		setEvidenceSource = "function setSemanticEvidencePackIdentity(value){viewState={...viewState,evidencePackId:value||''};}"
	}
	clearIdentitySource := functionSource("clearSemanticEnrichmentIdentity()")
	if clearIdentitySource == "" {
		clearIdentitySource = "function clearSemanticEnrichmentIdentity(){viewState={...viewState,proposalId:'',evidencePackId:'',approvalPendingRequest:null,approvalCommandId:'',approvalIdempotencyKey:'',approvalDecision:'',approvalExpectedVersion:null,approvalExpectedState:'',approvalPredecessorApprovalId:'',approvalVersion:0,approvalState:'none',activeApprovalId:''};}"
	}
	beginRequestSource := functionSource("beginSemanticRequest()")
	if beginRequestSource == "" {
		beginRequestSource = "function beginSemanticRequest(){semanticRequestGeneration+=1;clearSemanticEnrichmentIdentity();return semanticRequestGeneration;}"
	}

	script := strings.Join([]string{
		"'use strict';",
		stateSource,
		"let semanticRequestGeneration=0;",
		"let currentSpec={workspaceId:''};",
		secureIDSource,
		pendingMatchSource,
		approvalErrorConstants,
		executionConstants,
		strings.Join(executionSources, "\n"),
		errorMessageSource,
		operationSource,
		setEvidenceSource,
		clearIdentitySource,
		beginRequestSource,
		approvalSource,
		"const elements=new Map();",
		"const calls=[];",
		"const deferred=[];",
		"let uuidCalls=0;",
		"Object.defineProperty(globalThis,'crypto',{configurable:true,writable:true,value:{randomUUID(){uuidCalls+=1;return 'uuid-'+String(uuidCalls);}}});",
		"function element(id){if(!elements.has(id))elements.set(id,{style:{},textContent:'',hidden:false});return elements.get(id);}",
		"globalThis.document={getElementById:id=>element(id)};",
		"function api(url,opts){const call={url:url,opts:opts||{}};calls.push(call);return new Promise((resolve,reject)=>deferred.push({resolve:resolve,reject:reject,call:call}));}",
		approvalResponseSource,
		"function delayedJSONResponse(label){let resolveJSON;const json=new Promise(resolve=>{resolveJSON=resolve;});return {response:{ok:true,json:()=>json},resolveJSON:resolveJSON};}",
		"function assert(condition,message){if(!condition)throw new Error(message);}",
		"function setSemantic(label){currentSpec={workspaceId:'workspace-'+label};viewState={...viewState,proposalId:'proposal-'+label,evidencePackId:'pack-'+label,computedBasisId:'basis-'+label,generationId:'generation-'+label,validatedAgainstSnapshotId:'snapshot-'+label,intentRevision:8,approvalVersion:0,approvalState:'none',approvalExpectedVersion:0,approvalExpectedState:'none',activeApprovalId:'',approvalPredecessorApprovalId:''};}",
		"function assertPending(label,pending,message,badge){assert(viewState.proposalId==='proposal-'+label,'stale completion changed proposal identity');assert(viewState.approvalPendingRequest===pending,'stale completion cleared or replaced current pending request');assert(element('approval-result-msg').textContent===message,'stale completion changed result message: '+element('approval-result-msg').textContent);assert(element('approval-status-badge').textContent===badge,'stale completion changed approval badge: '+element('approval-status-badge').textContent);assert(viewState.approvalVersion===0&&viewState.approvalState==='none','stale completion changed approval lifecycle state');}",
		"async function runFetchRace(){",
		"semanticRequestGeneration=1;setSemantic('A');element('approval-result-msg').textContent='A pending';element('approval-status-badge').textContent='A badge';",
		"const a=submitProposalApproval('approve');assert(calls.length===1,'A fetch was not submitted');",
		"beginSemanticRequest();setSemantic('B');element('approval-result-msg').textContent='B pending';element('approval-status-badge').textContent='B badge';",
		"const b=submitProposalApproval('approve');assert(calls.length===2,'B fetch was not submitted');const bPending=viewState.approvalPendingRequest;",
		"deferred[0].resolve({ok:true,json:()=>Promise.resolve(completeResult(JSON.parse(calls[0].opts.body),'A',1))});await a;assertPending('B',bPending,'B pending','B badge');",
		"deferred[1].resolve({ok:true,json:()=>Promise.resolve(completeResult(JSON.parse(calls[1].opts.body),'B',1))});await b;assert(viewState.approvalVersion===1&&viewState.approvalState==='active','current fetch completion did not apply lifecycle state');assert(viewState.approvalPendingRequest===null,'current fetch completion did not clear pending request');assert(element('approval-status-badge').textContent==='Active','current fetch completion did not update approval badge');assert(element('approval-result-msg').textContent.includes('approval-B'),'current fetch completion did not update result message');",
		"}",
		"async function runJSONRace(){",
		"calls.length=0;deferred.length=0;semanticRequestGeneration=10;setSemantic('A');element('approval-result-msg').textContent='A pending';element('approval-status-badge').textContent='A badge';",
		"const a=submitProposalApproval('approve');assert(calls.length===1,'A JSON race fetch was not submitted');const aJSON=delayedJSONResponse('A');deferred[0].resolve(aJSON.response);await Promise.resolve();await Promise.resolve();",
		"beginSemanticRequest();setSemantic('B');element('approval-result-msg').textContent='B pending';element('approval-status-badge').textContent='B badge';",
		"const b=submitProposalApproval('approve');assert(calls.length===2,'B JSON race fetch was not submitted');const bPending=viewState.approvalPendingRequest;const bJSON=delayedJSONResponse('B');",
		"aJSON.resolveJSON(completeResult(JSON.parse(calls[0].opts.body),'A',1));await a;assertPending('B',bPending,'B pending','B badge');",
		"deferred[1].resolve(bJSON.response);await Promise.resolve();await Promise.resolve();bJSON.resolveJSON(completeResult(JSON.parse(calls[1].opts.body),'B',1));await b;assert(viewState.approvalVersion===1&&viewState.approvalState==='active','current JSON completion did not apply lifecycle state');assert(viewState.approvalPendingRequest===null,'current JSON completion did not clear pending request');assert(element('approval-status-badge').textContent==='Active','current JSON completion did not update approval badge');assert(element('approval-result-msg').textContent.includes('approval-B'),'current JSON completion did not update result message');",
		"}",
		"async function run(){await runFetchRace();await runJSONRace();}",
		"run().catch(error=>{console.error(error.stack||error);process.exitCode=1;});",
	}, "\n")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, nodePath, "--input-type=commonjs")
	cmd.Stdin = strings.NewReader(script)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("controlled embedded approval stale-completion harness failed: %v\n%s", err, output)
	}
}
