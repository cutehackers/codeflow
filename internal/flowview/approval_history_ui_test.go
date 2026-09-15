package flowview

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestApprovalHistoryUIHydratesDurableAggregateThroughEmbeddedFlowView(t *testing.T) {
	nodePath, err := exec.LookPath("node")
	if err != nil {
		t.Skipf("node is required for the controlled approval-history harness: %v", err)
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
	parts := []string{"'use strict';", stateSource, "let semanticRequestGeneration=1;", "let currentSpec=null,selected=0,currentFlowId='';", "const ENRICHMENT_STATUSES=['not_requested','pending','available','timed_out','unavailable'];", "const APPROVAL_HISTORY_SCHEMA_ID='https://codeflow.local/schemas/rflsc.approval-history.v1.schema.json';const APPROVAL_HISTORY_SCHEMA_VERSION=1;const APPROVAL_EVENT_SCHEMA_ID='https://codeflow.local/schemas/rflsc.approval-event.v2.schema.json';const APPROVAL_AGGREGATE_SCHEMA_ID='https://codeflow.local/schemas/rflsc.approval-aggregate.v2.schema.json';const APPROVAL_HISTORY_STATES=['none','active','rejected','revoked','superseded'];const APPROVAL_HISTORY_DECISIONS=['approve','edit_then_approve','reject','revoke','supersede'];const APPROVAL_HISTORY_RELATIONS=['initial','edit','reject','revoke','supersede'];const APPROVAL_HISTORY_MAX_RECORDS=256;let approvalHistoryModel=null;let approvalHistoryLoadSequence=0;let approvalHistoryMutationSequence=0;"}
	for _, name := range []string{
		"enrichmentEnvelope(data)",
		"enrichmentDisplayValue(value,fallback)",
		"isDisplayOnlyInferredProposal(proposal)",
		"setSemanticEvidencePackIdentity(packID)",
		"structuralIdentityOf(st)",
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
		"setApprovalHistoryFailure()",
		"applyApprovalHistoryPayload(payload)",
		"loadApprovalHistory(expectedAggregateId='')",
		"renderSemanticEnrichment(data)",
		"updateViewStateGeneration(data)",
		"renderSemanticTaskView(data,preserveSelection=false)",
	} {
		source := functionSource(name)
		if source == "" {
			t.Fatalf("embedded function %q not found", name)
		}
		parts = append(parts, source)
	}

	script := strings.Join(append(parts,
		"const elements=new Map();",
		"const calls=[];",
		"function element(id){if(!elements.has(id)){const node={id:id,style:{},dataset:{},_text:'',hidden:false,children:[],setAttribute(){},appendChild(child){this.children.push(child);}};Object.defineProperty(node,'textContent',{get(){return this._text;},set(value){this._text=String(value);if(id==='approval-history-events')this.children=[];}});elements.set(id,node);}return elements.get(id);}",
		"globalThis.document={getElementById:id=>element(id),createElement:tag=>({tagName:tag,textContent:'',children:[],setAttribute(){},appendChild(child){this.children.push(child);}})};",
		"function renderAll(){};function renderRequirementAlignment(){};function syncApprovalControls(){};function setViewStateStatus(){};function hideVerifiedGap(){};function showVerifiedGap(){};function hideTaskError(){};function loadAnalyses(){};",
		"function api(url,opts){calls.push({url:url,opts:opts||{}});return Promise.resolve({ok:true,status:200,json:async()=>({schemaId:'https://codeflow.local/schemas/rflsc.approval-history.v1.schema.json',schemaVersion:1,target:{workspaceId:'workspace-ui-h1',proposalId:'proposal-ui-h1',evidencePackId:'pack-ui-h1',computedBasisId:'basis-ui-h1',generationId:'generation-ui-h1',intentRevision:7,validatedSnapshotId:'snapshot-ui-h1',workspaceEpoch:1,mapId:'map-ui-h1',taskId:'task-ui-h1'},events:[{schemaId:'https://codeflow.local/schemas/rflsc.approval-event.v2.schema.json',schemaVersion:2,eventId:'event-ui-h1',approvalId:'approval-ui-h1',aggregateId:'aggregate-ui-h1',aggregateVersion:1,actorId:'actor-ui-h1',sessionId:'session-ui-h1',workspaceId:'workspace-ui-h1',proposalId:'proposal-ui-h1',evidencePackId:'pack-ui-h1',computedBasisId:'basis-ui-h1',generationId:'generation-ui-h1',intentRevision:7,decision:'approve',approvedText:'safe approved text',timestamp:'2026-09-07T00:00:00Z',lifecycleRelation:'initial'}],aggregate:{schemaId:'https://codeflow.local/schemas/rflsc.approval-aggregate.v2.schema.json',schemaVersion:2,aggregateId:'aggregate-ui-h1',workspaceId:'workspace-ui-h1',proposalId:'proposal-ui-h1',evidencePackId:'pack-ui-h1',computedBasisId:'basis-ui-h1',generationId:'generation-ui-h1',intentRevision:7,version:1,state:'active',lastEventId:'event-ui-h1',lastDecision:'approve',activeApprovalId:'approval-ui-h1',history:[{version:0,state:'none',eventId:'genesis-ui-h1',decision:'none'},{version:1,state:'active',eventId:'event-ui-h1',approvalId:'approval-ui-h1',decision:'approve'}]},freshness:'current'})});}",
		"function assert(condition,message){if(!condition)throw new Error(message);}",
		"async function run(){",
		"const data={workspaceId:'workspace-ui-h1',candidateAnswer:{requested:'show approval',candidate:'approval history'},taskIntent:{revision:7,request:{rawRequest:'show approval'}},semanticMap:{mapId:'map-ui-h1',generationId:'generation-ui-h1',computedBasisId:'basis-ui-h1',validatedAgainstSnapshotId:'snapshot-ui-h1',freshness:'current',summary:{requested:'show approval',current:'approval history'},basis:{dependencyFingerprint:'dependency-ui-h1'},quality:{stage:'Q3'},settlement:'passed',task:{intentRevision:7},steps:[{stepId:'step-ui-h1',ordinal:1,name:'approval',technicalName:'Approval',structuralIdentity:'approval|history'}]},enrichment:{status:'available',proposal:{proposalId:'proposal-ui-h1'},pack:{evidencePackId:'pack-ui-h1'}}};",
		"renderSemanticTaskView(data,false);await Promise.resolve();await Promise.resolve();await Promise.resolve();await Promise.resolve();",
		"assert(calls.length===1,'task-view render did not request durable approval history');",
		"const request=calls[0];const url=new URL(request.url,'http://flowview.test');",
		"assert(url.pathname==='/api/semantic/approval-history','history endpoint path was not used: '+request.url);assert(url.searchParams.get('proposalId')==='proposal-ui-h1'&&url.searchParams.get('evidencePackId')==='pack-ui-h1','history query identity was not bound to the rendered task');assert(request.opts.allowErrors===true,'history lookup did not allow bounded errors');",
		"assert(viewState.approvalState==='active'&&viewState.approvalVersion===1&&viewState.activeApprovalId==='approval-ui-h1','durable aggregate was not restored into view state');assert(element('approval-history-state').textContent==='active'&&element('approval-history-version').textContent==='1'&&element('approval-history-freshness').textContent==='current','durable aggregate facts were not rendered');assert(element('approval-history-events').children.length===1&&element('approval-history-events').children[0].textContent==='v1 · Approved','ordered lifecycle event was not rendered through textContent');assert(!element('approval-history-events').children[0].innerHTML,'history event rendering must not use innerHTML');",
		"}",
		"run().catch(error=>{console.error(error.stack||error);process.exitCode=1;});",
	), "\n")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, nodePath, "--input-type=commonjs")
	cmd.Stdin = strings.NewReader(script)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("controlled embedded approval-history harness failed: %v\n%s", err, output)
	}
}

func TestApprovalHistoryUIRejectsStaleAndUntrustedDurableHistory(t *testing.T) {
	nodePath, err := exec.LookPath("node")
	if err != nil {
		t.Skipf("node is required for the controlled approval-history harness: %v", err)
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
	parts := []string{
		"'use strict';", stateSource,
		"let semanticRequestGeneration=1;let currentSpec=null,selected=0,currentFlowId='';",
		"const ENRICHMENT_STATUSES=['not_requested','pending','available','timed_out','unavailable'];",
		"const APPROVAL_HISTORY_SCHEMA_ID='https://codeflow.local/schemas/rflsc.approval-history.v1.schema.json';const APPROVAL_HISTORY_SCHEMA_VERSION=1;const APPROVAL_EVENT_SCHEMA_ID='https://codeflow.local/schemas/rflsc.approval-event.v2.schema.json';const APPROVAL_AGGREGATE_SCHEMA_ID='https://codeflow.local/schemas/rflsc.approval-aggregate.v2.schema.json';const APPROVAL_HISTORY_STATES=['none','active','rejected','revoked','superseded'];const APPROVAL_HISTORY_DECISIONS=['approve','edit_then_approve','reject','revoke','supersede'];const APPROVAL_HISTORY_RELATIONS=['initial','edit','reject','revoke','supersede'];const APPROVAL_HISTORY_MAX_RECORDS=256;let approvalHistoryModel=null;let approvalHistoryLoadSequence=0;let approvalHistoryMutationSequence=0;",
	}
	for _, name := range []string{
		"enrichmentEnvelope(data)", "enrichmentDisplayValue(value,fallback)", "isDisplayOnlyInferredProposal(proposal)",
		"setSemanticEvidencePackIdentity(packID)", "clearSemanticEnrichmentIdentity()", "beginSemanticRequest()",
		"structuralIdentityOf(st)", "approvalHistoryExactKeys(value,required,optional)", "approvalHistoryValidID(value,allowEmpty=false,max=256)",
		"approvalHistoryValidInteger(value,min,max)", "approvalHistoryIdentity()", "approvalHistoryIdentityIsCurrent(identity)",
		"approvalHistoryValidText(value,max)", "approvalHistoryValidTimestamp(value)", "approvalHistoryTransition(state,active,event)", "validateApprovalHistoryPayload(payload,identity)",
		"approvalHistoryStateLabel(state)", "approvalHistoryDecisionLabel(decision)", "renderApprovalHistoryDisplay(events,aggregate,freshness)",
		"clearApprovalHistoryUI(freshness)", "setApprovalHistoryFailure()", "applyApprovalHistoryPayload(payload)",
		"loadApprovalHistory(expectedAggregateId='')", "appendApprovalHistoryProjection(payload)", "appendApprovalHistoryReceipt(result)", "renderSemanticEnrichment(data)",
		"updateViewStateGeneration(data)", "renderSemanticTaskView(data,preserveSelection=false)",
	} {
		source := functionSource(name)
		if source == "" {
			t.Fatalf("embedded function %q not found", name)
		}
		parts = append(parts, source)
	}

	script := strings.Join(append(parts,
		"const elements=new Map();const calls=[];let syncCalls=0;",
		"function element(id){if(!elements.has(id)){const node={id:id,style:{},dataset:{},_text:'',hidden:false,children:[],setAttribute(){},appendChild(child){this.children.push(child);}};Object.defineProperty(node,'textContent',{get(){return this._text;},set(value){this._text=String(value);if(id==='approval-history-events')this.children=[];}});elements.set(id,node);}return elements.get(id);}",
		"globalThis.document={getElementById:id=>element(id),createElement:tag=>({tagName:tag,textContent:'',children:[],setAttribute(){},appendChild(child){this.children.push(child);}})};",
		"function renderAll(){};function renderRequirementAlignment(){};function syncApprovalControls(){syncCalls+=1;};function setViewStateStatus(){};function hideVerifiedGap(){};function showVerifiedGap(){};function renderChangePulse(){};function hideTaskError(){};function loadAnalyses(){};",
		"const responseQueue=[];function api(url,opts){calls.push({url:url,opts:opts||{}});if(!responseQueue.length)throw new Error('unexpected history request');return Promise.resolve(responseQueue.shift());}",
		"function ok(payload){return {ok:true,status:200,json:async()=>payload};}function unavailable(){return {ok:false,status:404,json:async()=>({code:'approval_unavailable',message:'SECRET_PATH_TOKEN'} )};}function genericError(){return {ok:false,status:500,json:async()=>({code:'approval_invalid',message:'SECRET_PATH_TOKEN'})};}",
		"function payloadFor(proposal,decisions,freshness,canary){const target={workspaceId:'workspace-'+proposal,proposalId:proposal,evidencePackId:'pack-'+proposal,computedBasisId:'basis-'+proposal,generationId:'generation-'+proposal,intentRevision:7,validatedSnapshotId:'snapshot-'+proposal,workspaceEpoch:1,mapId:'map-'+proposal,taskId:'task-'+proposal};let state='none',active='',events=[],history=[{version:0,state:'none',eventId:'genesis-'+proposal,decision:'none'}];for(let i=0;i<decisions.length;i+=1){const decision=decisions[i],approvalId='approval-'+proposal+'-'+String(i+1),eventId='event-'+proposal+'-'+String(i+1),predecessor=active;const event={schemaId:APPROVAL_EVENT_SCHEMA_ID,schemaVersion:2,eventId:eventId,approvalId:approvalId,aggregateId:'aggregate-'+proposal,aggregateVersion:i+1,actorId:canary||'actor-'+proposal,sessionId:canary||'session-'+proposal,workspaceId:proposal.startsWith('workspace-')?proposal:'workspace-'+proposal,proposalId:proposal,evidencePackId:'pack-'+proposal,computedBasisId:'basis-'+proposal,generationId:'generation-'+proposal,intentRevision:7,decision:decision,approvedText:(decision==='approve'||decision==='edit_then_approve')?(canary||'approved text'):'',timestamp:'2026-09-07T00:00:0'+String(i)+'Z',lifecycleRelation:decision==='approve'?'initial':(decision==='edit_then_approve'?'edit':decision)};if(decision==='edit_then_approve'||decision==='revoke'||decision==='supersede'||(decision==='reject'&&state==='active'))event.predecessorApprovalId=predecessor;if(decision==='approve'||decision==='edit_then_approve'){state='active';active=approvalId;}else if(decision==='reject'){state='rejected';active='';}else if(decision==='revoke'){state='revoked';active='';}else if(decision==='supersede'){state='superseded';active='';}events.push(event);history.push({version:i+1,state:state,eventId:eventId,approvalId:approvalId,decision:decision});}const aggregate={schemaId:APPROVAL_AGGREGATE_SCHEMA_ID,schemaVersion:2,aggregateId:'aggregate-'+proposal,workspaceId:'workspace-'+proposal,proposalId:proposal,evidencePackId:'pack-'+proposal,computedBasisId:'basis-'+proposal,generationId:'generation-'+proposal,intentRevision:7,version:events.length,state:state,lastEventId:events[events.length-1].eventId,lastDecision:events[events.length-1].decision,history:history};if(active)aggregate.activeApprovalId=active;return {schemaId:APPROVAL_HISTORY_SCHEMA_ID,schemaVersion:1,target:target,events:events,aggregate:aggregate,freshness:freshness||'current'};}",
		"function dataFor(proposal){return {workspaceId:'workspace-'+proposal,candidateAnswer:{requested:'show approval',candidate:'approval history'},taskIntent:{revision:7,request:{rawRequest:'show approval'}},semanticMap:{mapId:'map-'+proposal,generationId:'generation-'+proposal,computedBasisId:'basis-'+proposal,validatedAgainstSnapshotId:'snapshot-'+proposal,freshness:'current',summary:{requested:'show approval',current:'approval history'},basis:{dependencyFingerprint:'dependency-'+proposal},quality:{stage:'Q3'},settlement:'passed',task:{intentRevision:7},steps:[{stepId:'step-'+proposal,ordinal:1,name:'approval',technicalName:'Approval',structuralIdentity:'approval|history'}]},enrichment:{status:'available',proposal:{proposalId:proposal},pack:{evidencePackId:'pack-'+proposal}}};}",
		"function assert(condition,message){if(!condition)throw new Error(message);}function flush(){return Promise.resolve().then(()=>Promise.resolve()).then(()=>Promise.resolve()).then(()=>Promise.resolve()).then(()=>Promise.resolve()).then(()=>Promise.resolve());}function listText(){return element('approval-history-events').children.map(child=>child.textContent).join('|');}function visibleText(){return Array.from(elements.values()).map(value=>value.textContent||'').join('|');}",
		"async function run(){",
		"responseQueue.push(ok(payloadFor('proposal-a',['approve'])));renderSemanticTaskView(dataFor('proposal-a'),false);await flush();assert(viewState.approvalState==='active'&&viewState.approvalVersion===1,'baseline history did not hydrate');",
		"let resolveFetchA;const fetchA=new Promise(resolve=>{resolveFetchA=resolve;});beginSemanticRequest();responseQueue.push(fetchA);renderSemanticTaskView(dataFor('proposal-a'),false);let resolveFetchB;const fetchB=new Promise(resolve=>{resolveFetchB=resolve;});beginSemanticRequest();responseQueue.push(fetchB);renderSemanticTaskView(dataFor('proposal-b'),false);resolveFetchA(ok(payloadFor('proposal-a',['reject'])));await flush();assert(viewState.proposalId==='proposal-b'&&viewState.approvalState==='none'&&listText()==='','stale fetch completion changed the newer identity: '+viewState.proposalId+'/'+viewState.approvalState+'/'+listText());resolveFetchB(ok(payloadFor('proposal-b',['approve'])));await flush();assert(viewState.proposalId==='proposal-b'&&viewState.approvalState==='active'&&listText()==='v1 · Approved','newer fetch did not hydrate');",
		"let resolveJSON;const delayedJSON=new Promise(resolve=>{resolveJSON=resolve;});beginSemanticRequest();responseQueue.push({ok:true,status:200,json:()=>delayedJSON});renderSemanticTaskView(dataFor('proposal-c'),false);await flush();beginSemanticRequest();responseQueue.push(ok(payloadFor('proposal-d',['reject'])));renderSemanticTaskView(dataFor('proposal-d'),false);resolveJSON(payloadFor('proposal-c',['approve']));await flush();assert(viewState.proposalId==='proposal-d'&&viewState.approvalState==='rejected','stale JSON completion changed the newer identity');assert(listText()==='v1 · Rejected','newer JSON result was not retained');",
		"beginSemanticRequest();const pendingCanary=Object.freeze({proposalId:'proposal-error',evidencePackId:'pack-proposal-error'});viewState={...viewState,approvalPendingRequest:pendingCanary};responseQueue.push(genericError());renderSemanticTaskView(dataFor('proposal-error'),false);await flush();assert(element('approval-history-status').textContent==='Unavailable'&&element('approval-history-error').textContent==='Approval history could not be loaded.','generic history error was not bounded');assert(viewState.approvalPendingRequest===pendingCanary,'bounded history error altered pending mutation identity');assert(!visibleText().includes('SECRET_PATH_TOKEN'),'history error leaked server details');",
		"beginSemanticRequest();responseQueue.push({ok:true,status:200,json:async()=>{throw new Error('SECRET_PATH_TOKEN')}});renderSemanticTaskView(dataFor('proposal-malformed'),false);await flush();assert(element('approval-history-status').textContent==='Unavailable'&&element('approval-history-error').textContent==='Approval history could not be loaded.','malformed history JSON did not use bounded failure');assert(!visibleText().includes('SECRET_PATH_TOKEN'),'malformed history error leaked details');",
		"const invalid=payloadFor('proposal-invalid',['approve']);invalid.aggregate.state='superseded';beginSemanticRequest();responseQueue.push(ok(invalid));renderSemanticTaskView(dataFor('proposal-invalid'),false);await flush();assert(viewState.approvalState==='none'&&viewState.approvalVersion===0,'invalid history applied attacker lifecycle state');assert(element('approval-history-error').textContent==='Approval history could not be loaded.','invalid history did not use bounded failure');const identityMismatch=payloadFor('proposal-identity',['approve']);identityMismatch.target.proposalId='attacker-proposal';beginSemanticRequest();responseQueue.push(ok(identityMismatch));renderSemanticTaskView(dataFor('proposal-identity'),false);await flush();assert(viewState.approvalState==='none'&&element('approval-history-error').textContent==='Approval history could not be loaded.','identity-mismatched history was applied');",
		"beginSemanticRequest();responseQueue.push(unavailable());renderSemanticTaskView(dataFor('proposal-missing'),false);await flush();assert(viewState.approvalState==='none'&&viewState.approvalVersion===0&&viewState.activeApprovalId===''&&listText()==='','approval_unavailable did not produce empty no-history state');assert(element('approval-history-status').textContent==='No durable approval history'&&syncCalls>0,'no-history controls/status were not synchronized');",
		"beginSemanticRequest();responseQueue.push(ok(payloadFor('proposal-historical',['approve'],'historical')));renderSemanticTaskView(dataFor('proposal-historical'),false);await flush();assert(element('approval-history-freshness').textContent==='historical'&&viewState.approvalState==='active','historical durable freshness was not displayed');",
		"for(const item of [['proposal-active',['approve'],'active'],['proposal-rejected',['reject'],'rejected'],['proposal-rejected-active',['approve','reject'],'rejected'],['proposal-revoked',['approve','revoke'],'revoked'],['proposal-superseded',['approve','supersede'],'superseded']]){beginSemanticRequest();const durablePayload=payloadFor(item[0],item[1]);responseQueue.push(ok(durablePayload));renderSemanticTaskView(dataFor(item[0]),false);await flush();assert(viewState.approvalState===item[2]&&viewState.approvalVersion===item[1].length&&viewState.approvalExpectedState===item[2]&&viewState.approvalExpectedVersion===item[1].length,'terminal aggregate was not restored: '+item[0]);const expectedList=item[1].map((decision,index)=>'v'+String(index+1)+' · '+(decision==='approve'?'Approved':(decision==='reject'?'Rejected':(decision==='revoke'?'Revoked':'Superseded')))).join('|');assert(listText()===expectedList,'terminal lifecycle order was not rendered: '+item[0]);const beforeRestart=JSON.stringify({model:approvalHistoryModel,viewState:{state:viewState.approvalState,version:viewState.approvalVersion,expectedState:viewState.approvalExpectedState,expectedVersion:viewState.approvalExpectedVersion,active:viewState.activeApprovalId},events:listText()});beginSemanticRequest();responseQueue.push(ok(durablePayload));renderSemanticTaskView(dataFor(item[0]),false);await flush();const afterRestart=JSON.stringify({model:approvalHistoryModel,viewState:{state:viewState.approvalState,version:viewState.approvalVersion,expectedState:viewState.approvalExpectedState,expectedVersion:viewState.approvalExpectedVersion,active:viewState.activeApprovalId},events:listText()});assert(afterRestart===beforeRestart,'restart hydration was not value-equivalent: '+item[0]);}",
		"const canary='<img src=x onerror=alert(1)> actor SECRET_PATH_TOKEN';beginSemanticRequest();responseQueue.push(ok(payloadFor('proposal-inject',['approve'], 'current', canary)));renderSemanticTaskView(dataFor('proposal-inject'),false);await flush();assert(listText()==='v1 · Approved'&&!visibleText().includes(canary),'history actor/text canary was rendered');assert(!element('approval-history-events').children[0].innerHTML,'history list must use textContent only');",
		"const callCountBeforeReceipt=calls.length;const receipt=payloadFor('proposal-inject',['approve','edit_then_approve']);assert(appendApprovalHistoryProjection(receipt),'validated projection was rejected');assert(calls.length===callCountBeforeReceipt&&viewState.approvalState==='active'&&viewState.approvalVersion===2&&listText()==='v1 · Approved|v2 · Edited and approved','receipt did not update history without a reload: '+viewState.approvalState+'/'+viewState.approvalVersion+'/'+listText());",
		"let resolveMutationFetch;const mutationFetch=new Promise(resolve=>{resolveMutationFetch=resolve;});beginSemanticRequest();responseQueue.push(mutationFetch);renderSemanticTaskView(dataFor('proposal-mutation'),false);const mutationReceipt=payloadFor('proposal-mutation',['approve']);assert(appendApprovalHistoryProjection(mutationReceipt),'validated mutation projection was rejected');resolveMutationFetch(ok(payloadFor('proposal-mutation',['reject'])));await flush();assert(viewState.approvalState==='active'&&viewState.approvalVersion===1&&listText()==='v1 · Approved','old history load overwrote committed receipt');",
		"}run().catch(error=>{console.error(error.stack||error);process.exitCode=1;});",
	), "\n")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, nodePath, "--input-type=commonjs")
	cmd.Stdin = strings.NewReader(script)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("controlled embedded stale/untrusted approval-history harness failed: %v\n%s", err, output)
	}
}

func TestApprovalHistoryUIHasAccessibleDurableHistoryMarkers(t *testing.T) {
	for _, marker := range []string{
		`id="approval-history-panel"`,
		`id="approval-history-summary"`,
		`id="approval-history-status" role="status"`,
		`aria-live="polite"`,
		`id="approval-history-events" aria-label="Ordered approval lifecycle events"`,
		`id="approval-history-error" role="alert"`,
	} {
		if !strings.Contains(IndexHTML, marker) {
			t.Fatalf("embedded approval-history accessibility marker %q is missing", marker)
		}
	}
	if strings.Contains(IndexHTML, "approval-history-events').innerHTML") {
		t.Fatal("approval history event list must not use innerHTML")
	}
}
