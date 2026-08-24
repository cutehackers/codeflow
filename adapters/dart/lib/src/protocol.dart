// CORE <-> adapter wire protocol (schemas/adapter-protocol.schema.json).
//
// NDJSON over stdio: one JSON request object per stdin line, one JSON
// response object per stdout line. Every response echoes the request `id`.
//
// Documented deviation: a MALFORMED line (invalid JSON / non-object) cannot
// carry a correlation id, but the error envelope schema requires a string id;
// we emit id:"" for those and CORE drops them (unknown-id rule).
library;

import 'dart:convert';

import 'harvest.dart';
import 'slice.dart';

/// Protocol major version this adapter speaks.
const int protocolVersion = 1;

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
      return _error('', 'E_BAD_REQUEST',
          'request line is not valid JSON: ${e.message}');
    } catch (e) {
      return _error('', 'E_BAD_REQUEST', 'request line is not valid JSON');
    }
    if (decoded is! Map) {
      return _error(
          '', 'E_BAD_REQUEST', 'request must be a JSON object');
    }

    final v = decoded['v'];
    final id =
        decoded['id'] is String && (decoded['id'] as String).isNotEmpty
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
          throw ArgumentError('params.candidateId (non-empty string) is required');
        }
        final entrySymbolPath = params['entrySymbolPath'];
        if (entrySymbolPath is! String || entrySymbolPath.isEmpty) {
          throw ArgumentError('params.entrySymbolPath (non-empty string) is required');
        }
        final opts = params['opts'] is Map ? params['opts'] as Map<String, Object?> : <String, Object?>{};
        return _ok(id, _slice({
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
}
