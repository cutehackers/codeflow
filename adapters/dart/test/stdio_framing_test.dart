import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:test/test.dart';

import 'helpers.dart';

/// Spawns the real adapter binary and feeds NDJSON lines through the stdin
/// sink, asserting response framing, correlation ids and clean shutdown.
void main() {
  test(
    'protocol framing over stdio '
    '(ping/malformed/unknown/version/detect/harvest/slice/shutdown)',
    () async {
      final packageRoot = findPackageRoot();
      final exampleRoot = exampleAppRoot().replaceAll('\\', '/');
      final process = await Process.start(
        Platform.resolvedExecutable,
        ['bin/codeflow_dart_adapter.dart'],
        workingDirectory: packageRoot,
      );

      final responses = <Map<String, Object?>>[];
      final gotAll = Completer<void>();
      process.stdout
          .transform(utf8.decoder)
          .transform(const LineSplitter())
          .listen((line) {
        responses.add(jsonDecode(line) as Map<String, Object?>);
        if (responses.length == 9 && !gotAll.isCompleted) {
          gotAll.complete();
        }
      });

      final requests = [
        '{"v":1,"id":"p1","op":"ping","params":{}}',
        'this is not json',
        '{"v":1,"id":"u1","op":"teleport","params":{}}',
        '{"v":2,"id":"v2","op":"ping","params":{}}',
        '42',
        jsonEncode({
          'v': 1,
          'id': 'd1',
          'op': 'detect',
          'params': {'repoRoot': exampleRoot},
        }),
        jsonEncode({
          'v': 1,
          'id': 'h1',
          'op': 'harvest_candidates',
          'params': {'repoRoot': exampleRoot},
        }),
        '{"v":1,"id":"s1","op":"slice","params":{}}',
        '{"v":1,"id":"x1","op":"shutdown","params":{}}',
      ];
      process.stdin.writeln(requests.join('\n'));
      await process.stdin.flush();
      await process.stdin.close();

      await gotAll.future.timeout(const Duration(minutes: 2));
      final exitCode = await process.exitCode;
      expect(exitCode, 0);
      expect(responses, hasLength(9));

      // 1. ping: version negotiation.
      expect(responses[0]['id'], 'p1');
      expect(responses[0]['ok'], true);
      expect(responses[0]['result'], {
        'adapterVersion': '0.1.0',
        'protocolVersion': 1,
      });

      // 2. malformed line: typed error, documented empty-id deviation.
      expect(responses[1]['id'], '');
      expect(responses[1]['ok'], false);
      expect((responses[1]['err']! as Map)['code'], 'E_BAD_REQUEST');
      expect(
        responses[1].keys.toSet(),
        {'id', 'ok', 'err'},
      );

      // 3. unknown op.
      expect(responses[2]['id'], 'u1');
      expect((responses[2]['err']! as Map)['code'], 'E_BAD_REQUEST');

      // 4. wrong protocol version.
      expect(responses[3]['id'], 'v2');
      expect(
        (responses[3]['err']! as Map)['code'],
        'E_UNSUPPORTED_VERSION',
      );

      // 5. non-object line.
      expect(responses[4]['id'], '');
      expect((responses[4]['err']! as Map)['code'], 'E_BAD_REQUEST');

      // 6. detect on the fixture.
      expect(responses[5]['id'], 'd1');
      expect(responses[5]['ok'], true);
      expect(responses[5]['result'], {'language': 'dart', 'confident': true});

      // 7. harvest on the fixture: ok envelope wrapping candidate array.
      expect(responses[6]['id'], 'h1');
      expect(responses[6]['ok'], true);
      final result = responses[6]['result']! as Map;
      expect(result.keys.toSet(), {'candidates'});
      final candidates = result['candidates']! as List;
      expect(candidates, isNotEmpty);
      expect(candidates.first['candidateId'],
          matches(RegExp(r'^cand-[a-z0-9]{16}$')));

      // 8. slice is deferred to ticket 07.
      expect(responses[7]['id'], 's1');
      expect(responses[7]['ok'], false);
      expect((responses[7]['err']! as Map)['code'], 'E_BAD_REQUEST');
      expect((responses[7]['err']! as Map)['message'], 'not implemented yet');
      expect((responses[7]['err']! as Map)['retryable'], false);

      // 9. shutdown ack; exit 0 asserted above.
      expect(responses[8]['id'], 'x1');
      expect(responses[8]['ok'], true);
      expect(responses[8]['result'], {'acknowledged': true});
    },
    timeout: const Timeout(Duration(minutes: 3)),
  );
}
