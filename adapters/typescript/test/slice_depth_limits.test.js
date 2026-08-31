'use strict';

const assert = require('assert');
const fs = require('fs');
const path = require('path');
const os = require('os');
const { sliceFlow } = require('../lib/slice');

function run() {
  console.log('--- Test Suite: Recursion Depth Limits (maxDepth = 5) ---');

  // Set up 7-node chain repository: A -> B -> C -> D -> E -> F -> G
  const tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), 'codeflow-depth-'));
  try {
    const srcDir = path.join(tmpDir, 'src');
    fs.mkdirSync(srcDir, { recursive: true });

    const nodes = ['A', 'B', 'C', 'D', 'E', 'F', 'G'];
    for (let i = 0; i < nodes.length; i++) {
      const cur = nodes[i];
      const next = nodes[i + 1];
      if (next) {
        fs.writeFileSync(path.join(srcDir, `Node${cur}.ts`), `
import { Node${next} } from './Node${next}';
export class Node${cur} {
  async step${cur}() {
    await this.node${next}.step${next}();
  }
}
`, 'utf8');
      } else {
        fs.writeFileSync(path.join(srcDir, `Node${cur}.ts`), `
export class Node${cur} {
  async step${cur}() {
    this.state = { done: true };
  }
}
`, 'utf8');
      }
    }

    // TS-DEP-03: Full chain across 7 nodes with default maxDepth = 5
    const res7 = sliceFlow({
      repoRoot: tmpDir,
      candidateId: 'cand-depth-7',
      entrySymbolPath: 'src/NodeA.ts#NodeA.stepA',
      opts: { maxDepth: 5 },
    });

    assert.strictEqual(res7.truncated, true, 'Traversing past depth 5 must set truncated = true');
    assert.strictEqual(res7.visitedCycleDetected, false);

    // Verify all edge depths are <= 5
    for (const edge of res7.edges) {
      assert(edge.depth <= 5, `Edge depth ${edge.depth} must be <= 5`);
    }

    // TS-DEP-04: Shallow chain with custom maxDepth = 2
    const resShallow = sliceFlow({
      repoRoot: tmpDir,
      candidateId: 'cand-depth-2',
      entrySymbolPath: 'src/NodeA.ts#NodeA.stepA',
      opts: { maxDepth: 2 },
    });

    assert.strictEqual(resShallow.truncated, true, 'Exceeding custom maxDepth = 2 must set truncated = true');
    for (const edge of resShallow.edges) {
      assert(edge.depth <= 2, `Edge depth ${edge.depth} must be <= 2`);
    }

    // TS-DEP-01: Entrypoint at NodeD (depth: D -> E -> F -> G = 3 hops <= 5)
    const resWithin = sliceFlow({
      repoRoot: tmpDir,
      candidateId: 'cand-depth-within',
      entrySymbolPath: 'src/NodeD.ts#NodeD.stepD',
      opts: { maxDepth: 5 },
    });

    assert.strictEqual(resWithin.truncated, false, 'Chain within depth limit must have truncated = false');
    assert.strictEqual(resWithin.visitedCycleDetected, false);
  } finally {
    fs.rmSync(tmpDir, { recursive: true, force: true });
  }

  console.log('✓ All Recursion Depth Limits tests passed.');
}

module.exports = { run };

if (require.main === module) {
  run();
}
