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
    // A header, expression fragment or whole control structure is not proof
    // of the selected executable statement.
    final matches = nodes.where((n) =>
        n.offset == start &&
        n.end == end &&
        (n is ExpressionStatement ||
            n is ReturnStatement ||
            n is VariableDeclarationStatement ||
            n is AssertStatement ||
            n is BreakStatement ||
            n is ContinueStatement));
    if (matches.length != 1) return null;
    final statement = matches.single;
    AstNode? structure;
    String? kind;
    AstNode? callable;
    FunctionBody? body;
    for (AstNode? node = statement.parent; node != null; node = node.parent) {
      if (structure == null) {
        if (node is IfStatement ||
            node is SwitchStatement ||
            node is ConditionalExpression) {
          structure = node;
          kind = 'condition';
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
        'nodeKind': 'statement',
        'byteRange': range(statement),
        'lineRange': lines(statement.offset, statement.end),
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
        'signatureByteRange': [byte(callable.offset), byte(body.offset)],
        'signatureLineRange': lines(callable.offset, body.offset),
        'byteRange': range(callable),
        'lineRange': lines(callable.offset, callable.end),
      },
    };
  }
}

class _Nodes extends GeneralizingAstVisitor<void> {
  _Nodes(this.nodes);
  final List<AstNode> nodes;
  @override
  void visitNode(AstNode node) {
    nodes.add(node);
    super.visitNode(node);
  }
}
