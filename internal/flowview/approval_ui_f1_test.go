package flowview

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestEmbeddedSemanticApprovalUsesSecureRetryIdentity(t *testing.T) {
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
		// Keep the pre-implementation test executable so it fails on the
		// observable legacy IDs rather than on a missing helper symbol.
		secureIDSource = "function secureApprovalActionID(){return '';}"
	}
	pendingMatchSource := functionSource("approvalPendingRequestMatches(pending,semantic)")
	if pendingMatchSource == "" {
		pendingMatchSource = "function approvalPendingRequestMatches(){return false;}"
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
	approvalResponseSource := `function approvalResponse(payload){const eventId='event-'+payload.commandId,approvalId='approval-'+payload.commandId,aggregateId='aggregate-'+payload.proposalId,requestDigest='sha256:'+'0'.repeat(64),payloadDigest='sha256:'+'1'.repeat(64),timestamp='2026-09-07T00:00:0'+String(Math.min(9,payload.expectedApprovalVersion))+'Z',nextState=(payload.decision==='approve'||payload.decision==='edit_then_approve')?'active':(payload.decision==='reject'?'rejected':(payload.decision==='revoke'?'revoked':'superseded')),relation=payload.decision==='approve'?'initial':(payload.decision==='edit_then_approve'?'edit':payload.decision),event={schemaId:APPROVAL_EXECUTION_SCHEMA_IDS.event,schemaVersion:2,eventId:eventId,approvalId:approvalId,aggregateId:aggregateId,aggregateVersion:payload.expectedApprovalVersion+1,actorId:'actor-ui-f1',sessionId:'session-ui-f1',workspaceId:'workspace-ui-f1',proposalId:payload.proposalId,evidencePackId:payload.evidencePackId,computedBasisId:payload.computedBasisId,generationId:payload.generationId,intentRevision:payload.intentRevision,decision:payload.decision,approvedText:payload.decision==='edit_then_approve'?payload.editedText:(nextState==='active'?'approved semantic text':''),timestamp:timestamp,lifecycleRelation:relation};if(payload.predecessorApprovalId!==undefined)event.predecessorApprovalId=payload.predecessorApprovalId;const history=[{version:0,state:'none',eventId:'genesis-'+aggregateId,decision:'none'}];for(let i=1;i<=payload.expectedApprovalVersion;i+=1)history.push({version:i,state:payload.expectedState,eventId:'prior-event-'+String(i),approvalId:i===payload.expectedApprovalVersion&&payload.predecessorApprovalId!==undefined?payload.predecessorApprovalId:'prior-approval-'+String(i),decision:'approve'});history.push({version:payload.expectedApprovalVersion+1,state:nextState,eventId:eventId,approvalId:approvalId,decision:payload.decision});const aggregate={schemaId:APPROVAL_EXECUTION_SCHEMA_IDS.aggregate,schemaVersion:2,aggregateId:aggregateId,workspaceId:'workspace-ui-f1',proposalId:payload.proposalId,evidencePackId:payload.evidencePackId,computedBasisId:payload.computedBasisId,generationId:payload.generationId,intentRevision:payload.intentRevision,version:payload.expectedApprovalVersion+1,state:nextState,lastEventId:eventId,lastDecision:payload.decision,history:history};if(nextState==='active')aggregate.activeApprovalId=approvalId;const idempotencyResult={schemaId:APPROVAL_EXECUTION_SCHEMA_IDS.idempotency,schemaVersion:1,idempotencyKey:payload.idempotencyKey,commandId:payload.commandId,actorId:'actor-ui-f1',sessionId:'session-ui-f1',workspaceId:'workspace-ui-f1',proposalId:payload.proposalId,evidencePackId:payload.evidencePackId,computedBasisId:payload.computedBasisId,generationId:payload.generationId,requestDigest:requestDigest,outcome:'committed',approvalId:approvalId,eventId:eventId,aggregateId:aggregateId,aggregateVersion:payload.expectedApprovalVersion+1,state:nextState,committedAt:timestamp,originalCommand:{commandId:payload.commandId,idempotencyKey:payload.idempotencyKey,actorId:'actor-ui-f1',workspaceId:'workspace-ui-f1',proposalId:payload.proposalId,decision:payload.decision,expectedApprovalVersion:payload.expectedApprovalVersion},originalResult:{outcome:'committed',approvalId:approvalId,eventId:eventId,aggregateId:aggregateId,aggregateVersion:payload.expectedApprovalVersion+1,state:nextState}};const outbox={schemaId:APPROVAL_EXECUTION_SCHEMA_IDS.outbox,schemaVersion:1,outboxId:'outbox-'+eventId,eventId:eventId,aggregateId:aggregateId,aggregateVersion:payload.expectedApprovalVersion+1,workspaceId:'workspace-ui-f1',payloadDigest:payloadDigest,committedAt:timestamp,deliveryState:'pending',committedEvent:{eventId:eventId,approvalId:approvalId,aggregateId:aggregateId,aggregateVersion:payload.expectedApprovalVersion+1,workspaceId:'workspace-ui-f1',decision:payload.decision,payloadDigest:payloadDigest}};return {ok:true,json:async()=>({receipt:{idempotencyResult:idempotencyResult,event:event,aggregate:aggregate,outbox:outbox,replayed:false},generationId:payload.generationId,computedBasisId:payload.computedBasisId,validatedSnapshotId:'snapshot-ui-f1',intentRevision:payload.intentRevision,freshness:'current'})};}`

	script := strings.Join([]string{
		"'use strict';",
		stateSource,
		"let currentSpec={workspaceId:'workspace-ui-f1'};",
		secureIDSource,
		pendingMatchSource,
		executionConstants,
		strings.Join(executionSources, "\n"),
		approvalSource,
		"const elements=new Map();",
		"const calls=[];",
		"const alerts=[];",
		"let uuidCalls=0;",
		"Object.defineProperty(globalThis,'crypto',{configurable:true,writable:true,value:{randomUUID(){uuidCalls+=1;return 'uuid-'+String(uuidCalls);}}});",
		"function element(id){if(!elements.has(id))elements.set(id,{style:{},textContent:'',hidden:false});return elements.get(id);}",
		"globalThis.document={getElementById:id=>element(id)};",
		"globalThis.alert=value=>alerts.push(value);",
		"function api(url,opts){const call={url:url,opts:opts||{}};calls.push(call);if(calls.length===1)return Promise.reject(new Error('transport rejected'));return new Promise((resolve,reject)=>{call.resolve=resolve;call.reject=reject;});}",
		"function assert(condition,message){if(!condition)throw new Error(message);}",
		"function assertActorless(payload){for(const key of ['actorId','sessionId','workspaceId','approver','actor','session','workspace'])assert(!(key in payload),'authority field leaked: '+key);}",
		approvalResponseSource,
		"async function runInvalidExecutionCases(){const cases=[['missing-top',result=>{delete result.freshness}],['unknown-top',result=>{result.unexpected='SECRET_PATH_TOKEN'}],['wrong-proposal',result=>{result.receipt.event.proposalId='other-proposal'}],['wrong-pack',result=>{result.receipt.aggregate.evidencePackId='other-pack'}],['wrong-basis',result=>{result.receipt.event.computedBasisId='other-basis'}],['wrong-generation',result=>{result.generationId='other-generation'}],['wrong-intent',result=>{result.receipt.event.intentRevision=8}],['wrong-command',result=>{result.receipt.idempotencyResult.commandId='other-command'}],['wrong-idempotency',result=>{result.receipt.idempotencyResult.idempotencyKey='other-idempotency'}],['wrong-event-identity',result=>{result.receipt.aggregate.lastEventId='other-event'}],['wrong-aggregate-identity',result=>{result.receipt.aggregate.aggregateId='other-aggregate'}],['wrong-version',result=>{result.receipt.event.aggregateVersion=2}],['wrong-prior-state',result=>{result.receipt.aggregate.history[0].state='active'}],['wrong-predecessor',result=>{result.receipt.event.predecessorApprovalId='unexpected-predecessor'}],['wrong-transition',result=>{result.receipt.event.lifecycleRelation='edit'}],['wrong-schema',result=>{result.receipt.outbox.schemaVersion=2}],['wrong-outbox',result=>{result.receipt.outbox.aggregateVersion=2}]];for(const item of cases){const label=item[0];viewState={...viewState,proposalId:'proposal-ui-f1',evidencePackId:'pack-ui-f1',computedBasisId:'basis-ui-f1',generationId:'generation-ui-f1',validatedAgainstSnapshotId:'snapshot-ui-f1',intentRevision:7,approvalVersion:0,approvalState:'none',approvalExpectedVersion:0,approvalExpectedState:'none',activeApprovalId:'',approvalPendingRequest:null,approvalCommandId:'',approvalIdempotencyKey:'',approvalDecision:''};element('approval-status-badge').textContent='badge-'+label;element('approval-result-msg').textContent='history-'+label;const promise=submitProposalApproval('approve');const call=calls[calls.length-1];const pending=viewState.approvalPendingRequest;const body=call.opts.body;const response=approvalResponse(JSON.parse(body));const result=await response.json();item[1](result);call.resolve({ok:true,status:200,json:async()=>result});await promise;assert(viewState.approvalState==='none'&&viewState.approvalVersion===0&&viewState.approvalExpectedState==='none'&&viewState.approvalExpectedVersion===0&&viewState.activeApprovalId===''&&viewState.approvalCommandId===pending.commandId&&viewState.approvalIdempotencyKey===pending.idempotencyKey&&viewState.approvalDecision==='approve','invalid '+label+' changed lifecycle state');assert(viewState.approvalPendingRequest===pending&&Object.isFrozen(pending)&&call.opts.body===body,'invalid '+label+' changed frozen pending request');assert(element('approval-status-badge').textContent==='badge-'+label,'invalid '+label+' changed approval badge');assert(element('approval-result-msg').textContent==='approval request failed; please retry','invalid '+label+' did not use bounded failure');assert(!element('approval-result-msg').textContent.includes('SECRET_PATH_TOKEN'),'invalid '+label+' leaked response detail');}}",
		"async function run(){",
		"viewState={...viewState,proposalId:'proposal-ui-f1',evidencePackId:'pack-ui-f1',computedBasisId:'basis-ui-f1',generationId:'generation-ui-f1',validatedAgainstSnapshotId:'snapshot-ui-f1',intentRevision:7,approvalVersion:0,approvalState:'none',approvalExpectedVersion:0,approvalExpectedState:'none',activeApprovalId:''};",
		"await submitProposalApproval('approve');",
		"assert(calls.length===1,'first approval call missing');",
		"const firstBody=calls[0].opts.body;const first=JSON.parse(firstBody);",
		"assert(first.commandId==='cmd-ui-uuid-1'&&first.idempotencyKey==='idem-ui-uuid-2','secure random IDs were not used: '+firstBody);",
		"assert(first.proposalId==='proposal-ui-f1'&&first.evidencePackId==='pack-ui-f1'&&first.computedBasisId==='basis-ui-f1'&&first.generationId==='generation-ui-f1'&&first.intentRevision===7&&first.decision==='approve'&&first.expectedApprovalVersion===0&&first.expectedState==='none','first approval body changed: '+firstBody);",
		"assertActorless(first);",
		"assert(viewState.approvalPendingRequest&&Object.isFrozen(viewState.approvalPendingRequest),'pending request is not immutable');",
		"const retry=submitProposalApproval('approve');",
		"assert(calls.length===2,'retry approval call missing');",
		"assert(calls[1].opts.body===firstBody,'retry body is not byte-equivalent');",
		"assert(calls[1].opts.body===JSON.stringify(viewState.approvalPendingRequest),'retry did not use stored pending request');",
		"calls[1].resolve({ok:true,json:async()=>({receipt:{event:{approvalId:'approval-ui-f1',decision:'approve'},aggregate:{activeApprovalId:'approval-ui-f1',version:1,state:'active'}}})});",
		"await retry;",
		"assert(viewState.approvalVersion===0&&viewState.approvalState==='none'&&viewState.approvalExpectedVersion===0&&viewState.approvalExpectedState==='none','malformed 2xx receipt mutated lifecycle state');",
		"assert(viewState.approvalPendingRequest&&Object.isFrozen(viewState.approvalPendingRequest)&&calls[1].opts.body===firstBody,'malformed 2xx receipt cleared or changed frozen retry identity');",
		"const validRetry=submitProposalApproval('approve');assert(calls.length===3,'valid retry approval call missing');calls[2].resolve(approvalResponse(JSON.parse(calls[2].opts.body)));await validRetry;",
		"assert(viewState.approvalVersion===1&&viewState.approvalState==='active'&&viewState.approvalExpectedVersion===1&&viewState.approvalExpectedState==='active','success did not apply committed version/state');",
		"assert(viewState.approvalPendingRequest===null&&viewState.approvalCommandId===''&&viewState.approvalIdempotencyKey===''&&viewState.approvalDecision==='','success did not clear pending approval identity');",
		"element('approval-edited-text').value='edited semantic meaning';const next=submitProposalApproval('edit_then_approve');",
		"assert(calls.length===4,'next approval call missing');",
		"const nextBody=calls[3].opts.body;const nextPayload=JSON.parse(nextBody);",
		"assert(nextPayload.commandId!=='cmd-ui-uuid-1'&&nextPayload.idempotencyKey!=='idem-ui-uuid-2','next intentional action reused old IDs');",
		"assert(nextPayload.expectedApprovalVersion===1&&nextPayload.expectedState==='active'&&nextPayload.decision==='edit_then_approve'&&nextPayload.predecessorApprovalId==='approval-'+first.commandId,'next approval did not use updated expected version/state: '+nextBody);",
		"assertActorless(nextPayload);",
		"calls[3].resolve(approvalResponse(nextPayload));",
		"await next;",
		"assert(viewState.approvalVersion===2&&viewState.approvalState==='active'&&viewState.approvalExpectedVersion===2&&viewState.approvalExpectedState==='active','edit_then_approve result did not apply');",
		"assert(viewState.approvalPendingRequest===null&&viewState.approvalCommandId===''&&viewState.approvalIdempotencyKey===''&&viewState.approvalDecision==='','edit_then_approve did not clear pending approval identity');",
		"await runInvalidExecutionCases();",
		"assert(alerts.length===0,'unexpected approval alert: '+alerts.join('|'));",
		"}",
		"run().catch(error=>{console.error(error.stack||error);process.exitCode=1;});",
	}, "\n")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, nodePath, "--input-type=commonjs")
	cmd.Stdin = strings.NewReader(script)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("controlled embedded approval harness failed: %v\n%s", err, output)
	}
}

func TestEmbeddedSemanticApprovalR1ActualSubmitProjection(t *testing.T) {
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
	parts := []string{
		"'use strict';",
		stateSource,
		"let semanticRequestGeneration=1;let currentSpec={flowId:'map-ui-f1',taskId:'task-ui-f1',workspaceId:'workspace-ui-f1'};",
		"const APPROVAL_EXECUTION_SCHEMA_IDS=Object.freeze({event:'https://codeflow.local/schemas/rflsc.approval-event.v2.schema.json',aggregate:'https://codeflow.local/schemas/rflsc.approval-aggregate.v2.schema.json',idempotency:'https://codeflow.local/schemas/rflsc.approval-idempotency-result.v1.schema.json',outbox:'https://codeflow.local/schemas/rflsc.approval-outbox.v1.schema.json'});const APPROVAL_EXECUTION_DECISIONS=Object.freeze(['approve','edit_then_approve','reject','revoke','supersede']);const APPROVAL_EXECUTION_STATES=Object.freeze(['none','active','rejected','revoked','superseded']);const APPROVAL_EXECUTION_RELATIONS=Object.freeze(['initial','edit','reject','revoke','supersede']);const APPROVAL_EXECUTION_DIGEST=/^sha256:[0-9a-f]{64}$/;const APPROVAL_EXECUTION_MAX_VERSION=1000000000;",
		"const APPROVAL_HISTORY_SCHEMA_ID='https://codeflow.local/schemas/rflsc.approval-history.v1.schema.json';const APPROVAL_HISTORY_SCHEMA_VERSION=1;const APPROVAL_EVENT_SCHEMA_ID='https://codeflow.local/schemas/rflsc.approval-event.v2.schema.json';const APPROVAL_AGGREGATE_SCHEMA_ID='https://codeflow.local/schemas/rflsc.approval-aggregate.v2.schema.json';const APPROVAL_HISTORY_STATES=['none','active','rejected','revoked','superseded'];const APPROVAL_HISTORY_DECISIONS=['approve','edit_then_approve','reject','revoke','supersede'];const APPROVAL_HISTORY_RELATIONS=['initial','edit','reject','revoke','supersede'];const APPROVAL_HISTORY_MAX_RECORDS=256;let approvalHistoryModel=null;let approvalHistoryLoadSequence=0;let approvalHistoryMutationSequence=0;",
		"const APPROVAL_VALIDATED_EXECUTION_RESULTS=new WeakSet();",
	}
	for _, name := range []string{
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
	} {
		source := functionSource(name)
		if source == "" {
			if name == "appendApprovalHistoryLocalReceipt(result)" {
				// Keep the regression harness executable against the pre-fix source.
				source = "function appendApprovalHistoryLocalReceipt(){return false;}"
			} else {
				t.Fatalf("embedded function %q not found", name)
			}
		}
		parts = append(parts, source)
	}
	approvalSource := functionSource("submitProposalApproval(decision)")
	if approvalSource == "" {
		t.Fatal("embedded approval function not found")
	}
	parts = append(parts, approvalSource)

	approvalResponseSource := `function approvalResponse(payload,replayed){const eventId='event-'+payload.commandId,approvalId='approval-'+payload.commandId,aggregateId='aggregate-'+payload.proposalId,requestDigest='sha256:'+'0'.repeat(64),payloadDigest='sha256:'+'1'.repeat(64),timestamp='2026-09-07T00:00:00Z',event={schemaId:'https://codeflow.local/schemas/rflsc.approval-event.v2.schema.json',schemaVersion:2,eventId:eventId,approvalId:approvalId,aggregateId:aggregateId,aggregateVersion:payload.expectedApprovalVersion+1,actorId:'actor-ui-f1',sessionId:'session-ui-f1',workspaceId:'workspace-ui-f1',proposalId:payload.proposalId,evidencePackId:payload.evidencePackId,computedBasisId:payload.computedBasisId,generationId:payload.generationId,intentRevision:payload.intentRevision,decision:payload.decision,approvedText:'approved semantic text',timestamp:timestamp,lifecycleRelation:'initial'},history=[{version:0,state:'none',eventId:'genesis-'+aggregateId,decision:'none'},{version:1,state:'active',eventId:eventId,approvalId:approvalId,decision:'approve'}],aggregate={schemaId:'https://codeflow.local/schemas/rflsc.approval-aggregate.v2.schema.json',schemaVersion:2,aggregateId:aggregateId,workspaceId:'workspace-ui-f1',proposalId:payload.proposalId,evidencePackId:payload.evidencePackId,computedBasisId:payload.computedBasisId,generationId:payload.generationId,intentRevision:payload.intentRevision,version:1,state:'active',lastEventId:eventId,lastDecision:'approve',activeApprovalId:approvalId,history:history},idempotencyResult={schemaId:'https://codeflow.local/schemas/rflsc.approval-idempotency-result.v1.schema.json',schemaVersion:1,idempotencyKey:payload.idempotencyKey,commandId:payload.commandId,actorId:'actor-ui-f1',sessionId:'session-ui-f1',workspaceId:'workspace-ui-f1',proposalId:payload.proposalId,evidencePackId:payload.evidencePackId,computedBasisId:payload.computedBasisId,generationId:payload.generationId,requestDigest:requestDigest,outcome:'committed',approvalId:approvalId,eventId:eventId,aggregateId:aggregateId,aggregateVersion:1,state:'active',committedAt:timestamp,originalCommand:{commandId:payload.commandId,idempotencyKey:payload.idempotencyKey,actorId:'actor-ui-f1',workspaceId:'workspace-ui-f1',proposalId:payload.proposalId,decision:payload.decision,expectedApprovalVersion:0},originalResult:{outcome:'committed',approvalId:approvalId,eventId:eventId,aggregateId:aggregateId,aggregateVersion:1,state:'active'}},outbox={schemaId:'https://codeflow.local/schemas/rflsc.approval-outbox.v1.schema.json',schemaVersion:1,outboxId:'outbox-'+eventId,eventId:eventId,aggregateId:aggregateId,aggregateVersion:1,workspaceId:'workspace-ui-f1',payloadDigest:payloadDigest,committedAt:timestamp,deliveryState:'pending',committedEvent:{eventId:eventId,approvalId:approvalId,aggregateId:aggregateId,aggregateVersion:1,workspaceId:'workspace-ui-f1',decision:'approve',payloadDigest:payloadDigest}};return {receipt:{idempotencyResult:idempotencyResult,event:event,aggregate:aggregate,outbox:outbox,replayed:!!replayed},generationId:payload.generationId,computedBasisId:payload.computedBasisId,validatedSnapshotId:'snapshot-ui-f1',intentRevision:payload.intentRevision,freshness:'current'};}`

	script := strings.Join(append(parts,
		"const elements=new Map();const calls=[];",
		"let uuidCalls=0;Object.defineProperty(globalThis,'crypto',{configurable:true,writable:true,value:{randomUUID(){uuidCalls+=1;return 'uuid-'+String(uuidCalls);}}});",
		"function element(id){if(!elements.has(id)){const node={id:id,style:{},dataset:{},_text:'',hidden:false,children:[],setAttribute(){},appendChild(child){this.children.push(child);}};Object.defineProperty(node,'textContent',{get(){return this._text;},set(value){this._text=String(value);if(id==='approval-history-events')this.children=[];}});elements.set(id,node);}return elements.get(id);}",
		"globalThis.document={getElementById:id=>element(id),createElement:tag=>({tagName:tag,textContent:'',children:[],setAttribute(){},appendChild(child){this.children.push(child);}})};",
		"function syncApprovalControls(){};",
		"function assert(condition,message){if(!condition)throw new Error(message);}",
		"let responseQueue=[];function api(url,opts){const call={url:url,opts:opts||{}};calls.push(call);const next=responseQueue.shift();if(next==='transport')return Promise.reject(new Error('transport lost'));if(next==='success')return Promise.resolve({ok:true,status:200,json:async()=>approvalResponse(JSON.parse(call.opts.body),false)});if(next==='replay')return Promise.resolve({ok:true,status:200,json:async()=>approvalResponse(JSON.parse(call.opts.body),true)});throw new Error('unexpected approval request');}",
		approvalResponseSource,
		"function historicalApprovalResult(payload,replayed){const result=approvalResponse(payload,replayed);result.freshness='historical';return result;}",
		"const originalApprovalAPI=api;let controlledApprovalMode=false;let controlledApprovalRequests=[];api=function(url,opts){if(!controlledApprovalMode)return originalApprovalAPI(url,opts);const call={url:url,opts:opts||{}};calls.push(call);return new Promise((resolve,reject)=>controlledApprovalRequests.push({resolve:resolve,reject:reject,call:call}));};",
		"function resetState(){viewState={...viewState,proposalId:'proposal-ui-f1',evidencePackId:'pack-ui-f1',computedBasisId:'basis-ui-f1',generationId:'generation-ui-f1',validatedAgainstSnapshotId:'snapshot-ui-f1',intentRevision:7,approvalCommandId:'',approvalIdempotencyKey:'',approvalDecision:'',approvalPendingRequest:null,approvalExpectedVersion:0,approvalExpectedState:'none',approvalPredecessorApprovalId:'',approvalVersion:0,approvalState:'none',activeApprovalId:''};currentSpec={flowId:'map-ui-f1',taskId:'task-ui-f1',workspaceId:'workspace-ui-f1'};clearApprovalHistoryUI('current');}",
		"function listText(){return element('approval-history-events').children.map(child=>child.textContent).join('|');}",
		"async function runConcurrentReplayFreshness(){resetState();calls.length=0;controlledApprovalRequests=[];controlledApprovalMode=true;const first=submitProposalApproval('approve');assert(calls.length===1,'first concurrent approval call missing');const firstBody=calls[0].opts.body;const firstPending=viewState.approvalPendingRequest;const second=submitProposalApproval('approve');assert(firstPending&&Object.isFrozen(firstPending)&&viewState.approvalPendingRequest===firstPending,'same-command concurrent approval replaced the frozen pending request');assert(calls.length===2&&calls[1].opts.body===firstBody,'same-command concurrent approval did not reuse exact frozen body');controlledApprovalRequests[0].resolve({ok:true,status:200,json:async()=>approvalResponse(JSON.parse(firstBody),false)});await first;assert(approvalHistoryModel&&approvalHistoryModel.freshness==='current'&&approvalHistoryModel.events.length===1&&approvalHistoryModel.aggregate.version===1,'first concurrent completion did not project current history');assert(viewState.approvalPendingRequest===null,'pending request was not cleared only after first projection');controlledApprovalRequests[1].resolve({ok:true,status:200,json:async()=>historicalApprovalResult(JSON.parse(firstBody),true)});await second;assert(approvalHistoryModel&&approvalHistoryModel.freshness==='historical'&&approvalHistoryModel.events.length===1&&approvalHistoryModel.aggregate.version===1,'replayed concurrent completion did not project historical freshness without duplication');assert(viewState.approvalPendingRequest===null&&viewState.approvalVersion===1&&viewState.approvalState==='active','replayed concurrent completion changed lifecycle or pending state');assert(calls.length===2&&calls.every(call=>call.url==='/api/semantic/approve'),'concurrent replay issued an unexpected history request');controlledApprovalMode=false;controlledApprovalRequests=[];calls.length=0;}",
		"async function runRejectedConcurrentReplay(){resetState();calls.length=0;controlledApprovalRequests=[];controlledApprovalMode=true;const first=submitProposalApproval('approve');const firstBody=calls[0].opts.body;const second=submitProposalApproval('approve');assert(calls.length===2&&calls[1].opts.body===firstBody,'negative replay did not reuse exact frozen body');controlledApprovalRequests[0].resolve({ok:true,status:200,json:async()=>approvalResponse(JSON.parse(firstBody),false)});await first;const before=JSON.stringify({model:approvalHistoryModel,view:viewState,badge:element('approval-status-badge').textContent,events:listText()});controlledApprovalRequests[1].resolve({ok:true,status:200,json:async()=>historicalApprovalResult(JSON.parse(firstBody),false)});await second;assert(JSON.stringify({model:approvalHistoryModel,view:viewState,badge:element('approval-status-badge').textContent,events:listText()})===before,'non-replayed concurrent completion mutated committed state');assert(calls.length===2&&calls.every(call=>call.url==='/api/semantic/approve'),'negative replay issued an unexpected history request');controlledApprovalMode=false;controlledApprovalRequests=[];calls.length=0;}",
		"async function runReplayFreshness(){resetState();responseQueue=['success'];const first=submitProposalApproval('approve');const firstRequest=JSON.parse(calls[0].opts.body);const firstPending=viewState.approvalPendingRequest;await first;assert(approvalHistoryModel&&approvalHistoryModel.freshness==='current'&&approvalHistoryModel.events.length===1&&approvalHistoryModel.aggregate.version===1,'initial current receipt was not displayed');const replayRaw=historicalApprovalResult(firstRequest,true);assert(replayRaw.receipt.replayed===true,'replay fixture was not marked replayed');const replaySemantic={proposalId:firstRequest.proposalId,evidencePackId:firstRequest.evidencePackId,computedBasisId:firstRequest.computedBasisId,generationId:firstRequest.generationId,validatedSnapshotId:'snapshot-ui-f1',intentRevision:firstRequest.intentRevision,decision:firstRequest.decision,editedText:'',expectedApprovalVersion:firstRequest.expectedApprovalVersion,expectedState:firstRequest.expectedState,predecessorApprovalId:''};const replayed=validateApprovalExecutionResult(replayRaw,firstPending,replaySemantic,{state:'none',version:0,activeApprovalId:'',aggregate:null});assert(replayed,'historical replay fixture failed strict validation');assert(appendApprovalHistoryReceipt(replayed),'historical replay projection failed');assert(approvalHistoryModel.freshness==='historical'&&element('approval-history-freshness').textContent==='historical','historical replay freshness was not projected');assert(approvalHistoryModel.events.length===1&&approvalHistoryModel.aggregate.version===1&&listText()==='v1 · Approved','historical replay duplicated or changed the event');assert(calls.length===1&&calls.every(call=>call.url==='/api/semantic/approve'),'history replay issued an unexpected history request');const lifecycleBefore=JSON.stringify({state:viewState.approvalState,version:viewState.approvalVersion,expectedState:viewState.approvalExpectedState,expectedVersion:viewState.approvalExpectedVersion,pending:viewState.approvalPendingRequest});const invalid=JSON.parse(JSON.stringify(replayRaw));invalid.freshness='stale';assert(!validateApprovalExecutionResult(invalid,firstPending,replaySemantic,{state:'none',version:0,activeApprovalId:'',aggregate:null}),'invalid freshness was accepted by strict validator');assert(JSON.stringify({state:viewState.approvalState,version:viewState.approvalVersion,expectedState:viewState.approvalExpectedState,expectedVersion:viewState.approvalExpectedVersion,pending:viewState.approvalPendingRequest})===lifecycleBefore,'invalid freshness changed lifecycle or pending identity');}",
		"async function runConcurrentReplayEventMismatch(){resetState();calls.length=0;controlledApprovalRequests=[];controlledApprovalMode=true;const first=submitProposalApproval('approve');const firstBody=calls[0].opts.body;const firstPending=viewState.approvalPendingRequest;const second=submitProposalApproval('approve');assert(firstPending&&Object.isFrozen(firstPending)&&viewState.approvalPendingRequest===firstPending,'event mismatch replay replaced the frozen pending request');assert(calls.length===2&&calls[1].opts.body===firstBody,'event mismatch replay did not reuse exact frozen body');controlledApprovalRequests[0].resolve({ok:true,status:200,json:async()=>approvalResponse(JSON.parse(firstBody),false)});await first;const replay=historicalApprovalResult(JSON.parse(firstBody),true);replay.receipt.event.approvedText='different approved semantic text';const semantic={proposalId:firstPending.proposalId,evidencePackId:firstPending.evidencePackId,computedBasisId:firstPending.computedBasisId,generationId:firstPending.generationId,validatedSnapshotId:'snapshot-ui-f1',intentRevision:firstPending.intentRevision,decision:firstPending.decision,editedText:'',expectedApprovalVersion:firstPending.expectedApprovalVersion,expectedState:firstPending.expectedState,predecessorApprovalId:''};const strictReplay=validateApprovalExecutionResult(replay,firstPending,semantic,{state:'none',version:0,activeApprovalId:'',aggregate:null});assert(strictReplay,'event mismatch fixture was not strict-valid against captured pending/previous');const before=JSON.stringify({view:viewState,model:approvalHistoryModel,badge:element('approval-status-badge').textContent,message:element('approval-result-msg').textContent,state:element('approval-history-state').textContent,version:element('approval-history-version').textContent,freshness:element('approval-history-freshness').textContent,status:element('approval-history-status').textContent,events:listText(),sequence:approvalHistoryMutationSequence});controlledApprovalRequests[1].resolve({ok:true,status:200,json:async()=>replay});await second;assert(JSON.stringify({view:viewState,model:approvalHistoryModel,badge:element('approval-status-badge').textContent,message:element('approval-result-msg').textContent,state:element('approval-history-state').textContent,version:element('approval-history-version').textContent,freshness:element('approval-history-freshness').textContent,status:element('approval-history-status').textContent,events:listText(),sequence:approvalHistoryMutationSequence})===before,'strict-valid event mismatch replay mutated committed state');assert(approvalHistoryModel.freshness==='current'&&approvalHistoryModel.events.length===1&&approvalHistoryModel.aggregate.version===1,'event mismatch replay changed freshness or history shape');assert(element('approval-history-freshness').textContent==='current'&&element('approval-history-events').children.length===1&&element('approval-history-status').textContent==='Loaded from durable history','event mismatch replay changed history DOM');assert(viewState.approvalPendingRequest===null&&viewState.approvalState==='active'&&viewState.approvalVersion===1,'event mismatch replay changed lifecycle or pending state');assert(calls.length===2&&calls.every(call=>call.url==='/api/semantic/approve'),'event mismatch replay issued an unexpected history request');controlledApprovalMode=false;controlledApprovalRequests=[];calls.length=0;}",
		"async function runConcurrentReplayLateIdentity(){resetState();calls.length=0;controlledApprovalRequests=[];controlledApprovalMode=true;const first=submitProposalApproval('approve');const firstBody=calls[0].opts.body;const firstPending=viewState.approvalPendingRequest;const second=submitProposalApproval('approve');assert(firstPending&&Object.isFrozen(firstPending)&&viewState.approvalPendingRequest===firstPending,'late identity replay replaced the frozen pending request');assert(calls.length===2&&calls[1].opts.body===firstBody,'late identity replay did not reuse exact frozen body');controlledApprovalRequests[0].resolve({ok:true,status:200,json:async()=>approvalResponse(JSON.parse(firstBody),false)});await first;const before=JSON.stringify({view:viewState,model:approvalHistoryModel,badge:element('approval-status-badge').textContent,message:element('approval-result-msg').textContent,state:element('approval-history-state').textContent,version:element('approval-history-version').textContent,freshness:element('approval-history-freshness').textContent,status:element('approval-history-status').textContent,events:listText(),sequence:approvalHistoryMutationSequence});currentSpec={...currentSpec,taskId:'task-ui-f1-late'};const replay=historicalApprovalResult(JSON.parse(firstBody),true);controlledApprovalRequests[1].resolve({ok:true,status:200,json:async()=>replay});await second;assert(JSON.stringify({view:viewState,model:approvalHistoryModel,badge:element('approval-status-badge').textContent,message:element('approval-result-msg').textContent,state:element('approval-history-state').textContent,version:element('approval-history-version').textContent,freshness:element('approval-history-freshness').textContent,status:element('approval-history-status').textContent,events:listText(),sequence:approvalHistoryMutationSequence})===before,'late identity replay mutated committed state');assert(approvalHistoryModel.freshness==='current'&&approvalHistoryModel.events.length===1&&approvalHistoryModel.aggregate.version===1,'late identity replay changed freshness or history shape');assert(element('approval-history-freshness').textContent==='current'&&element('approval-history-events').children.length===1&&element('approval-history-status').textContent==='Loaded from durable history','late identity replay changed history DOM');assert(viewState.approvalPendingRequest===null&&viewState.approvalState==='active'&&viewState.approvalVersion===1,'late identity replay changed lifecycle or pending state');assert(calls.length===2&&calls.every(call=>call.url==='/api/semantic/approve'),'late identity replay issued an unexpected history request');controlledApprovalMode=false;controlledApprovalRequests=[];calls.length=0;}",
		"async function run(){await runConcurrentReplayFreshness();await runRejectedConcurrentReplay();await runConcurrentReplayEventMismatch();await runConcurrentReplayLateIdentity();await runReplayFreshness();calls.length=0;",
		"resetState();responseQueue=['success'];await submitProposalApproval('approve');assert(calls.length===1,'first approval should make one mutation call');assert(calls.every(call=>call.url==='/api/semantic/approve'),'first approval made an unexpected history request: '+calls.map(call=>call.url).join('|'));assert(listText()==='v1 · Approved','valid first approval was not projected into empty history: '+listText());assert(element('approval-history-state').textContent==='active'&&element('approval-history-version').textContent==='1'&&element('approval-history-freshness').textContent==='current','first approval history facts were not rendered');assert(viewState.approvalState==='active'&&viewState.approvalVersion===1&&viewState.approvalExpectedState==='active'&&viewState.approvalExpectedVersion===1&&viewState.activeApprovalId,'first approval did not update lifecycle state');assert(viewState.approvalPendingRequest===null,'first approval did not clear pending request');",
		"calls.length=0;responseQueue=['transport'];resetState();const firstAttempt=submitProposalApproval('approve');await Promise.resolve();const frozen=viewState.approvalPendingRequest;const frozenBody=calls[0].opts.body;assert(frozen&&Object.isFrozen(frozen),'transport loss did not retain frozen pending request');await firstAttempt;assert(viewState.approvalPendingRequest===frozen&&calls[0].opts.body===frozenBody,'transport loss changed frozen pending request');responseQueue=['replay'];await submitProposalApproval('approve');assert(calls.length===2&&calls.every(call=>call.url==='/api/semantic/approve'),'replay made an unexpected history request: '+calls.map(call=>call.url).join('|'));assert(calls[1].opts.body===frozenBody,'replay did not reuse exact frozen request body');assert(listText()==='v1 · Approved','replayed approval duplicated or omitted the projected event: '+listText());assert(element('approval-history-state').textContent==='active'&&element('approval-history-version').textContent==='1','replayed approval history facts were incorrect');assert(viewState.approvalState==='active'&&viewState.approvalVersion===1&&viewState.approvalPendingRequest===null,'replayed approval did not finish lifecycle state');",
		"}",
		"run().catch(error=>{console.error(error.stack||error);process.exitCode=1;});",
	), "\n")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, nodePath, "--input-type=commonjs")
	cmd.Stdin = strings.NewReader(script)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("controlled embedded actual-submit approval harness failed: %v\n%s", err, output)
	}
}
