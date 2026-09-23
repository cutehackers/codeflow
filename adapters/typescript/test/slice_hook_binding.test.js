'use strict';

const assert = require('assert');
const fs = require('fs');
const os = require('os');
const path = require('path');
const { sliceFlow } = require('../lib/slice');

function run() {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'codeflow-hook-binding-'));
  try {
    fs.writeFileSync(path.join(root, 'hooks.ts'), `
export function useOther() {
  const calculate = () => { return 999; };
  return { calculate };
}
export function useCart() {
  const total = () => { return 42; };
  const clear = () => { throw new Error('not invoked'); };
  return { calculate: total, clear };
}`);
    const cases = [
      ['destructuring alias', 'const { calculate: compute } = useCart();', 'compute()', true],
      ['object receiver', 'const cart = useCart();', 'cart.calculate()', true],
      ['sibling scope', 'const { calculate } = useCart();', 'calculate()', true],
      ['unreturned function', 'const { clear } = useOther();', 'clear()', false],
    ];
    for (const [name, binding, call, resolves] of cases) {
      fs.writeFileSync(path.join(root, 'page.ts'), `
import { useOther, useCart } from './hooks';
function OtherPage() {
  const { calculate } = useOther();
  const onOther = () => { return calculate(); };
}
export function Page() {
  ${binding}
  const onCheckout = () => { const result = ${call}; return result; };
}`);
      const result = sliceFlow({ repoRoot: root, candidateId: 'checkout', entrySymbolPath: 'page.ts#Page.onCheckout' });
      const targets = result.edges.filter(edge => edge.resolutionStatus === 'resolved').map(edge => edge.toSymbolPath);
      assert.strictEqual(targets.includes('hooks.ts#useCart.total'), resolves, name + ': ' + JSON.stringify(targets));
      assert(!result.steps.some(step => step.symbolPath === 'useCart' || step.symbolPath === 'useOther'), name + ': hook body included');
      assert(!result.steps.some(step => step.symbolPath.endsWith('.clear')), name + ': unrelated callback included');
    }
    const shadowCases = [
      ['callback parameter', 'const { calculate } = useCart();', '(calculate)', '', ''],
      ['hook parameter', 'const { calculate } = useCart();', '()', '', 'useCart'],
      ['reassignment', 'let { calculate } = useCart();', '()', 'calculate = () => 99;', ''],
      ['destructured parameter', 'const { calculate } = useCart();', '({calculate}: {calculate: () => number})', '', ''],
      ['return type annotation', 'const { calculate } = useCart();', '(calculate: () => number): (() => number)', '', ''],
      ['local hook declaration', 'function useCart() { return {calculate: () => 99}; } const { calculate } = useCart();', '()', '', ''],
    ];
    for (const [name, binding, parameters, prefix, pageParameters] of shadowCases) {
      fs.writeFileSync(path.join(root, 'page.ts'), `
import { useCart } from './hooks';
export function Page(${pageParameters}) {
  ${binding}
  const onCheckout = ${parameters} => { ${prefix} const result = calculate(); return result; };
}`);
      const result = sliceFlow({ repoRoot: root, candidateId: 'checkout', entrySymbolPath: 'page.ts#Page.onCheckout' });
      assert(!result.edges.some(edge => edge.toSymbolPath === 'hooks.ts#useCart.total' && edge.resolutionStatus === 'resolved'), name + ': false hook target');
    }
    fs.writeFileSync(path.join(root, 'page.ts'), `
export function Page() {
  const calculate = () => { return 42; };
  const onCheckout = () => { const result = calculate(); return result; };
}`);
    const local = sliceFlow({ repoRoot: root, candidateId: 'checkout', entrySymbolPath: 'page.ts#Page.onCheckout' });
    assert(local.edges.some(edge => edge.toSymbolPath === 'page.ts#Page.calculate' && edge.resolutionStatus === 'resolved'), 'local callable regressed');
    fs.writeFileSync(path.join(root, 'page.ts'), `
export function Page() {
  const calculate: () => number = () => { return materialize(42); };
  const onCheckout = () => { const result = calculate(); return result; };
}
function Other() { const calculate = () => { return materialize(99); }; }
`);
    const typed = sliceFlow({ repoRoot: root, candidateId: 'checkout', entrySymbolPath: 'page.ts#Page.onCheckout' });
    assert(typed.steps.some(step => step.symbolPath === 'Page.calculate' && step.description.includes('42')), 'typed local implementation missing');
    assert(!typed.steps.some(step => step.symbolPath === 'Other.calculate'), 'typed local selected sibling implementation');
    fs.writeFileSync(path.join(root, 'page.ts'), `
import { useCart } from './hooks';
export function Page() {
  const cart = useCart();
  const onCheckout = () => { cart.calculate = () => 99; const result = cart.calculate(); return result; };
}`);
    const mutation = sliceFlow({ repoRoot: root, candidateId: 'checkout', entrySymbolPath: 'page.ts#Page.onCheckout' });
    assert(!mutation.edges.some(edge => edge.toSymbolPath === 'hooks.ts#useCart.total' && edge.resolutionStatus === 'resolved'), 'object member write ignored');
    fs.writeFileSync(path.join(root, 'page.ts'), `
import { useCart } from './hooks';
export function Page() {
  const cart = useCart(); const alias = cart;
  const onCheckout = () => { alias.calculate = () => 99; const result = cart.calculate(); return result; };
}`);
    const aliasMutation = sliceFlow({ repoRoot: root, candidateId: 'checkout', entrySymbolPath: 'page.ts#Page.onCheckout' });
    assert(!aliasMutation.edges.some(edge => edge.toSymbolPath === 'hooks.ts#useCart.total' && edge.resolutionStatus === 'resolved'), 'alias member write ignored');
    fs.writeFileSync(path.join(root, 'page.ts'), `
export function Page() {
  function calculate() { return materialize(42); }
  const onCheckout = () => { calculate = () => 99; const result = calculate(); return result; };
}`);
    const functionMutation = sliceFlow({ repoRoot: root, candidateId: 'checkout', entrySymbolPath: 'page.ts#Page.onCheckout' });
    assert(!functionMutation.edges.some(edge => edge.toSymbolPath === 'page.ts#Page.calculate' && edge.resolutionStatus === 'resolved'), 'rejected function fell back to name matching');
    fs.writeFileSync(path.join(root, 'hooks.ts'), `
export function useCart() {
  function useCallback(fn) { return () => 99; }
  const total = useCallback(() => { return materialize(42); }, []);
  return { calculate: total };
}`);
    fs.writeFileSync(path.join(root, 'page.ts'), `
import { useCart } from './hooks';
export function Page() {
  const {calculate} = useCart();
  const onCheckout = () => { const result = calculate(); return result; };
}`);
    const fakeCallback = sliceFlow({ repoRoot: root, candidateId: 'checkout', entrySymbolPath: 'page.ts#Page.onCheckout' });
    assert(!fakeCallback.edges.some(edge => edge.toSymbolPath === 'hooks.ts#useCart.total' && edge.resolutionStatus === 'resolved'), 'local wrapper mistaken for React useCallback');
    fs.writeFileSync(path.join(root, 'service.ts'), `
class Service { execute() { return materialize('wrong'); } }
class Actual { execute() { return materialize('actual'); } }
export { Actual as Service };
`);
    fs.writeFileSync(path.join(root, 'page.ts'), `
import { Service } from './service';
export function Page(service: Service) {
  const onCheckout = () => { const result = service.execute(); return result; };
}`);
    const exportAlias = sliceFlow({ repoRoot: root, candidateId: 'checkout', entrySymbolPath: 'page.ts#Page.onCheckout' });
    assert(!exportAlias.edges.some(edge => edge.toSymbolPath === 'service.ts#Service.execute' && edge.resolutionStatus === 'resolved'), 'private class mistaken for exported alias');
  } finally {
    fs.rmSync(root, { recursive: true, force: true });
  }
}

module.exports = { run };
if (require.main === module) run();
