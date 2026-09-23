'use strict';
const assert = require('assert');
const crypto = require('crypto');
const { sliceFlow } = require('../lib/slice');
const { CAPABILITIES } = require('../lib/protocol');
function run() {
  const source = `export function checkout(flag: boolean) {\n  const label = '한글 😀';\n  if (flag /* 조건 */ ) {\n    return calculate(label);\n  }\n  return '취소';\n}\nfunction calculate(value: string) { return value; }`;
  const params = { repoRoot: '/snapshot', candidateId: 'context', entrySymbolPath: 'entry.ts#checkout', snapshot: {snapshotId: 'snapshot-context', files: {'entry.ts': source}}, opts: {includeExecutionSemantics: true} };
  const result = sliceFlow(params);
  assert.strictEqual(CAPABILITIES.flowContext, true);
  const call = result.steps.find(step => step.description === 'calculate(label)');
  const returned = result.steps.find(step => step.description === 'return calculate(label);');
  const branch = result.steps.find(step => step.kind === 'branch');
  for (const step of [call, returned, branch]) {
    assert(step?.flowContext, `missing context for ${step?.description}`);
    const context=step.flowContext;
    assert.deepStrictEqual(context.statement.byteRange,step.anchor.byteRange);
    assert.strictEqual(context.sourceHash,crypto.createHash('sha256').update(source).digest('hex'));
    assert.strictEqual(context.snapshotId,'snapshot-context');
    assert(context.callable.signatureByteRange[1] <= step.anchor.byteRange[0]);
    const text=Buffer.from(source).subarray(...context.statement.byteRange).toString();
    assert(text.includes(step===branch ? '/* 조건 */ )' : step.description));
  }
  assert.strictEqual(call.flowContext.statement.nodeKind,'expression');
  assert.strictEqual(returned.flowContext.statement.nodeKind,'statement');
  assert.strictEqual(branch.flowContext.statement.nodeKind,'control_header');
  assert.strictEqual(call.flowContext.structuralContext.nodeKind,'condition');
  const nested=result.steps.find(step=>step.symbolPath==='calculate');
  assert.strictEqual(nested.flowContext.structuralContext.status,'none');
  assert.strictEqual(nested.flowContext.statement.lineRange[0],8);
  for (const header of ['while (flag /* test */ )', 'for (let i=0; flag; i++)', 'for (const item of items /* source */ )', 'switch (flag /* test */ )']) {
    const loopSource = `export function checkout() { ${header} ${header.startsWith('switch') ? '{ case true: act(); }' : '{ act(); }'} }`;
    const loop=sliceFlow({...params,snapshot:{snapshotId:'snapshot-context',files:{'entry.ts':loopSource}}});
    const gate=loop.steps.find(step=>step.kind==='branch');
    assert.strictEqual(gate?.flowContext?.statement.nodeKind,'control_header',header);
    assert.strictEqual(Buffer.from(loopSource).subarray(...gate.anchor.byteRange).toString(),header);
  }
  const broken=sliceFlow({...params,snapshot:{snapshotId:'snapshot-context',files:{'entry.ts':'export function checkout() { if (ready { execute(); } }'}}});
  assert(broken.steps.every(step=>!step.flowContext),'parse errors declared exact context');
  const missingSnapshot=sliceFlow({...params,snapshot:{files:{'entry.ts':source}}});
  assert(missingSnapshot.steps.every(step=>!step.flowContext),'context without snapshot identity');
  const legacy=sliceFlow({...params,opts:{}});
  assert(legacy.steps.every(step=>!step.flowContext));
}
module.exports={run};
if(require.main===module)run();
