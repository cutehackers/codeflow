// Stage 1 Domain Harvest (design §4.2): marker-based flow-candidate
// discovery over a Dart package directory tree.
//
// Deterministic by construction: files are walked in sorted order, every
// candidate map is built with a fixed key order, and the final list is
// sorted by entrySymbolPath. Identical input trees therefore yield
// byte-identical output lines.
library;

import 'dart:io';

import 'humanize.dart';
import 'profile.dart';
import 'scanner.dart';
import 'sha256.dart';

/// Adapter version reported by the ping op.
const String adapterVersion = '0.1.0';

/// Trigger classes (design §4.2, closed enum in candidate.schema.json).
const String triggerUserAction = 'user_action';
const String triggerUseCaseInvocation = 'use_case_invocation';
const String triggerSystemEvent = 'system_event';
const String triggerStateTransition = 'state_transition';

/// Concrete marker kinds matched by the profile pack.
const String markerNotifierMethod = 'notifier_method';
const String markerBlocHandler = 'bloc_handler';
const String markerRouteCallback = 'route_callback';
const String markerUsecaseCall = 'usecase_call';
const String markerLifecycleCallback = 'lifecycle_callback';
const String markerStateMutation = 'state_mutation';

/// Placeholder score; CORE recomputes (marker specificity x fan-in x
/// boundary reachability).
const double placeholderScore = 0.5;

final _fcmHandlerName = RegExp(
  r'^(firebase.*background.*handler|onBackgroundMessage)$',
  caseSensitive: false,
);

final _blocHandlerName = RegExp(r'^_?on[A-Z]');

final _uiCallbackName =
    RegExp(r'^on([A-Z]\w*)?(Pressed|Tapped|Submitted|Changed|Selected)$');

final _deepLinkEvidence = RegExp(r'\bdeep_?[Ll]ink\b');

final _routerParamEvidence = RegExp(r'\b(queryParameters|pathParameters)\b');

final _navigationEvidence = RegExp(
  r'\bcontext\s*\.\s*'
  r'(go|push|pushReplacement|pushNamed|goNamed|replace)\s*\('
  r'|\bNavigator\s*\.\s*of\s*\(\s*context'
  r'|\b\w*[Rr]outer\w*\s*\.\s*(go|push|replace|goNamed|pushNamed)\s*\(',
);

/// Methods that call these count as standalone state transitions.
final _notifyStyleCall = RegExp(r'\b(notifyListeners|update|emit)\s*\(');

/// UI plumbing names never become user-action entries.
const _excludedMethodNames = {
  'build',
  'new',
  'toString',
  'dispose',
  'initState',
};

// --- stdlib-only POSIX-style path helpers (no package:path) -----------------

String _toPosix(String path) => path.replaceAll('\\', '/');

String _join(String base, String rel) =>
    base.isEmpty ? rel : (base.endsWith('/') ? '$base$rel' : '$base/$rel');

String _basename(String path) {
  final i = path.lastIndexOf('/');
  return i < 0 ? path : path.substring(i + 1);
}

String _dirname(String path) {
  final i = path.lastIndexOf('/');
  if (i < 0) return '';
  if (i == 0) return '/';
  return path.substring(0, i);
}

String _stem(String posixPath) {
  final base = _basename(posixPath);
  final dot = base.lastIndexOf('.');
  return dot <= 0 ? base : base.substring(0, dot);
}

// ----------------------------------------------------------------------------

class _Marker {
  const _Marker(this.triggerClass, this.markerKind);

  final String triggerClass;
  final String markerKind;
}

/// Result payload of the detect op: {"language": "dart", "confident": bool}.
Map<String, Object?> detectRepo({required String repoRoot}) {
  final root = _toPosix(repoRoot);
  final pubspec = File(_join(root, 'pubspec.yaml'));
  if (!pubspec.existsSync()) {
    return {'language': 'dart', 'confident': false};
  }
  var content = '';
  try {
    content = pubspec.readAsStringSync();
  } catch (_) {
    return {'language': 'dart', 'confident': false};
  }
  var confident =
      RegExp(r'^\s*sdk\s*:', multiLine: true).hasMatch(content) ||
          RegExp(r'^\s*flutter\s*:', multiLine: true).hasMatch(content);
  // A bare directory without any Dart source under lib/ is not confidently
  // harvestable either way.
  var hasDartSource = false;
  final libDir = Directory(_join(root, 'lib'));
  if (libDir.existsSync()) {
    for (final entity in libDir.listSync(recursive: true)) {
      if (entity is File && _toPosix(entity.path).endsWith('.dart')) {
        hasDartSource = true;
        break;
      }
    }
  }
  return {'language': 'dart', 'confident': confident || hasDartSource};
}

/// Extracts the `name:` field from pubspec.yaml at [repoRoot], or null.
String? readPackageName(String repoRoot) {
  final file = File(_join(_toPosix(repoRoot), 'pubspec.yaml'));
  if (!file.existsSync()) return null;
  try {
    for (final line in file.readAsStringSync().split('\n')) {
      final m =
          RegExp(r'^name:\s*([^\s#]+)').firstMatch(line.trimLeft());
      if (m != null) return m.group(1);
    }
  } catch (_) {
    return null;
  }
  return null;
}

bool _isGenerated(String posixRelPath) =>
    posixRelPath.endsWith('.g.dart') ||
    posixRelPath.endsWith('.freezed.dart') ||
    posixRelPath.endsWith('.gr.dart') ||
    posixRelPath.endsWith('.gql.dart');

List<String> _collectDartFiles(String libDirPosix) {
  final results = <String>[];

  void walk(String absDirPosix, String relPrefix) {
    final dir = Directory(absDirPosix);
    final entities = dir.listSync(followLinks: false)
      ..sort((a, b) => a.path.compareTo(b.path));
    for (final entity in entities) {
      final abs = _toPosix(entity.path);
      final base = _basename(abs);
      if (base.startsWith('.')) continue;
      final rel = relPrefix.isEmpty ? base : '$relPrefix/$base';
      if (entity is Directory) {
        walk(abs, rel);
      } else if (entity is File && base.endsWith('.dart')) {
        if (!_isGenerated(rel)) results.add(rel);
      }
    }
  }

  walk(libDirPosix, '');
  results.sort();
  return results;
}

/// Resolves the owning Dart package name per file: nearest pubspec.yaml
/// walking up toward [repoRoot]; falls back to "unknown" (schema requires
/// intentSignals.packageName to be non-empty).
class _PackageNameResolver {
  _PackageNameResolver(this.repoRootPosix);

  final String repoRootPosix;
  final Map<String, String> _cache = {};

  String packageNameFor(String absoluteFileDirPosix) {
    var dir = absoluteFileDirPosix;
    while (true) {
      final cached = _cache[dir];
      if (cached != null) return cached;
      final name = readPackageName(dir);
      if (name != null) {
        _cache[dir] = name;
        return name;
      }
      if (dir == repoRootPosix || !dir.startsWith(repoRootPosix)) break;
      final parent = _dirname(dir);
      if (parent.isEmpty || parent.length < repoRootPosix.length) break;
      dir = parent;
    }
    return 'unknown';
  }
}

/// Harvest op result: {"candidates": [...]} conforming to candidate.schema.json.
///
/// [params] mirrors wire request params: `repoRoot` (required),
/// `libSubdir` (default "lib"), `profiles` (optional array; see profile.dart).
Map<String, Object?> harvestCandidates(Map<Object?, Object?> params) {
  final repoRootRaw = params['repoRoot'];
  if (repoRootRaw is! String || repoRootRaw.isEmpty) {
    throw ArgumentError('harvest_candidates requires params.repoRoot');
  }
  final repoRoot = _stripTrailingSlash(_toPosix(repoRootRaw));
  final libSubdirRaw = params['libSubdir'] is String
      ? params['libSubdir'] as String
      : 'lib';
  final libSubdir = _stripTrailingSlash(_toPosix(libSubdirRaw));
  final profile = resolveProfiles(params['profiles']);

  final libDirPosix = _join(repoRoot, libSubdir);
  if (!Directory(libDirPosix).existsSync()) {
    return {'candidates': const <Object?>[]};
  }

  final resolver = _PackageNameResolver(repoRoot);
  final candidates = <Map<String, Object?>>[];
  final relFiles = _collectDartFiles(libDirPosix);
  // Candidate paths are REPO-relative ("<repoRelPath>#symbol"); files are
  // walked relative to libSubdir, so re-base them here.
  final subdirPrefix =
      libSubdir == '.' || libSubdir == '' ? '' : '${_toPosix(libSubdir)}/';

  for (final rel in relFiles) {
    final absFile = _join(libDirPosix, rel);
    ScanResult scanned;
    try {
      scanned = scanSource(File(absFile).readAsStringSync());
    } catch (_) {
      continue; // unreadable file: skip deterministically, never crash
    }
    final posixRel = '$subdirPrefix$rel';
    final fileStem = _stem(rel);
    final packageName = resolver.packageNameFor(_dirname(absFile));

    for (final cls in scanned.classes) {
      for (final method in cls.methods) {
        if (_isConstructor(method.name, cls.name)) continue;
        final body = scanned.maskedBody(method);
        final docLine = scanned.firstDocLineAbove(method.nameLine);
        final marker = _classifyMethod(
          className: cls.name,
          methodName: method.name,
          bodyText: body,
          profile: profile,
        );
        if (marker == null) continue;
        candidates.add(_emit(
          canonicalPath: '$posixRel#${cls.name}.${method.name}',
          triggerClass: marker.triggerClass,
          markerKind: marker.markerKind,
          className: cls.name,
          derivedName: humanizeIdentifier(method.name),
          docLine: docLine,
          packageName: packageName,
          rootEquivalenceKey: cls.name,
        ));
      }
    }

    for (final fn in scanned.topLevelFunctions) {
      final body = scanned.maskedBody(fn);
      final docLine = scanned.firstDocLineAbove(fn.nameLine);
      final marker = _classifyTopLevelFunction(
        functionName: fn.name,
        bodyText: body,
      );
      if (marker == null) continue;
      candidates.add(_emit(
        canonicalPath: '$posixRel#${fn.name}',
        triggerClass: marker.triggerClass,
        markerKind: marker.markerKind,
        className: fileStem,
        derivedName: humanizeIdentifier(fn.name),
        docLine: docLine,
        packageName: packageName,
        rootEquivalenceKey: fileStem,
      ));
    }
  }

  candidates.sort((a, b) => (a['entrySymbolPath']! as String)
      .compareTo(b['entrySymbolPath']! as String));
  return {'candidates': candidates};
}

String _stripTrailingSlash(String s) =>
    s.length > 1 && s.endsWith('/') ? s.substring(0, s.length - 1) : s;

bool _isConstructor(String methodName, String className) =>
    methodName == className || methodName == 'new';

/// Marker decision tree for class members. Order encodes attribution
/// priority; each symbol yields at most ONE candidate (candidateId derives
/// from the symbol path alone - duplicates would collide).
_Marker? _classifyMethod({
  required String className,
  required String methodName,
  required String bodyText,
  required FrameworkProfile profile,
}) {
  final lowerClass = className.toLowerCase();
  final endsNotifier = lowerClass.endsWith('notifier');
  final endsCubit = lowerClass.endsWith('cubit');
  final endsBloc = lowerClass.endsWith('bloc');
  final endsUseCase =
      lowerClass.endsWith('usecase') || lowerClass.endsWith('use_case');
  final endsService = lowerClass.endsWith('service');
  final isPublic = !methodName.startsWith('_');

  // 1. Flutter lifecycle observer override -> system event.
  if (methodName == 'didChangeAppLifecycleState') {
    return const _Marker(triggerSystemEvent, markerLifecycleCallback);
  }

  // 2. Standalone state transition: mutation evidence AND notify-style call,
  //    inside Notifier/Cubit-scoped classes. Primary mutations stay
  //    attributed to their user_action owner (design §4.2 R11); only these
  //    standalone ones become candidates of their own.
  final mutationEvidence =
      profile.stateMutationRegexes.any((re) => re.hasMatch(bodyText));
  if (mutationEvidence &&
      _notifyStyleCall.hasMatch(bodyText) &&
      (endsNotifier || endsCubit)) {
    return const _Marker(triggerStateTransition, markerStateMutation);
  }

  // 3. UseCase / Service public methods -> use case invocation. Boundary-
  //    suffixed classes (Repository/ApiClient...) are never flow roots.
  if ((endsUseCase || endsService) &&
      isPublic &&
      !profile.matchesBoundaryClass(className)) {
    return const _Marker(triggerUseCaseInvocation, markerUsecaseCall);
  }

  // 4. Bloc event handlers (`void _on<Event>(...)` registration shape).
  if ((endsBloc || endsCubit) && _blocHandlerName.hasMatch(methodName)) {
    return const _Marker(triggerUserAction, markerBlocHandler);
  }

  // 5. Notifier action methods (anything except build / plumbing names).
  if (endsNotifier && !_excludedMethodNames.contains(methodName)) {
    return const _Marker(triggerUserAction, markerNotifierMethod);
  }

  // 6. Deep-link handler heuristic: explicit deepLink token, or router-state
  //    params combined with an actual navigation call -> system event.
  final navigates = _navigationEvidence.hasMatch(bodyText);
  if (_deepLinkEvidence.hasMatch(bodyText) ||
      (_routerParamEvidence.hasMatch(bodyText) && navigates)) {
    return const _Marker(triggerSystemEvent, markerRouteCallback);
  }

  // 7. Route callback heuristic: method whose body performs navigation.
  if (navigates) {
    return const _Marker(triggerUserAction, markerRouteCallback);
  }

  // 8. UI callback naming convention even without visible navigation.
  if (_uiCallbackName.hasMatch(methodName)) {
    return const _Marker(triggerUserAction, markerRouteCallback);
  }

  // 9. Profile-provided domain markers extend the built-in class sets.
  //    Fallback position: they must never mask stronger structural
  //    evidence above (lifecycle / bloc / navigation shapes).
  final qualified = '$className.$methodName';
  if (profile.domainMarkerRegexes
      .any((re) => re.hasMatch(className) || re.hasMatch(qualified))) {
    return const _Marker(triggerUserAction, markerNotifierMethod);
  }

  return null;
}

/// Marker decision tree for top-level functions.
_Marker? _classifyTopLevelFunction({
  required String functionName,
  required String bodyText,
}) {
  if (_fcmHandlerName.hasMatch(functionName)) {
    return const _Marker(triggerSystemEvent, markerLifecycleCallback);
  }
  final navigates = _navigationEvidence.hasMatch(bodyText);
  if (_deepLinkEvidence.hasMatch(bodyText) ||
      (_routerParamEvidence.hasMatch(bodyText) && navigates)) {
    return const _Marker(triggerSystemEvent, markerRouteCallback);
  }
  return null;
}

/// Builds one candidate object with a FIXED key order so identical inputs
/// produce byte-identical JSON lines.
Map<String, Object?> _emit({
  required String canonicalPath,
  required String triggerClass,
  required String markerKind,
  required String className,
  required String derivedName,
  required String? docLine,
  required String packageName,
  required String rootEquivalenceKey,
}) {
  return {
    'candidateId': candidateIdFor(canonicalPath),
    'triggerClass': triggerClass,
    'markerKind': markerKind,
    'entrySymbolPath': canonicalPath,
    'intentSignals': {
      'className': className,
      'derivedName': derivedName,
      'docLine': docLine,
      'packageName': packageName,
    },
    'score': placeholderScore,
    'fanIn': 0,
    'boundaryReachable': false,
    'rootEquivalenceKey': rootEquivalenceKey,
    'dedupedInto': null,
    'tieBreakRank': 0,
    'manifestOverride': 'none',
  };
}
