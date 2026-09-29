import 'dart:convert';
import 'dart:io';

import 'package:test/test.dart';
import 'package:codeflow_dart_adapter/codeflow_dart_adapter.dart';

import 'helpers.dart';

void main() {
  final exampleRoot = exampleAppRoot().replaceAll('\\', '/');

  group('Ticket 07 - Same-file Slicing', () {
    test(
      'keeps counter updates and skips an outer else-if after nested branches',
      () {
        final tempDir = Directory.systemTemp.createTempSync(
          'nested_else_slice',
        );
        try {
          final libDir = Directory('${tempDir.path}/lib')
            ..createSync(recursive: true);
          File(
            '${tempDir.path}/pubspec.yaml',
          ).writeAsStringSync('name: nested_else_slice\n');
          const source = '''
class Counter {
  int count = 0;
  bool mode = false;
  void process(bool enable, bool ready) {
    // 한글 설명은 소스 바이트 범위에 영향을 준다.
    if (enable) {
      count++;
      if (ready) count--;
    } else if (mode) {
      count = 5;
    }
    count = 10;
  }
}
''';
          File('${libDir.path}/counter.dart').writeAsStringSync(source);
          final result = sliceCandidate(
            repoRoot: tempDir.path.replaceAll('\\', '/'),
            candidateId: 'cand-nested-else',
            entrySymbolPath: 'lib/counter.dart#Counter.process',
          );
          final steps = (result['steps'] as List).cast<Map<String, Object?>>();
          final edges = (result['edges'] as List).cast<Map<String, Object?>>();
          final sourceBytes = utf8.encode(source);
          String textOf(Map<String, Object?> step) {
            final range = ((step['anchor'] as Map)['byteRange'] as List)
                .cast<int>();
            return utf8.decode(sourceBytes.sublist(range[0], range[1])).trim();
          }

          int ordinal(String sourceText) =>
              steps.singleWhere((step) => textOf(step) == sourceText)['ordinal']
                  as int;
          final increment = ordinal('count++;');
          final decrement = ordinal('count--;');
          final innerGuard = ordinal('if (ready)');
          final alternative = ordinal('if (mode)');
          final after = ordinal('count = 10;');
          bool connects(int from, int to) => edges.any(
            (edge) =>
                edge['stepOrdinal'] == from &&
                edge['targetStepOrdinal'] == to &&
                edge['kind'] == 'control_flow',
          );
          expect(connects(increment, innerGuard), isTrue);
          expect(connects(innerGuard, decrement), isTrue);
          expect(connects(innerGuard, after), isTrue);
          expect(connects(decrement, after), isTrue);
          expect(connects(innerGuard, alternative), isFalse);
          expect(connects(decrement, alternative), isFalse);
        } finally {
          tempDir.deleteSync(recursive: true);
        }
      },
    );

    test(
      'marks local declarations without treating a field write as local',
      () {
        final tempDir = Directory.systemTemp.createTempSync(
          'local_value_slice',
        );
        try {
          final libDir = Directory('${tempDir.path}/lib')
            ..createSync(recursive: true);
          File(
            '${tempDir.path}/pubspec.yaml',
          ).writeAsStringSync('name: local_value_slice\n');
          final source = '''
class State {
  int currentTurn = 1;
  void applyMove() {
    final player = currentTurn;
    final opponent = player + 1;
    currentTurn = opponent;
  }
}
''';
          File('${libDir.path}/state.dart').writeAsStringSync(source);
          final params = (
            repoRoot: tempDir.path.replaceAll('\\', '/'),
            candidateId: 'cand-local-value',
            entrySymbolPath: 'lib/state.dart#State.applyMove',
          );
          final result = sliceCandidate(
            repoRoot: params.repoRoot,
            candidateId: params.candidateId,
            entrySymbolPath: params.entrySymbolPath,
            opts: {'includeValueRoles': true},
          );
          final steps = (result['steps'] as List).cast<Map<String, Object?>>();
          final values = steps
              .where((step) => step['kind'] == 'mutation')
              .toList();
          expect(values.map((step) => step['valueRole']).toList(), [
            'local_binding',
            'local_binding',
            null,
          ]);
          expect(
            values.map((step) {
              final range = ((step['anchor'] as Map)['byteRange'] as List)
                  .cast<int>();
              return source.substring(range[0], range[1]);
            }).toList(),
            [
              'final player = currentTurn;',
              'final opponent = player + 1;',
              'currentTurn = opponent;',
            ],
          );
          final legacy = sliceCandidate(
            repoRoot: params.repoRoot,
            candidateId: params.candidateId,
            entrySymbolPath: params.entrySymbolPath,
          );
          expect(
            (legacy['steps'] as List).every(
              (step) => !(step as Map).containsKey('valueRole'),
            ),
            isTrue,
          );
        } finally {
          tempDir.deleteSync(recursive: true);
        }
      },
    );

    test('connects each simple switch case to its own return', () {
      final tempDir = Directory.systemTemp.createTempSync(
        'switch_return_slice',
      );
      try {
        final libDir = Directory('${tempDir.path}/lib')
          ..createSync(recursive: true);
        File(
          '${tempDir.path}/pubspec.yaml',
        ).writeAsStringSync('name: switch_return_slice\n');
        final source = '''
enum Owner { first, second, none }
class State {
  int firstCharges = 1;
  int secondCharges = 2;
  int chargesOf(Owner owner) {
    switch (owner) {
      case Owner.first:
        return firstCharges;
      case Owner.second:
        return secondCharges;
      case Owner.none:
        return 0;
    }
  }
}
''';
        File('${libDir.path}/state.dart').writeAsStringSync(source);
        final result = sliceCandidate(
          repoRoot: tempDir.path.replaceAll('\\', '/'),
          candidateId: 'cand-switch-return',
          entrySymbolPath: 'lib/state.dart#State.chargesOf',
        );
        final steps = (result['steps'] as List).cast<Map<String, Object?>>();
        final edges = (result['edges'] as List).cast<Map<String, Object?>>();
        final cases = steps.where((step) {
          final range = ((step['anchor'] as Map)['byteRange'] as List)
              .cast<int>();
          return step['kind'] == 'guard' &&
              source
                  .substring(range[0], range[1])
                  .trimLeft()
                  .startsWith('case ');
        }).toList();
        final returns = steps
            .where((step) => step['kind'] == 'return')
            .toList();
        expect(cases, hasLength(3));
        expect(returns, hasLength(3));
        for (var index = 0; index < cases.length; index++) {
          final from = cases[index]['ordinal'];
          final to = returns[index]['ordinal'];
          expect(
            edges.any(
              (edge) =>
                  edge['stepOrdinal'] == from &&
                  edge['targetStepOrdinal'] == to &&
                  edge['kind'] == 'control_flow' &&
                  (edge['conditions'] as List).single['outcome'] == 'truthy',
            ),
            isTrue,
          );
          if (index + 1 < cases.length) {
            expect(
              edges.any(
                (edge) =>
                    edge['stepOrdinal'] == from &&
                    edge['targetStepOrdinal'] == cases[index + 1]['ordinal'] &&
                    edge['kind'] == 'control_flow' &&
                    (edge['conditions'] as List).single['outcome'] == 'falsy',
              ),
              isTrue,
            );
          }
          expect(
            edges.any(
              (edge) =>
                  edge['stepOrdinal'] == to && edge['kind'] == 'control_flow',
            ),
            isFalse,
          );
        }
      } finally {
        tempDir.deleteSync(recursive: true);
      }
    });

    test(
      'slices a primary-constructor controller method without using the file range',
      () {
        final tempDir = Directory.systemTemp.createTempSync(
          'primary_constructor_slice',
        );
        try {
          final libDir = Directory('${tempDir.path}/lib')
            ..createSync(recursive: true);
          File(
            '${tempDir.path}/pubspec.yaml',
          ).writeAsStringSync('name: primary_constructor_slice\n');
          File('${libDir.path}/battle_controller.dart').writeAsStringSync('''
class BattleRepository {
  Future<void> endTurn(String turn) async {}
}

class BattleController({required this.repository}) {
  final BattleRepository repository;

  Future<void> _onEndTurn(String turn) async {
    if (turn.isEmpty) throw ArgumentError('turn');
    final nextTurn = turn.trim();
    await repository.endTurn(nextTurn);
  }
}
''');

          final result = sliceCandidate(
            repoRoot: tempDir.path.replaceAll('\\', '/'),
            candidateId: 'cand-primary000000',
            entrySymbolPath:
                'lib/battle_controller.dart#BattleController._onEndTurn',
          );
          final steps = (result['steps'] as List).cast<Map<String, Object?>>();
          expect(steps.length, greaterThanOrEqualTo(2));
          final sourceLength = utf8
              .encode(
                File(
                  '${libDir.path}/battle_controller.dart',
                ).readAsStringSync(),
              )
              .length;
          for (final step in steps) {
            final anchor = step['anchor'] as Map<String, Object?>;
            final range = (anchor['byteRange'] as List).cast<int>();
            expect(range, isNot(equals([0, sourceLength])));
            expect(
              anchor['enclosingSymbolPath'],
              'BattleController._onEndTurn',
            );
          }
        } finally {
          tempDir.deleteSync(recursive: true);
        }
      },
    );

    test('rejects an entry symbol that cannot be located', () {
      final tempDir = Directory.systemTemp.createTempSync(
        'missing_entry_slice',
      );
      try {
        final libDir = Directory('${tempDir.path}/lib')
          ..createSync(recursive: true);
        File(
          '${tempDir.path}/pubspec.yaml',
        ).writeAsStringSync('name: missing_entry_slice\n');
        File('${libDir.path}/service.dart').writeAsStringSync('''
class Service {
  void run() {}
}
''');
        expect(
          () => sliceCandidate(
            repoRoot: tempDir.path.replaceAll('\\', '/'),
            candidateId: 'cand-missing000000',
            entrySymbolPath: 'lib/service.dart#Service.missing',
          ),
          throwsA(
            isA<ArgumentError>().having(
              (error) => error.message,
              'message',
              contains('entry_symbol_not_found'),
            ),
          ),
        );
      } finally {
        tempDir.deleteSync(recursive: true);
      }
    });

    test(
      'preserves local execution relations and separate call invocations',
      () {
        final tempDir = Directory.systemTemp.createTempSync(
          'execution_relation_slice',
        );
        try {
          final libDir = Directory('${tempDir.path}/lib')
            ..createSync(recursive: true);
          File(
            '${tempDir.path}/pubspec.yaml',
          ).writeAsStringSync('name: execution_relation_slice\n');
          File('${libDir.path}/controller.dart').writeAsStringSync('''
class Worker {
  int state = 0;
  void execute() {
    state = state + 1;
  }
}

class Controller {
  Controller({required this.worker});
  final Worker worker;

  void run() {
    worker.execute();
    worker.execute();
  }
}
''');
          final result = sliceCandidate(
            repoRoot: tempDir.path.replaceAll('\\', '/'),
            candidateId: 'cand-execution0000',
            entrySymbolPath: 'lib/controller.dart#Controller.run',
          );
          final steps = (result['steps'] as List).cast<Map<String, Object?>>();
          final byOrdinal = <int, Map<String, Object?>>{
            for (final step in steps) step['ordinal'] as int: step,
          };
          expect(steps.every((step) => step['invocationId'] is String), isTrue);

          final callEdges = (result['edges'] as List)
              .cast<Map<String, Object?>>()
              .where((edge) => edge['kind'] == 'resolved_cross_file')
              .toList();
          expect(callEdges, hasLength(2));
          final targetInvocations = <String>{};
          for (final edge in callEdges) {
            final source = byOrdinal[edge['stepOrdinal'] as int]!;
            final target = byOrdinal[edge['targetStepOrdinal'] as int]!;
            expect(target['callerStepOrdinal'], source['ordinal']);
            expect(target['invocationId'], isNot(source['invocationId']));
            targetInvocations.add(target['invocationId'] as String);
          }
          expect(targetInvocations, hasLength(2));

          final localSequence = (result['edges'] as List)
              .cast<Map<String, Object?>>()
              .where(
                (edge) =>
                    edge['kind'] == 'control_flow' &&
                    edge['toSymbolPath'] ==
                        'lib/controller.dart#Controller.run',
              )
              .toList();
          expect(localSequence, hasLength(1));
        } finally {
          tempDir.deleteSync(recursive: true);
        }
      },
    );

    test(
      'slices EmailSignupNotifier.submit with guard, mutation and boundary call',
      () {
        final res = sliceCandidate(
          repoRoot: exampleRoot,
          candidateId: 'cand-7232d63b96bd6efa',
          entrySymbolPath:
              'lib/features/auth/email_signup_notifier.dart#EmailSignupNotifier.submit',
        );

        expect(res['candidateId'], 'cand-7232d63b96bd6efa');
        expect(res['language'], 'dart');
        expect(
          res['entrySymbolPath'],
          'lib/features/auth/email_signup_notifier.dart#EmailSignupNotifier.submit',
        );

        final steps = (res['steps'] as List).cast<Map<String, Object?>>();
        expect(steps, isNotEmpty);

        // Check step kinds exist
        final kinds = steps.map((s) => s['kind']).toList();
        expect(kinds, contains('mutation'));
        expect(kinds, contains('call'));

        // Check anchor structure
        for (final s in steps) {
          final anchor = s['anchor'] as Map<String, Object?>;
          expect(
            anchor['repoRelativePath'],
            'lib/features/auth/email_signup_notifier.dart',
          );
          expect(anchor['byteRange'], isA<List>());
          expect((anchor['byteRange'] as List).length, 2);
          expect(anchor['fileHash'], matches(RegExp(r'^[a-f0-9]{64}$')));
          expect(anchor['spanHash'], matches(RegExp(r'^[a-f0-9]{64}$')));
          expect(
            anchor['canonicalAstFingerprint'],
            matches(RegExp(r'^[a-f0-9]{64}$')),
          );
          // Symbol-scoped view range must exist and contain the statement span.
          expect(anchor['symbolRange'], isA<List>());
          final symbolRange = (anchor['symbolRange'] as List).cast<int>();
          expect(symbolRange.length, 2);
          final byteRange = (anchor['byteRange'] as List).cast<int>();
          expect(symbolRange[0], lessThanOrEqualTo(byteRange[0]));
          expect(symbolRange[1], greaterThanOrEqualTo(byteRange[1]));
        }

        // Check edges
        final edges = (res['edges'] as List).cast<Map<String, Object?>>();
        expect(edges, isNotEmpty);
        for (final e in edges) {
          // Every edge names the step that produced it so FlowView can attach
          // the delegation target to the right timeline card.
          expect(e['stepOrdinal'], isA<int>());
          final ord = e['stepOrdinal'] as int;
          expect(ord, greaterThanOrEqualTo(1));
          expect(ord, lessThanOrEqualTo(steps.length));
        }
        expect(
          edges.any(
            (e) =>
                e['kind'] == 'boundary_call' ||
                e['kind'] == 'resolved_cross_file' ||
                e['kind'] == 'unknown_edge',
          ),
          isTrue,
        );
      },
    );

    test('deterministic slicing: identical inputs produce byte-identical JSON', () {
      final res1 = sliceCandidate(
        repoRoot: exampleRoot,
        candidateId: 'cand-7232d63b96bd6efa',
        entrySymbolPath:
            'lib/features/auth/email_signup_notifier.dart#EmailSignupNotifier.submit',
      );
      final res2 = sliceCandidate(
        repoRoot: exampleRoot,
        candidateId: 'cand-7232d63b96bd6efa',
        entrySymbolPath:
            'lib/features/auth/email_signup_notifier.dart#EmailSignupNotifier.submit',
      );

      final json1 = jsonEncode(res1);
      final json2 = jsonEncode(res2);
      expect(json1, equals(json2));
    });

    test('redacts secret patterns in strings', () {
      final tempDir = Directory.systemTemp.createTempSync('slice_secret_test');
      try {
        final libDir = Directory('${tempDir.path}/lib')
          ..createSync(recursive: true);
        File(
          '${tempDir.path}/pubspec.yaml',
        ).writeAsStringSync('name: secret_test\n');
        File('${libDir.path}/secret_service.dart').writeAsStringSync('''
class SecretService {
  void login() {
    final apiKey = "api_key: 'secret12345'";
    final token = "token = 'xyz-999'";
  }
}
''');

        final res = sliceCandidate(
          repoRoot: tempDir.path.replaceAll('\\', '/'),
          candidateId: 'cand-secret00000000',
          entrySymbolPath: 'lib/secret_service.dart#SecretService.login',
        );

        final jsonString = jsonEncode(res);
        expect(jsonString.contains('secret12345'), isFalse);
        expect(jsonString.contains('xyz-999'), isFalse);
        expect(res['redactedCount'] as int, greaterThan(0));
      } finally {
        tempDir.deleteSync(recursive: true);
      }
    });
  });

  group('Ticket 08 - Cross-file Symbol Tracking', () {
    test(
      'traces Controller -> UseCase -> Repository boundary across files',
      () {
        final tempDir = Directory.systemTemp.createTempSync('cross_file_test');
        try {
          final authDir = Directory('${tempDir.path}/lib/auth')
            ..createSync(recursive: true);
          File(
            '${tempDir.path}/pubspec.yaml',
          ).writeAsStringSync('name: cross_test\n');

          File('${authDir.path}/auth_repository.dart').writeAsStringSync('''
class AuthRepository {
  Future<void> saveUser(String email) async {}
}
''');

          File('${authDir.path}/signup_usecase.dart').writeAsStringSync('''
import 'auth_repository.dart';

class SignupUseCase {
  const SignupUseCase(this._repo);
  final AuthRepository _repo;

  Future<void> execute(String email) async {
    if (email.isEmpty) throw ArgumentError('email empty');
    await _repo.saveUser(email);
  }
}
''');

          File('${authDir.path}/signup_controller.dart').writeAsStringSync('''
import 'signup_usecase.dart';

class SignupController {
  const SignupController(this._useCase);
  final SignupUseCase _useCase;

  Future<void> submit(String email) async {
    state = "loading";
    await _useCase.execute(email);
    state = "success";
  }
  set state(String s) {}
}
''');

          final res = sliceCandidate(
            repoRoot: tempDir.path.replaceAll('\\', '/'),
            candidateId: 'cand-crossfile00000',
            entrySymbolPath:
                'lib/auth/signup_controller.dart#SignupController.submit',
          );

          expect(res['language'], 'dart');
          final steps = (res['steps'] as List).cast<Map<String, Object?>>();
          expect(steps.length, greaterThanOrEqualTo(3));

          final edges = (res['edges'] as List).cast<Map<String, Object?>>();
          // Should contain resolved_cross_file and boundary_call
          expect(edges.any((e) => e['kind'] == 'resolved_cross_file'), isTrue);
          expect(edges.any((e) => e['kind'] == 'boundary_call'), isTrue);

          // Guard in usecase should be extracted
          expect(steps.any((s) => s['kind'] == 'guard'), isTrue);
        } finally {
          tempDir.deleteSync(recursive: true);
        }
      },
    );

    test('handles recursion/cycles gracefully without infinite loop', () {
      final tempDir = Directory.systemTemp.createTempSync('cycle_test');
      try {
        final libDir = Directory('${tempDir.path}/lib')
          ..createSync(recursive: true);
        File(
          '${tempDir.path}/pubspec.yaml',
        ).writeAsStringSync('name: cycle_test\n');

        File('${libDir.path}/a.dart').writeAsStringSync('''
import 'b.dart';
class ClassA {
  final ClassB b = ClassB();
  void ping() {
    b.pong();
  }
}
''');

        File('${libDir.path}/b.dart').writeAsStringSync('''
import 'a.dart';
class ClassB {
  final ClassA a = ClassA();
  void pong() {
    a.ping();
  }
}
''');

        final res = sliceCandidate(
          repoRoot: tempDir.path.replaceAll('\\', '/'),
          candidateId: 'cand-cycle000000000',
          entrySymbolPath: 'lib/a.dart#ClassA.ping',
        );

        expect(res['visitedCycleDetected'], isTrue);
        final edges = (res['edges'] as List).cast<Map<String, Object?>>();
        expect(
          edges.any(
            (edge) =>
                edge['kind'] == 'unknown_edge' &&
                edge['resolutionStatus'] == 'truncated' &&
                edge['toSymbolPath'] == 'lib/a.dart#ClassA.ping' &&
                (edge['unresolvedReason'] as String).contains('재귀'),
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
      } finally {
        tempDir.deleteSync(recursive: true);
      }
    });

    test(
      'traces ref.read(provider) through UseCase to Repository (depth 3)',
      () {
        final tempDir = Directory.systemTemp.createTempSync(
          'provider_depth3_test',
        );
        try {
          final libDir = Directory('${tempDir.path}/lib')
            ..createSync(recursive: true);
          File(
            '${tempDir.path}/pubspec.yaml',
          ).writeAsStringSync('name: provider_depth3_test\n');

          File('${libDir.path}/api_client.dart').writeAsStringSync('''
class ApiClient {
  Future<void> post(String url, Map<String, dynamic> body) async {}
}
''');

          File('${libDir.path}/auth_repository.dart').writeAsStringSync('''
import 'api_client.dart';

class AuthRepository {
  AuthRepository(this._client);
  final ApiClient _client;

  Future<void> register(String email) async {
    await _client.post('/auth/register', {'email': email});
  }
}
''');

          File('${libDir.path}/signup_usecase.dart').writeAsStringSync('''
import 'auth_repository.dart';

class SignUpUseCase {
  SignUpUseCase(this._repo);
  final AuthRepository _repo;

  Future<void> call(String email) async {
    if (email.isEmpty) throw ArgumentError('Email is required');
    await _repo.register(email);
  }
}
''');

          File('${libDir.path}/signup_controller.dart').writeAsStringSync('''
import 'signup_usecase.dart';

final signUpUseCaseProvider = Provider<SignUpUseCase>((ref) => throw UnimplementedError());

class SignUpController {
  SignUpController(this.ref);
  final dynamic ref;

  Future<void> submit(String email) async {
    state = "submitting";
    await ref.read(signUpUseCaseProvider)(email);
    state = "success";
  }

  set state(String val) {}
}
''');

          final res = sliceCandidate(
            repoRoot: tempDir.path.replaceAll('\\', '/'),
            candidateId: 'cand-providerdepth3',
            entrySymbolPath:
                'lib/signup_controller.dart#SignUpController.submit',
          );

          final steps = (res['steps'] as List).cast<Map<String, Object?>>();
          expect(steps.length, greaterThanOrEqualTo(3));

          final paths = steps
              .map(
                (s) =>
                    (s['anchor'] as Map<String, Object?>)['repoRelativePath'],
              )
              .toSet();
          expect(paths, contains('lib/signup_controller.dart'));
          expect(paths, contains('lib/signup_usecase.dart'));
          expect(paths, contains('lib/auth_repository.dart'));

          final edges = (res['edges'] as List).cast<Map<String, Object?>>();
          expect(edges.any((e) => e['kind'] == 'resolved_cross_file'), isTrue);
          expect(edges.any((e) => e['kind'] == 'boundary_call'), isTrue);
        } finally {
          tempDir.deleteSync(recursive: true);
        }
      },
    );

    test('uses a schema-safe edge for unresolved provider calls', () {
      final tempDir = Directory.systemTemp.createTempSync('provider_call_test');
      try {
        final libDir = Directory('${tempDir.path}/lib')
          ..createSync(recursive: true);
        File(
          '${tempDir.path}/pubspec.yaml',
        ).writeAsStringSync('name: provider_call_test\n');
        File('${libDir.path}/controller.dart').writeAsStringSync('''
class Controller {
  Future<void> submit() async {
    await ref.read(externalProvider)();
  }
}
''');

        final res = sliceCandidate(
          repoRoot: tempDir.path.replaceAll('\\', '/'),
          candidateId: 'cand-provider000000',
          entrySymbolPath: 'lib/controller.dart#Controller.submit',
        );

        final edges = (res['edges'] as List).cast<Map<String, Object?>>();
        final unknownEdge = edges.singleWhere(
          (edge) => edge['kind'] == 'unknown_edge',
        );
        expect(
          unknownEdge['toSymbolPath'],
          'lib/controller.dart#unresolved_dynamic.call',
        );
      } finally {
        tempDir.deleteSync(recursive: true);
      }
    });

    test('does not claim control flow continues after an await', () {
      final tempDir = Directory.systemTemp.createTempSync('await_slice_test');
      try {
        final libDir = Directory('${tempDir.path}/lib')
          ..createSync(recursive: true);
        File(
          '${tempDir.path}/pubspec.yaml',
        ).writeAsStringSync('name: await_slice_test\n');
        File('${libDir.path}/controller.dart').writeAsStringSync('''
class Worker {
  Future<void> run() async {}
}

class Controller {
  Controller(this.worker);
  final Worker worker;
  int state = 0;

  Future<void> submit() async {
    await worker.run();
    state = 1;
  }
}
''');

        final result = sliceCandidate(
          repoRoot: tempDir.path.replaceAll('\\', '/'),
          candidateId: 'cand-await00000000',
          entrySymbolPath: 'lib/controller.dart#Controller.submit',
        );
        final steps = (result['steps'] as List).cast<Map<String, Object?>>();
        final awaitStep = steps.singleWhere((step) => step['kind'] == 'await');
        final mutationStep = steps.singleWhere(
          (step) => step['kind'] == 'mutation',
        );
        final edges = (result['edges'] as List).cast<Map<String, Object?>>();
        expect(
          edges.where(
            (edge) =>
                edge['kind'] == 'control_flow' &&
                edge['stepOrdinal'] == awaitStep['ordinal'] &&
                edge['targetStepOrdinal'] == mutationStep['ordinal'],
          ),
          isEmpty,
        );
      } finally {
        tempDir.deleteSync(recursive: true);
      }
    });

    test(
      'VS-01 & VS-02: extracts intermediate declarations, transforms, returns, and proven conditions',
      () {
        final tempDir = Directory.systemTemp.createTempSync('vs01_vs02_test');
        try {
          final libDir = Directory('${tempDir.path}/lib')
            ..createSync(recursive: true);
          File(
            '${tempDir.path}/pubspec.yaml',
          ).writeAsStringSync('name: vs01_vs02_test\n');
          const source = '''
class BattleRepository {
  Future<void> endTurn(String turn) async {}
}

class BattleController({required this.repository}) {
  final BattleRepository repository;

  Future<void> _onEndTurn(String turn) async {
    if (turn.isEmpty) throw ArgumentError('turn');
    final nextTurn = turn.trim();
    await repository.endTurn(nextTurn);
    return;
  }
}
''';
          File(
            '${libDir.path}/battle_controller.dart',
          ).writeAsStringSync(source);

          final result = sliceCandidate(
            repoRoot: tempDir.path.replaceAll('\\', '/'),
            candidateId: 'cand-vs01-vs02-001',
            entrySymbolPath:
                'lib/battle_controller.dart#BattleController._onEndTurn',
          );
          final steps = (result['steps'] as List).cast<Map<String, Object?>>();
          expect(steps, hasLength(5));

          // Step 1: if (turn.isEmpty)
          expect(steps[0]['kind'], 'guard');
          expect(steps[0]['guardCondition'], 'turn.isEmpty');

          // Step 2: throw ArgumentError('turn');
          expect(steps[1]['kind'], 'guard');
          expect(steps[1]['description'], contains('예외 발생'));

          // Step 3: final nextTurn = turn.trim();
          expect(steps[2]['kind'], 'mutation');
          expect(steps[2]['description'], contains('nextTurn'));
          final step3Range =
              ((steps[2]['anchor'] as Map<String, Object?>)['byteRange']
                      as List)
                  .cast<int>();
          final step3Source = utf8.decode(
            utf8.encode(source).sublist(step3Range[0], step3Range[1]),
          );
          expect(step3Source, contains('final nextTurn = turn.trim();'));

          // An initializer call stays within the declaration's code evidence.
          // Step 4: await repository.endTurn(nextTurn);
          expect(steps[3]['kind'], 'await');

          // Step 5: return;
          expect(steps[4]['kind'], 'return');
          expect(steps[4]['description'], '실행을 종료하고 반환한다');

          // Check Edges
          final edges = (result['edges'] as List).cast<Map<String, Object?>>();

          // Condition truthy from guard to throw
          final truthyEdge = edges.firstWhere(
            (e) =>
                e['kind'] == 'control_flow' &&
                e['stepOrdinal'] == 1 &&
                e['targetStepOrdinal'] == 2,
          );
          expect(truthyEdge['conditions'], isNotNull);
          final truthyConds = (truthyEdge['conditions'] as List)
              .cast<Map<String, Object?>>();
          expect(truthyConds[0]['outcome'], 'truthy');

          // Condition falsy from guard to nextTurn (skipping throw on normal flow)
          final falsyEdge = edges.firstWhere(
            (e) =>
                e['kind'] == 'control_flow' &&
                e['stepOrdinal'] == 1 &&
                e['targetStepOrdinal'] == 3,
          );
          expect(falsyEdge['conditions'], isNotNull);
          final falsyConds = (falsyEdge['conditions'] as List)
              .cast<Map<String, Object?>>();
          expect(falsyConds[0]['outcome'], 'falsy');

          // Normal sequential edge from nextTurn (mutation) to await
          final seqEdge = edges.firstWhere(
            (e) =>
                e['kind'] == 'control_flow' &&
                e['stepOrdinal'] == 3 &&
                e['targetStepOrdinal'] == 4,
          );
          expect(seqEdge['conditions'], isNull);

          // No control_flow edge leaving throw (step 2)
          expect(
            edges.where(
              (e) => e['kind'] == 'control_flow' && e['stepOrdinal'] == 2,
            ),
            isEmpty,
          );

          // No control_flow edge leaving await / return
          expect(
            edges.where(
              (e) =>
                  e['kind'] == 'control_flow' &&
                  (e['stepOrdinal'] == 4 || e['stepOrdinal'] == 5),
            ),
            isEmpty,
          );
        } finally {
          tempDir.deleteSync(recursive: true);
        }
      },
    );

    test(
      'VS-01 & VS-02: multi-statement if-block and mutation connecting to guard',
      () {
        final tempDir = Directory.systemTemp.createTempSync('dart_branch_test');
        try {
          final libDir = Directory('${tempDir.path}/lib')
            ..createSync(recursive: true);
          File(
            '${tempDir.path}/pubspec.yaml',
          ).writeAsStringSync('name: dart_branch_test\n');
          const source = '''
class OrderProcessor {
  int processOrder(int amount) {
    var fee = amount * 10;
    if (amount <= 0) {
      final msg = 'invalid';
      throw ArgumentError(msg);
    }
    var total = amount + fee;
    return total;
  }
}
''';
          File('${libDir.path}/order_processor.dart').writeAsStringSync(source);

          final result = sliceCandidate(
            repoRoot: tempDir.path.replaceAll('\\', '/'),
            candidateId: 'cand-branch-001',
            entrySymbolPath:
                'lib/order_processor.dart#OrderProcessor.processOrder',
          );
          final steps = (result['steps'] as List).cast<Map<String, Object?>>();
          final edges = (result['edges'] as List).cast<Map<String, Object?>>();

          // fee (step 1) MUST connect to guard (step 2)
          final feeToGuard = edges.firstWhere(
            (e) =>
                e['kind'] == 'control_flow' &&
                e['stepOrdinal'] == 1 &&
                e['targetStepOrdinal'] == 2,
          );
          expect(feeToGuard['conditions'], isNull);

          // guard (step 2) truthy connects into if body
          final truthyEdge = edges.firstWhere(
            (e) =>
                e['kind'] == 'control_flow' &&
                e['stepOrdinal'] == 2 &&
                (e['conditions'] as List).any((c) => c['outcome'] == 'truthy'),
          );
          expect(truthyEdge, isNotNull);

          // guard (step 2) falsy connects to statement AFTER if body (var total = amount + fee)
          // It MUST NOT connect to throw statement!
          final falsyEdge = edges.firstWhere(
            (e) =>
                e['kind'] == 'control_flow' &&
                e['stepOrdinal'] == 2 &&
                (e['conditions'] as List).any((c) => c['outcome'] == 'falsy'),
          );
          final falsyTargetOrdinal = falsyEdge['targetStepOrdinal'] as int;
          final falsyTargetStep = steps.firstWhere(
            (s) => s['ordinal'] == falsyTargetOrdinal,
          );
          expect(falsyTargetStep['description'], contains('total'));
        } finally {
          tempDir.deleteSync(recursive: true);
        }
      },
    );
  });
}
