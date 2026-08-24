// Stage 2 Structural Slice (design §4.2, schemas/sliced-payload.schema.json).
//
// Extracts guard, mutation, effect/call and branch steps from AST statement
// slicing, follows import-resolved direct references across files up to depth 5,
// terminates at boundary markers (Repository/ApiClient/Service), records unknown
// edges for unresolved/dynamic calls, filters UI noise denylist, redacts
// secrets through the common regex scanner, and produces a deterministic
// language-neutral SlicedPayload.
library;

import 'dart:io';

import 'humanize.dart';
import 'scanner.dart';
import 'sha256.dart';

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
  });

  final String kind;
  final String toSymbolPath;
  final String resolutionStatus;
  final int depth;

  Map<String, Object?> toJson() => {
        'kind': kind,
        'toSymbolPath': toSymbolPath,
        'resolutionStatus': resolutionStatus,
        'depth': depth,
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
  });

  final String repoRoot;
  final String packageName;
  final List<String> boundarySuffixes;

  final Map<String, String> _fileCache = {};
  final Map<String, ScanResult> _scanCache = {};

  String? readFile(String repoRelPath) {
    if (_fileCache.containsKey(repoRelPath)) {
      return _fileCache[repoRelPath];
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

/// Resolves an import URI to a repo-relative path.
String? _resolveImportPath(
    String importUri, String currentRelPath, String packageName) {
  if (importUri.startsWith('package:')) {
    final rest = importUri.substring('package:'.length);
    final slash = rest.indexOf('/');
    if (slash < 0) return null;
    final pkg = rest.substring(0, slash);
    final subpath = rest.substring(slash + 1);
    if (pkg == packageName) {
      return 'lib/$subpath';
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

/// Attempts to resolve a called method on a receiver/function to a target file & method.
_ResolvedTarget? _resolveCallTarget({
  required _ResolverContext ctx,
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

    // Look for Provider / DI: `final fooProvider = Provider<FooService>(...)` or `ref.read(fooProvider)`
    if (targetClassName == null) {
      final cleanReceiver = receiverOrFunc.replaceAll(RegExp(r'^ref\.read\('), '').replaceAll(RegExp(r'\)$'), '');
      final providerRe = RegExp(
        RegExp.escape(cleanReceiver) +
            r'\s*=\s*(?:StateNotifierProvider|NotifierProvider|Provider|ChangeNotifierProvider|FutureProvider)\s*<([A-Za-z0-9_$]+)',
      );
      final pm = providerRe.firstMatch(currentContent);
      if (pm != null) {
        targetClassName = pm.group(1);
      }
    }
  } else if (receiverOrFunc.isEmpty) {
    // Direct function or method in current class
    targetClassName = currentClassName;
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
    final resolvedPath =
        _resolveImportPath(uri, currentRelPath, ctx.packageName);
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

/// Performs Stage 2 Structural Slice for a given candidate.
Map<String, Object?> sliceCandidate({
  required String repoRoot,
  required String candidateId,
  required String entrySymbolPath,
  Map<String, Object?> opts = const {},
}) {
  final posixRoot = _toPosix(repoRoot);
  final pubspecFile = File('$posixRoot/pubspec.yaml');
  var packageName = '';
  if (pubspecFile.existsSync()) {
    final match = RegExp(r'^name:\s*([a-zA-Z0-9_]+)', multiLine: true)
        .firstMatch(pubspecFile.readAsStringSync());
    if (match != null) {
      packageName = match.group(1)!;
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
    packageName: packageName,
    boundarySuffixes: boundarySuffixes,
  );

  final steps = <_SliceStep>[];
  final edges = <_SliceEdge>[];
  final visitedSet = <String>{};
  var truncated = false;
  var visitedCycleDetected = false;
  var totalRedactedCount = 0;

  // Split entrySymbolPath: e.g. "lib/features/auth/email_signup_notifier.dart#EmailSignupNotifier.submit"
  final hashIndex = entrySymbolPath.indexOf('#');
  if (hashIndex < 0) {
    throw ArgumentError('Invalid entrySymbolPath format: $entrySymbolPath');
  }
  final initialRelPath = entrySymbolPath.substring(0, hashIndex);
  final initialSymbol = entrySymbolPath.substring(hashIndex + 1);

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
    final fullSym = className.isNotEmpty ? '$className.$methodName' : methodName;
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
    final bodySource = fileContent.substring(targetMethod.bodyStart, targetMethod.bodyEnd);
    final bodyStartOffset = targetMethod.bodyStart;

    // Slice statements in body
    final stmtList = _extractStatements(bodySource, bodyStartOffset, fileContent);

    for (final stmt in stmtList) {
      final spanBytes = fileContent.substring(stmt.startOffset, stmt.endOffset);
      final spanHash = sha256Hex(spanBytes);
      final canonicalAst = _canonicalFingerprint(spanBytes);

      // Check redactions in the raw statement span
      final redactSpan = _redactSecrets(spanBytes);
      totalRedactedCount += redactSpan.count;

      final anchor = <String, Object?>{
        'repoRelativePath': relPath,
        'byteRange': [stmt.startOffset, stmt.endOffset],
        'fileHash': fileHash,
        'spanHash': spanHash,
        'enclosingSymbolPath': fullSym,
        'canonicalAstFingerprint': canonicalAst,
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

        final callTargetSym = receiver.isNotEmpty
            ? '$receiver.$calledMethod'
            : calledMethod;

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
            final cleanReceiver = receiver
                .replaceAll(RegExp(r'^ref\.read\('), '')
                .replaceAll(RegExp(r'\)$'), '');
            final providerRe = RegExp(
              RegExp.escape(cleanReceiver) +
                  r'\s*=\s*(?:StateNotifierProvider|NotifierProvider|Provider|ChangeNotifierProvider|FutureProvider)\s*<([A-Za-z0-9_$]+)',
            );
            final pm = providerRe.firstMatch(fileContent);
            if (pm != null) {
              receiverType = pm.group(1);
            }
          }
        }

        final isBoundary = _isBoundarySymbol(callTargetSym, boundarySuffixes) ||
            _isBoundarySymbol(receiver, boundarySuffixes) ||
            (receiverType != null &&
                _isBoundarySymbol(receiverType, boundarySuffixes));

        // Check boundary marker
        if (isBoundary) {
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
          ));
        } else {

          // Attempt cross-file resolution
          final resolved = _resolveCallTarget(
            ctx: ctx,
            currentRelPath: relPath,
            currentClassName: className,
            receiverOrFunc: receiver,
            methodName: calledMethod,
          );

          if (resolved != null) {
            final redactDesc = _redactSecrets(_deriveCallDescription(
                resolved.symbolPath,
                isBoundary: false));
            totalRedactedCount += redactDesc.count;

            steps.add(_SliceStep(
              ordinal: steps.length + 1,
              kind: 'call',
              description: redactDesc.text,
              symbolPath: resolved.symbolPath,
              anchor: anchor,
            ));

            edges.add(_SliceEdge(
              kind: 'resolved_cross_file',
              toSymbolPath: resolved.fullEntryPath,
              resolutionStatus: 'resolved',
              depth: depth,
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
                toSymbolPath: '$relPath#$callTargetSym',
                resolutionStatus: 'unresolved_dynamic',
                depth: depth,
              ));
            }
          }
        }
      } else if (stmt.type == _StmtType.branch) {
        final redactDesc = _redactSecrets(_deriveBranchDescription(stmt.rawText));
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
        'byteRange': [0, fileContent.length],
        'fileHash': fileHash,
        'spanHash': fileHash,
        'enclosingSymbolPath': fullSym,
        'canonicalAstFingerprint': sha256Hex(''),
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
  final guardRe = RegExp(r'\bif\s*\(([^)]+)\)', multiLine: true);
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
  final catchRe = RegExp(
    r'\bon\s+([A-Za-z0-9_$]+)\s*(?:catch\s*\([^)]*\))?\s*\{|\bcatch\s*\([^)]*\)\s*\{',
    multiLine: true,
  );
  final varAssignRe = RegExp(
    r'\b(?:final|var|const|[A-Z][A-Za-z0-9_$]*)\s+([A-Za-z0-9_$]+)\s*=\s*([^;]+);',
    multiLine: true,
  );

  // Guards (if)
  final occupied = <List<int>>[];
  for (final m in guardRe.allMatches(bodySource)) {
    final cond = m.group(1)!.trim();
    occupied.add([m.start, m.end]);
    results.add(_ExtractedStmt(
      type: _StmtType.guard,
      startOffset: baseOffset + m.start,
      endOffset: baseOffset + m.end,
      rawText: m.group(0)!,
      guardCondition: cond,
    ));
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

    if (raw.startsWith('if') || raw.startsWith('emit') || raw.contains('copyWith')) {
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

String _deriveCallDescription(String target, {required bool isBoundary}) {
  if (isBoundary) {
    return '외부 서비스/저장소에 작업을 요청한다: $target';
  }
  return '유스케이스 실행으로 처리를 위임한다: $target';
}

String _deriveBranchDescription(String raw) {
  if (raw.contains('Exception') || raw.contains('catch')) {
    return '오류 발생 시 예외를 포착하고 대체 처리한다';
  }
  return '분기 처리를 수행한다';
}
