package flowview

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestEmbeddedSemanticApprovalDecisionMatrix(t *testing.T) {
	for _, marker := range []string{
		`id="btn-semantic-approve"`, `name="approve"`, `aria-label="Approve semantic proposal"`,
		`id="btn-semantic-edit-then-approve"`, `name="edit_then_approve"`, `aria-label="Edit and approve semantic proposal"`,
		`id="btn-semantic-reject"`, `name="reject"`, `aria-label="Reject semantic proposal"`,
		`id="btn-semantic-revoke"`, `name="revoke"`, `aria-label="Revoke active semantic approval"`,
		`id="btn-semantic-supersede"`, `name="supersede"`, `aria-label="Supersede active semantic approval"`,
		`id="approval-edited-text"`, `name="editedText"`, `aria-label="Edited semantic proposal text"`, `maxlength="4096"`,
	} {
		if !strings.Contains(IndexHTML, marker) {
			t.Fatalf("approval accessibility marker %q not found", marker)
		}
	}

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
		secureIDSource = "function secureApprovalActionID(){return ''; }"
	}
	pendingMatchSource := functionSource("approvalPendingRequestMatches(pending,semantic)")
	if pendingMatchSource == "" {
		pendingMatchSource = "function approvalPendingRequestMatches(){return false;}"
	}
	errorMessageSource := functionSource("approvalErrorMessage(payload)")
	if errorMessageSource == "" {
		errorMessageSource = "function approvalErrorMessage(){return 'approval request failed; please retry';}"
	}
	editedValueSource := functionSource("approvalEditedTextValue()")
	if editedValueSource == "" {
		editedValueSource = "function approvalEditedTextValue(){return '';}"
	}
	editedLengthSource := functionSource("approvalEditedTextLength(value)")
	if editedLengthSource == "" {
		editedLengthSource = "function approvalEditedTextLength(){return 0;}"
	}
	admissionSource := functionSource("approvalLifecycleAdmission(decision,editedText)")
	if admissionSource == "" {
		admissionSource = "function approvalLifecycleAdmission(){return true;}"
	}
	admissionMessageSource := functionSource("approvalAdmissionMessage(decision,editedText)")
	if admissionMessageSource == "" {
		admissionMessageSource = "function approvalAdmissionMessage(){return 'approval action is not allowed in the current lifecycle state';}"
	}
	controlsSource := functionSource("syncApprovalControls()")
	if controlsSource == "" {
		controlsSource = "function syncApprovalControls(){}"
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
	approvalResponseSource := `function completeResult(payload,index){const decision=payload.decision,nextState=(decision==='approve'||decision==='edit_then_approve')?'active':(decision==='reject'?'rejected':(decision==='revoke'?'revoked':'superseded')),eventId='event-'+String(index),approvalId='approval-'+String(index),aggregateId='aggregate-ui-f4',timestamp='2026-09-07T00:00:0'+String(Math.min(9,payload.expectedApprovalVersion))+'Z',event={schemaId:APPROVAL_EXECUTION_SCHEMA_IDS.event,schemaVersion:2,eventId:eventId,approvalId:approvalId,aggregateId:aggregateId,aggregateVersion:payload.expectedApprovalVersion+1,actorId:'actor-ui-f4',sessionId:'session-ui-f4',workspaceId:'workspace-ui-f4',proposalId:payload.proposalId,evidencePackId:payload.evidencePackId,computedBasisId:payload.computedBasisId,generationId:payload.generationId,intentRevision:payload.intentRevision,decision:decision,approvedText:decision==='edit_then_approve'?payload.editedText:(nextState==='active'?'approved semantic text':''),timestamp:timestamp,lifecycleRelation:decision==='approve'?'initial':(decision==='edit_then_approve'?'edit':decision)},history=[{version:0,state:'none',eventId:'genesis-ui-f4',decision:'none'}];for(let i=1;i<=payload.expectedApprovalVersion;i+=1)history.push({version:i,state:'active',eventId:'prior-event-'+String(i),approvalId:i===payload.expectedApprovalVersion&&payload.predecessorApprovalId!==undefined?payload.predecessorApprovalId:'prior-approval-'+String(i),decision:i===1?'approve':'edit_then_approve'});history.push({version:payload.expectedApprovalVersion+1,state:nextState,eventId:eventId,approvalId:approvalId,decision:decision});if(payload.predecessorApprovalId!==undefined)event.predecessorApprovalId=payload.predecessorApprovalId;const aggregate={schemaId:APPROVAL_EXECUTION_SCHEMA_IDS.aggregate,schemaVersion:2,aggregateId:aggregateId,workspaceId:'workspace-ui-f4',proposalId:payload.proposalId,evidencePackId:payload.evidencePackId,computedBasisId:payload.computedBasisId,generationId:payload.generationId,intentRevision:payload.intentRevision,version:payload.expectedApprovalVersion+1,state:nextState,lastEventId:eventId,lastDecision:decision,history:history};if(nextState==='active')aggregate.activeApprovalId=approvalId;const requestDigest='sha256:'+'0'.repeat(64),payloadDigest='sha256:'+'1'.repeat(64),idempotencyResult={schemaId:APPROVAL_EXECUTION_SCHEMA_IDS.idempotency,schemaVersion:1,idempotencyKey:payload.idempotencyKey,commandId:payload.commandId,actorId:'actor-ui-f4',sessionId:'session-ui-f4',workspaceId:'workspace-ui-f4',proposalId:payload.proposalId,evidencePackId:payload.evidencePackId,computedBasisId:payload.computedBasisId,generationId:payload.generationId,requestDigest:requestDigest,outcome:'committed',approvalId:approvalId,eventId:eventId,aggregateId:aggregateId,aggregateVersion:payload.expectedApprovalVersion+1,state:nextState,committedAt:timestamp,originalCommand:{commandId:payload.commandId,idempotencyKey:payload.idempotencyKey,actorId:'actor-ui-f4',workspaceId:'workspace-ui-f4',proposalId:payload.proposalId,decision:decision,expectedApprovalVersion:payload.expectedApprovalVersion},originalResult:{outcome:'committed',approvalId:approvalId,eventId:eventId,aggregateId:aggregateId,aggregateVersion:payload.expectedApprovalVersion+1,state:nextState}},outbox={schemaId:APPROVAL_EXECUTION_SCHEMA_IDS.outbox,schemaVersion:1,outboxId:'outbox-'+eventId,eventId:eventId,aggregateId:aggregateId,aggregateVersion:payload.expectedApprovalVersion+1,workspaceId:'workspace-ui-f4',payloadDigest:payloadDigest,committedAt:timestamp,deliveryState:'pending',committedEvent:{eventId:eventId,approvalId:approvalId,aggregateId:aggregateId,aggregateVersion:payload.expectedApprovalVersion+1,workspaceId:'workspace-ui-f4',decision:decision,payloadDigest:payloadDigest}};return {receipt:{idempotencyResult:idempotencyResult,event:event,aggregate:aggregate,outbox:outbox,replayed:false},generationId:payload.generationId,computedBasisId:payload.computedBasisId,validatedSnapshotId:'snapshot-ui-f4',intentRevision:payload.intentRevision,freshness:'current'};}`
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
		"let currentSpec={workspaceId:'workspace-ui-f4'};",
		secureIDSource,
		pendingMatchSource,
		approvalErrorConstants,
		executionConstants,
		strings.Join(executionSources, "\n"),
		errorMessageSource,
		editedValueSource,
		editedLengthSource,
		admissionSource,
		admissionMessageSource,
		controlsSource,
		operationSource,
		setEvidenceSource,
		clearIdentitySource,
		beginRequestSource,
		approvalSource,
		"const elements=new Map();",
		"const calls=[];",
		"const deferred=[];",
		"let uuidCalls=0;",
		"let apiMode='success';",
		"Object.defineProperty(globalThis,'crypto',{configurable:true,writable:true,value:{randomUUID(){uuidCalls+=1;return 'uuid-'+String(uuidCalls);}}});",
		"function element(id){if(!elements.has(id))elements.set(id,{style:{},textContent:'',hidden:false,disabled:false,value:'',maxLength:4096});return elements.get(id);}",
		"globalThis.document={getElementById:id=>element(id)};",
		"function api(url,opts){const call={url:url,opts:opts||{}};call.payload=JSON.parse(call.opts.body);calls.push(call);if(apiMode==='transport')return Promise.reject(new Error('transport-secret'));if(apiMode==='conflict')return Promise.resolve({ok:false,json:async()=>({code:'approval_conflict',message:'raw-server-secret'})});if(apiMode==='deferred')return new Promise((resolve,reject)=>deferred.push({resolve:resolve,reject:reject,call:call}));return Promise.resolve({ok:true,json:async()=>completeResult(call.payload,calls.length)});}",
		approvalResponseSource,
		"function assert(condition,message){if(!condition)throw new Error(message);}",
		"function assertNoAuthority(payload){for(const key of ['actorId','sessionId','workspaceId','approver','actor','session','workspace'])assert(!(key in payload),'authority field leaked: '+key);}",
		"function assertStableMessage(){const text=element('approval-result-msg').textContent;assert(!text.includes('raw-server-secret')&&!text.includes('transport-secret'),'raw failure detail leaked: '+text);}",
		"function assertControls(state){const active=state==='active';const terminal=state==='rejected'||state==='revoked'||state==='superseded';assert(element('btn-semantic-approve').disabled===(!(!active&&!terminal)),'approve control state is incoherent for '+state);assert(element('btn-semantic-edit-then-approve').disabled!==active,'edit control state is incoherent for '+state);assert(element('btn-semantic-reject').disabled===terminal,'reject control state is incoherent for '+state);assert(element('btn-semantic-revoke').disabled!==active,'revoke control state is incoherent for '+state);assert(element('btn-semantic-supersede').disabled!==active,'supersede control state is incoherent for '+state);assert(element('approval-edited-text').disabled!==active,'edited text control state is incoherent for '+state);}",
		"function setState(state,version,active){semanticRequestGeneration+=1;viewState={...viewState,proposalId:'proposal-ui-f4',evidencePackId:'pack-ui-f4',computedBasisId:'basis-ui-f4',generationId:'generation-ui-f4',validatedAgainstSnapshotId:'snapshot-ui-f4',intentRevision:9,approvalVersion:version,approvalState:state,approvalExpectedVersion:version,approvalExpectedState:state,activeApprovalId:active||'',approvalPendingRequest:null,approvalCommandId:'',approvalIdempotencyKey:'',approvalDecision:'',approvalPredecessorApprovalId:''};element('approval-edited-text').value='';}",
		"async function submitAndAssert(decision,state,version,active,edited,wantPredecessor,wantEdited){setState(state,version,active);element('approval-edited-text').value=edited||'';apiMode='success';const before=calls.length;const promise=submitProposalApproval(decision);assert(calls.length===before+1,decision+' did not submit exactly one valid request');const call=calls[calls.length-1];const payload=call.payload;assert(payload.decision===decision,'wrong decision payload for '+decision);assertNoAuthority(payload);assert((('predecessorApprovalId' in payload)===wantPredecessor),'predecessor presence mismatch for '+decision+': '+JSON.stringify(payload));assert((('editedText' in payload)===wantEdited),'editedText presence mismatch for '+decision+': '+JSON.stringify(payload));if(wantPredecessor)assert(payload.predecessorApprovalId===active,'wrong predecessor for '+decision);if(wantEdited)assert(payload.editedText===edited.trim(),'wrong edited text for '+decision);assert(viewState.approvalPendingRequest&&Object.isFrozen(viewState.approvalPendingRequest),'valid request did not retain frozen pending identity');await promise;assert(viewState.approvalPendingRequest===null,'success did not clear pending for '+decision);assertControls(viewState.approvalState);return payload;}",
		"async function runValidMatrix(){const seen=new Set();for(const args of [['approve','none',0,'','',false,false],['edit_then_approve','active',1,'approval-existing','edited semantic meaning',true,true],['reject','none',0,'','',false,false],['reject','active',3,'approval-reject','',true,false],['revoke','active',4,'approval-revoke','',true,false],['supersede','active',5,'approval-supersede','',true,false]]){const payload=await submitAndAssert(...args);assert(!seen.has(payload.commandId)&&!seen.has(payload.idempotencyKey),'intentional success reused an ID');seen.add(payload.commandId);seen.add(payload.idempotencyKey);}}",
		"async function runInvalidAdmission(){setState('active',1,'approval-active');element('approval-edited-text').value='';const before=calls.length;await submitProposalApproval('approve');assert(calls.length===before,'approve was sent from active state');assert(element('approval-result-msg').textContent.length>0,'missing local approval validation message');setState('none',0,'approval-unexpected');const beforePredecessor=calls.length;await submitProposalApproval('approve');assert(calls.length===beforePredecessor,'approve was sent with a predecessor in none state');setState('none',0,'');const beforeRevoke=calls.length;await submitProposalApproval('revoke');assert(calls.length===beforeRevoke,'revoke was sent from none state');setState('active',1,'approval-edit');const beforeEmpty=calls.length;await submitProposalApproval('edit_then_approve');assert(calls.length===beforeEmpty,'empty edit text was sent');element('approval-edited-text').value='x'.repeat(4097);const beforeLong=calls.length;await submitProposalApproval('edit_then_approve');assert(calls.length===beforeLong,'oversize edit text was sent');for(const terminal of ['rejected','revoked','superseded']){setState(terminal,2,'');const beforeTerminal=calls.length;await submitProposalApproval('approve');assert(calls.length===beforeTerminal,'approve was sent from terminal state '+terminal);}}",
		"async function runRetryAndEditedIdentity(){setState('none',0,'');element('approval-edited-text').value='ignored';const offset=calls.length;apiMode='transport';const firstPromise=submitProposalApproval('approve');assert(calls.length===offset+1,'transport first request missing');const first=calls[offset];await firstPromise;assertStableMessage();assert(viewState.approvalPendingRequest&&Object.isFrozen(viewState.approvalPendingRequest),'transport rejection lost pending request');apiMode='success';const retryPromise=submitProposalApproval('approve');assert(calls.length===offset+2&&calls[offset+1].opts.body===first.opts.body,'transport retry changed exact body');assert(calls[offset+1].payload.commandId===first.payload.commandId&&calls[offset+1].payload.idempotencyKey===first.payload.idempotencyKey,'transport retry changed IDs');await retryPromise;assert(viewState.approvalPendingRequest===null,'successful retry did not clear pending');setState('active',1,'approval-edit');element('approval-edited-text').value='first edit';apiMode='conflict';await submitProposalApproval('edit_then_approve');const editFirst=calls[calls.length-1];assert(('editedText' in editFirst.payload)&&editFirst.payload.editedText==='first edit','edit payload missing first edited text');assert(element('approval-result-msg').textContent==='approval_conflict: approval request conflicts with current state','non-2xx response was not bounded');element('approval-edited-text').value='second edit';await submitProposalApproval('edit_then_approve');const editSecond=calls[calls.length-1];assert(editSecond.payload.editedText==='second edit'&&editSecond.payload.commandId!==editFirst.payload.commandId&&editSecond.payload.idempotencyKey!==editFirst.payload.idempotencyKey,'edited input did not create a fresh request identity');assertStableMessage();}",
		"async function runStaleRace(){apiMode='deferred';const offset=calls.length;setState('none',0,'');element('approval-result-msg').textContent='B pending';element('approval-status-badge').textContent='B badge';const a=submitProposalApproval('approve');assert(calls.length===offset+1,'A stale request setup failed');const aCall=calls[offset];beginSemanticRequest();setState('none',0,'');element('approval-result-msg').textContent='B pending';element('approval-status-badge').textContent='B badge';const b=submitProposalApproval('approve');assert(calls.length===offset+2,'B stale request setup failed');const bPending=viewState.approvalPendingRequest;const bCall=calls[offset+1];deferred[0].resolve({ok:true,status:200,json:async()=>completeResult(aCall.payload,1)});await a;assert(viewState.approvalPendingRequest===bPending&&element('approval-result-msg').textContent==='B pending'&&element('approval-status-badge').textContent==='B badge','stale A completion overwrote B');deferred[1].resolve({ok:true,status:200,json:async()=>completeResult(bCall.payload,2)});await b;assert(viewState.approvalPendingRequest===null&&viewState.approvalState==='active','current B completion did not apply');}",
		"async function run(){await runValidMatrix();await runInvalidAdmission();await runRetryAndEditedIdentity();await runStaleRace();}",
		"run().catch(error=>{console.error(error.stack||error);process.exitCode=1;});",
	}, "\n")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, nodePath, "--input-type=commonjs")
	cmd.Stdin = strings.NewReader(script)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("controlled embedded approval decision-matrix harness failed: %v\n%s", err, output)
	}
}
