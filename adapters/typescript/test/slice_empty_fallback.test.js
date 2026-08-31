'use strict';

const assert = require('assert');
const fs = require('fs');
const path = require('path');
const os = require('os');
const { sliceFlow } = require('../lib/slice');

function run() {
  console.log('--- Test Suite: Empty / Stub Function Slicing & Schema minItems: 1 ---');

  const tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), 'codeflow-empty-test-'));
  try {
    const srcDir = path.join(tmpDir, 'src');
    fs.mkdirSync(srcDir, { recursive: true });

    // 1. Empty top-level function
    fs.writeFileSync(path.join(srcDir, 'emptyTop.ts'), 'export function emptyFunction() {}', 'utf8');

    const res1 = sliceFlow({
      repoRoot: tmpDir,
      candidateId: 'cand-empty-001',
      entrySymbolPath: 'src/emptyTop.ts#emptyFunction',
      opts: { maxDepth: 5 },
    });

    assert.strictEqual(res1.steps.length, 1, 'Empty function must emit exactly 1 fallback step');
    assert.strictEqual(res1.steps[0].ordinal, 1);
    assert.strictEqual(res1.steps[0].kind, 'call');
    assert.strictEqual(res1.steps[0].description, 'Empty function');
    assert.strictEqual(res1.steps[0].symbolPath, 'emptyFunction');
    assert.strictEqual(res1.steps[0].anchor.repoRelativePath, 'src/emptyTop.ts');
    assert.strictEqual(res1.steps[0].anchor.enclosingSymbolPath, 'emptyFunction');
    assert.strictEqual(res1.steps[0].anchor.byteRange.length, 2);
    assert.strictEqual(typeof res1.steps[0].anchor.fileHash, 'string');
    assert.strictEqual(res1.steps[0].anchor.fileHash.length, 64);
    assert.strictEqual(typeof res1.steps[0].anchor.spanHash, 'string');
    assert.strictEqual(res1.steps[0].anchor.spanHash.length, 64);
    assert.strictEqual(typeof res1.steps[0].anchor.canonicalAstFingerprint, 'string');
    assert.strictEqual(res1.steps[0].anchor.canonicalAstFingerprint.length, 64);

    // 2. Comments-only function body
    fs.writeFileSync(path.join(srcDir, 'commented.ts'), `
export function processNoOp() {
  // Line comment
  /* Block comment no-op */
}
`, 'utf8');

    const res2 = sliceFlow({
      repoRoot: tmpDir,
      candidateId: 'cand-empty-002',
      entrySymbolPath: 'src/commented.ts#processNoOp',
      opts: { maxDepth: 5 },
    });

    assert.strictEqual(res2.steps.length, 1);
    assert.strictEqual(res2.steps[0].ordinal, 1);
    assert.strictEqual(res2.steps[0].kind, 'call');
    assert.strictEqual(res2.steps[0].description, 'Process no op');
    assert.strictEqual(res2.steps[0].symbolPath, 'processNoOp');

    // 3. Class method with empty body
    fs.writeFileSync(path.join(srcDir, 'OrderService.ts'), `
export class OrderService {
  submitOrder() {}
}
`, 'utf8');

    const res3 = sliceFlow({
      repoRoot: tmpDir,
      candidateId: 'cand-empty-003',
      entrySymbolPath: 'src/OrderService.ts#OrderService.submitOrder',
      opts: { maxDepth: 5 },
    });

    assert.strictEqual(res3.steps.length, 1);
    assert.strictEqual(res3.steps[0].ordinal, 1);
    assert.strictEqual(res3.steps[0].kind, 'call');
    assert.strictEqual(res3.steps[0].description, 'Submit order');
    assert.strictEqual(res3.steps[0].symbolPath, 'OrderService.submitOrder');

    // 4. Stub with only local variable declarations (no calls or mutations)
    fs.writeFileSync(path.join(srcDir, 'stub.ts'), `
export function stubHandler() {
  const x = 1;
  const y = 2;
}
`, 'utf8');

    const res4 = sliceFlow({
      repoRoot: tmpDir,
      candidateId: 'cand-empty-004',
      entrySymbolPath: 'src/stub.ts#stubHandler',
      opts: { maxDepth: 5 },
    });

    assert.strictEqual(res4.steps.length, 1);
    assert.strictEqual(res4.steps[0].ordinal, 1);
    assert.strictEqual(res4.steps[0].kind, 'call');
    assert.strictEqual(res4.steps[0].description, 'Stub handler');

    // 5. Unresolved / missing entry method in file
    fs.writeFileSync(path.join(srcDir, 'nonexistent.ts'), `
export function otherMethod() {
  console.log('hi');
}
`, 'utf8');

    const res5 = sliceFlow({
      repoRoot: tmpDir,
      candidateId: 'cand-empty-005',
      entrySymbolPath: 'src/nonexistent.ts#missingMethod',
      opts: { maxDepth: 5 },
    });

    assert.strictEqual(res5.steps.length, 1);
    assert.strictEqual(res5.steps[0].ordinal, 1);
    assert.strictEqual(res5.steps[0].kind, 'call');
    assert.strictEqual(res5.steps[0].description, 'Missing method');
    assert.strictEqual(res5.steps[0].symbolPath, 'missingMethod');

    console.log('✓ All Empty / Stub Function Slicing tests passed.');
  } finally {
    fs.rmSync(tmpDir, { recursive: true, force: true });
  }
}

module.exports = { run };

if (require.main === module) {
  run();
}
