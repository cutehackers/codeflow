'use strict';

const assert = require('assert');
const fs = require('fs');
const path = require('path');
const os = require('os');
const { sliceFlow } = require('../lib/slice');

function run() {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'codeflow-execution-'));
  try {
    fs.writeFileSync(path.join(root, 'entry.ts'), `
import { calculate } from './calculate';
export function checkout() {
  e.preventDefault();
  const first = calculate(1);
  const second = calculate(2);
  const third = calculate(3);
  return { first, second, third };
  unreachable();
}`);
    fs.writeFileSync(path.join(root, 'calculate.ts'), `
export function calculate(value: number) {
  verify(value);
  return value * 2;
}`);
    const params = { repoRoot: root, candidateId: 'cand-execution', entrySymbolPath: 'entry.ts#checkout' };
    const result = sliceFlow({ ...params, opts: { includeExecutionSemantics: true } });
    assert.strictEqual(result.steps.filter(step => step.kind === 'return').length, 4, 'all return results must be retained');
    assert(!result.steps.some(step => step.description.includes('unreachable')), 'unreachable statement included');
    assert(!result.steps.some(step => step.description.includes('preventDefault')), 'UI noise reintroduced');
    const calls = result.edges.filter(edge => edge.kind === 'resolved_cross_file' && edge.toSymbolPath === 'calculate.ts#calculate');
    assert.strictEqual(calls.length, 3);
    const invocations = new Set();
    for (const call of calls) {
      const entry = result.steps.find(step => step.ordinal === call.targetStepOrdinal);
      assert(entry && entry.symbolPath === 'calculate', 'missing explicit target entry');
      assert(entry.description.includes('verify(value)'), 'return chosen instead of invocation entry');
      assert.strictEqual(result.steps.filter(step => step.invocationId === entry.invocationId).length, 2);
      assert.strictEqual(entry.callerStepOrdinal, call.stepOrdinal, 'target belongs to a different caller');
      invocations.add(entry.invocationId);
    }
    assert.strictEqual(invocations.size, 3, 'separate calls share an invocation identity');
    assert.deepStrictEqual(result, sliceFlow({ ...params, opts: { includeExecutionSemantics: true } }), 'execution identities are not deterministic');
    const legacy = sliceFlow(params);
    assert(legacy.steps.every(step => !['return', 'throw'].includes(step.kind) && !('invocationId' in step)), 'unrequested extension leaked to legacy client');
    assert(legacy.edges.every(edge => !('targetStepOrdinal' in edge)), 'new edge field leaked to legacy client');
    const limited = sliceFlow({ ...params, opts: { includeExecutionSemantics: true, maxDepth: 1 } });
    assert(limited.edges.filter(edge => edge.kind === 'resolved_cross_file' && edge.toSymbolPath === 'calculate.ts#calculate').every(edge => !edge.targetStepOrdinal && edge.resolutionStatus !== 'resolved'), 'depth cap fabricated an invocation entry');
    fs.writeFileSync(path.join(root, 'completion.ts'), `
export function complete(flag: boolean) {
  const unused = () => { return 'nested'; };
  if (flag) return 'early';
  try {
    throw new Error('failure');
    unreachableTry();
  } catch (error) {
    recover(error);
  } finally {
    cleanup();
  }
  return 'done';
  unreachableEnd();
}`);
    const completion = sliceFlow({ ...params, entrySymbolPath: 'completion.ts#complete', opts: { includeExecutionSemantics: true } });
    assert.deepStrictEqual(completion.steps.filter(step => ['return', 'throw'].includes(step.kind)).map(step => step.description), ["return 'early';", "throw new Error('failure');", "return 'done';"]);
    assert(completion.steps.some(step => step.description.includes('recover(error)')), 'throw incorrectly removed catch');
    assert(completion.steps.some(step => step.description.includes('cleanup()')), 'throw incorrectly removed finally');
    assert(!completion.steps.some(step => /unreachableTry|unreachableEnd/.test(step.description)), 'unreachable block suffix retained');
    fs.writeFileSync(path.join(root, 'branches.ts'), `
export function choose(flag: boolean) {
  const unused = () => { sideEffect(); return 'nested'; };
  if (ready()) { return first(); } else { return second(); }
  unreachable();
}
export function objectResult() { return { run: () => sideEffect() }; }
function sideEffect() { return 'effect'; }
function ready() { return true; }
function first() { return 1; }
function second() { return 2; }
`);
    const branches = sliceFlow({ ...params, entrySymbolPath: 'branches.ts#choose', opts: { includeExecutionSemantics: true } });
    const targets = branches.edges.filter(edge => edge.kind === 'resolved_cross_file').map(edge => edge.toSymbolPath);
    assert(!targets.some(target => target.endsWith('#unreachable')), 'both terminal branches retained unreachable suffix');
    assert(!targets.includes('branches.ts#sideEffect'), 'uninvoked callback fabricated a call');
    assert.strictEqual(targets.filter(target => target === 'branches.ts#second').length, 1, 'else invocation duplicated');
    assert(targets.includes('branches.ts#ready'), 'condition call omitted');
    const objectResult = sliceFlow({ ...params, entrySymbolPath: 'branches.ts#objectResult', opts: { includeExecutionSemantics: true } });
    assert.strictEqual(objectResult.edges.length, 0, 'returned callback fabricated a call');
    fs.writeFileSync(path.join(root, 'expressions.ts'), `
export function nested() { return wrap(inner()); }
export function conditional(flag: boolean) { return flag ? yes() : no(); }
export function shortCircuit(flag: boolean) { return flag && yes(); }
export function repeat() { for (; yes(); no()) { wrap(1); } }
function inner() { return 1; }
function wrap(value: number) { return value; }
function yes() { return 1; }
function no() { return 0; }
`);
    const nested = sliceFlow({ ...params, entrySymbolPath: 'expressions.ts#nested', opts: { includeExecutionSemantics: true } });
    assert.deepStrictEqual(nested.edges.filter(edge => edge.kind === 'resolved_cross_file').map(edge => edge.toSymbolPath), ['expressions.ts#inner', 'expressions.ts#wrap'], 'nested evaluation order lost');
    const conditional = sliceFlow({ ...params, entrySymbolPath: 'expressions.ts#conditional', opts: { includeExecutionSemantics: true } });
    assert.deepStrictEqual(conditional.edges.filter(edge => edge.kind === 'resolved_cross_file').map(edge => edge.toSymbolPath), ['expressions.ts#yes', 'expressions.ts#no']);
    const shortCircuit = sliceFlow({ ...params, entrySymbolPath: 'expressions.ts#shortCircuit', opts: { includeExecutionSemantics: true } });
    assert.strictEqual(shortCircuit.steps.find(step => step.symbolPath === 'shortCircuit' && step.kind === 'call').guardCondition, 'flag');
    const repeat = sliceFlow({ ...params, entrySymbolPath: 'expressions.ts#repeat', opts: { includeExecutionSemantics: true } });
    assert.deepStrictEqual(repeat.edges.filter(edge => edge.kind === 'resolved_cross_file').map(edge => edge.toSymbolPath), ['expressions.ts#yes', 'expressions.ts#wrap', 'expressions.ts#no']);
    assert.deepStrictEqual(branches.steps.filter(step => step.symbolPath === 'choose' && step.kind === 'call').map(step => step.guardCondition), [null, 'ready()', '!(ready())']);
    assert(branches.steps.find(step => step.symbolPath === 'choose' && step.kind === 'call').ordinal < branches.steps.find(step => step.symbolPath === 'choose' && step.kind === 'branch').ordinal, 'branch preceded condition evaluation');
    assert.deepStrictEqual(conditional.steps.filter(step => step.symbolPath === 'conditional' && step.kind === 'call').map(step => step.guardCondition), ['flag', '!(flag)']);
  } finally {
    fs.rmSync(root, { recursive: true, force: true });
  }
}

module.exports = { run };
if (require.main === module) run();
