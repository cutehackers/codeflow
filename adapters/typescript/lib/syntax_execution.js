'use strict';

const ts = require('../vendor/typescript/typescript');
const { parseSource } = require('./syntax_functions');
const { syntaxContextReader, controlHeaderEnd, doConditionHeaderStart } = require('./syntax_context');

// Traverse statement syntax before applying legacy leaf classification. Parsing
// a whole block as a leaf can mistake an uninvoked callback for a call site.
function extractExecutionStatements(code, fileName, bodyStart, bodyEnd, extractStatements) {
  const tree = parseSource(code, fileName);
  const statements = [];
  let frontier = [];
  const controlContexts = [];
  const catchContexts = [];
  const catchCallContexts = [];
  const finallyContexts = [];
  let tryDepth = 0;
  function emit(statement) {
    statement.flowContext = contextFor(statement);
    statement.predecessorPaths = frontier.map(path => ({ index: path.index, loopBack: path.loopBack, loopReentry: path.loopReentry, loopExit: path.loopExit, switchExit: path.switchExit, parallel: path.parallel, failure: path.failure, finally: path.finally, terminal: path.terminal, conditions: path.conditions.map(condition => ({ ...condition })) }));
    statement.predecessors = [...new Set(frontier.map(path => path.index))];
    statements.push(statement);
    frontier = frontier.length
      ? frontier.map(path => ({ index: statements.length - 1, terminal: path.terminal, conditions: [] }))
      : [{ index: statements.length - 1, conditions: [] }];
    return frontier[0].index;
  }
  function decisionPaths(paths, outcome) {
    return paths.map(path => ({ index: path.index, conditions: [...path.conditions, { index: path.index, outcome }] }));
  }
  function isolate(action) {
    // No normal-flow proof crosses an unsupported control transfer. Keep the
    // source steps and their independently provable internal relationships.
    frontier = [];
    action();
    frontier = [];
  }
  let body;
  function locate(node) {
    if (ts.isBlock(node) && node.getStart(tree) + 1 === bodyStart && node.end - 1 === bodyEnd) body = node;
    if (ts.isArrowFunction(node) && !ts.isBlock(node.body) && node.body.getStart(tree) === bodyStart && node.body.end === bodyEnd) body = node.body;
    if (!body) ts.forEachChild(node, locate);
  }
  locate(tree);
  if (!body) return [];
  const contextFor = syntaxContextReader(code, tree, body);
  const owner = body.parent;
  const synchronousFunction = ts.isFunctionLike(owner) && !owner.asteriskToken && !owner.modifiers?.some(modifier => modifier.kind === ts.SyntaxKind.AsyncKeyword);
  // A direct synchronous call made inside an async function still throws at the
  // call site and can be handled by that function's catch. Generator execution
  // has different resumption semantics and remains outside this relation.
  const directThrowCallerEligible = ts.isFunctionLike(owner) && !owner.asteriskToken;
  // A direct synchronous callee completes at an async caller's call site too.
  // This does not model the completion of an async callee or an await expression.
  const normalReturnCallerEligible = ts.isFunctionLike(owner) && !owner.asteriskToken;
  let synchronous = synchronousFunction;
  let directThrowEligible = synchronous;
  let asyncThrowEligible = ts.isFunctionLike(owner) && !owner.asteriskToken && owner.modifiers?.some(modifier => modifier.kind === ts.SyntaxKind.AsyncKeyword);
  function inspectCompletion(node) {
    if (node !== body && ts.isFunctionLike(node)) return;
    if (ts.isAwaitExpression(node) || ts.isYieldExpression(node) || ts.isTryStatement(node)) synchronous = false;
    if (ts.isAwaitExpression(node) || ts.isYieldExpression(node)) directThrowEligible = false;
    if (ts.isYieldExpression(node)) asyncThrowEligible = false;
    ts.forEachChild(node, inspectCompletion);
  }
  inspectCompletion(body);

  // A top-level finally with exactly one return statement replaces every
  // completion from its try/catch. In this deliberately narrow form, only the
  // finalizer return may resume the caller; earlier returns are not outcomes.
  let prevailingFinallyReturnStart = -1;
  if (synchronousFunction && ts.isBlock(body) && body.statements.length > 0 && ts.isTryStatement(body.statements[0])) {
    const finalizer = body.statements[0].finallyBlock;
    if (finalizer?.statements.length === 1 && ts.isReturnStatement(finalizer.statements[0])) {
      prevailingFinallyReturnStart = finalizer.statements[0].getStart(tree);
    }
  }


  function directCall(expression) {
    while (expression && (ts.isAwaitExpression(expression) || ts.isParenthesizedExpression(expression) || ts.isAsExpression(expression) || ts.isSatisfiesExpression(expression))) expression = expression.expression;
    if (!expression || !ts.isCallExpression(expression)) return {};
    const target = expression.expression;
    if (ts.isIdentifier(target)) return { methodName: target.text, receiver: '' };
    if (ts.isPropertyAccessExpression(target)) return { methodName: target.name.text, receiver: target.expression.getText(tree).replace(/^this\./, '') };
    return {};
  }
  function unwrapped(expression) {
    while (expression && (ts.isParenthesizedExpression(expression) || ts.isAsExpression(expression) || ts.isSatisfiesExpression(expression))) expression = expression.expression;
    return expression;
  }
  function sourceBindsPromise() {
    let bound = false;
    function inspect(node) {
      if (bound) return;
      if ((ts.isVariableDeclaration(node) || ts.isParameter(node) || ts.isImportClause(node) || ts.isImportSpecifier(node) || ts.isNamespaceImport(node) || ts.isFunctionDeclaration(node) || ts.isClassDeclaration(node))
        && node.name && ts.isIdentifier(node.name) && node.name.text === 'Promise') bound = true;
      ts.forEachChild(node, inspect);
    }
    inspect(tree);
    return bound;
  }
  function promiseAllCalls(node) {
    if (!ts.isAwaitExpression(node)) return null;
    const expression = unwrapped(node.expression);
    if (!ts.isCallExpression(expression) || !ts.isPropertyAccessExpression(expression.expression)
      || expression.expression.name.text !== 'all' || !ts.isIdentifier(expression.expression.expression)
      || expression.expression.expression.text !== 'Promise') return null;
    if (sourceBindsPromise() || expression.arguments.length !== 1 || !ts.isArrayLiteralExpression(expression.arguments[0])) return { calls: null };
    const calls = expression.arguments[0].elements.map(unwrapped);
    if (calls.length < 2 || calls.some(call => !ts.isCallExpression(call))) return { calls: null };
    return { calls };
  }
  function expressionCalls(node, condition) {
    if (!node || ts.isFunctionLike(node) || ts.isClassLike(node)) return;
    const parallel = promiseAllCalls(node);
    if (parallel) {
      if (parallel.calls) {
        const sharedPredecessors = [...frontier];
        const parallelExits = [];
        for (const call of parallel.calls) {
          frontier = [...sharedPredecessors];
          expressionCalls(call, condition);
          parallelExits.push(...frontier.map(path => ({ ...path, parallel: true })));
        }
        frontier = parallelExits;
      }
      const startOffset = node.getStart(tree);
      const rawText = code.slice(startOffset, node.end);
      const limitation = parallel.calls ? (catchContexts.length ? '' : '대기 실패·거절 이후의 예외 경로를 확인하지 못했습니다.') : '병렬 대기의 내부 작업 관계를 확인하지 못했습니다.';
      emit({ type: 'await', startOffset, endOffset: node.end, rawText, description: rawText,
        ...(limitation ? { controlFlowLimitation: limitation } : {}) });
      return;
    }
    if (ts.isAwaitExpression(node)) {
      const awaitedStart = statements.length;
      expressionCalls(node.expression, condition);
      const awaitedExpression = unwrapped(node.expression);
      if (ts.isCallExpression(awaitedExpression) && statements.length === awaitedStart + 1 && statements[awaitedStart].type === 'call') {
        statements[awaitedStart].awaitedCall = true;
      }
      if (catchCallContexts.length && ts.isCallExpression(awaitedExpression) && statements.length === awaitedStart + 1 && statements[awaitedStart].type === 'call') {
        statements[awaitedStart].awaitedCatchCall = true;
      }
      const startOffset = node.getStart(tree);
      const rawText = code.slice(startOffset, node.end);
      emit({ type: 'await', startOffset, endOffset: node.end, rawText, description: rawText,
        ...(catchContexts.length ? {} : { controlFlowLimitation: '대기 실패·거절 이후의 예외 경로를 확인하지 못했습니다.' }) });
      return;
    }
    if (ts.isConditionalExpression(node)) {
      expressionCalls(node.condition, condition);
      const test = node.condition.getText(tree);
      const startOffset = node.getStart(tree);
      emit({ type: 'branch', startOffset, endOffset: node.condition.end, rawText: test, description: test });
      const decision = [...frontier];
      frontier = decisionPaths(decision, 'truthy');
      expressionCalls(node.whenTrue, condition ? `(${condition}) && (${test})` : test);
      const trueExits = [...frontier];
      frontier = decisionPaths(decision, 'falsy');
      expressionCalls(node.whenFalse, condition ? `(${condition}) && !(${test})` : `!(${test})`);
      frontier = [...new Set([...trueExits, ...frontier])];
      return;
    }
    if (ts.isBinaryExpression(node) && [ts.SyntaxKind.AmpersandAmpersandToken, ts.SyntaxKind.BarBarToken, ts.SyntaxKind.QuestionQuestionToken].includes(node.operatorToken.kind)) {
      expressionCalls(node.left, condition);
      const left = node.left.getText(tree);
      const gate = node.operatorToken.kind === ts.SyntaxKind.AmpersandAmpersandToken ? left
        : node.operatorToken.kind === ts.SyntaxKind.BarBarToken ? `!(${left})` : `(${left}) == null`;
      emit({ type: 'branch', startOffset: node.left.getStart(tree), endOffset: node.left.end, rawText: left, description: left });
      const decision = [...frontier];
      const evaluate = node.operatorToken.kind === ts.SyntaxKind.AmpersandAmpersandToken ? 'truthy'
        : node.operatorToken.kind === ts.SyntaxKind.BarBarToken ? 'falsy' : 'nullish';
      const skip = evaluate === 'truthy' ? 'falsy' : evaluate === 'falsy' ? 'truthy' : 'non_nullish';
      const skippedRight = decisionPaths(decision, skip);
      frontier = decisionPaths(decision, evaluate);
      expressionCalls(node.right, condition ? `(${condition}) && (${gate})` : gate);
      frontier = [...new Set([...skippedRight, ...frontier])];
      return;
    }
    if (ts.isBinaryExpression(node) && node.operatorToken.kind >= ts.SyntaxKind.FirstAssignment && node.operatorToken.kind <= ts.SyntaxKind.LastAssignment) {
      if (ts.isArrayLiteralExpression(node.left) || ts.isObjectLiteralExpression(node.left)) {
        // Destructuring evaluates the RHS before computed keys/defaults. The
        // latter require iterator/property/default semantics not yet modeled.
        expressionCalls(node.right, condition);
        const startOffset = node.getStart(tree);
        const rawText = code.slice(startOffset, node.end);
        emit({ type: 'mutation', startOffset, endOffset: node.end, rawText, description: rawText,
          controlFlowLimitation: '구조 분해 대입 내부의 키·기본값·반복자 실행 연결을 확인하지 못했습니다.' });
        return;
      }
      expressionCalls(node.left, condition);
      const logical = new Map([
        [ts.SyntaxKind.AmpersandAmpersandEqualsToken, ['truthy', 'falsy']],
        [ts.SyntaxKind.BarBarEqualsToken, ['falsy', 'truthy']],
        [ts.SyntaxKind.QuestionQuestionEqualsToken, ['nullish', 'non_nullish']],
      ]).get(node.operatorToken.kind);
      let skipped = [];
      if (logical) {
        const left = node.left.getText(tree);
        emit({ type: 'branch', startOffset: node.left.getStart(tree), endOffset: node.left.end, rawText: left, description: left });
        const decision = [...frontier];
        skipped = decisionPaths(decision, logical[1]);
        frontier = decisionPaths(decision, logical[0]);
      }
      const valueStart = statements.length;
      expressionCalls(node.right, condition);
      const directValue = node.operatorToken.kind === ts.SyntaxKind.EqualsToken && ts.isIdentifier(node.left)
        && ts.isCallExpression(node.right) && statements.length === valueStart + 1
        && statements[valueStart].type === 'call';
      const startOffset = node.getStart(tree);
      const rawText = code.slice(startOffset, node.end);
      emit({ type: 'mutation', startOffset, endOffset: node.end, rawText, description: rawText, ...(directValue ? { assignmentSourceIndex: valueStart } : {}) });
      frontier = [...skipped, ...frontier];
      return;
    }
    if (ts.isDeleteExpression(node) || ((ts.isPrefixUnaryExpression(node) || ts.isPostfixUnaryExpression(node)) && [ts.SyntaxKind.PlusPlusToken, ts.SyntaxKind.MinusMinusToken].includes(node.operator))) {
      expressionCalls(ts.isDeleteExpression(node) ? node.expression : node.operand, condition);
      const startOffset = node.getStart(tree);
      const rawText = code.slice(startOffset, node.end);
      emit({ type: 'mutation', startOffset, endOffset: node.end, rawText, description: rawText });
      return;
    }
    if (ts.isNewExpression(node)) {
      expressionCalls(node.expression, condition);
      for (const argument of node.arguments || []) expressionCalls(argument, condition);
      const startOffset = node.getStart(tree);
      const rawText = code.slice(startOffset, node.end);
      emit({ type: 'call', startOffset, endOffset: node.end, rawText, description: rawText, guardCondition: condition || null,
        controlFlowLimitation: '생성자 내부와 초기화 과정의 실행 연결을 확인하지 못했습니다.' });
      return;
    }
    if (ts.isVariableDeclaration(node) && node.initializer) {
      const valueStart = statements.length;
      expressionCalls(node.initializer, condition);
      const bindingPattern = !ts.isIdentifier(node.name);
      if (statements.length > valueStart || bindingPattern) {
        const directValue = !bindingPattern && ts.isCallExpression(node.initializer)
          && statements.length === valueStart + 1 && statements[valueStart].type === 'call';
        const startOffset = node.getStart(tree);
        const rawText = code.slice(startOffset, node.end);
        emit({ type: 'mutation', startOffset, endOffset: node.end, rawText, description: rawText,
          ...(directValue ? { assignmentSourceIndex: valueStart } : {}),
          ...(bindingPattern ? { controlFlowLimitation: '구조 분해 초기화의 키·기본값·반복자 실행 연결을 확인하지 못했습니다.' } : {}) });
      }
      return;
    }
    if (ts.isCallExpression(node)) {
      expressionCalls(node.expression, condition);
      for (const argument of node.arguments) expressionCalls(argument, condition);
      const startOffset = node.getStart(tree);
      const rawText = code.slice(startOffset, node.end);
      const callIndex = emit({ ...directCall(node), type: 'call', startOffset, endOffset: node.end, rawText, description: rawText, guardCondition: condition || null,
        ...(tryDepth ? { normalReturnUnsupported: true } : {}) });
      if (catchCallContexts.length) catchCallContexts[catchCallContexts.length - 1].push(callIndex);
      return;
    }
    ts.forEachChild(node, child => expressionCalls(child, condition));
  }
  function completion(node, condition) {
    expressionCalls(node.expression, condition);

    const startOffset = node.getStart(tree);
    const rawText = code.slice(startOffset, node.end);
    emit({ guardCondition: condition || null, type: ts.isReturnStatement(node) ? 'return' : 'throw', startOffset, endOffset: node.end, rawText, description: rawText,
      ...(ts.isReturnStatement(node) && startOffset === prevailingFinallyReturnStart ? { prevailingFinallyReturn: true } : {}) });
    if (ts.isThrowStatement(node) && catchContexts.length) {
      catchContexts[catchContexts.length - 1].push(...frontier.map(path => ({ ...path, failure: true })));
    } else if (finallyContexts.length) {
      finallyContexts[finallyContexts.length - 1].push(...frontier.map(path => ({ ...path, terminal: true, finally: true })));
    }
    frontier = [];
  }
  function leaf(node, condition) {
    const countBeforeCalls = statements.length;
    expressionCalls(node, condition);
    const hasCalls = statements.length > countBeforeCalls;
    const start = node.getStart(tree);
    let text = code.slice(start, node.end);
    const excluded = [];
    function exclude(child) {
      if (ts.isFunctionLike(child) || ts.isClassLike(child)) {
        excluded.push([child.getStart(tree) - start, child.end - start]);
        return;
      }
      ts.forEachChild(child, exclude);
    }
    ts.forEachChild(node, exclude);
    for (const [from, to] of excluded) text = text.slice(0, from) + text.slice(from, to).replace(/[^\r\n]/g, ' ') + text.slice(to);
    // Keep original offsets and source descriptions while hiding uninvoked bodies
    // from the legacy classifier's token stream.
    const maskedCode = code.slice(0, start) + text + code.slice(node.end);
    for (const statement of extractStatements(text, start, maskedCode)) {
      if (hasCalls) continue;
      statement.rawText = code.slice(statement.startOffset, statement.endOffset);
      emit(statement);
    }
  }
  function completesBlock(node) {
    if (ts.isReturnStatement(node) || ts.isThrowStatement(node) || ts.isBreakStatement(node) || ts.isContinueStatement(node)) return true;
    if (ts.isBlock(node)) return node.statements.some(completesBlock);
    if (ts.isTryStatement(node)) {
      if (node.finallyBlock && completesBlock(node.finallyBlock)) return true;
      return completesBlock(node.tryBlock) && (!node.catchClause || completesBlock(node.catchClause.block));
    }
    return ts.isIfStatement(node) && !!node.elseStatement && completesBlock(node.thenStatement) && completesBlock(node.elseStatement);
  }
  function simpleWhile(node) {
    let supported = true;
    function inspect(child) {
      if (ts.isFunctionLike(child) || ts.isClassLike(child)) return;
      if ((ts.isIterationStatement(child, false) && !ts.isWhileStatement(child)) || ((ts.isBreakStatement(child) || ts.isContinueStatement(child)) && child.label)
        || ts.isLabeledStatement(child) || (ts.isTryStatement(child) && (!child.catchClause || child.finallyBlock) && !simpleAwaitFinally(child)) || ts.isSwitchStatement(child)
        || ts.isYieldExpression(child)) supported = false;
      ts.forEachChild(child, inspect);
    }
    inspect(node.expression);
    inspect(node.statement);
    return supported;
  }
  function simpleAwaitFinally(node) {
    if (!ts.isTryStatement(node) || !node.finallyBlock || node.tryBlock.statements.length !== 1) return false;
    const awaited = node.tryBlock.statements[0];
    if (!ts.isExpressionStatement(awaited) || !ts.isAwaitExpression(awaited.expression)) return false;
    const directCalls = statements => statements.length > 0 && statements.every(statement =>
      ts.isExpressionStatement(statement) && ts.isCallExpression(statement.expression));
    return (!node.catchClause || directCalls(node.catchClause.block.statements)) && directCalls(node.finallyBlock.statements);
  }
  function simpleFor(node) {
    let supported = true;
    function inspect(child) {
      if (!child || ts.isFunctionLike(child) || ts.isClassLike(child)) return;
      if (ts.isIterationStatement(child, false) || ((ts.isBreakStatement(child) || ts.isContinueStatement(child)) && child.label)
        || ts.isLabeledStatement(child) || ts.isTryStatement(child) || ts.isSwitchStatement(child)
        || ts.isYieldExpression(child)) supported = false;
      ts.forEachChild(child, inspect);
    }
    inspect(node.initializer);
    inspect(node.condition);
    inspect(node.incrementor);
    inspect(node.statement);
    return supported;
  }
  function simpleDo(node) {
    let supported = true;
    function inspect(child) {
      if (!child || ts.isFunctionLike(child) || ts.isClassLike(child)) return;
      if (ts.isIterationStatement(child, false) || ((ts.isBreakStatement(child) || ts.isContinueStatement(child)) && child.label)
        || ts.isLabeledStatement(child) || ts.isTryStatement(child) || ts.isSwitchStatement(child)
        || ts.isAwaitExpression(child) || ts.isYieldExpression(child)) supported = false;
      ts.forEachChild(child, inspect);
    }
    inspect(node.expression);
    inspect(node.statement);
    return supported;
  }
  function simpleSwitch(node) {
    let supported = true;
    function inspect(child) {
      if (ts.isFunctionLike(child) || ts.isClassLike(child)) return;
      if (ts.isIterationStatement(child, false) || ts.isTryStatement(child) || ts.isSwitchStatement(child)
        || ts.isAwaitExpression(child) || ts.isYieldExpression(child) || ts.isLabeledStatement(child)) supported = false;
      ts.forEachChild(child, inspect);
    }
    for (const clause of node.caseBlock.clauses) for (const statement of clause.statements) inspect(statement);
    return supported;
  }
  function visit(node, condition) {
    if (ts.isFunctionLike(node) || ts.isClassLike(node)) return;
    if (ts.isReturnStatement(node) || ts.isThrowStatement(node)) { completion(node, condition); return; }
    if (ts.isBreakStatement(node) || ts.isContinueStatement(node)) {
      const startOffset = node.getStart(tree);
      const rawText = code.slice(startOffset, node.end);
      const nearestControl = controlContexts[controlContexts.length - 1];
      const nearestLoop = [...controlContexts].reverse().find(context => context.type === 'loop');
      const type = !node.label && ((ts.isBreakStatement(node) && nearestControl) || (ts.isContinueStatement(node) && nearestLoop))
        ? (ts.isBreakStatement(node) ? 'break' : 'continue') : 'branch';
      emit({ type, startOffset, endOffset: node.end, rawText, description: rawText, guardCondition: condition || null });
      if (type === 'break') {
        const marker = nearestControl.type === 'switch' ? { switchExit: true } : { loopExit: true };
        nearestControl.breaks.push(...frontier.map(path => ({ ...path, ...marker })));
      }
      if (type === 'continue') nearestLoop.continues.push(...frontier);
      frontier = [];
      return;
    }
    if (ts.isBlock(node)) {
      for (const statement of node.statements) {
        visit(statement, condition);
        // This only proves that the remainder of this block is unreachable.
        // A surrounding catch/finally and the caller still need their own steps.
        if (completesBlock(statement)) break;
      }
      return;
    }
    if (ts.isIfStatement(node)) {
      const conditionText = node.expression.getText(tree);
      const immediateCompletion = ts.isReturnStatement(node.thenStatement) || ts.isThrowStatement(node.thenStatement);
      expressionCalls(node.expression, condition);
      emit({ type: immediateCompletion && !node.elseStatement ? 'guard' : 'branch', guardCondition: conditionText,
        startOffset: node.getStart(tree), endOffset: controlHeaderEnd(node, tree) || node.expression.end + 1,
        rawText: code.slice(node.getStart(tree), controlHeaderEnd(node, tree) || node.expression.end + 1), description: `if (${conditionText})` });
      const decision = [...frontier];
      frontier = decisionPaths(decision, 'truthy');
      visit(node.thenStatement, condition ? `(${condition}) && (${conditionText})` : conditionText);
      const trueExits = [...frontier];
      frontier = decisionPaths(decision, 'falsy');
      if (node.elseStatement) visit(node.elseStatement, condition ? `(${condition}) && !(${conditionText})` : `!(${conditionText})`);
      frontier = [...new Set([...trueExits, ...frontier])];
      return;
    }
    if (ts.isTryStatement(node)) {
      const regionStart = statements.length;
      const simpleAwaitFinalizer = simpleAwaitFinally(node);
      // A single direct completion in a finally block replaces every prior
      // completion from its try/catch. No normal continuation can survive a
      // finalizer throw, and a finalizer return is handled separately by the
      // caller-resumption rule above.
      const finalizerAlwaysCompletes = node.finallyBlock?.statements.length === 1
        && (ts.isReturnStatement(node.finallyBlock.statements[0]) || ts.isThrowStatement(node.finallyBlock.statements[0]));
      const terminalExits = [];
      const caughtThrows = [];
      const caughtCalls = [];
      tryDepth++;
      if (node.finallyBlock) finallyContexts.push(terminalExits);
      if (node.catchClause) {
        catchContexts.push(caughtThrows);
        catchCallContexts.push(caughtCalls);
      }
      visit(node.tryBlock, condition);
      if (node.catchClause) {
        catchContexts.pop();
        catchCallContexts.pop();
      }
      const normalExits = [...frontier];
      const rejectedAwaits = statements.slice(regionStart).flatMap((statement, index) => statement.type === 'await' ? [{ index: regionStart + index, conditions: [], failure: true }] : []);
      let catchExits = [];
      if (node.catchClause) {
        frontier = [...rejectedAwaits, ...caughtThrows];
        const catchStart = statements.length;
        visit(node.catchClause.block, condition);
        if (statements[catchStart]) {
          for (const callIndex of caughtCalls) statements[callIndex].catchTargetIndex = catchStart;
        }
        catchExits = [...frontier];
      }
      if (node.finallyBlock) finallyContexts.pop();
      frontier = [...normalExits, ...catchExits, ...terminalExits];
      const finalizerHasPredecessor = frontier.length > 0;
      if (node.finallyBlock) {
        if (finalizerHasPredecessor) visit(node.finallyBlock, condition);
        else isolate(() => visit(node.finallyBlock, condition));
        const finalizerExits = [...frontier];
        const nestedTerminalExits = finalizerExits.filter(path => path.terminal);
        if (nestedTerminalExits.length && finallyContexts.length) {
          finallyContexts[finallyContexts.length - 1].push(...nestedTerminalExits.map(path => ({ ...path, finally: true })));
        }
        frontier = finalizerExits.filter(path => !path.terminal);
      }
      if (!finalizerAlwaysCompletes && !rejectedAwaits.length && !caughtThrows.length && statements[regionStart]) statements[regionStart].controlFlowLimitation = 'try 내부에서 catch로 이어지는 실패 원인을 확인하지 못했습니다.';
      if (!finalizerAlwaysCompletes && ((!node.catchClause && !simpleAwaitFinalizer) || !finalizerHasPredecessor) && statements[regionStart]) statements[regionStart].controlFlowLimitation = '예외 처리와 finally 이후의 진행 관계를 확인하지 못했습니다.';
      tryDepth--;
      return;
    }
    if (ts.isSwitchStatement(node) && simpleSwitch(node)) {
      expressionCalls(node.expression, condition);
      const selector = node.expression.getText(tree);
      const startOffset = node.getStart(tree);
      emit({ type: 'branch', startOffset, endOffset: controlHeaderEnd(node, tree) || node.expression.end + 1, rawText: selector, description: `switch (${selector})` });
      const selectedPaths = [...frontier];
      const switchContext = { type: 'switch', breaks: [] };
      controlContexts.push(switchContext);
      let fallthroughExits = [];
      let hasDefault = false;
      for (const clause of node.caseBlock.clauses) {
        hasDefault = hasDefault || ts.isDefaultClause(clause);
        frontier = [...selectedPaths, ...fallthroughExits];
        const clauseStart = clause.getStart(tree);
        const clauseEnd = clause.statements.length ? clause.statements[0].getStart(tree) : clause.end;
        const rawText = code.slice(clauseStart, clauseEnd).trim();
        emit({ type: 'branch', startOffset: clauseStart, endOffset: clauseEnd, rawText, description: rawText });
        for (const statement of clause.statements) {
          visit(statement, condition);
          if (completesBlock(statement)) break;
        }
        fallthroughExits = [...frontier];
      }
      controlContexts.pop();
      frontier = [...fallthroughExits, ...switchContext.breaks, ...(hasDefault ? [] : selectedPaths)];
      return;
    }
    if (ts.isSwitchStatement(node)) {
      const regionStart = statements.length;
      frontier = [];
      expressionCalls(node.expression, condition);
      const selector = node.expression.getText(tree);
      const startOffset = node.getStart(tree);
      emit({ type: 'branch', startOffset, endOffset: controlHeaderEnd(node, tree) || node.expression.end + 1, rawText: selector, description: `switch (${selector})` });
      let fallthrough = '';
      for (const clause of node.caseBlock.clauses) {
        frontier = [];
        const ownCondition = ts.isCaseClause(clause) ? `${selector} === ${clause.expression.getText(tree)}` : `default (${selector})`;
        const gate = fallthrough ? `(${fallthrough}) || (${ownCondition})` : ownCondition;
        if (ts.isCaseClause(clause)) expressionCalls(clause.expression, condition);
        const clauseStart = clause.getStart(tree);
        const clauseEnd = clause.statements.length ? clause.statements[0].getStart(tree) : clause.end;
        const rawText = code.slice(clauseStart, clauseEnd).trim();
        emit({ type: 'branch', startOffset: clauseStart, endOffset: clauseEnd, rawText, description: rawText });
        fallthrough = gate;
        for (const statement of clause.statements) {
          visit(statement, condition ? `(${condition}) && (${gate})` : gate);
          if (completesBlock(statement)) { fallthrough = ''; break; }
        }
      }
      frontier = [];
      if (statements[regionStart]) statements[regionStart].controlFlowLimitation = 'switch의 분기 진입과 합류 관계를 확인하지 못했습니다.';
      return;
    }
    if ((ts.isForOfStatement(node) || ts.isForInStatement(node)) && !node.awaitModifier && simpleWhile(node)) {
      const entry = statements.length;
      expressionCalls(node.expression, condition);
      const startOffset = node.getStart(tree);
      const endOffset = controlHeaderEnd(node, tree) || node.expression.end + 1;
      const header = code.slice(startOffset, endOffset);
      emit({ type: 'branch', startOffset, endOffset, rawText: header, description: header, guardCondition: header });
      const decision = [...frontier];
      frontier = decisionPaths(decision, 'truthy');
      const loop = { type: 'loop', breaks: [], continues: [] };
      controlContexts.push(loop);
      visit(node.statement, condition ? `(${condition}) && (${header})` : header);
      controlContexts.pop();
      for (const path of [...frontier, ...loop.continues]) {
        statements[entry].predecessorPaths.push({ ...path, loopBack: true });
      }
      frontier = [...decisionPaths(decision, 'falsy'), ...loop.breaks];
      return;
    }
    if (ts.isForOfStatement(node) && node.awaitModifier && simpleWhile(node)) {
      expressionCalls(node.expression, condition);
      const entry = statements.length;
      const startOffset = node.getStart(tree);
      const endOffset = controlHeaderEnd(node, tree) || node.expression.end + 1;
      const header = code.slice(startOffset, endOffset);
      // Every async-iterator iteration waits for the next item. Its result
      // decides whether the body starts or the loop exits. Iterator rejection
      // and closing require protocol knowledge we do not infer here.
      emit({ type: 'await', startOffset, endOffset, rawText: header, description: header, guardCondition: header,
        controlFlowLimitation: '비동기 반복자의 거절·정리 과정과 반복 값의 출처를 확인하지 못했습니다.' });
      emit({ type: 'branch', startOffset, endOffset, rawText: header, description: header, guardCondition: header });
      const decision = [...frontier];
      frontier = decisionPaths(decision, 'truthy');
      const loop = { type: 'loop', breaks: [], continues: [] };
      controlContexts.push(loop);
      visit(node.statement, condition ? `(${condition}) && (${header})` : header);
      controlContexts.pop();
      for (const path of [...frontier, ...loop.continues]) {
        statements[entry].predecessorPaths.push({ ...path, loopBack: true });
      }
      frontier = [...decisionPaths(decision, 'falsy'), ...loop.breaks];
      return;
    }
    if (ts.isForOfStatement(node) || ts.isForInStatement(node)) {
      const regionStart = statements.length;
      frontier = [];
      expressionCalls(node.expression, condition);
      const startOffset = node.getStart(tree);
      const endOffset = controlHeaderEnd(node, tree) || node.expression.end + 1;
      const header = code.slice(startOffset, endOffset);
      emit({ type: 'branch', startOffset, endOffset, rawText: header, description: header });
      visit(node.statement, condition ? `(${condition}) && (${header})` : header);
      frontier = [];
      if (statements[regionStart]) statements[regionStart].controlFlowLimitation = '반복 진입·중단·다음 반복의 연결을 확인하지 못했습니다.';
      return;
    }
    if (ts.isWhileStatement(node) && simpleWhile(node)) {
      const entry = statements.length;
      expressionCalls(node.expression, condition);
      const gate = node.expression.getText(tree);
      const startOffset = node.getStart(tree);
      emit({ type: 'branch', startOffset, endOffset: controlHeaderEnd(node, tree), rawText: gate,
        description: `while (${gate})`, guardCondition: gate });
      const decision = [...frontier];
      frontier = decisionPaths(decision, 'truthy');
      const loop = { type: 'loop', breaks: [], continues: [] };
      controlContexts.push(loop);
      visit(node.statement, condition ? `(${condition}) && (${gate})` : gate);
      controlContexts.pop();
      for (const path of [...frontier, ...loop.continues]) {
        statements[entry].predecessorPaths.push({ ...path, loopBack: true });
      }
      frontier = [...decisionPaths(decision, 'falsy'), ...loop.breaks];
      return;
    }
    if (ts.isForStatement(node) && simpleFor(node)) {
      expressionCalls(node.initializer, condition);
      expressionCalls(node.condition, condition);
      const entry = statements.length;
      const test = node.condition ? node.condition.getText(tree) : 'true';
      const startOffset = node.getStart(tree);
      const endOffset = controlHeaderEnd(node, tree) || (node.condition ? node.condition.end : startOffset + 3);
      const header = code.slice(startOffset, endOffset);
      emit({ type: 'branch', startOffset, endOffset, rawText: header,
        description: header, guardCondition: test });
      const decision = [...frontier];
      frontier = decisionPaths(decision, 'truthy');
      const loop = { type: 'loop', breaks: [], continues: [] };
      controlContexts.push(loop);
      visit(node.statement, condition ? `(${condition}) && (${test})` : test);
      controlContexts.pop();
      let repeated = [...frontier, ...loop.continues];
      if (node.incrementor) {
        frontier = repeated;
        expressionCalls(node.incrementor, condition ? `(${condition}) && (${test})` : test);
        repeated = [...frontier];
      }
      for (const path of repeated) {
        statements[entry].predecessorPaths.push({ ...path, loopBack: true });
      }
      frontier = [...decisionPaths(decision, 'falsy'), ...loop.breaks];
      return;
    }
    if (ts.isDoStatement(node) && simpleDo(node)) {
      const bodyEntry = statements.length;
      const loop = { type: 'loop', breaks: [], continues: [] };
      controlContexts.push(loop);
      visit(node.statement, condition);
      controlContexts.pop();
      const repeated = [...frontier, ...loop.continues];
      if (!repeated.length) {
        frontier = [...loop.breaks];
        return;
      }
      frontier = repeated;
      expressionCalls(node.expression, condition);
      const entry = statements.length;
      const gate = node.expression.getText(tree);
      const startOffset = doConditionHeaderStart(node, tree);
      const endOffset = controlHeaderEnd(node, tree) || node.expression.end;
      const header = code.slice(startOffset, endOffset);
      emit({ type: 'branch', startOffset, endOffset, rawText: header, description: header, guardCondition: gate });
      const decision = [...frontier];
      const reentry = bodyEntry < entry ? bodyEntry : entry;
      for (const path of decisionPaths(decision, 'truthy')) {
        statements[reentry].predecessorPaths.push({ ...path, loopReentry: true });
      }
      frontier = [...decisionPaths(decision, 'falsy'), ...loop.breaks];
      return;
    }
    if (ts.isForStatement(node) || ts.isWhileStatement(node) || ts.isDoStatement(node)) {
      const regionStart = statements.length;
      frontier = [];
      if (ts.isForStatement(node)) expressionCalls(node.initializer, condition);
      const test = ts.isForStatement(node) ? node.condition : node.expression;
      const gate = test ? test.getText(tree) : 'true';
      const bodyCondition = condition ? `(${condition}) && (${gate})` : gate;
      if (ts.isDoStatement(node)) visit(node.statement, condition);
      expressionCalls(test, condition);
      const startOffset = node.getStart(tree);
      emit({ type: 'branch', startOffset, endOffset: controlHeaderEnd(node, tree) || (test ? test.end : startOffset + 3), rawText: gate, description: `loop (${gate})`, guardCondition: gate });
      if (!ts.isDoStatement(node)) visit(node.statement, bodyCondition);
      if (ts.isForStatement(node)) isolate(() => expressionCalls(node.incrementor, bodyCondition));
      frontier = [];
      if (statements[regionStart]) statements[regionStart].controlFlowLimitation = '반복 진입·중단·다음 반복의 연결을 확인하지 못했습니다.';
      return;
    }
    if (ts.isExpressionStatement(node) || ts.isVariableStatement(node)) { leaf(node, condition); return; }
    ts.forEachChild(node, child => visit(child, condition));
  }
  if (ts.isBlock(body)) visit(body);
  else {
    expressionCalls(body);
    const startOffset = body.getStart(tree);
    const rawText = code.slice(startOffset, body.end);
    emit({type: 'return', startOffset, endOffset: body.end, rawText, description: rawText});
    frontier = [];
  }
  statements.prevailingFinallyReturnIndex = statements.findIndex(statement => statement.prevailingFinallyReturn);
  statements.normalReturnEligible = synchronousFunction && (synchronous || statements.prevailingFinallyReturnIndex >= 0) && !statements.some(statement => statement.controlFlowLimitation);
  statements.normalReturnCallerEligible = normalReturnCallerEligible;
  statements.directThrowEligible = directThrowEligible;
  statements.directThrowCallerEligible = directThrowCallerEligible;
  statements.asyncThrowEligible = asyncThrowEligible;
  return statements;
}

module.exports = { extractExecutionStatements };
