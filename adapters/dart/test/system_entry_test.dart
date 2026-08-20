import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:test/test.dart';

Future<Map<String, dynamic>> _call(
  IOSink input,
  StreamIterator<String> output,
  int id,
  String method,
  Map<String, dynamic> params,
) async {
  input.writeln(
    jsonEncode({
      'jsonrpc': '2.0',
      'id': id,
      'method': method,
      'params': params,
    }),
  );
  await input.flush();
  expect(await output.moveNext(), isTrue);
  return jsonDecode(output.current) as Map<String, dynamic>;
}

void main() {
  test('discovers bounded FCM, session, and lifecycle system entries', () async {
    final repo = await Directory.systemTemp.createTemp(
      'codeflow-system-entry-',
    );
    addTearDown(() => repo.delete(recursive: true));
    final lib = Directory('${repo.path}${Platform.pathSeparator}lib');
    await lib.create();
    await File('${lib.path}${Platform.pathSeparator}push.dart').writeAsString(
      '''
class PushRegistration {
  void start() { FirebaseMessaging.instance.onTokenRefresh.listen(_registerToken); }
  void _registerToken(String token) { _persistToken(token); }
  void _persistToken(String token) { context.go('/registered'); }
}
class SecondaryPushRegistration {
  void start() { FirebaseMessaging.instance.onTokenRefresh.listen(_registerToken); }
  void _registerToken(String token) { registerSecondary(token); }
}
class SessionRefresh {
  void start() { sessionChanges.listen(_refreshSession); }
  void _refreshSession(Object session) { renew(session); }
}
class AppLifecycle extends State<Object> with WidgetsBindingObserver {
  void initState() { warmUp(); }
  void didChangeAppLifecycleState(Object state) { resume(state); }
}
class SecondaryLifecycle extends State<Object> {
  void initState() { restore(); }
}
''',
    );
    final process = await Process.start(Platform.resolvedExecutable, [
      File('bin/codeflow-dart-adapter.dart').absolute.path,
      '--stdio',
    ], workingDirectory: Directory.current.path);
    addTearDown(() async {
      process.stdin.close();
      await process.exitCode;
    });
    final output = StreamIterator(
      process.stdout.transform(utf8.decoder).transform(const LineSplitter()),
    );
    addTearDown(output.cancel);
    final initialized = await _call(process.stdin, output, 1, 'initialize', {
      'protocol_version': '1',
    });
    expect(initialized['result'], isNotNull);
    final discovered = await _call(
      process.stdin,
      output,
      2,
      'discoverEntryPoints',
      {'repository': repo.path},
    );
    final entries =
        (discovered['result'] as Map<String, dynamic>)['entry_points']
            as List<dynamic>;
    final entry = entries.cast<Map>().singleWhere(
      (item) =>
          item['flow_id'] ==
          'system:push-token:lib/push.dart:PushRegistration:_registerToken',
    );
    expect((entry['anchor'] as Map)['path'], 'lib/push.dart');
    final ids = entries.cast<Map>().map((item) => item['flow_id']).toSet();
    expect(
      ids,
      contains(
        'system:push-token:lib/push.dart:SecondaryPushRegistration:_registerToken',
      ),
    );
    expect(
      ids,
      contains('system:session:lib/push.dart:SessionRefresh:_refreshSession'),
    );
    expect(
      ids,
      contains('system:lifecycle:lib/push.dart:AppLifecycle:initState'),
    );
    expect(
      ids,
      contains(
        'system:lifecycle:lib/push.dart:AppLifecycle:didChangeAppLifecycleState',
      ),
    );
    expect(
      ids,
      contains('system:lifecycle:lib/push.dart:SecondaryLifecycle:initState'),
    );
    final refined = await _call(process.stdin, output, 3, 'refineRouteFlow', {
      'repository': repo.path,
      'flow_id':
          'system:push-token:lib/push.dart:PushRegistration:_registerToken',
      'paths': ['lib/push.dart'],
      'analysis_paths': ['lib/push.dart'],
    });
    final facts =
        (refined['result'] as Map<String, dynamic>)['facts'] as List<dynamic>;
    expect(
      facts.cast<Map>().where((fact) => fact['kind'] == 'system_event'),
      hasLength(1),
    );
    expect(
      facts.cast<Map>().where(
        (fact) =>
            fact['kind'] == 'route_transition' &&
            fact['object'] == 'route:/registered',
      ),
      hasLength(1),
    );
  });
}
