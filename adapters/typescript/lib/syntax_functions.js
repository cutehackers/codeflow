'use strict';

const ts = require('../vendor/typescript/typescript');

function parseSource(source, fileName) {
  if (fileName) return ts.createSourceFile(fileName, source, ts.ScriptTarget.Latest, true);
  const plain = ts.createSourceFile('source.ts', source, ts.ScriptTarget.Latest, true);
  if (!plain.parseDiagnostics.length) return plain;
  const jsx = ts.createSourceFile('source.tsx', source, ts.ScriptTarget.Latest, true);
  return jsx.parseDiagnostics.length < plain.parseDiagnostics.length ? jsx : plain;
}

function scanSyntaxFunctions(source, fileName) {
  const tree = parseSource(source, fileName);
  const functions = [];
  function namedCallable(expression) {
    if (!expression) return null;
    while (ts.isParenthesizedExpression(expression) || ts.isAsExpression(expression) || ts.isSatisfiesExpression(expression)) expression = expression.expression;
    if (ts.isCallExpression(expression) && /^(?:React\.)?(?:useCallback|memo|forwardRef|observer)$/.test(expression.expression.getText(tree))) expression = expression.arguments[0];
    return expression && ts.isFunctionLike(expression) ? expression : null;
  }
  function register(fn, name, parentScope) {
    if (!fn.body) return;
    const block = ts.isBlock(fn.body);
    const fullName = parentScope ? parentScope + '.' + name : name;
    functions.push({ name: fullName, localName: name, parentScope, bodyStart: fn.body.getStart(tree) + (block ? 1 : 0), bodyEnd: fn.body.end - (block ? 1 : 0), isAsync: !!fn.modifiers?.some(modifier => modifier.kind === ts.SyntaxKind.AsyncKeyword) });
    ts.forEachChild(fn.body, child => visit(child, fullName));
  }
  function objectMembers(object, owner) {
    for (const property of object.properties) {
      if (!property.name || !ts.isIdentifier(property.name)) continue;
      const name = property.name.text;
      if (ts.isMethodDeclaration(property)) register(property, name, owner);
      if (!ts.isPropertyAssignment(property)) continue;
      const value = property.initializer;
      if (ts.isObjectLiteralExpression(value)) objectMembers(value, owner + '.' + name);
      else {
        const fn = namedCallable(value);
        if (fn) register(fn, name, owner);
      }
    }
  }
  function visit(node, owner) {
    if (ts.isClassLike(node)) return;
    if (ts.isFunctionDeclaration(node) && node.name) { register(node, node.name.text, owner); return; }
    if (ts.isVariableDeclaration(node) && ts.isIdentifier(node.name)) {
      const fn = namedCallable(node.initializer);
      if (fn) { register(fn, node.name.text, owner); return; }
      if (node.initializer && ts.isObjectLiteralExpression(node.initializer)) {
        objectMembers(node.initializer, owner ? owner + '.' + node.name.text : node.name.text);
        return;
      }
    }
    // An anonymous callback is not a named invocation target.
    if (ts.isFunctionLike(node)) return;
    ts.forEachChild(node, child => visit(child, owner));
  }
  visit(tree, '');
  return { functions, tree };
}

module.exports = { scanSyntaxFunctions, parseSource };
