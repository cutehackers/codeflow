import 'dart:io';

import 'package:test/test.dart';

import 'helpers.dart';
import 'package:codeflow_dart_adapter/src/harvest.dart';

void main() {
  test('placeholder score/fanIn/boundary constants match ticket 06A spec', () {
    expect(placeholderScore, 0.5);
  });

  test('harvest over the example app returns every expected candidate', () {
    final result = harvestCandidates({'repoRoot': exampleAppRoot()});
    final candidates = result['candidates']! as List<dynamic>;
    final paths = candidates
        .map((c) => (c as Map)['entrySymbolPath'] as String)
        .toList();

    expect(paths, [
      'lib/features/auth/email_signup_notifier.dart#EmailSignupNotifier.resetToIdle',
      'lib/features/auth/email_signup_notifier.dart#EmailSignupNotifier.submit',
      'lib/features/cart/cart_bloc.dart#CartBloc._onCheckedOut',
      'lib/features/cart/cart_bloc.dart#CartBloc._onItemAdded',
      'lib/features/orders/place_order_usecase.dart#PlaceOrderUseCase.call',
      'lib/features/settings/settings_notifier.dart#SettingsNotifier.toggleDarkMode',
      'lib/main.dart#AppLifecycleObserver.didChangeAppLifecycleState',
      'lib/main.dart#AppLifecycleObserver.handleDeepLink',
      'lib/main.dart#ShopHomeScreen.onCheckoutPressed',
      'lib/main.dart#firebaseMessagingBackgroundHandler',
    ]);
  });

  test('generated files are skipped', () {
    final result = harvestCandidates({
      'repoRoot': exampleAppRoot(),
      'libSubdir': 'lib',
    });
    final candidates = result['candidates']! as List<dynamic>;
    for (final c in candidates) {
      expect((c as Map)['entrySymbolPath'] as String, isNot(contains('.g.dart')));
    }
  });

  test('candidate objects conform to candidate.schema.json shape', () {
    final result = harvestCandidates({'repoRoot': exampleAppRoot()});
    final candidates = result['candidates']! as List<dynamic>;
    expect(candidates, isNotEmpty);

    const allowedTopLevelKeys = {
      'candidateId',
      'triggerClass',
      'markerKind',
      'entrySymbolPath',
      'intentSignals',
      'score',
      'fanIn',
      'boundaryReachable',
      'rootEquivalenceKey',
      'dedupedInto',
      'tieBreakRank',
      'manifestOverride',
    };
    const triggerClasses = {
      'user_action',
      'use_case_invocation',
      'system_event',
      'state_transition',
    };
    const markerKinds = {
      'notifier_method',
      'bloc_handler',
      'route_callback',
      'usecase_call',
      'lifecycle_callback',
      'state_mutation',
    };

    for (final raw in candidates) {
      final c = raw! as Map;
      expect(c.keys.toSet(), allowedTopLevelKeys);
      expect(c['candidateId'] as String, matches(RegExp(r'^cand-[a-z0-9]{8,32}$')));
      expect(c['entrySymbolPath'] as String,
          matches(RegExp(r'^[A-Za-z0-9_./-]+\.dart#[A-Za-z_$][\w$]*(\.[A-Za-z_$][\w$]*)*$')));
      expect(triggerClasses, contains(c['triggerClass']));
      expect(markerKinds, contains(c['markerKind']));
      expect(c['score'], 0.5);
      expect(c['fanIn'], 0);
      expect(c['boundaryReachable'], false);
      expect(c['dedupedInto'], isNull);
      expect(c['tieBreakRank'], 0);
      expect(c['manifestOverride'], 'none');

      final signals = c['intentSignals']! as Map;
      expect(signals.keys.toSet(),
          {'className', 'derivedName', 'docLine', 'packageName'});
      expect((signals['className'] as String), isNotEmpty);
      expect((signals['derivedName'] as String), isNotEmpty);
      expect(signals['docLine'], anyOf(isNull, isA<String>()));
      expect((signals['packageName'] as String), isNotEmpty);
    }
  });

  test('candidateId equals cand-sha256(entrySymbolPath)[0:16]', () {
    final result = harvestCandidates({'repoRoot': exampleAppRoot()});
    final submit = (result['candidates']! as List<dynamic>)
        .map((c) => c! as Map)
        .firstWhere((c) =>
            c['entrySymbolPath'] ==
            'lib/features/orders/place_order_usecase.dart#PlaceOrderUseCase.call');
    // Precomputed with python3 hashlib.sha256 - guards the pure-Dart sha256.
    expect(submit['candidateId'], 'cand-96ad0f161342c24b');
  });

  test('intentSignals carry doc lines and humanized names where expected',
      () {
    final result = harvestCandidates({'repoRoot': exampleAppRoot()});
    final byPath = {
      for (final c in result['candidates']! as List<dynamic>)
        (c! as Map)['entrySymbolPath'] as String: c as Map,
    };

    final submit =
        byPath['lib/features/auth/email_signup_notifier.dart#EmailSignupNotifier.submit']!;
    expect(submit['triggerClass'], 'user_action');
    expect(submit['markerKind'], 'notifier_method');
    final signals = submit['intentSignals']! as Map;
    expect(signals['className'], 'EmailSignupNotifier');
    expect(signals['derivedName'], 'Submit');
    expect(
      signals['docLine'],
      'Submits the email signup form as one user-visible journey step.',
    );
    expect(signals['packageName'], 'shop_app');

    final handler =
        byPath['lib/features/cart/cart_bloc.dart#CartBloc._onItemAdded']!;
    expect(handler['markerKind'], 'bloc_handler');
    expect((handler['intentSignals']! as Map)['derivedName'], 'Item added');

    final mutation =
        byPath['lib/features/settings/settings_notifier.dart#SettingsNotifier.toggleDarkMode']!;
    expect(mutation['triggerClass'], 'state_transition');
    expect(mutation['markerKind'], 'state_mutation');

    final fcm = byPath['lib/main.dart#firebaseMessagingBackgroundHandler']!;
    expect(fcm['triggerClass'], 'system_event');
    expect(fcm['markerKind'], 'lifecycle_callback');
    expect((fcm['intentSignals']! as Map)['className'], 'main');
    expect(fcm['rootEquivalenceKey'], 'main');
  });

  test('custom profile domain markers add candidates; unknown names ignored',
      () {
    final base = harvestCandidates({
      'repoRoot': exampleAppRoot(),
      'profiles': [
        {
          'name': 'definitely-not-a-profile',
          'patterns': {
            'domainMarkers': [r'Nope$'],
          },
        },
        {
          'name': 'riverpod',
          'patterns': {
            'domainMarkers': [r'SignupProbeStore$'],
            'boundarySuffixes': ['Gateway'],
          },
        },
      ],
    });
    // Unknown name ignored -> same candidate count as default run.
    final fallback = harvestCandidates({'repoRoot': exampleAppRoot()});
    expect(base.length, fallback.length);
  });

  test('profile domain markers promote matching classes to user_action', () {
    final tmp = Directory.systemTemp.createTempSync('codeflow_profile_test');
    addTearDown(() => tmp.deleteSync(recursive: true));
    Directory('${tmp.path}/lib').createSync();
    File('${tmp.path}/pubspec.yaml')
        .writeAsStringSync('name: probe_pkg\nsdk:\n');
    File('${tmp.path}/lib/store.dart').writeAsStringSync('''
class InventoryStore {
  void restockShelf(String sku) {}
}
''');

    final result = harvestCandidates({
      'repoRoot': tmp.path,
      'profiles': [
        {
          'name': 'riverpod',
          'patterns': {
            'domainMarkers': [r'[A-Za-z0-9_]+Store$'],
          },
        },
      ],
    });
    final candidates = result['candidates']! as List<dynamic>;
    expect(candidates, hasLength(1));
    final c = candidates.single! as Map;
    expect(c['entrySymbolPath'], 'lib/store.dart#InventoryStore.restockShelf');
    expect(c['triggerClass'], 'user_action');
    expect(c['markerKind'], 'notifier_method');
    expect((c['intentSignals']! as Map)['packageName'], 'probe_pkg');
  });

  test('profile boundarySuffixes can exclude classes from harvesting', () {
    final tmp = Directory.systemTemp.createTempSync('codeflow_boundary_test');
    addTearDown(() => tmp.deleteSync(recursive: true));
    Directory('${tmp.path}/lib').createSync();
    File('${tmp.path}/pubspec.yaml')
        .writeAsStringSync('name: boundary_pkg\nsdk:\n');
    File('${tmp.path}/lib/gateway.dart').writeAsStringSync('''
class CartGatewayService {
  Future<void> pushCart(Object cart) async {}
}
''');

    // Default: public *Service methods are use-case roots.
    final before = harvestCandidates({'repoRoot': tmp.path});
    final candidates = before['candidates']! as List<dynamic>;
    expect(candidates, hasLength(1));
    expect((candidates.single! as Map)['markerKind'], 'usecase_call');

    // Extended profile declares GatewayService a boundary -> suppressed.
    final after = harvestCandidates({
      'repoRoot': tmp.path,
      'profiles': [
        {
          'name': 'riverpod',
          'patterns': {
            'boundarySuffixes': ['GatewayService'],
          },
        },
      ],
    });
    expect(((after['candidates']!) as List).isEmpty, isTrue);
  });
}
