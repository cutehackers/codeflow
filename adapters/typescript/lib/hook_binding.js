'use strict';

const ts = require('../vendor/typescript/typescript');
const models = new WeakMap();

// Binding is performed against captured text only. The compiler host cannot read
// ambient files, load libraries, emit files, or resolve packages on the machine.
function sourceModel(code, scan, fileName) {
  if (models.has(scan)) return models.get(scan);
  const source = ts.createSourceFile(fileName, code, ts.ScriptTarget.Latest, true,
    /\.tsx$/.test(fileName) ? ts.ScriptKind.TSX : /\.jsx$/.test(fileName) ? ts.ScriptKind.JSX : ts.ScriptKind.TS);
  const options = { noLib: true, noResolve: true, allowJs: true, target: ts.ScriptTarget.Latest };
  const host = {
    getSourceFile: name => name === fileName ? source : undefined,
    getDefaultLibFileName: () => '', writeFile: () => {}, getCurrentDirectory: () => '/',
    getDirectories: () => [], fileExists: name => name === fileName,
    readFile: name => name === fileName ? code : undefined,
    getCanonicalFileName: name => name, useCaseSensitiveFileNames: () => true, getNewLine: () => '\n',
  };
  const program = ts.createProgram([fileName], options, host);
  const model = { source, checker: program.getTypeChecker(), scan };
  models.set(scan, model);
  return model;
}

function unwrap(expression) {
  while (expression && (ts.isParenthesizedExpression(expression) || ts.isAsExpression(expression) || ts.isNonNullExpression(expression) || ts.isSatisfiesExpression(expression))) expression = expression.expression;
  return expression;
}

function oneDeclaration(model, identifier) {
  const declarations = model.checker.getSymbolAtLocation(identifier)?.declarations;
  return declarations?.length === 1 ? declarations[0] : null;
}

function rootIdentifier(expression) {
  expression = unwrap(expression);
  while (expression && (ts.isPropertyAccessExpression(expression) || ts.isElementAccessExpression(expression))) expression = unwrap(expression.expression);
  return expression && ts.isIdentifier(expression) ? expression : null;
}

function hasWrites(model, identifier) {
  const symbol = model.checker.getSymbolAtLocation(identifier);
  const aliases = new Set([symbol]);
  const variables = [];
  function collect(node) {
    if (ts.isVariableDeclaration(node) && ts.isIdentifier(node.name) && node.initializer) variables.push(node);
    ts.forEachChild(node, collect);
  }
  collect(model.source);
  let changed = true;
  while (changed) {
    changed = false;
    for (const variable of variables) {
      const value = unwrap(variable.initializer);
      if (!ts.isIdentifier(value) || !aliases.has(model.checker.getSymbolAtLocation(value))) continue;
      const alias = model.checker.getSymbolAtLocation(variable.name);
      if (alias && !aliases.has(alias)) { aliases.add(alias); changed = true; }
    }
  }
  let found = false;
  function visit(node) {
    if (found) return;
    let target;
    if (ts.isBinaryExpression(node) && node.operatorToken.kind >= ts.SyntaxKind.FirstAssignment && node.operatorToken.kind <= ts.SyntaxKind.LastAssignment) target = node.left;
    if ((ts.isPrefixUnaryExpression(node) || ts.isPostfixUnaryExpression(node)) && [ts.SyntaxKind.PlusPlusToken, ts.SyntaxKind.MinusMinusToken].includes(node.operator)) target = node.operand;
    if (ts.isDeleteExpression(node)) target = node.expression;
    const root = target && rootIdentifier(target);
    if (root && aliases.has(model.checker.getSymbolAtLocation(root))) found = true;
    ts.forEachChild(node, visit);
  }
  visit(model.source);
  return found;
}

function isReactCallback(model, expression) {
  const callee = expression.expression;
  if (ts.isIdentifier(callee)) {
    const declaration = oneDeclaration(model, callee);
    return declaration && ts.isImportSpecifier(declaration)
      && (declaration.propertyName || declaration.name).text === 'useCallback'
      && declaration.parent.parent.parent.moduleSpecifier?.text === 'react';
  }
  if (ts.isPropertyAccessExpression(callee) && callee.name.text === 'useCallback' && ts.isIdentifier(callee.expression)) {
    const declaration = oneDeclaration(model, callee.expression);
    const statement = declaration && (ts.isImportClause(declaration) ? declaration.parent : ts.isNamespaceImport(declaration) ? declaration.parent.parent : null);
    return statement?.moduleSpecifier?.text === 'react';
  }
  return false;
}

function callableTarget(model, declaration, relPath) {
  let expression = ts.isVariableDeclaration(declaration) ? unwrap(declaration.initializer) : declaration;
  if (!expression) return null;
  if (ts.isVariableDeclaration(declaration) && (!(declaration.parent.flags & ts.NodeFlags.Const) || hasWrites(model, declaration.name))) return null;
  if (ts.isFunctionDeclaration(declaration) && declaration.name && hasWrites(model, declaration.name)) return null;
  // Hooks commonly return a function created by useCallback. Inspect its actual
  // argument, retaining the callback's source body rather than its declaration.
  if (ts.isCallExpression(expression)) {
    if (!isReactCallback(model, expression)) return null;
    expression = unwrap(expression.arguments[0]);
  }
  if (!expression || !ts.isFunctionLike(expression) || !expression.body) return null;
  const block = ts.isBlock(expression.body);
  const matches = model.scan.topLevelFunctions.filter(fn => fn.bodyStart === expression.body.getStart(model.source) + (block ? 1 : 0) && fn.bodyEnd === expression.body.end - (block ? 1 : 0));
  return matches.length === 1 ? { relPath, className: matches[0].parentScope, methodName: matches[0].localName } : null;
}

function returnedMember(model, hook, property, relPath) {
  if (ts.isVariableDeclaration(hook) && (!(hook.parent.flags & ts.NodeFlags.Const) || hasWrites(model, hook.name))) return null;
  if (ts.isFunctionDeclaration(hook) && hook.name && hasWrites(model, hook.name)) return null;
  let fn = ts.isVariableDeclaration(hook) ? unwrap(hook.initializer) : hook;
  if (!fn || !ts.isFunctionLike(fn) || !fn.body || !ts.isBlock(fn.body)) return null;
  const returns = [];
  function visit(node) {
    if (node !== fn && ts.isFunctionLike(node)) return;
    if (ts.isReturnStatement(node)) returns.push(node);
    ts.forEachChild(node, visit);
  }
  visit(fn.body);
  if (returns.length !== 1 || returns[0].parent !== fn.body) return null;
  const result = unwrap(returns[0].expression);
  let value;
  if (result && ts.isObjectLiteralExpression(result)) {
    if (result.properties.some(node => ts.isSpreadAssignment(node))) return null;
    const entries = result.properties.filter(node => node.name && !ts.isComputedPropertyName(node.name) && node.name.text === property);
    if (entries.length !== 1) return null;
    const entry = entries[0];
    if (ts.isShorthandPropertyAssignment(entry)) {
      const symbol = model.checker.getShorthandAssignmentValueSymbol(entry);
      return symbol?.declarations?.length === 1 ? callableTarget(model, symbol.declarations[0], relPath) : null;
    }
    if (ts.isPropertyAssignment(entry)) value = unwrap(entry.initializer);
  } else if (result && ts.isArrayLiteralExpression(result) && Number.isInteger(property)) value = unwrap(result.elements[property]);
  if (!value || !ts.isIdentifier(value)) return null;
  const declaration = oneDeclaration(model, value);
  return declaration ? callableTarget(model, declaration, relPath) : null;
}

function typedParameterTarget(model, declaration, methodName, resolveModule) {
  let type = declaration.type;
  if (ts.isBindingElement(declaration) && ts.isParameter(declaration.parent.parent)) {
    const annotation = declaration.parent.parent.type;
    if (!annotation || !ts.isTypeReferenceNode(annotation) || !ts.isIdentifier(annotation.typeName)) return null;
    const definition = oneDeclaration(model, annotation.typeName);
    if (!definition || !ts.isInterfaceDeclaration(definition)) return null;
    const property = (declaration.propertyName || declaration.name).text;
    const members = definition.members.filter(member => ts.isPropertySignature(member) && member.name?.text === property);
    if (members.length !== 1) return null;
    type = members[0].type;
  }
  if (!type || !ts.isTypeReferenceNode(type) || !ts.isIdentifier(type.typeName)) return null;
  const imported = oneDeclaration(model, type.typeName);
  if (!imported || !ts.isImportSpecifier(imported)) return null;
  const statement = imported.parent.parent.parent;
  const files = resolveModule(statement.moduleSpecifier.text);
  if (files.length !== 1) return null;
  const className = (imported.propertyName || imported.name).text;
  const target = sourceModel(files[0].code, files[0].scan, files[0].relPath);
  const module = target.checker.getSymbolAtLocation(target.source);
  if (!module || target.source.parseDiagnostics.length) return null;
  const exported = target.checker.getExportsOfModule(module).find(symbol => symbol.name === className);
  const definitions = exported?.declarations || [];
  if (definitions.length !== 1 || !ts.isClassDeclaration(definitions[0])) return null;
  const methods = definitions[0].members.filter(member => ts.isMethodDeclaration(member) && member.name?.text === methodName && member.body && !member.modifiers?.some(modifier => modifier.kind === ts.SyntaxKind.StaticKeyword));
  if (methods.length !== 1) return null;
  const classes = files[0].scan.classes.filter(cls => cls.name === className);
  if (classes.length !== 1 || classes[0].methods.filter(method => method.name === methodName).length !== 1) return null;
  return { relPath: files[0].relPath, className, methodName };
}

function resolveHookMember({ code, scan, currentRelPath, receiver, methodName, offset, endOffset, resolveModule }) {
  if (!Number.isInteger(offset)) return undefined;
  const model = sourceModel(code, scan, currentRelPath);
  if (model.source.parseDiagnostics.length) return null;
  const expected = receiver ? receiver + '.' + methodName : methodName;
  const calls = [];
  function visit(node) {
    if (ts.isCallExpression(node) && node.getStart(model.source) >= offset && (endOffset === undefined || node.end <= endOffset) && node.expression.getText(model.source).replace(/\s/g, '') === expected) calls.push(node);
    ts.forEachChild(node, visit);
  }
  visit(model.source);
  calls.sort((a, b) => a.getStart(model.source) - b.getStart(model.source));
  if (!calls.length) return undefined;
  const call = calls[0];
  const identifier = rootIdentifier(call.expression);
  if (!identifier) return null;
  const declaration = oneDeclaration(model, identifier);
  if (!declaration) return undefined;
  if (ts.isParameter(declaration) || (ts.isBindingElement(declaration) && ts.isParameter(declaration.parent.parent))) {
    return receiver && receiver === identifier.text && !hasWrites(model, identifier) ? typedParameterTarget(model, declaration, methodName, resolveModule) : null;
  }
  let variable = declaration;
  let property = receiver ? methodName : null;
  if (ts.isBindingElement(declaration)) {
    if (declaration.dotDotDotToken || declaration.initializer) return null;
    const pattern = declaration.parent;
    property = ts.isObjectBindingPattern(pattern) ? (declaration.propertyName || declaration.name).text : pattern.elements.indexOf(declaration);
    variable = pattern.parent;
  }
  if (!ts.isVariableDeclaration(variable)) {
    if (ts.isImportSpecifier(variable) || ts.isImportClause(variable) || ts.isNamespaceImport(variable) || ts.isClassDeclaration(variable)) return undefined;
    return receiver ? null : callableTarget(model, declaration, currentRelPath);
  }
  if (!(variable.parent.flags & ts.NodeFlags.Const) || hasWrites(model, identifier)) return null;
  const initializer = unwrap(variable.initializer);
  if (!initializer || !ts.isCallExpression(initializer)) return receiver ? undefined : callableTarget(model, variable, currentRelPath);
  const source = unwrap(initializer.expression);
  if (!ts.isIdentifier(source)) return undefined;
  const imported = oneDeclaration(model, source);
  if (!imported || !ts.isImportSpecifier(imported)) return null;
  const hookName = (imported.propertyName || imported.name).text;
  if (!/^use[A-Z]/.test(hookName)) return undefined;
  if (receiver && receiver !== identifier.text) return null;
  if (property === null) return null;
  const importDeclaration = imported.parent.parent.parent;
  if (!ts.isImportDeclaration(importDeclaration) || !ts.isStringLiteral(importDeclaration.moduleSpecifier)) return null;
  const files = resolveModule(importDeclaration.moduleSpecifier.text);
  if (files.length !== 1) return null;
  const file = files[0];
  const target = sourceModel(file.code, file.scan, file.relPath);
  if (target.source.parseDiagnostics.length) return null;
  const module = target.checker.getSymbolAtLocation(target.source);
  if (!module) return null;
  const exports = target.checker.getExportsOfModule(module);
  const hook = exports.find(symbol => symbol.name === hookName);
  return hook?.declarations?.length === 1 ? returnedMember(target, hook.declarations[0], property, file.relPath) : null;
}

module.exports = { resolveHookMember };
