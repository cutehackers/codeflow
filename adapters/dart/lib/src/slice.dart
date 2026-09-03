// Stage 2 Structural Slice (design §4.2, schemas/sliced-payload.schema.json).
//
// Extracts guard, mutation, effect/call and branch steps from AST statement
// slicing, follows import-resolved direct references across files up to depth 5,
// terminates at boundary markers (Repository/ApiClient/Service), records unknown
// edges for unresolved/dynamic calls, filters UI noise denylist, redacts
// secrets through the common regex scanner, and produces a deterministic
// language-neutral SlicedPayload.
library;

import 'dart:convert';
import 'dart:io';

import 'humanize.dart';
import 'scanner.dart';
import 'sha256.dart';

int _byteOffset(String source, int charOffset) {
  if (charOffset <= 0) return 0;
  if (charOffset >= source.length) return utf8.encode(source).length;
  return utf8.encode(source.substring(0, charOffset)).length;
}

// --- Secret scanner & redaction ---------------------------------------------

final _secretPattern = RegExp(
  r"""\b(?:api[_-]?key|secret|token|password)\s*[:=]\s*['"]?[^\s;'"]{3,}['"]?""",
  caseSensitive: false,
);

class _RedactionResult {
  const _RedactionResult(this.text, this.count);
  final String text;
  final int count;
}

_RedactionResult _redactSecrets(String input) {
  var count = 0;
  final replaced = input.replaceAllMapped(_secretPattern, (match) {
    count++;
    final full = match.group(0)!;
    final colonIdx = full.indexOf(RegExp(r'[:=]'));
    if (colonIdx >= 0) {
      final prefix = full.substring(0, colonIdx + 1);
      return '$prefix "***REDACTED***"';
    }
    return '***REDACTED***';
  });
  return _RedactionResult(replaced, count);
}

// --- UI Noise Denylist ------------------------------------------------------

const _defaultDenylist = {
  'TextStyle',
  'BoxDecoration',
  'EdgeInsets',
  'EdgeInsetsDirectional',
  'BorderRadius',
  'Color',
  'Colors',
  'Icons',
  'IconData',
  'print',
  'debugPrint',
  'toJson',
  'fromJson',
  'SizedBox',
  'Container',
  'Padding',
  'Column',
  'Row',
  'Center',
  'Text',
  'Icon',
  'Spacer',
  'Expanded',
  'Flexible',
  'Divider',
  'Align',
};

// --- Boundary marker checks -------------------------------------------------

const _defaultBoundarySuffixes = [
  'Repository',
  'ApiClient',
  'Client',
  'Service',
  'Database',
  'Storage',
  'Gateway',
  'DataSource',
  'RemoteDataSource',
  'LocalDataSource',
];

bool _isBoundarySymbol(String symbolPath, List<String> boundarySuffixes) {
  for (final suffix in boundarySuffixes) {
    if (symbolPath.contains(suffix)) {
      return true;
    }
  }
  return false;
}

/// Returns a schema-safe placeholder for a call whose target cannot be
/// resolved statically. The edge still records that uncertainty explicitly.
String _unresolvedDynamicSymbol(String methodName) =>
    'unresolved_dynamic.$methodName';

// --- Slicing Data Structures ------------------------------------------------

class _SliceStep {
  _SliceStep({
    required this.ordinal,
    required this.kind,
    required this.description,
    required this.symbolPath,
    required this.anchor,
    this.guardCondition,
    this.stateBefore,
    this.stateAfter,
    this.effectTarget,
  });

  int ordinal;
  final String kind;
  final String description;
  final String symbolPath;
  final Map<String, Object?> anchor;
  final String? guardCondition;
  final String? stateBefore;
  final String? stateAfter;
  final String? effectTarget;

  Map<String, Object?> toJson() {
    final map = <String, Object?>{
      'ordinal': ordinal,
      'kind': kind,
      'description': description,
      'symbolPath': symbolPath,
      'anchor': anchor,
    };
    if (guardCondition != null) map['guardCondition'] = guardCondition;
    if (stateBefore != null) map['stateBefore'] = stateBefore;
    if (stateAfter != null) map['stateAfter'] = stateAfter;
    if (effectTarget != null) map['effectTarget'] = effectTarget;
    return map;
  }
}

class _SliceEdge {
  const _SliceEdge({
    required this.kind,
    required this.toSymbolPath,
    required this.resolutionStatus,
    required this.depth,
    required this.stepOrdinal,
  });

  final String kind;
  final String toSymbolPath;
  final String resolutionStatus;
  final int depth;

  /// 1-based ordinal of the step that produced this edge, so FlowView can
  /// attach the delegation target to the right timeline card.
  final int stepOrdinal;

  Map<String, Object?> toJson() => {
        'kind': kind,
        'toSymbolPath': toSymbolPath,
        'resolutionStatus': resolutionStatus,
        'depth': depth,
        'stepOrdinal': stepOrdinal,
      };
}

// --- POSIX & AST Helpers ----------------------------------------------------

String _toPosix(String path) => path.replaceAll('\\', '/');

String _canonicalFingerprint(String nodeText) {
  var t = nodeText
      .replaceAll(RegExp(r'//[^\n]*'), ' ')
      .replaceAll(RegExp(r'/\*.*?\*/', dotAll: true), ' ')
      .replaceAll(RegExp(r'\s+'), ' ')
      .trim();
  return sha256Hex(t);
}

// --- Resolver Context -------------------------------------------------------

class _ResolverContext {
  _ResolverContext({
    required this.repoRoot,
    required this.packageName,
    required this.boundarySuffixes,
    this.overlay,
  });

  final String repoRoot;
  final String packageName;
  final List<String> boundarySuffixes;
  final Map<String, String>? overlay;

  final Map<String, String> _fileCache = {};
  final Map<String, ScanResult> _scanCache = {};

  String? readFile(String repoRelPath) {
    if (_fileCache.containsKey(repoRelPath)) {
      return _fileCache[repoRelPath];
    }
    if (overlay != null) {
      final content = overlay![repoRelPath];
      if (content == null) return null;
      _fileCache[repoRelPath] = content;
      return content;
    }
    final fullPath = '$repoRoot/$repoRelPath';
    final file = File(fullPath);
    if (!file.existsSync()) {
      return null;
    }
    final content = file.readAsStringSync();
    _fileCache[repoRelPath] = content;
    return content;
  }

  ScanResult? scanFile(String repoRelPath) {
    if (_scanCache.containsKey(repoRelPath)) {
      return _scanCache[repoRelPath];
    }
    final content = readFile(repoRelPath);
    if (content == null) return null;
    final scan = scanSource(content);
    _scanCache[repoRelPath] = scan;
    return scan;
  }
}

// --- Cross-file Symbol Resolution -------------------------------------------

class _ResolvedTarget {
  const _ResolvedTarget({
    required this.repoRelativePath,
    required this.className,
    required this.methodName,
    required this.method,
  });

  final String repoRelativePath;
  final String className;
  final String methodName;
  final ScannedMethod method;

  String get symbolPath =>
      className.isNotEmpty ? '$className.$methodName' : methodName;

  String get fullEntryPath => '$repoRelativePath#$symbolPath';
}

/// Discovers imports declared in [source].
List<String> _extractImports(String source) {
  final imports = <String>[];
  final re = RegExp(r"""^\s*import\s+['"]([^'"]+)['"]""", multiLine: true);
  for (final m in re.allMatches(source)) {
    imports.add(m.group(1)!);
  }
  return imports;
}

/// Resolves an import URI to a repo-relative path. [packageName] is the
/// owning package of the importing file (used to map `package:<pkg>/...`
/// onto its `lib/`); [workspacePackages] maps every other package name in a
/// monorepo workspace onto its package root so cross-package imports
/// resolve too.
String? _resolveImportPath(String importUri, String currentRelPath,
    String packageName, Map<String, String> workspacePackages) {
  if (importUri.startsWith('package:')) {
    final rest = importUri.substring('package:'.length);
    final slash = rest.indexOf('/');
    if (slash < 0) return null;
    final pkg = rest.substring(0, slash);
    final subpath = rest.substring(slash + 1);
    if (pkg == packageName) {
      return 'lib/$subpath';
    }
    final pkgRoot = workspacePackages[pkg];
    if (pkgRoot != null) {
      return '$pkgRoot/lib/$subpath';
    }
    return null; // External package
  }
  if (importUri.startsWith('dart:')) {
    return null; // SDK library
  }
  // Relative import
  final curDir = currentRelPath.contains('/')
      ? currentRelPath.substring(0, currentRelPath.lastIndexOf('/'))
      : '';
  final parts = curDir.isEmpty ? <String>[] : curDir.split('/');
  for (final segment in importUri.split('/')) {
    if (segment == '.' || segment.isEmpty) continue;
    if (segment == '..') {
      if (parts.isNotEmpty) parts.removeLast();
    } else {
      parts.add(segment);
    }
  }
  return parts.join('/');
}

/// Extracts the provider variable name from a `ref.read(x)` / `ref.watch(x)`
/// receiver, or null when the receiver is not a ref-read.
String? _providerNameFromReceiver(String receiverOrFunc) {
  final m = RegExp(r'^ref\.(?:read|watch)\(\s*([A-Za-z0-9_$]+)\s*\)$')
      .firstMatch(receiverOrFunc.trim());
  return m?.group(1);
}

/// Attempts to resolve a called method on a receiver/function to a target file & method.
_ResolvedTarget? _resolveCallTarget({
  required _ResolverContext ctx,
  required Map<String, String> workspacePackages,
  required String currentRelPath,
  required String currentClassName,
  required String receiverOrFunc,
  required String methodName,
}) {
  final currentContent = ctx.readFile(currentRelPath);
  if (currentContent == null) return null;
  final currentScan = ctx.scanFile(currentRelPath);
  if (currentScan == null) return null;

  // 1. Check if receiver is a field in the current class or constructor parameter
  String? targetClassName;
  if (receiverOrFunc.isNotEmpty && receiverOrFunc != 'this') {
    // Look in current class body for field declaration: `final FooType receiver;`
    final fieldRe = RegExp(
      r'(?:(?:final|late|var|const)\s+)*([A-Z][A-Za-z0-9_$]*)\s+' +
          RegExp.escape(receiverOrFunc) +
          r'\b',
    );
    final m = fieldRe.firstMatch(currentContent);
    if (m != null) {
      targetClassName = m.group(1);
    }

    // Look for Provider / DI: `final fooProvider = Provider<FooService>(...)` or `ref.read(fooProvider)`.
    // The provider variable usually lives in the same file OR in an imported
    // domain file, so search the import closure too.
    if (targetClassName == null) {
      final cleanReceiver = _providerNameFromReceiver(receiverOrFunc) ??
          receiverOrFunc
              .replaceAll(RegExp(r'^ref\.read\('), '')
              .replaceAll(RegExp(r'\)$'), '');
      final providerRe = RegExp(
        r'(?:final\s+)?' +
            RegExp.escape(cleanReceiver) +
            r'\s*=\s*(?:StateNotifierProvider|NotifierProvider|Provider|ChangeNotifierProvider|FutureProvider|StreamProvider)\s*<([^>]+)>',
      );
      final pm = providerRe.firstMatch(currentContent);
      if (pm != null) {
        // `Provider<EstablishSessionAfterJoinUseCase>` — take the first type
        // argument's head as the class the call resolves to.
        final typeArg = pm.group(1)!.split(',')[0].trim();
        targetClassName = typeArg.split('<')[0].trim();
      } else {
        // Search imported files for the provider definition.
        for (final uri in _extractImports(currentContent)) {
          final resolvedPath = _resolveImportPath(
              uri, currentRelPath, ctx.packageName, workspacePackages);
          if (resolvedPath == null) continue;
          final importedContent = ctx.readFile(resolvedPath);
          if (importedContent == null) continue;
          final im = providerRe.firstMatch(importedContent);
          if (im != null) {
            final typeArg = im.group(1)!.split(',')[0].trim();
            targetClassName = typeArg.split('<')[0].trim();
            break;
          }
        }
      }
    }
  } else if (receiverOrFunc.isEmpty) {
    // Direct function or method in current class — unless the name is a
    // callable field (`final SignUpUseCase _signUp;` then `_signUp(draft)`
    // invokes `SignUpUseCase.call`). Resolve the field's declared type and
    // follow its `call` method so UseCase delegation traverses.
    final fieldRe = RegExp(
      r'(?:final|late|var|const)?\s*([A-Z][A-Za-z0-9_$]*)\s+' +
          RegExp.escape(methodName) +
          r'\s*[;=]',
    );
    final fm = fieldRe.firstMatch(currentContent);
    if (fm != null) {
      targetClassName = fm.group(1);
      methodName = 'call';
    } else {
      targetClassName = currentClassName;
    }
  }

  // If targetClassName is still null, but receiver matches a ClassName pattern (PascalCase), it might be static method or constructor
  if (targetClassName == null &&
      receiverOrFunc.isNotEmpty &&
      receiverOrFunc[0].toUpperCase() == receiverOrFunc[0] &&
      !receiverOrFunc.startsWith('_')) {
    targetClassName = receiverOrFunc;
  }

  // 2. Search in current file first
  if (targetClassName != null && targetClassName.isNotEmpty) {
    for (final cls in currentScan.classes) {
      if (cls.name == targetClassName) {
        for (final m in cls.methods) {
          if (m.name == methodName) {
            return _ResolvedTarget(
              repoRelativePath: currentRelPath,
              className: targetClassName,
              methodName: methodName,
              method: m,
            );
          }
        }
      }
    }
  }

  // Check top-level functions in current file
  if (targetClassName == null || targetClassName.isEmpty) {
    for (final fn in currentScan.topLevelFunctions) {
      if (fn.name == methodName) {
        return _ResolvedTarget(
          repoRelativePath: currentRelPath,
          className: '',
          methodName: methodName,
          method: fn,
        );
      }
    }
  }

  // 3. Search in imported files
  final importUris = _extractImports(currentContent);
  for (final uri in importUris) {
    final resolvedPath = _resolveImportPath(
        uri, currentRelPath, ctx.packageName, workspacePackages);
    if (resolvedPath == null) continue;
    final importedScan = ctx.scanFile(resolvedPath);
    if (importedScan == null) continue;

    if (targetClassName != null && targetClassName.isNotEmpty) {
      for (final cls in importedScan.classes) {
        if (cls.name == targetClassName) {
          for (final m in cls.methods) {
            if (m.name == methodName) {
              return _ResolvedTarget(
                repoRelativePath: resolvedPath,
                className: targetClassName,
                methodName: methodName,
                method: m,
              );
            }
          }
        }
      }
    } else {
      // Direct method/function search
      for (final cls in importedScan.classes) {
        for (final m in cls.methods) {
          if (m.name == methodName) {
            return _ResolvedTarget(
              repoRelativePath: resolvedPath,
              className: cls.name,
              methodName: methodName,
              method: m,
            );
          }
        }
      }
      for (final fn in importedScan.topLevelFunctions) {
        if (fn.name == methodName) {
          return _ResolvedTarget(
            repoRelativePath: resolvedPath,
            className: '',
            methodName: methodName,
            method: fn,
          );
        }
      }
    }
  }

  return null;
}

// --- Main Slicing Engine ----------------------------------------------------

/// Walks [repoRootPosix] for nested `pubspec.yaml` files (packages/*,
/// apps/*) and records `<package name> -> <package dir>` pairs so
/// cross-package imports resolve inside a monorepo workspace. Bounded to
/// two directory levels to stay deterministic and cheap.
void _collectWorkspacePackages(String repoRootPosix, Map<String, String> out) {
  final rootDir = Directory(repoRootPosix);
  if (!rootDir.existsSync()) return;
  void scanPackageDir(String dirPath) {
    final pubspec = File('$dirPath/pubspec.yaml');
    if (pubspec.existsSync()) {
      try {
        for (final line in pubspec.readAsStringSync().split('\n')) {
          final m = RegExp(r'^name:\s*([^\s#]+)').firstMatch(line.trimLeft());
          if (m != null) {
            final rel = dirPath.startsWith(repoRootPosix)
                ? dirPath.substring(repoRootPosix.length + 1)
                : dirPath;
            out[m.group(1)!] = rel;
            return;
          }
        }
      } catch (_) {
        // unreadable pubspec: skip deterministically
      }
    }
  }

  for (final top in ['packages', 'apps']) {
    final topDir = Directory('$repoRootPosix/$top');
    if (!topDir.existsSync()) continue;
    for (final entity in topDir.listSync(followLinks: false)
      ..sort((a, b) => a.path.compareTo(b.path))) {
      if (entity is Directory) {
        scanPackageDir(entity.path);
      }
    }
  }
}

/// Performs Stage 2 Structural Slice for a given candidate.
Map<String, Object?> sliceCandidate({
  required String repoRoot,
  required String candidateId,
  required String entrySymbolPath,
  Map<String, Object?> opts = const {},
  Map<String, String>? contentOverlay,
}) {
  final posixRoot = _toPosix(repoRoot);
  final pubspecFile = File('$posixRoot/pubspec.yaml');
  var packageName = '';
  final pubspecContent = contentOverlay != null
      ? contentOverlay['pubspec.yaml']
      : (pubspecFile.existsSync() ? pubspecFile.readAsStringSync() : null);
  if (pubspecContent != null) {
    final match = RegExp(r'^name:\s*([a-zA-Z0-9_]+)', multiLine: true)
        .firstMatch(pubspecContent);
    if (match != null) {
      packageName = match.group(1)!;
    }
  }

  // Split entrySymbolPath: e.g. "lib/features/auth/email_signup_notifier.dart#EmailSignupNotifier.submit"
  final hashIndex = entrySymbolPath.indexOf('#');
  if (hashIndex < 0) {
    throw ArgumentError('Invalid entrySymbolPath format: $entrySymbolPath');
  }
  final initialRelPath0 = entrySymbolPath.substring(0, hashIndex);
  final initialSymbol = entrySymbolPath.substring(hashIndex + 1);
  // Repo-relative path as published (may already carry packages/ prefix).
  final initialRelPath = initialRelPath0;

  // Monorepo workspace: map every sibling package's name onto its root so
  // `package:<pkg>/...` imports resolve across packages. The owning package
  // of the entry file wins via [packageName] above; when the entry lives in
  // a nested package (workspace layout), that package's name is used.
  var effectivePackageName = packageName;
  final workspacePackages = <String, String>{};
  if (contentOverlay == null)
    _collectWorkspacePackages(posixRoot, workspacePackages);
  if (effectivePackageName.isEmpty && workspacePackages.isNotEmpty) {
    // Infer from the entry file path: packages/<name>/lib/...
    final m = RegExp(r'^packages/([^/]+)/').firstMatch(initialRelPath0);
    if (m != null) {
      final pkgDir = 'packages/${m.group(1)}';
      for (final entry in workspacePackages.entries) {
        if (entry.value == pkgDir) {
          effectivePackageName = entry.key;
          break;
        }
      }
    }
  }

  final boundarySuffixes = <String>[..._defaultBoundarySuffixes];
  final customBoundaries = opts['boundaryMarkers'];
  if (customBoundaries is List) {
    for (final b in customBoundaries) {
      if (b is String && b.isNotEmpty && !boundarySuffixes.contains(b)) {
        boundarySuffixes.add(b);
      }
    }
  }

  final ctx = _ResolverContext(
    repoRoot: posixRoot,
    packageName: effectivePackageName,
    boundarySuffixes: boundarySuffixes,
    overlay: contentOverlay,
  );

  final steps = <_SliceStep>[];
  final edges = <_SliceEdge>[];
  final visitedSet = <String>{};
  var truncated = false;
  var visitedCycleDetected = false;
  var totalRedactedCount = 0;

  String initialClass = '';
  String initialMethod = initialSymbol;
  if (initialSymbol.contains('.')) {
    final dot = initialSymbol.indexOf('.');
    initialClass = initialSymbol.substring(0, dot);
    initialMethod = initialSymbol.substring(dot + 1);
  }

  // Recursive slice walker
  void sliceMethodBody({
    required String relPath,
    required String className,
    required String methodName,
    required int depth,
  }) {
    final fullSym =
        className.isNotEmpty ? '$className.$methodName' : methodName;
    final entryKey = '$relPath#$fullSym';

    if (visitedSet.contains(entryKey)) {
      visitedCycleDetected = true;
      return;
    }
    visitedSet.add(entryKey);

    if (depth > 5) {
      truncated = true;
      return;
    }

    final fileContent = ctx.readFile(relPath);
    if (fileContent == null) return;
    final scan = ctx.scanFile(relPath);
    if (scan == null) return;

    ScannedMethod? targetMethod;
    if (className.isNotEmpty) {
      for (final cls in scan.classes) {
        if (cls.name == className) {
          for (final m in cls.methods) {
            if (m.name == methodName) {
              targetMethod = m;
              break;
            }
          }
          break;
        }
      }
    } else {
      for (final fn in scan.topLevelFunctions) {
        if (fn.name == methodName) {
          targetMethod = fn;
          break;
        }
      }
    }

    if (targetMethod == null) return;

    final fileHash = sha256Hex(fileContent);
    final bodySource =
        fileContent.substring(targetMethod.bodyStart, targetMethod.bodyEnd);
    final bodyStartOffset = targetMethod.bodyStart;

    // Symbol-scoped view range for FlowView: signature line through the end
    // of the method body. Presentation-only; never used for identity.
    final symbolRange = [
      _byteOffset(
          fileContent, _lineStartOffset(fileContent, targetMethod.nameLine)),
      _byteOffset(fileContent, targetMethod.bodyEnd),
    ];

    // Slice statements in body
    final stmtList =
        _extractStatements(bodySource, bodyStartOffset, fileContent);

    for (final stmt in stmtList) {
      final spanBytes = fileContent.substring(stmt.startOffset, stmt.endOffset);
      final spanHash = sha256Hex(spanBytes);
      final canonicalAst = _canonicalFingerprint(spanBytes);

      // Check redactions in the raw statement span
      final redactSpan = _redactSecrets(spanBytes);
      totalRedactedCount += redactSpan.count;

      final anchor = <String, Object?>{
        'repoRelativePath': relPath,
        'byteRange': [
          _byteOffset(fileContent, stmt.startOffset),
          _byteOffset(fileContent, stmt.endOffset)
        ],
        'fileHash': fileHash,
        'spanHash': spanHash,
        'enclosingSymbolPath': fullSym,
        'canonicalAstFingerprint': canonicalAst,
        'symbolRange': symbolRange,
      };

      // Guard check
      if (stmt.type == _StmtType.guard) {
        final rawCond = stmt.guardCondition ?? '';
        final redactCond = _redactSecrets(rawCond);
        totalRedactedCount += redactCond.count;

        var desc = _deriveGuardDescription(redactCond.text);
        final redactDesc = _redactSecrets(desc);
        totalRedactedCount += redactDesc.count;

        steps.add(_SliceStep(
          ordinal: steps.length + 1,
          kind: 'guard',
          description: redactDesc.text,
          symbolPath: fullSym,
          anchor: anchor,
          guardCondition: redactCond.text,
        ));
      } else if (stmt.type == _StmtType.mutation) {
        final rawBefore = stmt.stateBefore;
        final rawAfter = stmt.stateAfter;

        String? finalBefore;
        String? finalAfter;
        if (rawBefore != null) {
          final r = _redactSecrets(rawBefore);
          totalRedactedCount += r.count;
          finalBefore = r.text;
        }
        if (rawAfter != null) {
          final r = _redactSecrets(rawAfter);
          totalRedactedCount += r.count;
          finalAfter = r.text;
        }

        var desc = _deriveMutationDescription(stmt.rawText, finalAfter);
        final redactDesc = _redactSecrets(desc);
        totalRedactedCount += redactDesc.count;

        steps.add(_SliceStep(
          ordinal: steps.length + 1,
          kind: 'mutation',
          description: redactDesc.text,
          symbolPath: fullSym,
          anchor: anchor,
          stateBefore: finalBefore,
          stateAfter: finalAfter,
        ));
      } else if (stmt.type == _StmtType.call) {
        final receiver = stmt.callReceiver ?? '';
        final calledMethod = stmt.callMethod ?? '';

        if (_defaultDenylist.contains(calledMethod) ||
            _defaultDenylist.contains(receiver)) {
          continue;
        }

        final callTargetSym =
            receiver.isNotEmpty ? '$receiver.$calledMethod' : calledMethod;

        // Determine receiver type if receiver is a variable or provider
        String? receiverType;
        if (receiver.isNotEmpty && receiver != 'this') {
          final fieldRe = RegExp(
            r'(?:(?:final|late|var|const)\s+)*([A-Z][A-Za-z0-9_$]*)\s+' +
                RegExp.escape(receiver) +
                r'\b',
          );
          final m = fieldRe.firstMatch(fileContent);
          if (m != null) {
            receiverType = m.group(1);
          } else {
            final cleanReceiver = _providerNameFromReceiver(receiver) ??
                receiver
                    .replaceAll(RegExp(r'^ref\.read\('), '')
                    .replaceAll(RegExp(r'\)$'), '');
            final providerRe = RegExp(
              r'(?:final\s+)?' +
                  RegExp.escape(cleanReceiver) +
                  r'\s*=\s*(?:StateNotifierProvider|NotifierProvider|Provider|ChangeNotifierProvider|FutureProvider|StreamProvider)\s*<([^>]+)>',
            );
            final pm = providerRe.firstMatch(fileContent);
            if (pm != null) {
              final typeArg = pm.group(1)!.split(',')[0].trim();
              receiverType = typeArg.split('<')[0].trim();
            } else {
              for (final uri in _extractImports(fileContent)) {
                final resolvedPath = _resolveImportPath(
                    uri, relPath, effectivePackageName, workspacePackages);
                if (resolvedPath == null) continue;
                final importedContent = ctx.readFile(resolvedPath);
                if (importedContent == null) continue;
                final im = providerRe.firstMatch(importedContent);
                if (im != null) {
                  final typeArg = im.group(1)!.split(',')[0].trim();
                  receiverType = typeArg.split('<')[0].trim();
                  break;
                }
              }
            }
          }
        }

        final isBoundary = _isBoundarySymbol(callTargetSym, boundarySuffixes) ||
            _isBoundarySymbol(receiver, boundarySuffixes) ||
            (receiverType != null &&
                _isBoundarySymbol(receiverType, boundarySuffixes));

        // Attempt cross-file resolution first
        final resolved = _resolveCallTarget(
          ctx: ctx,
          workspacePackages: workspacePackages,
          currentRelPath: relPath,
          currentClassName: className,
          receiverOrFunc: receiver,
          methodName: calledMethod,
        );

        if (resolved != null) {
          final isResolvedBoundary = isBoundary ||
              _isBoundarySymbol(resolved.symbolPath, boundarySuffixes) ||
              _isBoundarySymbol(resolved.className, boundarySuffixes);
          final edgeKind =
              isResolvedBoundary ? 'boundary_call' : 'resolved_cross_file';
          final redactDesc = _redactSecrets(_deriveCallDescription(
              resolved.symbolPath,
              isBoundary: isResolvedBoundary));
          totalRedactedCount += redactDesc.count;

          steps.add(_SliceStep(
            ordinal: steps.length + 1,
            kind: 'call',
            description: redactDesc.text,
            symbolPath: resolved.symbolPath,
            anchor: anchor,
            effectTarget: isResolvedBoundary ? resolved.symbolPath : null,
          ));

          edges.add(_SliceEdge(
            kind: edgeKind,
            toSymbolPath: resolved.fullEntryPath,
            resolutionStatus: 'resolved',
            depth: depth,
            stepOrdinal: steps.last.ordinal,
          ));

          // Traverse target
          if (depth < 5) {
            sliceMethodBody(
              relPath: resolved.repoRelativePath,
              className: resolved.className,
              methodName: resolved.methodName,
              depth: depth + 1,
            );
          } else {
            truncated = true;
          }
        } else if (isBoundary) {
          final boundarySym = receiverType != null
              ? '$receiverType.$calledMethod'
              : callTargetSym;
          final redactDesc = _redactSecrets(
              _deriveCallDescription(boundarySym, isBoundary: true));
          totalRedactedCount += redactDesc.count;

          final redactTarget = _redactSecrets(boundarySym);
          totalRedactedCount += redactTarget.count;

          steps.add(_SliceStep(
            ordinal: steps.length + 1,
            kind: 'call',
            description: redactDesc.text,
            symbolPath: boundarySym,
            anchor: anchor,
            effectTarget: redactTarget.text,
          ));

          edges.add(_SliceEdge(
            kind: 'boundary_call',
            toSymbolPath: '$relPath#$boundarySym',
            resolutionStatus: 'resolved',
            depth: depth,
            stepOrdinal: steps.last.ordinal,
          ));
        } else {
          // Unresolved dynamic call / unknown edge
          if (calledMethod.isNotEmpty &&
              !_defaultDenylist.contains(calledMethod)) {
            final redactDesc = _redactSecrets(
                _deriveCallDescription(callTargetSym, isBoundary: false));
            totalRedactedCount += redactDesc.count;

            steps.add(_SliceStep(
              ordinal: steps.length + 1,
              kind: 'call',
              description: redactDesc.text,
              symbolPath: fullSym,
              anchor: anchor,
            ));

            edges.add(_SliceEdge(
              kind: 'unknown_edge',
              toSymbolPath:
                  '$relPath#${_unresolvedDynamicSymbol(calledMethod)}',
              resolutionStatus: 'unresolved_dynamic',
              depth: depth,
              stepOrdinal: steps.last.ordinal,
            ));
          }
        }
      } else if (stmt.type == _StmtType.branch) {
        final redactDesc =
            _redactSecrets(_deriveBranchDescription(stmt.rawText));
        totalRedactedCount += redactDesc.count;

        steps.add(_SliceStep(
          ordinal: steps.length + 1,
          kind: 'branch',
          description: redactDesc.text,
          symbolPath: fullSym,
          anchor: anchor,
        ));
      }
    }
  }

  sliceMethodBody(
    relPath: initialRelPath,
    className: initialClass,
    methodName: initialMethod,
    depth: 1,
  );

  if (steps.isEmpty) {
    final fileContent = ctx.readFile(initialRelPath) ?? '';
    final fileHash = sha256Hex(fileContent);
    final fullSym = initialClass.isNotEmpty
        ? '$initialClass.$initialMethod'
        : initialMethod;
    steps.add(_SliceStep(
      ordinal: 1,
      kind: 'call',
      description: humanizeIdentifier(initialMethod),
      symbolPath: fullSym,
      anchor: {
        'repoRelativePath': initialRelPath,
        'byteRange': [0, utf8.encode(fileContent).length],
        'fileHash': fileHash,
        'spanHash': fileHash,
        'enclosingSymbolPath': fullSym,
        'canonicalAstFingerprint': sha256Hex(''),
        'symbolRange': [0, utf8.encode(fileContent).length],
      },
    ));
  }

  // Normalize ordinals
  for (var i = 0; i < steps.length; i++) {
    steps[i].ordinal = i + 1;
  }

  return {
    'candidateId': candidateId,
    'language': 'dart',
    'entrySymbolPath': entrySymbolPath,
    'steps': steps.map((s) => s.toJson()).toList(),
    'edges': edges.map((e) => e.toJson()).toList(),
    'truncated': truncated,
    'visitedCycleDetected': visitedCycleDetected,
    'redactedCount': totalRedactedCount,
  };
}

// --- Statement Extraction Model ---------------------------------------------

enum _StmtType { guard, mutation, call, branch }

class _ExtractedStmt {
  const _ExtractedStmt({
    required this.type,
    required this.startOffset,
    required this.endOffset,
    required this.rawText,
    this.guardCondition,
    this.stateBefore,
    this.stateAfter,
    this.callReceiver,
    this.callMethod,
  });

  final _StmtType type;
  final int startOffset;
  final int endOffset;
  final String rawText;
  final String? guardCondition;
  final String? stateBefore;
  final String? stateAfter;
  final String? callReceiver;
  final String? callMethod;
}

List<_ExtractedStmt> _extractStatements(
    String bodySource, int baseOffset, String fullFileSource) {
  final results = <_ExtractedStmt>[];

  // Regex patterns for statements
  // Capture the full balanced paren body of `if (...)` so nested calls in
  // the condition (`if (await _signUp(draft) case ...)`) survive intact.
  RegExp guardRe = RegExp(r'\bif\s*\(', multiLine: true);
  final throwRe = RegExp(r'\bthrow\s+([^;]+);', multiLine: true);
  final mutationRe = RegExp(
    r'\b(?:state|_state|value)\s*=\s*([^;]+);'
    r'|\bemit\s*\(([^)]+)\);'
    r'|\bstate\.copyWith\s*\(([^)]+)\)',
    multiLine: true,
  );
  final callRe = RegExp(
    r'(?:await\s+)?([A-Za-z0-9_$.()]+)\s*\(([^)]*)\);',
    multiLine: true,
  );
  // Condition form: no trailing `;` — used inside `if (...)` guards.
  final condCallRe = RegExp(
    r'(?:await\s+)?([A-Za-z0-9_$.()]+)\s*\(([^)]*)\)',
    multiLine: true,
  );
  final catchRe = RegExp(
    r'\bon\s+([A-Za-z0-9_$]+)\s*(?:catch\s*\([^)]*\))?\s*\{|\bcatch\s*\([^)]*\)\s*\{',
    multiLine: true,
  );
  final varAssignRe = RegExp(
    r'\b(?:final|var|const|[A-Z][A-Za-z0-9_$]*)\s+([A-Za-z0-9_$]+)\s*=\s*([^;]+);',
    multiLine: true,
  );

  // Guards (if) — balanced-paren scan
  final occupied = <List<int>>[];
  for (final m in guardRe.allMatches(bodySource)) {
    // depth counts NESTED opens between the outer parens; the first `)`
    // seen at depth 0 closes the `if (` itself.
    var depth = 0;
    var j = m.end;
    while (j < bodySource.length) {
      final ch = bodySource[j];
      if (ch == '(') {
        depth++;
      } else if (ch == ')') {
        if (depth == 0) break;
        depth--;
      }
      j++;
    }
    if (j >= bodySource.length) continue;
    final condStart = m.end;
    final condEnd = j; // exclusive, at closing paren
    final cond = bodySource.substring(condStart, condEnd).trim();
    occupied.add([m.start, condEnd + 1]);
    results.add(_ExtractedStmt(
      type: _StmtType.guard,
      startOffset: baseOffset + m.start,
      endOffset: baseOffset + condEnd + 1,
      rawText: bodySource.substring(m.start, condEnd + 1),
      guardCondition: cond,
    ));
    // A call awaited inside the condition (`if (await _signUp(draft) ...)`)
    // is a real delegation step, not just a guard — extract it too so the
    // slice can follow it across layers.
    for (final cm in condCallRe.allMatches(cond)) {
      final cTarget = cm.group(1)!.trim();
      final cRaw = cm.group(0)!;
      // Keep genuine invocations only: awaited calls, member calls, or
      // private callable fields. Skips type checks (`case Error(:final e)`).
      final genuine = cRaw.startsWith('await') ||
          cTarget.contains('.') ||
          cTarget.startsWith('_');
      if (cTarget.isEmpty || !genuine) {
        continue;
      }
      var cReceiver = '';
      var cMethod = cTarget;
      if (cTarget.contains('ref.read(')) {
        cReceiver = cTarget;
        cMethod = 'call';
      } else if (cTarget.contains('.')) {
        final lastDot = cTarget.lastIndexOf('.');
        cReceiver = cTarget.substring(0, lastDot);
        cMethod = cTarget.substring(lastDot + 1);
      }
      results.add(_ExtractedStmt(
        type: _StmtType.call,
        startOffset: baseOffset + condStart + cm.start,
        endOffset: baseOffset + condStart + cm.end,
        rawText: cm.group(0)!,
        callReceiver: cReceiver,
        callMethod: cMethod,
      ));
    }
  }

  // Throws (as guards / failure branches)
  for (final m in throwRe.allMatches(bodySource)) {
    occupied.add([m.start, m.end]);
    results.add(_ExtractedStmt(
      type: _StmtType.guard,
      startOffset: baseOffset + m.start,
      endOffset: baseOffset + m.end,
      rawText: m.group(0)!,
      guardCondition: '실패 시 예외 발생: ${m.group(1)!.trim()}',
    ));
  }

  bool insideOccupied(int start, int end) {
    for (final span in occupied) {
      if (start < span[1] && end > span[0]) return true;
    }
    return false;
  }

  // Mutations
  for (final m in mutationRe.allMatches(bodySource)) {
    if (insideOccupied(m.start, m.end)) continue;
    final raw = m.group(0)!;
    String? stateAfter;
    String? stateBefore;

    if (raw.contains('copyWith')) {
      final inner = m.group(3) ?? m.group(1) ?? '';
      stateAfter = inner.trim();
      stateBefore = 'status: idle';
    } else if (raw.contains('emit')) {
      stateAfter = (m.group(2) ?? '').trim();
    } else {
      stateAfter = (m.group(1) ?? '').trim();
    }

    results.add(_ExtractedStmt(
      type: _StmtType.mutation,
      startOffset: baseOffset + m.start,
      endOffset: baseOffset + m.end,
      rawText: raw,
      stateBefore: stateBefore,
      stateAfter: stateAfter,
    ));
  }

  // Calls
  for (final m in callRe.allMatches(bodySource)) {
    if (insideOccupied(m.start, m.end)) continue;
    final raw = m.group(0)!;
    final target = m.group(1)!.trim();

    if (raw.startsWith('if') ||
        raw.startsWith('emit') ||
        raw.contains('copyWith')) {
      continue;
    }
    // Skip accessor tails like `.error(...)` where the regex started mid-
    // expression (generics keep the real receiver out of reach).
    if (target.startsWith('.')) {
      continue;
    }

    var receiver = '';
    var method = target;

    // Handle ref.read(provider)(args) or foo.bar(args)
    if (target.contains('ref.read(')) {
      receiver = target;
      method = 'call';
    } else if (target.contains('.')) {
      final lastDot = target.lastIndexOf('.');
      receiver = target.substring(0, lastDot);
      method = target.substring(lastDot + 1);
    }

    results.add(_ExtractedStmt(
      type: _StmtType.call,
      startOffset: baseOffset + m.start,
      endOffset: baseOffset + m.end,
      rawText: raw,
      callReceiver: receiver,
      callMethod: method,
    ));
  }

  // Branches
  for (final m in catchRe.allMatches(bodySource)) {
    results.add(_ExtractedStmt(
      type: _StmtType.branch,
      startOffset: baseOffset + m.start,
      endOffset: baseOffset + m.end,
      rawText: m.group(0)!,
    ));
  }

  // Check local variable assignments that contain secrets
  for (final m in varAssignRe.allMatches(bodySource)) {
    final raw = m.group(0)!;
    if (_secretPattern.hasMatch(raw)) {
      results.add(_ExtractedStmt(
        type: _StmtType.mutation,
        startOffset: baseOffset + m.start,
        endOffset: baseOffset + m.end,
        rawText: raw,
        stateAfter: 'secret value assigned',
      ));
    }
  }

  results.sort((a, b) => a.startOffset.compareTo(b.startOffset));
  return results;
}

// --- Derived Descriptions ---------------------------------------------------

String _deriveGuardDescription(String condition) {
  if (condition.contains('email') && condition.contains('@')) {
    return '이메일 형식과 유효성을 검사한다';
  }
  if (condition.contains('email') && condition.contains('isEmpty')) {
    return '이메일 필수 입력 여부를 검사한다';
  }
  if (condition.contains('password')) {
    return '비밀번호 규칙을 검증한다';
  }
  if (condition.contains('!= null') || condition.contains('== null')) {
    return '필수 데이터 존재 여부를 확인한다';
  }
  return '조건 분기를 확인한다: $condition';
}

String _deriveMutationDescription(String raw, String? stateAfter) {
  if (stateAfter != null) {
    if (stateAfter.contains('submitting') || stateAfter.contains('loading')) {
      return '진행 상태로 갱신한다';
    }
    if (stateAfter.contains('done') || stateAfter.contains('success')) {
      return '성공 상태로 갱신한다';
    }
    if (stateAfter.contains('failed') || stateAfter.contains('error')) {
      return '실패 상태로 갱신한다';
    }
    if (stateAfter.contains('idle')) {
      return '초기 대기 상태로 재설정한다';
    }
  }
  return '상태를 갱신한다';
}

// Domain-centric call description: derive business intent from target symbol
// instead of generic delegation phrase. Uses verb/noun mapping similar to
// Go's naming.DeriveTitle but in Dart, falls back to humanized English.
const _koVerbMap = <String, String>{
  'submit': '제출한다',
  'send': '전송한다',
  'create': '생성한다',
  'save': '저장한다',
  'update': '갱신한다',
  'delete': '삭제한다',
  'remove': '제거한다',
  'load': '불러온다',
  'fetch': '가져온다',
  'check': '검사한다',
  'validate': '검증한다',
  'verify': '인증한다',
  'is': '확인한다',
  'taken': '확인한다',
  'available': '확인한다',
  'exist': '확인한다',
  'exists': '확인한다',
  'login': '로그인한다',
  'signup': '회원가입한다',
  'register': '가입한다',
  'handle': '처리한다',
  'execute': '실행한다',
  'call': '호출한다',
  'valid': '검증한다',
  'invalid': '검증한다',
};

const _koNounMap = <String, String>{
  'order': '주문을',
  'cart': '장바구니를',
  'user': '사용자를',
  'email': '이메일을',
  'password': '비밀번호를',
  'auth': '인증 정보를',
  'token': '토큰을',
  'session': '세션을',
  'profile': '프로필을',
  'status': '상태를',
  'data': '데이터를',
  'signup': '회원가입을',
  'account': '계정을',
  'availability': '가용성을',
  'validity': '유효성을',
  'address': '주소를',
};

String _extractIntentIdentifier(String target) {
  var t = target;
  if (t.contains('#')) t = t.split('#').last;
  if (t.contains('/')) t = t.split('/').last;
  if (t.contains(':')) t = t.split(':').last;
  final parts = t.split('.').where((p) => p.isNotEmpty).toList();
  if (parts.isEmpty) return t;
  const genericMethods = {'call', 'execute', 'run', 'invoke', 'perform'};
  var last = parts.last;
  if (genericMethods.contains(last.toLowerCase()) && parts.length >= 2) {
    var cls = parts[parts.length - 2];
    // Strip architecture suffixes to expose domain intent
    for (final suf in [
      'UseCase',
      'Usecase',
      'Repository',
      'Controller',
      'Service',
      'ApiClient',
      'DataSource',
      'Manager'
    ]) {
      if (cls.endsWith(suf) && cls.length > suf.length) {
        cls = cls.substring(0, cls.length - suf.length);
        break;
      }
    }
    return cls.isNotEmpty ? cls : last;
  }
  // For isValid style, keep last but caller will combine if needed
  return last;
}

String _deriveDomainTitle(String raw) {
  final words = splitIdentifierWords(raw).map((w) => w.toLowerCase()).toList();
  if (words.isEmpty) return humanizeIdentifier(raw);
  String foundVerbKo = '';
  String foundNounKo = '';
  String verbWord = '';
  for (final w in words) {
    if (_koVerbMap.containsKey(w) && foundVerbKo.isEmpty) {
      foundVerbKo = _koVerbMap[w]!;
      verbWord = w;
    }
  }
  for (final w in words) {
    if (_koNounMap.containsKey(w) && foundNounKo.isEmpty) {
      if (w == verbWord) continue;
      foundNounKo = _koNounMap[w]!;
    }
  }
  if (foundVerbKo.isNotEmpty && foundNounKo.isNotEmpty)
    return '$foundNounKo $foundVerbKo';
  if (foundVerbKo.isNotEmpty) return foundVerbKo;
  return humanizeIdentifier(raw);
}

String _deriveCallDescription(String target, {required bool isBoundary}) {
  final intent = _extractIntentIdentifier(target);
  final domain = _deriveDomainTitle(intent);
  // Keep boundary distinction only in layer/edge kind, not in title.
  // Title is pure domain intent; FlowView lanes already convey external vs internal.
  return domain;
}

String _deriveBranchDescription(String raw) {
  if (raw.contains('Exception') || raw.contains('catch')) {
    return '오류 발생 시 예외를 포착하고 대체 처리한다';
  }
  return '분기 처리를 수행한다';
}

/// Byte offset of the start of the 0-based [line] in [source].
/// Clamps to the last line start when [line] exceeds the file.
int _lineStartOffset(String source, int line) {
  var offset = 0;
  for (var i = 0; i < line; i++) {
    final next = source.indexOf('\n', offset);
    if (next < 0) return offset;
    offset = next + 1;
  }
  return offset;
}
