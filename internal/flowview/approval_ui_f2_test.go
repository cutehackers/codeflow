package flowview

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestEmbeddedSemanticApprovalBoundsErrorResponses(t *testing.T) {
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
	approvalErrorConstants := ""
	if !strings.Contains(pendingMatchSource, "const APPROVAL_ERROR_MESSAGES") && !strings.Contains(errorMessageSource, "const APPROVAL_ERROR_MESSAGES") {
		approvalErrorConstants = "const APPROVAL_ERROR_MESSAGES=Object.freeze({approval_invalid:'approval request is invalid',approval_conflict:'approval request conflicts with current state',approval_unavailable:'approval service is unavailable',approval_unauthenticated:'approval authentication is required',approval_unauthorized:'approval workspace is not authorized'});"
	}

	script := strings.Join([]string{
		"'use strict';",
		stateSource,
		secureIDSource,
		pendingMatchSource,
		approvalErrorConstants,
		errorMessageSource,
		approvalSource,
		"const elements=new Map();",
		"const calls=[];",
		"const alerts=[];",
		"let mode='conflict';",
		"let uuidCalls=0;",
		"Object.defineProperty(globalThis,'crypto',{configurable:true,writable:true,value:{randomUUID(){uuidCalls+=1;return 'uuid-'+String(uuidCalls);}}});",
		"function element(id){if(!elements.has(id))elements.set(id,{style:{},textContent:'',hidden:false});return elements.get(id);}",
		"globalThis.document={getElementById:id=>element(id)};",
		"globalThis.alert=value=>alerts.push(value);",
		"function api(url,opts){const call={url:url,opts:opts||{}};calls.push(call);if(mode==='transport')return Promise.reject(new Error('transport-secret token /Users/private'));if(mode==='malformed')return Promise.resolve({ok:false,statusText:'raw-status token',json:async()=>{throw new Error('malformed-secret /Users/private');}});return Promise.resolve({ok:false,statusText:'raw-status token',json:async()=>({code:'approval_conflict',message:'raw-server-secret https://secret.example/token'})});}",
		"function assert(condition,message){if(!condition)throw new Error(message);}",
		"function assertNoLeak(value){const text=String(value||'');for(const secret of ['raw-server-secret','https://secret.example/token','raw-status','transport-secret','/Users/private','token'])assert(!text.includes(secret),'raw detail leaked: '+secret+' in '+text);}",
		"async function run(){",
		"viewState={...viewState,proposalId:'proposal-ui-f2',evidencePackId:'pack-ui-f2',computedBasisId:'basis-ui-f2',generationId:'generation-ui-f2',intentRevision:8,approvalVersion:0,approvalState:'none',approvalExpectedVersion:0,approvalExpectedState:'none',activeApprovalId:''};",
		"await submitProposalApproval('approve');",
		"const firstBody=calls[0].opts.body;const firstPending=JSON.stringify(viewState.approvalPendingRequest);",
		"assert(calls.length===1&&calls[0].opts.allowErrors===true,'approval request did not opt into bounded non-2xx handling');",
		"assert(element('approval-result-msg').textContent==='approval_conflict: approval request conflicts with current state','conflict response was not mapped to its stable allowlisted message: '+element('approval-result-msg').textContent);",
		"assert(firstBody===firstPending,'conflict changed the pending request body');assert(alerts.length===0,'conflict raised an alert');assertNoLeak(element('approval-result-msg').textContent);",
		"mode='malformed';await submitProposalApproval('approve');",
		"assert(calls[1].opts.body===firstBody,'malformed response changed the pending request body');",
		"assert(element('approval-result-msg').textContent==='approval request failed; please retry','malformed response did not use the generic bounded failure: '+element('approval-result-msg').textContent);assert(alerts.length===0,'malformed response raised an alert');assertNoLeak(element('approval-result-msg').textContent);",
		"mode='transport';await submitProposalApproval('approve');",
		"assert(calls[2].opts.body===firstBody,'transport rejection changed the pending request body');",
		"assert(element('approval-result-msg').textContent==='approval request could not be sent; please retry','transport rejection did not use the generic retryable message: '+element('approval-result-msg').textContent);assert(alerts.length===0,'transport rejection raised an alert');assertNoLeak(element('approval-result-msg').textContent);",
		"}",
		"run().catch(error=>{console.error(error.stack||error);process.exitCode=1;});",
	}, "\n")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, nodePath, "--input-type=commonjs")
	cmd.Stdin = strings.NewReader(script)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("controlled embedded approval error harness failed: %v\n%s", err, output)
	}
}
