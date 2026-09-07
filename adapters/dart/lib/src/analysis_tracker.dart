// Request-scoped source observations for the Dart analyzer.
//
// Production JSON-RPC analysis is supplied an immutable snapshot overlay. The
// tracker is deliberately the only source abstraction used by those paths so
// that read-set evidence describes what the analyzer actually looked up.
library;

import 'dart:convert';
import 'dart:io';

import 'secret.dart';
import 'sha256.dart';

const String trackerAnalysisSchemaId =
    'https://codeflow.local/schemas/adapter-analysis.schema.json';
const String trackerReadSetSchemaId =
    'https://codeflow.local/schemas/analysis-read-set.schema.json';
const String trackerClosureSchemaId =
    'https://codeflow.local/schemas/causal-observation-closure.schema.json';
const String trackerAdapterVersion = '0.1.0';
const String trackerAnalyzerVersion = 'dart-structural/0.1.0';
const int trackerProtocolVersion = 1;

/// Tracks only successful reads, failed lookups, and enumerations performed by
/// one analyzer request. It is safe for direct package tests without a
/// snapshot, but production callers always provide [overlay].
class AnalysisObservationTracker {
  AnalysisObservationTracker(Map<Object?, Object?> params, this.operation)
      : params = params,
        overlay = _overlayFromParams(params),
        repoRoot =
            params['repoRoot'] is String ? params['repoRoot'] as String : null {
    final snapshot = _snapshot(params);
    final rawDocuments = snapshot['documents'];
    if (rawDocuments is List) {
      for (final raw in rawDocuments) {
        if (raw is! Map || raw['path'] is! String) continue;
        final path = _normalize(raw['path'] as String);
        if (path == null) continue;
        final identity = <String, Object?>{};
        for (final field in const [
          'documentRevisionId',
          'contentId',
          'documentVersion'
        ]) {
          final value = raw[field];
          if ((field == 'documentVersion' && value is int) ||
              (field != 'documentVersion' &&
                  value is String &&
                  value.isNotEmpty)) {
            identity[field] = value;
          }
        }
        identities[path] = identity;
      }
    }
  }

  final Map<Object?, Object?> params;
  final String operation;
  final Map<String, String>? overlay;
  final String? repoRoot;
  final Map<String, Map<String, Object?>> identities = {};
  final Map<String, String> _documents = {};
  final Map<String, Map<String, Object?>> _missing = {};
  final Map<String, Map<String, Object?>> _membership = {};
  final Map<String, Map<String, Object?>> _frontiers = {};
  final Set<String> _coverageRoots = {};

  /// Returns immutable-snapshot content for [path], recording a document only
  /// after the lookup succeeds. A failed lookup records its exact selector and
  /// snapshot scope as a negative observation.
  String? read(String path) {
    final normalized = _normalize(path);
    if (normalized == null) return null;
    String? content;
    if (overlay != null) {
      if (!overlay!.containsKey(normalized)) {
        recordMissing(normalized);
        return null;
      }
      content = overlay![normalized];
    } else if (repoRoot != null && repoRoot!.isNotEmpty) {
      try {
        content = File('$repoRoot/$normalized').readAsStringSync();
      } catch (_) {
        recordMissing(normalized);
        return null;
      }
    } else {
      recordMissing(normalized);
      return null;
    }
    if (content == null) {
      recordMissing(normalized);
      return null;
    }
    _documents[normalized] = content;
    _missing.remove(normalized);
    _coverageRoots.add(_sourceRoot(normalized));
    return content;
  }

  /// Records an actual failed lookup. Repeated misses are coalesced by path.
  void recordMissing(String path, {String? selector}) {
    final normalized = _normalize(path);
    if (normalized == null || _documents.containsKey(normalized)) return;
    final snapshotId = _snapshot(params)['snapshotId'];
    final scope = snapshotId is String && snapshotId.isNotEmpty
        ? 'immutable snapshot $snapshotId'
        : 'captured source scope';
    _missing[normalized] = {
      'kind': 'negative_lookup',
      'path': normalized,
      'valueHash': sha256Hex('$normalized:absent'),
      'detail':
          '${selector ?? normalized} absent from $scope during $operation',
      'measured': true,
    };
  }

  /// Enumerates the exact Dart source set consumed by harvest/detect.
  List<String> enumerateDartSourceFiles({String libSubdir = 'lib'}) {
    final normalizedSubdir = _normalize(libSubdir) ?? 'lib';
    final prefix = normalizedSubdir == '.' || normalizedSubdir.isEmpty
        ? ''
        : '$normalizedSubdir/';
    List<String> files;
    if (overlay != null) {
      files = overlay!.keys
          .where((path) => path.endsWith('.dart'))
          .where((path) => prefix.isEmpty || path.startsWith(prefix))
          .where((path) => !_isGenerated(path))
          .toList()
        ..sort();
    } else if (repoRoot != null) {
      files = [];
      final root = Directory('$repoRoot/$normalizedSubdir');
      if (root.existsSync()) {
        void walk(Directory dir) {
          List<FileSystemEntity> entities;
          try {
            entities = dir.listSync(followLinks: false)
              ..sort((a, b) => a.path.compareTo(b.path));
          } catch (_) {
            return;
          }
          for (final entity in entities) {
            if (entity is Directory) {
              walk(entity);
            } else if (entity is File) {
              final relative = _relativeToRoot(entity.path);
              if (relative != null &&
                  relative.endsWith('.dart') &&
                  !_isGenerated(relative)) {
                files.add(relative);
              }
            }
          }
        }

        walk(root);
        files.sort();
      }
    } else {
      files = [];
    }
    recordMembership('.', files);
    return files;
  }

  /// Records membership of the exact enumeration used by the operation.
  void recordMembership(String scope, Iterable<String> files) {
    final normalized =
        files.map(_normalize).whereType<String>().toSet().toList()..sort();
    _membership[scope] = {
      'kind': 'source_membership',
      'path': scope,
      'valueHash': sha256Hex(normalized.join('\n')),
      'detail':
          'membership measured from the source enumeration used by $operation',
      'measured': true,
    };
    _coverageRoots.add(scope);
  }

  /// Records a configuration/dependency frontier only after its document was
  /// actually read and parsed by the analyzer.
  bool recordDependency(String path) {
    final normalized = _normalize(path);
    if (normalized == null) return false;
    final content = _documents[normalized];
    if (content == null) return false;
    _frontiers[normalized] = {
      'kind': 'dependency_frontier',
      'path': normalized,
      'valueHash': sha256Hex(content),
      'detail':
          'dependency/configuration frontier established by analyzer traversal',
      'measured': true,
    };
    return true;
  }

  /// Builds the legacy metadata shape consumed by the canonical v2 envelope.
  /// Every list is derived from this tracker's actual observations.
  Map<String, Object?> metadata({List<Object?> diagnostics = const []}) {
    final snapshot = _snapshot(params);
    final documents = _documents.entries.map((entry) {
      final identity = identities[entry.key] ?? const <String, Object?>{};
      final content = entry.value;
      return <String, Object?>{
        'path': entry.key,
        ...identity,
        'contentHash': sha256Hex(content),
        'byteLength': utf8.encode(content).length,
      };
    }).toList()
      ..sort((a, b) => (a['path']! as String).compareTo(b['path']! as String));
    var basis = params['computedBasisId'] is String
        ? params['computedBasisId'] as String
        : snapshot['computedBasisId'] is String
            ? snapshot['computedBasisId'] as String
            : '';
    if (basis.isEmpty) {
      basis = sha256Hex(documents
          .map((doc) => '${doc['path']}:${doc['contentHash']}\n')
          .join());
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
    final required = params['requiredObservations'] is List
        ? (params['requiredObservations'] as List)
            .whereType<String>()
            .where((value) => value.isNotEmpty)
            .toList()
        : <String>[];
    final negatives = _sortedObservations(_missing.values);
    final memberships = _sortedObservations(_membership.values);
    final frontiers = _sortedObservations(_frontiers.values);
    final measured = <String>[];
    if (negatives.isNotEmpty) measured.add('negative_lookup');
    if (memberships.isNotEmpty) measured.add('membership');
    if (frontiers.isNotEmpty) measured.add('dependency_frontier');
    final unsupported = ['runtime_observation', 'dynamic_resolution'];
    final incompleteReasons = <String>[];
    for (final kind in required) {
      if (measured.contains(kind)) continue;
      final reason = unsupported.contains(kind)
          ? '$kind is unsupported and was not measured'
          : '$kind is not measured for $operation';
      if (!incompleteReasons.contains(reason)) incompleteReasons.add(reason);
    }
    final profile = <String, Object?>{
      'adapter': 'dart',
      'adapterVersion': trackerAdapterVersion,
      'analyzerRevision': trackerAnalyzerVersion,
      'features': [
        'symbols',
        'calls',
        'snapshot_overlay',
        'negative_lookup',
        'membership',
        'dependency_frontier'
      ],
      'protocolVersions': [trackerProtocolVersion],
      'unsupported': unsupported,
      'coverageBoundary': {
        'includedSourceRoots':
            (_coverageRoots.isEmpty ? {'.'} : _coverageRoots).toList()..sort(),
        'excludedReasons': const <String>[],
        'measured': true,
      },
    };
    final boundedDiagnostics =
        diagnostics.take(64).map((item) => _diagnostic(item)).toList();
    final readSet = <String, Object?>{
      'schemaId': trackerReadSetSchemaId,
      'schemaVersion': 1,
      'readSetId': readSetId,
      'computedBasisId': basis,
      'workspaceEpoch': epoch,
      'documents': documents,
      'indexes': const <Object?>[],
      'negativeObservations': negatives,
      'membershipObservations': memberships,
      'dependencyFrontiers': frontiers,
      'requiredObservations': required,
      'adapterVersions': {'dart': trackerAdapterVersion},
    };
    final closure = <String, Object?>{
      'schemaId': trackerClosureSchemaId,
      'schemaVersion': 1,
      'closureId': closureId,
      'analysisReadSetId': readSetId,
      'computedBasisId': basis,
      'workspaceEpoch': epoch,
      'closureStatus': incompleteReasons.isEmpty ? 'closed' : 'open',
      'negativeObservations': negatives,
      'membershipObservations': memberships,
      'dependencyFrontiers': frontiers,
      'requiredObservations': required,
      'measuredObservations': measured,
      'capabilityProfile': profile,
      'coverageBoundary': profile['coverageBoundary'],
      'incompleteReasons': incompleteReasons,
      'closureDigest': sha256Hex(
          jsonEncode({'analysisReadSet': readSet, 'profile': profile})),
    };
    final snapshotIdentity = <String, Object?>{};
    for (final field in const [
      'snapshotId',
      'rootTreeId',
      'dependencyFingerprint',
      'configurationFingerprint'
    ]) {
      final value = snapshot[field];
      if (value is String && value.isNotEmpty) snapshotIdentity[field] = value;
    }
    return {
      'schemaId': trackerAnalysisSchemaId,
      'schemaVersion': 1,
      'operation': operation,
      'computedBasisId': basis,
      'workspaceEpoch': epoch,
      'analysisReadSet': readSet,
      'causalObservationClosure': closure,
      'capabilityProfile': profile,
      'analyzerVersion': trackerAnalyzerVersion,
      'diagnostics': boundedDiagnostics,
      ...snapshotIdentity,
    };
  }

  String? _relativeToRoot(String absolutePath) {
    if (repoRoot == null) return null;
    final root = _posix(repoRoot!);
    final path = _posix(absolutePath);
    final prefix = root.endsWith('/') ? root : '$root/';
    if (!path.startsWith(prefix)) return null;
    return _normalize(path.substring(prefix.length));
  }

  static String _sourceRoot(String path) {
    final slash = path.indexOf('/');
    return slash < 0 ? '.' : path.substring(0, slash);
  }

  static List<Map<String, Object?>> _sortedObservations(
      Iterable<Map<String, Object?>> observations) {
    final result = observations.toList();
    result.sort((a, b) {
      final ap = a['path'] is String ? a['path'] as String : '';
      final bp = b['path'] is String ? b['path'] as String : '';
      return ap.compareTo(bp);
    });
    return result;
  }

  static Map<String, Object?> _diagnostic(Object? value) {
    if (value is Map) {
      final severity = value['severity'] is String &&
              const ['info', 'warning', 'error'].contains(value['severity'])
          ? value['severity'] as String
          : 'warning';
      final message = redactDiagnostic(
          value['message']?.toString() ?? 'adapter diagnostic');
      final result = <String, Object?>{
        'severity': severity,
        'message': message.length > 512 ? message.substring(0, 512) : message,
      };
      final path = value['path']?.toString();
      if (path != null && path.isNotEmpty) {
        result['path'] = redactDiagnostic(path, maxBytes: 256);
      }
      return result;
    }
    final message = redactDiagnostic(value?.toString() ?? 'adapter diagnostic');
    return {
      'severity': 'warning',
      'message': message.length > 512 ? message.substring(0, 512) : message,
    };
  }

  static Map<Object?, Object?> _snapshot(Map<Object?, Object?> params) {
    final value = params['snapshot'];
    return value is Map ? value.cast<Object?, Object?>() : const {};
  }

  static Map<String, String>? _overlayFromParams(Map<Object?, Object?> params) {
    final snapshot = _snapshot(params);
    final raw = params['contentOverlay'] ??
        snapshot['contentOverlay'] ??
        snapshot['files'];
    if (raw is! Map) return null;
    final result = <String, String>{};
    for (final entry in raw.entries) {
      if (entry.key is! String) continue;
      final path = _normalize(entry.key as String);
      if (path == null) continue;
      final value = entry.value;
      if (value is String) {
        result[path] = value;
      } else if (value is Map && value['content'] is String) {
        result[path] = value['content'] as String;
      }
    }
    return result;
  }

  static String? _normalize(String value) {
    final normalized = _posix(value).replaceFirst(RegExp(r'^\./'), '');
    if (normalized.isEmpty || normalized.startsWith('/')) return null;
    final segments = normalized.split('/');
    if (segments.any((segment) => segment == '..' || segment.isEmpty))
      return null;
    return normalized;
  }

  static String _posix(String value) => value.replaceAll('\\', '/');

  static bool _isGenerated(String path) =>
      path.endsWith('.g.dart') ||
      path.endsWith('.freezed.dart') ||
      path.endsWith('.gr.dart') ||
      path.endsWith('.gql.dart');
}
