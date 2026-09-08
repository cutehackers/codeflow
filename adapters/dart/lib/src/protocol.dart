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

import 'analysis_tracker.dart';
import 'harvest.dart';
import 'secret.dart';
import 'slice.dart';

export 'secret.dart'
    show DiagnosticRedaction, redactDiagnostic, redactValue, redactionMarker;

/// Protocol major version this adapter speaks.
const int protocolVersion = 1;
const String jsonRpcVersion = '2.0';
const String analyzerVersion = 'dart-structural/0.4.0';
const String analysisSchemaId =
    'https://codeflow.local/schemas/adapter-analysis.schema.json';
const String analyzerRequestSchemaId =
    'https://codeflow.local/schemas/rflsc.analyzer-request.v2.schema.json';
const String analyzerResultSchemaId =
    'https://codeflow.local/schemas/rflsc.analyzer-result.v2.schema.json';
const String _readSetV2SchemaId =
    'https://codeflow.local/schemas/rflsc.analysis-read-set.v2.schema.json';
const String _closureV2SchemaId =
    'https://codeflow.local/schemas/rflsc.observation-closure.v2.schema.json';
const Map<String, Object?> capabilities = {
  'cancellation': true,
  'progress': true,
  'batchAck': true,
  'snapshotOverlay': true,
  'analysisMetadata': true,
  'maxMessageBytes': 1048576,
  'maxInFlight': 64,
};

/// Encodes one outbound response and enforces the common negotiated body
/// bound before a Content-Length frame is written. An oversized value is
/// replaced by one small typed error without recursively invoking the writer.
List<int>? encodeBoundedResponse(Object? value, {int maxBytes = 1 << 20}) {
  final limit = maxBytes > 0 ? maxBytes : 1 << 20;
  List<int>? body;
  try {
    body = utf8.encode(jsonEncode(value));
  } catch (_) {
    body = null;
  }
  if (body != null && body.length <= limit) return body;

  final id = value is Map && value['id'] is String ? value['id'] as String : '';
  final fallback = <String, Object?>{
    'jsonrpc': jsonRpcVersion,
    'id': id,
    'error': <String, Object?>{
      'code': -32000,
      'message': 'adapter response exceeds maxMessageBytes',
      'data': <String, Object?>{
        'code': 'E_ADAPTER_INTERNAL',
        'retryable': false,
      },
    },
  };
  try {
    body = utf8.encode(jsonEncode(fallback));
  } catch (_) {
    return null;
  }
  if (body.length > limit) {
    // An untrusted id can consume the remaining bound. Drop it and retry the
    // fixed-size typed error before declaring the bound too small.
    fallback['id'] = '';
    try {
      body = utf8.encode(jsonEncode(fallback));
    } catch (_) {
      return null;
    }
  }
  return body.length <= limit ? body : null;
}

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
    final params = rawParams.cast<Object?, Object?>();
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

    if ({'detect', 'harvest_candidates', 'slice'}.contains(method)) {
      final requestError = _validateAnalyzerRequestV2(id, method, params);
      if (requestError != null) {
        return _rpcError(id, 'E_BAD_REQUEST', requestError);
      }
    }

    final snapshotOverlay = _overlayFromParams(params);
    if ({'detect', 'harvest_candidates', 'slice'}.contains(method) &&
        snapshotOverlay == null) {
      return _rpcError(id, 'E_BAD_REQUEST',
          'snapshot.files (immutable protocol content) is required');
    }

    // The line helper remains compatible with direct package tests that pass
    // repoRoot. Production RPC requests use a disposable root only for
    // relative-name parsing, while every source read is forced through the
    // snapshot overlay.
    final operationPayload = params['payload'] is Map
        ? (params['payload'] as Map).cast<Object?, Object?>()
        : const <Object?, Object?>{};
    final internalParams = <Object?, Object?>{...params, ...operationPayload};
    if (operationPayload['repoRoot'] is String &&
        (operationPayload['repoRoot'] as String).isNotEmpty) {
      internalParams['repoRoot'] = operationPayload['repoRoot'];
    } else {
      internalParams['repoRoot'] = Directory.current.path;
    }
    final trackerParams = <Object?, Object?>{
      ...params,
      'repoRoot': internalParams['repoRoot'],
    };
    final tracker = AnalysisObservationTracker(trackerParams, op);
    internalParams['_analysisTracker'] = tracker;

    if (op == 'detect') {
      final detected = detectRepo(
        repoRoot: internalParams['repoRoot'] as String,
        contentOverlay: snapshotOverlay,
        tracker: tracker,
      );
      final result = <String, Object?>{
        ...detected,
      };
      final metadata = _analysisMetadata(
          {...internalParams, ...params}, op, const [], tracker);
      return {
        'jsonrpc': jsonRpcVersion,
        'id': id,
        'result': _analyzerResultV2(id, op, params, result, metadata),
      };
    }
    try {
      Map<String, Object?> result;
      if (op == 'harvest_candidates') {
        // Keep the complete overlay on the private call. The legacy line
        // dispatcher intentionally cannot be used here because it would
        // invoke a live-disk default detector/slicer before metadata is
        // attached.
        result = _harvest(internalParams);
      } else if (op == 'slice') {
        final candidateId = internalParams['candidateId'];
        if (candidateId is! String || candidateId.isEmpty) {
          return _rpcError(id, 'E_BAD_REQUEST',
              'params.candidateId (non-empty string) is required');
        }
        final entrySymbolPath = internalParams['entrySymbolPath'];
        if (entrySymbolPath is! String || entrySymbolPath.isEmpty) {
          return _rpcError(id, 'E_BAD_REQUEST',
              'params.entrySymbolPath (non-empty string) is required');
        }
        result = _slice({
          ...internalParams,
          'repoRoot': internalParams['repoRoot'],
          'candidateId': candidateId,
          'entrySymbolPath': entrySymbolPath,
          'opts': internalParams['opts'] is Map
              ? (internalParams['opts'] as Map).cast<String, Object?>()
              : <String, Object?>{},
          'contentOverlay': snapshotOverlay,
        });
      } else {
        return _rpcError(id, 'E_BAD_REQUEST', 'unknown method: $method');
      }
      final explicitPaths = <String>[];
      if (rawParams['entrySymbolPath'] is String) {
        explicitPaths
            .add((rawParams['entrySymbolPath'] as String).split('#').first);
      }
      final metadata = _analysisMetadata(
          {...internalParams, ...params}, op, explicitPaths, tracker);
      return {
        'jsonrpc': jsonRpcVersion,
        'id': id,
        'result': _analyzerResultV2(id, op, params, result, metadata),
      };
    } on ArgumentError catch (e) {
      return _rpcError(id, 'E_BAD_REQUEST', '${e.message ?? e}');
    } catch (e) {
      return _rpcError(id, 'E_ADAPTER_INTERNAL', '$e');
    }
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

  static Map<String, Object?> _defaultHarvest(Map<Object?, Object?> params) {
    final tracker = params['_analysisTracker'];
    return harvestCandidates(
      params,
      tracker: tracker is AnalysisObservationTracker ? tracker : null,
    );
  }

  static Map<String, Object?> _defaultSlice(Map<Object?, Object?> params) {
    final tracker = params['_analysisTracker'];
    return sliceCandidate(
      repoRoot: params['repoRoot'] as String,
      candidateId: params['candidateId'] as String,
      entrySymbolPath: params['entrySymbolPath'] as String,
      opts: (params['opts'] as Map?)?.cast<String, Object?>() ?? const {},
      contentOverlay: _overlayFromParams(params),
      tracker: tracker is AnalysisObservationTracker ? tracker : null,
    );
  }

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
      'err': {
        'code': code,
        'message': redactDiagnostic(message),
        'retryable': retryable
      },
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
        'message': redactDiagnostic(message),
        'data': {
          'code': code,
          'retryable': retryable,
          'detail': redactDiagnostic(message),
        },
      },
    };
  }

  static String? _validateAnalyzerRequestV2(
      String id, String operation, Map<Object?, Object?> params) {
    if (params['schemaId'] != analyzerRequestSchemaId ||
        params['schemaVersion'] != 2) {
      return 'analysis request must use rflsc.analyzer-request.v2';
    }
    if (params['requestId'] != id) {
      return 'analysis requestId must match JSON-RPC id';
    }
    if (params['operation'] != operation) {
      return 'analysis operation does not match JSON-RPC method';
    }
    final snapshot = params['snapshot'];
    if (snapshot is! Map) return 'analysis snapshot is required';
    for (final field in const [
      'snapshotId',
      'workspaceEpoch',
      'computedBasisId',
      'rootTreeId',
      'dependencyFingerprint',
      'documents',
      'files',
      'repositoryPathWriteAudit'
    ]) {
      if (!snapshot.containsKey(field)) {
        return 'analysis snapshot is missing $field';
      }
    }
    return null;
  }

  static Map<String, Object?> _analyzerResultV2(
      String id,
      String operation,
      Map<Object?, Object?> params,
      Map<String, Object?> payload,
      Map<String, Object?> metadata) {
    final snapshot = params['snapshot'] is Map
        ? (params['snapshot'] as Map).cast<Object?, Object?>()
        : const <Object?, Object?>{};
    final oldRead = metadata['analysisReadSet'] is Map
        ? (metadata['analysisReadSet'] as Map).cast<Object?, Object?>()
        : const <Object?, Object?>{};
    final oldClosure = metadata['causalObservationClosure'] is Map
        ? (metadata['causalObservationClosure'] as Map).cast<Object?, Object?>()
        : const <Object?, Object?>{};
    final oldCapability = metadata['capabilityProfile'] is Map
        ? (metadata['capabilityProfile'] as Map).cast<Object?, Object?>()
        : const <Object?, Object?>{};
    final coverage = oldClosure['coverageBoundary'] is Map
        ? (oldClosure['coverageBoundary'] as Map).cast<String, Object?>()
        : oldCapability['coverageBoundary'] is Map
            ? (oldCapability['coverageBoundary'] as Map).cast<String, Object?>()
            : <String, Object?>{
                'includedSourceRoots': ['.'],
                'measured': true,
              };
    List<Object?> listValue(Object? value) =>
        value is List ? value.toList() : <Object?>[];
    final readSetId = oldRead['readSetId'] is String
        ? oldRead['readSetId'] as String
        : 'readset-$id';
    final readSet = <String, Object?>{
      'schemaId': _readSetV2SchemaId,
      'schemaVersion': 2,
      'readSetId': readSetId,
      'computedBasisId': snapshot['computedBasisId'],
      'workspaceEpoch': snapshot['workspaceEpoch'],
      'documents': listValue(oldRead['documents']),
      'negativeObservations': listValue(oldRead['negativeObservations']),
      'membershipObservations': listValue(oldRead['membershipObservations']),
      'dependencyFrontiers': listValue(oldRead['dependencyFrontiers']),
    };
    final closure = <String, Object?>{
      'schemaId': _closureV2SchemaId,
      'schemaVersion': 2,
      'closureId': oldClosure['closureId'] is String
          ? oldClosure['closureId']
          : 'closure-$id',
      'analysisReadSetId': readSetId,
      'computedBasisId': snapshot['computedBasisId'],
      'workspaceEpoch': snapshot['workspaceEpoch'],
      'closureStatus':
          oldClosure['closureStatus'] == 'open' ? 'open' : 'closed',
      'negativeObservations': readSet['negativeObservations'],
      'membershipObservations': readSet['membershipObservations'],
      'dependencyFrontiers': readSet['dependencyFrontiers'],
      'requiredObservations': listValue(oldClosure['requiredObservations']),
      'measuredObservations': listValue(oldClosure['measuredObservations']),
      'incompleteReasons': listValue(oldClosure['incompleteReasons']),
    };
    final features = oldCapability['features'] is List &&
            (oldCapability['features'] as List).isNotEmpty
        ? listValue(oldCapability['features'])
        : <Object?>['snapshot_bytes'];
    return {
      'schemaId': analyzerResultSchemaId,
      'schemaVersion': 2,
      'requestId': id,
      'operation': operation,
      'adapterVersion': adapterVersion,
      'analyzerRevision': analyzerVersion,
      'workspaceEpoch': snapshot['workspaceEpoch'],
      'computedBasisId': snapshot['computedBasisId'],
      'snapshotId': snapshot['snapshotId'],
      'snapshotTreeDigest': snapshot['rootTreeId'],
      'dependencyFingerprint': snapshot['dependencyFingerprint'],
      'analysisReadSet': readSet,
      'causalObservationClosure': closure,
      'capabilityProfile': {
        'adapter': 'dart',
        'adapterVersion': adapterVersion,
        'analyzerRevision': analyzerVersion,
        'features': features,
        'unsupported': listValue(oldCapability['unsupported']),
      },
      'coverage': coverage,
      'diagnostics': listValue(metadata['diagnostics']),
      'payload': payload,
    };
  }

  static Map<String, Object?> _analysisMetadata(Map<Object?, Object?> params,
      String operation, List<String> explicitPaths,
      [AnalysisObservationTracker? tracker]) {
    final active = tracker ?? AnalysisObservationTracker(params, operation);
    if (tracker == null) {
      for (final path in explicitPaths) {
        active.read(path);
      }
    }
    return active.metadata();
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
