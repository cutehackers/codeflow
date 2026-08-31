'use strict';

const assert = require('assert');
const fs = require('fs');
const path = require('path');
const os = require('os');
const { sliceFlow, extractStatements } = require('../lib/slice');
const { sha256Hex, canonicalAstFingerprint, byteOffset } = require('../lib/sha256');

function run() {
  console.log('--- Test Suite: Multi-Line Statement Extraction & Accurate Byte Ranges ---');

  // 1. Test extractStatements directly with multi-line guards and calls
  const snippet = `
function processOrder() {
  if (
    !user.isValid ||
    !user.hasPermission('write')
  ) {
    return;
  }

  if (!email.includes('@')) {
    throw new Error("Invalid email address format");
  }

  this.state = {
    ...this.state,
    loading: true,
    error: null,
  };

  await this.orderRepository
    .saveOrder({
      id: order.id,
      items: order.items,
      total: order.total,
    });
}
`;

  const bodyStart = snippet.indexOf('{') + 1;
  const bodyEnd = snippet.lastIndexOf('}');
  const bodyText = snippet.substring(bodyStart, bodyEnd);
  const stmts = extractStatements(bodyText, bodyStart, snippet);

  assert.strictEqual(stmts.length, 4, 'Should extract exactly 4 statements');

  // Stmt 1: Multi-line guard
  assert.strictEqual(stmts[0].type, 'guard');
  assert(stmts[0].guardCondition.includes('!user.isValid'));
  assert(stmts[0].guardCondition.includes("!user.hasPermission('write')"));
  assert(stmts[0].rawText.startsWith('if ('));
  assert(stmts[0].rawText.endsWith('}'));

  // Stmt 2: Multi-line throw guard
  assert.strictEqual(stmts[1].type, 'guard');
  assert(stmts[1].guardCondition.includes("!email.includes('@')"));
  assert(stmts[1].rawText.includes('throw new Error'));

  // Stmt 3: Multi-line mutation
  assert.strictEqual(stmts[2].type, 'mutation');
  assert(stmts[2].rawText.includes('this.state ='));
  assert(stmts[2].rawText.includes('loading: true'));

  // Stmt 4: Multi-line chained call
  assert.strictEqual(stmts[3].type, 'call');
  assert.strictEqual(stmts[3].receiver, 'orderRepository');
  assert.strictEqual(stmts[3].methodName, 'saveOrder');

  // 2. Integration test using sliceFlow with temporary fixture repository
  const tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), 'codeflow-multiline-'));
  try {
    const srcDir = path.join(tmpDir, 'src');
    fs.mkdirSync(srcDir, { recursive: true });

    const orderTs = `
export class OrderService {
  async submitOrder(user: any, order: any) {
    if (
      !user.isValid ||
      !user.hasPermission('write')
    ) {
      return;
    }

    if (order.amount <= 0) {
      throw new Error(
        'Amount must be positive'
      );
    }

    this.state = {
      loading: true,
    };

    await this.orderRepository.saveOrder({
      id: order.id,
      total: order.amount,
    });
  }
}
`;
    fs.writeFileSync(path.join(srcDir, 'OrderService.ts'), orderTs, 'utf8');

    const res = sliceFlow({
      repoRoot: tmpDir,
      candidateId: 'cand-multiline-001',
      entrySymbolPath: 'src/OrderService.ts#OrderService.submitOrder',
      opts: { maxDepth: 5 },
    });

    assert.strictEqual(res.candidateId, 'cand-multiline-001');
    assert.strictEqual(res.language, 'typescript');
    assert.strictEqual(res.truncated, false);
    assert.strictEqual(res.visitedCycleDetected, false);
    assert.strictEqual(res.steps.length, 4);

    // Verify Steps
    assert.strictEqual(res.steps[0].kind, 'guard');
    assert.strictEqual(res.steps[1].kind, 'guard');
    assert.strictEqual(res.steps[2].kind, 'mutation');
    assert.strictEqual(res.steps[3].kind, 'effect'); // Boundary effect (Repository suffix)
    assert.strictEqual(res.steps[3].effectTarget, 'orderRepository.saveOrder');

    // Verify Anchor byte ranges and span hashes
    const fileContent = fs.readFileSync(path.join(srcDir, 'OrderService.ts'), 'utf8');
    const fileBytes = Buffer.from(fileContent, 'utf8');

    for (const step of res.steps) {
      const [start, end] = step.anchor.byteRange;
      assert(start >= 0 && end > start && end <= fileBytes.length);

      const sliceStr = fileBytes.subarray(start, end).toString('utf8');
      assert.strictEqual(step.anchor.spanHash, sha256Hex(sliceStr));
      assert.strictEqual(step.anchor.canonicalAstFingerprint, canonicalAstFingerprint(sliceStr));
    }

    // Verify boundary_call edge
    assert.strictEqual(res.edges.length, 1);
    assert.strictEqual(res.edges[0].kind, 'boundary_call');
    assert.strictEqual(res.edges[0].depth, 1);
    assert.strictEqual(res.edges[0].stepOrdinal, 4);
  } finally {
    fs.rmSync(tmpDir, { recursive: true, force: true });
  }

  console.log('✓ All Multi-Line Statement Extraction & Accurate Byte Ranges tests passed.');
}

module.exports = { run };

if (require.main === module) {
  run();
}
