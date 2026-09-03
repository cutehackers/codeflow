// CORE <-> adapter protocol (schemas/adapter-protocol.schema.json).
// Production traffic is JSON-RPC 2.0 over Content-Length framed stdio.
// AdapterServer.handleLine remains a direct legacy helper for package tests.
//
// Documented deviation: a MALFORMED line (invalid JSON / non-object) cannot
// carry a correlation id, but the error envelope schema requires a string id;
// we emit id:"" for those and CORE drops them (unknown-id rule).
library;

import 'dart:convert';
import 'dart:io';

import 'harvest.dart';
import 'sha256.dart';
import 'slice.dart';

/// Protocol major version this adapter speaks.
const int protocolVersion = 1;
const String jsonRpcVersion = '2.0';
const String analyzerVersion = 'dart-structural/0.1.0';
const String analysisSchemaId =
    'https://codeflow.local/schemas/adapter-analysis.schema.json';
const String _readSetSchemaId =
    'https://codeflow.local/schemas/analysis-read-set.schema.json';
const String _closureSchemaId =
    'https://codeflow.local/schemas/causal-observation-closure.schema.json';
final RegExp _secretPattern = RegExp(
    r'''\b(?:api[_-]?key|secret|token|password)\s*[:=]\s*['"]?[^\s;'"}]+['"]?''',
    caseSensitive: false);

const Map<String, Object?> capabilities = {
  'cancellation': true,
  'progress': true,
  'batchAck': true,
  'snapshotOverlay': true,
  'analysisMetadata': true,
  'maxMessageBytes': 1048576,
  'maxInFlight': 64,
};

class AdapterServer {
  AdapterServer({
    required Stream<String> requests,
    required void Function(String line) respond,
    Map<String, Object?> Function(Map<Object?, Object?> params)? harvestFn,
    Map<String, Object?> Function({required String repoRoot})? detectFn,
    Map<String, Object?> Function(Map<Object?, Object?> params)? sliceFn,
  })  : _requests = requests,
        _respond = respond,
        _harvest = harvestFn ?? _defaultHarvest,
        _detect = detectFn ??
            (({required String repoRoot}) => detectRepo(repoRoot: repoRoot)),
        _slice = sliceFn ?? _defaultSlice;

  final Stream<String> _requests;
  final void Function(String line) _respond;
  final Map<String, Object?> Function(Map<Object?, Object?> params) _harvest;
  final Map<String, Object?> Function({required String repoRoot}) _detect;
  final Map<String, Object?> Function(Map<Object?, Object?> params) _slice;

  bool _shutdownRequested = false;

  /// Reads requests until EOF or an acked shutdown; writes one response line
  /// per request. Never crashes on bad input - every failure becomes a typed
  /// error envelope.
  Future<void> serve() async {
    await for (final raw in _requests) {
      final response = handleLine(raw);
      if (response != null) {
        _respond(response);
      }
      if (_shutdownRequested) break;
    }
  }

  /// Handles one request line; returns the encoded response line, or null
  /// when nothing may be written (never happens in practice today).
  String? handleLine(String raw) {
    Object? decoded;
    try {
      decoded = jsonDecode(raw);
    } on FormatException catch (e) {
      return _error(
          '', 'E_BAD_REQUEST', 'request line is not valid JSON: ${e.message}');
    } catch (e) {
      return _error('', 'E_BAD_REQUEST', 'request line is not valid JSON');
    }
    if (decoded is! Map) {
      return _error('', 'E_BAD_REQUEST', 'request must be a JSON object');
    }

    final v = decoded['v'];
    final id = decoded['id'] is String && (decoded['id'] as String).isNotEmpty
        ? decoded['id'] as String
        : '';

    if (v is! int || v != protocolVersion) {
      return _error(id, 'E_UNSUPPORTED_VERSION',
          'unsupported protocol version ${jsonEncode(v)}; expected $protocolVersion');
    }

    try {
      return _dispatch(id, decoded);
    } on ArgumentError catch (e) {
      return _error(id, 'E_BAD_REQUEST', '${e.message ?? e}');
    } catch (e) {
      // Any unexpected exception becomes a typed internal error; the loop
      // stays alive for the next request.
      return _error(id, 'E_ADAPTER_INTERNAL', '$e');
    }
  }

  /// Handles one production JSON-RPC request. [handleLine] remains the
  /// legacy direct helper used by package tests and is not used by the framed
  /// stdio entrypoint.
  Map<String, Object?> handleRpcRequest(Object? decoded) {
    final id = decoded is Map && decoded['id'] is String
        ? decoded['id'] as String
        : '';
    if (decoded is! Map) {
      return _rpcError(id, 'E_BAD_REQUEST', 'request must be a JSON object');
    }
    if (decoded['jsonrpc'] != jsonRpcVersion) {
      return _rpcError(id, 'E_UNSUPPORTED_VERSION', 'jsonrpc must be "2.0"');
    }
    if (id.isEmpty) {
      return _rpcError(id, 'E_BAD_REQUEST', 'request id must be non-empty');
    }
    final method = decoded['method'];
    if (method is! String) {
      return _rpcError(id, 'E_BAD_REQUEST', 'method must be a string');
    }
    final rawParams = decoded['params'];
    if (rawParams is! Map) {
      return _rpcError(id, 'E_BAD_REQUEST', 'params must be a JSON object');
    }
    final op = method == 'ping' ? 'ping' : method;
    if (!{
      'initialize',
      'ping',
      'detect',
      'harvest_candidates',
      'slice',
      'shutdown'
    }.contains(method)) {
      return _rpcError(id, 'E_BAD_REQUEST', 'unknown method: $method');
    }
    if (method == 'initialize' || method == 'ping') {
      return {
        'jsonrpc': jsonRpcVersion,
        'id': id,
        'result': {
          'adapterVersion': adapterVersion,
          'protocolVersion': protocolVersion,
          'protocolVersions': [protocolVersion],
          'analyzerVersion': analyzerVersion,
          'schemaId': analysisSchemaId,
          'schemaVersion': 1,
          'capabilities': capabilities,
        },
      };
    }

    final legacy = jsonDecode(handleLine(jsonEncode({
      'v': protocolVersion,
      'id': id,
      'op': op,
      'params': rawParams,
    }))!);
    if (op == 'detect' && rawParams['repoRoot'] is String) {
      final detected = detectRepo(
        repoRoot: rawParams['repoRoot'] as String,
        contentOverlay: _overlayFromParams(rawParams.cast<Object?, Object?>()),
      );
      final result = <String, Object?>{
        ...detected,
        ..._analysisMetadata(rawParams.cast<Object?, Object?>(), op, const []),
      };
      return {'jsonrpc': jsonRpcVersion, 'id': id, 'result': result};
    }
    if (legacy is! Map || legacy['ok'] != true) {
      final error = legacy is Map && legacy['err'] is Map
          ? legacy['err'] as Map
          : const <Object?, Object?>{};
      return _rpcError(
        id,
        error['code'] is String
            ? error['code'] as String
            : 'E_ADAPTER_INTERNAL',
        error['message'] is String
            ? error['message'] as String
            : 'adapter request failed',
        retryable: error['retryable'] == true,
      );
    }
    var result = (legacy['result'] as Map?)?.cast<String, Object?>() ??
        <String, Object?>{};
    if (op == 'detect' || op == 'harvest_candidates' || op == 'slice') {
      final explicitPaths = <String>[];
      if (rawParams['entrySymbolPath'] is String) {
        explicitPaths
            .add((rawParams['entrySymbolPath'] as String).split('#').first);
      }
      result = {...result, ..._analysisMetadata(rawParams, op, explicitPaths)};
    }
    return {'jsonrpc': jsonRpcVersion, 'id': id, 'result': result};
  }

  String? _dispatch(String id, Map decoded) {
    final op = decoded['op'];
    if (op is! String) {
      return _error(id, 'E_BAD_REQUEST', 'missing or non-string "op"');
    }
    final paramsRaw = decoded['params'];
    final params = paramsRaw is Map ? paramsRaw : <Object?, Object?>{};

    switch (op) {
      case 'ping':
        return _ok(id, {
          'adapterVersion': adapterVersion,
          'protocolVersion': protocolVersion,
        });

      case 'detect':
        final repoRoot = _requireRepoRoot(params);
        return _ok(id, {..._detect(repoRoot: repoRoot)});

      case 'harvest_candidates':
        _requireRepoRoot(params);
        return _ok(id, _harvest(params));

      case 'slice':
        final repoRoot = _requireRepoRoot(params);
        final candidateId = params['candidateId'];
        if (candidateId is! String || candidateId.isEmpty) {
          throw ArgumentError(
              'params.candidateId (non-empty string) is required');
        }
        final entrySymbolPath = params['entrySymbolPath'];
        if (entrySymbolPath is! String || entrySymbolPath.isEmpty) {
          throw ArgumentError(
              'params.entrySymbolPath (non-empty string) is required');
        }
        final opts = params['opts'] is Map
            ? params['opts'] as Map<String, Object?>
            : <String, Object?>{};
        return _ok(
            id,
            _slice({
              'repoRoot': repoRoot,
              'candidateId': candidateId,
              'entrySymbolPath': entrySymbolPath,
              'opts': opts,
            }));

      case 'shutdown':
        _shutdownRequested = true;
        return _ok(id, {'acknowledged': true});

      default:
        return _error(id, 'E_BAD_REQUEST', 'unknown op: $op');
    }
  }

  static Map<String, Object?> _defaultHarvest(Map<Object?, Object?> params) =>
      harvestCandidates(params);

  static Map<String, Object?> _defaultSlice(Map<Object?, Object?> params) =>
      sliceCandidate(
        repoRoot: params['repoRoot'] as String,
        candidateId: params['candidateId'] as String,
        entrySymbolPath: params['entrySymbolPath'] as String,
        opts: (params['opts'] as Map?)?.cast<String, Object?>() ?? const {},
        contentOverlay: _overlayFromParams(params),
      );

  String _requireRepoRoot(Map<Object?, Object?> params) {
    final repoRoot = params['repoRoot'];
    if (repoRoot is! String || repoRoot.isEmpty) {
      throw ArgumentError('params.repoRoot (non-empty string) is required');
    }
    return repoRoot;
  }

  static String _ok(String id, Map<String, Object?> result) =>
      jsonEncode({'id': id, 'ok': true, 'result': result});

  static String _error(String id, String code, String message,
      {bool retryable = false}) {
    return jsonEncode({
      'id': id,
      'ok': false,
      'err': {'code': code, 'message': message, 'retryable': retryable},
    });
  }

  static Map<String, Object?> _rpcError(String id, String code, String message,
      {bool retryable = false}) {
    final rpcCode = code == 'E_BAD_REQUEST' || code == 'E_UNSUPPORTED_VERSION'
        ? -32602
        : -32000;
    return {
      'jsonrpc': jsonRpcVersion,
      'id': id,
      'error': {
        'code': rpcCode,
        'message': _redactDiagnostic(message),
        'data': {
          'code': code,
          'retryable': retryable,
          'detail': _redactDiagnostic(message),
        },
      },
    };
  }

  static String _redactDiagnostic(String input) {
    final redacted =
        input.replaceAllMapped(_secretPattern, (_) => '***REDACTED***');
    return redacted.length > 512 ? redacted.substring(0, 512) : redacted;
  }

  static Map<String, Object?> _analysisMetadata(Map<Object?, Object?> params,
      String operation, List<String> explicitPaths) {
    final snapshot = params['snapshot'] is Map
        ? (params['snapshot'] as Map).cast<Object?, Object?>()
        : const <Object?, Object?>{};
    final overlayRaw = params['contentOverlay'] ??
        snapshot['contentOverlay'] ??
        snapshot['files'];
    final documents = <Map<String, Object?>>[];
    if (overlayRaw is Map) {
      final keys = overlayRaw.keys.whereType<String>().toList()..sort();
      for (final key in keys.take(4096)) {
        final value = overlayRaw[key];
        final content = value is String
            ? value
            : value is Map && value['content'] is String
                ? value['content'] as String
                : null;
        if (content == null ||
            key.isEmpty ||
            key.startsWith('/') ||
            key.contains('..')) continue;
        documents.add({
          'path': key.replaceAll('\\', '/'),
          'contentHash': sha256Hex(content),
          'byteLength': utf8.encode(content).length,
        });
      }
    } else {
      final root =
          params['repoRoot'] is String ? params['repoRoot'] as String : '';
      final paths = <String>[...explicitPaths];
      if (operation == 'detect') paths.add('pubspec.yaml');
      for (final rel in paths.toSet()) {
        final file = File('$root/$rel');
        try {
          if (!file.existsSync()) continue;
          final content = file.readAsStringSync();
          documents.add({
            'path': rel,
            'contentHash': sha256Hex(content),
            'byteLength': utf8.encode(content).length,
          });
        } catch (_) {}
      }
    }
    documents
        .sort((a, b) => (a['path']! as String).compareTo(b['path']! as String));
    var basis = params['computedBasisId'] is String
        ? params['computedBasisId'] as String
        : snapshot['computedBasisId'] is String
            ? snapshot['computedBasisId'] as String
            : '';
    if (basis.isEmpty) {
      basis = sha256Hex(
          documents.map((d) => '${d['path']}:${d['contentHash']}\n').join());
    }
    final epoch = params['workspaceEpoch'] is int
        ? params['workspaceEpoch'] as int
        : snapshot['workspaceEpoch'] is int
            ? snapshot['workspaceEpoch'] as int
            : 0;
    final readSetId =
        'readset-${sha256Hex('$basis:$epoch:$operation').substring(0, 24)}';
    final closureId =
        'closure-${sha256Hex('$readSetId:$operation').substring(0, 24)}';
    final profile = <String, Object?>{
      'adapter': 'dart',
      'features': [
        'symbols',
        'calls',
        'snapshot_overlay',
        'negative_lookup',
        'membership',
        'dependency_frontier'
      ],
      'protocolVersions': [protocolVersion],
      'coverageBoundary': {
        'includedSourceRoots': ['.'],
        'excludedReasons': []
      },
    };
    final readSet = <String, Object?>{
      'schemaId': _readSetSchemaId,
      'schemaVersion': 1,
      'readSetId': readSetId,
      'computedBasisId': basis,
      'workspaceEpoch': epoch,
      'documents': documents,
      'indexes': [],
      'negativeObservations': [],
      'membershipObservations': [
        {
          'kind': 'source_membership',
          'path': '.',
          'valueHash': sha256Hex(documents.map((d) => d['path']).join('\n'))
        }
      ],
      'dependencyFrontiers': [
        {
          'kind': 'dependency_frontier',
          'path': operation,
          'detail': 'frontier bounded at adapter boundary'
        }
      ],
      'adapterVersions': {'dart': adapterVersion},
    };
    final closure = <String, Object?>{
      'schemaId': _closureSchemaId,
      'schemaVersion': 1,
      'closureId': closureId,
      'analysisReadSetId': readSetId,
      'computedBasisId': basis,
      'workspaceEpoch': epoch,
      'closureStatus': 'closed',
      'negativeObservations': [],
      'membershipObservations': readSet['membershipObservations'],
      'dependencyFrontiers': readSet['dependencyFrontiers'],
      'capabilityProfile': profile,
      'coverageBoundary': profile['coverageBoundary'],
      'incompleteReasons': [],
      'closureDigest':
          sha256Hex(jsonEncode({'readSet': readSet, 'profile': profile})),
    };
    return {
      'schemaId': analysisSchemaId,
      'schemaVersion': 1,
      'operation': operation,
      'computedBasisId': basis,
      'workspaceEpoch': epoch,
      'analysisReadSet': readSet,
      'causalObservationClosure': closure,
      'capabilityProfile': profile,
      'analyzerVersion': analyzerVersion,
      'diagnostics': [],
    };
  }
}

Map<String, String>? _overlayFromParams(Map<Object?, Object?> params) {
  final snapshot = params['snapshot'] is Map
      ? (params['snapshot'] as Map).cast<Object?, Object?>()
      : const <Object?, Object?>{};
  final raw = params['contentOverlay'] ??
      snapshot['contentOverlay'] ??
      snapshot['files'];
  if (raw is! Map) return null;
  final out = <String, String>{};
  for (final entry in raw.entries) {
    if (entry.key is! String) continue;
    final key = (entry.key as String)
        .replaceAll('\\', '/')
        .replaceFirst(RegExp(r'^\./'), '');
    if (key.isEmpty || key.startsWith('/') || key.contains('..')) continue;
    final value = entry.value;
    if (value is String) {
      out[key] = value;
    } else if (value is Map && value['content'] is String) {
      out[key] = value['content'] as String;
    }
  }
  return out;
}
