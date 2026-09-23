'use strict';
const assert = require('assert');
const fs = require('fs');
const os = require('os');
const path = require('path');
const { sliceFlow } = require('../lib/slice');
const { scanSyntaxFunctions } = require('../lib/syntax_functions');

function run() {
 const root = fs.mkdtempSync(path.join(os.tmpdir(), 'codeflow-expression-body-'));
 const source = `export const price = (value: number) => value * 2;
export const choose = (ready: boolean) => ready ? price(2) : price(3);
export const record = (state: any) => state.count++;
export const factory = () => () => hidden();
export const object = () => ({ label: '주문', total: price(4) });
export const service = { calculate: (value: number) => price(value) };
export function start() { return choose(true); }
function hidden() { return 99; }
`;
 try {
  fs.writeFileSync(path.join(root,'flow.ts'),source);
  assert(scanSyntaxFunctions(source,'flow.ts').functions.some(fn=>fn.name==='price'),'expression body not indexed');
  const analyze = name => sliceFlow({repoRoot:root,candidateId:'cand-expression',entrySymbolPath:`flow.ts#${name}`,snapshot:{snapshotId:'expression-snapshot'},opts:{includeExecutionSemantics:true}});
  for (const [name, expression] of [['price','value * 2'],['choose','ready ? price(2) : price(3)'],['object',"({ label: '주문', total: price(4) })"],['service.calculate','price(value)']]) {
   const result = analyze(name);
   const returned = result.steps.find(step=>step.kind==='return' && step.symbolPath===name);
   assert(returned,`${name}: implicit return missing`);
   assert.strictEqual(Buffer.from(source).subarray(...returned.anchor.byteRange).toString(),expression);
   assert(returned.flowContext,`${name}: exact expression context missing`);
   assert.strictEqual(returned.flowContext.statement.nodeKind,'expression');
  }
  const chosen=analyze('choose');
  const branch=chosen.steps.find(step=>step.kind==='branch' && step.symbolPath==='choose');
  assert(branch,'conditional decision missing');
  const outcomes=chosen.edges.filter(edge=>edge.kind==='control_flow' && edge.stepOrdinal===branch.ordinal).map(edge=>edge.conditions[0].outcome).sort();
  assert.deepStrictEqual(outcomes,['falsy','truthy']);
  const record=analyze('record');
  assert.deepStrictEqual(record.steps.filter(step=>step.symbolPath==='record').map(step=>step.kind),['mutation','return']);
  const factory=analyze('factory');
  assert.strictEqual(factory.steps.length,1,'returned closure executed prematurely');
  assert.strictEqual(factory.steps[0].kind,'return');
  const caller=analyze('start');
  assert(caller.steps.some(step=>step.symbolPath==='choose' && step.kind==='return'),'called expression body omitted');
  assert(caller.steps.filter(step=>step.symbolPath==='price' && step.kind==='return').length===2,'separate calls lost');
 } finally {fs.rmSync(root,{recursive:true,force:true});}
}
module.exports={run};
if(require.main===module)run();
