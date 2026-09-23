'use strict';
const assert = require('assert');
const fs = require('fs');
const os = require('os');
const path = require('path');
const { sliceFlow } = require('../lib/slice');
function run() {
 const root = fs.mkdtempSync(path.join(os.tmpdir(),'codeflow-normal-return-'));
 try {
  const source = `function price(flag) { if (flag) return 1; return 2; }
function outer(value) { return value; }
function promiseValue() { return Promise.resolve(1); }
async function asyncValue() { return 1; }
async function signal() { return; }
function* generatorValue() { yield 1; return 2; }
function finalValue() { try { return 1; } finally { throw 2; } }
function finalReplacement() { try { return original(); } finally { return replacement(); } }
async function asyncFinalReplacement() { try { return original(); } finally { return replacement(); } }
export function start() { let first; let second; first = price(true); second = price(false); return outer(price(true)); }
export async function awaited() { const value = await price(true); return value; }
export function initialized() { const amount = price(true); return amount; }
export async function asyncDirectCaller() { const amount = price(true); after(amount); }
export async function asyncCallerWithLaterWait() { const amount = price(true); await signal(); after(amount); }
export function destructured() { const { amount = price(false) } = price(true); return amount; }
export async function distinctLimits() { await price(true); const next = new UnknownThing(); return next; }
export function uncertain() { return finalValue(); }
export function asyncCaller() { return asyncValue(); }
export function generatorCaller() { return generatorValue(); }
export function finallyCaller() { let result; result = finalReplacement(); after(result); }
export async function asyncFinallyCaller() { const result = await asyncFinalReplacement(); return result; }
function original() { return 1; }
function replacement() { return 2; }
function after(value) { return value; }
`;
  fs.writeFileSync(path.join(root,'flow.ts'),source);
  const analyze = name => sliceFlow({repoRoot:root,snapshot:{snapshotId:'snapshot-return',files:{'flow.ts':source}},candidateId:'cand-normalreturn',entrySymbolPath:`flow.ts#${name}`,opts:{includeExecutionSemantics:true}});
  const limits = analyze('distinctLimits').edges.filter(e=>e.unresolvedReason);
  assert(limits.length >= 2);
  assert.strictEqual(new Set(limits.map(e=>e.toSymbolPath)).size, limits.length, 'different execution limitations must have distinct subjects');
  const awaited = analyze('awaited');
  const waiting = awaited.steps.find(s=>s.kind==='await');
  assert(waiting, 'await must remain an explicit execution step');
  assert.strictEqual(waiting.description, 'await price(true)');
  assert.deepStrictEqual(waiting.flowContext.statement.byteRange, waiting.anchor.byteRange);
  const resume = awaited.edges.filter(e=>e.kind==='await_resume');
  assert.strictEqual(resume.length,1);
  assert.strictEqual(resume[0].stepOrdinal,waiting.ordinal);
  assert.strictEqual(awaited.steps.find(s=>s.ordinal===resume[0].targetStepOrdinal).kind,'mutation');
  assert(awaited.edges.some(e=>e.stepOrdinal===waiting.ordinal && e.kind==='unknown_edge'),'rejection path must remain explicit');
  assert(!awaited.edges.some(e=>e.stepOrdinal===waiting.ordinal && e.kind==='control_flow'));
  const initialized = analyze('initialized');
  const received = initialized.steps.find(s=>s.kind==='mutation' && s.description==='amount = price(true)');
  assert(received, 'call result initialization must be an execution step');
  assert.deepStrictEqual(received.flowContext?.statement.byteRange, received.anchor.byteRange, 'initialization must have exact source context');
  assert(received.assignmentSourceOrdinal, 'direct initialization must preserve value source');
  assert.strictEqual(initialized.edges.filter(e=>e.kind==='return' && e.targetStepOrdinal===received.ordinal).length,2);
  const asyncDirectCaller = analyze('asyncDirectCaller');
  const asyncReceived = asyncDirectCaller.steps.find(s=>s.kind==='mutation' && s.description==='amount = price(true)');
  assert(asyncReceived, 'async caller must retain the direct synchronous call result step');
  assert.strictEqual(asyncDirectCaller.edges.filter(e=>e.kind==='return' && e.targetStepOrdinal===asyncReceived.ordinal).length,2, 'direct synchronous return must resume an async caller expression');
  const asyncCallerWithLaterWait = analyze('asyncCallerWithLaterWait');
  const laterWaitReceived = asyncCallerWithLaterWait.steps.find(s=>s.kind==='mutation' && s.description==='amount = price(true)');
  assert(laterWaitReceived, 'async caller with a later await must retain the direct synchronous call result step');
  assert.strictEqual(asyncCallerWithLaterWait.edges.filter(e=>e.kind==='return' && e.targetStepOrdinal===laterWaitReceived.ordinal).length,2, 'later await must not erase an earlier direct synchronous return');
  const destructured = analyze('destructured');
  assert.strictEqual(destructured.edges.filter(e=>e.kind==='return').length,0,'unmodeled binding defaults cannot imply normal resumption');
  const result = analyze('start');
  const steps = new Map(result.steps.map(s=>[s.ordinal,s]));
  const returned = result.edges.filter(e=>e.kind==='return');
  assert.strictEqual(returned.length,7,'three price invocations and outer must keep their returns');
  for(const edge of returned) {
   const from=steps.get(edge.stepOrdinal), to=steps.get(edge.targetStepOrdinal);
   const caller=steps.get(from.callerStepOrdinal);
   assert.strictEqual(from.kind,'return');
   assert.strictEqual(to.invocationId,caller.invocationId);
   assert(result.edges.some(e=>e.kind==='control_flow' && e.stepOrdinal===caller.ordinal && e.targetStepOrdinal===to.ordinal));
  }
  const nested = returned.filter(e=>steps.get(e.stepOrdinal).symbolPath==='price' && steps.get(e.targetStepOrdinal).description==='outer(price(true))');
  assert.strictEqual(nested.length,2,'inner must resume at outer call, not enclosing return');
  const finallyCaller = analyze('finallyCaller');
  const finallySteps = new Map(finallyCaller.steps.map(s=>[s.ordinal,s]));
  const finalResult = finallyCaller.steps.find(s=>s.description==='result = finalReplacement()');
  const finalReturns = finallyCaller.edges.filter(edge=>edge.kind==='return' && edge.targetStepOrdinal===finalResult.ordinal);
  assert.strictEqual(finalReturns.length,1,'only the finalizer return may resume the caller');
  assert.strictEqual(finallySteps.get(finalReturns[0].stepOrdinal).description,'return replacement();');
  assert(!finallyCaller.edges.some(edge=>edge.kind==='return' && finallySteps.get(edge.stepOrdinal)?.description==='return original();' && edge.targetStepOrdinal===finalResult.ordinal),'try return bypassed finalizer result');
  for(const name of ['awaited','uncertain','asyncCaller','asyncFinallyCaller','generatorCaller']) assert.strictEqual(analyze(name).edges.filter(e=>e.kind==='return').length,0,`${name}: unproven normal return`);
 } finally { fs.rmSync(root,{recursive:true,force:true}); }
}
module.exports={run};
if(require.main===module)run();
