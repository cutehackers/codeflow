import 'dart:convert';
import 'package:analyzer/dart/ast/ast.dart';
import 'package:analyzer/dart/ast/visitor.dart';
import 'package:analyzer/dart/analysis/utilities.dart';
import 'package:test/test.dart';
import 'package:codeflow_dart_adapter/codeflow_dart_adapter.dart';

Map<String, Object?> slice(
  String source, {
  String? snapshotId = 'snapshot-test',
}) => sliceCandidate(
  repoRoot: '/unused',
  candidateId: 'test',
  entrySymbolPath: 'lib/service.dart#Service.run',
  snapshotId: snapshotId,
  contentOverlay: {'pubspec.yaml': 'name: test\n', 'lib/service.dart': source},
);

void main() {
  test(
    'awaited permission guard and catch headers retain exact source proof',
    () {
      const source = '''
class Service {
  Future<void> run() async {
    try {
      if (!await checkPermission()) return;
    } on Object {
      return;
    } catch (error) {
      return;
    }
  }
  Future<bool> checkPermission() async => true;
}
''';
      final steps = (slice(source)['steps'] as List).cast<Map>();
      String selectedText(Map step) {
        final range = (step['anchor']['byteRange'] as List).cast<int>();
        return utf8.decode(utf8.encode(source).sublist(range[0], range[1]));
      }

      final selected = steps
          .where(
            (step) => const {
              'await checkPermission()',
              'on Object {',
              'catch (error) {',
            }.contains(selectedText(step)),
          )
          .toList();
      expect(selected, hasLength(3));
      for (final step in selected) {
        final context = step['flowContext'] as Map;
        final statement = context['statement'] as Map;
        expect(statement['byteRange'], step['anchor']['byteRange']);
        expect(
          statement['nodeKind'],
          selectedText(step).startsWith('await')
              ? 'expression'
              : 'control_header',
        );
        final structure = context['structuralContext'] as Map;
        expect(structure['status'], 'present');
        expect(structure['nodeKind'], 'condition');
        final range = (structure['byteRange'] as List).cast<int>();
        final span = (statement['byteRange'] as List).cast<int>();
        expect(range[0], lessThanOrEqualTo(span[0]));
        expect(range[1], greaterThan(span[1]));
        expect(context['sourceHash'], step['anchor']['fileHash']);
        expect(context['snapshotId'], 'snapshot-test');
      }
    },
  );

  test('callable completion distinguishes direct and deferred bodies', () {
    const source = '''
class Service {
  int run() {
    return 1;
  }
  Future<int> later() async {
    return 2;
  }
}
''';
    final result = slice(source);
    final steps = (result['steps'] as List).cast<Map>();
    final direct = steps.firstWhere(
      (step) => step['kind'] == 'return' && step['symbolPath'] == 'Service.run',
    );
    expect(direct['flowContext']['callable']['completionMode'], 'synchronous');
    expect(
      direct['flowContext']['statement']['directReturnCompletion'],
      isTrue,
    );
    final deferredResult = sliceCandidate(
      repoRoot: '/unused',
      candidateId: 'later',
      entrySymbolPath: 'lib/service.dart#Service.later',
      snapshotId: 'snapshot-test',
      contentOverlay: {
        'pubspec.yaml': 'name: test\n',
        'lib/service.dart': source,
      },
    );
    final deferred = (deferredResult['steps'] as List).cast<Map>().firstWhere(
      (step) =>
          step['kind'] == 'return' && step['symbolPath'] == 'Service.later',
    );
    expect(deferred['flowContext']['callable']['completionMode'], 'deferred');
  });

  test('return through finally cannot directly resume its caller', () {
    const source = '''
class Service {
  int run() {
    try {
      return 1;
    } finally {
      print('cleanup');
    }
  }
}
''';
    final result = slice(source);
    final steps = (result['steps'] as List).cast<Map>();
    final returned = steps.firstWhere((step) => step['kind'] == 'return');
    expect(
      returned['flowContext']['callable']['completionMode'],
      'synchronous',
    );
    expect(
      returned['flowContext']['statement']['directReturnCompletion'],
      isFalse,
    );
  });

  test(
    'return in finally can complete directly after overriding try return',
    () {
      const source = '''
class Service {
  int run() {
    try {
      return 1;
    } finally {
      return 2;
    }
  }
}
''';
      final steps = (slice(source)['steps'] as List).cast<Map>();
      final returns = steps.where((step) => step['kind'] == 'return').toList();
      expect(returns, hasLength(2));
      expect(
        returns[0]['flowContext']['statement']['directReturnCompletion'],
        isFalse,
      );
      expect(
        returns[1]['flowContext']['statement']['directReturnCompletion'],
        isTrue,
      );
    },
  );

  test('simple switch case labels have exact source context', () {
    const source = '''
enum Owner { first, second }
class Service {
  int run(Owner owner) {
    switch (owner) {
      case Owner.first:
        return 1;
      case Owner.second:
        return 2;
    }
  }
}
''';
    final steps = (slice(source)['steps'] as List).cast<Map>();
    final cases = steps.where((step) {
      final range = (step['anchor']['byteRange'] as List).cast<int>();
      return utf8
          .decode(utf8.encode(source).sublist(range[0], range[1]))
          .startsWith('case ');
    }).toList();
    expect(cases, hasLength(2));
    for (final step in cases) {
      final anchor = step['anchor'] as Map;
      final context = step['flowContext'] as Map;
      expect(context['statement']['nodeKind'], 'control_header');
      expect(context['statement']['byteRange'], anchor['byteRange']);
      expect(context['structuralContext']['nodeKind'], 'condition');
    }
  });

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
    final serialized = (result['steps'] as List)
        .map((dynamic step) {
          final range = (step['anchor']['byteRange'] as List).cast<int>();
          return utf8.decode(utf8.encode(source).sublist(range[0], range[1]));
        })
        .join('\n');
    expect(serialized, isNot(contains('deletedCall')));
    expect(serialized, isNot(contains('blockComment')));
    expect(serialized, contains('final text = "client.stringCall();";'));
    expect(serialized, contains('execute'));
    final steps = (result['steps'] as List).cast<Map>();
    expect(
      steps.where((step) => step['kind'] == 'call').any((step) {
        final range = (step['anchor']['byteRange'] as List).cast<int>();
        return utf8
            .decode(utf8.encode(source).sublist(range[0], range[1]))
            .contains('stringCall');
      }),
      isFalse,
    );
    final guard = steps.firstWhere((s) => s['kind'] == 'guard');
    final context = guard['flowContext'] as Map<String, Object?>;
    final statement = context['statement'] as Map<String, Object?>;
    final range = (statement['byteRange'] as List).cast<int>();
    expect(
      utf8.decode(utf8.encode(source).sublist(range[0], range[1])),
      'if (ready)',
    );
    final structure = context['structuralContext'] as Map<String, Object?>;
    expect(structure['nodeKind'], 'condition');
  });

  test(
    'every exact range equals a parsed statement in original UTF-8 source',
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
        final node = statements.nodes.singleWhere(
          (n) =>
              utf8.encode(source.substring(0, n.offset)).length == range[0] &&
              utf8
                      .encode(
                        source.substring(
                          0,
                          n is IfStatement ? n.rightParenthesis.end : n.end,
                        ),
                      )
                      .length ==
                  range[1],
        );
        expect(
          node,
          anyOf(
            isA<ExpressionStatement>(),
            isA<IfStatement>(),
            isA<VariableDeclarationStatement>(),
          ),
        );
        expect(meta['snapshotId'], 'snapshot-test');
        if (node is IfStatement) {
          expect(meta['structuralContext']['nodeKind'], 'condition');
        }
        proven++;
      }
      expect(proven, greaterThan(0));
    },
  );

  test('missing snapshot or invalid syntax never produces exact metadata', () {
    const source = 'class Service { void run() { client.execute(); } }';
    for (final result in [slice(source, snapshotId: null)]) {
      for (final dynamic step in result['steps'] as List) {
        expect(step['flowContext'], isNull);
      }
    }
    expect(
      () => slice(source.substring(0, source.length - 1)),
      throwsA(isA<ArgumentError>()),
      reason: 'A malformed source must not become a full-file fallback step.',
    );
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
