'use strict';

const assert = require('assert');
const { scanSource } = require('../lib/scanner');

function run() {
  console.log('--- Test Suite: TypeScript Generic Functions, Classes & Methods ---');

  // TS-GEN-01: Generic async top-level function
  const code1 = 'export async function fetchData<T>(url: string): Promise<T> {\n  return {} as T;\n}';
  const scan1 = scanSource(code1);
  assert.strictEqual(scan1.topLevelFunctions.length, 1);
  assert.strictEqual(scan1.topLevelFunctions[0].name, 'fetchData');
  assert.strictEqual(scan1.topLevelFunctions[0].isAsync, true);

  // TS-GEN-02: Bounded generic function with multiple parameters
  const code2 = 'function merge<T extends object, U extends Record<string, any>>(a: T, b: U): T & U {\n  return Object.assign({}, a, b);\n}';
  const scan2 = scanSource(code2);
  assert.strictEqual(scan2.topLevelFunctions.length, 1);
  assert.strictEqual(scan2.topLevelFunctions[0].name, 'merge');
  assert.strictEqual(scan2.topLevelFunctions[0].isAsync, false);

  // TS-GEN-03: Generic arrow function with TSX trailing comma syntax (<T,>)
  const code3 = 'export const handleOrder = async <T,>(order: T): Promise<T> => {\n  return order;\n};';
  const scan3 = scanSource(code3);
  assert.strictEqual(scan3.topLevelFunctions.length, 1);
  assert.strictEqual(scan3.topLevelFunctions[0].name, 'handleOrder');
  assert.strictEqual(scan3.topLevelFunctions[0].isAsync, true);

  // TS-GEN-04: Multi-parameter generic arrow function
  const code4 = 'const submitPayload = <TReq, TRes extends BaseResponse>(req: TReq): TRes => {\n  return {} as TRes;\n};';
  const scan4 = scanSource(code4);
  assert.strictEqual(scan4.topLevelFunctions.length, 1);
  assert.strictEqual(scan4.topLevelFunctions[0].name, 'submitPayload');
  assert.strictEqual(scan4.topLevelFunctions[0].isAsync, false);

  // TS-GEN-05: Generic class declaration with extends and implements
  const code5 = `
export class OrderService<TEntity, TRepo extends Repository<TEntity>> extends BaseService<TEntity> implements IService<TEntity> {
  public async execute<TRequest, TResult>(req: TRequest): Promise<TResult> {
    return {} as TResult;
  }
  protected static async query<T = any>(sql: string): Promise<T[]> {
    return [];
  }
}
`;
  const scan5 = scanSource(code5);
  assert.strictEqual(scan5.classes.length, 1);
  assert.strictEqual(scan5.classes[0].name, 'OrderService');
  assert.strictEqual(scan5.classes[0].extendsName, 'BaseService');
  assert.strictEqual(scan5.classes[0].methods.length, 2);

  // Method 1
  assert.strictEqual(scan5.classes[0].methods[0].name, 'execute');
  assert.strictEqual(scan5.classes[0].methods[0].isAsync, true);

  // Method 2 (default type argument)
  assert.strictEqual(scan5.classes[0].methods[1].name, 'query');
  assert.strictEqual(scan5.classes[0].methods[1].isAsync, true);

  // Inner closures / functions inside class methods must not pollute top-level scope or class methods
  const codeScope = `
export class Service {
  exec() {
    function innerHelper() {
      return 1;
    }
    const innerArrow = () => {
      return 2;
    };
    return innerHelper() + innerArrow();
  }
}
`;
  const scanScope = scanSource(codeScope);
  assert.strictEqual(scanScope.classes.length, 1);
  assert.strictEqual(scanScope.classes[0].methods.length, 1);
  assert.strictEqual(scanScope.classes[0].methods[0].name, 'exec');
  assert.strictEqual(scanScope.topLevelFunctions.length, 0, 'Class inner functions must not pollute top-level scope');

  console.log('✓ All TypeScript Generic Functions, Classes & Methods tests passed.');
}

module.exports = { run };

if (require.main === module) {
  run();
}
