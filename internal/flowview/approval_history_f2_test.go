package flowview

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestEmbeddedApprovalHistoryRejectsIdentityTimestampGenesisAndEpochRegressions(t *testing.T) {
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
		"let semanticRequestGeneration=1;let currentSpec={flowId:'map-f2',taskId:'task-f2',workspaceId:'workspace-f2',workspaceEpoch:1};",
		"const APPROVAL_HISTORY_SCHEMA_ID='https://codeflow.local/schemas/rflsc.approval-history.v1.schema.json';const APPROVAL_HISTORY_SCHEMA_VERSION=1;const APPROVAL_EVENT_SCHEMA_ID='https://codeflow.local/schemas/rflsc.approval-event.v2.schema.json';const APPROVAL_AGGREGATE_SCHEMA_ID='https://codeflow.local/schemas/rflsc.approval-aggregate.v2.schema.json';const APPROVAL_HISTORY_STATES=['none','active','rejected','revoked','superseded'];const APPROVAL_HISTORY_DECISIONS=['approve','edit_then_approve','reject','revoke','supersede'];const APPROVAL_HISTORY_RELATIONS=['initial','edit','reject','revoke','supersede'];const APPROVAL_HISTORY_MAX_RECORDS=256;let approvalHistoryModel=null;let approvalHistoryLoadSequence=0;let approvalHistoryMutationSequence=0;",
		"const APPROVAL_EXECUTION_SCHEMA_IDS={event:APPROVAL_EVENT_SCHEMA_ID,aggregate:APPROVAL_AGGREGATE_SCHEMA_ID,idempotency:'https://codeflow.local/schemas/rflsc.approval-idempotency-result.v1.schema.json',outbox:'https://codeflow.local/schemas/rflsc.approval-outbox.v1.schema.json'};",
	}
	for _, name := range []string{
		"approvalHistoryExactKeys(value,required,optional)",
		"approvalHistoryValidID(value,allowEmpty=false,max=256)",
		"approvalHistoryValidInteger(value,min,max)",
		"approvalHistoryIdentity()",
		"approvalHistoryIdentityIsCurrent(identity)",
		"approvalHistoryValidText(value,max)",
		"approvalHistoryTransition(state,active,event)",
		"validateApprovalHistoryPayload(payload,identity)",
		"approvalHistoryDecisionLabel(decision)",
		"approvalHistoryStateLabel(state)",
		"renderApprovalHistoryDisplay(events,aggregate,freshness)",
		"applyApprovalHistoryPayload(payload)",
		"appendApprovalHistoryProjection(payload)",
		"approvalExecutionExactKeys(value,required,optional)",
		"approvalExecutionValidText(value,max,required)",
		"approvalExecutionValidID(value)",
		"appendApprovalHistoryLocalReceipt(result)",
	} {
		source := functionSource(name)
		if source == "" {
			t.Fatalf("embedded function %q not found", name)
		}
		parts = append(parts, source)
	}
	timestampSource := functionSource("approvalHistoryValidTimestamp(value)")
	if timestampSource == "" {
		// Keep the RED harness executable against the permissive implementation.
		timestampSource = "function approvalHistoryValidTimestamp(value){return approvalHistoryValidText(value,128)&&value.trim()!=='';}"
	}
	parts = append(parts, timestampSource)

	basePayload := `function historyPayload(){const event1={schemaId:APPROVAL_EVENT_SCHEMA_ID,schemaVersion:2,eventId:'event-f2-1',approvalId:'approval-f2-1',aggregateId:'aggregate-f2',aggregateVersion:1,actorId:'actor-f2',sessionId:'session-f2',workspaceId:'workspace-f2',proposalId:'proposal-f2',evidencePackId:'pack-f2',computedBasisId:'basis-f2',generationId:'generation-f2',intentRevision:7,decision:'approve',approvedText:'approved text',timestamp:'2026-09-07T00:00:00Z',lifecycleRelation:'initial'},event2={schemaId:APPROVAL_EVENT_SCHEMA_ID,schemaVersion:2,eventId:'event-f2-2',approvalId:'approval-f2-2',aggregateId:'aggregate-f2',aggregateVersion:2,actorId:'actor-f2',sessionId:'session-f2',workspaceId:'workspace-f2',proposalId:'proposal-f2',evidencePackId:'pack-f2',computedBasisId:'basis-f2',generationId:'generation-f2',intentRevision:7,decision:'edit_then_approve',approvedText:'edited text',timestamp:'2026-09-07T00:00:01Z',lifecycleRelation:'edit',predecessorApprovalId:'approval-f2-1'},events=[event1,event2],aggregate={schemaId:APPROVAL_AGGREGATE_SCHEMA_ID,schemaVersion:2,aggregateId:'aggregate-f2',workspaceId:'workspace-f2',proposalId:'proposal-f2',evidencePackId:'pack-f2',computedBasisId:'basis-f2',generationId:'generation-f2',intentRevision:7,version:2,state:'active',lastEventId:'event-f2-2',lastDecision:'edit_then_approve',activeApprovalId:'approval-f2-2',history:[{version:0,state:'none',eventId:'genesis-f2',decision:'none'},{version:1,state:'active',eventId:'event-f2-1',approvalId:'approval-f2-1',decision:'approve'},{version:2,state:'active',eventId:'event-f2-2',approvalId:'approval-f2-2',decision:'edit_then_approve'}]};return {schemaId:APPROVAL_HISTORY_SCHEMA_ID,schemaVersion:1,target:{workspaceId:'workspace-f2',proposalId:'proposal-f2',evidencePackId:'pack-f2',computedBasisId:'basis-f2',generationId:'generation-f2',intentRevision:7,validatedSnapshotId:'snapshot-f2',workspaceEpoch:1,mapId:'map-f2',taskId:'task-f2'},events:events,aggregate:aggregate,freshness:'current'};}`

	script := strings.Join(append(parts,
		basePayload,
		"const elements=new Map();function element(id){if(!elements.has(id)){const node={id:id,style:{},dataset:{},_text:'',hidden:false,children:[],setAttribute(){},appendChild(child){this.children.push(child);}};Object.defineProperty(node,'textContent',{get(){return this._text;},set(value){this._text=String(value);if(id==='approval-history-events')this.children=[];}});elements.set(id,node);}return elements.get(id);}globalThis.document={getElementById:id=>element(id),createElement:tag=>({tagName:tag,textContent:'',children:[],setAttribute(){},appendChild(child){this.children.push(child);}})};function syncApprovalControls(){}",
		"function assert(condition,message){if(!condition)throw new Error(message);}",
		"function reset(){approvalHistoryModel=null;approvalHistoryLoadSequence=0;approvalHistoryMutationSequence=0;semanticRequestGeneration=1;currentSpec={flowId:'map-f2',taskId:'task-f2',workspaceId:'workspace-f2',workspaceEpoch:1};viewState={...viewState,proposalId:'proposal-f2',evidencePackId:'pack-f2',computedBasisId:'basis-f2',generationId:'generation-f2',validatedAgainstSnapshotId:'snapshot-f2',intentRevision:7,approvalVersion:0,approvalState:'none',approvalExpectedVersion:0,approvalExpectedState:'none',activeApprovalId:'',approvalPendingRequest:null};}",
		"function assertRejected(label,mutate){reset();const payload=historyPayload();mutate(payload);const before=JSON.stringify({model:approvalHistoryModel,version:viewState.approvalVersion,state:viewState.approvalState,mutation:approvalHistoryMutationSequence});assert(!appendApprovalHistoryProjection(payload),label+' payload was accepted');assert(JSON.stringify({model:approvalHistoryModel,version:viewState.approvalVersion,state:viewState.approvalState,mutation:approvalHistoryMutationSequence})===before,label+' mutated history/lifecycle state');}",
		"function localReceipt(){const payload=historyPayload();payload.events=payload.events.slice(0,1);payload.aggregate.version=1;payload.aggregate.state='active';payload.aggregate.lastEventId=payload.events[0].eventId;payload.aggregate.lastDecision=payload.events[0].decision;payload.aggregate.activeApprovalId=payload.events[0].approvalId;payload.aggregate.history=payload.aggregate.history.slice(0,2);return {receipt:{event:payload.events[0],aggregate:payload.aggregate},validatedSnapshotId:'snapshot-f2',freshness:'current'};}",
		"function assertLocalRejected(label,epoch){reset();currentSpec.workspaceEpoch=epoch;const before=JSON.stringify({model:approvalHistoryModel,version:viewState.approvalVersion,state:viewState.approvalState,mutation:approvalHistoryMutationSequence});assert(!appendApprovalHistoryLocalReceipt(localReceipt()),label+' local receipt was accepted');assert(JSON.stringify({model:approvalHistoryModel,version:viewState.approvalVersion,state:viewState.approvalState,mutation:approvalHistoryMutationSequence})===before,label+' local receipt mutated history/lifecycle state');}",
		"async function run(){",
		"assertRejected('event aggregate identity',payload=>{payload.events[1].aggregateId='aggregate-other';});",
		"assertRejected('noncanonical timestamp',payload=>{payload.events[0].timestamp='2026-09-07T00:00:00.000Z';});",
		"for(const item of [['non-leap February 29','2026-02-29T00:00:00Z'],['February 30','2026-02-30T00:00:00Z'],['positive offset','2026-09-07T00:00:00+00:00'],['negative offset','2026-09-07T00:00:00-05:00'],['empty fraction','2026-09-07T00:00:00.Z'],['ten-digit fraction','2026-09-07T00:00:00.1234567890Z'],['trailing zero fraction','2026-09-07T00:00:00.10Z'],['trailing zero nanofraction','2026-09-07T00:00:00.123456780Z']])assertRejected(item[0]+' timestamp',payload=>{payload.events[0].timestamp=item[1];});",
		"for(let width=1;width<=9;width+=1){const fraction='123456789'.slice(0,width),timestamp='2026-09-07T00:00:00.'+fraction+'Z';reset();const payload=historyPayload();payload.events[0].timestamp=timestamp;assert(appendApprovalHistoryProjection(payload),'canonical '+String(width)+'-digit timestamp was rejected');assert(approvalHistoryModel&&approvalHistoryModel.events[0].timestamp===timestamp,'canonical '+String(width)+'-digit timestamp was not preserved exactly');}",
		"assertRejected('genesis event identity',payload=>{payload.aggregate.history[0].eventId=payload.events[0].eventId;});",
		"for(const item of [['negative',-1],['unsafe',Number.MAX_SAFE_INTEGER+1],['fractional',1.5]])assertRejected(''+item[0]+' workspace epoch',payload=>{payload.target.workspaceEpoch=item[1];});",
		"reset();const valid=historyPayload();valid.target.workspaceEpoch=Number.MAX_SAFE_INTEGER;assert(appendApprovalHistoryProjection(valid),'safe high workspace epoch was rejected');assert(approvalHistoryModel&&approvalHistoryModel.target.workspaceEpoch===Number.MAX_SAFE_INTEGER,'safe high workspace epoch was not applied');assert(viewState.approvalVersion===2&&viewState.approvalState==='active','valid high epoch changed lifecycle projection incorrectly');",
		"assertLocalRejected('local negative',-1);assertLocalRejected('local fractional',1.5);assertLocalRejected('local unsafe',Number.MAX_SAFE_INTEGER+1);reset();currentSpec.workspaceEpoch=Number.MAX_SAFE_INTEGER;assert(appendApprovalHistoryLocalReceipt(localReceipt()),'safe high local workspace epoch was rejected');assert(approvalHistoryModel&&approvalHistoryModel.target.workspaceEpoch===Number.MAX_SAFE_INTEGER,'safe high local workspace epoch was not preserved');",
		"}",
		"run().catch(error=>{console.error(error.stack||error);process.exitCode=1;});",
	), "\n")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, nodePath, "--input-type=commonjs")
	cmd.Stdin = strings.NewReader(script)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("controlled approval-history F2 harness failed: %v\n%s", err, output)
	}
}
