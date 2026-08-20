#!/usr/bin/env dart

// A bounded, source-backed go_router adapter. It accepts only direct literal
// GoRoute(path: '/...') declarations; dynamic expressions are intentionally
// omitted rather than guessed.
import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:analyzer/dart/analysis/utilities.dart';
import 'package:analyzer/dart/analysis/analysis_context_collection.dart';
import 'package:analyzer/dart/analysis/results.dart';
import 'package:analyzer/dart/ast/ast.dart';
import 'package:analyzer/dart/ast/visitor.dart';
import 'package:analyzer/dart/element/element.dart';

const protocol = '1';
final Map<String, AnalysisContextCollection> _analysisContexts = {};
const _analysisContextsPerRepository = 4;

Future<void> main(List<String> args) async {
  if (args.length == 1 && args[0] == '--probe') {
    stdout.writeln(
      jsonEncode({
        'adapter_version': '0.5.0',
        'protocol_version': protocol,
        'status': 'ready',
      }),
    );
    return;
  }
  if (args.length != 1 || args[0] != '--stdio') {
    stderr.writeln('usage: codeflow-dart-adapter --probe|--stdio');
    exitCode = 64;
    return;
  }
  await for (final line
      in stdin.transform(utf8.decoder).transform(const LineSplitter())) {
    try {
      final request = jsonDecode(line);
      if (request is! Map || request['jsonrpc'] != '2.0') {
        _error(null, 'ADAPTER_MALFORMED', 'request must be JSON-RPC 2.0');
        continue;
      }
      final id = request['id'];
      final method = request['method'];
      final params = request['params'];
      if (method == 'cancel')
        continue; // discovery is bounded; parent kills after deadline.
      if (method == 'initialize') {
        if (params is! Map || params['protocol_version'] != protocol) {
          _error(
            id,
            'ADAPTER_INCOMPATIBLE',
            'protocol_version $protocol required',
          );
          continue;
        }
        _result(id, {
          'protocol_version': protocol,
          'adapter_version': '0.5.0',
          'capabilities': [
            'discover_entry_points',
            'refine_route_flow',
            'riverpod_state',
            'dart_analyzer_ast',
            'resolved_symbols',
            'cancellation',
            'shutdown',
          ],
        });
      } else if (method == 'discoverEntryPoints') {
        if (params is! Map || params['repository'] is! String) {
          _error(id, 'ADAPTER_MALFORMED', 'repository is required');
          continue;
        }
        _result(id, {
          'entry_points': await _discover(params['repository'] as String),
        });
      } else if (method == 'refineRouteFlow') {
        if (params is! Map ||
            params['repository'] is! String ||
            params['flow_id'] is! String ||
            params['paths'] is! List) {
          _error(
            id,
            'ADAPTER_MALFORMED',
            'repository, flow_id, and paths are required',
          );
          continue;
        }
        _result(id, {
          'facts': await _refine(
            params['repository'] as String,
            params['flow_id'] as String,
            (params['paths'] as List).whereType<String>().toList(),
            ((params['analysis_paths'] as List?) ?? params['paths'] as List)
                .whereType<String>()
                .toList(),
          ),
        });
      } else if (method == 'shutdown') {
        _result(id, {});
        break;
      } else {
        _error(id, 'METHOD_NOT_FOUND', 'unsupported method $method');
      }
    } catch (e) {
      _error(null, 'ADAPTER_UNAVAILABLE', 'source inspection failed: $e');
    }
  }
}

// This is a deliberately bounded refinement for the CF-G05 supported shape:
// an onPressed method tear-off, direct private helper calls, and a literal
// GoRouter/context navigation. It reads only graph-slice paths supplied by Core.
Future<List<Map<String, Object>>> _refine(
  String root,
  String flowId,
  List<String> paths,
  List<String> analysisPaths,
) async {
  final facts = <Map<String, Object>>[];
  final sources = <String, String>{};
  final units = <String, CompilationUnit>{};
  // One long-lived adapter process owns one analyzer context per repository.
  // Multi-flow compilation therefore pays package resolution once while each
  // flow still receives an independently verified source slice.
  final includedPaths =
      analysisPaths
          .where((path) => path.endsWith('.dart') && !path.startsWith('../'))
          .map(
            (path) =>
                '$root${Platform.pathSeparator}${path.replaceAll('/', Platform.pathSeparator)}',
          )
          .toSet()
          .toList()
        ..sort();
  if (includedPaths.isEmpty) return facts;
  final analysisKey = '$root\u0000${includedPaths.join('\u0000')}';
  var analysis = _analysisContexts[analysisKey];
  if (analysis == null) {
    // A long-running Core may see several bounded graph slices as files are
    // added or flows are refreshed. Keep useful Analyzer contexts warm without
    // retaining every historical path set for the lifetime of the process.
    final repositoryPrefix = '$root\u0000';
    final repositoryKeys = _analysisContexts.keys
        .where((key) => key.startsWith(repositoryPrefix))
        .toList();
    while (repositoryKeys.length >= _analysisContextsPerRepository) {
      final oldest = repositoryKeys.removeAt(0);
      _analysisContexts.remove(oldest)?.dispose();
    }
    analysis = AnalysisContextCollection(
      includedPaths: includedPaths,
      sdkPath: _dartSdkPath(),
    );
    _analysisContexts[analysisKey] = analysis;
  }
  final activeAnalysis = analysis;
  final contracts = await _externalContracts(root);
  // Resolve the complete validated slice first. This lets route ownership be
  // established before any callback is admitted.
  final flowPaths =
      paths
          .where((path) => path.endsWith('.dart') && !path.startsWith('../'))
          .toSet()
          .toList()
        ..sort();
  // Independent package analysis drivers can resolve their files in parallel.
  // Results are reinserted in path order so fact generation stays byte-for-byte
  // deterministic even when completion order differs.
  final resolved = await Future.wait(
    flowPaths.map((relative) => _resolveSource(activeAnalysis, root, relative)),
  );
  for (final result in resolved.whereType<_ResolvedSource>()) {
    sources[result.relative] = result.source;
    if (result.unit != null) units[result.relative] = result.unit!;
  }
  final causality = _ResolvedCausality(sources, units);
  if (flowId.startsWith('system:')) {
    return _refineSystem(flowId, sources, units, causality, contracts);
  }
  final selectedOwners = _selectedRouteOwners(flowId, sources, units);
  for (final entry in units.entries) {
    final relative = entry.key;
    final resolvedUnit = entry.value;
    final source = sources[relative]!;
    // Only resolved method tear-offs become actions. Text that merely looks
    // like `onPressed: name` is a candidate, not evidence. The visitor also
    // accepts the common `condition ? null : method` shape while rejecting
    // closures, computed callbacks, unresolved identifiers, and fields.
    final presses = _resolvedCallbacks(resolvedUnit, source);
    for (final press in presses) {
      if (!selectedOwners.contains(press.owner)) continue;
      final action = press.name;
      final actionSymbol = press.symbol;
      final owner = press.owner;
      facts.add(
        _fact(
          'user_action',
          actionSymbol,
          press.label ?? '',
          relative,
          source,
          press.start,
          press.end,
          'resolved_ast:${press.trigger}:$actionSymbol',
          proof: 'resolved_ast',
          symbolId: actionSymbol,
        ),
      );
      final body = press.body;
      facts.addAll(
        causality.routeTransitions(actionSymbol, relative, source, body),
      );
      facts.addAll(
        causality.eventChainFacts(actionSymbol, relative, source, body),
      );
      facts.addAll(_riverpodFacts(source, relative, owner, actionSymbol, body));
      facts.addAll(
        _boundaryFacts(source, relative, actionSymbol, body, contracts),
      );
      final calls = _directMethodCalls(
        source,
        body,
        resolvedUnit: resolvedUnit,
      );
      for (final call in calls) {
        final target = call.name;
        if (target == action ||
            target == 'if' ||
            target == 'for' ||
            target == 'setState')
          continue;
        final offset = call.start;
        final targetSymbol = call.symbol!;
        facts.add(
          _fact(
            'call',
            actionSymbol,
            targetSymbol,
            relative,
            source,
            offset,
            call.end,
            'resolved_ast:call:$actionSymbol:$targetSymbol',
            proof: 'resolved_ast',
            symbolId: targetSymbol,
          ),
        );
        final targetBody = _resolvedMethodBody(
          resolvedUnit,
          call.element!,
          source,
        );
        if (targetBody != null) {
          facts.addAll(
            causality.routeTransitions(
              targetSymbol,
              relative,
              source,
              targetBody,
            ),
          );
          facts.addAll(
            causality.eventChainFacts(
              targetSymbol,
              relative,
              source,
              targetBody,
            ),
          );
          facts.addAll(
            _riverpodFacts(source, relative, owner, actionSymbol, targetBody),
          );
          facts.addAll(
            _boundaryFacts(
              source,
              relative,
              targetSymbol,
              targetBody,
              contracts,
            ),
          );
          facts.addAll(
            _controlFacts(targetSymbol, relative, source, targetBody),
          );
        }
      }
      facts.addAll(_controlFacts(actionSymbol, relative, source, body));
    }
  }
  final unique = <String, Map<String, Object>>{};
  for (final fact in facts) {
    unique['${fact['kind']}:${fact['subject']}:${fact['object']}:${(fact['anchor'] as Map)['byte_start']}'] =
        fact;
  }
  final result = unique.values.toList();
  result.sort(
    (a, b) => '${a['kind']}:${a['subject']}:${a['object']}'.compareTo(
      '${b['kind']}:${b['subject']}:${b['object']}',
    ),
  );
  return result;
}

// System entries are deliberately narrow framework shapes. They are not
// inferred from method names alone: discovery has already anchored either a
// Flutter lifecycle override or a concrete session/FCM stream subscription.
// The selected method must resolve uniquely inside its declared owner in the
// same current source slice. A file may legitimately contain several State
// classes with the same lifecycle method name.
List<Map<String, Object>> _refineSystem(
  String flowId,
  Map<String, String> sources,
  Map<String, CompilationUnit> units,
  _ResolvedCausality causality,
  Map<String, _ExternalContract> contracts,
) {
  final parts = flowId.split(':');
  if (parts.length < 5) return [];
  final kind = parts[1];
  final methodName = parts.last;
  final owner = parts[parts.length - 2];
  final relative = parts.sublist(2, parts.length - 2).join(':');
  final unit = units[relative];
  final source = sources[relative];
  if (unit == null || source == null) return [];
  final methods = _namedMethods(unit, source, owner, methodName);
  if (methods.length != 1) return [];
  final method = methods.single;
  final facts = <Map<String, Object>>[
    _fact(
      'system_event',
      method.symbol,
      kind,
      relative,
      source,
      method.start,
      method.end,
      'resolved_ast:system_entry:$kind:${method.symbol}',
      proof: 'resolved_ast',
      symbolId: method.symbol,
    ),
  ];
  facts.addAll(
    causality.routeTransitions(method.symbol, relative, source, method.body),
  );
  facts.addAll(
    causality.eventChainFacts(method.symbol, relative, source, method.body),
  );
  facts.addAll(
    _riverpodFacts(source, relative, method.owner, method.symbol, method.body),
  );
  facts.addAll(
    _boundaryFacts(source, relative, method.symbol, method.body, contracts),
  );
  for (final call in _directMethodCalls(
    source,
    method.body,
    resolvedUnit: unit,
  )) {
    if (call.name == methodName || call.symbol == null) continue;
    facts.add(
      _fact(
        'call',
        method.symbol,
        call.symbol!,
        relative,
        source,
        call.start,
        call.end,
        'resolved_ast:call:${method.symbol}:${call.symbol}',
        proof: 'resolved_ast',
        symbolId: call.symbol,
      ),
    );
    final targetBody = _resolvedMethodBody(unit, call.element!, source);
    if (targetBody == null) continue;
    facts.addAll(
      causality.routeTransitions(call.symbol!, relative, source, targetBody),
    );
    facts.addAll(
      causality.eventChainFacts(call.symbol!, relative, source, targetBody),
    );
    facts.addAll(
      _riverpodFacts(source, relative, method.owner, method.symbol, targetBody),
    );
    facts.addAll(
      _boundaryFacts(source, relative, call.symbol!, targetBody, contracts),
    );
    facts.addAll(_controlFacts(call.symbol!, relative, source, targetBody));
  }
  return facts;
}

class _ResolvedSource {
  final String relative;
  final String source;
  final CompilationUnit? unit;
  const _ResolvedSource(this.relative, this.source, this.unit);
}

Future<_ResolvedSource?> _resolveSource(
  AnalysisContextCollection analysis,
  String root,
  String relative,
) async {
  final file = File(
    '$root${Platform.pathSeparator}${relative.replaceAll('/', Platform.pathSeparator)}',
  );
  if (!await file.exists()) return null;
  final source = utf8.decode(await file.readAsBytes());
  CompilationUnit? resolvedUnit;
  try {
    final result = await analysis
        .contextFor(file.path)
        .currentSession
        .getResolvedUnit(file.path);
    if (result is ResolvedUnitResult) resolvedUnit = result.unit;
  } catch (_) {
    // A syntax tree may still help discovery, but it is never allowed to
    // create an observed callback/call relationship. Those facts require a
    // resolved element below.
  }
  return _ResolvedSource(relative, source, resolvedUnit);
}

String? _dartSdkPath() {
  final candidates = <String>[];
  final configured = Platform.environment['DART_SDK'];
  if (configured != null && configured.isNotEmpty) candidates.add(configured);
  final flutter = Platform.environment['FLUTTER_ROOT'];
  if (flutter != null && flutter.isNotEmpty) {
    candidates.add(
      '$flutter${Platform.pathSeparator}bin${Platform.pathSeparator}cache${Platform.pathSeparator}dart-sdk',
    );
  }
  candidates.add(_sdkRootForExecutable(Platform.resolvedExecutable));
  final executableName = Platform.isWindows ? 'dart.exe' : 'dart';
  for (final directory in (Platform.environment['PATH'] ?? '').split(
    Platform.isWindows ? ';' : ':',
  )) {
    if (directory.isEmpty) continue;
    final executable = File(
      '$directory${Platform.pathSeparator}$executableName',
    );
    if (executable.existsSync()) {
      candidates.add(_sdkRootForExecutable(executable.path));
      // Flutter's bin/dart is a launcher script rather than a symlink. Its
      // actual SDK is the adjacent bin/cache/dart-sdk directory.
      candidates.add(
        '${executable.parent.path}${Platform.pathSeparator}cache${Platform.pathSeparator}dart-sdk',
      );
      break;
    }
  }
  for (final candidate in candidates.toSet()) {
    final libraries = File(
      '$candidate${Platform.pathSeparator}lib${Platform.pathSeparator}_internal${Platform.pathSeparator}sdk_library_metadata${Platform.pathSeparator}lib${Platform.pathSeparator}libraries.dart',
    );
    if (libraries.existsSync()) return candidate;
  }
  return null;
}

String _sdkRootForExecutable(String executable) {
  try {
    return File(File(executable).resolveSymbolicLinksSync()).parent.parent.path;
  } catch (_) {
    return File(executable).parent.parent.path;
  }
}

// Resolved causality is a deep module behind one small interface: given an
// already selected action body, return only source-backed navigation and
// event→state→listener facts. Product names never enter this implementation.
// Candidate slicing happens in Core; every relationship below additionally
// requires Analyzer elements from the exact supplied source slice.
class _ResolvedCausality {
  final Map<String, String> sources;
  final Map<String, CompilationUnit> units;
  final Map<String, _RouteConstant> _routeConstants = {};
  final Map<String, _RouteConstant> _destinationRoutes = {};
  final Map<String, _LocatedBody> _executables = {};

  _ResolvedCausality(this.sources, this.units) {
    _indexExecutables();
    _indexRouteConstants();
    _indexDestinationRoutes();
  }

  List<Map<String, Object>> routeTransitions(
    String subject,
    String path,
    String source,
    _Body body,
  ) {
    final unit = units[path];
    if (unit == null) return [];
    final visitor = _ResolvedNavigationVisitor(
      body.start,
      body.start + body.text.length,
      _routeConstants,
      _destinationRoutes,
    );
    unit.accept(visitor);
    return visitor.navigation
        .map(
          (item) => _fact(
            'route_transition',
            subject,
            'route:${item.route}',
            path,
            source,
            item.start,
            item.end,
            item.fingerprint,
            proof: item.proof,
            symbolId: item.symbolId,
          ),
        )
        .toList();
  }

  List<Map<String, Object>> eventChainFacts(
    String subject,
    String path,
    String source,
    _Body body,
  ) {
    final unit = units[path];
    if (unit == null) return [];
    final dispatchVisitor = _DispatchVisitor(
      body.start,
      body.start + body.text.length,
    );
    unit.accept(dispatchVisitor);
    final chains = <_ResolvedEventChain>[];
    for (final dispatch in dispatchVisitor.dispatches) {
      final state = _uniqueStateTransition(dispatch.eventTypeSymbol);
      if (state == null) continue;
      final listener = _uniqueListenerTransition(
        dispatch.providerSymbol,
        state,
      );
      if (listener == null) continue;
      final guard = _lastTerminalGuard(unit, body, dispatch.start);
      if (guard == null) continue;
      chains.add(_ResolvedEventChain(dispatch, state, listener, guard));
    }
    if (chains.length != 1) return [];
    final chain = chains.single;
    final eventName = chain.dispatch.eventType.displayName;
    final providerName = chain.dispatch.provider.displayName;
    final stateName = chain.state.stateType.displayName;
    final listenerSubject = chain.listener.body.symbol;
    return [
      _fact(
        'confirmation_condition',
        subject,
        chain.guard.condition.toSource(),
        path,
        source,
        chain.guard.start,
        chain.guard.end,
        'resolved_ast:terminal_guard:$subject:${chain.guard.condition.toSource()}',
        proof: 'resolved_ast',
        symbolId: subject,
      ),
      _fact(
        'terminal_result',
        subject,
        'result:no_navigation',
        path,
        source,
        chain.guard.start,
        chain.guard.end,
        'resolved_ast:return_without_navigation:$subject',
        proof: 'resolved_ast',
        symbolId: subject,
      ),
      _fact(
        'event_dispatch',
        subject,
        'event:$eventName',
        path,
        source,
        chain.dispatch.start,
        chain.dispatch.end,
        'resolved_ast:dispatch:${chain.dispatch.providerSymbol}:${chain.dispatch.eventTypeSymbol}',
        proof: 'resolved_ast',
        symbolId: chain.dispatch.eventTypeSymbol,
      ),
      _fact(
        'notifier_state_transition',
        'provider:$providerName',
        'state:$stateName.${chain.state.field}=${chain.state.value}',
        chain.state.body.path,
        chain.state.body.source,
        chain.state.start,
        chain.state.end,
        'resolved_ast:event_state:${chain.dispatch.eventTypeSymbol}:${chain.state.stateTypeSymbol}:${chain.state.field}=${chain.state.value}',
        proof: 'resolved_ast',
        symbolId: chain.state.body.symbol,
      ),
      _fact(
        'listener_condition',
        listenerSubject,
        chain.listener.condition.toSource(),
        chain.listener.body.path,
        chain.listener.body.source,
        chain.listener.condition.offset,
        chain.listener.condition.end,
        'resolved_ast:listener:${chain.dispatch.providerSymbol}:${chain.state.stateTypeSymbol}:${chain.state.field}',
        proof: 'resolved_ast',
        symbolId: listenerSubject,
      ),
      _fact(
        'route_transition',
        listenerSubject,
        'route:${chain.listener.navigation.route}',
        chain.listener.body.path,
        chain.listener.body.source,
        chain.listener.navigation.start,
        chain.listener.navigation.end,
        chain.listener.navigation.fingerprint,
        proof: chain.listener.navigation.proof,
        symbolId: chain.listener.navigation.symbolId,
      ),
    ];
  }

  void _indexExecutables() {
    for (final entry in units.entries) {
      final source = sources[entry.key]!;
      for (final declaration in entry.value.declarations) {
        if (declaration is ClassDeclaration) {
          for (final method
              in declaration.members.whereType<MethodDeclaration>()) {
            final element = method.declaredFragment?.element;
            if (element == null || element.isSynthetic) continue;
            final symbol = _canonicalSymbol(element);
            _executables[symbol] = _LocatedBody(
              entry.key,
              source,
              entry.value,
              symbol,
              _functionBody(source, method.body),
            );
          }
        } else if (declaration is FunctionDeclaration) {
          final element = declaration.declaredFragment?.element;
          if (element == null || element.isSynthetic) continue;
          final symbol = _canonicalSymbol(element);
          _executables[symbol] = _LocatedBody(
            entry.key,
            source,
            entry.value,
            symbol,
            _functionBody(source, declaration.functionExpression.body),
          );
        }
      }
    }
  }

  void _indexRouteConstants() {
    for (final entry in units.entries) {
      final source = sources[entry.key]!;
      final visitor = _ResolvedRouteConstantVisitor(entry.key, source);
      entry.value.accept(visitor);
      for (final route in visitor.routes) {
        _routeConstants[route.symbol] = route;
      }
    }
  }

  void _indexDestinationRoutes() {
    final candidates = <String, List<_RouteConstant>>{};
    for (final unit in units.values) {
      final visitor = _DestinationResolverVisitor(_routeConstants);
      unit.accept(visitor);
      for (final entry in visitor.routes.entries) {
        candidates.putIfAbsent(entry.key, () => []).add(entry.value);
      }
    }
    for (final entry in candidates.entries) {
      final unique = {
        for (final route in entry.value)
          '${route.symbol}\u0000${route.value}': route,
      };
      if (unique.length == 1) {
        _destinationRoutes[entry.key] = unique.values.single;
      }
    }
  }

  _ResolvedStateTransition? _uniqueStateTransition(String eventTypeSymbol) {
    final found = <_ResolvedStateTransition>[];
    for (final entry in units.entries) {
      final visitor = _EventCaseVisitor(eventTypeSymbol);
      entry.value.accept(visitor);
      for (final eventCase in visitor.cases) {
        final direct = _stateTransitionsInRange(
          _LocatedBody(
            entry.key,
            sources[entry.key]!,
            entry.value,
            eventTypeSymbol,
            _Body(
              eventCase.offset,
              sources[entry.key]!.substring(eventCase.offset, eventCase.end),
            ),
          ),
        );
        found.addAll(direct);
        if (direct.isNotEmpty) continue;
        final calls = _ResolvedCallVisitor(eventCase.offset, eventCase.end);
        entry.value.accept(calls);
        for (final call in calls.calls) {
          final target = _executables[call];
          if (target != null) found.addAll(_stateTransitionsInRange(target));
        }
      }
    }
    final unique = {
      for (final item in found)
        '${item.body.path}:${item.start}:${item.end}': item,
    };
    return unique.length == 1 ? unique.values.single : null;
  }

  List<_ResolvedStateTransition> _stateTransitionsInRange(_LocatedBody body) {
    final visitor = _ResolvedStateAssignmentVisitor(
      body.body.start,
      body.body.start + body.body.text.length,
    );
    body.unit.accept(visitor);
    return visitor.transitions
        .map(
          (item) => _ResolvedStateTransition(
            body,
            item.start,
            item.end,
            item.stateType,
            item.stateTypeSymbol,
            item.field,
            item.value,
          ),
        )
        .toList();
  }

  _ResolvedListenerTransition? _uniqueListenerTransition(
    String providerSymbol,
    _ResolvedStateTransition state,
  ) {
    final found = <_ResolvedListenerTransition>[];
    for (final unit in units.values) {
      final registrations = _ListenRegistrationVisitor(providerSymbol);
      unit.accept(registrations);
      for (final symbol in registrations.callbackSymbols) {
        final body = _executables[symbol];
        if (body == null) continue;
        final conditions = _ListenerConditionVisitor(
          body.body.start,
          body.body.start + body.body.text.length,
          state.stateTypeSymbol,
          state.field,
        );
        body.unit.accept(conditions);
        for (final statement in conditions.statements) {
          final navigation = _ResolvedNavigationVisitor(
            statement.thenStatement.offset,
            statement.thenStatement.end,
            _routeConstants,
            _destinationRoutes,
          );
          body.unit.accept(navigation);
          if (navigation.navigation.length == 1) {
            found.add(
              _ResolvedListenerTransition(
                body,
                statement.expression,
                navigation.navigation.single,
              ),
            );
          }
        }
      }
    }
    final unique = {
      for (final item in found)
        '${item.body.symbol}:${item.condition.offset}:${item.navigation.route}':
            item,
    };
    return unique.length == 1 ? unique.values.single : null;
  }
}

class _LocatedBody {
  final String path, source, symbol;
  final CompilationUnit unit;
  final _Body body;
  const _LocatedBody(this.path, this.source, this.unit, this.symbol, this.body);
}

class _RouteConstant {
  final String path, source, symbol, value;
  final int start, end;
  final Element element;
  const _RouteConstant(
    this.path,
    this.source,
    this.symbol,
    this.value,
    this.start,
    this.end,
    this.element,
  );
}

class _ResolvedRouteConstantVisitor extends RecursiveAstVisitor<void> {
  final String path, source;
  final List<_RouteConstant> routes = [];
  _ResolvedRouteConstantVisitor(this.path, this.source);

  @override
  void visitVariableDeclaration(VariableDeclaration node) {
    final value = node.initializer;
    final parent = node.parent;
    final element = node.declaredFragment?.element;
    if (value is SimpleStringLiteral &&
        value.value.startsWith('/') &&
        parent is VariableDeclarationList &&
        parent.keyword?.lexeme == 'const' &&
        element != null) {
      routes.add(
        _RouteConstant(
          path,
          source,
          _canonicalSymbol(element),
          value.value,
          node.offset,
          node.end,
          element,
        ),
      );
    }
    super.visitVariableDeclaration(node);
  }
}

class _DestinationResolverVisitor extends RecursiveAstVisitor<void> {
  final Map<String, _RouteConstant> constants;
  final Map<String, _RouteConstant> routes = {};
  _DestinationResolverVisitor(this.constants);

  @override
  void visitSwitchExpressionCase(SwitchExpressionCase node) {
    final destination = _patternTypeElement(node.guardedPattern.pattern);
    final route = _routeForExpression(node.expression, constants);
    if (destination != null && route != null) {
      routes[_canonicalSymbol(destination)] = route;
    }
    super.visitSwitchExpressionCase(node);
  }
}

Element? _patternTypeElement(DartPattern pattern) {
  if (pattern is ObjectPattern) return pattern.type.element;
  if (pattern is DeclaredVariablePattern && pattern.type is NamedType) {
    return (pattern.type as NamedType).element;
  }
  if (pattern is ConstantPattern &&
      pattern.expression is InstanceCreationExpression) {
    return (pattern.expression as InstanceCreationExpression)
        .constructorName
        .element
        ?.enclosingElement;
  }
  return null;
}

_RouteConstant? _routeForExpression(
  Expression expression,
  Map<String, _RouteConstant> constants,
) {
  final element = _expressionElement(expression);
  return element == null ? null : constants[_canonicalSymbol(element)];
}

Element? _expressionElement(Expression expression) {
  if (expression is SimpleIdentifier) return expression.element;
  if (expression is PrefixedIdentifier) return expression.identifier.element;
  if (expression is PropertyAccess) return expression.propertyName.element;
  return null;
}

class _ResolvedNavigation {
  final int start, end;
  final String route, proof, fingerprint;
  final String? symbolId;
  const _ResolvedNavigation(
    this.start,
    this.end,
    this.route,
    this.proof,
    this.fingerprint,
    this.symbolId,
  );
}

class _ResolvedNavigationVisitor extends RecursiveAstVisitor<void> {
  final int start, end;
  final Map<String, _RouteConstant> constants;
  final Map<String, _RouteConstant> destinationRoutes;
  final List<_ResolvedNavigation> navigation = [];
  _ResolvedNavigationVisitor(
    this.start,
    this.end,
    this.constants,
    this.destinationRoutes,
  );

  @override
  void visitMethodInvocation(MethodInvocation node) {
    if (node.offset < start || node.end > end) {
      super.visitMethodInvocation(node);
      return;
    }
    final method = node.methodName.name;
    if ((method != 'go' && method != 'push') ||
        node.argumentList.arguments.isEmpty) {
      super.visitMethodInvocation(node);
      return;
    }
    final argument = node.argumentList.arguments.first;
    if (argument is SimpleStringLiteral && argument.value.startsWith('/')) {
      final target = node.target;
      final directContext =
          target is SimpleIdentifier && target.name == 'context';
      final staticGoRouter =
          target is MethodInvocation &&
          target.methodName.name == 'of' &&
          target.target is SimpleIdentifier &&
          (target.target as SimpleIdentifier).name == 'GoRouter';
      if (directContext || staticGoRouter) {
        navigation.add(
          _ResolvedNavigation(
            node.offset,
            node.end,
            argument.value,
            'framework_rule_v1',
            'go_router:ast_literal:${argument.value}',
            null,
          ),
        );
      }
    } else {
      _RouteConstant? route;
      String? semanticSymbol;
      final element = _expressionElement(argument);
      if (element != null) {
        semanticSymbol = _canonicalSymbol(element);
        route = constants[semanticSymbol];
      } else if (argument is InstanceCreationExpression) {
        final type = argument.constructorName.element?.enclosingElement;
        if (type != null) {
          semanticSymbol = _canonicalSymbol(type);
          route = destinationRoutes[semanticSymbol];
        }
      }
      if (route != null &&
          semanticSymbol != null &&
          node.methodName.element != null) {
        navigation.add(
          _ResolvedNavigation(
            node.offset,
            node.end,
            route.value,
            'resolved_ast',
            'resolved_ast:navigation:$semanticSymbol:${route.symbol}:${route.value}',
            semanticSymbol,
          ),
        );
      }
    }
    super.visitMethodInvocation(node);
  }
}

class _ResolvedDispatch {
  final int start, end;
  final Element provider, eventType;
  final String providerSymbol, eventTypeSymbol;
  const _ResolvedDispatch(
    this.start,
    this.end,
    this.provider,
    this.eventType,
    this.providerSymbol,
    this.eventTypeSymbol,
  );
}

class _DispatchVisitor extends RecursiveAstVisitor<void> {
  final int start, end;
  final List<_ResolvedDispatch> dispatches = [];
  _DispatchVisitor(this.start, this.end);

  @override
  void visitMethodInvocation(MethodInvocation node) {
    if (node.offset >= start &&
        node.end <= end &&
        node.methodName.name == 'dispatch' &&
        node.argumentList.arguments.length >= 2) {
      final provider = _expressionElement(node.argumentList.arguments.first);
      final event = node.argumentList.arguments[1];
      if (provider != null && event is InstanceCreationExpression) {
        final eventType = event.constructorName.element?.enclosingElement;
        if (eventType != null && node.methodName.element != null) {
          dispatches.add(
            _ResolvedDispatch(
              node.offset,
              node.end,
              provider,
              eventType,
              _canonicalSymbol(provider),
              _canonicalSymbol(eventType),
            ),
          );
        }
      }
    }
    super.visitMethodInvocation(node);
  }
}

class _EventCaseVisitor extends RecursiveAstVisitor<void> {
  final String eventTypeSymbol;
  final List<SwitchPatternCase> cases = [];
  _EventCaseVisitor(this.eventTypeSymbol);

  @override
  void visitSwitchPatternCase(SwitchPatternCase node) {
    final type = _patternTypeElement(node.guardedPattern.pattern);
    if (type != null && _canonicalSymbol(type) == eventTypeSymbol) {
      cases.add(node);
    }
    super.visitSwitchPatternCase(node);
  }
}

class _ResolvedCallVisitor extends RecursiveAstVisitor<void> {
  final int start, end;
  final List<String> calls = [];
  _ResolvedCallVisitor(this.start, this.end);
  @override
  void visitMethodInvocation(MethodInvocation node) {
    if (node.offset >= start && node.end <= end) {
      final element = node.methodName.element;
      if (element != null && !element.isSynthetic) {
        calls.add(_canonicalSymbol(element));
      }
    }
    super.visitMethodInvocation(node);
  }
}

class _StateValue {
  final int start, end;
  final Element stateType;
  final String stateTypeSymbol, field, value;
  const _StateValue(
    this.start,
    this.end,
    this.stateType,
    this.stateTypeSymbol,
    this.field,
    this.value,
  );
}

class _ResolvedStateAssignmentVisitor extends RecursiveAstVisitor<void> {
  final int start, end;
  final List<_StateValue> transitions = [];
  _ResolvedStateAssignmentVisitor(this.start, this.end);
  @override
  void visitAssignmentExpression(AssignmentExpression node) {
    if (node.offset >= start &&
        node.end <= end &&
        node.leftHandSide.toSource() == 'state') {
      final visitor = _NamedBooleanStateVisitor();
      node.rightHandSide.accept(visitor);
      if (visitor.values.length == 1) {
        final value = visitor.values.single;
        transitions.add(
          _StateValue(
            node.offset,
            node.end,
            value.stateType,
            _canonicalSymbol(value.stateType),
            value.field,
            value.value,
          ),
        );
      }
    }
    super.visitAssignmentExpression(node);
  }
}

class _NamedBooleanState {
  final Element stateType;
  final String field, value;
  const _NamedBooleanState(this.stateType, this.field, this.value);
}

class _NamedBooleanStateVisitor extends RecursiveAstVisitor<void> {
  final List<_NamedBooleanState> values = [];
  @override
  void visitNamedExpression(NamedExpression node) {
    final value = node.expression;
    final arguments = node.parent;
    final creation = arguments?.parent;
    if (value is BooleanLiteral && creation is InstanceCreationExpression) {
      final stateType = creation.constructorName.element?.enclosingElement;
      if (stateType != null) {
        values.add(
          _NamedBooleanState(
            stateType,
            node.name.label.name,
            value.value.toString(),
          ),
        );
      }
    }
    super.visitNamedExpression(node);
  }
}

class _ResolvedStateTransition {
  final _LocatedBody body;
  final int start, end;
  final Element stateType;
  final String stateTypeSymbol, field, value;
  const _ResolvedStateTransition(
    this.body,
    this.start,
    this.end,
    this.stateType,
    this.stateTypeSymbol,
    this.field,
    this.value,
  );
}

class _ListenRegistrationVisitor extends RecursiveAstVisitor<void> {
  final String providerSymbol;
  final List<String> callbackSymbols = [];
  _ListenRegistrationVisitor(this.providerSymbol);
  @override
  void visitMethodInvocation(MethodInvocation node) {
    if (node.methodName.name == 'listen' &&
        node.argumentList.arguments.length >= 2) {
      final provider = _expressionElement(node.argumentList.arguments.first);
      final callback = _expressionElement(node.argumentList.arguments[1]);
      if (provider != null &&
          callback is ExecutableElement &&
          _canonicalSymbol(provider) == providerSymbol) {
        callbackSymbols.add(_canonicalSymbol(callback));
      }
    }
    super.visitMethodInvocation(node);
  }
}

class _ListenerConditionVisitor extends RecursiveAstVisitor<void> {
  final int start, end;
  final String stateTypeSymbol, field;
  final List<IfStatement> statements = [];
  _ListenerConditionVisitor(
    this.start,
    this.end,
    this.stateTypeSymbol,
    this.field,
  );
  @override
  void visitIfStatement(IfStatement node) {
    if (node.offset >= start &&
        node.end <= end &&
        _conditionReadsStateField(node.expression, stateTypeSymbol, field)) {
      statements.add(node);
    }
    super.visitIfStatement(node);
  }
}

bool _conditionReadsStateField(
  Expression condition,
  String stateTypeSymbol,
  String field,
) {
  final visitor = _ResolvedMemberVisitor(field, stateTypeSymbol);
  condition.accept(visitor);
  return visitor.found;
}

class _ResolvedMemberVisitor extends RecursiveAstVisitor<void> {
  final String field, ownerSymbol;
  bool found = false;
  _ResolvedMemberVisitor(this.field, this.ownerSymbol);
  void _check(SimpleIdentifier identifier) {
    final element = identifier.element;
    final owner = element?.enclosingElement;
    if (identifier.name == field &&
        owner != null &&
        _canonicalSymbol(owner) == ownerSymbol) {
      found = true;
    }
  }

  @override
  void visitSimpleIdentifier(SimpleIdentifier node) {
    _check(node);
    super.visitSimpleIdentifier(node);
  }
}

class _TerminalGuard {
  final Expression condition;
  final int start, end;
  const _TerminalGuard(this.condition, this.start, this.end);
}

_TerminalGuard? _lastTerminalGuard(
  CompilationUnit unit,
  _Body body,
  int before,
) {
  final visitor = _TerminalGuardVisitor(body.start, before);
  unit.accept(visitor);
  if (visitor.guards.isEmpty) return null;
  visitor.guards.sort((a, b) => a.start.compareTo(b.start));
  return visitor.guards.last;
}

class _TerminalGuardVisitor extends RecursiveAstVisitor<void> {
  final int start, end;
  final List<_TerminalGuard> guards = [];
  _TerminalGuardVisitor(this.start, this.end);
  @override
  void visitIfStatement(IfStatement node) {
    if (node.offset >= start &&
        node.end <= end &&
        _returnsImmediately(node.thenStatement)) {
      guards.add(_TerminalGuard(node.expression, node.offset, node.end));
    }
    super.visitIfStatement(node);
  }
}

bool _returnsImmediately(Statement statement) {
  if (statement is ReturnStatement) return true;
  return statement is Block &&
      statement.statements.length == 1 &&
      statement.statements.single is ReturnStatement;
}

class _ResolvedListenerTransition {
  final _LocatedBody body;
  final Expression condition;
  final _ResolvedNavigation navigation;
  const _ResolvedListenerTransition(this.body, this.condition, this.navigation);
}

class _ResolvedEventChain {
  final _ResolvedDispatch dispatch;
  final _ResolvedStateTransition state;
  final _ResolvedListenerTransition listener;
  final _TerminalGuard guard;
  const _ResolvedEventChain(
    this.dispatch,
    this.state,
    this.listener,
    this.guard,
  );
}

class _ExternalContract {
  final String path, source, result;
  final int start, end;
  _ExternalContract(this.path, this.source, this.start, this.end, this.result);
}

// Contracts are local, versioned evidence. The adapter never contacts an API:
// it can only expose the result that a current contract explicitly declares.
Future<Map<String, _ExternalContract>> _externalContracts(String root) async {
  final file = File(
    '$root${Platform.pathSeparator}codeflow.external-contracts.json',
  );
  if (!await file.exists()) return {};
  final source = utf8.decode(await file.readAsBytes());
  dynamic decoded;
  try {
    decoded = jsonDecode(source);
  } catch (_) {
    return {};
  }
  if (decoded is! Map ||
      decoded['version'] != '1' ||
      decoded['external'] is! Map) {
    return {};
  }
  final contracts = <String, _ExternalContract>{};
  for (final entry in (decoded['external'] as Map).entries) {
    if (entry.key is! String) continue;
    final key = entry.key as String;
    final value = entry.value;
    final result = value is String
        ? value
        : value is Map && value['result'] is String
        ? value['result'] as String
        : null;
    if (result == null) continue;
    final start = source.indexOf(key);
    if (start < 0) continue;
    contracts[key] = _ExternalContract(
      'codeflow.external-contracts.json',
      source,
      start,
      start + key.length,
      result,
    );
  }
  return contracts;
}

List<Map<String, Object>> _boundaryFacts(
  String source,
  String path,
  String caller,
  _Body body,
  Map<String, _ExternalContract> contracts,
) {
  final facts = <Map<String, Object>>[];
  final repositories = RegExp(
    r'\b([A-Za-z_]\w*(?:Repository|Store|Dao))\s*\.\s*([A-Za-z_]\w*)\s*\(',
  ).allMatches(body.text);
  for (final match in repositories) {
    final target = 'repository:${match.group(1)!}.${match.group(2)!}';
    facts.add(
      _fact(
        'repository_access',
        caller,
        target,
        path,
        source,
        body.start + match.start,
        body.start + match.end,
        'dart:$target',
      ),
    );
  }
  final externals = RegExp(
    r'\b([A-Za-z_]\w*(?:Api|Client|Gateway))\s*\.\s*([A-Za-z_]\w*)\s*\(',
  ).allMatches(body.text);
  for (final match in externals) {
    final key = '${match.group(1)!}.${match.group(2)!}';
    final target = 'external:$key';
    final start = body.start + match.start;
    facts.add(
      _fact(
        'external_call',
        caller,
        target,
        path,
        source,
        start,
        body.start + match.end,
        'dart:$target',
      ),
    );
    final contract = contracts[key];
    if (contract == null) {
      facts.add(
        _fact(
          'external_boundary_unknown',
          target,
          'EXTERNAL_BOUNDARY_UNKNOWN',
          path,
          source,
          start,
          body.start + match.end,
          'external:missing-contract:$key',
        ),
      );
    } else {
      facts.add(
        _fact(
          'external_result',
          target,
          contract.result,
          contract.path,
          contract.source,
          contract.start,
          contract.end,
          'external:contract:$key',
          proof: 'contract_v1',
        ),
      );
    }
  }
  return facts;
}

// A condition becomes a branch candidate only when a real `if` expression is
// present. A dispatch is marked unknown only for a receiver declared `dynamic`;
// ordinary method-looking text is never guessed into a call target.
List<Map<String, Object>> _controlFacts(
  String owner,
  String path,
  String source,
  _Body body,
) {
  final facts = <Map<String, Object>>[];
  final condition = _firstIfCondition(source, body);
  if (condition == null) return facts;
  final normalized = condition.expression.replaceAll(RegExp(r'\s+'), '');
  facts.add(
    _fact(
      'condition',
      owner,
      condition.expression,
      path,
      source,
      condition.start,
      condition.end,
      'dart:condition:$owner:$normalized',
    ),
  );
  final declaration = RegExp(
    r'\bdynamic\s+([A-Za-z_]\w*)',
  ).firstMatch(body.text);
  if (declaration == null) return facts;
  final variable = declaration.group(1)!;
  final dispatch = RegExp(
    '\\b${RegExp.escape(variable)}\\s*\\.\\s*[A-Za-z_]\\w*\\s*\\(',
  ).firstMatch(body.text);
  if (dispatch != null) {
    final start = body.start + dispatch.start;
    facts.add(
      _fact(
        'dynamic_dispatch',
        owner,
        '',
        path,
        source,
        start,
        body.start + dispatch.end,
        'dart:dynamic_dispatch:$owner:$variable',
      ),
    );
  }
  return facts;
}

// Riverpod support is intentionally structural: a direct `ref.read/watch` of
// a declared Notifier provider followed by a direct notifier method with a
// direct `state =` assignment. Anything less direct remains an unknown fact
// at the dependency's source position instead of being inferred.
Map<String, String> _providerNotifiers(String source) {
  final providers = <String, String>{};
  final expression = RegExp(
    r'final\s+(\w+)\s*=\s*(?:NotifierProvider|AsyncNotifierProvider)(?:\.family)?\s*<\s*(\w+)',
    multiLine: true,
  );
  for (final m in expression.allMatches(source)) {
    providers[m.group(1)!] = m.group(2)!;
  }
  return providers;
}

List<Map<String, Object>> _riverpodFacts(
  String source,
  String path,
  String owner,
  String actionSymbol,
  _Body body,
) {
  final facts = <Map<String, Object>>[];
  final providers = _providerNotifiers(source);
  final reads = RegExp(
    r'ref\s*\.\s*(read|watch)\s*\(\s*(\w+)(\.notifier)?\s*\)',
  ).allMatches(body.text);
  for (final read in reads) {
    final provider = read.group(2)!;
    final pos = body.start + read.start;
    final dependency = 'provider:$provider';
    facts.add(
      _fact(
        'provider_dependency',
        actionSymbol,
        dependency,
        path,
        source,
        pos,
        body.start + read.end,
        'riverpod:${read.group(1)}:$provider',
      ),
    );
    final operation = RegExp(
      '${RegExp.escape(read.group(0)!)}\\s*\\.\\s*(\\w+)\\s*\\(',
    ).firstMatch(body.text.substring(read.start));
    final notifier = providers[provider];
    // Reading a provider value is already a complete dependency fact. It does
    // not imply that this action mutates state, so do not manufacture an
    // unknown state transition for a plain `ref.read/watch(provider)`.
    if (read.group(3) == null) continue;
    if (operation == null || notifier == null) {
      facts.add(
        _fact(
          'unknown_state',
          actionSymbol,
          'riverpod:$provider',
          path,
          source,
          pos,
          body.start + read.end,
          'riverpod:unsupported:$provider',
        ),
      );
      continue;
    }
    final method = operation.group(1)!;
    final operationStart = body.start + read.start + operation.start;
    final operationSymbol = '$notifier.$method';
    facts.add(
      _fact(
        'notifier_operation',
        dependency,
        operationSymbol,
        path,
        source,
        operationStart,
        body.start + read.start + operation.end,
        'riverpod:operation:$provider:$method',
      ),
    );
    final notifierBody = _notifierMethodBody(source, notifier, method);
    if (notifierBody == null ||
        (notifierBody.text.contains('try') &&
            notifierBody.text.contains('catch'))) {
      facts.add(
        _fact(
          'unknown_state',
          operationSymbol,
          'state:$notifier',
          path,
          source,
          operationStart,
          body.start + read.start + operation.end,
          'riverpod:unsupported-transition:$notifier:$method',
        ),
      );
      continue;
    }
    final assignments = _stateAssignments(source, notifierBody);
    if (assignments.isEmpty) {
      facts.add(
        _fact(
          'unknown_state',
          operationSymbol,
          'state:$notifier',
          path,
          source,
          operationStart,
          body.start + read.start + operation.end,
          'riverpod:no-direct-state:$notifier:$method',
        ),
      );
      continue;
    }
    for (final assignment in assignments) {
      final start = assignment.start;
      final value = assignment.value.replaceAll(RegExp(r'\s+'), ' ').trim();
      facts.add(
        _fact(
          'state_transition',
          operationSymbol,
          'state:$notifier:$value',
          path,
          source,
          start,
          assignment.end,
          'riverpod:state:$notifier:$method:$value',
        ),
      );
    }
  }
  return facts;
}

class _Body {
  final int start;
  final String text;
  _Body(this.start, this.text);
}

class _ResolvedCallback {
  final String name, symbol, owner, trigger;
  final String? label;
  final int start, end;
  final _Body body;
  _ResolvedCallback(
    this.name,
    this.symbol,
    this.owner,
    this.trigger,
    this.label,
    this.start,
    this.end,
    this.body,
  );
}

class _ResolvedSystemMethod {
  final String symbol, owner;
  final int start, end;
  final _Body body;
  _ResolvedSystemMethod(
    this.symbol,
    this.owner,
    this.start,
    this.end,
    this.body,
  );
}

List<_ResolvedSystemMethod> _namedMethods(
  CompilationUnit unit,
  String source,
  String owner,
  String name,
) {
  final matches = <_ResolvedSystemMethod>[];
  for (final declaration in unit.declarations) {
    if (declaration is ClassDeclaration && declaration.name.lexeme == owner) {
      for (final method in declaration.members.whereType<MethodDeclaration>()) {
        if (method.name.lexeme != name) continue;
        final element = method.declaredFragment?.element;
        if (element == null) continue;
        matches.add(
          _ResolvedSystemMethod(
            _canonicalSymbol(element),
            _displayOwner(element),
            method.offset,
            method.end,
            _functionBody(source, method.body),
          ),
        );
      }
    } else if (owner == 'top-level' &&
        declaration is FunctionDeclaration &&
        declaration.name.lexeme == name) {
      final element = declaration.declaredFragment?.element;
      if (element == null) continue;
      matches.add(
        _ResolvedSystemMethod(
          _canonicalSymbol(element),
          _displayOwner(element),
          declaration.offset,
          declaration.end,
          _functionBody(source, declaration.functionExpression.body),
        ),
      );
    }
  }
  return matches;
}

// This identity contains semantic scope, not display prose or source
// coordinates. Equal method names in different packages/classes are distinct.
String _canonicalSymbol(Element element) {
  // A variable declaration and a resolved read of that variable are exposed
  // by Analyzer as a PropertyInducingElement and its synthetic getter. Treat
  // both as one semantic identity so cross-file const/provider links join.
  final semantic = element is PropertyAccessorElement
      ? element.variable
      : element;
  final base = semantic.baseElement;
  final library = base.library?.uri.toString() ?? 'unresolved-library';
  final owner = base.enclosingElement;
  final ownerPart = owner == null
      ? 'top-level'
      : '${owner.kind.displayName}:${owner.displayName}';
  return '$library::$ownerPart::${base.kind.displayName}:${base.displayName}';
}

String _displayOwner(Element element) {
  final owner = element.baseElement.enclosingElement?.displayName;
  return owner == null || owner.isEmpty ? 'top_level' : owner;
}

ExecutableElement? _callbackElement(Expression expression) {
  Expression candidate = expression;
  if (candidate is ConditionalExpression) {
    if (candidate.thenExpression is NullLiteral &&
        candidate.elseExpression is SimpleIdentifier) {
      candidate = candidate.elseExpression;
    } else if (candidate.elseExpression is NullLiteral &&
        candidate.thenExpression is SimpleIdentifier) {
      candidate = candidate.thenExpression;
    } else {
      return null;
    }
  }
  if (candidate is! SimpleIdentifier) return null;
  final element = candidate.element;
  if (element is! ExecutableElement || element.isSynthetic) return null;
  return element.baseElement;
}

_Body? _resolvedMethodBody(
  CompilationUnit unit,
  ExecutableElement element,
  String source,
) {
  final wanted = element.baseElement;
  for (final declaration in unit.declarations) {
    if (declaration is ClassDeclaration) {
      for (final member in declaration.members.whereType<MethodDeclaration>()) {
        final declared = member.declaredFragment?.element;
        if (declared != null && declared.baseElement.id == wanted.id) {
          return _functionBody(source, member.body);
        }
      }
    } else if (declaration is FunctionDeclaration) {
      final declared = declaration.declaredFragment?.element;
      if (declared != null && declared.baseElement.id == wanted.id) {
        return _functionBody(source, declaration.functionExpression.body);
      }
    }
  }
  return null;
}

List<_ResolvedCallback> _resolvedCallbacks(
  CompilationUnit unit,
  String source,
) {
  final visitor = _OnPressedVisitor();
  unit.accept(visitor);
  final callbacks = <_ResolvedCallback>[];
  for (final named in visitor.arguments) {
    final element = _callbackElement(named.expression);
    if (element == null) continue;
    final body = _resolvedMethodBody(unit, element, source);
    if (body == null) continue;
    callbacks.add(
      _ResolvedCallback(
        element.displayName,
        _canonicalSymbol(element),
        _displayOwner(element),
        named.name.label.name,
        _interactionLabel(named),
        named.offset,
        named.end,
        body,
      ),
    );
  }
  return callbacks;
}

// A button's visible copy is useful domain evidence, but only when it is a
// direct source literal owned by the same widget as the resolved callback.
// Dynamic strings, inherited labels, and arbitrary nearby text are omitted so
// reader-facing scenario names never pretend to be confirmed UI copy.
String? _interactionLabel(NamedExpression callback) {
  AstNode? node = callback.parent;
  for (var depth = 0; node != null && depth < 4; depth++, node = node.parent) {
    if (node is! InstanceCreationExpression) continue;
    final label = _staticWidgetLabel(node);
    if (label != null) return label;
  }
  return null;
}

String? _staticWidgetLabel(InstanceCreationExpression widget) {
  for (final argument in widget.argumentList.arguments) {
    if (argument is! NamedExpression) continue;
    final name = argument.name.label.name;
    if (const {'semanticLabel', 'tooltip', 'label'}.contains(name)) {
      final value = _staticText(argument.expression, 0);
      if (value != null) return value;
    }
  }
  for (final argument in widget.argumentList.arguments) {
    if (argument is NamedExpression && argument.name.label.name == 'child') {
      final value = _staticText(argument.expression, 0);
      if (value != null) return value;
    }
  }
  return null;
}

String? _staticText(Expression expression, int depth) {
  if (expression is SimpleStringLiteral) {
    final value = expression.value.trim();
    return value.isEmpty ? null : value;
  }
  if (depth >= 3 || expression is! InstanceCreationExpression) return null;
  for (final argument in expression.argumentList.arguments) {
    if (argument is NamedExpression) {
      if (!const {
        'data',
        'text',
        'label',
        'child',
        'semanticLabel',
      }.contains(argument.name.label.name)) {
        continue;
      }
      final value = _staticText(argument.expression, depth + 1);
      if (value != null) return value;
      continue;
    }
    final value = _staticText(argument, depth + 1);
    if (value != null) return value;
  }
  return null;
}

class _OnPressedVisitor extends RecursiveAstVisitor<void> {
  final List<NamedExpression> arguments = [];
  @override
  void visitNamedExpression(NamedExpression node) {
    if (const {
      'onPressed',
      'onTap',
      'onLongPress',
      'onChanged',
      'onSubmitted',
    }.contains(node.name.label.name)) {
      arguments.add(node);
    }
    super.visitNamedExpression(node);
  }
}

// A provider may expose a method name shared by another Notifier. Scope the
// lookup to its declared class so a state fact can never be borrowed from a
// different provider merely because the method text happens to match.
_Body? _notifierMethodBody(String source, String className, String method) {
  final unit = parseString(content: source, throwIfDiagnostics: false).unit;
  for (final declaration in unit.declarations.whereType<ClassDeclaration>()) {
    if (declaration.name.lexeme != className) continue;
    for (final member in declaration.members.whereType<MethodDeclaration>()) {
      if (member.name.lexeme == method)
        return _functionBody(source, member.body);
    }
  }
  return null;
}

_Body _functionBody(String source, FunctionBody body) {
  if (body is BlockFunctionBody) {
    final start = body.block.leftBracket.end;
    final end = body.block.rightBracket.offset;
    return _Body(start, source.substring(start, end));
  }
  final start = body.offset;
  final end = body.end;
  return _Body(start, source.substring(start, end));
}

class _CallSite {
  final String name;
  final String? symbol;
  final ExecutableElement? element;
  final int start, end;
  _CallSite(this.name, this.start, this.end, [this.symbol, this.element]);
}

class _DirectCallVisitor extends RecursiveAstVisitor<void> {
  final int start, end;
  final List<_CallSite> calls = [];
  _DirectCallVisitor(this.start, this.end);
  @override
  void visitMethodInvocation(MethodInvocation node) {
    if (node.offset >= start && node.end <= end && node.target == null) {
      final element = node.methodName.element;
      // A same-looking identifier is not a call edge unless analyzer resolved
      // it to an executable declaration.
      if (element != null && !element.isSynthetic) {
        calls.add(
          _CallSite(
            node.methodName.name,
            node.offset,
            node.end,
            _canonicalSymbol(element),
            element.baseElement as ExecutableElement,
          ),
        );
      }
    }
    super.visitMethodInvocation(node);
  }
}

List<_CallSite> _directMethodCalls(
  String source,
  _Body body, {
  CompilationUnit? resolvedUnit,
}) {
  final unit =
      resolvedUnit ??
      parseString(content: source, throwIfDiagnostics: false).unit;
  final visitor = _DirectCallVisitor(body.start, body.start + body.text.length);
  unit.accept(visitor);
  return visitor.calls;
}

class _IfCondition {
  final String expression;
  final int start, end;
  _IfCondition(this.expression, this.start, this.end);
}

class _IfVisitor extends RecursiveAstVisitor<void> {
  final int start, end;
  final List<IfStatement> statements = [];
  _IfVisitor(this.start, this.end);
  @override
  void visitIfStatement(IfStatement node) {
    if (node.offset >= start && node.end <= end) statements.add(node);
    super.visitIfStatement(node);
  }
}

_IfCondition? _firstIfCondition(String source, _Body body) {
  final unit = parseString(content: source, throwIfDiagnostics: false).unit;
  final visitor = _IfVisitor(body.start, body.start + body.text.length);
  unit.accept(visitor);
  if (visitor.statements.isEmpty) return null;
  final expression = visitor.statements.first.expression;
  return _IfCondition(expression.toSource(), expression.offset, expression.end);
}

class _StateAssignment {
  final String value;
  final int start, end;
  _StateAssignment(this.value, this.start, this.end);
}

class _AssignmentVisitor extends RecursiveAstVisitor<void> {
  final int start, end;
  final List<_StateAssignment> assignments = [];
  _AssignmentVisitor(this.start, this.end);
  @override
  void visitAssignmentExpression(AssignmentExpression node) {
    if (node.offset >= start &&
        node.end <= end &&
        node.leftHandSide.toSource() == 'state') {
      assignments.add(
        _StateAssignment(node.rightHandSide.toSource(), node.offset, node.end),
      );
    }
    super.visitAssignmentExpression(node);
  }
}

List<_StateAssignment> _stateAssignments(String source, _Body body) {
  final unit = parseString(content: source, throwIfDiagnostics: false).unit;
  final visitor = _AssignmentVisitor(body.start, body.start + body.text.length);
  unit.accept(visitor);
  return visitor.assignments;
}

Map<String, Object> _fact(
  String kind,
  String subject,
  String object,
  String path,
  String source,
  int start,
  int end,
  String fp, {
  String proof = 'framework_rule_v1',
  String? symbolId,
}) => {
  'kind': kind,
  'subject': subject,
  'object': object,
  'proof': proof,
  if (symbolId != null) 'symbol_id': symbolId,
  'anchor': {
    'path': path,
    'line_start': '\n'.allMatches(source.substring(0, start)).length + 1,
    'line_end': '\n'.allMatches(source.substring(0, end)).length + 1,
    'byte_start': utf8.encode(source.substring(0, start)).length,
    'byte_end': utf8.encode(source.substring(0, end)).length,
    'semantic_fingerprint': fp,
  },
};
void _result(Object? id, Object result) =>
    stdout.writeln(jsonEncode({'jsonrpc': '2.0', 'id': id, 'result': result}));
void _error(Object? id, String code, String message) => stdout.writeln(
  jsonEncode({
    'jsonrpc': '2.0',
    'id': id,
    'error': {'code': code, 'message': message},
  }),
);

Future<List<Map<String, Object>>> _discover(String root) async {
  final directory = Directory(root);
  if (!await directory.exists()) return [];
  // Baseline compilation intentionally materializes a read-only source mirror
  // below `.codeflow/cache`. Exclude that cache only while discovering from a
  // product root; when it is the requested root, it is the exact source tree.
  final codeflowSegment =
      '${Platform.pathSeparator}.codeflow${Platform.pathSeparator}';
  final rootIsCodeflowMirror = root.contains(codeflowSegment);
  final entries = <Map<String, Object>>[];
  await for (final entity in directory.list(
    recursive: true,
    followLinks: false,
  )) {
    if (entity is! File ||
        !entity.path.endsWith('.dart') ||
        entity.path.endsWith('.g.dart') ||
        entity.path.endsWith('.freezed.dart') ||
        entity.path.contains(
          '${Platform.pathSeparator}.dart_tool${Platform.pathSeparator}',
        ) ||
        entity.path.contains(
          '${Platform.pathSeparator}build${Platform.pathSeparator}',
        ) ||
        (!rootIsCodeflowMirror && entity.path.contains(codeflowSegment)) ||
        entity.path.contains(
          '${Platform.pathSeparator}.git${Platform.pathSeparator}',
        ))
      continue;
    final relative = entity.path
        .substring(root.length + 1)
        .replaceAll('\\', '/');
    if (relative.startsWith('test/') || relative.contains('/test/')) continue;
    final raw = await entity.readAsBytes();
    final source = utf8.decode(raw);
    // Route discovery uses the Dart parser. System discovery is limited to
    // exact lifecycle overrides and concrete session/FCM `.listen(method)`
    // subscriptions, so unrelated methods never become entry points.
    if (source.contains('GoRoute'))
      for (final declaration in _routeDeclarations(source)) {
        final route = declaration.path;
        final prefix = source.substring(0, declaration.start);
        final fragment = source.substring(declaration.start, declaration.end);
        final segments = route.split('/').where((value) => value.isNotEmpty);
        final alias = segments.isEmpty ? 'root' : segments.last;
        entries.add({
          'flow_id': 'route:$route',
          'alias': alias,
          'anchor': {
            'path': relative,
            'line_start': '\n'.allMatches(prefix).length + 1,
            'line_end':
                '\n'.allMatches(prefix).length +
                '\n'.allMatches(fragment).length +
                1,
            'byte_start': utf8.encode(prefix).length,
            'byte_end': utf8.encode(prefix + fragment).length,
            'semantic_fingerprint': 'go_router:GoRoute:path:$route',
          },
        });
      }
    for (final system in _systemEntries(source, relative)) {
      entries.add(system);
    }
  }
  entries.sort(
    (a, b) => (a['flow_id']! as String).compareTo(b['flow_id']! as String),
  );
  return entries;
}

List<Map<String, Object>> _systemEntries(String source, String relative) {
  final unit = parseString(content: source, throwIfDiagnostics: false).unit;
  final entries = <Map<String, Object>>[];
  void add(String kind, String owner, String name, AstNode node) {
    final prefix = source.substring(0, node.offset);
    final fragment = source.substring(node.offset, node.end);
    entries.add({
      'flow_id': 'system:$kind:$relative:$owner:$name',
      'alias': kind == 'push-token'
          ? '푸시 토큰 갱신'
          : kind == 'session'
          ? '세션 갱신'
          : '앱 생명주기',
      'anchor': {
        'path': relative,
        'line_start': '\n'.allMatches(prefix).length + 1,
        'line_end':
            '\n'.allMatches(prefix).length +
            '\n'.allMatches(fragment).length +
            1,
        'byte_start': utf8.encode(prefix).length,
        'byte_end': utf8.encode(prefix + fragment).length,
        'semantic_fingerprint': 'system_entry:$kind:$relative:$owner:$name',
      },
    });
  }

  for (final declaration in unit.declarations.whereType<ClassDeclaration>()) {
    if (!_isLifecycleOwner(declaration)) continue;
    for (final method in declaration.members.whereType<MethodDeclaration>()) {
      if (method.name.lexeme == 'initState' ||
          method.name.lexeme == 'didChangeAppLifecycleState') {
        add('lifecycle', declaration.name.lexeme, method.name.lexeme, method);
      }
    }
  }
  final subscriptions = _SystemSubscriptionVisitor();
  unit.accept(subscriptions);
  for (final subscription in subscriptions.subscriptions) {
    add(
      subscription.kind,
      subscription.owner,
      subscription.callback,
      subscription.node,
    );
  }
  final unique = <String, Map<String, Object>>{};
  for (final entry in entries) unique[entry['flow_id']! as String] = entry;
  return unique.values.toList();
}

bool _isLifecycleOwner(ClassDeclaration declaration) {
  final superclass = declaration.extendsClause?.superclass.toSource() ?? '';
  final normalized = superclass.replaceAll(RegExp(r'\s+'), '');
  if (normalized == 'State' ||
      normalized.startsWith('State<') ||
      normalized == 'ConsumerState' ||
      normalized.startsWith('ConsumerState<')) {
    return true;
  }
  return declaration.withClause?.mixinTypes.any(
        (type) =>
            type.toSource().replaceAll(RegExp(r'\s+'), '') ==
            'WidgetsBindingObserver',
      ) ??
      false;
}

class _SystemSubscription {
  final String kind, owner, callback;
  final MethodInvocation node;
  _SystemSubscription(this.kind, this.owner, this.callback, this.node);
}

class _SystemSubscriptionVisitor extends RecursiveAstVisitor<void> {
  final List<_SystemSubscription> subscriptions = [];
  @override
  void visitMethodInvocation(MethodInvocation node) {
    if (node.methodName.name == 'listen' &&
        node.argumentList.arguments.length == 1) {
      final callback = node.argumentList.arguments.single;
      if (callback is SimpleIdentifier) {
        final receiver = node.target?.toSource() ?? '';
        final owner =
            node.thisOrAncestorOfType<ClassDeclaration>()?.name.lexeme ??
            'top-level';
        if (receiver.contains('onTokenRefresh')) {
          subscriptions.add(
            _SystemSubscription('push-token', owner, callback.name, node),
          );
        } else if (receiver.toLowerCase().contains('session')) {
          subscriptions.add(
            _SystemSubscription('session', owner, callback.name, node),
          );
        }
      }
    }
    super.visitMethodInvocation(node);
  }
}

class _RouteDeclaration {
  final String path;
  final int start, end;
  final Set<String> builderOwners;
  _RouteDeclaration(this.path, this.start, this.end, this.builderOwners);
}

Set<String> _selectedRouteOwners(
  String flowId,
  Map<String, String> sources,
  Map<String, CompilationUnit> units,
) {
  final route = flowId.startsWith('route:') ? flowId.substring(6) : flowId;
  final owners = <String>{};
  for (final source in sources.values) {
    for (final declaration in _routeDeclarations(source)) {
      if (declaration.path == route && declaration.builderOwners.length == 1) {
        owners.addAll(declaration.builderOwners);
      }
    }
  }
  // StatefulWidget callbacks live on State, not on the route's Widget class.
  // The extends type is resolved as part of the same analyzer unit; we admit
  // only the state class whose generic widget argument names a selected owner.
  var changed = true;
  while (changed) {
    changed = false;
    for (final unit in units.values) {
      for (final declaration
          in unit.declarations.whereType<ClassDeclaration>()) {
        final superclass = declaration.extendsClause?.superclass;
        if (superclass == null) continue;
        final type = superclass.toSource().replaceAll(RegExp(r'\s+'), '');
        for (final owner in owners.toList()) {
          if ((type.startsWith('State<$owner>') ||
                  type.startsWith('ConsumerState<$owner>')) &&
              owners.add(declaration.name.lexeme)) {
            changed = true;
          }
        }
      }
    }
  }
  return owners;
}

class _ConstantStringVisitor extends RecursiveAstVisitor<void> {
  final Map<String, String> values = {};
  @override
  void visitVariableDeclaration(VariableDeclaration node) {
    final value = node.initializer;
    final parent = node.parent;
    if (value is StringLiteral &&
        value.stringValue?.startsWith('/') == true &&
        parent is VariableDeclarationList &&
        parent.keyword?.lexeme == 'const') {
      values[node.name.lexeme] = value.stringValue!;
    }
    super.visitVariableDeclaration(node);
  }
}

class _GoRouteVisitor extends RecursiveAstVisitor<void> {
  final Map<String, String> constants;
  final List<_RouteDeclaration> routes = [];
  _GoRouteVisitor(this.constants);
  @override
  void visitInstanceCreationExpression(InstanceCreationExpression node) {
    final type = node.constructorName.type.toSource();
    if (type == 'GoRoute' || type.endsWith('.GoRoute')) {
      _add(node.argumentList, node.offset, node.end);
    }
    super.visitInstanceCreationExpression(node);
  }

  @override
  void visitMethodInvocation(MethodInvocation node) {
    if (node.methodName.name == 'GoRoute') {
      _add(node.argumentList, node.offset, node.end);
    }
    super.visitMethodInvocation(node);
  }

  void _add(ArgumentList arguments, int start, int end) {
    String? path;
    final builderOwners = <String>{};
    for (final argument in arguments.arguments.whereType<NamedExpression>()) {
      if (argument.name.label.name == 'path') {
        final expression = argument.expression;
        if (expression is StringLiteral) {
          path = expression.stringValue;
        } else if (expression is SimpleIdentifier) {
          path = constants[expression.name];
        }
      } else if (argument.name.label.name == 'builder' ||
          argument.name.label.name == 'pageBuilder') {
        final visitor = _ConstructedTypeVisitor();
        argument.expression.accept(visitor);
        builderOwners.addAll(visitor.types);
      }
    }
    if (path?.startsWith('/') == true) {
      routes.add(_RouteDeclaration(path!, start, end, builderOwners));
    }
  }
}

class _ConstructedTypeVisitor extends RecursiveAstVisitor<void> {
  final Set<String> types = {};
  @override
  void visitInstanceCreationExpression(InstanceCreationExpression node) {
    types.add(node.constructorName.type.name.lexeme);
    super.visitInstanceCreationExpression(node);
  }
}

class _TypedGoRouteVisitor extends RecursiveAstVisitor<void> {
  final Map<String, String> constants;
  final Map<String, Set<String>> routeOwners;
  final List<_RouteDeclaration> routes = [];
  _TypedGoRouteVisitor(this.constants, this.routeOwners);

  @override
  void visitAnnotation(Annotation node) {
    if (node.name.name != 'TypedGoRoute') {
      super.visitAnnotation(node);
      return;
    }
    final typeArguments = node.typeArguments?.arguments;
    if (typeArguments == null || typeArguments.length != 1) {
      super.visitAnnotation(node);
      return;
    }
    String? path;
    for (final argument
        in node.arguments?.arguments.whereType<NamedExpression>() ??
            const <NamedExpression>[]) {
      if (argument.name.label.name != 'path') continue;
      final expression = argument.expression;
      if (expression is StringLiteral) {
        path = expression.stringValue;
      } else if (expression is SimpleIdentifier) {
        path = constants[expression.name];
      }
    }
    final routeClass = typeArguments.single.toSource();
    final owners = routeOwners[routeClass] ?? const <String>{};
    if (path?.startsWith('/') == true && owners.length == 1) {
      routes.add(
        _RouteDeclaration(path!, node.offset, node.end, Set.of(owners)),
      );
    }
    super.visitAnnotation(node);
  }
}

List<_RouteDeclaration> _routeDeclarations(String source) {
  final unit = parseString(content: source, throwIfDiagnostics: false).unit;
  final constants = _ConstantStringVisitor();
  unit.accept(constants);
  final routes = _GoRouteVisitor(constants.values);
  unit.accept(routes);
  final routeOwners = <String, Set<String>>{};
  for (final declaration in unit.declarations.whereType<ClassDeclaration>()) {
    final owners = <String>{};
    for (final member in declaration.members.whereType<MethodDeclaration>()) {
      if (member.name.lexeme != 'build') continue;
      final visitor = _ConstructedTypeVisitor();
      member.body.accept(visitor);
      owners.addAll(visitor.types);
    }
    if (owners.length == 1) routeOwners[declaration.name.lexeme] = owners;
  }
  final typedRoutes = _TypedGoRouteVisitor(constants.values, routeOwners);
  unit.accept(typedRoutes);
  return [...routes.routes, ...typedRoutes.routes];
}
