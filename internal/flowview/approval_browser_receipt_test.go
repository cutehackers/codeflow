package flowview

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// VS09-A6/A12: actual HTTP publication receipts must be accepted by the
// shipped page, using a proposal produced by the real enrichment boundary.
func TestApprovalBrowserAcceptsPublishedHTTPReceipt(t *testing.T) {
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") == "flowview" {
		t.Skip("helper process")
	}
	srv, _, enrichment := newFlowViewApprovalProposalFixture(t)
	srv.Start()
	defer srv.Shutdown(context.Background())
	mapIR := srv.cachedSemanticMap(enrichment.Proposal.GenerationID, "")
	data, err := json.Marshal(map[string]any{"url": srv.URL(), "payload": map[string]any{"semanticMap": mapIR, "enrichment": enrichment, "workspaceId": srv.approvalWorkspaceID}})
	if err != nil {
		t.Fatal(err)
	}
	playwright, err := filepath.Abs("../../web/live-comprehension-workspace/node_modules/@playwright/test")
	if err != nil {
		t.Fatal(err)
	}
	module, _ := json.Marshal(playwright)
	script := `const {chromium,expect}=require(` + string(module) + `);
let input='';process.stdin.on('data',data=>input+=data);process.stdin.on('end',async()=>{
const fixture=JSON.parse(input);const browser=await chromium.launch({headless:true});try{
const page=await browser.newPage();await page.goto(fixture.url);
await page.evaluate(()=>clearApprovalHistoryUI());
await expect(page.locator('#approval-history-freshness')).toHaveText('unknown');
await page.evaluate(payload=>renderSemanticTaskView(payload),fixture.payload);
await expect(page.locator('#approval-history-status')).toHaveText('No durable approval history');
await expect(page.locator('#approval-history-freshness')).toHaveText('unknown');
await expect(page.locator('#btn-semantic-approve')).toBeEnabled();
const observer=await browser.newPage();await observer.goto(fixture.url);
await observer.evaluate(payload=>{renderSemanticTaskView(payload);window.approvalNotifications=[];liveEventSource.addEventListener('approval.updated',event=>window.approvalNotifications.push({id:event.lastEventId,cursor:lastSeenEventId,envelope:JSON.parse(event.data)}));},fixture.payload);
await expect(observer.locator('#badge-connection')).toHaveText('SSE: connected');
await expect(observer.locator('#approval-history-status')).toHaveText('No durable approval history');
const enrichURL=new URL('/api/semantic/enrich',fixture.url);enrichURL.search=new URL(fixture.url).search;
const otherEnrichmentResponse=await page.request.post(enrichURL.toString(),{data:{generationId:fixture.payload.enrichment.proposal.generationId,targetStepId:'step-live-delta',promptRevision:'different-browser-proposal'}});
expect(otherEnrichmentResponse.status()).toBe(200);const otherEnrichment=await otherEnrichmentResponse.json();expect(otherEnrichment.proposal.proposalId).not.toBe(fixture.payload.enrichment.proposal.proposalId);
const unrelatedPage=await browser.newPage();await unrelatedPage.goto(fixture.url);await unrelatedPage.evaluate(payload=>renderSemanticTaskView(payload),{...fixture.payload,enrichment:otherEnrichment});
await expect(unrelatedPage.locator('#approval-history-status')).toHaveText('No durable approval history');
await expect(unrelatedPage.locator('#badge-connection')).toHaveText('SSE: connected');
const responsePromise=page.waitForResponse(response=>response.url().includes('/api/semantic/approve')&&response.request().method()==='POST');
await page.locator('#btn-semantic-approve').click();const response=await responsePromise;
expect(response.status()).toBe(200);const receipt=await response.json();expect(receipt.receipt.outbox.deliveryState).toBe('published');
await expect(page.locator('#approval-history-state')).toHaveText('active');
await expect(page.locator('#approval-history-version')).toHaveText('1');
await expect(page.locator('#approval-result-msg')).toContainText('승인 기록 생성됨');
expect(await page.evaluate(()=>viewState.approvalPendingRequest)).toBeNull();
await expect(observer.locator('#approval-history-state')).toHaveText('active');
await expect(observer.locator('#approval-history-version')).toHaveText('1');
await expect(observer.locator('#approval-history-freshness')).toHaveText('current');
await expect(unrelatedPage.locator('#approval-history-state')).toHaveText('none');
await expect(unrelatedPage.locator('#approval-history-status')).toHaveText('No durable approval history');
await expect(unrelatedPage.locator('#approval-history-freshness')).toHaveText('unknown');
await unrelatedPage.close();
const notification=await observer.evaluate(()=>window.approvalNotifications[0]);
expect(notification.envelope.data.approvalEvent.eventId).toBe(receipt.receipt.event.eventId);
expect(notification.id).not.toBe(receipt.receipt.event.eventId);
expect(notification.cursor).toBe(notification.id);
await observer.evaluate(()=>{liveEventSource.close();clearApprovalHistoryUI();lastSeenEventId='evicted-browser-cursor';initLiveStream();liveEventSource.addEventListener('snapshot_sync',event=>window.recoveryNotification={id:event.lastEventId,cursor:lastSeenEventId,envelope:JSON.parse(event.data)});});
await expect(observer.locator('#approval-history-state')).toHaveText('active');
await expect(observer.locator('#approval-history-version')).toHaveText('1');
const recovery=await observer.evaluate(()=>window.recoveryNotification);
expect(recovery.cursor).toBe(recovery.id);
const transport=await observer.evaluate(()=>{liveEventSource.close();return {cursor:lastSeenEventId,sequence:viewState.lastEventSequence};});
const malformedRecovery=JSON.parse(JSON.stringify(recovery.envelope));malformedRecovery.sequence=transport.sequence+1;malformedRecovery.eventId='malformed-recovery-cursor';malformedRecovery.data={activity:{activity:'SECRET_UNVALIDATED',pendingRevisions:-1}};
await observer.evaluate(env=>liveEventSource.dispatchEvent(new MessageEvent('snapshot_sync',{lastEventId:env.eventId,data:JSON.stringify(env)})),malformedRecovery);
expect(await observer.evaluate(()=>lastSeenEventId)).toBe(transport.cursor);
await expect(observer.locator('#approval-history-state')).toHaveText('active');
const malformedOptional=JSON.parse(JSON.stringify(recovery.envelope));malformedOptional.sequence=transport.sequence+1;malformedOptional.eventId='invalid-optional-identity';malformedOptional.generationId={value:'not-a-generation-id'};
await observer.evaluate(env=>liveEventSource.dispatchEvent(new MessageEvent('snapshot_sync',{lastEventId:env.eventId,data:JSON.stringify(env)})),malformedOptional);
expect(await observer.evaluate(()=>lastSeenEventId)).toBe(transport.cursor);
for(const [label,mutate] of [
['future committed time',env=>env.data.committedAt='9999-12-31T23:59:59Z'],
['invalid payload ref',env=>env.payloadRef={secret:'not-a-reference'}],
['extra private text',env=>env.data.approvalEvent.approvedText='SECRET_EVENT_TEXT'],
['invalid version',env=>env.data.approvalEvent.aggregateVersion=-1],
['wrong workspace',env=>env.data.approvalEvent.workspaceId='unrelated-workspace'],
['mismatched basis',env=>env.computedBasisId='f'.repeat(64)],
['wrong schema',env=>env.schemaVersion=1],
]){
const env=JSON.parse(JSON.stringify(notification.envelope));env.sequence=transport.sequence+1;env.eventId='invalid-notification-cursor';mutate(env);
await observer.evaluate(env=>liveEventSource.dispatchEvent(new MessageEvent('approval.updated',{lastEventId:env.eventId,data:JSON.stringify(env)})),env);
expect(await observer.evaluate(()=>lastSeenEventId),label).toBe(transport.cursor);
await expect(observer.locator('#approval-history-events li'),label).toHaveText(['v1 · Approved']);
}
await observer.evaluate(env=>liveEventSource.dispatchEvent(new MessageEvent('approval.updated',{lastEventId:env.eventId,data:JSON.stringify(env)})),notification.envelope);
expect(await observer.evaluate(()=>lastSeenEventId)).toBe(transport.cursor);
const unrelated=JSON.parse(JSON.stringify(notification.envelope));unrelated.sequence=transport.sequence+1;unrelated.eventId='other-aggregate-envelope';unrelated.data.approvalEvent.aggregateId='unrelated-aggregate';
await observer.evaluate(env=>liveEventSource.dispatchEvent(new MessageEvent('approval.updated',{lastEventId:env.eventId,data:JSON.stringify(env)})),unrelated);
expect(await observer.evaluate(()=>lastSeenEventId)).toBe(unrelated.eventId);
await expect(observer.locator('#approval-history-events li')).toHaveText(['v1 · Approved']);
await observer.close();
const original=JSON.parse(response.request().postData());
const cases=[
['missing publication time',value=>delete value.receipt.outbox.publishedAt],
['non-UTC time',value=>value.receipt.outbox.publishedAt='2026-09-07T00:00:00+00:00'],
['nonexistent date',value=>value.receipt.outbox.publishedAt='2026-02-30T00:00:00Z'],
['noncanonical fraction',value=>value.receipt.outbox.publishedAt='2026-09-07T00:00:00.100Z'],
['published failure',value=>value.receipt.outbox.failureReason='SECRET_INVALID_RESPONSE'],
['pending published time',value=>value.receipt.outbox.deliveryState='pending'],
['failed delivery',value=>{value.receipt.outbox.deliveryState='failed';value.receipt.outbox.failureReason='failed';}],
['nanosecond reversal',value=>{const committed='2026-09-07T00:00:00.123456789Z';value.receipt.event.timestamp=committed;value.receipt.idempotencyResult.committedAt=committed;value.receipt.outbox.committedAt=committed;value.receipt.outbox.publishedAt='2026-09-07T00:00:00.123456788Z';}]
];
for(const [label,mutate] of cases){
await page.reload();await page.evaluate(({payload,request})=>{renderSemanticTaskView(payload);viewState={...viewState,approvalPendingRequest:Object.freeze(request),approvalCommandId:request.commandId,approvalIdempotencyKey:request.idempotencyKey,approvalDecision:request.decision};},{payload:fixture.payload,request:original});
await page.route(/\/api\/semantic\/approve(?:\?|$)/,async route=>{const real=await route.fetch();expect(real.status()).toBe(200);const value=await real.json();mutate(value);await route.fulfill({response:real,json:value});});
await page.locator('#btn-semantic-approve').click();await expect(page.locator('#approval-result-msg'),label).toHaveText('approval request failed; please retry');
await expect(page.locator('#approval-history-state'),label).toHaveText('none');
expect(await page.evaluate(()=>viewState.approvalPendingRequest),label).toEqual(original);
await page.unroute(/\/api\/semantic\/approve(?:\?|$)/);
}
}finally{await browser.close();}}).on('error',error=>{console.error(error);process.exitCode=1;});`
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "node", "-e", script)
	cmd.Stdin = strings.NewReader(string(data))
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("actual HTTP receipt to browser: %v\n%s", err, output)
	}
}
