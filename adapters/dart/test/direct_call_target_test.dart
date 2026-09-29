import 'package:codeflow_dart_adapter/codeflow_dart_adapter.dart';
import 'package:test/test.dart';

void main() {
  test('typed parameters resolve their imported instance methods', () {
    const rulesPath = 'lib/rules.dart';
    const statePath = 'lib/state.dart';
    const positionPath = 'lib/position.dart';
    const rules = '''
import 'state.dart';
import 'position.dart';
class Rules {
  int run(GameState state, Position from) {
    final owner = state.ownerAt(from);
    final distance = from.distanceTo(Position());
    return owner + distance;
  }
}
''';
    const state = '''
class GameState {
  int ownerAt(Object position) { return 1; }
}
''';
    const position = '''
class Position {
  int distanceTo(Position other) { return 2; }
}
''';
    final result = sliceCandidate(
      repoRoot: '/unused',
      candidateId: 'cand-typed-parameter',
      entrySymbolPath: '$rulesPath#Rules.run',
      snapshotId: 'snapshot-typed-parameter',
      contentOverlay: {
        rulesPath: rules,
        statePath: state,
        positionPath: position,
        'pubspec.yaml': 'name: typed_parameter\n',
      },
    );
    final edges = (result['edges'] as List).cast<Map<String, Object?>>();
    expect(
      edges.any(
        (edge) =>
            edge['kind'] == 'resolved_cross_file' &&
            edge['toSymbolPath'] == '$statePath#GameState.ownerAt',
      ),
      isTrue,
    );
    expect(
      edges.any(
        (edge) =>
            edge['kind'] == 'resolved_cross_file' &&
            edge['toSymbolPath'] == '$positionPath#Position.distanceTo',
      ),
      isTrue,
    );
    expect(
      edges.where(
        (edge) =>
            edge['kind'] == 'unknown_edge' &&
            (edge['toSymbolPath'] as String).contains('unresolved_dynamic.'),
      ),
      isEmpty,
    );
  });

  test('another method parameter cannot type the current receiver', () {
    const path = 'lib/controller.dart';
    const source = '''
class Service { void process() {} }
class Controller {
  void unrelated(Service delegate) {}
  void run(dynamic delegate) { delegate.process(); }
}
''';
    final result = sliceCandidate(
      repoRoot: '/unused',
      candidateId: 'cand-other-method-parameter',
      entrySymbolPath: '$path#Controller.run',
      snapshotId: 'snapshot-other-method-parameter',
      contentOverlay: {path: source, 'pubspec.yaml': 'name: other_method\n'},
    );
    final edges = (result['edges'] as List).cast<Map<String, Object?>>();
    expect(
      edges.any((edge) => edge['toSymbolPath'] == '$path#Service.process'),
      isFalse,
    );
    expect(
      edges.any(
        (edge) =>
            edge['kind'] == 'unknown_edge' &&
            (edge['toSymbolPath'] as String).contains(
              'unresolved_dynamic.process',
            ),
      ),
      isTrue,
    );
  });

  test('hidden, prefixed, or ambiguous parameter types remain unresolved', () {
    const entryPath = 'lib/controller.dart';
    const firstPath = 'lib/first.dart';
    const secondPath = 'lib/second.dart';
    const service = 'class Service { void process() {} }\n';
    for (final imports in [
      "import 'first.dart' hide Service;",
      "import 'first.dart' as first;",
      "import 'first.dart';\nimport 'second.dart';",
    ]) {
      final entry =
          '''
$imports
class Controller {
  void run(Service delegate) { delegate.process(); }
}
''';
      final result = sliceCandidate(
        repoRoot: '/unused',
        candidateId: 'cand-import-visibility',
        entrySymbolPath: '$entryPath#Controller.run',
        snapshotId: 'snapshot-import-visibility',
        contentOverlay: {
          entryPath: entry,
          firstPath: service,
          secondPath: service,
          'pubspec.yaml': 'name: import_visibility\n',
        },
      );
      final edges = (result['edges'] as List).cast<Map<String, Object?>>();
      expect(
        edges.any(
          (edge) =>
              (edge['toSymbolPath'] as String).endsWith('#Service.process'),
        ),
        isFalse,
        reason: imports,
      );
      expect(
        edges.any(
          (edge) =>
              edge['kind'] == 'unknown_edge' &&
              edge['resolutionStatus'] == 'unresolved_type' &&
              (edge['toSymbolPath'] as String).contains('process'),
        ),
        isTrue,
        reason: imports,
      );
    }
  });

  test('field types obey imported type visibility', () {
    const entryPath = 'lib/controller.dart';
    const service = 'class Service { void process() {} }\n';
    for (final imports in [
      "import 'first.dart' hide Service;",
      "import 'first.dart' as first;",
      "import 'first.dart';\nimport 'second.dart';",
    ]) {
      final result = sliceCandidate(
        repoRoot: '/unused',
        candidateId: 'cand-field-visibility',
        entrySymbolPath: '$entryPath#Controller.run',
        snapshotId: 'snapshot-field-visibility',
        contentOverlay: {
          entryPath:
              '''
$imports
class Controller {
  final Service delegate;
  Controller(this.delegate);
  void run() { delegate.process(); }
}
''',
          'lib/first.dart': service,
          'lib/second.dart': service,
          'pubspec.yaml': 'name: field_visibility\n',
        },
      );
      final edges = (result['edges'] as List).cast<Map<String, Object?>>();
      expect(
        edges.any(
          (edge) =>
              (edge['toSymbolPath'] as String).endsWith('#Service.process'),
        ),
        isFalse,
        reason: imports,
      );
      expect(
        edges.any(
          (edge) =>
              edge['kind'] == 'unknown_edge' &&
              edge['resolutionStatus'] == 'unresolved_type',
        ),
        isTrue,
        reason: imports,
      );
    }
  });

  test('callable fields obey imported type visibility', () {
    const entryPath = 'lib/controller.dart';
    const service = 'class Service { void call() {} }\n';
    for (final imports in [
      "import 'first.dart' hide Service;",
      "import 'first.dart' as first;",
      "import 'first.dart';\nimport 'second.dart';",
    ]) {
      final result = sliceCandidate(
        repoRoot: '/unused',
        candidateId: 'cand-callable-visibility',
        entrySymbolPath: '$entryPath#Controller.run',
        snapshotId: 'snapshot-callable-visibility',
        contentOverlay: {
          entryPath:
              '''
$imports
class Controller {
  final Service delegate;
  Controller(this.delegate);
  void run() { delegate(); }
}
''',
          'lib/first.dart': service,
          'lib/second.dart': service,
          'pubspec.yaml': 'name: callable_visibility\n',
        },
      );
      final edges = (result['edges'] as List).cast<Map<String, Object?>>();
      expect(
        edges.any(
          (edge) => (edge['toSymbolPath'] as String).endsWith('#Service.call'),
        ),
        isFalse,
        reason: imports,
      );
    }
  });

  test('an unqualified call never targets an imported class method', () {
    const entryPath = 'lib/controller.dart';
    final result = sliceCandidate(
      repoRoot: '/unused',
      candidateId: 'cand-imported-method-name',
      entrySymbolPath: '$entryPath#Controller.run',
      snapshotId: 'snapshot-imported-method-name',
      contentOverlay: {
        entryPath: '''
import 'service.dart';
class Controller { void run() { process(); } }
''',
        'lib/service.dart': 'class Controller { void process() {} }\n',
        'pubspec.yaml': 'name: imported_method_name\n',
      },
    );
    final edges = (result['edges'] as List).cast<Map<String, Object?>>();
    expect(
      edges.any(
        (edge) => edge['toSymbolPath'] == 'lib/service.dart#Controller.process',
      ),
      isFalse,
    );
    expect(
      edges.any(
        (edge) =>
            edge['kind'] == 'unknown_edge' &&
            edge['resolutionStatus'] == 'unresolved_dynamic',
      ),
      isTrue,
    );
  });

  test('unqualified calls resolve one visible imported top-level function', () {
    const entryPath = 'lib/controller.dart';
    const servicePath = 'lib/service.dart';
    const service = 'int process() { return 1; }\n';
    for (final imports in [
      "import 'service.dart';",
      "import 'service.dart' hide process;",
      "import 'service.dart' as service;",
      "import 'service.dart';\nimport 'other.dart';",
    ]) {
      final result = sliceCandidate(
        repoRoot: '/unused',
        candidateId: 'cand-top-level-import',
        entrySymbolPath: '$entryPath#Controller.run',
        snapshotId: 'snapshot-top-level-import',
        contentOverlay: {
          entryPath:
              '''
$imports
class Controller { void run() { process(); } }
''',
          servicePath: service,
          'lib/other.dart': service,
          'pubspec.yaml': 'name: top_level_import\n',
        },
      );
      final edges = (result['edges'] as List).cast<Map<String, Object?>>();
      final targets = edges
          .where((edge) => edge['kind'] == 'resolved_cross_file')
          .map((edge) => edge['toSymbolPath'])
          .toList();
      if (imports == "import 'service.dart';") {
        expect(targets, contains('$servicePath#process'), reason: imports);
      } else {
        expect(
          targets,
          isNot(contains('$servicePath#process')),
          reason: imports,
        );
      }
    }
  });

  test('static calls require a visible static declaration', () {
    const entryPath = 'lib/controller.dart';
    for (final imports in [
      "import 'service.dart';",
      "import 'service.dart' hide Service;",
      "import 'service.dart' as service;",
      "import 'service.dart';\nimport 'other.dart';",
    ]) {
      final result = sliceCandidate(
        repoRoot: '/unused',
        candidateId: 'cand-static-visibility',
        entrySymbolPath: '$entryPath#Controller.run',
        snapshotId: 'snapshot-static-visibility',
        contentOverlay: {
          entryPath:
              '''
$imports
class Controller { void run() { Service.process(); } }
''',
          'lib/service.dart':
              'class Service { static int process() { return 1; } }\n',
          'lib/other.dart':
              'class Service { static int process() { return 2; } }\n',
          'pubspec.yaml': 'name: static_visibility\n',
        },
      );
      final edges = (result['edges'] as List).cast<Map<String, Object?>>();
      final resolved = edges.any(
        (edge) =>
            edge['resolutionStatus'] == 'resolved' &&
            edge['toSymbolPath'] == 'lib/service.dart#Service.process',
      );
      expect(
        resolved,
        imports == "import 'service.dart';",
        reason: '$imports: $edges',
      );
    }
    final instanceOnly = sliceCandidate(
      repoRoot: '/unused',
      candidateId: 'cand-instance-static-call',
      entrySymbolPath: '$entryPath#Controller.run',
      snapshotId: 'snapshot-instance-static-call',
      contentOverlay: {
        entryPath: '''
import 'service.dart';
class Controller {
  void run() { Service.process(); }
}
''',
        'lib/service.dart': 'class Service { int process() { return 1; } }\n',
        'pubspec.yaml': 'name: instance_static_call\n',
      },
    );
    final edges = (instanceOnly['edges'] as List).cast<Map<String, Object?>>();
    expect(
      edges.any(
        (edge) =>
            edge['resolutionStatus'] == 'resolved' &&
            edge['toSymbolPath'] == 'lib/service.dart#Service.process',
      ),
      isFalse,
    );
  });

  test('known const primary constructor is not an unresolved call', () {
    const entryPath = 'lib/controller.dart';
    const modelPath = 'lib/bacteria_move.dart';
    const entry = '''
import 'bacteria_move.dart';
class Controller {
  void run() {
    final moves = <BacteriaMove>[];
    moves.add(BacteriaMove(value: 1));
  }
}
''';
    const model = '''
class const BacteriaMove({required final int value}) {
  int score() => value + 1;
}
''';
    final result = sliceCandidate(
      repoRoot: '/unused',
      candidateId: 'cand-const-constructor',
      entrySymbolPath: '$entryPath#Controller.run',
      snapshotId: 'snapshot-const-constructor',
      contentOverlay: {
        entryPath: entry,
        modelPath: model,
        'pubspec.yaml': 'name: const_constructor\n',
      },
    );
    final steps = (result['steps'] as List).cast<Map<String, Object?>>();
    final edges = (result['edges'] as List).cast<Map<String, Object?>>();
    expect(
      steps.any((step) {
        final range = (step['anchor'] as Map)['byteRange'] as List;
        return entry
            .substring(range[0] as int, range[1] as int)
            .contains('BacteriaMove(value: 1)');
      }),
      isTrue,
    );
    expect(
      edges.where(
        (edge) =>
            edge['kind'] == 'unknown_edge' &&
            (edge['toSymbolPath'] as String).contains('BacteriaMove'),
      ),
      isEmpty,
    );
  });

  test('parameter shadowing an imported class keeps the call unresolved', () {
    const entryPath = 'lib/controller.dart';
    const modelPath = 'lib/bacteria_move.dart';
    const entry = '''
import 'bacteria_move.dart';
class Controller {
  void run(int Function({required int value}) BacteriaMove) {
    final moves = <int>[];
    moves.add(BacteriaMove(value: 1));
  }
}
''';
    const model = '''
class const BacteriaMove({required final int value}) {}
''';
    final result = sliceCandidate(
      repoRoot: '/unused',
      candidateId: 'cand-shadowed-constructor',
      entrySymbolPath: '$entryPath#Controller.run',
      snapshotId: 'snapshot-shadowed-constructor',
      contentOverlay: {
        entryPath: entry,
        modelPath: model,
        'pubspec.yaml': 'name: shadowed_constructor\n',
      },
    );
    final edges = (result['edges'] as List).cast<Map<String, Object?>>();
    expect(
      edges.any(
        (edge) =>
            edge['kind'] == 'unknown_edge' &&
            (edge['toSymbolPath'] as String).contains('BacteriaMove'),
      ),
      isTrue,
    );
  });

  test('a hidden primary constructor is not treated as visible', () {
    const entryPath = 'lib/controller.dart';
    const modelPath = 'lib/bacteria_move.dart';
    const entry = '''
import 'bacteria_move.dart' hide BacteriaMove;
class Controller {
  void run() {
    final moves = <Object>[];
    moves.add(BacteriaMove(value: 1));
  }
}
''';
    const model = '''
class const BacteriaMove({required final int value}) {}
''';
    final result = sliceCandidate(
      repoRoot: '/unused',
      candidateId: 'cand-hidden-constructor',
      entrySymbolPath: '$entryPath#Controller.run',
      snapshotId: 'snapshot-hidden-constructor',
      contentOverlay: {
        entryPath: entry,
        modelPath: model,
        'pubspec.yaml': 'name: hidden_constructor\n',
      },
    );
    final edges = (result['edges'] as List).cast<Map<String, Object?>>();
    expect(
      edges.any(
        (edge) =>
            edge['kind'] == 'unknown_edge' &&
            (edge['toSymbolPath'] as String).contains('BacteriaMove'),
      ),
      isTrue,
    );
  });

  test('untyped receiver never resolves to an imported namesake method', () {
    const entryPath = 'lib/controller.dart';
    const servicePath = 'lib/service.dart';
    const entry = '''
import 'service.dart';
class Controller {
  void run(dynamic delegate) {
    delegate.process();
  }
}
''';
    const service = '''
class Service {
  void process() {}
}
''';
    final result = sliceCandidate(
      repoRoot: '/unused',
      candidateId: 'cand-untyped-receiver',
      entrySymbolPath: '$entryPath#Controller.run',
      snapshotId: 'snapshot-untyped-receiver',
      contentOverlay: {
        entryPath: entry,
        servicePath: service,
        'pubspec.yaml': 'name: untyped_receiver\n',
      },
    );
    final edges = (result['edges'] as List).cast<Map<String, Object?>>();
    expect(
      edges.any(
        (edge) => edge['toSymbolPath'] == '$servicePath#Service.process',
      ),
      isFalse,
    );
    expect(
      edges.any(
        (edge) =>
            edge['kind'] == 'unknown_edge' &&
            (edge['toSymbolPath'] as String).contains(
              'unresolved_dynamic.process',
            ),
      ),
      isTrue,
    );
  });

  test('a field in another class cannot type an untyped receiver', () {
    const path = 'lib/controller.dart';
    const source = '''
class Service {
  void process() {}
}
class Other {
  final Service delegate;
  Other(this.delegate);
}
class Controller {
  void run(dynamic delegate) {
    delegate.process();
  }
}
''';
    final result = sliceCandidate(
      repoRoot: '/unused',
      candidateId: 'cand-foreign-field',
      entrySymbolPath: '$path#Controller.run',
      snapshotId: 'snapshot-foreign-field',
      contentOverlay: {path: source, 'pubspec.yaml': 'name: foreign_field\n'},
    );
    final edges = (result['edges'] as List).cast<Map<String, Object?>>();
    expect(
      edges.any((edge) => edge['toSymbolPath'] == '$path#Service.process'),
      isFalse,
    );
    expect(edges.any((edge) => edge['kind'] == 'unknown_edge'), isTrue);
  });

  test('local and field collection names follow their visible scope', () {
    const path = 'lib/rules.dart';
    const source = '''
class Rules {
  void scan() {
    final visited = <int>{};
    visited.add(1);
    if (true) {
      final items = <int>[];
      items.add(2);
    }
    items.add(3);
  }
  final items = <int>[];
}
''';
    final result = sliceCandidate(
      repoRoot: '/unused',
      candidateId: 'cand-collection-scope',
      entrySymbolPath: '$path#Rules.scan',
      snapshotId: 'snapshot-collection-scope',
      contentOverlay: {
        path: source,
        'pubspec.yaml': 'name: collection_scope\n',
      },
    );
    final steps = (result['steps'] as List).cast<Map<String, Object?>>();
    final edges = (result['edges'] as List).cast<Map<String, Object?>>();
    String code(Map step) {
      final range = (step['anchor'] as Map)['byteRange'] as List;
      return source.substring(range[0] as int, range[1] as int);
    }

    final visited = steps.firstWhere((step) => code(step) == 'visited.add(1);');
    final inner = steps.firstWhere((step) => code(step) == 'items.add(2);');
    final outer = steps.firstWhere((step) => code(step) == 'items.add(3);');
    expect(visited['kind'], 'mutation');
    expect(inner['kind'], 'mutation');
    expect(outer['kind'], 'mutation');
    expect(
      edges.any(
        (edge) =>
            edge['stepOrdinal'] == outer['ordinal'] &&
            (edge['toSymbolPath'] as String).contains('unresolved_dynamic.add'),
      ),
      isFalse,
    );
  });

  test(
    'a core Set.from result has source-backed local collection mutations',
    () {
      const path = 'lib/rules.dart';
      const source = '''
class Rules {
  static void run(Set<int> source) {
    final copied = Set<int>.from(source);
    copied.add(1);
  }
}
''';
      final result = sliceCandidate(
        repoRoot: '/unused',
        candidateId: 'cand-factory-receiver',
        entrySymbolPath: '$path#Rules.run',
        snapshotId: 'snapshot-factory-receiver',
        contentOverlay: {
          path: source,
          'pubspec.yaml': 'name: factory_receiver\n',
        },
      );
      final steps = (result['steps'] as List).cast<Map<String, Object?>>();
      final edges = (result['edges'] as List).cast<Map<String, Object?>>();
      final call = steps.firstWhere((step) {
        final range = (step['anchor'] as Map)['byteRange'] as List;
        return source.substring(range[0] as int, range[1] as int) ==
            'copied.add(1);';
      });
      expect(call['kind'], 'mutation');
      expect(
        edges.any(
          (edge) =>
              edge['stepOrdinal'] == call['ordinal'] &&
              edge['resolutionStatus'] == 'unresolved_dynamic',
        ),
        isFalse,
      );
    },
  );

  test('a core List.from result has a source-backed local mutation', () {
    const path = 'lib/rules.dart';
    const source = '''
class Rules {
  static void run(List<int> source) {
    final queue = List<int>.from(source);
    queue.add(1);
  }
}
''';
    final result = sliceCandidate(
      repoRoot: '/unused',
      candidateId: 'cand-list-factory',
      entrySymbolPath: '$path#Rules.run',
      snapshotId: 'snapshot-list-factory',
      contentOverlay: {path: source, 'pubspec.yaml': 'name: list_factory\n'},
    );
    final steps = (result['steps'] as List).cast<Map<String, Object?>>();
    final add = steps.firstWhere((step) {
      final range = (step['anchor'] as Map)['byteRange'] as List;
      return source.substring(range[0] as int, range[1] as int) ==
          'queue.add(1);';
    });
    expect(add['kind'], 'mutation');
  });

  test('an imported Set class is not treated as the core collection', () {
    const path = 'lib/rules.dart';
    const customPath = 'lib/custom_set.dart';
    const source = '''
import 'custom_set.dart';
class Rules {
  static void run(Iterable<int> source) {
    final copied = Set<int>.from(source);
    copied.add(1);
  }
}
''';
    const custom = '''
class Set<T> {
  Set.from(Iterable<T> source);
  void add(T value) {}
}
''';
    final result = sliceCandidate(
      repoRoot: '/unused',
      candidateId: 'cand-custom-set',
      entrySymbolPath: '$path#Rules.run',
      snapshotId: 'snapshot-custom-set',
      contentOverlay: {
        path: source,
        customPath: custom,
        'pubspec.yaml': 'name: custom_set\n',
      },
    );
    final steps = (result['steps'] as List).cast<Map<String, Object?>>();
    final add = steps.firstWhere((step) {
      final anchor = step['anchor'] as Map<String, Object?>;
      if (anchor['repoRelativePath'] != path) return false;
      final range = anchor['byteRange'] as List;
      return source.substring(range[0] as int, range[1] as int) ==
          'copied.add(1);';
    });
    expect(add['kind'], 'call');
  });

  test('a final collection field mutation is identified in its class', () {
    const path = 'lib/controller.dart';
    const source = '''
class Controller {
  final List<int> _history = [];
  void run() {
    _history.add(1);
  }
}
''';
    final result = sliceCandidate(
      repoRoot: '/unused',
      candidateId: 'cand-collection-field',
      entrySymbolPath: '$path#Controller.run',
      snapshotId: 'snapshot-collection-field',
      contentOverlay: {
        path: source,
        'pubspec.yaml': 'name: collection_field\n',
      },
    );
    final steps = (result['steps'] as List).cast<Map<String, Object?>>();
    final add = steps.firstWhere((step) {
      final range = (step['anchor'] as Map)['byteRange'] as List;
      return source.substring(range[0] as int, range[1] as int) ==
          '_history.add(1);';
    });
    expect(add['kind'], 'mutation');
  });

  test('a parameter shadowing a collection field stays unresolved', () {
    const path = 'lib/controller.dart';
    const source = '''
class Controller {
  final List<int> _history = [];
  void run(dynamic _history) {
    _history.add(1);
  }
}
''';
    final result = sliceCandidate(
      repoRoot: '/unused',
      candidateId: 'cand-shadowed-collection-field',
      entrySymbolPath: '$path#Controller.run',
      snapshotId: 'snapshot-shadowed-collection-field',
      contentOverlay: {
        path: source,
        'pubspec.yaml': 'name: shadowed_collection_field\n',
      },
    );
    final steps = (result['steps'] as List).cast<Map<String, Object?>>();
    final add = steps.firstWhere((step) {
      final range = (step['anchor'] as Map)['byteRange'] as List;
      return source.substring(range[0] as int, range[1] as int) ==
          '_history.add(1);';
    });
    expect(add['kind'], 'call');
  });

  test(
    'removeWhere is a mutation while callback execution remains unknown',
    () {
      const path = 'lib/rules.dart';
      const source = '''
class Rules {
  void run() {
    final values = <int>[1, 2];
    values.removeWhere((value) => value == 1);
  }
}
''';
      final result = sliceCandidate(
        repoRoot: '/unused',
        candidateId: 'cand-remove-where',
        entrySymbolPath: '$path#Rules.run',
        snapshotId: 'snapshot-remove-where',
        contentOverlay: {path: source, 'pubspec.yaml': 'name: remove_where\n'},
      );
      final steps = (result['steps'] as List).cast<Map<String, Object?>>();
      final edges = (result['edges'] as List).cast<Map<String, Object?>>();
      final mutation = steps.firstWhere((step) {
        final range = (step['anchor'] as Map)['byteRange'] as List;
        return source.substring(range[0] as int, range[1] as int) ==
            'values.removeWhere((value) => value == 1);';
      });
      expect(mutation['kind'], 'mutation');
      expect(
        edges.any(
          (edge) =>
              edge['kind'] == 'unknown_edge' &&
              (edge['toSymbolPath'] as String).contains('callback_'),
        ),
        isTrue,
      );
    },
  );

  test('declarations use the complete AST statement range', () {
    const path = 'lib/rules.dart';
    const source = '''
class Rules {
  static int calculate(int seed) {
    final marker = 'semi;colon';
    final adjusted = seed + marker.length;
    return adjusted;
  }
}
''';
    final result = sliceCandidate(
      repoRoot: '/unused',
      candidateId: 'cand-declaration-range',
      entrySymbolPath: '$path#Rules.calculate',
      snapshotId: 'snapshot-declaration-range',
      contentOverlay: {
        path: source,
        'pubspec.yaml': 'name: declaration_range\n',
      },
    );
    final steps = (result['steps'] as List).cast<Map<String, Object?>>();
    final marker = steps.firstWhere((step) {
      final range = (step['anchor'] as Map)['byteRange'] as List;
      return source.substring(range[0] as int, range[1] as int) ==
          "final marker = 'semi;colon';";
    });
    expect(marker['kind'], 'mutation');
    expect(marker['flowContext'], isNotNull);
  });

  test('an assigned copyWith call keeps its enclosing declaration', () {
    const path = 'lib/rules.dart';
    const source = '''
class GameState {
  GameState copyWith({String? status}) => this;
}
class Rules {
  static GameState update(GameState state) {
    final next = state.copyWith(status: 'ready;go');
    return next;
  }
}
''';
    final result = sliceCandidate(
      repoRoot: '/unused',
      candidateId: 'cand-assigned-copy',
      entrySymbolPath: '$path#Rules.update',
      snapshotId: 'snapshot-assigned-copy',
      contentOverlay: {path: source, 'pubspec.yaml': 'name: assigned_copy\n'},
    );
    final steps = (result['steps'] as List).cast<Map<String, Object?>>();
    final assignment = steps.firstWhere((step) {
      final range = (step['anchor'] as Map)['byteRange'] as List;
      return source.substring(range[0] as int, range[1] as int) ==
          "final next = state.copyWith(status: 'ready;go');";
    });
    expect(assignment['kind'], 'mutation');
    expect(assignment['flowContext'], isNotNull);
  });

  test('a callback return never terminates its enclosing method', () {
    const path = 'lib/rules.dart';
    const source = '''
class Rules {
  static void run(List<int> items) {
    items.forEach((item) {
      if (item == 0) return;
    });
    final finished = true;
  }
}
''';
    final result = sliceCandidate(
      repoRoot: '/unused',
      candidateId: 'cand-callback-return',
      entrySymbolPath: '$path#Rules.run',
      snapshotId: 'snapshot-callback-return',
      contentOverlay: {path: source, 'pubspec.yaml': 'name: callback_return\n'},
    );
    final steps = (result['steps'] as List).cast<Map<String, Object?>>();
    final edges = (result['edges'] as List).cast<Map<String, Object?>>();
    String code(Map step) {
      final range = (step['anchor'] as Map)['byteRange'] as List;
      return source.substring(range[0] as int, range[1] as int);
    }

    final callbackReturn = steps.firstWhere(
      (step) => step['kind'] == 'return' && code(step).contains('return'),
    );
    final call = steps.firstWhere(
      (step) => code(step).startsWith('items.forEach('),
    );
    final finished = steps.firstWhere(
      (step) => code(step) == 'final finished = true;',
    );
    expect(callbackReturn['invocationId'], isNot(call['invocationId']));
    expect(callbackReturn['invocationId'], isNot(finished['invocationId']));
    expect(
      edges.any(
        (edge) =>
            edge['stepOrdinal'] == callbackReturn['ordinal'] &&
            edge['targetStepOrdinal'] == finished['ordinal'],
      ),
      isFalse,
    );
    expect(
      edges.any(
        (edge) =>
            edge['stepOrdinal'] == call['ordinal'] &&
            edge['targetStepOrdinal'] == finished['ordinal'] &&
            edge['kind'] == 'control_flow',
      ),
      isTrue,
    );
    expect(
      edges.any(
        (edge) =>
            edge['stepOrdinal'] == call['ordinal'] &&
            edge['resolutionStatus'] == 'unresolved_dynamic',
      ),
      isTrue,
    );
  });

  test('unsupported callback control does not reject the outer method', () {
    const path = 'lib/rules.dart';
    const source = '''
class Rules {
  static void run(List<int> items) {
    items.forEach((item) {
      do { item++; } while (item < 2);
    });
    final finished = true;
  }
}
''';
    final result = sliceCandidate(
      repoRoot: '/unused',
      candidateId: 'cand-nested-control',
      entrySymbolPath: '$path#Rules.run',
      snapshotId: 'snapshot-nested-control',
      contentOverlay: {path: source, 'pubspec.yaml': 'name: nested_control\n'},
    );
    final steps = (result['steps'] as List).cast<Map<String, Object?>>();
    expect(
      steps.any((step) {
        final range = (step['anchor'] as Map)['byteRange'] as List;
        return source.substring(range[0] as int, range[1] as int) ==
            'final finished = true;';
      }),
      isTrue,
    );
  });

  test('timer registration keeps callback execution uncertain', () {
    const path = 'lib/rules.dart';
    const source = '''
class Rules {
  void run() {
    final timer = Timer(const Duration(milliseconds: 10), () {
      if (done) return;
    });
    final finished = true;
  }
}
''';
    final result = sliceCandidate(
      repoRoot: '/unused',
      candidateId: 'cand-timer-callback',
      entrySymbolPath: '$path#Rules.run',
      snapshotId: 'snapshot-timer-callback',
      opts: const {'includeCallbackRelations': true},
      contentOverlay: {path: source, 'pubspec.yaml': 'name: timer_callback\n'},
    );
    final steps = (result['steps'] as List).cast<Map<String, Object?>>();
    final edges = (result['edges'] as List).cast<Map<String, Object?>>();
    String code(Map step) {
      final range = (step['anchor'] as Map)['byteRange'] as List;
      return source.substring(range[0] as int, range[1] as int);
    }

    final registration = steps.firstWhere(
      (step) => code(step).startsWith('final timer = Timer('),
    );
    final callbackGuard = steps.firstWhere(
      (step) => step['kind'] == 'guard' && code(step).contains('if (done)'),
    );
    expect(callbackGuard['invocationId'], isNot(registration['invocationId']));
    expect(
      edges.any(
        (edge) =>
            edge['stepOrdinal'] == registration['ordinal'] &&
            edge['targetStepOrdinal'] == callbackGuard['ordinal'] &&
            edge['kind'] == 'callback_argument' &&
            edge['resolutionStatus'] == 'resolved',
      ),
      isTrue,
    );
    expect(
      edges.any(
        (edge) =>
            edge['stepOrdinal'] == registration['ordinal'] &&
            edge['kind'] == 'unknown_edge' &&
            (edge['unresolvedReason'] as String?)?.contains('콜백') == true,
      ),
      isTrue,
    );
  });

  test('callback relation is opt-in for older adapter consumers', () {
    const path = 'lib/rules.dart';
    const source = '''
class Rules {
  void run(List<int> items) {
    items.forEach((item) { item++; });
  }
}
''';
    final result = sliceCandidate(
      repoRoot: '/unused',
      candidateId: 'cand-callback-compatibility',
      entrySymbolPath: '$path#Rules.run',
      snapshotId: 'snapshot-callback-compatibility',
      contentOverlay: {
        path: source,
        'pubspec.yaml': 'name: callback_compatibility\n',
      },
    );
    final edges = (result['edges'] as List).cast<Map<String, Object?>>();
    expect(edges.any((edge) => edge['kind'] == 'callback_argument'), isFalse);
  });

  test('an arrow callback keeps its expression as exact source', () {
    const path = 'lib/rules.dart';
    const source = '''
class Rules {
  void run(List<int> items) {
    final selected = items.where((item) => item > 2).firstOrNull;
  }
}
''';
    final result = sliceCandidate(
      repoRoot: '/unused',
      candidateId: 'cand-arrow-callback',
      entrySymbolPath: '$path#Rules.run',
      snapshotId: 'snapshot-arrow-callback',
      opts: const {'includeCallbackRelations': true},
      contentOverlay: {path: source, 'pubspec.yaml': 'name: arrow_callback\n'},
    );
    final steps = (result['steps'] as List).cast<Map<String, Object?>>();
    final edges = (result['edges'] as List).cast<Map<String, Object?>>();
    String code(Map step) {
      final range = (step['anchor'] as Map)['byteRange'] as List;
      return source.substring(range[0] as int, range[1] as int);
    }

    final sourceStep = steps.firstWhere(
      (step) => code(step).startsWith('final selected ='),
    );
    final callbackStep = steps.firstWhere((step) => code(step) == 'item > 2');
    expect(callbackStep['flowContext'], isNotNull);
    expect(callbackStep['invocationId'], isNot(sourceStep['invocationId']));
    expect(
      edges.any(
        (edge) =>
            edge['kind'] == 'callback_argument' &&
            edge['stepOrdinal'] == sourceStep['ordinal'] &&
            edge['targetStepOrdinal'] == callbackStep['ordinal'],
      ),
      isTrue,
    );
    expect(
      edges.any(
        (edge) =>
            edge['kind'] == 'resolved_cross_file' &&
            edge['targetStepOrdinal'] == callbackStep['ordinal'],
      ),
      isFalse,
    );
  });

  test('depth limit does not publish a confirmed call without a target', () {
    final files = <String, String>{'pubspec.yaml': 'name: depth_limit\n'};
    for (var index = 0; index < 6; index++) {
      files['lib/node_$index.dart'] = index == 5
          ? '''
class Node$index {
  void run() {
    final complete = true;
  }
}
'''
          : '''
import 'node_${index + 1}.dart';
class Node$index {
  final Node${index + 1} next = Node${index + 1}();
  void run() {
    next.run();
  }
}
''';
    }
    final result = sliceCandidate(
      repoRoot: '/unused',
      candidateId: 'cand-depth-limit',
      entrySymbolPath: 'lib/node_0.dart#Node0.run',
      snapshotId: 'snapshot-depth-limit',
      contentOverlay: files,
    );
    final edges = (result['edges'] as List).cast<Map<String, Object?>>();
    expect(result['truncated'], isTrue);
    expect(
      edges.any(
        (edge) =>
            edge['kind'] == 'unknown_edge' &&
            edge['resolutionStatus'] == 'truncated' &&
            edge['depth'] == 5 &&
            edge['toSymbolPath'] == 'lib/node_5.dart#Node5.run',
      ),
      isTrue,
    );
    expect(
      edges.any(
        (edge) =>
            edge['kind'] == 'resolved_cross_file' &&
            edge['targetStepOrdinal'] == null,
      ),
      isFalse,
    );
  });

  test('a collection call evaluates its direct argument before mutation', () {
    const path = 'lib/rules.dart';
    const source = '''
class Rules {
  static List<int> getValidMoves(int seed) {
    final moves = <int>[];
    moves.addAll(getValidMovesFrom(seed));
    return moves;
  }
  static List<int> getValidMovesFrom(int seed) {
    return [seed];
  }
}
''';
    final result = sliceCandidate(
      repoRoot: '/unused',
      candidateId: 'cand-collection-argument',
      entrySymbolPath: '$path#Rules.getValidMoves',
      snapshotId: 'snapshot-collection-argument',
      contentOverlay: {path: source, 'pubspec.yaml': 'name: moves\n'},
    );
    final steps = (result['steps'] as List).cast<Map<String, Object?>>();
    final edges = (result['edges'] as List).cast<Map<String, Object?>>();
    String code(Map step) {
      final range = (step['anchor'] as Map)['byteRange'] as List;
      return source.substring(range[0] as int, range[1] as int);
    }

    final argument = steps.firstWhere(
      (step) => code(step) == 'getValidMovesFrom(seed)',
    );
    final collection = steps.firstWhere(
      (step) => code(step) == 'moves.addAll(getValidMovesFrom(seed));',
    );
    expect(argument['kind'], 'call');
    expect(collection['kind'], 'mutation');
    expect(argument['ordinal'] as int, lessThan(collection['ordinal'] as int));
    expect(argument['flowContext'], isNotNull);
    expect(collection['flowContext'], isNotNull);
    expect(
      edges.any(
        (edge) =>
            edge['stepOrdinal'] == argument['ordinal'] &&
            edge['toSymbolPath'] == '$path#Rules.getValidMovesFrom' &&
            edge['kind'] == 'resolved_cross_file',
      ),
      isTrue,
    );
    expect(
      edges.any(
        (edge) => (edge['toSymbolPath'] as String).contains(
          'unresolved_dynamic.addAll',
        ),
      ),
      isFalse,
    );
  });

  test('inner break resumes the outer updater when no body step follows', () {
    const path = 'lib/rules.dart';
    const source = '''
class GameRules {
  void scan() {
    for (var row = 0; row < 2; row++) {
      for (var column = 0; column < 2; column++) {
        if (column == 1) break;
        visit(row, column);
      }
    }
    finish();
  }
  void visit(int row, int column) {}
  void finish() {}
}
''';
    final result = sliceCandidate(
      repoRoot: '/unused',
      candidateId: 'cand-inner-break',
      entrySymbolPath: '$path#GameRules.scan',
      snapshotId: 'snapshot-inner-break',
      contentOverlay: {path: source, 'pubspec.yaml': 'name: inner_break\n'},
    );
    final steps = (result['steps'] as List).cast<Map<String, Object?>>();
    final edges = (result['edges'] as List).cast<Map<String, Object?>>();
    String code(Map step) {
      final range = (step['anchor'] as Map)['byteRange'] as List;
      return source.substring(range[0] as int, range[1] as int);
    }

    final stop = steps.firstWhere((step) => code(step) == 'break;');
    final rowUpdate = steps.firstWhere((step) => code(step) == 'row++');
    final finish = steps.firstWhere((step) => code(step) == 'finish();');
    expect(
      edges.any(
        (edge) =>
            edge['stepOrdinal'] == stop['ordinal'] &&
            edge['targetStepOrdinal'] == rowUpdate['ordinal'] &&
            edge['kind'] == 'loop_exit_back',
      ),
      isTrue,
    );
    expect(
      edges.any(
        (edge) =>
            edge['stepOrdinal'] == stop['ordinal'] &&
            edge['targetStepOrdinal'] == finish['ordinal'],
      ),
      isFalse,
    );
  });

  test('empty loop bodies retain their condition and exit paths', () {
    const path = 'lib/rules.dart';
    const source = '''
class GameRules {
  void scan(bool again) {
    for (var index = 0; index < 2; index++) {}
    while (again) {}
    finish();
  }
  void finish() {}
}
''';
    final result = sliceCandidate(
      repoRoot: '/unused',
      candidateId: 'cand-empty-loops',
      entrySymbolPath: '$path#GameRules.scan',
      snapshotId: 'snapshot-empty-loops',
      contentOverlay: {path: source, 'pubspec.yaml': 'name: empty_loops\n'},
    );
    final steps = (result['steps'] as List).cast<Map<String, Object?>>();
    final edges = (result['edges'] as List).cast<Map<String, Object?>>();
    String code(Map step) {
      final range = (step['anchor'] as Map)['byteRange'] as List;
      return source.substring(range[0] as int, range[1] as int);
    }

    final forCondition = steps.firstWhere((step) => code(step) == 'index < 2');
    final update = steps.firstWhere((step) => code(step) == 'index++');
    final whileCondition = steps.firstWhere((step) => code(step) == 'again');
    final finish = steps.firstWhere((step) => code(step) == 'finish();');
    bool hasEdge(Map from, Map to, String kind, [String? outcome]) => edges.any(
      (edge) =>
          edge['stepOrdinal'] == from['ordinal'] &&
          edge['targetStepOrdinal'] == to['ordinal'] &&
          edge['kind'] == kind &&
          (outcome == null ||
              (edge['conditions'] as List).any(
                (item) => (item as Map)['outcome'] == outcome,
              )),
    );
    expect(hasEdge(forCondition, update, 'control_flow', 'truthy'), isTrue);
    expect(
      hasEdge(forCondition, whileCondition, 'control_flow', 'falsy'),
      isTrue,
    );
    expect(hasEdge(update, forCondition, 'loop_back'), isTrue);
    expect(
      hasEdge(whileCondition, whileCondition, 'loop_back', 'truthy'),
      isTrue,
    );
    expect(hasEdge(whileCondition, finish, 'control_flow', 'falsy'), isTrue);
  });

  test('unsupported loop shape cannot publish an assumed execution path', () {
    const path = 'lib/rules.dart';
    const source = '''
class GameRules {
  void scan() {
    for (var row = 0, column = 0; row < 2; row++, column++) {
      visit(row, column);
    }
    finish();
  }
  void visit(int row, int column) {}
  void finish() {}
}
''';
    expect(
      () => sliceCandidate(
        repoRoot: '/unused',
        candidateId: 'cand-unsupported-loop',
        entrySymbolPath: '$path#GameRules.scan',
        snapshotId: 'snapshot-unsupported-loop',
        contentOverlay: {
          path: source,
          'pubspec.yaml': 'name: unsupported_loop\n',
        },
      ),
      throwsA(
        isA<ArgumentError>().having(
          (error) => error.message,
          'message',
          contains('entry_symbol_unsupported_control_flow:'),
        ),
      ),
    );
  });

  test('switch break inside a loop does not exit the loop', () {
    const path = 'lib/rules.dart';
    const source = '''
class GameRules {
  void scan() {
    for (var index = 0; index < 2; index++) {
      switch (index) {
        case 0: break;
        default: visit();
      }
      finishIteration();
    }
    finish();
  }
  void visit() {}
  void finishIteration() {}
  void finish() {}
}
''';
    final result = sliceCandidate(
      repoRoot: '/unused',
      candidateId: 'cand-switch-loop',
      entrySymbolPath: '$path#GameRules.scan',
      snapshotId: 'snapshot-switch-loop',
      contentOverlay: {path: source, 'pubspec.yaml': 'name: switch_loop\n'},
    );
    final steps = (result['steps'] as List).cast<Map<String, Object?>>();
    final edges = (result['edges'] as List).cast<Map<String, Object?>>();
    String code(Map step) {
      final range = (step['anchor'] as Map)['byteRange'] as List;
      return source.substring(range[0] as int, range[1] as int);
    }

    final switchBreak = steps.firstWhere((step) => code(step) == 'break;');
    expect(
      edges
          .where((edge) => edge['stepOrdinal'] == switchBreak['ordinal'])
          .any(
            (edge) =>
                edge['kind'] == 'loop_exit' || edge['kind'] == 'loop_exit_back',
          ),
      isFalse,
    );
  });

  test('while break and continue preserve their distinct exits', () {
    const path = 'lib/rules.dart';
    const source = '''
class GameRules {
  void scan() {
    var index = 0;
    while (index < 3) {
      index++;
      if (index == 1) continue;
      if (index == 2) break;
      visit();
    }
    finish();
  }
  void visit() {}
  void finish() {}
}
''';
    final result = sliceCandidate(
      repoRoot: '/unused',
      candidateId: 'cand-while-exits',
      entrySymbolPath: '$path#GameRules.scan',
      snapshotId: 'snapshot-while-exits',
      contentOverlay: {path: source, 'pubspec.yaml': 'name: while_exits\n'},
    );
    final steps = (result['steps'] as List).cast<Map<String, Object?>>();
    final edges = (result['edges'] as List).cast<Map<String, Object?>>();
    String code(Map step) {
      final range = (step['anchor'] as Map)['byteRange'] as List;
      return source.substring(range[0] as int, range[1] as int);
    }

    final condition = steps.firstWhere((step) => code(step) == 'index < 3');
    final skip = steps.firstWhere((step) => code(step) == 'continue;');
    final stop = steps.firstWhere((step) => code(step) == 'break;');
    final visit = steps.firstWhere((step) => code(step) == 'visit();');
    final finish = steps.firstWhere((step) => code(step) == 'finish();');
    bool hasEdge(Map from, Map to, String kind, [String? outcome]) => edges.any(
      (edge) =>
          edge['stepOrdinal'] == from['ordinal'] &&
          edge['targetStepOrdinal'] == to['ordinal'] &&
          edge['kind'] == kind &&
          (outcome == null ||
              (edge['conditions'] as List).any(
                (item) => (item as Map)['outcome'] == outcome,
              )),
    );
    expect(hasEdge(condition, finish, 'control_flow', 'falsy'), isTrue);
    expect(hasEdge(skip, condition, 'loop_back'), isTrue);
    expect(hasEdge(stop, finish, 'loop_exit'), isTrue);
    expect(hasEdge(visit, condition, 'loop_back'), isTrue);
    expect(hasEdge(stop, visit, 'control_flow'), isFalse);
    expect(steps.every((step) => step['flowContext'] != null), isTrue);
  });

  test('for-in repeats through its source-backed header', () {
    const path = 'lib/rules.dart';
    const source = '''
class GameRules {
  void scan(List<int> items) {
    for (final item in items) {
      if (item == 0) continue;
      visit(item);
    }
    finish();
  }
  void visit(int item) {}
  void finish() {}
}
''';
    final result = sliceCandidate(
      repoRoot: '/unused',
      candidateId: 'cand-for-in',
      entrySymbolPath: '$path#GameRules.scan',
      snapshotId: 'snapshot-for-in',
      contentOverlay: {path: source, 'pubspec.yaml': 'name: for_in\n'},
    );
    final steps = (result['steps'] as List).cast<Map<String, Object?>>();
    final edges = (result['edges'] as List).cast<Map<String, Object?>>();
    String code(Map step) {
      final range = (step['anchor'] as Map)['byteRange'] as List;
      return source.substring(range[0] as int, range[1] as int);
    }

    final header = steps.firstWhere(
      (step) => code(step) == 'for (final item in items)',
    );
    final skip = steps.firstWhere((step) => code(step) == 'continue;');
    final visit = steps.firstWhere((step) => code(step) == 'visit(item);');
    final finish = steps.firstWhere((step) => code(step) == 'finish();');
    bool hasEdge(Map from, Map to, String kind, [String? outcome]) => edges.any(
      (edge) =>
          edge['stepOrdinal'] == from['ordinal'] &&
          edge['targetStepOrdinal'] == to['ordinal'] &&
          edge['kind'] == kind &&
          (outcome == null ||
              (edge['conditions'] as List).any(
                (item) => (item as Map)['outcome'] == outcome,
              )),
    );
    expect(hasEdge(header, finish, 'control_flow', 'falsy'), isTrue);
    expect(hasEdge(skip, header, 'loop_back'), isTrue);
    expect(hasEdge(visit, header, 'loop_back'), isTrue);
    expect(steps.every((step) => step['flowContext'] != null), isTrue);
  });

  test('nested loops finish through each enclosing loop', () {
    const path = 'lib/rules.dart';
    const source = '''
class GameRules {
  void scan() {
    for (var row = 0; row < 2; row++) {
      for (var column = 0; column < 2; column++) {
        visit(row, column);
      }
    }
    finish();
  }
  void visit(int row, int column) {}
  void finish() {}
}
''';
    final result = sliceCandidate(
      repoRoot: '/unused',
      candidateId: 'cand-nested-loops',
      entrySymbolPath: '$path#GameRules.scan',
      snapshotId: 'snapshot-nested-loops',
      contentOverlay: {path: source, 'pubspec.yaml': 'name: nested_loops\n'},
    );
    final steps = (result['steps'] as List).cast<Map<String, Object?>>();
    final edges = (result['edges'] as List).cast<Map<String, Object?>>();
    String code(Map step) {
      final range = (step['anchor'] as Map)['byteRange'] as List;
      return source.substring(range[0] as int, range[1] as int);
    }

    final outerUpdate = steps.firstWhere((step) => code(step) == 'row++');
    final innerCondition = steps.firstWhere(
      (step) => code(step) == 'column < 2',
    );
    final innerUpdate = steps.firstWhere((step) => code(step) == 'column++');
    final body = steps.firstWhere(
      (step) => code(step) == 'visit(row, column);',
    );
    final finish = steps.firstWhere((step) => code(step) == 'finish();');
    bool hasEdge(Map from, Map to, String kind, [String? outcome]) => edges.any(
      (edge) =>
          edge['stepOrdinal'] == from['ordinal'] &&
          edge['targetStepOrdinal'] == to['ordinal'] &&
          edge['kind'] == kind &&
          (outcome == null ||
              (edge['conditions'] as List).any(
                (item) => (item as Map)['outcome'] == outcome,
              )),
    );
    expect(hasEdge(body, innerUpdate, 'loop_back'), isTrue);
    expect(hasEdge(body, outerUpdate, 'loop_back'), isFalse);
    expect(
      hasEdge(innerCondition, outerUpdate, 'control_flow', 'falsy'),
      isTrue,
    );
    expect(hasEdge(innerCondition, finish, 'control_flow', 'falsy'), isFalse);
  });

  test('a false guard inside nested loops advances the inner loop', () {
    const path = 'lib/rules.dart';
    const source = '''
class GameRules {
  int scan(List<List<bool>> board) {
    var total = 0;
    for (var row = 0; row < board.length; row++) {
      for (var column = 0; column < board[row].length; column++) {
        if (board[row][column]) {
          total++;
        }
      }
    }
    return total;
  }
}
''';
    final result = sliceCandidate(
      repoRoot: '/unused',
      candidateId: 'cand-nested-guard',
      entrySymbolPath: '$path#GameRules.scan',
      snapshotId: 'snapshot-nested-guard',
      contentOverlay: {path: source, 'pubspec.yaml': 'name: nested_guard\n'},
    );
    final steps = (result['steps'] as List).cast<Map<String, Object?>>();
    final edges = (result['edges'] as List).cast<Map<String, Object?>>();
    String code(Map step) {
      final range = (step['anchor'] as Map)['byteRange'] as List;
      return source.substring(range[0] as int, range[1] as int);
    }

    final guard = steps.firstWhere(
      (step) => code(step) == 'if (board[row][column])',
    );
    final innerUpdate = steps.firstWhere((step) => code(step) == 'column++');
    final outerUpdate = steps.firstWhere((step) => code(step) == 'row++');
    final resultStep = steps.firstWhere(
      (step) => code(step) == 'return total;',
    );
    bool hasEdge(Map from, Map to, String kind, String outcome) => edges.any(
      (edge) =>
          edge['stepOrdinal'] == from['ordinal'] &&
          edge['targetStepOrdinal'] == to['ordinal'] &&
          edge['kind'] == kind &&
          (edge['conditions'] as List?)?.any(
                (item) => (item as Map)['outcome'] == outcome,
              ) ==
              true,
    );
    expect(hasEdge(guard, innerUpdate, 'loop_back', 'falsy'), isTrue);
    expect(hasEdge(guard, outerUpdate, 'loop_back', 'falsy'), isFalse);
    expect(hasEdge(guard, resultStep, 'control_flow', 'falsy'), isFalse);
  });

  test('for loop and continue follow the condition and updater', () {
    const path = 'lib/rules.dart';
    const source = '''
class GameRules {
  void scan() {
    var total = 0;
    for (var index = -1; index < 3; index++) {
      if (index == 1) continue;
      total += index;
    }
    finish(total);
  }
  void finish(int value) { return; }
}
''';
    final result = sliceCandidate(
      repoRoot: '/unused',
      candidateId: 'cand-for-control',
      entrySymbolPath: '$path#GameRules.scan',
      snapshotId: 'snapshot-for-control',
      contentOverlay: {path: source, 'pubspec.yaml': 'name: for_control\n'},
    );
    final steps = (result['steps'] as List).cast<Map<String, Object?>>();
    final edges = (result['edges'] as List).cast<Map<String, Object?>>();
    String code(Map step) {
      final range = (step['anchor'] as Map)['byteRange'] as List;
      return source.substring(range[0] as int, range[1] as int);
    }

    final condition = steps.firstWhere((step) => code(step) == 'index < 3');
    final update = steps.firstWhere((step) => code(step) == 'index++');
    final skip = steps.firstWhere((step) => code(step) == 'continue;');
    final body = steps.firstWhere((step) => code(step) == 'total += index;');
    final finish = steps.firstWhere((step) => code(step) == 'finish(total);');
    bool hasEdge(Map from, Map to, String kind, [String? outcome]) => edges.any(
      (edge) =>
          edge['stepOrdinal'] == from['ordinal'] &&
          edge['targetStepOrdinal'] == to['ordinal'] &&
          edge['kind'] == kind &&
          (outcome == null ||
              (edge['conditions'] as List).any(
                (item) => (item as Map)['outcome'] == outcome,
              )),
    );
    expect(condition['kind'], 'branch');
    expect(skip['kind'], 'continue');
    expect(hasEdge(condition, finish, 'control_flow', 'falsy'), isTrue);
    expect(hasEdge(skip, update, 'loop_back'), isTrue);
    expect(hasEdge(body, update, 'loop_back'), isTrue);
    expect(hasEdge(update, condition, 'loop_back'), isTrue);
    expect(hasEdge(condition, update, 'control_flow'), isFalse);
    expect(steps.every((step) => step['flowContext'] != null), isTrue);
  });

  test('for loop variable declarations retain exact source evidence', () {
    const path = 'lib/rules.dart';
    const source = '''
class GameRules {
  void scan() {
    for (var dr = -2; dr <= 2; dr++) {
      final distance = dr;
    }
  }
}
''';
    final result = sliceCandidate(
      repoRoot: '/unused',
      candidateId: 'cand-for-initializer',
      entrySymbolPath: '$path#GameRules.scan',
      snapshotId: 'snapshot-for-initializer',
      contentOverlay: {path: source, 'pubspec.yaml': 'name: for_init\n'},
    );
    final steps = (result['steps'] as List).cast<Map<String, Object?>>();
    final initializer = steps.firstWhere(
      (step) => step['kind'] == 'mutation' && step['stateAfter'] == 'dr',
    );
    final range = (initializer['anchor'] as Map)['byteRange'] as List;
    expect(source.substring(range[0] as int, range[1] as int), 'var dr = -2;');
    expect(
      ((initializer['flowContext'] as Map)['statement'] as Map)['byteRange'],
      range,
    );
  });

  test('a direct call inside a condition has exact expression evidence', () {
    const path = 'lib/rules.dart';
    const source = '''
class GameRules {
  static bool allowed(int value) { return value > 0; }
  void apply(int value) {
    if (GameRules.allowed(value)) return;
  }
}
''';
    final result = sliceCandidate(
      repoRoot: '/unused',
      candidateId: 'cand-condition-call',
      entrySymbolPath: '$path#GameRules.apply',
      snapshotId: 'snapshot-condition-call',
      contentOverlay: {path: source, 'pubspec.yaml': 'name: condition_call\n'},
    );
    final steps = (result['steps'] as List).cast<Map<String, Object?>>();
    final call = steps.firstWhere((step) => step['kind'] == 'call');
    final anchor = call['anchor'] as Map;
    final range = anchor['byteRange'] as List;
    expect(
      source.substring(range[0] as int, range[1] as int),
      'GameRules.allowed(value)',
    );
    expect(
      ((call['flowContext'] as Map)['statement'] as Map)['byteRange'],
      range,
    );
    expect(
      ((call['flowContext'] as Map)['structuralContext'] as Map)['nodeKind'],
      'condition',
    );
  });

  test('a direct initializer call enters its imported implementation', () {
    const controllerPath = 'lib/controller.dart';
    const rulesPath = 'lib/rules.dart';
    const controller = '''
import 'rules.dart';
class GameController {
  int _state = 0;
  void onCellTapped(int pos) {
    final nextState = GameRules.applyMove(_state, pos);
    _state = nextState;
  }
}
''';
    const rules = '''
class GameRules {
  static int applyMove(int state, int pos) {
    final next = state + pos;
    final items = <int>[];
    items.removeWhere((item) => item == pos);
    return next;
  }
}
''';
    final result = sliceCandidate(
      repoRoot: '/unused',
      candidateId: 'cand-initializer-call',
      entrySymbolPath: '$controllerPath#GameController.onCellTapped',
      snapshotId: 'snapshot-initializer-call',
      contentOverlay: {
        controllerPath: controller,
        rulesPath: rules,
        'pubspec.yaml': 'name: move_flow\n',
      },
    );
    final steps = (result['steps'] as List).cast<Map<String, Object?>>();
    final edges = (result['edges'] as List).cast<Map<String, Object?>>();
    final call = steps.firstWhere(
      (step) =>
          step['kind'] == 'call' &&
          (step['anchor'] as Map)['repoRelativePath'] == controllerPath,
    );
    final callRange = (call['anchor'] as Map)['byteRange'] as List;
    expect(
      controller.substring(callRange[0] as int, callRange[1] as int),
      'GameRules.applyMove(_state, pos)',
    );
    expect(
      ((call['flowContext'] as Map)['statement'] as Map)['byteRange'],
      callRange,
    );
    expect(
      ((call['flowContext'] as Map)['statement'] as Map)['nodeKind'],
      'expression',
    );
    final direct = edges.singleWhere(
      (edge) =>
          edge['kind'] == 'resolved_cross_file' &&
          edge['stepOrdinal'] == call['ordinal'],
    );
    expect(direct['toSymbolPath'], '$rulesPath#GameRules.applyMove');
    final callee = steps[(direct['targetStepOrdinal'] as int) - 1];
    expect((callee['anchor'] as Map)['repoRelativePath'], rulesPath);
    expect(
      (callee['anchor'] as Map)['enclosingSymbolPath'],
      'GameRules.applyMove',
    );
    expect(
      edges
          .where((edge) => edge['toSymbolPath'] != null)
          .every(
            (edge) => RegExp(
              r'^[A-Za-z0-9_./$-]+#[A-Za-z0-9_.$-]+$',
            ).hasMatch(edge['toSymbolPath'] as String),
          ),
      isTrue,
    );
    expect(
      steps.any(
        (step) =>
            step['kind'] == 'mutation' && step['stateAfter'] == 'nextState',
      ),
      isTrue,
    );
    final assignment = steps.firstWhere(
      (step) => step['kind'] == 'mutation' && step['stateAfter'] == 'nextState',
    );
    expect(assignment['assignmentSourceOrdinal'], call['ordinal']);
  });

  test('a call inside an eager transformation executes before assignment', () {
    const path = 'lib/rules.dart';
    const source = '''
class Rules {
  static int calculate(int seed) => seed * 2;
  static int run(int seed) {
    final adjusted = calculate(seed) + 1;
    return adjusted;
  }
}
''';
    final result = sliceCandidate(
      repoRoot: '/unused',
      candidateId: 'cand-eager-transformation',
      entrySymbolPath: '$path#Rules.run',
      snapshotId: 'snapshot-eager-transformation',
      contentOverlay: {path: source, 'pubspec.yaml': 'name: eager_transform\n'},
    );
    final steps = (result['steps'] as List).cast<Map<String, Object?>>();
    final edges = (result['edges'] as List).cast<Map<String, Object?>>();
    String code(Map step) {
      final range = (step['anchor'] as Map)['byteRange'] as List;
      return source.substring(range[0] as int, range[1] as int);
    }

    final call = steps.firstWhere((step) => code(step) == 'calculate(seed)');
    final assignment = steps.firstWhere(
      (step) => code(step) == 'final adjusted = calculate(seed) + 1;',
    );
    expect(call['kind'], 'call');
    expect(call['ordinal'] as int, lessThan(assignment['ordinal'] as int));
    expect(call['flowContext'], isNotNull);
    expect(assignment['assignmentSourceOrdinal'], isNull);
    expect(
      edges.any(
        (edge) =>
            edge['stepOrdinal'] == call['ordinal'] &&
            edge['kind'] == 'resolved_cross_file' &&
            edge['toSymbolPath'] == '$path#Rules.calculate',
      ),
      isTrue,
    );
  });

  test('eager operands and a short-circuit call keep their distinct paths', () {
    const path = 'lib/rules.dart';
    const source = '''
class Rules {
  static int left() => 1;
  static int right() => 2;
  static bool check() => true;
  static int run(bool enabled) {
    final total = left() + right();
    final ready = enabled && check();
    return ready ? total : 0;
  }
}
''';
    final result = sliceCandidate(
      repoRoot: '/unused',
      candidateId: 'cand-eager-order',
      entrySymbolPath: '$path#Rules.run',
      snapshotId: 'snapshot-eager-order',
      contentOverlay: {path: source, 'pubspec.yaml': 'name: eager_order\n'},
    );
    final steps = (result['steps'] as List).cast<Map<String, Object?>>();
    final edges = (result['edges'] as List).cast<Map<String, Object?>>();
    String code(Map step) {
      final range = (step['anchor'] as Map)['byteRange'] as List;
      return source.substring(range[0] as int, range[1] as int);
    }

    final left = steps.firstWhere((step) => code(step) == 'left()');
    final right = steps.firstWhere((step) => code(step) == 'right()');
    final total = steps.firstWhere(
      (step) => code(step) == 'final total = left() + right();',
    );
    expect(left['ordinal'] as int, lessThan(right['ordinal'] as int));
    expect(right['ordinal'] as int, lessThan(total['ordinal'] as int));
    expect(total['assignmentSourceOrdinal'], isNull);
    final condition = steps.firstWhere((step) => code(step) == 'enabled');
    final check = steps.firstWhere((step) => code(step) == 'check()');
    final ready = steps.firstWhere(
      (step) => code(step) == 'final ready = enabled && check();',
    );
    bool pathTo(Map from, Map to, String? outcome) => edges.any(
      (edge) =>
          edge['kind'] == 'control_flow' &&
          edge['stepOrdinal'] == from['ordinal'] &&
          edge['targetStepOrdinal'] == to['ordinal'] &&
          (outcome == null ||
              (edge['conditions'] as List).any(
                (condition) => (condition as Map)['outcome'] == outcome,
              )),
    );
    expect(condition['kind'], 'guard');
    expect(condition['flowContext'], isNotNull);
    expect(check['flowContext'], isNotNull);
    expect(pathTo(condition, check, 'truthy'), isTrue);
    expect(pathTo(condition, ready, 'falsy'), isTrue);
    expect(pathTo(check, ready, null), isTrue);
    expect(
      edges.any(
        (edge) =>
            edge['stepOrdinal'] == check['ordinal'] &&
            edge['kind'] == 'resolved_cross_file' &&
            edge['toSymbolPath'] == '$path#Rules.check' &&
            edge['targetStepOrdinal'] != null,
      ),
      isTrue,
    );
    expect(
      steps.any(
        (step) =>
            code(step) == 'true' &&
            step['kind'] == 'return' &&
            (step['anchor'] as Map)['enclosingSymbolPath'] == 'Rules.check',
      ),
      isTrue,
    );
  });

  for (final entry in [
    (
      operator: '||',
      parameter: 'bool enabled',
      execute: 'falsy',
      skip: 'truthy',
    ),
    (
      operator: '??',
      parameter: 'bool? enabled',
      execute: 'nullish',
      skip: 'non_nullish',
    ),
  ]) {
    test('${entry.operator} calls only on its required outcome', () {
      const path = 'lib/rules.dart';
      final source =
          '''
class Rules {
  static bool check() => true;
  static bool run(${entry.parameter}) {
    final ready = enabled ${entry.operator} check();
    return ready;
  }
}
''';
      final result = sliceCandidate(
        repoRoot: '/unused',
        candidateId:
            'cand-short-circuit-${entry.operator == '||' ? 'or' : 'null'}',
        entrySymbolPath: '$path#Rules.run',
        snapshotId: 'snapshot-short-circuit-${entry.operator}',
        contentOverlay: {path: source, 'pubspec.yaml': 'name: short_circuit\n'},
      );
      final steps = (result['steps'] as List).cast<Map<String, Object?>>();
      final edges = (result['edges'] as List).cast<Map<String, Object?>>();
      String code(Map step) {
        final range = (step['anchor'] as Map)['byteRange'] as List;
        return source.substring(range[0] as int, range[1] as int);
      }

      final condition = steps.firstWhere((step) => code(step) == 'enabled');
      final check = steps.firstWhere((step) => code(step) == 'check()');
      final ready = steps.firstWhere(
        (step) =>
            code(step) == 'final ready = enabled ${entry.operator} check();',
      );
      expect(condition['flowContext'], isNotNull);
      expect(check['flowContext'], isNotNull);
      for (final pair in [
        (target: check, outcome: entry.execute),
        (target: ready, outcome: entry.skip),
      ]) {
        expect(
          edges.any(
            (edge) =>
                edge['kind'] == 'control_flow' &&
                edge['stepOrdinal'] == condition['ordinal'] &&
                edge['targetStepOrdinal'] == pair.target['ordinal'] &&
                (edge['conditions'] as List).any(
                  (item) => (item as Map)['outcome'] == pair.outcome,
                ),
          ),
          isTrue,
        );
      }
    });
  }

  test('overloaded arrow arithmetic is not certified as a direct return', () {
    const path = 'lib/rules.dart';
    const source = '''
class NumberBox {
  NumberBox operator +(int amount) => this;
}
class Rules {
  static NumberBox combine(NumberBox box) => box + 1;
  static void run(NumberBox box) { combine(box); }
}
''';
    final result = sliceCandidate(
      repoRoot: '/unused',
      candidateId: 'cand-overloaded-arrow',
      entrySymbolPath: '$path#Rules.run',
      snapshotId: 'snapshot-overloaded-arrow',
      contentOverlay: {
        path: source,
        'pubspec.yaml': 'name: overloaded_arrow\n',
      },
    );
    final steps = (result['steps'] as List).cast<Map<String, Object?>>();
    final edges = (result['edges'] as List).cast<Map<String, Object?>>();
    expect(
      steps.any(
        (step) =>
            (step['anchor'] as Map)['enclosingSymbolPath'] == 'Rules.combine' &&
            step['kind'] == 'return',
      ),
      isFalse,
    );
    expect(
      edges.any(
        (edge) =>
            edge['toSymbolPath'] == '$path#Rules.combine' &&
            edge['targetStepOrdinal'] == null,
      ),
      isTrue,
    );
  });

  test('early-return guard keeps the false path to the next statement', () {
    const path = 'lib/controller.dart';
    const source = '''
// 한글 설명은 소스 바이트 위치와 문자열 위치를 다르게 만든다.
class GameController {
  void onCellTapped(int pos) {
    if (pos < 0) return;
    final selected = _state.currentTurn;
    _move(selected);
  }
  void _move(int pos) { return; }
}
''';
    final result = sliceCandidate(
      repoRoot: '/unused',
      candidateId: 'cand-guard-fallthrough',
      entrySymbolPath: '$path#GameController.onCellTapped',
      contentOverlay: {path: source, 'pubspec.yaml': 'name: guard_path\n'},
    );
    final steps = (result['steps'] as List).cast<Map<String, Object?>>();
    final edges = (result['edges'] as List).cast<Map<String, Object?>>();
    final guard = steps.firstWhere((step) => step['kind'] == 'guard');
    final selected = steps.firstWhere(
      (step) => step['stateAfter'] == 'selected',
    );
    expect(
      edges.any(
        (edge) =>
            edge['kind'] == 'control_flow' &&
            edge['stepOrdinal'] == guard['ordinal'] &&
            edge['targetStepOrdinal'] == selected['ordinal'] &&
            (edge['conditions'] as List).any(
              (condition) => (condition as Map)['outcome'] == 'falsy',
            ),
      ),
      isTrue,
    );
  });

  test('if and else branches join after the alternative branch', () {
    const path = 'lib/controller.dart';
    const source = '''
// 한글 주석
class GameController {
  void onCellTapped(int pos) {
    if (pos == 1) {
      final thenValue = _state.currentTurn;
    } else {
      final elseValue = _state.ownerAt(pos);
    }
    notifyListeners();
  }
}
''';
    final result = sliceCandidate(
      repoRoot: '/unused',
      candidateId: 'cand-branch-join',
      entrySymbolPath: '$path#GameController.onCellTapped',
      contentOverlay: {path: source, 'pubspec.yaml': 'name: branch_join\n'},
    );
    final steps = (result['steps'] as List).cast<Map<String, Object?>>();
    final edges = (result['edges'] as List).cast<Map<String, Object?>>();
    final thenStep = steps.firstWhere(
      (step) => step['stateAfter'] == 'thenValue',
    );
    final elseStep = steps.firstWhere(
      (step) => step['stateAfter'] == 'elseValue',
    );
    final notify = steps.firstWhere(
      (step) => (step['description'] as String).contains('Notify listeners'),
    );
    expect(
      edges.any(
        (edge) =>
            edge['stepOrdinal'] == thenStep['ordinal'] &&
            edge['targetStepOrdinal'] == elseStep['ordinal'],
      ),
      isFalse,
    );
    expect(
      edges.any(
        (edge) =>
            edge['stepOrdinal'] == thenStep['ordinal'] &&
            edge['targetStepOrdinal'] == notify['ordinal'] &&
            edge['kind'] == 'control_flow',
      ),
      isTrue,
    );
  });

  test('else-if alternatives retain each outcome and skip other branches', () {
    const path = 'lib/controller.dart';
    const source = '''
// 한글 주석
class GameController {
  void onCellTapped(int pos) {
    if (pos == 1) {
      final firstValue = _state.first;
    } else if (pos == 2) {
      final secondValue = _state.second;
    } else {
      final defaultValue = _state.last;
    }
    notifyListeners();
  }
}
''';
    final result = sliceCandidate(
      repoRoot: '/unused',
      candidateId: 'cand-else-if-join',
      entrySymbolPath: '$path#GameController.onCellTapped',
      contentOverlay: {path: source, 'pubspec.yaml': 'name: else_if_join\n'},
    );
    final steps = (result['steps'] as List).cast<Map<String, Object?>>();
    final edges = (result['edges'] as List).cast<Map<String, Object?>>();
    final first = steps.firstWhere(
      (step) => step['stateAfter'] == 'firstValue',
    );
    final second = steps.firstWhere(
      (step) => step['stateAfter'] == 'secondValue',
    );
    final fallback = steps.firstWhere(
      (step) => step['stateAfter'] == 'defaultValue',
    );
    final guards = steps.where((step) => step['kind'] == 'guard').toList();
    final notify = steps.firstWhere(
      (step) => (step['description'] as String).contains('Notify listeners'),
    );
    bool hasEdge(
      Map<String, Object?> from,
      Map<String, Object?> to, [
      String? outcome,
    ]) => edges.any(
      (edge) =>
          edge['kind'] == 'control_flow' &&
          edge['stepOrdinal'] == from['ordinal'] &&
          edge['targetStepOrdinal'] == to['ordinal'] &&
          (outcome == null ||
              (edge['conditions'] as List).any(
                (condition) => (condition as Map)['outcome'] == outcome,
              )),
    );

    expect(guards, hasLength(2));
    expect(hasEdge(guards[0], first, 'truthy'), isTrue);
    expect(hasEdge(guards[0], guards[1], 'falsy'), isTrue);
    expect(hasEdge(guards[1], second, 'truthy'), isTrue);
    expect(hasEdge(guards[1], fallback, 'falsy'), isTrue);
    expect(hasEdge(first, second), isFalse);
    expect(hasEdge(first, notify), isTrue);
    expect(hasEdge(second, fallback), isFalse);
    expect(hasEdge(second, notify), isTrue);
  });

  test('a call with a parenthesis in a string remains an executable step', () {
    const path = 'lib/controller.dart';
    const source = '''
class GameController {
  void start(bool deploy) {
    if (deploy) {
      _showBanner('Explosive Spore) placed');
    }
  }
  void _showBanner(String text) { return; }
}
''';
    final result = sliceCandidate(
      repoRoot: '/unused',
      candidateId: 'cand-string-parenthesis',
      entrySymbolPath: '$path#GameController.start',
      contentOverlay: {path: source, 'pubspec.yaml': 'name: call_string\n'},
    );
    final steps = (result['steps'] as List).cast<Map<String, Object?>>();
    final edges = (result['edges'] as List).cast<Map<String, Object?>>();
    final call = steps.firstWhere(
      (step) =>
          step['kind'] == 'call' &&
          step['symbolPath'] == 'GameController.start',
    );
    final range = (call['anchor'] as Map)['byteRange'] as List;
    expect(
      source.substring(range[0] as int, range[1] as int),
      "_showBanner('Explosive Spore) placed');",
    );
    expect(
      edges.any(
        (edge) =>
            edge['kind'] == 'resolved_cross_file' &&
            edge['stepOrdinal'] == call['ordinal'] &&
            edge['targetStepOrdinal'] != null,
      ),
      isTrue,
    );
  });

  test('state assignment describes its destination rather than its value', () {
    const path = 'lib/controller.dart';
    const source = '''
class GameController {
  void applyMove() {
    final nextState = _state.next;
    _state = nextState;
  }
}
''';
    final result = sliceCandidate(
      repoRoot: '/unused',
      candidateId: 'cand-state-destination',
      entrySymbolPath: '$path#GameController.applyMove',
      contentOverlay: {path: source, 'pubspec.yaml': 'name: state_target\n'},
    );
    final steps = (result['steps'] as List).cast<Map<String, Object?>>();
    expect(steps, hasLength(2));
    expect(steps[0]['description'], contains('nextState'));
    expect(steps[1]['description'], '상태를 갱신한다');
    expect(steps[1]['stateAfter'], 'nextState');
  });

  test(
    'an initializer call keeps the complete declaration as code evidence',
    () {
      const path = 'lib/controller.dart';
      const source = '''
class GameController {
  void onCellTapped(int pos) {
    final tappedOwner = _state.ownerAt(pos);
    if (tappedOwner != null) return;
  }
}
''';
      final result = sliceCandidate(
        repoRoot: '/unused',
        candidateId: 'cand-initializer',
        entrySymbolPath: '$path#GameController.onCellTapped',
        snapshotId: 'snapshot-test',
        contentOverlay: {path: source, 'pubspec.yaml': 'name: direct_target\n'},
      );
      final steps = (result['steps'] as List).cast<Map<String, Object?>>();
      expect(steps, isNotEmpty);
      expect(steps.every((step) => step['flowContext'] != null), isTrue);
      expect(
        steps.any((step) {
          final range = (step['anchor'] as Map)['byteRange'] as List;
          return source
              .substring(range[0] as int, range[1] as int)
              .contains('final tappedOwner = _state.ownerAt(pos);');
        }),
        isTrue,
      );
    },
  );

  test(
    'a callee beginning with another call keeps its own source identity',
    () {
      const path = 'lib/service.dart';
      const source = '''
class GameController {
  void start() {
    _schedule();
    return;
  }

  void _schedule() {
    _show();
    return;
  }

  void _show() {
    return;
  }
}
''';
      final result = sliceCandidate(
        repoRoot: '/unused',
        candidateId: 'cand-direct-target',
        entrySymbolPath: '$path#GameController.start',
        contentOverlay: {path: source, 'pubspec.yaml': 'name: direct_target\n'},
      );
      final steps = (result['steps'] as List).cast<Map<String, Object?>>();
      final edges = (result['edges'] as List).cast<Map<String, Object?>>();
      final scheduleEdge = edges.singleWhere(
        (edge) =>
            edge['kind'] == 'resolved_cross_file' &&
            edge['toSymbolPath'] == '$path#GameController._schedule',
      );
      final targetOrdinal = scheduleEdge['targetStepOrdinal'] as int;
      final target = steps[targetOrdinal - 1];
      final anchor = target['anchor'] as Map<String, Object?>;
      expect(target['symbolPath'], 'GameController._schedule');
      expect(anchor['enclosingSymbolPath'], 'GameController._schedule');
      expect(target['callerStepOrdinal'], scheduleEdge['stepOrdinal']);
    },
  );
}
