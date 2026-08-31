'use strict';

const assert = require('assert');
const fs = require('fs');
const path = require('path');
const os = require('os');
const { harvestCandidates } = require('../lib/harvest');
const { humanizeIdentifier } = require('../lib/humanize');

function run() {
  console.log('--- Test Suite: Harvest Determinism & Intent Signals ---');

  // 1. Humanization tests
  assert.strictEqual(humanizeIdentifier('submitOrder'), 'Submit order');
  assert.strictEqual(humanizeIdentifier('_onItemAdded'), 'Item added');
  assert.strictEqual(humanizeIdentifier('onCheckoutPressed'), 'Checkout pressed');
  assert.strictEqual(humanizeIdentifier('URLLoader'), 'Url loader');
  assert.strictEqual(humanizeIdentifier('handleClick'), 'Handle click');

  // 2. Deterministic harvest test
  const tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), 'codeflow-harvest-'));
  try {
    const srcDir = path.join(tmpDir, 'src');
    fs.mkdirSync(srcDir, { recursive: true });

    fs.writeFileSync(path.join(srcDir, 'LoginUseCase.ts'), `
export class LoginUseCase {
  async execute(email: string, pass: string) {
    return true;
  }
}
`, 'utf8');

    fs.writeFileSync(path.join(srcDir, 'OrderScreen.tsx'), `
export class OrderScreen {
  async handleSubmit() {
    console.log('submit order');
  }
}
`, 'utf8');

    const h1 = harvestCandidates({ repoRoot: tmpDir });
    const h2 = harvestCandidates({ repoRoot: tmpDir });

    assert.strictEqual(JSON.stringify(h1), JSON.stringify(h2), 'Consecutive harvests must be byte-identical');
    assert.strictEqual(h1.candidates.length, 2, 'Should harvest exactly 2 candidates from both files');

    // Verify candidate trigger classifications
    const loginCand = h1.candidates.find(c => c.entrySymbolPath.includes('LoginUseCase'));
    assert(loginCand, 'Login candidate must exist');
    assert.strictEqual(loginCand.triggerClass, 'use_case_invocation');
    assert.strictEqual(loginCand.markerKind, 'usecase_call');

    const orderCand = h1.candidates.find(c => c.entrySymbolPath.includes('OrderScreen'));
    assert(orderCand, 'Order candidate must exist');
    assert.strictEqual(orderCand.triggerClass, 'user_action');
    assert.strictEqual(orderCand.markerKind, 'route_callback');
  } finally {
    fs.rmSync(tmpDir, { recursive: true, force: true });
  }

  console.log('✓ All Harvest Determinism & Intent Signals tests passed.');
}

module.exports = { run };

if (require.main === module) {
  run();
}
