import 'dart:convert';
import 'dart:io';

import 'package:test/test.dart';
import 'package:codeflow_dart_adapter/codeflow_dart_adapter.dart';

import 'helpers.dart';

void main() {
  final exampleRoot = exampleAppRoot().replaceAll('\\', '/');

  group('Ticket 07 - Same-file Slicing', () {
    test('slices EmailSignupNotifier.submit with guard, mutation and boundary call', () {
      final res = sliceCandidate(
        repoRoot: exampleRoot,
        candidateId: 'cand-7232d63b96bd6efa',
        entrySymbolPath:
            'lib/features/auth/email_signup_notifier.dart#EmailSignupNotifier.submit',
      );

      expect(res['candidateId'], 'cand-7232d63b96bd6efa');
      expect(res['language'], 'dart');
      expect(res['entrySymbolPath'],
          'lib/features/auth/email_signup_notifier.dart#EmailSignupNotifier.submit');

      final steps = (res['steps'] as List).cast<Map<String, Object?>>();
      expect(steps, isNotEmpty);

      // Check step kinds exist
      final kinds = steps.map((s) => s['kind']).toList();
      expect(kinds, contains('mutation'));
      expect(kinds, contains('call'));

      // Check anchor structure
      for (final s in steps) {
        final anchor = s['anchor'] as Map<String, Object?>;
        expect(anchor['repoRelativePath'], 'lib/features/auth/email_signup_notifier.dart');
        expect(anchor['byteRange'], isA<List>());
        expect((anchor['byteRange'] as List).length, 2);
        expect(anchor['fileHash'], matches(RegExp(r'^[a-f0-9]{64}$')));
        expect(anchor['spanHash'], matches(RegExp(r'^[a-f0-9]{64}$')));
        expect(anchor['canonicalAstFingerprint'], matches(RegExp(r'^[a-f0-9]{64}$')));
      }

      // Check edges
      final edges = (res['edges'] as List).cast<Map<String, Object?>>();
      expect(edges, isNotEmpty);
      expect(edges.any((e) => e['kind'] == 'boundary_call' || e['kind'] == 'resolved_cross_file' || e['kind'] == 'unknown_edge'), isTrue);
    });

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
        final libDir = Directory('${tempDir.path}/lib')..createSync(recursive: true);
        File('${tempDir.path}/pubspec.yaml').writeAsStringSync('name: secret_test\n');
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
    test('traces Controller -> UseCase -> Repository boundary across files', () {
      final tempDir = Directory.systemTemp.createTempSync('cross_file_test');
      try {
        final authDir = Directory('${tempDir.path}/lib/auth')..createSync(recursive: true);
        File('${tempDir.path}/pubspec.yaml').writeAsStringSync('name: cross_test\n');

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
          entrySymbolPath: 'lib/auth/signup_controller.dart#SignupController.submit',
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
    });

    test('handles recursion/cycles gracefully without infinite loop', () {
      final tempDir = Directory.systemTemp.createTempSync('cycle_test');
      try {
        final libDir = Directory('${tempDir.path}/lib')..createSync(recursive: true);
        File('${tempDir.path}/pubspec.yaml').writeAsStringSync('name: cycle_test\n');

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
      } finally {
        tempDir.deleteSync(recursive: true);
      }
    });
  });
}
