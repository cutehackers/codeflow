import 'dart:convert';
import 'package:test/test.dart';
import 'package:codeflow_dart_adapter/codeflow_dart_adapter.dart';

void main() {
  group('VS-11 Dart Flow Context Metadata Extraction', () {
    test(
        'extracts exact statement and enclosing callback for nested callback fixture',
        () {
      final code = '''
class SettingsPage {
  Widget build(BuildContext context) {
    return Scaffold(
      body: ListView(
        children: [
          ListTile(
            title: Text('Account'),
            onTap: () {
              Navigator.pushNamed(context, '/account');
            },
          ),
        ],
      ),
    );
  }
}
''';
      final overlay = {
        'pubspec.yaml': 'name: test_app\n',
        'lib/settings_page.dart': code,
      };

      final res = sliceCandidate(
        repoRoot: '/fake/root',
        candidateId: 'cand-test1',
        entrySymbolPath: 'lib/settings_page.dart#SettingsPage.build',
        contentOverlay: overlay,
        snapshotId: 'snap-12345',
      );

      final steps = (res['steps'] as List).cast<Map<String, Object?>>();
      expect(steps, isNotEmpty);

      final navStep = steps.firstWhere(
        (s) =>
            s['description'].toString().toLowerCase().contains('push') ||
            s['symbolPath'].toString().toLowerCase().contains('push'),
      );
      final fc = navStep['flowContext'] as Map<String, Object?>?;
      expect(fc, isNotNull);

      // Statement
      final stmt = fc!['statement'] as Map<String, Object?>;
      expect(stmt['nodeKind'], 'statement');
      final stmtRange = (stmt['byteRange'] as List).cast<int>();
      expect(stmtRange[0], lessThan(stmtRange[1]));
      final stmtText =
          utf8.decode(utf8.encode(code).sublist(stmtRange[0], stmtRange[1]));
      expect(stmtText, contains('Navigator.pushNamed'));

      // Structural context: callback
      final struct = fc['structuralContext'] as Map<String, Object?>;
      expect(struct['status'], 'present');
      expect(struct['nodeKind'], 'callback');
      final structRange = (struct['byteRange'] as List).cast<int>();
      expect(structRange[0], lessThanOrEqualTo(stmtRange[0]));
      expect(stmtRange[1], lessThanOrEqualTo(structRange[1]));
      expect(structRange[0] < stmtRange[0] || stmtRange[1] < structRange[1],
          isTrue);
      final structText = utf8
          .decode(utf8.encode(code).sublist(structRange[0], structRange[1]));
      expect(structText, startsWith('onTap:'));

      // Callable
      final callable = fc['callable'] as Map<String, Object?>;
      final sigRange = (callable['signatureByteRange'] as List).cast<int>();
      final callRange = (callable['byteRange'] as List).cast<int>();
      expect(callRange[0], lessThanOrEqualTo(structRange[0]));
      expect(structRange[1], lessThanOrEqualTo(callRange[1]));
      expect(sigRange[0], equals(callRange[0]));
      expect(callable['signature'], contains('Widget build'));

      // Snapshot ID and Path
      expect(fc['snapshotId'], 'snap-12345');
      expect(fc['canonicalPath'], 'lib/settings_page.dart');
    });

    test('extracts enclosing condition when statement is in if-block', () {
      final code = '''
class AuthService {
  void checkStatus() {
    if (isAuthenticated) {
      sessionManager.refresh();
    }
  }
}
''';
      final overlay = {
        'pubspec.yaml': 'name: test_app\n',
        'lib/auth_service.dart': code,
      };

      final res = sliceCandidate(
        repoRoot: '/fake/root',
        candidateId: 'cand-test2',
        snapshotId: 'snap-test2',
        entrySymbolPath: 'lib/auth_service.dart#AuthService.checkStatus',
        contentOverlay: overlay,
      );

      final steps = (res['steps'] as List).cast<Map<String, Object?>>();
      final step = steps.firstWhere(
        (s) =>
            s['description'].toString().toLowerCase().contains('refresh') ||
            s['symbolPath'].toString().toLowerCase().contains('refresh'),
      );
      final fc = step['flowContext'] as Map<String, Object?>;
      final struct = fc['structuralContext'] as Map<String, Object?>;
      expect(struct['status'], 'present');
      expect(struct['nodeKind'], 'condition');
      final structRange = (struct['byteRange'] as List).cast<int>();
      final structText = utf8
          .decode(utf8.encode(code).sublist(structRange[0], structRange[1]));
      expect(structText, startsWith('if'));
    });

    test('extracts enclosing builder when statement is in builder callback',
        () {
      final code = '''
class ItemView {
  Widget render() {
    return ListView.builder(
      itemBuilder: (context, index) {
        tracker.trackItem(index);
      },
    );
  }
}
''';
      final overlay = {
        'pubspec.yaml': 'name: test_app\n',
        'lib/item_view.dart': code,
      };

      final res = sliceCandidate(
        repoRoot: '/fake/root',
        candidateId: 'cand-test3',
        snapshotId: 'snap-test3',
        entrySymbolPath: 'lib/item_view.dart#ItemView.render',
        contentOverlay: overlay,
      );

      final steps = (res['steps'] as List).cast<Map<String, Object?>>();
      final step = steps.firstWhere(
        (s) =>
            s['description'].toString().toLowerCase().contains('track') ||
            s['symbolPath'].toString().toLowerCase().contains('track'),
      );
      final fc = step['flowContext'] as Map<String, Object?>;
      final struct = fc['structuralContext'] as Map<String, Object?>;
      expect(struct['status'], 'present');
      expect(struct['nodeKind'], 'builder');
      final structRange = (struct['byteRange'] as List).cast<int>();
      final structText = utf8
          .decode(utf8.encode(code).sublist(structRange[0], structRange[1]));
      expect(structText, startsWith('itemBuilder:'));
    });

    test(
        'marks structural context as none when no enclosing condition/callback/builder exists',
        () {
      final code = '''
class SimpleService {
  void logout() {
    authClient.signOut();
  }
}
''';
      final overlay = {
        'pubspec.yaml': 'name: test_app\n',
        'lib/simple_service.dart': code,
      };

      final res = sliceCandidate(
        repoRoot: '/fake/root',
        candidateId: 'cand-test4',
        snapshotId: 'snap-test4',
        entrySymbolPath: 'lib/simple_service.dart#SimpleService.logout',
        contentOverlay: overlay,
      );

      final steps = (res['steps'] as List).cast<Map<String, Object?>>();
      final step = steps.firstWhere((s) =>
          s['description'].toString().contains('signOut') ||
          s['symbolPath'].toString().contains('signOut'));
      final fc = step['flowContext'] as Map<String, Object?>;
      final struct = fc['structuralContext'] as Map<String, Object?>;
      expect(struct['status'], 'none');
      expect(struct.containsKey('byteRange'), isFalse);
      expect(struct.containsKey('nodeKind'), isFalse);

      final callable = fc['callable'] as Map<String, Object?>;
      expect(callable['signature'], contains('void logout()'));
    });
  });
}
