import 'dart:convert';
import 'package:analyzer/dart/ast/ast.dart';
import 'package:analyzer/dart/ast/visitor.dart';
import 'package:analyzer/dart/analysis/utilities.dart';
import 'package:test/test.dart';
import 'package:codeflow_dart_adapter/codeflow_dart_adapter.dart';

Map<String, Object?> slice(String source,
        {String? snapshotId = 'snapshot-test'}) =>
    sliceCandidate(
        repoRoot: '/unused',
        candidateId: 'test',
        entrySymbolPath: 'lib/service.dart#Service.run',
        snapshotId: snapshotId,
        contentOverlay: {
          'pubspec.yaml': 'name: test\n',
          'lib/service.dart': source
        });

void main() {
  test('comments and strings never become semantic steps', () {
    const source = '''
class Service {
  void run() {
    // client.deletedCall();
    /* client.blockComment(); */
    final text = "client.stringCall();";
    if (ready) {
      client.execute();
    }
  }
}
''';
    final result = slice(source);
    final serialized = (result['steps'] as List).map((dynamic step) {
      final range = (step['anchor']['byteRange'] as List).cast<int>();
      return utf8.decode(utf8.encode(source).sublist(range[0], range[1]));
    }).join('\n');
    expect(serialized, isNot(contains('deletedCall')));
    expect(serialized, isNot(contains('blockComment')));
    expect(serialized, isNot(contains('stringCall')));
    expect(serialized, contains('execute'));
    final steps = (result['steps'] as List).cast<Map>();
    final guard = steps.firstWhere((s) => s['kind'] == 'guard');
    expect(guard['flowContext'], isNull,
        reason: 'if header is not an executable statement node');
  });

  test('every exact range equals a parsed statement in original UTF-8 source',
      () {
    const source = '''
class Service {
  void run() {
    final label = '설정 🔒';
    items.forEach((item) {
      if (ready) client.execute();
    });
  }
}
''';
    final unit = parseString(content: source).unit;
    final statements = _Statements();
    unit.accept(statements);
    final result = slice(source);
    var proven = 0;
    for (final dynamic step in result['steps'] as List) {
      final dynamic meta = step['flowContext'];
      if (meta == null) continue;
      final range = (meta['statement']['byteRange'] as List).cast<int>();
      final node = statements.nodes.singleWhere((n) =>
          utf8.encode(source.substring(0, n.offset)).length == range[0] &&
          utf8.encode(source.substring(0, n.end)).length == range[1]);
      expect(node, isA<ExpressionStatement>());
      expect(meta['snapshotId'], 'snapshot-test');
      expect(meta['structuralContext']['nodeKind'], 'condition');
      proven++;
    }
    expect(proven, greaterThan(0));
  });

  test('missing snapshot or invalid syntax never produces exact metadata', () {
    const source = 'class Service { void run() { client.execute(); } }';
    for (final result in [
      slice(source, snapshotId: null),
      slice(source.substring(0, source.length - 1))
    ]) {
      for (final dynamic step in result['steps'] as List) {
        expect(step['flowContext'], isNull);
      }
    }
  });
}

class _Statements extends GeneralizingAstVisitor<void> {
  final nodes = <Statement>[];
  @override
  void visitNode(AstNode node) {
    if (node is Statement) nodes.add(node);
    super.visitNode(node);
  }
}
