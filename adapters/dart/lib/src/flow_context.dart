import 'dart:convert';
import 'package:analyzer/dart/ast/ast.dart';
import 'package:analyzer/dart/ast/visitor.dart';

/// Syntax proof comes only from parsing the exact snapshot bytes. This does
/// not resolve imports or consult the live filesystem.
class SnapshotSyntax {
  SnapshotSyntax(CompilationUnit unit, {required this.valid}) {
    unit.accept(_Nodes(nodes));
    var token = unit.beginToken;
    while (!token.isEof) {
      tokenStarts.add(token.offset);
      tokenEnds.add(token.end);
      token = token.next!;
    }
  }

  final bool valid;
  final nodes = <AstNode>[];
  final tokenStarts = <int>{};
  final tokenEnds = <int>{};

  bool isCodeRange(int start, int end) =>
      tokenStarts.contains(start) && tokenEnds.contains(end);

  Map<String, Object?>? contextFor({
    required String source,
    required String path,
    required String? snapshotId,
    required String sourceHash,
    required int start,
    required int end,
  }) {
    if (!valid || snapshotId == null || snapshotId.isEmpty) return null;
    // Control headers and selected expression steps are executable when their
    // exact token range matches the parsed AST. Other fragments require a
    // complete statement.
    final matches = nodes.where(
      (n) =>
          (n is IfStatement &&
              n.ifKeyword.offset == start &&
              n.rightParenthesis.end == end) ||
          (n is SwitchMember &&
              n.labels.isEmpty &&
              n.keyword.offset == start &&
              n.colon.end == end) ||
          (n is ForStatement &&
              n.forKeyword.offset == start &&
              n.rightParenthesis.end == end &&
              n.forLoopParts is ForEachParts) ||
          (n.offset == start &&
              ((n.end == end &&
                      (n is ExpressionStatement ||
                          n is ReturnStatement ||
                          n is VariableDeclarationStatement ||
                          n is AssertStatement ||
                          n is BreakStatement ||
                          n is ContinueStatement ||
                          _isDirectAssignedCall(n) ||
                          _isDirectArgumentCall(n) ||
                          _isEagerBinaryOperandCall(n) ||
                          _isDirectShortCircuitOperand(n) ||
                          _isExpressionBodyReturn(n) ||
                          _isConditionCall(n) ||
                          _isLoopExpression(n))) ||
                  _isForInitializer(n, end, source))),
    );
    if (matches.length != 1) return null;
    final statement = matches.single;
    AstNode? structure = statement is IfStatement || statement is ForStatement
        ? statement
        : null;
    String? kind = statement is IfStatement
        ? 'condition'
        : statement is ForStatement
        ? 'loop'
        : null;
    AstNode? callable;
    FunctionBody? body;
    var returnCanCompleteDirectly =
        statement is ReturnStatement || _isExpressionBodyReturn(statement);
    for (AstNode? node = statement.parent; node != null; node = node.parent) {
      if (node is TryStatement &&
          node.finallyBlock != null &&
          (statement.offset < node.finallyBlock!.offset ||
              statement.end > node.finallyBlock!.end)) {
        returnCanCompleteDirectly = false;
      }
      if (node is FunctionExpression && node.parent is! FunctionDeclaration) {
        returnCanCompleteDirectly = false;
      }
      if (structure == null) {
        if (node is IfStatement ||
            node is SwitchStatement ||
            node is ConditionalExpression) {
          structure = node;
          kind = 'condition';
        } else if (node is ForStatement ||
            node is WhileStatement ||
            node is DoStatement) {
          structure = node;
          kind = 'loop';
        } else if (node is FunctionExpression &&
            node.parent is! FunctionDeclaration) {
          structure = node;
          kind = 'callback';
          final parent = node.parent;
          if (parent is NamedArgument) {
            structure = parent;
            if (parent.name.lexeme.toLowerCase().contains('builder')) {
              kind = 'builder';
            }
          }
        }
      }
      if (node is MethodDeclaration) {
        callable = node;
        body = node.body;
        break;
      }
      if (node is FunctionDeclaration) {
        callable = node;
        body = node.functionExpression.body;
        break;
      }
      if (node is ConstructorDeclaration) {
        callable = node;
        body = node.body;
        break;
      }
    }
    if (callable == null || body == null) return null;
    int byte(int offset) => utf8.encode(source.substring(0, offset)).length;
    List<int> range(AstNode node) => [byte(node.offset), byte(node.end)];
    List<int> lines(int start, int end) => [
      '\n'.allMatches(source.substring(0, start)).length + 1,
      '\n'.allMatches(source.substring(0, end - 1)).length + 1,
    ];
    return {
      'snapshotId': snapshotId,
      'sourceHash': sourceHash,
      'canonicalPath': path,
      'statement': {
        'nodeKind': statement is SwitchMember
            ? 'control_header'
            : _isDirectAssignedCall(statement) ||
                  _isDirectArgumentCall(statement) ||
                  _isEagerBinaryOperandCall(statement) ||
                  _isDirectShortCircuitOperand(statement) ||
                  _isExpressionBodyReturn(statement) ||
                  _isConditionCall(statement) ||
                  _isLoopExpression(statement)
            ? 'expression'
            : 'statement',
        'directReturnCompletion': returnCanCompleteDirectly,
        'byteRange': [byte(start), byte(end)],
        'lineRange': lines(start, end),
      },
      'structuralContext': structure == null
          ? {'status': 'none'}
          : {
              'status': 'present',
              'nodeKind': kind,
              'byteRange': range(structure),
              'lineRange': lines(structure.offset, structure.end),
            },
      'callable': {
        'signature': source.substring(callable.offset, body.offset).trim(),
        'completionMode': body.isAsynchronous || body.isGenerator
            ? 'deferred'
            : 'synchronous',
        'signatureByteRange': [byte(callable.offset), byte(body.offset)],
        'signatureLineRange': lines(callable.offset, body.offset),
        'byteRange': range(callable),
        'lineRange': lines(callable.offset, callable.end),
      },
    };
  }
}

bool _isDirectAssignedCall(AstNode node) {
  if (node is! MethodInvocation && node is! FunctionExpressionInvocation) {
    return false;
  }
  final parent = node.parent;
  return (parent is VariableDeclaration &&
          identical(parent.initializer, node)) ||
      (parent is AssignmentExpression &&
          parent.operator.lexeme == '=' &&
          identical(parent.rightHandSide, node));
}

bool _isDirectArgumentCall(AstNode node) {
  if (node is! MethodInvocation && node is! FunctionExpressionInvocation) {
    return false;
  }
  final parent = node.parent;
  if (parent is ArgumentList) return true;
  return parent is NamedArgument && parent.parent is ArgumentList;
}

bool _isEagerBinaryOperandCall(AstNode node) {
  if (node is! MethodInvocation && node is! FunctionExpressionInvocation) {
    return false;
  }
  final parent = node.parent;
  if (parent is! BinaryExpression ||
      const {'&&', '||', '??'}.contains(parent.operator.lexeme) ||
      !identical(parent.leftOperand, node) &&
          !identical(parent.rightOperand, node)) {
    return false;
  }
  final owner = parent.parent;
  return owner is VariableDeclaration && identical(owner.initializer, parent) ||
      owner is AssignmentExpression &&
          owner.operator.lexeme == '=' &&
          identical(owner.rightHandSide, parent);
}

bool _isDirectShortCircuitOperand(AstNode node) {
  if (node is! SimpleIdentifier &&
      node is! MethodInvocation &&
      node is! FunctionExpressionInvocation) {
    return false;
  }
  final parent = node.parent;
  if (parent is! BinaryExpression ||
      !const {'&&', '||', '??'}.contains(parent.operator.lexeme) ||
      !identical(parent.leftOperand, node) &&
          !identical(parent.rightOperand, node)) {
    return false;
  }
  final owner = parent.parent;
  return owner is VariableDeclaration && identical(owner.initializer, parent) ||
      owner is AssignmentExpression &&
          owner.operator.lexeme == '=' &&
          identical(owner.rightHandSide, parent);
}

bool _isExpressionBodyReturn(AstNode node) =>
    node is Expression &&
    node.parent is ExpressionFunctionBody &&
    identical((node.parent as ExpressionFunctionBody).expression, node);

bool _isConditionCall(AstNode node) {
  if (node is! MethodInvocation && node is! FunctionExpressionInvocation) {
    return false;
  }
  for (AstNode? parent = node.parent; parent != null; parent = parent.parent) {
    if (parent is IfStatement) {
      return node.offset >= parent.leftParenthesis.end &&
          node.end <= parent.rightParenthesis.offset;
    }
    if (parent is Statement) return false;
  }
  return false;
}

bool _isLoopExpression(AstNode node) {
  if (node is! Expression) return false;
  final parent = node.parent;
  return (parent is ForParts &&
          (identical(parent.condition, node) ||
              parent.updaters.contains(node))) ||
      (parent is WhileStatement && identical(parent.condition, node)) ||
      (parent is DoStatement && identical(parent.condition, node));
}

bool _isForInitializer(AstNode node, int end, String source) =>
    node is VariableDeclarationList &&
    node.parent is ForPartsWithDeclarations &&
    node.end + 1 == end &&
    node.end < source.length &&
    source[node.end] == ';';

class _Nodes extends GeneralizingAstVisitor<void> {
  _Nodes(this.nodes);
  final List<AstNode> nodes;
  @override
  void visitNode(AstNode node) {
    nodes.add(node);
    super.visitNode(node);
  }
}
