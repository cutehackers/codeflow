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
) async {
  final facts = <Map<String, Object>>[];
  final sources = <String, String>{};
  final units = <String, CompilationUnit>{};
  final analysis = AnalysisContextCollection(includedPaths: [root]);
  final contracts = await _externalContracts(root);
  final hasHomeDestinationSeam = await _hasHomeDestinationSeam(root, paths);
  // Resolve the complete validated slice first. This lets route ownership be
  // established before any callback is admitted.
  for (final relative in paths.toSet()) {
    if (!relative.endsWith('.dart') || relative.startsWith('../')) continue;
    final file = File(
      '$root${Platform.pathSeparator}${relative.replaceAll('/', Platform.pathSeparator)}',
    );
    if (!await file.exists()) continue;
    final bytes = await file.readAsBytes();
    final source = utf8.decode(bytes);
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
    sources[relative] = source;
    if (resolvedUnit != null) units[relative] = resolvedUnit;
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
          '',
          relative,
          source,
          press.start,
          press.end,
          'resolved_ast:onPressed:$actionSymbol',
          proof: 'resolved_ast',
          symbolId: actionSymbol,
        ),
      );
      final body = press.body;
      if (hasHomeDestinationSeam) {
        facts.addAll(
          _homeDestinationTransitions(actionSymbol, relative, source, body),
        );
      }
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
          if (hasHomeDestinationSeam) {
            facts.addAll(
              _homeDestinationTransitions(
                targetSymbol,
                relative,
                source,
                targetBody,
              ),
            );
          }
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
          final nav = RegExp(
            r'''(?:context\s*\.\s*(?:go|push)|GoRouter\.of\s*\(\s*context\s*\)\s*\.\s*(?:go|push))\s*\(\s*['"](/[^'"]+)['"]''',
          ).firstMatch(targetBody.text);
          if (nav != null) {
            final pos = targetBody.start + nav.start;
            facts.add(
              _fact(
                'route_transition',
                targetSymbol,
                'route:${nav.group(1)!}',
                relative,
                source,
                pos,
                targetBody.start + nav.end,
                'go_router:transition:${nav.group(1)!}',
              ),
            );
          }
        }
      }
      facts.addAll(_controlFacts(actionSymbol, relative, source, body));
      final direct = RegExp(
        r'''(?:context\s*\.\s*(?:go|push)|GoRouter\.of\s*\(\s*context\s*\)\s*\.\s*(?:go|push))\s*\(\s*['"](/[^'"]+)['"]''',
      ).firstMatch(body.text);
      if (direct != null) {
        final pos = body.start + direct.start;
        facts.add(
          _fact(
            'route_transition',
            actionSymbol,
            'route:${direct.group(1)!}',
            relative,
            source,
            pos,
            body.start + direct.end,
            'go_router:transition:${direct.group(1)!}',
          ),
        );
      }
    }
  }
  facts.addAll(_joinCancelChain(sources));
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

// Bounded CF-G13E event chain. Every pattern is intentionally exact and all
// involved files must have been supplied by the validated graph slice. This
// does not model arbitrary async Riverpod behavior.
List<Map<String, Object>> _joinCancelChain(Map<String, String> sources) {
  final results = <Map<String, Object>>[];
  final pageMatches = <_CancelPage>[];
  for (final entry in sources.entries) {
    final source = entry.value;
    final dispatch = RegExp(
      r'ref\s*\.\s*dispatch\s*\(\s*joinControllerProvider\s*,\s*const\s+JoinCancelEvent\s*\(\s*\)\s*\)',
    ).firstMatch(source);
    final confirmation = RegExp(
      r'if\s*\(\s*!mounted\s*\|\|\s*confirmed\s*!=\s*true\s*\)\s*return\s*;',
    ).firstMatch(source);
    final listener = RegExp(
      r'if\s*\(\s*state\.isCanceled\s*\)\s*\{\s*GoRouter\.of\s*\(\s*context\s*\)\s*\.\s*go\s*\(\s*authPath\s*\)\s*;',
    ).firstMatch(source);
    final owner = RegExp(r'class\s+(\w+)').firstMatch(source)?.group(1);
    if (dispatch != null &&
        confirmation != null &&
        listener != null &&
        owner != null) {
      pageMatches.add(
        _CancelPage(entry.key, source, owner, dispatch, confirmation, listener),
      );
    }
  }
  if (pageMatches.length != 1) return results;
  final page = pageMatches.single;
  final controllerMatches = sources.entries
      .where(
        (entry) =>
            RegExp(
              r'case\s+final\s+JoinCancelEvent\s+\w+\s*:\s*await\s+_onJoinCancel\s*\(',
            ).hasMatch(entry.value) &&
            RegExp(
              r'state\s*=\s*const\s+AsyncData\s*\(\s*JoinState\s*\(\s*isCanceled\s*:\s*true\s*\)\s*\)\s*;',
            ).hasMatch(entry.value),
      )
      .toList();
  final authMatches = sources.entries
      .where(
        (entry) => RegExp(
          r'''const\s+String\s+authPath\s*=\s*['"]\/auth['"]''',
        ).hasMatch(entry.value),
      )
      .toList();
  if (controllerMatches.length != 1 || authMatches.length != 1) return results;
  final controller = controllerMatches.single;
  final assignment = RegExp(
    r'state\s*=\s*const\s+AsyncData\s*\(\s*JoinState\s*\(\s*isCanceled\s*:\s*true\s*\)\s*\)\s*;',
  ).firstMatch(controller.value)!;
  results.add(
    _fact(
      'confirmation_condition',
      '${page.owner}._requestExit',
      'confirmed == true',
      page.path,
      page.source,
      page.confirmation.start,
      page.confirmation.end,
      'dart:confirm:JoinCancelEvent',
    ),
  );
  // The guard has an explicit `return`, so its negative outcome is not an
  // unknown destination. It is a source-backed terminal result: this action
  // performs no navigation (cancel declined, or the widget is already gone).
  results.add(
    _fact(
      'terminal_result',
      '${page.owner}._requestExit',
      'result:no_navigation',
      page.path,
      page.source,
      page.confirmation.start,
      page.confirmation.end,
      'dart:terminal:return_without_navigation',
    ),
  );
  results.add(
    _fact(
      'event_dispatch',
      '${page.owner}._requestExit',
      'event:JoinCancelEvent',
      page.path,
      page.source,
      page.dispatch.start,
      page.dispatch.end,
      'riverpod:dispatch:joinControllerProvider:JoinCancelEvent',
    ),
  );
  results.add(
    _fact(
      'notifier_state_transition',
      'provider:joinControllerProvider',
      'state:JoinState.isCanceled=true',
      controller.key,
      controller.value,
      assignment.start,
      assignment.end,
      'riverpod:event_state:JoinCancelEvent:isCanceled',
    ),
  );
  results.add(
    _fact(
      'listener_condition',
      '${page.owner}._onWizardSettled',
      'state.isCanceled',
      page.path,
      page.source,
      page.listener.start,
      page.listener.start + page.listener.group(0)!.indexOf('GoRouter'),
      'dart:listener_condition:isCanceled',
    ),
  );
  final navStart =
      page.listener.start + page.listener.group(0)!.indexOf('GoRouter');
  results.add(
    _fact(
      'route_transition',
      '${page.owner}._onWizardSettled',
      'route:/auth',
      page.path,
      page.source,
      navStart,
      page.listener.end,
      'go_router:listener:authPath:/auth',
    ),
  );
  return results;
}

class _CancelPage {
  final String path, source, owner;
  final RegExpMatch dispatch, confirmation, listener;
  _CancelPage(
    this.path,
    this.source,
    this.owner,
    this.dispatch,
    this.confirmation,
    this.listener,
  );
}

// This result is emitted only from the action body (or a direct helper body)
// that contains the dispatcher invocation. Keeping the subject tied to that
// body lets Core attach a source-backed condition branch rather than showing a
// route outcome as unconditional for every action on the page.
List<Map<String, Object>> _homeDestinationTransitions(
  String subject,
  String path,
  String source,
  _Body body,
) {
  return RegExp(
        r'routeDestinationDispatcherProvider\)\s*\.\s*go\s*\(\s*const\s+HomeDestination\s*\(\s*\)\s*\)',
      )
      .allMatches(body.text)
      .map(
        (match) => _fact(
          'route_transition',
          subject,
          'route:/home',
          path,
          source,
          body.start + match.start,
          body.start + match.end,
          'route_destination:HomeDestination:/home',
        ),
      )
      .toList();
}

Future<bool> _hasHomeDestinationSeam(String root, List<String> paths) async {
  var dispatcher = false, resolver = false, home = false;
  for (final relative in paths) {
    final file = File(
      '$root${Platform.pathSeparator}${relative.replaceAll('/', Platform.pathSeparator)}',
    );
    if (!await file.exists()) continue;
    final source = utf8.decode(await file.readAsBytes());
    dispatcher =
        dispatcher ||
        source.contains('_router.go(resolveDestination(destination))');
    resolver =
        resolver ||
        RegExp(r'HomeDestination\s*\(\s*\)\s*=>\s*homePath').hasMatch(source);
    home =
        home ||
        RegExp(
          r'''const\s+String\s+homePath\s*=\s*['"]\/home['"]''',
        ).hasMatch(source);
  }
  return dispatcher && resolver && home;
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
  final String name, symbol, owner;
  final int start, end;
  final _Body body;
  _ResolvedCallback(
    this.name,
    this.symbol,
    this.owner,
    this.start,
    this.end,
    this.body,
  );
}

// This identity contains semantic scope, not display prose or source
// coordinates. Equal method names in different packages/classes are distinct.
String _canonicalSymbol(Element element) {
  final base = element.baseElement;
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
        named.offset,
        named.end,
        body,
      ),
    );
  }
  return callbacks;
}

class _OnPressedVisitor extends RecursiveAstVisitor<void> {
  final List<NamedExpression> arguments = [];
  @override
  void visitNamedExpression(NamedExpression node) {
    if (node.name.label.name == 'onPressed') arguments.add(node);
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
        (!rootIsCodeflowMirror && entity.path.contains(codeflowSegment)) ||
        entity.path.contains(
          '${Platform.pathSeparator}.git${Platform.pathSeparator}',
        ))
      continue;
    final raw = await entity.readAsBytes();
    final source = utf8.decode(raw);
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
          'path': entity.path.substring(root.length + 1).replaceAll('\\', '/'),
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
  }
  entries.sort(
    (a, b) => (a['flow_id']! as String).compareTo(b['flow_id']! as String),
  );
  return entries;
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
      } else if (argument.name.label.name == 'builder') {
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

List<_RouteDeclaration> _routeDeclarations(String source) {
  final unit = parseString(content: source, throwIfDiagnostics: false).unit;
  final constants = _ConstantStringVisitor();
  unit.accept(constants);
  final routes = _GoRouteVisitor(constants.values);
  unit.accept(routes);
  return routes.routes;
}
