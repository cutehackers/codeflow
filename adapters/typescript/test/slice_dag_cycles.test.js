'use strict';

const assert = require('assert');
const fs = require('fs');
const path = require('path');
const os = require('os');
const { sliceFlow } = require('../lib/slice');

function run() {
  console.log('--- Test Suite: Diamond DAG vs Circular Cycle Detection ---');

  // TS-DAG-01: Diamond DAG Dependency (A calls B & C, both call D)
  // MUST NOT trigger visitedCycleDetected!
  const tmpDiamond = fs.mkdtempSync(path.join(os.tmpdir(), 'codeflow-dag-'));
  try {
    const srcDir = path.join(tmpDiamond, 'src');
    fs.mkdirSync(srcDir, { recursive: true });

    // Entry A
    fs.writeFileSync(path.join(srcDir, 'OrderController.ts'), `
import { PaymentModule } from './PaymentModule';
import { InventoryModule } from './InventoryModule';

export class OrderController {
  async submit() {
    await this.paymentModule.processPayment();
    await this.inventoryModule.reserveStock();
  }
}
`, 'utf8');

    // Node B
    fs.writeFileSync(path.join(srcDir, 'PaymentModule.ts'), `
import { AuditLogger } from './AuditLogger';

export class PaymentModule {
  async processPayment() {
    await this.auditLogger.logEvent();
  }
}
`, 'utf8');

    // Node C
    fs.writeFileSync(path.join(srcDir, 'InventoryModule.ts'), `
import { AuditLogger } from './AuditLogger';

export class InventoryModule {
  async reserveStock() {
    await this.auditLogger.logEvent();
  }
}
`, 'utf8');

    // Shared Node D
    fs.writeFileSync(path.join(srcDir, 'AuditLogger.ts'), `
export class AuditLogger {
  async logEvent() {
    this.state = { logged: true };
  }
}
`, 'utf8');

    const res = sliceFlow({
      repoRoot: tmpDiamond,
      candidateId: 'cand-diamond-001',
      entrySymbolPath: 'src/OrderController.ts#OrderController.submit',
      opts: { maxDepth: 5 },
    });

    assert.strictEqual(res.visitedCycleDetected, false, 'Diamond DAG must NOT trigger cycle detection');
    assert.strictEqual(res.truncated, false, 'Diamond DAG within depth 5 must not be truncated');

    // Verify resolved edges to both branches and shared node
    const edgeTargets = res.edges.map(e => e.toSymbolPath);
    assert(edgeTargets.some(t => t.includes('PaymentModule.processPayment')));
    assert(edgeTargets.some(t => t.includes('InventoryModule.reserveStock')));
    assert(edgeTargets.some(t => t.includes('AuditLogger.logEvent')));
  } finally {
    fs.rmSync(tmpDiamond, { recursive: true, force: true });
  }

  // TS-DAG-02: 2-Hop Mutual Recursion (A -> B -> A)
  const tmpCycle2 = fs.mkdtempSync(path.join(os.tmpdir(), 'codeflow-cycle2-'));
  try {
    const srcDir = path.join(tmpCycle2, 'src');
    fs.mkdirSync(srcDir, { recursive: true });

    fs.writeFileSync(path.join(srcDir, 'ClassA.ts'), `
import { ClassB } from './ClassB';
export class ClassA {
  async ping() {
    await this.classB.pong();
  }
}
`, 'utf8');

    fs.writeFileSync(path.join(srcDir, 'ClassB.ts'), `
import { ClassA } from './ClassA';
export class ClassB {
  async pong() {
    await this.classA.ping();
  }
}
`, 'utf8');

    const res = sliceFlow({
      repoRoot: tmpCycle2,
      candidateId: 'cand-cycle2-001',
      entrySymbolPath: 'src/ClassA.ts#ClassA.ping',
      opts: { maxDepth: 5 },
    });

    assert.strictEqual(res.visitedCycleDetected, true, 'Mutual recursion must set visitedCycleDetected = true');
  } finally {
    fs.rmSync(tmpCycle2, { recursive: true, force: true });
  }

  // TS-DAG-03: 1-Hop Self Recursion (A -> A)
  const tmpCycle1 = fs.mkdtempSync(path.join(os.tmpdir(), 'codeflow-cycle1-'));
  try {
    const srcDir = path.join(tmpCycle1, 'src');
    fs.mkdirSync(srcDir, { recursive: true });

    fs.writeFileSync(path.join(srcDir, 'TreeScanner.ts'), `
export class TreeScanner {
  async walk() {
    await this.walk();
  }
}
`, 'utf8');

    const res = sliceFlow({
      repoRoot: tmpCycle1,
      candidateId: 'cand-cycle1-001',
      entrySymbolPath: 'src/TreeScanner.ts#TreeScanner.walk',
      opts: { maxDepth: 5 },
    });

    assert.strictEqual(res.visitedCycleDetected, true, 'Self-recursion must set visitedCycleDetected = true');
  } finally {
    fs.rmSync(tmpCycle1, { recursive: true, force: true });
  }

  // TS-DAG-04: 3-Hop Recursion (A -> B -> C -> D -> B)
  const tmpCycle3 = fs.mkdtempSync(path.join(os.tmpdir(), 'codeflow-cycle3-'));
  try {
    const srcDir = path.join(tmpCycle3, 'src');
    fs.mkdirSync(srcDir, { recursive: true });

    fs.writeFileSync(path.join(srcDir, 'NodeA.ts'), `
import { NodeB } from './NodeB';
export class NodeA {
  async stepA() {
    await this.nodeB.stepB();
  }
}
`, 'utf8');

    fs.writeFileSync(path.join(srcDir, 'NodeB.ts'), `
import { NodeC } from './NodeC';
export class NodeB {
  async stepB() {
    await this.nodeC.stepC();
  }
}
`, 'utf8');

    fs.writeFileSync(path.join(srcDir, 'NodeC.ts'), `
import { NodeD } from './NodeD';
export class NodeC {
  async stepC() {
    await this.nodeD.stepD();
  }
}
`, 'utf8');

    fs.writeFileSync(path.join(srcDir, 'NodeD.ts'), `
import { NodeB } from './NodeB';
export class NodeD {
  async stepD() {
    await this.nodeB.stepB();
  }
}
`, 'utf8');

    const res = sliceFlow({
      repoRoot: tmpCycle3,
      candidateId: 'cand-cycle3-001',
      entrySymbolPath: 'src/NodeA.ts#NodeA.stepA',
      opts: { maxDepth: 5 },
    });

    assert.strictEqual(res.visitedCycleDetected, true, '3-hop cycle must set visitedCycleDetected = true');
  } finally {
    fs.rmSync(tmpCycle3, { recursive: true, force: true });
  }

  console.log('✓ All Diamond DAG vs Circular Cycle Detection tests passed.');
}

module.exports = { run };

if (require.main === module) {
  run();
}
