'use strict';

const assert = require('assert');
const fs = require('fs');
const path = require('path');
const os = require('os');
const { sliceFlow, extractStatements } = require('../lib/slice');

function run() {
  console.log('--- Test Suite: Switch and Loop AST Statement Extraction ---');

  // 1. Direct unit test: Switch statement extraction
  const switchSnippet = `
switch (action.type) {
  case 'SIGNUP':
    await this.authService.signUp(action.payload);
    this.state = { status: 'submitting' };
    break;
  case 'LOGOUT': {
    await this.authService.logout();
    break;
  }
  default:
    this.logger.warn('Unknown action');
}
`;
  const switchStmts = extractStatements(switchSnippet, 0, switchSnippet);

  assert.strictEqual(switchStmts.length, 8, 'Must extract 8 AST items from switch statement');
  assert.strictEqual(switchStmts[0].type, 'branch');
  assert.strictEqual(switchStmts[0].description, 'switch (action.type)');
  assert.strictEqual(switchStmts[1].type, 'branch');
  assert.strictEqual(switchStmts[1].description, "case 'SIGNUP':");
  assert.strictEqual(switchStmts[2].type, 'call');
  assert.strictEqual(switchStmts[2].receiver, 'authService');
  assert.strictEqual(switchStmts[2].methodName, 'signUp');
  assert.strictEqual(switchStmts[3].type, 'mutation');
  assert.strictEqual(switchStmts[4].type, 'branch');
  assert.strictEqual(switchStmts[4].description, "case 'LOGOUT':");
  assert.strictEqual(switchStmts[5].type, 'call');
  assert.strictEqual(switchStmts[5].receiver, 'authService');
  assert.strictEqual(switchStmts[5].methodName, 'logout');
  assert.strictEqual(switchStmts[6].type, 'branch');
  assert.strictEqual(switchStmts[6].description, 'default:');
  assert.strictEqual(switchStmts[7].type, 'call');
  assert.strictEqual(switchStmts[7].receiver, 'logger');
  assert.strictEqual(switchStmts[7].methodName, 'warn');

  // 2. Direct unit test: Loops extraction (for, while, do-while, unbraced)
  const loopSnippet = `
for (let i = 0; i < items.length; i++) {
  if (!items[i].valid) return;
  await this.orderRepo.save(items[i]);
  this.state = { count: i + 1 };
}
for (const elem of elements) {
  await this.emitter.emit(elem);
}
for await (const chunk of stream) {
  await this.sink.write(chunk);
}
while (queue.hasMore()) {
  await this.worker.consume();
}
do {
  await this.poller.poll();
} while (poller.isBusy());
for (const x of simpleList) await this.proc.handle(x);
`;
  const loopStmts = extractStatements(loopSnippet, 0, loopSnippet);

  assert(loopStmts.some(s => s.type === 'guard' && s.guardCondition === '!items[i].valid'), 'Guard in loop must be extracted');
  assert(loopStmts.some(s => s.type === 'call' && s.receiver === 'orderRepo' && s.methodName === 'save'), 'Call in loop must be extracted');
  assert(loopStmts.some(s => s.type === 'mutation' && s.description.includes('count: i + 1')), 'Mutation in loop must be extracted');
  assert(loopStmts.some(s => s.type === 'call' && s.receiver === 'emitter' && s.methodName === 'emit'), 'Call in for..of must be extracted');
  assert(loopStmts.some(s => s.type === 'call' && s.receiver === 'sink' && s.methodName === 'write'), 'Call in for await must be extracted');
  assert(loopStmts.some(s => s.type === 'call' && s.receiver === 'worker' && s.methodName === 'consume'), 'Call in while must be extracted');
  assert(loopStmts.some(s => s.type === 'call' && s.receiver === 'poller' && s.methodName === 'poll'), 'Call in do..while must be extracted');
  assert(loopStmts.some(s => s.type === 'call' && s.receiver === 'proc' && s.methodName === 'handle'), 'Call in unbraced loop must be extracted');

  // 3. Integration test: Multi-file traversal from inside switch and loops
  const tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), 'codeflow-controlflow-'));
  try {
    const srcDir = path.join(tmpDir, 'src');
    fs.mkdirSync(srcDir, { recursive: true });

    fs.writeFileSync(path.join(srcDir, 'Dispatcher.ts'), `
import { TargetHelper } from './TargetHelper';

export class Dispatcher {
  async dispatch(action: any) {
    switch (action.type) {
      case 'EXECUTE':
        for (const item of action.items) {
          await this.targetHelper.executeItem(item);
        }
        break;
      default:
        await this.targetHelper.defaultItem();
    }
  }
}
`, 'utf8');

    fs.writeFileSync(path.join(srcDir, 'TargetHelper.ts'), `
export class TargetHelper {
  async executeItem(item: any) {
    this.state = { executed: true };
  }
  async defaultItem() {
    this.state = { defaulted: true };
  }
}
`, 'utf8');

    const res = sliceFlow({
      repoRoot: tmpDir,
      candidateId: 'cand-ctrl-001',
      entrySymbolPath: 'src/Dispatcher.ts#Dispatcher.dispatch',
      opts: { maxDepth: 5 },
    });

    assert.strictEqual(res.candidateId, 'cand-ctrl-001');
    assert.strictEqual(res.language, 'typescript');
    assert.strictEqual(res.truncated, false);
    assert.strictEqual(res.visitedCycleDetected, false);

    // Verify cross-file edges resolved from within switch and loop
    const edgeTargets = res.edges.map(e => e.toSymbolPath);
    assert(edgeTargets.some(t => t.includes('TargetHelper.executeItem')), 'Must resolve TargetHelper.executeItem edge');
    assert(edgeTargets.some(t => t.includes('TargetHelper.defaultItem')), 'Must resolve TargetHelper.defaultItem edge');

    // Verify steps sliced inside target methods
    const targetSteps = res.steps.filter(s => s.symbolPath === 'TargetHelper.executeItem' || s.symbolPath === 'TargetHelper.defaultItem');
    assert(targetSteps.length >= 2, 'Must slice steps within TargetHelper');

    console.log('✓ All Switch and Loop AST Statement Extraction tests passed.');
  } finally {
    fs.rmSync(tmpDir, { recursive: true, force: true });
  }
}

module.exports = { run };

if (require.main === module) {
  run();
}
