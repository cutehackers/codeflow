'use strict';

const assert = require('assert');
const fs = require('fs');
const path = require('path');
const os = require('os');

const { scanSource, scanMethods, findMatchingBrace, countPrecedingBackslashes, isRegexStart } = require('../lib/scanner');
const { sliceFlow, extractStatements, tokenize, resolveCallTarget } = require('../lib/slice');
const { redactSecrets, secretPattern } = require('../lib/secret');

function runSuite1StringEscaping() {
  console.log('--- Adversarial Suite 1: Pathological String Escaping ---');

  // 1.1 Double backslash before closing double quote: "\\"
  const c1 = 'function test1() {\n  const s = "\\\\";\n  return 1;\n}';
  const s1 = scanSource(c1);
  assert.strictEqual(s1.topLevelFunctions.length, 1);
  assert.strictEqual(c1[s1.topLevelFunctions[0].bodyEnd], '}');

  // 1.2 Single quoted single backslash: '\\'
  const c2 = "function test2() {\n  const s = '\\\\';\n  return 2;\n}";
  const s2 = scanSource(c2);
  assert.strictEqual(s2.topLevelFunctions.length, 1);
  assert.strictEqual(c2[s2.topLevelFunctions[0].bodyEnd], '}');

  // 1.3 Odd backslashes before quote: "\\\"" and "\\\\\\\""
  const c3 = 'function test3() {\n  const s = "\\\\\\\"";\n  const s2 = "\\\"";\n  return 3;\n}';
  const s3 = scanSource(c3);
  assert.strictEqual(s3.topLevelFunctions.length, 1);
  assert.strictEqual(c3[s3.topLevelFunctions[0].bodyEnd], '}');

  // 1.4 Template literal with escaped interpolation: `\${"\\\\"}`
  const c4 = 'function test4() {\n  const s = `\\${"\\\\\\\\"};\n  return 4;\n}';
  const s4 = scanSource(c4);
  assert.strictEqual(s4.topLevelFunctions.length, 1);

  // 1.5 Deeply nested template literal with braces and quotes
  const c5 = `function test5() {
  const s = \`outer \${ { a: "}", b: \`inner \${ { c: "\\\\\\"" } }\` } } end\`;
  return 5;
}`;
  const s5 = scanSource(c5);
  assert.strictEqual(s5.topLevelFunctions.length, 1);
  assert.strictEqual(c5[s5.topLevelFunctions[0].bodyEnd], '}');

  // 1.6 Slicing statements containing pathological strings
  const bodyText = `
    const a = "\\\\\\\"";
    const b = '\\\\';
    const c = \`\\\${\\"\\\\\\\\"}\`;
    await this.storage.persist(a, b, c);
  `;
  const stmts = extractStatements(bodyText, 0, bodyText);
  assert(stmts.some(s => s.type === 'call' && s.methodName === 'persist'), 'Must extract persist call despite pathological strings');

  console.log('✓ Suite 1 passed.');
}

function runSuite2RegexBraces() {
  console.log('--- Adversarial Suite 2: Complex Regexes & Braces ---');

  // 2.1 Scanner findMatchingBrace with regex quantifiers, escaped braces, and closing braces
  const codeReg = `
class RegexSuite {
  testRegexes() {
    const r1 = /[a-z]{1,5}/;
    const r2 = /\\{[0-9]+\\}/g;
    const r3 = /}/;
    const r4 = /[{}]/g;
    const r5 = /[^}]/;
    return r1.test("abc");
  }
}
`;
  const scan = scanSource(codeReg);
  assert.strictEqual(scan.classes.length, 1);
  assert.strictEqual(scan.classes[0].methods.length, 1);
  assert.strictEqual(codeReg[scan.classes[0].methods[0].bodyEnd], '}');

  // 2.2 Slicer statement extraction with regexes in methods
  const tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), 'adv-regex-'));
  try {
    const srcDir = path.join(tmpDir, 'src');
    fs.mkdirSync(srcDir, { recursive: true });

    fs.writeFileSync(path.join(srcDir, 'RegexService.ts'), `
import { TargetService } from './TargetService';

export class RegexService {
  async process(input: string) {
    if (/[a-z]{1,5}/.test(input)) {
      await this.targetService.stepQuantifier();
    }
    const rEsc = /\\{[0-9]+\\}/g;
    await this.targetService.stepEscaped();
  }
}
`, 'utf8');

    fs.writeFileSync(path.join(srcDir, 'TargetService.ts'), `
export class TargetService {
  async stepQuantifier() {
    this.state = { quant: true };
  }
  async stepEscaped() {
    this.state = { escaped: true };
  }
}
`, 'utf8');

    const res = sliceFlow({
      repoRoot: tmpDir,
      candidateId: 'cand-adv-regex',
      entrySymbolPath: 'src/RegexService.ts#RegexService.process',
      opts: { maxDepth: 5 },
    });

    assert(res.steps.length >= 2, 'Must extract steps from RegexService');
    const edgeTargets = res.edges.map(e => e.toSymbolPath);
    assert(edgeTargets.some(t => t.includes('targetService.stepEscaped')), 'Must resolve targetService.stepEscaped edge');
  } finally {
    fs.rmSync(tmpDir, { recursive: true, force: true });
  }

  console.log('✓ Suite 2 passed.');
}

function runSuite3Generics() {
  console.log('--- Adversarial Suite 3: Multi-Level Nested Generics ---');

  // 3.1 3-level generic class: class Map<K extends string, V extends Promise<Array<T>>>
  const codeGen3 = `
export class DataMap<K extends string, V extends Promise<Array<T>>> {
  async get(key: K): Promise<V> {
    return {} as V;
  }
}
`;
  const scan3 = scanSource(codeGen3);
  assert.strictEqual(scan3.classes.length, 1, 'Must scan 3-level generic class');
  assert.strictEqual(scan3.classes[0].name, 'DataMap');
  assert.strictEqual(scan3.classes[0].methods.length, 1);
  assert.strictEqual(scan3.classes[0].methods[0].name, 'get');

  // 3.2 Complex generic async top-level function
  const codeFn = `
export async function transformStream<TIn extends Record<string, any>, TOut extends Promise<Array<TIn>>>(
  input: TIn
): Promise<TOut> {
  return {} as TOut;
}
`;
  const scanFn = scanSource(codeFn);
  assert.strictEqual(scanFn.topLevelFunctions.length, 1);
  assert.strictEqual(scanFn.topLevelFunctions[0].name, 'transformStream');
  assert.strictEqual(scanFn.topLevelFunctions[0].isAsync, true);

  // 3.3 Generic arrow function with TSX trailing comma
  const codeArrow = `
export const processItems = async <T,>(items: Array<T>): Promise<Array<T>> => {
  return items;
};
`;
  const scanArrow = scanSource(codeArrow);
  assert.strictEqual(scanArrow.topLevelFunctions.length, 1);
  assert.strictEqual(scanArrow.topLevelFunctions[0].name, 'processItems');
  assert.strictEqual(scanArrow.topLevelFunctions[0].isAsync, true);

  console.log('✓ Suite 3 passed.');
}

function runSuite4DeepDiamondDAG() {
  console.log('--- Adversarial Suite 4: Deep Diamond DAG (A -> B -> D, A -> C -> D, D -> E) ---');

  const tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), 'adv-dag-'));
  try {
    const srcDir = path.join(tmpDir, 'src');
    fs.mkdirSync(srcDir, { recursive: true });

    // Node A
    fs.writeFileSync(path.join(srcDir, 'NodeA.ts'), `
import { NodeB } from './NodeB';
import { NodeC } from './NodeC';

export class NodeA {
  async start() {
    await this.nodeB.branchB();
    await this.nodeC.branchC();
  }
}
`, 'utf8');

    // Node B -> D
    fs.writeFileSync(path.join(srcDir, 'NodeB.ts'), `
import { NodeD } from './NodeD';

export class NodeB {
  async branchB() {
    await this.nodeD.commonD();
  }
}
`, 'utf8');

    // Node C -> D
    fs.writeFileSync(path.join(srcDir, 'NodeC.ts'), `
import { NodeD } from './NodeD';

export class NodeC {
  async branchC() {
    await this.nodeD.commonD();
  }
}
`, 'utf8');

    // Node D -> E
    fs.writeFileSync(path.join(srcDir, 'NodeD.ts'), `
import { NodeE } from './NodeE';

export class NodeD {
  async commonD() {
    await this.nodeE.terminalE();
  }
}
`, 'utf8');

    // Node E (terminal leaf)
    fs.writeFileSync(path.join(srcDir, 'NodeE.ts'), `
export class NodeE {
  async terminalE() {
    this.state = { completed: true };
  }
}
`, 'utf8');

    const res = sliceFlow({
      repoRoot: tmpDir,
      candidateId: 'cand-deep-diamond',
      entrySymbolPath: 'src/NodeA.ts#NodeA.start',
      opts: { maxDepth: 5 },
    });

    assert.strictEqual(res.visitedCycleDetected, false, 'Deep Diamond DAG must NOT trigger cycle detection');
    assert.strictEqual(res.truncated, false, 'Deep Diamond DAG within maxDepth 5 must not be truncated');

    // Verify both branches slice D and E
    const dSteps = res.steps.filter(s => s.symbolPath === 'NodeD.commonD');
    const eSteps = res.steps.filter(s => s.symbolPath === 'NodeE.terminalE');
    assert.strictEqual(dSteps.length, 2, 'NodeD must be sliced along both branches (from B and from C)');
    assert.strictEqual(eSteps.length, 2, 'NodeE must be sliced along both branches (from D via B and C)');

    // Verify edges
    const edgeTargets = res.edges.map(e => e.toSymbolPath);
    assert(edgeTargets.some(t => t.includes('NodeB.branchB')));
    assert(edgeTargets.some(t => t.includes('NodeC.branchC')));
    assert(edgeTargets.some(t => t.includes('NodeD.commonD')));
    assert(edgeTargets.some(t => t.includes('NodeE.terminalE')));
  } finally {
    fs.rmSync(tmpDir, { recursive: true, force: true });
  }

  console.log('✓ Suite 4 passed.');
}

function runSuite5CircularRecursion() {
  console.log('--- Adversarial Suite 5: True Circular Recursion (A -> B -> C -> A) ---');

  const tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), 'adv-cycle-'));
  try {
    const srcDir = path.join(tmpDir, 'src');
    fs.mkdirSync(srcDir, { recursive: true });

    // Node A -> B
    fs.writeFileSync(path.join(srcDir, 'NodeA.ts'), `
import { NodeB } from './NodeB';
export class NodeA {
  async loopA() {
    await this.nodeB.loopB();
  }
}
`, 'utf8');

    // Node B -> C
    fs.writeFileSync(path.join(srcDir, 'NodeB.ts'), `
import { NodeC } from './NodeC';
export class NodeB {
  async loopB() {
    await this.nodeC.loopC();
  }
}
`, 'utf8');

    // Node C -> A (closes cycle)
    fs.writeFileSync(path.join(srcDir, 'NodeC.ts'), `
import { NodeA } from './NodeA';
export class NodeC {
  async loopC() {
    await this.nodeA.loopA();
  }
}
`, 'utf8');

    const res = sliceFlow({
      repoRoot: tmpDir,
      candidateId: 'cand-true-cycle-3',
      entrySymbolPath: 'src/NodeA.ts#NodeA.loopA',
      opts: { maxDepth: 5 },
    });

    assert.strictEqual(res.visitedCycleDetected, true, 'Circular cycle A -> B -> C -> A must set visitedCycleDetected = true');
    assert.strictEqual(res.truncated, false, 'Cycle terminated before hitting maxDepth must have truncated = false');
    assert.strictEqual(res.steps.length, 3, 'Must capture the 3 steps leading into the cycle');
  } finally {
    fs.rmSync(tmpDir, { recursive: true, force: true });
  }

  console.log('✓ Suite 5 passed.');
}

function runSuite6DeepRecursion() {
  console.log('--- Adversarial Suite 6: Deep Recursion Depth Bounds (> 5 levels) ---');

  const tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), 'adv-depth-'));
  try {
    const srcDir = path.join(tmpDir, 'src');
    fs.mkdirSync(srcDir, { recursive: true });

    const nodes = ['A', 'B', 'C', 'D', 'E', 'F', 'G', 'H', 'I'];
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

    // Default maxDepth = 5 on 9-node chain
    const res5 = sliceFlow({
      repoRoot: tmpDir,
      candidateId: 'cand-depth-5',
      entrySymbolPath: 'src/NodeA.ts#NodeA.stepA',
      opts: { maxDepth: 5 },
    });

    assert.strictEqual(res5.truncated, true, 'Exceeding depth 5 must set truncated = true');
    assert.strictEqual(res5.visitedCycleDetected, false);
    for (const edge of res5.edges) {
      assert(edge.depth <= 5, `Edge depth ${edge.depth} must be <= 5`);
    }

    // Custom maxDepth = 3
    const res3 = sliceFlow({
      repoRoot: tmpDir,
      candidateId: 'cand-depth-3',
      entrySymbolPath: 'src/NodeA.ts#NodeA.stepA',
      opts: { maxDepth: 3 },
    });

    assert.strictEqual(res3.truncated, true, 'Exceeding custom depth 3 must set truncated = true');
    for (const edge of res3.edges) {
      assert(edge.depth <= 3, `Edge depth ${edge.depth} must be <= 3`);
    }
    assert.strictEqual(res3.edges.length, 3, 'Must have exactly 3 edges at depth 1, 2, 3');
  } finally {
    fs.rmSync(tmpDir, { recursive: true, force: true });
  }

  console.log('✓ Suite 6 passed.');
}

function runSuite7SecretRedaction() {
  console.log('--- Adversarial Suite 7: Single-Gate Secret Redaction ---');

  const cases = [
    { in: 'const apiKey = "sk-live-12345";', expectedCount: 1 },
    { in: 'const API_KEY = "sk-live-12345";', expectedCount: 1 },
    { in: 'const api_key = "sk-live-12345";', expectedCount: 1 },
    { in: 'const api-key = "sk-live-12345";', expectedCount: 1 },
    { in: 'const apikey = "sk-live-12345";', expectedCount: 1 },
    { in: 'const secret = "secret_val_123";', expectedCount: 1 },
    { in: 'const token = "bearer_abc_123";', expectedCount: 1 },
    { in: 'const password = "mysecretpass";', expectedCount: 1 },
    // Negative tests: safe domain identifiers
    { in: 'const tokenCount = 42;', expectedCount: 0 },
    { in: 'const isPasswordValid = true;', expectedCount: 0 },
    { in: 'function tokenize(x) { return x; }', expectedCount: 0 },
    { in: 'if (user.hasSecretKey()) return;', expectedCount: 0 },
  ];

  for (const c of cases) {
    const res = redactSecrets(c.in);
    assert.strictEqual(res.count, c.expectedCount, `Expected count ${c.expectedCount} for: ${c.in}`);
    if (c.expectedCount > 0) {
      assert(res.text.includes('***REDACTED***'), `Must contain redacted placeholder for: ${c.in}`);
    }
  }

  console.log('✓ Suite 7 passed.');
}

function runFuzzHarness() {
  console.log('--- Adversarial Suite 8: Fuzz & Stress Harness ---');

  // Random lexical fuzz generator
  const tokens = [
    'class ', 'function ', 'async ', 'export ', 'default ', '{', '}', '(', ')', '[', ']',
    ';', ':', '=', '=>', 'import ', 'from ', '"string \\" \\\\ "', "'str \\' \\\\'",
    '`tmpl ${ { a: "}" } }`', '`\\${"\\\\"}', '// comment { } \n', '/* block comment { } */',
    '/[a-z]{1,5}/', '/\\{[0-9]+\\}/g', '/}/', '/[{}]/g', 'return ', 'await ', 'this.state = ',
    'this.service.call()', 'if (', ') return;', 'apiKey = "sk-123"', 'token = "tok-456"'
  ];

  const iterations = 100;
  for (let iter = 0; iter < iterations; iter++) {
    const len = 10 + Math.floor(Math.random() * 30);
    let sample = '';
    for (let j = 0; j < len; j++) {
      sample += tokens[Math.floor(Math.random() * tokens.length)] + ' ';
    }

    try {
      scanSource(sample);
      extractStatements(sample, 0, sample);
      redactSecrets(sample);
    } catch (err) {
      assert.fail(`Fuzz crashed on iteration ${iter}: ${err.message}\nInput:\n${sample}`);
    }
  }

  console.log(`✓ Fuzz harness executed ${iterations} iterations without unhandled exceptions.`);
}

function runSuite9BoundaryAndResilience() {
  console.log('--- Adversarial Suite 9: Boundary & Edge Case Resilience ---');

  // 9.1 Circular file-level imports (FileA imports FileB, FileB imports FileA)
  const tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), 'adv-circ-imp-'));
  try {
    const srcDir = path.join(tmpDir, 'src');
    fs.mkdirSync(srcDir, { recursive: true });

    fs.writeFileSync(path.join(srcDir, 'Alpha.ts'), `
import { Beta } from './Beta';
export class Alpha {
  async runAlpha() {
    await this.beta.doBeta();
  }
}
`, 'utf8');

    fs.writeFileSync(path.join(srcDir, 'Beta.ts'), `
import { Alpha } from './Alpha';
export class Beta {
  async doBeta() {
    this.state = { betaDone: true };
  }
}
`, 'utf8');

    const res = sliceFlow({
      repoRoot: tmpDir,
      candidateId: 'cand-circ-import',
      entrySymbolPath: 'src/Alpha.ts#Alpha.runAlpha',
      opts: { maxDepth: 5 },
    });

    assert.strictEqual(res.visitedCycleDetected, false, 'Circular file imports without circular method call must not trigger cycle');
    assert.strictEqual(res.steps.length, 2, 'Must slice both Alpha.runAlpha and Beta.doBeta');
  } finally {
    fs.rmSync(tmpDir, { recursive: true, force: true });
  }

  // 9.2 Generics depth boundary check (3 levels supported, 4+ levels boundary recorded)
  const codeGen3 = 'class Gen3<A extends B<C<D>>> { fn() {} }';
  const codeGen4 = 'class Gen4<A extends B<C<D<E>>>> { fn() {} }';
  const s3 = scanSource(codeGen3);
  const s4 = scanSource(codeGen4);
  assert.strictEqual(s3.classes.length, 1, '3-level generics must be parsed');
  // Record boundary behavior for 4-level generics
  console.log('  * Generics depth: 3-level supported =', s3.classes.length === 1, '| 4-level scanned =', s4.classes.length === 1);

  // 9.3 JSX / TSX element statement extraction
  const jsxCode = `
    const element = <div className="card"><button onClick={this.handleClick}>Click</button></div>;
    await this.service.loadData();
  `;
  const jsxStmts = extractStatements(jsxCode, 0, jsxCode);
  assert(jsxStmts.some(s => s.type === 'call' && s.methodName === 'loadData'), 'Must extract loadData call alongside JSX elements');

  // 9.4 Multi-line chained method calls
  const chainCode = `
    await this.client
      .createQueryBuilder()
      .where("id = :id")
      .execute();
  `;
  const chainStmts = extractStatements(chainCode, 0, chainCode);
  assert(chainStmts.length >= 1, 'Must extract chained method call statement');

  console.log('✓ Suite 9 passed.');
}

function run() {
  console.log('================================================================');
  console.log('  Running TypeScript Adapter Adversarial Empirical Test Suite   ');
  console.log('================================================================\n');

  runSuite1StringEscaping();
  runSuite2RegexBraces();
  runSuite3Generics();
  runSuite4DeepDiamondDAG();
  runSuite5CircularRecursion();
  runSuite6DeepRecursion();
  runSuite7SecretRedaction();
  runFuzzHarness();
  runSuite9BoundaryAndResilience();

  console.log('\n================================================================');
  console.log('  ALL Adversarial Empirical Tests Passed Successfully!          ');
  console.log('================================================================');
}

module.exports = { run };

if (require.main === module) {
  run();
}
