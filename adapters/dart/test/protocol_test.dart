import 'dart:convert';

import 'package:test/test.dart';

import 'package:codeflow_dart_adapter/src/protocol.dart';
import 'helpers.dart';

/// Direct unit-level framing tests against AdapterServer.handleLine.
void main() {
  late AdapterServer server;
  setUp(() {
    server = AdapterServer(
      requests: const Stream<String>.empty(),
      respond: (_) {},
    );
  });

  Map<String, Object?> handle(String line) =>
      jsonDecode(server.handleLine(line)!) as Map<String, Object?>;

  test('malformed JSON responds E_BAD_REQUEST with empty id', () {
    for (final bad in ['', '   ', '{', 'not json at all', '[1,2]', '"str"']) {
      final r = handle(bad);
      expect(r['id'], '', reason: 'input: $bad');
      expect(r['ok'], false, reason: 'input: $bad');
      final err = r['err']! as Map;
      expect(err['code'], 'E_BAD_REQUEST', reason: 'input: $bad');
      expect(err['retryable'], false);
      expect(err.keys.toSet(), {'code', 'message', 'retryable'});
    }
  });

  test('ping negotiates versions and echoes the id', () {
    final r = handle('{"v":1,"id":"abc-1","op":"ping","params":{}}');
    expect(r.keys.toSet(), {'id', 'ok', 'result'});
    expect(r['id'], 'abc-1');
    expect(r['ok'], true);
    expect(r['result'], {'adapterVersion': '0.1.0', 'protocolVersion': 1});
  });

  test('missing or non-integer v => E_UNSUPPORTED_VERSION', () {
    for (final line in [
      '{"id":"x","op":"ping","params":{}}',
      '{"v":"1","id":"x","op":"ping","params":{}}',
      '{"v":2,"id":"x","op":"ping","params":{}}',
    ]) {
      final r = handle(line);
      expect((r['err']! as Map)['code'], 'E_UNSUPPORTED_VERSION');
      // id still echoed when present.
      if (line.contains('"id":"x"')) expect(r['id'], 'x');
    }
  });

  test('unknown op => E_BAD_REQUEST; missing op likewise', () {
    expect((handle('{"v":1,"id":"k","op":"warp","params":{}}')['err']!
        as Map)['code'], 'E_BAD_REQUEST');
    expect((handle('{"v":1,"id":"k","params":{}}')['err']! as Map)['code'],
        'E_BAD_REQUEST');
  });

  test('detect requires repoRoot (typed error, not a crash)', () {
    final r = handle('{"v":1,"id":"d","op":"detect","params":{}}');
    expect(r['ok'], false);
    expect((r['err']! as Map)['code'], 'E_BAD_REQUEST');

    final ok = handle(
      '{"v":1,"id":"d2","op":"detect","params":{"repoRoot":"${exampleAppRoot().replaceAll("\\", "/")}"}}',
    );
    expect(ok['ok'], true);
    expect(ok['result'], {'language': 'dart', 'confident': true});
  });

  test('harvest_candidates without repoRoot is E_BAD_REQUEST', () {
    final r = handle('{"v":1,"id":"h","op":"harvest_candidates","params":{}}');
    expect(r['ok'], false);
    expect((r['err']! as Map)['code'], 'E_BAD_REQUEST');
  });

  test('slice answers the ticket-07 placeholder error', () {
    final r = handle('{"v":1,"id":"s","op":"slice","params":{}}');
    expect(r['ok'], false);
    final err = r['err']! as Map;
    expect(err['code'], 'E_BAD_REQUEST');
    expect(err['message'], 'not implemented yet');
  });

  test('shutdown acks and stops the loop', () async {
    final responses = <String>[];
    final server2 = AdapterServer(
      requests: Stream.fromIterable([
        '{"v":1,"id":"a","op":"ping","params":{}}',
        '{"v":1,"id":"b","op":"shutdown","params":{}}',
        '{"v":1,"id":"c","op":"ping","params":{}}',
      ]),
      respond: responses.add,
    );
    await server2.serve();
    expect(responses.length, 2); // ping ack + shutdown ack; c never runs
    expect(jsonDecode(responses[0])['id'], 'a');
    expect(jsonDecode(responses[1])['id'], 'b');
    expect(jsonDecode(responses[1])['result'], {'acknowledged': true});
  });

  test('internal exceptions become E_ADAPTER_INTERNAL, loop survives', () {
    final crashingServer = AdapterServer(
      requests: const Stream<String>.empty(),
      respond: (_) {},
      harvestFn: (params) => throw StateError('boom'),
    );
    final r = jsonDecode(crashingServer.handleLine(
            '{"v":1,"id":"z","op":"harvest_candidates","params":{"repoRoot":"/tmp"}}')!)
        as Map<String, Object?>;
    expect(r['ok'], false);
    final err = r['err']! as Map;
    expect(err['code'], 'E_ADAPTER_INTERNAL');
    expect(err['message'], contains('boom'));
    expect(r['id'], 'z');

    // The same server instance still answers a healthy request afterwards.
    final ok = jsonDecode(crashingServer.handleLine(
        '{"v":1,"id":"y","op":"ping","params":{}}')!) as Map<String, Object?>;
    expect(ok['ok'], true);
  });
}
