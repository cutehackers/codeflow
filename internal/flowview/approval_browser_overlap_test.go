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

// VS09-A7/A9/A12: notification relevance is established by real selected
// history, not by generation identity shared by multiple stored proposals.
func TestApprovalBrowserUnrelatedNotificationCannotCancelSelectedRefresh(t *testing.T) {
	runApprovalBrowserOverlappingNotifications(t, false)
}

func TestApprovalBrowserNewerSelectedNotificationSupersedesOlderRefresh(t *testing.T) {
	runApprovalBrowserOverlappingNotifications(t, true)
}

func runApprovalBrowserOverlappingNotifications(t *testing.T, newerSelected bool) {
	t.Helper()
	if os.Getenv("CODEFLOW_MODEL_HOST_HELPER") == "flowview" {
		t.Skip("helper process")
	}
	srv, _, enrichment := newFlowViewApprovalProposalFixture(t)
	srv.Start()
	defer srv.Shutdown(context.Background())
	data, err := json.Marshal(map[string]any{
		"url": srv.URL(), "command": json.RawMessage(flowViewApprovalBody(enrichment, "selected-overlap", "approve", false, "")),
		"newerSelected": newerSelected,
		"payload":       map[string]any{"semanticMap": srv.cachedSemanticMap(enrichment.Proposal.GenerationID, ""), "enrichment": enrichment, "workspaceId": srv.approvalWorkspaceID},
	})
	if err != nil {
		t.Fatal(err)
	}
	module, err := filepath.Abs("../../web/live-comprehension-workspace/node_modules/@playwright/test")
	if err != nil {
		t.Fatal(err)
	}
	moduleJSON, _ := json.Marshal(module)
	script := `const {chromium,expect}=require(` + string(moduleJSON) + `);
let input='';process.stdin.on('data',x=>input+=x);process.stdin.on('end',async()=>{
 const f=JSON.parse(input);const browser=await chromium.launch({headless:true});
 try {
  const page=await browser.newPage();await page.goto(f.url);
  await page.evaluate(x=>renderSemanticTaskView(x),f.payload);
  await expect(page.locator('#approval-history-status')).toHaveText('No durable approval history');
  await expect(page.locator('#badge-connection')).toHaveText('SSE: connected');
  const endpoint=path=>{const u=new URL(path,f.url);u.search=new URL(f.url).search;return u.toString();};
  const otherResponse=await page.request.post(endpoint('/api/semantic/enrich'),{data:{generationId:f.command.generationId,targetStepId:'step-live-delta',promptRevision:'overlap-second-proposal'}});
  expect(otherResponse.status()).toBe(200);const other=await otherResponse.json();
  expect(other.proposal.proposalId).not.toBe(f.command.proposalId);
  let capturedResolve,releaseResolve,secondResolve;
  const captured=new Promise(r=>capturedResolve=r),release=new Promise(r=>releaseResolve=r),second=new Promise(r=>secondResolve=r);
  let count=0;
  await page.route(/\/api\/semantic\/approval-history(?:\?|$)/,async route=>{
   const n=++count;const response=await route.fetch();expect(response.status()).toBe(200);expect((await response.json()).aggregate.version).toBe(n===2&&f.newerSelected?2:1);
   if(n===1){capturedResolve();await release;}
   await route.fulfill({response,headers:{...response.headers(),'X-Test-Overlap-History':String(n)}});
   if(n===2)secondResolve();
  });
  const firstCommit=await page.request.post(endpoint('/api/semantic/approve'),{data:f.command});expect(firstCommit.status()).toBe(200);const firstReceipt=await firstCommit.json();
  await captured;
  const secondResponse=page.waitForResponse(r=>r.headers()['x-test-overlap-history']==='2');
  const otherCommand={...f.command,commandId:'command-other-overlap',idempotencyKey:'idempotency-other-overlap',proposalId:other.proposal.proposalId,evidencePackId:other.pack.evidencePackId};
  const newerCommand={...f.command,commandId:'command-selected-newer',idempotencyKey:'key-selected-newer',decision:'edit_then_approve',editedText:'Newer selected meaning',expectedApprovalVersion:1,expectedState:'active',predecessorApprovalId:firstReceipt.receipt.aggregate.activeApprovalId};
  expect((await page.request.post(endpoint('/api/semantic/approve'),{data:f.newerSelected?newerCommand:otherCommand})).status()).toBe(200);
  await second;await (await secondResponse).finished();
  const firstResponse=page.waitForResponse(r=>r.headers()['x-test-overlap-history']==='1');releaseResolve();await (await firstResponse).finished();
  await page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(resolve)));
  await expect(page.locator('#approval-history-state')).toHaveText('active');
  await expect(page.locator('#approval-history-version')).toHaveText(f.newerSelected?'2':'1');
  await expect(page.locator('#approval-history-events li')).toHaveText(f.newerSelected?['v1 · Approved','v2 · Edited and approved']:['v1 · Approved']);
  expect(await page.evaluate(()=>viewState.proposalId)).toBe(f.command.proposalId);
  expect(await page.evaluate(()=>viewState.approvalPendingRequest)).toBeNull();
 } finally {await browser.close();}
}).on('error',e=>{console.error(e);process.exitCode=1;});`
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "node", "-e", script)
	cmd.Stdin = strings.NewReader(string(data))
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("overlapping real approval streams: %v\n%s", err, output)
	}
}
