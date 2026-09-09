import 'dart:convert';
import 'dart:io';

import 'package:test/test.dart';

import 'package:codeflow_dart_adapter/src/protocol.dart';
import 'package:codeflow_dart_adapter/src/sha256.dart';
import 'helpers.dart';

Map<String, Object?> _snapshotFor(Map<String, String> files) {
  final paths = files.keys.toList()..sort();
  final documents = <Map<String, Object?>>[];
  final treeParts = <String>[];
  for (final path in paths) {
    final content = files[path]!;
    final hash = sha256Hex(content);
    documents.add({
      'path': path,
      'documentRevisionId': 'rev-$hash',
      'contentId': hash,
      'documentVersion': 1,
      'contentHash': hash,
      'byteLength': utf8.encode(content).length,
    });
    treeParts.add('$path:$hash\n');
  }
  final tree = sha256Hex(treeParts.join());
  return {
    'schemaId': analyzerRequestSchemaId,
    'schemaVersion': 2,
    'snapshotId': 'snapshot-${tree.substring(0, 12)}',
    'computedBasisId': 'basis-${tree.substring(0, 12)}',
    'workspaceEpoch': 7,
    'rootTreeId': tree,
    'dependencyFingerprint': 'dependency-${tree.substring(0, 12)}',
    'documents': documents,
    'files': files,
    'repositoryPathWriteAudit': {
      'codeflowWriteCount': 0,
      'sourceIntegrityViolation': false,
      'capturedSnapshotTreeDigest': tree,
    },
  };
}

Map<String, Object?> _rpcAnalysisParams(String id, String operation,
        Map<String, String> files, List<String> required,
        [Map<String, Object?> payload = const {}]) =>
    {
      'schemaId': analyzerRequestSchemaId,
      'schemaVersion': 2,
      'requestId': id,
      'operation': operation,
      'requiredObservations': required,
      'snapshot': _snapshotFor(files),
      'payload': payload,
    };

Map<String, Object?> _rpcResult(Map<String, Object?> response) =>
    (response['result'] as Map).cast<String, Object?>();

Map<String, Object?> _rpcReadSet(Map<String, Object?> response) =>
    (_rpcResult(response)['analysisReadSet'] as Map).cast<String, Object?>();

Map<String, Object?> _rpcClosure(Map<String, Object?> response) =>
    (_rpcResult(response)['causalObservationClosure'] as Map)
        .cast<String, Object?>();

List<Map<String, Object?>> _rpcObjects(Object? value) => value is List
    ? value
        .whereType<Map>()
        .map((item) => item.cast<String, Object?>())
        .toList()
    : <Map<String, Object?>>[];

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
    expect(r['result'], {'adapterVersion': '0.4.0', 'protocolVersion': 1});
		expect(capabilities['maxMessageBytes'], 128 * 1024 * 1024);
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

  test(
      'production RPC diagnostics redact malformed quoted keys before clipping',
      () {
    final secret = List.filled(200, 'dart-adapter-secret-').join();
    final response = server.handleRpcRequest({
      'jsonrpc': '2.0',
      'id': 'diagnostic-1',
      'method':
          'unknown {"databasePassword":"$secret","x-api-key":"$secret","clientSecret":"$secret',
      'params': <String, Object?>{},
    });
    final encoded = jsonEncode(response);
    expect(encoded, isNot(contains(secret.substring(0, 32))));
    expect(encoded, contains('***REDACTED***'));
  });

  test('structured diagnostics redact nested keys before the byte bound', () {
    final secret = List.filled(200, 'dart-nested-secret-').join();
    final encoded = redactDiagnostic({
      'outer': [
        {'clientSecret': secret},
        {'safe': List.filled(100, 'diagnostic-').join()},
      ],
    });
    expect(encoded, isNot(contains(secret.substring(0, 32))));
    expect(encoded, contains('***REDACTED***'));
  });

  test('production response writer enforces exact and oversized bounds', () {
    const limit = 1 << 20;
    final exactResponse = <String, Object?>{
      'jsonrpc': '2.0',
      'id': 'bound-1',
      'result': <String, Object?>{'padding': ''},
    };
    final emptyPaddingLength =
        encodeBoundedResponse(exactResponse, maxBytes: limit)!.length;
    (exactResponse['result'] as Map<String, Object?>)['padding'] =
        List.filled(limit - emptyPaddingLength, 'x').join();
    final exact = encodeBoundedResponse(exactResponse, maxBytes: limit);
    expect(exact, isNotNull);
    expect(exact!.length, limit);

    final oversized = <String, Object?>{
      'jsonrpc': '2.0',
      'id': 'bound-2',
      'result': <String, Object?>{
        'padding': List.filled(limit + 1, 'x').join(),
      },
    };
    final fallback = encodeBoundedResponse(oversized, maxBytes: limit);
    expect(fallback, isNotNull);
    expect(fallback!.length, lessThanOrEqualTo(limit));
    final decoded = jsonDecode(utf8.decode(fallback)) as Map;
    expect(decoded['error'], isNotNull);
    expect(decoded['result'], isNull);
    expect((decoded['error'] as Map)['data']['code'], 'E_ADAPTER_INTERNAL');

    final hugeIdFallback = encodeBoundedResponse({
      'jsonrpc': '2.0',
      'id': List.filled(512, 'i').join(),
      'result': {'padding': List.filled(512, 'x').join()},
    }, maxBytes: 256);
    expect(hugeIdFallback, isNotNull);
    expect(hugeIdFallback!.length, lessThanOrEqualTo(256));
    final hugeIdDecoded = jsonDecode(utf8.decode(hugeIdFallback)) as Map;
    expect(hugeIdDecoded['id'], '');
    expect(hugeIdDecoded['error'], isNotNull);

    final productionHugeIdFallback = encodeBoundedResponse({
      'jsonrpc': '2.0',
      'id': List.filled(limit - 64, 'i').join(),
      'result': {'padding': List.filled(256, 'x').join()},
    }, maxBytes: limit);
    expect(productionHugeIdFallback, isNotNull);
    expect(productionHugeIdFallback!.length, lessThanOrEqualTo(limit));
    expect(jsonDecode(utf8.decode(productionHugeIdFallback))['id'], '');
    expect(
        encodeBoundedResponse(
            {'id': 'x', 'result': List.filled(256, 'x').join()},
            maxBytes: 64),
        isNull);
  });

  test('unknown op => E_BAD_REQUEST; missing op likewise', () {
    expect(
        (handle('{"v":1,"id":"k","op":"warp","params":{}}')['err']!
            as Map)['code'],
        'E_BAD_REQUEST');
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

  test('slice without repoRoot is E_BAD_REQUEST', () {
    final r = handle('{"v":1,"id":"s","op":"slice","params":{}}');
    expect(r['ok'], false);
    final err = r['err']! as Map;
    expect(err['code'], 'E_BAD_REQUEST');
    expect(err['message'], contains('repoRoot'));
  });

  test('slice with valid params executes successfully', () {
    final r = handle(jsonEncode({
      'v': 1,
      'id': 's2',
      'op': 'slice',
      'params': {
        'repoRoot': exampleAppRoot().replaceAll('\\', '/'),
        'candidateId': 'cand-7232d63b96bd6efa',
        'entrySymbolPath':
            'lib/features/auth/email_signup_notifier.dart#EmailSignupNotifier.submit',
      },
    }));
    expect(r['ok'], true);

    final result = r['result']! as Map;
    expect(result['candidateId'], 'cand-7232d63b96bd6efa');
    expect(result['language'], 'dart');
    expect(result['steps'], isNotEmpty);
  });

  test('JSON-RPC analysis uses snapshot content when live root differs',
      () async {
    final liveRoot =
        await Directory.systemTemp.createTemp('codeflow-live-root-');
    addTearDown(() => liveRoot.delete(recursive: true));
    await File('${liveRoot.path}/pubspec.yaml')
        .writeAsString('name: live_package\n');
    await Directory('${liveRoot.path}/lib').create(recursive: true);
    await File('${liveRoot.path}/lib/main.dart').writeAsString(
        'class Screen { void onPressed() { state = \'live\'; } }\n');
    const snapshotSource =
        "class Screen { void onPressed() { state = 'snapshot'; } }\n";
    final snapshotHash = sha256Hex(snapshotSource);
    final baseParams = <String, Object?>{
      'repoRoot': liveRoot.path,
      'snapshot': <String, Object?>{
        'schemaId': analyzerRequestSchemaId,
        'schemaVersion': 2,
        'snapshotId': 'snapshot-test',
        'computedBasisId': 'snapshot-basis',
        'workspaceEpoch': 3,
        'rootTreeId': 'tree-test',
        'dependencyFingerprint': 'dependency-test',
        'repositoryPathWriteAudit': <String, Object?>{
          'codeflowWriteCount': 0,
          'sourceIntegrityViolation': false,
          'capturedSnapshotTreeDigest': 'tree-test',
        },
        'documents': <Map<String, Object?>>[
          <String, Object?>{
            'path': 'lib/main.dart',
            'documentRevisionId': 'rev-$snapshotHash',
            'contentId': snapshotHash,
            'documentVersion': 1,
            'contentHash': snapshotHash,
            'byteLength': utf8.encode(snapshotSource).length,
          },
        ],
        'files': <String, String>{'lib/main.dart': snapshotSource},
      },
    };

    Map<String, Object?> paramsFor(String id, String operation,
            [Map<String, Object?> payload = const {}]) =>
        {
          ...baseParams,
          'schemaId': analyzerRequestSchemaId,
          'schemaVersion': 2,
          'requestId': id,
          'operation': operation,
          if (payload.isNotEmpty) 'payload': payload,
        };

    final harvest = server.handleRpcRequest({
      'jsonrpc': '2.0',
      'id': 'snapshot-harvest',
      'method': 'harvest_candidates',
      'params': paramsFor('snapshot-harvest', 'harvest_candidates'),
    });
    expect(harvest['error'], isNull);
    final candidates =
        ((harvest['result'] as Map)['payload'] as Map)['candidates'] as List;
    expect(candidates, isNotEmpty);
    expect(
        (candidates.first['intentSignals'] as Map)['packageName'], 'unknown');

    final slice = server.handleRpcRequest({
      'jsonrpc': '2.0',
      'id': 'snapshot-slice',
      'method': 'slice',
      'params': paramsFor('snapshot-slice', 'slice', {
        'candidateId': candidates.first['candidateId'],
        'entrySymbolPath': 'lib/main.dart#Screen.onPressed',
      }),
    });
    expect(slice['error'], isNull);
    final steps =
        (((slice['result'] as Map)['payload'] as Map)['steps'] as List)
            .toString();
    expect(steps, contains('snapshot'));
    expect(steps, isNot(contains('live')));
  });

  test('v2 observations track actual Dart operation reads', () {
    final detect = server.handleRpcRequest({
      'jsonrpc': '2.0',
      'id': 'dart-detect-missing',
      'method': 'detect',
      'params': _rpcAnalysisParams(
          'dart-detect-missing',
          'detect',
          {'README.md': 'not consulted'},
          ['negative_lookup', 'membership', 'dependency_frontier']),
    });
    expect(detect['error'], isNull);
    final unsignedClosure = Map<String, dynamic>.from(_rpcClosure(detect));
    final digest = unsignedClosure.remove('closureDigest');
    expect(digest, sha256Hex(jsonEncode({'readSet': _rpcReadSet(detect), 'closure': unsignedClosure})));
    expect(_rpcObjects(_rpcReadSet(detect)['documents']), isEmpty);
    final detectNegative =
        _rpcObjects(_rpcReadSet(detect)['negativeObservations']);
    expect(detectNegative.single['path'], 'pubspec.yaml');
    expect(detectNegative.single['detail'], contains('snapshot-'));
    expect(_rpcObjects(_rpcReadSet(detect)['membershipObservations']),
        hasLength(1));
    expect(_rpcObjects(_rpcReadSet(detect)['dependencyFrontiers']), isEmpty);
    expect(_rpcClosure(detect)['closureStatus'], 'open');
    expect(_rpcClosure(detect)['incompleteReasons'].toString(),
        contains('dependency_frontier'));

    final harvestFiles = <String, String>{
      'pubspec.yaml':
          'name: tracked_app\nenvironment:\n  sdk: ">=3.0.0 <4.0.0"\n',
      'lib/main.dart':
          'class Screen { void onPressed() { state = \'snapshot\'; } }\n',
      'README.md': 'not consulted',
    };
    final harvest = server.handleRpcRequest({
      'jsonrpc': '2.0',
      'id': 'dart-harvest',
      'method': 'harvest_candidates',
      'params': _rpcAnalysisParams('dart-harvest', 'harvest_candidates',
          harvestFiles, ['negative_lookup']),
    });
    expect(harvest['error'], isNull);
    final harvestRead = _rpcReadSet(harvest);
    final harvestPaths = _rpcObjects(harvestRead['documents'])
        .map((doc) => doc['path'])
        .toList();
    expect(harvestPaths, ['lib/main.dart', 'pubspec.yaml']);
    expect(_rpcObjects(harvestRead['membershipObservations']), hasLength(1));
    expect(_rpcObjects(harvestRead['dependencyFrontiers']).single['path'],
        'pubspec.yaml');
    expect(_rpcObjects(harvestRead['negativeObservations']), isEmpty);
    expect(_rpcClosure(harvest)['closureStatus'], 'open');

    final harvestExtraFiles = <String, String>{
      ...harvestFiles,
      'lib/extra.dart': 'class Extra {}\n',
    };
    final harvestExtra = server.handleRpcRequest({
      'jsonrpc': '2.0',
      'id': 'dart-harvest-extra',
      'method': 'harvest_candidates',
      'params': _rpcAnalysisParams('dart-harvest-extra', 'harvest_candidates',
          harvestExtraFiles, const []),
    });
    final firstMembership =
        _rpcObjects(harvestRead['membershipObservations']).single['valueHash'];
    final extraMembership =
        _rpcObjects(_rpcReadSet(harvestExtra)['membershipObservations'])
            .single['valueHash'];
    expect(extraMembership, isNot(firstMembership));

    final slice = server.handleRpcRequest({
      'jsonrpc': '2.0',
      'id': 'dart-slice',
      'method': 'slice',
      'params': _rpcAnalysisParams('dart-slice', 'slice', {
        'pubspec.yaml': harvestFiles['pubspec.yaml']!,
        'lib/main.dart': harvestFiles['lib/main.dart']!,
        'README.md': harvestFiles['README.md']!,
      }, [
        'membership'
      ], {
        'candidateId': 'candidate-v2',
        'entrySymbolPath': 'lib/main.dart#Screen.onPressed',
      }),
    });
    expect(slice['error'], isNull);
    final sliceRead = _rpcReadSet(slice);
    final slicePaths =
        _rpcObjects(sliceRead['documents']).map((doc) => doc['path']).toList();
    expect(slicePaths, ['lib/main.dart', 'pubspec.yaml']);
    expect(_rpcObjects(sliceRead['membershipObservations']), hasLength(1));
    expect(_rpcObjects(sliceRead['dependencyFrontiers']).single['path'],
        'pubspec.yaml');
    expect(_rpcClosure(slice)['closureStatus'], 'closed');

    final unsupported = server.handleRpcRequest({
      'jsonrpc': '2.0',
      'id': 'dart-unsupported',
      'method': 'harvest_candidates',
      'params': _rpcAnalysisParams('dart-unsupported', 'harvest_candidates',
          harvestFiles, ['runtime_observation']),
    });
    expect(_rpcClosure(unsupported)['closureStatus'], 'open');
    expect(_rpcClosure(unsupported)['incompleteReasons'].toString(),
        contains('runtime_observation'));
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
