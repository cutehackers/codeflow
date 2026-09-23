'use strict';

const assert = require('assert');
const fs = require('fs');
const os = require('os');
const path = require('path');
const { sliceFlow } = require('../lib/slice');

function run() {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'codeflow-control-'));
  try {
    fs.writeFileSync(path.join(root, 'flow.ts'), `
export function branch(flag: boolean) { before(); if (flag) yes(); else no(); after(); }
export function early(flag: boolean) { if (flag) return 1; after(); }
export function stopped(flag: boolean) { if (flag) return 1; else return 2; unreachable(); }
export function exception() { before(); try { action(); } finally { cleanup(); } after(); }
export function terminalFinally() { try { return action(); } finally { cleanup(); } after(); }
export function terminalThrow() { try { throw failure; } finally { cleanup(); } after(); }
export function caughtThrow() { try { before(); throw failure; } catch { recover(); } finally { cleanup(); } after(); }
export function calledThrow() { try { failingAction(); } catch { recoverCalledFailure(); } after(); }
export function calledFinallyThrow() { try { finallyFailingAction(); } catch { recoverFinallyFailure(); } after(); }
export function uncaughtCalledThrow() { failingAction(); after(); }
export async function asyncCalledThrow() { try { failingAction(); } catch { recoverAsyncFailure(); } after(); }
export async function awaitedAsyncCalledThrow() { try { await asyncFailingAction(); } catch { recoverAwaitedAsyncFailure(); } after(); }
export async function locallyRecoveredAsyncCalledThrow() { try { await locallyRecoveredAsyncAction(); } catch { recoverLocalAsyncFailure(); } after(); }
export function forEach(items: any[]) { for (const item of items) { process(item); } after(); }
export function counted(limit: number) { for (let index = 0; index < limit; index++) { process(index); } after(); }
export function countedContinue(limit: number) { for (let index = 0; index < limit; index++) { if (index === 1) continue; process(index); } after(); }
export function doRetry(again: boolean) { do { process(); } while (again); after(); }
export async function asyncIterable(items: AsyncIterable<number>) { for await (const item of items) { await retry(); } after(); }
export async function recoveredAsyncIterable(items: AsyncIterable<number>) { for await (const item of items) { try { await retry(); } catch { recover(); } } after(); }
export function forBreak(items: any[]) { for (const item of items) { break; } after(); }
export function forContinue(items: any[]) { for (const item of items) { continue; } after(); }
export function selectAction(action: string) { switch (action) { case 'approve': approve(); break; case 'reject': reject(); break; default: review(); } after(); }
export async function parallelSave() { await Promise.all([save(), notify()]); after(); }
export async function unsupportedParallel(tasks: Promise<unknown>[]) { await Promise.all(tasks); after(); }
export async function asyncRetry(done: boolean) { while (!done) { await retry(); } after(); }
export async function recoveredRetry(done: boolean) { while (!done) { try { await retry(); } catch { recover(); } } after(); }
export async function finalizingRetry(done: boolean) { while (!done) { try { await retry(); } finally { cleanup(); } } after(); }
export async function recoveredFinalizingRetry(done: boolean) { while (!done) { try { await retry(); } catch { recover(); } finally { cleanup(); } } after(); }
export function empty(flag: boolean) { if (flag) {} else {} after(); }
export function filtered(flag: boolean, e: Event) { if (flag) e.preventDefault(); after(); }
export function conjunction(flag: boolean) { return flag && yes(); }
export function disjunction(flag: boolean) { return flag || yes(); }
export function fallback(flag: boolean | null) { return flag ?? yes(); }
export function assignment(flag: boolean, state: any) { return flag ? (state.value = 1) : (state.value = 2); }
export function increment(flag: boolean, state: any) { return flag && state.count++; }
export function construct(flag: boolean) { return flag ? new Service() : null; }
export function update(state: any) { state.value = before(); return state.value; }
export function destructure(state: any) { return [state.x = fallback()] = values; }
export function logical(state: any) { return state.value ??= before(); }
export function valueAssignment() { let total = 0; total = before(); return total; }
export function commaAssignment() { let total = 0; total = (before(), 3); return total; }
export function targetAssignment(accounts: any) { accounts[before()] = yes(); }
export function nestedAssignment() { let total = 0; total = before(yes()); return total; }
function before() { return 0; }
function yes() { return 1; }
function no() { return 2; }
function after() { return 3; }
function action() { return 4; }
function cleanup() { return 5; }
function work() { return 6; }
function recover() { return 6; }
function failingAction() { throw failure; }
function finallyFailingAction() { try { work(); } finally { throw failure; } }
async function asyncFailingAction() { throw failure; }
async function locallyRecoveredAsyncAction() { try { throw failure; } catch { return 15; } }
function recoverCalledFailure() { return 13; }
function recoverFinallyFailure() { return 17; }
function recoverAsyncFailure() { return 14; }
function recoverAwaitedAsyncFailure() { return 15; }
function recoverLocalAsyncFailure() { return 16; }
function process(value: any) { return value; }
function approve() { return 7; }
function reject() { return 8; }
function review() { return 9; }
async function save() { return 10; }
async function notify() { return 11; }
async function retry() { return 12; }
`);
    function analyze(name) { return sliceFlow({ repoRoot: root, candidateId: 'cand-control', entrySymbolPath: `flow.ts#${name}`, opts: { includeExecutionSemantics: true } }); }
    function connections(result) {
      const steps = new Map(result.steps.map(step => [step.ordinal, step]));
      return result.edges.filter(edge => edge.kind === 'control_flow').map(edge => {
        const from = steps.get(edge.stepOrdinal), to = steps.get(edge.targetStepOrdinal);
        assert.strictEqual(from.invocationId, to.invocationId);
        assert(!['return', 'throw'].includes(from.kind), 'completion gained normal successor');
        return [from.description, to.description];
      });
    }
    for (const [name, count] of [['assignment', 2], ['increment', 1], ['update', 1], ['logical', 1]]) {
      const result = analyze(name);
      assert.strictEqual(result.steps.filter(step => step.kind === 'mutation').length, count, `${name}: mutation omitted or duplicated`);
      for (const mutation of result.steps.filter(step => step.kind === 'mutation')) {
        assert(result.edges.some(edge => edge.kind === 'control_flow' && edge.targetStepOrdinal === mutation.ordinal), `${name}: mutation disconnected`);
      }
    }

    const valueAssignment = analyze('valueAssignment');
    const assignmentStep = valueAssignment.steps.find(step => step.kind === 'mutation');
    const valueCall = valueAssignment.steps.find(step => step.description === 'before()');
    assert.strictEqual(assignmentStep.assignmentSourceOrdinal, valueCall.ordinal, 'direct value source omitted');
    for (const name of ['commaAssignment', 'targetAssignment', 'nestedAssignment', 'update', 'logical']) {
      assert(!analyze(name).steps.some(step => step.assignmentSourceOrdinal !== undefined), `${name}: unrelated evaluation labeled as direct value`);
    }
    const destructure = analyze('destructure');
    const destructureMutation = destructure.steps.find(step => step.kind === 'mutation');
    assert(destructureMutation, 'destructuring write missing');
    assert(destructure.edges.some(edge => edge.kind === 'unknown_edge' && edge.stepOrdinal === destructureMutation.ordinal && edge.unresolvedReason.includes('구조 분해')), 'destructuring uncertainty lost in transport');
    const construction = analyze('construct');
    const constructor = construction.steps.find(step => step.description === 'new Service()');
    assert(constructor && constructor.kind === 'call', 'constructor omitted');
    assert(construction.edges.some(edge => edge.kind === 'unknown_edge' && edge.stepOrdinal === constructor.ordinal && edge.unresolvedReason.includes('생성자')), 'constructor boundary omitted');
    const logical = analyze('logical');
    const logicalDecision = logical.steps.find(step => step.kind === 'branch');
    assert.deepStrictEqual(logical.edges.filter(edge => edge.kind === 'control_flow' && edge.stepOrdinal === logicalDecision.ordinal).map(edge => edge.conditions[0].outcome).sort(), ['non_nullish', 'nullish']);
    const branchResult = analyze('branch');
    const branch = connections(branchResult);
    const decision = branchResult.steps.find(step => step.description === 'if (flag)');
    for (const [description, outcome] of [['yes()', 'truthy'], ['no()', 'falsy']]) {
      const target = branchResult.steps.find(step => step.description === description);
      const edge = branchResult.edges.find(edge => edge.kind === 'control_flow' && edge.targetStepOrdinal === target.ordinal);
      assert.deepStrictEqual(edge.conditions, [{ stepOrdinal: decision.ordinal, outcome }]);
    }
    for (const expected of [['before()', 'if (flag)'], ['if (flag)', 'yes()'], ['if (flag)', 'no()'], ['yes()', 'after()'], ['no()', 'after()']]) {
      assert(branch.some(pair => JSON.stringify(pair) === JSON.stringify(expected)), `missing ${expected}`);
    }
    assert(!branch.some(([from, to]) => from === 'yes()' && to === 'no()'), 'branches serialized');
    for (const name of ['empty', 'filtered']) {
      const result = analyze(name);
      const target = result.steps.find(step => step.description === 'after()');
      const alternatives = result.edges.filter(edge => edge.kind === 'control_flow' && edge.targetStepOrdinal === target.ordinal);
      assert.strictEqual(alternatives.length, 2, `${name}: conditional alternatives collapsed`);
      assert.deepStrictEqual(alternatives.map(edge => edge.conditions[0].outcome).sort(), ['falsy', 'truthy']);
      assert.strictEqual(alternatives[0].stepOrdinal, alternatives[1].stepOrdinal);
    }
    assert(connections(analyze('early')).some(([from, to]) => from.includes('if (flag)') && to === 'after()'), 'empty false path lost');
    for (const [name, evaluate, skip] of [['conjunction','truthy','falsy'],['disjunction','falsy','truthy'],['fallback','nullish','non_nullish']]) {
      const result = analyze(name);
      const decision = result.steps.find(step => step.kind === 'branch' && step.description === 'flag');
      assert(decision, `${name}: left operand decision missing`);
      const outgoing = result.edges.filter(edge => edge.kind === 'control_flow' && edge.stepOrdinal === decision.ordinal);
      assert.strictEqual(outgoing.length, 2);
      const calls = outgoing.find(edge => edge.conditions[0].outcome === evaluate);
      const bypass = outgoing.find(edge => edge.conditions[0].outcome === skip);
      assert.strictEqual(result.steps.find(step => step.ordinal === calls.targetStepOrdinal).description, 'yes()');
      assert.strictEqual(result.steps.find(step => step.ordinal === bypass.targetStepOrdinal).kind, 'return');
      connections(result);
    }
    const stopped = analyze('stopped');
    connections(stopped);
    assert(!stopped.steps.some(step => step.description.includes('unreachable')), 'terminal branches gained successor');
    const exception = analyze('exception');
    assert(exception.edges.some(edge => edge.unresolvedReason?.includes('finally')), 'missing exception limitation');
    assert(connections(exception).some(([from,to]) => from === 'cleanup()' && to === 'after()'), 'finally continuation missing');
    const terminalFinally = analyze('terminalFinally');
    assert(terminalFinally.edges.some(edge => edge.unresolvedReason?.includes('finally')), 'terminal finally limitation missing');
    const terminalReturn = terminalFinally.steps.find(step => step.description === 'return action();');
    const cleanup = terminalFinally.steps.find(step => step.description === 'cleanup()');
    assert(terminalFinally.edges.some(edge => edge.kind === 'finally' && edge.stepOrdinal === terminalReturn.ordinal && edge.targetStepOrdinal === cleanup.ordinal), 'terminal completion did not enter finally');
    assert(!connections(terminalFinally).some(([from, to]) => from === 'cleanup()' && to === 'after()'), 'terminal finally gained normal successor');
    assert(!terminalFinally.steps.some(step => step.description === 'after()'), 'terminal finally retained unreachable code');
    const terminalThrow = analyze('terminalThrow');
    const thrown = terminalThrow.steps.find(step => step.kind === 'throw');
    const throwCleanup = terminalThrow.steps.find(step => step.description === 'cleanup()');
    assert(terminalThrow.edges.some(edge => edge.kind === 'finally' && edge.stepOrdinal === thrown.ordinal && edge.targetStepOrdinal === throwCleanup.ordinal), 'throw completion did not enter finally');
    assert(!terminalThrow.steps.some(step => step.description === 'after()'), 'terminal throw finally retained unreachable code');
    const caughtThrow = analyze('caughtThrow');
    const caughtThrown = caughtThrow.steps.find(step => step.description === 'throw failure;');
    const recover = caughtThrow.steps.find(step => step.description === 'recover()');
    const caughtCleanup = caughtThrow.steps.find(step => step.description === 'cleanup()');
    const caughtAfter = caughtThrow.steps.find(step => step.description === 'after()');
    assert(caughtThrow.edges.some(edge => edge.kind === 'failure' && edge.stepOrdinal === caughtThrown.ordinal && edge.targetStepOrdinal === recover.ordinal), 'explicit throw did not enter catch');
    assert(connections(caughtThrow).some(([from, to]) => from === 'recover()' && to === 'cleanup()'), 'caught throw did not enter finally');
    assert(connections(caughtThrow).some(([from, to]) => from === 'cleanup()' && to === 'after()'), 'caught throw did not resume after finally');
    assert(caughtAfter, 'caught throw lost normal continuation');
    const calledThrow = analyze('calledThrow');
    const calledFailure = calledThrow.steps.find(step => step.description === 'throw failure;' && step.symbolPath === 'failingAction');
    const calledRecovery = calledThrow.steps.find(step => step.description === 'recoverCalledFailure()');
    assert(calledFailure && calledRecovery, 'called throw or recovery step missing');
    assert(calledThrow.edges.some(edge => edge.kind === 'failure' && edge.stepOrdinal === calledFailure.ordinal && edge.targetStepOrdinal === calledRecovery.ordinal), 'called throw did not enter caller catch');
    const calledFinallyThrow = analyze('calledFinallyThrow');
    const calledFinallyFailure = calledFinallyThrow.steps.find(step => step.description === 'throw failure;' && step.symbolPath === 'finallyFailingAction');
    const calledFinallyRecovery = calledFinallyThrow.steps.find(step => step.description === 'recoverFinallyFailure()');
    assert(calledFinallyFailure && calledFinallyRecovery, 'called finalizer throw or recovery step missing');
    assert(calledFinallyThrow.edges.some(edge => edge.kind === 'failure' && edge.stepOrdinal === calledFinallyFailure.ordinal && edge.targetStepOrdinal === calledFinallyRecovery.ordinal), 'called finalizer throw did not enter caller catch');
    assert(!calledFinallyThrow.edges.some(edge => edge.kind === 'unknown_edge' && edge.stepOrdinal === calledFinallyFailure.ordinal), 'finalizer throw retained an unproven continuation');
    const uncaughtCalledThrow = analyze('uncaughtCalledThrow');
    assert(!uncaughtCalledThrow.edges.some(edge => edge.kind === 'failure' && uncaughtCalledThrow.steps.find(step => step.ordinal === edge.stepOrdinal)?.symbolPath === 'failingAction'), 'uncaught called throw gained a recovery relation');
    const asyncCalledThrow = analyze('asyncCalledThrow');
    const asyncCalledFailure = asyncCalledThrow.steps.find(step => step.description === 'throw failure;' && step.symbolPath === 'failingAction');
    const asyncCalledRecovery = asyncCalledThrow.steps.find(step => step.description === 'recoverAsyncFailure()');
    assert(asyncCalledFailure && asyncCalledRecovery, 'async caller throw or recovery step missing');
    assert(asyncCalledThrow.edges.some(edge => edge.kind === 'failure' && edge.stepOrdinal === asyncCalledFailure.ordinal && edge.targetStepOrdinal === asyncCalledRecovery.ordinal), 'direct synchronous throw did not enter async caller catch');
    const awaitedAsyncCalledThrow = analyze('awaitedAsyncCalledThrow');
    const awaitedAsyncFailure = awaitedAsyncCalledThrow.steps.find(step => step.description === 'throw failure;' && step.symbolPath === 'asyncFailingAction');
    const awaitedAsyncRecovery = awaitedAsyncCalledThrow.steps.find(step => step.description === 'recoverAwaitedAsyncFailure()');
    assert(awaitedAsyncFailure && awaitedAsyncRecovery, 'awaited async throw or recovery step missing');
    assert(awaitedAsyncCalledThrow.edges.some(edge => edge.kind === 'failure' && edge.stepOrdinal === awaitedAsyncFailure.ordinal && edge.targetStepOrdinal === awaitedAsyncRecovery.ordinal), 'awaited async throw did not enter caller catch');
    const locallyRecoveredAsyncCalledThrow = analyze('locallyRecoveredAsyncCalledThrow');
    assert(!locallyRecoveredAsyncCalledThrow.edges.some(edge => edge.kind === 'failure' && locallyRecoveredAsyncCalledThrow.steps.find(step => step.ordinal === edge.stepOrdinal)?.symbolPath === 'locallyRecoveredAsyncAction' && locallyRecoveredAsyncCalledThrow.steps.find(step => step.ordinal === edge.targetStepOrdinal)?.symbolPath === 'locallyRecoveredAsyncCalledThrow'), 'locally recovered async throw escaped to caller catch');
    const forEach = analyze('forEach');
    const iterableHeader = forEach.steps.find(step => step.description === 'for (const item of items)');
    const process = forEach.steps.find(step => step.description === 'process(item)');
    const iterableAfter = forEach.steps.find(step => step.description === 'after()');
    assert(iterableHeader && process && iterableAfter, 'iterable loop steps missing');
    assert(forEach.edges.some(edge => edge.kind === 'control_flow' && edge.stepOrdinal === iterableHeader.ordinal && edge.targetStepOrdinal === process.ordinal && edge.conditions?.[0]?.outcome === 'truthy'), 'iterable loop body entry missing');
    assert(forEach.edges.some(edge => edge.kind === 'control_flow' && edge.stepOrdinal === iterableHeader.ordinal && edge.targetStepOrdinal === iterableAfter.ordinal && edge.conditions?.[0]?.outcome === 'falsy'), 'iterable loop exit missing');
    assert(forEach.edges.some(edge => edge.kind === 'loop_back' && edge.stepOrdinal === process.ordinal && edge.targetStepOrdinal === iterableHeader.ordinal), 'iterable loop re-evaluation missing');
    const counted = analyze('counted');
    const countedHeader = counted.steps.find(step => step.description === 'for (let index = 0; index < limit; index++)');
    const countedProcess = counted.steps.find(step => step.description === 'process(index)');
    const increment = counted.steps.find(step => step.description === 'index++');
    const countedAfter = counted.steps.find(step => step.description === 'after()');
    assert(countedHeader && countedProcess && increment && countedAfter, 'counted loop steps missing');
    assert(counted.edges.some(edge => edge.kind === 'control_flow' && edge.stepOrdinal === countedHeader.ordinal && edge.targetStepOrdinal === countedProcess.ordinal && edge.conditions?.[0]?.outcome === 'truthy'), 'counted loop body entry missing');
    assert(counted.edges.some(edge => edge.kind === 'control_flow' && edge.stepOrdinal === countedHeader.ordinal && edge.targetStepOrdinal === countedAfter.ordinal && edge.conditions?.[0]?.outcome === 'falsy'), 'counted loop exit missing');
    assert(counted.edges.some(edge => edge.kind === 'control_flow' && edge.stepOrdinal === countedProcess.ordinal && edge.targetStepOrdinal === increment.ordinal), 'counted loop increment missing');
    assert(counted.edges.some(edge => edge.kind === 'loop_back' && edge.stepOrdinal === increment.ordinal && edge.targetStepOrdinal === countedHeader.ordinal), 'counted loop re-evaluation missing');
    const countedContinue = analyze('countedContinue');
    const continuedIncrement = countedContinue.steps.find(step => step.description === 'index++');
    const countedContinueStep = countedContinue.steps.find(step => step.kind === 'continue');
    assert(continuedIncrement && countedContinueStep, 'counted continue steps missing');
    assert(countedContinue.edges.some(edge => edge.kind === 'control_flow' && edge.stepOrdinal === countedContinueStep.ordinal && edge.targetStepOrdinal === continuedIncrement.ordinal), 'continue bypassed counted loop increment');
    const doRetry = analyze('doRetry');
    const doProcess = doRetry.steps.find(step => step.description === 'process()');
    const doHeader = doRetry.steps.find(step => step.description === 'while (again)');
    const doAfter = doRetry.steps.find(step => step.description === 'after()');
    assert(doProcess && doHeader && doAfter, 'do loop steps missing');
    assert(doRetry.edges.some(edge => edge.kind === 'control_flow' && edge.stepOrdinal === doProcess.ordinal && edge.targetStepOrdinal === doHeader.ordinal), 'do loop condition evaluation missing');
    assert(doRetry.edges.some(edge => edge.kind === 'loop_reentry' && edge.stepOrdinal === doHeader.ordinal && edge.targetStepOrdinal === doProcess.ordinal && edge.conditions?.[0]?.outcome === 'truthy'), 'do loop body re-entry missing');
    assert(doRetry.edges.some(edge => edge.kind === 'control_flow' && edge.stepOrdinal === doHeader.ordinal && edge.targetStepOrdinal === doAfter.ordinal && edge.conditions?.[0]?.outcome === 'falsy'), 'do loop exit missing');
    const asyncIterable = analyze('asyncIterable');
    const asyncHeader = asyncIterable.steps.find(step => step.description === 'for await (const item of items)');
    const asyncDecision = asyncIterable.steps.find(step => step.kind === 'branch' && step.description === 'for await (const item of items)');
    const asyncIterationCall = asyncIterable.steps.find(step => step.description === 'retry()');
    const asyncIterationRetry = asyncIterable.steps.find(step => step.description === 'await retry()');
    const asyncAfter = asyncIterable.steps.find(step => step.description === 'after()');
    assert(asyncHeader && asyncDecision && asyncIterationCall && asyncIterationRetry && asyncAfter, 'async iterable steps missing');
    assert.strictEqual(asyncHeader.kind, 'await', 'async iterator must expose its next-item wait');
    assert(asyncIterable.edges.some(edge => edge.kind === 'await_resume' && edge.stepOrdinal === asyncHeader.ordinal && edge.targetStepOrdinal === asyncDecision.ordinal), 'async iterable next item result missing');
    assert(asyncIterable.edges.some(edge => edge.kind === 'control_flow' && edge.stepOrdinal === asyncDecision.ordinal && edge.targetStepOrdinal === asyncIterationCall.ordinal && edge.conditions?.[0]?.outcome === 'truthy'), 'async iterable body entry missing');
    assert(asyncIterable.edges.some(edge => edge.kind === 'control_flow' && edge.stepOrdinal === asyncDecision.ordinal && edge.targetStepOrdinal === asyncAfter.ordinal && edge.conditions?.[0]?.outcome === 'falsy'), 'async iterable exit missing');
    assert(asyncIterable.edges.some(edge => edge.kind === 'await_loop_back' && edge.stepOrdinal === asyncIterationRetry.ordinal && edge.targetStepOrdinal === asyncHeader.ordinal), 'async iterable next-item wait missing');
    assert(asyncIterable.edges.some(edge => edge.kind === 'unknown_edge' && edge.stepOrdinal === asyncHeader.ordinal && edge.unresolvedReason.includes('비동기 반복자')), 'async iterable limitation missing');
    const recoveredAsyncIterable = analyze('recoveredAsyncIterable');
    const recoveredAsyncHeader = recoveredAsyncIterable.steps.find(step => step.kind === 'await' && step.description === 'for await (const item of items)');
    const recoveredAsyncRetry = recoveredAsyncIterable.steps.find(step => step.kind === 'await' && step.description === 'await retry()');
    const recoveredAsyncCall = recoveredAsyncIterable.steps.find(step => step.description === 'recover()');
    const recoveredAsyncAfter = recoveredAsyncIterable.steps.find(step => step.description === 'after()');
    assert(recoveredAsyncHeader && recoveredAsyncRetry && recoveredAsyncCall && recoveredAsyncAfter, 'recovered async iterable steps missing');
    assert(recoveredAsyncIterable.edges.some(edge => edge.kind === 'await_loop_back' && edge.stepOrdinal === recoveredAsyncRetry.ordinal && edge.targetStepOrdinal === recoveredAsyncHeader.ordinal), 'successful async iterable body did not wait for the next item');
    assert(recoveredAsyncIterable.edges.some(edge => edge.kind === 'failure' && edge.stepOrdinal === recoveredAsyncRetry.ordinal && edge.targetStepOrdinal === recoveredAsyncCall.ordinal), 'async iterable body rejection did not enter recovery');
    assert(recoveredAsyncIterable.edges.some(edge => edge.kind === 'loop_back' && edge.stepOrdinal === recoveredAsyncCall.ordinal && edge.targetStepOrdinal === recoveredAsyncHeader.ordinal), 'async iterable recovery did not wait for the next item');
    assert(recoveredAsyncIterable.edges.some(edge => edge.kind === 'control_flow' && edge.targetStepOrdinal === recoveredAsyncAfter.ordinal && edge.conditions?.[0]?.outcome === 'falsy'), 'recovered async iterable exit missing');
    const forBreak = analyze('forBreak');
    const breakHeader = forBreak.steps.find(step => step.description === 'for (const item of items)');
    const breakStep = forBreak.steps.find(step => step.kind === 'break');
    const breakAfter = forBreak.steps.find(step => step.description === 'after()');
    assert(forBreak.edges.some(edge => edge.kind === 'loop_exit' && edge.stepOrdinal === breakStep.ordinal && edge.targetStepOrdinal === breakAfter.ordinal), 'iterable break exit missing');
    assert(forBreak.edges.some(edge => edge.kind === 'control_flow' && edge.stepOrdinal === breakHeader.ordinal && edge.targetStepOrdinal === breakAfter.ordinal && edge.conditions?.[0]?.outcome === 'falsy'), 'iterable natural exit missing');
    const forContinue = analyze('forContinue');
    const continueHeader = forContinue.steps.find(step => step.description === 'for (const item of items)');
    const continueStep = forContinue.steps.find(step => step.kind === 'continue');
    assert(forContinue.edges.some(edge => edge.kind === 'loop_back' && edge.stepOrdinal === continueStep.ordinal && edge.targetStepOrdinal === continueHeader.ordinal), 'iterable continue re-evaluation missing');
    const selectAction = analyze('selectAction');
    const selector = selectAction.steps.find(step => step.description === 'switch (action)');
    const approveCase = selectAction.steps.find(step => step.description === "case 'approve':");
    const approve = selectAction.steps.find(step => step.description === 'approve()');
    const switchBreak = selectAction.steps.find(step => step.kind === 'break');
    const selectAfter = selectAction.steps.find(step => step.description === 'after()');
    assert(selectAction.edges.some(edge => edge.kind === 'control_flow' && edge.stepOrdinal === selector.ordinal && edge.targetStepOrdinal === approveCase.ordinal), 'switch case selection missing');
    assert(selectAction.edges.some(edge => edge.kind === 'control_flow' && edge.stepOrdinal === approveCase.ordinal && edge.targetStepOrdinal === approve.ordinal), 'switch case body entry missing');
    assert(selectAction.edges.some(edge => edge.kind === 'switch_exit' && edge.stepOrdinal === switchBreak.ordinal && edge.targetStepOrdinal === selectAfter.ordinal), 'switch break exit missing');
    const parallelSave = analyze('parallelSave');
    const save = parallelSave.steps.find(step => step.description === 'save()');
    const notify = parallelSave.steps.find(step => step.description === 'notify()');
    const parallelAwait = parallelSave.steps.find(step => step.kind === 'await');
    const parallelAfter = parallelSave.steps.find(step => step.description === 'after()');
    assert(save && notify && parallelAwait && parallelAfter, 'parallel wait steps missing');
    assert(parallelSave.edges.some(edge => edge.kind === 'parallel_wait' && edge.stepOrdinal === save.ordinal && edge.targetStepOrdinal === parallelAwait.ordinal), 'first parallel member missing');
    assert(parallelSave.edges.some(edge => edge.kind === 'parallel_wait' && edge.stepOrdinal === notify.ordinal && edge.targetStepOrdinal === parallelAwait.ordinal), 'second parallel member missing');
    assert(!parallelSave.edges.some(edge => edge.kind === 'control_flow' && edge.stepOrdinal === save.ordinal && edge.targetStepOrdinal === notify.ordinal), 'parallel members were serialized');
    assert(parallelSave.edges.some(edge => edge.kind === 'await_resume' && edge.stepOrdinal === parallelAwait.ordinal && edge.targetStepOrdinal === parallelAfter.ordinal), 'parallel wait did not resume');
    const unsupportedParallel = analyze('unsupportedParallel');
    const unsupportedAwait = unsupportedParallel.steps.find(step => step.kind === 'await');
    assert(unsupportedAwait, 'unsupported parallel wait missing');
    assert(unsupportedParallel.edges.some(edge => edge.kind === 'unknown_edge' && edge.stepOrdinal === unsupportedAwait.ordinal && edge.unresolvedReason.includes('병렬 대기')), 'unsupported parallel wait was not marked');
    const asyncRetry = analyze('asyncRetry');
    const retryHeader = asyncRetry.steps.find(step => step.description === 'while (!done)');
    const retryAwait = asyncRetry.steps.find(step => step.kind === 'await');
    const retryAfter = asyncRetry.steps.find(step => step.description === 'after()');
    assert(retryHeader && retryAwait && retryAfter, 'async retry loop steps missing');
    assert(asyncRetry.edges.some(edge => edge.kind === 'await_loop_back' && edge.stepOrdinal === retryAwait.ordinal && edge.targetStepOrdinal === retryHeader.ordinal), 'await did not re-evaluate retry condition');
    assert(asyncRetry.edges.some(edge => edge.kind === 'control_flow' && edge.stepOrdinal === retryHeader.ordinal && edge.targetStepOrdinal === retryAfter.ordinal && edge.conditions?.[0]?.outcome === 'falsy'), 'async retry termination missing');
    const recoveredRetry = analyze('recoveredRetry');
    const recoveredHeader = recoveredRetry.steps.find(step => step.description === 'while (!done)');
    const recoveredAwait = recoveredRetry.steps.find(step => step.kind === 'await');
    const recoveredCall = recoveredRetry.steps.find(step => step.description === 'recover()');
    const recoveredAfter = recoveredRetry.steps.find(step => step.description === 'after()');
    assert(recoveredHeader && recoveredAwait && recoveredCall && recoveredAfter, 'recovered retry steps missing');
    assert(recoveredRetry.edges.some(edge => edge.kind === 'await_loop_back' && edge.stepOrdinal === recoveredAwait.ordinal && edge.targetStepOrdinal === recoveredHeader.ordinal), 'successful retry did not re-evaluate condition');
    assert(recoveredRetry.edges.some(edge => edge.kind === 'failure' && edge.stepOrdinal === recoveredAwait.ordinal && edge.targetStepOrdinal === recoveredCall.ordinal), 'rejected retry did not enter recovery');
    assert(recoveredRetry.edges.some(edge => edge.kind === 'loop_back' && edge.stepOrdinal === recoveredCall.ordinal && edge.targetStepOrdinal === recoveredHeader.ordinal), 'recovery did not re-evaluate condition');
    assert(recoveredRetry.edges.some(edge => edge.kind === 'control_flow' && edge.stepOrdinal === recoveredHeader.ordinal && edge.targetStepOrdinal === recoveredAfter.ordinal && edge.conditions?.[0]?.outcome === 'falsy'), 'recovered retry termination missing');
    const finalizingRetry = analyze('finalizingRetry');
    const finalizingHeader = finalizingRetry.steps.find(step => step.description === 'while (!done)');
    const finalizingAwait = finalizingRetry.steps.find(step => step.kind === 'await' && step.description === 'await retry()');
    const finalizingCleanup = finalizingRetry.steps.find(step => step.description === 'cleanup()');
    const finalizingAfter = finalizingRetry.steps.find(step => step.description === 'after()');
    assert(finalizingHeader && finalizingAwait && finalizingCleanup && finalizingAfter, 'finalizing retry steps missing');
    assert(finalizingRetry.edges.some(edge => edge.kind === 'await_resume' && edge.stepOrdinal === finalizingAwait.ordinal && edge.targetStepOrdinal === finalizingCleanup.ordinal), 'successful retry did not enter finalizer');
    assert(finalizingRetry.edges.some(edge => edge.kind === 'loop_back' && edge.stepOrdinal === finalizingCleanup.ordinal && edge.targetStepOrdinal === finalizingHeader.ordinal), 'finalizer did not re-evaluate condition');
    assert(finalizingRetry.edges.some(edge => edge.kind === 'control_flow' && edge.stepOrdinal === finalizingHeader.ordinal && edge.targetStepOrdinal === finalizingAfter.ordinal && edge.conditions?.[0]?.outcome === 'falsy'), 'finalizing retry termination missing');
    assert(finalizingRetry.edges.some(edge => edge.kind === 'unknown_edge' && edge.stepOrdinal === finalizingAwait.ordinal && edge.unresolvedReason.includes('대기 실패')), 'unhandled retry rejection must remain unknown');
    const recoveredFinalizingRetry = analyze('recoveredFinalizingRetry');
    const recoveredFinalizingHeader = recoveredFinalizingRetry.steps.find(step => step.description === 'while (!done)');
    const recoveredFinalizingAwait = recoveredFinalizingRetry.steps.find(step => step.kind === 'await' && step.description === 'await retry()');
    const recoveredFinalizingCall = recoveredFinalizingRetry.steps.find(step => step.description === 'recover()');
    const recoveredFinalizingCleanup = recoveredFinalizingRetry.steps.find(step => step.description === 'cleanup()');
    assert(recoveredFinalizingHeader && recoveredFinalizingAwait && recoveredFinalizingCall && recoveredFinalizingCleanup, 'recovered finalizing retry steps missing');
    assert(recoveredFinalizingRetry.edges.some(edge => edge.kind === 'failure' && edge.stepOrdinal === recoveredFinalizingAwait.ordinal && edge.targetStepOrdinal === recoveredFinalizingCall.ordinal), 'rejected retry did not enter recovery before finalizer');
    assert(recoveredFinalizingRetry.edges.some(edge => edge.kind === 'await_resume' && edge.stepOrdinal === recoveredFinalizingAwait.ordinal && edge.targetStepOrdinal === recoveredFinalizingCleanup.ordinal), 'successful retry did not enter finalizer');
    assert(recoveredFinalizingRetry.edges.some(edge => edge.kind === 'control_flow' && edge.stepOrdinal === recoveredFinalizingCall.ordinal && edge.targetStepOrdinal === recoveredFinalizingCleanup.ordinal), 'recovery did not enter finalizer');
    assert(recoveredFinalizingRetry.edges.some(edge => edge.kind === 'loop_back' && edge.stepOrdinal === recoveredFinalizingCleanup.ordinal && edge.targetStepOrdinal === recoveredFinalizingHeader.ordinal), 'finalizer did not re-evaluate after recovery');
  } finally { fs.rmSync(root, { recursive: true, force: true }); }
}
module.exports = { run };
if (require.main === module) run();
