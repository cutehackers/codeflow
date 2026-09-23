'use strict';

const assert = require('assert');
const { extractExecutionStatements } = require('../lib/syntax_execution');

function run() {
  function analyze(body) {
    const source = `function execute(flag, nested) { ${body} }`;
    return extractExecutionStatements(source, 'flow.ts', source.indexOf('{') + 1, source.lastIndexOf('}'), () => []);
  }
  function paths(statements, description) {
    const target = statements.find(statement => statement.description === description);
    assert(target, `missing ${description}`);
    assert(Array.isArray(target.predecessorPaths), 'branch path evidence missing');
    return target.predecessorPaths.map(path => ({
      from: statements[path.index].description,
      conditions: path.conditions.map(condition => [statements[condition.index].description, condition.outcome]),
    }));
  }
  for (const expression of ['state.value = 1', 'state.count++', '++state.count', 'delete state.value', 'state.count += 2']) {
    const statements = analyze(`return flag && (${expression});`);
    const mutation = statements.find(statement => statement.type === 'mutation');
    assert(mutation, `missing mutation: ${expression}`);
    assert.strictEqual(mutation.rawText, expression);
    assert.deepStrictEqual(paths(statements, expression), [{ from: 'flag', conditions: [['flag', 'truthy']] }]);
  }
  for (const body of ['({[key()]: state.x} = source());', '[state.x = fallback()] = source();']) {
    const statements = analyze(body);
    assert.deepStrictEqual(statements.filter(step => step.type === 'call').map(step => step.rawText), ['source()'], 'unsupported destructuring must not invent key/default execution');
    assert(statements.find(step => step.type === 'mutation')?.controlFlowLimitation, 'destructuring uncertainty missing');
  }
  const construction = analyze('return flag ? new Service(prepare()) : null;');
  const constructor = construction.find(statement => statement.rawText === 'new Service(prepare())');
  assert(constructor && constructor.type === 'call', 'constructor execution missing');
  assert(constructor.controlFlowLimitation, 'unresolved constructor must retain an analysis boundary');
  assert.deepStrictEqual(paths(construction, constructor.description), [{ from: 'prepare()', conditions: [] }]);
  const assigned = analyze('return flag ? (state.value = first()) : (state.value = second());');
  assert.strictEqual(assigned.filter(statement => statement.type === 'mutation').length, 2);
  assert.deepStrictEqual(paths(assigned, 'state.value = first()'), [{ from: 'first()', conditions: [] }]);
  const branch = analyze('if (flag) yes(); else no(); after();');
  assert.deepStrictEqual(paths(branch, 'yes()'), [{ from: 'if (flag)', conditions: [['if (flag)', 'truthy']] }]);
  assert.deepStrictEqual(paths(branch, 'no()'), [{ from: 'if (flag)', conditions: [['if (flag)', 'falsy']] }]);
  assert.deepStrictEqual(paths(branch, 'after()'), [{ from: 'yes()', conditions: [] }, { from: 'no()', conditions: [] }]);

  const empty = analyze('if (flag) {} else {} after();');
  assert.deepStrictEqual(paths(empty, 'after()'), [
    { from: 'if (flag)', conditions: [['if (flag)', 'truthy']] },
    { from: 'if (flag)', conditions: [['if (flag)', 'falsy']] },
  ], 'parallel alternatives sharing endpoints must survive');

  const nested = analyze('if (flag) { if (nested) yes(); } after();');
  assert.deepStrictEqual(paths(nested, 'if (nested)'), [{ from: 'if (flag)', conditions: [['if (flag)', 'truthy']] }]);
  assert.deepStrictEqual(paths(nested, 'after()'), [
    { from: 'yes()', conditions: [] },
    { from: 'if (nested)', conditions: [['if (nested)', 'falsy']] },
    { from: 'if (flag)', conditions: [['if (flag)', 'falsy']] },
  ]);
  const early = analyze('if (flag) return 1; after();');
  assert.deepStrictEqual(paths(early, 'after()'), [{ from: 'if (flag)', conditions: [['if (flag)', 'falsy']] }]);

  const conditional = analyze('return flag ? yes() : no();');
  assert.deepStrictEqual(paths(conditional, 'yes()'), [{ from: 'flag', conditions: [['flag', 'truthy']] }]);
  assert.deepStrictEqual(paths(conditional, 'no()'), [{ from: 'flag', conditions: [['flag', 'falsy']] }]);
  assert.strictEqual(conditional.filter(statement => statement.type === 'return').length, 1);
  for (const [operator, evaluate, skip] of [['&&', 'truthy', 'falsy'], ['||', 'falsy', 'truthy'], ['??', 'nullish', 'non_nullish']]) {
    const statements = analyze(`return flag ${operator} yes();`);
    assert.deepStrictEqual(paths(statements, 'yes()'), [{from:'flag',conditions:[['flag',evaluate]]}], `${operator}: right operand condition lost`);
    const returned = statements.find(statement => statement.type === 'return');
    assert(paths(statements, returned.description).some(path => path.from === 'flag' && path.conditions[0]?.[1] === skip), `${operator}: skipped right operand path lost`);
    const decision = statements.find(statement => statement.type === 'branch');
    assert.strictEqual(decision.flowContext.statement.nodeKind, 'expression');
  }
}

module.exports = { run };
if (require.main === module) run();
