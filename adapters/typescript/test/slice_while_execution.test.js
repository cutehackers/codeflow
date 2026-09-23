'use strict';
const assert = require('assert');
const { sliceFlow } = require('../lib/slice');
function run() {
 const source = `function check(value) { return value < 2; }
export function retry(count) { while (count < 2) { count++; if (count === 1) return count; } return -1; }
export function reevaluate(count) { while (check(count)) { count++; } return count; }
export function empty(flag) { while (flag) {} return 1; }
export function interrupted(flag) { while (flag) { break; } return 1; }
export function next(count) { while (count < 3) { count++; if (count === 1) continue; break; } return count; }
export function labeled(flag) { outer: while (flag) { break outer; } return 1; }
export function nested(outer, inner) { while (outer) { while (inner) { break; } } return 1; }
export function nestedContinue(outer, inner) { while (outer) { while (inner) { continue; } outer = false; } return 1; }
export function completed(flag) { while (flag) { return 1; } return 2; }
`;
 const analyze = name => sliceFlow({repoRoot:'/snapshot',candidateId:'cand-whileflow',snapshot:{snapshotId:'snapshot-while',files:{'flow.ts':source}},entrySymbolPath:`flow.ts#${name}`,opts:{includeExecutionSemantics:true}});
 const retry = analyze('retry');
 const step = (result,text) => { const matches=result.steps.filter(s=>s.description===text); assert.strictEqual(matches.length,1,text);return matches[0]; };
 const header = step(retry,'while (count < 2)'), increment=step(retry,'count++'), final=step(retry,'return -1;');
 assert(retry.edges.some(e=>e.stepOrdinal===header.ordinal && e.targetStepOrdinal===increment.ordinal && e.conditions?.[0].outcome==='truthy'));
 assert(retry.edges.some(e=>e.stepOrdinal===header.ordinal && e.targetStepOrdinal===final.ordinal && e.conditions?.[0].outcome==='falsy'));
 const back=retry.edges.filter(e=>e.kind==='loop_back');
 assert.strictEqual(back.length,1);
 assert.strictEqual(back[0].targetStepOrdinal,header.ordinal);
 assert.strictEqual(back[0].conditions[0].outcome,'falsy');
 assert.strictEqual(retry.steps.find(s=>s.ordinal===back[0].stepOrdinal).kind,'guard');
 const reevaluate=analyze('reevaluate');
 assert.strictEqual(reevaluate.edges.find(e=>e.kind==='loop_back').targetStepOrdinal,step(reevaluate,'check(count)').ordinal);
 const empty=analyze('empty');
 const self=empty.edges.find(e=>e.kind==='loop_back');
 assert.strictEqual(self.stepOrdinal,self.targetStepOrdinal);
 assert.strictEqual(self.conditions[0].outcome,'truthy');
 assert(empty.edges.some(e=>e.conditions?.[0].outcome==='falsy' && e.kind==='control_flow'));
 assert(!analyze('completed').edges.some(e=>e.kind==='loop_back'),'return cannot loop');
 assert(!analyze('interrupted').edges.some(e=>e.kind==='loop_back'),'unsupported break remains unknown');
 const nested=analyze('nested');
 const innerBreak=nested.steps.find(s=>s.kind==='break');
 assert(innerBreak);
 const outerHeader=step(nested,'while (outer)');
 assert(nested.edges.some(e=>e.kind==='loop_exit_back' && e.stepOrdinal===innerBreak.ordinal && e.targetStepOrdinal===outerHeader.ordinal),'inner break must reevaluate outer condition');
 const nestedContinue=analyze('nestedContinue');
 const innerContinue=nestedContinue.steps.find(s=>s.kind==='continue');
 assert(nestedContinue.edges.some(e=>e.kind==='loop_back' && e.stepOrdinal===innerContinue.ordinal && e.targetStepOrdinal===step(nestedContinue,'while (inner)').ordinal),'continue must stay in nearest loop');
 const interrupted=analyze('interrupted');
 const exit=interrupted.edges.find(e=>e.kind==='loop_exit');
 assert(exit);
 assert.strictEqual(interrupted.steps.find(s=>s.ordinal===exit.stepOrdinal).kind,'break');
 assert.strictEqual(exit.targetStepOrdinal,step(interrupted,'return 1;').ordinal);
 assert(!interrupted.edges.some(e=>e.unresolvedReason));
 const next=analyze('next');
 const continued=next.steps.find(s=>s.kind==='continue');
 assert(continued);
 assert(next.edges.some(e=>e.kind==='loop_back' && e.stepOrdinal===continued.ordinal && e.targetStepOrdinal===step(next,'while (count < 3)').ordinal));
 assert(!next.edges.some(e=>e.stepOrdinal===continued.ordinal && e.kind==='control_flow'));
 assert(analyze('labeled').edges.some(e=>e.unresolvedReason));
}
module.exports={run};
if(require.main===module)run();
