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
import 'package:analyzer/dart/ast/ast.dart';
import 'package:analyzer/dart/ast/visitor.dart';
import 'package:analyzer/dart/analysis/utilities.dart';

import 'analysis_tracker.dart';
import 'flow_context.dart';
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
    'unresolved_dynamic.${methodName.replaceAll(RegExp(r'[^A-Za-z0-9_.$-]'), '_')}';

// --- Slicing Data Structures ------------------------------------------------

class _SliceStep {
  _SliceStep({
    required this.ordinal,
    required this.kind,
    required this.description,
    required this.symbolPath,
    required this.anchor,
    this.invocationId,
    this.callerStepOrdinal,
    this.assignmentSourceOrdinal,
    this.valueRole,
    this.guardCondition,
    this.stateBefore,
    this.stateAfter,
    this.effectTarget,
    this.flowContext,
    this.guardThenEndOffset,
    this.guardElseEndOffset,
    this.shortCircuitCallStart,
    this.shortCircuitAssignmentStart,
    this.shortCircuitOperator,
    this.isLoopCondition = false,
  });

  int ordinal;
  final String kind;
  final String description;
  final String symbolPath;
  final Map<String, Object?> anchor;
  String? invocationId;
  int? callerStepOrdinal;
  final int? assignmentSourceOrdinal;
  final String? valueRole;
  final String? guardCondition;
  final String? stateBefore;
  final String? stateAfter;
  final String? effectTarget;
  final Map<String, Object?>? flowContext;
  final int? guardThenEndOffset;
  final int? guardElseEndOffset;
  final int? shortCircuitCallStart;
  final int? shortCircuitAssignmentStart;
  final String? shortCircuitOperator;
  final bool isLoopCondition;

  Map<String, Object?> toJson() {
    final map = <String, Object?>{
      'ordinal': ordinal,
      'kind': kind,
      'description': description,
      'symbolPath': symbolPath,
      'anchor': anchor,
    };
    if (invocationId != null) map['invocationId'] = invocationId;
    if (callerStepOrdinal != null) {
      map['callerStepOrdinal'] = callerStepOrdinal;
    }
    if (assignmentSourceOrdinal != null) {
      map['assignmentSourceOrdinal'] = assignmentSourceOrdinal;
    }
    if (valueRole != null) map['valueRole'] = valueRole;
    if (guardCondition != null) map['guardCondition'] = guardCondition;
    if (stateBefore != null) map['stateBefore'] = stateBefore;
    if (stateAfter != null) map['stateAfter'] = stateAfter;
    if (effectTarget != null) map['effectTarget'] = effectTarget;
    if (flowContext != null) map['flowContext'] = flowContext;
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
    this.targetStepOrdinal,
    this.conditions,
    this.unresolvedReason,
  });

  final String kind;
  final String toSymbolPath;
  final String resolutionStatus;
  final int depth;

  /// 1-based ordinal of the step that produced this edge, so FlowView can
  /// attach the delegation target to the right timeline card.
  final int stepOrdinal;
  final int? targetStepOrdinal;
  final List<Map<String, Object?>>? conditions;
  final String? unresolvedReason;

  Map<String, Object?> toJson() {
    final map = <String, Object?>{
      'kind': kind,
      'toSymbolPath': toSymbolPath,
      'resolutionStatus': resolutionStatus,
      'depth': depth,
      'stepOrdinal': stepOrdinal,
    };
    if (targetStepOrdinal != null) {
      map['targetStepOrdinal'] = targetStepOrdinal;
    }
    if (conditions != null && conditions!.isNotEmpty) {
      map['conditions'] = conditions;
    }
    if (unresolvedReason != null) {
      map['unresolvedReason'] = unresolvedReason;
    }
    return map;
  }
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
    this.tracker,
    Map<String, String>? packageDirs,
  }) : packageDirs = packageDirs ?? const {};

  final String repoRoot;
  final String packageName;
  final List<String> boundarySuffixes;
  final Map<String, String>? overlay;
  final AnalysisObservationTracker? tracker;

  /// Maps owning package directory ('' for the repo root package) onto the
  /// package name, derived from snapshot pubspecs in tracker/overlay mode.
  /// Empty in live-disk mode, where the filesystem resolver applies instead.
  final Map<String, String> packageDirs;

  /// Owning package of [repoRelPath]: nearest ancestor package directory,
  /// else the entry package.
  String ownerNameFor(String repoRelPath) {
    var bestDir = '';
    var bestName = '';
    packageDirs.forEach((dir, name) {
      if (dir.isEmpty) {
        if (bestDir.isEmpty && bestName.isEmpty) bestName = name;
        return;
      }
      final prefix = '$dir/';
      if ((repoRelPath == dir || repoRelPath.startsWith(prefix)) &&
          dir.length > bestDir.length) {
        bestDir = dir;
        bestName = name;
      }
    });
    if (bestName.isNotEmpty) return bestName;
    return packageName;
  }

  /// `lib/` prefix owning [repoRelPath], e.g. `packages/foo/lib/`.
  String libPrefixFor(String repoRelPath) {
    var bestDir = '';
    packageDirs.forEach((dir, _) {
      if (dir.isEmpty) return;
      final prefix = '$dir/';
      if ((repoRelPath == dir || repoRelPath.startsWith(prefix)) &&
          dir.length > bestDir.length) {
        bestDir = dir;
      }
    });
    return bestDir.isEmpty ? 'lib/' : '$bestDir/lib/';
  }

  /// Owner-aware import resolution. An import that cannot be mapped into the
  /// snapshot (SDK library, external package) is recorded as a genuine
  /// negative lookup: the analyzer attempted the resolution and the target
  /// is absent from the captured scope.
  String? resolveImport(
    String importUri,
    String currentRelPath,
    Map<String, String> workspacePackages,
  ) {
    final owner = ownerNameFor(currentRelPath);
    final resolved = _resolveImportPath(
      importUri,
      currentRelPath,
      owner,
      workspacePackages,
      ownerLibPrefix: libPrefixFor(currentRelPath),
    );
    if (resolved == null) {
      tracker?.recordMissing(
        importUri,
        selector: 'import $importUri from $currentRelPath',
      );
    }
    return resolved;
  }

  final Map<String, String> _fileCache = {};
  final Map<String, ScanResult> _scanCache = {};

  String? readFile(String repoRelPath) {
    if (_fileCache.containsKey(repoRelPath)) {
      return _fileCache[repoRelPath];
    }
    if (tracker != null) {
      final content = tracker!.read(repoRelPath);
      if (content != null) _fileCache[repoRelPath] = content;
      return content;
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
/// owning package of the importing file (use the resolver's [ownerNameFor]);
/// [ownerLibPrefix] is that package's `lib/` prefix (use [libPrefixFor]) so
/// same-package imports resolve inside nested workspace packages instead of
/// a root `lib/` that may not exist. [workspacePackages] maps every other
/// package name in a monorepo workspace onto its package root so
/// cross-package imports resolve too.
String? _resolveImportPath(
  String importUri,
  String currentRelPath,
  String packageName,
  Map<String, String> workspacePackages, {
  String ownerLibPrefix = 'lib/',
}) {
  if (importUri.startsWith('package:')) {
    final rest = importUri.substring('package:'.length);
    final slash = rest.indexOf('/');
    if (slash < 0) return null;
    final pkg = rest.substring(0, slash);
    final subpath = rest.substring(slash + 1);
    if (pkg == packageName) {
      return '$ownerLibPrefix$subpath';
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
  final m = RegExp(
    r'^ref\.(?:read|watch)\(\s*([A-Za-z0-9_$]+)\s*\)$',
  ).firstMatch(receiverOrFunc.trim());
  return m?.group(1);
}

String? _nominalReceiverType(TypeAnnotation? type) {
  final name = type?.toSource().trim();
  if (name == null || !RegExp(r'^[A-Z][A-Za-z0-9_$]*$').hasMatch(name)) {
    return null;
  }
  return name;
}

({Map<String, String?> parameters, Map<String, String> fields})
_receiverTypeFacts(
  SnapshotSyntax proof,
  ScannedMethod target,
  String className,
  String methodName,
) {
  final parameters = <String, String?>{};
  final fields = <String, String>{};
  if (!proof.valid) return (parameters: parameters, fields: fields);

  FormalParameterList? parameterList;
  if (className.isNotEmpty) {
    for (final declaration in proof.nodes.whereType<ClassDeclaration>()) {
      if (declaration.namePart.typeName.lexeme != className) continue;
      for (final member in declaration.body.members) {
        if (member is FieldDeclaration) {
          final type = _nominalReceiverType(member.fields.type);
          if (type != null) {
            for (final variable in member.fields.variables) {
              fields[variable.name.lexeme] = type;
            }
          }
        } else if (member is MethodDeclaration &&
            member.name.lexeme == methodName &&
            member.body.offset <= target.bodyStart &&
            member.body.end >= target.bodyEnd) {
          parameterList = member.parameters;
        }
      }
    }
  } else {
    for (final declaration in proof.nodes.whereType<FunctionDeclaration>()) {
      final body = declaration.functionExpression.body;
      if (declaration.name.lexeme == methodName &&
          body.offset <= target.bodyStart &&
          body.end >= target.bodyEnd) {
        parameterList = declaration.functionExpression.parameters;
      }
    }
  }
  for (final parameter in parameterList?.parameters ?? <FormalParameter>[]) {
    final name = parameter.name?.lexeme;
    if (name != null) parameters[name] = _nominalReceiverType(parameter.type);
  }
  return (parameters: parameters, fields: fields);
}

/// Attempts to resolve a called method on a receiver/function to a target file & method.
_ResolvedTarget? _resolveNominalReceiverTarget({
  required _ResolverContext ctx,
  required Map<String, String> workspacePackages,
  required String currentRelPath,
  required String currentContent,
  required ScanResult currentScan,
  required String className,
  required String methodName,
  bool requireStatic = false,
}) {
  _ResolvedTarget? findIn(ScanResult scan, String path) {
    final content = path == currentRelPath
        ? currentContent
        : ctx.readFile(path);
    if (content == null) return null;
    final syntax = parseString(content: content, throwIfDiagnostics: false);
    if (syntax.errors.isNotEmpty) return null;
    final declared = syntax.unit.declarations
        .whereType<ClassDeclaration>()
        .where(
          (declaration) => declaration.namePart.typeName.lexeme == className,
        )
        .expand((declaration) => declaration.body.members)
        .whereType<MethodDeclaration>()
        .any(
          (method) =>
              method.name.lexeme == methodName &&
              method.isStatic == requireStatic,
        );
    if (!declared) return null;
    for (final declaration in scan.classes) {
      if (declaration.name != className) continue;
      for (final method in declaration.methods) {
        if (method.name == methodName) {
          return _ResolvedTarget(
            repoRelativePath: path,
            className: className,
            methodName: methodName,
            method: method,
          );
        }
      }
    }
    return null;
  }

  final local = findIn(currentScan, currentRelPath);
  if (local != null) return local;
  final parsed = parseString(
    content: currentContent,
    throwIfDiagnostics: false,
  );
  if (parsed.errors.isNotEmpty) return null;
  final candidates = <String, _ResolvedTarget>{};
  for (final directive in parsed.unit.directives.whereType<ImportDirective>()) {
    if (directive.prefix != null) continue;
    if (directive.combinators.any(
      (combinator) =>
          combinator is HideCombinator &&
              combinator.hiddenNames.any((name) => name.name == className) ||
          combinator is ShowCombinator &&
              !combinator.shownNames.any((name) => name.name == className),
    )) {
      continue;
    }
    final uri = directive.uri.stringValue;
    if (uri == null) continue;
    final path = ctx.resolveImport(uri, currentRelPath, workspacePackages);
    if (path == null) continue;
    final scan = ctx.scanFile(path);
    if (scan == null) continue;
    final candidate = findIn(scan, path);
    if (candidate != null) candidates[path] = candidate;
  }
  return candidates.length == 1 ? candidates.values.single : null;
}

_ResolvedTarget? _resolveUnqualifiedTarget({
  required _ResolverContext ctx,
  required Map<String, String> workspacePackages,
  required String currentRelPath,
  required String currentContent,
  required ScanResult currentScan,
  required String currentClassName,
  required String methodName,
}) {
  for (final declaration in currentScan.classes) {
    if (declaration.name != currentClassName) continue;
    for (final method in declaration.methods) {
      if (method.name == methodName) {
        return _ResolvedTarget(
          repoRelativePath: currentRelPath,
          className: currentClassName,
          methodName: methodName,
          method: method,
        );
      }
    }
  }
  for (final function in currentScan.topLevelFunctions) {
    if (function.name == methodName) {
      return _ResolvedTarget(
        repoRelativePath: currentRelPath,
        className: '',
        methodName: methodName,
        method: function,
      );
    }
  }

  final parsed = parseString(
    content: currentContent,
    throwIfDiagnostics: false,
  );
  if (parsed.errors.isNotEmpty) return null;
  final candidates = <String, _ResolvedTarget>{};
  for (final directive in parsed.unit.directives.whereType<ImportDirective>()) {
    if (directive.prefix != null ||
        directive.combinators.any(
          (combinator) =>
              combinator is HideCombinator &&
                  combinator.hiddenNames.any(
                    (name) => name.name == methodName,
                  ) ||
              combinator is ShowCombinator &&
                  !combinator.shownNames.any((name) => name.name == methodName),
        )) {
      continue;
    }
    final uri = directive.uri.stringValue;
    if (uri == null) continue;
    final path = ctx.resolveImport(uri, currentRelPath, workspacePackages);
    if (path == null) continue;
    final scan = ctx.scanFile(path);
    if (scan == null) continue;
    for (final function in scan.topLevelFunctions) {
      if (function.name == methodName) {
        candidates[path] = _ResolvedTarget(
          repoRelativePath: path,
          className: '',
          methodName: methodName,
          method: function,
        );
      }
    }
  }
  return candidates.length == 1 ? candidates.values.single : null;
}

_ResolvedTarget? _resolveCallTarget({
  required _ResolverContext ctx,
  required Map<String, String> workspacePackages,
  required String currentRelPath,
  required String currentClassName,
  required String receiverOrFunc,
  required String methodName,
  required Map<String, String?> parameterTypes,
  required Map<String, String> fieldTypes,
}) {
  final currentContent = ctx.readFile(currentRelPath);
  if (currentContent == null) return null;
  final currentScan = ctx.scanFile(currentRelPath);
  if (currentScan == null) return null;

  if (receiverOrFunc == 'this') {
    for (final declaration in currentScan.classes) {
      if (declaration.name != currentClassName) continue;
      for (final method in declaration.methods) {
        if (method.name == methodName) {
          return _ResolvedTarget(
            repoRelativePath: currentRelPath,
            className: currentClassName,
            methodName: methodName,
            method: method,
          );
        }
      }
    }
    return null;
  }
  if (receiverOrFunc.isEmpty) {
    final fieldType = fieldTypes[methodName];
    if (fieldType != null) {
      return _resolveNominalReceiverTarget(
        ctx: ctx,
        workspacePackages: workspacePackages,
        currentRelPath: currentRelPath,
        currentContent: currentContent,
        currentScan: currentScan,
        className: fieldType,
        methodName: 'call',
      );
    }
    return _resolveUnqualifiedTarget(
      ctx: ctx,
      workspacePackages: workspacePackages,
      currentRelPath: currentRelPath,
      currentContent: currentContent,
      currentScan: currentScan,
      currentClassName: currentClassName,
      methodName: methodName,
    );
  }

  // 1. Check if receiver is a field in the current class or constructor parameter
  String? targetClassName;
  if (receiverOrFunc.isNotEmpty) {
    if (parameterTypes.containsKey(receiverOrFunc)) {
      targetClassName = parameterTypes[receiverOrFunc];
      if (targetClassName == null) return null;
      return _resolveNominalReceiverTarget(
        ctx: ctx,
        workspacePackages: workspacePackages,
        currentRelPath: currentRelPath,
        currentContent: currentContent,
        currentScan: currentScan,
        className: targetClassName,
        methodName: methodName,
      );
    } else {
      targetClassName = fieldTypes[receiverOrFunc];
      if (targetClassName != null) {
        return _resolveNominalReceiverTarget(
          ctx: ctx,
          workspacePackages: workspacePackages,
          currentRelPath: currentRelPath,
          currentContent: currentContent,
          currentScan: currentScan,
          className: targetClassName,
          methodName: methodName,
        );
      }
    }

    // Look for Provider / DI: `final fooProvider = Provider<FooService>(...)` or `ref.read(fooProvider)`.
    // The provider variable usually lives in the same file OR in an imported
    // domain file, so search the import closure too.
    if (targetClassName == null) {
      final cleanReceiver =
          _providerNameFromReceiver(receiverOrFunc) ??
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
          final resolvedPath = ctx.resolveImport(
            uri,
            currentRelPath,
            workspacePackages,
          );
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
  }

  // If targetClassName is still null, but receiver matches a ClassName pattern (PascalCase), it might be static method or constructor
  if (targetClassName == null &&
      receiverOrFunc.isNotEmpty &&
      receiverOrFunc[0].toUpperCase() == receiverOrFunc[0] &&
      !receiverOrFunc.startsWith('_')) {
    return _resolveNominalReceiverTarget(
      ctx: ctx,
      workspacePackages: workspacePackages,
      currentRelPath: currentRelPath,
      currentContent: currentContent,
      currentScan: currentScan,
      className: receiverOrFunc,
      methodName: methodName,
      requireStatic: true,
    );
  }

  // An imported class with the same method name does not identify the type
  // of an arbitrary receiver. Keep that call unresolved until its receiver
  // can be tied to a declaration in the snapshot.
  if (receiverOrFunc.isNotEmpty && targetClassName == null) return null;

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

  // 3. Search in imported files
  final importUris = _extractImports(currentContent);
  for (final uri in importUris) {
    final resolvedPath = ctx.resolveImport(
      uri,
      currentRelPath,
      workspacePackages,
    );
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
    for (final entity in topDir.listSync(
      followLinks: false,
    )..sort((a, b) => a.path.compareTo(b.path))) {
      if (entity is Directory) {
        scanPackageDir(entity.path);
      }
    }
  }
}

/// Snapshot-backed counterpart of [_collectWorkspacePackages]: derives the
/// same `<package name> -> <package dir>` map (plus [packageDirs]
/// `<package dir> -> <package name>`) from immutable overlay keys instead of
/// the adapter host disk, which is a disposable temp dir in production.
void _collectWorkspacePackagesFromOverlay(
  Map<String, String> overlay,
  Map<String, String> out, {
  Map<String, String>? packageDirs,
}) {
  final pubspecPaths =
      overlay.keys
          .where(
            (path) => path == 'pubspec.yaml' || path.endsWith('/pubspec.yaml'),
          )
          .toList()
        ..sort();
  for (final path in pubspecPaths) {
    final content = overlay[path];
    if (content == null) continue;
    for (final line in content.split('\n')) {
      final m = RegExp(r'^name:\s*([^\s#]+)').firstMatch(line.trimLeft());
      if (m != null) {
        final dir = path == 'pubspec.yaml'
            ? ''
            : path.substring(0, path.length - 13);
        out[m.group(1)!] = dir;
        packageDirs?[dir] = m.group(1)!;
        break;
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
  AnalysisObservationTracker? tracker,
  String? snapshotId,
}) {
  final effectiveSnapshotId =
      snapshotId ??
      (opts['snapshotId'] as String?) ??
      ((opts['snapshot'] as Map?)?['snapshotId'] as String?);
  final posixRoot = _toPosix(repoRoot);
  final pubspecFile = File('$posixRoot/pubspec.yaml');
  var packageName = '';
  final pubspecContent = tracker != null
      ? tracker.read('pubspec.yaml')
      : contentOverlay != null
      ? contentOverlay['pubspec.yaml']
      : (pubspecFile.existsSync() ? pubspecFile.readAsStringSync() : null);
  if (tracker != null && pubspecContent != null) {
    tracker.recordDependency('pubspec.yaml');
  }
  if (pubspecContent != null) {
    final match = RegExp(
      r'^name:\s*([a-zA-Z0-9_]+)',
      multiLine: true,
    ).firstMatch(pubspecContent);
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
  if (tracker != null &&
      !tracker
          .enumerateDartSourceFiles(libSubdir: '.')
          .contains(initialRelPath)) {
    throw ArgumentError(
      'Entry source file is outside the captured source set: $initialRelPath',
    );
  }

  // Monorepo workspace: map every sibling package's name onto its root so
  // `package:<pkg>/...` imports resolve across packages. The owning package
  // of the entry file wins via [packageName] above; when the entry lives in
  // a nested package (workspace layout), that package's name is used.
  var effectivePackageName = packageName;
  final workspacePackages = <String, String>{};
  final packageDirs = <String, String>{};
  if (contentOverlay == null && tracker?.overlay == null) {
    _collectWorkspacePackages(posixRoot, workspacePackages);
  } else {
    // Snapshot-backed runs have no live root: derive the package map from
    // the immutable overlay instead of the adapter host disk.
    final snapshotOverlay = tracker?.overlay ?? contentOverlay;
    if (snapshotOverlay != null) {
      _collectWorkspacePackagesFromOverlay(
        snapshotOverlay,
        workspacePackages,
        packageDirs: packageDirs,
      );
    }
  }
  for (final entry in workspacePackages.entries) {
    packageDirs.putIfAbsent(entry.value, () => entry.key);
  }
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
    tracker: tracker,
    packageDirs: packageDirs,
  );

  final steps = <_SliceStep>[];
  final edges = <_SliceEdge>[];
  final activeSymbols = <String>{};
  var truncated = false;
  var visitedCycleDetected = false;
  var totalRedactedCount = 0;
  var entryMethodFound = false;

  void addDirectCallEdge({
    required String targetPath,
    required int callerOrdinal,
    required int depth,
    required String resolvedKind,
    int? targetStepOrdinal,
  }) {
    if (targetStepOrdinal != null || resolvedKind == 'boundary_call') {
      edges.add(
        _SliceEdge(
          kind: resolvedKind,
          toSymbolPath: targetPath,
          resolutionStatus: 'resolved',
          depth: depth,
          stepOrdinal: callerOrdinal,
          targetStepOrdinal: targetStepOrdinal,
        ),
      );
      return;
    }
    final depthLimited = depth >= 5;
    final cycleLimited = activeSymbols.contains(targetPath);
    edges.add(
      _SliceEdge(
        kind: 'unknown_edge',
        toSymbolPath: targetPath,
        resolutionStatus: depthLimited || cycleLimited
            ? 'truncated'
            : 'unresolved_type',
        depth: depth,
        stepOrdinal: callerOrdinal,
        unresolvedReason: depthLimited
            ? '분석 깊이 한도 때문에 호출 대상의 실행 문장까지 확인하지 못했습니다.'
            : cycleLimited
            ? '재귀 호출이 현재 분석 경로에 있어 다음 실행 문장을 펼치지 못했습니다.'
            : '호출 대상의 실행 문장을 현재 소스에서 확인하지 못했습니다.',
      ),
    );
  }

  String initialClass = '';
  String initialMethod = initialSymbol;
  if (initialSymbol.contains('.')) {
    final dot = initialSymbol.indexOf('.');
    initialClass = initialSymbol.substring(0, dot);
    initialMethod = initialSymbol.substring(dot + 1);
  }

  // Recursive slice walker
  int? sliceMethodBody({
    required String relPath,
    required String className,
    required String methodName,
    required int depth,
    required String invocationId,
    int? callerStepOrdinal,
  }) {
    final fullSym = className.isNotEmpty
        ? '$className.$methodName'
        : methodName;
    final entryKey = '$relPath#$fullSym';

    if (activeSymbols.contains(entryKey)) {
      visitedCycleDetected = true;
      return null;
    }

    if (depth > 5) {
      truncated = true;
      return null;
    }

    final fileContent = ctx.readFile(relPath);
    if (fileContent == null) return null;
    final scan = ctx.scanFile(relPath);
    if (scan == null) return null;

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

    if (targetMethod == null) return null;
    activeSymbols.add(entryKey);
    if (depth == 1 &&
        relPath == initialRelPath &&
        className == initialClass &&
        methodName == initialMethod) {
      entryMethodFound = true;
    }

    final fileHash = sha256Hex(fileContent);
    final bodySource = fileContent.substring(
      targetMethod.bodyStart,
      targetMethod.bodyEnd,
    );
    final bodyStartOffset = targetMethod.bodyStart;

    // Symbol-scoped view range for FlowView: signature line through the end
    // of the method body. Presentation-only; never used for identity.
    final symbolRange = [
      _byteOffset(
        fileContent,
        _lineStartOffset(fileContent, targetMethod.nameLine),
      ),
      _byteOffset(fileContent, targetMethod.bodyEnd),
    ];

    final parsed = parseString(content: fileContent, throwIfDiagnostics: false);
    final proof = SnapshotSyntax(parsed.unit, valid: parsed.errors.isEmpty);
    final receiverTypes = _receiverTypeFacts(
      proof,
      targetMethod,
      className,
      methodName,
    );
    final bodylessPrimaryConstructors = <String, bool>{};
    bool isBodylessPrimaryConstructorCall(
      String name,
      int startOffset,
      int endOffset,
    ) {
      if (!proof.valid ||
          proof.nodes.whereType<FormalParameter>().any(
            (parameter) => parameter.name?.lexeme == name,
          ) ||
          proof.nodes.whereType<VariableDeclaration>().any(
            (declaration) => declaration.name.lexeme == name,
          ) ||
          !proof.nodes.whereType<MethodInvocation>().any(
            (invocation) =>
                invocation.offset == startOffset &&
                invocation.end == endOffset &&
                invocation.target == null &&
                invocation.methodName.name == name,
          )) {
        return false;
      }
      return bodylessPrimaryConstructors.putIfAbsent(name, () {
        final units = <CompilationUnit>[parsed.unit];
        for (final directive
            in parsed.unit.directives.whereType<ImportDirective>()) {
          if (directive.prefix != null ||
              directive.combinators.any(
                (combinator) =>
                    combinator is HideCombinator &&
                        combinator.hiddenNames.any(
                          (hidden) => hidden.name == name,
                        ) ||
                    combinator is ShowCombinator &&
                        !combinator.shownNames.any(
                          (shown) => shown.name == name,
                        ),
              )) {
            continue;
          }
          final uri = directive.uri.stringValue;
          if (uri == null) continue;
          final importedPath = ctx.resolveImport(
            uri,
            relPath,
            workspacePackages,
          );
          if (importedPath == null) continue;
          final importedContent = ctx.readFile(importedPath);
          if (importedContent == null) continue;
          final imported = parseString(
            content: importedContent,
            throwIfDiagnostics: false,
          );
          if (imported.errors.isEmpty) units.add(imported.unit);
        }
        var constructorCount = 0;
        for (final unit in units) {
          if (unit.declarations.whereType<FunctionDeclaration>().any(
            (declaration) => declaration.name.lexeme == name,
          )) {
            return false;
          }
          for (final declaration
              in unit.declarations.whereType<ClassDeclaration>()) {
            if (declaration.namePart.typeName.lexeme != name) continue;
            final namePart = declaration.namePart;
            if (namePart is! PrimaryConstructorDeclaration ||
                namePart.constKeyword == null ||
                namePart.constructorName != null ||
                namePart.body != null) {
              return false;
            }
            constructorCount++;
          }
        }
        return constructorCount == 1;
      });
    }

    final nestedFunctions = proof.nodes
        .whereType<FunctionExpression>()
        .where(
          (function) =>
              function.offset >= targetMethod!.bodyStart &&
              function.end <= targetMethod.bodyEnd,
        )
        .toList();
    final nestedFunctionRanges = [
      for (final function in nestedFunctions)
        (
          function: function,
          start: _byteOffset(fileContent, function.offset),
          end: _byteOffset(fileContent, function.end),
        ),
    ];
    final statementRanges = _StatementRangeCollector(fileContent);
    parsed.unit.accept(statementRanges);
    final executableStatements = _ExecutableStatementCollector(fileContent);
    parsed.unit.accept(executableStatements);
    final directValueCalls = _DirectValueCallCollector(fileContent);
    parsed.unit.accept(directValueCalls);
    bool hasCoreCollectionFactory(String typeName) {
      if (!proof.valid ||
          !proof.nodes.whereType<InstanceCreationExpression>().any(
            (creation) =>
                creation.constructorName.type.name.lexeme == typeName &&
                creation.constructorName.type.importPrefix == null &&
                creation.constructorName.name?.name == 'from',
          )) {
        return false;
      }
      bool shadowsCoreType(CompilationUnit unit) =>
          unit.declarations.whereType<ClassDeclaration>().any(
            (declaration) => declaration.namePart.typeName.lexeme == typeName,
          ) ||
          unit.declarations.whereType<EnumDeclaration>().any(
            (declaration) => declaration.namePart.typeName.lexeme == typeName,
          ) ||
          unit.declarations.whereType<ExtensionTypeDeclaration>().any(
            (declaration) => declaration.namePart.typeName.lexeme == typeName,
          ) ||
          unit.declarations.whereType<MixinDeclaration>().any(
            (declaration) => declaration.name.lexeme == typeName,
          ) ||
          unit.declarations.whereType<TypeAlias>().any(
            (declaration) => declaration.name.lexeme == typeName,
          ) ||
          unit.declarations.whereType<FunctionDeclaration>().any(
            (declaration) => declaration.name.lexeme == typeName,
          ) ||
          unit.declarations.whereType<TopLevelVariableDeclaration>().any(
            (declaration) => declaration.variables.variables.any(
              (variable) => variable.name.lexeme == typeName,
            ),
          );
      if (shadowsCoreType(parsed.unit) ||
          proof.nodes.whereType<FormalParameter>().any(
            (parameter) => parameter.name?.lexeme == typeName,
          ) ||
          proof.nodes.whereType<VariableDeclaration>().any(
            (variable) => variable.name.lexeme == typeName,
          )) {
        return false;
      }
      for (final directive in parsed.unit.directives) {
        if (directive is PartDirective) return false;
        if (directive is! ImportDirective || directive.prefix != null) continue;
        if (directive.combinators.any(
          (combinator) =>
              combinator is HideCombinator &&
                  combinator.hiddenNames.any(
                    (hidden) => hidden.name == typeName,
                  ) ||
              combinator is ShowCombinator &&
                  !combinator.shownNames.any((shown) => shown.name == typeName),
        )) {
          continue;
        }
        final uri = directive.uri.stringValue;
        if (uri == null) return false;
        if (uri.startsWith('dart:')) continue;
        final importedPath = ctx.resolveImport(uri, relPath, workspacePackages);
        if (importedPath == null) return false;
        final importedContent = ctx.readFile(importedPath);
        if (importedContent == null) return false;
        final imported = parseString(
          content: importedContent,
          throwIfDiagnostics: false,
        );
        if (imported.errors.isNotEmpty ||
            shadowsCoreType(imported.unit) ||
            imported.unit.directives.whereType<ExportDirective>().isNotEmpty) {
          return false;
        }
      }
      return true;
    }

    final localCollections = _LocalCollectionCollector(
      coreCollectionFactoryNames: {
        for (final typeName in const ['List', 'Set'])
          if (hasCoreCollectionFactory(typeName)) typeName,
      },
    );
    parsed.unit.accept(localCollections);
    final loopSyntax = _LoopSyntaxCollector(fileContent);
    parsed.unit.accept(loopSyntax);
    if (loopSyntax.unsupportedControls.any(
      (node) =>
          node.offset >= targetMethod!.bodyStart &&
          node.end <= targetMethod.bodyEnd &&
          !nestedFunctions.any(
            (function) =>
                node.offset >= function.offset && node.end <= function.end,
          ),
    )) {
      throw ArgumentError('entry_symbol_unsupported_control_flow: $entryKey');
    }

    // Slice statements in body
    final stmtList = _extractStatements(
      bodySource,
      bodyStartOffset,
      statementRanges.conditionalRanges,
      statementRanges.expressionEnds,
      statementRanges.expressionCalls,
      executableStatements.statements
          .where(
            (statement) =>
                statement.startOffset >= targetMethod!.bodyStart &&
                statement.endOffset <= targetMethod.bodyEnd,
          )
          .toList(),
    );
    for (final body in proof.nodes.whereType<ExpressionFunctionBody>()) {
      final expression = body.expression;
      if (expression.offset < targetMethod.bodyStart ||
          expression.end > targetMethod.bodyEnd ||
          fileContent
              .substring(targetMethod.bodyStart, expression.offset)
              .trim()
              .isNotEmpty ||
          fileContent
              .substring(expression.end, targetMethod.bodyEnd)
              .trim()
              .isNotEmpty ||
          !_isDirectReturnExpression(
            expression,
            _directReturnParameterNames(body),
            _directReturnNumericParameterNames(body),
          )) {
        continue;
      }
      final value = fileContent.substring(expression.offset, expression.end);
      stmtList.add(
        _ExtractedStmt(
          type: _StmtType.returnStmt,
          startOffset: expression.offset,
          endOffset: expression.end,
          rawText: value,
          guardCondition: value,
        ),
      );
    }
    for (final function in nestedFunctions) {
      AstNode? argument = function.parent;
      if (argument is NamedArgument) argument = argument.parent;
      if (argument is! ArgumentList ||
          function.body is! ExpressionFunctionBody) {
        continue;
      }
      final expression = (function.body as ExpressionFunctionBody).expression;
      if (stmtList.any(
        (statement) =>
            statement.startOffset >= expression.offset &&
            statement.endOffset <= expression.end,
      )) {
        continue;
      }
      final value = fileContent.substring(expression.offset, expression.end);
      stmtList.add(
        _ExtractedStmt(
          type: _StmtType.returnStmt,
          startOffset: expression.offset,
          endOffset: expression.end,
          rawText: value,
          guardCondition: value,
        ),
      );
    }
    for (final argumentCall in directValueCalls.argumentCalls) {
      if (argumentCall.startOffset < targetMethod.bodyStart ||
          argumentCall.endOffset > targetMethod.bodyEnd) {
        continue;
      }
      final enclosingStatement = statementRanges.expressionCalls.entries
          .where(
            (entry) =>
                entry.key < argumentCall.startOffset &&
                entry.value.endOffset >= argumentCall.endOffset,
          )
          .firstOrNull;
      if (enclosingStatement == null) continue;
      stmtList.add(
        _ExtractedStmt(
          type: _StmtType.call,
          startOffset: argumentCall.startOffset,
          endOffset: argumentCall.endOffset,
          rawText: fileContent.substring(
            argumentCall.startOffset,
            argumentCall.endOffset,
          ),
          callReceiver: argumentCall.receiver,
          callMethod: argumentCall.method,
          isEmbeddedCall: true,
        ),
      );
    }
    for (final operandCall in directValueCalls.eagerOperandCalls) {
      if (operandCall.startOffset < targetMethod.bodyStart ||
          operandCall.endOffset > targetMethod.bodyEnd ||
          !stmtList.any(
            (statement) =>
                statement.type == _StmtType.mutation &&
                statement.startOffset < operandCall.startOffset &&
                statement.endOffset >= operandCall.endOffset,
          )) {
        continue;
      }
      stmtList.add(
        _ExtractedStmt(
          type: _StmtType.call,
          startOffset: operandCall.startOffset,
          endOffset: operandCall.endOffset,
          rawText: fileContent.substring(
            operandCall.startOffset,
            operandCall.endOffset,
          ),
          callReceiver: operandCall.receiver,
          callMethod: operandCall.method,
          isEmbeddedCall: true,
        ),
      );
    }
    for (final conditional in directValueCalls.shortCircuitValues) {
      final expression = conditional.expression;
      final mutation = stmtList
          .where(
            (statement) =>
                statement.type == _StmtType.mutation &&
                statement.startOffset < expression.offset &&
                statement.endOffset >= expression.end,
          )
          .firstOrNull;
      if (mutation == null ||
          expression.offset < targetMethod.bodyStart ||
          expression.end > targetMethod.bodyEnd) {
        continue;
      }
      final left = expression.leftOperand;
      stmtList.add(
        _ExtractedStmt(
          type: _StmtType.guard,
          startOffset: left.offset,
          endOffset: left.end,
          rawText: fileContent.substring(left.offset, left.end),
          guardCondition: fileContent.substring(left.offset, left.end),
          shortCircuitCallStart: conditional.call.startOffset,
          shortCircuitAssignmentStart: mutation.startOffset,
          shortCircuitOperator: conditional.operator,
        ),
      );
      stmtList.add(
        _ExtractedStmt(
          type: _StmtType.call,
          startOffset: conditional.call.startOffset,
          endOffset: conditional.call.endOffset,
          rawText: fileContent.substring(
            conditional.call.startOffset,
            conditional.call.endOffset,
          ),
          callReceiver: conditional.call.receiver,
          callMethod: conditional.call.method,
          isEmbeddedCall: true,
        ),
      );
    }
    stmtList.addAll(
      loopSyntax.statementsWithin(targetMethod.bodyStart, targetMethod.bodyEnd),
    );
    stmtList.sort((a, b) {
      final aEmbedded = a.isEmbeddedCall || a.shortCircuitOperator != null;
      final bEmbedded = b.isEmbeddedCall || b.shortCircuitOperator != null;
      if (aEmbedded &&
          (b.type == _StmtType.call || b.type == _StmtType.mutation) &&
          b.startOffset < a.startOffset &&
          b.endOffset >= a.endOffset) {
        return -1;
      }
      if (bEmbedded &&
          (a.type == _StmtType.call || a.type == _StmtType.mutation) &&
          a.startOffset < b.startOffset &&
          a.endOffset >= b.endOffset) {
        return 1;
      }
      return a.startOffset.compareTo(b.startOffset);
    });
    final localSteps = <_SliceStep>[];
    int? firstLocalStepOrdinal;
    FunctionExpression? nestedFunctionForStep(_SliceStep step) {
      final byteRange = step.anchor['byteRange'] as List;
      final start = byteRange[0] as int;
      final end = byteRange[1] as int;
      final enclosing =
          nestedFunctionRanges
              .where((range) => start >= range.start && end <= range.end)
              .toList()
            ..sort((a, b) => (a.end - a.start).compareTo(b.end - b.start));
      return enclosing.firstOrNull?.function;
    }

    void addLocalStep(_SliceStep step) {
      steps.add(step);
      localSteps.add(step);
      final function = nestedFunctionForStep(step);
      if (function != null) {
        step.invocationId = sha256Hex(
          jsonEncode([invocationId, relPath, function.offset, function.end]),
        );
        step.callerStepOrdinal = null;
      } else {
        firstLocalStepOrdinal ??= step.ordinal;
      }
    }

    for (final stmt in stmtList) {
      // Only real source tokens may enter the semantic ledger. Comments and
      // string contents are never executable candidates.
      if (!proof.isCodeRange(stmt.startOffset, stmt.endOffset)) continue;
      final spanBytes = fileContent.substring(stmt.startOffset, stmt.endOffset);
      final spanHash = sha256Hex(spanBytes);
      final canonicalAst = _canonicalFingerprint(spanBytes);

      // Check redactions in the raw statement span
      final redactSpan = _redactSecrets(spanBytes);
      totalRedactedCount += redactSpan.count;

      final flowContextMeta = proof.contextFor(
        source: fileContent,
        path: relPath,
        snapshotId: effectiveSnapshotId,
        sourceHash: fileHash,
        start: stmt.startOffset,
        end: stmt.endOffset,
      );

      final anchor = <String, Object?>{
        'repoRelativePath': relPath,
        'byteRange': [
          _byteOffset(fileContent, stmt.startOffset),
          _byteOffset(fileContent, stmt.endOffset),
        ],
        'fileHash': fileHash,
        'spanHash': spanHash,
        'enclosingSymbolPath': fullSym,
        'canonicalAstFingerprint': canonicalAst,
        'symbolRange': symbolRange,
      };

      // Guard check
      if (stmt.type == _StmtType.loopCondition) {
        final condition = _redactSecrets(stmt.guardCondition ?? stmt.rawText);
        totalRedactedCount += condition.count;
        addLocalStep(
          _SliceStep(
            ordinal: steps.length + 1,
            kind: 'branch',
            description: '반복 조건을 확인한다: ${condition.text}',
            symbolPath: fullSym,
            anchor: anchor,
            invocationId: invocationId,
            callerStepOrdinal: callerStepOrdinal,
            guardCondition: condition.text,
            flowContext: flowContextMeta,
            isLoopCondition: true,
          ),
        );
      } else if (stmt.type == _StmtType.loopUpdate) {
        final update = _redactSecrets(stmt.rawText);
        totalRedactedCount += update.count;
        addLocalStep(
          _SliceStep(
            ordinal: steps.length + 1,
            kind: 'mutation',
            description: '반복 값을 갱신한다: ${update.text}',
            symbolPath: fullSym,
            anchor: anchor,
            invocationId: invocationId,
            callerStepOrdinal: callerStepOrdinal,
            flowContext: flowContextMeta,
          ),
        );
      } else if (stmt.type == _StmtType.breakStmt ||
          stmt.type == _StmtType.continueStmt) {
        addLocalStep(
          _SliceStep(
            ordinal: steps.length + 1,
            kind: stmt.type == _StmtType.breakStmt ? 'break' : 'continue',
            description: stmt.type == _StmtType.breakStmt
                ? '반복을 종료한다'
                : '다음 반복으로 진행한다',
            symbolPath: fullSym,
            anchor: anchor,
            invocationId: invocationId,
            callerStepOrdinal: callerStepOrdinal,
            flowContext: flowContextMeta,
          ),
        );
      } else if (stmt.type == _StmtType.guard) {
        final rawCond = stmt.guardCondition ?? '';
        final redactCond = _redactSecrets(rawCond);
        totalRedactedCount += redactCond.count;

        var desc = _deriveGuardDescription(redactCond.text);
        final redactDesc = _redactSecrets(desc);
        totalRedactedCount += redactDesc.count;

        addLocalStep(
          _SliceStep(
            ordinal: steps.length + 1,
            kind: 'guard',
            description: redactDesc.text,
            symbolPath: fullSym,
            anchor: anchor,
            invocationId: invocationId,
            callerStepOrdinal: callerStepOrdinal,
            guardCondition: redactCond.text,
            flowContext: flowContextMeta,
            guardThenEndOffset: stmt.guardThenEnd == null
                ? null
                : _byteOffset(fileContent, stmt.guardThenEnd!),
            guardElseEndOffset: stmt.guardElseEnd == null
                ? null
                : _byteOffset(fileContent, stmt.guardElseEnd!),
            shortCircuitCallStart: stmt.shortCircuitCallStart == null
                ? null
                : _byteOffset(fileContent, stmt.shortCircuitCallStart!),
            shortCircuitAssignmentStart:
                stmt.shortCircuitAssignmentStart == null
                ? null
                : _byteOffset(fileContent, stmt.shortCircuitAssignmentStart!),
            shortCircuitOperator: stmt.shortCircuitOperator,
          ),
        );
      } else if (stmt.type == _StmtType.mutation) {
        int? assignmentSourceOrdinal;
        final valueCall = directValueCalls.within(
          stmt.startOffset,
          stmt.endOffset,
        );
        if (valueCall != null && !_defaultDenylist.contains(valueCall.method)) {
          final resolved = _resolveCallTarget(
            ctx: ctx,
            workspacePackages: workspacePackages,
            currentRelPath: relPath,
            currentClassName: className,
            receiverOrFunc: valueCall.receiver,
            methodName: valueCall.method,
            parameterTypes: receiverTypes.parameters,
            fieldTypes: receiverTypes.fields,
          );
          if (resolved != null &&
              !_isBoundarySymbol(resolved.symbolPath, boundarySuffixes)) {
            final callSpan = fileContent.substring(
              valueCall.startOffset,
              valueCall.endOffset,
            );
            final callAnchor = <String, Object?>{
              ...anchor,
              'byteRange': [
                _byteOffset(fileContent, valueCall.startOffset),
                _byteOffset(fileContent, valueCall.endOffset),
              ],
              'spanHash': sha256Hex(callSpan),
              'canonicalAstFingerprint': _canonicalFingerprint(callSpan),
            };
            final callDescription = _redactSecrets(
              _deriveCallDescription(resolved.symbolPath, isBoundary: false),
            );
            totalRedactedCount += callDescription.count;
            addLocalStep(
              _SliceStep(
                ordinal: steps.length + 1,
                kind: 'call',
                description: callDescription.text,
                symbolPath: fullSym,
                anchor: callAnchor,
                invocationId: invocationId,
                callerStepOrdinal: callerStepOrdinal,
                flowContext: proof.contextFor(
                  source: fileContent,
                  path: relPath,
                  snapshotId: effectiveSnapshotId,
                  sourceHash: fileHash,
                  start: valueCall.startOffset,
                  end: valueCall.endOffset,
                ),
              ),
            );
            final callOrdinal = localSteps.last.ordinal;
            assignmentSourceOrdinal = callOrdinal;
            int? targetStepOrdinal;
            if (depth < 5) {
              targetStepOrdinal = sliceMethodBody(
                relPath: resolved.repoRelativePath,
                className: resolved.className,
                methodName: resolved.methodName,
                depth: depth + 1,
                invocationId: sha256Hex(
                  jsonEncode([
                    invocationId,
                    relPath,
                    valueCall.startOffset,
                    valueCall.endOffset,
                    resolved.fullEntryPath,
                  ]),
                ),
                callerStepOrdinal: callOrdinal,
              );
            } else {
              truncated = true;
            }
            addDirectCallEdge(
              targetPath: resolved.fullEntryPath,
              callerOrdinal: callOrdinal,
              depth: depth,
              resolvedKind: 'resolved_cross_file',
              targetStepOrdinal: targetStepOrdinal,
            );
          }
        }
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

        addLocalStep(
          _SliceStep(
            ordinal: steps.length + 1,
            kind: 'mutation',
            description: redactDesc.text,
            symbolPath: fullSym,
            anchor: anchor,
            invocationId: invocationId,
            callerStepOrdinal: callerStepOrdinal,
            assignmentSourceOrdinal: assignmentSourceOrdinal,
            valueRole: opts['includeValueRoles'] == true
                ? stmt.valueRole
                : null,
            stateBefore: finalBefore,
            stateAfter: finalAfter,
            flowContext: flowContextMeta,
          ),
        );
      } else if (stmt.type == _StmtType.call) {
        final receiver = stmt.callReceiver ?? '';
        final calledMethod = stmt.callMethod ?? '';

        if (_defaultDenylist.contains(calledMethod) ||
            _defaultDenylist.contains(receiver)) {
          continue;
        }

        if (localCollections.proves(receiver, stmt.startOffset)) {
          if (calledMethod == 'contains') continue;
          if (const {
            'add',
            'addAll',
            'remove',
            'removeWhere',
          }.contains(calledMethod)) {
            final description = _redactSecrets(
              '컬렉션을 갱신한다: $receiver.$calledMethod',
            );
            totalRedactedCount += description.count;
            addLocalStep(
              _SliceStep(
                ordinal: steps.length + 1,
                kind: 'mutation',
                description: description.text,
                symbolPath: fullSym,
                anchor: anchor,
                invocationId: invocationId,
                callerStepOrdinal: callerStepOrdinal,
                stateAfter: receiver,
                flowContext: flowContextMeta,
              ),
            );
            continue;
          }
        }

        final callTargetSym = receiver.isNotEmpty
            ? '$receiver.$calledMethod'
            : calledMethod;

        // Determine receiver type if receiver is a variable or provider
        String? receiverType;
        if (receiver.isNotEmpty && receiver != 'this') {
          receiverType = receiverTypes.parameters.containsKey(receiver)
              ? receiverTypes.parameters[receiver]
              : receiverTypes.fields[receiver];
          if (receiverType == null &&
              !receiverTypes.parameters.containsKey(receiver)) {
            final cleanReceiver =
                _providerNameFromReceiver(receiver) ??
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
                final resolvedPath = ctx.resolveImport(
                  uri,
                  relPath,
                  workspacePackages,
                );
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

        final isBoundary =
            _isBoundarySymbol(callTargetSym, boundarySuffixes) ||
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
          parameterTypes: receiverTypes.parameters,
          fieldTypes: receiverTypes.fields,
        );

        if (resolved != null) {
          final isResolvedBoundary =
              isBoundary ||
              _isBoundarySymbol(resolved.symbolPath, boundarySuffixes) ||
              _isBoundarySymbol(resolved.className, boundarySuffixes);
          final edgeKind = isResolvedBoundary
              ? 'boundary_call'
              : 'resolved_cross_file';
          final redactDesc = _redactSecrets(
            _deriveCallDescription(
              resolved.symbolPath,
              isBoundary: isResolvedBoundary,
            ),
          );
          totalRedactedCount += redactDesc.count;

          addLocalStep(
            _SliceStep(
              ordinal: steps.length + 1,
              kind: stmt.isAwait ? 'await' : 'call',
              description: redactDesc.text,
              symbolPath: fullSym,
              anchor: anchor,
              invocationId: invocationId,
              callerStepOrdinal: callerStepOrdinal,
              effectTarget: isResolvedBoundary ? resolved.symbolPath : null,
              flowContext: flowContextMeta,
            ),
          );

          final callerOrdinal = localSteps.last.ordinal;
          int? targetStepOrdinal;

          // Traverse target as a distinct invocation before recording a direct
          // call edge. A second syntactic call receives a second invocation ID.
          if (depth < 5) {
            targetStepOrdinal = sliceMethodBody(
              relPath: resolved.repoRelativePath,
              className: resolved.className,
              methodName: resolved.methodName,
              depth: depth + 1,
              invocationId: sha256Hex(
                jsonEncode([
                  invocationId,
                  relPath,
                  stmt.startOffset,
                  stmt.endOffset,
                  resolved.fullEntryPath,
                ]),
              ),
              callerStepOrdinal: callerOrdinal,
            );
          } else {
            truncated = true;
          }
          addDirectCallEdge(
            targetPath: resolved.fullEntryPath,
            callerOrdinal: callerOrdinal,
            depth: depth,
            resolvedKind: edgeKind,
            targetStepOrdinal: targetStepOrdinal,
          );
        } else {
          // Unresolved dynamic call / unknown edge
          if (calledMethod.isNotEmpty &&
              !_defaultDenylist.contains(calledMethod)) {
            if (receiver.isEmpty &&
                isBodylessPrimaryConstructorCall(
                  calledMethod,
                  stmt.startOffset,
                  stmt.endOffset,
                )) {
              final redactDesc = _redactSecrets(
                _deriveCallDescription(callTargetSym, isBoundary: false),
              );
              totalRedactedCount += redactDesc.count;
              addLocalStep(
                _SliceStep(
                  ordinal: steps.length + 1,
                  kind: 'call',
                  description: redactDesc.text,
                  symbolPath: fullSym,
                  anchor: anchor,
                  invocationId: invocationId,
                  callerStepOrdinal: callerStepOrdinal,
                  flowContext: flowContextMeta,
                ),
              );
              continue;
            }
            final redactDesc = _redactSecrets(
              _deriveCallDescription(callTargetSym, isBoundary: false),
            );
            totalRedactedCount += redactDesc.count;

            addLocalStep(
              _SliceStep(
                ordinal: steps.length + 1,
                kind: 'call',
                description: redactDesc.text,
                symbolPath: fullSym,
                anchor: anchor,
                invocationId: invocationId,
                callerStepOrdinal: callerStepOrdinal,
                flowContext: flowContextMeta,
              ),
            );

            edges.add(
              _SliceEdge(
                kind: 'unknown_edge',
                toSymbolPath:
                    '$relPath#${_unresolvedDynamicSymbol(calledMethod)}',
                resolutionStatus: receiverType == null
                    ? 'unresolved_dynamic'
                    : 'unresolved_type',
                depth: depth,
                stepOrdinal: localSteps.last.ordinal,
                unresolvedReason: receiverType == null
                    ? '호출 대상의 타입을 현재 소스에서 확인하지 못했습니다.'
                    : '선언된 타입의 호출 대상을 현재 소스에서 하나로 확인하지 못했습니다.',
              ),
            );
          }
        }
      } else if (stmt.type == _StmtType.branch) {
        final redactDesc = _redactSecrets(
          _deriveBranchDescription(stmt.rawText),
        );
        totalRedactedCount += redactDesc.count;

        addLocalStep(
          _SliceStep(
            ordinal: steps.length + 1,
            kind: 'branch',
            description: redactDesc.text,
            symbolPath: fullSym,
            anchor: anchor,
            invocationId: invocationId,
            callerStepOrdinal: callerStepOrdinal,
            flowContext: flowContextMeta,
          ),
        );
      } else if (stmt.type == _StmtType.returnStmt) {
        final retVal = stmt.guardCondition;
        final desc =
            (retVal != null &&
                retVal.isNotEmpty &&
                !retVal.contains('\n') &&
                retVal.length <= 30 &&
                !retVal.contains('('))
            ? '결과를 반환한다: $retVal'
            : '실행을 종료하고 반환한다';
        final redactDesc = _redactSecrets(desc);
        totalRedactedCount += redactDesc.count;

        addLocalStep(
          _SliceStep(
            ordinal: steps.length + 1,
            kind: 'return',
            description: redactDesc.text,
            symbolPath: fullSym,
            anchor: anchor,
            invocationId: invocationId,
            callerStepOrdinal: callerStepOrdinal,
            flowContext: flowContextMeta,
          ),
        );
      }
    }
    for (final function in nestedFunctions) {
      AstNode? argument = function.parent;
      if (argument is NamedArgument) argument = argument.parent;
      if (argument is! ArgumentList) continue;
      final functionStart = _byteOffset(fileContent, function.offset);
      final functionEnd = _byteOffset(fileContent, function.end);
      final callbackInvocationId = sha256Hex(
        jsonEncode([invocationId, relPath, function.offset, function.end]),
      );
      final enclosing =
          localSteps.where((step) {
            final range = step.anchor['byteRange'] as List;
            return step.invocationId != callbackInvocationId &&
                (range[0] as int) <= functionStart &&
                (range[1] as int) >= functionEnd;
          }).toList()..sort((a, b) {
            final aRange = a.anchor['byteRange'] as List;
            final bRange = b.anchor['byteRange'] as List;
            return ((aRange[1] as int) - (aRange[0] as int)).compareTo(
              (bRange[1] as int) - (bRange[0] as int),
            );
          });
      if (enclosing.isEmpty) continue;
      if (opts['includeCallbackRelations'] == true) {
        final callbackSteps =
            localSteps
                .where((step) => step.invocationId == callbackInvocationId)
                .toList()
              ..sort((a, b) => a.ordinal.compareTo(b.ordinal));
        if (callbackSteps.isNotEmpty) {
          edges.add(
            _SliceEdge(
              kind: 'callback_argument',
              toSymbolPath: '$relPath#$fullSym',
              resolutionStatus: 'resolved',
              depth: depth,
              stepOrdinal: enclosing.first.ordinal,
              targetStepOrdinal: callbackSteps.first.ordinal,
            ),
          );
        }
      }
      edges.add(
        _SliceEdge(
          kind: 'unknown_edge',
          toSymbolPath:
              '$relPath#unresolved_dynamic.callback_${function.offset}',
          resolutionStatus: 'unresolved_dynamic',
          depth: depth,
          stepOrdinal: enclosing.first.ordinal,
          unresolvedReason: '콜백을 전달한 코드는 확인했지만 실제 호출 시점과 실행 여부는 확인되지 않았습니다.',
        ),
      );
    }
    // A nested function has its own execution. Keep its source steps in the
    // payload, but do not connect them to the enclosing method's sequence.
    localSteps.removeWhere((step) => nestedFunctionForStep(step) != null);
    final simpleSwitches = executableStatements.simpleSwitches
        .where(
          (syntax) =>
              syntax.start >= targetMethod!.bodyStart &&
              syntax.end <= targetMethod.bodyEnd &&
              !nestedFunctions.any(
                (function) =>
                    syntax.start >= function.offset &&
                    syntax.end <= function.end,
              ),
        )
        .toList();
    final switchCaseStarts = {
      for (final syntax in simpleSwitches)
        for (final member in syntax.cases)
          _byteOffset(fileContent, member.labelStart),
    };
    int afterEnclosingAlternative(
      _SliceStep step,
      int stepIndex,
      int nextIndex,
    ) {
      if (nextIndex >= localSteps.length) return nextIndex;
      final currentStart = (step.anchor['byteRange'] as List)[0] as int;
      final nextStart =
          (localSteps[nextIndex].anchor['byteRange'] as List)[0] as int;
      var alternativeEnd = 0;
      for (final guard in localSteps.take(stepIndex)) {
        final thenEnd = guard.guardThenEndOffset;
        final elseEnd = guard.guardElseEndOffset;
        final guardEnd = (guard.anchor['byteRange'] as List)[1] as int;
        if (thenEnd != null &&
            elseEnd != null &&
            currentStart >= guardEnd &&
            currentStart < thenEnd &&
            nextStart >= thenEnd &&
            nextStart < elseEnd &&
            elseEnd > alternativeEnd) {
          alternativeEnd = elseEnd;
        }
      }
      if (alternativeEnd > 0) {
        while (nextIndex < localSteps.length &&
            ((localSteps[nextIndex].anchor['byteRange'] as List)[0] as int) <
                alternativeEnd) {
          nextIndex++;
        }
      }
      return nextIndex;
    }

    for (var i = 0; i < localSteps.length; i++) {
      final step = localSteps[i];
      if ((step.kind == 'guard' || step.kind == 'branch') &&
          !step.isLoopCondition) {
        if (switchCaseStarts.contains((step.anchor['byteRange'] as List)[0])) {
          continue;
        }
        final isThrow =
            step.guardCondition != null &&
            step.guardCondition!.contains('실패 시 예외 발생');
        if (isThrow) {
          continue;
        }

        if (step.shortCircuitOperator != null) {
          final call = localSteps
              .where(
                (candidate) =>
                    candidate.kind == 'call' &&
                    (candidate.anchor['byteRange'] as List)[0] ==
                        step.shortCircuitCallStart,
              )
              .firstOrNull;
          final assignment = localSteps
              .where(
                (candidate) =>
                    candidate.kind == 'mutation' &&
                    (candidate.anchor['byteRange'] as List)[0] ==
                        step.shortCircuitAssignmentStart,
              )
              .firstOrNull;
          if (call == null || assignment == null) {
            throw ArgumentError(
              'entry_symbol_unsupported_control_flow: $entryKey',
            );
          }
          final executeOutcome = switch (step.shortCircuitOperator) {
            '&&' => 'truthy',
            '||' => 'falsy',
            '??' => 'nullish',
            _ => throw ArgumentError('unsupported short-circuit operator'),
          };
          final skipOutcome = switch (step.shortCircuitOperator) {
            '&&' => 'falsy',
            '||' => 'truthy',
            '??' => 'non_nullish',
            _ => throw ArgumentError('unsupported short-circuit operator'),
          };
          for (final (target, outcome) in [
            (call, executeOutcome),
            (assignment, skipOutcome),
          ]) {
            edges.add(
              _SliceEdge(
                kind: 'control_flow',
                toSymbolPath: '$relPath#$fullSym',
                resolutionStatus: 'resolved',
                depth: depth,
                stepOrdinal: step.ordinal,
                targetStepOrdinal: target.ordinal,
                conditions: [
                  {'stepOrdinal': step.ordinal, 'outcome': outcome},
                ],
              ),
            );
          }
          continue;
        }

        final thenEndOffset = step.guardThenEndOffset;
        final elseEndOffset = step.guardElseEndOffset;

        _SliceStep? thenStep;
        _SliceStep? elseStep;
        _SliceStep? afterStep;

        for (var j = i + 1; j < localSteps.length; j++) {
          final candidate = localSteps[j];
          final cStart = (candidate.anchor['byteRange'] as List)[0] as int;
          if (thenEndOffset != null) {
            if (cStart < thenEndOffset) {
              thenStep ??= candidate;
            } else if (elseEndOffset != null && cStart < elseEndOffset) {
              elseStep ??= candidate;
            } else {
              afterStep ??= candidate;
              break;
            }
          } else {
            thenStep ??= candidate;
            if (j == i + 2) {
              afterStep ??= candidate;
              break;
            }
          }
        }
        if (afterStep != null) {
          final afterIndex = afterEnclosingAlternative(
            step,
            i,
            localSteps.indexOf(afterStep),
          );
          afterStep = afterIndex < localSteps.length
              ? localSteps[afterIndex]
              : null;
        }

        final truthyTarget = thenStep ?? afterStep;
        if (truthyTarget != null) {
          edges.add(
            _SliceEdge(
              kind: 'control_flow',
              toSymbolPath: '$relPath#$fullSym',
              resolutionStatus: 'resolved',
              depth: depth,
              stepOrdinal: step.ordinal,
              targetStepOrdinal: truthyTarget.ordinal,
              conditions: [
                {'stepOrdinal': step.ordinal, 'outcome': 'truthy'},
              ],
            ),
          );
        }
        final falsyTarget = elseStep ?? afterStep;
        if (falsyTarget != null) {
          edges.add(
            _SliceEdge(
              kind: 'control_flow',
              toSymbolPath: '$relPath#$fullSym',
              resolutionStatus: 'resolved',
              depth: depth,
              stepOrdinal: step.ordinal,
              targetStepOrdinal: falsyTarget.ordinal,
              conditions: [
                {'stepOrdinal': step.ordinal, 'outcome': 'falsy'},
              ],
            ),
          );
        }
      } else if (!step.isLoopCondition &&
          step.kind != 'break' &&
          step.kind != 'continue' &&
          step.kind != 'await' &&
          step.kind != 'return' &&
          step.kind != 'throw' &&
          !step.description.contains('예외 발생') &&
          (step.guardCondition == null ||
              !step.guardCondition!.contains('예외 발생'))) {
        if (i + 1 < localSteps.length) {
          final nextIndex = afterEnclosingAlternative(step, i, i + 1);
          if (nextIndex >= localSteps.length) continue;
          final next = localSteps[nextIndex];
          final isCatch =
              next.kind == 'branch' && next.description.contains('catch');
          if (!isCatch) {
            edges.add(
              _SliceEdge(
                kind: 'control_flow',
                toSymbolPath: '$relPath#$fullSym',
                resolutionStatus: 'resolved',
                depth: depth,
                stepOrdinal: step.ordinal,
                targetStepOrdinal: next.ordinal,
              ),
            );
          }
        }
      }
    }
    for (final syntax in simpleSwitches) {
      final members = <(_SliceStep, _SliceStep, bool)>[];
      for (final member in syntax.cases) {
        final labelStart = _byteOffset(fileContent, member.labelStart);
        final returnStart = _byteOffset(fileContent, member.returnStart);
        final label = localSteps
            .where(
              (step) =>
                  (step.anchor['byteRange'] as List)[0] == labelStart &&
                  (step.kind == 'guard' || step.kind == 'branch'),
            )
            .firstOrNull;
        final result = localSteps
            .where(
              (step) =>
                  (step.anchor['byteRange'] as List)[0] == returnStart &&
                  step.kind == 'return',
            )
            .firstOrNull;
        if (label == null || result == null) {
          throw ArgumentError(
            'entry_symbol_unsupported_control_flow: $entryKey',
          );
        }
        members.add((label, result, member.isDefault));
      }
      for (var index = 0; index < members.length; index++) {
        final (label, result, isDefault) = members[index];
        edges.add(
          _SliceEdge(
            kind: 'control_flow',
            toSymbolPath: '$relPath#$fullSym',
            resolutionStatus: 'resolved',
            depth: depth,
            stepOrdinal: label.ordinal,
            targetStepOrdinal: result.ordinal,
            conditions: isDefault
                ? null
                : [
                    {'stepOrdinal': label.ordinal, 'outcome': 'truthy'},
                  ],
          ),
        );
        if (index + 1 < members.length) {
          edges.add(
            _SliceEdge(
              kind: 'control_flow',
              toSymbolPath: '$relPath#$fullSym',
              resolutionStatus: 'resolved',
              depth: depth,
              stepOrdinal: label.ordinal,
              targetStepOrdinal: members[index + 1].$1.ordinal,
              conditions: [
                {'stepOrdinal': label.ordinal, 'outcome': 'falsy'},
              ],
            ),
          );
        } else if (!isDefault) {
          edges.add(
            _SliceEdge(
              kind: 'unknown_edge',
              toSymbolPath:
                  '$relPath#unresolved_dynamic.switch_no_match_${syntax.start}',
              resolutionStatus: 'unresolved_dynamic',
              depth: depth,
              stepOrdinal: label.ordinal,
              unresolvedReason: 'switch의 모든 입력값에 해당하는 case가 있는지 확인하지 못했습니다.',
            ),
          );
        }
      }
    }
    for (final loop
        in loopSyntax.loops
            .where(
              (loop) =>
                  loop.start >= targetMethod!.bodyStart &&
                  loop.end <= targetMethod.bodyEnd,
            )
            .toList()
          ..sort((a, b) => (a.end - a.start).compareTo(b.end - b.start))) {
      final conditionStart = _byteOffset(fileContent, loop.conditionStart);
      final conditionEnd = _byteOffset(fileContent, loop.conditionEnd);
      final bodyStart = _byteOffset(fileContent, loop.bodyStart);
      final bodyEnd = _byteOffset(fileContent, loop.bodyEnd);
      final loopEnd = _byteOffset(fileContent, loop.end);
      int stepStart(_SliceStep step) =>
          (step.anchor['byteRange'] as List)[0] as int;
      int stepEnd(_SliceStep step) =>
          (step.anchor['byteRange'] as List)[1] as int;
      _SliceStep? matching(int start, int end) {
        for (final step in localSteps) {
          if (stepStart(step) == start && stepEnd(step) == end) return step;
        }
        return null;
      }

      final condition = matching(conditionStart, conditionEnd);
      final update = loop.updateStart == null || loop.updateEnd == null
          ? null
          : matching(
              _byteOffset(fileContent, loop.updateStart!),
              _byteOffset(fileContent, loop.updateEnd!),
            );
      final bodySteps = localSteps
          .where(
            (step) => stepStart(step) >= bodyStart && stepEnd(step) <= bodyEnd,
          )
          .toList();
      var after = localSteps
          .where((step) => stepStart(step) >= loopEnd)
          .firstOrNull;
      final parentLoops =
          loopSyntax.loops
              .where(
                (parent) => parent.start < loop.start && parent.end > loop.end,
              )
              .toList()
            ..sort((a, b) => (a.end - a.start).compareTo(b.end - b.start));
      if (parentLoops.isNotEmpty) {
        final parent = parentLoops.first;
        final parentBodyEnd = _byteOffset(fileContent, parent.bodyEnd);
        if (after == null || stepStart(after) >= parentBodyEnd) {
          after = parent.updateStart == null || parent.updateEnd == null
              ? matching(
                  _byteOffset(fileContent, parent.conditionStart),
                  _byteOffset(fileContent, parent.conditionEnd),
                )
              : matching(
                  _byteOffset(fileContent, parent.updateStart!),
                  _byteOffset(fileContent, parent.updateEnd!),
                );
        }
      }
      if (condition == null) continue;
      void clearDirectFlow(_SliceStep step) {
        edges.removeWhere(
          (edge) =>
              edge.stepOrdinal == step.ordinal &&
              edge.kind == 'control_flow' &&
              edge.toSymbolPath == '$relPath#$fullSym',
        );
      }

      void connect(
        _SliceStep from,
        _SliceStep to,
        String kind, [
        String? outcome,
      ]) {
        edges.add(
          _SliceEdge(
            kind: kind,
            toSymbolPath: '$relPath#$fullSym',
            resolutionStatus: 'resolved',
            depth: depth,
            stepOrdinal: from.ordinal,
            targetStepOrdinal: to.ordinal,
            conditions: outcome == null
                ? null
                : [
                    {'stepOrdinal': from.ordinal, 'outcome': outcome},
                  ],
          ),
        );
      }

      clearDirectFlow(condition);
      if (bodySteps.isEmpty) {
        final emptyBodyTarget = update ?? condition;
        connect(
          condition,
          emptyBodyTarget,
          update == null ? 'loop_back' : 'control_flow',
          'truthy',
        );
      } else {
        connect(condition, bodySteps.first, 'control_flow', 'truthy');
      }
      if (after != null) connect(condition, after, 'control_flow', 'falsy');
      final repeatTarget = update ?? condition;
      if (update != null) {
        clearDirectFlow(update);
        connect(update, condition, 'loop_back');
      }
      bool belongsToNestedLoop(_SliceStep step) => loopSyntax.loops.any(
        (inner) =>
            inner.start > loop.start &&
            inner.end < loop.end &&
            stepStart(step) >= _byteOffset(fileContent, inner.bodyStart) &&
            stepEnd(step) <= _byteOffset(fileContent, inner.bodyEnd),
      );
      for (final step in bodySteps) {
        if ((step.kind != 'guard' && step.kind != 'branch') ||
            step.isLoopCondition ||
            belongsToNestedLoop(step)) {
          continue;
        }
        final escaping = edges.where((edge) {
          if (edge.stepOrdinal != step.ordinal ||
              edge.kind != 'control_flow' ||
              edge.toSymbolPath != '$relPath#$fullSym' ||
              edge.targetStepOrdinal == null) {
            return false;
          }
          final target = localSteps
              .where((candidate) => candidate.ordinal == edge.targetStepOrdinal)
              .firstOrNull;
          return target != null && stepStart(target) >= loopEnd;
        }).toList();
        for (final edge in escaping) {
          edges.remove(edge);
          edges.add(
            _SliceEdge(
              kind: 'loop_back',
              toSymbolPath: edge.toSymbolPath,
              resolutionStatus: edge.resolutionStatus,
              depth: edge.depth,
              stepOrdinal: edge.stepOrdinal,
              targetStepOrdinal: repeatTarget.ordinal,
              conditions: edge.conditions,
            ),
          );
        }
      }
      for (final step in bodySteps) {
        if (step.kind != 'break' && step.kind != 'continue') continue;
        if (loopSyntax.exitLoopStart[stepStart(step)] != loop.start) continue;
        final nested = loopSyntax.loops.any(
          (inner) =>
              inner.start > loop.start &&
              inner.end < loop.end &&
              stepStart(step) >= _byteOffset(fileContent, inner.bodyStart) &&
              stepEnd(step) <= _byteOffset(fileContent, inner.bodyEnd),
        );
        if (nested) continue;
        if (step.kind == 'continue') {
          connect(step, repeatTarget, 'loop_back');
        } else if (after != null) {
          connect(
            step,
            after,
            after.ordinal < step.ordinal ? 'loop_exit_back' : 'loop_exit',
          );
        }
      }
      if (bodySteps.isNotEmpty) {
        final last = bodySteps.last;
        if (last.kind != 'return' &&
            last.kind != 'throw' &&
            last.kind != 'break' &&
            last.kind != 'continue' &&
            !last.isLoopCondition &&
            !belongsToNestedLoop(last)) {
          clearDirectFlow(last);
          connect(last, repeatTarget, 'loop_back');
        }
      }
    }
    activeSymbols.remove(entryKey);
    return firstLocalStepOrdinal;
  }

  sliceMethodBody(
    relPath: initialRelPath,
    className: initialClass,
    methodName: initialMethod,
    depth: 1,
    invocationId: sha256Hex(entrySymbolPath),
  );

  if (!entryMethodFound) {
    throw ArgumentError('entry_symbol_not_found: $entrySymbolPath');
  }
  if (steps.isEmpty) {
    throw ArgumentError(
      'entry_symbol_has_no_executable_steps: $entrySymbolPath',
    );
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

enum _StmtType {
  guard,
  mutation,
  call,
  branch,
  returnStmt,
  loopCondition,
  loopUpdate,
  breakStmt,
  continueStmt,
}

class _ExtractedStmt {
  const _ExtractedStmt({
    required this.type,
    required this.startOffset,
    required this.endOffset,
    required this.rawText,
    this.guardCondition,
    this.stateBefore,
    this.stateAfter,
    this.valueRole,
    this.callReceiver,
    this.callMethod,
    this.isAwait = false,
    this.isEmbeddedCall = false,
    this.guardThenEnd,
    this.guardElseEnd,
    this.shortCircuitCallStart,
    this.shortCircuitAssignmentStart,
    this.shortCircuitOperator,
  });

  final _StmtType type;
  final int startOffset;
  final int endOffset;
  final String rawText;
  final String? guardCondition;
  final String? stateBefore;
  final String? stateAfter;
  final String? valueRole;
  final String? callReceiver;
  final String? callMethod;
  final bool isAwait;
  final bool isEmbeddedCall;
  final int? guardThenEnd;
  final int? guardElseEnd;
  final int? shortCircuitCallStart;
  final int? shortCircuitAssignmentStart;
  final String? shortCircuitOperator;
}

class _ConditionalRange {
  const _ConditionalRange(this.thenEnd, this.elseEnd);

  final int thenEnd;
  final int? elseEnd;
}

class _StatementRangeCollector extends RecursiveAstVisitor<void> {
  _StatementRangeCollector(this.source);

  final String source;
  final conditionalRanges = <int, _ConditionalRange>{};
  final expressionEnds = <int, int>{};
  final expressionCalls = <int, _DirectValueCall>{};

  @override
  void visitIfStatement(IfStatement node) {
    conditionalRanges[node.offset] = _ConditionalRange(
      node.thenStatement.end,
      node.elseStatement?.end,
    );
    super.visitIfStatement(node);
  }

  @override
  void visitExpressionStatement(ExpressionStatement node) {
    expressionEnds[node.offset] = node.end;
    final expression = node.expression is AwaitExpression
        ? (node.expression as AwaitExpression).expression
        : node.expression;
    final call = _directCallFromExpression(source, expression);
    if (call != null) expressionCalls[node.offset] = call;
    super.visitExpressionStatement(node);
  }
}

class _ExecutableStatementCollector extends RecursiveAstVisitor<void> {
  _ExecutableStatementCollector(this.source);

  final String source;
  final statements = <_ExtractedStmt>[];
  final simpleSwitches = <_SimpleSwitchReturn>[];

  @override
  void visitSwitchStatement(SwitchStatement node) {
    final members = node.members;
    final simple =
        members.isNotEmpty &&
        members.every(
          (member) =>
              member.labels.isEmpty &&
              member.statements.length == 1 &&
              member.statements.single is ReturnStatement &&
              (member is SwitchCase ||
                  member is SwitchDefault ||
                  member is SwitchPatternCase &&
                      member.guardedPattern.pattern is ConstantPattern &&
                      member.guardedPattern.whenClause == null),
        );
    if (simple) {
      final cases = <_SimpleSwitchCase>[];
      for (final member in members) {
        final isDefault = member is SwitchDefault;
        statements.add(
          _ExtractedStmt(
            type: isDefault ? _StmtType.branch : _StmtType.guard,
            startOffset: member.keyword.offset,
            endOffset: member.colon.end,
            rawText: source.substring(member.keyword.offset, member.colon.end),
            guardCondition: isDefault
                ? null
                : source.substring(member.keyword.offset, member.colon.end),
          ),
        );
        cases.add(
          _SimpleSwitchCase(
            labelStart: member.keyword.offset,
            returnStart: member.statements.single.offset,
            isDefault: isDefault,
          ),
        );
      }
      simpleSwitches.add(_SimpleSwitchReturn(node.offset, node.end, cases));
    }
    super.visitSwitchStatement(node);
  }

  @override
  void visitForStatement(ForStatement node) {
    final parts = node.forLoopParts;
    if (parts is ForPartsWithDeclarations &&
        parts.variables.end < source.length &&
        source[parts.variables.end] == ';') {
      final names = parts.variables.variables
          .map((variable) => variable.name.lexeme)
          .join(', ');
      statements.add(
        _ExtractedStmt(
          type: _StmtType.mutation,
          startOffset: parts.variables.offset,
          endOffset: parts.variables.end + 1,
          rawText: source.substring(
            parts.variables.offset,
            parts.variables.end + 1,
          ),
          stateAfter: names,
        ),
      );
    }
    super.visitForStatement(node);
  }

  @override
  void visitVariableDeclarationStatement(VariableDeclarationStatement node) {
    final names = node.variables.variables
        .map((variable) => variable.name.lexeme)
        .toList();
    statements.add(
      _ExtractedStmt(
        type: _StmtType.mutation,
        startOffset: node.offset,
        endOffset: node.end,
        rawText: source.substring(node.offset, node.end),
        stateAfter: names.length == 1 ? names.first : names.join(', '),
        valueRole: 'local_binding',
      ),
    );
    super.visitVariableDeclarationStatement(node);
  }

  @override
  void visitExpressionStatement(ExpressionStatement node) {
    final expression = node.expression;
    if (expression is AssignmentExpression) {
      final destination = source.substring(
        expression.leftHandSide.offset,
        expression.leftHandSide.end,
      );
      final value = source.substring(
        expression.rightHandSide.offset,
        expression.rightHandSide.end,
      );
      statements.add(
        _ExtractedStmt(
          type: _StmtType.mutation,
          startOffset: node.offset,
          endOffset: node.end,
          rawText: source.substring(node.offset, node.end),
          stateAfter:
              const {'state', '_state', 'value', 'status'}.contains(destination)
              ? value
              : destination,
        ),
      );
    } else {
      Expression? updated;
      if (expression is PostfixExpression &&
          const {'++', '--'}.contains(expression.operator.lexeme)) {
        updated = expression.operand;
      } else if (expression is PrefixExpression &&
          const {'++', '--'}.contains(expression.operator.lexeme)) {
        updated = expression.operand;
      }
      if (updated != null) {
        statements.add(
          _ExtractedStmt(
            type: _StmtType.mutation,
            startOffset: node.offset,
            endOffset: node.end,
            rawText: source.substring(node.offset, node.end),
            stateAfter: source.substring(updated.offset, updated.end),
          ),
        );
      }
    }
    super.visitExpressionStatement(node);
  }

  @override
  void visitReturnStatement(ReturnStatement node) {
    final expression = node.expression;
    statements.add(
      _ExtractedStmt(
        type: _StmtType.returnStmt,
        startOffset: node.offset,
        endOffset: node.end,
        rawText: source.substring(node.offset, node.end),
        guardCondition: expression == null
            ? null
            : source.substring(expression.offset, expression.end),
      ),
    );
    super.visitReturnStatement(node);
  }
}

class _SimpleSwitchCase {
  const _SimpleSwitchCase({
    required this.labelStart,
    required this.returnStart,
    required this.isDefault,
  });

  final int labelStart;
  final int returnStart;
  final bool isDefault;
}

class _SimpleSwitchReturn {
  const _SimpleSwitchReturn(this.start, this.end, this.cases);

  final int start;
  final int end;
  final List<_SimpleSwitchCase> cases;
}

class _LoopSyntax {
  const _LoopSyntax({
    required this.start,
    required this.end,
    required this.bodyStart,
    required this.bodyEnd,
    required this.conditionStart,
    required this.conditionEnd,
    this.updateStart,
    this.updateEnd,
  });

  final int start;
  final int end;
  final int bodyStart;
  final int bodyEnd;
  final int conditionStart;
  final int conditionEnd;
  final int? updateStart;
  final int? updateEnd;
}

class _LoopSyntaxCollector extends RecursiveAstVisitor<void> {
  _LoopSyntaxCollector(this.source);

  final String source;
  final loops = <_LoopSyntax>[];
  final exits = <AstNode>[];
  final exitLoopStart = <int, int>{};
  final unsupportedControls = <AstNode>[];

  void recordExit(AstNode node, {required bool isBreak}) {
    exits.add(node);
    if ((node is BreakStatement && node.label != null) ||
        (node is ContinueStatement && node.label != null)) {
      unsupportedControls.add(node);
      return;
    }
    for (
      AstNode? parent = node.parent;
      parent != null;
      parent = parent.parent
    ) {
      if (isBreak && parent is SwitchStatement) return;
      if (parent is ForStatement ||
          parent is WhileStatement ||
          parent is DoStatement) {
        exitLoopStart[_byteOffset(source, node.offset)] = parent.offset;
        return;
      }
    }
  }

  @override
  void visitForStatement(ForStatement node) {
    final parts = node.forLoopParts;
    if (parts is ForParts &&
        parts.condition != null &&
        parts.updaters.length <= 1) {
      final condition = parts.condition!;
      final updater = parts.updaters.isEmpty ? null : parts.updaters.single;
      loops.add(
        _LoopSyntax(
          start: node.offset,
          end: node.end,
          bodyStart: node.body.offset,
          bodyEnd: node.body.end,
          conditionStart: condition.offset,
          conditionEnd: condition.end,
          updateStart: updater?.offset,
          updateEnd: updater?.end,
        ),
      );
    } else if (parts is ForEachParts && node.awaitKeyword == null) {
      loops.add(
        _LoopSyntax(
          start: node.offset,
          end: node.end,
          bodyStart: node.body.offset,
          bodyEnd: node.body.end,
          conditionStart: node.forKeyword.offset,
          conditionEnd: node.rightParenthesis.end,
        ),
      );
    } else {
      unsupportedControls.add(node);
    }
    super.visitForStatement(node);
  }

  @override
  void visitWhileStatement(WhileStatement node) {
    loops.add(
      _LoopSyntax(
        start: node.offset,
        end: node.end,
        bodyStart: node.body.offset,
        bodyEnd: node.body.end,
        conditionStart: node.condition.offset,
        conditionEnd: node.condition.end,
      ),
    );
    super.visitWhileStatement(node);
  }

  @override
  void visitDoStatement(DoStatement node) {
    unsupportedControls.add(node);
    super.visitDoStatement(node);
  }

  @override
  void visitBreakStatement(BreakStatement node) {
    recordExit(node, isBreak: true);
    super.visitBreakStatement(node);
  }

  @override
  void visitContinueStatement(ContinueStatement node) {
    recordExit(node, isBreak: false);
    super.visitContinueStatement(node);
  }

  List<_ExtractedStmt> statementsWithin(int start, int end) {
    final statements = <_ExtractedStmt>[];
    for (final loop in loops) {
      if (loop.start < start || loop.end > end) continue;
      statements.add(
        _ExtractedStmt(
          type: _StmtType.loopCondition,
          startOffset: loop.conditionStart,
          endOffset: loop.conditionEnd,
          rawText: source.substring(loop.conditionStart, loop.conditionEnd),
          guardCondition: source.substring(
            loop.conditionStart,
            loop.conditionEnd,
          ),
        ),
      );
      if (loop.updateStart != null && loop.updateEnd != null) {
        statements.add(
          _ExtractedStmt(
            type: _StmtType.loopUpdate,
            startOffset: loop.updateStart!,
            endOffset: loop.updateEnd!,
            rawText: source.substring(loop.updateStart!, loop.updateEnd!),
          ),
        );
      }
    }
    for (final node in exits) {
      if (node.offset < start || node.end > end) continue;
      statements.add(
        _ExtractedStmt(
          type: node is BreakStatement
              ? _StmtType.breakStmt
              : _StmtType.continueStmt,
          startOffset: node.offset,
          endOffset: node.end,
          rawText: source.substring(node.offset, node.end),
        ),
      );
    }
    return statements;
  }
}

class _DirectValueCall {
  const _DirectValueCall(
    this.startOffset,
    this.endOffset,
    this.receiver,
    this.method,
  );

  final int startOffset;
  final int endOffset;
  final String receiver;
  final String method;
}

class _ShortCircuitValue {
  const _ShortCircuitValue(this.expression, this.call, this.operator);

  final BinaryExpression expression;
  final _DirectValueCall call;
  final String operator;
}

_DirectValueCall? _directCallFromExpression(
  String source,
  Expression? expression,
) {
  if (expression is MethodInvocation) {
    final target = expression.target;
    if (target != null &&
        target is! SimpleIdentifier &&
        target is! ThisExpression) {
      return null;
    }
    final receiver = target == null
        ? ''
        : source.substring(target.offset, target.end);
    return _DirectValueCall(
      expression.offset,
      expression.end,
      receiver == 'this' ? 'this' : receiver,
      expression.methodName.name,
    );
  }
  if (expression is FunctionExpressionInvocation) {
    final function = expression.function;
    if (function is SimpleIdentifier) {
      return _DirectValueCall(
        expression.offset,
        expression.end,
        '',
        function.name,
      );
    }
    if (function is MethodInvocation &&
        function.target is SimpleIdentifier &&
        (function.target as SimpleIdentifier).name == 'ref' &&
        function.methodName.name == 'read') {
      return _DirectValueCall(
        expression.offset,
        expression.end,
        source.substring(function.offset, function.end),
        'call',
      );
    }
  }
  return null;
}

bool _isEagerBinaryOperator(String operator) =>
    operator != '&&' && operator != '||' && operator != '??';

FormalParameterList? _directReturnParameters(ExpressionFunctionBody body) {
  final parent = body.parent;
  if (parent is MethodDeclaration) return parent.parameters;
  if (parent is FunctionExpression) return parent.parameters;
  return null;
}

Set<String> _directReturnParameterNames(ExpressionFunctionBody body) =>
    _directReturnParameters(
      body,
    )?.parameters.map((parameter) => parameter.name?.lexeme).nonNulls.toSet() ??
    {};

Set<String> _directReturnNumericParameterNames(ExpressionFunctionBody body) =>
    _directReturnParameters(body)?.parameters
        .where(
          (parameter) => const {
            'int',
            'double',
            'num',
          }.contains(parameter.type?.toSource()),
        )
        .map((parameter) => parameter.name?.lexeme)
        .nonNulls
        .toSet() ??
    {};

bool _isDirectNumericExpression(
  Expression expression,
  Set<String> numericParameters,
) {
  if (expression is IntegerLiteral || expression is DoubleLiteral) return true;
  if (expression is SimpleIdentifier) {
    return numericParameters.contains(expression.name);
  }
  if (expression is ParenthesizedExpression) {
    return _isDirectNumericExpression(expression.expression, numericParameters);
  }
  if (expression is PrefixExpression &&
      const {'-', '~'}.contains(expression.operator.lexeme)) {
    return _isDirectNumericExpression(expression.operand, numericParameters);
  }
  if (expression is BinaryExpression &&
      const {
        '+',
        '-',
        '*',
        '/',
        '~/',
        '%',
      }.contains(expression.operator.lexeme)) {
    return _isDirectNumericExpression(
          expression.leftOperand,
          numericParameters,
        ) &&
        _isDirectNumericExpression(expression.rightOperand, numericParameters);
  }
  return false;
}

bool _isDirectReturnExpression(
  Expression expression,
  Set<String> parameterNames,
  Set<String> numericParameters,
) {
  if (expression is BooleanLiteral ||
      expression is IntegerLiteral ||
      expression is DoubleLiteral ||
      expression is SimpleStringLiteral ||
      expression is NullLiteral) {
    return true;
  }
  if (expression is SimpleIdentifier) {
    return parameterNames.contains(expression.name);
  }
  if (expression is ParenthesizedExpression) {
    return _isDirectReturnExpression(
      expression.expression,
      parameterNames,
      numericParameters,
    );
  }
  if (expression is PrefixExpression &&
      expression.operator.lexeme == '!' &&
      expression.operand is BooleanLiteral) {
    return true;
  }
  return _isDirectNumericExpression(expression, numericParameters);
}

class _DirectValueCallCollector extends RecursiveAstVisitor<void> {
  _DirectValueCallCollector(this.source);

  final String source;
  final calls = <_DirectValueCall>[];
  final argumentCalls = <_DirectValueCall>[];
  final eagerOperandCalls = <_DirectValueCall>[];
  final shortCircuitValues = <_ShortCircuitValue>[];

  _DirectValueCall? within(int start, int end) {
    final matches = calls
        .where((call) => call.startOffset >= start && call.endOffset <= end)
        .toList();
    return matches.length == 1 ? matches.single : null;
  }

  void record(Expression? expression) {
    final call = _directCallFromExpression(source, expression);
    if (call != null) calls.add(call);
  }

  @override
  void visitArgumentList(ArgumentList node) {
    for (final argument in node.arguments) {
      final call = _directCallFromExpression(
        source,
        argument.argumentExpression,
      );
      if (call != null) argumentCalls.add(call);
    }
    super.visitArgumentList(node);
  }

  @override
  void visitBinaryExpression(BinaryExpression node) {
    final parent = node.parent;
    final directAssignment =
        parent is VariableDeclaration && identical(parent.initializer, node) ||
        parent is AssignmentExpression &&
            parent.operator.lexeme == '=' &&
            identical(parent.rightHandSide, node);
    if (directAssignment) {
      if (_isEagerBinaryOperator(node.operator.lexeme)) {
        for (final operand in [node.leftOperand, node.rightOperand]) {
          final call = _directCallFromExpression(source, operand);
          if (call != null) eagerOperandCalls.add(call);
        }
      } else if (node.leftOperand is SimpleIdentifier &&
          const {'&&', '||', '??'}.contains(node.operator.lexeme)) {
        final call = _directCallFromExpression(source, node.rightOperand);
        if (call != null) {
          shortCircuitValues.add(
            _ShortCircuitValue(node, call, node.operator.lexeme),
          );
        }
      }
    }
    super.visitBinaryExpression(node);
  }

  @override
  void visitVariableDeclaration(VariableDeclaration node) {
    record(node.initializer);
    super.visitVariableDeclaration(node);
  }

  @override
  void visitAssignmentExpression(AssignmentExpression node) {
    if (node.operator.lexeme == '=') record(node.rightHandSide);
    super.visitAssignmentExpression(node);
  }
}

class _LocalCollectionDeclaration {
  const _LocalCollectionDeclaration(
    this.name,
    this.offset,
    this.scope,
    this.isCollection, {
    this.isField = false,
  });

  final String name;
  final int offset;
  final AstNode scope;
  final bool isCollection;
  final bool isField;
}

class _LocalCollectionCollector extends RecursiveAstVisitor<void> {
  _LocalCollectionCollector({required this.coreCollectionFactoryNames});

  final Set<String> coreCollectionFactoryNames;
  final declarations = <_LocalCollectionDeclaration>[];
  final nestedFunctions = <FunctionExpression>[];

  @override
  void visitFunctionExpression(FunctionExpression node) {
    nestedFunctions.add(node);
    super.visitFunctionExpression(node);
  }

  @override
  void visitFormalParameterList(FormalParameterList node) {
    final owner = node.parent;
    if (owner != null) {
      for (final parameter in node.parameters) {
        final name = parameter.name?.lexeme;
        if (name != null) {
          declarations.add(
            _LocalCollectionDeclaration(name, parameter.offset, owner, false),
          );
        }
      }
    }
    super.visitFormalParameterList(node);
  }

  @override
  void visitVariableDeclaration(VariableDeclaration node) {
    final list = node.parent;
    final statement = list?.parent;
    final scope = statement?.parent;
    if (list is VariableDeclarationList &&
        ((statement is VariableDeclarationStatement && scope is Block) ||
            (statement is FieldDeclaration && scope is ClassBody))) {
      final initializer = node.initializer;
      final isLiteral =
          initializer is ListLiteral ||
          (initializer is SetOrMapLiteral &&
              initializer.typeArguments?.arguments.length == 1 &&
              initializer.elements.every(
                (element) => element is! MapLiteralEntry,
              )) ||
          (initializer is InstanceCreationExpression &&
              coreCollectionFactoryNames.contains(
                initializer.constructorName.type.name.lexeme,
              ) &&
              initializer.constructorName.type.importPrefix == null &&
              initializer.constructorName.name?.name == 'from');
      declarations.add(
        _LocalCollectionDeclaration(
          node.name.lexeme,
          node.offset,
          scope!,
          list.keyword?.lexeme == 'final' && isLiteral,
          isField: statement is FieldDeclaration,
        ),
      );
    }
    super.visitVariableDeclaration(node);
  }

  bool proves(String receiver, int callOffset) {
    if (receiver.isEmpty) return false;
    final visible =
        declarations
            .where(
              (declaration) =>
                  declaration.name == receiver &&
                  (declaration.isField || declaration.offset < callOffset) &&
                  declaration.scope.offset < callOffset &&
                  callOffset < declaration.scope.end,
            )
            .toList()
          ..sort((a, b) {
            final scopeOrder = (a.scope.end - a.scope.offset).compareTo(
              b.scope.end - b.scope.offset,
            );
            return scopeOrder != 0 ? scopeOrder : b.offset.compareTo(a.offset);
          });
    if (visible.isEmpty) return false;
    final declaration = visible.first;
    if (nestedFunctions.any(
      (function) =>
          (declaration.isField || function.offset > declaration.offset) &&
          function.offset < callOffset &&
          callOffset < function.end,
    )) {
      return false;
    }
    return declaration.isCollection;
  }
}

List<_ExtractedStmt> _extractStatements(
  String bodySource,
  int baseOffset,
  Map<int, _ConditionalRange> conditionalRanges,
  Map<int, int> expressionEnds,
  Map<int, _DirectValueCall> expressionCalls,
  List<_ExtractedStmt> executableStatements,
) {
  final results = <_ExtractedStmt>[];

  // Regex patterns for statements
  // Capture the full balanced paren body of `if (...)` so nested calls in
  // the condition (`if (await _signUp(draft) case ...)`) survive intact.
  RegExp guardRe = RegExp(r'\bif\s*\(', multiLine: true);
  final throwRe = RegExp(r'\bthrow\s+([^;]+);', multiLine: true);
  final mutationRe = RegExp(
    r'\bemit\s*\(([^)]+)\);'
    r'|\bstate\.copyWith\s*\(([^)]+)\)',
    multiLine: true,
  );
  final callStartRe = RegExp(
    r'(?:await\s+)?([A-Za-z0-9_$.()]+)\s*\(',
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

    final conditionalRange = conditionalRanges[baseOffset + m.start];

    results.add(
      _ExtractedStmt(
        type: _StmtType.guard,
        startOffset: baseOffset + m.start,
        endOffset: baseOffset + condEnd + 1,
        rawText: bodySource.substring(m.start, condEnd + 1),
        guardCondition: cond,
        guardThenEnd: conditionalRange?.thenEnd,
        guardElseEnd: conditionalRange?.elseEnd,
      ),
    );
    // A call awaited inside the condition (`if (await _signUp(draft) ...)`)
    // is a real delegation step, not just a guard — extract it too so the
    // slice can follow it across layers.
    for (final cm in condCallRe.allMatches(cond)) {
      final cTarget = cm.group(1)!.trim();
      final cRaw = cm.group(0)!;
      // Keep genuine invocations only: awaited calls, member calls, or
      // private callable fields. Skips type checks (`case Error(:final e)`).
      final genuine =
          cRaw.startsWith('await') ||
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
      results.add(
        _ExtractedStmt(
          type: _StmtType.call,
          startOffset: baseOffset + condStart + cm.start,
          endOffset: baseOffset + condStart + cm.end,
          rawText: cm.group(0)!,
          callReceiver: cReceiver,
          callMethod: cMethod,
          isAwait: cRaw.trimLeft().startsWith('await '),
        ),
      );
    }
  }

  // Throws (as guards / failure branches)
  for (final m in throwRe.allMatches(bodySource)) {
    occupied.add([m.start, m.end]);
    results.add(
      _ExtractedStmt(
        type: _StmtType.guard,
        startOffset: baseOffset + m.start,
        endOffset: baseOffset + m.end,
        rawText: m.group(0)!,
        guardCondition: '실패 시 예외 발생: ${m.group(1)!.trim()}',
      ),
    );
  }

  bool insideOccupied(int start, int end) {
    for (final span in occupied) {
      if (start < span[1] &&
          end > span[0] &&
          !(start < span[0] && end > span[1])) {
        return true;
      }
    }
    return false;
  }

  // Mutations
  for (final m in mutationRe.allMatches(bodySource)) {
    if (insideOccupied(m.start, m.end)) continue;
    if (executableStatements.any(
      (statement) =>
          statement.startOffset <= baseOffset + m.start &&
          statement.endOffset >= baseOffset + m.end,
    )) {
      continue;
    }
    occupied.add([m.start, m.end]);
    final raw = m.group(0)!;
    String? stateAfter;
    String? stateBefore;

    if (raw.contains('copyWith')) {
      final inner = m.group(2) ?? '';
      stateAfter = inner.trim();
      stateBefore = 'status: idle';
    } else if (raw.contains('emit')) {
      stateAfter = (m.group(1) ?? '').trim();
    } else {
      stateAfter = '';
    }

    results.add(
      _ExtractedStmt(
        type: _StmtType.mutation,
        startOffset: baseOffset + m.start,
        endOffset: baseOffset + m.end,
        rawText: raw,
        stateBefore: stateBefore,
        stateAfter: stateAfter,
      ),
    );
  }

  // Calls
  for (final m in callStartRe.allMatches(bodySource)) {
    final statementEnd = expressionEnds[baseOffset + m.start];
    if (statementEnd == null) continue;
    final localEnd = statementEnd - baseOffset;
    if (localEnd <= m.end || localEnd > bodySource.length) continue;
    if (insideOccupied(m.start, localEnd)) continue;
    final raw = bodySource.substring(m.start, localEnd);
    // A callback argument can make the permissive receiver pattern consume
    // the first opening parenthesis: removeWhere((item) ...). The call name
    // ends before that parenthesis, while the AST still owns the full range.
    final target = m.group(1)!.trim().replaceFirst(RegExp(r'\(+$'), '');

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
    final astCall = expressionCalls[baseOffset + m.start];
    if (astCall != null) {
      receiver = astCall.receiver;
      method = astCall.method;
    }

    results.add(
      _ExtractedStmt(
        type: _StmtType.call,
        startOffset: baseOffset + m.start,
        endOffset: statementEnd,
        rawText: raw,
        callReceiver: receiver,
        callMethod: method,
        isAwait: raw.trimLeft().startsWith('await '),
      ),
    );
  }

  // Branches
  for (final m in catchRe.allMatches(bodySource)) {
    results.add(
      _ExtractedStmt(
        type: _StmtType.branch,
        startOffset: baseOffset + m.start,
        endOffset: baseOffset + m.end,
        rawText: m.group(0)!,
      ),
    );
  }

  // Declarations, assignments, and returns come from the parsed snapshot.
  for (final statement in executableStatements) {
    final start = statement.startOffset - baseOffset;
    final end = statement.endOffset - baseOffset;
    if (insideOccupied(start, end)) continue;
    occupied.add([start, end]);
    results.add(statement);
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
  final stateAssignment = RegExp(
    r'^\s*(state|_state|value)\s*=(?!=|>)',
  ).firstMatch(raw);
  if (stateAssignment != null) {
    return stateAssignment.group(1) == 'value' ? '값을 갱신한다' : '상태를 갱신한다';
  }
  if (stateAfter != null) {
    if (stateAfter == 'secret value assigned') {
      return '보안 값을 할당한다';
    }
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
    if (!stateAfter.contains(' ') && !stateAfter.contains('(')) {
      return '변환 결과를 $stateAfter에 할당한다';
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
      'Manager',
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
